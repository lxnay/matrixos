package ostree

import (
	"matrixos/vector/lib/filesystems"
	"os"
	"testing"
)

// --- helpers for 3-way diff tests ---

func mkPI(path, typ string, perms uint32, uid, gid, size uint64, link string) filesystems.PathInfo {
	return filesystems.PathInfo{
		Mode: &filesystems.PathMode{Type: typ, Perms: os.FileMode(perms)},
		Uid:  uid, Gid: gid, Size: size,
		Path: path, Link: link,
	}
}

func findChange(changes []EtcChange, path string) *EtcChange {
	for i := range changes {
		if changes[i].Path == path {
			return &changes[i]
		}
	}
	return nil
}

func ptr(pi filesystems.PathInfo) *filesystems.PathInfo {
	return &pi
}

func TestComputeEtcDiffUnchanged(t *testing.T) {
	old := []filesystems.PathInfo{mkPI("/usr/etc/passwd", "-", 0644, 0, 0, 100, "")}
	new_ := []filesystems.PathInfo{mkPI("/usr/etc/passwd", "-", 0644, 0, 0, 100, "")}
	user := []*filesystems.PathInfo{ptr(mkPI("/etc/passwd", "-", 0644, 0, 0, 100, ""))}

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 0 {
		t.Errorf("Expected no changes, got %d: %+v", len(changes), changes)
	}
}

func TestComputeEtcDiffUpstreamAdd(t *testing.T) {
	old := []filesystems.PathInfo{}
	new_ := []filesystems.PathInfo{mkPI("/usr/etc/newfile", "-", 0644, 0, 0, 50, "")}
	user := []*filesystems.PathInfo{}

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	c := changes[0]
	if c.Path != "newfile" || c.Action != EtcActionAdd {
		t.Errorf("Expected add of 'newfile', got %q action=%s", c.Path, c.Action)
	}
	if c.Old != nil || c.New == nil || c.User != nil {
		t.Error("Old/User should be nil, New should be set")
	}
}

func TestComputeEtcDiffUpstreamRemove(t *testing.T) {
	old := []filesystems.PathInfo{mkPI("/usr/etc/gone", "-", 0644, 0, 0, 10, "")}
	new_ := []filesystems.PathInfo{}
	user := []*filesystems.PathInfo{ptr(mkPI("/etc/gone", "-", 0644, 0, 0, 10, ""))}

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	c := changes[0]
	if c.Path != "gone" || c.Action != EtcActionRemove {
		t.Errorf("Expected remove of 'gone', got %q action=%s", c.Path, c.Action)
	}
}

func TestComputeEtcDiffUpstreamUpdate(t *testing.T) {
	old := []filesystems.PathInfo{mkPI("/usr/etc/cfg", "-", 0644, 0, 0, 100, "")}
	new_ := []filesystems.PathInfo{mkPI("/usr/etc/cfg", "-", 0644, 0, 0, 200, "")} // size changed
	user := []*filesystems.PathInfo{ptr(mkPI("/etc/cfg", "-", 0644, 0, 0, 100, ""))}

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	c := changes[0]
	if c.Path != "cfg" || c.Action != EtcActionUpdate {
		t.Errorf("Expected update of 'cfg', got %q action=%s", c.Path, c.Action)
	}
}

func TestComputeEtcDiffUserOnly(t *testing.T) {
	old := []filesystems.PathInfo{mkPI("/usr/etc/cfg", "-", 0644, 0, 0, 100, "")}
	new_ := []filesystems.PathInfo{mkPI("/usr/etc/cfg", "-", 0644, 0, 0, 100, "")}
	user := []*filesystems.PathInfo{ptr(mkPI("/etc/cfg", "-", 0755, 0, 0, 100, ""))} // perms changed

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	c := changes[0]
	if c.Path != "cfg" || c.Action != EtcActionUserOnly {
		t.Errorf("Expected user-only of 'cfg', got %q action=%s", c.Path, c.Action)
	}
}

func TestComputeEtcDiffConflictBothModified(t *testing.T) {
	old := []filesystems.PathInfo{mkPI("/usr/etc/cfg", "-", 0644, 0, 0, 100, "")}
	new_ := []filesystems.PathInfo{mkPI("/usr/etc/cfg", "-", 0644, 0, 0, 200, "")}   // upstream size change
	user := []*filesystems.PathInfo{ptr(mkPI("/etc/cfg", "-", 0755, 0, 0, 300, ""))} // user perms+size change

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	c := changes[0]
	if c.Path != "cfg" || c.Action != EtcActionConflict {
		t.Errorf("Expected conflict of 'cfg', got %q action=%s", c.Path, c.Action)
	}
}

