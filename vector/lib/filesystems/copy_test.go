package filesystems

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCopyFile(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "src.txt")
		dst := filepath.Join(tmpDir, "dst.txt")
		content := []byte("hello world")
		if err := os.WriteFile(src, content, 0644); err != nil {
			t.Fatal(err)
		}

		if err := CopyFile(src, dst); err != nil {
			t.Fatalf("CopyFile failed: %v", err)
		}

		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("Failed to read dst: %v", err)
		}
		if string(data) != string(content) {
			t.Errorf("got %q, want %q", data, content)
		}
	})

	t.Run("EmptyFile", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "src_empty.txt")
		dst := filepath.Join(tmpDir, "dst_empty.txt")
		if err := os.WriteFile(src, nil, 0644); err != nil {
			t.Fatal(err)
		}

		if err := CopyFile(src, dst); err != nil {
			t.Fatalf("CopyFile failed: %v", err)
		}

		info, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() != 0 {
			t.Errorf("expected empty file, got size %d", info.Size())
		}
	})

	t.Run("SrcNotFound", func(t *testing.T) {
		tmpDir := t.TempDir()
		err := CopyFile(filepath.Join(tmpDir, "nonexistent"), filepath.Join(tmpDir, "dst"))
		if err == nil {
			t.Error("expected error for nonexistent source")
		}
	})
}

