package ostree

import (
	"sort"
	"strings"

	"matrixos/vector/lib/filesystems"
)

const (
	// EtcActionAdd means the file was added upstream and will appear in /etc.
	EtcActionAdd EtcChangeAction = "add"
	// EtcActionUpdate means upstream modified the file and the user did not;
	// the file in /etc will be replaced with the new version.
	EtcActionUpdate EtcChangeAction = "update"
	// EtcActionRemove means upstream removed the file and the user did not
	// modify it; the file will be removed from /etc.
	EtcActionRemove EtcChangeAction = "remove"
	// EtcActionConflict means both upstream and the user changed the file
	// (or one side added/removed while the other modified); manual
	// resolution is required.
	EtcActionConflict EtcChangeAction = "conflict"
	// EtcActionUserOnly means the user made a change that upstream did not
	// touch; the file in /etc stays as-is.
	EtcActionUserOnly EtcChangeAction = "user-only"
)

// EtcChangeAction describes what will happen to a file in /etc during merge.
type EtcChangeAction string

// EtcChange describes a single change detected by the 3-way /etc diff.
type EtcChange struct {
	Path   string                // Relative path within /etc (e.g. "conf.d/foo")
	Action EtcChangeAction       // What will happen to this path
	Old    *filesystems.PathInfo // State in old commit (nil if absent)
	New    *filesystems.PathInfo // State in new commit (nil if absent)
	User   *filesystems.PathInfo // Current live state (nil if absent)
}

func addIndexedPathInfo(m map[string]*filesystems.PathInfo, pi *filesystems.PathInfo, prefix string) {
	rel := strings.TrimPrefix(pi.Path, prefix)
	rel = strings.TrimPrefix(rel, "/")
	if rel != "" {
		m[rel] = pi
	}
}

// indexPathInfoSlice builds a map from relative path to *PathInfo.
// It strips the given prefix from each entry's Path and skips the root
// directory itself (empty relative path after stripping).
func indexPathInfoSlice(items *[]filesystems.PathInfo, prefix string) map[string]*filesystems.PathInfo {
	if items == nil {
		return map[string]*filesystems.PathInfo{}
	}
	m := make(map[string]*filesystems.PathInfo, len(*items))
	for i := range *items {
		addIndexedPathInfo(m, &(*items)[i], prefix)
	}
	return m
}

// indexPathInfoPtrSlice is like indexPathInfoSlice but for []*PathInfo.
func indexPathInfoPtrSlice(items []*filesystems.PathInfo, prefix string) map[string]*filesystems.PathInfo {
	m := make(map[string]*filesystems.PathInfo, len(items))
	for _, pi := range items {
		addIndexedPathInfo(m, pi, prefix)
	}
	return m
}

// computeEtcDiff performs a 3-way diff between the old pristine /usr/etc,
// the new pristine /usr/etc, and the user's live /etc.
//
// The algorithm keys every entry by its relative path (e.g. "conf.d/foo")
// and classifies each path into one of the EtcChangeAction categories.
func computeEtcDiff(
	oldContent *[]filesystems.PathInfo,
	newContent *[]filesystems.PathInfo,
	userContent []*filesystems.PathInfo,
) []EtcChange {
	oldMap := indexPathInfoSlice(oldContent, "/usr/etc")
	newMap := indexPathInfoSlice(newContent, "/usr/etc")
	userMap := indexPathInfoPtrSlice(userContent, "/etc")

	// Collect every unique relative path.
	allPaths := make(map[string]struct{})
	for k := range oldMap {
		allPaths[k] = struct{}{}
	}
	for k := range newMap {
		allPaths[k] = struct{}{}
	}
	for k := range userMap {
		allPaths[k] = struct{}{}
	}

	var changes []EtcChange
	for relPath := range allPaths {
		change := classifyEtcChange(relPath, oldMap[relPath], newMap[relPath], userMap[relPath])
		if change != nil {
			changes = append(changes, *change)
		}
	}

	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Path < changes[j].Path
	})
	return changes
}

