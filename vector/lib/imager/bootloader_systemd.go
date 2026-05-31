package imager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"matrixos/vector/lib/filesystems"
)

// SystemdBootBootloader implements Bootloader for systemd-boot.
type SystemdBootBootloader struct {
	im *Imager
}

// NewSystemdBootBootloader creates a new SystemdBootBootloader backed by the given Imager.
func NewSystemdBootBootloader(im *Imager) *SystemdBootBootloader {
	return &SystemdBootBootloader{im: im}
}

func (s *SystemdBootBootloader) Configure() error {
	im := s.im

	if im.rootfs == "" {
		return errors.New("rootfs not set, call SetRootfs first")
	}
	if im.efiDevice == "" {
		return errors.New("missing efiDevice, not set in NewImagerOptions")
	}
	if im.bootDevice == "" {
		return errors.New("missing bootDevice, not set in NewImagerOptions")
	}
	if im.bootfsMount == "" {
		return errors.New("missing bootfsMount, call MountBootfs first")
	}
	if im.rootfsMount == "" {
		return errors.New("missing rootfsMount, call MountRootfs first")
	}

	efibootDir, err := im.EfiBootDir()
	if err != nil {
		return fmt.Errorf("failed to determine EFI boot directory: %w", err)
	}

	_, err = filesystems.DeviceUUID(im.efiDevice)
	if err != nil {
		return fmt.Errorf("unable to get UUID for %s: %w", im.efiDevice, err)
	}
	_, err = filesystems.DeviceUUID(im.bootDevice)
	if err != nil {
		return fmt.Errorf("unable to get UUID for %s: %w", im.bootDevice, err)
	}

	// Verify kernel exists.
	if _, err := im.GetKernelPath(); err != nil {
		return fmt.Errorf("failed to determine kernel version: %w", err)
	}

	// Get the boot commit.
	bootCommit, err := im.ostree.BootCommit(im.rootfsMount)
	if err != nil || bootCommit == "" {
		return fmt.Errorf("cannot determine ostree boot commit: %w", err)
	}
	im.Print("Found boot commit: %s\n", bootCommit)

	// Ensure efibootDir exists.
	if err := os.MkdirAll(efibootDir, 0755); err != nil {
		return fmt.Errorf("failed to create efibootDir %s: %w", efibootDir, err)
	}

	// Write loader/loader.conf onto the boot partition.
	loaderDir := filepath.Join(im.efifsMount, "loader")
	if err := os.MkdirAll(loaderDir, 0755); err != nil {
		return fmt.Errorf("failed to create loader dir %s: %w", loaderDir, err)
	}
	loaderConf := "default @saved\ntimeout 3\nconsole-mode keep\n"
	loaderConfPath := filepath.Join(loaderDir, "loader.conf")
	if err := os.WriteFile(loaderConfPath, []byte(loaderConf), 0644); err != nil {
		return fmt.Errorf("failed to write loader.conf: %w", err)
	}
	im.Print("Wrote loader.conf:\n%s\n", loaderConf)

	// Write an environment file so the running system knows which ESP is in use.
	efiRoot, err := im.EfiRoot()
	if err != nil {
		return err
	}
	envDir := filepath.Join(im.rootfs, "etc", "environment.d")
	if err := os.MkdirAll(envDir, 0755); err != nil {
		return fmt.Errorf("failed to create environment.d dir: %w", err)
	}
	envContent := fmt.Sprintf("SYSTEMD_BOOT_ESP=%s/\n", efiRoot)
	envPath := filepath.Join(envDir, "99-matrixos-imager-systemd-boot.conf")
	if err := os.WriteFile(envPath, []byte(envContent), 0644); err != nil {
		return fmt.Errorf("failed to write systemd-boot env config: %w", err)
	}

	return nil
}

func (s *SystemdBootBootloader) BootArgs() ([]string, error) {
	return []string{
		"plymouth.ignore-serial-consoles",
		"console=ttyS0,115200",
		"console=tty0",
	}, nil
}

