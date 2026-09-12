// Package qbittorrent implements the small, read-only qBittorrent WebUI
// compatibility boundary used by Mastarr. It owns its HTTP session, cookie
// handling and upstream DTOs so the client can be tested and versioned without
// importing Mastarr's root module.
package qbittorrent

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
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	generated "github.com/guilycst/mastarr/clients/qbittorrent/generated"
)

const (
	defaultMaxResponseBytes int64 = 8 << 20
	defaultMaxItems               = 10_000
	defaultMaxFiles               = 100_000
	defaultHTTPTimeout            = 15 * time.Second
	maxVersionBytes               = 128
	maxHashChars                  = 128
	maxHashAggregateBytes         = 4 << 10
	maxUsernameChars              = 256
	maxPasswordChars              = 1024
	maxFilterChars                = 64
	maxCategoryChars              = 512
	maxTagChars                   = 512
	maxSortChars                  = 64
)

const (
	apiLogin       = "/api/v2/auth/login"
	apiAppVersion  = "/api/v2/app/version"
	apiWebAPI      = "/api/v2/app/webapiVersion"
	apiTorrentInfo = "/api/v2/torrents/info"
	apiProperties  = "/api/v2/torrents/properties"
	apiFiles       = "/api/v2/torrents/files"
	apiCategories  = "/api/v2/torrents/categories"
	apiTags        = "/api/v2/torrents/tags"
)

