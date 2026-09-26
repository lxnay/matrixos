package ostree

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"matrixos/vector/lib/runner"
)

// gpgMu serialises operations that invoke the gpg binary for key
// import or daemon management.  The gpg binary does not handle
// concurrent invocations safely.
var gpgMu sync.Mutex

// KillGpgDaemons kills any gpg-agent, dirmngr, and scdaemon processes
// associated with the given GPG homedir. This prevents leftover daemon
// processes from lingering after GPG operations complete.
func KillGpgDaemons(run runner.Func, homeDir string, stdout, stderr io.Writer) {
	if homeDir == "" {
		return
	}
	gpgMu.Lock()
	defer gpgMu.Unlock()
	_ = run(&runner.Cmd{
		Name:   "gpgconf",
		Args:   []string{"--homedir", homeDir, "--kill", "all"},
		Stdout: stdout,
		Stderr: stderr,
	})
}

// ClientSideGpgArgs returns arguments for client-side GPG verification.
func ClientSideGpgArgs(gpgEnabled bool, pubKeyPath string) ([]string, error) {
	var gpgArgs []string

	if gpgEnabled {
		gpgArgs = append(
			gpgArgs,
			"--set=gpg-verify=true",
			"--gpg-import="+pubKeyPath,
		)
	} else {
		gpgArgs = append(gpgArgs, "--no-gpg-verify")
	}
	return gpgArgs, nil
}

// PatchGpgHomeDir sets the correct permissions on the GPG homedir.
func PatchGpgHomeDir(homeDir string) error {
	if homeDir == "" {
		return errors.New("missing homeDir parameter")
	}

	if err := os.MkdirAll(homeDir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(homeDir, 0700); err != nil {
		return err
	}

	err := walkGpgHomeDir(homeDir, func(path string, info os.FileInfo) error {
		if !info.IsDir() {
			return os.Chmod(path, 0600)
		}
		return nil
	})
	if err != nil {
		return err
	}

	curUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("could not find root user: %w", err)
	}
	uid, _ := strconv.Atoi(curUser.Uid)
	gid, _ := strconv.Atoi(curUser.Gid)

	return walkGpgHomeDir(homeDir, func(path string, info os.FileInfo) error {
		return os.Chown(path, uid, gid)
	})
}

// walkGpgHomeDir tolerates entries disappearing while GPG updates its keyring.
func walkGpgHomeDir(homeDir string, visit func(string, os.FileInfo) error) error {
	return filepath.Walk(homeDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if path != homeDir && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if err := visit(path, info); err != nil &&
			path != homeDir && errors.Is(err, os.ErrNotExist) {
			return nil
		} else {
			return err
		}
	})
}

// GpgSignedFilePath returns the path to a GPG signed file.
func GpgSignedFilePath(filePath string) string {
	return filePath + ".asc"
}

func (o *Ostree) getDevGpgHomeDir() (string, error) {
	dir, err := o.cfg.GetItem("Ostree.DevGpgHomeDir")
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", errors.New("invalid Ostree.DevGpgHomeDir")
	}
	return dir, nil
}

func (o *Ostree) GpgHomeDir() (string, error) {
	devGpgHomeDir, err := o.getDevGpgHomeDir()
	if err != nil {
		return "", err
	}
	if err := PatchGpgHomeDir(devGpgHomeDir); err != nil {
		return "", err
	}
	return devGpgHomeDir, nil
}

func (o *Ostree) GpgKeyID() (string, error) {
	homeDir, err := o.GpgHomeDir()
	if err != nil {
		return "", err
	}
	pubkeyPath, err := o.GpgBestPubKeyPath()
	if err != nil {
		return "", err
	}

	out := new(bytes.Buffer)
	err = o.runner(&runner.Cmd{
		Name: "gpg",
		Args: []string{
			"--homedir", homeDir,
			"--batch", "--yes",
			"--with-colons",
			"--show-keys",
			"--keyid-format", "LONG",
			pubkeyPath,
		},
		Stdout: out,
		Stderr: o.stdout,
	})
	if err != nil {
		return "", err
	}

	var keyID string
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "pub") {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) >= 5 {
			keyID = strings.TrimSpace(parts[4])
			break
		}
	}

	err = scanner.Err()
	if err != nil {
		return "", err
	}

	if keyID == "" {
		return keyID, errors.New("cannot find gpg ostree key id.")
	}
	return keyID, nil
}

func (o *Ostree) ImportGpgKey(keyPath string) error {
	if keyPath == "" {
		return errors.New("missing keyPath parameter")
	}
	if !fileExists(keyPath) {
		return fmt.Errorf("file %s does not exist", keyPath)
	}

	homeDir, err := o.GpgHomeDir()
	if err != nil {
		return err
	}

	gpgMu.Lock()
	defer gpgMu.Unlock()
	return o.runner(&runner.Cmd{
		Name:   "gpg",
		Args:   []string{"--homedir", homeDir, "--batch", "--yes", "--import", keyPath},
		Stdout: o.stdout,
		Stderr: o.stderr,
	})
}