func TestComputeEtcDiffConverged(t *testing.T) {
	// old=A, new=B, user=B → both changed the same way → skip
	old := []filesystems.PathInfo{mkPI("/usr/etc/cfg", "-", 0644, 0, 0, 100, "")}
	new_ := []filesystems.PathInfo{mkPI("/usr/etc/cfg", "-", 0755, 0, 0, 200, "")}
	user := []*filesystems.PathInfo{ptr(mkPI("/etc/cfg", "-", 0755, 0, 0, 200, ""))}

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 0 {
		t.Errorf("Expected no changes (converged), got %d: %+v", len(changes), changes)
	}
}

func TestComputeEtcDiffBothRemoved(t *testing.T) {
	old := []filesystems.PathInfo{mkPI("/usr/etc/gone", "-", 0644, 0, 0, 10, "")}
	new_ := []filesystems.PathInfo{}
	user := []*filesystems.PathInfo{}

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 0 {
		t.Errorf("Expected no changes (both removed), got %d", len(changes))
	}
}

func TestComputeEtcDiffConflictUpstreamRemoveUserModified(t *testing.T) {
	old := []filesystems.PathInfo{mkPI("/usr/etc/cfg", "-", 0644, 0, 0, 100, "")}
	new_ := []filesystems.PathInfo{}
	user := []*filesystems.PathInfo{ptr(mkPI("/etc/cfg", "-", 0755, 0, 0, 100, ""))} // user changed perms

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	if changes[0].Action != EtcActionConflict {
		t.Errorf("Expected conflict, got %s", changes[0].Action)
	}
}

func TestComputeEtcDiffConflictUpstreamChangedUserRemoved(t *testing.T) {
	old := []filesystems.PathInfo{mkPI("/usr/etc/cfg", "-", 0644, 0, 0, 100, "")}
	new_ := []filesystems.PathInfo{mkPI("/usr/etc/cfg", "-", 0644, 0, 0, 200, "")} // upstream changed
	user := []*filesystems.PathInfo{}                                              // user removed

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	if changes[0].Action != EtcActionConflict {
		t.Errorf("Expected conflict, got %s", changes[0].Action)
	}
}

func TestComputeEtcDiffUserRemovedUnchangedUpstream(t *testing.T) {
	old := []filesystems.PathInfo{mkPI("/usr/etc/cfg", "-", 0644, 0, 0, 100, "")}
	new_ := []filesystems.PathInfo{mkPI("/usr/etc/cfg", "-", 0644, 0, 0, 100, "")} // unchanged
	user := []*filesystems.PathInfo{}                                              // user removed

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	if changes[0].Action != EtcActionUserOnly {
		t.Errorf("Expected user-only, got %s", changes[0].Action)
	}
}

func TestComputeEtcDiffUserAdded(t *testing.T) {
	old := []filesystems.PathInfo{}
	new_ := []filesystems.PathInfo{}
	user := []*filesystems.PathInfo{ptr(mkPI("/etc/custom", "-", 0644, 0, 0, 42, ""))}

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	c := changes[0]
	if c.Path != "custom" || c.Action != EtcActionUserOnly {
		t.Errorf("Expected user-only of 'custom', got %q action=%s", c.Path, c.Action)
	}
}

func TestComputeEtcDiffConflictBothAdded(t *testing.T) {
	old := []filesystems.PathInfo{}
	new_ := []filesystems.PathInfo{mkPI("/usr/etc/both", "-", 0644, 0, 0, 50, "")}
	user := []*filesystems.PathInfo{ptr(mkPI("/etc/both", "-", 0755, 0, 0, 60, ""))} // different

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	if changes[0].Action != EtcActionConflict {
		t.Errorf("Expected conflict, got %s", changes[0].Action)
	}
}

func TestComputeEtcDiffBothAddedIdentical(t *testing.T) {
	old := []filesystems.PathInfo{}
	new_ := []filesystems.PathInfo{mkPI("/usr/etc/same", "-", 0644, 0, 0, 50, "")}
	user := []*filesystems.PathInfo{ptr(mkPI("/etc/same", "-", 0644, 0, 0, 50, ""))}

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 0 {
		t.Errorf("Expected no changes (both added identical), got %d", len(changes))
	}
}

