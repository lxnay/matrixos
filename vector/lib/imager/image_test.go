package imager

import (
	"bytes"
	"errors"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
)

// baseImageConfig returns a mock config with all keys needed by Image.
func baseImageConfig() *config.MockConfig {
	return &config.MockConfig{
		Items: map[string][]string{
			"Imager.ImagesDir":                      {"/tmp/images"},
			"Imager.MountDir":                       {"/tmp/mnt"},
			"Imager.ImageSize":                      {"32G"},
			"Imager.EfiPartitionSize":               {"200M"},
			"Imager.BootPartitionSize":              {"1G"},
			"Imager.Compressor":                     {"xz -f -0 -T0"},
			"Imager.EspPartitionType":               {"C12A7328-F81F-11D2-BA4B-00A0C93EC93B"},
			"Imager.BootPartitionType":              {"BC13C2FF-59E6-4262-A352-B275FD6F7172"},
			"Imager.RootPartitionType":              {"4F68BCE3-E8CD-4DB1-96E7-FBCAF984B709"},
			"matrixOS.OsName":                       {"matrixos"},
			"Imager.BootRoot":                       {"/boot"},
			"Imager.EfiRoot":                        {"/efi"},
			"Imager.RelativeEfiBootPath":            {"EFI/BOOT"},
			"Imager.EfiExecutable":                  {"BOOTX64.EFI"},
			"Imager.EfiCertificateFileName":         {"secureboot.pem"},
			"Imager.EfiCertificateFileNameDer":      {"secureboot.der"},
			"Imager.EfiCertificateFileNameKek":      {"secureboot-kek.pem"},
			"Imager.EfiCertificateFileNameKekDer":   {"secureboot-kek.der"},
			"Releaser.ReadOnlyVdb":                  {"/usr/var-db-pkg"},
			"matrixOS.Root":                         {"/opt/matrixos"},
			"Imager.LocksDir":                       {"/tmp/locks"},
			"Imager.LockWaitSeconds":                {"300"},
			"Seeder.ChrootMetadataDir":              {"/etc/matrixos"},
			"Seeder.ChrootMetadataDirBuildFileName": {"build.txt"},
			"matrixOS.LogsDir":                      {"/tmp/logs"},
			"Imager.HooksDir":                       {"/tmp/image/hooks"},
			"Imager.TestsDir":                       {"/tmp/image/tests"},
			"Imager.BootFilesystemType":             {"btrfs"},
			"Imager.Bootloader":                     {"grub"},
		},
	}
}

func newTestImager(cfg *config.MockConfig, ostree *ostree.MockOstree) *Imager {
	im, _ := NewImager(cfg, ostree, filesystems.DefaultMockFsenc(), nil)
	return im
}

func newTestImagerWithRunner(cfg *config.MockConfig, ostree *ostree.MockOstree, runner *runner.MockRunner) *Imager {
	im := newTestImager(cfg, ostree)
	im.runner = runner.Run
	return im
}

// --- Interface compliance ---

func TestImageImplementsIImager(t *testing.T) {
	var _ IImager = (*Imager)(nil)
}

// --- NewImager Tests ---

func TestNewImager(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		im, err := NewImager(baseImageConfig(), &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), nil)
		if err != nil {
			t.Fatalf("NewImager() error: %v", err)
		}
		if im == nil {
			t.Fatal("NewImager() returned nil")
		}
	})

	t.Run("NilConfig", func(t *testing.T) {
		_, err := NewImager(nil, &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), nil)
		if err == nil {
			t.Fatal("expected error for nil config")
		}
	})

	t.Run("NilOstree", func(t *testing.T) {
		_, err := NewImager(baseImageConfig(), nil, filesystems.DefaultMockFsenc(), nil)
		if err == nil {
			t.Fatal("expected error for nil ostree")
		}
	})

	t.Run("NilFsenc", func(t *testing.T) {
		_, err := NewImager(baseImageConfig(), &ostree.MockOstree{}, nil, nil)
		if err == nil {
			t.Fatal("expected error for nil fsenc")
		}
	})
}