func TestCopyFilePreserveXattrs(t *testing.T) {
	dir := t.TempDir()

	t.Run("BasicCopy", func(t *testing.T) {
		src := filepath.Join(dir, "src_basic")
		dst := filepath.Join(dir, "dst_basic")
		content := []byte("hello xattr world")
		if err := os.WriteFile(src, content, 0644); err != nil {
			t.Fatal(err)
		}

		if err := CopyFilePreserveXattrs(src, dst); err != nil {
			t.Fatalf("CopyFilePreserveXattrs failed: %v", err)
		}

		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(content) {
			t.Errorf("content mismatch: got %q, want %q", got, content)
		}

		info, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0644 {
			t.Errorf("permissions mismatch: got %04o, want 0644", info.Mode().Perm())
		}
	})

	t.Run("PreservesXattrs", func(t *testing.T) {
		src := filepath.Join(dir, "src_xattr")
		dst := filepath.Join(dir, "dst_xattr")
		if err := os.WriteFile(src, []byte("data"), 0755); err != nil {
			t.Fatal(err)
		}

		attrName := "user.test_attr"
		attrVal := []byte("test_value_123")
		if err := unix.Lsetxattr(src, attrName, attrVal, 0); err != nil {
			t.Skipf("filesystem does not support user xattrs: %v", err)
		}

		if err := CopyFilePreserveXattrs(src, dst); err != nil {
			t.Fatalf("CopyFilePreserveXattrs failed: %v", err)
		}

		// Verify xattr was copied
		sz, err := unix.Lgetxattr(dst, attrName, nil)
		if err != nil {
			t.Fatalf("xattr not found on dst: %v", err)
		}
		buf := make([]byte, sz)
		_, err = unix.Lgetxattr(dst, attrName, buf)
		if err != nil {
			t.Fatalf("failed to read xattr: %v", err)
		}
		if string(buf) != string(attrVal) {
			t.Errorf("xattr value mismatch: got %q, want %q", buf, attrVal)
		}
	})

	t.Run("MultipleXattrs", func(t *testing.T) {
		src := filepath.Join(dir, "src_multi")
		dst := filepath.Join(dir, "dst_multi")
		if err := os.WriteFile(src, []byte("multi"), 0600); err != nil {
			t.Fatal(err)
		}

		attrs := map[string]string{
			"user.attr_a": "value_a",
			"user.attr_b": "value_b",
			"user.attr_c": "value_c",
		}
		for k, v := range attrs {
			if err := unix.Lsetxattr(src, k, []byte(v), 0); err != nil {
				t.Skipf("filesystem does not support user xattrs: %v", err)
			}
		}

		if err := CopyFilePreserveXattrs(src, dst); err != nil {
			t.Fatalf("CopyFilePreserveXattrs failed: %v", err)
		}

		for k, want := range attrs {
			sz, err := unix.Lgetxattr(dst, k, nil)
			if err != nil {
				t.Errorf("xattr %s not found on dst: %v", k, err)
				continue
			}
			buf := make([]byte, sz)
			unix.Lgetxattr(dst, k, buf)
			if string(buf) != want {
				t.Errorf("xattr %s: got %q, want %q", k, buf, want)
			}
		}
	})

	t.Run("EmptyFile", func(t *testing.T) {
		src := filepath.Join(dir, "src_empty")
		dst := filepath.Join(dir, "dst_empty")
		if err := os.WriteFile(src, nil, 0600); err != nil {
			t.Fatal(err)
		}

		if err := CopyFilePreserveXattrs(src, dst); err != nil {
			t.Fatalf("CopyFilePreserveXattrs failed: %v", err)
		}

		info, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() != 0 {
			t.Errorf("expected empty file, got size %d", info.Size())
		}
	})

	t.Run("SrcNotExist", func(t *testing.T) {
		err := CopyFilePreserveXattrs("/nonexistent/file", filepath.Join(dir, "dst_noexist"))
		if err == nil {
			t.Error("expected error for nonexistent source")
		}
	})

	t.Run("SetxattrError", func(t *testing.T) {
		origLsetxattr := sysLsetxattr
		origLgetxattr := sysLgetxattr
		origLlistxattr := sysLlistxattr

		xattrStore := map[string]map[string][]byte{}

		// Set a real xattr on the source via the mock
		sysLsetxattr = func(path, attr string, data []byte, flags int) error {
			// Allow setting on src, fail on dst
			if strings.HasSuffix(path, "_xfail_dst") {
				return unix.EPERM
			}
			if _, ok := xattrStore[path]; !ok {
				xattrStore[path] = make(map[string][]byte)
			}
			val := make([]byte, len(data))
			copy(val, data)
			xattrStore[path][attr] = val
			return nil
		}
		sysLgetxattr = func(path, attr string, dest []byte) (int, error) {
			fa, ok := xattrStore[path]
			if !ok {
				return 0, unix.ENODATA
			}
			v, ok := fa[attr]
			if !ok {
				return 0, unix.ENODATA
			}
			if dest == nil {
				return len(v), nil
			}
			return copy(dest, v), nil
		}
		sysLlistxattr = func(path string, dest []byte) (int, error) {
			fa, ok := xattrStore[path]
			if !ok {
				return 0, nil
			}
			var packed []byte
			for name := range fa {
				packed = append(packed, []byte(name)...)
				packed = append(packed, 0)
			}
			if dest == nil {
				return len(packed), nil
			}
			return copy(dest, packed), nil
		}
		t.Cleanup(func() {
			sysLsetxattr = origLsetxattr
			sysLgetxattr = origLgetxattr
			sysLlistxattr = origLlistxattr
		})

		src := filepath.Join(dir, "src_xfail")
		dst := filepath.Join(dir, "src_xfail_dst")
		if err := os.WriteFile(src, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}

		// Set an xattr on the source
		sysLsetxattr(src, "user.test", []byte("val"), 0)

		err := CopyFilePreserveXattrs(src, dst)
		if err == nil {
			t.Error("expected error when sysLsetxattr fails on destination")
		}
	})
}

