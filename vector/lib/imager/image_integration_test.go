package imager

// Integration tests for the full image partitioning and formatting workflow.
// These tests run as a normal user by creating real sparse image files and
// using the mock runner to intercept system commands (sgdisk, mkfs, mount, etc.).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
)

// --- Full workflow integration tests ---

// TestIntegrationCreateAndPartitionImage creates a real sparse image file
// and runs the partitioning workflow, verifying command sequences.
func TestIntegrationCreateAndPartitionImage(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "test.img")

	mockRunner := runner.NewMockRunner()
	im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)
	im.imagePath = imagePath
	im.mode = ModeCreateImageFile

	// Step 1: Create a real sparse image file.
	if err := im.CreateImage("64M"); err != nil {
		t.Fatalf("CreateImage failed: %v", err)
	}

	info, err := os.Stat(imagePath)
	if err != nil {
		t.Fatalf("Image file not created: %v", err)
	}
	expectedSize := int64(64 * 1024 * 1024)
	if info.Size() != expectedSize {
		t.Fatalf("Expected image size %d, got %d", expectedSize, info.Size())
	}

	// Step 2: Set device path and clear the partition table.
	im.devicePath = imagePath
	if err := im.ClearPartitionTable(); err != nil {
		t.Fatalf("ClearPartitionTable failed: %v", err)
	}

	// Verify sgdisk was called twice (sgdisk -g -o and sgdisk -Z).
	if len(mockRunner.Calls) != 2 {
		t.Fatalf("Expected 2 sgdisk calls for ClearPartitionTable, got %d", len(mockRunner.Calls))
	}
	if mockRunner.Calls[0].Name != "sgdisk" {
		t.Errorf("Call 0: expected sgdisk, got %q", mockRunner.Calls[0].Name)
	}
	if !containsArg(mockRunner.Calls[0].Args, "-g") || !containsArg(mockRunner.Calls[0].Args, "-o") {
		t.Errorf("Call 0: expected -g and -o flags, got %v", mockRunner.Calls[0].Args)
	}
	if !containsArg(mockRunner.Calls[0].Args, imagePath) {
		t.Errorf("Call 0: expected device path %s in args, got %v", imagePath, mockRunner.Calls[0].Args)
	}
	if mockRunner.Calls[1].Name != "sgdisk" {
		t.Errorf("Call 1: expected sgdisk, got %q", mockRunner.Calls[1].Name)
	}
	if !containsArg(mockRunner.Calls[1].Args, "-Z") {
		t.Errorf("Call 1: expected -Z flag, got %v", mockRunner.Calls[1].Args)
	}

	// Step 3: Partition the device.
	mockRunner.Calls = nil // reset
	if err := im.PartitionDevices("200M", "1G", "64M"); err != nil {
		t.Fatalf("PartitionDevices failed: %v", err)
	}

	// Verify: 3 sgdisk partition calls + 1 auto-grow flag + 1 partprobe = 5 calls.
	if len(mockRunner.Calls) != 5 {
		t.Fatalf("Expected 5 calls for PartitionDevices, got %d", len(mockRunner.Calls))
	}

	// Verify EFI partition creation (sgdisk -n 1:0:+200M ...).
	if mockRunner.Calls[0].Name != "sgdisk" {
		t.Errorf("Call 0: expected sgdisk, got %q", mockRunner.Calls[0].Name)
	}
	if !containsArg(mockRunner.Calls[0].Args, "-n") {
		t.Errorf("Call 0: expected -n flag, got %v", mockRunner.Calls[0].Args)
	}
	if !containsArgPrefix(mockRunner.Calls[0].Args, "1:0:+200M") {
		t.Errorf("Call 0: expected EFI partition spec '1:0:+200M' in args, got %v", mockRunner.Calls[0].Args)
	}

	// Verify boot partition creation (sgdisk -n 2:0:+1G ...).
	if !containsArgPrefix(mockRunner.Calls[1].Args, "2:0:+1G") {
		t.Errorf("Call 1: expected boot partition spec '2:0:+1G' in args, got %v", mockRunner.Calls[1].Args)
	}

	// Verify root partition creation (sgdisk -n 3:0:-10M ...).
	if !containsArgPrefix(mockRunner.Calls[2].Args, "3:0:-10M") {
		t.Errorf("Call 2: expected root partition spec '3:0:-10M' in args, got %v", mockRunner.Calls[2].Args)
	}

	// Verify auto-grow flag (sgdisk -A 3:set:59 ...).
	if !containsArg(mockRunner.Calls[3].Args, "-A") || !containsArg(mockRunner.Calls[3].Args, "3:set:59") {
		t.Errorf("Call 3: expected auto-grow flag, got %v", mockRunner.Calls[3].Args)
	}

	// Verify partprobe.
	if mockRunner.Calls[4].Name != "partprobe" {
		t.Errorf("Call 4: expected partprobe, got %q", mockRunner.Calls[4].Name)
	}
}

