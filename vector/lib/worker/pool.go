package worker

import (
	"context"
	"fmt"
	"sync"
)

// PoolOptions configures RunPool.
type PoolOptions[T any] struct {
	// Items is the ordered list of work items to process.
	Items []T
	// Parallelism is the maximum number of concurrent workers. Must be >= 1.
	Parallelism int
	// Run processes a single work item. Returning a non-nil error cancels
	// the pool context for sibling workers and skips remaining unstarted items.
	Run func(ctx context.Context, item T) error
	// OnError is called (serialized internally) for every worker error.
	// Optional; must not block on the pool.
	OnError func(err error)
	// OnSkip is called (serialized internally) for every item skipped due
	// to an earlier failure. Optional; must not block on the pool.
	OnSkip func(item T)
}

// RunPool processes opts.Items concurrently using a fixed pool of
// opts.Parallelism goroutines. On the first worker error, the pool cancels
// its derived context, reports errors and skipped items via OnError/OnSkip,
// drains any unstarted items through OnSkip, and returns the first error.
func RunPool[T any](ctx context.Context, opts PoolOptions[T]) error {
	if opts.Parallelism < 1 {
		return fmt.Errorf("parallelism must be >= 1, got %d", opts.Parallelism)
	}
	if opts.Run == nil {
		return fmt.Errorf("worker pool Run function must not be nil")
	}

	poolCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var firstErr error
	var errMu sync.Mutex

	recordError := func(err error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		if opts.OnError != nil {
			opts.OnError(err)
		}
		errMu.Unlock()
		cancel()
	}

	work := make(chan T, len(opts.Items))
	for _, item := range opts.Items {
		work <- item
	}
	close(work)

	var wg sync.WaitGroup
	for i := 0; i < opts.Parallelism; i++ {
		wg.Go(func() {
			for {
				select {
				case <-poolCtx.Done():
					return
				case item, ok := <-work:
					if !ok {
						return
					}

					errMu.Lock()
					failed := firstErr != nil
					if failed && opts.OnSkip != nil {
						opts.OnSkip(item)
					}
					errMu.Unlock()
					if failed {
						return
					}

					if err := opts.Run(poolCtx, item); err != nil {
						recordError(err)
						return
					}
				}
			}
		})
	}

	wg.Wait()

	// Drain any items that were never picked up by a worker.
	if firstErr != nil && opts.OnSkip != nil {
		for item := range work {
			opts.OnSkip(item)
		}
	}

	return firstErr
}
