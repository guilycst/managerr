//go:build windows

package storage

import (
	"errors"
	"fmt"
	"os"
)

var ErrAlreadyLocked = errors.New("storage database is already locked")

type processLock struct {
	file *os.File
	path string
}

func acquireProcessLock(path string) (*processLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, ErrAlreadyLocked
		}
		return nil, fmt.Errorf("open process lock: %w", err)
	}
	return &processLock{file: file, path: path}, nil
}

func (lock *processLock) close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	err := lock.file.Close()
	if removeErr := os.Remove(lock.path); err == nil {
		err = removeErr
	}
	if err != nil {
		return fmt.Errorf("release process lock: %w", err)
	}
	return nil
}
