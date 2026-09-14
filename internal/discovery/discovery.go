// Package discovery turns bounded filesystem and download-client observations
// into durable, reviewable media groups. It has no write access to a
// filesystem or an upstream service. The scanner deliberately keeps
// provenance, client completion and file stability as separate observations;
// none of them is inferred from a display name or a category/tag.
package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

const (
	defaultPageSize             = 1_000
	defaultMaxEntries           = 10_000
	defaultMaxDepth             = 32
	defaultMaxFilesystemPages   = 10_000
	defaultMinimumStableSpacing = 30 * time.Second
)

var (
	// ErrInvalidScannerOptions identifies configuration which would make a
	// scan unbounded or weaken root-relative path handling.
	ErrInvalidScannerOptions = errors.New("invalid discovery scanner options")
	// ErrInvalidObservation identifies a malformed value crossing the
	// persistence boundary.
	ErrInvalidObservation = errors.New("invalid discovery observation")
	// ErrStoreUnavailable identifies a scanner without a persistence owner.
	ErrStoreUnavailable = errors.New("discovery observation store is unavailable")
)

// GroupKind is a conservative shape classification. A classification is a
// suggestion for review; it is never a title identity or an Arr ID.
type GroupKind string

const (
	GroupMovie      GroupKind = "movie"
	GroupEpisode    GroupKind = "episode"
	GroupSeasonPack GroupKind = "season_pack"
	GroupAnime      GroupKind = "anime"
	GroupMixed      GroupKind = "mixed"
	GroupUnknown    GroupKind = "unknown"
)

// MatchConfidence describes how much structure was present in a filename.
type MatchConfidence string

const (
	ConfidenceExact      MatchConfidence = "exact"
	ConfidenceSuggested  MatchConfidence = "suggested"
	ConfidenceAmbiguous  MatchConfidence = "ambiguous"
	ConfidenceUnresolved MatchConfidence = "unresolved"
)

// StabilityState is derived only from observations of the same exact path.
type StabilityState string

const (
	StabilityStable   StabilityState = "stable"
	StabilityChanging StabilityState = "changing"
	StabilityUnknown  StabilityState = "unknown"
)

// ClientCompletionState records client lifecycle independently from file
// stability. An unknown state is honest when no client can be correlated.
type ClientCompletionState string

const (
	ClientCompletionComplete    ClientCompletionState = "complete"
	ClientCompletionDownloading ClientCompletionState = "downloading"
	ClientCompletionProcessing  ClientCompletionState = "processing"
	ClientCompletionUnknown     ClientCompletionState = "unknown"
)

// ProvenanceState is explicit because an empty provenance list means unknown,
// not confirmed absence of a download-client association.
type ProvenanceState string

const (
	ProvenanceKnown   ProvenanceState = "known"
	ProvenanceUnknown ProvenanceState = "unknown"
)

// CompanionKind identifies why an otherwise non-video file is retained in a
// review. Unsupported files are visible and never silently discarded.
type CompanionKind string

const (
	CompanionKnown             CompanionKind = "known"
	CompanionUnsupported       CompanionKind = "unsupported"
	CompanionUnmatchedSubtitle CompanionKind = "unmatched_subtitle"
	CompanionSubtitlePair      CompanionKind = "subtitle_pair"
)

// ReviewReason is a stable, machine-readable explanation for why a group
// needs human review before an action plan can be made.
type ReviewReason string

const (
	ReviewOrphanProvenance          ReviewReason = "orphan_provenance"
	ReviewClientCompletionUnknown   ReviewReason = "client_completion_unknown"
	ReviewClientInventoryIncomplete ReviewReason = "client_inventory_incomplete"
	ReviewStabilityUnknown          ReviewReason = "stability_unknown"
	ReviewFileChanging              ReviewReason = "file_changing"
	ReviewCoveragePartial           ReviewReason = "coverage_partial"
	ReviewUnsupportedChild          ReviewReason = "unsupported_child"
	ReviewUnsupportedCompanion      ReviewReason = "unsupported_companion"
	ReviewUnmatchedSubtitle         ReviewReason = "unmatched_subtitle"
	ReviewAmbiguousAssociation      ReviewReason = "ambiguous_association"
	ReviewAnimeMappingRequired      ReviewReason = "anime_mapping_required"
	ReviewNotSeenInCompleteScan     ReviewReason = "not_seen_in_complete_scan"
)

// FileStability is one exact file's stability evidence. PreviousObservedAt is
// nil for the first observation and prevents a single scan from claiming a
// stable payload.
type FileStability struct {
	Path                string
	State               StabilityState
	ObservedAt          time.Time
	PreviousObservedAt  *time.Time
	ObservationCount    int
	CurrentFingerprint  string
	PreviousFingerprint string
}

// StabilityObservation is an aggregate over all selected files. A group is
// stable only when every file has two matching observations far enough apart.
type StabilityObservation struct {
	State          StabilityState
	ObservedAt     time.Time
	MinimumSpacing time.Duration
	Files          []FileStability
	Reason         string
}

// ClientItemObservation is one connection-scoped completion observation. The
// same upstream item ID may legitimately occur on different connections.
type ClientItemObservation struct {
	ConnectionID domain.ConfigID
	ClientItemID string
	Hash         string
	State        ClientCompletionState
	CompletedAt  *time.Time
}

// ClientCompletionObservation contains download-client evidence matched to a
// group. Items is authoritative because connection identity is part of every
// item. ConnectionID and ClientItemIDs are compatibility projections for the
// single-connection case and must not be used to correlate across instances.
type ClientCompletionObservation struct {
	State         ClientCompletionState
	ConnectionID  domain.ConfigID
	ClientItemIDs []string
	Items         []ClientItemObservation
	CompletedAt   *time.Time
	ObservedAt    time.Time
	Known         bool
	UnknownItem   bool
}

// MediaAssociation is a filename-derived suggestion. Episode and absolute
// numbers are useful review hints, but the selected title/episode IDs are
// supplied later by the review/API layer.
type MediaAssociation struct {
	FilePath       string
	Kind           domain.MediaKind
	Confidence     MatchConfidence
	SeasonNumber   *int
	EpisodeNumbers []int
	AbsoluteNumber *int
	Reason         string
}

// SubtitleAssociation keeps language/forced/SDH labels and exact pairing
// candidates visible to a later explicit subtitle decision.
type SubtitleAssociation struct {
	FilePath        string
	VideoPaths      []string
	PairID          string
	Language        string
	Forced          bool
	HearingImpaired bool
	Confidence      MatchConfidence
	Reason          string
}

// Companion preserves files which do not parse as a video or supported
// subtitle. VideoPaths can contain multiple candidates when the association
// is ambiguous.
type Companion struct {
	FilePath   string
	Kind       CompanionKind
	VideoPaths []string
	Reason     string
}

// Discovery is one persisted, exact file group suitable for review. Files are
// root-relative manifests and are never wildcard paths.
type Discovery struct {
	ID                  domain.RuntimeID
	RootID              domain.ConfigID
	RelativePath        string
	Kind                GroupKind
	Files               []domain.FileManifestEntry
	Videos              []MediaAssociation
	Subtitles           []SubtitleAssociation
	Companions          []Companion
	ProvenanceState     ProvenanceState
	Provenance          []domain.Provenance
	ClientCompletion    ClientCompletionObservation
	ClientCoverage      []domain.Coverage
	Stability           StabilityObservation
	Readiness           domain.Readiness
	ReviewReasons       []ReviewReason
	Coverage            domain.Coverage
	UnsupportedChildren []ports.UnsupportedChildEvidence
	ObservedAt          time.Time
	ManifestRevision    string
	FirstSeenAt         time.Time
	LastSeenAt          time.Time
	Active              bool
}

// Group is an alias for callers that use the filesystem terminology.
type Group = Discovery

// DirectoryObservation is the immutable result of one bounded root scan.
// Entries includes directories and files returned by the read port, excluding
// configured application trash/staging paths. History is append-only in the
// persistence port.
type DirectoryObservation struct {
	ID                  domain.RuntimeID
	RootID              domain.ConfigID
	RelativePrefix      string
	Entries             []domain.FileManifestEntry
	Coverage            domain.Coverage
	UnsupportedChildren []ports.UnsupportedChildEvidence
	ObservedAt          time.Time
	FirstSeenAt         time.Time
	LastSeenAt          time.Time
	ManifestRevision    string
}

// ScanResult contains the persisted observation and the current group view.
// A result may accompany an error when a root-level read was unavailable; in
// that case coverage is unknown and is still safe to retain as evidence.
type ScanResult struct {
	Observation DirectoryObservation
	Discoveries []Discovery
	Coverage    domain.Coverage
	Persisted   bool
}

// DownloadSource binds one read-only inventory port to its configured
// connection. A source failure marks provenance as unknown for the scan.
type DownloadSource struct {
	ConnectionID domain.ConfigID
	Inventory    ports.DownloadInventoryPort
}

// Options bounds one scanner invocation.
type Options struct {
	PageSize             int
	MaxEntries           int
	MaxDepth             int
	MaxFilesystemPages   int
	MinimumStableSpacing time.Duration
	ExcludedPrefixes     []string
	Now                  func() time.Time
	MaxClientPages       int
	MaxClientItems       int
}

