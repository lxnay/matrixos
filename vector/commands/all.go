package commands

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
)

var (
	// imagerDefaultFlags are the default flags passed to the imager command.
	imagerDefaultFlags = []string{"--local-ostree"}

	// allLockTimeout is the maximum time to wait for the build lock.
	allLockTimeout = 600 * time.Second

	// allMailCommand is the mail program used to send build-result emails.
	allMailCommand = "mutt"
)

// AllCommand orchestrates a full build-and-release cycle:
// seeds → releases → images → janitor → CDN push.
type AllCommand struct {
	BaseCommand
	UI
	SignalGuard
	fs *flag.FlagSet

	// Flags
	forceRelease   bool
	onlyImages     bool
	forceImages    bool
	skipImages     bool
	onBuildServer  bool
	resumeSeeders  bool
	buildName      string
	buildID        string
	skipSeedersRaw string
	onlySeedersRaw string
	disableJanitor bool
	disableMail    bool
	mailUser       string
	cdnPusher      string
	verbose        bool

	// Replaceable for testing
	cmdRunner runner.Func

	// Internal state
	logFile string
}

func verifyCndPusher(cdnPusherPath string) (bool, error) {
	run := false
	if cdnPusherPath == "" {
		return run, nil
	}

	if !filesystems.FileExists(cdnPusherPath) {
		return run, fmt.Errorf("cdn-pusher %s: file not found", cdnPusherPath)
	}

	info, err := os.Stat(cdnPusherPath)
	if err != nil {
		return run, fmt.Errorf("cdn-pusher %s: %w", cdnPusherPath, err)
	}

	if info.Mode()&0111 == 0 {
		return run, fmt.Errorf("unable to push to CDN. %s not executable", cdnPusherPath)
	}
	run = true
	return run, nil
}

// NewAllCommand creates a new AllCommand.
func NewAllCommand() *AllCommand {
	return &AllCommand{
		cmdRunner: runner.Run,
	}
}

func (c *AllCommand) Name() string {
	return "all"
}

func (c *AllCommand) Init(args []string) error {
	if err := c.parseArgs(args); err != nil {
		return err
	}

	if err := c.initBaseConfig(); err != nil {
		return err
	}

	c.StartUI()
	return nil
}

func (c *AllCommand) parseArgs(args []string) error {
	c.fs = flag.NewFlagSet("all", flag.ContinueOnError)

	c.fs.BoolVar(&c.forceRelease, "force-release", false,
		"Force the re-release of the latest built seeds")
	c.fs.BoolVar(&c.onlyImages, "only-images", false,
		"Generate images from the last committed branches, "+
			"skipping seeder and releaser")
	c.fs.BoolVar(&c.forceImages, "force-images", false,
		"Force images creation for all branches after the seeder and releaser")
	c.fs.BoolVar(&c.skipImages, "skip-images", false,
		"Skip images generation for all branches after the seeder and releaser")
	c.fs.BoolVar(&c.onBuildServer, "on-build-server", false,
		"Optimize execution if seeding, release and imaging "+
			"happens on the same machine")
	c.fs.BoolVar(&c.resumeSeeders, "resume", false,
		"Allow seeder to resume seeds (chroots) build from a checkpoint")
	c.fs.StringVar(&c.buildName, "build-name", "matrixOS weekly",
		"Name of the build")
	c.fs.StringVar(&c.buildID, "build-id", "weekly",
		"ID of the build")
	c.fs.StringVar(&c.skipSeedersRaw, "skip-seeders", "",
		"Comma-separated list of seeders to skip (by name)")
	c.fs.StringVar(&c.onlySeedersRaw, "only-seeders", "",
		"Comma-separated allow-list of seeders to accept (by name)")
	c.fs.BoolVar(&c.disableJanitor, "disable-janitor", false,
		"Disable old artifacts cleanup at the end of the build")
	c.fs.BoolVar(&c.disableMail, "disable-send-mail", false,
		"Disable email sending at the end of the build")
	c.fs.StringVar(&c.mailUser, "mail-user", "root",
		"Recipient for build-result emails")
	c.fs.StringVar(&c.cdnPusher, "cdn-pusher", "",
		"Path to an executable that pushes generated artifacts to a CDN")
	c.fs.BoolVar(&c.verbose, "verbose", false, "Show detailed output")

	c.fs.Usage = func() {
		fmt.Printf("Usage: vector build %s [options]\n", c.Name())
		fmt.Println("\nOptions:")
		c.fs.PrintDefaults()
	}
	if err := c.fs.Parse(args); err != nil {
		return err
	}

	if err := requireRoot(); err != nil {
		return err
	}

	if c.buildID == "" {
		return fmt.Errorf("build ID cannot be empty")
	}

	if _, err := verifyCndPusher(c.cdnPusher); err != nil {
		return err
	}

	return nil
}

