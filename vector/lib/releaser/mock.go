package releaser

import "matrixos/vector/lib/worker"

// Compile-time interface check.
var _ IRelease = (*MockReleaser)(nil)

// MockReleaser implements IRelease for testing commands.
// Only the fields/methods relevant to each test need to be configured;
// everything else returns safe zero values.
type MockReleaser struct {
	worker.IO

	// Build / Cleanup
	BuildErr    error
	BuildCalled bool

	CleanupCalled bool

	// Locking
	ExecuteWithReleaseLockErr error
}

// DefaultMockReleaser returns a MockReleaser with sensible defaults for tests.
func DefaultMockReleaser() *MockReleaser {
	return &MockReleaser{}
}

func (m *MockReleaser) ExecuteWithReleaseLock(fn func() error) error {
	if m.ExecuteWithReleaseLockErr != nil {
		return m.ExecuteWithReleaseLockErr
	}
	return fn()
}

func (m *MockReleaser) Build() error {
	m.BuildCalled = true
	return m.BuildErr
}

func (m *MockReleaser) Cleanup() {
	m.CleanupCalled = true
}
