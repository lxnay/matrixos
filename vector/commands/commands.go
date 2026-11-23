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

	sigCoordMu            sync.Mutex
	activeGuards          []*SignalGuard
	globalSigCh           chan os.Signal
	globalSigDone         chan struct{}
	globalSigListenerDone chan struct{}
)

type SignalGuard struct {
	mu              sync.Mutex
	cleanupMu       sync.Mutex
	preCleanups     []func()
	cleanups        []func()
	armGen          uint64
	clearGen        uint64
	runningCleanups bool
	armed           bool
	sigCleanupDone  <-chan struct{}
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
	sg.mu.Lock()
	sg.armGen++
	sg.mu.Unlock()
	activeGuards = append(activeGuards, sg)

	if globalSigCh == nil {
		sigCh := make(chan os.Signal, 1)
		done := make(chan struct{})
		listenerDone := make(chan struct{})
		globalSigCh = sigCh
		globalSigDone = done
		globalSigListenerDone = listenerDone

		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		go func(sigCh chan os.Signal, done <-chan struct{}, listenerDone chan struct{}) {
			defer func() {
				sigCoordMu.Lock()
				if globalSigDone == done {
					signal.Stop(sigCh)
					globalSigCh = nil
					globalSigDone = nil
					globalSigListenerDone = nil
				}
				sigCoordMu.Unlock()
				close(listenerDone)
			}()

			select {
			case <-done:
				return
			case sig := <-sigCh:
				cleanupDone := make(chan struct{})
				sigCoordMu.Lock()
				if globalSigDone != done {
					// Disarm() already stopped this listener while a signal was buffered.
					sigCoordMu.Unlock()
					return
				}
				// Detach this exiting listener immediately so any subsequent Arm()
				// spawns a fresh live listener rather than assuming this one is active.
				signal.Stop(sigCh)
				globalSigCh = nil
				globalSigDone = nil
				globalSigListenerDone = nil

				guards := make([]*SignalGuard, len(activeGuards))
				copy(guards, activeGuards)
				for _, g := range guards {
					g.sigCleanupDone = cleanupDone
				}
				sigCoordMu.Unlock()

				func() {
					defer close(cleanupDone)
					fmt.Fprintf(os.Stderr, "\nReceived signal %s, running cleanup ...\n", sig)
					for i := len(guards) - 1; i >= 0; i-- {
						guards[i].runCleanups()
					}
				}()
				signalExit(1)
			}
		}(sigCh, done, listenerDone)
	}
}

// Disarm unregisters this guard from the signal listener and clears its
// cleanup stack.  It is safe to call multiple times.
func (sg *SignalGuard) Disarm() {
	sigCoordMu.Lock()
	wasArmed := sg.armed
	waitSigCleanup := sg.sigCleanupDone
	var myGen uint64
	var waitListener <-chan struct{}
	if wasArmed {
		sg.armed = false
		sg.mu.Lock()
		myGen = sg.armGen
		sg.mu.Unlock()
		for i, g := range activeGuards {
			if g == sg {
				activeGuards = append(activeGuards[:i], activeGuards[i+1:]...)
				break
			}
		}
		if len(activeGuards) == 0 && globalSigCh != nil {
			signal.Stop(globalSigCh)
			close(globalSigDone)
			waitListener = globalSigListenerDone
			globalSigCh = nil
			globalSigDone = nil
			globalSigListenerDone = nil
		}
	}
	sigCoordMu.Unlock()

	if waitListener != nil {
		<-waitListener
	}
	if waitSigCleanup != nil {
		<-waitSigCleanup
	}

	if wasArmed {
		sg.mu.Lock()
		if sg.armGen == myGen {
			if sg.runningCleanups {
				// Defer clearing until runCleanups() completes so calling
				// Disarm() from inside a cleanup does not self-deadlock on
				// cleanupMu, and never wipe cleanups pushed after a re-Arm().
				sg.clearGen = myGen
			} else {
				sg.preCleanups = nil
				sg.cleanups = nil
			}
		}
		sg.mu.Unlock()
	}
}

// PushPreCleanup adds a high-priority cleanup function (such as a context
// cancellation func) that always runs in LIFO order BEFORE regular cleanups
// registered via PushCleanup. This ensures parallel workers stop dequeuing
// new work before resource teardowns run.
func (sg *SignalGuard) PushPreCleanup(fn func()) {
	if fn == nil {
		return
	}
	sg.mu.Lock()
	defer sg.mu.Unlock()
	sg.preCleanups = append(sg.preCleanups, fn)
}

// PushCleanup adds a cleanup function to the top of the LIFO stack.
// Cleanups are executed in reverse order of registration (last-in, first-out)
// exactly like nested bash trap handlers.
func (sg *SignalGuard) PushCleanup(fn func()) {
	if fn == nil {
		return
	}
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

// runCleanups drains and executes all registered cleanups in LIFO order,
// executing preCleanups before regular cleanups on each pass.
// cleanupMu serializes concurrent callers (e.g. a signal handler arriving
// while deferred cleanup is already running) so callers wait for in-flight
// cleanups to finish rather than exiting early.
func (sg *SignalGuard) runCleanups() {
	sg.cleanupMu.Lock()
	defer sg.cleanupMu.Unlock()

	sg.mu.Lock()
	sg.runningCleanups = true
	sg.mu.Unlock()
	defer func() {
		sg.mu.Lock()
		sg.runningCleanups = false
		if sg.clearGen != 0 && sg.clearGen == sg.armGen {
			sg.preCleanups = nil
			sg.cleanups = nil
			sg.clearGen = 0
		}
		sg.mu.Unlock()
	}()

	runBatch := func(fns []func()) {
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

	for {
		sg.mu.Lock()
		if len(sg.preCleanups) > 0 {
			pre := make([]func(), len(sg.preCleanups))
			copy(pre, sg.preCleanups)
			sg.preCleanups = nil
			sg.mu.Unlock()
			runBatch(pre)
			continue
		}
		if len(sg.cleanups) == 0 {
			sg.mu.Unlock()
			return
		}
		fns := make([]func(), len(sg.cleanups))
		copy(fns, sg.cleanups)
		sg.cleanups = nil
		sg.mu.Unlock()
		runBatch(fns)
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
	ui.FlushPrinters()
	ui.printer = ui.NewStdoutWriter(name)
	ui.errPrinter = ui.NewStderrWriter(name)
}

// SetupPlainPrinters creates stdout/stderr writers that show the
// command icon but omit the [name] label.  Use this for short,
// informational commands where a repeated [name] prefix would be
// noisy.
func (ui *UI) SetupPlainPrinters() {
	ui.FlushPrinters()
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

// requireRoot returns an error if the current effective user ID is not root.
func requireRoot() error {
	return requireRootFn(getEuid)
}

// requireRootFn checks root privileges using the provided euid getter.
func requireRootFn(euid func() int) error {
	if euid() != 0 {
		return fmt.Errorf("this command must be run as root")
	}
	return nil
}
