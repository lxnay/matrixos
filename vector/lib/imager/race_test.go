package imager

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
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
	// Barrier in PushCleanup (called per-worker before setupMu) ensures all
	// workers pass pre-setup ctx checks before any worker can fail in im.Build.
	var readyWG sync.WaitGroup
	readyWG.Add(len(refs))

	opts := &ParallelImageOptions{
		Refs:            refs,
		Parallelism:     4,
		Config:          cfg,
		NewStdoutWriter: func(string) io.Writer { return io.Discard },
		NewStderrWriter: func(string) io.Writer { return io.Discard },
		PushCleanup: func(func()) {
			readyWG.Done()
			readyWG.Wait()
		},
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

func TestNewImager_NilOptsPreservesEncryptedFlag(t *testing.T) {
	fsenc := filesystems.DefaultMockFsenc()
	fsenc.EncryptionEnabled_ = true

	im, err := NewImager(baseImageConfig(), &ostree.MockOstree{}, fsenc, nil)
	if err != nil {
		t.Fatalf("NewImager: %v", err)
	}
	if !im.encrypted {
		t.Error("NewImager with nil opts dropped encrypted=true from fsenc.EncryptionEnabled()")
	}
}

func TestImagerCleanup_ClosesFsencBeforeDetachingLoop(t *testing.T) {
	cfg := baseImageConfig()
	var events []string

	fsenc := &orderTrackingFsenc{
		MockFsenc: filesystems.DefaultMockFsenc(),
		onCleanup: func() {
			events = append(events, "fsenc.Cleanup")
		},
	}
	im, err := NewImager(cfg, &ostree.MockOstree{}, fsenc, nil)
	if err != nil {
		t.Fatalf("NewImager: %v", err)
	}
	im.SetStdout(io.Discard)
	im.SetStderr(io.Discard)

	im.Cleanup()

	if len(events) != 1 || events[0] != "fsenc.Cleanup" {
		t.Errorf("Imager.Cleanup did not invoke fsenc.Cleanup before detaching loops/syncing; events = %v", events)
	}
}

type orderTrackingFsenc struct {
	*filesystems.MockFsenc
	onCleanup func()
}

func (o *orderTrackingFsenc) Cleanup() {
	if o.onCleanup != nil {
		o.onCleanup()
	}
}

func TestParallelImage_NonPositiveParallelismReturnsError(t *testing.T) {
	var stderrBuf bytes.Buffer
	opts := testParallelImageOpts([]string{"matrixos/amd64/dev/bedrock"})
	opts.Parallelism = 0
	opts.NewStderrWriter = func(string) io.Writer { return &stderrBuf }

	if err := ParallelImage(context.Background(), opts); err == nil {
		t.Fatalf("expected error for Parallelism <= 0, got nil (stderr: %q)", stderrBuf.String())
	}
}

func TestParallelImage_CancelsInFlightWorkersOnFirstError(t *testing.T) {
	tmp := t.TempDir()
	refs := []string{"matrixos/amd64/dev/fail", "matrixos/amd64/dev/slow"}
	cfg := baseImageConfig()
	cfg.Items["Imager.LocksDir"] = []string{tmp}
	cfg.Items["Imager.LockWaitSeconds"] = []string{"5"}
	cfg.Items["Ostree.FullBranchSuffix"] = []string{"full"}

	failReturned := make(chan struct{})
	var slowReachedSetup atomic.Bool

	opts := &ParallelImageOptions{
		Refs:        refs,
		Parallelism: 2,
		Config:      cfg,
		NewStdoutWriter: func(label string) io.Writer {
			if label == "image:matrixos/amd64/dev/slow" {
				// Hold slow worker in flight until fail worker fails SetupBuild
				// and setError cancels poolCtx.
				<-failReturned
				time.Sleep(30 * time.Millisecond)
			}
			return io.Discard
		},
		NewStderrWriter: func(string) io.Writer { return io.Discard },
		PushCleanup:     func(func()) {},
		SetupBuild: func(pushCleanup func(func()), ot ostree.IOstree, im IImager) error {
			if ot.Ref() == "matrixos/amd64/dev/fail" {
				defer close(failReturned)
				return errors.New("setup boom")
			}
			slowReachedSetup.Store(true)
			return nil
		},
	}

	err := ParallelImage(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error from failing image worker")
	}
	if slowReachedSetup.Load() {
		t.Fatal("in-flight sibling worker reached SetupBuild despite earlier failure (pool context not cancelled)")
	}
}
