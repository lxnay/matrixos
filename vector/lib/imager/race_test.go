package imager

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/ostree"
)

func TestParallelImage_ConcurrentPoolStderrSkip(t *testing.T) {
	var stderrBuf bytes.Buffer
	refs := []string{"r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8", "r9", "r10", "r11", "r12"}
	// Empty config causes imageWorker to fail immediately while other workers
	// concurrently log "Skipping ref ..." to poolStderr.
	cfg := &config.MockConfig{Items: map[string][]string{}}

	opts := &ParallelImageOptions{
		Refs:        refs,
		Parallelism: 6,
		Config:      cfg,
		NewStdoutWriter: func(label string) io.Writer {
			return io.Discard
		},
		NewStderrWriter: func(label string) io.Writer {
			if label == "images" {
				return &stderrBuf
			}
			return io.Discard
		},
	}

	err := ParallelImage(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error from ParallelImage")
	}
}

func TestParallelImage_UniqueLuksMapperNames(t *testing.T) {
	tmp := t.TempDir()
	refs := []string{"matrixos/amd64/dev/gnome", "matrixos/amd64/dev/cosmic", "matrixos/amd64/dev/server", "matrixos/amd64/dev/bedrock"}
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Imager.EncryptionKey":       {"secret-passphrase"},
			"Imager.EncryptedRootFsName": {"matrixosroot"},
			"Imager.LocksDir":            {tmp},
			"Imager.LockWaitSeconds":     {"5"},
			"Ostree.FullBranchSuffix":    {"full"},
		},
		Bools: map[string]bool{
			"Imager.Encryption": true,
		},
	}

	var mu sync.Mutex
	seen := make(map[string]string)

	opts := &ParallelImageOptions{
		Refs:            refs,
		Parallelism:     4,
		Config:          cfg,
		NewStdoutWriter: func(string) io.Writer { return io.Discard },
		NewStderrWriter: func(string) io.Writer { return io.Discard },
		PushCleanup:     func(func()) {},
		SetupBuild: func(pushCleanup func(func()), ot ostree.IOstree, im IImager) error {
			concrete := im.(*Imager)
			name, err := concrete.fsenc.EncryptedRootFsName()
			if err != nil {
				return err
			}
			mu.Lock()
			seen[ot.Ref()] = name
			mu.Unlock()
			return nil
		},
	}

	_ = ParallelImage(context.Background(), opts)
	if len(seen) != len(refs) {
		t.Fatalf("expected all %d refs to reach SetupBuild, got %d (%v)", len(refs), len(seen), seen)
	}
	unique := make(map[string]bool)
	for _, name := range seen {
		unique[name] = true
	}
	if len(unique) != len(seen) {
		t.Errorf("LUKS mapper name collision across parallel workers: %v", seen)
	}
}
