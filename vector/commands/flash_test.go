package commands

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/imager"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/validation"
)

// --- Test helpers ---

// newTestFlashCommand creates a FlashCommand with injected mocks,
// bypassing Init() which requires real config, ostree binary, etc.
func newTestFlashCommand(
	ot ostree.IOstree,
	im *imager.MockImager,
	cfg *config.MockConfig,
	args []string,
) (*FlashCommand, error) {
	cmd := NewFlashCommand()
	cmd.ot = ot
	cmd.im = im
	cmd.cfg = cfg

	qa, err := validation.New(cfg)
	if err != nil {
		return nil, err
	}
	cmd.qa = qa
	cmd.StartUI()

	if err := cmd.parseArgs(args); err != nil {
		return nil, err
	}
	return cmd, nil
}

func defaultFlashTestConfig() *config.MockConfig {
	return &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Sysroot":          {"/sysroot"},
			"Ostree.FullBranchSuffix": {"full"},
			"Ostree.RepoDir":          {"/ostree/repo"},
			"Ostree.Remote":           {"origin"},
			"matrixOS.OsName":         {"matrixos"},
			"matrixOS.Arch":           {"x86_64"},
			"Imager.BootRoot":         {"/boot"},
			"Imager.EfiRoot":          {"/efi"},
			"Imager.Compressor":       {"xz"},
		},
	}
}

func withRootEuid(t *testing.T) {
	t.Helper()
	origEuid := getEuid
	getEuid = func() int { return 0 }
	t.Cleanup(func() { getEuid = origEuid })
}

func withNoSleep(t *testing.T) {
	t.Helper()
	origSleep := sleepFn
	sleepFn = func(time.Duration) {}
	t.Cleanup(func() { sleepFn = origSleep })
}

// --- Name / Constructor tests ---

func TestFlashName(t *testing.T) {
	cmd := NewFlashCommand()
	if name := cmd.Name(); name != "flash" {
		t.Errorf("Expected name 'flash', got %q", name)
	}
}

func TestNewFlashCommand(t *testing.T) {
	cmd := NewFlashCommand()
	if cmd == nil {
		t.Fatal("NewFlashCommand returned nil")
	}
}

// --- parseArgs tests ---

func TestFlashParseArgsMissingRoot(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 1000 }
	defer func() { getEuid = origEuid }()

	cmd := NewFlashCommand()
	err := cmd.parseArgs([]string{})
	if err == nil {
		t.Fatal("Expected error for non-root, got nil")
	}
	if !strings.Contains(err.Error(), "must be run as root") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestFlashParseArgsDefaults(t *testing.T) {
	withRootEuid(t)

	cmd := NewFlashCommand()
	if err := cmd.parseArgs([]string{}); err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}

	if cmd.batch {
		t.Error("Expected batch false by default")
	}
	if cmd.dryRun {
		t.Error("Expected dryRun false by default")
	}
	if cmd.ref != "" {
		t.Errorf("Expected empty ref, got %q", cmd.ref)
	}
}

