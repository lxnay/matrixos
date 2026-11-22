package releaser

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
	"matrixos/vector/lib/validation"
)

// ---------------------------------------------------------------------------
// parseServicesFile
// ---------------------------------------------------------------------------

func TestParseServicesFile_HappyPath(t *testing.T) {
	f := filepath.Join(t.TempDir(), "svc.conf")
	content := `# comment line
enable sshd.service NetworkManager.service
disable bluetooth.service
mask cups.service
preset-enable pipewire.service
preset-disable tracker.service
preset-mask avahi.service
set-default graphical.target

`
	os.WriteFile(f, []byte(content), 0o644)

	actions, err := parseServicesFile(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []serviceAction{
		{action: "enable", services: []string{"sshd.service", "NetworkManager.service"}},
		{action: "disable", services: []string{"bluetooth.service"}},
		{action: "mask", services: []string{"cups.service"}},
		{action: "preset-enable", services: []string{"pipewire.service"}},
		{action: "preset-disable", services: []string{"tracker.service"}},
		{action: "preset-mask", services: []string{"avahi.service"}},
		{action: "set-default", services: []string{"graphical.target"}},
	}

	if len(actions) != len(want) {
		t.Fatalf("got %d actions, want %d", len(actions), len(want))
	}
	for i, w := range want {
		a := actions[i]
		if a.action != w.action {
			t.Errorf("actions[%d].action = %q, want %q", i, a.action, w.action)
		}
		if len(a.services) != len(w.services) {
			t.Errorf("actions[%d].services len = %d, want %d", i, len(a.services), len(w.services))
			continue
		}
		for j := range w.services {
			if a.services[j] != w.services[j] {
				t.Errorf("actions[%d].services[%d] = %q, want %q", i, j, a.services[j], w.services[j])
			}
		}
	}
}

func TestParseServicesFile_SkipsBlanksAndComments(t *testing.T) {
	f := filepath.Join(t.TempDir(), "svc.conf")
	content := `
# full-line comment
   # indented comment

enable foo.service
`
	os.WriteFile(f, []byte(content), 0o644)

	actions, err := parseServicesFile(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	if actions[0].action != "enable" {
		t.Errorf("action = %q, want %q", actions[0].action, "enable")
	}
}

func TestParseServicesFile_SkipsSingleFieldLines(t *testing.T) {
	f := filepath.Join(t.TempDir(), "svc.conf")
	// A line with only an action keyword and no services should be skipped.
	content := "enable\n"
	os.WriteFile(f, []byte(content), 0o644)

	actions, err := parseServicesFile(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("got %d actions, want 0 (single-field line should be skipped)", len(actions))
	}
}

func TestParseServicesFile_NonexistentFile(t *testing.T) {
	_, err := parseServicesFile("/nonexistent/file.conf")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestParseServicesFile_EmptyFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "empty.conf")
	os.WriteFile(f, []byte(""), 0o644)

	actions, err := parseServicesFile(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("got %d actions, want 0", len(actions))
	}
}

func TestParseServicesFile_MultipleServicesPerLine(t *testing.T) {
	f := filepath.Join(t.TempDir(), "svc.conf")
	content := "enable a.service b.service c.service\n"
	os.WriteFile(f, []byte(content), 0o644)

	actions, err := parseServicesFile(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	if len(actions[0].services) != 3 {
		t.Errorf("got %d services, want 3", len(actions[0].services))
	}
}

func TestParseServicesFile_WhitespaceVariations(t *testing.T) {
	f := filepath.Join(t.TempDir(), "svc.conf")
	content := "  enable   foo.service   bar.service  \n"
	os.WriteFile(f, []byte(content), 0o644)

	actions, err := parseServicesFile(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	if actions[0].action != "enable" {
		t.Errorf("action = %q, want %q", actions[0].action, "enable")
	}
	if len(actions[0].services) != 2 {
		t.Errorf("got %d services, want 2", len(actions[0].services))
	}
}

// ---------------------------------------------------------------------------
// buildServicesScript
// ---------------------------------------------------------------------------

func TestBuildServicesScript_FullScript(t *testing.T) {
	script, err := buildServicesScript(buildServicesScriptOptions{
		enable:        []string{"sshd.service", "NetworkManager.service"},
		disable:       []string{"bluetooth.service"},
		mask:          []string{"cups.service"},
		presetEnable:  []string{"pipewire.socket"},
		presetDisable: []string{"tracker.service"},
		presetMask:    []string{"avahi.service"},
		defaultTarget: "graphical.target",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must start with shebang and set -e.
	if !strings.HasPrefix(script, "#!/bin/bash\nset -e\n") {
		t.Error("script missing shebang or set -e")
	}

	expectations := []string{
		"systemctl enable sshd.service NetworkManager.service",
		"systemctl disable bluetooth.service",
		"systemctl mask cups.service",
		"systemctl --global enable pipewire.socket",
		"systemctl --global disable tracker.service",
		"systemctl --global mask avahi.service",
		"systemctl set-default graphical.target",
	}
	for _, want := range expectations {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}
}

func TestBuildServicesScript_Empty(t *testing.T) {
	script, err := buildServicesScript(buildServicesScriptOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if script != "" {
		t.Errorf("expected empty script, got %q", script)
	}
}

func TestBuildServicesScript_InvalidServiceName(t *testing.T) {
	tests := []struct {
		name string
		svc  string
	}{
		{"semicolon", "sshd.service; rm -rf /"},
		{"backtick", "`evil`"},
		{"dollar", "${HOME}"},
		{"pipe", "sshd|cat"},
		{"space", "sshd .service"},
		{"slash", "../etc/passwd"},
		{"newline", "sshd\n.service"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildServicesScript(buildServicesScriptOptions{
				enable: []string{tt.svc},
			})
			if err == nil {
				t.Fatalf("expected error for %q, got nil", tt.svc)
			}
			if !strings.Contains(err.Error(), "invalid service") {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestBuildServicesScript_ValidSpecialNames(t *testing.T) {
	// Systemd allows @, dots, dashes, underscores, backslashes.
	valid := []string{
		"getty@tty1.service",
		"my_service.service",
		"my-service.timer",
		`dbus-org.freedesktop.NetworkManager.service`,
	}
	opts := buildServicesScriptOptions{
		enable: valid,
	}
	_, err := buildServicesScript(opts)
	if err != nil {
		t.Fatalf("unexpected error for valid names: %v", err)
	}
}

func TestBuildServicesScript_InvalidDefaultTarget(t *testing.T) {
	opts := buildServicesScriptOptions{
		defaultTarget: "graphical.target; echo pwned",
	}
	_, err := buildServicesScript(opts)
	if err == nil {
		t.Fatal("expected error for invalid default target, got nil")
	}
}

// ---------------------------------------------------------------------------
// SetupHostname
// ---------------------------------------------------------------------------

func TestSetupHostname_HappyPath(t *testing.T) {
	imageDir := t.TempDir()
	os.MkdirAll(filepath.Join(imageDir, "etc"), 0o755)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.Hostname": {"myhost"},
		},
		Bools: map[string]bool{},
	}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	if err := r.SetupHostname(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(imageDir, "etc/hostname"))
	if err != nil {
		t.Fatalf("failed to read hostname file: %v", err)
	}
	if string(data) != "myhost\n" {
		t.Errorf("hostname = %q, want %q", data, "myhost\n")
	}

	out := r.Stdout().(*bytes.Buffer).String()
	if out != "Setting hostname to: myhost\n" {
		t.Errorf("stdout = %q, want %q", out, "Setting hostname to: myhost\n")
	}
}

func TestSetupHostname_HostnameConfigError(t *testing.T) {
	// Missing Releaser.Hostname key → error.
	cfg := &config.MockConfig{Items: map[string][]string{}, Bools: map[string]bool{}}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(),
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	if err := r.SetupHostname(); err == nil {
		t.Fatal("expected error for missing Hostname config, got nil")
	}
}

func TestSetupHostname_ErrConfigPropagates(t *testing.T) {
	wantErr := errors.New("cfg broken")
	cfg := &config.ErrConfig{Err: wantErr}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(),
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.SetupHostname()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestSetupHostname_WriteFailsNoEtcDir(t *testing.T) {
	// imageDir has no etc/ → WriteFile fails.
	imageDir := t.TempDir()
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.Hostname": {"host"},
		},
		Bools: map[string]bool{},
	}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	if err := r.SetupHostname(); err == nil {
		t.Fatal("expected error when etc/ dir doesn't exist, got nil")
	}
}

func TestSetupHostname_OverwritesExistingFile(t *testing.T) {
	imageDir := t.TempDir()
	os.MkdirAll(filepath.Join(imageDir, "etc"), 0o755)
	os.WriteFile(filepath.Join(imageDir, "etc/hostname"), []byte("old\n"), 0o644)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.Hostname": {"newhost"},
		},
		Bools: map[string]bool{},
	}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       imageDir,
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	if err := r.SetupHostname(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(imageDir, "etc/hostname"))
	if string(data) != "newhost\n" {
		t.Errorf("hostname = %q, want %q", data, "newhost\n")
	}
}

// ---------------------------------------------------------------------------
// SetupServices
// ---------------------------------------------------------------------------

func TestSetupServices_HooksDirError(t *testing.T) {
	// Missing Releaser.HooksDir → error.
	cfg := &config.MockConfig{Items: map[string][]string{}, Bools: map[string]bool{}}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "origin/matrixos"},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(),
		ref:            "origin/matrixos",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	if err := r.SetupServices(); err == nil {
		t.Fatal("expected error for missing HooksDir config, got nil")
	}
}

func TestSetupServices_HappyPath(t *testing.T) {
	imageDir := t.TempDir()
	os.MkdirAll(filepath.Join(imageDir, "tmp"), 0o755)

	hooksDir := t.TempDir()
	svcFile := filepath.Join(hooksDir, "myref.conf")
	content := `enable sshd.service NetworkManager.service
disable bluetooth.service
mask cups.service
preset-enable pipewire.socket
set-default graphical.target
`
	os.WriteFile(svcFile, []byte(content), 0o644)

	devDir := t.TempDir()
	// The chroot method checks for build/init/init.sh.
	initDir := filepath.Join(devDir, "build", "init")
	os.MkdirAll(initDir, 0o755)
	os.WriteFile(filepath.Join(initDir, "init.sh"), nil, 0o755)

	var capturedArgs []string
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.HooksDir": {hooksDir},
			"matrixOS.Root":     {devDir},
		},
		Bools: map[string]bool{},
	}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "myref"},
		chrootRunner: runner.ChrootRunFunc(func(c *runner.ChrootCmd) error {
			capturedArgs = c.Args
			return nil
		}),
		imageDir: imageDir,
		ref:      "myref",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	if err := r.SetupServices(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The chroot should have been called with the script path.
	if len(capturedArgs) != 1 ||
		capturedArgs[0] != "/tmp/_matrixos_services.sh" {
		t.Errorf(
			"chroot args = %v, want [/tmp/_matrixos_services.sh]",
			capturedArgs,
		)
	}

	// The script file should have been cleaned up (defer os.Remove).
	scriptPath := filepath.Join(imageDir, "tmp", "_matrixos_services.sh")
	if _, err := os.Stat(scriptPath); err == nil {
		t.Error("expected script to be removed after execution")
	}

	out := r.Stdout().(*bytes.Buffer).String()
	if !strings.Contains(out, "Setting up services in a single chroot call") {
		t.Errorf("expected progress message, got: %s", out)
	}
}

func TestSetupServices_NoServicesFile_SkipsGracefully(t *testing.T) {
	hooksDir := t.TempDir()
	// No .conf file exists → should return error.
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.HooksDir": {hooksDir},
		},
		Bools: map[string]bool{},
	}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "myref"},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(),
		ref:            "myref",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.SetupServices()
	if err == nil {
		t.Fatal("expected error when services file does not exist")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSetupServices_FallbackToServicesSubdir(t *testing.T) {
	// Primary path doesn't exist, but the fallback (parent/services/ref.conf) does.
	baseDir := t.TempDir()
	hooksDir := filepath.Join(baseDir, "hooks")
	os.MkdirAll(hooksDir, 0o755)

	servicesDir := filepath.Join(baseDir, "services")
	os.MkdirAll(servicesDir, 0o755)
	// Create a minimal (empty) services file at the fallback location.
	svcFile := filepath.Join(servicesDir, "myref.conf")
	os.WriteFile(svcFile, []byte("# empty\n"), 0o644)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.HooksDir": {hooksDir},
		},
		Bools: map[string]bool{},
	}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "myref"},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(),
		ref:            "myref",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	// SetupServices will find the fallback file and parse it.
	// Since the file only contains comments, no chroot call is needed.
	err := r.SetupServices()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify that the specific "Create an empty" error message was NOT emitted
	// (only the initial "Trying to look harder" warning should appear).
	warn := r.Stderr().(*bytes.Buffer).String()
	if bytes.Contains([]byte(warn), []byte("Create an empty")) {
		t.Error("fallback path was not tried; got 'Create an empty' error")
	}
}

