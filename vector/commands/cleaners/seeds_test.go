package cleaners

import (
	"errors"
	"fmt"
	"matrixos/vector/lib/config"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestSeedsCleaner_Name(t *testing.T) {
	cleaner := &SeedsCleaner{}
	if cleaner.Name() != "seeds" {
		t.Errorf("Expected name to be 'seeds', but got '%s'", cleaner.Name())
	}
}

func TestSeedsCleaner_Init(t *testing.T) {
	cleaner := &SeedsCleaner{}
	mockCfg := &config.MockConfig{Items: map[string][]string{}}
	err := cleaner.Init(mockCfg, os.Stdout, os.Stderr)
	if err != nil {
		t.Errorf("Init should not return an error, but got: %v", err)
	}
	if cleaner.cfg != mockCfg {
		t.Error("cfg should be initialized with the provided config")
	}
}

func TestSeedsCleaner_isDryRun(t *testing.T) {
	tests := []struct {
		name     string
		dryRun   string
		expected bool
		wantErr  bool
	}{
		{"DryRunTrue", "true", true, false},
		{"DryRunFalse", "false", false, false},
		{"DryRunNotSet", "", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCfg := &config.MockConfig{Items: map[string][]string{}}
			if tt.dryRun != "" {
				mockCfg.Items["SeedsCleaner.DryRun"] = []string{tt.dryRun}
			}
			cleaner := &SeedsCleaner{baseCleaner: baseCleaner{cfg: mockCfg}}
			got, err := cleaner.isDryRun()
			if (err != nil) != tt.wantErr {
				t.Errorf("isDryRun() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("isDryRun() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestSeedsCleaner_isDryRun_Error(t *testing.T) {
	errCfg := &config.ErrConfig{Err: errors.New("config error")}
	cleaner := &SeedsCleaner{baseCleaner: baseCleaner{cfg: errCfg}}
	_, err := cleaner.isDryRun()
	if err == nil {
		t.Error("isDryRun() should return an error when config fails")
	}
}

func TestSeedsCleaner_MinAmountOfSeeds(t *testing.T) {
	tests := []struct {
		name     string
		val      string
		expected int
		wantErr  bool
	}{
		{"Valid", "5", 5, false},
		{"ValidZero", "0", 0, false},
		{"ValidLarge", "100", 100, false},
		{"Invalid", "abc", 0, true},
		{"NotSet", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCfg := &config.MockConfig{Items: map[string][]string{}}
			if tt.name != "NotSet" {
				mockCfg.Items["SeedsCleaner.MinAmountOfSeeds"] = []string{tt.val}
			}
			cleaner := &SeedsCleaner{baseCleaner: baseCleaner{cfg: mockCfg}}
			got, err := cleaner.MinAmountOfSeeds()
			if (err != nil) != tt.wantErr {
				t.Errorf("MinAmountOfSeeds() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("MinAmountOfSeeds() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestSeedsCleaner_MinAmountOfSeeds_Error(t *testing.T) {
	errCfg := &config.ErrConfig{Err: errors.New("config error")}
	cleaner := &SeedsCleaner{baseCleaner: baseCleaner{cfg: errCfg}}
	_, err := cleaner.MinAmountOfSeeds()
	if err == nil {
		t.Error("MinAmountOfSeeds() should return an error when config fails")
	}
}

func TestChrootDirNamePattern(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		matches bool
		groups  []string // [full, name, date]
	}{
		{"ValidBedrock", "bedrock-20260101", true, []string{"bedrock-20260101", "bedrock", "20260101"}},
		{"ValidServer", "server-20261231", true, []string{"server-20261231", "server", "20261231"}},
		{"ValidGnome", "gnome-20260305", true, []string{"gnome-20260305", "gnome", "20260305"}},
		{"ValidUnderscores", "my_seed-20260101", true, []string{"my_seed-20260101", "my_seed", "20260101"}},
		{"ValidNumbers", "seed123-20260101", true, []string{"seed123-20260101", "seed123", "20260101"}},
		{"NoDate", "bedrock-abc", false, nil},
		{"NoDash", "bedrock20260101", false, nil},
		{"Empty", "", false, nil},
		{"ShortDate", "bedrock-2026010", false, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matches := ChrootDirNamePattern.FindStringSubmatch(tt.input)
			if tt.matches && matches == nil {
				t.Errorf("Expected %q to match ChrootDirNamePattern", tt.input)
			}
			if !tt.matches && matches != nil {
				t.Errorf("Expected %q to NOT match ChrootDirNamePattern, got %v", tt.input, matches)
			}
			if tt.matches && matches != nil {
				if len(matches) < 3 {
					t.Fatalf("Expected at least 3 groups, got %d: %v", len(matches), matches)
				}
				for i, expected := range tt.groups {
					if matches[i] != expected {
						t.Errorf("Group %d: expected %q, got %q", i, expected, matches[i])
					}
				}
			}
		})
	}
}

func TestSortChrootDirs(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{
			"ChronologicalOrder",
			[]string{
				"/chroots/bedrock-20260103",
				"/chroots/bedrock-20260101",
				"/chroots/bedrock-20260102",
			},
			[]string{
				"/chroots/bedrock-20260101",
				"/chroots/bedrock-20260102",
				"/chroots/bedrock-20260103",
			},
		},
		{
			"AlreadySorted",
			[]string{
				"/chroots/bedrock-20260101",
				"/chroots/bedrock-20260102",
				"/chroots/bedrock-20260103",
			},
			[]string{
				"/chroots/bedrock-20260101",
				"/chroots/bedrock-20260102",
				"/chroots/bedrock-20260103",
			},
		},
		{
			"ReverseOrder",
			[]string{
				"/chroots/bedrock-20260103",
				"/chroots/bedrock-20260102",
				"/chroots/bedrock-20260101",
			},
			[]string{
				"/chroots/bedrock-20260101",
				"/chroots/bedrock-20260102",
				"/chroots/bedrock-20260103",
			},
		},
		{
			"SingleElement",
			[]string{"/chroots/bedrock-20260101"},
			[]string{"/chroots/bedrock-20260101"},
		},
		{
			"Empty",
			[]string{},
			[]string{},
		},
		{
			"MixedSeeds",
			[]string{
				"/chroots/gnome-20260201",
				"/chroots/bedrock-20260101",
				"/chroots/gnome-20260101",
				"/chroots/bedrock-20260201",
			},
			[]string{
				"/chroots/bedrock-20260101",
				"/chroots/gnome-20260101",
				"/chroots/gnome-20260201",
				"/chroots/bedrock-20260201",
			},
		},
		{
			"NonMatchingEntries",
			[]string{
				"/chroots/invalid",
				"/chroots/bedrock-20260101",
				"/chroots/other",
			},
			// Non-matching entries keep relative order (stable behavior not guaranteed,
			// but they compare as equal, so the sort should not rearrange them).
			[]string{
				"/chroots/invalid",
				"/chroots/bedrock-20260101",
				"/chroots/other",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sortChrootDirs(tt.input)
			if len(got) != len(tt.expected) {
				t.Fatalf("Length mismatch: got %d, want %d", len(got), len(tt.expected))
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("Index %d: got %q, want %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}

// --- Integration tests for SeedsCleaner.Run() ---

// createTestSeeder sets up a single seeder directory with the required
// executable stubs, params.sh script, and chroot directories.
func createTestSeeder(
	t *testing.T,
	seedersDir, chrootsDir string,
	seederName, seedName string,
	chrootDirNames []string,
) {
	t.Helper()

	seederDir := filepath.Join(seedersDir, seederName)
	if err := os.MkdirAll(seederDir, 0755); err != nil {
		t.Fatalf("Failed to create seeder dir %s: %v", seederDir, err)
	}

	// Create executable stubs required by the detector.
	for _, name := range []string{"chroot.sh", "prepper.sh"} {
		path := filepath.Join(seederDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/bash\n"), 0755); err != nil {
			t.Fatalf("Failed to create %s: %v", name, err)
		}
	}

	// Create the chroot directories.
	for _, dir := range chrootDirNames {
		path := filepath.Join(chrootsDir, dir)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatalf("Failed to create chroot dir %s: %v", dir, err)
		}
	}

	// Determine the latest chroot dir (last when sorted lexicographically).
	latest := ""
	if len(chrootDirNames) > 0 {
		sorted := slices.Clone(chrootDirNames)
		slices.Sort(sorted)
		latest = filepath.Join(chrootsDir, sorted[len(sorted)-1])
	}

	// Create params.sh — a sourceable bash script that sets up
	// the variables and functions expected by Seeder.ParseSeederParams.
	paramsScript := fmt.Sprintf(`#!/bin/bash
SEEDER_CHROOT_NAME="%s"
SEEDER_CHROOTS_DIR="%s"
PREFERRED_SEEDER_CHROOT_DIR="%s"
SEEDER_DEPENDS=""

%s_params.find_latest_chroot_dir() {
    echo "%s"
}

%s_params.find_all_chroot_dirs() {
    for d in "${SEEDER_CHROOTS_DIR}"/%s-*; do
        if [ -d "$d" ]; then
            echo "$d"
        fi
    done
}
`, seedName, chrootsDir, latest, seedName, latest, seedName, seedName)

	paramsSh := filepath.Join(seederDir, "params.sh")
	if err := os.WriteFile(paramsSh, []byte(paramsScript), 0755); err != nil {
		t.Fatalf("Failed to create params.sh: %v", err)
	}
}

// buildSeedsCleanerConfig returns a MockConfig wired for SeedsCleaner.Run().
func buildSeedsCleanerConfig(
	seedersDir, chrootSeedersDir, devDir, dryRun, minSeeds string,
) *config.MockConfig {
	return &config.MockConfig{Items: map[string][]string{
		"Seeder.SeedersDir":                         {seedersDir},
		"Seeder.ChrootSeedersDir":                   {chrootSeedersDir},
		"Seeder.SeederDisabledFileName":             {".disabled"},
		"Seeder.ChrootExecutableName":               {"chroot.sh"},
		"Seeder.PrepperExecutableName":              {"prepper.sh"},
		"Seeder.PostBuildExecutableName":            {"poster.sh"},
		"Seeder.ParamsExecutableName":               {"params.sh"},
		"Seeder.ChrootSeedersPhasesStateDir":        {"/build/.seeders_phases"},
		"Seeder.ChrootSeederDoneFlagFileNamePrefix": {"seeder.complete"},
		"Seeder.SeedsVersioningCadence":             {"weekly"},
		"matrixOS.Root":                             {devDir},
		"matrixOS.DefaultRoot":                      {"/matrixos"},
		"matrixOS.OverlayGitRepo":                   {"https://example.com/overlay.git"},
		"matrixOS.DefaultPrivateGitRepoPath":         {"/matrixos/private"},
		"SeedsCleaner.DryRun":                       {dryRun},
		"SeedsCleaner.MinAmountOfSeeds":             {minSeeds},
	}}
}

func TestSeedsCleaner_Run_Integration(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available, skipping integration test")
	}

	tests := []struct {
		name            string
		dryRun          string
		minSeeds        string
		chrootDirs      []string
		expectedRemoved []string
		expectedKept    []string
		wantErr         bool
	}{
		{
			name:            "RealRun_RemovesOldest",
			dryRun:          "false",
			minSeeds:        "2",
			chrootDirs:      []string{"bedrock-20260101", "bedrock-20260102", "bedrock-20260103", "bedrock-20260104"},
			expectedRemoved: []string{"bedrock-20260101", "bedrock-20260102"},
			expectedKept:    []string{"bedrock-20260103", "bedrock-20260104"},
		},
		{
			name:            "DryRun_KeepsAll",
			dryRun:          "true",
			minSeeds:        "2",
			chrootDirs:      []string{"bedrock-20260101", "bedrock-20260102", "bedrock-20260103", "bedrock-20260104"},
			expectedRemoved: []string{},
			expectedKept:    []string{"bedrock-20260101", "bedrock-20260102", "bedrock-20260103", "bedrock-20260104"},
		},
		{
			name:            "WithinMinimum_KeepsAll",
			dryRun:          "false",
			minSeeds:        "4",
			chrootDirs:      []string{"bedrock-20260101", "bedrock-20260102", "bedrock-20260103", "bedrock-20260104"},
			expectedRemoved: []string{},
			expectedKept:    []string{"bedrock-20260101", "bedrock-20260102", "bedrock-20260103", "bedrock-20260104"},
		},
		{
			name:            "ExactlyMinimum_KeepsAll",
			dryRun:          "false",
			minSeeds:        "2",
			chrootDirs:      []string{"bedrock-20260101", "bedrock-20260102"},
			expectedRemoved: []string{},
			expectedKept:    []string{"bedrock-20260101", "bedrock-20260102"},
		},
		{
			name:            "Min1_RemovesAllButNewest",
			dryRun:          "false",
			minSeeds:        "1",
			chrootDirs:      []string{"bedrock-20260101", "bedrock-20260102", "bedrock-20260103"},
			expectedRemoved: []string{"bedrock-20260101", "bedrock-20260102"},
			expectedKept:    []string{"bedrock-20260103"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			seedersDir := filepath.Join(tempDir, "seeders")
			chrootsDir := filepath.Join(tempDir, "chroots")
			chrootsSeedersTempDir := t.TempDir()
			chrootsSeedersDir := filepath.Join(chrootsSeedersTempDir, "build", "seeders")
			devDir := filepath.Join(tempDir, "dev")

			for _, d := range []string{seedersDir, chrootsDir, chrootsSeedersDir, devDir} {
				if err := os.MkdirAll(d, 0755); err != nil {
					t.Fatalf("Failed to create dir %s: %v", d, err)
				}
			}

			createTestSeeder(t, seedersDir, chrootsDir, "00-bedrock", "bedrock", tt.chrootDirs)

			mockCfg := buildSeedsCleanerConfig(
				seedersDir,
				chrootsSeedersDir,
				devDir,
				tt.dryRun,
				tt.minSeeds,
			)

			cleaner := &SeedsCleaner{}
			if err := cleaner.Init(mockCfg, os.Stdout, os.Stderr); err != nil {
				t.Fatalf("Init failed: %v", err)
			}

			err := cleaner.Run()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}

			for _, dir := range tt.expectedRemoved {
				path := filepath.Join(chrootsDir, dir)
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("Directory %s should have been removed", dir)
				}
			}

			for _, dir := range tt.expectedKept {
				path := filepath.Join(chrootsDir, dir)
				if _, err := os.Stat(path); os.IsNotExist(err) {
					t.Errorf("Directory %s should have been kept", dir)
				}
			}
		})
	}
}

func TestSeedsCleaner_Run_Integration_MultipleSeeders(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available, skipping integration test")
	}

	tempDir := t.TempDir()
	seedersDir := filepath.Join(tempDir, "seeders")
	chrootsDir := filepath.Join(tempDir, "chroots")
	devDir := filepath.Join(tempDir, "dev")
	chrootsSeedersTempDir := t.TempDir()
	chrootsSeedersDir := filepath.Join(chrootsSeedersTempDir, "build", "seeders")

	for _, d := range []string{seedersDir, chrootsDir, chrootsSeedersDir, devDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("Failed to create dir %s: %v", d, err)
		}
	}

	// bedrock: 4 chroot dirs → with min=2 the 2 oldest should be removed.
	createTestSeeder(t, seedersDir, chrootsDir, "00-bedrock", "bedrock",
		[]string{"bedrock-20260101", "bedrock-20260102", "bedrock-20260103", "bedrock-20260104"})

	// server: only 2 chroot dirs → at min=2 all should be kept.
	createTestSeeder(t, seedersDir, chrootsDir, "10-server", "server",
		[]string{"server-20260201", "server-20260202"})

	mockCfg := buildSeedsCleanerConfig(seedersDir, chrootsSeedersDir, devDir, "false", "2")

	cleaner := &SeedsCleaner{}
	if err := cleaner.Init(mockCfg, os.Stdout, os.Stderr); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if err := cleaner.Run(); err != nil {
		t.Fatalf("Run() failed: %v", err)
	}

	// bedrock: oldest 2 removed, newest 2 kept.
	for _, dir := range []string{"bedrock-20260101", "bedrock-20260102"} {
		path := filepath.Join(chrootsDir, dir)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("Directory %s should have been removed", dir)
		}
	}
	for _, dir := range []string{"bedrock-20260103", "bedrock-20260104"} {
		path := filepath.Join(chrootsDir, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Directory %s should have been kept", dir)
		}
	}

	// server: both dirs kept.
	for _, dir := range []string{"server-20260201", "server-20260202"} {
		path := filepath.Join(chrootsDir, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("Directory %s should have been kept", dir)
		}
	}
}

func TestSeedsCleaner_Run_Integration_NoSeeders(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available, skipping integration test")
	}

	tempDir := t.TempDir()
	seedersDir := filepath.Join(tempDir, "seeders")
	devDir := filepath.Join(tempDir, "dev")
	chrootsSeedersTempDir := t.TempDir()
	chrootsSeedersDir := filepath.Join(chrootsSeedersTempDir, "build", "seeders")

	for _, d := range []string{seedersDir, chrootsSeedersDir, devDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("Failed to create dir %s: %v", d, err)
		}
	}

	mockCfg := buildSeedsCleanerConfig(seedersDir, chrootsSeedersDir, devDir, "false", "2")

	cleaner := &SeedsCleaner{}
	if err := cleaner.Init(mockCfg, os.Stdout, os.Stderr); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	if err := cleaner.Run(); err != nil {
		t.Fatalf("Run() should succeed with no seeders, got: %v", err)
	}
}
