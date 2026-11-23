package filesystems

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	// create it and retry (still same-device).
	if errors.Is(err, syscall.ENOENT) {
		dstDir := filepath.Dir(dst)
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

	// Only fall back for cross-device errors.
	if !isCrossDeviceError(err) {
		return fmt.Errorf("failed to move %s to %s: %w", src, dst, err)
	}

	return moveCrossDevice(src, dst)
}

func moveCrossDevice(src, dst string) error {
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("failed to stat source %s: %w", src, err)
	}

	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return moveSymlinkCrossDevice(src, dst)
	}
	if srcInfo.IsDir() {
		return moveDirCrossDevice(src, dst)
	}
	if srcInfo.Mode()&os.ModeNamedPipe != 0 {
		return moveFifoCrossDevice(src, dst, srcInfo.Mode())
	}
	if !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("cannot move unsupported special file %s (mode %s)", src, srcInfo.Mode())
	}
	return moveFileCrossDevice(src, dst)
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

// moveDirCrossDevice copies a directory across devices and removes the source.
func moveDirCrossDevice(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("failed to create parent of destination %s: %w", dst, err)
	}

	if err := CopyDirPreserve(src, dst); err != nil {
		return fmt.Errorf("failed to copy directory %s to %s: %w", src, dst, err)
	}

	if err := os.RemoveAll(src); err != nil {
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