// DefaultOptions returns the safe bounded discovery defaults.
func DefaultOptions() Options {
	return Options{
		PageSize: defaultPageSize, MaxEntries: defaultMaxEntries, MaxDepth: defaultMaxDepth,
		MaxFilesystemPages:   defaultMaxFilesystemPages,
		MinimumStableSpacing: defaultMinimumStableSpacing,
		ExcludedPrefixes:     []string{".mastarr-trash", ".mastarr-staging"},
		MaxClientPages:       100, MaxClientItems: defaultMaxEntries,
	}
}

func normalizeOptions(options Options) (Options, error) {
	defaults := DefaultOptions()
	options.ExcludedPrefixes = append([]string(nil), options.ExcludedPrefixes...)
	if options.PageSize <= 0 {
		options.PageSize = defaults.PageSize
	}
	if options.MaxEntries <= 0 {
		options.MaxEntries = defaults.MaxEntries
	}
	if options.PageSize > options.MaxEntries {
		options.PageSize = options.MaxEntries
	}
	if options.MaxDepth <= 0 {
		options.MaxDepth = defaults.MaxDepth
	}
	if options.MaxFilesystemPages <= 0 {
		options.MaxFilesystemPages = defaults.MaxFilesystemPages
	}
	if options.MinimumStableSpacing <= 0 {
		options.MinimumStableSpacing = defaults.MinimumStableSpacing
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.MaxClientPages <= 0 {
		options.MaxClientPages = defaults.MaxClientPages
	}
	if options.MaxClientItems <= 0 {
		options.MaxClientItems = options.MaxEntries
	}
	if len(options.ExcludedPrefixes) == 0 {
		options.ExcludedPrefixes = append([]string(nil), defaults.ExcludedPrefixes...)
	}
	for index, prefix := range options.ExcludedPrefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" || prefix == "." || strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "\\") || strings.Contains(prefix, "\x00") || path.Clean(prefix) != prefix {
			return Options{}, fmt.Errorf("%w: invalid excluded prefix at index %d", ErrInvalidScannerOptions, index)
		}
		for _, component := range strings.Split(prefix, "/") {
			if component == "" || component == "." || component == ".." {
				return Options{}, fmt.Errorf("%w: invalid excluded prefix at index %d", ErrInvalidScannerOptions, index)
			}
		}
		options.ExcludedPrefixes[index] = prefix
	}
	return options, nil
}

// ObservationStore persists immutable directory history and the current
// group projection. Implementations should use one transaction for Commit.
type ObservationStore interface {
	Commit(ctx context.Context, observation DirectoryObservation, discoveries []Discovery) error
	ListDirectoryObservations(ctx context.Context, rootID domain.ConfigID) ([]DirectoryObservation, error)
	ListDiscoveries(ctx context.Context, rootID domain.ConfigID, includeInactive bool) ([]Discovery, error)
}

// Scanner performs one explicit read-only scan and persists its result.
type Scanner struct {
	filesystem ports.FilesystemReadPort
	store      ObservationStore
	sources    []DownloadSource
	options    Options
}

// New constructs a bounded scanner. It does not touch the filesystem or any
// upstream until Scan is called.
func New(filesystem ports.FilesystemReadPort, store ObservationStore, sources []DownloadSource, options Options) (*Scanner, error) {
	if filesystem == nil {
		return nil, fmt.Errorf("%w: filesystem port is required", ErrInvalidScannerOptions)
	}
	if store == nil {
		return nil, ErrStoreUnavailable
	}
	var err error
	options, err = normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	copySources := make([]DownloadSource, len(sources))
	copy(copySources, sources)
	for index, source := range copySources {
		if !source.ConnectionID.Valid() {
			return nil, fmt.Errorf("%w: download source %d has invalid connection id", ErrInvalidScannerOptions, index)
		}
		if source.Inventory == nil {
			return nil, fmt.Errorf("%w: download source %q has no inventory", ErrInvalidScannerOptions, source.ConnectionID)
		}
	}
	return &Scanner{filesystem: filesystem, store: store, sources: copySources, options: options}, nil
}

// NewScanner is an explicit constructor alias for dependency wiring.
func NewScanner(filesystem ports.FilesystemReadPort, store ObservationStore, sources []DownloadSource, options Options) (*Scanner, error) {
	return New(filesystem, store, sources, options)
}

// Scan observes one configured root. It never registers/imports media or
// changes a client. A root-level filesystem error is returned after safely
// retaining an unknown-coverage observation when possible.
func (scanner *Scanner) Scan(ctx context.Context, rootID domain.ConfigID) (ScanResult, error) {
	var result ScanResult
	if !rootID.Valid() {
		return result, fmt.Errorf("%w: invalid root id", ErrInvalidObservation)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	startedAt := scanner.now()
	observationID, err := domain.NewRuntimeID()
	if err != nil {
		return result, err
	}
	entries, coverage, enumErr := scanner.enumerate(ctx, rootID, observationID, startedAt)
	now := scanner.now()
	if now.Before(startedAt) {
		now = startedAt
	}
	coverage.CompletedAt = &now
	coverage.ObservedAt = now
	if enumErr != nil {
		coverage.Completeness = domain.CompletenessUnknown
		coverage.ReasonCodes = appendUnique(coverage.ReasonCodes, "root_unavailable")
	}
	manifestRevision := manifestRevision(entries)
	coverage.SnapshotRevision = manifestRevision
	observation := DirectoryObservation{
		ID: observationID, RootID: rootID, Entries: cloneEntries(entries), Coverage: coverage,
		UnsupportedChildren: unsupportedChildren(coverage.ReasonCodes),
		ObservedAt:          now, FirstSeenAt: now, LastSeenAt: now, ManifestRevision: manifestRevision,
	}
	if err := observation.Validate(); err != nil {
		return result, err
	}
	result = ScanResult{Observation: cloneDirectoryObservation(observation), Coverage: cloneCoverage(coverage)}
	if enumErr != nil {
		if commitErr := scanner.store.Commit(ctx, observation, nil); commitErr == nil {
			result.Persisted = true
		}
		return result, enumErr
	}
	previous, err := scanner.store.ListDiscoveries(ctx, rootID, true)
	if err != nil {
		return result, fmt.Errorf("load prior discoveries: %w", err)
	}
	clientEvidence, clientIncomplete, clientReasons, clientCoverages, err := scanner.clientEvidence(ctx, rootID)
	if err != nil {
		return result, err
	}
	discoveries := groupEntries(entries, coverage, previous, clientEvidence, clientIncomplete, clientReasons, clientCoverages, scanner.options, now)
	for index := range discoveries {
		if !discoveries[index].ID.Valid() {
			identifier, idErr := domain.NewRuntimeID()
			if idErr != nil {
				return result, fmt.Errorf("create discovery id: %w", idErr)
			}
			discoveries[index].ID = identifier
		}
		if err := discoveries[index].Validate(); err != nil {
			return result, fmt.Errorf("discovery %d: %w", index, err)
		}
	}
	if err := scanner.store.Commit(ctx, observation, discoveries); err != nil {
		return result, fmt.Errorf("persist discovery observation: %w", err)
	}
	result.Discoveries = cloneDiscoveries(discoveries)
	result.Persisted = true
	return result, nil
}

func (scanner *Scanner) now() time.Time {
	now := scanner.options.Now()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return now.UTC()
}

type enumerationState struct {
	entries    []domain.FileManifestEntry
	reasons    []string
	partial    bool
	unknown    bool
	count      int
	pageCount  int
	maxReached bool
}

func (scanner *Scanner) enumerate(ctx context.Context, rootID domain.ConfigID, sourceID domain.RuntimeID, now time.Time) ([]domain.FileManifestEntry, domain.Coverage, error) {
	state := enumerationState{entries: make([]domain.FileManifestEntry, 0)}
	err := scanner.enumerateDirectory(ctx, rootID, "", 0, &state)
	coverage := domain.Coverage{
		SourceID: sourceID, RootID: rootID, Completeness: domain.CompletenessComplete,
		ObservedCount: int64(state.count), ReasonCodes: uniqueReasons(state.reasons),
		StartedAt: &now, ObservedAt: now,
	}
	if state.unknown {
		coverage.Completeness = domain.CompletenessUnknown
	} else if state.partial {
		coverage.Completeness = domain.CompletenessPartial
	}
	if err != nil {
		coverage.Completeness = domain.CompletenessUnknown
		coverage.ReasonCodes = appendUnique(coverage.ReasonCodes, "root_unavailable")
	}
	return state.entries, coverage, err
}

func (scanner *Scanner) enumerateDirectory(ctx context.Context, rootID domain.ConfigID, prefix string, depth int, state *enumerationState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > scanner.options.MaxDepth {
		state.partial = true
		state.reasons = appendUnique(state.reasons, "enumeration_depth_limit")
		return nil
	}
	cursor := ""
	seenCursors := map[string]struct{}{"": {}}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if state.pageCount >= scanner.options.MaxFilesystemPages {
			state.partial = true
			state.maxReached = true
			state.reasons = appendUnique(state.reasons, "enumeration_page_limit")
			return nil
		}
		page, err := scanner.filesystem.EnumeratePage(ctx, rootID, prefix, cursor, scanner.options.PageSize)
		if err != nil {
			if prefix == "" {
				return err
			}
			state.partial = true
			state.reasons = appendUnique(state.reasons, unsupportedReasonCode(prefix, "directory_unavailable"))
			return nil
		}
		state.pageCount++
		for _, reason := range page.Coverage.ReasonCodes {
			if evidence, ok := ports.ParseUnsupportedChildReasonCode(reason); ok && scanner.isExcluded(evidence.RelativePath) {
				continue
			}
			state.reasons = appendUnique(state.reasons, reason)
			if strings.HasPrefix(reason, "unsupported_child:") {
				state.partial = true
			}
		}
		switch page.Coverage.Completeness {
		case domain.CompletenessComplete:
		case domain.CompletenessPartial:
			state.partial = true
		case domain.CompletenessUnknown:
			state.unknown = true
		default:
			state.unknown = true
			state.reasons = appendUnique(state.reasons, "coverage_unknown")
		}
		for _, entry := range page.Items {
			if scanner.isExcluded(entry.RelativePath) {
				continue
			}
			if state.count >= scanner.options.MaxEntries {
				state.partial = true
				state.maxReached = true
				state.reasons = appendUnique(state.reasons, "enumeration_limit")
				return nil
			}
			if err := entry.Validate(); err != nil {
				state.partial = true
				state.reasons = appendUnique(state.reasons, unsupportedReasonCode(entry.RelativePath, "invalid_manifest"))
				continue
			}
			state.entries = append(state.entries, cloneEntry(entry))
			state.count++
			if entry.Type == domain.ManifestDirectory {
				if err := scanner.enumerateDirectory(ctx, rootID, entry.RelativePath, depth+1, state); err != nil {
					if ctxErr := ctx.Err(); ctxErr != nil {
						return ctxErr
					}
					if prefix == "" {
						// A child directory failure is evidence in an otherwise
						// usable root. Keep the valid siblings and continue.
						state.partial = true
						state.reasons = appendUnique(state.reasons, unsupportedReasonCode(entry.RelativePath, "directory_unavailable"))
					} else {
						return err
					}
				}
				if state.maxReached {
					return nil
				}
			}
		}
		if page.NextCursor == "" || state.maxReached {
			break
		}
		if _, exists := seenCursors[page.NextCursor]; exists {
			state.partial = true
			state.maxReached = true
			state.reasons = appendUnique(state.reasons, "enumeration_cursor_cycle")
			break
		}
		seenCursors[page.NextCursor] = struct{}{}
		cursor = page.NextCursor
	}
	return nil
}

