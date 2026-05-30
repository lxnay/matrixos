package commands

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
)

// mountInfo holds the UUID and filesystem type for a mountpoint.
type mountInfo struct {
	UUID   string
	FSType string
}

// jailbreakRunner abstracts OS-level operations so they can be replaced in tests.
type jailbreakRunner struct {
	// execCommand wraps exec.Command for spawning processes.
	execCommand func(name string, args ...string) cmdRunner
	// readFile reads a file's content.
	readFile func(path string) ([]byte, error)
	// writeFile writes data to a file with the given permissions.
	writeFile func(path string, data []byte, perm os.FileMode) error
	// appendFile appends data to a file.
	appendFile func(path string, data []byte) error
	// mkdirAll creates directories recursively.
	mkdirAll func(path string, perm os.FileMode) error
	// stat returns file info for a path.
	stat func(path string) (os.FileInfo, error)
	// removeFile removes a file.
	removeFile func(path string) error
	// remove recursively removes a path.
	removeAll func(path string) error
	// rename renames a file.
	rename func(src, dst string) error
	// realpath resolves a path to its real absolute form.
	realpath func(path string) (string, error)
	// copyFile copies src to dst.
	copyFile func(src, dst string) error
	// getMountInfo returns UUID and filesystem type for a mountpoint.
	getMountInfo func(mnt string) (*mountInfo, error)
	// remountRW remounts a filesystem read-write.
	remountRW func(mnt string) error
	// stdin provides user input for confirmation prompts.
	stdin io.Reader
	// stdout is the writer for info output.
	stdout io.Writer
	// stderr is the writer for error/warning output.
	stderr io.Writer
}

// cmdRunner abstracts an exec.Cmd for testability.
type cmdRunner interface {
	Run() error
	Output() ([]byte, error)
	SetStdout(w io.Writer)
	SetStderr(w io.Writer)
}

// realCmdRunner wraps a real os/exec.Cmd.
type realCmdRunner struct {
	cmd interface {
		Run() error
		Output() ([]byte, error)
	}
	stdout *io.Writer
	stderr *io.Writer
}

func (r *realCmdRunner) Run() error              { return r.cmd.Run() }
func (r *realCmdRunner) Output() ([]byte, error) { return r.cmd.Output() }
func (r *realCmdRunner) SetStdout(w io.Writer)   { *r.stdout = w }
func (r *realCmdRunner) SetStderr(w io.Writer)   { *r.stderr = w }

func defaultRunner() *jailbreakRunner {
	return &jailbreakRunner{
		execCommand: func(name string, args ...string) cmdRunner {
			cmd := execCommand(name, args...)
			return &realCmdRunner{
				cmd:    cmd,
				stdout: &cmd.Stdout,
				stderr: &cmd.Stderr,
			}
		},
		readFile:  os.ReadFile,
		writeFile: os.WriteFile,
		appendFile: func(path string, data []byte) error {
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.Write(data)
			return err
		},
		mkdirAll:     os.MkdirAll,
		stat:         os.Stat,
		removeFile:   os.Remove,
		removeAll:    os.RemoveAll,
		rename:       filesystems.Move,
		realpath:     filepath.EvalSymlinks,
		copyFile:     filesystems.CopyFile,
		getMountInfo: getMountInfoFromSystem,
		remountRW:    filesystems.RemountReadWrite,
		stdin:        os.Stdin,
		stdout:       os.Stdout,
		stderr:       os.Stderr,
	}
}

// getMountInfoFromSystem uses findmnt to get UUID and FSTYPE for a mount path.
func getMountInfoFromSystem(mnt string) (*mountInfo, error) {
	if mnt == "" {
		return nil, fmt.Errorf("missing mount path parameter")
	}

	uuid, err := filesystems.MountpointToUUID(mnt)
	if err != nil {
		return nil, fmt.Errorf("cannot determine UUID for %s: %w", mnt, err)
	}

	fstype, err := filesystems.MountpointToFSType(mnt)
	if err != nil {
		return nil, fmt.Errorf("cannot determine FSTYPE for %s: %w", mnt, err)
	}

	return &mountInfo{UUID: uuid, FSType: fstype}, nil
}

