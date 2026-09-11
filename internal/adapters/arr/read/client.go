// Package read implements the read-only Radarr and Sonarr API boundary.
//
// Preview and reprocessing calls are kept separate from native import
// execution. This package never calls the Arr command endpoint.
package read

import (
	"bytes"
	"context"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

const (
	defaultPageSize      = 100
	defaultMaxPages      = 100
	defaultMaxRecords    = 10_000
	defaultMaxFiles      = 50_000
	defaultMaxResponse   = 16 << 20
	maxCursorBytes       = 64 << 10
	maxCursorSeenIDs     = 2_048
	maxHistoryReasonCode = 256
)

const (
	apiMovies       = "/api/v3/movie"
	apiMovieFiles   = "/api/v3/moviefile"
	apiMovieLookup  = "/api/v3/movie/lookup"
	apiSeries       = "/api/v3/series"
	apiEpisodes     = "/api/v3/episode"
	apiSeriesLookup = "/api/v3/series/lookup"
	apiRootFolders  = "/api/v3/rootfolder"
	apiQuality      = "/api/v3/qualityprofile"
	apiHistory      = "/api/v3/history"
	apiManualImport = "/api/v3/manualimport"
)

// Config contains one Arr instance's non-secret endpoint and a supplied API
// key. RootPaths are private adapter configuration; they convert exact
// root-relative targets into the absolute folder query required by Arr.
type Config struct {
	ConnectionID domain.ConfigID
	Kind         domain.ConnectionKind
	Endpoint     string
	APIKey       string
	HTTPClient   *http.Client
	RootPaths    map[domain.ConfigID]string
	Mappings     []domain.PathMapping

	MaxPageSize     int
	MaxPages        int
	MaxRecords      int
	MaxFiles        int
	MaxResponseSize int64
}

// Client is a read-only Arr API client. It implements no registration or
// import execution methods.
type Client struct {
	config    Config
	endpoint  *url.URL
	http      *http.Client
	cursorKey []byte
}

var _ ports.MediaManagerReadPort = (*Client)(nil)
var _ ports.CapabilityPort = (*Client)(nil)

// New validates configuration without contacting the upstream instance.
func New(config Config) (*Client, error) {
	if !config.ConnectionID.Valid() {
		return nil, errors.New("Arr connection id is invalid")
	}
	switch config.Kind {
	case domain.ConnectionRadarr, domain.ConnectionSonarr:
	default:
		return nil, errors.New("Arr read client requires radarr or sonarr kind")
	}
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if config.MaxPageSize <= 0 {
		config.MaxPageSize = defaultPageSize
	}
	if config.MaxPages <= 0 {
		config.MaxPages = defaultMaxPages
	}
	if config.MaxRecords <= 0 {
		config.MaxRecords = defaultMaxRecords
	}
	if config.MaxFiles <= 0 {
		config.MaxFiles = defaultMaxFiles
	}
	if config.MaxPageSize > config.MaxRecords {
		config.MaxPageSize = config.MaxRecords
	}
	if config.MaxResponseSize <= 0 {
		config.MaxResponseSize = defaultMaxResponse
	}
	if err := validateMappings(config.ConnectionID, config.Mappings); err != nil {
		return nil, err
	}
	for rootID, rootPath := range config.RootPaths {
		if !rootID.Valid() || strings.TrimSpace(rootPath) == "" || !absoluteRemotePath(normalizeRemotePath(rootPath)) {
			return nil, errors.New("Arr root path is invalid")
		}
	}

	baseClient := http.DefaultClient
	if config.HTTPClient != nil {
		baseClient = config.HTTPClient
	}
	copyClient := *baseClient
	client := &copyClient
	if client.Timeout == 0 {
		client.Timeout = 30 * time.Second
	}
	// Arr read and preview requests must never follow redirects. A same-origin
	// 307/308 preserves POST semantics and can otherwise turn a scoped
	// manual-import preview into an unapproved command request. Returning the
	// response lets the caller normalize the 3xx status without issuing the
	// redirected request, for both GET and POST.
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	cursorKey := make([]byte, 32)
	if _, err := cryptorand.Read(cursorKey); err != nil {
		return nil, errors.New("Arr cursor key setup failed")
	}
	config.RootPaths = cloneRootPaths(config.RootPaths)
	config.Mappings = append([]domain.PathMapping(nil), config.Mappings...)
	return &Client{config: config, endpoint: endpoint, http: client, cursorKey: cursorKey}, nil
}

// NewClient is an explicit constructor alias.
func NewClient(config Config) (*Client, error) { return New(config) }

// ConnectionID identifies the upstream scope for every returned record.
func (client *Client) ConnectionID() domain.ConfigID { return client.config.ConnectionID }

// ScopedIdentity keeps Arr numeric IDs distinct across configured instances.
func (client *Client) ScopedIdentity(externalID string) string {
	if externalID == "" {
		return ""
	}
	return client.config.ConnectionID.String() + ":" + externalID
}

// Capabilities describes the narrow read and preview surfaces. Native command
// execution remains unknown until X-05 proves its no-overwrite boundary.
func (client *Client) Capabilities(ctx context.Context, connectionID domain.ConfigID) ([]domain.Capability, error) {
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return []domain.Capability{
		{Name: "arr.inventory", State: domain.CapabilitySupported, Version: "v3", Evidence: []string{"typed movie/series reads"}, ObservedAt: now},
		{Name: "arr.lookup", State: domain.CapabilitySupported, Version: "v3", Evidence: []string{"typed provider lookup"}, ObservedAt: now},
		{Name: "arr.manual-import-preview", State: domain.CapabilitySupported, Version: "v3", Evidence: []string{"GET manualimport"}, ObservedAt: now},
		{Name: "arr.manual-import-subtitle-attributes", State: domain.CapabilityUnknown, Version: "v3", Reason: "Arr manual-import resources do not consistently expose forced or SDH evidence", Evidence: []string{"subtitle_forced_unknown", "subtitle_hearing_impaired_unknown"}, ObservedAt: now},
		{Name: "arr.manual-import-execution", State: domain.CapabilityUnknown, Version: "v3", Reason: "native no-overwrite race remains an open compatibility gate", Evidence: []string{"CAP-ARR-NO-OVERWRITE"}, ObservedAt: now},
	}, nil
}

// List reads one bounded catalog page. Arr's movie and series endpoints return
// full arrays, so pagination is performed against one locally observed,
// authenticated snapshot. The adapter never sends page/pageSize to these
// endpoints: doing so would make an Arr full-array response look like a
// complete page and lose records after the first local page.
func (client *Client) List(ctx context.Context, connectionID domain.ConfigID, cursor string, requestedLimit int) (ports.Page[ports.MediaRecord], error) {
	var result ports.Page[ports.MediaRecord]
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	limit, err := client.pageLimit(requestedLimit)
	if err != nil {
		return result, err
	}
	state, err := client.decodeCursor(cursor)
	if err != nil {
		return result, err
	}
	if state.SourceID == "" {
		state.SourceID, err = domain.NewRuntimeID()
		if err != nil {
			return result, errors.New("Arr inventory source identity unavailable")
		}
		state.StartedAt = time.Now().UTC()
		state.PageSize = limit
		state.Collection = "inventory"
	} else if state.Collection != "inventory" {
		return result, invalidInput("arr.inventory.cursor")
	} else if requestedLimit > 0 && state.PageSize != limit {
		return result, invalidInput("arr.inventory.cursor")
	}
	if state.Page >= client.config.MaxPages || state.ObservedCount >= client.config.MaxRecords {
		return result, invalidInput("arr.inventory.cursor")
	}
	body, err := client.get(ctx, "arr.inventory.list", catalogPath(client.config.Kind), nil)
	if err != nil {
		return result, err
	}
	allRecords, upstreamMore, total, err := decodeCollection(body, limit)
	if err != nil {
		return result, malformed("arr.inventory.list")
	}
	fullArray := isJSONArray(body)
	if fullArray {
		// For an Arr movie/series endpoint the array is the complete upstream
		// response. The helper's len>=limit heuristic is for paginated object
		// responses and would manufacture an empty continuation at an exact
		// local boundary.
		upstreamMore = false
	}
	if state.SnapshotRevision == "" {
		state.SnapshotRevision = digest(body)
	} else if state.SnapshotRevision != digest(body) {
		// A continuation can still return useful observations from the new
		// response, but the result can no longer establish complete absence
		// against the original snapshot.
		addReason(&state.Reasons, "catalog_snapshot_changed")
	}
	if total > len(allRecords) {
		// An object response with an explicit total is paginated upstream. Keep
		// its metadata in the cursor, but still enforce local bounds below.
		upstreamMore = true
	}
	start := state.Offset
	if start < 0 {
		return result, invalidInput("arr.inventory.cursor")
	}
	if start > len(allRecords) {
		start = len(allRecords)
	}
	remaining := client.config.MaxRecords - state.ObservedCount
	if remaining <= 0 {
		return result, invalidInput("arr.inventory.cursor")
	}
	pageCount := minInt(limit, remaining)
	available := len(allRecords) - start
	if pageCount > available {
		pageCount = available
	}
	rawRecords := allRecords[start : start+pageCount]
	hasMore := upstreamMore
	if start+pageCount < len(allRecords) {
		hasMore = true
	}
	if state.ObservedCount+pageCount >= client.config.MaxRecords {
		if start+pageCount < len(allRecords) || total > client.config.MaxRecords || upstreamMore {
			addReason(&state.Reasons, "catalog_record_limit")
			hasMore = false
		}
	}
	if pageCount < limit && start+pageCount >= len(allRecords) {
		// A full-array response ending before the requested page size is a
		// natural terminal page. Keep upstream object metadata in consideration
		// for the rare paginated response.
		hasMore = upstreamMore && total > len(allRecords)
	}
	seen := make(map[string]struct{}, len(state.SeenIDs))
	for _, id := range state.SeenIDs {
		seen[id] = struct{}{}
	}
	pageSeen := make(map[string]struct{}, len(rawRecords))
	records := make([]ports.MediaRecord, 0, len(rawRecords))
	for index, raw := range rawRecords {
		if err := ctx.Err(); err != nil {
			return ports.Page[ports.MediaRecord]{}, err
		}
		record, externalID, reasons, decodeErr := client.decodeRecord(ctx, raw)
		if decodeErr != nil {
			return ports.Page[ports.MediaRecord]{}, malformed(fmt.Sprintf("arr.inventory.record.%d", index))
		}
		for _, reason := range reasons {
			addReason(&state.Reasons, reason)
		}
		if externalID == "" {
			addReason(&state.Reasons, "record_missing_id")
			continue
		}
		if _, exists := seen[externalID]; exists {
			addReason(&state.Reasons, "catalog_overlap")
			continue
		}
		if _, exists := pageSeen[externalID]; exists {
			addReason(&state.Reasons, "catalog_duplicate")
			continue
		}
		pageSeen[externalID] = struct{}{}
		seen[externalID] = struct{}{}
		if len(state.SeenIDs) < maxCursorSeenIDs {
			state.SeenIDs = append(state.SeenIDs, externalID)
		}
		records = append(records, record)
	}
	state.Page++
	state.Offset = start + len(rawRecords)
	state.ObservedCount += len(records)
	if len(rawRecords) == 0 {
		hasMore = false
	}
	if hasMore && state.Page >= client.config.MaxPages {
		hasMore = false
		addReason(&state.Reasons, "catalog_page_limit")
	}
	if hasMore && state.ObservedCount >= client.config.MaxRecords {
		hasMore = false
		addReason(&state.Reasons, "catalog_record_limit")
	}
	now := time.Now().UTC()
	coverage := domain.Coverage{
		SourceID: state.SourceID, ConnectionID: connectionID,
		Completeness: domain.CompletenessPartial, ReasonCodes: append([]string(nil), state.Reasons...),
		ObservedCount: int64(state.ObservedCount), SnapshotRevision: state.SnapshotRevision,
		StartedAt: timePtr(state.StartedAt), ObservedAt: now,
	}
	if !hasMore {
		coverage.CompletedAt = &now
		if len(state.Reasons) == 0 {
			coverage.Completeness = domain.CompletenessComplete
		}
	} else {
		addReason(&coverage.ReasonCodes, "pagination_continues")
	}
	result.Items = records
	result.Coverage = coverage
	if hasMore {
		result.NextCursor, err = client.encodeCursor(state)
		if err != nil {
			return ports.Page[ports.MediaRecord]{}, err
		}
	}
	return result, nil
}

// Lookup searches only the selected Arr instance. It returns suggestions and
// never calls a release-search or mutation endpoint.
func (client *Client) Lookup(ctx context.Context, connectionID domain.ConfigID, providerID string, kind domain.MediaKind) ([]ports.MediaRecord, error) {
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	providerID = strings.TrimSpace(providerID)
	if providerID == "" || !validLookupKind(client.config.Kind, kind) {
		return nil, invalidInput("arr.lookup")
	}
	prefix := "tvdb"
	if client.config.Kind == domain.ConnectionRadarr {
		prefix = "tmdb"
	}
	body, err := client.get(ctx, "arr.lookup", lookupPath(client.config.Kind), url.Values{"term": []string{prefix + ":" + providerID}})
	if err != nil {
		return nil, err
	}
	rawRecords, _, _, err := decodeCollection(body, client.config.MaxPageSize)
	if err != nil {
		return nil, malformed("arr.lookup")
	}
	if len(rawRecords) > client.config.MaxRecords {
		rawRecords = rawRecords[:client.config.MaxRecords]
	}
	result := make([]ports.MediaRecord, 0, len(rawRecords))
	for index, raw := range rawRecords {
		record, _, _, decodeErr := client.decodeRecord(ctx, raw)
		if decodeErr != nil {
			return nil, malformed(fmt.Sprintf("arr.lookup.record.%d", index))
		}
		record.Kind = kind
		result = append(result, record)
	}
	return result, nil
}

// Options reads current root-folder and quality-profile options. Empty
// series-specific option lists remain empty when the upstream does not expose a
// typed endpoint; the client never invents values.
func (client *Client) Options(ctx context.Context, connectionID domain.ConfigID) (ports.ManagerOptions, error) {
	var result ports.ManagerOptions
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	rootsBody, err := client.get(ctx, "arr.options.root_folders", apiRootFolders, nil)
	if err != nil {
		return result, err
	}
	var roots []rootFolderDTO
	if err := decodeJSON(rootsBody, &roots); err != nil {
		return result, malformed("arr.options.root_folders")
	}
	for _, root := range roots {
		if strings.TrimSpace(root.Path) != "" {
			result.RootFolders = append(result.RootFolders, root.Path)
		}
	}
	qualityBody, err := client.get(ctx, "arr.options.quality_profiles", apiQuality, nil)
	if err != nil {
		return result, err
	}
	var profiles []qualityProfileDTO
	if err := decodeJSON(qualityBody, &profiles); err != nil {
		return result, malformed("arr.options.quality_profiles")
	}
	if len(roots) > client.config.MaxRecords || len(profiles) > client.config.MaxRecords {
		return result, malformed("arr.options.bounds")
	}
	for _, profile := range profiles {
		id := scalarString(profile.ID)
		if id == "" || strings.TrimSpace(profile.Name) == "" {
			return result, malformed("arr.options.quality_profiles")
		}
		result.QualityProfiles = append(result.QualityProfiles, ports.QualityProfile{ID: id, Name: profile.Name})
	}
	result.ObservedAt = time.Now().UTC()
	return result, nil
}

// HistoryEntry is bounded, typed history evidence. Arbitrary upstream JSON is
// intentionally not exposed to domain callers.
type HistoryEntry struct {
	ID          string
	EventType   string
	Date        time.Time
	SourceTitle string
	MovieID     string
	SeriesID    string
	EpisodeID   string
	DownloadID  string
	Quality     string
	Successful  bool
	SourcePath  string
	Destination string
}

// History reads the complete bounded history collection page by page.
func (client *Client) History(ctx context.Context, connectionID domain.ConfigID, cursor string, requestedLimit int) (ports.Page[HistoryEntry], error) {
	var result ports.Page[HistoryEntry]
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	limit, err := client.pageLimit(requestedLimit)
	if err != nil {
		return result, err
	}
	state, err := client.decodeCursor(cursor)
	if err != nil {
		return result, err
	}
	if state.SourceID == "" {
		state.SourceID, err = domain.NewRuntimeID()
		if err != nil {
			return result, errors.New("Arr history source identity unavailable")
		}
		state.StartedAt = time.Now().UTC()
		state.PageSize = limit
		state.Collection = "history"
	} else if state.Collection != "history" {
		return result, invalidInput("arr.history.cursor")
	} else if requestedLimit > 0 && state.PageSize != limit {
		return result, invalidInput("arr.history.cursor")
	}
	body, err := client.get(ctx, "arr.history.list", apiHistory, url.Values{"page": []string{strconv.Itoa(state.Page + 1)}, "pageSize": []string{strconv.Itoa(limit)}})
	if err != nil {
		return result, err
	}
	rawRecords, hasMore, total, err := decodeCollection(body, limit)
	if err != nil {
		return result, malformed("arr.history.list")
	}
	explicitTotal := collectionHasTotal(body)
	if total > client.config.MaxRecords {
		// The configured record bound makes the full upstream collection
		// unobservable. Retain the evidence on every page, but allow callers to
		// consume the remaining bounded pages before terminating.
		addReason(&state.Reasons, "history_record_limit")
	}
	if len(rawRecords) > limit {
		rawRecords = rawRecords[:limit]
		hasMore = true
		addReason(&state.Reasons, "history_page_exceeded_limit")
	}
	remaining := client.config.MaxRecords - state.ObservedCount
	if remaining <= 0 {
		return result, invalidInput("arr.history.cursor")
	}
	if len(rawRecords) > remaining {
		rawRecords = rawRecords[:remaining]
		addReason(&state.Reasons, "history_record_limit")
		hasMore = false
	}
	if state.SnapshotRevision == "" {
		state.SnapshotRevision = digest(body)
	}
	entries := make([]HistoryEntry, 0, len(rawRecords))
	for index, raw := range rawRecords {
		entry, decodeErr := decodeHistoryEntry(raw)
		if decodeErr != nil {
			return result, malformed(fmt.Sprintf("arr.history.record.%d", index))
		}
		entries = append(entries, entry)
	}
	state.Page++
	state.ObservedCount += len(entries)
	if explicitTotal && (total < state.ObservedCount || (!hasMore && total != state.ObservedCount)) {
		// A total that disagrees with the records observed across this cursor is
		// inconsistent evidence. Keep any upstream continuation, but never let a
		// false total turn an incomplete history into confirmed absence.
		addReason(&state.Reasons, "history_total_inconsistent")
	}
	if len(rawRecords) == 0 {
		hasMore = false
	}
	if hasMore && state.Page >= client.config.MaxPages {
		hasMore = false
		addReason(&state.Reasons, "history_page_limit")
	}
	if hasMore && state.ObservedCount >= client.config.MaxRecords {
		hasMore = false
		addReason(&state.Reasons, "history_record_limit")
	}
	now := time.Now().UTC()
	result.Items = entries
	result.Coverage = domain.Coverage{
		SourceID: state.SourceID, ConnectionID: connectionID,
		Completeness: domain.CompletenessPartial, ReasonCodes: append([]string(nil), state.Reasons...),
		ObservedCount: int64(state.ObservedCount), SnapshotRevision: state.SnapshotRevision,
		StartedAt: timePtr(state.StartedAt), ObservedAt: now,
	}
	if !hasMore {
		result.Coverage.CompletedAt = &now
		if len(state.Reasons) == 0 {
			result.Coverage.Completeness = domain.CompletenessComplete
		}
	} else {
		addReason(&result.Coverage.ReasonCodes, "pagination_continues")
		result.NextCursor, err = client.encodeCursor(state)
		if err != nil {
			return ports.Page[HistoryEntry]{}, err
		}
	}
	return result, nil
}

// PreviewImport calls native GET manualimport and maps only the exact
// requested files. Rejections are retained as typed evidence. It never calls
// the Arr command endpoint.
func (client *Client) PreviewImport(ctx context.Context, connectionID domain.ConfigID, request ports.ImportPreviewRequest) (ports.ImportPreview, error) {
	return client.previewImport(ctx, connectionID, request, "")
}

// NativePreviewRequest supplies the optional upstream download identity that
// makes an Arr manual-import query source-scoped. The common port predates
// this product-specific value, so callers with client provenance can use
// PreviewImportWithDownloadID. Without it PreviewImport deliberately omits
// movieId/seriesId: Arr treats an ID without downloadId as a library-file
// query, which could preview a destination file instead of downloaded input.
type NativePreviewRequest struct {
	Request    ports.ImportPreviewRequest
	DownloadID string
}

func (client *Client) PreviewImportWithDownloadID(ctx context.Context, connectionID domain.ConfigID, request NativePreviewRequest) (ports.ImportPreview, error) {
	if strings.TrimSpace(request.DownloadID) == "" {
		return ports.ImportPreview{}, invalidInput("arr.manual_import.preview.download_id")
	}
	return client.previewImport(ctx, connectionID, request.Request, strings.TrimSpace(request.DownloadID))
}

func (client *Client) previewImport(ctx context.Context, connectionID domain.ConfigID, request ports.ImportPreviewRequest, downloadID string) (ports.ImportPreview, error) {
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return ports.ImportPreview{}, err
	}
	if err := validateImportRequest(request, client.config.MaxFiles); err != nil {
		return ports.ImportPreview{}, err
	}
	groups := groupImportFiles(request.Files)
	preview := ports.ImportPreview{Files: make([]ports.ImportFile, 0, len(request.Files)), Rejections: make([]ports.ImportRejection, 0)}
	var rawEvidence [][]byte
	for _, group := range groups {
		query, err := client.previewQuery(request.RegisteredExternalID, downloadID, group)
		if err != nil {
			return ports.ImportPreview{}, err
		}
		body, err := client.get(ctx, "arr.manual_import.preview", apiManualImport, query)
		if err != nil {
			return ports.ImportPreview{}, err
		}
		rawEvidence = append(rawEvidence, body)
		expectedDownloads := make(map[string]string, len(group))
		if strings.TrimSpace(downloadID) != "" {
			for _, file := range group {
				expectedDownloads[sourceKey(file.Source)] = strings.TrimSpace(downloadID)
			}
		}
		accepted, rejected, err := client.mapPreviewResponseFor(body, group, query.Get("folder"), request.RegisteredExternalID, nil, expectedDownloads)
		if err != nil {
			return ports.ImportPreview{}, err
		}
		preview.Files = append(preview.Files, accepted...)
		preview.Rejections = append(preview.Rejections, rejected...)
	}
	preview.Revision = client.previewRevision(request, downloadID, rawEvidence)
	preview.ObservedAt = time.Now().UTC()
	return preview, nil
}