func (scanner *Scanner) isExcluded(relativePath string) bool {
	for _, prefix := range scanner.options.ExcludedPrefixes {
		if relativePath == prefix || strings.HasPrefix(relativePath, prefix+"/") {
			return true
		}
	}
	return false
}

type clientEvidenceResult struct {
	items      []downloadSourceItem
	coverages  []domain.Coverage
	incomplete bool
	reasons    []string
}

type downloadSourceItem struct {
	source DownloadSource
	item   ports.DownloadItem
}

func (scanner *Scanner) clientEvidence(ctx context.Context, rootID domain.ConfigID) ([]downloadSourceItem, bool, []string, []domain.Coverage, error) {
	result := clientEvidenceResult{items: make([]downloadSourceItem, 0), coverages: make([]domain.Coverage, 0, len(scanner.sources))}
	for _, source := range scanner.sources {
		coverageID, idErr := domain.NewRuntimeID()
		if idErr != nil {
			return nil, false, nil, nil, fmt.Errorf("create client coverage id: %w", idErr)
		}
		now := scanner.now()
		coverage := domain.Coverage{SourceID: coverageID, ConnectionID: source.ConnectionID, RootID: rootID, Completeness: domain.CompletenessComplete, StartedAt: &now, ObservedAt: now}
		if len(result.items) >= scanner.options.MaxClientItems {
			markClientCoverageIncomplete(&result, &coverage, domain.CompletenessUnknown, "client_inventory_limit")
			completed := scanner.now()
			if completed.Before(now) {
				completed = now
			}
			coverage.CompletedAt = &completed
			coverage.ObservedAt = completed
			result.coverages = append(result.coverages, coverage)
			continue
		}
		cursor := ""
		seenCursors := map[string]struct{}{"": {}}
		pages := 0
		for {
			if err := ctx.Err(); err != nil {
				return nil, false, nil, nil, err
			}
			if pages >= scanner.options.MaxClientPages {
				markClientCoverageIncomplete(&result, &coverage, domain.CompletenessPartial, "client_inventory_limit")
				break
			}
			page, err := source.Inventory.List(ctx, source.ConnectionID, cursor, scanner.options.PageSize)
			if err != nil {
				markClientCoverageIncomplete(&result, &coverage, domain.CompletenessUnknown, "client_inventory_incomplete")
				break
			}
			pages++
			coverage.ObservedCount += int64(len(page.Items))
			if page.Coverage.ObservedAt.After(coverage.ObservedAt) {
				coverage.ObservedAt = page.Coverage.ObservedAt
			}
			truncated := false
			for _, item := range page.Items {
				if len(result.items) >= scanner.options.MaxClientItems {
					truncated = true
					break
				}
				result.items = append(result.items, downloadSourceItem{source: source, item: cloneDownloadItem(item)})
			}
			for _, reason := range page.Coverage.ReasonCodes {
				result.reasons = appendUnique(result.reasons, reason)
				coverage.ReasonCodes = appendUnique(coverage.ReasonCodes, reason)
			}
			switch page.Coverage.Completeness {
			case domain.CompletenessComplete:
			case domain.CompletenessPartial:
				markClientCoverageIncomplete(&result, &coverage, domain.CompletenessPartial, "client_inventory_incomplete")
			case domain.CompletenessUnknown:
				markClientCoverageIncomplete(&result, &coverage, domain.CompletenessUnknown, "client_inventory_incomplete")
			default:
				markClientCoverageIncomplete(&result, &coverage, domain.CompletenessUnknown, "client_inventory_incomplete")
			}
			if truncated || (len(result.items) >= scanner.options.MaxClientItems && page.NextCursor != "") {
				markClientCoverageIncomplete(&result, &coverage, domain.CompletenessPartial, "client_inventory_limit")
				break
			}
			if page.NextCursor == "" {
				break
			}
			if _, exists := seenCursors[page.NextCursor]; exists {
				markClientCoverageIncomplete(&result, &coverage, domain.CompletenessPartial, "client_inventory_cursor_cycle")
				break
			}
			seenCursors[page.NextCursor] = struct{}{}
			cursor = page.NextCursor
		}
		completed := scanner.now()
		if completed.Before(now) {
			completed = now
		}
		coverage.CompletedAt = &completed
		coverage.ObservedAt = completed
		result.coverages = append(result.coverages, coverage)
	}
	return result.items, result.incomplete, uniqueReasons(result.reasons), result.coverages, nil
}

func markClientCoverageIncomplete(result *clientEvidenceResult, coverage *domain.Coverage, completeness domain.Completeness, reason string) {
	result.incomplete = true
	result.reasons = appendUnique(result.reasons, reason)
	coverage.ReasonCodes = appendUnique(coverage.ReasonCodes, reason)
	if completeness == domain.CompletenessUnknown || coverage.Completeness == domain.CompletenessComplete {
		coverage.Completeness = completeness
	}
}

type groupFiles struct {
	key   string
	files []domain.FileManifestEntry
}

func groupEntries(entries []domain.FileManifestEntry, coverage domain.Coverage, previous []Discovery, clientItems []downloadSourceItem, clientIncomplete bool, clientReasons []string, clientCoverages []domain.Coverage, options Options, now time.Time) []Discovery {
	groups := make(map[string]*groupFiles)
	keys := make([]string, 0)
	for _, entry := range entries {
		if entry.Type == domain.ManifestDirectory {
			continue
		}
		key := groupKey(entry.RelativePath)
		if _, exists := groups[key]; !exists {
			groups[key] = &groupFiles{key: key}
			keys = append(keys, key)
		}
		groups[key].files = append(groups[key].files, cloneEntry(entry))
	}
	sort.Strings(keys)
	previousByKey := make(map[string]Discovery, len(previous))
	for _, prior := range previous {
		previousByKey[prior.RelativePath] = prior
	}
	result := make([]Discovery, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		prior := previousByKey[key]
		discovery := buildDiscovery(group.key, group.files, coverage, prior, clientItems, clientIncomplete, clientReasons, clientCoverages, options, now)
		result = append(result, discovery)
	}
	return result
}

