// Package seerr implements the small, read-only Seerr HTTP compatibility
// boundary used by Mastarr. The package owns transport, authentication,
// upstream DTO decoding and sanitized errors; it has no dependency on the
// Mastarr root module.
package seerr

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
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	generated "github.com/guilycst/mastarr/clients/seerr/internal/generated"
)

const (
	DefaultMaxResponseBytes int64 = 16 << 20
	DefaultMaxPageSize            = 100
	DefaultMaxPages               = 100
	DefaultMaxRecords             = 10_000
	DefaultRequestTimeout         = 15 * time.Second

	maxCredentialChars = 512
	maxUserAgentChars  = 128
	maxInstanceChars   = 256
	maxVersionChars    = 128
	maxTextChars       = 4096
	maxProviderChars   = 256
	maxNestedItems     = 512
	maxTags            = 256
	maxCursorBytes     = 64 << 10
	maxCursorSeenIDs   = 2048
	maxReasonCodes     = 256
	maxJSONDepth       = 64
	maxPageArray       = 1001
)

const (
	apiStatus   = "/status"
	apiMedia    = "/media"
	apiRequests = "/request"
)

// ErrorCode is a stable, sanitized class for an upstream or local failure.
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

// UpstreamError intentionally carries no endpoint, credential or response
// body. The fields are safe to persist as operation evidence.
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
		return fmt.Sprintf("Seerr %s failed (%s, status %d)", operation, err.Code, err.Status)
	}
	return fmt.Sprintf("Seerr %s failed (%s)", operation, err.Code)
}

// IsCode reports whether err or a wrapped error carries code.
func IsCode(err error, code ErrorCode) bool {
	var upstream UpstreamError
	return errors.As(err, &upstream) && upstream.Code == code
}

// Config controls one Seerr connection. Endpoint may include a reverse proxy
// path prefix; the client appends /api/v1. APIKey is sent as X-Api-Key.
// BearerToken, Token and AuthToken are bearer aliases, selected in that order
// when APIKey is empty. Only one credential is sent on a request.
type Config struct {
	Endpoint    string
	APIKey      string
	BearerToken string
	Token       string
	AuthToken   string
	InstanceID  string
	HTTPClient  *http.Client

	RequestTimeout   time.Duration
	MaxResponseBytes int64
	MaxPageSize      int
	MaxPages         int
	MaxRecords       int
	UserAgent        string
}

// Client is an authenticated, read-only Seerr HTTP client. It has no methods
// capable of creating, approving, cancelling or deleting a request.
type Client struct {
	endpoint         *url.URL
	httpClient       *http.Client
	authKind         string
	authToken        string
	instanceID       string
	requestTimeout   time.Duration
	maxResponseBytes int64
	maxPageSize      int
	maxPages         int
	maxRecords       int
	userAgent        string
	cursorKey        []byte
}

