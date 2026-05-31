package imager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func (im *Imager) ClearPartitionTable() error {
	if im.devicePath == "" {
		return errors.New("missing devicePath, not set in NewImagerOptions")
	}

	im.Print("Clearing partition table on %s ...\n", im.devicePath)
	if err := im.runner(&runner.Cmd{
		Name:   "sgdisk",
		Args:   []string{"-g", "-o", im.devicePath},
		Stdout: im.stdout,
		Stderr: im.stderr,
	}); err != nil {
		return fmt.Errorf("sgdisk -g -o failed on %s: %w", im.devicePath, err)
	}
	return im.runner(&runner.Cmd{
		Name:   "sgdisk",
		Args:   []string{"-Z", im.devicePath},
		Stdout: im.stdout,
		Stderr: im.stderr,
	})
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
	epArgs := []string{
		"sgdisk",
		"-n", fmt.Sprintf("1:0:+%s", efiSize),
		"-t", fmt.Sprintf("1:%s", espPartType),
		im.devicePath,
	}
	if err := im.runner(&runner.Cmd{
		Name:   epArgs[0],
		Args:   epArgs[1:],
		Stdout: im.stdout,
		Stderr: im.stderr,
	}); err != nil {
		return fmt.Errorf("sgdisk EFI partition failed: %w", err)
	}

	// Create boot partition.
	bpArgs := []string{
		"sgdisk",
		"-n", fmt.Sprintf("2:0:+%s", bootSize),
		"-t", fmt.Sprintf("2:%s", bootPartType),
		im.devicePath,
	}
	if err := im.runner(&runner.Cmd{
		Name:   bpArgs[0],
		Args:   bpArgs[1:],
		Stdout: im.stdout,
		Stderr: im.stderr,
	}); err != nil {
		return fmt.Errorf("sgdisk boot partition failed: %w", err)
	}

	// Create root partition with -10M padding for systemd-repart.
	rpArgs := []string{
		"sgdisk",
		"-n", "3:0:-10M",
		"-t", fmt.Sprintf("3:%s", rootPartType),
		im.devicePath,
	}
	if err := im.runner(&runner.Cmd{
		Name:   rpArgs[0],
		Args:   rpArgs[1:],
		Stdout: im.stdout,
		Stderr: im.stderr,
	}); err != nil {
		return fmt.Errorf("sgdisk root partition failed: %w", err)
	}

	// Set the auto-grow flag (bit 59) on partition 3.
	agArgs := []string{
		"sgdisk", "-A", "3:set:59", im.devicePath,
	}
	if err := im.runner(&runner.Cmd{
		Name:   agArgs[0],
		Args:   agArgs[1:],
		Stdout: im.stdout,
		Stderr: im.stderr,
	}); err != nil {
		return fmt.Errorf("sgdisk set auto-grow flag failed: %w", err)
	}

	im.Print("Refreshing partition table ...\n")
	args := []string{
		"partprobe", "-s", im.devicePath,
	}
	if err := im.runner(&runner.Cmd{
		Name:   args[0],
		Args:   args[1:],
		Stdout: im.stdout,
		Stderr: im.stderr,
	}); err != nil {
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
	args := []string{
		"mkfs.vfat",
		"-F", "32",
		"-n", label,
		im.efiDevice,
	}
	return im.runner(&runner.Cmd{
		Name:   args[0],
		Args:   args[1:],
		Stdout: im.stdout,
		Stderr: im.stderr,
	})
}

func (im *Imager) MountEfifs(mountEfifs string) error {
	if im.efiDevice == "" {
		return errors.New("missing efiDevice, not set in NewImagerOptions")
	}
	if mountEfifs == "" {
		return errors.New("missing mountEfifs parameter")
	}

	if !filesystems.DirectoryExists(mountEfifs) {
		im.Print("Creating %s ...\n", mountEfifs)
		if err := os.MkdirAll(mountEfifs, 0755); err != nil {
			return fmt.Errorf("failed to create mount point %s: %w", mountEfifs, err)
		}
	}

	im.Print("Mounting %s to %s\n", im.efiDevice, mountEfifs)
	im.trackMount(mountEfifs)
	if err := im.runner(&runner.Cmd{
		Name:   "mount",
		Args:   []string{"-t", "vfat", im.efiDevice, mountEfifs},
		Stdout: im.stdout,
		Stderr: im.stderr,
	}); err != nil {
		return err
	}
	im.efifsMount = mountEfifs
	return nil
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
		return im.formatBootfsBtrfs(label)
	case "vfat":
		return im.formatBootfsVfat(label)
	default:
		return fmt.Errorf("unsupported boot filesystem type %q: must be btrfs or vfat", fsType)
	}
}

