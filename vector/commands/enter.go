package commands

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/seeder"
)

// normalizeChrootPath returns a canonical path for comparing and entering
// chroot directories. Relative paths are anchored against the working
// directory without pre-cleaning so "<symlink>/.." expands in POSIX order
// via filepath.EvalSymlinks (e.g. /home -> /var/home on OSTree hosts),
// falling back to filepath.Clean when the path does not exist on disk.
func normalizeChrootPath(p string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		if wd, err := os.Getwd(); err == nil {
			if strings.HasSuffix(wd, string(filepath.Separator)) {
				p = wd + p
			} else {
				p = wd + string(filepath.Separator) + p
			}
		}
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return resolved
}

// containsNormalizedPath reports whether target (already normalized) matches
// any non-empty path in paths after normalization.
func containsNormalizedPath(paths []string, target string) bool {
	if target == "" {
		return false
	}
	for _, p := range paths {
		if p != "" && normalizeChrootPath(p) == target {
			return true
		}
	}
	return false
}

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

func (c *EnterCommand) makeSeederParams(sd seeder.ISeeder) SeedersParams {
	paramsMap := make(SeedersParams)
	for _, info := range c.detected {
		params, err := sd.ParseSeederParams(info)
		if err != nil || params == nil {
			// Skip seeders whose params cannot be parsed.
			continue
		}
		paramsMap[info.Name] = params
	}
	return paramsMap
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

	if err := requireRoot(); err != nil {
		return err
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
	defer c.FlushPrinters()

	opts := seeder.NewSeederOptions{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
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

	seedersParams := c.makeSeederParams(sd)

	completeChrootDirs := make(map[string]bool)
	for _, params := range seedersParams {
		for _, dir := range params.CompleteChrootDirs {
			if norm := normalizeChrootPath(dir); norm != "" {
				completeChrootDirs[norm] = true
			}
		}
	}
	partialChrootDirs := make(map[string]bool)
	for _, params := range seedersParams {
		for _, dir := range params.PartialChrootDirs {
			if norm := normalizeChrootPath(dir); norm != "" {
				partialChrootDirs[norm] = true
			}
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

		normTarget := normalizeChrootPath(target)
		if completeChrootDirs[normTarget] {
			chrootDirs = append(chrootDirs, normTarget)
			continue
		}
		if partialChrootDirs[normTarget] {
			chrootDirs = append(chrootDirs, normTarget)
			continue
		}

		if filesystems.DirectoryExists(normTarget) {
			chrootDirs = append(chrootDirs, normTarget)
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
		if err := c.enter(sd, seedersParams, chrootDir); err != nil {
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
		if !ok || params == nil {
			continue
		}
		if params.ChrootsDir == "" {
			continue
		}
		normDir := normalizeChrootPath(params.ChrootsDir)
		if !seen[normDir] {
			seen[normDir] = true
			chrootsDirs = append(chrootsDirs, normDir)
		}
	}

	// Look for each name inside the collected chroots dirs.
	var resolved []string
	resolvedSeen := make(map[string]bool)
	for _, name := range names {
		for _, dir := range chrootsDirs {
			candidate := normalizeChrootPath(filepath.Join(dir, name))
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
func (c *EnterCommand) enter(sd seeder.ISeeder, seedersParams SeedersParams, chrootDir string) error {
	chrootDir = normalizeChrootPath(chrootDir)
	name := filepath.Base(chrootDir)
	c.SetupPrinters(name)
	defer c.FlushPrinters()

	sd.SetStdout(c.printer)
	sd.SetStderr(c.errPrinter)

	c.Printf("\n%s%sEntering seed %s: %s%s\n",
		c.cBold, c.iconRocket, name, chrootDir, c.cReset)
	c.Println(c.separator)

	return c.chrootWorker(sd, seedersParams, chrootDir)
}

func (c *EnterCommand) chrootWorker(sd seeder.ISeeder, seedersParams SeedersParams, chrootDir string) error {
	normChrootDir := normalizeChrootPath(chrootDir)

	// Find the corresponding seeder chroot dir matching it with chrootDir.
	var si seeder.SeederInfo
	var found bool
	for _, info := range c.detected {
		params, ok := seedersParams[info.Name]
		if !ok || params == nil {
			continue
		}
		if containsNormalizedPath(params.CompleteChrootDirs, normChrootDir) ||
			containsNormalizedPath(params.PartialChrootDirs, normChrootDir) ||
			(params.PreferredChrootDir != "" && normalizeChrootPath(params.PreferredChrootDir) == normChrootDir) {
			found = true
			si = info // copy
			break
		}
	}
	if !found {
		return fmt.Errorf(
			"no valid seeder chroot found for chroot dir %s. Try with --skiplock and full chroot path.",
			normChrootDir,
		)
	}

	env := []string{
		fmt.Sprintf("TERM=%s", os.Getenv("TERM")),
	}

	// Monkey patch the seeder's chroot worker to use our chroot runner and env.
	si.ChrootChrootExec = "/bin/sh"
	si.ChrootChrootArgs = []string{"--login"}

	opts := &seeder.SeedOptions{
		ChrootDir: normChrootDir,
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
