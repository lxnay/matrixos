package commands

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"matrixos/vector/lib/imager"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/releaser"
	"matrixos/vector/lib/validation"
)

// ImagesCommand is a command for building matrixOS images for all detected
// ostree refs.
type ImagesCommand struct {
	BaseCommand
	UI
	SignalGuard
	fs *flag.FlagSet

	// Library instances
	qa *validation.QA

	// Flags
	localOstree         bool
	includeFullBranches bool
	verbose             bool
	skipReleasesRaw     string
	onlyReleasesRaw     string

	// Parsed filter lists
	skipReleases []string
	onlyReleases []string
}

// NewImagesCommand creates a new ImagesCommand
func NewImagesCommand() *ImagesCommand {
	return &ImagesCommand{}
}

func (c *ImagesCommand) Name() string {
	return "images"
}

func (c *ImagesCommand) Init(args []string) error {
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

	c.StartUI()

	return nil
}

// parseArgs parses the command-line arguments without initializing config or ostree.
func (c *ImagesCommand) parseArgs(args []string) error {
	c.fs = flag.NewFlagSet("images", flag.ContinueOnError)

	c.fs.BoolVar(&c.localOstree, "local-ostree", false,
		"Use the local, already pulled ostree repo instead of fetching from remote")
	c.fs.BoolVar(&c.includeFullBranches, "include-full-branches", false,
		"Include *-full branches in the image creation process")
	c.fs.StringVar(&c.skipReleasesRaw, "skip-releases", "",
		"Comma-separated list of release branches to skip")
	c.fs.StringVar(&c.onlyReleasesRaw, "only-releases", "",
		"Comma-separated allow-list of release branches to accept")
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

	// Parse the comma-separated filter lists.
	c.skipReleases = SplitCSV(c.skipReleasesRaw)
	c.onlyReleases = SplitCSV(c.onlyReleasesRaw)

	return nil
}

// Run uses the SignalGuard to ensure cleanup functions are executed even
// when the process is terminated by SIGINT or SIGTERM.
func (c *ImagesCommand) Run() error {
	return c.RunWithGuard(c.runImages)
}

// detectReleases detects available OS releases using either local or
// remote listing, then applies the skip/only filters.
func (c *ImagesCommand) detectReleases(w io.Writer) ([]string, error) {
	det, err := releaser.NewReleaseDetector(c.ot)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize release detector: %w", err)
	}
	det.SetStderr(w)

	var refs []string
	if c.localOstree {
		refs, err = det.DetectLocalReleases(c.skipFilter(), c.onlyFilter())
	} else {
		refs, err = det.DetectRemoteReleases(c.skipFilter(), c.onlyFilter())
	}

	return refs, err
}

// skipFilter returns a RefFilterFunc that skips branches present in the
// --skip-releases list.  Returns nil when no skip list is configured.
func (c *ImagesCommand) skipFilter() releaser.RefFilterFunc {
	return makeSkipFilter(c.skipReleases)
}

// onlyFilter returns a RefFilterFunc that accepts only branches present
// in the --only-releases list.  Returns nil when no only-list is
// configured (i.e. all branches pass).
func (c *ImagesCommand) onlyFilter() releaser.RefFilterFunc {
	return makeOnlyFilter(c.onlyReleases)
}

