package releaser

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
)

// newOstreeTestReleaser returns a Releaser wired to a MockOstree with a
// real temporary imageDir. The caller can customise the mock after creation.
func newOstreeTestReleaser(tb testing.TB) (*Releaser, *ostree.MockOstree) {
	tb.Helper()
	mock := &ostree.MockOstree{}
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: &config.MockConfig{Items: map[string][]string{}, Bools: map[string]bool{}}},
		ostree:         mock,
		chrootRunner:   runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})
	r.imageDir = tb.(*testing.T).TempDir()
	return r, mock
}

// ---------------------------------------------------------------------------
// SymlinkEtc
// ---------------------------------------------------------------------------

func TestSymlinkEtc_CreatesSymlink(t *testing.T) {
	r, _ := newOstreeTestReleaser(t)

	if err := r.SymlinkEtc(); err != nil {
		t.Fatalf("SymlinkEtc: %v", err)
	}

	target, err := os.Readlink(filepath.Join(r.imageDir, "etc"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "usr/etc" {
		t.Errorf("symlink target = %q, want %q", target, "usr/etc")
	}
}

func TestSymlinkEtc_FailsIfAlreadyExists(t *testing.T) {
	r, _ := newOstreeTestReleaser(t)

	// Create a real directory so the symlink creation fails.
	if err := os.MkdirAll(filepath.Join(r.imageDir, "etc"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := r.SymlinkEtc(); err == nil {
		t.Fatal("expected error when etc already exists")
	}
}

// ---------------------------------------------------------------------------
// UnlinkEtc
// ---------------------------------------------------------------------------

func TestUnlinkEtc_RemovesSymlink(t *testing.T) {
	r, _ := newOstreeTestReleaser(t)

	// Create the symlink first.
	if err := os.Symlink("usr/etc", filepath.Join(r.imageDir, "etc")); err != nil {
		t.Fatal(err)
	}

	if err := r.UnlinkEtc(); err != nil {
		t.Fatalf("UnlinkEtc: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(r.imageDir, "etc")); !os.IsNotExist(err) {
		t.Error("expected etc to be removed")
	}
}

func TestUnlinkEtc_FailsIfMissing(t *testing.T) {
	r, _ := newOstreeTestReleaser(t)

	if err := r.UnlinkEtc(); err == nil {
		t.Fatal("expected error when etc does not exist")
	}
}

// ---------------------------------------------------------------------------
// AddExtraDotDotToUsrEtcPortage
// ---------------------------------------------------------------------------

func TestAddExtraDotDotToUsrEtcPortage_Success(t *testing.T) {
	r, _ := newOstreeTestReleaser(t)

	// Layout: imageDir/usr/etc/portage -> ../portage-data
	// After adding "../", becomes: ../../portage-data
	// That resolves relative to imageDir/usr/etc/ → imageDir/portage-data
	usrEtc := filepath.Join(r.imageDir, "usr", "etc")
	if err := os.MkdirAll(usrEtc, 0755); err != nil {
		t.Fatal(err)
	}

	// Create the target that the *adjusted* symlink (../../portage-data) will point to.
	if err := os.MkdirAll(filepath.Join(r.imageDir, "portage-data"), 0755); err != nil {
		t.Fatal(err)
	}

	origTarget := "../portage-data"
	if err := os.Symlink(origTarget, filepath.Join(usrEtc, "portage")); err != nil {
		t.Fatal(err)
	}

	if err := r.AddExtraDotDotToUsrEtcPortage(); err != nil {
		t.Fatalf("AddExtraDotDotToUsrEtcPortage: %v", err)
	}

	got, err := os.Readlink(filepath.Join(usrEtc, "portage"))
	if err != nil {
		t.Fatal(err)
	}
	want := "../" + origTarget
	if got != want {
		t.Errorf("symlink = %q, want %q", got, want)
	}
}

func TestAddExtraDotDotToUsrEtcPortage_BrokenSymlink(t *testing.T) {
	r, _ := newOstreeTestReleaser(t)

	usrEtc := filepath.Join(r.imageDir, "usr", "etc")
	if err := os.MkdirAll(usrEtc, 0755); err != nil {
		t.Fatal(err)
	}

	// Symlink to a target that won't exist after adding "../".
	if err := os.Symlink("nonexistent", filepath.Join(usrEtc, "portage")); err != nil {
		t.Fatal(err)
	}

	err := r.AddExtraDotDotToUsrEtcPortage()
	if err == nil {
		t.Fatal("expected error for broken symlink")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error = %q, want it to mention 'broken'", err)
	}
}

func TestAddExtraDotDotToUsrEtcPortage_NoSymlink(t *testing.T) {
	r, _ := newOstreeTestReleaser(t)

	// Don't create the symlink at all.
	err := r.AddExtraDotDotToUsrEtcPortage()
	if err == nil {
		t.Fatal("expected error when symlink is missing")
	}
}

// ---------------------------------------------------------------------------
// RemoveExtraDotDotFromUsrEtcPortage
// ---------------------------------------------------------------------------

func TestRemoveExtraDotDotFromUsrEtcPortage_Success(t *testing.T) {
	r, _ := newOstreeTestReleaser(t)

	usrEtc := filepath.Join(r.imageDir, "usr", "etc")
	if err := os.MkdirAll(usrEtc, 0755); err != nil {
		t.Fatal(err)
	}

	// Starts with the extra "../" prefix.
	if err := os.Symlink("../../../portage-target", filepath.Join(usrEtc, "portage")); err != nil {
		t.Fatal(err)
	}

	if err := r.RemoveExtraDotDotFromUsrEtcPortage(); err != nil {
		t.Fatalf("RemoveExtraDotDotFromUsrEtcPortage: %v", err)
	}

	got, err := os.Readlink(filepath.Join(usrEtc, "portage"))
	if err != nil {
		t.Fatal(err)
	}
	want := "../../portage-target"
	if got != want {
		t.Errorf("symlink = %q, want %q", got, want)
	}
}

func TestRemoveExtraDotDotFromUsrEtcPortage_NoPrefix(t *testing.T) {
	r, _ := newOstreeTestReleaser(t)

	usrEtc := filepath.Join(r.imageDir, "usr", "etc")
	if err := os.MkdirAll(usrEtc, 0755); err != nil {
		t.Fatal(err)
	}

	// Target that does not have the "../" prefix — should remain unchanged.
	if err := os.Symlink("portage-target", filepath.Join(usrEtc, "portage")); err != nil {
		t.Fatal(err)
	}

	if err := r.RemoveExtraDotDotFromUsrEtcPortage(); err != nil {
		t.Fatalf("RemoveExtraDotDotFromUsrEtcPortage: %v", err)
	}

	got, err := os.Readlink(filepath.Join(usrEtc, "portage"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "portage-target" {
		t.Errorf("symlink = %q, want %q", got, "portage-target")
	}
}

func TestRemoveExtraDotDotFromUsrEtcPortage_NoSymlink(t *testing.T) {
	r, _ := newOstreeTestReleaser(t)

	err := r.RemoveExtraDotDotFromUsrEtcPortage()
	if err == nil {
		t.Fatal("expected error when symlink is missing")
	}
}

// ---------------------------------------------------------------------------
// OstreePrepare
// ---------------------------------------------------------------------------

func TestOstreePrepare_Success(t *testing.T) {
	r, _ := newOstreeTestReleaser(t)

	if err := r.OstreePrepare(); err != nil {
		t.Fatalf("OstreePrepare: %v", err)
	}
}

func TestOstreePrepare_PrepareError(t *testing.T) {
	r, mock := newOstreeTestReleaser(t)
	mock.PrepareFilesystemHierarchyErr = errors.New("prepare boom")

	err := r.OstreePrepare()
	if err == nil || err.Error() != "prepare boom" {
		t.Fatalf("expected 'prepare boom', got %v", err)
	}
}

func TestOstreePrepare_ValidateError(t *testing.T) {
	r, mock := newOstreeTestReleaser(t)
	mock.ValidateFilesystemHierarchyErr = errors.New("validate boom")

	err := r.OstreePrepare()
	if err == nil || err.Error() != "validate boom" {
		t.Fatalf("expected 'validate boom', got %v", err)
	}
}

// ---------------------------------------------------------------------------
// MaybeOstreeInit
// ---------------------------------------------------------------------------

func TestMaybeOstreeInit_AlreadyExists(t *testing.T) {
	r, mock := newOstreeTestReleaser(t)

	repoDir := t.TempDir()
	mock.RepoDir_ = repoDir
	// Create the objects directory so the check passes.
	if err := os.MkdirAll(filepath.Join(repoDir, "objects"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := r.MaybeOstreeInit(); err != nil {
		t.Fatalf("MaybeOstreeInit: %v", err)
	}

	if mock.InitRepoCalled {
		t.Error("InitRepo should NOT be called when repo already exists")
	}
}

func TestMaybeOstreeInit_CreatesNew(t *testing.T) {
	r, mock := newOstreeTestReleaser(t)

	repoDir := filepath.Join(t.TempDir(), "repo")
	mock.RepoDir_ = repoDir

	if err := r.MaybeOstreeInit(); err != nil {
		t.Fatalf("MaybeOstreeInit: %v", err)
	}

	if !mock.InitRepoCalled {
		t.Error("InitRepo should be called for a new repo")
	}
	if !mock.SetGpgCalled {
		t.Error("SetGpg should be called after init")
	}
}

func TestMaybeOstreeInit_RepoDirError(t *testing.T) {
	r, mock := newOstreeTestReleaser(t)
	mock.RepoDirErr = errors.New("repodir boom")

	err := r.MaybeOstreeInit()
	if err == nil || err.Error() != "repodir boom" {
		t.Fatalf("expected 'repodir boom', got %v", err)
	}
}

func TestMaybeOstreeInit_InitRepoError(t *testing.T) {
	r, mock := newOstreeTestReleaser(t)
	mock.RepoDir_ = filepath.Join(t.TempDir(), "repo")
	mock.InitRepoErr = errors.New("init boom")

	err := r.MaybeOstreeInit()
	if err == nil || err.Error() != "init boom" {
		t.Fatalf("expected 'init boom', got %v", err)
	}
}

func TestMaybeOstreeInit_GpgEnabledError(t *testing.T) {
	r, mock := newOstreeTestReleaser(t)
	mock.RepoDir_ = filepath.Join(t.TempDir(), "repo")
	mock.GpgEnabledErr = errors.New("gpg boom")

	err := r.MaybeOstreeInit()
	if err == nil || err.Error() != "gpg boom" {
		t.Fatalf("expected 'gpg boom', got %v", err)
	}
}

func TestMaybeOstreeInit_SetGpgError(t *testing.T) {
	r, mock := newOstreeTestReleaser(t)
	mock.RepoDir_ = filepath.Join(t.TempDir(), "repo")
	mock.SetGpgErr = errors.New("setgpg boom")

	err := r.MaybeOstreeInit()
	if err == nil || err.Error() != "setgpg boom" {
		t.Fatalf("expected 'setgpg boom', got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Release
// ---------------------------------------------------------------------------

// releaseTestSetup configures a Releaser and MockOstree for a successful
// Release() call. Individual tests can break one thing at a time.
func releaseTestSetup(t *testing.T) (*Releaser, *ostree.MockOstree) {
	t.Helper()
	r, mock := newOstreeTestReleaser(t)

	repoDir := filepath.Join(t.TempDir(), "repo")
	mock.RepoDir_ = repoDir
	mock.OsName_ = "matrixOS"
	mock.FancyOsName_ = "Matrix OS"
	mock.GpgArgs_ = []string{"--gpg-sign=ABC"}

	cfgMock := r.cfg.(*config.MockConfig)
	cfgMock.Items["Seeder.ChrootMetadataDir"] = []string{"usr/share/matrixos"}
	cfgMock.Items["Releaser.ReadOnlyVdb"] = []string{"usr/share/vdb"}
	cfgMock.Bools["Releaser.GenerateStaticDeltas"] = true

	// Create a minimal RO vdb so listPackages succeeds.
	vdbDir := filepath.Join(r.imageDir, "usr", "share", "vdb", "sys-apps", "placeholder-1.0")
	if err := os.MkdirAll(vdbDir, 0755); err != nil {
		t.Fatal(err)
	}

	return r, mock
}

func TestRelease_Success(t *testing.T) {
	r, mock := releaseTestSetup(t)
	opts := CommitOptions{Branch: "matrixos/stable"}

	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if !mock.CommitCalled {
		t.Error("Commit should be called")
	}
	if !mock.PruneCalled {
		t.Error("Prune should be called")
	}
	if !mock.GenerateStaticDeltaCalled {
		t.Error("GenerateStaticDelta should be called")
	}
	if !mock.UpdateSummaryCalled {
		t.Error("UpdateSummary should be called")
	}

	// Validate commit options.
	if !strings.Contains(mock.CommitOpts.Subject, "matrixOS") {
		t.Errorf("subject should contain OS name, got %q", mock.CommitOpts.Subject)
	}
	if !strings.Contains(mock.CommitOpts.Body, "Matrix OS") {
		t.Errorf("body should contain fancy OS name, got %q", mock.CommitOpts.Body)
	}
}

func TestRelease_WithConsume(t *testing.T) {
	r, mock := releaseTestSetup(t)
	opts := CommitOptions{Branch: "matrixos/stable", Consume: true}

	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if !mock.CommitOpts.Consume {
		t.Error("commit opts should have Consume=true")
	}
}

func TestRelease_MissingBranch(t *testing.T) {
	r, _ := releaseTestSetup(t)
	opts := CommitOptions{}

	err := r.Release(opts)
	if err == nil || !strings.Contains(err.Error(), "missing branch") {
		t.Fatalf("expected 'missing branch' error, got %v", err)
	}
}

func TestRelease_EtcExists(t *testing.T) {
	r, _ := releaseTestSetup(t)

	// Create /etc in imageDir — this should cause an error.
	if err := os.MkdirAll(filepath.Join(r.imageDir, "etc"), 0755); err != nil {
		t.Fatal(err)
	}

	opts := CommitOptions{Branch: "matrixos/stable"}
	err := r.Release(opts)
	if err == nil || !strings.Contains(err.Error(), "illegal") {
		t.Fatalf("expected 'illegal' error, got %v", err)
	}
}

func TestRelease_OsNameError(t *testing.T) {
	r, mock := releaseTestSetup(t)
	mock.OsNameErr = errors.New("osname boom")

	opts := CommitOptions{Branch: "matrixos/stable"}
	err := r.Release(opts)
	if err == nil || err.Error() != "osname boom" {
		t.Fatalf("expected 'osname boom', got %v", err)
	}
}

func TestRelease_FancyOsNameError(t *testing.T) {
	r, mock := releaseTestSetup(t)
	mock.FancyOsNameErr = errors.New("fancy boom")

	opts := CommitOptions{Branch: "matrixos/stable"}
	err := r.Release(opts)
	if err == nil || err.Error() != "fancy boom" {
		t.Fatalf("expected 'fancy boom', got %v", err)
	}
}

func TestRelease_GpgArgsError(t *testing.T) {
	r, mock := releaseTestSetup(t)
	mock.GpgArgsErr = errors.New("gpg boom")

	opts := CommitOptions{Branch: "matrixos/stable"}
	err := r.Release(opts)
	if err == nil || err.Error() != "gpg boom" {
		t.Fatalf("expected 'gpg boom', got %v", err)
	}
}

func TestRelease_CommitError(t *testing.T) {
	r, mock := releaseTestSetup(t)
	mock.CommitErr = errors.New("commit boom")

	opts := CommitOptions{Branch: "matrixos/stable"}
	err := r.Release(opts)
	if err == nil || !strings.Contains(err.Error(), "commit boom") {
		t.Fatalf("expected wrapped 'commit boom', got %v", err)
	}
}

func TestRelease_PruneError(t *testing.T) {
	r, mock := releaseTestSetup(t)
	mock.PruneErr = errors.New("prune boom")

	opts := CommitOptions{Branch: "matrixos/stable"}
	err := r.Release(opts)
	if err == nil || !strings.Contains(err.Error(), "prune boom") {
		t.Fatalf("expected wrapped 'prune boom', got %v", err)
	}
}

func TestRelease_StaticDeltaError(t *testing.T) {
	r, mock := releaseTestSetup(t)
	mock.GenerateStaticDeltaErr = errors.New("delta boom")

	opts := CommitOptions{Branch: "matrixos/stable"}
	err := r.Release(opts)
	if err == nil || !strings.Contains(err.Error(), "delta boom") {
		t.Fatalf("expected wrapped 'delta boom', got %v", err)
	}
}

func TestRelease_UpdateSummaryError(t *testing.T) {
	r, mock := releaseTestSetup(t)
	mock.UpdateSummaryErr = errors.New("summary boom")

	opts := CommitOptions{Branch: "matrixos/stable"}
	err := r.Release(opts)
	if err == nil || !strings.Contains(err.Error(), "summary boom") {
		t.Fatalf("expected wrapped 'summary boom', got %v", err)
	}
}

func TestRelease_SkipsStaticDeltas(t *testing.T) {
	r, mock := releaseTestSetup(t)
	cfgMock := r.cfg.(*config.MockConfig)
	cfgMock.Bools["Releaser.GenerateStaticDeltas"] = false

	opts := CommitOptions{Branch: "matrixos/stable"}
	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if mock.GenerateStaticDeltaCalled {
		t.Error("GenerateStaticDelta should NOT be called when disabled")
	}
	if !mock.UpdateSummaryCalled {
		t.Error("UpdateSummary should still be called")
	}
}

func TestRelease_WithParentBranch(t *testing.T) {
	r, mock := releaseTestSetup(t)

	opts := CommitOptions{
		Branch:       "matrixos/stable",
		ParentBranch: "matrixos/testing",
		ParentRev:    "abc123parent",
	}

	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if mock.CommitOpts.Parent != "abc123parent" {
		t.Errorf("commit parent = %q, want %q", mock.CommitOpts.Parent, "abc123parent")
	}
	if !strings.Contains(mock.CommitOpts.Body, "matrixos/testing") {
		t.Errorf("body should contain parent branch, got %q", mock.CommitOpts.Body)
	}
}

func TestRelease_ParentRevPassedToCommit(t *testing.T) {
	r, mock := releaseTestSetup(t)

	opts := CommitOptions{
		Branch:    "matrixos/stable",
		ParentRev: "deadbeef",
	}

	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if mock.CommitOpts.Parent != "deadbeef" {
		t.Errorf("commit parent = %q, want %q", mock.CommitOpts.Parent, "deadbeef")
	}
}

func TestRelease_ReadsMetadataFile(t *testing.T) {
	r, mock := releaseTestSetup(t)

	// Create a build metadata file in imageDir.
	metaDir := filepath.Join(r.imageDir, "usr/share/matrixos")
	if err := os.MkdirAll(metaDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "build"), []byte("build-42"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CommitOptions{Branch: "matrixos/stable"}
	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if !strings.Contains(mock.CommitOpts.Body, "build-42") {
		t.Errorf("body should contain metadata, got %q", mock.CommitOpts.Body)
	}
}

func TestRelease_MissingMetadataUsesDefault(t *testing.T) {
	r, mock := releaseTestSetup(t)

	opts := CommitOptions{Branch: "matrixos/stable"}
	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if !strings.Contains(mock.CommitOpts.Body, "not available") {
		t.Errorf("body should contain 'not available' when metadata missing, got %q", mock.CommitOpts.Body)
	}
}

func TestRelease_BuildMetadataFileError(t *testing.T) {
	r, _ := releaseTestSetup(t)
	r.cfg = &config.ErrConfig{Err: errors.New("cfg boom")}

	opts := CommitOptions{Branch: "matrixos/stable"}
	err := r.Release(opts)
	if err == nil || !strings.Contains(err.Error(), "cfg boom") {
		t.Fatalf("expected 'cfg boom', got %v", err)
	}
}

func TestRelease_BodyContainsNoneWhenNoParent(t *testing.T) {
	r, mock := releaseTestSetup(t)

	opts := CommitOptions{Branch: "matrixos/stable"}
	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if !strings.Contains(mock.CommitOpts.Body, "none") {
		t.Errorf("body should contain 'none' when no parent branch, got %q", mock.CommitOpts.Body)
	}
}

func TestRelease_GpgArgsPassedToCommit(t *testing.T) {
	r, mock := releaseTestSetup(t)
	mock.GpgArgs_ = []string{"--gpg-sign=KEY1", "--gpg-homedir=/tmp"}

	opts := CommitOptions{Branch: "matrixos/stable"}
	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if len(mock.CommitOpts.GpgArgs) != 2 {
		t.Fatalf("gpgArgs len = %d, want 2", len(mock.CommitOpts.GpgArgs))
	}
	if mock.CommitOpts.GpgArgs[0] != "--gpg-sign=KEY1" {
		t.Errorf("gpgArgs[0] = %q, want %q", mock.CommitOpts.GpgArgs[0], "--gpg-sign=KEY1")
	}
}

func TestRelease_EmptyParentRevNoParentInCommit(t *testing.T) {
	r, mock := releaseTestSetup(t)

	opts := CommitOptions{Branch: "matrixos/stable"}
	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if mock.CommitOpts.Parent != "" {
		t.Errorf("commit parent should be empty when no ParentRev, got %q", mock.CommitOpts.Parent)
	}
}

func TestRelease_ConsumePassedToCommit(t *testing.T) {
	r, mock := releaseTestSetup(t)

	opts := CommitOptions{Branch: "matrixos/stable", Consume: true, ParentRev: "rev1"}
	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if !mock.CommitOpts.Consume {
		t.Error("Consume should be passed through to ostree commit")
	}
	if mock.CommitOpts.Parent != "rev1" {
		t.Errorf("Parent = %q, want %q", mock.CommitOpts.Parent, "rev1")
	}
}

func TestRelease_ImageDirPassedToCommit(t *testing.T) {
	r, mock := releaseTestSetup(t)

	opts := CommitOptions{Branch: "matrixos/stable"}
	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if mock.CommitOpts.ImageDir != r.imageDir {
		t.Errorf("commit ImageDir = %q, want %q", mock.CommitOpts.ImageDir, r.imageDir)
	}
}

func TestRelease_SubjectContainsBranch(t *testing.T) {
	r, mock := releaseTestSetup(t)

	opts := CommitOptions{Branch: "matrixos/testing"}
	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if !strings.Contains(mock.CommitOpts.Subject, "matrixos/testing") {
		t.Errorf("subject should contain branch name, got %q", mock.CommitOpts.Subject)
	}
}

func TestRelease_BodyContainsParentBranchWhenSet(t *testing.T) {
	r, mock := releaseTestSetup(t)

	opts := CommitOptions{
		Branch:       "matrixos/stable",
		ParentBranch: "matrixos/testing-full",
		ParentRev:    "abc123",
	}
	if err := r.Release(opts); err != nil {
		t.Fatalf("Release: %v", err)
	}

	if !strings.Contains(mock.CommitOpts.Body, "matrixos/testing-full") {
		t.Errorf("body should contain parent branch name, got %q", mock.CommitOpts.Body)
	}
}

func TestRelease_EmptyImageDir(t *testing.T) {
	r, _ := releaseTestSetup(t)
	r.imageDir = ""

	opts := CommitOptions{Branch: "matrixos/stable"}
	err := r.Release(opts)
	if err == nil || !strings.Contains(err.Error(), "imageDir") {
		t.Fatalf("expected imageDir error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// listPackages
// ---------------------------------------------------------------------------

// mkVdbPkg creates a category/package directory inside a vdb root.
func mkVdbPkg(t *testing.T, vdbDir, cat, pkg string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(vdbDir, cat, pkg), 0755); err != nil {
		t.Fatal(err)
	}
}

func newListPkgReleaser(t *testing.T, roVdb string) *Releaser {
	t.Helper()
	r, _ := newOstreeTestReleaser(t)
	r.cfg = &config.MockConfig{
		Items: map[string][]string{
			"Releaser.ReadOnlyVdb": {roVdb},
		},
		Bools: map[string]bool{},
	}
	return r
}

func TestListPackages_UsesReadOnlyVdb(t *testing.T) {
	roVdb := "usr/share/vdb"
	r := newListPkgReleaser(t, roVdb)

	// Create the RO vdb with a package.
	mkVdbPkg(t, filepath.Join(r.imageDir, roVdb), "sys-apps", "systemd-256")

	pkgs, err := r.listPackages(r.imageDir)
	if err != nil {
		t.Fatalf("listPackages: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0] != "sys-apps/systemd-256" {
		t.Fatalf("expected [sys-apps/systemd-256], got %v", pkgs)
	}
}

func TestListPackages_FallsBackToRwVdb(t *testing.T) {
	roVdb := "usr/share/vdb-does-not-exist"
	r := newListPkgReleaser(t, roVdb)

	// Only create the RW vdb (var/db/pkg).
	mkVdbPkg(t, filepath.Join(r.imageDir, ostree.RwVdbPath), "dev-libs", "openssl-3.1")

	pkgs, err := r.listPackages(r.imageDir)
	if err != nil {
		t.Fatalf("listPackages: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0] != "dev-libs/openssl-3.1" {
		t.Fatalf("expected [dev-libs/openssl-3.1], got %v", pkgs)
	}
}

func TestListPackages_RoVdbTakesPrecedenceOverRw(t *testing.T) {
	roVdb := "usr/share/vdb"
	r := newListPkgReleaser(t, roVdb)

	// Create both vdb directories with different content.
	mkVdbPkg(t, filepath.Join(r.imageDir, roVdb), "sys-apps", "from-ro-1.0")
	mkVdbPkg(t, filepath.Join(r.imageDir, ostree.RwVdbPath), "sys-apps", "from-rw-1.0")

	pkgs, err := r.listPackages(r.imageDir)
	if err != nil {
		t.Fatalf("listPackages: %v", err)
	}
	// Should use ro vdb, not rw.
	if len(pkgs) != 1 || pkgs[0] != "sys-apps/from-ro-1.0" {
		t.Fatalf("expected [sys-apps/from-ro-1.0], got %v", pkgs)
	}
}

func TestListPackages_NoVdbDir(t *testing.T) {
	roVdb := "no/such/vdb"
	r := newListPkgReleaser(t, roVdb)

	// Neither RO nor RW vdb exists.
	_, err := r.listPackages(r.imageDir)
	if err == nil {
		t.Fatal("expected error when no vdb directory exists")
	}
	if !strings.Contains(err.Error(), "no vdb directory found") {
		t.Errorf("error = %q, want 'no vdb directory found'", err)
	}
}

func TestListPackages_ReadOnlyVdbConfigError(t *testing.T) {
	r, _ := newOstreeTestReleaser(t)
	// Empty config → ReadOnlyVdb returns an error.
	r.cfg = &config.MockConfig{
		Items: map[string][]string{},
		Bools: map[string]bool{},
	}

	_, err := r.listPackages(r.imageDir)
	if err == nil {
		t.Fatal("expected error when ReadOnlyVdb config is missing")
	}
}

func TestListPackages_MultiplePackages(t *testing.T) {
	roVdb := "usr/share/vdb"
	r := newListPkgReleaser(t, roVdb)

	vdbRoot := filepath.Join(r.imageDir, roVdb)
	mkVdbPkg(t, vdbRoot, "sys-apps", "systemd-256")
	mkVdbPkg(t, vdbRoot, "dev-libs", "openssl-3.1")
	mkVdbPkg(t, vdbRoot, "app-misc", "screen-4.9")

	pkgs, err := r.listPackages(r.imageDir)
	if err != nil {
		t.Fatalf("listPackages: %v", err)
	}
	if len(pkgs) != 3 {
		t.Fatalf("expected 3 packages, got %d: %v", len(pkgs), pkgs)
	}
}

func TestListPackages_UsesProvidedImageDir(t *testing.T) {
	roVdb := "usr/share/vdb"
	r := newListPkgReleaser(t, roVdb)

	// Use a separate temp dir (not r.imageDir) as the imageDir argument.
	altImageDir := t.TempDir()
	mkVdbPkg(t, filepath.Join(altImageDir, roVdb), "net-misc", "curl-8.5")

	pkgs, err := r.listPackages(altImageDir)
	if err != nil {
		t.Fatalf("listPackages: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0] != "net-misc/curl-8.5" {
		t.Fatalf("expected [net-misc/curl-8.5], got %v", pkgs)
	}
}
