//go:build linux

package organize

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const organizeWritesSupported = true

func renameNoReplace(sourceParent *os.File, sourceName string, destinationParent *os.File, destinationName string) (bool, error) {
	err := unix.Renameat2(int(sourceParent.Fd()), sourceName, int(destinationParent.Fd()), destinationName, unix.RENAME_NOREPLACE)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP) {
		return false, fmt.Errorf("%w: no-replace rename is unavailable: %v", ErrUnsupported, err)
	}
	return false, classifyOrganizeError(err)
}

func removeEntry(parent *os.File, name string, directory bool) error {
	flags := 0
	if directory {
		flags = unix.AT_REMOVEDIR
	}
	if err := unix.Unlinkat(int(parent.Fd()), name, flags); err != nil {
		if errors.Is(err, unix.EXDEV) {
			return fmt.Errorf("%w: %v", errCrossDevice, err)
		}
		if errors.Is(err, unix.ENOTEMPTY) || errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("%w: directory is not empty", ErrSourceChanged)
		}
		return classifyOrganizeError(err)
	}
	return nil
}
