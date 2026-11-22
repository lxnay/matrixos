package releaser

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
	"matrixos/vector/lib/validation"
)

// Compile-time interface check.
var _ IRelease = (*Releaser)(nil)

// ReleaseStage represents a release stage (dev or prod).
type ReleaseStage string

const (
	StageDev  ReleaseStage = "dev"
	StageProd ReleaseStage = "prod"
)

// ValidateReleaseStage validates a release stage string.
func ValidateReleaseStage(stage string) (ReleaseStage, error) {
	switch ReleaseStage(stage) {
	case StageDev, StageProd:
		return ReleaseStage(stage), nil
	default:
		return "", fmt.Errorf("unknown release stage: %s", stage)
	}
}

// CommitOptions controls how an ostree commit is performed.
type CommitOptions struct {
	Branch       string // ostree branch to commit to
	ParentBranch string // parent branch (empty for root branches)
	ParentRev    string // parent commit hash (from rev-parse).
	Consume      bool   // --consume flag for ostree commit
}

// RefFilterFunc decides whether a ref should be skipped.
// Returns true to skip, false to include.
type RefFilterFunc func(ref string) bool

// NewReleaserOptions contains options for creating a new Releaser.
type NewReleaserOptions struct {
	ChrootDir string // source chroot directory
	ImageDir  string // destination image directory
	Ref       string // ostree ref (branch) to operate on
	Verbose   bool   // show detailed output
}

// IRelease defines the interface for release operations.
// It contains only the methods that callers outside this package use
// through an interface-typed variable (e.g. commands.ReleaseCommand).
// All remaining public methods live on *Releaser directly.
type IRelease interface {
	// SetStdout replaces the writer used for informational output.
	SetStdout(w io.Writer)
	// SetStderr replaces the writer used for warnings and errors.
	SetStderr(w io.Writer)
	// Print writes a formatted informational message to stdout.
	Print(format string, args ...any)
	// ExecuteWithReleaseLock acquires an exclusive file lock for the given .Ref,
	// executes fn under that lock, and releases the lock when fn returns.
	ExecuteWithReleaseLock(fn func() error) error
	// Build runs the full release pipeline for a single branch.
	Build() error
	// Cleanup unmounts all mount points tracked by this Releaser instance
	// in reverse order. It is safe to call multiple times.
	Cleanup()
}

// Releaser provides release creation and manipulation operations.
type Releaser struct {
	*ReleaserConfig
	ostree       ostree.IOstree
	runner       runner.Func
	chrootRunner runner.ChrootRunFunc
	ioMu         sync.Mutex
	stdout       io.Writer
	stderr       io.Writer

	chrootDir string // source chroot directory
	imageDir  string // destination image directory
	ref       string // ostree ref (branch) to operate on
	verbose   bool   // show detailed output

	// QA validation instance.
	qa *validation.QA

	// trackedMounts records every mount point created by this Releaser
	// so that Cleanup can attempt to unmount them all on failure or signal.
	trackedMountsMu sync.Mutex
	trackedMounts   []string
}

// NewReleaser creates a new Releaser instance.
func NewReleaser(cfg config.IConfig, ot ostree.IOstree, opts *NewReleaserOptions) (*Releaser, error) {
	if cfg == nil {
		return nil, errors.New("missing config parameter")
	}
	if ot == nil {
		return nil, errors.New("missing ostree parameter")
	}
	if opts == nil {
		return nil, errors.New("missing options parameter")
	}
	if opts.Ref == "" {
		return nil, errors.New("missing Ref in options")
	}

	qa, err := validation.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize QA: %w", err)
	}

	return &Releaser{
		ReleaserConfig: NewReleaserConfig(cfg),
		ostree:         ot,
		runner:         runner.Run,
		chrootRunner:   runner.ChrootRun,
		qa:             qa,
		stdout:         os.Stdout,
		stderr:         os.Stderr,
		chrootDir:      opts.ChrootDir,
		imageDir:       opts.ImageDir,
		ref:            opts.Ref,
		verbose:        opts.Verbose,
	}, nil
}

