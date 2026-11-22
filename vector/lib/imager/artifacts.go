package imager

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
)

// --- Helpers ---

// buildImagePath builds the full image file path from a suffix.
func (im *Imager) buildImagePath(suffix string) (string, error) {
	outDir, err := im.ImagesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(outDir, suffix), nil
}

// cleanAndStripRef cleans a remote prefix and removes the -full suffix from the stored ref.
func (im *Imager) cleanAndStripRef() (string, error) {
	if im.ref == "" {
		return "", errors.New("missing ref, set Ref in NewImagerOptions")
	}
	return ostree.CleanAndStripRef(im.ostree)
}

// refToSuffix converts slashes in a ref to underscores for use in file names.
func refToSuffix(ref string) string {
	return strings.ReplaceAll(ref, "/", "_")
}

// validateImageModeForCreation checks that the image mode is set to
// ModeCreateImageFile and that the imagePath is not empty.
func (im *Imager) validateImageModeForCreation() error {
	if im.mode != ModeCreateImageFile {
		return errors.New("invalid image creation mode")
	}
	if im.imagePath == "" {
		return errors.New("missing imagePath parameter")
	}
	return nil
}

// --- Operations ---

func (im *Imager) extractSeedName(data []byte) (string, error) {
	// Extract version from SEED_NAME= line.
	var releaseVersion string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "SEED_NAME=") {
			continue
		}

		seedName := strings.TrimPrefix(line, "SEED_NAME=")
		// Version is the part after the last '-'.
		if idx := strings.LastIndex(seedName, "-"); idx >= 0 {
			releaseVersion = seedName[idx+1:]
			im.Print("Extracted release version: %s\n", releaseVersion)
		} else {
			im.PrintWarning("WARNING: SEED_NAME= value has no '-' separator\n")
		}
		break

	}
	if scanner.Err() != nil {
		return releaseVersion, fmt.Errorf("failed to scan build metadata file: %w", scanner.Err())
	}
	return releaseVersion, nil
}

func (im *Imager) ExtractReleaseVersion() (string, error) {
	if im.rootfs == "" {
		return "", errors.New("rootfs not set, call SetRootfs first")
	}

	releaseVersion := time.Now().Format("20060102")
	metadataRelPath, err := im.BuildMetadataFile()
	if err != nil {
		return "", fmt.Errorf(
			"failed to determine build metadata file path: %w", err)
	}
	metadataFile := filepath.Join(im.rootfs, metadataRelPath)

	if filesystems.FileExists(metadataFile) {
		im.Print("Build metadata:\n")
		data, err := os.ReadFile(metadataFile)
		if err != nil {
			return "", fmt.Errorf(
				"failed to read build metadata file %s: %w", metadataFile, err)
		}
		im.Print("%s", string(data))

		releaseVersion, err = im.extractSeedName(data)
		if err != nil {
			return "", fmt.Errorf(
				"failed to extract release version from build metadata: %w", err)
		}
	} else {
		im.PrintWarning(
			"WARNING! Build metadata file not found: %s\n", metadataFile,
		)
	}

	return releaseVersion, nil
}

func (im *Imager) BuildImagePath() (string, error) {
	if im.ref == "" {
		return "", errors.New("missing ref, set Ref in NewImagerOptions")
	}
	ref := ostree.CleanRemoteFromRef(im.ref)
	suffix := refToSuffix(ref) + ".img"
	return im.buildImagePath(suffix)
}

func (im *Imager) BuildImagePathWithReleaseVersion(releaseVersion string) (string, error) {
	if im.ref == "" {
		return "", errors.New("missing ref, set Ref in NewImagerOptions")
	}
	if releaseVersion == "" {
		return "", errors.New("missing releaseVersion parameter")
	}
	ref := ostree.CleanRemoteFromRef(im.ref)
	suffix := refToSuffix(ref) + "-" + releaseVersion + ".img"
	return im.buildImagePath(suffix)
}