func TestSetupServices_ParseError(t *testing.T) {
	hooksDir := t.TempDir()
	// Write a line longer than bufio.MaxScanTokenSize (64 KiB) so the
	// scanner returns bufio.ErrTooLong. This triggers the parse-error
	// path regardless of uid (no permission tricks needed).
	svcFile := filepath.Join(hooksDir, "myref.conf")
	longLine := make([]byte, 64*1024+1)
	for i := range longLine {
		longLine[i] = 'x'
	}
	longLine[len(longLine)-1] = '\n'
	os.WriteFile(svcFile, longLine, 0o644)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.HooksDir": {hooksDir},
		},
		Bools: map[string]bool{},
	}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "myref"},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(),
		ref:            "myref",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.SetupServices()
	if err == nil {
		t.Fatal("expected parse error from oversized line, got nil")
	}
}

func TestSetupServices_ScriptWriteFailure(t *testing.T) {
	// imageDir has no tmp/ subdirectory → script write fails.
	hooksDir := t.TempDir()
	svcFile := filepath.Join(hooksDir, "myref.conf")
	os.WriteFile(svcFile, []byte("enable sshd.service\n"), 0o644)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.HooksDir": {hooksDir},
		},
		Bools: map[string]bool{},
	}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "myref"},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(), // no tmp/ inside
		ref:            "myref",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.SetupServices()
	if err == nil {
		t.Fatal("expected error from script write, got nil")
	}
	if !strings.Contains(err.Error(), "failed to write services script") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSetupServices_ErrConfigPropagates(t *testing.T) {
	wantErr := errors.New("cfg broken")
	cfg := &config.ErrConfig{Err: wantErr}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "ref"},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(),
		ref:            "ref",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.SetupServices()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

