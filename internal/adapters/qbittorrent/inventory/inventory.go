// Package inventory implements the read-only qBittorrent WebUI inventory
// boundary. It deliberately contains no control methods: discovery must not
// stop, move, rename, retag or remove a torrent.
package inventory

import (
	"bytes"
	"context"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha1"
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
	"net/http/cookiejar"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	native "github.com/guilycst/mastarr/clients/qbittorrent"
	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

const (
	defaultMaxPageSize       = 200
	defaultMaxPages          = 100
	defaultMaxItems          = 1_000
	defaultMaxFilesPerItem   = 10_000
	defaultMaxResponseBytes  = 8 << 20
	defaultMaxDescriptorSize = 4 << 20
	maxVersionLength         = 128
	maxCursorIDLength        = 256
	maxCursorBytes           = 128 << 10
	maxEncodedCursorBytes    = 128 << 10
	maxCursorItems           = 1_000
	maxReasonCodes           = 256
)

const (
	apiWebAPIVersion = "/api/v2/app/webapiVersion"
	apiAppVersion    = "/api/v2/app/version"
	apiLogin         = "/api/v2/auth/login"
	apiTorrentInfo   = "/api/v2/torrents/info"
	apiProperties    = "/api/v2/torrents/properties"
	apiFiles         = "/api/v2/torrents/files"
	apiExport        = "/api/v2/torrents/export"
)

// DescriptorMode controls optional best-effort export of the original
// torrent. Exported bytes are never returned by List; only metadata and a
// digest are retained in DescriptorObservation.
type DescriptorMode string

const (
	DescriptorDisabled   DescriptorMode = "disabled"
	DescriptorBestEffort DescriptorMode = "best_effort"
)

// Config contains non-secret connection settings plus credentials supplied by
// the runtime configuration boundary. Username and Password are never put in
// an error, log or returned observation.
type Config struct {
	ConnectionID domain.ConfigID
	Endpoint     string
	Username     string
	Password     string

	HTTPClient *http.Client

	// Mappings translate qBittorrent's upstream absolute content namespace to
	// configured root-relative targets. An item remains visible when a mapping
	// is absent or ambiguous, but its Payload is empty and coverage is partial.
	Mappings []domain.PathMapping

	MaxPageSize       int
	MaxPages          int
	MaxItems          int
	MaxFilesPerItem   int
	MaxResponseBytes  int64
	MaxDescriptorSize int64
	DescriptorMode    DescriptorMode
}

// VersionObservation records the two read-only qBittorrent version probes.
// A non-nil error from Version means at least one probe was unavailable or
// malformed; successful fields remain useful evidence.
type VersionObservation struct {
	ConnectionID domain.ConfigID
	WebAPI       string
	Application  string
	ObservedAt   time.Time
}

// FileObservation preserves qBittorrent file metadata that the frozen common
// DownloadItem port does not have fields for. Path is the upstream content
// path after safe normalization and before configured mapping.
type FileObservation struct {
	Index        int
	Name         string
	Path         string
	Size         int64
	Progress     float64
	Priority     int
	Availability float64
	Seeds        int
	IsSeed       bool
}

// TorrentObservation is the detailed adapter result. Item is the common
// hexagonal-port value; the remaining fields preserve qBittorrent-specific
// evidence for callers that need to present a complete review.
type TorrentObservation struct {
	Item           ports.DownloadItem
	ScopedIdentity string
	ContentPath    string
	SavePath       string
	InfoHashV1     string
	InfoHashV2     string
	AddedAt        *time.Time
	Ratio          float64
	Seeds          int
	Leechers       int
	Files          []FileObservation
}

// DetailedPage is the adapter-specific page returned by ListDetailed.
type DetailedPage struct {
	Items      []TorrentObservation
	NextCursor string
	Coverage   domain.Coverage
	Version    VersionObservation
}

// Client is a read-only authenticated qBittorrent WebUI client.
type Client struct {
	config    Config
	endpoint  *url.URL
	http      *http.Client
	upstream  *native.Client
	cursorKey []byte

	authMu        sync.Mutex
	authenticated bool
}

// infoCapture carries the raw inventory response for the adapter-specific
// identity fields retained by the historical root contract. The standalone
// compatibility module deliberately exposes the upstream's frozen Torrent
// DTO, while this adapter still needs to preserve qBittorrent v1/v2 identity
// observations used to bind optional descriptor metadata. The native DTOs do
// not escape this package; only the local torrentSummary is returned.
type infoCapture struct {
	mu   sync.Mutex
	body []byte
}

type infoCaptureContextKey struct{}
type fileCaptureContextKey struct{}

type fileCapture struct {
	mu   sync.Mutex
	body []byte
}

// infoCaptureTransport tees bounded inventory bodies before the standalone
// client decodes them. It does not decode, rewrite or weaken the standalone
// strict schema; it only retains a bounded copy for local identity metadata.
type infoCaptureTransport struct {
	base     http.RoundTripper
	maxBytes int64
}

func (transport *infoCaptureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	base := transport.base
	if base == nil {
		base = http.DefaultTransport
	}
	response, err := base.RoundTrip(request)
	if err != nil || response == nil {
		return response, err
	}
	if !strings.HasSuffix(request.URL.Path, apiTorrentInfo) && !strings.HasSuffix(request.URL.Path, apiFiles) && !strings.HasSuffix(request.URL.Path, apiAppVersion) {
		return response, nil
	}
	if response.Body == nil {
		response.Body = io.NopCloser(strings.NewReader(""))
	}
	data, readErr := readBounded(response.Body, transport.maxBytes)
	_ = response.Body.Close()
	if strings.HasSuffix(request.URL.Path, apiTorrentInfo) {
		if capture, ok := request.Context().Value(infoCaptureContextKey{}).(*infoCapture); ok && capture != nil && readErr == nil {
			capture.mu.Lock()
			capture.body = append(capture.body[:0], data...)
			capture.mu.Unlock()
		}
		if readErr == nil {
			data = stripLegacyInventoryFields(data)
		}
	} else if strings.HasSuffix(request.URL.Path, apiFiles) {
		if capture, ok := request.Context().Value(fileCaptureContextKey{}).(*fileCapture); ok && capture != nil && readErr == nil {
			capture.mu.Lock()
			capture.body = append(capture.body[:0], data...)
			capture.mu.Unlock()
		}
		if readErr == nil {
			data = stripLegacyInventoryFields(data)
		}
	} else if readErr == nil && response.StatusCode == http.StatusOK {
		// qBittorrent commonly returns an application version without a
		// leading `v`; the standalone compatibility schema freezes the
		// stricter `vX.Y.Z` spelling. Prefix only a plain numeric token at
		// this adapter boundary, leaving malformed or decorated values for
		// the native validator to reject.
		trimmed := bytes.TrimSpace(data)
		if bytes.Equal(trimmed, data) && len(trimmed) > 0 && trimmed[0] >= '0' && trimmed[0] <= '9' {
			data = append([]byte("v"), trimmed...)
		}
	}
	if readErr != nil {
		data = nil
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}

func (capture *infoCapture) bytes() []byte {
	if capture == nil {
		return nil
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return append([]byte(nil), capture.body...)
}

func (capture *fileCapture) bytes() []byte {
	if capture == nil {
		return nil
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return append([]byte(nil), capture.body...)
}

// stripLegacyInventoryFields removes fields that belonged to the historical
// root adapter's synthetic identity envelope before the strict standalone
// DTO sees the response. Real qBittorrent responses do not define these
// fields. The original bounded bytes remain available through infoCapture so
// descriptor identity and metadata semantics are preserved locally.
func stripLegacyInventoryFields(data []byte) []byte {
	if !utf8.Valid(data) {
		return data
	}
	if !bytes.Contains(data, []byte(`"infohash_v1"`)) && !bytes.Contains(data, []byte(`"infohash_v2"`)) && !bytes.Contains(data, []byte(`"has_metadata"`)) && !bytes.Contains(data, []byte(`"seeds"`)) {
		return data
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var output bytes.Buffer
	if err := rewriteInventoryJSON(decoder, &output); err != nil {
		return data
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return data
	}
	return output.Bytes()
}

func rewriteInventoryJSON(decoder *json.Decoder, output *bytes.Buffer) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '[':
			output.WriteByte('[')
			first := true
			for decoder.More() {
				if !first {
					output.WriteByte(',')
				}
				if err := rewriteInventoryJSON(decoder, output); err != nil {
					return err
				}
				first = false
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("inventory JSON array is incomplete")
			}
			output.WriteByte(']')
			return nil
		case '{':
			output.WriteByte('{')
			first := true
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("inventory JSON key is invalid")
				}
				if legacyInventoryField(key) {
					if err := skipInventoryJSON(decoder); err != nil {
						return err
					}
					continue
				}
				if !first {
					output.WriteByte(',')
				}
				encodedKey, err := json.Marshal(key)
				if err != nil {
					return err
				}
				output.Write(encodedKey)
				output.WriteByte(':')
				if err := rewriteInventoryJSON(decoder, output); err != nil {
					return err
				}
				first = false
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return errors.New("inventory JSON object is incomplete")
			}
			output.WriteByte('}')
			return nil
		default:
			return errors.New("inventory JSON delimiter is invalid")
		}
	default:
		encoded, err := json.Marshal(token)
		if err != nil {
			return err
		}
		output.Write(encoded)
		return nil
	}
}

