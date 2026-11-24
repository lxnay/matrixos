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

func TestDeploy(t *testing.T) {
	var commands [][]string
	fakeCommit := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	sysroot := t.TempDir()
	repoDir := "/fake/repo"
	ref := "matrixos/dev/gnome"
	bootArgs := []string{"arg1=val1", "arg2=val2"}

	// Setup config
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir":  {repoDir},
			"Ostree.Sysroot":  {sysroot},
			"Ostree.Remote":   {"origin"},
			"matrixOS.OsName": {"matrixos"},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg, Ref: ref})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		args, name, stdout := cmd.Args, cmd.Name, cmd.Stdout
		cmdArgs := append([]string{name}, args...)
		commands = append(commands, cmdArgs)

		if len(args) > 0 {
			if args[0] == "rev-parse" {
				stdout.Write([]byte(fakeCommit + "\n"))
			}
		}
		return nil
	}

	gotSysroot, err := o.Sysroot()
	if err != nil {
		t.Fatalf("Sysroot failed: %v", err)
	}

	// Call Deploy
	err = o.Deploy(gotSysroot, bootArgs)
	if err != nil {
		t.Fatalf("Deploy failed: %v", err)
	}

	// Verify commands
	expectedCommands := []string{
		fmt.Sprintf("ostree rev-parse --repo=%s %s", repoDir, ref),
		fmt.Sprintf("ostree admin init-fs %s", sysroot),
		fmt.Sprintf("ostree admin os-init matrixos --sysroot=%s", sysroot),
		fmt.Sprintf("ostree --repo=%s/ostree/repo pull-local %s %s", sysroot, repoDir, fakeCommit),
		fmt.Sprintf("ostree refs --repo=%s/ostree/repo --create=origin:%s %s", sysroot, ref, fakeCommit),
		fmt.Sprintf("ostree config --repo=%s/ostree/repo set sysroot.bootloader none", sysroot),
		fmt.Sprintf("ostree config --repo=%s/ostree/repo set sysroot.bootprefix false", sysroot),
		fmt.Sprintf("ostree admin deploy --sysroot=%s --os=matrixos --karg-append=arg1=val1 --karg-append=arg2=val2 origin:%s", sysroot, ref),
	}

	if len(commands) != len(expectedCommands) {
		t.Errorf("Expected %d commands, got %d", len(expectedCommands), len(commands))
	}

	for i, cmd := range commands {
		if i >= len(expectedCommands) {
			break
		}
		cmdStr := strings.Join(cmd, " ")
		if cmdStr != expectedCommands[i] {
			t.Errorf("Command %d mismatch:\nGot:  %s\nWant: %s", i, cmdStr, expectedCommands[i])
		}
	}
}

func TestDeployIntegration(t *testing.T) {
	checkOstreeAvailable(t)
	if os.Getuid() != 0 {
		t.Skip("Skipping Deploy integration test: requires root privileges")
	}

	// Ensure we are using the real runCommand (in case other tests mocked it)
	// Since tests run sequentially in this package, this is just a safety measure.
	// Note: runCommand is a global variable in ostree.go

	repoDir := setupTestRepo(t)

	// Create content to commit
	contentDir := t.TempDir()
	// Create a minimal rootfs structure
	if err := os.MkdirAll(filepath.Join(contentDir, "usr", "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	// ostree admin deploy requires /usr/etc to exist in the commit
	if err := os.MkdirAll(filepath.Join(contentDir, "usr", "etc"), 0755); err != nil {
		t.Fatal(err)
	}
	// Create a dummy kernel in /usr/lib/modules/KVER/vmlinuz
	// ostree admin deploy expects the kernel to be in /usr/lib/modules or /boot (with specific naming)
	kernelVer := "6.6.6-test"
	modulesDir := filepath.Join(contentDir, "usr", "lib", "modules", kernelVer)
	if err := os.MkdirAll(modulesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modulesDir, "vmlinuz"), []byte("kernel"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modulesDir, "initramfs.img"), []byte("initramfs"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "usr", "lib", "os-release"), []byte("NAME=TestOS\nID=testos\nVERSION=1.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../lib/os-release", filepath.Join(contentDir, "usr/etc", "os-release")); err != nil {
		t.Fatal(err)
	}

	branch := "test/os"
	// Commit to the repo
	cmd := exec.Command("ostree", "commit", "--repo="+repoDir, "--branch="+branch, "--subject=test", contentDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ostree commit failed: %v, output: %s", err, out)
	}

	// Setup config for the Deploy call
	sysroot := t.TempDir()
	// ostree admin deploy sets immutable attributes on the deployment directory.
	// We need to clear them to allow t.TempDir cleanup to succeed.
	defer func() {
		exec.Command("chattr", "-R", "-i", sysroot).Run()
	}()
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir":  {repoDir},
			"Ostree.Sysroot":  {sysroot},
			"Ostree.Remote":   {"origin"},
			"matrixOS.OsName": {"matrixos"},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg, Ref: branch})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	gotSysroot, err := o.Sysroot()
	if err != nil {
		t.Fatalf("Sysroot failed: %v", err)
	}

	// Perform Deployment
	// This will pull from repoDir into sysroot/ostree/repo and then deploy
	if err := o.Deploy(gotSysroot, []string{"karg1=val1"}); err != nil {
		t.Fatalf("Deploy failed: %v", err)
	}

	// Verify that the deployment directory was created
	// We can verify the booted ref or just check if the deployment directory exists
	if _, err := o.DeployedRootfs(); err != nil {
		t.Errorf("DeployedRootfs failed or deployment not found: %v", err)
	}
}

