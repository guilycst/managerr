package domain

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// MediaKind is the title shape understood by the Arr and library adapters.
type MediaKind string

const (
	MediaMovie   MediaKind = "movie"
	MediaEpisode MediaKind = "episode"
	MediaSeason  MediaKind = "season"
	MediaAnime   MediaKind = "anime"
)

// Source identifies who owns a configuration record.
type Source string

const (
	SourceYAML Source = "yaml"
	SourceAPI  Source = "api"
)

// ReloadPolicy describes when a source change takes effect.
type ReloadPolicy string

const ReloadOnRestart ReloadPolicy = "restart_required"

// SourceMetadata explains configuration ownership without exposing secrets.
type SourceMetadata struct {
	Source       Source
	Editable     bool
	DocumentID   string
	Revision     string
	StartupAt    time.Time
	ReloadPolicy ReloadPolicy
}

// Validate enforces the source/editability invariants used by API responses.
func (metadata SourceMetadata) Validate() error {
	if metadata.Source != SourceYAML && metadata.Source != SourceAPI {
		return errors.New("source must be yaml or api")
	}
	if metadata.Source == SourceYAML && metadata.Editable {
		return errors.New("yaml configuration is read-only")
	}
	if metadata.Source == SourceAPI && !metadata.Editable {
		return errors.New("api configuration must be editable")
	}
	if strings.TrimSpace(metadata.DocumentID) == "" {
		return errors.New("source document id is required")
	}
	if strings.TrimSpace(metadata.Revision) == "" {
		return errors.New("source revision is required")
	}
	if metadata.StartupAt.IsZero() {
		return errors.New("source startup time is required")
	}
	if metadata.ReloadPolicy != ReloadOnRestart {
		return errors.New("unsupported reload policy")
	}
	return nil
}

// Completeness describes whether an observation can establish absence.
type Completeness string

const (
	CompletenessComplete Completeness = "complete"
	CompletenessPartial  Completeness = "partial"
	CompletenessUnknown  Completeness = "unknown"
)

// Coverage records the scope and quality of one observation snapshot.
type Coverage struct {
	SourceID         RuntimeID
	ConnectionID     ConfigID
	RootID           ConfigID
	Completeness     Completeness
	ReasonCodes      []string
	ObservedCount    int64
	SnapshotRevision string
	StartedAt        *time.Time
	CompletedAt      *time.Time
	ObservedAt       time.Time
}

// Validate checks coverage identifiers and timestamp ordering. Optional
// connection/root/source identifiers are allowed for aggregate snapshots.
func (coverage Coverage) Validate() error {
	switch coverage.Completeness {
	case CompletenessComplete, CompletenessPartial, CompletenessUnknown:
	default:
		return errors.New("unsupported coverage completeness")
	}
	if coverage.ObservedCount < 0 {
		return errors.New("coverage observed count cannot be negative")
	}
	if coverage.ObservedAt.IsZero() {
		return errors.New("coverage observed time is required")
	}
	if coverage.StartedAt != nil && coverage.CompletedAt != nil && coverage.CompletedAt.Before(*coverage.StartedAt) {
		return errors.New("coverage completed time precedes start")
	}
	if coverage.CompletedAt != nil && coverage.CompletedAt.After(coverage.ObservedAt) {
		return errors.New("coverage completed time follows observation")
	}
	if coverage.ConnectionID != "" && !coverage.ConnectionID.Valid() {
		return errors.New("coverage has an invalid connection id")
	}
	if coverage.RootID != "" && !coverage.RootID.Valid() {
		return errors.New("coverage has an invalid root id")
	}
	if coverage.SourceID != "" && !coverage.SourceID.Valid() {
		return errors.New("coverage has an invalid source id")
	}
	return nil
}

// CapabilityState is intentionally three-valued. A missing or unverified API
// must not be presented as unsupported or available.
type CapabilityState string

const (
	CapabilitySupported   CapabilityState = "supported"
	CapabilityUnsupported CapabilityState = "unsupported"
	CapabilityUnknown     CapabilityState = "unknown"
)

