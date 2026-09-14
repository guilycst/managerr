// Package placement implements the reviewed copy and hardlink filesystem
// actions. It is deliberately independent of the durable executor: callers
// provide an exact ports request, and the package returns one effect with
// per-file evidence. The executor can journal that effect and use the
// read-only reconciliation methods after an interrupted call.
package placement

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
	"github.com/guilycst/mastarr/internal/ports"
)

const (
	// DefaultBufferSize bounds the memory used while copying or hashing one
	// file. A copy never reads an entire payload into memory.
	DefaultBufferSize = 1 << 20
	// MaxBufferSize keeps a caller-supplied chunk bound from turning one action
	// into an unexpectedly large allocation.
	MaxBufferSize = 16 << 20
	// DefaultStagePrefix identifies the staging namespace used by older or
	// target-specific implementations. Linux v0.0.1 uses anonymous staging,
	// so a successful copy has no pathname alias to clean up.
	DefaultStagePrefix = ".mastarr-stage"
)

var (
	ErrRootNotConfigured    = errors.New("filesystem root is not configured")
	ErrRootTarget           = errors.New("filesystem root target is not allowed")
	ErrPathEscape           = errors.New("filesystem path escapes configured root")
	ErrSymlink              = errors.New("filesystem symlink is not allowed")
	ErrSpecialFile          = errors.New("filesystem special file is not allowed")
	ErrInvalidPlan          = errors.New("filesystem placement plan is invalid")
	ErrReadOnly             = errors.New("filesystem placement destination is read-only")
	ErrUnsupported          = errors.New("filesystem placement is unsupported")
	ErrSourceChanged        = errors.New("filesystem source changed after approval")
	ErrDestinationConflict  = errors.New("filesystem destination conflicts with approved content")
	ErrDestinationExists    = errors.New("filesystem destination appeared during publication")
	ErrStageChanged         = errors.New("filesystem staging object changed before publication")
	ErrReconciliationNeeded = errors.New("filesystem effect requires read-only reconciliation")
	ErrPublicationUnknown   = errors.New("filesystem publication durability is unknown")
	ErrJournalUnknown       = errors.New("filesystem effect journal durability is unknown")
	ErrHardlinkIdentity     = errors.New("filesystem hardlink identity verification failed")
)

// TransferMode is intentionally explicit. A failed hardlink is never silently
// retried as a copy.
type TransferMode string

const (
	TransferCopy     TransferMode = "copy"
	TransferHardlink TransferMode = "hardlink"
)

// Root is the host path for one configured storage root. Host paths remain
// constructor-only configuration; action requests carry only root-relative
// domain.FileTarget values.
type Root struct {
	ID           domain.ConfigID
	Path         string
	ReadOnly     bool
	Capabilities []domain.Capability
}

// Options bounds one placer. A journal is optional so the filesystem adapter
// remains independent from storage and can be tested with a small fake.
type Options struct {
	BufferSize    int
	StagePrefix   string
	Journal       EffectJournal
	Clock         func() time.Time
	SyncDirectory func(*os.File) error

	// beforeStagePublication is a package-private fault-injection seam used by
	// synthetic race tests. Production callers cannot install a callback.
	beforeStagePublication func(*os.File, string)
	// beforeHardlinkPublication is a package-private fault-injection seam used
	// to exchange the source pathname after validation. Production callers
	// cannot install a callback.
	beforeHardlinkPublication func(*os.File)
}

// FileEffect is the optional durable-journal projection. It keeps the exact
// source manifest and destination target for one file, while never recording a
// host path or payload bytes.
type FileEffect struct {
	OperationID string
	Ordinal     int
	Mode        TransferMode
	Source      domain.FileManifestEntry
	Destination domain.FileTarget
	Outcome     domain.EffectOutcome
	Evidence    []string
	ObservedAt  time.Time
}

// EffectJournal receives one record after each file reaches a verified
// terminal state. If Append fails after publication, the action returns an
// UncertainError and leaves the destination in place for reconciliation.
type EffectJournal interface {
	Append(context.Context, FileEffect) error
}

// UncertainError means a local effect may already be visible but its durable
// evidence could not be completed. The operation ID and ordinal let the
// executor resume with ReconcileCopy or ReconcileHardlink without guessing.
type UncertainError struct {
	OperationID string
	Ordinal     int
	Cause       error
}

func (err *UncertainError) Error() string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("filesystem operation %s file %d: %v", err.OperationID, err.Ordinal, err.Cause)
}

func (err *UncertainError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// Placer implements the frozen filesystem action port for copy and hardlink.
// Other action methods deliberately return ErrUnsupported so this lane cannot
// broaden into move, rename, trash or delete.
type Placer struct {
	roots map[domain.ConfigID]Root
	opts  Options
}

var _ ports.FilesystemActionPort = (*Placer)(nil)

// New validates root identity and stores a private copy of the effective root
// map. It does not touch the filesystem until an action is requested.
func New(roots []Root, options Options) (*Placer, error) {
	if options.BufferSize <= 0 {
		options.BufferSize = DefaultBufferSize
	}
	if options.BufferSize > MaxBufferSize {
		return nil, fmt.Errorf("%w: buffer size exceeds %d bytes", ErrInvalidPlan, MaxBufferSize)
	}
	if options.StagePrefix == "" {
		options.StagePrefix = DefaultStagePrefix
	}
	if !validStagePrefix(options.StagePrefix) {
		return nil, fmt.Errorf("%w: stage prefix must be a simple name", ErrInvalidPlan)
	}
	if options.Clock == nil {
		options.Clock = func() time.Time { return time.Now().UTC() }
	}
	configured := make(map[domain.ConfigID]Root, len(roots))
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
		root.Capabilities = append([]domain.Capability(nil), root.Capabilities...)
		configured[root.ID] = root
	}
	return &Placer{roots: configured, opts: options}, nil
}

// NewFromStorageRoots adapts the frozen domain configuration snapshot without
// making configuration a dependency of this filesystem package.
func NewFromStorageRoots(roots []domain.StorageRoot, options Options) (*Placer, error) {
	configured := make([]Root, 0, len(roots))
	for _, root := range roots {
		configured = append(configured, Root{
			ID: root.ID, Path: root.Path, ReadOnly: root.ReadOnly,
			Capabilities: append([]domain.Capability(nil), root.Capabilities...),
		})
	}
	return New(configured, options)
}