// ---------------------------------------------------------------------------
// ReleaseHook
// ---------------------------------------------------------------------------

func TestReleaseHook_HooksDirError(t *testing.T) {
	cfg := &config.MockConfig{Items: map[string][]string{}, Bools: map[string]bool{}}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "ref"},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(),
		ref:            "ref",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	if err := r.ReleaseHook(); err == nil {
		t.Fatal("expected error for missing HooksDir config, got nil")
	}
}

func TestReleaseHook_NoHookFile_SkipsGracefully(t *testing.T) {
	devDir := t.TempDir()
	hooksDir := filepath.Join(devDir, "release", "hooks")
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.HooksDir":                  {hooksDir},
			"matrixOS.Root":                      {devDir},
			"matrixOS.DefaultPrivateGitRepoPath": {"/priv"},
			"matrixOS.DefaultUsername":           {"user"},
			"matrixOS.OsName":                    {"matrixos"},
		},
		Bools: map[string]bool{},
	}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "myref"},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(),
		ref:            "myref",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.ReleaseHook()
	if err == nil {
		t.Fatal("expected error when hook file does not exist")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestReleaseHook_ExecutesHookScript(t *testing.T) {
	devDir := t.TempDir()
	hooksDir := filepath.Join(devDir, "release", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("failed to create hooks dir: %v", err)
	}
	imageDir := t.TempDir()
	hookFile := filepath.Join(hooksDir, "myref.sh")
	markerFile := filepath.Join(t.TempDir(), "marker")

	// Write a script that creates a marker file with $CHROOT_DIR content.
	script := "#!/bin/sh\necho \"$CHROOT_DIR\" > " + markerFile + "\n"
	os.WriteFile(hookFile, []byte(script), 0o755)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.HooksDir":                  {hooksDir},
			"matrixOS.Root":                      {devDir},
			"matrixOS.DefaultPrivateGitRepoPath": {"/priv"},
			"matrixOS.DefaultUsername":           {"user"},
			"matrixOS.OsName":                    {"matrixos"},
		},
		Bools: map[string]bool{},
	}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "myref"},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       imageDir,
		ref:            "myref",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	if err := r.ReleaseHook(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(markerFile)
	if err != nil {
		t.Fatalf("marker file not created: %v", err)
	}
	got := string(bytes.TrimSpace(data))
	if got != imageDir {
		t.Errorf("CHROOT_DIR = %q, want %q", got, imageDir)
	}

	out := r.Stdout().(*bytes.Buffer).String()
	want := "Running release hook " + hookFile + " ...\n"
	if out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestReleaseHook_HookScriptFails(t *testing.T) {
	devDir := t.TempDir()
	hooksDir := filepath.Join(devDir, "release", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("failed to create hooks dir: %v", err)
	}
	hookFile := filepath.Join(hooksDir, "myref.sh")
	os.WriteFile(hookFile, []byte("#!/bin/sh\nexit 1\n"), 0o755)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.HooksDir":                  {hooksDir},
			"matrixOS.Root":                      {devDir},
			"matrixOS.DefaultPrivateGitRepoPath": {"/priv"},
			"matrixOS.DefaultUsername":           {"user"},
			"matrixOS.OsName":                    {"matrixos"},
		},
		Bools: map[string]bool{},
	}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "myref"},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(),
		ref:            "myref",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.ReleaseHook()
	if err == nil {
		t.Fatal("expected error when hook exits non-zero, got nil")
	}
}