// ReprocessFile is product-neutral exact input used to build a native
// reprocessing request. Reprocessing revises preview candidates; it does not
// execute import.
type ReprocessFile struct {
	Source           domain.FileTarget
	MovieOrEpisodeID string
	EpisodeIDs       []string
	DownloadID       string
	Subtitle         bool
	Language         string
	Forced           bool
	HearingImpaired  bool
	// The fields below mirror the pinned Arr manual-import resource. They are
	// retained from the reviewed candidate so a reprocessing preview cannot
	// silently discard quality, language, season or native scoring context.
	SeasonNumber      *int
	Episodes          []ArrEpisodeReference
	Quality           json.RawMessage
	Languages         []ArrLanguage
	ReleaseGroup      string
	CustomFormats     []json.RawMessage
	CustomFormatScore int
	IndexerFlags      int
	ReleaseType       string
}

// ReprocessPreviewRequest contains exact reviewed preview candidates.
type ReprocessPreviewRequest struct {
	RegisteredExternalID string
	Files                []ReprocessFile
	Transfer             string
}

// RadarrManualImportReprocess is the typed Radarr POST manualimport DTO.
type RadarrManualImportReprocess struct {
	Path              string            `json:"path"`
	MovieID           int               `json:"movieId"`
	Quality           json.RawMessage   `json:"quality"`
	Languages         []ArrLanguage     `json:"languages"`
	ReleaseGroup      string            `json:"releaseGroup"`
	DownloadID        string            `json:"downloadId"`
	CustomFormats     []json.RawMessage `json:"customFormats"`
	CustomFormatScore int               `json:"customFormatScore"`
	IndexerFlags      int               `json:"indexerFlags"`
}