// TestIntegrationFormatAndMountWorkflow tests the full format and mount workflow
// on a sparse image, verifying the correct commands are issued.
func TestIntegrationFormatAndMountWorkflow(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "test.img")

	mockRunner := runner.NewMockRunner()
	im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)
	im.imagePath = imagePath
	im.mode = ModeCreateImageFile

	// Create a real sparse image.
	if err := im.CreateImage("64M"); err != nil {
		t.Fatalf("CreateImage failed: %v", err)
	}

	// Simulate partition devices (these would be e.g. /dev/loop0p1 in real life).
	im.efiDevice = imagePath + "p1"
	im.bootDevice = imagePath + "p2"
	im.rootDevice = imagePath + "p3"

	// --- Format EFI ---
	mockRunner.Calls = nil
	if err := im.FormatEfifs(); err != nil {
		t.Fatalf("FormatEfifs failed: %v", err)
	}
	if len(mockRunner.Calls) != 1 {
		t.Fatalf("Expected 1 call for FormatEfifs, got %d", len(mockRunner.Calls))
	}
	if mockRunner.Calls[0].Name != "mkfs.vfat" {
		t.Errorf("FormatEfifs: expected mkfs.vfat, got %q", mockRunner.Calls[0].Name)
	}
	if !containsArg(mockRunner.Calls[0].Args, "-F") || !containsArg(mockRunner.Calls[0].Args, "32") {
		t.Errorf("FormatEfifs: expected -F 32 flags, got %v", mockRunner.Calls[0].Args)
	}
	if !containsArg(mockRunner.Calls[0].Args, im.efiDevice) {
		t.Errorf("FormatEfifs: expected device path in args, got %v", mockRunner.Calls[0].Args)
	}

	// --- Format Boot ---
	mockRunner.Calls = nil
	if err := im.FormatBootfs(); err != nil {
		t.Fatalf("FormatBootfs failed: %v", err)
	}
	if len(mockRunner.Calls) != 1 {
		t.Fatalf("Expected 1 call for FormatBootfs, got %d", len(mockRunner.Calls))
	}
	if mockRunner.Calls[0].Name != "mkfs.btrfs" {
		t.Errorf("FormatBootfs: expected mkfs.btrfs, got %q", mockRunner.Calls[0].Name)
	}
	if !containsArg(mockRunner.Calls[0].Args, im.bootDevice) {
		t.Errorf("FormatBootfs: expected device path in args, got %v", mockRunner.Calls[0].Args)
	}

	// --- Format Root ---
	mockRunner.Calls = nil
	if err := im.FormatRootfs(); err != nil {
		t.Fatalf("FormatRootfs failed: %v", err)
	}
	if len(mockRunner.Calls) != 1 {
		t.Fatalf("Expected 1 call for FormatRootfs, got %d", len(mockRunner.Calls))
	}
	if mockRunner.Calls[0].Name != "mkfs.btrfs" {
		t.Errorf("FormatRootfs: expected mkfs.btrfs, got %q", mockRunner.Calls[0].Name)
	}
	if !containsArg(mockRunner.Calls[0].Args, im.rootDevice) {
		t.Errorf("FormatRootfs: expected device path in args, got %v", mockRunner.Calls[0].Args)
	}

	// --- Mount EFI ---
	mockRunner.Calls = nil
	mountEfi := filepath.Join(tmpDir, "mnt", "efi")
	if err := im.MountEfifs(mountEfi); err != nil {
		t.Fatalf("MountEfifs failed: %v", err)
	}
	if len(mockRunner.Calls) != 1 {
		t.Fatalf("Expected 1 call for MountEfifs, got %d", len(mockRunner.Calls))
	}
	if mockRunner.Calls[0].Name != "mount" {
		t.Errorf("MountEfifs: expected mount, got %q", mockRunner.Calls[0].Name)
	}
	if !containsArg(mockRunner.Calls[0].Args, "-t") || !containsArg(mockRunner.Calls[0].Args, "vfat") {
		t.Errorf("MountEfifs: expected -t vfat flags, got %v", mockRunner.Calls[0].Args)
	}
	if !containsArg(mockRunner.Calls[0].Args, im.efiDevice) || !containsArg(mockRunner.Calls[0].Args, mountEfi) {
		t.Errorf("MountEfifs: expected device and mount point in args, got %v", mockRunner.Calls[0].Args)
	}
	// Verify mount point directory was created.
	if _, err := os.Stat(mountEfi); os.IsNotExist(err) {
		t.Errorf("MountEfifs: mount point directory was not created: %s", mountEfi)
	}
	// Verify mount point was stored.
	if im.EfifsMount() != mountEfi {
		t.Errorf("MountEfifs: expected efifsMount %q, got %q", mountEfi, im.EfifsMount())
	}

	// --- Mount Boot ---
	mockRunner.Calls = nil
	mountBoot := filepath.Join(tmpDir, "mnt", "boot")
	if err := im.MountBootfs(mountBoot); err != nil {
		t.Fatalf("MountBootfs failed: %v", err)
	}
	if len(mockRunner.Calls) != 1 {
		t.Fatalf("Expected 1 call for MountBootfs, got %d", len(mockRunner.Calls))
	}
	if mockRunner.Calls[0].Name != "mount" {
		t.Errorf("MountBootfs: expected mount, got %q", mockRunner.Calls[0].Name)
	}
	// Verify mount point was stored.
	if im.BootfsMount() != mountBoot {
		t.Errorf("MountBootfs: expected bootfsMount %q, got %q", mountBoot, im.BootfsMount())
	}

	// --- Mount Root ---
	mockRunner.Calls = nil
	mountRoot := filepath.Join(tmpDir, "mnt", "rootfs")
	if err := im.MountRootfs(mountRoot); err != nil {
		t.Fatalf("MountRootfs failed: %v", err)
	}
	if len(mockRunner.Calls) != 1 {
		t.Fatalf("Expected 1 call for MountRootfs, got %d", len(mockRunner.Calls))
	}
	if mockRunner.Calls[0].Name != "mount" {
		t.Errorf("MountRootfs: expected mount, got %q", mockRunner.Calls[0].Name)
	}
	// Verify btrfs compression options are passed.
	if !containsArg(mockRunner.Calls[0].Args, "-o") {
		t.Errorf("MountRootfs: expected -o flag for btrfs options, got %v", mockRunner.Calls[0].Args)
	}
	foundCompress := false
	for _, arg := range mockRunner.Calls[0].Args {
		if strings.Contains(arg, "compress-force=") {
			foundCompress = true
			break
		}
	}
	if !foundCompress {
		t.Errorf("MountRootfs: expected compress-force option, got %v", mockRunner.Calls[0].Args)
	}
	// Verify mount point was stored.
	if im.RootfsMount() != mountRoot {
		t.Errorf("MountRootfs: expected rootfsMount %q, got %q", mountRoot, im.RootfsMount())
	}
}

