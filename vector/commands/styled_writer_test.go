package commands

import (
	"bytes"
	"strings"
	"testing"
)

// --- newStyledWriter ---

func TestNewStyledWriter(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "[P]", "\033[32m", "\033[0m", true)
	if sw == nil {
		t.Fatal("expected non-nil styledWriter")
	}
	if sw.dest != &buf {
		t.Error("dest not set correctly")
	}
	if sw.prefix != "[P]" {
		t.Errorf("prefix = %q, want %q", sw.prefix, "[P]")
	}
	if sw.isTTY != true {
		t.Error("isTTY should be true")
	}
}

// --- cleanLine ---

func TestCleanLineTTY(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)
	// TTY mode: ANSI sequences should be preserved.
	input := "\033[31mred\033[0m"
	got := sw.cleanLine(input)
	if got != input {
		t.Errorf("cleanLine (TTY) = %q, want %q", got, input)
	}
}

func TestCleanLineNonTTY(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", false)
	input := "\033[31mred\033[0m normal"
	got := sw.cleanLine(input)
	if strings.Contains(got, "\033[") {
		t.Errorf("cleanLine (non-TTY) should strip ANSI, got %q", got)
	}
	if got != "red normal" {
		t.Errorf("cleanLine (non-TTY) = %q, want %q", got, "red normal")
	}
}

func TestCleanLineStripsDCS(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", false)
	input := "before\x1bPdevice\x1b\\after"
	got := sw.cleanLine(input)
	if got != "beforeafter" {
		t.Errorf("cleanLine DCS strip = %q, want %q", got, "beforeafter")
	}
}

func TestCleanLineStripsOSC(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", false)
	input := "before\x1b]0;title\x07after"
	got := sw.cleanLine(input)
	if got != "beforeafter" {
		t.Errorf("cleanLine OSC strip = %q, want %q", got, "beforeafter")
	}
}

// --- Write: complete lines ---

func TestWriteCompleteLine(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "[X]", "\033[1m", "\033[0m", true)

	n, err := sw.Write([]byte("hello world\n"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 12 {
		t.Errorf("Write returned %d, want 12", n)
	}

	got := buf.String()
	if !strings.Contains(got, "[X]") {
		t.Errorf("output missing prefix, got %q", got)
	}
	if !strings.Contains(got, "hello world") {
		t.Errorf("output missing content, got %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("output should end with newline, got %q", got)
	}
}

func TestWriteBlankLine(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "[X]", "\033[1m", "\033[0m", true)

	sw.Write([]byte("\n"))
	got := buf.String()
	// Blank lines should be written without prefix.
	if strings.Contains(got, "[X]") {
		t.Errorf("blank line should not have prefix, got %q", got)
	}
	if got != "\n" {
		t.Errorf("blank line output = %q, want %q", got, "\n")
	}
}

func TestWriteNoPrefix(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)

	sw.Write([]byte("plain line\n"))
	got := buf.String()
	if got != "plain line\n" {
		t.Errorf("no-prefix output = %q, want %q", got, "plain line\n")
	}
}

func TestWriteMultipleLines(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, ">", "", "", true)

	sw.Write([]byte("line1\nline2\nline3\n"))
	got := buf.String()
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), got)
	}
	for i, l := range lines {
		if !strings.Contains(l, ">") {
			t.Errorf("line %d missing prefix: %q", i, l)
		}
	}
}

func TestWritePartialThenComplete(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)

	// Partial write — no output yet.
	sw.Write([]byte("partial"))
	if buf.Len() != 0 {
		t.Errorf("expected no output for partial write, got %q", buf.String())
	}

	// Complete the line.
	sw.Write([]byte(" done\n"))
	got := buf.String()
	if got != "partial done\n" {
		t.Errorf("completed partial = %q, want %q", got, "partial done\n")
	}
}

// --- Write: carriage return handling ---

