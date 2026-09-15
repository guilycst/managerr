// Package sonarr implements the small, read-only Sonarr v3 compatibility
// boundary used by Mastarr. It owns transport, API-key authentication and
// upstream DTO normalization; it has no dependency on the Mastarr root module.
package sonarr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	generated "github.com/guilycst/mastarr/clients/sonarr/internal/generated"
)

const (
	DefaultMaxResponseBytes int64 = 8 << 20
	DefaultMaxItems               = 10_000
	DefaultMaxEpisodes            = 50_000
	DefaultMaxFiles               = 50_000
	DefaultMaxRejections          = 10_000
	DefaultRequestTimeout         = 15 * time.Second

	maxAPIKeyChars       = 512
	maxUserAgentChars    = 128
	maxVersionChars      = 128
	maxTextChars         = 4096
	maxTitleChars        = 1024
	maxProviderIDChars   = 256
	maxDownloadIDChars   = 512
	maxRejectionCode     = 256
	maxRejectionMessage  = 2048
	maxQualityNameChars  = 256
	maxLanguageNameChars = 128
	maxSeriesTypeChars   = 64
)

const (
	apiPrefix       = "/api/v3"
	apiSystemStatus = "/system/status"
	apiSeries       = "/series"
	apiRootFolder   = "/rootfolder"
	apiQuality      = "/qualityprofile"
	apiEpisode      = "/episode"
	apiEpisodeFile  = "/episodefile"
	apiManualImport = "/manualimport"
)

var errResponseTooLarge = errors.New("response exceeds configured bound")

// ErrorCode is a stable, sanitized class for an upstream failure.
type ErrorCode string

const (
	ErrorUnavailable      ErrorCode = "unavailable"
	ErrorRateLimited      ErrorCode = "rate_limited"
	ErrorUnauthorized     ErrorCode = "unauthorized"
	ErrorForbidden        ErrorCode = "forbidden"
	ErrorInvalidInput     ErrorCode = "invalid_input"
	ErrorNotFound         ErrorCode = "not_found"
	ErrorConflict         ErrorCode = "conflict"
	ErrorUnsupported      ErrorCode = "unsupported"
	ErrorMalformed        ErrorCode = "malformed_response"
	ErrorResponseTooLarge ErrorCode = "response_too_large"
	ErrorUnknown          ErrorCode = "unknown"
)

// UpstreamError never contains a URL, API key or response body.
type UpstreamError struct {
	Code      ErrorCode
	Operation string
	Status    int
	Retryable bool
}

func (err UpstreamError) Error() string {
	operation := err.Operation
	if operation == "" {
		operation = "request"
	}
	if err.Status > 0 {
		return fmt.Sprintf("Sonarr %s failed (%s, status %d)", operation, err.Code, err.Status)
	}
	return fmt.Sprintf("Sonarr %s failed (%s)", operation, err.Code)
}

// IsCode reports whether err or a wrapped error carries code.
func IsCode(err error, code ErrorCode) bool {
	var upstream UpstreamError
	return errors.As(err, &upstream) && upstream.Code == code
}

// Config controls one Sonarr connection. Endpoint is the server or reverse
// proxy prefix; this client appends /api/v3. APIKey is held in memory and sent
// only in X-Api-Key headers.
type Config struct {
	Endpoint         string
	APIKey           string
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
	MaxResponseBytes int64
	MaxItems         int
	MaxEpisodes      int
	MaxFiles         int
	MaxRejections    int
	UserAgent        string
}

// Client is an independent, read-only Sonarr API client.
type Client struct {
	endpoint         *url.URL
	httpClient       *http.Client
	requestTimeout   time.Duration
	maxResponseBytes int64
	maxItems         int
	maxEpisodes      int
	maxFiles         int
	maxRejections    int
	apiKey           string
	userAgent        string
}

// New validates configuration without contacting Sonarr.
func New(config Config) (*Client, error) {
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if !validBoundedText(config.APIKey, maxAPIKeyChars, true) || strings.TrimSpace(config.APIKey) != config.APIKey {
		return nil, errors.New("Sonarr API key is invalid")
	}

	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	if maxResponseBytes < 1 || maxResponseBytes >= math.MaxInt64 {
		return nil, errors.New("Sonarr response bound is invalid")
	}
	maxItems := config.MaxItems
	if maxItems <= 0 {
		maxItems = DefaultMaxItems
	}
	maxEpisodes := config.MaxEpisodes
	if maxEpisodes <= 0 {
		maxEpisodes = DefaultMaxEpisodes
	}
	maxFiles := config.MaxFiles
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}
	maxRejections := config.MaxRejections
	if maxRejections <= 0 {
		maxRejections = DefaultMaxRejections
	}
	if maxItems > DefaultMaxItems || maxEpisodes > DefaultMaxEpisodes || maxFiles > DefaultMaxFiles || maxRejections > DefaultMaxRejections {
		return nil, errors.New("Sonarr item bound exceeds compatibility ceiling")
	}
	requestTimeout := config.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = DefaultRequestTimeout
	}
	if requestTimeout < 0 {
		return nil, errors.New("Sonarr request timeout cannot be negative")
	}
	userAgent := config.UserAgent
	if userAgent == "" {
		userAgent = "mastarr-sonarr-client/0.0.1"
	}
	if !validBoundedText(userAgent, maxUserAgentChars, true) || strings.TrimSpace(userAgent) != userAgent {
		return nil, errors.New("Sonarr user agent is invalid")
	}

	baseClient := http.DefaultClient
	if config.HTTPClient != nil {
		baseClient = config.HTTPClient
	}
	clientCopy := *baseClient
	clientCopy.Jar = nil
	// Never replay an API-key-bearing request at a redirect target. The
	// response is returned to request so its 3xx status can be classified.
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{
		endpoint:         endpoint,
		httpClient:       &clientCopy,
		requestTimeout:   requestTimeout,
		maxResponseBytes: maxResponseBytes,
		maxItems:         maxItems,
		maxEpisodes:      maxEpisodes,
		maxFiles:         maxFiles,
		maxRejections:    maxRejections,
		apiKey:           config.APIKey,
		userAgent:        userAgent,
	}, nil
}

// NewClient is an explicit constructor alias.
func NewClient(config Config) (*Client, error) { return New(config) }

// Endpoint returns the configured endpoint without credentials.
func (client *Client) Endpoint() string {
	if client == nil || client.endpoint == nil {
		return ""
	}
	return client.endpoint.String()
}

// String returns a safe diagnostic representation. The API key is deliberately
// omitted so ordinary formatted logging cannot disclose credentials.
func (client *Client) String() string {
	return "SonarrClient{" + client.Endpoint() + "}"
}

// Coverage describes the completeness of one full-array Sonarr snapshot.
// Sonarr v3 returns these resources as complete arrays rather than paged
// responses. The client therefore rejects an over-bound array and only emits
// Complete after retaining every record.
type Coverage struct {
	Completeness  string
	ObservedCount int
	ObservedAt    time.Time
	Reason        string
}

