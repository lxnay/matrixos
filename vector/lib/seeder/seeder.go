// Package seeder provides seed detection, locking, and execution utilities
// for managing matrixOS seeders from outside their root filesystem.
package seeder

import (
	"errors"
	"io"
	"os"
	"syscall"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
)

// Compile-time interface check.
var _ ISeeder = (*Seeder)(nil)

// SeederFilterFunc decides whether a seeder should be skipped.
// Returns true to skip, false to include.
type SeederFilterFunc func(name string) bool

// SeederInfo holds the resolved paths for a single discovered seeder.
type SeederInfo struct {
	Name                string   // e.g. "00-bedrock"
	Dir                 string   // absolute directory path
	ChrootExec          string   // path to the chroot executable itself (e.g. chroot)
	ChrootChrootExec    string   // path to the chroot executable inside the chroot
	ChrootChrootArgs    []string // args to run the chroot executable inside the chroot
	PrepperExec         string   // path to the prepper executable
	PostBuildExec       string   // path to the post-build executable (empty if absent)
	PostBuildChrootExec string   // path to the post-build executable inside the chroot
}

// NewSeederOptions contains options for creating a new Seeder.
type NewSeederOptions struct {
	Verbose bool // show detailed output
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// SeedOptions contains options for running a seeder.
type SeedOptions struct {
	ChrootDir   string
	Dir         string
	Info        SeederInfo
	Env         []string
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	SysProcAttr *syscall.SysProcAttr
}

// ISeeder defines the interface for seeder operations.
// Only methods that are called through ISeeder-typed variables (by
// callers, workers, or parallel orchestration) belong here. Pure config
// accessors live on SeederConfig; internal helpers stay on the concrete
// Seeder struct.
type ISeeder interface {
	// --- I/O ---

	// SetStdout replaces the writer used for informational output.
	SetStdout(w io.Writer)
	// SetStderr replaces the writer used for warnings and errors.
	SetStderr(w io.Writer)
	// Stdout returns the current informational output writer.
	Stdout() io.Writer
	// Stderr returns the current warning/error output writer.
	Stderr() io.Writer
	// Print writes a formatted informational message to stdout.
	Print(format string, args ...any)
	// PrintWarning writes a formatted warning message to stderr.
	PrintWarning(format string, args ...any)
	// PrintError writes a formatted error/diagnostic message to stderr.
	PrintError(format string, args ...any)

	// --- Params ---

	// ParseSeederParams resolves the params executable inside
	// info.Dir, sources it in a bash subshell, and extracts the
	// required variables (SEEDER_CHROOT_NAME, etc.).
	ParseSeederParams(info SeederInfo) (*SeederParams, error)

	// --- Done-flag management ---

	// SeederDoneFlagFile computes the done-flag file path for a seeder.
	SeederDoneFlagFile(name, chrootDir string) (string, error)
	// IsSeederDone checks whether the seeder done-flag file exists.
	IsSeederDone(name, chrootDir string) (bool, error)
	// MarkSeederDone creates the done-flag file for the given seeder.
	MarkSeederDone(name, chrootDir string) error

	// --- Lifecycle / operations ---

	// MaybeInitializePrivateRepo ensures the private example repository exists
	// and is built. It clones the repo from PrivateExampleGitRepo if missing,
	// or runs ./make.sh if the .built marker is absent.
	MaybeInitializePrivateRepo() error
	// ImportGentooGpgKeys imports Gentoo release engineering GPG keys.
	ImportGentooGpgKeys() error
	// KillGpgDaemons kills gpg-agent/dirmngr/scdaemon for the seeder GPG homedir.
	KillGpgDaemons()
	// ExecutePrepper runs the prepper script with required env vars.
	ExecutePrepper(
		info SeederInfo, params *SeederParams, opts *PrepperOptions,
	) error
	// Cleanup unmounts all mount points tracked by this Seeder instance
	// in reverse order. It is safe to call multiple times.
	Cleanup()
	// SetupChrootDNS copies /etc/resolv.conf into the chroot.
	SetupChrootDNS(chrootDir string) error
	// SetupChrootDirs creates phase dirs and clones the dev toolkit.
	SetupChrootDirs(chrootDir string) error
	// Seed runs the seeder script inside the chroot.
	Seed(opts *SeedOptions) error
	// PostBuild runs the post-build script inside the chroot.
	// It is called sequentially after all parallel builds complete.
	PostBuild(opts *SeedOptions) error

	// --- Locking ---

	// ExecuteWithSeederLock acquires an exclusive file lock for the given seeder name,
	// executes fn under that lock, and releases the lock when fn returns.
	ExecuteWithSeederLock(name string, fn func() error) error
}

// Seeder provides seed detection and manipulation operations.
type Seeder struct {
	*SeederConfig
	runner       runner.Func
	chrootRunner runner.ChrootRunFunc
	stdin        io.Reader
	stdout       io.Writer
	stderr       io.Writer

	verbose bool
}

// NewSeeder creates a new Seeder instance.
func NewSeeder(cfg config.IConfig, opts *NewSeederOptions) (*Seeder, error) {
	if cfg == nil {
		return nil, errors.New("missing config parameter")
	}
	if opts == nil {
		opts = &NewSeederOptions{}
	}

	stdin := opts.Stdin
	// keep nil.
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	return &Seeder{
		SeederConfig: NewSeederConfig(cfg),
		runner:       runner.Run,
		chrootRunner: runner.ChrootRun,
		stdin:        stdin,
		stdout:       stdout,
		stderr:       stderr,
		verbose:      opts.Verbose,
	}, nil
}

// Cleanup releases any resources held by this Seeder instance.
func (s *Seeder) Cleanup() {}