func TestWriteCRLF(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)

	sw.Write([]byte("windows line\r\n"))
	got := buf.String()
	if got != "windows line\n" {
		t.Errorf("CRLF output = %q, want %q", got, "windows line\n")
	}
}

func TestWriteLFFollowedByCRLF(t *testing.T) {
	t.Run("PreservesPrefixOnBothLines", func(t *testing.T) {
		var buf bytes.Buffer
		sw := newStyledWriter(&buf, "[P]", "", "", false)

		if _, err := sw.Write([]byte("line1\nline2\r\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		want := "[P] line1\n[P] line2\n"
		if got := buf.String(); got != want {
			t.Errorf("Write(\"line1\\nline2\\r\\n\") = %q, want %q", got, want)
		}
	})

	t.Run("PreservesTwoBlankLines", func(t *testing.T) {
		var buf bytes.Buffer
		sw := newStyledWriter(&buf, "[P]", "", "", false)

		if _, err := sw.Write([]byte("\n\r\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if got := buf.String(); got != "\n\n" {
			t.Errorf("Write(\"\\n\\r\\n\") = %q, want \"\\n\\n\"", got)
		}
	})
}

func TestFlushStripsBufferedTrailingCR(t *testing.T) {
	t.Run("NonTTYAndLog", func(t *testing.T) {
		var dest, logBuf bytes.Buffer
		sw := newStyledWriter(&dest, "[P]", "", "", false)
		sw.SetLogWriter(&logBuf)

		if _, err := sw.Write([]byte("progress 50%\rprogress 100%\r")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		sw.Flush()
		if strings.Contains(dest.String(), "\r") {
			t.Errorf("non-TTY Flush leaked raw CR: %q", dest.String())
		}
		if strings.Contains(logBuf.String(), "\r") {
			t.Errorf("log Flush leaked raw CR: %q", logBuf.String())
		}
		if got := dest.String(); got != "[P] progress 100%\n" {
			t.Errorf("dest = %q, want %q", got, "[P] progress 100%\n")
		}
	})

	t.Run("TTYAndFlushInline", func(t *testing.T) {
		var dest bytes.Buffer
		sw := newStyledWriter(&dest, "[P]", "\033[32m", "\033[0m", true)

		if _, err := sw.Write([]byte("100%\r")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		sw.FlushInline()
		if strings.Contains(dest.String(), "\r") {
			t.Errorf("TTY FlushInline leaked raw trailing CR: %q", dest.String())
		}
	})
}

type failFirstWriter struct {
	failed bool
	buf    bytes.Buffer
}

func (f *failFirstWriter) Write(p []byte) (int, error) {
	if !f.failed {
		f.failed = true
		return 0, bytes.ErrTooLarge
	}
	return f.buf.Write(p)
}

func TestWriteConsecutiveCRBeforeLFAndFlush(t *testing.T) {
	t.Run("NonTTY_CRCRLF", func(t *testing.T) {
		var dest bytes.Buffer
		sw := newStyledWriter(&dest, "[P]", "", "", false)
		if _, err := sw.Write([]byte("hello\r\r\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if got := dest.String(); got != "[P] hello\n" {
			t.Errorf("Write(\"hello\\r\\r\\n\") = %q, want %q", got, "[P] hello\n")
		}
	})

	t.Run("NonTTY_SplitCR_CRLF", func(t *testing.T) {
		var dest bytes.Buffer
		sw := newStyledWriter(&dest, "[P]", "", "", false)
		if _, err := sw.Write([]byte("hello\r")); err != nil {
			t.Fatalf("Write 1: %v", err)
		}
		if _, err := sw.Write([]byte("\r\n")); err != nil {
			t.Fatalf("Write 2: %v", err)
		}
		if got := dest.String(); got != "[P] hello\n" {
			t.Errorf("split Write(\"hello\\r\", \"\\r\\n\") = %q, want %q", got, "[P] hello\n")
		}
	})

	t.Run("NonTTY_CRCR_ThenFlush", func(t *testing.T) {
		var dest bytes.Buffer
		sw := newStyledWriter(&dest, "[P]", "", "", false)
		if _, err := sw.Write([]byte("hello\r\r")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		sw.Flush()
		if got := dest.String(); got != "[P] hello\n" {
			t.Errorf("Write(\"hello\\r\\r\") + Flush() = %q, want %q", got, "[P] hello\n")
		}
	})

	t.Run("TTY_CRCRLF_PreservesLineAndLog", func(t *testing.T) {
		var dest, logBuf bytes.Buffer
		sw := newStyledWriter(&dest, "[P]", "", "", true)
		sw.SetLogWriter(&logBuf)
		if _, err := sw.Write([]byte("hello\r\r\n")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if got := logBuf.String(); got != "[P] hello\n" {
			t.Errorf("logBuf = %q, want %q", got, "[P] hello\n")
		}
		if got := dest.String(); got != "[P] hello\n" {
			t.Errorf("dest = %q, want %q", got, "[P] hello\n")
		}
	})
}

func TestFlushAfterDestWriteErrorStillParsesBufferedLines(t *testing.T) {
	dest := &failFirstWriter{}
	var logBuf bytes.Buffer
	sw := newStyledWriter(dest, "[P]", "", "", false)
	sw.SetLogWriter(&logBuf)

	if _, err := sw.Write([]byte("line1\nprogress 50%\rprogress 100%\nline3\n")); err == nil {
		t.Fatal("expected Write to return dest error")
	}
	sw.Flush()

	want := "[P] line1\n[P] progress 100%\n[P] line3\n"
	if got := logBuf.String(); got != want {
		t.Errorf("logBuf after Flush = %q, want %q", got, want)
	}
	if strings.Contains(dest.buf.String(), "\r") || strings.Contains(dest.buf.String(), "progress 50%") {
		t.Errorf("dest received unparsed CR/overwrite buffer: %q", dest.buf.String())
	}
}

func TestWriteCROverwriteTTY(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)

	// Standalone \r on TTY should produce an overwrite line.
	sw.Write([]byte("progress 50%\rprogress 100%\n"))
	got := buf.String()
	// Should contain the overwrite escape and the final line.
	if !strings.Contains(got, "progress 50%") {
		t.Errorf("expected overwrite content, got %q", got)
	}
	if !strings.Contains(got, "progress 100%") {
		t.Errorf("expected final line, got %q", got)
	}
}

func TestWriteCROverwriteNonTTY(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", false)

	// Non-TTY: \r segments are discarded.
	sw.Write([]byte("progress 50%\rprogress 100%\n"))
	got := buf.String()
	// Only the final line after the last \r should appear.
	if strings.Contains(got, "progress 50%") {
		t.Errorf("non-TTY should discard \\r segments, got %q", got)
	}
	if !strings.Contains(got, "progress 100%") {
		t.Errorf("expected final line, got %q", got)
	}
}

func TestWriteCREmptySegment(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)

	// Empty \r segment (just whitespace) should be silently ignored on TTY.
	sw.Write([]byte("   \rreal line\n"))
	got := buf.String()
	if !strings.Contains(got, "real line") {
		t.Errorf("expected final real line, got %q", got)
	}
}

// --- writeOverwriteLine ---

func TestWriteOverwriteLineWithPrefix(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "[P]", "\033[1m", "\033[0m", true)

	err := sw.writeOverwriteLine("downloading...")
	if err != nil {
		t.Fatalf("writeOverwriteLine error: %v", err)
	}
	if !sw.hasOverwrite {
		t.Error("hasOverwrite should be true")
	}
	got := buf.String()
	if !strings.Contains(got, "[P]") {
		t.Errorf("overwrite line missing prefix, got %q", got)
	}
	if !strings.Contains(got, "downloading...") {
		t.Errorf("overwrite line missing content, got %q", got)
	}
	if !strings.HasPrefix(got, "\r\033[2K") {
		t.Errorf("overwrite line should start with CR+clear, got %q", got)
	}
}

func TestWriteOverwriteLineNoPrefix(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)

	sw.writeOverwriteLine("bare progress")
	got := buf.String()
	if !strings.HasPrefix(got, "\r\033[2K") {
		t.Errorf("expected CR+clear prefix, got %q", got)
	}
	if !strings.Contains(got, "bare progress") {
		t.Errorf("expected content, got %q", got)
	}
}

// --- writeCompleteLine clears overwrite ---

func TestWriteCompleteLineClearsOverwrite(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)
	sw.hasOverwrite = true

	sw.writeCompleteLine("final")
	if sw.hasOverwrite {
		t.Error("hasOverwrite should be false after writeCompleteLine")
	}
	got := buf.String()
	// Should contain the clear sequence before the line.
	if !strings.Contains(got, "\r\033[2K") {
		t.Errorf("expected overwrite clear, got %q", got)
	}
	if !strings.Contains(got, "final") {
		t.Errorf("expected line content, got %q", got)
	}
}

// --- Flush ---

func TestFlushWithContent(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)

	sw.Write([]byte("buffered"))
	if buf.Len() != 0 {
		t.Fatal("expected no output before flush")
	}

	sw.Flush()
	got := buf.String()
	if !strings.Contains(got, "buffered") {
		t.Errorf("flush should write buffered content, got %q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("Flush should add newline, got %q", got)
	}
}

func TestFlushEmpty(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)

	sw.Flush()
	if buf.Len() != 0 {
		t.Errorf("flushing empty buffer should produce no output, got %q", buf.String())
	}
}

func TestFlushWithPrefix(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "[P]", "\033[1m", "\033[0m", true)

	sw.Write([]byte("trail"))
	sw.Flush()
	got := buf.String()
	if !strings.Contains(got, "[P]") {
		t.Errorf("flush with prefix missing prefix, got %q", got)
	}
	if !strings.Contains(got, "trail") {
		t.Errorf("flush should contain buffered content, got %q", got)
	}
}

func TestFlushBlankOnly(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)

	sw.Write([]byte("   "))
	sw.Flush()
	if buf.Len() != 0 {
		t.Errorf("flushing whitespace-only buffer should produce no output, got %q", buf.String())
	}
}

