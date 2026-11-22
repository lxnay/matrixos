package filesystems

// MockFsenc implements IFsenc for testing.
// Only the fields/methods relevant to each test need to be configured;
// everything else returns safe zero values.
type MockFsenc struct {
	EncryptionEnabled_     bool
	EncryptionEnabledErr   error
	EncryptionKey_         string
	EncryptionKeyErr       error
	EncryptedRootFsName_   string
	EncryptedRootFsNameErr error
	ValidateLuksErr        error
	LuksEncryptErr         error
}

func (m *MockFsenc) Cleanup() {}

func (m *MockFsenc) EncryptionEnabled() (bool, error) {
	return m.EncryptionEnabled_, m.EncryptionEnabledErr
}

func (m *MockFsenc) EncryptionKey() (string, error) {
	return m.EncryptionKey_, m.EncryptionKeyErr
}

func (m *MockFsenc) EncryptedRootFsName() (string, error) {
	return m.EncryptedRootFsName_, m.EncryptedRootFsNameErr
}

func (m *MockFsenc) LuksEncrypt(devicePath, desiredLuksDevice string) error {
	return m.LuksEncryptErr
}

func (m *MockFsenc) ValidateLuksVariables() error {
	return m.ValidateLuksErr
}

// DefaultMockFsenc returns a MockFsenc with sensible defaults for testing.
func DefaultMockFsenc() *MockFsenc {
	return &MockFsenc{
		EncryptionEnabled_:   false,
		EncryptedRootFsName_: "crypt-root",
	}
}
