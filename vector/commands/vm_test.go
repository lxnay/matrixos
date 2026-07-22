package commands

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"matrixos/vector/lib/config"
)

// newTestVMDriver creates a VMDriver wired to in-process pipes,
// bypassing qemu-system-x86_64 entirely.  The caller writes to
// fakeStdout to simulate VM output and reads from fakeStdin to
// inspect what Send() wrote.
func newTestVMDriver() (vm *VMDriver, fakeStdout io.WriteCloser, fakeStdin io.ReadCloser) {
	// Pipe for VMDriver.stdout (VM → driver)
	stdoutR, stdoutW := io.Pipe()
	// Pipe for VMDriver.stdin  (driver → VM)
	stdinR, stdinW := io.Pipe()

	vm = &VMDriver{
		stdin:  stdinW,
		stdout: stdoutR,
		reader: bufio.NewReader(stdoutR),
	}
	return vm, stdoutW, stdinR
}

// --- NewVMCommand tests ---

func TestNewVMCommand(t *testing.T) {
	cmd := NewVMCommand()
	if cmd == nil {
		t.Fatal("NewVMCommand returned nil")
	}
	if cmd.Name() != "vm" {
		t.Errorf("expected name %q, got %q", "vm", cmd.Name())
	}
}

func TestVMCommandName(t *testing.T) {
	c := &VMCommand{}
	c.fs = nil // Name() is not callable without fs; use NewVMCommand.
	cmd := NewVMCommand()
	if cmd.Name() != "vm" {
		t.Errorf("expected %q, got %q", "vm", cmd.Name())
	}
}

// --- Flag parsing ---

func TestVMCommandInitDefaults(t *testing.T) {
	// Init requires config which we can't load in unit tests,
	// so test flag parsing directly on the flagset.
	c := NewVMCommand()
	// Parse with no flags – should use defaults.
	if err := c.fs.Parse([]string{}); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if c.imagePath != "" {
		t.Errorf("default imagePath should be empty, got %q", c.imagePath)
	}
	if c.memory != "4G" {
		t.Errorf("default memory should be %q, got %q", "4G", c.memory)
	}
	if c.sshPort != "" {
		t.Errorf("default sshPort should be empty, got %q", c.sshPort)
	}
	if c.cpus != "4" {
		t.Errorf("default cpus should be %q, got %q", "4", c.cpus)
	}
	if !c.graphical {
		t.Error("default graphical should be true")
	}
	if !c.gpuAccel {
		t.Error("default gpuAccel should be true")
	}
	if !c.audio {
		t.Error("default audio should be true")
	}
	if c.interactive {
		t.Error("default interactive should be false")
	}
	if c.venusAccel {
		t.Error("default venusAccel should be false")
	}
	if c.display != "sdl" {
		t.Errorf("default display should be %q, got %q", "sdl", c.display)
	}
	if c.gpuMemory != "512M" {
		t.Errorf("default gpuMemory should be %q, got %q", "512M", c.gpuMemory)
	}
	if c.waitBoot != 120*time.Second {
		t.Errorf("default waitBoot should be %v, got %v", 120*time.Second, c.waitBoot)
	}
	if c.waitTests != 120*time.Second {
		t.Errorf("default waitTests should be %v, got %v", 120*time.Second, c.waitTests)
	}
	if c.maxRunTime != 300*time.Second {
		t.Errorf("default maxRunTime should be %v, got %v", 300*time.Second, c.maxRunTime)
	}
	if c.audioDev != "pipewire" {
		t.Errorf("default audioDev should be %q, got %q", "pipewire", c.audioDev)
	}
	if c.extraDisk {
		t.Error("default extraDisk should be false")
	}
	if c.extraDiskSize != "64G" {
		t.Errorf("default extraDiskSize should be %q, got %q", "64G", c.extraDiskSize)
	}
}

