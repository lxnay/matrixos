package imager

import (
	"fmt"
	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
	"os"
	"path/filepath"
)

// chroot executes a command inside the image directory via chroot and unshare.
func (im *Imager) chroot(env []string, name string, args []string) error {
	devDir, err := im.DevDir()
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
		"RUNNER_TYPE=imager",
	)

	cmd := runner.ChrootCmd{
		Cmd: runner.Cmd{
			Name:   name,
			Args:   args,
			Env:    env,
			Stdout: im.Stdout(),
			Stderr: im.Stderr(),
		},
		ChrootExec: initScript,
		ChrootDir:  im.rootfs,
	}

	err = im.chrootRunner(&cmd)
	if err != nil {
		return fmt.Errorf("chrooted %s %s failed: %w", name, args, err)
	}

	return nil
}