func buildDiscovery(key string, files []domain.FileManifestEntry, coverage domain.Coverage, prior Discovery, clientItems []downloadSourceItem, clientIncomplete bool, clientReasons []string, clientCoverages []domain.Coverage, options Options, now time.Time) Discovery {
	discovery := Discovery{
		ID: prior.ID, RootID: files[0].RootID, RelativePath: key, Files: cloneEntries(files),
		Coverage: coverage, ClientCoverage: cloneCoverages(clientCoverages), UnsupportedChildren: unsupportedChildrenForGroup(coverage.ReasonCodes, key),
		ObservedAt: now, FirstSeenAt: now, LastSeenAt: now, Active: true,
		ManifestRevision: manifestRevision(files), ProvenanceState: ProvenanceUnknown,
		Readiness: domain.ReadinessUnknown,
	}
	if prior.FirstSeenAt.IsZero() == false && prior.FirstSeenAt.Before(discovery.FirstSeenAt) {
		discovery.FirstSeenAt = prior.FirstSeenAt
	}
	discovery.Videos, discovery.Subtitles, discovery.Companions, discovery.Kind = classifyFiles(files)
	discovery.Stability = stabilityFor(files, prior.Stability, options.MinimumStableSpacing, now)
	discovery.Provenance, discovery.ClientCompletion = correlateClient(discovery.RootID, key, files, clientItems, now)
	if (clientIncomplete || !clientCoverageComplete(discovery.ClientCoverage)) && discovery.ClientCompletion.State == ClientCompletionComplete {
		discovery.ClientCompletion.State = ClientCompletionUnknown
	}
	if len(discovery.Provenance) > 0 {
		discovery.ProvenanceState = ProvenanceKnown
	}
	if clientIncomplete {
		discovery.ReviewReasons = appendReview(discovery.ReviewReasons, ReviewClientInventoryIncomplete)
	}
	for _, reason := range clientReasons {
		if evidence, ok := ports.ParseUnsupportedChildReasonCode(reason); ok {
			if pathWithinGroup(evidence.RelativePath, key) {
				discovery.ReviewReasons = appendReview(discovery.ReviewReasons, ReviewUnsupportedChild)
			}
		}
	}
	discovery.ReviewReasons = appendReviewReasons(discovery.ReviewReasons, reviewForDiscovery(discovery))
	discovery.Readiness = readinessFor(discovery)
	return discovery
}

func groupKey(relativePath string) string {
	directory := path.Dir(relativePath)
	if directory == "." {
		base := path.Base(relativePath)
		stem := base[:len(base)-len(path.Ext(base))]
		if fileClass(relativePath) == "subtitle" {
			stem = subtitleGroupStem(stem)
		}
		if stem == "" {
			stem = base
		}
		// Root files and top-level directories share one path namespace. Keep
		// root-file groups explicitly tagged so Movie.mkv cannot merge with
		// Movie/Other.mkv while subtitle/video siblings retain one key.
		return "file:" + stem
	}
	parts := strings.Split(directory, "/")
	return parts[0]
}

func subtitleGroupStem(stem string) string {
	stem = strings.TrimSpace(stem)
	for {
		separator := strings.LastIndexAny(stem, " ._-")
		if separator < 0 || separator == len(stem)-1 {
			break
		}
		tail := strings.ToLower(stem[separator+1:])
		if tail != "forced" && tail != "sdh" && tail != "hi" && len(tail) != 2 && !(len(tail) == 3 && isASCIIWord(tail)) {
			break
		}
		stem = strings.TrimSpace(stem[:separator])
	}
	return stem
}

func isASCIIWord(value string) bool {
	for _, char := range value {
		if char < 'a' || char > 'z' {
			return false
		}
	}
	return value != ""
}

var (
	seasonEpisodePattern = regexp.MustCompile(`(?i)(?:^|[^a-z])s(\d{1,2})e(\d{1,4})(?:[^a-z0-9]*e?(\d{1,4}))?`)
	xEpisodePattern      = regexp.MustCompile(`(?i)(?:^|[^0-9])(\d{1,2})x(\d{1,4})(?:[^0-9]+(?:e|x)?(\d{1,4}))?`)
	seasonPattern        = regexp.MustCompile(`(?i)(?:season[ ._-]*|^s)(\d{1,2})(?:[^0-9]|$)`)
	animeNumberPattern   = regexp.MustCompile(`(?:^|[\[\( _.-])(\d{2,4})(?:[\]\) _.-]|$)`)
)

func classifyFiles(files []domain.FileManifestEntry) ([]MediaAssociation, []SubtitleAssociation, []Companion, GroupKind) {
	videos := make([]domain.FileManifestEntry, 0)
	subtitleEntries := make([]domain.FileManifestEntry, 0)
	companions := make([]Companion, 0)
	for _, entry := range files {
		switch fileClass(entry.RelativePath) {
		case "video":
			videos = append(videos, entry)
		case "subtitle":
			subtitleEntries = append(subtitleEntries, entry)
		default:
			kind := CompanionKnown
			reason := "known companion"
			if !isKnownCompanion(entry.RelativePath) {
				kind = CompanionUnsupported
				reason = "unsupported companion requires classification"
			}
			companions = append(companions, Companion{FilePath: entry.RelativePath, Kind: kind, Reason: reason})
		}
	}
	videoAssociations := make([]MediaAssociation, 0, len(videos))
	groupKind := GroupUnknown
	for _, video := range videos {
		association := associationFor(video.RelativePath)
		videoAssociations = append(videoAssociations, association)
		if groupKind == GroupUnknown {
			groupKind = groupKindFor(association)
		} else if groupKind != groupKindFor(association) {
			groupKind = GroupMixed
		}
	}
	if len(videos) == 0 {
		if len(subtitleEntries) > 0 || len(companions) > 0 {
			groupKind = GroupUnknown
		}
	} else if len(videos) > 1 {
		switch groupKind {
		case GroupAnime:
		case GroupEpisode, GroupSeasonPack:
			groupKind = GroupSeasonPack
		default:
			groupKind = GroupMixed
		}
	}
	subtitles := make([]SubtitleAssociation, 0, len(subtitleEntries))
	videoPaths := make([]string, 0, len(videos))
	pairPaths := make(map[string]struct{}, len(subtitleEntries))
	for _, video := range videos {
		videoPaths = append(videoPaths, video.RelativePath)
	}
	for _, subtitle := range subtitleEntries {
		extension := strings.ToLower(path.Ext(subtitle.RelativePath))
		if extension == ".idx" || extension == ".sub" {
			pairPaths[strings.ToLower(subtitle.RelativePath)] = struct{}{}
		}
	}
	for _, subtitle := range subtitleEntries {
		association := subtitleFor(subtitle.RelativePath, videoPaths)
		if association.PairID != "" {
			extension := strings.ToLower(path.Ext(subtitle.RelativePath))
			counterpart := ".idx"
			if extension == ".idx" {
				counterpart = ".sub"
			}
			_, paired := pairPaths[strings.ToLower(strings.TrimSuffix(subtitle.RelativePath, path.Ext(subtitle.RelativePath))+counterpart)]
			if !paired {
				association.Confidence = ConfidenceUnresolved
				association.Reason = "IDX/SUB pair is incomplete"
			}
		}
		subtitles = append(subtitles, association)
		if association.Reason != "" && association.Reason != "paired subtitle" {
			companions = append(companions, Companion{FilePath: association.FilePath, Kind: CompanionUnmatchedSubtitle, VideoPaths: append([]string(nil), association.VideoPaths...), Reason: association.Reason})
		}
	}
	return videoAssociations, subtitles, companions, groupKind
}

func fileClass(relativePath string) string {
	extension := strings.ToLower(path.Ext(relativePath))
	switch extension {
	case ".mkv", ".mp4", ".m4v", ".avi", ".mov", ".webm", ".ts", ".m2ts", ".wmv", ".flv":
		return "video"
	case ".srt", ".ass", ".ssa", ".vtt":
		return "subtitle"
	case ".idx", ".sub":
		return "subtitle"
	default:
		return "companion"
	}
}

func isKnownCompanion(relativePath string) bool {
	switch strings.ToLower(path.Ext(relativePath)) {
	case ".nfo", ".jpg", ".jpeg", ".png", ".webp", ".txt", ".sfv", ".md5", ".srr":
		return true
	default:
		return false
	}
}

func associationFor(relativePath string) MediaAssociation {
	base := strings.TrimSuffix(path.Base(relativePath), path.Ext(relativePath))
	if match := seasonEpisodePattern.FindStringSubmatch(base); match != nil {
		season, _ := strconv.Atoi(match[1])
		episodes := []int{}
		if episode, parseErr := strconv.Atoi(match[2]); parseErr == nil {
			episodes = append(episodes, episode)
		}
		if match[3] != "" {
			if episode, parseErr := strconv.Atoi(match[3]); parseErr == nil {
				episodes = append(episodes, episode)
			}
		}
		return MediaAssociation{FilePath: relativePath, Kind: domain.MediaEpisode, Confidence: ConfidenceExact, SeasonNumber: intPointer(season), EpisodeNumbers: episodes}
	}
	if match := xEpisodePattern.FindStringSubmatch(base); match != nil {
		season, _ := strconv.Atoi(match[1])
		episodes := []int{}
		if episode, parseErr := strconv.Atoi(match[2]); parseErr == nil {
			episodes = append(episodes, episode)
		}
		if match[3] != "" {
			if episode, parseErr := strconv.Atoi(match[3]); parseErr == nil {
				episodes = append(episodes, episode)
			}
		}
		return MediaAssociation{FilePath: relativePath, Kind: domain.MediaEpisode, Confidence: ConfidenceExact, SeasonNumber: intPointer(season), EpisodeNumbers: episodes}
	}
	if match := seasonPattern.FindStringSubmatch(base); match != nil {
		season, parseErr := strconv.Atoi(match[1])
		if parseErr == nil {
			return MediaAssociation{FilePath: relativePath, Kind: domain.MediaSeason, Confidence: ConfidenceSuggested, SeasonNumber: intPointer(season), Reason: "season pack requires episode selection"}
		}
	}
	if absolute := animeAbsoluteNumber(base); absolute != nil {
		return MediaAssociation{FilePath: relativePath, Kind: domain.MediaAnime, Confidence: ConfidenceUnresolved, AbsoluteNumber: absolute, Reason: "absolute anime numbering needs Sonarr mapping"}
	}
	return MediaAssociation{FilePath: relativePath, Kind: domain.MediaMovie, Confidence: ConfidenceAmbiguous, Reason: "title identity requires review"}
}

