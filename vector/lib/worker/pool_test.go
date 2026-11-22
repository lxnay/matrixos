package worker

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunPool_Validation(t *testing.T) {
	if err := RunPool(context.Background(), PoolOptions[int]{
		Items:       []int{1},
		Parallelism: 0,
		Run:         func(context.Context, int) error { return nil },
	}); err == nil {
		t.Fatal("expected error for Parallelism == 0")
	}
	if err := RunPool(context.Background(), PoolOptions[int]{
		Items:       []int{1},
		Parallelism: -2,
		Run:         func(context.Context, int) error { return nil },
	}); err == nil {
		t.Fatal("expected error for negative Parallelism")
	}
	if err := RunPool(context.Background(), PoolOptions[int]{
		Items:       []int{1},
		Parallelism: 1,
	}); err == nil {
		t.Fatal("expected error for nil Run func")
	}
}

func TestRunPool_ProcessesAllItemsWithBound(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	var mu sync.Mutex
	var got []int

	items := []int{1, 2, 3, 4, 5, 6, 7, 8}
	err := RunPool(context.Background(), PoolOptions[int]{
		Items:       items,
		Parallelism: 3,
		Run: func(_ context.Context, item int) error {
			cur := inFlight.Add(1)
			for {
				prev := maxInFlight.Load()
				if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			inFlight.Add(-1)
			mu.Lock()
			got = append(got, item)
			mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if maxInFlight.Load() > 3 {
		t.Fatalf("exceeded parallelism limit: maxInFlight = %d", maxInFlight.Load())
	}
	slices.Sort(got)
	if !slices.Equal(got, items) {
		t.Fatalf("processed %v, want %v", got, items)
	}
}

func TestRunPool_ErrorCancelsSiblingsAndSkipsRemaining(t *testing.T) {
	boom := errors.New("boom")
	var errCount int
	var skipped []string
	var startedSlow = make(chan struct{})
	var slowSawCancel atomic.Bool

	err := RunPool(context.Background(), PoolOptions[string]{
		Items:       []string{"slow", "fail", "rem1", "rem2"},
		Parallelism: 2,
		Run: func(ctx context.Context, item string) error {
			switch item {
			case "slow":
				close(startedSlow)
				<-ctx.Done()
				slowSawCancel.Store(true)
				return ctx.Err()
			case "fail":
				<-startedSlow
				return boom
			default:
				return nil
			}
		},
		OnError: func(err error) {
			// Serialized by RunPool — plain increment under -race proves serialization.
			errCount++
		},
		OnSkip: func(item string) {
			skipped = append(skipped, item)
		},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got error %v, want %v", err, boom)
	}
	if !slowSawCancel.Load() {
		t.Fatal("sibling worker did not observe pool context cancellation")
	}
	if errCount < 1 {
		t.Fatalf("expected OnError to be called at least once, got %d", errCount)
	}
	slices.Sort(skipped)
	if !slices.Equal(skipped, []string{"rem1", "rem2"}) {
		t.Fatalf("skipped = %v, want [rem1 rem2]", skipped)
	}
}

func TestRunPool_EmptyItemsAndCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := RunPool(ctx, PoolOptions[string]{
		Parallelism: 2,
		Run: func(context.Context, string) error {
			t.Fatal("Run should not be called on empty items")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("expected nil error for empty items, got: %v", err)
	}
}

func TestRunPool_CancelledContextWithItemsReturnsContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for range 50 {
		var ran atomic.Bool
		var skipped []string
		err := RunPool(ctx, PoolOptions[string]{
			Items:       []string{"a", "b", "c"},
			Parallelism: 2,
			Run: func(context.Context, string) error {
				ran.Store(true)
				return nil
			},
			OnSkip: func(item string) {
				skipped = append(skipped, item)
			},
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled for non-empty items with cancelled ctx, got: %v", err)
		}
		if ran.Load() {
			t.Fatal("Run was called despite context already being cancelled")
		}
		slices.Sort(skipped)
		if !slices.Equal(skipped, []string{"a", "b", "c"}) {
			t.Fatalf("skipped = %v, want [a b c]", skipped)
		}
	}
}
