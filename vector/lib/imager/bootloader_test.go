package imager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
)

// --- Bootloader interface compliance ---

func TestGrubBootloaderImplementsBootloader(t *testing.T) {
	var _ Bootloader = (*GrubBootloader)(nil)
}

func TestSystemdBootBootloaderImplementsBootloader(t *testing.T) {
	var _ Bootloader = (*SystemdBootBootloader)(nil)
}

func TestMockBootloaderImplementsBootloader(t *testing.T) {
	var _ Bootloader = (*MockBootloader)(nil)
}

func TestGetBootloader(t *testing.T) {
	t.Run("DefaultsToGrub", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		bl := im.GetBootloader()
		if bl == nil {
			t.Fatal("GetBootloader() returned nil")
		}
		if _, ok := bl.(*GrubBootloader); !ok {
			t.Fatalf("expected *GrubBootloader, got %T", bl)
		}
	})

	t.Run("SystemdBoot", func(t *testing.T) {
		cfg := baseImageConfig()
		cfg.Items["Imager.Bootloader"] = []string{"systemd-boot"}
		im := newTestImager(cfg, &ostree.MockOstree{})
		bl := im.GetBootloader()
		if bl == nil {
			t.Fatal("GetBootloader() returned nil")
		}
		if _, ok := bl.(*SystemdBootBootloader); !ok {
			t.Fatalf("expected *SystemdBootBootloader, got %T", bl)
		}
	})

	t.Run("UnknownBootloaderErrors", func(t *testing.T) {
		cfg := baseImageConfig()
		cfg.Items["Imager.Bootloader"] = []string{"lilo"}
		_, err := NewImager(cfg, &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), nil)
		if err == nil {
			t.Fatal("expected error for unknown bootloader")
		}
		if !strings.Contains(err.Error(), "unknown bootloader") {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

// --- InstallBootloader Tests ---

func TestInstallBootloader(t *testing.T) {
	t.Run("EmptyOstreeDeployRootfs", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.devicePath = "/dev/sda"
		im.efifsMount = "/mnt/efi"
		im.bootfsMount = "/mnt/boot"
		err := im.InstallBootloader()
		if err == nil {
			t.Fatal("expected error for empty ostreeDeployRootfs")
		}
		if !strings.Contains(err.Error(), "rootfs not set") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("EmptyEfifsMount", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.devicePath = "/dev/sda"
		im.SetRootfs("/rootfs")
		im.bootfsMount = "/mnt/boot"
		err := im.InstallBootloader()
		if err == nil {
			t.Fatal("expected error for empty efifsMount")
		}
		if !strings.Contains(err.Error(), "efifsMount") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("EmptyBootfsMount", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.devicePath = "/dev/sda"
		im.SetRootfs("/rootfs")
		im.efifsMount = "/mnt/efi"
		err := im.InstallBootloader()
		if err == nil {
			t.Fatal("expected error for empty bootfsMount")
		}
		if !strings.Contains(err.Error(), "bootfsMount") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("EmptyDevicePath", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.SetRootfs("/rootfs")
		im.efifsMount = "/mnt/efi"
		im.bootfsMount = "/mnt/boot"
		err := im.InstallBootloader()
		if err == nil {
			t.Fatal("expected error for empty devicePath")
		}
		if !strings.Contains(err.Error(), "devicePath") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("ConfigErrors", func(t *testing.T) {
		// Missing EfiRoot config
		cfg := baseImageConfig()
		delete(cfg.Items, "Imager.EfiRoot")
		im := newTestImager(cfg, &ostree.MockOstree{})
		im.devicePath = "/dev/sda"
		im.SetRootfs("/rootfs")
		im.efifsMount = "/mnt/efi"
		im.bootfsMount = "/mnt/boot"
		err := im.InstallBootloader()
		if err == nil {
			t.Fatal("expected error for missing EfiRoot config")
		}

		// Missing BootRoot config
		cfg2 := baseImageConfig()
		delete(cfg2.Items, "Imager.BootRoot")
		im2 := newTestImager(cfg2, &ostree.MockOstree{})
		im2.devicePath = "/dev/sda"
		im2.SetRootfs("/rootfs")
		im2.efifsMount = "/mnt/efi"
		im2.bootfsMount = "/mnt/boot"
		err = im2.InstallBootloader()
		if err == nil {
			t.Fatal("expected error for missing BootRoot config")
		}

		// Missing OsName config
		cfg3 := baseImageConfig()
		delete(cfg3.Items, "matrixOS.OsName")
		im3 := newTestImager(cfg3, &ostree.MockOstree{})
		im3.devicePath = "/dev/sda"
		im3.SetRootfs("/rootfs")
		im3.efifsMount = "/mnt/efi"
		im3.bootfsMount = "/mnt/boot"
		err = im3.InstallBootloader()
		if err == nil {
			t.Fatal("expected error for missing OsName config")
		}

		// Missing EfiExecutable config
		cfg4 := baseImageConfig()
		delete(cfg4.Items, "Imager.EfiExecutable")
		im4 := newTestImager(cfg4, &ostree.MockOstree{})
		im4.devicePath = "/dev/sda"
		im4.SetRootfs("/rootfs")
		im4.efifsMount = "/mnt/efi"
		im4.bootfsMount = "/mnt/boot"
		err = im4.InstallBootloader()
		if err == nil {
			t.Fatal("expected error for missing EfiExecutable config")
		}
	})
}

// --- SetupBootloaderConfig Tests ---

func TestSetupBootloaderConfig(t *testing.T) {
	t.Run("EmptyRef", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.SetRootfs("/rootfs")
		im.rootfsMount = "/sysroot"
		im.bootfsMount = "/boot"
		im.efifsMount = "/efi"
		err := im.SetupBootloaderConfig()
		if err == nil {
			t.Error("should error for empty ref")
		}
	})

	t.Run("OstreeError", func(t *testing.T) {
		mo := &ostree.MockOstree{RemoveFullErr: errors.New("ostree error")}
		im := newTestImager(baseImageConfig(), mo)
		im.SetRootfs("/rootfs")
		im.ref = "ref"
		im.rootfsMount = "/sysroot"
		im.bootfsMount = "/boot"
		im.efifsMount = "/efi"
		err := im.SetupBootloaderConfig()
		if err == nil {
			t.Error("should propagate ostree error")
		}
	})

	t.Run("EmptyOtherParams", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.ref = "ref"
		im.rootfsMount = "/sysroot"
		im.bootfsMount = "/boot"
		im.efifsMount = "/efi"
		if err := im.SetupBootloaderConfig(); err == nil {
			t.Error("should error for empty rootfs")
		}
		im.SetRootfs("/rootfs")
		im.rootfsMount = ""
		if err := im.SetupBootloaderConfig(); err == nil {
			t.Error("should error for empty rootfsMount")
		}
		im.rootfsMount = "/sys"
		im.bootfsMount = ""
		if err := im.SetupBootloaderConfig(); err == nil {
			t.Error("should error for empty bootfsMount")
		}
		im.bootfsMount = "/boot"
		im.efifsMount = ""
		if err := im.SetupBootloaderConfig(); err == nil {
			t.Error("should error for empty efifsMount")
		}
		im.efifsMount = "/efi"
		if err := im.SetupBootloaderConfig(); err == nil {
			t.Error("should error for empty efiDevice")
		}
		im.efiDevice = "/dev/sda1"
		if err := im.SetupBootloaderConfig(); err == nil {
			t.Error("should error for empty bootDevice")
		}
	})
}

// --- SetupVmtestConfig Tests ---

func TestSetupVmtestConfig(t *testing.T) {
	t.Run("EmptyParam", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		err := im.SetupVmtestConfig()
		if err == nil {
			t.Error("should error for empty bootfsMount")
		}
	})

	t.Run("NoLoaderConf", func(t *testing.T) {
		tmpDir := t.TempDir()
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.bootfsMount = tmpDir
		err := im.SetupVmtestConfig()
		if err == nil {
			t.Error("should error when ostree boot config doesn't exist")
		}
	})

	t.Run("Success", func(t *testing.T) {
		tmpDir := t.TempDir()
		loaderDir := filepath.Join(tmpDir, "loader", "entries")
		os.MkdirAll(loaderDir, 0755)
		confContent := "title matrixos\noptions root=UUID=xxx quiet splash rw\n"
		os.WriteFile(filepath.Join(loaderDir, "ostree-1.conf"), []byte(confContent), 0644)

		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.SetImagePath(filepath.Join(tmpDir, "test.img"))
		im.SetImageMode(ModeCreateImageFile)
		im.bootfsMount = tmpDir
		err := im.SetupVmtestConfig()
		if err != nil {
			t.Fatalf("error: %v", err)
		}

		vmtestCfg := filepath.Join(tmpDir, ".imager.vmtest", "entries", "ostree-1.conf")
		data, err := os.ReadFile(vmtestCfg)
		if err != nil {
			t.Fatalf("failed to read vmtest config: %v", err)
		}
		content := string(data)
		if strings.Contains(content, "splash") {
			t.Error("vmtest config should not contain 'splash'")
		}
		if !strings.Contains(content, "console=ttyS0,115200") {
			t.Error("vmtest config should contain console params")
		}
		if !strings.Contains(content, "systemd.log_color=0") {
			t.Error("vmtest config should contain systemd params")
		}
	})
}

// --- InstallSecurebootCerts Tests ---

func TestInstallSecurebootCerts(t *testing.T) {
	t.Run("EmptyParams", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		if err := im.InstallSecurebootCerts(); err == nil {
			t.Error("should error for empty rootfs")
		}
		im.SetRootfs("/rootfs")
		if err := im.InstallSecurebootCerts(); err == nil {
			t.Error("should error for empty efifsMount")
		}
	})

	t.Run("ConfigError", func(t *testing.T) {
		ec := &config.ErrConfig{Err: errors.New("cfg error")}
		im, _ := NewImager(ec, &ostree.MockOstree{}, filesystems.DefaultMockFsenc(), nil)
		im.SetRootfs("/rootfs")
		im.efifsMount = "/efi"
		err := im.InstallSecurebootCerts()
		if err == nil {
			t.Error("should error from broken config")
		}
	})
}

// --- InstallMemtest Tests ---

func TestInstallMemtest(t *testing.T) {
	t.Run("EmptyParams", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		if err := im.InstallMemtest(); err == nil {
			t.Error("should error for empty rootfs")
		}
	})

	t.Run("NoMemtest", func(t *testing.T) {
		tmpDir := t.TempDir()
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.SetRootfs(tmpDir)
		im.efifsMount = filepath.Join(tmpDir, "efimount")
		os.MkdirAll(filepath.Join(im.efifsMount, "EFI/BOOT"), 0755)
		err := im.InstallMemtest()
		if err != nil {
			t.Fatalf("should not error when memtest not found: %v", err)
		}
	})

	t.Run("Success", func(t *testing.T) {
		tmpDir := t.TempDir()
		memtestDir := filepath.Join(tmpDir, "usr", "share", "memtest86+")
		os.MkdirAll(memtestDir, 0755)
		os.WriteFile(filepath.Join(memtestDir, "memtest.efi64"), []byte("EFI"), 0644)
		efiMount := filepath.Join(tmpDir, "efimount")
		efibootdir := filepath.Join(efiMount, "EFI/BOOT")
		os.MkdirAll(efibootdir, 0755)

		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.SetRootfs(tmpDir)
		im.efifsMount = efiMount
		err := im.InstallMemtest()
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		copied := filepath.Join(efibootdir, "memtest86plus.efi")
		if _, err := os.Stat(copied); os.IsNotExist(err) {
			t.Error("memtest86plus.efi should have been copied")
		}
	})

	t.Run("DelegatesToBootloader", func(t *testing.T) {
		tmpDir := t.TempDir()
		memtestDir := filepath.Join(tmpDir, "usr", "share", "memtest86+")
		os.MkdirAll(memtestDir, 0755)
		hostMemtest := filepath.Join(memtestDir, "memtest.efi64")
		os.WriteFile(hostMemtest, []byte("EFI"), 0644)
		efiMount := filepath.Join(tmpDir, "efimount")
		os.MkdirAll(filepath.Join(efiMount, "EFI/BOOT"), 0755)

		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.SetRootfs(tmpDir)
		im.efifsMount = efiMount
		mock := &MockBootloader{}
		im.bootloader = mock

		if err := im.InstallMemtest(); err != nil {
			t.Fatalf("error: %v", err)
		}
		if !mock.ConfigureMemtestCalled {
			t.Error("ConfigureMemtest should have been called on the bootloader")
		}
		if mock.ConfigureMemtestBin != hostMemtest {
			t.Errorf("ConfigureMemtest bin = %q, want %q", mock.ConfigureMemtestBin, hostMemtest)
		}
	})

	t.Run("SkippedWhenMemtestMissing", func(t *testing.T) {
		tmpDir := t.TempDir()
		efiMount := filepath.Join(tmpDir, "efimount")
		os.MkdirAll(filepath.Join(efiMount, "EFI/BOOT"), 0755)

		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		im.SetRootfs(tmpDir)
		im.efifsMount = efiMount
		mock := &MockBootloader{}
		im.bootloader = mock

		if err := im.InstallMemtest(); err != nil {
			t.Fatalf("error: %v", err)
		}
		if mock.ConfigureMemtestCalled {
			t.Error("ConfigureMemtest must not be called when memtest is missing")
		}
	})
}

// --- ConfigureMemtest Tests ---

func TestGrubConfigureMemtestIsNoop(t *testing.T) {
	im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
	g := NewGrubBootloader(im)
	if err := g.ConfigureMemtest("/anywhere/memtest.efi64"); err != nil {
		t.Errorf("GRUB ConfigureMemtest should be a no-op, got: %v", err)
	}
}

func TestSystemdBootConfigureMemtest(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		tmpDir := t.TempDir()
		efiMount := filepath.Join(tmpDir, "efimount")
		if err := os.MkdirAll(efiMount, 0755); err != nil {
			t.Fatal(err)
		}

		cfg := baseImageConfig()
		cfg.Items["Imager.Bootloader"] = []string{"systemd-boot"}
		im := newTestImager(cfg, &ostree.MockOstree{})
		im.efifsMount = efiMount
		s := NewSystemdBootBootloader(im)

		if err := s.ConfigureMemtest("/rootfs/usr/share/memtest86+/memtest.efi64"); err != nil {
			t.Fatalf("ConfigureMemtest: %v", err)
		}

		entryPath := filepath.Join(efiMount, "loader/entries/memtest.conf")
		data, err := os.ReadFile(entryPath)
		if err != nil {
			t.Fatalf("loader entry not written: %v", err)
		}
		got := string(data)
		if !strings.Contains(got, "title    Memtest86+") {
			t.Errorf("missing title line, got:\n%s", got)
		}
		if !strings.Contains(got, "efi      /EFI/BOOT/memtest86plus.efi") {
			t.Errorf("missing or wrong efi line, got:\n%s", got)
		}
	})

	t.Run("MissingEfifsMount", func(t *testing.T) {
		cfg := baseImageConfig()
		cfg.Items["Imager.Bootloader"] = []string{"systemd-boot"}
		im := newTestImager(cfg, &ostree.MockOstree{})
		s := NewSystemdBootBootloader(im)
		if err := s.ConfigureMemtest("/x"); err == nil {
			t.Error("should error when efifsMount is unset")
		}
	})

	t.Run("MissingRelativeEfiBootPath", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfg := baseImageConfig()
		cfg.Items["Imager.Bootloader"] = []string{"systemd-boot"}
		delete(cfg.Items, "Imager.RelativeEfiBootPath")
		im := newTestImager(cfg, &ostree.MockOstree{})
		im.efifsMount = tmpDir
		s := NewSystemdBootBootloader(im)
		if err := s.ConfigureMemtest("/x"); err == nil {
			t.Error("should error when RelativeEfiBootPath is unset")
		}
	})
}

