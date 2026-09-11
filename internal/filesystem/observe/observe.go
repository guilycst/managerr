// Package observe provides read-only, root-confined filesystem observations.
//
// Paths accepted by this package are always relative to a configured root and
// use slash separators. Unix implementations resolve every path component
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
	"time"

	"github.com/guilycst/managerr/internal/domain"
	"github.com/guilycst/managerr/internal/ports"
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
	ErrRootNotConfigured = errors.New("filesystem root is not configured")
	ErrRootTarget        = errors.New("filesystem root target is not allowed")
	ErrPathEscape        = errors.New("filesystem path escapes configured root")
	ErrSymlink           = errors.New("filesystem symlink is not allowed")
	ErrSpecialFile       = errors.New("filesystem special file is not allowed")
	ErrChanged           = errors.New("filesystem object changed during observation")
	ErrEnumerationLimit  = errors.New("filesystem enumeration limit reached")
	ErrAmbiguousMapping  = errors.New("filesystem path mapping is ambiguous")
	ErrMappingMismatch   = errors.New("filesystem path does not match mapping")
	ErrDirectory         = errors.New("filesystem directory cannot be hashed")
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
	roots map[domain.ConfigID]Root
	opts  Options
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
	return &Observer{roots: configured, opts: options}, nil
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
// Cursor state contains directory identity and mtime; a changed directory is
// rejected instead of silently mixing two snapshots.
func (o *Observer) EnumeratePage(ctx context.Context, rootID domain.ConfigID, relativePrefix, cursor string, limit int) (ports.Page[domain.FileManifestEntry], error) {
	var page ports.Page[domain.FileManifestEntry]
	started := time.Now().UTC()
	if err := ctx.Err(); err != nil {
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
	offset, expected := cursorValue.Offset, cursorValue.DirectoryID
	if cursor != "" && cursorValue.Prefix != relativePrefix {
		return page, fmt.Errorf("%w: cursor belongs to another directory", ErrChanged)
	}
	if offset > o.opts.EnumerationMax {
		return page, fmt.Errorf("%w: cursor offset exceeds maximum", ErrEnumerationLimit)
	}
	directory, err := openConstrainedDirectory(root.Path, relativePrefix)
	if err != nil {
		if relativePrefix == "" && errors.Is(err, ErrRootTarget) {
			return page, err
		}
		return page, wrapObservationError(domain.FileTarget{RootID: rootID, RelativePath: relativePrefix}, err)
	}
	defer directory.Close()
	directoryInfo, err := directory.Stat()
	if err != nil {
		return page, fmt.Errorf("stat enumeration directory: %w", err)
	}
	directoryID := fileIdentity(directoryInfo)
	if expected != "" && expected != directoryID {
		return page, fmt.Errorf("%w: enumeration directory identity changed", ErrChanged)
	}
	if offset > 0 && cursorMTime(cursor) != directoryInfo.ModTime().UnixNano() {
		return page, fmt.Errorf("%w: enumeration directory timestamp changed", ErrChanged)
	}

	if offset > 0 {
		if err := discardNames(ctx, directory, offset); err != nil {
			return page, err
		}
	}
	page.Items, err = readEntries(ctx, directory, rootID, relativePrefix, limit)
	if err != nil {
		return page, err
	}
	page.Coverage = coverage(root, len(page.Items), started, time.Now().UTC())
	if len(page.Items) == limit {
		more, err := hasMoreNames(ctx, directory)
		if err != nil {
			return page, err
		}
		if more {
			page.NextCursor = encodeCursor(cursorState{
				Offset: offset + len(page.Items), DirectoryID: directoryID,
				DirectoryMTime: directoryInfo.ModTime().UnixNano(), Prefix: relativePrefix,
			})
			page.Coverage.Completeness = domain.CompletenessPartial
			page.Coverage.ReasonCodes = []string{"enumeration_limit"}
		}
	}
	finalInfo, err := directory.Stat()
	if err != nil {
		return page, fmt.Errorf("stat enumeration directory after read: %w", err)
	}
	if fileChanged(directoryInfo, finalInfo) {
		return page, fmt.Errorf("%w: enumeration directory changed", ErrChanged)
	}
	if page.NextCursor == "" {
		completed := time.Now().UTC()
		page.Coverage.CompletedAt = &completed
		page.Coverage.ObservedAt = completed
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
	}
	result := make([]domain.Capability, 0, len(names)+1)
	rootFile, openErr := openConstrainedDirectory(root.Path, "")
	if openErr != nil {
		reason := capabilityReason(openErr)
		for _, name := range names {
			result = append(result, domain.Capability{Name: name, State: domain.CapabilityUnknown, Reason: reason, ObservedAt: now})
		}
		result = append(result, domain.Capability{Name: "fs.no_follow", State: noFollowCapability(), ObservedAt: now})
		return result, nil
	}
	defer rootFile.Close()
	writable := !root.ReadOnly && writableDirectory(rootFile)
	readState := domain.CapabilitySupported
	writeState := domain.CapabilityUnsupported
	writeReason := "root is read-only or not writable"
	if writable {
		writeState = domain.CapabilitySupported
		writeReason = ""
	}
	for _, name := range names {
		state := readState
		reason := ""
		if strings.HasPrefix(name, "fs.") && name != "fs.enumerate" && name != "fs.stat" && name != "fs.hash" {
			state, reason = writeState, writeReason
		}
		if name == "fs.hardlink" || name == "fs.move" || name == "fs.rename" {
			state, reason = domain.CapabilityUnknown, "selected source and destination filesystem support requires action-time evidence"
		}
		result = append(result, domain.Capability{Name: name, State: state, Reason: reason, ObservedAt: now})
	}
	result = append(result, domain.Capability{Name: "fs.no_follow", State: noFollowCapability(), ObservedAt: now})
	return result, nil
}

func coverage(root Root, count int, started, observed time.Time) domain.Coverage {
	source, err := domain.NewRuntimeID()
	if err != nil {
		// crypto/rand failure is extraordinarily unlikely. Empty source ID is
		// valid for aggregate evidence and keeps observation usable.
		source = ""
	}
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

func readEntries(ctx context.Context, directory *os.File, rootID domain.ConfigID, prefix string, limit int) ([]domain.FileManifestEntry, error) {
	names, err := directory.Readdirnames(limit)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("read directory: %w", err)
	}
	if len(names) > limit {
		names = names[:limit]
	}
	entries := make([]domain.FileManifestEntry, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.Contains(name, "/") || strings.Contains(name, "\\") || name == "." || name == ".." {
			return nil, fmt.Errorf("%w: directory entry %q", ErrPathEscape, name)
		}
		relativePath := path.Join(prefix, name)
		if prefix == "" {
			relativePath = name
		}
		child, info, err := openConstrainedChild(directory, name)
		if err != nil {
			return nil, wrapObservationError(domain.FileTarget{RootID: rootID, RelativePath: relativePath}, err)
		}
		entry, entryErr := manifestEntry(rootID, relativePath, info, time.Now().UTC())
		child.Close()
		if entryErr != nil {
			return nil, entryErr
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func discardNames(ctx context.Context, directory *os.File, count int) error {
	for count > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := count
		if chunk > 256 {
			chunk = 256
		}
		names, err := directory.Readdirnames(chunk)
		count -= len(names)
		if err != nil {
			if errors.Is(err, io.EOF) && count == 0 {
				return nil
			}
			return fmt.Errorf("advance enumeration cursor: %w", err)
		}
		if len(names) == 0 {
			return fmt.Errorf("advance enumeration cursor: %w", io.EOF)
		}
	}
	return nil
}

func hasMoreNames(ctx context.Context, directory *os.File) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	names, err := directory.Readdirnames(1)
	if len(names) > 0 {
		// There is no seek-back operation in this API. Caller only invokes
		// this after a full page and cursor offset advances past this probe.
		return true, nil
	}
	if errors.Is(err, io.EOF) {
		return false, nil
	}
	return false, fmt.Errorf("read directory continuation: %w", err)
}

type cursorState struct {
	Offset         int    `json:"offset"`
	DirectoryID    string `json:"directoryId"`
	DirectoryMTime int64  `json:"directoryMtime"`
	Prefix         string `json:"prefix"`
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
	if err := json.Unmarshal(decoded, &state); err != nil || state.Offset < 0 || state.DirectoryID == "" {
		return cursorState{}, errors.New("invalid enumeration cursor")
	}
	return state, nil
}

func cursorMTime(value string) int64 {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0
	}
	var state cursorState
	if json.Unmarshal(decoded, &state) != nil {
		return 0
	}
	return state.DirectoryMTime
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
