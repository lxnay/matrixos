package seeder

import (
	"errors"
	"testing"

	"matrixos/vector/lib/config"
)

// configAccessorTestCase describes a single accessor under test.
type configAccessorTestCase struct {
	name    string
	key     string                          // expected config key
	call    func(s *Seeder) (string, error) // the accessor to invoke
	wantVal string                          // expected string value
}

// stringAccessors returns test cases for every string config accessor.
func stringAccessors() []configAccessorTestCase {
	return []configAccessorTestCase{
		{
			name:    "PhasesStateDir",
			key:     "Seeder.ChrootSeedersPhasesStateDir",
			call:    func(s *Seeder) (string, error) { return s.PhasesStateDir() },
			wantVal: "/build/.seeders_phases",
		},
		{
			name:    "PreppersPhasesStateDir",
			key:     "Seeder.ChrootPreppersPhasesStateDir",
			call:    func(s *Seeder) (string, error) { return s.PreppersPhasesStateDir() },
			wantVal: "/build/preppers/.preppers_phases",
		},
		{
			name:    "SeederDoneFlagFilePrefix",
			key:     "Seeder.ChrootSeederDoneFlagFileNamePrefix",
			call:    func(s *Seeder) (string, error) { return s.SeederDoneFlagFilePrefix() },
			wantVal: "seeder.complete",
		},
		{
			name:    "PrivateExampleGitRepo",
			key:     "matrixOS.PrivateExampleGitRepo",
			call:    func(s *Seeder) (string, error) { return s.PrivateExampleGitRepo() },
			wantVal: "https://example.com/private.git",
		},
		{
			name:    "PrivateGitRepoPath",
			key:     "matrixOS.PrivateGitRepoPath",
			call:    func(s *Seeder) (string, error) { return s.PrivateGitRepoPath() },
			wantVal: "/priv",
		},
		{
			name:    "GitCloneArgs",
			key:     "Seeder.GitCloneArgs",
			call:    func(s *Seeder) (string, error) { return s.GitCloneArgs() },
			wantVal: "--depth 1",
		},
		{
			name:    "LockDir",
			key:     "Seeder.LocksDir",
			call:    func(s *Seeder) (string, error) { return s.LockDir() },
			wantVal: "/locks/seeder",
		},
		{
			name:    "LockWaitSeconds",
			key:     "Seeder.LockWaitSeconds",
			call:    func(s *Seeder) (string, error) { return s.LockWaitSeconds() },
			wantVal: "86400",
		},
		{
			name:    "Stage3DownloadUrl",
			key:     "Seeder.Stage3DownloadUrl",
			call:    func(s *Seeder) (string, error) { return s.Stage3DownloadUrl() },
			wantVal: "https://distfiles.gentoo.org/releases/amd64/autobuilds/current-stage3-amd64-systemd/latest-stage3-amd64-systemd.txt",
		},
		{
			name:    "ChrootMetadataDir",
			key:     "Seeder.ChrootMetadataDir",
			call:    func(s *Seeder) (string, error) { return s.ChrootMetadataDir() },
			wantVal: "/build/.metadata",
		},
		{
			name:    "ChrootMetadataDirBuildFileName",
			key:     "Seeder.ChrootMetadataDirBuildFileName",
			call:    func(s *Seeder) (string, error) { return s.ChrootMetadataDirBuildFileName() },
			wantVal: "build.json",
		},
	}
}

func TestConfigAccessors_ReturnsValue(t *testing.T) {
	for _, tc := range stringAccessors() {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSeeder()
			s.cfg.(*config.MockConfig).Items[tc.key] = []string{tc.wantVal}

			got, err := tc.call(s)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.wantVal {
				t.Errorf("got %q, want %q", got, tc.wantVal)
			}
		})
	}
}

func TestConfigAccessors_ErrorOnEmpty(t *testing.T) {
	for _, tc := range stringAccessors() {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestSeeder()
			// Key absent => GetItem returns "" => configItem returns error.

			_, err := tc.call(s)
			if err == nil {
				t.Fatal("expected error for empty config value, got nil")
			}
		})
	}
}

func TestBuildMetadataFile_ReturnsJoinedPath(t *testing.T) {
	s := newTestSeeder()
	s.cfg.(*config.MockConfig).Items["Seeder.ChrootMetadataDir"] = []string{"/build/.metadata"}
	s.cfg.(*config.MockConfig).Items["Seeder.ChrootMetadataDirBuildFileName"] = []string{"build.json"}

	got, err := s.BuildMetadataFile()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "/build/.metadata/build.json"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildMetadataFile_ErrorOnMissingDir(t *testing.T) {
	s := newTestSeeder()
	// ChrootMetadataDir key absent => error
	s.cfg.(*config.MockConfig).Items["Seeder.ChrootMetadataDirBuildFileName"] = []string{"build.json"}

	_, err := s.BuildMetadataFile()
	if err == nil {
		t.Fatal("expected error for missing ChrootMetadataDir, got nil")
	}
}

func TestBuildMetadataFile_ErrorOnMissingFileName(t *testing.T) {
	s := newTestSeeder()
	s.cfg.(*config.MockConfig).Items["Seeder.ChrootMetadataDir"] = []string{"/build/.metadata"}
	// ChrootMetadataDirBuildFileName key absent => error

	_, err := s.BuildMetadataFile()
	if err == nil {
		t.Fatal("expected error for missing ChrootMetadataDirBuildFileName, got nil")
	}
}

func TestConfigAccessors_PropagatesGetItemError(t *testing.T) {
	for _, tc := range stringAccessors() {
		t.Run(tc.name, func(t *testing.T) {
			wantErr := errors.New("cfg broken")
			s := newTestSeeder()
			s.cfg = &config.ErrConfig{Err: wantErr}

			_, err := tc.call(s)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, wantErr) {
				t.Errorf("got error %v, want %v", err, wantErr)
			}
		})
	}
}

