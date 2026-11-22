package commands

import (
	"bufio"
	"bytes"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSignalGuard_ReArmRace(t *testing.T) {
	var sg SignalGuard
	for range 200 {
		sg.Arm()
		sg.Disarm()
	}
}

func TestSignalGuard_ConcurrentRunCleanupsWaits(t *testing.T) {
	var sg SignalGuard
	var finished atomic.Bool
	started := make(chan struct{})

	sg.PushCleanup(func() {
		close(started)
		time.Sleep(50 * time.Millisecond)
		finished.Store(true)
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sg.RunCleanups()
	}()

	<-started
	// Second concurrent caller (e.g. signal handler) must not return before
	// in-flight cleanups have finished executing.
	sg.RunCleanups()
	if !finished.Load() {
		t.Error("second RunCleanups returned while first cleanup was still running")
	}
	wg.Wait()
}

func TestStyledWriter_ConcurrentGlobalLogDest(t *testing.T) {
	var logBuf bytes.Buffer
	SetGlobalLogWriter(&logBuf)
	defer ClearGlobalLogWriter()

	var out1, out2 bytes.Buffer
	w1 := newStyledWriter(&out1, "[w1]", "", "", false)
	w2 := newStyledWriter(&out2, "[w2]", "", "", false)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 100 {
			_, _ = w1.Write([]byte("line from worker 1\n"))
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			_, _ = w2.Write([]byte("line from worker 2\n"))
		}
	}()
	wg.Wait()
}

func TestVMDriver_ExpectTimeoutThenExpectRace(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	defer pr.Close()

	vm := &VMDriver{
		stdout:  pr,
		reader:  bufio.NewReader(pr),
		display: io.Discard,
	}

	// First Expect times out while reader is blocked on pipe.
	if err := vm.Expect("never-arrives", 20*time.Millisecond); err == nil {
		t.Fatal("expected timeout error")
	}

	// Second Expect while writing to the pipe races with the leaked reader
	// goroutine from the first Expect on vm.reader.
	go func() {
		time.Sleep(10 * time.Millisecond)
		_, _ = pw.Write([]byte("ready\n"))
	}()
	if err := vm.Expect("ready", 200*time.Millisecond); err != nil {
		t.Fatalf("second Expect failed: %v", err)
	}
}
