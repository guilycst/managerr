//go:build darwin || linux

package placement

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	placementOpenReadOnly = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
)

func openExistingTargetWithParent(rootPath, relative string) (*os.File, *os.File, string, fs.FileInfo, error) {
	if relative == "" {
		return nil, nil, "", nil, fmt.Errorf("%w", ErrRootTarget)
	}
	parent, name, err := openExistingParent(rootPath, relative)
	if err != nil {
		return nil, nil, "", nil, err
	}
	file, info, err := openExistingChild(parent, name)
	if err != nil {
		parent.Close()
		return nil, nil, "", nil, err
	}
	return file, parent, name, info, nil
}

func openExistingDirectory(rootPath, relative string) (*os.File, error) {
	root, err := openRootDirectory(rootPath)
	if err != nil {
		return nil, err
	}
	current := root
	for _, component := range relativeParts(relative) {
		next, childErr := openDirectoryChild(current, component)
		if childErr != nil {
			current.Close()
			return nil, childErr
		}
		current.Close()
		current = next
	}
	return current, nil
}

func openExistingParent(rootPath, relative string) (*os.File, string, error) {
	parentRelative := path.Dir(relative)
	if parentRelative == "." {
		parentRelative = ""
	}
	parent, err := openExistingDirectory(rootPath, parentRelative)
	if err != nil {
		return nil, "", err
	}
	return parent, path.Base(relative), nil
}

func ensureDirectoryPath(rootPath, relative string, syncFn func(*os.File) error) (*os.File, error) {
	directory, _, err := ensureDirectoryPathWithCreated(rootPath, relative, syncFn)
	return directory, err
}

func ensureDirectoryPathWithCreated(rootPath, relative string, syncFn func(*os.File) error) (*os.File, []string, error) {
	root, err := openRootDirectory(rootPath)
	if err != nil {
		return nil, nil, err
	}
	current := root
	created := make([]string, 0)
	var prefix string
	for _, component := range relativeParts(relative) {
		if component == "" || component == "." || component == ".." || strings.ContainsAny(component, `/\\`) {
			current.Close()
			return nil, created, fmt.Errorf("%w: invalid destination directory component", ErrPathEscape)
		}
		if prefix == "" {
			prefix = component
		} else {
			prefix = path.Join(prefix, component)
		}
		var stat unix.Stat_t
		statErr := unix.Fstatat(int(current.Fd()), component, &stat, unix.AT_SYMLINK_NOFOLLOW)
		createdHere := false
		if errors.Is(statErr, unix.ENOENT) {
			if err := unix.Mkdirat(int(current.Fd()), component, 0o755); err != nil {
				if !errors.Is(err, unix.EEXIST) {
					current.Close()
					return nil, created, classifyPlacementError(err)
				}
			} else {
				createdHere = true
			}
			if createdHere {
				created = append(created, prefix)
				if syncFn != nil {
					if err := syncFn(current); err != nil {
						current.Close()
						return nil, created, fmt.Errorf("%w: sync containing directory before creating %q: %w", ErrPublicationUnknown, prefix, err)
					}
				}
			}
		} else if statErr != nil {
			current.Close()
			return nil, created, classifyPlacementError(statErr)
		} else if (uint32(stat.Mode) & unix.S_IFMT) == unix.S_IFLNK {
			current.Close()
			return nil, created, ErrSymlink
		} else if (uint32(stat.Mode) & unix.S_IFMT) != unix.S_IFDIR {
			current.Close()
			return nil, created, ErrSpecialFile
		}
		next, childErr := openDirectoryChild(current, component)
		if childErr != nil {
			current.Close()
			return nil, created, childErr
		}
		current.Close()
		current = next
	}
	return current, created, nil
}

func syncDirectoryPath(rootPath, relative string) error {
	directory, err := openExistingDirectory(rootPath, relative)
	if err != nil {
		return err
	}
	defer directory.Close()
	return syncDirectory(directory)
}

func openRootDirectory(rootPath string) (*os.File, error) {
	fd, err := unix.Open("/", placementOpenReadOnly|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, classifyPlacementError(err)
	}
	root := os.NewFile(uintptr(fd), "/")
	if root == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open filesystem root: invalid descriptor")
	}
	if rootPath == "/" {
		return root, nil
	}
	current := root
	for _, component := range strings.Split(strings.TrimPrefix(rootPath, "/"), "/") {
		if component == "" || component == "." || component == ".." {
			current.Close()
			return nil, fmt.Errorf("%w: root path is not canonical", ErrRootNotConfigured)
		}
		next, childErr := openDirectoryChild(current, component)
		if childErr != nil {
			current.Close()
			return nil, childErr
		}
		current.Close()
		current = next
	}
	return current, nil
}

