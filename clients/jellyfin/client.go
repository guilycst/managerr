// Package jellyfin implements the small, independent Jellyfin HTTP
// compatibility boundary used by Mastarr. The package owns transport,
// authentication, upstream DTO decoding and sanitized errors; it has no
// dependency on the Mastarr root module.
package jellyfin

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
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	generated "github.com/guilycst/mastarr/clients/jellyfin/internal/generated"
)

const (
	DefaultMaxResponseBytes = 16 << 20
	DefaultMaxPageSize      = 100
	DefaultMaxPages         = 100
	DefaultMaxItems         = 10_000
	DefaultRequestTimeout   = 15 * time.Second

	maxTokenChars       = 512
	maxUserAgentChars   = 128
	maxUserIDChars      = 256
	maxItemIDChars      = 256
	maxLibraryIDChars   = 256
	maxSystemTextChars  = 256
	maxTextChars        = 4096
	maxProviderIDChars  = 256
	maxProviderIDs      = 64
	maxMediaSources     = 128
	maxQueryChars       = 4096
	maxCollectionFields = 1024
	maxJSONDepth        = 64
)

const (
	apiSystemInfo     = "/System/Info/Public"
	apiMediaFolders   = "/Library/MediaFolders"
	apiUserViews      = "/UserViews"
	apiItems          = "/Items"
	apiLibraryRefresh = "/Library/Refresh"
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

// UpstreamError intentionally carries no URL, token, response body or
// filesystem detail. Callers can use Code and Retryable without parsing text.
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
		return fmt.Sprintf("Jellyfin %s failed (%s, status %d)", operation, err.Code, err.Status)
	}
	return fmt.Sprintf("Jellyfin %s failed (%s)", operation, err.Code)
}

// IsCode reports whether err or a wrapped error carries code.
func IsCode(err error, code ErrorCode) bool {
	var upstream UpstreamError
	return errors.As(err, &upstream) && upstream.Code == code
}

// Config controls one Jellyfin connection. Endpoint may include a reverse
// proxy path prefix. Token, APIKey and AuthToken are accepted aliases, with
// Token taking precedence, then APIKey, then AuthToken.
type Config struct {
	Endpoint   string
	Token      string
	APIKey     string
	AuthToken  string
	UserID     string
	HTTPClient *http.Client

	RequestTimeout   time.Duration
	MaxResponseBytes int64
	MaxPageSize      int
	MaxPages         int
	MaxItems         int
	UserAgent        string
}

// Client is an independent Jellyfin client. Read methods never mutate
// Jellyfin. Refresh is an explicit operation and is never called by reads.
type Client struct {
	endpoint         *url.URL
	httpClient       *http.Client
	token            string
	userID           string
	requestTimeout   time.Duration
	maxResponseBytes int64
	maxPageSize      int
	maxPages         int
	maxItems         int
	userAgent        string
}

