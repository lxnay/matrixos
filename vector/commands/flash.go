package commands

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"time"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/imager"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/validation"
)

var (
	devPathPattern       = regexp.MustCompile(`^/dev/`)
	confirmPattern       = regexp.MustCompile(`(?i)^(ack|lgtm|yes|y|no)$`)
	confirmAcceptPattern = regexp.MustCompile(`(?i)^(ack|lgtm|yes|y)$`)
	// sleepFn is the sleep function used for countdown delays.
	// Overridable in tests.
	sleepFn = time.Sleep

	// installRoot is the root path passed to VerifyImagerEnvironmentSetup.
	// Overridable in tests.
	installRoot = "/"
)

// FlashCommand installs the currently running (or specified) matrixOS system
// to a block device or set of partitions — the Go equivalent of install.device.
type FlashCommand struct {
	BaseCommand
	UI
	SignalGuard
	fs *flag.FlagSet

	// Library instances
	im    imager.IImager
	fsenc filesystems.IFsenc
	qa    *validation.QA

	// Flags
	batch          bool
	dryRun         bool
	ref            string
	repoDir        string
	wholeDevice    string
	efiDevicePath  string
	bootDevicePath string
	rootDevicePath string

	// Interactive prompt source (overridable for tests)
	prompter *Prompter
}

// NewFlashCommand creates a new FlashCommand.
func NewFlashCommand() *FlashCommand {
	return &FlashCommand{}
}

// Name returns the command name.
func (c *FlashCommand) Name() string {
	return "flash"
}

