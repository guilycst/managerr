// Package ports defines the typed boundaries used by domain services. Adapters
// implement these contracts; callers never reach an upstream database or shell.
package ports

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/guilycst/managerr/internal/domain"
)

// Page is the bounded result shared by read-only connector inventories.
type Page[T any] struct {
	Items      []T
	NextCursor string
	Coverage   domain.Coverage
}

// UnsupportedChildEvidence is path-scoped evidence carried by a filesystem
// coverage reason. It keeps an unobservable child visible without treating it
// as a discovered manifest entry.
type UnsupportedChildEvidence struct {
	RelativePath string `json:"relativePath"`
	Reason       string `json:"reason"`
}

const unsupportedChildReasonPrefix = "unsupported_child:"

// ParseUnsupportedChildReasonCode decodes the stable filesystem coverage
// reason emitted by adapters. Unrelated or malformed reasons return false.
func ParseUnsupportedChildReasonCode(code string) (UnsupportedChildEvidence, bool) {
	if !strings.HasPrefix(code, unsupportedChildReasonPrefix) {
		return UnsupportedChildEvidence{}, false
	}
	var evidence UnsupportedChildEvidence
	if err := json.Unmarshal([]byte(strings.TrimPrefix(code, unsupportedChildReasonPrefix)), &evidence); err != nil {
		return UnsupportedChildEvidence{}, false
	}
	if evidence.RelativePath == "" || evidence.Reason == "" {
		return UnsupportedChildEvidence{}, false
	}
	return evidence, true
}

// DownloadItem is a client-scoped observation. Categories and tags remain
// hints and never authorize an Arr operation.
type DownloadItem struct {
	ExternalID     string
	Name           string
	Protocol       string
	State          string
	Progress       float64
	Seeding        bool
	Category       string
	Tags           []string
	Hash           string
	CompletedAt    *time.Time
	ProcessingDone bool
	Payload        []domain.FileManifestEntry
	Descriptor     *DescriptorObservation
}

// DescriptorObservation describes an original torrent/NZB when the client can
// provide it, without placing descriptor bytes in an ordinary inventory result.
type DescriptorObservation struct {
	ID          domain.RuntimeID
	Kind        string
	Available   bool
	Size        int64
	Digest      string
	CapturedAt  *time.Time
	Source      string
	Unavailable string
}

// DownloadInventoryPort is read-only and safe to call during discovery.
type DownloadInventoryPort interface {
	List(ctx context.Context, connectionID domain.ConfigID, cursor string, limit int) (Page[DownloadItem], error)
}

// DownloadRef scopes every client operation to one configured instance.
type DownloadRef struct {
	ConnectionID domain.ConfigID
	ExternalID   string
}

// DownloadObservation is read back before and after a client mutation.
type DownloadObservation struct {
	Ref          DownloadRef
	State        string
	Seeding      bool
	Payload      []domain.FileManifestEntry
	ObservedAt   time.Time
	Capabilities []domain.Capability
}

// ClientEffect records a sanitized native operation identifier and its
// certainty. It never contains an upstream response body.
type ClientEffect struct {
	OperationID string
	Outcome     domain.EffectOutcome
	ObservedAt  time.Time
	Evidence    []string
}

// DownloadControlPort contains only the supported qBittorrent mutations.
// Remove is metadata-only by contract and never deletes payload bytes.
type DownloadControlPort interface {
	Observe(ctx context.Context, ref DownloadRef) (DownloadObservation, error)
	Stop(ctx context.Context, ref DownloadRef) (ClientEffect, error)
	Relocate(ctx context.Context, ref DownloadRef, destination domain.FileTarget) (ClientEffect, error)
	RenameFile(ctx context.Context, ref DownloadRef, source domain.FileTarget, newName string) (ClientEffect, error)
	RenameFolder(ctx context.Context, ref DownloadRef, source domain.FileTarget, newName string) (ClientEffect, error)
	Remove(ctx context.Context, ref DownloadRef) (ClientEffect, error)
}

// MediaFile is a file association read from Radarr or Sonarr.
type MediaFile struct {
	ExternalID string
	Path       domain.FileTarget
	Size       int64
	Digest     string
	EpisodeIDs []string
	MovieID    string
}