// New validates configuration without contacting Jellyfin.
func New(config Config) (*Client, error) {
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	token := configuredToken(config)
	if err := validateBoundedText(token, maxTokenChars, true); err != nil || strings.TrimSpace(token) != token {
		return nil, errors.New("Jellyfin token is invalid")
	}
	if err := validateUserID(config.UserID); err != nil {
		return nil, err
	}
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	if maxResponseBytes < 1 || maxResponseBytes >= math.MaxInt64 {
		return nil, errors.New("Jellyfin response bound is invalid")
	}
	maxPageSize := config.MaxPageSize
	if maxPageSize <= 0 {
		maxPageSize = DefaultMaxPageSize
	}
	maxPages := config.MaxPages
	if maxPages <= 0 {
		maxPages = DefaultMaxPages
	}
	maxItems := config.MaxItems
	if maxItems <= 0 {
		maxItems = DefaultMaxItems
	}
	if maxPageSize > maxItems || maxPageSize > DefaultMaxItems || maxPages > DefaultMaxPages || maxItems > DefaultMaxItems {
		return nil, errors.New("Jellyfin item bound exceeds compatibility ceiling")
	}
	requestTimeout := config.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = DefaultRequestTimeout
	}
	if requestTimeout < 0 {
		return nil, errors.New("Jellyfin request timeout cannot be negative")
	}
	userAgent := config.UserAgent
	if userAgent == "" {
		userAgent = "mastarr-jellyfin-client/0.0.1"
	}
	if err := validateBoundedText(userAgent, maxUserAgentChars, true); err != nil || strings.TrimSpace(userAgent) != userAgent {
		return nil, errors.New("Jellyfin user agent is invalid")
	}

	baseClient := http.DefaultClient
	if config.HTTPClient != nil {
		baseClient = config.HTTPClient
	}
	clientCopy := *baseClient
	clientCopy.Jar = nil
	// Never replay a token-bearing request at a redirect target. The caller
	// receives the 3xx response and a sanitized error instead.
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &Client{
		endpoint: endpoint, httpClient: &clientCopy, token: token,
		userID: config.UserID, requestTimeout: requestTimeout,
		maxResponseBytes: maxResponseBytes, maxPageSize: maxPageSize,
		maxPages: maxPages, maxItems: maxItems, userAgent: userAgent,
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

// String returns a safe diagnostic representation; the token is omitted.
func (client *Client) String() string { return "JellyfinClient{" + client.Endpoint() + "}" }

// SystemInfo is the sanitized public system-info observation.
type SystemInfo struct {
	ProductName            string
	ServerName             string
	Version                string
	OperatingSystem        string
	ServerID               string
	StartupWizardCompleted *bool
	ObservedAt             time.Time
}

// Version is an alias for the server version observation.
func (client *Client) Version(ctx context.Context) (string, error) {
	info, err := client.GetSystemInfo(ctx)
	return info.Version, err
}

// Status is an alias for GetSystemInfo.
func (client *Client) Status(ctx context.Context) (SystemInfo, error) {
	return client.GetSystemInfo(ctx)
}

// GetSystemInfo reads Jellyfin's public system-info endpoint.
func (client *Client) GetSystemInfo(ctx context.Context) (SystemInfo, error) {
	body, err := client.get(ctx, "jellyfin.system.info", apiSystemInfo, nil)
	if err != nil {
		return SystemInfo{}, err
	}
	if err := requireObjectFields(body, "Version"); err != nil {
		return SystemInfo{}, malformed("jellyfin.system.info")
	}
	var value generated.SystemInfo
	if err := decodeJSON(body, &value); err != nil || validateBoundedText(value.Version, 128, true) != nil {
		return SystemInfo{}, malformed("jellyfin.system.info")
	}
	productName, err := optionalText(value.ProductName, maxSystemTextChars)
	if err != nil {
		return SystemInfo{}, malformed("jellyfin.system.info")
	}
	serverName, err := optionalText(value.ServerName, maxSystemTextChars)
	if err != nil {
		return SystemInfo{}, malformed("jellyfin.system.info")
	}
	operatingSystem, err := optionalText(value.OperatingSystem, maxSystemTextChars)
	if err != nil {
		return SystemInfo{}, malformed("jellyfin.system.info")
	}
	serverID, err := optionalText(value.Id, maxItemIDChars)
	if err != nil {
		return SystemInfo{}, malformed("jellyfin.system.info")
	}
	return SystemInfo{
		ProductName:            productName,
		ServerName:             serverName,
		Version:                value.Version,
		OperatingSystem:        operatingSystem,
		ServerID:               serverID,
		StartupWizardCompleted: cloneBool(value.StartupWizardCompleted),
		ObservedAt:             time.Now().UTC(),
	}, nil
}

// Coverage describes the completeness of one native response page. Jellyfin
// has no immutable snapshot token, so a multi-request traversal is explicitly
// partial even when its totals appear consistent.
type Coverage struct {
	Completeness  string
	ObservedCount int
	ObservedAt    time.Time
	ReasonCodes   []string
}

const (
	CompletenessComplete = "complete"
	CompletenessPartial  = "partial"
	CompletenessUnknown  = "unknown"
)

// Page is a bounded observation page. NextCursor is reserved for a later
// adapter-owned cursor contract; native Jellyfin offsets remain explicit.
type Page[T any] struct {
	Items      []T
	NextCursor string
	Coverage   Coverage
}

// Library identifies one Jellyfin collection folder or user view.
type Library struct {
	ID             string
	Name           string
	CollectionType string
	Type           string
}

// Libraries is an alias for a page of library observations.
func (client *Client) Libraries(ctx context.Context) (Page[Library], error) {
	return client.ListLibraries(ctx)
}

// ListLibraries reads media folders and falls back to Jellyfin's canonical
// UserViews route only when the primary route is explicitly unavailable. The
// fallback is still read-only and uses the configured user query when present.
func (client *Client) ListLibraries(ctx context.Context) (Page[Library], error) {
	body, err := client.get(ctx, "jellyfin.libraries", apiMediaFolders, nil)
	if err != nil && (IsCode(err, ErrorNotFound) || IsCode(err, ErrorUnsupported)) {
		var query url.Values
		if client.userID != "" {
			query = url.Values{"userId": []string{client.userID}}
		}
		body, err = client.get(ctx, "jellyfin.views", apiUserViews, query)
	}
	if err != nil {
		return Page[Library]{}, err
	}
	items, err := decodeLibraries(body, client.maxItems)
	if err != nil {
		return Page[Library]{}, malformed("jellyfin.libraries")
	}
	return Page[Library]{
		Items:    items,
		Coverage: completeCoverage(len(items)),
	}, nil
}

// ItemQuery is the supported subset of Jellyfin's offset-based Items query.
// ParentID and ItemIDs are mutually exclusive to avoid ambiguous scopes.
type ItemQuery struct {
	ParentID            string
	ItemIDs             []string
	IncludeItemTypes    []string
	Recursive           bool
	StartIndex          int
	Limit               int
	UserID              string
	Fields              []string
	CollapseBoxSetItems *bool
}

// Item is a normalized library-item observation. Provider IDs and media
// sources are evidence only; this type deliberately does not claim that a
// path is mounted, playable or eventually visible after a refresh.
type Item struct {
	ID                string
	Name              string
	Type              string
	Title             string
	Path              string
	LocationType      string
	MediaType         string
	ProviderIDs       map[string]string
	ProviderRelations []ProviderRelationship
	MediaSources      []MediaSource
	ObservedAt        time.Time
}

// ProviderRelationship preserves each provider namespace instead of reducing
// a title to one guessed provider.
type ProviderRelationship struct {
	Provider string
	ID       string
}

// MediaSource is native source evidence. A source path is not a local
// filesystem verification and never by itself establishes availability.
type MediaSource struct {
	ID           string
	Path         string
	Protocol     string
	LocationType string
	MediaType    string
}

// ListItems reads one bounded native Items page. A page whose envelope start
// and total prove all requested records were returned is complete; an offset
// page with more records remains partial. Missing pagination metadata is
// unknown for a potentially paginated scope.
func (client *Client) ListItems(ctx context.Context, query ItemQuery) (Page[Item], error) {
	params, err := client.itemQuery(query)
	if err != nil {
		return Page[Item]{}, err
	}
	body, err := client.get(ctx, "jellyfin.items", apiItems, params)
	if err != nil {
		return Page[Item]{}, err
	}
	decoded, err := decodeItems(body, client.maxItems)
	if err != nil {
		return Page[Item]{}, malformed("jellyfin.items")
	}
	items, total, start, arrayShape := decoded.items, decoded.total, decoded.start, decoded.arrayShape
	if query.StartIndex < 0 {
		return Page[Item]{}, invalidInput("jellyfin.items.start_index")
	}
	if decoded.startPresent && start != query.StartIndex {
		return Page[Item]{}, malformed("jellyfin.items")
	}
	if query.Limit > 0 && len(items) > query.Limit {
		return Page[Item]{}, malformed("jellyfin.items.limit")
	}
	if err := validateRequestedItems(items, query.ItemIDs); err != nil {
		return Page[Item]{}, malformed("jellyfin.items.scope")
	}
	coverage := Coverage{ObservedCount: len(items), ObservedAt: time.Now().UTC()}
	if arrayShape {
		// An exact single-ID request is a bounded native lookup. Its array
		// response is authoritative even when Limit=1, so an empty response can
		// establish absence. A general offset/limited array has no total or
		// start metadata and must retain unknown coverage.
		if len(query.ItemIDs) == 1 && query.StartIndex == 0 {
			coverage.Completeness = CompletenessComplete
		} else if query.Limit > 0 || query.StartIndex > 0 {
			coverage.Completeness = CompletenessUnknown
			coverage.ReasonCodes = []string{"pagination_total_missing"}
		} else {
			coverage.Completeness = CompletenessComplete
		}
		return Page[Item]{Items: items, Coverage: coverage}, nil
	}
	if !decoded.totalPresent {
		coverage.Completeness = CompletenessUnknown
		coverage.ReasonCodes = append(coverage.ReasonCodes, "pagination_total_missing")
		if !arrayShape && !decoded.startPresent {
			coverage.ReasonCodes = append(coverage.ReasonCodes, "pagination_start_missing")
		}
		return Page[Item]{Items: items, Coverage: coverage}, nil
	}
	if !arrayShape && !decoded.startPresent {
		coverage.Completeness = CompletenessUnknown
		coverage.ReasonCodes = []string{"pagination_start_missing"}
		return Page[Item]{Items: items, Coverage: coverage}, nil
	}
	end := int64(start) + int64(len(items))
	if int64(start) > total || end > total {
		return Page[Item]{}, malformed("jellyfin.items.pagination")
	}
	switch {
	case end < total:
		coverage.Completeness = CompletenessPartial
		coverage.ReasonCodes = []string{"pagination_continues"}
	case len(items) == 0 && int64(start) < total:
		coverage.Completeness = CompletenessUnknown
		coverage.ReasonCodes = []string{"pagination_gap"}
	default:
		coverage.Completeness = CompletenessComplete
	}
	return Page[Item]{Items: items, Coverage: coverage}, nil
}

// Items is an alias for ListItems.
func (client *Client) Items(ctx context.Context, query ItemQuery) (Page[Item], error) {
	return client.ListItems(ctx, query)
}

// ListAllItems traverses offset pages up to configured bounds. Since Jellyfin
// does not expose an immutable collection revision, a traversal requiring more
// than one request is returned as partial with an explicit reason.
func (client *Client) ListAllItems(ctx context.Context, query ItemQuery) (Page[Item], error) {
	if query.StartIndex != 0 {
		return Page[Item]{}, invalidInput("jellyfin.items.start_index")
	}
	limit := query.Limit
	if limit == 0 {
		limit = client.maxPageSize
	}
	if limit < 1 || limit > client.maxPageSize {
		return Page[Item]{}, invalidInput("jellyfin.items.limit")
	}
	var result []Item
	pageCount := 0
	partialReasons := make([]string, 0, 2)
	for {
		if pageCount >= client.maxPages || len(result) >= client.maxItems {
			return Page[Item]{Items: result, Coverage: Coverage{
				Completeness: CompletenessPartial, ObservedCount: len(result),
				ObservedAt: time.Now().UTC(), ReasonCodes: []string{"pagination_limit"},
			}}, nil
		}
		pageQuery := query
		pageQuery.StartIndex = len(result)
		pageQuery.Limit = limit
		page, err := client.ListItems(ctx, pageQuery)
		if err != nil {
			return Page[Item]{Items: result, Coverage: Coverage{
				Completeness: CompletenessUnknown, ObservedCount: len(result),
				ObservedAt: time.Now().UTC(), ReasonCodes: []string{"pagination_read_failed"},
			}}, err
		}
		pageCount++
		for _, reason := range page.Coverage.ReasonCodes {
			if !containsString(partialReasons, reason) {
				partialReasons = append(partialReasons, reason)
			}
		}
		for _, item := range page.Items {
			if containsItemID(result, item.ID) {
				return Page[Item]{}, malformed("jellyfin.items.duplicate")
			}
			result = append(result, item)
			if len(result) > client.maxItems {
				return Page[Item]{}, tooLarge("jellyfin.items")
			}
		}
		if page.Coverage.Completeness == CompletenessUnknown {
			return Page[Item]{Items: result, Coverage: Coverage{
				Completeness: CompletenessUnknown, ObservedCount: len(result),
				ObservedAt: time.Now().UTC(), ReasonCodes: partialReasons,
			}}, nil
		}
		if page.Coverage.Completeness == CompletenessComplete {
			break
		}
		if len(page.Items) == 0 {
			return Page[Item]{Items: result, Coverage: Coverage{
				Completeness: CompletenessUnknown, ObservedCount: len(result),
				ObservedAt: time.Now().UTC(), ReasonCodes: append(partialReasons, "pagination_gap"),
			}}, nil
		}
	}
	coverage := Coverage{Completeness: CompletenessComplete, ObservedCount: len(result), ObservedAt: time.Now().UTC(), ReasonCodes: partialReasons}
	if pageCount > 1 {
		coverage.Completeness = CompletenessPartial
		if !containsString(coverage.ReasonCodes, "pagination_snapshot_unverified") {
			coverage.ReasonCodes = append(coverage.ReasonCodes, "pagination_snapshot_unverified")
		}
	}
	return Page[Item]{Items: result, Coverage: coverage}, nil
}

// ObserveItem reads exactly one item by native ID and verifies the returned
// identity before returning evidence.
func (client *Client) ObserveItem(ctx context.Context, itemID string) (Item, error) {
	if err := validatePathID(itemID, maxItemIDChars); err != nil {
		return Item{}, invalidInput("jellyfin.item.id")
	}
	page, err := client.ListItems(ctx, ItemQuery{ItemIDs: []string{itemID}, Limit: 1})
	if err != nil {
		return Item{}, err
	}
	if len(page.Items) == 0 && page.Coverage.Completeness == CompletenessComplete {
		return Item{}, UpstreamError{Code: ErrorNotFound, Operation: "jellyfin.item", Status: http.StatusNotFound}
	}
	if len(page.Items) != 1 || page.Items[0].ID != itemID {
		return Item{}, UpstreamError{Code: ErrorUnknown, Operation: "jellyfin.item"}
	}
	return page.Items[0], nil
}

// GetItem is a naming alias for ObserveItem.
func (client *Client) GetItem(ctx context.Context, itemID string) (Item, error) {
	return client.ObserveItem(ctx, itemID)
}

// RefreshScope identifies the explicit native refresh scope.
type RefreshScope string

const (
	RefreshLibraryScope RefreshScope = "library"
	RefreshItemScope    RefreshScope = "item"
)

// RefreshRequest is validated before any native request is dispatched.
type RefreshRequest struct {
	Scope  RefreshScope
	ItemID string
}

// RefreshResult records only native request acceptance. It contains no
// library/item availability field by design; callers must perform a later read
// and retain that observation separately.
type RefreshResult struct {
	Scope      RefreshScope
	ItemID     string
	Accepted   bool
	Status     int
	ObservedAt time.Time
	Evidence   []string
}

// Refresh performs one explicitly requested native refresh operation.
func (client *Client) Refresh(ctx context.Context, request RefreshRequest) (RefreshResult, error) {
	result := RefreshResult{Scope: request.Scope, ItemID: request.ItemID, ObservedAt: time.Now().UTC()}
	var endpoint string
	switch request.Scope {
	case RefreshLibraryScope:
		if request.ItemID != "" {
			return result, invalidInput("jellyfin.refresh.library")
		}
		endpoint = apiLibraryRefresh
	case RefreshItemScope:
		if err := validatePathID(request.ItemID, maxItemIDChars); err != nil {
			return result, invalidInput("jellyfin.refresh.item")
		}
		endpoint = "/Items/" + request.ItemID + "/Refresh"
	default:
		return result, invalidInput("jellyfin.refresh.scope")
	}
	status, err := client.postEmpty(ctx, "jellyfin.refresh."+string(request.Scope), endpoint)
	if err != nil {
		return result, err
	}
	result.Accepted = true
	result.Status = status
	result.Evidence = []string{"refresh_request_accepted", "availability_requires_later_read"}
	return result, nil
}

// RefreshLibrary requests an explicit library refresh.
func (client *Client) RefreshLibrary(ctx context.Context) (RefreshResult, error) {
	return client.Refresh(ctx, RefreshRequest{Scope: RefreshLibraryScope})
}

// RefreshItem requests an explicit item refresh.
func (client *Client) RefreshItem(ctx context.Context, itemID string) (RefreshResult, error) {
	return client.Refresh(ctx, RefreshRequest{Scope: RefreshItemScope, ItemID: itemID})
}

func (client *Client) itemQuery(query ItemQuery) (url.Values, error) {
	if query.ParentID != "" && len(query.ItemIDs) > 0 {
		return nil, invalidInput("jellyfin.items.scope")
	}
	if query.StartIndex < 0 || query.StartIndex > client.maxItems {
		return nil, invalidInput("jellyfin.items.start_index")
	}
	if query.Limit < 0 || query.Limit > client.maxPageSize {
		return nil, invalidInput("jellyfin.items.limit")
	}
	if query.ParentID != "" {
		if err := validateQueryID(query.ParentID, maxItemIDChars); err != nil {
			return nil, invalidInput("jellyfin.items.parent_id")
		}
	}
	values := url.Values{}
	if query.ParentID != "" {
		values.Set("ParentId", query.ParentID)
	}
	if len(query.ItemIDs) > 0 {
		if len(query.ItemIDs) > client.maxItems {
			return nil, tooLarge("jellyfin.items.ids")
		}
		seen := make(map[string]struct{}, len(query.ItemIDs))
		ids := make([]string, 0, len(query.ItemIDs))
		for _, value := range query.ItemIDs {
			if err := validateQueryID(value, maxItemIDChars); err != nil {
				return nil, invalidInput("jellyfin.items.ids")
			}
			if _, exists := seen[value]; exists {
				return nil, invalidInput("jellyfin.items.ids")
			}
			seen[value] = struct{}{}
			ids = append(ids, value)
		}
		values.Set("Ids", strings.Join(ids, ","))
	}
	includeTypes := query.IncludeItemTypes
	if len(includeTypes) == 0 && query.ParentID != "" {
		includeTypes = []string{"Series", "Movie", "Episode"}
	}
	if len(includeTypes) > 0 {
		joined, err := joinBoundedValues(includeTypes, maxQueryChars)
		if err != nil {
			return nil, invalidInput("jellyfin.items.types")
		}
		values.Set("IncludeItemTypes", joined)
	}
	values.Set("Recursive", strconv.FormatBool(query.Recursive))
	if query.StartIndex > 0 {
		values.Set("StartIndex", strconv.Itoa(query.StartIndex))
	}
	if query.Limit > 0 {
		values.Set("Limit", strconv.Itoa(query.Limit))
	}
	userID := query.UserID
	if userID == "" {
		userID = client.userID
	}
	if userID != "" {
		if err := validateUserID(userID); err != nil {
			return nil, invalidInput("jellyfin.items.user_id")
		}
		values.Set("UserId", userID)
	}
	fields := query.Fields
	if len(fields) == 0 {
		fields = []string{"ProviderIds", "MediaSources", "Path", "LocationType", "MediaType", "Type"}
	}
	if len(fields) > 0 {
		joined, err := joinBoundedValues(fields, maxCollectionFields)
		if err != nil {
			return nil, invalidInput("jellyfin.items.fields")
		}
		values.Set("Fields", joined)
	}
	if query.CollapseBoxSetItems != nil {
		values.Set("collapseBoxSetItems", strconv.FormatBool(*query.CollapseBoxSetItems))
	}
	return values, nil
}

func (client *Client) get(ctx context.Context, operation, endpoint string, query url.Values) ([]byte, error) {
	body, status, err := client.request(ctx, operation, http.MethodGet, endpoint, query)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, normalizeStatus(operation, status)
	}
	return body, nil
}