func TestVMCommandFlagOverrides(t *testing.T) {
	c := NewVMCommand()
	err := c.fs.Parse([]string{
		"-image", "/tmp/test.qcow2",
		"-memory", "8G",
		"-ssh_port", "3333",
		"-cpus", "8",
		"-graphical=false",
		"-gpuaccel=false",
		"-audio=false",
		"-interactive",
		"-venus",
		"-wait_boot", "60s",
		"-wait_tests", "90s",
		"-max_run_time", "600s",
		"-audio_dev", "alsa",
		"-display", "gtk",
		"-gpu_memory", "1G",
		"-extra_disk",
		"-extra_disk_size", "128G",
	})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if c.imagePath != "/tmp/test.qcow2" {
		t.Errorf("imagePath: got %q, want %q", c.imagePath, "/tmp/test.qcow2")
	}
	if c.memory != "8G" {
		t.Errorf("memory: got %q, want %q", c.memory, "8G")
	}
	if c.sshPort != "3333" {
		t.Errorf("sshPort: got %q, want %q", c.sshPort, "3333")
	}
	if c.cpus != "8" {
		t.Errorf("cpus: got %q, want %q", c.cpus, "8")
	}
	if c.graphical {
		t.Error("graphical should be false")
	}
	if c.gpuAccel {
		t.Error("gpuAccel should be false")
	}
	if c.audio {
		t.Error("audio should be false")
	}
	if !c.interactive {
		t.Error("interactive should be true")
	}
	if !c.venusAccel {
		t.Error("venusAccel should be true")
	}
	if c.display != "gtk" {
		t.Errorf("display: got %q, want %q", c.display, "gtk")
	}
	if c.gpuMemory != "1G" {
		t.Errorf("gpuMemory: got %q, want %q", c.gpuMemory, "1G")
	}
	if c.waitBoot != 60*time.Second {
		t.Errorf("waitBoot: got %v, want %v", c.waitBoot, 60*time.Second)
	}
	if c.waitTests != 90*time.Second {
		t.Errorf("waitTests: got %v, want %v", c.waitTests, 90*time.Second)
	}
	if c.maxRunTime != 600*time.Second {
		t.Errorf("maxRunTime: got %v, want %v", c.maxRunTime, 600*time.Second)
	}
	if c.audioDev != "alsa" {
		t.Errorf("audioDev: got %q, want %q", c.audioDev, "alsa")
	}
	if !c.extraDisk {
		t.Error("extraDisk should be true")
	}
	if c.extraDiskSize != "128G" {
		t.Errorf("extraDiskSize: got %q, want %q", c.extraDiskSize, "128G")
	}
}

// --- Run validation ---

func TestVMCommandRunMissingImage(t *testing.T) {
	c := NewVMCommand()
	_ = c.fs.Parse([]string{})
	// Manually set a cfg so we don't fail on initBaseConfig.
	c.cfg = &config.MockConfig{}

	err := c.Run()
	if err == nil {
		t.Fatal("expected error for missing --image, got nil")
	}
	if !strings.Contains(err.Error(), "missing required flag: --image") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- VMDriver.Send ---

func TestVMDriverSend(t *testing.T) {
	vm, _, fakeStdin := newTestVMDriver()
	defer fakeStdin.Close()

	go func() {
		_ = vm.Send("hello world")
		vm.stdin.Close()
	}()

	buf := make([]byte, 256)
	n, err := fakeStdin.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("unexpected error reading from stdin pipe: %v", err)
	}
	got := string(buf[:n])
	if got != "hello world\n" {
		t.Errorf("Send wrote %q, want %q", got, "hello world\n")
	}
}

// --- VMDriver.Expect ---

func TestExpectMatchImmediate(t *testing.T) {
	vm, fakeStdout, _ := newTestVMDriver()

	go func() {
		_, _ = fakeStdout.Write([]byte("matrixos login:"))
		fakeStdout.Close()
	}()

	if err := vm.Expect("matrixos login:", 2*time.Second); err != nil {
		t.Fatalf("Expect failed: %v", err)
	}
}

func TestExpectMatchAcrossChunks(t *testing.T) {
	vm, fakeStdout, _ := newTestVMDriver()

	go func() {
		// Write the target string split across two writes.
		_, _ = fakeStdout.Write([]byte("matrix"))
		time.Sleep(50 * time.Millisecond)
		_, _ = fakeStdout.Write([]byte("os login:"))
		fakeStdout.Close()
	}()

	if err := vm.Expect("matrixos login:", 2*time.Second); err != nil {
		t.Fatalf("Expect failed: %v", err)
	}
}