const (
	CompletenessComplete = "complete"
	CompletenessUnknown  = "unknown"
)

// Page is a bounded full-array observation. NextCursor is always empty for
// this module because Sonarr's native read endpoints have no cursor contract;
// callers must start a new observation instead of pretending a later request
// is a continuation of an earlier mutable array.
type Page[T any] struct {
	Items      []T
	NextCursor string
	Coverage   Coverage
}

// SystemStatus is the typed status/version observation needed for capability
// and compatibility checks.
type SystemStatus struct {
	Version      string
	Branch       string
	AppName      string
	InstanceName string
	StartTime    string
	ObservedAt   time.Time
}

// Version reads the Sonarr version from /system/status.
func (client *Client) Version(ctx context.Context) (string, error) {
	status, err := client.Status(ctx)
	if err != nil {
		return "", err
	}
	return status.Version, nil
}

// Status reads /system/status.
func (client *Client) Status(ctx context.Context) (SystemStatus, error) {
	body, err := client.get(ctx, "sonarr.system.status", apiSystemStatus, nil)
	if err != nil {
		return SystemStatus{}, err
	}
	if err := requireObjectFields(body, "version"); err != nil {
		return SystemStatus{}, malformed("sonarr.system.status")
	}
	var value generated.SystemStatus
	if err := decodeJSON(body, &value); err != nil {
		return SystemStatus{}, malformed("sonarr.system.status")
	}
	if err := validateVersion(value.Version); err != nil {
		return SystemStatus{}, malformed("sonarr.system.status")
	}
	result := SystemStatus{Version: value.Version, ObservedAt: time.Now().UTC()}
	if value.Branch != nil {
		if err := validateText(*value.Branch, maxVersionChars, false); err != nil {
			return SystemStatus{}, malformed("sonarr.system.status")
		}
		result.Branch = *value.Branch
	}
	if value.AppName != nil {
		if err := validateText(*value.AppName, maxVersionChars, false); err != nil {
			return SystemStatus{}, malformed("sonarr.system.status")
		}
		result.AppName = *value.AppName
	}
	if value.InstanceName != nil {
		if err := validateText(*value.InstanceName, maxTextChars, false); err != nil {
			return SystemStatus{}, malformed("sonarr.system.status")
		}
		result.InstanceName = *value.InstanceName
	}
	if value.StartTime != nil {
		if err := validateText(*value.StartTime, maxTextChars, false); err != nil {
			return SystemStatus{}, malformed("sonarr.system.status")
		}
		result.StartTime = *value.StartTime
	}
	return result, nil
}

// GetSystemStatus is an explicit method-name alias for Status.
func (client *Client) GetSystemStatus(ctx context.Context) (SystemStatus, error) {
	return client.Status(ctx)
}

// Series is one normalized Sonarr catalog record.
type Series struct {
	ID           int64
	Title        string
	Year         *int32
	Path         string
	Monitored    bool
	SeasonFolder *bool
	SeriesType   string
	TVDBID       *int64
	TVMazeID     *int64
	IMDBID       string
	ProviderIDs  map[string]string
}

// ListSeries reads the complete /series array and reports complete coverage.
func (client *Client) ListSeries(ctx context.Context) (Page[Series], error) {
	body, err := client.get(ctx, "sonarr.series.list", apiSeries, nil)
	if err != nil {
		return Page[Series]{}, err
	}
	if err := requireArrayObjectFields(body, "id", "title", "path", "monitored"); err != nil {
		return Page[Series]{}, malformed("sonarr.series.list")
	}
	var values []generated.Series
	if err := decodeJSON(body, &values); err != nil {
		return Page[Series]{}, malformed("sonarr.series.list")
	}
	if len(values) > client.maxItems {
		return Page[Series]{}, tooLarge("sonarr.series.list", client.maxItems)
	}
	items := make([]Series, len(values))
	seen := make(map[int64]struct{}, len(values))
	for index, value := range values {
		item, err := normalizeSeries(value)
		if err != nil {
			return Page[Series]{}, malformed("sonarr.series.list")
		}
		if _, exists := seen[item.ID]; exists {
			return Page[Series]{}, malformed("sonarr.series.list")
		}
		seen[item.ID] = struct{}{}
		items[index] = item
	}
	return completePage(items), nil
}

// SeriesCatalog is a concise alias for ListSeries.
func (client *Client) SeriesCatalog(ctx context.Context) (Page[Series], error) {
	return client.ListSeries(ctx)
}

// GetSeries reads one series by native ID.
func (client *Client) GetSeries(ctx context.Context, id int64) (Series, error) {
	if id <= 0 {
		return Series{}, invalidInput("sonarr.series.get")
	}
	body, err := client.get(ctx, "sonarr.series.get", fmt.Sprintf("%s/%d", apiSeries, id), nil)
	if err != nil {
		return Series{}, err
	}
	if err := requireObjectFields(body, "id", "title", "path", "monitored"); err != nil {
		return Series{}, malformed("sonarr.series.get")
	}
	var value generated.Series
	if err := decodeJSON(body, &value); err != nil {
		return Series{}, malformed("sonarr.series.get")
	}
	item, err := normalizeSeries(value)
	if err != nil || item.ID != id {
		return Series{}, malformed("sonarr.series.get")
	}
	return item, nil
}

// RootFolder is one configured Sonarr root path.
type RootFolder struct {
	ID         int64
	Path       string
	Accessible bool
	FreeSpace  *int64
}

// ListRootFolders reads configured root folders.
func (client *Client) ListRootFolders(ctx context.Context) (Page[RootFolder], error) {
	body, err := client.get(ctx, "sonarr.rootfolder.list", apiRootFolder, nil)
	if err != nil {
		return Page[RootFolder]{}, err
	}
	if err := requireArrayObjectFields(body, "id", "path", "accessible"); err != nil {
		return Page[RootFolder]{}, malformed("sonarr.rootfolder.list")
	}
	var values []generated.RootFolder
	if err := decodeJSON(body, &values); err != nil {
		return Page[RootFolder]{}, malformed("sonarr.rootfolder.list")
	}
	if len(values) > client.maxItems {
		return Page[RootFolder]{}, tooLarge("sonarr.rootfolder.list", client.maxItems)
	}
	items := make([]RootFolder, len(values))
	seen := make(map[int64]struct{}, len(values))
	for index, value := range values {
		if value.Id <= 0 || validateNativePath(value.Path) != nil {
			return Page[RootFolder]{}, malformed("sonarr.rootfolder.list")
		}
		if _, exists := seen[value.Id]; exists {
			return Page[RootFolder]{}, malformed("sonarr.rootfolder.list")
		}
		seen[value.Id] = struct{}{}
		var freeSpace *int64
		if value.FreeSpace != nil {
			if *value.FreeSpace < 0 {
				return Page[RootFolder]{}, malformed("sonarr.rootfolder.list")
			}
			copyValue := *value.FreeSpace
			freeSpace = &copyValue
		}
		items[index] = RootFolder{ID: value.Id, Path: value.Path, Accessible: value.Accessible, FreeSpace: freeSpace}
	}
	return completePage(items), nil
}

