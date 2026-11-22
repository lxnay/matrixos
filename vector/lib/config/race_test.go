package config

import (
	"os"
	"path/filepath"
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

func TestIniConfig_ReloadIsAtomicAndDoesNotDuplicate(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "matrixos.conf")
	content := "[matrixOS]\nRoot=" + tmp + "\nDefaultRoot=" + tmp + "\nConfRoot=" + tmp + "\nArtifactsRoot=" + tmp + "\n[Section]\nKey=v1\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := NewIniConfigFromPath(&ConfigFromPathParams{
		ConfigPath:    cfgPath,
		DefaultRoot:   tmp,
		ConfRoot:      tmp,
		ArtifactsRoot: tmp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Load(); err != nil {
		t.Fatalf("first Load: %v", err)
	}

	// Reloading the same config must not duplicate value history.
	if err := cfg.Load(); err != nil {
		t.Fatalf("second Load: %v", err)
	}
	items, err := cfg.GetItems("Section.Key")
	if err != nil {
		t.Fatalf("GetItems: %v", err)
	}
	if len(items) != 1 || items[0] != "v1" {
		t.Errorf("after reload, Section.Key items = %v, want [v1]", items)
	}

	// A failing reload (broken subconfig dir/file) must not corrupt previously loaded state.
	subDir := cfgPath + ".d"
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Create a dangling .conf symlink to force subconfig LoadConfig to fail
	// (works even when tests run as root).
	badSub := filepath.Join(subDir, "99-bad.conf")
	if err := os.Symlink(filepath.Join(tmp, "does-not-exist.conf"), badSub); err != nil {
		t.Fatal(err)
	}
	// Change main config Key=v2 so partial mutation would overwrite v1 with v2.
	contentV2 := "[matrixOS]\nRoot=" + tmp + "\nDefaultRoot=" + tmp + "\nConfRoot=" + tmp + "\nArtifactsRoot=" + tmp + "\n[Section]\nKey=v2\n"
	if err := os.WriteFile(cfgPath, []byte(contentV2), 0644); err != nil {
		t.Fatal(err)
	}

	if err := cfg.Load(); err == nil {
		t.Fatal("expected error from malformed subconfig")
	}
	got, err := cfg.GetItem("Section.Key")
	if err != nil {
		t.Fatalf("GetItem after failed reload: %v", err)
	}
	if got != "v1" {
		t.Errorf("failed reload mutated config state: Section.Key = %q, want %q", got, "v1")
	}
}
