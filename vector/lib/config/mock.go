package config

import (
	"slices"
	"sync"
)

// Compile-time interface checks.
var (
	_ IConfig = (*MockConfig)(nil)
	_ IConfig = (*ErrConfig)(nil)
)

// MockConfig is a test-only implementation of IConfig backed by in-memory maps.
// It is exported so that other packages can use it in their tests without
// duplicating the mock.
//
// Usage:
//
//	cfg := &config.MockConfig{
//	    Items: map[string][]string{
//	        "Section.Key": {"value"},
//	    },
//	    Bools: map[string]bool{
//	        "Section.Flag": true,
//	    },
//	}
type MockConfig struct {
	mu    sync.RWMutex
	Items map[string][]string
	Bools map[string]bool
}

// Load is a no-op for the mock.
func (m *MockConfig) Load() error { return nil }

// Clone returns a deep copy of the mock config.
func (m *MockConfig) Clone() IConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()

	newItems := make(map[string][]string, len(m.Items))
	for k, v := range m.Items {
		newItems[k] = slices.Clone(v)
	}

	newBools := make(map[string]bool, len(m.Bools))
	for k, v := range m.Bools {
		newBools[k] = v
	}

	return &MockConfig{
		Items: newItems,
		Bools: newBools,
	}
}

// AddOverlay adds an overlay of config values that take precedence over the loaded config file.
func (m *MockConfig) AddOverlay(overlay map[string][]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Items == nil {
		m.Items = make(map[string][]string)
	}
	for k, v := range overlay {
		if vals, ok := m.Items[k]; ok {
			m.Items[k] = append(vals, v...)
		} else {
			m.Items[k] = slices.Clone(v)
		}
		if len(v) > 0 {
			last := v[len(v)-1]
			if last == "true" || last == "false" {
				if m.Bools == nil {
					m.Bools = make(map[string]bool)
				}
				m.Bools[k] = last == "true"
			} else if m.Bools != nil {
				if _, exists := m.Bools[k]; exists {
					m.Bools[k] = false
				}
			}
		}
	}
	return nil
}

// GetItem returns the last value from the Items map for the given key,
// or "" if the key is absent. This matches the real config behavior where
// the last value wins when multiple values exist.
func (m *MockConfig) GetItem(key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if lst, ok := m.Items[key]; ok {
		var val string
		if len(lst) > 0 {
			val = lst[len(lst)-1]
		}
		return val, nil
	}
	return "", nil
}

// GetBool returns the boolean value from the Bools map for the given key.
func (m *MockConfig) GetBool(key string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if val, ok := m.Bools[key]; ok {
		return val, nil
	}
	return false, nil
}

// ErrConfig is a test-only IConfig that returns the configured error for every
// method call. Useful for testing error-propagation paths.
//
// Usage:
//
//	cfg := &config.ErrConfig{Err: errors.New("broken")}
type ErrConfig struct{ Err error }

// Load returns the configured error.
func (e *ErrConfig) Load() error { return e.Err }

// Clone returns a new ErrConfig with the same error.
func (e *ErrConfig) Clone() IConfig { return &ErrConfig{Err: e.Err} }

// AddOverlay returns the configured error.
func (e *ErrConfig) AddOverlay(map[string][]string) error { return e.Err }

// GetItem returns ("", Err).
func (e *ErrConfig) GetItem(string) (string, error) { return "", e.Err }

// GetBool returns (false, Err).
func (e *ErrConfig) GetBool(string) (bool, error) { return false, e.Err }
