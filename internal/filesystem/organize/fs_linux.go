//go:build linux

package organize

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
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

// moveOwned performs same-filesystem publication from an operation-private
// quarantine. renameat2 has name-based source semantics, so the source name
// is never sent directly to the destination. A raced replacement is first
// quarantined, rejected by identity validation, then restored without replace.
// The quarantine payload is kept below a mode-0700 directory, so cleanup is
// never issued against a media-root pathname that another media process can
// exchange. Regular files use AT_EMPTY_PATH for descriptor-bound destination
// linking; directories use the validated private entry with no-replace rename
// because Linux does not expose a descriptor-bound directory rename primitive.
func moveOwned(ctx context.Context, operationID string, ordinal int, source *nodeHandle, destinationParent *os.File, destinationName string, mapping ports.FileMap) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	quarantineName := privateEntryName("move", operationID, ordinal, mapping.Source.RelativePath)
	quarantineDirectory, err := createPrivateDirectoryAt(source.parent, quarantineName)
	if err != nil {
		return fmt.Errorf("%w: create move quarantine: %v", ErrReconciliationNeeded, err)
	}
	quarantineDirectoryInfo, infoErr := quarantineDirectory.Stat()
	if infoErr != nil {
		_ = quarantineDirectory.Close()
		return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: inspect move quarantine: %v", ErrReconciliationNeeded, infoErr)}
	}
	const quarantinePayload = "payload"
	if _, err := renameNoReplace(source.parent, source.name, quarantineDirectory, quarantinePayload); err != nil {
		_ = quarantineDirectory.Close()
		if cleanupErr := removePrivateDirectory(source.parent, quarantineName, quarantineDirectoryInfo); cleanupErr != nil {
			return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: quarantine move failed: %v; quarantine cleanup: %v", ErrReconciliationNeeded, err, cleanupErr)}
		}
		if isNotExist(err) {
			return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: source disappeared before quarantine", ErrReconciliationNeeded)}
		}
		if isExist(err) {
			return fmt.Errorf("%w: move quarantine appeared", ErrDestinationExists)
		}
		if isCrossDevice(err) {
			return ErrCrossDevice
		}
		return fmt.Errorf("%w: quarantine move source: %v", ErrPublicationUnknown, err)
	}

	quarantine, err := openChild(quarantineDirectory, quarantinePayload)
	if err != nil {
		_ = quarantineDirectory.Close()
		return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: open quarantined source: %v", ErrReconciliationNeeded, err)}
	}
	approved := sameObject(source.info, quarantine.info)
	if approved {
		approved = validateManifestNode(quarantine.file, quarantine.info, mapping.Source) == nil
	}
	if !approved {
		quarantine.close()
		return restoreQuarantine(source.parent, quarantineDirectory, quarantineName, quarantinePayload, source.name, quarantineDirectoryInfo, operationID, ordinal, ErrSourceChanged)
	}
	if err := ctx.Err(); err != nil {
		quarantine.close()
		return restoreQuarantine(source.parent, quarantineDirectory, quarantineName, quarantinePayload, source.name, quarantineDirectoryInfo, operationID, ordinal, err)
	}

	if mapping.Source.Type == domain.ManifestDirectory {
		published, publishErr := renameNoReplace(quarantineDirectory, quarantinePayload, destinationParent, destinationName)
		quarantine.close()
		if publishErr != nil {
			if published {
				_ = quarantineDirectory.Close()
				return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: directory move returned after publication: %v", ErrPublicationUnknown, publishErr)}
			}
			if isExist(publishErr) {
				return restoreQuarantine(source.parent, quarantineDirectory, quarantineName, quarantinePayload, source.name, quarantineDirectoryInfo, operationID, ordinal, ErrDestinationExists)
			}
			_ = quarantineDirectory.Close()
			return fmt.Errorf("%w: move directory quarantine: %v", ErrPublicationUnknown, publishErr)
		}
		if closeErr := quarantineDirectory.Close(); closeErr != nil {
			return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: close move quarantine: %v", ErrPublicationUnknown, closeErr)}
		}
		if cleanupErr := removePrivateDirectory(source.parent, quarantineName, quarantineDirectoryInfo); cleanupErr != nil {
			return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: remove move quarantine: %v", ErrPublicationUnknown, cleanupErr)}
		}
		return nil
	}
	if err := linkDescriptorNoReplace(quarantine.file, destinationParent, destinationName); err != nil {
		quarantine.close()
		if isExist(err) {
			return restoreQuarantine(source.parent, quarantineDirectory, quarantineName, quarantinePayload, source.name, quarantineDirectoryInfo, operationID, ordinal, ErrDestinationExists)
		}
		return fmt.Errorf("%w: descriptor-bound move publication: %v", ErrPublicationUnknown, err)
	}
	// Keep the approved descriptor open through publication. Before removing
	// its private quarantine name, ensure that name still refers to that same
	// inode. The private directory is mode-0700 and therefore cannot be
	// exchanged by a process that merely has access to the media root.
	quarantineInfo := quarantine.info
	quarantine.close()
	if err := removeOwnedQuarantine(operationID, ordinal, quarantineDirectory, quarantinePayload, quarantineInfo); err != nil {
		_ = quarantineDirectory.Close()
		return err
	}
	if err := quarantineDirectory.Sync(); err != nil {
		_ = quarantineDirectory.Close()
		return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: sync move quarantine: %v", ErrPublicationUnknown, err)}
	}
	if err := quarantineDirectory.Close(); err != nil {
		return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: close move quarantine: %v", ErrPublicationUnknown, err)}
	}
	if err := removePrivateDirectory(source.parent, quarantineName, quarantineDirectoryInfo); err != nil {
		return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: remove move quarantine: %v", ErrPublicationUnknown, err)}
	}
	return nil
}

