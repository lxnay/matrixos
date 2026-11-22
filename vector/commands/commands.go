package commands

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// ICommand defines the interface for a vector command
type ICommand interface {
	// Name returns the name of the command.
	Name() string
	// Init initializes the command with the given arguments.
	Init(args []string) error
	// Run executes the command.
	Run() error
}

// SignalGuard manages a LIFO stack of cleanup functions that are executed
// on process termination signals (SIGINT, SIGTERM) or when RunWithGuard
// catches a panic.
//
// Embed it in any command struct and call Arm() + PushCleanup() to register
// cleanup work.  Disarm() stops the signal listener (idempotent).
//
// Usage:
//
//	func (c *MyCommand) Run() error {
//	    c.Arm()
//	    defer c.Disarm()
//
//	    c.PushCleanup(func() { unmountAll() })
//	    c.PushCleanup(func() { removeTemp() })
//	    ...
//	}
//
// Or use RunWithGuard for automatic panic recovery:
//
//	func (c *MyCommand) Run() error {
//	    return c.RunWithGuard(func() error {
//	        c.PushCleanup(func() { unmountAll() })
//	        ...
//	    })
//	}
var (
	// signalExit is the exit function called by SignalGuard after running
	// cleanups on SIGINT/SIGTERM. Overridden in tests.
	signalExit = os.Exit

	sigCoordMu    sync.Mutex
	activeGuards  []*SignalGuard
	globalSigCh   chan os.Signal
	globalSigDone chan struct{}
)

type SignalGuard struct {
	mu        sync.Mutex
	cleanupMu sync.Mutex
	cleanups  []func()
	armed     bool
}

// runAllActiveCleanups executes cleanups for all currently armed SignalGuards
// in LIFO order (innermost nested command first, outermost parent last).
func runAllActiveCleanups() {
	sigCoordMu.Lock()
	guards := make([]*SignalGuard, len(activeGuards))
	copy(guards, activeGuards)
	sigCoordMu.Unlock()

	for i := len(guards) - 1; i >= 0; i-- {
		guards[i].runCleanups()
	}
}

// Arm registers this guard with the process-wide SIGINT/SIGTERM listener.
// On receipt all armed guards' cleanup stacks are executed in LIFO order
// before the process exits with code 1.
// Arm is idempotent — calling it on an already-armed guard is a no-op.
func (sg *SignalGuard) Arm() {
	sigCoordMu.Lock()
	defer sigCoordMu.Unlock()

	if sg.armed {
		return
	}
	sg.armed = true
	activeGuards = append(activeGuards, sg)

	if len(activeGuards) == 1 {
		sigCh := make(chan os.Signal, 1)
		done := make(chan struct{})
		globalSigCh = sigCh
		globalSigDone = done

		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		go func(sigCh <-chan os.Signal, done <-chan struct{}) {
			select {
			case sig := <-sigCh:
				fmt.Fprintf(os.Stderr, "\nReceived signal %s, running cleanup ...\n", sig)
				runAllActiveCleanups()
				signalExit(1)
			case <-done:
				return
			}
		}(sigCh, done)
	}
}

// Disarm unregisters this guard from the signal listener and clears its
// cleanup stack.  It is safe to call multiple times.
func (sg *SignalGuard) Disarm() {
	sigCoordMu.Lock()
	wasArmed := sg.armed
	if wasArmed {
		sg.armed = false
		for i, g := range activeGuards {
			if g == sg {
				activeGuards = append(activeGuards[:i], activeGuards[i+1:]...)
				break
			}
		}
		if len(activeGuards) == 0 && globalSigCh != nil {
			signal.Stop(globalSigCh)
			close(globalSigDone)
			globalSigCh = nil
			globalSigDone = nil
		}
	}
	sigCoordMu.Unlock()

	if wasArmed {
		sg.mu.Lock()
		sg.cleanups = nil
		sg.mu.Unlock()
	}
}

// PushCleanup adds a cleanup function to the top of the LIFO stack.
// Cleanups are executed in reverse order of registration (last-in, first-out)
// exactly like nested bash trap handlers.
func (sg *SignalGuard) PushCleanup(fn func()) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	sg.cleanups = append(sg.cleanups, fn)
}