func skipInventoryJSON(decoder *json.Decoder) error {
	var discard json.RawMessage
	return decoder.Decode(&discard)
}

func legacyInventoryField(key string) bool {
	switch key {
	case "infohash_v1", "infohash_v2", "has_metadata", "seeds":
		return true
	default:
		return false
	}
}

var _ ports.DownloadInventoryPort = (*Client)(nil)
var _ ports.CapabilityPort = (*Client)(nil)

// New validates the connection boundary without making a network request.
func New(config Config) (*Client, error) {
	if !config.ConnectionID.Valid() {
		return nil, errors.New("qBittorrent connection id is invalid")
	}
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if config.MaxPageSize <= 0 {
		config.MaxPageSize = defaultMaxPageSize
	}
	if config.MaxPages <= 0 {
		config.MaxPages = defaultMaxPages
	}
	if config.MaxItems <= 0 {
		config.MaxItems = defaultMaxItems
	}
	if config.MaxFilesPerItem <= 0 {
		config.MaxFilesPerItem = defaultMaxFilesPerItem
	}
	if config.MaxPageSize > config.MaxItems {
		config.MaxPageSize = config.MaxItems
	}
	if config.MaxPageSize > maxCursorItems || config.MaxItems > maxCursorItems {
		return nil, errors.New("qBittorrent inventory bounds exceed cursor ceiling")
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = defaultMaxResponseBytes
	}
	if config.MaxDescriptorSize <= 0 {
		config.MaxDescriptorSize = defaultMaxDescriptorSize
	}
	if config.DescriptorMode == "" {
		config.DescriptorMode = DescriptorDisabled
	}
	if config.DescriptorMode != DescriptorDisabled && config.DescriptorMode != DescriptorBestEffort {
		return nil, errors.New("qBittorrent descriptor mode is unsupported")
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
	httpClient := &copyClient
	if httpClient.Timeout == 0 {
		httpClient.Timeout = 15 * time.Second
	}
	// A caller-provided http.Client can be safely reused for transport and
	// timeout policy, but its mutable cookie jar is connection state. Always
	// isolate that state per adapter instance so two qBittorrent connections
	// cannot exchange SID cookies.
	jar, jarErr := cookiejar.New(nil)
	if jarErr != nil {
		return nil, errors.New("qBittorrent session cookie setup failed")
	}
	httpClient.Jar = jar
	baseScheme, baseHost := endpoint.Scheme, endpoint.Host
	customRedirect := httpClient.CheckRedirect
	httpClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != baseScheme || !strings.EqualFold(request.URL.Host, baseHost) {
			return http.ErrUseLastResponse
		}
		if customRedirect != nil {
			return customRedirect(request, via)
		}
		return nil
	}
	cursorKey := make([]byte, 32)
	if _, err := cryptorand.Read(cursorKey); err != nil {
		return nil, errors.New("qBittorrent cursor key setup failed")
	}

	// The standalone compatibility client owns qBittorrent's authenticated
	// read session. Keep the adapter's descriptor client separate because
	// descriptor export is intentionally outside the frozen standalone read
	// surface; it remains best-effort metadata and never exposes bytes.
	upstreamHTTP := *httpClient
	upstreamHTTP.Jar = nil
	capture := &infoCaptureTransport{base: upstreamHTTP.Transport, maxBytes: config.MaxResponseBytes}
	upstreamHTTP.Transport = capture
	nativeUsername, nativePassword := config.Username, config.Password
	// The root adapter historically allowed credentials to be supplied by a
	// later runtime boundary (and its descriptor-only tests intentionally omit
	// them). The standalone client validates its own login form at construction
	// time, so use non-secret placeholders only for that compatibility boundary;
	// the server still decides whether the login succeeds.
	if nativeUsername == "" {
		nativeUsername = "unconfigured-user"
	}
	if nativePassword == "" {
		nativePassword = "unconfigured-password"
	}
	upstream, err := native.New(native.Config{
		Endpoint:         config.Endpoint,
		Username:         nativeUsername,
		Password:         nativePassword,
		HTTPClient:       &upstreamHTTP,
		MaxResponseBytes: config.MaxResponseBytes,
		MaxItems:         config.MaxItems,
		MaxFiles:         config.MaxFilesPerItem,
	})
	if err != nil {
		return nil, errors.New("qBittorrent compatibility client setup failed")
	}

	return &Client{config: config, endpoint: endpoint, http: httpClient, upstream: upstream, cursorKey: cursorKey}, nil
}

// NewClient is an explicit alias for callers that prefer constructor names
// that identify the returned object.
func NewClient(config Config) (*Client, error) { return New(config) }

// ConnectionID returns the stable connection scope used by every item.
func (client *Client) ConnectionID() domain.ConfigID { return client.config.ConnectionID }

// ScopedIdentity makes the connection scope explicit for persistence keys.
// The common port keeps ExternalID as the upstream hash; persistence must use
// this tuple-like value or the connection ID separately and never use a hash
// globally.
func (client *Client) ScopedIdentity(externalID string) string {
	if externalID == "" {
		return ""
	}
	return client.config.ConnectionID.String() + ":" + externalID
}

// List implements ports.DownloadInventoryPort. Use ListDetailed when callers
// need qBittorrent-specific file properties and version fields.
func (client *Client) List(ctx context.Context, connectionID domain.ConfigID, cursor string, limit int) (ports.Page[ports.DownloadItem], error) {
	detailed, err := client.ListDetailed(ctx, connectionID, cursor, limit)
	if err != nil {
		return ports.Page[ports.DownloadItem]{}, err
	}
	items := make([]ports.DownloadItem, 0, len(detailed.Items))
	for _, item := range detailed.Items {
		items = append(items, item.Item)
	}
	return ports.Page[ports.DownloadItem]{
		Items: detailedItems(items), NextCursor: detailed.NextCursor, Coverage: detailed.Coverage,
	}, nil
}

func detailedItems(items []ports.DownloadItem) []ports.DownloadItem {
	if len(items) == 0 {
		return nil
	}
	return items
}

