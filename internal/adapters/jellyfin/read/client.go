// Package read implements the read-only Jellyfin observation boundary.
//
// Jellyfin exposes library membership and media-source details through HTTP.
// This package keeps those observations separate from refresh execution and
// from Seerr's request state. It never issues an upstream mutation.
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
	"math"
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
	defaultMaxItems      = 10_000
	defaultMaxResponse   = 16 << 20
	maxCursorBytes       = 64 << 10
	maxReasonCodes       = 256
	maxProviderIDs       = 32
	maxMediaSources      = 128
	maxSourcePathLength  = 4096
	maxVersionLength     = 128
	maxLibraryIDLength   = 256
	maxItemIDLength      = 256
	maxTitleLength       = 4096
	maxSnapshotPartBytes = 8192
)

const (
	apiMediaFolders = "/Library/MediaFolders"
	apiViews        = "/Users/Me/Views"
	apiItems        = "/Items"
	apiSystemPublic = "/System/Info/Public"
)

// Config contains one Jellyfin instance's endpoint, token and observation
// bounds. APIKey, Token and AuthToken are aliases accepted for callers that
// use Jellyfin's different names; APIKey has precedence when supplied.
// Mappings translate Jellyfin's absolute media paths into configured roots.
type Config struct {
	ConnectionID domain.ConfigID
	Endpoint     string
	APIKey       string
	Token        string
	AuthToken    string
	UserID       string
	HTTPClient   *http.Client
	Mappings     []domain.PathMapping

	MaxPageSize     int
	MaxPages        int
	MaxItems        int
	MaxResponseSize int64
}

// VersionObservation is a sanitized read of Jellyfin's public system info.
type VersionObservation struct {
	ConnectionID domain.ConfigID
	ProductName  string
	ServerName   string
	Version      string
	ObservedAt   time.Time
}

// LibraryObservation identifies one supported Jellyfin collection folder.
type LibraryObservation struct {
	ExternalID     string
	Name           string
	CollectionType string
	Type           string
	ObservedAt     time.Time
}

// ProviderRelationship preserves every provider ID instead of reducing a
// title to one guessed provider. Values are scoped by ConnectionID on the
// containing observation.
type ProviderRelationship struct {
	Provider string
	ID       string
}

// MediaSourceObservation is the selected playable-media evidence from one
// Jellyfin media source. Path remains an upstream observation; MappedTarget
// is present only after an unambiguous configured-root mapping.
type MediaSourceObservation struct {
	ID               string
	Path             string
	Protocol         string
	LocationType     string
	MediaType        string
	MappedTarget     *domain.FileTarget
	PlayableEvidence []string
}

// ItemObservation retains Jellyfin-specific library/provider/source evidence
// while Item remains the frozen common port value used by aggregation.
type ItemObservation struct {
	Item                  ports.MediaServerItem
	LibraryID             string
	LibraryName           string
	ItemType              string
	LocationType          string
	ProviderIDs           map[string]string
	ProviderRelationships []ProviderRelationship
	MediaSources          []MediaSourceObservation
	UnavailableReason     string
	Evidence              []string
}

// DetailedPage is the adapter-specific page for callers that need independent
// playable-media evidence and provider relationships.
type DetailedPage struct {
	Items      []ItemObservation
	NextCursor string
	Coverage   domain.Coverage
}

// Client is an authenticated, read-only Jellyfin HTTP client.
type Client struct {
	config    Config
	endpoint  *url.URL
	http      *http.Client
	cursorKey []byte
}

var _ ports.MediaServerReadPort = (*Client)(nil)
var _ ports.MediaServerRefreshPort = (*Client)(nil)
var _ ports.CapabilityPort = (*Client)(nil)

