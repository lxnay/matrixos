// Package worker provides shared concurrency and I/O primitives used by the
// parallel pipeline stages (seeder, releaser, imager).
package worker

import (
	"fmt"
	"io"
	"os"
	"sync"
)

// IO holds synchronized stdout and stderr writers and provides formatted
// printing helpers. It is intended to be embedded in worker structs (Imager,
// Releaser, Seeder) and their mocks.
//
// A zero-value IO is immediately usable and defaults to os.Stdout and
// os.Stderr whenever a writer is unset (nil).
type IO struct {
	mu     sync.Mutex
	stdout io.Writer
	stderr io.Writer
}

// SetStdout replaces the writer used for informational output.
func (x *IO) SetStdout(w io.Writer) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.stdout = w
}

// SetStderr replaces the writer used for warnings and errors.
func (x *IO) SetStderr(w io.Writer) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.stderr = w
}

// Stdout returns the current informational output writer, defaulting to
// os.Stdout if unset.
func (x *IO) Stdout() io.Writer {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.stdout == nil {
		return os.Stdout
	}
	return x.stdout
}

// Stderr returns the current warning/error output writer, defaulting to
// os.Stderr if unset.
func (x *IO) Stderr() io.Writer {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.stderr == nil {
		return os.Stderr
	}
	return x.stderr
}

// Print writes a formatted informational message to stdout.
func (x *IO) Print(format string, args ...any) {
	x.mu.Lock()
	defer x.mu.Unlock()
	w := x.stdout
	if w == nil {
		w = os.Stdout
	}
	fmt.Fprintf(w, format, args...)
}

// PrintWarning writes a formatted warning message to stderr.
func (x *IO) PrintWarning(format string, args ...any) {
	x.mu.Lock()
	defer x.mu.Unlock()
	w := x.stderr
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, format, args...)
}

// PrintError writes a formatted error/diagnostic message to stderr.
func (x *IO) PrintError(format string, args ...any) {
	x.mu.Lock()
	defer x.mu.Unlock()
	w := x.stderr
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, format, args...)
}

// FlushWriters calls Flush() on each writer that implements interface{ Flush() }.
// Nil writers and non-flushing writers are silently ignored.
func FlushWriters(writers ...io.Writer) {
	type flusher interface{ Flush() }
	for _, w := range writers {
		if f, ok := w.(flusher); ok {
			f.Flush()
		}
	}
}