func (client *Client) postEmpty(ctx context.Context, operation, endpoint string) (int, error) {
	body, status, err := client.request(ctx, operation, http.MethodPost, endpoint, nil)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK && status != http.StatusAccepted && status != http.StatusNoContent {
		return status, normalizeStatus(operation, status)
	}
	// Native refresh responses are status-only in the compatibility contract.
	// Bound and discard any compatibility server body so it cannot become a
	// hidden result or an unbounded memory read.
	_ = body
	return status, nil
}

func (client *Client) request(ctx context.Context, operation, method, endpoint string, query url.Values) ([]byte, int, error) {
	requestContext, cancel := context.WithTimeout(ctx, client.requestTimeout)
	defer cancel()
	if err := requestContext.Err(); err != nil {
		return nil, 0, err
	}
	requestURL := *client.endpoint
	requestURL.Path = strings.TrimRight(client.endpoint.Path, "/") + endpoint
	requestURL.RawPath = ""
	if query != nil {
		requestURL.RawQuery = query.Encode()
	} else {
		requestURL.RawQuery = ""
	}
	request, err := http.NewRequestWithContext(requestContext, method, requestURL.String(), nil)
	if err != nil {
		return nil, 0, invalidInput(operation)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", client.userAgent)
	if client.token != "" {
		request.Header.Set("X-Emby-Token", client.token)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		if contextErr := requestContext.Err(); contextErr != nil {
			return nil, 0, contextErr
		}
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, 0, UpstreamError{Code: ErrorUnavailable, Operation: operation, Retryable: true}
		}
		return nil, 0, UpstreamError{Code: ErrorUnavailable, Operation: operation, Retryable: true}
	}
	defer response.Body.Close()
	body, readErr := readBounded(response.Body, client.maxResponseBytes)
	if readErr != nil {
		if errors.Is(readErr, errResponseTooLarge) {
			return nil, response.StatusCode, UpstreamError{Code: ErrorResponseTooLarge, Operation: operation, Status: response.StatusCode}
		}
		if contextErr := requestContext.Err(); contextErr != nil {
			return nil, response.StatusCode, contextErr
		}
		return nil, response.StatusCode, UpstreamError{Code: ErrorMalformed, Operation: operation, Status: response.StatusCode}
	}
	return body, response.StatusCode, nil
}

