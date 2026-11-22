package seeder

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"matrixos/vector/lib/config"
)

// newDetectTestDetector sets up a SeederDetector with mock config pointing at a real
// temp directory tree so Detect can walk the filesystem.
func newDetectTestDetector(t *testing.T, seedersDir, chrootSeedersDir string) *SeederDetector {
	t.Helper()
	cfg := &config.MockConfig{Items: map[string][]string{
		"Seeder.SeedersDir":              {seedersDir},
		"Seeder.ChrootSeedersDir":        {chrootSeedersDir},
		"Seeder.SeederDisabledFileName":  {"__disabled__"},
		"Seeder.ChrootExecutableName":    {"chroot.sh"},
		"Seeder.PrepperExecutableName":   {"prepper.sh"},
		"Seeder.PostBuildExecutableName": {"poster.sh"},
	}, Bools: map[string]bool{}}
	d, err := NewSeederDetector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d.SetStderr(&bytes.Buffer{})
	return d
}

// createSeederDir creates a seeder directory with executable chroot.sh and prepper.sh.
func createSeederDir(t *testing.T, seedersDir, name string) string {
	t.Helper()
	dir := filepath.Join(seedersDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"chroot.sh", "prepper.sh"} {
		p := filepath.Join(dir, f)
		if err := os.WriteFile(p, []byte("#!/bin/bash\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// ---------- NewSeederDetector ----------

func TestNewSeederDetector_NilConfig(t *testing.T) {
	_, err := NewSeederDetector(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

// ---------- Detect ----------

func TestDetect_FindsAllSeeders(t *testing.T) {
	seedersDir := t.TempDir()
	chrootSeedersDir := t.TempDir()
	createSeederDir(t, seedersDir, "00-bedrock")
	createSeederDir(t, seedersDir, "10-server")

	d := newDetectTestDetector(t, seedersDir, chrootSeedersDir)
	got, err := d.Detect(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 seeders, got %d", len(got))
	}
	if got[0].Name != "00-bedrock" {
		t.Errorf("first seeder name = %q, want %q", got[0].Name, "00-bedrock")
	}
	if got[1].Name != "10-server" {
		t.Errorf("second seeder name = %q, want %q", got[1].Name, "10-server")
	}
}

func TestDetect_ChrootChrootExec(t *testing.T) {
	seedersDir := t.TempDir()
	chrootSeedersDir := t.TempDir()
	createSeederDir(t, seedersDir, "00-bedrock")
	createSeederDir(t, seedersDir, "10-server")

	d := newDetectTestDetector(t, seedersDir, chrootSeedersDir)
	got, err := d.Detect(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 seeders, got %d", len(got))
	}
	expectedChrootExec := filepath.Join(seedersDir, "00-bedrock", "chroot.sh")
	if got[0].ChrootExec != expectedChrootExec {
		t.Errorf("first seeder ChrootExec = %q, want %q",
			got[0].ChrootExec, expectedChrootExec,
		)
	}
	expectedChrootChrootExec := filepath.Join(chrootSeedersDir, "00-bedrock", "chroot.sh")
	if got[0].ChrootChrootExec != expectedChrootChrootExec {
		t.Errorf("first seeder ChrootChrootExec = %q, want %q",
			got[0].ChrootChrootExec, expectedChrootChrootExec,
		)
	}
}

func TestDetect_SkipsDisabledSeeder(t *testing.T) {
	seedersDir := t.TempDir()
	chrootSeedersDir := t.TempDir()
	dir := createSeederDir(t, seedersDir, "00-bedrock")
	createSeederDir(t, seedersDir, "10-server")

	// Disable 00-bedrock.
	if err := os.WriteFile(filepath.Join(dir, "__disabled__"), []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	d := newDetectTestDetector(t, seedersDir, chrootSeedersDir)
	got, err := d.Detect(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 seeder, got %d", len(got))
	}
	if got[0].Name != "10-server" {
		t.Errorf("seeder name = %q, want %q", got[0].Name, "10-server")
	}
}

func TestDetect_SkipFilter(t *testing.T) {
	seedersDir := t.TempDir()
	chrootSeedersDir := t.TempDir()
	createSeederDir(t, seedersDir, "00-bedrock")
	createSeederDir(t, seedersDir, "10-server")

	skip := func(name string) bool { return name == "10-server" }

	d := newDetectTestDetector(t, seedersDir, chrootSeedersDir)
	got, err := d.Detect(skip, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 seeder, got %d", len(got))
	}
	if got[0].Name != "00-bedrock" {
		t.Errorf("seeder name = %q, want %q", got[0].Name, "00-bedrock")
	}
}

func TestDetect_OnlyFilter(t *testing.T) {
	seedersDir := t.TempDir()
	chrootSeedersDir := t.TempDir()
	createSeederDir(t, seedersDir, "00-bedrock")
	createSeederDir(t, seedersDir, "10-server")

	only := func(name string) bool { return name == "10-server" }

	d := newDetectTestDetector(t, seedersDir, chrootSeedersDir)
	got, err := d.Detect(nil, only)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 seeder, got %d", len(got))
	}
	if got[0].Name != "10-server" {
		t.Errorf("seeder name = %q, want %q", got[0].Name, "10-server")
	}
}

func TestDetect_SkipTakesPrecedenceOverOnly(t *testing.T) {
	seedersDir := t.TempDir()
	chrootSeedersDir := t.TempDir()
	createSeederDir(t, seedersDir, "10-server")

	skip := func(name string) bool { return name == "10-server" }
	only := func(name string) bool { return name == "10-server" }

	d := newDetectTestDetector(t, seedersDir, chrootSeedersDir)
	got, err := d.Detect(skip, only)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 seeders, got %d", len(got))
	}
}

func TestDetect_SkipsDirWithoutChrootExec(t *testing.T) {
	seedersDir := t.TempDir()
	chrootSeedersDir := t.TempDir()
	// Create a directory without chroot.sh.
	dir := filepath.Join(seedersDir, "99-nochroot")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prepper.sh"), []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatal(err)
	}

	d := newDetectTestDetector(t, seedersDir, chrootSeedersDir)
	got, err := d.Detect(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 seeders, got %d", len(got))
	}
}

func TestDetect_ErrorNonExecutableChroot(t *testing.T) {
	seedersDir := t.TempDir()
	chrootSeedersDir := t.TempDir()
	dir := filepath.Join(seedersDir, "00-bedrock")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// chroot.sh exists but is not executable.
	if err := os.WriteFile(filepath.Join(dir, "chroot.sh"), []byte("#!/bin/bash\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prepper.sh"), []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatal(err)
	}

	d := newDetectTestDetector(t, seedersDir, chrootSeedersDir)
	_, err := d.Detect(nil, nil)
	if err == nil {
		t.Fatal("expected error for non-executable chroot.sh")
	}
}

func TestDetect_ErrorMissingPrepper(t *testing.T) {
	seedersDir := t.TempDir()
	chrootSeedersDir := t.TempDir()
	dir := filepath.Join(seedersDir, "00-bedrock")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	// chroot.sh exists and is executable, but prepper.sh is missing.
	if err := os.WriteFile(filepath.Join(dir, "chroot.sh"), []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatal(err)
	}

	d := newDetectTestDetector(t, seedersDir, chrootSeedersDir)
	_, err := d.Detect(nil, nil)
	if err == nil {
		t.Fatal("expected error for missing prepper.sh")
	}
}

func TestDetect_ErrorNonExecutablePrepper(t *testing.T) {
	seedersDir := t.TempDir()
	chrootSeedersDir := t.TempDir()
	dir := filepath.Join(seedersDir, "00-bedrock")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "chroot.sh"), []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatal(err)
	}
	// prepper.sh exists but is not executable.
	if err := os.WriteFile(filepath.Join(dir, "prepper.sh"), []byte("#!/bin/bash\n"), 0644); err != nil {
		t.Fatal(err)
	}

	d := newDetectTestDetector(t, seedersDir, chrootSeedersDir)
	_, err := d.Detect(nil, nil)
	if err == nil {
		t.Fatal("expected error for non-executable prepper.sh")
	}
}

func TestDetect_SeedersDirNotDirectory(t *testing.T) {
	cfg := &config.MockConfig{Items: map[string][]string{
		"Seeder.SeedersDir":             {"/nonexistent/path"},
		"Seeder.SeederDisabledFileName": {"__disabled__"},
		"Seeder.ChrootExecutableName":   {"chroot.sh"},
		"Seeder.PrepperExecutableName":  {"prepper.sh"},
	}, Bools: map[string]bool{}}
	d, err := NewSeederDetector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d.SetStderr(&bytes.Buffer{})

	_, err = d.Detect(nil, nil)
	if err == nil {
		t.Fatal("expected error for nonexistent seeders dir")
	}
}

func TestDetect_EmptyDir(t *testing.T) {
	seedersDir := t.TempDir()
	chrootSeedersDir := t.TempDir()
	d := newDetectTestDetector(t, seedersDir, chrootSeedersDir)

	got, err := d.Detect(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 seeders, got %d", len(got))
	}
}

func TestDetect_PopulatesSeederInfo(t *testing.T) {
	seedersDir := t.TempDir()
	chrootSeedersDir := t.TempDir()
	createSeederDir(t, seedersDir, "00-bedrock")

	d := newDetectTestDetector(t, seedersDir, chrootSeedersDir)
	got, err := d.Detect(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 seeder, got %d", len(got))
	}
	si := got[0]
	if si.Name != "00-bedrock" {
		t.Errorf("Name = %q, want %q", si.Name, "00-bedrock")
	}
	wantDir := filepath.Join(seedersDir, "00-bedrock")
	if si.Dir != wantDir {
		t.Errorf("Dir = %q, want %q", si.Dir, wantDir)
	}
	if si.ChrootExec != filepath.Join(wantDir, "chroot.sh") {
		t.Errorf("ChrootExec = %q, want %q", si.ChrootExec, filepath.Join(wantDir, "chroot.sh"))
	}
	if si.PrepperExec != filepath.Join(wantDir, "prepper.sh") {
		t.Errorf("PrepperExec = %q, want %q", si.PrepperExec, filepath.Join(wantDir, "prepper.sh"))
	}
}

func TestDetect_SkipsNonDirectoryEntries(t *testing.T) {
	seedersDir := t.TempDir()
	chrootSeedersDir := t.TempDir()
	// Create a regular file in the seeders dir (not a subdirectory).
	if err := os.WriteFile(filepath.Join(seedersDir, "README.md"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	createSeederDir(t, seedersDir, "00-bedrock")

	d := newDetectTestDetector(t, seedersDir, chrootSeedersDir)
	got, err := d.Detect(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 seeder, got %d", len(got))
	}
}

// ---------- Naming helpers ----------

func TestSeederExecToName(t *testing.T) {
	got := SeederExecToName("/build/seeders/00-bedrock/chroot.sh")
	want := "00-bedrock"
	if got != want {
		t.Errorf("SeederExecToName() = %q, want %q", got, want)
	}
}

func TestSeederNameWithoutOrderPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"00-bedrock", "bedrock"},
		{"10-server", "server"},
		{"20-gnome", "gnome"},
		{"nodash", "nodash"},
		{"-leadingdash", "leadingdash"},
		{"00-multi-dash", "multi-dash"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := SeederNameWithoutOrderPrefix(tt.input)
			if got != tt.want {
				t.Errorf("SeederNameWithoutOrderPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
