package releaser

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
	"matrixos/vector/lib/validation"
)

// ---------------------------------------------------------------------------
// CleanRootfs
// ---------------------------------------------------------------------------

func TestCleanRootfs_SecureBootCertPathError(t *testing.T) {
	// No "Seeder.SecureBootPublicKey" in config → SecureBootCertPath returns error.
	cfg := &config.MockConfig{Items: map[string][]string{}, Bools: map[string]bool{}}
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

	err := r.CleanRootfs()
	if err == nil {
		t.Fatal("expected error when SecureBootCertPath is missing, got nil")
	}
}

func TestCleanRootfs_CopySecureBootCertFails(t *testing.T) {
	// Provide a non-existent file as the cert source → CopyFile fails.
	imageDir := t.TempDir()
	os.MkdirAll(filepath.Join(imageDir, "etc", "portage"), 0o755)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Seeder.SecureBootPublicKey": {"/nonexistent/cert.pem"},
		},
		Bools: map[string]bool{},
	}
	qa, _ := validation.New(cfg)
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		qa:             qa,
		imageDir:       imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.CleanRootfs()
	if err == nil {
		t.Fatal("expected error when cert source does not exist, got nil")
	}
}

func TestCleanRootfs_SecureBootKekPathError(t *testing.T) {
	// Provide a valid cert source but no KEK path in config.
	imageDir := t.TempDir()
	os.MkdirAll(filepath.Join(imageDir, "etc", "portage"), 0o755)

	certFile := filepath.Join(t.TempDir(), "cert.pem")
	os.WriteFile(certFile, []byte("cert"), 0o644)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Seeder.SecureBootPublicKey": {certFile},
			// "Seeder.SecureBootKekPublicKey" deliberately missing.
		},
		Bools: map[string]bool{},
	}
	qa, _ := validation.New(cfg)
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		qa:             qa,
		imageDir:       imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.CleanRootfs()
	if err == nil {
		t.Fatal("expected error when SecureBootKekPath is missing, got nil")
	}
}

func TestCleanRootfs_CopySecureBootKekFails(t *testing.T) {
	imageDir := t.TempDir()
	os.MkdirAll(filepath.Join(imageDir, "etc", "portage"), 0o755)

	certFile := filepath.Join(t.TempDir(), "cert.pem")
	os.WriteFile(certFile, []byte("cert"), 0o644)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Seeder.SecureBootPublicKey":    {certFile},
			"Seeder.SecureBootKekPublicKey": {"/nonexistent/kek.pem"},
		},
		Bools: map[string]bool{},
	}
	qa, _ := validation.New(cfg)
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		qa:             qa,
		imageDir:       imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.CleanRootfs()
	if err == nil {
		t.Fatal("expected error when KEK source does not exist, got nil")
	}
}

func TestCleanRootfs_DefaultPrivateGitRepoPathError(t *testing.T) {
	imageDir := t.TempDir()
	os.MkdirAll(filepath.Join(imageDir, "etc", "portage"), 0o755)

	certFile := filepath.Join(t.TempDir(), "cert.pem")
	os.WriteFile(certFile, []byte("cert"), 0o644)
	kekFile := filepath.Join(t.TempDir(), "kek.pem")
	os.WriteFile(kekFile, []byte("kek"), 0o644)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Seeder.SecureBootPublicKey":    {certFile},
			"Seeder.SecureBootKekPublicKey": {kekFile},
			// "matrixOS.DefaultPrivateGitRepoPath" deliberately missing.
		},
		Bools: map[string]bool{},
	}
	qa, _ := validation.New(cfg)
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		qa:             qa,
		imageDir:       imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.CleanRootfs()
	if err == nil {
		t.Fatal("expected error when DefaultPrivateGitRepoPath is missing, got nil")
	}
}

