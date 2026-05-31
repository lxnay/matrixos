package imager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
)

// --- CreateImage Tests ---

func TestCreateImage(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		tmpDir := t.TempDir()
		imagePath := filepath.Join(tmpDir, "subdir", "test.img")
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.imagePath = imagePath
		im.mode = ModeCreateImageFile

		err := im.CreateImage("1M")
		if err != nil {
			t.Fatalf("CreateImage() error: %v", err)
		}
		// Verify sparse file was created with the right size.
		info, err := os.Stat(imagePath)
		if err != nil {
			t.Fatalf("image file not created: %v", err)
		}
		expectedSize := int64(1024 * 1024)
		if info.Size() != expectedSize {
			t.Errorf("expected size %d, got %d", expectedSize, info.Size())
		}
	})

	t.Run("SuccessWithGigabytes", func(t *testing.T) {
		tmpDir := t.TempDir()
		imagePath := filepath.Join(tmpDir, "test.img")
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.imagePath = imagePath
		im.mode = ModeCreateImageFile

		err := im.CreateImage("1G")
		if err != nil {
			t.Fatalf("CreateImage() error: %v", err)
		}
		info, err := os.Stat(imagePath)
		if err != nil {
			t.Fatalf("image file not created: %v", err)
		}
		expectedSize := int64(1024 * 1024 * 1024)
		if info.Size() != expectedSize {
			t.Errorf("expected size %d, got %d", expectedSize, info.Size())
		}
	})

	t.Run("CreatesParentDirectories", func(t *testing.T) {
		tmpDir := t.TempDir()
		imagePath := filepath.Join(tmpDir, "a", "b", "c", "test.img")
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.imagePath = imagePath
		im.mode = ModeCreateImageFile

		err := im.CreateImage("1K")
		if err != nil {
			t.Fatalf("CreateImage() error: %v", err)
		}
		info, err := os.Stat(imagePath)
		if err != nil {
			t.Fatalf("image file not created: %v", err)
		}
		if info.Size() != 1024 {
			t.Errorf("expected size 1024, got %d", info.Size())
		}
	})

	t.Run("EmptyPath", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.mode = ModeCreateImageFile
		err := im.CreateImage("32G")
		if err == nil {
			t.Error("should error for empty imagePath")
		}
	})

	t.Run("EmptySize", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.imagePath = "/tmp/test.img"
		im.mode = ModeCreateImageFile
		err := im.CreateImage("")
		if err == nil {
			t.Error("should error for empty imageSize")
		}
	})

	t.Run("InvalidSize", func(t *testing.T) {
		tmpDir := t.TempDir()
		imagePath := filepath.Join(tmpDir, "test.img")
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.imagePath = imagePath
		im.mode = ModeCreateImageFile

		err := im.CreateImage("notanumber")
		if err == nil {
			t.Error("should error for invalid size")
		}
	})
}

// --- ClearPartitionTable Tests ---

func TestClearPartitionTable(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		runner := runner.NewMockRunner()
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, runner)
		im.devicePath = "/dev/sda"

		err := im.ClearPartitionTable()
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if len(runner.Calls) != 2 {
			t.Fatalf("expected 2 sgdisk calls, got %d", len(runner.Calls))
		}
		if runner.Calls[0].Name != "sgdisk" {
			t.Errorf("call 0: expected sgdisk, got %q", runner.Calls[0].Name)
		}
		if runner.Calls[1].Name != "sgdisk" {
			t.Errorf("call 1: expected sgdisk, got %q", runner.Calls[1].Name)
		}
	})

	t.Run("EmptyDevice", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		err := im.ClearPartitionTable()
		if err == nil {
			t.Error("should error for empty devicePath")
		}
	})

	t.Run("FirstSgdiskFails", func(t *testing.T) {
		runner := runner.NewMockRunnerFailOnCall(0, errors.New("sgdisk error"))
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, runner)
		im.devicePath = "/dev/sda"

		err := im.ClearPartitionTable()
		if err == nil {
			t.Error("should propagate sgdisk error")
		}
	})
}

// --- DatedFsLabel Tests ---

func TestDatedFsLabel(t *testing.T) {
	im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
	label := im.DatedFsLabel()
	expected := time.Now().Format("20060102")
	if label != expected {
		t.Errorf("DatedFsLabel() = %q, want %q", label, expected)
	}
}

// --- PartitionDevices Tests ---