// MediaRecord is one title in one Arr instance.
type MediaRecord struct {
	ExternalID string
	ProviderID string
	Title      string
	Kind       domain.MediaKind
	Monitored  bool
	Files      []MediaFile
}

// MediaManagerReadPort exposes catalog, options and exact native import
// previews. Preview methods do not execute an import.
type MediaManagerReadPort interface {
	List(ctx context.Context, connectionID domain.ConfigID, cursor string, limit int) (Page[MediaRecord], error)
	Lookup(ctx context.Context, connectionID domain.ConfigID, providerID string, kind domain.MediaKind) ([]MediaRecord, error)
	Options(ctx context.Context, connectionID domain.ConfigID) (ManagerOptions, error)
	PreviewImport(ctx context.Context, connectionID domain.ConfigID, request ImportPreviewRequest) (ImportPreview, error)
	ObserveImport(ctx context.Context, connectionID domain.ConfigID, externalID string) (ImportObservation, error)
}

// ManagerOptions contains only fields that the selected Arr instance reports
// as valid for a registration or import plan.
type ManagerOptions struct {
	RootFolders     []string
	QualityProfiles []QualityProfile
	SeriesTypes     []string
	Seasons         []string
	ObservedAt      time.Time
}

type QualityProfile struct {
	ID   string
	Name string
}

// RegistrationFields uses pointers for optional values so an upsert preserves
// upstream fields the reviewer did not explicitly select.
type RegistrationFields struct {
	RootFolder       string
	QualityProfileID string
	Monitored        *bool
	SeriesType       string
	SeasonFolder     *bool
	Seasons          []string
}

type RegistrationRequest struct {
	ProviderID string
	Kind       domain.MediaKind
	Fields     RegistrationFields
}

type RegistrationResult struct {
	ExternalID string
	Record     MediaRecord
	Effect     ClientEffect
}

// ImportFile is the exact reviewed file/episode association sent to Arr.
type ImportFile struct {
	Source           domain.FileTarget
	MovieOrEpisodeID string
	Subtitle         bool
	Language         string
	Forced           bool
	HearingImpaired  bool
}

type ImportPreviewRequest struct {
	RegisteredExternalID string
	Files                []ImportFile
	Transfer             string
}

type ImportPreview struct {
	Revision   string
	Files      []ImportFile
	Rejections []ImportRejection
	ObservedAt time.Time
}

type ImportRejection struct {
	Source domain.FileTarget
	Code   string
	Reason string
}

type ImportRequest struct {
	RegisteredExternalID string
	PreviewRevision      string
	Files                []ImportFile
	Transfer             string
}

type ImportObservation struct {
	ExternalID string
	Files      []MediaFile
	Effect     *ClientEffect
	ObservedAt time.Time
}

// MediaManagerWritePort contains explicit registration/import operations. It
// does not expose a generic command endpoint.
type MediaManagerWritePort interface {
	Register(ctx context.Context, connectionID domain.ConfigID, request RegistrationRequest) (RegistrationResult, error)
	Import(ctx context.Context, connectionID domain.ConfigID, request ImportRequest) (ImportObservation, error)
}

// MediaServerItem is an independent Jellyfin availability observation.
type MediaServerItem struct {
	ExternalID string
	ProviderID string
	Title      string
	Playable   bool
	ObservedAt time.Time
}

type MediaServerReadPort interface {
	List(ctx context.Context, connectionID domain.ConfigID, cursor string, limit int) (Page[MediaServerItem], error)
}

type RefreshScope string

const (
	RefreshLibrary RefreshScope = "library"
	RefreshItem    RefreshScope = "item"
)

type RefreshRequest struct {
	Scope      RefreshScope
	ExternalID string
}

type RefreshResult struct {
	Accepted    bool
	OperationID string
	ObservedAt  time.Time
	Evidence    []string
}

type MediaServerRefreshPort interface {
	Refresh(ctx context.Context, connectionID domain.ConfigID, request RefreshRequest) (RefreshResult, error)
}

// RequestRecord preserves Seerr's native status while allowing aggregation.
type RequestRecord struct {
	ExternalID string
	ProviderID string
	Status     string
	MediaID    string
	ObservedAt time.Time
}

