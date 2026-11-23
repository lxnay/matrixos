package filesystems

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// --- Move (same device / fast path) ---

func TestMove_FileSuccess(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "dst.txt")
	if err := os.WriteFile(src, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Move(src, dst); err != nil {
		t.Fatalf("Move failed: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read dst: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("got %q, want %q", data, "hello")
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should have been removed")
	}
}

func TestMove_DirSuccess(t *testing.T) {
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "srcdir")
	dstDir := filepath.Join(tmp, "dstdir")
	if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "sub", "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Move(srcDir, dstDir); err != nil {
		t.Fatalf("Move failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dstDir, "sub", "file.txt"))
	if err != nil {
		t.Fatalf("failed to read moved file: %v", err)
	}
	if string(data) != "content" {
		t.Errorf("got %q, want %q", data, "content")
	}

	if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
		t.Error("source dir should have been removed")
	}
}

func TestMove_SrcNotFound(t *testing.T) {
	tmp := t.TempDir()
	err := Move(filepath.Join(tmp, "nonexistent"), filepath.Join(tmp, "dst"))
	if err == nil {
		t.Error("expected error for nonexistent source")
	}

	nestedDst := filepath.Join(tmp, "newparent", "nested", "dst")
	if err := Move(filepath.Join(tmp, "missing-src"), nestedDst); err == nil {
		t.Error("expected error for nonexistent source with missing dst parent")
	}
	if _, statErr := os.Stat(filepath.Join(tmp, "newparent")); !os.IsNotExist(statErr) {
		t.Errorf("Move created destination parent directory despite missing source: stat err = %v", statErr)
	}
}

func TestMove_PermissionsPreserved(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.sh")
	dst := filepath.Join(tmp, "dst.sh")
	if err := os.WriteFile(src, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := Move(src, dst); err != nil {
		t.Fatalf("Move failed: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("permissions: got %o, want %o", info.Mode().Perm(), 0755)
	}
}

func TestMove_DstDirCreated(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "nested", "deep", "dst.txt")
	if err := os.WriteFile(src, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Move(src, dst); err != nil {
		t.Fatalf("Move failed: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "test" {
		t.Errorf("got %q, want %q", data, "test")
	}
}

func TestMove_EmptyFile(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "empty.txt")
	dst := filepath.Join(tmp, "empty_dst.txt")
	if err := os.WriteFile(src, nil, 0644); err != nil {
		t.Fatal(err)
	}

	if err := Move(src, dst); err != nil {
		t.Fatalf("Move failed: %v", err)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Errorf("expected empty file, got size %d", info.Size())
	}
}

// --- isCrossDeviceError ---

func TestIsCrossDeviceError_True(t *testing.T) {
	err := &os.LinkError{
		Op:  "rename",
		Old: "/a",
		New: "/b",
		Err: syscall.EXDEV,
	}
	if !isCrossDeviceError(err) {
		t.Error("expected true for EXDEV LinkError")
	}
}

func TestIsCrossDeviceError_False(t *testing.T) {
	err := &os.LinkError{
		Op:  "rename",
		Old: "/a",
		New: "/b",
		Err: syscall.ENOENT,
	}
	if isCrossDeviceError(err) {
		t.Error("expected false for ENOENT")
	}
}

func TestIsCrossDeviceError_WrappedEXDEV(t *testing.T) {
	err := errors.New("wrap: " + syscall.EXDEV.Error())
	// Not directly an EXDEV, the error message matches but errors.Is won't.
	if isCrossDeviceError(err) {
		t.Error("plain string wrapping should not match")
	}
}

// --- Cross-device fallback paths ---

func TestMoveFileCrossDevice(t *testing.T) {
	// Since we can't easily create real cross-device scenarios in unit tests,
	// we exercise moveFileCrossDevice directly.
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.txt")
	dst := filepath.Join(tmp, "subdir", "dst.txt")
	if err := os.WriteFile(src, []byte("cross device"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := moveFileCrossDevice(src, dst); err != nil {
		t.Fatalf("moveFileCrossDevice failed: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "cross device" {
		t.Errorf("got %q, want %q", data, "cross device")
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("permissions: got %o, want %o", info.Mode().Perm(), 0600)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should have been removed")
	}
}

func TestMoveDirCrossDevice(t *testing.T) {
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "srcdir")
	dstDir := filepath.Join(tmp, "dstdir")
	nested := filepath.Join(srcDir, "a", "b")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "file.txt"), []byte("deep"), 0644); err != nil {
		t.Fatal(err)
	}
	// Also create a symlink.
	if err := os.Symlink("file.txt", filepath.Join(nested, "link.txt")); err != nil {
		t.Fatal(err)
	}

	if err := moveDirCrossDevice(srcDir, dstDir); err != nil {
		t.Fatalf("moveDirCrossDevice failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dstDir, "a", "b", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "deep" {
		t.Errorf("got %q, want %q", data, "deep")
	}

	target, err := os.Readlink(filepath.Join(dstDir, "a", "b", "link.txt"))
	if err != nil {
		t.Fatalf("failed to read symlink: %v", err)
	}
	if target != "file.txt" {
		t.Errorf("symlink target: got %q, want %q", target, "file.txt")
	}

	if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
		t.Error("source dir should have been removed")
	}
}

func TestMoveFileCrossDevice_SrcNotFound(t *testing.T) {
	tmp := t.TempDir()
	err := moveFileCrossDevice(filepath.Join(tmp, "nope"), filepath.Join(tmp, "dst"))
	if err == nil {
		t.Error("expected error for nonexistent source")
	}
}

func TestMoveDirCrossDevice_AtomicStagingAndCleanupOnError(t *testing.T) {
	t.Run("NoPartialDstOnFailure", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src")
		dstParent := filepath.Join(tmp, "dstparent")
		dst := filepath.Join(dstParent, "dst")
		if err := os.MkdirAll(src, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := unix.Mknod(filepath.Join(src, "z.sock"), unix.S_IFSOCK|0600, 0); err != nil {
			t.Fatalf("Mknod S_IFSOCK: %v", err)
		}

		err := moveDirCrossDevice(src, dst)
		if err == nil {
			t.Fatal("expected moveDirCrossDevice to fail on unix socket inside src")
		}
		if _, statErr := os.Lstat(dst); !os.IsNotExist(statErr) {
			t.Fatalf("moveDirCrossDevice left partial destination %s on failure (stat err=%v)", dst, statErr)
		}
		entries, readErr := os.ReadDir(dstParent)
		if readErr == nil {
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".move-dir-") {
					t.Errorf("moveDirCrossDevice leaked staging dir %s", e.Name())
				}
			}
		}
	})

	t.Run("RejectsExistingNonEmptyDstWithoutMerging", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src")
		dst := filepath.Join(tmp, "dst")
		if err := os.MkdirAll(src, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dst, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "from_src.txt"), []byte("src"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, "existing.txt"), []byte("dst"), 0644); err != nil {
			t.Fatal(err)
		}

		if err := moveDirCrossDevice(src, dst); err == nil {
			t.Fatal("expected moveDirCrossDevice to fail when dst is a non-empty directory")
		}
		if _, err := os.Stat(filepath.Join(dst, "from_src.txt")); !os.IsNotExist(err) {
			t.Fatal("moveDirCrossDevice merged src into existing non-empty dst")
		}
		if _, err := os.Stat(filepath.Join(dst, "existing.txt")); err != nil {
			t.Fatalf("existing dst contents damaged: %v", err)
		}
		if _, err := os.Stat(filepath.Join(src, "from_src.txt")); err != nil {
			t.Fatalf("src contents should remain intact on error: %v", err)
		}
	})

	t.Run("TrailingSlashOnDst", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src")
		dst := filepath.Join(tmp, "dst") + "/"
		if err := os.MkdirAll(src, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("ok"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := moveDirCrossDevice(src, dst); err != nil {
			t.Fatalf("moveDirCrossDevice with trailing slash on dst failed: %v", err)
		}
		if _, err := os.Stat(filepath.Join(tmp, "dst", "f.txt")); err != nil {
			t.Fatalf("expected moved file at dst/f.txt: %v", err)
		}
	})
}