func TestExpectTimeout(t *testing.T) {
	vm, fakeStdout, _ := newTestVMDriver()
	defer fakeStdout.Close()

	// Never write what is expected; should timeout.
	go func() {
		_, _ = fakeStdout.Write([]byte("some other output\n"))
		// Block to keep the pipe open longer than the timeout.
		time.Sleep(5 * time.Second)
	}()

	err := vm.Expect("matrixos login:", 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout waiting for pattern") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestExpectEOFBeforeMatch(t *testing.T) {
	vm, fakeStdout, _ := newTestVMDriver()

	go func() {
		_, _ = fakeStdout.Write([]byte("some output but not the target"))
		fakeStdout.Close()
	}()

	err := vm.Expect("matrixos login:", 2*time.Second)
	if err == nil {
		t.Fatal("expected EOF error, got nil")
	}
	if !strings.Contains(err.Error(), "EOF reached while waiting for pattern") {
		t.Errorf("expected EOF error, got: %v", err)
	}
}

func TestExpectBufferTrimming(t *testing.T) {
	vm, fakeStdout, _ := newTestVMDriver()

	go func() {
		// Write enough data to trigger the 4096-byte trim logic.
		filler := strings.Repeat("x", 5000)
		_, _ = fakeStdout.Write([]byte(filler))
		// Now write the target after the trim boundary.
		_, _ = fakeStdout.Write([]byte("TARGET_FOUND"))
		fakeStdout.Close()
	}()

	if err := vm.Expect("TARGET_FOUND", 2*time.Second); err != nil {
		t.Fatalf("Expect failed after buffer trim: %v", err)
	}
}

func TestExpectWithANSISequences(t *testing.T) {
	vm, fakeStdout, _ := newTestVMDriver()

	go func() {
		// Write output with ANSI escape sequences interspersed.
		_, _ = fakeStdout.Write([]byte("\x1b[2Jmatrixos \x1b[0mlogin:"))
		fakeStdout.Close()
	}()

	// The match buffer includes raw bytes (ANSI stripping only affects print output),
	// so the target must still appear verbatim in the raw stream.
	if err := vm.Expect("login:", 2*time.Second); err != nil {
		t.Fatalf("Expect failed with ANSI output: %v", err)
	}
}

func TestExpectEmptyTarget(t *testing.T) {
	vm, fakeStdout, _ := newTestVMDriver()

	go func() {
		_, _ = fakeStdout.Write([]byte("anything"))
		fakeStdout.Close()
	}()

	// Empty string is always contained in any string.
	if err := vm.Expect("", 2*time.Second); err != nil {
		t.Fatalf("Expect failed for empty target: %v", err)
	}
}

// --- NewVMDriver ---

func TestNewVMDriverReturnsNonNil(t *testing.T) {
	// We pass a bogus binary that won't exist, but NewVMDriver only creates
	// the exec.Cmd without starting it, so it should succeed.
	ctx := context.Background()
	vm, err := NewVMDriver(ctx, []string{"-version"})
	if err != nil {
		t.Fatalf("NewVMDriver returned error: %v", err)
	}
	if vm == nil {
		t.Fatal("NewVMDriver returned nil")
	}
	if vm.cmd == nil {
		t.Error("VMDriver.cmd is nil")
	}
	if vm.stdin == nil {
		t.Error("VMDriver.stdin is nil")
	}
	if vm.stdout == nil {
		t.Error("VMDriver.stdout is nil")
	}
	if vm.reader == nil {
		t.Error("VMDriver.reader is nil")
	}
}

func TestNewVMDriverContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	vm, err := NewVMDriver(ctx, []string{"-version"})
	if err != nil {
		t.Fatalf("NewVMDriver returned error: %v", err)
	}
	// Cancel before starting — just verifying it doesn't panic.
	cancel()
	_ = vm
}

// --- Multiple Expect calls in sequence ---

