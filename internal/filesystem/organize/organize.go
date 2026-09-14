// Package organize implements the filesystem actions whose semantics differ
// from placement. It owns same-filesystem move/rename, exact permanent delete
// and the explicitly requested cross-device copy/verify/delete composition.
//
// The package accepts only root-relative, identity-bound manifests from the
// filesystem action ports. It never reaches a download client or an Arr
// adapter; linked-client coordination is an ordered workflow concern.
package organize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/filesystem/placement"
	"github.com/guilycst/mastarr/internal/ports"
)

var (
	ErrRootNotConfigured    = errors.New("filesystem organize root is not configured")
	ErrRootTarget           = errors.New("filesystem organize root target is not allowed")
	ErrPathEscape           = errors.New("filesystem organize path escapes configured root")
	ErrSymlink              = errors.New("filesystem organize symlink is not allowed")
	ErrSpecialFile          = errors.New("filesystem organize special file is not allowed")
	ErrInvalidPlan          = errors.New("filesystem organize plan is invalid")
	ErrReadOnly             = errors.New("filesystem organize destination is read-only")
	ErrUnsupported          = errors.New("filesystem organize operation is unsupported")
	ErrSourceChanged        = errors.New("filesystem organize source changed after approval")
	ErrDestinationConflict  = errors.New("filesystem organize destination conflicts with approved content")
	ErrDestinationExists    = errors.New("filesystem organize destination appeared during publication")
	ErrCrossDevice          = errors.New("filesystem organize move crosses filesystems")
	ErrSameFilesystem       = errors.New("filesystem organize composition requires different filesystems")
	ErrDeleteUnknown        = errors.New("filesystem organize deletion result is unknown")
	ErrReconciliationNeeded = errors.New("filesystem organize effect requires read-only reconciliation")
	ErrPublicationUnknown   = errors.New("filesystem organize publication durability is unknown")

	// UncertainError is intentionally the same shape as placement's error. The
	// alias lets a durable executor carry one operation/ordinal contract across
	// both filesystem action implementations without importing storage here.
	ErrOperationUnknown = placement.ErrPublicationUnknown
)

// UncertainError identifies an effect that may have become visible before its
// read-back or durability evidence completed.
type UncertainError = placement.UncertainError

// Root is one configured storage root. Paths are constructor-only values;
// action requests contain only root-relative domain targets.
type Root struct {
	ID           domain.ConfigID
	Path         string
	ReadOnly     bool
	Capabilities []domain.Capability
}

// Options configures the organizer. Placement is optional; when absent New
// constructs the reviewed placement implementation from the same roots for
// explicit cross-device copy/verify/delete composition.
type Options struct {
	Placement     *placement.Placer
	BufferSize    int
	Clock         func() time.Time
	SyncDirectory func(*os.File) error

	// These seams are package-private so synthetic tests can schedule a
	// replacement between identity validation and the target-specific syscall.
	// Production callers cannot install them.
	beforeMovePublication   func()
	beforeDeletePublication func()
	// afterDestinationGuardVerification is package-private so synthetic tests
	// can mutate the visible destination after its final pre-delete read-back.
	// Production callers cannot install it.
	afterDestinationGuardVerification func()
}

// CrossDeviceMoveRequest is deliberately distinct from FilesystemMoveRequest
// so a cross-device copy/verify/delete cannot be an accidental Move fallback.
type CrossDeviceMoveRequest struct {
	Files []ports.FileMap
}

func (request CrossDeviceMoveRequest) Validate() error {
	return (ports.FilesystemMoveRequest{Files: request.Files}).Validate()
}

// Organizer implements the filesystem action port. Trash and restore remain
// owned by the later trash lane; Copy and Hardlink delegate to the reviewed
// placement implementation and retain their explicit transfer semantics.
type Organizer struct {
	roots map[domain.ConfigID]Root
	opts  Options
	place *placement.Placer
}

var _ ports.FilesystemActionPort = (*Organizer)(nil)

// New validates root identity and does not touch the filesystem until an
// action is requested.
func New(roots []Root, options Options) (*Organizer, error) {
	if options.Clock == nil {
		options.Clock = func() time.Time { return time.Now().UTC() }
	}
	configured := make(map[domain.ConfigID]Root, len(roots))
	physicalRoots := make([]string, 0, len(roots))
	placementRoots := make([]placement.Root, 0, len(roots))
	for _, root := range roots {
		if !root.ID.Valid() {
			return nil, fmt.Errorf("%w: invalid root id", ErrRootNotConfigured)
		}
		if strings.IndexByte(root.Path, 0) >= 0 || !filepath.IsAbs(root.Path) {
			return nil, fmt.Errorf("%w: root path must be absolute", ErrRootNotConfigured)
		}
		cleaned := filepath.Clean(root.Path)
		if cleaned != root.Path {
			return nil, fmt.Errorf("%w: root path must be canonical", ErrRootNotConfigured)
		}
		if _, exists := configured[root.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate root id %q", ErrRootNotConfigured, root.ID)
		}
		physicalPath := effectiveRootPath(root.Path)
		for _, existingPath := range physicalRoots {
			if rootPathsConflict(existingPath, physicalPath) {
				return nil, fmt.Errorf("%w: roots %q and %q overlap physically", ErrRootNotConfigured, existingPath, physicalPath)
			}
		}
		physicalRoots = append(physicalRoots, physicalPath)
		root.Capabilities = append([]domain.Capability(nil), root.Capabilities...)
		configured[root.ID] = root
		placementRoots = append(placementRoots, placement.Root{
			ID: root.ID, Path: root.Path, ReadOnly: root.ReadOnly,
			Capabilities: append([]domain.Capability(nil), root.Capabilities...),
		})
	}
	place := options.Placement
	if place == nil {
		var err error
		place, err = placement.New(placementRoots, placement.Options{BufferSize: options.BufferSize})
		if err != nil {
			return nil, err
		}
	}
	return &Organizer{roots: configured, opts: options, place: place}, nil
}

// NewFromStorageRoots adapts the frozen storage-root snapshot without making
// configuration a dependency of this package.
func NewFromStorageRoots(roots []domain.StorageRoot, options Options) (*Organizer, error) {
	configured := make([]Root, 0, len(roots))
	for _, root := range roots {
		configured = append(configured, Root{
			ID: root.ID, Path: root.Path, ReadOnly: root.ReadOnly,
			Capabilities: append([]domain.Capability(nil), root.Capabilities...),
		})
	}
	return New(configured, options)
}

func (organizer *Organizer) Copy(ctx context.Context, request ports.FilesystemCopyRequest) (ports.FilesystemEffect, error) {
	return organizer.place.Copy(ctx, request)
}

func (organizer *Organizer) Hardlink(ctx context.Context, request ports.FilesystemHardlinkRequest) (ports.FilesystemEffect, error) {
	return organizer.place.Hardlink(ctx, request)
}

func (organizer *Organizer) Trash(context.Context, ports.FilesystemTrashRequest) (ports.FilesystemEffect, error) {
	return ports.FilesystemEffect{}, ErrUnsupported
}

func (organizer *Organizer) Restore(context.Context, ports.FilesystemRestoreRequest) (ports.FilesystemEffect, error) {
	return ports.FilesystemEffect{}, ErrUnsupported
}

