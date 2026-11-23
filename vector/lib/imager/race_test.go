package imager

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
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

func TestImagerCleanup_ConcurrentCallsSerializeTmpDirRemoval(t *testing.T) {
	cfg := baseImageConfig()
	tmpDir := t.TempDir()

	inFsenc := make(chan struct{})
	releaseFsenc := make(chan struct{})
	var fsencCalls atomic.Int32
	var tmpDirExistedDuringFirstCleanup atomic.Bool

	fsenc := &orderTrackingFsenc{
		MockFsenc: filesystems.DefaultMockFsenc(),
		onCleanup: func() {
			if fsencCalls.Add(1) == 1 {
				close(inFsenc)
				<-releaseFsenc
				if _, err := os.Stat(tmpDir); err == nil {
					tmpDirExistedDuringFirstCleanup.Store(true)
				}
			}
		},
	}

	im, err := NewImager(cfg, &ostree.MockOstree{}, fsenc, nil)
	if err != nil {
		t.Fatalf("NewImager: %v", err)
	}
	im.SetStdout(io.Discard)
	im.SetStderr(io.Discard)
	im.trackTmpDir(tmpDir)

	call1Done := make(chan struct{})
	go func() {
		im.Cleanup()
		close(call1Done)
	}()

	<-inFsenc

	call2Done := make(chan struct{})
	go func() {
		im.Cleanup()
		close(call2Done)
	}()

	// On buggy code without cleanupMu, call 2 completes and removes tmpDir
	// while call 1 is still blocked in fsenc.Cleanup().
	select {
	case <-call2Done:
		close(releaseFsenc)
		<-call1Done
		t.Fatal("second concurrent Imager.Cleanup() completed while first Cleanup() was still in progress")
	case <-time.After(400 * time.Millisecond):
		close(releaseFsenc)
	}

	<-call1Done
	<-call2Done

	if !tmpDirExistedDuringFirstCleanup.Load() {
		t.Fatal("second concurrent Imager.Cleanup() deleted tmpDir while first Cleanup() was still closing mounts/LUKS")
	}
}

func TestImagerMount_CleanupDuringInFlightMountAborts(t *testing.T) {
	cfg := baseImageConfig()
	im := newTestImager(cfg, &ostree.MockOstree{})
	im.SetStdout(io.Discard)
	im.SetStderr(io.Discard)
	im.SetRootDevice("/dev/loop0p3")

	mountDir := t.TempDir()
	inMount := make(chan struct{})
	releaseMount := make(chan struct{})
	im.runner = func(cmd *runner.Cmd) error {
		close(inMount)
		<-releaseMount
		return nil
	}

	mountErrCh := make(chan error, 1)
	go func() {
		mountErrCh <- im.MountRootfs(mountDir)
	}()

	<-inMount
	// Signal-driven Cleanup runs and completes while mount is still in flight.
	im.Cleanup()
	close(releaseMount)

	if err := <-mountErrCh; err == nil {
		t.Fatal("MountRootfs succeeded even though Cleanup() ran while mount was in flight (TOCTOU mount leak)")
	}
}

type flushTrackingWriter struct {
	flushed atomic.Bool
}

func (w *flushTrackingWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *flushTrackingWriter) Flush()                      { w.flushed.Store(true) }

func TestParallelImage_FlushesPoolAndWorkerWriters(t *testing.T) {
	poolStderr := &flushTrackingWriter{}
	workerStdout := &flushTrackingWriter{}

	opts := &ParallelImageOptions{
		Refs:        []string{"matrixos/amd64/dev/bedrock"},
		Parallelism: 1,
		Config:      &config.MockConfig{Items: map[string][]string{}},
		NewStdoutWriter: func(label string) io.Writer {
			if label == "images:matrixos/amd64/dev/bedrock" {
				return workerStdout
			}
			return io.Discard
		},
		NewStderrWriter: func(label string) io.Writer {
			if label == "images" {
				return poolStderr
			}
			return io.Discard
		},
		PushCleanup: func(func()) {},
	}

	_ = ParallelImage(context.Background(), opts)

	if !poolStderr.flushed.Load() {
		t.Error("expected poolStderr writer to be flushed")
	}
	if !workerStdout.flushed.Load() {
		t.Error("expected workerStdout writer to be flushed")
	}
}

