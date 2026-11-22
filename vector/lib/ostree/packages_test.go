package ostree

import (
	"fmt"
	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommitAndListPackages(t *testing.T) {
	repoDir := setupTestRepo(t)

	// Create root first — we need its resolved path to lay out the commit content
	// so that the in-commit paths match what ListPackages looks up via
	// filepath.Join(root, RwVdbPath).
	root := t.TempDir()
	// Resolve symlinks (e.g. /tmp -> sysroot/tmp) so ostree can traverse the path.
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	// Create content to commit.
	// listPackagesFromPath runs: ostree ls -R commit -- <root>/var/db/pkg
	// so the commit tree must contain the package data at that absolute path.
	contentDir := t.TempDir()
	contentDir, err = filepath.EvalSymlinks(contentDir)
	if err != nil {
		t.Fatal(err)
	}

	// Strip the leading separator so filepath.Join works correctly.
	relRoot := strings.TrimPrefix(root, string(filepath.Separator))
	pkgDir := filepath.Join(contentDir, relRoot, RwVdbPath, "sys-apps", "systemd")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Create a file inside
	if err := os.WriteFile(filepath.Join(pkgDir, "CONTENTS"), []byte("foo"), 0644); err != nil {
		t.Fatal(err)
	}

	// Commit
	branch := "test/branch"
	cmd := exec.Command("ostree", "commit", "--repo="+repoDir, "--branch="+branch, "--subject=test", contentDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ostree commit failed: %v, output: %s", err, out)
	}

	// Setup Ostree struct
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.ReadOnlyVdb": {RwVdbPath},
			"Ostree.Root":          {root},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	// Get Commit Hash
	commit, err := o.lastCommitFromRepo(repoDir, branch)
	if err != nil {
		t.Fatalf("lastCommitFromRepo failed: %v", err)
	}
	if commit == "" {
		t.Fatal("commit hash is empty")
	}

	rootRepo := filepath.Join(root, "ostree", "repo")
	if err := os.MkdirAll(filepath.Dir(rootRepo), 0755); err != nil {
		t.Fatal(err)
	}
	// Symlink the repo we created to the sysroot location
	if err := os.Symlink(repoDir, rootRepo); err != nil {
		t.Fatal(err)
	}
	// Also create the vdb dir in sysroot as ListPackages checks for existence
	if err := os.MkdirAll(filepath.Join(root, RwVdbPath), 0755); err != nil {
		t.Fatal(err)
	}

	pkgs, err := o.ListPackages(commit)
	if err != nil {
		t.Fatalf("ListPackages failed: %v", err)
	}

	if len(pkgs) != 1 {
		t.Errorf("expected 1 package, got %d", len(pkgs))
	} else if pkgs[0] != "sys-apps/systemd" {
		t.Errorf("expected sys-apps/systemd, got %s", pkgs[0])
	}
}

func TestListPackagesErrors(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{}, // Missing ReadOnlyVdb
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}
	if _, err := o.ListPackages("commit"); err == nil {
		t.Error("ListPackages should fail if ReadOnlyVdb is missing")
	}

	cfg = &config.MockConfig{
		Items: map[string][]string{
			"Releaser.ReadOnlyVdb": {RwVdbPath},
		},
	}
	o, _ = NewOstree(NewOstreeOptions{Config: cfg})
	// Sysroot does not exist
	if _, err := o.ListPackages("commit"); err == nil {
		t.Error("ListPackages should fail if sysroot/var/db/pkg does not exist")
	}
}

func TestListPackagesMocked(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.ReadOnlyVdb": {RwVdbPath},
			"Ostree.Root":          {"/"},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		stdout := cmd.Stdout
		// Mock ls -R output
		output := `d00755 0 0 0 abc abc /
d00755 0 0 0 abc abc /var/db/pkg/cat/pkg
-00644 0 0 0 abc /var/db/pkg/cat/pkg/CONTENTS
d00755 0 0 0 abc abc /var/db/pkg/cat/other
`
		stdout.Write([]byte(output))
		return nil
	}

	// We need directoryExists to return true for sysroot/var/db/pkg
	sysroot := t.TempDir()
	os.MkdirAll(filepath.Join(sysroot, RwVdbPath), 0755)

	pkgs, err := o.ListPackages("commit")
	if err != nil {
		t.Fatalf("ListPackages failed: %v", err)
	}
	if len(pkgs) != 2 {
		t.Errorf("Expected 2 packages, got %d", len(pkgs))
	}
	if pkgs[0] != "cat/other" || pkgs[1] != "cat/pkg" {
		t.Errorf("Unexpected packages: %v", pkgs)
	}
}