// Move performs only a same-filesystem no-replace move. A cross-device plan
// returns ErrCrossDevice without changing either root; callers must invoke
// CopyVerifyDelete with a separately approved composition.
func (organizer *Organizer) Move(ctx context.Context, request ports.FilesystemMoveRequest) (ports.FilesystemEffect, error) {
	operationID, err := organizer.newOperationID()
	if err != nil {
		return ports.FilesystemEffect{}, err
	}
	return organizer.MoveWithOperation(ctx, operationID, request)
}

func (organizer *Organizer) MoveWithOperation(ctx context.Context, operationID string, request ports.FilesystemMoveRequest) (ports.FilesystemEffect, error) {
	return organizer.moveWithOperation(ctx, operationID, "move", request)
}

// Rename uses the same exact, no-replace primitive as Move. Keeping a
// separate method preserves the action-level distinction for workflow and
// evidence without weakening the shared path and identity checks.
func (organizer *Organizer) Rename(ctx context.Context, request ports.FilesystemRenameRequest) (ports.FilesystemEffect, error) {
	operationID, err := organizer.newOperationID()
	if err != nil {
		return ports.FilesystemEffect{}, err
	}
	return organizer.RenameWithOperation(ctx, operationID, request)
}

func (organizer *Organizer) RenameWithOperation(ctx context.Context, operationID string, request ports.FilesystemRenameRequest) (ports.FilesystemEffect, error) {
	return organizer.moveWithOperation(ctx, operationID, "rename", ports.FilesystemMoveRequest{Files: request.Files})
}

// CopyVerifyDelete is the explicit cross-device composition. It refuses a
// same-filesystem request and never turns Move into an implicit copy.
func (organizer *Organizer) CopyVerifyDelete(ctx context.Context, request CrossDeviceMoveRequest) (ports.FilesystemEffect, error) {
	operationID, err := organizer.newOperationID()
	if err != nil {
		return ports.FilesystemEffect{}, err
	}
	return organizer.CopyVerifyDeleteWithOperation(ctx, operationID, request)
}

