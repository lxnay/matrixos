package commands

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/seeder"
)

// EnterCommand enters a seeded chroot interactively.
type EnterCommand struct {
	BaseCommand
	UI
	SignalGuard
	fs *flag.FlagSet

	// Library instances
	det      seeder.ISeederDetector
	detected []seeder.SeederInfo

	// Flags
	skipLock bool

	// Positional arguments (chroot dirs or names)
	targets []string
}

// NewEnterCommand creates a new EnterCommand.
func NewEnterCommand() *EnterCommand {
	return &EnterCommand{}
}

func (c *EnterCommand) Name() string {
	return "enter"
}

// SeedersParams maps seeder names to their parsed params.
type SeedersParams map[string]*seeder.SeederParams

func (c *EnterCommand) makeSeederParams(sd seeder.ISeeder) (SeedersParams, error) {
	paramsMap := make(SeedersParams)
	for _, info := range c.detected {
		params, err := sd.ParseSeederParams(info)
		if err != nil || params == nil {
			// Skip seeders whose params cannot be parsed.
			continue
		}
		paramsMap[info.Name] = params
	}
	return paramsMap, nil
}

func (c *EnterCommand) Init(args []string) error {
	if err := c.parseArgs(args); err != nil {
		return err
	}
	if err := c.initBaseConfig(); err != nil {
		return fmt.Errorf("error reading config: %w", err)
	}
	c.StartUI()
	c.SetupPrinters("enter")
	defer c.FlushPrinters()

	det, err := seeder.NewSeederDetector(c.cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize seeder detector: %w", err)
	}
	det.SetStderr(c.errPrinter)
	c.det = det

	detected, err := c.det.Detect(nil, nil)
	if err != nil {
		return fmt.Errorf("seeder detection failed: %w", err)
	}
	c.detected = detected

	return nil
}

func (c *EnterCommand) parseArgs(args []string) error {
	c.fs = flag.NewFlagSet("enter", flag.ContinueOnError)

	c.fs.BoolVar(&c.skipLock, "skiplock", false, "Skip acquiring the seeder lock before entering the chroot")

	c.fs.Usage = func() {
		fmt.Println("Usage: vector dev enter [--skiplock] <chroot-dir-or-name> [...]")
		fmt.Println()
		fmt.Println("Enter a seeded chroot interactively.")
		fmt.Println("Specify full paths to chroot directories, or just the chroot name.")
		fmt.Println()
		c.fs.PrintDefaults()
	}

	if err := c.fs.Parse(args); err != nil {
		return err
	}

	if getEuid() != 0 {
		return fmt.Errorf("this command must be run as root")
	}

	c.targets = c.fs.Args()
	if len(c.targets) == 0 {
		c.fs.Usage()
		return fmt.Errorf("no chroot dirs or names specified")
	}

	return nil
}

// Run delegates to the SignalGuard for cleanup on signals/panics.
func (c *EnterCommand) Run() error {
	return c.RunWithGuard(c.run)
}

// run implements the enter workflow.
func (c *EnterCommand) run() error {
	opts := seeder.NewSeederOptions{
		Verbose: false,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}
	sd, err := newSeeder(c.cfg, &opts)
	if err != nil {
		return fmt.Errorf("failed to initialize seeder: %w", err)
	}
	c.PushCleanup(sd.Cleanup)
	defer sd.Cleanup()

	sd.SetStdout(c.printer)
	sd.SetStderr(c.errPrinter)

	// Classify targets into absolute dirs and bare names.
	var chrootDirs []string
	var chrootNames []string

	seedersParams, err := c.makeSeederParams(sd)
	if err != nil {
		return fmt.Errorf("failed to make params map: %w", err)
	}

	completeChrootDirs := make(map[string]bool)
	for _, params := range seedersParams {
		for _, dir := range params.CompleteChrootDirs {
			completeChrootDirs[dir] = true
		}
	}
	partialChrootDirs := make(map[string]bool)
	for _, params := range seedersParams {
		for _, dir := range params.PartialChrootDirs {
			partialChrootDirs[dir] = true
		}
	}

	for _, target := range c.targets {
		if target == "" {
			continue
		}

		if params, ok := seedersParams[target]; ok {
			preferred := params.PreferredChrootDir
			if preferred != "" && filesystems.DirectoryExists(preferred) {
				chrootNames = append(chrootNames, filepath.Base(preferred))
				continue
			}

			latest := params.LatestAvailableChrootDir
			if latest != "" && filesystems.DirectoryExists(latest) {
				chrootName := filepath.Base(latest)
				chrootNames = append(chrootNames, chrootName)
				continue
			}
		}

		if completeChrootDirs[target] {
			chrootDirs = append(chrootDirs, target)
			continue
		}
		if partialChrootDirs[target] {
			chrootDirs = append(chrootDirs, target)
			continue
		}

		if filesystems.DirectoryExists(target) {
			chrootDirs = append(chrootDirs, target)
			continue
		}

		name := filepath.Base(target)
		if target == name {
			// Bare name — will be resolved against detected seeders.
			chrootNames = append(chrootNames, target)
			continue
		}
	}

	// Resolve bare names by scanning seeder params for SEEDER_CHROOTS_DIR.
	if len(chrootNames) > 0 {
		c.Printf("%s%sResolving chroot names...%s\n",
			c.cBold, c.iconSearch, c.cReset)
		for _, name := range chrootNames {
			c.Printf("  %s%s%s\n", c.cCyan, name, c.cReset)
		}

		resolved, err := c.resolveNames(seedersParams, chrootNames)
		if err != nil {
			return err
		}
		chrootDirs = append(chrootDirs, resolved...)
	}

	if len(chrootDirs) == 0 {
		return fmt.Errorf("no chroot dirs or names found")
	}

	for _, d := range chrootDirs {
		c.Printf("  %s%s%s%s\n", c.cGreen, c.iconCheck, d, c.cReset)
	}

	// Enter each chroot.
	for _, chrootDir := range chrootDirs {
		if err := c.enter(sd, chrootDir); err != nil {
			return fmt.Errorf("error entering chroot %s: %w", chrootDir, err)
		}
	}

	return nil
}