// ListDetailed reads a bounded qBittorrent page and all bounded properties and
// file metadata for each returned torrent. Pagination is offset-based because
// that is qBittorrent's supported read API; cursor state carries a source ID,
// cumulative count, previous page identities and a fingerprint so overlapping
// or repeated upstream pages terminate as partial rather than looping.
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
		if requestedLimit > 0 && limit != state.PageSize {
			return result, invalidInput("qbit.inventory.cursor")
		}
		limit = state.PageSize
	}
	if state.ObservedCount >= int64(client.config.MaxItems) {
		return result, invalidInput("qbit.inventory.cursor")
	}
	if state.SourceID == "" {
		state.SourceID, err = domain.NewRuntimeID()
		if err != nil {
			return result, errors.New("qBittorrent inventory source identity unavailable")
		}
		state.StartedAt = time.Now().UTC()
		state.PageSize = limit
	}
	fetchLimit := limit
	if remaining := int64(client.config.MaxItems) - state.ObservedCount; remaining < int64(fetchLimit) {
		fetchLimit = int(remaining)
	}

	version, versionErr := client.Version(ctx, connectionID)
	if versionErr != nil {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if code, ok := upstreamCode(versionErr); ok && code == domain.OutcomeUnauthorized {
			return result, versionErr
		}
		addReason(&result.Coverage.ReasonCodes, "version_observation_partial")
	}
	result.Version = version

	capture := &infoCapture{}
	nativeContext := context.WithValue(ctx, infoCaptureContextKey{}, capture)
	nativePage, err := client.upstream.ListTorrents(nativeContext, native.TorrentListOptions{
		Limit: fetchLimit, Offset: state.Offset, Sort: "hash",
	})
	if err != nil {
		return result, translateNativeError("qbit.inventory.list", err)
	}
	summaries := mergeNativeSummaries(nativePage, capture.bytes())
	if summaries == nil && len(nativePage) > 0 {
		return result, upstreamMalformed("qbit.inventory.list")
	}
	pageFingerprint := fingerprint(summaries)
	responseOverflow := len(summaries) > fetchLimit
	if responseOverflow {
		summaries = summaries[:fetchLimit]
	}

	result.Coverage = domain.Coverage{
		SourceID: state.SourceID, ConnectionID: connectionID,
		Completeness: domain.CompletenessComplete, ObservedCount: state.ObservedCount,
		SnapshotRevision: versionRevision(version), StartedAt: timePtr(state.StartedAt),
		ObservedAt: time.Now().UTC(),
	}
	if state.PriorPartial {
		// The cursor deliberately carries only a bounded aggregate marker. The
		// exact reasons belong to the page that observed them; callers can keep
		// those page results while this marker prevents a later page from
		// promoting the overall scan back to complete.
		addReason(&result.Coverage.ReasonCodes, "pagination_prior_partial")
	}
	if versionErr != nil {
		addReason(&result.Coverage.ReasonCodes, "version_observation_partial")
	}
	if responseOverflow {
		addReason(&result.Coverage.ReasonCodes, "pagination_response_exceeded_limit")
	}

	repeatedPage := false
	for _, previousFingerprint := range state.PageFingerprints {
		if pageFingerprint == previousFingerprint && len(summaries) > 0 {
			repeatedPage = true
			break
		}
	}
	if repeatedPage {
		addReason(&result.Coverage.ReasonCodes, "pagination_stalled")
		summaries = nil
	}

	seen := make(map[string]struct{}, len(state.SeenIDs))
	for _, id := range state.SeenIDs {
		seen[id] = struct{}{}
	}
	current := make(map[string]struct{}, len(summaries))
	items := make([]TorrentObservation, 0, len(summaries))
	for index, summary := range summaries {
		if err := ctx.Err(); err != nil {
			return DetailedPage{}, err
		}
		id := summary.externalID()
		if id != "" {
			if _, exists := seen[id]; exists {
				addReason(&result.Coverage.ReasonCodes, "pagination_overlap")
				continue
			}
			if _, exists := current[id]; exists {
				addReason(&result.Coverage.ReasonCodes, "pagination_duplicate")
				continue
			}
			current[id] = struct{}{}
		}
		item, itemReasons, itemErr := client.observeTorrent(ctx, connectionID, summary)
		if itemErr != nil {
			return DetailedPage{}, itemErr
		}
		for _, reason := range itemReasons {
			addReason(&result.Coverage.ReasonCodes, fmt.Sprintf("item_%d_%s", index, reason))
		}
		items = append(items, item)
		if id != "" {
			seen[id] = struct{}{}
			state.SeenIDs = append(state.SeenIDs, id)
		}
	}
	result.Items = items
	result.Coverage.ObservedCount += int64(len(items))

	pageCount := state.PageCount + 1
	offsetOverflow := state.Offset > int(^uint(0)>>1)-len(summaries)
	if offsetOverflow {
		addReason(&result.Coverage.ReasonCodes, "pagination_offset_overflow")
	}
	if !offsetOverflow {
		state.Offset += len(summaries)
	}
	state.PageCount = pageCount
	state.ObservedCount = result.Coverage.ObservedCount
	if !repeatedPage && len(summaries) > 0 {
		state.PageFingerprints = append(state.PageFingerprints, pageFingerprint)
	}

	allDuplicate := len(summaries) > 0 && len(items) == 0
	stop := len(summaries) == 0 || len(summaries) < fetchLimit || responseOverflow || offsetOverflow || allDuplicate
	if allDuplicate {
		addReason(&result.Coverage.ReasonCodes, "pagination_stalled")
	}
	hardStop := responseOverflow || offsetOverflow || allDuplicate || repeatedPage
	if len(summaries) >= fetchLimit && !stop {
		switch {
		case pageCount >= client.config.MaxPages:
			stop = true
			hardStop = true
			addReason(&result.Coverage.ReasonCodes, "pagination_limit")
		case state.ObservedCount >= int64(client.config.MaxItems):
			stop = true
			hardStop = true
			addReason(&result.Coverage.ReasonCodes, "pagination_limit")
		}
	}
	if stop && !hardStop && state.PageCount > 1 {
		stable, reason, validationErr := client.validateSnapshot(ctx, state)
		if validationErr != nil {
			return DetailedPage{}, validationErr
		}
		if !stable {
			addReason(&result.Coverage.ReasonCodes, reason)
		}
	}
	if len(result.Coverage.ReasonCodes) > 0 {
		result.Coverage.Completeness = domain.CompletenessPartial
		state.PriorPartial = true
	}
	if !stop {
		var encodeErr error
		result.NextCursor, encodeErr = client.encodeCursorChecked(state)
		if encodeErr != nil {
			result.NextCursor = ""
			stop = true
			addReason(&result.Coverage.ReasonCodes, "pagination_cursor_limit")
		}
	}
	if !stop {
		result.Coverage.Completeness = domain.CompletenessPartial
		addReason(&result.Coverage.ReasonCodes, "pagination_continues")
	} else {
		completed := time.Now().UTC()
		result.Coverage.CompletedAt = &completed
		result.Coverage.ObservedAt = completed
		if len(result.Coverage.ReasonCodes) > 0 {
			result.Coverage.Completeness = domain.CompletenessPartial
		}
	}
	return result, nil
}

// validateSnapshot performs a bounded second, summary-only traversal before a
// multi-page scan can claim complete coverage. qBittorrent has no snapshot
// token, so page membership, identities and observed count must all match the
// original pass. A mismatch remains partial evidence rather than an absence
// claim.
func (client *Client) validateSnapshot(ctx context.Context, state inventoryCursor) (bool, string, error) {
	if state.PageSize <= 0 {
		return false, "pagination_revalidation_unavailable", nil
	}
	ids := make(map[string]struct{}, len(state.SeenIDs))
	fingerprints := make([]string, 0, len(state.PageFingerprints))
	var observed int64
	offset := 0
	for page := 0; page < client.config.MaxPages; page++ {
		capture := &infoCapture{}
		nativeContext := context.WithValue(ctx, infoCaptureContextKey{}, capture)
		nativePage, err := client.upstream.ListTorrents(nativeContext, native.TorrentListOptions{
			Limit: state.PageSize, Offset: offset, Sort: "hash",
		})
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return false, "", ctxErr
			}
			return false, "pagination_revalidation_unavailable", nil
		}
		summaries := mergeNativeSummaries(nativePage, capture.bytes())
		if len(summaries) > state.PageSize {
			return false, "pagination_revalidation_overflow", nil
		}
		if len(summaries) > 0 {
			fingerprints = append(fingerprints, fingerprint(summaries))
		}
		for _, summary := range summaries {
			observed++
			id := summary.externalID()
			if id == "" {
				continue
			}
			if _, exists := ids[id]; exists {
				// A duplicate during the validation pass proves that the
				// mutable upstream inventory changed or cannot be treated as a
				// stable snapshot. Keep the public reason at the snapshot
				// boundary rather than claiming a complete scan.
				return false, "pagination_snapshot_changed", nil
			}
			ids[id] = struct{}{}
		}
		if len(summaries) < state.PageSize {
			if observed != state.ObservedCount || !sameIdentities(ids, state.SeenIDs) || !sameStrings(fingerprints, state.PageFingerprints) {
				return false, "pagination_snapshot_changed", nil
			}
			return true, "", nil
		}
		if observed >= int64(client.config.MaxItems) {
			return false, "pagination_revalidation_limit", nil
		}
		if offset > int(^uint(0)>>1)-len(summaries) {
			return false, "pagination_revalidation_overflow", nil
		}
		offset += len(summaries)
	}
	return false, "pagination_revalidation_limit", nil
}

