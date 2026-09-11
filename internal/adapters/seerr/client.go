// Package seerr implements Seerr's read-only media and request catalog
// boundary. It preserves Seerr's native status values and pagination
// evidence; it never creates, approves, cancels or deletes a request.
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

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

const (
	defaultPageSize    = 100
	defaultMaxPages    = 100
	defaultMaxRecords  = 10_000
	defaultMaxResponse = 16 << 20
	maxCursorBytes     = 64 << 10
	maxCursorSeenIDs   = 2_048
	maxReasonCodes     = 256
	maxTextLength      = 4_096
	maxVersionLength   = 128
	maxNestedItems     = 512
	maxTags            = 256
)

const (
	apiStatus   = "/status"
	apiMedia    = "/media"
	apiRequests = "/request"
)

// Config contains one Seerr instance's endpoint, API key and observation
// bounds. Token and AuthToken are accepted as aliases for APIKey for callers
// that use generic connection credential names; APIKey wins.
type Config struct {
	ConnectionID domain.ConfigID
	Endpoint     string
	APIKey       string
	Token        string
	AuthToken    string
	HTTPClient   *http.Client

	MaxPageSize     int
	MaxPages        int
	MaxRecords      int
	MaxResponseSize int64
}

// Client is an authenticated, read-only Seerr HTTP client.
type Client struct {
	config    Config
	endpoint  *url.URL
	http      *http.Client
	cursorKey []byte
}

var _ ports.RequestCatalogReadPort = (*Client)(nil)
var _ ports.CapabilityPort = (*Client)(nil)

// VersionObservation is a sanitized read of Seerr's status endpoint.
type VersionObservation struct {
	ConnectionID domain.ConfigID
	ProductName  string
	Version      string
	CommitTag    string
	Commit       string
	ObservedAt   time.Time
}

// ProviderRelationship preserves every provider identity returned by Seerr.
// IDs are scoped by the containing Seerr connection.
type ProviderRelationship struct {
	Provider string
	ID       string
}

// ServiceRelationship preserves the Arr service relationship represented by
// one Seerr media record. Service availability is independent from media
// availability and request state.
type ServiceRelationship struct {
	Kind       string
	ServiceID  string
	ExternalID string
	Slug       string
	Is4K       bool
}

// AvailabilityObservation is Seerr's own media availability evidence. It is
// deliberately separate from request status and from Jellyfin playability.
type AvailabilityObservation struct {
	Known              bool
	Available          bool
	PartiallyAvailable bool
	NativeStatus       int
	NativeStatusName   string
	ObservedAt         time.Time
	Reason             string
}

// MediaStatus is Seerr's native media status enum. Unknown values remain
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
	ID                 string
	SeasonNumber       int
	NativeStatus       int
	NativeStatusName   string
	NativeStatus4K     int
	NativeStatus4KName string
	ObservedAt         time.Time
}

// RequestSummary preserves request references nested in a media record.
type RequestSummary struct {
	ID               string
	NativeStatus     int
	NativeStatusName string
	Type             string
	Is4K             bool
	CreatedAt        *time.Time
	UpdatedAt        *time.Time
}