// resolveNames maps bare chroot names to full paths by examining
// each detected seeder's params for SEEDER_CHROOTS_DIR.
func (c *EnterCommand) resolveNames(sps SeedersParams, names []string) ([]string, error) {
	// Collect all unique SEEDER_CHROOTS_DIR values.
	seen := make(map[string]bool)
	var chrootsDirs []string
	for _, info := range c.detected {
		params, ok := sps[info.Name]
		if !ok {
			continue
		}
		if params.ChrootsDir == "" {
			continue
		}
		if !seen[params.ChrootsDir] {
			seen[params.ChrootsDir] = true
			chrootsDirs = append(chrootsDirs, params.ChrootsDir)
		}
	}

	// Look for each name inside the collected chroots dirs.
	var resolved []string
	resolvedSeen := make(map[string]bool)
	for _, name := range names {
		for _, dir := range chrootsDirs {
			candidate := filepath.Join(dir, name)
			if resolvedSeen[candidate] {
				continue
			}
			resolvedSeen[candidate] = true
			if filesystems.DirectoryExists(candidate) {
				resolved = append(resolved, candidate)
			}
		}
	}

	return resolved, nil
}

// enter runs an interactive shell inside the chroot, and tears down mounts afterwards.
func (c *EnterCommand) enter(sd seeder.ISeeder, chrootDir string) error {
	name := filepath.Base(chrootDir)
	c.SetupPrinters(name)
	defer c.FlushPrinters()

	sd.SetStdout(c.printer)
	sd.SetStderr(c.errPrinter)

	c.Printf("\n%s%sEntering seed %s: %s%s\n",
		c.cBold, c.iconRocket, name, chrootDir, c.cReset)
	c.Println(c.separator)

	return c.chrootWorker(sd, chrootDir)
}

func (c *EnterCommand) chrootWorker(sd seeder.ISeeder, chrootDir string) error {
	// Find the corresponding seeder chroot dir matching it with chrootDir.
	var si seeder.SeederInfo
	var found bool
	for _, info := range c.detected {
		params, err := sd.ParseSeederParams(info)
		if err != nil {
			// Skip seeders whose params cannot be parsed.
			continue
		}
		if slices.Contains(params.CompleteChrootDirs, chrootDir) {
			found = true
			si = info // copy
			break
		}
		if slices.Contains(params.PartialChrootDirs, chrootDir) {
			found = true
			si = info // copy
			break
		}
		if params.PreferredChrootDir == chrootDir {
			// Last ditch attempt.
			found = true
			si = info // copy
			break
		}
	}
	if !found {
		return fmt.Errorf(
			"no valid seeder chroot found for chroot dir %s. Try with --skiplock and full chroot path.",
			chrootDir,
		)
	}

	env := []string{
		fmt.Sprintf("TERM=%s", os.Getenv("TERM")),
	}

	// Monkey patch the seeder's chroot worker to use our chroot runner and env.
	si.ChrootChrootExec = "/bin/sh"
	si.ChrootChrootArgs = []string{"--login"}

	opts := &seeder.SeedOptions{
		ChrootDir: chrootDir,
		Info:      si,
		Env:       env,
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
	}

	// This has the advantage that we use the same entry point.
	enterer := func() error { return sd.Seed(opts) }

	if c.skipLock {
		c.Printf("%s%sSkipping seeder lock acquisition (--skiplock).%s\n",
			c.cYellow, c.iconWarn, c.cReset)
		return enterer()
	}
	return sd.ExecuteWithSeederLock(si.Name, enterer)
}