func decodeLibraries(body []byte, maxItems int) ([]Library, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("empty library response")
	}
	var values []generated.Library
	if trimmed[0] == '[' {
		if err := requireArrayObjectFields(body, "Id"); err != nil || decodeJSON(body, &values) != nil {
			return nil, errors.New("invalid library array")
		}
	} else {
		if err := requireObjectFields(body, "Items"); err != nil {
			return nil, err
		}
		var envelope generated.LibraryEnvelope
		if err := decodeJSON(body, &envelope); err != nil {
			return nil, err
		}
		values = envelope.Items
	}
	if len(values) > maxItems {
		return nil, errTooLarge
	}
	result := make([]Library, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if validatePathID(value.Id, maxLibraryIDChars) != nil {
			return nil, fmt.Errorf("library %d has invalid id", index)
		}
		nameValue, err := optionalText(value.Name, maxTextChars)
		if err != nil {
			return nil, fmt.Errorf("library %d has invalid name", index)
		}
		titleValue, err := optionalText(value.Title, maxTextChars)
		if err != nil {
			return nil, fmt.Errorf("library %d has invalid title", index)
		}
		name := firstNonEmpty(nameValue, titleValue)
		if validateBoundedText(name, maxTextChars, true) != nil {
			return nil, fmt.Errorf("library %d has invalid name", index)
		}
		if _, exists := seen[value.Id]; exists {
			return nil, errors.New("duplicate library id")
		}
		seen[value.Id] = struct{}{}
		collectionType, err := optionalText(value.CollectionType, 128)
		if err != nil {
			return nil, fmt.Errorf("library %d has invalid collection type", index)
		}
		libraryType, err := optionalText(value.Type, 128)
		if err != nil {
			return nil, fmt.Errorf("library %d has invalid type", index)
		}
		result[index] = Library{ID: value.Id, Name: name, CollectionType: collectionType, Type: libraryType}
	}
	return result, nil
}

