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

	"golang.org/x/sys/unix"
)

// CopyFile copies a file from src to dst atomically. It writes to a temporary
// file first, syncs it, then renames to the final destination. This ensures the
// destination is never left in a partial state.
func CopyFile(src, dst string) error {
	return copyFileAtomic(src, dst, ".copy-*.tmp")
}

// copyFileAtomic copies src to a temporary file in dst's directory, preserves
// source permissions (including special mode bits), fsyncs, and atomically
// renames to dst.
func copyFileAtomic(src, dst, tmpPattern string) error {
	srcStat, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcStat.Mode().IsRegular() {
		return fmt.Errorf("cannot copy non-regular file %s (mode %s)", src, srcStat.Mode())
	}

	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	srcInfo, err := sourceFile.Stat()
	if err != nil {
		return err
	}
	if !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("cannot copy non-regular file %s (mode %s)", src, srcInfo.Mode())
	}

	destFile, err := os.CreateTemp(filepath.Dir(dst), tmpPattern)
	if err != nil {
		return err
	}
	tmpPath := destFile.Name()
	success := false
	defer func() {
		if !success {
			_ = destFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err = io.Copy(destFile, sourceFile); err != nil {
		return err
	}
	if err = destFile.Chmod(srcInfo.Mode()); err != nil {
		return err
	}
	if err = destFile.Sync(); err != nil {
		return err
	}
	if err = destFile.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmpPath, dst); err != nil {
		return err
	}
	success = true
	return nil
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
	if isSameOrSubpath(src, dst) {
		return fmt.Errorf("cannot copy directory %s into itself (%s): %w", src, dst, syscall.EINVAL)
	}

	type dirMeta struct {
		src  string
		dst  string
		mode fs.FileMode
	}

	// Create directories with owner rwx during the walk so non-root copies can
	// populate read-only (e.g. 0555/0500) source directories and apply directory
	// xattrs, then restore exact permissions and special bits bottom-up once all
	// children are written.
	if err := os.MkdirAll(dst, srcInfo.Mode().Perm()|0700); err != nil {
		return fmt.Errorf("failed to create destination %s: %w", dst, err)
	}
	dirs := []dirMeta{{src: src, dst: dst, mode: srcInfo.Mode()}}

	err = filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
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
			if err := os.MkdirAll(dstPath, info.Mode().Perm()|0700); err != nil {
				return err
			}
			dirs = append(dirs, dirMeta{src: path, dst: dstPath, mode: info.Mode()})
			return nil
		}

		// Handle named pipes (FIFOs) without blocking on os.Open.
		if info.Mode()&os.ModeNamedPipe != 0 {
			if err := unix.Mkfifo(dstPath, uint32(info.Mode().Perm())); err != nil {
				return fmt.Errorf("failed to create FIFO %s: %w", dstPath, err)
			}
			return os.Chmod(dstPath, info.Mode())
		}

		// Guard against other special files (sockets, device nodes) that cannot
		// be copied with os.Open/io.Copy.
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cannot copy unsupported special file %s (mode %s)", path, info.Mode())
		}

		// Handle regular files.
		return CopyFilePreserveXattrs(path, dstPath)
	})
	if err != nil {
		return err
	}

	for i := len(dirs) - 1; i >= 0; i-- {
		if err := copyXattrs(dirs[i].src, dirs[i].dst); err != nil {
			return err
		}
		if err := os.Chmod(dirs[i].dst, dirs[i].mode); err != nil {
			return fmt.Errorf("failed to chmod destination %s: %w", dirs[i].dst, err)
		}
	}
	return nil
}

// copyXattrs copies extended attributes (including security.capability and
// user.*) from src to dst without following symlinks.
func copyXattrs(src, dst string) error {
	xattrs, err := readXattrs(src, false)
	if err != nil || len(xattrs) == 0 {
		return nil
	}
	for _, xa := range xattrs {
		name := strings.TrimSuffix(string(xa.Name), "\x00")
		if err := sysLsetxattr(dst, name, xa.Value, 0); err != nil {
			return fmt.Errorf("failed to set xattr %q on %s: %w", name, dst, err)
		}
	}
	return nil
}

// CopyFilePreserveXattrs copies a file from src to dst, preserving permissions
// and extended attributes (xattrs). This is equivalent to "cp -a" for regular files.
func CopyFilePreserveXattrs(src, dst string) (retErr error) {
	srcStat, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !srcStat.Mode().IsRegular() {
		return fmt.Errorf("cannot copy non-regular file %s (mode %s)", src, srcStat.Mode())
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}
	if !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("cannot copy non-regular file %s (mode %s)", src, srcInfo.Mode())
	}
	if dstInfo, statErr := os.Stat(dst); statErr == nil {
		if os.SameFile(srcInfo, dstInfo) {
			return nil
		}
		if !dstInfo.Mode().IsRegular() {
			return fmt.Errorf("cannot overwrite non-regular destination %s (mode %s)", dst, dstInfo.Mode())
		}
	}

	// Create destination with owner rw so non-root copies can still set user.*
	// extended attributes on read-only (e.g. 0444/0400) source files, then
	// apply exact source permissions and special bits after copying xattrs.
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode().Perm()|0600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			if cerr := dstFile.Close(); cerr != nil && retErr == nil {
				retErr = cerr
			}
		}
	}()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}
	closed = true
	if err := dstFile.Close(); err != nil {
		return err
	}

	// Copy extended attributes (includes security.capability and user.*).
	if err := copyXattrs(src, dst); err != nil {
		return err
	}

	// Preserve exact permissions and special bits regardless of umask or pre-existing dst.
	if err := os.Chmod(dst, srcInfo.Mode()); err != nil {
		return err
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
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return fmt.Errorf("cp --reflink=auto failed: %w: %s", err, msg)
		}
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
