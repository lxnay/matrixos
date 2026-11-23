package cgroups

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWorkerPool_ConcurrentCloseAndOperations(t *testing.T) {
	root := fakeCgroupRoot(t)
	pool, err := NewWorkerPool(&WorkerPoolOptions{
		Parallelism:       2,
		NumCPUs:           4,
		MemPerWorkerBytes: 1 << 30,
		TotalMemBytes:     2 << 30,
		CgroupRoot:        root,
	})
	if err != nil {
		t.Fatalf("NewWorkerPool: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		for range 50 {
			_ = pool.BoostWorker(0)
		}
	}()
	go func() {
		defer wg.Done()
		for range 50 {
			_ = pool.UnboostWorker(0)
		}
	}()
	go func() {
		defer wg.Done()
		for range 50 {
			_ = pool.SysProcAttr(0)
		}
	}()
	go func() {
		defer wg.Done()
		pool.Close()
		pool.Close() // idempotent concurrent close
	}()
	wg.Wait()
}

func TestWorkerPool_MultiplePoolsInSameProcessDoNotCollide(t *testing.T) {
	root := fakeCgroupRoot(t)
	opts := &WorkerPoolOptions{
		Parallelism:       2,
		NumCPUs:           4,
		MemPerWorkerBytes: 1 << 30,
		TotalMemBytes:     2 << 30,
		CgroupRoot:        root,
	}

	pool1, err := NewWorkerPool(opts)
	if err != nil {
		t.Fatalf("first NewWorkerPool: %v", err)
	}
	defer pool1.Close()

	pool2, err := NewWorkerPool(opts)
	if err != nil {
		t.Fatalf("second concurrent NewWorkerPool in same process failed: %v", err)
	}
	defer pool2.Close()

	if pool1.parentDir == pool2.parentDir {
		t.Errorf("both pools share the same parentDir %q", pool1.parentDir)
	}
}

func TestWorkerPool_CgroupFDHasCloseOnExec(t *testing.T) {
	root := fakeCgroupRoot(t)
	pool, err := NewWorkerPool(&WorkerPoolOptions{
		Parallelism:       1,
		NumCPUs:           2,
		MemPerWorkerBytes: 1 << 30,
		TotalMemBytes:     1 << 30,
		CgroupRoot:        root,
	})
	if err != nil {
		t.Fatalf("NewWorkerPool: %v", err)
	}
	defer pool.Close()

	attr := pool.SysProcAttr(0)
	if attr == nil {
		t.Fatal("expected non-nil SysProcAttr")
	}
	flags, err := unix.FcntlInt(uintptr(attr.CgroupFD), unix.F_GETFD, 0)
	if err != nil {
		t.Fatalf("F_GETFD: %v", err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("worker cgroup fd %d is missing FD_CLOEXEC (flags=0x%x)", attr.CgroupFD, flags)
	}
}

func TestNewWorkerPool_NilOptsAndZeroMemPerWorker(t *testing.T) {
	t.Run("NilOpts", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("NewWorkerPool(nil) panicked: %v", r)
			}
		}()
		if _, err := NewWorkerPool(nil); err == nil {
			t.Fatal("expected error for nil options")
		}
	})

	t.Run("ZeroMemPerWorker", func(t *testing.T) {
		root := fakeCgroupRoot(t)
		pool, err := NewWorkerPool(&WorkerPoolOptions{
			Parallelism:       1,
			NumCPUs:           2,
			MemPerWorkerBytes: 0,
			TotalMemBytes:     1 << 30,
			CgroupRoot:        root,
		})
		if pool != nil {
			pool.Close()
		}
		if err == nil {
			t.Fatal("expected error for MemPerWorkerBytes=0")
		}
	})
}

func TestWorkerPool_CloseRetriesStaleDirs(t *testing.T) {
	root := fakeCgroupRoot(t)
	pool, err := NewWorkerPool(&WorkerPoolOptions{
		Parallelism:       1,
		NumCPUs:           2,
		MemPerWorkerBytes: 1 << 30,
		TotalMemBytes:     1 << 30,
		CgroupRoot:        root,
	})
	if err != nil {
		t.Fatalf("NewWorkerPool: %v", err)
	}

	workerDir := pool.workers[0].dir
	parentDir := pool.parentDir

	// On a regular filesystem fake cgroup root, files inside workerDir prevent
	// os.Remove(workerDir) from succeeding on the first Close(), simulating EBUSY.
	pool.Close()
	if _, err := os.Stat(workerDir); err != nil {
		t.Fatalf("expected workerDir to remain after first failed remove: %v", err)
	}

	// Simulate the kernel releasing/emptying the cgroup before the second Close().
	entries, err := os.ReadDir(workerDir)
	if err != nil {
		t.Fatalf("ReadDir(workerDir): %v", err)
	}
	for _, e := range entries {
		_ = os.Remove(filepath.Join(workerDir, e.Name()))
	}
	_ = os.Remove(filepath.Join(parentDir, "cgroup.subtree_control"))

	pool.Close()
	if _, err := os.Stat(workerDir); !os.IsNotExist(err) {
		t.Fatalf("workerDir %s still exists after second Close() retry", workerDir)
	}
	if _, err := os.Stat(parentDir); !os.IsNotExist(err) {
		t.Fatalf("parentDir %s still exists after second Close() retry", parentDir)
	}
}