func TestFlushClearsOverwrite(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)
	sw.hasOverwrite = true

	sw.Write([]byte("trail"))
	sw.Flush()
	got := buf.String()
	if !strings.Contains(got, "\r\033[2K") {
		t.Errorf("flush should clear overwrite, got %q", got)
	}
	if sw.hasOverwrite {
		t.Error("hasOverwrite should be false after flush")
	}
}

// --- FlushInline ---

func TestFlushInline(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)

	sw.Write([]byte("prompt>"))
	sw.FlushInline()
	got := buf.String()
	if !strings.Contains(got, "prompt>") {
		t.Errorf("FlushInline should write content, got %q", got)
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("FlushInline should NOT add newline, got %q", got)
	}
}

func TestFlushInlineWithPrefix(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "[>]", "\033[1m", "\033[0m", true)

	sw.Write([]byte("input? "))
	sw.FlushInline()
	got := buf.String()
	if !strings.Contains(got, "[>]") {
		t.Errorf("FlushInline with prefix missing prefix, got %q", got)
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("FlushInline should not end with newline, got %q", got)
	}
}

// --- Non-TTY ANSI stripping in Write ---

func TestWriteNonTTYStripsANSI(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "[X]", "", "", false)

	sw.Write([]byte("\033[1m\033[32mbold green text\033[0m\n"))
	got := buf.String()
	if strings.Contains(got, "\033[") {
		t.Errorf("non-TTY Write should strip ANSI, got %q", got)
	}
	if !strings.Contains(got, "bold green text") {
		t.Errorf("expected text content, got %q", got)
	}
}