// MediaObservation is the detailed Seerr media record. Record is the frozen
// common port value used by aggregators; all other fields retain connector
// evidence needed to explain status and relationships.
type MediaObservation struct {
	Record ports.RequestRecord

	ID                    string
	ScopedIdentity        string
	MediaType             string
	TMDBID                string
	TVDBID                string
	IMDBID                string
	ProviderIDs           map[string]string
	ProviderRelationships []ProviderRelationship

	NativeStatus         int
	NativeStatusName     string
	NativeStatus4K       int
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

// ServiceError preserves Seerr's serviceErrors relationship without exposing
// arbitrary upstream JSON.
type ServiceError struct {
	Kind string
	ID   string
	Name string
}

// RequestObservation is the detailed Seerr request record. Nested Media
// availability remains independent from this request's native status.
type RequestObservation struct {
	Record ports.RequestRecord

	ID                   string
	ScopedIdentity       string
	Type                 string
	NativeStatus         int
	NativeStatusName     string
	Media                MediaObservation
	SeasonCount          int
	Seasons              []SeasonObservation
	Is4K                 bool
	ServerID             string
	ProfileID            string
	RootFolder           string
	LanguageProfileID    string
	Tags                 []int64
	IsAutoRequest        bool
	IgnoreQuota          bool
	ServiceRelationships []ServiceRelationship
	SourceCreatedAt      *time.Time
	SourceUpdatedAt      *time.Time
	Evidence             []string
}

// PageInfo is the upstream pageInfo object. Complete is false when a
// response omitted or malformed one of its fields.
type PageInfo struct {
	Pages    int
	PageSize int
	Results  int
	Page     int
	Complete bool
}

// MediaPage is the detailed media page returned by ListMediaDetailed.
type MediaPage struct {
	Items      []MediaObservation
	NextCursor string
	Coverage   domain.Coverage
	PageInfo   PageInfo
}

// RequestPage is the detailed request page returned by ListRequestsDetailed.
type RequestPage struct {
	Items         []RequestObservation
	NextCursor    string
	Coverage      domain.Coverage
	PageInfo      PageInfo
	ServiceErrors []ServiceError
}

// New validates configuration without contacting Seerr.
func New(config Config) (*Client, error) {
	if !config.ConnectionID.Valid() {
		return nil, errors.New("Seerr connection id is invalid")
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
	if config.MaxPageSize > config.MaxRecords {
		config.MaxPageSize = config.MaxRecords
	}
	if config.MaxResponseSize <= 0 {
		config.MaxResponseSize = defaultMaxResponse
	}
	if config.MaxResponseSize >= math.MaxInt64 {
		return nil, errors.New("Seerr response bound is invalid")
	}

	baseClient := http.DefaultClient
	if config.HTTPClient != nil {
		baseClient = config.HTTPClient
	}
	copyClient := *baseClient
	if copyClient.Timeout == 0 {
		copyClient.Timeout = 30 * time.Second
	}
	copyClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	cursorKey := make([]byte, 32)
	if _, err := cryptorand.Read(cursorKey); err != nil {
		return nil, errors.New("Seerr cursor key setup failed")
	}
	return &Client{config: config, endpoint: endpoint, http: &copyClient, cursorKey: cursorKey}, nil
}

// NewClient is an explicit constructor alias.
func NewClient(config Config) (*Client, error) { return New(config) }

// ConnectionID identifies the upstream scope for every returned record.
func (client *Client) ConnectionID() domain.ConfigID { return client.config.ConnectionID }

// ScopedIdentity keeps Seerr numeric IDs distinct across configured
// instances. Empty IDs remain unknown rather than becoming a shared key.
func (client *Client) ScopedIdentity(externalID string) string {
	if strings.TrimSpace(externalID) == "" {
		return ""
	}
	return client.config.ConnectionID.String() + ":" + strings.TrimSpace(externalID)
}

// Version reads Seerr's status endpoint. A missing status route is returned
// as OutcomeUnsupported so callers can expose the version gate explicitly.
func (client *Client) Version(ctx context.Context, connectionID domain.ConfigID) (VersionObservation, error) {
	result := VersionObservation{ConnectionID: connectionID, ProductName: "seerr", ObservedAt: time.Now().UTC()}
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	body, err := client.get(ctx, "seerr.version", apiStatus, url.Values{"checkUpdateAvailable": []string{"false"}})
	if err != nil {
		return result, err
	}
	var status statusDTO
	if err := decodeJSON(body, &status); err != nil {
		return result, malformed("seerr.version")
	}
	result.Version = boundedText(status.Version, maxVersionLength)
	result.CommitTag = boundedText(status.CommitTag, maxVersionLength)
	result.Commit = boundedText(status.Commit, maxVersionLength)
	if result.Version == "" {
		return result, malformed("seerr.version")
	}
	return result, nil
}

// Capabilities reports read surfaces and the explicit v0.0.1 write boundary.
// The compatibility matrix pins only the inspected route source, not a
// Seerr release and disposable server. A successful status response therefore
// establishes version metadata only; it cannot promote media/request routes
// into supported capabilities.
func (client *Client) Capabilities(ctx context.Context, connectionID domain.ConfigID) ([]domain.Capability, error) {
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return nil, err
	}
	version, versionErr := client.Version(ctx, connectionID)
	if versionErr != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if code, ok := upstreamCode(versionErr); ok && code == domain.OutcomeUnauthorized {
			return nil, versionErr
		}
	}

	now := time.Now().UTC()
	versionState := domain.CapabilityUnknown
	versionReason := "version observation is unavailable"
	if version.Version != "" {
		versionState, versionReason = domain.CapabilitySupported, ""
	} else if code, ok := upstreamCode(versionErr); ok && code == domain.OutcomeUnsupported {
		versionState, versionReason = domain.CapabilityUnsupported, "Seerr status endpoint is unsupported"
	}
	readState := domain.CapabilityUnknown
	readReason := "Seerr media/request route compatibility is not pinned to a verified release"
	if versionErr != nil {
		switch {
		case versionState == domain.CapabilityUnsupported:
			readReason = "Seerr media/request compatibility is unknown because the status endpoint is unsupported"
		case versionState == domain.CapabilityUnknown:
			readReason = "Seerr media/request compatibility is unknown because the version observation is unavailable"
		}
	}
	return []domain.Capability{
		{Name: "seerr.version", State: versionState, Version: version.Version, Reason: versionReason, Evidence: []string{"GET /api/v1/status"}, ObservedAt: now},
		{Name: "seerr.media.read", State: readState, Version: version.Version, Reason: readReason, Evidence: []string{"GET /api/v1/media with take/skip/pageInfo", "compatibility_version_unpinned"}, ObservedAt: now},
		{Name: "seerr.requests.read", State: readState, Version: version.Version, Reason: readReason, Evidence: []string{"GET /api/v1/request with take/skip/pageInfo", "compatibility_version_unpinned"}, ObservedAt: now},
		{Name: "seerr.provider-relationships", State: readState, Version: version.Version, Reason: readReason, Evidence: []string{"typed tmdb/tvdb/imdb and service IDs", "compatibility_version_unpinned"}, ObservedAt: now},
		{Name: "seerr.writes", State: domain.CapabilityUnsupported, Version: version.Version, Reason: "Seerr connector is read-only in v0.0.1", Evidence: []string{"no mutation port or HTTP write method"}, ObservedAt: now},
	}, nil
}

// ListMedia implements ports.RequestCatalogReadPort with the common record
// view. Use ListMediaDetailed when status, provider and service evidence is
// needed by a reconciliation explanation.
func (client *Client) ListMedia(ctx context.Context, connectionID domain.ConfigID, cursor string, limit int) (ports.Page[ports.RequestRecord], error) {
	detailed, err := client.ListMediaDetailed(ctx, connectionID, cursor, limit)
	if err != nil {
		return ports.Page[ports.RequestRecord]{}, err
	}
	items := make([]ports.RequestRecord, 0, len(detailed.Items))
	for _, item := range detailed.Items {
		items = append(items, item.Record)
	}
	return ports.Page[ports.RequestRecord]{Items: items, NextCursor: detailed.NextCursor, Coverage: detailed.Coverage}, nil
}

// ListRequests implements ports.RequestCatalogReadPort with the common
// record view. Request pagination is independent from media pagination.
func (client *Client) ListRequests(ctx context.Context, connectionID domain.ConfigID, cursor string, limit int) (ports.Page[ports.RequestRecord], error) {
	detailed, err := client.ListRequestsDetailed(ctx, connectionID, cursor, limit)
	if err != nil {
		return ports.Page[ports.RequestRecord]{}, err
	}
	items := make([]ports.RequestRecord, 0, len(detailed.Items))
	for _, item := range detailed.Items {
		items = append(items, item.Record)
	}
	return ports.Page[ports.RequestRecord]{Items: items, NextCursor: detailed.NextCursor, Coverage: detailed.Coverage}, nil
}