func (s *SystemdBootBootloader) Install() error {
	im := s.im

	if im.rootfs == "" {
		return errors.New("rootfs not set, call SetRootfs first")
	}
	if im.efifsMount == "" {
		return errors.New("missing efifsMount, call MountEfifs first")
	}
	if im.bootfsMount == "" {
		return errors.New("missing bootfsMount, call MountBootfs first")
	}
	if im.devicePath == "" {
		return errors.New("missing devicePath, not set in NewImagerOptions")
	}

	efibootDir, err := im.EfiBootDir()
	if err != nil {
		return fmt.Errorf("failed to determine EFI boot directory: %w", err)
	}

	im.Print("Installing systemd-boot ...\n")

	efiRoot, err := im.EfiRoot()
	if err != nil {
		return fmt.Errorf("failed to determine EFI root: %w", err)
	}
	bootRoot, err := im.BootRoot()
	if err != nil {
		return fmt.Errorf("failed to determine boot root: %w", err)
	}

	env := []string{
		"IMAGER_EFI_MOUNT=" + im.efifsMount,
		"IMAGER_BOOT_MOUNT=" + im.bootfsMount,
		"IMAGER_EFI_ROOT=" + efiRoot,
		"IMAGER_BOOT_ROOT=" + bootRoot,
	}

	err = im.chroot(
		env,
		"/usr/bin/bootctl",
		[]string{
			"install",
			"--esp-path=" + efiRoot,
			"--boot-path=" + bootRoot,
			"--variables=no",
			"--all-architectures",
		},
	)
	if err != nil {
		return fmt.Errorf("bootctl install failed: %w", err)
	}

	// Verify BOOTX64.EFI was created.
	efiExe, err := im.EfiExecutable()
	if err != nil {
		return fmt.Errorf("failed to determine EFI executable: %w", err)
	}
	bootx64efi := filepath.Join(efibootDir, efiExe)
	if !filesystems.PathExists(bootx64efi) {
		return fmt.Errorf("%s does not exist after bootctl install", bootx64efi)
	}

	// Replace the unsigned systemd-boot EFI binary with the signed one.
	signedBin := filepath.Join(im.rootfs, "usr", "lib", "systemd", "boot", "efi", "systemd-bootx64.efi.signed")
	if filesystems.FileExists(signedBin) {
		// bootctl installs the EFI binary at <esp>/EFI/systemd/systemd-bootx64.efi;
		// the fallback BOOTX64.EFI is a copy. Replace both with the signed binary.
		systemdEfiDir := filepath.Join(im.efifsMount, "EFI", "systemd")
		installedBin := filepath.Join(systemdEfiDir, "systemd-bootx64.efi")
		im.Print("Replacing %s with signed binary %s\n", installedBin, signedBin)
		if err := filesystems.CopyFile(signedBin, installedBin); err != nil {
			return fmt.Errorf("failed to copy signed systemd-boot binary: %w", err)
		}
		im.Print("Replacing %s with signed binary %s\n", bootx64efi, signedBin)
		if err := filesystems.CopyFile(signedBin, bootx64efi); err != nil {
			return fmt.Errorf("failed to copy signed systemd-boot fallback binary: %w", err)
		}
	} else {
		im.PrintWarning("signed systemd-boot binary not found at %s, skipping replacement\n", signedBin)
	}

	return nil
}

// ConfigureMemtest writes a systemd-boot Type-1 loader entry for the
// memtest86+ EFI binary already installed by InstallMemtest at
// <esp>/<RelativeEfiBootPath>/memtest86plus.efi.
func (s *SystemdBootBootloader) ConfigureMemtest(memtestBin string) error {
	im := s.im

	if im.efifsMount == "" {
		return errors.New("missing efifsMount, call MountEfifs first")
	}

	relEfiBootPath, err := im.RelativeEfiBootPath()
	if err != nil {
		return fmt.Errorf("failed to determine relative EFI boot path: %w", err)
	}

	entriesDir := filepath.Join(im.efifsMount, "loader", "entries")
	if err := os.MkdirAll(entriesDir, 0755); err != nil {
		return fmt.Errorf("failed to create loader entries dir %s: %w", entriesDir, err)
	}

	// systemd-boot wants forward-slash, ESP-relative paths starting with /.
	efiPath := "/" + filepath.ToSlash(filepath.Join(relEfiBootPath, "memtest86plus.efi"))
	entry := fmt.Sprintf("title    Memtest86+\nefi      %s\n", efiPath)
	entryPath := filepath.Join(entriesDir, "memtest.conf")
	if err := os.WriteFile(entryPath, []byte(entry), 0644); err != nil {
		return fmt.Errorf("failed to write memtest loader entry: %w", err)
	}
	im.Print("Wrote systemd-boot memtest entry %s -> %s\n", entryPath, efiPath)
	return nil
}

// ConfigureVmtest is a no-op for systemd-boot for now.
// The GRUB implementation uses SMBIOS detection at runtime, which systemd-boot
// cannot replicate. Systemd-boot works fine without any special configuration.
func (s *SystemdBootBootloader) ConfigureVmtest() error {
	im := s.im

	if im.bootfsMount == "" {
		return errors.New("missing bootfsMount, call MountBootfs first")
	}

	ostreeBootCfg := filepath.Join(im.bootfsMount, "loader", "entries", "ostree-1.conf")
	if !filesystems.FileExists(ostreeBootCfg) {
		return fmt.Errorf("%s does not exist, cannot set up vmtest config", ostreeBootCfg)
	}

	// No need for further changes for systemd-boot since we cannot use
	// the SMBIOS-based detection in vmtest.
	// The existing ostree-1.conf entry will be used as-is for vmtest.

	return nil
}
