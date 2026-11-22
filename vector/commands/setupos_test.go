package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"matrixos/vector/lib/config"
)

// testSetupOSRunner creates a setupOSRunner with sensible defaults for testing.
func testSetupOSRunner() *setupOSRunner {
	return &setupOSRunner{
		execCommand: func(name string, args ...string) cmdRunner {
			return &mockCmdRunner{}
		},
		readFile: func(path string) ([]byte, error) {
			return nil, fmt.Errorf("readFile not mocked for %s", path)
		},
		writeFile: func(path string, data []byte, perm os.FileMode) error {
			return nil
		},
		appendFile: func(path string, data []byte) error {
			return nil
		},
		mkdirAll: func(path string, perm os.FileMode) error {
			return nil
		},
		mkdirTemp: func(dir, pattern string) (string, error) {
			return "/tmp/mock-setupos-win-test", nil
		},
		stat: func(path string) (os.FileInfo, error) {
			return nil, fmt.Errorf("stat not mocked for %s", path)
		},
		removeFile: func(path string) error { return nil },
		fileExists: func(path string) bool { return false },
		copyFile:   func(src, dst string) error { return nil },
		chmod:      func(path string, mode os.FileMode) error { return nil },
		chown:      func(path string, uid, gid int) error { return nil },
		getMountDevice: func(mnt string) (string, error) {
			return "/dev/sda1", nil
		},
		getPartitionNumber: func(device string) (string, error) {
			return "1", nil
		},
		getPartitionLabel: func(device string) (string, error) {
			return "EFI", nil
		},
		getBlockDevice: func(device string) (string, error) {
			return "/dev/sda", nil
		},
		listBlockDevices: func(fields string) ([]string, error) {
			return nil, nil
		},
		getBlkidValue: func(device, tag string) (string, error) {
			return "test-uuid", nil
		},
		getEuid:        func() int { return 0 },
		getCurrentUser: func() string { return "root" },
		stdin:          strings.NewReader(""),
		stdout:         io.Discard,
		stderr:         io.Discard,
	}
}

func setupOSTestConfig() *config.MockConfig {
	return &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.DefaultUsername":             {"matrix"},
			"matrixOS.FancyOsName":                 {"matrixOS"},
			"Imager.EfiRoot":                       {"/efi"},
			"Imager.RelativeEfiBootPath":           {"efi/BOOT"},
			"Imager.EfiStandardBootExecutablePath": {`\EFI\BOOT\BOOTX64.EFI`},
			"Jailbreak.BootLoaderEntry":            {"matrixos-jailbroken.conf"},
			"Imager.EspPartitionType":              {"C12A7328-F81F-11D2-BA4B-00A0C93EC93B"},
		},
	}
}

func newTestSetupOSCommand(
	cfg *config.MockConfig,
	runner *setupOSRunner,
) (*SetupOSCommand, error) {
	cmd := &SetupOSCommand{}
	cmd.cfg = cfg
	cmd.StartUI()
	cmd.run = runner
	cmd.prompt = NewPrompter(runner.stdin, runner.stdout, runner.stderr, &cmd.UI)
	if err := cmd.parseArgs(nil); err != nil {
		return nil, err
	}
	return cmd, nil
}

// initSetupOSCmd creates a SetupOSCommand with the given runner,
// initializes UI and scanner.  For tests that exercise individual methods.
func initSetupOSCmd(runner *setupOSRunner) *SetupOSCommand {
	cmd := &SetupOSCommand{}
	cmd.cfg = setupOSTestConfig()
	cmd.StartUI()
	cmd.run = runner
	cmd.prompt = NewPrompter(runner.stdin, runner.stdout, runner.stderr, &cmd.UI)
	return cmd
}

func TestSetupOSName(t *testing.T) {
	cmd := &SetupOSCommand{}
	cmd.parseArgs(nil)
	if cmd.Name() != "setupOS" {
		t.Fatalf("expected name 'setupOS', got %q", cmd.Name())
	}
}

func TestSetupOSRequiresRoot(t *testing.T) {
	runner := testSetupOSRunner()
	runner.getEuid = func() int { return 1000 }

	cmd, err := newTestSetupOSCommand(setupOSTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestSetupOSCommand failed: %v", err)
	}

	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "must be run as root") {
		t.Fatalf("expected root error, got: %v", err)
	}
}

