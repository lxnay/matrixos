package filesystems

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	// mountInfoPath is the path to the mountinfo file.
	mountInfoPath = "/proc/self/mountinfo"

	// ReadMountInfo reads and parses the system mount info. Replaceable for testing.
	ReadMountInfo = defaultReadMountInfo

	// blkidLookup queries a device attribute via blkid. Replaceable for testing.
	blkidLookup = defaultBlkidLookup

	// triggerAutomount forces a kernel autofs mountpoint to resolve to its real
	// backing filesystem by accessing the path. Replaceable for testing.
	triggerAutomount = defaultTriggerAutomount
)

// MountInfoEntry represents a parsed line from /proc/self/mountinfo.
type MountInfoEntry struct {
	MountID    int
	ParentID   int
	Major      int
	Minor      int
	Root       string
	Mountpoint string
	Options    string
	FSType     string
	Source     string
	SuperOpts  string
}

// String returns a human-readable representation of a MountInfoEntry.
func (e *MountInfoEntry) String() string {
	return fmt.Sprintf("TARGET=%s SOURCE=%s FSTYPE=%s OPTIONS=%s",
		e.Mountpoint, e.Source, e.FSType, e.Options)
}

func defaultReadMountInfo() ([]*MountInfoEntry, error) {
	return parseMountInfoFile(mountInfoPath)
}