func TestFlashParseArgsAllFlags(t *testing.T) {
	withRootEuid(t)

	cmd := NewFlashCommand()
	if err := cmd.parseArgs([]string{
		"--batch",
		"--dry-run",
		"--ref", "matrixos/x86_64/dev/gnome",
		"--ostree-repo", "/tmp/repo",
		"--install-device", "/dev/sda",
	}); err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}

	checks := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"batch", cmd.batch, true},
		{"dryRun", cmd.dryRun, true},
		{"ref", cmd.ref, "matrixos/x86_64/dev/gnome"},
		{"repoDir", cmd.repoDir, "/tmp/repo"},
		{"wholeDevice", cmd.wholeDevice, "/dev/sda"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestFlashParseArgsBatchShortFlag(t *testing.T) {
	withRootEuid(t)

	cmd := NewFlashCommand()
	if err := cmd.parseArgs([]string{"-b"}); err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if !cmd.batch {
		t.Error("Expected -b to set batch mode")
	}
}

// --- resolveRef tests ---

func TestFlashResolveRefMissingPair(t *testing.T) {
	withRootEuid(t)

	ot := &ostree.MockOstree{}
	cfg := defaultFlashTestConfig()

	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg,
		[]string{"--ref", "matrixos/x86_64/dev/gnome"})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}

	_, err = cmd.resolveRef()
	if err == nil {
		t.Fatal("Expected error when --ref without --ostree-repo")
	}
	if !strings.Contains(err.Error(), "specify both") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestFlashResolveBootedRefEmpty(t *testing.T) {
	withRootEuid(t)

	ot := &ostree.MockOstree{}
	cfg := defaultFlashTestConfig()

	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg, []string{})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}

	// MockOstree.BootedRef returns "" by default.
	_, err = cmd.resolveRef()
	if err == nil {
		t.Fatal("Expected error for empty booted ref")
	}
	if !strings.Contains(err.Error(), "unable to find booted ostree ref") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestFlashResolveExplicitRefNotFound(t *testing.T) {
	withRootEuid(t)

	repoDir := t.TempDir()
	ot := &ostree.MockOstree{
		LocalRefs_: []string{"matrixos/x86_64/dev/cosmic"},
	}
	cfg := defaultFlashTestConfig()

	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg,
		[]string{"--ref", "matrixos/x86_64/dev/gnome", "--ostree-repo", repoDir})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}

	_, err = cmd.resolveRef()
	if err == nil {
		t.Fatal("Expected error for ref not in local repo")
	}
	if !strings.Contains(err.Error(), "not found in local repo") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestFlashResolveExplicitRefFound(t *testing.T) {
	withRootEuid(t)

	repoDir := t.TempDir()
	ot := &ostree.MockOstree{
		LocalRefs_: []string{"matrixos/x86_64/dev/gnome"},
	}
	cfg := defaultFlashTestConfig()

	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg,
		[]string{"--ref", "matrixos/x86_64/dev/gnome", "--ostree-repo", repoDir})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}
	cmd.SetupPrinters("flash")

	ref, err := cmd.resolveRef()
	if err != nil {
		t.Fatalf("resolveRef failed: %v", err)
	}
	if ref != "matrixos/x86_64/dev/gnome" {
		t.Errorf("Expected ref 'matrixos/x86_64/dev/gnome', got %q", ref)
	}
}

// --- validateDeviceExistence tests ---

func TestValidateDeviceExistenceOK(t *testing.T) {
	opts := &imager.BuildOptions{}
	if err := validateDeviceExistence(opts); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestValidateDeviceExistenceNonExistent(t *testing.T) {
	opts := &imager.BuildOptions{
		EfiDevice: "/dev/nonexistent-xyz-999",
	}
	err := validateDeviceExistence(opts)
	if err == nil {
		t.Fatal("Expected error for non-existent device")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// --- validateDeviceCombination tests ---

func TestValidateDeviceCombinationPartialError(t *testing.T) {
	withRootEuid(t)

	cmd := NewFlashCommand()
	cmd.StartUI()
	opts := &imager.BuildOptions{
		EfiDevice: "/dev/sda1",
	}
	err := cmd.validateDeviceCombination(opts)
	if err == nil {
		t.Fatal("Expected error for partial partitions")
	}
	if !strings.Contains(err.Error(), "please specify all") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestValidateDeviceCombinationConflict(t *testing.T) {
	withRootEuid(t)

	cmd := NewFlashCommand()
	cmd.StartUI()
	opts := &imager.BuildOptions{
		WholeDevice: "/dev/sda",
		EfiDevice:   "/dev/sda1",
		BootDevice:  "/dev/sda2",
		RootDevice:  "/dev/sda3",
	}
	err := cmd.validateDeviceCombination(opts)
	if err == nil {
		t.Fatal("Expected error for conflicting whole + partitions")
	}
	if !strings.Contains(err.Error(), "not both") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestValidateDeviceCombinationBatchNoDevices(t *testing.T) {
	withRootEuid(t)

	cmd := NewFlashCommand()
	cmd.batch = true
	cmd.StartUI()
	opts := &imager.BuildOptions{}
	err := cmd.validateDeviceCombination(opts)
	if err == nil {
		t.Fatal("Expected error for batch mode without devices")
	}
	if !strings.Contains(err.Error(), "batch mode requires") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestValidateDeviceCombinationAllPartitionsOK(t *testing.T) {
	withRootEuid(t)

	cmd := NewFlashCommand()
	cmd.StartUI()
	opts := &imager.BuildOptions{
		EfiDevice:  "/dev/sda1",
		BootDevice: "/dev/sda2",
		RootDevice: "/dev/sda3",
	}
	if err := cmd.validateDeviceCombination(opts); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestValidateDeviceCombinationWholeDeviceOK(t *testing.T) {
	withRootEuid(t)

	cmd := NewFlashCommand()
	cmd.StartUI()
	opts := &imager.BuildOptions{
		WholeDevice: "/dev/sda",
	}
	if err := cmd.validateDeviceCombination(opts); err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

// --- resolveDevicesBatch tests ---

func TestResolveDevicesBatchSummary(t *testing.T) {
	withRootEuid(t)
	withNoSleep(t)

	ot := &ostree.MockOstree{}
	cfg := defaultFlashTestConfig()

	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg,
		[]string{"--batch", "--dry-run", "--install-device", "/dev/null"})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}
	cmd.SetupPrinters("flash")

	opts := &imager.BuildOptions{WholeDevice: "/dev/null"}
	result, err := cmd.resolveDevicesBatch(opts)
	if err != nil {
		t.Fatalf("resolveDevicesBatch failed: %v", err)
	}
	if result.WholeDevice != "/dev/null" {
		t.Errorf("Expected whole device /dev/null, got %q", result.WholeDevice)
	}
}

// --- showSummary tests ---

func TestShowSummaryWholeDevice(t *testing.T) {
	withRootEuid(t)

	ot := &ostree.MockOstree{}
	cfg := defaultFlashTestConfig()

	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg, []string{})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}
	cmd.SetupPrinters("flash")

	opts := &imager.BuildOptions{WholeDevice: "/dev/sda"}
	// Just ensure no panic.
	cmd.showSummary(opts)
}

func TestShowSummaryPartitions(t *testing.T) {
	withRootEuid(t)

	ot := &ostree.MockOstree{}
	cfg := defaultFlashTestConfig()

	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg, []string{})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}
	cmd.SetupPrinters("flash")

	opts := &imager.BuildOptions{
		EfiDevice:  "/dev/sda1",
		BootDevice: "/dev/sda2",
		RootDevice: "/dev/sda3",
	}
	// Just ensure no panic.
	cmd.showSummary(opts)
}

// --- resolveDevices with real temp files ---

func TestResolveDevicesBatchWholeDevice(t *testing.T) {
	withRootEuid(t)
	withNoSleep(t)

	tmpFile, err := os.CreateTemp("", "flash-dev")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	ot := &ostree.MockOstree{}
	cfg := defaultFlashTestConfig()

	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg,
		[]string{"--batch", "--install-device", tmpFile.Name()})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}
	cmd.SetupPrinters("flash")

	opts, err := cmd.resolveDevices()
	if err != nil {
		t.Fatalf("resolveDevices failed: %v", err)
	}
	if opts.WholeDevice != tmpFile.Name() {
		t.Errorf("Expected %q, got %q", tmpFile.Name(), opts.WholeDevice)
	}
}