// --- Real cross-device tests (require root to mount tmpfs) ---

// mountTmpfs mounts a tmpfs at the given path. The caller must unmount it.
func mountTmpfs(t *testing.T, target string) {
	t.Helper()
	if err := Mount("tmpfs", target, "tmpfs", 0, "size=16M"); err != nil {
		t.Fatalf("failed to mount tmpfs on %s: %v", target, err)
	}
	t.Cleanup(func() {
		Unmount(target, 0)
	})
}

func TestMove_FileCrossDeviceReal(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping cross-device test: must run as root")
	}

	base := t.TempDir()

	// Create a tmpfs mount so src and dst live on different devices.
	mntDir := filepath.Join(base, "mnt")
	if err := os.MkdirAll(mntDir, 0755); err != nil {
		t.Fatal(err)
	}
	mountTmpfs(t, mntDir)

	src := filepath.Join(mntDir, "src.txt")
	dst := filepath.Join(base, "dst.txt") // base is on the original fs
	content := []byte("cross-device file content")
	if err := os.WriteFile(src, content, 0750); err != nil {
		t.Fatal(err)
	}

	if err := Move(src, dst); err != nil {
		t.Fatalf("Move across devices failed: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read dst: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("content: got %q, want %q", data, content)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0750 {
		t.Errorf("permissions: got %o, want %o", info.Mode().Perm(), 0750)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should have been removed after cross-device move")
	}
}

func TestMove_DirCrossDeviceReal(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping cross-device test: must run as root")
	}

	base := t.TempDir()

	mntDir := filepath.Join(base, "mnt")
	if err := os.MkdirAll(mntDir, 0755); err != nil {
		t.Fatal(err)
	}
	mountTmpfs(t, mntDir)

	srcDir := filepath.Join(mntDir, "srcdir")
	dstDir := filepath.Join(base, "dstdir")
	nested := filepath.Join(srcDir, "a", "b")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "file.txt"), []byte("deep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file.txt", filepath.Join(nested, "link.txt")); err != nil {
		t.Fatal(err)
	}

	if err := Move(srcDir, dstDir); err != nil {
		t.Fatalf("Move dir across devices failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dstDir, "a", "b", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "deep" {
		t.Errorf("content: got %q, want %q", data, "deep")
	}

	target, err := os.Readlink(filepath.Join(dstDir, "a", "b", "link.txt"))
	if err != nil {
		t.Fatalf("failed to read symlink: %v", err)
	}
	if target != "file.txt" {
		t.Errorf("symlink target: got %q, want %q", target, "file.txt")
	}

	if _, err := os.Stat(srcDir); !os.IsNotExist(err) {
		t.Error("source dir should have been removed after cross-device move")
	}
}
