package commands

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"matrixos/vector/lib/filesystems"
)

var userRegex = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

const maxLuksPasswordAttempts = 5

// setupOSRunner abstracts OS-level operations so they can be replaced in tests.
type setupOSRunner struct {
	osRunner
	// interactiveStdout/interactiveStderr bypass line-buffered styledWriter so
	// interactive prompts (passwd, cryptsetup, systemd-firstboot) display
	// without waiting for a trailing newline. Nil falls back to stdout/stderr.
	interactiveStdout  io.Writer
	interactiveStderr  io.Writer
	mkdirTemp          func(dir, pattern string) (string, error)
	fileExists         func(path string) bool
	chmod              func(path string, mode os.FileMode) error
	chown              func(path string, uid, gid int) error
	getMountDevice     func(mnt string) (string, error)
	getPartitionNumber func(device string) (string, error)
	getPartitionLabel  func(device string) (string, error)
	getBlockDevice     func(device string) (string, error)
	listBlockDevices   func(fields string) ([]string, error)
	getBlkidValue      func(device, tag string) (string, error)
	getEuid            func() int
	getCurrentUser     func() string
	glob               func(pattern string) ([]string, error)
}

func (r *setupOSRunner) getInteractiveStdout() io.Writer {
	if r.interactiveStdout != nil {
		return r.interactiveStdout
	}
	return r.stdout
}

func (r *setupOSRunner) getInteractiveStderr() io.Writer {
	if r.interactiveStderr != nil {
		return r.interactiveStderr
	}
	return r.stderr
}

func defaultSetupOSRunner() *setupOSRunner {
	return &setupOSRunner{
		osRunner:          defaultOSRunner(),
		interactiveStdout: os.Stdout,
		interactiveStderr: os.Stderr,
		mkdirTemp:         os.MkdirTemp,
		fileExists: func(path string) bool {
			_, err := os.Stat(path)
			return err == nil
		},
		chmod:              os.Chmod,
		chown:              os.Chown,
		getMountDevice:     filesystems.MountpointToDevice,
		getPartitionNumber: filesystems.PartitionNumber,
		getPartitionLabel:  filesystems.PartitionLabel,
		getBlockDevice:     filesystems.BlockDeviceForPartition,
		listBlockDevices: func(fields string) ([]string, error) {
			cmd := execCommand("lsblk", "-no", fields)
			out, err := cmd.Output()
			if err != nil {
				return nil, err
			}
			var lines []string
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					lines = append(lines, line)
				}
			}
			return lines, nil
		},
		getBlkidValue: func(device, tag string) (string, error) {
			cmd := execCommand("blkid", "-s", tag, "-o", "value", device)
			out, err := cmd.Output()
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(string(out)), nil
		},
		getEuid:        getEuid,
		getCurrentUser: func() string { return os.Getenv("USER") },
		glob:           filepath.Glob,
	}
}

// SetupOSCommand is a command for setting up matrixOS (username, passwords, locale, etc.)
type SetupOSCommand struct {
	BaseCommand
	UI
	fs  *flag.FlagSet
	run *setupOSRunner

	// Flags.
	skipEncryption bool
	skipPasswords  bool
	usernameFlag   string
	localeFlag     string
	keymapFlag     string
	timezoneFlag   string
	hostnameFlag   string

	// prompt handles interactive user input.
	prompt *Prompter
}

// NewSetupOSCommand creates a new SetupOSCommand
func NewSetupOSCommand() *SetupOSCommand {
	return &SetupOSCommand{}
}

func (c *SetupOSCommand) Name() string {
	return "setupOS"
}

func (c *SetupOSCommand) Init(args []string) error {
	if err := c.parseArgs(args); err != nil {
		return err
	}

	if err := c.initClientConfig(); err != nil {
		return err
	}

	c.StartUI()
	c.SetupPrinters(c.Name())
	c.run = defaultSetupOSRunner()
	c.run.stdout = c.StdoutWriter()
	c.run.stderr = c.StderrWriter()
	c.prompt = NewPrompter(c.run.stdin, c.run.stdout, c.run.stderr, &c.UI)
	return nil
}

