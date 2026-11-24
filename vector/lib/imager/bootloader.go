package imager

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/runner"
)

// Bootloader defines the interface for bootloader operations.
// Each supported bootloader (e.g. GRUB, systemd-boot) implements this interface.
type Bootloader interface {
	// Configure sets up the bootloader configuration files
	// (e.g., config files, themes, environment variables).
	Configure() error

	// BootArgs generates the kernel boot arguments needed for the image based on
	// the imager configuration and image content. These boot args can be bootloader
	// specific.
	BootArgs() ([]string, error)

	// Install installs the bootloader binaries into the image.
	Install() error

	// ConfigureMemtest sets up the memtest86+ EFI binary in the image
	// if available on the host.
	ConfigureMemtest() error

	// ConfigureVmtest sets up VM test boot configuration.
	ConfigureVmtest() error
}

// SetupBootloaderConfig delegates to the configured Bootloader.Configure().
func (im *Imager) SetupBootloaderConfig() error {
	return im.bootloader.Configure()
}

// InstallBootloader delegates to the configured Bootloader.Install().
func (im *Imager) InstallBootloader() error {
	return im.bootloader.Install()
}

// GenerateBootArgs delegates to the configured Bootloader.BootArgs().
func (im *Imager) GenerateBootArgs() ([]string, error) {
	return im.bootloader.BootArgs()
}

// SetupVmtestConfig delegates to the configured Bootloader.ConfigureVmtest().
func (im *Imager) SetupVmtestConfig() error {
	return im.bootloader.ConfigureVmtest()
}

type bootloaderConfigInfo struct {
	efibootDir     string
	efiDeviceUUID  string
	bootDeviceUUID string
}

// prepareBootloaderConfig validates the preconditions shared by bootloader
// Configure() implementations, resolves partition UUIDs and the ostree boot
// commit, and ensures the EFI boot directory exists.
func (im *Imager) prepareBootloaderConfig() (*bootloaderConfigInfo, error) {
	if im.rootfs == "" {
		return nil, errors.New("rootfs not set, call SetRootfs first")
	}
	if im.efiDevice == "" {
		return nil, errors.New("missing efiDevice, not set in NewImagerOptions")
	}
	if im.bootDevice == "" {
		return nil, errors.New("missing bootDevice, not set in NewImagerOptions")
	}
	if im.efifsMount == "" {
		return nil, errors.New("missing efifsMount, call MountEfifs first")
	}
	if im.bootfsMount == "" {
		return nil, errors.New("missing bootfsMount, call MountBootfs first")
	}
	if im.rootfsMount == "" {
		return nil, errors.New("missing rootfsMount, call MountRootfs first")
	}

	efibootDir, err := im.EfiBootDir()
	if err != nil {
		return nil, fmt.Errorf("failed to determine EFI boot directory: %w", err)
	}

	efiDeviceUUID, err := filesystems.DeviceUUID(im.efiDevice)
	if err != nil {
		return nil, fmt.Errorf("unable to get UUID for %s: %w", im.efiDevice, err)
	}
	bootDeviceUUID, err := filesystems.DeviceUUID(im.bootDevice)
	if err != nil {
		return nil, fmt.Errorf("unable to get UUID for %s: %w", im.bootDevice, err)
	}

	// Verify kernel exists.
	if _, err := im.GetKernelPath(); err != nil {
		return nil, fmt.Errorf("failed to determine kernel version: %w", err)
	}

	// Get the boot commit.
	bootCommit, err := im.ostree.BootCommit(im.rootfsMount)
	if err != nil || bootCommit == "" {
		return nil, fmt.Errorf("cannot determine ostree boot commit: %w", err)
	}
	im.Print("Found boot commit: %s\n", bootCommit)

	if err := os.MkdirAll(efibootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create efibootDir %s: %w", efibootDir, err)
	}

	return &bootloaderConfigInfo{
		efibootDir:     efibootDir,
		efiDeviceUUID:  efiDeviceUUID,
		bootDeviceUUID: bootDeviceUUID,
	}, nil
}