func TestBootedStatus(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Root": {"/"},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		stdout := cmd.Stdout
		// Mock ostree admin status --json
		jsonOutput := `{
			"deployments": [
				{
					"booted": true,
					"checksum": "hash123",
					"refspec": "origin:branch"
				},
				{
					"booted": false,
					"checksum": "hash456",
					"refspec": "origin:old"
				}
			]
		}`
		stdout.Write([]byte(jsonOutput))
		return nil
	}

	ref, err := o.BootedRef()
	if err != nil {
		t.Fatalf("BootedRef failed: %v", err)
	}
	if ref != "origin:branch" {
		t.Errorf("BootedRef = %q, want origin:branch", ref)
	}
}

func TestBootCommit(t *testing.T) {
	sysroot := t.TempDir()
	osName := "matrixos"

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.OsName": {osName},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	// Setup directory structure: sysroot/ostree/boot.1/matrixos/COMMIT_HASH
	bootDir := filepath.Join(sysroot, "ostree", "boot.1", osName)
	if err := os.MkdirAll(bootDir, 0755); err != nil {
		t.Fatal(err)
	}

	commitHash := "a1b2c3d4"
	if err := os.Mkdir(filepath.Join(bootDir, commitHash), 0755); err != nil {
		t.Fatal(err)
	}

	got, err := o.BootCommit(sysroot)
	if err != nil {
		t.Fatalf("BootCommit failed: %v", err)
	}
	if got != commitHash {
		t.Errorf("BootCommit = %q, want %q", got, commitHash)
	}
}