func (c *SetupOSCommand) parseArgs(args []string) error {
	c.fs = flag.NewFlagSet("setupOS", flag.ContinueOnError)
	c.fs.BoolVar(&c.skipEncryption, "skip-encryption", false, "Skip disk encryption password change step")
	c.fs.BoolVar(&c.skipPasswords, "skip-passwords", false, "Skip username change, user password and root password steps")
	c.fs.StringVar(&c.usernameFlag, "username", "", "Set the username directly without prompting (useful with --skip-passwords)")
	c.fs.StringVar(&c.localeFlag, "locale", "", "Set the system locale (e.g. en_US.UTF-8)")
	c.fs.StringVar(&c.keymapFlag, "keymap", "", "Set the console keymap (e.g. us)")
	c.fs.StringVar(&c.timezoneFlag, "timezone", "", "Set the system timezone (e.g. Europe/Rome)")
	c.fs.StringVar(&c.hostnameFlag, "hostname", "", "Set the system hostname")
	c.fs.Usage = func() {
		fmt.Printf("Usage: vector %s [flags]\n", c.Name())
		fmt.Println("  Setup matrixOS: configure username, passwords, locale, disk encryption and boot entries.")
		c.fs.PrintDefaults()
	}
	return c.fs.Parse(args)
}

func (c *SetupOSCommand) configStr(key string) (string, error) {
	return c.configItem(key)
}

