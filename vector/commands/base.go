package commands

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
)

// Prompter provides interactive input prompting with validation.
// It is designed to be reusable across any command that needs user input.
type Prompter struct {
	Scanner *bufio.Scanner
	Stdout  io.Writer
	Stderr  io.Writer
	UI      *UI
}

// flusher is implemented by writers that buffer partial lines (e.g. styledWriter).
type flusher interface {
	Flush()
}

// inlineFlusher is implemented by writers that can flush without
// appending a trailing newline (for interactive prompts).
type inlineFlusher interface {
	FlushInline()
}

// flushWriter flushes w if it implements the flusher interface.
func flushWriter(w io.Writer) {
	if f, ok := w.(flusher); ok {
		f.Flush()
	}
}

// flushWriterInline flushes w without a trailing newline so the cursor
// stays on the prompt line.  Falls back to a regular Flush if the
// writer does not support inline flushing.
func flushWriterInline(w io.Writer) {
	if f, ok := w.(inlineFlusher); ok {
		f.FlushInline()
		return
	}
	flushWriter(w)
}

// NewPrompter creates a Prompter from a reader and writers.
func NewPrompter(stdin io.Reader, stdout, stderr io.Writer, ui *UI) *Prompter {
	return &Prompter{
		Scanner: bufio.NewScanner(stdin),
		Stdout:  stdout,
		Stderr:  stderr,
		UI:      ui,
	}
}

// AskInput prompts the user for input with a default value and optional
// regex validation.  Returns the user's input or the default value if
// empty input is given (or on EOF).
func (p *Prompter) AskInput(prompt, defaultVal string, pattern *regexp.Regexp) (string, error) {
	for {
		defDisplay := defaultVal
		if defDisplay == "" {
			defDisplay = "none"
		}
		fmt.Fprintf(p.Stdout, "   %s%s%s (default: %s): %s",
			p.UI.cYellow, p.UI.iconQuestion, prompt, defDisplay, p.UI.cReset)
		flushWriterInline(p.Stdout)

		if !p.Scanner.Scan() {
			if err := p.Scanner.Err(); err != nil {
				return "", fmt.Errorf("failed to read input: %w", err)
			}
			// EOF — use default.
			return defaultVal, nil
		}
		input := strings.TrimSpace(p.Scanner.Text())

		if input == "" {
			return defaultVal, nil
		}

		if pattern != nil && !pattern.MatchString(input) {
			fmt.Fprintf(p.Stderr, "   %s%sInvalid input format. Please try again.%s\n",
				p.UI.cRed, p.UI.iconError, p.UI.cReset)
			continue
		}
		return input, nil
	}
}

// AskConfirm displays a prompt and returns true if the user answers
// y/yes (case-insensitive).  Returns false on any other input or EOF.
func (p *Prompter) AskConfirm(prompt string) bool {
	fmt.Fprintf(p.Stdout, "%s", prompt)
	flushWriterInline(p.Stdout)

	if !p.Scanner.Scan() {
		return false
	}
	response := strings.ToLower(strings.TrimSpace(p.Scanner.Text()))
	return response == "y" || response == "yes"
}

type BaseCommand struct {
	cfg config.IConfig
	ot  ostree.IOstree
}

// shortRef returns a short version of the ref for display purposes (e.g. "fcos" for "fcos/36/x86_64").
func (c *BaseCommand) shortRef(ref string) string {
	// remove the remote, get the first char.
	remote := ostree.ExtractRemoteFromRef(ref)
	if remote != "" {
		ref = ostree.CleanRemoteFromRef(ref)
		remote = fmt.Sprintf("%s:", string(remote[0]))
	}

	// for each element /, get the first letter unless it's the last
	// element.
	parts := strings.Split(ref, "/")
	var srefs []string
	for idx, part := range parts {
		if part == "" {
			continue
		}

		if idx == len(parts)-1 {
			srefs = append(srefs, part)
		} else {
			srefs = append(srefs, string(part[0]))
		}
	}
	return remote + strings.Join(srefs, "/")
}