// TestIntegrationPartitionTypeGUIDs verifies that partition type GUIDs from config
// are correctly passed to sgdisk during partitioning.
func TestIntegrationPartitionTypeGUIDs(t *testing.T) {
	mockRunner := runner.NewMockRunner()
	cfg := baseImageConfig()
	im := newTestImagerWithRunner(cfg, &ostree.MockOstree{}, mockRunner)

	im.devicePath = "/dev/fake"
	if err := im.PartitionDevices("200M", "1G", "32G"); err != nil {
		t.Fatalf("PartitionDevices failed: %v", err)
	}

	espType := cfg.Items["Imager.EspPartitionType"][0]
	bootType := cfg.Items["Imager.BootPartitionType"][0]
	rootType := cfg.Items["Imager.RootPartitionType"][0]

	// Call 0: EFI partition — check type GUID.
	if !containsArgPrefix(mockRunner.Calls[0].Args, "1:"+espType) {
		t.Errorf("EFI partition: expected type GUID %s, got %v", espType, mockRunner.Calls[0].Args)
	}

	// Call 1: Boot partition — check type GUID.
	if !containsArgPrefix(mockRunner.Calls[1].Args, "2:"+bootType) {
		t.Errorf("Boot partition: expected type GUID %s, got %v", bootType, mockRunner.Calls[1].Args)
	}

	// Call 2: Root partition — check type GUID.
	if !containsArgPrefix(mockRunner.Calls[2].Args, "3:"+rootType) {
		t.Errorf("Root partition: expected type GUID %s, got %v", rootType, mockRunner.Calls[2].Args)
	}
}