// Copy performs an exact-plan copy. Every source is checked before any
// destination publication. A destination that already contains the requested
// digest is already satisfied; an unequal existing file is a conflict.
func (p *Placer) Copy(ctx context.Context, request ports.FilesystemCopyRequest) (ports.FilesystemEffect, error) {
	operationID, err := p.newOperationID()
	if err != nil {
		return ports.FilesystemEffect{}, err
	}
	return p.CopyWithOperation(ctx, operationID, request)
}

// CopyWithOperation is the stable-ID form used by a durable executor. The
// operation ID is used only in sanitized evidence and staging names.
func (p *Placer) CopyWithOperation(ctx context.Context, operationID string, request ports.FilesystemCopyRequest) (ports.FilesystemEffect, error) {
	return p.runCopy(ctx, operationID, request)
}

// Hardlink performs an exact-plan hardlink with no copy fallback. Existing
// content at another inode is a conflict, even when its bytes are identical.
func (p *Placer) Hardlink(ctx context.Context, request ports.FilesystemHardlinkRequest) (ports.FilesystemEffect, error) {
	operationID, err := p.newOperationID()
	if err != nil {
		return ports.FilesystemEffect{}, err
	}
	return p.HardlinkWithOperation(ctx, operationID, request)
}

// HardlinkWithOperation is the stable-ID form used by a durable executor.
func (p *Placer) HardlinkWithOperation(ctx context.Context, operationID string, request ports.FilesystemHardlinkRequest) (ports.FilesystemEffect, error) {
	return p.runHardlink(ctx, operationID, request)
}

// ReconcileCopy performs only read operations. It proves whether each exact
// destination now contains the approved digest after a lost journal/publication
// response. It never retries a transfer and never removes a destination.
func (p *Placer) ReconcileCopy(ctx context.Context, operationID string, request ports.FilesystemCopyRequest) (ports.FilesystemEffect, error) {
	plans, _, err := p.expandCopyRequest(ctx, request)
	if err != nil {
		return ports.FilesystemEffect{}, err
	}
	return p.reconcilePlans(ctx, operationID, TransferCopy, plans)
}