// QualityProfile is one configured Sonarr quality profile.
type QualityProfile struct {
	ID   int64
	Name string
}

// ListQualityProfiles reads configured quality profiles.
func (client *Client) ListQualityProfiles(ctx context.Context) (Page[QualityProfile], error) {
	body, err := client.get(ctx, "sonarr.qualityprofile.list", apiQuality, nil)
	if err != nil {
		return Page[QualityProfile]{}, err
	}
	if err := requireArrayObjectFields(body, "id", "name"); err != nil {
		return Page[QualityProfile]{}, malformed("sonarr.qualityprofile.list")
	}
	var values []generated.QualityProfile
	if err := decodeJSON(body, &values); err != nil {
		return Page[QualityProfile]{}, malformed("sonarr.qualityprofile.list")
	}
	if len(values) > client.maxItems {
		return Page[QualityProfile]{}, tooLarge("sonarr.qualityprofile.list", client.maxItems)
	}
	items := make([]QualityProfile, len(values))
	seen := make(map[int64]struct{}, len(values))
	for index, value := range values {
		if value.Id <= 0 || validateText(value.Name, maxTextChars, true) != nil {
			return Page[QualityProfile]{}, malformed("sonarr.qualityprofile.list")
		}
		if _, exists := seen[value.Id]; exists {
			return Page[QualityProfile]{}, malformed("sonarr.qualityprofile.list")
		}
		seen[value.Id] = struct{}{}
		items[index] = QualityProfile{ID: value.Id, Name: value.Name}
	}
	return completePage(items), nil
}

// EpisodeFile is the normalized file association returned by Sonarr.
type EpisodeFile struct {
	ID           int64
	SeriesID     int64
	Path         string
	RelativePath string
	Size         int64
	DateAdded    string
	EpisodeIDs   []int64
}

// Episode is one episode observation. EpisodeFile is present when Sonarr
// returned nested file evidence; callers needing authoritative association
// should request includeEpisodeFile=true.
type Episode struct {
	ID                         int64
	SeriesID                   int64
	EpisodeFileID              *int64
	SeasonNumber               int32
	EpisodeNumber              int32
	AbsoluteEpisodeNumber      *int32
	SceneAbsoluteEpisodeNumber *int32
	Title                      string
	HasFile                    bool
	EpisodeFile                *EpisodeFile
}

// ListEpisodes reads all episodes for seriesID. The includeEpisodeFile flag
// is sent explicitly so callers cannot accidentally rely on Sonarr's false
// default when association evidence is required.
func (client *Client) ListEpisodes(ctx context.Context, seriesID int64, includeEpisodeFile bool) (Page[Episode], error) {
	if seriesID <= 0 {
		return Page[Episode]{}, invalidInput("sonarr.episode.list")
	}
	query := url.Values{
		"seriesId":           []string{fmt.Sprintf("%d", seriesID)},
		"includeEpisodeFile": []string{fmt.Sprintf("%t", includeEpisodeFile)},
	}
	body, err := client.get(ctx, "sonarr.episode.list", apiEpisode, query)
	if err != nil {
		return Page[Episode]{}, err
	}
	if err := requireArrayObjectFields(body, "id", "seriesId", "seasonNumber", "episodeNumber", "hasFile"); err != nil {
		return Page[Episode]{}, malformed("sonarr.episode.list")
	}
	if err := validateEpisodeShape(body); err != nil {
		return Page[Episode]{}, malformed("sonarr.episode.list")
	}
	var values []generated.Episode
	if err := decodeJSON(body, &values); err != nil {
		return Page[Episode]{}, malformed("sonarr.episode.list")
	}
	if len(values) > client.maxEpisodes {
		return Page[Episode]{}, tooLarge("sonarr.episode.list", client.maxEpisodes)
	}
	items := make([]Episode, len(values))
	seen := make(map[int64]struct{}, len(values))
	for index, value := range values {
		item, err := normalizeEpisode(value, includeEpisodeFile)
		if err != nil || item.SeriesID != seriesID {
			return Page[Episode]{}, malformed("sonarr.episode.list")
		}
		if _, exists := seen[item.ID]; exists {
			return Page[Episode]{}, malformed("sonarr.episode.list")
		}
		seen[item.ID] = struct{}{}
		items[index] = item
	}
	return completePage(items), nil
}

// ListEpisodesWithFiles requests the association-bearing Sonarr form.
func (client *Client) ListEpisodesWithFiles(ctx context.Context, seriesID int64) (Page[Episode], error) {
	return client.ListEpisodes(ctx, seriesID, true)
}

// ListEpisodeFiles reads all episode files for seriesID.
func (client *Client) ListEpisodeFiles(ctx context.Context, seriesID int64) (Page[EpisodeFile], error) {
	if seriesID <= 0 {
		return Page[EpisodeFile]{}, invalidInput("sonarr.episodefile.list")
	}
	query := url.Values{"seriesId": []string{fmt.Sprintf("%d", seriesID)}}
	body, err := client.get(ctx, "sonarr.episodefile.list", apiEpisodeFile, query)
	if err != nil {
		return Page[EpisodeFile]{}, err
	}
	if err := requireArrayObjectFields(body, "id", "seriesId", "path", "size"); err != nil {
		return Page[EpisodeFile]{}, malformed("sonarr.episodefile.list")
	}
	var values []generated.EpisodeFile
	if err := decodeJSON(body, &values); err != nil {
		return Page[EpisodeFile]{}, malformed("sonarr.episodefile.list")
	}
	if len(values) > client.maxFiles {
		return Page[EpisodeFile]{}, tooLarge("sonarr.episodefile.list", client.maxFiles)
	}
	items := make([]EpisodeFile, len(values))
	seen := make(map[int64]struct{}, len(values))
	for index, value := range values {
		item, err := normalizeEpisodeFile(value, seriesID)
		if err != nil {
			return Page[EpisodeFile]{}, malformed("sonarr.episodefile.list")
		}
		if _, exists := seen[item.ID]; exists {
			return Page[EpisodeFile]{}, malformed("sonarr.episodefile.list")
		}
		seen[item.ID] = struct{}{}
		items[index] = item
	}
	return completePage(items), nil
}

// ManualImportQuery describes a native preview read. Folder is passed to
// Sonarr exactly as configured by the root adapter; this module does no path
// mapping or filesystem access.
type ManualImportQuery struct {
	Folder              string
	FilterExistingFiles bool
	// SeriesID is retained only to make the mode switch explicit to callers
	// upgrading from the first draft. It is rejected by PreviewManualImport;
	// use PreviewLibraryImport for Sonarr's native series mode.
	SeriesID   *int64
	DownloadID string
}

