package imager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"matrixos/vector/lib/filesystems"
)

// Compile-time interface check.
var _ Bootloader = (*SystemdBootBootloader)(nil)

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

	if _, err := im.prepareBootloaderConfig(); err != nil {
		return err
	}

	// Write loader/loader.conf onto the EFI partition.
	loaderDir := filepath.Join(im.efifsMount, "loader")
	if err := os.MkdirAll(loaderDir, 0755); err != nil {
		return fmt.Errorf("failed to create loader dir %s: %w", loaderDir, err)
	}
	loaderConf := "default 0\ntimeout 3\nconsole-mode keep\n"
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
	envContent := fmt.Sprintf("SYSTEMD_BOOT_ESP=%s/\n", efiRoot)
	return im.writeBootloaderEnvFile("99-matrixos-imager-systemd-boot.conf", envContent)
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

	info, err := im.prepareBootloaderInstall()
	if err != nil {
		return err
	}

	im.Print("Installing systemd-boot ...\n")

	err = im.chroot(
		info.chrootEnv,
		"/usr/bin/bootctl",
		[]string{
			"install",
			"--esp-path=" + info.efiRoot,
			"--boot-path=" + info.bootRoot,
			"--variables=no",
			"--all-architectures",
		},
	)
	if err != nil {
		return fmt.Errorf("bootctl install failed: %w", err)
	}

	// Verify BOOTX64.EFI was created.
	bootx64efi, err := verifyEfiExecutable(info.efibootDir, info.efiExe, "bootctl install")
	if err != nil {
		return err
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
func (s *SystemdBootBootloader) ConfigureMemtest() error {
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
