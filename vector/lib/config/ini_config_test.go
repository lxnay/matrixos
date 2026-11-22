package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestIniConfig_Load_Expansion(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "matrixos-test-*.conf")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Define absolute paths for roots to ensure deterministic tests
	rootPath := "/tmp/matrixos-root"
	defaultRootPath := "/tmp/matrixos-default-root"
	confRootPath := "/tmp/matrixos-conf-root"
	artifactsRootPath := "/tmp/matrixos-artifacts"
	privateRepoPath := "/tmp/matrixos-private"
	defaultPrivateRepoPath := "/tmp/matrixos-default-private"

	configContent := `
[matrixOS]
Root=` + rootPath + `
DefaultRoot=` + defaultRootPath + `
ConfRoot=` + confRootPath + `
ArtifactsRoot=` + artifactsRootPath + `
PrivateGitRepoPath=` + privateRepoPath + `
DefaultPrivateGitRepoPath=` + defaultPrivateRepoPath + `
LogsDir=/var/log/matrixos
LocksDir=locks

[Seeder]
DownloadsDir=out/seeder/downloads
DistfilesDir=out/seeder/distfiles
BinpkgsDir=out/seeder/binpkgs
PortageReposDir=out/seeder/repos
GpgKeysDir=out/seeder/gpg-keys
SecureBootPrivateKey=sb-keys/db.key
SecureBootPublicKey=sb-keys/db.pem
SecureBootKekPublicKey=sb-keys/KEK.pem
DefaultSecureBootPrivateKey=sb-keys/db.key
DefaultSecureBootPublicKey=sb-keys/db.pem
LocksDir=locks/seeder
SeedersDir=build/seeders
ChrootSeedersDir=build/seeders

[Releaser]
LocksDir=locks/releaser
HooksDir=release/hooks

[Imager]
LocksDir=locks/imager
ImagesDir=out/images
MountDir=out/mounts
HooksDir=image/hooks
TestsDir=image/tests

[Ostree]
RepoDir=ostree/repo
DevGpgHomeDir=gpg-home
GpgPrivateKey=keys/priv.key
GpgPublicKey=keys/pub.key
GpgOfficialPublicKey=pubkeys/ostree.gpg
`
	if _, err := tmpFile.WriteString(configContent); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	params := configFromPathParams{
		ConfigPath:    tmpFile.Name(),
		DefaultRoot:   rootPath,
		ConfRoot:      confRootPath,
		ArtifactsRoot: artifactsRootPath,
	}
	cfg, err := newIniConfigFromPath(&params)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Helper to check values
	check := func(key, expected string) {
		val, err := cfg.GetItem(key)
		if err != nil {
			t.Errorf("GetItem(%q) returned error: %v", key, err)
			return
		}
		if val != expected {
			t.Errorf("Key %q: expected %q, got %q", key, expected, val)
		}
	}

	check("matrixOS.Root", rootPath)
	check("matrixOS.ConfRoot", confRootPath)
	check("matrixOS.ArtifactsRoot", artifactsRootPath)

	// Relative to matrixOS.Root
	check("matrixOS.PrivateGitRepoPath", privateRepoPath)
	check("matrixOS.DefaultPrivateGitRepoPath", defaultPrivateRepoPath)
	check("matrixOS.LocksDir", filepath.Join(rootPath, "locks"))
	check("matrixOS.LogsDir", "/var/log/matrixos")

	check("Seeder.SeedersDir", filepath.Join(rootPath, "build/seeders"))
	check("Seeder.LocksDir", filepath.Join(rootPath, "locks/seeder"))
	// Relative to matrixOS.ArtifactsRoot
	check("Seeder.DownloadsDir", filepath.Join(artifactsRootPath, "out/seeder/downloads"))
	check("Seeder.DistfilesDir", filepath.Join(artifactsRootPath, "out/seeder/distfiles"))
	check("Seeder.BinpkgsDir", filepath.Join(artifactsRootPath, "out/seeder/binpkgs"))
	check("Seeder.PortageReposDir", filepath.Join(artifactsRootPath, "out/seeder/repos"))
	check("Seeder.GpgKeysDir", filepath.Join(artifactsRootPath, "out/seeder/gpg-keys"))

	check("Releaser.LocksDir", filepath.Join(rootPath, "locks/releaser"))
	check("Releaser.HooksDir", filepath.Join(rootPath, "release/hooks"))

	check("Imager.LocksDir", filepath.Join(rootPath, "locks/imager"))
	check("Imager.ImagesDir", filepath.Join(artifactsRootPath, "out/images"))
	check("Imager.MountDir", filepath.Join(artifactsRootPath, "out/mounts"))
	// Relative to matrixOS.ConfRoot
	check("Imager.HooksDir", filepath.Join(confRootPath, "image/hooks"))
	check("Imager.TestsDir", filepath.Join(confRootPath, "image/tests"))

	check("Ostree.DevGpgHomeDir", filepath.Join(artifactsRootPath, "gpg-home"))
	check("Ostree.GpgOfficialPublicKey", filepath.Join(confRootPath, "pubkeys/ostree.gpg"))
	check("Ostree.RepoDir", filepath.Join(rootPath, "ostree/repo"))

	// Relative to PrivateGitRepoPath
	check("Seeder.SecureBootPrivateKey", filepath.Join(privateRepoPath, "sb-keys/db.key"))
	check("Seeder.SecureBootPublicKey", filepath.Join(privateRepoPath, "sb-keys/db.pem"))
	check("Seeder.SecureBootKekPublicKey", filepath.Join(privateRepoPath, "sb-keys/KEK.pem"))
	check("Ostree.GpgPrivateKey", filepath.Join(privateRepoPath, "keys/priv.key"))
	check("Ostree.GpgPublicKey", filepath.Join(privateRepoPath, "keys/pub.key"))
	// Relative to DefaultPrivateGitRepoPath
	check("Seeder.DefaultSecureBootPrivateKey", filepath.Join(defaultPrivateRepoPath, "sb-keys/db.key"))
	check("Seeder.DefaultSecureBootPublicKey", filepath.Join(defaultPrivateRepoPath, "sb-keys/db.pem"))

	// Relative to DefaultRoot
	check("matrixOS.DefaultRoot", defaultRootPath)
	check("Seeder.ChrootSeedersDir", filepath.Join(defaultRootPath, "build/seeders"))
}

