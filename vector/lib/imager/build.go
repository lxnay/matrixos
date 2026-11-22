package imager

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
)

const (
	Sha256Ext       = ".sha256"
	PackagesFileExt = ".packages.txt"
	GpgPubKeyAscExt = ".pubkey.asc"
)

// BuildOptions holds the resolved and validated options for building the image.
type BuildOptions struct {
	EfiDevice   string
	BootDevice  string
	RootDevice  string
	WholeDevice string
}

// Build implements the core image setup logic.
// It partitions, formats, mounts, deploys ostree, installs the bootloader,
// and performs post-processing (productionization, compression, signing).
func (im *Imager) Build(opts *BuildOptions) error {
	mountRootfs, err := im.prepareRootfs()
	if err != nil {
		return err
	}

	if err := im.setupDevices(opts); err != nil {
		return err
	}

	rootDeviceUUID, err := im.formatAndMountFilesystems(mountRootfs)
	if err != nil {
		return err
	}

	if err := im.deployOstree(mountRootfs, rootDeviceUUID); err != nil {
		return err
	}

	if err := im.installSystemComponents(); err != nil {
		return err
	}

	releaseVersion, pkgList, err := im.extractBuildMetadata()
	if err != nil {
		return err
	}

	return im.finalizeBuild(releaseVersion, pkgList)
}

// prepareRootfs creates the temporary rootfs directory and configures
// the sysroot overlay for the build.
func (im *Imager) prepareRootfs() (string, error) {
	mountDir, err := im.MountDir()
	if err != nil {
		return "", err
	}
	if !filesystems.DirectoryExists(mountDir) {
		im.Print("Creating mount dir: %s ...\n", mountDir)
		if err := os.MkdirAll(mountDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create mount dir: %w", err)
		}
	}

	suffix := refToSuffix(im.Ref())
	prefix := fmt.Sprintf("rootfs-%s", suffix)
	mountRootfs, err := filesystems.CreateTempDir(mountDir, prefix)
	if err != nil {
		return "", fmt.Errorf("failed to create temp rootfs dir: %w", err)
	}
	im.trackTmpDir(mountRootfs)

	if err := im.addSysrootOverlay(mountRootfs); err != nil {
		return "", fmt.Errorf("failed to add sysroot overlay: %w", err)
	}

	return mountRootfs, nil
}

// setupDevices reads partition size configs and configures block devices
// based on the build options. It handles three modes: whole device,
// image file creation, and existing device partitions.
func (im *Imager) setupDevices(opts *BuildOptions) error {
	efiSize, err := im.EfiPartitionSize()
	if err != nil {
		return err
	}
	bootSize, err := im.BootPartitionSize()
	if err != nil {
		return err
	}
	imageSize, err := im.ImageSize()
	if err != nil {
		return err
	}

	deployOnDev := opts.BootDevice != "" || opts.RootDevice != "" || opts.EfiDevice != ""
	if deployOnDev {
		// Failsafe: ensure all 3 device paths are present.
		for _, d := range []string{opts.BootDevice, opts.RootDevice, opts.EfiDevice} {
			if d == "" {
				return fmt.Errorf("failsafe: missing device path parameter")
			}
		}
	}

	if opts.WholeDevice != "" {
		return im.setupWholeDevice(opts.WholeDevice, efiSize, bootSize, imageSize)
	}
	if !deployOnDev {
		return im.setupImageFile(efiSize, bootSize, imageSize)
	}
	return im.setupExistingPartitions(opts)
}