// JailbreakCommand converts a OSTree deployment into a mutable Gentoo install.
type JailbreakCommand struct {
	BaseCommand
	UI
	fs                     *flag.FlagSet
	run                    *jailbreakRunner
	prompt                 *Prompter
	yoloSkipFullBranchChk  bool
	yoloSkipDestroyConfirm bool
}

// NewJailbreakCommand creates a new JailbreakCommand.
func NewJailbreakCommand() *JailbreakCommand {
	return &JailbreakCommand{}
}

// Name returns the name of the command.
func (c *JailbreakCommand) Name() string {
	return "jailbreak"
}

// Init initializes the command.
func (c *JailbreakCommand) Init(args []string) error {
	if err := c.parseArgs(args); err != nil {
		return err
	}

	if err := c.initClientConfig(); err != nil {
		return err
	}
	if err := c.initOstree(); err != nil {
		return err
	}
	c.StartUI()
	c.SetupPrinters(c.Name())
	c.run = defaultRunner()
	c.run.stdout = c.StdoutWriter()
	c.run.stderr = c.StderrWriter()
	c.prompt = NewPrompter(c.run.stdin, c.run.stdout, c.run.stderr, &c.UI)
	return nil
}

func (c *JailbreakCommand) parseArgs(args []string) error {
	c.fs = flag.NewFlagSet("jailbreak", flag.ContinueOnError)
	c.fs.BoolVar(&c.yoloSkipFullBranchChk, "yolo-skip-full-branch-check", false,
		"skip the check that requires being on a -full branch")
	c.fs.BoolVar(&c.yoloSkipDestroyConfirm, "yolo-skip-destroy-confirmation", false,
		"skip the DESTROYALL confirmation prompt")
	c.fs.Usage = func() {
		fmt.Printf("Usage: vector %s\n", c.Name())
		c.fs.PrintDefaults()
	}
	return c.fs.Parse(args)
}

// configStr fetches a mandatory string value from config, returning an error if missing.
func (c *JailbreakCommand) configStr(key string) (string, error) {
	val, err := c.cfg.GetItem(key)
	if err != nil {
		return "", fmt.Errorf("config key %s: %w", key, err)
	}
	if val == "" {
		return "", fmt.Errorf("config key %s is empty", key)
	}
	return val, nil
}

func (c *JailbreakCommand) Run() error {
	if getEuid() != 0 {
		return fmt.Errorf("this command must be run as root")
	}

	sysroot, err := c.configStr("Ostree.Sysroot")
	if err != nil {
		return err
	}
	bootRoot, err := c.configStr("Imager.BootRoot")
	if err != nil {
		return err
	}
	efiRoot, err := c.configStr("Imager.EfiRoot")
	if err != nil {
		return err
	}
	fullSuffix, err := c.configStr("Ostree.FullBranchSuffix")
	if err != nil {
		return err
	}

	c.printTitle(fullSuffix)
	c.printGiantWarning()

	if err := c.sanityChecks(sysroot, bootRoot, efiRoot, fullSuffix); err != nil {
		return err
	}
	if err := c.remountSysroot(sysroot); err != nil {
		return err
	}
	if err := c.cloneToSysroot(sysroot); err != nil {
		return err
	}
	if err := c.generateFstab(sysroot, bootRoot, efiRoot); err != nil {
		return err
	}
	if err := c.bootloaderSetup(bootRoot); err != nil {
		return err
	}
	if err := c.cleanConfig(sysroot, bootRoot, efiRoot); err != nil {
		return err
	}
	if err := c.cleanPackages(sysroot); err != nil {
		return err
	}

	fmt.Fprintf(c.run.stdout, "\n%s%sAll done. Try rebooting now. Good luck with emerge!%s\n",
		c.cGreen, c.iconCheck, c.cReset)
	return nil
}