func TestListContents(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Ostree.RepoDir": {"/repo"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		// Simulate `ostree ls -C -R` output with directories, files, and a symlink.
		mockOutput := `d00755 0 0 0 aaa111 bbb222 /etc
-00644 0 0 42 ccc333 /etc/hostname
l00777 0 0 0 ddd444 /etc/localtime -> /usr/share/zoneinfo/UTC
d00755 0 0 0 eee555 fff666 /etc/conf.d
-00644 0 0 100 ggg777 /etc/conf.d/net
`
		o.runner = func(cmd *runner.Cmd) error {
			stdout := cmd.Stdout
			stdout.Write([]byte(mockOutput))
			return nil
		}

		pis, err := o.ListContents("abc123", "/etc")
		if err != nil {
			t.Fatalf("ListContents failed: %v", err)
		}
		if pis == nil {
			t.Fatal("ListContents returned nil")
		}
		if len(*pis) != 5 {
			t.Fatalf("expected 5 entries, got %d", len(*pis))
		}

		// Verify directory entry
		d := (*pis)[0]
		if d.Mode.Type != "d" {
			t.Errorf("entry[0] type = %q, want %q", d.Mode.Type, "d")
		}
		if d.Path != "/etc" {
			t.Errorf("entry[0] path = %q, want %q", d.Path, "/etc")
		}

		// Verify regular file entry
		f := (*pis)[1]
		if f.Mode.Type != "-" {
			t.Errorf("entry[1] type = %q, want %q", f.Mode.Type, "-")
		}
		if f.Path != "/etc/hostname" {
			t.Errorf("entry[1] path = %q, want %q", f.Path, "/etc/hostname")
		}
		if f.Size != 42 {
			t.Errorf("entry[1] size = %d, want 42", f.Size)
		}
		if f.OSTreeChecksum != "ccc333" {
			t.Errorf("entry[1] checksum = %q, want %q", f.OSTreeChecksum, "ccc333")
		}

		// Verify symlink entry
		l := (*pis)[2]
		if l.Mode.Type != "l" {
			t.Errorf("entry[2] type = %q, want %q", l.Mode.Type, "l")
		}
		if l.Path != "/etc/localtime" {
			t.Errorf("entry[2] path = %q, want %q", l.Path, "/etc/localtime")
		}
		if l.Link != "/usr/share/zoneinfo/UTC" {
			t.Errorf("entry[2] link = %q, want %q", l.Link, "/usr/share/zoneinfo/UTC")
		}
	})

	t.Run("EmptyCommit", func(t *testing.T) {
		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Ostree.RepoDir": {"/repo"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		_, err = o.ListContents("", "/etc")
		if err == nil {
			t.Error("expected error for empty commit, got nil")
		}
	})

	t.Run("EmptyPath", func(t *testing.T) {
		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Ostree.RepoDir": {"/repo"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		_, err = o.ListContents("abc123", "")
		if err == nil {
			t.Error("expected error for empty path, got nil")
		}
	})

	t.Run("MissingRepoDir", func(t *testing.T) {
		cfg := &config.MockConfig{
			Items: map[string][]string{},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		_, err = o.ListContents("abc123", "/etc")
		if err == nil {
			t.Error("expected error for missing RepoDir, got nil")
		}
	})

	t.Run("CommandError", func(t *testing.T) {
		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Ostree.RepoDir": {"/repo"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		o.runner = func(cmd *runner.Cmd) error {
			return fmt.Errorf("ostree ls failed")
		}

		_, err = o.ListContents("abc123", "/etc")
		if err == nil {
			t.Error("expected error when command fails, got nil")
		}
	})

	t.Run("EmptyOutput", func(t *testing.T) {
		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Ostree.RepoDir": {"/repo"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		o.runner = func(cmd *runner.Cmd) error {
			// Write nothing
			return nil
		}

		pis, err := o.ListContents("abc123", "/etc")
		if err != nil {
			t.Fatalf("ListContents failed: %v", err)
		}
		if pis == nil || len(*pis) != 0 {
			t.Errorf("expected empty result, got %v", pis)
		}
	})

	t.Run("VerifiesCommandArgs", func(t *testing.T) {
		var capturedArgs []string
		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Ostree.RepoDir": {"/my/repo"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		o.runner = func(cmd *runner.Cmd) error {
			args, name := cmd.Args, cmd.Name
			capturedArgs = append([]string{name}, args...)
			return nil
		}

		_, err = o.ListContents("commitABC", "/usr/bin")
		if err != nil {
			t.Fatalf("ListContents failed: %v", err)
		}

		// Expected: ostree --repo=/my/repo ls -C -R commitABC -- /usr/bin
		foundRepo := false
		foundLs := false
		foundCommit := false
		foundPath := false
		foundDashDash := false
		for _, arg := range capturedArgs {
			switch arg {
			case "--repo=/my/repo":
				foundRepo = true
			case "ls":
				foundLs = true
			case "commitABC":
				foundCommit = true
			case "/usr/bin":
				foundPath = true
			case "--":
				foundDashDash = true
			}
		}
		if !foundRepo {
			t.Errorf("missing --repo arg in %v", capturedArgs)
		}
		if !foundLs {
			t.Errorf("missing ls arg in %v", capturedArgs)
		}
		if !foundCommit {
			t.Errorf("missing commit arg in %v", capturedArgs)
		}
		if !foundPath {
			t.Errorf("missing path arg in %v", capturedArgs)
		}
		if !foundDashDash {
			t.Errorf("missing -- separator in %v", capturedArgs)
		}
	})

	t.Run("MalformedLine", func(t *testing.T) {
		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Ostree.RepoDir": {"/repo"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		o.runner = func(cmd *runner.Cmd) error {
			stdout := cmd.Stdout
			stdout.Write([]byte("this is not valid ostree ls output\n"))
			return nil
		}

		_, err = o.ListContents("abc123", "/etc")
		if err == nil {
			t.Error("expected error for malformed output, got nil")
		}
	})
}