// New validates configuration without contacting Jellyfin.
func New(config Config) (*Client, error) {
	if !config.ConnectionID.Valid() {
		return nil, errors.New("Jellyfin connection id is invalid")
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
	if config.MaxItems <= 0 {
		config.MaxItems = defaultMaxItems
	}
	if config.MaxPageSize > config.MaxItems {
		config.MaxPageSize = config.MaxItems
	}
	if config.MaxResponseSize <= 0 {
		config.MaxResponseSize = defaultMaxResponse
	}
	if config.MaxResponseSize > math.MaxInt64-1 {
		return nil, errors.New("Jellyfin response bound is invalid")
	}
	if err := validateUserID(config.UserID); err != nil {
		return nil, err
	}
	if err := validateMappings(config.ConnectionID, config.Mappings); err != nil {
		return nil, err
	}
	config.Mappings = append([]domain.PathMapping(nil), config.Mappings...)

	baseClient := http.DefaultClient
	if config.HTTPClient != nil {
		baseClient = config.HTTPClient
	}
	copyClient := *baseClient
	if copyClient.Timeout == 0 {
		copyClient.Timeout = 30 * time.Second
	}
	// A refresh is deliberately not sent by this package. Refusing redirects
	// also prevents a read request carrying a token from escaping the endpoint
	// authority or being replayed against an unexpected route.
	copyClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	cursorKey := make([]byte, 32)
	if _, err := cryptorand.Read(cursorKey); err != nil {
		return nil, errors.New("Jellyfin cursor key setup failed")
	}
	return &Client{config: config, endpoint: endpoint, http: &copyClient, cursorKey: cursorKey}, nil
}

// NewClient is an explicit constructor alias.
func NewClient(config Config) (*Client, error) { return New(config) }

// ConnectionID returns the stable configuration scope for every observation.
func (client *Client) ConnectionID() domain.ConfigID { return client.config.ConnectionID }

// ScopedIdentity prevents the same Jellyfin ID from colliding across
// configured instances.
func (client *Client) ScopedIdentity(externalID string) string {
	if strings.TrimSpace(externalID) == "" {
		return ""
	}
	return client.config.ConnectionID.String() + ":" + strings.TrimSpace(externalID)
}

// Version performs a read-only Jellyfin public system-info probe.
func (client *Client) Version(ctx context.Context, connectionID domain.ConfigID) (VersionObservation, error) {
	result := VersionObservation{ConnectionID: connectionID, ObservedAt: time.Now().UTC()}
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	body, err := client.get(ctx, "jellyfin.version", apiSystemPublic, nil)
	if err != nil {
		return result, err
	}
	var info systemInfoDTO
	if err := decodeJSON(body, &info); err != nil {
		return result, malformed("jellyfin.version")
	}
	result.ProductName = boundedText(info.ProductName, maxVersionLength)
	result.ServerName = boundedText(info.ServerName, maxVersionLength)
	result.Version = boundedText(info.Version, maxVersionLength)
	if result.Version == "" {
		return result, malformed("jellyfin.version")
	}
	return result, nil
}

// Libraries lists supported collection folders. Music/books and other
// non-media collections are returned as observations too, but are not used to
// infer playable Movie/Series availability.
func (client *Client) Libraries(ctx context.Context, connectionID domain.ConfigID) ([]LibraryObservation, error) {
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return nil, err
	}
	body, err := client.get(ctx, "jellyfin.libraries", apiMediaFolders, nil)
	if err != nil {
		if code, ok := upstreamCode(err); ok && code == domain.OutcomeUnsupported {
			// Some Jellyfin-compatible deployments expose user views instead of
			// MediaFolders. The fallback is still read-only and remains scoped.
			body, err = client.get(ctx, "jellyfin.views", client.viewsEndpoint(), nil)
		}
		if err != nil {
			return nil, err
		}
	}
	items, err := decodeLibraryCollection(body)
	if err != nil {
		return nil, malformed("jellyfin.libraries")
	}
	now := time.Now().UTC()
	libraries := make([]LibraryObservation, 0, len(items))
	for index, item := range items {
		id := boundedText(item.ID, maxLibraryIDLength)
		name := boundedText(firstNonEmpty(item.Name, item.Title), maxTitleLength)
		if id == "" || name == "" {
			return nil, malformed(fmt.Sprintf("jellyfin.libraries.%d", index))
		}
		libraries = append(libraries, LibraryObservation{
			ExternalID: id, Name: name, CollectionType: boundedText(item.CollectionType, maxVersionLength),
			Type: boundedText(item.Type, maxVersionLength), ObservedAt: now,
		})
	}
	return libraries, nil
}

// ListLibraries is an alias kept for callers whose naming mirrors the HTTP
// resource.
func (client *Client) ListLibraries(ctx context.Context, connectionID domain.ConfigID) ([]LibraryObservation, error) {
	return client.Libraries(ctx, connectionID)
}

// List implements ports.MediaServerReadPort and returns the common item view.
func (client *Client) List(ctx context.Context, connectionID domain.ConfigID, cursor string, limit int) (ports.Page[ports.MediaServerItem], error) {
	detailed, err := client.ListDetailed(ctx, connectionID, cursor, limit)
	if err != nil {
		return ports.Page[ports.MediaServerItem]{}, err
	}
	items := make([]ports.MediaServerItem, 0, len(detailed.Items))
	for _, item := range detailed.Items {
		items = append(items, item.Item)
	}
	return ports.Page[ports.MediaServerItem]{Items: items, NextCursor: detailed.NextCursor, Coverage: detailed.Coverage}, nil
}

