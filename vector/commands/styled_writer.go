package commands

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
)

// ansiStripRe matches all common ANSI escape sequences: CSI, DCS, and OSC.
var ansiStripRe = regexp.MustCompile(
	`\x1b\[[0-9;?]*[a-zA-Z]` + // CSI sequences (including SGR)
		`|\x1bP[^\x1b]*\x1b\\` + // DCS (Device Control Strings)
		`|\x1b\][^\x07]*\x07`, // OSC (Operating System Commands)
)

var (
	globalLogMu   sync.Mutex
	globalLogDest io.Writer
)

// SetGlobalLogWriter sets a process-wide log writer that every
// subsequently created styledWriter will tee ANSI-stripped output to.
// Call ClearGlobalLogWriter when done.
func SetGlobalLogWriter(w io.Writer) {
	globalLogMu.Lock()
	defer globalLogMu.Unlock()
	globalLogDest = w
}

// ClearGlobalLogWriter removes the process-wide log writer.
func ClearGlobalLogWriter() {
	SetGlobalLogWriter(nil)
}

// styledWriter is an io.Writer that decorates each complete line of output
// with a colored/bold prefix and an ANSI reset suffix.  Partial writes are
// buffered until a newline arrives so that every line gets the full treatment.
//
// It is TTY-aware:
//   - When writing to a terminal, carriage-return (\r) based overwrites are
//     rendered in-place so progress bars and spinners look correct.
//   - When writing to a non-terminal (e.g. a log file), \r segments and ANSI
//     escape sequences are silently stripped so the output stays clean.
type styledWriter struct {
	mu           sync.Mutex
	dest         io.Writer
	logDest      io.Writer // optional secondary writer for log capture
	prefix       string    // already styled prefix string (e.g. icon + label)
	style        string    // ANSI escape applied to the line body
	reset        string    // ANSI reset sequence
	buf          []byte    // partial-line accumulator
	isTTY        bool      // whether dest is a terminal
	hasOverwrite bool      // TTY-only: an overwrite line is currently displayed
}

// newStyledWriter creates a writer that prefixes every complete line with
// style+prefix and appends a reset sequence.  When isTTY is false, incoming
// ANSI escape sequences and \r-based overwrites are stripped before output.
func newStyledWriter(dest io.Writer, prefix, style, reset string, isTTY bool) *styledWriter {
	globalLogMu.Lock()
	lw := globalLogDest
	globalLogMu.Unlock()

	return &styledWriter{
		dest:    dest,
		logDest: lw,
		prefix:  prefix,
		style:   style,
		reset:   reset,
		isTTY:   isTTY,
	}
}

// SetLogWriter sets a secondary writer that receives ANSI-stripped
// copies of every completed line.  Pass nil to disable.
func (w *styledWriter) SetLogWriter(lw io.Writer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.logDest = lw
}

// writeToLog writes content to the log writer after stripping ANSI
// escape sequences.  It is a no-op when no log writer is set.
func (w *styledWriter) writeToLog(content string) {
	if w.logDest == nil {
		return
	}
	_, _ = io.WriteString(
		w.logDest,
		ansiStripRe.ReplaceAllString(content, ""),
	)
}

// Write implements io.Writer.  It buffers partial lines and flushes each
// complete line individually so every line gets the prefix/style treatment.
// Carriage returns (\r) are handled specially:
//   - On a TTY the current line is overwritten in place.
//   - Otherwise the overwritten content is silently dropped.
func (w *styledWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf = append(w.buf, p...)
	total := len(p)

	for {
		nlIdx := bytes.IndexByte(w.buf, '\n')
		crIdx := bytes.IndexByte(w.buf, '\r')

		if nlIdx < 0 && crIdx < 0 {
			break
		}

		// Treat \r\n as a regular newline.
		if crIdx >= 0 && crIdx+1 < len(w.buf) && w.buf[crIdx+1] == '\n' {
			line := string(w.buf[:crIdx])
			w.buf = w.buf[crIdx+2:]
			if err := w.writeCompleteLine(line); err != nil {
				return total, err
			}
			continue
		}

		// Standalone \r – the process is overwriting the current line
		// (e.g. a progress bar).  However, if \r is the very last byte
		// in the buffer we cannot tell yet whether a \n follows (making
		// it a \r\n pair).  Keep it buffered and wait for the next Write.
		if crIdx >= 0 && (nlIdx < 0 || crIdx < nlIdx) {
			if crIdx == len(w.buf)-1 {
				break // wait for more data
			}
			content := string(w.buf[:crIdx])
			w.buf = w.buf[crIdx+1:]

			if w.isTTY && strings.TrimSpace(content) != "" {
				if err := w.writeOverwriteLine(content); err != nil {
					return total, err
				}
			}
			// In non-TTY mode the overwritten segment is simply discarded.
			continue
		}

		// Regular newline.
		line := string(w.buf[:nlIdx])
		w.buf = w.buf[nlIdx+1:]
		if err := w.writeCompleteLine(line); err != nil {
			return total, err
		}
	}

	return total, nil
}