// ListMediaDetailed reads one bounded page from Seerr's media catalog.
func (client *Client) ListMediaDetailed(ctx context.Context, connectionID domain.ConfigID, cursor string, requestedLimit int) (MediaPage, error) {
	var result MediaPage
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	state, err := client.startCursor("media", cursor, requestedLimit)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	query := url.Values{
		"take": []string{strconv.Itoa(state.Limit)},
		"skip": []string{strconv.Itoa(state.Skip)},
	}
	body, err := client.get(ctx, "seerr.media", apiMedia, query)
	if err != nil {
		return result, err
	}
	items, info, hasPageInfo, rawCount, pageDigest, err := decodeMediaPage(body)
	if err != nil {
		return result, malformed("seerr.media")
	}
	result.PageInfo = info
	if hasPageInfo {
		if err := state.observePageInfo(info); err != nil {
			addReason(&state.Reasons, "page_info_changed")
		}
		if state.ExpectedTotal < 0 {
			state.ExpectedTotal = info.Results
		}
		if state.ExpectedPages == 0 {
			state.ExpectedPages = info.Pages
		}
		if state.PageSize == 0 {
			state.PageSize = info.PageSize
		}
		if state.SnapshotRevision == "" {
			state.SnapshotRevision = pageDigest
		}
		if info.Page != state.expectedPage() {
			addReason(&state.Reasons, "page_number_changed")
		}
	} else {
		addReason(&state.Reasons, "page_info_missing")
	}
	if state.SnapshotRevision == "" {
		state.SnapshotRevision = pageDigest
	}
	markOffsetSnapshotUnverified(&state, info, hasPageInfo, rawCount)
	responseBoundHit := false
	if rawCount > state.Limit {
		rawCount = state.Limit
		items = items[:rawCount]
		responseBoundHit = true
		addReason(&state.Reasons, "pagination_response_exceeded_limit")
	} else if rawCount < len(items) {
		items = items[:rawCount]
	}
	recordBoundHit := false
	remainingRecords := client.config.MaxRecords - state.ObservedCount
	if remainingRecords < len(items) {
		if remainingRecords < 0 {
			remainingRecords = 0
		}
		items = items[:remainingRecords]
		recordBoundHit = true
		addReason(&state.Reasons, "records_limit")
	}

	observed := make([]MediaObservation, 0, len(items))
	for index, item := range items {
		mapped, reason := client.mapMedia(item)
		if reason != "" {
			addReason(&state.Reasons, fmt.Sprintf("media_%d_%s", index, reason))
			continue
		}
		key := "media:" + mapped.ID
		if state.hasSeen(key) {
			addReason(&state.Reasons, "pagination_overlap")
			continue
		}
		if len(state.SeenIDs) >= maxCursorSeenIDs {
			addReason(&state.Reasons, "identity_window_limit")
			break
		}
		state.SeenIDs = append(state.SeenIDs, key)
		observed = append(observed, mapped)
	}
	result.Items = observed
	state.Skip += rawCount
	state.PageCount++
	state.ObservedCount += len(observed)

	more, consistencyReason := pageHasMore(info, hasPageInfo, state.Skip, rawCount, state.Limit)
	if consistencyReason != "" {
		addReason(&state.Reasons, consistencyReason)
		more = false
	}
	if state.PageCount >= client.config.MaxPages && more {
		addReason(&state.Reasons, "pagination_limit")
		more = false
	}
	if responseBoundHit || recordBoundHit {
		more = false
	}
	if state.ObservedCount >= client.config.MaxRecords && more {
		addReason(&state.Reasons, "records_limit")
		more = false
	}
	emptyBeforeTotal := len(items) == 0 && hasPageInfo && state.ExpectedTotal > state.Skip
	if emptyBeforeTotal {
		addReason(&state.Reasons, "pagination_empty_before_total")
		more = false
	}

	result.Coverage = client.coverage(state, connectionID)
	if more {
		state.Reasons = append([]string(nil), state.Reasons...)
		result.NextCursor, err = client.encodeCursor(state)
		if err != nil {
			addReason(&state.Reasons, "pagination_cursor_limit")
			result.NextCursor = ""
			more = false
			result.Coverage = client.coverage(state, connectionID)
		}
	}
	if more && result.NextCursor != "" {
		addReason(&result.Coverage.ReasonCodes, "pagination_continues")
		result.Coverage.Completeness = domain.CompletenessPartial
	} else {
		completed := time.Now().UTC()
		result.Coverage.CompletedAt = &completed
		result.Coverage.ObservedAt = completed
		if len(result.Coverage.ReasonCodes) == 0 && hasPageInfo && info.Complete {
			result.Coverage.Completeness = domain.CompletenessComplete
		} else if result.Coverage.Completeness != domain.CompletenessUnknown {
			result.Coverage.Completeness = domain.CompletenessPartial
		}
	}
	return result, nil
}

// ListRequestsDetailed reads one bounded page from Seerr's request catalog.
func (client *Client) ListRequestsDetailed(ctx context.Context, connectionID domain.ConfigID, cursor string, requestedLimit int) (RequestPage, error) {
	var result RequestPage
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	state, err := client.startCursor("requests", cursor, requestedLimit)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	query := url.Values{
		"take": []string{strconv.Itoa(state.Limit)},
		"skip": []string{strconv.Itoa(state.Skip)},
	}
	body, err := client.get(ctx, "seerr.requests", apiRequests, query)
	if err != nil {
		return result, err
	}
	items, info, hasPageInfo, rawCount, pageDigest, services, err := decodeRequestPage(body)
	if err != nil {
		return result, malformed("seerr.requests")
	}
	result.PageInfo = info
	result.ServiceErrors = services
	if hasPageInfo {
		if err := state.observePageInfo(info); err != nil {
			addReason(&state.Reasons, "page_info_changed")
		}
		if state.ExpectedTotal < 0 {
			state.ExpectedTotal = info.Results
		}
		if state.ExpectedPages == 0 {
			state.ExpectedPages = info.Pages
		}
		if state.PageSize == 0 {
			state.PageSize = info.PageSize
		}
		if state.SnapshotRevision == "" {
			state.SnapshotRevision = pageDigest
		}
		if info.Page != state.expectedPage() {
			addReason(&state.Reasons, "page_number_changed")
		}
	} else {
		addReason(&state.Reasons, "page_info_missing")
	}
	if state.SnapshotRevision == "" {
		state.SnapshotRevision = pageDigest
	}
	markOffsetSnapshotUnverified(&state, info, hasPageInfo, rawCount)
	responseBoundHit := false
	if rawCount > state.Limit {
		rawCount = state.Limit
		items = items[:rawCount]
		responseBoundHit = true
		addReason(&state.Reasons, "pagination_response_exceeded_limit")
	} else if rawCount < len(items) {
		items = items[:rawCount]
	}
	recordBoundHit := false
	remainingRecords := client.config.MaxRecords - state.ObservedCount
	if remainingRecords < len(items) {
		if remainingRecords < 0 {
			remainingRecords = 0
		}
		items = items[:remainingRecords]
		recordBoundHit = true
		addReason(&state.Reasons, "records_limit")
	}

	observed := make([]RequestObservation, 0, len(items))
	for index, item := range items {
		mapped, reason := client.mapRequest(item)
		if reason != "" {
			addReason(&state.Reasons, fmt.Sprintf("request_%d_%s", index, reason))
			continue
		}
		key := "request:" + mapped.ID
		if state.hasSeen(key) {
			addReason(&state.Reasons, "pagination_overlap")
			continue
		}
		if len(state.SeenIDs) >= maxCursorSeenIDs {
			addReason(&state.Reasons, "identity_window_limit")
			break
		}
		state.SeenIDs = append(state.SeenIDs, key)
		observed = append(observed, mapped)
	}
	result.Items = observed
	state.Skip += rawCount
	state.PageCount++
	state.ObservedCount += len(observed)

	more, consistencyReason := pageHasMore(info, hasPageInfo, state.Skip, rawCount, state.Limit)
	if consistencyReason != "" {
		addReason(&state.Reasons, consistencyReason)
		more = false
	}
	if state.PageCount >= client.config.MaxPages && more {
		addReason(&state.Reasons, "pagination_limit")
		more = false
	}
	if responseBoundHit || recordBoundHit {
		more = false
	}
	if state.ObservedCount >= client.config.MaxRecords && more {
		addReason(&state.Reasons, "records_limit")
		more = false
	}
	emptyBeforeTotal := len(items) == 0 && hasPageInfo && state.ExpectedTotal > state.Skip
	if emptyBeforeTotal {
		addReason(&state.Reasons, "pagination_empty_before_total")
		more = false
	}

	result.Coverage = client.coverage(state, connectionID)
	if more {
		result.NextCursor, err = client.encodeCursor(state)
		if err != nil {
			addReason(&state.Reasons, "pagination_cursor_limit")
			result.NextCursor = ""
			more = false
			result.Coverage = client.coverage(state, connectionID)
		}
	}
	if more && result.NextCursor != "" {
		addReason(&result.Coverage.ReasonCodes, "pagination_continues")
		result.Coverage.Completeness = domain.CompletenessPartial
	} else {
		completed := time.Now().UTC()
		result.Coverage.CompletedAt = &completed
		result.Coverage.ObservedAt = completed
		if len(result.Coverage.ReasonCodes) == 0 && hasPageInfo && info.Complete {
			result.Coverage.Completeness = domain.CompletenessComplete
		} else if result.Coverage.Completeness != domain.CompletenessUnknown {
			result.Coverage.Completeness = domain.CompletenessPartial
		}
	}
	return result, nil
}

