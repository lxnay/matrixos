package imager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"matrixos/vector/lib/filesystems"
)

// Compile-time interface check.
var _ Bootloader = (*GrubBootloader)(nil)

// GrubBootloader implements Bootloader for the GRUB bootloader.
type GrubBootloader struct {
	im *Imager
}

// NewGrubBootloader creates a new GrubBootloader backed by the given Imager.
func NewGrubBootloader(im *Imager) *GrubBootloader {
	return &GrubBootloader{im: im}
}

func (g *GrubBootloader) Configure() error {
	im := g.im

	info, err := im.prepareBootloaderConfig()
	if err != nil {
		return err
	}

	ref, err := im.cleanAndStripRef()
	if err != nil {
		return fmt.Errorf("failed to clean ref: %w", err)
	}

	devDir, err := im.DevDir()
	if err != nil {
		return err
	}

	srcGrubCfg := filepath.Join(devDir, "image", "boot", ref, "grub.cfg")
	resolved, err := filepath.EvalSymlinks(srcGrubCfg)
	if err != nil {
		return fmt.Errorf("failed to resolve grub config path %s: %w", srcGrubCfg, err)
	}
	srcGrubCfg = resolved

	if !filesystems.FileExists(srcGrubCfg) {
		return fmt.Errorf("grub config %s does not exist", srcGrubCfg)
	}
	im.Print("Using grub config from %s\n", srcGrubCfg)

	dstGrubCfg := filepath.Join(info.efibootDir, "grub.cfg")
	im.Print("Copying grub: %s -> %s\n", srcGrubCfg, dstGrubCfg)
	if err := filesystems.CopyFile(srcGrubCfg, dstGrubCfg); err != nil {
		return fmt.Errorf("failed to copy grub config: %w", err)
	}

	// Copy GRUB themes if available.
	osName, err := im.OsName()
	if err != nil {
		return err
	}
	themesDir := filepath.Join(
		im.rootfs,
		"usr", "share", "grub",
		"themes", osName+"-theme",
	)
	if filesystems.DirectoryExists(themesDir) {
		im.Print("Copying GRUB themes from %s ...\n", themesDir)
		dstThemesDir := filepath.Join(im.bootfsMount, "grub", "themes")

		if err := os.MkdirAll(dstThemesDir, 0755); err != nil {
			return fmt.Errorf("failed to create themes dir: %w", err)
		}

		dstThemeDir := filepath.Join(dstThemesDir, filepath.Base(themesDir))

		if err := filesystems.CopyDirPreserve(themesDir, dstThemeDir); err != nil {
			return fmt.Errorf("failed to copy themes: %w", err)
		}
	}

	// Write GRUB_CFG environment file.
	efiRoot, err := im.EfiRoot()
	if err != nil {
		return err
	}
	relEfiBootPath, err := im.RelativeEfiBootPath()
	if err != nil {
		return err
	}

	grubCfgEnv := fmt.Sprintf("GRUB_CFG=%s/%s/grub.cfg\n", efiRoot, relEfiBootPath)
	if err := im.writeBootloaderEnvFile("99-matrixos-imager-grub.conf", grubCfgEnv); err != nil {
		return err
	}

	// Perform template substitutions in grub.cfg.
	grubData, err := os.ReadFile(dstGrubCfg)
	if err != nil {
		return fmt.Errorf("failed to read grub config for substitution: %w", err)
	}
	grubContent := string(grubData)
	grubContent = strings.ReplaceAll(grubContent, "%BOOTUUID%", info.bootDeviceUUID)
	grubContent = strings.ReplaceAll(grubContent, "%EFIUUID%", info.efiDeviceUUID)
	grubContent = strings.ReplaceAll(grubContent, "%OSNAME%", osName)
	if err := os.WriteFile(dstGrubCfg, []byte(grubContent), 0644); err != nil {
		return fmt.Errorf("failed to write substituted grub config: %w", err)
	}

	im.Print("Current grub.cfg:\n")
	im.Print("%s\n", grubContent)

	return nil
}

func (g *GrubBootloader) BootArgs() ([]string, error) {
	im := g.im

	if im.efiDevice == "" {
		return nil, errors.New("missing efiDevice, not set in NewImagerOptions")
	}
	if im.bootDevice == "" {
		return nil, errors.New("missing bootDevice, not set in NewImagerOptions")
	}

	// grub does not benefit from systemd-gpt-auto-generator the way systemd-boot
	// does: instruct systemd explicitly to mount the ESP and boot partitions.
	efiRoot, err := im.EfiRoot()
	if err != nil {
		return nil, err
	}
	efiPartUUID, err := filesystems.DevicePartUUID(im.efiDevice)
	if err != nil {
		return nil, fmt.Errorf("unable to get PARTUUID of EFI partition: %w", err)
	}
	bootRoot, err := im.BootRoot()
	if err != nil {
		return nil, err
	}
	bootPartUUID, err := filesystems.DevicePartUUID(im.bootDevice)
	if err != nil {
		return nil, fmt.Errorf("unable to get PARTUUID of boot partition: %w", err)
	}
	return []string{
		fmt.Sprintf("systemd.mount-extra=PARTUUID=%s:%s:auto:defaults", efiPartUUID, efiRoot),
		fmt.Sprintf("systemd.mount-extra=PARTUUID=%s:%s:auto:defaults", bootPartUUID, bootRoot),
	}, nil
}