func sameIdentities(actual map[string]struct{}, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for _, id := range expected {
		if _, exists := actual[id]; !exists {
			return false
		}
	}
	return true
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// Version performs authenticated, read-only qBittorrent version probes.
func (client *Client) Version(ctx context.Context, connectionID domain.ConfigID) (VersionObservation, error) {
	result := VersionObservation{ConnectionID: connectionID, ObservedAt: time.Now().UTC()}
	if err := validateConnectionScope(client.config.ConnectionID, connectionID); err != nil {
		return result, err
	}
	var firstErr error
	application, applicationErr := client.upstream.ApplicationVersion(ctx)
	if applicationErr == nil {
		result.Application = strings.TrimPrefix(application, "v")
	} else {
		firstErr = translateNativeError("qbit.version", applicationErr)
	}
	webAPI, webAPIErr := client.upstream.WebAPIVersion(ctx)
	if webAPIErr == nil {
		result.WebAPI = webAPI
	} else if firstErr == nil {
		firstErr = translateNativeError("qbit.version", webAPIErr)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, ctxErr
	}
	if firstErr != nil {
		if code, ok := upstreamCode(firstErr); ok && code == domain.OutcomeUnauthorized {
			return result, firstErr
		}
		return result, firstErr
	}
	return result, nil
}

// Capabilities exposes version and endpoint capability evidence without
// enabling any mutation. Descriptor export stays unknown unless explicitly
// requested for best-effort probing per item.
func (client *Client) Capabilities(ctx context.Context, connectionID domain.ConfigID) ([]domain.Capability, error) {
	version, versionErr := client.Version(ctx, connectionID)
	if versionErr != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if version.WebAPI == "" && version.Application == "" {
			return nil, versionErr
		}
	}
	now := time.Now().UTC()
	versionValue := version.Application
	if versionValue == "" {
		versionValue = version.WebAPI
	}
	state := domain.CapabilityUnknown
	reason := "version observation is unavailable"
	if version.WebAPI != "" {
		state, reason = domain.CapabilitySupported, ""
	}
	capabilities := []domain.Capability{
		{Name: "qbt.webapi_version", State: versionState(version.WebAPI), Version: version.WebAPI, Reason: versionReason(version.WebAPI), ObservedAt: now},
		{Name: "qbt.application_version", State: versionState(version.Application), Version: version.Application, Reason: versionReason(version.Application), ObservedAt: now},
		{Name: "qbt.inventory", State: state, Version: versionValue, Reason: reason, ObservedAt: now},
		{Name: "qbt.torrent_properties", State: state, Version: versionValue, Reason: reason, ObservedAt: now},
		{Name: "qbt.torrent_files", State: state, Version: versionValue, Reason: reason, ObservedAt: now},
	}
	descriptorState := domain.CapabilityUnknown
	descriptorReason := "descriptor export is not requested"
	if client.config.DescriptorMode == DescriptorBestEffort {
		descriptorReason = "descriptor export is probed per torrent"
	}
	capabilities = append(capabilities, domain.Capability{
		Name: "qbt.descriptor_export", State: descriptorState, Version: versionValue,
		Reason: descriptorReason, ObservedAt: now,
	})
	return capabilities, nil
}

func (client *Client) observeTorrent(ctx context.Context, connectionID domain.ConfigID, summary torrentSummary) (TorrentObservation, []string, error) {
	observedAt := time.Now().UTC()
	id := summary.externalID()
	item := ports.DownloadItem{
		ExternalID: id,
		Name:       summary.Name,
		Protocol:   "torrent",
		State:      summary.State,
		Progress:   summary.Progress,
		Seeding:    isSeedingState(summary.State),
		Category:   summary.Category,
		Tags:       splitTags(summary.Tags),
		Hash:       id,
	}
	var reasons []string
	if id == "" {
		reasons = append(reasons, "identity_unknown")
	}
	if summary.Name == "" {
		reasons = append(reasons, "name_unknown")
	}
	if !knownTorrentState(summary.State) {
		reasons = append(reasons, "state_unknown")
	} else if summary.Progress >= 1 && isTerminalState(summary.State) {
		item.ProcessingDone = true
	} else if summary.Progress >= 1 {
		reasons = append(reasons, "processing_state_nonterminal")
	}
	if !validFraction(summary.Progress) {
		item.Progress = 0
		reasons = append(reasons, "progress_unknown")
	}
	if !validFractionOrRatio(summary.Ratio) {
		reasons = append(reasons, "ratio_unknown")
	}

	result := TorrentObservation{
		Item: item, ScopedIdentity: client.ScopedIdentity(id),
		ContentPath: normalizeRemotePath(summary.ContentPath),
		SavePath:    normalizeRemotePath(summary.SavePath),
		InfoHashV1:  normalizeHash(summary.InfoHashV1, 40),
		InfoHashV2:  normalizeHash(summary.InfoHashV2, 64),
		Ratio:       normalizedRatio(summary.Ratio), Seeds: summary.NumSeeds, Leechers: summary.NumLeechs,
	}
	result.Item.CompletedAt, reasons = mergeTimestamp(summary.CompletionOn, result.Item.CompletedAt, reasons, "completion_time")
	result.AddedAt, reasons = timestamp(summary.AddedOn, reasons, "added_time")

	if id == "" {
		reasons = append(reasons, "properties_unavailable")
	} else if properties, err := client.upstream.GetTorrentProperties(ctx, id); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return TorrentObservation{}, nil, ctxErr
		}
		reasons = append(reasons, "properties_unavailable")
	} else {
		if properties.SavePath != "" {
			result.SavePath = normalizeRemotePath(properties.SavePath)
		}
		if validFractionOrRatio(properties.ShareRatio) {
			result.Ratio = properties.ShareRatio
		}
		if properties.CompletionDate > 0 && result.Item.CompletedAt == nil {
			value, valid := timestampValue(properties.CompletionDate)
			if valid {
				result.Item.CompletedAt = value
			} else {
				reasons = append(reasons, "completion_time_unknown")
			}
		}
		if result.AddedAt == nil && properties.AdditionDate > 0 {
			value, valid := timestampValue(properties.AdditionDate)
			if valid {
				result.AddedAt = value
			}
		}
	}

	var files []torrentFile
	if id == "" {
		reasons = append(reasons, "files_unavailable")
	} else {
		capture := &fileCapture{}
		nativeContext := context.WithValue(ctx, fileCaptureContextKey{}, capture)
		nativeFiles, err := client.upstream.GetTorrentFiles(nativeContext, id)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return TorrentObservation{}, nil, ctxErr
			}
			if native.IsCode(err, native.ErrorUnknown) {
				reasons = append(reasons, "files_malformed")
			} else {
				reasons = append(reasons, "files_unavailable")
			}
		} else {
			legacyFiles := decodeLegacyFiles(capture.bytes())
			files = make([]torrentFile, len(nativeFiles))
			for index, file := range nativeFiles {
				seeds := -1
				if index < len(legacyFiles) && legacyFiles[index].Seeds != nil {
					seeds = *legacyFiles[index].Seeds
				}
				files[index] = torrentFile{
					Index: int(file.Index), Name: file.Name, Size: file.Size,
					Progress: file.Progress, Priority: int(file.Priority),
					Availability: file.Availability, Seeds: seeds, IsSeed: file.IsSeed,
				}
			}
			mapped, fileReasons := client.mapFiles(summary.ContentPath, files, observedAt)
			result.Files = make([]FileObservation, 0, len(mapped))
			reasons = append(reasons, fileReasons...)
			result.Item.Payload = make([]domain.FileManifestEntry, 0, len(mapped))
			for _, file := range mapped {
				result.Files = append(result.Files, file.Observation)
				if file.Entry.RootID != "" {
					result.Item.Payload = append(result.Item.Payload, file.Entry)
				}
			}
		}
	}

	result.Item.Descriptor, reasons = client.descriptor(ctx, summary, reasons)
	if err := ctx.Err(); err != nil {
		return TorrentObservation{}, nil, err
	}
	if len(result.Item.Payload) == 0 && len(files) > 0 {
		reasons = append(reasons, "payload_mapping_unavailable")
	}
	return result, uniqueReasons(reasons), nil
}

type mappedFile struct {
	Observation FileObservation
	Entry       domain.FileManifestEntry
}