func (c *JailbreakCommand) printTitle(fullSuffix string) {
	w := c.run.stderr
	fmt.Fprintf(w, "%s%s\n", c.cBold, c.separator)
	fmt.Fprintf(w, "    %s%smatrixOS JAILBREAKING: VECTOR/IMMUTABLE -> MUTABLE GENTOO%s\n",
		c.cBold, c.iconRocket, c.cReset)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "    ...aka. turn your system into a regular Gentoo install")
	fmt.Fprintln(w, "    so that you can feel like a real hacker!")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "   %s%sPLEASE MAKE SURE to run (and then reboot!), see README.md for more details:%s\n",
		c.cYellow, c.iconWarn, c.cReset)
	fmt.Fprintf(w, "    Show available remote branches:\n")
	fmt.Fprintf(w, "     # vector branch remote\n")
	fmt.Fprintf(w, "    Switch to your desired 'full' branch:\n")
	fmt.Fprintf(w, "     # vector branch switch matrixos/<your branch>-%s\n", fullSuffix)
	fmt.Fprintf(w, "%s%s\n", c.cBold, c.separator)
	fmt.Fprintln(w)
}

func (c *JailbreakCommand) printGiantWarning() {
	w := c.run.stderr
	fmt.Fprintf(w, "%s%sWARNING:%s This will clone the current OS to the physical disk\n",
		c.cRed, c.iconError, c.cReset)
	fmt.Fprintln(w, "and detach from the OSTree deployment. IT CANNOT BE UNDONE (easily).")
	fmt.Fprintf(w, "%s%sWARNING:%s If something goes wrong, you will end up with a DESTROYED\n",
		c.cRed, c.iconError, c.cReset)
	fmt.Fprintln(w, "system. So, BACK EVERYTHING UP and buckle up!")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "This operation clones the current system and should carry all your data over.")
	fmt.Fprintln(w, "However, it may contain bugs or wrong assumptions. The matrixOS team is not going")
	fmt.Fprintln(w, "to be responsible for your potentially incurred data loss and by running this tool")
	fmt.Fprintln(w, "you accept the risk.")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s%sTo perform the cloning, this tool will not use much additional space.%s\n",
		c.cBlue, c.iconDoc, c.cReset)
	fmt.Fprintln(w, "This means that you won't need 2x the used disk space to perform this operation.")
	fmt.Fprintln(w)
}

func (c *JailbreakCommand) sanityChecks(sysroot, bootRoot, efiRoot, fullSuffix string) error {
	if err := c.checkSysrootExists(sysroot); err != nil {
		return err
	}
	if !c.yoloSkipFullBranchChk {
		if err := c.checkOnFullBranch(fullSuffix); err != nil {
			return err
		}
	}
	if err := c.checkVdbExists(fullSuffix); err != nil {
		return err
	}
	if err := c.checkDiskSpace(sysroot); err != nil {
		return err
	}
	if err := c.checkMountInfo(sysroot, bootRoot, efiRoot); err != nil {
		return err
	}
	if !c.yoloSkipDestroyConfirm {
		if err := c.confirmDestroy(); err != nil {
			return err
		}
	}
	return nil
}

// checkSysrootExists verifies that the sysroot path exists.
func (c *JailbreakCommand) checkSysrootExists(sysroot string) error {
	if _, err := c.run.stat(sysroot); err != nil {
		return fmt.Errorf("%s does not exist", sysroot)
	}
	return nil
}

// checkOnFullBranch verifies that the booted deployment is on a -full branch.
func (c *JailbreakCommand) checkOnFullBranch(fullSuffix string) error {
	deployments, err := c.ot.ListDeployments()
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}
	for _, dep := range deployments {
		if dep.Booted && strings.Contains(dep.Refspec, fullSuffix) {
			return nil
		}
	}

	fmt.Fprintf(c.run.stderr,
		"%s%sYou have not switched to a -%s branch. Please read the instructions above.%s\n",
		c.cYellow, c.iconWarn, fullSuffix, c.cReset)
	fmt.Fprintf(c.run.stderr, "%s%sShowing available full branches:%s\n",
		c.cBlue, c.iconSearch, c.cReset)
	refs, err := c.ot.RemoteRefs()
	if err == nil {
		for _, ref := range refs {
			if strings.HasSuffix(ref, "-"+fullSuffix) {
				fmt.Fprintf(c.run.stderr, "  %s%s%s\n", c.cCyan, ref, c.cReset)
			}
		}
	}
	return fmt.Errorf("not on a -%s branch", fullSuffix)
}

