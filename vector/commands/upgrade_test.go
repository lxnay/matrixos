package commands

import (
	"bytes"
	"fmt"
	"io"
	"matrixos/vector/lib/config"
	"matrixos/vector/lib/ostree"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	mockCurrentSHA = "old-sha"
	mockNewSHA     = "new-sha"
	mockRefSpec    = "remote:branch"
	stateroot      = "matrixos"
)

// newTestUpgradeCommand creates an UpgradeCommand with injected mock dependencies,
// bypassing initConfig/initOstree which require real config files and ostree binary.
func newTestUpgradeCommand(ot ostree.IOstree, args []string) (*UpgradeCommand, error) {
	cmd := &UpgradeCommand{}
	cmd.ot = ot
	cmd.StartUI()
	if err := cmd.parseArgs(args); err != nil {
		return nil, err
	}
	return cmd, nil
}

// newTestUpgradeCommandWithConfig creates an UpgradeCommand with mock ostree.IOstree and
// a real config from a file, for tests that need config values (e.g. bootloader).
func newTestUpgradeCommandWithConfig(ot ostree.IOstree, cfg *config.MockConfig, args []string) (*UpgradeCommand, error) {
	cmd := &UpgradeCommand{}
	cmd.ot = ot
	cmd.cfg = cfg
	cmd.StartUI()
	if err := cmd.parseArgs(args); err != nil {
		return nil, err
	}
	return cmd, nil
}

// upgradeHarness holds common test state for upgrade tests.
type upgradeHarness struct {
	mock    *ostree.MockOstree
	cleanup func()
}

func setupUpgradeHarness(t *testing.T, currentSHA, newSHA string) *upgradeHarness {
	t.Helper()

	origEuid := getEuid
	getEuid = func() int { return 0 }

	// Mock execCommand for ostree ls (package listing)
	origExec := execCommand
	execCommand = func(command string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestUpgradeHelperProcess", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = []string{
			"GO_WANT_UPGRADE_HELPER_PROCESS=1",
			"TEST_CURRENT_SHA=" + currentSHA,
			"TEST_NEW_SHA=" + newSHA,
		}
		return cmd
	}

	mock := &ostree.MockOstree{
		Root_: "/",
		Deployments: []ostree.Deployment{
			{
				Booted:    true,
				Checksum:  currentSHA,
				Stateroot: stateroot,
				Refspec:   mockRefSpec,
			},
		},
		LastCommit_: newSHA,
		PackagesByCommit: map[string][]string{
			currentSHA: {"app-misc/foo-1.0"},
			newSHA:     {"app-misc/foo-1.1"},
		},
	}

	return &upgradeHarness{
		mock: mock,
		cleanup: func() {
			getEuid = origEuid
			execCommand = origExec
		},
	}
}

func runCaptureStdout(f func() error) (string, error) {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := f()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return stripAnsi(buf.String()), err
}

// runCaptureCombined captures both stdout and stderr produced by f. Used by
// tests that need to assert on PrintErrf warnings/errors.
func runCaptureCombined(f func() error) (string, error) {
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	r, w, _ := os.Pipe()
	os.Stdout = w
	os.Stderr = w

	err := f()

	w.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return stripAnsi(buf.String()), err
}

func stripAnsi(str string) string {
	const ansi = "[\u001B\u009B][[\\]()#;?]*(?:(?:(?:[a-zA-Z\\d]*(?:;[a-zA-Z\\d]*)*)?\u0007)|(?:(?:\\d{1,4}(?:;\\d{0,4})*)?[\\dA-PRZcf-ntqry=><~]))"
	re := regexp.MustCompile(ansi)
	return re.ReplaceAllString(str, "")
}