// splitCSV splits a comma-separated string into a trimmed slice of strings.
// Empty input returns nil.
func SplitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// makeNameFilter returns a predicate that returns true for names present in
// the list, or nil when the list is empty.
func makeNameFilter(names []string) func(string) bool {
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]bool, len(names))
	for _, s := range names {
		set[s] = true
	}
	return func(name string) bool { return set[name] }
}

// makeSkipFilter returns a filter function that returns true for names
// present in the skip list.  Returns nil when the list is empty.
func makeSkipFilter(skip []string) func(string) bool {
	return makeNameFilter(skip)
}

// makeOnlyFilter returns a filter function that returns true for names
// present in the allow list.  Returns nil when the list is empty
// (meaning all names pass).
func makeOnlyFilter(only []string) func(string) bool {
	return makeNameFilter(only)
}

// showLocalRefs prints the local ostree refs using printFn.
func showLocalRefs(ot ostree.IOstree, printFn func(string, ...any)) error {
	refs, err := ot.LocalRefs()
	if err != nil {
		return fmt.Errorf("failed to list local refs: %w", err)
	}
	printFn("Local refs:\n")
	for _, r := range refs {
		printFn("  %s\n", r)
	}
	return nil
}

// showRemoteRefs prints the remote ostree refs using printFn.
func showRemoteRefs(ot ostree.IOstree, printFn func(string, ...any)) error {
	refs, err := ot.RemoteRefs()
	if err != nil {
		return fmt.Errorf("failed to list remote refs: %w", err)
	}
	printFn("Remote refs:\n")
	for _, r := range refs {
		printFn("  %s\n", r)
	}
	return nil
}

// configItem retrieves a required non-empty string from c.cfg.
func (c *BaseCommand) configItem(key string) (string, error) {
	return config.GetRequiredItem(c.cfg, key)
}

// initBaseConfig initializes the base configuration for the command.
func (c *BaseCommand) initBaseConfig() error {
	cfg, err := config.NewBaseConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if err := cfg.Load(); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	c.cfg = cfg
	return nil
}

// initClientConfig initializes the client configuration for the command.
func (c *BaseCommand) initClientConfig() error {
	cfg, err := config.NewClientConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if err := cfg.Load(); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	c.cfg = cfg
	return nil
}

// resolveRefRemoteResult holds the result of resolveRefRemote.
type resolveRefRemoteResult struct {
	Ref    string // cleaned ref (remote prefix stripped if present)
	Remote string // resolved remote name
}

func (c *BaseCommand) detectRemotedAndPlainRefs(errf func(format string, args ...any)) error {
	localRefs, err := c.ot.LocalRefs()
	if err != nil {
		errf("WARNING: failed to list local refs: %v. Ignoring as we may have an empty ostree repository.\n", err)
		return nil
	}

	duplicates := make(map[string][]string)
	for _, r := range localRefs {
		ref := ostree.CleanRemoteFromRef(r)
		duplicates[ref] = append(duplicates[ref], r)
	}

	problems := false
	for _, variants := range duplicates {
		if len(variants) == 1 {
			continue
		}
		errf(
			"ERROR: %s exist at the same time in the local repo; this can lead to unexpected behavior.\n",
			strings.Join(variants, ", "))
		errf("Please remove one of these refs to avoid ambiguity using `ostree refs --delete <ref>`.\n")
		problems = true
	}

	if problems {
		return fmt.Errorf("ambiguous refs detected")
	}
	return nil
}

// resolveRefRemote checks whether ref contains a remote prefix
// (e.g. "origin:matrixos/...").  When it does the remote is extracted,
// a warning is emitted to warnf, and the Ostree.Remote config key is
// overridden via an overlay.  The returned result always contains the
// cleaned ref and the resolved remote, regardless of whether the ref
// contained an embedded remote.
func (c *BaseCommand) resolveRefRemote(ref string, warnf func(format string, args ...any)) (*resolveRefRemoteResult, error) {
	remote, err := c.ot.Remote()
	if err != nil {
		return nil, err
	}

	if remoted := ostree.ExtractRemoteFromRef(ref); remoted != "" {
		origRef := ref
		remote = remoted
		ref = ostree.CleanRemoteFromRef(ref)
		warnf(
			"WARNING: %s contains the remote reference, using remote=%s and ref=%s\n",
			origRef, remote, ref)

		overlay := map[string][]string{
			"Ostree.Remote": {remote},
		}
		if err := c.cfg.AddOverlay(overlay); err != nil {
			return nil, fmt.Errorf("failed to add config overlay: %w", err)
		}
	}

	return &resolveRefRemoteResult{Ref: ref, Remote: remote}, nil
}