func TestPartitionDevices(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		runner := runner.NewMockRunner()
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, runner)

		im.devicePath = "/dev/loop0"
		err := im.PartitionDevices("200M", "1G", "32G")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		// 4 sgdisk calls + 1 partprobe = 5.
		if len(runner.Calls) != 5 {
			t.Fatalf("expected 5 runner calls, got %d", len(runner.Calls))
		}
		commands := make([]string, len(runner.Calls))
		for i, c := range runner.Calls {
			commands[i] = c.Name
		}
		if commands[0] != "sgdisk" || commands[1] != "sgdisk" || commands[2] != "sgdisk" || commands[3] != "sgdisk" {
			t.Errorf("expected 4 sgdisk calls, got %v", commands[:4])
		}
		if commands[4] != "partprobe" {
			t.Errorf("expected partprobe call, got %q", commands[4])
		}
	})

	t.Run("EmptyParams", func(t *testing.T) {
		runner := runner.NewMockRunner()
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, runner)

		im.devicePath = "/dev/x"
		if err := im.PartitionDevices("", "1G", "32G"); err == nil {
			t.Error("should error for empty efiSize")
		}
		if err := im.PartitionDevices("200M", "", "32G"); err == nil {
			t.Error("should error for empty bootSize")
		}
		if err := im.PartitionDevices("200M", "1G", ""); err == nil {
			t.Error("should error for empty imageSize")
		}
		im.devicePath = ""
		if err := im.PartitionDevices("200M", "1G", "32G"); err == nil {
			t.Error("should error for empty devicePath")
		}
	})

	t.Run("SgdiskFails", func(t *testing.T) {
		runner := runner.NewMockRunnerFailOnCall(0, errors.New("sgdisk failed"))
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, runner)
		im.devicePath = "/dev/loop0"

		err := im.PartitionDevices("200M", "1G", "32G")
		if err == nil {
			t.Error("should propagate sgdisk error")
		}
	})

	t.Run("ConfigError", func(t *testing.T) {
		ec := &config.ErrConfig{Err: errors.New("cfg error")}
		im, _ := NewImager(ec, &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), nil)
		runner := runner.NewMockRunner()
		im.runner = runner.Run
		im.devicePath = "/dev/loop0"

		err := im.PartitionDevices("200M", "1G", "32G")
		if err == nil {
			t.Error("should error from broken config")
		}
	})
}

// --- FormatEfifs Tests ---

func TestFormatEfifs(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		runner := runner.NewMockRunner()
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, runner)
		im.efiDevice = "/dev/loop0p1"

		err := im.FormatEfifs()
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if len(runner.Calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(runner.Calls))
		}
		if runner.Calls[0].Name != "mkfs.vfat" {
			t.Errorf("expected mkfs.vfat, got %q", runner.Calls[0].Name)
		}
	})

	t.Run("Empty", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		if err := im.FormatEfifs(); err == nil {
			t.Error("should error for empty device")
		}
	})
}

// --- MountEfifs Tests ---

func TestMountEfifs(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		tmpDir := t.TempDir()
		mountPoint := filepath.Join(tmpDir, "efi")
		runner := runner.NewMockRunner()
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, runner)
		im.efiDevice = "/dev/loop0p1"

		err := im.MountEfifs(mountPoint)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if len(runner.Calls) != 1 || runner.Calls[0].Name != "mount" {
			t.Errorf("expected mount call, got %v", runner.Calls)
		}
	})

	t.Run("EmptyParams", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		if err := im.MountEfifs("/tmp/efi"); err == nil {
			t.Error("should error for empty device")
		}
		im.efiDevice = "/dev/x"
		if err := im.MountEfifs(""); err == nil {
			t.Error("should error for empty mount point")
		}
	})
}

// --- FormatBootfs Tests ---

