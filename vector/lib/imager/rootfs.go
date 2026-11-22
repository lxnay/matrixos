package imager

import (
	"fmt"

	"matrixos/vector/lib/worker"
)

// chroot executes a command inside the image directory via chroot and unshare.
func (im *Imager) chroot(env []string, name string, args []string) error {
	devDir, err := im.DevDir()
	if err != nil {
		return fmt.Errorf("failed to get dev dir: %w", err)
	}
	return worker.RunInitChroot(worker.InitChrootOptions{
		DevDir:     devDir,
		ChrootDir:  im.rootfs,
		RunnerType: "imager",
		Name:       name,
		Args:       args,
		Env:        env,
		Stdout:     im.Stdout(),
		Stderr:     im.Stderr(),
		ChrootRun:  im.chrootRunner,
	})
}