func (c *SetupOSCommand) Run() error {
	defer c.FlushPrinters()

	if err := requireRootFn(c.run.getEuid); err != nil {
		return err
	}
	if c.run.getCurrentUser() != "root" {
		fmt.Fprintf(c.run.stderr,
			"%s%sPlease log into your Desktop Environment as root to perform this task.%s\n",
			c.cYellow, c.iconWarn, c.cReset)
		fmt.Fprintf(c.run.stderr,
			"%s%sThis is due to usermod needing to move your user home directory.%s\n",
			c.cYellow, c.iconWarn, c.cReset)
		return fmt.Errorf("must be logged in as root")
	}

	defaultUsername, err := c.configStr("matrixOS.DefaultUsername")
	if err != nil {
		return err
	}
	efiRoot, err := c.configStr("Imager.EfiRoot")
	if err != nil {
		return err
	}
	relativeEfiBoot, err := c.configStr("Imager.RelativeEfiBootPath")
	if err != nil {
		return err
	}
	fancyOsName, err := c.configStr("matrixOS.FancyOsName")
	if err != nil {
		return err
	}
	efiExecPath, err := c.configStr("Imager.EfiStandardBootExecutablePath")
	if err != nil {
		return err
	}
	jailbrokenEntry, err := c.configStr("Jailbreak.BootLoaderEntry")
	if err != nil {
		return err
	}

	fmt.Fprintf(c.run.stdout, "\n%s%smatrixOS Setup%s\n", c.cBold, c.iconGear, c.cReset)
	fmt.Fprintf(c.run.stdout, "%s\n\n", c.separator)

	// Step 1: LUKS password
	fmt.Fprintf(c.run.stdout, "%s%sStep 1: Disk Encryption%s\n", c.cBold, c.iconGear, c.cReset)
	if c.skipEncryption {
		fmt.Fprintf(c.run.stdout, "   %s%sSkipping disk encryption (--skip-encryption).%s\n",
			c.cBlue, c.iconCheck, c.cReset)
	} else {
		if err := c.changeLuksPassword(); err != nil {
			return fmt.Errorf("LUKS password change failed: %w", err)
		}
	}
	fmt.Fprintln(c.run.stdout)

	// Resolve the effective username for later steps.
	username := defaultUsername
	if c.usernameFlag != "" {
		username = c.usernameFlag
	}

	if c.skipPasswords {
		fmt.Fprintf(c.run.stdout, "   %s%sSkipping steps 2-4 (--skip-passwords), username=%s.%s\n\n",
			c.cBlue, c.iconCheck, username, c.cReset)
	} else {
		// Step 2: Username change
		if c.usernameFlag == "" {
			fmt.Fprintf(c.run.stdout, "%s%sStep 2: User Account%s\n", c.cBold, c.iconGear, c.cReset)
			newName, err := c.changeUsername(defaultUsername)
			if err != nil {
				return fmt.Errorf("username change failed: %w", err)
			}
			username = newName
		} else {
			fmt.Fprintf(c.run.stdout, "%s%sStep 2: User Account (--username=%s)%s\n", c.cBold, c.iconGear, c.usernameFlag, c.cReset)
			fmt.Fprintf(c.run.stdout, "   %s%sUsing provided username: %s%s\n",
				c.cBlue, c.iconCheck, username, c.cReset)
		}
		fmt.Fprintln(c.run.stdout)

		// Step 3: User password
		fmt.Fprintf(c.run.stdout, "%s%sStep 3: User Password%s\n", c.cBold, c.iconGear, c.cReset)
		if err := c.changeUserPassword(username); err != nil {
			return fmt.Errorf("user password change failed: %w", err)
		}
		fmt.Fprintln(c.run.stdout)

		// Step 4: Root password
		fmt.Fprintf(c.run.stdout, "%s%sStep 4: Root Password%s\n", c.cBold, c.iconGear, c.cReset)
		if err := c.changeUserPassword("root"); err != nil {
			return fmt.Errorf("root password change failed: %w", err)
		}
		fmt.Fprintln(c.run.stdout)
	}

	// Step 5: Localization
	fmt.Fprintf(c.run.stdout, "%s%sStep 5: Localization%s\n", c.cBold, c.iconGear, c.cReset)
	if err := c.setupLocalization(); err != nil {
		return fmt.Errorf("localization setup failed: %w", err)
	}
	fmt.Fprintln(c.run.stdout)

	// Step 6: AccountsService
	fmt.Fprintf(c.run.stdout, "%s%sStep 6: AccountsService%s\n", c.cBold, c.iconGear, c.cReset)
	if err := c.setupAccountsService(username); err != nil {
		fmt.Fprintf(c.run.stderr, "   %s%sAccountsService setup warning: %v%s\n",
			c.cYellow, c.iconWarn, err, c.cReset)
	}
	fmt.Fprintln(c.run.stdout)

	// Step 7: Keymap
	fmt.Fprintf(c.run.stdout, "%s%sStep 7: Keyboard Mapping%s\n", c.cBold, c.iconGear, c.cReset)
	if err := c.setupLocalizationKeymap(jailbrokenEntry); err != nil {
		fmt.Fprintf(c.run.stderr, "   %s%sKeymap setup warning: %v%s\n",
			c.cYellow, c.iconWarn, err, c.cReset)
	}
	fmt.Fprintln(c.run.stdout)

	// Step 8: Windows detection
	fmt.Fprintf(c.run.stdout, "%s%sStep 8: Windows Detection%s\n", c.cBold, c.iconSearch, c.cReset)
	if err := c.detectWindows(efiRoot, relativeEfiBoot); err != nil {
		fmt.Fprintf(c.run.stderr, "   %s%sWindows detection warning: %v%s\n",
			c.cYellow, c.iconWarn, err, c.cReset)
	}
	fmt.Fprintln(c.run.stdout)

	// Step 9: EFI boot entry
	fmt.Fprintf(c.run.stdout, "%s%sStep 9: EFI Boot Entry%s\n", c.cBold, c.iconRocket, c.cReset)
	if err := c.addOSBoot(efiRoot, fancyOsName, efiExecPath); err != nil {
		fmt.Fprintf(c.run.stderr, "   %s%sEFI boot entry warning: %v%s\n",
			c.cYellow, c.iconWarn, err, c.cReset)
	}

	fmt.Fprintf(c.run.stdout, "\n%s\n", c.separator)
	fmt.Fprintf(c.run.stdout, "%s%sSetup complete! Please reboot and enjoy the OS.%s\n",
		c.cGreen, c.iconCheck, c.cReset)
	return nil
}

// checkUsername verifies that the given username exists on the system.
func (c *SetupOSCommand) checkUsername(username string) error {
	cmd := c.run.execCommand("id", "-u", username)
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		fmt.Fprintf(
			c.run.stderr,
			"   %s%sUser %s does not exist (removed or already migrated).%s\n",
			c.cYellow, c.iconWarn, username, c.cReset,
		)
		fmt.Fprintf(
			c.run.stderr,
			"   %s%sTo re-run this step, use the --skip-passwords and --username flags.%s\n",
			c.cYellow, c.iconWarn, c.cReset,
		)
		return fmt.Errorf("user %q does not exist (removed or already migrated)", username)
	}
	return nil
}