// --- GenerateKernelBootArgs Tests ---

func TestGenerateKernelBootArgs(t *testing.T) {
	t.Run("EmptyRef", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
		// ref is empty
		_, err := im.GenerateKernelBootArgs()
		if err == nil {
			t.Error("should error for empty ref")
		}
	})

	t.Run("OstreeRefError", func(t *testing.T) {
		mo := &ostree.MockOstree{RemoveFullErr: errors.New("ostree error")}
		im := newTestImager(baseImageConfig(), mo)
		im.ref = "ref"
		_, err := im.GenerateKernelBootArgs()
		if err == nil {
			t.Error("should propagate ostree error")
		}
	})

	t.Run("EmptyEfiDevice", func(t *testing.T) {
		mo := &ostree.MockOstree{Ref_: "matrixos/amd64/gnome"}
		im := newTestImager(baseImageConfig(), mo)
		im.ref = "matrixos/amd64/gnome"
		// efiDevice empty
		_, err := im.GenerateKernelBootArgs()
		if err == nil {
			t.Error("should error for empty efiDevice")
		}
		if !strings.Contains(err.Error(), "efiDevice") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("EmptyBootDevice", func(t *testing.T) {
		mo := &ostree.MockOstree{Ref_: "matrixos/amd64/gnome"}
		im := newTestImager(baseImageConfig(), mo)
		im.ref = "matrixos/amd64/gnome"
		im.efiDevice = "/dev/sda1"
		// bootDevice empty
		_, err := im.GenerateKernelBootArgs()
		if err == nil {
			t.Error("should error for empty bootDevice")
		}
		if !strings.Contains(err.Error(), "bootDevice") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("EmptyRootDevice", func(t *testing.T) {
		mo := &ostree.MockOstree{Ref_: "matrixos/amd64/gnome"}
		im := newTestImager(baseImageConfig(), mo)
		im.ref = "matrixos/amd64/gnome"
		im.efiDevice = "/dev/sda1"
		im.bootDevice = "/dev/sda2"
		// rootDevice empty
		_, err := im.GenerateKernelBootArgs()
		if err == nil {
			t.Error("should error for empty rootDevice")
		}
		if !strings.Contains(err.Error(), "rootDevice") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("EncryptedMissingRealRootDevice", func(t *testing.T) {
		mo := &ostree.MockOstree{Ref_: "matrixos/amd64/gnome"}
		fsenc := &filesystems.MockFsenc{EncryptionEnabled_: true}
		opts := &NewImagerOptions{
			EfiDevice:  "/dev/sda1",
			BootDevice: "/dev/sda2",
			RootDevice: "/dev/sda3",
		}
		im, err := NewImager(baseImageConfig(), mo, fsenc, opts)
		if err != nil {
			t.Fatal(err)
		}
		im.ref = "matrixos/amd64/gnome"
		// realRootDevice not set => error for encrypted.

		_, err = im.GenerateKernelBootArgs()
		if err == nil {
			t.Error("should error for missing realRootDevice on encrypted image")
		}
		if !strings.Contains(err.Error(), "realRootDevice") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("ConfigError", func(t *testing.T) {
		ec := &config.ErrConfig{Err: errors.New("cfg error")}
		mo := &ostree.MockOstree{Ref_: "matrixos/amd64/gnome"}
		im, _ := NewImager(ec, mo, filesystems.DefaultMockFsenc(), nil)
		im.ref = "matrixos/amd64/gnome"
		im.efiDevice = "/dev/sda1"
		im.bootDevice = "/dev/sda2"
		im.rootDevice = "/dev/sda3"

		// DeviceUUID will fail for non-real device, but config error
		// from EfiRoot should come first.
		_, err := im.GenerateKernelBootArgs()
		if err == nil {
			t.Error("should error from broken config")
		}
	})
}

// --- SetupBootloaderConfig additional tests ---

func TestSetupBootloaderConfigAdditional(t *testing.T) {
	t.Run("EmptyEfifsMount", func(t *testing.T) {
		im := newTestImager(baseImageConfig(), &ostree.MockOstree{Ref_: "matrixos/amd64/gnome"})
		im.ref = "matrixos/amd64/gnome"
		im.SetRootfs("/rootfs")
		im.efiDevice = "/dev/sda1"
		im.bootDevice = "/dev/sda2"
		im.rootfsMount = "/sysroot"
		im.bootfsMount = "/boot"
		// efifsMount empty
		err := im.SetupBootloaderConfig()
		if err == nil {
			t.Error("should error for empty efifsMount")
		}
	})

	t.Run("ConfigEfiRootError", func(t *testing.T) {
		cfg := baseImageConfig()
		delete(cfg.Items, "Imager.RelativeEfiBootPath")
		im := newTestImager(cfg, &ostree.MockOstree{Ref_: "matrixos/amd64/gnome"})
		im.ref = "matrixos/amd64/gnome"
		im.SetRootfs("/rootfs")
		im.efiDevice = "/dev/sda1"
		im.bootDevice = "/dev/sda2"
		im.rootfsMount = "/sysroot"
		im.bootfsMount = "/boot"
		im.efifsMount = "/efi"
		err := im.SetupBootloaderConfig()
		if err == nil {
			t.Error("should error for missing RelativeEfiBootPath config")
		}
	})
}

// --- InstallBootloader additional tests ---

func TestInstallBootloaderAdditional(t *testing.T) {
	t.Run("EfiBootDirError", func(t *testing.T) {
		cfg := baseImageConfig()
		delete(cfg.Items, "Imager.RelativeEfiBootPath")
		im := newTestImager(cfg, &ostree.MockOstree{})
		im.SetRootfs("/rootfs")
		im.efifsMount = "/efi"
		im.bootfsMount = "/boot"
		im.devicePath = "/dev/sda"
		err := im.InstallBootloader()
		if err == nil {
			t.Error("should error for EfiBootDir config error")
		}
	})
}

// --- InstallSecurebootCerts additional tests ---

func TestInstallSecurebootCertsAdditional(t *testing.T) {
	t.Run("NoCerts", func(t *testing.T) {
		tmpDir := t.TempDir()
		efiMount := filepath.Join(tmpDir, "efimount")
		efibootdir := filepath.Join(efiMount, "EFI/BOOT")
		os.MkdirAll(efibootdir, 0755)

		cfg := baseImageConfig()
		mockRunner := runner.NewMockRunner()
		im := newTestImagerWithRunner(cfg, &ostree.MockOstree{}, mockRunner)
		im.SetRootfs(tmpDir)
		im.efifsMount = efiMount
		// No certs exist in rootfs: function should print warnings and succeed.
		if err := im.InstallSecurebootCerts(); err != nil {
			t.Fatalf("expected nil error when no certs are present, got: %v", err)
		}
	})
}