// Capability is evidence for one narrowly scoped operation.
type Capability struct {
	Name       string
	State      CapabilityState
	Version    string
	Reason     string
	Evidence   []string
	ObservedAt time.Time
}

// Validate checks a capability observation without interpreting its reason.
func (capability Capability) Validate() error {
	if strings.TrimSpace(capability.Name) == "" {
		return errors.New("capability name is required")
	}
	switch capability.State {
	case CapabilitySupported, CapabilityUnsupported, CapabilityUnknown:
	default:
		return errors.New("unsupported capability state")
	}
	if capability.ObservedAt.IsZero() {
		return errors.New("capability observation time is required")
	}
	return nil
}

// FileTarget identifies a configured-root-relative path. Host paths never
// enter action payloads through this value.
type FileTarget struct {
	RootID       ConfigID
	RelativePath string
}

// Validate rejects traversal, aliases and root targets before an adapter sees a
// path. Paths use slash separators in the API and are not host path strings.
func (target FileTarget) Validate() error {
	if !target.RootID.Valid() {
		return errors.New("file target has an invalid root id")
	}
	if err := ValidateRelativePath(target.RelativePath); err != nil {
		return err
	}
	return nil
}

// ValidateRelativePath enforces the root-relative path contract.
func ValidateRelativePath(relativePath string) error {
	if relativePath == "" {
		return errors.New("relative path is required")
	}
	if strings.IndexByte(relativePath, 0) >= 0 || strings.HasPrefix(relativePath, "/") {
		return errors.New("relative path must stay below its configured root")
	}
	if strings.Contains(relativePath, "\\") {
		return errors.New("relative path must use slash separators")
	}
	clean := path.Clean(relativePath)
	if clean == "." || clean != relativePath {
		return errors.New("relative path is not canonical")
	}
	for _, part := range strings.Split(relativePath, "/") {
		if part == ".." || part == "." || part == "" {
			return errors.New("relative path contains an unsafe component")
		}
	}
	return nil
}

// ManifestEntryType classifies a selected filesystem object.
type ManifestEntryType string

const (
	ManifestFile      ManifestEntryType = "file"
	ManifestDirectory ManifestEntryType = "directory"
	ManifestSubtitle  ManifestEntryType = "subtitle"
	ManifestCompanion ManifestEntryType = "companion"
)

// ManifestRole records the intended media role when known.
type ManifestRole string

const (
	RoleVideo     ManifestRole = "video"
	RoleSubtitle  ManifestRole = "subtitle"
	RoleCompanion ManifestRole = "companion"
)

// FileManifestEntry is an exact, bounded observation. It is never a wildcard
// for a directory's future children.
type FileManifestEntry struct {
	RootID       ConfigID
	RelativePath string
	Type         ManifestEntryType
	Size         int64
	Digest       string
	FileIdentity string
	Role         ManifestRole
	ObservedAt   time.Time
	Children     []FileManifestEntry
}

// Validate checks the fields needed to bind an action to an observed object.
func (entry FileManifestEntry) Validate() error {
	if err := (FileTarget{RootID: entry.RootID, RelativePath: entry.RelativePath}).Validate(); err != nil {
		return err
	}
	switch entry.Type {
	case ManifestFile, ManifestDirectory, ManifestSubtitle, ManifestCompanion:
	default:
		return errors.New("unsupported manifest entry type")
	}
	if entry.Size < 0 {
		return errors.New("manifest entry size cannot be negative")
	}
	if entry.Role != "" && entry.Role != RoleVideo && entry.Role != RoleSubtitle && entry.Role != RoleCompanion {
		return errors.New("unsupported manifest entry role")
	}
	if len(entry.Children) > 0 && entry.Type != ManifestDirectory {
		return errors.New("only directory entries can carry children")
	}
	for index, child := range entry.Children {
		if err := child.Validate(); err != nil {
			return fmt.Errorf("manifest child %d: %w", index, err)
		}
		if child.RootID != entry.RootID || !strings.HasPrefix(child.RelativePath, entry.RelativePath+"/") {
			return errors.New("manifest child must remain below its directory")
		}
	}
	for left := 0; left < len(entry.Children); left++ {
		for right := left + 1; right < len(entry.Children); right++ {
			if manifestEntriesOverlap(entry.Children[left], entry.Children[right]) {
				return errors.New("directory manifest children overlap")
			}
		}
	}
	if entry.ObservedAt.IsZero() {
		return errors.New("manifest entry observation time is required")
	}
	return nil
}