// --- Concurrent write safety ---

func TestWriteConcurrent(t *testing.T) {
	var buf bytes.Buffer
	sw := newStyledWriter(&buf, "", "", "", true)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			sw.Write([]byte("line\n"))
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != 10 {
		t.Errorf("expected 10 lines from concurrent writes, got %d", len(lines))
	}
}

// --- SetLogWriter ---

func TestSetLogWriterCaptures(t *testing.T) {
	var dest, logBuf bytes.Buffer
	sw := newStyledWriter(&dest, "[P]", "\033[32m", "\033[0m", true)
	sw.SetLogWriter(&logBuf)

	sw.Write([]byte("hello world\n"))

	// Dest (stdout/stderr) should receive the styled output.
	if !strings.Contains(dest.String(), "hello world") {
		t.Errorf("dest should contain line, got %q", dest.String())
	}

	// Log should receive the ANSI-stripped output.
	got := logBuf.String()
	if !strings.Contains(got, "hello world") {
		t.Errorf("log should contain line, got %q", got)
	}
	if strings.Contains(got, "\033[") {
		t.Error("log output should be ANSI-stripped")
	}
}

func TestSetLogWriterBlankLine(t *testing.T) {
	var dest, logBuf bytes.Buffer
	sw := newStyledWriter(&dest, "[P]", "\033[32m", "\033[0m", true)
	sw.SetLogWriter(&logBuf)

	sw.Write([]byte("\n"))

	if logBuf.String() != "\n" {
		t.Errorf("log blank line = %q, want %q", logBuf.String(), "\n")
	}
}