// classifyEtcChange determines the action for a single path given its state
// in the old commit, new commit, and user's live filesystem.
//
// Truth table (✓ = present, ✗ = absent):
//
//	old   new   user  | result
//	───── ───── ───── | ─────────────────────────────────────────────
//	 ✓     ✓     ✓   | old==new && old==user → skip (unchanged)
//	                  | old==new && old!=user → user-only
//	                  | old!=new && old==user → update
//	                  | old!=new && old!=user → conflict (unless new==user → skip)
//	 ✗     ✓     ✗   | add
//	 ✗     ✓     ✓   | new==user → skip, else conflict
//	 ✓     ✗     ✓   | old==user → remove, else conflict
//	 ✓     ✗     ✗   | skip (both removed)
//	 ✓     ✓     ✗   | old==new → user-only, else conflict
//	 ✗     ✗     ✓   | user-only
func classifyEtcChange(relPath string, old, new_, user *filesystems.PathInfo) *EtcChange {
	hasOld := old != nil
	hasNew := new_ != nil
	hasUser := user != nil

	switch {
	case hasOld && hasNew && hasUser:
		oldEqNew := old.Equals(new_)
		oldEqUser := old.Equals(user)

		switch {
		case oldEqNew && oldEqUser:
			return nil // unchanged everywhere
		case oldEqNew:
			// upstream unchanged, user modified
			return &EtcChange{Path: relPath, Action: EtcActionUserOnly, Old: old, New: new_, User: user}
		case oldEqUser:
			// upstream modified, user unchanged
			return &EtcChange{Path: relPath, Action: EtcActionUpdate, Old: old, New: new_, User: user}
		default:
			// both modified
			if new_.Equals(user) {
				return nil // converged to the same state
			}
			return &EtcChange{Path: relPath, Action: EtcActionConflict, Old: old, New: new_, User: user}
		}

	case !hasOld && hasNew && !hasUser:
		// upstream added, user doesn't have it
		return &EtcChange{Path: relPath, Action: EtcActionAdd, New: new_}

	case !hasOld && hasNew && hasUser:
		// upstream added AND user has it
		if new_.Equals(user) {
			return nil
		}
		return &EtcChange{Path: relPath, Action: EtcActionConflict, New: new_, User: user}

	case hasOld && !hasNew && hasUser:
		// upstream removed, user still has it
		if old.Equals(user) {
			return &EtcChange{Path: relPath, Action: EtcActionRemove, Old: old, User: user}
		}
		return &EtcChange{Path: relPath, Action: EtcActionConflict, Old: old, User: user}

	case hasOld && !hasNew && !hasUser:
		// both removed
		return nil

	case hasOld && hasNew && !hasUser:
		// user removed it
		if old.Equals(new_) {
			return &EtcChange{Path: relPath, Action: EtcActionUserOnly, Old: old, New: new_}
		}
		// upstream changed, user removed → conflict
		return &EtcChange{Path: relPath, Action: EtcActionConflict, Old: old, New: new_}

	case !hasOld && !hasNew && hasUser:
		// user added, not in old or new
		return &EtcChange{Path: relPath, Action: EtcActionUserOnly, User: user}

	default:
		return nil
	}
}

func (o *Ostree) ListEtcChanges(aCommit, bCommit string) ([]EtcChange, error) {
	oldEtcContent, err := o.ListContents(aCommit, "/usr/etc")
	if err != nil {
		return nil, err
	}
	newEtcContent, err := o.ListContents(bCommit, "/usr/etc")
	if err != nil {
		return nil, err
	}
	userEtcContent, err := filesystems.ListContents("/etc")
	if err != nil {
		return nil, err
	}

	changes := computeEtcDiff(oldEtcContent, newEtcContent, userEtcContent)
	return changes, nil
}
