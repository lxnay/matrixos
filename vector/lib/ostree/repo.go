package ostree

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
)

type AddRemoteOptions struct {
	Remote    string
	RemoteURL string
	GpgArgs   []string
	RepoDir   string
	Sysroot   string
}

func (o *Ostree) InitRepo() error {
	repoDir, err := o.RepoDir()
	if err != nil {
		return err
	}

	if err := o.ostreeRun("--repo="+repoDir, "init", "--mode=archive"); err != nil {
		return err
	}

	// Ensure concurrent writers block rather than fail-fast when the repo lock
	// is held by another process (e.g. parallel branch releases).
	// Use "--" to prevent "-1" from being parsed as an option flag.
	return o.ostreeRun("--repo="+repoDir, "config", "set", "--", "core.lock-timeout-secs", "-1")
}


// LastCommit returns the commit hash of the latest commit in the given ref.
func LastCommit(repoDir, ref string, verbose bool) (string, error) {
	if repoDir == "" {
		return "", errors.New("invalid repoDir parameter")
	}
	if ref == "" {
		return "", errors.New("invalid ref parameter")
	}

	stdout, err := RunWithStdoutCapture(
		verbose,
		"rev-parse",
		"--repo="+repoDir,
		ref,
	)
	if err != nil {
		return "", err
	}
	lines, err := readerToList(stdout)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("no commit found for ref %s", ref)
	}
	return lines[0], nil
}

// listRemotesFromRepo lists remotes using the instance runner.
func (o *Ostree) listRemotesFromRepo(repoDir string) ([]string, error) {
	if repoDir == "" {
		return nil, errors.New("invalid repoDir parameter")
	}
	stdout, err := o.ostreeRunCapture("--repo="+repoDir, "remote", "list")
	if err != nil {
		return nil, err
	}
	return readerToList(stdout)
}

// lastCommitFromRepo returns the last commit for a ref using the instance runner.
func (o *Ostree) lastCommitFromRepo(repoDir, ref string) (string, error) {
	if repoDir == "" {
		return "", errors.New("invalid repoDir parameter")
	}
	if ref == "" {
		return "", errors.New("invalid ref parameter")
	}
	stdout, err := o.ostreeRunCapture("rev-parse", "--repo="+repoDir, ref)
	if err != nil {
		return "", err
	}
	lines, err := readerToList(stdout)
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("no commit found for ref %s", ref)
	}
	return lines[0], nil
}

// listLocalRefsFromRepo lists local refs using the instance runner.
func (o *Ostree) listLocalRefsFromRepo(repoDir string) ([]string, error) {
	if repoDir == "" {
		return nil, errors.New("invalid repoDir parameter")
	}
	stdout, err := o.ostreeRunCapture("--repo="+repoDir, "refs")
	if err != nil {
		return nil, err
	}
	refs, err := readerToList(stdout)
	if err != nil {
		return nil, err
	}
	// remove ostree-metadata from the list.
	return slices.DeleteFunc(refs, func(ref string) bool {
		return ref == "ostree-metadata"
	}), nil
}

// listRemoteRefsFromRepo lists remote refs using the instance runner.
func (o *Ostree) listRemoteRefsFromRepo(repoDir, remote string) ([]string, error) {
	if repoDir == "" {
		return nil, errors.New("invalid repoDir parameter")
	}
	if remote == "" {
		return nil, errors.New("invalid remote parameter")
	}
	stdout, err := o.ostreeRunCapture("--repo="+repoDir, "remote", "refs", remote)
	if err != nil {
		return nil, err
	}
	return readerToList(stdout)
}

// addRemote adds a remote using the instance runner.
func (o *Ostree) addRemote(opts AddRemoteOptions) error {
	if opts.Remote == "" {
		return errors.New("invalid Remote parameter")
	}
	if opts.RemoteURL == "" {
		return errors.New("invalid RemoteURL parameter")
	}
	if opts.RepoDir != "" && !directoryExists(opts.RepoDir) {
		return fmt.Errorf("repoDir %s does not exist", opts.RepoDir)
	}
	if opts.Sysroot != "" && !directoryExists(opts.Sysroot) {
		return fmt.Errorf("sysroot %s does not exist", opts.Sysroot)
	}
	args := []string{"remote", "add"}
	if opts.Sysroot != "" {
		args = append(args, "--sysroot="+opts.Sysroot)
	}
	if opts.RepoDir != "" {
		args = append(args, "--repo="+opts.RepoDir)
	}
	args = append(args, "--force")
	args = append(args, opts.GpgArgs...)
	args = append(args, opts.Remote, opts.RemoteURL)
	return o.ostreeRun(args...)
}