// New validates configuration without contacting Seerr.
func New(config Config) (*Client, error) {
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	authKind, authToken := configuredCredential(config)
	if authToken != "" && !validBoundedText(authToken, maxCredentialChars, true) {
		return nil, errors.New("Seerr credential is invalid")
	}
	if authToken != "" && strings.TrimSpace(authToken) != authToken {
		return nil, errors.New("Seerr credential is invalid")
	}
	if config.InstanceID != "" && !validBoundedText(config.InstanceID, maxInstanceChars, true) {
		return nil, errors.New("Seerr instance id is invalid")
	}
	if config.InstanceID != "" && strings.TrimSpace(config.InstanceID) != config.InstanceID {
		return nil, errors.New("Seerr instance id is invalid")
	}

	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	if maxResponseBytes < 1 || maxResponseBytes >= math.MaxInt64 {
		return nil, errors.New("Seerr response bound is invalid")
	}
	maxPageSize := config.MaxPageSize
	if maxPageSize <= 0 {
		maxPageSize = DefaultMaxPageSize
	}
	maxPages := config.MaxPages
	if maxPages <= 0 {
		maxPages = DefaultMaxPages
	}
	maxRecords := config.MaxRecords
	if maxRecords <= 0 {
		maxRecords = DefaultMaxRecords
	}
	if maxPageSize > maxRecords || maxPageSize > 1000 || maxPages > DefaultMaxPages || maxRecords > DefaultMaxRecords {
		return nil, errors.New("Seerr pagination bound exceeds compatibility ceiling")
	}
	requestTimeout := config.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = DefaultRequestTimeout
	}
	if requestTimeout < 0 {
		return nil, errors.New("Seerr request timeout cannot be negative")
	}
	userAgent := config.UserAgent
	if userAgent == "" {
		userAgent = "mastarr-seerr-client/0.0.1"
	}
	if !validBoundedText(userAgent, maxUserAgentChars, true) || strings.TrimSpace(userAgent) != userAgent {
		return nil, errors.New("Seerr user agent is invalid")
	}

	baseClient := http.DefaultClient
	if config.HTTPClient != nil {
		baseClient = config.HTTPClient
	}
	clientCopy := *baseClient
	clientCopy.Jar = nil
	// Never replay a credential-bearing request at a redirect target. The
	// caller receives the 3xx response and a sanitized unsupported error.
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	cursorKey := make([]byte, 32)
	if _, err := cryptorand.Read(cursorKey); err != nil {
		return nil, errors.New("Seerr cursor key setup failed")
	}
	return &Client{
		endpoint: endpoint, httpClient: &clientCopy, authKind: authKind,
		authToken: authToken, instanceID: config.InstanceID,
		requestTimeout: requestTimeout, maxResponseBytes: maxResponseBytes,
		maxPageSize: maxPageSize, maxPages: maxPages, maxRecords: maxRecords,
		userAgent: userAgent, cursorKey: cursorKey,
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

// String returns a safe diagnostic representation; credentials are omitted.
func (client *Client) String() string { return "SeerrClient{" + client.Endpoint() + "}" }

// ScopedIdentity keeps native IDs distinct when the caller configures more
// than one Seerr instance. An empty instance ID deliberately leaves the native
// ID unchanged for callers that maintain the surrounding connection scope.
func (client *Client) ScopedIdentity(id int64) string {
	if id <= 0 {
		return ""
	}
	value := strconv.FormatInt(id, 10)
	if client == nil || client.instanceID == "" {
		return value
	}
	return client.instanceID + ":" + value
}

// SystemStatus is the sanitized status/version observation.
type SystemStatus struct {
	ProductName string
	Version     string
	CommitTag   string
	Commit      string
	ObservedAt  time.Time
}

// Status reads Seerr's native status endpoint. The checkUpdateAvailable query
// is explicit because it is part of the documented status read shape and does
// not trigger a hidden discovery request.
func (client *Client) Status(ctx context.Context) (SystemStatus, error) {
	body, err := client.get(ctx, "seerr.status", apiStatus, url.Values{"checkUpdateAvailable": []string{"false"}})
	if err != nil {
		return SystemStatus{}, err
	}
	if err := requireObjectFields(body, "version"); err != nil {
		return SystemStatus{}, malformed("seerr.status")
	}
	var value generated.Status
	if err := decodeJSON(body, &value); err != nil || !validBoundedText(value.Version, maxVersionChars, false) {
		return SystemStatus{}, malformed("seerr.status")
	}
	result := SystemStatus{ProductName: "seerr", Version: value.Version, ObservedAt: time.Now().UTC()}
	if value.CommitTag != nil {
		if !validBoundedText(*value.CommitTag, maxVersionChars, false) {
			return SystemStatus{}, malformed("seerr.status")
		}
		result.CommitTag = *value.CommitTag
	}
	if value.Commit != nil {
		if !validBoundedText(*value.Commit, maxVersionChars, false) {
			return SystemStatus{}, malformed("seerr.status")
		}
		result.Commit = *value.Commit
	}
	return result, nil
}

// GetStatus is a descriptive alias for Status.
func (client *Client) GetStatus(ctx context.Context) (SystemStatus, error) {
	return client.Status(ctx)
}

// Version reads the Seerr version from the status endpoint.
func (client *Client) Version(ctx context.Context) (string, error) {
	status, err := client.Status(ctx)
	if err != nil {
		return "", err
	}
	return status.Version, nil
}

// Coverage describes the evidence boundary of one page or traversal. A page
// with no native pageInfo is unknown because a short response cannot prove
// absence. A multi-page result remains partial because Seerr has no immutable
// collection snapshot token.
type Coverage struct {
	Completeness  string
	ObservedCount int
	ExpectedCount int
	ObservedAt    time.Time
	CompletedAt   *time.Time
	ReasonCodes   []string
	// Reason is retained as a concise compatibility alias for callers that
	// previously stored one reason instead of the complete evidence list.
	Reason string
}

const (
	CompletenessComplete = "complete"
	CompletenessPartial  = "partial"
	CompletenessUnknown  = "unknown"
)

// PageInfo preserves native pageInfo fields and their presence separately
// from their zero values. Complete is true only when all four fields establish
// a valid page boundary.
type PageInfo struct {
	Pages       int
	PageSize    int
	Results     int
	Page        int
	HasPages    bool
	HasPageSize bool
	HasResults  bool
	HasPage     bool
	Complete    bool
	Present     bool
}

// Page is a bounded Seerr observation page. NextCursor is an authenticated,
// client-local continuation token; it is empty when traversal is complete or
// the native evidence cannot safely establish a continuation.
type Page[T any] struct {
	Items         []T
	NextCursor    string
	Coverage      Coverage
	PageInfo      PageInfo
	ServiceErrors []ServiceError
}

// MediaPage and RequestPage make the common concrete return shapes discoverable
// while preserving the generic Page API used by other standalone clients.
type MediaPage = Page[MediaObservation]
type RequestPage = Page[RequestObservation]

// ProviderRelationship preserves provider identity returned by Seerr.
type ProviderRelationship struct {
	Provider string
	ID       string
}

// ServiceRelationship preserves one Arr service relationship. Service
// availability remains independent from media availability and request state.
type ServiceRelationship struct {
	Kind       string
	ServiceID  string
	ExternalID string
	Slug       string
	Is4K       bool
}

// AvailabilityObservation is Seerr's media availability evidence. It is
// deliberately separate from request status and Jellyfin playability.
type AvailabilityObservation struct {
	Known              bool
	Available          bool
	PartiallyAvailable bool
	NativeStatus       int
	NativeStatusName   string
	ObservedAt         time.Time
	Reason             string
}

// MediaStatus is Seerr's native media status enum. Unknown future values stay
// visible through NativeStatus and map to "unknown".
type MediaStatus int

const (
	MediaStatusUnknown            MediaStatus = 1
	MediaStatusPending            MediaStatus = 2
	MediaStatusProcessing         MediaStatus = 3
	MediaStatusPartiallyAvailable MediaStatus = 4
	MediaStatusAvailable          MediaStatus = 5
	MediaStatusBlocklisted        MediaStatus = 6
	MediaStatusDeleted            MediaStatus = 7
)

func (status MediaStatus) String() string {
	switch status {
	case MediaStatusUnknown:
		return "unknown"
	case MediaStatusPending:
		return "pending"
	case MediaStatusProcessing:
		return "processing"
	case MediaStatusPartiallyAvailable:
		return "partially_available"
	case MediaStatusAvailable:
		return "available"
	case MediaStatusBlocklisted:
		return "blocklisted"
	case MediaStatusDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

// RequestStatus is Seerr's native request status enum.
type RequestStatus int

const (
	RequestStatusPending   RequestStatus = 1
	RequestStatusApproved  RequestStatus = 2
	RequestStatusDeclined  RequestStatus = 3
	RequestStatusFailed    RequestStatus = 4
	RequestStatusCompleted RequestStatus = 5
)

func (status RequestStatus) String() string {
	switch status {
	case RequestStatusPending:
		return "pending"
	case RequestStatusApproved:
		return "approved"
	case RequestStatusDeclined:
		return "declined"
	case RequestStatusFailed:
		return "failed"
	case RequestStatusCompleted:
		return "completed"
	default:
		return "unknown"
	}
}

// SeasonObservation preserves season-level native status and identity.
type SeasonObservation struct {
	ID                  int64
	SeasonNumber        int
	NativeStatus        int
	NativeStatusKnown   bool
	NativeStatusName    string
	NativeStatus4K      int
	NativeStatus4KKnown bool
	NativeStatus4KName  string
	ObservedAt          time.Time
}

// RequestSummary preserves a request reference nested in a media record.
type RequestSummary struct {
	ID                int64
	NativeStatus      int
	NativeStatusKnown bool
	NativeStatusName  string
	Type              string
	Is4K              bool
	CreatedAt         *time.Time
	UpdatedAt         *time.Time
}

// MediaObservation is a normalized Seerr media record. Generated upstream
// DTOs do not appear in this public type.
type MediaObservation struct {
	ID                    int64
	ScopedIdentity        string
	MediaType             string
	TMDBID                string
	TVDBID                string
	IMDBID                string
	ProviderIDs           map[string]string
	ProviderRelationships []ProviderRelationship

	NativeStatus         int
	NativeStatusKnown    bool
	NativeStatusName     string
	NativeStatus4K       int
	NativeStatus4KKnown  bool
	NativeStatus4KName   string
	Requests             []RequestSummary
	Seasons              []SeasonObservation
	ServiceRelationships []ServiceRelationship

	JellyfinMediaID   string
	JellyfinMediaID4K string
	RatingKey         string
	RatingKey4K       string
	MediaAddedAt      *time.Time
	SourceCreatedAt   *time.Time
	SourceUpdatedAt   *time.Time

	Availability AvailabilityObservation
	Evidence     []string
}

// RequestObservation is a normalized Seerr request record. Nested media
// availability remains independent from this request's native status.
type RequestObservation struct {
	ID                   int64
	ScopedIdentity       string
	Type                 string
	NativeStatus         int
	NativeStatusKnown    bool
	NativeStatusName     string
	Media                MediaObservation
	MediaKnown           bool
	SeasonCount          int
	Seasons              []SeasonObservation
	Is4K                 bool
	ServerID             int64
	ServerIDKnown        bool
	ProfileID            int64
	ProfileIDKnown       bool
	RootFolder           string
	LanguageProfileID    int64
	LanguageProfileKnown bool
	Tags                 []int64
	IsAutoRequest        bool
	IgnoreQuota          bool
	ServiceRelationships []ServiceRelationship
	SourceCreatedAt      *time.Time
	SourceUpdatedAt      *time.Time
	Evidence             []string
}

// ServiceError preserves typed serviceErrors evidence without exposing
// arbitrary upstream JSON.
type ServiceError struct {
	Kind string
	ID   int64
	Name string
}

// ListMedia reads one bounded page from Seerr's /media catalog.
func (client *Client) ListMedia(ctx context.Context, cursor string, requestedLimit int) (MediaPage, error) {
	var result MediaPage
	state, err := client.startCursor("media", cursor, requestedLimit)
	if err != nil {
		return result, err
	}
	if err := contextError(ctx); err != nil {
		return result, err
	}
	body, err := client.get(ctx, "seerr.media.list", apiMedia, url.Values{
		"take": []string{strconv.Itoa(state.Limit)},
		"skip": []string{strconv.Itoa(state.Skip)},
	})
	if err != nil {
		return result, err
	}
	values, info, hasPageInfo, rawCount, fingerprint, err := decodeMediaPage(body)
	if err != nil {
		return result, malformed("seerr.media.list")
	}
	result.PageInfo = info
	preparePageState(&state, info, hasPageInfo, rawCount, fingerprint)
	if rawCount > state.Limit {
		addReason(&state.Reasons, "pagination_response_exceeded_limit")
		rawCount = state.Limit
		values = values[:rawCount]
	}
	if remaining := client.maxRecords - state.ObservedCount; remaining < len(values) {
		if remaining < 0 {
			remaining = 0
		}
		values = values[:remaining]
		addReason(&state.Reasons, "records_limit")
	}

	result.Items = make([]MediaObservation, 0, len(values))
	for index, value := range values {
		item, reasons, err := client.normalizeMedia(value)
		for _, reason := range reasons {
			addReason(&state.Reasons, fmt.Sprintf("media_%d_%s", index, reason))
		}
		if err != nil {
			addReason(&state.Reasons, fmt.Sprintf("media_%d_identity_unknown", index))
			continue
		}
		key := "media:" + strconv.FormatInt(item.ID, 10)
		if state.hasSeen(key) {
			addReason(&state.Reasons, "pagination_overlap")
			continue
		}
		if len(state.SeenIDs) >= maxCursorSeenIDs {
			addReason(&state.Reasons, "identity_window_limit")
			break
		}
		state.SeenIDs = append(state.SeenIDs, key)
		result.Items = append(result.Items, item)
	}
	state.Skip += rawCount
	state.PageCount++
	state.ObservedCount += len(result.Items)
	more := client.more(&state, info, hasPageInfo, rawCount)
	result = client.finishMediaPage(result, state, more)
	return result, nil
}

// ListMediaDetailed is an explicit alias for ListMedia.
func (client *Client) ListMediaDetailed(ctx context.Context, cursor string, requestedLimit int) (MediaPage, error) {
	return client.ListMedia(ctx, cursor, requestedLimit)
}

// Media is a concise alias for ListMedia.
func (client *Client) Media(ctx context.Context, cursor string, requestedLimit int) (MediaPage, error) {
	return client.ListMedia(ctx, cursor, requestedLimit)
}

// ListRequests reads one bounded page from Seerr's /request catalog.
func (client *Client) ListRequests(ctx context.Context, cursor string, requestedLimit int) (RequestPage, error) {
	var result RequestPage
	state, err := client.startCursor("requests", cursor, requestedLimit)
	if err != nil {
		return result, err
	}
	if err := contextError(ctx); err != nil {
		return result, err
	}
	body, err := client.get(ctx, "seerr.requests.list", apiRequests, url.Values{
		"take": []string{strconv.Itoa(state.Limit)},
		"skip": []string{strconv.Itoa(state.Skip)},
	})
	if err != nil {
		return result, err
	}
	values, info, hasPageInfo, rawCount, fingerprint, services, err := decodeRequestPage(body)
	if err != nil {
		return result, malformed("seerr.requests.list")
	}
	result.PageInfo = info
	result.ServiceErrors = services
	preparePageState(&state, info, hasPageInfo, rawCount, fingerprint)
	if rawCount > state.Limit {
		addReason(&state.Reasons, "pagination_response_exceeded_limit")
		rawCount = state.Limit
		values = values[:rawCount]
	}
	if remaining := client.maxRecords - state.ObservedCount; remaining < len(values) {
		if remaining < 0 {
			remaining = 0
		}
		values = values[:remaining]
		addReason(&state.Reasons, "records_limit")
	}

	result.Items = make([]RequestObservation, 0, len(values))
	for index, value := range values {
		item, reasons, err := client.normalizeRequest(value)
		for _, reason := range reasons {
			addReason(&state.Reasons, fmt.Sprintf("request_%d_%s", index, reason))
		}
		if err != nil {
			addReason(&state.Reasons, fmt.Sprintf("request_%d_identity_unknown", index))
			continue
		}
		key := "request:" + strconv.FormatInt(item.ID, 10)
		if state.hasSeen(key) {
			addReason(&state.Reasons, "pagination_overlap")
			continue
		}
		if len(state.SeenIDs) >= maxCursorSeenIDs {
			addReason(&state.Reasons, "identity_window_limit")
			break
		}
		state.SeenIDs = append(state.SeenIDs, key)
		result.Items = append(result.Items, item)
	}
	state.Skip += rawCount
	state.PageCount++
	state.ObservedCount += len(result.Items)
	more := client.more(&state, info, hasPageInfo, rawCount)
	result = client.finishRequestPage(result, state, more)
	return result, nil
}

// ListRequestsDetailed is an explicit alias for ListRequests.
func (client *Client) ListRequestsDetailed(ctx context.Context, cursor string, requestedLimit int) (RequestPage, error) {
	return client.ListRequests(ctx, cursor, requestedLimit)
}

// Requests is a concise alias for ListRequests.
func (client *Client) Requests(ctx context.Context, cursor string, requestedLimit int) (RequestPage, error) {
	return client.ListRequests(ctx, cursor, requestedLimit)
}

// ListAllMedia consumes authenticated continuation pages until a safe native
// boundary or a configured limit is reached.
func (client *Client) ListAllMedia(ctx context.Context, requestedLimit int) (MediaPage, error) {
	var all MediaPage
	cursor := ""
	for {
		page, err := client.ListMedia(ctx, cursor, requestedLimit)
		if err != nil {
			return all, err
		}
		all.Items = append(all.Items, page.Items...)
		all.PageInfo = page.PageInfo
		all.Coverage = page.Coverage
		if page.NextCursor == "" {
			all.Coverage.ObservedCount = len(all.Items)
			return all, nil
		}
		cursor = page.NextCursor
	}
}

// ListAllRequests is the request-catalog equivalent of ListAllMedia.
func (client *Client) ListAllRequests(ctx context.Context, requestedLimit int) (RequestPage, error) {
	var all RequestPage
	cursor := ""
	for {
		page, err := client.ListRequests(ctx, cursor, requestedLimit)
		if err != nil {
			return all, err
		}
		all.Items = append(all.Items, page.Items...)
		all.PageInfo = page.PageInfo
		all.Coverage = page.Coverage
		all.ServiceErrors = append(all.ServiceErrors, page.ServiceErrors...)
		if page.NextCursor == "" {
			all.Coverage.ObservedCount = len(all.Items)
			return all, nil
		}
		cursor = page.NextCursor
	}
}

type cursorState struct {
	Collection       string    `json:"collection"`
	Limit            int       `json:"limit"`
	Skip             int       `json:"skip"`
	PageCount        int       `json:"pageCount"`
	ObservedCount    int       `json:"observedCount"`
	ExpectedTotal    int       `json:"expectedTotal"`
	ExpectedPages    int       `json:"expectedPages"`
	PageSize         int       `json:"pageSize"`
	SnapshotRevision string    `json:"snapshotRevision"`
	StartedAt        time.Time `json:"startedAt"`
	SeenIDs          []string  `json:"seenIds,omitempty"`
	Reasons          []string  `json:"reasons,omitempty"`
}

func (client *Client) startCursor(collection, value string, requestedLimit int) (cursorState, error) {
	limit, err := client.pageLimit(requestedLimit)
	if err != nil {
		return cursorState{}, err
	}
	if value == "" {
		return cursorState{Collection: collection, Limit: limit, ExpectedTotal: -1, StartedAt: time.Now().UTC()}, nil
	}
	state, err := client.decodeCursor(value)
	if err != nil {
		return cursorState{}, err
	}
	if state.Collection != collection || (requestedLimit > 0 && state.Limit != limit) || state.PageCount >= client.maxPages || state.ObservedCount >= client.maxRecords {
		return cursorState{}, invalidInput("seerr.catalog.cursor")
	}
	return state, nil
}

func (client *Client) pageLimit(requested int) (int, error) {
	if requested <= 0 {
		return client.maxPageSize, nil
	}
	if requested > client.maxPageSize {
		return 0, invalidInput("seerr.catalog.limit")
	}
	return requested, nil
}

func (client *Client) encodeCursor(state cursorState) (string, error) {
	if len(state.SeenIDs) > maxCursorSeenIDs || len(state.Reasons) > maxReasonCodes {
		return "", invalidInput("seerr.catalog.cursor")
	}
	payload, err := json.Marshal(state)
	if err != nil || len(payload) > maxCursorBytes {
		return "", invalidInput("seerr.catalog.cursor")
	}
	mac := hmac.New(sha256.New, client.cursorKey)
	_, _ = mac.Write(payload)
	value := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(value) > maxCursorBytes {
		return "", invalidInput("seerr.catalog.cursor")
	}
	return value, nil
}

func (client *Client) decodeCursor(value string) (cursorState, error) {
	if len(value) > maxCursorBytes {
		return cursorState{}, invalidInput("seerr.catalog.cursor")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return cursorState{}, invalidInput("seerr.catalog.cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) > maxCursorBytes {
		return cursorState{}, invalidInput("seerr.catalog.cursor")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursorState{}, invalidInput("seerr.catalog.cursor")
	}
	mac := hmac.New(sha256.New, client.cursorKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursorState{}, invalidInput("seerr.catalog.cursor")
	}
	var state cursorState
	if err := decodeJSON(payload, &state); err != nil || state.Collection == "" || state.Limit <= 0 || state.Limit > client.maxPageSize || state.Skip < 0 || state.PageCount < 0 || state.ObservedCount < 0 || state.ObservedCount > client.maxRecords || state.ExpectedTotal < -1 || state.ExpectedPages < 0 || state.PageSize < 0 || len(state.SeenIDs) > maxCursorSeenIDs || len(state.Reasons) > maxReasonCodes || state.StartedAt.IsZero() {
		return cursorState{}, invalidInput("seerr.catalog.cursor")
	}
	return state, nil
}

func (state cursorState) hasSeen(value string) bool {
	for _, existing := range state.SeenIDs {
		if existing == value {
			return true
		}
	}
	return false
}

func preparePageState(state *cursorState, info PageInfo, hasPageInfo bool, rawCount int, fingerprint string) {
	if state.SnapshotRevision == "" {
		state.SnapshotRevision = fingerprint
	}
	if !hasPageInfo {
		addReason(&state.Reasons, "pagination_page_info_missing")
		return
	}
	if !info.Complete {
		addReason(&state.Reasons, "pagination_page_info_incomplete")
		return
	}
	if state.ExpectedTotal < 0 {
		state.ExpectedTotal = info.Results
		state.ExpectedPages = info.Pages
		state.PageSize = info.PageSize
	} else if state.ExpectedTotal != info.Results || state.ExpectedPages != info.Pages || state.PageSize != info.PageSize {
		addReason(&state.Reasons, "pagination_page_info_changed")
	}
	expectedPage := 1
	if state.PageSize > 0 {
		expectedPage = state.Skip/state.PageSize + 1
	}
	if info.Page != expectedPage {
		addReason(&state.Reasons, "pagination_page_number_changed")
	}
	if info.Results > info.PageSize || info.Pages > 1 {
		addReason(&state.Reasons, "pagination_snapshot_unverified")
	}
	if rawCount > 0 && rawCount < info.PageSize && state.Skip+rawCount < info.Results {
		addReason(&state.Reasons, "pagination_short_page_before_total")
	}
}

func (client *Client) more(state *cursorState, info PageInfo, hasPageInfo bool, rawCount int) bool {
	if !hasPageInfo || !info.Complete || hasReason(state.Reasons, "pagination_page_info_changed") || hasReason(state.Reasons, "pagination_page_number_changed") || hasReason(state.Reasons, "pagination_short_page_before_total") {
		return false
	}
	byTotal := state.Skip < info.Results
	byPage := info.Page < info.Pages
	if byTotal != byPage {
		addReason(&state.Reasons, "pagination_page_info_inconsistent")
		return false
	}
	if rawCount == 0 && byTotal {
		addReason(&state.Reasons, "pagination_empty_before_total")
		return false
	}
	if state.PageCount >= client.maxPages && byTotal {
		addReason(&state.Reasons, "pagination_limit")
		return false
	}
	if state.ObservedCount >= client.maxRecords && byTotal {
		addReason(&state.Reasons, "records_limit")
		return false
	}
	return byTotal
}

func (client *Client) finishMediaPage(result MediaPage, state cursorState, more bool) MediaPage {
	result.Coverage = coverageFromState(state, more)
	if more {
		cursor, err := client.encodeCursor(state)
		if err != nil {
			addReason(&state.Reasons, "pagination_cursor_limit")
			result.Coverage = coverageFromState(state, false)
		} else {
			result.NextCursor = cursor
			result.Coverage.Completeness = CompletenessPartial
		}
	}
	if result.NextCursor == "" {
		now := time.Now().UTC()
		result.Coverage.CompletedAt = &now
		result.Coverage.ObservedAt = now
	}
	return result
}

func (client *Client) finishRequestPage(result RequestPage, state cursorState, more bool) RequestPage {
	result.Coverage = coverageFromState(state, more)
	if more {
		cursor, err := client.encodeCursor(state)
		if err != nil {
			addReason(&state.Reasons, "pagination_cursor_limit")
			result.Coverage = coverageFromState(state, false)
		} else {
			result.NextCursor = cursor
			result.Coverage.Completeness = CompletenessPartial
		}
	}
	if result.NextCursor == "" {
		now := time.Now().UTC()
		result.Coverage.CompletedAt = &now
		result.Coverage.ObservedAt = now
	}
	return result
}

func coverageFromState(state cursorState, more bool) Coverage {
	coverage := Coverage{Completeness: CompletenessComplete, ObservedCount: state.ObservedCount, ExpectedCount: maxInt(state.ExpectedTotal, 0), ObservedAt: time.Now().UTC()}
	if state.ExpectedTotal < 0 || hasReason(state.Reasons, "pagination_page_info_missing") || hasReason(state.Reasons, "pagination_page_info_incomplete") {
		coverage.Completeness = CompletenessUnknown
	}
	if more || len(state.Reasons) > 0 {
		if coverage.Completeness != CompletenessUnknown {
			coverage.Completeness = CompletenessPartial
		}
	}
	coverage.ReasonCodes = append([]string(nil), state.Reasons...)
	if len(coverage.ReasonCodes) > 0 {
		coverage.Reason = coverage.ReasonCodes[0]
	}
	return coverage
}

func (client *Client) normalizeMedia(value generated.Media) (MediaObservation, []string, error) {
	if value.Id <= 0 {
		return MediaObservation{}, nil, errors.New("media identity is invalid")
	}
	now := time.Now().UTC()
	result := MediaObservation{ID: value.Id, ScopedIdentity: client.ScopedIdentity(value.Id), ProviderIDs: make(map[string]string)}
	var reasons []string
	if value.MediaType != nil {
		if !validBoundedText(*value.MediaType, 64, false) {
			return MediaObservation{}, nil, errors.New("media type is invalid")
		}
		result.MediaType = *value.MediaType
	}
	if value.TmdbId != nil {
		if *value.TmdbId < 0 {
			return MediaObservation{}, nil, errors.New("tmdb id is invalid")
		}
		result.TMDBID = strconv.FormatInt(*value.TmdbId, 10)
		result.ProviderIDs["tmdb"] = result.TMDBID
	}
	if value.TvdbId != nil {
		if *value.TvdbId < 0 {
			return MediaObservation{}, nil, errors.New("tvdb id is invalid")
		}
		result.TVDBID = strconv.FormatInt(*value.TvdbId, 10)
		result.ProviderIDs["tvdb"] = result.TVDBID
	}
	if value.ImdbId != nil {
		if !validBoundedText(*value.ImdbId, maxProviderChars, false) {
			return MediaObservation{}, nil, errors.New("imdb id is invalid")
		}
		result.IMDBID = *value.ImdbId
		if result.IMDBID != "" {
			result.ProviderIDs["imdb"] = result.IMDBID
		}
	}
	result.ProviderRelationships = providerRelationships(result.ProviderIDs)
	result.NativeStatus, result.NativeStatusKnown, result.NativeStatusName, result.Availability = normalizeAvailability(value.Status, now)
	if value.Status != nil && *value.Status < 0 {
		return MediaObservation{}, nil, errors.New("media status is invalid")
	}
	if value.Status == nil {
		reasons = append(reasons, "native_status_missing")
	}
	if result.Availability.Reason != "" && result.Availability.Reason != "native_media_status_not_available" {
		reasons = append(reasons, result.Availability.Reason)
	}
	result.NativeStatus4K, result.NativeStatus4KKnown, result.NativeStatus4KName = normalizeStatus(value.Status4k)
	if value.Status4k != nil && *value.Status4k < 0 {
		return MediaObservation{}, nil, errors.New("media 4k status is invalid")
	}
	if value.Status4k == nil {
		reasons = append(reasons, "native_status_4k_missing")
	}
	if value.Status4k != nil && result.NativeStatus4KName == "unknown" {
		reasons = append(reasons, "native_media_status_4k_unknown")
	}
	if value.Requests != nil {
		if len(*value.Requests) > maxNestedItems {
			return MediaObservation{}, nil, errors.New("media request bound exceeded")
		}
		for _, nested := range *value.Requests {
			mapped, reason := normalizeRequestSummary(nested, now)
			if reason != "" {
				reasons = append(reasons, "nested_request_"+reason)
				continue
			}
			result.Requests = append(result.Requests, mapped)
		}
	}
	if value.Seasons != nil {
		if len(*value.Seasons) > maxNestedItems {
			return MediaObservation{}, nil, errors.New("media season bound exceeded")
		}
		for _, nested := range *value.Seasons {
			mapped, reason := normalizeSeason(nested, now)
			if reason != "" {
				reasons = append(reasons, "nested_season_"+reason)
				continue
			}
			result.Seasons = append(result.Seasons, mapped)
		}
	}
	result.ServiceRelationships = serviceRelationships(value, false)
	result.JellyfinMediaID = optionalString(value.JellyfinMediaId, maxTextChars, &reasons, "jellyfin_media_id")
	result.JellyfinMediaID4K = optionalString(value.JellyfinMediaId4k, maxTextChars, &reasons, "jellyfin_media_id_4k")
	result.RatingKey = optionalString(value.RatingKey, maxTextChars, &reasons, "rating_key")
	result.RatingKey4K = optionalString(value.RatingKey4k, maxTextChars, &reasons, "rating_key_4k")
	result.MediaAddedAt = optionalTime(value.MediaAddedAt, &reasons, "media_added_at")
	result.SourceCreatedAt = optionalTime(value.CreatedAt, &reasons, "source_created_at")
	result.SourceUpdatedAt = optionalTime(value.UpdatedAt, &reasons, "source_updated_at")
	result.Evidence = uniqueStrings(reasons)
	return result, reasons, nil
}

func (client *Client) normalizeRequest(value generated.Request) (RequestObservation, []string, error) {
	if value.Id <= 0 {
		return RequestObservation{}, nil, errors.New("request identity is invalid")
	}
	now := time.Now().UTC()
	result := RequestObservation{ID: value.Id, ScopedIdentity: client.ScopedIdentity(value.Id)}
	var reasons []string
	result.NativeStatus, result.NativeStatusKnown, result.NativeStatusName = normalizeRequestStatus(value.Status)
	if value.Status != nil && *value.Status < 0 {
		return RequestObservation{}, nil, errors.New("request status is invalid")
	}
	if value.Status == nil {
		reasons = append(reasons, "native_status_missing")
	}
	if value.Type != nil {
		if !validBoundedText(*value.Type, 64, false) {
			return RequestObservation{}, nil, errors.New("request type is invalid")
		}
		result.Type = *value.Type
	}
	if value.Media != nil {
		media, mediaReasons, mediaErr := client.normalizeMedia(*value.Media)
		if mediaErr != nil {
			reasons = append(reasons, "nested_media_identity_unknown")
		} else {
			result.Media = media
			result.MediaKnown = true
			for _, reason := range mediaReasons {
				reasons = append(reasons, "nested_media_"+reason)
			}
		}
	} else {
		reasons = append(reasons, "nested_media_missing")
	}
	if value.SeasonCount != nil {
		if *value.SeasonCount < 0 {
			return RequestObservation{}, nil, errors.New("request season count is invalid")
		}
		result.SeasonCount = int(*value.SeasonCount)
	}
	if value.Seasons != nil {
		if len(*value.Seasons) > maxNestedItems {
			return RequestObservation{}, nil, errors.New("request season bound exceeded")
		}
		for _, nested := range *value.Seasons {
			mapped, reason := normalizeSeason(nested, now)
			if reason != "" {
				reasons = append(reasons, "nested_season_"+reason)
				continue
			}
			result.Seasons = append(result.Seasons, mapped)
		}
	}
	if value.ServerId != nil {
		if *value.ServerId < 0 {
			return RequestObservation{}, nil, errors.New("request server id is invalid")
		}
		result.ServerID, result.ServerIDKnown = *value.ServerId, true
	}
	if value.ProfileId != nil {
		if *value.ProfileId < 0 {
			return RequestObservation{}, nil, errors.New("request profile id is invalid")
		}
		result.ProfileID, result.ProfileIDKnown = *value.ProfileId, true
	}
	if value.RootFolder != nil {
		if !validBoundedText(*value.RootFolder, maxTextChars, false) {
			return RequestObservation{}, nil, errors.New("request root folder is invalid")
		}
		result.RootFolder = *value.RootFolder
	}
	if value.LanguageProfileId != nil {
		if *value.LanguageProfileId < 0 {
			return RequestObservation{}, nil, errors.New("request language profile id is invalid")
		}
		result.LanguageProfileID, result.LanguageProfileKnown = *value.LanguageProfileId, true
	}
	if value.Tags != nil {
		if len(*value.Tags) > maxTags {
			return RequestObservation{}, nil, errors.New("request tag bound exceeded")
		}
		result.Tags = make([]int64, 0, len(*value.Tags))
		for _, tag := range *value.Tags {
			if tag < 0 {
				return RequestObservation{}, nil, errors.New("request tag is invalid")
			}
			result.Tags = append(result.Tags, tag)
		}
	}
	result.Is4K = value.Is4k != nil && *value.Is4k
	result.IsAutoRequest = value.IsAutoRequest != nil && *value.IsAutoRequest
	result.IgnoreQuota = value.IgnoreQuota != nil && *value.IgnoreQuota
	result.SourceCreatedAt = optionalTime(value.CreatedAt, &reasons, "source_created_at")
	result.SourceUpdatedAt = optionalTime(value.UpdatedAt, &reasons, "source_updated_at")
	if result.MediaKnown {
		result.ServiceRelationships = append([]ServiceRelationship(nil), result.Media.ServiceRelationships...)
		result.Evidence = append(result.Evidence, result.Media.Evidence...)
	}
	result.Evidence = uniqueStrings(append(reasons, result.Evidence...))
	return result, result.Evidence, nil
}

func normalizeAvailability(raw *int32, now time.Time) (int, bool, string, AvailabilityObservation) {
	status, known, name := normalizeStatus(raw)
	availability := AvailabilityObservation{Known: known, NativeStatus: status, NativeStatusName: name, ObservedAt: now}
	if !known {
		availability.Reason = "native_media_status_missing"
		return status, known, name, availability
	}
	switch MediaStatus(status) {
	case MediaStatusAvailable:
		availability.Available = true
	case MediaStatusPartiallyAvailable:
		availability.PartiallyAvailable = true
	case MediaStatusDeleted:
		availability.Reason = "native_media_status_deleted"
	default:
		if name == "unknown" {
			availability.Reason = "native_media_status_unknown"
		} else {
			availability.Reason = "native_media_status_not_available"
		}
	}
	return status, known, name, availability
}

func normalizeStatus(raw *int32) (int, bool, string) {
	if raw == nil {
		return 0, false, "unknown"
	}
	status := int(*raw)
	return status, true, MediaStatus(status).String()
}

func normalizeRequestStatus(raw *int32) (int, bool, string) {
	if raw == nil {
		return 0, false, "unknown"
	}
	status := int(*raw)
	return status, true, RequestStatus(status).String()
}

func normalizeRequestSummary(value generated.RequestSummary, now time.Time) (RequestSummary, string) {
	if value.Id <= 0 {
		return RequestSummary{}, "identity_unknown"
	}
	result := RequestSummary{ID: value.Id}
	result.NativeStatus, result.NativeStatusKnown, result.NativeStatusName = normalizeRequestStatus(value.Status)
	if value.Status != nil && *value.Status < 0 {
		return RequestSummary{}, "status_invalid"
	}
	if value.Type != nil {
		if !validBoundedText(*value.Type, 64, false) {
			return RequestSummary{}, "type_invalid"
		}
		result.Type = *value.Type
	}
	if value.Is4k != nil {
		result.Is4K = *value.Is4k
	}
	var reasons []string
	result.CreatedAt = optionalTime(value.CreatedAt, &reasons, "created_at")
	result.UpdatedAt = optionalTime(value.UpdatedAt, &reasons, "updated_at")
	if len(reasons) > 0 {
		return result, reasons[0]
	}
	return result, ""
}

func normalizeSeason(value generated.Season, now time.Time) (SeasonObservation, string) {
	if value.Id <= 0 {
		return SeasonObservation{}, "identity_unknown"
	}
	result := SeasonObservation{ID: value.Id, ObservedAt: now}
	if value.SeasonNumber != nil {
		if *value.SeasonNumber < 0 {
			return SeasonObservation{}, "number_invalid"
		}
		result.SeasonNumber = int(*value.SeasonNumber)
	}
	result.NativeStatus, result.NativeStatusKnown, result.NativeStatusName = normalizeStatus(value.Status)
	result.NativeStatus4K, result.NativeStatus4KKnown, result.NativeStatus4KName = normalizeStatus(value.Status4k)
	if value.Status != nil && *value.Status < 0 || value.Status4k != nil && *value.Status4k < 0 {
		return SeasonObservation{}, "status_invalid"
	}
	return result, ""
}

func serviceRelationships(value generated.Media, is4K bool) []ServiceRelationship {
	result := make([]ServiceRelationship, 0, 2)
	if value.ServiceId != nil || value.ExternalServiceId != nil {
		result = append(result, ServiceRelationship{Kind: serviceKind(value.MediaType), ServiceID: optionalInt64(value.ServiceId), ExternalID: optionalInt64(value.ExternalServiceId), Slug: optionalStringValue(value.ExternalServiceSlug), Is4K: is4K})
	}
	if value.ServiceId4k != nil || value.ExternalServiceId4k != nil {
		result = append(result, ServiceRelationship{Kind: serviceKind(value.MediaType), ServiceID: optionalInt64(value.ServiceId4k), ExternalID: optionalInt64(value.ExternalServiceId4k), Slug: optionalStringValue(value.ExternalServiceSlug4k), Is4K: true})
	}
	return result
}

func serviceKind(value *string) string {
	if value == nil {
		return "unknown"
	}
	switch strings.ToLower(strings.TrimSpace(*value)) {
	case "movie":
		return "radarr"
	case "tv", "series":
		return "sonarr"
	default:
		return "unknown"
	}
}

func providerRelationships(values map[string]string) []ProviderRelationship {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]ProviderRelationship, 0, len(keys))
	for _, key := range keys {
		result = append(result, ProviderRelationship{Provider: key, ID: values[key]})
	}
	return result
}

func optionalInt64(value *int64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatInt(*value, 10)
}

func optionalStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func optionalString(value *string, limit int, reasons *[]string, reason string) string {
	if value == nil {
		return ""
	}
	if !validBoundedText(*value, limit, false) {
		*reasons = append(*reasons, reason+"_invalid")
		return ""
	}
	return *value
}

func optionalTime(value *string, reasons *[]string, reason string) *time.Time {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		*reasons = append(*reasons, reason+"_invalid")
		return nil
	}
	parsed = parsed.UTC()
	return &parsed
}

func decodeMediaPage(data []byte) ([]generated.Media, PageInfo, bool, int, string, error) {
	object, resultRaw, pageInfoRaw, err := decodeEnvelope(data)
	if err != nil {
		return nil, PageInfo{}, false, 0, "", err
	}
	_ = object
	var envelope generated.MediaPage
	if err := decodeJSON(data, &envelope); err != nil {
		return nil, PageInfo{}, false, 0, "", err
	}
	var values []generated.Media
	if err := decodeJSON(resultRaw, &values); err != nil || values == nil || len(values) > maxPageArray {
		return nil, PageInfo{}, false, 0, "", errors.New("media result array is invalid")
	}
	info, present, err := decodePageInfo(pageInfoRaw, len(values))
	if err != nil {
		return nil, PageInfo{}, false, 0, "", err
	}
	return values, info, present, len(values), digest(data), nil
}

func decodeRequestPage(data []byte) ([]generated.Request, PageInfo, bool, int, string, []ServiceError, error) {
	object, resultRaw, pageInfoRaw, err := decodeEnvelope(data)
	if err != nil {
		return nil, PageInfo{}, false, 0, "", nil, err
	}
	var envelope generated.RequestPage
	if err := decodeJSON(data, &envelope); err != nil {
		return nil, PageInfo{}, false, 0, "", nil, err
	}
	var values []generated.Request
	if err := decodeJSON(resultRaw, &values); err != nil || values == nil || len(values) > maxPageArray {
		return nil, PageInfo{}, false, 0, "", nil, errors.New("request result array is invalid")
	}
	info, present, err := decodePageInfo(pageInfoRaw, len(values))
	if err != nil {
		return nil, PageInfo{}, false, 0, "", nil, err
	}
	services, err := normalizeServiceErrors(object["serviceErrors"])
	if err != nil {
		return nil, PageInfo{}, false, 0, "", nil, err
	}
	return values, info, present, len(values), digest(data), services, nil
}

func decodeEnvelope(data []byte) (map[string]json.RawMessage, json.RawMessage, json.RawMessage, error) {
	if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, nil, nil, errors.New("Seerr page is empty")
	}
	if err := scanJSON(data); err != nil {
		return nil, nil, nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return nil, nil, nil, errors.New("Seerr page object is required")
	}
	resultRaw, ok := object["results"]
	if !ok || bytes.Equal(bytes.TrimSpace(resultRaw), []byte("null")) {
		return nil, nil, nil, errors.New("Seerr page results are required")
	}
	var results []json.RawMessage
	if err := json.Unmarshal(resultRaw, &results); err != nil || results == nil {
		return nil, nil, nil, errors.New("Seerr page results array is required")
	}
	return object, resultRaw, object["pageInfo"], nil
}