func (c *AllCommand) Run() error {
	return c.RunWithGuard(c.run)
}

// sendMail sends a build-result email via mutt if available and enabled.
func (c *AllCommand) sendMail(buildErr error) {
	if c.disableMail {
		c.Printf("Not sending an email with the build results.\n")
		return
	}

	muttExec, err := exec.LookPath(allMailCommand)
	if err != nil {
		c.PrintErrf("mutt not installed, not emailing build status.\n")
		return
	}

	status := "SUCCESSFUL"
	if buildErr != nil {
		status = "FAILED"
	}
	subject := fmt.Sprintf(
		"[%s] %s execution at %s",
		c.buildName,
		status,
		time.Now().Format("20060102"),
	)

	muttArgs := []string{"-s", subject}
	if c.logFile != "" && filesystems.FileExists(c.logFile) {
		muttArgs = append(muttArgs, "-a", c.logFile)
	}
	muttArgs = append(muttArgs, "--", c.mailUser)

	cmd := runner.Cmd{
		Name:   muttExec,
		Args:   muttArgs,
		Stdin:  strings.NewReader(""),
		Stdout: c.StdoutWriter(),
		Stderr: c.StderrWriter(),
	}
	if err := c.cmdRunner(&cmd); err != nil {
		c.PrintErrf("Failed to send mail: %v\n", err)
	}
}

func (c *AllCommand) run() error {
	c.SetupPrinters("build:all")
	defer c.FlushPrinters()
	c.PushCleanup(c.FlushPrinters)

	// Acquire build lock.
	rawUnlock, err := c.acquireBuildLock()
	if err != nil {
		return err
	}
	unlock := sync.OnceFunc(rawUnlock)
	defer unlock()
	c.PushCleanup(unlock)

	// Set up log file.
	logF, err := c.setupLogFile()
	if err != nil {
		return err
	}

	// Tee all styled output (this command + sub-commands) to the log before
	// registering closeLog so a signal cannot close logF and then leave a
	// closed file attached as the global log writer.
	SetGlobalLogWriter(logF)
	c.SetLogWriter(logF)

	// Flush buffered output, detach printers/global log, and close logF once
	// before sendMail attaches c.logFile (and on signal/panic/early return).
	closeLog := sync.OnceFunc(func() {
		c.FlushPrinters()
		c.SetLogWriter(nil)
		ClearGlobalLogWriter()
		_ = logF.Close()
	})
	defer closeLog()
	c.PushCleanup(closeLog)

	c.Printf("Logfile at: %s\n", c.logFile)

	// Run the pipeline, flush & close the log, then send mail.
	runErr := c.runPipeline()
	closeLog()

	c.sendMail(runErr)
	return runErr
}