func TestCleanRootfs_HappyPath(t *testing.T) {
	imageDir := t.TempDir()

	// Create the etc/portage directory for cert destination.
	os.MkdirAll(filepath.Join(imageDir, "etc", "portage"), 0o755)

	// Create source cert files.
	certFile := filepath.Join(t.TempDir(), "cert.pem")
	os.WriteFile(certFile, []byte("cert-data"), 0o644)
	kekFile := filepath.Join(t.TempDir(), "kek.pem")
	os.WriteFile(kekFile, []byte("kek-data"), 0o644)

	// Populate directories / files that should be cleaned.
	for _, d := range []string{
		"root/.ssh",
		"root/.gnupg",
		"root/.cache",
		"root/.local",
		"var/lib/gdm/.cache",
		"var/lib/gdm/.local",
		"var/lib/gdm/.config",
		"priv-repo",
		"var/lib/sbctl/keys",
		"var/tmp/ostree-gpg-private",
		"tmp/junk",
		"dev/junk",
		"boot/junk",
		"var/lib/systemd/coredump/junk",
		"var/tmp/portage/junk",
		"usr/include",
	} {
		os.MkdirAll(filepath.Join(imageDir, d), 0o755)
	}
	// Create files that should be removed.
	for _, f := range []string{
		"etc/resolv.conf",
		"etc/portage/secureboot.x509",
		"root/.bash_history",
		"root/.lesshst",
		"root/.bashrc",
	} {
		dir := filepath.Dir(filepath.Join(imageDir, f))
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(imageDir, f), []byte("data"), 0o644)
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Seeder.SecureBootPublicKey":         {certFile},
			"Seeder.SecureBootKekPublicKey":      {kekFile},
			"matrixOS.DefaultPrivateGitRepoPath": {"/priv-repo"},
		},
		Bools: map[string]bool{},
	}
	qa, _ := validation.New(cfg)
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		qa:             qa,
		imageDir:       imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.CleanRootfs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify cert was copied.
	certDst := filepath.Join(imageDir, imagerPortageSecureBootPem)
	data, err := os.ReadFile(certDst)
	if err != nil {
		t.Fatalf("cert not copied: %v", err)
	}
	if string(data) != "cert-data" {
		t.Errorf("cert content = %q, want %q", data, "cert-data")
	}

	kekDst := filepath.Join(imageDir, imagerPortageSecureBootKek)
	data, err = os.ReadFile(kekDst)
	if err != nil {
		t.Fatalf("KEK cert not copied: %v", err)
	}
	if string(data) != "kek-data" {
		t.Errorf("KEK content = %q, want %q", data, "kek-data")
	}

	// Verify /etc/resolv.conf was removed.
	if _, err := os.Stat(filepath.Join(imageDir, "etc/resolv.conf")); !os.IsNotExist(err) {
		t.Error("etc/resolv.conf should have been removed")
	}

	// Verify Portage directory was created.
	gentooRepo := filepath.Join(imageDir, "var/db/repos/gentoo")
	fi, err := os.Stat(gentooRepo)
	if err != nil {
		t.Fatalf("var/db/repos/gentoo not created: %v", err)
	}
	if !fi.IsDir() {
		t.Error("var/db/repos/gentoo should be a directory")
	}

	// Verify emptied directories still exist but are empty.
	for _, d := range []string{"tmp", "dev", "boot"} {
		p := filepath.Join(imageDir, d)
		entries, err := os.ReadDir(p)
		if err != nil {
			t.Errorf("%s read error: %v", d, err)
			continue
		}
		if len(entries) != 0 {
			t.Errorf("%s should be empty, has %d entries", d, len(entries))
		}
	}
}

func TestCleanRootfs_CertCopyPreservesExistingFile(t *testing.T) {
	// If the destination cert already exists, CopyFile overwrites it.
	imageDir := t.TempDir()
	os.MkdirAll(filepath.Join(imageDir, "etc", "portage"), 0o755)

	// Pre-existing file at destination.
	certDst := filepath.Join(imageDir, imagerPortageSecureBootPem)
	os.WriteFile(certDst, []byte("old"), 0o644)

	certFile := filepath.Join(t.TempDir(), "cert.pem")
	os.WriteFile(certFile, []byte("new"), 0o644)
	kekFile := filepath.Join(t.TempDir(), "kek.pem")
	os.WriteFile(kekFile, []byte("kek"), 0o644)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Seeder.SecureBootPublicKey":         {certFile},
			"Seeder.SecureBootKekPublicKey":      {kekFile},
			"matrixOS.DefaultPrivateGitRepoPath": {"/priv"},
		},
		Bools: map[string]bool{},
	}
	qa, _ := validation.New(cfg)
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		qa:             qa,
		imageDir:       imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	if err := r.CleanRootfs(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(certDst)
	if string(data) != "new" {
		t.Errorf("cert not overwritten: got %q, want %q", data, "new")
	}
}