func TestSetupOSRequiresRootLogin(t *testing.T) {
	runner := testSetupOSRunner()
	runner.getEuid = func() int { return 0 }
	runner.getCurrentUser = func() string { return "matrix" }

	cmd, err := newTestSetupOSCommand(setupOSTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestSetupOSCommand failed: %v", err)
	}

	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "must be logged in as root") {
		t.Fatalf("expected root login error, got: %v", err)
	}
}

func TestSetupOSLuksNoEncryption(t *testing.T) {
	runner := testSetupOSRunner()
	runner.readFile = func(path string) ([]byte, error) {
		if path == "/proc/cmdline" {
			return []byte("root=UUID=abc123 rw quiet"), nil
		}
		return nil, fmt.Errorf("not mocked: %s", path)
	}

	cmd := initSetupOSCmd(runner)

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout

	err := cmd.changeLuksPassword()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "No disk encryption detected") {
		t.Errorf("expected no-encryption message, got: %s", stdout.String())
	}
}

func TestSetupOSLuksDetected(t *testing.T) {
	runner := testSetupOSRunner()
	runner.readFile = func(path string) ([]byte, error) {
		if path == "/proc/cmdline" {
			return []byte("root=UUID=abc123 rd.luks.uuid=my-luks-uuid rw"), nil
		}
		return nil, fmt.Errorf("not mocked: %s", path)
	}
	runner.fileExists = func(path string) bool {
		return path == "/dev/disk/by-uuid/my-luks-uuid"
	}
	// cryptsetup succeeds on first try.
	runner.execCommand = func(name string, args ...string) cmdRunner {
		return &mockCmdRunner{}
	}

	cmd := initSetupOSCmd(runner)

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout

	err := cmd.changeLuksPassword()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "LUKS encryption detected") {
		t.Errorf("expected LUKS detected message, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "LUKS password changed") {
		t.Errorf("expected LUKS changed message, got: %s", stdout.String())
	}
}

func TestSetupOSChangeUsernameSkip(t *testing.T) {
	runner := testSetupOSRunner()
	// `id -u matrix` succeeds.
	runner.execCommand = func(name string, args ...string) cmdRunner {
		if name == "id" && len(args) >= 2 && args[0] == "-u" {
			return &mockCmdRunner{outputVal: []byte("1000")}
		}
		return &mockCmdRunner{}
	}
	// User enters empty line (accept default).
	runner.stdin = strings.NewReader("\n")

	cmd := initSetupOSCmd(runner)

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout
	cmd.prompt.Stdout = &stdout

	result, err := cmd.changeUsername("matrix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "matrix" {
		t.Errorf("expected username='matrix', got %q", result)
	}
	if !strings.Contains(stdout.String(), "Skipping username change") {
		t.Errorf("expected skip message, got: %s", stdout.String())
	}
}

func TestSetupOSChangeUsernameRename(t *testing.T) {
	var executedCmds []string
	runner := testSetupOSRunner()
	runner.execCommand = func(name string, args ...string) cmdRunner {
		cmdStr := name + " " + strings.Join(args, " ")
		executedCmds = append(executedCmds, cmdStr)
		if name == "id" {
			return &mockCmdRunner{outputVal: []byte("1000")}
		}
		if name == "getent" {
			return &mockCmdRunner{outputVal: []byte("matrix:x:1000:")}
		}
		return &mockCmdRunner{}
	}
	runner.fileExists = func(path string) bool {
		return path == "/etc/tmpfiles.d/matrixos-live-home.conf"
	}
	// Provide username and full name.
	runner.stdin = strings.NewReader("alice\nAlice Wonderland\n")

	cmd := initSetupOSCmd(runner)

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout
	cmd.prompt.Stdout = &stdout

	result, err := cmd.changeUsername("matrix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "alice" {
		t.Errorf("expected username='alice', got %q", result)
	}

	// Check groupmod was called.
	foundGroupmod := false
	foundUsermod := false
	for _, c := range executedCmds {
		if strings.HasPrefix(c, "groupmod") {
			foundGroupmod = true
			if !strings.Contains(c, "alice") {
				t.Errorf("groupmod should rename to alice: %s", c)
			}
		}
		if strings.HasPrefix(c, "usermod") {
			foundUsermod = true
			if !strings.Contains(c, "alice") {
				t.Errorf("usermod should use alice: %s", c)
			}
			if !strings.Contains(c, "Alice Wonderland") {
				t.Errorf("usermod should set full name: %s", c)
			}
		}
	}
	if !foundGroupmod {
		t.Error("expected groupmod to be called")
	}
	if !foundUsermod {
		t.Error("expected usermod to be called")
	}
}

