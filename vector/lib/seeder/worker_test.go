package seeder

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
)

// newTestSeederWithConfig returns a Seeder with mock dependencies and
// the provided config, suitable for worker unit tests.
func newTestSeederWithConfig(cfg *config.MockConfig) *Seeder {
	mr := runner.NewMockRunner()
	s := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	s.SetStdout(&bytes.Buffer{})
	s.SetStderr(&bytes.Buffer{})
	return s
}

func workerTestConfig() *config.MockConfig {
	return &config.MockConfig{
		Items: map[string][]string{},
		Bools: map[string]bool{},
	}
}

func TestSeederDoneFlagFile(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["Seeder.ChrootSeederDoneFlagFileNamePrefix"] = []string{
		"seeder.complete",
	}
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{
		"/build/.seeders_phases",
	}

	sd := newTestSeederWithConfig(cfg)
	got, err := sd.SeederDoneFlagFile("00-bedrock", "/chroot/test")
	if err != nil {
		t.Fatalf("SeederDoneFlagFile: %v", err)
	}

	want := "/chroot/test/build/.seeders_phases/seeder.complete_00-bedrock"
	if got != want {
		t.Errorf("SeederDoneFlagFile:\n got  %q\n want %q", got, want)
	}
}

func TestSeederDoneFlagFileTrimsSlash(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["Seeder.ChrootSeederDoneFlagFileNamePrefix"] = []string{
		"seeder.complete",
	}
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{
		"/build/.seeders_phases",
	}

	sd := newTestSeederWithConfig(cfg)
	got, err := sd.SeederDoneFlagFile(
		"10-server", "/chroot/test/",
	)
	if err != nil {
		t.Fatalf("SeederDoneFlagFile: %v", err)
	}

	want := "/chroot/test/build/.seeders_phases/seeder.complete_10-server"
	if got != want {
		t.Errorf("SeederDoneFlagFile:\n got  %q\n want %q", got, want)
	}
}

func TestIsSeederDone(t *testing.T) {
	tmp := t.TempDir()
	cfg := workerTestConfig()
	cfg.Items["Seeder.ChrootSeederDoneFlagFileNamePrefix"] = []string{
		"seeder.complete",
	}
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{
		"/build/.seeders_phases",
	}

	sd := newTestSeederWithConfig(cfg)

	// Not done yet.
	done, err := sd.IsSeederDone("my-seeder", tmp)
	if err != nil {
		t.Fatalf("IsSeederDone: %v", err)
	}
	if done {
		t.Error("Expected seeder to not be done")
	}
}

func TestMarkSeederDone(t *testing.T) {
	tmp := t.TempDir()
	cfg := workerTestConfig()
	cfg.Items["Seeder.ChrootSeederDoneFlagFileNamePrefix"] = []string{
		"seeder.complete",
	}
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{
		"/build/.seeders_phases",
	}

	sd := newTestSeederWithConfig(cfg)

	// Mark as done.
	if err := sd.MarkSeederDone("my-seeder", tmp); err != nil {
		t.Fatalf("MarkSeederDone: %v", err)
	}

	// Verify flag file was created.
	flagFile, _ := sd.SeederDoneFlagFile("my-seeder", tmp)
	if _, err := os.Stat(flagFile); os.IsNotExist(err) {
		t.Errorf("Expected flag file to exist: %s", flagFile)
	}

	// Now IsSeederDone should return true.
	done, err := sd.IsSeederDone("my-seeder", tmp)
	if err != nil {
		t.Fatalf("IsSeederDone: %v", err)
	}
	if !done {
		t.Error("Expected seeder to be done")
	}
}

func TestNewConfigAccessors(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["Seeder.DownloadsDir"] = []string{"/dl"}
	cfg.Items["Seeder.DistfilesDir"] = []string{"/distfiles"}
	cfg.Items["Seeder.BinpkgsDir"] = []string{"/binpkgs"}
	cfg.Items["Seeder.GpgKeysDir"] = []string{"/gpg"}
	cfg.Items["matrixOS.Root"] = []string{"/root"}
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["matrixOS.GitRepo"] = []string{
		"https://example.com/repo.git",
	}
	cfg.Items["matrixOS.DefaultPrivateGitRepoPath"] = []string{
		"/matrixos/private",
	}

	sd := newTestSeederWithConfig(cfg)

	tests := []struct {
		name string
		fn   func() (string, error)
		want string
	}{
		{"DownloadsDir", sd.DownloadsDir, "/dl"},
		{"DistfilesDir", sd.DistfilesDir, "/distfiles"},
		{"BinpkgsDir", sd.BinpkgsDir, "/binpkgs"},
		{"GpgKeysDir", sd.GpgKeysDir, "/gpg"},
		{"DevDir", sd.DevDir, "/root"},
		{"DefaultDevDir", sd.DefaultDevDir, "/matrixos"},
		{
			"GitRepo", sd.GitRepo,
			"https://example.com/repo.git",
		},
		{
			"DefaultPrivateGitRepoPath",
			sd.DefaultPrivateGitRepoPath,
			"/matrixos/private",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.fn()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got != tc.want {
				t.Errorf("%s: got %q, want %q",
					tc.name, got, tc.want)
			}
		})
	}
}

// writeParamsScript creates a params.sh in dir that exports the seeder
// variables and defines the <seedName>_params.find_latest_chroot_dir
// function. Returns the full path to the script.
func writeParamsScript(t *testing.T, dir, seedName, chrootName, chrootsDir, preferredDir, latestDir string, allDirs []string) string {
	t.Helper()
	return writeParamsScriptFull(t, dir, seedName, chrootName, chrootsDir, preferredDir, latestDir, allDirs, nil)
}

func writeParamsScriptFull(t *testing.T, dir, seedName, chrootName, chrootsDir, preferredDir, latestDir string, completeDirs, partialDirs []string) string {
	t.Helper()
	script := fmt.Sprintf(
		"#!/bin/bash\n"+
			"SEEDER_DEPENDS=\"\"\n"+
			"SEEDER_CHROOT_NAME=%q\n"+
			"SEEDER_CHROOTS_DIR=%q\n"+
			"PREFERRED_SEEDER_CHROOT_DIR=%q\n"+
			"%s_params.find_latest_chroot_dir() { echo %q; }\n"+
			"%s_params.find_all_chroot_dirs() { echo %q; }\n"+
			"%s_params.find_partial_chroot_dirs() { echo %q; }",
		chrootName, chrootsDir, preferredDir,
		seedName, latestDir,
		seedName, strings.Join(completeDirs, " "),
		seedName, strings.Join(partialDirs, " "),
	)
	p := filepath.Join(dir, "params.sh")
	if err := os.WriteFile(p, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile params.sh: %v", err)
	}
	return p
}

// newRealSeeder creates a Seeder that uses the real runner.Run so
// bash actually executes. DevDir is set to devDir in the config.
func newRealSeeder(devDir string) *Seeder {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.Root":                             {devDir},
			"matrixOS.DefaultRoot":                      {"/matrixos"},
			"matrixOS.OverlayGitRepo":                   {"https://example.com/overlay.git"},
			"matrixOS.DefaultPrivateGitRepoPath":        {"/matrixos/private"},
			"Seeder.ChrootSeedersPhasesStateDir":        {"/build/.seeders_phases"},
			"Seeder.ChrootSeederDoneFlagFileNamePrefix": {"seeder.complete"},
			"Seeder.SeedsVersioningCadence":             {"weekly"},
			"Seeder.ParamsExecutableName":               {"params.sh"},
		},
		Bools: map[string]bool{},
	}
	s := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       runner.Run,
		chrootRunner: runner.ChrootRun,
	}
	s.SetStdout(&bytes.Buffer{})
	s.SetStderr(&bytes.Buffer{})
	return s
}

func TestParseSeederParams_Success(t *testing.T) {
	tmp := t.TempDir()
	latestDir := filepath.Join(tmp, "chroots", "bedrock-20260228")
	os.MkdirAll(latestDir, 0755)

	writeParamsScript(t, tmp,
		"bedrock",
		"bedrock-20260228",
		"/mnt/chroots",
		"/mnt/chroots/bedrock-20260228",
		latestDir,
		[]string{
			"/mnt/chroots/bedrock-20260228",
			"/mnt/chroots/bedrock-20260104",
		},
	)

	sd := newRealSeeder(tmp)
	params, err := sd.ParseSeederParams(SeederInfo{Name: "00-bedrock", Dir: tmp})
	if err != nil {
		t.Fatalf("ParseSeederParams: %v", err)
	}

	if params.ChrootName != "bedrock-20260228" {
		t.Errorf("ChrootName: got %q, want %q",
			params.ChrootName, "bedrock-20260228")
	}
	if params.ChrootsDir != "/mnt/chroots" {
		t.Errorf("ChrootsDir: got %q, want %q",
			params.ChrootsDir, "/mnt/chroots")
	}
	if params.PreferredChrootDir != "/mnt/chroots/bedrock-20260228" {
		t.Errorf("PreferredChrootDir: got %q, want %q",
			params.PreferredChrootDir,
			"/mnt/chroots/bedrock-20260228")
	}
	if params.LatestAvailableChrootDir != latestDir {
		t.Errorf("LatestAvailableChrootDir: got %q, want %q",
			params.LatestAvailableChrootDir, latestDir)
	}

	if len(params.CompleteChrootDirs) != 2 {
		t.Errorf("len(CompleteChrootDirs): got %d, want %d",
			len(params.CompleteChrootDirs), 2)
	}

	if params.CompleteChrootDirs[0] != "/mnt/chroots/bedrock-20260228" {
		t.Errorf("CompleteChrootDirs[0]: got %q, want %q",
			params.CompleteChrootDirs[0], "/mnt/chroots/bedrock-20260228")
	}
	if params.CompleteChrootDirs[1] != "/mnt/chroots/bedrock-20260104" {
		t.Errorf("CompleteChrootDirs[1]: got %q, want %q",
			params.CompleteChrootDirs[1], "/mnt/chroots/bedrock-20260104")
	}
}