// ReconcileHardlink performs only read operations and proves same-object
// identity for every selected destination. It never falls back to copying.
func (p *Placer) ReconcileHardlink(ctx context.Context, operationID string, request ports.FilesystemHardlinkRequest) (ports.FilesystemEffect, error) {
	if err := validateOperationID(operationID); err != nil {
		return ports.FilesystemEffect{}, err
	}
	if err := request.Validate(); err != nil {
		return ports.FilesystemEffect{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	plans := make([]filePlan, 0, len(request.Files))
	for _, mapping := range request.Files {
		if _, err := p.root(mapping.Destination.RootID); err != nil {
			return ports.FilesystemEffect{}, err
		}
		plans = append(plans, filePlan{source: mapping.Source, destination: mapping.Destination})
	}
	return p.reconcilePlans(ctx, operationID, TransferHardlink, plans)
}

// Unsupported methods make the lane's scope explicit. F-05 owns the future
// move/rename/delete implementations and must not inherit partial semantics.
func (p *Placer) Move(context.Context, ports.FilesystemMoveRequest) (ports.FilesystemEffect, error) {
	return ports.FilesystemEffect{}, ErrUnsupported
}

func (p *Placer) Rename(context.Context, ports.FilesystemRenameRequest) (ports.FilesystemEffect, error) {
	return ports.FilesystemEffect{}, ErrUnsupported
}

func (p *Placer) Trash(context.Context, ports.FilesystemTrashRequest) (ports.FilesystemEffect, error) {
	return ports.FilesystemEffect{}, ErrUnsupported
}

func (p *Placer) Restore(context.Context, ports.FilesystemRestoreRequest) (ports.FilesystemEffect, error) {
	return ports.FilesystemEffect{}, ErrUnsupported
}

func (p *Placer) Delete(context.Context, ports.FilesystemDeleteRequest) (ports.FilesystemEffect, error) {
	return ports.FilesystemEffect{}, ErrUnsupported
}

type filePlan struct {
	source      domain.FileManifestEntry
	destination domain.FileTarget
}

type sourceHandle struct {
	file   *os.File
	parent *os.File
	name   string
	info   fs.FileInfo
}

func (source *sourceHandle) close() {
	if source == nil {
		return
	}
	if source.file != nil {
		_ = source.file.Close()
	}
	if source.parent != nil {
		_ = source.parent.Close()
	}
}

type directorySnapshot struct {
	source      domain.FileManifestEntry
	destination domain.FileTarget
	identity    string
}

type createdDirectory struct {
	rootID   domain.ConfigID
	relative string
}

type fileResult struct {
	outcome     domain.EffectOutcome
	evidence    []string
	publication bool
}

func (p *Placer) runCopy(ctx context.Context, operationID string, request ports.FilesystemCopyRequest) (ports.FilesystemEffect, error) {
	if err := validateOperationID(operationID); err != nil {
		return ports.FilesystemEffect{}, err
	}
	plans, directories, err := p.prepareCopy(ctx, request)
	if err != nil {
		return ports.FilesystemEffect{}, err
	}
	if err := checkCopyDestinations(ctx, p, request, plans, directories); err != nil {
		return ports.FilesystemEffect{}, err
	}
	if err := ctx.Err(); err != nil {
		return ports.FilesystemEffect{}, err
	}

	createdDirectories := make([]createdDirectory, 0)
	for _, directory := range directories {
		if err := ctx.Err(); err != nil {
			p.cleanupDirectories(createdDirectories)
			return ports.FilesystemEffect{}, err
		}
		created, err := p.ensureDestinationDirectory(directory.destination)
		if err != nil {
			p.cleanupDirectories(append(createdDirectories, created...))
			return ports.FilesystemEffect{}, err
		}
		createdDirectories = append(createdDirectories, created...)
	}

	effect := ports.FilesystemEffect{ObservedAt: p.now()}
	for ordinal, plan := range plans {
		if err := ctx.Err(); err != nil {
			if len(effect.Affected) == 0 {
				p.cleanupDirectories(createdDirectories)
			}
			return effect, err
		}
		result, err := p.copyOne(ctx, operationID, ordinal, plan)
		if err != nil {
			if len(effect.Affected) == 0 {
				p.cleanupDirectories(createdDirectories)
			}
			effect.ObservedAt = p.now()
			return effect, err
		}
		effect.Affected = append(effect.Affected, plan.source)
		if result.outcome == domain.OutcomeApplied {
			effect.Outcome = domain.OutcomeApplied
		} else if effect.Outcome == "" {
			effect.Outcome = domain.OutcomeAlreadySatisfied
		}
		effect.Evidence = append(effect.Evidence, result.evidence...)
		if err := p.appendJournal(ctx, FileEffect{
			OperationID: operationID, Ordinal: ordinal, Mode: TransferCopy,
			Source: plan.source, Destination: plan.destination,
			Outcome: result.outcome, Evidence: append([]string(nil), result.evidence...),
			ObservedAt: p.now(),
		}); err != nil {
			return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: err}
		}
	}
	if err := p.verifyDirectoriesStable(directories); err != nil {
		return effect, err
	}
	effect.ObservedAt = p.now()
	return effect, nil
}

func (p *Placer) runHardlink(ctx context.Context, operationID string, request ports.FilesystemHardlinkRequest) (ports.FilesystemEffect, error) {
	if err := validateOperationID(operationID); err != nil {
		return ports.FilesystemEffect{}, err
	}
	if err := request.Validate(); err != nil {
		return ports.FilesystemEffect{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	plans := make([]filePlan, 0, len(request.Files))
	for _, mapping := range request.Files {
		if err := p.checkDestinationRoot(mapping.Destination.RootID, "fs.hardlink"); err != nil {
			return ports.FilesystemEffect{}, err
		}
		if err := p.checkSourceRoot(mapping.Source.RootID); err != nil {
			return ports.FilesystemEffect{}, err
		}
		plans = append(plans, filePlan{source: mapping.Source, destination: mapping.Destination})
	}
	if err := checkHardlinkDestinations(ctx, p, plans); err != nil {
		return ports.FilesystemEffect{}, err
	}
	if err := ctx.Err(); err != nil {
		return ports.FilesystemEffect{}, err
	}

	effect := ports.FilesystemEffect{ObservedAt: p.now()}
	for ordinal, plan := range plans {
		if err := ctx.Err(); err != nil {
			return effect, err
		}
		result, err := p.hardlinkOne(ctx, operationID, ordinal, plan)
		if err != nil {
			return effect, err
		}
		effect.Affected = append(effect.Affected, plan.source)
		if result.outcome == domain.OutcomeApplied {
			effect.Outcome = domain.OutcomeApplied
		} else if effect.Outcome == "" {
			effect.Outcome = domain.OutcomeAlreadySatisfied
		}
		effect.Evidence = append(effect.Evidence, result.evidence...)
		if err := p.appendJournal(ctx, FileEffect{
			OperationID: operationID, Ordinal: ordinal, Mode: TransferHardlink,
			Source: plan.source, Destination: plan.destination,
			Outcome: result.outcome, Evidence: append([]string(nil), result.evidence...),
			ObservedAt: p.now(),
		}); err != nil {
			return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: err}
		}
	}
	effect.ObservedAt = p.now()
	return effect, nil
}

func (p *Placer) prepareCopy(ctx context.Context, request ports.FilesystemCopyRequest) ([]filePlan, []directorySnapshot, error) {
	plans, directories, err := p.expandCopyRequest(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	for _, directory := range directories {
		source, err := p.openSource(directory.source.RootID, directory.source.RelativePath)
		if err != nil {
			return nil, nil, fmt.Errorf("source directory %q: %w", directory.source.RelativePath, err)
		}
		if !source.info.IsDir() {
			source.close()
			return nil, nil, fmt.Errorf("%w: source %q is not a directory", ErrSourceChanged, directory.source.RelativePath)
		}
		if fileIdentity(source.info) != directory.source.FileIdentity {
			source.close()
			return nil, nil, fmt.Errorf("%w: source directory %q identity differs", ErrSourceChanged, directory.source.RelativePath)
		}
		if err := verifyDirectoryChildren(source.file, directory.source); err != nil {
			source.close()
			return nil, nil, err
		}
		source.close()
	}
	for _, plan := range plans {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		source, err := p.openSource(plan.source.RootID, plan.source.RelativePath)
		if err != nil {
			return nil, nil, fmt.Errorf("source file %q: %w", plan.source.RelativePath, err)
		}
		if source.info.IsDir() || !source.info.Mode().IsRegular() {
			source.close()
			return nil, nil, fmt.Errorf("%w: source %q is not a regular file", ErrSpecialFile, plan.source.RelativePath)
		}
		if fileIdentity(source.info) != plan.source.FileIdentity || source.info.Size() != plan.source.Size {
			source.close()
			return nil, nil, fmt.Errorf("%w: source file %q identity differs", ErrSourceChanged, plan.source.RelativePath)
		}
		source.close()
	}
	return plans, directories, nil
}

func (p *Placer) expandCopyRequest(ctx context.Context, request ports.FilesystemCopyRequest) ([]filePlan, []directorySnapshot, error) {
	if err := request.Validate(); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	plans := make([]filePlan, 0, len(request.Files))
	directories := make([]directorySnapshot, 0)
	destinations := make(map[string]struct{})
	for _, mapping := range request.Files {
		if err := p.checkSourceRoot(mapping.Source.RootID); err != nil {
			return nil, nil, err
		}
		if err := expandCopyMapping(mapping.Source, mapping.Destination, &plans, &directories, destinations); err != nil {
			return nil, nil, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return plans, directories, nil
}

func expandCopyMapping(source domain.FileManifestEntry, destination domain.FileTarget, plans *[]filePlan, directories *[]directorySnapshot, destinations map[string]struct{}) error {
	if source.Type != domain.ManifestDirectory {
		key := destination.RootID.String() + "\x00" + destination.RelativePath
		if _, exists := destinations[key]; exists {
			return fmt.Errorf("destination %q is selected more than once", destination.RelativePath)
		}
		destinations[key] = struct{}{}
		*plans = append(*plans, filePlan{source: source, destination: destination})
		return nil
	}
	if len(source.Children) == 0 {
		return fmt.Errorf("directory %q has no exact child manifest", source.RelativePath)
	}
	*directories = append(*directories, directorySnapshot{source: source, destination: destination, identity: source.FileIdentity})
	for _, child := range source.Children {
		suffix, ok := childSuffix(source.RelativePath, child.RelativePath)
		if !ok || suffix == "" || strings.Contains(suffix, "/") {
			return fmt.Errorf("directory %q child %q is not an immediate exact child", source.RelativePath, child.RelativePath)
		}
		childDestination := domain.FileTarget{RootID: destination.RootID, RelativePath: path.Join(destination.RelativePath, suffix)}
		if err := childDestination.Validate(); err != nil {
			return err
		}
		if err := expandCopyMapping(child, childDestination, plans, directories, destinations); err != nil {
			return err
		}
	}
	return nil
}

func childSuffix(parent, child string) (string, bool) {
	prefix := parent + "/"
	if !strings.HasPrefix(child, prefix) {
		return "", false
	}
	return strings.TrimPrefix(child, prefix), true
}

func verifyDirectoryChildren(directory *os.File, entry domain.FileManifestEntry) error {
	actual, err := listDirectoryNames(directory)
	if err != nil {
		return fmt.Errorf("%w: enumerate source directory %q: %v", ErrSourceChanged, entry.RelativePath, err)
	}
	wanted := make([]string, 0, len(entry.Children))
	for _, child := range entry.Children {
		suffix, ok := childSuffix(entry.RelativePath, child.RelativePath)
		if !ok || suffix == "" || strings.Contains(suffix, "/") {
			return fmt.Errorf("%w: invalid child manifest for %q", ErrInvalidPlan, entry.RelativePath)
		}
		wanted = append(wanted, suffix)
	}
	sort.Strings(actual)
	sort.Strings(wanted)
	if len(actual) != len(wanted) {
		return fmt.Errorf("%w: source directory %q child count changed", ErrSourceChanged, entry.RelativePath)
	}
	for index := range actual {
		if actual[index] != wanted[index] {
			return fmt.Errorf("%w: source directory %q child set changed", ErrSourceChanged, entry.RelativePath)
		}
	}
	return nil
}

func (p *Placer) verifyDirectoriesStable(directories []directorySnapshot) error {
	for _, directory := range directories {
		source, err := p.openSource(directory.source.RootID, directory.source.RelativePath)
		if err != nil {
			return fmt.Errorf("%w: source directory %q is unavailable after copy", ErrSourceChanged, directory.source.RelativePath)
		}
		if !source.info.IsDir() || fileIdentity(source.info) != directory.identity {
			source.close()
			return fmt.Errorf("%w: source directory %q changed during copy", ErrSourceChanged, directory.source.RelativePath)
		}
		if err := verifyDirectoryChildren(source.file, directory.source); err != nil {
			source.close()
			return err
		}
		source.close()
	}
	return nil
}

func (p *Placer) copyOne(ctx context.Context, operationID string, ordinal int, plan filePlan) (fileResult, error) {
	if err := ctx.Err(); err != nil {
		return fileResult{}, err
	}
	source, err := p.openSource(plan.source.RootID, plan.source.RelativePath)
	if err != nil {
		return fileResult{}, fmt.Errorf("source file %q: %w", plan.source.RelativePath, err)
	}
	defer source.close()
	if source.info.IsDir() || !source.info.Mode().IsRegular() {
		return fileResult{}, fmt.Errorf("%w: source file %q is not regular", ErrSpecialFile, plan.source.RelativePath)
	}
	if fileIdentity(source.info) != plan.source.FileIdentity || source.info.Size() != plan.source.Size {
		return fileResult{}, fmt.Errorf("%w: source file %q identity differs", ErrSourceChanged, plan.source.RelativePath)
	}
	parent, name, err := p.openDestinationParent(plan.destination)
	if err != nil {
		return fileResult{}, err
	}
	defer parent.Close()

	if existing, info, openErr := openExistingChild(parent, name); openErr == nil {
		defer existing.Close()
		if !info.Mode().IsRegular() || info.IsDir() {
			return fileResult{}, fmt.Errorf("%w: destination %q is not a regular file", ErrDestinationConflict, plan.destination.RelativePath)
		}
		if err := verifyDigest(existing, info, plan.source.Digest, p.opts.BufferSize, context.WithoutCancel(ctx)); err != nil {
			if errors.Is(err, ErrSourceChanged) {
				return fileResult{}, fmt.Errorf("%w: destination %q changed while inspected", ErrDestinationConflict, plan.destination.RelativePath)
			}
			return fileResult{}, fmt.Errorf("%w: destination %q has different content", ErrDestinationConflict, plan.destination.RelativePath)
		}
		if err := ctx.Err(); err != nil {
			return fileResult{}, err
		}
		return fileResult{outcome: domain.OutcomeAlreadySatisfied, evidence: fileEvidence(operationID, ordinal, plan, "already_satisfied")}, nil
	} else if !isNotExist(openErr) {
		return fileResult{}, fmt.Errorf("inspect destination %q: %w", plan.destination.RelativePath, openErr)
	}

	stageName := stageName(p.opts.StagePrefix, operationID, ordinal)
	stage, err := createExclusiveChild(parent, stageName)
	if err != nil {
		return fileResult{}, fmt.Errorf("create exclusive staging file: %w", err)
	}
	stageInfo, err := stage.Stat()
	if err != nil {
		_ = stage.Close()
		return fileResult{}, fmt.Errorf("inspect exclusive staging file: %w", err)
	}
	stageClosed := false
	cleanupStage := func() {
		if !stageClosed {
			_ = stage.Close()
			stageClosed = true
		}
		// Linux stages are anonymous inodes. Closing an unpublished stage
		// reclaims it without touching a pathname that another actor could have
		// substituted. Targets without this guarantee fail closed before copy.
	}

	digest, copyErr := copyAndDigest(ctx, source.file, source.info, stage, plan.source, p.opts.BufferSize)
	if copyErr != nil {
		cleanupStage()
		return fileResult{}, copyErr
	}
	if err := stage.Sync(); err != nil {
		cleanupStage()
		return fileResult{}, fmt.Errorf("sync staging file: %w", err)
	}
	if err := p.verifySourcePath(plan.source, source.info); err != nil {
		cleanupStage()
		return fileResult{}, err
	}
	if err := compareDigest(digest, plan.source.Digest); err != nil {
		cleanupStage()
		return fileResult{}, fmt.Errorf("%w: source file %q content differs", ErrSourceChanged, plan.source.RelativePath)
	}
	if err := ctx.Err(); err != nil {
		cleanupStage()
		return fileResult{}, err
	}
	if err := verifyOwnedStage(stage, stageInfo); err != nil {
		cleanupStage()
		return fileResult{}, err
	}
	if p.opts.beforeStagePublication != nil {
		p.opts.beforeStagePublication(parent, stageName)
	}
	if err := ctx.Err(); err != nil {
		cleanupStage()
		return fileResult{}, err
	}

	published, publishErr := publishNoReplace(stage, parent, stageName, name)
	if publishErr != nil {
		if !published {
			cleanupStage()
			if isExist(publishErr) {
				if existing, info, inspectErr := openExistingChild(parent, name); inspectErr == nil {
					defer existing.Close()
					if info.Mode().IsRegular() && !info.IsDir() && verifyDigest(existing, info, plan.source.Digest, p.opts.BufferSize, context.WithoutCancel(ctx)) == nil {
						if err := ctx.Err(); err != nil {
							return fileResult{}, err
						}
						return fileResult{outcome: domain.OutcomeAlreadySatisfied, evidence: fileEvidence(operationID, ordinal, plan, "already_satisfied_race")}, nil
					}
				}
				return fileResult{}, fmt.Errorf("%w: destination %q appeared during publication", ErrDestinationExists, plan.destination.RelativePath)
			}
			return fileResult{}, fmt.Errorf("publish destination %q: %w", plan.destination.RelativePath, publishErr)
		}
		// A successful no-replace link followed by a cleanup or durability
		// failure leaves a visible final object. Preserve it and reconcile.
		return fileResult{publication: true}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: %v", ErrPublicationUnknown, publishErr)}
	}
	if err := stage.Close(); err != nil {
		stageClosed = true
		return fileResult{publication: true}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: close staging file after publication: %v", ErrPublicationUnknown, err)}
	}
	stageClosed = true
	if err := p.syncDirectory(parent); err != nil {
		return fileResult{publication: true}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination directory sync failed: %v", ErrPublicationUnknown, err)}
	}
	if err := p.verifyPublishedCopy(plan, parent, name, context.WithoutCancel(ctx)); err != nil {
		return fileResult{publication: true}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: err}
	}
	if err := p.verifySourcePath(plan.source, source.info); err != nil {
		return fileResult{publication: true}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: err}
	}
	return fileResult{outcome: domain.OutcomeApplied, publication: true, evidence: fileEvidence(operationID, ordinal, plan, "applied")}, nil
}

func (p *Placer) hardlinkOne(ctx context.Context, operationID string, ordinal int, plan filePlan) (fileResult, error) {
	if err := ctx.Err(); err != nil {
		return fileResult{}, err
	}
	source, err := p.openSource(plan.source.RootID, plan.source.RelativePath)
	if err != nil {
		return fileResult{}, fmt.Errorf("source file %q: %w", plan.source.RelativePath, err)
	}
	defer source.close()
	if source.info.IsDir() || !source.info.Mode().IsRegular() {
		return fileResult{}, fmt.Errorf("%w: source file %q is not regular", ErrSpecialFile, plan.source.RelativePath)
	}
	if fileIdentity(source.info) != plan.source.FileIdentity || source.info.Size() != plan.source.Size {
		return fileResult{}, fmt.Errorf("%w: source file %q identity differs", ErrSourceChanged, plan.source.RelativePath)
	}
	parent, name, err := p.openDestinationParent(plan.destination)
	if err != nil {
		return fileResult{}, err
	}
	defer parent.Close()
	if existing, info, openErr := openExistingChild(parent, name); openErr == nil {
		defer existing.Close()
		if sameObject(source.info, info) {
			if err := ctx.Err(); err != nil {
				return fileResult{}, err
			}
			return fileResult{outcome: domain.OutcomeAlreadySatisfied, evidence: fileEvidence(operationID, ordinal, plan, "already_satisfied")}, nil
		}
		return fileResult{}, fmt.Errorf("%w: destination %q is a different file object", ErrDestinationConflict, plan.destination.RelativePath)
	} else if !isNotExist(openErr) {
		return fileResult{}, fmt.Errorf("inspect destination %q: %w", plan.destination.RelativePath, openErr)
	}
	if err := ctx.Err(); err != nil {
		return fileResult{}, err
	}
	if err := p.verifySourcePath(plan.source, source.info); err != nil {
		return fileResult{}, err
	}
	if p.opts.beforeHardlinkPublication != nil {
		p.opts.beforeHardlinkPublication(source.file)
	}
	if err := ctx.Err(); err != nil {
		return fileResult{}, err
	}
	published, publishErr := linkNoReplace(source.file, parent, name)
	if publishErr != nil {
		if !published {
			if isExist(publishErr) {
				if existing, info, inspectErr := openExistingChild(parent, name); inspectErr == nil {
					defer existing.Close()
					if sameObject(source.info, info) {
						if err := ctx.Err(); err != nil {
							return fileResult{}, err
						}
						return fileResult{outcome: domain.OutcomeAlreadySatisfied, evidence: fileEvidence(operationID, ordinal, plan, "already_satisfied_race")}, nil
					}
				}
				return fileResult{}, fmt.Errorf("%w: destination %q appeared during publication", ErrDestinationExists, plan.destination.RelativePath)
			}
			if isCrossDevice(publishErr) {
				return fileResult{}, fmt.Errorf("%w: source and destination are on different filesystems", ErrUnsupported)
			}
			return fileResult{}, fmt.Errorf("%w: hardlink destination %q: %v", ErrUnsupported, plan.destination.RelativePath, publishErr)
		}
		return fileResult{publication: true}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: %v", ErrPublicationUnknown, publishErr)}
	}
	if err := p.syncDirectory(parent); err != nil {
		return fileResult{publication: true}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination directory sync failed: %v", ErrPublicationUnknown, err)}
	}
	verified, info, verifyErr := openExistingChild(parent, name)
	if verifyErr != nil {
		return fileResult{publication: true}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: read-back destination: %v", ErrHardlinkIdentity, verifyErr)}
	}
	defer verified.Close()
	if !sameObject(source.info, info) {
		return fileResult{publication: true}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: ErrHardlinkIdentity}
	}
	if err := p.verifyPublishedHardlink(plan, source.info, parent, name); err != nil {
		return fileResult{publication: true}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: err}
	}
	if err := p.verifySourcePath(plan.source, source.info); err != nil {
		return fileResult{publication: true}, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: err}
	}
	return fileResult{outcome: domain.OutcomeApplied, publication: true, evidence: fileEvidence(operationID, ordinal, plan, "applied")}, nil
}