type cursorState struct {
	SourceID         domain.RuntimeID `json:"sourceId"`
	Collection       string           `json:"collection"`
	Limit            int              `json:"limit"`
	Skip             int              `json:"skip"`
	PageCount        int              `json:"pageCount"`
	ObservedCount    int              `json:"observedCount"`
	ExpectedTotal    int              `json:"expectedTotal"`
	ExpectedPages    int              `json:"expectedPages"`
	PageSize         int              `json:"pageSize"`
	SnapshotRevision string           `json:"snapshotRevision"`
	StartedAt        time.Time        `json:"startedAt"`
	SeenIDs          []string         `json:"seenIds,omitempty"`
	Reasons          []string         `json:"reasons,omitempty"`
}

func (client *Client) startCursor(collection, value string, requestedLimit int) (cursorState, error) {
	limit, err := client.pageLimit(requestedLimit)
	if err != nil {
		return cursorState{}, err
	}
	if value == "" {
		sourceID, idErr := domain.NewRuntimeID()
		if idErr != nil {
			return cursorState{}, errors.New("Seerr catalog source identity unavailable")
		}
		return cursorState{SourceID: sourceID, Collection: collection, Limit: limit, ExpectedTotal: -1, StartedAt: time.Now().UTC()}, nil
	}
	state, err := client.decodeCursor(value)
	if err != nil {
		return cursorState{}, err
	}
	if state.Collection != collection || state.Limit != limit && requestedLimit > 0 {
		return cursorState{}, invalidInput("seerr.catalog.cursor")
	}
	if state.PageCount >= client.config.MaxPages || state.ObservedCount >= client.config.MaxRecords {
		return cursorState{}, invalidInput("seerr.catalog.cursor")
	}
	if state.Limit <= 0 {
		return cursorState{}, invalidInput("seerr.catalog.cursor")
	}
	return state, nil
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
	if err := decodeJSON(payload, &state); err != nil || !state.SourceID.Valid() || state.Collection == "" || state.Limit <= 0 || state.Skip < 0 || state.PageCount < 0 || state.ObservedCount < 0 || state.ExpectedTotal < -1 || state.ExpectedPages < 0 || state.PageSize < 0 || len(state.SeenIDs) > maxCursorSeenIDs || len(state.Reasons) > maxReasonCodes || state.StartedAt.IsZero() {
		return cursorState{}, invalidInput("seerr.catalog.cursor")
	}
	return state, nil
}

func (state *cursorState) observePageInfo(info PageInfo) error {
	if !info.Complete || info.Pages < 0 || info.PageSize <= 0 || info.Results < 0 || info.Page <= 0 || info.Pages == 0 && info.Results != 0 {
		return errors.New("incomplete page info")
	}
	if state.ExpectedTotal >= 0 && state.ExpectedTotal != info.Results {
		return errors.New("page total changed")
	}
	if state.ExpectedPages > 0 && state.ExpectedPages != info.Pages {
		return errors.New("page count changed")
	}
	if state.PageSize > 0 && state.PageSize != info.PageSize {
		return errors.New("page size changed")
	}
	return nil
}

func (state cursorState) expectedPage() int {
	if state.PageSize <= 0 {
		return state.PageCount + 1
	}
	return state.Skip/state.PageSize + 1
}

func (state cursorState) hasSeen(value string) bool {
	for _, existing := range state.SeenIDs {
		if existing == value {
			return true
		}
	}
	return false
}

func (client *Client) coverage(state cursorState, connectionID domain.ConfigID) domain.Coverage {
	coverage := domain.Coverage{
		SourceID: state.SourceID, ConnectionID: connectionID,
		Completeness: domain.CompletenessComplete, ObservedCount: int64(state.ObservedCount),
		SnapshotRevision: state.SnapshotRevision, StartedAt: timePtr(state.StartedAt), ObservedAt: time.Now().UTC(),
	}
	for _, reason := range state.Reasons {
		addReason(&coverage.ReasonCodes, reason)
	}
	if len(coverage.ReasonCodes) > 0 {
		coverage.Completeness = domain.CompletenessPartial
	}
	return coverage
}

func pageHasMore(info PageInfo, hasPageInfo bool, nextSkip, rawCount, limit int) (bool, string) {
	if hasPageInfo {
		byTotal := nextSkip < info.Results
		byPage := info.Page < info.Pages
		if byTotal != byPage {
			return false, "page_info_inconsistent"
		}
		return byTotal, ""
	}
	return rawCount >= limit && rawCount > 0, ""
}

// markOffsetSnapshotUnverified records the boundary that an offset traversal
// cannot establish from Seerr's response alone. pageInfo validates totals and
// page shape, but it is not an immutable collection identity: a deletion,
// insertion or reorder can replace records between two otherwise consistent
// requests. Keep the complete result visible while forcing partial coverage
// until a versioned upstream snapshot token is available.
func markOffsetSnapshotUnverified(state *cursorState, info PageInfo, hasPageInfo bool, rawCount int) {
	if hasPageInfo {
		if info.Pages > 1 || info.Results > info.PageSize {
			addReason(&state.Reasons, "pagination_snapshot_unverified")
		}
		return
	}
	if rawCount >= state.Limit && rawCount > 0 {
		addReason(&state.Reasons, "pagination_snapshot_unverified")
	}
}

