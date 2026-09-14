//go:build linux

package placement

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const placementWritesSupported = true

// createExclusiveChild creates an anonymous staging inode in the destination
// directory. O_TMPFILE gives the operation a descriptor-owned inode with no
// pathname alias, so close reclaims an unpublished stage and publication does
// not leave a hidden hardlink beside the destination.
func createExclusiveChild(parent *os.File, _ string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), ".", unix.O_TMPFILE|unix.O_WRONLY|unix.O_CLOEXEC, 0o600)
	if err != nil {
		if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOSYS) {
			return nil, fmt.Errorf("%w: anonymous staging is unavailable: %v", ErrUnsupported, err)
		}
		return nil, classifyPlacementError(err)
	}
	file := os.NewFile(uintptr(fd), "anonymous-stage")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("create anonymous staging file: invalid descriptor")
	}
	return file, nil
}

// linkStageNoReplace publishes the already-open anonymous staging inode.
// AT_EMPTY_PATH binds the source to the descriptor and therefore cannot
// publish a replacement object at a raced pathname.
func linkStageNoReplace(stage, parent *os.File, _, destinationName string) error {
	if stage == nil {
		return fmt.Errorf("%w: missing staging descriptor", ErrStageChanged)
	}
	err := unix.Linkat(int(stage.Fd()), "", int(parent.Fd()), destinationName, unix.AT_EMPTY_PATH)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.ENOSYS) {
		return fmt.Errorf("%w: descriptor-bound staging publication is unavailable: %v", ErrUnsupported, err)
	}
	return err
}

// linkOpenSourceNoReplace publishes from the manifest-validated source
// descriptor. A source pathname exchange after validation cannot affect the
// inode selected by this operation.
func linkOpenSourceNoReplace(source, destinationParent *os.File, destinationName string) error {
	if source == nil {
		return fmt.Errorf("%w: missing source descriptor", ErrSourceChanged)
	}
	err := unix.Linkat(int(source.Fd()), "", int(destinationParent.Fd()), destinationName, unix.AT_EMPTY_PATH)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.ENOSYS) {
		return fmt.Errorf("%w: descriptor-bound hardlink publication is unavailable: %v", ErrUnsupported, err)
	}
	return err
}
