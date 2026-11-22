package ostree

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
)

func TestOstree_GpgConcurrentSerialization(t *testing.T) {
	tmp := t.TempDir()
	gpgHome := filepath.Join(tmp, "gnupg")
	pubKey := filepath.Join(tmp, "pub.asc")
	payload := filepath.Join(tmp, "payload.bin")
	if err := os.WriteFile(pubKey, []byte("pub"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payload, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.DevGpgHomeDir":        {gpgHome},
			"Ostree.GpgPublicKey":         {pubKey},
			"Ostree.GpgOfficialPublicKey": {pubKey},
		},
	}

	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	mockRunner := func(cmd *runner.Cmd) error {
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		inFlight.Add(-1)
		if cmd.Stdout != nil {
			fmt.Fprintln(cmd.Stdout, "pub:u:4096:1:AABBCCDDEEFF0011:1700000000:::")
		}
		return nil
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, err := NewOstree(NewOstreeOptions{Config: cfg})
			if err != nil {
				t.Errorf("NewOstree: %v", err)
				return
			}
			o.runner = mockRunner
			if _, err := o.GpgKeyID(); err != nil {
				t.Errorf("GpgKeyID: %v", err)
			}
			if err := o.GpgSignFile(payload); err != nil {
				t.Errorf("GpgSignFile: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := maxInFlight.Load(); got > 1 {
		t.Errorf("concurrent gpg invocations detected: maxInFlight = %d, want 1", got)
	}
}
