package commands

import (
	"context"
	"flag"
	"fmt"
	"io"
	"runtime"
	"sync"
	"syscall"
	"time"

	"matrixos/vector/lib/cgroups"
	"matrixos/vector/lib/config"
	"matrixos/vector/lib/seeder"
	"matrixos/vector/lib/validation"
)

// newSeeder is the factory used to create an ISeeder.
// Tests replace it with a function that returns a mock.
var newSeeder = func(cfg config.IConfig, opts *seeder.NewSeederOptions) (seeder.ISeeder, error) {
	return seeder.NewSeeder(cfg, opts)
}

// SeedsCommand orchestrates the seeder workflow — detecting,
// preparing, and building chroot filesystems using the configured
// seeders.  It is the Go port of build/seeder.
type SeedsCommand struct {
	BaseCommand
	UI
	SignalGuard
	fs *flag.FlagSet

	// Library instances
	det seeder.ISeederDetector
	qa  *validation.QA

	// Flags
	chrootDir        string
	skipSeedersRaw   string
	onlySeedersRaw   string
	resume           bool
	builtRootfsFile  string
	builtSeedersFile string
	stage3File       string
	verbose          bool

	// Parsed from flags
	skipSeeders []string
	onlySeeders []string

	// Mutex for concurrent access to results
	mu sync.Mutex

	// Results populated during Run().
	BuiltSeeders []string

	// cgroupRoot overrides the cgroup v2 mount point (for testing).
	cgroupRoot string
}

// NewSeedsCommand creates a new SeedsCommand.
func NewSeedsCommand() *SeedsCommand {
	return &SeedsCommand{}
}

func (c *SeedsCommand) Name() string {
	return "seeds"
}

func (c *SeedsCommand) Init(args []string) error {
	if err := c.parseArgs(args); err != nil {
		return err
	}
	if err := c.initBaseConfig(); err != nil {
		return err
	}

	qa, err := validation.New(c.cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize QA: %w", err)
	}
	c.qa = qa

	det, err := seeder.NewSeederDetector(c.cfg)
	if err != nil {
		return fmt.Errorf(
			"failed to initialize seeder detector: %w", err,
		)
	}
	c.det = det

	c.StartUI()
	return nil
}

// parseArgs parses command-line arguments without initializing config.
func (c *SeedsCommand) parseArgs(args []string) error {
	c.fs = flag.NewFlagSet("seeds", flag.ContinueOnError)

	c.fs.StringVar(&c.chrootDir, "chroot-dir", "",
		"Use the provided directory as chroot directory")
	c.fs.StringVar(&c.skipSeedersRaw, "skip-seeders", "",
		"Comma-separated list of seeders to skip")
	c.fs.StringVar(&c.onlySeedersRaw, "only-seeders", "",
		"Comma-separated allow-list of seeders to accept")
	c.fs.BoolVar(&c.resume, "resume", false,
		"Try resuming seeder work inside chroot")
	c.fs.StringVar(
		&c.builtRootfsFile, "built-rootfs-file", "",
		"Path to write successfully built chroot dirs")
	c.fs.StringVar(
		&c.builtSeedersFile, "built-seeders-file", "",
		"Path to write successfully built seeder names")
	c.fs.StringVar(&c.stage3File, "stage3-file", "",
		"Gentoo stage3 file to unpack (skip download)")
	c.fs.BoolVar(&c.verbose, "verbose", false,
		"Enable verbose mode")

	c.fs.Usage = func() {
		fmt.Printf(
			"Usage: vector build %s [options]\n", c.Name(),
		)
		fmt.Println("\nOptions:")
		c.fs.PrintDefaults()
	}
	if err := c.fs.Parse(args); err != nil {
		return err
	}

	if err := requireRoot(); err != nil {
		return err
	}

	c.skipSeeders = SplitCSV(c.skipSeedersRaw)
	c.onlySeeders = SplitCSV(c.onlySeedersRaw)
	return nil
}

// Run delegates to the SignalGuard for cleanup on signals/panics.
func (c *SeedsCommand) Run() error {
	return c.RunWithGuard(c.runSeeds)
}