// checkVdbExists verifies that the package database exists (either the
// read-only VDB or /var/db/pkg).
func (c *JailbreakCommand) checkVdbExists(fullSuffix string) error {
	roVdb, err := c.cfg.GetItem("Releaser.ReadOnlyVdb")
	if err != nil {
		return fmt.Errorf("failed to get Releaser.ReadOnlyVdb: %w", err)
	}
	_, roVdbErr := c.run.stat(roVdb)
	_, varDbErr := c.run.stat(ostree.RwVdbPath)
	if roVdbErr != nil && varDbErr != nil {
		return fmt.Errorf("you have not switched to a -%s branch or must reboot first", fullSuffix)
	}
	return nil
}

// checkDiskSpace verifies that at least 4 GiB of disk space is available.
func (c *JailbreakCommand) checkDiskSpace(sysroot string) error {
	dfCmd := c.run.execCommand("df", sysroot, "--output=avail", "--block-size=1000")
	dfOut, err := dfCmd.Output()
	if err != nil {
		fmt.Fprintf(c.run.stderr, "%s%sUnable to determine the free space available. Use at your own risk%s\n",
			c.cYellow, c.iconWarn, c.cReset)
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(dfOut)), "\n")
	if len(lines) >= 2 {
		avail := strings.TrimSpace(lines[len(lines)-1])
		var space int64
		fmt.Sscanf(avail, "%d", &space)
		if space > 0 && space < 4000000 {
			return fmt.Errorf("less than 4GiB of space available, cannot continue")
		}
	}
	return nil
}

// checkMountInfo verifies that mount info can be obtained for all critical paths.
func (c *JailbreakCommand) checkMountInfo(sysroot, bootRoot, efiRoot string) error {
	for _, dev := range []string{sysroot, bootRoot, efiRoot} {
		if _, err := c.run.getMountInfo(dev); err != nil {
			return fmt.Errorf("mount info check failed for %s: %w", dev, err)
		}
	}
	return nil
}

// confirmDestroy prompts the user for the DESTROYALL confirmation.
func (c *JailbreakCommand) confirmDestroy() error {
	input, err := c.prompt.AskInput("Type 'DESTROYALL' to continue", "", nil)
	if err != nil {
		return fmt.Errorf("aborted: %w", err)
	}
	if input != "DESTROYALL" {
		return fmt.Errorf("aborted")
	}
	return nil
}

func (c *JailbreakCommand) remountSysroot(sysroot string) error {
	fmt.Fprintf(c.run.stdout, "%s%sRemounting physical root filesystem read/write ...%s\n",
		c.cBold, c.iconGear, c.cReset)
	return c.run.remountRW(sysroot)
}