func openDirectoryChild(directory *os.File, name string) (*os.File, error) {
	stat, err := statChild(directory, name)
	if err != nil {
		return nil, err
	}
	if (uint32(stat.Mode) & unix.S_IFMT) != unix.S_IFDIR {
		return nil, ErrSpecialFile
	}
	fd, err := unix.Openat(int(directory.Fd()), name, placementOpenReadOnly|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, classifyPlacementError(err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("open directory: invalid descriptor")
	}
	return file, nil
}

func openExistingChild(directory *os.File, name string) (*os.File, fs.FileInfo, error) {
	stat, err := statChild(directory, name)
	if err != nil {
		return nil, nil, err
	}
	typeOf := uint32(stat.Mode) & unix.S_IFMT
	if typeOf != unix.S_IFREG && typeOf != unix.S_IFDIR {
		return nil, nil, ErrSpecialFile
	}
	flags := placementOpenReadOnly
	if typeOf == unix.S_IFDIR {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(int(directory.Fd()), name, flags, 0)
	if err != nil {
		return nil, nil, classifyPlacementError(err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, nil, errors.New("open filesystem object: invalid descriptor")
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		file.Close()
		return nil, nil, ErrSymlink
	}
	return file, info, nil
}

func statChild(directory *os.File, name string) (*unix.Stat_t, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		return nil, ErrPathEscape
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(int(directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, classifyPlacementError(err)
	}
	if (uint32(stat.Mode) & unix.S_IFMT) == unix.S_IFLNK {
		return nil, ErrSymlink
	}
	return &stat, nil
}

func createExclusiveChild(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, classifyPlacementError(err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("create staging file: invalid descriptor")
	}
	return file, nil
}

func publishNoReplace(stage, parent *os.File, stageName, destinationName string) (bool, error) {
	// The destination is linked from the open staging inode. The caller keeps
	// the staging pathname for the janitor because unlinking by name cannot be
	// made conditional on inode identity.
	if err := linkStageNoReplace(stage, parent, stageName, destinationName); err != nil {
		return false, classifyPlacementError(err)
	}
	return true, nil
}

func verifyOwnedChild(parent *os.File, name string, expected fs.FileInfo) error {
	if expected == nil {
		return fmt.Errorf("%w: missing staging identity", ErrStageChanged)
	}
	actual, info, err := openExistingChild(parent, name)
	if err != nil {
		return fmt.Errorf("%w: staging path %q is unavailable: %v", ErrStageChanged, name, err)
	}
	actual.Close()
	if !sameObject(expected, info) {
		return fmt.Errorf("%w: staging path %q names another object", ErrStageChanged, name)
	}
	return nil
}

func linkNoReplace(sourceParent *os.File, sourceName string, destinationParent *os.File, destinationName string) (bool, error) {
	if err := unix.Linkat(int(sourceParent.Fd()), sourceName, int(destinationParent.Fd()), destinationName, 0); err != nil {
		return false, classifyPlacementError(err)
	}
	return true, nil
}

func removeEmptyDirectory(rootPath, relative string) error {
	if relative == "" {
		return ErrRootTarget
	}
	parent, name, err := openExistingParent(rootPath, relative)
	if err != nil {
		return err
	}
	defer parent.Close()
	return classifyPlacementError(unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR))
}

func syncDirectory(directory *os.File) error {
	return directory.Sync()
}

func listDirectoryNames(directory *os.File) ([]string, error) {
	clone, err := duplicateFile(directory)
	if err != nil {
		return nil, err
	}
	defer clone.Close()
	return clone.Readdirnames(-1)
}

func duplicateFile(file *os.File) (*os.File, error) {
	fd, err := unix.Dup(int(file.Fd()))
	if err != nil {
		return nil, classifyPlacementError(err)
	}
	clone := os.NewFile(uintptr(fd), file.Name())
	if clone == nil {
		_ = unix.Close(fd)
		return nil, errors.New("duplicate filesystem descriptor: invalid descriptor")
	}
	return clone, nil
}

func sameObject(left, right fs.FileInfo) bool {
	leftStat, leftOK := left.Sys().(*syscall.Stat_t)
	rightStat, rightOK := right.Sys().(*syscall.Stat_t)
	return leftOK && rightOK && leftStat.Dev == rightStat.Dev && leftStat.Ino == rightStat.Ino
}

func fileIdentity(info fs.FileInfo) string {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("unix:dev=%d:ino=%d:size=%d:mtime=%d", stat.Dev, stat.Ino, info.Size(), info.ModTime().UnixNano())
	}
	return fmt.Sprintf("unix:mode=%o:size=%d:mtime=%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
}

func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ENOENT) }

func isExist(err error) bool { return errors.Is(err, os.ErrExist) || errors.Is(err, unix.EEXIST) }

func isCrossDevice(err error) bool { return errors.Is(err, unix.EXDEV) }

func classifyPlacementError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, unix.ELOOP):
		return ErrSymlink
	case errors.Is(err, unix.ENOTDIR):
		return ErrSpecialFile
	case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
		return fmt.Errorf("%w: filesystem access denied", os.ErrPermission)
	case errors.Is(err, unix.ENOENT):
		return fmt.Errorf("filesystem object does not exist: %w", os.ErrNotExist)
	case errors.Is(err, unix.EEXIST):
		return fmt.Errorf("filesystem object already exists: %w", os.ErrExist)
	case errors.Is(err, unix.EXDEV):
		return fmt.Errorf("cross-device filesystem operation: %w", unix.EXDEV)
	default:
		return err
	}
}
