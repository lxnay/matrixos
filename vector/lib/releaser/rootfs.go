package releaser

import (
	"fmt"
	"os"
	"path/filepath"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/runner"
)

const (
	imagerPortageSecureBootPem = "etc/portage/secureboot.pem"
	imagerPortageSecureBootKek = "etc/portage/secureboot-kek.pem"
)

func (r *Releaser) CleanRootfs() error {
	if err := checkImageDir(r.imageDir); err != nil {
		return err
	}
	if err := filesystems.CheckDirIsRoot(r.imageDir); err != nil {
		return err
	}

	// Copy SecureBoot certs from private repo.
	certSrc, err := r.SecureBootCertPath()
	if err != nil {
		return err
	}
	certDst := filepath.Join(r.imageDir, imagerPortageSecureBootPem)
	if err := filesystems.CopyFile(certSrc, certDst); err != nil {
		return fmt.Errorf("failed to copy secureboot cert: %w", err)
	}

	kekSrc, err := r.SecureBootKekPath()
	if err != nil {
		return err
	}
	kekDst := filepath.Join(r.imageDir, imagerPortageSecureBootKek)
	if err := filesystems.CopyFile(kekSrc, kekDst); err != nil {
		return fmt.Errorf("failed to copy secureboot KEK cert: %w", err)
	}

	defaultPrivPath, err := r.DefaultPrivateGitRepoPath()
	if err != nil {
		return err
	}

	removeDirs := []string{
		"/root/.bash_history",
		"/root/.ssh",
		"/root/.gnupg",
		"/root/.cache",
		"/root/.local",
		"/var/lib/gdm/.cache",
		"/var/lib/gdm/.local",
		"/var/lib/gdm/.config",
		defaultPrivPath,
		"/var/lib/sbctl/keys",
		"/var/tmp/ostree-gpg-private",
	}

	emptyDirs := []string{
		"/tmp",
		"/dev",
		"/boot",
		"/root",
		"/var/lib/systemd/coredump",
		"/var/tmp/portage",
	}

	removeFiles := []string{
		"/etc/resolv.conf",
		"/etc/ssl/apache2/server.key",
		"/etc/ssl/apache2/server.pem",
		"/etc/portage/secureboot.x509",
		"/root/.lesshst",
		"/root/.bashrc",
		"/root/.xauth*",
	}

	rdOpts := filesystems.RemoveDirOptions{
		Stdout: r.stdout,
		Stderr: r.stderr,
	}
	for _, d := range removeDirs {
		_ = filesystems.RemoveDir(filepath.Join(r.imageDir, d), rdOpts)
	}

	edOpts := filesystems.EmptyDirOptions{
		Stdout: r.stdout,
		Stderr: r.stderr,
	}
	for _, d := range emptyDirs {
		_ = filesystems.EmptyDir(filepath.Join(r.imageDir, d), edOpts)
	}

	rfgOpts := filesystems.RemoveFileWithGlobOptions{
		Stdout: r.stdout,
		Stderr: r.stderr,
	}
	for _, p := range removeFiles {
		_ = filesystems.RemoveFileWithGlob(
			filepath.Join(r.imageDir, p),
			rfgOpts,
		)
	}

	// Prepare Portage directory.
	os.MkdirAll(filepath.Join(r.imageDir, "var/db/repos/gentoo"), 0755)

	return nil
}

// chroot executes a command inside the image directory via chroot and unshare.
func (r *Releaser) chroot(env []string, name string, args []string) error {

	devDir, err := r.DevDir()
	if err != nil {
		return fmt.Errorf("failed to get dev dir: %w", err)
	}

	initScript := filepath.Join(devDir, "build", "init", "init.sh")
	if _, err := os.Stat(initScript); os.IsNotExist(err) {
		return fmt.Errorf("init script not found at %s", initScript)
	}

	env = config.FilterEnvKey(env, "MATRIXOS_DEV_DIR")
	env = config.FilterEnvKey(env, "RUNNER_TYPE")
	env = append(env,
		"MATRIXOS_DEV_DIR="+devDir,
		"RUNNER_TYPE=releaser",
	)

	cmd := runner.ChrootCmd{
		Cmd: runner.Cmd{
			Name:   name,
			Args:   args,
			Env:    env,
			Stdout: r.stdout,
			Stderr: r.stderr,
		},
		ChrootExec: initScript,
		ChrootDir:  r.imageDir,
	}

	err = r.chrootRunner(&cmd)
	if err != nil {
		return fmt.Errorf("chrooted %s %s failed: %w", name, args, err)
	}

	return nil
}

func (r *Releaser) PostCleanShrink() error {
	if err := checkImageDir(r.imageDir); err != nil {
		return err
	}
	if err := filesystems.CheckDirIsRoot(r.imageDir); err != nil {
		return err
	}

	r.Print("Shrinking the rootfs to save space ...\n")

	err := r.chroot(
		nil,
		"/bin/bash",
		// bash -c 'cmd "$@"' -- <args...> passes args as positional params.
		// The exit $? ensures we return the correct exit code from emerge and
		// prevent bash from optimizing the command, making emerge run as PID 1.
		[]string{
			"-c",
			`test -e /etc/profile && source /etc/profile; emerge "$@"; exit $?`,
			"--",
			"--depclean",
			"--with-bdeps=n",
			"--complete-graph",
		},
	)
	if err != nil {
		return err
	}

	removeDirs := []string{"/usr/include"}
	emptyDirs := []string{
		"/usr/lib/pkgconfig",
		"/var/db/repos",
		"/usr/src",
	}

	for _, d := range removeDirs {
		opts := filesystems.RemoveDirOptions{
			Stdout: r.stdout,
			Stderr: r.stderr,
		}
		_ = filesystems.RemoveDir(filepath.Join(r.imageDir, d), opts)
	}
	for _, d := range emptyDirs {
		opts := filesystems.EmptyDirOptions{
			Stdout: r.stdout,
			Stderr: r.stderr,
		}
		_ = filesystems.EmptyDir(filepath.Join(r.imageDir, d), opts)
	}

	r.Print("Removing all {.a,.la} files\n")

	walker := func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			ext := filepath.Ext(path)
			if ext == ".a" || ext == ".la" {
				os.Remove(path)
			}
		}
		return nil
	}

	_ = filepath.WalkDir(filepath.Join(r.imageDir, "usr"), walker)

	r.Print("Shrink completed.\n")
	return nil
}