func decodePageInfo(raw json.RawMessage, fallbackResults int) (PageInfo, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return PageInfo{}, false, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return PageInfo{}, true, errors.New("Seerr pageInfo object is required")
	}
	info := PageInfo{Present: true}
	fields := []struct {
		name string
		dest *int
		has  *bool
	}{{"pages", &info.Pages, &info.HasPages}, {"pageSize", &info.PageSize, &info.HasPageSize}, {"results", &info.Results, &info.HasResults}, {"page", &info.Page, &info.HasPage}}
	for _, field := range fields {
		value, present := object[field.name]
		if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			continue
		}
		parsed, err := parseJSONInt(value)
		if err != nil || parsed < 0 || parsed > math.MaxInt32 {
			return PageInfo{}, true, errors.New("Seerr pageInfo integer is invalid")
		}
		*field.dest = int(parsed)
		*field.has = true
	}
	if info.HasResults && info.Results < fallbackResults {
		return PageInfo{}, true, errors.New("Seerr pageInfo total is smaller than the page")
	}
	if info.HasPages && info.Pages == 0 && info.HasResults && info.Results != 0 {
		return PageInfo{}, true, errors.New("Seerr pageInfo pages contradict total")
	}
	if info.HasPageSize && info.PageSize == 0 && info.HasResults && info.Results != 0 {
		return PageInfo{}, true, errors.New("Seerr pageInfo size contradicts total")
	}
	if info.HasPage && info.Page == 0 {
		return PageInfo{}, true, errors.New("Seerr pageInfo page is invalid")
	}
	if info.HasPages && info.Pages > 0 && info.HasPage && info.Page > info.Pages {
		return PageInfo{}, true, errors.New("Seerr pageInfo page exceeds pages")
	}
	info.Complete = info.HasPages && info.HasPageSize && info.HasResults && info.HasPage && info.Page >= 1 && ((info.Results == 0 && info.Pages == 0) || (info.Pages >= 1 && info.PageSize > 0))
	return info, true, nil
}