func TestComputeEtcDiffSymlinks(t *testing.T) {
	old := []filesystems.PathInfo{mkPI("/usr/etc/link", "l", 0777, 0, 0, 0, "old_target")}
	new_ := []filesystems.PathInfo{mkPI("/usr/etc/link", "l", 0777, 0, 0, 0, "new_target")}
	user := []*filesystems.PathInfo{ptr(mkPI("/etc/link", "l", 0777, 0, 0, 0, "old_target"))}

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	c := changes[0]
	if c.Path != "link" || c.Action != EtcActionUpdate {
		t.Errorf("Expected update of symlink 'link', got %q action=%s", c.Path, c.Action)
	}
}

func TestComputeEtcDiffMultipleChanges(t *testing.T) {
	old := []filesystems.PathInfo{
		mkPI("/usr/etc/keep", "-", 0644, 0, 0, 100, ""),
		mkPI("/usr/etc/update", "-", 0644, 0, 0, 100, ""),
		mkPI("/usr/etc/conflict", "-", 0644, 0, 0, 100, ""),
		mkPI("/usr/etc/remove", "-", 0644, 0, 0, 100, ""),
	}
	new_ := []filesystems.PathInfo{
		mkPI("/usr/etc/keep", "-", 0644, 0, 0, 100, ""),
		mkPI("/usr/etc/update", "-", 0644, 0, 0, 200, ""),   // upstream changed size
		mkPI("/usr/etc/conflict", "-", 0644, 0, 0, 300, ""), // upstream changed
		mkPI("/usr/etc/added", "-", 0644, 0, 0, 50, ""),     // new file
	}
	user := []*filesystems.PathInfo{
		ptr(mkPI("/etc/keep", "-", 0644, 0, 0, 100, "")),
		ptr(mkPI("/etc/update", "-", 0644, 0, 0, 100, "")),   // unchanged
		ptr(mkPI("/etc/conflict", "-", 0755, 0, 0, 400, "")), // user also changed
		ptr(mkPI("/etc/remove", "-", 0644, 0, 0, 100, "")),   // upstream removed, user unchanged
		ptr(mkPI("/etc/useronly", "-", 0644, 0, 0, 99, "")),  // user added
	}

	changes := computeEtcDiff(&old, &new_, user)

	expected := map[string]EtcChangeAction{
		"update":   EtcActionUpdate,
		"conflict": EtcActionConflict,
		"added":    EtcActionAdd,
		"remove":   EtcActionRemove,
		"useronly": EtcActionUserOnly,
	}

	if len(changes) != len(expected) {
		t.Fatalf("Expected %d changes, got %d: %+v", len(expected), len(changes), changes)
	}
	for path, action := range expected {
		c := findChange(changes, path)
		if c == nil {
			t.Errorf("Missing change for path %q", path)
			continue
		}
		if c.Action != action {
			t.Errorf("Path %q: expected action %s, got %s", path, action, c.Action)
		}
	}
}

func TestComputeEtcDiffNilInputs(t *testing.T) {
	// nil old and new should not panic
	user := []*filesystems.PathInfo{ptr(mkPI("/etc/custom", "-", 0644, 0, 0, 10, ""))}
	changes := computeEtcDiff(nil, nil, user)
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	if changes[0].Action != EtcActionUserOnly {
		t.Errorf("Expected user-only, got %s", changes[0].Action)
	}
}

func TestComputeEtcDiffSorted(t *testing.T) {
	old := []filesystems.PathInfo{}
	new_ := []filesystems.PathInfo{
		mkPI("/usr/etc/z_file", "-", 0644, 0, 0, 1, ""),
		mkPI("/usr/etc/a_file", "-", 0644, 0, 0, 1, ""),
		mkPI("/usr/etc/m_file", "-", 0644, 0, 0, 1, ""),
	}
	user := []*filesystems.PathInfo{}

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 3 {
		t.Fatalf("Expected 3 changes, got %d", len(changes))
	}
	if changes[0].Path != "a_file" || changes[1].Path != "m_file" || changes[2].Path != "z_file" {
		t.Errorf("Results not sorted: %s, %s, %s",
			changes[0].Path, changes[1].Path, changes[2].Path)
	}
}

func TestComputeEtcDiffDirectories(t *testing.T) {
	old := []filesystems.PathInfo{mkPI("/usr/etc/conf.d", "d", 0755, 0, 0, 0, "")}
	new_ := []filesystems.PathInfo{mkPI("/usr/etc/conf.d", "d", 0700, 0, 0, 0, "")} // perms changed
	user := []*filesystems.PathInfo{ptr(mkPI("/etc/conf.d", "d", 0755, 0, 0, 0, ""))}

	changes := computeEtcDiff(&old, &new_, user)
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	c := changes[0]
	if c.Path != "conf.d" || c.Action != EtcActionUpdate {
		t.Errorf("Expected update of directory 'conf.d', got %q action=%s", c.Path, c.Action)
	}
}