func copyAndDigest(ctx context.Context, source *os.File, sourceInfo fs.FileInfo, stage *os.File, expected domain.FileManifestEntry, bufferSize int) (string, error) {
	hasher := sha256.New()
	buffer := make([]byte, bufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if _, err := hasher.Write(buffer[:read]); err != nil {
				return "", err
			}
			written, err := stage.Write(buffer[:read])
			if err != nil {
				return "", fmt.Errorf("write staging file: %w", err)
			}
			if written != read {
				return "", io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("read source file %q: %w", expected.RelativePath, readErr)
		}
	}
	after, err := source.Stat()
	if err != nil {
		return "", fmt.Errorf("stat source after copy: %w", err)
	}
	if fileChanged(sourceInfo, after) || after.Size() != expected.Size {
		return "", fmt.Errorf("%w: source file %q changed during copy", ErrSourceChanged, expected.RelativePath)
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

func verifyDigest(file *os.File, before fs.FileInfo, expected string, bufferSize int, ctx context.Context) error {
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
	if fileChanged(before, after) {
		return ErrSourceChanged
	}
	return compareDigest("sha256:"+hex.EncodeToString(hasher.Sum(nil)), expected)
}

func compareDigest(actual, expected string) error {
	left := normalizeDigest(actual)
	right := normalizeDigest(expected)
	if left == "" || right == "" || left != right {
		return ErrDestinationConflict
	}
	return nil
}

func (p *Placer) verifyPublishedCopy(plan filePlan, parent *os.File, name string, ctx context.Context) error {
	file, info, err := openExistingChild(parent, name)
	if err != nil {
		return fmt.Errorf("%w: published destination disappeared", ErrReconciliationNeeded)
	}
	defer file.Close()
	if info.IsDir() || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: published destination is not regular", ErrReconciliationNeeded)
	}
	if info.Size() != plan.source.Size {
		return fmt.Errorf("%w: published destination size differs", ErrReconciliationNeeded)
	}
	if err := verifyDigest(file, info, plan.source.Digest, p.opts.BufferSize, ctx); err != nil {
		return fmt.Errorf("%w: published destination content differs", ErrReconciliationNeeded)
	}
	root, err := p.root(plan.destination.RootID)
	if err != nil {
		return err
	}
	visible, visibleParent, _, visibleInfo, err := openExistingTargetWithParent(root.Path, plan.destination.RelativePath)
	if err != nil {
		return fmt.Errorf("%w: published destination path read-back failed", ErrReconciliationNeeded)
	}
	defer visible.Close()
	defer visibleParent.Close()
	if !sameObject(info, visibleInfo) {
		return fmt.Errorf("%w: published destination path points to another object", ErrReconciliationNeeded)
	}
	if err := verifyDigest(visible, visibleInfo, plan.source.Digest, p.opts.BufferSize, ctx); err != nil {
		return fmt.Errorf("%w: published destination path content differs", ErrReconciliationNeeded)
	}
	return nil
}

func (p *Placer) verifyPublishedHardlink(plan filePlan, sourceInfo fs.FileInfo, parent *os.File, name string) error {
	file, info, err := openExistingChild(parent, name)
	if err != nil {
		return fmt.Errorf("%w: published hardlink read-back failed", ErrHardlinkIdentity)
	}
	file.Close()
	if !sameObject(sourceInfo, info) {
		return ErrHardlinkIdentity
	}
	root, err := p.root(plan.destination.RootID)
	if err != nil {
		return err
	}
	visible, visibleParent, _, visibleInfo, err := openExistingTargetWithParent(root.Path, plan.destination.RelativePath)
	if err != nil {
		return fmt.Errorf("%w: published hardlink path read-back failed", ErrHardlinkIdentity)
	}
	defer visible.Close()
	defer visibleParent.Close()
	if !sameObject(sourceInfo, visibleInfo) {
		return ErrHardlinkIdentity
	}
	return nil
}

func (p *Placer) reconcilePlans(ctx context.Context, operationID string, mode TransferMode, plans []filePlan) (ports.FilesystemEffect, error) {
	if err := validateOperationID(operationID); err != nil {
		return ports.FilesystemEffect{}, err
	}
	effect := ports.FilesystemEffect{ObservedAt: p.now()}
	for ordinal, plan := range plans {
		if err := ctx.Err(); err != nil {
			return effect, err
		}
		var source *sourceHandle
		if mode == TransferHardlink {
			var sourceErr error
			source, sourceErr = p.openSource(plan.source.RootID, plan.source.RelativePath)
			if sourceErr != nil {
				return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: source read-back failed: %v", ErrReconciliationNeeded, sourceErr)}
			}
			if sourceErr := validateSourceManifest(plan.source, source.info); sourceErr != nil {
				source.close()
				return effect, &UncertainError{
					OperationID: operationID,
					Ordinal:     ordinal,
					Cause:       fmt.Errorf("%w: %w", ErrReconciliationNeeded, sourceErr),
				}
			}
		}
		parent, name, parentErr := p.openExistingDestinationParent(plan.destination)
		if parentErr != nil {
			if source != nil {
				source.close()
			}
			return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination parent read-back failed: %v", ErrReconciliationNeeded, parentErr)}
		}
		destination, destinationInfo, destinationErr := openExistingChild(parent, name)
		if destinationErr != nil {
			parent.Close()
			if source != nil {
				source.close()
			}
			return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination %q is not materialized", ErrReconciliationNeeded, plan.destination.RelativePath)}
		}
		var verifyErr error
		if mode == TransferHardlink {
			if !sameObject(source.info, destinationInfo) {
				verifyErr = ErrHardlinkIdentity
			} else {
				verifyErr = p.verifyPublishedHardlink(plan, source.info, parent, name)
			}
		} else {
			verifyErr = p.verifyPublishedCopy(plan, parent, name, context.WithoutCancel(ctx))
		}
		destination.Close()
		parent.Close()
		if source != nil {
			source.close()
		}
		if verifyErr != nil {
			return effect, &UncertainError{OperationID: operationID, Ordinal: ordinal, Cause: fmt.Errorf("%w: destination %q verification failed: %v", ErrReconciliationNeeded, plan.destination.RelativePath, verifyErr)}
		}
		effect.Affected = append(effect.Affected, plan.source)
		if effect.Outcome == "" {
			effect.Outcome = domain.OutcomeAlreadySatisfied
		}
		effect.Evidence = append(effect.Evidence, fileEvidence(operationID, ordinal, plan, "reconciled")...)
	}
	effect.ObservedAt = p.now()
	return effect, nil
}

func (p *Placer) appendJournal(ctx context.Context, effect FileEffect) error {
	if p.opts.Journal == nil {
		return nil
	}
	if err := p.opts.Journal.Append(context.WithoutCancel(ctx), effect); err != nil {
		return fmt.Errorf("%w: %w", ErrJournalUnknown, err)
	}
	return nil
}

func fileEvidence(operationID string, ordinal int, plan filePlan, state string) []string {
	return []string{
		"operation=" + operationID,
		fmt.Sprintf("ordinal=%d", ordinal),
		"source_root=" + plan.source.RootID.String(),
		"source_path=" + plan.source.RelativePath,
		"destination_root=" + plan.destination.RootID.String(),
		"destination_path=" + plan.destination.RelativePath,
		"state=" + state,
	}
}

func (p *Placer) openSource(rootID domain.ConfigID, relative string) (*sourceHandle, error) {
	root, err := p.root(rootID)
	if err != nil {
		return nil, err
	}
	file, parent, name, info, err := openExistingTargetWithParent(root.Path, relative)
	if err != nil {
		return nil, err
	}
	return &sourceHandle{file: file, parent: parent, name: name, info: info}, nil
}

func (p *Placer) openDestinationParent(target domain.FileTarget) (*os.File, string, error) {
	return p.openDestinationParentWith(target, true)
}

func (p *Placer) openExistingDestinationParent(target domain.FileTarget) (*os.File, string, error) {
	return p.openDestinationParentWith(target, false)
}

func (p *Placer) openDestinationParentWith(target domain.FileTarget, create bool) (*os.File, string, error) {
	if err := target.Validate(); err != nil {
		return nil, "", fmt.Errorf("%w: destination target: %v", ErrInvalidPlan, err)
	}
	root, err := p.root(target.RootID)
	if err != nil {
		return nil, "", err
	}
	parentRelative := path.Dir(target.RelativePath)
	if parentRelative == "." {
		parentRelative = ""
	}
	var parent *os.File
	if create {
		parent, err = ensureDirectoryPath(root.Path, parentRelative, p.syncDirectory)
	} else {
		parent, err = openExistingDirectory(root.Path, parentRelative)
	}
	if err != nil {
		return nil, "", fmt.Errorf("open destination parent %q: %w", target.RelativePath, err)
	}
	return parent, path.Base(target.RelativePath), nil
}

func checkCopyDestinations(ctx context.Context, p *Placer, request ports.FilesystemCopyRequest, plans []filePlan, directories []directorySnapshot) error {
	for _, mapping := range request.Files {
		if err := p.checkDestinationRoot(mapping.Destination.RootID, "fs.copy"); err != nil {
			return err
		}
	}
	for _, directory := range directories {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.checkExistingDestinationDirectory(directory.destination); err != nil {
			return err
		}
	}
	for _, plan := range plans {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := p.checkCopyDestination(ctx, plan); err != nil {
			return err
		}
	}
	return nil
}

func checkHardlinkDestinations(ctx context.Context, p *Placer, plans []filePlan) error {
	for _, plan := range plans {
		if err := ctx.Err(); err != nil {
			return err
		}
		source, err := p.openSource(plan.source.RootID, plan.source.RelativePath)
		if err != nil {
			return fmt.Errorf("source file %q: %w", plan.source.RelativePath, err)
		}
		if source.info.IsDir() || !source.info.Mode().IsRegular() || fileIdentity(source.info) != plan.source.FileIdentity || source.info.Size() != plan.source.Size {
			source.close()
			return fmt.Errorf("%w: source file %q changed during destination check", ErrSourceChanged, plan.source.RelativePath)
		}
		parent, name, parentErr := p.openExistingDestinationParent(plan.destination)
		if parentErr != nil {
			source.close()
			if isNotExist(parentErr) {
				continue
			}
			return parentErr
		}
		existing, info, existingErr := openExistingChild(parent, name)
		if existingErr != nil {
			parent.Close()
			source.close()
			if isNotExist(existingErr) {
				continue
			}
			return fmt.Errorf("inspect destination %q: %w", plan.destination.RelativePath, existingErr)
		}
		existing.Close()
		parent.Close()
		source.close()
		if !sameObject(source.info, info) {
			return fmt.Errorf("%w: destination %q is a different file object", ErrDestinationConflict, plan.destination.RelativePath)
		}
	}
	return nil
}

func (p *Placer) checkExistingDestinationDirectory(target domain.FileTarget) error {
	if err := target.Validate(); err != nil {
		return fmt.Errorf("%w: destination directory: %v", ErrInvalidPlan, err)
	}
	root, err := p.root(target.RootID)
	if err != nil {
		return err
	}
	directory, err := openExistingDirectory(root.Path, target.RelativePath)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect destination directory %q: %w", target.RelativePath, err)
	}
	directory.Close()
	return nil
}