func TestIniConfig_Defaults(t *testing.T) {
	// Test that defaults are applied when keys are missing
	tmpFile, err := os.CreateTemp("", "matrixos-test-defaults-*.conf")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	params := configFromPathParams{
		ConfigPath:    tmpFile.Name(),
		DefaultRoot:   filepath.Dir(tmpFile.Name()),
		ConfRoot:      filepath.Dir(tmpFile.Name()),
		ArtifactsRoot: filepath.Dir(tmpFile.Name()),
	}

	cfg, err := newIniConfigFromPath(&params)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// matrixOS.Root should default to CWD (absolute)
	val, err := cfg.GetItem("matrixOS.Root")
	if err != nil {
		t.Errorf("GetItem(matrixOS.Root) error: %v", err)
	}
	if !filepath.IsAbs(val) {
		t.Errorf("Default matrixOS.Root should be absolute, got %q", val)
	}
}

func TestIniConfig_GetItem_LastValue(t *testing.T) {
	// Create an IniConfig manually with multiple values for a key
	cfg := &IniConfig{
		cfg: map[string][]string{
			"Test.Key": {"value1", "value2", "value3"},
		},
	}

	val, err := cfg.GetItem("Test.Key")
	if err != nil {
		t.Fatalf("GetItem returned error: %v", err)
	}

	expected := "value3"
	if val != expected {
		t.Errorf("GetItem returned %q, expected %q (last value)", val, expected)
	}
}

func TestIniConfig_GenerateSubConfigs(t *testing.T) {
	// Create a temporary directory for the test
	tmpDir, err := os.MkdirTemp("", "matrixos-test-subconfig-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create the main config file
	configPath := filepath.Join(tmpDir, "matrixos.conf")
	configContent := `
[Section1]
Key1=Value1
Key2=Value2

[Section2]
Key3=Value3
`
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write main config: %v", err)
	}

	// Create the subconfig directory
	subConfigDir := configPath + ".d"
	if err := os.Mkdir(subConfigDir, 0755); err != nil {
		t.Fatalf("Failed to create subconfig dir: %v", err)
	}

	// Create an override config file
	overridePath := filepath.Join(subConfigDir, "00-override.conf")
	overrideContent := `
[Section1]
Key1=OverrideValue1
KeyNew=ValueNew
`
	if err := os.WriteFile(overridePath, []byte(overrideContent), 0644); err != nil {
		t.Fatalf("Failed to write override config: %v", err)
	}

	// Create another override config file
	override2Path := filepath.Join(subConfigDir, "10-override.conf")
	override2Content := `
[Section2]
Key3=OverrideValue3
`
	if err := os.WriteFile(override2Path, []byte(override2Content), 0644); err != nil {
		t.Fatalf("Failed to write override config 2: %v", err)
	}

	// Create a non-conf file to ensure it's ignored
	ignoredPath := filepath.Join(subConfigDir, "README.md")
	if err := os.WriteFile(ignoredPath, []byte("Ignore me"), 0644); err != nil {
		t.Fatalf("Failed to write ignored file: %v", err)
	}

	params := configFromPathParams{
		ConfigPath:    configPath,
		DefaultRoot:   tmpDir,
		ConfRoot:      tmpDir,
		ArtifactsRoot: tmpDir,
	}
	cfg, err := newIniConfigFromPath(&params)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	if err := cfg.Load(); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Helper to check values
	check := func(key, expected string) {
		val, err := cfg.GetItem(key)
		if err != nil {
			t.Errorf("GetItem(%q) returned error: %v", key, err)
			return
		}
		if val != expected {
			t.Errorf("Key %q: expected %q, got %q", key, expected, val)
		}
	}

	// Check main config values
	check("Section1.Key2", "Value2")

	// Check overridden values
	// Since the implementation uses a map[string][]string and appends,
	// GetItem returns the last one, which should be the override.
	check("Section1.Key1", "OverrideValue1")
	check("Section2.Key3", "OverrideValue3")

	// Check new value from subconfig
	check("Section1.KeyNew", "ValueNew")
}

