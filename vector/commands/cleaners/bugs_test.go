package cleaners

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"matrixos/vector/lib/config"
)

func TestCleanDirectoryBasedOnMtime_NonNotExistStatErrorDoesNotPanic(t *testing.T) {
	// Putting a regular file as a parent path component triggers ENOTDIR from os.Stat
	// even when running as root, which is NOT os.ErrNotExist.
	notADir := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(notADir, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(notADir, "child")

	var stdout, stderr bytes.Buffer
	err := cleanDirectoryBasedOnMtime(badPath, 24*time.Hour, true, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for ENOTDIR stat failure, got nil")
	}
}

func TestImagesCleaner_NonNotExistStatErrorDoesNotPanic(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(notADir, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(notADir, "child")

	cfg := &config.MockConfig{Items: map[string][]string{
		"Imager.ImagesDir":                {badPath},
		"ImagesCleaner.MinAmountOfImages": {"2"},
		"ImagesCleaner.DryRun":            {"true"},
	}}
	c := &ImagesCleaner{}
	var stdout, stderr bytes.Buffer
	if err := c.Init(cfg, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := c.Run(); err == nil {
		t.Fatal("expected error for ENOTDIR stat failure, got nil")
	}
}

func TestCleaners_RejectNegativeMinAmount(t *testing.T) {
	imgCfg := &config.MockConfig{Items: map[string][]string{
		"ImagesCleaner.MinAmountOfImages": {"-1"},
	}}
	imgCleaner := &ImagesCleaner{baseCleaner: baseCleaner{cfg: imgCfg}}
	if _, err := imgCleaner.MinAmountOfImages(); err == nil {
		t.Error("MinAmountOfImages accepted negative value -1")
	}

	seedCfg := &config.MockConfig{Items: map[string][]string{
		"SeedsCleaner.MinAmountOfSeeds": {"-1"},
	}}
	seedCleaner := &SeedsCleaner{baseCleaner: baseCleaner{cfg: seedCfg}}
	if _, err := seedCleaner.MinAmountOfSeeds(); err == nil {
		t.Error("MinAmountOfSeeds accepted negative value -1")
	}
}

func TestSeedsCleaner_DryRunDoesNotDeleteRegularFile(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	tempDir := t.TempDir()
	seedersDir := filepath.Join(tempDir, "seeders")
	chrootsDir := filepath.Join(tempDir, "chroots")
	chrootsSeedersDir := filepath.Join(t.TempDir(), "build", "seeders")
	devDir := filepath.Join(tempDir, "dev")
	for _, d := range []string{seedersDir, chrootsDir, chrootsSeedersDir, devDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	// Create 1 real dir (newest) and 1 regular file (oldest) so the regular file
	// is selected into markedForRemoval with minSeeds=1.
	createTestSeeder(t, seedersDir, chrootsDir, "00-bedrock", "bedrock", []string{"bedrock-20260102"})
	regFile := filepath.Join(chrootsDir, "bedrock-20260101")
	if err := os.WriteFile(regFile, []byte("stray-file"), 0644); err != nil {
		t.Fatal(err)
	}
	// Update params.sh so find_all_chroot_dirs includes the regular file too.
	paramsSh := filepath.Join(seedersDir, "00-bedrock", "params.sh")
	data, err := os.ReadFile(paramsSh)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.ReplaceAll(data, []byte(`if [ -d "$d" ]; then`), []byte(`if [ -e "$d" ]; then`))
	if err := os.WriteFile(paramsSh, updated, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := buildSeedsCleanerConfig(seedersDir, chrootsSeedersDir, devDir, "true", "1")
	cleaner := &SeedsCleaner{}
	var stdout, stderr bytes.Buffer
	if err := cleaner.Init(cfg, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if err := cleaner.Run(); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	if _, err := os.Stat(regFile); err != nil {
		t.Fatalf("DryRun=true deleted regular file %s: %v", regFile, err)
	}
}

func TestSeedsCleaner_RejectsHostRootPath(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	tempDir := t.TempDir()
	seedersDir := filepath.Join(tempDir, "seeders")
	chrootsDir := filepath.Join(tempDir, "chroots")
	chrootsSeedersDir := filepath.Join(t.TempDir(), "build", "seeders")
	devDir := filepath.Join(tempDir, "dev")
	for _, d := range []string{seedersDir, chrootsDir, chrootsSeedersDir, devDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	createTestSeeder(t, seedersDir, chrootsDir, "00-bedrock", "bedrock", []string{"bedrock-20260102"})
	// Inject "/" as an older candidate directory in params.sh and run with DryRun=true
	// (CheckDirIsRoot runs before dryRun and before RemoveAll).
	paramsSh := filepath.Join(seedersDir, "00-bedrock", "params.sh")
	data, err := os.ReadFile(paramsSh)
	if err != nil {
		t.Fatal(err)
	}
	injected := bytes.ReplaceAll(data, []byte(`    for d in "${SEEDER_CHROOTS_DIR}"/bedrock-*; do`),
		[]byte("    echo \"/\"\n    for d in \"${SEEDER_CHROOTS_DIR}\"/bedrock-*; do"))
	if err := os.WriteFile(paramsSh, injected, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := buildSeedsCleanerConfig(seedersDir, chrootsSeedersDir, devDir, "true", "1")
	cleaner := &SeedsCleaner{}
	var stdout, stderr bytes.Buffer
	if err := cleaner.Init(cfg, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	err = cleaner.Run()
	if err == nil {
		t.Fatal("expected error rejecting host root '/', got nil")
	}
	if !strings.Contains(err.Error(), "IS MAPPED TO HOST ROOT") {
		t.Fatalf("expected CheckDirIsRoot rejection error, got: %v", err)
	}
}

func TestSortChrootDirs_StrictWeakOrderAndBasenameMatching(t *testing.T) {
	t.Run("NonMatchingDoesNotTrapNewestAtStart", func(t *testing.T) {
		dirs := []string{
			"/chroots/bedrock-20260103",
			"/chroots/invalid",
			"/chroots/bedrock-20260101",
		}
		got := sortChrootDirs(dirs)
		if got[len(got)-1] != "/chroots/bedrock-20260103" {
			t.Fatalf("newest chroot was not sorted last (would be marked for deletion): got %v", got)
		}
		if got[len(got)-2] != "/chroots/bedrock-20260101" {
			t.Fatalf("dated chroots not sorted chronologically: got %v", got)
		}
	})

	t.Run("MatchesBasenameNotAncestorDir", func(t *testing.T) {
		dirs := []string{
			"/build/run-20260101/chroots/bedrock-20260105",
			"/build/run-20260101/chroots/bedrock-20260102",
		}
		got := sortChrootDirs(dirs)
		if got[0] != "/build/run-20260101/chroots/bedrock-20260102" ||
			got[1] != "/build/run-20260101/chroots/bedrock-20260105" {
			t.Fatalf("sortChrootDirs matched ancestor date instead of basename: got %v", got)
		}
	})
}