// ListDetailed reads a bounded local page from the current Jellyfin library
// snapshot. Jellyfin's Items endpoint is offset-based; the signed cursor
// carries the local offset and snapshot digest so a changed upstream result
// cannot silently become a complete absence proof.
func (client *Client) ListDetailed(ctx context.Context, connectionID domain.ConfigID, cursor string, requestedLimit int) (DetailedPage, error) {
	var result DetailedPage
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
	if state.SourceID != "" {
		if state.Collection != "inventory" || state.PageSize != limit && requestedLimit > 0 {
			return result, invalidInput("jellyfin.inventory.cursor")
		}
		limit = state.PageSize
	}
	if state.SourceID == "" {
		state.SourceID, err = domain.NewRuntimeID()
		if err != nil {
			return result, errors.New("Jellyfin inventory source identity unavailable")
		}
		state.StartedAt = time.Now().UTC()
		state.PageSize = limit
		state.Collection = "inventory"
	}
	if state.PageCount >= client.config.MaxPages || state.ObservedCount >= client.config.MaxItems {
		return result, invalidInput("jellyfin.inventory.cursor")
	}

	snapshot, err := client.collectSnapshot(ctx, connectionID)
	if err != nil {
		return result, err
	}
	now := time.Now().UTC()
	result.Coverage = domain.Coverage{
		SourceID: state.SourceID, ConnectionID: connectionID,
		Completeness: snapshot.completeness, ObservedCount: int64(state.ObservedCount),
		SnapshotRevision: snapshot.revision, StartedAt: timePtr(state.StartedAt), ObservedAt: now,
	}
	for _, reason := range snapshot.reasons {
		addReason(&result.Coverage.ReasonCodes, reason)
	}
	for _, reason := range state.Reasons {
		addReason(&result.Coverage.ReasonCodes, reason)
	}
	if state.SnapshotRevision != "" && state.SnapshotRevision != snapshot.revision {
		addReason(&result.Coverage.ReasonCodes, "inventory_snapshot_changed")
		state.Reasons = appendReason(state.Reasons, "inventory_snapshot_changed")
	}
	if state.Offset < 0 || state.Offset > len(snapshot.items) {
		addReason(&result.Coverage.ReasonCodes, "inventory_offset_changed")
		state.Reasons = appendReason(state.Reasons, "inventory_offset_changed")
		if state.Offset < 0 {
			state.Offset = 0
		}
		if state.Offset > len(snapshot.items) {
			state.Offset = len(snapshot.items)
		}
	}
	end := state.Offset + limit
	if end < state.Offset || end > len(snapshot.items) {
		end = len(snapshot.items)
	}
	result.Items = append([]ItemObservation(nil), snapshot.items[state.Offset:end]...)
	state.Offset = end
	state.PageCount++
	state.ObservedCount += len(result.Items)
	result.Coverage.ObservedCount = int64(state.ObservedCount)

	more := state.Offset < len(snapshot.items)
	// An offset traversal without an upstream snapshot token is still safe to
	// page locally: the result remains partial and the signed cursor carries
	// the digest so a changed re-read stops with an explicit reason. Other
	// partial/unknown conditions can mean that the local result itself is
	// truncated or ambiguous, so do not offer a continuation for those.
	continuationSafe := snapshot.completeness != domain.CompletenessUnknown
	for _, reason := range snapshot.reasons {
		if reason != "pagination_snapshot_unverified" {
			continuationSafe = false
			break
		}
	}
	stop := !more || state.PageCount >= client.config.MaxPages || state.ObservedCount >= client.config.MaxItems || state.SnapshotRevision != "" && state.SnapshotRevision != snapshot.revision || !continuationSafe
	if state.PageCount >= client.config.MaxPages && more {
		addReason(&result.Coverage.ReasonCodes, "pagination_limit")
	}
	if state.ObservedCount >= client.config.MaxItems && more {
		addReason(&result.Coverage.ReasonCodes, "items_limit")
	}
	if !stop && more {
		state.SnapshotRevision = snapshot.revision
		result.NextCursor, err = client.encodeCursor(state)
		if err != nil {
			addReason(&result.Coverage.ReasonCodes, "pagination_cursor_limit")
			stop = true
		}
	}
	if more && !stop && result.NextCursor != "" {
		addReason(&result.Coverage.ReasonCodes, "pagination_continues")
	}
	if len(result.Coverage.ReasonCodes) > 0 {
		result.Coverage.Completeness = domain.CompletenessPartial
	}
	if stop {
		completed := time.Now().UTC()
		result.Coverage.CompletedAt = &completed
		result.Coverage.ObservedAt = completed
		if snapshot.completeness == domain.CompletenessUnknown {
			result.Coverage.Completeness = domain.CompletenessUnknown
		} else if len(result.Coverage.ReasonCodes) > 0 {
			result.Coverage.Completeness = domain.CompletenessPartial
		}
	}
	return result, nil
}

// ObserveItem reads one item by Jellyfin ID and returns its independent media
// source evidence. It does not infer availability from Arr or Seerr state.
func (client *Client) ObserveItem(ctx context.Context, connectionID domain.ConfigID, externalID string) (ItemObservation, error) {
	var result ItemObservation
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	id := externalID
	if id == "" || strings.TrimSpace(id) != id || len(id) > maxItemIDLength || strings.ContainsAny(id, "\r\n") {
		return result, invalidInput("jellyfin.item.id")
	}
	query := url.Values{
		"Ids":    []string{id},
		"Fields": []string{"ProviderIds,MediaSources,Path,LocationType,MediaType"},
	}
	if client.config.UserID != "" {
		query.Set("UserId", client.config.UserID)
	}
	body, err := client.get(ctx, "jellyfin.item", apiItems, query)
	if err != nil {
		return result, err
	}
	items, err := decodeItemCollection(body)
	if err != nil {
		return result, malformed("jellyfin.item")
	}
	for _, dto := range items {
		if strings.TrimSpace(dto.ID) != id {
			continue
		}
		return client.observeItem(dto, "", "")
	}
	return result, domain.UpstreamError{Code: domain.OutcomeUnavailable, Status: http.StatusNotFound, Operation: "jellyfin.item", Detail: "item was not observed"}
}

// Item is a naming alias for ObserveItem.
func (client *Client) Item(ctx context.Context, connectionID domain.ConfigID, externalID string) (ItemObservation, error) {
	return client.ObserveItem(ctx, connectionID, externalID)
}

// Refresh implements the frozen refresh port with an explicit unsupported
// result. The read lane has no version-pinned refresh route; X-09 owns the
// future mutation adapter after a disposable write fixture proves it.
func (client *Client) Refresh(ctx context.Context, connectionID domain.ConfigID, request ports.RefreshRequest) (ports.RefreshResult, error) {
	result := ports.RefreshResult{ObservedAt: time.Now().UTC()}
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	switch request.Scope {
	case ports.RefreshLibrary:
		if request.ExternalID != "" {
			return result, invalidInput("jellyfin.refresh.library")
		}
	case ports.RefreshItem:
		if strings.TrimSpace(request.ExternalID) != request.ExternalID || strings.TrimSpace(request.ExternalID) == "" || len(request.ExternalID) > maxItemIDLength || strings.ContainsAny(request.ExternalID, "\r\n") {
			return result, invalidInput("jellyfin.refresh.item")
		}
	default:
		return result, invalidInput("jellyfin.refresh.scope")
	}
	result.Evidence = []string{"refresh_scope_unverified", "read_adapter_no_mutation"}
	return result, domain.UpstreamError{
		Code: domain.OutcomeUnsupported, Operation: "jellyfin.refresh",
		Detail: "refresh capability is not version-pinned",
	}
}