func TestResolveDevicesBatchAllPartitions(t *testing.T) {
	withRootEuid(t)
	withNoSleep(t)

	efiFile, _ := os.CreateTemp("", "efi-dev")
	bootFile, _ := os.CreateTemp("", "boot-dev")
	rootFile, _ := os.CreateTemp("", "root-dev")
	defer os.Remove(efiFile.Name())
	defer os.Remove(bootFile.Name())
	defer os.Remove(rootFile.Name())
	efiFile.Close()
	bootFile.Close()
	rootFile.Close()

	ot := &ostree.MockOstree{}
	cfg := defaultFlashTestConfig()

	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg,
		[]string{
			"--batch",
			"--efi-device-path", efiFile.Name(),
			"--boot-device-path", bootFile.Name(),
			"--root-device-path", rootFile.Name(),
		})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}
	cmd.SetupPrinters("flash")

	opts, err := cmd.resolveDevices()
	if err != nil {
		t.Fatalf("resolveDevices failed: %v", err)
	}
	if opts.EfiDevice != efiFile.Name() || opts.BootDevice != bootFile.Name() || opts.RootDevice != rootFile.Name() {
		t.Error("Device paths not returned correctly")
	}
}

func TestResolveDevicesNonExistent(t *testing.T) {
	withRootEuid(t)

	ot := &ostree.MockOstree{}
	cfg := defaultFlashTestConfig()

	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg,
		[]string{"--batch", "--install-device", "/dev/nonexistent-flash-test-xyz"})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}
	cmd.SetupPrinters("flash")

	_, err = cmd.resolveDevices()
	if err == nil {
		t.Fatal("Expected error for non-existent device")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// --- resolveExplicitRef with shortname expansion ---

func TestFlashResolveExplicitRefShortname(t *testing.T) {
	withRootEuid(t)

	repoDir := t.TempDir()
	ot := &ostree.MockOstree{
		LocalRefs_: []string{"matrixos/x86_64/dev/gnome"},
		OsName_:    "matrixos",
		Arch_:      "x86_64",
	}
	cfg := defaultFlashTestConfig()

	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg,
		[]string{"--ref", "gnome", "--ostree-repo", repoDir})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}
	cmd.SetupPrinters("flash")

	ref, err := cmd.resolveRef()
	if err != nil {
		t.Fatalf("resolveRef failed: %v", err)
	}
	if ref != "matrixos/x86_64/dev/gnome" {
		t.Errorf("Expected expanded ref, got %q", ref)
	}
}

