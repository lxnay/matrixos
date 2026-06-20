package ostree

import (
	"fmt"
	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoOperations(t *testing.T) {
	repoDir := setupTestRepo(t)

	// Test AddRemote
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir":   {repoDir},
			"Ostree.Remote":    {"origin"},
			"Ostree.RemoteUrl": {"http://example.com"},
		},
		Bools: map[string]bool{
			"Ostree.Gpg": false,
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	// Test ListRemotes (empty)
	remotes, err := o.ListRemotes()
	if err != nil {
		t.Fatalf("ListRemotes failed: %v", err)
	}
	if len(remotes) != 0 {
		t.Errorf("expected 0 remotes, got %d", len(remotes))
	}

	err = o.AddRemote()
	if err != nil {
		t.Fatalf("AddRemote failed: %v", err)
	}

	// Test ListRemotes (1)
	remotes, err = o.ListRemotes()
	if err != nil {
		t.Fatalf("ListRemotes failed: %v", err)
	}
	if len(remotes) != 1 || remotes[0] != "origin" {
		t.Errorf("expected [origin], got %v", remotes)
	}

	// Test LocalRefs (empty)
	refs, err := o.LocalRefs()
	if err != nil {
		t.Fatalf("LocalRefs failed: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("expected 0 refs, got %d", len(refs))
	}
}

func TestOstreeCommandsMocked(t *testing.T) {
	var lastCmdArgs []string

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir":                {"/repo"},
			"Ostree.Root":                   {"/"},
			"Ostree.Remote":                 {"origin"},
			"Ostree.KeepObjectsYoungerThan": {"2023-01-01"},
		},
		Bools: map[string]bool{
			"Ostree.Gpg": false,
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg, Ref: "ref"})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		args, stdout := cmd.Args, cmd.Stdout
		lastCmdArgs = args
		// Mock rev-parse for GenerateStaticDelta
		if len(args) > 0 && args[0] == "rev-parse" {
			stdout.Write([]byte("commit-hash\n"))
		}
		return nil
	}

	// Pull
	if err := o.Pull(); err != nil {
		t.Fatalf("Pull failed: %v", err)
	}
	if lastCmdArgs[1] != "pull" || lastCmdArgs[2] != "origin" || lastCmdArgs[3] != "ref" {
		t.Errorf("Pull args mismatch: %v", lastCmdArgs)
	}

	// Prune
	if err := o.Prune(); err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	// args: --repo=/repo prune --depth=5 --refs-only --keep-younger-than=... --only-branch=ref
	if lastCmdArgs[1] != "prune" || lastCmdArgs[5] != "--only-branch=ref" {
		t.Errorf("Prune args mismatch: %v", lastCmdArgs)
	}

	// GenerateStaticDelta
	if err := o.GenerateStaticDelta(); err != nil {
		t.Fatalf("GenerateStaticDelta failed: %v", err)
	}
	// First it calls rev-parse, then static-delta generate
	// Since we only capture last, we check static-delta
	if lastCmdArgs[1] != "static-delta" || lastCmdArgs[2] != "generate" {
		t.Errorf("GenerateStaticDelta args mismatch: %v", lastCmdArgs)
	}

	// UpdateSummary
	if err := o.UpdateSummary(); err != nil {
		t.Fatalf("UpdateSummary failed: %v", err)
	}
	if lastCmdArgs[1] != "summary" || lastCmdArgs[2] != "--update" {
		t.Errorf("UpdateSummary args mismatch: %v", lastCmdArgs)
	}

	// Upgrade
	if err := o.Upgrade([]string{"--check"}); err != nil {
		t.Fatalf("Upgrade failed: %v", err)
	}
	if lastCmdArgs[1] != "upgrade" || lastCmdArgs[3] != "--check" {
		t.Errorf("Upgrade args mismatch: %v", lastCmdArgs)
	}
}

func TestMaybeInitializeRemote(t *testing.T) {
	var cmds []string
	repoDir := t.TempDir()
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir":   {repoDir},
			"Ostree.Remote":    {"origin"},
			"Ostree.RemoteUrl": {"http://url"},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		args := cmd.Args
		cmds = append(cmds, strings.Join(args, " "))
		return nil
	}

	if err := o.MaybeInitializeRemote(); err != nil {
		t.Fatalf("MaybeInitializeRemote failed: %v", err)
	}

	// Check for expected commands
	// 1. init (since repoDir is empty)
	// 2. remote add (since list returns empty in mock)
	if len(cmds) < 2 {
		t.Errorf("Expected at least 2 commands, got %d: %v", len(cmds), cmds)
	}
}

