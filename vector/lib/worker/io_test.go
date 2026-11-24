package worker

import (
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
)

func TestIO_ZeroValueDefaults(t *testing.T) {
	var x IO
	if x.Stdout() != os.Stdout {
		t.Fatal("expected zero-value Stdout() == os.Stdout")
	}
	if x.Stderr() != os.Stderr {
		t.Fatal("expected zero-value Stderr() == os.Stderr")
	}
}

func TestIO_SetAndPrint(t *testing.T) {
	var x IO
	var outBuf, errBuf bytes.Buffer
	x.SetStdout(&outBuf)
	x.SetStderr(&errBuf)

	if x.Stdout() != &outBuf {
		t.Fatal("Stdout() did not return configured writer")
	}
	if x.Stderr() != &errBuf {
		t.Fatal("Stderr() did not return configured writer")
	}

	x.Print("hello %s %d", "world", 1)
	x.PrintWarning("warn %d;", 2)
	x.PrintError("err %d", 3)

	if got := outBuf.String(); got != "hello world 1" {
		t.Fatalf("stdout = %q, want %q", got, "hello world 1")
	}
	if got := errBuf.String(); got != "warn 2;err 3" {
		t.Fatalf("stderr = %q, want %q", got, "warn 2;err 3")
	}

	// Resetting to nil restores defaults rather than panicking.
	x.SetStdout(nil)
	x.SetStderr(nil)
	if x.Stdout() != os.Stdout {
		t.Fatal("expected Stdout() == os.Stdout after SetStdout(nil)")
	}
	if x.Stderr() != os.Stderr {
		t.Fatal("expected Stderr() == os.Stderr after SetStderr(nil)")
	}
}

func TestIO_ConcurrentAccessRaceFree(t *testing.T) {
	var x IO
	x.SetStdout(io.Discard)
	x.SetStderr(io.Discard)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			for j := 0; j < 100; j++ {
				x.Print("out %d\n", j)
				x.PrintWarning("warn %d\n", j)
				x.PrintError("err %d\n", j)
				_ = x.Stdout()
				_ = x.Stderr()
			}
		})
	}
	wg.Go(func() {
		for j := 0; j < 100; j++ {
			x.SetStdout(io.Discard)
			x.SetStderr(io.Discard)
		}
	})
	wg.Wait()
}

type testFlusher struct {
	bytes.Buffer
	flushed int
}

func (f *testFlusher) Flush() { f.flushed++ }

func TestFlushWriters(t *testing.T) {
	f1 := &testFlusher{}
	f2 := &testFlusher{}
	var plain bytes.Buffer

	FlushWriters(nil, &plain, f1, f2)
	if f1.flushed != 1 || f2.flushed != 1 {
		t.Fatalf("expected both flushers flushed once, got %d and %d", f1.flushed, f2.flushed)
	}
}