func (g *GrubBootloader) Install() error {
	im := g.im

	info, err := im.prepareBootloaderInstall()
	if err != nil {
		return err
	}

	im.Print("Installing bootloader ...\n")

	osName, err := im.OsName()
	if err != nil {
		return fmt.Errorf("failed to determine OS name: %w", err)
	}

	err = im.chroot(
		info.chrootEnv,
		"/usr/bin/grub-install",
		[]string{
			"--target=x86_64-efi",
			"--directory=/usr/lib/grub/x86_64-efi",
			"--efi-directory=" + info.efiRoot,
			"--boot-directory=" + info.bootRoot,
			"--themes=" + osName + "-theme",
			"--removable",
			"--modules=ext2 btrfs gzio part_gpt fat part_msdos all_video",
			im.devicePath,
		},
	)
	if err != nil {
		return fmt.Errorf("grub-install failed: %w", err)
	}

	// Verify BOOTX64.EFI was created.
	if _, err := verifyEfiExecutable(info.efibootDir, info.efiExe, "grub-install"); err != nil {
		return err
	}

	// Replace unsigned GRUBX64.EFI with the signed one.
	grubx64efi := filepath.Join(info.efibootDir, "GRUBX64.EFI")
	im.Print("Removing existing %s as it's not signed ...\n", grubx64efi)
	os.Remove(grubx64efi)

	signedGrubx64efi := filepath.Join(im.rootfs, "usr", "lib", "grub", "grub-x86_64.efi.signed")
	im.Print("Moving %s to %s\n", signedGrubx64efi, grubx64efi)
	if err := filesystems.Move(signedGrubx64efi, grubx64efi); err != nil {
		return fmt.Errorf("failed to move signed grub binary: %w", err)
	}

	// Copy the shim binaries onto the EFI BOOT dir. shim is grub-specific:
	// it overrides BOOTX64.EFI so that, on SecureBoot, the firmware launches
	// shim which in turn chain-loads grubx64.efi. Other bootloaders (e.g.
	// systemd-boot) must NOT have shim copied over their own BOOTX64.EFI.
	shimDir := filepath.Join(im.rootfs, "usr", "share", "shim")
	im.Print("Copying shim for Secureboot from %s to %s ...\n", shimDir, info.efibootDir)
	if err := filesystems.CopyDirPreserve(shimDir, info.efibootDir); err != nil {
		return fmt.Errorf("failed to copy shim binaries from %s: %w", shimDir, err)
	}

	return nil
}

// ConfigureMemtest is a no-op for GRUB. The memtest entry is generated by
// grub-mkconfig from /etc/grub.d/ scripts shipped with the memtest86+ package.
func (g *GrubBootloader) ConfigureMemtest() error {
	return nil
}

func (g *GrubBootloader) ConfigureVmtest() error {
	im := g.im

	if err := im.validateImageModeForCreation(); err != nil {
		return err
	}

	if im.bootfsMount == "" {
		return errors.New("missing bootfsMount, call MountBootfs first")
	}

	im.Print("Setting up vmtest grub config based on the ostree boot config in %s ...\n", im.bootfsMount)

	ostreeBootCfg := filepath.Join(im.bootfsMount, "loader", "entries", "ostree-1.conf")
	if !filesystems.FileExists(ostreeBootCfg) {
		return fmt.Errorf("%s does not exist, cannot set up vmtest config", ostreeBootCfg)
	}

	vmtestCfgDir := filepath.Join(im.bootfsMount, ".imager.vmtest", "entries")
	if err := os.MkdirAll(vmtestCfgDir, 0755); err != nil {
		return fmt.Errorf("failed to create vmtest config dir: %w", err)
	}

	vmtestBootCfg := filepath.Join(vmtestCfgDir, "ostree-1.conf")

	consoleParams := "console=ttyS0,115200"
	systemdParams := "systemd.log_color=0"
	envParams := "systemd.setenv=SYSTEMD_COLORS=0 systemd.setenv=SYSTEMD_URLIFY=0"
	bootParams := consoleParams + " " + systemdParams + " " + envParams

	if err := filesystems.CopyFile(ostreeBootCfg, vmtestBootCfg); err != nil {
		return fmt.Errorf("failed to copy vmtest config: %w", err)
	}

	data, err := os.ReadFile(vmtestBootCfg)
	if err != nil {
		return fmt.Errorf("failed to read vmtest config: %w", err)
	}

	content := string(data)
	content = strings.ReplaceAll(content, "splash", "")
	content = strings.ReplaceAll(content, "quiet", bootParams)

	if err := os.WriteFile(vmtestBootCfg, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write vmtest config: %w", err)
	}

	im.Print("Set up vmtest grub config at %s\n", vmtestBootCfg)
	im.Print("Current vmtest grub config:\n")
	im.Print("%s\n", content)

	return nil
}
