// Package nzbget is an independent, read-only NZBGet JSON-RPC client.
//
// It deliberately owns its transport and wire models so an application can
// consume it without importing Mastarr's root module. Mutating NZBGet methods
// are outside this client's contract.
package nzbget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

const (
	// ProtocolVersion is the NZBGet JSON-RPC envelope version used by this
	// client. NZBGet's API calls this field "version", rather than "jsonrpc".
	ProtocolVersion = "1.1"

	// DefaultMaxResponseBytes bounds every response body before JSON decoding.
	DefaultMaxResponseBytes int64 = 8 << 20
	// DefaultRequestTimeout bounds a request when its caller supplied no
	// deadline.
	DefaultRequestTimeout         = 15 * time.Second
	maxResponseBytesCeiling int64 = 64 << 20
	maxMethodLength               = 32
	maxErrorDetailLength          = 128
	maxVersionLength              = 128
)

// Config configures an NZBGet endpoint. Endpoint must be an absolute URL. A
// /jsonrpc path is added when the URL has no path. Credentials are sent only
// as HTTP Basic authentication and are never included in errors.
type Config struct {
	Endpoint         string
	Username         string
	Password         string
	HTTPClient       *http.Client
	MaxResponseBytes int64
	RequestTimeout   time.Duration
	UserAgent        string
}

// Client is a read-only NZBGet JSON-RPC client.
type Client struct {
	endpoint         *url.URL
	httpClient       *http.Client
	maxResponseBytes int64
	requestTimeout   time.Duration
	username         string
	password         string
	userAgent        string
	requestID        atomic.Uint64
	methods          Methods
}

// New validates configuration without making a network request.
func New(config Config) (*Client, error) {
	endpoint, err := parseEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	maxResponseBytes := config.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = DefaultMaxResponseBytes
	}
	if maxResponseBytes < 1 || maxResponseBytes > maxResponseBytesCeiling {
		return nil, errors.New("nzbget response bound is outside the supported range")
	}
	requestTimeout := config.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = DefaultRequestTimeout
	}
	if requestTimeout < 0 {
		return nil, errors.New("nzbget request timeout cannot be negative")
	}
	userAgent := strings.TrimSpace(config.UserAgent)
	if userAgent == "" {
		userAgent = "mastarr-nzbget-client/0.0.1"
	}
	if len(userAgent) > 128 || strings.IndexFunc(userAgent, unicode.IsControl) >= 0 {
		return nil, errors.New("nzbget user agent is invalid")
	}

	baseClient := http.DefaultClient
	if config.HTTPClient != nil {
		baseClient = config.HTTPClient
	}
	clientCopy := *baseClient
	originalRedirect := clientCopy.CheckRedirect
	baseScheme, baseHost := endpoint.Scheme, endpoint.Host
	clientCopy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if request.URL.Scheme != baseScheme || !strings.EqualFold(request.URL.Host, baseHost) {
			return http.ErrUseLastResponse
		}
		if originalRedirect != nil {
			return originalRedirect(request, via)
		}
		return nil
	}
	client := &Client{
		endpoint: endpoint, httpClient: &clientCopy, maxResponseBytes: maxResponseBytes,
		requestTimeout: requestTimeout, username: config.Username, password: config.Password,
		userAgent: userAgent,
	}
	client.methods = NewMethods(client)
	return client, nil
}

// Endpoint returns the configured endpoint without credentials.
func (client *Client) Endpoint() string {
	if client == nil || client.endpoint == nil {
		return ""
	}
	return client.endpoint.String()
}

// Methods returns generated typed wire wrappers backed by this client's
// transport. The wrappers preserve wire DTOs; the convenience methods below
// additionally normalize them for application use.
func (client *Client) Methods() Methods {
	if client == nil {
		return Methods{}
	}
	return client.methods
}

// VersionObservation is the normalized result of version.
type VersionObservation struct {
	Version string
}

// ParameterObservation preserves an NZBGet post-processing parameter,
// including an explicit null value.
type ParameterObservation struct {
	Name  string
	Value *string
}