func (organizer *Organizer) CopyVerifyDeleteWithOperation(ctx context.Context, operationID string, request CrossDeviceMoveRequest) (ports.FilesystemEffect, error) {
	if err := validateOperationID(operationID); err != nil {
		return ports.FilesystemEffect{}, err
	}
	if err := request.Validate(); err != nil {
		return ports.FilesystemEffect{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	if err := (ports.FilesystemCopyRequest{Files: request.Files}).Validate(); err != nil {
		return ports.FilesystemEffect{}, fmt.Errorf("%w: cross-device copy: %v", ErrInvalidPlan, err)
	}
	copyOperationID, err := derivedOperationID(operationID, "copy")
	if err != nil {
		return ports.FilesystemEffect{}, err
	}
	deleteOperationID, err := derivedOperationID(operationID, "delete")
	if err != nil {
		return ports.FilesystemEffect{}, err
	}
	plans, err := organizer.prepareCrossDevice(ctx, request.Files)
	if err != nil {
		return ports.FilesystemEffect{}, err
	}
	effect := ports.FilesystemEffect{ObservedAt: organizer.now()}
	pending := make([]ports.FileMap, 0, len(plans))
	for ordinal, plan := range plans {
		if plan.alreadySatisfied {
			effect = appendEffect(effect, plan.mapping.Source, fileEvidence(operationID, ordinal, plan.mapping.Source, "already_satisfied")...)
			continue
		}
		pending = append(pending, plan.mapping)
	}
	if len(pending) == 0 {
		effect.Evidence = append(effect.Evidence, "composition=copy_verify_delete")
		effect.ObservedAt = organizer.now()
		return effect, nil
	}
	copyRequest := ports.FilesystemCopyRequest{Files: pending}
	copyEffect, err := organizer.place.CopyWithOperation(ctx, copyOperationID, copyRequest)
	if err != nil {
		return copyEffect, err
	}
	// Placement already performs a verified read-back before returning, but
	// this explicit read-only reconciliation is retained at the composition
	// boundary so delete can never follow an ambiguous copy result.
	if _, err := organizer.place.ReconcileCopy(ctx, copyOperationID, copyRequest); err != nil {
		return copyEffect, &UncertainError{OperationID: operationID, Ordinal: 0, Cause: fmt.Errorf("%w: copied destination cannot be reconciled: %v", ErrReconciliationNeeded, err)}
	}
	guards := make(map[string]*destinationGuard, len(pending))
	createdGuards := make([]*destinationGuard, 0, len(pending))
	for ordinal, mapping := range pending {
		guard, guardErr := organizer.createDestinationGuard(ctx, operationID, ordinal, mapping)
		if guardErr != nil {
			if cleanupErr := cleanupDestinationGuards(createdGuards); cleanupErr != nil {
				return copyEffect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination protection: %v; cleanup: %v", ErrReconciliationNeeded, guardErr, cleanupErr)}
			}
			return copyEffect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination protection: %v", ErrReconciliationNeeded, guardErr)}
		}
		guards[fileMapKey(mapping)] = guard
		createdGuards = append(createdGuards, guard)
	}
	deleteFiles := make([]domain.FileManifestEntry, 0, len(pending))
	for _, mapping := range pending {
		deleteFiles = append(deleteFiles, mapping.Source)
	}
	deleteEffect, err := organizer.deleteWithProtection(ctx, deleteOperationID, ports.FilesystemDeleteRequest{Files: deleteFiles}, guards)
	copyEffect.Affected = append(effect.Affected, copyEffect.Affected...)
	copyEffect.Evidence = append(effect.Evidence, copyEffect.Evidence...)
	copyEffect.Evidence = append(copyEffect.Evidence, "composition=copy_verify_delete")
	copyEffect.Evidence = append(copyEffect.Evidence, deleteEffect.Evidence...)
	if err != nil {
		return copyEffect, err
	}
	copyEffect.Outcome = domain.OutcomeApplied
	return copyEffect, nil
}

// MoveAcrossDevices is a descriptive alias for callers that model the
// composition as a move while retaining the explicit request type.
func (organizer *Organizer) MoveAcrossDevices(ctx context.Context, request CrossDeviceMoveRequest) (ports.FilesystemEffect, error) {
	return organizer.CopyVerifyDelete(ctx, request)
}

// Delete permanently removes exactly the selected live manifest. It is an
// explicit API action; this package does not implement trash retention.
func (organizer *Organizer) Delete(ctx context.Context, request ports.FilesystemDeleteRequest) (ports.FilesystemEffect, error) {
	operationID, err := organizer.newOperationID()
	if err != nil {
		return ports.FilesystemEffect{}, err
	}
	return organizer.DeleteWithOperation(ctx, operationID, request)
}

func (organizer *Organizer) DeleteWithOperation(ctx context.Context, operationID string, request ports.FilesystemDeleteRequest) (ports.FilesystemEffect, error) {
	return organizer.deleteWithProtection(ctx, operationID, request, nil)
}

func (organizer *Organizer) deleteWithProtection(ctx context.Context, operationID string, request ports.FilesystemDeleteRequest, guards map[string]*destinationGuard) (ports.FilesystemEffect, error) {
	if err := validateOperationID(operationID); err != nil {
		return ports.FilesystemEffect{}, err
	}
	if err := request.Validate(); err != nil {
		return ports.FilesystemEffect{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	plans, err := organizer.prepareDelete(ctx, request.Files)
	if err != nil {
		return ports.FilesystemEffect{}, err
	}
	effect := ports.FilesystemEffect{ObservedAt: organizer.now()}
	for ordinal, plan := range plans {
		if err := ctx.Err(); err != nil {
			return effect, err
		}
		if plan.alreadySatisfied {
			effect = appendEffect(effect, plan.entry, fileEvidence(operationID, ordinal, plan.entry, "already_satisfied")...)
			continue
		}
		var guard *destinationGuard
		if guards != nil {
			guard = guards[fileManifestKey(plan.entry)]
		}
		deleted, err := organizer.deleteOne(ctx, operationID, ordinal, plan.entry, guard)
		if err != nil {
			effect.Affected = append(effect.Affected, deleted...)
			effect.ObservedAt = organizer.now()
			return effect, err
		}
		effect = appendEffect(effect, plan.entry, fileEvidence(operationID, ordinal, plan.entry, "applied")...)
		if guard != nil {
			if guardErr := guard.verify(ctx); guardErr != nil {
				if restoreErr := guard.restore(ctx); restoreErr != nil {
					return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination changed after source removal: %v; restore: %v", ErrReconciliationNeeded, guardErr, restoreErr)}
				}
				return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination was restored after source removal: %v", ErrReconciliationNeeded, guardErr)}
			}
			if cleanupErr := guard.cleanup(); cleanupErr != nil {
				return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination protection cleanup: %v", ErrReconciliationNeeded, cleanupErr)}
			}
		}
	}
	effect.ObservedAt = organizer.now()
	return effect, nil
}

// ReconcileMove is read-only and proves the destination identity plus absence
// of the selected old path. It is the safe restart path after an uncertain
// rename syscall.
func (organizer *Organizer) ReconcileMove(ctx context.Context, operationID string, request ports.FilesystemMoveRequest) (ports.FilesystemEffect, error) {
	return organizer.reconcileMove(ctx, operationID, request)
}

func (organizer *Organizer) ReconcileRename(ctx context.Context, operationID string, request ports.FilesystemRenameRequest) (ports.FilesystemEffect, error) {
	return organizer.reconcileMove(ctx, operationID, ports.FilesystemMoveRequest{Files: request.Files})
}

// ReconcileDelete is read-only. An absent selected path is already satisfied;
// a present identity-bound path remains unresolved and is never deleted here.
func (organizer *Organizer) ReconcileDelete(ctx context.Context, operationID string, request ports.FilesystemDeleteRequest) (ports.FilesystemEffect, error) {
	if err := validateOperationID(operationID); err != nil {
		return ports.FilesystemEffect{}, err
	}
	if err := request.Validate(); err != nil {
		return ports.FilesystemEffect{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	effect := ports.FilesystemEffect{ObservedAt: organizer.now()}
	for ordinal, entry := range request.Files {
		if err := ctx.Err(); err != nil {
			return effect, err
		}
		node, err := organizer.openEntry(entry)
		if err != nil {
			if isNotExist(err) {
				effect = appendEffect(effect, entry, fileEvidence(operationID, ordinal, entry, "already_satisfied")...)
				continue
			}
			return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: delete source read-back: %v", ErrReconciliationNeeded, err)}
		}
		validationErr := validateManifestNode(node.file, node.info, entry)
		node.close()
		if validationErr != nil {
			return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: delete target changed: %v", ErrReconciliationNeeded, validationErr)}
		}
		return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: selected delete target remains present", ErrReconciliationNeeded)}
	}
	effect.ObservedAt = organizer.now()
	return effect, nil
}

type movePlan struct {
	mapping          ports.FileMap
	alreadySatisfied bool
}

type deletePlan struct {
	entry            domain.FileManifestEntry
	alreadySatisfied bool
}

// destinationGuard is a same-filesystem, operation-owned witness for a
// cross-device copy. It holds a descriptor-backed hardlink tree under a
// private root while source removal is in flight. A destination pathname can
// therefore disappear without destroying the only verified copy.
type destinationGuard struct {
	organizer *Organizer
	mapping   ports.FileMap
	root      Root
	name      string
	directory *os.File
	cleaned   bool
}

func cleanupDestinationGuards(guards []*destinationGuard) error {
	var firstErr error
	for _, guard := range guards {
		if err := guard.cleanup(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func fileMapKey(mapping ports.FileMap) string {
	return mapping.Source.RootID.String() + "\x00" + mapping.Source.RelativePath
}

func fileManifestKey(entry domain.FileManifestEntry) string {
	return entry.RootID.String() + "\x00" + entry.RelativePath
}

func (organizer *Organizer) createDestinationGuard(ctx context.Context, operationID string, ordinal int, mapping ports.FileMap) (*destinationGuard, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := organizer.root(mapping.Destination.RootID)
	if err != nil {
		return nil, err
	}
	destination, err := organizer.openEntry(destinationAsManifest(mapping))
	if err != nil {
		return nil, fmt.Errorf("open destination: %w", err)
	}
	defer destination.close()
	if err := validateCopiedNode(ctx, destination.file, destination.info, mapping.Source, organizer.copyBufferSize()); err != nil {
		return nil, fmt.Errorf("validate destination: %w", err)
	}
	name := privateEntryName("copy-guard", operationID, ordinal, mapping.Destination.RelativePath)
	directory, err := createPrivateDirectory(root.Path, name)
	if err != nil {
		return nil, err
	}
	guard := &destinationGuard{organizer: organizer, mapping: mapping, root: root, name: name, directory: directory}
	if err := copyGuardEntry(ctx, destination.file, destination.info, mapping.Source, directory, "payload", organizer.copyBufferSize(), organizer.syncDirectory); err != nil {
		_ = guard.cleanup()
		return nil, fmt.Errorf("snapshot destination: %w", err)
	}
	if err := organizer.syncDirectory(directory); err != nil {
		_ = guard.cleanup()
		return nil, fmt.Errorf("sync destination protection: %w", err)
	}
	return guard, nil
}

func (guard *destinationGuard) verify(ctx context.Context) error {
	if guard == nil || guard.directory == nil || guard.cleaned {
		return fmt.Errorf("destination guard is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	destination, err := guard.organizer.openEntry(destinationAsManifest(guard.mapping))
	if err != nil {
		return fmt.Errorf("destination read-back: %w", err)
	}
	destinationErr := validateCopiedNode(ctx, destination.file, destination.info, guard.mapping.Source, guard.organizer.copyBufferSize())
	destination.close()
	if destinationErr != nil {
		return fmt.Errorf("destination identity changed: %w", destinationErr)
	}
	payload, err := openChild(guard.directory, "payload")
	if err != nil {
		return fmt.Errorf("guard read-back: %w", err)
	}
	payloadErr := validateCopiedNode(ctx, payload.file, payload.info, guard.mapping.Source, guard.organizer.copyBufferSize())
	payload.close()
	if payloadErr != nil {
		return fmt.Errorf("guard identity changed: %w", payloadErr)
	}
	return nil
}

func (guard *destinationGuard) restore(ctx context.Context) error {
	if guard == nil || guard.directory == nil || guard.cleaned {
		return fmt.Errorf("destination guard is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	destination, err := guard.organizer.openEntry(destinationAsManifest(guard.mapping))
	if err == nil {
		destination.close()
		return fmt.Errorf("destination path is occupied")
	}
	if !isNotExist(err) {
		return fmt.Errorf("inspect destination before restore: %w", err)
	}
	parent, name, err := guard.organizer.openDestinationParent(guard.mapping.Destination)
	if err != nil {
		return err
	}
	defer parent.Close()
	payload, err := openChild(guard.directory, "payload")
	if err != nil {
		return fmt.Errorf("open guard payload: %w", err)
	}
	if err := copyGuardEntry(ctx, payload.file, payload.info, guard.mapping.Source, parent, name, guard.organizer.copyBufferSize(), guard.organizer.syncDirectory); err != nil {
		payload.close()
		return fmt.Errorf("restore destination: %w", err)
	}
	payload.close()
	if err := guard.organizer.syncDirectory(parent); err != nil {
		return fmt.Errorf("sync restored destination parent: %w", err)
	}
	return guard.verify(ctx)
}

func (guard *destinationGuard) cleanup() error {
	if guard == nil || guard.directory == nil || guard.cleaned {
		return nil
	}
	if err := removeGuardEntry(guard.directory, "payload"); err != nil {
		return err
	}
	if err := guard.directory.Close(); err != nil {
		return fmt.Errorf("close destination guard: %w", err)
	}
	rootDirectory, err := openRootDirectory(guard.root.Path)
	if err != nil {
		return err
	}
	defer rootDirectory.Close()
	if err := removeEntry(rootDirectory, guard.name, true); err != nil {
		return fmt.Errorf("remove destination guard: %w", err)
	}
	guard.cleaned = true
	guard.directory = nil
	return nil
}

func copyGuardEntry(ctx context.Context, source *os.File, sourceInfo fs.FileInfo, entry domain.FileManifestEntry, destinationParent *os.File, destinationName string, bufferSize int, syncFn func(*os.File) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if entry.Type != domain.ManifestDirectory {
		destination, err := createFileNoReplace(destinationParent, destinationName, sourceInfo.Mode().Perm())
		if err != nil {
			return err
		}
		if err := copyGuardFile(ctx, source, sourceInfo, destination, entry, bufferSize); err != nil {
			_ = destination.Close()
			return err
		}
		if err := destination.Close(); err != nil {
			return err
		}
		if syncFn != nil {
			if err := syncFn(destinationParent); err != nil {
				return err
			}
		}
		return nil
	}
	if err := makeDirectoryNoReplace(destinationParent, destinationName, sourceInfo.Mode().Perm()); err != nil {
		return err
	}
	destination, err := openChild(destinationParent, destinationName)
	if err != nil {
		return err
	}
	defer destination.close()
	for _, child := range entry.Children {
		childName := path.Base(child.RelativePath)
		childSource, childErr := openChild(source, childName)
		if childErr != nil {
			return childErr
		}
		cloneErr := copyGuardEntry(ctx, childSource.file, childSource.info, child, destination.file, childName, bufferSize, syncFn)
		childSource.close()
		if cloneErr != nil {
			return cloneErr
		}
	}
	if syncFn != nil {
		if err := syncFn(destination.file); err != nil {
			return err
		}
	}
	return nil
}

func copyGuardFile(ctx context.Context, source *os.File, sourceInfo fs.FileInfo, destination *os.File, entry domain.FileManifestEntry, bufferSize int) error {
	if bufferSize <= 0 {
		bufferSize = placement.DefaultBufferSize
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind guard source: %w", err)
	}
	hasher := sha256.New()
	buffer := make([]byte, bufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if _, err := hasher.Write(buffer[:read]); err != nil {
				return err
			}
			written, err := destination.Write(buffer[:read])
			if err != nil {
				return fmt.Errorf("write destination protection: %w", err)
			}
			if written != read {
				return io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read destination protection source: %w", readErr)
		}
	}
	if err := destination.Sync(); err != nil {
		return fmt.Errorf("sync destination protection file: %w", err)
	}
	after, err := source.Stat()
	if err != nil {
		return fmt.Errorf("stat destination protection source: %w", err)
	}
	if !sameObject(sourceInfo, after) || after.Size() != entry.Size {
		return ErrReconciliationNeeded
	}
	actual := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if normalizeDigestValue(actual) != normalizeDigestValue(entry.Digest) {
		return ErrReconciliationNeeded
	}
	return nil
}

func removeGuardEntry(parent *os.File, name string) error {
	node, err := openChild(parent, name)
	if err != nil {
		return err
	}
	if node.info.IsDir() {
		names, listErr := listDirectoryNames(node.file)
		if listErr != nil {
			node.close()
			return listErr
		}
		for _, childName := range names {
			if childErr := removeGuardEntry(node.file, childName); childErr != nil {
				node.close()
				return childErr
			}
		}
	}
	approved := node.info
	node.close()
	if current, currentErr := openChild(parent, name); currentErr == nil {
		owned := sameObject(approved, current.info)
		current.close()
		if !owned {
			return fmt.Errorf("%w: guard entry replaced before cleanup", ErrReconciliationNeeded)
		}
	} else {
		return currentErr
	}
	if err := removeEntry(parent, name, approved.IsDir()); err != nil {
		return err
	}
	return nil
}

func (organizer *Organizer) openDestinationParent(target domain.FileTarget) (*os.File, string, error) {
	if err := target.Validate(); err != nil {
		return nil, "", fmt.Errorf("%w: destination target: %v", ErrInvalidPlan, err)
	}
	root, err := organizer.root(target.RootID)
	if err != nil {
		return nil, "", err
	}
	parentRelative := path.Dir(target.RelativePath)
	if parentRelative == "." {
		parentRelative = ""
	}
	parent, _, err := ensureDirectoryPathWithCreated(root.Path, parentRelative, organizer.syncDirectory)
	if err != nil {
		return nil, "", fmt.Errorf("open destination parent %q: %w", target.RelativePath, err)
	}
	return parent, path.Base(target.RelativePath), nil
}

type nodeHandle struct {
	file   *os.File
	parent *os.File
	name   string
	info   fs.FileInfo
}

func (node *nodeHandle) close() {
	if node == nil {
		return
	}
	if node.file != nil {
		_ = node.file.Close()
	}
	if node.parent != nil {
		_ = node.parent.Close()
	}
}

func (organizer *Organizer) moveWithOperation(ctx context.Context, operationID, action string, request ports.FilesystemMoveRequest) (ports.FilesystemEffect, error) {
	if err := validateOperationID(operationID); err != nil {
		return ports.FilesystemEffect{}, err
	}
	if err := request.Validate(); err != nil {
		return ports.FilesystemEffect{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	plans, err := organizer.prepareMove(ctx, request.Files)
	if err != nil {
		return ports.FilesystemEffect{}, err
	}
	effect := ports.FilesystemEffect{ObservedAt: organizer.now()}
	for ordinal, plan := range plans {
		if err := ctx.Err(); err != nil {
			return effect, err
		}
		if plan.alreadySatisfied {
			effect = appendEffect(effect, plan.mapping.Source, fileEvidence(operationID, ordinal, plan.mapping.Source, "already_satisfied")...)
			continue
		}
		result, err := organizer.moveOne(ctx, operationID, ordinal, action, plan.mapping)
		if err != nil {
			return effect, err
		}
		effect = appendEffect(effect, plan.mapping.Source, result.evidence...)
	}
	effect.ObservedAt = organizer.now()
	return effect, nil
}

type actionResult struct {
	evidence []string
}

func (organizer *Organizer) prepareMove(ctx context.Context, mappings []ports.FileMap) ([]movePlan, error) {
	plans := make([]movePlan, 0, len(mappings))
	for _, mapping := range mappings {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := organizer.checkDestinationRoot(mapping.Destination.RootID); err != nil {
			return nil, err
		}
		if err := organizer.checkSourceRoot(mapping.Source.RootID); err != nil {
			return nil, err
		}
		if err := organizer.checkSourceWritableRoot(mapping.Source.RootID); err != nil {
			return nil, err
		}
		source, sourceErr := organizer.openEntry(mapping.Source)
		if sourceErr != nil && !isNotExist(sourceErr) {
			return nil, fmt.Errorf("open move source %q: %w", mapping.Source.RelativePath, sourceErr)
		}
		plan := movePlan{mapping: mapping}
		if sourceErr == nil {
			if err := validateManifestNode(source.file, source.info, mapping.Source); err != nil {
				source.close()
				return nil, fmt.Errorf("%w: move source %q: %v", ErrSourceChanged, mapping.Source.RelativePath, err)
			}
			if err := organizer.ensureSameFilesystem(source.info, mapping.Destination.RootID); err != nil {
				source.close()
				return nil, err
			}
			source.close()
		}
		destination, destinationErr := organizer.openEntry(destinationAsManifest(mapping))
		if destinationErr == nil {
			if sourceErr == nil {
				destination.close()
				return nil, fmt.Errorf("%w: move destination %q already exists", ErrDestinationConflict, mapping.Destination.RelativePath)
			}
			if err := validateManifestNode(destination.file, destination.info, mapping.Source); err != nil {
				destination.close()
				return nil, fmt.Errorf("%w: existing destination %q does not match the approved source: %v", ErrReconciliationNeeded, mapping.Destination.RelativePath, err)
			}
			destination.close()
			plan.alreadySatisfied = true
		} else if !isNotExist(destinationErr) {
			return nil, fmt.Errorf("inspect move destination %q: %w", mapping.Destination.RelativePath, destinationErr)
		} else if sourceErr != nil {
			return nil, &UncertainError{OperationID: "prepare", Ordinal: len(plans), Cause: fmt.Errorf("%w: both source and destination are absent", ErrReconciliationNeeded)}
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func (organizer *Organizer) moveOne(ctx context.Context, operationID string, ordinal int, action string, mapping ports.FileMap) (actionResult, error) {
	source, err := organizer.openEntry(mapping.Source)
	if err != nil {
		return actionResult{}, fmt.Errorf("%w: move source read-back failed: %v", ErrSourceChanged, err)
	}
	defer source.close()
	if err := validateManifestNode(source.file, source.info, mapping.Source); err != nil {
		return actionResult{}, fmt.Errorf("%w: move source changed before publication: %v", ErrSourceChanged, err)
	}
	destinationParent, destinationName, created, err := organizer.ensureDestinationParent(mapping.Destination)
	if err != nil {
		_ = created
		return actionResult{}, err
	}
	defer destinationParent.Close()
	if existing, existingErr := openChild(destinationParent, destinationName); existingErr == nil {
		existing.close()
		return actionResult{}, fmt.Errorf("%w: move destination %q appeared", ErrDestinationExists, mapping.Destination.RelativePath)
	} else if !isNotExist(existingErr) {
		return actionResult{}, fmt.Errorf("inspect move destination %q: %w", mapping.Destination.RelativePath, existingErr)
	}
	if organizer.opts.beforeMovePublication != nil {
		organizer.opts.beforeMovePublication()
	}
	if err := ctx.Err(); err != nil {
		return actionResult{}, err
	}
	// Linux moves first quarantine the directory entry and validate that the
	// quarantined inode is the approved descriptor. The native publication is
	// therefore never directed at an object selected by a raced source name.
	if publishErr := moveOwned(ctx, operationID, ordinal, source, destinationParent, destinationName, mapping); publishErr != nil {
		if errors.Is(publishErr, ErrDestinationExists) {
			return actionResult{}, fmt.Errorf("%w: move destination %q appeared during publication", ErrDestinationExists, mapping.Destination.RelativePath)
		}
		if errors.Is(publishErr, ErrCrossDevice) {
			return actionResult{}, ErrCrossDevice
		}
		return actionResult{}, publishErr
	}
	if err := organizer.syncDirectory(source.parent); err != nil {
		return actionResult{}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: source parent sync: %v", ErrPublicationUnknown, err)}
	}
	if err := organizer.syncDirectory(destinationParent); err != nil {
		return actionResult{}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination parent sync: %v", ErrPublicationUnknown, err)}
	}
	destination, destinationErr := organizer.openEntry(destinationAsManifest(mapping))
	if destinationErr != nil {
		return actionResult{}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination read-back failed: %v", ErrReconciliationNeeded, destinationErr)}
	}
	readBackErr := validateManifestNode(destination.file, destination.info, mapping.Source)
	destination.close()
	if readBackErr != nil {
		return actionResult{}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination identity changed: %v", ErrReconciliationNeeded, readBackErr)}
	}
	if old, oldErr := organizer.openEntry(mapping.Source); oldErr == nil {
		old.close()
		return actionResult{}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: old path remains after %s", ErrReconciliationNeeded, action)}
	} else if !isNotExist(oldErr) {
		return actionResult{}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: old path read-back failed: %v", ErrReconciliationNeeded, oldErr)}
	}
	return actionResult{evidence: fileEvidence(operationID, ordinal, mapping.Source, "applied")}, nil
}

func (organizer *Organizer) prepareDelete(ctx context.Context, entries []domain.FileManifestEntry) ([]deletePlan, error) {
	plans := make([]deletePlan, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := organizer.checkWritableRoot(entry.RootID); err != nil {
			return nil, err
		}
		node, err := organizer.openEntry(entry)
		if err != nil {
			if isNotExist(err) {
				plans = append(plans, deletePlan{entry: entry, alreadySatisfied: true})
				continue
			}
			return nil, fmt.Errorf("open delete source %q: %w", entry.RelativePath, err)
		}
		validationErr := validateManifestNode(node.file, node.info, entry)
		node.close()
		if validationErr != nil {
			return nil, fmt.Errorf("%w: delete source %q: %v", ErrSourceChanged, entry.RelativePath, validationErr)
		}
		plans = append(plans, deletePlan{entry: entry})
	}
	return plans, nil
}

func (organizer *Organizer) deleteOne(ctx context.Context, operationID string, ordinal int, entry domain.FileManifestEntry, guard *destinationGuard) ([]domain.FileManifestEntry, error) {
	node, err := organizer.openEntry(entry)
	if err != nil {
		return nil, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: delete source disappeared: %v", ErrReconciliationNeeded, err)}
	}
	defer node.close()
	if err := validateManifestNode(node.file, node.info, entry); err != nil {
		return nil, fmt.Errorf("%w: delete source changed before mutation: %v", ErrSourceChanged, err)
	}
	if organizer.opts.beforeDeletePublication != nil {
		organizer.opts.beforeDeletePublication()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if guard != nil {
		if err := guard.verify(ctx); err != nil {
			return nil, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination changed before source removal: %v", ErrReconciliationNeeded, err)}
		}
		if organizer.opts.afterDestinationGuardVerification != nil {
			organizer.opts.afterDestinationGuardVerification()
		}
	}
	current, currentErr := organizer.openEntry(entry)
	if currentErr != nil {
		return nil, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: delete source changed before mutation: %v", ErrReconciliationNeeded, currentErr)}
	}
	currentErr = validateManifestNode(current.file, current.info, entry)
	current.close()
	if currentErr != nil {
		return nil, fmt.Errorf("%w: delete source changed before mutation: %v", ErrSourceChanged, currentErr)
	}
	deleted, err := deleteOwnedNode(ctx, operationID, ordinal, node.parent, node.name, entry)
	if err != nil {
		return deleted, err
	}
	if err := organizer.syncDirectory(node.parent); err != nil {
		return deleted, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: delete parent sync: %v", ErrPublicationUnknown, err)}
	}
	if stillThere, statErr := openChild(node.parent, node.name); statErr == nil {
		stillThere.close()
		return deleted, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: deleted path remains visible", ErrDeleteUnknown)}
	} else if !isNotExist(statErr) {
		return deleted, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: deleted path read-back failed: %v", ErrDeleteUnknown, statErr)}
	}
	return deleted, nil
}

func (organizer *Organizer) reconcileMove(ctx context.Context, operationID string, request ports.FilesystemMoveRequest) (ports.FilesystemEffect, error) {
	if err := validateOperationID(operationID); err != nil {
		return ports.FilesystemEffect{}, err
	}
	if err := request.Validate(); err != nil {
		return ports.FilesystemEffect{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	effect := ports.FilesystemEffect{ObservedAt: organizer.now()}
	for ordinal, mapping := range request.Files {
		if err := ctx.Err(); err != nil {
			return effect, err
		}
		old, oldErr := organizer.openEntry(mapping.Source)
		if oldErr == nil {
			old.close()
			return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: old path remains", ErrReconciliationNeeded)}
		}
		if !isNotExist(oldErr) {
			return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: old path read-back failed: %v", ErrReconciliationNeeded, oldErr)}
		}
		destination, destinationErr := organizer.openEntry(destinationAsManifest(mapping))
		if destinationErr != nil {
			return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination read-back failed: %v", ErrReconciliationNeeded, destinationErr)}
		}
		validationErr := validateManifestNode(destination.file, destination.info, mapping.Source)
		destination.close()
		if validationErr != nil {
			return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination identity mismatch: %v", ErrReconciliationNeeded, validationErr)}
		}
		effect = appendEffect(effect, mapping.Source, fileEvidence(operationID, ordinal, mapping.Source, "reconciled")...)
	}
	effect.ObservedAt = organizer.now()
	return effect, nil
}

func (organizer *Organizer) checkCrossDevice(ctx context.Context, mappings []ports.FileMap) error {
	for _, mapping := range mappings {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := organizer.checkDestinationRoot(mapping.Destination.RootID); err != nil {
			return err
		}
		source, err := organizer.openEntry(mapping.Source)
		if err != nil {
			return fmt.Errorf("open cross-device source %q: %w", mapping.Source.RelativePath, err)
		}
		if err := validateManifestNode(source.file, source.info, mapping.Source); err != nil {
			source.close()
			return fmt.Errorf("%w: cross-device source %q: %v", ErrSourceChanged, mapping.Source.RelativePath, err)
		}
		deviceErr := organizer.ensureDifferentFilesystem(source.info, mapping.Destination.RootID)
		source.close()
		if deviceErr != nil {
			return deviceErr
		}
	}
	return nil
}

type crossDevicePlan struct {
	mapping          ports.FileMap
	alreadySatisfied bool
}

// prepareCrossDevice binds a retry to either the still-present source or the
// already-materialized destination. This makes a completed copy/verify/delete
// idempotent after its response was lost, while an absent source with no
// matching destination remains uncertain and never triggers a new write.
func (organizer *Organizer) prepareCrossDevice(ctx context.Context, mappings []ports.FileMap) ([]crossDevicePlan, error) {
	plans := make([]crossDevicePlan, 0, len(mappings))
	for ordinal, mapping := range mappings {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := organizer.checkDestinationRoot(mapping.Destination.RootID); err != nil {
			return nil, err
		}
		if err := organizer.checkSourceWritableRoot(mapping.Source.RootID); err != nil {
			return nil, err
		}
		source, sourceErr := organizer.openEntry(mapping.Source)
		if sourceErr == nil {
			if validationErr := validateManifestNode(source.file, source.info, mapping.Source); validationErr != nil {
				source.close()
				return nil, fmt.Errorf("%w: cross-device source %q: %v", ErrSourceChanged, mapping.Source.RelativePath, validationErr)
			}
			deviceErr := organizer.ensureDifferentFilesystem(source.info, mapping.Destination.RootID)
			source.close()
			if deviceErr != nil {
				return nil, deviceErr
			}
			plans = append(plans, crossDevicePlan{mapping: mapping})
			continue
		}
		if !isNotExist(sourceErr) {
			return nil, fmt.Errorf("open cross-device source %q: %w", mapping.Source.RelativePath, sourceErr)
		}

		destination, destinationErr := organizer.openEntry(destinationAsManifest(mapping))
		if destinationErr == nil {
			validationErr := validateCopiedNode(ctx, destination.file, destination.info, destinationAsManifest(mapping), organizer.copyBufferSize())
			destination.close()
			if validationErr != nil {
				return nil, &UncertainError{OperationID: "prepare", Ordinal: ordinal, Cause: fmt.Errorf("%w: existing destination %q does not match approved source: %v", ErrReconciliationNeeded, mapping.Destination.RelativePath, validationErr)}
			}
			plans = append(plans, crossDevicePlan{mapping: mapping, alreadySatisfied: true})
			continue
		}
		if !isNotExist(destinationErr) {
			return nil, fmt.Errorf("inspect cross-device destination %q: %w", mapping.Destination.RelativePath, destinationErr)
		}
		return nil, &UncertainError{OperationID: "prepare", Ordinal: ordinal, Cause: fmt.Errorf("%w: source and destination are both absent", ErrReconciliationNeeded)}
	}
	return plans, nil
}

func destinationAsManifest(mapping ports.FileMap) domain.FileManifestEntry {
	return domain.FileManifestEntry{
		RootID: mapping.Destination.RootID, RelativePath: mapping.Destination.RelativePath,
		Type: mapping.Source.Type, Size: mapping.Source.Size, Digest: mapping.Source.Digest,
		FileIdentity: mapping.Source.FileIdentity, Role: mapping.Source.Role,
		ObservedAt: mapping.Source.ObservedAt,
		Children:   remapManifestChildren(mapping.Source, mapping.Destination),
	}
}

func remapManifestChildren(source domain.FileManifestEntry, destination domain.FileTarget) []domain.FileManifestEntry {
	return remapManifestChildrenAt(source, destination.RootID, destination.RelativePath)
}

func remapManifestChildrenAt(source domain.FileManifestEntry, destinationRoot domain.ConfigID, destinationPath string) []domain.FileManifestEntry {
	children := make([]domain.FileManifestEntry, 0, len(source.Children))
	for _, originalChild := range source.Children {
		child := originalChild
		suffix := strings.TrimPrefix(child.RelativePath, source.RelativePath+"/")
		child.RelativePath = path.Join(destinationPath, suffix)
		child.RootID = destinationRoot
		child.Children = remapManifestChildrenAt(originalChild, destinationRoot, child.RelativePath)
		children = append(children, child)
	}
	return children
}

func validateManifestNode(file *os.File, info fs.FileInfo, entry domain.FileManifestEntry) error {
	if !matchesManifest(info, entry) {
		return fmt.Errorf("identity, mode or size differs")
	}
	if entry.Type != domain.ManifestDirectory {
		return nil
	}
	actual, err := listDirectoryNames(file)
	if err != nil {
		return fmt.Errorf("enumerate directory: %w", err)
	}
	wanted := make([]string, 0, len(entry.Children))
	for _, child := range entry.Children {
		suffix := strings.TrimPrefix(child.RelativePath, entry.RelativePath+"/")
		if suffix == "" || strings.Contains(suffix, "/") {
			return fmt.Errorf("child %q is not an immediate manifest child", child.RelativePath)
		}
		wanted = append(wanted, suffix)
	}
	sort.Strings(actual)
	sort.Strings(wanted)
	if len(actual) != len(wanted) {
		return fmt.Errorf("directory child count differs")
	}
	for index := range actual {
		if actual[index] != wanted[index] {
			return fmt.Errorf("directory child set differs")
		}
	}
	for _, child := range entry.Children {
		childName := path.Base(child.RelativePath)
		childNode, err := openChild(file, childName)
		if err != nil {
			return fmt.Errorf("open child %q: %w", child.RelativePath, err)
		}
		childErr := validateManifestNode(childNode.file, childNode.info, child)
		childNode.close()
		if childErr != nil {
			return childErr
		}
	}
	return nil
}

// validateCopiedNode proves a destination created by copy/verify. Source
// identity cannot match across filesystems, so regular files use their exact
// size and strong digest while directories use their exact approved child set.
func validateCopiedNode(ctx context.Context, file *os.File, info fs.FileInfo, entry domain.FileManifestEntry, bufferSize int) error {
	if info == nil || info.Mode()&fs.ModeSymlink != 0 {
		return ErrSymlink
	}
	if entry.Type == domain.ManifestDirectory {
		if !info.IsDir() {
			return ErrSourceChanged
		}
		actual, err := listDirectoryNames(file)
		if err != nil {
			return err
		}
		wanted := make([]string, 0, len(entry.Children))
		for _, child := range entry.Children {
			suffix := strings.TrimPrefix(child.RelativePath, entry.RelativePath+"/")
			if suffix == "" || strings.Contains(suffix, "/") {
				return ErrInvalidPlan
			}
			wanted = append(wanted, suffix)
		}
		sort.Strings(actual)
		sort.Strings(wanted)
		if len(actual) != len(wanted) {
			return fmt.Errorf("%w: directory child count differs: actual=%v wanted=%v", ErrReconciliationNeeded, actual, wanted)
		}
		for index := range actual {
			if actual[index] != wanted[index] {
				return fmt.Errorf("%w: directory child differs: actual=%v wanted=%v", ErrReconciliationNeeded, actual, wanted)
			}
		}
		for _, child := range entry.Children {
			childNode, err := openChild(file, path.Base(child.RelativePath))
			if err != nil {
				return fmt.Errorf("open child %q: %w", child.RelativePath, err)
			}
			childErr := validateCopiedNode(ctx, childNode.file, childNode.info, child, bufferSize)
			childNode.close()
			if childErr != nil {
				return fmt.Errorf("validate child %q: %w", child.RelativePath, childErr)
			}
		}
		finalNames, err := listDirectoryNames(file)
		if err != nil {
			return err
		}
		sort.Strings(finalNames)
		if len(finalNames) != len(wanted) {
			return fmt.Errorf("%w: final directory child count differs: actual=%v wanted=%v", ErrReconciliationNeeded, finalNames, wanted)
		}
		for index := range finalNames {
			if finalNames[index] != wanted[index] {
				return fmt.Errorf("%w: final directory child differs: actual=%v wanted=%v", ErrReconciliationNeeded, finalNames, wanted)
			}
		}
		return nil
	}
	if info.IsDir() || !info.Mode().IsRegular() || info.Size() != entry.Size {
		return ErrReconciliationNeeded
	}
	return verifyFileDigest(ctx, file, info, entry.Digest, bufferSize)
}

func verifyFileDigest(ctx context.Context, file *os.File, before fs.FileInfo, expected string, bufferSize int) error {
	if bufferSize <= 0 {
		bufferSize = placement.DefaultBufferSize
	}
	hasher := sha256.New()
	buffer := make([]byte, bufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			if _, err := hasher.Write(buffer[:read]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	after, err := file.Stat()
	if err != nil {
		return err
	}
	if !sameObject(before, after) || after.Size() != before.Size() {
		return ErrReconciliationNeeded
	}
	actual := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if normalizeDigestValue(actual) != normalizeDigestValue(expected) {
		return ErrReconciliationNeeded
	}
	return nil
}

func normalizeDigestValue(value string) string {
	value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "sha256:")
	if len(value) != sha256.Size*2 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func matchesManifest(info fs.FileInfo, entry domain.FileManifestEntry) bool {
	if info == nil || info.Mode()&fs.ModeSymlink != 0 {
		return false
	}
	wantDirectory := entry.Type == domain.ManifestDirectory
	if wantDirectory != info.IsDir() {
		return false
	}
	if !wantDirectory && !info.Mode().IsRegular() {
		return false
	}
	if info.Size() != entry.Size || fileIdentity(info) != entry.FileIdentity {
		return false
	}
	// Current observe identities bind device, inode, size and mtime. Keep
	// compatibility with those observations while honoring a future identity
	// format that carries permission bits explicitly.
	if mode := modeFromIdentity(entry.FileIdentity); mode != 0 && info.Mode().Perm() != mode {
		return false
	}
	return true
}

// modeFromIdentity is intentionally conservative. Older observations encode
// no permission bits; in that case validation still binds the full identity
// and size, while current observations include the mode marker below.
func modeFromIdentity(identity string) fs.FileMode {
	if marker := strings.LastIndex(identity, ":mode="); marker >= 0 {
		var mode uint32
		if _, err := fmt.Sscanf(identity[marker+len(":mode="):], "%o", &mode); err == nil {
			return fs.FileMode(mode) & fs.ModePerm
		}
	}
	return 0
}

func appendEffect(effect ports.FilesystemEffect, entry domain.FileManifestEntry, evidence ...string) ports.FilesystemEffect {
	state := ""
	for _, item := range evidence {
		if strings.HasPrefix(item, "state=") {
			state = strings.TrimPrefix(item, "state=")
		}
	}
	switch state {
	case "applied":
		effect.Outcome = domain.OutcomeApplied
	case "already_satisfied", "already_satisfied_race", "reconciled":
		if effect.Outcome == "" {
			effect.Outcome = domain.OutcomeAlreadySatisfied
		}
	}
	effect.Affected = append(effect.Affected, entry)
	effect.Evidence = append(effect.Evidence, evidence...)
	return effect
}

func fileEvidence(operationID string, ordinal int, entry domain.FileManifestEntry, state string) []string {
	return []string{
		"operation=" + operationID,
		fmt.Sprintf("ordinal=%d", ordinal),
		"source_root=" + entry.RootID.String(),
		"source_path=" + entry.RelativePath,
		"state=" + state,
	}
}

func (organizer *Organizer) ensureDestinationParent(target domain.FileTarget) (*os.File, string, []string, error) {
	if err := target.Validate(); err != nil {
		return nil, "", nil, fmt.Errorf("%w: destination target: %v", ErrInvalidPlan, err)
	}
	root, err := organizer.root(target.RootID)
	if err != nil {
		return nil, "", nil, err
	}
	parentRelative := path.Dir(target.RelativePath)
	if parentRelative == "." {
		parentRelative = ""
	}
	parent, created, err := ensureDirectoryPathWithCreated(root.Path, parentRelative, organizer.syncDirectory)
	if err != nil {
		return nil, "", created, fmt.Errorf("open destination parent %q: %w", target.RelativePath, err)
	}
	return parent, path.Base(target.RelativePath), created, nil
}

func (organizer *Organizer) openEntry(entry domain.FileManifestEntry) (*nodeHandle, error) {
	root, err := organizer.root(entry.RootID)
	if err != nil {
		return nil, err
	}
	return openExistingTarget(root.Path, entry.RelativePath)
}

func (organizer *Organizer) root(id domain.ConfigID) (Root, error) {
	root, ok := organizer.roots[id]
	if !ok {
		return Root{}, fmt.Errorf("%w: %q", ErrRootNotConfigured, id)
	}
	return root, nil
}

func (organizer *Organizer) checkSourceRoot(id domain.ConfigID) error {
	_, err := organizer.root(id)
	return err
}

func (organizer *Organizer) checkSourceWritableRoot(id domain.ConfigID) error {
	root, err := organizer.root(id)
	if err != nil {
		return err
	}
	if root.ReadOnly {
		return fmt.Errorf("%w: root %q", ErrReadOnly, id)
	}
	return nil
}

func (organizer *Organizer) checkDestinationRoot(id domain.ConfigID) error {
	root, err := organizer.root(id)
	if err != nil {
		return err
	}
	if root.ReadOnly {
		return fmt.Errorf("%w: root %q", ErrReadOnly, id)
	}
	if !organizeWritesSupported {
		return fmt.Errorf("%w: target lacks reviewed no-follow/no-replace organize primitives", ErrUnsupported)
	}
	return nil
}

func (organizer *Organizer) checkWritableRoot(id domain.ConfigID) error {
	return organizer.checkDestinationRoot(id)
}

func (organizer *Organizer) ensureSameFilesystem(sourceInfo fs.FileInfo, destinationRootID domain.ConfigID) error {
	device, err := filesystemDevice(sourceInfo)
	if err != nil {
		return err
	}
	root, err := organizer.root(destinationRootID)
	if err != nil {
		return err
	}
	destination, err := openRootDirectory(root.Path)
	if err != nil {
		return err
	}
	destinationInfo, statErr := destination.Stat()
	destination.Close()
	if statErr != nil {
		return statErr
	}
	destinationDevice, err := filesystemDevice(destinationInfo)
	if err != nil {
		return err
	}
	if device != destinationDevice {
		return ErrCrossDevice
	}
	return nil
}

func (organizer *Organizer) ensureDifferentFilesystem(sourceInfo fs.FileInfo, destinationRootID domain.ConfigID) error {
	device, err := filesystemDevice(sourceInfo)
	if err != nil {
		return err
	}
	root, err := organizer.root(destinationRootID)
	if err != nil {
		return err
	}
	destination, err := openRootDirectory(root.Path)
	if err != nil {
		return err
	}
	destinationInfo, statErr := destination.Stat()
	destination.Close()
	if statErr != nil {
		return statErr
	}
	destinationDevice, err := filesystemDevice(destinationInfo)
	if err != nil {
		return err
	}
	if device == destinationDevice {
		return ErrSameFilesystem
	}
	return nil
}

func (organizer *Organizer) syncDirectory(directory *os.File) error {
	if organizer.opts.SyncDirectory != nil {
		return organizer.opts.SyncDirectory(directory)
	}
	return syncDirectory(directory)
}

func (organizer *Organizer) copyBufferSize() int {
	if organizer.opts.BufferSize <= 0 {
		return placement.DefaultBufferSize
	}
	return organizer.opts.BufferSize
}

func (organizer *Organizer) now() time.Time {
	if organizer.opts.Clock == nil {
		return time.Now().UTC()
	}
	return organizer.opts.Clock().UTC()
}

func (organizer *Organizer) newOperationID() (string, error) {
	id, err := domain.NewRuntimeID()
	if err != nil {
		return "", fmt.Errorf("generate filesystem organize operation id: %w", err)
	}
	return id.String(), nil
}

func validateOperationID(value string) error {
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "/\\\x00\r\n") {
		return fmt.Errorf("%w: invalid operation id", ErrInvalidPlan)
	}
	return nil
}

// effectiveRootPath resolves existing symlink aliases at the configuration
// boundary. Missing roots retain their canonical lexical path and are still
// checked for overlap; action-time descriptor traversal remains authoritative.
func effectiveRootPath(rootPath string) string {
	if resolved, err := filepath.EvalSymlinks(rootPath); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(rootPath)
}

// rootPathsOverlap rejects equal and nested roots. A logical root must own one
// unambiguous physical namespace; allowing nested aliases would make a request
// choose different read-only or writable authority for the same object.
func rootPathsOverlap(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	return rootPathContains(left, right) || rootPathContains(right, left)
}

func rootPathsConflict(left, right string) bool {
	if rootPathsOverlap(left, right) {
		return true
	}
	leftInfo, leftErr := os.Stat(left)
	rightInfo, rightErr := os.Stat(right)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func rootPathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func privateEntryName(kind, operationID string, ordinal int, relative string) string {
	seed := fmt.Sprintf("%s\x00%s\x00%d\x00%s", kind, operationID, ordinal, relative)
	digest := sha256.Sum256([]byte(seed))
	return fmt.Sprintf(".mastarr-%s-%x", kind, digest[:12])
}

func derivedOperationID(operationID, suffix string) (string, error) {
	if err := validateOperationID(operationID); err != nil {
		return "", err
	}
	if suffix == "" || strings.ContainsAny(suffix, "/\\\x00\r\n") {
		return "", fmt.Errorf("%w: invalid operation suffix", ErrInvalidPlan)
	}
	derived := operationID + "-" + suffix
	if err := validateOperationID(derived); err != nil {
		return "", fmt.Errorf("%w: derived operation id exceeds limit", ErrInvalidPlan)
	}
	return derived, nil
}

func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }

func isExist(err error) bool { return errors.Is(err, os.ErrExist) }

func isCrossDevice(err error) bool { return errors.Is(err, errCrossDevice) }
