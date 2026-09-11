// Package observe provides read-only, root-confined filesystem observations.
//
// Paths accepted by this package are always relative to a configured root and
// use slash separators. Linux and Darwin implementations resolve every path component
// through directory descriptors with O_NOFOLLOW. This keeps an observation
// bound to the configured root even when another process replaces a directory
// while it is being walked.
package observe

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

const (
	// DefaultEnumerationLimit keeps a caller that omits a page size bounded.
	DefaultEnumerationLimit = 1_000
	// DefaultEnumerationMax is the largest page this observer will serve.
	DefaultEnumerationMax = 10_000
	// DefaultHashBufferSize bounds memory used while hashing a file.
	DefaultHashBufferSize = 1 << 20
)

var (
	ErrRootNotConfigured      = errors.New("filesystem root is not configured")
	ErrRootTarget             = errors.New("filesystem root target is not allowed")
	ErrPathEscape             = errors.New("filesystem path escapes configured root")
	ErrSymlink                = errors.New("filesystem symlink is not allowed")
	ErrSpecialFile            = errors.New("filesystem special file is not allowed")
	ErrChanged                = errors.New("filesystem object changed during observation")
	ErrEnumerationLimit       = errors.New("filesystem enumeration limit reached")
	ErrEnumerationCursor      = errors.New("filesystem enumeration cursor is expired or unavailable")
	ErrEnumerationStale       = fmt.Errorf("%w: cursor is stale", ErrEnumerationCursor)
	ErrEnumerationCursorStale = ErrEnumerationStale
	ErrAmbiguousMapping       = errors.New("filesystem path mapping is ambiguous")
	ErrMappingMismatch        = errors.New("filesystem path does not match mapping")
	ErrDirectory              = errors.New("filesystem directory cannot be hashed")
)

// Root is the host path for one configured storage root. Revision is copied
// into coverage evidence when supplied; it is never used to resolve paths.
type Root struct {
	ID           domain.ConfigID
	Path         string
	Revision     string
	ReadOnly     bool
	Capabilities []domain.Capability
}

// Options bounds an observer. Zero values select safe defaults.
type Options struct {
	EnumerationLimit int
	EnumerationMax   int
	HashBufferSize   int
}

// Observer implements the read-only filesystem port.
type Observer struct {
	roots    map[domain.ConfigID]Root
	opts     Options
	cursorMu sync.Mutex
	cursors  map[string]*enumerationCursorState
}

// FilesystemObserver names the concrete adapter when dependency wiring wants
// the longer role-oriented type name.
type FilesystemObserver = Observer

var _ ports.FilesystemReadPort = (*Observer)(nil)

// New creates an observer without touching configured roots. A missing mount
// is reported as unknown by Capabilities and as an error by a requested read.
func New(roots []Root, options Options) (*Observer, error) {
	options = normalizeOptions(options)
	configured := make(map[domain.ConfigID]Root, len(roots))
	for _, root := range roots {
		if !root.ID.Valid() {
			return nil, fmt.Errorf("%w: invalid root id", ErrRootNotConfigured)
		}
		if strings.IndexByte(root.Path, 0) >= 0 || !pathIsAbsolute(root.Path) {
			return nil, fmt.Errorf("%w: root path must be absolute", ErrRootNotConfigured)
		}
		cleaned := cleanHostPath(root.Path)
		root.Path = cleaned
		if _, exists := configured[root.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate root id %q", ErrRootNotConfigured, root.ID)
		}
		configured[root.ID] = root
	}
	return &Observer{roots: configured, opts: options, cursors: make(map[string]*enumerationCursorState)}, nil
}

// NewFromStorageRoots adapts effective configuration roots to the observer.
func NewFromStorageRoots(roots []domain.StorageRoot, options Options) (*Observer, error) {
	configured := make([]Root, 0, len(roots))
	for _, root := range roots {
		configured = append(configured, Root{
			ID: root.ID, Path: root.Path, Revision: root.Revision,
			ReadOnly: root.ReadOnly, Capabilities: append([]domain.Capability(nil), root.Capabilities...),
		})
	}
	return New(configured, options)
}