type statusDTO struct {
	Version   string `json:"version"`
	CommitTag string `json:"commitTag"`
	Commit    string `json:"commit"`
}

type mediaDTO struct {
	ID                    json.RawMessage     `json:"id"`
	MediaType             string              `json:"mediaType"`
	TMDBID                json.RawMessage     `json:"tmdbId"`
	TVDBID                json.RawMessage     `json:"tvdbId"`
	IMDBID                string              `json:"imdbId"`
	Status                json.RawMessage     `json:"status"`
	Status4K              json.RawMessage     `json:"status4k"`
	Requests              []requestSummaryDTO `json:"requests"`
	Seasons               []seasonDTO         `json:"seasons"`
	ServiceID             json.RawMessage     `json:"serviceId"`
	ServiceID4K           json.RawMessage     `json:"serviceId4k"`
	ExternalServiceID     json.RawMessage     `json:"externalServiceId"`
	ExternalServiceID4K   json.RawMessage     `json:"externalServiceId4k"`
	ExternalServiceSlug   string              `json:"externalServiceSlug"`
	ExternalServiceSlug4K string              `json:"externalServiceSlug4k"`
	JellyfinMediaID       json.RawMessage     `json:"jellyfinMediaId"`
	JellyfinMediaID4K     json.RawMessage     `json:"jellyfinMediaId4k"`
	RatingKey             string              `json:"ratingKey"`
	RatingKey4K           string              `json:"ratingKey4k"`
	MediaAddedAt          json.RawMessage     `json:"mediaAddedAt"`
	CreatedAt             string              `json:"createdAt"`
	UpdatedAt             string              `json:"updatedAt"`
}

type requestDTO struct {
	ID                json.RawMessage   `json:"id"`
	Status            json.RawMessage   `json:"status"`
	Media             mediaDTO          `json:"media"`
	Type              string            `json:"type"`
	SeasonCount       int               `json:"seasonCount"`
	Seasons           []seasonDTO       `json:"seasons"`
	Is4K              bool              `json:"is4k"`
	ServerID          json.RawMessage   `json:"serverId"`
	ProfileID         json.RawMessage   `json:"profileId"`
	RootFolder        string            `json:"rootFolder"`
	LanguageProfileID json.RawMessage   `json:"languageProfileId"`
	Tags              []json.RawMessage `json:"tags"`
	IsAutoRequest     bool              `json:"isAutoRequest"`
	IgnoreQuota       bool              `json:"ignoreQuota"`
	CreatedAt         string            `json:"createdAt"`
	UpdatedAt         string            `json:"updatedAt"`
}

type requestSummaryDTO struct {
	ID        json.RawMessage `json:"id"`
	Status    json.RawMessage `json:"status"`
	Type      string          `json:"type"`
	Is4K      bool            `json:"is4k"`
	CreatedAt string          `json:"createdAt"`
	UpdatedAt string          `json:"updatedAt"`
}

type seasonDTO struct {
	ID           json.RawMessage `json:"id"`
	SeasonNumber int             `json:"seasonNumber"`
	Status       json.RawMessage `json:"status"`
	Status4K     json.RawMessage `json:"status4k"`
}

type serviceErrorDTO struct {
	ID   json.RawMessage `json:"id"`
	Name string          `json:"name"`
}

type pageInfoDTO struct {
	Pages    json.RawMessage `json:"pages"`
	PageSize json.RawMessage `json:"pageSize"`
	Results  json.RawMessage `json:"results"`
	Page     json.RawMessage `json:"page"`
}

type responseEnvelope[T any] struct {
	PageInfo json.RawMessage `json:"pageInfo"`
	Results  []T             `json:"results"`
}

type requestEnvelope struct {
	PageInfo      json.RawMessage              `json:"pageInfo"`
	Results       []requestDTO                 `json:"results"`
	ServiceErrors map[string][]serviceErrorDTO `json:"serviceErrors"`
}

func decodeMediaPage(body []byte) ([]mediaDTO, PageInfo, bool, int, string, error) {
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return nil, PageInfo{}, false, 0, "", errors.New("null media response")
	}
	if isJSONArray(body) {
		var items []mediaDTO
		if err := decodeJSON(body, &items); err != nil {
			return nil, PageInfo{}, false, 0, "", err
		}
		return items, PageInfo{}, false, len(items), pageDigest("media", PageInfo{}, items), nil
	}
	var envelope responseEnvelope[mediaDTO]
	if err := decodeJSON(body, &envelope); err != nil {
		return nil, PageInfo{}, false, 0, "", err
	}
	info, present, err := decodePageInfo(envelope.PageInfo, len(envelope.Results))
	if err != nil {
		return nil, PageInfo{}, false, 0, "", err
	}
	return envelope.Results, info, present, len(envelope.Results), pageDigest("media", info, envelope.Results), nil
}

func decodeRequestPage(body []byte) ([]requestDTO, PageInfo, bool, int, string, []ServiceError, error) {
	if bytes.Equal(bytes.TrimSpace(body), []byte("null")) {
		return nil, PageInfo{}, false, 0, "", nil, errors.New("null request response")
	}
	if isJSONArray(body) {
		var items []requestDTO
		if err := decodeJSON(body, &items); err != nil {
			return nil, PageInfo{}, false, 0, "", nil, err
		}
		return items, PageInfo{}, false, len(items), pageDigest("requests", PageInfo{}, items), nil, nil
	}
	var envelope requestEnvelope
	if err := decodeJSON(body, &envelope); err != nil {
		return nil, PageInfo{}, false, 0, "", nil, err
	}
	info, present, err := decodePageInfo(envelope.PageInfo, len(envelope.Results))
	if err != nil {
		return nil, PageInfo{}, false, 0, "", nil, err
	}
	services := make([]ServiceError, 0)
	kinds := make([]string, 0, len(envelope.ServiceErrors))
	for kind := range envelope.ServiceErrors {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		values := envelope.ServiceErrors[kind]
		if len(services) >= maxNestedItems {
			break
		}
		for _, value := range values {
			if len(services) >= maxNestedItems {
				break
			}
			id, _ := rawString(value.ID)
			services = append(services, ServiceError{Kind: boundedText(kind, maxVersionLength), ID: boundedText(id, maxTextLength), Name: boundedText(value.Name, maxTextLength)})
		}
	}
	return envelope.Results, info, present, len(envelope.Results), pageDigest("requests", info, envelope.Results), services, nil
}

