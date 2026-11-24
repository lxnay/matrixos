package releaser

import (
	"errors"
	"testing"

	"matrixos/vector/lib/config"
)

// configAccessorTestCase describes a single accessor under test.
type configAccessorTestCase struct {
	name    string
	key     string                            // expected config key
	call    func(r *Releaser) (string, error) // the accessor to invoke
	wantVal string                            // expected string value
}

// stringAccessors returns test cases for every string config accessor.
func stringAccessors() []configAccessorTestCase {
	return []configAccessorTestCase{
		{
			name:    "Hostname",
			key:     "Releaser.Hostname",
			call:    func(r *Releaser) (string, error) { return r.Hostname() },
			wantVal: "myhost",
		},
		{
			name:    "HooksDir",
			key:     "Releaser.HooksDir",
			call:    func(r *Releaser) (string, error) { return r.HooksDir() },
			wantVal: "/hooks",
		},
		{
			name:    "ReadOnlyVdb",
			key:     "Releaser.ReadOnlyVdb",
			call:    func(r *Releaser) (string, error) { return r.ReadOnlyVdb() },
			wantVal: "/vdb",
		},
		{
			name:    "LockDir",
			key:     "Releaser.LocksDir",
			call:    func(r *Releaser) (string, error) { return r.LockDir() },
			wantVal: "/locks",
		},
		{
			name:    "LockWaitSeconds",
			key:     "Releaser.LockWaitSeconds",
			call:    func(r *Releaser) (string, error) { return r.LockWaitSeconds() },
			wantVal: "30",
		},
		{
			name:    "SecureBootCertPath",
			key:     "Seeder.SecureBootPublicKey",
			call:    func(r *Releaser) (string, error) { return r.SecureBootCertPath() },
			wantVal: "/sb.pem",
		},
		{
			name:    "SecureBootKekPath",
			key:     "Seeder.SecureBootKekPublicKey",
			call:    func(r *Releaser) (string, error) { return r.SecureBootKekPath() },
			wantVal: "/kek.pem",
		},
		{
			name:    "PrivateGitRepoPath",
			key:     "matrixOS.PrivateGitRepoPath",
			call:    func(r *Releaser) (string, error) { return r.PrivateGitRepoPath() },
			wantVal: "/priv",
		},
		{
			name:    "DefaultPrivateGitRepoPath",
			key:     "matrixOS.DefaultPrivateGitRepoPath",
			call:    func(r *Releaser) (string, error) { return r.DefaultPrivateGitRepoPath() },
			wantVal: "/defpriv",
		},
		{
			name:    "BuildMetadataFile",
			key:     "Seeder.ChrootMetadataDir",
			call:    func(r *Releaser) (string, error) { return r.BuildMetadataFile() },
			wantVal: "/meta",
		},
	}
}

func TestConfigAccessors_ReturnsValue(t *testing.T) {
	for _, tc := range stringAccessors() {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestReleaser()
			r.cfg.(*config.MockConfig).Items[tc.key] = []string{tc.wantVal}

			got, err := tc.call(r)
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
			r := newTestReleaser()
			// Key absent => GetItem returns "" => configItem returns error.

			_, err := tc.call(r)
			if err == nil {
				t.Fatal("expected error for empty config value, got nil")
			}
		})
	}
}

func TestConfigAccessors_PropagatesGetItemError(t *testing.T) {
	for _, tc := range stringAccessors() {
		t.Run(tc.name, func(t *testing.T) {
			wantErr := errors.New("cfg broken")
			r := newTestReleaser()
			r.cfg = &config.ErrConfig{Err: wantErr}

			_, err := tc.call(r)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, wantErr) {
				t.Errorf("got error %v, want %v", err, wantErr)
			}
		})
	}
}

func TestGenerateStaticDeltas_ReturnsConfiguredBool(t *testing.T) {
	r := newTestReleaser()
	r.cfg.(*config.MockConfig).Bools["Releaser.GenerateStaticDeltas"] = true

	got, err := r.GenerateStaticDeltas()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Error("expected true, got false")
	}
}

func TestGenerateStaticDeltas_DefaultFalse(t *testing.T) {
	r := newTestReleaser()

	got, err := r.GenerateStaticDeltas()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Error("expected false, got true")
	}
}

func TestGenerateStaticDeltas_PropagatesError(t *testing.T) {
	wantErr := errors.New("bool broken")
	r := newTestReleaser()
	r.cfg = &config.ErrConfig{Err: wantErr}

	_, err := r.GenerateStaticDeltas()
	if !errors.Is(err, wantErr) {
		t.Errorf("got error %v, want %v", err, wantErr)
	}
}

func TestParallelism_Default(t *testing.T) {
	r := newTestReleaser()
	got, err := r.Parallelism()
	if err != nil {
		t.Fatalf("Parallelism: %v", err)
	}
	if got != 1 {
		t.Errorf("Parallelism = %d, want 1", got)
	}
}

func TestParallelism_Set(t *testing.T) {
	r := newTestReleaser()
	r.cfg.(*config.MockConfig).Items["Releaser.Parallelism"] = []string{"4"}
	got, err := r.Parallelism()
	if err != nil {
		t.Fatalf("Parallelism: %v", err)
	}
	if got != 4 {
		t.Errorf("Parallelism = %d, want 4", got)
	}
}

func TestParallelism_InvalidValue(t *testing.T) {
	r := newTestReleaser()
	r.cfg.(*config.MockConfig).Items["Releaser.Parallelism"] = []string{"notanumber"}
	_, err := r.Parallelism()
	if err == nil {
		t.Fatal("expected error for invalid parallelism, got nil")
	}
}

func TestParallelism_NegativeValue(t *testing.T) {
	r := newTestReleaser()
	r.cfg.(*config.MockConfig).Items["Releaser.Parallelism"] = []string{"-1"}
	got, err := r.Parallelism()
	if err != nil {
		t.Fatalf("Parallelism: %v", err)
	}
	if got != 1 {
		t.Errorf("Parallelism = %d, want 1 (clamped)", got)
	}
}