func normalizeOptions(options Options) Options {
	if options.EnumerationLimit <= 0 {
		options.EnumerationLimit = DefaultEnumerationLimit
	}
	if options.EnumerationMax <= 0 {
		options.EnumerationMax = DefaultEnumerationMax
	}
	if options.EnumerationLimit > options.EnumerationMax {
		options.EnumerationLimit = options.EnumerationMax
	}
	if options.HashBufferSize <= 0 {
		options.HashBufferSize = DefaultHashBufferSize
	}
	return options
}

func (o *Observer) root(id domain.ConfigID) (Root, error) {
	root, ok := o.roots[id]
	if !ok {
		return Root{}, fmt.Errorf("%w: %q", ErrRootNotConfigured, id)
	}
	return root, nil
}

// Stat obtains exact object identity and mode for a non-root target. It does
// not follow symlinks and reports effective access based on the opened object.
func (o *Observer) Stat(ctx context.Context, target domain.FileTarget) (ports.FilesystemObservation, error) {
	if err := ctx.Err(); err != nil {
		return ports.FilesystemObservation{}, err
	}
	if err := validateTarget(target); err != nil {
		return ports.FilesystemObservation{}, err
	}
	root, err := o.root(target.RootID)
	if err != nil {
		return ports.FilesystemObservation{}, err
	}
	file, info, err := openConstrainedTarget(root.Path, target.RelativePath)
	if err != nil {
		return ports.FilesystemObservation{}, wrapObservationError(target, err)
	}
	defer file.Close()
	if err := ctx.Err(); err != nil {
		return ports.FilesystemObservation{}, err
	}
	entry, err := manifestEntry(target.RootID, target.RelativePath, info, time.Now().UTC())
	if err != nil {
		return ports.FilesystemObservation{}, err
	}
	readable, writable := accessForOpened(root, target, info)
	return ports.FilesystemObservation{
		Entry: entry, Mode: info.Mode().String(), Readable: readable,
		Writable: writable, ObservedAt: entry.ObservedAt,
	}, nil
}