func TestAddRemoteToRootfs(t *testing.T) {
	var lastArgs []string
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Remote":    {"origin"},
			"Ostree.RemoteUrl": {"http://url"},
		},
		Bools: map[string]bool{"Ostree.Gpg": false},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		args := cmd.Args
		lastArgs = args
		return nil
	}

	if err := o.AddRemoteToRootfs("/sysroot"); err != nil {
		t.Fatalf("AddRemoteToRootfs failed: %v", err)
	}

	// Expected: remote add --sysroot=/sysroot --force --no-gpg-verify origin http://url
	foundSysroot := false
	for _, arg := range lastArgs {
		if arg == "--sysroot=/sysroot" {
			foundSysroot = true
			break
		}
	}
	if !foundSysroot {
		t.Errorf("AddRemoteToRootfs args missing sysroot: %v", lastArgs)
	}
}

func TestPullInvalidRef(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir": {"/repo"},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}
	if err := o.Pull(); err == nil {
		t.Error("Pull should fail for empty ref")
	}
}

func TestMaybeInitializeRemoteIdempotency(t *testing.T) {
	var cmds []string
	repoDir := t.TempDir()
	// Create objects dir to simulate existing repo
	os.MkdirAll(filepath.Join(repoDir, "objects"), 0755)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir":   {repoDir},
			"Ostree.Remote":    {"origin"},
			"Ostree.RemoteUrl": {"http://url"},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		args, stdout := cmd.Args, cmd.Stdout
		cmds = append(cmds, strings.Join(args, " "))
		// Mock ListRemotes output
		// args: --repo=... remote list
		for i, arg := range args {
			if arg == "remote" && i+1 < len(args) && args[i+1] == "list" {
				fmt.Fprintln(stdout, "origin")
				return nil
			}
		}
		return nil
	}

	if err := o.MaybeInitializeRemote(); err != nil {
		t.Fatalf("MaybeInitializeRemote failed: %v", err)
	}

	// Should NOT see "init" or "remote add"
	for _, cmd := range cmds {
		if strings.Contains(cmd, "init") {
			t.Error("Should not have initialized repo")
		}
		if strings.Contains(cmd, "remote add") {
			t.Error("Should not have added remote")
		}
	}
}

func TestAddRemote_Error(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir": {"/repo"},
			"Ostree.Remote":  {"origin"},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		return fmt.Errorf("error")
	}
	if err := o.AddRemote(); err == nil {
		t.Error("AddRemote should fail on error")
	}
}

func TestOstreeWrappers(t *testing.T) {
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
		return nil
	}

	if _, err := o.ListRemotes(); err != nil {
		t.Error(err)
	}
	if _, err := o.LocalRefs(); err != nil {
		t.Error(err)
	}
}

func TestLastCommit_Errors(t *testing.T) {
	origRunCommand := runCommand
	defer func() { runCommand = origRunCommand }()

	runCommand = func(cmd *runner.Cmd) error {
		return fmt.Errorf("not found")
	}

	// Test standalone LastCommit if exposed or wrapper
	if _, err := LastCommit("/repo", "ref", false); err == nil {
		t.Error("LastCommit should fail if cmd fails")
	}
}

func TestMiscWrappers_Errors(t *testing.T) {
	cfg := &config.MockConfig{Items: map[string][]string{"Ostree.RepoDir": {"/repo"}}}
	o, err := NewOstree(NewOstreeOptions{Config: cfg, Ref: "ref"})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		return fmt.Errorf("cmd error")
	}

	if err := o.Pull(); err == nil {
		t.Error("Pull should fail on cmd error")
	}
	if err := o.Prune(); err == nil {
		t.Error("Prune should fail on cmd error")
	}
	if err := o.UpdateSummary(); err == nil {
		t.Error("UpdateSummary should fail on cmd error")
	}
	if err := o.GenerateStaticDelta(); err == nil {
		t.Error("GenerateStaticDelta should fail on cmd error")
	}
	if err := o.Upgrade(nil); err == nil {
		t.Error("Upgrade should fail on cmd error")
	}
}

