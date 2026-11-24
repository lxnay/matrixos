package imager

import "matrixos/vector/lib/worker"

// Compile-time interface checks.
var (
	_ Bootloader = (*MockBootloader)(nil)
	_ IImager    = (*MockImager)(nil)
)

// MockBootloader implements Bootloader for testing.
type MockBootloader struct {
	ConfigureCalled        bool
	ConfigureErr           error
	InstallCalled          bool
	InstallErr             error
	ConfigureMemtestCalled bool
	ConfigureMemtestErr    error
	ConfigureVmtestCalled  bool
	ConfigureVmtestErr     error
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

func (m *MockBootloader) ConfigureMemtest() error {
	m.ConfigureMemtestCalled = true
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
	worker.IO

	Ref_ string

	// Track calls
	BuildCalled   bool
	BuildErr      error
	CleanupCalled bool

	// ExecuteWithImageLock support
	ExecuteWithImageLockCalled bool
	ExecuteWithImageLockErr    error
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