func TestSetLogWriterNoPrefix(t *testing.T) {
	var dest, logBuf bytes.Buffer
	sw := newStyledWriter(&dest, "", "", "", true)
	sw.SetLogWriter(&logBuf)

	sw.Write([]byte("plain\n"))

	if logBuf.String() != "plain\n" {
		t.Errorf("log = %q, want %q", logBuf.String(), "plain\n")
	}
}

func TestSetLogWriterNil(t *testing.T) {
	var dest bytes.Buffer
	sw := newStyledWriter(&dest, "[P]", "\033[32m", "\033[0m", true)
	// No log writer set — should not panic.
	sw.Write([]byte("safe\n"))
	if !strings.Contains(dest.String(), "safe") {
		t.Error("dest should still receive output")
	}
}

func TestSetLogWriterFlush(t *testing.T) {
	var dest, logBuf bytes.Buffer
	sw := newStyledWriter(&dest, "[P]", "\033[32m", "\033[0m", true)
	sw.SetLogWriter(&logBuf)

	// Write partial (no newline), then flush.
	sw.Write([]byte("partial"))
	sw.Flush()

	got := logBuf.String()
	if !strings.Contains(got, "partial") {
		t.Errorf("log after flush should contain partial, got %q", got)
	}
	if strings.Contains(got, "\033[") {
		t.Error("log output after flush should be ANSI-stripped")
	}
}

// --- UI.SetLogWriter ---

func TestUISetLogWriterCapturesBothStreams(t *testing.T) {
	var stdoutDest, stderrDest, logBuf bytes.Buffer

	ui := &UI{}
	ui.printer = newStyledWriter(
		&stdoutDest, "[out]", "\033[32m", "\033[0m", true,
	)
	ui.errPrinter = newStyledWriter(
		&stderrDest, "[err]", "\033[31m", "\033[0m", true,
	)

	// Both printers share the same log writer.
	ui.SetLogWriter(&logBuf)

	// Write through stdout printer.
	ui.Printf("stdout line\n")
	// Write through stderr printer.
	ui.PrintErrf("stderr line\n")

	// stdout dest should have the stdout line only.
	if !strings.Contains(stdoutDest.String(), "stdout line") {
		t.Errorf("stdout dest missing output, got %q",
			stdoutDest.String())
	}
	if strings.Contains(stdoutDest.String(), "stderr line") {
		t.Error("stdout dest should not contain stderr output")
	}

	// stderr dest should have the stderr line only.
	if !strings.Contains(stderrDest.String(), "stderr line") {
		t.Errorf("stderr dest missing output, got %q",
			stderrDest.String())
	}
	if strings.Contains(stderrDest.String(), "stdout line") {
		t.Error("stderr dest should not contain stdout output")
	}

	// Log should have BOTH lines, ANSI-stripped.
	logStr := logBuf.String()
	if !strings.Contains(logStr, "stdout line") {
		t.Errorf("log missing stdout line, got %q", logStr)
	}
	if !strings.Contains(logStr, "stderr line") {
		t.Errorf("log missing stderr line, got %q", logStr)
	}
	if strings.Contains(logStr, "\033[") {
		t.Error("log should be ANSI-stripped")
	}
}