func TestExpectSequentialMatches(t *testing.T) {
	vm, fakeStdout, _ := newTestVMDriver()

	go func() {
		_, _ = fakeStdout.Write([]byte("matrixos login: "))
		time.Sleep(50 * time.Millisecond)
		_, _ = fakeStdout.Write([]byte("Password: "))
		time.Sleep(50 * time.Millisecond)
		_, _ = fakeStdout.Write([]byte("root@matrixos# "))
		fakeStdout.Close()
	}()

	if err := vm.Expect("matrixos login:", 2*time.Second); err != nil {
		t.Fatalf("first Expect failed: %v", err)
	}
	if err := vm.Expect("Password:", 2*time.Second); err != nil {
		t.Fatalf("second Expect failed: %v", err)
	}
	if err := vm.Expect("#", 2*time.Second); err != nil {
		t.Fatalf("third Expect failed: %v", err)
	}
}

// --- Send and Expect interaction ---

func TestSendAndExpect(t *testing.T) {
	vm, fakeStdout, fakeStdin := newTestVMDriver()

	go func() {
		// Simulate: login prompt → read username → password prompt
		_, _ = fakeStdout.Write([]byte("login: "))

		// Read what the driver sends.
		buf := make([]byte, 256)
		n, _ := fakeStdin.Read(buf)
		got := string(buf[:n])
		if got != "root\n" {
			t.Errorf("expected %q from Send, got %q", "root\n", got)
		}

		_, _ = fakeStdout.Write([]byte("Password: "))

		n, _ = fakeStdin.Read(buf)
		got = string(buf[:n])
		if got != "matrix\n" {
			t.Errorf("expected %q from Send, got %q", "matrix\n", got)
		}

		_, _ = fakeStdout.Write([]byte("root@matrixos# "))
		fakeStdout.Close()
		fakeStdin.Close()
	}()

	if err := vm.Expect("login:", 2*time.Second); err != nil {
		t.Fatalf("Expect login failed: %v", err)
	}
	if err := vm.Send("root"); err != nil {
		t.Fatalf("Send root failed: %v", err)
	}
	if err := vm.Expect("Password:", 2*time.Second); err != nil {
		t.Fatalf("Expect password failed: %v", err)
	}
	if err := vm.Send("matrix"); err != nil {
		t.Fatalf("Send password failed: %v", err)
	}
	if err := vm.Expect("#", 2*time.Second); err != nil {
		t.Fatalf("Expect prompt failed: %v", err)
	}
}

// --- gpuAccel flag ---

func TestVMCommandGpuAccelDefault(t *testing.T) {
	c := NewVMCommand()
	if err := c.fs.Parse([]string{}); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !c.gpuAccel {
		t.Error("gpuAccel should default to true")
	}
}

func TestVMCommandGpuAccelDisabled(t *testing.T) {
	c := NewVMCommand()
	if err := c.fs.Parse([]string{"-gpuaccel=false"}); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if c.gpuAccel {
		t.Error("gpuAccel should be false after -gpuaccel=false")
	}
}

func TestVMCommandGpuAccelDisabledWithNonGraphical(t *testing.T) {
	// Both flags can coexist; gpuAccel is irrelevant when graphical is off,
	// but parsing should still succeed.
	c := NewVMCommand()
	if err := c.fs.Parse([]string{"-gpuaccel=false", "-graphical=false"}); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if c.gpuAccel {
		t.Error("gpuAccel should be false")
	}
	if c.graphical {
		t.Error("graphical should be false")
	}
}

func TestVMCommandGpuAccelExplicitTrue(t *testing.T) {
	c := NewVMCommand()
	if err := c.fs.Parse([]string{"-gpuaccel=true"}); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !c.gpuAccel {
		t.Error("gpuAccel should be true when explicitly set to true")
	}
}

// --- extra_disk flags ---

func TestVMCommandExtraDiskDefault(t *testing.T) {
	c := NewVMCommand()
	if err := c.fs.Parse([]string{}); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if c.extraDisk {
		t.Error("extraDisk should default to false")
	}
	if c.extraDiskSize != "64G" {
		t.Errorf("extraDiskSize should default to %q, got %q", "64G", c.extraDiskSize)
	}
}