// Hash calculates a SHA-256 digest of one regular file. Identity, size and
// modification time are checked again after reading, so a changing source
// cannot produce approved evidence silently.
func (o *Observer) Hash(ctx context.Context, target domain.FileTarget) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validateTarget(target); err != nil {
		return "", err
	}
	root, err := o.root(target.RootID)
	if err != nil {
		return "", err
	}
	file, before, err := openConstrainedTarget(root.Path, target.RelativePath)
	if err != nil {
		return "", wrapObservationError(target, err)
	}
	defer file.Close()
	if before.IsDir() {
		return "", fmt.Errorf("%w: %q", ErrDirectory, target.RelativePath)
	}
	if !before.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %q", ErrSpecialFile, target.RelativePath)
	}

	digest := sha256.New()
	buffer := make([]byte, o.opts.HashBufferSize)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			if _, err := digest.Write(buffer[:read]); err != nil {
				return "", fmt.Errorf("hash %q: %w", target.RelativePath, err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("hash %q: %w", target.RelativePath, readErr)
		}
	}
	after, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat hashed file %q: %w", target.RelativePath, err)
	}
	if fileChanged(before, after) {
		return "", fmt.Errorf("%w: %q", ErrChanged, target.RelativePath)
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

// Enumerate returns one bounded page of direct children below relativePrefix.
// Prefix "" means the configured root itself; that is the only permitted root
// observation target. Use EnumeratePage when a caller needs continuation.
func (o *Observer) Enumerate(ctx context.Context, rootID domain.ConfigID, relativePrefix string, limit int) (ports.Page[domain.FileManifestEntry], error) {
	return o.EnumeratePage(ctx, rootID, relativePrefix, "", limit)
}

// EnumeratePage continues a bounded directory page using an opaque cursor.
// Cursor state contains directory identity, mtime, one coverage identity,
// cumulative count, generation and a compact prior-partial marker. Each
// generation is single-use; replayed or concurrent old tokens return a stale
// cursor error without advancing the stream. The observer retains a capped
// descriptor stream for each active cursor, so continuation never replays or
// materializes a directory snapshot. A changed directory is rejected instead
// of silently mixing two snapshots. Cancellation invalidates a continuation
// after any read. Cursors expire after a short idle period and are invalid
// after process restart.
func (o *Observer) EnumeratePage(ctx context.Context, rootID domain.ConfigID, relativePrefix, cursor string, limit int) (ports.Page[domain.FileManifestEntry], error) {
	var page ports.Page[domain.FileManifestEntry]
	started := time.Now().UTC()
	if err := ctx.Err(); err != nil {
		if cursor != "" {
			if cursorValue, decodeErr := decodeCursor(cursor); decodeErr == nil {
				o.invalidateEnumerationCursor(cursorValue.CursorID.String(), cursorValue.Generation)
			}
		}
		return page, err
	}
	if err := validatePrefix(relativePrefix); err != nil {
		return page, err
	}
	root, err := o.root(rootID)
	if err != nil {
		return page, err
	}
	limit, err = o.pageLimit(limit)
	if err != nil {
		return page, err
	}
	cursorValue, err := decodeCursor(cursor)
	if err != nil {
		return page, err
	}

	continuation := cursor != ""
	var (
		state         *enumerationCursorState
		cursorID      domain.RuntimeID
		directory     *os.File
		directoryInfo fs.FileInfo
		sourceID      domain.RuntimeID
		priorPartial  bool
		stream        *directoryCursor
		keepState     bool
		dropState     bool
	)
	if continuation {
		if cursorValue.Prefix != relativePrefix {
			return page, fmt.Errorf("%w: cursor belongs to another directory", ErrChanged)
		}
		cursorID = cursorValue.CursorID
		var found bool
		state, found = o.lockEnumerationCursor(cursorID.String())
		if !found {
			return page, ErrEnumerationStale
		}
		// State lock stays held until this call finishes. This serializes two
		// continuations for one cursor and protects its stream position.
		defer func() {
			// Invalidate before releasing state.mu. A same-token caller may
			// already be queued on this mutex; it must observe cancellation
			// before it can read or advance the retained directory stream.
			if dropState {
				state.invalidated = true
			}
			state.mu.Unlock()
			if dropState {
				o.dropEnumerationCursor(cursorID.String(), state)
			}
		}()
		if state.invalidated {
			return page, ErrEnumerationStale
		}
		if state.rootID != rootID || state.prefix != relativePrefix ||
			state.directoryID != cursorValue.DirectoryID ||
			state.directoryMTime != cursorValue.DirectoryMTime ||
			state.sourceID != cursorValue.SourceID ||
			!state.startedAt.Equal(cursorValue.StartedAt) ||
			state.priorPartial != cursorValue.PriorPartial ||
			state.generation != cursorValue.Generation ||
			state.observedCount != cursorValue.ObservedCount {
			if state.generation != cursorValue.Generation {
				return page, fmt.Errorf("%w: cursor generation already consumed", ErrEnumerationStale)
			}
			dropState = true
			return page, fmt.Errorf("%w: cursor metadata changed", ErrChanged)
		}
		directory = state.directory
		stream = state.stream
		sourceID = state.sourceID
		started = state.startedAt
		priorPartial = state.priorPartial
		if directory == nil || stream == nil {
			dropState = true
			return page, ErrEnumerationCursor
		}
		directoryInfo, err = directory.Stat()
		if err != nil {
			dropState = true
			return page, fmt.Errorf("stat enumeration directory: %w", err)
		}
		if fileIdentity(directoryInfo) != state.directoryID || directoryInfo.ModTime().UnixNano() != state.directoryMTime {
			dropState = true
			return page, fmt.Errorf("%w: enumeration directory changed", ErrChanged)
		}
		// Re-open the configured path for each continuation to notice a path
		// replacement while retaining the original descriptor for no-gap reads.
		current, currentErr := openConstrainedDirectory(root.Path, relativePrefix)
		if currentErr != nil {
			return page, wrapObservationError(domain.FileTarget{RootID: rootID, RelativePath: relativePrefix}, currentErr)
		}
		currentInfo, currentStatErr := current.Stat()
		current.Close()
		if currentStatErr != nil {
			return page, fmt.Errorf("stat enumeration directory: %w", currentStatErr)
		}
		if fileIdentity(currentInfo) != state.directoryID || currentInfo.ModTime().UnixNano() != state.directoryMTime {
			dropState = true
			return page, fmt.Errorf("%w: enumeration directory changed", ErrChanged)
		}
	} else {
		sourceID, err = domain.NewRuntimeID()
		if err != nil {
			return page, fmt.Errorf("create enumeration source: %w", err)
		}
		directory, err = openConstrainedDirectory(root.Path, relativePrefix)
		if err != nil {
			if relativePrefix == "" && errors.Is(err, ErrRootTarget) {
				return page, err
			}
			return page, wrapObservationError(domain.FileTarget{RootID: rootID, RelativePath: relativePrefix}, err)
		}
		defer func() {
			if !keepState {
				_ = directory.Close()
			}
		}()
		directoryInfo, err = directory.Stat()
		if err != nil {
			return page, fmt.Errorf("stat enumeration directory: %w", err)
		}
		state = &enumerationCursorState{
			directory: directory, stream: newDirectoryCursor(directory),
			rootID: rootID, prefix: relativePrefix,
			directoryID:    fileIdentity(directoryInfo),
			directoryMTime: directoryInfo.ModTime().UnixNano(),
			sourceID:       sourceID, startedAt: started, generation: 1,
		}
		stream = state.stream
	}

	var inspected int
	var exhausted bool
	var unsupported []string
	page.Items, inspected, exhausted, unsupported, err = readEntries(ctx, stream, rootID, relativePrefix, limit)
	if err != nil {
		if continuation {
			dropState = true
		}
		return page, err
	}
	if err := ctx.Err(); err != nil {
		if continuation {
			dropState = true
		}
		return page, err
	}
	state.observedCount += int64(len(page.Items))
	observed := time.Now().UTC()
	page.Coverage = coverage(root, sourceID, state.observedCount, started, observed)
	if priorPartial {
		page.Coverage.Completeness = domain.CompletenessPartial
		page.Coverage.ReasonCodes = append(page.Coverage.ReasonCodes, "unsupported_child_prior")
	}
	page.Coverage.ReasonCodes = append(page.Coverage.ReasonCodes, unsupported...)
	if len(unsupported) > 0 {
		page.Coverage.Completeness = domain.CompletenessPartial
	}
	more := !exhausted && inspected == limit
	if more {
		state.priorPartial = priorPartial || len(unsupported) > 0
		state.touch()
		if !continuation {
			cursorID, err = domain.NewRuntimeID()
			if err != nil {
				return page, fmt.Errorf("create enumeration cursor: %w", err)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		if continuation {
			dropState = true
		}
		return page, err
	}
	finalInfo, err := directory.Stat()
	if err != nil {
		if continuation {
			dropState = true
		}
		return page, fmt.Errorf("stat enumeration directory after read: %w", err)
	}
	if fileChanged(directoryInfo, finalInfo) {
		if continuation {
			dropState = true
		}
		return page, fmt.Errorf("%w: enumeration directory changed", ErrChanged)
	}
	if err := ctx.Err(); err != nil {
		if continuation {
			dropState = true
		}
		return page, err
	}
	if continuation {
		// Consume token generation before releasing state lock. A concurrent
		// replay then observes stale generation even if it acquired the map
		// before this call removes a terminal cursor.
		state.generation++
	}
	if more {
		page.NextCursor = encodeCursor(cursorState{
			CursorID: cursorID, DirectoryID: state.directoryID,
			DirectoryMTime: state.directoryMTime, Prefix: relativePrefix,
			SourceID: sourceID, StartedAt: started,
			PriorPartial: state.priorPartial, Generation: state.generation,
			ObservedCount: state.observedCount,
		})
		page.Coverage.Completeness = domain.CompletenessPartial
		page.Coverage.ReasonCodes = append(page.Coverage.ReasonCodes, "enumeration_limit")
	}
	if page.NextCursor == "" {
		completed := time.Now().UTC()
		page.Coverage.CompletedAt = &completed
		page.Coverage.ObservedAt = completed
		if continuation {
			dropState = true
		}
	}
	if page.NextCursor != "" && !continuation {
		o.storeEnumerationCursor(cursorID.String(), state)
		keepState = true
	}
	return page, nil
}

func (o *Observer) pageLimit(requested int) (int, error) {
	if requested <= 0 {
		requested = o.opts.EnumerationLimit
	}
	if requested > o.opts.EnumerationMax {
		return 0, fmt.Errorf("%w: requested %d exceeds maximum %d", ErrEnumerationLimit, requested, o.opts.EnumerationMax)
	}
	return requested, nil
}

// Capabilities reports read and write capability evidence without mutating a
// root. Missing mounts and inaccessible roots remain unknown.
func (o *Observer) Capabilities(ctx context.Context, rootID domain.ConfigID) ([]domain.Capability, error) {
	root, err := o.root(rootID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	names := []string{
		"fs.enumerate", "fs.stat", "fs.hash", "fs.copy", "fs.hardlink",
		"fs.move", "fs.rename", "fs.trash", "fs.restore", "fs.delete",
		"fs.no_follow",
	}
	result := make([]domain.Capability, 0, len(names)+1)
	rootFile, openErr := openConstrainedDirectory(root.Path, "")
	if openErr != nil {
		defaultReason := capabilityReason(openErr)
		for _, name := range names {
			state := domain.CapabilityUnknown
			reason := defaultReason
			if name == "fs.no_follow" {
				state, reason = noFollowCapability(), ""
			}
			result = append(result, mergeConfiguredCapability(root, name, state, reason, now))
		}
		return result, nil
	}
	defer rootFile.Close()
	writable := !root.ReadOnly && writableDirectory(rootFile)
	readState := domain.CapabilitySupported
	writeState := domain.CapabilityUnsupported
	writeReason := "root is read-only"
	if !root.ReadOnly {
		writeReason = "root is not writable to effective process credentials"
	}
	if writable {
		writeState = domain.CapabilitySupported
		writeReason = ""
	}
	for _, name := range names {
		state := readState
		reason := ""
		switch {
		case name == "fs.no_follow":
			state = noFollowCapability()
		case isMutationCapability(name):
			state, reason = writeState, writeReason
		}
		if isSourceDestinationCapability(name) && state != domain.CapabilityUnsupported {
			state, reason = domain.CapabilityUnknown, "selected source and destination filesystem support requires action-time evidence"
		}
		result = append(result, mergeConfiguredCapability(root, name, state, reason, now))
	}
	return result, nil
}

func isMutationCapability(name string) bool {
	switch name {
	case "fs.copy", "fs.hardlink", "fs.move", "fs.rename", "fs.trash", "fs.restore", "fs.delete":
		return true
	default:
		return false
	}
}

func isSourceDestinationCapability(name string) bool {
	switch name {
	case "fs.hardlink", "fs.move", "fs.rename":
		return true
	default:
		return false
	}
}

// mergeConfiguredCapability applies the authority supplied with a root after
// checking current physical evidence. A physical prohibition is stronger than
// stale configuration, while configured unsupported/unknown states cannot be
// promoted by a permission probe.
func mergeConfiguredCapability(root Root, name string, observedState domain.CapabilityState, observedReason string, now time.Time) domain.Capability {
	capability := domain.Capability{Name: name, State: observedState, Reason: observedReason, ObservedAt: now}
	var configured *domain.Capability
	for index := range root.Capabilities {
		if root.Capabilities[index].Name == name {
			configured = &root.Capabilities[index]
			break
		}
	}
	if configured == nil {
		return capability
	}
	capability.Version = configured.Version
	capability.Evidence = append([]string(nil), configured.Evidence...)
	switch {
	case observedState == domain.CapabilityUnsupported:
		capability.State = domain.CapabilityUnsupported
		if configured.State == domain.CapabilityUnsupported && configured.Reason != "" {
			capability.Reason = configured.Reason
		}
	case configured.State == domain.CapabilityUnsupported:
		capability.State = domain.CapabilityUnsupported
		capability.Reason = configured.Reason
		if capability.Reason == "" {
			capability.Reason = "configured unsupported"
		}
	case observedState == domain.CapabilityUnknown:
		capability.State = domain.CapabilityUnknown
		if configured.State == domain.CapabilityUnknown && configured.Reason != "" {
			capability.Reason = configured.Reason
		}
	case configured.State == domain.CapabilityUnknown:
		capability.State = domain.CapabilityUnknown
		capability.Reason = configured.Reason
		if capability.Reason == "" {
			capability.Reason = "configured capability is unverified"
		}
	case configured.State == domain.CapabilitySupported && configured.Reason != "":
		capability.Reason = configured.Reason
	}
	return capability
}

func coverage(root Root, source domain.RuntimeID, count int64, started, observed time.Time) domain.Coverage {
	return domain.Coverage{
		SourceID: source, RootID: root.ID, Completeness: domain.CompletenessComplete,
		ObservedCount: int64(count), SnapshotRevision: root.Revision,
		StartedAt: &started, ObservedAt: observed,
	}
}

func manifestEntry(rootID domain.ConfigID, relativePath string, info fs.FileInfo, observed time.Time) (domain.FileManifestEntry, error) {
	typeOf := domain.ManifestFile
	role := domain.RoleCompanion
	if info.IsDir() {
		typeOf, role = domain.ManifestDirectory, ""
	} else if !info.Mode().IsRegular() {
		return domain.FileManifestEntry{}, fmt.Errorf("%w: %q", ErrSpecialFile, relativePath)
	} else if isSubtitle(relativePath) {
		typeOf, role = domain.ManifestSubtitle, domain.RoleSubtitle
	} else if isVideo(relativePath) {
		role = domain.RoleVideo
	}
	return domain.FileManifestEntry{
		RootID: rootID, RelativePath: relativePath, Type: typeOf,
		Size: info.Size(), FileIdentity: fileIdentity(info), Role: role,
		ObservedAt: observed,
	}, nil
}

// unsupportedChildReasonCode keeps one unobservable child visible through the
// frozen coverage contract without pretending it is a manifest entry. The
// wire type and parser belong to ports so downstream consumers do not import
// this adapter package.
func unsupportedChildReasonCode(relativePath, reason string) string {
	evidence, _ := json.Marshal(ports.UnsupportedChildEvidence{RelativePath: relativePath, Reason: reason})
	return "unsupported_child:" + string(evidence)
}

func unsupportedChildReason(err error) string {
	switch {
	case errors.Is(err, ErrSymlink):
		return "symlink"
	case errors.Is(err, ErrSpecialFile):
		return "special_file"
	case errors.Is(err, fs.ErrPermission):
		return "permission_denied"
	case errors.Is(err, fs.ErrNotExist):
		return "disappeared"
	case errors.Is(err, ErrPathEscape):
		return "invalid_name"
	default:
		return "observation_failed"
	}
}

func readEntries(ctx context.Context, directory *directoryCursor, rootID domain.ConfigID, prefix string, limit int) ([]domain.FileManifestEntry, int, bool, []string, error) {
	entries := make([]domain.FileManifestEntry, 0, limit)
	unsupported := make([]string, 0)
	inspected := 0
	exhausted := false
	for inspected < limit {
		if err := ctx.Err(); err != nil {
			return nil, inspected, exhausted, unsupported, err
		}
		directoryEntry, err := directory.next(ctx)
		if errors.Is(err, io.EOF) {
			exhausted = true
			break
		}
		if err != nil {
			return nil, inspected, exhausted, unsupported, err
		}
		inspected++
		name := directoryEntry.Name
		if strings.Contains(name, "/") || strings.Contains(name, "\\") || name == "." || name == ".." {
			unsupported = append(unsupported, unsupportedChildReasonCode(path.Join(prefix, name), "invalid_name"))
			if err := ctx.Err(); err != nil {
				return nil, inspected, exhausted, unsupported, err
			}
			continue
		}
		relativePath := path.Join(prefix, name)
		if prefix == "" {
			relativePath = name
		}
		child, info, err := openConstrainedChild(directory.file, name)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, inspected, exhausted, unsupported, ctxErr
			}
			unsupported = append(unsupported, unsupportedChildReasonCode(relativePath, unsupportedChildReason(err)))
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, inspected, exhausted, unsupported, ctxErr
			}
			continue
		}
		entry, entryErr := manifestEntry(rootID, relativePath, info, time.Now().UTC())
		child.Close()
		if entryErr != nil {
			unsupported = append(unsupported, unsupportedChildReasonCode(relativePath, unsupportedChildReason(entryErr)))
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, inspected, exhausted, unsupported, ctxErr
			}
			continue
		}
		entries = append(entries, entry)
		if err := ctx.Err(); err != nil {
			return nil, inspected, exhausted, unsupported, err
		}
	}
	return entries, inspected, exhausted, unsupported, nil
}