// cleanLine strips ANSI sequences from line when not writing to a TTY.
func (w *styledWriter) cleanLine(line string) string {
	if w.isTTY {
		return line
	}
	return ansiStripRe.ReplaceAllString(line, "")
}

// writeCompleteLine formats and writes a single finished line (after \n) to
// the underlying destination, clearing any in-progress overwrite first.
func (w *styledWriter) writeCompleteLine(line string) error {
	// If we previously wrote an overwrite line, clear it first.
	if w.isTTY && w.hasOverwrite {
		_, _ = io.WriteString(w.dest, "\r\033[2K")
		w.hasOverwrite = false
	}

	line = w.cleanLine(line)

	// Preserve blank lines without the prefix to keep output tidy.
	if strings.TrimSpace(line) == "" {
		w.writeToLog("\n")
		_, err := fmt.Fprintln(w.dest)
		return err
	}

	// Plain mode: no prefix/style decoration.
	if w.prefix == "" {
		plain := fmt.Sprintf("%s\n", line)
		w.writeToLog(plain)
		_, err := fmt.Fprintf(w.dest, "%s\n", line)
		return err
	}

	formatted := fmt.Sprintf("%s%s %s%s\n", w.style, w.prefix, line, w.reset)
	w.writeToLog(formatted)
	_, err := io.WriteString(w.dest, formatted)
	return err
}

// writeOverwriteLine writes a temporary (no trailing newline) line to the
// terminal that will be overwritten by the next \r segment or completed by
// the next \n.  Only called when isTTY is true.
func (w *styledWriter) writeOverwriteLine(line string) error {
	var formatted string
	if w.prefix == "" {
		formatted = fmt.Sprintf("\r\033[2K%s", line)
	} else {
		formatted = fmt.Sprintf("\r\033[2K%s%s %s%s", w.style, w.prefix, line, w.reset)
	}
	_, err := io.WriteString(w.dest, formatted)
	w.hasOverwrite = true
	return err
}

// Flush writes any remaining buffered content that did not end with a newline.
func (w *styledWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked(true)
}

// FlushInline writes any remaining buffered content without appending a
// trailing newline.  Use this before reading interactive input so the
// cursor stays on the same line as the prompt.
func (w *styledWriter) FlushInline() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushLocked(false)
}

// flushLocked is the shared implementation for Flush / FlushInline.
// When addNewline is true the output ends with \n; otherwise the cursor
// stays at the end of the line.
func (w *styledWriter) flushLocked(addNewline bool) {
	if w.isTTY && w.hasOverwrite {
		_, _ = io.WriteString(w.dest, "\r\033[2K")
		w.hasOverwrite = false
	}

	if len(w.buf) == 0 {
		return
	}

	line := w.cleanLine(string(w.buf))
	w.buf = nil
	if strings.TrimSpace(line) == "" {
		return
	}

	nl := ""
	if addNewline {
		nl = "\n"
	}

	if w.prefix == "" {
		plain := fmt.Sprintf("%s%s", line, nl)
		w.writeToLog(plain)
		_, _ = fmt.Fprintf(w.dest, "%s%s", line, nl)
		return
	}

	formatted := fmt.Sprintf("%s%s %s%s%s", w.style, w.prefix, line, w.reset, nl)
	w.writeToLog(formatted)
	_, _ = io.WriteString(w.dest, formatted)
}