func TestReleaseHook_HookScriptStdout(t *testing.T) {
	devDir := t.TempDir()
	hooksDir := filepath.Join(devDir, "release", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("failed to create hooks dir: %v", err)
	}
	hookFile := filepath.Join(hooksDir, "myref.sh")
	os.WriteFile(hookFile, []byte("#!/bin/sh\necho hello\n"), 0o755)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.HooksDir":                  {hooksDir},
			"matrixOS.Root":                      {devDir},
			"matrixOS.DefaultPrivateGitRepoPath": {"/priv"},
			"matrixOS.DefaultUsername":           {"user"},
			"matrixOS.OsName":                    {"matrixos"},
		},
		Bools: map[string]bool{},
	}
	var stdout, stderr bytes.Buffer
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "myref"},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(),
		ref:            "myref",
	}
	r.SetStdout(&stdout)
	r.SetStderr(&stderr)

	if err := r.ReleaseHook(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	// Output should contain the initial print message AND "hello\n" from the script.
	if !bytes.Contains([]byte(out), []byte("hello\n")) {
		t.Errorf("stdout = %q, expected to contain %q", out, "hello\n")
	}
}

func TestReleaseHook_ErrConfigPropagates(t *testing.T) {
	wantErr := errors.New("cfg broken")
	cfg := &config.ErrConfig{Err: wantErr}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "ref"},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(),
		ref:            "ref",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	err := r.ReleaseHook()
	if !errors.Is(err, wantErr) {
		t.Errorf("got %v, want %v", err, wantErr)
	}
}

