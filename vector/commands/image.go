package commands

import (
	"flag"
	"fmt"
	"time"

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
	// Must be root.
	if getEuid() != 0 {
		return fmt.Errorf("this command must be run as root")
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

// runImage implements the image building logic.
func (c *ImageCommand) runImage() error {
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

	c.SetupPrinters(fmt.Sprintf("image:%s", c.shortRef(c.ref)))
	defer c.FlushPrinters()

	ref := c.ref
	if ostree.IsBranchShortName(ref) {
		return fmt.Errorf(
			"specify a complete branch name, %s is not allowed",
			ref,
		)
	}
	// Set up styled writers for subprocess output.
	c.ot.SetStdout(c.StdoutWriter())
	c.ot.SetStderr(c.StderrWriter())
	c.ot.SetVerbose(false) // ostree's own verbose flag, separate from ours.

	// Verify imager environment.
	if err := c.qa.VerifyImagerEnvironmentSetup("/"); err != nil {
		return fmt.Errorf("environment verification failed: %w", err)
	}

	fsenc, err := filesystems.NewFsenc(
		c.cfg,
		func(mapperName string) {
			c.Printf(
				"Opening encrypted rootfs as %s ...\n", mapperName)
		},
		func(mapperName string) {
			c.Printf(
				"Closing encrypted rootfs as %s ...\n", mapperName)
		},
	)
	if err != nil {
		return fmt.Errorf("failed to initialize fsenc: %w", err)
	}
	c.fsenc = fsenc

	// Validate LUKS variables.
	if err := c.fsenc.ValidateLuksVariables(); err != nil {
		return fmt.Errorf("LUKS validation failed: %w", err)
	}

	opts := imager.NewImagerOptions{}
	im, err := imager.NewImager(c.cfg, c.ot, c.fsenc, &opts)
	if err != nil {
		return fmt.Errorf("failed to initialize imager: %w", err)
	}
	c.im = im
	c.im.SetStdout(c.StdoutWriter())
	c.im.SetStderr(c.StderrWriter())

	// Fail fast on bad params.
	if err := failFastChecks(c.ot, imager.NewImagerConfig(c.cfg)); err != nil {
		return err
	}

	// Validate device paths.
	buildOpts, err := c.validateDevicePaths()
	if err != nil {
		return err
	}

	// Check against mixing refs with remote embedded in the ref and not, warn the user about it.
	if err := c.detectRemotedAndPlainRefs(c.im.PrintError); err != nil {
		return err
	}

	// Handle refs that contain the remote prefix (e.g. "origin:matrixos/...").
	rr, err := c.resolveRefRemote(ref, c.im.PrintWarning)
	if err != nil {
		return err
	}
	ref = rr.Ref

	c.im.SetRef(ref)
	c.ot.SetRef(ref)

	// Setup image (the main work) under an exclusive image lock.
	return c.im.ExecuteWithImageLock(func() error {
		c.PushCleanup(c.im.Cleanup)
		c.PushCleanup(c.fsenc.Cleanup)
		c.PushCleanup(c.FlushPrinters)

		if err := c.initGpg(); err != nil {
			return err
		}
		c.PushCleanup(c.killGpg)

		// Initialize ostree.
		if c.localOstree {
			if err := c.showLocalRefs(); err != nil {
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

	if opts.EfiDevice != "" {
		if !filesystems.PathExists(opts.EfiDevice) {
			return nil, fmt.Errorf("%s does not exist", opts.EfiDevice)
		}
		c.Printf("Selected the following device as EFI System Partition: %s (WILL NOT BE FORMATTED)\n", opts.EfiDevice)
	}
	if opts.BootDevice != "" {
		if !filesystems.PathExists(opts.BootDevice) {
			return nil, fmt.Errorf("%s does not exist", opts.BootDevice)
		}
	}
	if opts.RootDevice != "" {
		if !filesystems.PathExists(opts.RootDevice) {
			return nil, fmt.Errorf("%s does not exist", opts.RootDevice)
		}
	}
	if opts.WholeDevice != "" {
		if !filesystems.PathExists(opts.WholeDevice) {
			return nil, fmt.Errorf("%s does not exist", opts.WholeDevice)
		}
	}

	// Check that either all 3 partition device paths are set or none.
	anyDevice := opts.EfiDevice != "" || opts.BootDevice != "" || opts.RootDevice != ""
	if anyDevice {
		if opts.EfiDevice == "" || opts.BootDevice == "" || opts.RootDevice == "" {
			return nil, fmt.Errorf("please specify all --*-device-path flags or none")
		}
	}

	// Cannot specify both whole device and individual partitions.
	if opts.WholeDevice != "" && anyDevice {
		return nil, fmt.Errorf(
			"please specify either --install-device or individual --*-device-path flags, not both")
	}

	if opts.WholeDevice != "" {
		c.PrintErrf("Specified whole device %s to flash.\n", opts.WholeDevice)
	}

	return opts, nil
}

// initializeRemoteOstree sets up the ostree remote and pulls the specified ref.
func (c *ImageCommand) initializeRemoteOstree() error {
	if err := c.showRemoteRefs(); err != nil {
		return err
	}

	remote, err := c.ot.Remote()
	if err != nil {
		return err
	}

	c.im.Print("\n%s%sPulling ostree ref %s:%s ...%s\n",
		c.cBold, c.iconDownload, remote, c.ot.Ref(), c.cReset)
	if err := c.ot.Pull(); err != nil {
		return fmt.Errorf("ostree pull failed: %w", err)
	}
	return nil
}

// showLocalRefs prints the local ostree refs to the provided printf function.
func (c *ImageCommand) showLocalRefs() error {
	return showLocalRefs(c.ot, c.im.Print)
}

// showRemoteRefs prints the remote ostree refs to the provided printf function.
func (c *ImageCommand) showRemoteRefs() error {
	return showRemoteRefs(c.ot, c.im.Print)
}