func normalizeServiceErrors(raw json.RawMessage) ([]ServiceError, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var values map[string][]generated.ServiceError
	if err := json.Unmarshal(trimmed, &values); err != nil {
		return nil, errors.New("Seerr serviceErrors object is invalid")
	}
	if values == nil {
		return nil, errors.New("Seerr serviceErrors object is invalid")
	}
	kinds := make([]string, 0, len(values))
	for kind := range values {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	result := make([]ServiceError, 0)
	for _, kind := range kinds {
		if !validBoundedText(kind, 128, true) {
			continue
		}
		for _, value := range values[kind] {
			if len(result) >= maxNestedItems {
				return result, nil
			}
			item := ServiceError{Kind: kind}
			if value.Id != nil && *value.Id >= 0 {
				item.ID = *value.Id
			}
			if value.Name != nil && validBoundedText(*value.Name, maxTextChars, false) {
				item.Name = *value.Name
			}
			result = append(result, item)
		}
	}
	return result, nil
}

func (client *Client) get(ctx context.Context, operation, resource string, query url.Values) ([]byte, error) {
	body, status, err := client.request(ctx, operation, resource, query)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, statusError(operation, status)
	}
	return body, nil
}

func (client *Client) request(ctx context.Context, operation, resource string, query url.Values) ([]byte, int, error) {
	requestContext, cancel := client.requestContext(ctx)
	defer cancel()
	if err := requestContext.Err(); err != nil {
		return nil, 0, err
	}
	requestURL := *client.endpoint
	requestURL.Path = client.apiPath(resource)
	requestURL.RawPath = ""
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, 0, errors.New("Seerr request could not be created")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", client.userAgent)
	switch client.authKind {
	case "api-key":
		request.Header.Set("X-Api-Key", client.authToken)
	case "bearer":
		request.Header.Set("Authorization", "Bearer "+client.authToken)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		if ctxErr := requestContext.Err(); ctxErr != nil {
			return nil, 0, ctxErr
		}
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, 0, UpstreamError{Code: ErrorUnavailable, Operation: operation, Retryable: true}
		}
		return nil, 0, UpstreamError{Code: ErrorUnavailable, Operation: operation, Retryable: true}
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, client.maxResponseBytes)
	if err != nil {
		if errors.Is(err, errResponseTooLarge) {
			return nil, response.StatusCode, UpstreamError{Code: ErrorResponseTooLarge, Operation: operation, Status: response.StatusCode}
		}
		return nil, response.StatusCode, UpstreamError{Code: ErrorUnknown, Operation: operation, Status: response.StatusCode}
	}
	return body, response.StatusCode, nil
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

