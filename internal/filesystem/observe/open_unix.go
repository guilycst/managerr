//go:build darwin || linux

package observe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/guilycst/managerr/internal/domain"
	"golang.org/x/sys/unix"
)

const openReadOnly = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK

func pathIsAbsolute(value string) bool { return filepath.IsAbs(value) }

func cleanHostPath(value string) string { return filepath.Clean(value) }

func openConstrainedDirectory(rootPath, relativePath string) (*os.File, error) {
	root, err := openRootDirectory(rootPath)
	if err != nil {
		return nil, err
	}
	parts := splitRelative(relativePath)
	current := root
	for _, part := range parts {
		next, err := openChild(current, part, true)
		if err != nil {
			current.Close()
			return nil, err
		}
		current.Close()
		current = next
	}
	return current, nil
}

func openConstrainedTarget(rootPath, relativePath string) (*os.File, os.FileInfo, error) {
	parts := splitRelative(relativePath)
	if len(parts) == 0 {
		return nil, nil, fmt.Errorf("%w", ErrRootTarget)
	}
	root, err := openRootDirectory(rootPath)
	if err != nil {
		return nil, nil, err
	}
	current := root
	for _, part := range parts[:len(parts)-1] {
		next, err := openChild(current, part, true)
		if err != nil {
			current.Close()
			return nil, nil, err
		}
		current.Close()
		current = next
	}
	target, err := openChild(current, parts[len(parts)-1], false)
	current.Close()
	if err != nil {
		return nil, nil, err
	}
	info, err := target.Stat()
	if err != nil {
		target.Close()
		return nil, nil, err
	}
	return target, info, nil
}

func openConstrainedChild(directory *os.File, name string) (*os.File, os.FileInfo, error) {
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || name == "." || name == ".." {
		return nil, nil, fmt.Errorf("%w: invalid directory component", ErrPathEscape)
	}
	child, err := openChild(directory, name, false)
	if err != nil {
		return nil, nil, err
	}
	info, err := child.Stat()
	if err != nil {
		child.Close()
		return nil, nil, err
	}
	return child, info, nil
}

func openRootDirectory(rootPath string) (*os.File, error) {
	// Open root through descriptors too. Opening the absolute path directly
	// would permit a symlink in one of its parent components.
	root, err := os.Open("/")
	if err != nil {
		return nil, err
	}
	if rootPath == string(filepath.Separator) {
		return root, nil
	}
	parts := strings.Split(strings.TrimPrefix(rootPath, string(filepath.Separator)), string(filepath.Separator))
	current := root
	for _, part := range parts {
		if part == "" {
			continue
		}
		next, err := openChild(current, part, true)
		if err != nil {
			current.Close()
			return nil, err
		}
		current.Close()
		current = next
	}
	return current, nil
}

func openChild(directory *os.File, name string, directoryOnly bool) (*os.File, error) {
	var before unix.Stat_t
	if err := unix.Fstatat(int(directory.Fd()), name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, classifyUnixOpenError(err)
	}
	fileType := uint32(before.Mode) & unix.S_IFMT
	if fileType == unix.S_IFLNK {
		return nil, ErrSymlink
	}
	if directoryOnly && fileType != unix.S_IFDIR {
		return nil, fmt.Errorf("%w: path component is not a directory", ErrSpecialFile)
	}
	flags := openReadOnly
	if directoryOnly {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(int(directory.Fd()), name, flags, 0)
	if err != nil {
		return nil, classifyUnixOpenError(err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open filesystem object: invalid descriptor")
	}
	return file, nil
}

func splitRelative(relativePath string) []string {
	if relativePath == "" {
		return nil
	}
	return strings.Split(relativePath, "/")
}

func writableDirectory(file *os.File) bool {
	return unix.Faccessat(int(file.Fd()), ".", unix.W_OK|unix.X_OK, unix.AT_EACCESS) == nil
}

func writablePath(rootPath, relativePath string, directory bool) bool {
	root, err := openRootDirectory(rootPath)
	if err != nil {
		return false
	}
	defer root.Close()
	parts := splitRelative(relativePath)
	current := root
	parentParts := parts
	if len(parentParts) > 0 {
		parentParts = parts[:len(parts)-1]
	}
	for _, part := range parentParts {
		next, err := openChild(current, part, true)
		if err != nil {
			return false
		}
		if current != root {
			current.Close()
		}
		current = next
	}
	if current != root {
		defer current.Close()
	}
	name := "."
	if len(parts) > 0 {
		name = parts[len(parts)-1]
	}
	mode := uint32(unix.W_OK)
	if directory {
		mode |= unix.X_OK
	}
	return unix.Faccessat(int(current.Fd()), name, mode, unix.AT_EACCESS) == nil
}

func classifyUnixOpenError(err error) error {
	switch {
	case errors.Is(err, unix.ELOOP):
		return ErrSymlink
	case errors.Is(err, unix.ENOTDIR):
		return fmt.Errorf("%w: path component is not a directory", ErrSpecialFile)
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		return fmt.Errorf("%w: filesystem access denied", os.ErrPermission)
	case errors.Is(err, unix.ENOENT):
		return fmt.Errorf("filesystem object does not exist: %w", os.ErrNotExist)
	default:
		return err
	}
}

func noFollowCapability() domain.CapabilityState {
	return domain.CapabilitySupported
}