func (o *Ostree) GpgSignFile(file string) error {
	if file == "" {
		return errors.New("missing file parameter")
	}
	if !fileExists(file) {
		return fmt.Errorf("file %s does not exist", file)
	}

	homeDir, err := o.GpgHomeDir()
	if err != nil {
		return err
	}

	keyID, err := o.GpgKeyID()
	if err != nil {
		return err
	}

	ascFile := GpgSignedFilePath(file)

	err = o.runner(&runner.Cmd{
		Name: "gpg",
		Args: []string{
			"--homedir", homeDir,
			"--batch", "--yes",
			"--local-user", keyID,
			"--armor", "--detach-sign",
			"--output", ascFile,
			file,
		},
		Stdout: o.stdout,
		Stderr: o.stdout,
	})
	if err != nil {
		return err
	}

	o.Print("GPG signature file %v created.\n", ascFile)
	return nil
}

func (o *Ostree) GpgKeys() ([]string, error) {
	var keys []string

	gpgKeyPath, err := o.GpgPrivateKeyPath()
	if err != nil {
		return nil, err
	}
	keys = append(keys, gpgKeyPath)

	signingPubKey, err := o.GpgBestPubKeyPath()
	if err != nil {
		return nil, err
	}
	keys = append(keys, signingPubKey)

	officialPubKeyPath, err := o.GpgOfficialPubKeyPath()
	if err != nil {
		return nil, err
	}
	// if it's the same as signingPubKey, do not add a dup.
	if signingPubKey != officialPubKeyPath {
		keys = append(keys, officialPubKeyPath)
	}

	return keys, nil
}

func (o *Ostree) InitializeSigningGpg() error {
	keys, err := o.GpgKeys()
	if err != nil {
		return err
	}

	o.Print("GPG signing enabled.\n")
	for _, key := range keys {
		if !fileExists(key) {
			o.PrintError("WARNING: Signing GPG key %s not present, skipping import ...\n", key)
			continue
		}
		if err := o.ImportGpgKey(key); err != nil {
			return fmt.Errorf("failed to import gpg key %s: %w", key, err)
		}
	}
	return nil
}

// initializeRemoteSigningGpg imports GPG keys into the remote ostree repository.
func (o *Ostree) initializeRemoteSigningGpg(remote, repoDir string) error {
	if remote == "" {
		return errors.New("initializeRemoteSigningGpg: missing remote parameter")
	}
	if repoDir == "" {
		return errors.New("initializeRemoteSigningGpg: missing repoDir parameter")
	}

	keys, err := o.GpgKeys()
	if err != nil {
		return err
	}

	o.Print("Remote GPG signing enabled.\n")
	for _, key := range keys {
		if !fileExists(key) {
			o.PrintError("WARNING: Remote signing GPG key %s not present, skipping import ...\n", key)
			continue
		}
		gpgMu.Lock()
		err := o.ostreeRun("--repo="+repoDir, "remote", "gpg-import", remote, "-k", key)
		gpgMu.Unlock()
		if err != nil {
			return fmt.Errorf("failed to import gpg key %s to remote %s: %w", key, remote, err)
		}
	}
	return nil
}

func (o *Ostree) MaybeInitializeGpg() error {
	repoDir, err := o.RepoDir()
	if err != nil {
		return err
	}
	remote, err := o.Remote()
	if err != nil {
		return err
	}

	return o.maybeInitializeGpgForRepo(remote, repoDir)
}

// maybeInitializeGpgForRepo initializes GPG keys for an ostree repository.
func (o *Ostree) maybeInitializeGpgForRepo(remote, repoDir string) error {
	gpgEnabled, err := o.GpgEnabled()
	if err != nil {
		return err
	}
	if !gpgEnabled {
		o.Print("GPG signing is disabled. Skipping GPG initialization ...")
		return nil
	}

	if err := o.InitializeSigningGpg(); err != nil {
		return err
	}
	return o.initializeRemoteSigningGpg(remote, repoDir)
}

func (o *Ostree) GpgArgs() ([]string, error) {
	gpgEnabled, err := o.GpgEnabled()
	if err != nil {
		return nil, err
	}
	if !gpgEnabled {
		return nil, nil
	}

	keyID, err := o.GpgKeyID()
	if err != nil {
		return nil, err
	}

	homeDir, err := o.GpgHomeDir()
	if err != nil {
		return nil, err
	}

	return []string{
		"--gpg-sign=" + keyID,
		"--gpg-homedir=" + homeDir,
	}, nil
}

// KillGpgDaemons kills any gpg-agent, dirmngr, and scdaemon processes
// for the OSTree GPG homedir.
func (o *Ostree) KillGpgDaemons() {
	homeDir, err := o.getDevGpgHomeDir()
	if err != nil {
		return
	}
	o.Print("Killing GPG daemons for %s ...\n", homeDir)
	KillGpgDaemons(o.runner, homeDir, o.stdout, o.stderr)
}