func groupKindFor(association MediaAssociation) GroupKind {
	if association.Kind == domain.MediaAnime {
		return GroupAnime
	}
	if association.Kind == domain.MediaSeason {
		return GroupSeasonPack
	}
	if association.Kind == domain.MediaEpisode {
		return GroupEpisode
	}
	return GroupMovie
}

func animeAbsoluteNumber(base string) *int {
	for _, match := range animeNumberPattern.FindAllStringSubmatch(base, -1) {
		if len(match) < 2 {
			continue
		}
		number, err := strconv.Atoi(match[1])
		if err != nil || number < 1 || number > 9999 {
			continue
		}
		// Four-digit tokens in common movie names are years even when wrapped
		// in parentheses. Keep year-like evidence ambiguous until a later
		// title/episode mapping confirms anime semantics.
		if len(match[1]) == 4 && number >= 1000 && number <= 2999 {
			continue
		}
		return intPointer(number)
	}
	return nil
}

func subtitleFor(relativePath string, videoPaths []string) SubtitleAssociation {
	base := strings.TrimSuffix(path.Base(relativePath), path.Ext(relativePath))
	normalized := normalizeStem(base)
	candidates := make([]string, 0)
	for _, video := range videoPaths {
		videoBase := strings.TrimSuffix(path.Base(video), path.Ext(video))
		videoStem := normalizeStem(videoBase)
		if normalized == videoStem || strings.HasPrefix(normalized, videoStem+" ") || strings.HasPrefix(videoStem, normalized+" ") {
			candidates = append(candidates, video)
		}
	}
	if len(candidates) == 0 && len(videoPaths) == 1 {
		candidates = append(candidates, videoPaths[0])
	}
	forced := containsToken(base, "forced")
	hearingImpaired := containsToken(base, "sdh") || containsToken(base, "hi") || containsToken(base, "hearing impaired")
	language := ""
	for _, token := range strings.Fields(strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(strings.ToLower(base))) {
		if len(token) < 2 || len(token) > 3 || !isASCIIWord(token) || isSubtitleLabel(token) {
			continue
		}
		language = token
		break
	}
	pairID := ""
	if extension := strings.ToLower(path.Ext(relativePath)); extension == ".idx" || extension == ".sub" {
		pairID = strings.TrimSuffix(relativePath, path.Ext(relativePath))
	}
	result := SubtitleAssociation{FilePath: relativePath, VideoPaths: candidates, PairID: pairID, Language: language, Forced: forced, HearingImpaired: hearingImpaired, Confidence: ConfidenceSuggested}
	if len(candidates) == 1 {
		result.Confidence = ConfidenceExact
		result.Reason = "paired subtitle"
	} else if len(candidates) > 1 {
		result.Confidence = ConfidenceAmbiguous
		result.Reason = "subtitle matches multiple videos"
	} else {
		result.Confidence = ConfidenceUnresolved
		result.Reason = "subtitle has no matching video"
	}
	return result
}

func isSubtitleLabel(value string) bool {
	switch strings.ToLower(value) {
	case "forced", "sdh", "hi":
		return true
	default:
		return false
	}
}

func normalizeStem(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, ".", " ")
	value = strings.Join(strings.Fields(value), " ")
	for _, token := range []string{"forced", "sdh", "hearing impaired", "hi"} {
		value = strings.ReplaceAll(value, token, " ")
	}
	return strings.Join(strings.Fields(value), " ")
}

func containsToken(value, token string) bool {
	value = strings.ToLower(value)
	token = strings.ToLower(token)
	return strings.Contains(" "+strings.NewReplacer(".", " ", "_", " ", "-", " ").Replace(value)+" ", " "+token+" ")
}

func correlateClient(rootID domain.ConfigID, groupPath string, files []domain.FileManifestEntry, items []downloadSourceItem, now time.Time) ([]domain.Provenance, ClientCompletionObservation) {
	provenance := make([]domain.Provenance, 0)
	completion := ClientCompletionObservation{State: ClientCompletionUnknown, ObservedAt: now, Known: false}
	seen := make(map[string]struct{})
	for _, candidate := range items {
		matched := false
		for _, payload := range candidate.item.Payload {
			if payload.RootID != rootID || !manifestTouchesGroup(payload, groupPath, files) {
				continue
			}
			matched = true
			break
		}
		if !matched {
			continue
		}
		key := candidate.source.ConnectionID.String() + "\x00" + candidate.item.ExternalID + "\x00" + candidate.item.Hash
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		var completedAt *time.Time
		if candidate.item.CompletedAt != nil {
			value := candidate.item.CompletedAt.UTC()
			completedAt = &value
		}
		var sourcePath *domain.FileTarget
		if len(files) > 0 {
			target := domain.FileTarget{RootID: rootID, RelativePath: files[0].RelativePath}
			sourcePath = &target
		}
		if candidate.item.ExternalID == "" && candidate.item.Hash == "" {
			continue
		}
		descriptorID := domain.RuntimeID("")
		if candidate.item.Descriptor != nil {
			descriptorID = candidate.item.Descriptor.ID
		}
		provenance = append(provenance, domain.Provenance{ConnectionID: candidate.source.ConnectionID, ClientItemID: candidate.item.ExternalID, Hash: candidate.item.Hash, CompletedAt: completedAt, DescriptorID: descriptorID, SourcePath: sourcePath})
		candidateState := completionState(candidate.item)
		completion = mergeCompletion(completion, ClientItemObservation{
			ConnectionID: candidate.source.ConnectionID,
			ClientItemID: candidate.item.ExternalID,
			Hash:         candidate.item.Hash,
			State:        candidateState,
			CompletedAt:  completedAt,
		})
	}
	return provenance, completion
}

func manifestTouchesGroup(payload domain.FileManifestEntry, groupPath string, files []domain.FileManifestEntry) bool {
	if payload.RelativePath == groupPath || strings.HasPrefix(payload.RelativePath, groupPath+"/") || strings.HasPrefix(groupPath, payload.RelativePath+"/") {
		return true
	}
	for _, file := range files {
		if payload.RelativePath == file.RelativePath || payload.Type == domain.ManifestDirectory && strings.HasPrefix(file.RelativePath, payload.RelativePath+"/") {
			return true
		}
	}
	return false
}

func completionState(item ports.DownloadItem) ClientCompletionState {
	if item.ProcessingDone {
		return ClientCompletionComplete
	}
	state := strings.ToLower(strings.TrimSpace(item.State))
	switch {
	case strings.Contains(state, "unpack"), strings.Contains(state, "extract"), strings.Contains(state, "repair"), strings.Contains(state, "process"), strings.HasPrefix(state, "pp_"):
		return ClientCompletionProcessing
	case state == "completed", state == "complete", state == "seeding", state == "uploading", state == "stalledup", state == "pausedup", state == "queuedup":
		return ClientCompletionComplete
	case strings.Contains(state, "check"), state == "moving", state == "allocating":
		return ClientCompletionProcessing
	case state == "downloading", state == "stalleddl", state == "pauseddl", state == "queued", state == "queueddl", state == "active", state == "metadl", state == "forced", state == "forceddl":
		return ClientCompletionDownloading
	default:
		return ClientCompletionUnknown
	}
}

func mergeCompletion(current ClientCompletionObservation, item ClientItemObservation) ClientCompletionObservation {
	current.Known = true
	if current.ConnectionID == "" && len(current.Items) == 0 {
		current.ConnectionID = item.ConnectionID
	} else if current.ConnectionID != item.ConnectionID {
		current.ConnectionID = ""
	}
	current.Items = append(current.Items, cloneClientItemObservation(item))
	if item.ClientItemID != "" {
		// Keep this compatibility projection lossless in the presence of
		// equal IDs from different connections. Callers needing identity use
		// Items, which always includes ConnectionID.
		current.ClientItemIDs = append(current.ClientItemIDs, item.ClientItemID)
	}
	if item.CompletedAt != nil && (current.CompletedAt == nil || item.CompletedAt.Before(*current.CompletedAt)) {
		value := *item.CompletedAt
		current.CompletedAt = &value
	}
	if item.State == ClientCompletionUnknown {
		current.UnknownItem = true
		current.State = ClientCompletionUnknown
		return current
	}
	if current.UnknownItem {
		return current
	}
	// In-progress evidence wins over a completed item so a multi-source or
	// duplicate-path group never becomes ready prematurely.
	priority := func(value ClientCompletionState) int {
		switch value {
		case ClientCompletionDownloading:
			return 3
		case ClientCompletionProcessing:
			return 2
		case ClientCompletionComplete:
			return 1
		default:
			return 0
		}
	}
	if priority(item.State) > priority(current.State) {
		current.State = item.State
	}
	return current
}