// ValidateAction checks the evidence required before a filesystem effect can
// use an observed entry. Directory entries must carry their exact child list.
func (entry FileManifestEntry) ValidateAction(requireDigest bool) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(entry.FileIdentity) == "" {
		return errors.New("filesystem action requires file identity evidence")
	}
	if requireDigest && entry.Type != ManifestDirectory && !strongDigest(entry.Digest) {
		return errors.New("filesystem copy requires a strong content digest")
	}
	if entry.Type == ManifestDirectory {
		if len(entry.Children) == 0 {
			return errors.New("filesystem action requires an exact directory child manifest")
		}
		for index, child := range entry.Children {
			if err := child.ValidateAction(requireDigest); err != nil {
				return fmt.Errorf("manifest child %d: %w", index, err)
			}
		}
	}
	return nil
}

func strongDigest(value string) bool {
	value = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), "sha256:")
	if len(value) != hex.EncodedLen(32) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func manifestEntriesOverlap(left, right FileManifestEntry) bool {
	if left.RootID != right.RootID {
		return false
	}
	if left.RelativePath == right.RelativePath {
		return true
	}
	return left.Type == ManifestDirectory && strings.HasPrefix(right.RelativePath, left.RelativePath+"/") || right.Type == ManifestDirectory && strings.HasPrefix(left.RelativePath, right.RelativePath+"/")
}

// TrackingDimension separates registration, import, server availability and
// request state. These dimensions must not be collapsed into one boolean.
type TrackingDimension string

const (
	TrackingRegistration TrackingDimension = "registration"
	TrackingImport       TrackingDimension = "import"
	TrackingAvailability TrackingDimension = "availability"
	TrackingRequest      TrackingDimension = "request"
)

type TrackingValue string

const (
	TrackingPresent TrackingValue = "present"
	TrackingAbsent  TrackingValue = "absent"
	TrackingUnknown TrackingValue = "unknown"
)

// TrackingObservation is one instance-scoped tracking fact.
type TrackingObservation struct {
	ConnectionID   ConfigID
	Dimension      TrackingDimension
	Value          TrackingValue
	ExternalID     string
	ProviderID     string
	ObservedAt     time.Time
	CoverageID     RuntimeID
	Coverage       *Coverage
	CoverageMaxAge time.Duration
	Evidence       []string
}

// Validate ensures an observation cannot accidentally lose its instance scope.
func (observation TrackingObservation) Validate() error {
	if !observation.ConnectionID.Valid() {
		return errors.New("tracking observation has an invalid connection id")
	}
	switch observation.Dimension {
	case TrackingRegistration, TrackingImport, TrackingAvailability, TrackingRequest:
	default:
		return errors.New("unsupported tracking dimension")
	}
	switch observation.Value {
	case TrackingPresent, TrackingAbsent, TrackingUnknown:
	default:
		return errors.New("unsupported tracking value")
	}
	if observation.ObservedAt.IsZero() {
		return errors.New("tracking observation time is required")
	}
	if observation.CoverageID != "" && !observation.CoverageID.Valid() {
		return errors.New("tracking observation has an invalid coverage id")
	}
	if observation.Coverage != nil {
		if err := observation.Coverage.Validate(); err != nil {
			return fmt.Errorf("tracking observation coverage: %w", err)
		}
		if observation.CoverageID == "" || observation.Coverage.SourceID != observation.CoverageID {
			return errors.New("tracking observation coverage id does not match its coverage")
		}
		if observation.Coverage.ConnectionID != observation.ConnectionID {
			return errors.New("tracking observation coverage is scoped to another connection")
		}
		if observation.Coverage.ObservedAt.After(observation.ObservedAt) {
			return errors.New("tracking observation coverage is newer than the observation")
		}
	}
	if observation.Value == TrackingAbsent {
		if observation.Coverage == nil || observation.CoverageID == "" {
			return errors.New("absent tracking requires a complete coverage proof")
		}
		if observation.Coverage.Completeness != CompletenessComplete {
			return errors.New("absent tracking requires complete coverage")
		}
		if observation.Coverage.CompletedAt == nil || observation.Coverage.CompletedAt.After(observation.ObservedAt) {
			return errors.New("absent tracking requires completed fresh coverage")
		}
		if observation.CoverageMaxAge <= 0 {
			return errors.New("absent tracking requires a positive coverage freshness bound")
		}
		if age := observation.ObservedAt.Sub(observation.Coverage.ObservedAt); age < 0 || age > observation.CoverageMaxAge {
			return errors.New("absent tracking coverage is stale")
		}
	}
	return nil
}