func TestCheckHardlinkPreservation(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	file1 := filepath.Join(srcDir, "file1")
	if err := os.WriteFile(file1, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	file2 := filepath.Join(srcDir, "file2")
	if err := os.Link(file1, file2); err != nil {
		t.Fatal(err)
	}

	dstFile1 := filepath.Join(dstDir, "file1")
	dstFile2 := filepath.Join(dstDir, "file2")
	if err := os.WriteFile(dstFile1, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(dstFile1, dstFile2); err != nil {
		t.Fatal(err)
	}

	if err := CheckHardlinkPreservation(srcDir, dstDir, CheckHardlinkPreservationOptions{}); err != nil {
		t.Errorf("CheckHardlinkPreservation failed when links preserved: %v", err)
	}

	dstDirBroken := t.TempDir()
	dstBroken1 := filepath.Join(dstDirBroken, "file1")
	dstBroken2 := filepath.Join(dstDirBroken, "file2")
	if err := os.WriteFile(dstBroken1, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dstBroken2, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := CheckHardlinkPreservation(srcDir, dstDirBroken, CheckHardlinkPreservationOptions{}); err == nil {
		t.Error("CheckHardlinkPreservation should fail when links are broken")
	}
}

func TestCopyFileReflink(t *testing.T) {
	t.Run("EmptySrc", func(t *testing.T) {
		err := CopyFileReflink("", "/tmp/dst")
		if err == nil {
			t.Error("expected error for empty src")
		}
	})

	t.Run("EmptyDst", func(t *testing.T) {
		err := CopyFileReflink("/tmp/src", "")
		if err == nil {
			t.Error("expected error for empty dst")
		}
	})

	t.Run("Success", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "src.img")
		dst := filepath.Join(tmpDir, "dst.img")
		content := []byte("image data for reflink test")
		if err := os.WriteFile(src, content, 0644); err != nil {
			t.Fatal(err)
		}

		if err := CopyFileReflink(src, dst); err != nil {
			t.Fatalf("CopyFileReflink failed: %v", err)
		}

		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("Failed to read dst: %v", err)
		}
		if string(data) != string(content) {
			t.Errorf("got %q, want %q", data, content)
		}
	})

	t.Run("SrcNotFound", func(t *testing.T) {
		tmpDir := t.TempDir()
		err := CopyFileReflink(filepath.Join(tmpDir, "nonexistent"), filepath.Join(tmpDir, "dst"))
		if err == nil {
			t.Error("expected error for nonexistent source")
		}
	})
}