// Build runs the full release pipeline for a single branch.
// The full branch name (with -full suffix) is computed internally via
// IOstree.BranchToFull.  The regular branch used for the second commit
// is taken from r.ref.
func (r *Releaser) Build() error {
	// Compute the full branch name (with -full suffix).
	fullBranch, err := r.ostree.BranchToFull()
	if err != nil {
		return fmt.Errorf("failed to compute full branch name: %w", err)
	}

	setRefs := func(ref string) {
		r.SetRef(ref)
		r.ostree.SetRef(ref)
		r.Print("Switched to ref: %s\n", ref)
	}

	r.Print("Switching to full branch %s for release build\n", fullBranch)
	originalRef := r.ref
	setRefs(fullBranch)
	// If we fail, reset the values to their original state.
	defer setRefs(originalRef)

	// Verify releaser environment.
	if err := r.qa.VerifyReleaserEnvironmentSetup("/"); err != nil {
		return fmt.Errorf("environment verification failed: %w", err)
	}

	// Pre-release operations.
	if err := r.CheckMatrixOS(); err != nil {
		return fmt.Errorf("matrixOS check failed: %w", err)
	}
	if err := r.SyncFilesystem(); err != nil {
		return fmt.Errorf("filesystem sync failed: %w", err)
	}
	if err := r.PreCleanQAChecks(); err != nil {
		return fmt.Errorf("pre-clean QA checks failed: %w", err)
	}
	if err := r.CleanRootfs(); err != nil {
		return fmt.Errorf("rootfs clean failed: %w", err)
	}
	if err := r.SetupServices(); err != nil {
		return fmt.Errorf("services setup failed: %w", err)
	}
	if err := r.SetupHostname(); err != nil {
		return fmt.Errorf("hostname setup failed: %w", err)
	}

	// Initialize GPG for signing.
	if err := r.ostree.InitializeSigningGpg(); err != nil {
		return fmt.Errorf("GPG signing initialization failed: %w", err)
	}

	// Release hook and ostree preparation.
	if err := r.ReleaseHook(); err != nil {
		return fmt.Errorf("release hook failed: %w", err)
	}
	if err := r.OstreePrepare(); err != nil {
		return fmt.Errorf("ostree preparation failed: %w", err)
	}
	if err := r.MaybeOstreeInit(); err != nil {
		return fmt.Errorf("ostree init failed: %w", err)
	}

	// --- First commit: full branch (no consume) ---
	if err := r.Release(CommitOptions{
		Branch:  fullBranch,
		Consume: false,
	}); err != nil {
		return fmt.Errorf("full branch release failed: %w", err)
	}

	// Re-link /etc and fix portage for post-clean shrink (uses emerge).
	if err := r.SymlinkEtc(); err != nil {
		return fmt.Errorf("symlink /etc failed: %w", err)
	}
	if err := r.AddExtraDotDotToUsrEtcPortage(); err != nil {
		return fmt.Errorf("add extra ../ to /usr/etc/portage failed: %w", err)
	}

	// Remove dev artifacts to produce the smaller branch.
	if err := r.PostCleanShrink(); err != nil {
		return fmt.Errorf("post-clean shrink failed: %w", err)
	}

	// Restore portage symlink for client-side deployment.
	if err := r.RemoveExtraDotDotFromUsrEtcPortage(); err != nil {
		return fmt.Errorf("remove extra ../ from /usr/etc/portage failed: %w", err)
	}

	// --- Second commit: regular branch (consume, parent=full) ---
	if err := r.UnlinkEtc(); err != nil {
		return fmt.Errorf("unlink /etc (second commit) failed: %w", err)
	}

	// Resolve parent commit for fullBranch that we just committed via Release().
	if fullBranch != r.ostree.Ref() {
		return fmt.Errorf(
			"unexpected ostree ref after full commit: got %s, want %s",
			r.ostree.Ref(),
			fullBranch,
		)
	}
	parentRev, err := r.ostree.LastCommit()
	if err != nil {
		return fmt.Errorf("unable to run ostree rev-parse for parent branch: %w", err)
	}

	r.Print(
		"Parent commit (last commit of: %s) for second commit: %s\n",
		fullBranch, parentRev,
	)

	r.Print("Switching back to normal branch %s for second commit\n", originalRef)
	setRefs(originalRef)

	if err := r.Release(CommitOptions{
		Branch:       r.ref,
		ParentBranch: fullBranch,
		ParentRev:    parentRev,
		Consume:      true,
	}); err != nil {
		return fmt.Errorf("branch release failed: %w", err)
	}

	return nil
}

// checkImageDir validates that imageDir is a non-empty existing directory.
func checkImageDir(imageDir string) error {
	if imageDir == "" {
		return errors.New("imageDir is empty")
	}
	if !filesystems.DirectoryExists(imageDir) {
		return errors.New("imageDir not found")
	}
	return nil
}

// checkChrootDir validates that chrootDir is a non-empty existing directory.
func checkChrootDir(chrootDir string) error {
	if chrootDir == "" {
		return errors.New("chrootDir is empty")
	}
	if !filesystems.DirectoryExists(chrootDir) {
		return errors.New("chrootDir not found")
	}
	return nil
}
