package cleaners

import (
	"matrixos/vector/lib/config"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestImagesCleaner_Name(t *testing.T) {
	cleaner := &ImagesCleaner{}
	if cleaner.Name() != "images" {
		t.Errorf("Expected name to be 'images', but got '%s'", cleaner.Name())
	}
}

func TestImagesCleaner_Init(t *testing.T) {
	cleaner := &ImagesCleaner{}
	mockCfg := &config.MockConfig{Items: map[string][]string{}}
	err := cleaner.Init(mockCfg, os.Stdout, os.Stderr)
	if err != nil {
		t.Errorf("Init should not return an error, but got: %v", err)
	}
	if cleaner.cfg != mockCfg {
		t.Error("cfg should be initialized with the provided config")
	}
}

func TestImagesCleaner_isDryRun(t *testing.T) {
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
				mockCfg.Items["ImagesCleaner.DryRun"] = []string{tt.dryRun}
			}
			cleaner := &ImagesCleaner{baseCleaner: baseCleaner{cfg: mockCfg}}
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

func TestImagesCleaner_MinAmountOfImages(t *testing.T) {
	tests := []struct {
		name     string
		val      string
		expected int
		wantErr  bool
	}{
		{"Valid", "5", 5, false},
		{"Invalid", "abc", 0, true},
		{"NotSet", "", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCfg := &config.MockConfig{Items: map[string][]string{}}
			if tt.name != "NotSet" {
				mockCfg.Items["ImagesCleaner.MinAmountOfImages"] = []string{tt.val}
			}
			cleaner := &ImagesCleaner{baseCleaner: baseCleaner{cfg: mockCfg}}
			got, err := cleaner.MinAmountOfImages()
			if (err != nil) != tt.wantErr {
				t.Errorf("MinAmountOfImages() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("MinAmountOfImages() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestImagesCleaner_Run(t *testing.T) {
	// Create dummy files
	files := []string{
		// Prefix 1: matrixos_test
		"matrixos_test-20260101.img.xz", // Oldest
		"matrixos_test-20260102.img.xz",
		"matrixos_test-20260103.img.xz",
		"matrixos_test-20260104.img.xz", // Newest
		// Associated files
		"matrixos_test-20260101.img.xz.asc",
		"matrixos_test-20260101.img.xz.sha256",

		// Prefix 2: matrixos_other (only 2 files, should keep all if min=2)
		"matrixos_other-20260101.img.xz",
		"matrixos_other-20260102.img.xz",

		// Invalid files
		"random_file.txt",
		"matrixos_invalid_date-202.img.xz",
	}

	tests := []struct {
		name            string
		dryRun          string
		minImages       string
		expectedDeleted []string
		expectedKept    []string
		wantErr         bool
	}{
		{
			name:      "RealRun_Min2",
			dryRun:    "false",
			minImages: "2",
			expectedDeleted: []string{
				"matrixos_test-20260101.img.xz",
				"matrixos_test-20260101.img.xz.asc",
				"matrixos_test-20260101.img.xz.sha256",
				"matrixos_test-20260102.img.xz",
			},
			expectedKept: []string{
				"matrixos_test-20260103.img.xz",
				"matrixos_test-20260104.img.xz",
				"matrixos_other-20260101.img.xz",
				"matrixos_other-20260102.img.xz",
				"random_file.txt",
			},
			wantErr: false,
		},
		{
			name:            "DryRun_Min2",
			dryRun:          "true",
			minImages:       "2",
			expectedDeleted: []string{}, // Nothing deleted
			expectedKept: []string{
				"matrixos_test-20260101.img.xz",
				"matrixos_test-20260102.img.xz",
				"matrixos_test-20260103.img.xz",
				"matrixos_test-20260104.img.xz",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subTempDir, err := os.MkdirTemp("", "test-images-sub-*")
			if err != nil {
				t.Fatalf("Failed to create sub temp dir: %v", err)
			}
			defer os.RemoveAll(subTempDir)

			for _, f := range files {
				path := filepath.Join(subTempDir, f)
				if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
					t.Fatalf("Failed to create file %s: %v", path, err)
				}
			}
			if err := os.Mkdir(filepath.Join(subTempDir, "subdir"), 0755); err != nil {
				t.Fatalf("Failed to create subdir: %v", err)
			}

			mockCfg := &config.MockConfig{Items: map[string][]string{}}
			mockCfg.Items["ImagesCleaner.DryRun"] = []string{tt.dryRun}
			mockCfg.Items["ImagesCleaner.MinAmountOfImages"] = []string{tt.minImages}
			mockCfg.Items["Imager.ImagesDir"] = []string{subTempDir}

			cleaner := &ImagesCleaner{}
			err = cleaner.Init(mockCfg, os.Stdout, os.Stderr)
			if err != nil {
				t.Fatalf("Init failed: %v", err)
			}
			err = cleaner.Run()

			if (err != nil) != tt.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}

			for _, f := range tt.expectedDeleted {
				path := filepath.Join(subTempDir, f)
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Errorf("File %s should have been deleted", f)
				}
			}

			for _, f := range tt.expectedKept {
				path := filepath.Join(subTempDir, f)
				if _, err := os.Stat(path); os.IsNotExist(err) {
					t.Errorf("File %s should have been kept", f)
				}
			}
		})
	}
}

func TestImageFileNamePattern(t *testing.T) {
	regex := regexp.MustCompile(ImageFileNamePattern)

	matching := []string{
		// .img.xz variants
		"matrixos_amd64_dev_gnome-20260320.img.xz",
		"matrixos_amd64_dev_gnome-20260320.img.xz.asc",
		"matrixos_amd64_dev_gnome-20260320.img.xz.sha256",
		// .img.qcow2 variants
		"matrixos_amd64_dev_server-20260318.img.qcow2",
		"matrixos_amd64_dev_server-20260318.img.qcow2.asc",
		"matrixos_amd64_dev_server-20260318.img.qcow2.sha256",
		// .img only (packages.txt, pubkey.asc)
		"matrixos_amd64_dev_server-20260223.img.packages.txt",
		"matrixos_amd64_dev_server-20260302.img.pubkey.asc",
		// bare .img
		"matrixos_amd64_dev_server-20260320.img",
		// .img.zstd variants
		"matrixos_amd64_dev_server-20260320.img.zstd",
		"matrixos_amd64_dev_server-20260320.img.zstd.asc",
		"matrixos_amd64_dev_server-20260320.img.zstd.sha256",
		// .img.gz variants
		"matrixos_amd64_dev_server-20260320.img.gz",
		"matrixos_amd64_dev_server-20260320.img.gz.asc",
		// .img.bz2 variants
		"matrixos_amd64_dev_server-20260320.img.bz2",
		"matrixos_amd64_dev_server-20260320.img.bz2.sha256",
	}

	notMatching := []string{
		"random_file.txt",
		"matrixos_amd64_dev_server-2026031.img.xz",
		"something_else-20260320.img.xz",
		"matrixos_amd64_dev_server-20260320.txt",
	}

	for _, name := range matching {
		t.Run("match_"+name, func(t *testing.T) {
			if !regex.MatchString(name) {
				t.Errorf("expected %q to match", name)
			}
		})
	}

	for _, name := range notMatching {
		t.Run("nomatch_"+name, func(t *testing.T) {
			if regex.MatchString(name) {
				t.Errorf("expected %q to NOT match", name)
			}
		})
	}
}