var (
	applicationVersionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+([-+]?[A-Za-z][A-Za-z0-9._+-]*)?$`)
	webAPIVersionPattern      = regexp.MustCompile(`^[0-9]+\.[0-9]+(\.[0-9]+)?([-+]?[A-Za-z][A-Za-z0-9._+-]*)?$`)
	errResponseTooLarge       = errors.New("response exceeds configured bound")
)

// ErrorCode classifies a sanitized qBittorrent failure. Error messages never
// include the endpoint, credentials or upstream response body.
type ErrorCode string

const (
	ErrorUnavailable  ErrorCode = "unavailable"
	ErrorRateLimited  ErrorCode = "rate_limited"
	ErrorUnauthorized ErrorCode = "unauthorized"
	ErrorInvalidInput ErrorCode = "invalid_input"
	ErrorConflict     ErrorCode = "conflict"
	ErrorUnsupported  ErrorCode = "unsupported"
	ErrorUnknown      ErrorCode = "unknown"
)

// UpstreamError is returned for failures originating at the qBittorrent
// boundary. It deliberately omits response text because upstream bodies can
// contain credentials, private paths or other sensitive information.
type UpstreamError struct {
	Code      ErrorCode
	Operation string
	Status    int
	Retryable bool
}

func (e UpstreamError) Error() string {
	operation := e.Operation
	if operation == "" {
		operation = "request"
	}
	if e.Status > 0 {
		return fmt.Sprintf("qBittorrent %s failed (%s, status %d)", operation, e.Code, e.Status)
	}
	return fmt.Sprintf("qBittorrent %s failed (%s)", operation, e.Code)
}

// IsCode reports whether err, including a wrapped error, has the requested
// sanitized upstream code.
func IsCode(err error, code ErrorCode) bool {
	var upstream UpstreamError
	return errors.As(err, &upstream) && upstream.Code == code
}

// Config controls one qBittorrent WebUI connection. Username and Password are
// used only for the in-memory login request and are never copied into an
// observation or error. HTTPClient is cloned and does not inherit caller
// cookie state; this client applies only the validated SID selected for each
// request. Redirects are rejected to prevent a SID-bearing request from
// leaving the configured origin.
type Config struct {
	Endpoint string
	Username string
	Password string

	HTTPClient *http.Client

	// MaxResponseBytes bounds every upstream response body. A non-positive
	// value uses the safe default of 8 MiB.
	MaxResponseBytes int64
	// MaxItems and MaxFiles reject valid JSON arrays that exceed the caller's
	// memory bound instead of silently truncating inventory evidence.
	MaxItems int
	MaxFiles int
}

// Client is a cookie-authenticated, read-only qBittorrent WebUI client.
type Client struct {
	endpoint *url.URL
	http     *http.Client
	config   Config

	authMu         sync.Mutex
	authenticated  bool
	authInFlight   *authFlight
	authGeneration uint64
	sessionSID     string
}

// authFlight publishes one immutable result to every caller that joined the
// same authentication attempt. The result is written while authMu is held
// and read only after done is closed, so an ordinary failed flight cannot
// turn every waiter into a new login leader. A leader cancellation is marked
// separately so active waiters can elect one replacement attempt.
type authFlight struct {
	done           chan struct{}
	err            error // immutable result returned to callers that joined
	leaderErr      error // caller-scoped result returned to the leader
	leaderCanceled bool
}

// sessionCredential binds the SID selected for a request to the generation
// that authenticated it. Keeping both values under authMu prevents a request
// from being labeled with one generation while sending another session.
type sessionCredential struct {
	sid        string
	generation uint64
}

// Versions contains both read-only version probes.
type Versions struct {
	Application string
	WebAPI      string
}

// TorrentListOptions is passed through to qBittorrent's /torrents/info
// endpoint. A zero value requests the complete upstream list, subject to the
// configured response and item bounds.
type TorrentListOptions struct {
	Filter   string
	Category string
	Tag      string
	Sort     string
	Reverse  bool
	Limit    int
	Offset   int
	Hashes   []string
}

// Torrent is the normalized qBittorrent inventory record. Timestamp and
// counter fields retain the upstream Unix or sentinel values (qBittorrent uses
// -1 when an integer is unknown).
type Torrent struct {
	AddedOn           int64
	AmountLeft        int64
	AutoTMM           bool
	Availability      float64
	Category          string
	Completed         int64
	CompletionOn      int64
	ContentPath       string
	DLLimit           int64
	DLSpeed           int64
	Downloaded        int64
	DownloadedSession int64
	ETA               int64
	FLPiecePrio       bool
	ForceStart        bool
	Hash              string
	IsPrivate         bool
	LastActivity      int64
	MagnetURI         string
	MaxRatio          float64
	MaxSeedingTime    int64
	Name              string
	NumComplete       int64
	NumIncomplete     int64
	NumLeechs         int64
	NumSeeds          int64
	Priority          int64
	Progress          float64
	Ratio             float64
	RatioLimit        float64
	Reannounce        int64
	SavePath          string
	SeedingTime       int64
	SeedingTimeLimit  int64
	SeenComplete      int64
	SeqDL             bool
	Size              int64
	State             string
	SuperSeeding      bool
	Tags              string
	TimeActive        int64
	TotalSize         int64
	Tracker           string
	UpLimit           int64
	Uploaded          int64
	UploadedSession   int64
	UpSpeed           int64
}

// TorrentProperties contains qBittorrent's generic properties response.
type TorrentProperties struct {
	SavePath               string
	CreationDate           int64
	PieceSize              int64
	Comment                string
	TotalWasted            int64
	TotalUploaded          int64
	TotalUploadedSession   int64
	TotalDownloaded        int64
	TotalDownloadedSession int64
	UpLimit                int64
	DLLimit                int64
	TimeElapsed            int64
	SeedingTime            int64
	NbConnections          int64
	NbConnectionsLimit     int64
	ShareRatio             float64
	AdditionDate           int64
	CompletionDate         int64
	CreatedBy              string
	DLSpeedAvg             int64
	DLSpeed                int64
	ETA                    int64
	LastSeen               int64
	Peers                  int64
	PeersTotal             int64
	PiecesHave             int64
	PiecesNum              int64
	Reannounce             int64
	Seeds                  int64
	SeedsTotal             int64
	TotalSize              int64
	UpSpeedAvg             int64
	UpSpeed                int64
	IsPrivate              bool
}

// TorrentFile is one file from qBittorrent's torrent contents response.
type TorrentFile struct {
	Index        int64
	Name         string
	Size         int64
	Progress     float64
	Priority     int64
	IsSeed       bool
	PieceRange   []int64
	Availability float64
}

// Category is one value from the qBittorrent category map. The map key is the
// authoritative category identifier and is retained by Categories.
type Category struct {
	Name     string
	SavePath string
}

// New validates the endpoint and prepares an isolated in-memory session
// credential without making a network request.
func New(config Config) (*Client, error) {
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = defaultMaxResponseBytes
	}
	if config.MaxResponseBytes >= math.MaxInt64 {
		return nil, errors.New("qBittorrent response bound is invalid")
	}
	if config.MaxItems <= 0 {
		config.MaxItems = defaultMaxItems
	}
	if config.MaxFiles <= 0 {
		config.MaxFiles = defaultMaxFiles
	}
	if config.MaxItems > defaultMaxItems || config.MaxFiles > defaultMaxFiles {
		return nil, errors.New("qBittorrent item bound exceeds compatibility ceiling")
	}
	if config.Username == "" || config.Password == "" {
		return nil, errors.New("qBittorrent credentials are required")
	}
	if !validCredential(config.Username, maxUsernameChars) || !validCredential(config.Password, maxPasswordChars) {
		return nil, errors.New("qBittorrent credential exceeds compatibility bound")
	}

	baseClient := http.DefaultClient
	if config.HTTPClient != nil {
		baseClient = config.HTTPClient
	}
	clientCopy := *baseClient
	if clientCopy.Timeout <= 0 {
		clientCopy.Timeout = defaultHTTPTimeout
	}
	// Do not let net/http select or persist cookies on our behalf. In
	// particular, a delayed response from an old request must never restore an
	// obsolete SID before its status can be checked against authGeneration.
	clientCopy.Jar = nil
	// qBittorrent's login response establishes the SID cookie. Refusing every
	// redirect makes the origin boundary explicit and keeps POST login bodies
	// from being replayed by net/http at another location.
	clientCopy.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &Client{
		endpoint: endpoint,
		http:     &clientCopy,
		config:   config,
	}, nil
}

// NewClient is an explicit constructor alias.
func NewClient(config Config) (*Client, error) { return New(config) }

// Login establishes the qBittorrent SID session. It is the sole POST made by
// this module; all other methods are GET-only reads.
func (c *Client) Login(ctx context.Context) error {
	return c.ensureSession(ctx)
}

// ApplicationVersion reads /api/v2/app/version.
func (c *Client) ApplicationVersion(ctx context.Context) (string, error) {
	return c.readVersion(ctx, "qbit.app.version", apiAppVersion)
}

// GetApplicationVersion is an explicit method-name alias.
func (c *Client) GetApplicationVersion(ctx context.Context) (string, error) {
	return c.ApplicationVersion(ctx)
}

// WebAPIVersion reads /api/v2/app/webapiVersion.
func (c *Client) WebAPIVersion(ctx context.Context) (string, error) {
	return c.readVersion(ctx, "qbit.app.webapi_version", apiWebAPI)
}

// GetWebAPIVersion is an explicit method-name alias.
func (c *Client) GetWebAPIVersion(ctx context.Context) (string, error) {
	return c.WebAPIVersion(ctx)
}

// Versions reads both qBittorrent version endpoints in order.
func (c *Client) Versions(ctx context.Context) (Versions, error) {
	application, err := c.ApplicationVersion(ctx)
	if err != nil {
		return Versions{}, err
	}
	webAPI, err := c.WebAPIVersion(ctx)
	if err != nil {
		return Versions{}, err
	}
	return Versions{Application: application, WebAPI: webAPI}, nil
}

// ListTorrents reads /api/v2/torrents/info and preserves the upstream order.
func (c *Client) ListTorrents(ctx context.Context, options TorrentListOptions) ([]Torrent, error) {
	query, err := listQuery(options)
	if err != nil {
		return nil, err
	}
	body, err := c.get(ctx, "qbit.torrents.info", apiTorrentInfo, query)
	if err != nil {
		return nil, err
	}
	var records []generated.TorrentInfo
	if err := decodeJSON(body, &records); err != nil {
		return nil, malformed("qbit.torrents.info")
	}
	if len(records) > c.config.MaxItems {
		return nil, bounded("qbit.torrents.info", c.config.MaxItems)
	}
	seen := make(map[string]struct{}, len(records))
	for i := range records {
		if !validInventoryHash(records[i].Hash) {
			return nil, malformed("qbit.torrents.info")
		}
		identity := canonicalHashIdentity(records[i].Hash)
		if _, exists := seen[identity]; exists {
			return nil, malformed("qbit.torrents.info")
		}
		seen[identity] = struct{}{}
	}
	result := make([]Torrent, len(records))
	for i := range records {
		result[i] = normalizeTorrent(records[i])
	}
	return result, nil
}

// Torrents is a concise alias for ListTorrents.
func (c *Client) Torrents(ctx context.Context, options TorrentListOptions) ([]Torrent, error) {
	return c.ListTorrents(ctx, options)
}

// GetTorrentProperties reads generic properties for one torrent.
func (c *Client) GetTorrentProperties(ctx context.Context, hash string) (TorrentProperties, error) {
	if err := validateHash(hash); err != nil {
		return TorrentProperties{}, err
	}
	body, err := c.get(ctx, "qbit.torrents.properties", apiProperties, url.Values{"hash": {hash}})
	if err != nil {
		return TorrentProperties{}, err
	}
	var properties generated.TorrentProperties
	if err := decodeJSON(body, &properties); err != nil {
		return TorrentProperties{}, malformed("qbit.torrents.properties")
	}
	return normalizeProperties(properties), nil
}

// TorrentProperties is a method-name alias for GetTorrentProperties.
func (c *Client) TorrentProperties(ctx context.Context, hash string) (TorrentProperties, error) {
	return c.GetTorrentProperties(ctx, hash)
}

// GetTorrentFiles reads torrent contents for one hash.
func (c *Client) GetTorrentFiles(ctx context.Context, hash string) ([]TorrentFile, error) {
	if err := validateHash(hash); err != nil {
		return nil, err
	}
	body, err := c.get(ctx, "qbit.torrents.files", apiFiles, url.Values{"hash": {hash}})
	if err != nil {
		return nil, err
	}
	var files []generated.TorrentFile
	if err := decodeJSON(body, &files); err != nil {
		return nil, malformed("qbit.torrents.files")
	}
	if len(files) > c.config.MaxFiles {
		return nil, bounded("qbit.torrents.files", c.config.MaxFiles)
	}
	result := make([]TorrentFile, len(files))
	for i := range files {
		if len(files[i].PieceRange) != 2 || files[i].PieceRange[0] < 0 || files[i].PieceRange[1] < files[i].PieceRange[0] {
			return nil, malformed("qbit.torrents.files")
		}
		result[i] = normalizeFile(files[i])
	}
	return result, nil
}

// TorrentFiles is a method-name alias for GetTorrentFiles.
func (c *Client) TorrentFiles(ctx context.Context, hash string) ([]TorrentFile, error) {
	return c.GetTorrentFiles(ctx, hash)
}

// Categories reads the map returned by /api/v2/torrents/categories. The map
// key is retained even when an upstream value's name differs.
func (c *Client) Categories(ctx context.Context) (map[string]Category, error) {
	body, err := c.get(ctx, "qbit.torrents.categories", apiCategories, nil)
	if err != nil {
		return nil, err
	}
	var categories generated.CategoryMap
	if err := decodeJSON(body, &categories); err != nil {
		return nil, malformed("qbit.torrents.categories")
	}
	if len(categories) > c.config.MaxItems {
		return nil, bounded("qbit.torrents.categories", c.config.MaxItems)
	}
	result := make(map[string]Category, len(categories))
	for name, category := range categories {
		result[name] = Category{Name: category.Name, SavePath: category.SavePath}
	}
	return result, nil
}

// GetCategories is a method-name alias for Categories.
func (c *Client) GetCategories(ctx context.Context) (map[string]Category, error) {
	return c.Categories(ctx)
}

// Tags reads all tag names. qBittorrent returns a JSON array rather than a
// map, so order is preserved for callers that present upstream evidence.
func (c *Client) Tags(ctx context.Context) ([]string, error) {
	body, err := c.get(ctx, "qbit.torrents.tags", apiTags, nil)
	if err != nil {
		return nil, err
	}
	var tags []string
	if err := decodeJSON(body, &tags); err != nil {
		return nil, malformed("qbit.torrents.tags")
	}
	if len(tags) > c.config.MaxItems {
		return nil, bounded("qbit.torrents.tags", c.config.MaxItems)
	}
	for _, tag := range tags {
		if !validBoundedText(tag, maxTagChars, false) {
			return nil, malformed("qbit.torrents.tags")
		}
	}
	return tags, nil
}

// GetTags is a method-name alias for Tags.
func (c *Client) GetTags(ctx context.Context) ([]string, error) { return c.Tags(ctx) }

func (c *Client) readVersion(ctx context.Context, operation, endpoint string) (string, error) {
	body, err := c.get(ctx, operation, endpoint, nil)
	if err != nil {
		return "", err
	}
	version := string(body)
	if len(version) == 0 || len(version) > maxVersionBytes || !utf8.ValidString(version) || strings.IndexFunc(version, unicode.IsSpace) >= 0 || strings.IndexFunc(version, unicode.IsControl) >= 0 {
		return "", malformed(operation)
	}
	pattern := applicationVersionPattern
	if operation == "qbit.app.webapi_version" {
		pattern = webAPIVersionPattern
	}
	if !pattern.MatchString(version) {
		return "", malformed(operation)
	}
	return version, nil
}

func (c *Client) ensureSession(ctx context.Context) error {
	for {
		if err := contextError(ctx); err != nil {
			return err
		}
		c.authMu.Lock()
		if c.authenticated {
			c.authMu.Unlock()
			if err := contextError(ctx); err != nil {
				return err
			}
			return nil
		}
		if c.authInFlight == nil {
			flight := &authFlight{done: make(chan struct{})}
			c.authInFlight = flight
			c.authMu.Unlock()

			sid, authErr, responseComplete := c.authenticate(ctx)
			c.authMu.Lock()
			err := authErr
			leaderErr := authErr
			if ctxErr := contextError(ctx); ctxErr != nil {
				// Cancellation belongs to the leader's call. A completed
				// upstream response still has an immutable result for active
				// waiters. A completed successful login can install its
				// validated session for them; only an interrupted attempt
				// lets them elect a replacement flight.
				leaderErr = ctxErr
				if responseComplete && err == nil {
					c.authenticated = true
					c.authGeneration++
					c.sessionSID = sid
				} else if !responseComplete {
					err = ctxErr
					flight.leaderCanceled = true
				}
			} else if err == nil {
				c.authenticated = true
				c.authGeneration++
				c.sessionSID = sid
			}
			flight.err = err
			flight.leaderErr = leaderErr
			leaderResult := flight.leaderErr
			c.authInFlight = nil
			close(flight.done)
			c.authMu.Unlock()
			if leaderResult != nil {
				return leaderResult
			}
			if err := contextError(ctx); err != nil {
				return err
			}
			return nil
		}
		flight := c.authInFlight
		c.authMu.Unlock()
		select {
		case <-flight.done:
			if err := contextError(ctx); err != nil {
				return err
			}
			if flight.leaderCanceled {
				continue
			}
			return flight.err
		case <-contextDone(ctx):
			return contextError(ctx)
		}
	}
}

func (c *Client) authenticate(ctx context.Context) (string, error, bool) {
	form := url.Values{}
	form.Set("username", c.config.Username)
	form.Set("password", c.config.Password)
	body, status, cookies, responseComplete, err := c.requestOnce(ctx, "qbit.auth.login", http.MethodPost, apiLogin, nil, "", strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", 16<<10)
	if status != 0 && status != http.StatusOK {
		return "", statusError("qbit.auth.login", status), responseComplete
	}
	if err != nil {
		return "", err, responseComplete
	}
	if !bytes.Equal(bytes.TrimSpace(body), []byte("Ok.")) {
		return "", UpstreamError{Code: ErrorUnauthorized, Operation: "qbit.auth.login", Status: status}, responseComplete
	}
	if sid, ok := c.usableSID(cookies); ok {
		return sid, nil, responseComplete
	}
	return "", UpstreamError{Code: ErrorUnauthorized, Operation: "qbit.auth.login", Status: status}, responseComplete
}

func (c *Client) invalidateSession(expectedGeneration uint64) bool {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if !c.authenticated || c.authGeneration != expectedGeneration {
		return false
	}
	c.authenticated = false
	c.sessionSID = ""
	return true
}

func (c *Client) sessionCredential() (sessionCredential, bool) {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	if !c.authenticated || !usableSIDValue(c.sessionSID) {
		return sessionCredential{}, false
	}
	return sessionCredential{sid: c.sessionSID, generation: c.authGeneration}, true
}

func (c *Client) currentSession(ctx context.Context) (sessionCredential, error) {
	for {
		if err := contextError(ctx); err != nil {
			return sessionCredential{}, err
		}
		if credential, ok := c.sessionCredential(); ok {
			return credential, nil
		}
		if err := c.ensureSession(ctx); err != nil {
			return sessionCredential{}, err
		}
	}
}

func (c *Client) get(ctx context.Context, operation, endpoint string, query url.Values) ([]byte, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	credential, err := c.currentSession(ctx)
	if err != nil {
		return nil, err
	}
	return c.getAfterSession(ctx, operation, endpoint, query, true, credential)
}

func (c *Client) getAfterSession(ctx context.Context, operation, endpoint string, query url.Values, retryAuth bool, credential sessionCredential) ([]byte, error) {
	body, status, _, _, err := c.requestOnce(ctx, operation, http.MethodGet, endpoint, query, credential.sid, nil, "", c.config.MaxResponseBytes)
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		if retryAuth {
			c.invalidateSession(credential.generation)
			if err := c.ensureSession(ctx); err != nil {
				return nil, err
			}
			nextCredential, err := c.currentSession(ctx)
			if err != nil {
				return nil, err
			}
			return c.getAfterSession(ctx, operation, endpoint, query, false, nextCredential)
		}
		return nil, statusError(operation, status)
	}
	if status != 0 && status != http.StatusOK {
		return nil, statusError(operation, status)
	}
	if err != nil {
		return nil, err
	}
	return body, nil
}

// requestOnce reports whether an upstream response completed with a usable
// status identity. An oversized body still has a completed HTTP status even
// though its content is intentionally discarded at the bound.
func (c *Client) requestOnce(ctx context.Context, operation, method, endpoint string, query url.Values, sid string, body io.Reader, contentType string, maxBytes int64) ([]byte, int, []*http.Cookie, bool, error) {
	if err := contextError(ctx); err != nil {
		return nil, 0, nil, false, err
	}
	requestURL := *c.endpoint
	requestURL.Path = strings.TrimRight(c.endpoint.Path, "/") + endpoint
	requestURL.RawPath = ""
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return nil, 0, nil, false, UpstreamError{Code: ErrorInvalidInput, Operation: operation}
	}
	request.Header.Set("Accept", "application/json, text/plain")
	request.Header.Set("User-Agent", "mastarr-qbittorrent-client/0.0.1")
	request.Header.Set("Referer", c.origin()+"/")
	request.Header.Set("Origin", c.origin())
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if sid != "" {
		if !usableSIDValue(sid) {
			return nil, 0, nil, false, invalidInput(operation)
		}
		request.Header.Set("Cookie", "SID="+sid)
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, 0, nil, false, ctxErr
		}
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil, 0, nil, false, UpstreamError{Code: ErrorUnavailable, Operation: operation, Retryable: true}
		}
		return nil, 0, nil, false, UpstreamError{Code: ErrorUnavailable, Operation: operation, Retryable: true}
	}
	defer response.Body.Close()
	cookies := response.Cookies()
	data, err := readBounded(response.Body, maxBytes)
	if err != nil {
		bodyBounded := response.StatusCode != 0 && errors.Is(err, errResponseTooLarge)
		return nil, response.StatusCode, cookies, bodyBounded, UpstreamError{Code: ErrorUnknown, Operation: operation, Status: response.StatusCode}
	}
	return data, response.StatusCode, cookies, true, nil
}

func (c *Client) origin() string {
	return c.endpoint.Scheme + "://" + c.endpoint.Host
}

func parseEndpoint(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return nil, errors.New("qBittorrent endpoint is invalid")
	}
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("qBittorrent endpoint is invalid")
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return nil, errors.New("qBittorrent endpoint scheme is unsupported")
	}
	if !utf8.ValidString(endpoint.Host) {
		return nil, errors.New("qBittorrent endpoint is invalid")
	}
	// Keep the configured path as a literal reverse-proxy prefix. Dot
	// segments, escaped path bytes and repeated separators have different
	// normalization rules across proxies, so they are rejected at the origin
	// boundary instead of being sent for a server to interpret.
	if strings.Contains(endpoint.Path, "%") || strings.Contains(endpoint.Path, "\\") || strings.Contains(endpoint.Path, "//") {
		return nil, errors.New("qBittorrent endpoint path is ambiguous")
	}
	if endpoint.RawPath != "" && strings.Contains(endpoint.RawPath, "%") {
		return nil, errors.New("qBittorrent endpoint path is ambiguous")
	}
	for _, segment := range strings.Split(endpoint.Path, "/") {
		if segment == "." || segment == ".." {
			return nil, errors.New("qBittorrent endpoint path is ambiguous")
		}
	}
	return endpoint, nil
}

func listQuery(options TorrentListOptions) (url.Values, error) {
	query := url.Values{}
	for _, field := range []struct {
		key   string
		value string
		max   int
	}{
		{key: "filter", value: options.Filter, max: maxFilterChars},
		{key: "category", value: options.Category, max: maxCategoryChars},
		{key: "tag", value: options.Tag, max: maxTagChars},
		{key: "sort", value: options.Sort, max: maxSortChars},
	} {
		if err := validateQueryText(field.value, field.max); err != nil {
			return nil, err
		}
		if field.value != "" {
			query.Set(field.key, field.value)
		}
	}
	if options.Reverse {
		query.Set("reverse", strconv.FormatBool(options.Reverse))
	}
	if options.Limit < 0 || options.Limit > defaultMaxItems || options.Offset < -defaultMaxItems || options.Offset > defaultMaxItems {
		return nil, invalidInput("qbit.torrents.info")
	}
	if options.Limit > 0 {
		query.Set("limit", strconv.Itoa(options.Limit))
	}
	if options.Offset != 0 {
		query.Set("offset", strconv.Itoa(options.Offset))
	}
	if len(options.Hashes) > 0 {
		hashes := make([]string, len(options.Hashes))
		seen := make(map[string]struct{}, len(options.Hashes))
		joinedBytes := 0
		for i, hash := range options.Hashes {
			if err := validateHash(hash); err != nil {
				return nil, err
			}
			identity := canonicalHashIdentity(hash)
			if _, exists := seen[identity]; exists {
				return nil, invalidInput("qbit.torrents.info")
			}
			seen[identity] = struct{}{}
			if i > 0 {
				joinedBytes++
			}
			joinedBytes += len(hash)
			if joinedBytes > maxHashAggregateBytes {
				return nil, invalidInput("qbit.torrents.info")
			}
			hashes[i] = hash
		}
		query.Set("hashes", strings.Join(hashes, "|"))
	}
	return query, nil
}

func validateHash(hash string) error {
	if !validBoundedText(hash, maxHashChars, true) || strings.Contains(hash, "|") || strings.IndexFunc(hash, unicode.IsSpace) >= 0 {
		return invalidInput("qbit.torrents.hash")
	}
	return nil
}

func validInventoryHash(hash string) bool {
	if !validBoundedText(hash, maxHashChars, true) || strings.Contains(hash, "|") || strings.IndexFunc(hash, unicode.IsSpace) >= 0 {
		return false
	}
	if len(hash) != 40 && len(hash) != 64 {
		return false
	}
	return isHexIdentity(hash)
}

func canonicalHashIdentity(hash string) string {
	if (len(hash) == 40 || len(hash) == 64) && isHexIdentity(hash) {
		return strings.ToLower(hash)
	}
	return hash
}

func isHexIdentity(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func validateQueryText(value string, maxChars int) error {
	if !validBoundedText(value, maxChars, false) {
		return invalidInput("qbit.torrents.info")
	}
	return nil
}

func validCredential(value string, maxChars int) bool {
	return value != "" && validBoundedText(value, maxChars, false)
}

func validBoundedText(value string, maxChars int, rejectEmpty bool) bool {
	if rejectEmpty && value == "" {
		return false
	}
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxChars && strings.IndexFunc(value, unicode.IsControl) < 0
}

func usableSIDValue(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsSpace) >= 0 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	// http.Cookie.String rejects values that could change the meaning of the
	// manually controlled Cookie header. Check its serialized form once here
	// so every accepted SID is safe to attach verbatim to a request.
	cookie := (&http.Cookie{Name: "SID", Value: value}).String()
	return cookie == "SID="+value
}

func (c *Client) usableSID(responseCookies []*http.Cookie) (string, bool) {
	requestURL := c.apiRootURL()
	for _, responseCookie := range responseCookies {
		if responseCookie == nil || responseCookie.Name != "SID" || !usableSIDValue(responseCookie.Value) {
			continue
		}
		if responseCookie.MaxAge < 0 || (!responseCookie.Expires.IsZero() && !responseCookie.Expires.After(time.Now())) {
			continue
		}
		if responseCookie.Secure && requestURL.Scheme != "https" {
			continue
		}
		if !cookieDomainApplies(responseCookie.Domain, requestURL.Hostname()) || !cookiePathApplies(responseCookie.Path, requestURL.Path, c.loginPath()) {
			continue
		}
		return responseCookie.Value, true
	}
	return "", false
}

func (c *Client) hasUsableSID(responseCookies []*http.Cookie) bool {
	_, ok := c.usableSID(responseCookies)
	return ok
}

func (c *Client) apiRootURL() *url.URL {
	requestURL := *c.endpoint
	requestURL.Path = strings.TrimRight(c.endpoint.Path, "/") + "/api/v2/"
	requestURL.RawPath = ""
	requestURL.RawQuery = ""
	return &requestURL
}

func (c *Client) loginPath() string {
	return strings.TrimRight(c.endpoint.Path, "/") + apiLogin
}

func cookieDomainApplies(cookieDomain, host string) bool {
	if cookieDomain == "" {
		return true
	}
	cookieDomain = strings.TrimPrefix(strings.ToLower(cookieDomain), ".")
	host = strings.ToLower(host)
	return host == cookieDomain || strings.HasSuffix(host, "."+cookieDomain)
}

func cookiePathApplies(cookiePath, requestPath, sourcePath string) bool {
	if cookiePath == "" {
		cookiePath = defaultCookiePath(sourcePath)
	}
	if cookiePath == "" || cookiePath[0] != '/' || requestPath == "" || requestPath[0] != '/' {
		return false
	}
	if requestPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(requestPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") || (len(requestPath) > len(cookiePath) && requestPath[len(cookiePath)] == '/')
}

func defaultCookiePath(sourcePath string) string {
	if sourcePath == "" || sourcePath[0] != '/' {
		return "/"
	}
	if index := strings.LastIndexByte(sourcePath, '/'); index <= 0 {
		return "/"
	} else {
		return sourcePath[:index]
	}
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func contextDone(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}

func decodeJSON(data []byte, target any) error {
	if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("empty JSON response")
	}
	if !utf8.Valid(data) {
		return errors.New("JSON response is not valid UTF-8")
	}
	if err := validateJSONMembers(data, reflect.TypeOf(target)); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("trailing JSON response data")
	}
	return nil
}

// validateJSONMembers applies the exact, case-sensitive object-member rules
// that encoding/json does not provide. It rejects duplicate names within one
// object while keeping separate objects in an array independent. Map keys are
// dynamic by contract; fixed struct fields use their JSON tags as the only
// accepted spelling.
func validateJSONMembers(data []byte, targetType reflect.Type) error {
	scanner := jsonMemberScanner{decoder: json.NewDecoder(bytes.NewReader(data))}
	if err := scanner.value(targetType); err != nil {
		return err
	}
	if _, err := scanner.decoder.Token(); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON response data")
		}
		return err
	}
	return nil
}

type jsonMemberScanner struct {
	decoder *json.Decoder
}

func (s *jsonMemberScanner) value(valueType reflect.Type) error {
	token, err := s.decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("null JSON value")
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			return s.object(valueType)
		case '[':
			return s.array(valueType)
		default:
			return errors.New("unexpected JSON delimiter")
		}
	default:
		return nil
	}
}

func (s *jsonMemberScanner) object(valueType reflect.Type) error {
	valueType = indirectJSONType(valueType)
	fields := jsonStructFields(valueType)
	mapElement := jsonMapElementType(valueType)
	required := jsonRequiredFields(valueType)
	seen := make(map[string]struct{})
	for s.decoder.More() {
		token, err := s.decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("JSON object member name is invalid")
		}
		if _, exists := seen[key]; exists {
			return errors.New("duplicate JSON object member")
		}
		seen[key] = struct{}{}

		fieldType, known := fields[key]
		if fields != nil && !known {
			for fieldName := range fields {
				if strings.EqualFold(fieldName, key) {
					return errors.New("JSON object member spelling is not exact")
				}
			}
		}
		if !known {
			fieldType = mapElement
		}
		if err := s.value(fieldType); err != nil {
			return err
		}
	}
	end, err := s.decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := end.(json.Delim); !ok || delimiter != '}' {
		return errors.New("JSON object is not closed")
	}
	for fieldName := range required {
		if _, present := seen[fieldName]; !present {
			return errors.New("missing required JSON object member")
		}
	}
	return nil
}

func (s *jsonMemberScanner) array(valueType reflect.Type) error {
	valueType = indirectJSONType(valueType)
	var elementType reflect.Type
	if valueType != nil && (valueType.Kind() == reflect.Array || valueType.Kind() == reflect.Slice) {
		elementType = valueType.Elem()
	}
	for s.decoder.More() {
		if err := s.value(elementType); err != nil {
			return err
		}
	}
	end, err := s.decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := end.(json.Delim); !ok || delimiter != ']' {
		return errors.New("JSON array is not closed")
	}
	return nil
}

func indirectJSONType(valueType reflect.Type) reflect.Type {
	for valueType != nil && valueType.Kind() == reflect.Pointer {
		valueType = valueType.Elem()
	}
	return valueType
}

func jsonStructFields(valueType reflect.Type) map[string]reflect.Type {
	if valueType == nil || valueType.Kind() != reflect.Struct {
		return nil
	}
	fields := make(map[string]reflect.Type)
	for index := 0; index < valueType.NumField(); index++ {
		field := valueType.Field(index)
		if field.PkgPath != "" {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}

// jsonRequiredFields mirrors the frozen OpenAPI required sets for the four
// fixed response objects. Their current schemas require every declared
// member, including fields whose valid upstream value is zero or false.
func jsonRequiredFields(valueType reflect.Type) map[string]struct{} {
	valueType = indirectJSONType(valueType)
	switch valueType {
	case reflect.TypeOf(generated.TorrentInfo{}), reflect.TypeOf(generated.TorrentProperties{}), reflect.TypeOf(generated.TorrentFile{}), reflect.TypeOf(generated.Category{}):
		fields := jsonStructFields(valueType)
		required := make(map[string]struct{}, len(fields))
		for name := range fields {
			required[name] = struct{}{}
		}
		return required
	default:
		return nil
	}
}

func jsonMapElementType(valueType reflect.Type) reflect.Type {
	if valueType == nil || valueType.Kind() != reflect.Map || valueType.Key().Kind() != reflect.String {
		return nil
	}
	return valueType.Elem()
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxResponseBytes
	}
	if maxBytes >= math.MaxInt64 {
		return nil, errors.New("response bound is invalid")
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, errors.New("response could not be read")
	}
	if int64(len(data)) > maxBytes {
		return nil, errResponseTooLarge
	}
	return data, nil
}

func statusError(operation string, status int) UpstreamError {
	code := ErrorUnknown
	retryable := false
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		code = ErrorUnauthorized
	case status == http.StatusBadRequest:
		code = ErrorInvalidInput
	case status == http.StatusNotFound && isVersionOperation(operation):
		code = ErrorUnsupported
	case status == http.StatusNotFound:
		code = ErrorUnavailable
	case status == http.StatusConflict:
		code = ErrorConflict
	case status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented || status == http.StatusHTTPVersionNotSupported:
		code = ErrorUnsupported
	case status >= http.StatusOK && status < http.StatusMultipleChoices:
		code = ErrorUnsupported
	case status == http.StatusTooManyRequests:
		code = ErrorRateLimited
		retryable = true
	case status >= http.StatusInternalServerError:
		code = ErrorUnavailable
		retryable = true
	}
	return UpstreamError{Code: code, Operation: operation, Status: status, Retryable: retryable}
}

func isVersionOperation(operation string) bool {
	return operation == "qbit.app.version" || operation == "qbit.app.webapi_version"
}

func invalidInput(operation string) UpstreamError {
	return UpstreamError{Code: ErrorInvalidInput, Operation: operation}
}

func malformed(operation string) UpstreamError {
	return UpstreamError{Code: ErrorUnknown, Operation: operation}
}

func bounded(operation string, _ int) UpstreamError {
	return UpstreamError{Code: ErrorUnknown, Operation: operation, Retryable: false}
}

func normalizeTorrent(record generated.TorrentInfo) Torrent {
	return Torrent{
		AddedOn:           record.AddedOn,
		AmountLeft:        record.AmountLeft,
		AutoTMM:           record.AutoTmm,
		Availability:      record.Availability,
		Category:          record.Category,
		Completed:         record.Completed,
		CompletionOn:      record.CompletionOn,
		ContentPath:       record.ContentPath,
		DLLimit:           record.DlLimit,
		DLSpeed:           record.Dlspeed,
		Downloaded:        record.Downloaded,
		DownloadedSession: record.DownloadedSession,
		ETA:               record.Eta,
		FLPiecePrio:       record.FLPiecePrio,
		ForceStart:        record.ForceStart,
		Hash:              record.Hash,
		IsPrivate:         record.IsPrivate,
		LastActivity:      record.LastActivity,
		MagnetURI:         record.MagnetUri,
		MaxRatio:          record.MaxRatio,
		MaxSeedingTime:    record.MaxSeedingTime,
		Name:              record.Name,
		NumComplete:       record.NumComplete,
		NumIncomplete:     record.NumIncomplete,
		NumLeechs:         record.NumLeechs,
		NumSeeds:          record.NumSeeds,
		Priority:          record.Priority,
		Progress:          record.Progress,
		Ratio:             record.Ratio,
		RatioLimit:        record.RatioLimit,
		Reannounce:        record.Reannounce,
		SavePath:          record.SavePath,
		SeedingTime:       record.SeedingTime,
		SeedingTimeLimit:  record.SeedingTimeLimit,
		SeenComplete:      record.SeenComplete,
		SeqDL:             record.SeqDl,
		Size:              record.Size,
		State:             record.State,
		SuperSeeding:      record.SuperSeeding,
		Tags:              record.Tags,
		TimeActive:        record.TimeActive,
		TotalSize:         record.TotalSize,
		Tracker:           record.Tracker,
		UpLimit:           record.UpLimit,
		Uploaded:          record.Uploaded,
		UploadedSession:   record.UploadedSession,
		UpSpeed:           record.Upspeed,
	}
}

func normalizeProperties(properties generated.TorrentProperties) TorrentProperties {
	return TorrentProperties{
		SavePath:               properties.SavePath,
		CreationDate:           properties.CreationDate,
		PieceSize:              properties.PieceSize,
		Comment:                properties.Comment,
		TotalWasted:            properties.TotalWasted,
		TotalUploaded:          properties.TotalUploaded,
		TotalUploadedSession:   properties.TotalUploadedSession,
		TotalDownloaded:        properties.TotalDownloaded,
		TotalDownloadedSession: properties.TotalDownloadedSession,
		UpLimit:                properties.UpLimit,
		DLLimit:                properties.DlLimit,
		TimeElapsed:            properties.TimeElapsed,
		SeedingTime:            properties.SeedingTime,
		NbConnections:          properties.NbConnections,
		NbConnectionsLimit:     properties.NbConnectionsLimit,
		ShareRatio:             properties.ShareRatio,
		AdditionDate:           properties.AdditionDate,
		CompletionDate:         properties.CompletionDate,
		CreatedBy:              properties.CreatedBy,
		DLSpeedAvg:             properties.DlSpeedAvg,
		DLSpeed:                properties.DlSpeed,
		ETA:                    properties.Eta,
		LastSeen:               properties.LastSeen,
		Peers:                  properties.Peers,
		PeersTotal:             properties.PeersTotal,
		PiecesHave:             properties.PiecesHave,
		PiecesNum:              properties.PiecesNum,
		Reannounce:             properties.Reannounce,
		Seeds:                  properties.Seeds,
		SeedsTotal:             properties.SeedsTotal,
		TotalSize:              properties.TotalSize,
		UpSpeedAvg:             properties.UpSpeedAvg,
		UpSpeed:                properties.UpSpeed,
		IsPrivate:              properties.IsPrivate,
	}
}

func normalizeFile(file generated.TorrentFile) TorrentFile {
	return TorrentFile{
		Index:        file.Index,
		Name:         file.Name,
		Size:         file.Size,
		Progress:     file.Progress,
		Priority:     file.Priority,
		IsSeed:       file.IsSeed,
		PieceRange:   append([]int64(nil), file.PieceRange...),
		Availability: file.Availability,
	}
}