// LibraryImportQuery selects Sonarr's registered-library manual-import mode.
// It is intentionally separate from ManualImportQuery: Sonarr ignores the
// folder argument when seriesId is supplied and scans the registered series
// path instead.
type LibraryImportQuery struct {
	SeriesID            int64
	SeasonNumber        *int32
	FilterExistingFiles bool
}

// ManualImportCandidate is a typed native preview candidate. Rejections and
// optional associations remain visible so callers can require explicit review.
type ManualImportCandidate struct {
	ID            int64
	Path          string
	RelativePath  string
	FolderName    string
	Name          string
	Size          int64
	Series        *SeriesReference
	SeasonNumber  *int32
	Episodes      []EpisodeReference
	EpisodeFileID *int64
	ReleaseGroup  string
	Quality       *Quality
	// Language preserves Sonarr 3.x's native singular language member.
	Language          *Language
	Languages         []Language
	DownloadID        string
	ReleaseType       string
	CustomFormatScore *int32
	IndexerFlags      *int32
	Forced            *bool
	HearingImpaired   *bool
	Rejections        []ImportRejection
}

// SeriesReference is the identity-bearing nested series object in native
// manual-import resources.
type SeriesReference struct {
	ID       int64
	Title    string
	TVDBID   *int64
	TVMazeID *int64
}

// EpisodeReference is one exact native episode association.
type EpisodeReference struct {
	ID                         int64
	SeriesID                   int64
	EpisodeFileID              *int64
	SeasonNumber               int32
	EpisodeNumber              int32
	AbsoluteEpisodeNumber      *int32
	SceneAbsoluteEpisodeNumber *int32
}

// Language preserves Sonarr's typed language ID/name pair.
type Language struct {
	ID   int32
	Name string
}

// Quality preserves only the typed quality and revision fields used by
// reconciliation. Arbitrary upstream properties remain inside generated DTOs.
type Quality struct {
	Quality  *QualityDetails
	Revision *QualityRevision
}

type QualityDetails struct {
	ID         *int32
	Name       string
	Source     string
	Resolution *int32
}

type QualityRevision struct {
	Version  *int32
	Real     *int32
	IsRepack *bool
}

// ImportRejection preserves native type/reason evidence. Message is retained
// as an explicit compatibility alias for older Sonarr response shapes.
type ImportRejection struct {
	Type   string
	Reason string
	// Message is retained as a compatibility alias. For Sonarr 3.x it mirrors
	// Reason, while older plural/message fixtures are normalized into both.
	Message string
}

// PreviewManualImport performs only Sonarr's downloaded-folder GET
// /manualimport mode. A series ID is rejected because Sonarr interprets it as
// the registered-library overload and ignores the requested folder.
func (client *Client) PreviewManualImport(ctx context.Context, query ManualImportQuery) (Page[ManualImportCandidate], error) {
	if validateNativePath(query.Folder) != nil {
		return Page[ManualImportCandidate]{}, invalidInput("sonarr.manualimport.preview")
	}
	if query.SeriesID != nil {
		return Page[ManualImportCandidate]{}, invalidInput("sonarr.manualimport.preview.mode")
	}
	values := url.Values{
		"folder":              []string{query.Folder},
		"filterExistingFiles": []string{fmt.Sprintf("%t", query.FilterExistingFiles)},
	}
	if query.DownloadID != "" {
		if validateBoundedID(query.DownloadID, maxDownloadIDChars) != nil {
			return Page[ManualImportCandidate]{}, invalidInput("sonarr.manualimport.preview")
		}
		values.Set("downloadId", query.DownloadID)
	}
	return client.previewManualImport(ctx, "sonarr.manualimport.preview.folder", values, 0)
}

// PreviewLibraryImport performs Sonarr's registered-library manual-import
// mode. The native request contains seriesId and optional seasonNumber, with
// no folder or downloadId; the returned series and every episode association
// are checked against the requested series.
func (client *Client) PreviewLibraryImport(ctx context.Context, query LibraryImportQuery) (Page[ManualImportCandidate], error) {
	if query.SeriesID <= 0 {
		return Page[ManualImportCandidate]{}, invalidInput("sonarr.manualimport.preview.library")
	}
	values := url.Values{
		"seriesId":            []string{fmt.Sprintf("%d", query.SeriesID)},
		"filterExistingFiles": []string{fmt.Sprintf("%t", query.FilterExistingFiles)},
	}
	if query.SeasonNumber != nil {
		if *query.SeasonNumber < 0 {
			return Page[ManualImportCandidate]{}, invalidInput("sonarr.manualimport.preview.library")
		}
		values.Set("seasonNumber", fmt.Sprintf("%d", *query.SeasonNumber))
	}
	return client.previewManualImport(ctx, "sonarr.manualimport.preview.library", values, query.SeriesID)
}

func (client *Client) previewManualImport(ctx context.Context, operation string, values url.Values, expectedSeriesID int64) (Page[ManualImportCandidate], error) {
	body, err := client.get(ctx, operation, apiManualImport, values)
	if err != nil {
		return Page[ManualImportCandidate]{}, err
	}
	if err := requireArrayObjectFields(body, "id", "path", "relativePath", "name", "size"); err != nil {
		return Page[ManualImportCandidate]{}, malformed(operation)
	}
	if err := validateManualImportShape(body); err != nil {
		return Page[ManualImportCandidate]{}, malformed(operation)
	}
	var valuesDTO []generated.ManualImportResource
	if err := decodeJSON(body, &valuesDTO); err != nil {
		return Page[ManualImportCandidate]{}, malformed(operation)
	}
	if len(valuesDTO) > client.maxFiles {
		return Page[ManualImportCandidate]{}, tooLarge(operation, client.maxFiles)
	}
	items := make([]ManualImportCandidate, len(valuesDTO))
	seen := make(map[string]struct{}, len(valuesDTO))
	for index, value := range valuesDTO {
		item, err := normalizeManualImport(value, client.maxRejections, expectedSeriesID)
		if err != nil {
			return Page[ManualImportCandidate]{}, malformed(operation)
		}
		identity := item.Path + "\x00" + item.RelativePath
		if _, exists := seen[identity]; exists {
			return Page[ManualImportCandidate]{}, malformed(operation)
		}
		seen[identity] = struct{}{}
		items[index] = item
	}
	return completePage(items), nil
}

// PreviewImport is a concise alias for PreviewManualImport.
func (client *Client) PreviewImport(ctx context.Context, query ManualImportQuery) (Page[ManualImportCandidate], error) {
	return client.PreviewManualImport(ctx, query)
}