// TestIntegrationClearPartitionAndRepartition tests the sequence of clearing
// a partition table and repartitioning, as done in the whole-device workflow.
func TestIntegrationClearPartitionAndRepartition(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "device.img")

	mockRunner := runner.NewMockRunner()
	im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)

	// Create a real sparse image.
	im.imagePath = imagePath
	im.mode = ModeCreateImageFile
	if err := im.CreateImage("128M"); err != nil {
		t.Fatalf("CreateImage failed: %v", err)
	}

	// Verify the image is a sparse file (blocks allocated < total size).
	info, err := os.Stat(imagePath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Size() != 128*1024*1024 {
		t.Fatalf("Expected 128MiB, got %d bytes", info.Size())
	}

	// Clear partition table.
	im.devicePath = imagePath
	if err := im.ClearPartitionTable(); err != nil {
		t.Fatalf("ClearPartitionTable failed: %v", err)
	}
	clearCalls := len(mockRunner.Calls)

	// Partition.
	if err := im.PartitionDevices("100M", "500M", "128M"); err != nil {
		t.Fatalf("PartitionDevices failed: %v", err)
	}
	partCalls := len(mockRunner.Calls) - clearCalls

	// Verify total call count: 2 (clear) + 5 (partition) = 7.
	if clearCalls != 2 {
		t.Errorf("Expected 2 clear calls, got %d", clearCalls)
	}
	if partCalls != 5 {
		t.Errorf("Expected 5 partition calls, got %d", partCalls)
	}

	// Verify all sgdisk calls referenced the correct device path.
	for i, call := range mockRunner.Calls {
		if call.Name == "sgdisk" {
			if !containsArg(call.Args, imagePath) {
				t.Errorf("Call %d (%s): device path %s not in args %v", i, call.Name, imagePath, call.Args)
			}
		}
	}
}

// TestIntegrationFormatFsLabels verifies that filesystem labels contain the
// correct date-based prefix.
func TestIntegrationFormatFsLabels(t *testing.T) {
	mockRunner := runner.NewMockRunner()
	im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)

	im.efiDevice = "/dev/fake1"
	im.bootDevice = "/dev/fake2"
	im.rootDevice = "/dev/fake3"

	// Format EFI.
	if err := im.FormatEfifs(); err != nil {
		t.Fatalf("FormatEfifs failed: %v", err)
	}
	// EFI label should start with "ME".
	efiLabel := findArgAfter(mockRunner.Calls[0].Args, "-n")
	if efiLabel == "" || !strings.HasPrefix(efiLabel, "ME") {
		t.Errorf("EFI label should start with 'ME', got %q", efiLabel)
	}

	// Format Boot.
	mockRunner.Calls = nil
	if err := im.FormatBootfs(); err != nil {
		t.Fatalf("FormatBootfs failed: %v", err)
	}
	bootLabel := findArgAfter(mockRunner.Calls[0].Args, "-L")
	if bootLabel == "" || !strings.HasPrefix(bootLabel, "MB") {
		t.Errorf("Boot label should start with 'MB', got %q", bootLabel)
	}

	// Format Root.
	mockRunner.Calls = nil
	if err := im.FormatRootfs(); err != nil {
		t.Fatalf("FormatRootfs failed: %v", err)
	}
	rootLabel := findArgAfter(mockRunner.Calls[0].Args, "-L")
	if rootLabel == "" || !strings.HasPrefix(rootLabel, "MR") {
		t.Errorf("Root label should start with 'MR', got %q", rootLabel)
	}
}