// updateStdWriters updates the stdout and stderr writers with the given label
// and propagates them to the seeder library.
func (c *SeedsCommand) updateStdWriters(sd seeder.ISeeder, name string) {
	c.SetupPrinters(fmt.Sprintf("seeds:%s", name))
	sd.SetStdout(c.StdoutWriter())
	sd.SetStderr(c.StderrWriter())
	c.det.SetStderr(c.StderrWriter())
}

// runSeeds implements the seeder workflow, mirroring the bash seeder
// script's main() function.
func (c *SeedsCommand) runSeeds() error {
	sd, err := newSeeder(
		c.cfg,
		&seeder.NewSeederOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to initialize seeder: %w", err)
	}

	c.updateStdWriters(sd, "main")
	c.PushCleanup(c.FlushPrinters)

	// Ensure private repo is initialized.
	if err := sd.MaybeInitializePrivateRepo(); err != nil {
		return fmt.Errorf(
			"private repo initialization failed: %w", err,
		)
	}

	// Verify seeder environment.
	if err := c.qa.VerifySeederEnvironmentSetup("/"); err != nil {
		return fmt.Errorf(
			"environment verification failed: %w", err,
		)
	}

	c.PushCleanup(sd.Cleanup)

	// Import Gentoo GPG keys.
	if err := sd.ImportGentooGpgKeys(); err != nil {
		return fmt.Errorf("GPG key import failed: %w", err)
	}
	c.PushCleanup(sd.KillGpgDaemons)

	// Detect seeders.
	seeders, err := c.det.Detect(c.skipFilter(), c.onlyFilter())
	if err != nil {
		return fmt.Errorf("seeder detection failed: %w", err)
	}
	if len(seeders) == 0 {
		return fmt.Errorf("no seeders found, nothing to do")
	}

	// Print execution plan.
	c.Printf("Will execute seeders in the following order:\n")
	for _, s := range seeders {
		c.Printf("  %s\n", s.Dir)
	}

	// Initialize output files.
	if err := c.initOutputFiles(); err != nil {
		return err
	}

	// Read resource config via SeederConfig.
	scfg := seeder.NewSeederConfig(c.cfg)

	// Determine parallelism level.
	parallelism, err := scfg.Parallelism()
	if err != nil {
		return fmt.Errorf("failed to read parallelism config: %w", err)
	}
	if parallelism < 1 {
		c.PrintErrf(
			"WARNING: Seeder.Parallelism=%d is invalid, assuming 1.\n",
			parallelism,
		)
		parallelism = 1
	}

	if parallelism > 1 {
		c.Printf("Parallel mode: up to %d seeders at once.\n", parallelism)
	}

	maxMemGiB, err := scfg.MaxMemoryGiB()
	if err != nil {
		return fmt.Errorf("failed to read max memory config: %w", err)
	}
	maxCores, err := scfg.MaxCores()
	if err != nil {
		return fmt.Errorf("failed to read max cores config: %w", err)
	}

	coresMultiplier, err := scfg.CoresMultiplier()
	if err != nil {
		return fmt.Errorf("failed to read cores multiplier config: %w", err)
	}

	// Parse params for every seeder to extract dependencies.
	paramsByName, err := c.parseSeedersParams(sd, seeders)
	if err != nil {
		return err
	}

	// Create per-worker cgroups for memory and CPU limiting.
	cgPool, err := c.createWorkerCgroups(&cgroupOpts{
		maxMemGiB:       maxMemGiB,
		maxCores:        maxCores,
		coresMultiplier: coresMultiplier,
	}, parallelism)
	if err != nil {
		return err
	}

	// Context cancelled on SIGINT/SIGTERM via PushCleanup.
	ctx, cancel := context.WithCancel(context.Background())
	c.PushCleanup(cancel)
	defer cancel()

	psOpts := &seeder.ParallelSeedOptions{
		Seeders:      seeders,
		ParamsByName: paramsByName,
		Parallelism:  parallelism,
		ChrootDir:    c.chrootDir,
		Resume:       c.resume,
		Stage3File:   c.stage3File,
		NewSeeder: func(opts *seeder.NewSeederOptions) (seeder.ISeeder, error) {
			return newSeeder(c.cfg, opts)
		},
		NewStdoutWriter: func(label string) io.Writer {
			return c.UI.NewStdoutWriter(label)
		},
		NewStderrWriter: func(label string) io.Writer {
			return c.UI.NewStderrWriter(label)
		},
		PushCleanup:  c.PushCleanup,
		OnSeederDone: c.recordResults,
	}
	if cgPool != nil {
		psOpts.SysProcAttr = cgPool.SysProcAttr
		psOpts.BoostWorker = cgPool.BoostWorker
		psOpts.UnboostWorker = cgPool.UnboostWorker
	}

	seedStart := time.Now()
	sd.Print(
		"Seeding started at %s\n",
		seedStart.Format(time.RFC3339),
	)
	defer func() {
		seedEnd := time.Now()
		sd.Print(
			"Seeding finished at %s (elapsed: %s)\n",
			seedEnd.Format(time.RFC3339), seedEnd.Sub(seedStart),
		)
	}()

	if err := seeder.ParallelSeed(ctx, psOpts); err != nil {
		return err
	}

	// Run post-build hooks sequentially.
	if err := c.runPostBuild(seeders, paramsByName); err != nil {
		return err
	}

	c.Printf("All done:\n")
	for _, info := range seeders {
		c.Printf("  [%s] %s done.\n", info.Name, info.Dir)
	}
	return nil
}

