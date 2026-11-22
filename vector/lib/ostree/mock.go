package ostree

import (
	"errors"
	"io"
	"strings"

	"matrixos/vector/lib/filesystems"
)

// mockOstree implements IOstree for testing commands.
// Only the fields/methods relevant to each test need to be configured;
// everything else returns safe zero values.
type MockOstree struct {
	Root_            string
	RootErr          error
	Ref_             string
	Deployments      []Deployment
	DeploymentsErr   error
	Refs             []string
	RefsErr          error
	Remote_          string
	RemoteErr        error
	SwitchRef        string
	SwitchErr        error
	LastCommit_      string
	LastCommitErr    error
	UpgradeArgs      []string
	UpgradeErr       error
	PinIndex         int
	PinChecksum      string
	PinErr           error
	PinNotFoundErr   error
	Packages         []string
	PackagesErr      error
	PackagesByCommit map[string][]string

	LocalRefs_   []string
	LocalRefsErr error

	RemoveFullResult    string
	RemoveFullResultSet bool // when true, return RemoveFullResult even if empty
	RemoveFullErr       error

	BootCommitResult string
	BootCommitErr    error

	RepoDir_   string
	RepoDirErr error

	OsName_   string
	OsNameErr error

	Arch_   string
	ArchErr error

	FancyOsName_   string
	FancyOsNameErr error

	GpgEnabled_   bool
	GpgEnabledErr error

	GpgArgs_   []string
	GpgArgsErr error

	SetGpgErr    error
	SetGpgCalled bool
	SetGpgArg    bool

	InitRepoErr    error
	InitRepoCalled bool

	CommitErr    error
	CommitCalled bool
	CommitOpts   CommitOptions

	PruneErr    error
	PruneCalled bool

	GenerateStaticDeltaErr    error
	GenerateStaticDeltaCalled bool

	UpdateSummaryErr    error
	UpdateSummaryCalled bool

	PrepareFilesystemHierarchyErr  error
	ValidateFilesystemHierarchyErr error

	Sysroot_   string
	SysrootErr error

	GpgPrivateKeyPath_   string
	GpgPrivateKeyPathErr error

	GpgBestPubKeyPath_   string
	GpgBestPubKeyPathErr error

	ReadwriteErr       error
	ReadwriteCalled    bool
	ReadwritePermanent bool

	PostCopyErr    error
	PostCopyCalled bool
}

func (m *MockOstree) CloneForRef(ref string) (IOstree, error) {
	clone := *m
	clone.Ref_ = ref
	return &clone, nil
}