// writeBootloaderEnvFile creates an environment.d drop-in file inside rootfs.
func (im *Imager) writeBootloaderEnvFile(filename, content string) error {
	envDir := filepath.Join(im.rootfs, "etc", "environment.d")
	if err := os.MkdirAll(envDir, 0755); err != nil {
		return fmt.Errorf("failed to create environment.d dir: %w", err)
	}
	envPath := filepath.Join(envDir, filename)
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", filename, err)
	}
	return nil
}

type bootloaderInstallInfo struct {
	efibootDir string
	efiRoot    string
	bootRoot   string
	efiExe     string
	chrootEnv  []string
}

// prepareBootloaderInstall validates the preconditions shared by bootloader
// Install() implementations and builds the chroot mount environment.
func (im *Imager) prepareBootloaderInstall() (*bootloaderInstallInfo, error) {
	if im.rootfs == "" {
		return nil, errors.New("rootfs not set, call SetRootfs first")
	}
	if im.efifsMount == "" {
		return nil, errors.New("missing efifsMount, call MountEfifs first")
	}
	if im.bootfsMount == "" {
		return nil, errors.New("missing bootfsMount, call MountBootfs first")
	}
	if im.devicePath == "" {
		return nil, errors.New("missing devicePath, not set in NewImagerOptions")
	}

	efibootDir, err := im.EfiBootDir()
	if err != nil {
		return nil, fmt.Errorf("failed to determine EFI boot directory: %w", err)
	}
	efiRoot, err := im.EfiRoot()
	if err != nil {
		return nil, fmt.Errorf("failed to determine EFI root: %w", err)
	}
	bootRoot, err := im.BootRoot()
	if err != nil {
		return nil, fmt.Errorf("failed to determine boot root: %w", err)
	}
	efiExe, err := im.EfiExecutable()
	if err != nil {
		return nil, fmt.Errorf("failed to determine EFI executable: %w", err)
	}

	return &bootloaderInstallInfo{
		efibootDir: efibootDir,
		efiRoot:    efiRoot,
		bootRoot:   bootRoot,
		efiExe:     efiExe,
		chrootEnv: []string{
			"IMAGER_EFI_MOUNT=" + im.efifsMount,
			"IMAGER_BOOT_MOUNT=" + im.bootfsMount,
			"IMAGER_EFI_ROOT=" + efiRoot,
			"IMAGER_BOOT_ROOT=" + bootRoot,
		},
	}, nil
}

// verifyEfiExecutable confirms that the bootloader installer created the
// expected EFI fallback executable inside efibootDir.
func verifyEfiExecutable(efibootDir, efiExe, toolName string) (string, error) {
	bootx64efi := filepath.Join(efibootDir, efiExe)
	if !filesystems.PathExists(bootx64efi) {
		return "", fmt.Errorf("%s does not exist after %s", bootx64efi, toolName)
	}
	return bootx64efi, nil
}

func (im *Imager) InstallSecurebootCerts() error {
	if im.rootfs == "" {
		return errors.New("rootfs not set, call SetRootfs first")
	}
	if im.efifsMount == "" {
		return errors.New("missing efifsMount, call MountEfifs first")
	}

	certFileName, err := im.EfiCertificateFileName()
	if err != nil {
		return err
	}
	certDerFileName, err := im.EfiCertificateFileNameDer()
	if err != nil {
		return err
	}
	kekFileName, err := im.EfiCertificateFileNameKek()
	if err != nil {
		return err
	}
	kekDerFileName, err := im.EfiCertificateFileNameKekDer()
	if err != nil {
		return err
	}

	certs := []struct {
		srcName  string
		dstName  string
		derName  string
		copyMsg  string
		derMsg   string
		errLabel string
	}{
		{
			srcName:  "secureboot.pem",
			dstName:  certFileName,
			derName:  certDerFileName,
			copyMsg:  "Copying SecureBoot cert to EFI partition ...\n",
			derMsg:   "Generating SecureBoot MOK ...\n",
			errLabel: "SecureBoot cert",
		},
		{
			srcName:  "secureboot-kek.pem",
			dstName:  kekFileName,
			derName:  kekDerFileName,
			copyMsg:  "Copying SecureBoot KEK cert to EFI partition ...\n",
			derMsg:   "Generating SecureBoot KEK DER for convenience ...\n",
			errLabel: "SecureBoot KEK cert",
		},
	}

	for _, c := range certs {
		src := filepath.Join(im.rootfs, "etc", "portage", c.srcName)
		if !filesystems.FileExists(src) {
			im.PrintWarning("NO SECUREBOOT CERT AT: %s -- ignoring.\n", src)
			continue
		}
		im.Print("%s", c.copyMsg)
		if err := filesystems.CopyFile(src, filepath.Join(im.efifsMount, c.dstName)); err != nil {
			return fmt.Errorf("failed to copy %s: %w", c.errLabel, err)
		}
		im.Print("%s", c.derMsg)
		if err := im.runner(&runner.Cmd{
			Name: "openssl",
			Args: []string{
				"x509",
				"-in", src,
				"-outform", "DER",
				"-out", filepath.Join(im.efifsMount, c.derName),
			},
			Stdout: im.Stdout(),
			Stderr: im.Stderr(),
		}); err != nil {
			return fmt.Errorf("openssl DER conversion failed for %s: %w", c.errLabel, err)
		}
	}

	return nil
}