// changeUsername handles renaming the default user account.
// Returns the final username (either the new name or the original if skipped).
func (c *SetupOSCommand) changeUsername(defaultUsername string) (string, error) {
	if err := c.checkUsername(defaultUsername); err != nil {
		fmt.Fprintf(c.run.stdout, "   %s%s%v%s\n", c.cYellow, c.iconWarn, err, c.cReset)
		return defaultUsername, nil
	}

	selectedUsername, err := c.prompt.AskInput(
		fmt.Sprintf("Enter desired username replacing %s, hit enter to skip", defaultUsername),
		defaultUsername, userRegex)
	if err != nil {
		return "", err
	}

	if selectedUsername == defaultUsername {
		fmt.Fprintf(c.run.stdout, "   %s%sSkipping username change.%s\n",
			c.cBlue, c.iconCheck, c.cReset)
		return defaultUsername, nil
	}

	selectedFullname, err := c.prompt.AskInput("Enter desired full name, hit enter to skip", "", nil)
	if err != nil {
		return "", err
	}

	// Rename group if it exists.
	checkGroup := c.run.execCommand("getent", "group", defaultUsername)
	if _, err := checkGroup.Output(); err == nil {
		fmt.Fprintf(c.run.stdout, "   %s%sRenaming group %s...%s\n",
			c.cBold, c.iconUpdate, defaultUsername, c.cReset)
		groupmodCmd := c.run.execCommand("groupmod", "-n", selectedUsername, defaultUsername)
		groupmodCmd.SetStdout(c.run.stdout)
		groupmodCmd.SetStderr(c.run.stderr)
		if err := groupmodCmd.Run(); err != nil {
			return "", fmt.Errorf("failed to rename group: %w", err)
		}
	}

	fmt.Fprintf(c.run.stdout, "   %s%sRenaming user and moving home directory (%s -> %s)...%s\n",
		c.cBold, c.iconUpdate, defaultUsername, selectedUsername, c.cReset)

	usermodArgs := []string{
		"-l", selectedUsername,
		"-d", "/home/" + selectedUsername,
		"-m", defaultUsername,
	}
	if selectedFullname != "" {
		usermodArgs = append(usermodArgs, "-c", selectedFullname)
	}
	usermodCmd := c.run.execCommand("usermod", usermodArgs...)
	usermodCmd.SetStdout(c.run.stdout)
	usermodCmd.SetStderr(c.run.stderr)
	if err := usermodCmd.Run(); err != nil {
		return "", fmt.Errorf("failed to rename user: %w", err)
	}

	// Clean up stock user config files.
	liveHomeTmpfile := "/etc/tmpfiles.d/matrixos-live-home.conf"
	if c.run.fileExists(liveHomeTmpfile) {
		fmt.Fprintf(c.run.stdout, "   %sRemoving stock user tmpfiles config...%s\n",
			c.iconDoc, c.cReset)
		c.run.removeFile(liveHomeTmpfile)
	}

	liveAccountTmpfile := fmt.Sprintf("/etc/sysusers.d/acct-user-%s.conf", defaultUsername)
	if c.run.fileExists(liveAccountTmpfile) {
		fmt.Fprintf(c.run.stdout, "   %sRemoving stock account sysusers config...%s\n",
			c.iconDoc, c.cReset)
		c.run.removeFile(liveAccountTmpfile)
	}

	fmt.Fprintf(c.run.stdout, "   %s%sUser renamed successfully.%s\n",
		c.cGreen, c.iconCheck, c.cReset)
	return selectedUsername, nil
}

// changeUserPassword runs passwd for the given user.
func (c *SetupOSCommand) changeUserPassword(targetUser string) error {
	if err := c.checkUsername(targetUser); err != nil {
		return err
	}

	fmt.Fprintf(c.run.stdout, "   %s%sChanging password for user: %s%s\n",
		c.cBold, c.iconGear, targetUser, c.cReset)
	c.FlushPrinters()

	passwdCmd := c.run.execCommand("passwd", targetUser)
	passwdCmd.SetStdin(c.run.stdin)
	passwdCmd.SetStdout(c.run.getInteractiveStdout())
	passwdCmd.SetStderr(c.run.getInteractiveStderr())
	if err := passwdCmd.Run(); err != nil {
		return fmt.Errorf("failed to change password for %s: %w", targetUser, err)
	}

	fmt.Fprintf(c.run.stdout, "   %s%sPassword changed for %s.%s\n",
		c.cGreen, c.iconCheck, targetUser, c.cReset)
	return nil
}