// --- Stdout/Stderr Getter Tests ---

func TestImageOutputGetters(t *testing.T) {
	im := newTestImager(baseImageConfig(), &ostree.MockOstree{})

	// Default writers are os.Stdout and os.Stderr.
	if im.Stdout() == nil {
		t.Error("Stdout() should not be nil")
	}
	if im.Stderr() == nil {
		t.Error("Stderr() should not be nil")
	}

	// After setting custom writers.
	var stdout, stderr bytes.Buffer
	im.SetStdout(&stdout)
	im.SetStderr(&stderr)

	if im.Stdout() != &stdout {
		t.Error("Stdout() should return the custom writer")
	}
	if im.Stderr() != &stderr {
		t.Error("Stderr() should return the custom writer")
	}
}

// --- Print Method Tests ---

func TestImagePrintMethods(t *testing.T) {
	im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
	var stdout, stderr bytes.Buffer
	im.SetStdout(&stdout)
	im.SetStderr(&stderr)

	im.Print("hello %s\n", "world")
	if stdout.String() != "hello world\n" {
		t.Errorf("Print() output: %q", stdout.String())
	}

	im.PrintWarning("warn %d\n", 42)
	if stderr.String() != "warn 42\n" {
		t.Errorf("PrintWarning() output: %q", stderr.String())
	}

	stderr.Reset()
	im.PrintError("err %s\n", "test")
	if stderr.String() != "err test\n" {
		t.Errorf("PrintError() output: %q", stderr.String())
	}
}

// --- trackMounts Tests ---

func TestImageTrackMounts(t *testing.T) {
	im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
	var stdout, stderr bytes.Buffer
	im.SetStdout(&stdout)
	im.SetStderr(&stderr)

	im.trackMounts([]string{"/mnt/a", "/mnt/b", "/mnt/c"})
	if len(im.trackedMounts) != 3 {
		t.Errorf("expected 3 mounts, got %d", len(im.trackedMounts))
	}

	im.trackMount("/mnt/d")
	if len(im.trackedMounts) != 4 {
		t.Errorf("expected 4 mounts, got %d", len(im.trackedMounts))
	}
}

// --- SetImageMode Tests ---

func TestSetImageMode(t *testing.T) {
	t.Run("FlashToDeviceSuccess", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.devicePath = "/dev/sda"
		err := im.SetImageMode(ModeFlashToDevice)
		if err != nil {
			t.Fatalf("SetImageMode() error: %v", err)
		}
		if im.ImageMode() != ModeFlashToDevice {
			t.Error("mode should be ModeFlashToDevice")
		}
	})

	t.Run("FlashToDeviceNoDevice", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		// devicePath is empty.
		err := im.SetImageMode(ModeFlashToDevice)
		if err == nil {
			t.Error("should error for empty devicePath")
		}
	})

	t.Run("CreateImageFileSuccess", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.imagePath = "/tmp/test.img"
		err := im.SetImageMode(ModeCreateImageFile)
		if err != nil {
			t.Fatalf("SetImageMode() error: %v", err)
		}
		if im.ImageMode() != ModeCreateImageFile {
			t.Error("mode should be ModeCreateImageFile")
		}
	})

	t.Run("CreateImageFileNoPath", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		// imagePath is empty.
		err := im.SetImageMode(ModeCreateImageFile)
		if err == nil {
			t.Error("should error for empty imagePath")
		}
	})

	t.Run("InvalidMode", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		err := im.SetImageMode(ImageMode(99))
		if err == nil {
			t.Error("should error for invalid mode")
		}
	})
}

// --- NewImager with Options Tests ---

