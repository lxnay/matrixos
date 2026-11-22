package seeder

import (
	"context"
	"io"
	"testing"
	"time"
)

func TestParallelSeed_EmptySeedersDoesNotDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := ParallelSeed(ctx, &ParallelSeedOptions{
		Seeders:         nil,
		Parallelism:     2,
		NewStdoutWriter: func(string) io.Writer { return io.Discard },
		NewStderrWriter: func(string) io.Writer { return io.Discard },
	})
	if err != nil {
		t.Fatalf("ParallelSeed with empty seeders returned error: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("ParallelSeed deadlocked until context expired: %v", ctx.Err())
	}
}