// Capabilities reports the tested read surfaces. The public compatibility
// matrix does not pin a Jellyfin release or a versioned catalog fixture yet,
// so a successful system-info response can establish only the version
// observation. It must not be promoted into a catalog capability claim.
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
		versionState, versionReason = domain.CapabilityUnsupported, "Jellyfin public system-info endpoint is unsupported"
	}
	readState := domain.CapabilityUnknown
	readReason := "Jellyfin catalog route compatibility is not pinned to a verified release"
	if versionErr != nil {
		switch {
		case versionState == domain.CapabilityUnsupported:
			readReason = "Jellyfin catalog compatibility is unknown because the system-info endpoint is unsupported"
		case versionState == domain.CapabilityUnknown:
			readReason = "Jellyfin catalog compatibility is unknown because the version observation is unavailable"
		}
	}
	caps := []domain.Capability{
		{Name: "jellyfin.version", State: versionState, Version: version.Version, Reason: versionReason, ObservedAt: now},
		{Name: "jellyfin.libraries", State: readState, Version: version.Version, Reason: readReason, Evidence: []string{"Library/MediaFolders or user Views", "compatibility_version_unpinned"}, ObservedAt: now},
		{Name: "jellyfin.items", State: readState, Version: version.Version, Reason: readReason, Evidence: []string{"Items with provider and media-source fields", "compatibility_version_unpinned"}, ObservedAt: now},
		{Name: "jellyfin.provider-ids", State: readState, Version: version.Version, Reason: readReason, Evidence: []string{"compatibility_version_unpinned"}, ObservedAt: now},
	}
	playableState := domain.CapabilityUnknown
	playableReason := "Jellyfin playable-media compatibility is not pinned to a verified release"
	if len(client.config.Mappings) == 0 {
		playableReason = "Jellyfin playable-media compatibility is unknown because no configured root mapping exists"
	}
	caps = append(caps,
		domain.Capability{Name: "jellyfin.playable-media", State: playableState, Version: version.Version, Reason: playableReason, ObservedAt: now},
		domain.Capability{Name: "jellyfin.refresh", State: domain.CapabilityUnsupported, Version: version.Version, Reason: "refresh route and version are not pinned; read adapter never mutates", Evidence: []string{"refresh_scope_unverified"}, ObservedAt: now},
	)
	return caps, nil
}

type snapshot struct {
	items        []ItemObservation
	revision     string
	completeness domain.Completeness
	reasons      []string
}

func (client *Client) collectSnapshot(ctx context.Context, connectionID domain.ConfigID) (snapshot, error) {
	libraries, err := client.Libraries(ctx, connectionID)
	if err != nil {
		return snapshot{}, err
	}
	result := snapshot{completeness: domain.CompletenessComplete}
	seen := make(map[string]struct{})
	seenLibraries := make(map[string]LibraryObservation, len(libraries))
	pageCount := 0
	for _, library := range libraries {
		if err := ctx.Err(); err != nil {
			return snapshot{}, err
		}
		if previous, exists := seenLibraries[library.ExternalID]; exists {
			// A repeated library identity makes the collection boundary
			// ambiguous, even when the duplicate rows happen to have equal
			// names. Keep the observation bounded but never certify absence
			// from it. The first row remains the only row traversed so a
			// duplicate cannot manufacture repeated item reads.
			addReason(&result.reasons, "library_identity_duplicate")
			if previous.Name != library.Name || previous.CollectionType != library.CollectionType || previous.Type != library.Type {
				addReason(&result.reasons, "library_identity_conflict")
			}
			result.completeness = domain.CompletenessPartial
			continue
		}
		seenLibraries[library.ExternalID] = library
		if len(result.items) >= client.config.MaxItems {
			result.completeness = domain.CompletenessPartial
			addReason(&result.reasons, "items_limit")
			break
		}
		items, reasons, pages, collectErr := client.collectLibraryItems(ctx, library, client.config.MaxItems-len(result.items), client.config.MaxPages-pageCount)
		pageCount += pages
		for _, reason := range reasons {
			addReason(&result.reasons, reason)
		}
		if collectErr != nil {
			if len(result.items) == 0 && pageCount == pages {
				return snapshot{}, collectErr
			}
			result.completeness = domain.CompletenessPartial
			addReason(&result.reasons, "library_items_unavailable")
		}
		for _, item := range items {
			id := item.Item.ExternalID
			if id != "" {
				if _, exists := seen[id]; exists {
					addReason(&result.reasons, "library_item_overlap")
					continue
				}
				seen[id] = struct{}{}
			}
			result.items = append(result.items, item)
		}
		if pageCount >= client.config.MaxPages && library.ExternalID != "" {
			result.completeness = domain.CompletenessPartial
			addReason(&result.reasons, "pagination_limit")
			break
		}
	}
	if len(libraries) > 1 || pageCount > 1 {
		// Jellyfin's offset Items endpoint does not expose an immutable
		// collection token. Multiple upstream reads can therefore observe a
		// deletion, insertion or reorder between offsets/libraries. A local
		// digest records what was seen; it cannot prove that the traversal was
		// stable, so retain partial coverage until a versioned boundary exists.
		addReason(&result.reasons, "pagination_snapshot_unverified")
		result.completeness = domain.CompletenessPartial
	}
	result.revision = snapshotDigest(result.items)
	if len(result.reasons) > 0 && result.completeness == domain.CompletenessComplete {
		result.completeness = domain.CompletenessPartial
	}
	return result, nil
}