func (c *JailbreakCommand) cloneToSysroot(sysroot string) error {
	// Find currently booted deployment.
	deployments, err := c.ot.ListDeployments()
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}
	var booted *ostree.Deployment
	for i := range deployments {
		if deployments[i].Booted {
			booted = &deployments[i]
			break
		}
	}
	if booted == nil {
		return fmt.Errorf("unable to find booted deployment")
	}

	osName, err := c.configStr("matrixOS.OsName")
	if err != nil {
		return err
	}

	deploymentDir := filepath.Join(
		"/ostree/deploy", osName, "deploy",
		booted.Checksum+"."+fmt.Sprint(booted.Serial),
	)
	if _, err := c.run.stat(deploymentDir); err != nil {
		return fmt.Errorf("unable to find deployment dir %s: %w", deploymentDir, err)
	}

	fmt.Fprintf(c.run.stdout, "%s%sFound currently deployed commit: %s%s\n",
		c.cBlue, c.iconSearch, booted.Checksum, c.cReset)
	fmt.Fprintf(c.run.stdout, "%s%sCloning your current install of matrixOS to %s ...%s\n",
		c.cBold, c.iconDownload, sysroot, c.cReset)

	// Use cpio to clone, excluding certain paths.
	cpioCmd := c.run.execCommand("sh", "-c",
		fmt.Sprintf(
			`cd %q && find . -xdev -depth `+
				`-not -path "./sysroot*" `+
				`-not -path "./ostree*" `+
				`-not -path "./mnt*" `+
				`-not -path "./var/lib/nfs*" `+
				`-not -path "./tmp*" `+
				`-not -path "./run*" `+
				`-not -path "./sys*" `+
				`-not -path "./proc*" `+
				`-not -path "./dev*" `+
				`-printf '%%P\0' | cpio --null -pd0lu %q`,
			deploymentDir, sysroot,
		),
	)
	cpioCmd.SetStdout(c.run.stdout)
	cpioCmd.SetStderr(c.run.stderr)
	if err := cpioCmd.Run(); err != nil {
		return fmt.Errorf("failed to clone deployment to sysroot: %w", err)
	}

	// Copy /var separately — in OSTree, /var lives outside the deployment
	// directory (at /ostree/deploy/<osname>/var) and is bind-mounted in.
	// The -xdev flag above prevents find from crossing into it, so user
	// data under /var/home is silently skipped without this extra pass.
	varDir := filepath.Join("/ostree/deploy", osName, "var")
	if _, err := c.run.stat(varDir); err == nil {
		fmt.Fprintf(c.run.stdout, "%s%sCopying /var from %s ...%s\n",
			c.cBlue, c.iconDownload, varDir, c.cReset)

		sysrootVar := filepath.Join(sysroot, "var")
		if err := c.run.mkdirAll(sysrootVar, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", sysrootVar, err)
		}

		cpioVarCmd := c.run.execCommand("sh", "-c",
			fmt.Sprintf(
				`cd %q && find . -xdev -depth `+
					`-not -path "./lib/nfs*" `+
					`-not -path "./tmp*" `+
					`-printf '%%P\0' | cpio --null -pd0lu %q`,
				varDir, sysrootVar,
			),
		)
		cpioVarCmd.SetStdout(c.run.stdout)
		cpioVarCmd.SetStderr(c.run.stderr)
		if err := cpioVarCmd.Run(); err != nil {
			return fmt.Errorf("failed to clone /var to sysroot: %w", err)
		}
	}

	// Restore /efi and /boot directories.
	for _, dir := range []string{"efi", "boot"} {
		p := filepath.Join(sysroot, dir)
		fmt.Fprintf(c.run.stdout, "%s%sRestoring /%s...%s\n",
			c.cBlue, c.iconGear, dir, c.cReset)
		if err := c.run.mkdirAll(p, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", p, err)
		}
	}

	// Remove immutable bits.
	fmt.Fprintf(c.run.stdout, "%s%sUnlocking files (removing immutable bit) ...%s\n",
		c.cBold, c.iconGear, c.cReset)
	chattrCmd := c.run.execCommand("chattr", "-R", "-i", sysroot+"/")
	chattrCmd.Run() // best effort

	return nil
}

func (c *JailbreakCommand) generateFstab(sysroot, bootRoot, efiRoot string) error {
	fmt.Fprintf(c.run.stdout, "%s%sGenerating /etc/fstab ...%s\n",
		c.cBold, c.iconGear, c.cReset)

	rootInfo, err := c.run.getMountInfo(sysroot)
	if err != nil {
		return fmt.Errorf("fstab: %w", err)
	}
	bootInfo, err := c.run.getMountInfo(bootRoot)
	if err != nil {
		return fmt.Errorf("fstab: %w", err)
	}
	efiInfo, err := c.run.getMountInfo(efiRoot)
	if err != nil {
		return fmt.Errorf("fstab: %w", err)
	}

	fstabPath := filepath.Join(sysroot, "etc", "fstab")
	var fstab strings.Builder
	fmt.Fprintf(&fstab, "UUID=%s / %s defaults 0 1\n", rootInfo.UUID, rootInfo.FSType)
	fmt.Fprintf(&fstab, "UUID=%s %s %s defaults 0 1\n", bootInfo.UUID, bootRoot, bootInfo.FSType)
	fmt.Fprintf(&fstab, "UUID=%s %s %s defaults 0 1\n", efiInfo.UUID, efiRoot, efiInfo.FSType)

	return c.run.appendFile(fstabPath, []byte(fstab.String()))
}