func (im *Imager) CompressedImagePath() (string, error) {
	if err := im.validateImageModeForCreation(); err != nil {
		return "", err
	}
	compressor, err := im.Compressor()
	if err != nil {
		return "", fmt.Errorf("failed to get compressor: %w", err)
	}
	if compressor == "" {
		return "", errors.New("missing compressor parameter")
	}
	parts := strings.Fields(compressor)
	if len(parts) == 0 {
		return "", errors.New("invalid compressor parameters: empty command")
	}
	return im.imagePath + "." + parts[0], nil
}

func (im *Imager) CompressImage() error {
	imagePathWithExt, err := im.CompressedImagePath()
	if err != nil {
		return err
	}
	compressor, err := im.Compressor()
	if err != nil {
		return fmt.Errorf("failed to get compressor: %w", err)
	}
	parts := strings.Fields(compressor)

	args := append(parts[1:], im.imagePath)
	cmd := &runner.Cmd{
		Name:   parts[0],
		Args:   args,
		Stdout: im.Stdout(),
		Stderr: im.Stderr(),
	}
	if err := im.runner(cmd); err != nil {
		return fmt.Errorf("compression failed: %w", err)
	}

	if !filesystems.FileExists(imagePathWithExt) {
		return fmt.Errorf("compressed image was not created at the expected path: %s", imagePathWithExt)
	}
	return nil
}

func (im *Imager) Qcow2ImagePath() (string, error) {
	if err := im.validateImageModeForCreation(); err != nil {
		return "", err
	}
	return im.imagePath + ".qcow2", nil
}

func (im *Imager) CreateQcow2Image() error {
	if err := im.validateImageModeForCreation(); err != nil {
		return err
	}
	qcow2Path, _ := im.Qcow2ImagePath()
	return im.runner(&runner.Cmd{
		Name: "qemu-img",
		Args: []string{
			"convert",
			"-c",
			"-O",
			"qcow2",
			"-p",
			im.imagePath,
			qcow2Path,
		},
		Stdout: im.Stdout(),
		Stderr: im.Stderr(),
	})
}

func (im *Imager) RemoveImageFile() error {
	if err := im.validateImageModeForCreation(); err != nil {
		return err
	}

	im.Print("Removing %s ...\n", im.imagePath)
	for _, path := range []string{im.imagePath, im.imagePath + ".sha256", im.imagePath + ".asc"} {
		os.Remove(path) // Ignore errors (file may not exist).
	}
	return nil
}

func (im *Imager) ShowFinalFilesystemInfo() error {
	if im.devicePath == "" {
		return errors.New("missing devicePath, not set in NewImagerOptions")
	}
	if im.bootfsMount == "" {
		return errors.New("missing bootfsMount, call MountBootfs first")
	}
	if im.efifsMount == "" {
		return errors.New("missing efifsMount, call MountEfifs first")
	}

	im.Print("Final boot partition directory tree:\n")
	if err := filesystems.PrintDirectoryTree(im.Stdout(), im.bootfsMount); err != nil {
		im.PrintWarning("WARNING: failed to list boot directory tree: %v\n", err)
	}

	im.Print("Final EFI partition directory tree:\n")
	if err := filesystems.PrintDirectoryTree(im.Stdout(), im.efifsMount); err != nil {
		im.PrintWarning("WARNING: failed to list EFI directory tree: %v\n", err)
	}

	im.Print("Block devices on %s:\n", im.devicePath)
	if err := filesystems.PrintBlockDeviceInfo(im.Stdout(), im.devicePath); err != nil {
		im.PrintWarning("WARNING: failed to get block device info: %v\n", err)
	}

	im.Print("Filesystem setup complete!\n")
	return nil
}

func (im *Imager) ShowImageTestInfo(artifacts []string) error {
	if err := im.validateImageModeForCreation(); err != nil {
		return err
	}

	if len(artifacts) != 0 {
		im.Print("Generated artifacts:\n")
		for _, a := range artifacts {
			im.Print(">> %s\n", a)
		}
	}

	im.Print("How to test:\n")
	im.Print("    # vector dev vm -image %s -memory 8G -interactive\n", im.imagePath)
	im.Print("To move to a USB stick:\n")
	im.Print("    xz -dc %s | dd of=/dev/sdX bs=4M conv=sparse,fsync status=progress\n", im.imagePath)
	return nil
}