func TestCopyDirPreserve(t *testing.T) {
	t.Run("BasicTree", func(t *testing.T) {
		src := t.TempDir()
		dst := filepath.Join(t.TempDir(), "dst")

		// Create a small tree:
		//
		//	src/
		//	  file1.txt
		//	  sub/
		//	    file2.txt
		if err := os.WriteFile(filepath.Join(src, "file1.txt"), []byte("content1"), 0644); err != nil {
			t.Fatal(err)
		}
		subDir := filepath.Join(src, "sub")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(subDir, "file2.txt"), []byte("content2"), 0600); err != nil {
			t.Fatal(err)
		}

		if err := CopyDirPreserve(src, dst); err != nil {
			t.Fatalf("CopyDirPreserve failed: %v", err)
		}

		// Verify file1
		data, err := os.ReadFile(filepath.Join(dst, "file1.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "content1" {
			t.Errorf("file1: got %q, want %q", data, "content1")
		}

		// Verify sub/file2
		data, err = os.ReadFile(filepath.Join(dst, "sub", "file2.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "content2" {
			t.Errorf("file2: got %q, want %q", data, "content2")
		}

		// Verify file2 permissions
		info, err := os.Stat(filepath.Join(dst, "sub", "file2.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("file2 perms: got %04o, want 0600", info.Mode().Perm())
		}
	})

	t.Run("PreservesSymlinks", func(t *testing.T) {
		src := t.TempDir()
		dst := filepath.Join(t.TempDir(), "dst")

		targetFile := filepath.Join(src, "target.txt")
		if err := os.WriteFile(targetFile, []byte("target"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("target.txt", filepath.Join(src, "link.txt")); err != nil {
			t.Fatal(err)
		}

		if err := CopyDirPreserve(src, dst); err != nil {
			t.Fatalf("CopyDirPreserve failed: %v", err)
		}

		linkPath := filepath.Join(dst, "link.txt")
		linkInfo, err := os.Lstat(linkPath)
		if err != nil {
			t.Fatal(err)
		}
		if linkInfo.Mode()&os.ModeSymlink == 0 {
			t.Error("expected symlink, got regular file")
		}

		linkTarget, err := os.Readlink(linkPath)
		if err != nil {
			t.Fatal(err)
		}
		if linkTarget != "target.txt" {
			t.Errorf("symlink target: got %q, want %q", linkTarget, "target.txt")
		}
	})

	t.Run("PreservesDirPermissions", func(t *testing.T) {
		src := t.TempDir()
		dst := filepath.Join(t.TempDir(), "dst")

		subDir := filepath.Join(src, "restricted")
		if err := os.MkdirAll(subDir, 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(subDir, "f.txt"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}

		if err := CopyDirPreserve(src, dst); err != nil {
			t.Fatalf("CopyDirPreserve failed: %v", err)
		}

		info, err := os.Stat(filepath.Join(dst, "restricted"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0750 {
			t.Errorf("dir perms: got %04o, want 0750", info.Mode().Perm())
		}
	})

	t.Run("EmptyDir", func(t *testing.T) {
		src := t.TempDir()
		dst := filepath.Join(t.TempDir(), "dst")

		if err := CopyDirPreserve(src, dst); err != nil {
			t.Fatalf("CopyDirPreserve failed: %v", err)
		}

		info, err := os.Stat(dst)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Error("expected dst to be a directory")
		}
	})

	t.Run("NestedEmpty", func(t *testing.T) {
		src := t.TempDir()
		dst := filepath.Join(t.TempDir(), "dst")

		if err := os.MkdirAll(filepath.Join(src, "a", "b", "c"), 0755); err != nil {
			t.Fatal(err)
		}

		if err := CopyDirPreserve(src, dst); err != nil {
			t.Fatalf("CopyDirPreserve failed: %v", err)
		}

		info, err := os.Stat(filepath.Join(dst, "a", "b", "c"))
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Error("expected nested dir to exist")
		}
	})

	t.Run("PreservesXattrs", func(t *testing.T) {
		src := t.TempDir()
		dst := filepath.Join(t.TempDir(), "dst")

		srcFile := filepath.Join(src, "xfile.txt")
		if err := os.WriteFile(srcFile, []byte("xattr data"), 0644); err != nil {
			t.Fatal(err)
		}

		attrName := "user.test_copy_dir"
		attrVal := []byte("dir_test_value")
		if err := unix.Lsetxattr(srcFile, attrName, attrVal, 0); err != nil {
			t.Skipf("filesystem does not support user xattrs: %v", err)
		}

		if err := CopyDirPreserve(src, dst); err != nil {
			t.Fatalf("CopyDirPreserve failed: %v", err)
		}

		dstFile := filepath.Join(dst, "xfile.txt")
		sz, err := unix.Lgetxattr(dstFile, attrName, nil)
		if err != nil {
			t.Fatalf("xattr not found on dst: %v", err)
		}
		buf := make([]byte, sz)
		if _, err := unix.Lgetxattr(dstFile, attrName, buf); err != nil {
			t.Fatalf("failed to read xattr: %v", err)
		}
		if string(buf) != string(attrVal) {
			t.Errorf("xattr value: got %q, want %q", buf, attrVal)
		}
	})

	t.Run("PreservesStickyBit", func(t *testing.T) {
		src := t.TempDir()
		dst := filepath.Join(t.TempDir(), "dst")

		stickyDir := filepath.Join(src, "sticky")
		if err := os.MkdirAll(stickyDir, 0755); err != nil {
			t.Fatal(err)
		}
		// Set the sticky bit.
		if err := os.Chmod(stickyDir, 0755|os.ModeSticky); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stickyDir, "f.txt"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}

		if err := CopyDirPreserve(src, dst); err != nil {
			t.Fatalf("CopyDirPreserve failed: %v", err)
		}

		info, err := os.Stat(filepath.Join(dst, "sticky"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSticky == 0 {
			t.Errorf("sticky bit not preserved: got mode %v", info.Mode())
		}
		if info.Mode().Perm() != 0755 {
			t.Errorf("perms changed: got %04o, want 0755", info.Mode().Perm())
		}
	})

	t.Run("PreservesSetgidBit", func(t *testing.T) {
		src := t.TempDir()
		dst := filepath.Join(t.TempDir(), "dst")

		setgidDir := filepath.Join(src, "setgid")
		if err := os.MkdirAll(setgidDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(setgidDir, 0755|os.ModeSetgid); err != nil {
			t.Fatal(err)
		}

		if err := CopyDirPreserve(src, dst); err != nil {
			t.Fatalf("CopyDirPreserve failed: %v", err)
		}

		info, err := os.Stat(filepath.Join(dst, "setgid"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode()&os.ModeSetgid == 0 {
			t.Errorf("setgid bit not preserved: got mode %v", info.Mode())
		}
	})

	t.Run("PreservesDirXattrs", func(t *testing.T) {
		src := t.TempDir()
		dst := filepath.Join(t.TempDir(), "dst")

		subDir := filepath.Join(src, "xattrdir")
		if err := os.MkdirAll(subDir, 0755); err != nil {
			t.Fatal(err)
		}

		attrName := "user.dir_test"
		attrVal := []byte("dir_xattr_value")
		if err := unix.Lsetxattr(subDir, attrName, attrVal, 0); err != nil {
			t.Skipf("filesystem does not support user xattrs on dirs: %v", err)
		}

		// Also put a file with xattrs inside.
		fileInDir := filepath.Join(subDir, "inner.txt")
		if err := os.WriteFile(fileInDir, []byte("inner"), 0644); err != nil {
			t.Fatal(err)
		}
		fileAttr := "user.file_in_dir"
		fileAttrVal := []byte("file_val")
		if err := unix.Lsetxattr(fileInDir, fileAttr, fileAttrVal, 0); err != nil {
			t.Skipf("filesystem does not support user xattrs: %v", err)
		}
		// Make the source subdir read-only to verify directory xattrs are
		// applied before bottom-up chmod restores 0555 permissions.
		if err := os.Chmod(subDir, 0555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = os.Chmod(subDir, 0755)
			_ = os.Chmod(filepath.Join(dst, "xattrdir"), 0755)
		})

		if err := CopyDirPreserve(src, dst); err != nil {
			t.Fatalf("CopyDirPreserve failed: %v", err)
		}

		// Verify directory xattrs were preserved (including on read-only dirs).
		dstDir := filepath.Join(dst, "xattrdir")
		dirSz, err := unix.Lgetxattr(dstDir, attrName, nil)
		if err != nil {
			t.Fatalf("directory xattr %q not found on %s: %v", attrName, dstDir, err)
		}
		dirBuf := make([]byte, dirSz)
		if _, err := unix.Lgetxattr(dstDir, attrName, dirBuf); err != nil {
			t.Fatalf("failed to read directory xattr: %v", err)
		}
		if string(dirBuf) != string(attrVal) {
			t.Errorf("directory xattr: got %q, want %q", dirBuf, attrVal)
		}

		// Verify file xattrs were preserved.
		dstFile := filepath.Join(dst, "xattrdir", "inner.txt")
		sz, err := unix.Lgetxattr(dstFile, fileAttr, nil)
		if err != nil {
			t.Fatalf("file xattr not found on dst: %v", err)
		}
		buf := make([]byte, sz)
		if _, err := unix.Lgetxattr(dstFile, fileAttr, buf); err != nil {
			t.Fatalf("failed to read file xattr: %v", err)
		}
		if string(buf) != string(fileAttrVal) {
			t.Errorf("file xattr: got %q, want %q", buf, fileAttrVal)
		}
	})

	t.Run("SrcNotExist", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "dst")
		err := CopyDirPreserve("/nonexistent/dir", dst)
		if err == nil {
			t.Error("expected error for nonexistent source")
		}
	})

	t.Run("SrcIsFile", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "notadir.txt")
		dst := filepath.Join(tmpDir, "dst")
		if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}

		err := CopyDirPreserve(src, dst)
		if err == nil {
			t.Error("expected error when source is a file")
		}
		if !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}