// parseMountInfoFile parses a mountinfo-formatted file.
func parseMountInfoFile(path string) ([]*MountInfoEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []*MountInfoEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		entry, err := parseMountInfoLine(line)
		if err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// parseMountInfoLine parses a single line from /proc/self/mountinfo.
// Format: mount_id parent_id major:minor root mountpoint options [optional...] - fstype source super_options
func parseMountInfoLine(line string) (*MountInfoEntry, error) {
	parts := strings.SplitN(line, " - ", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("malformed mountinfo line: no separator")
	}

	left := strings.Fields(parts[0])
	right := strings.Fields(parts[1])

	if len(left) < 6 {
		return nil, fmt.Errorf("malformed mountinfo line: %d left fields, need >= 6", len(left))
	}
	if len(right) < 2 {
		return nil, fmt.Errorf("malformed mountinfo line: %d right fields, need >= 2", len(right))
	}

	mountID, _ := strconv.Atoi(left[0])
	parentID, _ := strconv.Atoi(left[1])

	var major, minor int
	if _, err := fmt.Sscanf(left[2], "%d:%d", &major, &minor); err != nil {
		return nil, fmt.Errorf("malformed major:minor field: %s", left[2])
	}

	entry := &MountInfoEntry{
		MountID:    mountID,
		ParentID:   parentID,
		Major:      major,
		Minor:      minor,
		Root:       unescapeOctal(left[3]),
		Mountpoint: unescapeOctal(left[4]),
		Options:    left[5],
		FSType:     right[0],
		Source:     right[1],
	}
	if len(right) >= 3 {
		entry.SuperOpts = right[2]
	}

	return entry, nil
}

// unescapeOctal decodes octal escapes in mountinfo fields.
// Common escapes: \040 (space), \011 (tab), \012 (newline), \134 (backslash).
func unescapeOctal(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			o1 := s[i+1] - '0'
			o2 := s[i+2] - '0'
			o3 := s[i+3] - '0'
			if o1 <= 7 && o2 <= 7 && o3 <= 7 {
				b.WriteByte(o1*64 + o2*8 + o3)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// isPathUnderMount returns true if path equals mountpoint or is a descendant.
func isPathUnderMount(path, mountpoint string) bool {
	if mountpoint == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == mountpoint || strings.HasPrefix(path, mountpoint+"/")
}

// resolvePath returns the canonical path with symlinks resolved.
// If resolution fails (e.g. the path does not exist), it falls back
// to filepath.Clean so callers always get a normalised path.
func resolvePath(p string) string {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return resolved
}

// findMountByTarget returns the mount entry for an exact mountpoint match.
// When multiple mounts exist at the same target, the last (most recent) is returned.
// The path is resolved through symlinks before comparison because the kernel
// records the real (resolved) path in /proc/self/mountinfo.
func findMountByTarget(mnt string) (*MountInfoEntry, error) {
	entries, err := ReadMountInfo()
	if err != nil {
		return nil, err
	}
	resolved := resolvePath(mnt)
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Mountpoint == resolved {
			return entries[i], nil
		}
	}
	return nil, fmt.Errorf("no mount found for target %s", mnt)
}

// findMountContainingPath returns the entry whose mountpoint is the longest
// prefix of path (equivalent to findmnt -T <path>).
// The path is resolved through symlinks before comparison.
//
// When the best match is a systemd autofs stub (FSType=autofs, Source=systemd-1),
// as installed by systemd-gpt-auto-generator for /boot and /efi on systemd-boot
// systems, the path is touched to trigger the automount and mountinfo is
// re-scanned so the real backing filesystem entry is returned instead.
func findMountContainingPath(path string) (*MountInfoEntry, error) {
	entries, err := ReadMountInfo()
	if err != nil {
		return nil, err
	}
	resolved := resolvePath(path)
	best := bestMountForPath(entries, resolved)
	if best != nil && best.FSType == "autofs" {
		triggerAutomount(resolved)
		if refreshed, rErr := ReadMountInfo(); rErr == nil {
			if b := bestMountForPath(refreshed, resolved); b != nil {
				best = b
			}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no mount found containing path %s", path)
	}
	return best, nil
}

// bestMountForPath returns the entry whose mountpoint is the longest prefix
// of resolved. On ties (same mountpoint), a non-autofs entry is preferred
// over an autofs stub so the real backing filesystem wins.
func bestMountForPath(entries []*MountInfoEntry, resolved string) *MountInfoEntry {
	var best *MountInfoEntry
	bestLen := -1
	for i := range entries {
		mp := entries[i].Mountpoint
		if !isPathUnderMount(resolved, mp) {
			continue
		}
		switch {
		case len(mp) > bestLen:
			bestLen = len(mp)
			best = entries[i]
		case len(mp) == bestLen && best != nil && best.FSType == "autofs" && entries[i].FSType != "autofs":
			best = entries[i]
		}
	}
	return best
}

// defaultTriggerAutomount opens the path so that the kernel resolves any
// systemd autofs stub mounted there to its real backing filesystem. Failures
// are ignored: the caller falls back to whatever mountinfo currently reports.
func defaultTriggerAutomount(path string) {
	if f, err := os.Open(path); err == nil {
		_ = f.Close()
	}
}

// listMountsByPrefix returns entries whose mountpoint starts with prefix.
// The prefix is resolved through symlinks before comparison.
func listMountsByPrefix(prefix string) ([]*MountInfoEntry, error) {
	entries, err := ReadMountInfo()
	if err != nil {
		return nil, err
	}
	resolved := resolvePath(prefix)
	var result []*MountInfoEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Mountpoint, resolved) {
			result = append(result, e)
		}
	}
	return result, nil
}

// findMountsBySource returns entries whose Source matches source.
func findMountsBySource(source string) ([]*MountInfoEntry, error) {
	entries, err := ReadMountInfo()
	if err != nil {
		return nil, err
	}
	var result []*MountInfoEntry
	for _, e := range entries {
		if e.Source == source {
			result = append(result, e)
		}
	}
	return result, nil
}

// IsMounted returns true if there is a mount at the exact mountpoint.
func IsMounted(mnt string) (bool, error) {
	_, err := findMountByTarget(mnt)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// formatMountEntries formats mount entries for debug logging.
func formatMountEntries(entries []*MountInfoEntry) string {
	if len(entries) == 0 {
		return "(no mounts)"
	}
	var lines []string
	for _, e := range entries {
		lines = append(lines, e.String())
	}
	return strings.Join(lines, "\n")
}

// defaultBlkidLookup queries a single device attribute via blkid.
// tag is the blkid attribute name: UUID, PARTUUID, LABEL, PARTTYPE, etc.
func defaultBlkidLookup(devPath, tag string) (string, error) {
	cmd := exec.Command("blkid", "-o", "value", "-s", tag, devPath)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()

	if err != nil {
		return "", fmt.Errorf("blkid lookup failed for %s tag %s: %w", devPath, tag, err)
	}
	val := strings.TrimSpace(string(out))
	if val == "" {
		return "", fmt.Errorf("no %s found for device %s", tag, devPath)
	}
	return val, nil
}

// resolveDeviceAttribute looks up a device attribute (UUID, PARTUUID, etc.)
// for a block device using blkid. The tag parameter is the blkid attribute name
// (e.g. "UUID", "PARTUUID", "LABEL", "PARTTYPE").
func resolveDeviceAttribute(devPath, tag string) (string, error) {
	return blkidLookup(devPath, tag)
}