func TestRemoteRefs(t *testing.T) {
	origRunCommand := runCommand
	defer func() { runCommand = origRunCommand }()

	t.Run("Success", func(t *testing.T) {
		root := "/myroot"
		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Ostree.RepoDir": {filepath.Join(root, "ostree/repo")},
				"Ostree.Remote":  {"origin"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		o.runner = func(cmd *runner.Cmd) error {
			stdout := cmd.Stdout
			stdout.Write([]byte("matrixos/amd64/gnome\nmatrixos/amd64/server\nmatrixos/amd64/dev/gnome\n"))
			return nil
		}

		refs, err := o.RemoteRefs()
		if err != nil {
			t.Fatalf("RemoteRefs failed: %v", err)
		}
		if len(refs) != 3 {
			t.Fatalf("expected 3 refs, got %d", len(refs))
		}
		if refs[0] != "matrixos/amd64/gnome" {
			t.Errorf("refs[0] = %q, want %q", refs[0], "matrixos/amd64/gnome")
		}
		if refs[1] != "matrixos/amd64/server" {
			t.Errorf("refs[1] = %q, want %q", refs[1], "matrixos/amd64/server")
		}
		if refs[2] != "matrixos/amd64/dev/gnome" {
			t.Errorf("refs[2] = %q, want %q", refs[2], "matrixos/amd64/dev/gnome")
		}
	})

	t.Run("VerifiesRepoPathAndRemote", func(t *testing.T) {
		var capturedArgs []string
		runCommand = func(cmd *runner.Cmd) error {
			args, name, stdout := cmd.Args, cmd.Name, cmd.Stdout
			capturedArgs = append([]string{name}, args...)
			stdout.Write([]byte("ref1\n"))
			return nil
		}

		root := "/custom/root"
		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Ostree.RepoDir": {filepath.Join(root, "ostree/repo")},
				"Ostree.Remote":  {"myremote"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		_, err = o.RemoteRefs()
		if err != nil {
			t.Fatalf("RemoteRefs failed: %v", err)
		}

		expectedRepoArg := "--repo=/custom/root/ostree/repo"
		foundRepo := false
		foundRemote := false
		for _, arg := range capturedArgs {
			if arg == expectedRepoArg {
				foundRepo = true
			}
			if arg == "myremote" {
				foundRemote = true
			}
		}
		if !foundRepo {
			t.Errorf("expected repo arg %q in command args %v", expectedRepoArg, capturedArgs)
		}
		if !foundRemote {
			t.Errorf("expected remote %q in command args %v", "myremote", capturedArgs)
		}
	})

	t.Run("EmptyRepoDir", func(t *testing.T) {
		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Ostree.Remote": {"origin"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		_, err = o.RemoteRefs()
		if err == nil {
			t.Error("expected error for empty repoDir, got nil")
		}
	})

	t.Run("EmptyRemote", func(t *testing.T) {
		root := "/custom/root"
		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Ostree.RepoDir": {filepath.Join(root, "ostree/repo")},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		_, err = o.RemoteRefs()
		if err == nil {
			t.Error("expected error for empty remote, got nil")
		}
	})

	t.Run("NoRefs", func(t *testing.T) {
		runCommand = func(cmd *runner.Cmd) error {
			return nil
		}

		root := t.TempDir()
		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Ostree.RepoDir": {filepath.Join(root, "ostree/repo")},
				"Ostree.Remote":  {"origin"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		refs, err := o.RemoteRefs()
		if err != nil {
			t.Fatalf("RemoteRefs failed: %v", err)
		}
		if len(refs) != 0 {
			t.Errorf("expected 0 refs, got %d", len(refs))
		}
	})

	t.Run("CommandError", func(t *testing.T) {
		runCommand = func(cmd *runner.Cmd) error {
			return fmt.Errorf("remote refs failed")
		}

		root := t.TempDir()
		cfg := &config.MockConfig{
			Items: map[string][]string{
				"Ostree.RepoDir": {filepath.Join(root, "ostree/repo")},
				"Ostree.Remote":  {"origin"},
			},
		}
		o, err := NewOstree(NewOstreeOptions{Config: cfg})
		if err != nil {
			t.Fatalf("NewOstree failed: %v", err)
		}

		_, err = o.RemoteRefs()
		if err == nil {
			t.Error("expected error when ostree command fails, got nil")
		}
	})
}

func TestLocalRefs(t *testing.T) {
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
		stdout.Write([]byte("ref1\nostree-metadata\nref2\n"))
		return nil
	}

	refs, err := o.LocalRefs()
	if err != nil {
		t.Fatalf("LocalRefs failed: %v", err)
	}

	for _, ref := range refs {
		if ref == "ostree-metadata" {
			t.Errorf("LocalRefs() should not return 'ostree-metadata'")
		}
	}

	if len(refs) != 2 {
		t.Errorf("expected 2 refs, got %d", len(refs))
	}

	if refs[0] != "ref1" {
		t.Errorf("refs[0] = %q, want %q", refs[0], "ref1")
	}
	if refs[1] != "ref2" {
		t.Errorf("refs[1] = %q, want %q", refs[1], "ref2")
	}
}

func TestInitRepo_Integration(t *testing.T) {
	checkOstreeAvailable(t)

	dir := t.TempDir()
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir": {dir},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	if err := o.InitRepo(); err != nil {
		t.Fatalf("InitRepo failed: %v", err)
	}

	// Verify the repo config exists and contains lock-timeout-secs=-1.
	data, err := os.ReadFile(filepath.Join(dir, "config"))
	if err != nil {
		t.Fatalf("reading ostree config: %v", err)
	}
	if !strings.Contains(string(data), "lock-timeout-secs=-1") {
		t.Errorf("expected lock-timeout-secs=-1 in repo config, got:\n%s", data)
	}
}