// setupWholeDevice flashes to a whole device by clearing the partition table,
// partitioning, and formatting the EFI partition.
func (im *Imager) setupWholeDevice(device, efiSize, bootSize, imageSize string) error {
	im.SetDevicePath(device)
	if err := im.SetImageMode(ModeFlashToDevice); err != nil {
		return err
	}

	if err := im.ClearPartitionTable(); err != nil {
		return fmt.Errorf("failed to clear partition table: %w", err)
	}
	filesystems.DevicesSettle()

	if err := im.PartitionDevices(efiSize, bootSize, imageSize); err != nil {
		return fmt.Errorf("failed to partition device: %w", err)
	}
	filesystems.DevicesSettle()

	partEfi, err := filesystems.BlockDeviceNthPartition(device, 1)
	if err != nil || partEfi == "" {
		return fmt.Errorf("unable to get partition 1 of %s: %w", device, err)
	}
	partBoot, err := filesystems.BlockDeviceNthPartition(device, 2)
	if err != nil || partBoot == "" {
		return fmt.Errorf("unable to get partition 2 of %s: %w", device, err)
	}
	partRoot, err := filesystems.BlockDeviceNthPartition(device, 3)
	if err != nil || partRoot == "" {
		return fmt.Errorf("unable to get partition 3 of %s: %w", device, err)
	}

	for _, lp := range []string{partEfi, partBoot, partRoot} {
		if !filesystems.PathExists(lp) {
			return fmt.Errorf("%s does not exist", lp)
		}
	}

	im.SetEfiDevice(partEfi)
	if err := im.FormatEfifs(); err != nil {
		return fmt.Errorf("failed to format EFI: %w", err)
	}
	im.SetBootDevice(partBoot)
	im.SetRootDevice(partRoot)
	return nil
}

// setupImageFile creates a disk image file, sets up a loop device,
// partitions it, and formats the EFI partition.
func (im *Imager) setupImageFile(efiSize, bootSize, imageSize string) error {
	imagePath, err := im.BuildImagePath()
	if err != nil {
		return err
	}
	im.SetImagePath(imagePath)
	if err := im.SetImageMode(ModeCreateImageFile); err != nil {
		return err
	}

	if err := im.CreateImage(imageSize); err != nil {
		return fmt.Errorf("failed to create image: %w", err)
	}

	im.SetDevicePath(imagePath)
	if err := im.PartitionDevices(efiSize, bootSize, imageSize); err != nil {
		return fmt.Errorf("failed to partition image: %w", err)
	}

	loop, err := filesystems.NewLoop(imagePath)
	if err != nil {
		return fmt.Errorf("failed to create loop device: %w", err)
	}
	im.trackLoopDevice(loop)

	if err := loop.Attach(); err != nil {
		return fmt.Errorf("failed to attach image %s as loop device: %w",
			imagePath, err)
	}
	blockDevice := loop.Device
	im.SetDevicePath(blockDevice)
	filesystems.DevicesSettle()

	loopPartEfi := blockDevice + "p1"
	loopPartBoot := blockDevice + "p2"
	loopPartRoot := blockDevice + "p3"
	for _, lp := range []string{loopPartEfi, loopPartBoot, loopPartRoot} {
		if !filesystems.PathExists(lp) {
			return fmt.Errorf("%s does not exist", lp)
		}
	}

	im.SetEfiDevice(loopPartEfi)
	if err := im.FormatEfifs(); err != nil {
		return fmt.Errorf("failed to format EFI: %w", err)
	}
	im.SetBootDevice(loopPartBoot)
	im.SetRootDevice(loopPartRoot)
	return nil
}

// setupExistingPartitions configures the build to deploy onto
// pre-existing device partitions without reformatting EFI.
func (im *Imager) setupExistingPartitions(opts *BuildOptions) error {
	im.Print("EFI System Partition at %s will NOT be formatted. (yay?)\n", opts.EfiDevice)
	im.Print("Boot device %s will be used to derive parent block device, and install bootloader.\n", opts.BootDevice)
	im.SetEfiDevice(opts.EfiDevice)
	im.SetBootDevice(opts.BootDevice)
	im.SetRootDevice(opts.RootDevice)

	blockDevice, err := filesystems.BlockDeviceForPartition(opts.BootDevice)
	if err != nil {
		return fmt.Errorf("failed to determine parent block device: %w", err)
	}
	im.SetDevicePath(blockDevice)
	if err := im.SetImageMode(ModeFlashToDevice); err != nil {
		return err
	}
	return nil
}