func stabilityFor(files []domain.FileManifestEntry, prior StabilityObservation, minimumSpacing time.Duration, now time.Time) StabilityObservation {
	result := StabilityObservation{State: StabilityStable, ObservedAt: now, MinimumSpacing: minimumSpacing, Files: make([]FileStability, 0, len(files))}
	priorByPath := make(map[string]FileStability, len(prior.Files))
	for _, item := range prior.Files {
		priorByPath[item.Path] = item
	}
	for _, entry := range files {
		fingerprint := fileFingerprint(entry)
		file := FileStability{Path: entry.RelativePath, State: StabilityUnknown, ObservedAt: now, ObservationCount: 1, CurrentFingerprint: fingerprint}
		if strings.TrimSpace(entry.FileIdentity) == "" {
			file.PreviousFingerprint = ""
		} else if previous, ok := priorByPath[entry.RelativePath]; ok {
			file.ObservationCount = previous.ObservationCount + 1
			file.PreviousFingerprint = previous.CurrentFingerprint
			previousAt := previous.ObservedAt
			file.PreviousObservedAt = &previousAt
			same := previous.CurrentFingerprint == fingerprint
			if !same {
				file.State = StabilityChanging
			} else if now.Sub(previous.ObservedAt) >= minimumSpacing {
				file.State = StabilityStable
			} else {
				file.State = StabilityUnknown
			}
		}
		result.Files = append(result.Files, file)
		switch file.State {
		case StabilityChanging:
			result.State = StabilityChanging
		case StabilityUnknown:
			if result.State == StabilityStable {
				result.State = StabilityUnknown
			}
		}
	}
	currentPaths := make(map[string]struct{}, len(files))
	for _, entry := range files {
		currentPaths[entry.RelativePath] = struct{}{}
	}
	for _, previous := range prior.Files {
		if _, exists := currentPaths[previous.Path]; exists {
			continue
		}
		result.Files = append(result.Files, FileStability{Path: previous.Path, State: StabilityChanging, ObservedAt: now, PreviousObservedAt: timePointer(previous.ObservedAt), ObservationCount: previous.ObservationCount, PreviousFingerprint: previous.CurrentFingerprint})
		result.State = StabilityChanging
	}
	if len(files) == 0 {
		result.State = StabilityUnknown
	}
	if result.State == StabilityChanging {
		result.Reason = "one or more files changed between observations"
	} else if result.State == StabilityUnknown {
		result.Reason = "two observations at the configured spacing are required"
	}
	return result
}

func readinessFor(discovery Discovery) domain.Readiness {
	if len(discovery.Videos) == 0 {
		if len(discovery.Companions) > 0 || len(discovery.Subtitles) > 0 {
			return domain.ReadinessUnsupported
		}
		return domain.ReadinessUnknown
	}
	if discovery.ClientCompletion.State == ClientCompletionDownloading {
		return domain.ReadinessDownloading
	}
	if discovery.ClientCompletion.State == ClientCompletionProcessing {
		return domain.ReadinessProcessing
	}
	if discovery.Stability.State == StabilityChanging {
		return domain.ReadinessChanging
	}
	if discovery.Stability.State != StabilityStable || discovery.Coverage.Completeness != domain.CompletenessComplete || discovery.ClientCompletion.State != ClientCompletionComplete || !clientCoverageComplete(discovery.ClientCoverage) {
		return domain.ReadinessUnknown
	}
	return domain.ReadinessReady
}

func clientCoverageComplete(coverages []domain.Coverage) bool {
	if len(coverages) == 0 {
		return false
	}
	for _, coverage := range coverages {
		if coverage.Completeness != domain.CompletenessComplete {
			return false
		}
	}
	return true
}

func reviewForDiscovery(discovery Discovery) []ReviewReason {
	result := make([]ReviewReason, 0)
	if discovery.ProvenanceState == ProvenanceUnknown {
		result = appendReview(result, ReviewOrphanProvenance)
	}
	if discovery.ClientCompletion.State == ClientCompletionUnknown {
		result = appendReview(result, ReviewClientCompletionUnknown)
	}
	if discovery.Stability.State == StabilityUnknown {
		result = appendReview(result, ReviewStabilityUnknown)
	}
	if discovery.Stability.State == StabilityChanging {
		result = appendReview(result, ReviewFileChanging)
	}
	if discovery.Coverage.Completeness != domain.CompletenessComplete {
		result = appendReview(result, ReviewCoveragePartial)
	}
	if len(discovery.ClientCoverage) > 0 && !clientCoverageComplete(discovery.ClientCoverage) {
		result = appendReview(result, ReviewClientInventoryIncomplete)
	}
	for _, video := range discovery.Videos {
		if video.Confidence == ConfidenceAmbiguous {
			result = appendReview(result, ReviewAmbiguousAssociation)
		}
		if video.Kind == domain.MediaAnime {
			result = appendReview(result, ReviewAnimeMappingRequired)
		}
	}
	for _, subtitle := range discovery.Subtitles {
		if subtitle.Reason != "paired subtitle" {
			result = appendReview(result, ReviewUnmatchedSubtitle)
		}
	}
	for _, companion := range discovery.Companions {
		if companion.Kind == CompanionUnsupported {
			result = appendReview(result, ReviewUnsupportedCompanion)
		}
		if companion.Kind == CompanionUnmatchedSubtitle {
			result = appendReview(result, ReviewUnmatchedSubtitle)
		}
	}
	if len(discovery.UnsupportedChildren) > 0 {
		result = appendReview(result, ReviewUnsupportedChild)
	}
	return result
}

// MemoryStore is a concurrency-safe reference implementation used by unit
// tests and small embedders. Its history semantics mirror the SQLite
// repository: observations append, current groups upsert by root/path, and a
// complete scan retires groups absent from that complete evidence.
type MemoryStore struct {
	mu           sync.RWMutex
	observations map[domain.ConfigID][]DirectoryObservation
	discoveries  map[domain.ConfigID]map[string]Discovery
}

// NewMemoryStore creates an empty durable-store-shaped implementation.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{observations: make(map[domain.ConfigID][]DirectoryObservation), discoveries: make(map[domain.ConfigID]map[string]Discovery)}
}

var _ ObservationStore = (*MemoryStore)(nil)

// Commit validates and atomically applies one observation and its group
// projection. It never treats partial or unknown coverage as absence.
func (store *MemoryStore) Commit(ctx context.Context, observation DirectoryObservation, discoveries []Discovery) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := observation.Validate(); err != nil {
		return err
	}
	inputPaths := make(map[string]struct{}, len(discoveries))
	for index := range discoveries {
		if !discoveries[index].ID.Valid() {
			identifier, idErr := domain.NewRuntimeID()
			if idErr != nil {
				return fmt.Errorf("create discovery id: %w", idErr)
			}
			discoveries[index].ID = identifier
		}
		if err := discoveries[index].Validate(); err != nil {
			return fmt.Errorf("discovery %d: %w", index, err)
		}
		if discoveries[index].RootID != observation.RootID || discoveries[index].Coverage.SourceID != observation.Coverage.SourceID {
			return fmt.Errorf("%w: discovery %d has mismatched observation scope", ErrInvalidObservation, index)
		}
		if _, exists := inputPaths[discoveries[index].RelativePath]; exists {
			return fmt.Errorf("%w: duplicate discovery path %q", ErrInvalidObservation, discoveries[index].RelativePath)
		}
		inputPaths[discoveries[index].RelativePath] = struct{}{}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.observations == nil {
		store.observations = make(map[domain.ConfigID][]DirectoryObservation)
	}
	if store.discoveries == nil {
		store.discoveries = make(map[domain.ConfigID]map[string]Discovery)
	}
	store.observations[observation.RootID] = append(store.observations[observation.RootID], cloneDirectoryObservation(observation))
	current := store.discoveries[observation.RootID]
	if current == nil {
		current = make(map[string]Discovery)
		store.discoveries[observation.RootID] = current
	}
	seen := make(map[string]struct{}, len(discoveries))
	for _, incoming := range discoveries {
		key := incoming.RelativePath
		seen[key] = struct{}{}
		if existing, ok := current[key]; ok {
			incoming.ID = existing.ID
			incoming.FirstSeenAt = existing.FirstSeenAt
			if incoming.FirstSeenAt.IsZero() || observation.ObservedAt.Before(incoming.FirstSeenAt) {
				incoming.FirstSeenAt = observation.ObservedAt
			}
		}
		incoming.LastSeenAt = observation.ObservedAt
		incoming.Active = true
		current[key] = cloneDiscovery(incoming)
	}
	if observation.Coverage.Completeness == domain.CompletenessComplete {
		for key, existing := range current {
			if _, exists := seen[key]; exists || !existing.Active {
				continue
			}
			existing.Active = false
			existing.Readiness = domain.ReadinessUnknown
			existing.ReviewReasons = appendReview(existing.ReviewReasons, ReviewNotSeenInCompleteScan)
			current[key] = existing
		}
	}
	return nil
}