func (client *Client) mapFiles(contentPath string, files []torrentFile, observedAt time.Time) ([]mappedFile, []string) {
	result := make([]mappedFile, 0, len(files))
	var reasons []string
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		remotePath, valid := remoteFilePath(contentPath, file.Name, len(files))
		if !valid || remotePath == "" {
			reasons = append(reasons, "file_path_unknown")
			continue
		}
		if file.Index < 0 || file.Size < 0 || !validFraction(file.Progress) || !validAvailability(file.Availability) || file.Seeds < -1 {
			reasons = append(reasons, "file_metadata_unknown")
			continue
		}
		if file.Availability == -1 {
			reasons = append(reasons, "file_availability_unknown")
		}
		if file.Seeds == -1 {
			reasons = append(reasons, "file_seeds_unknown")
		}
		observation := FileObservation{
			Index: file.Index, Name: file.Name, Path: remotePath, Size: file.Size,
			Progress: file.Progress, Priority: file.Priority, Availability: file.Availability,
			Seeds: file.Seeds, IsSeed: file.IsSeed,
		}
		entry := domain.FileManifestEntry{
			Size: file.Size, Type: manifestType(file.Name), Role: manifestRole(file.Name), ObservedAt: observedAt,
		}
		mapped, ok, ambiguous := client.mapPath(remotePath)
		if ambiguous {
			reasons = append(reasons, "payload_mapping_ambiguous")
			result = append(result, mappedFile{Observation: observation})
			continue
		}
		if !ok {
			reasons = append(reasons, "payload_unmapped")
			result = append(result, mappedFile{Observation: observation})
			continue
		}
		entry.RootID, entry.RelativePath = mapped.RootID, mapped.RelativePath
		key := entry.RootID.String() + ":" + entry.RelativePath
		if _, exists := seen[key]; exists {
			reasons = append(reasons, "payload_duplicate")
			result = append(result, mappedFile{Observation: observation})
			continue
		}
		seen[key] = struct{}{}
		result = append(result, mappedFile{Observation: observation, Entry: entry})
	}
	return result, uniqueReasons(reasons)
}

func (client *Client) descriptor(ctx context.Context, summary torrentSummary, reasons []string) (*ports.DescriptorObservation, []string) {
	descriptor := &ports.DescriptorObservation{Kind: "torrent", Available: false, Unavailable: "not_requested"}
	if client.config.DescriptorMode != DescriptorBestEffort {
		return descriptor, reasons
	}
	id := summary.externalID()
	if id == "" {
		descriptor.Unavailable = "identity_unavailable"
		return descriptor, append(reasons, "descriptor_unavailable")
	}
	if !summary.HasMetadata {
		descriptor.Unavailable = "metadata_unavailable"
		return descriptor, append(reasons, "descriptor_unavailable")
	}
	body, err := client.get(ctx, "qbit.inventory.descriptor", apiExport, url.Values{"hash": []string{id}}, client.config.MaxDescriptorSize)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return descriptor, append(reasons, "descriptor_unavailable")
		}
		if code, ok := upstreamCode(err); ok && code == domain.OutcomeUnsupported {
			descriptor.Unavailable = "export_unsupported"
		} else {
			descriptor.Unavailable = "export_unavailable"
		}
		return descriptor, append(reasons, "descriptor_unavailable")
	}
	parsed, valid := parseTorrentDescriptor(body)
	if !valid {
		descriptor.Unavailable = "export_malformed"
		return descriptor, append(reasons, "descriptor_unavailable")
	}
	if !descriptorIdentityMatches(parsed.info, descriptorIdentityHashes(summary)) {
		descriptor.Unavailable = "export_identity_mismatch"
		return descriptor, append(reasons, "descriptor_unavailable")
	}
	identifier, idErr := domain.NewRuntimeID()
	if idErr != nil {
		descriptor.Unavailable = "descriptor_identity_unavailable"
		return descriptor, append(reasons, "descriptor_unavailable")
	}
	digest := sha256.Sum256(body)
	captured := time.Now().UTC()
	*descriptor = ports.DescriptorObservation{
		ID: identifier, Kind: "torrent", Available: true, Size: int64(len(body)),
		Digest: "sha256:" + hex.EncodeToString(digest[:]), CapturedAt: &captured,
		Source: "qbittorrent.export",
	}
	return descriptor, reasons
}

func (client *Client) mapPath(remote string) (domain.FileTarget, bool, bool) {
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
			return errors.New("qBittorrent path mapping is invalid")
		}
		if mapping.DestinationPrefix != "" {
			if err := domain.ValidateRelativePath(strings.Trim(mapping.DestinationPrefix, "/")); err != nil {
				return errors.New("qBittorrent path mapping destination is invalid")
			}
		}
	}
	return nil
}

func (client *Client) pageLimit(requested int) (int, error) {
	if requested <= 0 {
		requested = client.config.MaxPageSize
	}
	if requested > client.config.MaxPageSize {
		return 0, invalidInput("qbit.inventory.limit")
	}
	return requested, nil
}

func (client *Client) decodeCursor(value string) (inventoryCursor, error) {
	if value == "" {
		return inventoryCursor{}, nil
	}
	if len(value) > maxEncodedCursorBytes || strings.Count(value, ".") != 1 {
		return inventoryCursor{}, invalidInput("qbit.inventory.cursor")
	}
	payloadValue, signatureValue, ok := strings.Cut(value, ".")
	if !ok || payloadValue == "" || signatureValue == "" || len(client.cursorKey) == 0 {
		return inventoryCursor{}, invalidInput("qbit.inventory.cursor")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(payloadValue)
	if err != nil || len(decoded) == 0 || len(decoded) > maxCursorBytes {
		return inventoryCursor{}, invalidInput("qbit.inventory.cursor")
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != payloadValue {
		return inventoryCursor{}, invalidInput("qbit.inventory.cursor")
	}
	signature, err := base64.RawURLEncoding.DecodeString(signatureValue)
	if err != nil || len(signature) != sha256.Size {
		return inventoryCursor{}, invalidInput("qbit.inventory.cursor")
	}
	if base64.RawURLEncoding.EncodeToString(signature) != signatureValue {
		return inventoryCursor{}, invalidInput("qbit.inventory.cursor")
	}
	mac := hmac.New(sha256.New, client.cursorKey)
	_, _ = mac.Write(decoded)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return inventoryCursor{}, invalidInput("qbit.inventory.cursor")
	}
	var state inventoryCursor
	if err := json.Unmarshal(decoded, &state); err != nil || state.Version != 1 || state.ConnectionID != client.config.ConnectionID.String() || !state.SourceID.Valid() || state.StartedAt.IsZero() || state.Offset <= 0 || state.PageCount <= 0 || state.PageCount >= client.config.MaxPages || state.PageSize <= 0 || state.PageSize > client.config.MaxPageSize || state.ObservedCount < 0 || state.ObservedCount > int64(client.config.MaxItems) || len(state.SeenIDs) > client.config.MaxItems || len(state.PageFingerprints) == 0 || len(state.PageFingerprints) != state.PageCount {
		return inventoryCursor{}, invalidInput("qbit.inventory.cursor")
	}
	for _, id := range state.SeenIDs {
		if len(id) > maxCursorIDLength || normalizeHash(id, 0) != id {
			return inventoryCursor{}, invalidInput("qbit.inventory.cursor")
		}
	}
	for _, pageFingerprint := range state.PageFingerprints {
		if len(pageFingerprint) != sha256.Size*2 || normalizeHex(pageFingerprint) != pageFingerprint {
			return inventoryCursor{}, invalidInput("qbit.inventory.cursor")
		}
	}
	return state, nil
}

func (client *Client) encodeCursor(state inventoryCursor) string {
	value, _ := client.encodeCursorChecked(state)
	return value
}

func (client *Client) encodeCursorChecked(state inventoryCursor) (string, error) {
	if len(client.cursorKey) == 0 || state.PageSize <= 0 || state.PageSize > maxCursorItems || state.PageCount <= 0 || state.Offset <= 0 || len(state.SeenIDs) > maxCursorItems || len(state.PageFingerprints) != state.PageCount {
		return "", cursorEncodingError()
	}
	state.Version = 1
	state.ConnectionID = client.config.ConnectionID.String()
	encoded, err := json.Marshal(state)
	if err != nil || len(encoded) > maxCursorBytes {
		return "", cursorEncodingError()
	}
	mac := hmac.New(sha256.New, client.cursorKey)
	_, _ = mac.Write(encoded)
	payload := base64.RawURLEncoding.EncodeToString(encoded)
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(payload)+1+len(signature) > maxEncodedCursorBytes {
		return "", cursorEncodingError()
	}
	return payload + "." + signature, nil
}

func cursorEncodingError() error {
	return domain.UpstreamError{Code: domain.OutcomeUnknown, Operation: "qbit.inventory.cursor", Detail: "continuation cursor exceeds its configured bound"}
}

func (client *Client) getJSONFor(ctx context.Context, operation, endpoint string, query url.Values, target any) error {
	body, err := client.get(ctx, operation, endpoint, query, client.config.MaxResponseBytes)
	if err != nil {
		return err
	}
	if err := decodeJSON(body, target); err != nil {
		return upstreamMalformed(operation)
	}
	return nil
}

func (client *Client) getJSON(ctx context.Context, operation, endpoint string, query url.Values, maxBytes int64) ([]byte, error) {
	return client.get(ctx, operation, endpoint, query, maxBytes)
}

func (client *Client) get(ctx context.Context, operation, endpoint string, query url.Values, maxBytes int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := client.ensureSession(ctx); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		body, status, err := client.requestOnce(ctx, operation, http.MethodGet, endpoint, query, nil, "", maxBytes)
		if err != nil {
			return nil, err
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			if attempt == 0 {
				client.invalidateSession()
				if err := client.ensureSession(ctx); err != nil {
					return nil, err
				}
				continue
			}
			return nil, normalizeStatus(operation, status)
		}
		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			return nil, normalizeStatus(operation, status)
		}
		return body, nil
	}
	return nil, normalizeStatus(operation, http.StatusUnauthorized)
}