type cursorState struct {
	CursorID       domain.RuntimeID `json:"cursorId"`
	DirectoryID    string           `json:"directoryId"`
	DirectoryMTime int64            `json:"directoryMtime"`
	Prefix         string           `json:"prefix"`
	SourceID       domain.RuntimeID `json:"sourceId"`
	StartedAt      time.Time        `json:"startedAt"`
	PriorPartial   bool             `json:"priorPartial"`
	Generation     uint64           `json:"generation"`
	ObservedCount  int64            `json:"observedCount"`
}

func encodeCursor(state cursorState) string {
	encoded, _ := json.Marshal(state)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeCursor(value string) (cursorState, error) {
	if value == "" {
		return cursorState{}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursorState{}, fmt.Errorf("invalid enumeration cursor: %w", err)
	}
	var state cursorState
	if err := json.Unmarshal(decoded, &state); err != nil || !state.CursorID.Valid() || state.DirectoryID == "" || state.Generation == 0 || state.ObservedCount < 0 {
		return cursorState{}, errors.New("invalid enumeration cursor")
	}
	if !state.SourceID.Valid() || state.StartedAt.IsZero() {
		return cursorState{}, errors.New("invalid enumeration cursor")
	}
	return state, nil
}

func validateTarget(target domain.FileTarget) error {
	if err := target.Validate(); err != nil {
		if target.RelativePath == "" || target.RelativePath == "." {
			return fmt.Errorf("%w: %v", ErrRootTarget, err)
		}
		if hasTraversalComponent(target.RelativePath) {
			return fmt.Errorf("%w: %v", ErrPathEscape, err)
		}
		return err
	}
	return nil
}

func validatePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if err := domain.ValidateRelativePath(prefix); err != nil {
		if hasTraversalComponent(prefix) {
			return fmt.Errorf("%w: %v", ErrPathEscape, err)
		}
		return err
	}
	return nil
}

