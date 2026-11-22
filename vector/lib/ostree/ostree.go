package ostree

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/runner"
	"os"
	"strings"
)

// Compile-time interface check.
var _ IOstree = (*Ostree)(nil)

// IOstree defines the interface for ostree operations used by callers
// outside package ostree. Internal helpers remain on the concrete *Ostree.
type IOstree interface {

	// CloneForRef creates a new IOstree instance with the same configuration but a different ref.
	CloneForRef(ref string) (IOstree, error)

	// SetStdout sets the stdout writer for the Ostree instance.
	SetStdout(io.Writer)
	// SetStderr sets the stderr writer for the Ostree instance.
	SetStderr(io.Writer)

	// Ref returns the ref for the Ostree instance.
	Ref() string
	// SetRef sets the ref for the Ostree instance.
	SetRef(ref string)

	// IsBranchFullSuffixed checks if the instance ref is a "full" branch.
	IsBranchFullSuffixed() (bool, error)
	// BranchToFull converts a normal branch name to a full one.
	BranchToFull() (string, error)
	// RemoveFullFromBranch removes the "-full" suffix from a branch name.
	RemoveFullFromBranch() (string, error)
	// GpgEnabled returns whether GPG signing and verification is enabled.
	GpgEnabled() (bool, error)
	// GpgPrivateKeyPath returns the user defined private/ placed GPG private key path.
	GpgPrivateKeyPath() (string, error)
	// OsName returns the name of the OS as defined in the config.
	OsName() (string, error)
	// FancyOsName returns the fancy (display) name of the OS as defined in the config.
	FancyOsName() (string, error)
	// Arch returns the build architecture as defined in the config.
	Arch() (string, error)
	// RepoDir returns the path to the ostree repository.
	RepoDir() (string, error)
	// Sysroot returns the path to the ostree sysroot directory. Usually /sysroot.
	Sysroot() (string, error)
	// Root returns the path to the root filesystem directory used as root for
	// ostree operations (i.e. --sysroot).
	Root() (string, error)
	// Remote returns the name of the remote.
	Remote() (string, error)
	// GpgBestPubKeyPath returns the path to the GPG public key to use.
	// It prefers the private key path over the official one.
	GpgBestPubKeyPath() (string, error)
	// GpgArgs returns the gpg arguments for ostree commands.
	GpgArgs() ([]string, error)
	// SetGpg enables or disables GPG verification in the local ostree repository.
	SetGpg(enabled bool) error

	// PrepareFilesystemHierarchy prepares the filesystem hierarchy for OSTree.
	PrepareFilesystemHierarchy(rootfs string) error
	// ValidateFilesystemHierarchy validates the filesystem hierarchy for OSTree.
	ValidateFilesystemHierarchy(rootfs string) error

	// SetVerbose sets the verbose flag for the Ostree instance.
	SetVerbose(bool)

	// Commit performs an ostree commit using the instance runner.
	Commit(opts CommitOptions) error
	// InitRepo initialises the local ostree repository in archive mode.
	InitRepo() error
	// BootCommit returns the boot commit from an ostree sysroot.
	BootCommit(sysroot string) (string, error)
	// LastCommit returns the last commit for the instance ref.
	LastCommit() (string, error)
	// GpgSignFile signs a file with GPG.
	GpgSignFile(file string) error
	// InitializeSigningGpg imports GPG keys into the local GPG keyring.
	InitializeSigningGpg() error
	// MaybeInitializeGpg initializes GPG keys for an ostree repository.
	MaybeInitializeGpg() error
	// KillGpgDaemons kills gpg-agent/dirmngr/scdaemon for the OSTree GPG homedir.
	KillGpgDaemons()
	// MaybeInitializeRemote initializes an ostree remote.
	MaybeInitializeRemote() error
	// Pull pulls the instance ref from its configured remote.
	Pull() error
	// Prune prunes the ostree repo for the instance ref.
	Prune() error
	// GenerateStaticDelta generates a static delta for an ostree repository.
	GenerateStaticDelta() error
	// UpdateSummary updates the summary of an ostree repository.
	UpdateSummary() error
	// AddRemote adds a remote to an ostree repo.
	AddRemote() error
	// AddRemoteToRootfs adds a remote to an ostree rootfs.
	AddRemoteToRootfs(rootfs string) error
	// LocalRefs lists the locally available ostree refs.
	LocalRefs() ([]string, error)
	// RemoteRefs lists the remote available ostree refs.
	RemoteRefs() ([]string, error)
	// ListDeployments lists the deployments in the / filesystem.
	ListDeployments() ([]Deployment, error)
	// DeployedRootfs returns the path to the deployed rootfs.
	DeployedRootfs() (string, error)
	// BootedRef returns the ref of the booted deployment.
	BootedRef() (string, error)
	// Switch runs `ostree admin switch` to switch to the instance ref.
	Switch() error
	// PostCopy runs `ostree admin post-copy` to perform post-copy operations
	// (e.g. after copying an OS image to a new disk).
	PostCopy() error
	// Deploy deploys an ostree commit.
	Deploy(sysroot string, bootArgs []string) error
	// Upgrade runs `ostree admin upgrade`.
	Upgrade(args []string) error
	// Pin pins a deployment by index so that it can be persisted as rollback target.
	Pin(targetIndex int) error
	// Unpin removes a previously pinned deployment.
	Unpin(targetIndex int) error
	// ListPackages lists the packages in a commit.
	ListPackages(commit string) ([]string, error)
	// ListEtcChanges performs a 3-way diff between the old pristine /usr/etc,
	// the new pristine /usr/etc, and the user's live /etc, and returns a list of
	// changes with their classification (add/update/remove/conflict/user-only).
	ListEtcChanges(aCommit, bCommit string) ([]EtcChange, error)
	// Readwrite unlocks the filesystem for writing using `ostree admin unlock`.
	// If permanent is true, it uses --hotfix (persistent across reboots).
	// If permanent is false, it uses --transient (lost on reboot).
	Readwrite(permanent bool) error
}