// deleteOwned applies exact-manifest deletion inside an operation-private
// quarantine. The original source name is moved atomically before any
// recursive child is removed. A replacement at the public path is therefore
// restored untouched; no pathname unlink is issued against it.
func deleteOwnedNode(ctx context.Context, operationID string, ordinal int, parent *os.File, name string, entry domain.FileManifestEntry) ([]domain.FileManifestEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	quarantineName := privateEntryName("delete", operationID, ordinal, entry.RelativePath)
	quarantineDirectory, err := createPrivateDirectoryAt(parent, quarantineName)
	if err != nil {
		return nil, fmt.Errorf("%w: create delete quarantine: %v", ErrReconciliationNeeded, err)
	}
	quarantineDirectoryInfo, infoErr := quarantineDirectory.Stat()
	if infoErr != nil {
		_ = quarantineDirectory.Close()
		return nil, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: inspect delete quarantine: %v", ErrReconciliationNeeded, infoErr)}
	}
	const quarantinePayload = "payload"
	if _, err := renameNoReplace(parent, name, quarantineDirectory, quarantinePayload); err != nil {
		_ = quarantineDirectory.Close()
		if cleanupErr := removePrivateDirectory(parent, quarantineName, quarantineDirectoryInfo); cleanupErr != nil {
			return nil, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: quarantine delete failed: %v; quarantine cleanup: %v", ErrReconciliationNeeded, err, cleanupErr)}
		}
		if isNotExist(err) {
			return nil, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: selected source disappeared before quarantine", ErrReconciliationNeeded)}
		}
		if isExist(err) {
			return nil, fmt.Errorf("%w: delete quarantine appeared", ErrDestinationExists)
		}
		return nil, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: quarantine delete source: %v", ErrDeleteUnknown, err)}
	}

	quarantine, err := openChild(quarantineDirectory, quarantinePayload)
	if err != nil {
		_ = quarantineDirectory.Close()
		return nil, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: open quarantined delete target: %v", ErrReconciliationNeeded, err)}
	}
	if !sameObjectFromManifest(quarantine.info, entry) || validateManifestNode(quarantine.file, quarantine.info, entry) != nil {
		quarantine.close()
		return nil, restoreQuarantine(parent, quarantineDirectory, quarantineName, quarantinePayload, name, quarantineDirectoryInfo, operationID, ordinal, ErrSourceChanged)
	}
	if entry.Type != domain.ManifestDirectory {
		approvedInfo := quarantine.info
		quarantine.close()
		if err := removeOwnedQuarantine(operationID, ordinal, quarantineDirectory, quarantinePayload, approvedInfo); err != nil {
			_ = quarantineDirectory.Close()
			return nil, err
		}
		if err := quarantineDirectory.Sync(); err != nil {
			_ = quarantineDirectory.Close()
			return nil, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: sync delete quarantine: %v", ErrPublicationUnknown, err)}
		}
		if err := quarantineDirectory.Close(); err != nil {
			return nil, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: close delete quarantine: %v", ErrPublicationUnknown, err)}
		}
		if err := removePrivateDirectory(parent, quarantineName, quarantineDirectoryInfo); err != nil {
			return nil, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: remove delete quarantine: %v", ErrDeleteUnknown, err)}
		}
		return []domain.FileManifestEntry{entry}, nil
	}

	deleted := make([]domain.FileManifestEntry, 0, len(entry.Children))
	for _, child := range entry.Children {
		childName := path.Base(child.RelativePath)
		childDeleted, childErr := deleteOwnedNode(ctx, operationID, ordinal, quarantine.file, childName, child)
		deleted = append(deleted, childDeleted...)
		if childErr != nil {
			quarantine.close()
			return deleted, restoreQuarantine(parent, quarantineDirectory, quarantineName, quarantinePayload, name, quarantineDirectoryInfo, operationID, ordinal, childErr)
		}
		if err := syncDirectory(quarantine.file); err != nil {
			quarantine.close()
			return deleted, restoreQuarantine(parent, quarantineDirectory, quarantineName, quarantinePayload, name, quarantineDirectoryInfo, operationID, ordinal, fmt.Errorf("%w: child directory sync: %v", ErrPublicationUnknown, err))
		}
	}
	remaining, err := listDirectoryNames(quarantine.file)
	approvedInfo := quarantine.info
	quarantine.close()
	if err != nil {
		return deleted, restoreQuarantine(parent, quarantineDirectory, quarantineName, quarantinePayload, name, quarantineDirectoryInfo, operationID, ordinal, fmt.Errorf("%w: directory scope read-back failed: %v", ErrReconciliationNeeded, err))
	}
	if len(remaining) != 0 {
		return deleted, restoreQuarantine(parent, quarantineDirectory, quarantineName, quarantinePayload, name, quarantineDirectoryInfo, operationID, ordinal, fmt.Errorf("%w: directory %q gained an unselected child", ErrSourceChanged, entry.RelativePath))
	}
	if err := removeOwnedQuarantine(operationID, ordinal, quarantineDirectory, quarantinePayload, approvedInfo); err != nil {
		_ = quarantineDirectory.Close()
		return deleted, err
	}
	if err := quarantineDirectory.Sync(); err != nil {
		_ = quarantineDirectory.Close()
		return deleted, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: sync delete quarantine: %v", ErrPublicationUnknown, err)}
	}
	if err := quarantineDirectory.Close(); err != nil {
		return deleted, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: close delete quarantine: %v", ErrPublicationUnknown, err)}
	}
	if err := removePrivateDirectory(parent, quarantineName, quarantineDirectoryInfo); err != nil {
		return deleted, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: remove delete quarantine: %v", ErrDeleteUnknown, err)}
	}
	return append(deleted, entry), nil
}