func (client *Client) ensureSession(ctx context.Context) error {
	client.authMu.Lock()
	defer client.authMu.Unlock()
	if client.authenticated {
		return nil
	}
	form := url.Values{}
	form.Set("username", client.config.Username)
	form.Set("password", client.config.Password)
	body, status, err := client.requestOnce(ctx, "qbit.auth.login", http.MethodPost, apiLogin, nil, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", 16<<10)
	if err != nil {
		return err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return normalizeStatus("qbit.auth.login", status)
	}
	if !bytes.Equal(bytes.TrimSpace(body), []byte("Ok.")) {
		return domain.UpstreamError{Code: domain.OutcomeUnauthorized, Status: status, Operation: "qbit.auth.login", Detail: "authentication failed"}
	}
	client.authenticated = true
	return nil
}

func (client *Client) invalidateSession() {
	client.authMu.Lock()
	client.authenticated = false
	client.authMu.Unlock()
}

func (client *Client) requestOnce(ctx context.Context, operation, method, endpoint string, query url.Values, body io.Reader, contentType string, maxBytes int64) ([]byte, int, error) {
	requestURL := *client.endpoint
	requestURL.Path = strings.TrimRight(client.endpoint.Path, "/") + endpoint
	requestURL.RawPath = ""
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return nil, 0, errors.New("qBittorrent request could not be created")
	}
	request.Header.Set("Accept", "application/json, text/plain")
	request.Header.Set("User-Agent", "mastarr-qbittorrent-inventory/0.0.1")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
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
	data, readErr := readBounded(response.Body, maxBytes)
	if readErr != nil {
		return nil, response.StatusCode, domain.UpstreamError{Code: domain.OutcomeUnknown, Retryable: false, Status: response.StatusCode, Operation: operation, Detail: readErr.Error()}
	}
	return data, response.StatusCode, nil
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxResponseBytes
	}
	if maxBytes == math.MaxInt64 {
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
	if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("empty JSON response")
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

type torrentDescriptor struct {
	// info is the exact byte span of the bencoded info value. Torrent v1 and
	// v2 identities are hashes of this raw span, including its original key
	// ordering and encoding.
	info []byte
}

// parseTorrentDescriptor performs a bounded structural check of the bencode
// returned by qBittorrent's export endpoint and retains the exact raw info
// value span for identity binding. It intentionally does not decode metadata
// or expose descriptor bytes to callers.
func parseTorrentDescriptor(data []byte) (torrentDescriptor, bool) {
	var descriptor torrentDescriptor
	if len(data) == 0 || data[0] != 'd' {
		return descriptor, false
	}
	parser := bencodeParser{data: data}
	parser.offset++ // top-level dictionary marker
	infoCount := 0
	for parser.offset < len(parser.data) && parser.data[parser.offset] != 'e' {
		key, ok := parser.bytesValue()
		if !ok {
			return descriptor, false
		}
		valueStart := parser.offset
		if !parser.value(1) {
			return descriptor, false
		}
		if bytes.Equal(key, []byte("info")) {
			infoCount++
			if infoCount > 1 || valueStart >= len(parser.data) || parser.data[valueStart] != 'd' || !validDictionary(parser.data[valueStart:parser.offset]) {
				return descriptor, false
			}
			descriptor.info = parser.data[valueStart:parser.offset]
		}
	}
	if !parser.consumeEnd() || parser.offset != len(data) {
		return descriptor, false
	}
	return descriptor, infoCount == 1
}

// validTorrentDescriptor keeps the structural validator available to tests
// and callers that only need a format check.
func validTorrentDescriptor(data []byte) bool {
	_, valid := parseTorrentDescriptor(data)
	return valid
}

func descriptorIdentityHashes(summary torrentSummary) []string {
	values := []string{
		normalizeHash(summary.Hash, 0),
		normalizeHash(summary.InfoHashV1, 40),
		normalizeHash(summary.InfoHashV2, 64),
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		duplicate := false
		for _, existing := range result {
			if existing == value {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result = append(result, value)
		}
	}
	return result
}

func descriptorIdentityMatches(info []byte, supported []string) bool {
	if len(info) == 0 {
		return false
	}
	v1 := sha1.Sum(info)
	v2 := sha256.Sum256(info)
	v1Hex := hex.EncodeToString(v1[:])
	v2Hex := hex.EncodeToString(v2[:])
	for _, value := range supported {
		switch len(value) {
		case sha1.Size * 2:
			if value == v1Hex {
				return true
			}
		case sha256.Size * 2:
			if value == v2Hex {
				return true
			}
		}
	}
	return false
}

func validDictionary(data []byte) bool {
	if len(data) == 0 || data[0] != 'd' {
		return false
	}
	parser := bencodeParser{data: data}
	parser.offset++
	entries := 0
	for parser.offset < len(parser.data) && parser.data[parser.offset] != 'e' {
		if !parser.bytes() || !parser.value(1) {
			return false
		}
		entries++
	}
	return entries > 0 && parser.consumeEnd() && parser.offset == len(data)
}

type bencodeParser struct {
	data   []byte
	offset int
}

func (parser *bencodeParser) value(depth int) bool {
	if depth > 64 || parser.offset >= len(parser.data) {
		return false
	}
	switch parser.data[parser.offset] {
	case 'i':
		return parser.integer()
	case 'l':
		parser.offset++
		for parser.offset < len(parser.data) && parser.data[parser.offset] != 'e' {
			if !parser.value(depth + 1) {
				return false
			}
		}
		return parser.consumeEnd()
	case 'd':
		parser.offset++
		for parser.offset < len(parser.data) && parser.data[parser.offset] != 'e' {
			if !parser.bytes() || !parser.value(depth+1) {
				return false
			}
		}
		return parser.consumeEnd()
	default:
		return parser.bytes()
	}
}

func (parser *bencodeParser) integer() bool {
	parser.offset++ // i
	if parser.offset >= len(parser.data) {
		return false
	}
	digitsStart := parser.offset
	if parser.data[parser.offset] == '-' {
		parser.offset++
		digitsStart = parser.offset
		if parser.offset >= len(parser.data) || parser.data[parser.offset] == '0' {
			return false
		}
	}
	if parser.offset >= len(parser.data) || parser.data[parser.offset] == 'e' {
		return false
	}
	for parser.offset < len(parser.data) && parser.data[parser.offset] >= '0' && parser.data[parser.offset] <= '9' {
		parser.offset++
	}
	if parser.offset == digitsStart || (parser.offset-digitsStart > 1 && parser.data[digitsStart] == '0') {
		return false
	}
	return parser.consumeEnd()
}

func (parser *bencodeParser) bytes() bool {
	_, ok := parser.bytesValue()
	return ok
}

func (parser *bencodeParser) bytesValue() ([]byte, bool) {
	start := parser.offset
	for parser.offset < len(parser.data) && parser.data[parser.offset] >= '0' && parser.data[parser.offset] <= '9' {
		parser.offset++
	}
	if parser.offset == start || (parser.offset-start > 1 && parser.data[start] == '0') || parser.offset >= len(parser.data) || parser.data[parser.offset] != ':' {
		return nil, false
	}
	length := 0
	for index := start; index < parser.offset; index++ {
		digit := int(parser.data[index] - '0')
		if length > (len(parser.data)-digit)/10 {
			return nil, false
		}
		length = length*10 + digit
	}
	parser.offset++ // colon
	if length > len(parser.data)-parser.offset {
		return nil, false
	}
	value := parser.data[parser.offset : parser.offset+length]
	parser.offset += length
	return value, true
}

func (parser *bencodeParser) consumeEnd() bool {
	if parser.offset >= len(parser.data) || parser.data[parser.offset] != 'e' {
		return false
	}
	parser.offset++
	return true
}

func parseEndpoint(value string) (*url.URL, error) {
	if strings.TrimSpace(value) != value || value == "" {
		return nil, errors.New("qBittorrent endpoint is invalid")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("qBittorrent endpoint must be an absolute URL without credentials")
	}
	return parsed, nil
}

func validateConnectionScope(expected, requested domain.ConfigID) error {
	if !requested.Valid() || requested != expected {
		return invalidInput("qbit.connection")
	}
	return nil
}

func invalidInput(operation string) error {
	return domain.UpstreamError{Code: domain.OutcomeInvalidInput, Operation: operation, Detail: "request is invalid"}
}

func upstreamMalformed(operation string) error {
	return domain.UpstreamError{Code: domain.OutcomeUnknown, Operation: operation, Detail: "upstream response is malformed"}
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
	case status == http.StatusRequestTimeout || status >= 500:
		code, retryable = domain.OutcomeUnavailable, true
	}
	return domain.UpstreamError{Code: code, Status: status, Retryable: retryable, Operation: operation, Detail: "upstream request failed"}
}

func upstreamCode(err error) (domain.UpstreamErrorCode, bool) {
	var upstream domain.UpstreamError
	if errors.As(err, &upstream) {
		return upstream.Code, true
	}
	return "", false
}

func translateNativeError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var upstream native.UpstreamError
	if !errors.As(err, &upstream) {
		return err
	}
	code := domain.OutcomeUnknown
	switch upstream.Code {
	case native.ErrorUnavailable:
		code = domain.OutcomeUnavailable
	case native.ErrorRateLimited:
		code = domain.OutcomeRateLimited
	case native.ErrorUnauthorized:
		code = domain.OutcomeUnauthorized
	case native.ErrorInvalidInput:
		code = domain.OutcomeInvalidInput
	case native.ErrorConflict:
		code = domain.OutcomeConflict
	case native.ErrorUnsupported:
		code = domain.OutcomeUnsupported
	case native.ErrorUnknown:
		code = domain.OutcomeUnknown
	}
	return domain.UpstreamError{
		Code: code, Status: upstream.Status, Retryable: upstream.Retryable,
		Operation: operation, Detail: "qBittorrent upstream request failed",
	}
}