// Init parses flags, loads config and ostree, and prepares the UI.
func (c *FlashCommand) Init(args []string) error {
	if err := c.parseArgs(args); err != nil {
		return err
	}

	if err := c.initClientConfig(); err != nil {
		return err
	}

	// When an explicit --ostree-repo is given, override the repo dir
	// once, before ostree init, so the whole command sees it.
	if c.repoDir != "" {
		overlay := map[string][]string{
			"Ostree.RepoDir": {c.repoDir},
		}
		if err := c.cfg.AddOverlay(overlay); err != nil {
			return fmt.Errorf("failed to override ostree repo: %w", err)
		}
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

// parseArgs registers and parses command-line flags.
func (c *FlashCommand) parseArgs(args []string) error {
	c.fs = flag.NewFlagSet("flash", flag.ContinueOnError)

	c.fs.BoolVar(&c.batch, "batch", false, "Run in batch mode (non-interactive)")
	c.fs.BoolVar(&c.batch, "b", false, "Run in batch mode (non-interactive)")
	c.fs.BoolVar(&c.dryRun, "dry-run", false, "Don't make any changes, just show what would happen")
	c.fs.StringVar(&c.ref, "ref", "", "[DEV] ostree ref to install")
	c.fs.StringVar(&c.repoDir, "ostree-repo", "", "[DEV] path to ostree repository")
	c.fs.StringVar(&c.wholeDevice, "install-device", "", "Whole block device to wipe and install (e.g. /dev/sda)")
	c.fs.StringVar(&c.efiDevicePath, "efi-device-path", "", "EFI System Partition path (will not be formatted)")
	c.fs.StringVar(&c.bootDevicePath, "boot-device-path", "", "Boot device path (DATA WIPED)")
	c.fs.StringVar(&c.rootDevicePath, "root-device-path", "", "Root device path (DATA WIPED)")

	c.fs.Usage = func() {
		fmt.Println("Usage: vector flash [options]")
		fmt.Println()
		fmt.Println("Flash (install) the current matrixOS system to a block device or partitions.")
		fmt.Println()
		fmt.Println("Options:")
		c.fs.PrintDefaults()
	}

	if err := c.fs.Parse(args); err != nil {
		return err
	}

	if err := requireRoot(); err != nil {
		return err
	}

	return nil
}

// Run delegates to runFlash inside a SignalGuard.
func (c *FlashCommand) Run() error {
	return c.RunWithGuard(c.runFlash)
}

// runFlash implements the main flash/install logic.
func (c *FlashCommand) runFlash() error {
	c.SetupPrinters(c.Name())

	ref, err := c.resolveRef()
	if err != nil {
		return err
	}

	// Narrow the prefix now that we know the ref.
	c.SetupPrinters(fmt.Sprintf("flash:%s", c.shortRef(ref)))
	defer c.FlushPrinters()

	c.ot.SetStdout(c.StdoutWriter())
	c.ot.SetStderr(c.StderrWriter())
	c.ot.SetVerbose(false)

	if err := c.qa.VerifyImagerEnvironmentSetup(installRoot); err != nil {
		return fmt.Errorf("environment verification failed: %w", err)
	}

	fsenc, err := filesystems.NewFsenc(
		c.cfg,
		func(n string) { c.Printf("Opening encrypted rootfs as %s ...\n", n) },
		func(n string) { c.Printf("Opened encrypted rootfs as %s.\n", n) },
	)
	if err != nil {
		return fmt.Errorf("failed to initialize fsenc: %w", err)
	}
	fsenc.SetStdout(c.StdoutWriter())
	fsenc.SetStderr(c.StderrWriter())
	c.fsenc = fsenc

	if err := c.fsenc.ValidateLuksVariables(); err != nil {
		return fmt.Errorf("LUKS validation failed: %w", err)
	}

	if c.im == nil {
		im, err := imager.NewImager(c.cfg, c.ot, c.fsenc, nil)
		if err != nil {
			return fmt.Errorf("failed to initialize imager: %w", err)
		}
		c.im = im
	}
	c.im.SetStdout(c.StdoutWriter())
	c.im.SetStderr(c.StderrWriter())

	buildOpts, err := c.resolveDevices()
	if err != nil {
		return err
	}

	if err := c.detectRemotedAndPlainRefs(c.im.PrintError); err != nil {
		return err
	}

	rr, err := c.resolveRefRemote(ref, c.im.PrintWarning)
	if err != nil {
		return err
	}
	ref = rr.Ref

	c.im.SetRef(ref)
	c.ot.SetRef(ref)

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

	if err := c.showLocalRefs(); err != nil {
		return err
	}

	if c.dryRun {
		c.Printf("\n%sDRY RUN: would install ref %s to the selected devices.%s\n",
			c.cYellow, ref, c.cReset)
		c.Printf("No changes made.\n")
		return nil
	}

	return c.im.Build(buildOpts)
}

// resolveRef determines the ostree ref to install.
// When --ref and --ostree-repo are given, validates them against local refs.
// Otherwise falls back to the currently booted ref.
func (c *FlashCommand) resolveRef() (string, error) {
	refGiven := c.ref != ""
	repoGiven := c.repoDir != ""

	if refGiven != repoGiven {
		return "", fmt.Errorf("please specify both --ref and --ostree-repo")
	}

	if refGiven {
		return c.resolveExplicitRef()
	}
	return c.resolveBootedRef()
}

// resolveExplicitRef validates the user-supplied --ref against the specified repo.
func (c *FlashCommand) resolveExplicitRef() (string, error) {
	ref := c.ref

	if ostree.IsBranchShortName(ref) {
		osName, err := c.ot.OsName()
		if err != nil {
			return "", fmt.Errorf("failed to get OS name: %w", err)
		}
		arch, err := c.ot.Arch()
		if err != nil {
			return "", fmt.Errorf("failed to get architecture: %w", err)
		}
		expanded, err := ostree.BranchShortnameToNormal("dev", ref, osName, arch)
		if err != nil {
			return "", fmt.Errorf("failed to expand branch shortname: %w", err)
		}
		c.PrintErrf("WARNING: branch shortname specified, assuming dev release stage: %s\n", expanded)
		ref = expanded
	}

	if !filesystems.DirectoryExists(c.repoDir) {
		return "", fmt.Errorf("ostree repo %s does not exist", c.repoDir)
	}

	localRefs, err := c.ot.LocalRefs()
	if err != nil {
		return "", fmt.Errorf("failed to list local refs: %w", err)
	}

	found := false
	for _, lr := range localRefs {
		if lr == ref {
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("ref %s not found in local repo %s", ref, c.repoDir)
	}

	c.Printf("Using ostree repo %s with ref: %s\n", c.repoDir, ref)
	return ref, nil
}

// resolveBootedRef reads the currently booted ostree ref.
// The client config already provides the correct default RepoDir.
func (c *FlashCommand) resolveBootedRef() (string, error) {
	ref, err := c.ot.BootedRef()
	if err != nil {
		return "", fmt.Errorf("unable to determine booted ostree ref: %w", err)
	}
	if ref == "" {
		return "", fmt.Errorf("unable to find booted ostree ref")
	}

	c.Printf("Currently booted branch: %s\n", ref)
	return ref, nil
}

// resolveDevices determines the target device(s) for installation, either
// from flags (batch) or via interactive prompts.
func (c *FlashCommand) resolveDevices() (*imager.BuildOptions, error) {
	opts := &imager.BuildOptions{
		EfiDevice:   c.efiDevicePath,
		BootDevice:  c.bootDevicePath,
		RootDevice:  c.rootDevicePath,
		WholeDevice: c.wholeDevice,
	}

	if err := validateDeviceExistence(opts); err != nil {
		return nil, err
	}

	if err := c.validateDeviceCombination(opts); err != nil {
		return nil, err
	}

	if !c.batch {
		return c.resolveDevicesInteractive(opts)
	}
	return c.resolveDevicesBatch(opts)
}

// validateDeviceExistence checks that any specified device paths actually exist.
func validateDeviceExistence(opts *imager.BuildOptions) error {
	for _, pair := range []struct {
		path, label string
	}{
		{opts.EfiDevice, "EFI device"},
		{opts.BootDevice, "boot device"},
		{opts.RootDevice, "root device"},
		{opts.WholeDevice, "install device"},
	} {
		if pair.path != "" && !filesystems.PathExists(pair.path) {
			return fmt.Errorf("%s %s does not exist", pair.label, pair.path)
		}
	}
	return nil
}

// validateBuildDeviceCombination checks that the device flags form a valid
// combination: either whole device, or all three individual partitions, or none.
func validateBuildDeviceCombination(opts *imager.BuildOptions) error {
	anyPartition := opts.EfiDevice != "" || opts.BootDevice != "" || opts.RootDevice != ""
	allPartitions := opts.EfiDevice != "" && opts.BootDevice != "" && opts.RootDevice != ""

	if anyPartition && !allPartitions {
		return fmt.Errorf("please specify all --*-device-path flags or none")
	}
	if opts.WholeDevice != "" && anyPartition {
		return fmt.Errorf("please specify either --install-device or individual --*-device-path flags, not both")
	}
	return nil
}

// validateDeviceCombination checks that the flags form a valid combination
// for flash mode (including requiring devices in batch mode).
func (c *FlashCommand) validateDeviceCombination(opts *imager.BuildOptions) error {
	if err := validateBuildDeviceCombination(opts); err != nil {
		return err
	}
	anyPartition := opts.EfiDevice != "" || opts.BootDevice != "" || opts.RootDevice != ""
	if c.batch && opts.WholeDevice == "" && !anyPartition {
		return fmt.Errorf("batch mode requires device flags; see --help")
	}
	return nil
}

// resolveDevicesBatch validates batch-mode flags and displays a summary.
func (c *FlashCommand) resolveDevicesBatch(opts *imager.BuildOptions) (*imager.BuildOptions, error) {
	c.Println("Executing in batch mode ...")
	c.showSummary(opts)
	c.Printf("\nProceeding with installation in 30 seconds (CTRL+C to abort) ...\n")

	if !c.dryRun {
		sleepFn(30 * time.Second)
	}
	return opts, nil
}

// resolveDevicesInteractive prompts the user for target devices when not
// running in batch mode.
func (c *FlashCommand) resolveDevicesInteractive(opts *imager.BuildOptions) (*imager.BuildOptions, error) {
	prompt := c.getPrompter()

	c.Println("Executing in interactive mode ...")

	allSet := opts.EfiDevice != "" && opts.BootDevice != "" && opts.RootDevice != ""
	if !allSet && opts.WholeDevice == "" {
		answer, err := prompt.AskInput(
			"Install on a whole block device? (e.g. /dev/sda). Type yes?",
			"no", confirmPattern)
		if err != nil {
			return nil, err
		}

		if confirmAcceptPattern.MatchString(answer) {
			if err := c.promptWholeDevice(opts); err != nil {
				return nil, err
			}
		} else {
			c.Println("Installing into pre-existing partitions ...")
			if err := c.promptPartitions(opts); err != nil {
				return nil, err
			}
		}
	}

	c.showSummary(opts)

	confirm, err := prompt.AskInput(
		"Does that look good? Type yes?",
		"no", confirmPattern)
	if err != nil {
		return nil, err
	}
	if !confirmAcceptPattern.MatchString(confirm) {
		return nil, fmt.Errorf("operation cancelled by user")
	}

	c.Printf("Proceeding in 5 seconds ...\n")
	if !c.dryRun {
		sleepFn(5 * time.Second)
	}

	return opts, nil
}

// promptWholeDevice asks the user for a whole block device path.
func (c *FlashCommand) promptWholeDevice(opts *imager.BuildOptions) error {
	prompt := c.getPrompter()
	for opts.WholeDevice == "" {
		dev, err := prompt.AskInput(
			"Block device to wipe and install on? (e.g. /dev/sda or /dev/nvme0n1)",
			"", devPathPattern)
		if err != nil {
			return err
		}
		if dev == "" || !filesystems.PathExists(dev) {
			c.PrintErrf("Device %s does not exist. Try again.\n", dev)
			continue
		}
		opts.WholeDevice = dev
	}
	return nil
}

// promptPartitions asks the user for each individual partition path.
func (c *FlashCommand) promptPartitions(opts *imager.BuildOptions) error {
	pTypes, err := c.loadPartitionTypes()
	if err != nil {
		return err
	}

	if err := c.promptPartition(&opts.EfiDevice, "ESP", pTypes.esp); err != nil {
		return err
	}
	if err := c.promptPartition(&opts.BootDevice, "boot", pTypes.boot); err != nil {
		return err
	}
	return c.promptPartition(&opts.RootDevice, "root", pTypes.root)
}

// partitionTypes holds the expected GPT partition type GUIDs.
type partitionTypes struct {
	esp  string
	boot string
	root string
}

// loadPartitionTypes reads partition type GUIDs from the imager config.
func (c *FlashCommand) loadPartitionTypes() (*partitionTypes, error) {
	icfg := imager.NewImagerConfig(c.cfg)
	esp, err := icfg.EspPartitionType()
	if err != nil {
		return nil, fmt.Errorf("failed to read ESP partition type: %w", err)
	}
	boot, err := icfg.BootPartitionType()
	if err != nil {
		return nil, fmt.Errorf("failed to read boot partition type: %w", err)
	}
	root, err := icfg.RootPartitionType()
	if err != nil {
		return nil, fmt.Errorf("failed to read root partition type: %w", err)
	}
	return &partitionTypes{esp: esp, boot: boot, root: root}, nil
}

// promptPartition interactively asks for a partition device and validates
// its GPT type GUID.
func (c *FlashCommand) promptPartition(target *string, label, expectedType string) error {
	prompt := c.getPrompter()
	if *target != "" {
		return checkPartitionType(*target, expectedType)
	}
	for *target == "" {
		dev, err := prompt.AskInput(
			fmt.Sprintf("Partition for %s? (e.g. /dev/sda1 or /dev/nvme0n1p1)", label),
			"", devPathPattern)
		if err != nil {
			return err
		}
		if dev == "" || !filesystems.PathExists(dev) {
			c.PrintErrf("Partition %s does not exist. Try again.\n", dev)
			continue
		}
		if err := checkPartitionType(dev, expectedType); err != nil {
			c.PrintErrf("%v\n", err)
			continue
		}
		*target = dev
	}
	return nil
}

// checkPartitionType verifies that a device has the expected GPT partition type.
func checkPartitionType(dev, expected string) error {
	ptype, err := filesystems.PartitionType(dev)
	if err != nil {
		return fmt.Errorf("cannot determine partition type for %s: %w", dev, err)
	}
	if ptype != expected {
		return fmt.Errorf(
			"partition type GUID for %s is %s, expected %s",
			dev, ptype, expected)
	}
	return nil
}

// showSummary prints a human-readable summary of what will be installed.
func (c *FlashCommand) showSummary(opts *imager.BuildOptions) {
	osName := "matrixOS"
	if n, err := c.ot.OsName(); err == nil && n != "" {
		osName = n
	}

	c.Println("Installation summary:")
	if opts.WholeDevice != "" {
		c.Printf("  %s — wipe and install %s on the whole disk.\n",
			opts.WholeDevice, osName)
	} else {
		c.Printf("  %s (ESP)  — mount and place bootloader files (NOT formatted).\n", opts.EfiDevice)
		c.Printf("  %s (BOOT) — format and install /boot. DATA WILL BE LOST.\n", opts.BootDevice)
		c.Printf("  %s (ROOT) — format and install /. DATA WILL BE LOST.\n", opts.RootDevice)
	}
	c.Println("Note: final partition resizing will happen on first boot.")
}

// showLocalRefs lists the locally available ostree refs.
func (c *FlashCommand) showLocalRefs() error {
	return showLocalRefs(c.ot, c.im.Print)
}

// getPrompter returns the interactive prompter, creating one if needed.
func (c *FlashCommand) getPrompter() *Prompter {
	if c.prompter != nil {
		return c.prompter
	}
	c.prompter = NewPrompter(os.Stdin, c.StdoutWriter(), c.StderrWriter(), &c.UI)
	return c.prompter
}