// runPipeline executes the seeds → releases → images → janitor → CDN pipeline.
func (c *AllCommand) runPipeline() error {
	builtSeeders, done, err := c.buildSeeds()
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	builtReleases, done, err := c.buildReleases(builtSeeders)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	imagesBuilt, err := c.buildImages(builtReleases)
	if err != nil {
		return err
	}

	if err := c.runJanitor(); err != nil {
		return err
	}

	if err := c.runCDNPusher(builtReleases, imagesBuilt); err != nil {
		return err
	}

	return nil
}

// buildImages decides whether to build images and, if so,
// runs the imager. It returns true if images were built.
func (c *AllCommand) buildImages(builtReleases []string) (bool, error) {
	var onlyReleases []string

	if c.skipImages {
		c.Printf("Skipping images creation via --skip-images.\n")
		return false, nil
	}

	if len(builtReleases) > 0 {
		onlyReleases = builtReleases
		c.Printf(
			"Creating new images only for freshly built releases: %s ...\n",
			strings.Join(builtReleases, ","),
		)
	} else if c.forceImages {
		c.Printf("Forcing new images via --force-images.\n")
	} else if c.onlyImages {
		c.Printf("Creating only images (all) via --only-images.\n")
	} else {
		c.Printf("No images to release. Yay?\n")
		return false, nil
	}

	args := imagerDefaultFlags
	if len(onlyReleases) > 0 {
		onlyReleasesFlag := "--only-releases=" + strings.Join(onlyReleases, ",")
		args = append(args, onlyReleasesFlag)
	}

	cmd := NewImagesCommand()
	if err := cmd.Init(args); err != nil {
		return false, err
	}

	if err := cmd.Run(); err != nil {
		return false, err
	}
	return true, nil
}

// buildReleases decides whether to build releases and, if so,
// runs the releaser. It returns the list of built releases, a done
// flag indicating there is nothing to release (pipeline should stop),
// or an error.
func (c *AllCommand) buildReleases(builtSeeders []string) ([]string, bool, error) {
	if c.onlyImages {
		c.Printf("Skipping releases build due to --only-images.\n")
		return nil, false, nil
	}

	if !c.forceRelease && len(builtSeeders) == 0 {
		c.Printf("No new seeds built, skipping releases and images.\n")
		return nil, true, nil
	}

	var onlySeeders []string
	if c.forceRelease {
		c.Printf("Forcing all releases and new images via --force-release.\n")
	} else if len(builtSeeders) > 0 {
		onlySeeders = builtSeeders
		c.Printf(
			"Releasing only for freshly built seeders: %s ...\n",
			strings.Join(builtSeeders, ","),
		)
	}

	c.Printf("Releasing ...\n")

	var args []string
	if c.verbose {
		args = append(args, "--verbose")
	}
	args = append(args, c.seederFilterArgs()...)
	if len(onlySeeders) > 0 {
		onlySeedersFlag := "--only-seeders=" + strings.Join(onlySeeders, ",")
		args = append(args, onlySeedersFlag)
	}

	cmd := NewReleasesCommand()
	if err := cmd.Init(args); err != nil {
		return nil, false, err
	}
	if err := cmd.Run(); err != nil {
		return nil, false, err
	}

	releases := cmd.BuiltReleases // copy.

	if c.onBuildServer {
		// We do not want to reference a remote in the release branches built
		// when building on the build server, because it's not configured by design.
		c.Printf("Executing on a build server, stripping remote prefixes ...\n")
		for i, r := range releases {
			releases[i] = ostree.CleanRemoteFromRef(r)
		}
	}

	for _, r := range releases {
		c.Printf("Built release: %s\n", r)
	}
	return releases, false, nil
}