type inventoryCursor struct {
	Version          int              `json:"v"`
	ConnectionID     string           `json:"connectionId"`
	SourceID         domain.RuntimeID `json:"sourceId"`
	StartedAt        time.Time        `json:"startedAt"`
	Offset           int              `json:"offset"`
	PageCount        int              `json:"pageCount"`
	PageSize         int              `json:"pageSize"`
	ObservedCount    int64            `json:"observedCount"`
	SeenIDs          []string         `json:"seenIds,omitempty"`
	PageFingerprints []string         `json:"pageFingerprints,omitempty"`
	PriorPartial     bool             `json:"priorPartial,omitempty"`
}

type torrentSummary struct {
	AddedOn           int64   `json:"added_on"`
	AmountLeft        int64   `json:"amount_left"`
	AutoTMM           bool    `json:"auto_tmm"`
	Availability      float64 `json:"availability"`
	Category          string  `json:"category"`
	Completed         int64   `json:"completed"`
	CompletionOn      int64   `json:"completion_on"`
	ContentPath       string  `json:"content_path"`
	DLLimit           int64   `json:"dl_limit"`
	DLSpeed           int64   `json:"dlspeed"`
	Downloaded        int64   `json:"downloaded"`
	DownloadedSession int64   `json:"downloaded_session"`
	ETA               int64   `json:"eta"`
	FLPiecePrio       bool    `json:"f_l_piece_prio"`
	ForceStart        bool    `json:"force_start"`
	Hash              string  `json:"hash"`
	InfoHashV1        string  `json:"infohash_v1"`
	InfoHashV2        string  `json:"infohash_v2"`
	IsPrivate         bool    `json:"isPrivate"`
	LastActivity      int64   `json:"last_activity"`
	MagnetURI         string  `json:"magnet_uri"`
	MaxRatio          float64 `json:"max_ratio"`
	MaxSeedingTime    int64   `json:"max_seeding_time"`
	Name              string  `json:"name"`
	NumComplete       int64   `json:"num_complete"`
	NumIncomplete     int64   `json:"num_incomplete"`
	NumLeechs         int     `json:"num_leechs"`
	NumSeeds          int     `json:"num_seeds"`
	Priority          int64   `json:"priority"`
	Progress          float64 `json:"progress"`
	Ratio             float64 `json:"ratio"`
	RatioLimit        float64 `json:"ratio_limit"`
	Reannounce        int64   `json:"reannounce"`
	SavePath          string  `json:"save_path"`
	SeedingTime       int64   `json:"seeding_time"`
	SeedingTimeLimit  int64   `json:"seeding_time_limit"`
	SeenComplete      int64   `json:"seen_complete"`
	SeqDL             bool    `json:"seq_dl"`
	Size              int64   `json:"size"`
	State             string  `json:"state"`
	SuperSeeding      bool    `json:"super_seeding"`
	Tags              string  `json:"tags"`
	TimeActive        int64   `json:"time_active"`
	TotalSize         int64   `json:"total_size"`
	Tracker           string  `json:"tracker"`
	UpLimit           int64   `json:"up_limit"`
	Uploaded          int64   `json:"uploaded"`
	UploadedSession   int64   `json:"uploaded_session"`
	UpSpeed           int64   `json:"upspeed"`
	HasMetadata       bool    `json:"has_metadata"`
}

type torrentIdentityFields struct {
	Hash        string `json:"hash"`
	InfoHashV1  string `json:"infohash_v1"`
	InfoHashV2  string `json:"infohash_v2"`
	HasMetadata *bool  `json:"has_metadata"`
}

type legacyFileFields struct {
	Seeds *int `json:"seeds"`
}

func decodeLegacyFiles(raw []byte) []legacyFileFields {
	if len(raw) == 0 {
		return nil
	}
	var files []legacyFileFields
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&files); err != nil {
		return nil
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil
	}
	return files
}

// mergeNativeSummaries translates the standalone module's public Torrent DTO
// into the root adapter's local summary. The raw inventory envelope is used
// only for legacy v1/v2 identity evidence; native generated DTOs never appear
// in a port result or a package-level API.
func mergeNativeSummaries(records []native.Torrent, raw []byte) []torrentSummary {
	identities := make([]torrentIdentityFields, 0, len(records))
	if len(raw) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		var decoded []torrentIdentityFields
		if err := decoder.Decode(&decoded); err == nil {
			var trailing any
			if decoder.Decode(&trailing) == io.EOF && len(decoded) == len(records) {
				identities = decoded
			}
		}
	}
	result := make([]torrentSummary, len(records))
	for index, record := range records {
		identity := torrentIdentityFields{}
		if index < len(identities) {
			identity = identities[index]
		}
		hash := record.Hash
		if identity.Hash != "" {
			hash = identity.Hash
		}
		hasMetadata := record.Size > 0 || record.TotalSize > 0
		if identity.HasMetadata != nil {
			hasMetadata = *identity.HasMetadata
		}
		result[index] = torrentSummary{
			AddedOn: record.AddedOn, Category: record.Category, CompletionOn: record.CompletionOn,
			ContentPath: record.ContentPath, Hash: hash, InfoHashV1: identity.InfoHashV1,
			InfoHashV2: identity.InfoHashV2, Name: record.Name, Progress: record.Progress,
			Ratio: record.Ratio, State: record.State, Tags: record.Tags, SavePath: record.SavePath,
			NumSeeds: int(record.NumSeeds), NumLeechs: int(record.NumLeechs), HasMetadata: hasMetadata,
		}
	}
	return result
}