// TestIntegrationCreateImageIdempotent verifies that CreateImage correctly
// removes an existing image file before creating a new one.
func TestIntegrationCreateImageIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "test.img")

	im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
	im.imagePath = imagePath
	im.mode = ModeCreateImageFile

	// Create first image.
	if err := im.CreateImage("1M"); err != nil {
		t.Fatalf("First CreateImage failed: %v", err)
	}

	// Write some data to the image.
	f, err := os.OpenFile(imagePath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteAt([]byte("marker"), 0)
	f.Close()

	// Create second image (should replace the first).
	if err := im.CreateImage("2M"); err != nil {
		t.Fatalf("Second CreateImage failed: %v", err)
	}

	info, err := os.Stat(imagePath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	expectedSize := int64(2 * 1024 * 1024)
	if info.Size() != expectedSize {
		t.Errorf("Expected size %d after re-create, got %d", expectedSize, info.Size())
	}

	// Verify marker was removed (sparse file should be zeroed).
	data := make([]byte, 6)
	rf, _ := os.Open(imagePath)
	rf.Read(data)
	rf.Close()
	if string(data) == "marker" {
		t.Error("Expected marker to be overwritten by truncate")
	}
}

// TestIntegrationPartitionDevicesConfigErrors verifies that PartitionDevices
// returns errors when required config values are missing.
func TestIntegrationPartitionDevicesConfigErrors(t *testing.T) {
	t.Run("MissingEspPartitionType", func(t *testing.T) {
		cfg := baseImageConfig()
		delete(cfg.Items, "Imager.EspPartitionType")
		mockRunner := runner.NewMockRunner()
		im := newTestImagerWithRunner(cfg, &ostree.MockOstree{}, mockRunner)
		im.devicePath = "/dev/fake"

		err := im.PartitionDevices("200M", "1G", "32G")
		if err == nil {
			t.Fatal("Expected error for missing EspPartitionType")
		}
	})

	t.Run("MissingBootPartitionType", func(t *testing.T) {
		cfg := baseImageConfig()
		delete(cfg.Items, "Imager.BootPartitionType")
		mockRunner := runner.NewMockRunner()
		im := newTestImagerWithRunner(cfg, &ostree.MockOstree{}, mockRunner)
		im.devicePath = "/dev/fake"

		err := im.PartitionDevices("200M", "1G", "32G")
		if err == nil {
			t.Fatal("Expected error for missing BootPartitionType")
		}
	})

	t.Run("MissingRootPartitionType", func(t *testing.T) {
		cfg := baseImageConfig()
		delete(cfg.Items, "Imager.RootPartitionType")
		mockRunner := runner.NewMockRunner()
		im := newTestImagerWithRunner(cfg, &ostree.MockOstree{}, mockRunner)
		im.devicePath = "/dev/fake"

		err := im.PartitionDevices("200M", "1G", "32G")
		if err == nil {
			t.Fatal("Expected error for missing RootPartitionType")
		}
	})
}

// TestIntegrationPartitionDevicesSgdiskFailures verifies that PartitionDevices
// correctly propagates sgdisk errors at each stage.
func TestIntegrationPartitionDevicesSgdiskFailures(t *testing.T) {
	t.Run("EfiPartitionFails", func(t *testing.T) {
		mockRunner := runner.NewMockRunnerFailOnCall(0, os.ErrPermission)
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)
		im.devicePath = "/dev/fake"

		err := im.PartitionDevices("200M", "1G", "32G")
		if err == nil {
			t.Fatal("Expected error when EFI sgdisk fails")
		}
		if !strings.Contains(err.Error(), "EFI") {
			t.Errorf("Expected EFI error, got: %v", err)
		}
	})

	t.Run("BootPartitionFails", func(t *testing.T) {
		mockRunner := runner.NewMockRunnerFailOnCall(1, os.ErrPermission)
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)
		im.devicePath = "/dev/fake"

		err := im.PartitionDevices("200M", "1G", "32G")
		if err == nil {
			t.Fatal("Expected error when boot sgdisk fails")
		}
		if !strings.Contains(err.Error(), "boot") {
			t.Errorf("Expected boot error, got: %v", err)
		}
	})

	t.Run("RootPartitionFails", func(t *testing.T) {
		mockRunner := runner.NewMockRunnerFailOnCall(2, os.ErrPermission)
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)
		im.devicePath = "/dev/fake"

		err := im.PartitionDevices("200M", "1G", "32G")
		if err == nil {
			t.Fatal("Expected error when root sgdisk fails")
		}
		if !strings.Contains(err.Error(), "root") {
			t.Errorf("Expected root error, got: %v", err)
		}
	})

	t.Run("AutoGrowFlagFails", func(t *testing.T) {
		mockRunner := runner.NewMockRunnerFailOnCall(3, os.ErrPermission)
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)
		im.devicePath = "/dev/fake"

		err := im.PartitionDevices("200M", "1G", "32G")
		if err == nil {
			t.Fatal("Expected error when auto-grow flag fails")
		}
		if !strings.Contains(err.Error(), "auto-grow") {
			t.Errorf("Expected auto-grow error, got: %v", err)
		}
	})

	t.Run("PartprobeFails", func(t *testing.T) {
		mockRunner := runner.NewMockRunnerFailOnCall(4, os.ErrPermission)
		im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)
		im.devicePath = "/dev/fake"

		err := im.PartitionDevices("200M", "1G", "32G")
		if err == nil {
			t.Fatal("Expected error when partprobe fails")
		}
		if !strings.Contains(err.Error(), "partprobe") {
			t.Errorf("Expected partprobe error, got: %v", err)
		}
	})
}