// changeLuksPassword detects LUKS encryption and prompts for password change.
func (c *SetupOSCommand) changeLuksPassword() error {
	fmt.Fprintf(c.run.stdout, "   %sChecking for disk encryption...%s\n",
		c.iconSearch, c.cReset)

	cmdlineData, err := c.run.readFile("/proc/cmdline")
	if err != nil {
		fmt.Fprintf(c.run.stdout, "   %s%sUnable to read /proc/cmdline, skipping LUKS check.%s\n",
			c.cYellow, c.iconWarn, c.cReset)
		return nil
	}

	uuid := ""
	for _, field := range strings.Fields(string(cmdlineData)) {
		if v, found := strings.CutPrefix(field, "rd.luks.uuid="); found {
			uuid = v
			break
		}
	}

	if uuid == "" {
		fmt.Fprintf(c.run.stdout, "   %s%sNo disk encryption detected, skipping.%s\n",
			c.cBlue, c.iconCheck, c.cReset)
		return nil
	}

	luksDevice := "/dev/disk/by-uuid/" + uuid
	if !c.run.fileExists(luksDevice) {
		fmt.Fprintf(c.run.stderr, "   %s%sCannot find LUKS device for UUID=%s, skipping.%s\n",
			c.cYellow, c.iconWarn, uuid, c.cReset)
		return nil
	}

	fmt.Fprintf(c.run.stdout, "   %s%sLUKS encryption detected (UUID: %s)%s\n",
		c.cBlue, c.iconGear, uuid, c.cReset)
	fmt.Fprintf(c.run.stdout, "   %sPlease pick a new password for the encrypted disk.%s\n",
		c.iconQuestion, c.cReset)

	var lastErr error
	for attempt := 1; attempt <= maxLuksPasswordAttempts; attempt++ {
		c.FlushPrinters()
		cryptsetupCmd := c.run.execCommand("cryptsetup", "luksChangeKey", luksDevice)
		cryptsetupCmd.SetStdin(c.run.stdin)
		cryptsetupCmd.SetStdout(c.run.getInteractiveStdout())
		cryptsetupCmd.SetStderr(c.run.getInteractiveStderr())
		lastErr = cryptsetupCmd.Run()
		if lastErr == nil {
			break
		}
		if attempt < maxLuksPasswordAttempts {
			fmt.Fprintf(c.run.stderr, "   %s%sFailed to change LUKS password (%d/%d). Please try again.%s\n",
				c.cRed, c.iconError, attempt, maxLuksPasswordAttempts, c.cReset)
		}
	}
	if lastErr != nil {
		return fmt.Errorf("failed to change LUKS password after %d attempts: %w", maxLuksPasswordAttempts, lastErr)
	}

	fmt.Fprintf(c.run.stdout, "   %s%sLUKS password changed successfully.%s\n",
		c.cGreen, c.iconCheck, c.cReset)
	return nil
}

