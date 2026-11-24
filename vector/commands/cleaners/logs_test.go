package cleaners

import (
	"matrixos/vector/lib/config"
	"os"
	"path"
	"testing"
	"time"
)

func TestLogsCleaner_Name(t *testing.T) {
	cleaner := &LogsCleaner{}
	if cleaner.Name() != "logs" {
		t.Errorf("Expected name to be 'logs', but got '%s'", cleaner.Name())
	}
}

func TestLogsCleaner_Init(t *testing.T) {
	cleaner := &LogsCleaner{}
	mockCfg := &config.MockConfig{Items: map[string][]string{}}
	err := cleaner.Init(mockCfg, os.Stdout, os.Stderr)
	if err != nil {
		t.Errorf("Init should not return an error, but got: %v", err)
	}
	if cleaner.cfg != mockCfg {
		t.Error("cfg should be initialized with the provided config")
	}
}

func TestLogsCleaner_isDryRun(t *testing.T) {
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
				mockCfg.Items["LogsCleaner.DryRun"] = []string{tt.dryRun}
			}
			cleaner := &LogsCleaner{baseCleaner: baseCleaner{cfg: mockCfg}}
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

func TestLogsCleaner_getLogsDir(t *testing.T) {
	tests := []struct {
		name     string
		logsDir  string
		expected string
		wantErr  bool
	}{
		{"LogsDirSet", "/tmp/logs", "/tmp/logs", false},
		{"LogsDirNotSet", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCfg := &config.MockConfig{Items: map[string][]string{}}
			if tt.logsDir != "" {
				mockCfg.Items["matrixOS.LogsDir"] = []string{tt.logsDir}
			}
			cleaner := &LogsCleaner{baseCleaner: baseCleaner{cfg: mockCfg}}
			got, err := cleaner.getLogsDir()
			if (err != nil) != tt.wantErr {
				t.Errorf("getLogsDir() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("getLogsDir() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestLogsCleaner_Run(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "test-logs-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	logsDir := path.Join(tempDir, "logs")

	tests := []struct {
		name          string
		dryRun        string
		logsDir       string
		wantErr       bool
		expectOldFile bool
		expectNewFile bool
	}{
		{"DryRun", "true", logsDir, false, true, true},
		{"RealRun", "false", logsDir, false, false, true},
		{"LogsDirNotSet", "", "", true, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			weeklyBuilderDir := path.Join(logsDir, "weekly-builder")
			err = os.MkdirAll(weeklyBuilderDir, 0755)
			if err != nil {
				t.Fatalf("Failed to create test directories: %v", err)
			}

			oldFile, err := os.Create(path.Join(weeklyBuilderDir, "old.log"))
			if err != nil {
				t.Fatalf("Failed to create old log file: %v", err)
			}
			defer oldFile.Close()

			twoMonthsAgo := time.Now().Add(-2 * 30 * 24 * time.Hour)
			err = os.Chtimes(oldFile.Name(), twoMonthsAgo, twoMonthsAgo)
			if err != nil {
				t.Fatalf("Failed to change old file mtime: %v", err)
			}

			newFile, err := os.Create(path.Join(weeklyBuilderDir, "new.log"))
			if err != nil {
				t.Fatalf("Failed to create new log file: %v", err)
			}
			defer newFile.Close()

			mockCfg := &config.MockConfig{Items: map[string][]string{}}
			if tt.dryRun != "" {
				mockCfg.Items["LogsCleaner.DryRun"] = []string{tt.dryRun}
			}
			if tt.logsDir != "" {
				mockCfg.Items["matrixOS.LogsDir"] = []string{tt.logsDir}
			}

			cleaner := &LogsCleaner{}
			err = cleaner.Init(mockCfg, os.Stdout, os.Stderr)
			if err != nil {
				t.Fatalf("Init failed: %v", err)
			}
			err = cleaner.Run()

			if (err != nil) != tt.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tt.wantErr)
			}

			// Check if files exist
			_, errOld := os.Stat(oldFile.Name())
			if tt.expectOldFile && os.IsNotExist(errOld) {
				t.Errorf("Expected old file to exist, but it was deleted")
			}
			if !tt.expectOldFile && errOld == nil {
				t.Errorf("Expected old file to be deleted, but it exists")
			}

			_, errNew := os.Stat(newFile.Name())
			if tt.expectNewFile && os.IsNotExist(errNew) {
				t.Errorf("Expected new file to exist, but it was deleted")
			}
			if !tt.expectNewFile && errNew == nil {
				t.Errorf("Expected new file to be deleted, but it exists")
			}
		})
	}
}