type RequestCatalogReadPort interface {
	ListMedia(ctx context.Context, connectionID domain.ConfigID, cursor string, limit int) (Page[RequestRecord], error)
	ListRequests(ctx context.Context, connectionID domain.ConfigID, cursor string, limit int) (Page[RequestRecord], error)
}

// FilesystemObservation is a stat/hash result bound to a configured root.
type FilesystemObservation struct {
	Entry      domain.FileManifestEntry
	Mode       string
	Readable   bool
	Writable   bool
	ObservedAt time.Time
}

type FilesystemReadPort interface {
	Enumerate(ctx context.Context, rootID domain.ConfigID, relativePrefix string, limit int) (Page[domain.FileManifestEntry], error)
	// EnumeratePage resumes a bounded observation using the opaque cursor from
	// the previous page. Implementations must reject cursors from a changed
	// directory snapshot rather than silently mixing evidence.
	EnumeratePage(ctx context.Context, rootID domain.ConfigID, relativePrefix, cursor string, limit int) (Page[domain.FileManifestEntry], error)
	Stat(ctx context.Context, target domain.FileTarget) (FilesystemObservation, error)
	Hash(ctx context.Context, target domain.FileTarget) (string, error)
	Capabilities(ctx context.Context, rootID domain.ConfigID) ([]domain.Capability, error)
}

type FileMap struct {
	Source      domain.FileManifestEntry
	Destination domain.FileTarget
}

func (mapping FileMap) Validate() error {
	if err := mapping.Source.Validate(); err != nil {
		return err
	}
	return mapping.Destination.Validate()
}

type FilesystemCopyRequest struct {
	Files []FileMap
}

func (request FilesystemCopyRequest) Validate() error {
	return validateFileMaps(request.Files, true, true)
}

// FilesystemHardlinkRequest has its own type so a directory cannot be passed
// to Hardlink while still sharing the exact manifest contract with copy.
type FilesystemHardlinkRequest struct {
	Files []FileMap
}

func (request FilesystemHardlinkRequest) Validate() error {
	return validateFileMaps(request.Files, false, false)
}

// FilesystemMoveRequest carries exact source manifests for same-filesystem
// moves. It requires identity evidence but not a content hash.
type FilesystemMoveRequest struct {
	Files []FileMap
}

func (request FilesystemMoveRequest) Validate() error {
	return validateFileMaps(request.Files, true, false)
}

// FilesystemRenameRequest carries exact source manifests for native or
// application-side renames. It requires identity evidence but not a hash.
type FilesystemRenameRequest struct {
	Files []FileMap
}

func (request FilesystemRenameRequest) Validate() error {
	return validateFileMaps(request.Files, true, false)
}

type FilesystemTrashRequest struct {
	Files     []domain.FileManifestEntry
	Retention time.Duration
}

func (request FilesystemTrashRequest) Validate() error {
	if request.Retention <= 0 {
		return fmt.Errorf("trash retention must be positive")
	}
	return validateManifest(request.Files, false)
}

type FilesystemRestoreRequest struct {
	Files []FileMap
}

func (request FilesystemRestoreRequest) Validate() error {
	return validateFileMaps(request.Files, true, false)
}

type FilesystemDeleteRequest struct {
	Files []domain.FileManifestEntry
}

func (request FilesystemDeleteRequest) Validate() error {
	return validateManifest(request.Files, false)
}

type FilesystemEffect struct {
	Outcome    domain.EffectOutcome
	Affected   []domain.FileManifestEntry
	ObservedAt time.Time
	Evidence   []string
}

// FilesystemActionPort owns reviewed, root-confined file effects. Each method
// accepts an exact map or manifest, never an arbitrary command or glob.
type FilesystemActionPort interface {
	Copy(ctx context.Context, request FilesystemCopyRequest) (FilesystemEffect, error)
	Hardlink(ctx context.Context, request FilesystemHardlinkRequest) (FilesystemEffect, error)
	Move(ctx context.Context, request FilesystemMoveRequest) (FilesystemEffect, error)
	Rename(ctx context.Context, request FilesystemRenameRequest) (FilesystemEffect, error)
	Trash(ctx context.Context, request FilesystemTrashRequest) (FilesystemEffect, error)
	Restore(ctx context.Context, request FilesystemRestoreRequest) (FilesystemEffect, error)
	Delete(ctx context.Context, request FilesystemDeleteRequest) (FilesystemEffect, error)
}

