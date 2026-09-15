// Package radarr implements the small, read-only Radarr v3 compatibility
// boundary used by Mastarr. It owns transport, API-key authentication and
// upstream DTO normalization; it has no dependency on the Mastarr root
// module.
package radarr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	generated "github.com/guilycst/mastarr/clients/radarr/internal/generated"
)

const (
	DefaultMaxResponseBytes = 8 << 20
	DefaultMaxItems         = 10_000
	DefaultMaxMovieFiles    = 50_000
	DefaultMaxRejections    = 10_000
	DefaultRequestTimeout   = 15 * time.Second

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
)

const (
	apiPrefix       = "/api/v3"
	apiSystemStatus = "/system/status"
	apiMovie        = "/movie"
	apiRootFolder   = "/rootfolder"
	apiQuality      = "/qualityprofile"
	apiMovieFile    = "/moviefile"
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
		return fmt.Sprintf("Radarr %s failed (%s, status %d)", operation, err.Code, err.Status)
	}
	return fmt.Sprintf("Radarr %s failed (%s)", operation, err.Code)
}

// IsCode reports whether err or a wrapped error carries code.
func IsCode(err error, code ErrorCode) bool {
	var upstream UpstreamError
	return errors.As(err, &upstream) && upstream.Code == code
}

// Config controls one Radarr connection. Endpoint is the server or reverse
// proxy prefix; this client appends /api/v3. APIKey is held in memory and sent
// only in X-Api-Key headers.
type Config struct {
	Endpoint         string
	APIKey           string
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
	MaxResponseBytes int64
	MaxItems         int
	MaxMovieFiles    int
	MaxRejections    int
	UserAgent        string
}

// Client is an independent, read-only Radarr API client.
type Client struct {
	endpoint         *url.URL
	httpClient       *http.Client
	requestTimeout   time.Duration
	maxResponseBytes int64
	maxItems         int
	maxMovieFiles    int
	maxRejections    int
	apiKey           string
	userAgent        string
}