// --- SetGlobalLogWriter ---

func TestGlobalLogWriterInheritedByNewWriters(t *testing.T) {
	var logBuf bytes.Buffer
	SetGlobalLogWriter(&logBuf)
	defer ClearGlobalLogWriter()

	// Simulate a sub-command creating its own styledWriter
	// after the global is set — it should inherit the log.
	var dest bytes.Buffer
	sw := newStyledWriter(
		&dest, "[sub]", "\033[34m", "\033[0m", true,
	)

	sw.Write([]byte("sub-command output\n"))

	// Dest gets the styled output.
	if !strings.Contains(dest.String(), "sub-command output") {
		t.Errorf("dest missing output, got %q", dest.String())
	}

	// Global log gets the ANSI-stripped output.
	logStr := logBuf.String()
	if !strings.Contains(logStr, "sub-command output") {
		t.Errorf("global log missing output, got %q", logStr)
	}
	if strings.Contains(logStr, "\033[") {
		t.Error("global log should be ANSI-stripped")
	}
}

func TestGlobalLogWriterClearedAfter(t *testing.T) {
	var logBuf bytes.Buffer
	SetGlobalLogWriter(&logBuf)
	ClearGlobalLogWriter()

	// Writers created after clearing should not log.
	var dest bytes.Buffer
	sw := newStyledWriter(&dest, "[P]", "", "", true)
	sw.Write([]byte("after clear\n"))

	if logBuf.Len() != 0 {
		t.Errorf(
			"log should be empty after clear, got %q",
			logBuf.String(),
		)
	}
}

func TestGlobalLogWriterMultipleSubCommands(t *testing.T) {
	var logBuf bytes.Buffer
	SetGlobalLogWriter(&logBuf)
	defer ClearGlobalLogWriter()

	// Simulate the AllCommand scenario: parent printer set
	// per-instance, then multiple sub-commands created.
	var parentDest bytes.Buffer
	parent := newStyledWriter(
		&parentDest, "[all]", "\033[32m", "\033[0m", true,
	)
	parent.SetLogWriter(&logBuf) // per-instance (pre-existing)
	parent.Write([]byte("parent line\n"))

	// Sub-command 1: its own UI with own printers.
	var sub1Dest bytes.Buffer
	sub1 := newStyledWriter(
		&sub1Dest, "[seeds]", "\033[34m", "\033[0m", true,
	)
	sub1.Write([]byte("seed output\n"))

	// Sub-command 2: its own UI with own printers.
	var sub2Dest bytes.Buffer
	sub2 := newStyledWriter(
		&sub2Dest, "[release]", "\033[35m", "\033[0m", true,
	)
	sub2.Write([]byte("release output\n"))

	logStr := logBuf.String()
	for _, want := range []string{
		"parent line", "seed output", "release output",
	} {
		if !strings.Contains(logStr, want) {
			t.Errorf("log missing %q, got %q", want, logStr)
		}
	}
	if strings.Contains(logStr, "\033[") {
		t.Error("log should be ANSI-stripped")
	}
}