func (client *Client) apiPath(resource string) string {
	base := strings.TrimRight(client.endpoint.Path, "/")
	if strings.HasSuffix(base, "/api/v1") {
		return base + resource
	}
	return base + "/api/v1" + resource
}

func configuredCredential(config Config) (string, string) {
	if config.APIKey != "" {
		return "api-key", config.APIKey
	}
	for _, token := range []string{config.BearerToken, config.Token, config.AuthToken} {
		if token != "" {
			return "bearer", token
		}
	}
	return "", ""
}

func parseEndpoint(value string) (*url.URL, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return nil, errors.New("Seerr endpoint is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Seerr endpoint must be an absolute URL without credentials or query")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("Seerr endpoint scheme is unsupported")
	}
	if !utf8.ValidString(parsed.Host) || strings.Contains(parsed.Path, "\\") || strings.Contains(parsed.Path, "//") || strings.Contains(parsed.Path, "%") || parsed.RawPath != "" {
		return nil, errors.New("Seerr endpoint path is ambiguous")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == "." || segment == ".." {
			return nil, errors.New("Seerr endpoint path is ambiguous")
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
	case status == http.StatusNotFound:
		code = ErrorUnsupported
	case status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented:
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

var errResponseTooLarge = errors.New("response exceeds configured bound")

func invalidInput(operation string) UpstreamError {
	return UpstreamError{Code: ErrorInvalidInput, Operation: operation}
}

func malformed(operation string) UpstreamError {
	return UpstreamError{Code: ErrorMalformed, Operation: operation}
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
	if err := scanJSON(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON response data")
	}
	return nil
}

// scanJSON rejects duplicate object members, invalid UTF-8 and excessively
// nested payloads before generated DTO unmarshalling. Unknown members remain
// allowed for forward-compatible Seerr releases.
func scanJSON(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 || !utf8.Valid(data) {
		return errors.New("JSON response is invalid")
	}
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
	if depth > maxJSONDepth {
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

func parseJSONInt(data []byte) (int64, error) {
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return 0, err
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func validBoundedText(value string, maxRunes int, allowEmpty bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return false
	}
	if !allowEmpty && value == "" {
		return false
	}
	for _, character := range value {
		if character == '\x00' || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func addReason(reasons *[]string, value string) {
	if value == "" || hasReason(*reasons, value) || len(*reasons) >= maxReasonCodes {
		return
	}
	*reasons = append(*reasons, value)
}

func hasReason(reasons []string, value string) bool {
	for _, reason := range reasons {
		if reason == value {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || hasReason(result, value) {
			continue
		}
		result = append(result, value)
	}
	return result
}

func maxInt(value, fallback int) int {
	if value > fallback {
		return value
	}
	return fallback
}