func (client *Client) collectLibraryItems(ctx context.Context, library LibraryObservation, remaining, pagesRemaining int) ([]ItemObservation, []string, int, error) {
	if remaining <= 0 {
		return nil, []string{"items_limit"}, 0, nil
	}
	if pagesRemaining <= 0 {
		return nil, []string{"pagination_limit"}, 0, nil
	}
	var result []ItemObservation
	var reasons []string
	offset := 0
	pages := 0
	for len(result) < remaining && pages < pagesRemaining {
		if err := ctx.Err(); err != nil {
			return result, reasons, pages, err
		}
		pageLimit := minInt(client.config.MaxPageSize, remaining-len(result))
		query := url.Values{
			"SortBy":              []string{"SortName"},
			"SortOrder":           []string{"Ascending"},
			"IncludeItemTypes":    []string{"Series,Movie,Episode"},
			"Recursive":           []string{"true"},
			"StartIndex":          []string{strconv.Itoa(offset)},
			"Limit":               []string{strconv.Itoa(pageLimit)},
			"ParentId":            []string{library.ExternalID},
			"Fields":              []string{"ProviderIds,MediaSources,Path,LocationType,MediaType,Type"},
			"collapseBoxSetItems": []string{"false"},
		}
		if client.config.UserID != "" {
			query.Set("UserId", client.config.UserID)
		}
		body, err := client.get(ctx, "jellyfin.library.items", apiItems, query)
		if err != nil {
			return result, reasons, pages, err
		}
		items, total, fullArray, err := decodeItemsPage(body)
		if err != nil {
			return result, reasons, pages, malformed("jellyfin.library.items")
		}
		pages++
		responseBoundHit := false
		if len(items) > pageLimit {
			items = items[:pageLimit]
			responseBoundHit = true
			addReason(&reasons, "pagination_response_exceeded_limit")
		}
		if len(items) == 0 {
			if total > int64(offset) {
				addReason(&reasons, "pagination_empty_before_total")
			}
			break
		}
		for index, item := range items {
			observation, observeErr := client.observeItem(item, library.ExternalID, library.Name)
			if observeErr != nil {
				return result, reasons, pages, observeErr
			}
			if observation.Item.ExternalID == "" {
				addReason(&reasons, fmt.Sprintf("item_%d_identity_unknown", index))
				continue
			}
			result = append(result, observation)
			if len(result) >= remaining {
				if !fullArray && (total < 0 || total > int64(offset+len(items))) {
					addReason(&reasons, "items_limit")
				}
				break
			}
		}
		if responseBoundHit {
			break
		}
		if !fullArray && total < 0 {
			if len(items) == pageLimit {
				addReason(&reasons, "pagination_total_missing")
			}
			break
		}
		if !fullArray && total >= 0 && total < int64(offset+len(items)) {
			addReason(&reasons, "pagination_total_inconsistent")
			break
		}
		if fullArray || total <= int64(offset+len(items)) || len(items) < pageLimit {
			break
		}
		nextOffset := offset + len(items)
		if nextOffset <= offset {
			addReason(&reasons, "pagination_stalled")
			break
		}
		offset = nextOffset
	}
	if pages >= pagesRemaining && len(result) < remaining {
		addReason(&reasons, "pagination_limit")
	}
	if pages > 1 {
		// The endpoint is offset-based and supplies no immutable snapshot
		// identity. Never turn a multi-request traversal into complete
		// absence evidence merely because totals happened to agree.
		addReason(&reasons, "pagination_snapshot_unverified")
	}
	return result, reasons, pages, nil
}

func (client *Client) observeItem(item itemDTO, libraryID, libraryName string) (ItemObservation, error) {
	now := time.Now().UTC()
	id := boundedText(item.ID, maxItemIDLength)
	title := boundedText(firstNonEmpty(item.Name, item.Title), maxTitleLength)
	providers, relationships, providerReasons := providerValues(item.ProviderIDs)
	itemValue := ports.MediaServerItem{
		ExternalID: id,
		ProviderID: firstProviderID(providers),
		Title:      title,
		Playable:   false,
		ObservedAt: now,
	}
	result := ItemObservation{
		Item: itemValue, LibraryID: boundedText(libraryID, maxLibraryIDLength), LibraryName: boundedText(libraryName, maxTitleLength),
		ItemType: boundedText(item.Type, maxVersionLength), LocationType: boundedText(item.LocationType, maxVersionLength),
		ProviderIDs: providers, ProviderRelationships: relationships, Evidence: providerReasons,
	}
	if id == "" {
		result.UnavailableReason = "identity_unknown"
		result.Evidence = appendReason(result.Evidence, "identity_unknown")
		return result, nil
	}
	if title == "" {
		result.Evidence = appendReason(result.Evidence, "title_unknown")
	}
	itemLocationType := strings.TrimSpace(item.LocationType)
	if itemLocationType != "" && !strings.EqualFold(itemLocationType, "FileSystem") {
		result.UnavailableReason = "location_not_playable"
		result.Evidence = appendReason(result.Evidence, "location_not_playable")
	} else {
		sources := item.MediaSources
		pathOnly := false
		if len(sources) == 0 && strings.TrimSpace(item.Path) != "" {
			// Jellyfin can omit MediaSources for a normal item read while still
			// returning its exact Path. Keep that evidence bounded and typed,
			// without treating a title-only item as playable.
			sources = []mediaSourceDTO{{ID: id + ":path", Path: item.Path, Protocol: "File", LocationType: item.LocationType, MediaType: item.MediaType}}
			pathOnly = true
			result.Evidence = appendReason(result.Evidence, "media_source_from_item_path")
		}
		for index, source := range sources {
			if index >= maxMediaSources {
				result.Evidence = appendReason(result.Evidence, "media_sources_limit")
				break
			}
			observed, playable := client.observeMediaSource(source)
			if pathOnly {
				// An item-level Path is useful correlation evidence, but it is
				// not native MediaSources evidence and cannot claim playability,
				// even when a configured mapping can translate the path.
				playable = false
				observed.PlayableEvidence = appendReason(observed.PlayableEvidence, "media_source_path_only_unverified")
			} else if reason := nativeItemMediaTypeReason(item, source); reason != "" {
				playable = false
				observed.PlayableEvidence = appendReason(observed.PlayableEvidence, reason)
			}
			result.MediaSources = append(result.MediaSources, observed)
			result.Evidence = appendReasons(result.Evidence, observed.PlayableEvidence...)
			if playable {
				result.Item.Playable = true
			}
		}
		if len(sources) == 0 {
			result.UnavailableReason = "media_source_missing"
			result.Evidence = appendReason(result.Evidence, "media_source_missing")
		} else if !result.Item.Playable && result.UnavailableReason == "" {
			result.UnavailableReason = "playable_media_unverified"
			result.Evidence = appendReason(result.Evidence, "playable_media_unverified")
		}
	}
	return result, nil
}

