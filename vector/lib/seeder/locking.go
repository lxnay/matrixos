package seeder

import (
	"errors"
	"fmt"

	"matrixos/vector/lib/filesystems"
)

func (s *Seeder) SeederLockDir() (string, error) {
	lockDir, err := s.LockDir()
	if err != nil {
		return "", err
	}
	return filesystems.EnsureLockDir(lockDir)
}

func (s *Seeder) SeederLockPath(name string) (string, error) {
	if name == "" {
		return "", errors.New("missing seeder name")
	}
	lockDir, err := s.SeederLockDir()
	if err != nil {
		return "", err
	}
	return filesystems.EnsureLockPath(lockDir, name)
}

func (s *Seeder) ExecuteWithSeederLock(name string, fn func() error) error {
	lockPath, err := s.SeederLockPath(name)
	if err != nil {
		return fmt.Errorf("failed to get seeder lock path: %w", err)
	}
	s.Print("Acquiring seeder %s lock via %s ...\n", name, lockPath)

	timeoutStr, err := s.LockWaitSeconds()
	if err != nil {
		return fmt.Errorf("failed to get lock wait seconds: %w", err)
	}
	return filesystems.ExecuteWithLock(lockPath, timeoutStr, fn, func() {
		s.Print("Lock for seeder %s, %s acquired!\n", name, lockPath)
	})
}