func (summary torrentSummary) externalID() string {
	for _, value := range []string{
		normalizeHash(summary.Hash, 0),
		normalizeHash(summary.InfoHashV1, 40),
		normalizeHash(summary.InfoHashV2, 64),
	} {
		if value != "" {
			return value
		}
	}
	return ""
}

type torrentProperties struct {
	SavePath       string   `json:"save_path"`
	CompletionDate int64    `json:"completion_date"`
	AdditionDate   int64    `json:"addition_date"`
	ShareRatio     *float64 `json:"share_ratio"`
}

type torrentFile struct {
	Index        int     `json:"index"`
	Name         string  `json:"name"`
	Size         int64   `json:"size"`
	Progress     float64 `json:"progress"`
	Priority     int     `json:"priority"`
	Availability float64 `json:"availability"`
	Seeds        int     `json:"seeds"`
	IsSeed       bool    `json:"is_seed"`
}

func fingerprint(value any) string {
	data, _ := json.Marshal(value)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func boundedIDs(summaries []torrentSummary, maximum int) []string {
	ids := make([]string, 0, len(summaries))
	seen := make(map[string]struct{}, len(summaries))
	for _, summary := range summaries {
		id := summary.externalID()
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		if len(ids) == maximum {
			break
		}
	}
	return ids
}

func versionRevision(version VersionObservation) string {
	if version.Application != "" {
		return version.Application
	}
	return version.WebAPI
}

func versionState(value string) domain.CapabilityState {
	if value == "" {
		return domain.CapabilityUnknown
	}
	return domain.CapabilitySupported
}

func versionReason(value string) string {
	if value == "" {
		return "version observation is unavailable"
	}
	return ""
}

func validVersion(value string) bool {
	if value == "" || len(value) > maxVersionLength {
		return false
	}
	if strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r)
	}) {
		return false
	}
	for _, r := range value {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_' || r == '+') {
			return false
		}
	}
	for _, r := range value {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func normalizeHash(value string, expectedLength int) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if expectedLength != 0 && len(value) != expectedLength {
		return ""
	}
	if expectedLength == 0 && len(value) != 40 && len(value) != 64 {
		return ""
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return ""
		}
	}
	return value
}

func normalizeHex(value string) string {
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return ""
		}
	}
	return value
}

func validFraction(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

func normalizedRatio(value float64) float64 {
	if !validFractionOrRatio(value) {
		return 0
	}
	return value
}

func validFractionOrRatio(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

// qBittorrent reports -1 when a file's availability is unknown. Preserve
// that sentinel as unknown evidence rather than discarding the whole file;
// finite non-negative values are fractions or piece counts reported by the
// WebUI depending on client version.
func validAvailability(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && (value >= 0 || value == -1)
}

func isSeedingState(state string) bool {
	switch state {
	case "uploading", "stalledUP", "queuedUP", "forcedUP":
		return true
	default:
		return false
	}
}

func knownTorrentState(state string) bool {
	switch state {
	case "error", "missingFiles", "uploading", "pausedUP", "queuedUP", "stalledUP", "checkingUP", "forcedUP", "allocating", "downloading", "metaDL", "pausedDL", "queuedDL", "stalledDL", "checkingDL", "forcedDL", "checkingResumeData", "moving", "stoppedUP", "stoppedDL":
		return true
	default:
		return false
	}
}

// Only states that explicitly describe a settled payload can authorize a
// downstream import. Unknown/future qBittorrent values intentionally stay
// false even when progress is 1.
func isTerminalState(state string) bool {
	switch state {
	case "uploading", "pausedUP", "queuedUP", "stalledUP", "forcedUP", "stoppedUP", "pausedDL", "queuedDL", "stalledDL", "forcedDL", "stoppedDL":
		return true
	default:
		return false
	}
}

func splitTags(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}

func timestamp(seconds int64, reasons []string, reason string) (*time.Time, []string) {
	if seconds == 0 {
		return nil, reasons
	}
	value, valid := timestampValue(seconds)
	if !valid {
		return nil, append(reasons, reason+"_unknown")
	}
	return value, reasons
}

func mergeTimestamp(seconds int64, existing *time.Time, reasons []string, reason string) (*time.Time, []string) {
	if existing != nil {
		return existing, reasons
	}
	return timestamp(seconds, reasons, reason)
}

func timestampValue(seconds int64) (*time.Time, bool) {
	if seconds <= 0 {
		return nil, false
	}
	value := time.Unix(seconds, 0).UTC()
	return &value, true
}

func timePtr(value time.Time) *time.Time { return &value }

func normalizeRemotePath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	if value == "" {
		return ""
	}
	return path.Clean(value)
}

func remoteFilePath(contentPath, name string, fileCount int) (string, bool) {
	name = strings.ReplaceAll(strings.TrimSpace(name), `\`, "/")
	contentPath = normalizeRemotePath(contentPath)
	if name == "" || strings.ContainsRune(name, 0) {
		return "", false
	}
	if absoluteRemotePath(name) {
		clean := path.Clean(name)
		return clean, clean == name
	}
	for _, component := range strings.Split(name, "/") {
		if component == "" || component == "." || component == ".." {
			return "", false
		}
	}
	if contentPath == "" {
		clean := path.Clean(name)
		return clean, clean == name && clean != "." && !pathBoundaryMatch(clean, "..")
	}
	contentBase := path.Base(contentPath)
	if fileCount == 1 && name == contentBase && manifestType(name) != domain.ManifestCompanion {
		return contentPath, true
	}
	if name == contentBase || strings.HasPrefix(name, contentBase+"/") {
		return path.Join(path.Dir(contentPath), name), true
	}
	clean := path.Join(contentPath, name)
	if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." {
		return "", false
	}
	return clean, true
}

func normalizeMappingPrefix(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	if value == "" {
		return ""
	}
	clean := path.Clean(value)
	if clean != "/" {
		clean = strings.TrimRight(clean, "/")
	}
	return clean
}

func absoluteRemotePath(value string) bool {
	if strings.HasPrefix(value, "/") {
		return true
	}
	return len(value) >= 3 && value[1] == ':' && value[2] == '/'
}

func pathBoundaryMatch(value, prefix string) bool {
	if value == "" || prefix == "" {
		return false
	}
	if prefix == "/" {
		return strings.HasPrefix(value, "/")
	}
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}

func manifestType(name string) domain.ManifestEntryType {
	switch strings.ToLower(path.Ext(name)) {
	case ".srt", ".ass", ".ssa", ".vtt", ".sub", ".idx":
		return domain.ManifestSubtitle
	case ".mkv", ".mp4", ".m4v", ".avi", ".mov", ".ts", ".m2ts", ".webm", ".wmv", ".flv":
		return domain.ManifestFile
	default:
		return domain.ManifestCompanion
	}
}

func manifestRole(name string) domain.ManifestRole {
	switch manifestType(name) {
	case domain.ManifestSubtitle:
		return domain.RoleSubtitle
	case domain.ManifestFile:
		return domain.RoleVideo
	default:
		return domain.RoleCompanion
	}
}

func uniqueReasons(reasons []string) []string {
	if len(reasons) == 0 {
		return nil
	}
	result := make([]string, 0, len(reasons))
	seen := make(map[string]struct{}, len(reasons))
	for _, reason := range reasons {
		if reason == "" {
			continue
		}
		if _, exists := seen[reason]; exists {
			continue
		}
		seen[reason] = struct{}{}
		result = append(result, reason)
	}
	return result
}

func addReason(reasons *[]string, reason string) {
	if reason == "" || len(*reasons) >= maxReasonCodes {
		return
	}
	for _, current := range *reasons {
		if current == reason {
			return
		}
	}
	*reasons = append(*reasons, reason)
}