func TestFormatBootfs(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		runner := runner.NewMockRunner()
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, runner)
		im.bootDevice = "/dev/loop0p2"

		err := im.FormatBootfs()
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if runner.Calls[0].Name != "mkfs.btrfs" {
			t.Errorf("expected mkfs.btrfs, got %q", runner.Calls[0].Name)
		}
	})

	t.Run("Vfat", func(t *testing.T) {
		r := runner.NewMockRunner()
		cfg := baseImageConfig()
		cfg.Items["Imager.BootFilesystemType"] = []string{"vfat"}
		im := newTestImagerWithRunner(cfg, &ostree.MockOstree{}, r)
		im.bootDevice = "/dev/loop0p2"
		if err := im.FormatBootfs(); err != nil {
			t.Fatalf("error: %v", err)
		}
		if r.Calls[0].Name != "mkfs.vfat" {
			t.Errorf("expected mkfs.vfat, got %q", r.Calls[0].Name)
		}
	})

	t.Run("InvalidFsType", func(t *testing.T) {
		r := runner.NewMockRunner()
		cfg := baseImageConfig()
		cfg.Items["Imager.BootFilesystemType"] = []string{"ext4"}
		im := newTestImagerWithRunner(cfg, &ostree.MockOstree{}, r)
		im.bootDevice = "/dev/loop0p2"
		if err := im.FormatBootfs(); err == nil {
			t.Error("expected error for unsupported fs type")
		}
	})

	t.Run("Empty", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		if err := im.FormatBootfs(); err == nil {
			t.Error("should error for empty device")
		}
	})
}

// --- MountBootfs Tests ---

func TestMountBootfs(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		tmpDir := t.TempDir()
		mountPoint := filepath.Join(tmpDir, "boot")
		runner := runner.NewMockRunner()
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, runner)
		im.bootDevice = "/dev/loop0p2"

		err := im.MountBootfs(mountPoint)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if len(runner.Calls) != 1 || runner.Calls[0].Name != "mount" {
			t.Errorf("expected mount call, got %v", runner.Calls)
		}
	})

	t.Run("VfatPassesTFlag", func(t *testing.T) {
		tmpDir := t.TempDir()
		mountPoint := filepath.Join(tmpDir, "boot")
		r := runner.NewMockRunner()
		cfg := baseImageConfig()
		cfg.Items["Imager.BootFilesystemType"] = []string{"vfat"}
		im := newTestImagerWithRunner(cfg, &ostree.MockOstree{}, r)
		im.bootDevice = "/dev/loop0p2"
		if err := im.MountBootfs(mountPoint); err != nil {
			t.Fatalf("error: %v", err)
		}
		args := r.Calls[0].Args
		if len(args) < 2 || args[0] != "-t" || args[1] != "vfat" {
			t.Errorf("expected -t vfat in mount args, got %v", args)
		}
	})

	t.Run("EmptyParams", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		if err := im.MountBootfs("/boot"); err == nil {
			t.Error("should error for empty device")
		}
		im.bootDevice = "/dev/x"
		if err := im.MountBootfs(""); err == nil {
			t.Error("should error for empty mount point")
		}
	})
}

// --- FormatRootfs Tests ---

func TestFormatRootfs(t *testing.T) {
	runner := runner.NewMockRunner()
	im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, runner)
	im.rootDevice = "/dev/loop0p3"

	err := im.FormatRootfs()
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if runner.Calls[0].Name != "mkfs.btrfs" {
		t.Errorf("expected mkfs.btrfs, got %q", runner.Calls[0].Name)
	}
}

// --- RootfsKernelArgs Tests ---

func TestRootfsKernelArgs(t *testing.T) {
	im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
	args := im.RootfsKernelArgs()
	if len(args) != 1 || args[0] != "rootflags=discard=async" {
		t.Errorf("unexpected kernel args: %v", args)
	}
}

// --- MountRootfs Tests ---

func TestMountRootfs(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		runner := runner.NewMockRunner()
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, runner)
		im.rootDevice = "/dev/loop0p3"

		err := im.MountRootfs("/tmp/rootfs")
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if runner.Calls[0].Name != "mount" {
			t.Errorf("expected mount, got %q", runner.Calls[0].Name)
		}
		// Check btrfs options.
		found := false
		for _, arg := range runner.Calls[0].Args {
			if strings.Contains(arg, "compress-force=zstd:6") {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected btrfs compression options in mount args")
		}
	})

	t.Run("EmptyParams", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		if err := im.MountRootfs("/tmp/mnt"); err == nil {
			t.Error("should error for empty rootDevice")
		}
		im.rootDevice = "/dev/x"
		if err := im.MountRootfs(""); err == nil {
			t.Error("should error for empty mountRootfs")
		}
	})
}

// --- FinalizeFilesystems Tests ---

