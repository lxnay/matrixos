package filesystems

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"matrixos/vector/lib/runner"
)

func TestMountpointToDevice(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{
			{Mountpoint: "/mnt", Source: "/dev/sda1", FSType: "ext4"},
		})

		device, err := MountpointToDevice("/mnt")
		if err != nil {
			t.Fatalf("MountpointToDevice failed: %v", err)
		}
		if device != "/dev/sda1" {
			t.Errorf("Expected device /dev/sda1, got %s", device)
		}
	})

	t.Run("NoMountpoint", func(t *testing.T) {
		_, err := MountpointToDevice("")
		if err == nil {
			t.Error("Expected error for missing mountpoint, got nil")
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{})
		_, err := MountpointToDevice("/mnt")
		if err == nil {
			t.Error("Expected error when mount not found")
		}
	})

	t.Run("MultipleOutputs", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{
			{Mountpoint: "/mnt", Source: "/dev/sda1", FSType: "ext4"},
			{Mountpoint: "/mnt", Source: "/dev/sda2", FSType: "ext4"},
		})
		device, err := MountpointToDevice("/mnt")
		if err != nil {
			t.Fatalf("MountpointToDevice failed: %v", err)
		}
		if device != "/dev/sda2" {
			t.Errorf("Expected most recent device /dev/sda2, got %s", device)
		}
	})
}

func TestMountpointToUUID(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		setupMockBlkid(t, map[string]string{
			"/dev/sda1:UUID": "abcd-1234-ef56-7890",
		})
		setupMockMountInfo(t, []*MountInfoEntry{
			{Mountpoint: "/mnt", Source: "/dev/sda1", FSType: "ext4"},
		})

		uuid, err := MountpointToUUID("/mnt")
		if err != nil {
			t.Fatalf("MountpointToUUID failed: %v", err)
		}
		if uuid != "abcd-1234-ef56-7890" {
			t.Errorf("Expected UUID abcd-1234-ef56-7890, got %s", uuid)
		}
	})

	t.Run("NoMountpoint", func(t *testing.T) {
		_, err := MountpointToUUID("")
		if err == nil {
			t.Error("Expected error for missing mountpoint, got nil")
		}
	})

	t.Run("MountNotFound", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{})
		_, err := MountpointToUUID("/mnt")
		if err == nil {
			t.Error("Expected error when mount not found")
		}
	})

	t.Run("NoUUID", func(t *testing.T) {
		setupMockBlkidFail(t)
		setupMockMountInfo(t, []*MountInfoEntry{
			{Mountpoint: "/mnt", Source: "tmpfs", FSType: "tmpfs"},
		})
		_, err := MountpointToUUID("/mnt")
		if err == nil {
			t.Error("Expected error for no UUID found, got nil")
		}
	})
}

func TestMountpointToFSType(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{
			{Mountpoint: "/mnt", Source: "/dev/sda1", FSType: "ext4"},
		})

		fstype, err := MountpointToFSType("/mnt")
		if err != nil {
			t.Fatalf("MountpointToFSType failed: %v", err)
		}
		if fstype != "ext4" {
			t.Errorf("Expected FSTYPE ext4, got %s", fstype)
		}
	})

	t.Run("SuccessVfat", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{
			{Mountpoint: "/boot/efi", Source: "/dev/sda2", FSType: "vfat"},
		})

		fstype, err := MountpointToFSType("/boot/efi")
		if err != nil {
			t.Fatalf("MountpointToFSType failed: %v", err)
		}
		if fstype != "vfat" {
			t.Errorf("Expected FSTYPE vfat, got %s", fstype)
		}
	})

	t.Run("NoMountpoint", func(t *testing.T) {
		_, err := MountpointToFSType("")
		if err == nil {
			t.Error("Expected error for missing mountpoint, got nil")
		}
	})

	t.Run("MountNotFound", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{})
		_, err := MountpointToFSType("/mnt")
		if err == nil {
			t.Error("Expected error when mount not found")
		}
	})
}

func TestCleanupMounts(t *testing.T) {
	setupMockExec(t)
	setupMockSyscalls(t)

	t.Run("SuccessfulUnmount", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{
			{Mountpoint: "/mnt/test", Source: "/dev/sda1"},
		})
		CleanupMounts(CleanupMountsOptions{
			Mounts: []string{"/mnt/test"},
		})
	})

	t.Run("MountNotExist", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{})
		CleanupMounts(CleanupMountsOptions{
			Mounts: []string{"/mnt/test"},
		})
	})

	t.Run("UnmountFail", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{
			{Mountpoint: "/mnt/fail", Source: "/dev/sda1"},
		})
		os.Setenv("MOCK_UMOUNT_FAIL", "1")
		defer os.Unsetenv("MOCK_UMOUNT_FAIL")
		// Should not panic or error out, just log
		CleanupMounts(CleanupMountsOptions{
			Mounts: []string{"/mnt/fail"},
		})
	})
}