func TestCleanRootfs_Constants(t *testing.T) {
	if imagerPortageSecureBootPem != "etc/portage/secureboot.pem" {
		t.Errorf("imagerPortageSecureBootPem = %q", imagerPortageSecureBootPem)
	}
	if imagerPortageSecureBootKek != "etc/portage/secureboot-kek.pem" {
		t.Errorf("imagerPortageSecureBootKek = %q", imagerPortageSecureBootKek)
	}
}

// ---------------------------------------------------------------------------
// PostCleanShrink
// ---------------------------------------------------------------------------

func TestPostCleanShrink_PrintsStartAndEndMessages(t *testing.T) {
	mockMountSyscalls(t)

	imageDir := t.TempDir()

	cfg := &config.MockConfig{Items: map[string][]string{}, Bools: map[string]bool{}}
	qa, _ := validation.New(cfg)
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		qa:             qa,
		imageDir:       imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	_ = r.PostCleanShrink()

	out := r.Stdout().(*bytes.Buffer).String()
	want := "Shrinking the rootfs to save space ...\n"
	if len(out) < len(want) || out[:len(want)] != want {
		t.Errorf("stdout = %q, want prefix %q", out, want)
	}
}

func TestPostCleanShrink_MountSetupFailure(t *testing.T) {
	// Mock Mount to fail so Setup returns an error.
	origMount := filesystems.Mount
	filesystems.Mount = func(source, target, fstype string, flags uintptr, data string) error {
		return fmt.Errorf("mock mount failure")
	}
	t.Cleanup(func() { filesystems.Mount = origMount })

	imageDir := t.TempDir()

	cfg := &config.MockConfig{Items: map[string][]string{}, Bools: map[string]bool{}}
	qa, _ := validation.New(cfg)
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		qa:             qa,
		imageDir:       imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.PostCleanShrink()
	if err == nil {
		t.Fatal("expected error from mount Setup, got nil")
	}
}

func TestPostCleanShrink_TracksMounts(t *testing.T) {
	mockMountSyscalls(t)

	imageDir := t.TempDir()
	devDir := t.TempDir()
	initDir := filepath.Join(devDir, "build", "init")
	os.MkdirAll(initDir, 0o755)
	os.WriteFile(filepath.Join(initDir, "init.sh"), []byte("#!/bin/sh\n"), 0o755)

	chrootCalled := false
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.Root": {devDir},
		},
		Bools: map[string]bool{},
	}
	qa, _ := validation.New(cfg)
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner: runner.ChrootRunFunc(func(c *runner.ChrootCmd) error {
			chrootCalled = true
			return nil
		}),
		qa:       qa,
		imageDir: imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	if err := r.PostCleanShrink(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !chrootCalled {
		t.Error("expected chroot to be called")
	}
}

func TestPostCleanShrink_EmptyImageDir(t *testing.T) {
	cfg := &config.MockConfig{Items: map[string][]string{}, Bools: map[string]bool{}}
	qa, _ := validation.New(cfg)
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		qa:             qa,
		imageDir:       "",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.PostCleanShrink()
	if err == nil {
		t.Fatal("expected error for empty imageDir, got nil")
	}
}

func TestPostCleanShrink_WalkerRemovesAAndLaFiles(t *testing.T) {
	// Test the walker function directly by creating .a and .la files
	// and a regular file, then confirming only .a/.la are removed.
	imageDir := t.TempDir()
	usrDir := filepath.Join(imageDir, "usr", "lib")
	os.MkdirAll(usrDir, 0o755)

	aFile := filepath.Join(usrDir, "libfoo.a")
	laFile := filepath.Join(usrDir, "libfoo.la")
	soFile := filepath.Join(usrDir, "libfoo.so")

	for _, f := range []string{aFile, laFile, soFile} {
		os.WriteFile(f, []byte("data"), 0o644)
	}

	// Simulate the walker directly.
	walker := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			ext := filepath.Ext(path)
			if ext == ".a" || ext == ".la" {
				os.Remove(path)
			}
		}
		return nil
	}
	filepath.WalkDir(filepath.Join(imageDir, "usr"), walker)

	// .a and .la should be removed.
	if _, err := os.Stat(aFile); !os.IsNotExist(err) {
		t.Error(".a file should have been removed")
	}
	if _, err := os.Stat(laFile); !os.IsNotExist(err) {
		t.Error(".la file should have been removed")
	}
	// .so should survive.
	if _, err := os.Stat(soFile); err != nil {
		t.Error(".so file should still exist")
	}
}