// bootloaderSetup orchestrates the full bootloader configuration.
func (c *JailbreakCommand) bootloaderSetup(bootRoot string) error {
	kernelBootArgs, err := c.parseKernelBootArgs()
	if err != nil {
		return err
	}

	release, err := c.kernelRelease()
	if err != nil {
		return err
	}

	bootKernelPath, initramfsBootPath, err := c.copyKernelAndInitramfs(release)
	if err != nil {
		return err
	}

	return c.writeBootloaderEntry(bootRoot, bootKernelPath, initramfsBootPath, kernelBootArgs)
}

// parseKernelBootArgs reads /proc/cmdline and returns the filtered list of
// kernel boot arguments suitable for re-use in a fresh BLS entry.
func (c *JailbreakCommand) parseKernelBootArgs() ([]string, error) {
	cmdlineData, err := c.run.readFile("/proc/cmdline")
	if err != nil {
		return nil, fmt.Errorf("failed to read /proc/cmdline: %w", err)
	}

	var kernelBootArgs []string
	for _, arg := range strings.Fields(strings.TrimSpace(string(cmdlineData))) {
		switch {
		case strings.HasPrefix(arg, "BOOT_IMAGE="):
			continue
		case arg == "rw":
			continue
		case strings.HasPrefix(arg, "ostree="):
			continue
		case strings.HasPrefix(arg, "systemd.mount-extra="):
			continue
		default:
			kernelBootArgs = append(kernelBootArgs, arg)
		}
	}
	return kernelBootArgs, nil
}

// kernelRelease returns the running kernel release string (equivalent to
// `uname -r`).
func (c *JailbreakCommand) kernelRelease() (string, error) {
	data, err := c.run.readFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return "", fmt.Errorf("failed to read kernel release: %w", err)
	}
	release := strings.TrimSpace(string(data))
	if release == "" {
		return "", fmt.Errorf("kernel release is empty")
	}
	return release, nil
}

// copyKernelAndInitramfs copies the booted kernel (and, when present, the
// matching initramfs) from the canonical, bootloader-independent location
// /usr/lib/modules/<release>/ into /boot, returning the destination paths.
func (c *JailbreakCommand) copyKernelAndInitramfs(release string) (bootKernelPath, initramfsBootPath string, err error) {
	fmt.Fprintf(c.run.stdout, "%s%sCopying booted kernel ...%s\n",
		c.cBold, c.iconGear, c.cReset)

	modDir := filepath.Join("/usr/lib/modules", release)
	srcKernel := filepath.Join(modDir, "vmlinuz")
	bootKernelPath = filepath.Join("/boot", "kernel-"+release)
	if err := c.run.copyFile(srcKernel, bootKernelPath); err != nil {
		return "", "", fmt.Errorf("failed to copy kernel from %s: %w", srcKernel, err)
	}

	srcInitramfs := filepath.Join(modDir, "initramfs")
	if _, err := c.run.stat(srcInitramfs); err == nil {
		initramfsBootPath = filepath.Join("/boot", "initramfs-"+release)
		if err := c.run.copyFile(srcInitramfs, initramfsBootPath); err != nil {
			return "", "", fmt.Errorf("failed to copy initramfs from %s: %w", srcInitramfs, err)
		}
	} else {
		fmt.Fprintf(c.run.stderr, "%s%sInitramfs not found at %s, ignoring ...%s\n",
			c.cYellow, c.iconWarn, srcInitramfs, c.cReset)
	}

	return bootKernelPath, initramfsBootPath, nil
}