func TestDeploy_Errors(t *testing.T) {
	origRunCommand := runCommand
	defer func() { runCommand = origRunCommand }()

	// Trigger error at specific steps
	tests := []struct {
		name      string
		failAtCmd string
		wantErr   bool
	}{
		{"rev-parse fail", "rev-parse", true},
		{"init-fs fail", "init-fs", true},
		{"os-init fail", "os-init", true},
		{"pull-local fail", "pull-local", true},
		{"refs create fail", "refs", true},
		{"bootloader config fail", "bootloader", true},
		{"deploy fail", "admin deploy", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCommand = func(cmd *runner.Cmd) error {
				args, stdout := cmd.Args, cmd.Stdout
				cmdStr := strings.Join(args, " ")
				if strings.Contains(cmdStr, tt.failAtCmd) {
					return fmt.Errorf("simulated error")
				}
				// Mock essential returns
				if len(args) > 0 && args[0] == "rev-parse" {
					stdout.Write([]byte("hash\n"))
				}
				return nil
			}

			cfg := &config.MockConfig{
				Items: map[string][]string{
					"Ostree.RepoDir":  {"/repo"},
					"Ostree.Sysroot":  {"/sysroot"},
					"Ostree.Remote":   {"origin"},
					"matrixOS.OsName": {"matrixos"},
				},
			}
			o, err := NewOstree(NewOstreeOptions{Config: cfg, Ref: "ref"})
			if err != nil {
				t.Fatalf("NewOstree failed: %v", err)
			}

			sysroot, err := o.Sysroot()
			if err != nil {
				t.Fatalf("Sysroot failed: %v", err)
			}

			err = o.Deploy(sysroot, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("Deploy() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestBootedStatus_Errors(t *testing.T) {
	tests := []struct {
		name       string
		jsonOutput string
		mockErr    error
		wantRefErr bool
	}{
		{
			name:       "cmd failed",
			mockErr:    fmt.Errorf("cmd failed"),
			wantRefErr: true,
		},
		{
			name:       "invalid json",
			jsonOutput: "{ invalid json",
			wantRefErr: true,
		},
		{
			name:       "no booted deployment",
			jsonOutput: `{"deployments": [{"booted": false}]}`,
			wantRefErr: true,
		},
	}

	cfg := &config.MockConfig{Items: map[string][]string{"Ostree.Root": {"/"}}}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o.runner = func(cmd *runner.Cmd) error {
				stdout := cmd.Stdout
				if tt.mockErr != nil {
					return tt.mockErr
				}
				stdout.Write([]byte(tt.jsonOutput))
				return nil
			}

			_, err := o.BootedRef()
			if (err != nil) != tt.wantRefErr {
				t.Errorf("BootedRef() error = %v, wantErr %v", err, tt.wantRefErr)
			}
		})
	}
}

func TestListDeployments(t *testing.T) {
	origRunCommand := runCommand
	defer func() { runCommand = origRunCommand }()

	fakeJSON := `{
		"deployments": [
			{
				"checksum": "abc123",
				"stateroot": "matrixos",
				"refspec": "origin:matrixos/amd64/gnome",
				"booted": true,
				"pending": false,
				"rollback": false,
				"staged": false,
				"index": 0,
				"serial": 1
			},
			{
				"checksum": "def456",
				"stateroot": "matrixos",
				"refspec": "origin:matrixos/amd64/server",
				"booted": false,
				"pending": false,
				"rollback": true,
				"staged": false,
				"index": 1,
				"serial": 0
			}
		]
	}`

	runCommand = func(cmd *runner.Cmd) error {
		stdout := cmd.Stdout
		// Expect ostree admin status --json
		stdout.Write([]byte(fakeJSON))
		return nil
	}

	root := t.TempDir()
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Root": {root},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	deployments, err := o.ListDeployments()
	if err != nil {
		t.Fatalf("ListDeployments failed: %v", err)
	}

	if len(deployments) != 2 {
		t.Fatalf("expected 2 deployments, got %d", len(deployments))
	}

	// Verify first deployment (booted)
	d0 := deployments[0]
	if d0.Checksum != "abc123" {
		t.Errorf("deployment[0].Checksum = %q, want %q", d0.Checksum, "abc123")
	}
	if d0.Stateroot != "matrixos" {
		t.Errorf("deployment[0].Stateroot = %q, want %q", d0.Stateroot, "matrixos")
	}
	if d0.Refspec != "origin:matrixos/amd64/gnome" {
		t.Errorf("deployment[0].Refspec = %q, want %q", d0.Refspec, "origin:matrixos/amd64/gnome")
	}
	if !d0.Booted {
		t.Error("deployment[0].Booted should be true")
	}
	if d0.Rollback {
		t.Error("deployment[0].Rollback should be false")
	}
	if d0.Index != 0 {
		t.Errorf("deployment[0].Index = %d, want 0", d0.Index)
	}
	if d0.Serial != 1 {
		t.Errorf("deployment[0].Serial = %d, want 1", d0.Serial)
	}

	// Verify second deployment (rollback)
	d1 := deployments[1]
	if d1.Checksum != "def456" {
		t.Errorf("deployment[1].Checksum = %q, want %q", d1.Checksum, "def456")
	}
	if d1.Booted {
		t.Error("deployment[1].Booted should be false")
	}
	if !d1.Rollback {
		t.Error("deployment[1].Rollback should be true")
	}
	if d1.Index != 1 {
		t.Errorf("deployment[1].Index = %d, want 1", d1.Index)
	}
}

func TestListDeployments_EmptyRoot(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	_, err = o.ListDeployments()
	if err == nil {
		t.Error("expected error for empty root, got nil")
	}
}

func TestListDeployments_NoDeployments(t *testing.T) {
	root := t.TempDir()
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Root": {root},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		stdout := cmd.Stdout
		stdout.Write([]byte(`{"deployments": []}`))
		return nil
	}

	deployments, err := o.ListDeployments()
	if err != nil {
		t.Fatalf("ListDeployments failed: %v", err)
	}
	if len(deployments) != 0 {
		t.Errorf("expected 0 deployments, got %d", len(deployments))
	}
}

func TestListDeployments_CommandError(t *testing.T) {
	root := t.TempDir()
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Root": {root},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		return fmt.Errorf("ostree command failed")
	}

	_, err = o.ListDeployments()
	if err == nil {
		t.Error("expected error when ostree command fails, got nil")
	}
}

