package commands

import (
	"matrixos/vector/lib/config"
	"strings"
	"testing"
)

// This file adds coverage for constructors, Name(), and Init() methods
// across all commands. Tests that need config loading to fail use
// t.Chdir() to move to a temp dir where no config files exist.

// noConfigDir moves to a temp directory so config loading fails.
// It also disables the system search path so that a host-installed
// config at /etc/matrixos/conf is not picked up.
func noConfigDir(t *testing.T) {
	t.Helper()
	t.Chdir(t.TempDir())
	config.SystemSearchPathEnabled = false
	t.Cleanup(func() { config.SystemSearchPathEnabled = true })
}

// --- BranchCommand ---

func TestBranchNewAndName(t *testing.T) {
	cmd := NewBranchCommand()
	if cmd == nil {
		t.Fatal("NewBranchCommand returned nil")
	}
	if got := cmd.Name(); got != "branch" {
		t.Errorf("Name() = %q, want %q", got, "branch")
	}
}

func TestBranchInitNoSubcommand(t *testing.T) {
	cmd := NewBranchCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init with no subcommand")
	}
	if !strings.Contains(err.Error(), "no subcommand provided") {
		t.Errorf("error = %q, want 'no subcommand provided'", err.Error())
	}
}

func TestBranchInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	cmd := NewBranchCommand()
	err := cmd.Init([]string{"show"})
	if err == nil {
		t.Fatal("expected error from Init (config not available)")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

// --- CfgCommand ---

func TestCfgNewAndName(t *testing.T) {
	cmd := NewCfgCommand()
	if cmd == nil {
		t.Fatal("NewCfgCommand returned nil")
	}
	if got := cmd.Name(); got != "cfg" {
		t.Errorf("Name() = %q, want %q", got, "cfg")
	}
}

func TestCfgInitNoSubcommand(t *testing.T) {
	cmd := NewCfgCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init with no subcommand")
	}
	if !strings.Contains(err.Error(), "no subcommand provided") {
		t.Errorf("error = %q, want 'no subcommand provided'", err.Error())
	}
}

func TestCfgInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	cmd := NewCfgCommand()
	err := cmd.Init([]string{"get", "matrixOS.Root"})
	if err == nil {
		t.Fatal("expected error from Init (config not available)")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

func TestCfgRunUnknownSubcommand(t *testing.T) {
	cmd := NewCfgCommand()
	cmd.sub = "nonexistent"
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand: nonexistent") {
		t.Errorf("error = %q, want 'unknown subcommand: nonexistent'", err.Error())
	}
}

// --- CheckCommand ---

func TestCheckInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	cmd := NewCheckCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init (config not available)")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

// --- EnterCommand ---

func TestEnterInitNoArgs(t *testing.T) {
	orig := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = orig }()

	cmd := NewEnterCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init with no args")
	}
	if !strings.Contains(err.Error(), "no chroot dirs or names specified") {
		t.Errorf("error = %q, want 'no chroot dirs or names specified'", err.Error())
	}
}

func TestEnterInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	orig := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = orig }()

	cmd := NewEnterCommand()
	err := cmd.Init([]string{"/tmp/test-chroot"})
	if err == nil {
		t.Fatal("expected error from Init (config not available)")
	}
	if !strings.Contains(err.Error(), "error reading config") {
		t.Errorf("error = %q, want 'error reading config'", err.Error())
	}
}

// --- FlashCommand ---

func TestFlashInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	orig := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = orig }()

	cmd := NewFlashCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

// --- ImageCommand ---

func TestImageInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	orig := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = orig }()

	cmd := NewImageCommand()
	err := cmd.Init([]string{"-ref", "test/ref"})
	if err == nil {
		t.Fatal("expected error from Init")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

// --- ImagesCommand ---

func TestImagesInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	orig := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = orig }()

	cmd := NewImagesCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

// --- JailbreakCommand ---

func TestJailbreakNewAndName(t *testing.T) {
	cmd := NewJailbreakCommand()
	if cmd == nil {
		t.Fatal("NewJailbreakCommand returned nil")
	}
	if got := cmd.Name(); got != "jailbreak" {
		t.Errorf("Name() = %q, want %q", got, "jailbreak")
	}
}

func TestJailbreakInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	cmd := NewJailbreakCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

// --- JanitorCommand ---

func TestJanitorInitParsesArgs(t *testing.T) {
	cmd := NewJanitorCommand()
	err := cmd.Init([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- KargsCommand ---

func TestKargsNewAndName(t *testing.T) {
	cmd := NewKargsCommand()
	if cmd == nil {
		t.Fatal("NewKargsCommand returned nil")
	}
	if got := cmd.Name(); got != "kargs" {
		t.Errorf("Name() = %q, want %q", got, "kargs")
	}
}

func TestKargsInitNoSubcommand(t *testing.T) {
	cmd := NewKargsCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init with no subcommand")
	}
	if !strings.Contains(err.Error(), "no subcommand provided") {
		t.Errorf("error = %q, want 'no subcommand provided'", err.Error())
	}
}

func TestKargsInitSucceedsWithoutConfig(t *testing.T) {
	noConfigDir(t)
	cmd := NewKargsCommand()
	if err := cmd.Init([]string{"add", "quiet"}); err != nil {
		t.Fatalf("Init should not require config: %v", err)
	}
}

// --- ReadWriteCommand ---

func TestReadWriteInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	cmd := NewReadWriteCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

// --- ReleaseCommand ---

func TestReleaseInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	orig := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = orig }()

	cmd := NewReleaseCommand()
	err := cmd.Init([]string{"-ref", "test/ref", "-chroot-dir", "/tmp", "-image-dir", "/tmp"})
	if err == nil {
		t.Fatal("expected error from Init")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

// --- ReleasesCommand ---

func TestReleasesInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	orig := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = orig }()

	cmd := NewReleasesCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

// --- SeedsCommand ---

func TestSeedsInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	orig := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = orig }()

	cmd := NewSeedsCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

