package seeder

import (
	"fmt"
	"io"
	"os"
)

// Compile-time interface check.
var _ ISeeder = (*MockSeeder)(nil)

// MockSeeder implements ISeeder for testing commands.
// Only the fields/methods relevant to each test need to be configured;
// everything else returns safe zero values.
type MockSeeder struct {
	stdout io.Writer
	stderr io.Writer

	// Params
	ParseSeederParams_   *SeederParams
	ParseSeederParamsErr error

	// Done-flag management
	SeederDoneFlagFile_   string
	SeederDoneFlagFileErr error
	IsSeederDone_         bool
	IsSeederDoneErr       error
	MarkSeederDoneErr     error

	// Lifecycle / execution
	MaybeInitializePrivateRepoErr error
	ImportGentooGpgKeysErr        error
	ExecutePrepperErr             error
	SetupChrootDNSErr             error
	SetupChrootDirsErr            error
	SeedErr                       error
	PostBuildErr                  error
	ExecuteWithSeederLockErr      error

	// Track calls
	MaybeInitializePrivateRepoCalled bool
	ImportGentooGpgKeysCalled        bool
	KillGpgDaemonsCalled             bool
	ExecutePrepperCalled             bool
	CleanupCalled                    bool
	SetupChrootDNSCalled             bool
	SetupChrootDirsCalled            bool
	SeedCalled                       bool
	PostBuildCalled                  bool
	MarkSeederDoneCalled             bool
	ExecuteWithSeederLockCalled      bool
	ExecuteWithSeederLockName        string
}

// DefaultMockSeeder returns a MockSeeder with sensible defaults for tests.
func DefaultMockSeeder() *MockSeeder {
	return &MockSeeder{}
}

func (m *MockSeeder) SetStdout(w io.Writer) { m.stdout = w }
func (m *MockSeeder) SetStderr(w io.Writer) { m.stderr = w }
func (m *MockSeeder) Stdout() io.Writer {
	if m.stdout == nil {
		return os.Stdout
	}
	return m.stdout
}
func (m *MockSeeder) Stderr() io.Writer {
	if m.stderr == nil {
		return os.Stderr
	}
	return m.stderr
}

func (m *MockSeeder) Print(format string, args ...any) {
	fmt.Fprintf(m.Stdout(), format, args...)
}
func (m *MockSeeder) PrintWarning(format string, args ...any) {
	fmt.Fprintf(m.Stderr(), format, args...)
}
func (m *MockSeeder) PrintError(format string, args ...any) {
	fmt.Fprintf(m.Stderr(), format, args...)
}

func (m *MockSeeder) ParseSeederParams(
	info SeederInfo,
) (*SeederParams, error) {
	return m.ParseSeederParams_, m.ParseSeederParamsErr
}

func (m *MockSeeder) SeederDoneFlagFile(
	name, chrootDir string,
) (string, error) {
	return m.SeederDoneFlagFile_, m.SeederDoneFlagFileErr
}
func (m *MockSeeder) IsSeederDone(
	name, chrootDir string,
) (bool, error) {
	return m.IsSeederDone_, m.IsSeederDoneErr
}
func (m *MockSeeder) MarkSeederDone(
	name, chrootDir string,
) error {
	m.MarkSeederDoneCalled = true
	return m.MarkSeederDoneErr
}

func (m *MockSeeder) MaybeInitializePrivateRepo() error {
	m.MaybeInitializePrivateRepoCalled = true
	return m.MaybeInitializePrivateRepoErr
}

func (m *MockSeeder) ImportGentooGpgKeys() error {
	m.ImportGentooGpgKeysCalled = true
	return m.ImportGentooGpgKeysErr
}

func (m *MockSeeder) KillGpgDaemons() {
	m.KillGpgDaemonsCalled = true
}

func (m *MockSeeder) ExecutePrepper(
	info SeederInfo, params *SeederParams, opts *PrepperOptions,
) error {
	m.ExecutePrepperCalled = true
	return m.ExecutePrepperErr
}

func (m *MockSeeder) Cleanup() {
	m.CleanupCalled = true
}

func (m *MockSeeder) SetupChrootDNS(chrootDir string) error {
	m.SetupChrootDNSCalled = true
	return m.SetupChrootDNSErr
}

func (m *MockSeeder) SetupChrootDirs(chrootDir string) error {
	m.SetupChrootDirsCalled = true
	return m.SetupChrootDirsErr
}

func (m *MockSeeder) Seed(*SeedOptions) error {
	m.SeedCalled = true
	return m.SeedErr
}

func (m *MockSeeder) PostBuild(*SeedOptions) error {
	m.PostBuildCalled = true
	return m.PostBuildErr
}

func (m *MockSeeder) ExecuteWithSeederLock(name string, fn func() error) error {
	m.ExecuteWithSeederLockCalled = true
	m.ExecuteWithSeederLockName = name
	if m.ExecuteWithSeederLockErr != nil {
		return m.ExecuteWithSeederLockErr
	}
	return fn()
}