func TestCheckActiveMounts(t *testing.T) {
	t.Run("NoActiveMounts", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{})
		if err := CheckActiveMounts("/mnt/test"); err != nil {
			t.Errorf("CheckActiveMounts failed: %v", err)
		}
	})

	t.Run("ActiveMountsDetected", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{
			{Mountpoint: "/mnt/test/proc", Source: "proc", FSType: "proc"},
		})
		if err := CheckActiveMounts("/mnt/test"); err == nil {
			t.Error("CheckActiveMounts should fail when mounts are detected")
		}
	})
}

func TestCleanupCryptsetupDevices(t *testing.T) {
	setupMockExec(t)

	t.Run("Success", func(t *testing.T) {
		tmpDir := t.TempDir()

		originalDevMapperPrefix := devMapperPrefix
		t.Cleanup(func() { devMapperPrefix = originalDevMapperPrefix })
		devMapperPrefix = tmpDir

		devPath := filepath.Join(tmpDir, "mycrypt")
		if _, err := os.Create(devPath); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(devPath)
		CleanupCryptsetupDevices([]string{"mycrypt"})
	})

	t.Run("CloseFail", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{})
		os.Setenv("MOCK_CRYPTSETUP_FAIL", "1")
		defer os.Unsetenv("MOCK_CRYPTSETUP_FAIL")
		tmpDir := t.TempDir()

		originalDevMapperPrefix := devMapperPrefix
		t.Cleanup(func() { devMapperPrefix = originalDevMapperPrefix })
		devMapperPrefix = tmpDir

		devPath := filepath.Join(tmpDir, "mycrypt")
		if _, err := os.Create(devPath); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(devPath)

		// Should not panic or error out, just log
		CleanupCryptsetupDevices([]string{"mycrypt"})
	})
}

func TestRemountReadWriteIntegration(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping integration test: requires root")
	}

	tmpDir := t.TempDir()

	// Mount a tmpfs.
	if err := unix.Mount("tmpfs", tmpDir, "tmpfs", 0, "size=1M"); err != nil {
		t.Fatalf("mount tmpfs: %v", err)
	}
	t.Cleanup(func() { unix.Unmount(tmpDir, 0) })

	// Remount read-only.
	if err := unix.Mount("", tmpDir, "", unix.MS_REMOUNT|unix.MS_RDONLY, ""); err != nil {
		t.Fatalf("remount ro: %v", err)
	}

	// Verify it's read-only.
	testFile := filepath.Join(tmpDir, "probe")
	if err := os.WriteFile(testFile, []byte("x"), 0644); err == nil {
		t.Fatal("expected write to fail on read-only mount")
	}

	// Use the real RemountReadWrite.
	if err := RemountReadWrite(tmpDir); err != nil {
		t.Fatalf("RemountReadWrite: %v", err)
	}

	// Verify it's writable.
	if err := os.WriteFile(testFile, []byte("x"), 0644); err != nil {
		t.Fatalf("write after RemountReadWrite failed: %v", err)
	}
}

func TestRemountReadWrite(t *testing.T) {
	setupMockExec(t)

	t.Run("EmptyMountpoint", func(t *testing.T) {
		err := RemountReadWrite("")
		if err == nil {
			t.Fatal("expected error for empty mountpoint")
		}
		if !strings.Contains(err.Error(), "missing mnt parameter") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("ExecFails", func(t *testing.T) {
		origExecRun := execRun
		t.Cleanup(func() { execRun = origExecRun })
		execRun = func(c *runner.Cmd) error {
			return fmt.Errorf("mount failed")
		}

		err := RemountReadWrite("/mnt")
		if err == nil {
			t.Fatal("expected error when mount command fails")
		}
		if !strings.Contains(err.Error(), "mount failed") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("Success", func(t *testing.T) {
		var capturedArgs []string
		origExecRun := execRun
		t.Cleanup(func() { execRun = origExecRun })
		execRun = func(c *runner.Cmd) error {
			capturedArgs = append([]string{c.Name}, c.Args...)
			return nil
		}

		if err := RemountReadWrite("/mnt"); err != nil {
			t.Fatalf("RemountReadWrite failed: %v", err)
		}

		expected := []string{"mount", "-o", "remount,rw", "--", "/mnt"}
		if len(capturedArgs) != len(expected) {
			t.Fatalf("expected args %v, got %v", expected, capturedArgs)
		}
		for i, arg := range expected {
			if capturedArgs[i] != arg {
				t.Errorf("arg[%d]: expected %q, got %q", i, arg, capturedArgs[i])
			}
		}
	})
}