func completePage[T any](items []T) Page[T] {
	return Page[T]{Items: items, Coverage: Coverage{Completeness: CompletenessComplete, ObservedCount: len(items), ObservedAt: time.Now().UTC()}}
}

func (client *Client) get(ctx context.Context, operation, endpoint string, query url.Values) ([]byte, error) {
	requestContext, cancel := client.requestContext(ctx)
	defer cancel()
	if err := requestContext.Err(); err != nil {
		return nil, err
	}
	requestURL := *client.endpoint
	requestURL.Path = strings.TrimRight(client.endpoint.Path, "/") + apiPrefix + endpoint
	requestURL.RawPath = ""
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, invalidInput(operation)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Api-Key", client.apiKey)
	request.Header.Set("User-Agent", client.userAgent)
	response, err := client.httpClient.Do(request)
	if err != nil {
		if contextErr := requestContext.Err(); contextErr != nil {
			return nil, contextErr
		}
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, UpstreamError{Code: ErrorUnavailable, Operation: operation, Retryable: true}
		}
		return nil, UpstreamError{Code: ErrorUnavailable, Operation: operation, Retryable: true}
	}
	defer response.Body.Close()
	body, readErr := readBounded(response.Body, client.maxResponseBytes)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		// Status is authoritative even when an error page exceeds the retention
		// bound. Never include body text in the returned error.
		return nil, statusError(operation, response.StatusCode)
	}
	if readErr != nil {
		if errors.Is(readErr, errResponseTooLarge) {
			return nil, UpstreamError{Code: ErrorResponseTooLarge, Operation: operation, Status: response.StatusCode}
		}
		if contextErr := requestContext.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, UpstreamError{Code: ErrorMalformed, Operation: operation, Status: response.StatusCode}
	}
	return body, nil
}

func (client *Client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client.requestTimeout <= 0 {
		return ctx, func() {}
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, client.requestTimeout)
}

func parseEndpoint(raw string) (*url.URL, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return nil, errors.New("Sonarr endpoint is invalid")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Sonarr endpoint must be an absolute URL without credentials or query")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("Sonarr endpoint scheme is unsupported")
	}
	if !utf8.ValidString(parsed.Host) || strings.Contains(parsed.Path, "\\") || strings.Contains(parsed.Path, "//") || strings.Contains(parsed.Path, "%") || (parsed.RawPath != "" && strings.Contains(parsed.RawPath, "%")) {
		return nil, errors.New("Sonarr endpoint path is ambiguous")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return nil, errors.New("Sonarr endpoint path is ambiguous")
		}
	}
	return parsed, nil
}

func statusError(operation string, status int) UpstreamError {
	code := ErrorUnknown
	retryable := false
	switch {
	case status == http.StatusUnauthorized:
		code = ErrorUnauthorized
	case status == http.StatusForbidden:
		code = ErrorForbidden
	case status == http.StatusBadRequest:
		code = ErrorInvalidInput
	case status == http.StatusNotFound && operation == "sonarr.series.get":
		code = ErrorNotFound
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented:
		code = ErrorUnsupported
	case status == http.StatusConflict:
		code = ErrorConflict
	case status == http.StatusTooManyRequests:
		code, retryable = ErrorRateLimited, true
	case status == http.StatusRequestTimeout || status >= http.StatusInternalServerError:
		code, retryable = ErrorUnavailable, true
	case status >= http.StatusMultipleChoices && status < http.StatusBadRequest:
		code = ErrorUnsupported
	}
	return UpstreamError{Code: code, Operation: operation, Status: status, Retryable: retryable}
}

func invalidInput(operation string) UpstreamError {
	return UpstreamError{Code: ErrorInvalidInput, Operation: operation}
}

func malformed(operation string) UpstreamError {
	return UpstreamError{Code: ErrorMalformed, Operation: operation}
}

func tooLarge(operation string, bound int) UpstreamError {
	return UpstreamError{Code: ErrorResponseTooLarge, Operation: operation, Retryable: false}
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	if reader == nil || maxBytes < 1 || maxBytes >= math.MaxInt64 {
		return nil, errors.New("response bound is invalid")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, errors.New("response body could not be read")
	}
	if int64(len(body)) > maxBytes {
		return nil, errResponseTooLarge
	}
	return body, nil
}

func decodeJSON(data []byte, target any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || !utf8.Valid(data) {
		return errors.New("JSON response is invalid")
	}
	if err := scanJSON(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON response data")
	}
	return nil
}

// scanJSON rejects duplicate object members and deeply nested payloads before
// generated DTO unmarshalling. Sonarr is allowed to add fields, so unknown
// members are retained only in generated AdditionalProperties and never
// copied into normalized observations.
func scanJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder, 0); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON response data")
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON response nesting exceeds bound")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return nil
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("JSON member name is invalid")
			}
			if _, exists := seen[name]; exists {
				return errors.New("duplicate JSON object member")
			}
			seen[name] = struct{}{}
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("JSON object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("JSON array is not closed")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func requireObjectFields(data []byte, fields ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return errors.New("JSON object is required")
	}
	for _, field := range fields {
		raw, ok := object[field]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.New("required JSON member is missing")
		}
	}
	return nil
}

func requireArrayObjectFields(data []byte, fields ...string) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil || values == nil {
		return errors.New("JSON array is required")
	}
	for _, value := range values {
		if err := requireObjectFields(value, fields...); err != nil {
			return err
		}
	}
	return nil
}

// validateEpisodeShape validates required fields in nested episode-file
// objects before generated scalar fields can default omitted numbers to zero.
// The top-level endpoint is still an array snapshot, so one malformed nested
// association invalidates the complete observation.
func validateEpisodeShape(data []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil || values == nil {
		return errors.New("episode array is required")
	}
	for _, raw := range values {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return errors.New("episode object is required")
		}
		episodeFile, present := object["episodeFile"]
		if !present {
			continue
		}
		if err := requireObjectFields(episodeFile, "id", "seriesId", "path", "size"); err != nil {
			return err
		}
	}
	return nil
}