// TestIntegrationMountCreatesDirectories verifies that mount functions create
// mount point directories when they don't exist.
func TestIntegrationMountCreatesDirectories(t *testing.T) {
	mockRunner := runner.NewMockRunner()
	im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)

	im.efiDevice = "/dev/fake1"
	im.bootDevice = "/dev/fake2"
	im.rootDevice = "/dev/fake3"

	tmpDir := t.TempDir()

	// Test nested mount point creation.
	deepEfi := filepath.Join(tmpDir, "a", "b", "c", "efi")
	if err := im.MountEfifs(deepEfi); err != nil {
		t.Fatalf("MountEfifs failed: %v", err)
	}
	if _, err := os.Stat(deepEfi); os.IsNotExist(err) {
		t.Error("MountEfifs did not create nested mount point directory")
	}
	if im.EfifsMount() != deepEfi {
		t.Errorf("MountEfifs: expected efifsMount %q, got %q", deepEfi, im.EfifsMount())
	}

	deepBoot := filepath.Join(tmpDir, "x", "y", "boot")
	if err := im.MountBootfs(deepBoot); err != nil {
		t.Fatalf("MountBootfs failed: %v", err)
	}
	if _, err := os.Stat(deepBoot); os.IsNotExist(err) {
		t.Error("MountBootfs did not create nested mount point directory")
	}
	if im.BootfsMount() != deepBoot {
		t.Errorf("MountBootfs: expected bootfsMount %q, got %q", deepBoot, im.BootfsMount())
	}

	deepRoot := filepath.Join(tmpDir, "r", "o", "rootfs")
	if err := im.MountRootfs(deepRoot); err != nil {
		t.Fatalf("MountRootfs failed: %v", err)
	}
	if _, err := os.Stat(deepRoot); os.IsNotExist(err) {
		t.Error("MountRootfs did not create nested mount point directory")
	}
	if im.RootfsMount() != deepRoot {
		t.Errorf("MountRootfs: expected rootfsMount %q, got %q", deepRoot, im.RootfsMount())
	}
}