func TestSetupOSChangeUsernameNonExistent(t *testing.T) {
	runner := testSetupOSRunner()
	runner.execCommand = func(name string, args ...string) cmdRunner {
		if name == "id" {
			return &mockCmdRunner{outputErr: fmt.Errorf("no such user")}
		}
		return &mockCmdRunner{}
	}

	cmd := initSetupOSCmd(runner)

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout

	result, err := cmd.changeUsername("matrix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "matrix" {
		t.Errorf("expected fallback to default username, got %q", result)
	}
}

func TestSetupOSChangeUserPassword(t *testing.T) {
	var passwdCalled bool
	runner := testSetupOSRunner()
	runner.execCommand = func(name string, args ...string) cmdRunner {
		if name == "id" {
			return &mockCmdRunner{outputVal: []byte("0")}
		}
		if name == "passwd" {
			passwdCalled = true
			return &mockCmdRunner{}
		}
		return &mockCmdRunner{}
	}

	cmd := initSetupOSCmd(runner)

	err := cmd.changeUserPassword("root")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !passwdCalled {
		t.Error("expected passwd to be called")
	}
}

func TestSetupOSSetupLocalization(t *testing.T) {
	runner := testSetupOSRunner()
	runner.execCommand = func(name string, args ...string) cmdRunner {
		return &mockCmdRunner{}
	}

	cmd := initSetupOSCmd(runner)

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout

	err := cmd.setupLocalization()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(stdout.String(), "Localization configured") {
		t.Errorf("expected localization message, got: %s", stdout.String())
	}
}

func TestSetupOSParseLocalizationFlags(t *testing.T) {
	cmd := &SetupOSCommand{}
	err := cmd.parseArgs([]string{
		"--locale", "en_US.UTF-8",
		"--keymap", "us",
		"--timezone", "Europe/Rome",
		"--hostname", "myhost",
	})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cmd.localeFlag != "en_US.UTF-8" {
		t.Errorf("expected locale='en_US.UTF-8', got %q", cmd.localeFlag)
	}
	if cmd.keymapFlag != "us" {
		t.Errorf("expected keymap='us', got %q", cmd.keymapFlag)
	}
	if cmd.timezoneFlag != "Europe/Rome" {
		t.Errorf("expected timezone='Europe/Rome', got %q", cmd.timezoneFlag)
	}
	if cmd.hostnameFlag != "myhost" {
		t.Errorf("expected hostname='myhost', got %q", cmd.hostnameFlag)
	}
}

func TestSetupOSLocalizationAllFlagsNoPrompt(t *testing.T) {
	var capturedArgs []string
	runner := testSetupOSRunner()
	runner.execCommand = func(name string, args ...string) cmdRunner {
		if name == "systemd-firstboot" {
			capturedArgs = args
		}
		return &mockCmdRunner{}
	}

	cmd := initSetupOSCmd(runner)
	cmd.localeFlag = "en_US.UTF-8"
	cmd.keymapFlag = "us"
	cmd.timezoneFlag = "Europe/Rome"
	cmd.hostnameFlag = "myhost"

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout

	err := cmd.setupLocalization()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	joined := strings.Join(capturedArgs, " ")
	if strings.Contains(joined, "--prompt") {
		t.Errorf("expected no --prompt when all flags set, got args: %v", capturedArgs)
	}
	for _, want := range []string{
		"--locale=en_US.UTF-8",
		"--keymap=us",
		"--timezone=Europe/Rome",
		"--hostname=myhost",
		"--reset",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in systemd-firstboot args, got: %v", want, capturedArgs)
		}
	}
}

func TestSetupOSLocalizationPartialFlagsPrompt(t *testing.T) {
	var capturedArgs []string
	runner := testSetupOSRunner()
	runner.execCommand = func(name string, args ...string) cmdRunner {
		if name == "systemd-firstboot" {
			capturedArgs = args
		}
		return &mockCmdRunner{}
	}

	cmd := initSetupOSCmd(runner)
	cmd.localeFlag = "en_US.UTF-8"
	cmd.timezoneFlag = "Europe/Rome"
	// keymapFlag and hostnameFlag left empty.

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout

	err := cmd.setupLocalization()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, "--prompt") {
		t.Errorf("expected --prompt when not all flags set, got args: %v", capturedArgs)
	}
	if !strings.Contains(joined, "--locale=en_US.UTF-8") {
		t.Errorf("expected --locale in args, got: %v", capturedArgs)
	}
	if !strings.Contains(joined, "--timezone=Europe/Rome") {
		t.Errorf("expected --timezone in args, got: %v", capturedArgs)
	}
	if strings.Contains(joined, "--keymap=") {
		t.Errorf("unexpected --keymap in args, got: %v", capturedArgs)
	}
	if strings.Contains(joined, "--hostname=") {
		t.Errorf("unexpected --hostname in args, got: %v", capturedArgs)
	}
}

