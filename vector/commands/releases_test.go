package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/seeder"
	"matrixos/vector/lib/validation"
)

// --- Test helpers ---

// newTestReleasesCommand creates a ReleasesCommand with injected mocks,
// bypassing Init() which requires real config files and root.
func newTestReleasesCommand(
	ot ostree.IOstree,
	sd *seeder.MockSeeder,
	det *seeder.MockSeederDetector,
	cfg *config.MockConfig,
	args []string,
) (*ReleasesCommand, error) {
	cmd := NewReleasesCommand()
	cmd.ot = ot
	cmd.sd = sd
	cmd.det = det
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

func defaultReleasesTestConfig(t *testing.T) *config.MockConfig {
	t.Helper()
	return &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Sysroot":              {"/sysroot"},
			"Ostree.FullBranchSuffix":     {"full"},
			"matrixOS.OsName":             {"matrixos"},
			"matrixOS.Arch":               {"amd64"},
			"matrixOS.PrivateGitRepoPath": {t.TempDir()},
			"Releaser.LocksDir":           {filepath.Join(t.TempDir(), "locks")},
			"Releaser.LockWaitSeconds":    {"5"},
		},
	}
}

func defaultReleasesTestSeeders(baseDir string) []seeder.SeederInfo {
	seederDir := filepath.Join(baseDir, "00-bedrock")
	return []seeder.SeederInfo{
		{
			Name:        "00-bedrock",
			Dir:         seederDir,
			ChrootExec:  filepath.Join(seederDir, "chroot.sh"),
			PrepperExec: filepath.Join(seederDir, "prepper.sh"),
		},
	}
}

// setupReleasesTestDir creates a temp directory containing the mock seeder
// structure (with a params.sh file) and a mock chroot directory.
// It returns (seedersBaseDir, chrootDir).
func setupReleasesTestDir(t *testing.T) (string, string) {
	t.Helper()
	baseDir := t.TempDir()
	seederDir := filepath.Join(baseDir, "00-bedrock")
	if err := os.MkdirAll(seederDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(seederDir, "params.sh"),
		[]byte("#!/bin/bash\n"), 0755,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	chrootDir := filepath.Join(t.TempDir(), "bedrock-20260228")
	if err := os.MkdirAll(chrootDir, 0755); err != nil {
		t.Fatalf("MkdirAll chroot: %v", err)
	}
	return baseDir, chrootDir
}

// requireReleasesTools skips the test if the host does not have
// the executables that VerifyReleaserEnvironmentSetup checks for.
func requireReleasesTools(t *testing.T) {
	t.Helper()
	tools := []string{
		"chroot", "find", "findmnt", "gpg",
		"openssl", "ostree", "unshare",
	}
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf(
				"skipping: required tool %q not found", tool,
			)
		}
	}
}

// --- Tests ---

func TestReleasesName(t *testing.T) {
	cmd := NewReleasesCommand()
	if name := cmd.Name(); name != "releases" {
		t.Errorf("Expected name 'releases', got %q", name)
	}
}

func TestNewReleasesCommand(t *testing.T) {
	cmd := NewReleasesCommand()
	if cmd == nil {
		t.Fatal("NewReleasesCommand returned nil")
	}
}

func TestReleasesParseArgsNotRoot(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 1000 }
	defer func() { getEuid = origEuid }()

	cmd := NewReleasesCommand()
	cmd.StartUI()
	err := cmd.parseArgs([]string{})
	if err == nil {
		t.Fatal("Expected error for non-root, got nil")
	}
	if !strings.Contains(err.Error(), "must be run as root") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestReleasesParseArgsDefaults(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	cmd := NewReleasesCommand()
	cmd.StartUI()
	if err := cmd.parseArgs([]string{}); err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cmd.releaseStage != "dev" {
		t.Errorf(
			"Expected releaseStage 'dev', got %q",
			cmd.releaseStage,
		)
	}
	if cmd.verbose {
		t.Error("Expected verbose false by default")
	}
	if len(cmd.skipSeeders) != 0 {
		t.Errorf(
			"Expected empty skipSeeders, got %v",
			cmd.skipSeeders,
		)
	}
	if len(cmd.onlySeeders) != 0 {
		t.Errorf(
			"Expected empty onlySeeders, got %v",
			cmd.onlySeeders,
		)
	}
}