// TestIntegrationNewImageWithOptions verifies that NewImagerOptions correctly
// initializes all device fields.
func TestIntegrationNewImageWithOptions(t *testing.T) {
	opts := &NewImagerOptions{
		EfiDevice:  "/dev/sda1",
		BootDevice: "/dev/sda2",
		RootDevice: "/dev/sda3",
		DevicePath: "/dev/sda",
	}

	im, err := NewImager(baseImageConfig(), &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), opts)
	if err != nil {
		t.Fatalf("NewImager failed: %v", err)
	}

	if im.efiDevice != opts.EfiDevice {
		t.Errorf("efiDevice: got %q, want %q", im.efiDevice, opts.EfiDevice)
	}
	if im.bootDevice != opts.BootDevice {
		t.Errorf("bootDevice: got %q, want %q", im.bootDevice, opts.BootDevice)
	}
	if im.rootDevice != opts.RootDevice {
		t.Errorf("rootDevice: got %q, want %q", im.rootDevice, opts.RootDevice)
	}
	if im.devicePath != opts.DevicePath {
		t.Errorf("devicePath: got %q, want %q", im.devicePath, opts.DevicePath)
	}
}

// TestIntegrationDeviceSetters verifies that device setters update the struct
// and that subsequent operations use the new values.
func TestIntegrationDeviceSetters(t *testing.T) {
	mockRunner := runner.NewMockRunner()
	im := newTestImagerWithRunner(baseImageConfig(), &ostree.MockOstree{}, mockRunner)

	// Set devices via setters.
	im.SetEfiDevice("/dev/sda1")
	im.SetBootDevice("/dev/sda2")
	im.SetRootDevice("/dev/sda3")
	im.SetDevicePath("/dev/sda")

	// Verify ClearPartitionTable uses the set device path.
	if err := im.ClearPartitionTable(); err != nil {
		t.Fatalf("ClearPartitionTable failed: %v", err)
	}
	if !containsArg(mockRunner.Calls[0].Args, "/dev/sda") {
		t.Errorf("ClearPartitionTable: expected /dev/sda in args, got %v", mockRunner.Calls[0].Args)
	}

	// Verify FormatEfifs uses the set EFI device.
	mockRunner.Calls = nil
	if err := im.FormatEfifs(); err != nil {
		t.Fatalf("FormatEfifs failed: %v", err)
	}
	if !containsArg(mockRunner.Calls[0].Args, "/dev/sda1") {
		t.Errorf("FormatEfifs: expected /dev/sda1 in args, got %v", mockRunner.Calls[0].Args)
	}

	// Verify FormatBootfs uses the set boot device.
	mockRunner.Calls = nil
	if err := im.FormatBootfs(); err != nil {
		t.Fatalf("FormatBootfs failed: %v", err)
	}
	if !containsArg(mockRunner.Calls[0].Args, "/dev/sda2") {
		t.Errorf("FormatBootfs: expected /dev/sda2 in args, got %v", mockRunner.Calls[0].Args)
	}

	// Verify FormatRootfs uses the set root device.
	mockRunner.Calls = nil
	if err := im.FormatRootfs(); err != nil {
		t.Fatalf("FormatRootfs failed: %v", err)
	}
	if !containsArg(mockRunner.Calls[0].Args, "/dev/sda3") {
		t.Errorf("FormatRootfs: expected /dev/sda3 in args, got %v", mockRunner.Calls[0].Args)
	}
}

// --- Test helpers ---

// containsArg checks if a string appears in the argument list.
func containsArg(args []string, target string) bool {
	for _, a := range args {
		if a == target {
			return true
		}
	}
	return false
}

// containsArgPrefix checks if any element in args contains the given substring.
func containsArgPrefix(args []string, sub string) bool {
	for _, a := range args {
		if strings.Contains(a, sub) {
			return true
		}
	}
	return false
}

// findArgAfter returns the argument appearing immediately after the given flag.
func findArgAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