// GroupObservation is normalized queue-group evidence. Native NZBGet status
// and paths remain untouched; size fields expose both halves and a combined
// unsigned value so a 64-bit wire value is not truncated.
type GroupObservation struct {
	NZBID               int64
	ID                  *int64
	FirstID             *int64
	LastID              *int64
	NZBFilename         string
	NZBName             string
	NZBNicename         *string
	Kind                string
	URL                 *string
	DestDir             string
	FinalDir            *string
	Category            string
	FileSizeLo          uint32
	FileSizeHi          uint32
	FileSizeBytes       uint64
	RemainingSizeLo     uint32
	RemainingSizeHi     uint32
	RemainingSizeBytes  uint64
	PausedSizeLo        uint32
	PausedSizeHi        uint32
	PausedSizeBytes     uint64
	FileSizeMB          int64
	RemainingSizeMB     int64
	PausedSizeMB        int64
	FileCount           int64
	RemainingFileCount  int64
	RemainingParCount   int64
	MinPostTime         *time.Time
	MaxPostTime         *time.Time
	MinPriority         *int64
	MaxPriority         *int64
	ActiveDownloads     int64
	Status              string
	TotalArticles       int64
	SuccessArticles     int64
	FailedArticles      int64
	Health              int64
	CriticalHealth      int64
	DownloadedSizeLo    *uint32
	DownloadedSizeHi    *uint32
	DownloadedSizeBytes *uint64
	StatusDetails       GroupStatusDetails
	Parameters          []ParameterObservation
	PostInfoText        *string
	PostStageProgress   *int64
}

// GroupStatusDetails retains version-specific post-processing status fields.
type GroupStatusDetails struct {
	ParStatus        *string
	UnpackStatus     *string
	ExParStatus      *string
	MoveStatus       *string
	ScriptStatus     *string
	DeleteStatus     *string
	URLStatus        *string
	MarkStatus       *string
	ScriptStatuses   []ScriptStatus
	PostTotalTimeSec *int64
	PostStageTimeSec *int64
	ParTimeSec       *int64
	RepairTimeSec    *int64
	UnpackTimeSec    *int64
	Deleted          *bool
}

// FileObservation is normalized file-level evidence returned by listfiles.
type FileObservation struct {
	ID                 int64
	NZBID              int64
	NZBFilename        string
	NZBName            string
	NZBNicename        *string
	Subject            *string
	Filename           *string
	FilenameConfirmed  *bool
	DestDir            *string
	FileSizeLo         uint32
	FileSizeHi         uint32
	FileSizeBytes      uint64
	RemainingSizeLo    uint32
	RemainingSizeHi    uint32
	RemainingSizeBytes uint64
	Paused             *bool
	PostTime           *time.Time
	Category           *string
	Priority           *int64
	ActiveDownloads    *int64
	Progress           *int64
}

// HistoryObservation is normalized history evidence. ID is NZBGet's
// deprecated alias of NZBID and is never promoted to a second identity.
type HistoryObservation struct {
	NZBID               int64
	ID                  *int64
	CanonicalNZBID      int64
	Kind                string
	NZBFilename         string
	Name                string
	NZBName             *string
	NZBNicename         *string
	URL                 *string
	RetryData           *bool
	HistoryTime         *time.Time
	HistoryTimeUnix     *int64
	DestDir             string
	FinalDir            *string
	Category            string
	FileSizeLo          uint32
	FileSizeHi          uint32
	FileSizeBytes       uint64
	FileSizeMB          *int64
	FileCount           int64
	RemainingFileCount  int64
	MinPostTime         *time.Time
	MaxPostTime         *time.Time
	TotalArticles       int64
	SuccessArticles     int64
	FailedArticles      int64
	Health              int64
	CriticalHealth      int64
	Deleted             *bool
	DownloadedSizeLo    *uint32
	DownloadedSizeHi    *uint32
	DownloadedSizeBytes *uint64
	DownloadedSizeMB    *int64
	DownloadTimeSec     *int64
	PostTotalTimeSec    *int64
	ParTimeSec          *int64
	RepairTimeSec       *int64
	UnpackTimeSec       *int64
	MessageCount        *int64
	DupeKey             *string
	DupeScore           *int64
	DupeMode            *string
	Status              string
	ParStatus           *string
	ExParStatus         *string
	UnpackStatus        *string
	URLStatus           *string
	ScriptStatus        *string
	ScriptStatuses      []ScriptStatus
	MoveStatus          *string
	DeleteStatus        *string
	MarkStatus          *string
	ExtraParBlocks      *int64
	Parameters          []ParameterObservation
}