func TestParseSeederParams_Depends(t *testing.T) {
	tmp := t.TempDir()
	latestDir := filepath.Join(tmp, "chroots", "server-20260228")
	os.MkdirAll(latestDir, 0755)

	script := fmt.Sprintf(
		"#!/bin/bash\n"+
			"SEEDER_DEPENDS=\"00-bedrock 10-server\"\n"+
			"SEEDER_CHROOT_NAME=server-20260228\n"+
			"SEEDER_CHROOTS_DIR=/mnt/chroots\n"+
			"PREFERRED_SEEDER_CHROOT_DIR=/mnt/chroots/server-20260228\n"+
			"server_params.find_latest_chroot_dir() { echo %q; }\n"+
			"server_params.find_all_chroot_dirs() { echo %q; }\n"+
			"server_params.find_partial_chroot_dirs() { echo; }",
		latestDir, latestDir,
	)
	paramsFile := filepath.Join(tmp, "params.sh")
	os.WriteFile(paramsFile, []byte(script), 0755)

	sd := newRealSeeder(tmp)
	params, err := sd.ParseSeederParams(SeederInfo{Name: "10-server", Dir: tmp})
	if err != nil {
		t.Fatalf("ParseSeederParams: %v", err)
	}

	if len(params.Depends) != 2 {
		t.Fatalf("len(Depends): got %d, want 2 (entries: %v)",
			len(params.Depends), params.Depends)
	}
	if params.Depends[0] != "00-bedrock" {
		t.Errorf("Depends[0]: got %q, want %q",
			params.Depends[0], "00-bedrock")
	}
	if params.Depends[1] != "10-server" {
		t.Errorf("Depends[1]: got %q, want %q",
			params.Depends[1], "10-server")
	}
}

func TestParseSeederParams_SpaceSeparatedCompleteChrootDirs(t *testing.T) {
	tmp := t.TempDir()
	latestDir := filepath.Join(tmp, "chroots", "bedrock-20260228")
	os.MkdirAll(latestDir, 0755)

	// Create a params.sh where find_all_chroot_dirs echoes
	// all directories on a SINGLE line, space-separated.
	completeDirs := []string{
		"/mnt/chroots/bedrock-20260228",
		"/mnt/chroots/bedrock-20260104",
		"/mnt/chroots/bedrock-20251015",
	}
	script := fmt.Sprintf(
		"#!/bin/bash\n"+
			"SEEDER_DEPENDS=\"\"\n"+
			"SEEDER_CHROOT_NAME=bedrock-20260228\n"+
			"SEEDER_CHROOTS_DIR=/mnt/chroots\n"+
			"PREFERRED_SEEDER_CHROOT_DIR=/mnt/chroots/bedrock-20260228\n"+
			"bedrock_params.find_latest_chroot_dir() { echo %q; }\n"+
			"bedrock_params.find_all_chroot_dirs() { echo %q; }\n"+
			"bedrock_params.find_partial_chroot_dirs() { echo; }",
		latestDir, strings.Join(completeDirs, " "),
	)
	paramsFile := filepath.Join(tmp, "params.sh")
	os.WriteFile(paramsFile, []byte(script), 0755)

	sd := newRealSeeder(tmp)
	params, err := sd.ParseSeederParams(SeederInfo{Name: "00-bedrock", Dir: tmp})
	if err != nil {
		t.Fatalf("ParseSeederParams: %v", err)
	}

	if len(params.CompleteChrootDirs) != 3 {
		t.Fatalf("len(CompleteChrootDirs): got %d, want 3 (entries: %v)",
			len(params.CompleteChrootDirs), params.CompleteChrootDirs)
	}

	for i, want := range completeDirs {
		if params.CompleteChrootDirs[i] != want {
			t.Errorf("CompleteChrootDirs[%d]: got %q, want %q",
				i, params.CompleteChrootDirs[i], want)
		}
	}
}

func TestParseSeederParams_PartialChrootDirs(t *testing.T) {
	tmp := t.TempDir()
	latestDir := filepath.Join(tmp, "chroots", "bedrock-20260228")
	os.MkdirAll(latestDir, 0755)

	partialDirs := []string{
		"/mnt/chroots/bedrock-20260301",
		"/mnt/chroots/bedrock-20260308",
	}
	writeParamsScriptFull(t, tmp,
		"bedrock", "bedrock-20260228", "/mnt/chroots",
		"/mnt/chroots/bedrock-20260228", latestDir,
		[]string{"/mnt/chroots/bedrock-20260228"},
		partialDirs,
	)

	sd := newRealSeeder(tmp)
	params, err := sd.ParseSeederParams(SeederInfo{Name: "00-bedrock", Dir: tmp})
	if err != nil {
		t.Fatalf("ParseSeederParams: %v", err)
	}

	if len(params.PartialChrootDirs) != 2 {
		t.Fatalf("len(PartialChrootDirs): got %d, want 2 (entries: %v)",
			len(params.PartialChrootDirs), params.PartialChrootDirs)
	}
	for i, want := range partialDirs {
		if params.PartialChrootDirs[i] != want {
			t.Errorf("PartialChrootDirs[%d]: got %q, want %q",
				i, params.PartialChrootDirs[i], want)
		}
	}

	// CompleteChrootDirs should still work.
	if len(params.CompleteChrootDirs) != 1 {
		t.Fatalf("len(CompleteChrootDirs): got %d, want 1",
			len(params.CompleteChrootDirs))
	}
}

func TestParseSeederParams_UsesDevDir(t *testing.T) {
	tmp := t.TempDir()
	latestDir := filepath.Join(tmp, "latest-chroot")
	os.MkdirAll(latestDir, 0755)

	// params.sh echoes MATRIXOS_DEV_DIR back as the chroot name
	// so we can verify it was set correctly.
	script := fmt.Sprintf("#!/bin/bash\n"+
		"SEEDER_DEPENDS=\"\"\n"+
		"SEEDER_CHROOT_NAME=\"${MATRIXOS_DEV_DIR}\"\n"+
		"SEEDER_CHROOTS_DIR=/chroots\n"+
		"PREFERRED_SEEDER_CHROOT_DIR=/chroots/test\n"+
		"bedrock_params.find_latest_chroot_dir() { echo %q; }\n"+
		"bedrock_params.find_all_chroot_dirs() { echo %q; }\n"+
		"bedrock_params.find_partial_chroot_dirs() { echo; }\n",
		latestDir, latestDir)
	paramsFile := filepath.Join(tmp, "params.sh")
	os.WriteFile(paramsFile, []byte(script), 0755)

	sd := newRealSeeder("/my/custom/devdir")
	params, err := sd.ParseSeederParams(SeederInfo{Name: "00-bedrock", Dir: tmp})
	if err != nil {
		t.Fatalf("ParseSeederParams: %v", err)
	}
	if params.ChrootName != "/my/custom/devdir" {
		t.Errorf("MATRIXOS_DEV_DIR not passed: got %q",
			params.ChrootName)
	}
}