// setupLocalization runs systemd-firstboot and configures locale settings.
func (c *SetupOSCommand) setupLocalization() error {
	fmt.Fprintf(c.run.stdout, "   %s%sConfiguring system locale and timezone...%s\n",
		c.cBold, c.iconGear, c.cReset)

	firstbootArgs := []string{"--reset"}
	if c.localeFlag != "" {
		firstbootArgs = append(firstbootArgs, "--locale="+c.localeFlag)
	}
	if c.keymapFlag != "" {
		firstbootArgs = append(firstbootArgs, "--keymap="+c.keymapFlag)
	}
	if c.timezoneFlag != "" {
		firstbootArgs = append(firstbootArgs, "--timezone="+c.timezoneFlag)
	}
	if c.hostnameFlag != "" {
		firstbootArgs = append(firstbootArgs, "--hostname="+c.hostnameFlag)
	}
	if c.localeFlag == "" || c.keymapFlag == "" || c.timezoneFlag == "" || c.hostnameFlag == "" {
		firstbootArgs = append(firstbootArgs, "--prompt")
	}
	c.FlushPrinters()
	firstbootCmd := c.run.execCommand("systemd-firstboot", firstbootArgs...)
	firstbootCmd.SetStdin(c.run.stdin)
	firstbootCmd.SetStdout(c.run.getInteractiveStdout())
	firstbootCmd.SetStderr(c.run.getInteractiveStderr())
	if err := firstbootCmd.Run(); err != nil {
		return fmt.Errorf("systemd-firstboot failed: %w", err)
	}

	// env-update for Gentoo locale fix.
	fmt.Fprintf(c.run.stdout, "   %s%sRunning env-update for locale configuration...%s\n",
		c.cGreen, c.iconGear, c.cReset)
	envUpdateCmd := c.run.execCommand("env-update")
	envUpdateCmd.Run() // best effort

	fmt.Fprintf(c.run.stdout, "   %s%sLocalization configured.%s\n",
		c.cGreen, c.iconCheck, c.cReset)
	return nil
}

// setupAccountsService configures AccountsService for the given user.
func (c *SetupOSCommand) setupAccountsService(username string) error {
	// Setup AccountsService directories.
	asDir := "/var/lib/AccountsService"
	asUsersDir := filepath.Join(asDir, "users")
	asIconsDir := filepath.Join(asDir, "icons")
	c.run.mkdirAll(asIconsDir, 0755)

	// Remove root AccountsService entry (in case we logged via gdm).
	c.run.removeFile(filepath.Join(asUsersDir, "root"))

	// Read locale.
	lang := ""
	if localeData, err := c.run.readFile("/etc/locale.conf"); err == nil {
		for _, line := range strings.Split(string(localeData), "\n") {
			if v, found := strings.CutPrefix(line, "LANG="); found {
				lang = strings.TrimSpace(v)
				break
			}
		}
	}

	if lang == "" || username == "" {
		fmt.Fprintf(c.run.stdout, "   %s%sSkipping AccountsService (no locale or username).%s\n",
			c.cYellow, c.iconWarn, c.cReset)
		return nil
	}

	fmt.Fprintf(
		c.run.stdout,
		"   %s%sConfiguring AccountsService for user %s with LANG=%s...%s\n",
		c.cBold, c.iconGear, username, lang, c.cReset,
	)
	if _, err := c.run.stat(asUsersDir); err == nil {
		userCfg := filepath.Join(asUsersDir, username)

		// Determine icon path.
		srcIconPath := "/usr/share/pixmaps/faces/tree.jpg"
		dstIconPath := "/home/" + username + "/.face"
		if c.run.fileExists(srcIconPath) {
			dstIconPath = filepath.Join(asIconsDir, username)
			c.run.copyFile(srcIconPath, dstIconPath)
			c.run.chmod(dstIconPath, 0644)
		}

		content := fmt.Sprintf("[User]\nLanguages=%s;\nSession=\nIcon=%s\nSystemAccount=false\n",
			lang, dstIconPath)
		c.run.writeFile(userCfg, []byte(content), 0600)
		c.run.chown(userCfg, 0, 0)

		fmt.Fprintf(c.run.stdout, "   %s%sAccountsService configured for %s (lang=%s).%s\n",
			c.cGreen, c.iconCheck, username, lang, c.cReset)
	}

	return nil
}

