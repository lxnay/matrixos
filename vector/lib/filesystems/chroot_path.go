package filesystems

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// CanonicalPath returns a canonical absolute path with symlinks resolved in
// POSIX order. When p does not yet exist, symlinks on its nearest existing
// ancestor are resolved and the remaining components are preserved.
func CanonicalPath(p string) string {
	if p == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		if filepath.IsAbs(resolved) {
			return resolved
		}
		if abs, err := filepath.Abs(resolved); err == nil {
			return CanonicalPath(abs)
		}
		return resolved
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if dir := filepath.Dir(abs); dir != abs {
		return filepath.Join(CanonicalPath(dir), filepath.Base(abs))
	}
	return abs
}

// ResolvePathInRoot resolves virtual path p strictly inside chroot root and
// returns its host path and os.FileInfo.
func ResolvePathInRoot(root, p string) (string, os.FileInfo, error) {
	return ResolvePathInRootWith(root, p, os.Lstat, os.Stat, os.Readlink)
}

// ResolvePathInRootWith resolves virtual path p strictly inside chroot root
// using injected lstat/stat/readlink functions (allowing mock runners in tests).
// Symlinks are expanded in POSIX order with absolute targets re-rooted at root
// and ".." clamped at virtual "/".
func ResolvePathInRootWith(
	root, p string,
	lstatFn, statFn func(string) (os.FileInfo, error),
	readlinkFn func(string) (string, error),
) (string, os.FileInfo, error) {
	if root == "" || p == "" {
		return "", nil, &os.PathError{Op: "stat", Path: p, Err: syscall.ENOENT}
	}
	if lstatFn == nil {
		lstatFn = os.Lstat
	}
	if statFn == nil {
		statFn = os.Stat
	}
	if readlinkFn == nil {
		readlinkFn = os.Readlink
	}
	root = filepath.Clean(root)

	requireDir := strings.HasSuffix(p, "/")
	pending := splitPathSegments(p)
	var resolved []string
	var lastFi os.FileInfo

	for hops := 0; len(pending) > 0; {
		comp := pending[0]
		pending = pending[1:]
		if comp == "." {
			continue
		}
		if comp == ".." {
			if len(resolved) > 0 {
				resolved = resolved[:len(resolved)-1]
			}
			lastFi = nil
			continue
		}

		hostPath := filepath.Join(root, filepath.Join(resolved...), comp)
		fi, err := lstatFn(hostPath)
		if err != nil {
			return "", nil, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			hops++
			if hops > 40 {
				return "", nil, fmt.Errorf("too many levels of symbolic links resolving %s under %s", p, root)
			}
			target, err := readlinkFn(hostPath)
			if err != nil {
				return "", nil, err
			}
			if len(pending) == 0 && strings.HasSuffix(target, "/") {
				requireDir = true
			}
			if strings.HasPrefix(target, "/") {
				resolved = nil
			}
			lastFi = nil
			pending = append(splitPathSegments(target), pending...)
			continue
		}
		if len(pending) > 0 && !fi.IsDir() {
			return "", nil, &os.PathError{Op: "stat", Path: hostPath, Err: syscall.ENOTDIR}
		}
		resolved = append(resolved, comp)
		lastFi = fi
	}

	finalPath := filepath.Join(root, filepath.Join(resolved...))
	var err error
	if len(resolved) == 0 {
		if lastFi, err = statFn(root); err != nil {
			return "", nil, err
		}
		if !lastFi.IsDir() {
			return "", nil, &os.PathError{Op: "stat", Path: finalPath, Err: syscall.ENOTDIR}
		}
	} else if lastFi == nil {
		if lastFi, err = lstatFn(finalPath); err != nil {
			return "", nil, err
		}
	}
	if requireDir && !lastFi.IsDir() {
		return "", nil, &os.PathError{Op: "stat", Path: finalPath, Err: syscall.ENOTDIR}
	}
	return finalPath, lastFi, nil
}

func splitPathSegments(p string) []string {
	var segs []string
	for _, s := range strings.Split(p, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	return segs
}
