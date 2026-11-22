package ostree

import (
	"errors"
	"fmt"
	"os"

	"matrixos/vector/lib/filesystems"
)

// CommitOptions contains the parameters for an ostree commit operation.
type CommitOptions struct {
	// Subject is the commit subject line.
	Subject string
	// Body is inline commit body text. If non-empty a temporary file is
	// created automatically and passed via --body-file (takes precedence
	// over BodyFile).
	Body string
	// BodyFile is the path to a file containing the commit body text.
	// Ignored when Body is set. If both are empty, no --body-file is passed.
	BodyFile string
	// Parent is the parent commit hash (from rev-parse). If empty, no
	// --parent flag is passed.
	Parent string
	// GpgArgs are additional GPG-related arguments (e.g. --gpg-sign=...).
	GpgArgs []string
	// Consume passes --consume to ostree commit, which deletes the source
	// tree after a successful commit.
	Consume bool
	// ImageDir is the path to the directory tree to commit.
	ImageDir string
}

// validate checks that all required fields are populated.
func (o *CommitOptions) validate() error {
	if o.ImageDir == "" {
		return errors.New("missing ImageDir in CommitOptions")
	}
	if !directoryExists(o.ImageDir) {
		return fmt.Errorf("image directory %s does not exist", o.ImageDir)
	}
	if o.BodyFile != "" && !fileExists(o.BodyFile) {
		return fmt.Errorf("body file %s does not exist", o.BodyFile)
	}
	return nil
}

// args builds the ostree commit argument list from the options.
func (o *Ostree) commitArgs(opts *CommitOptions) ([]string, error) {
	ref := o.Ref()
	if ref == "" {
		return nil, errors.New("ostree ref is not set")
	}
	repoDir, err := o.RepoDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get ostree repo dir: %w", err)
	}

	a := []string{"commit"}
	if opts.Consume {
		a = append(a, "--consume")
	}
	a = append(a, "--repo="+repoDir)
	if opts.Parent != "" {
		a = append(a, "--parent="+opts.Parent)
	}
	a = append(a, "--branch="+ref)
	a = append(a, opts.GpgArgs...)
	if opts.Subject != "" {
		a = append(a, "--subject="+opts.Subject)
	}
	if opts.BodyFile != "" {
		a = append(a, "--body-file="+opts.BodyFile)
	}
	a = append(a, opts.ImageDir)
	return a, nil
}

// materializeBody writes opts.Body to a temporary file and sets opts.BodyFile.
// The caller must remove the returned path (if non-empty) when done.
func materializeBody(opts *CommitOptions) (tmpPath string, err error) {
	if opts.Body == "" {
		return "", nil
	}
	f, err := filesystems.CreateTempFile("/tmp", "ostree.commit.body")
	if err != nil {
		return "", fmt.Errorf("failed to create temp body file: %w", err)
	}
	if _, err := f.WriteString(opts.Body); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", fmt.Errorf("failed to write body file: %w", err)
	}
	f.Close()
	opts.BodyFile = f.Name()
	return f.Name(), nil
}

func (o *Ostree) Commit(opts CommitOptions) error {
	ref := o.Ref()
	if ref == "" {
		return errors.New("ostree ref is not set")
	}

	tmp, err := materializeBody(&opts)
	if tmp != "" {
		defer os.Remove(tmp)
	}
	if err != nil {
		return err
	}
	if err := opts.validate(); err != nil {
		return err
	}
	o.Print(
		"Committing ostree rootfs from %s to branch: %s\n",
		opts.ImageDir,
		ref,
	)
	args, err := o.commitArgs(&opts)
	if err != nil {
		return err
	}
	// Serialize GPG-signed commits with gpgMu so ostree's internal GPGME
	// signing never races with concurrent gpg operations. Callers hold
	// LockRepo before Commit, preserving LockRepo -> gpgMu.
	if len(opts.GpgArgs) > 0 {
		gpgMu.Lock()
		defer gpgMu.Unlock()
	}
	return o.ostreeRun(args...)
}
