package commands

import (
	"flag"
	"fmt"
	"time"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/imager"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/validation"
)

// ImageCommand is a command for building matrixOS images.
type ImageCommand struct {
	BaseCommand
	UI
	SignalGuard
	fs *flag.FlagSet

	// Library instances
	im    imager.IImager
	fsenc filesystems.IFsenc
	qa    *validation.QA

	// Flags
	ref            string
	localOstree    bool
	wholeDevice    string
	efiDevicePath  string
	bootDevicePath string
	rootDevicePath string
	verbose        bool
}

// NewImageCommand creates a new ImageCommand
func NewImageCommand() *ImageCommand {
	return &ImageCommand{}
}

func (c *ImageCommand) Name() string {
	return "image"
}

func (c *ImageCommand) Init(args []string) error {
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
func (c *ImageCommand) parseArgs(args []string) error {
	c.fs = flag.NewFlagSet("image", flag.ContinueOnError)

	c.fs.StringVar(&c.ref, "ref", "", "The ostree ref name to build on (branch name, with or without remote)")
	c.fs.BoolVar(&c.localOstree, "local-ostree", false, "Use the local ostree repo instead of fetching from remote")
	c.fs.StringVar(&c.wholeDevice, "install-device", "", "Whole block device path for imaging (e.g. /dev/sda)")
	c.fs.StringVar(&c.efiDevicePath, "efi-device-path", "", "EFI System Partition path (will not be formatted)")
	c.fs.StringVar(&c.bootDevicePath, "boot-device-path", "", "Boot device path (DATA WIPED)")
	c.fs.StringVar(&c.rootDevicePath, "root-device-path", "", "Root device path (DATA WIPED)")
	c.fs.BoolVar(&c.verbose, "verbose", false, "Show detailed output")

	c.fs.Usage = func() {
		fmt.Printf("Usage: vector build %s [options]\n", c.Name())
		fmt.Println("\nOptions:")
		c.fs.PrintDefaults()
	}
	if err := c.fs.Parse(args); err != nil {
		return err
	}
	// Resolve ref.
	if c.ref == "" {
		return fmt.Errorf("--ref is required")
	}
	if err := requireRoot(); err != nil {
		return err
	}

	return nil
}

// Run uses the SignalGuard to ensure that all registered cleanup functions
// (mounts, loop devices, temp dirs, etc.) are executed even when the process
// is terminated by SIGINT or SIGTERM.
func (c *ImageCommand) Run() error {
	return c.RunWithGuard(c.runImage)
}

func failFastChecks(ot ostree.IOstree, icfg *imager.ImagerConfig) error {
	if _, err := icfg.CreateQcow2(); err != nil {
		return err
	}
	if _, err := icfg.Productionize(); err != nil {
		return err
	}
	if _, err := icfg.ImageTests(); err != nil {
		return err
	}
	if _, err := icfg.Compressor(); err != nil {
		return err
	}
	_, err := ot.GpgEnabled()
	if err != nil {
		return err
	}
	return nil
}

func initImagerRuntime(
	cfg config.IConfig,
	ot ostree.IOstree,
	qa *validation.QA,
	ui *UI,
	envRoot string,
	existingIm imager.IImager,
) (filesystems.IFsenc, imager.IImager, error) {
	ot.SetStdout(ui.StdoutWriter())
	ot.SetStderr(ui.StderrWriter())
	ot.SetVerbose(false) // ostree's own verbose flag, separate from ours.

	if err := qa.VerifyImagerEnvironmentSetup(envRoot); err != nil {
		return nil, nil, fmt.Errorf("environment verification failed: %w", err)
	}

	fsenc, err := filesystems.NewFsenc(
		cfg,
		func(mapperName string) {
			ui.Printf("Opening encrypted rootfs as %s ...\n", mapperName)
		},
		func(mapperName string) {
			ui.Printf("Opened encrypted rootfs as %s.\n", mapperName)
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize fsenc: %w", err)
	}
	fsenc.SetStdout(ui.StdoutWriter())
	fsenc.SetStderr(ui.StderrWriter())

	if err := fsenc.ValidateLuksVariables(); err != nil {
		return nil, nil, fmt.Errorf("LUKS validation failed: %w", err)
	}

	im := existingIm
	if im == nil {
		im, err = imager.NewImager(cfg, ot, fsenc, &imager.NewImagerOptions{})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to initialize imager: %w", err)
		}
	}
	im.SetStdout(ui.StdoutWriter())
	im.SetStderr(ui.StderrWriter())
	return fsenc, im, nil
}

func (c *BaseCommand) prepareImagerRef(im imager.IImager, ref string) (string, error) {
	if err := c.detectRemotedAndPlainRefs(im.PrintError); err != nil {
		return "", err
	}
	rr, err := c.resolveRefRemote(ref, im.PrintWarning)
	if err != nil {
		return "", err
	}
	im.SetRef(rr.Ref)
	c.ot.SetRef(rr.Ref)
	return rr.Ref, nil
}

func pullRemoteOstree(ui *UI, ot ostree.IOstree, im imager.IImager) error {
	if err := showRemoteRefs(ot, im.Print); err != nil {
		return err
	}
	remote, err := ot.Remote()
	if err != nil {
		return err
	}
	im.Print("\n%s%sPulling ostree ref %s:%s ...%s\n",
		ui.cBold, ui.iconDownload, remote, ot.Ref(), ui.cReset)
	if err := ot.Pull(); err != nil {
		return fmt.Errorf("ostree pull failed: %w", err)
	}
	return nil
}

// runImage implements the image building logic.
func (c *ImageCommand) runImage() error {
	c.SetupPrinters(fmt.Sprintf("image:%s", c.shortRef(c.ref)))
	defer c.FlushPrinters()

	imageStart := time.Now()
	c.Printf(
		"[%s] Imaging started at %s\n",
		c.ref, imageStart.Format(time.RFC3339),
	)
	defer func() {
		imageEnd := time.Now()
		c.Printf(
			"[%s] Imaging finished at %s (elapsed: %s)\n",
			c.ref,
			imageEnd.Format(time.RFC3339),
			imageEnd.Sub(imageStart).Round(time.Second),
		)
	}()

	ref := c.ref
	if ostree.IsBranchShortName(ref) {
		return fmt.Errorf(
			"specify a complete branch name, %s is not allowed",
			ref,
		)
	}

	fsenc, im, err := initImagerRuntime(c.cfg, c.ot, c.qa, &c.UI, "/", nil)
	if err != nil {
		return err
	}
	c.fsenc = fsenc
	c.im = im

	// Fail fast on bad params.
	if err := failFastChecks(c.ot, imager.NewImagerConfig(c.cfg)); err != nil {
		return err
	}

	// Validate device paths.
	buildOpts, err := c.validateDevicePaths()
	if err != nil {
		return err
	}

	if _, err := c.prepareImagerRef(c.im, ref); err != nil {
		return err
	}

	// Setup image (the main work) under an exclusive image lock.
	return c.im.ExecuteWithImageLock(func() error {
		// Register cleanups in reverse execution order (LIFO): unmount/detach
		// imager resources first, close any remaining LUKS mappers second, and
		// flush buffered printer output last.
		c.PushCleanup(c.FlushPrinters)
		c.PushCleanup(c.fsenc.Cleanup)
		c.PushCleanup(c.im.Cleanup)

		if err := c.initGpg(); err != nil {
			return err
		}
		c.PushCleanup(c.killGpg)

		// Initialize ostree.
		if c.localOstree {
			if err := showLocalRefs(c.ot, c.im.Print); err != nil {
				return err
			}
		} else {
			if err := c.initializeRemoteOstree(); err != nil {
				return err
			}
		}

		return c.im.Build(buildOpts)
	})
}

// validateDevicePaths validates the device path flags and returns resolved values.
func (c *ImageCommand) validateDevicePaths() (*imager.BuildOptions, error) {
	opts := &imager.BuildOptions{
		EfiDevice:   c.efiDevicePath,
		BootDevice:  c.bootDevicePath,
		RootDevice:  c.rootDevicePath,
		WholeDevice: c.wholeDevice,
	}

	if err := validateDeviceExistence(opts); err != nil {
		return nil, err
	}
	if err := validateBuildDeviceCombination(opts); err != nil {
		return nil, err
	}

	if opts.EfiDevice != "" {
		c.Printf("Selected the following device as EFI System Partition: %s (WILL NOT BE FORMATTED)\n", opts.EfiDevice)
	}
	if opts.WholeDevice != "" {
		c.PrintErrf("Specified whole device %s to flash.\n", opts.WholeDevice)
	}

	return opts, nil
}

// initializeRemoteOstree sets up the ostree remote and pulls the specified ref.
func (c *ImageCommand) initializeRemoteOstree() error {
	return pullRemoteOstree(&c.UI, c.ot, c.im)
}
