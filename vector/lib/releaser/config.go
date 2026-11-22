package releaser

import "matrixos/vector/lib/config"

// ReleaserConfig provides releaser configuration accessors. It wraps a
// config.IConfig and exposes the parsed, validated values that callers
// outside the releaser package need.
//
// Releaser embeds *ReleaserConfig, so all accessors are available on a
// Releaser instance as well. Callers that only need configuration (not
// build operations) can create a ReleaserConfig directly via
// NewReleaserConfig.
type ReleaserConfig struct {
	cfg config.IConfig
}

// NewReleaserConfig creates a new ReleaserConfig instance.
func NewReleaserConfig(cfg config.IConfig) *ReleaserConfig {
	return &ReleaserConfig{cfg: cfg}
}

// --- Config accessors ---
// Each method retrieves a single configuration value and validates it.

// configItem retrieves a non-empty config string or returns an error.
func (c *ReleaserConfig) configItem(key string) (string, error) {
	return config.GetRequiredItem(c.cfg, key)
}

func (c *ReleaserConfig) Hostname() (string, error) {
	return c.configItem("Releaser.Hostname")
}

func (c *ReleaserConfig) HooksDir() (string, error) {
	return c.configItem("Releaser.HooksDir")
}

func (c *ReleaserConfig) DevDir() (string, error) {
	return c.configItem("matrixOS.Root")
}

func (c *ReleaserConfig) ReadOnlyVdb() (string, error) {
	return c.configItem("Releaser.ReadOnlyVdb")
}

func (c *ReleaserConfig) LockDir() (string, error) {
	return c.configItem("Releaser.LocksDir")
}

func (c *ReleaserConfig) LockWaitSeconds() (string, error) {
	return c.configItem("Releaser.LockWaitSeconds")
}

func (c *ReleaserConfig) GenerateStaticDeltas() (bool, error) {
	return c.cfg.GetBool("Releaser.GenerateStaticDeltas")
}

func (c *ReleaserConfig) SecureBootCertPath() (string, error) {
	return c.configItem("Seeder.SecureBootPublicKey")
}

func (c *ReleaserConfig) SecureBootKekPath() (string, error) {
	return c.configItem("Seeder.SecureBootKekPublicKey")
}

func (c *ReleaserConfig) PrivateGitRepoPath() (string, error) {
	return c.configItem("matrixOS.PrivateGitRepoPath")
}

func (c *ReleaserConfig) DefaultPrivateGitRepoPath() (string, error) {
	return c.configItem("matrixOS.DefaultPrivateGitRepoPath")
}

func (c *ReleaserConfig) BuildMetadataFile() (string, error) {
	return c.configItem("Seeder.ChrootMetadataDir")
}

// Parallelism returns the maximum number of releases to build
// in parallel.  Defaults to 1 (sequential) if not set or invalid.
func (c *ReleaserConfig) Parallelism() (int, error) {
	return config.GetParallelism(c.cfg, "Releaser.Parallelism")
}
