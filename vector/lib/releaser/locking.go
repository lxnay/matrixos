package releaser

import (
	"errors"
	"fmt"

	"matrixos/vector/lib/filesystems"
)

func (r *Releaser) ReleaseLockDir() (string, error) {
	lockDir, err := r.LockDir()
	if err != nil {
		return "", err
	}
	return filesystems.EnsureLockDir(lockDir)
}

func (r *Releaser) ReleaseLockPath() (string, error) {
	if r.ref == "" {
		return "", errors.New("missing release name")
	}
	lockDir, err := r.ReleaseLockDir()
	if err != nil {
		return "", err
	}
	return filesystems.EnsureLockPath(lockDir, r.ref)
}

func (r *Releaser) ExecuteWithReleaseLock(fn func() error) error {
	lockPath, err := r.ReleaseLockPath()
	if err != nil {
		return fmt.Errorf("failed to get release lock path: %w", err)
	}
	r.Print("Acquiring release %s lock via %s ...\n", r.ref, lockPath)

	timeoutStr, err := r.LockWaitSeconds()
	if err != nil {
		return fmt.Errorf("failed to get lock wait seconds: %w", err)
	}
	return filesystems.ExecuteWithLock(lockPath, timeoutStr, fn, func() {
		r.Print("Lock for releaser %s, %s acquired!\n", r.ref, lockPath)
	})
}