func (im *Imager) formatBootfsBtrfs(label string) error {
	im.Print("Creating btrfs on %s (boot)\n", im.bootDevice)
	return im.runner(&runner.Cmd{
		Name:   "mkfs.btrfs",
		Args:   []string{"-f", "-L", label, im.bootDevice},
		Stdout: im.stdout,
		Stderr: im.stderr,
	})
}

func (im *Imager) formatBootfsVfat(label string) error {
	im.Print("Creating vfat on %s (boot)\n", im.bootDevice)
	return im.runner(&runner.Cmd{
		Name:   "mkfs.vfat",
		Args:   []string{"-F", "32", "-n", label, im.bootDevice},
		Stdout: im.stdout,
		Stderr: im.stderr,
	})
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
	if !filesystems.DirectoryExists(mountBootfs) {
		im.Print("Creating %s ...\n", mountBootfs)
		if err := os.MkdirAll(mountBootfs, 0755); err != nil {
			return fmt.Errorf("failed to create mount point %s: %w", mountBootfs, err)
		}
	}
	im.Print("Mounting %s to %s\n", im.bootDevice, mountBootfs)
	im.trackMount(mountBootfs)
	var mountArgs []string
	switch fsType {
	case "vfat":
		mountArgs = []string{"-t", "vfat", im.bootDevice, mountBootfs}
	default:
		mountArgs = []string{im.bootDevice, mountBootfs}
	}
	if err := im.runner(&runner.Cmd{
		Name:   "mount",
		Args:   mountArgs,
		Stdout: im.stdout,
		Stderr: im.stderr,
	}); err != nil {
		return err
	}
	im.bootfsMount = mountBootfs
	return nil
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
	args := []string{
		"mkfs.btrfs",
		"-f",
		"-L", label,
		im.rootDevice,
	}
	return im.runner(&runner.Cmd{
		Name:   args[0],
		Args:   args[1:],
		Stdout: im.stdout,
		Stderr: im.stderr,
	})
}

func (im *Imager) RootfsKernelArgs() []string {
	return []string{"rootflags=discard=async"}
}

func (im *Imager) MountRootfs(mountRootfs string) error {
	if im.rootDevice == "" {
		return errors.New("missing rootDevice, not set in NewImagerOptions")
	}
	if mountRootfs == "" {
		return errors.New("missing mountRootfs parameter")
	}

	if !filesystems.DirectoryExists(mountRootfs) {
		im.Print("Creating %s ...\n", mountRootfs)
		if err := os.MkdirAll(mountRootfs, 0755); err != nil {
			return fmt.Errorf("failed to create mount point %s: %w", mountRootfs, err)
		}
	}

	compression := "zstd:6"
	btrfsOpts := fmt.Sprintf("compress-force=%s,space_cache=v2,commit=120", compression)
	im.Print("Mounting %s to %s\n", im.rootDevice, mountRootfs)

	im.trackMount(mountRootfs)
	args := []string{
		"mount",
		"-o", btrfsOpts,
		im.rootDevice,
		mountRootfs,
	}
	if err := im.runner(&runner.Cmd{
		Name:   args[0],
		Args:   args[1:],
		Stdout: im.stdout,
		Stderr: im.stderr,
	}); err != nil {
		return err
	}
	im.rootfsMount = mountRootfs

	return nil
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
		im.runner, im.stdout, im.stderr,
		im.rootfsMount, im.bootfsMount,
	)

	return nil
}