// Config accessors — return zero values (not used in branch/upgrade tests).
func (m *MockOstree) SetStdout(_ io.Writer)                                   {}
func (m *MockOstree) SetStderr(_ io.Writer)                                   {}
func (m *MockOstree) Print(_ string, _ ...interface{})                        {}
func (m *MockOstree) PrintError(_ string, _ ...interface{})                   {}
func (m *MockOstree) Ref() string                                             { return m.Ref_ }
func (m *MockOstree) SetRef(ref string)                                       { m.Ref_ = ref }
func (m *MockOstree) FullBranchSuffix() (string, error)                       { return "-full", nil }
func (m *MockOstree) IsBranchFullSuffixed() (bool, error) { return false, nil }
func (m *MockOstree) BranchToFull() (string, error)       { return "", nil }
func (m *MockOstree) RemoveFullFromBranch() (string, error) {
	if m.RemoveFullErr != nil {
		return "", m.RemoveFullErr
	}
	if m.RemoveFullResultSet {
		return m.RemoveFullResult, nil
	}
	// Default: strip -full suffix if present.
	return strings.TrimSuffix(m.Ref_, "-full"), nil
}
func (m *MockOstree) GpgEnabled() (bool, error) { return m.GpgEnabled_, m.GpgEnabledErr }
func (m *MockOstree) GpgPrivateKeyPath() (string, error) {
	return m.GpgPrivateKeyPath_, m.GpgPrivateKeyPathErr
}
func (m *MockOstree) GpgPublicKeyPath() (string, error)          { return "", nil }
func (m *MockOstree) GpgOfficialPubKeyPath() (string, error)     { return "", nil }
func (m *MockOstree) OsName() (string, error)                    { return m.OsName_, m.OsNameErr }
func (m *MockOstree) Arch() (string, error)                      { return m.Arch_, m.ArchErr }
func (m *MockOstree) RepoDir() (string, error)                   { return m.RepoDir_, m.RepoDirErr }
func (m *MockOstree) Sysroot() (string, error)                   { return m.Sysroot_, m.SysrootErr }
func (m *MockOstree) Remote() (string, error)                    { return m.Remote_, m.RemoteErr }
func (m *MockOstree) RemoteURL() (string, error)                 { return "", nil }
func (m *MockOstree) AvailableGpgPubKeyPaths() ([]string, error) { return nil, nil }
func (m *MockOstree) GpgBestPubKeyPath() (string, error) {
	return m.GpgBestPubKeyPath_, m.GpgBestPubKeyPathErr
}
func (m *MockOstree) ClientSideGpgArgs() ([]string, error) { return nil, nil }
func (m *MockOstree) GpgHomeDir() (string, error)          { return "", nil }
func (m *MockOstree) GpgKeyID() (string, error)            { return "", nil }
func (m *MockOstree) FancyOsName() (string, error)         { return m.FancyOsName_, m.FancyOsNameErr }
func (m *MockOstree) GpgArgs() ([]string, error)           { return m.GpgArgs_, m.GpgArgsErr }
func (m *MockOstree) SetGpg(enabled bool) error {
	m.SetGpgCalled = true
	m.SetGpgArg = enabled
	return m.SetGpgErr
}
func (m *MockOstree) SetVerbose(_ bool)     {}
func (m *MockOstree) SetupEtc(string) error { return nil }
func (m *MockOstree) PrepareFilesystemHierarchy(string) error {
	return m.PrepareFilesystemHierarchyErr
}
func (m *MockOstree) ValidateFilesystemHierarchy(string) error {
	return m.ValidateFilesystemHierarchyErr
}
func (m *MockOstree) BootCommit(string) (string, error) {
	if m.BootCommitErr != nil {
		return "", m.BootCommitErr
	}
	if m.BootCommitResult != "" {
		return m.BootCommitResult, nil
	}
	return "abc123commit", nil
}
func (m *MockOstree) Commit(opts CommitOptions) error {
	m.CommitCalled = true
	m.CommitOpts = opts
	return m.CommitErr
}
func (m *MockOstree) InitRepo() error {
	m.InitRepoCalled = true
	return m.InitRepoErr
}
func (m *MockOstree) ImportGpgKey(string) error { return nil }
func (m *MockOstree) GpgSignFile(string) error       { return nil }
func (m *MockOstree) GpgKeys() ([]string, error)     { return nil, nil }
func (m *MockOstree) InitializeSigningGpg() error    { return nil }
func (m *MockOstree) MaybeInitializeGpg() error      { return nil }
func (m *MockOstree) KillGpgDaemons()                {}
func (m *MockOstree) MaybeInitializeRemote() error   { return nil }
func (m *MockOstree) Pull() error                    { return nil }
func (m *MockOstree) Prune() error {
	m.PruneCalled = true
	return m.PruneErr
}
func (m *MockOstree) GenerateStaticDelta() error {
	m.GenerateStaticDeltaCalled = true
	return m.GenerateStaticDeltaErr
}
func (m *MockOstree) UpdateSummary() error {
	m.UpdateSummaryCalled = true
	return m.UpdateSummaryErr
}
func (m *MockOstree) AddRemote() error               { return nil }
func (m *MockOstree) AddRemoteToRootfs(string) error { return nil }
func (m *MockOstree) LocalRefs() ([]string, error) {
	return m.LocalRefs_, m.LocalRefsErr
}
func (m *MockOstree) ListContents(string, string) (*[]filesystems.PathInfo, error) {
	return nil, nil
}
func (m *MockOstree) ListEtcChanges(string, string) ([]EtcChange, error) { return nil, nil }
func (m *MockOstree) DeployedRootfs() (string, error)                    { return "", nil }
func (m *MockOstree) BootedRef() (string, error) { return "", nil }
func (m *MockOstree) PostCopy() error {
	m.PostCopyCalled = true
	return m.PostCopyErr
}
func (m *MockOstree) Deploy(string, []string) error { return nil }
func (m *MockOstree) Readwrite(permanent bool) error {
	m.ReadwriteCalled = true
	m.ReadwritePermanent = permanent
	return m.ReadwriteErr
}

// Methods with configurable behavior for tests.
func (m *MockOstree) Root() (string, error) {
	if m.Root_ == "" {
		return "/", m.RootErr
	}
	return m.Root_, m.RootErr
}

func (m *MockOstree) ListDeployments() ([]Deployment, error) {
	return m.Deployments, m.DeploymentsErr
}

func (m *MockOstree) RemoteRefs() ([]string, error) {
	return m.Refs, m.RefsErr
}

func (m *MockOstree) Switch() error {
	m.SwitchRef = m.Ref_
	return m.SwitchErr
}

func (m *MockOstree) LastCommit() (string, error) {
	return m.LastCommit_, m.LastCommitErr
}

func (m *MockOstree) Upgrade(args []string) error {
	m.UpgradeArgs = args
	return m.UpgradeErr
}

func (m *MockOstree) Pin(targetIndex int) error {
	for _, dep := range m.Deployments {
		if dep.Index == targetIndex {
			m.PinChecksum = dep.Checksum
			m.PinIndex = dep.Index
			return m.PinErr
		}
	}
	return m.PinNotFoundErr
}

func (m *MockOstree) Unpin(targetIndex int) error {
	if m.PinIndex != targetIndex {
		return errors.New("not previously pinned")
	}
	found := false
	for _, dep := range m.Deployments {
		if dep.Index == targetIndex {
			found = true
		}
	}
	if !found {
		return m.PinNotFoundErr
	}

	m.PinIndex = -1
	return m.PinErr
}

func (m *MockOstree) ListPackages(commit string) ([]string, error) {
	if m.PackagesByCommit != nil {
		if pkgs, ok := m.PackagesByCommit[commit]; ok {
			return pkgs, m.PackagesErr
		}
	}
	return m.Packages, m.PackagesErr
}