// Readiness indicates whether a discovered payload can be considered for an
// exact review.
type Readiness string

const (
	ReadinessReady       Readiness = "ready"
	ReadinessDownloading Readiness = "downloading"
	ReadinessProcessing  Readiness = "processing"
	ReadinessChanging    Readiness = "changing"
	ReadinessUnsupported Readiness = "unsupported"
	ReadinessUnknown     Readiness = "unknown"
)

// Provenance associates a discovery with a download client without asserting
// that the client registered or imported the media.
type Provenance struct {
	ConnectionID ConfigID
	ClientItemID string
	Hash         string
	CompletedAt  *time.Time
	DescriptorID RuntimeID
	SourcePath   *FileTarget
}

// Validate checks optional provenance references when they are present.
func (provenance Provenance) Validate() error {
	if (strings.TrimSpace(provenance.ClientItemID) != "" || strings.TrimSpace(provenance.Hash) != "") && !provenance.ConnectionID.Valid() {
		return errors.New("client provenance requires a valid connection id")
	}
	if provenance.ConnectionID != "" && !provenance.ConnectionID.Valid() {
		return errors.New("provenance has an invalid connection id")
	}
	if provenance.DescriptorID != "" && !provenance.DescriptorID.Valid() {
		return errors.New("provenance has an invalid descriptor id")
	}
	if provenance.SourcePath != nil {
		if err := provenance.SourcePath.Validate(); err != nil {
			return fmt.Errorf("provenance source path: %w", err)
		}
	}
	return nil
}

// UpstreamErrorCode is the normalized error vocabulary shared by adapters.
type UpstreamErrorCode string

const (
	OutcomeUnavailable  UpstreamErrorCode = "unavailable"
	OutcomeRateLimited  UpstreamErrorCode = "rate_limited"
	OutcomeUnauthorized UpstreamErrorCode = "unauthorized"
	OutcomeInvalidInput UpstreamErrorCode = "invalid_input"
	OutcomeConflict     UpstreamErrorCode = "conflict"
	OutcomeUnsupported  UpstreamErrorCode = "unsupported"
	OutcomeUnknown      UpstreamErrorCode = "outcome_unknown"
)

// UpstreamError carries sanitized evidence. Adapters must redact response
// bodies, URLs, credentials and filesystem internals before constructing it.
type UpstreamError struct {
	Code       UpstreamErrorCode
	Status     int
	Retryable  bool
	Operation  string
	UpstreamID string
	Detail     string
}

func (err UpstreamError) Error() string {
	if err.Detail == "" {
		return string(err.Code)
	}
	return fmt.Sprintf("%s: %s", err.Code, err.Detail)
}

// Validate checks normalized errors without inspecting or logging secret input.
func (err UpstreamError) Validate() error {
	switch err.Code {
	case OutcomeUnavailable, OutcomeRateLimited, OutcomeUnauthorized, OutcomeInvalidInput, OutcomeConflict, OutcomeUnsupported, OutcomeUnknown:
	default:
		return errors.New("unsupported upstream error code")
	}
	if err.Status < 0 || err.Status > 599 {
		return errors.New("upstream status must be between 0 and 599")
	}
	return nil
}
