package imager

import (
	"path/filepath"

	"matrixos/vector/lib/config"
)

// ImagerConfig provides imager configuration accessors. It wraps a
// config.IConfig and exposes the parsed, validated values that callers
// outside the imager package need (compressor, partition types, feature
// flags, etc.).
//
// Imager embeds *ImagerConfig, so all accessors are available on an
// Imager instance as well. Callers that only need configuration (not
// build operations) can create an ImagerConfig directly via
// NewImagerConfig.
type ImagerConfig struct {
	cfg config.IConfig
}

// NewImagerConfig creates a new ImagerConfig instance.
func NewImagerConfig(cfg config.IConfig) *ImagerConfig {
	return &ImagerConfig{cfg: cfg}
}

// --- Config accessors ---
// Each method retrieves a single configuration value and validates it.

// configItem retrieves a non-empty config string or returns an error.
func (c *ImagerConfig) configItem(key string) (string, error) {
	return config.GetRequiredItem(c.cfg, key)
}

func (c *ImagerConfig) ImagesDir() (string, error) {
	return c.configItem("Imager.ImagesDir")
}

func (c *ImagerConfig) MountDir() (string, error) {
	return c.configItem("Imager.MountDir")
}

func (c *ImagerConfig) ImageSize() (string, error) {
	return c.configItem("Imager.ImageSize")
}

func (c *ImagerConfig) EfiPartitionSize() (string, error) {
	return c.configItem("Imager.EfiPartitionSize")
}

func (c *ImagerConfig) BootPartitionSize() (string, error) {
	return c.configItem("Imager.BootPartitionSize")
}

func (c *ImagerConfig) Compressor() (string, error) {
	return c.configItem("Imager.Compressor")
}

func (c *ImagerConfig) EspPartitionType() (string, error) {
	return c.configItem("Imager.EspPartitionType")
}

func (c *ImagerConfig) BootPartitionType() (string, error) {
	return c.configItem("Imager.BootPartitionType")
}

func (c *ImagerConfig) RootPartitionType() (string, error) {
	return c.configItem("Imager.RootPartitionType")
}

func (c *ImagerConfig) OsName() (string, error) {
	return c.configItem("matrixOS.OsName")
}

func (c *ImagerConfig) BootRoot() (string, error) {
	return c.configItem("Imager.BootRoot")
}

func (c *ImagerConfig) EfiRoot() (string, error) {
	return c.configItem("Imager.EfiRoot")
}

func (c *ImagerConfig) RelativeEfiBootPath() (string, error) {
	return c.configItem("Imager.RelativeEfiBootPath")
}

func (c *ImagerConfig) EfiExecutable() (string, error) {
	return c.configItem("Imager.EfiExecutable")
}

func (c *ImagerConfig) EfiCertificateFileName() (string, error) {
	return c.configItem("Imager.EfiCertificateFileName")
}

func (c *ImagerConfig) EfiCertificateFileNameDer() (string, error) {
	return c.configItem("Imager.EfiCertificateFileNameDer")
}

func (c *ImagerConfig) EfiCertificateFileNameKek() (string, error) {
	return c.configItem("Imager.EfiCertificateFileNameKek")
}

func (c *ImagerConfig) EfiCertificateFileNameKekDer() (string, error) {
	return c.configItem("Imager.EfiCertificateFileNameKekDer")
}

func (c *ImagerConfig) ReadOnlyVdb() (string, error) {
	return c.configItem("Releaser.ReadOnlyVdb")
}

func (c *ImagerConfig) DevDir() (string, error) {
	return c.configItem("matrixOS.Root")
}

func (c *ImagerConfig) HooksDir() (string, error) {
	return c.configItem("Imager.HooksDir")
}

func (c *ImagerConfig) TestsDir() (string, error) {
	return c.configItem("Imager.TestsDir")
}

func (c *ImagerConfig) LockDir() (string, error) {
	return c.configItem("Imager.LocksDir")
}

func (c *ImagerConfig) LockWaitSeconds() (string, error) {
	return c.configItem("Imager.LockWaitSeconds")
}

func (c *ImagerConfig) BuildMetadataFile() (string, error) {
	metadataDir, err := c.configItem("Seeder.ChrootMetadataDir")
	if err != nil {
		return "", err
	}
	buildFileName, err := c.configItem("Seeder.ChrootMetadataDirBuildFileName")
	if err != nil {
		return "", err
	}
	return filepath.Join(metadataDir, buildFileName), nil
}

func (c *ImagerConfig) CreateQcow2() (bool, error) {
	return c.cfg.GetBool("Imager.CreateQcow2")
}

func (c *ImagerConfig) Productionize() (bool, error) {
	return c.cfg.GetBool("Imager.Productionize")
}

func (c *ImagerConfig) ImageTests() (bool, error) {
	return c.cfg.GetBool("Imager.ImageTests")
}

// Parallelism returns the maximum number of images to build
// in parallel.  Defaults to 1 (sequential) if not set or invalid.
func (c *ImagerConfig) Parallelism() (int, error) {
	return config.GetParallelism(c.cfg, "Imager.Parallelism")
}

// BootFilesystemType returns the filesystem type for the boot partition.
// Valid values are "btrfs" and "vfat".
func (c *ImagerConfig) BootFilesystemType() (string, error) {
	return c.configItem("Imager.BootFilesystemType")
}

// Bootloader returns the bootloader to install in the generated image.
// Valid values are "grub" and "systemd-boot".
func (c *ImagerConfig) Bootloader() (string, error) {
	v, err := c.cfg.GetItem("Imager.Bootloader")
	if err != nil || v == "" {
		return "grub", nil
	}
	return v, nil
}
