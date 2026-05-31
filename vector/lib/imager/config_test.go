package imager

import (
	"errors"
	"path/filepath"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
)

// --- Config Accessor Tests ---

func TestConfigAccessors(t *testing.T) {
	cfg := baseImageConfig()
	im := newTestImager(cfg, &ostree.MockOstree{})

	tests := []struct {
		name     string
		fn       func() (string, error)
		expected string
	}{
		{"ImagesDir", im.ImagesDir, "/tmp/images"},
		{"MountDir", im.MountDir, "/tmp/mnt"},
		{"ImageSize", im.ImageSize, "32G"},
		{"EfiPartitionSize", im.EfiPartitionSize, "200M"},
		{"BootPartitionSize", im.BootPartitionSize, "1G"},
		{"Compressor", im.Compressor, "xz -f -0 -T0"},
		{"EspPartitionType", im.EspPartitionType, "C12A7328-F81F-11D2-BA4B-00A0C93EC93B"},
		{"BootPartitionType", im.BootPartitionType, "BC13C2FF-59E6-4262-A352-B275FD6F7172"},
		{"RootPartitionType", im.RootPartitionType, "4F68BCE3-E8CD-4DB1-96E7-FBCAF984B709"},
		{"OsName", im.OsName, "matrixos"},
		{"BootRoot", im.BootRoot, "/boot"},
		{"EfiRoot", im.EfiRoot, "/efi"},
		{"RelativeEfiBootPath", im.RelativeEfiBootPath, "EFI/BOOT"},
		{"EfiExecutable", im.EfiExecutable, "BOOTX64.EFI"},
		{"EfiCertificateFileName", im.EfiCertificateFileName, "secureboot.pem"},
		{"EfiCertificateFileNameDer", im.EfiCertificateFileNameDer, "secureboot.der"},
		{"EfiCertificateFileNameKek", im.EfiCertificateFileNameKek, "secureboot-kek.pem"},
		{"EfiCertificateFileNameKekDer", im.EfiCertificateFileNameKekDer, "secureboot-kek.der"},
		{"ReadOnlyVdb", im.ReadOnlyVdb, "/usr/var-db-pkg"},
		{"DevDir", im.DevDir, "/opt/matrixos"},
		{"LockDir", im.LockDir, "/tmp/locks"},
		{"LockWaitSeconds", im.LockWaitSeconds, "300"},
		{"HooksDir", im.HooksDir, "/tmp/image/hooks"},
		{"TestsDir", im.TestsDir, "/tmp/image/tests"},
		{"BootFilesystemType", im.BootFilesystemType, "btrfs"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := tt.fn()
			if err != nil {
				t.Fatalf("%s() error: %v", tt.name, err)
			}
			if val != tt.expected {
				t.Errorf("%s() = %q, want %q", tt.name, val, tt.expected)
			}
		})
	}
}

func TestConfigAccessorsEmptyValue(t *testing.T) {
	accessors := []struct {
		key  string
		name string
		fn   func(*Imager) (string, error)
	}{
		{"Imager.ImagesDir", "ImagesDir", func(im *Imager) (string, error) { return im.ImagesDir() }},
		{"Imager.MountDir", "MountDir", func(im *Imager) (string, error) { return im.MountDir() }},
		{"Imager.ImageSize", "ImageSize", func(im *Imager) (string, error) { return im.ImageSize() }},
		{"matrixOS.OsName", "OsName", func(im *Imager) (string, error) { return im.OsName() }},
		{"Imager.LocksDir", "LockDir", func(im *Imager) (string, error) { return im.LockDir() }},
		{"Imager.HooksDir", "HooksDir", func(im *Imager) (string, error) { return im.HooksDir() }},
		{"Imager.TestsDir", "TestsDir", func(im *Imager) (string, error) { return im.TestsDir() }},
	}

	for _, tt := range accessors {
		t.Run(tt.name+"_Empty", func(t *testing.T) {
			cfg := baseImageConfig()
			cfg.Items[tt.key] = []string{""}
			im := newTestImager(cfg, &ostree.MockOstree{})
			_, err := tt.fn(im)
			if err == nil {
				t.Errorf("%s() should return error for empty value", tt.name)
			}
		})
	}
}

func TestConfigAccessorsConfigError(t *testing.T) {
	ec := &config.ErrConfig{Err: errors.New("cfg error")}
	im, _ := NewImager(ec, &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), nil)
	im.runner = runner.NewMockRunner().Run

	accessors := []struct {
		name string
		fn   func() (string, error)
	}{
		{"ImagesDir", im.ImagesDir},
		{"MountDir", im.MountDir},
		{"ImageSize", im.ImageSize},
		{"OsName", im.OsName},
		{"BootRoot", im.BootRoot},
		{"EfiRoot", im.EfiRoot},
		{"LockDir", im.LockDir},
		{"HooksDir", im.HooksDir},
		{"TestsDir", im.TestsDir},
	}

	for _, tt := range accessors {
		t.Run(tt.name+"_ConfigError", func(t *testing.T) {
			_, err := tt.fn()
			if err == nil {
				t.Errorf("%s() should return error from broken config", tt.name)
			}
		})
	}
}