func (client *Client) observeMediaSource(source mediaSourceDTO) (MediaSourceObservation, bool) {
	result := MediaSourceObservation{
		ID:       boundedText(source.ID, maxItemIDLength),
		Protocol: boundedText(source.Protocol, maxVersionLength), LocationType: boundedText(source.LocationType, maxVersionLength),
		MediaType: boundedText(source.MediaType, maxVersionLength),
	}
	if reason := nativeMediaSourceReason(source); reason != "" {
		result.PlayableEvidence = appendReason(result.PlayableEvidence, reason)
		if pathValue := normalizeRemotePath(source.Path); pathValue != "" {
			result.Path = pathValue
		}
		return result, false
	}
	pathValue := normalizeRemotePath(source.Path)
	if pathValue == "" {
		result.PlayableEvidence = appendReason(result.PlayableEvidence, "media_source_path_invalid")
		return result, false
	}
	result.Path = pathValue
	if len(client.config.Mappings) == 0 {
		// A path is still a genuine native playable-media observation when no
		// namespace mapping is configured. Capability reporting marks the
		// correlation as unknown; this flag only says Jellyfin supplied a
		// playable source.
		result.PlayableEvidence = appendReason(result.PlayableEvidence, "media_source_present")
		return result, true
	}
	target, mapped, ambiguous := client.mapPath(pathValue)
	if ambiguous {
		result.PlayableEvidence = appendReason(result.PlayableEvidence, "media_source_mapping_ambiguous")
		return result, false
	}
	if !mapped {
		result.PlayableEvidence = appendReason(result.PlayableEvidence, "media_source_path_unmapped")
		return result, false
	}
	result.MappedTarget = &target
	result.PlayableEvidence = appendReason(result.PlayableEvidence, "media_source_mapped")
	return result, true
}

// nativeMediaSourceReason validates the fields that make a Jellyfin source a
// local video source. A path by itself, or a source with metadata omitted or
// outside the tested File/FileSystem/Video shape, is observation evidence but
// cannot establish playability.
func nativeMediaSourceReason(source mediaSourceDTO) string {
	if strings.TrimSpace(source.ID) == "" {
		return "media_source_id_missing"
	}
	if strings.TrimSpace(source.Protocol) == "" {
		return "media_source_protocol_missing"
	}
	if !strings.EqualFold(strings.TrimSpace(source.Protocol), "File") {
		return "media_source_protocol_unsupported"
	}
	if strings.TrimSpace(source.LocationType) == "" {
		return "media_source_location_missing"
	}
	if !strings.EqualFold(strings.TrimSpace(source.LocationType), "FileSystem") {
		return "media_source_location_unsupported"
	}
	if strings.TrimSpace(source.MediaType) == "" {
		return "media_source_media_type_missing"
	}
	if strings.TrimSpace(source.Path) == "" {
		return "media_source_path_missing"
	}
	if normalizeRemotePath(source.Path) == "" {
		return "media_source_path_invalid"
	}
	return ""
}

func nativeItemMediaTypeReason(item itemDTO, source mediaSourceDTO) string {
	itemMediaType := strings.TrimSpace(item.MediaType)
	sourceMediaType := strings.TrimSpace(source.MediaType)
	if itemMediaType == "" {
		return "item_media_type_missing"
	}
	if sourceMediaType == "" {
		// nativeMediaSourceReason reports the more specific source omission.
		return ""
	}
	if !strings.EqualFold(itemMediaType, sourceMediaType) {
		return "media_source_media_type_mismatch"
	}
	if !strings.EqualFold(itemMediaType, "Video") {
		return "item_media_type_unsupported"
	}
	return ""
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
		if len(prefix) == bestLength && (selected.RootID != mapping.RootID || strings.Trim(selected.DestinationPrefix, "/") != strings.Trim(mapping.DestinationPrefix, "/")) {
			ambiguous = true
		}
	}
	if bestLength < 0 || ambiguous {
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
			return errors.New("Jellyfin path mapping is invalid")
		}
		if mapping.DestinationPrefix != "" {
			if err := domain.ValidateRelativePath(strings.Trim(mapping.DestinationPrefix, "/")); err != nil {
				return errors.New("Jellyfin path mapping destination is invalid")
			}
		}
	}
	return nil
}

func validateUserID(value string) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) != value || len(value) > maxItemIDLength || strings.ContainsAny(value, "/\\?#") {
		return errors.New("Jellyfin user id is invalid")
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return errors.New("Jellyfin user id is invalid")
		}
	}
	return nil
}

