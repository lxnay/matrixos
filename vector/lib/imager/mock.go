package imager

import (
	"fmt"
	"io"
	"os"
)

// MockBootloader implements Bootloader for testing.
type MockBootloader struct {
	ConfigureCalled         bool
	ConfigureErr            error
	InstallCalled           bool
	InstallErr              error
	ConfigureMemtestCalled  bool
	ConfigureMemtestBin     string
	ConfigureMemtestErr     error
	ConfigureVmtestCalled   bool
	ConfigureVmtestErr      error
}

func (m *MockBootloader) Configure() error {
	m.ConfigureCalled = true
	return m.ConfigureErr
}

func (m *MockBootloader) Install() error {
	m.InstallCalled = true
	return m.InstallErr
}

func (m *MockBootloader) BootArgs() ([]string, error) {
	return []string{}, nil
}

func (m *MockBootloader) ConfigureMemtest(memtestBin string) error {
	m.ConfigureMemtestCalled = true
	m.ConfigureMemtestBin = memtestBin
	return m.ConfigureMemtestErr
}

func (m *MockBootloader) ConfigureVmtest() error {
	m.ConfigureVmtestCalled = true
	return m.ConfigureVmtestErr
}

// MockImager implements IImager for testing.
// Only the fields/methods relevant to each test need to be configured;
// everything else returns safe zero values.
type MockImager struct {
	Ref_ string

	// Track calls
	BuildCalled   bool
	BuildErr      error
	CleanupCalled bool

	// ExecuteWithImageLock support
	ExecuteWithImageLockCalled bool
	ExecuteWithImageLockErr    error

	// I/O writers for Print methods
	stdout io.Writer
	stderr io.Writer
}

func (m *MockImager) SetStdout(w io.Writer) { m.stdout = w }
func (m *MockImager) SetStderr(w io.Writer) { m.stderr = w }
func (m *MockImager) Print(format string, a ...any) {
	w := m.stdout
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintf(w, format, a...)
}
func (m *MockImager) PrintWarning(format string, a ...any) {
	w := m.stderr
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, format, a...)
}
func (m *MockImager) PrintError(format string, a ...any) {
	w := m.stderr
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, format, a...)
}

func (m *MockImager) SetRef(ref string)              { m.Ref_ = ref }
func (m *MockImager) Build(opts *BuildOptions) error { m.BuildCalled = true; return m.BuildErr }
func (m *MockImager) Cleanup()                       { m.CleanupCalled = true }

// ExecuteWithImageLock either calls fn directly or returns the configured error.
func (m *MockImager) ExecuteWithImageLock(fn func() error) error {
	m.ExecuteWithImageLockCalled = true
	if m.ExecuteWithImageLockErr != nil {
		return m.ExecuteWithImageLockErr
	}
	return fn()
}

// DefaultMockImager returns a MockImager pre-configured with sensible defaults for testing.
func DefaultMockImager() *MockImager {
	return &MockImager{}
}
