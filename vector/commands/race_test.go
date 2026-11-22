package commands

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
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

func TestVMDriver_WaitAndCloseAreIdempotent(t *testing.T) {
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "sh", "-c", "echo ready")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	vm := &VMDriver{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		reader:  bufio.NewReader(stdout),
		display: io.Discard,
	}
	if err := vm.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := vm.Expect("ready", 2*time.Second); err != nil {
		t.Fatalf("Expect: %v", err)
	}
	if err := vm.Wait(); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	// Mirrors runInteractive/runTests calling vm.Wait() followed by defer vm.Close().
	if err := vm.Close(); err != nil {
		t.Fatalf("Close after Wait failed (double exec.Cmd.Wait): %v", err)
	}
}

func TestVMCommand_RunTestsCancelsBeforeWaitingOnError(t *testing.T) {
	binDir := t.TempDir()
	fakeQemu := filepath.Join(binDir, qemuSystemBinary())
	if err := os.WriteFile(fakeQemu, []byte("#!/bin/sh\nexec sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	c := NewVMCommand()
	c.StartUI()
	c.SetupPrinters("vm")
	defer c.FlushPrinters()
	c.waitBoot = 60 * time.Millisecond
	c.waitTests = 60 * time.Millisecond
	c.maxRunTime = 3 * time.Second

	start := time.Now()
	err := c.runTests(nil)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected boot timeout error, got nil")
	}
	// With cancel() running before vm.Close(), sleep 30 is interrupted immediately
	// (~60ms + process teardown) rather than blocking until maxRunTime (3s).
	if elapsed > 1*time.Second {
		t.Fatalf("runTests took %v after 60ms boot timeout (defer vm.Close ran before defer cancel)", elapsed)
	}
}

func TestSignalGuard_PushCleanupDuringRunCleanupsExecutes(t *testing.T) {
	var sg SignalGuard
	var lateRan atomic.Bool

	sg.PushCleanup(func() {
		sg.PushCleanup(func() {
			lateRan.Store(true)
		})
	})

	sg.RunCleanups()

	if !lateRan.Load() {
		t.Fatal("cleanup pushed while RunCleanups was draining did not execute")
	}
}

func TestSignalGuard_DisarmWaitsForInFlightSignalHandler(t *testing.T) {
	origExit := signalExit
	exitDone := make(chan struct{}, 1)
	signalExit = func(int) {
		exitDone <- struct{}{}
	}
	t.Cleanup(func() { signalExit = origExit })

	var sg SignalGuard
	inCleanup := make(chan struct{})
	releaseCleanup := make(chan struct{})

	sg.Arm()
	sg.PushCleanup(func() {
		close(inCleanup)
		<-releaseCleanup
	})

	sigCoordMu.Lock()
	ch := globalSigCh
	sigCoordMu.Unlock()
	ch <- syscall.SIGINT

	<-inCleanup

	disarmDone := make(chan struct{})
	go func() {
		sg.Disarm()
		close(disarmDone)
	}()

	select {
	case <-disarmDone:
		close(releaseCleanup)
		<-exitDone
		t.Fatal("Disarm() returned while signal handler was still executing cleanups")
	case <-time.After(80 * time.Millisecond):
		close(releaseCleanup)
	}

	<-exitDone
	<-disarmDone
}

func TestSignalGuard_RespawnsListenerAfterSignalExitReturns(t *testing.T) {
	origExit := signalExit
	exitCh := make(chan int, 2)
	signalExit = func(code int) {
		exitCh <- code
	}
	t.Cleanup(func() { signalExit = origExit })

	var outer, inner1, inner2 SignalGuard
	outer.Arm()
	defer outer.Disarm()

	inner1.Arm()
	sigCoordMu.Lock()
	ch1 := globalSigCh
	sigCoordMu.Unlock()
	ch1 <- syscall.SIGINT

	select {
	case <-exitCh:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first signalExit")
	}
	inner1.Disarm()

	// Wait briefly for the first listener goroutine to finish its defer cleanup.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		sigCoordMu.Lock()
		cleared := globalSigCh == nil
		sigCoordMu.Unlock()
		if cleared {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Arming another guard while outer is still armed must ensure a live listener.
	inner2.Arm()
	defer inner2.Disarm()

	sigCoordMu.Lock()
	ch2 := globalSigCh
	sigCoordMu.Unlock()
	if ch2 == nil {
		t.Fatal("expected live signal channel after re-arming")
	}
	ch2 <- syscall.SIGINT

	select {
	case <-exitCh:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("second signal was never handled because previous listener exited without clearing globalSigCh")
	}
}

func TestAllCommand_SignalCleanupFlushesAndClosesLog(t *testing.T) {
	tmp := t.TempDir()
	pusher := filepath.Join(tmp, "push.sh")
	if err := os.WriteFile(pusher, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	cmd := NewAllCommand()
	cmd.cfg = &config.MockConfig{
		Items: map[string][]string{
			"matrixOS.LocksDir": {filepath.Join(tmp, "locks")},
			"matrixOS.LogsDir":  {filepath.Join(tmp, "logs")},
		},
	}
	cmd.buildID = "sigtest"
	cmd.buildName = "sig-build"
	cmd.onlyImages = true
	cmd.skipImages = true
	cmd.disableJanitor = true
	cmd.disableMail = true
	cmd.cdnPusher = pusher

	var logAfterSignalCleanup string
	cmd.cmdRunner = func(c *runner.Cmd) error {
		_, _ = io.WriteString(c.Stdout, "partial-before-signal")
		// Simulate SignalGuard firing while AllCommand is in the middle of run().
		cmd.RunCleanups()
		data, err := os.ReadFile(cmd.logFile)
		if err != nil {
			t.Errorf("ReadFile(logFile) after signal cleanup: %v", err)
		}
		logAfterSignalCleanup = string(data)
		return nil
	}

	if err := cmd.run(); err != nil {
		t.Fatalf("AllCommand.run(): %v", err)
	}
	if !strings.Contains(logAfterSignalCleanup, "partial-before-signal") {
		t.Fatalf("logFile missing flushed output after SignalGuard cleanup; got:\n%s", logAfterSignalCleanup)
	}
}

func TestSignalGuard_PreCleanupRunsBeforeLaterCleanups(t *testing.T) {
	var sg SignalGuard
	var order []string

	sg.PushCleanup(func() { order = append(order, "flush") })
	sg.PushPreCleanup(func() { order = append(order, "cancel") })
	// Worker cleanup pushed AFTER cancel must still run AFTER cancel.
	sg.PushCleanup(func() { order = append(order, "worker-cleanup") })

	sg.RunCleanups()

	want := []string{"cancel", "worker-cleanup", "flush"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order[%d] = %q, want %q (full: %v)", i, order[i], want[i], order)
		}
	}
}

func TestVMCommand_RunInteractiveDoesNotDeadlockOnLargeOutput(t *testing.T) {
	binDir := t.TempDir()
	fakeQemu := filepath.Join(binDir, qemuSystemBinary())
	// Emit 256 KiB (> 64 KiB Linux pipe buffer) and exit 0.
	script := "#!/bin/sh\ndd if=/dev/zero bs=1024 count=256 2>/dev/null | tr '\\0' 'A'\n"
	if err := os.WriteFile(fakeQemu, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Redirect os.Stdout temporarily so test output isn't flooded with 256 KiB.
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	origStdout := os.Stdout
	os.Stdout = devNull
	t.Cleanup(func() { os.Stdout = origStdout })

	c := NewVMCommand()
	c.StartUI()
	c.SetupPrinters("vm")
	defer c.FlushPrinters()

	done := make(chan error, 1)
	go func() {
		done <- c.runInteractive(nil)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runInteractive failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runInteractive deadlocked when QEMU emitted >64 KiB to stdout")
	}
}

type slowSyncBuffer struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	delay time.Duration
}

func (s *slowSyncBuffer) Write(p []byte) (int, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *slowSyncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestVMDriver_WaitDrainsAllStdoutBeforeClosingPipe(t *testing.T) {
	// Emit >10 KiB (>10 pump reads of 1024 bytes) and exit immediately so
	// cmd.Wait() races ahead of the pump goroutine if called before <-pumpDone.
	cmd := exec.Command("sh", "-c", "echo ready; seq 1 3000; echo FINAL_MARKER_END")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	disp := &slowSyncBuffer{delay: 5 * time.Millisecond}
	vm := &VMDriver{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		reader:  bufio.NewReader(stdout),
		display: disp,
	}
	if err := vm.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := vm.Expect("ready", 2*time.Second); err != nil {
		t.Fatalf("Expect: %v", err)
	}
	if err := vm.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !strings.Contains(disp.String(), "FINAL_MARKER_END") {
		t.Fatal("Wait closed StdoutPipe before pump drained trailing output (missing FINAL_MARKER_END)")
	}
}