func TestSetupOSLocalizationNoFlagsPrompt(t *testing.T) {
	var capturedArgs []string
	runner := testSetupOSRunner()
	runner.execCommand = func(name string, args ...string) cmdRunner {
		if name == "systemd-firstboot" {
			capturedArgs = args
		}
		return &mockCmdRunner{}
	}

	cmd := initSetupOSCmd(runner)
	// All flags left empty.

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout

	err := cmd.setupLocalization()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, "--prompt") {
		t.Errorf("expected --prompt when no flags set, got args: %v", capturedArgs)
	}
	if !strings.Contains(joined, "--reset") {
		t.Errorf("expected --reset in args, got: %v", capturedArgs)
	}
}

func TestSetupOSSetupAccountsService(t *testing.T) {
	writtenFiles := make(map[string][]byte)
	runner := testSetupOSRunner()
	runner.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/etc/locale.conf":
			return []byte("LANG=en_US.UTF-8\n"), nil
		}
		return nil, fmt.Errorf("not mocked: %s", path)
	}
	runner.stat = func(path string) (os.FileInfo, error) {
		if path == "/var/lib/AccountsService/users" {
			return fakeFileInfo{}, nil
		}
		return nil, fmt.Errorf("not found")
	}
	runner.writeFile = func(path string, data []byte, perm os.FileMode) error {
		writtenFiles[path] = data
		return nil
	}
	runner.fileExists = func(path string) bool {
		return false
	}

	cmd := initSetupOSCmd(runner)

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout

	err := cmd.setupAccountsService("alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check AccountsService config was written.
	asCfg, ok := writtenFiles["/var/lib/AccountsService/users/alice"]
	if !ok {
		t.Error("expected AccountsService config to be written for alice")
	} else {
		if !strings.Contains(string(asCfg), "Languages=en_US.UTF-8") {
			t.Errorf("AccountsService config missing language, got: %s", string(asCfg))
		}
	}

	if !strings.Contains(stdout.String(), "AccountsService configured") {
		t.Errorf("expected AccountsService message, got: %s", stdout.String())
	}
}

func TestSetupOSKeymapOstree(t *testing.T) {
	writtenFiles := make(map[string][]byte)
	runner := testSetupOSRunner()
	runner.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/etc/vconsole.conf":
			return []byte("KEYMAP=de-latin1\n"), nil
		case "/proc/cmdline":
			return []byte("root=UUID=abc ostree=/ostree/boot rw"), nil
		case "/boot/loader/entries/matrixos-boot.conf":
			return []byte(
				"title matrixOS\noptions root=UUID=abc rw\n",
			), nil
		}
		return nil, fmt.Errorf("not mocked: %s", path)
	}
	runner.writeFile = func(
		path string, data []byte, perm os.FileMode,
	) error {
		writtenFiles[path] = data
		return nil
	}
	runner.glob = func(pattern string) ([]string, error) {
		return []string{
			"/boot/loader/entries/matrixos-boot.conf",
		}, nil
	}

	cmd := initSetupOSCmd(runner)

	err := cmd.setupLocalizationKeymap("matrixos-jailbroken.conf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	blsPath := "/boot/loader/entries/matrixos-boot.conf"
	written, ok := writtenFiles[blsPath]
	if !ok {
		t.Fatal("expected BLS config to be written")
	}
	if !strings.Contains(string(written), "vconsole.keymap=de-latin1") {
		t.Errorf(
			"expected keymap in BLS config, got: %s",
			string(written),
		)
	}
}

