package ostree

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"matrixos/vector/lib/filesystems"
)

const (
	// RwVdbPath is the rw path to the Portage package database.
	RwVdbPath = "/var/db/pkg"
)

// ParseModeString takes a hybrid string like "-00644" and parses it.
func ParseModeString(input string) (*filesystems.PathMode, error) {
	if len(input) < 4 {
		return nil, fmt.Errorf("input too short to be valid mode string: %q", input)
	}

	mode := filesystems.PathMode{
		Type: string(input[0]),
	}

	// Extract the octal portion.
	// strconv.ParseUint inherently understands base 8 if we specify it.
	rawPerms, err := strconv.ParseUint(input[1:], 8, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse octal permissions: %w", err)
	}

	// Define POSIX bitmasks (using Go's 0o prefix for octal literals)
	const (
		posixSetUID = 0o4000
		posixSetGID = 0o2000
		posixSticky = 0o1000
		posixPerms  = 0o0777 // Mask for standard rwxrwxrwx
	)

	// Extract special bits via bitwise AND
	mode.SetUID = (rawPerms & posixSetUID) != 0
	mode.SetGID = (rawPerms & posixSetGID) != 0
	mode.Sticky = (rawPerms & posixSticky) != 0

	// Extract standard permissions
	mode.Perms = fs.FileMode(rawPerms & posixPerms)

	return &mode, nil
}

// ParseOstreeLsChecksumLine parses a line from `ostree ls -C` output into a PathInfo struct.
func ParseOstreeLsChecksumLine(line string) (*filesystems.PathInfo, error) {
	parts := strings.Fields(line)
	if len(parts) < 6 {
		return nil, fmt.Errorf("unexpected format for ostree ls line: %q", line)
	}
	idx := 0

	pi := &filesystems.PathInfo{}
	mode, err := ParseModeString(parts[idx])
	if err != nil {
		return nil, err
	}
	pi.Mode = mode
	idx++

	uid, gid := parts[idx], parts[idx+1]
	idx += 2

	pi.Uid, err = strconv.ParseUint(uid, 10, 32)
	if err != nil {
		return nil, err
	}

	pi.Gid, err = strconv.ParseUint(gid, 10, 32)
	if err != nil {
		return nil, err
	}

	pi.Size, err = strconv.ParseUint(parts[idx], 10, 64)
	if err != nil {
		return nil, err
	}
	idx++

	if pi.Mode.Type == "d" {
		// Directories have two checksums, use the second one.
		idx++
	}
	if idx+1 >= len(parts) {
		return nil, fmt.Errorf("unexpected format for ostree ls line: %q", line)
	}

	pi.OSTreeChecksum = parts[idx]
	idx++

	pi.Path = parts[idx]
	idx++
	if pi.Mode.Type == "l" && idx+1 < len(parts) && parts[idx] == "->" {
		idx++
		pi.Link = parts[idx]
	}
	return pi, nil
}

func (o *Ostree) ListContents(commit, path string) (*[]filesystems.PathInfo, error) {
	if commit == "" {
		return nil, errors.New("missing commit parameter")
	}
	if path == "" {
		return nil, errors.New("missing path parameter")
	}
	repoDir, err := o.RepoDir()
	if err != nil {
		return nil, err
	}
	return o.listContentsOfPath(commit, repoDir, path)
}

func (o *Ostree) listContentsOfPath(commit, repoDir, path string) (*[]filesystems.PathInfo, error) {
	stdout, err := o.ostreeRunCapture(
		"--repo="+repoDir,
		"ls",
		"-C",
		"-R",
		commit,
		"--",
		path,
	)
	if err != nil {
		return nil, err
	}

	var pis []filesystems.PathInfo

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		pi, err := ParseOstreeLsChecksumLine(line)
		if err != nil {
			return nil, err
		}
		pis = append(pis, *pi)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return &pis, nil
}

func (o *Ostree) ListPackages(commit string) ([]string, error) {
	if commit == "" {
		return nil, errors.New("missing commit parameter")
	}
	root, err := o.Root()
	if err != nil {
		return nil, err
	}

	roVdb, err := o.cfg.GetItem("Releaser.ReadOnlyVdb")
	if err != nil {
		return nil, err
	}
	if roVdb == "" {
		return nil, fmt.Errorf("config item Releaser.ReadOnlyVdb is not set")
	}

	pkgs, err := o.listPackagesFromPath(root, roVdb, commit)
	if err == nil && len(pkgs) > 0 {
		return pkgs, nil
	}
	return o.listPackagesFromPath(root, RwVdbPath, commit)
}

func (o *Ostree) listPackagesFromPath(root, path, commit string) ([]string, error) {
	repoDir := filepath.Join(root, "ostree", "repo")
	// path is inside the OSTree commit tree (e.g. /var/db/pkg), NOT on the host root.
	vardbpkg := filepath.Clean("/" + path)

	pis, err := o.listContentsOfPath(commit, repoDir, vardbpkg)
	if err != nil {
		return nil, err
	}

	var pkgs []string

	prefix := vardbpkg
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	for _, pi := range *pis {
		if pi.Mode.Type != "d" {
			continue
		}
		if !strings.HasPrefix(pi.Path, prefix) {
			continue
		}

		relPath := strings.TrimPrefix(pi.Path, prefix)
		relPath = strings.TrimSuffix(relPath, "/")

		if strings.Count(relPath, "/") == 1 {
			pkgs = append(pkgs, relPath)
		}
	}

	sort.Strings(pkgs)
	return pkgs, nil
}