// pullFromRepo pulls an ostree ref using the instance runner.
func (o *Ostree) pullFromRepo(repoDir, remote, ref string) error {
	if repoDir == "" {
		return errors.New("invalid repoDir parameter")
	}
	if remote == "" {
		return errors.New("invalid remote parameter")
	}
	if ref == "" {
		return errors.New("invalid ref parameter")
	}
	o.Print("Pulling ostree from %s %s:%s ...\n", repoDir, remote, ref)
	return o.ostreeRun("--repo="+repoDir, "pull", remote, ref)
}

// pruneFromRepo prunes an ostree repo using the instance runner.
func (o *Ostree) pruneFromRepo(repoDir, ref, keepObjectsYoungerThan string) error {
	if repoDir == "" {
		return errors.New("invalid repoDir parameter")
	}
	if ref == "" {
		return errors.New("invalid ref parameter")
	}
	if keepObjectsYoungerThan == "" {
		return errors.New("invalid keepObjectsYoungerThan parameter")
	}
	o.Print("Pruning ostree repo for %s and branch %s ...\n", repoDir, ref)
	return o.ostreeRun(
		"--repo="+repoDir, "prune",
		"--depth=5",
		"--refs-only",
		"--keep-younger-than="+keepObjectsYoungerThan,
		"--only-branch="+ref,
	)
}

func (o *Ostree) ListRemotes() ([]string, error) {
	repoDir, err := o.RepoDir()
	if err != nil {
		return nil, err
	}
	return o.listRemotesFromRepo(repoDir)
}

func (o *Ostree) LastCommit() (string, error) {
	repoDir, err := o.RepoDir()
	if err != nil {
		return "", err
	}
	return o.lastCommitFromRepo(repoDir, o.ref)
}

func (o *Ostree) Pull() error {
	ref := o.ref
	if ref == "" {
		return errors.New("invalid ref parameter")
	}
	repoDir, err := o.RepoDir()
	if err != nil {
		return err
	}
	remote, err := o.Remote()
	if err != nil {
		return err
	}
	return o.pullFromRepo(repoDir, remote, ref)
}

func (o *Ostree) Prune() error {
	ref := o.ref
	if ref == "" {
		return errors.New("invalid ref parameter")
	}
	repoDir, err := o.RepoDir()
	if err != nil {
		return err
	}
	keepObjectsYoungerThan, err := o.cfg.GetItem("Ostree.KeepObjectsYoungerThan")
	if err != nil {
		return err
	}
	return o.pruneFromRepo(repoDir, ref, keepObjectsYoungerThan)
}

func (o *Ostree) GenerateStaticDelta() error {
	ref := o.ref
	if ref == "" {
		return errors.New("invalid ref parameter")
	}

	repoDir, err := o.RepoDir()
	if err != nil {
		return err
	}

	o.Print("Generating static delta for %s and ref %s ...\n", repoDir, ref)

	stdout, err := o.ostreeRunCapture(
		"--repo="+repoDir,
		"rev-parse",
		ref,
	)
	if err != nil {
		return err
	}

	revNew, err := readerToFirstNonEmptyLine(stdout)
	if err != nil {
		return err
	}

	stdout, err = o.ostreeRunCapture(
		"--repo="+repoDir,
		"rev-parse",
		ref+"^",
	)
	if err != nil {
		// This is not a fatal error, the branch might not have a previous commit.
	}
	revOld, _ := readerToFirstNonEmptyLine(stdout)

	if revOld != "" {
		err := o.runCmd(
			io.Discard,
			os.Stderr,
			"--repo="+repoDir,
			"rev-parse",
			revOld,
		)
		if err != nil {
			fmt.Fprintf(
				os.Stderr,
				"WARNING: rev-parse for old revision %s failed, Falling back to full delta ...\n",
				revOld,
			)
			revOld = ""
		}
	}
	// SAFETY CHECK: Does the parent object actually exist?
	if revOld != "" {
		err := o.runCmd(
			io.Discard,
			os.Stderr,
			"show",
			"--repo="+repoDir,
			revOld,
		)
		if err != nil {
			fmt.Fprintf(
				os.Stderr,
				"WARNING: Parent commit %s is referenced but missing. Falling back to full delta.\n",
				revOld,
			)
			revOld = ""
		}
	}

	args := []string{
		"--repo=" + repoDir,
		"static-delta", "generate",
		"--to=" + revNew,
		"--inline",
		"--min-fallback-size=0",
		"--disable-bsdiff",
		"--max-chunk-size=64",
	}

	if revOld == "" {
		args = append(args, "--empty")
	} else {
		args = append(args, "--from="+revOld)
	}

	return o.ostreeRun(args...)
}