// validateManualImportShape checks every nested object whose scalar fields
// carry identity or association meaning. Sonarr responses add fields over
// time, so unknown members remain allowed by the compatibility contract, but
// missing or null required association fields fail closed.
func validateManualImportShape(data []byte) error {
	if err := scanJSON(data); err != nil {
		return err
	}
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil || values == nil {
		return errors.New("manual import array is required")
	}
	for _, raw := range values {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return errors.New("manual import object is required")
		}
		if nested, present := object["series"]; present && !isJSONNull(nested) {
			if err := requireObjectFields(nested, "id", "title"); err != nil {
				return err
			}
		}
		if nested, present := object["episodes"]; present {
			if isJSONNull(nested) {
				return errors.New("manual import episodes cannot be null")
			}
			var episodes []json.RawMessage
			if err := json.Unmarshal(nested, &episodes); err != nil || episodes == nil {
				return errors.New("manual import episodes array is required")
			}
			for _, episode := range episodes {
				if err := requireObjectFields(episode, "id", "seriesId", "seasonNumber", "episodeNumber"); err != nil {
					return err
				}
				var episodeObject map[string]json.RawMessage
				if err := json.Unmarshal(episode, &episodeObject); err != nil {
					return err
				}
				if value, present := episodeObject["episodeFileId"]; present && isJSONNull(value) {
					return errors.New("manual import episode file id cannot be null")
				}
			}
		}
		if nested, present := object["language"]; present && !isJSONNull(nested) {
			if err := requireObjectFields(nested, "id", "name"); err != nil {
				return err
			}
		}
		if nested, present := object["languages"]; present {
			if isJSONNull(nested) {
				return errors.New("manual import languages cannot be null")
			}
			var languages []json.RawMessage
			if err := json.Unmarshal(nested, &languages); err != nil || languages == nil {
				return errors.New("manual import languages array is required")
			}
			for _, language := range languages {
				if err := requireObjectFields(language, "id", "name"); err != nil {
					return err
				}
			}
		}
		if nested, present := object["rejections"]; present {
			if isJSONNull(nested) {
				return errors.New("manual import rejections cannot be null")
			}
			var rejections []json.RawMessage
			if err := json.Unmarshal(nested, &rejections); err != nil || rejections == nil {
				return errors.New("manual import rejections array is required")
			}
			for _, rejection := range rejections {
				var rejectionObject map[string]json.RawMessage
				if err := json.Unmarshal(rejection, &rejectionObject); err != nil || rejectionObject == nil {
					return errors.New("manual import rejection object is required")
				}
				if value, present := rejectionObject["type"]; !present || isJSONNull(value) {
					return errors.New("manual import rejection type is required")
				}
				reason, hasReason := rejectionObject["reason"]
				message, hasMessage := rejectionObject["message"]
				if (!hasReason || isJSONNull(reason)) && (!hasMessage || isJSONNull(message)) {
					return errors.New("manual import rejection reason is required")
				}
			}
		}
	}
	return nil
}

func isJSONNull(value []byte) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func normalizeSeries(value generated.Series) (Series, error) {
	if value.Id <= 0 || validateText(value.Title, maxTitleChars, true) != nil || validateNativePath(value.Path) != nil {
		return Series{}, errors.New("series identity is invalid")
	}
	result := Series{ID: value.Id, Title: value.Title, Path: value.Path, Monitored: value.Monitored}
	if value.Year != nil {
		copyValue := *value.Year
		result.Year = &copyValue
	}
	if value.SeasonFolder != nil {
		copyValue := *value.SeasonFolder
		result.SeasonFolder = &copyValue
	}
	if value.SeriesType != nil {
		if validateText(*value.SeriesType, maxSeriesTypeChars, false) != nil {
			return Series{}, errors.New("series type is invalid")
		}
		result.SeriesType = *value.SeriesType
	}
	if value.TvdbId != nil {
		if *value.TvdbId < 0 {
			return Series{}, errors.New("series tvdb id is invalid")
		}
		copyValue := *value.TvdbId
		result.TVDBID = &copyValue
	}
	if value.TvMazeId != nil {
		if *value.TvMazeId < 0 {
			return Series{}, errors.New("series tvmaze id is invalid")
		}
		copyValue := *value.TvMazeId
		result.TVMazeID = &copyValue
	}
	if value.ImdbId != nil {
		if validateText(*value.ImdbId, maxProviderIDChars, false) != nil {
			return Series{}, errors.New("series imdb id is invalid")
		}
		result.IMDBID = *value.ImdbId
	}
	if value.ProviderIds != nil {
		result.ProviderIDs = make(map[string]string, len(*value.ProviderIds))
		for key, providerID := range *value.ProviderIds {
			if validateText(key, maxProviderIDChars, true) != nil || validateText(providerID, maxProviderIDChars, true) != nil {
				return Series{}, errors.New("series provider id is invalid")
			}
			result.ProviderIDs[key] = providerID
		}
	}
	return result, nil
}

func normalizeEpisode(value generated.Episode, includeEpisodeFile bool) (Episode, error) {
	if value.Id <= 0 || value.SeriesId <= 0 || value.SeasonNumber < 0 || value.EpisodeNumber < 0 {
		return Episode{}, errors.New("episode identity is invalid")
	}
	result := Episode{ID: value.Id, SeriesID: value.SeriesId, SeasonNumber: value.SeasonNumber, EpisodeNumber: value.EpisodeNumber, HasFile: value.HasFile}
	if value.Title != nil {
		if validateText(*value.Title, maxTitleChars, false) != nil {
			return Episode{}, errors.New("episode title is invalid")
		}
		result.Title = *value.Title
	}
	if value.AbsoluteEpisodeNumber != nil {
		if *value.AbsoluteEpisodeNumber < 0 {
			return Episode{}, errors.New("absolute episode number is invalid")
		}
		copyValue := *value.AbsoluteEpisodeNumber
		result.AbsoluteEpisodeNumber = &copyValue
	}
	if value.SceneAbsoluteEpisodeNumber != nil {
		if *value.SceneAbsoluteEpisodeNumber < 0 {
			return Episode{}, errors.New("scene absolute episode number is invalid")
		}
		copyValue := *value.SceneAbsoluteEpisodeNumber
		result.SceneAbsoluteEpisodeNumber = &copyValue
	}
	if value.EpisodeFileId != nil {
		if *value.EpisodeFileId < 0 {
			return Episode{}, errors.New("episode file id is invalid")
		}
		copyValue := *value.EpisodeFileId
		result.EpisodeFileID = &copyValue
	}
	if value.EpisodeFile != nil {
		file, err := normalizeEpisodeFile(*value.EpisodeFile, value.SeriesId)
		if err != nil {
			return Episode{}, err
		}
		if result.EpisodeFileID != nil && *result.EpisodeFileID != file.ID {
			return Episode{}, errors.New("episode file identity disagrees")
		}
		if result.EpisodeFileID == nil {
			copyValue := file.ID
			result.EpisodeFileID = &copyValue
		}
		result.EpisodeFile = &file
	}
	if includeEpisodeFile && result.HasFile && result.EpisodeFile == nil {
		return Episode{}, errors.New("episode file evidence is missing")
	}
	return result, nil
}