type decodedItems struct {
	items        []Item
	total        int64
	start        int
	arrayShape   bool
	startPresent bool
	totalPresent bool
}

func decodeItems(body []byte, maxItems int) (decodedItems, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return decodedItems{}, errors.New("empty item response")
	}
	var values []generated.Item
	total := int64(-1)
	start := 0
	arrayShape := trimmed[0] == '['
	startPresent := arrayShape
	totalPresent := arrayShape
	if arrayShape {
		if err := requireArrayObjectFields(body, "Id"); err != nil || decodeJSON(body, &values) != nil {
			return decodedItems{}, errors.New("invalid item array")
		}
		total = int64(len(values))
	} else {
		if err := requireObjectFields(body, "Items"); err != nil {
			return decodedItems{}, err
		}
		var envelope generated.ItemPage
		if err := decodeJSON(body, &envelope); err != nil {
			return decodedItems{}, err
		}
		values = envelope.Items
		if envelope.TotalRecordCount != nil {
			if *envelope.TotalRecordCount < 0 {
				return decodedItems{}, errors.New("negative total")
			}
			total = *envelope.TotalRecordCount
			totalPresent = true
		}
		if envelope.StartIndex != nil {
			if *envelope.StartIndex < 0 || *envelope.StartIndex > math.MaxInt32 {
				return decodedItems{}, errors.New("invalid start")
			}
			start = int(*envelope.StartIndex)
			startPresent = true
		}
	}
	if len(values) > maxItems {
		return decodedItems{}, errTooLarge
	}
	result := make([]Item, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		item, err := normalizeItem(value)
		if err != nil {
			return decodedItems{}, fmt.Errorf("item %d: %w", index, err)
		}
		if _, exists := seen[item.ID]; exists {
			return decodedItems{}, errors.New("duplicate item id")
		}
		seen[item.ID] = struct{}{}
		result[index] = item
	}
	return decodedItems{
		items: result, total: total, start: start, arrayShape: arrayShape,
		startPresent: startPresent, totalPresent: totalPresent,
	}, nil
}