func TestImagerCleanup_RetainsTmpDirWhenActiveMountRemains(t *testing.T) {
	cfg := baseImageConfig()
	im := newTestImager(cfg, &ostree.MockOstree{})
	im.SetStdout(io.Discard)
	im.SetStderr(io.Discard)

	tmpDir := t.TempDir()
	marker := tmpDir + "/keep.txt"
	if err := os.WriteFile(marker, []byte("precious"), 0644); err != nil {
		t.Fatal(err)
	}
	im.trackTmpDir(tmpDir)

	origReadMountInfo := filesystems.ReadMountInfo
	filesystems.ReadMountInfo = func() ([]*filesystems.MountInfoEntry, error) {
		return []*filesystems.MountInfoEntry{
			{Mountpoint: tmpDir, Source: "/dev/loop99p3", FSType: "btrfs"},
		}, nil
	}
	t.Cleanup(func() { filesystems.ReadMountInfo = origReadMountInfo })

	im.Cleanup()

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("Imager.Cleanup removed tmpDir %s while an active mount was present: %v", tmpDir, err)
	}
	im.trackMu.Lock()
	retained := len(im.trackedTmpDirs)
	im.trackMu.Unlock()
	if retained != 1 {
		t.Fatalf("expected tmpDir with active mount to stay retained in trackedTmpDirs, got %d", retained)
	}
}

func TestImagerCleanup_UnmountsLateReTrackedMountBeforeRemovingTmpDir(t *testing.T) {
	cfg := baseImageConfig()
	tmpDir := t.TempDir()
	marker := tmpDir + "/data.txt"
	if err := os.WriteFile(marker, []byte("mounted-data"), 0644); err != nil {
		t.Fatal(err)
	}

	var mounted atomic.Bool
	var unmountedBeforeDelete atomic.Bool

	origReadMountInfo := filesystems.ReadMountInfo
	origUnmount := filesystems.Unmount
	filesystems.ReadMountInfo = func() ([]*filesystems.MountInfoEntry, error) {
		if mounted.Load() {
			return []*filesystems.MountInfoEntry{
				{Mountpoint: tmpDir, Source: "/dev/loop99p3", FSType: "btrfs"},
			}, nil
		}
		return nil, nil
	}
	filesystems.Unmount = func(target string, flags int) error {
		if target == tmpDir {
			if _, err := os.Stat(marker); err == nil {
				unmountedBeforeDelete.Store(true)
			}
			mounted.Store(false)
		}
		return nil
	}
	t.Cleanup(func() {
		filesystems.ReadMountInfo = origReadMountInfo
		filesystems.Unmount = origUnmount
	})

	inFsenc := make(chan struct{})
	releaseFsenc := make(chan struct{})
	var fsencCalls atomic.Int32

	fsenc := &orderTrackingFsenc{
		MockFsenc: filesystems.DefaultMockFsenc(),
		onCleanup: func() {
			if fsencCalls.Add(1) == 1 {
				close(inFsenc)
				<-releaseFsenc
			}
		},
	}

	im, err := NewImager(cfg, &ostree.MockOstree{}, fsenc, nil)
	if err != nil {
		t.Fatalf("NewImager: %v", err)
	}
	im.SetStdout(io.Discard)
	im.SetStderr(io.Discard)
	im.SetRootDevice("/dev/loop99p3")
	im.trackTmpDir(tmpDir)

	inMount := make(chan struct{})
	releaseMount := make(chan struct{})
	im.runner = func(cmd *runner.Cmd) error {
		close(inMount)
		<-releaseMount
		mounted.Store(true)
		return nil
	}

	mountErrCh := make(chan error, 1)
	go func() {
		mountErrCh <- im.MountRootfs(tmpDir)
	}()
	<-inMount

	cleanup1Done := make(chan struct{})
	go func() {
		im.Cleanup()
		close(cleanup1Done)
	}()
	<-inFsenc

	// Let MountRootfs finish while Cleanup #1 is still in fsenc.Cleanup();
	// MountRootfs will detect cleanup, re-track tmpDir, and block on Cleanup #2.
	close(releaseMount)
	time.Sleep(40 * time.Millisecond)
	close(releaseFsenc)

	<-cleanup1Done
	if err := <-mountErrCh; err == nil {
		t.Fatal("expected MountRootfs to return error after overlapping Cleanup")
	}

	if !unmountedBeforeDelete.Load() {
		t.Fatal("Imager.Cleanup removed tmpDir before unmounting late re-tracked mount")
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Fatalf("expected tmpDir %s to be removed after unmounting, stat err = %v", tmpDir, err)
	}
}

func TestImagerMount_RefusesMountAfterPriorCleanupCompleted(t *testing.T) {
	cfg := baseImageConfig()
	im := newTestImager(cfg, &ostree.MockOstree{})
	im.SetStdout(io.Discard)
	im.SetStderr(io.Discard)
	im.SetRootDevice("/dev/loop0p3")

	var mountCalled atomic.Bool
	im.runner = func(cmd *runner.Cmd) error {
		mountCalled.Store(true)
		return nil
	}

	im.Cleanup()

	if err := im.MountRootfs(t.TempDir()); err == nil {
		t.Fatal("expected MountRootfs to fail after Cleanup() already ran")
	}
	if mountCalled.Load() {
		t.Fatal("mount command was invoked even though Cleanup() had already completed")
	}
}
