package imager

import (
	"errors"
	"fmt"

	"matrixos/vector/lib/filesystems"
)

func (im *Imager) ImageLockDir() (string, error) {
	lockDir, err := im.LockDir()
	if err != nil {
		return "", err
	}
	return filesystems.EnsureLockDir(lockDir)
}

func (im *Imager) ImageLockPath() (string, error) {
	if im.ref == "" {
		return "", errors.New("missing ref, set Ref in NewImagerOptions")
	}

	ref, err := im.cleanAndStripRef()
	if err != nil {
		return "", fmt.Errorf("failed to clean ref: %w", err)
	}

	lockDir, err := im.ImageLockDir()
	if err != nil {
		return "", err
	}
	return filesystems.EnsureLockPath(lockDir, ref)
}

func (im *Imager) ExecuteWithImageLock(fn func() error) error {
	lockPath, err := im.ImageLockPath()
	if err != nil {
		return fmt.Errorf("failed to get image lock path: %w", err)
	}
	im.Print("Acquiring branch %s lock via %s ...\n", im.ref, lockPath)

	timeoutStr, err := im.LockWaitSeconds()
	if err != nil {
		return fmt.Errorf("failed to get lock wait seconds: %w", err)
	}
	return filesystems.ExecuteWithLock(lockPath, timeoutStr, fn, func() {
		im.Print("Lock for imager %s, %s acquired!\n", im.ref, lockPath)
	})
}
