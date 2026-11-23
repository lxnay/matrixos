package filesystems

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ResolvePathInRoot resolves virtual path p strictly inside chroot root and
// returns its host path and os.FileInfo using the host OS filesystem.
func ResolvePathInRoot(root, p string) (string, os.FileInfo, error) {
	return ResolvePathInRootWith(root, p, os.Lstat, os.Stat, os.Readlink)
}

// ResolvePathInRootWith resolves virtual path p strictly inside chroot root
// using injected lstat/stat/readlink functions (allowing mock runners in tests).
// It walks path components in POSIX order (expanding symlinks before applying
// "..", enforcing directory-ness on intermediate components and trailing
// slashes, clamping ".." at virtual "/", and re-rooting absolute symlink
// targets inside root), including when root == "/".
func ResolvePathInRootWith(
	root, p string,
	lstatFn func(string) (os.FileInfo, error),
	statFn func(string) (os.FileInfo, error),
	readlinkFn func(string) (string, error),
) (string, os.FileInfo, error) {
	if root == "" {
		return "", nil, &os.PathError{Op: "stat", Path: root, Err: syscall.ENOENT}
	}
	if p == "" {
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
	var pending []string
	for _, seg := range strings.Split(p, "/") {
		if seg != "" {
			pending = append(pending, seg)
		}
	}

	const maxSymlinkHops = 40
	var resolved []string
	var lastFi os.FileInfo
	hops := 0

	for len(pending) > 0 {
		comp := pending[0]
		pending = pending[1:]
		if comp == "" || comp == "." {
			continue
		}
		if comp == ".." {
			if len(resolved) > 0 {
				resolved = resolved[:len(resolved)-1]
			}
			lastFi = nil
			continue
		}

		next := make([]string, len(resolved)+1)
		copy(next, resolved)
		next[len(resolved)] = comp

		hostPath := filepath.Join(append([]string{root}, next...)...)
		fi, err := lstatFn(hostPath)
		if err != nil {
			return "", nil, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			hops++
			if hops > maxSymlinkHops {
				return "", nil, fmt.Errorf("too many levels of symbolic links resolving %s under %s", p, root)
			}
			target, err := readlinkFn(hostPath)
			if err != nil {
				return "", nil, err
			}
			if len(pending) == 0 && strings.HasSuffix(target, "/") {
				requireDir = true
			}
			var targetParts []string
			for _, seg := range strings.Split(target, "/") {
				if seg != "" {
					targetParts = append(targetParts, seg)
				}
			}
			if strings.HasPrefix(target, "/") {
				resolved = nil
			}
			lastFi = nil
			pending = append(targetParts, pending...)
			continue
		}
		if len(pending) > 0 && !fi.IsDir() {
			return "", nil, &os.PathError{Op: "stat", Path: hostPath, Err: syscall.ENOTDIR}
		}
		resolved = next
		lastFi = fi
	}

	var finalPath string
	var err error
	if len(resolved) == 0 {
		finalPath = root
		lastFi, err = statFn(root)
		if err != nil {
			return "", nil, err
		}
		if !lastFi.IsDir() {
			return "", nil, &os.PathError{Op: "stat", Path: finalPath, Err: syscall.ENOTDIR}
		}
	} else {
		finalPath = filepath.Join(append([]string{root}, resolved...)...)
		if lastFi == nil {
			lastFi, err = lstatFn(finalPath)
			if err != nil {
				return "", nil, err
			}
		}
	}
	if requireDir && !lastFi.IsDir() {
		return "", nil, &os.PathError{Op: "stat", Path: finalPath, Err: syscall.ENOTDIR}
	}
	return finalPath, lastFi, nil
}