// --- resolveDevicesInteractive with cancelled user ---

func TestResolveDevicesInteractiveCancelled(t *testing.T) {
	withRootEuid(t)
	withNoSleep(t)

	for _, tc := range []struct {
		name  string
		input string
	}{
		{"PartitionPromptEOF", "no\n"},
		{"WholeDevicePromptEOF", "yes\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ot := &ostree.MockOstree{}
			cfg := defaultFlashTestConfig()
			cfg.Items["Imager.EspPartitionType"] = []string{"c12a7328-f81f-11d2-ba4b-00a0c93ec93b"}
			cfg.Items["Imager.BootPartitionType"] = []string{"bc13c2ff-59e6-4262-a352-b275fd6f7172"}
			cfg.Items["Imager.RootPartitionType"] = []string{"4f68bce3-e8cd-4db1-96e7-fbcaf984b709"}

			cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg, []string{})
			if err != nil {
				t.Fatalf("newTestFlashCommand failed: %v", err)
			}
			cmd.SetupPrinters("flash")
			cmd.printer = newStyledWriter(io.Discard, "", "", "", false)
			cmd.errPrinter = newStyledWriter(io.Discard, "", "", "", false)
			cmd.prompter = NewPrompter(strings.NewReader(tc.input), io.Discard, io.Discard, &cmd.UI)

			errCh := make(chan error, 1)
			go func() {
				_, rErr := cmd.resolveDevicesInteractive(&imager.BuildOptions{})
				errCh <- rErr
			}()

			select {
			case err = <-errCh:
				if err == nil {
					t.Fatal("Expected error from interactive prompt on EOF")
				}
			case <-time.After(300 * time.Millisecond):
				t.Fatalf("resolveDevicesInteractive(%q) spun in an infinite busy-loop on stdin EOF", tc.input)
			}
		})
	}
}

// --- detectRemotedAndPlainRefs call-site tests ---