func (p *Placer) checkCopyDestination(ctx context.Context, plan filePlan) error {
	parent, name, err := p.openExistingDestinationParent(plan.destination)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return err
	}
	defer parent.Close()
	existing, info, err := openExistingChild(parent, name)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect destination %q: %w", plan.destination.RelativePath, err)
	}
	defer existing.Close()
	if info.IsDir() || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: destination %q is not a regular file", ErrDestinationConflict, plan.destination.RelativePath)
	}
	if err := verifyDigest(existing, info, plan.source.Digest, p.opts.BufferSize, ctx); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%w: destination %q has different content", ErrDestinationConflict, plan.destination.RelativePath)
	}
	return nil
}

func (p *Placer) verifySourcePath(entry domain.FileManifestEntry, expected fs.FileInfo) error {
	root, err := p.root(entry.RootID)
	if err != nil {
		return err
	}
	file, parent, _, info, err := openExistingTargetWithParent(root.Path, entry.RelativePath)
	if err != nil {
		return fmt.Errorf("%w: source path %q: %w", ErrSourceChanged, entry.RelativePath, err)
	}
	file.Close()
	parent.Close()
	if !sameObject(expected, info) || fileChanged(expected, info) || info.Size() != entry.Size {
		return fmt.Errorf("%w: source path %q no longer matches approved identity", ErrSourceChanged, entry.RelativePath)
	}
	return nil
}

