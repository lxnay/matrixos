package imager

import (
	"bytes"
	"context"
	"io"
	"testing"

	"matrixos/vector/lib/config"
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