func TestReleaseHook_NonExecutableFile(t *testing.T) {
	devDir := t.TempDir()
	hooksDir := filepath.Join(devDir, "release", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("failed to create hooks dir: %v", err)
	}
	hookFile := filepath.Join(hooksDir, "myref.sh")
	// Write script without execute permission.
	os.WriteFile(hookFile, []byte("#!/bin/sh\necho hi\n"), 0o644)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.HooksDir":                  {hooksDir},
			"matrixOS.Root":                      {devDir},
			"matrixOS.DefaultPrivateGitRepoPath": {"/priv"},
			"matrixOS.DefaultUsername":           {"user"},
			"matrixOS.OsName":                    {"matrixos"},
		},
		Bools: map[string]bool{},
	}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: cfg},
		ostree:         &ostree.MockOstree{Ref_: "myref"},
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
		imageDir:       t.TempDir(),
		ref:            "myref",
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})

	// Even root cannot execute a file with zero execute bits (0o644).
	// CAP_DAC_OVERRIDE only grants execute when at least one x-bit is set.
	err := r.ReleaseHook()
	if err == nil {
		t.Fatal("expected permission error from exec, got nil")
	}
}

// ---------------------------------------------------------------------------
// serviceAction struct
// ---------------------------------------------------------------------------

func TestServiceAction_ZeroValue(t *testing.T) {
	var sa serviceAction
	if sa.action != "" {
		t.Errorf("expected empty action, got %q", sa.action)
	}
	if sa.services != nil {
		t.Errorf("expected nil services, got %v", sa.services)
	}
}

// ---------------------------------------------------------------------------
// SetupHostname + QA helper
// ---------------------------------------------------------------------------

func TestSetupHostname_WithQA(t *testing.T) {
	imageDir := t.TempDir()
	os.MkdirAll(filepath.Join(imageDir, "etc"), 0o755)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Releaser.Hostname": {"qahost"},
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

	if err := r.SetupHostname(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(imageDir, "etc/hostname"))
	if string(data) != "qahost\n" {
		t.Errorf("hostname = %q, want %q", data, "qahost\n")
	}
}