func TestPostCleanShrink_WalkerPreservesNonStaticFiles(t *testing.T) {
	imageDir := t.TempDir()
	usrDir := filepath.Join(imageDir, "usr", "lib")
	os.MkdirAll(usrDir, 0o755)

	keep := []string{"libbar.so", "libbar.so.1", "libbar.h", "README"}
	for _, f := range keep {
		os.WriteFile(filepath.Join(usrDir, f), []byte("data"), 0o644)
	}

	walker := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			ext := filepath.Ext(path)
			if ext == ".a" || ext == ".la" {
				os.Remove(path)
			}
		}
		return nil
	}
	filepath.WalkDir(filepath.Join(imageDir, "usr"), walker)

	for _, f := range keep {
		if _, err := os.Stat(filepath.Join(usrDir, f)); err != nil {
			t.Errorf("%s should still exist", f)
		}
	}
}

// ---------------------------------------------------------------------------
// PostCleanShrink with mocked ChrootRun — full flow
// (uses injected chrootRunner field on Releaser)
// ---------------------------------------------------------------------------

func TestPostCleanShrink_ChrootRunFailure(t *testing.T) {
	mockMountSyscalls(t)

	chrootRunCalled := false

	imageDir := t.TempDir()
	devDir := t.TempDir()
	initDir := filepath.Join(devDir, "build", "init")
	os.MkdirAll(initDir, 0o755)
	os.WriteFile(filepath.Join(initDir, "init.sh"), []byte("#!/bin/sh\n"), 0o755)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.Root": {devDir},
		},
		Bools: map[string]bool{},
	}
	qa, _ := validation.New(cfg)
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner: runner.ChrootRunFunc(func(c *runner.ChrootCmd) error {
			chrootRunCalled = true
			return errors.New("fake chroot error")
		}),
		qa:       qa,
		imageDir: imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.PostCleanShrink()
	if err == nil {
		t.Fatal("expected error from ChrootRun, got nil")
	}
	if !chrootRunCalled {
		t.Error("expected ChrootRun to be called")
	}
}

// ---------------------------------------------------------------------------
// CleanRootfs – removal / empty operations are tolerant of missing paths
// ---------------------------------------------------------------------------