func normalizeItem(value generated.Item) (Item, error) {
	if validatePathID(value.Id, maxItemIDChars) != nil {
		return Item{}, errors.New("invalid item id")
	}
	name, err := optionalText(value.Name, maxTextChars)
	if err != nil {
		return Item{}, errors.New("invalid item name")
	}
	title, err := optionalText(value.Title, maxTextChars)
	if err != nil {
		return Item{}, errors.New("invalid item title")
	}
	typeName, err := optionalText(value.Type, 128)
	if err != nil {
		return Item{}, errors.New("invalid item type")
	}
	itemPath, err := optionalText(value.Path, maxTextChars)
	if err != nil {
		return Item{}, errors.New("invalid item path")
	}
	locationType, err := optionalText(value.LocationType, 128)
	if err != nil {
		return Item{}, errors.New("invalid item location")
	}
	mediaType, err := optionalText(value.MediaType, 128)
	if err != nil {
		return Item{}, errors.New("invalid item media type")
	}
	result := Item{
		ID:           value.Id,
		Name:         name,
		Title:        title,
		Type:         typeName,
		Path:         itemPath,
		LocationType: locationType,
		MediaType:    mediaType,
		ObservedAt:   time.Now().UTC(),
	}
	providers, relations, err := normalizeProviders(value.ProviderIds)
	if err != nil {
		return Item{}, err
	}
	result.ProviderIDs = providers
	result.ProviderRelations = relations
	if value.MediaSources != nil {
		if len(*value.MediaSources) > maxMediaSources {
			return Item{}, errors.New("media source limit exceeded")
		}
		result.MediaSources = make([]MediaSource, len(*value.MediaSources))
		seenSources := make(map[string]struct{}, len(*value.MediaSources))
		for index, source := range *value.MediaSources {
			id, err := optionalText(source.Id, maxItemIDChars)
			if err != nil {
				return Item{}, fmt.Errorf("media source %d has invalid id", index)
			}
			if id == "" {
				return Item{}, fmt.Errorf("media source %d has no id", index)
			}
			if _, exists := seenSources[id]; exists {
				return Item{}, errors.New("duplicate media source id")
			}
			seenSources[id] = struct{}{}
			pathValue, err := optionalText(source.Path, maxTextChars)
			if err != nil {
				return Item{}, fmt.Errorf("media source %d has invalid path", index)
			}
			protocol, err := optionalText(source.Protocol, 128)
			if err != nil {
				return Item{}, fmt.Errorf("media source %d has invalid protocol", index)
			}
			location, err := optionalText(source.LocationType, 128)
			if err != nil {
				return Item{}, fmt.Errorf("media source %d has invalid location", index)
			}
			media, err := optionalText(source.MediaType, 128)
			if err != nil {
				return Item{}, fmt.Errorf("media source %d has invalid media type", index)
			}
			result.MediaSources[index] = MediaSource{
				ID:           id,
				Path:         pathValue,
				Protocol:     protocol,
				LocationType: location,
				MediaType:    media,
			}
		}
	}
	return result, nil
}