func sameObjectFromManifest(info fs.FileInfo, entry domain.FileManifestEntry) bool {
	return info != nil && info.Size() == entry.Size && info.Mode().IsRegular() == (entry.Type != domain.ManifestDirectory)
}

func restoreQuarantine(parent, quarantineDirectory *os.File, quarantineName, payloadName, sourceName string, quarantineDirectoryInfo fs.FileInfo, operationID string, ordinal int, cause error) error {
	if _, err := renameNoReplace(quarantineDirectory, payloadName, parent, sourceName); err != nil {
		_ = quarantineDirectory.Close()
		return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: quarantine restore failed: %v (original cause: %v)", ErrReconciliationNeeded, err, cause)}
	}
	if err := syncDirectory(parent); err != nil {
		_ = quarantineDirectory.Close()
		return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: quarantine restore parent sync failed: %v (original cause: %v)", ErrReconciliationNeeded, err, cause)}
	}
	if err := quarantineDirectory.Sync(); err != nil {
		_ = quarantineDirectory.Close()
		return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: quarantine restore sync failed: %v (original cause: %v)", ErrReconciliationNeeded, err, cause)}
	}
	if err := quarantineDirectory.Close(); err != nil {
		return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: quarantine restore close failed: %v (original cause: %v)", ErrReconciliationNeeded, err, cause)}
	}
	if err := removePrivateDirectory(parent, quarantineName, quarantineDirectoryInfo); err != nil {
		return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: quarantine cleanup failed: %v (original cause: %v)", ErrReconciliationNeeded, err, cause)}
	}
	return fmt.Errorf("%w: %v", cause, ErrSourceChanged)
}