func TestWorkerPool_AcquireSysProcAttrSurvivesClose(t *testing.T) {
	root := fakeCgroupRoot(t)
	pool, err := NewWorkerPool(&WorkerPoolOptions{
		Parallelism:       1,
		NumCPUs:           2,
		MemPerWorkerBytes: 1 << 30,
		TotalMemBytes:     1 << 30,
		CgroupRoot:        root,
	})
	if err != nil {
		t.Fatalf("NewWorkerPool: %v", err)
	}

	attr, release := pool.AcquireSysProcAttr(0)
	if attr == nil || !attr.UseCgroupFD {
		t.Fatalf("expected valid SysProcAttr with UseCgroupFD, got %+v", attr)
	}
	flags, err := unix.FcntlInt(uintptr(attr.CgroupFD), unix.F_GETFD, 0)
	if err != nil {
		release()
		t.Fatalf("F_GETFD on acquired dup fd: %v", err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		release()
		t.Fatalf("acquired dup fd %d missing FD_CLOEXEC", attr.CgroupFD)
	}

	// Closing the pool must NOT invalidate the caller's owned dup FD.
	pool.Close()
	if _, err := unix.FcntlInt(uintptr(attr.CgroupFD), unix.F_GETFD, 0); err != nil {
		release()
		t.Fatalf("pool.Close() closed the caller's owned dup FD %d: %v", attr.CgroupFD, err)
	}

	// Releasing must close the dup FD and be idempotent.
	release()
	release()
	if _, err := unix.FcntlInt(uintptr(attr.CgroupFD), unix.F_GETFD, 0); err == nil {
		t.Fatalf("expected dup FD %d to be closed after release()", attr.CgroupFD)
	}

	// Acquiring after Close must return nil, noop.
	attr2, release2 := pool.AcquireSysProcAttr(0)
	release2()
	if attr2 != nil {
		t.Fatalf("expected nil SysProcAttr after Close, got %+v", attr2)
	}
}

func TestWorkerPool_CloseRetriesTransientEBUSY(t *testing.T) {
	root := fakeCgroupRoot(t)
	pool, err := NewWorkerPool(&WorkerPoolOptions{
		Parallelism:       1,
		NumCPUs:           2,
		MemPerWorkerBytes: 1 << 30,
		TotalMemBytes:     1 << 30,
		CgroupRoot:        root,
	})
	if err != nil {
		t.Fatalf("NewWorkerPool: %v", err)
	}

	workerDir := pool.workers[0].dir
	parentDir := pool.parentDir

	// Empty the fake dirs so os.Remove succeeds once simulated EBUSY clears.
	entries, err := os.ReadDir(workerDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		_ = os.Remove(filepath.Join(workerDir, e.Name()))
	}
	_ = os.Remove(filepath.Join(parentDir, "cgroup.subtree_control"))

	origRemove := cgroupRemoveFunc
	origDelay := cgroupRemoveRetryDelay
	cgroupRemoveRetryDelay = time.Millisecond
	var workerAttempts int
	cgroupRemoveFunc = func(path string) error {
		if path == workerDir {
			workerAttempts++
			if workerAttempts < 3 {
				return &os.PathError{Op: "remove", Path: path, Err: syscall.EBUSY}
			}
		}
		return os.Remove(path)
	}
	t.Cleanup(func() {
		cgroupRemoveFunc = origRemove
		cgroupRemoveRetryDelay = origDelay
	})

	pool.Close()
	if workerAttempts != 3 {
		t.Errorf("workerDir remove attempts = %d, want 3", workerAttempts)
	}
	if _, err := os.Stat(workerDir); !os.IsNotExist(err) {
		t.Fatalf("workerDir %s still exists after EBUSY retries", workerDir)
	}
	if _, err := os.Stat(parentDir); !os.IsNotExist(err) {
		t.Fatalf("parentDir %s still exists after EBUSY retries", parentDir)
	}
}