func (p *Placer) ensureDestinationDirectory(target domain.FileTarget) ([]createdDirectory, error) {
	if err := target.Validate(); err != nil {
		return nil, fmt.Errorf("%w: destination directory: %v", ErrInvalidPlan, err)
	}
	root, err := p.root(target.RootID)
	if err != nil {
		return nil, err
	}
	directory, created, err := ensureDirectoryPathWithCreated(root.Path, target.RelativePath, p.syncDirectory)
	if directory != nil {
		directory.Close()
	}
	if err != nil {
		return makeCreatedDirectories(target.RootID, created), err
	}
	if err := p.syncDirectoryPath(root.Path, target.RelativePath); err != nil {
		return makeCreatedDirectories(target.RootID, created), fmt.Errorf("%w: sync destination directory %q: %w", ErrPublicationUnknown, target.RelativePath, err)
	}
	return makeCreatedDirectories(target.RootID, created), nil
}

func (p *Placer) syncDirectory(directory *os.File) error {
	if p.opts.SyncDirectory != nil {
		return p.opts.SyncDirectory(directory)
	}
	return syncDirectory(directory)
}

func (p *Placer) syncDirectoryPath(rootPath, relative string) error {
	directory, err := openExistingDirectory(rootPath, relative)
	if err != nil {
		return err
	}
	defer directory.Close()
	return p.syncDirectory(directory)
}

