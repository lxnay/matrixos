package filesystems

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCopyFile_ConcurrentSameDest(t *testing.T) {
	tmp := t.TempDir()
	src1 := filepath.Join(tmp, "src1")
	src2 := filepath.Join(tmp, "src2")
	dst := filepath.Join(tmp, "dst")
	if err := os.WriteFile(src1, []byte("aaaa-content-1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src2, []byte("bbbb-content-2"), 0644); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for _, src := range []string{src1, src2} {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			for range 50 {
				if err := CopyFile(s, dst); err != nil {
					errs <- err
					return
				}
			}
		}(src)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent CopyFile failed: %v", err)
	}
}