func TestReleasesParseArgsValid(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	cmd := NewReleasesCommand()
	cmd.StartUI()
	err := cmd.parseArgs([]string{
		"--release-stage", "prod",
		"--skip-seeders", "a,b",
		"--only-seeders", "c",
		"--built-releases-file", "/tmp/releases.txt",
		"--verbose",
	})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}

	checks := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"releaseStage", cmd.releaseStage, "prod"},
		{"builtReleasesFile", cmd.builtReleasesFile, "/tmp/releases.txt"},
		{"verbose", cmd.verbose, true},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}

	if len(cmd.skipSeeders) != 2 {
		t.Errorf(
			"skipSeeders len: got %d, want 2",
			len(cmd.skipSeeders),
		)
	}
	if len(cmd.onlySeeders) != 1 {
		t.Errorf(
			"onlySeeders len: got %d, want 1",
			len(cmd.onlySeeders),
		)
	}
}

func TestReleasesParseArgsInvalidStage(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	cmd := NewReleasesCommand()
	cmd.StartUI()
	err := cmd.parseArgs([]string{
		"--release-stage", "invalid",
	})
	if err == nil {
		t.Fatal("Expected error for invalid release stage, got nil")
	}
	if !strings.Contains(err.Error(), "unknown release stage") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// --- Filter tests ---

func TestReleasesSkipFilter(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	cmd := NewReleasesCommand()
	cmd.StartUI()
	_ = cmd.parseArgs([]string{"--skip-seeders", "00-bedrock,20-gnome"})

	skip := cmd.skipFilter()
	if skip == nil {
		t.Fatal("Expected non-nil skip filter")
	}
	if !skip("00-bedrock") {
		t.Error("Expected 00-bedrock to be skipped")
	}
	if skip("10-server") {
		t.Error("Expected 10-server to NOT be skipped")
	}
	if !skip("20-gnome") {
		t.Error("Expected 20-gnome to be skipped")
	}
}

func TestReleasesSkipFilterNil(t *testing.T) {
	cmd := &ReleasesCommand{}
	if cmd.skipFilter() != nil {
		t.Error("Expected nil skip filter when no skip seeders set")
	}
}

func TestReleasesOnlyFilter(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	cmd := NewReleasesCommand()
	cmd.StartUI()
	_ = cmd.parseArgs([]string{"--only-seeders", "00-bedrock"})

	only := cmd.onlyFilter()
	if only == nil {
		t.Fatal("Expected non-nil only filter")
	}
	if !only("00-bedrock") {
		t.Error("Expected 00-bedrock to pass only filter")
	}
	if only("10-server") {
		t.Error("Expected 10-server to NOT pass only filter")
	}
}

func TestReleasesOnlyFilterNil(t *testing.T) {
	cmd := &ReleasesCommand{}
	if cmd.onlyFilter() != nil {
		t.Error(
			"Expected nil only filter when no only seeders set",
		)
	}
}

// --- No seeders found ---

func TestReleasesNoSeedersFound(t *testing.T) {
	requireReleasesTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	ot := &ostree.MockOstree{}
	sd := seeder.DefaultMockSeeder()
	det := &seeder.MockSeederDetector{
		Detect_: nil, // no seeders
	}
	cfg := defaultReleasesTestConfig(t)

	cmd, err := newTestReleasesCommand(ot, sd, det, cfg, []string{})
	if err != nil {
		t.Fatalf("newTestReleasesCommand: %v", err)
	}

	err = cmd.runReleases()
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no seeders found") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// --- Detection error ---

func TestReleasesDetectionError(t *testing.T) {
	requireReleasesTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	ot := &ostree.MockOstree{}
	sd := seeder.DefaultMockSeeder()
	det := &seeder.MockSeederDetector{
		DetectErr: fmt.Errorf("scan failed"),
	}
	cfg := defaultReleasesTestConfig(t)

	cmd, err := newTestReleasesCommand(ot, sd, det, cfg, []string{})
	if err != nil {
		t.Fatalf("newTestReleasesCommand: %v", err)
	}

	err = cmd.runReleases()
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !strings.Contains(err.Error(), "seeder detection failed") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// --- Find chroot dir error ---

func TestReleasesFindChrootDirParamsError(t *testing.T) {
	requireReleasesTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	ot := &ostree.MockOstree{
		OsName_: "matrixos",
		Arch_:   "amd64",
	}
	sd := seeder.DefaultMockSeeder()
	sd.ParseSeederParamsErr = fmt.Errorf("bad params")

	baseDir, _ := setupReleasesTestDir(t)
	det := &seeder.MockSeederDetector{
		Detect_: defaultReleasesTestSeeders(baseDir),
	}
	cfg := defaultReleasesTestConfig(t)

	cmd, err := newTestReleasesCommand(ot, sd, det, cfg, []string{})
	if err != nil {
		t.Fatalf("newTestReleasesCommand: %v", err)
	}

	err = cmd.runReleases()
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse params") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// --- Built releases file ---

func TestReleasesBuiltReleasesFile(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	tmp := t.TempDir()
	relFile := filepath.Join(tmp, "releases.txt")

	cmd := &ReleasesCommand{
		builtReleasesFile: relFile,
	}
	cmd.sd = seeder.DefaultMockSeeder()

	// Init the file.
	if err := cmd.initBuiltReleasesFile(); err != nil {
		t.Fatalf("initBuiltReleasesFile failed: %v", err)
	}

	// Record some branches.
	if err := cmd.recordBuiltRelease("matrixos/amd64/dev/bedrock"); err != nil {
		t.Fatalf("recordBuiltRelease failed: %v", err)
	}
	if err := cmd.recordBuiltRelease("matrixos/amd64/dev/gnome"); err != nil {
		t.Fatalf("recordBuiltRelease failed: %v", err)
	}

	data, err := os.ReadFile(relFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "matrixos/amd64/dev/bedrock") {
		t.Errorf(
			"Expected bedrock branch in file, got: %s", content,
		)
	}
	if !strings.Contains(content, "matrixos/amd64/dev/gnome") {
		t.Errorf(
			"Expected gnome branch in file, got: %s", content,
		)
	}

	// Verify in-memory accumulation.
	want := []string{
		"matrixos/amd64/dev/bedrock",
		"matrixos/amd64/dev/gnome",
	}
	if len(cmd.BuiltReleases) != len(want) {
		t.Fatalf("BuiltReleases len = %d, want %d",
			len(cmd.BuiltReleases), len(want))
	}
	for i, w := range want {
		if cmd.BuiltReleases[i] != w {
			t.Errorf("BuiltReleases[%d] = %q, want %q",
				i, cmd.BuiltReleases[i], w)
		}
	}
}

func TestReleasesBuiltReleasesFileEmpty(t *testing.T) {
	// No file configured — should be a no-op for file I/O.
	cmd := &ReleasesCommand{}
	cmd.sd = seeder.DefaultMockSeeder()

	if err := cmd.initBuiltReleasesFile(); err != nil {
		t.Fatalf("initBuiltReleasesFile failed: %v", err)
	}
	if err := cmd.recordBuiltRelease("branch"); err != nil {
		t.Fatalf("recordBuiltRelease failed: %v", err)
	}

	// In-memory slice is still populated even without a file.
	if len(cmd.BuiltReleases) != 1 ||
		cmd.BuiltReleases[0] != "branch" {
		t.Errorf("BuiltReleases = %v, want [branch]",
			cmd.BuiltReleases)
	}
}

// --- Build subcommand registration ---

func TestReleasesBuildSubcommand(t *testing.T) {
	bc := NewBuildCommand()
	err := bc.Init([]string{"releases", "--help"})
	// --help causes flag.Parse to return ErrHelp, which is acceptable.
	// What matters is "releases" is recognized (not "unknown subcommand").
	if err != nil && strings.Contains(
		err.Error(), "unknown subcommand",
	) {
		t.Errorf(
			"Expected 'releases' to be a known subcommand, got: %v",
			err,
		)
	}
}
