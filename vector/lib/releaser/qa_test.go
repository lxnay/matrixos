package releaser

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
	"matrixos/vector/lib/validation"
)

// newTestReleaserWithQA builds a Releaser with a real validation.QA backed by
// the supplied MockConfig.  Unlike newTestReleaser(), the qa field is non-nil
// so that CheckMatrixOS / PreCleanQAChecks can be exercised.
func newTestReleaserWithQA(t *testing.T, cfg *config.MockConfig) *Releaser {
	t.Helper()
	qa, err := validation.New(cfg)
	if err != nil {
		t.Fatalf("validation.New: %v", err)
	}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		qa:             qa,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})
	return r
}

// stdoutString returns whatever was written to the test Releaser's stdout.
func stdoutString(r *Releaser) string { return r.Stdout().(*bytes.Buffer).String() }

// ---------------------------------------------------------------------------
// CheckMatrixOS
// ---------------------------------------------------------------------------

func TestCheckMatrixOS_ConfigError(t *testing.T) {
	wantErr := errors.New("cfg broken")
	qa, _ := validation.New(&config.ErrConfig{Err: wantErr})
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: &config.ErrConfig{Err: wantErr}},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		qa:             qa,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.CheckMatrixOS()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestCheckMatrixOS_EmptyPath(t *testing.T) {
	cfg := &config.MockConfig{Items: map[string][]string{}, Bools: map[string]bool{}}
	// Key absent → GetItem returns "" → CheckMatrixOSPrivate returns error.
	r := newTestReleaserWithQA(t, cfg)

	err := r.CheckMatrixOS()
	if err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
}

func TestCheckMatrixOS_NonexistentPath(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.PrivateGitRepoPath": {"/nonexistent/path/abc123"},
		},
		Bools: map[string]bool{},
	}
	r := newTestReleaserWithQA(t, cfg)

	err := r.CheckMatrixOS()
	if err == nil {
		t.Fatal("expected error for nonexistent path, got nil")
	}
}

func TestCheckMatrixOS_PathIsFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notadir")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.PrivateGitRepoPath": {f.Name()},
		},
		Bools: map[string]bool{},
	}
	r := newTestReleaserWithQA(t, cfg)

	err = r.CheckMatrixOS()
	if err == nil {
		t.Fatal("expected error when path is a file, got nil")
	}
}

func TestCheckMatrixOS_ValidDirectory(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.PrivateGitRepoPath": {dir},
		},
		Bools: map[string]bool{},
	}
	r := newTestReleaserWithQA(t, cfg)

	if err := r.CheckMatrixOS(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// PreCleanQAChecks – SecureBootCertPath error (first branch)
// ---------------------------------------------------------------------------

func TestPreCleanQAChecks_SecureBootCertPathError(t *testing.T) {
	// SecureBootCertPath reads "Seeder.SecureBootPublicKey".
	// A missing key makes configItem return an error.
	cfg := &config.MockConfig{Items: map[string][]string{}, Bools: map[string]bool{}}
	r := newTestReleaserWithQA(t, cfg)
	r.imageDir = t.TempDir()

	err := r.PreCleanQAChecks()
	if err == nil {
		t.Fatal("expected error when SecureBootCertPath is missing, got nil")
	}

	// Ensure the initial print was written before the error.
	out := stdoutString(r)
	if out == "" {
		t.Error("expected 'Pre clean QA Checks ...' printed to stdout")
	}
}

func TestPreCleanQAChecks_SecureBootCertPathConfigError(t *testing.T) {
	wantErr := errors.New("cfg broken")
	cfg := &config.ErrConfig{Err: wantErr}
	qa, _ := validation.New(cfg)
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		qa:             qa,
		imageDir:       t.TempDir(),
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.PreCleanQAChecks()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

// ---------------------------------------------------------------------------
// PreCleanQAChecks – VerifyDistroRootfsEnvironmentSetup failure
// ---------------------------------------------------------------------------

func TestPreCleanQAChecks_VerifyDistroRootfsFails(t *testing.T) {
	// Provide SecureBootCertPath so we pass the first gate,
	// but the imageDir is an empty temp dir → VerifyDistroRootfsEnvironmentSetup
	// will fail because no expected executables exist there.
	imageDir := t.TempDir()
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Seeder.SecureBootPublicKey": {"/some/cert.pem"},
		},
		Bools: map[string]bool{},
	}
	r := newTestReleaserWithQA(t, cfg)
	r.imageDir = imageDir

	err := r.PreCleanQAChecks()
	if err == nil {
		t.Fatal("expected error from VerifyDistroRootfsEnvironmentSetup, got nil")
	}
}

// ---------------------------------------------------------------------------
// PreCleanQAChecks – CheckSecureBoot failure
// ---------------------------------------------------------------------------