func (client *Client) viewsEndpoint() string {
	if client.config.UserID == "" {
		return apiViews
	}
	return "/Users/" + client.config.UserID + "/Views"
}

type cursorState struct {
	SourceID         domain.RuntimeID `json:"sourceId"`
	Collection       string           `json:"collection"`
	PageSize         int              `json:"pageSize"`
	PageCount        int              `json:"pageCount"`
	Offset           int              `json:"offset"`
	ObservedCount    int              `json:"observedCount"`
	SnapshotRevision string           `json:"snapshotRevision"`
	StartedAt        time.Time        `json:"startedAt"`
	Reasons          []string         `json:"reasons,omitempty"`
}

func (client *Client) encodeCursor(state cursorState) (string, error) {
	payload, err := json.Marshal(state)
	if err != nil || len(payload) > maxCursorBytes {
		return "", invalidInput("jellyfin.inventory.cursor")
	}
	mac := hmac.New(sha256.New, client.cursorKey)
	_, _ = mac.Write(payload)
	encoded := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(encoded) > maxCursorBytes {
		return "", invalidInput("jellyfin.inventory.cursor")
	}
	return encoded, nil
}

func (client *Client) decodeCursor(value string) (cursorState, error) {
	if value == "" {
		return cursorState{}, nil
	}
	if len(value) > maxCursorBytes {
		return cursorState{}, invalidInput("jellyfin.inventory.cursor")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return cursorState{}, invalidInput("jellyfin.inventory.cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) > maxCursorBytes {
		return cursorState{}, invalidInput("jellyfin.inventory.cursor")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursorState{}, invalidInput("jellyfin.inventory.cursor")
	}
	mac := hmac.New(sha256.New, client.cursorKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursorState{}, invalidInput("jellyfin.inventory.cursor")
	}
	var state cursorState
	if err := decodeJSON(payload, &state); err != nil || !state.SourceID.Valid() || state.Collection == "" || state.PageSize <= 0 || state.Offset < 0 || state.PageCount <= 0 || state.ObservedCount < 0 {
		return cursorState{}, invalidInput("jellyfin.inventory.cursor")
	}
	return state, nil
}

type systemInfoDTO struct {
	ProductName string `json:"ProductName"`
	ServerName  string `json:"ServerName"`
	Version     string `json:"Version"`
}

type libraryDTO struct {
	ID             string `json:"Id"`
	Name           string `json:"Name"`
	Title          string `json:"title"`
	Type           string `json:"Type"`
	CollectionType string `json:"CollectionType"`
}

type libraryEnvelope struct {
	Items json.RawMessage `json:"Items"`
}

type itemDTO struct {
	ID           string            `json:"Id"`
	Name         string            `json:"Name"`
	Title        string            `json:"title"`
	Type         string            `json:"Type"`
	LocationType string            `json:"LocationType"`
	MediaType    string            `json:"MediaType"`
	Path         string            `json:"Path"`
	ProviderIDs  map[string]string `json:"ProviderIds"`
	MediaSources []mediaSourceDTO  `json:"MediaSources"`
}

type itemEnvelope struct {
	Items            json.RawMessage `json:"Items"`
	TotalRecordCount json.RawMessage `json:"TotalRecordCount"`
	StartIndex       int             `json:"StartIndex"`
}

type mediaSourceDTO struct {
	ID           string `json:"Id"`
	Path         string `json:"Path"`
	Protocol     string `json:"Protocol"`
	LocationType string `json:"LocationType"`
	MediaType    string `json:"MediaType"`
}

func decodeLibraryCollection(body []byte) ([]libraryDTO, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" || trimmed == "null" {
		return nil, errors.New("empty library response")
	}
	if strings.HasPrefix(trimmed, "[") {
		var items []libraryDTO
		if err := decodeJSON(body, &items); err != nil {
			return nil, err
		}
		return items, nil
	}
	var envelope libraryEnvelope
	if err := decodeJSON(body, &envelope); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(envelope.Items)) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Items), []byte("null")) {
		return nil, errors.New("library items are missing")
	}
	var items []libraryDTO
	if err := decodeJSON(envelope.Items, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func decodeItemCollection(body []byte) ([]itemDTO, error) {
	items, _, _, err := decodeItemsPage(body)
	return items, err
}

func decodeItemsPage(body []byte) ([]itemDTO, int64, bool, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" || trimmed == "null" {
		return nil, 0, false, errors.New("empty item response")
	}
	if strings.HasPrefix(trimmed, "[") {
		var items []itemDTO
		if err := decodeJSON(body, &items); err != nil {
			return nil, 0, true, err
		}
		return items, int64(len(items)), true, nil
	}
	var envelope itemEnvelope
	if err := decodeJSON(body, &envelope); err != nil {
		return nil, 0, false, err
	}
	if len(bytes.TrimSpace(envelope.Items)) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Items), []byte("null")) {
		return nil, 0, false, errors.New("item items are missing")
	}
	var items []itemDTO
	if err := decodeJSON(envelope.Items, &items); err != nil {
		return nil, 0, false, err
	}
	trimmedTotal := strings.TrimSpace(string(envelope.TotalRecordCount))
	if trimmedTotal == "" || trimmedTotal == "null" {
		return items, -1, false, nil
	}
	var total json.Number
	if err := decodeJSON(envelope.TotalRecordCount, &total); err != nil {
		return nil, 0, false, errors.New("item total is invalid")
	}
	parsedTotal, err := strconv.ParseInt(total.String(), 10, 64)
	if err != nil || parsedTotal < 0 {
		return nil, 0, false, errors.New("negative item total")
	}
	return items, parsedTotal, false, nil
}

