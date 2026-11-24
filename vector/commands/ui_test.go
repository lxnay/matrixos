package commands

import (
	"bytes"
	"strings"
	"testing"
)

// --- IsTTY ---

func TestIsTTYDefault(t *testing.T) {
	var ui UI
	// Default should be false (not initialized).
	if ui.IsTTY() {
		t.Error("IsTTY should be false before StartUI")
	}
}

// --- NewStdoutWriter / NewStderrWriter ---

func TestNewStdoutWriter(t *testing.T) {
	var ui UI
	ui.StartUI()
	sw := ui.NewStdoutWriter("test")
	if sw == nil {
		t.Fatal("NewStdoutWriter returned nil")
	}
}

func TestNewStderrWriter(t *testing.T) {
	var ui UI
	ui.StartUI()
	sw := ui.NewStderrWriter("test")
	if sw == nil {
		t.Fatal("NewStderrWriter returned nil")
	}
}

// --- SetupPrinters / StdoutWriter / StderrWriter ---

func TestSetupPrinters(t *testing.T) {
	var ui UI
	ui.StartUI()

	if ui.StdoutWriter() != nil {
		t.Error("StdoutWriter should be nil before SetupPrinters")
	}
	if ui.StderrWriter() != nil {
		t.Error("StderrWriter should be nil before SetupPrinters")
	}

	ui.SetupPrinters("mycommand")

	if ui.StdoutWriter() == nil {
		t.Error("StdoutWriter should be non-nil after SetupPrinters")
	}
	if ui.StderrWriter() == nil {
		t.Error("StderrWriter should be non-nil after SetupPrinters")
	}
}

func TestSetupPlainPrinters(t *testing.T) {
	var ui UI
	ui.StartUI()

	ui.SetupPlainPrinters()

	if ui.StdoutWriter() == nil {
		t.Error("StdoutWriter should be non-nil after SetupPlainPrinters")
	}
	if ui.StderrWriter() == nil {
		t.Error("StderrWriter should be non-nil after SetupPlainPrinters")
	}
}

// --- Println / Printf (with buffer-backed printers) ---

func newUIWithBuffers(t *testing.T) (*UI, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var ui UI
	ui.StartUI()

	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	ui.printer = newStyledWriter(outBuf, nil, "", "", "", false)
	ui.errPrinter = newStyledWriter(errBuf, nil, "", "", "", false)
	return &ui, outBuf, errBuf
}

func TestPrintln(t *testing.T) {
	ui, out, _ := newUIWithBuffers(t)
	ui.Println("hello world")
	ui.FlushPrinters()
	got := out.String()
	if !strings.Contains(got, "hello world") {
		t.Errorf("Println output = %q, want to contain %q", got, "hello world")
	}
}

func TestPrintlnNoPrinter(t *testing.T) {
	// When printer is nil, Println should not panic.
	var ui UI
	// Just verify it doesn't panic (output goes to real stdout).
	ui.Println("fallback test")
}

func TestPrintf(t *testing.T) {
	ui, out, _ := newUIWithBuffers(t)
	ui.Printf("count: %d\n", 42)
	ui.FlushPrinters()
	got := out.String()
	if !strings.Contains(got, "count: 42") {
		t.Errorf("Printf output = %q, want to contain %q", got, "count: 42")
	}
}

func TestPrintfNoPrinter(t *testing.T) {
	var ui UI
	// Should not panic.
	ui.Printf("fallback %s\n", "test")
}

func TestPrintErr(t *testing.T) {
	ui, _, errBuf := newUIWithBuffers(t)
	ui.PrintErr("something went wrong")
	ui.FlushPrinters()
	got := errBuf.String()
	if !strings.Contains(got, "something went wrong") {
		t.Errorf("PrintErr output = %q, want to contain %q", got, "something went wrong")
	}
}

func TestPrintErrNoPrinter(t *testing.T) {
	var ui UI
	// Should not panic.
	ui.PrintErr("fallback error")
}

func TestPrintErrf(t *testing.T) {
	ui, _, errBuf := newUIWithBuffers(t)
	ui.PrintErrf("error: %s\n", "timeout")
	ui.FlushPrinters()
	got := errBuf.String()
	if !strings.Contains(got, "error: timeout") {
		t.Errorf("PrintErrf output = %q, want to contain %q", got, "error: timeout")
	}
}

func TestPrintErrf_NoPrinter(t *testing.T) {
	var ui UI
	// Should not panic.
	ui.PrintErrf("fallback %s\n", "error")
}

// --- FlushPrinters ---

func TestFlushPrintersNil(t *testing.T) {
	var ui UI
	// Should not panic when printers are nil.
	ui.FlushPrinters()
}

func TestFlushPrintersFlushesContent(t *testing.T) {
	ui, out, errBuf := newUIWithBuffers(t)

	// Write partial content (no newline).
	ui.Printf("partial stdout")
	ui.PrintErrf("partial stderr")

	// Before flush, buffers hold content internally.
	ui.FlushPrinters()

	stdout := out.String()
	stderr := errBuf.String()
	if !strings.Contains(stdout, "partial stdout") {
		t.Errorf("FlushPrinters should flush stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "partial stderr") {
		t.Errorf("FlushPrinters should flush stderr, got %q", stderr)
	}
}

// --- StartUI icons ---

func TestStartUISetsIcons(t *testing.T) {
	var ui UI
	ui.StartUI()

	// Running in test (not a TTY), so we get the non-emoji icons.
	if ui.iconCheck == "" {
		t.Error("iconCheck should be set after StartUI")
	}
	if ui.iconError == "" {
		t.Error("iconError should be set after StartUI")
	}
	if ui.separator == "" {
		t.Error("separator should be set after StartUI")
	}
}

// --- SignalGuard additional coverage ---

func TestPushCleanupAndRunCleanups(t *testing.T) {
	var sg SignalGuard
	var order []string
	sg.PushCleanup(func() { order = append(order, "a") })
	sg.PushCleanup(func() { order = append(order, "b") })
	sg.RunCleanups()

	if len(order) != 2 {
		t.Fatalf("expected 2 cleanups, got %d", len(order))
	}
	if order[0] != "b" || order[1] != "a" {
		t.Errorf("order = %v, want [b, a]", order)
	}

	// Second RunCleanups should be no-op.
	sg.RunCleanups()
	if len(order) != 2 {
		t.Error("RunCleanups should be idempotent")
	}
}