func decodePageInfo(raw json.RawMessage, fallbackResults int) (PageInfo, bool, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return PageInfo{}, false, nil
	}
	var dto pageInfoDTO
	if err := decodeJSON(raw, &dto); err != nil {
		return PageInfo{}, true, err
	}
	pages, pagesOK, err := optionalNonNegative(dto.Pages)
	if err != nil {
		return PageInfo{}, true, err
	}
	pageSize, pageSizeOK, err := optionalNonNegative(dto.PageSize)
	if err != nil {
		return PageInfo{}, true, err
	}
	results, resultsOK, err := optionalNonNegative(dto.Results)
	if err != nil {
		return PageInfo{}, true, err
	}
	page, pageOK, err := optionalNonNegative(dto.Page)
	if err != nil {
		return PageInfo{}, true, err
	}
	if !resultsOK {
		results = fallbackResults
	}
	complete := pagesOK && pageSizeOK && resultsOK && pageOK && pageSize > 0 && page >= 1 && (pages > 0 || results == 0)
	return PageInfo{Pages: pages, PageSize: pageSize, Results: results, Page: page, Complete: complete}, true, nil
}

func (client *Client) mapMedia(dto mediaDTO) (MediaObservation, string) {
	now := time.Now().UTC()
	id, ok := positiveID(dto.ID)
	if !ok || strings.TrimSpace(id) == "" {
		return MediaObservation{}, "identity_unknown"
	}
	id = boundedText(id, maxTextLength)
	status, statusOK := rawInt(dto.Status)
	statusName := MediaStatus(status).String()
	if !statusOK {
		status = 0
		statusName = "unknown"
	}
	status4K, status4KOK := rawInt(dto.Status4K)
	status4KName := MediaStatus(status4K).String()
	if !status4KOK {
		status4K = 0
		status4KName = "unknown"
	}
	providers := make(map[string]string, 3)
	if value, valid := rawString(dto.TMDBID); valid && value != "" {
		providers["tmdb"] = boundedText(value, maxTextLength)
	}
	if value, valid := rawString(dto.TVDBID); valid && value != "" {
		providers["tvdb"] = boundedText(value, maxTextLength)
	}
	if value := boundedText(dto.IMDBID, maxTextLength); value != "" {
		providers["imdb"] = value
	}
	providerRelationships := providerValues(providers)
	providerID := firstProviderID(dto.MediaType, providers)
	media := MediaObservation{
		ID: id, MediaType: boundedText(dto.MediaType, maxVersionLength),
		TMDBID: providers["tmdb"], TVDBID: providers["tvdb"], IMDBID: providers["imdb"],
		ProviderIDs: providers, ProviderRelationships: providerRelationships,
		NativeStatus: status, NativeStatusName: statusName,
		NativeStatus4K: status4K, NativeStatus4KName: status4KName,
		JellyfinMediaID: rawStringOrEmpty(dto.JellyfinMediaID), JellyfinMediaID4K: rawStringOrEmpty(dto.JellyfinMediaID4K),
		RatingKey: boundedText(dto.RatingKey, maxTextLength), RatingKey4K: boundedText(dto.RatingKey4K, maxTextLength),
		Evidence: nil,
	}
	media.ScopedIdentity = client.ScopedIdentity(id)
	media.Record = ports.RequestRecord{ExternalID: id, ProviderID: providerID, Status: statusName, MediaID: id, ObservedAt: now}
	media.Availability = availability(status, statusOK, now)
	media.SourceCreatedAt, media.SourceUpdatedAt = sourceTimes(dto.CreatedAt, dto.UpdatedAt, &media.Evidence)
	if raw, present := rawTimestamp(dto.MediaAddedAt); present {
		if raw != nil {
			media.MediaAddedAt = raw
		} else {
			if bytes.Equal(bytes.TrimSpace(dto.MediaAddedAt), []byte("null")) {
				addReason(&media.Evidence, "media_added_at_null")
			} else {
				addReason(&media.Evidence, "media_added_at_malformed")
			}
		}
	}
	if !statusOK {
		addReason(&media.Evidence, "native_status_unknown")
	}
	if !status4KOK && len(bytes.TrimSpace(dto.Status4K)) > 0 {
		addReason(&media.Evidence, "native_4k_status_unknown")
	}
	media.ServiceRelationships = serviceRelationships(serviceKind(dto.MediaType), dto.ServiceID, dto.ExternalServiceID, dto.ExternalServiceSlug, false, dto.ServiceID4K, dto.ExternalServiceID4K, dto.ExternalServiceSlug4K)
	if len(dto.Requests) > maxNestedItems {
		addReason(&media.Evidence, "nested_requests_limit")
		dto.Requests = dto.Requests[:maxNestedItems]
	}
	for _, request := range dto.Requests {
		summary, reason := mapRequestSummary(request, now)
		if reason != "" {
			addReason(&media.Evidence, "nested_request_"+reason)
			continue
		}
		media.Requests = append(media.Requests, summary)
	}
	if len(dto.Seasons) > maxNestedItems {
		addReason(&media.Evidence, "nested_seasons_limit")
		dto.Seasons = dto.Seasons[:maxNestedItems]
	}
	for _, season := range dto.Seasons {
		mapped, reason := mapSeason(season, now)
		if reason != "" {
			addReason(&media.Evidence, "nested_season_"+reason)
			continue
		}
		media.Seasons = append(media.Seasons, mapped)
	}
	return media, ""
}