// New validates configuration without contacting Radarr.
func New(config Config) (*Client, error) {
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if !validBoundedText(config.APIKey, maxAPIKeyChars, true) || strings.TrimSpace(config.APIKey) != config.APIKey {
		return nil, errors.New("Radarr API key is invalid")
	}

	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	if maxResponseBytes < 1 || maxResponseBytes >= math.MaxInt64 {
		return nil, errors.New("Radarr response bound is invalid")
	}
	maxItems := config.MaxItems
	if maxItems <= 0 {
		maxItems = DefaultMaxItems
	}
	maxMovieFiles := config.MaxMovieFiles
	if maxMovieFiles <= 0 {
		maxMovieFiles = DefaultMaxMovieFiles
	}
	maxRejections := config.MaxRejections
	if maxRejections <= 0 {
		maxRejections = DefaultMaxRejections
	}
	if maxItems > DefaultMaxItems || maxMovieFiles > DefaultMaxMovieFiles || maxRejections > DefaultMaxRejections {
		return nil, errors.New("Radarr item bound exceeds compatibility ceiling")
	}
	requestTimeout := config.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = DefaultRequestTimeout
	}
	if requestTimeout < 0 {
		return nil, errors.New("Radarr request timeout cannot be negative")
	}
	userAgent := config.UserAgent
	if userAgent == "" {
		userAgent = "mastarr-radarr-client/0.0.1"
	}
	if !validBoundedText(userAgent, maxUserAgentChars, true) || strings.TrimSpace(userAgent) != userAgent {
		return nil, errors.New("Radarr user agent is invalid")
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
		maxMovieFiles:    maxMovieFiles,
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
func (client *Client) String() string { return "RadarrClient{" + client.Endpoint() + "}" }

// Coverage describes the completeness of one full-array Radarr snapshot.
// Radarr's native read endpoints return complete arrays rather than cursors.
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
// this module because these native endpoints have no cursor contract.
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

// Version reads Radarr's version from /system/status.
func (client *Client) Version(ctx context.Context) (string, error) {
	status, err := client.Status(ctx)
	if err != nil {
		return "", err
	}
	return status.Version, nil
}

// Status reads /system/status.
func (client *Client) Status(ctx context.Context) (SystemStatus, error) {
	body, err := client.get(ctx, "radarr.system.status", apiSystemStatus, nil)
	if err != nil {
		return SystemStatus{}, err
	}
	if err := requireObjectFields(body, "version"); err != nil {
		return SystemStatus{}, malformed("radarr.system.status")
	}
	var value generated.SystemStatus
	if err := decodeJSON(body, &value); err != nil || validateVersion(value.Version) != nil {
		return SystemStatus{}, malformed("radarr.system.status")
	}
	result := SystemStatus{Version: value.Version, ObservedAt: time.Now().UTC()}
	for _, field := range []struct {
		value *string
		dest  *string
		bound int
	}{{value.Branch, &result.Branch, maxVersionChars}, {value.AppName, &result.AppName, maxVersionChars}, {value.InstanceName, &result.InstanceName, maxTextChars}, {value.StartTime, &result.StartTime, maxTextChars}} {
		if field.value == nil {
			continue
		}
		if validateText(*field.value, field.bound, false) != nil {
			return SystemStatus{}, malformed("radarr.system.status")
		}
		*field.dest = *field.value
	}
	return result, nil
}

// GetSystemStatus is an explicit method-name alias for Status.
func (client *Client) GetSystemStatus(ctx context.Context) (SystemStatus, error) {
	return client.Status(ctx)
}

// Movie is one normalized Radarr catalog record.
type Movie struct {
	ID          int64
	Title       string
	Year        *int32
	Path        string
	Monitored   bool
	HasFile     *bool
	MovieFileID *int64
	TMDBID      *int64
	IMDBID      string
	ProviderIDs map[string]string
	MovieFile   *MovieFile
}

// ListMovies reads the complete /movie array and reports complete coverage.
func (client *Client) ListMovies(ctx context.Context) (Page[Movie], error) {
	body, err := client.get(ctx, "radarr.movie.list", apiMovie, nil)
	if err != nil {
		return Page[Movie]{}, err
	}
	if err := requireArrayObjectFields(body, "id", "title", "path", "monitored"); err != nil {
		return Page[Movie]{}, malformed("radarr.movie.list")
	}
	if err := validateMovieNestedShape(body); err != nil {
		return Page[Movie]{}, malformed("radarr.movie.list")
	}
	var values []generated.Movie
	if err := decodeJSON(body, &values); err != nil {
		return Page[Movie]{}, malformed("radarr.movie.list")
	}
	if len(values) > client.maxItems {
		return Page[Movie]{}, tooLarge("radarr.movie.list", client.maxItems)
	}
	items := make([]Movie, len(values))
	seen := make(map[int64]struct{}, len(values))
	for index, value := range values {
		item, err := normalizeMovie(value)
		if err != nil {
			return Page[Movie]{}, malformed("radarr.movie.list")
		}
		if _, exists := seen[item.ID]; exists {
			return Page[Movie]{}, malformed("radarr.movie.list")
		}
		seen[item.ID] = struct{}{}
		items[index] = item
	}
	return completePage(items), nil
}

// MovieCatalog is a concise alias for ListMovies.
func (client *Client) MovieCatalog(ctx context.Context) (Page[Movie], error) {
	return client.ListMovies(ctx)
}

// Catalog is retained as a concise catalog alias.
func (client *Client) Catalog(ctx context.Context) (Page[Movie], error) {
	return client.ListMovies(ctx)
}

// GetMovie reads one movie by native ID.
func (client *Client) GetMovie(ctx context.Context, id int64) (Movie, error) {
	if id <= 0 {
		return Movie{}, invalidInput("radarr.movie.get")
	}
	body, err := client.get(ctx, "radarr.movie.get", fmt.Sprintf("%s/%d", apiMovie, id), nil)
	if err != nil {
		return Movie{}, err
	}
	if err := requireObjectFields(body, "id", "title", "path", "monitored"); err != nil {
		return Movie{}, malformed("radarr.movie.get")
	}
	if err := validateMovieNestedShape(body); err != nil {
		return Movie{}, malformed("radarr.movie.get")
	}
	var value generated.Movie
	if err := decodeJSON(body, &value); err != nil {
		return Movie{}, malformed("radarr.movie.get")
	}
	item, err := normalizeMovie(value)
	if err != nil || item.ID != id {
		return Movie{}, malformed("radarr.movie.get")
	}
	return item, nil
}

// RootFolder is one configured Radarr root path.
type RootFolder struct {
	ID         int64
	Path       string
	Name       string
	Accessible *bool
	FreeSpace  *int64
}

// ListRootFolders reads configured root folders.
func (client *Client) ListRootFolders(ctx context.Context) (Page[RootFolder], error) {
	body, err := client.get(ctx, "radarr.rootfolder.list", apiRootFolder, nil)
	if err != nil {
		return Page[RootFolder]{}, err
	}
	if err := requireArrayObjectFields(body, "id", "path"); err != nil {
		return Page[RootFolder]{}, malformed("radarr.rootfolder.list")
	}
	var values []generated.RootFolder
	if err := decodeJSON(body, &values); err != nil {
		return Page[RootFolder]{}, malformed("radarr.rootfolder.list")
	}
	if len(values) > client.maxItems {
		return Page[RootFolder]{}, tooLarge("radarr.rootfolder.list", client.maxItems)
	}
	items := make([]RootFolder, len(values))
	seen := make(map[int64]struct{}, len(values))
	for index, value := range values {
		if value.Id <= 0 || validateNativePath(value.Path) != nil {
			return Page[RootFolder]{}, malformed("radarr.rootfolder.list")
		}
		if _, exists := seen[value.Id]; exists {
			return Page[RootFolder]{}, malformed("radarr.rootfolder.list")
		}
		seen[value.Id] = struct{}{}
		item := RootFolder{ID: value.Id, Path: value.Path}
		if value.Name != nil {
			if validateText(*value.Name, maxTextChars, false) != nil {
				return Page[RootFolder]{}, malformed("radarr.rootfolder.list")
			}
			item.Name = *value.Name
		}
		if value.Accessible != nil {
			accessible := *value.Accessible
			item.Accessible = &accessible
		}
		if value.FreeSpace != nil {
			if *value.FreeSpace < 0 {
				return Page[RootFolder]{}, malformed("radarr.rootfolder.list")
			}
			freeSpace := *value.FreeSpace
			item.FreeSpace = &freeSpace
		}
		items[index] = item
	}
	return completePage(items), nil
}

// QualityProfile is one configured Radarr quality profile.
type QualityProfile struct {
	ID   int64
	Name string
}

// ListQualityProfiles reads configured quality profiles.
func (client *Client) ListQualityProfiles(ctx context.Context) (Page[QualityProfile], error) {
	body, err := client.get(ctx, "radarr.qualityprofile.list", apiQuality, nil)
	if err != nil {
		return Page[QualityProfile]{}, err
	}
	if err := requireArrayObjectFields(body, "id", "name"); err != nil {
		return Page[QualityProfile]{}, malformed("radarr.qualityprofile.list")
	}
	var values []generated.QualityProfile
	if err := decodeJSON(body, &values); err != nil {
		return Page[QualityProfile]{}, malformed("radarr.qualityprofile.list")
	}
	if len(values) > client.maxItems {
		return Page[QualityProfile]{}, tooLarge("radarr.qualityprofile.list", client.maxItems)
	}
	items := make([]QualityProfile, len(values))
	seen := make(map[int64]struct{}, len(values))
	for index, value := range values {
		if value.Id <= 0 || validateText(value.Name, maxTextChars, true) != nil {
			return Page[QualityProfile]{}, malformed("radarr.qualityprofile.list")
		}
		if _, exists := seen[value.Id]; exists {
			return Page[QualityProfile]{}, malformed("radarr.qualityprofile.list")
		}
		seen[value.Id] = struct{}{}
		items[index] = QualityProfile{ID: value.Id, Name: value.Name}
	}
	return completePage(items), nil
}

// MovieFile is the normalized file association returned by Radarr.
type MovieFile struct {
	ID           int64
	MovieID      int64
	Path         string
	RelativePath string
	Size         int64
	DateAdded    string
	SceneName    string
	ReleaseGroup string
	Quality      *Quality
	Languages    []Language
}

// ListMovieFiles reads all movie files for movieID and verifies the native
// movieId association on every returned record.
func (client *Client) ListMovieFiles(ctx context.Context, movieID int64) (Page[MovieFile], error) {
	if movieID <= 0 {
		return Page[MovieFile]{}, invalidInput("radarr.moviefile.list")
	}
	query := url.Values{"movieId": []string{fmt.Sprintf("%d", movieID)}}
	body, err := client.get(ctx, "radarr.moviefile.list", apiMovieFile, query)
	if err != nil {
		return Page[MovieFile]{}, err
	}
	if err := requireArrayObjectFields(body, "id", "movieId", "path", "size"); err != nil {
		return Page[MovieFile]{}, malformed("radarr.moviefile.list")
	}
	if err := validateMovieFileNestedShape(body); err != nil {
		return Page[MovieFile]{}, malformed("radarr.moviefile.list")
	}
	var values []generated.MovieFile
	if err := decodeJSON(body, &values); err != nil {
		return Page[MovieFile]{}, malformed("radarr.moviefile.list")
	}
	if len(values) > client.maxMovieFiles {
		return Page[MovieFile]{}, tooLarge("radarr.moviefile.list", client.maxMovieFiles)
	}
	items := make([]MovieFile, len(values))
	seen := make(map[int64]struct{}, len(values))
	for index, value := range values {
		item, err := normalizeMovieFile(value, movieID)
		if err != nil {
			return Page[MovieFile]{}, malformed("radarr.moviefile.list")
		}
		if _, exists := seen[item.ID]; exists {
			return Page[MovieFile]{}, malformed("radarr.moviefile.list")
		}
		seen[item.ID] = struct{}{}
		items[index] = item
	}
	return completePage(items), nil
}

// ListMovieFile is an explicit singular alias for ListMovieFiles.
func (client *Client) ListMovieFile(ctx context.Context, movieID int64) (Page[MovieFile], error) {
	return client.ListMovieFiles(ctx, movieID)
}

// ManualImportQuery describes Radarr's downloaded-folder preview mode. Folder
// is always required because Radarr's native manual-import service needs a
// source path; MovieID, when supplied, narrows that source to one movie.
type ManualImportQuery struct {
	Folder              string
	FilterExistingFiles bool
	MovieID             *int64
	DownloadID          string
}

// LibraryImportQuery is retained as a compatibility name for a movie-bound
// preview. It is path-bound: Folder is required and is never resolved from a
// registered movie record. Radarr does not provide a movieId-only library
// manual-import scan.
type LibraryImportQuery struct {
	Folder              string
	MovieID             int64
	FilterExistingFiles bool
	DownloadID          string
}

// MovieImportQuery is a descriptive alias for LibraryImportQuery.
type MovieImportQuery = LibraryImportQuery

// ManualImportCandidate is a typed native preview candidate. Rejections and
// optional associations remain visible so callers can require explicit review.
type ManualImportCandidate struct {
	ID                int64
	Path              string
	RelativePath      string
	FolderName        string
	Name              string
	Size              int64
	Movie             *MovieReference
	MovieFileID       *int64
	ReleaseGroup      string
	Quality           *Quality
	Languages         []Language
	DownloadID        string
	CustomFormatScore *int32
	IndexerFlags      *int32
	Rejections        []ImportRejection
}

// MovieReference is the identity-bearing nested movie object in a native
// manual-import resource.
type MovieReference struct {
	ID     int64
	Title  string
	Year   *int32
	TMDBID *int64
	IMDBID string
}

// Language preserves Radarr's typed language ID/name/ISO observation.
type Language struct {
	ID      int32
	Name    string
	ISOCode string
}

// Quality preserves typed quality and revision fields used by reconciliation.
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
// as an explicit compatibility alias for older response shapes.
type ImportRejection struct {
	Type    string
	Reason  string
	Message string
}

// PreviewManualImport performs only Radarr's downloaded-folder GET
// /manualimport mode.
func (client *Client) PreviewManualImport(ctx context.Context, query ManualImportQuery) (Page[ManualImportCandidate], error) {
	if validateNativePath(query.Folder) != nil {
		return Page[ManualImportCandidate]{}, invalidInput("radarr.manualimport.preview")
	}
	if query.MovieID != nil {
		if *query.MovieID <= 0 {
			return Page[ManualImportCandidate]{}, invalidInput("radarr.manualimport.preview.movie_id")
		}
	}
	values := url.Values{
		"folder":              []string{query.Folder},
		"filterExistingFiles": []string{fmt.Sprintf("%t", query.FilterExistingFiles)},
	}
	if query.MovieID != nil {
		values.Set("movieId", fmt.Sprintf("%d", *query.MovieID))
	}
	if query.DownloadID != "" {
		if validateBoundedID(query.DownloadID, maxDownloadIDChars) != nil {
			return Page[ManualImportCandidate]{}, invalidInput("radarr.manualimport.preview")
		}
		values.Set("downloadId", query.DownloadID)
	}
	return client.previewManualImport(ctx, "radarr.manualimport.preview.folder", values, 0)
}

// PreviewLibraryImport performs a path-bound, movie-scoped manual-import
// preview. The compatibility name remains for callers that used the first
// draft, but Folder is mandatory and is sent to Radarr with movieId.
func (client *Client) PreviewLibraryImport(ctx context.Context, query LibraryImportQuery) (Page[ManualImportCandidate], error) {
	if validateNativePath(query.Folder) != nil || query.MovieID <= 0 {
		return Page[ManualImportCandidate]{}, invalidInput("radarr.manualimport.preview.library")
	}
	values := url.Values{
		"folder":              []string{query.Folder},
		"movieId":             []string{fmt.Sprintf("%d", query.MovieID)},
		"filterExistingFiles": []string{fmt.Sprintf("%t", query.FilterExistingFiles)},
	}
	if query.DownloadID != "" {
		if validateBoundedID(query.DownloadID, maxDownloadIDChars) != nil {
			return Page[ManualImportCandidate]{}, invalidInput("radarr.manualimport.preview.library")
		}
		values.Set("downloadId", query.DownloadID)
	}
	return client.previewManualImport(ctx, "radarr.manualimport.preview.movie", values, query.MovieID)
}

// PreviewMovieImport is an explicit movie-named alias for PreviewLibraryImport.
func (client *Client) PreviewMovieImport(ctx context.Context, query MovieImportQuery) (Page[ManualImportCandidate], error) {
	return client.PreviewLibraryImport(ctx, query)
}

// PreviewImport is a concise alias for PreviewManualImport.
func (client *Client) PreviewImport(ctx context.Context, query ManualImportQuery) (Page[ManualImportCandidate], error) {
	return client.PreviewManualImport(ctx, query)
}

func (client *Client) previewManualImport(ctx context.Context, operation string, values url.Values, expectedMovieID int64) (Page[ManualImportCandidate], error) {
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
	if len(valuesDTO) > client.maxMovieFiles {
		return Page[ManualImportCandidate]{}, tooLarge(operation, client.maxMovieFiles)
	}
	items := make([]ManualImportCandidate, len(valuesDTO))
	seen := make(map[string]struct{}, len(valuesDTO))
	for index, value := range valuesDTO {
		item, err := normalizeManualImport(value, client.maxRejections, expectedMovieID)
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
		return nil, errors.New("Radarr endpoint is invalid")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Radarr endpoint must be an absolute URL without credentials or query")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("Radarr endpoint scheme is unsupported")
	}
	if !utf8.ValidString(parsed.Host) || strings.Contains(parsed.Path, "\\") || strings.Contains(parsed.Path, "//") || strings.Contains(parsed.Path, "%") || (parsed.RawPath != "" && strings.Contains(parsed.RawPath, "%")) {
		return nil, errors.New("Radarr endpoint path is ambiguous")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return nil, errors.New("Radarr endpoint path is ambiguous")
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
	case status == http.StatusNotFound && operation == "radarr.movie.get":
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

func tooLarge(operation string, _ int) UpstreamError {
	return UpstreamError{Code: ErrorResponseTooLarge, Operation: operation}
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
// generated DTO unmarshalling. Radarr can add fields over time, so unknown
// members remain allowed by the compatibility contract.
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

func validateMovieNestedShape(data []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil || values == nil {
		var object map[string]json.RawMessage
		if json.Unmarshal(data, &object) != nil || object == nil {
			return errors.New("movie object is required")
		}
		return validateMovieObjectShape(object)
	}
	for _, raw := range values {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return errors.New("movie object is required")
		}
		if err := validateMovieObjectShape(object); err != nil {
			return err
		}
	}
	return nil
}

func validateMovieObjectShape(object map[string]json.RawMessage) error {
	if nested, present := object["movieFile"]; present && !isJSONNull(nested) {
		if err := requireObjectFields(nested, "id", "movieId", "path", "size"); err != nil {
			return err
		}
		var nestedObject map[string]json.RawMessage
		if err := json.Unmarshal(nested, &nestedObject); err != nil || nestedObject == nil {
			return errors.New("movie-file object is required")
		}
		if err := validateMovieFileObjectShape(nestedObject); err != nil {
			return err
		}
	}
	return nil
}

func validateMovieFileNestedShape(data []byte) error {
	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil || values == nil {
		return errors.New("movie-file array is required")
	}
	for _, raw := range values {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil || object == nil {
			return errors.New("movie-file object is required")
		}
		if err := validateMovieFileObjectShape(object); err != nil {
			return err
		}
	}
	return nil
}

func validateMovieFileObjectShape(object map[string]json.RawMessage) error {
	if nested, present := object["languages"]; present {
		if isJSONNull(nested) {
			return errors.New("movie-file languages cannot be null")
		}
		var values []json.RawMessage
		if err := json.Unmarshal(nested, &values); err != nil || values == nil {
			return errors.New("movie-file languages array is required")
		}
		for _, language := range values {
			if err := requireObjectFields(language, "id", "name"); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateManualImportShape checks nested identity, language and rejection
// fields before generated scalar fields can default omitted values to zero.
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
		if nested, present := object["movie"]; present && !isJSONNull(nested) {
			if err := requireObjectFields(nested, "id"); err != nil {
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

func isJSONNull(value []byte) bool { return bytes.Equal(bytes.TrimSpace(value), []byte("null")) }

func normalizeMovie(value generated.Movie) (Movie, error) {
	if value.Id <= 0 || validateText(value.Title, maxTitleChars, true) != nil || validateNativePath(value.Path) != nil {
		return Movie{}, errors.New("movie identity is invalid")
	}
	result := Movie{ID: value.Id, Title: value.Title, Path: value.Path, Monitored: value.Monitored}
	if value.Year != nil {
		copyValue := *value.Year
		result.Year = &copyValue
	}
	if value.HasFile != nil {
		copyValue := *value.HasFile
		result.HasFile = &copyValue
	}
	if value.MovieFileId != nil {
		if *value.MovieFileId < 0 {
			return Movie{}, errors.New("movie file id is invalid")
		}
		copyValue := *value.MovieFileId
		result.MovieFileID = &copyValue
	}
	if value.TmdbId != nil {
		if *value.TmdbId < 0 {
			return Movie{}, errors.New("movie tmdb id is invalid")
		}
		copyValue := *value.TmdbId
		result.TMDBID = &copyValue
	}
	if value.ImdbId != nil {
		if validateText(*value.ImdbId, maxProviderIDChars, false) != nil {
			return Movie{}, errors.New("movie imdb id is invalid")
		}
		result.IMDBID = *value.ImdbId
	}
	if value.ProviderIds != nil {
		result.ProviderIDs = make(map[string]string, len(*value.ProviderIds))
		for key, providerID := range *value.ProviderIds {
			if validateText(key, maxProviderIDChars, true) != nil || validateText(providerID, maxProviderIDChars, true) != nil {
				return Movie{}, errors.New("movie provider id is invalid")
			}
			result.ProviderIDs[key] = providerID
		}
	}
	if value.MovieFile != nil {
		file, err := normalizeMovieFile(*value.MovieFile, value.Id)
		if err != nil {
			return Movie{}, err
		}
		if value.MovieFileId != nil && *value.MovieFileId != file.ID {
			return Movie{}, errors.New("movie file identity disagrees")
		}
		result.MovieFile = &file
		if result.MovieFileID == nil {
			copyValue := file.ID
			result.MovieFileID = &copyValue
		}
	}
	return result, nil
}

func normalizeMovieFile(value generated.MovieFile, expectedMovieID int64) (MovieFile, error) {
	if value.Id <= 0 || value.MovieId <= 0 || validateNativePath(value.Path) != nil || value.Size < 0 {
		return MovieFile{}, errors.New("movie file is invalid")
	}
	if expectedMovieID > 0 && value.MovieId != expectedMovieID {
		return MovieFile{}, errors.New("movie file movie identity disagrees")
	}
	result := MovieFile{ID: value.Id, MovieID: value.MovieId, Path: value.Path, Size: value.Size}
	if value.RelativePath != nil {
		if validateText(*value.RelativePath, maxTextChars, false) != nil {
			return MovieFile{}, errors.New("movie relative path is invalid")
		}
		result.RelativePath = *value.RelativePath
	}
	if value.DateAdded != nil {
		if validateText(*value.DateAdded, maxVersionChars, false) != nil {
			return MovieFile{}, errors.New("movie date is invalid")
		}
		result.DateAdded = *value.DateAdded
	}
	if value.SceneName != nil {
		if validateText(*value.SceneName, maxTitleChars, false) != nil {
			return MovieFile{}, errors.New("movie scene name is invalid")
		}
		result.SceneName = *value.SceneName
	}
	if value.ReleaseGroup != nil {
		if validateText(*value.ReleaseGroup, maxDownloadIDChars, false) != nil {
			return MovieFile{}, errors.New("movie release group is invalid")
		}
		result.ReleaseGroup = *value.ReleaseGroup
	}
	if value.Quality != nil {
		quality, err := normalizeQuality(*value.Quality)
		if err != nil {
			return MovieFile{}, err
		}
		result.Quality = &quality
	}
	if value.Languages != nil {
		languages, err := normalizeLanguages(*value.Languages)
		if err != nil {
			return MovieFile{}, err
		}
		result.Languages = languages
	}
	return result, nil
}

func normalizeManualImport(value generated.ManualImportResource, maxRejections int, expectedMovieID int64) (ManualImportCandidate, error) {
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
	if value.Movie != nil {
		movie, err := normalizeMovieReference(*value.Movie)
		if err != nil {
			return ManualImportCandidate{}, err
		}
		if expectedMovieID > 0 && movie.ID != expectedMovieID {
			return ManualImportCandidate{}, errors.New("manual import movie identity disagrees")
		}
		result.Movie = &movie
	} else if expectedMovieID > 0 {
		return ManualImportCandidate{}, errors.New("manual import movie evidence is missing")
	}
	if value.MovieFileId != nil {
		if *value.MovieFileId < 0 {
			return ManualImportCandidate{}, errors.New("manual import movie file is invalid")
		}
		copyValue := *value.MovieFileId
		result.MovieFileID = &copyValue
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
	if value.Languages != nil {
		languages, err := normalizeLanguages(*value.Languages)
		if err != nil {
			return ManualImportCandidate{}, err
		}
		result.Languages = languages
	}
	if value.DownloadId != nil {
		if validateBoundedID(*value.DownloadId, maxDownloadIDChars) != nil {
			return ManualImportCandidate{}, errors.New("manual import download id is invalid")
		}
		result.DownloadID = *value.DownloadId
	}
	if value.CustomFormatScore != nil {
		copyValue := *value.CustomFormatScore
		result.CustomFormatScore = &copyValue
	}
	if value.IndexerFlags != nil {
		copyValue := *value.IndexerFlags
		result.IndexerFlags = &copyValue
	}
	if value.Rejections != nil {
		if len(*value.Rejections) > maxRejections {
			return ManualImportCandidate{}, errors.New("manual import rejection bound exceeded")
		}
		result.Rejections = make([]ImportRejection, len(*value.Rejections))
		for index, rejection := range *value.Rejections {
			if validateText(rejection.Type, maxRejectionCode, true) != nil {
				return ManualImportCandidate{}, errors.New("manual import rejection type is invalid")
			}
			item := ImportRejection{Type: rejection.Type}
			if rejection.Reason != nil {
				if validateText(*rejection.Reason, maxRejectionMessage, true) != nil {
					return ManualImportCandidate{}, errors.New("manual import rejection reason is invalid")
				}
				item.Reason = *rejection.Reason
			}
			if rejection.Message != nil {
				if validateText(*rejection.Message, maxRejectionMessage, false) != nil {
					return ManualImportCandidate{}, errors.New("manual import rejection message is invalid")
				}
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

func normalizeMovieReference(value generated.MovieReference) (MovieReference, error) {
	if value.Id <= 0 {
		return MovieReference{}, errors.New("manual import movie reference is invalid")
	}
	result := MovieReference{ID: value.Id}
	if value.Title != nil {
		if validateText(*value.Title, maxTitleChars, false) != nil {
			return MovieReference{}, errors.New("manual import movie title is invalid")
		}
		result.Title = *value.Title
	}
	if value.Year != nil {
		copyValue := *value.Year
		result.Year = &copyValue
	}
	if value.TmdbId != nil {
		if *value.TmdbId < 0 {
			return MovieReference{}, errors.New("manual import tmdb id is invalid")
		}
		copyValue := *value.TmdbId
		result.TMDBID = &copyValue
	}
	if value.ImdbId != nil {
		if validateText(*value.ImdbId, maxProviderIDChars, false) != nil {
			return MovieReference{}, errors.New("manual import imdb id is invalid")
		}
		result.IMDBID = *value.ImdbId
	}
	return result, nil
}

func normalizeLanguages(values []generated.Language) ([]Language, error) {
	if len(values) > 128 {
		return nil, errors.New("movie language bound exceeded")
	}
	result := make([]Language, len(values))
	seen := make(map[int32]struct{}, len(values))
	for index, value := range values {
		if value.Id < 0 || validateText(value.Name, maxLanguageNameChars, true) != nil {
			return nil, errors.New("movie language is invalid")
		}
		if _, exists := seen[value.Id]; exists {
			return nil, errors.New("duplicate movie language")
		}
		seen[value.Id] = struct{}{}
		item := Language{ID: value.Id, Name: value.Name}
		if value.IsoCode != nil {
			if validateText(*value.IsoCode, maxLanguageNameChars, false) != nil {
				return nil, errors.New("movie language ISO code is invalid")
			}
			item.ISOCode = *value.IsoCode
		}
		result[index] = item
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
				return Quality{}, errors.New("movie quality name is invalid")
			}
			details.Name = *value.Quality.Name
		}
		if value.Quality.Source != nil {
			if validateText(*value.Quality.Source, maxLanguageNameChars, false) != nil {
				return Quality{}, errors.New("movie quality source is invalid")
			}
			details.Source = *value.Quality.Source
		}
		if value.Quality.Resolution != nil {
			if *value.Quality.Resolution < 0 {
				return Quality{}, errors.New("movie quality resolution is invalid")
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

func validateVersion(value string) error {
	if validateText(value, maxVersionChars, true) != nil || strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return errors.New("version is invalid")
	}
	return nil
}

func validateNativePath(value string) error { return validateText(value, maxTextChars, true) }

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
