//go:build !(darwin || linux)

package organize

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const organizeWritesSupported = false

// Read helpers keep cross-compilation and future platform ports possible.
// Mutations remain fail-closed until a target-specific no-follow/no-replace
// implementation is reviewed.
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
	full := rootPath
	if relative != "" {
		full = filepath.Join(rootPath, filepath.FromSlash(relative))
	}
	if err := checkNoSymlinkComponents(full); err != nil {
		return nil, err
	}
	info, err := os.Lstat(full)
	if err != nil {
		return nil, classifyOrganizeError(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, ErrSymlink
	}
	if !info.IsDir() {
		return nil, ErrSpecialFile
	}
	return os.Open(full)
}

func openRootDirectory(rootPath string) (*os.File, error) {
	if err := checkNoSymlinkComponents(rootPath); err != nil {
		return nil, err
	}
	info, err := os.Lstat(rootPath)
	if err != nil {
		return nil, classifyOrganizeError(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, ErrSymlink
	}
	if !info.IsDir() {
		return nil, ErrSpecialFile
	}
	return os.Open(rootPath)
}

func openExistingParent(rootPath, relative string) (*os.File, string, error) {
	parentRelative := filepath.Dir(relative)
	if parentRelative == "." {
		parentRelative = ""
	}
	parent, err := openExistingDirectory(rootPath, parentRelative)
	if err != nil {
		return nil, "", err
	}
	return parent, filepath.Base(relative), nil
}

func ensureDirectoryPath(rootPath, relative string, syncFn func(*os.File) error) (*os.File, error) {
	directory, _, err := ensureDirectoryPathWithCreated(rootPath, relative, syncFn)
	return directory, err
}

func ensureDirectoryPathWithCreated(rootPath, relative string, syncFn func(*os.File) error) (*os.File, []string, error) {
	if err := checkNoSymlinkComponents(rootPath); err != nil {
		return nil, nil, err
	}
	current := rootPath
	created := make([]string, 0)
	var prefix string
	for _, component := range relativeParts(relative) {
		if !safeComponent(component) {
			return nil, created, fmt.Errorf("%w: invalid destination directory component", ErrPathEscape)
		}
		if prefix == "" {
			prefix = component
		} else {
			prefix = filepath.Join(prefix, component)
		}
		next := filepath.Join(current, filepath.FromSlash(component))
		if err := checkNoSymlinkComponents(next); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, created, err
		}
		info, statErr := os.Lstat(next)
		if errors.Is(statErr, os.ErrNotExist) {
			if mkdirErr := os.Mkdir(next, 0o755); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
				return nil, created, classifyOrganizeError(mkdirErr)
			} else if mkdirErr == nil {
				created = append(created, filepath.ToSlash(prefix))
				if syncFn != nil {
					parent, openErr := os.Open(current)
					if openErr != nil {
						return nil, created, fmt.Errorf("%w: open containing directory: %v", ErrPublicationUnknown, openErr)
					}
					syncErr := syncFn(parent)
					_ = parent.Close()
					if syncErr != nil {
						return nil, created, fmt.Errorf("%w: sync containing directory: %v", ErrPublicationUnknown, syncErr)
					}
				}
				info, statErr = os.Lstat(next)
			}
		}
		if statErr != nil {
			return nil, created, classifyOrganizeError(statErr)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, created, ErrSymlink
		}
		if !info.IsDir() {
			return nil, created, ErrSpecialFile
		}
		current = next
	}
	directory, openErr := openExistingDirectory(rootPath, relative)
	return directory, created, openErr
}

func openExistingChild(parent *os.File, name string) (*os.File, fs.FileInfo, error) {
	if !safeComponent(name) {
		return nil, nil, ErrPathEscape
	}
	full := filepath.Join(parent.Name(), name)
	if err := checkNoSymlinkComponents(full); err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(full)
	if err != nil {
		return nil, nil, classifyOrganizeError(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, nil, ErrSymlink
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return nil, nil, ErrSpecialFile
	}
	file, err := os.Open(full)
	if err != nil {
		return nil, nil, classifyOrganizeError(err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if opened.Mode()&fs.ModeSymlink != 0 {
		_ = file.Close()
		return nil, nil, ErrSymlink
	}
	return file, opened, nil
}

func openChild(parent *os.File, name string) (*nodeHandle, error) {
	file, info, err := openExistingChild(parent, name)
	if err != nil {
		return nil, err
	}
	return &nodeHandle{file: file, name: name, info: info}, nil
}

func listDirectoryNames(directory *os.File) ([]string, error) {
	clone, err := os.Open(directory.Name())
	if err != nil {
		return nil, err
	}
	defer clone.Close()
	return clone.Readdirnames(-1)
}

func syncDirectory(directory *os.File) error { return directory.Sync() }

func filesystemDevice(info fs.FileInfo) (uint64, error) {
	if info == nil {
		return 0, fmt.Errorf("%w: filesystem device identity unavailable", ErrUnsupported)
	}
	return 0, fmt.Errorf("%w: filesystem device identity unavailable", ErrUnsupported)
}

func sameObject(left, right fs.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right)
}

func fileIdentity(info fs.FileInfo) string {
	return fmt.Sprintf("fallback:mode=%o:size=%d:mtime=%d:name=%s", info.Mode(), info.Size(), info.ModTime().UnixNano(), info.Name())
}

func classifyOrganizeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("filesystem object does not exist: %w", os.ErrNotExist)
	}
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("filesystem object already exists: %w", os.ErrExist)
	}
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("filesystem access denied: %w", os.ErrPermission)
	}
	return err
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

func renameNoReplace(_ *os.File, _ string, _ *os.File, _ string) (bool, error) {
	return false, fmt.Errorf("%w: target lacks reviewed no-replace organize primitive", ErrUnsupported)
}

func removeEntry(_ *os.File, _ string, _ bool) error {
	return fmt.Errorf("%w: target lacks reviewed organize deletion primitive", ErrUnsupported)
}

func checkNoSymlinkComponents(name string) error {
	clean := filepath.Clean(name)
	volume := filepath.VolumeName(clean)
	remainder := strings.TrimPrefix(clean, volume)
	current := volume
	if filepath.IsAbs(clean) {
		current += string(filepath.Separator)
	}
	for _, component := range strings.Split(strings.Trim(remainder, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return classifyOrganizeError(err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return ErrSymlink
		}
	}
	return nil
}
