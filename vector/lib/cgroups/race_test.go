package cgroups

import (
	"sync"
	"testing"
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