// TestUpgradeHelperProcess is a subprocess helper for execCommand mocking.
// It only handles "ostree ls" (for package listing) and "sbverify".
func TestUpgradeHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_UPGRADE_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}
	if len(args) == 0 {
		os.Exit(1)
	}

	cmd := args[0]

	// Handle sbverify (succeeds unless TEST_SBVERIFY_FAIL is set)
	if cmd == "sbverify" {
		if os.Getenv("TEST_SBVERIFY_FAIL") != "" {
			os.Exit(1)
		}
		return
	}

	// Handle "ostree ls -R <commit> -- <path>"
	if cmd == "ostree" {
		for _, arg := range args {
			if strings.Contains(arg, "/usr/var-db-pkg") {
				os.Exit(1)
				return
			}
		}

		currentSHA := os.Getenv("TEST_CURRENT_SHA")
		newSHA := os.Getenv("TEST_NEW_SHA")

		mockPackages := map[string][]string{
			currentSHA: {
				"d00755 0 0 0 /var/db/pkg/app-misc/foo-1.0/",
				"-00644 0 0 0 /var/db/pkg/app-misc/foo-1.0/CONTENTS",
			},
			newSHA: {
				"d00755 0 0 0 /var/db/pkg/app-misc/foo-1.1/",
				"-00644 0 0 0 /var/db/pkg/app-misc/foo-1.1/CONTENTS",
			},
		}

		for _, arg := range args {
			if pkgs, ok := mockPackages[arg]; ok {
				for _, line := range pkgs {
					fmt.Println(line)
				}
				return
			}
		}
	}

	os.Exit(1)
}