// RunCleanups executes all registered cleanup functions in LIFO order
// and clears the stack.  It can be called explicitly (e.g. from defer)
// in addition to being invoked automatically by the signal handler.
// Each cleanup runs in its own recovery block so a panicking cleanup
// does not prevent the remaining ones from executing.
func (sg *SignalGuard) RunCleanups() {
	sg.runCleanups()
}

// runCleanups drains and executes all registered cleanups in LIFO order.
// cleanupMu serializes concurrent callers (e.g. a signal handler arriving
// while deferred cleanup is already running) so callers wait for in-flight
// cleanups to finish rather than exiting early.
func (sg *SignalGuard) runCleanups() {
	sg.cleanupMu.Lock()
	defer sg.cleanupMu.Unlock()

	sg.mu.Lock()
	fns := make([]func(), len(sg.cleanups))
	copy(fns, sg.cleanups)
	sg.cleanups = nil
	sg.mu.Unlock()

	// Execute in LIFO order.
	for i := len(fns) - 1; i >= 0; i-- {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "panic during cleanup: %v\n", r)
				}
			}()
			fns[i]()
		}()
	}
}

// RunWithGuard is a convenience wrapper that arms the guard, runs fn,
// ensures cleanups run on return (normal or panic), and then disarms.
func (sg *SignalGuard) RunWithGuard(fn func() error) (retErr error) {
	sg.Arm()

	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic caught by SignalGuard: %v\n", r)
			retErr = fmt.Errorf("panic: %v", r)
		}
		sg.runCleanups()
		sg.Disarm()
	}()

	return fn()
}

// UI provides common UI styles and icons for commands
type UI struct {
	// UI Styles
	cReset, cRed, cGreen, cYellow, cBlue string
	cMagenta, cCyan, cWhite, cBold       string

	// UI Icons
	iconSearch, iconDownload, iconCheck         string
	iconUpdate, iconPackage                     string
	iconQuestion, iconRocket, iconGear, iconDoc string
	iconNew, iconError, iconWarn                string
	separator                                   string

	// TTY detection result (set by StartUI)
	isTTY bool

	// Per-command styled printers initialised by SetupPrinters.
	printer    *styledWriter
	errPrinter *styledWriter
}

// IsTTY returns whether stdout was detected as a terminal.
func (ui *UI) IsTTY() bool {
	return ui.isTTY
}

// NewStdoutWriter creates a styled writer for os.Stdout using the UI
// theme.  The returned writer can be passed to sub-processes or
// libraries that need a prefixed, TTY-aware io.Writer.
func (ui *UI) NewStdoutWriter(name string) *styledWriter {
	prefix := fmt.Sprintf("%s%s%s[%s]%s", ui.cBold, ui.cGreen, ui.iconGear, name, ui.cReset)
	return newStyledWriter(os.Stdout, prefix, ui.cGreen, ui.cReset, ui.isTTY)
}

// NewStderrWriter creates a styled writer for os.Stderr using the UI
// theme.  See NewStdoutWriter for details.
func (ui *UI) NewStderrWriter(name string) *styledWriter {
	prefix := fmt.Sprintf("%s%s%s[%s]%s", ui.cBold, ui.cRed, ui.iconWarn, name, ui.cReset)
	return newStyledWriter(os.Stderr, prefix, ui.cYellow, ui.cReset, ui.isTTY)
}

// SetupPrinters creates the default stdout/stderr styled writers for
// the given command name.  After calling this, Println / Printf /
// PrintErr / PrintErrf write through these writers.
func (ui *UI) SetupPrinters(name string) {
	ui.printer = ui.NewStdoutWriter(name)
	ui.errPrinter = ui.NewStderrWriter(name)
}

// SetupPlainPrinters creates stdout/stderr writers that show the
// command icon but omit the [name] label.  Use this for short,
// informational commands where a repeated [name] prefix would be
// noisy.
func (ui *UI) SetupPlainPrinters() {
	prefix := fmt.Sprintf("%s%s%s%s", ui.cBold, ui.cGreen, ui.iconGear, ui.cReset)
	ui.printer = newStyledWriter(os.Stdout, prefix, "", "", ui.isTTY)
	errPrefix := fmt.Sprintf("%s%s%s%s", ui.cBold, ui.cRed, ui.iconWarn, ui.cReset)
	ui.errPrinter = newStyledWriter(os.Stderr, errPrefix, "", "", ui.isTTY)
}