func makeCreatedDirectories(rootID domain.ConfigID, paths []string) []createdDirectory {
	created := make([]createdDirectory, 0, len(paths))
	for _, relative := range paths {
		created = append(created, createdDirectory{rootID: rootID, relative: relative})
	}
	return created
}

func (p *Placer) cleanupDirectories(paths []createdDirectory) {
	// There is no portable inode-conditional directory unlink operation. A
	// pathname can be replaced after creation and before error cleanup, so
	// preserving every created directory is safer than deleting an unowned
	// replacement. The root-relative candidates remain identifiable to the
	// durable janitor once that lifecycle is wired by the executor.
	_ = paths
}

func (p *Placer) root(id domain.ConfigID) (Root, error) {
	root, ok := p.roots[id]
	if !ok {
		return Root{}, fmt.Errorf("%w: %q", ErrRootNotConfigured, id)
	}
	return root, nil
}

func (p *Placer) checkSourceRoot(id domain.ConfigID) error {
	_, err := p.root(id)
	return err
}

func (p *Placer) checkDestinationRoot(id domain.ConfigID, capabilityName string) error {
	root, err := p.root(id)
	if err != nil {
		return err
	}
	if root.ReadOnly {
		return fmt.Errorf("%w: root %q", ErrReadOnly, id)
	}
	if !placementWritesSupported {
		return fmt.Errorf("%w: platform lacks the descriptor-relative no-follow placement guarantees", ErrUnsupported)
	}
	for _, capability := range root.Capabilities {
		if capability.Name != capabilityName {
			continue
		}
		if capability.State == domain.CapabilityUnsupported {
			return fmt.Errorf("%w: %s (%s)", ErrUnsupported, capabilityName, capability.Reason)
		}
		// Unknown capability is allowed to proceed only because the action
		// establishes action-time evidence with descriptor-relative syscalls.
	}
	return nil
}