func normalizeProviders(values *map[string]string) (map[string]string, []ProviderRelationship, error) {
	if values == nil {
		return nil, nil, nil
	}
	if len(*values) > maxProviderIDs {
		return nil, nil, errors.New("provider id limit exceeded")
	}
	keys := make([]string, 0, len(*values))
	result := make(map[string]string, len(*values))
	for key, value := range *values {
		if validateBoundedText(key, maxProviderIDChars, true) != nil || validateBoundedText(value, maxProviderIDChars, true) != nil {
			return nil, nil, errors.New("provider id is invalid")
		}
		result[key] = value
		keys = append(keys, key)
	}
	sort.Strings(keys)
	relations := make([]ProviderRelationship, 0, len(keys))
	for _, key := range keys {
		relations = append(relations, ProviderRelationship{Provider: key, ID: result[key]})
	}
	return result, relations, nil
}

func requireObjectFields(body []byte, fields ...string) error {
	var value map[string]json.RawMessage
	if err := decodeJSON(body, &value); err != nil {
		return err
	}
	for _, field := range fields {
		raw, ok := value[field]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("required field %s is missing", field)
		}
	}
	return nil
}

func requireArrayObjectFields(body []byte, fields ...string) error {
	var values []map[string]json.RawMessage
	if err := decodeJSON(body, &values); err != nil {
		return err
	}
	for index, value := range values {
		for _, field := range fields {
			raw, ok := value[field]
			if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return fmt.Errorf("array item %d required field %s is missing", index, field)
			}
		}
	}
	return nil
}