func TestVMCommandExtraDiskEnabled(t *testing.T) {
	c := NewVMCommand()
	if err := c.fs.Parse([]string{"-extra_disk"}); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !c.extraDisk {
		t.Error("extraDisk should be true after -extra_disk")
	}
	// Size should still be the default.
	if c.extraDiskSize != "64G" {
		t.Errorf("extraDiskSize should be %q, got %q", "64G", c.extraDiskSize)
	}
}

func TestVMCommandExtraDiskSizeOverride(t *testing.T) {
	c := NewVMCommand()
	if err := c.fs.Parse([]string{"-extra_disk", "-extra_disk_size", "256G"}); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !c.extraDisk {
		t.Error("extraDisk should be true")
	}
	if c.extraDiskSize != "256G" {
		t.Errorf("extraDiskSize: got %q, want %q", c.extraDiskSize, "256G")
	}
}

func TestVMCommandExtraDiskSizeWithoutExtraDisk(t *testing.T) {
	// Setting the size without enabling extra_disk should parse fine;
	// the size is simply ignored at runtime.
	c := NewVMCommand()
	if err := c.fs.Parse([]string{"-extra_disk_size", "100G"}); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if c.extraDisk {
		t.Error("extraDisk should be false")
	}
	if c.extraDiskSize != "100G" {
		t.Errorf("extraDiskSize: got %q, want %q", c.extraDiskSize, "100G")
	}
}

// --- Constants ---

func TestConstants(t *testing.T) {
	bin := qemuSystemBinary()
	if !strings.HasPrefix(bin, "qemu-system-") {
		t.Errorf("qemuSystemBinary(): got %q, want prefix %q", bin, "qemu-system-")
	}
	if rootPassword != "matrix" {
		t.Errorf("rootPassword: got %q, want %q", rootPassword, "matrix")
	}
}

// --- budgetRemaining ---

func TestBudgetRemainingCappedByCap(t *testing.T) {
	// Budget is large; result must be capped to cap.
	start := time.Now()
	got := budgetRemaining(start, 10*time.Second, 3*time.Second)
	if got > 3*time.Second {
		t.Errorf("expected result <= cap (3s), got %v", got)
	}
	if got <= 0 {
		t.Errorf("expected positive result, got %v", got)
	}
}

func TestBudgetRemainingDecrementsByElapsed(t *testing.T) {
	// Start is 2s in the past; budget is 5s; cap is 5s.
	// Remaining should be ~3s, not 5s.
	start := time.Now().Add(-2 * time.Second)
	got := budgetRemaining(start, 5*time.Second, 5*time.Second)
	if got >= 4*time.Second {
		t.Errorf("budget should be reduced by elapsed time; got %v, want < 4s", got)
	}
	if got <= 0 {
		t.Errorf("expected positive result, got %v", got)
	}
}

func TestBudgetRemainingExpired(t *testing.T) {
	// Start is 10s in the past; budget is only 5s → already expired.
	start := time.Now().Add(-10 * time.Second)
	got := budgetRemaining(start, 5*time.Second, 5*time.Second)
	if got > 0 {
		t.Errorf("expected non-positive result for expired budget, got %v", got)
	}
}

func TestBudgetRemainingCapWinsOverBudget(t *testing.T) {
	// Both budget and cap are large; cap must still win.
	start := time.Now()
	got := budgetRemaining(start, 100*time.Second, 1*time.Second)
	if got > 1*time.Second {
		t.Errorf("cap should limit result to 1s, got %v", got)
	}
}

func TestBudgetRemainingBudgetWinsOverCap(t *testing.T) {
	// 4s elapsed out of 5s budget; remaining ≈1s, cap is 10s → budget wins.
	start := time.Now().Add(-4 * time.Second)
	got := budgetRemaining(start, 5*time.Second, 10*time.Second)
	if got >= 2*time.Second {
		t.Errorf("depleted budget should limit result, got %v", got)
	}
	if got <= 0 {
		t.Errorf("expected positive result, got %v", got)
	}
}