func validateManifest(entries []domain.FileManifestEntry, requireDigest bool) error {
	if len(entries) == 0 {
		return fmt.Errorf("filesystem manifest cannot be empty")
	}
	for index, entry := range entries {
		if err := entry.ValidateAction(requireDigest); err != nil {
			return fmt.Errorf("manifest entry %d: %w", index, err)
		}
	}
	for left := 0; left < len(entries); left++ {
		for right := left + 1; right < len(entries); right++ {
			if manifestEntriesOverlap(entries[left], entries[right]) {
				return fmt.Errorf("manifest entries %q and %q overlap", entries[left].RelativePath, entries[right].RelativePath)
			}
		}
	}
	return nil
}

func validateFileMaps(mappings []FileMap, allowDirectories, requireDigest bool) error {
	if len(mappings) == 0 {
		return fmt.Errorf("filesystem map cannot be empty")
	}
	for index, mapping := range mappings {
		if err := mapping.Validate(); err != nil {
			return fmt.Errorf("file map %d: %w", index, err)
		}
		if err := mapping.Source.ValidateAction(requireDigest); err != nil {
			return fmt.Errorf("file map %d source: %w", index, err)
		}
		if !allowDirectories && mapping.Source.Type == domain.ManifestDirectory {
			return fmt.Errorf("file map %d: hardlink does not support directories", index)
		}
		if pathsOverlap(mapping.Source.RootID, mapping.Source.RelativePath, mapping.Destination.RootID, mapping.Destination.RelativePath) {
			return fmt.Errorf("file map %d: source and destination overlap", index)
		}
	}
	for left := 0; left < len(mappings); left++ {
		for right := left + 1; right < len(mappings); right++ {
			if manifestEntriesOverlap(mappings[left].Source, mappings[right].Source) {
				return fmt.Errorf("file maps %d and %d source manifests overlap", left, right)
			}
			if targetsOverlap(mappings[left].Destination, mappings[right].Destination) {
				return fmt.Errorf("file maps %d and %d destinations overlap", left, right)
			}
			if pathsOverlap(mappings[left].Source.RootID, mappings[left].Source.RelativePath, mappings[right].Destination.RootID, mappings[right].Destination.RelativePath) || pathsOverlap(mappings[right].Source.RootID, mappings[right].Source.RelativePath, mappings[left].Destination.RootID, mappings[left].Destination.RelativePath) {
				return fmt.Errorf("file maps %d and %d source and destination paths overlap", left, right)
			}
		}
	}
	return nil
}

func pathsOverlap(leftRoot domain.ConfigID, leftPath string, rightRoot domain.ConfigID, rightPath string) bool {
	if leftRoot != rightRoot {
		return false
	}
	return leftPath == rightPath || strings.HasPrefix(leftPath, rightPath+"/") || strings.HasPrefix(rightPath, leftPath+"/")
}

func manifestEntriesOverlap(left, right domain.FileManifestEntry) bool {
	if left.RootID != right.RootID {
		return false
	}
	if left.RelativePath == right.RelativePath {
		return true
	}
	return left.Type == domain.ManifestDirectory && strings.HasPrefix(right.RelativePath, left.RelativePath+"/") || right.Type == domain.ManifestDirectory && strings.HasPrefix(left.RelativePath, right.RelativePath+"/")
}

func targetsOverlap(left, right domain.FileTarget) bool {
	if left.RootID != right.RootID {
		return false
	}
	return left.RelativePath == right.RelativePath || strings.HasPrefix(left.RelativePath, right.RelativePath+"/") || strings.HasPrefix(right.RelativePath, left.RelativePath+"/")
}

// ConfigurationRepositoryPort exposes the effective startup snapshot. YAML
// records remain read-only through this boundary.
type ConfigurationRepositoryPort interface {
	Snapshot(ctx context.Context) (domain.ConfigurationSnapshot, error)
}

// CapabilityPort lets workers inspect one connector's tested feature set
// without assuming that an absent API is unsupported.
type CapabilityPort interface {
	Capabilities(ctx context.Context, connectionID domain.ConfigID) ([]domain.Capability, error)
}