func TestSetupOSKeymapJailbroken(t *testing.T) {
	writtenFiles := make(map[string][]byte)
	runner := testSetupOSRunner()
	runner.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/etc/vconsole.conf":
			return []byte("KEYMAP=it\n"), nil
		case "/proc/cmdline":
			return []byte("root=UUID=abc rw quiet"), nil
		case "/boot/loader/entries/matrixos-jailbroken.conf":
			return []byte("title matrixOS\noptions root=UUID=abc rw\n"), nil
		}
		return nil, fmt.Errorf("not mocked: %s", path)
	}
	runner.fileExists = func(path string) bool {
		return path == "/boot/loader/entries/matrixos-jailbroken.conf"
	}
	runner.writeFile = func(path string, data []byte, perm os.FileMode) error {
		writtenFiles[path] = data
		return nil
	}
	runner.execCommand = func(name string, args ...string) cmdRunner {
		return &mockCmdRunner{}
	}

	cmd := initSetupOSCmd(runner)

	err := cmd.setupLocalizationKeymap("matrixos-jailbroken.conf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	written, ok := writtenFiles["/boot/loader/entries/matrixos-jailbroken.conf"]
	if !ok {
		t.Fatal("expected jailbroken BLS config to be written")
	}
	if !strings.Contains(string(written), "vconsole.keymap=it") {
		t.Errorf("expected keymap in BLS config, got: %s", string(written))
	}
}

func TestSetupOSDetectWindowsNoEFI(t *testing.T) {
	runner := testSetupOSRunner()
	runner.listBlockDevices = func(fields string) ([]string, error) {
		return []string{"/dev/sda1 0fc63daf-8483-4772-8e79-3d69d8477de4"}, nil
	}

	cmd := initSetupOSCmd(runner)

	var stderr bytes.Buffer
	cmd.run.stderr = &stderr

	err := cmd.detectWindows("/efi", "efi/BOOT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr.String(), "No EFI System partitions") {
		t.Errorf("expected no-EFI message, got: %s", stderr.String())
	}
}

func TestSetupOSDetectWindowsFound(t *testing.T) {
	var appendedData []byte
	runner := testSetupOSRunner()
	runner.listBlockDevices = func(fields string) ([]string, error) {
		return []string{
			"/dev/sda1 c12a7328-f81f-11d2-ba4b-00a0c93ec93b",
		}, nil
	}
	runner.fileExists = func(path string) bool {
		if path == "/efi/efi/BOOT/grub.cfg" {
			return true
		}
		// The Windows bootloader exists on the mounted partition.
		if strings.HasSuffix(path, "/EFI/Microsoft/Boot/bootmgfw.efi") {
			return true
		}
		return false
	}
	runner.execCommand = func(name string, args ...string) cmdRunner {
		if name == "lsblk" && len(args) >= 3 && args[1] == "MOUNTPOINT" {
			// Not mounted.
			return &mockCmdRunner{outputVal: []byte("")}
		}
		return &mockCmdRunner{}
	}
	runner.appendFile = func(path string, data []byte) error {
		appendedData = data
		return nil
	}
	runner.getBlkidValue = func(device, tag string) (string, error) {
		return "ABCD-1234", nil
	}

	cmd := initSetupOSCmd(runner)

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout

	err := cmd.detectWindows("/efi", "efi/BOOT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if appendedData == nil {
		t.Fatal("expected grub entry to be appended")
	}
	if !strings.Contains(string(appendedData), "ABCD-1234") {
		t.Errorf("grub entry should contain UUID, got: %s", string(appendedData))
	}
	if !strings.Contains(string(appendedData), "bootmgfw.efi") {
		t.Errorf("grub entry should contain bootmgfw.efi, got: %s", string(appendedData))
	}
}

