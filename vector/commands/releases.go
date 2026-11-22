package commands

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sync"
	"time"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/releaser"
	"matrixos/vector/lib/seeder"
	"matrixos/vector/lib/validation"
)

// ReleasesCommand orchestrates the release workflow across all detected
// seeders — detecting, resolving branches, and committing each seeder's
// chroot filesystem into the ostree repository.
type ReleasesCommand struct {
	BaseCommand
	UI
	SignalGuard
	fs *flag.FlagSet

	// Library instances
	sd  seeder.ISeeder
	det seeder.ISeederDetector
	qa  *validation.QA

	// Flags
	releaseStage      string
	skipSeedersRaw    string
	onlySeedersRaw    string
	builtReleasesFile string
	verbose           bool

	// Parsed from flags
	skipSeeders []string
	onlySeeders []string

	// Mutex for concurrent access to results
	mu sync.Mutex

	// Results populated during Run().
	BuiltReleases []string
}

// NewReleasesCommand creates a new ReleasesCommand.
func NewReleasesCommand() *ReleasesCommand {
	return &ReleasesCommand{}
}

func (c *ReleasesCommand) Name() string {
	return "releases"
}

func (c *ReleasesCommand) Init(args []string) error {
	if err := c.parseArgs(args); err != nil {
		return err
	}
	if err := c.initBaseConfig(); err != nil {
		return err
	}
	if err := c.initOstree(); err != nil {
		return err
	}

	qa, err := validation.New(c.cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize QA: %w", err)
	}
	c.qa = qa

	sd, err := seeder.NewSeeder(
		c.cfg, &seeder.NewSeederOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to initialize seeder: %w", err)
	}
	c.sd = sd

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
func (c *ReleasesCommand) parseArgs(args []string) error {
	c.fs = flag.NewFlagSet("releases", flag.ContinueOnError)

	c.fs.StringVar(&c.releaseStage, "release-stage", "dev",
		"Release stage: dev or prod")
	c.fs.StringVar(&c.skipSeedersRaw, "skip-seeders", "",
		"Comma-separated list of seeders to skip (by name)")
	c.fs.StringVar(&c.onlySeedersRaw, "only-seeders", "",
		"Comma-separated allow-list of seeders to accept (by name)")
	c.fs.StringVar(&c.builtReleasesFile, "built-releases-file", "",
		"Path to a file where successfully built release branches will be written")
	c.fs.BoolVar(&c.verbose, "verbose", false, "Show detailed output")

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

	// Validate release stage.
	if _, err := releaser.ValidateReleaseStage(c.releaseStage); err != nil {
		return err
	}

	// Must be root.
	if getEuid() != 0 {
		return fmt.Errorf("this command must be run as root")
	}

	c.skipSeeders = SplitCSV(c.skipSeedersRaw)
	c.onlySeeders = SplitCSV(c.onlySeedersRaw)
	return nil
}

// Run delegates to the SignalGuard for cleanup on signals/panics.
func (c *ReleasesCommand) Run() error {
	return c.RunWithGuard(c.runReleases)
}

// updateStdWriters updates the stdout and stderr writers with the given
// label and propagates them to the seeder library.
func (c *ReleasesCommand) updateStdWriters(name string) {
	c.SetupPrinters(fmt.Sprintf("releases:%s", name))
	c.sd.SetStdout(c.StdoutWriter())
	c.sd.SetStderr(c.StderrWriter())
	c.det.SetStderr(c.StderrWriter())
}

// runReleases implements the multi-seeder release workflow.
func (c *ReleasesCommand) runReleases() error {
	c.updateStdWriters("main")
	c.PushCleanup(c.FlushPrinters)

	c.PushCleanup(c.killGpg)

	// Verify releaser environment.
	if err := c.qa.VerifyReleaserEnvironmentSetup("/"); err != nil {
		return fmt.Errorf("environment verification failed: %w", err)
	}

	// Detect seeders.
	seeders, err := c.det.Detect(
		c.skipFilter(), c.onlyFilter(),
	)
	if err != nil {
		return fmt.Errorf("seeder detection failed: %w", err)
	}
	if len(seeders) == 0 {
		return fmt.Errorf("no seeders found, nothing to do")
	}

	// Initialize built-releases output file.
	if err := c.initBuiltReleasesFile(); err != nil {
		return err
	}

	// Read parallelism config.
	rcfg := releaser.NewReleaserConfig(c.cfg)
	parallelism, err := rcfg.Parallelism()
	if err != nil {
		return fmt.Errorf(
			"failed to read parallelism config: %w", err,
		)
	}
	if parallelism < 1 {
		c.sd.Print(
			"WARNING: Releaser.Parallelism=%d is invalid, assuming 1.\n",
			parallelism,
		)
		parallelism = 1
	}

	c.sd.Print("Selected release stage: %s\n", c.releaseStage)
	if parallelism > 1 {
		c.sd.Print(
			"Parallel mode: up to %d releases at once.\n",
			parallelism,
		)
	}
	c.sd.Print(
		"Will release seeds in the following order:\n",
	)
	for _, s := range seeders {
		c.sd.Print("  %s\n", s.Name)
	}

	// Context cancelled on SIGINT/SIGTERM via PushCleanup.
	ctx, cancel := context.WithCancel(context.Background())
	c.PushCleanup(cancel)
	defer cancel()

	releaseStart := time.Now()
	c.sd.Print(
		"Releasing started at %s\n",
		releaseStart.Format(time.RFC3339),
	)
	defer func() {
		releaseEnd := time.Now()
		c.sd.Print(
			"Releasing finished at %s (elapsed: %s)\n",
			releaseEnd.Format(time.RFC3339),
			releaseEnd.Sub(releaseStart),
		)
	}()

	prOpts := &releaser.ParallelReleaseOptions{
		Seeders:      seeders,
		Parallelism:  parallelism,
		ReleaseStage: c.releaseStage,
		Config:       c.cfg,
		Ostree:       c.ot,
		NewStdoutWriter: func(label string) io.Writer {
			return c.UI.NewStdoutWriter(label)
		},
		NewStderrWriter: func(label string) io.Writer {
			return c.UI.NewStderrWriter(label)
		},
		PushCleanup:   c.PushCleanup,
		FindChrootDir: c.findChrootDir,
		ShortRef:      c.shortRef,
		OnReleaseDone: c.recordBuiltRelease,
	}

	if err := releaser.ParallelRelease(ctx, prOpts); err != nil {
		return err
	}

	c.sd.Print("SUCCESS: All builds released.\n")
	for _, b := range c.BuiltReleases {
		c.sd.Print("  %s\n", b)
	}

	return nil
}

// --- Helper methods ---

// findChrootDir locates the chroot directory for a seeder by parsing
// its params.sh file. This grabs both the preferred chroot dir (which may not exist)
// and the latest available chroot dir (which checks for existing chroots).
func (c *ReleasesCommand) findChrootDir(info seeder.SeederInfo) (string, error) {
	params, err := c.sd.ParseSeederParams(info)
	if err != nil {
		return "", fmt.Errorf("failed to parse params: %w", err)
	}

	chrootDir := params.LatestAvailableChrootDir
	if chrootDir == "" {
		return "", fmt.Errorf(
			"no chroot dir specified in params.sh for seeder %s",
			info.Name,
		)
	}
	if !filesystems.DirectoryExists(chrootDir) {
		return "", fmt.Errorf(
			"unable to find chroot dir: %s", chrootDir,
		)
	}
	return chrootDir, nil
}

// skipFilter returns a SeederFilterFunc that skips seeders present in
// --skip-seeders.  Returns nil when no skip list is configured.
func (c *ReleasesCommand) skipFilter() seeder.SeederFilterFunc {
	return makeSkipFilter(c.skipSeeders)
}

// onlyFilter returns a SeederFilterFunc that accepts only seeders in
// --only-seeders.  Returns nil when no allow-list is configured
// (all seeders pass).
func (c *ReleasesCommand) onlyFilter() seeder.SeederFilterFunc {
	return makeOnlyFilter(c.onlySeeders)
}

// initBuiltReleasesFile truncates the built-releases output file if the
// flag was provided.
func (c *ReleasesCommand) initBuiltReleasesFile() error {
	if c.builtReleasesFile == "" {
		return nil
	}
	c.sd.Print(
		"Marking freshly built releases into %s ...\n",
		c.builtReleasesFile,
	)
	return truncateOutputFile(c.builtReleasesFile)
}

// recordBuiltRelease appends the given branch to the built-releases
// output file if the corresponding flag was provided.
// It is safe for concurrent use.
func (c *ReleasesCommand) recordBuiltRelease(branch string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.BuiltReleases = append(c.BuiltReleases, branch)
	return appendLineToFile(c.builtReleasesFile, branch)
}