func (p *Placer) newOperationID() (string, error) {
	id, err := domain.NewRuntimeID()
	if err != nil {
		return "", fmt.Errorf("generate filesystem operation id: %w", err)
	}
	return id.String(), nil
}

func (p *Placer) now() time.Time {
	if p.opts.Clock == nil {
		return time.Now().UTC()
	}
	return p.opts.Clock().UTC()
}

func validateOperationID(value string) error {
	if strings.TrimSpace(value) == "" || len(value) > 128 || strings.ContainsAny(value, `/\\`) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: operation id is invalid", ErrInvalidPlan)
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x21 || value[index] > 0x7e {
			return fmt.Errorf("%w: operation id is invalid", ErrInvalidPlan)
		}
	}
	return nil
}

func validStagePrefix(value string) bool {
	if value == "" || len(value) > 64 || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) || strings.ContainsRune(value, 0) {
		return false
	}
	return path.Base(value) == value
}

func stageName(prefix, operationID string, ordinal int) string {
	return fmt.Sprintf("%s-%s-%d.part", prefix, operationID, ordinal)
}

func normalizeDigest(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) != hex.EncodedLen(sha256.Size) {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func fileChanged(before, after fs.FileInfo) bool {
	return fileIdentity(before) != fileIdentity(after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime())
}

func validateSourceManifest(entry domain.FileManifestEntry, info fs.FileInfo) error {
	if info.IsDir() || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: source %q is not a regular file", ErrSourceChanged, entry.RelativePath)
	}
	if fileIdentity(info) != entry.FileIdentity || info.Size() != entry.Size {
		return fmt.Errorf("%w: source %q identity, size or mode differs", ErrSourceChanged, entry.RelativePath)
	}
	return nil
}

func relativeParts(relative string) []string {
	if relative == "" {
		return nil
	}
	return strings.Split(relative, "/")
}