// runCommand runs a generic binary with args and stdout/stderr handling.
var runCommand runner.Func = runner.Run

// newOstreeCmd creates a runner.Cmd for the "ostree" binary with the given
// stdout/stderr writers and arguments.
func newOstreeCmd(stdout, stderr io.Writer, args ...string) *runner.Cmd {
	return &runner.Cmd{
		Name:   "ostree",
		Args:   args,
		Stdout: stdout,
		Stderr: stderr,
	}
}

func readerToList(reader io.Reader) ([]string, error) {
	var elements []string
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		elements = append(elements, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return elements, nil
}

func readerToFirstNonEmptyLine(reader io.Reader) (string, error) {
	lines, err := readerToList(reader)
	if err != nil || len(lines) == 0 {
		return "", err
	}
	return lines[0], nil
}

// SetupEnvironment sets the LC_TIME environment variable to "C".
// This is to ensure that Cloudflare can correctly process requests
// without throwing HTTP 400 errors.
func SetupEnvironment() {
	os.Setenv("LC_TIME", "C")
}

type Ostree struct {
	cfg     config.IConfig
	stdout  io.Writer
	stderr  io.Writer
	runner  runner.Func
	verbose bool
	ref     string
}

// NewOstreeOptions holds configuration for NewOstree.
type NewOstreeOptions struct {
	Config  config.IConfig
	Stdout  io.Writer
	Stderr  io.Writer
	Verbose bool
	Ref     string
}

// NewOstree creates a new Ostree instance.
func NewOstree(opts NewOstreeOptions) (*Ostree, error) {
	if opts.Config == nil {
		return nil, errors.New("missing config parameter")
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	return &Ostree{
		cfg:     opts.Config,
		stdout:  stdout,
		stderr:  stderr,
		runner:  runCommand,
		verbose: opts.Verbose,
		ref:     opts.Ref,
	}, nil
}

func (o *Ostree) CloneForRef(ref string) (IOstree, error) {
	return NewOstree(NewOstreeOptions{
		Config:  o.cfg,
		Stdout:  o.stdout,
		Stderr:  o.stderr,
		Verbose: o.verbose,
		Ref:     ref,
	})
}

func (o *Ostree) SetStdout(w io.Writer) {
	o.stdout = w
}

func (o *Ostree) SetStderr(w io.Writer) {
	o.stderr = w
}

func (o *Ostree) Print(format string, a ...any) {
	fmt.Fprintf(o.stdout, format, a...)
}

func (o *Ostree) PrintError(format string, a ...any) {
	fmt.Fprintf(o.stderr, format, a...)
}

func (o *Ostree) SetVerbose(v bool) {
	o.verbose = v
}

func (o *Ostree) Ref() string {
	return o.ref
}

func (o *Ostree) SetRef(ref string) {
	o.ref = ref
}

// runCmd runs a command via the instance's command runner, optionally adding
// --verbose and the "ostree" binary name automatically.
func (o *Ostree) runCmd(stdout, stderr io.Writer, verbose bool, args ...string) error {
	var finalArgs []string
	if verbose {
		finalArgs = append(finalArgs, "--verbose")
		o.PrintError(">> Executing: ostree --verbose %s\n", strings.Join(args, " "))
	}
	finalArgs = append(finalArgs, args...)
	return o.runner(newOstreeCmd(stdout, stderr, finalArgs...))
}

// ostreeRun runs an ostree command with stdout/stderr directed to the instance's stdout/stderr.
func (o *Ostree) ostreeRun(args ...string) error {
	return o.runCmd(o.stdout, o.stderr, o.verbose, args...)
}

// ostreeRunCapture runs an ostree command and captures its stdout.
func (o *Ostree) ostreeRunCapture(args ...string) (io.Reader, error) {
	if o.verbose {
		o.PrintError(">> Executing: ostree (stdout capture) %s\n", strings.Join(args, " "))
	}
	stdo := new(bytes.Buffer)
	err := o.runCmd(stdo, o.stderr, false, args...)
	return stdo, err
}

var pathExists = filesystems.PathExists
var fileExists = filesystems.FileExists
var directoryExists = filesystems.DirectoryExists