// formatAndMountFilesystems formats boot/root partitions (with optional
// encryption), mounts rootfs, EFI, and boot, and returns the root device UUID.
func (im *Imager) formatAndMountFilesystems(mountRootfs string) (string, error) {
	if err := im.FormatBootfs(); err != nil {
		return "", fmt.Errorf("failed to format boot: %w", err)
	}

	if err := im.MaybeEncryptRootfs(); err != nil {
		return "", fmt.Errorf("failed to enable encryption: %w", err)
	}

	if err := im.FormatRootfs(); err != nil {
		return "", fmt.Errorf("failed to format rootfs: %w", err)
	}
	rootDeviceUUID, err := filesystems.DeviceUUID(im.RootDevice())
	if err != nil || rootDeviceUUID == "" {
		return "", fmt.Errorf("unable to get UUID for %s: %w", im.RootDevice(), err)
	}

	if err := im.MountRootfs(mountRootfs); err != nil {
		return "", fmt.Errorf("failed to mount rootfs: %w", err)
	}

	efiRoot, err := im.EfiRoot()
	if err != nil {
		return "", err
	}
	mountEfifs := filepath.Join(mountRootfs, efiRoot)
	if err := im.MountEfifs(mountEfifs); err != nil {
		return "", fmt.Errorf("failed to mount EFI: %w", err)
	}

	bootRoot, err := im.BootRoot()
	if err != nil {
		return "", err
	}
	mountBootfs := filepath.Join(mountRootfs, bootRoot)
	if err := im.MountBootfs(mountBootfs); err != nil {
		return "", fmt.Errorf("failed to mount boot: %w", err)
	}

	return rootDeviceUUID, nil
}

// deployOstree generates kernel boot arguments, deploys the ostree ref
// into the mounted rootfs, and verifies the deployed environment.
func (im *Imager) deployOstree(mountRootfs, rootDeviceUUID string) error {
	kernelBootArgs, err := im.GenerateKernelBootArgs()
	if err != nil {
		return fmt.Errorf("failed to generate kernel boot args: %w", err)
	}
	bootArgs := append(kernelBootArgs,
		"root=UUID="+rootDeviceUUID,
		"rw",
		"splash",
		"quiet",
	)
	im.Print("Boot arguments: %s\n", strings.Join(bootArgs, " "))

	im.Print("\nDeploying ostree into %s ...\n", mountRootfs)

	if err := im.ostree.AddRemote(); err != nil {
		return fmt.Errorf("failed to add ostree remote: %w", err)
	}

	if err := im.ostree.Deploy(mountRootfs, bootArgs); err != nil {
		return fmt.Errorf("ostree deploy failed: %w", err)
	}

	if err := im.ostree.AddRemoteToRootfs(mountRootfs); err != nil {
		return fmt.Errorf("failed to add remote to rootfs: %w", err)
	}

	rootfs, err := im.ostree.DeployedRootfs()
	if err != nil {
		return fmt.Errorf("failed to get deployed rootfs: %w", err)
	}
	im.SetRootfs(rootfs)
	if !strings.HasPrefix(rootfs, mountRootfs) {
		return fmt.Errorf("deployed rootfs %s is not under expected mount rootfs %s", rootfs, mountRootfs)
	}

	im.Print("Verifying rootfs environment ...\n")
	if err := im.qa.VerifyDistroRootfsEnvironmentSetup(rootfs); err != nil {
		return fmt.Errorf("rootfs environment verification failed: %w", err)
	}

	return nil
}

// installSystemComponents sets up the bootloader, passwords, secureboot
// certificates, memtest, and runs post-deploy hooks.
func (im *Imager) installSystemComponents() error {
	if err := im.SetupBootloaderConfig(); err != nil {
		return fmt.Errorf("failed to setup bootloader config: %w", err)
	}
	if err := im.SetupPasswords(); err != nil {
		return fmt.Errorf("failed to setup passwords: %w", err)
	}
	if err := im.InstallBootloader(); err != nil {
		return fmt.Errorf("failed to install bootloader: %w", err)
	}

	// Set up VM test config only if creating an image file, not flashing to a device.
	switch im.ImageMode() {
	case ModeCreateImageFile:
		if err := im.SetupVmtestConfig(); err != nil {
			return fmt.Errorf("failed to setup vmtest config: %w", err)
		}
	}

	if err := im.InstallSecurebootCerts(); err != nil {
		return fmt.Errorf("failed to install secureboot certs: %w", err)
	}
	if err := im.InstallMemtest(); err != nil {
		return fmt.Errorf("failed to install memtest: %w", err)
	}
	if err := im.SetupHooks(); err != nil {
		return fmt.Errorf("failed to run hooks: %w", err)
	}
	return nil
}

