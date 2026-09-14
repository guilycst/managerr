//go:build darwin || linux

package organize

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

const organizeOpenReadOnly = unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK

func openExistingTarget(rootPath, relative string) (*nodeHandle, error) {
	if relative == "" {
		return nil, fmt.Errorf("%w", ErrRootTarget)
	}
	parent, name, err := openExistingParent(rootPath, relative)
	if err != nil {
		return nil, err
	}
	file, info, err := openExistingChild(parent, name)
	if err != nil {
		_ = parent.Close()
		return nil, err
	}
	return &nodeHandle{file: file, parent: parent, name: name, info: info}, nil
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
			_ = current.Close()
			return nil, childErr
		}
		_ = current.Close()
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
		if !safeComponent(component) {
			_ = current.Close()
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
			if mkdirErr := unix.Mkdirat(int(current.Fd()), component, 0o755); mkdirErr != nil {
				if !errors.Is(mkdirErr, unix.EEXIST) {
					_ = current.Close()
					return nil, created, classifyOrganizeError(mkdirErr)
				}
			} else {
				createdHere = true
			}
			if createdHere {
				created = append(created, prefix)
				if syncFn != nil {
					if syncErr := syncFn(current); syncErr != nil {
						_ = current.Close()
						return nil, created, fmt.Errorf("%w: sync containing directory before creating %q: %w", ErrPublicationUnknown, prefix, syncErr)
					}
				}
			}
		} else if statErr != nil {
			_ = current.Close()
			return nil, created, classifyOrganizeError(statErr)
		} else {
			if (uint32(stat.Mode) & unix.S_IFMT) == unix.S_IFLNK {
				_ = current.Close()
				return nil, created, ErrSymlink
			}
			if (uint32(stat.Mode) & unix.S_IFMT) != unix.S_IFDIR {
				_ = current.Close()
				return nil, created, ErrSpecialFile
			}
		}
		next, childErr := openDirectoryChild(current, component)
		if childErr != nil {
			_ = current.Close()
			return nil, created, childErr
		}
		_ = current.Close()
		current = next
	}
	return current, created, nil
}

func openRootDirectory(rootPath string) (*os.File, error) {
	fd, err := unix.Open("/", organizeOpenReadOnly|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, classifyOrganizeError(err)
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
		if !safeComponent(component) {
			_ = current.Close()
			return nil, fmt.Errorf("%w: root path is not canonical", ErrRootNotConfigured)
		}
		next, childErr := openDirectoryChild(current, component)
		if childErr != nil {
			_ = current.Close()
			return nil, childErr
		}
		_ = current.Close()
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
	fd, err := unix.Openat(int(directory.Fd()), name, organizeOpenReadOnly|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, classifyOrganizeError(err)
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
	flags := organizeOpenReadOnly
	if typeOf == unix.S_IFDIR {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(int(directory.Fd()), name, flags, 0)
	if err != nil {
		return nil, nil, classifyOrganizeError(err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, nil, errors.New("open filesystem object: invalid descriptor")
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, classifyOrganizeError(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		_ = file.Close()
		return nil, nil, ErrSymlink
	}
	if info.IsDir() != (typeOf == unix.S_IFDIR) {
		_ = file.Close()
		return nil, nil, ErrSourceChanged
	}
	return file, info, nil
}

func openChild(directory *os.File, name string) (*nodeHandle, error) {
	file, info, err := openExistingChild(directory, name)
	if err != nil {
		return nil, err
	}
	return &nodeHandle{file: file, name: name, info: info}, nil
}

func statChild(directory *os.File, name string) (*unix.Stat_t, error) {
	if !safeComponent(name) {
		return nil, ErrPathEscape
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(int(directory.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, classifyOrganizeError(err)
	}
	if (uint32(stat.Mode) & unix.S_IFMT) == unix.S_IFLNK {
		return nil, ErrSymlink
	}
	return &stat, nil
}

func listDirectoryNames(directory *os.File) ([]string, error) {
	// Dup would share the directory stream offset with the caller. Open the
	// directory through its descriptor instead so repeated read-backs always
	// enumerate from the beginning without following a pathname alias.
	fd, err := unix.Openat(int(directory.Fd()), ".", organizeOpenReadOnly|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, classifyOrganizeError(err)
	}
	clone := os.NewFile(uintptr(fd), directory.Name())
	if clone == nil {
		_ = unix.Close(fd)
		return nil, errors.New("duplicate directory descriptor: invalid descriptor")
	}
	defer clone.Close()
	return clone.Readdirnames(-1)
}

func syncDirectory(directory *os.File) error {
	return directory.Sync()
}

func filesystemDevice(info fs.FileInfo) (uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, fmt.Errorf("%w: filesystem device identity unavailable", ErrUnsupported)
	}
	return uint64(stat.Dev), nil
}

func sameObject(left, right fs.FileInfo) bool {
	if left == nil || right == nil {
		return false
	}
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

func classifyOrganizeError(err error) error {
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
		return fmt.Errorf("%w: %v", errCrossDevice, err)
	default:
		return err
	}
}

func relativeParts(relative string) []string {
	if relative == "" {
		return nil
	}
	return strings.Split(relative, "/")
}

func safeComponent(component string) bool {
	return component != "" && component != "." && component != ".." && !strings.ContainsAny(component, `/\\`)
}