func hasTraversalComponent(value string) bool {
	for _, component := range strings.Split(value, "/") {
		if component == "." || component == ".." {
			return true
		}
	}
	return false
}

func wrapObservationError(target domain.FileTarget, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("observe %q: %w", target.RelativePath, err)
}

func accessForOpened(root Root, target domain.FileTarget, info fs.FileInfo) (readable, writable bool) {
	readable = true
	if root.ReadOnly {
		return readable, false
	}
	return readable, writablePath(root.Path, target.RelativePath, info.IsDir())
}

func fileChanged(before, after fs.FileInfo) bool {
	return fileIdentity(before) != fileIdentity(after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime())
}

func isSubtitle(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".srt", ".ass", ".ssa", ".vtt", ".idx", ".sub", ".sup":
		return true
	default:
		return false
	}
}

func isVideo(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".mkv", ".mp4", ".m4v", ".avi", ".mov", ".wmv", ".webm", ".ts", ".m2ts", ".mpeg", ".mpg":
		return true
	default:
		return false
	}
}

func capabilityReason(err error) string {
	switch {
	case errors.Is(err, ErrSymlink):
		return "root contains a symlink component"
	case errors.Is(err, ErrSpecialFile):
		return "root is not a directory"
	case errors.Is(err, fs.ErrPermission):
		return "root is not accessible to effective process credentials"
	default:
		return "root is unavailable"
	}
}