// Version reads and validates the upstream version string.
func (client *Client) Version(ctx context.Context) (VersionObservation, error) {
	wire, err := client.methods.Version(ctx, VersionRequest{})
	if err != nil {
		return VersionObservation{}, err
	}
	value := strings.TrimSpace(string(wire))
	if value == "" || len(value) > maxVersionLength || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return VersionObservation{}, malformed(MethodVersion)
	}
	return VersionObservation{Version: value}, nil
}

// ListGroups reads queued group summaries. NZBGet requires the deprecated log
// count argument to be zero for this compatibility contract.
func (client *Client) ListGroups(ctx context.Context, numberOfLogEntries int64) ([]GroupObservation, error) {
	if numberOfLogEntries != 0 {
		return nil, invalidInput(MethodListGroups)
	}
	wire, err := client.methods.ListGroups(ctx, ListGroupsRequest{NumberOfLogEntries: numberOfLogEntries})
	if err != nil {
		return nil, err
	}
	result := make([]GroupObservation, 0, len(wire))
	for _, item := range wire {
		result = append(result, normalizeGroup(item))
	}
	return result, nil
}

// ListFiles reads file summaries. IDFrom and IDTo are deprecated and must be
// zero; NZBID zero requests all groups.
func (client *Client) ListFiles(ctx context.Context, idFrom, idTo, nzbID int64) ([]FileObservation, error) {
	if idFrom != 0 || idTo != 0 || nzbID < 0 {
		return nil, invalidInput(MethodListFiles)
	}
	wire, err := client.methods.ListFiles(ctx, ListFilesRequest{IDFrom: idFrom, IDTo: idTo, NZBID: nzbID})
	if err != nil {
		return nil, err
	}
	result := make([]FileObservation, 0, len(wire))
	for _, item := range wire {
		result = append(result, normalizeFile(item))
	}
	return result, nil
}

// History reads visible history by default. Hidden=true is available for
// callers that explicitly need duplicate records.
func (client *Client) History(ctx context.Context, hidden bool) ([]HistoryObservation, error) {
	wire, err := client.methods.History(ctx, HistoryRequest{Hidden: hidden})
	if err != nil {
		return nil, err
	}
	result := make([]HistoryObservation, 0, len(wire))
	for _, item := range wire {
		observation, normalizeErr := normalizeHistory(item)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		result = append(result, observation)
	}
	return result, nil
}

// Invoke implements the generated Invoker interface. It accepts only the
// four documented read methods and emits the exact NZBGet 1.1 envelope.
func (client *Client) Invoke(ctx context.Context, method string, params []any, result any) error {
	if client == nil {
		return invalidInput("client")
	}
	if ctx == nil {
		return invalidInput("context")
	}
	if err := validateMethodCall(method, params); err != nil {
		return err
	}
	if method == MethodVersion {
		// A nil slice would encode as JSON null. NZBGet requires an empty
		// positional array for a no-argument call.
		params = []any{}
	}
	if result == nil {
		return invalidInput("result")
	}
	if err := ctx.Err(); err != nil {
		return contextError(method, err)
	}
	requestID, ok := client.nextRequestID()
	if !ok {
		return &UpstreamError{Kind: ErrorProtocol, Method: method, Detail: "request id space exhausted"}
	}
	requestBody, err := json.Marshal(wireRequest{Version: ProtocolVersion, Method: method, Params: params, ID: requestID})
	if err != nil {
		return malformedRequest(method)
	}
	requestContext := ctx
	cancel := func() {}
	if client.requestTimeout > 0 {
		if deadline, hasDeadline := ctx.Deadline(); !hasDeadline || time.Until(deadline) > client.requestTimeout {
			requestContext, cancel = context.WithTimeout(ctx, client.requestTimeout)
		}
	}
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, client.endpoint.String(), bytes.NewReader(requestBody))
	if err != nil {
		return malformedRequest(method)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", client.userAgent)
	if client.username != "" || client.password != "" {
		request.SetBasicAuth(client.username, client.password)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		if requestContextErr := requestContext.Err(); requestContextErr != nil {
			return contextError(method, requestContextErr)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return contextError(method, err)
		}
		if netErr, isNetErr := err.(net.Error); isNetErr && netErr.Timeout() {
			return &UpstreamError{Kind: ErrorTimeout, Method: method, Retryable: true, Detail: "upstream request timed out", cause: err}
		}
		return &UpstreamError{Kind: ErrorUnavailable, Method: method, Retryable: true, Detail: "upstream is unavailable", cause: err}
	}
	defer response.Body.Close()
	body, err := readBounded(response.Body, client.maxResponseBytes)
	if err != nil {
		return &UpstreamError{Kind: ErrorResponseTooLarge, Method: method, StatusCode: response.StatusCode, Detail: "response exceeds configured bound"}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return normalizeHTTPError(method, response.StatusCode)
	}
	var envelope wireResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		return malformed(method)
	}
	if envelope.Version != ProtocolVersion || !responseIDMatches(envelope.ID, requestID) {
		return malformed(method)
	}
	resultIsNull := len(envelope.Result) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Result), []byte("null"))
	if envelope.Error != nil {
		if !resultIsNull {
			return malformed(method)
		}
		return normalizeRPCError(method, envelope.Error)
	}
	if resultIsNull {
		return malformed(method)
	}
	if err := json.Unmarshal(envelope.Result, result); err != nil {
		return malformed(method)
	}
	return nil
}

