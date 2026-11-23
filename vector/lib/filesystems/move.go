package filesystems

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Move moves src to dst. It first attempts os.Rename which is atomic but only
// works within the same filesystem. When the source and destination reside on
// different devices (EXDEV), it falls back to a copy-then-remove strategy:
//
//   - Files are copied to a temporary file in the destination directory and
//     then atomically renamed to the final path, so dst is never left in a
//     partial state. The source is removed only after the rename succeeds.
//   - Directories are copied recursively with CopyDirPreserve (preserving
//     permissions, xattrs and symlinks) and the source tree is removed
//     afterwards.
func Move(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}

	// If rename failed because the destination parent doesn't exist,
	// create it and retry (still same-device), but only when src actually
	// exists and dst has a valid target name.
	if errors.Is(err, syscall.ENOENT) {
		if srcInfo, srcErr := os.Lstat(src); srcErr == nil {
			if cleanDst, vErr := validateCrossDeviceDst(dst, srcInfo.IsDir()); vErr == nil {
				dstDir := filepath.Dir(cleanDst)
				if _, statErr := os.Stat(dstDir); os.IsNotExist(statErr) {
					if mkErr := os.MkdirAll(dstDir, 0755); mkErr != nil {
						return fmt.Errorf("failed to create destination directory %s: %w", dstDir, mkErr)
					}
					err = os.Rename(src, dst)
					if err == nil {
						return nil
					}
					if !isCrossDeviceError(err) {
						return fmt.Errorf("failed to move %s to %s: %w", src, dst, err)
					}
					// If the retry got EXDEV, fall through to the cross-device path.
				}
			}
		}
	}

	// Only fall back for cross-device errors.
	if !isCrossDeviceError(err) {
		return fmt.Errorf("failed to move %s to %s: %w", src, dst, err)
	}

	return moveCrossDevice(src, dst)
}

// validateCrossDeviceDst validates dst before any staging directories are
// created on the destination filesystem, rejecting trailing slashes on
// non-directories, root "/", and "." / ".." final path components (which POSIX
// rename(2) forbids) without lexically collapsing "/." or "/.." away.
func validateCrossDeviceDst(dst string, isDir bool) (string, error) {
	if dst == "" {
		return "", syscall.ENOENT
	}
	if !isDir && strings.HasSuffix(dst, string(filepath.Separator)) {
		return "", syscall.ENOTDIR
	}
	trimmed := strings.TrimRight(dst, string(filepath.Separator))
	if trimmed == "" {
		return "", syscall.EBUSY
	}
	lastComp := trimmed
	if idx := strings.LastIndexByte(trimmed, filepath.Separator); idx >= 0 {
		lastComp = trimmed[idx+1:]
	}
	if lastComp == "." || lastComp == ".." {
		return "", syscall.EINVAL
	}
	return trimmed, nil
}

// isSameEntry reports whether dst already exists as the same directory entry
// or hard-linked inode as srcInfo (via Lstat so symlinks compare their own inode).
func isSameEntry(srcInfo os.FileInfo, dst string) bool {
	dstInfo, err := os.Lstat(dst)
	return err == nil && os.SameFile(srcInfo, dstInfo)
}

// canonicalAbs resolves symlinks on the deepest existing ancestor of p and
// returns a clean absolute path for directory containment checks.
func canonicalAbs(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = filepath.Clean(p)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	dir := filepath.Dir(abs)
	if dir != abs {
		return filepath.Join(canonicalAbs(dir), filepath.Base(abs))
	}
	return abs
}

// isSameOrSubpath reports whether dst equals src or lies beneath src.
func isSameOrSubpath(src, dst string) bool {
	cSrc := canonicalAbs(src)
	cDst := canonicalAbs(dst)
	if cSrc == cDst {
		return true
	}
	if cSrc == string(filepath.Separator) {
		return strings.HasPrefix(cDst, string(filepath.Separator))
	}
	return strings.HasPrefix(cDst, cSrc+string(filepath.Separator))
}

func moveCrossDevice(src, dst string) error {
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %w", src, err)
	}

	cleanDst, err := validateCrossDeviceDst(dst, srcInfo.IsDir())
	if err != nil {
		return fmt.Errorf("failed to move %s to %s: %w", src, dst, err)
	}
	if isSameEntry(srcInfo, cleanDst) {
		return nil
	}

	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return moveSymlinkCrossDevice(src, cleanDst)
	}
	if srcInfo.IsDir() {
		return moveDirCrossDevice(src, cleanDst)
	}
	if srcInfo.Mode()&os.ModeNamedPipe != 0 {
		return moveFifoCrossDevice(src, cleanDst, srcInfo.Mode())
	}
	if !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("cannot move unsupported special file %s (mode %s)", src, srcInfo.Mode())
	}
	return moveFileCrossDevice(src, cleanDst)
}