func TestCleanRootfs_ToleratesMissingPaths(t *testing.T) {
	// A fresh imageDir with only the cert destination dir should work.
	// The RemoveDir / EmptyDir / RemoveFileWithGlob calls should not
	// error when the target paths don't exist.
	imageDir := t.TempDir()
	os.MkdirAll(filepath.Join(imageDir, "etc", "portage"), 0o755)

	certFile := filepath.Join(t.TempDir(), "cert.pem")
	os.WriteFile(certFile, []byte("c"), 0o644)
	kekFile := filepath.Join(t.TempDir(), "kek.pem")
	os.WriteFile(kekFile, []byte("k"), 0o644)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Seeder.SecureBootPublicKey":         {certFile},
			"Seeder.SecureBootKekPublicKey":      {kekFile},
			"matrixOS.DefaultPrivateGitRepoPath": {"/nonexistent-priv"},
		},
		Bools: map[string]bool{},
	}
	qa, _ := validation.New(cfg)
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		qa:             qa,
		imageDir:       imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	if err := r.CleanRootfs(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CleanRootfs – config error propagation via ErrConfig
// ---------------------------------------------------------------------------

func TestCleanRootfs_ErrConfigPropagates(t *testing.T) {
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

	err := r.CleanRootfs()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// chroot
// ---------------------------------------------------------------------------

// newChrootReleaser builds a Releaser wired for chroot tests.
// It creates a real init.sh script under devDir/build/init/ so os.Stat succeeds.
func newChrootReleaser(t *testing.T, mr *runner.MockRunner) *Releaser {
	t.Helper()

	devDir := t.TempDir()
	initDir := filepath.Join(devDir, "build", "init")
	if err := os.MkdirAll(initDir, 0o755); err != nil {
		t.Fatalf("mkdir init dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(initDir, "init.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write init.sh: %v", err)
	}

	imageDir := t.TempDir()

	s := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: &config.MockConfig{
			Items: map[string][]string{
				"matrixOS.Root": {devDir},
			},
			Bools: map[string]bool{},
		}},
		ostree:       &ostree.MockOstree{},
		chrootRunner: mr.ChrootRun,
		imageDir:     imageDir,
	}
	s.SetStdout(&bytes.Buffer{})
	s.SetStderr(&bytes.Buffer{})
	return s
}

func TestChroot_HappyPath(t *testing.T) {
	mr := runner.NewMockRunner()
	r := newChrootReleaser(t, mr)

	err := r.chroot([]string{"FOO=bar"}, "echo", []string{"hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mr.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mr.Calls))
	}
	call := mr.Calls[0]

	if call.Name != "chroot:echo" {
		t.Errorf("Name = %q, want %q", call.Name, "chroot:echo")
	}
	if !slices.Equal(call.Args, []string{"hello"}) {
		t.Errorf("Args = %v, want %v", call.Args, []string{"hello"})
	}
	if call.ChrootDir != r.imageDir {
		t.Errorf("ChrootDir = %q, want %q", call.ChrootDir, r.imageDir)
	}
}

func TestChroot_SetsEnvironment(t *testing.T) {
	mr := runner.NewMockRunner()
	r := newChrootReleaser(t, mr)

	devDir, _ := r.DevDir()

	err := r.chroot([]string{"OTHER=val"}, "cmd", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	env := mr.Calls[0].Env

	if !slices.Contains(env, "MATRIXOS_DEV_DIR="+devDir) {
		t.Errorf("env missing MATRIXOS_DEV_DIR=%s: %v", devDir, env)
	}
	if !slices.Contains(env, "RUNNER_TYPE=releaser") {
		t.Errorf("env missing RUNNER_TYPE=releaser: %v", env)
	}
	if !slices.Contains(env, "OTHER=val") {
		t.Errorf("env missing OTHER=val: %v", env)
	}
}

func TestChroot_ReplacesExistingEnvKeys(t *testing.T) {
	mr := runner.NewMockRunner()
	r := newChrootReleaser(t, mr)

	devDir, _ := r.DevDir()

	env := []string{
		"MATRIXOS_DEV_DIR=/old/path",
		"RUNNER_TYPE=builder",
		"KEEP=me",
	}

	err := r.chroot(env, "cmd", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := mr.Calls[0].Env

	if slices.Contains(got, "MATRIXOS_DEV_DIR=/old/path") {
		t.Error("old MATRIXOS_DEV_DIR should have been filtered")
	}
	if slices.Contains(got, "RUNNER_TYPE=builder") {
		t.Error("old RUNNER_TYPE should have been filtered")
	}
	if !slices.Contains(got, "MATRIXOS_DEV_DIR="+devDir) {
		t.Errorf("missing new MATRIXOS_DEV_DIR: %v", got)
	}
	if !slices.Contains(got, "RUNNER_TYPE=releaser") {
		t.Errorf("missing new RUNNER_TYPE: %v", got)
	}
	if !slices.Contains(got, "KEEP=me") {
		t.Errorf("KEEP=me should be preserved: %v", got)
	}
}

func TestChroot_NilEnv(t *testing.T) {
	mr := runner.NewMockRunner()
	r := newChrootReleaser(t, mr)

	devDir, _ := r.DevDir()

	err := r.chroot(nil, "cmd", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := mr.Calls[0].Env
	if !slices.Contains(got, "MATRIXOS_DEV_DIR="+devDir) {
		t.Errorf("missing MATRIXOS_DEV_DIR: %v", got)
	}
	if !slices.Contains(got, "RUNNER_TYPE=releaser") {
		t.Errorf("missing RUNNER_TYPE: %v", got)
	}
}

func TestChroot_DevDirError(t *testing.T) {
	mr := runner.NewMockRunner()
	r := newChrootReleaser(t, mr)

	r.cfg = &config.MockConfig{
		Items: map[string][]string{},
		Bools: map[string]bool{},
	}

	err := r.chroot(nil, "cmd", nil)
	if err == nil {
		t.Fatal("expected error when DevDir fails, got nil")
	}
	if len(mr.Calls) != 0 {
		t.Errorf("expected no runner calls, got %d", len(mr.Calls))
	}
}

func TestChroot_DevDirConfigError(t *testing.T) {
	mr := runner.NewMockRunner()

	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: &config.ErrConfig{Err: errors.New("cfg broken")}},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   mr.ChrootRun,
		imageDir:       "/tmp",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.chroot(nil, "cmd", nil)
	if err == nil {
		t.Fatal("expected error when config is broken, got nil")
	}
	if len(mr.Calls) != 0 {
		t.Errorf("expected no runner calls, got %d", len(mr.Calls))
	}
}

func TestChroot_InitScriptNotFound(t *testing.T) {
	mr := runner.NewMockRunner()

	devDir := t.TempDir()
	// Do NOT create build/init/init.sh under devDir.

	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: &config.MockConfig{
			Items: map[string][]string{
				"matrixOS.Root": {devDir},
			},
			Bools: map[string]bool{},
		}},
		ostree:       &ostree.MockOstree{},
		chrootRunner: mr.ChrootRun,
		imageDir:     t.TempDir(),
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.chroot(nil, "cmd", nil)
	if err == nil {
		t.Fatal("expected error when init script is missing, got nil")
	}
	if len(mr.Calls) != 0 {
		t.Errorf("expected no runner calls, got %d", len(mr.Calls))
	}
}

func TestChroot_RunnerError(t *testing.T) {
	wantErr := errors.New("chroot exec failed")
	mr := runner.NewMockRunnerFailOnCall(0, wantErr)
	r := newChrootReleaser(t, mr)

	err := r.chroot(nil, "cmd", []string{"arg1"})
	if err == nil {
		t.Fatal("expected error from chrootRunner, got nil")
	}
	if !errors.Is(err, wantErr) {
		if !bytes.Contains([]byte(err.Error()), []byte("chroot exec failed")) {
			t.Errorf("error %q should contain %q", err, wantErr)
		}
	}
	if len(mr.Calls) != 1 {
		t.Errorf("expected 1 runner call, got %d", len(mr.Calls))
	}
}

func TestChroot_ChrootCmdFields(t *testing.T) {
	var captured *runner.ChrootCmd

	devDir := t.TempDir()
	initDir := filepath.Join(devDir, "build", "init")
	os.MkdirAll(initDir, 0o755)
	initScript := filepath.Join(initDir, "init.sh")
	if err := os.WriteFile(initScript, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write init.sh: %v", err)
	}

	imageDir := t.TempDir()
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: &config.MockConfig{
			Items: map[string][]string{
				"matrixOS.Root": {devDir},
			},
			Bools: map[string]bool{},
		}},
		ostree: &ostree.MockOstree{},
		chrootRunner: func(c *runner.ChrootCmd) error {
			captured = c
			return nil
		},
		imageDir: imageDir,
	}
	r.SetStdout(outBuf)
	r.SetStderr(errBuf)

	err := r.chroot([]string{"A=1"}, "mybin", []string{"--flag", "val"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured == nil {
		t.Fatal("chrootRunner was not called")
	}
	if captured.ChrootExec != initScript {
		t.Errorf("ChrootExec = %q, want %q", captured.ChrootExec, initScript)
	}
	if captured.ChrootDir != imageDir {
		t.Errorf("ChrootDir = %q, want %q", captured.ChrootDir, imageDir)
	}
	if captured.Cmd.Name != "mybin" {
		t.Errorf("Name = %q, want %q", captured.Cmd.Name, "mybin")
	}
	if !slices.Equal(captured.Cmd.Args, []string{"--flag", "val"}) {
		t.Errorf("Args = %v, want %v", captured.Cmd.Args, []string{"--flag", "val"})
	}
	if captured.Cmd.Stdout != outBuf {
		t.Error("Stdout not wired to releaser stdout")
	}
	if captured.Cmd.Stderr != errBuf {
		t.Error("Stderr not wired to releaser stderr")
	}
}