func (client *Client) mapRequest(dto requestDTO) (RequestObservation, string) {
	now := time.Now().UTC()
	id, ok := positiveID(dto.ID)
	if !ok || strings.TrimSpace(id) == "" {
		return RequestObservation{}, "identity_unknown"
	}
	status, statusOK := rawInt(dto.Status)
	statusName := RequestStatus(status).String()
	if !statusOK {
		status = 0
		statusName = "unknown"
	}
	media, mediaReason := client.mapMedia(dto.Media)
	if mediaReason != "" {
		media.Evidence = appendReason(media.Evidence, "nested_media_"+mediaReason)
	}
	request := RequestObservation{
		ID: boundedText(id, maxTextLength), Type: boundedText(dto.Type, maxVersionLength),
		NativeStatus: status, NativeStatusName: statusName, Media: media,
		SeasonCount: maxInt(dto.SeasonCount, 0), Is4K: dto.Is4K,
		ServerID: rawStringOrEmpty(dto.ServerID), ProfileID: rawStringOrEmpty(dto.ProfileID),
		RootFolder: boundedText(dto.RootFolder, maxTextLength), LanguageProfileID: rawStringOrEmpty(dto.LanguageProfileID),
		IsAutoRequest: dto.IsAutoRequest, IgnoreQuota: dto.IgnoreQuota,
		ServiceRelationships: media.ServiceRelationships, Evidence: nil,
	}
	request.ScopedIdentity = client.ScopedIdentity(id)
	request.Record = ports.RequestRecord{ExternalID: id, ProviderID: media.Record.ProviderID, Status: statusName, MediaID: media.Record.MediaID, ObservedAt: now}
	request.SourceCreatedAt, request.SourceUpdatedAt = sourceTimes(dto.CreatedAt, dto.UpdatedAt, &request.Evidence)
	if !statusOK {
		addReason(&request.Evidence, "native_status_unknown")
	}
	if len(dto.Tags) > maxTags {
		addReason(&request.Evidence, "tags_limit")
		dto.Tags = dto.Tags[:maxTags]
	}
	for _, rawTag := range dto.Tags {
		value, valid := rawInt64(rawTag)
		if !valid {
			addReason(&request.Evidence, "tag_unknown")
			continue
		}
		request.Tags = append(request.Tags, value)
	}
	if len(dto.Seasons) > maxNestedItems {
		addReason(&request.Evidence, "nested_seasons_limit")
		dto.Seasons = dto.Seasons[:maxNestedItems]
	}
	for _, season := range dto.Seasons {
		mapped, reason := mapSeason(season, now)
		if reason != "" {
			addReason(&request.Evidence, "nested_season_"+reason)
			continue
		}
		request.Seasons = append(request.Seasons, mapped)
	}
	return request, ""
}

func mapRequestSummary(dto requestSummaryDTO, now time.Time) (RequestSummary, string) {
	id, ok := positiveID(dto.ID)
	if !ok || strings.TrimSpace(id) == "" {
		return RequestSummary{}, "identity_unknown"
	}
	status, statusOK := rawInt(dto.Status)
	if !statusOK {
		status = 0
	}
	created, updated, _ := parsedTimes(dto.CreatedAt, dto.UpdatedAt)
	if created == nil && strings.TrimSpace(dto.CreatedAt) != "" || updated == nil && strings.TrimSpace(dto.UpdatedAt) != "" {
		return RequestSummary{ID: boundedText(id, maxTextLength), NativeStatus: status, NativeStatusName: RequestStatus(status).String(), Type: boundedText(dto.Type, maxVersionLength), Is4K: dto.Is4K, CreatedAt: created, UpdatedAt: updated}, "timestamp_unknown"
	}
	return RequestSummary{ID: boundedText(id, maxTextLength), NativeStatus: status, NativeStatusName: RequestStatus(status).String(), Type: boundedText(dto.Type, maxVersionLength), Is4K: dto.Is4K, CreatedAt: created, UpdatedAt: updated}, ""
}

func mapSeason(dto seasonDTO, now time.Time) (SeasonObservation, string) {
	id, ok := positiveID(dto.ID)
	if !ok || strings.TrimSpace(id) == "" {
		return SeasonObservation{}, "identity_unknown"
	}
	status, statusOK := rawInt(dto.Status)
	status4K, status4KOK := rawInt(dto.Status4K)
	if !statusOK {
		status = 0
	}
	if !status4KOK {
		status4K = 0
	}
	return SeasonObservation{ID: boundedText(id, maxTextLength), SeasonNumber: maxInt(dto.SeasonNumber, 0), NativeStatus: status, NativeStatusName: MediaStatus(status).String(), NativeStatus4K: status4K, NativeStatus4KName: MediaStatus(status4K).String(), ObservedAt: now}, ""
}

func availability(status int, known bool, observedAt time.Time) AvailabilityObservation {
	name := MediaStatus(status).String()
	result := AvailabilityObservation{Known: known, NativeStatus: status, NativeStatusName: name, ObservedAt: observedAt}
	if !known {
		result.Reason = "native_media_status_unknown"
		return result
	}
	switch MediaStatus(status) {
	case MediaStatusAvailable:
		result.Available = true
	case MediaStatusPartiallyAvailable:
		result.PartiallyAvailable = true
	case MediaStatusDeleted:
		result.Reason = "native_media_status_deleted"
	default:
		result.Reason = "native_media_status_not_available"
	}
	return result
}

func serviceRelationships(kind string, serviceID, externalID json.RawMessage, slug string, is4K bool, serviceID4K, externalID4K json.RawMessage, slug4K string) []ServiceRelationship {
	result := make([]ServiceRelationship, 0, 2)
	if id := rawStringOrEmpty(serviceID); id != "" || rawStringOrEmpty(externalID) != "" {
		result = append(result, ServiceRelationship{Kind: boundedText(kind, maxVersionLength), ServiceID: boundedText(id, maxTextLength), ExternalID: boundedText(rawStringOrEmpty(externalID), maxTextLength), Slug: boundedText(slug, maxTextLength), Is4K: is4K})
	}
	if id := rawStringOrEmpty(serviceID4K); id != "" || rawStringOrEmpty(externalID4K) != "" {
		result = append(result, ServiceRelationship{Kind: boundedText(kind, maxVersionLength), ServiceID: boundedText(id, maxTextLength), ExternalID: boundedText(rawStringOrEmpty(externalID4K), maxTextLength), Slug: boundedText(slug4K, maxTextLength), Is4K: true})
	}
	return result
}

func serviceKind(mediaType string) string {
	if strings.EqualFold(strings.TrimSpace(mediaType), "movie") {
		return "radarr"
	}
	if strings.EqualFold(strings.TrimSpace(mediaType), "tv") || strings.EqualFold(strings.TrimSpace(mediaType), "series") {
		return "sonarr"
	}
	return "unknown"
}

func providerValues(values map[string]string) []ProviderRelationship {
	result := make([]ProviderRelationship, 0, len(values))
	for _, provider := range []string{"tmdb", "tvdb", "imdb"} {
		if value := strings.TrimSpace(values[provider]); value != "" {
			result = append(result, ProviderRelationship{Provider: provider, ID: value})
		}
	}
	return result
}

func firstProviderID(mediaType string, values map[string]string) string {
	providers := []string{"tmdb", "tvdb", "imdb"}
	if strings.EqualFold(strings.TrimSpace(mediaType), "tv") || strings.EqualFold(strings.TrimSpace(mediaType), "series") {
		providers = []string{"tvdb", "tmdb", "imdb"}
	}
	for _, provider := range providers {
		if value := strings.TrimSpace(values[provider]); value != "" {
			return value
		}
	}
	return ""
}

func sourceTimes(created, updated string, reasons *[]string) (*time.Time, *time.Time) {
	createdAt, updatedAt, invalid := parsedTimes(created, updated)
	if invalid {
		addReason(reasons, "source_timestamp_malformed")
	}
	return createdAt, updatedAt
}