// setupLocalizationKeymap configures the vconsole keymap in kernel boot args.
func (c *SetupOSCommand) setupLocalizationKeymap(jailbrokenEntry string) error {
	keymap := ""
	if vconsoleData, err := c.run.readFile("/etc/vconsole.conf"); err == nil {
		for line := range strings.SplitSeq(string(vconsoleData), "\n") {
			if v, found := strings.CutPrefix(line, "KEYMAP="); found {
				keymap = strings.TrimSpace(v)
				break
			}
		}
	}
	if keymap == "" {
		return nil
	}

	// Check if we're on ostree.
	cmdlineData, err := c.run.readFile("/proc/cmdline")
	if err != nil {
		return nil
	}
	cmdline := string(cmdlineData)
	isOstree := strings.Contains(cmdline, "ostree=")
	jailbrokenConfig := filepath.Join("/boot/loader/entries", jailbrokenEntry)
	karg := "vconsole.keymap=" + keymap

	if isOstree {
		fmt.Fprintf(
			c.run.stdout,
			"   %sConfiguring early boot keyboard mapping via BLS entries...%s\n",
			c.iconGear, c.cReset,
		)
		entries, err := listBLSEntryPaths(c.run.glob)
		if err != nil {
			return fmt.Errorf("BLS keymap setup failed: %w", err)
		}
		for _, entry := range entries {
			_, err := addKargsToBLSFile(
				entry, []string{karg},
				c.run.readFile, c.run.writeFile,
			)
			if err != nil {
				return fmt.Errorf(
					"failed to add keymap to %s: %w",
					filepath.Base(entry), err,
				)
			}
		}
	} else if c.run.fileExists(jailbrokenConfig) {
		fmt.Fprintf(
			c.run.stdout,
			"   %sConfiguring early boot keyboard mapping via BLS config...%s\n",
			c.iconGear, c.cReset,
		)
		_, err := addKargsToBLSFile(
			jailbrokenConfig, []string{karg},
			c.run.readFile, c.run.writeFile,
		)
		if err != nil {
			return err
		}
	} else {
		fmt.Fprintf(
			c.run.stdout,
			"   %s%sJailbroken matrixOS without original boot config."+
				" Skipping keymap setup.%s\n",
			c.cYellow, c.iconWarn, c.cReset,
		)
	}

	return nil
}

// detectWindows scans EFI partitions for Windows bootloader and adds grub entries.
func (c *SetupOSCommand) detectWindows(efiRoot, relativeEfiBoot string) error {
	fmt.Fprintf(c.run.stdout, "   %sScanning for Windows installations...%s\n",
		c.iconSearch, c.cReset)

	espPartType, err := c.cfg.GetItem("Imager.EspPartitionType")
	if err != nil || espPartType == "" {
		fmt.Fprintf(c.run.stderr, "   %s%sESP partition type not configured, skipping Windows detection.%s\n",
			c.cYellow, c.iconWarn, c.cReset)
		return nil
	}

	lines, err := c.run.listBlockDevices("PATH,PARTTYPE")
	if err != nil {
		fmt.Fprintf(c.run.stderr, "   %s%sUnable to list block devices.%s\n",
			c.cYellow, c.iconWarn, c.cReset)
		return nil
	}

	var efiPartitions []string
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if strings.EqualFold(fields[1], espPartType) {
			efiPartitions = append(efiPartitions, fields[0])
		}
	}

	if len(efiPartitions) == 0 {
		fmt.Fprintf(c.run.stderr, "   %s%sNo EFI System partitions detected.%s\n",
			c.cYellow, c.iconWarn, c.cReset)
		return nil
	}

	grubCfg := filepath.Join(efiRoot, relativeEfiBoot, "grub.cfg")
	if !c.run.fileExists(grubCfg) {
		fmt.Fprintf(c.run.stderr, "   %s%sNo %s found. Skipping Windows detection.%s\n",
			c.cYellow, c.iconWarn, grubCfg, c.cReset)
		return nil
	}

	msblPath := "/EFI/Microsoft/Boot/bootmgfw.efi"

	for _, part := range efiPartitions {
		if err := c.checkPartitionForWindows(part, grubCfg, msblPath); err != nil {
			fmt.Fprintf(c.run.stderr, "   %s%s%v%s\n",
				c.cYellow, c.iconWarn, err, c.cReset)
		}
	}

	return nil
}

