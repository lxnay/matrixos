package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/seeder"
	"matrixos/vector/lib/validation"
)

// --- Test helpers ---

// fakeMissingTools creates stub executables for any tools required by
// VerifySeederEnvironmentSetup that are not present on the host, and
// prepends the temp directory to PATH so exec.LookPath finds them.
// It restores PATH when the test finishes.
func fakeMissingTools(t *testing.T) {
	t.Helper()
	tools := []string{
		"chroot", "git", "gpg", "openssl",
		"ostree", "unshare", "wget",
	}
	var missing []string
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	if len(missing) == 0 {
		return
	}
	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, tool := range missing {
		p := filepath.Join(binDir, tool)
		if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("WriteFile %s: %v", tool, err)
		}
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+":"+origPath)
}

// newTestSeedsCommand creates a SeedsCommand with injected mocks,
// bypassing Init() which requires real config files and root.
// sd may be nil, in which case a DefaultMockSeeder is used.
// The caller must defer the returned cleanup function to restore
// the package-level newSeeder var.
func newTestSeedsCommand(sd *seeder.MockSeeder, det *seeder.MockSeederDetector,
	cfg *config.MockConfig, args []string,
) (*SeedsCommand, func(), error) {
	if sd == nil {
		sd = seeder.DefaultMockSeeder()
	}
	origNewSeeder := newSeeder
	newSeeder = func(_ config.IConfig, _ *seeder.NewSeederOptions) (seeder.ISeeder, error) {
		return sd, nil
	}

	cmd := NewSeedsCommand()
	cmd.det = det
	cmd.cfg = cfg

	qa, err := validation.New(cfg)
	if err != nil {
		newSeeder = origNewSeeder
		return nil, nil, err
	}
	cmd.qa = qa
	cmd.StartUI()

	if err := cmd.parseArgs(args); err != nil {
		newSeeder = origNewSeeder
		return nil, nil, err
	}
	return cmd, func() { newSeeder = origNewSeeder }, nil
}

func defaultSeedsTestConfig(t *testing.T) *config.MockConfig {
	t.Helper()
	return &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.PrivateGitRepoPath": {t.TempDir()},
		},
	}
}

