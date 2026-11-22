package config

import (
	"sync"
	"testing"
)

func TestIniConfig_ConcurrentGetItemsAndAddOverlay(t *testing.T) {
	// Pre-allocate slice with spare capacity so append in AddOverlay writes
	// into the backing array returned earlier by GetItems if uncloned.
	backing := make([]string, 1, 16)
	backing[0] = "v0"
	cfg := &IniConfig{
		cfg: map[string][]string{
			"A.Key": backing,
		},
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 200 {
			items, err := cfg.GetItems("A.Key")
			if err == nil && len(items) > 0 {
				// Simulate caller mutating/reading returned slice.
				items[0] = "caller-mutated"
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			_ = cfg.AddOverlay(map[string][]string{"A.Key": {"v1"}})
			_, _ = cfg.GetItem("A.Key")
		}
	}()
	wg.Wait()
}

func TestMockConfig_ConcurrentAccess(t *testing.T) {
	cfg := &MockConfig{
		Items: map[string][]string{"A.Key": {"v0"}},
		Bools: map[string]bool{"A.Flag": true},
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for range 200 {
			_ = cfg.AddOverlay(map[string][]string{"A.Key": {"v1"}})
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			_, _ = cfg.GetItem("A.Key")
			_, _ = cfg.GetItems("A.Key")
			_, _ = cfg.GetBool("A.Flag")
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			_ = cfg.Clone()
		}
	}()
	wg.Wait()
}