// SonarrManualImportReprocess is the typed Sonarr POST manualimport DTO.
type SonarrManualImportReprocess struct {
	Path              string            `json:"path"`
	SeriesID          int               `json:"seriesId"`
	SeasonNumber      *int              `json:"seasonNumber"`
	EpisodeIDs        []int             `json:"episodeIds"`
	Quality           json.RawMessage   `json:"quality"`
	Languages         []ArrLanguage     `json:"languages"`
	ReleaseGroup      string            `json:"releaseGroup"`
	DownloadID        string            `json:"downloadId"`
	CustomFormats     []json.RawMessage `json:"customFormats"`
	CustomFormatScore int               `json:"customFormatScore"`
	IndexerFlags      int               `json:"indexerFlags"`
	ReleaseType       string            `json:"releaseType"`
}

// ArrLanguage is the bounded native language object used by Arr's manual
// import request. A string language code is never sent as an untyped native
// field; callers either preserve this object from a preview or use one of the
// known mappings in languagePayload.
type ArrLanguage struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// RadarrManualImportResource is the typed Radarr preview response.
type RadarrManualImportResource struct {
	ID                json.RawMessage      `json:"id"`
	Path              string               `json:"path"`
	RelativePath      string               `json:"relativePath"`
	FolderName        string               `json:"folderName"`
	Name              string               `json:"name"`
	Size              int64                `json:"size"`
	Movie             *ArrMovieReference   `json:"movie"`
	MovieFileID       json.RawMessage      `json:"movieFileId"`
	ReleaseGroup      string               `json:"releaseGroup"`
	Quality           json.RawMessage      `json:"quality"`
	Languages         []json.RawMessage    `json:"languages"`
	DownloadID        string               `json:"downloadId"`
	CustomFormats     []json.RawMessage    `json:"customFormats"`
	CustomFormatScore int                  `json:"customFormatScore"`
	IndexerFlags      int                  `json:"indexerFlags"`
	Forced            *bool                `json:"forced"`
	IsForced          *bool                `json:"isForced"`
	HearingImpaired   *bool                `json:"hearingImpaired"`
	IsHearingImpaired *bool                `json:"isHearingImpaired"`
	Rejections        []ArrImportRejection `json:"rejections"`
}

// SonarrManualImportResource is the typed Sonarr preview response.
type SonarrManualImportResource struct {
	ID                json.RawMessage       `json:"id"`
	Path              string                `json:"path"`
	RelativePath      string                `json:"relativePath"`
	FolderName        string                `json:"folderName"`
	Name              string                `json:"name"`
	Size              int64                 `json:"size"`
	Series            *ArrSeriesReference   `json:"series"`
	SeasonNumber      *int                  `json:"seasonNumber"`
	Episodes          []ArrEpisodeReference `json:"episodes"`
	EpisodeFileID     json.RawMessage       `json:"episodeFileId"`
	ReleaseGroup      string                `json:"releaseGroup"`
	Quality           json.RawMessage       `json:"quality"`
	Languages         []json.RawMessage     `json:"languages"`
	DownloadID        string                `json:"downloadId"`
	ReleaseType       string                `json:"releaseType"`
	CustomFormats     []json.RawMessage     `json:"customFormats"`
	CustomFormatScore int                   `json:"customFormatScore"`
	IndexerFlags      int                   `json:"indexerFlags"`
	Forced            *bool                 `json:"forced"`
	IsForced          *bool                 `json:"isForced"`
	HearingImpaired   *bool                 `json:"hearingImpaired"`
	IsHearingImpaired *bool                 `json:"isHearingImpaired"`
	Rejections        []ArrImportRejection  `json:"rejections"`
}

func (resource RadarrManualImportResource) ForcedEvidence() *bool {
	if resource.Forced != nil {
		return resource.Forced
	}
	return resource.IsForced
}

func (resource RadarrManualImportResource) HearingImpairedEvidence() *bool {
	if resource.HearingImpaired != nil {
		return resource.HearingImpaired
	}
	return resource.IsHearingImpaired
}

func (resource SonarrManualImportResource) ForcedEvidence() *bool {
	if resource.Forced != nil {
		return resource.Forced
	}
	return resource.IsForced
}

func (resource SonarrManualImportResource) HearingImpairedEvidence() *bool {
	if resource.HearingImpaired != nil {
		return resource.HearingImpaired
	}
	return resource.IsHearingImpaired
}

// ArrMovieReference and ArrSeriesReference are the identity-bearing nested
// objects returned by the native manual-import resources.
type ArrMovieReference struct {
	ID     json.RawMessage `json:"id"`
	Title  string          `json:"title"`
	TmdbID json.RawMessage `json:"tmdbId"`
	TvdbID json.RawMessage `json:"tvdbId"`
	ImdbID json.RawMessage `json:"imdbId"`
}

type ArrSeriesReference struct {
	ID       json.RawMessage `json:"id"`
	Title    string          `json:"title"`
	TvdbID   json.RawMessage `json:"tvdbId"`
	TvMazeID json.RawMessage `json:"tvMazeId"`
}

type ArrEpisodeReference struct {
	SeriesID                   json.RawMessage `json:"seriesId"`
	ID                         json.RawMessage `json:"id"`
	EpisodeFileID              json.RawMessage `json:"episodeFileId"`
	SeasonNumber               int             `json:"seasonNumber"`
	EpisodeNumber              int             `json:"episodeNumber"`
	AbsoluteEpisodeNumber      int             `json:"absoluteEpisodeNumber"`
	SceneAbsoluteEpisodeNumber int             `json:"sceneAbsoluteEpisodeNumber"`
}

// ArrImportRejection preserves native preview reason/type fields without
// exposing arbitrary upstream JSON.
type ArrImportRejection struct {
	Code   string `json:"type,omitempty"`
	Reason string `json:"message,omitempty"`
}