// writeBootloaderEntry generates and writes the BLS (Boot Loader Specification) entry.
func (c *JailbreakCommand) writeBootloaderEntry(bootRoot, bootKernelPath, initramfsBootPath string, kernelBootArgs []string) error {
	blsEntry, err := c.configStr("Jailbreak.BootLoaderEntry")
	if err != nil {
		return err
	}

	entriesDir := filepath.Join(bootRoot, "loader", "entries")
	if err := c.run.mkdirAll(entriesDir, 0755); err != nil {
		return fmt.Errorf("failed to create BLS entries dir: %w", err)
	}
	blsCfgPath := filepath.Join(entriesDir, blsEntry)

	fmt.Fprintf(c.run.stdout,
		"%s%sSetting up %s/loader/entries with kernel boot params: %s ...%s\n",
		c.cBold, c.iconGear, bootRoot, strings.Join(kernelBootArgs, " "), c.cReset)

	var bls strings.Builder
	fmt.Fprintln(&bls, "title matrixOS (Gentoo-based, jailbroken)")
	fmt.Fprintln(&bls, "version 1")
	fmt.Fprintf(&bls, "options %s\n", strings.Join(kernelBootArgs, " "))
	fmt.Fprintf(&bls, "linux %s\n", strings.TrimPrefix(bootKernelPath, bootRoot))
	if initramfsBootPath != "" {
		fmt.Fprintf(&bls, "initrd %s\n", strings.TrimPrefix(initramfsBootPath, bootRoot))
	}

	if err := c.run.writeFile(blsCfgPath, []byte(bls.String()), 0644); err != nil {
		return fmt.Errorf("failed to write BLS config: %w", err)
	}

	fmt.Fprintf(c.run.stdout, "%s%sFinal BLS bootloader config:%s\n",
		c.cBlue, c.iconDoc, c.cReset)
	fmt.Fprint(c.run.stdout, bls.String())
	fmt.Fprintln(c.run.stdout, c.separator)

	return nil
}

func (c *JailbreakCommand) cleanConfig(sysroot, bootRoot, efiRoot string) error {
	if err := c.cleanConfigSetupBLS(bootRoot); err != nil {
		return err
	}
	if err := c.cleanConfigSetupSystemdRepart(sysroot); err != nil {
		return err
	}
	if err := c.cleanConfigSetupVarDbPkg(sysroot); err != nil {
		return err
	}
	if err := c.cleanConfigFixSrv(sysroot); err != nil {
		return err
	}
	return c.cleanConfigSetupSecurebootKeys(efiRoot)
}

func (c *JailbreakCommand) cleanConfigSetupBLS(bootRoot string) error {
	fmt.Fprintf(c.run.stdout, "%s%sRemoving old %s/loader/entries/ configs ...%s\n",
		c.cBold, c.iconGear, bootRoot, c.cReset)
	for _, old := range []string{"ostree-1.conf", "ostree-2.conf"} {
		c.run.removeFile(filepath.Join(bootRoot, "loader", "entries", old))
	}
	return nil
}

func (c *JailbreakCommand) cleanConfigSetupSystemdRepart(sysroot string) error {
	fmt.Fprintf(c.run.stdout, "%s%sDisabling systemd-repart config ...%s\n",
		c.cBold, c.iconGear, c.cReset)
	c.run.removeFile(filepath.Join(sysroot, "etc", "repart.d", "50-matrixos-rootfs.conf"))
	return nil
}

func (c *JailbreakCommand) cleanConfigSetupVarDbPkg(sysroot string) error {
	fmt.Fprintf(c.run.stdout, "%s%sSetting up %s ...%s\n",
		c.cBold, c.iconGear, ostree.RwVdbPath, c.cReset)
	roVdb, err := c.configStr("Releaser.ReadOnlyVdb")
	if err != nil {
		return err
	}

	// Remove any symlink at /var/db/pkg, then move the read-only VDB into place.
	varDbPkg := filepath.Join(sysroot, ostree.RwVdbPath)
	if err := c.run.removeAll(varDbPkg); err != nil {
		return fmt.Errorf("failed to remove %s: %w", varDbPkg, err)
	}

	src := filepath.Join(sysroot, roVdb)
	return c.run.rename(src, varDbPkg)
}