type cgroupOpts struct {
	maxMemGiB       int
	maxCores        int
	coresMultiplier float64
}

// createWorkerCgroups sets up per-worker cgroup v2 resource limits.
// Returns nil when parallelism <= 1 and no resource caps are configured.
func (c *SeedsCommand) createWorkerCgroups(opts *cgroupOpts, parallelism int) (*cgroups.WorkerPool, error) {
	if parallelism <= 1 && opts.maxMemGiB <= 0 && opts.maxCores <= 0 {
		return nil, nil
	}
	var si syscall.Sysinfo_t
	if err := syscall.Sysinfo(&si); err != nil {
		return nil, fmt.Errorf("failed to query system memory: %w", err)
	}
	totalBytes := si.Totalram * uint64(si.Unit)
	if opts.maxMemGiB > 0 {
		configBytes := uint64(opts.maxMemGiB) * 1024 * 1024 * 1024
		if configBytes < totalBytes {
			totalBytes = configBytes
		}
	}
	if parallelism < 1 {
		parallelism = 1
	}
	memPerWorker := totalBytes / uint64(parallelism)
	numCPUs := runtime.NumCPU()
	if opts.maxCores > 0 && opts.maxCores < numCPUs {
		numCPUs = opts.maxCores
	}
	cgPool, err := cgroups.NewWorkerPool(&cgroups.WorkerPoolOptions{
		Parallelism:       parallelism,
		MemPerWorkerBytes: memPerWorker,
		TotalMemBytes:     totalBytes,
		NumCPUs:           numCPUs,
		CoresMultiplier:   opts.coresMultiplier,
		CgroupRoot:        c.cgroupRoot,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create worker cgroups: %w", err)
	}
	c.PushCleanup(cgPool.Close)
	cpusPerWorker := numCPUs / parallelism
	if opts.coresMultiplier > 0 && opts.coresMultiplier != 1.0 {
		c.Printf("Worker cgroups: %d workers × %d GiB RAM, %d CPUs each (plus %.1fx oversubscription)\n",
			parallelism, memPerWorker/(1024*1024*1024), cpusPerWorker, opts.coresMultiplier)
	} else {
		c.Printf("Worker cgroups: %d workers × %d GiB RAM, %d CPUs each\n",
			parallelism, memPerWorker/(1024*1024*1024), cpusPerWorker)
	}
	return cgPool, nil
}

// runPostBuild runs post-build scripts sequentially for every seeder
// that has a PostBuildExec configured. Called after all parallel builds
// complete successfully.
func (c *SeedsCommand) runPostBuild(seeders []seeder.SeederInfo, paramsByName map[string]*seeder.SeederParams) error {
	c.Printf("Running post-build hooks sequentially ...\n")
	for _, info := range seeders {
		if info.PostBuildExec == "" {
			c.Printf("  [%s] No post-build script, skipping.\n", info.Name)
			continue
		}

		params := paramsByName[info.Name]
		chrootDir, err := seeder.ResolveChrootDir(info.Name, params, c.chrootDir)
		if err != nil {
			return err
		}

		c.Printf("  [%s] Running post-build in %s ...\n", info.Name, chrootDir)

		sopts := &seeder.NewSeederOptions{
			Stdout: c.UI.NewStdoutWriter(fmt.Sprintf("seeds:%s:post", info.Name)),
			Stderr: c.UI.NewStderrWriter(fmt.Sprintf("seeds:%s:post", info.Name)),
		}
		postSD, err := newSeeder(c.cfg, sopts)
		if err != nil {
			return fmt.Errorf("[%s] failed to create seeder for post-build: %w", info.Name, err)
		}
		c.PushCleanup(postSD.Cleanup)

		if err := postSD.SetupChrootDNS(chrootDir); err != nil {
			return fmt.Errorf("[%s] post-build DNS setup failed: %w", info.Name, err)
		}

		postOpts := &seeder.SeedOptions{
			ChrootDir: chrootDir,
			Info:      info,
		}
		if err := postSD.PostBuild(postOpts); err != nil {
			return fmt.Errorf("[%s] post-build failed: %w", info.Name, err)
		}
		postSD.Cleanup()

		c.Printf("  [%s] Post-build complete.\n", info.Name)
	}
	return nil
}

// parseSeedersParams parses the params file for each seeder using sd and
// returns a map of seeder name to parsed params.
func (c *SeedsCommand) parseSeedersParams(sd seeder.ISeeder, seeders []seeder.SeederInfo) (map[string]*seeder.SeederParams, error) {
	paramsByName := make(map[string]*seeder.SeederParams, len(seeders))
	for _, info := range seeders {
		params, err := sd.ParseSeederParams(info)
		if err != nil {
			return nil, fmt.Errorf("[%s] failed to parse params: %w", info.Name, err)
		}
		paramsByName[info.Name] = params
	}
	return paramsByName, nil
}

// --- Helper methods ---

// skipFilter returns a SeederFilterFunc that skips seeders present in
// --skip-seeders.  Returns nil when no skip list is configured.
func (c *SeedsCommand) skipFilter() seeder.SeederFilterFunc {
	return makeSkipFilter(c.skipSeeders)
}

// onlyFilter returns a SeederFilterFunc that accepts only seeders in
// --only-seeders.  Returns nil when no allow-list is configured
// (all seeders pass).
func (c *SeedsCommand) onlyFilter() seeder.SeederFilterFunc {
	return makeOnlyFilter(c.onlySeeders)
}

// initOutputFiles truncates the built-rootfs and built-seeders output
// files if the corresponding flags were provided.
func (c *SeedsCommand) initOutputFiles() error {
	if c.builtRootfsFile != "" {
		c.Printf("Writing built chroots to %s ...\n", c.builtRootfsFile)
		if err := truncateOutputFile(c.builtRootfsFile); err != nil {
			return err
		}
	}
	if c.builtSeedersFile != "" {
		c.Printf("Writing built seeders to %s ...\n", c.builtSeedersFile)
		if err := truncateOutputFile(c.builtSeedersFile); err != nil {
			return err
		}
	}
	return nil
}

// recordBuiltRootfsFile appends the given chrootDir to the built-rootfs
// output file if the corresponding flag was provided.
func (c *SeedsCommand) recordBuiltRootfsFile(chrootDir string) error {
	return appendLineToFile(c.builtRootfsFile, chrootDir)
}

func (c *SeedsCommand) recordBuiltSeedersFile(seederName string) error {
	c.BuiltSeeders = append(c.BuiltSeeders, seederName)
	return appendLineToFile(c.builtSeedersFile, seederName)
}

// recordResults appends the seeder name and chroot dir to the output
// files if the corresponding flags were provided.
// It is safe for concurrent use.
func (c *SeedsCommand) recordResults(seederName, chrootDir string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.recordBuiltRootfsFile(chrootDir); err != nil {
		return fmt.Errorf("failed to record built rootfs: %w", err)
	}

	if err := c.recordBuiltSeedersFile(seederName); err != nil {
		return fmt.Errorf("failed to record built seeder: %w", err)
	}
	return nil
}