func TestParseSeederParams_EmptyChrootName(t *testing.T) {
	tmp := t.TempDir()
	latestDir := filepath.Join(tmp, "latest-chroot")
	os.MkdirAll(latestDir, 0755)

	writeParamsScript(t, tmp,
		"bedrock", "", "/mnt/chroots", "/mnt/chroots/bedrock", latestDir,
		[]string{"/mnt/chroots/bedrock"},
	)

	sd := newRealSeeder(tmp)
	_, err := sd.ParseSeederParams(SeederInfo{Name: "00-bedrock", Dir: tmp})
	if err == nil {
		t.Fatal("Expected error for empty ChrootName, got nil")
	}
	if !strings.Contains(err.Error(), "SEEDER_CHROOT_NAME is empty") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestParseSeederParams_EmptyChrootsDir(t *testing.T) {
	tmp := t.TempDir()
	latestDir := filepath.Join(tmp, "latest-chroot")
	os.MkdirAll(latestDir, 0755)

	writeParamsScript(t, tmp,
		"bedrock", "bedrock-20260228", "", "/mnt/chroots/bedrock", latestDir,
		[]string{},
	)

	sd := newRealSeeder(tmp)
	_, err := sd.ParseSeederParams(SeederInfo{Name: "00-bedrock", Dir: tmp})
	if err == nil {
		t.Fatal("Expected error for empty ChrootsDir, got nil")
	}
	// Empty middle line survives the split, caught by validation.
	if !strings.Contains(err.Error(), "SEEDER_CHROOTS_DIR is empty") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestParseSeederParams_EmptyPreferredChrootDir(t *testing.T) {
	tmp := t.TempDir()
	latestDir := filepath.Join(tmp, "latest-chroot")
	os.MkdirAll(latestDir, 0755)

	writeParamsScript(t, tmp,
		"bedrock", "bedrock-20260228", "/mnt/chroots", "", latestDir,
		[]string{},
	)

	sd := newRealSeeder(tmp)
	_, err := sd.ParseSeederParams(SeederInfo{Name: "00-bedrock", Dir: tmp})
	if err == nil {
		t.Fatal("Expected error for empty PreferredChrootDir, got nil")
	}
	// Empty third echo line survives the split, caught by validation.
	if !strings.Contains(err.Error(), "PREFERRED_SEEDER_CHROOT_DIR is empty") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestParseSeederParams_EmptyLatestChrootDir(t *testing.T) {
	tmp := t.TempDir()

	// The find_latest_chroot_dir function returns empty string.
	writeParamsScript(t, tmp,
		"bedrock", "bedrock-20260228", "/mnt/chroots",
		"/mnt/chroots/bedrock-20260228", "",
		[]string{"/mnt/chroots/bedrock-20260228"},
	)

	sd := newRealSeeder(tmp)
	params, err := sd.ParseSeederParams(SeederInfo{Name: "00-bedrock", Dir: tmp})
	if err != nil {
		t.Fatalf("ParseSeederParams: %v", err)
	}
	// Empty LatestAvailableChrootDir is now accepted.
	if params.LatestAvailableChrootDir != "" {
		t.Errorf("LatestAvailableChrootDir: got %q, want empty",
			params.LatestAvailableChrootDir)
	}
}

func TestParseSeederParams_FunctionMissing(t *testing.T) {
	tmp := t.TempDir()

	// params.sh does NOT define bedrock_params.find_latest_chroot_dir.
	// The || true in the template should prevent set -e from killing
	// the script, yielding an empty 4th and 5th line.
	script := "#!/bin/bash\n" +
		"SEEDER_DEPENDS=\"\"\n" +
		"SEEDER_CHROOT_NAME=bedrock-20260228\n" +
		"SEEDER_CHROOTS_DIR=/mnt/chroots\n" +
		"PREFERRED_SEEDER_CHROOT_DIR=/mnt/chroots/bedrock-20260228\n"
	paramsFile := filepath.Join(tmp, "params.sh")
	os.WriteFile(paramsFile, []byte(script), 0755)

	sd := newRealSeeder(tmp)
	params, err := sd.ParseSeederParams(SeederInfo{Name: "00-bedrock", Dir: tmp})
	if err != nil {
		t.Fatalf("ParseSeederParams: %v", err)
	}
	// Missing function yields empty LatestAvailableChrootDir, now accepted.
	if params.LatestAvailableChrootDir != "" {
		t.Errorf("LatestAvailableChrootDir: got %q, want empty",
			params.LatestAvailableChrootDir)
	}
}

func TestParseSeederParams_LatestChrootDirNotExist(t *testing.T) {
	tmp := t.TempDir()

	// Point to a directory that does not exist.
	missingDir := filepath.Join(tmp, "does-not-exist")
	writeParamsScript(t, tmp,
		"bedrock", "bedrock-20260228", "/mnt/chroots",
		"/mnt/chroots/bedrock-20260228", missingDir,
		[]string{""},
	)

	sd := newRealSeeder(tmp)
	params, err := sd.ParseSeederParams(SeederInfo{Name: "00-bedrock", Dir: tmp})
	if err != nil {
		t.Fatalf("ParseSeederParams: %v", err)
	}
	// Non-existent directory is now accepted without validation.
	if params.LatestAvailableChrootDir != missingDir {
		t.Errorf("LatestAvailableChrootDir: got %q, want %q",
			params.LatestAvailableChrootDir, missingDir)
	}

	if len(params.CompleteChrootDirs) != 0 {
		t.Errorf("len(CompleteChrootDirs): got %d, want %d",
			len(params.CompleteChrootDirs), 0)
	}
}

func TestParseParamsVariables_LatestAvailableChrootDir(t *testing.T) {
	tmp := t.TempDir()
	latestDir := filepath.Join(tmp, "chroots", "bedrock-latest")
	os.MkdirAll(latestDir, 0755)

	writeParamsScript(t, tmp,
		"bedrock", "bedrock-20260228", "/mnt/chroots",
		"/mnt/chroots/bedrock-20260228", latestDir,
		[]string{latestDir},
	)
	paramsFile := filepath.Join(tmp, "params.sh")

	sd := newRealSeeder(tmp)
	// Call parseParamsVariables directly to test the raw output
	// without ParseSeederParams' directory-existence check.
	params, err := sd.parseParamsVariables("00-bedrock", paramsFile)
	if err != nil {
		t.Fatalf("parseParamsVariables: %v", err)
	}

	if params.LatestAvailableChrootDir != latestDir {
		t.Errorf("LatestAvailableChrootDir: got %q, want %q",
			params.LatestAvailableChrootDir, latestDir)
	}
	if params.ChrootName != "bedrock-20260228" {
		t.Errorf("ChrootName: got %q, want %q",
			params.ChrootName, "bedrock-20260228")
	}
}

func TestParseParamsVariables_DifferentSeedNames(t *testing.T) {

	tests := []struct {
		seederName string // e.g. "10-server"
		seedName   string // e.g. "server"
	}{
		{"00-bedrock", "bedrock"},
		{"10-server", "server"},
		{"20-gnome", "gnome"},
		{"21-cosmic", "cosmic"},
	}

	for _, tc := range tests {
		t.Run(tc.seederName, func(t *testing.T) {
			tmp := t.TempDir()
			latestDir := filepath.Join(tmp, "chroots", "latest")
			os.MkdirAll(latestDir, 0755)

			writeParamsScript(t, tmp,
				tc.seedName, "chroot-name", "/chroots",
				"/chroots/preferred", latestDir,
				[]string{latestDir},
			)
			paramsFile := filepath.Join(tmp, "params.sh")

			sd := newRealSeeder(tmp)
			params, err := sd.parseParamsVariables(tc.seederName, paramsFile)
			if err != nil {
				t.Fatalf("parseParamsVariables(%q): %v", tc.seederName, err)
			}

			if params.LatestAvailableChrootDir != latestDir {
				t.Errorf("LatestAvailableChrootDir: got %q, want %q",
					params.LatestAvailableChrootDir, latestDir)
			}
		})
	}
}

func TestParseSeederParams_MissingVariable(t *testing.T) {
	tmp := t.TempDir()

	// Script only sets one of the three required variables.
	script := "#!/bin/bash\nSEEDER_CHROOT_NAME=bedrock\n"
	paramsFile := filepath.Join(tmp, "params.sh")
	os.WriteFile(paramsFile, []byte(script), 0755)

	sd := newRealSeeder(tmp)
	_, err := sd.ParseSeederParams(SeederInfo{Name: "00-bedrock", Dir: tmp})
	if err == nil {
		t.Fatal("Expected error for missing variables, got nil")
	}
}

func TestParseSeederParams_BadScript(t *testing.T) {
	tmp := t.TempDir()

	// Write a script with a syntax error.
	paramsFile := filepath.Join(tmp, "params.sh")
	os.WriteFile(paramsFile, []byte("#!/bin/bash\n(((\n"), 0755)

	sd := newRealSeeder(tmp)
	_, err := sd.ParseSeederParams(SeederInfo{Name: "00-bedrock", Dir: tmp})
	if err == nil {
		t.Fatal("Expected error for bad script, got nil")
	}
	if !strings.Contains(err.Error(), "failed to source params") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestParseSeederParams_NonexistentFile(t *testing.T) {
	tmp := t.TempDir()

	sd := newRealSeeder(tmp)
	_, err := sd.ParseSeederParams(SeederInfo{
		Name: "00-bedrock",
		Dir:  tmp,
	})
	if err == nil {
		t.Fatal("Expected error for nonexistent file, got nil")
	}
	if !strings.Contains(err.Error(), "params file not found") {
		t.Errorf("Unexpected error: %v", err)
	}
}

func TestParseSeederParams_DevDirError(t *testing.T) {
	tmp := t.TempDir()
	// Create a params.sh so the file-exists check passes.
	paramsFile := filepath.Join(tmp, "params.sh")
	os.WriteFile(paramsFile, []byte("#!/bin/bash\n"), 0755)

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.Root":               {""},
			"Seeder.ParamsExecutableName": {"params.sh"},
		},
		Bools: map[string]bool{},
	}
	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       runner.Run,
		chrootRunner: runner.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	_, err := sd.ParseSeederParams(SeederInfo{Name: "00-bedrock", Dir: tmp})
	if err == nil {
		t.Fatal("Expected error for empty DevDir, got nil")
	}
	if !strings.Contains(err.Error(), "dev dir") {
		t.Errorf("Unexpected error: %v", err)
	}
}

// --- ExecutePrepper tests ---

// writePrepperScript creates a prepper script in dir that writes all
// the expected env vars to an output file. Returns the script path.
func writePrepperScript(t *testing.T, dir, outputFile string) string {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/bash
set -eu
cat > %q <<EOF
MATRIXOS_DEV_DIR=${MATRIXOS_DEV_DIR}
SEEDER_BUILD_METADATA_FILE=${SEEDER_BUILD_METADATA_FILE}
SEEDER_LOCK_DIR=${SEEDER_LOCK_DIR}
SEEDER_LOCK_WAIT_SECS=${SEEDER_LOCK_WAIT_SECS}
PREPPERS_PHASES_STATE_DIR=${PREPPERS_PHASES_STATE_DIR}
SEEDER_CHROOT_NAME=${SEEDER_CHROOT_NAME}
SEEDER_CHROOTS_DIR=${SEEDER_CHROOTS_DIR}
PREFERRED_SEEDER_CHROOT_DIR=${PREFERRED_SEEDER_CHROOT_DIR}
CHROOT_DIR=${CHROOT_DIR}
DOWNLOAD_DIR=${DOWNLOAD_DIR}
CHROOT_RESUME=${CHROOT_RESUME}
STAGE3_FILE=${STAGE3_FILE}
STAGE3_URL=${STAGE3_URL}
SEEDER_DATE_CADENCE=${SEEDER_DATE_CADENCE}
DEFAULT_MATRIXOS_DEV_DIR=${DEFAULT_MATRIXOS_DEV_DIR}
SEEDER_GPG_KEYS_DIR=${SEEDER_GPG_KEYS_DIR}
SEEDER_OVERLAY_GIT_REPO=${SEEDER_OVERLAY_GIT_REPO}
DEFAULT_PRIVATE_GIT_REPO_PATH=${DEFAULT_PRIVATE_GIT_REPO_PATH}
EOF
`, outputFile)
	p := filepath.Join(dir, "prepper.sh")
	if err := os.WriteFile(p, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile prepper.sh: %v", err)
	}
	return p
}

func newPrepperSeeder(devDir, downloadsDir, stage3URL string) *Seeder {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.Root":                             {devDir},
			"matrixOS.DefaultRoot":                      {"/matrixos"},
			"matrixOS.OverlayGitRepo":                   {"https://example.com/overlay.git"},
			"matrixOS.DefaultPrivateGitRepoPath":        {"/matrixos/private"},
			"Seeder.DownloadsDir":                       {downloadsDir},
			"Seeder.Stage3DownloadUrl":                  {stage3URL},
			"Seeder.ChrootMetadataDir":                  {"/build/.metadata"},
			"Seeder.ChrootMetadataDirBuildFileName":     {"build.json"},
			"Seeder.LocksDir":                           {"/locks/seeder"},
			"Seeder.LockWaitSeconds":                    {"86400"},
			"Seeder.ChrootPreppersPhasesStateDir":       {"/build/preppers/.preppers_phases"},
			"Seeder.ChrootSeedersPhasesStateDir":        {"/build/.seeders_phases"},
			"Seeder.ChrootSeederDoneFlagFileNamePrefix": {"seeder.complete"},
			"Seeder.SeedsVersioningCadence":             {"weekly"},
			"Seeder.GpgKeysDir":                         {"/gpg-keys"},
		},
		Bools: map[string]bool{},
	}
	s := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       runner.Run,
		chrootRunner: runner.ChrootRun,
	}
	s.SetStdout(&bytes.Buffer{})
	s.SetStderr(&bytes.Buffer{})
	return s
}

// readEnvFile parses a KEY=VALUE file into a map.
func readEnvFile(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			m[parts[0]] = parts[1]
		}
	}
	return m
}

func TestExecutePrepper_Success(t *testing.T) {
	tmp := t.TempDir()
	outputFile := filepath.Join(tmp, "env-output.txt")
	prepperScript := writePrepperScript(t, tmp, outputFile)

	sd := newPrepperSeeder(
		"/my/dev",
		"/my/downloads",
		"https://example.com/stage3.tar.xz",
	)

	info := SeederInfo{
		Name:        "00-bedrock",
		PrepperExec: prepperScript,
	}
	params := &SeederParams{
		ChrootName:         "bedrock-20260228",
		ChrootsDir:         "/mnt/chroots",
		PreferredChrootDir: "/mnt/chroots/bedrock-20260228",
	}
	opts := &PrepperOptions{
		ChrootDir:  "/mnt/chroots/bedrock-20260228",
		Resume:     false,
		Stage3File: "stage3-amd64-latest.tar.xz",
	}

	if err := sd.ExecutePrepper(info, params, opts); err != nil {
		t.Fatalf("ExecutePrepper: %v", err)
	}

	env := readEnvFile(t, outputFile)

	checks := map[string]string{
		"MATRIXOS_DEV_DIR":              "/my/dev",
		"SEEDER_BUILD_METADATA_FILE":    "/build/.metadata/build.json",
		"SEEDER_LOCK_DIR":               "/locks/seeder",
		"SEEDER_LOCK_WAIT_SECS":         "86400",
		"PREPPERS_PHASES_STATE_DIR":     "/build/preppers/.preppers_phases",
		"SEEDER_CHROOT_NAME":            "bedrock-20260228",
		"SEEDER_CHROOTS_DIR":            "/mnt/chroots",
		"PREFERRED_SEEDER_CHROOT_DIR":   "/mnt/chroots/bedrock-20260228",
		"CHROOT_DIR":                    "/mnt/chroots/bedrock-20260228",
		"DOWNLOAD_DIR":                  "/my/downloads",
		"CHROOT_RESUME":                 "",
		"STAGE3_FILE":                   "stage3-amd64-latest.tar.xz",
		"STAGE3_URL":                    "https://example.com/stage3.tar.xz",
		"SEEDER_DATE_CADENCE":           "weekly",
		"DEFAULT_MATRIXOS_DEV_DIR":      "/matrixos",
		"SEEDER_GPG_KEYS_DIR":           "/gpg-keys",
		"SEEDER_OVERLAY_GIT_REPO":       "https://example.com/overlay.git",
		"DEFAULT_PRIVATE_GIT_REPO_PATH": "/matrixos/private",
	}
	for k, want := range checks {
		got := env[k]
		if got != want {
			t.Errorf("%s: got %q, want %q", k, got, want)
		}
	}
}

func TestExecutePrepper_Resume(t *testing.T) {
	tmp := t.TempDir()
	outputFile := filepath.Join(tmp, "env-output.txt")
	prepperScript := writePrepperScript(t, tmp, outputFile)

	sd := newPrepperSeeder(
		"/dev/dir",
		"/downloads",
		"https://example.com/stage3.tar.xz",
	)

	info := SeederInfo{PrepperExec: prepperScript}
	params := &SeederParams{
		ChrootName:         "bedrock",
		ChrootsDir:         "/chroots",
		PreferredChrootDir: "/chroots/bedrock",
	}
	opts := &PrepperOptions{
		ChrootDir: "/chroots/bedrock",
		Resume:    true,
	}

	if err := sd.ExecutePrepper(info, params, opts); err != nil {
		t.Fatalf("ExecutePrepper: %v", err)
	}

	env := readEnvFile(t, outputFile)
	if env["CHROOT_RESUME"] != "1" {
		t.Errorf("CHROOT_RESUME: got %q, want %q",
			env["CHROOT_RESUME"], "1")
	}
}

func TestExecutePrepper_ScriptFailure(t *testing.T) {
	tmp := t.TempDir()

	// Write a script that exits with an error.
	failScript := filepath.Join(tmp, "fail-prepper.sh")
	os.WriteFile(failScript, []byte("#!/bin/bash\nexit 1\n"), 0755)

	sd := newPrepperSeeder(
		"/dev/dir", "/downloads",
		"https://example.com/stage3.tar.xz",
	)

	info := SeederInfo{PrepperExec: failScript}
	params := &SeederParams{
		ChrootName:         "bedrock",
		ChrootsDir:         "/chroots",
		PreferredChrootDir: "/chroots/bedrock",
	}
	opts := &PrepperOptions{ChrootDir: "/chroots/bedrock"}

	err := sd.ExecutePrepper(info, params, opts)
	if err == nil {
		t.Fatal("Expected error from failing script, got nil")
	}
}

func TestExecutePrepper_DevDirError(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.Root":            {""},
			"Seeder.DownloadsDir":      {"/dl"},
			"Seeder.Stage3DownloadUrl": {"http://x"},
		},
		Bools: map[string]bool{},
	}
	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       runner.Run,
		chrootRunner: runner.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	err := sd.ExecutePrepper(
		SeederInfo{PrepperExec: "/fake"},
		&SeederParams{ChrootName: "x", ChrootsDir: "/x", PreferredChrootDir: "/x"},
		&PrepperOptions{ChrootDir: "/x"},
	)
	if err == nil {
		t.Fatal("Expected error for empty DevDir, got nil")
	}
}

func TestExecutePrepper_DownloadsDirError(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.Root":                             {"/dev"},
			"matrixOS.DefaultRoot":                      {"/matrixos"},
			"Seeder.DownloadsDir":                       {""},
			"Seeder.Stage3DownloadUrl":                  {"http://x"},
			"Seeder.ChrootSeedersPhasesStateDir":        {"/build/.seeders_phases"},
			"Seeder.ChrootSeederDoneFlagFileNamePrefix": {"seeder.complete"},
			"Seeder.SeedsVersioningCadence":             {"weekly"},
			"matrixOS.OverlayGitRepo":                   {"https://example.com/overlay.git"},
			"matrixOS.DefaultPrivateGitRepoPath":        {"/matrixos/private"},
		},
		Bools: map[string]bool{},
	}
	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       runner.Run,
		chrootRunner: runner.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	err := sd.ExecutePrepper(
		SeederInfo{PrepperExec: "/fake"},
		&SeederParams{ChrootName: "x", ChrootsDir: "/x", PreferredChrootDir: "/x"},
		&PrepperOptions{ChrootDir: "/x"},
	)
	if err == nil {
		t.Fatal("Expected error for empty DownloadsDir, got nil")
	}
}

func TestExecutePrepper_Stage3URLError(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.Root":                             {"/dev"},
			"matrixOS.DefaultRoot":                      {"/matrixos"},
			"Seeder.DownloadsDir":                       {"/dl"},
			"Seeder.Stage3DownloadUrl":                  {""},
			"Seeder.ChrootSeedersPhasesStateDir":        {"/build/.seeders_phases"},
			"Seeder.ChrootSeederDoneFlagFileNamePrefix": {"seeder.complete"},
			"Seeder.SeedsVersioningCadence":             {"weekly"},
			"matrixOS.OverlayGitRepo":                   {"https://example.com/overlay.git"},
			"matrixOS.DefaultPrivateGitRepoPath":        {"/matrixos/private"},
		},
		Bools: map[string]bool{},
	}
	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       runner.Run,
		chrootRunner: runner.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	err := sd.ExecutePrepper(
		SeederInfo{PrepperExec: "/fake"},
		&SeederParams{ChrootName: "x", ChrootsDir: "/x", PreferredChrootDir: "/x"},
		&PrepperOptions{ChrootDir: "/x"},
	)
	if err == nil {
		t.Fatal("Expected error for empty Stage3URL, got nil")
	}
}

// --- Seed tests ---

func TestSeed_Success(t *testing.T) {
	mr := runner.NewMockRunner()
	cfg := workerTestConfig()
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["matrixOS.OverlayGitRepo"] = []string{"https://example.com/overlay.git"}
	cfg.Items["matrixOS.DefaultPrivateGitRepoPath"] = []string{"/matrixos/private"}
	cfg.Items["matrixOS.PrivateGitRepoPath"] = []string{"/srv/private"}
	cfg.Items["matrixOS.Root"] = []string{"/sr/build/daily"}
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{"/build/.seeders_phases"}
	cfg.Items["Seeder.ChrootSeederDoneFlagFileNamePrefix"] = []string{"seeder.complete"}
	cfg.Items["Seeder.SeedsVersioningCadence"] = []string{"weekly"}
	cfg.Items["Seeder.DistfilesDir"] = []string{"/srv/distfiles"}
	cfg.Items["Seeder.BinpkgsDir"] = []string{"/srv/binpkgs"}
	cfg.Items["Seeder.SeedersDir"] = []string{"/matrixos/build/seeders"}

	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	info := SeederInfo{
		Name:             "00-bedrock",
		ChrootExec:       "/srv/build/daily/build/seeders/00-bedrock/chroot.sh",
		ChrootChrootExec: "/matrixos/build/seeders/00-bedrock/chroot.sh",
	}
	if err := sd.Seed(&SeedOptions{ChrootDir: "/mnt/chroot", Info: info}); err != nil {
		t.Fatalf("Seed: unexpected error: %v", err)
	}

	if len(mr.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mr.Calls))
	}
	call := mr.Calls[0]
	if call.Name != "chroot:/matrixos/build/seeders/00-bedrock/chroot.sh" {
		t.Errorf("Name = %q, want chroot exec name", call.Name)
	}
	if call.ChrootDir != "/mnt/chroot" {
		t.Errorf("ChrootDir = %q, want %q", call.ChrootDir, "/mnt/chroot")
	}

	// Verify expected env vars are present.
	wantEnv := map[string]bool{
		"MATRIXOS_DEV_DIR=/matrixos":                              false,
		"SEEDERS_PHASES_STATE_DIR=/build/.seeders_phases":         false,
		"SEEDER_DATE_CADENCE=weekly":                              false,
		"DEFAULT_MATRIXOS_DEV_DIR=/matrixos":                      false,
		"SEEDER_OVERLAY_GIT_REPO=https://example.com/overlay.git": false,
		"DEFAULT_PRIVATE_GIT_REPO_PATH=/matrixos/private":         false,
		"SEEDER_PRIVATE_GIT_REPO_PATH=/srv/private":               false,
		"SEEDER_DISTFILES_DIR=/srv/distfiles":                     false,
		"SEEDER_BINPKGS_DIR=/srv/binpkgs":                         false,
	}
	for _, e := range call.Env {
		if _, ok := wantEnv[e]; ok {
			wantEnv[e] = true
		}
	}
	for env, found := range wantEnv {
		if !found {
			t.Errorf("%s not found in env", env)
		}
	}
}

func TestSeed_DefaultDevDirError(t *testing.T) {
	mr := runner.NewMockRunner()
	cfg := workerTestConfig()
	cfg.Items["matrixOS.DefaultRoot"] = []string{""} // empty → error

	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	err := sd.Seed(&SeedOptions{ChrootDir: "/mnt/chroot", Info: SeederInfo{ChrootExec: "/x"}})
	if err == nil {
		t.Fatal("expected error for empty DefaultDevDir")
	}
}

func TestSeed_PhasesStateDirError(t *testing.T) {
	mr := runner.NewMockRunner()
	cfg := workerTestConfig()
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{""} // empty → error

	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	err := sd.Seed(&SeedOptions{ChrootDir: "/mnt/chroot", Info: SeederInfo{ChrootExec: "/x"}})
	if err == nil {
		t.Fatal("expected error for empty PhasesStateDir")
	}
}

func TestSeed_SeedsVersioningCadenceError(t *testing.T) {
	mr := runner.NewMockRunner()
	cfg := workerTestConfig()
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{"/build/.seeders_phases"}
	cfg.Items["Seeder.ChrootSeederDoneFlagFileNamePrefix"] = []string{"seeder.complete"}
	cfg.Items["Seeder.SeedsVersioningCadence"] = []string{"invalid-cadence"}

	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	err := sd.Seed(&SeedOptions{ChrootDir: "/mnt/chroot", Info: SeederInfo{ChrootExec: "/x"}})
	if err == nil {
		t.Fatal("expected error for invalid SeedsVersioningCadence")
	}
}

func TestSeed_FiltersExistingEnvVars(t *testing.T) {
	// Ensure that pre-existing MATRIXOS_DEV_DIR and SEEDERS_PHASES_STATE_DIR
	// values in the environment are overridden, not duplicated.
	t.Setenv("MATRIXOS_DEV_DIR", "/should/be/overridden")
	t.Setenv("SEEDERS_PHASES_STATE_DIR", "/should/also/be/overridden")
	t.Setenv("SEEDER_DATE_CADENCE", "should-be-overridden")
	t.Setenv("DEFAULT_MATRIXOS_DEV_DIR", "/should/be/overridden")
	t.Setenv("SEEDER_OVERLAY_GIT_REPO", "should-be-overridden")
	t.Setenv("DEFAULT_PRIVATE_GIT_REPO_PATH", "/should/be/overridden")
	t.Setenv("SEEDER_PRIVATE_GIT_REPO_PATH", "/should/be/overridden")
	t.Setenv("SEEDER_DISTFILES_DIR", "/should/be/overridden")
	t.Setenv("SEEDER_BINPKGS_DIR", "/should/be/overridden")

	mr := runner.NewMockRunner()
	cfg := workerTestConfig()
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["matrixOS.OverlayGitRepo"] = []string{"https://example.com/overlay.git"}
	cfg.Items["matrixOS.DefaultPrivateGitRepoPath"] = []string{"/matrixos/private"}
	cfg.Items["matrixOS.PrivateGitRepoPath"] = []string{"/srv/private"}
	cfg.Items["matrixOS.Root"] = []string{"/srv/build/daily"}
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{"/build/.seeders_phases"}
	cfg.Items["Seeder.ChrootSeederDoneFlagFileNamePrefix"] = []string{"seeder.complete"}
	cfg.Items["Seeder.SeedsVersioningCadence"] = []string{"daily"}
	cfg.Items["Seeder.DistfilesDir"] = []string{"/srv/distfiles"}
	cfg.Items["Seeder.BinpkgsDir"] = []string{"/srv/binpkgs"}
	cfg.Items["Seeder.SeedersDir"] = []string{"/matrixos/build/seeders"}

	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	info := SeederInfo{ChrootChrootExec: "/matrixos/build/seeders/00-bedrock/chroot.sh"}
	if err := sd.Seed(&SeedOptions{ChrootDir: "/mnt/chroot", Info: info}); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	call := mr.Calls[0]

	// Count occurrences of each key.
	devDirCount := 0
	phasesCount := 0
	cadenceCount := 0
	defaultDevDirCount := 0
	overlayCount := 0
	privatePathCount := 0
	seederPrivateCount := 0
	seederDistfilesCount := 0
	seederBinpkgsCount := 0
	for _, e := range call.Env {
		if strings.HasPrefix(e, "MATRIXOS_DEV_DIR=") {
			devDirCount++
		}
		if strings.HasPrefix(e, "SEEDERS_PHASES_STATE_DIR=") {
			phasesCount++
		}
		if strings.HasPrefix(e, "SEEDER_DATE_CADENCE=") {
			cadenceCount++
		}
		if strings.HasPrefix(e, "DEFAULT_MATRIXOS_DEV_DIR=") {
			defaultDevDirCount++
		}
		if strings.HasPrefix(e, "SEEDER_OVERLAY_GIT_REPO=") {
			overlayCount++
		}
		if strings.HasPrefix(e, "DEFAULT_PRIVATE_GIT_REPO_PATH=") {
			privatePathCount++
		}
		if strings.HasPrefix(e, "SEEDER_PRIVATE_GIT_REPO_PATH=") {
			seederPrivateCount++
		}
		if strings.HasPrefix(e, "SEEDER_DISTFILES_DIR=") {
			seederDistfilesCount++
		}
		if strings.HasPrefix(e, "SEEDER_BINPKGS_DIR=") {
			seederBinpkgsCount++
		}
	}
	if devDirCount != 1 {
		t.Errorf("MATRIXOS_DEV_DIR appears %d times, want 1", devDirCount)
	}
	if phasesCount != 1 {
		t.Errorf("SEEDERS_PHASES_STATE_DIR appears %d times, want 1", phasesCount)
	}
	if cadenceCount != 1 {
		t.Errorf("SEEDER_DATE_CADENCE appears %d times, want 1", cadenceCount)
	}
	if defaultDevDirCount != 1 {
		t.Errorf("DEFAULT_MATRIXOS_DEV_DIR appears %d times, want 1", defaultDevDirCount)
	}
	if overlayCount != 1 {
		t.Errorf("SEEDER_OVERLAY_GIT_REPO appears %d times, want 1", overlayCount)
	}
	if privatePathCount != 1 {
		t.Errorf("DEFAULT_PRIVATE_GIT_REPO_PATH appears %d times, want 1", privatePathCount)
	}
	if seederPrivateCount != 1 {
		t.Errorf("SEEDER_PRIVATE_GIT_REPO_PATH appears %d times, want 1", seederPrivateCount)
	}
	if seederDistfilesCount != 1 {
		t.Errorf("SEEDER_DISTFILES_DIR appears %d times, want 1", seederDistfilesCount)
	}
	if seederBinpkgsCount != 1 {
		t.Errorf("SEEDER_BINPKGS_DIR appears %d times, want 1", seederBinpkgsCount)
	}

	// Verify the correct values won.
	wantEnv := map[string]bool{
		"MATRIXOS_DEV_DIR=/matrixos":                              false,
		"SEEDERS_PHASES_STATE_DIR=/build/.seeders_phases":         false,
		"SEEDER_DATE_CADENCE=daily":                               false,
		"DEFAULT_MATRIXOS_DEV_DIR=/matrixos":                      false,
		"SEEDER_OVERLAY_GIT_REPO=https://example.com/overlay.git": false,
		"DEFAULT_PRIVATE_GIT_REPO_PATH=/matrixos/private":         false,
		"SEEDER_PRIVATE_GIT_REPO_PATH=/srv/private":               false,
		"SEEDER_DISTFILES_DIR=/srv/distfiles":                     false,
		"SEEDER_BINPKGS_DIR=/srv/binpkgs":                         false,
	}
	for _, e := range call.Env {
		if _, ok := wantEnv[e]; ok {
			wantEnv[e] = true
		}
	}
	for env, found := range wantEnv {
		if !found {
			t.Errorf("%s not found in env", env)
		}
	}
}

func TestSeed_ChrootRunnerError(t *testing.T) {
	mr := runner.NewMockRunnerFailOnCall(0, fmt.Errorf("chroot boom"))
	cfg := workerTestConfig()
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["matrixOS.OverlayGitRepo"] = []string{"https://example.com/overlay.git"}
	cfg.Items["matrixOS.DefaultPrivateGitRepoPath"] = []string{"/matrixos/private"}
	cfg.Items["matrixOS.PrivateGitRepoPath"] = []string{"/srv/private"}
	cfg.Items["matrixOS.Root"] = []string{"/srv/build/daily"}
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{"/build/.seeders_phases"}
	cfg.Items["Seeder.ChrootSeederDoneFlagFileNamePrefix"] = []string{"seeder.complete"}
	cfg.Items["Seeder.SeedsVersioningCadence"] = []string{"weekly"}
	cfg.Items["Seeder.DistfilesDir"] = []string{"/srv/distfiles"}
	cfg.Items["Seeder.BinpkgsDir"] = []string{"/srv/binpkgs"}
	cfg.Items["Seeder.SeedersDir"] = []string{"/matrixos/build/seeders"}

	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	err := sd.Seed(&SeedOptions{ChrootDir: "/mnt/chroot", Info: SeederInfo{ChrootExec: "/x"}})
	if err == nil {
		t.Fatal("expected error from chrootRunner")
	}
	if !strings.Contains(err.Error(), "chroot boom") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- generateSeederEnvVars tests ---

func TestGenerateSeederEnvVars_Success(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["matrixOS.PrivateGitRepoPath"] = []string{"/srv/private"}
	cfg.Items["Seeder.DistfilesDir"] = []string{"/srv/distfiles"}
	cfg.Items["Seeder.BinpkgsDir"] = []string{"/srv/binpkgs"}

	sd := newTestSeederWithConfig(cfg)

	env, err := sd.generateSeederEnvVars(os.Environ())
	if err != nil {
		t.Fatalf("generateSeederEnvVars: unexpected error: %v", err)
	}

	wantEnv := map[string]bool{
		"MATRIXOS_DEV_DIR=/matrixos":                false,
		"SEEDER_PRIVATE_GIT_REPO_PATH=/srv/private": false,
		"SEEDER_DISTFILES_DIR=/srv/distfiles":       false,
		"SEEDER_BINPKGS_DIR=/srv/binpkgs":           false,
	}
	for _, e := range env {
		if _, ok := wantEnv[e]; ok {
			wantEnv[e] = true
		}
	}
	for entry, found := range wantEnv {
		if !found {
			t.Errorf("%s not found in env", entry)
		}
	}
}

func TestGenerateSeederEnvVars_FiltersExistingKeys(t *testing.T) {
	t.Setenv("MATRIXOS_DEV_DIR", "/old/devdir")
	t.Setenv("SEEDER_PRIVATE_GIT_REPO_PATH", "/old/private")
	t.Setenv("SEEDER_DISTFILES_DIR", "/old/distfiles")
	t.Setenv("SEEDER_BINPKGS_DIR", "/old/binpkgs")

	cfg := workerTestConfig()
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["matrixOS.PrivateGitRepoPath"] = []string{"/srv/private"}
	cfg.Items["Seeder.DistfilesDir"] = []string{"/srv/distfiles"}
	cfg.Items["Seeder.BinpkgsDir"] = []string{"/srv/binpkgs"}

	sd := newTestSeederWithConfig(cfg)

	env, err := sd.generateSeederEnvVars(os.Environ())
	if err != nil {
		t.Fatalf("generateSeederEnvVars: %v", err)
	}

	counts := map[string]int{
		"MATRIXOS_DEV_DIR=":             0,
		"SEEDER_PRIVATE_GIT_REPO_PATH=": 0,
		"SEEDER_DISTFILES_DIR=":         0,
		"SEEDER_BINPKGS_DIR=":           0,
	}
	for _, e := range env {
		for prefix := range counts {
			if strings.HasPrefix(e, prefix) {
				counts[prefix]++
			}
		}
	}
	for prefix, count := range counts {
		if count != 1 {
			t.Errorf("%s appears %d times, want 1", prefix, count)
		}
	}

	// Verify values are the new ones, not the old ones.
	wantEnv := map[string]bool{
		"MATRIXOS_DEV_DIR=/matrixos":                false,
		"SEEDER_PRIVATE_GIT_REPO_PATH=/srv/private": false,
		"SEEDER_DISTFILES_DIR=/srv/distfiles":       false,
		"SEEDER_BINPKGS_DIR=/srv/binpkgs":           false,
	}
	for _, e := range env {
		if _, ok := wantEnv[e]; ok {
			wantEnv[e] = true
		}
	}
	for entry, found := range wantEnv {
		if !found {
			t.Errorf("%s not found in env", entry)
		}
	}
}

func TestGenerateSeederEnvVars_DefaultDevDirError(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["matrixOS.DefaultRoot"] = []string{""} // empty → error

	sd := newTestSeederWithConfig(cfg)

	_, err := sd.generateSeederEnvVars(os.Environ())
	if err == nil {
		t.Fatal("expected error for empty DefaultDevDir")
	}
}

func TestGenerateSeederEnvVars_PrivateGitRepoPathError(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["matrixOS.PrivateGitRepoPath"] = []string{""} // empty → error

	sd := newTestSeederWithConfig(cfg)

	_, err := sd.generateSeederEnvVars(os.Environ())
	if err == nil {
		t.Fatal("expected error for empty PrivateGitRepoPath")
	}
}

func TestGenerateSeederEnvVars_DistfilesDirError(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["matrixOS.PrivateGitRepoPath"] = []string{"/srv/private"}
	cfg.Items["Seeder.DistfilesDir"] = []string{""} // empty → error

	sd := newTestSeederWithConfig(cfg)

	_, err := sd.generateSeederEnvVars(os.Environ())
	if err == nil {
		t.Fatal("expected error for empty DistfilesDir")
	}
}

func TestGenerateSeederEnvVars_BinpkgsDirError(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["matrixOS.PrivateGitRepoPath"] = []string{"/srv/private"}
	cfg.Items["Seeder.DistfilesDir"] = []string{"/srv/distfiles"}
	cfg.Items["Seeder.BinpkgsDir"] = []string{""} // empty → error

	sd := newTestSeederWithConfig(cfg)

	_, err := sd.generateSeederEnvVars(os.Environ())
	if err == nil {
		t.Fatal("expected error for empty BinpkgsDir")
	}
}

func TestGenerateSeederEnvVars_EmptyInputEnv(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["matrixOS.PrivateGitRepoPath"] = []string{"/srv/private"}
	cfg.Items["Seeder.DistfilesDir"] = []string{"/srv/distfiles"}
	cfg.Items["Seeder.BinpkgsDir"] = []string{"/srv/binpkgs"}

	sd := newTestSeederWithConfig(cfg)

	env, err := sd.generateSeederEnvVars(nil)
	if err != nil {
		t.Fatalf("generateSeederEnvVars: unexpected error: %v", err)
	}

	if len(env) != 5 {
		t.Fatalf("expected exactly 5 env vars, got %d: %v", len(env), env)
	}

	wantEnv := map[string]bool{
		"MATRIXOS_DEV_DIR=/matrixos":                false,
		"SEEDER_PRIVATE_GIT_REPO_PATH=/srv/private": false,
		"SEEDER_DISTFILES_DIR=/srv/distfiles":       false,
		"SEEDER_BINPKGS_DIR=/srv/binpkgs":           false,
		"RUNNER_TYPE=seeder":                        false,
	}
	for _, e := range env {
		if _, ok := wantEnv[e]; ok {
			wantEnv[e] = true
		}
	}
	for entry, found := range wantEnv {
		if !found {
			t.Errorf("%s not found in env", entry)
		}
	}
}

// --- ImportGentooGpgKeys tests ---

func TestImportGentooGpgKeys_Success(t *testing.T) {
	tmp := t.TempDir()
	gpgDir := filepath.Join(tmp, "gpg")

	mr := runner.NewMockRunner()
	cfg := workerTestConfig()
	cfg.Items["Seeder.GpgKeysDir"] = []string{gpgDir}

	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	if err := sd.ImportGentooGpgKeys(); err != nil {
		t.Fatalf("ImportGentooGpgKeys: %v", err)
	}

	// gpgDir should have been created with 0700 permissions.
	info, err := os.Stat(gpgDir)
	if err != nil {
		t.Fatalf("Stat gpgDir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Errorf("gpgDir permissions = %o, want 0700", perm)
	}

	// The mock runner should have been called with gpg.
	if len(mr.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mr.Calls))
	}
	if mr.Calls[0].Name != "gpg" {
		t.Errorf("Name = %q, want %q", mr.Calls[0].Name, "gpg")
	}
}

func TestImportGentooGpgKeys_FixesFilePermissions(t *testing.T) {
	tmp := t.TempDir()
	gpgDir := filepath.Join(tmp, "gpg")
	os.MkdirAll(gpgDir, 0755)

	// Create a file with overly broad permissions.
	testFile := filepath.Join(gpgDir, "pubring.kbx")
	os.WriteFile(testFile, []byte("key"), 0644)

	mr := runner.NewMockRunner()
	cfg := workerTestConfig()
	cfg.Items["Seeder.GpgKeysDir"] = []string{gpgDir}

	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	if err := sd.ImportGentooGpgKeys(); err != nil {
		t.Fatalf("ImportGentooGpgKeys: %v", err)
	}

	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}
}

func TestImportGentooGpgKeys_GpgKeysDirError(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["Seeder.GpgKeysDir"] = []string{""} // empty → error

	mr := runner.NewMockRunner()
	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	if err := sd.ImportGentooGpgKeys(); err == nil {
		t.Fatal("expected error for empty GpgKeysDir")
	}
}

func TestImportGentooGpgKeys_RunnerError(t *testing.T) {
	tmp := t.TempDir()
	gpgDir := filepath.Join(tmp, "gpg")

	mr := runner.NewMockRunnerFailOnCall(0, fmt.Errorf("gpg failed"))
	cfg := workerTestConfig()
	cfg.Items["Seeder.GpgKeysDir"] = []string{gpgDir}

	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	err := sd.ImportGentooGpgKeys()
	if err == nil {
		t.Fatal("expected error from runner")
	}
	if !strings.Contains(err.Error(), "gpg failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestKillGpgDaemons_Success(t *testing.T) {
	tmp := t.TempDir()
	gpgDir := filepath.Join(tmp, "gpg")

	mr := runner.NewMockRunner()
	cfg := workerTestConfig()
	cfg.Items["Seeder.GpgKeysDir"] = []string{gpgDir}

	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	sd.KillGpgDaemons() // should not panic

	if len(mr.Calls) != 1 {
		t.Fatalf("Expected 1 call, got %d", len(mr.Calls))
	}
	if mr.Calls[0].Name != "gpgconf" {
		t.Errorf("Expected gpgconf, got %s", mr.Calls[0].Name)
	}
	wantArgs := []string{"--homedir", gpgDir, "--kill", "all"}
	if strings.Join(mr.Calls[0].Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("args = %v, want %v", mr.Calls[0].Args, wantArgs)
	}
}

func TestKillGpgDaemons_BadConfig(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["Seeder.GpgKeysDir"] = []string{""} // empty → error

	mr := runner.NewMockRunner()
	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	sd.KillGpgDaemons() // should not panic, should be a no-op

	if len(mr.Calls) != 0 {
		t.Error("Expected no calls for bad config")
	}
}

// --- SetupChrootDNS tests ---

func TestSetupChrootDNS_Success(t *testing.T) {
	// This test requires /etc/resolv.conf to exist on the host.
	if _, err := os.Stat("/etc/resolv.conf"); os.IsNotExist(err) {
		t.Skip("skipping: /etc/resolv.conf not found")
	}

	tmp := t.TempDir()
	cfg := workerTestConfig()
	sd := newTestSeederWithConfig(cfg)

	if err := sd.SetupChrootDNS(tmp); err != nil {
		t.Fatalf("SetupChrootDNS: %v", err)
	}

	dst := filepath.Join(tmp, "etc", "resolv.conf")
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		t.Fatal("resolv.conf not copied into chroot")
	}

	// Content should match host.
	hostData, _ := os.ReadFile("/etc/resolv.conf")
	chrootData, _ := os.ReadFile(dst)
	if string(chrootData) != string(hostData) {
		t.Error("chroot resolv.conf does not match host")
	}
}

func TestSetupChrootDNS_CreatesEtcDir(t *testing.T) {
	if _, err := os.Stat("/etc/resolv.conf"); os.IsNotExist(err) {
		t.Skip("skipping: /etc/resolv.conf not found")
	}

	tmp := t.TempDir()
	cfg := workerTestConfig()
	sd := newTestSeederWithConfig(cfg)

	// etc dir does not exist yet — SetupChrootDNS should create it.
	if err := sd.SetupChrootDNS(tmp); err != nil {
		t.Fatalf("SetupChrootDNS: %v", err)
	}

	etcDir := filepath.Join(tmp, "etc")
	info, err := os.Stat(etcDir)
	if err != nil {
		t.Fatalf("Stat etc: %v", err)
	}
	if !info.IsDir() {
		t.Error("etc is not a directory")
	}
}

// --- SetupChrootDirs tests ---

func TestSetupChrootDirs_CreatesPhasesDirAndCallsSetupDevDir(t *testing.T) {
	tmp := t.TempDir()

	mr := runner.NewMockRunner()
	cfg := workerTestConfig()
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{"/build/.seeders_phases"}
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["Seeder.UseLocalGitRepoInsideChroot"] = []string{"true"}
	cfg.Bools["Seeder.UseLocalGitRepoInsideChroot"] = true
	cfg.Items["Seeder.GitCloneArgs"] = []string{"--depth 1"}
	cfg.Items["matrixOS.Root"] = []string{"/dev/toolkit"}
	cfg.Items["Seeder.DeleteDotGitFromGitRepo"] = []string{"false"}
	cfg.Bools["Seeder.DeleteDotGitFromGitRepo"] = false

	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	if err := sd.SetupChrootDirs(tmp); err != nil {
		t.Fatalf("SetupChrootDirs: %v", err)
	}

	// Phases dir should exist.
	phasesDir := filepath.Join(tmp, "build", ".seeders_phases")
	if _, err := os.Stat(phasesDir); os.IsNotExist(err) {
		t.Error("phases dir not created")
	}

	// git clone should have been called (local clone).
	if len(mr.Calls) < 1 {
		t.Fatal("expected at least 1 runner call (git clone)")
	}
	call := mr.Calls[0]
	if call.Name != "git" {
		t.Errorf("expected git call, got %q", call.Name)
	}
	if call.Args[0] != "clone" {
		t.Errorf("expected clone subcommand, got %q", call.Args[0])
	}
}

func TestSetupChrootDirs_SkipsCloneIfDevDirExists(t *testing.T) {
	tmp := t.TempDir()

	mr := runner.NewMockRunner()
	cfg := workerTestConfig()
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{"/build/.seeders_phases"}
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["Seeder.DeleteDotGitFromGitRepo"] = []string{"false"}
	cfg.Bools["Seeder.DeleteDotGitFromGitRepo"] = false

	// Pre-create the dev dir so clone is skipped.
	chrootDevDir := filepath.Join(tmp, "matrixos")
	os.MkdirAll(chrootDevDir, 0755)

	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	if err := sd.SetupChrootDirs(tmp); err != nil {
		t.Fatalf("SetupChrootDirs: %v", err)
	}

	// No runner calls expected (clone skipped).
	if len(mr.Calls) != 0 {
		t.Errorf("expected 0 runner calls, got %d", len(mr.Calls))
	}
}

func TestSetupChrootDirs_RemoteClone(t *testing.T) {
	tmp := t.TempDir()

	mr := runner.NewMockRunner()
	cfg := workerTestConfig()
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{"/build/.seeders_phases"}
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}
	cfg.Items["Seeder.UseLocalGitRepoInsideChroot"] = []string{"false"}
	cfg.Bools["Seeder.UseLocalGitRepoInsideChroot"] = false
	cfg.Items["Seeder.GitCloneArgs"] = []string{"--depth 1"}
	cfg.Items["matrixOS.GitRepo"] = []string{"https://example.com/repo.git"}
	cfg.Items["Seeder.DeleteDotGitFromGitRepo"] = []string{"false"}
	cfg.Bools["Seeder.DeleteDotGitFromGitRepo"] = false

	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	if err := sd.SetupChrootDirs(tmp); err != nil {
		t.Fatalf("SetupChrootDirs: %v", err)
	}

	// RetryableCmd calls the runner multiple times on failure, but on
	// success the first call is git clone with the remote URL.
	if len(mr.Calls) < 1 {
		t.Fatal("expected at least 1 runner call")
	}
	call := mr.Calls[0]
	if call.Name != "git" {
		t.Errorf("expected git, got %q", call.Name)
	}
	// The URL should appear in the args.
	found := false
	for _, a := range call.Args {
		if a == "https://example.com/repo.git" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("remote URL not found in args: %v", call.Args)
	}
}

func TestSetupChrootDirs_PhasesStateDirError(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{""} // error
	cfg.Items["matrixOS.DefaultRoot"] = []string{"/matrixos"}

	mr := runner.NewMockRunner()
	sd := &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       mr.Run,
		chrootRunner: mr.ChrootRun,
	}
	sd.SetStdout(&bytes.Buffer{})
	sd.SetStderr(&bytes.Buffer{})

	err := sd.SetupChrootDirs("/tmp/fake")
	if err == nil {
		t.Fatal("expected error for empty PhasesStateDir")
	}
}

// --- cleanDevDirGitDir tests ---

func TestCleanDevDirGitDir_DeletesWhenConfigured(t *testing.T) {
	tmp := t.TempDir()
	dotGit := filepath.Join(tmp, ".git")
	os.MkdirAll(dotGit, 0755)
	os.WriteFile(filepath.Join(dotGit, "HEAD"), []byte("ref"), 0644)

	cfg := workerTestConfig()
	cfg.Items["Seeder.DeleteDotGitFromGitRepo"] = []string{"true"}
	cfg.Bools["Seeder.DeleteDotGitFromGitRepo"] = true

	sd := newTestSeederWithConfig(cfg)

	if err := sd.cleanDevDirGitDir(tmp); err != nil {
		t.Fatalf("cleanDevDirGitDir: %v", err)
	}

	if _, err := os.Stat(dotGit); !os.IsNotExist(err) {
		t.Error(".git dir should have been deleted")
	}
}

func TestCleanDevDirGitDir_SkipsWhenNotConfigured(t *testing.T) {
	tmp := t.TempDir()
	dotGit := filepath.Join(tmp, ".git")
	os.MkdirAll(dotGit, 0755)

	cfg := workerTestConfig()
	cfg.Items["Seeder.DeleteDotGitFromGitRepo"] = []string{"false"}
	cfg.Bools["Seeder.DeleteDotGitFromGitRepo"] = false

	sd := newTestSeederWithConfig(cfg)

	if err := sd.cleanDevDirGitDir(tmp); err != nil {
		t.Fatalf("cleanDevDirGitDir: %v", err)
	}

	if _, err := os.Stat(dotGit); os.IsNotExist(err) {
		t.Error(".git dir should NOT have been deleted")
	}
}

func TestCleanDevDirGitDir_NoDotGitDir(t *testing.T) {
	tmp := t.TempDir()

	cfg := workerTestConfig()
	cfg.Items["Seeder.DeleteDotGitFromGitRepo"] = []string{"true"}
	cfg.Bools["Seeder.DeleteDotGitFromGitRepo"] = true

	sd := newTestSeederWithConfig(cfg)

	// Should succeed without error when .git doesn't exist.
	if err := sd.cleanDevDirGitDir(tmp); err != nil {
		t.Fatalf("cleanDevDirGitDir: %v", err)
	}
}

// --- Done-flag error branch tests ---

func TestSeederDoneFlagFile_PhasesStateDirError(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{""}
	cfg.Items["Seeder.ChrootSeederDoneFlagFileNamePrefix"] = []string{"done"}

	sd := newTestSeederWithConfig(cfg)
	_, err := sd.SeederDoneFlagFile("x", "/chroot")
	if err == nil {
		t.Fatal("expected error for empty PhasesStateDir")
	}
}

func TestSeederDoneFlagFile_PrefixError(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{"/build/.phases"}
	cfg.Items["Seeder.ChrootSeederDoneFlagFileNamePrefix"] = []string{""}

	sd := newTestSeederWithConfig(cfg)
	_, err := sd.SeederDoneFlagFile("x", "/chroot")
	if err == nil {
		t.Fatal("expected error for empty prefix")
	}
}

func TestIsSeederDone_ConfigError(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{""}

	sd := newTestSeederWithConfig(cfg)
	_, err := sd.IsSeederDone("x", "/chroot")
	if err == nil {
		t.Fatal("expected error propagated from SeederDoneFlagFile")
	}
}

func TestMarkSeederDone_ConfigError(t *testing.T) {
	cfg := workerTestConfig()
	cfg.Items["Seeder.ChrootSeedersPhasesStateDir"] = []string{""}

	sd := newTestSeederWithConfig(cfg)
	err := sd.MarkSeederDone("x", "/chroot")
	if err == nil {
		t.Fatal("expected error propagated from SeederDoneFlagFile")
	}
}

// --- ParseSeederParams integration test with real seeders ---

func TestParseSeederParams_RealSeeders(t *testing.T) {

	cfg, err := config.NewBaseConfig()
	if err != nil {
		t.Skipf("skipping: unable to load base config: %v", err)
	}
	if err := cfg.Load(); err != nil {
		t.Skipf("skipping: unable to load base config: %v", err)
	}

	sd, err := NewSeeder(cfg, nil)
	if err != nil {
		t.Fatalf("NewSeeder: %v", err)
	}

	devDir, err := sd.DevDir()
	if err != nil {
		t.Fatalf("DevDir: %v", err)
	}
	t.Logf("DevDir: %s", devDir)

	det, err := NewSeederDetector(cfg)
	if err != nil {
		t.Fatalf("NewSeederDetector: %v", err)
	}

	seeders, err := det.Detect(nil, nil)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(seeders) == 0 {
		t.Fatal("Detect returned no seeders")
	}

	for _, info := range seeders {
		t.Run(info.Name, func(t *testing.T) {
			params, err := sd.ParseSeederParams(info)
			if err != nil {
				t.Fatalf("ParseSeederParams(%s): %v", info.Name, err)
			}

			if params.ChrootName == "" {
				t.Errorf("ChrootName is empty for %s", info.Name)
			}
			if params.ChrootsDir == "" {
				t.Errorf("ChrootsDir is empty for %s", info.Name)
			}
			if params.PreferredChrootDir == "" {
				t.Errorf("PreferredChrootDir is empty for %s", info.Name)
			}

			t.Logf("  ChrootName:         %s", params.ChrootName)
			t.Logf("  ChrootsDir:         %s", params.ChrootsDir)
			t.Logf("  PreferredChrootDir: %s", params.PreferredChrootDir)
			t.Logf("  LatestAvailable:    %s", params.LatestAvailableChrootDir)
			t.Logf("  CompleteChrootDirs: %v", params.CompleteChrootDirs)
			t.Logf("  PartialChrootDirs:  %v", params.PartialChrootDirs)
			t.Logf("  Depends:            %v", params.Depends)
		})
	}
}