var (
	errTooLarge = errors.New("response exceeds configured bound")
)

var errResponseTooLarge = errTooLarge

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, errors.New("response could not be read")
	}
	if int64(len(data)) > maxBytes {
		return nil, errResponseTooLarge
	}
	return data, nil
}

// decodeJSON rejects duplicate keys and trailing JSON while deliberately
// allowing forward-compatible unknown fields. Unknown upstream members stay
// in the generated boundary and are not copied into normalized types.
func decodeJSON(data []byte, target any) error {
	if err := validateJSON(data); err != nil {
		return err
	}
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

func validateJSON(data []byte) error {
	if !utf8.Valid(data) {
		return errors.New("response is not valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := walkJSON(decoder, 0); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("response contains trailing JSON")
	}
	return nil
}

func walkJSON(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}
	if depth >= maxJSONDepth {
		return errors.New("JSON nesting exceeds configured bound")
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("object key is invalid")
			}
			if _, exists := seen[name]; exists {
				return errors.New("duplicate object key")
			}
			seen[name] = struct{}{}
			if err := walkJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("object is not closed")
		}
	case '[':
		for decoder.More() {
			if err := walkJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("array is not closed")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

func normalizeStatus(operation string, status int) error {
	code := ErrorUnknown
	retryable := false
	switch {
	case status == http.StatusUnauthorized:
		code = ErrorUnauthorized
	case status == http.StatusForbidden:
		code = ErrorForbidden
	case status == http.StatusTooManyRequests:
		code, retryable = ErrorRateLimited, true
	case status == http.StatusBadRequest:
		code = ErrorInvalidInput
	case status == http.StatusConflict:
		code = ErrorConflict
	case status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented:
		if strings.HasPrefix(operation, "jellyfin.refresh.") {
			code = ErrorUnsupported
		} else {
			code = ErrorNotFound
		}
	case status == http.StatusRequestTimeout || status >= http.StatusInternalServerError:
		code, retryable = ErrorUnavailable, true
	}
	return UpstreamError{Code: code, Operation: operation, Status: status, Retryable: retryable}
}

func malformed(operation string) error {
	return UpstreamError{Code: ErrorMalformed, Operation: operation}
}

func invalidInput(operation string) error {
	return UpstreamError{Code: ErrorInvalidInput, Operation: operation}
}

func tooLarge(operation string) error {
	return UpstreamError{Code: ErrorResponseTooLarge, Operation: operation}
}

func parseEndpoint(value string) (*url.URL, error) {
	if strings.TrimSpace(value) != value || value == "" {
		return nil, errors.New("Jellyfin endpoint is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Jellyfin endpoint must be an absolute URL without credentials or query")
	}
	return parsed, nil
}

func validateUserID(value string) error {
	if value == "" {
		return nil
	}
	return validatePathID(value, maxUserIDChars)
}

func validatePathID(value string, max int) error {
	if validateBoundedText(value, max, true) != nil || strings.TrimSpace(value) != value || strings.ContainsAny(value, "/\\?#") {
		return errors.New("identifier is invalid")
	}
	return nil
}

func validateQueryID(value string, max int) error {
	if validateBoundedText(value, max, true) != nil || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n") {
		return errors.New("identifier is invalid")
	}
	return nil
}

func validateBoundedText(value string, max int, nonEmpty bool) error {
	if nonEmpty && value == "" || len(value) > max || !utf8.ValidString(value) {
		return errors.New("text is invalid")
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return errors.New("text contains control characters")
		}
	}
	return nil
}

func optionalText(value *string, max int) (string, error) {
	if value == nil {
		return "", nil
	}
	if err := validateBoundedText(*value, max, false); err != nil {
		return "", err
	}
	return *value, nil
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func configuredToken(config Config) string {
	switch {
	case config.Token != "":
		return config.Token
	case config.APIKey != "":
		return config.APIKey
	case config.AuthToken != "":
		return config.AuthToken
	default:
		return ""
	}
}

func joinBoundedValues(values []string, max int) (string, error) {
	copyValues := append([]string(nil), values...)
	for _, value := range copyValues {
		if validateBoundedText(value, max, true) != nil || strings.ContainsAny(value, "\r\n") {
			return "", errors.New("query value is invalid")
		}
	}
	joined := strings.Join(copyValues, ",")
	if len(joined) > max {
		return "", errors.New("query is too large")
	}
	return joined, nil
}

func completeCoverage(count int) Coverage {
	return Coverage{Completeness: CompletenessComplete, ObservedCount: count, ObservedAt: time.Now().UTC()}
}

func containsItemID(items []Item, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func validateRequestedItems(items []Item, requested []string) error {
	if len(requested) == 0 {
		return nil
	}
	wanted := make(map[string]struct{}, len(requested))
	for _, id := range requested {
		wanted[id] = struct{}{}
	}
	for _, item := range items {
		if _, ok := wanted[item.ID]; !ok {
			return errors.New("response contains item outside requested scope")
		}
	}
	return nil
}
