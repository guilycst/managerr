//go:build !windows

package storage

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

var ErrAlreadyLocked = errors.New("storage database is already locked")

type processLock struct {
	file *os.File
}

func acquireProcessLock(path string) (*processLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open process lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrAlreadyLocked
		}
		return nil, fmt.Errorf("acquire process lock: %w", err)
	}
	return &processLock{file: file}, nil
}

func (lock *processLock) close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	if err := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN); err != nil {
		_ = lock.file.Close()
		return fmt.Errorf("release process lock: %w", err)
	}
	return lock.file.Close()
}