func TestNewImageWithOptions(t *testing.T) {
	t.Run("WithDeviceOpts", func(t *testing.T) {
		opts := &NewImagerOptions{
			EfiDevice:  "/dev/sda1",
			BootDevice: "/dev/sda2",
			RootDevice: "/dev/sda3",
			DevicePath: "/dev/sda",
			Mode:       ModeFlashToDevice,
		}
		im, err := NewImager(baseImageConfig(), &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), opts)
		if err != nil {
			t.Fatalf("NewImager() error: %v", err)
		}
		if im.EfiDevice() != "/dev/sda1" {
			t.Errorf("EfiDevice() = %q", im.EfiDevice())
		}
		if im.BootDevice() != "/dev/sda2" {
			t.Errorf("BootDevice() = %q", im.BootDevice())
		}
		if im.RootDevice() != "/dev/sda3" {
			t.Errorf("RootDevice() = %q", im.RootDevice())
		}
		if im.DevicePath() != "/dev/sda" {
			t.Errorf("DevicePath() = %q", im.DevicePath())
		}
		if im.ImageMode() != ModeFlashToDevice {
			t.Errorf("ImageMode() = %d", im.ImageMode())
		}
	})

	t.Run("WithEncryption", func(t *testing.T) {
		fsenc := &filesystems.MockFsenc{EncryptionEnabled_: true}
		opts := &NewImagerOptions{Mode: ModeCreateImageFile}
		im, err := NewImager(baseImageConfig(), &ostree.MockOstree{}, fsenc, opts)
		if err != nil {
			t.Fatalf("NewImager() error: %v", err)
		}
		if !im.encrypted {
			t.Error("expected encrypted = true")
		}
	})

	t.Run("EncryptionCheckError", func(t *testing.T) {
		fsenc := &filesystems.MockFsenc{EncryptionEnabledErr: errImageTest}
		opts := &NewImagerOptions{} // opts must be non-nil to trigger encryption check assignment
		_, err := NewImager(baseImageConfig(), &ostree.MockOstree{}, fsenc, opts)
		if err == nil {
			t.Error("expected error from EncryptionEnabled")
		}
	})
}

// --- Device Setter/Getter Tests ---

func TestImageDeviceSettersGetters(t *testing.T) {
	im := newTestImager(baseImageConfig(), &ostree.MockOstree{})

	im.SetEfiDevice("/dev/sda1")
	if im.EfiDevice() != "/dev/sda1" {
		t.Errorf("EfiDevice() = %q", im.EfiDevice())
	}

	im.SetBootDevice("/dev/sda2")
	if im.BootDevice() != "/dev/sda2" {
		t.Errorf("BootDevice() = %q", im.BootDevice())
	}

	im.SetRootDevice("/dev/sda3")
	if im.RootDevice() != "/dev/sda3" {
		t.Errorf("RootDevice() = %q", im.RootDevice())
	}

	im.SetDevicePath("/dev/sda")
	if im.DevicePath() != "/dev/sda" {
		t.Errorf("DevicePath() = %q", im.DevicePath())
	}

	im.SetImagePath("/tmp/test.img")
	if im.ImagePath() != "/tmp/test.img" {
		t.Errorf("ImagePath() = %q", im.ImagePath())
	}

	im.SetRootfs("/rootfs")
	if im.Rootfs() != "/rootfs" {
		t.Errorf("Rootfs() = %q", im.Rootfs())
	}

	im.SetRef("matrixos/amd64/gnome")
	if im.Ref() != "matrixos/amd64/gnome" {
		t.Errorf("Ref() = %q", im.Ref())
	}
}

// --- Mount Point Getter Tests ---

func TestImageMountGetters(t *testing.T) {
	im := newTestImager(baseImageConfig(), &ostree.MockOstree{})

	// Initially empty.
	if im.EfifsMount() != "" {
		t.Errorf("EfifsMount() should be empty, got %q", im.EfifsMount())
	}
	if im.BootfsMount() != "" {
		t.Errorf("BootfsMount() should be empty, got %q", im.BootfsMount())
	}
	if im.RootfsMount() != "" {
		t.Errorf("RootfsMount() should be empty, got %q", im.RootfsMount())
	}
}

var errImageTest = errors.New("test error")