// --- BuildMetadataFile Tests ---

func TestBuildMetadataFile(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		cfg := baseImageConfig()
		im := newTestImager(cfg, &ostree.MockOstree{})
		result, err := im.BuildMetadataFile()
		if err != nil {
			t.Fatalf("BuildMetadataFile() error: %v", err)
		}
		expected := filepath.Join("/etc/matrixos", "build.txt")
		if result != expected {
			t.Errorf("BuildMetadataFile() = %q, want %q", result, expected)
		}
	})

	t.Run("EmptyDir", func(t *testing.T) {
		cfg := baseImageConfig()
		cfg.Items["Seeder.ChrootMetadataDir"] = []string{""}
		im := newTestImager(cfg, &ostree.MockOstree{})
		_, err := im.BuildMetadataFile()
		if err == nil {
			t.Error("should error for empty metadata dir")
		}
	})

	t.Run("EmptyFileName", func(t *testing.T) {
		cfg := baseImageConfig()
		cfg.Items["Seeder.ChrootMetadataDirBuildFileName"] = []string{""}
		im := newTestImager(cfg, &ostree.MockOstree{})
		_, err := im.BuildMetadataFile()
		if err == nil {
			t.Error("should error for empty build file name")
		}
	})

	t.Run("ConfigError", func(t *testing.T) {
		ec := &config.ErrConfig{Err: errors.New("cfg error")}
		im, _ := NewImager(ec, &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), nil)
		_, err := im.BuildMetadataFile()
		if err == nil {
			t.Error("should error from broken config")
		}
	})
}

// --- Boolean Config Accessor Tests ---

func TestBoolConfigAccessors(t *testing.T) {
	t.Run("CreateQcow2_True", func(t *testing.T) {
		cfg := baseImageConfig()
		cfg.Bools = map[string]bool{"Imager.CreateQcow2": true}
		im := newTestImager(cfg, &ostree.MockOstree{})
		v, err := im.CreateQcow2()
		if err != nil {
			t.Fatalf("CreateQcow2() error: %v", err)
		}
		if !v {
			t.Error("expected true")
		}
	})

	t.Run("CreateQcow2_False", func(t *testing.T) {
		cfg := baseImageConfig()
		cfg.Bools = map[string]bool{"Imager.CreateQcow2": false}
		im := newTestImager(cfg, &ostree.MockOstree{})
		v, err := im.CreateQcow2()
		if err != nil {
			t.Fatalf("CreateQcow2() error: %v", err)
		}
		if v {
			t.Error("expected false")
		}
	})

	t.Run("CreateQcow2_ConfigError", func(t *testing.T) {
		ec := &config.ErrConfig{Err: errors.New("cfg error")}
		im, _ := NewImager(ec, &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), nil)
		_, err := im.CreateQcow2()
		if err == nil {
			t.Error("should error from broken config")
		}
	})

	t.Run("Productionize_True", func(t *testing.T) {
		cfg := baseImageConfig()
		cfg.Bools = map[string]bool{"Imager.Productionize": true}
		im := newTestImager(cfg, &ostree.MockOstree{})
		v, err := im.Productionize()
		if err != nil {
			t.Fatalf("Productionize() error: %v", err)
		}
		if !v {
			t.Error("expected true")
		}
	})

	t.Run("Productionize_ConfigError", func(t *testing.T) {
		ec := &config.ErrConfig{Err: errors.New("cfg error")}
		im, _ := NewImager(ec, &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), nil)
		_, err := im.Productionize()
		if err == nil {
			t.Error("should error from broken config")
		}
	})

	t.Run("ImageTests_True", func(t *testing.T) {
		cfg := baseImageConfig()
		cfg.Bools = map[string]bool{"Imager.ImageTests": true}
		im := newTestImager(cfg, &ostree.MockOstree{})
		v, err := im.ImageTests()
		if err != nil {
			t.Fatalf("ImageTests() error: %v", err)
		}
		if !v {
			t.Error("expected true")
		}
	})

	t.Run("ImageTests_ConfigError", func(t *testing.T) {
		ec := &config.ErrConfig{Err: errors.New("cfg error")}
		im, _ := NewImager(ec, &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), nil)
		_, err := im.ImageTests()
		if err == nil {
			t.Error("should error from broken config")
		}
	})
}