func removePrivateDirectory(parent *os.File, name string, approved fs.FileInfo) error {
	current, err := openChild(parent, name)
	if err != nil {
		return &UncertainError{OperationID: "quarantine", Ordinal: 0, Cause: fmt.Errorf("%w: private quarantine disappeared before removal: %v", ErrReconciliationNeeded, err)}
	}
	owned := sameObject(approved, current.info) && current.info.IsDir()
	current.close()
	if !owned {
		return &UncertainError{OperationID: "quarantine", Ordinal: 0, Cause: fmt.Errorf("%w: private quarantine was replaced before removal", ErrReconciliationNeeded)}
	}
	if err := removeEntry(parent, name, true); err != nil {
		return &UncertainError{OperationID: "quarantine", Ordinal: 0, Cause: fmt.Errorf("%w: remove private quarantine: %v", ErrDeleteUnknown, err)}
	}
	return nil
}

func removeOwnedQuarantine(operationID string, ordinal int, parent *os.File, name string, approved fs.FileInfo) error {
	current, err := openChild(parent, name)
	if err != nil {
		return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: quarantine payload disappeared before removal: %v", ErrReconciliationNeeded, err)}
	}
	owned := sameObject(approved, current.info)
	current.close()
	if !owned {
		return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: quarantine payload was replaced before removal", ErrReconciliationNeeded)}
	}
	if err := removeEntry(parent, name, approved.IsDir()); err != nil {
		return &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: remove quarantine payload: %v", ErrDeleteUnknown, err)}
	}
	return nil
}

func linkDescriptorNoReplace(source, destinationParent *os.File, destinationName string) error {
	if source == nil {
		return fmt.Errorf("%w: missing descriptor", ErrSourceChanged)
	}
	err := unix.Linkat(int(source.Fd()), "", int(destinationParent.Fd()), destinationName, unix.AT_EMPTY_PATH)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.ENOSYS) {
		return fmt.Errorf("%w: descriptor-bound link is unavailable: %v", ErrUnsupported, err)
	}
	return classifyOrganizeError(err)
}

func createPrivateDirectory(rootPath, name string) (*os.File, error) {
	root, err := openRootDirectory(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	directory, err := createPrivateDirectoryAt(root, name)
	if err != nil {
		return nil, err
	}
	if err := syncDirectory(root); err != nil {
		_ = directory.Close()
		return nil, fmt.Errorf("%w: sync private directory parent: %v", ErrPublicationUnknown, err)
	}
	return directory, nil
}

// createPrivateDirectoryAt creates a mode-0700 namespace below a retained
// directory descriptor. Move/delete quarantines use this instead of placing
// the payload pathname directly beside user media, so a process that can only
// access the media root cannot exchange the final cleanup entry.
func createPrivateDirectoryAt(parent *os.File, name string) (*os.File, error) {
	if !safeComponent(name) {
		return nil, ErrPathEscape
	}
	if err := unix.Mkdirat(int(parent.Fd()), name, 0o700); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return nil, fmt.Errorf("%w: private directory already exists", ErrDestinationExists)
		}
		return nil, classifyOrganizeError(err)
	}
	directory, err := openDirectoryChild(parent, name)
	if err != nil {
		return nil, err
	}
	return directory, nil
}

func createFileNoReplace(parent *os.File, name string, mode os.FileMode) (*os.File, error) {
	if !safeComponent(name) {
		return nil, ErrPathEscape
	}
	flags := unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fd, err := unix.Openat(int(parent.Fd()), name, flags, uint32(mode.Perm()))
	if err != nil {
		if errors.Is(err, unix.EEXIST) {
			return nil, fmt.Errorf("%w: file already exists", ErrDestinationExists)
		}
		return nil, classifyOrganizeError(err)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("create file: invalid descriptor")
	}
	return file, nil
}

func makeDirectoryNoReplace(parent *os.File, name string, mode os.FileMode) error {
	if err := unix.Mkdirat(int(parent.Fd()), name, uint32(mode.Perm())); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return fmt.Errorf("%w: directory already exists", ErrDestinationExists)
		}
		return classifyOrganizeError(err)
	}
	return nil
}
