//go:build !(darwin || linux)

package placement

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const placementWritesSupported = false

// This fallback retains root-relative and no-symlink checks where the target
// platform lacks the Unix descriptor-relative primitives. The action remains
// usable for ordinary paths, but platform capability evidence must keep
// no-follow/atomicity unknown until a target-specific verification exists.

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
	name := rootPath
	if relative != "" {
		name = filepath.Join(rootPath, filepath.FromSlash(relative))
	}
	if err := checkNoSymlinkComponents(name); err != nil {
		return nil, err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return nil, classifyPlacementError(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, ErrSymlink
	}
	if !info.IsDir() {
		return nil, ErrSpecialFile
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, classifyPlacementError(err)
	}
	return file, nil
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
	if err := checkNoSymlinkComponents(rootPath); err != nil {
		return nil, nil, err
	}
	current := rootPath
	created := make([]string, 0)
	var prefix string
	for _, component := range relativeParts(relative) {
		if component == "" || component == "." || component == ".." || strings.ContainsAny(component, `/\\`) {
			return nil, created, fmt.Errorf("%w: invalid destination directory component", ErrPathEscape)
		}
		if prefix == "" {
			prefix = component
		} else {
			prefix = path.Join(prefix, component)
		}
		parentPath := current
		current = filepath.Join(current, filepath.FromSlash(component))
		if err := checkNoSymlinkComponents(current); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, created, err
		}
		info, statErr := os.Lstat(current)
		createdHere := false
		if errors.Is(statErr, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil {
				if !errors.Is(err, os.ErrExist) {
					return nil, created, classifyPlacementError(err)
				}
			} else {
				createdHere = true
			}
			if createdHere {
				created = append(created, prefix)
				if syncFn != nil {
					parent, err := os.Open(parentPath)
					if err != nil {
						return nil, created, fmt.Errorf("%w: open containing directory before creating %q: %w", ErrPublicationUnknown, prefix, err)
					}
					syncErr := syncFn(parent)
					closeErr := parent.Close()
					if syncErr != nil {
						return nil, created, fmt.Errorf("%w: sync containing directory before creating %q: %w", ErrPublicationUnknown, prefix, syncErr)
					}
					if closeErr != nil {
						return nil, created, fmt.Errorf("%w: close containing directory after creating %q: %w", ErrPublicationUnknown, prefix, closeErr)
					}
				}
			}
			info, statErr = os.Lstat(current)
		}
		if statErr != nil {
			return nil, created, classifyPlacementError(statErr)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return nil, created, ErrSymlink
		}
		if !info.IsDir() {
			return nil, created, ErrSpecialFile
		}
	}
	directory, err := openExistingDirectory(rootPath, relative)
	return directory, created, err
}

func syncDirectoryPath(rootPath, relative string) error {
	directory, err := openExistingDirectory(rootPath, relative)
	if err != nil {
		return err
	}
	defer directory.Close()
	return syncDirectory(directory)
}

func openExistingChild(parent *os.File, name string) (*os.File, fs.FileInfo, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\\`) {
		return nil, nil, ErrPathEscape
	}
	full := filepath.Join(parent.Name(), filepath.FromSlash(name))
	if err := checkNoSymlinkComponents(full); err != nil {
		return nil, nil, err
	}
	info, err := os.Lstat(full)
	if err != nil {
		return nil, nil, classifyPlacementError(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, nil, ErrSymlink
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return nil, nil, ErrSpecialFile
	}
	file, err := os.Open(full)
	if err != nil {
		return nil, nil, classifyPlacementError(err)
	}
	openedInfo, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if openedInfo.Mode()&fs.ModeSymlink != 0 {
		file.Close()
		return nil, nil, ErrSymlink
	}
	return file, openedInfo, nil
}

func createExclusiveChild(parent *os.File, name string) (*os.File, error) {
	full := filepath.Join(parent.Name(), filepath.FromSlash(name))
	if err := checkNoSymlinkComponents(parent.Name()); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, classifyPlacementError(err)
	}
	return file, nil
}

func publishNoReplace(stage, parent *os.File, stageName, destinationName string) (bool, error) {
	// Writes are disabled on this target, but retain the descriptor-bound
	// staging contract for any future target-specific implementation.
	stage := filepath.Join(parent.Name(), filepath.FromSlash(stageName))
	destination := filepath.Join(parent.Name(), filepath.FromSlash(destinationName))
	if err := os.Link(stage, destination); err != nil {
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
	source := filepath.Join(sourceParent.Name(), filepath.FromSlash(sourceName))
	destination := filepath.Join(destinationParent.Name(), filepath.FromSlash(destinationName))
	if err := os.Link(source, destination); err != nil {
		return false, classifyPlacementError(err)
	}
	return true, nil
}

func removeEmptyDirectory(rootPath, relative string) error {
	if relative == "" {
		return ErrRootTarget
	}
	return classifyPlacementError(os.Remove(filepath.Join(rootPath, filepath.FromSlash(relative))))
}

func syncDirectory(directory *os.File) error {
	// Some non-Unix filesystems do not expose directory sync. Keep the
	// conservative unknown result at the caller rather than pretending it is
	// durable; a successful call here is still useful where supported.
	return directory.Sync()
}

func listDirectoryNames(directory *os.File) ([]string, error) {
	clone, err := os.Open(directory.Name())
	if err != nil {
		return nil, err
	}
	defer clone.Close()
	return clone.Readdirnames(-1)
}

func sameObject(left, right fs.FileInfo) bool { return os.SameFile(left, right) }

func fileIdentity(info fs.FileInfo) string {
	return fmt.Sprintf("fallback:mode=%o:size=%d:mtime=%d:name=%s", info.Mode(), info.Size(), info.ModTime().UnixNano(), info.Name())
}

func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }

func isExist(err error) bool { return errors.Is(err, os.ErrExist) }

func isCrossDevice(_ error) bool { return false }

func classifyPlacementError(err error) error {
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
			return classifyPlacementError(err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return ErrSymlink
		}
	}
	return nil
}