func TestSeedsVersioningCadence_ValidValues(t *testing.T) {
	for _, val := range []string{"daily", "weekly", "monthly"} {
		t.Run(val, func(t *testing.T) {
			s := newTestSeeder()
			s.cfg.(*config.MockConfig).Items["Seeder.SeedsVersioningCadence"] = []string{val}

			got, err := s.SeedsVersioningCadence()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != val {
				t.Errorf("got %q, want %q", got, val)
			}
		})
	}
}

func TestSeedsVersioningCadence_DefaultsToWeekly(t *testing.T) {
	s := newTestSeeder()
	// Key absent => GetItem returns "" => defaults to "weekly".

	got, err := s.SeedsVersioningCadence()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "weekly" {
		t.Errorf("got %q, want %q", got, "weekly")
	}
}

func TestSeedsVersioningCadence_InvalidValue(t *testing.T) {
	s := newTestSeeder()
	s.cfg.(*config.MockConfig).Items["Seeder.SeedsVersioningCadence"] = []string{"biweekly"}

	_, err := s.SeedsVersioningCadence()
	if err == nil {
		t.Fatal("expected error for invalid cadence value, got nil")
	}
}

func TestSeedsVersioningCadence_PropagatesError(t *testing.T) {
	wantErr := errors.New("cfg broken")
	s := newTestSeeder()
	s.cfg = &config.ErrConfig{Err: wantErr}

	_, err := s.SeedsVersioningCadence()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("got error %v, want %v", err, wantErr)
	}
}

func TestParallelism_Default(t *testing.T) {
	s := newTestSeeder()
	got, err := s.Parallelism()
	if err != nil {
		t.Fatalf("Parallelism: %v", err)
	}
	if got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestParallelism_Set(t *testing.T) {
	s := newTestSeeder()
	s.cfg.(*config.MockConfig).Items["Seeder.Parallelism"] = []string{"4"}
	got, err := s.Parallelism()
	if err != nil {
		t.Fatalf("Parallelism: %v", err)
	}
	if got != 4 {
		t.Errorf("got %d, want 4", got)
	}
}

func TestParallelism_InvalidValue(t *testing.T) {
	s := newTestSeeder()
	s.cfg.(*config.MockConfig).Items["Seeder.Parallelism"] = []string{"notanumber"}
	_, err := s.Parallelism()
	if err == nil {
		t.Fatal("expected error for invalid parallelism, got nil")
	}
}

func TestParallelism_NegativeValue(t *testing.T) {
	s := newTestSeeder()
	s.cfg.(*config.MockConfig).Items["Seeder.Parallelism"] = []string{"-1"}
	got, err := s.Parallelism()
	if err != nil {
		t.Fatalf("Parallelism: %v", err)
	}
	if got != 1 {
		t.Errorf("got %d, want 1 (clamped)", got)
	}
}

func TestCoresMultiplier_Default(t *testing.T) {
	s := newTestSeeder()
	got, err := s.CoresMultiplier()
	if err != nil {
		t.Fatalf("CoresMultiplier: %v", err)
	}
	if got != 1.0 {
		t.Errorf("got %f, want 1.0", got)
	}
}

func TestCoresMultiplier_Set(t *testing.T) {
	s := newTestSeeder()
	s.cfg.(*config.MockConfig).Items["Seeder.CoresMultiplier"] = []string{"1.5"}
	got, err := s.CoresMultiplier()
	if err != nil {
		t.Fatalf("CoresMultiplier: %v", err)
	}
	if got != 1.5 {
		t.Errorf("got %f, want 1.5", got)
	}
}

func TestCoresMultiplier_InvalidValue(t *testing.T) {
	s := newTestSeeder()
	s.cfg.(*config.MockConfig).Items["Seeder.CoresMultiplier"] = []string{"notfloat"}
	_, err := s.CoresMultiplier()
	if err == nil {
		t.Fatal("expected error for invalid CoresMultiplier, got nil")
	}
}

func TestCoresMultiplier_ClampLow(t *testing.T) {
	s := newTestSeeder()
	s.cfg.(*config.MockConfig).Items["Seeder.CoresMultiplier"] = []string{"0.01"}
	got, err := s.CoresMultiplier()
	if err != nil {
		t.Fatalf("CoresMultiplier: %v", err)
	}
	if got != 0.1 {
		t.Errorf("got %f, want 0.1 (clamped)", got)
	}
}