// buildSeeds decides whether to build seeds and, if so, runs the seeder.
// It returns the list of built seeders, a done flag indicating there is
// nothing to release (pipeline should stop), or an error.
func (c *AllCommand) buildSeeds() ([]string, bool, error) {
	if c.onlyImages {
		c.Printf("Skipping seeds and releases build due to --only-images.\n")
		return nil, false, nil
	}

	c.Printf("Seeding ...\n")

	var args []string
	if c.verbose {
		args = append(args, "--verbose")
	}
	args = append(args, c.seederFilterArgs()...)
	if c.resumeSeeders {
		args = append(args, "--resume")
	}

	cmd := NewSeedsCommand()
	if err := cmd.Init(args); err != nil {
		return nil, false, err
	}
	if err := cmd.Run(); err != nil {
		return nil, false, err
	}

	for _, s := range cmd.BuiltSeeders {
		c.Printf("Seeder built: %s\n", s)
	}

	return cmd.BuiltSeeders, false, nil
}

// runJanitor runs artifact cleanup.
func (c *AllCommand) runJanitor() error {
	if c.disableJanitor {
		c.Printf("Janitor disabled, skipping ...\n")
		return nil
	}

	c.Printf("Running janitor clean ups ...\n")
	cmd := NewJanitorCommand()
	if err := cmd.Init(nil); err != nil {
		return err
	}
	return cmd.Run()
}

// seederFilterArgs returns the --skip-seeders and --only-seeders flags
// forwarded to both the seeder and releaser sub-processes.
func (c *AllCommand) seederFilterArgs() []string {
	var args []string
	if c.skipSeedersRaw != "" {
		args = append(args, "--skip-seeders="+c.skipSeedersRaw)
	}
	if c.onlySeedersRaw != "" {
		args = append(args, "--only-seeders="+c.onlySeedersRaw)
	}
	return args
}

// acquireBuildLock creates the locks directory and acquires an
// exclusive file lock. The caller must defer the returned unlock
// function.
func (c *AllCommand) acquireBuildLock() (func(), error) {
	baseLocksDir, err := c.configItem("matrixOS.LocksDir")
	if err != nil {
		return nil, err
	}

	locksDir := filepath.Join(baseLocksDir, c.buildID+"-builder")
	if err := os.MkdirAll(locksDir, 0755); err != nil {
		return nil, fmt.Errorf(
			"failed to create locks dir: %w", err)
	}

	lockFile := filepath.Join(locksDir, c.buildID+"-builder.lock")
	unlock, err := filesystems.AcquireFileLock(lockFile, allLockTimeout)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to acquire build lock (another builder running?): %w", err)
	}

	return unlock, nil
}

// setupLogFile creates the log directory and opens a timestamped
// log file. The caller must close the returned file.
func (c *AllCommand) setupLogFile() (*os.File, error) {
	baseLogDir, err := c.configItem("matrixOS.LogsDir")
	if err != nil {
		return nil, err
	}

	logDir := filepath.Join(baseLogDir, c.buildID+"-builder")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log dir: %w", err)
	}

	c.logFile = filepath.Join(
		logDir,
		fmt.Sprintf(
			"build-%s.log",
			time.Now().Format("20060102-150405"),
		),
	)

	logF, err := os.Create(c.logFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}
	return logF, nil
}

// runCDNPusher executes the CDN pusher script if configured.
func (c *AllCommand) runCDNPusher(builtReleases []string, imagesBuilt bool) error {
	var run bool
	var err error
	if run, err = verifyCndPusher(c.cdnPusher); err != nil {
		return err
	}
	if !run {
		c.Printf("CDN pusher not configured, skipping CDN push.\n")
		return nil
	}

	imagesFlag := "0"
	if imagesBuilt {
		imagesFlag = "1"
	}

	env := append(os.Environ(),
		"MATRIXOS_BUILT_RELEASES="+strings.Join(builtReleases, " "),
		"MATRIXOS_BUILT_IMAGES="+imagesFlag,
	)

	c.Printf("Pushing to CDN via %s ...\n", c.cdnPusher)

	cmd := runner.Cmd{
		Name:   c.cdnPusher,
		Env:    env,
		Stdout: c.StdoutWriter(),
		Stderr: c.StderrWriter(),
	}
	return c.cmdRunner(&cmd)
}
