package ostree

import (
	"matrixos/vector/lib/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertSymlink(t *testing.T, path, target string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Errorf("stat %s failed: %v", path, err)
		return
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("%s is not a symlink", path)
		return
	}
	got, err := os.Readlink(path)
	if err != nil {
		t.Errorf("readlink %s failed: %v", path, err)
		return
	}
	if got != target {
		t.Errorf("symlink %s -> %s, want %s", path, got, target)
	}
}

func assertDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Errorf("stat %s failed: %v", path, err)
		return
	}
	if !info.IsDir() {
		t.Errorf("%s is not a directory", path)
	}
}

func setupMinimalHierarchy(t *testing.T, imageDir string) {
	t.Helper()
	dirs := []string{"tmp", "etc", "var/db/pkg", "opt", "srv", "usr/local"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(imageDir, d), 0755); err != nil {
			t.Fatalf("failed to create dir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(imageDir, "etc", "machine-id"), []byte("id"), 0644); err != nil {
		t.Fatalf("failed to write machine-id: %v", err)
	}
}

func TestPrepareFilesystemHierarchy(t *testing.T) {
	imageDir := t.TempDir()

	// Create initial directories that are expected to exist or be moved
	dirs := []string{
		"tmp",
		"etc",
		"var/db/pkg",
		"opt",
		"srv",
		"home",
		"usr/local",
		"root",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(imageDir, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Create machine-id
	if err := os.WriteFile(filepath.Join(imageDir, "etc", "machine-id"), []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.ReadOnlyVdb": {"/usr/var/db/pkg"}, // Different to test move
			"Imager.EfiRoot":       {"/efi"},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	if err := o.PrepareFilesystemHierarchy(imageDir); err != nil {
		t.Fatalf("PrepareFilesystemHierarchy failed: %v", err)
	}

	// Verifications
	assertSymlink(t, filepath.Join(imageDir, "ostree"), "sysroot/ostree")

	// /tmp must be an empty real directory with mode 1777 (sticky).
	if tmpInfo, err := os.Lstat(filepath.Join(imageDir, "tmp")); err != nil {
		t.Errorf("stat tmp failed: %v", err)
	} else {
		if tmpInfo.Mode()&os.ModeSymlink != 0 {
			t.Error("tmp should not be a symlink")
		}
		if !tmpInfo.IsDir() {
			t.Error("tmp should be a directory")
		}
		if tmpInfo.Mode().Perm() != 0o777 || tmpInfo.Mode()&os.ModeSticky == 0 {
			t.Errorf("tmp mode = %04o, want 1777", tmpInfo.Mode())
		}
	}

	assertDir(t, filepath.Join(imageDir, "usr", "etc"))
	// Note: PrepareFilesystemHierarchy moves etc -> usr/etc but does NOT create the symlink back.
	// That is handled by a separate function in the bash script (release_lib.symlink_etc).
	if _, err := os.Stat(filepath.Join(imageDir, "etc")); !os.IsNotExist(err) {
		t.Error("etc directory should have been moved")
	}

	// Check var/db/pkg move
	// Config was /usr/var/db/pkg
	assertDir(t, filepath.Join(imageDir, "usr", "var", "db", "pkg"))
	assertSymlink(t, filepath.Join(imageDir, "var", "db", "pkg"), "../../usr/var/db/pkg")

	// Check opt
	assertDir(t, filepath.Join(imageDir, "usr", "opt"))
	assertSymlink(t, filepath.Join(imageDir, "opt"), "usr/opt")

	// Check srv
	assertDir(t, filepath.Join(imageDir, "var", "srv"))
	assertSymlink(t, filepath.Join(imageDir, "srv"), "var/srv")

	// Check home
	assertDir(t, filepath.Join(imageDir, "var", "home"))
	assertSymlink(t, filepath.Join(imageDir, "home"), "var/home")
	// Check root
	assertDir(t, filepath.Join(imageDir, "var", "roothome"))
	assertSymlink(t, filepath.Join(imageDir, "root"), "var/roothome")

	// Check usr/local
	assertDir(t, filepath.Join(imageDir, "var", "usrlocal"))
	assertSymlink(t, filepath.Join(imageDir, "usr", "local"), "../var/usrlocal")
}

func TestPrepareFilesystemHierarchySafety(t *testing.T) {
	imageDir := t.TempDir()
	// Setup initial state
	dirs := []string{"tmp", "etc", "var/db/pkg", "opt", "srv", "home", "usr/local"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(imageDir, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(imageDir, "etc", "machine-id"), []byte("id"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.ReadOnlyVdb": {"/usr/var-db-pkg"},
			"Imager.EfiRoot":       {"/efi"},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	// First run
	if err := o.PrepareFilesystemHierarchy(imageDir); err != nil {
		t.Fatalf("First run failed: %v", err)
	}

	// Second run (Safety check)
	err = o.PrepareFilesystemHierarchy(imageDir)
	if err == nil {
		t.Fatal("Second run should have failed due to marker file")
	} else if !strings.Contains(err.Error(), "already prepared") {
		t.Errorf("Unexpected error message: %v", err)
	}
}

func TestPrepareFilesystemHierarchyEdgeCases(t *testing.T) {
	// Case: Home is a directory
	t.Run("HomeDir", func(t *testing.T) {
		imageDir := t.TempDir()
		setupMinimalHierarchy(t, imageDir)
		os.Mkdir(filepath.Join(imageDir, "home"), 0755)

		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Releaser.ReadOnlyVdb": {"/usr/var-db-pkg"},
				"Imager.EfiRoot":       {"/efi"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}
		if err := o.PrepareFilesystemHierarchy(imageDir); err != nil {
			t.Fatalf("PrepareFilesystemHierarchy failed: %v", err)
		}
		// Check if home is now a symlink
		assertSymlink(t, filepath.Join(imageDir, "home"), "var/home")
		// Check if var/home exists
		assertDir(t, filepath.Join(imageDir, "var", "home"))
	})

	// Case: Home is invalid symlink
	t.Run("HomeInvalidSymlink", func(t *testing.T) {
		imageDir := t.TempDir()
		setupMinimalHierarchy(t, imageDir)
		os.MkdirAll(filepath.Join(imageDir, "var", "home"), 0755)
		os.Symlink("/invalid", filepath.Join(imageDir, "home"))

		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Releaser.ReadOnlyVdb": {RwVdbPath},
				"Imager.EfiRoot":       {"/efi"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}
		if err := o.PrepareFilesystemHierarchy(imageDir); err == nil {
			t.Error("Expected error for invalid home symlink")
		}
	})
}

func TestValidateFilesystemHierarchy(t *testing.T) {
	tempDir := t.TempDir()

	cfg := &config.MockConfig{}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	// Sub-test for missing directory
	t.Run("MissingDirectories", func(t *testing.T) {
		err := o.ValidateFilesystemHierarchy(tempDir)
		if err == nil {
			t.Error("expected error for missing directories, got nil")
		}
	})

	// Sub-test for correct hierarchy
	t.Run("ValidHierarchy", func(t *testing.T) {
		// Clean the tempDir for this subtest
		entries, _ := os.ReadDir(tempDir)
		for _, entry := range entries {
			os.RemoveAll(filepath.Join(tempDir, entry.Name()))
		}

		dirs := []string{"/etc", "/home", "/opt", "/root", "/srv", "/usr/local"}
		for _, d := range dirs {
			linkPath := filepath.Join(tempDir, d)
			if d == "/usr/local" {
				os.MkdirAll(filepath.Join(tempDir, "usr"), 0755)
			}

			// Just create some dummy targets
			dummyTarget := filepath.Join(tempDir, "dummy_"+strings.ReplaceAll(d, "/", "_"))
			os.MkdirAll(dummyTarget, 0755)

			if err := os.Symlink(dummyTarget, linkPath); err != nil {
				t.Fatalf("failed to create symlink %s: %v", linkPath, err)
			}
		}

		// /tmp must be a real sticky directory, not a symlink.
		tmpPath := filepath.Join(tempDir, "tmp")
		if err := os.Mkdir(tmpPath, os.ModeSticky|0o777); err != nil {
			t.Fatalf("failed to create tmp: %v", err)
		}
		if err := os.Chmod(tmpPath, os.ModeSticky|0o777); err != nil {
			t.Fatalf("failed to chmod tmp: %v", err)
		}

		err := o.ValidateFilesystemHierarchy(tempDir)
		if err != nil {
			t.Errorf("expected nil error for valid hierarchy, got %v", err)
		}
	})

	// Sub-test for regular directory instead of symlink
	t.Run("DirectoryInsteadOfSymlink", func(t *testing.T) {
		// Clean the tempDir for this subtest
		entries, _ := os.ReadDir(tempDir)
		for _, entry := range entries {
			os.RemoveAll(filepath.Join(tempDir, entry.Name()))
		}

		dirs := []string{"/etc", "/home", "/opt", "/root", "/srv", "/tmp", "/usr/local"}
		for _, d := range dirs {
			linkPath := filepath.Join(tempDir, d)
			if d == "/usr/local" {
				os.MkdirAll(filepath.Join(tempDir, "usr"), 0755)
			}
			os.MkdirAll(linkPath, 0755)
		}

		err := o.ValidateFilesystemHierarchy(tempDir)
		if err == nil {
			t.Error("expected error when directories are not symlinks, got nil")
		}
	})
}