func parsedTimes(created, updated string) (*time.Time, *time.Time, bool) {
	createdAt, createdOK := parseTimestamp(created)
	updatedAt, updatedOK := parseTimestamp(updated)
	return createdAt, updatedAt, strings.TrimSpace(created) != "" && !createdOK || strings.TrimSpace(updated) != "" && !updatedOK
}

func parseTimestamp(value string) (*time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, true
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z07:00", "2006-01-02 15:04:05"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed, true
		}
	}
	return nil, false
}

func rawTimestamp(raw json.RawMessage) (*time.Time, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return nil, true
	}
	var value string
	if err := decodeJSON(trimmed, &value); err != nil {
		return nil, true
	}
	parsed, _ := parseTimestamp(value)
	return parsed, true
}

func rawString(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false
	}
	if trimmed[0] == '"' {
		var value string
		if err := decodeJSON(trimmed, &value); err != nil {
			return "", false
		}
		return strings.TrimSpace(value), true
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil {
		return "", false
	}
	return strings.TrimSpace(number.String()), true
}

func rawStringOrEmpty(raw json.RawMessage) string {
	value, _ := rawString(raw)
	return value
}

func positiveID(raw json.RawMessage) (string, bool) {
	value, ok := rawString(raw)
	if !ok || value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return "", false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return "", false
	}
	return strconv.FormatInt(parsed, 10), true
}

func rawInt(raw json.RawMessage) (int, bool) {
	value, ok := rawString(raw)
	if !ok || value == "" {
		return 0, false
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

func rawInt64(raw json.RawMessage) (int64, bool) {
	value, ok := rawString(raw)
	if !ok || value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, false
	}
	return parsed, true
}

func optionalNonNegative(raw json.RawMessage) (int, bool, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, false, nil
	}
	value, ok := rawInt(raw)
	if !ok {
		return 0, true, errors.New("page info integer is invalid")
	}
	return value, true, nil
}

func pageDigest(collection string, info PageInfo, values any) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, collection)
	_, _ = io.WriteString(hash, "|")
	_, _ = io.WriteString(hash, strconv.Itoa(info.Pages))
	_, _ = io.WriteString(hash, "|")
	_, _ = io.WriteString(hash, strconv.Itoa(info.PageSize))
	_, _ = io.WriteString(hash, "|")
	_, _ = io.WriteString(hash, strconv.Itoa(info.Results))
	_, _ = io.WriteString(hash, "|")
	_, _ = io.WriteString(hash, strconv.Itoa(info.Page))
	encoded, _ := json.Marshal(values)
	_, _ = hash.Write(encoded)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func (client *Client) pageLimit(requested int) (int, error) {
	if requested <= 0 {
		return client.config.MaxPageSize, nil
	}
	if requested > client.config.MaxPageSize {
		return 0, invalidInput("seerr.catalog.limit")
	}
	return requested, nil
}

func (client *Client) get(ctx context.Context, operation, resource string, query url.Values) ([]byte, error) {
	body, status, err := client.request(ctx, operation, resource, query)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, normalizeStatus(operation, status)
	}
	return body, nil
}

func (client *Client) request(ctx context.Context, operation, resource string, query url.Values) ([]byte, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	requestURL := *client.endpoint
	requestURL.Path = client.apiPath(resource)
	requestURL.RawPath = ""
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, 0, errors.New("Seerr request could not be created")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "mastarr-seerr-read/0.0.1")
	if key := client.authToken(); key != "" {
		request.Header.Set("X-Api-Key", key)
	}
	response, err := client.http.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, 0, ctxErr
		}
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, 0, domain.UpstreamError{Code: domain.OutcomeUnavailable, Retryable: true, Operation: operation, Detail: "upstream request timed out"}
		}
		return nil, 0, domain.UpstreamError{Code: domain.OutcomeUnavailable, Retryable: true, Operation: operation, Detail: "upstream is unavailable"}
	}
	defer response.Body.Close()
	body, readErr := readBounded(response.Body, client.config.MaxResponseSize)
	if readErr != nil {
		return nil, response.StatusCode, domain.UpstreamError{Code: domain.OutcomeUnknown, Status: response.StatusCode, Operation: operation, Detail: "upstream response exceeded the configured bound"}
	}
	return body, response.StatusCode, nil
}

func (client *Client) apiPath(resource string) string {
	base := strings.TrimRight(client.endpoint.Path, "/")
	if strings.HasSuffix(base, "/api/v1") {
		return base + resource
	}
	return base + "/api/v1" + resource
}

func (client *Client) authToken() string {
	if strings.TrimSpace(client.config.APIKey) != "" {
		return strings.TrimSpace(client.config.APIKey)
	}
	if strings.TrimSpace(client.config.Token) != "" {
		return strings.TrimSpace(client.config.Token)
	}
	return strings.TrimSpace(client.config.AuthToken)
}

func parseEndpoint(value string) (*url.URL, error) {
	if strings.TrimSpace(value) != value || value == "" {
		return nil, errors.New("Seerr endpoint is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Seerr endpoint must be an absolute URL without credentials or query")
	}
	return parsed, nil
}

func validateConnectionScope(expected, requested domain.ConfigID) error {
	if !requested.Valid() || requested != expected {
		return invalidInput("seerr.connection")
	}
	return nil
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes >= math.MaxInt64 {
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
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("response contains trailing JSON")
	}
	return nil
}

func isJSONArray(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && trimmed[0] == '['
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

func malformed(operation string) error {
	return domain.UpstreamError{Code: domain.OutcomeUnknown, Operation: operation, Detail: "upstream response is malformed"}
}

func invalidInput(operation string) error {
	return domain.UpstreamError{Code: domain.OutcomeInvalidInput, Operation: operation, Detail: "request is invalid"}
}

func upstreamCode(err error) (domain.UpstreamErrorCode, bool) {
	var upstream domain.UpstreamError
	if !errors.As(err, &upstream) {
		return "", false
	}
	return upstream.Code, true
}

func maxInt(value, minimum int) int {
	if value < minimum {
		return minimum
	}
	return value
}

func boundedText(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return value[:max]
	}
	return value
}

func addReason(reasons *[]string, reason string) {
	if strings.TrimSpace(reason) == "" {
		return
	}
	for _, existing := range *reasons {
		if existing == reason {
			return
		}
	}
	if len(*reasons) < maxReasonCodes {
		*reasons = append(*reasons, reason)
	}
}

func appendReason(reasons []string, reason string) []string {
	addReason(&reasons, reason)
	return reasons
}

func timePtr(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}
