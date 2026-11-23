package imager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/runner"
)

func (im *Imager) CreateImage(imageSize string) (retErr error) {
	if err := im.validateImageModeForCreation(); err != nil {
		return err
	}

	if imageSize == "" {
		return errors.New("missing imageSize parameter")
	}

	imagesDir := filepath.Dir(im.imagePath)
	im.Print(
		"Creating images directory: %s (if it does not exist)\n",
		imagesDir,
	)
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		return fmt.Errorf("failed to create images directory %s: %w", imagesDir, err)
	}

	// Don't skip removing or sgdisk gets confused due to truncate.
	if err := im.RemoveImageFile(); err != nil {
		return err
	}

	sizeBytes, err := filesystems.ParseHumanSize(imageSize)
	if err != nil {
		return fmt.Errorf("failed to parse image size %q: %w", imageSize, err)
	}

	im.Print("Creating block device image file: %s\n", im.imagePath)
	f, err := os.Create(im.imagePath)
	if err != nil {
		return fmt.Errorf("failed to create image file %s: %w", im.imagePath, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()

	if err := f.Truncate(sizeBytes); err != nil {
		return fmt.Errorf("failed to truncate image file %s to %d bytes: %w", im.imagePath, sizeBytes, err)
	}
	return nil
}

func (im *Imager) runCmd(name string, args ...string) error {
	return im.runner(&runner.Cmd{
		Name:   name,
		Args:   args,
		Stdout: im.Stdout(),
		Stderr: im.Stderr(),
	})
}

func (im *Imager) mountPartition(device, missingDeviceMsg, mountPoint, missingMountMsg string, mountArgs []string, dest *string) error {
	if device == "" {
		return errors.New(missingDeviceMsg)
	}
	if mountPoint == "" {
		return errors.New(missingMountMsg)
	}
	if !im.trackMountIfOpen(mountPoint) {
		return fmt.Errorf("cleanup already ran; refusing to mount %s", mountPoint)
	}
	finishOp := sync.OnceFunc(im.finishTrackedOp)
	defer finishOp()

	if !filesystems.DirectoryExists(mountPoint) {
		im.Print("Creating %s ...\n", mountPoint)
		if err := os.MkdirAll(mountPoint, 0755); err != nil {
			return fmt.Errorf("failed to create mount point %s: %w", mountPoint, err)
		}
	}

	im.Print("Mounting %s to %s\n", device, mountPoint)
	if err := im.runCmd("mount", mountArgs...); err != nil {
		return err
	}
	if im.cleanupActiveOrRan() {
		im.trackMount(mountPoint)
		finishOp()
		im.Cleanup()
		return fmt.Errorf("cleanup interrupted mount of %s", mountPoint)
	}
	*dest = mountPoint
	return nil
}

func (im *Imager) ClearPartitionTable() error {
	if im.devicePath == "" {
		return errors.New("missing devicePath, not set in NewImagerOptions")
	}

	im.Print("Clearing partition table on %s ...\n", im.devicePath)
	if err := im.runCmd("sgdisk", "-g", "-o", im.devicePath); err != nil {
		return fmt.Errorf("sgdisk -g -o failed on %s: %w", im.devicePath, err)
	}
	return im.runCmd("sgdisk", "-Z", im.devicePath)
}

func (im *Imager) DatedFsLabel() string {
	return time.Now().Format("20060102")
}

func (im *Imager) PartitionDevices(efiSize, bootSize, imageSize string) error {
	if efiSize == "" {
		return errors.New("missing efiSize parameter")
	}
	if bootSize == "" {
		return errors.New("missing bootSize parameter")
	}
	if imageSize == "" {
		return errors.New("missing imageSize parameter")
	}
	if im.devicePath == "" {
		return errors.New("missing devicePath, not set in NewImagerOptions")
	}

	espPartType, err := im.EspPartitionType()
	if err != nil {
		return err
	}
	bootPartType, err := im.BootPartitionType()
	if err != nil {
		return err
	}
	rootPartType, err := im.RootPartitionType()
	if err != nil {
		return err
	}

	im.Print("Partitioning %s:\n", im.devicePath)
	im.Print(" --> p1 (EFI: %s)\n", efiSize)
	im.Print(" --> p2 (BOOT: %s)\n", bootSize)
	im.Print(" --> p3 (ROOT: Remainder of %s, plus autogrow)\n", imageSize)

	// Create EFI partition.
	if err := im.runCmd(
		"sgdisk",
		"-n", fmt.Sprintf("1:0:+%s", efiSize),
		"-t", fmt.Sprintf("1:%s", espPartType),
		im.devicePath,
	); err != nil {
		return fmt.Errorf("sgdisk EFI partition failed: %w", err)
	}

	// Create boot partition.
	if err := im.runCmd(
		"sgdisk",
		"-n", fmt.Sprintf("2:0:+%s", bootSize),
		"-t", fmt.Sprintf("2:%s", bootPartType),
		im.devicePath,
	); err != nil {
		return fmt.Errorf("sgdisk boot partition failed: %w", err)
	}

	// Create root partition with -10M padding for systemd-repart.
	if err := im.runCmd(
		"sgdisk",
		"-n", "3:0:-10M",
		"-t", fmt.Sprintf("3:%s", rootPartType),
		im.devicePath,
	); err != nil {
		return fmt.Errorf("sgdisk root partition failed: %w", err)
	}

	// Set the auto-grow flag (bit 59) on partition 3.
	if err := im.runCmd("sgdisk", "-A", "3:set:59", im.devicePath); err != nil {
		return fmt.Errorf("sgdisk set auto-grow flag failed: %w", err)
	}

	im.Print("Refreshing partition table ...\n")
	if err := im.runCmd("partprobe", "-s", im.devicePath); err != nil {
		return fmt.Errorf("partprobe failed: %w", err)
	}

	filesystems.DevicesSettle()
	return nil
}

func (im *Imager) FormatEfifs() error {
	if im.efiDevice == "" {
		return errors.New("missing efiDevice, not set in NewImagerOptions")
	}

	im.Print("Creating EFI partition on %s\n", im.efiDevice)
	label := "ME" + im.DatedFsLabel()
	return im.runCmd("mkfs.vfat", "-F", "32", "-n", label, im.efiDevice)
}

func (im *Imager) MountEfifs(mountEfifs string) error {
	return im.mountPartition(
		im.efiDevice, "missing efiDevice, not set in NewImagerOptions",
		mountEfifs, "missing mountEfifs parameter",
		[]string{"-t", "vfat", im.efiDevice, mountEfifs},
		&im.efifsMount,
	)
}

func (im *Imager) EfiBootDir() (string, error) {
	if im.efifsMount == "" {
		return "", errors.New("EFI filesystem not mounted")
	}
	relEfiBootPath, err := im.RelativeEfiBootPath()
	if err != nil {
		return "", err
	}
	efibootDir := filepath.Join(im.efifsMount, relEfiBootPath)
	return efibootDir, nil
}

func (im *Imager) FormatBootfs() error {
	if im.bootDevice == "" {
		return errors.New("missing bootDevice, not set in NewImagerOptions")
	}
	fsType, err := im.BootFilesystemType()
	if err != nil {
		return fmt.Errorf("failed to get boot filesystem type: %w", err)
	}
	label := "MB" + im.DatedFsLabel()
	switch fsType {
	case "btrfs":
		im.Print("Creating btrfs on %s (boot)\n", im.bootDevice)
		return im.runCmd("mkfs.btrfs", "-f", "-L", label, im.bootDevice)
	case "vfat":
		im.Print("Creating vfat on %s (boot)\n", im.bootDevice)
		return im.runCmd("mkfs.vfat", "-F", "32", "-n", label, im.bootDevice)
	default:
		return fmt.Errorf("unsupported boot filesystem type %q: must be btrfs or vfat", fsType)
	}
}

func (im *Imager) MountBootfs(mountBootfs string) error {
	if im.bootDevice == "" {
		return errors.New("missing bootDevice, not set in NewImagerOptions")
	}
	if mountBootfs == "" {
		return errors.New("missing mountBootfs parameter")
	}
	fsType, err := im.BootFilesystemType()
	if err != nil {
		return fmt.Errorf("failed to get boot filesystem type: %w", err)
	}
	mountArgs := []string{im.bootDevice, mountBootfs}
	if fsType == "vfat" {
		mountArgs = []string{"-t", "vfat", im.bootDevice, mountBootfs}
	}
	return im.mountPartition(
		im.bootDevice, "missing bootDevice, not set in NewImagerOptions",
		mountBootfs, "missing mountBootfs parameter",
		mountArgs,
		&im.bootfsMount,
	)
}

func (im *Imager) MaybeEncryptRootfs() error {
	if !im.encrypted {
		return nil
	}

	// Get the current root device.
	rootDevice := im.RootDevice()
	im.realRootDevice = rootDevice

	encRootfsName, err := im.fsenc.EncryptedRootFsName()
	if err != nil {
		return err
	}
	luksDevice, err := filesystems.GetLuksRootfsDevicePath(encRootfsName)
	if err != nil {
		return err
	}
	if err := im.fsenc.LuksEncrypt(im.rootDevice, luksDevice); err != nil {
		return fmt.Errorf("LUKS encryption failed: %w", err)
	}
	im.SetRootDevice(luksDevice)
	im.Print("New encrypted rootfs partition: %s\n", luksDevice)
	return nil
}

func (im *Imager) FormatRootfs() error {
	if im.rootDevice == "" {
		return errors.New("missing rootDevice, not set in NewImagerOptions")
	}

	label := "MR" + im.DatedFsLabel()
	im.Print("Creating btrfs on %s (root)\n", im.rootDevice)
	return im.runCmd("mkfs.btrfs", "-f", "-L", label, im.rootDevice)
}

func (im *Imager) RootfsKernelArgs() []string {
	return []string{"rootflags=discard=async"}
}

func (im *Imager) MountRootfs(mountRootfs string) error {
	btrfsOpts := "compress-force=zstd:6,space_cache=v2,commit=120"
	return im.mountPartition(
		im.rootDevice, "missing rootDevice, not set in NewImagerOptions",
		mountRootfs, "missing mountRootfs parameter",
		[]string{"-o", btrfsOpts, im.rootDevice, mountRootfs},
		&im.rootfsMount,
	)
}

func (im *Imager) FinalizeFilesystems() error {
	if im.rootfsMount == "" {
		return errors.New("missing rootfsMount, call MountRootfs first")
	}
	if im.bootfsMount == "" {
		return errors.New("missing bootfsMount, call MountBootfs first")
	}
	if im.efifsMount == "" {
		return errors.New("missing efifsMount, call MountEfifs first")
	}

	// fstrim may fail on USB sticks, so errors are intentionally ignored.
	filesystems.FstrimAll(
		im.runner, im.Stdout(), im.Stderr(),
		im.rootfsMount, im.bootfsMount,
	)

	return nil
}