func TestFlashDetectRemotedAndPlainRefs_Clean(t *testing.T) {
	withRootEuid(t)

	ot := &ostree.MockOstree{
		LocalRefs_: []string{
			"matrixos/x86_64/dev/gnome",
			"matrixos/x86_64/dev/cosmic",
		},
	}
	cfg := defaultFlashTestConfig()
	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg, []string{})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}
	cmd.SetupPrinters("flash")

	// No duplicates — should pass.
	if err := cmd.detectRemotedAndPlainRefs(cmd.im.PrintError); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFlashDetectRemotedAndPlainRefs_Ambiguous(t *testing.T) {
	withRootEuid(t)

	ot := &ostree.MockOstree{
		LocalRefs_: []string{
			"origin:matrixos/x86_64/dev/gnome",
			"matrixos/x86_64/dev/gnome",
		},
	}
	cfg := defaultFlashTestConfig()
	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg, []string{})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}
	cmd.SetupPrinters("flash")

	err = cmd.detectRemotedAndPlainRefs(cmd.im.PrintError)
	if err == nil {
		t.Fatal("expected error for ambiguous refs, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous refs detected") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFlashDetectRemotedAndPlainRefs_LocalRefsError(t *testing.T) {
	withRootEuid(t)

	ot := &ostree.MockOstree{
		LocalRefsErr: fmt.Errorf("localrefs failed"),
	}
	cfg := defaultFlashTestConfig()
	cmd, err := newTestFlashCommand(ot, imager.DefaultMockImager(), cfg, []string{})
	if err != nil {
		t.Fatalf("newTestFlashCommand failed: %v", err)
	}
	cmd.SetupPrinters("flash")

	var warnings []string
	errf := func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}
	err = cmd.detectRemotedAndPlainRefs(errf)
	if err != nil {
		t.Fatalf("expected no error (LocalRefs failure should be ignored), got: %v", err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected warning message from errf callback")
	}
	combined := strings.Join(warnings, " ")
	if !strings.Contains(combined, "WARNING: failed to list local refs") {
		t.Errorf("expected warning about failed local refs, got: %s", combined)
	}
	if !strings.Contains(combined, "localrefs failed") {
		t.Errorf("expected warning to contain wrapped error, got: %s", combined)
	}
}

// --- Comprehensive Run() integration tests ---

// withFakeInstallRoot creates a temporary directory tree that satisfies
// QA.VerifyImagerEnvironmentSetup: executable stubs in usr/bin/ and
// the required /usr/share/shim directory.
func withFakeInstallRoot(t *testing.T) {
	t.Helper()
	root := t.TempDir()

	binDir := root + "/usr/bin"
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root+"/usr/share/shim", 0o755); err != nil {
		t.Fatal(err)
	}

	for _, exe := range []string{
		"blockdev", "btrfs", "chroot", "cryptsetup", "efibootmgr",
		"findmnt", "fstrim", "gpg", "grub-install", "losetup",
		"mkfs.vfat", "mkfs.btrfs", "openssl", "ostree", "partprobe",
		"qemu-img", "qemu-system-x86_64", "sgdisk", "unshare",
		"udevadm", "xz",
	} {
		if err := os.WriteFile(binDir+"/"+exe, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	origRoot := installRoot
	installRoot = root
	t.Cleanup(func() { installRoot = origRoot })
}

// newRunTestFlashCommand builds a FlashCommand pre-wired with mock factories
// so that Run() can execute end-to-end without real filesystems/system tools.
func newRunTestFlashCommand(
	t *testing.T,
	ot *ostree.MockOstree,
	im *imager.MockImager,
	cfg *config.MockConfig,
	args []string,
) *FlashCommand {
	t.Helper()
	withRootEuid(t)
	withNoSleep(t)
	withFakeInstallRoot(t)

	// newTestFlashCommand pre-injects ot, im, cfg and qa.
	// runFlash() creates a real Fsenc from cfg (encryption disabled in test config).
	cmd, err := newTestFlashCommand(ot, im, cfg, args)
	if err != nil {
		t.Fatalf("newTestFlashCommand: %v", err)
	}
	return cmd
}

func TestFlashRunBlockDevice(t *testing.T) {
	// Create a temp file to act as the "whole block device".
	devFile, err := os.CreateTemp("", "flash-blkdev-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(devFile.Name())
	devFile.Close()

	repoDir := t.TempDir()
	ref := "matrixos/x86_64/dev/gnome"

	ot := &ostree.MockOstree{
		LocalRefs_: []string{ref},
		Remote_:    "origin",
		OsName_:    "matrixos",
		Arch_:      "x86_64",
	}
	im := imager.DefaultMockImager()
	cfg := defaultFlashTestConfig()

	cmd := newRunTestFlashCommand(t, ot, im, cfg, []string{
		"--batch",
		"--ref", ref,
		"--ostree-repo", repoDir,
		"--install-device", devFile.Name(),
	})

	if err := cmd.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	// Verify Build() was called.
	if !im.BuildCalled {
		t.Error("expected Build() to be called")
	}

	// Verify the ref was propagated to the mock imager.
	if im.Ref_ != ref {
		t.Errorf("imager ref: got %q, want %q", im.Ref_, ref)
	}

	// Verify the ref was propagated to the mock ostree.
	if ot.Ref_ != ref {
		t.Errorf("ostree ref: got %q, want %q", ot.Ref_, ref)
	}

	// Verify cleanup was invoked.
	if !im.CleanupCalled {
		t.Error("expected imager Cleanup() to be called")
	}
}

func TestFlashRunImageFile(t *testing.T) {
	// Create temp files to act as individual partition devices.
	efiFile, err := os.CreateTemp("", "flash-efi-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(efiFile.Name())
	efiFile.Close()

	bootFile, err := os.CreateTemp("", "flash-boot-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(bootFile.Name())
	bootFile.Close()

	rootFile, err := os.CreateTemp("", "flash-root-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(rootFile.Name())
	rootFile.Close()

	repoDir := t.TempDir()
	ref := "matrixos/x86_64/dev/cosmic"

	ot := &ostree.MockOstree{
		LocalRefs_: []string{ref},
		Remote_:    "origin",
		OsName_:    "matrixos",
		Arch_:      "x86_64",
	}
	im := imager.DefaultMockImager()
	cfg := defaultFlashTestConfig()

	cmd := newRunTestFlashCommand(t, ot, im, cfg, []string{
		"--batch",
		"--ref", ref,
		"--ostree-repo", repoDir,
		"--efi-device-path", efiFile.Name(),
		"--boot-device-path", bootFile.Name(),
		"--root-device-path", rootFile.Name(),
	})

	if err := cmd.Run(); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	// Verify Build() was called.
	if !im.BuildCalled {
		t.Error("expected Build() to be called")
	}

	// Verify the ref was propagated.
	if im.Ref_ != ref {
		t.Errorf("imager ref: got %q, want %q", im.Ref_, ref)
	}
	if ot.Ref_ != ref {
		t.Errorf("ostree ref: got %q, want %q", ot.Ref_, ref)
	}

	// Verify cleanup was invoked.
	if !im.CleanupCalled {
		t.Error("expected imager Cleanup() to be called")
	}
}
