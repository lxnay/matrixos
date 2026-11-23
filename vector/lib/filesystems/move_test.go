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

	t.Run("ReadOnlySourceSubdirAsNonRoot", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root bypasses DAC write checks on 0555 directories")
		}
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src")
		roSub := filepath.Join(src, "rosub")
		dst := filepath.Join(tmp, "dst")
		if err := os.MkdirAll(roSub, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(roSub, "inner.txt"), []byte("secret"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(roSub, 0555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = os.Chmod(roSub, 0755)
			_ = os.Chmod(filepath.Join(dst, "rosub"), 0755)
		})

		if err := moveDirCrossDevice(src, dst); err != nil {
			t.Fatalf("moveDirCrossDevice failed on read-only 0555 subdir: %v", err)
		}
		if _, err := os.Stat(src); !os.IsNotExist(err) {
			t.Fatalf("source dir %s still exists after moveDirCrossDevice", src)
		}
		data, err := os.ReadFile(filepath.Join(dst, "rosub", "inner.txt"))
		if err != nil || string(data) != "secret" {
			t.Fatalf("moved inner.txt = (%q, %v), want (\"secret\", nil)", string(data), err)
		}
		info, err := os.Stat(filepath.Join(dst, "rosub"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0555 {
			t.Errorf("dst/rosub perms = %04o, want 0555", info.Mode().Perm())
		}
	})

	t.Run("RejectsDotDotDotAndRootDst", func(t *testing.T) {
		tmp := t.TempDir()
		src := filepath.Join(tmp, "src")
		emptyDst := filepath.Join(tmp, "emptyDst")
		subDst := filepath.Join(tmp, "subParent", "sub")
		if err := os.MkdirAll(src, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "keep.txt"), []byte("keep"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(emptyDst, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(subDst, 0755); err != nil {
			t.Fatal(err)
		}

		if err := moveDirCrossDevice(src, emptyDst+"/."); err == nil {
			t.Fatal("moveDirCrossDevice accepted dst ending in '/.' and replaced emptyDst")
		}
		if _, err := os.Stat(filepath.Join(src, "keep.txt")); err != nil {
			t.Fatalf("src was deleted or damaged after '/.' rejection: %v", err)
		}
		if err := moveDirCrossDevice(src, subDst+"/.."); err == nil {
			t.Fatal("moveDirCrossDevice accepted dst ending in '/..'")
		}

		workDir := t.TempDir()
		t.Chdir(workDir)
		// Put a socket inside src so if moveDirCrossDevice stages a copy before
		// rejecting "." / ".." / "/", the error will mention unsupported special file.
		if err := unix.Mknod(filepath.Join(src, "s.sock"), unix.S_IFSOCK|0600, 0); err != nil {
			t.Fatalf("Mknod: %v", err)
		}
		for _, badDst := range []string{".", "..", "/"} {
			err := moveDirCrossDevice(src, badDst)
			if err == nil {
				t.Errorf("moveDirCrossDevice(src, %q) succeeded, want error", badDst)
			} else if strings.Contains(err.Error(), "unsupported special file") {
				t.Errorf("moveDirCrossDevice(src, %q) staged a full copy before rejecting invalid dst: %v", badDst, err)
			}
		}
	})
}

func TestMoveCrossDevice_NonDirTrailingSlashDoesNotCreateDir(t *testing.T) {
	cases := []struct {
		name  string
		setup func(path string) error
	}{
		{"RegularFile", func(p string) error { return os.WriteFile(p, []byte("hi"), 0644) }},
		{"Symlink", func(p string) error { return os.Symlink("target", p) }},
		{"FIFO", func(p string) error { return unix.Mkfifo(p, 0640) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			src := filepath.Join(tmp, "src")
			if err := tc.setup(src); err != nil {
				t.Fatal(err)
			}
			dstBase := filepath.Join(tmp, "newdst")
			if err := moveCrossDevice(src, dstBase+"/"); err == nil {
				t.Fatalf("moveCrossDevice(%s, dst/) succeeded for non-dir with trailing slash", tc.name)
			}
			if info, err := os.Lstat(dstBase); !os.IsNotExist(err) {
				t.Fatalf("moveCrossDevice(%s, dst/) created bogus entry at %s (info=%v, err=%v)", tc.name, dstBase, info, err)
			}
			if _, err := os.Lstat(src); err != nil {
				t.Fatalf("source %s should remain intact on error: %v", src, err)
			}
		})
	}
}

func TestMoveDirCrossDevice_ReadOnlyTopLevelSourceDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses DAC write checks on read-only directories")
	}
	for _, perm := range []os.FileMode{0555, 0500} {
		t.Run(perm.String(), func(t *testing.T) {
			tmp := t.TempDir()
			src := filepath.Join(tmp, "src")
			dst := filepath.Join(tmp, "dst")
			if err := os.MkdirAll(src, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(src, "inner.txt"), []byte("keep"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(src, perm); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = os.Chmod(src, 0755)
				_ = os.Chmod(dst, 0755)
			})

			if err := moveDirCrossDevice(src, dst); err != nil {
				t.Fatalf("moveDirCrossDevice failed on %04o top-level dir: %v", perm, err)
			}
			if _, err := os.Lstat(src); !os.IsNotExist(err) {
				t.Fatalf("source %s still exists after move (err=%v)", src, err)
			}
			data, err := os.ReadFile(filepath.Join(dst, "inner.txt"))
			if err != nil || string(data) != "keep" {
				t.Fatalf("dst/inner.txt = (%q, %v), want (\"keep\", nil)", string(data), err)
			}
			info, err := os.Lstat(dst)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != perm {
				t.Errorf("dst perms = %04o, want %04o", info.Mode().Perm(), perm)
			}
		})
	}
}