func (o *Ostree) UpdateSummary() error {
	o.Print("Updating ostree summary ...\n")

	repoDir, err := o.RepoDir()
	if err != nil {
		return err
	}

	args := []string{
		"--repo=" + repoDir,
		"summary",
		"--update",
	}

	gpgArgs, err := o.GpgArgs()
	if err != nil {
		return err
	}
	args = append(args, gpgArgs...)

	return o.ostreeRun(args...)
}

func (o *Ostree) AddRemote() error {
	repoDir, err := o.RepoDir()
	if err != nil {
		return err
	}

	gpgArgs, err := o.ClientSideGpgArgs()
	if err != nil {
		return err
	}

	remote, err := o.Remote()
	if err != nil {
		return err
	}
	remoteURL, err := o.RemoteURL()
	if err != nil {
		return err
	}

	opts := AddRemoteOptions{
		Remote:    remote,
		RemoteURL: remoteURL,
		GpgArgs:   gpgArgs,
		RepoDir:   repoDir,
	}
	return o.addRemote(opts)
}

func (o *Ostree) AddRemoteToRootfs(rootfs string) error {
	gpgArgs, err := o.ClientSideGpgArgs()
	if err != nil {
		return err
	}

	remote, err := o.Remote()
	if err != nil {
		return err
	}
	remoteURL, err := o.RemoteURL()
	if err != nil {
		return err
	}

	opts := AddRemoteOptions{
		Remote:    remote,
		RemoteURL: remoteURL,
		GpgArgs:   gpgArgs,
		Sysroot:   rootfs,
	}
	return o.addRemote(opts)
}

func (o *Ostree) LocalRefs() ([]string, error) {
	repoDir, err := o.RepoDir()
	if err != nil {
		return nil, err
	}
	return o.listLocalRefsFromRepo(repoDir)
}

func (o *Ostree) RemoteRefs() ([]string, error) {
	repoDir, err := o.RepoDir()
	if err != nil {
		return nil, err
	}
	remote, err := o.Remote()
	if err != nil {
		return nil, err
	}
	return o.listRemoteRefsFromRepo(repoDir, remote)
}

// MaybeInitializeRemote initializes an ostree remote.
func (o *Ostree) MaybeInitializeRemote() error {
	repoDir, err := o.RepoDir()
	if err != nil {
		return err
	}

	remote, err := o.Remote()
	if err != nil {
		return err
	}
	remoteURL, err := o.RemoteURL()
	if err != nil {
		return err
	}

	if !directoryExists(repoDir) {
		if err := os.MkdirAll(repoDir, 0755); err != nil {
			return err
		}
	}

	objectsDir := filepath.Join(repoDir, "objects")
	if !directoryExists(objectsDir) {
		o.Print("Initializing local ostree repo at %v ...\n", repoDir)
		if err := o.InitRepo(); err != nil {
			return err
		}
	} else {
		o.Print("ostree repo at %v already initialized. Reusing ...\n", repoDir)
	}

	remotes, err := o.listRemotesFromRepo(repoDir)
	if err != nil {
		return err
	}
	remoteFound := slices.Contains(remotes, remote)
	if remoteFound {
		o.Print("Remote %v already exists, reusing ...\n", remote)
	} else {
		o.Print("Initializing remote %v at %v ...\n", remote, repoDir)
		gpgArgs, err := o.ClientSideGpgArgs()
		if err != nil {
			return err
		}
		args := []string{"--repo=" + repoDir, "remote", "add"}
		args = append(args, gpgArgs...)
		args = append(args, remote, remoteURL)
		err = o.ostreeRun(args...)
		if err != nil {
			return err
		}
	}

	o.Print("Showing current ostree remotes:")
	err = o.ostreeRun("--repo="+repoDir, "remote", "list", "-u")
	return err
}