// --- SetupOSCommand ---

func TestSetupOSNewAndName(t *testing.T) {
	cmd := NewSetupOSCommand()
	if cmd == nil {
		t.Fatal("NewSetupOSCommand returned nil")
	}
	if got := cmd.Name(); got != "setupOS" {
		t.Errorf("Name() = %q, want %q", got, "setupOS")
	}
}

func TestSetupOSInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	cmd := NewSetupOSCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

// --- UpgradeCommand ---

func TestUpgradeNewAndName(t *testing.T) {
	cmd := NewUpgradeCommand()
	if cmd == nil {
		t.Fatal("NewUpgradeCommand returned nil")
	}
	if got := cmd.Name(); got != "upgrade" {
		t.Errorf("Name() = %q, want %q", got, "upgrade")
	}
}

func TestUpgradeInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	cmd := NewUpgradeCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

// --- VMCommand ---

func TestVMInitConfigFailure(t *testing.T) {
	noConfigDir(t)
	cmd := NewVMCommand()
	err := cmd.Init([]string{})
	if err == nil {
		t.Fatal("expected error from Init")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

// --- base.go helpers ---

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"  ", nil},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b , c ", []string{"a", "b", "c"}},
		{"single", []string{"single"}},
		{"a,,b", []string{"a", "b"}},
	}
	for _, tt := range tests {
		got := SplitCSV(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("SplitCSV(%q) len = %d, want %d", tt.input, len(got), len(tt.want))
			continue
		}
		for i := range tt.want {
			if got[i] != tt.want[i] {
				t.Errorf("SplitCSV(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestMakeSkipFilter(t *testing.T) {
	if f := makeSkipFilter(nil); f != nil {
		t.Error("expected nil filter for empty skip list")
	}

	f := makeSkipFilter([]string{"a", "b"})
	if f == nil {
		t.Fatal("expected non-nil filter")
	}
	if !f("a") {
		t.Error("filter should match 'a'")
	}
	if !f("b") {
		t.Error("filter should match 'b'")
	}
	if f("c") {
		t.Error("filter should not match 'c'")
	}
}

func TestMakeOnlyFilter(t *testing.T) {
	if f := makeOnlyFilter(nil); f != nil {
		t.Error("expected nil filter for empty only list")
	}

	f := makeOnlyFilter([]string{"x", "y"})
	if f == nil {
		t.Fatal("expected non-nil filter")
	}
	if !f("x") {
		t.Error("filter should match 'x'")
	}
	if f("z") {
		t.Error("filter should not match 'z'")
	}
}

func TestInitBaseConfigFailsWithoutFile(t *testing.T) {
	noConfigDir(t)
	cmd := &BaseCommand{}
	err := cmd.initBaseConfig()
	if err == nil {
		t.Fatal("expected error from initBaseConfig without config file")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

func TestInitClientConfigFailsWithoutFile(t *testing.T) {
	noConfigDir(t)
	cmd := &BaseCommand{}
	err := cmd.initClientConfig()
	if err == nil {
		t.Fatal("expected error from initClientConfig without config file")
	}
	if !strings.Contains(err.Error(), "failed to load config") {
		t.Errorf("error = %q, want 'failed to load config'", err.Error())
	}
}

func TestInitOstreeNoConfig(t *testing.T) {
	cmd := &BaseCommand{}
	err := cmd.initOstree()
	if err == nil {
		t.Fatal("expected error from initOstree without config")
	}
	if !strings.Contains(err.Error(), "config not initialized") {
		t.Errorf("error = %q, want 'config not initialized'", err.Error())
	}
}

// --- flushWriter / flushWriterInline helpers ---

type mockFlusher struct{ flushed bool }

func (m *mockFlusher) Write(p []byte) (int, error) { return len(p), nil }
func (m *mockFlusher) Flush()                      { m.flushed = true }

type mockInlineFlusher struct {
	mockFlusher
	inlineFlushed bool
}

func (m *mockInlineFlusher) FlushInline() { m.inlineFlushed = true }

func TestFlushWriter(t *testing.T) {
	m := &mockFlusher{}
	flushWriter(m)
	if !m.flushed {
		t.Error("expected Flush to be called")
	}
}

func TestFlushWriterNonFlusher(t *testing.T) {
	var buf strings.Builder
	flushWriter(&buf)
}

func TestFlushWriterInline(t *testing.T) {
	m := &mockInlineFlusher{}
	flushWriterInline(m)
	if !m.inlineFlushed {
		t.Error("expected FlushInline to be called")
	}
}

func TestFlushWriterInlineFallback(t *testing.T) {
	m := &mockFlusher{}
	flushWriterInline(m)
	if !m.flushed {
		t.Error("expected fallback Flush to be called")
	}
}

// --- NewPrompter ---

func TestNewPrompter(t *testing.T) {
	var ui UI
	p := NewPrompter(strings.NewReader(""), &strings.Builder{}, &strings.Builder{}, &ui)
	if p == nil {
		t.Fatal("NewPrompter returned nil")
	}
	if p.Scanner == nil {
		t.Error("Scanner should be set")
	}
	if p.UI != &ui {
		t.Error("UI should be set")
	}
}