func TestCopyDirPreserveAndMoveDir_RejectSelfAndDescendantDst(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	keepFile := filepath.Join(src, "keep.txt")
	if err := os.WriteFile(keepFile, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := CopyDirPreserve(src, src); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("CopyDirPreserve(src, src) = %v, want EINVAL", err)
	}
	if data, err := os.ReadFile(keepFile); err != nil || string(data) != "keep" {
		t.Fatalf("keep.txt damaged after CopyDirPreserve(src, src): %q, %v", string(data), err)
	}

	subDst := filepath.Join(src, "sub")
	if err := CopyDirPreserve(src, subDst); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("CopyDirPreserve(src, src/sub) = %v, want EINVAL", err)
	}
	if _, err := os.Lstat(subDst); !os.IsNotExist(err) {
		t.Fatalf("CopyDirPreserve(src, src/sub) created %s", subDst)
	}

	nestedDst := filepath.Join(src, "newparent", "sub")
	if err := moveDirCrossDevice(src, nestedDst); !errors.Is(err, syscall.EINVAL) {
		t.Fatalf("moveDirCrossDevice(src, src/newparent/sub) = %v, want EINVAL", err)
	}
	if _, err := os.Lstat(filepath.Join(src, "newparent")); !os.IsNotExist(err) {
		t.Fatalf("moveDirCrossDevice created dst parent inside src")
	}
	if data, err := os.ReadFile(keepFile); err != nil || string(data) != "keep" {
		t.Fatalf("keep.txt damaged after moveDirCrossDevice self-descendant rejection: %q, %v", string(data), err)
	}

	if err := moveDirCrossDevice("/", filepath.Join(tmp, "rootdst")); err == nil || !strings.Contains(err.Error(), "IS MAPPED TO HOST ROOT") {
		t.Fatalf("moveDirCrossDevice(\"/\", dst) = %v, want host-root guard error", err)
	}
}

func TestMoveCrossDevice_SamePathDoesNotDeleteSource(t *testing.T) {
	t.Run("RegularFile", func(t *testing.T) {
		tmp := t.TempDir()
		f := filepath.Join(tmp, "f.txt")
		if err := os.WriteFile(f, []byte("precious"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := moveCrossDevice(f, f); err != nil {
			t.Fatalf("moveCrossDevice(f, f): %v", err)
		}
		if data, err := os.ReadFile(f); err != nil || string(data) != "precious" {
			t.Fatalf("file lost or corrupted: (%q, %v)", string(data), err)
		}
	})

	t.Run("HardLink", func(t *testing.T) {
		tmp := t.TempDir()
		a := filepath.Join(tmp, "a.txt")
		b := filepath.Join(tmp, "b.txt")
		if err := os.WriteFile(a, []byte("shared"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(a, b); err != nil {
			t.Fatal(err)
		}
		if err := moveCrossDevice(a, b); err != nil {
			t.Fatalf("moveCrossDevice(a, hardlink): %v", err)
		}
		for _, p := range []string{a, b} {
			if data, err := os.ReadFile(p); err != nil || string(data) != "shared" {
				t.Fatalf("hardlink %s lost or corrupted: (%q, %v)", p, string(data), err)
			}
		}
	})

	t.Run("EmptyDir", func(t *testing.T) {
		tmp := t.TempDir()
		d := filepath.Join(tmp, "dir")
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
		if err := moveCrossDevice(d, d); err != nil {
			t.Fatalf("moveCrossDevice(d, d): %v", err)
		}
		if info, err := os.Lstat(d); err != nil || !info.IsDir() {
			t.Fatalf("dir lost: (%v, %v)", info, err)
		}
	})

	t.Run("Symlink", func(t *testing.T) {
		tmp := t.TempDir()
		l := filepath.Join(tmp, "link")
		if err := os.Symlink("target", l); err != nil {
			t.Fatal(err)
		}
		if err := moveCrossDevice(l, l); err != nil {
			t.Fatalf("moveCrossDevice(l, l): %v", err)
		}
		if target, err := os.Readlink(l); err != nil || target != "target" {
			t.Fatalf("symlink lost: (%q, %v)", target, err)
		}
	})

	t.Run("FIFO", func(t *testing.T) {
		tmp := t.TempDir()
		p := filepath.Join(tmp, "pipe")
		if err := unix.Mkfifo(p, 0640); err != nil {
			t.Fatal(err)
		}
		if err := moveCrossDevice(p, p); err != nil {
			t.Fatalf("moveCrossDevice(p, p): %v", err)
		}
		if info, err := os.Lstat(p); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			t.Fatalf("FIFO lost: (%v, %v)", info, err)
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
