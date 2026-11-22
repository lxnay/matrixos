package commands

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
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

func TestVMDriver_ExpectDoesNotBlockWriterAfterReturn(t *testing.T) {
	pr, pw := io.Pipe()
	defer pr.Close()

	vm := &VMDriver{
		stdout:  pr,
		reader:  bufio.NewReader(pr),
		display: io.Discard,
	}

	writeDone := make(chan error, 1)
	go func() {
		if _, err := pw.Write([]byte("matrixos login:\n")); err != nil {
			writeDone <- err
			return
		}
		// Simulate >64 chunks of output emitted after Expect returns (e.g.
		// systemd shutdown logs while caller is waiting in vm.Wait()).
		for range 200 {
			if _, err := pw.Write([]byte("shutting down service...\n")); err != nil {
				writeDone <- err
				return
			}
		}
		writeDone <- pw.Close()
	}()

	if err := vm.Expect("matrixos login:", 500*time.Millisecond); err != nil {
		t.Fatalf("Expect failed: %v", err)
	}

	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("writer failed: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("VMDriver pump stalled writer after Expect returned (deadlock risk during VM shutdown)")
	}
}

func TestSignalGuard_NestedGuardsDoNotExitMidCleanup(t *testing.T) {
	origExit := signalExit
	exitCalled := make(chan bool, 4)
	var innerCleanupDone atomic.Bool

	signalExit = func(code int) {
		exitCalled <- innerCleanupDone.Load()
	}
	t.Cleanup(func() { signalExit = origExit })

	// Mirrors AllCommand (outer guard with no cleanups) running SeedsCommand /
	// ReleasesCommand / ImagesCommand (inner guard with critical cleanups).
	var outer, inner SignalGuard
	outer.Arm()
	defer outer.Disarm()

	inner.Arm()
	defer inner.Disarm()
	inner.PushCleanup(func() {
		time.Sleep(60 * time.Millisecond)
		innerCleanupDone.Store(true)
	})

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("kill SIGINT: %v", err)
	}

	select {
	case doneBeforeExit := <-exitCalled:
		if !doneBeforeExit {
			t.Fatal("signalExit was called while inner SignalGuard cleanup was still running")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for signalExit")
	}
}

func TestAllCommand_FlushesAndClosesLogBeforeSendMail(t *testing.T) {
	tmp := t.TempDir()
	pusher := filepath.Join(tmp, "push.sh")
	if err := os.WriteFile(pusher, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	origMailCmd := allMailCommand
	allMailCommand = "sh" // guaranteed to exist in PATH for exec.LookPath
	t.Cleanup(func() { allMailCommand = origMailCmd })

	cmd := NewAllCommand()
	cmd.cfg = &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.LocksDir": {filepath.Join(tmp, "locks")},
			"matrixOS.LogsDir":  {filepath.Join(tmp, "logs")},
		},
	}
	cmd.buildID = "test"
	cmd.buildName = "test-build"
	cmd.onlyImages = true
	cmd.skipImages = true
	cmd.disableJanitor = true
	cmd.disableMail = false
	cmd.mailUser = "root"
	cmd.cdnPusher = pusher

	var logAtMailTime string
	var mailCalled bool
	cmd.cmdRunner = func(c *runner.Cmd) error {
		if c.Name == pusher {
			// Emit a partial line (no trailing newline) so it stays buffered
			// in styledWriter until FlushPrinters() runs.
			_, _ = io.WriteString(c.Stdout, "partial-cdn-output")
			return nil
		}
		// Mail invocation: read the attached log file at the moment sendMail executes.
		mailCalled = true
		data, err := os.ReadFile(cmd.logFile)
		if err != nil {
			t.Errorf("reading logFile during sendMail: %v", err)
		}
		logAtMailTime = string(data)
		return nil
	}

	if err := cmd.run(); err != nil {
		t.Fatalf("AllCommand.run(): %v", err)
	}
	if !mailCalled {
		t.Fatal("expected sendMail to invoke cmdRunner")
	}
	if !strings.Contains(logAtMailTime, "partial-cdn-output") {
		t.Errorf("logFile at sendMail time missing flushed partial output; got:\n%s", logAtMailTime)
	}
	finalLog, err := os.ReadFile(cmd.logFile)
	if err != nil {
		t.Fatalf("reading final logFile: %v", err)
	}
	if !strings.Contains(string(finalLog), "partial-cdn-output") {
		t.Errorf("final logFile missing flushed partial output (FlushPrinters ran after logF.Close); got:\n%s", string(finalLog))
	}
}