func providerValues(values map[string]string) (map[string]string, []ProviderRelationship, []string) {
	if len(values) == 0 {
		return nil, nil, nil
	}
	providers := make(map[string]string, minInt(len(values), maxProviderIDs))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var reasons []string
	for _, key := range keys {
		if len(providers) >= maxProviderIDs {
			addReason(&reasons, "provider_ids_limit")
			break
		}
		name := strings.TrimSpace(key)
		value := boundedText(values[key], maxItemIDLength)
		if name == "" || value == "" {
			addReason(&reasons, "provider_id_malformed")
			continue
		}
		providers[name] = value
	}
	relationships := make([]ProviderRelationship, 0, len(providers))
	for _, key := range sortedKeys(providers) {
		relationships = append(relationships, ProviderRelationship{Provider: key, ID: providers[key]})
	}
	return providers, relationships, reasons
}

func firstProviderID(values map[string]string) string {
	for _, key := range []string{"Tmdb", "TMDB", "TheMovieDb", "tmdb", "Tvdb", "TVDB", "tvdb", "Imdb", "IMDB", "imdb", "AniDB", "anidb"} {
		if value := strings.TrimSpace(values[key]); value != "" {
			return value
		}
	}
	keys := sortedKeys(values)
	if len(keys) == 0 {
		return ""
	}
	return values[keys[0]]
}

func snapshotDigest(items []ItemObservation) string {
	hash := sha256.New()
	for _, item := range items {
		_, _ = io.WriteString(hash, boundedText(item.Item.ExternalID, maxItemIDLength))
		_, _ = io.WriteString(hash, "\x00")
		_, _ = io.WriteString(hash, boundedText(item.LibraryID, maxLibraryIDLength))
		_, _ = io.WriteString(hash, "\x00")
		_, _ = io.WriteString(hash, boundedText(item.Item.Title, maxTitleLength))
		_, _ = io.WriteString(hash, "\x00")
		_, _ = io.WriteString(hash, strconv.FormatBool(item.Item.Playable))
		for _, provider := range item.ProviderRelationships {
			_, _ = io.WriteString(hash, "\x00"+provider.Provider+"="+provider.ID)
		}
		for _, source := range item.MediaSources {
			_, _ = io.WriteString(hash, "\x00"+source.ID+"="+source.Path)
			if source.MappedTarget != nil {
				_, _ = io.WriteString(hash, "@"+source.MappedTarget.RootID.String()+":"+source.MappedTarget.RelativePath)
			}
		}
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

func (client *Client) pageLimit(requested int) (int, error) {
	if requested <= 0 {
		return client.config.MaxPageSize, nil
	}
	if requested > client.config.MaxPageSize {
		return 0, invalidInput("jellyfin.inventory.limit")
	}
	return requested, nil
}

func (client *Client) get(ctx context.Context, operation, endpoint string, query url.Values) ([]byte, error) {
	body, status, err := client.request(ctx, operation, endpoint, http.MethodGet, query)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, normalizeStatus(operation, status)
	}
	return body, nil
}

func (client *Client) request(ctx context.Context, operation, endpoint, method string, query url.Values) ([]byte, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	requestURL := *client.endpoint
	requestURL.Path = strings.TrimRight(client.endpoint.Path, "/") + endpoint
	requestURL.RawPath = ""
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), nil)
	if err != nil {
		return nil, 0, errors.New("Jellyfin request could not be created")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "mastarr-jellyfin-read/0.0.1")
	if token := client.authToken(); token != "" {
		request.Header.Set("X-Emby-Token", token)
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
		return nil, errors.New("Jellyfin endpoint is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Jellyfin endpoint must be an absolute URL without credentials or query")
	}
	return parsed, nil
}

func validateConnectionScope(expected, requested domain.ConfigID) error {
	if !requested.Valid() || requested != expected {
		return invalidInput("jellyfin.connection")
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
	decoder := json.NewDecoder(strings.NewReader(string(data)))
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

func parseProviderID(value string) string { return boundedText(value, maxItemIDLength) }

func normalizeRemotePath(value string) string {
	if value == "" || len(value) > maxSourcePathLength || strings.TrimSpace(value) != value || strings.ContainsRune(value, 0) || strings.ContainsRune(value, '\\') || !absoluteRemotePath(value) {
		return ""
	}
	if len(value) == 3 && value[1] == ':' && value[2] == '/' {
		return value
	}
	clean := path.Clean(value)
	if clean != value || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(value, "//") {
		return ""
	}
	return clean
}

func normalizeMappingPrefix(value string) string {
	if value == "" || len(value) > maxSourcePathLength || strings.TrimSpace(value) != value || strings.ContainsRune(value, 0) || strings.ContainsRune(value, '\\') || !absoluteRemotePath(value) {
		return ""
	}
	if len(value) == 3 && value[1] == ':' && value[2] == '/' {
		return value
	}
	clean := path.Clean(value)
	if clean != value || strings.Contains(value, "//") {
		return ""
	}
	if len(clean) == 3 && clean[1] == ':' && clean[2] == '/' {
		return clean
	}
	if clean == "/" {
		return clean
	}
	return strings.TrimRight(clean, "/")
}

func absoluteRemotePath(value string) bool {
	return strings.HasPrefix(value, "/") || len(value) >= 3 && value[1] == ':' && value[2] == '/'
}

func pathBoundaryMatch(value, prefix string) bool {
	if prefix == "" {
		return false
	}
	if prefix == "/" {
		return strings.HasPrefix(value, "/")
	}
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func boundedText(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return value[:max]
	}
	return value
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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

func appendReasons(reasons []string, values ...string) []string {
	for _, value := range values {
		reasons = appendReason(reasons, value)
	}
	return reasons
}

func timePtr(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