func (client *Client) nextRequestID() (uint64, bool) {
	for {
		current := client.requestID.Load()
		if current == math.MaxUint64 {
			return 0, false
		}
		next := current + 1
		if client.requestID.CompareAndSwap(current, next) {
			return next, true
		}
	}
}

type wireRequest struct {
	Version string `json:"version"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      uint64 `json:"id"`
}

type wireResponse struct {
	Version string          `json:"version"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *wireError      `json:"error"`
}

type wireError struct {
	Code    int64  `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// ErrorKind classifies a sanitized transport or upstream failure.
type ErrorKind string

const (
	ErrorInvalidInput ErrorKind = "invalid_input"
	ErrorUnauthorized ErrorKind = "unauthorized"
	ErrorForbidden    ErrorKind = "forbidden"
	ErrorRateLimited  ErrorKind = "rate_limited"
	ErrorHTTP         ErrorKind = "http_error"
	ErrorUnavailable  ErrorKind = "unavailable"
	ErrorTimeout      ErrorKind = "timeout"
	ErrorCanceled     ErrorKind = "canceled"
	// ErrorCancelled is the British-spelling alias for ErrorCanceled.
	ErrorCancelled                  = ErrorCanceled
	ErrorMalformed        ErrorKind = "malformed_response"
	ErrorProtocol         ErrorKind = "protocol_error"
	ErrorUnsupported      ErrorKind = "unsupported"
	ErrorResponseTooLarge ErrorKind = "response_too_large"
	ErrorRemote           ErrorKind = "remote_error"
)

// UpstreamError is a typed, sanitized failure. Detail never contains the raw
// URL, credentials, response body or upstream JSON-RPC message.
type UpstreamError struct {
	Kind       ErrorKind
	Method     string
	StatusCode int
	RPCCode    int64
	Retryable  bool
	Detail     string
	cause      error
}

func (err *UpstreamError) Error() string {
	if err == nil {
		return "<nil>"
	}
	if err.Method == "" {
		return "nzbget " + string(err.Kind)
	}
	return "nzbget " + string(err.Kind) + " for " + err.Method
}

func (err *UpstreamError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

// IsKind reports whether err or one of its wrapped errors is an UpstreamError
// with the requested kind.
func IsKind(err error, kind ErrorKind) bool {
	var upstream *UpstreamError
	return errors.As(err, &upstream) && upstream.Kind == kind
}

func validateMethodCall(method string, params []any) error {
	if method == "" || len(method) > maxMethodLength || strings.IndexFunc(method, unicode.IsControl) >= 0 {
		return invalidInput("method")
	}
	switch method {
	case MethodVersion:
		if len(params) != 0 {
			return invalidInput(method)
		}
	case MethodListGroups:
		if len(params) != 1 || !isIntegerValue(params[0]) || !integerFitsInt64(params[0]) || integerValue(params[0]) != 0 {
			return invalidInput(method)
		}
	case MethodListFiles:
		if len(params) != 3 || !isIntegerValue(params[0]) || !isIntegerValue(params[1]) || !isIntegerValue(params[2]) || !integerFitsInt64(params[0]) || !integerFitsInt64(params[1]) || !integerFitsInt64(params[2]) || integerValue(params[0]) != 0 || integerValue(params[1]) != 0 || integerValue(params[2]) < 0 {
			return invalidInput(method)
		}
	case MethodHistory:
		if len(params) != 1 {
			return invalidInput(method)
		}
		if _, ok := params[0].(bool); !ok {
			return invalidInput(method)
		}
	default:
		return invalidInput(method)
	}
	return nil
}

func isIntegerValue(value any) bool {
	switch value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return true
	default:
		return false
	}
}

func integerFitsInt64(value any) bool {
	switch number := value.(type) {
	case uint:
		return uint64(number) <= math.MaxInt64
	case uint64:
		return number <= math.MaxInt64
	case json.Number:
		_, err := number.Int64()
		return err == nil
	default:
		return true
	}
}

func integerValue(value any) int64 {
	switch number := value.(type) {
	case int:
		return int64(number)
	case int8:
		return int64(number)
	case int16:
		return int64(number)
	case int32:
		return int64(number)
	case int64:
		return number
	case uint:
		if uint64(number) > math.MaxInt64 {
			return math.MaxInt64
		}
		return int64(number)
	case uint8:
		return int64(number)
	case uint16:
		return int64(number)
	case uint32:
		return int64(number)
	case uint64:
		if number > math.MaxInt64 {
			return math.MaxInt64
		}
		return int64(number)
	case json.Number:
		parsed, err := number.Int64()
		if err != nil {
			return math.MaxInt64
		}
		return parsed
	default:
		return math.MaxInt64
	}
}

func parseEndpoint(value string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("nzbget endpoint must be an absolute HTTP URL without credentials, query or fragment")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/jsonrpc"
	}
	parsed.RawPath = ""
	return parsed, nil
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes < 1 || maxBytes == math.MaxInt64 {
		return nil, errors.New("response bound is invalid")
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, errors.New("response could not be read")
	}
	if int64(len(body)) > maxBytes {
		return nil, errors.New("response exceeds bound")
	}
	return body, nil
}

func responseIDMatches(value json.RawMessage, expected uint64) bool {
	if len(value) == 0 {
		return false
	}
	var actual uint64
	return json.Unmarshal(value, &actual) == nil && actual == expected
}

func normalizeHTTPError(method string, status int) error {
	kind := ErrorHTTP
	retryable := false
	switch status {
	case http.StatusUnauthorized:
		kind = ErrorUnauthorized
	case http.StatusForbidden:
		kind = ErrorForbidden
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		kind, retryable = ErrorRateLimited, true
	default:
		if status >= 500 {
			kind, retryable = ErrorUnavailable, true
		}
	}
	return &UpstreamError{Kind: kind, Method: method, StatusCode: status, Retryable: retryable, Detail: "upstream HTTP request failed"}
}

func normalizeRPCError(method string, upstream *wireError) error {
	kind := ErrorRemote
	retryable := false
	switch upstream.Code {
	case -32600, -32602:
		kind = ErrorInvalidInput
	case -32601:
		kind = ErrorUnsupported
	case -32000, -32001, -32002:
		kind, retryable = ErrorUnavailable, true
	}
	return &UpstreamError{Kind: kind, Method: method, RPCCode: upstream.Code, Retryable: retryable, Detail: "JSON-RPC method failed"}
}

func contextError(method string, cause error) error {
	if errors.Is(cause, context.Canceled) {
		return &UpstreamError{Kind: ErrorCanceled, Method: method, Detail: "request canceled", cause: context.Canceled}
	}
	return &UpstreamError{Kind: ErrorTimeout, Method: method, Retryable: true, Detail: "request deadline exceeded", cause: context.DeadlineExceeded}
}

func invalidInput(method string) error {
	return &UpstreamError{Kind: ErrorInvalidInput, Method: method, Detail: "request input is invalid"}
}

func malformed(method string) error {
	return &UpstreamError{Kind: ErrorMalformed, Method: method, Detail: "upstream response is malformed"}
}

func malformedRequest(method string) error {
	return &UpstreamError{Kind: ErrorProtocol, Method: method, Detail: "request could not be encoded"}
}

func normalizeGroup(item GroupRecord) GroupObservation {
	return GroupObservation{
		NZBID: item.NZBID, ID: item.ID, FirstID: item.FirstID, LastID: item.LastID,
		NZBFilename: item.NZBFilename, NZBName: item.NZBName, NZBNicename: item.NZBNicename,
		Kind: item.Kind, URL: sanitizeOptionalURL(item.URL), DestDir: item.DestDir, FinalDir: item.FinalDir,
		Category: item.Category, FileSizeLo: item.FileSizeLo, FileSizeHi: item.FileSizeHi,
		FileSizeBytes: combine(item.FileSizeHi, item.FileSizeLo), RemainingSizeLo: item.RemainingSizeLo,
		RemainingSizeHi: item.RemainingSizeHi, RemainingSizeBytes: combine(item.RemainingSizeHi, item.RemainingSizeLo),
		PausedSizeLo: item.PausedSizeLo, PausedSizeHi: item.PausedSizeHi, PausedSizeBytes: combine(item.PausedSizeHi, item.PausedSizeLo),
		FileSizeMB: item.FileSizeMB, RemainingSizeMB: item.RemainingSizeMB, PausedSizeMB: item.PausedSizeMB,
		FileCount: item.FileCount, RemainingFileCount: item.RemainingFileCount, RemainingParCount: item.RemainingParCount,
		MinPostTime: unixTime(item.MinPostTime), MaxPostTime: unixTime(item.MaxPostTime), MinPriority: item.MinPriority, MaxPriority: item.MaxPriority,
		ActiveDownloads: item.ActiveDownloads, Status: item.Status, TotalArticles: item.TotalArticles, SuccessArticles: item.SuccessArticles,
		FailedArticles: item.FailedArticles, Health: item.Health, CriticalHealth: item.CriticalHealth,
		DownloadedSizeLo: item.DownloadedSizeLo, DownloadedSizeHi: item.DownloadedSizeHi, DownloadedSizeBytes: combineOptional(item.DownloadedSizeHi, item.DownloadedSizeLo),
		StatusDetails: GroupStatusDetails{ParStatus: item.ParStatus, UnpackStatus: item.UnpackStatus, ExParStatus: item.ExParStatus,
			MoveStatus: item.MoveStatus, ScriptStatus: item.ScriptStatus, DeleteStatus: item.DeleteStatus, URLStatus: item.UrlStatus,
			MarkStatus: item.MarkStatus, ScriptStatuses: append([]ScriptStatus(nil), item.ScriptStatuses...), PostTotalTimeSec: item.PostTotalTimeSec,
			PostStageTimeSec: item.PostStageTimeSec, ParTimeSec: item.ParTimeSec, RepairTimeSec: item.RepairTimeSec,
			UnpackTimeSec: item.UnpackTimeSec, Deleted: item.Deleted},
		Parameters: normalizeParameters(item.Parameters), PostInfoText: item.PostInfoText, PostStageProgress: item.PostStageProgress,
	}
}

func normalizeFile(item FileRecord) FileObservation {
	return FileObservation{
		ID: item.ID, NZBID: item.NZBID, NZBFilename: item.NZBFilename, NZBName: item.NZBName, NZBNicename: item.NZBNicename,
		Subject: item.Subject, Filename: item.Filename, FilenameConfirmed: item.FilenameConfirmed, DestDir: item.DestDir,
		FileSizeLo: item.FileSizeLo, FileSizeHi: item.FileSizeHi, FileSizeBytes: combine(item.FileSizeHi, item.FileSizeLo),
		RemainingSizeLo: item.RemainingSizeLo, RemainingSizeHi: item.RemainingSizeHi, RemainingSizeBytes: combine(item.RemainingSizeHi, item.RemainingSizeLo),
		Paused: item.Paused, PostTime: unixTime(item.PostTime), Category: item.Category, Priority: item.Priority,
		ActiveDownloads: item.ActiveDownloads, Progress: item.Progress,
	}
}

func normalizeHistory(item HistoryRecord) (HistoryObservation, error) {
	canonical, err := canonicalNZBID(item.NZBID, item.ID)
	if err != nil {
		return HistoryObservation{}, err
	}
	return HistoryObservation{
		NZBID: item.NZBID, ID: item.ID, CanonicalNZBID: canonical, Kind: item.Kind, NZBFilename: item.NZBFilename,
		Name: item.Name, NZBName: item.NZBName, NZBNicename: item.NZBNicename, URL: sanitizeOptionalURL(item.URL), RetryData: item.RetryData,
		HistoryTime: unixTime(item.HistoryTime), HistoryTimeUnix: item.HistoryTime, DestDir: item.DestDir, FinalDir: item.FinalDir,
		Category: item.Category, FileSizeLo: item.FileSizeLo, FileSizeHi: item.FileSizeHi, FileSizeBytes: combine(item.FileSizeHi, item.FileSizeLo),
		FileSizeMB: item.FileSizeMB, FileCount: item.FileCount, RemainingFileCount: item.RemainingFileCount,
		MinPostTime: unixTime(item.MinPostTime), MaxPostTime: unixTime(item.MaxPostTime), TotalArticles: item.TotalArticles,
		SuccessArticles: item.SuccessArticles, FailedArticles: item.FailedArticles, Health: item.Health, CriticalHealth: item.CriticalHealth,
		Deleted: item.Deleted, DownloadedSizeLo: item.DownloadedSizeLo, DownloadedSizeHi: item.DownloadedSizeHi,
		DownloadedSizeBytes: combineOptional(item.DownloadedSizeHi, item.DownloadedSizeLo), DownloadedSizeMB: item.DownloadedSizeMB,
		DownloadTimeSec: item.DownloadTimeSec, PostTotalTimeSec: item.PostTotalTimeSec, ParTimeSec: item.ParTimeSec,
		RepairTimeSec: item.RepairTimeSec, UnpackTimeSec: item.UnpackTimeSec, MessageCount: item.MessageCount, DupeKey: item.DupeKey,
		DupeScore: item.DupeScore, DupeMode: item.DupeMode, Status: item.Status, ParStatus: item.ParStatus, ExParStatus: item.ExParStatus,
		UnpackStatus: item.UnpackStatus, URLStatus: item.UrlStatus, ScriptStatus: item.ScriptStatus, ScriptStatuses: append([]ScriptStatus(nil), item.ScriptStatuses...),
		MoveStatus: item.MoveStatus, DeleteStatus: item.DeleteStatus, MarkStatus: item.MarkStatus, ExtraParBlocks: item.ExtraParBlocks,
		Parameters: normalizeParameters(item.Parameters),
	}, nil
}

func canonicalNZBID(nzbID int64, alias *int64) (int64, error) {
	if nzbID < 0 || alias != nil && *alias < 0 {
		return 0, malformed(MethodHistory)
	}
	if nzbID > 0 && alias != nil && *alias > 0 && nzbID != *alias {
		return 0, malformed(MethodHistory)
	}
	if nzbID > 0 {
		return nzbID, nil
	}
	if alias != nil && *alias > 0 {
		return *alias, nil
	}
	return 0, nil
}

func normalizeParameters(values []Parameter) []ParameterObservation {
	if len(values) == 0 {
		return nil
	}
	result := make([]ParameterObservation, 0, len(values))
	for _, value := range values {
		var copyValue *string
		if value.Value != nil {
			candidate := *value.Value
			copyValue = &candidate
		}
		result = append(result, ParameterObservation{Name: value.Name, Value: copyValue})
	}
	return result
}

func combine(hi, lo uint32) uint64 {
	return uint64(hi)<<32 | uint64(lo)
}

func combineOptional(hi, lo *uint32) *uint64 {
	if hi == nil || lo == nil {
		return nil
	}
	value := combine(*hi, *lo)
	return &value
}

func unixTime(value *int64) *time.Time {
	if value == nil || *value <= 0 {
		return nil
	}
	result := time.Unix(*value, 0).UTC()
	if result.Year() < 1970 || result.Year() > 9999 {
		return nil
	}
	return &result
}

func sanitizeOptionalURL(value *string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(*value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil
	}
	parsed.User = nil
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.RawFragment = ""
	parsed.Opaque = ""
	parsed.ForceQuery = false
	clean := parsed.String()
	if clean == "" || len(clean) > maxErrorDetailLength || strings.IndexFunc(clean, unicode.IsControl) >= 0 {
		return nil
	}
	return &clean
}

// Compile-time guard: the transport remains the only implementation of the
// generated Invoker boundary.
var _ Invoker = (*Client)(nil)