// extractBuildMetadata retrieves the package list and release version
// from the deployed rootfs.
func (im *Imager) extractBuildMetadata() (string, []string, error) {
	pkgList, err := im.ExtractPackageList()
	if err != nil {
		return "", nil, fmt.Errorf("failed to get package list: %w", err)
	}
	releaseVersion, err := im.ExtractReleaseVersion()
	if err != nil {
		return "", nil, fmt.Errorf("failed to get release version: %w", err)
	}
	return releaseVersion, pkgList, nil
}

// finalizeBuild finalizes filesystems, cleans up mounts, and handles
// post-processing based on the image mode.
func (im *Imager) finalizeBuild(releaseVersion string, pkgList []string) error {
	if err := im.FinalizeFilesystems(); err != nil {
		return fmt.Errorf("failed to finalize filesystems: %w", err)
	}
	if err := im.ShowFinalFilesystemInfo(); err != nil {
		return fmt.Errorf("failed to show filesystem info: %w", err)
	}

	im.Print("Running ostree post-copy phase ...\n")
	if err := im.ostree.PostCopy(); err != nil {
		return fmt.Errorf("ostree post-copy phase failed: %w", err)
	}

	// This unmounts everything, after this point we cannot touch the filesystems
	// anymore.
	im.Cleanup()

	switch im.ImageMode() {
	case ModeFlashToDevice:
		im.Print("On device install complete!\n")
		return nil
	case ModeCreateImageFile:
		if err := im.postImageCreation(releaseVersion, pkgList); err != nil {
			return fmt.Errorf("post image creation failed: %w", err)
		}
		im.Print("Image creation complete!\n")
		return nil
	default:
		return fmt.Errorf("unknown image mode: %v", im.ImageMode())
	}
}

// addSysrootOverlay updates the config with the sysroot overlay and validates it.
func (im *Imager) addSysrootOverlay(mountRootfs string) error {
	sysrootOverlay := map[string][]string{
		"Ostree.Sysroot": {mountRootfs},
	}
	if err := im.cfg.AddOverlay(sysrootOverlay); err != nil {
		return fmt.Errorf("failed to add config overlay: %w", err)
	}
	// Safety check
	sysroot, err := im.ostree.Sysroot()
	if err != nil {
		return err
	}
	if sysroot != mountRootfs {
		return fmt.Errorf(
			"sysroot from ostree config does not match expected mount rootfs")
	}
	return nil
}

// postImageCreation handles the post-build processing for image files,
// including optional productionization steps.
func (im *Imager) postImageCreation(releaseVersion string, pkgList []string) error {
	var generatedArtifacts []string

	productionize, err := im.Productionize()
	if err != nil {
		return fmt.Errorf("failed to determine if productionization is enabled: %w", err)
	}

	if !productionize {
		generatedArtifacts = append(generatedArtifacts, im.ImagePath())
		if err := im.ShowImageTestInfo(generatedArtifacts); err != nil {
			return fmt.Errorf("failed to show image test info: %w", err)
		}
		return nil
	}

	im.Print("\nProductionizing image for release version %s ...\n", releaseVersion)

	generatedArtifacts, err = im.productionizeImage(releaseVersion, pkgList)
	if err != nil {
		return fmt.Errorf("productionization failed: %w", err)
	}

	if err := im.ShowImageTestInfo(generatedArtifacts); err != nil {
		return fmt.Errorf("failed to show image test info: %w", err)
	}
	return nil
}