// StdoutWriter returns the current stdout styled writer, or nil if
// SetupPrinters has not been called.
func (ui *UI) StdoutWriter() *styledWriter {
	return ui.printer
}

// StderrWriter returns the current stderr styled writer, or nil if
// SetupPrinters has not been called.
func (ui *UI) StderrWriter() *styledWriter {
	return ui.errPrinter
}

// SetLogWriter sets a secondary log writer on both the stdout and
// stderr styled writers so that ANSI-stripped output is teed to it.
func (ui *UI) SetLogWriter(w io.Writer) {
	if ui.printer != nil {
		ui.printer.SetLogWriter(w)
	}
	if ui.errPrinter != nil {
		ui.errPrinter.SetLogWriter(w)
	}
}

// Println prints a styled line to the command's stdout writer.
// If SetupPrinters has not been called it falls back to fmt.Println.
func (ui *UI) Println(args ...interface{}) {
	if ui.printer == nil {
		fmt.Println(args...)
		return
	}
	fmt.Fprintln(ui.printer, fmt.Sprint(args...))
}

// Printf prints a styled formatted message to the command's stdout
// writer.  Include a trailing \n if you want the line flushed
// immediately.
func (ui *UI) Printf(format string, args ...interface{}) {
	if ui.printer == nil {
		fmt.Printf(format, args...)
		return
	}
	fmt.Fprintf(ui.printer, format, args...)
}

// PrintErr prints a styled line to the command's stderr writer.
func (ui *UI) PrintErr(args ...interface{}) {
	if ui.errPrinter == nil {
		fmt.Fprintln(os.Stderr, args...)
		return
	}
	fmt.Fprintln(ui.errPrinter, fmt.Sprint(args...))
}

// PrintErrf prints a styled formatted message to the command's stderr writer.
func (ui *UI) PrintErrf(format string, args ...interface{}) {
	if ui.errPrinter == nil {
		fmt.Fprintf(os.Stderr, format, args...)
		return
	}
	fmt.Fprintf(ui.errPrinter, format, args...)
}

// FlushPrinters flushes any buffered content in both styled writers.
func (ui *UI) FlushPrinters() {
	if ui.printer != nil {
		ui.printer.Flush()
	}
	if ui.errPrinter != nil {
		ui.errPrinter.Flush()
	}
}

// StartUI initializes the UI component with environment detection
func (ui *UI) StartUI() {
	useColor := false
	useEmoji := false

	// Check if stdout is a terminal
	_, err := unix.IoctlGetTermios(int(os.Stdout.Fd()), unix.TCGETS)
	isTerm := err == nil
	ui.isTTY = isTerm

	if isTerm {
		termEnv := os.Getenv("TERM")
		if termEnv != "dumb" {
			useColor = true
		}
		// Linux console has limited font support
		if termEnv != "linux" {
			useEmoji = true
		}
	}

	if useColor {
		ui.cReset = "\033[0m"
		ui.cRed = "\033[31m"
		ui.cGreen = "\033[32m"
		ui.cYellow = "\033[33m"
		ui.cBlue = "\033[34m"
		ui.cMagenta = "\033[35m"
		ui.cCyan = "\033[36m"
		ui.cWhite = "\033[37m"
		ui.cBold = "\033[1m"
	}

	if useEmoji {
		ui.iconSearch = "○ "
		ui.iconDownload = "⇩ "
		ui.iconCheck = "✔ "
		ui.iconUpdate = "↻ "
		ui.iconPackage = "▤ "
		ui.iconQuestion = "? "
		ui.iconRocket = "➤ "
		ui.iconGear = "⚙ "
		ui.iconDoc = "≡ "
		ui.iconNew = "★ "
		ui.iconError = "✖ "
		ui.iconWarn = "⚠ "
		ui.separator = "   ───────────────────────────────────────────────────"
	} else {
		ui.iconSearch = "[?] "
		ui.iconDownload = "[v] "
		ui.iconCheck = "[OK] "
		ui.iconUpdate = "[~] "
		ui.iconPackage = "[#] "
		ui.iconQuestion = "[?] "
		ui.iconRocket = "[>] "
		ui.iconGear = "[*] "
		ui.iconDoc = "[f] "
		ui.iconNew = "[+] "
		ui.iconError = "[X] "
		ui.iconWarn = "[!] "
		ui.separator = "   ---------------------------------------------------"
	}
}

var execCommand = exec.Command
var getEuid = os.Geteuid
