//go:build !(darwin || linux)

package observe

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/guilycst/managerr/internal/domain"
)

// Non-Unix platforms lack the descriptor-relative flags used by the Unix
// implementation. Keep the package portable, but report no-follow as unknown
// so callers cannot mistake this fallback for a race-safe confinement proof.
func pathIsAbsolute(value string) bool { return filepath.IsAbs(value) }

func cleanHostPath(value string) string { return filepath.Clean(value) }

func openConstrainedDirectory(rootPath, relativePath string) (*os.File, error) {
	if relativePath == "" {
		return openChecked(rootPath, true)
	}
	return openChecked(filepath.Join(append([]string{rootPath}, splitRelative(relativePath)...)...), true)
}

func openConstrainedTarget(rootPath, relativePath string) (*os.File, os.FileInfo, error) {
	if relativePath == "" {
		return nil, nil, fmt.Errorf("%w", ErrRootTarget)
	}
	file, err := openChecked(filepath.Join(append([]string{rootPath}, splitRelative(relativePath)...)...), false)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

func openConstrainedChild(directory *os.File, name string) (*os.File, os.FileInfo, error) {
	if strings.ContainsAny(name, `/\\`) || name == "." || name == ".." {
		return nil, nil, fmt.Errorf("%w: invalid directory component", ErrPathEscape)
	}
	file, err := openChecked(filepath.Join(directory.Name(), name), false)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

func openChecked(name string, directoryOnly bool) (*os.File, error) {
	if err := checkNoSymlinkComponents(name); err != nil {
		return nil, err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return nil, classifyOtherOpenError(err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, ErrSymlink
	}
	if directoryOnly && !info.IsDir() {
		return nil, fmt.Errorf("%w: path component is not a directory", ErrSpecialFile)
	}
	if !directoryOnly && !info.IsDir() && !info.Mode().IsRegular() {
		return nil, ErrSpecialFile
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, classifyOtherOpenError(err)
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	latest, err := os.Lstat(name)
	if err != nil {
		file.Close()
		return nil, classifyOtherOpenError(err)
	}
	if latest.Mode()&fs.ModeSymlink != 0 || opened.Mode()&fs.ModeSymlink != 0 {
		file.Close()
		return nil, ErrSymlink
	}
	if directoryOnly && !opened.IsDir() {
		file.Close()
		return nil, fmt.Errorf("%w: path component is not a directory", ErrSpecialFile)
	}
	return file, nil
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
			return classifyOtherOpenError(err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return ErrSymlink
		}
	}
	return nil
}

func splitRelative(relativePath string) []string {
	if relativePath == "" {
		return nil
	}
	return strings.Split(relativePath, "/")
}

func writableDirectory(file *os.File) bool {
	return false
}

func writableFile(file *os.File) bool {
	return false
}

func writablePath(rootPath, relativePath string, directory bool) bool {
	return false
}

func noFollowCapability() domain.CapabilityState {
	return domain.CapabilityUnknown
}

func classifyOtherOpenError(err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("%w: filesystem access denied", os.ErrPermission)
	}
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("filesystem object does not exist: %w", os.ErrNotExist)
	}
	return err
}