// productionizeImage handles post-build image processing:
// renaming with version, testing, QCOW2 creation, compression, checksums, GPG signing.
func (im *Imager) productionizeImage(releaseVersion string, pkgList []string) ([]string, error) {
	var artifacts []string

	// Rename image with release version.
	versionedImagePath, err := im.BuildImagePathWithReleaseVersion(releaseVersion)
	if err != nil {
		return artifacts, err
	}
	im.Print("Moving %s to %s ...\n", im.ImagePath(), versionedImagePath)
	if err := filesystems.Move(im.ImagePath(), versionedImagePath); err != nil {
		return artifacts, fmt.Errorf("failed to rename image: %w", err)
	}
	im.SetImagePath(versionedImagePath)
	if err := os.Chmod(im.ImagePath(), 0644); err != nil {
		return artifacts, fmt.Errorf("failed to chmod image: %w", err)
	}

	// Run image tests. Usually spawning a QEMU VM.
	run, err := im.ImageTests()
	if err != nil {
		return artifacts, err
	}
	if run {
		if err := im.TestImage(); err != nil {
			return artifacts, fmt.Errorf("image tests failed: %w", err)
		}
	}

	qcow2Created, err := im.buildCreateQcow2()
	if err != nil {
		return artifacts, err
	}
	if qcow2Created {
		qcow2ImagePath, err := im.Qcow2ImagePath()
		if err != nil {
			return artifacts, err
		}
		artifacts = append(artifacts, qcow2ImagePath)
	}

	pkgListPath, err := im.createPackageListFile(pkgList)
	if err != nil {
		return artifacts, fmt.Errorf("failed to create package list file: %w", err)
	}
	artifacts = append(artifacts, pkgListPath)

	compressor, err := im.Compressor()
	if err != nil {
		return artifacts, fmt.Errorf("failed to determine compressor: %w", err)
	}
	// Activate compressor only if set.
	compressedImageCreated := false
	if compressor != "" {
		im.Print("Compressing image with %s ...\n", compressor)
		if err := im.CompressImage(); err != nil {
			return artifacts, fmt.Errorf("failed to compress image: %w", err)
		}
		compressedImageCreated = true

		cmpPath, err := im.CompressedImagePath()
		if err != nil {
			return artifacts, err
		}
		artifacts = append(artifacts, cmpPath)
	} else {
		artifacts = append(artifacts, im.ImagePath())
	}

	sha256Paths, err := im.buildSha256sums(compressedImageCreated, qcow2Created)
	if err != nil {
		return artifacts, fmt.Errorf("failed to create sha256sums: %w", err)
	}
	artifacts = append(artifacts, sha256Paths...)

	gpgArtifacts, err := im.maybeGenerateGpgSignatures(compressedImageCreated, qcow2Created)
	if err != nil {
		return artifacts, fmt.Errorf("failed to generate GPG signatures: %w", err)
	}
	artifacts = append(artifacts, gpgArtifacts...)

	return artifacts, nil
}

// maybeGenerateGpgSignatures creates GPG signatures for the image and QCOW2 (if created),
// and returns the paths to the signature files.
func (im *Imager) maybeGenerateGpgSignatures(compressedImageCreated, qcow2Created bool) ([]string, error) {
	gpgEnabled, err := im.ostree.GpgEnabled()
	if err != nil {
		return nil, err
	}

	if !gpgEnabled {
		im.PrintWarning("WARNING: GPG signing of images not enabled in settings.\n")
		return nil, nil
	}

	var artifacts []string
	gpgKeyPath, err := im.ostree.GpgPrivateKeyPath()
	if err != nil {
		return artifacts, err
	}
	if !filesystems.PathExists(gpgKeyPath) {
		im.PrintWarning("WARNING: %s not found. Cannot create GPG signatures of image.\n", gpgKeyPath)
		return artifacts, fmt.Errorf("%s not found, cannot create GPG signatures", gpgKeyPath)
	}

	im.Print("%s exists, creating GPG signatures ...\n", gpgKeyPath)
	if err := im.ostree.InitializeSigningGpg(); err != nil {
		return artifacts, fmt.Errorf("failed to initialize signing GPG: %w", err)
	}

	if compressedImageCreated {
		cmpPath, err := im.CompressedImagePath()
		if err != nil {
			return artifacts, fmt.Errorf("cannot get compressed image path: %w", err)
		}
		if err := im.ostree.GpgSignFile(cmpPath); err != nil {
			return artifacts, fmt.Errorf("failed to GPG sign image: %w", err)
		}
		artifacts = append(artifacts, ostree.GpgSignedFilePath(cmpPath))
	} else {
		if err := im.ostree.GpgSignFile(im.ImagePath()); err != nil {
			return artifacts, fmt.Errorf("failed to GPG sign image: %w", err)
		}
		artifacts = append(artifacts, ostree.GpgSignedFilePath(im.ImagePath()))
	}

	if qcow2Created {
		qcow2ImagePath, err := im.Qcow2ImagePath()
		if err != nil {
			return artifacts, err
		}

		im.Print("Creating GPG signatures of: %s\n", qcow2ImagePath)
		if err := im.ostree.GpgSignFile(qcow2ImagePath); err != nil {
			return artifacts, fmt.Errorf("failed to GPG sign QCOW2: %w", err)
		}
		artifacts = append(artifacts, ostree.GpgSignedFilePath(qcow2ImagePath))
	}

	// Store the GPG pubkey for later mirroring to CDNs.
	pubKeyPath, err := im.ostree.GpgBestPubKeyPath()
	if err != nil {
		return artifacts, err
	}
	gpgPubkeyImagePath := im.ImagePath() + GpgPubKeyAscExt
	if err := filesystems.CopyFile(pubKeyPath, gpgPubkeyImagePath); err != nil {
		return artifacts, fmt.Errorf("failed to copy GPG pubkey: %w", err)
	}
	artifacts = append(artifacts, gpgPubkeyImagePath)

	return artifacts, nil
}