// runImages implements the main images building logic.
func (c *ImagesCommand) runImages() error {
	c.SetupPrinters("images:all")
	defer c.FlushPrinters()

	c.ot.SetStdout(c.StdoutWriter())
	c.ot.SetStderr(c.StderrWriter())
	c.ot.SetVerbose(false)

	// Verify imager environment.
	if err := c.qa.VerifyImagerEnvironmentSetup("/"); err != nil {
		return fmt.Errorf("environment verification failed: %w", err)
	}

	// Detect available refs.
	refs, err := c.detectReleases(c.StderrWriter())
	if err != nil {
		return err
	}

	if len(refs) == 0 {
		c.PrintErr("No release refs found.")
		return fmt.Errorf("No release refs found, detected or surviving the filters.")
	}

	// Filter full-suffixed branches before parallel execution.
	var filteredRefs []string
	for _, ref := range refs {
		ot, err := c.ot.CloneForRef(ref)
		if err != nil {
			return fmt.Errorf("failed to clone ostree for ref %s: %w", ref, err)
		}
		isFull, err := ot.IsBranchFullSuffixed()
		if err != nil {
			return fmt.Errorf("failed to check full branch suffix for %s: %w", ref, err)
		}
		if isFull && !c.includeFullBranches {
			c.Printf(
				"Skipping full branch: %s (use --include-full-branches to include)\n", ref)
			continue
		}
		filteredRefs = append(filteredRefs, ref)
	}

	if len(filteredRefs) == 0 {
		c.PrintErr("No release refs remaining after filtering.")
		return fmt.Errorf("no release refs remaining after filtering")
	}

	// Fail fast on bad params.
	icfg := imager.NewImagerConfig(c.cfg)
	if err := failFastChecks(c.ot, icfg); err != nil {
		return err
	}

	// Read parallelism config.
	parallelism, err := icfg.Parallelism()
	if err != nil {
		return fmt.Errorf("failed to read parallelism config: %w", err)
	}
	if parallelism < 1 {
		c.Printf(
			"WARNING: Imager.Parallelism=%d is invalid, assuming 1.\n",
			parallelism,
		)
		parallelism = 1
	}
	if parallelism > 1 {
		c.Printf(
			"Parallel mode: up to %d images at once.\n",
			parallelism,
		)
	}

	// Detect ambiguous local refs once before parallel execution.
	if err := c.detectRemotedAndPlainRefs(func(format string, args ...any) {
		fmt.Fprintf(c.StderrWriter(), format, args...)
	}); err != nil {
		return err
	}

	// Important note: here we have 3 cases.
	// 1) on build server. Branches have no remote: prefix.
	// 2) on "client server", user just building images away from
	//    the mothership (client side). Branches have remote:a/b/c (remote: prefix)
	// 3) on images only build server, where ostree repo is pulled from a remote.
	// Case 2 and 3 are fine, we use the remote prefix and happy days.
	// For case 1, we detect this from the absence of the remote: prefix and set remote="local".
	//
	// Resolve remote prefixes sequentially before parallel execution
	// to avoid concurrent writes to the shared config overlay.
	var resolvedRefs []string
	for _, ref := range filteredRefs {
		rr, err := c.resolveRefRemote(ref, func(format string, args ...any) {
			fmt.Fprintf(c.StderrWriter(), format, args...)
		})
		if err != nil {
			return err
		}
		resolvedRefs = append(resolvedRefs, rr.Ref)
	}

	imageStart := time.Now()
	c.Printf("Images building started at %s\n", imageStart.Format(time.RFC3339))
	defer func() {
		imageEnd := time.Now()
		c.Printf(
			"Images building finished at %s (elapsed: %s)\n",
			imageEnd.Format(time.RFC3339), imageEnd.Sub(imageStart),
		)
	}()

	c.Printf("Will image the following refs:\n")
	for _, ref := range resolvedRefs {
		c.Printf("  %s\n", ref)
	}

	// Context cancelled on SIGINT/SIGTERM via PushCleanup.
	ctx, cancel := context.WithCancel(context.Background())
	c.PushCleanup(cancel)
	defer cancel()

	var released []string

	piOpts := &imager.ParallelImageOptions{
		Refs:        resolvedRefs,
		Parallelism: parallelism,
		Config:      c.cfg,
		NewStdoutWriter: func(label string) io.Writer {
			return c.UI.NewStdoutWriter(label)
		},
		NewStderrWriter: func(label string) io.Writer {
			return c.UI.NewStderrWriter(label)
		},
		PushCleanup: c.PushCleanup,
		ShortRef:    c.shortRef,
		OnImageDone: func(ref string) error {
			released = append(released, ref)
			return nil
		},
		SetupBuild: func(pushCleanup func(func()), ot ostree.IOstree, im imager.IImager) error {
			if err := ot.MaybeInitializeRemote(); err != nil {
				return fmt.Errorf("failed to initialize remote: %w", err)
			}
			if err := ot.MaybeInitializeGpg(); err != nil {
				return fmt.Errorf("failed to initialize GPG: %w", err)
			}
			pushCleanup(func() { ot.KillGpgDaemons() })

			if c.localOstree {
				return c.showLocalRefs(ot, im)
			}
			return c.initializeRemoteOstree(ot, im)
		},
	}

	if err := imager.ParallelImage(ctx, piOpts); err != nil {
		return err
	}

	c.Printf("Successfully built images for %d releases:\n",
		len(released))
	for _, r := range released {
		c.Printf("  %s\n", r)
	}

	return nil
}

// initializeRemoteOstree sets up the ostree remote and pulls
// the specified ref.  The ot parameter is the per-worker ostree
// instance (cloned for parallel safety).
func (c *ImagesCommand) initializeRemoteOstree(ot ostree.IOstree, im imager.IImager) error {
	remote, err := ot.Remote()
	if err != nil {
		return err
	}

	if err := c.showRemoteRefs(ot, im); err != nil {
		return err
	}

	im.Print("\n%s%sPulling ostree ref %s:%s ...%s\n",
		c.cBold, c.iconDownload, remote, ot.Ref(), c.cReset)
	if err := ot.Pull(); err != nil {
		return fmt.Errorf("ostree pull failed: %w", err)
	}
	return nil
}

// showLocalRefs prints the local ostree refs to the provided printf function.
// The ot parameter is the per-worker ostree instance.
func (c *ImagesCommand) showLocalRefs(ot ostree.IOstree, im imager.IImager) error {
	return showLocalRefs(ot, im.Print)
}

// showRemoteRefs prints the remote ostree refs to the provided printf function.
// The ot parameter is the per-worker ostree instance.
func (c *ImagesCommand) showRemoteRefs(ot ostree.IOstree, im imager.IImager) error {
	return showRemoteRefs(ot, im.Print)
}