func normalizeEpisodeFile(value generated.EpisodeFile, expectedSeriesID int64) (EpisodeFile, error) {
	if value.Id <= 0 || value.SeriesId <= 0 || validateNativePath(value.Path) != nil || value.Size < 0 {
		return EpisodeFile{}, errors.New("episode file is invalid")
	}
	if expectedSeriesID > 0 && value.SeriesId != expectedSeriesID {
		return EpisodeFile{}, errors.New("episode file series identity disagrees")
	}
	result := EpisodeFile{ID: value.Id, SeriesID: value.SeriesId, Path: value.Path, Size: value.Size}
	if value.RelativePath != nil {
		if validateText(*value.RelativePath, maxTextChars, false) != nil {
			return EpisodeFile{}, errors.New("episode relative path is invalid")
		}
		result.RelativePath = *value.RelativePath
	}
	if value.DateAdded != nil {
		if validateText(*value.DateAdded, maxVersionChars, false) != nil {
			return EpisodeFile{}, errors.New("episode date is invalid")
		}
		result.DateAdded = *value.DateAdded
	}
	if value.EpisodeIds != nil {
		if len(*value.EpisodeIds) > DefaultMaxEpisodes {
			return EpisodeFile{}, errors.New("episode file association bound exceeded")
		}
		result.EpisodeIDs = make([]int64, len(*value.EpisodeIds))
		seen := make(map[int64]struct{}, len(*value.EpisodeIds))
		for index, id := range *value.EpisodeIds {
			if id <= 0 {
				return EpisodeFile{}, errors.New("episode association is invalid")
			}
			if _, exists := seen[id]; exists {
				return EpisodeFile{}, errors.New("duplicate episode association")
			}
			seen[id] = struct{}{}
			result.EpisodeIDs[index] = id
		}
	}
	return result, nil
}

func normalizeManualImport(value generated.ManualImportResource, maxRejections int, expectedSeriesID int64) (ManualImportCandidate, error) {
	if value.Id < 0 || validateNativePath(value.Path) != nil || validateText(value.RelativePath, maxTextChars, false) != nil || validateText(value.Name, maxTitleChars, true) != nil || value.Size < 0 {
		return ManualImportCandidate{}, errors.New("manual import candidate is invalid")
	}
	result := ManualImportCandidate{ID: value.Id, Path: value.Path, RelativePath: value.RelativePath, Name: value.Name, Size: value.Size}
	if value.FolderName != nil {
		if validateText(*value.FolderName, maxTextChars, false) != nil {
			return ManualImportCandidate{}, errors.New("manual import folder is invalid")
		}
		result.FolderName = *value.FolderName
	}
	if value.Series != nil {
		series, err := normalizeSeriesReference(*value.Series)
		if err != nil {
			return ManualImportCandidate{}, err
		}
		if expectedSeriesID > 0 && series.ID != expectedSeriesID {
			return ManualImportCandidate{}, errors.New("manual import series identity disagrees")
		}
		result.Series = &series
	} else if expectedSeriesID > 0 {
		return ManualImportCandidate{}, errors.New("manual import series evidence is missing")
	}
	if value.SeasonNumber != nil {
		if *value.SeasonNumber < 0 {
			return ManualImportCandidate{}, errors.New("manual import season is invalid")
		}
		copyValue := *value.SeasonNumber
		result.SeasonNumber = &copyValue
	}
	if value.Episodes != nil {
		if len(*value.Episodes) > DefaultMaxEpisodes {
			return ManualImportCandidate{}, errors.New("manual import episode bound exceeded")
		}
		result.Episodes = make([]EpisodeReference, len(*value.Episodes))
		seen := make(map[int64]struct{}, len(*value.Episodes))
		for index, episode := range *value.Episodes {
			normalized, err := normalizeEpisodeReference(episode)
			if err != nil {
				return ManualImportCandidate{}, err
			}
			if _, exists := seen[normalized.ID]; exists {
				return ManualImportCandidate{}, errors.New("duplicate manual import episode")
			}
			associationSeriesID := expectedSeriesID
			if associationSeriesID == 0 && result.Series != nil {
				associationSeriesID = result.Series.ID
			}
			if associationSeriesID == 0 || normalized.SeriesID != associationSeriesID {
				return ManualImportCandidate{}, errors.New("manual import episode series identity disagrees")
			}
			seen[normalized.ID] = struct{}{}
			result.Episodes[index] = normalized
		}
	}
	if value.EpisodeFileId != nil {
		if *value.EpisodeFileId < 0 {
			return ManualImportCandidate{}, errors.New("manual import episode file is invalid")
		}
		copyValue := *value.EpisodeFileId
		result.EpisodeFileID = &copyValue
	}
	if value.ReleaseGroup != nil {
		if validateText(*value.ReleaseGroup, maxDownloadIDChars, false) != nil {
			return ManualImportCandidate{}, errors.New("manual import release group is invalid")
		}
		result.ReleaseGroup = *value.ReleaseGroup
	}
	if value.Quality != nil {
		quality, err := normalizeQuality(*value.Quality)
		if err != nil {
			return ManualImportCandidate{}, err
		}
		result.Quality = &quality
	}
	language, languages, err := normalizeLanguages(value.Language, value.Languages)
	if err != nil {
		return ManualImportCandidate{}, err
	}
	result.Language = language
	result.Languages = languages
	if value.DownloadId != nil {
		if validateBoundedID(*value.DownloadId, maxDownloadIDChars) != nil {
			return ManualImportCandidate{}, errors.New("manual import download id is invalid")
		}
		result.DownloadID = *value.DownloadId
	}
	if value.ReleaseType != nil {
		if validateText(*value.ReleaseType, maxLanguageNameChars, false) != nil {
			return ManualImportCandidate{}, errors.New("manual import release type is invalid")
		}
		result.ReleaseType = *value.ReleaseType
	}
	if value.CustomFormatScore != nil {
		copyValue := *value.CustomFormatScore
		result.CustomFormatScore = &copyValue
	}
	if value.IndexerFlags != nil {
		copyValue := *value.IndexerFlags
		result.IndexerFlags = &copyValue
	}
	result.Forced = firstBool(value.Forced, value.IsForced)
	result.HearingImpaired = firstBool(value.HearingImpaired, value.IsHearingImpaired)
	if value.Rejections != nil {
		if len(*value.Rejections) > maxRejections {
			return ManualImportCandidate{}, errors.New("manual import rejection bound exceeded")
		}
		result.Rejections = make([]ImportRejection, len(*value.Rejections))
		for index, rejection := range *value.Rejections {
			if validateText(rejection.Type, maxRejectionCode, true) != nil {
				return ManualImportCandidate{}, errors.New("manual import rejection type is invalid")
			}
			if rejection.Reason != nil && validateText(*rejection.Reason, maxRejectionMessage, true) != nil {
				return ManualImportCandidate{}, errors.New("manual import rejection reason is invalid")
			}
			if rejection.Message != nil && validateText(*rejection.Message, maxRejectionMessage, false) != nil {
				return ManualImportCandidate{}, errors.New("manual import rejection message is invalid")
			}
			item := ImportRejection{}
			item.Type = rejection.Type
			if rejection.Reason != nil {
				item.Reason = *rejection.Reason
			}
			if rejection.Message != nil {
				item.Message = *rejection.Message
			}
			if item.Reason != "" && item.Message != "" && item.Reason != item.Message {
				return ManualImportCandidate{}, errors.New("manual import rejection aliases disagree")
			}
			if item.Reason == "" && item.Message == "" {
				return ManualImportCandidate{}, errors.New("manual import rejection reason is missing")
			}
			if item.Reason == "" {
				item.Reason = item.Message
			}
			if item.Message == "" {
				item.Message = item.Reason
			}
			result.Rejections[index] = item
		}
	}
	return result, nil
}