// ListDirectoryObservations returns append-only history ordered oldest first.
func (store *MemoryStore) ListDirectoryObservations(ctx context.Context, rootID domain.ConfigID) ([]DirectoryObservation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	items := store.observations[rootID]
	result := make([]DirectoryObservation, len(items))
	for index, item := range items {
		result[index] = cloneDirectoryObservation(item)
	}
	return result, nil
}

// ListDiscoveries returns current active groups by default, or includes
// retired historical groups when includeInactive is true.
func (store *MemoryStore) ListDiscoveries(ctx context.Context, rootID domain.ConfigID, includeInactive bool) ([]Discovery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	result := make([]Discovery, 0)
	for _, item := range store.discoveries[rootID] {
		if !includeInactive && !item.Active {
			continue
		}
		result = append(result, cloneDiscovery(item))
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].LastSeenAt.Equal(result[right].LastSeenAt) {
			return result[left].RelativePath < result[right].RelativePath
		}
		return result[left].LastSeenAt.After(result[right].LastSeenAt)
	})
	return result, nil
}

// Current is a convenience view of active groups.
func (store *MemoryStore) Current(ctx context.Context, rootID domain.ConfigID) ([]Discovery, error) {
	return store.ListDiscoveries(ctx, rootID, false)
}

// History is a convenience alias for ListDirectoryObservations.
func (store *MemoryStore) History(ctx context.Context, rootID domain.ConfigID) ([]DirectoryObservation, error) {
	return store.ListDirectoryObservations(ctx, rootID)
}

func (observation DirectoryObservation) Validate() error {
	if !observation.ID.Valid() || !observation.RootID.Valid() {
		return fmt.Errorf("%w: observation identifiers are invalid", ErrInvalidObservation)
	}
	if observation.RelativePrefix != "" {
		if err := domain.ValidateRelativePath(observation.RelativePrefix); err != nil {
			return fmt.Errorf("%w: observation prefix: %v", ErrInvalidObservation, err)
		}
	}
	if err := observation.Coverage.Validate(); err != nil {
		return fmt.Errorf("%w: coverage: %v", ErrInvalidObservation, err)
	}
	if observation.Coverage.RootID != observation.RootID {
		return fmt.Errorf("%w: coverage root mismatch", ErrInvalidObservation)
	}
	if observation.ObservedAt.IsZero() || observation.FirstSeenAt.IsZero() || observation.LastSeenAt.IsZero() {
		return fmt.Errorf("%w: observation timestamps are required", ErrInvalidObservation)
	}
	if observation.FirstSeenAt.After(observation.LastSeenAt) || observation.LastSeenAt.After(observation.ObservedAt) {
		return fmt.Errorf("%w: observation timestamp order is invalid", ErrInvalidObservation)
	}
	seen := make(map[string]struct{}, len(observation.Entries))
	for index, entry := range observation.Entries {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("%w: entry %d: %v", ErrInvalidObservation, index, err)
		}
		if entry.RootID != observation.RootID {
			return fmt.Errorf("%w: entry %d has mismatched root", ErrInvalidObservation, index)
		}
		if _, exists := seen[entry.RelativePath]; exists {
			return fmt.Errorf("%w: duplicate entry %q", ErrInvalidObservation, entry.RelativePath)
		}
		seen[entry.RelativePath] = struct{}{}
	}
	for index, evidence := range observation.UnsupportedChildren {
		if err := domain.ValidateRelativePath(evidence.RelativePath); err != nil {
			return fmt.Errorf("%w: unsupported child %d path: %v", ErrInvalidObservation, index, err)
		}
		if strings.TrimSpace(evidence.Reason) == "" {
			return fmt.Errorf("%w: unsupported child %d reason is required", ErrInvalidObservation, index)
		}
	}
	return nil
}

func (discovery Discovery) Validate() error {
	if !discovery.ID.Valid() || !discovery.RootID.Valid() {
		return fmt.Errorf("%w: discovery identifiers are invalid", ErrInvalidObservation)
	}
	if err := domain.ValidateRelativePath(discovery.RelativePath); err != nil {
		return fmt.Errorf("%w: discovery path: %v", ErrInvalidObservation, err)
	}
	switch discovery.Kind {
	case GroupMovie, GroupEpisode, GroupSeasonPack, GroupAnime, GroupMixed, GroupUnknown:
	default:
		return fmt.Errorf("%w: unknown discovery kind", ErrInvalidObservation)
	}
	if err := discovery.Coverage.Validate(); err != nil {
		return fmt.Errorf("%w: coverage: %v", ErrInvalidObservation, err)
	}
	if discovery.Coverage.RootID != discovery.RootID {
		return fmt.Errorf("%w: coverage root mismatch", ErrInvalidObservation)
	}
	if discovery.ObservedAt.IsZero() || discovery.FirstSeenAt.IsZero() || discovery.LastSeenAt.IsZero() || discovery.LastSeenAt.After(discovery.ObservedAt) {
		return fmt.Errorf("%w: discovery timestamps are invalid", ErrInvalidObservation)
	}
	if discovery.ProvenanceState != ProvenanceKnown && discovery.ProvenanceState != ProvenanceUnknown {
		return fmt.Errorf("%w: provenance state is invalid", ErrInvalidObservation)
	}
	if discovery.ProvenanceState == ProvenanceKnown && len(discovery.Provenance) == 0 {
		return fmt.Errorf("%w: known provenance requires evidence", ErrInvalidObservation)
	}
	switch discovery.Readiness {
	case domain.ReadinessReady, domain.ReadinessDownloading, domain.ReadinessProcessing, domain.ReadinessChanging, domain.ReadinessUnsupported, domain.ReadinessUnknown:
	default:
		return fmt.Errorf("%w: readiness is invalid", ErrInvalidObservation)
	}
	switch discovery.Stability.State {
	case StabilityStable, StabilityChanging, StabilityUnknown:
	default:
		return fmt.Errorf("%w: stability state is invalid", ErrInvalidObservation)
	}
	if discovery.Stability.ObservedAt.IsZero() || discovery.Stability.MinimumSpacing <= 0 {
		return fmt.Errorf("%w: stability observation is incomplete", ErrInvalidObservation)
	}
	switch discovery.ClientCompletion.State {
	case ClientCompletionComplete, ClientCompletionDownloading, ClientCompletionProcessing, ClientCompletionUnknown:
	default:
		return fmt.Errorf("%w: client completion state is invalid", ErrInvalidObservation)
	}
	if discovery.ClientCompletion.ObservedAt.IsZero() {
		return fmt.Errorf("%w: client completion observation is incomplete", ErrInvalidObservation)
	}
	if discovery.ClientCompletion.ConnectionID != "" && !discovery.ClientCompletion.ConnectionID.Valid() {
		return fmt.Errorf("%w: client completion has an invalid connection", ErrInvalidObservation)
	}
	if discovery.ClientCompletion.Known && len(discovery.ClientCompletion.Items) == 0 && !discovery.ClientCompletion.ConnectionID.Valid() {
		return fmt.Errorf("%w: known client completion requires item evidence", ErrInvalidObservation)
	}
	if discovery.ClientCompletion.UnknownItem && discovery.ClientCompletion.State != ClientCompletionUnknown {
		return fmt.Errorf("%w: unknown client item requires unknown aggregate state", ErrInvalidObservation)
	}
	itemConnections := make(map[domain.ConfigID]struct{}, len(discovery.ClientCompletion.Items))
	for index, item := range discovery.ClientCompletion.Items {
		if !item.ConnectionID.Valid() {
			return fmt.Errorf("%w: client completion item %d has an invalid connection", ErrInvalidObservation, index)
		}
		if item.ClientItemID == "" && item.Hash == "" {
			return fmt.Errorf("%w: client completion item %d has no identity", ErrInvalidObservation, index)
		}
		switch item.State {
		case ClientCompletionComplete, ClientCompletionDownloading, ClientCompletionProcessing, ClientCompletionUnknown:
		default:
			return fmt.Errorf("%w: client completion item %d has an invalid state", ErrInvalidObservation, index)
		}
		itemConnections[item.ConnectionID] = struct{}{}
	}
	if len(itemConnections) > 1 && discovery.ClientCompletion.ConnectionID != "" {
		return fmt.Errorf("%w: aggregate client completion cannot select one of multiple connections", ErrInvalidObservation)
	}
	for index, coverage := range discovery.ClientCoverage {
		if err := coverage.Validate(); err != nil {
			return fmt.Errorf("%w: client coverage %d: %v", ErrInvalidObservation, index, err)
		}
		if coverage.ConnectionID == "" || !coverage.ConnectionID.Valid() {
			return fmt.Errorf("%w: client coverage %d has invalid connection", ErrInvalidObservation, index)
		}
	}
	seen := make(map[string]struct{}, len(discovery.Files))
	for index, entry := range discovery.Files {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("%w: file %d: %v", ErrInvalidObservation, index, err)
		}
		if entry.RootID != discovery.RootID {
			return fmt.Errorf("%w: file %d has mismatched root", ErrInvalidObservation, index)
		}
		if _, exists := seen[entry.RelativePath]; exists {
			return fmt.Errorf("%w: duplicate file %q", ErrInvalidObservation, entry.RelativePath)
		}
		seen[entry.RelativePath] = struct{}{}
	}
	for index, provenance := range discovery.Provenance {
		if err := provenance.Validate(); err != nil {
			return fmt.Errorf("%w: provenance %d: %v", ErrInvalidObservation, index, err)
		}
	}
	for index, evidence := range discovery.UnsupportedChildren {
		if err := domain.ValidateRelativePath(evidence.RelativePath); err != nil {
			return fmt.Errorf("%w: unsupported child %d path: %v", ErrInvalidObservation, index, err)
		}
		if strings.TrimSpace(evidence.Reason) == "" {
			return fmt.Errorf("%w: unsupported child %d reason is required", ErrInvalidObservation, index)
		}
	}
	return nil
}