func TestSetupOSAddOSBoot(t *testing.T) {
	var efibootmgrCalled bool
	var efiArgs string
	runner := testSetupOSRunner()
	runner.getMountDevice = func(mnt string) (string, error) { return "/dev/sda1", nil }
	runner.getPartitionNumber = func(device string) (string, error) { return "1", nil }
	runner.getPartitionLabel = func(device string) (string, error) { return "ESP", nil }
	runner.getBlockDevice = func(device string) (string, error) { return "/dev/sda", nil }
	runner.execCommand = func(name string, args ...string) cmdRunner {
		if name == "efibootmgr" {
			efibootmgrCalled = true
			efiArgs = strings.Join(args, " ")
		}
		return &mockCmdRunner{}
	}

	cmd := initSetupOSCmd(runner)

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout

	err := cmd.addOSBoot("/efi", "matrixOS", `\EFI\BOOT\BOOTX64.EFI`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !efibootmgrCalled {
		t.Error("expected efibootmgr to be called")
	}
	if !strings.Contains(efiArgs, "matrixOS on ESP") {
		t.Errorf("expected label in efibootmgr args, got: %s", efiArgs)
	}
	if !strings.Contains(efiArgs, "--disk /dev/sda") {
		t.Errorf("expected disk in efibootmgr args, got: %s", efiArgs)
	}
}

func TestSetupOSAddOSBootNoLabel(t *testing.T) {
	runner := testSetupOSRunner()
	runner.getMountDevice = func(mnt string) (string, error) { return "/dev/sda1", nil }
	runner.getPartitionNumber = func(device string) (string, error) { return "1", nil }
	runner.getPartitionLabel = func(device string) (string, error) { return "", nil }

	cmd := initSetupOSCmd(runner)

	err := cmd.addOSBoot("/efi", "matrixOS", `\EFI\BOOT\BOOTX64.EFI`)
	if err == nil || !strings.Contains(err.Error(), "unable to get partition label") {
		t.Fatalf("expected partition label error, got: %v", err)
	}
}

func TestSetupOSAskInputDefault(t *testing.T) {
	runner := testSetupOSRunner()
	runner.stdin = strings.NewReader("\n")

	cmd := initSetupOSCmd(runner)

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout
	cmd.prompt.Stdout = &stdout

	result, err := cmd.prompt.AskInput("Enter name", "default_val", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "default_val" {
		t.Errorf("expected 'default_val', got %q", result)
	}
}

func TestSetupOSAskInputRegexValidation(t *testing.T) {
	// First: invalid input, then valid input.
	runner := testSetupOSRunner()
	runner.stdin = strings.NewReader("INVALID!\nalice\n")

	cmd := initSetupOSCmd(runner)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.run.stdout = &stdout
	cmd.run.stderr = &stderr
	cmd.prompt.Stdout = &stdout
	cmd.prompt.Stderr = &stderr

	result, err := cmd.prompt.AskInput("Enter username", "default", userRegex)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "alice" {
		t.Errorf("expected 'alice', got %q", result)
	}
	if !strings.Contains(stderr.String(), "Invalid input format") {
		t.Errorf("expected validation error message, got stderr: %s", stderr.String())
	}
}

func TestSetupOSAskInputEOF(t *testing.T) {
	runner := testSetupOSRunner()
	runner.stdin = strings.NewReader("") // EOF immediately.

	cmd := initSetupOSCmd(runner)

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout
	cmd.prompt.Stdout = &stdout

	result, err := cmd.prompt.AskInput("Enter name", "fallback", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "fallback" {
		t.Errorf("expected 'fallback', got %q", result)
	}
}

func TestSetupOSSkipEncryption(t *testing.T) {
	runner := testSetupOSRunner()
	runner.readFile = func(path string) ([]byte, error) {
		// changeLuksPassword should NOT be called, so any readFile
		// for /proc/cmdline here means it leaked through.
		switch path {
		case "/proc/cmdline":
			return []byte("root=UUID=abc rd.luks.uuid=my-luks-uuid rw"), nil
		case "/etc/locale.conf":
			return []byte("LANG=en_US.UTF-8\n"), nil
		case "/etc/vconsole.conf":
			return []byte(""), nil
		}
		return nil, fmt.Errorf("not mocked: %s", path)
	}
	runner.execCommand = func(name string, args ...string) cmdRunner {
		if name == "id" {
			return &mockCmdRunner{outputVal: []byte("1000")}
		}
		return &mockCmdRunner{}
	}
	runner.stat = func(path string) (os.FileInfo, error) {
		if path == "/var/lib/AccountsService/users" {
			return fakeFileInfo{}, nil
		}
		return nil, fmt.Errorf("not found")
	}
	runner.fileExists = func(path string) bool { return false }
	runner.listBlockDevices = func(fields string) ([]string, error) { return nil, nil }
	runner.stdin = strings.NewReader("\n\n\n\n\n")

	cfg := setupOSTestConfig()
	cmd, err := newTestSetupOSCommand(cfg, runner)
	if err != nil {
		t.Fatalf("newTestSetupOSCommand failed: %v", err)
	}
	cmd.skipEncryption = true

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout

	err = cmd.Run()
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Skipping disk encryption") {
		t.Errorf("expected skip-encryption message, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Setup complete") {
		t.Errorf("expected completion message, got: %s", stdout.String())
	}
}

func TestSetupOSFullRunHappyPath(t *testing.T) {
	runner := testSetupOSRunner()

	// Track which commands are executed.
	var cmdLog []string
	runner.execCommand = func(name string, args ...string) cmdRunner {
		entry := name + " " + strings.Join(args, " ")
		cmdLog = append(cmdLog, entry)
		if name == "id" {
			return &mockCmdRunner{outputVal: []byte("1000")}
		}
		if name == "lsblk" && len(args) >= 2 && args[1] == "MOUNTPOINT" {
			return &mockCmdRunner{outputVal: []byte("")}
		}
		return &mockCmdRunner{}
	}
	runner.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/proc/cmdline":
			return []byte("root=UUID=abc rw quiet"), nil
		case "/etc/locale.conf":
			return []byte("LANG=en_US.UTF-8\n"), nil
		case "/etc/vconsole.conf":
			return []byte(""), nil
		}
		return nil, fmt.Errorf("not mocked: %s", path)
	}
	runner.stat = func(path string) (os.FileInfo, error) {
		if path == "/var/lib/AccountsService/users" {
			return fakeFileInfo{}, nil
		}
		return nil, fmt.Errorf("not found")
	}
	runner.fileExists = func(path string) bool {
		return false
	}
	runner.listBlockDevices = func(fields string) ([]string, error) {
		return nil, nil // No EFI partitions.
	}
	// User accepts all defaults (enter key for each prompt).
	runner.stdin = strings.NewReader("\n\n\n\n\n")

	cmd, err := newTestSetupOSCommand(setupOSTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestSetupOSCommand failed: %v", err)
	}

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout

	err = cmd.Run()
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Setup complete") {
		t.Errorf("expected completion message, got: %s", stdout.String())
	}
}