// normalizeLanguages preserves Sonarr's native singular language member and
// the older plural compatibility alias independently. When both are present,
// their semantic sets must be equal: the singular member represents a
// one-element set, so a plural superset is ambiguous and cannot contribute
// complete evidence.
func normalizeLanguages(singular *generated.Language, plural *[]generated.Language) (*Language, []Language, error) {
	var native *Language
	if singular != nil {
		value, err := normalizeLanguage(*singular)
		if err != nil {
			return nil, nil, err
		}
		native = &value
	}

	var aliases []Language
	if plural != nil {
		if len(*plural) > 128 {
			return nil, nil, errors.New("manual import language bound exceeded")
		}
		aliases = make([]Language, len(*plural))
		seen := make(map[int32]struct{}, len(*plural))
		for index, value := range *plural {
			normalized, err := normalizeLanguage(value)
			if err != nil {
				return nil, nil, err
			}
			if _, exists := seen[normalized.ID]; exists {
				return nil, nil, errors.New("duplicate manual import language")
			}
			seen[normalized.ID] = struct{}{}
			aliases[index] = normalized
		}
	}
	if native != nil && plural != nil {
		if len(aliases) != 1 || aliases[0].ID != native.ID || aliases[0].Name != native.Name {
			return nil, nil, errors.New("manual import language aliases disagree")
		}
	}
	return native, aliases, nil
}

func normalizeLanguage(value generated.Language) (Language, error) {
	if value.Id < 0 || validateText(value.Name, maxLanguageNameChars, true) != nil {
		return Language{}, errors.New("manual import language is invalid")
	}
	return Language{ID: value.Id, Name: value.Name}, nil
}

func normalizeSeriesReference(value generated.SeriesReference) (SeriesReference, error) {
	if value.Id <= 0 || validateText(value.Title, maxTitleChars, true) != nil {
		return SeriesReference{}, errors.New("manual import series reference is invalid")
	}
	result := SeriesReference{ID: value.Id, Title: value.Title}
	if value.TvdbId != nil {
		if *value.TvdbId < 0 {
			return SeriesReference{}, errors.New("manual import tvdb id is invalid")
		}
		copyValue := *value.TvdbId
		result.TVDBID = &copyValue
	}
	if value.TvMazeId != nil {
		if *value.TvMazeId < 0 {
			return SeriesReference{}, errors.New("manual import tvmaze id is invalid")
		}
		copyValue := *value.TvMazeId
		result.TVMazeID = &copyValue
	}
	return result, nil
}

func normalizeEpisodeReference(value generated.EpisodeReference) (EpisodeReference, error) {
	if value.Id <= 0 || value.SeriesId <= 0 || value.SeasonNumber < 0 || value.EpisodeNumber < 0 {
		return EpisodeReference{}, errors.New("manual import episode reference is invalid")
	}
	result := EpisodeReference{ID: value.Id, SeriesID: value.SeriesId, SeasonNumber: value.SeasonNumber, EpisodeNumber: value.EpisodeNumber}
	if value.EpisodeFileId != nil {
		if *value.EpisodeFileId < 0 {
			return EpisodeReference{}, errors.New("manual import episode file id is invalid")
		}
		copyValue := *value.EpisodeFileId
		result.EpisodeFileID = &copyValue
	}
	if value.AbsoluteEpisodeNumber != nil {
		if *value.AbsoluteEpisodeNumber < 0 {
			return EpisodeReference{}, errors.New("manual import absolute episode is invalid")
		}
		copyValue := *value.AbsoluteEpisodeNumber
		result.AbsoluteEpisodeNumber = &copyValue
	}
	if value.SceneAbsoluteEpisodeNumber != nil {
		if *value.SceneAbsoluteEpisodeNumber < 0 {
			return EpisodeReference{}, errors.New("manual import scene absolute episode is invalid")
		}
		copyValue := *value.SceneAbsoluteEpisodeNumber
		result.SceneAbsoluteEpisodeNumber = &copyValue
	}
	return result, nil
}

func normalizeQuality(value generated.Quality) (Quality, error) {
	result := Quality{}
	if value.Quality != nil {
		details := QualityDetails{}
		if value.Quality.Id != nil {
			copyValue := *value.Quality.Id
			details.ID = &copyValue
		}
		if value.Quality.Name != nil {
			if validateText(*value.Quality.Name, maxQualityNameChars, false) != nil {
				return Quality{}, errors.New("manual import quality name is invalid")
			}
			details.Name = *value.Quality.Name
		}
		if value.Quality.Source != nil {
			if validateText(*value.Quality.Source, maxLanguageNameChars, false) != nil {
				return Quality{}, errors.New("manual import quality source is invalid")
			}
			details.Source = *value.Quality.Source
		}
		if value.Quality.Resolution != nil {
			if *value.Quality.Resolution < 0 {
				return Quality{}, errors.New("manual import quality resolution is invalid")
			}
			copyValue := *value.Quality.Resolution
			details.Resolution = &copyValue
		}
		result.Quality = &details
	}
	if value.Revision != nil {
		revision := QualityRevision{}
		if value.Revision.Version != nil {
			copyValue := *value.Revision.Version
			revision.Version = &copyValue
		}
		if value.Revision.Real != nil {
			copyValue := *value.Revision.Real
			revision.Real = &copyValue
		}
		if value.Revision.IsRepack != nil {
			copyValue := *value.Revision.IsRepack
			revision.IsRepack = &copyValue
		}
		result.Revision = &revision
	}
	return result, nil
}

func firstBool(values ...*bool) *bool {
	for _, value := range values {
		if value != nil {
			copyValue := *value
			return &copyValue
		}
	}
	return nil
}

func validateVersion(value string) error {
	if validateText(value, maxVersionChars, true) != nil || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return errors.New("version is invalid")
	}
	return nil
}

func validateNativePath(value string) error {
	return validateText(value, maxTextChars, true)
}

func validateBoundedID(value string, maxChars int) error {
	if validateText(value, maxChars, true) != nil || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return errors.New("identifier is invalid")
	}
	return nil
}

func validateText(value string, maxChars int, nonEmpty bool) error {
	if nonEmpty && value == "" {
		return errors.New("text is empty")
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxChars || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return errors.New("text is invalid")
	}
	return nil
}

func validBoundedText(value string, maxChars int, nonEmpty bool) bool {
	return validateText(value, maxChars, nonEmpty) == nil
}