// --- Additional Empty Value Config Tests ---

func TestConfigAccessorsEmptyValueExtended(t *testing.T) {
	accessors := []struct {
		key  string
		name string
		fn   func(*Imager) (string, error)
	}{
		{"Imager.EfiPartitionSize", "EfiPartitionSize", func(im *Imager) (string, error) { return im.EfiPartitionSize() }},
		{"Imager.BootPartitionSize", "BootPartitionSize", func(im *Imager) (string, error) { return im.BootPartitionSize() }},
		{"Imager.Compressor", "Compressor", func(im *Imager) (string, error) { return im.Compressor() }},
		{"Imager.EspPartitionType", "EspPartitionType", func(im *Imager) (string, error) { return im.EspPartitionType() }},
		{"Imager.BootPartitionType", "BootPartitionType", func(im *Imager) (string, error) { return im.BootPartitionType() }},
		{"Imager.RootPartitionType", "RootPartitionType", func(im *Imager) (string, error) { return im.RootPartitionType() }},
		{"Imager.BootRoot", "BootRoot", func(im *Imager) (string, error) { return im.BootRoot() }},
		{"Imager.EfiRoot", "EfiRoot", func(im *Imager) (string, error) { return im.EfiRoot() }},
		{"Imager.RelativeEfiBootPath", "RelativeEfiBootPath", func(im *Imager) (string, error) { return im.RelativeEfiBootPath() }},
		{"Imager.EfiExecutable", "EfiExecutable", func(im *Imager) (string, error) { return im.EfiExecutable() }},
		{"Imager.EfiCertificateFileName", "EfiCertificateFileName", func(im *Imager) (string, error) { return im.EfiCertificateFileName() }},
		{"Imager.EfiCertificateFileNameDer", "EfiCertificateFileNameDer", func(im *Imager) (string, error) { return im.EfiCertificateFileNameDer() }},
		{"Imager.EfiCertificateFileNameKek", "EfiCertificateFileNameKek", func(im *Imager) (string, error) { return im.EfiCertificateFileNameKek() }},
		{"Imager.EfiCertificateFileNameKekDer", "EfiCertificateFileNameKekDer", func(im *Imager) (string, error) { return im.EfiCertificateFileNameKekDer() }},
		{"Releaser.ReadOnlyVdb", "ReadOnlyVdb", func(im *Imager) (string, error) { return im.ReadOnlyVdb() }},
		{"matrixOS.Root", "DevDir", func(im *Imager) (string, error) { return im.DevDir() }},
		{"Imager.LockWaitSeconds", "LockWaitSeconds", func(im *Imager) (string, error) { return im.LockWaitSeconds() }},
	}

	for _, tt := range accessors {
		t.Run(tt.name+"_Empty", func(t *testing.T) {
			cfg := baseImageConfig()
			cfg.Items[tt.key] = []string{""}
			im := newTestImager(cfg, &ostree.MockOstree{})
			_, err := tt.fn(im)
			if err == nil {
				t.Errorf("%s() should return error for empty value", tt.name)
			}
		})
	}
}

// --- Additional Config Error Tests ---

func TestConfigAccessorsConfigErrorExtended(t *testing.T) {
	ec := &config.ErrConfig{Err: errors.New("cfg error")}
	im, _ := NewImager(ec, &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), nil)
	im.runner = runner.NewMockRunner().Run

	accessors := []struct {
		name string
		fn   func() (string, error)
	}{
		{"EfiPartitionSize", im.EfiPartitionSize},
		{"BootPartitionSize", im.BootPartitionSize},
		{"Compressor", im.Compressor},
		{"EspPartitionType", im.EspPartitionType},
		{"BootPartitionType", im.BootPartitionType},
		{"RootPartitionType", im.RootPartitionType},
		{"RelativeEfiBootPath", im.RelativeEfiBootPath},
		{"EfiExecutable", im.EfiExecutable},
		{"EfiCertificateFileName", im.EfiCertificateFileName},
		{"EfiCertificateFileNameDer", im.EfiCertificateFileNameDer},
		{"EfiCertificateFileNameKek", im.EfiCertificateFileNameKek},
		{"EfiCertificateFileNameKekDer", im.EfiCertificateFileNameKekDer},
		{"ReadOnlyVdb", im.ReadOnlyVdb},
		{"DevDir", im.DevDir},
		{"LockWaitSeconds", im.LockWaitSeconds},
	}

	for _, tt := range accessors {
		t.Run(tt.name+"_ConfigError", func(t *testing.T) {
			_, err := tt.fn()
			if err == nil {
				t.Errorf("%s() should return error from broken config", tt.name)
			}
		})
	}
}