func TestListDeployments_InvalidJSON(t *testing.T) {
	root := t.TempDir()
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Root": {root},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		stdout := cmd.Stdout
		stdout.Write([]byte(`{not valid json}`))
		return nil
	}

	_, err = o.ListDeployments()
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestSwitch(t *testing.T) {
	var lastCmdArgs []string
	sysroot := t.TempDir()
	ref := "origin:matrixos/amd64/gnome"

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Root": {sysroot},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg, Ref: ref})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		args, name := cmd.Args, cmd.Name
		lastCmdArgs = append([]string{name}, args...)
		return nil
	}

	err = o.Switch()
	if err != nil {
		t.Fatalf("Switch failed: %v", err)
	}

	expectedCmd := fmt.Sprintf("ostree admin switch --sysroot=%s %s", sysroot, ref)
	gotCmd := strings.Join(lastCmdArgs, " ")
	if gotCmd != expectedCmd {
		t.Errorf("Command mismatch:\nGot:  %s\nWant: %s", gotCmd, expectedCmd)
	}
}

func TestSwitch_MissingSysroot(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg, Ref: "ref"})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		return nil
	}

	err = o.Switch()
	if err == nil {
		t.Fatal("Switch should fail when Ostree.Sysroot is missing")
	}
}

func TestSwitch_CommandError(t *testing.T) {
	sysroot := t.TempDir()
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Root": {sysroot},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg, Ref: "ref"})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		return fmt.Errorf("ostree admin switch failed")
	}

	err = o.Switch()
	if err == nil {
		t.Fatal("Switch should propagate command error")
	}
}

func TestSwitch_Verbose(t *testing.T) {
	var lastCmdArgs []string
	sysroot := t.TempDir()
	ref := "matrixos/amd64/gnome"

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Root": {sysroot},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg, Ref: ref})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		args, name := cmd.Args, cmd.Name
		lastCmdArgs = append([]string{name}, args...)
		return nil
	}

	o.SetVerbose(true)
	err = o.Switch()
	if err != nil {
		t.Fatalf("Switch failed: %v", err)
	}

	expectedCmd := fmt.Sprintf("ostree --verbose admin switch --sysroot=%s %s", sysroot, ref)
	gotCmd := strings.Join(lastCmdArgs, " ")
	if gotCmd != expectedCmd {
		t.Errorf("Command mismatch:\nGot:  %s\nWant: %s", gotCmd, expectedCmd)
	}
}

func TestPostCopy(t *testing.T) {
	var lastCmdArgs []string
	sysroot := t.TempDir()

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Sysroot": {sysroot},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		args, name := cmd.Args, cmd.Name
		lastCmdArgs = append([]string{name}, args...)
		return nil
	}

	err = o.PostCopy()
	if err != nil {
		t.Fatalf("PostCopy failed: %v", err)
	}

	expectedCmd := fmt.Sprintf("ostree admin post-copy --sysroot=%s", sysroot)
	gotCmd := strings.Join(lastCmdArgs, " ")
	if gotCmd != expectedCmd {
		t.Errorf("Command mismatch:\nGot:  %s\nWant: %s", gotCmd, expectedCmd)
	}
}

func TestPostCopy_MissingSysroot(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		return nil
	}

	err = o.PostCopy()
	if err == nil {
		t.Fatal("PostCopy should fail when Ostree.Sysroot is missing")
	}
}

func TestPostCopy_CommandError(t *testing.T) {
	sysroot := t.TempDir()
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Sysroot": {sysroot},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		return fmt.Errorf("ostree admin post-copy failed")
	}

	err = o.PostCopy()
	if err == nil {
		t.Fatal("PostCopy should propagate command error")
	}
}

func TestPostCopy_Verbose(t *testing.T) {
	var lastCmdArgs []string
	sysroot := t.TempDir()

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Sysroot": {sysroot},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		args, name := cmd.Args, cmd.Name
		lastCmdArgs = append([]string{name}, args...)
		return nil
	}

	o.SetVerbose(true)
	err = o.PostCopy()
	if err != nil {
		t.Fatalf("PostCopy failed: %v", err)
	}

	expectedCmd := fmt.Sprintf("ostree --verbose admin post-copy --sysroot=%s", sysroot)
	gotCmd := strings.Join(lastCmdArgs, " ")
	if gotCmd != expectedCmd {
		t.Errorf("Command mismatch:\nGot:  %s\nWant: %s", gotCmd, expectedCmd)
	}
}