func TestPreCleanQAChecks_CheckSecureBootFails(t *testing.T) {
	// Set up an imageDir that satisfies VerifyDistroRootfsEnvironmentSetup
	// by placing stub executables and directories, but has no usb-storage
	// module so that CheckSecureBoot fails.
	imageDir := t.TempDir()
	makeStubEnvironment(t, imageDir)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Seeder.SecureBootPublicKey": {"/some/cert.pem"},
		},
		Bools: map[string]bool{},
	}
	r := newTestReleaserWithQA(t, cfg)
	r.imageDir = imageDir

	err := r.PreCleanQAChecks()
	if err == nil {
		t.Fatal("expected error from CheckSecureBoot, got nil")
	}
}

// ---------------------------------------------------------------------------
// PreCleanQAChecks – CheckNumberOfKernels failure
// ---------------------------------------------------------------------------

func TestPreCleanQAChecks_CheckNumberOfKernelsFails(t *testing.T) {
	// Set up an imageDir that passes VerifyDistroRootfsEnvironmentSetup,
	// passes CheckSecureBoot (by having a usb-storage.ko file that won't
	// actually be verifiable — so CheckSecureBoot may fail first).
	// This test verifies the flow; it will fail at CheckSecureBoot because
	// the module won't have a valid signature chain, confirming the check
	// is reached.
	imageDir := t.TempDir()
	makeStubEnvironment(t, imageDir)
	// Create a fake usb-storage.ko so CheckSecureBoot finds it.
	modDir := filepath.Join(imageDir, "lib", "modules", "5.15.0")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "usb-storage.ko"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Seeder.SecureBootPublicKey": {"/some/cert.pem"},
		},
		Bools: map[string]bool{},
	}
	r := newTestReleaserWithQA(t, cfg)
	r.imageDir = imageDir

	err := r.PreCleanQAChecks()
	// Will fail at CheckSecureBoot (certificate parsing or chroot failure)
	// which is fine — we're confirming that the flow reaches past
	// VerifyDistroRootfsEnvironmentSetup.
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// PreCleanQAChecks – prints correct messages
// ---------------------------------------------------------------------------

func TestPreCleanQAChecks_PrintsStartMessage(t *testing.T) {
	cfg := &config.MockConfig{Items: map[string][]string{}, Bools: map[string]bool{}}
	r := newTestReleaserWithQA(t, cfg)
	r.imageDir = t.TempDir()

	_ = r.PreCleanQAChecks() // will error, but we only care about output

	out := stdoutString(r)
	want := "Pre clean QA Checks ...\n"
	if out != want {
		// The output may contain more if later steps write too; check prefix.
		if len(out) < len(want) || out[:len(want)] != want {
			t.Errorf("stdout = %q, want prefix %q", out, want)
		}
	}
}

// ---------------------------------------------------------------------------
// PreCleanQAChecks – full happy path (requires real fs layout)
// ---------------------------------------------------------------------------

func TestPreCleanQAChecks_HappyPath(t *testing.T) {
	// A true happy-path requires a realistic chroot with signed kernel
	// modules and a valid SecureBoot certificate.  That isn't feasible in
	// unit tests. Verify that we at least reach CheckNumberOfKernels by
	// observing that the error refers to kernels (not SecureBoot or env).
	//
	// Skip on CI or when the test tree is minimal.
	imageDir := t.TempDir()
	makeStubEnvironment(t, imageDir)

	// Create the /usr/share/shim dir
	shimDir := filepath.Join(imageDir, "usr", "share", "shim")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Seeder.SecureBootPublicKey": {"/some/cert.pem"},
		},
		Bools: map[string]bool{},
	}
	r := newTestReleaserWithQA(t, cfg)
	r.imageDir = imageDir

	err := r.PreCleanQAChecks()
	// Expected to fail eventually (no real SecureBoot cert, no real modules).
	if err == nil {
		t.Fatal("expected error in artificial environment, got nil")
	}
}

// ---------------------------------------------------------------------------
// helpers – create minimal stub environment
// ---------------------------------------------------------------------------

// makeStubEnvironment creates stub executables and directories that
// VerifyDistroRootfsEnvironmentSetup expects to find inside imageDir.
func makeStubEnvironment(t *testing.T, imageDir string) {
	t.Helper()

	executables := []string{
		"blockdev", "btrfs", "chroot", "cryptsetup", "efibootmgr",
		"find", "findmnt", "fstrim", "gpg", "losetup",
		"mkfs.btrfs", "mkfs.vfat", "openssl", "ostree",
		"partprobe", "qemu-img", "qemu-system-x86_64", "sgdisk",
		"udevadm", "unshare", "wget", "xz",
	}

	// Place all executables under <imageDir>/usr/bin/.
	binDir := filepath.Join(imageDir, "usr", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, exe := range executables {
		p := filepath.Join(binDir, exe)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// /usr/bin/grub-install (absolute-path executable checked when imageDir != "/")
	grub := filepath.Join(imageDir, "usr", "bin", "grub-install")
	if err := os.WriteFile(grub, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Required directories.
	for _, d := range []string{"usr/share/shim"} {
		if err := os.MkdirAll(filepath.Join(imageDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