// moveFifoCrossDevice recreates a named pipe (FIFO) at dst atomically without
// opening it (which would block waiting for a peer), then removes src.
func moveFifoCrossDevice(src, dst string, mode os.FileMode) error {
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", dstDir, err)
	}
	tmpDir, err := os.MkdirTemp(dstDir, ".move-fifo-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir for FIFO in %s: %w", dstDir, err)
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, "fifo")
	if err := unix.Mkfifo(tmpPath, uint32(mode.Perm())); err != nil {
		return fmt.Errorf("failed to create FIFO %s: %w", dst, err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("failed to chmod FIFO %s: %w", dst, err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("failed to place FIFO %s: %w", dst, err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("failed to remove source FIFO %s after move: %w", src, err)
	}
	return nil
}

// moveSymlinkCrossDevice recreates a symlink at dst atomically without
// following it, then removes the source symlink.
func moveSymlinkCrossDevice(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return fmt.Errorf("failed to read symlink %s: %w", src, err)
	}
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", dstDir, err)
	}
	// Create the symlink inside a private 0700 temp directory in dstDir, then
	// atomically rename it onto dst without any remove-then-symlink TOCTOU window.
	tmpDir, err := os.MkdirTemp(dstDir, ".move-link-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir for symlink in %s: %w", dstDir, err)
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, "link")
	if err := os.Symlink(target, tmpPath); err != nil {
		return fmt.Errorf("failed to create symlink %s -> %s: %w", dst, target, err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("failed to place symlink %s: %w", dst, err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("failed to remove source symlink %s after move: %w", src, err)
	}
	return nil
}

// moveFileCrossDevice copies a file across devices using a temporary file
// followed by an atomic rename, then removes the source.
func moveFileCrossDevice(src, dst string) error {
	dstDir := filepath.Dir(dst)
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory %s: %w", dstDir, err)
	}

	if err := copyFileAtomic(src, dst, ".move-*.tmp"); err != nil {
		return fmt.Errorf("failed to copy %s to %s: %w", src, dst, err)
	}

	// Source removal – best effort after a successful copy.
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("failed to remove source %s after move: %w", src, err)
	}

	return nil
}

// removeTree removes root and its contents, restoring owner rwx permissions
// on subdirectories if needed so non-root callers can remove read-only
// (e.g. 0555/0500) source or staging directory trees cleanly.
func removeTree(root string) error {
	if err := os.RemoveAll(root); err == nil {
		return nil
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(path, 0700)
		}
		return nil
	})
	return os.RemoveAll(root)
}

// moveDirCrossDevice copies a directory into a private staging directory in
// the destination's parent directory, atomically renames it into place, and
// removes the source only after placement succeeds.
func moveDirCrossDevice(src, dst string) error {
	if err := CheckDirIsRoot(src); err != nil {
		return err
	}
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %w", src, err)
	}
	cleanDst, err := validateCrossDeviceDst(dst, true)
	if err != nil {
		return fmt.Errorf("failed to move %s to %s: %w", src, dst, err)
	}
	if isSameEntry(srcInfo, cleanDst) {
		return nil
	}
	if isSameOrSubpath(src, cleanDst) {
		return fmt.Errorf("cannot move directory %s into itself (%s): %w", src, dst, syscall.EINVAL)
	}
	dstParent := filepath.Dir(cleanDst)
	if err := os.MkdirAll(dstParent, 0755); err != nil {
		return fmt.Errorf("failed to create parent of destination %s: %w", cleanDst, err)
	}

	// Stage directly inside dstParent so os.Rename moves within the same
	// parent directory and succeeds as non-root even on 0555/0500 source dirs.
	stageDst, err := os.MkdirTemp(dstParent, ".move-dir-*")
	if err != nil {
		return fmt.Errorf("failed to create staging dir in %s: %w", dstParent, err)
	}
	placed := false
	defer func() {
		if !placed {
			_ = removeTree(stageDst)
		}
	}()

	if err := CopyDirPreserve(src, stageDst); err != nil {
		return fmt.Errorf("failed to copy directory %s to %s: %w", src, cleanDst, err)
	}

	if err := os.Rename(stageDst, cleanDst); err != nil {
		return fmt.Errorf("failed to place directory %s at %s: %w", src, cleanDst, err)
	}
	placed = true

	if err := removeTree(src); err != nil {
		return fmt.Errorf("failed to remove source directory %s after move: %w", src, err)
	}

	return nil
}

// isCrossDeviceError reports whether err (possibly wrapped) contains the
// EXDEV (cross-device link) errno.
func isCrossDeviceError(err error) bool {
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return errors.Is(linkErr.Err, syscall.EXDEV)
	}
	return errors.Is(err, syscall.EXDEV)
}