// checkPartitionForWindows checks a single EFI partition for a Windows bootloader.
func (c *SetupOSCommand) checkPartitionForWindows(part, grubCfg, msblPath string) error {
	// Check if already mounted.
	checkMountCmd := c.run.execCommand("lsblk", "-no", "MOUNTPOINT", part)
	mountOut, _ := checkMountCmd.Output()
	existingMount := ""
	for _, line := range strings.Split(string(mountOut), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			existingMount = line
			break
		}
	}

	var mountPoint string
	if existingMount != "" {
		mountPoint = existingMount
	} else {
		// Create unique temp mount point and mount.
		mkdirTemp := c.run.mkdirTemp
		if mkdirTemp == nil {
			mkdirTemp = os.MkdirTemp
		}
		tmpDir, err := mkdirTemp("", fmt.Sprintf("setupos-win-%s-*", filepath.Base(part)))
		if err != nil {
			return fmt.Errorf("unable to create temp mount dir for %s: %w", part, err)
		}
		mountCmd := c.run.execCommand("mount", "-o", "ro", part, tmpDir)
		if err := mountCmd.Run(); err != nil {
			_ = c.run.removeFile(tmpDir)
			return fmt.Errorf("unable to mount %s: %w", part, err)
		}
		mountPoint = tmpDir
		defer func() {
			umountCmd := c.run.execCommand("umount", mountPoint)
			if err := umountCmd.Run(); err != nil {
				fmt.Fprintf(c.run.stderr, "   %s%sWarning: failed to unmount %s: %v%s\n",
					c.cYellow, c.iconWarn, mountPoint, err, c.cReset)
				return
			}
			_ = c.run.removeFile(mountPoint)
		}()
	}

	if !c.run.fileExists(filepath.Join(mountPoint, msblPath)) {
		fmt.Fprintf(c.run.stdout, "   Partition %s does not contain a Windows bootloader.\n", part)
		return nil
	}

	fmt.Fprintf(c.run.stdout, "   %s%sFound Windows on %s%s\n",
		c.cGreen, c.iconNew, part, c.cReset)

	uuid, err := c.run.getBlkidValue(part, "UUID")
	if err != nil {
		return fmt.Errorf("failed to get UUID for %s: %w", part, err)
	}

	entryName := fmt.Sprintf("Windows on %s (%s)", part, uuid)
	grubEntry := fmt.Sprintf(`
menuentry "%s" --class windows {
  insmod fat
  insmod chain
  search --no-floppy --fs-uuid --set=root %s
  chainloader %s
}
`, entryName, uuid, msblPath)

	fmt.Fprintf(c.run.stdout, "   %sAppending '%s' to grub.cfg...%s\n",
		c.iconDoc, entryName, c.cReset)

	if err := c.run.appendFile(grubCfg, []byte(grubEntry)); err != nil {
		return fmt.Errorf("failed to append Windows entry to grub.cfg: %w", err)
	}

	// If we mounted it, unmount happens via defer.
	return nil
}

// addOSBoot adds a OS UEFI boot entry via efibootmgr.
func (c *SetupOSCommand) addOSBoot(efiRoot, fancyOsName, efiExecPath string) error {
	efiDevice, err := c.run.getMountDevice(efiRoot)
	if err != nil {
		return fmt.Errorf("unable to find device for %s: %w", efiRoot, err)
	}

	partNo, err := c.run.getPartitionNumber(efiDevice)
	if err != nil {
		return fmt.Errorf("unable to get partition number for %s: %w", efiDevice, err)
	}
	partNo = strings.TrimSpace(partNo)

	label, err := c.run.getPartitionLabel(efiDevice)
	if err != nil || label == "" {
		return fmt.Errorf("unable to get partition label for %s", efiDevice)
	}

	blockDevice, err := c.run.getBlockDevice(efiDevice)
	if err != nil {
		return fmt.Errorf("unable to find block device for %s: %w", efiDevice, err)
	}

	bootLabel := fmt.Sprintf("%s on %s", fancyOsName, label)
	fmt.Fprintf(c.run.stdout, "   %s%sInstalling (%s) to ESP via efibootmgr...%s\n",
		c.cBold, c.iconRocket, bootLabel, c.cReset)
	fmt.Fprintf(c.run.stdout, "   %sNote: if this fails, you should already have a no-name entry.%s\n",
		c.iconWarn, c.cReset)

	efiCmd := c.run.execCommand("efibootmgr",
		"--create",
		"--disk", blockDevice,
		"--part", partNo,
		"--label", bootLabel,
		"--loader", efiExecPath,
	)
	efiCmd.SetStdout(c.run.stdout)
	efiCmd.SetStderr(c.run.stderr)
	if err := efiCmd.Run(); err != nil {
		return fmt.Errorf("efibootmgr failed: %w", err)
	}

	fmt.Fprintf(c.run.stdout, "   %s%sEFI boot entry created.%s\n",
		c.cGreen, c.iconCheck, c.cReset)
	return nil
}