// buildCreateQcow2 creates a QCOW2 image if enabled in settings, and returns whether it was created.
func (im *Imager) buildCreateQcow2() (bool, error) {
	createQcow2, err := im.CreateQcow2()
	if err != nil {
		return false, err
	}
	if !createQcow2 {
		return false, nil
	}

	im.Print("Creating QCOW2 image for %s ...\n", im.ImagePath())
	if err := im.CreateQcow2Image(); err != nil {
		return false, fmt.Errorf("QCOW2 creation failed: %w", err)
	}
	qcow2ImagePath, err := im.Qcow2ImagePath()
	if err != nil {
		return false, err
	}
	im.Print("QCOW2 image created: %s\n", qcow2ImagePath)
	return true, nil
}

// createPackageListFile creates a text file listing the packages included in the
// image, and returns the path to the file.
func (im *Imager) createPackageListFile(pkgList []string) (string, error) {
	pkgListPath := im.ImagePath() + PackagesFileExt
	im.Print("Creating package list file: %s\n", pkgListPath)
	pkgListData := strings.Join(pkgList, "\n") + "\n"
	if err := os.WriteFile(pkgListPath, []byte(pkgListData), 0644); err != nil {
		return "", fmt.Errorf("failed to write package list: %w", err)
	}
	return pkgListPath, nil
}

// buildSha256sums creates sha256 checksum files for the image and returns
// the paths to the checksum files.
func (im *Imager) buildSha256sums(compressedImageCreated, qcow2Created bool) ([]string, error) {
	var sha256Paths []string

	imagePath := im.ImagePath()
	if compressedImageCreated {
		cmpPath, err := im.CompressedImagePath()
		if err != nil {
			return nil, err
		}

		sha256Path := cmpPath + Sha256Ext
		im.Print("Creating sha256sum of: %s\n", cmpPath)
		if err := filesystems.Sha256(cmpPath, sha256Path); err != nil {
			return nil, err
		}
		sha256Paths = append(sha256Paths, sha256Path)
	} else {
		sha256Path := imagePath + Sha256Ext

		im.Print("Creating sha256sum of: %s\n", im.ImagePath())
		if err := filesystems.Sha256(im.ImagePath(), sha256Path); err != nil {
			return nil, err
		}
		sha256Paths = append(sha256Paths, sha256Path)
	}

	if qcow2Created {
		qcow2ImagePath, err := im.Qcow2ImagePath()
		if err != nil {
			return nil, err
		}
		qcow2Dir := filepath.Dir(qcow2ImagePath)
		qcow2Name := filepath.Base(qcow2ImagePath)
		qcow2Sha256 := filepath.Join(qcow2Dir, qcow2Name+Sha256Ext)

		im.Print("Creating sha256sum of: %s\n", qcow2ImagePath)
		if err := filesystems.Sha256(qcow2ImagePath, qcow2Sha256); err != nil {
			return nil, err
		}
		sha256Paths = append(sha256Paths, qcow2Sha256)
	}

	return sha256Paths, nil
}