func manifestRevision(entries []domain.FileManifestEntry) string {
	copyEntries := cloneEntries(entries)
	sort.Slice(copyEntries, func(left, right int) bool { return copyEntries[left].RelativePath < copyEntries[right].RelativePath })
	hash := sha256.New()
	for _, entry := range copyEntries {
		fmt.Fprintf(hash, "%s\x00%s\x00%d\x00%s\x00%s\x00%s\n", entry.RelativePath, entry.Type, entry.Size, entry.Digest, entry.FileIdentity, entry.Role)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func fileFingerprint(entry domain.FileManifestEntry) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%s", entry.RelativePath, entry.Size, entry.Digest, entry.FileIdentity, entry.Type)))
	return hex.EncodeToString(hash[:])
}

func unsupportedReasonCode(relativePath, reason string) string {
	evidence, err := json.Marshal(ports.UnsupportedChildEvidence{RelativePath: relativePath, Reason: reason})
	if err != nil {
		return "unsupported_child:{}"
	}
	return "unsupported_child:" + string(evidence)
}

func pathWithinGroup(value, group string) bool {
	return value == group || strings.HasPrefix(value, group+"/") || group == path.Dir(value)
}

func unsupportedChildren(reasons []string) []ports.UnsupportedChildEvidence {
	result := make([]ports.UnsupportedChildEvidence, 0)
	seen := make(map[string]struct{})
	for _, reason := range reasons {
		evidence, ok := ports.ParseUnsupportedChildReasonCode(reason)
		if !ok {
			continue
		}
		key := evidence.RelativePath + "\x00" + evidence.Reason
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, evidence)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].RelativePath < result[right].RelativePath })
	return result
}

func unsupportedChildrenForGroup(reasons []string, group string) []ports.UnsupportedChildEvidence {
	all := unsupportedChildren(reasons)
	result := make([]ports.UnsupportedChildEvidence, 0, len(all))
	for _, evidence := range all {
		if pathWithinGroup(evidence.RelativePath, group) {
			result = append(result, evidence)
		}
	}
	return result
}

func appendReview(values []ReviewReason, value ReviewReason) []ReviewReason {
	if containsReview(values, value) {
		return values
	}
	return append(values, value)
}

func appendReviewReasons(values []ReviewReason, more []ReviewReason) []ReviewReason {
	for _, value := range more {
		values = appendReview(values, value)
	}
	return values
}

func containsReview(values []ReviewReason, value ReviewReason) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func uniqueReasons(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = appendUnique(result, value)
	}
	return result
}

func containsString(values []string, value string) bool {
	for _, existing := range values {
		if existing == value {
			return true
		}
	}
	return false
}

func intPointer(value int) *int { return &value }

func timePointer(value time.Time) *time.Time {
	return &value
}

func cloneEntry(entry domain.FileManifestEntry) domain.FileManifestEntry {
	entry.Children = cloneEntries(entry.Children)
	return entry
}

func cloneEntries(entries []domain.FileManifestEntry) []domain.FileManifestEntry {
	if len(entries) == 0 {
		return nil
	}
	result := make([]domain.FileManifestEntry, len(entries))
	for index, entry := range entries {
		result[index] = cloneEntry(entry)
	}
	return result
}

func cloneDirectoryObservation(observation DirectoryObservation) DirectoryObservation {
	observation.Entries = cloneEntries(observation.Entries)
	observation.Coverage = cloneCoverage(observation.Coverage)
	observation.UnsupportedChildren = append([]ports.UnsupportedChildEvidence(nil), observation.UnsupportedChildren...)
	return observation
}

func cloneDiscovery(discovery Discovery) Discovery {
	discovery.Files = cloneEntries(discovery.Files)
	discovery.Videos = append([]MediaAssociation(nil), discovery.Videos...)
	for index := range discovery.Videos {
		discovery.Videos[index].EpisodeNumbers = append([]int(nil), discovery.Videos[index].EpisodeNumbers...)
		discovery.Videos[index].SeasonNumber = cloneIntPointer(discovery.Videos[index].SeasonNumber)
		discovery.Videos[index].AbsoluteNumber = cloneIntPointer(discovery.Videos[index].AbsoluteNumber)
	}
	discovery.Subtitles = append([]SubtitleAssociation(nil), discovery.Subtitles...)
	for index := range discovery.Subtitles {
		discovery.Subtitles[index].VideoPaths = append([]string(nil), discovery.Subtitles[index].VideoPaths...)
	}
	discovery.Companions = append([]Companion(nil), discovery.Companions...)
	for index := range discovery.Companions {
		discovery.Companions[index].VideoPaths = append([]string(nil), discovery.Companions[index].VideoPaths...)
	}
	discovery.Provenance = append([]domain.Provenance(nil), discovery.Provenance...)
	for index := range discovery.Provenance {
		if discovery.Provenance[index].CompletedAt != nil {
			value := *discovery.Provenance[index].CompletedAt
			discovery.Provenance[index].CompletedAt = &value
		}
		if discovery.Provenance[index].SourcePath != nil {
			value := *discovery.Provenance[index].SourcePath
			discovery.Provenance[index].SourcePath = &value
		}
	}
	discovery.ClientCompletion.ClientItemIDs = append([]string(nil), discovery.ClientCompletion.ClientItemIDs...)
	discovery.ClientCompletion.Items = cloneClientItemObservations(discovery.ClientCompletion.Items)
	discovery.ClientCompletion.CompletedAt = cloneTimePointer(discovery.ClientCompletion.CompletedAt)
	discovery.Stability.Files = append([]FileStability(nil), discovery.Stability.Files...)
	for index := range discovery.Stability.Files {
		if discovery.Stability.Files[index].PreviousObservedAt != nil {
			value := *discovery.Stability.Files[index].PreviousObservedAt
			discovery.Stability.Files[index].PreviousObservedAt = &value
		}
	}
	discovery.ReviewReasons = append([]ReviewReason(nil), discovery.ReviewReasons...)
	discovery.Coverage = cloneCoverage(discovery.Coverage)
	discovery.ClientCoverage = cloneCoverages(discovery.ClientCoverage)
	discovery.UnsupportedChildren = append([]ports.UnsupportedChildEvidence(nil), discovery.UnsupportedChildren...)
	return discovery
}

func cloneCoverages(coverages []domain.Coverage) []domain.Coverage {
	if len(coverages) == 0 {
		return nil
	}
	result := make([]domain.Coverage, len(coverages))
	for index, coverage := range coverages {
		result[index] = cloneCoverage(coverage)
	}
	return result
}

func cloneCoverage(coverage domain.Coverage) domain.Coverage {
	coverage.ReasonCodes = append([]string(nil), coverage.ReasonCodes...)
	coverage.StartedAt = cloneTimePointer(coverage.StartedAt)
	coverage.CompletedAt = cloneTimePointer(coverage.CompletedAt)
	return coverage
}

func cloneClientItemObservation(item ClientItemObservation) ClientItemObservation {
	item.CompletedAt = cloneTimePointer(item.CompletedAt)
	return item
}

func cloneClientItemObservations(items []ClientItemObservation) []ClientItemObservation {
	if len(items) == 0 {
		return nil
	}
	result := make([]ClientItemObservation, len(items))
	for index, item := range items {
		result[index] = cloneClientItemObservation(item)
	}
	return result
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneDiscoveries(discoveries []Discovery) []Discovery {
	if len(discoveries) == 0 {
		return nil
	}
	result := make([]Discovery, len(discoveries))
	for index, discovery := range discoveries {
		result[index] = cloneDiscovery(discovery)
	}
	return result
}

func cloneDownloadItem(item ports.DownloadItem) ports.DownloadItem {
	item.Tags = append([]string(nil), item.Tags...)
	item.Payload = cloneEntries(item.Payload)
	if item.CompletedAt != nil {
		value := *item.CompletedAt
		item.CompletedAt = &value
	}
	if item.Descriptor != nil {
		value := *item.Descriptor
		if value.CapturedAt != nil {
			captured := *value.CapturedAt
			value.CapturedAt = &captured
		}
		item.Descriptor = &value
	}
	return item
}
