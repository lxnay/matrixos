package commands

import (
	"flag"
	"fmt"
	"os"
	"time"

	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/releaser"
)

// ReleaseCommand is a command for building matrixOS releases.
type ReleaseCommand struct {
	BaseCommand
	UI
	SignalGuard
	fs *flag.FlagSet

	// Library instances
	rel releaser.IRelease

	// Flags
	ref       string
	chrootDir string
	imageDir  string
	verbose   bool
}

// NewReleaseCommand creates a new ReleaseCommand
func NewReleaseCommand() *ReleaseCommand {
	return &ReleaseCommand{}
}

func (c *ReleaseCommand) Name() string {
	return "release"
}

func (c *ReleaseCommand) Init(args []string) error {
	if err := c.parseArgs(args); err != nil {
		return err
	}

	if err := c.initBaseConfig(); err != nil {
		return err
	}

	if err := c.initOstree(); err != nil {
		return err
	}

	c.StartUI()

	return nil
}

// parseArgs parses the command-line arguments without initializing config or ostree.
func (c *ReleaseCommand) parseArgs(args []string) error {
	c.fs = flag.NewFlagSet("release", flag.ContinueOnError)

	c.fs.StringVar(&c.ref, "ref", "", "The ostree ref name to release (required)")
	c.fs.StringVar(&c.chrootDir, "chroot-dir", "", "Source chroot directory (required)")
	c.fs.StringVar(&c.imageDir, "image-dir", "", "Destination image directory (required)")
	c.fs.BoolVar(&c.verbose, "verbose", false, "Show detailed output")

	c.fs.Usage = func() {
		fmt.Printf("Usage: vector build %s [options]\n", c.Name())
		fmt.Println("\nOptions:")
		c.fs.PrintDefaults()
	}
	if err := c.fs.Parse(args); err != nil {
		return err
	}

	if c.ref == "" {
		return fmt.Errorf("--ref is required")
	}
	if c.chrootDir == "" {
		return fmt.Errorf("--chroot-dir is required")
	}
	if c.imageDir == "" {
		return fmt.Errorf("--image-dir is required")
	}

	// Reject full-suffixed branch names.
	if ostree.BranchContainsRemote(c.ref) {
		return fmt.Errorf("do not pass branch names with remote prefix; just the plain branch")
	}

	// Must be root.
	if getEuid() != 0 {
		return fmt.Errorf("this command must be run as root")
	}

	return nil
}

// Run uses the SignalGuard to ensure cleanup on signals.
func (c *ReleaseCommand) Run() error {
	return c.RunWithGuard(c.runRelease)
}

// runRelease implements the release workflow.
func (c *ReleaseCommand) runRelease() error {
	releaseStart := time.Now()
	c.Printf(
		"[%s] runRelease started at %s\n",
		c.ref, releaseStart.Format(time.RFC3339),
	)
	defer func() {
		releaseEnd := time.Now()
		c.Printf(
			"[%s] Release finished at %s (elapsed: %s)\n",
			c.ref,
			releaseEnd.Format(time.RFC3339),
			releaseEnd.Sub(releaseStart).Round(time.Second),
		)
	}()

	c.PushCleanup(c.killGpg)

	ref := c.ref
	if ostree.IsBranchShortName(ref) {
		return fmt.Errorf(
			"specify a complete branch name, %s is not allowed",
			ref,
		)
	}

	// Set up styled writers for subprocess output.
	c.SetupPrinters(fmt.Sprintf("release:%s", c.shortRef(ref)))
	defer c.FlushPrinters()

	c.ot.SetStdout(c.StdoutWriter())
	c.ot.SetStderr(c.StderrWriter())
	c.ot.SetVerbose(false) // ostree's own verbose flag, separate from ours.
	c.ot.SetRef(ref)

	// Create c.imageDir if it doesn't exist and check it's a valid directory.
	if err := os.MkdirAll(c.imageDir, 0755); err != nil {
		return fmt.Errorf("failed to create imageDir: %w", err)
	}

	// Create the releaser instance.
	opts := &releaser.NewReleaserOptions{
		ChrootDir: c.chrootDir,
		ImageDir:  c.imageDir,
		Ref:       ref,
	}
	rel, err := releaser.NewReleaser(c.cfg, c.ot, opts)
	if err != nil {
		return fmt.Errorf("failed to initialize releaser: %w", err)
	}
	c.rel = rel
	c.rel.SetStdout(c.StdoutWriter())
	c.rel.SetStderr(c.StderrWriter())

	// Execute the release pipeline under an exclusive release lock.
	return c.rel.ExecuteWithReleaseLock(func() error {
		// Register cleanup.
		c.PushCleanup(func() {
			c.rel.Cleanup()
			c.FlushPrinters()
		})

		if err := c.rel.Build(); err != nil {
			return fmt.Errorf("release build failed: %w", err)
		}
		c.rel.Print("Released filesystem at %s to ostree as branch: %s.\n",
			c.imageDir,
			ref,
		)
		return nil
	})
}
