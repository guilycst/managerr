//go:build linux

package placement

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const placementWritesSupported = true

// linkStageNoReplace links the already-open staging inode. Using the open
// descriptor avoids a pathname substitution between the ownership check and
// publication. AT_EMPTY_PATH may be unavailable to an unprivileged caller;
// /proc/self/fd is the equivalent descriptor-bound fallback when procfs is
// mounted.
func linkStageNoReplace(stage, parent *os.File, stageName, destinationName string) error {
	pathFD, err := unix.Openat(int(parent.Fd()), stageName, unix.O_PATH|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	pathFile := os.NewFile(uintptr(pathFD), stageName)
	if pathFile == nil {
		_ = unix.Close(pathFD)
		return errors.New("open staging identity descriptor: invalid descriptor")
	}
	pathInfo, statErr := pathFile.Stat()
	if statErr != nil {
		_ = pathFile.Close()
		return statErr
	}
	stageInfo, statErr := stage.Stat()
	if statErr != nil {
		_ = pathFile.Close()
		return statErr
	}
	if !sameObject(stageInfo, pathInfo) {
		_ = pathFile.Close()
		return ErrStageChanged
	}
	err = unix.Linkat(int(pathFile.Fd()), "", int(parent.Fd()), destinationName, unix.AT_EMPTY_PATH)
	_ = pathFile.Close()
	if err == nil || (!errors.Is(err, unix.EPERM) && !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOSYS)) {
		return err
	}
	return unix.Linkat(unix.AT_FDCWD, fmt.Sprintf("/proc/self/fd/%d", stage.Fd()), int(parent.Fd()), destinationName, unix.AT_SYMLINK_FOLLOW)
}