func (im *Imager) InstallMemtest() error {
	if im.rootfs == "" {
		return errors.New("rootfs not set, call SetRootfs first")
	}
	efibootDir, err := im.EfiBootDir()
	if err != nil {
		return err
	}

	memtestBin := filepath.Join(im.rootfs, "usr", "share", "memtest86+", "memtest.efi64")
	if !filesystems.PathExists(memtestBin) {
		im.PrintWarning("WARNING: %s not available, please install memtest86+\n", memtestBin)
		return nil
	}
	if err := filesystems.CopyFile(memtestBin, filepath.Join(efibootDir, "memtest86plus.efi")); err != nil {
		return fmt.Errorf("failed to copy memtest86+ EFI binary: %w", err)
	}

	return im.bootloader.ConfigureMemtest()
}

func (im *Imager) GenerateKernelBootArgs() ([]string, error) {
	ref, err := im.cleanAndStripRef()
	if err != nil {
		return nil, fmt.Errorf("failed to clean ref: %w", err)
	}
	if im.efiDevice == "" {
		return nil, errors.New("missing efiDevice, not set in NewImagerOptions")
	}
	if im.bootDevice == "" {
		return nil, errors.New("missing bootDevice, not set in NewImagerOptions")
	}
	if im.rootDevice == "" {
		return nil, errors.New("missing rootDevice, not set in NewImagerOptions")
	}

	// if we are encrypting, use the realRootDevice
	rootDevice := im.rootDevice
	if im.encrypted {
		if im.realRootDevice == "" {
			return nil, errors.New("missing realRootDevice for encrypted image")
		}
		rootDevice = im.realRootDevice
	}

	bootArgs := im.RootfsKernelArgs()

	bootloaderBootArgs, err := im.GenerateBootArgs()
	if err != nil {
		return nil, fmt.Errorf("failed to generate bootloader boot args: %w", err)
	}
	bootArgs = append(bootArgs, bootloaderBootArgs...)

	// Root device UUID for LUKS.
	rootDeviceUUID, err := filesystems.DeviceUUID(rootDevice)
	if err != nil {
		return nil, fmt.Errorf("unable to get device UUID for %s: %w", rootDevice, err)
	}
	if im.encrypted {
		bootArgs = append(bootArgs, fmt.Sprintf("rd.luks.uuid=%s", rootDeviceUUID))
	}

	// Read additional kernel cmdline params from the image boot directory.
	devDir, err := im.DevDir()
	if err != nil {
		return nil, err
	}
	cmdlineFile := filepath.Join(devDir, "image", "boot", ref, "cmdline.conf")
	if filesystems.FileExists(cmdlineFile) {
		im.Print("Reading additional kernel args from %s ...\n", cmdlineFile)
		data, err := os.ReadFile(cmdlineFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read cmdline file: %w", err)
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			bootArgs = append(bootArgs, line)
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("failed to scan cmdline file: %w", err)
		}
	} else {
		im.PrintWarning("WARNING: no additional kernel cmdline params available, %s does not exist.\n", cmdlineFile)
	}

	return bootArgs, nil
}