// UnmarshalJSON accepts both the current Arr type/message names and older
// fixtures that called the same fields code/reason. Both variants remain
// bounded typed evidence.
func (rejection *ArrImportRejection) UnmarshalJSON(data []byte) error {
	var value struct {
		Code    string `json:"code"`
		Type    string `json:"type"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	rejection.Code = firstNonEmpty(value.Code, value.Type)
	rejection.Reason = firstNonEmpty(value.Reason, value.Message)
	return nil
}

// ReprocessPreview POSTs only native manualimport reprocessing. Callers must
// still submit a separate reviewed import action; this method has no command
// payload and cannot execute an import.
func (client *Client) ReprocessPreview(ctx context.Context, connectionID domain.ConfigID, request ReprocessPreviewRequest) (ports.ImportPreview, error) {
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return ports.ImportPreview{}, err
	}
	if err := validateReprocessRequest(request, client.config.MaxFiles); err != nil {
		return ports.ImportPreview{}, err
	}
	switch client.config.Kind {
	case domain.ConnectionRadarr:
		movieID, err := parsePositiveInt(request.RegisteredExternalID)
		if err != nil {
			return ports.ImportPreview{}, invalidInput("arr.manual_import.reprocess.movie_id")
		}
		payload := make([]RadarrManualImportReprocess, 0, len(request.Files))
		for index, file := range request.Files {
			if err := file.Source.Validate(); err != nil {
				return ports.ImportPreview{}, fmt.Errorf("reprocess file %d: %w", index, err)
			}
			if strings.TrimSpace(file.MovieOrEpisodeID) == "" || file.MovieOrEpisodeID != request.RegisteredExternalID {
				return ports.ImportPreview{}, invalidInput("arr.manual_import.reprocess.movie_id")
			}
			languages, err := languagePayload(file)
			if err != nil {
				return ports.ImportPreview{}, err
			}
			dto := RadarrManualImportReprocess{
				Path:              absoluteFilePath(client.config.RootPaths, file.Source),
				MovieID:           movieID,
				Quality:           cloneRawMessage(file.Quality),
				Languages:         languages,
				ReleaseGroup:      strings.TrimSpace(file.ReleaseGroup),
				DownloadID:        strings.TrimSpace(file.DownloadID),
				CustomFormats:     cloneRawMessages(file.CustomFormats),
				CustomFormatScore: file.CustomFormatScore,
				IndexerFlags:      file.IndexerFlags,
			}
			if dto.Path == "" {
				return ports.ImportPreview{}, invalidInput("arr.manual_import.reprocess.root")
			}
			payload = append(payload, dto)
		}
		body, err := client.postJSON(ctx, "arr.manual_import.reprocess.radarr", apiManualImport, payload)
		if err != nil {
			return ports.ImportPreview{}, err
		}
		return client.decodeReprocessedPreview(body, request)
	case domain.ConnectionSonarr:
		seriesID, err := parsePositiveInt(request.RegisteredExternalID)
		if err != nil {
			return ports.ImportPreview{}, invalidInput("arr.manual_import.reprocess.series_id")
		}
		payload := make([]SonarrManualImportReprocess, 0, len(request.Files))
		for index, file := range request.Files {
			if err := file.Source.Validate(); err != nil {
				return ports.ImportPreview{}, fmt.Errorf("reprocess file %d: %w", index, err)
			}
			episodeIDs, err := uniquePositiveInts(file.EpisodeIDs)
			if err != nil {
				return ports.ImportPreview{}, invalidInput("arr.manual_import.reprocess.episode_ids")
			}
			if len(episodeIDs) == 0 {
				episodeID, parseErr := parsePositiveInt(file.MovieOrEpisodeID)
				if parseErr != nil {
					return ports.ImportPreview{}, invalidInput("arr.manual_import.reprocess.episode_ids")
				}
				episodeIDs = []int{episodeID}
			}
			if strings.TrimSpace(file.MovieOrEpisodeID) != "" {
				requestedEpisodeID, parseErr := parsePositiveInt(file.MovieOrEpisodeID)
				if parseErr != nil || !containsInt(episodeIDs, requestedEpisodeID) {
					return ports.ImportPreview{}, invalidInput("arr.manual_import.reprocess.episode_ids")
				}
			}
			languages, err := languagePayload(file)
			if err != nil {
				return ports.ImportPreview{}, err
			}
			filePath := absoluteFilePath(client.config.RootPaths, file.Source)
			if filePath == "" {
				return ports.ImportPreview{}, invalidInput("arr.manual_import.reprocess.root")
			}
			payload = append(payload, SonarrManualImportReprocess{
				Path:              filePath,
				SeriesID:          seriesID,
				SeasonNumber:      cloneIntPointer(file.SeasonNumber),
				EpisodeIDs:        episodeIDs,
				Quality:           cloneRawMessage(file.Quality),
				Languages:         languages,
				ReleaseGroup:      strings.TrimSpace(file.ReleaseGroup),
				DownloadID:        strings.TrimSpace(file.DownloadID),
				CustomFormats:     cloneRawMessages(file.CustomFormats),
				CustomFormatScore: file.CustomFormatScore,
				IndexerFlags:      file.IndexerFlags,
				ReleaseType:       strings.TrimSpace(file.ReleaseType),
			})
		}
		body, err := client.postJSON(ctx, "arr.manual_import.reprocess.sonarr", apiManualImport, payload)
		if err != nil {
			return ports.ImportPreview{}, err
		}
		return client.decodeReprocessedPreview(body, request)
	default:
		return ports.ImportPreview{}, invalidInput("arr.manual_import.reprocess.kind")
	}
}

// ObserveImport reads current title/file associations. It does not execute a
// command and leaves Effect nil because read-back is evidence, not mutation.
func (client *Client) ObserveImport(ctx context.Context, connectionID domain.ConfigID, externalID string) (ports.ImportObservation, error) {
	var result ports.ImportObservation
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	if _, err := parsePositiveInt(externalID); err != nil {
		return result, invalidInput("arr.import.external_id")
	}
	var files []ports.MediaFile
	switch client.config.Kind {
	case domain.ConnectionRadarr:
		body, err := client.get(ctx, "arr.movie.observe", apiMovies+"/"+url.PathEscape(externalID), nil)
		if err != nil {
			return result, err
		}
		var movie movieDTO
		if err := decodeJSON(body, &movie); err != nil {
			return result, malformed("arr.movie.observe")
		}
		if scalarString(movie.ID) != externalID {
			return result, observationIncomplete("arr.movie.observe", "movie_identity_mismatch")
		}
		if movie.MovieFile != nil {
			file, reason := client.movieFile(movie.ID, *movie.MovieFile)
			if reason != "" {
				return result, observationIncomplete("arr.movie.observe.file", reason)
			}
			files = append(files, file)
		} else if scalarString(movie.ID) != "" {
			body, err := client.get(ctx, "arr.movie.observe.files", apiMovieFiles, url.Values{"movieId": []string{scalarString(movie.ID)}})
			if err != nil {
				return result, err
			}
			rawFiles, _, totalFiles, decodeErr := decodeCollection(body, client.config.MaxPageSize)
			if decodeErr != nil {
				return result, malformed("arr.movie.observe.files")
			}
			if totalFiles > client.config.MaxFiles || len(rawFiles) > client.config.MaxFiles {
				return result, observationIncomplete("arr.movie.observe.files", "movie_files_limit")
			}
			if totalFiles > len(rawFiles) {
				return result, observationIncomplete("arr.movie.observe.files", "movie_files_incomplete")
			}
			for index, rawFile := range rawFiles {
				var dto movieFileDTO
				if decodeJSON(rawFile, &dto) != nil {
					return result, malformed(fmt.Sprintf("arr.movie.observe.file.%d", index))
				}
				file, reason := client.movieFile(movie.ID, dto)
				if reason != "" {
					return result, observationIncomplete("arr.movie.observe.file", reason)
				}
				files = append(files, file)
			}
		}
	case domain.ConnectionSonarr:
		body, err := client.get(ctx, "arr.episode.observe", apiEpisodes, url.Values{"seriesId": []string{externalID}})
		if err != nil {
			return result, err
		}
		raw, _, total, decodeErr := decodeCollection(body, client.config.MaxPageSize)
		if decodeErr != nil {
			return result, malformed("arr.episode.observe")
		}
		if total > client.config.MaxFiles || len(raw) > client.config.MaxFiles {
			return result, observationIncomplete("arr.episode.observe", "episode_files_limit")
		}
		if total > len(raw) {
			return result, observationIncomplete("arr.episode.observe", "episode_files_incomplete")
		}
		for index, rawEpisode := range raw {
			var episode episodeDTO
			if err := decodeJSON(rawEpisode, &episode); err != nil {
				return result, malformed(fmt.Sprintf("arr.episode.observe.%d", index))
			}
			seriesID := scalarString(episode.SeriesID)
			if seriesID == "" {
				return result, observationIncomplete("arr.episode.observe", "series_identity_missing")
			}
			if seriesID != externalID {
				return result, observationIncomplete("arr.episode.observe", "series_identity_mismatch")
			}
		}
		var reasons []string
		files, reasons = client.episodeFiles(raw)
		if len(reasons) > 0 {
			return result, observationIncomplete("arr.episode.observe", reasons[0])
		}
	}
	if len(files) == 0 {
		return result, observationIncomplete("arr.import.observe", "import_file_missing")
	}
	result.ExternalID = externalID
	result.Files = files
	result.ObservedAt = time.Now().UTC()
	return result, nil
}

func (client *Client) decodeRecord(ctx context.Context, raw json.RawMessage) (ports.MediaRecord, string, []string, error) {
	if client.config.Kind == domain.ConnectionRadarr {
		var movie movieDTO
		if err := decodeJSON(raw, &movie); err != nil {
			return ports.MediaRecord{}, "", nil, err
		}
		id := scalarString(movie.ID)
		record := ports.MediaRecord{ExternalID: id, ProviderID: firstScalar(movie.TmdbID, movie.TvdbID, movie.ImdbID), Title: movie.Title, Kind: domain.MediaMovie, Monitored: movie.Monitored}
		var reasons []string
		if movie.MovieFile != nil {
			file, reason := client.movieFile(movie.ID, *movie.MovieFile)
			if reason != "" {
				reasons = append(reasons, reason)
			} else {
				record.Files = []ports.MediaFile{file}
			}
		} else if id != "" {
			body, err := client.get(ctx, "arr.movie.files", apiMovieFiles, url.Values{"movieId": []string{id}})
			if err != nil {
				reasons = append(reasons, "movie_files_unavailable")
			} else {
				rawFiles, _, totalFiles, decodeErr := decodeCollection(body, client.config.MaxPageSize)
				if decodeErr != nil {
					reasons = append(reasons, "movie_files_malformed")
				} else {
					if totalFiles > client.config.MaxFiles || len(rawFiles) > client.config.MaxFiles {
						if len(rawFiles) > client.config.MaxFiles {
							rawFiles = rawFiles[:client.config.MaxFiles]
						}
						reasons = append(reasons, "movie_files_limit")
					}
					if totalFiles > len(rawFiles) {
						reasons = append(reasons, "movie_files_incomplete")
					}
					for _, rawFile := range rawFiles {
						var dto movieFileDTO
						if decodeJSON(rawFile, &dto) != nil {
							reasons = append(reasons, "movie_file_malformed")
							continue
						}
						file, reason := client.movieFile(movie.ID, dto)
						if reason != "" {
							reasons = append(reasons, reason)
						} else {
							record.Files = append(record.Files, file)
						}
					}
				}
			}
		}
		return record, id, reasons, nil
	}
	var series seriesDTO
	if err := decodeJSON(raw, &series); err != nil {
		return ports.MediaRecord{}, "", nil, err
	}
	id := scalarString(series.ID)
	record := ports.MediaRecord{ExternalID: id, ProviderID: firstScalar(series.TvdbID, series.ImdbID, series.TvMazeID), Title: series.Title, Kind: domain.MediaEpisode, Monitored: series.Monitored}
	var reasons []string
	if id == "" {
		return record, id, reasons, nil
	}
	body, err := client.get(ctx, "arr.series.episodes", apiEpisodes, url.Values{"seriesId": []string{id}})
	if err != nil {
		reasons = append(reasons, "episode_files_unavailable")
		return record, id, reasons, nil
	}
	rawEpisodes, _, totalEpisodes, decodeErr := decodeCollection(body, client.config.MaxPageSize)
	if decodeErr != nil {
		return record, id, []string{"episode_files_malformed"}, nil
	}
	if totalEpisodes > client.config.MaxFiles || len(rawEpisodes) > client.config.MaxFiles {
		if len(rawEpisodes) > client.config.MaxFiles {
			rawEpisodes = rawEpisodes[:client.config.MaxFiles]
		}
		reasons = append(reasons, "episode_files_limit")
	}
	if totalEpisodes > len(rawEpisodes) {
		reasons = append(reasons, "episode_files_incomplete")
	}
	files, fileReasons := client.episodeFiles(rawEpisodes)
	record.Files = files
	reasons = append(reasons, fileReasons...)
	return record, id, reasons, nil
}

func (client *Client) movieFile(movieID json.RawMessage, dto movieFileDTO) (ports.MediaFile, string) {
	file := ports.MediaFile{ExternalID: scalarString(dto.ID), Size: dto.Size, MovieID: scalarString(movieID)}
	if file.ExternalID == "" {
		return file, "movie_file_identity_missing"
	}
	if strings.TrimSpace(dto.Path) == "" {
		return file, "movie_file_path_missing"
	}
	target, mapped, ambiguous := client.mapPath(dto.Path)
	if ambiguous {
		return file, "movie_file_mapping_ambiguous"
	}
	if !mapped {
		return file, "movie_file_mapping_missing"
	}
	file.Path = target
	return file, ""
}

func (client *Client) episodeFiles(rawEpisodes []json.RawMessage) ([]ports.MediaFile, []string) {
	files := make([]ports.MediaFile, 0)
	reasons := make([]string, 0)
	index := make(map[string]int)
	for _, raw := range rawEpisodes {
		var episode episodeDTO
		if err := decodeJSON(raw, &episode); err != nil {
			reasons = append(reasons, "episode_malformed")
			continue
		}
		fileDTO := episode.EpisodeFile
		if fileDTO == nil && scalarString(episode.EpisodeFileID) != "" {
			reasons = append(reasons, "episode_file_details_missing")
			continue
		}
		if fileDTO == nil {
			continue
		}
		fileID := scalarString(fileDTO.ID)
		if fileID == "" || strings.TrimSpace(fileDTO.Path) == "" {
			reasons = append(reasons, "episode_file_identity_missing")
			continue
		}
		episodeID := scalarString(episode.ID)
		if episodeID == "" {
			reasons = append(reasons, "episode_identity_missing")
			continue
		}
		position, exists := index[fileID]
		if !exists {
			target, mapped, ambiguous := client.mapPath(fileDTO.Path)
			if ambiguous {
				reasons = append(reasons, "episode_file_mapping_ambiguous")
				continue
			}
			if !mapped {
				reasons = append(reasons, "episode_file_mapping_missing")
				continue
			}
			files = append(files, ports.MediaFile{ExternalID: fileID, Path: target, Size: fileDTO.Size})
			position = len(files) - 1
			index[fileID] = position
		}
		if !contains(files[position].EpisodeIDs, episodeID) {
			files[position].EpisodeIDs = append(files[position].EpisodeIDs, episodeID)
		}
	}
	return files, reasons
}

func (client *Client) mapPreviewResponse(body []byte, files []ports.ImportFile) ([]ports.ImportFile, []ports.ImportRejection, error) {
	folder := ""
	if len(files) > 0 {
		if absolute := absoluteFilePath(client.config.RootPaths, files[0].Source); absolute != "" {
			folder = path.Dir(absolute)
		}
	}
	return client.mapPreviewResponseFor(body, files, folder, "", nil, nil)
}

func (client *Client) mapPreviewResponseFor(body []byte, files []ports.ImportFile, queriedFolder, registeredID string, expectedEpisodeSets map[string][]string, expectedDownloadIDs map[string]string) ([]ports.ImportFile, []ports.ImportRejection, error) {
	accepted := make([]ports.ImportFile, 0)
	rejected := make([]ports.ImportRejection, 0)
	if expectedEpisodeSets == nil {
		expectedEpisodeSets = expectedEpisodeSetsFor(files)
	}
	if client.config.Kind == domain.ConnectionRadarr {
		var candidates []RadarrManualImportResource
		if err := decodeJSON(body, &candidates); err != nil {
			return nil, nil, malformed("arr.manual_import.preview.radarr")
		}
		for _, requested := range files {
			candidateFolder := queriedFolder
			if candidateFolder == "" {
				if absolute := absoluteFilePath(client.config.RootPaths, requested.Source); absolute != "" {
					candidateFolder = path.Dir(absolute)
				}
			}
			candidate, ambiguous := findRadarrCandidateFor(candidates, requested, client.config.RootPaths, candidateFolder)
			if ambiguous {
				rejected = append(rejected, rejection(requested.Source, "candidate_ambiguous", "native preview returned multiple candidates for the exact path"))
				continue
			}
			if candidate == nil {
				rejected = append(rejected, rejection(requested.Source, "not_returned", "native preview returned no exact candidate"))
				continue
			}
			if code, reason := validateDownloadScope(candidate.DownloadID, expectedDownloadIDs[sourceKey(requested.Source)]); code != "" {
				rejected = append(rejected, rejection(requested.Source, code, reason))
				continue
			}
			if len(candidate.Rejections) > 0 {
				for _, item := range candidate.Rejections {
					rejected = append(rejected, rejection(requested.Source, item.Code, item.Reason))
				}
				continue
			}
			if code, reason := validateCandidateKind(candidate.Path, candidate.RelativePath, requested, candidate.Languages, candidate.ForcedEvidence(), candidate.HearingImpairedEvidence()); code != "" {
				rejected = append(rejected, rejection(requested.Source, code, reason))
				continue
			}
			movieID := ""
			if candidate.Movie != nil {
				movieID = scalarString(candidate.Movie.ID)
			}
			if movieID == "" {
				rejected = append(rejected, rejection(requested.Source, "movie_missing", "native preview returned no movie association"))
				continue
			}
			if movieID != requested.MovieOrEpisodeID || (registeredID != "" && movieID != registeredID) {
				rejected = append(rejected, rejection(requested.Source, "movie_mismatch", "native preview selected another movie"))
				continue
			}
			accepted = append(accepted, requested)
		}
		return accepted, rejected, nil
	}
	var candidates []SonarrManualImportResource
	if err := decodeJSON(body, &candidates); err != nil {
		return nil, nil, malformed("arr.manual_import.preview.sonarr")
	}
	for _, requested := range files {
		candidateFolder := queriedFolder
		if candidateFolder == "" {
			if absolute := absoluteFilePath(client.config.RootPaths, requested.Source); absolute != "" {
				candidateFolder = path.Dir(absolute)
			}
		}
		candidate, ambiguous := findSonarrCandidateFor(candidates, requested, client.config.RootPaths, candidateFolder)
		if ambiguous {
			rejected = append(rejected, rejection(requested.Source, "candidate_ambiguous", "native preview returned multiple candidates for the exact path"))
			continue
		}
		if candidate == nil {
			rejected = append(rejected, rejection(requested.Source, "not_returned", "native preview returned no exact candidate"))
			continue
		}
		if code, reason := validateDownloadScope(candidate.DownloadID, expectedDownloadIDs[sourceKey(requested.Source)]); code != "" {
			rejected = append(rejected, rejection(requested.Source, code, reason))
			continue
		}
		if len(candidate.Rejections) > 0 {
			for _, item := range candidate.Rejections {
				rejected = append(rejected, rejection(requested.Source, item.Code, item.Reason))
			}
			continue
		}
		if code, reason := validateCandidateKind(candidate.Path, candidate.RelativePath, requested, candidate.Languages, candidate.ForcedEvidence(), candidate.HearingImpairedEvidence()); code != "" {
			rejected = append(rejected, rejection(requested.Source, code, reason))
			continue
		}
		seriesID := ""
		if candidate.Series != nil {
			seriesID = scalarString(candidate.Series.ID)
		}
		if seriesID == "" {
			rejected = append(rejected, rejection(requested.Source, "series_missing", "native preview returned no series association"))
			continue
		}
		if registeredID != "" && seriesID != registeredID {
			rejected = append(rejected, rejection(requested.Source, "series_mismatch", "native preview selected another series"))
			continue
		}
		nestedSeriesMismatch := false
		for _, episode := range candidate.Episodes {
			if nestedID := scalarString(episode.SeriesID); nestedID != "" && nestedID != seriesID {
				nestedSeriesMismatch = true
				break
			}
		}
		if nestedSeriesMismatch {
			rejected = append(rejected, rejection(requested.Source, "episode_series_mismatch", "native preview returned an episode from another series"))
			continue
		}
		nativeIDs, present, duplicate := candidateEpisodeIDs(candidate.Episodes)
		if !present {
			rejected = append(rejected, rejection(requested.Source, "episode_association_missing", "native preview returned no complete episode association"))
			continue
		}
		if duplicate {
			rejected = append(rejected, rejection(requested.Source, "episode_association_duplicate", "native preview returned a duplicate episode association"))
			continue
		}
		expected := expectedEpisodeSets[sourceKey(requested.Source)]
		if !sameStringSet(nativeIDs, expected) {
			rejected = append(rejected, rejection(requested.Source, "episode_set_mismatch", "native preview episode set differs from the reviewed selection"))
			continue
		}
		accepted = append(accepted, requested)
	}
	return accepted, rejected, nil
}

func candidateEpisodeIDs(values []ArrEpisodeReference) ([]string, bool, bool) {
	if len(values) == 0 {
		return nil, false, false
	}
	ids := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		id := scalarString(value.ID)
		if id == "" {
			return nil, false, false
		}
		if _, exists := seen[id]; exists {
			return ids, true, true
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, true, false
}

func expectedEpisodeSetsFor(files []ports.ImportFile) map[string][]string {
	result := make(map[string][]string)
	for _, file := range files {
		key := sourceKey(file.Source)
		if strings.TrimSpace(file.MovieOrEpisodeID) == "" {
			continue
		}
		result[key] = append(result[key], file.MovieOrEpisodeID)
	}
	for key := range result {
		sort.Strings(result[key])
	}
	return result
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sourceKey(source domain.FileTarget) string {
	return source.RootID.String() + "\x00" + source.RelativePath
}

func validateCandidateKind(candidatePath, relativePath string, requested ports.ImportFile, languages []json.RawMessage, forcedEvidence, hearingImpairedEvidence *bool) (string, string) {
	candidateName := path.Base(normalizeRemotePath(firstNonEmpty(candidatePath, relativePath)))
	extension := strings.ToLower(path.Ext(candidateName))
	isSubtitle := extension == ".srt" || extension == ".ass" || extension == ".ssa" || extension == ".vtt" || extension == ".sub" || extension == ".idx" || extension == ".sup"
	if requested.Subtitle != isSubtitle {
		if requested.Subtitle {
			return "subtitle_flag_mismatch", "native preview returned a non-subtitle candidate"
		}
		return "video_flag_mismatch", "native preview returned a subtitle candidate"
	}
	if requested.Subtitle && strings.TrimSpace(requested.Language) != "" && !nativeLanguagesContain(languages, requested.Language) {
		if len(languages) == 0 {
			return "subtitle_language_unknown", "native preview did not expose subtitle language evidence"
		}
		return "subtitle_language_mismatch", "native preview did not return the requested subtitle language"
	}
	if requested.Subtitle && requested.Forced {
		if forcedEvidence == nil {
			return "subtitle_forced_unknown", "native preview did not expose forced-subtitle evidence"
		}
		if !*forcedEvidence {
			return "subtitle_forced_mismatch", "native preview did not identify the subtitle as forced"
		}
	}
	if requested.Subtitle && requested.HearingImpaired {
		if hearingImpairedEvidence == nil {
			return "subtitle_hearing_impaired_unknown", "native preview did not expose SDH evidence"
		}
		if !*hearingImpairedEvidence {
			return "subtitle_hearing_impaired_mismatch", "native preview did not identify the subtitle as SDH"
		}
	}
	return "", ""
}

func nativeLanguagesContain(values []json.RawMessage, wanted string) bool {
	wanted = normalizeLanguage(wanted)
	if wanted == "" {
		return true
	}
	for _, raw := range values {
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) == nil {
			for _, key := range []string{"name", "isoCode", "code", "language"} {
				if candidate, ok := object[key]; ok && languageEqual(scalarString(candidate), wanted) {
					return true
				}
			}
		}
		if languageEqual(scalarString(raw), wanted) {
			return true
		}
	}
	return false
}

func languageEqual(left, right string) bool {
	left, right = normalizeLanguage(left), normalizeLanguage(right)
	if left == right {
		return true
	}
	aliases := map[string]string{"en": "english", "eng": "english", "pt": "portuguese", "por": "portuguese", "es": "spanish", "spa": "spanish", "ja": "japanese", "jpn": "japanese"}
	return aliases[left] != "" && aliases[left] == aliases[right]
}

func normalizeLanguage(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	return value
}

func (client *Client) decodeReprocessedPreview(body []byte, request ReprocessPreviewRequest) (ports.ImportPreview, error) {
	neutral := make([]ports.ImportFile, 0, len(request.Files))
	for _, file := range request.Files {
		mediaID := file.MovieOrEpisodeID
		if mediaID == "" && len(file.EpisodeIDs) > 0 {
			mediaID = file.EpisodeIDs[0]
		}
		neutral = append(neutral, ports.ImportFile{
			Source: file.Source, MovieOrEpisodeID: mediaID,
			Subtitle: file.Subtitle, Language: file.Language, Forced: file.Forced, HearingImpaired: file.HearingImpaired,
		})
	}
	accepted, rejected, err := client.mapPreviewResponseFor(body, neutral, "", request.RegisteredExternalID, reprocessEpisodeSets(request.Files), reprocessDownloadIDs(request.Files))
	if err != nil {
		return ports.ImportPreview{}, err
	}
	return ports.ImportPreview{Revision: client.reprocessRevision(request, body), Files: accepted, Rejections: rejected, ObservedAt: time.Now().UTC()}, nil
}

func reprocessEpisodeSets(files []ReprocessFile) map[string][]string {
	result := make(map[string][]string)
	for _, file := range files {
		key := sourceKey(file.Source)
		if len(file.EpisodeIDs) > 0 {
			result[key] = append(result[key], file.EpisodeIDs...)
			continue
		}
		if strings.TrimSpace(file.MovieOrEpisodeID) != "" {
			result[key] = append(result[key], file.MovieOrEpisodeID)
		}
	}
	for key := range result {
		sort.Strings(result[key])
	}
	return result
}

func reprocessDownloadIDs(files []ReprocessFile) map[string]string {
	result := make(map[string]string)
	for _, file := range files {
		if downloadID := strings.TrimSpace(file.DownloadID); downloadID != "" {
			result[sourceKey(file.Source)] = downloadID
		}
	}
	return result
}

func validateDownloadScope(candidate, expected string) (string, string) {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return "", ""
	}
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "download_unknown", "native preview did not expose source download identity"
	}
	if candidate != expected {
		return "download_mismatch", "native preview selected another source download"
	}
	return "", ""
}

func (client *Client) previewQuery(registeredID, downloadID string, files []ports.ImportFile) (url.Values, error) {
	if len(files) == 0 {
		return nil, invalidInput("arr.manual_import.preview.files")
	}
	folder := absoluteFilePath(client.config.RootPaths, files[0].Source)
	if folder == "" {
		return nil, invalidInput("arr.manual_import.preview.root")
	}
	query := url.Values{"folder": []string{path.Dir(folder)}, "filterExistingFiles": []string{"true"}}
	if strings.TrimSpace(downloadID) != "" {
		query.Set("downloadId", strings.TrimSpace(downloadID))
	}
	switch client.config.Kind {
	case domain.ConnectionRadarr:
		if _, err := parsePositiveInt(registeredID); err != nil {
			return nil, invalidInput("arr.manual_import.preview.movie_id")
		}
		if downloadID != "" {
			query.Set("movieId", registeredID)
		}
	case domain.ConnectionSonarr:
		if _, err := parsePositiveInt(registeredID); err != nil {
			return nil, invalidInput("arr.manual_import.preview.series_id")
		}
		if downloadID != "" {
			query.Set("seriesId", registeredID)
		}
	default:
		return nil, invalidInput("arr.manual_import.preview.kind")
	}
	return query, nil
}

func groupImportFiles(files []ports.ImportFile) [][]ports.ImportFile {
	groups := make(map[string][]ports.ImportFile)
	order := make([]string, 0)
	for _, file := range files {
		key := file.Source.RootID.String() + ":" + path.Dir(file.Source.RelativePath)
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], file)
	}
	result := make([][]ports.ImportFile, 0, len(order))
	for _, key := range order {
		result = append(result, groups[key])
	}
	return result
}

func findRadarrCandidate(candidates []RadarrManualImportResource, requested ports.ImportFile, roots map[domain.ConfigID]string) *RadarrManualImportResource {
	folder := ""
	if absolute := absoluteFilePath(roots, requested.Source); absolute != "" {
		folder = path.Dir(absolute)
	}
	candidate, ambiguous := findRadarrCandidateFor(candidates, requested, roots, folder)
	if ambiguous {
		return nil
	}
	return candidate
}

func findRadarrCandidateFor(candidates []RadarrManualImportResource, requested ports.ImportFile, roots map[domain.ConfigID]string, queriedFolder string) (*RadarrManualImportResource, bool) {
	var match *RadarrManualImportResource
	for index := range candidates {
		candidate := &candidates[index]
		if candidateTargetMatches(candidate.Path, candidate.RelativePath, requested.Source, roots, queriedFolder) {
			if match != nil {
				return nil, true
			}
			match = candidate
		}
	}
	return match, false
}

func findSonarrCandidate(candidates []SonarrManualImportResource, requested ports.ImportFile, roots map[domain.ConfigID]string) *SonarrManualImportResource {
	folder := ""
	if absolute := absoluteFilePath(roots, requested.Source); absolute != "" {
		folder = path.Dir(absolute)
	}
	candidate, ambiguous := findSonarrCandidateFor(candidates, requested, roots, folder)
	if ambiguous {
		return nil
	}
	return candidate
}

func findSonarrCandidateFor(candidates []SonarrManualImportResource, requested ports.ImportFile, roots map[domain.ConfigID]string, queriedFolder string) (*SonarrManualImportResource, bool) {
	var match *SonarrManualImportResource
	for index := range candidates {
		candidate := &candidates[index]
		if candidateTargetMatches(candidate.Path, candidate.RelativePath, requested.Source, roots, queriedFolder) {
			if match != nil {
				return nil, true
			}
			match = candidate
		}
	}
	return match, false
}

func targetMatches(candidatePath string, requested domain.FileTarget, roots map[domain.ConfigID]string) bool {
	absolute := absoluteFilePath(roots, requested)
	if absolute == "" {
		return false
	}
	return targetMatchesInFolder(candidatePath, requested, roots, path.Dir(absolute))
}

func candidateTargetMatches(candidatePath, relativePath string, requested domain.FileTarget, roots map[domain.ConfigID]string, queriedFolder string) bool {
	// Arr's Path is authoritative when present. Falling back to RelativePath
	// after a non-empty Path mismatch would let a malformed response bind a
	// candidate from another directory.
	if strings.TrimSpace(candidatePath) != "" {
		return targetMatchesInFolder(candidatePath, requested, roots, queriedFolder)
	}
	return targetMatchesInFolder(relativePath, requested, roots, queriedFolder)
}

func targetMatchesInFolder(candidatePath string, requested domain.FileTarget, roots map[domain.ConfigID]string, queriedFolder string) bool {
	absolute := absoluteFilePath(roots, requested)
	if absolute == "" {
		return false
	}
	if !safeNativePath(candidatePath) {
		return false
	}
	normalizedCandidate := normalizeRemotePath(candidatePath)
	if normalizedCandidate == "" {
		return false
	}
	if !absoluteRemotePath(normalizedCandidate) {
		folder := normalizeRemotePath(queriedFolder)
		if !absoluteRemotePath(folder) {
			return false
		}
		normalizedCandidate = normalizeRemotePath(path.Join(folder, normalizedCandidate))
	}
	return normalizedCandidate == normalizeRemotePath(absolute)
}

func safeNativePath(value string) bool {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	if value == "" || strings.ContainsRune(value, 0) {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func validateImportRequest(request ports.ImportPreviewRequest, maxFiles int) error {
	if strings.TrimSpace(request.RegisteredExternalID) == "" {
		return invalidInput("arr.manual_import.preview.registered_id")
	}
	if len(request.Files) == 0 || len(request.Files) > maxFiles {
		return invalidInput("arr.manual_import.preview.files")
	}
	if !validTransfer(request.Transfer) {
		return invalidInput("arr.manual_import.preview.transfer")
	}
	for index, file := range request.Files {
		if err := file.Source.Validate(); err != nil {
			return fmt.Errorf("preview file %d: %w", index, err)
		}
		if strings.TrimSpace(file.MovieOrEpisodeID) == "" {
			return invalidInput("arr.manual_import.preview.episode_id")
		}
	}
	if err := rejectDuplicateImportIdentities(request.Files); err != nil {
		return err
	}
	return nil
}

func validateReprocessRequest(request ReprocessPreviewRequest, maxFiles int) error {
	if strings.TrimSpace(request.RegisteredExternalID) == "" {
		return invalidInput("arr.manual_import.reprocess.registered_id")
	}
	if len(request.Files) == 0 || len(request.Files) > maxFiles {
		return invalidInput("arr.manual_import.reprocess.files")
	}
	if !validTransfer(request.Transfer) {
		return invalidInput("arr.manual_import.reprocess.transfer")
	}
	for index, file := range request.Files {
		if err := file.Source.Validate(); err != nil {
			return fmt.Errorf("reprocess file %d: %w", index, err)
		}
		if strings.TrimSpace(file.MovieOrEpisodeID) == "" && len(file.EpisodeIDs) == 0 {
			return invalidInput("arr.manual_import.reprocess.episode_ids")
		}
		if file.SeasonNumber != nil && *file.SeasonNumber < 0 {
			return invalidInput("arr.manual_import.reprocess.season_number")
		}
	}
	if err := rejectDuplicateReprocessIdentities(request.Files); err != nil {
		return err
	}
	return nil
}

func rejectDuplicateImportIdentities(files []ports.ImportFile) error {
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		key := file.Source.RootID.String() + "\x00" + file.Source.RelativePath + "\x00" + strings.TrimSpace(file.MovieOrEpisodeID)
		if _, exists := seen[key]; exists {
			return invalidInput("arr.manual_import.preview.duplicate_file")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func rejectDuplicateReprocessIdentities(files []ReprocessFile) error {
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		key := file.Source.RootID.String() + "\x00" + file.Source.RelativePath + "\x00" + strings.TrimSpace(file.MovieOrEpisodeID)
		if _, exists := seen[key]; exists {
			return invalidInput("arr.manual_import.reprocess.duplicate_file")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func languagePayload(file ReprocessFile) ([]ArrLanguage, error) {
	if file.Languages != nil {
		for _, language := range file.Languages {
			if language.ID <= 0 || strings.TrimSpace(language.Name) == "" {
				return nil, invalidInput("arr.manual_import.reprocess.languages")
			}
		}
		return append(make([]ArrLanguage, 0, len(file.Languages)), file.Languages...), nil
	}
	result := make([]ArrLanguage, 0, 1)
	language := normalizeLanguage(file.Language)
	if language == "" {
		return result, nil
	}
	known := map[string]ArrLanguage{
		"en":    {ID: 1, Name: "English"},
		"eng":   {ID: 1, Name: "English"},
		"pt":    {ID: 18, Name: "Portuguese"},
		"por":   {ID: 18, Name: "Portuguese"},
		"pt-br": {ID: 33, Name: "Portuguese (Brazil)"},
		"es":    {ID: 2, Name: "Spanish"},
		"spa":   {ID: 2, Name: "Spanish"},
		"fr":    {ID: 3, Name: "French"},
		"fra":   {ID: 3, Name: "French"},
		"de":    {ID: 4, Name: "German"},
		"deu":   {ID: 4, Name: "German"},
		"ja":    {ID: 8, Name: "Japanese"},
		"jpn":   {ID: 8, Name: "Japanese"},
	}
	value, ok := known[language]
	if !ok {
		return nil, invalidInput("arr.manual_import.reprocess.language")
	}
	return append(result, value), nil
}

func validTransfer(value string) bool {
	switch value {
	case "copy", "hardlink", "move":
		return true
	default:
		return false
	}
}

func catalogPath(kind domain.ConnectionKind) string {
	if kind == domain.ConnectionRadarr {
		return apiMovies
	}
	return apiSeries
}

func lookupPath(kind domain.ConnectionKind) string {
	if kind == domain.ConnectionRadarr {
		return apiMovieLookup
	}
	return apiSeriesLookup
}

func validLookupKind(kind domain.ConnectionKind, mediaKind domain.MediaKind) bool {
	if kind == domain.ConnectionRadarr {
		return mediaKind == domain.MediaMovie
	}
	return mediaKind == domain.MediaEpisode || mediaKind == domain.MediaSeason || mediaKind == domain.MediaAnime
}

func (client *Client) pageLimit(requested int) (int, error) {
	if requested <= 0 {
		return client.config.MaxPageSize, nil
	}
	if requested > client.config.MaxPageSize {
		return 0, invalidInput("arr.inventory.limit")
	}
	return requested, nil
}

type cursorState struct {
	SourceID         domain.RuntimeID
	Collection       string
	Page             int
	PageSize         int
	Offset           int
	ObservedCount    int
	SeenIDs          []string
	Reasons          []string
	SnapshotRevision string
	StartedAt        time.Time
}

func (client *Client) encodeCursor(state cursorState) (string, error) {
	payload, err := json.Marshal(state)
	if err != nil || len(payload) > maxCursorBytes {
		return "", invalidInput("arr.inventory.cursor")
	}
	mac := hmac.New(sha256.New, client.cursorKey)
	_, _ = mac.Write(payload)
	encoded := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(encoded) > maxCursorBytes {
		return "", invalidInput("arr.inventory.cursor")
	}
	return encoded, nil
}

func (client *Client) decodeCursor(value string) (cursorState, error) {
	if value == "" {
		return cursorState{}, nil
	}
	if len(value) > maxCursorBytes {
		return cursorState{}, invalidInput("arr.inventory.cursor")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return cursorState{}, invalidInput("arr.inventory.cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) > maxCursorBytes {
		return cursorState{}, invalidInput("arr.inventory.cursor")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursorState{}, invalidInput("arr.inventory.cursor")
	}
	mac := hmac.New(sha256.New, client.cursorKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursorState{}, invalidInput("arr.inventory.cursor")
	}
	var state cursorState
	if err := decodeJSON(payload, &state); err != nil || !state.SourceID.Valid() || state.Page <= 0 || state.PageSize <= 0 || state.Offset < 0 || state.ObservedCount < 0 {
		return cursorState{}, invalidInput("arr.inventory.cursor")
	}
	return state, nil
}

// The Arr response types below intentionally contain only fields the domain
// can use. Arr adds fields frequently; accepting those fields while decoding
// keeps an upstream upgrade from turning a read-only inventory into an
// outage. Request DTOs above are built explicitly and never carry arbitrary
// command fields.
type movieDTO struct {
	ID        json.RawMessage `json:"id"`
	Title     string          `json:"title"`
	TmdbID    json.RawMessage `json:"tmdbId"`
	TvdbID    json.RawMessage `json:"tvdbId"`
	ImdbID    json.RawMessage `json:"imdbId"`
	Monitored bool            `json:"monitored"`
	MovieFile *movieFileDTO   `json:"movieFile"`
}

type movieFileDTO struct {
	ID           json.RawMessage `json:"id"`
	Path         string          `json:"path"`
	RelativePath string          `json:"relativePath"`
	Size         int64           `json:"size"`
}

type seriesDTO struct {
	ID        json.RawMessage `json:"id"`
	Title     string          `json:"title"`
	TvdbID    json.RawMessage `json:"tvdbId"`
	ImdbID    json.RawMessage `json:"imdbId"`
	TvMazeID  json.RawMessage `json:"tvMazeId"`
	Monitored bool            `json:"monitored"`
}

type episodeDTO struct {
	ID            json.RawMessage `json:"id"`
	SeriesID      json.RawMessage `json:"seriesId"`
	EpisodeFileID json.RawMessage `json:"episodeFileId"`
	EpisodeFile   *episodeFileDTO `json:"episodeFile"`
}

type episodeFileDTO struct {
	ID   json.RawMessage `json:"id"`
	Path string          `json:"path"`
	Size int64           `json:"size"`
}

type rootFolderDTO struct {
	Path string `json:"path"`
}

type qualityProfileDTO struct {
	ID   json.RawMessage `json:"id"`
	Name string          `json:"name"`
}

type historyDTO struct {
	ID          json.RawMessage `json:"id"`
	EventType   string          `json:"eventType"`
	Date        string          `json:"date"`
	SourceTitle string          `json:"sourceTitle"`
	MovieID     json.RawMessage `json:"movieId"`
	SeriesID    json.RawMessage `json:"seriesId"`
	EpisodeID   json.RawMessage `json:"episodeId"`
	DownloadID  string          `json:"downloadId"`
	Quality     json.RawMessage `json:"quality"`
	Successful  bool            `json:"successful"`
	SourcePath  string          `json:"sourcePath"`
	Destination string          `json:"destination"`
}

func decodeHistoryEntry(raw json.RawMessage) (HistoryEntry, error) {
	var dto historyDTO
	if err := decodeJSON(raw, &dto); err != nil {
		return HistoryEntry{}, err
	}
	id := scalarString(dto.ID)
	if id == "" {
		return HistoryEntry{}, errors.New("history id is missing")
	}
	date, err := parseRFC3339UTC(dto.Date)
	if err != nil {
		return HistoryEntry{}, err
	}
	return HistoryEntry{
		ID: id, EventType: strings.TrimSpace(dto.EventType), Date: date,
		SourceTitle: strings.TrimSpace(dto.SourceTitle), MovieID: scalarString(dto.MovieID),
		SeriesID: scalarString(dto.SeriesID), EpisodeID: scalarString(dto.EpisodeID),
		DownloadID: strings.TrimSpace(dto.DownloadID), Quality: qualityString(dto.Quality),
		Successful: dto.Successful, SourcePath: strings.TrimSpace(dto.SourcePath),
		Destination: strings.TrimSpace(dto.Destination),
	}, nil
}

func parseRFC3339UTC(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("history date is missing")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func parseEndpoint(value string) (*url.URL, error) {
	if strings.TrimSpace(value) != value || value == "" {
		return nil, errors.New("Arr endpoint is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Arr endpoint must be an absolute URL without credentials or query")
	}
	return parsed, nil
}

func validateConnectionScope(expected, requested domain.ConfigID) error {
	if !requested.Valid() || requested != expected {
		return invalidInput("arr.connection")
	}
	return nil
}

func invalidInput(operation string) error {
	return domain.UpstreamError{Code: domain.OutcomeInvalidInput, Operation: operation, Detail: "request is invalid"}
}

func malformed(operation string) error {
	return domain.UpstreamError{Code: domain.OutcomeUnknown, Operation: operation, Detail: "upstream response is malformed"}
}

func observationIncomplete(operation, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "required import evidence is incomplete"
	}
	return domain.UpstreamError{
		Code:      domain.OutcomeUnknown,
		Operation: operation,
		Detail:    "required import evidence is incomplete: " + reason,
	}
}

func normalizeStatus(operation string, status int) error {
	code := domain.OutcomeUnknown
	retryable := false
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		code = domain.OutcomeUnauthorized
	case status == http.StatusTooManyRequests:
		code, retryable = domain.OutcomeRateLimited, true
	case status == http.StatusBadRequest:
		code = domain.OutcomeInvalidInput
	case status == http.StatusConflict:
		code = domain.OutcomeConflict
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented:
		code = domain.OutcomeUnsupported
	case status == http.StatusRequestTimeout || status >= http.StatusInternalServerError:
		code, retryable = domain.OutcomeUnavailable, true
	}
	return domain.UpstreamError{Code: code, Status: status, Retryable: retryable, Operation: operation, Detail: "upstream request failed"}
}

func (client *Client) get(ctx context.Context, operation, endpoint string, query url.Values) ([]byte, error) {
	body, status, err := client.request(ctx, operation, http.MethodGet, endpoint, query, nil)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, normalizeStatus(operation, status)
	}
	return body, nil
}

func (client *Client) postJSON(ctx context.Context, operation, endpoint string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, invalidInput(operation)
	}
	response, status, requestErr := client.request(ctx, operation, http.MethodPost, endpoint, nil, bytes.NewReader(body))
	if requestErr != nil {
		return nil, requestErr
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, normalizeStatus(operation, status)
	}
	return response, nil
}

func (client *Client) request(ctx context.Context, operation, method, endpoint string, query url.Values, body io.Reader) ([]byte, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	requestURL := *client.endpoint
	requestURL.Path = strings.TrimRight(client.endpoint.Path, "/") + endpoint
	requestURL.RawPath = ""
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return nil, 0, errors.New("Arr request could not be created")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "mastarr-arr-read/0.0.1")
	if client.config.APIKey != "" {
		request.Header.Set("X-Api-Key", client.config.APIKey)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, 0, ctxErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, 0, err
		}
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, 0, domain.UpstreamError{Code: domain.OutcomeUnavailable, Retryable: true, Operation: operation, Detail: "upstream request timed out"}
		}
		return nil, 0, domain.UpstreamError{Code: domain.OutcomeUnavailable, Retryable: true, Operation: operation, Detail: "upstream is unavailable"}
	}
	defer response.Body.Close()
	data, readErr := readBounded(response.Body, client.config.MaxResponseSize)
	if readErr != nil {
		return nil, response.StatusCode, domain.UpstreamError{Code: domain.OutcomeUnknown, Status: response.StatusCode, Operation: operation, Detail: "upstream response exceeded the configured bound"}
	}
	return data, response.StatusCode, nil
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("response bound is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, errors.New("response could not be read")
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("response exceeds configured bound")
	}
	return data, nil
}

func decodeJSON(data []byte, target any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("empty JSON response")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON response data")
	}
	return nil
}

func decodeCollection(data []byte, limit int) ([]json.RawMessage, bool, int, error) {
	var value json.RawMessage
	if err := decodeJSON(data, &value); err != nil {
		return nil, false, 0, err
	}
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var items []json.RawMessage
		if err := decodeJSON(trimmed, &items); err != nil {
			return nil, false, 0, err
		}
		return items, len(items) >= limit && limit > 0, len(items), nil
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false, 0, errors.New("collection is neither array nor object")
	}
	var object map[string]json.RawMessage
	if err := decodeJSON(trimmed, &object); err != nil {
		return nil, false, 0, err
	}
	var rawItems json.RawMessage
	for _, key := range []string{"records", "items", "results"} {
		if candidate, ok := object[key]; ok {
			rawItems = candidate
			break
		}
	}
	if len(rawItems) == 0 || bytes.Equal(bytes.TrimSpace(rawItems), []byte("null")) {
		return nil, false, 0, errors.New("collection items are missing")
	}
	var items []json.RawMessage
	if err := decodeJSON(rawItems, &items); err != nil {
		return nil, false, 0, err
	}
	total := len(items)
	hasExplicitTotal := false
	for _, key := range []string{"totalRecords", "total", "count"} {
		if rawTotal, ok := object[key]; ok {
			hasExplicitTotal = true
			if parsed, parseErr := rawPositiveInt(rawTotal); parseErr == nil {
				total = parsed
			}
			break
		}
	}
	hasMore := false
	if rawMore, ok := object["hasMore"]; ok {
		_ = json.Unmarshal(rawMore, &hasMore)
	} else if rawMore, ok := object["hasMoreResults"]; ok {
		_ = json.Unmarshal(rawMore, &hasMore)
	} else {
		page, pageErr := rawPositiveInt(object["page"])
		pageSize, sizeErr := rawPositiveInt(object["pageSize"])
		if pageErr == nil && sizeErr == nil && hasExplicitTotal && total > 0 {
			hasMore = page*pageSize < total
		} else {
			hasMore = len(items) >= limit && limit > 0
		}
	}
	return items, hasMore, total, nil
}

// collectionHasTotal reports whether an object response supplied an explicit
// collection total. It lets callers distinguish a global total from the
// per-page item count used when an upstream response omits totals.
func collectionHasTotal(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var object map[string]json.RawMessage
	if decodeJSON(trimmed, &object) != nil {
		return false
	}
	for _, key := range []string{"totalRecords", "total", "count"} {
		if _, ok := object[key]; ok {
			return true
		}
	}
	return false
}

func isJSONArray(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && trimmed[0] == '['
}

func rawPositiveInt(value json.RawMessage) (int, error) {
	if len(value) == 0 {
		return 0, errors.New("integer is missing")
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return 0, err
	}
	parsed, err := strconv.Atoi(number.String())
	if err != nil || parsed <= 0 {
		return 0, errors.New("integer is invalid")
	}
	return parsed, nil
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func scalarString(value json.RawMessage) string {
	if len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return ""
	}
	var stringValue string
	if json.Unmarshal(value, &stringValue) == nil {
		return strings.TrimSpace(stringValue)
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if decoder.Decode(&number) == nil {
		return strings.TrimSpace(number.String())
	}
	return ""
}

func qualityString(value json.RawMessage) string {
	if result := scalarString(value); result != "" {
		return result
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(value, &object) != nil {
		return ""
	}
	if nested, ok := object["quality"]; ok {
		if result := qualityString(nested); result != "" {
			return result
		}
	}
	for _, key := range []string{"name", "qualityName"} {
		if result := scalarString(object[key]); result != "" {
			return result
		}
	}
	return ""
}

func firstScalar(values ...json.RawMessage) string {
	for _, value := range values {
		if result := scalarString(value); result != "" {
			return result
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func parsePositiveInt(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return 0, errors.New("integer is invalid")
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, errors.New("integer is invalid")
	}
	return parsed, nil
}

func positiveInts(values []string) ([]int, error) {
	result := make([]int, 0, len(values))
	for _, value := range values {
		parsed, err := parsePositiveInt(value)
		if err != nil {
			return nil, err
		}
		result = append(result, parsed)
	}
	return result, nil
}

func uniquePositiveInts(values []string) ([]int, error) {
	result, err := positiveInts(values)
	if err != nil {
		return nil, err
	}
	seen := make(map[int]struct{}, len(result))
	for _, value := range result {
		if _, exists := seen[value]; exists {
			return nil, errors.New("integer set contains a duplicate")
		}
		seen[value] = struct{}{}
	}
	return result, nil
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	if value == nil {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func cloneRawMessages(values []json.RawMessage) []json.RawMessage {
	result := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		result = append(result, cloneRawMessage(value))
	}
	return result
}

func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func containsRawID(values []json.RawMessage, wanted string) bool {
	for _, value := range values {
		if scalarString(value) == wanted {
			return true
		}
	}
	return false
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (client *Client) previewRevision(request ports.ImportPreviewRequest, downloadID string, evidence [][]byte) string {
	return previewRevisionScoped(client.config.ConnectionID.String(), string(client.config.Kind), client.configRevision(), request, downloadID, evidence)
}

// previewRevision remains available to package tests and older callers while
// the client method above binds the revision to the configured connection and
// mapping configuration. Every semantically meaningful selection field is
// canonicalized before hashing, so review order does not change the binding.
func previewRevision(request ports.ImportPreviewRequest, evidence [][]byte) string {
	return previewRevisionScoped("", "", "", request, "", evidence)
}

func previewRevisionScoped(connectionID, kind, configRevision string, request ports.ImportPreviewRequest, downloadID string, evidence [][]byte) string {
	type revisionFile struct {
		RootID          string `json:"rootId"`
		Path            string `json:"relativePath"`
		ID              string `json:"mediaId"`
		Subtitle        bool   `json:"subtitle"`
		Language        string `json:"language"`
		Forced          bool   `json:"forced"`
		HearingImpaired bool   `json:"hearingImpaired"`
	}
	type revisionInput struct {
		ConnectionID   string         `json:"connectionId"`
		Kind           string         `json:"kind"`
		ConfigRevision string         `json:"configRevision"`
		RegisteredID   string         `json:"registeredId"`
		DownloadID     string         `json:"downloadId"`
		Transfer       string         `json:"transfer"`
		Files          []revisionFile `json:"files"`
		Evidence       []string       `json:"evidence"`
	}
	input := revisionInput{
		ConnectionID: connectionID, Kind: kind, ConfigRevision: configRevision,
		RegisteredID: request.RegisteredExternalID, DownloadID: strings.TrimSpace(downloadID),
		Transfer: request.Transfer, Files: make([]revisionFile, 0, len(request.Files)), Evidence: make([]string, 0, len(evidence)),
	}
	for _, file := range request.Files {
		input.Files = append(input.Files, revisionFile{
			RootID: file.Source.RootID.String(), Path: file.Source.RelativePath,
			ID: file.MovieOrEpisodeID, Subtitle: file.Subtitle, Language: normalizeLanguage(file.Language),
			Forced: file.Forced, HearingImpaired: file.HearingImpaired,
		})
	}
	sort.Slice(input.Files, func(left, right int) bool {
		leftEncoded, _ := json.Marshal(input.Files[left])
		rightEncoded, _ := json.Marshal(input.Files[right])
		return string(leftEncoded) < string(rightEncoded)
	})
	for _, body := range evidence {
		input.Evidence = append(input.Evidence, digest(body))
	}
	sort.Strings(input.Evidence)
	encoded, _ := json.Marshal(input)
	return digest(encoded)
}

func (client *Client) reprocessRevision(request ReprocessPreviewRequest, evidence []byte) string {
	type revisionEpisode struct {
		SeriesID                   string `json:"seriesId"`
		ID                         string `json:"id"`
		EpisodeFileID              string `json:"episodeFileId"`
		SeasonNumber               int    `json:"seasonNumber"`
		EpisodeNumber              int    `json:"episodeNumber"`
		AbsoluteEpisodeNumber      int    `json:"absoluteEpisodeNumber"`
		SceneAbsoluteEpisodeNumber int    `json:"sceneAbsoluteEpisodeNumber"`
	}
	type revisionFile struct {
		RootID              string            `json:"rootId"`
		Path                string            `json:"relativePath"`
		ID                  string            `json:"mediaId"`
		EpisodeIDs          []string          `json:"episodeIds"`
		DownloadID          string            `json:"downloadId"`
		Subtitle            bool              `json:"subtitle"`
		Language            string            `json:"language"`
		Forced              bool              `json:"forced"`
		HearingImpaired     bool              `json:"hearingImpaired"`
		SeasonNumber        *int              `json:"seasonNumber"`
		Episodes            []revisionEpisode `json:"episodes"`
		QualityDigest       string            `json:"qualityDigest"`
		Languages           []ArrLanguage     `json:"languages"`
		ReleaseGroup        string            `json:"releaseGroup"`
		CustomFormatDigests []string          `json:"customFormatDigests"`
		CustomFormatScore   int               `json:"customFormatScore"`
		IndexerFlags        int               `json:"indexerFlags"`
		ReleaseType         string            `json:"releaseType"`
	}
	type revisionInput struct {
		ConnectionID   string         `json:"connectionId"`
		Kind           string         `json:"kind"`
		ConfigRevision string         `json:"configRevision"`
		RegisteredID   string         `json:"registeredId"`
		Transfer       string         `json:"transfer"`
		Files          []revisionFile `json:"files"`
		Evidence       string         `json:"evidence"`
	}
	input := revisionInput{
		ConnectionID: client.config.ConnectionID.String(), Kind: string(client.config.Kind), ConfigRevision: client.configRevision(),
		RegisteredID: request.RegisteredExternalID, Transfer: request.Transfer, Files: make([]revisionFile, 0, len(request.Files)), Evidence: digest(evidence),
	}
	for _, file := range request.Files {
		languages, _ := languagePayload(file)
		episodes := make([]revisionEpisode, 0, len(file.Episodes))
		for _, episode := range file.Episodes {
			episodes = append(episodes, revisionEpisode{
				SeriesID: scalarString(episode.SeriesID), ID: scalarString(episode.ID), EpisodeFileID: scalarString(episode.EpisodeFileID),
				SeasonNumber: episode.SeasonNumber, EpisodeNumber: episode.EpisodeNumber,
				AbsoluteEpisodeNumber: episode.AbsoluteEpisodeNumber, SceneAbsoluteEpisodeNumber: episode.SceneAbsoluteEpisodeNumber,
			})
		}
		sort.Slice(episodes, func(left, right int) bool {
			leftEncoded, _ := json.Marshal(episodes[left])
			rightEncoded, _ := json.Marshal(episodes[right])
			return string(leftEncoded) < string(rightEncoded)
		})
		episodeIDs := append([]string(nil), file.EpisodeIDs...)
		sort.Strings(episodeIDs)
		customFormatDigests := make([]string, 0, len(file.CustomFormats))
		for _, customFormat := range file.CustomFormats {
			customFormatDigests = append(customFormatDigests, digest(customFormat))
		}
		sort.Strings(customFormatDigests)
		input.Files = append(input.Files, revisionFile{
			RootID: file.Source.RootID.String(), Path: file.Source.RelativePath, ID: file.MovieOrEpisodeID,
			EpisodeIDs: episodeIDs, DownloadID: strings.TrimSpace(file.DownloadID), Subtitle: file.Subtitle,
			Language: normalizeLanguage(file.Language), Forced: file.Forced, HearingImpaired: file.HearingImpaired,
			SeasonNumber: cloneIntPointer(file.SeasonNumber), Episodes: episodes, QualityDigest: digest(file.Quality),
			Languages: languages, ReleaseGroup: strings.TrimSpace(file.ReleaseGroup), CustomFormatDigests: customFormatDigests,
			CustomFormatScore: file.CustomFormatScore, IndexerFlags: file.IndexerFlags, ReleaseType: strings.TrimSpace(file.ReleaseType),
		})
	}
	sort.Slice(input.Files, func(left, right int) bool {
		leftEncoded, _ := json.Marshal(input.Files[left])
		rightEncoded, _ := json.Marshal(input.Files[right])
		return string(leftEncoded) < string(rightEncoded)
	})
	encoded, _ := json.Marshal(input)
	return digest(encoded)
}

func (client *Client) configRevision() string {
	type root struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	type mapping struct {
		ID                string `json:"id"`
		ConnectionID      string `json:"connectionId"`
		SourcePrefix      string `json:"sourcePrefix"`
		RootID            string `json:"rootId"`
		DestinationPrefix string `json:"destinationPrefix"`
		Revision          string `json:"revision"`
	}
	type config struct {
		ConnectionID string    `json:"connectionId"`
		Kind         string    `json:"kind"`
		Roots        []root    `json:"roots"`
		Mappings     []mapping `json:"mappings"`
	}
	input := config{ConnectionID: client.config.ConnectionID.String(), Kind: string(client.config.Kind), Roots: make([]root, 0, len(client.config.RootPaths)), Mappings: make([]mapping, 0, len(client.config.Mappings))}
	for rootID, rootPath := range client.config.RootPaths {
		input.Roots = append(input.Roots, root{ID: rootID.String(), Path: normalizeRemotePath(rootPath)})
	}
	sort.Slice(input.Roots, func(left, right int) bool { return input.Roots[left].ID < input.Roots[right].ID })
	for _, item := range client.config.Mappings {
		input.Mappings = append(input.Mappings, mapping{ID: item.ID.String(), ConnectionID: item.ConnectionID.String(), SourcePrefix: normalizeMappingPrefix(item.SourcePrefix), RootID: item.RootID.String(), DestinationPrefix: strings.Trim(item.DestinationPrefix, "/"), Revision: strings.TrimSpace(item.Revision)})
	}
	sort.Slice(input.Mappings, func(left, right int) bool {
		leftEncoded, _ := json.Marshal(input.Mappings[left])
		rightEncoded, _ := json.Marshal(input.Mappings[right])
		return string(leftEncoded) < string(rightEncoded)
	})
	encoded, _ := json.Marshal(input)
	return digest(encoded)
}

func rejection(source domain.FileTarget, code, reason string) ports.ImportRejection {
	code = strings.TrimSpace(code)
	if code == "" {
		code = "native_rejection"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "native preview rejected the file"
	}
	return ports.ImportRejection{Source: source, Code: code, Reason: reason}
}

func addReason(reasons *[]string, reason string) {
	if reason == "" || len(*reasons) >= maxHistoryReasonCode {
		return
	}
	for _, existing := range *reasons {
		if existing == reason {
			return
		}
	}
	*reasons = append(*reasons, reason)
}

func timePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func cloneRootPaths(input map[domain.ConfigID]string) map[domain.ConfigID]string {
	if len(input) == 0 {
		return nil
	}
	output := make(map[domain.ConfigID]string, len(input))
	for id, value := range input {
		output[id] = value
	}
	return output
}

func (client *Client) mapPath(remote string) (domain.FileTarget, bool, bool) {
	remote = normalizeRemotePath(remote)
	if remote == "" {
		return domain.FileTarget{}, false, false
	}
	bestLength := -1
	var selected domain.PathMapping
	ambiguous := false
	for _, mapping := range client.config.Mappings {
		if mapping.ConnectionID != client.config.ConnectionID {
			continue
		}
		prefix := normalizeMappingPrefix(mapping.SourcePrefix)
		if !pathBoundaryMatch(remote, prefix) {
			continue
		}
		if len(prefix) > bestLength {
			bestLength = len(prefix)
			selected = mapping
			ambiguous = false
			continue
		}
		if len(prefix) == bestLength && (selected.RootID != mapping.RootID || selected.DestinationPrefix != mapping.DestinationPrefix) {
			ambiguous = true
		}
	}
	if ambiguous || bestLength < 0 {
		return domain.FileTarget{}, false, ambiguous
	}
	prefix := normalizeMappingPrefix(selected.SourcePrefix)
	suffix := strings.TrimPrefix(remote, prefix)
	suffix = strings.TrimPrefix(suffix, "/")
	relative := strings.Trim(selected.DestinationPrefix, "/")
	if suffix != "" {
		if relative == "" {
			relative = suffix
		} else {
			relative = path.Join(relative, suffix)
		}
	}
	target := domain.FileTarget{RootID: selected.RootID, RelativePath: relative}
	if err := target.Validate(); err != nil {
		return domain.FileTarget{}, false, false
	}
	return target, true, false
}

func validateMappings(connectionID domain.ConfigID, mappings []domain.PathMapping) error {
	for _, mapping := range mappings {
		if mapping.ConnectionID != connectionID {
			continue
		}
		prefix := normalizeMappingPrefix(mapping.SourcePrefix)
		if !mapping.RootID.Valid() || !absoluteRemotePath(prefix) {
			return errors.New("Arr path mapping is invalid")
		}
		if mapping.DestinationPrefix != "" {
			if err := domain.ValidateRelativePath(strings.Trim(mapping.DestinationPrefix, "/")); err != nil {
				return errors.New("Arr path mapping destination is invalid")
			}
		}
	}
	return nil
}

func normalizeRemotePath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	if value == "" || strings.ContainsRune(value, 0) {
		return ""
	}
	clean := cleanArrPath(value)
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return ""
	}
	return clean
}

func normalizeMappingPrefix(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	if value == "" {
		return ""
	}
	clean := cleanArrPath(value)
	if len(clean) == 3 && clean[1] == ':' && clean[2] == '/' {
		return clean
	}
	return strings.TrimRight(clean, "/")
}

func cleanArrPath(value string) string {
	if len(value) >= 2 && value[1] == ':' {
		drive := value[:2]
		rest := strings.TrimPrefix(value[2:], "/")
		if rest == "" {
			return drive + "/"
		}
		clean := path.Clean("/" + rest)
		if clean == "/" {
			return drive + "/"
		}
		return drive + clean
	}
	return path.Clean(value)
}

func absoluteRemotePath(value string) bool {
	return strings.HasPrefix(value, "/") || (len(value) >= 3 && value[1] == ':' && value[2] == '/')
}

func pathBoundaryMatch(value, prefix string) bool {
	if value == "" || prefix == "" {
		return false
	}
	if prefix == "/" {
		return strings.HasPrefix(value, "/")
	}
	if len(prefix) == 3 && prefix[1] == ':' && prefix[2] == '/' {
		return strings.HasPrefix(value, prefix)
	}
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}

func absoluteFilePath(roots map[domain.ConfigID]string, target domain.FileTarget) string {
	if err := target.Validate(); err != nil {
		return ""
	}
	root, ok := roots[target.RootID]
	if !ok || !absoluteRemotePath(normalizeRemotePath(root)) {
		return ""
	}
	return path.Join(root, target.RelativePath)
}