func defaultSeedsTestSeeders(baseDir string) []seeder.SeederInfo {
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

// setupSeedsTestDir creates a temp directory containing the mock seeder
// structure (with a params.sh file) and a mock chroot directory.
// It returns (seedersBaseDir, chrootDir). Both directories are
// automatically cleaned up when the test finishes.
func setupSeedsTestDir(t *testing.T) (string, string) {
	t.Helper()
	baseDir := t.TempDir()
	seederDir := filepath.Join(baseDir, "00-bedrock")
	if err := os.MkdirAll(seederDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seederDir, "params.sh"), []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	chrootDir := filepath.Join(t.TempDir(), "bedrock-20260228")
	if err := os.MkdirAll(chrootDir, 0755); err != nil {
		t.Fatalf("MkdirAll chroot: %v", err)
	}
	return baseDir, chrootDir
}

// requireSeedsTools ensures all executables needed by
// VerifySeederEnvironmentSetup are available, creating stubs for any
// that are missing.
func requireSeedsTools(t *testing.T) {
	t.Helper()
	fakeMissingTools(t)
}

// --- Tests ---

func TestSeedsName(t *testing.T) {
	cmd := NewSeedsCommand()
	if name := cmd.Name(); name != "seeds" {
		t.Errorf("Expected name 'seeds', got %q", name)
	}
}

func TestNewSeedsCommand(t *testing.T) {
	cmd := NewSeedsCommand()
	if cmd == nil {
		t.Fatal("NewSeedsCommand returned nil")
	}
}

func TestSeedsParseArgsNotRoot(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 1000 }
	defer func() { getEuid = origEuid }()

	cmd := NewSeedsCommand()
	cmd.StartUI()
	err := cmd.parseArgs([]string{})
	if err == nil {
		t.Fatal("Expected error for non-root, got nil")
	}
	if !strings.Contains(err.Error(), "must be run as root") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestSeedsParseArgsValid(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	cmd := NewSeedsCommand()
	cmd.StartUI()
	err := cmd.parseArgs([]string{
		"--chroot-dir", "/some/chroot",
		"--skip-seeders", "a,b",
		"--only-seeders", "c",
		"--resume",
		"--stage3-file", "/tmp/stage3.tar.xz",
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
		{"chrootDir", cmd.chrootDir, "/some/chroot"},
		{"resume", cmd.resume, true},
		{"stage3File", cmd.stage3File, "/tmp/stage3.tar.xz"},
		{"verbose", cmd.verbose, true},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}

	if len(cmd.skipSeeders) != 2 {
		t.Errorf("skipSeeders len: got %d, want 2", len(cmd.skipSeeders))
	}
	if len(cmd.onlySeeders) != 1 {
		t.Errorf("onlySeeders len: got %d, want 1", len(cmd.onlySeeders))
	}
}

func TestSeedsParseArgsDefaults(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	cmd := NewSeedsCommand()
	cmd.StartUI()
	if err := cmd.parseArgs([]string{}); err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cmd.verbose {
		t.Error("Expected verbose false by default")
	}
	if cmd.resume {
		t.Error("Expected resume false by default")
	}
}

func TestSeedsNoSeedersFound(t *testing.T) {
	requireSeedsTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	det := &seeder.MockSeederDetector{
		Detect_: nil, // no seeders
	}
	cfg := defaultSeedsTestConfig(t)

	cmd, cleanup, err := newTestSeedsCommand(nil, det, cfg, []string{})
	if err != nil {
		t.Fatalf("newTestSeedsCommand: %v", err)
	}
	defer cleanup()

	err = cmd.runSeeds()
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no seeders found") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestSeedsDetectionError(t *testing.T) {
	requireSeedsTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	det := &seeder.MockSeederDetector{
		DetectErr: fmt.Errorf("scan failed"),
	}
	cfg := defaultSeedsTestConfig(t)

	cmd, cleanup, err := newTestSeedsCommand(nil, det, cfg, []string{})
	if err != nil {
		t.Fatalf("newTestSeedsCommand: %v", err)
	}
	defer cleanup()

	err = cmd.runSeeds()
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !strings.Contains(err.Error(), "seeder detection failed") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestSeedsGpgImportError(t *testing.T) {
	requireSeedsTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	sd := seeder.DefaultMockSeeder()
	sd.ImportGentooGpgKeysErr = fmt.Errorf("gpg failed")
	det := &seeder.MockSeederDetector{
		Detect_: defaultSeedsTestSeeders(t.TempDir()),
	}
	cfg := defaultSeedsTestConfig(t)

	cmd, cleanup, err := newTestSeedsCommand(sd, det, cfg, []string{})
	if err != nil {
		t.Fatalf("newTestSeedsCommand: %v", err)
	}
	defer cleanup()

	err = cmd.runSeeds()
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !strings.Contains(err.Error(), "GPG key import failed") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestSeedsPrivateRepoError(t *testing.T) {
	requireSeedsTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	sd := seeder.DefaultMockSeeder()
	sd.MaybeInitializePrivateRepoErr = fmt.Errorf("clone failed")
	det := &seeder.MockSeederDetector{
		Detect_: defaultSeedsTestSeeders(t.TempDir()),
	}
	cfg := defaultSeedsTestConfig(t)

	cmd, cleanup, err := newTestSeedsCommand(sd, det, cfg, []string{})
	if err != nil {
		t.Fatalf("newTestSeedsCommand: %v", err)
	}
	defer cleanup()

	err = cmd.runSeeds()
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !strings.Contains(err.Error(), "private repo") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestSeedsFullPipeline(t *testing.T) {
	requireSeedsTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	sd := seeder.DefaultMockSeeder()
	baseDir, chrootDir := setupSeedsTestDir(t)
	sd.ParseSeederParams_ = &seeder.SeederParams{
		ChrootName:         "bedrock-20260228",
		ChrootsDir:         filepath.Dir(chrootDir),
		PreferredChrootDir: chrootDir,
	}
	det := &seeder.MockSeederDetector{
		Detect_: defaultSeedsTestSeeders(baseDir),
	}
	cfg := defaultSeedsTestConfig(t)

	cmd, cleanup, err := newTestSeedsCommand(sd, det, cfg, []string{})
	if err != nil {
		t.Fatalf("newTestSeedsCommand: %v", err)
	}
	defer cleanup()

	err = cmd.runSeeds()
	if err != nil {
		t.Fatalf("runSeeds failed: %v", err)
	}

	// Verify pipeline steps were called.
	if !sd.ImportGentooGpgKeysCalled {
		t.Error("ImportGentooGpgKeys not called")
	}
	if !sd.MaybeInitializePrivateRepoCalled {
		t.Error("MaybeInitializePrivateRepo not called")
	}
	if !sd.ExecuteWithSeederLockCalled {
		t.Error("ExecuteWithSeederLock not called")
	}
	if !sd.ExecutePrepperCalled {
		t.Error("ExecutePrepper not called")
	}
	if !sd.SetupChrootDNSCalled {
		t.Error("SetupChrootDNS not called")
	}
	if !sd.SetupChrootDirsCalled {
		t.Error("SetupChrootDirs not called")
	}
	if !sd.SeedCalled {
		t.Error("Seed not called")
	}
	if !sd.MarkSeederDoneCalled {
		t.Error("MarkSeederDone not called")
	}
}

func TestSeedsWorkerPrepperError(t *testing.T) {
	requireSeedsTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	sd := seeder.DefaultMockSeeder()
	baseDir, chrootDir := setupSeedsTestDir(t)
	sd.ParseSeederParams_ = &seeder.SeederParams{
		ChrootName:         "bedrock-20260228",
		ChrootsDir:         filepath.Dir(chrootDir),
		PreferredChrootDir: chrootDir,
	}
	sd.ExecutePrepperErr = fmt.Errorf("prepper failed")
	det := &seeder.MockSeederDetector{
		Detect_: defaultSeedsTestSeeders(baseDir),
	}
	cfg := defaultSeedsTestConfig(t)

	cmd, cleanup, err := newTestSeedsCommand(sd, det, cfg, []string{})
	if err != nil {
		t.Fatalf("newTestSeedsCommand: %v", err)
	}
	defer cleanup()

	err = cmd.runSeeds()
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
	if !strings.Contains(err.Error(), "prepper failed") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestSeedsWorkerSkipsDoneSeeder(t *testing.T) {
	requireSeedsTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	sd := seeder.DefaultMockSeeder()
	baseDir, chrootDir := setupSeedsTestDir(t)
	sd.ParseSeederParams_ = &seeder.SeederParams{
		ChrootName:         "bedrock-20260228",
		ChrootsDir:         filepath.Dir(chrootDir),
		PreferredChrootDir: chrootDir,
	}
	sd.IsSeederDone_ = true // Already done
	det := &seeder.MockSeederDetector{
		Detect_: defaultSeedsTestSeeders(baseDir),
	}
	cfg := defaultSeedsTestConfig(t)

	cmd, cleanup, err := newTestSeedsCommand(sd, det, cfg, []string{})
	if err != nil {
		t.Fatalf("newTestSeedsCommand: %v", err)
	}
	defer cleanup()

	err = cmd.runSeeds()
	if err != nil {
		t.Fatalf("runSeeds failed: %v", err)
	}

	// The prepper and chroot should NOT have been called.
	if sd.ExecutePrepperCalled {
		t.Error("ExecutePrepper should not be called for done seeder")
	}
	if sd.SeedCalled {
		t.Error("Seed should not be called for done seeder")
	}
}

func TestSeedsBuildSubcommand(t *testing.T) {
	bc := NewBuildCommand()
	err := bc.Init([]string{"seeds", "--help"})
	// --help causes flag.Parse to return ErrHelp, which is acceptable.
	// What matters is "seeds" is recognized.
	if err != nil && strings.Contains(
		err.Error(), "unknown subcommand",
	) {
		t.Errorf(
			"Expected 'seeds' to be a known subcommand, got: %v",
			err,
		)
	}
}

func TestSeedsOutputFiles(t *testing.T) {
	requireSeedsTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	tmp := t.TempDir()
	rootfsFile := filepath.Join(tmp, "rootfs.txt")
	seedersFile := filepath.Join(tmp, "seeders.txt")

	sd := seeder.DefaultMockSeeder()
	baseDir, chrootDir := setupSeedsTestDir(t)
	sd.ParseSeederParams_ = &seeder.SeederParams{
		ChrootName:         "bedrock-20260228",
		ChrootsDir:         filepath.Dir(chrootDir),
		PreferredChrootDir: chrootDir,
	}

	det := &seeder.MockSeederDetector{
		Detect_: defaultSeedsTestSeeders(baseDir),
	}
	cfg := defaultSeedsTestConfig(t)

	cmd, cleanup, err := newTestSeedsCommand(sd, det, cfg, []string{
		"--built-rootfs-file", rootfsFile,
		"--built-seeders-file", seedersFile,
	})
	if err != nil {
		t.Fatalf("newTestSeedsCommand: %v", err)
	}
	defer cleanup()

	if err := cmd.runSeeds(); err != nil {
		t.Fatalf("runSeeds failed: %v", err)
	}

	rootfsData, err := os.ReadFile(rootfsFile)
	if err != nil {
		t.Fatalf("read rootfs file: %v", err)
	}
	if !strings.Contains(string(rootfsData), "bedrock-20260228") {
		t.Errorf(
			"rootfs file should contain chroot dir, got: %s",
			string(rootfsData),
		)
	}

	seedersData, err := os.ReadFile(seedersFile)
	if err != nil {
		t.Fatalf("read seeders file: %v", err)
	}
	if !strings.Contains(string(seedersData), "00-bedrock") {
		t.Errorf(
			"seeders file should contain seeder name, got: %s",
			string(seedersData),
		)
	}
}

// --- skipFilter / onlyFilter ---

func TestSeedsSkipFilterNil(t *testing.T) {
	cmd := &SeedsCommand{}
	if f := cmd.skipFilter(); f != nil {
		t.Error("Expected nil filter when skipSeeders is empty")
	}
}

func TestSeedsSkipFilterMatch(t *testing.T) {
	cmd := &SeedsCommand{skipSeeders: []string{"a", "b"}}
	f := cmd.skipFilter()
	if f == nil {
		t.Fatal("Expected non-nil filter")
	}
	if !f("a") {
		t.Error("Expected 'a' to match skip filter")
	}
	if f("c") {
		t.Error("Expected 'c' not to match skip filter")
	}
}

func TestSeedsOnlyFilterNil(t *testing.T) {
	cmd := &SeedsCommand{}
	if f := cmd.onlyFilter(); f != nil {
		t.Error("Expected nil filter when onlySeeders is empty")
	}
}

func TestSeedsOnlyFilterMatch(t *testing.T) {
	cmd := &SeedsCommand{onlySeeders: []string{"x", "y"}}
	f := cmd.onlyFilter()
	if f == nil {
		t.Fatal("Expected non-nil filter")
	}
	if !f("x") {
		t.Error("Expected 'x' to match only filter")
	}
	if f("z") {
		t.Error("Expected 'z' not to match only filter")
	}
}

// --- initOutputFiles ---

func TestSeedsInitOutputFilesCreatesFiles(t *testing.T) {
	tmp := t.TempDir()
	rootfs := filepath.Join(tmp, "rootfs.txt")
	seeders := filepath.Join(tmp, "seeders.txt")

	cmd := &SeedsCommand{}
	cmd.StartUI()
	cmd.builtRootfsFile = rootfs
	cmd.builtSeedersFile = seeders

	if err := cmd.initOutputFiles(); err != nil {
		t.Fatalf("initOutputFiles: %v", err)
	}

	for _, f := range []string{rootfs, seeders} {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("ReadFile %s: %v", f, err)
		}
		if len(data) != 0 {
			t.Errorf("Expected empty file %s, got %d bytes", f, len(data))
		}
	}
}

func TestSeedsInitOutputFilesNoop(t *testing.T) {
	cmd := &SeedsCommand{}
	cmd.StartUI()
	// No flags set — should succeed without creating anything.
	if err := cmd.initOutputFiles(); err != nil {
		t.Fatalf("initOutputFiles: %v", err)
	}
}

func TestSeedsInitOutputFilesError(t *testing.T) {
	cmd := &SeedsCommand{}
	cmd.StartUI()
	cmd.builtRootfsFile = "/nonexistent-dir-xyz/file.txt"
	if err := cmd.initOutputFiles(); err == nil {
		t.Error("Expected error for bad path")
	}
}

func TestSeedsInitOutputFilesSeedersError(t *testing.T) {
	tmp := t.TempDir()
	cmd := &SeedsCommand{}
	cmd.StartUI()
	cmd.builtRootfsFile = filepath.Join(tmp, "rootfs.txt")
	cmd.builtSeedersFile = "/nonexistent-dir-xyz/seeders.txt"
	if err := cmd.initOutputFiles(); err == nil {
		t.Error("Expected error for bad seeders path")
	}
}

// --- recordBuiltRootfsFile / recordBuiltSeedersFile / recordResults ---

func TestSeedsRecordBuiltRootfsFile(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "rootfs.txt")
	if err := os.WriteFile(f, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := &SeedsCommand{}
	cmd.builtRootfsFile = f

	if err := cmd.recordBuiltRootfsFile("/chroot/a"); err != nil {
		t.Fatalf("recordBuiltRootfsFile: %v", err)
	}
	if err := cmd.recordBuiltRootfsFile("/chroot/b"); err != nil {
		t.Fatalf("recordBuiltRootfsFile: %v", err)
	}

	data, _ := os.ReadFile(f)
	lines := strings.TrimSpace(string(data))
	if lines != "/chroot/a\n/chroot/b" {
		t.Errorf("Unexpected content: %q", lines)
	}
}

func TestSeedsRecordBuiltRootfsFileNoFlag(t *testing.T) {
	cmd := &SeedsCommand{}
	if err := cmd.recordBuiltRootfsFile("/chroot/a"); err != nil {
		t.Fatalf("Expected nil error when flag empty, got: %v", err)
	}
}

func TestSeedsRecordBuiltSeedersFile(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "seeders.txt")
	if err := os.WriteFile(f, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := &SeedsCommand{}
	cmd.builtSeedersFile = f

	if err := cmd.recordBuiltSeedersFile("00-bedrock"); err != nil {
		t.Fatalf("recordBuiltSeedersFile: %v", err)
	}

	data, _ := os.ReadFile(f)
	if !strings.Contains(string(data), "00-bedrock") {
		t.Errorf("Expected seeder name in file, got: %s", data)
	}

	// Verify in-memory accumulation.
	if len(cmd.BuiltSeeders) != 1 ||
		cmd.BuiltSeeders[0] != "00-bedrock" {
		t.Errorf("BuiltSeeders = %v, want [00-bedrock]",
			cmd.BuiltSeeders)
	}
}

func TestSeedsRecordBuiltSeedersFileNoFlag(t *testing.T) {
	cmd := &SeedsCommand{}
	if err := cmd.recordBuiltSeedersFile("x"); err != nil {
		t.Fatalf("Expected nil error when flag empty, got: %v", err)
	}
	// In-memory slice is still populated even without a file.
	if len(cmd.BuiltSeeders) != 1 || cmd.BuiltSeeders[0] != "x" {
		t.Errorf("BuiltSeeders = %v, want [x]", cmd.BuiltSeeders)
	}
}

func TestSeedsRecordResults(t *testing.T) {
	tmp := t.TempDir()
	rootfsFile := filepath.Join(tmp, "rootfs.txt")
	seedersFile := filepath.Join(tmp, "seeders.txt")
	for _, f := range []string{rootfsFile, seedersFile} {
		if err := os.WriteFile(f, []byte{}, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cmd := &SeedsCommand{}
	cmd.builtRootfsFile = rootfsFile
	cmd.builtSeedersFile = seedersFile

	if err := cmd.recordResults("00-bedrock", "/chroot/bed"); err != nil {
		t.Fatalf("recordResults: %v", err)
	}

	rootfsData, _ := os.ReadFile(rootfsFile)
	if !strings.Contains(string(rootfsData), "/chroot/bed") {
		t.Error("rootfs file missing chroot dir")
	}
	seedersData, _ := os.ReadFile(seedersFile)
	if !strings.Contains(string(seedersData), "00-bedrock") {
		t.Error("seeders file missing seeder name")
	}

	// Verify in-memory accumulation via recordResults.
	if len(cmd.BuiltSeeders) != 1 ||
		cmd.BuiltSeeders[0] != "00-bedrock" {
		t.Errorf("BuiltSeeders = %v, want [00-bedrock]",
			cmd.BuiltSeeders)
	}
}

func TestSeedsRecordResultsRootfsError(t *testing.T) {
	cmd := &SeedsCommand{}
	cmd.builtRootfsFile = "/nonexistent-xyz/rootfs.txt"
	if err := cmd.recordResults("s", "/c"); err == nil {
		t.Error("Expected error for bad rootfs path")
	}
}

func TestSeedsRecordResultsSeedersError(t *testing.T) {
	tmp := t.TempDir()
	rootfsFile := filepath.Join(tmp, "rootfs.txt")
	if err := os.WriteFile(rootfsFile, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := &SeedsCommand{}
	cmd.builtRootfsFile = rootfsFile
	cmd.builtSeedersFile = "/nonexistent-xyz/seeders.txt"
	if err := cmd.recordResults("s", "/c"); err == nil {
		t.Error("Expected error for bad seeders path")
	}
}

// --- updateStdWriters ---

func TestSeedsUpdateStdWriters(t *testing.T) {
	sd := seeder.DefaultMockSeeder()
	det := &seeder.MockSeederDetector{}
	cmd := &SeedsCommand{}
	cmd.det = det
	cmd.StartUI()
	cmd.updateStdWriters(sd, "test-seeder")

	if cmd.StdoutWriter() == nil {
		t.Error("StdoutWriter should be set")
	}
	if cmd.StderrWriter() == nil {
		t.Error("StderrWriter should be set")
	}
}

// --- Run ---

func TestSeedsRunDelegates(t *testing.T) {
	requireSeedsTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	sd := seeder.DefaultMockSeeder()
	baseDir, chrootDir := setupSeedsTestDir(t)
	sd.ParseSeederParams_ = &seeder.SeederParams{
		ChrootName:         "bedrock-20260228",
		ChrootsDir:         filepath.Dir(chrootDir),
		PreferredChrootDir: chrootDir,
	}
	det := &seeder.MockSeederDetector{
		Detect_: defaultSeedsTestSeeders(baseDir),
	}
	cfg := defaultSeedsTestConfig(t)

	cmd, cleanup, err := newTestSeedsCommand(sd, det, cfg, []string{})
	if err != nil {
		t.Fatalf("newTestSeedsCommand: %v", err)
	}
	defer cleanup()

	// Run() wraps runSeeds via RunWithGuard — verify it completes.
	if err := cmd.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sd.SeedCalled {
		t.Error("Seed should be called via Run()")
	}
}

// --- runSeeds error paths ---

func TestSeedsRunSeedsNewSeederError(t *testing.T) {
	requireSeedsTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	cfg := defaultSeedsTestConfig(t)
	det := &seeder.MockSeederDetector{}

	origNewSeeder := newSeeder
	newSeeder = func(_ config.IConfig, _ *seeder.NewSeederOptions) (seeder.ISeeder, error) {
		return nil, fmt.Errorf("seeder init boom")
	}
	defer func() { newSeeder = origNewSeeder }()

	cmd := NewSeedsCommand()
	cmd.det = det
	cmd.cfg = cfg
	qa, _ := validation.New(cfg)
	cmd.qa = qa
	cmd.StartUI()

	err := cmd.runSeeds()
	if err == nil || !strings.Contains(err.Error(), "seeder init boom") {
		t.Fatalf("Expected seeder init error, got: %v", err)
	}
}

func TestSeedsRunSeedsEnvironmentError(t *testing.T) {
	// Use a config that makes VerifySeederEnvironmentSetup fail
	// by pointing PrivateGitRepoPath to a non-existent directory.
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.PrivateGitRepoPath": {"/nonexistent-dir-xyz-abc"},
		},
	}
	det := &seeder.MockSeederDetector{}

	cmd, cleanup, err := newTestSeedsCommand(nil, det, cfg, []string{})
	if err != nil {
		t.Fatalf("newTestSeedsCommand: %v", err)
	}
	defer cleanup()

	err = cmd.runSeeds()
	if err == nil || !strings.Contains(err.Error(), "environment verification failed") {
		t.Fatalf("Expected environment error, got: %v", err)
	}
}

// --- Init via real newTestSeedsCommand ---

func TestSeedsParseArgsInvalidFlag(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	cmd := NewSeedsCommand()
	cmd.StartUI()

	err := cmd.parseArgs([]string{"--invalid-flag"})
	if err == nil {
		t.Error("Expected error for invalid flag")
	}
}

// --- Verify full pipeline with verbose flag ---

func TestSeedsFullPipelineVerbose(t *testing.T) {
	requireSeedsTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	sd := seeder.DefaultMockSeeder()
	baseDir, chrootDir := setupSeedsTestDir(t)
	sd.ParseSeederParams_ = &seeder.SeederParams{
		ChrootName:         "bedrock-20260228",
		ChrootsDir:         filepath.Dir(chrootDir),
		PreferredChrootDir: chrootDir,
	}
	det := &seeder.MockSeederDetector{
		Detect_: defaultSeedsTestSeeders(baseDir),
	}
	cfg := defaultSeedsTestConfig(t)

	cmd, cleanup, err := newTestSeedsCommand(sd, det, cfg, []string{
		"--verbose",
	})
	if err != nil {
		t.Fatalf("newTestSeedsCommand: %v", err)
	}
	defer cleanup()

	if !cmd.verbose {
		t.Error("Expected verbose=true")
	}
	if err := cmd.runSeeds(); err != nil {
		t.Fatalf("runSeeds: %v", err)
	}
}

func TestSeedsExecuteWithSeederLockError(t *testing.T) {
	requireSeedsTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	sd := seeder.DefaultMockSeeder()
	sd.ExecuteWithSeederLockErr = fmt.Errorf("lock failed")
	baseDir, chrootDir := setupSeedsTestDir(t)
	sd.ParseSeederParams_ = &seeder.SeederParams{
		ChrootName:         "bedrock-20260228",
		ChrootsDir:         filepath.Dir(chrootDir),
		PreferredChrootDir: chrootDir,
	}
	det := &seeder.MockSeederDetector{
		Detect_: defaultSeedsTestSeeders(baseDir),
	}
	cfg := defaultSeedsTestConfig(t)

	cmd, cleanup, err := newTestSeedsCommand(sd, det, cfg, []string{})
	if err != nil {
		t.Fatalf("newTestSeedsCommand: %v", err)
	}
	defer cleanup()

	err = cmd.runSeeds()
	if err == nil || !strings.Contains(err.Error(), "lock failed") {
		t.Fatalf("Expected lock error, got: %v", err)
	}
}

// --- Parallel execution tests ---

// setupParallelSeeders creates a temp directory with multiple seeder
// directories containing params.sh files, and returns the base dir,
// SeederInfo list, and a map of chrootDirs per seeder name.
func setupParallelSeeders(t *testing.T, names []string) (string, []seeder.SeederInfo, map[string]string) {
	t.Helper()
	baseDir := t.TempDir()
	var infos []seeder.SeederInfo
	chrootDirs := make(map[string]string)
	for _, name := range names {
		seederDir := filepath.Join(baseDir, name)
		if err := os.MkdirAll(seederDir, 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(seederDir, "params.sh"), []byte("#!/bin/bash\n"), 0755); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		chrootDir := filepath.Join(t.TempDir(), name+"-chroot")
		if err := os.MkdirAll(chrootDir, 0755); err != nil {
			t.Fatalf("MkdirAll chroot: %v", err)
		}
		chrootDirs[name] = chrootDir
		infos = append(infos, seeder.SeederInfo{
			Name:        name,
			Dir:         seederDir,
			ChrootExec:  filepath.Join(seederDir, "chroot.sh"),
			PrepperExec: filepath.Join(seederDir, "prepper.sh"),
		})
	}
	return baseDir, infos, chrootDirs
}

// fakeCgroupRoot creates a temp directory that looks like a cgroup v2 mount
// point (has cgroup.subtree_control with memory+cpuset), suitable for use as CgroupRoot in tests.
func fakeCgroupRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cgroup.subtree_control"), []byte("memory cpuset"), 0644); err != nil {
		t.Fatalf("create fake subtree_control: %v", err)
	}
	return root
}

// parallelTestSeeder wraps MockSeeder to return per-seeder params and
// track execution order.
type parallelTestSeeder struct {
	*seeder.MockSeeder
	paramsMap map[string]*seeder.SeederParams
	mu        *sync.Mutex
	executed  *[]string
}

func (p *parallelTestSeeder) ParseSeederParams(info seeder.SeederInfo) (*seeder.SeederParams, error) {
	params, ok := p.paramsMap[info.Name]
	if !ok {
		return nil, fmt.Errorf("unknown seeder: %s", info.Name)
	}
	return params, nil
}

func (p *parallelTestSeeder) Seed(opts *seeder.SeedOptions) error {
	p.mu.Lock()
	*p.executed = append(*p.executed, opts.Info.Name)
	p.mu.Unlock()
	return p.MockSeeder.Seed(opts)
}

func TestSeedsParallelUsedWhenConfigured(t *testing.T) {
	requireSeedsTools(t)
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	names := []string{"00-bedrock"}
	_, infos, chrootDirs := setupParallelSeeders(t, names)

	paramsMap := map[string]*seeder.SeederParams{
		"00-bedrock": {
			ChrootName:         "bedrock-20260228",
			ChrootsDir:         "/mnt/chroots",
			PreferredChrootDir: chrootDirs["00-bedrock"],
		},
	}

	var mu sync.Mutex
	executed := make([]string, 0)

	origNewSeeder := newSeeder
	newSeeder = func(_ config.IConfig, opts *seeder.NewSeederOptions) (seeder.ISeeder, error) {
		sd := seeder.DefaultMockSeeder()
		return &parallelTestSeeder{
			MockSeeder: sd,
			paramsMap:  paramsMap,
			mu:         &mu,
			executed:   &executed,
		}, nil
	}
	defer func() { newSeeder = origNewSeeder }()

	det := &seeder.MockSeederDetector{Detect_: infos}
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.PrivateGitRepoPath": {t.TempDir()},
			"Seeder.Parallelism":          {"2"},
		},
	}

	cmd := NewSeedsCommand()
	cmd.det = det
	cmd.cfg = cfg
	cmd.cgroupRoot = fakeCgroupRoot(t)
	qa, _ := validation.New(cfg)
	cmd.qa = qa
	cmd.StartUI()
	if err := cmd.parseArgs([]string{}); err != nil {
		t.Fatalf("parseArgs: %v", err)
	}

	// runSeeds should use the parallel path when Parallelism > 1.
	err := cmd.runSeeds()
	if err != nil {
		t.Fatalf("runSeeds: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(executed) != 1 || executed[0] != "00-bedrock" {
		t.Errorf("Expected [00-bedrock], got %v", executed)
	}

	// Verify runSeeds() closed worker cgroup FDs even when invoked directly
	// without RunWithGuard.
	fdEntries, fdErr := os.ReadDir("/proc/self/fd")
	if fdErr == nil {
		cgRoot := cmd.cgroupRoot
		if resolved, err := filepath.EvalSymlinks(cgRoot); err == nil {
			cgRoot = resolved
		}
		cgPrefix := cgRoot + string(os.PathSeparator)
		for _, e := range fdEntries {
			if target, lerr := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); lerr == nil && strings.HasPrefix(target, cgPrefix) {
				t.Errorf("runSeeds() leaked open cgroup FD %s -> %s", e.Name(), target)
			}
		}
	}
}