func TestFinalizeFilesystems(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		runner := runner.NewMockRunner()
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, runner)
		im.rootfsMount = "/mnt/rootfs"
		im.bootfsMount = "/mnt/boot"
		im.efifsMount = "/mnt/efi"

		err := im.FinalizeFilesystems()
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if len(runner.Calls) != 2 {
			t.Fatalf("expected 2 fstrim calls, got %d", len(runner.Calls))
		}
		for _, c := range runner.Calls {
			if c.Name != "fstrim" {
				t.Errorf("expected fstrim, got %q", c.Name)
			}
		}
	})

	t.Run("EmptyParams", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		if err := im.FinalizeFilesystems(); err == nil {
			t.Error("should error for empty rootfsMount")
		}
		im.rootfsMount = "/mnt/rootfs"
		if err := im.FinalizeFilesystems(); err == nil {
			t.Error("should error for empty bootfsMount")
		}
		im.bootfsMount = "/mnt/boot"
		if err := im.FinalizeFilesystems(); err == nil {
			t.Error("should error for empty efifsMount")
		}
	})
}

// --- MaybeEncryptRootfs Tests ---

func TestMaybeEncryptRootfs(t *testing.T) {
	t.Run("NotEncrypted", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		// Default: encrypted = false.
		err := im.MaybeEncryptRootfs()
		if err != nil {
			t.Fatalf("MaybeEncryptRootfs() error: %v", err)
		}
	})

	t.Run("EncryptedNameError", func(t *testing.T) {
		fsenc := &filesystems.MockFsenc{
			EncryptionEnabled_:     true,
			EncryptedRootFsNameErr: errors.New("name error"),
		}
		opts := &NewImagerOptions{} // non-nil so encrypted gets assigned
		im, err := NewImager(baseImageConfig(), &ostree.MockOstree{}, fsenc, opts)
		if err != nil {
			t.Fatal(err)
		}
		im.rootDevice = "/dev/sda3"

		err = im.MaybeEncryptRootfs()
		if err == nil {
			t.Error("expected error from EncryptedRootFsName")
		}
	})
}

// --- EfiBootDir Tests ---

func TestEfiBootDir(t *testing.T) {
	t.Run("EmptyEfifsMount", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		_, err := im.EfiBootDir()
		if err == nil {
			t.Error("should error for empty efifsMount")
		}
	})

	t.Run("ConfigError", func(t *testing.T) {
		ec := &config.ErrConfig{Err: errors.New("cfg error")}
		im, _ := NewImager(ec, &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), nil)
		im.efifsMount = "/efi"
		_, err := im.EfiBootDir()
		if err == nil {
			t.Error("should error from broken config")
		}
	})

	t.Run("Success", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.efifsMount = "/mnt/efi"
		result, err := im.EfiBootDir()
		if err != nil {
			t.Fatalf("EfiBootDir() error: %v", err)
		}
		if result != "/mnt/efi/EFI/BOOT" {
			t.Errorf("EfiBootDir() = %q, want /mnt/efi/EFI/BOOT", result)
		}
	})
}

// --- FormatRootfs Tests ---

func TestFormatRootfsEmpty(t *testing.T) {
	im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
	// rootDevice empty.
	if err := im.FormatRootfs(); err == nil {
		t.Error("should error for empty rootDevice")
	}
}

// --- MountEfifs Additional Tests ---

func TestMountEfifsRunnerError(t *testing.T) {
	mockRunner := runner.NewMockRunnerFailOnCall(0, errors.New("mount error"))
	im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)
	im.efiDevice = "/dev/loop0p1"

	tmpDir := t.TempDir()
	err := im.MountEfifs(filepath.Join(tmpDir, "efi"))
	if err == nil {
		t.Error("should propagate mount error")
	}
}

// --- MountBootfs Additional Tests ---

func TestMountBootfsRunnerError(t *testing.T) {
	mockRunner := runner.NewMockRunnerFailOnCall(0, errors.New("mount error"))
	im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)
	im.bootDevice = "/dev/loop0p2"

	tmpDir := t.TempDir()
	err := im.MountBootfs(filepath.Join(tmpDir, "boot"))
	if err == nil {
		t.Error("should propagate mount error")
	}
}

// --- MountRootfs Additional Tests ---

func TestMountRootfsRunnerError(t *testing.T) {
	mockRunner := runner.NewMockRunnerFailOnCall(0, errors.New("mount error"))
	im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)
	im.rootDevice = "/dev/loop0p3"

	err := im.MountRootfs("/tmp/rootfs")
	if err == nil {
		t.Error("should propagate mount error")
	}
}