func TestUpgradeRun(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()

	cmd, err := newTestUpgradeCommand(h.mock, []string{"-y"})
	if err != nil {
		t.Fatalf("newTestUpgradeCommand failed: %v", err)
	}

	output, err := runCaptureStdout(func() error {
		return cmd.Run()
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	expected := []string{
		"Checking for updates on branch: " + mockRefSpec,
		"Current version: " + mockCurrentSHA,
		"Fetching updates...",
		"Update Available: " + mockNewSHA,
		"Analyzing package changes...",
		"app-misc/foo-1.0 -> app-misc/foo-1.1",
		"Deploying update...",
		"Upgrade successful!",
		"Please reboot at your earliest convenience.",
	}
	for _, s := range expected {
		if !strings.Contains(output, s) {
			t.Errorf("Missing expected output: %q", s)
		}
	}
}

func TestUpgradeNoUpdate(t *testing.T) {
	h := setupUpgradeHarness(t, mockNewSHA, mockNewSHA)
	defer h.cleanup()

	// Both current and new are the same commit
	h.mock.Deployments[0].Checksum = mockNewSHA

	cmd, err := newTestUpgradeCommand(h.mock, nil)
	if err != nil {
		t.Fatalf("newTestUpgradeCommand failed: %v", err)
	}

	output, err := runCaptureStdout(func() error {
		return cmd.Run()
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !strings.Contains(output, "System is already up to date") {
		t.Errorf("Expected 'System is already up to date', got:\n%s", output)
	}
}

func TestUpgradePretend(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()

	cmd, err := newTestUpgradeCommand(h.mock, []string{"--pretend"})
	if err != nil {
		t.Fatalf("newTestUpgradeCommand failed: %v", err)
	}

	output, err := runCaptureStdout(func() error {
		return cmd.Run()
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	expected := []string{
		"Fetching updates...",
		"Analyzing package changes...",
		"Running in pretend mode. Exiting.",
	}
	for _, s := range expected {
		if !strings.Contains(output, s) {
			t.Errorf("Missing expected output: %q\nGot:\n%s", s, output)
		}
	}
	if strings.Contains(output, "Deploying update...") {
		t.Error("Should not deploy in pretend mode")
	}
}

func TestUpgradeForce(t *testing.T) {
	h := setupUpgradeHarness(t, mockNewSHA, mockNewSHA)
	defer h.cleanup()

	h.mock.Deployments[0].Checksum = mockNewSHA

	cmd, err := newTestUpgradeCommand(h.mock, []string{"--force", "-y"})
	if err != nil {
		t.Fatalf("newTestUpgradeCommand failed: %v", err)
	}

	output, err := runCaptureStdout(func() error {
		return cmd.Run()
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	expected := []string{
		"System is already up to date.",
		"Forcing update despite no changes...",
		"Deploying update...",
		"Upgrade successful!",
	}
	for _, s := range expected {
		if !strings.Contains(output, s) {
			t.Errorf("Missing expected output: %q\nGot:\n%s", s, output)
		}
	}
}

func TestUpgradeAbort(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()

	// Simulate user typing "n"
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() {
		os.Stdin = oldStdin
		r.Close()
	}()
	go func() {
		w.Write([]byte("n\n"))
		w.Close()
	}()

	cmd, err := newTestUpgradeCommand(h.mock, nil)
	if err != nil {
		t.Fatalf("newTestUpgradeCommand failed: %v", err)
	}

	output, err := runCaptureStdout(func() error {
		return cmd.Run()
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !strings.Contains(output, "Aborted.") {
		t.Errorf("Expected 'Aborted.', got:\n%s", output)
	}
	if strings.Contains(output, "Deploying update...") {
		t.Error("Should not deploy after abort")
	}
}

func TestUpgradeBootloaderSuccess(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()

	tmpDir := t.TempDir()

	// Add a non-booted deployment for the new commit (bootloader update needs it)
	h.mock.Deployments = append(h.mock.Deployments, ostree.Deployment{
		Booted:    false,
		Checksum:  mockNewSHA,
		Stateroot: stateroot,
		Refspec:   mockRefSpec,
		Index:     0,
	})
	h.mock.Root_ = tmpDir

	// Create deployment rootfs with grub + shim files
	newRoot := ostree.BuildDeploymentRootfs(tmpDir, stateroot, mockNewSHA, 0)
	grubSrc := filepath.Join(newRoot, "usr/lib/grub/grub-x86_64.efi.signed")
	if err := os.MkdirAll(filepath.Dir(grubSrc), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(grubSrc, []byte("new grub"), 0644); err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(newRoot, "usr/share/shim")
	if err := os.MkdirAll(shimDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shimDir, "shimx64.efi"), []byte("new shim"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create EFI directory with existing grub + certificate
	efiRoot := filepath.Join(tmpDir, "efi")
	grubDir := filepath.Join(efiRoot, "EFI/BOOT")
	if err := os.MkdirAll(grubDir, 0755); err != nil {
		t.Fatal(err)
	}
	existingGrub := filepath.Join(grubDir, "GRUBX64.EFI")
	if err := os.WriteFile(existingGrub, []byte("old grub"), 0644); err != nil {
		t.Fatal(err)
	}
	// mmx64.efi marker tells the upgrader shim is actually deployed here.
	if err := os.WriteFile(filepath.Join(grubDir, "mmx64.efi"), []byte("old mok"), 0644); err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(efiRoot, "secureboot.crt")
	if err := os.WriteFile(certFile, []byte("dummy cert"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.MockConfig{Items: map[string][]string{
		"Imager.EfiRoot":                {efiRoot},
		"Imager.EfiCertificateFileName": {"secureboot.crt"},
	}}

	cmd, err := newTestUpgradeCommandWithConfig(h.mock, cfg, []string{"-y", "--update-bootloader"})
	if err != nil {
		t.Fatalf("newTestUpgradeCommandWithConfig failed: %v", err)
	}

	output, err := runCaptureStdout(func() error {
		return cmd.Run()
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	expected := []string{
		"Updating bootloader binaries...",
		"Found EFI file: " + existingGrub,
		"Verified EFI file: " + existingGrub,
		"Updating GRUB in " + grubDir,
		"Copying grub-x86_64.efi.signed to " + existingGrub,
		"Copying shimx64.efi to " + filepath.Join(grubDir, "shimx64.efi"),
		"Upgrade successful!",
	}
	for _, s := range expected {
		if !strings.Contains(output, s) {
			t.Errorf("Missing expected output: %q\nGot:\n%s", s, output)
		}
	}

	content, err := os.ReadFile(existingGrub)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new grub" {
		t.Errorf("Expected grub content 'new grub', got %q", content)
	}
}

func TestUpgradeBootloaderSystemdBootSuccess(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()

	tmpDir := t.TempDir()

	h.mock.Deployments = append(h.mock.Deployments, ostree.Deployment{
		Booted:    false,
		Checksum:  mockNewSHA,
		Stateroot: stateroot,
		Refspec:   mockRefSpec,
		Index:     0,
	})
	h.mock.Root_ = tmpDir

	// Deployment rootfs ships only systemd-boot signed binary (no grub, no shim).
	newRoot := ostree.BuildDeploymentRootfs(tmpDir, stateroot, mockNewSHA, 0)
	sdSrc := filepath.Join(newRoot, "usr/lib/systemd/boot/efi/systemd-bootx64.efi.signed")
	if err := os.MkdirAll(filepath.Dir(sdSrc), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sdSrc, []byte("new sd-boot"), 0644); err != nil {
		t.Fatal(err)
	}

	// ESP layout: systemd-boot canonical path + fallback BOOTX64.EFI.
	// BOOTX64.EFI must NOT be touched (no mmx64.efi → no shim deploy detected).
	efiRoot := filepath.Join(tmpDir, "efi")
	sdDir := filepath.Join(efiRoot, "EFI/systemd")
	bootDir := filepath.Join(efiRoot, "EFI/BOOT")
	if err := os.MkdirAll(sdDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bootDir, 0755); err != nil {
		t.Fatal(err)
	}
	existingSd := filepath.Join(sdDir, "systemd-bootx64.efi")
	if err := os.WriteFile(existingSd, []byte("old sd-boot"), 0644); err != nil {
		t.Fatal(err)
	}
	fallbackBoot := filepath.Join(bootDir, "BOOTX64.EFI")
	if err := os.WriteFile(fallbackBoot, []byte("untouched fallback"), 0644); err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(efiRoot, "secureboot.crt")
	if err := os.WriteFile(certFile, []byte("dummy cert"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.MockConfig{Items: map[string][]string{
		"Imager.EfiRoot":                {efiRoot},
		"Imager.EfiCertificateFileName": {"secureboot.crt"},
	}}

	cmd, err := newTestUpgradeCommandWithConfig(h.mock, cfg, []string{"-y", "--update-bootloader"})
	if err != nil {
		t.Fatalf("newTestUpgradeCommandWithConfig failed: %v", err)
	}

	output, err := runCaptureStdout(func() error {
		return cmd.Run()
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	expected := []string{
		"Updating bootloader binaries...",
		"Found EFI file: " + existingSd,
		"Verified EFI file: " + existingSd,
		"Updating systemd-boot in " + sdDir,
		"Copying systemd-bootx64.efi.signed to " + existingSd,
		"Upgrade successful!",
	}
	for _, s := range expected {
		if !strings.Contains(output, s) {
			t.Errorf("Missing expected output: %q\nGot:\n%s", s, output)
		}
	}
	unexpected := []string{
		"Updating GRUB in ",
		"Updating shim binaries in ",
	}
	for _, s := range unexpected {
		if strings.Contains(output, s) {
			t.Errorf("Unexpected output present: %q\nGot:\n%s", s, output)
		}
	}

	content, err := os.ReadFile(existingSd)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new sd-boot" {
		t.Errorf("Expected systemd-boot content 'new sd-boot', got %q", content)
	}
	// The fallback BOOTX64.EFI must be left alone since shim is not deployed.
	fb, err := os.ReadFile(fallbackBoot)
	if err != nil {
		t.Fatal(err)
	}
	if string(fb) != "untouched fallback" {
		t.Errorf("BOOTX64.EFI should not be modified, got %q", fb)
	}
}

func TestUpgradeBootloaderShimSkippedWithoutMOK(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()

	tmpDir := t.TempDir()

	h.mock.Deployments = append(h.mock.Deployments, ostree.Deployment{
		Booted:    false,
		Checksum:  mockNewSHA,
		Stateroot: stateroot,
		Refspec:   mockRefSpec,
		Index:     0,
	})
	h.mock.Root_ = tmpDir

	// Deployment ships grub + shim in the commit, but ESP has no mmx64.efi
	// marker anywhere, so shim refresh must be skipped.
	newRoot := ostree.BuildDeploymentRootfs(tmpDir, stateroot, mockNewSHA, 0)
	grubSrc := filepath.Join(newRoot, "usr/lib/grub/grub-x86_64.efi.signed")
	if err := os.MkdirAll(filepath.Dir(grubSrc), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(grubSrc, []byte("new grub"), 0644); err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(newRoot, "usr/share/shim")
	if err := os.MkdirAll(shimDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shimDir, "shimx64.efi"), []byte("new shim"), 0644); err != nil {
		t.Fatal(err)
	}

	efiRoot := filepath.Join(tmpDir, "efi")
	grubDir := filepath.Join(efiRoot, "EFI/BOOT")
	if err := os.MkdirAll(grubDir, 0755); err != nil {
		t.Fatal(err)
	}
	existingGrub := filepath.Join(grubDir, "GRUBX64.EFI")
	if err := os.WriteFile(existingGrub, []byte("old grub"), 0644); err != nil {
		t.Fatal(err)
	}
	certFile := filepath.Join(efiRoot, "secureboot.crt")
	if err := os.WriteFile(certFile, []byte("dummy cert"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.MockConfig{Items: map[string][]string{
		"Imager.EfiRoot":                {efiRoot},
		"Imager.EfiCertificateFileName": {"secureboot.crt"},
	}}

	cmd, err := newTestUpgradeCommandWithConfig(h.mock, cfg, []string{"-y", "--update-bootloader"})
	if err != nil {
		t.Fatalf("newTestUpgradeCommandWithConfig failed: %v", err)
	}

	output, err := runCaptureStdout(func() error {
		return cmd.Run()
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !strings.Contains(output, "Updating GRUB in "+grubDir) {
		t.Errorf("Expected GRUB update, got:\n%s", output)
	}
	if strings.Contains(output, "Updating shim binaries in ") {
		t.Errorf("Shim must not be updated without mmx64.efi marker, got:\n%s", output)
	}
	if strings.Contains(output, "Copying shimx64.efi to ") {
		t.Errorf("shimx64.efi must not be copied without mmx64.efi marker, got:\n%s", output)
	}

	// shimx64.efi must not appear in the ESP.
	if _, err := os.Stat(filepath.Join(grubDir, "shimx64.efi")); !os.IsNotExist(err) {
		t.Errorf("shimx64.efi should not exist in ESP, err=%v", err)
	}
}

// bootloaderTestSetup builds a deployment + ESP scaffold with knobs for the
// edge-case bootloader tests below. It returns the ESP root and the
// deployment newRoot for further per-test tweaks.
type bootloaderFixtureOpts struct {
	withGrubSrc        bool
	withSystemdSrc     bool
	withShimSrc        bool
	espGrub            bool
	espSystemd         bool
	espMOK             bool // place mmx64.efi under espGrub dir (or sd dir if grub absent)
	espMOKExtraDir     string
	espExtraJunk       bool   // unrelated file to ensure walker is robust
	sbverifyFailingDir string // dir whose binaries should fail sbverify (substring match)
}

type bootloaderFixture struct {
	efiRoot  string
	newRoot  string
	grubDir  string
	sdDir    string
	mokExtra string // if espMOKExtraDir set
	certFile string
}

func buildBootloaderFixture(t *testing.T, tmpDir string, opts bootloaderFixtureOpts) *bootloaderFixture {
	t.Helper()
	newRoot := ostree.BuildDeploymentRootfs(tmpDir, stateroot, mockNewSHA, 0)
	if opts.withGrubSrc {
		p := filepath.Join(newRoot, "usr/lib/grub/grub-x86_64.efi.signed")
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("new grub"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if opts.withSystemdSrc {
		p := filepath.Join(newRoot, "usr/lib/systemd/boot/efi/systemd-bootx64.efi.signed")
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("new sd-boot"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if opts.withShimSrc {
		shimDir := filepath.Join(newRoot, "usr/share/shim")
		if err := os.MkdirAll(shimDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(shimDir, "shimx64.efi"), []byte("new shim"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(shimDir, "mmx64.efi"), []byte("new mok"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	efiRoot := filepath.Join(tmpDir, "efi")
	if err := os.MkdirAll(efiRoot, 0755); err != nil {
		t.Fatal(err)
	}
	fx := &bootloaderFixture{efiRoot: efiRoot, newRoot: newRoot}

	if opts.espGrub {
		fx.grubDir = filepath.Join(efiRoot, "EFI/BOOT")
		if err := os.MkdirAll(fx.grubDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fx.grubDir, "GRUBX64.EFI"), []byte("old grub"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if opts.espSystemd {
		fx.sdDir = filepath.Join(efiRoot, "EFI/systemd")
		if err := os.MkdirAll(fx.sdDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fx.sdDir, "systemd-bootx64.efi"), []byte("old sd-boot"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if opts.espMOK {
		// place mmx64.efi next to the grub binary if present, otherwise sd dir.
		target := fx.grubDir
		if target == "" {
			target = fx.sdDir
		}
		if target == "" {
			t.Fatal("espMOK requested but no ESP dir created")
		}
		if err := os.WriteFile(filepath.Join(target, "mmx64.efi"), []byte("old mok"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if opts.espMOKExtraDir != "" {
		fx.mokExtra = filepath.Join(efiRoot, opts.espMOKExtraDir)
		if err := os.MkdirAll(fx.mokExtra, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fx.mokExtra, "mmx64.efi"), []byte("extra mok"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fx.mokExtra, "GRUBX64.EFI"), []byte("old extra grub"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if opts.espExtraJunk {
		junk := filepath.Join(efiRoot, "EFI/junk")
		if err := os.MkdirAll(junk, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(junk, "README.txt"), []byte("hi"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	fx.certFile = filepath.Join(efiRoot, "secureboot.crt")
	if err := os.WriteFile(fx.certFile, []byte("dummy cert"), 0644); err != nil {
		t.Fatal(err)
	}
	return fx
}

// useFailingSbverify swaps execCommand so sbverify exits non-zero.
func useFailingSbverify(t *testing.T) {
	t.Helper()
	prev := execCommand
	execCommand = func(command string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestUpgradeHelperProcess", "--", command}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = []string{
			"GO_WANT_UPGRADE_HELPER_PROCESS=1",
			"TEST_CURRENT_SHA=" + mockCurrentSHA,
			"TEST_NEW_SHA=" + mockNewSHA,
			"TEST_SBVERIFY_FAIL=1",
		}
		return cmd
	}
	t.Cleanup(func() { execCommand = prev })
}

func newCommitDeployment(h *upgradeHarness, tmpDir string) {
	h.mock.Deployments = append(h.mock.Deployments, ostree.Deployment{
		Booted:    false,
		Checksum:  mockNewSHA,
		Stateroot: stateroot,
		Refspec:   mockRefSpec,
		Index:     0,
	})
	h.mock.Root_ = tmpDir
}

func runBootloaderUpgrade(t *testing.T, h *upgradeHarness, fx *bootloaderFixture) (string, error) {
	t.Helper()
	cfg := &config.MockConfig{Items: map[string][]string{
		"Imager.EfiRoot":                {fx.efiRoot},
		"Imager.EfiCertificateFileName": {"secureboot.crt"},
	}}
	cmd, err := newTestUpgradeCommandWithConfig(h.mock, cfg, []string{"-y", "--update-bootloader"})
	if err != nil {
		t.Fatalf("newTestUpgradeCommandWithConfig failed: %v", err)
	}
	return runCaptureCombined(func() error { return cmd.Run() })
}

// TestUpgradeBootloaderShimMissingInCommit covers case (1):
// ESP advertises shim (mmx64.efi present) but the new commit no longer ships
// /usr/share/shim/. We must warn and not fail.
func TestUpgradeBootloaderShimMissingInCommit(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()
	tmpDir := t.TempDir()
	newCommitDeployment(h, tmpDir)

	fx := buildBootloaderFixture(t, tmpDir, bootloaderFixtureOpts{
		withGrubSrc: true,
		withShimSrc: false, // no /usr/share/shim in new commit
		espGrub:     true,
		espMOK:      true,
	})

	output, err := runBootloaderUpgrade(t, h, fx)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !strings.Contains(output, "Updating GRUB in "+fx.grubDir) {
		t.Errorf("Expected GRUB update, got:\n%s", output)
	}
	if !strings.Contains(output, "Shim is deployed in ESP but ") {
		t.Errorf("Expected shim-missing warning, got:\n%s", output)
	}
	if strings.Contains(output, "Copying shimx64.efi to ") {
		t.Errorf("shim must not be copied when missing in commit, got:\n%s", output)
	}
	if !strings.Contains(output, "Upgrade successful!") {
		t.Errorf("Expected upgrade success despite missing shim, got:\n%s", output)
	}
}

// TestUpgradeBootloaderDualBootSkipsShim covers case (2):
// If both GRUB and systemd-boot are detected in ESP, shim refresh is skipped
// with a warning to avoid clobbering bootloaders.
func TestUpgradeBootloaderDualBootSkipsShim(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()
	tmpDir := t.TempDir()
	newCommitDeployment(h, tmpDir)

	fx := buildBootloaderFixture(t, tmpDir, bootloaderFixtureOpts{
		withGrubSrc:    true,
		withSystemdSrc: true,
		withShimSrc:    true,
		espGrub:        true,
		espSystemd:     true,
		espMOK:         true, // mmx64.efi present, but dual-boot must still skip
	})

	output, err := runBootloaderUpgrade(t, h, fx)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !strings.Contains(output, "Updating GRUB in "+fx.grubDir) {
		t.Errorf("Expected GRUB update, got:\n%s", output)
	}
	if !strings.Contains(output, "Updating systemd-boot in "+fx.sdDir) {
		t.Errorf("Expected systemd-boot update, got:\n%s", output)
	}
	if !strings.Contains(output, "Both GRUB and systemd-boot detected") {
		t.Errorf("Expected dual-boot warning, got:\n%s", output)
	}
	if strings.Contains(output, "Updating shim binaries in ") {
		t.Errorf("Shim must not be updated in dual-boot scenario, got:\n%s", output)
	}
}

// TestUpgradeBootloaderMultipleShimDirs covers case (3):
// shim files are copied into every ESP directory that hosts mmx64.efi.
func TestUpgradeBootloaderMultipleShimDirs(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()
	tmpDir := t.TempDir()
	newCommitDeployment(h, tmpDir)

	fx := buildBootloaderFixture(t, tmpDir, bootloaderFixtureOpts{
		withGrubSrc:    true,
		withShimSrc:    true,
		espGrub:        true,
		espMOK:         true,
		espMOKExtraDir: "EFI/matrixos",
	})

	output, err := runBootloaderUpgrade(t, h, fx)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	for _, dir := range []string{fx.grubDir, fx.mokExtra} {
		if !strings.Contains(output, "Updating shim binaries in "+dir) {
			t.Errorf("Expected shim update in %s, got:\n%s", dir, output)
		}
		if !strings.Contains(output, "Copying shimx64.efi to "+filepath.Join(dir, "shimx64.efi")) {
			t.Errorf("Expected shimx64.efi copy into %s, got:\n%s", dir, output)
		}
		if _, err := os.Stat(filepath.Join(dir, "shimx64.efi")); err != nil {
			t.Errorf("shimx64.efi missing from %s: %v", dir, err)
		}
	}
}

// TestUpgradeBootloaderSbverifyFailure covers case (4):
// sbverify failure must abort the upgrade with a non-zero exit and skip the
// copy.
func TestUpgradeBootloaderSbverifyFailure(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()
	useFailingSbverify(t)
	tmpDir := t.TempDir()
	newCommitDeployment(h, tmpDir)

	fx := buildBootloaderFixture(t, tmpDir, bootloaderFixtureOpts{
		withGrubSrc: true,
		espGrub:     true,
		espMOK:      true,
	})
	existingGrub := filepath.Join(fx.grubDir, "GRUBX64.EFI")

	output, err := runBootloaderUpgrade(t, h, fx)
	if err == nil {
		t.Fatalf("Expected upgrade to abort on sbverify failure, got nil\nOutput:\n%s", output)
	}
	if !strings.Contains(err.Error(), "sbverify failed") {
		t.Errorf("Unexpected error: %v", err)
	}
	if !strings.Contains(output, "Error verifying EFI file") {
		t.Errorf("Expected sbverify error in output, got:\n%s", output)
	}
	if strings.Contains(output, "Updating GRUB in ") {
		t.Errorf("GRUB update must not run after sbverify failure, got:\n%s", output)
	}
	content, err := os.ReadFile(existingGrub)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "old grub" {
		t.Errorf("GRUBX64.EFI must be untouched after sbverify failure, got %q", content)
	}
}

// TestUpgradeBootloaderNoEfiBinaries covers case (5):
// ESP exists but contains no recognised bootloader binaries — the walker
// completes cleanly and no updates are performed.
func TestUpgradeBootloaderNoEfiBinaries(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()
	tmpDir := t.TempDir()
	newCommitDeployment(h, tmpDir)

	fx := buildBootloaderFixture(t, tmpDir, bootloaderFixtureOpts{
		withGrubSrc:  true,
		espExtraJunk: true,
	})

	output, err := runBootloaderUpgrade(t, h, fx)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if strings.Contains(output, "Updating GRUB in ") ||
		strings.Contains(output, "Updating systemd-boot in ") ||
		strings.Contains(output, "Updating shim binaries in ") {
		t.Errorf("No updates expected on empty ESP, got:\n%s", output)
	}
	if !strings.Contains(output, "Upgrade successful!") {
		t.Errorf("Upgrade should still succeed with no EFI binaries, got:\n%s", output)
	}
}

// TestUpgradeBootloaderMissingSignedBinary covers case (6):
// ESP has GRUBX64.EFI but the new commit lacks the signed grub binary; the
// upgrade must error cleanly and leave the existing binary in place.
func TestUpgradeBootloaderMissingSignedBinary(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()
	tmpDir := t.TempDir()
	newCommitDeployment(h, tmpDir)

	fx := buildBootloaderFixture(t, tmpDir, bootloaderFixtureOpts{
		withGrubSrc: false, // missing in commit
		espGrub:     true,
		espMOK:      true,
	})
	existingGrub := filepath.Join(fx.grubDir, "GRUBX64.EFI")

	_, err := runBootloaderUpgrade(t, h, fx)
	if err == nil {
		t.Fatal("Expected error for missing signed grub binary, got nil")
	}
	if !strings.Contains(err.Error(), "required bootloader binary missing") {
		t.Errorf("Unexpected error: %v", err)
	}
	content, err := os.ReadFile(existingGrub)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "old grub" {
		t.Errorf("GRUBX64.EFI must be untouched on hard error, got %q", content)
	}
}

func TestUpgradeBootloaderMissingConfig(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()

	cfg := &config.MockConfig{}

	cmd, err := newTestUpgradeCommandWithConfig(h.mock, cfg, []string{"-y", "--update-bootloader"})
	if err != nil {
		t.Fatalf("newTestUpgradeCommandWithConfig failed: %v", err)
	}

	_, err = runCaptureStdout(func() error {
		return cmd.Run()
	})
	if err == nil {
		t.Fatal("Expected error for missing EfiRoot config, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestUpgradeBootloaderMissingCert(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()

	efiRoot := t.TempDir()
	cfg := &config.MockConfig{Items: map[string][]string{
		"Imager.EfiRoot":                {efiRoot},
		"Imager.EfiCertificateFileName": {"nonexistent.crt"},
	}}

	cmd, err := newTestUpgradeCommandWithConfig(h.mock, cfg, []string{"-y", "--update-bootloader"})
	if err != nil {
		t.Fatalf("newTestUpgradeCommandWithConfig failed: %v", err)
	}

	_, err = runCaptureStdout(func() error {
		return cmd.Run()
	})
	if err == nil {
		t.Fatal("Expected error for missing cert, got nil")
	}
}

func TestUpgradeToFlag(t *testing.T) {
	targetCommit := "specific-commit-sha"
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()

	// Add packages for the target commit so analyzeDiff works.
	h.mock.PackagesByCommit[targetCommit] = []string{"app-misc/foo-2.0"}

	cmd, err := newTestUpgradeCommand(h.mock, []string{"-y", "--to", targetCommit})
	if err != nil {
		t.Fatalf("newTestUpgradeCommand failed: %v", err)
	}

	output, err := runCaptureStdout(func() error {
		return cmd.Run()
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// The --to commit should appear as the update target, not LastCommit_.
	if strings.Contains(output, mockNewSHA) {
		t.Errorf("Should not contain LastCommit_ %q when --to is used\nGot:\n%s",
			mockNewSHA, output)
	}
	expected := []string{
		"Update Available: " + targetCommit,
		"Deploying update...",
		"Upgrade successful!",
	}
	for _, s := range expected {
		if !strings.Contains(output, s) {
			t.Errorf("Missing expected output: %q\nGot:\n%s", s, output)
		}
	}
}

func TestUpgradeToFlagPretend(t *testing.T) {
	targetCommit := "pretend-commit-sha"
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()

	h.mock.PackagesByCommit[targetCommit] = []string{"app-misc/foo-3.0"}

	cmd, err := newTestUpgradeCommand(h.mock, []string{"--pretend", "--to", targetCommit})
	if err != nil {
		t.Fatalf("newTestUpgradeCommand failed: %v", err)
	}

	output, err := runCaptureStdout(func() error {
		return cmd.Run()
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	expected := []string{
		"Update Available: " + targetCommit,
		"Running in pretend mode. Exiting.",
	}
	for _, s := range expected {
		if !strings.Contains(output, s) {
			t.Errorf("Missing expected output: %q\nGot:\n%s", s, output)
		}
	}
	if strings.Contains(output, "Deploying update...") {
		t.Error("Should not deploy in pretend mode")
	}
}

func TestUpgradeToFlagSameAsCurrentCommit(t *testing.T) {
	h := setupUpgradeHarness(t, mockCurrentSHA, mockNewSHA)
	defer h.cleanup()

	// Use --to with the same commit as current; should report up to date.
	cmd, err := newTestUpgradeCommand(h.mock, []string{"-y", "--to", mockCurrentSHA})
	if err != nil {
		t.Fatalf("newTestUpgradeCommand failed: %v", err)
	}

	output, err := runCaptureStdout(func() error {
		return cmd.Run()
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !strings.Contains(output, "System is already up to date") {
		t.Errorf("Expected 'System is already up to date' when --to equals current commit\nGot:\n%s", output)
	}
}

func TestUpgradeNotRoot(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 1000 }
	defer func() { getEuid = origEuid }()

	cmd := &UpgradeCommand{}
	cmd.ot = &ostree.MockOstree{}
	cmd.StartUI()
	if err := cmd.parseArgs([]string{"-y"}); err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}

	err := cmd.Run()
	if err == nil {
		t.Fatal("Expected error for non-root, got nil")
	}
	if !strings.Contains(err.Error(), "must be run as root") {
		t.Errorf("Unexpected error: %v", err)
	}
}