func TestIniConfig_GenerateParent(t *testing.T) {
	// Create a temporary directory for the test
	tmpDir, err := os.MkdirTemp("", "matrixos-test-parent-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create the parent config file
	parentContent := `
[matrixOS]
Root=/parent/root
LogsDir=logs-from-parent

[Seeder]
DownloadsDir=parent-downloads
BinpkgsDir=parent-binpkgs
`
	parentPath := filepath.Join(tmpDir, "parent.conf")
	if err := os.WriteFile(parentPath, []byte(parentContent), 0644); err != nil {
		t.Fatalf("Failed to write parent config: %v", err)
	}

	// Create the main config file that references the parent
	mainContent := `
[matrixOS]
ParentConfig=parent.conf
Root=/main/root
ArtifactsRoot=/main/root
LogsDir=logs-from-main

[Seeder]
DownloadsDir=main-downloads
`
	mainPath := filepath.Join(tmpDir, "matrixos.conf")
	if err := os.WriteFile(mainPath, []byte(mainContent), 0644); err != nil {
		t.Fatalf("Failed to write main config: %v", err)
	}

	params := configFromPathParams{
		ConfigPath:    mainPath,
		DefaultRoot:   tmpDir,
		ConfRoot:      tmpDir,
		ArtifactsRoot: tmpDir,
	}
	cfg, err := newIniConfigFromPath(&params)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	check := func(key, expected string) {
		t.Helper()
		val, err := cfg.GetItem(key)
		if err != nil {
			t.Errorf("GetItem(%q) returned error: %v", key, err)
			return
		}
		if val != expected {
			t.Errorf("Key %q: expected %q, got %q", key, expected, val)
		}
	}

	// Main config values should override parent values (last value wins).
	check("matrixOS.Root", "/main/root")
	check("matrixOS.LogsDir", "/main/root/logs-from-main")
	check("Seeder.DownloadsDir", "/main/root/main-downloads")

	// Values only present in the parent should still be accessible.
	check("Seeder.BinpkgsDir", "/main/root/parent-binpkgs")
}

func TestIniConfig_GenerateParent_MissingParentFile(t *testing.T) {
	// When ParentConfig references a file that doesn't exist, Load should
	// succeed (generateParent silently skips missing files).
	tmpDir, err := os.MkdirTemp("", "matrixos-test-parent-missing-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mainContent := `
[matrixOS]
ParentConfig=nonexistent.conf
Root=/some/root
`
	mainPath := filepath.Join(tmpDir, "matrixos.conf")
	if err := os.WriteFile(mainPath, []byte(mainContent), 0644); err != nil {
		t.Fatalf("Failed to write main config: %v", err)
	}

	params := configFromPathParams{
		ConfigPath:    mainPath,
		DefaultRoot:   tmpDir,
		ConfRoot:      tmpDir,
		ArtifactsRoot: tmpDir,
	}
	cfg, err := newIniConfigFromPath(&params)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load should succeed when parent file is missing, got: %v", err)
	}

	val, err := cfg.GetItem("matrixOS.Root")
	if err != nil {
		t.Fatalf("GetItem(matrixOS.Root) error: %v", err)
	}
	if val != "/some/root" {
		t.Errorf("Expected /some/root, got %q", val)
	}
}

func TestIniConfig_GenerateParent_NoParentConfig(t *testing.T) {
	// When no ParentConfig key exists, generateParent is a no-op.
	tmpDir, err := os.MkdirTemp("", "matrixos-test-parent-nokey-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mainContent := `
[matrixOS]
Root=/some/root
LogsDir=logs
`
	mainPath := filepath.Join(tmpDir, "matrixos.conf")
	if err := os.WriteFile(mainPath, []byte(mainContent), 0644); err != nil {
		t.Fatalf("Failed to write main config: %v", err)
	}

	params := configFromPathParams{
		ConfigPath:    mainPath,
		DefaultRoot:   tmpDir,
		ConfRoot:      tmpDir,
		ArtifactsRoot: tmpDir,
	}
	cfg, err := newIniConfigFromPath(&params)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	check := func(key, expected string) {
		t.Helper()
		val, err := cfg.GetItem(key)
		if err != nil {
			t.Errorf("GetItem(%q) error: %v", key, err)
			return
		}
		if val != expected {
			t.Errorf("Key %q: expected %q, got %q", key, expected, val)
		}
	}

	check("matrixOS.Root", "/some/root")
	check("matrixOS.LogsDir", "/some/root/logs")
}

func TestIniConfig_GenerateParent_WithSubConfigs(t *testing.T) {
	// Test the full chain: parent → main → sub-configs.
	// The priority order is: parent (lowest) < main < sub-configs (highest).
	tmpDir, err := os.MkdirTemp("", "matrixos-test-parent-sub-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Parent config: provides base values
	parentContent := `
[matrixOS]
Root=/chain/root

[Seeder]
DownloadsDir=parent-downloads
BinpkgsDir=parent-binpkgs
DistfilesDir=parent-distfiles
`
	parentPath := filepath.Join(tmpDir, "parent.conf")
	if err := os.WriteFile(parentPath, []byte(parentContent), 0644); err != nil {
		t.Fatalf("Failed to write parent config: %v", err)
	}

	// Main config: overrides some parent values, references parent
	mainContent := `
[matrixOS]
ParentConfig=parent.conf
Root=/chain/root
ArtifactsRoot=/chain/root

[Seeder]
DownloadsDir=main-downloads
`
	mainPath := filepath.Join(tmpDir, "matrixos.conf")
	if err := os.WriteFile(mainPath, []byte(mainContent), 0644); err != nil {
		t.Fatalf("Failed to write main config: %v", err)
	}

	// Sub-config directory
	subConfigDir := mainPath + ".d"
	if err := os.Mkdir(subConfigDir, 0755); err != nil {
		t.Fatalf("Failed to create subconfig dir: %v", err)
	}

	// Sub-config: overrides a value from main
	subContent := `
[Seeder]
DownloadsDir=sub-downloads
`
	subPath := filepath.Join(subConfigDir, "00-override.conf")
	if err := os.WriteFile(subPath, []byte(subContent), 0644); err != nil {
		t.Fatalf("Failed to write sub config: %v", err)
	}

	params := configFromPathParams{
		ConfigPath:    mainPath,
		DefaultRoot:   tmpDir,
		ConfRoot:      tmpDir,
		ArtifactsRoot: tmpDir,
	}
	cfg, err := newIniConfigFromPath(&params)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	check := func(key, expected string) {
		t.Helper()
		val, err := cfg.GetItem(key)
		if err != nil {
			t.Errorf("GetItem(%q) error: %v", key, err)
			return
		}
		if val != expected {
			t.Errorf("Key %q: expected %q, got %q", key, expected, val)
		}
	}

	// Sub-config wins over main for DownloadsDir (last value wins via GetItem)
	check("Seeder.DownloadsDir", "/chain/root/sub-downloads")

	// Only in parent, inherited through the chain
	check("Seeder.BinpkgsDir", "/chain/root/parent-binpkgs")
	check("Seeder.DistfilesDir", "/chain/root/parent-distfiles")

	// setVal preserves history: parent, main, and sub-config entries are all kept.
	allDownloads, err := cfg.GetItems("Seeder.DownloadsDir")
	if err != nil {
		t.Fatalf("GetItems(Seeder.DownloadsDir) error: %v", err)
	}
	if len(allDownloads) != 3 {
		t.Errorf("Expected 3 history entries for Seeder.DownloadsDir, got %d: %v",
			len(allDownloads), allDownloads)
	}
}

func TestIniConfig_GenerateParent_ParentOverrideOrder(t *testing.T) {
	// Verify that the main config values take precedence over parent for
	// the same keys: parent is loaded first, then main appends on top.
	tmpDir, err := os.MkdirTemp("", "matrixos-test-parent-order-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	parentContent := `
[matrixOS]
Root=/parent/root

[Seeder]
DownloadsDir=from-parent
`
	if err := os.WriteFile(filepath.Join(tmpDir, "parent.conf"), []byte(parentContent), 0644); err != nil {
		t.Fatalf("Failed to write parent config: %v", err)
	}

	mainContent := `
[matrixOS]
ParentConfig=parent.conf
Root=/main/root
ArtifactsRoot=/main/root

[Seeder]
DownloadsDir=from-main
`
	mainPath := filepath.Join(tmpDir, "main.conf")
	if err := os.WriteFile(mainPath, []byte(mainContent), 0644); err != nil {
		t.Fatalf("Failed to write main config: %v", err)
	}

	params := configFromPathParams{
		ConfigPath:    mainPath,
		DefaultRoot:   tmpDir,
		ConfRoot:      tmpDir,
		ArtifactsRoot: tmpDir,
	}
	cfg, err := newIniConfigFromPath(&params)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// GetItem should return the main config value (last appended wins)
	val, err := cfg.GetItem("Seeder.DownloadsDir")
	if err != nil {
		t.Fatalf("GetItem error: %v", err)
	}
	expected := "/main/root/from-main"
	if val != expected {
		t.Errorf("Expected %q (main overrides parent), got %q", expected, val)
	}

	// setVal preserves history: both parent and main entries are kept.
	allVals, err := cfg.GetItems("Seeder.DownloadsDir")
	if err != nil {
		t.Fatalf("GetItems error: %v", err)
	}
	if len(allVals) != 2 {
		t.Fatalf("Expected 2 values (parent + main), got %d: %v", len(allVals), allVals)
	}
	// Last entry (main) should be expanded; first entry (parent) stays raw.
	if allVals[1] != expected {
		t.Errorf("Last value should be expanded main (%q), got %q", expected, allVals[1])
	}
}

func TestSearchPaths(t *testing.T) {
	// Create a temporary directory structure to test search path discovery
	tmpDir, err := os.MkdirTemp("", "matrixos-test-searchpaths-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create .matrixos marker file in the root of temp dir
	if err := os.WriteFile(filepath.Join(tmpDir, ".matrixos"), []byte(""), 0644); err != nil {
		t.Fatalf("Failed to create .matrixos file: %v", err)
	}

	// Create conf directory
	confDir := filepath.Join(tmpDir, "conf")
	if err := os.Mkdir(confDir, 0755); err != nil {
		t.Fatalf("Failed to create conf dir: %v", err)
	}

	// Create a subdirectory to run the test from
	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdir: %v", err)
	}

	// Save current WD and deferred restore
	originalWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current working directory: %v", err)
	}
	// We need to change back
	defer func() {
		_ = os.Chdir(originalWd)
	}()

	// Change to subdir
	if err := os.Chdir(subDir); err != nil {
		t.Fatalf("Failed to change working directory: %v", err)
	}

	// helper to verify if we found our expected path
	found := false
	expectedPath, _ := filepath.EvalSymlinks(confDir)

	paths := searchPaths(BaseConfigFileName)
	for _, sp := range paths {
		// Resolve symlinks just in case tmp dir has them
		evalDirPath, err := filepath.EvalSymlinks(sp.dirPath)
		if err != nil {
			evalDirPath = sp.dirPath
		}

		if evalDirPath == expectedPath {
			found = true
			if sp.fileName != BaseConfigFileName {
				t.Errorf("Expected fileName %q, got %q", BaseConfigFileName, sp.fileName)
			}

			// Evaluated comparison for root as well
			evalRoot, _ := filepath.EvalSymlinks(sp.defaultRoot)
			evalTmp, _ := filepath.EvalSymlinks(tmpDir)
			if evalRoot != evalTmp {
				t.Errorf("Expected defaultRoot %q, got %q", evalTmp, evalRoot)
			}

			evalConfRoot, _ := filepath.EvalSymlinks(sp.confRoot)
			if evalConfRoot != evalTmp {
				t.Errorf("Expected confRoot %q, got %q", evalTmp, evalConfRoot)
			}

			evalArtifactsRoot, _ := filepath.EvalSymlinks(sp.artifactsRoot)
			if evalArtifactsRoot != evalTmp {
				t.Errorf("Expected artifactsRoot %q, got %q", evalTmp, evalArtifactsRoot)
			}
			break
		}
	}

	if !found {
		t.Errorf("searchPaths did not find expected configuration directory: %s", expectedPath)
		for i, p := range paths {
			t.Logf("Search path %d: %+v", i, p)
		}
	}
}

func TestIniConfig_ConcurrentReadWrite(t *testing.T) {
	// Verify that concurrent GetItem, GetItems, Clone, and AddOverlay
	// calls do not race. Run with -race to catch data races.
	cfg := &IniConfig{
		cfg: map[string][]string{
			"A.Key": {"val1", "val2"},
			"B.Key": {"x"},
		},
	}

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Concurrent readers
	for i := 0; i < goroutines/2; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = cfg.GetItem("A.Key")
				_, _ = cfg.GetItems("B.Key")
				_ = cfg.Clone()
			}
		}()
	}

	// Concurrent writers (AddOverlay)
	for i := 0; i < goroutines/2; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = cfg.AddOverlay(map[string][]string{
					"A.Key": {"new"},
				})
			}
		}()
	}

	wg.Wait()

	// Sanity: the key should still be readable after all the mutations.
	val, err := cfg.GetItem("A.Key")
	if err != nil {
		t.Fatalf("GetItem after concurrent access: %v", err)
	}
	if val != "new" {
		t.Errorf("Expected last overlay value %q, got %q", "new", val)
	}
}

func TestIniConfig_Clone(t *testing.T) {
	original := &IniConfig{
		sp: &searchPath{
			fileName:      "test.conf",
			dirPath:       "/etc/test",
			confRoot:      "/etc/test/conf",
			artifactsRoot: "/var/cache/test",
			defaultRoot:   "/opt/test",
		},
		cfg: map[string][]string{
			"A.Key": {"v1", "v2"},
			"B.Key": {"x"},
		},
	}

	clone := original.Clone()
	cloneConcrete := clone.(*IniConfig)

	// Clone must not be the same pointer.
	if clone == original {
		t.Fatal("Clone returned the same pointer")
	}
	if cloneConcrete.sp == original.sp {
		t.Fatal("Clone shares the same searchPath pointer")
	}

	// Values should match.
	for _, key := range []string{"A.Key", "B.Key"} {
		origVal, err := original.GetItem(key)
		if err != nil {
			t.Errorf("Original missing key %q: %v", key, err)
			continue
		}
		cloneVal, err := clone.GetItem(key)
		if err != nil {
			t.Errorf("Clone missing key %q: %v", key, err)
			continue
		}
		if origVal != cloneVal {
			t.Errorf("Key %q: original=%q clone=%q", key, origVal, cloneVal)
		}
	}

	// Mutating the clone must not affect the original.
	if err := clone.AddOverlay(map[string][]string{"A.Key": {"mutated"}}); err != nil {
		t.Fatalf("clone.AddOverlay error: %v", err)
	}

	cloneVal, err := clone.GetItem("A.Key")
	if err != nil {
		t.Fatalf("clone.GetItem(A.Key) error: %v", err)
	}
	if cloneVal != "mutated" {
		t.Errorf("Clone A.Key should be %q after overlay, got %q", "mutated", cloneVal)
	}
	origVal, err := original.GetItem("A.Key")
	if err != nil {
		t.Fatalf("original.GetItem(A.Key) error: %v", err)
	}
	if origVal != "v2" {
		t.Errorf("Original A.Key should still be %q, got %q", "v2", origVal)
	}

	// Mutating original must not affect the clone.
	if err := original.AddOverlay(map[string][]string{"B.Key": {"orig-mutated"}}); err != nil {
		t.Fatalf("original.AddOverlay error: %v", err)
	}
	cloneBVal, err := clone.GetItem("B.Key")
	if err != nil {
		t.Fatalf("clone.GetItem(B.Key) error: %v", err)
	}
	if cloneBVal != "x" {
		t.Errorf("Clone B.Key should still be %q, got %q", "x", cloneBVal)
	}
}

func TestIniConfig_Clone_Nil(t *testing.T) {
	var cfg *IniConfig
	clone := cfg.Clone()
	if clone != nil {
		t.Errorf("Clone of nil should be nil, got %v", clone)
	}
}

func TestIniConfig_Clone_Empty(t *testing.T) {
	cfg := &IniConfig{
		sp: &searchPath{
			fileName:      "a.conf",
			dirPath:       "/d",
			confRoot:      "/c",
			artifactsRoot: "/a",
			defaultRoot:   "/r",
		},
		cfg: map[string][]string{},
	}
	clone := cfg.Clone()
	if clone == nil {
		t.Fatal("Clone of empty config should not be nil")
	}
	cloneConcrete := clone.(*IniConfig)
	if cloneConcrete.sp == nil {
		t.Fatal("Clone searchPath should not be nil")
	}
	if cloneConcrete.sp.fileName != "a.conf" || cloneConcrete.sp.dirPath != "/d" || cloneConcrete.sp.defaultRoot != "/r" {
		t.Errorf("Clone searchPath mismatch: got %+v", cloneConcrete.sp)
	}
	if cloneConcrete.sp.confRoot != "/c" {
		t.Errorf("Clone confRoot mismatch: expected /c, got %q", cloneConcrete.sp.confRoot)
	}
	if cloneConcrete.sp.artifactsRoot != "/a" {
		t.Errorf("Clone artifactsRoot mismatch: expected /a, got %q", cloneConcrete.sp.artifactsRoot)
	}
	if len(cloneConcrete.cfg) != 0 {
		t.Errorf("Clone cfg should be empty, got %v", cloneConcrete.cfg)
	}
}

func TestIniConfig_NewFromPath_Validation(t *testing.T) {
	// nil params
	_, err := newIniConfigFromPath(nil)
	if err == nil {
		t.Fatal("Expected error for nil params")
	}

	// missing ConfigPath
	_, err = newIniConfigFromPath(&configFromPathParams{
		DefaultRoot:   "/r",
		ConfRoot:      "/c",
		ArtifactsRoot: "/a",
	})
	if err == nil {
		t.Fatal("Expected error for empty ConfigPath")
	}

	// missing DefaultRoot
	_, err = newIniConfigFromPath(&configFromPathParams{
		ConfigPath:    "/tmp/test.conf",
		ConfRoot:      "/c",
		ArtifactsRoot: "/a",
	})
	if err == nil {
		t.Fatal("Expected error for empty DefaultRoot")
	}

	// missing ConfRoot
	_, err = newIniConfigFromPath(&configFromPathParams{
		ConfigPath:    "/tmp/test.conf",
		DefaultRoot:   "/r",
		ArtifactsRoot: "/a",
	})
	if err == nil {
		t.Fatal("Expected error for empty ConfRoot")
	}

	// missing ArtifactsRoot
	_, err = newIniConfigFromPath(&configFromPathParams{
		ConfigPath:  "/tmp/test.conf",
		DefaultRoot: "/r",
		ConfRoot:    "/c",
	})
	if err == nil {
		t.Fatal("Expected error for empty ArtifactsRoot")
	}
}

func TestIniConfig_ArtifactsRoot_Expansion(t *testing.T) {
	// Verify that ArtifactsRoot-dependent paths expand against ArtifactsRoot
	// while Root-dependent and ConfRoot-dependent paths remain with their own base.
	tmpDir, err := os.MkdirTemp("", "matrixos-test-artifacts-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configContent := `
[matrixOS]
Root=/the/root
ConfRoot=/the/confroot
ArtifactsRoot=/the/artifacts
LogsDir=logs
LocksDir=locks

[Seeder]
LocksDir=locks/seeder
SeedersDir=build/seeders
DownloadsDir=seeder/downloads
DistfilesDir=seeder/distfiles
BinpkgsDir=seeder/binpkgs
PortageReposDir=seeder/repos
GpgKeysDir=seeder-gpg/

[Imager]
LocksDir=locks/imager
ImagesDir=images
MountDir=mounts
HooksDir=image/hooks
TestsDir=image/tests

[Ostree]
RepoDir=ostree/repo
DevGpgHomeDir=ostree-gpg/
GpgOfficialPublicKey=pubkeys/ostree.gpg

[Releaser]
LocksDir=locks/releaser
HooksDir=release/hooks
`
	configPath := filepath.Join(tmpDir, "matrixos.conf")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	params := configFromPathParams{
		ConfigPath:    configPath,
		DefaultRoot:   "/the/root",
		ConfRoot:      "/the/confroot",
		ArtifactsRoot: "/the/artifacts",
	}
	cfg, err := newIniConfigFromPath(&params)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	check := func(key, expected string) {
		t.Helper()
		val, err := cfg.GetItem(key)
		if err != nil {
			t.Errorf("GetItem(%q) error: %v", key, err)
			return
		}
		if val != expected {
			t.Errorf("Key %q: expected %q, got %q", key, expected, val)
		}
	}

	// Root-dependent
	check("matrixOS.LogsDir", "/the/root/logs")
	check("matrixOS.LocksDir", "/the/root/locks")
	check("Seeder.LocksDir", "/the/root/locks/seeder")
	check("Seeder.SeedersDir", "/the/root/build/seeders")
	check("Imager.LocksDir", "/the/root/locks/imager")
	check("Ostree.RepoDir", "/the/root/ostree/repo")
	check("Releaser.LocksDir", "/the/root/locks/releaser")
	check("Releaser.HooksDir", "/the/root/release/hooks")

	// ConfRoot-dependent
	check("Ostree.GpgOfficialPublicKey", "/the/confroot/pubkeys/ostree.gpg")
	check("Imager.HooksDir", "/the/confroot/image/hooks")
	check("Imager.TestsDir", "/the/confroot/image/tests")

	// ArtifactsRoot-dependent
	check("Seeder.DownloadsDir", "/the/artifacts/seeder/downloads")
	check("Seeder.DistfilesDir", "/the/artifacts/seeder/distfiles")
	check("Seeder.BinpkgsDir", "/the/artifacts/seeder/binpkgs")
	check("Seeder.PortageReposDir", "/the/artifacts/seeder/repos")
	check("Seeder.GpgKeysDir", "/the/artifacts/seeder-gpg")
	check("Imager.ImagesDir", "/the/artifacts/images")
	check("Imager.MountDir", "/the/artifacts/mounts")
	check("Ostree.DevGpgHomeDir", "/the/artifacts/ostree-gpg")
}

func TestIniConfig_ArtifactsRoot_Default(t *testing.T) {
	// When ArtifactsRoot is NOT set in the config file, it should default
	// to the searchPath's artifactsRoot value.
	tmpDir, err := os.MkdirTemp("", "matrixos-test-artifacts-default-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configContent := `
[matrixOS]
Root=/the/root

[Seeder]
DownloadsDir=seeder/downloads

[Imager]
ImagesDir=images
`
	configPath := filepath.Join(tmpDir, "matrixos.conf")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	params := configFromPathParams{
		ConfigPath:    configPath,
		DefaultRoot:   tmpDir,
		ConfRoot:      tmpDir,
		ArtifactsRoot: "/fallback/artifacts",
	}
	cfg, err := newIniConfigFromPath(&params)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	check := func(key, expected string) {
		t.Helper()
		val, err := cfg.GetItem(key)
		if err != nil {
			t.Errorf("GetItem(%q) error: %v", key, err)
			return
		}
		if val != expected {
			t.Errorf("Key %q: expected %q, got %q", key, expected, val)
		}
	}

	// ArtifactsRoot defaults to the searchPath.artifactsRoot
	check("matrixOS.ArtifactsRoot", "/fallback/artifacts")
	check("Seeder.DownloadsDir", "/fallback/artifacts/seeder/downloads")
	check("Imager.ImagesDir", "/fallback/artifacts/images")
}

func TestIniConfig_ArtifactsRoot_Relative(t *testing.T) {
	// When ArtifactsRoot is set to a relative path in the config file,
	// it should be expanded using smartRootify against searchPath.artifactsRoot.
	tmpDir, err := os.MkdirTemp("", "matrixos-test-artifacts-rel-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configContent := `
[matrixOS]
Root=/the/root
ArtifactsRoot=out/

[Seeder]
DownloadsDir=seeder/downloads
`
	configPath := filepath.Join(tmpDir, "matrixos.conf")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	params := configFromPathParams{
		ConfigPath:    configPath,
		DefaultRoot:   tmpDir,
		ConfRoot:      tmpDir,
		ArtifactsRoot: "/base/artifacts",
	}
	cfg, err := newIniConfigFromPath(&params)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	check := func(key, expected string) {
		t.Helper()
		val, err := cfg.GetItem(key)
		if err != nil {
			t.Errorf("GetItem(%q) error: %v", key, err)
			return
		}
		if val != expected {
			t.Errorf("Key %q: expected %q, got %q", key, expected, val)
		}
	}

	// Relative ArtifactsRoot "out/" should be resolved against the
	// searchPath.artifactsRoot which is "/base/artifacts".
	check("matrixOS.ArtifactsRoot", "/base/artifacts/out")
	check("Seeder.DownloadsDir", "/base/artifacts/out/seeder/downloads")
}

func TestIniConfig_AddOverlay(t *testing.T) {
	cfg := &IniConfig{
		cfg: map[string][]string{
			"Existing.Key": {"old"},
		},
	}

	err := cfg.AddOverlay(map[string][]string{
		"Existing.Key": {"new1", "new2"},
		"Brand.New":    {"fresh"},
	})
	if err != nil {
		t.Fatalf("AddOverlay returned error: %v", err)
	}

	// Existing key should have appended values; last wins.
	val, err := cfg.GetItem("Existing.Key")
	if err != nil {
		t.Fatalf("GetItem(Existing.Key) error: %v", err)
	}
	if val != "new2" {
		t.Errorf("Expected %q, got %q", "new2", val)
	}

	// Full history should contain all three values.
	all, err := cfg.GetItems("Existing.Key")
	if err != nil {
		t.Fatalf("GetItems(Existing.Key) error: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("Expected 3 values, got %d: %v", len(all), all)
	}

	// New key should be present.
	val, err = cfg.GetItem("Brand.New")
	if err != nil {
		t.Fatalf("GetItem(Brand.New) error: %v", err)
	}
	if val != "fresh" {
		t.Errorf("Expected %q, got %q", "fresh", val)
	}
}

func TestIniConfig_AddOverlay_NilReceiver(t *testing.T) {
	var cfg *IniConfig
	err := cfg.AddOverlay(map[string][]string{"K": {"V"}})
	if err == nil {
		t.Fatal("Expected error for nil receiver")
	}
}

func TestIniConfig_AddOverlay_NilOverlay(t *testing.T) {
	cfg := &IniConfig{cfg: map[string][]string{}}
	err := cfg.AddOverlay(nil)
	if err == nil {
		t.Fatal("Expected error for nil overlay")
	}
}

func TestIniConfig_AddOverlay_Multiple(t *testing.T) {
	cfg := &IniConfig{
		cfg: map[string][]string{
			"S.A": {"base"},
		},
	}

	// Apply two overlays in sequence.
	if err := cfg.AddOverlay(map[string][]string{"S.A": {"layer1"}}); err != nil {
		t.Fatalf("AddOverlay layer1 error: %v", err)
	}
	if err := cfg.AddOverlay(map[string][]string{"S.A": {"layer2"}}); err != nil {
		t.Fatalf("AddOverlay layer2 error: %v", err)
	}

	val, err := cfg.GetItem("S.A")
	if err != nil {
		t.Fatalf("GetItem(S.A) error: %v", err)
	}
	if val != "layer2" {
		t.Errorf("Expected last overlay %q, got %q", "layer2", val)
	}

	all, err := cfg.GetItems("S.A")
	if err != nil {
		t.Fatalf("GetItems(S.A) error: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("Expected 3 history entries, got %d: %v", len(all), all)
	}
}