func TestSetupOSFullRunAllFlags(t *testing.T) {
	// Track every external command in order.
	var cmdLog []string
	writtenFiles := make(map[string][]byte)
	var appendedFiles []string

	runner := testSetupOSRunner()
	runner.execCommand = func(name string, args ...string) cmdRunner {
		entry := name + " " + strings.Join(args, " ")
		cmdLog = append(cmdLog, entry)

		switch name {
		case "id":
			return &mockCmdRunner{outputVal: []byte("1000")}
		case "getent":
			// group exists for the default user
			return &mockCmdRunner{outputVal: []byte("matrix:x:1000:")}
		case "lsblk":
			if len(args) >= 2 && args[1] == "MOUNTPOINT" {
				return &mockCmdRunner{outputVal: []byte("")}
			}
			return &mockCmdRunner{}
		default:
			return &mockCmdRunner{}
		}
	}
	runner.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/proc/cmdline":
			return []byte("root=UUID=abc rd.luks.uuid=my-luks-uuid rw ostree=/ostree/boot"), nil
		case "/etc/locale.conf":
			return []byte("LANG=it_IT.UTF-8\n"), nil
		case "/etc/vconsole.conf":
			return []byte("KEYMAP=it\n"), nil
		case "/boot/loader/entries/matrixos-boot.conf":
			return []byte("title matrixOS\noptions root=UUID=abc rw\n"), nil
		}
		return nil, fmt.Errorf("not mocked: %s", path)
	}
	runner.writeFile = func(path string, data []byte, perm os.FileMode) error {
		writtenFiles[path] = data
		return nil
	}
	runner.appendFile = func(path string, data []byte) error {
		appendedFiles = append(appendedFiles, path)
		return nil
	}
	runner.stat = func(path string) (os.FileInfo, error) {
		if path == "/var/lib/AccountsService/users" {
			return fakeFileInfo{}, nil
		}
		return nil, fmt.Errorf("not found")
	}
	runner.fileExists = func(path string) bool {
		switch path {
		case "/dev/disk/by-uuid/my-luks-uuid":
			return true
		case "/usr/share/pixmaps/faces/tree.jpg":
			return true
		case "/efi/efi/BOOT/grub.cfg":
			return true
		}
		if strings.HasSuffix(path, "/EFI/Microsoft/Boot/bootmgfw.efi") {
			return true
		}
		return false
	}
	runner.listBlockDevices = func(fields string) ([]string, error) {
		return []string{
			"/dev/sdb1 C12A7328-F81F-11D2-BA4B-00A0C93EC93B",
		}, nil
	}
	runner.getBlkidValue = func(device, tag string) (string, error) {
		return "WIN-UUID-1234", nil
	}
	runner.glob = func(pattern string) ([]string, error) {
		return []string{"/boot/loader/entries/matrixos-boot.conf"}, nil
	}

	cfg := setupOSTestConfig()
	cmd := &SetupOSCommand{}
	cmd.cfg = cfg
	cmd.StartUI()
	cmd.run = runner
	cmd.prompt = NewPrompter(runner.stdin, runner.stdout, runner.stderr, &cmd.UI)

	// Parse with all flags to avoid any interactive prompts.
	err := cmd.parseArgs([]string{
		"--skip-encryption",
		"--skip-passwords",
		"--username", "alice",
		"--locale", "it_IT.UTF-8",
		"--keymap", "it",
		"--timezone", "Europe/Rome",
		"--hostname", "testbox",
	})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}

	var stdout bytes.Buffer
	cmd.run.stdout = &stdout

	err = cmd.Run()
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}

	output := stdout.String()

	// Verify final message.
	if !strings.Contains(output, "Setup complete") {
		t.Errorf("expected completion message, got: %s", output)
	}

	// Verify skip messages for encryption and passwords.
	if !strings.Contains(output, "Skipping disk encryption") {
		t.Errorf("expected skip-encryption message, got: %s", output)
	}
	if !strings.Contains(output, "Skipping steps 2-4") {
		t.Errorf("expected skip-passwords message, got: %s", output)
	}
	if !strings.Contains(output, "username=alice") {
		t.Errorf("expected username=alice in skip message, got: %s", output)
	}

	// Verify systemd-firstboot was called with all flags and no --prompt.
	var firstbootEntry string
	for _, entry := range cmdLog {
		if strings.HasPrefix(entry, "systemd-firstboot ") {
			firstbootEntry = entry
			break
		}
	}
	if firstbootEntry == "" {
		t.Fatal("expected systemd-firstboot to be called")
	}
	for _, want := range []string{
		"--reset",
		"--locale=it_IT.UTF-8",
		"--keymap=it",
		"--timezone=Europe/Rome",
		"--hostname=testbox",
	} {
		if !strings.Contains(firstbootEntry, want) {
			t.Errorf("expected %q in systemd-firstboot args, got: %s", want, firstbootEntry)
		}
	}
	if strings.Contains(firstbootEntry, "--prompt") {
		t.Errorf("did not expect --prompt when all flags set, got: %s", firstbootEntry)
	}

	// Verify env-update was called (best-effort locale fix).
	found := false
	for _, entry := range cmdLog {
		if entry == "env-update " {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected env-update to be called, cmdLog: %v", cmdLog)
	}

	// Verify AccountsService config was written for alice.
	asCfg, ok := writtenFiles["/var/lib/AccountsService/users/alice"]
	if !ok {
		t.Error("expected AccountsService config to be written for alice")
	} else {
		if !strings.Contains(string(asCfg), "Languages=it_IT.UTF-8") {
			t.Errorf("AccountsService config missing language, got: %s", string(asCfg))
		}
	}

	// Verify keymap was added to BLS entry (ostree path).
	blsData, ok := writtenFiles["/boot/loader/entries/matrixos-boot.conf"]
	if !ok {
		t.Error("expected BLS config to be written with keymap")
	} else {
		if !strings.Contains(string(blsData), "vconsole.keymap=it") {
			t.Errorf("expected vconsole.keymap=it in BLS config, got: %s", string(blsData))
		}
	}

	// Verify Windows detection found the partition and appended grub entry.
	if len(appendedFiles) == 0 {
		t.Error("expected grub.cfg to be appended with Windows entry")
	} else if appendedFiles[0] != "/efi/efi/BOOT/grub.cfg" {
		t.Errorf("expected append to grub.cfg, got: %s", appendedFiles[0])
	}

	// Verify efibootmgr was called.
	var efiEntry string
	for _, entry := range cmdLog {
		if strings.HasPrefix(entry, "efibootmgr ") {
			efiEntry = entry
			break
		}
	}
	if efiEntry == "" {
		t.Fatal("expected efibootmgr to be called")
	}
	if !strings.Contains(efiEntry, "--label") {
		t.Errorf("expected --label in efibootmgr args, got: %s", efiEntry)
	}
	if !strings.Contains(efiEntry, "matrixOS on EFI") {
		t.Errorf("expected 'matrixOS on EFI' in efibootmgr args, got: %s", efiEntry)
	}

	// Verify step ordering: systemd-firstboot before efibootmgr.
	firstbootIdx := -1
	efiIdx := -1
	for i, entry := range cmdLog {
		if strings.HasPrefix(entry, "systemd-firstboot ") && firstbootIdx == -1 {
			firstbootIdx = i
		}
		if strings.HasPrefix(entry, "efibootmgr ") && efiIdx == -1 {
			efiIdx = i
		}
	}
	if firstbootIdx >= efiIdx {
		t.Errorf("expected systemd-firstboot (idx=%d) before efibootmgr (idx=%d)", firstbootIdx, efiIdx)
	}
}
