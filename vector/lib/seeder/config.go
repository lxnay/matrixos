package seeder

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"matrixos/vector/lib/config"
)

// SeederConfig provides seeder configuration accessors. It wraps a
// config.IConfig and exposes the parsed, validated values that callers
// outside the seeder package need (parallelism settings, paths, etc.).
//
// Seeder embeds *SeederConfig, so all accessors are available on a
// Seeder instance as well. Callers that only need configuration (not
// build operations) can create a SeederConfig directly via
// NewSeederConfig.
type SeederConfig struct {
	cfg config.IConfig
}

// NewSeederConfig creates a new SeederConfig instance.
func NewSeederConfig(cfg config.IConfig) *SeederConfig {
	return &SeederConfig{cfg: cfg}
}

// configItem retrieves a non-empty config string or returns an error.
func (c *SeederConfig) configItem(key string) (string, error) {
	return config.GetRequiredItem(c.cfg, key)
}

// --- Config getters exposed to external callers via SeederConfig ---

func (c *SeederConfig) ParamsExecutableName() (string, error) {
	return c.configItem("Seeder.ParamsExecutableName")
}

func (c *SeederConfig) PrivateGitRepoPath() (string, error) {
	return c.configItem("matrixOS.PrivateGitRepoPath")
}

func (c *SeederConfig) DefaultPrivateGitRepoPath() (string, error) {
	return c.configItem("matrixOS.DefaultPrivateGitRepoPath")
}

// Parallelism returns the maximum number of seeders to build in parallel.
// Defaults to 1 (sequential) if not set or invalid.
func (c *SeederConfig) Parallelism() (int, error) {
	return config.GetParallelism(c.cfg, "Seeder.Parallelism")
}

// MaxMemoryGiB returns the maximum total memory (in GiB) to allocate across
// all parallel workers. 0 means use all available system memory.
func (c *SeederConfig) MaxMemoryGiB() (int, error) {
	v, err := c.cfg.GetItem("Seeder.MaxMemoryGiB")
	if err != nil || v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid Seeder.MaxMemoryGiB %q: %w", v, err)
	}
	if n < 0 {
		return 0, nil
	}
	return n, nil
}

// MaxCores returns the maximum number of CPU cores to allocate across all parallel
// workers. 0 means use all available cores.
func (c *SeederConfig) MaxCores() (int, error) {
	v, err := c.cfg.GetItem("Seeder.MaxCores")
	if err != nil || v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid Seeder.MaxCores %q: %w", v, err)
	}
	if n < 0 {
		return 0, nil
	}
	return n, nil
}

// CoresMultiplier returns the CPU cores oversubscription multiplier.
// Values > 1.0 allow overlapping cpuset ranges between workers, giving
// each worker more cores than a strict partition. Defaults to 1.0.
func (c *SeederConfig) CoresMultiplier() (float64, error) {
	v, err := c.cfg.GetItem("Seeder.CoresMultiplier")
	if err != nil || strings.TrimSpace(v) == "" {
		return 1.0, nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid Seeder.CoresMultiplier %q: %w", v, err)
	}
	if f < 0.1 {
		return 0.1, nil
	}
	return f, nil
}

// --- Internal config getters (only used by Seeder methods) ---

func (s *Seeder) UseLocalGitRepoInsideChroot() (bool, error) {
	return s.cfg.GetBool("Seeder.UseLocalGitRepoInsideChroot")
}

func (s *Seeder) DeleteDotGitFromGitRepo() (bool, error) {
	return s.cfg.GetBool("Seeder.DeleteDotGitFromGitRepo")
}

func (s *Seeder) GitCloneArgs() (string, error) {
	return s.configItem("Seeder.GitCloneArgs")
}

func (s *Seeder) ChrootMetadataDir() (string, error) {
	return s.configItem("Seeder.ChrootMetadataDir")
}

func (s *Seeder) ChrootMetadataDirBuildFileName() (string, error) {
	return s.configItem("Seeder.ChrootMetadataDirBuildFileName")
}

func (s *Seeder) BuildMetadataFile() (string, error) {
	dir, err := s.ChrootMetadataDir()
	if err != nil {
		return "", err
	}
	fileName, err := s.ChrootMetadataDirBuildFileName()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

func (s *Seeder) PhasesStateDir() (string, error) {
	return s.configItem("Seeder.ChrootSeedersPhasesStateDir")
}

func (s *Seeder) PreppersPhasesStateDir() (string, error) {
	return s.configItem("Seeder.ChrootPreppersPhasesStateDir")
}

func (s *Seeder) SeederDoneFlagFilePrefix() (string, error) {
	return s.configItem("Seeder.ChrootSeederDoneFlagFileNamePrefix")
}

func (s *Seeder) PrivateExampleGitRepo() (string, error) {
	return s.configItem("matrixOS.PrivateExampleGitRepo")
}

func (s *Seeder) LockDir() (string, error) {
	return s.configItem("Seeder.LocksDir")
}

func (s *Seeder) LockWaitSeconds() (string, error) {
	return s.configItem("Seeder.LockWaitSeconds")
}

func (s *Seeder) Stage3DownloadUrl() (string, error) {
	return s.configItem("Seeder.Stage3DownloadUrl")
}

func (s *Seeder) DownloadsDir() (string, error) {
	return s.configItem("Seeder.DownloadsDir")
}

func (s *Seeder) DistfilesDir() (string, error) {
	return s.configItem("Seeder.DistfilesDir")
}

func (s *Seeder) BinpkgsDir() (string, error) {
	return s.configItem("Seeder.BinpkgsDir")
}

func (s *Seeder) GpgKeysDir() (string, error) {
	return s.configItem("Seeder.GpgKeysDir")
}

func (s *Seeder) DevDir() (string, error) {
	return s.configItem("matrixOS.Root")
}

func (s *Seeder) DefaultDevDir() (string, error) {
	return s.configItem("matrixOS.DefaultRoot")
}

func (s *Seeder) GitRepo() (string, error) {
	return s.configItem("matrixOS.GitRepo")
}

// SeedsVersioningCadence returns the configured seed versioning cadence.
// Valid values are "daily", "weekly", or "monthly".
// If the value is not set, it defaults to "weekly".
func (s *Seeder) SeedsVersioningCadence() (string, error) {
	v, err := s.cfg.GetItem("Seeder.SeedsVersioningCadence")
	if err != nil {
		return "", err
	}
	if v == "" {
		return "weekly", nil
	}
	switch v {
	case "daily", "weekly", "monthly":
		return v, nil
	default:
		return "", fmt.Errorf(
			"invalid Seeder.SeedsVersioningCadence %q: must be daily, weekly, or monthly", v,
		)
	}
}
