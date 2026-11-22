package filesystems

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// CopyFile copies a file from src to dst atomically. It writes to a temporary
// file first, syncs it, then renames to the final destination. This ensures the
// destination is never left in a partial state.
func CopyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst + ".tmp")
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	err = destFile.Sync()
	if err != nil {
		return err
	}
	sourceFile.Close()
	destFile.Close()

	return os.Rename(dst+".tmp", dst)
}

// CopyDirPreserve recursively copies a directory tree from src to dst,
// preserving permissions and extended attributes. Symlinks are recreated
// as symlinks. This is the native Go equivalent of "cp -rp".
func CopyDirPreserve(src, dst string) error {
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %w", src, err)
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("source %s is not a directory", src)
	}

	if err := os.MkdirAll(dst, srcInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("failed to create destination %s: %w", dst, err)
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		info, err := d.Info()
		if err != nil {
			return err
		}

		// Handle symlinks.
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("failed to read symlink %s: %w", path, err)
			}
			return os.Symlink(target, dstPath)
		}

		// Handle directories.
		if d.IsDir() {
			if relPath == "." {
				return nil // already created above
			}
			if err := os.MkdirAll(dstPath, info.Mode().Perm()); err != nil {
				return err
			}
			// Preserve sticky, setuid, setgid bits that MkdirAll ignores.
			specialBits := info.Mode() & (os.ModeSticky | os.ModeSetuid | os.ModeSetgid)
			if specialBits != 0 {
				return os.Chmod(dstPath, info.Mode().Perm()|specialBits)
			}
			return nil
		}

		// Handle regular files.
		return CopyFilePreserveXattrs(path, dstPath)
	})
}

// CopyFilePreserveXattrs copies a file from src to dst, preserving permissions
// and extended attributes (xattrs). This is equivalent to "cp -a" for regular files.
func CopyFilePreserveXattrs(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	// Copy extended attributes (includes security.capability)
	attrs, err := sysLlistxattr(src, nil)
	if err != nil || attrs == 0 {
		return nil // no xattrs or not supported
	}
	buf := make([]byte, attrs)
	attrs, err = sysLlistxattr(src, buf)
	if err != nil {
		return nil
	}

	// xattr names are null-terminated strings packed together
	for _, name := range strings.Split(strings.TrimRight(string(buf[:attrs]), "\x00"), "\x00") {
		if name == "" {
			continue
		}
		sz, err := sysLgetxattr(src, name, nil)
		if err != nil {
			continue
		}
		val := make([]byte, sz)
		_, err = sysLgetxattr(src, name, val)
		if err != nil {
			continue
		}
		if err := sysLsetxattr(dst, name, val, 0); err != nil {
			return fmt.Errorf("failed to set xattr %q on %s: %w", name, dst, err)
		}
	}
	return nil
}

// CopyFileReflink copies a single file using cp with --reflink=auto to exploit
// CoW cloning on filesystems that support it (e.g. btrfs, xfs). Falls back to
// a full copy transparently. Preserves attributes and links.
func CopyFileReflink(src, dst string) error {
	if src == "" {
		return fmt.Errorf("missing src parameter")
	}
	if dst == "" {
		return fmt.Errorf("missing dst parameter")
	}
	cmd := exec.Command("cp", "-a", "--reflink=auto", "--preserve=links", src, dst)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cp --reflink=auto failed: %w", err)
	}
	return nil
}

// CheckHardlinkPreservationOptions specifies options for the
// CheckHardlinkPreservation function.
type CheckHardlinkPreservationOptions struct {
	Stdout io.Writer // informational output (nil defaults to os.Stdout)
	Stderr io.Writer // error/warning output (nil defaults to os.Stderr)
}

// CheckHardlinkPreservation verifies that hardlinks are preserved between source and destination.
func CheckHardlinkPreservation(src, dst string, opts CheckHardlinkPreservationOptions) error {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	if src == "" || dst == "" {
		return fmt.Errorf("missing parameter (src: %s, dst: %s)", src, dst)
	}
	fmt.Fprintf(stdout, "Checking hardlink preservation from %s to %s...\n", src, dst)

	// 1. Walk the source directory to find files with multiple links.
	// 2. Track Inodes to find the first pair of files sharing the same inode.
	// Using a map for O(1) checks.

	// Map from Inode (uint64) -> Path
	seenInodes := make(map[uint64]string)

	var file1Src, file2Src string
	foundPair := false

	// Sentinel error to stop walking early
	errFoundPair := fmt.Errorf("found pair")

	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		sys, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}

		if sys.Nlink > 1 {
			if existingPath, ok := seenInodes[sys.Ino]; ok {
				file1Src = existingPath
				file2Src = path
				foundPair = true
				return errFoundPair
			}
			seenInodes[sys.Ino] = path
		}
		return nil
	})

	if err != nil && err != errFoundPair {
		return fmt.Errorf("error walking source directory: %w", err)
	}

	if !foundPair {
		fmt.Fprintln(stderr, "WARNING: no hardlinked file pairs found in source. Cannot verify.")
		return nil
	}

	relPath1, err := filepath.Rel(src, file1Src)
	if err != nil {
		return err
	}
	relPath2, err := filepath.Rel(src, file2Src)
	if err != nil {
		return err
	}

	file1Dst := filepath.Join(dst, relPath1)
	file2Dst := filepath.Join(dst, relPath2)

	info1, err := os.Stat(file1Dst)
	if err != nil {
		return err
	}
	info2, err := os.Stat(file2Dst)
	if err != nil {
		return err
	}

	stat1, ok1 := info1.Sys().(*syscall.Stat_t)
	stat2, ok2 := info2.Sys().(*syscall.Stat_t)

	if !ok1 || !ok2 {
		return fmt.Errorf("could not get inode info")
	}

	if stat1.Ino != stat2.Ino {
		return fmt.Errorf(
			"CRITICAL: hardlinks BROKEN! Files were duplicated.\n  File 1: %s (inode: %d)\n  File 2: %s (inode: %d)",
			file1Dst, stat1.Ino, file2Dst, stat2.Ino,
		)
	}

	fmt.Fprintf(stdout, "SUCCESS: hardlinks preserved (Inode: %d).\n", stat1.Ino)
	return nil
}