func (c *JailbreakCommand) cleanConfigFixSrv(sysroot string) error {
	fmt.Fprintf(c.run.stdout, "%s%sFixing /srv ...%s\n",
		c.cBold, c.iconGear, c.cReset)
	srv := filepath.Join(sysroot, "srv")

	info, err := c.run.stat(srv)
	if err != nil {
		// Does not exist at all — create it.
		if err := c.run.mkdirAll(srv, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", srv, err)
		}
		return c.run.writeFile(filepath.Join(srv, ".keep"), []byte{}, 0644)
	}

	// If it's a dangling symlink, replace it with a directory.
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		if err := c.run.removeFile(srv); err != nil {
			return fmt.Errorf("failed to remove %s: %w", srv, err)
		}
		if err := c.run.mkdirAll(srv, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", srv, err)
		}
		return c.run.writeFile(filepath.Join(srv, ".keep"), []byte{}, 0644)
	}
	return nil
}

func (c *JailbreakCommand) cleanConfigSetupSecurebootKeys(efiRoot string) error {
	certPath, err := c.cfg.GetItem("Seeder.DefaultSecureBootPublicKey")
	if err != nil {
		fmt.Fprintf(c.run.stderr, "%s%sSecureBoot cert config not found: %v%s\n",
			c.cYellow, c.iconWarn, err, c.cReset)
		return nil
	}

	fmt.Fprintf(c.run.stdout, "%s%smatrixOS uses its own secureboot key pair.%s\n",
		c.cBlue, c.iconDoc, c.cReset)
	fmt.Fprintf(c.run.stdout, "%s%sFor jailbroken systems, we need to create a separate set of keys.%s\n",
		c.cBlue, c.iconDoc, c.cReset)

	if _, err := c.run.stat(efiRoot); err != nil {
		fmt.Fprintf(c.run.stderr, "%s%s%s not found, skipping SecureBoot automatic MOK generation%s\n",
			c.cYellow, c.iconWarn, efiRoot, c.cReset)
		return nil
	}

	if _, err := c.run.stat(certPath); err != nil {
		fmt.Fprintf(c.run.stderr, "%s%sUnable to find SecureBoot certificate at: %s%s\n",
			c.cYellow, c.iconWarn, certPath, c.cReset)
		return nil
	}

	fmt.Fprintf(c.run.stdout, "%s%sCreating a new MOK file to ease shim MOK keys loading ...%s\n",
		c.cBold, c.iconGear, c.cReset)
	mokPath := filepath.Join(efiRoot, "matrixos-jailbroken-secureboot-cert.mok")
	opensslCmd := c.run.execCommand("openssl", "x509", "-in", certPath, "-outform", "DER", "-out", mokPath)
	opensslCmd.SetStdout(c.run.stdout)
	opensslCmd.SetStderr(c.run.stderr)
	if err := opensslCmd.Run(); err != nil {
		fmt.Fprintf(c.run.stderr, "%s%sFailed to generate MOK file: %v%s\n",
			c.cYellow, c.iconWarn, err, c.cReset)
	}

	return nil
}

func (c *JailbreakCommand) cleanPackages(sysroot string) error {
	fmt.Fprintf(c.run.stdout, "%s%sCleaning packages ...%s\n",
		c.cBold, c.iconPackage, c.cReset)

	defaultUsername, _ := c.cfg.GetItem("matrixOS.DefaultUsername")

	// Check if user exists.
	idCmd := c.run.execCommand("id", "-u", defaultUsername)
	uidOut, err := idCmd.Output()
	uid := strings.TrimSpace(string(uidOut))

	// If user does not exist, unmerge the packages.
	if err != nil || uid == "" {
		pkgsToClean := []string{
			"acct-user/matrixos-live-home",
			"acct-user/matrixos-live",
			"virtual/matrixos-setup",
			"virtual/matrixos-devel",
		}
		args := append([]string{
			"--root=" + sysroot,
			"--depclean",
			"-v",
			"--with-bdeps=n",
		}, pkgsToClean...)
		emergeCmd := c.run.execCommand("emerge", args...)
		emergeCmd.SetStdout(c.run.stdout)
		emergeCmd.SetStderr(c.run.stderr)
		emergeCmd.Run() // best effort
	}

	return nil
}