// initOstree initializes the ostree client for the command.
func (c *BaseCommand) initOstree() error {
	if c.cfg == nil {
		return fmt.Errorf("config not initialized")
	}
	opts := ostree.NewOstreeOptions{
		Config: c.cfg,
	}
	ot, err := ostree.NewOstree(opts)
	if err != nil {
		return fmt.Errorf("failed to initialize ostree: %w", err)
	}
	c.ot = ot
	return nil
}

// initGpg initializes the GPG keychain for the command.
func (c *BaseCommand) initGpg() error {
	if err := c.ot.MaybeInitializeRemote(); err != nil {
		return fmt.Errorf("failed to initialize remote: %w", err)
	}
	if err := c.ot.MaybeInitializeGpg(); err != nil {
		return fmt.Errorf("failed to initialize GPG: %w", err)
	}
	return nil
}

// killGpg kills any GPG daemons spawned for the OSTree GPG homedir.
func (c *BaseCommand) killGpg() {
	if c.ot != nil {
		c.ot.KillGpgDaemons()
	}
}

// truncateOutputFile creates or truncates an output tracking file to empty.
func truncateOutputFile(path string) error {
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		return fmt.Errorf("failed to init %s: %w", path, err)
	}
	return nil
}

// appendLineToFile appends line followed by a newline to an existing output
// tracking file. It is a no-op when path is empty.
func appendLineToFile(path, line string) error {
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, line)
	return err
}

// appendBytesToFile appends data to path, creating it (0644) if absent.
func appendBytesToFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// cmdRunner abstracts an *exec.Cmd for testability across interactive commands.
type cmdRunner interface {
	Run() error
	Output() ([]byte, error)
	SetStdin(r io.Reader)
	SetStdout(w io.Writer)
	SetStderr(w io.Writer)
}

// realCmdRunner wraps a real *exec.Cmd.
type realCmdRunner struct {
	cmd *exec.Cmd
}

func (r *realCmdRunner) Run() error              { return r.cmd.Run() }
func (r *realCmdRunner) Output() ([]byte, error) { return r.cmd.Output() }
func (r *realCmdRunner) SetStdin(rd io.Reader)   { r.cmd.Stdin = rd }
func (r *realCmdRunner) SetStdout(w io.Writer)   { r.cmd.Stdout = w }
func (r *realCmdRunner) SetStderr(w io.Writer)   { r.cmd.Stderr = w }

func newRealCmdRunner(name string, args ...string) cmdRunner {
	return &realCmdRunner{cmd: execCommand(name, args...)}
}

// osRunner holds the common OS-level hooks embedded by interactive command
// runners (jailbreakRunner, setupOSRunner) so they can be mocked in tests.
type osRunner struct {
	execCommand func(name string, args ...string) cmdRunner
	readFile    func(path string) ([]byte, error)
	writeFile   func(path string, data []byte, perm os.FileMode) error
	appendFile  func(path string, data []byte) error
	mkdirAll    func(path string, perm os.FileMode) error
	stat        func(path string) (os.FileInfo, error)
	lstat       func(path string) (os.FileInfo, error)
	removeFile  func(path string) error
	copyFile    func(src, dst string) error
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
}

func defaultOSRunner() osRunner {
	return osRunner{
		execCommand: newRealCmdRunner,
		readFile:    os.ReadFile,
		writeFile:   os.WriteFile,
		appendFile:  appendBytesToFile,
		mkdirAll:    os.MkdirAll,
		stat:        os.Stat,
		lstat:       os.Lstat,
		removeFile:  os.Remove,
		copyFile:    filesystems.CopyFile,
		stdin:       os.Stdin,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
	}
}
