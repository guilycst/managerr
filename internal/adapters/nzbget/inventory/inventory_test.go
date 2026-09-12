package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

type rpcFixtureCall struct {
	Version string
	Method  string
	Params  []json.RawMessage
}

// rpcError is fixture-only wire data. Production transport and envelope
// handling belong to the standalone NZBGet client.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcFixtureHandler struct {
	mu sync.Mutex

	queue              []byte
	history            []byte
	version            []byte
	queueResults       [][]byte
	historyResults     [][]byte
	queueResultIndex   int
	historyResultIndex int
	status             map[string]int
	malformed          map[string]bool
	requireAuth        bool
	username           string
	password           string
	blockMethod        string
	blockDone          chan struct{}
	responseJSONRPC    string
	responseVersion    string
	responseID         json.RawMessage
	responseError      *rpcError
	calls              []rpcFixtureCall
	methods            []string
}

func (handler *rpcFixtureHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if handler.requireAuth {
		username, password, ok := request.BasicAuth()
		if !ok || username != handler.username || password != handler.password {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	var incoming struct {
		Version string            `json:"version"`
		Method  string            `json:"method"`
		Params  []json.RawMessage `json:"params"`
		ID      json.RawMessage   `json:"id"`
	}
	if err := json.NewDecoder(request.Body).Decode(&incoming); err != nil {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	handler.mu.Lock()
	handler.methods = append(handler.methods, incoming.Method)
	handler.calls = append(handler.calls, rpcFixtureCall{Version: incoming.Version, Method: incoming.Method, Params: append([]json.RawMessage(nil), incoming.Params...)})
	status := handler.status[incoming.Method]
	malformed := handler.malformed[incoming.Method]
	var result []byte
	switch incoming.Method {
	case "listgroups":
		result = handler.queue
		if handler.queueResultIndex < len(handler.queueResults) {
			result = handler.queueResults[handler.queueResultIndex]
			handler.queueResultIndex++
		}
	case "history":
		result = handler.history
		if handler.historyResultIndex < len(handler.historyResults) {
			result = handler.historyResults[handler.historyResultIndex]
			handler.historyResultIndex++
		}
	case "version":
		result = handler.version
	default:
		status = http.StatusNotFound
	}
	block := handler.blockMethod == incoming.Method
	blockDone := handler.blockDone
	handler.mu.Unlock()
	if block && blockDone != nil {
		select {
		case <-request.Context().Done():
			return
		case <-blockDone:
		}
	}
	if status != 0 {
		response.WriteHeader(status)
		return
	}
	if malformed {
		_, _ = response.Write([]byte("{"))
		return
	}
	if result == nil {
		result = []byte("null")
	}
	response.Header().Set("Content-Type", "application/json")
	version := handler.responseVersion
	if version == "" {
		version = "1.1"
	}
	responseID := handler.responseID
	if len(responseID) == 0 {
		responseID = incoming.ID
	}
	if handler.responseJSONRPC != "" {
		_, _ = response.Write([]byte(`{"jsonrpc":"` + handler.responseJSONRPC + `","id":`))
	} else {
		_, _ = response.Write([]byte(`{"version":"` + version + `","id":`))
	}
	_, _ = response.Write(responseID)
	if handler.responseError != nil {
		errorBody, _ := json.Marshal(handler.responseError)
		_, _ = response.Write([]byte(`,"error":`))
		_, _ = response.Write(errorBody)
		_, _ = response.Write([]byte("}"))
		return
	}
	_, _ = response.Write([]byte(`,"result":`))
	_, _ = response.Write(result)
	_, _ = response.Write([]byte("}"))
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../../../tests/fixtures/nzbget", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func newFixtureClient(t *testing.T, handler *rpcFixtureHandler, connectionID domain.ConfigID, configure func(*Config)) (*Client, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	config := Config{
		ConnectionID: connectionID,
		Endpoint:     server.URL,
		Username:     "fixture-user",
		Password:     "fixture-password",
		Mappings: []domain.PathMapping{{
			ConnectionID:      connectionID,
			SourcePrefix:      "/downloads",
			RootID:            "library",
			DestinationPrefix: "managed",
		}},
	}
	if configure != nil {
		configure(&config)
	}
	client, err := New(config)
	if err != nil {
		server.Close()
		t.Fatalf("New: %v", err)
	}
	return client, server.Close
}

func rpcResult(t *testing.T, value any) []byte {
	t.Helper()
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal RPC result: %v", err)
	}
	return result
}

func assertErrorCode(t *testing.T, err error, want domain.UpstreamErrorCode) domain.UpstreamError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error", want)
	}
	var upstream domain.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("error %T does not carry upstream evidence: %v", err, err)
	}
	if upstream.Code != want {
		t.Fatalf("error code = %s, want %s", upstream.Code, want)
	}
	return upstream
}

func hasReason(coverage domain.Coverage, want string) bool {
	for _, reason := range coverage.ReasonCodes {
		if reason == want {
			return true
		}
	}
	return false
}

func TestListQueueHistoryCorrelationAndMapping(t *testing.T) {
	handler := &rpcFixtureHandler{
		queue:       fixture(t, "queue.json"),
		history:     fixture(t, "history-provenance.json"),
		version:     fixture(t, "version.json"),
		requireAuth: true,
		username:    "fixture-user",
		password:    "fixture-password",
		status:      make(map[string]int),
		malformed:   make(map[string]bool),
	}
	client, closeServer := newFixtureClient(t, handler, "nzb-main", nil)
	defer closeServer()

	page, err := client.ListDetailed(context.Background(), "nzb-main", "", 10)
	if err != nil {
		t.Fatalf("ListDetailed: %v", err)
	}
	if len(page.Items) != 3 || page.NextCursor != "" || page.Coverage.Completeness != domain.CompletenessComplete || page.Coverage.ObservedCount != 3 {
		t.Fatalf("page = %#v", page)
	}
	queue := page.Items[0]
	if queue.NZBID != 77 || queue.Item.ExternalID != "77" || queue.Item.State != "queued" || queue.Item.ProcessingDone || queue.ArrDownloadID != "arr-series-77" || queue.MappedPath == nil || queue.MappedPath.RelativePath != "managed/tv" || queue.History != nil {
		t.Fatalf("queue observation = %#v", queue)
	}
	processing := page.Items[1]
	if processing.NZBID != 78 || processing.Item.State != "completed" || !processing.Item.ProcessingDone || processing.ArrDownloadID != "78" || processing.MappedPath == nil || processing.MappedPath.RelativePath != "managed/movies/Example Processing" || processing.Item.Progress != 1 || processing.Queue == nil || processing.Queue.PostInfoText != "" {
		t.Fatalf("post-processing observation = %#v, mapped=%+v", processing, processing.MappedPath)
	}
	history := page.Items[2]
	if history.NZBID != 42 || history.HistoryID != 42 || history.Item.ExternalID != "42" || history.ArrDownloadID != "arr-download-42" || history.Item.State != "completed" || !history.Item.ProcessingDone || history.History == nil || history.MappedPath == nil || history.MappedPath.RelativePath != "managed/movies/Example Film (2024)" || history.Item.CompletedAt == nil {
		t.Fatalf("history observation = %#v", history)
	}
	if history.Item.Payload != nil {
		t.Fatalf("NZBGet history fabricated exact payload: %#v", history.Item.Payload)
	}
	if err := page.Coverage.Validate(); err != nil {
		t.Fatalf("coverage validation: %v", err)
	}

	handler.mu.Lock()
	defer handler.mu.Unlock()
	if len(handler.calls) != 2 || handler.calls[0].Method != "listgroups" || handler.calls[1].Method != "history" {
		t.Fatalf("RPC calls = %#v", handler.calls)
	}
	if len(handler.calls[0].Params) != 1 || string(handler.calls[0].Params[0]) != "0" || len(handler.calls[1].Params) != 1 || string(handler.calls[1].Params[0]) != "false" {
		t.Fatalf("RPC params = %#v", handler.calls)
	}
	for _, method := range handler.methods {
		if method != "listgroups" && method != "history" {
			t.Fatalf("unexpected mutating or unsupported method %q", method)
		}
	}
}

func TestQueueHistoryMergeRetainsHistoryProvenance(t *testing.T) {
	queue := []map[string]any{{
		"NZBID": 501, "ID": 501, "Kind": "NZB", "NZBFilename": "/incoming/Merged.nzb", "NZBName": "Merged",
		"DestDir": "/downloads", "Status": "PP_FINISHED",
	}}
	history := []map[string]any{{
		"NZBID": 501, "ID": 501, "Kind": "NZB", "NZBFilename": "/incoming/Merged.nzb", "NZBName": "Merged",
		"Name": "Merged", "DestDir": "/downloads", "FinalDir": "/downloads/movies/Merged", "HistoryTime": 1731144400,
		"Status": "SUCCESS/ALL", "ParStatus": "SUCCESS", "UnpackStatus": "SUCCESS", "ScriptStatus": "SUCCESS", "MoveStatus": "SUCCESS",
		"Parameters": []map[string]any{{"Name": "drone", "Value": "arr-merged-501"}},
	}}
	handler := &rpcFixtureHandler{queue: rpcResult(t, queue), history: rpcResult(t, history), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-merge", nil)
	defer closeServer()
	page, err := client.ListDetailed(context.Background(), "nzb-merge", "", 1)
	if err != nil || len(page.Items) != 1 || page.Coverage.Completeness != domain.CompletenessComplete {
		t.Fatalf("merged page = %#v, %v", page, err)
	}
	item := page.Items[0]
	if item.History == nil || item.Drone != "arr-merged-501" || item.ArrDownloadID != "arr-merged-501" || item.FinalDir != "/downloads/movies/Merged" || item.ContentPath != item.FinalDir || item.MappedPath == nil || item.MappedPath.RelativePath != "managed/movies/Merged" || item.Item.CompletedAt == nil {
		t.Fatalf("merged provenance = %#v", item)
	}
}

func TestDuplicateHistoryIDsRemainPartial(t *testing.T) {
	history := []map[string]any{
		{"NZBID": 601, "ID": 601, "Kind": "NZB", "NZBName": "Duplicate", "DestDir": "/downloads", "Status": "SUCCESS/ALL"},
		{"NZBID": 601, "ID": 601, "Kind": "NZB", "NZBName": "Duplicate", "DestDir": "/downloads", "Status": "SUCCESS/ALL"},
	}
	handler := &rpcFixtureHandler{queue: fixture(t, "empty.json"), history: rpcResult(t, history), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-duplicate", nil)
	defer closeServer()
	page, err := client.ListDetailed(context.Background(), "nzb-duplicate", "", 1)
	if err != nil || len(page.Items) != 1 || page.Coverage.Completeness != domain.CompletenessPartial || !hasReason(page.Coverage, "history_duplicate_id") {
		t.Fatalf("duplicate history = %#v, %v", page, err)
	}
}

func TestVersionAndCapabilities(t *testing.T) {
	handler := &rpcFixtureHandler{version: fixture(t, "version.json"), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-main", nil)
	defer closeServer()

	version, err := client.Version(context.Background(), "nzb-main")
	if err != nil || version.Version != "24.2.1" || version.ConnectionID != "nzb-main" {
		t.Fatalf("version = %#v, %v", version, err)
	}
	capabilities, err := client.Capabilities(context.Background(), "nzb-main")
	if err != nil || len(capabilities) != 4 {
		t.Fatalf("capabilities = %#v, %v", capabilities, err)
	}
	for _, capability := range capabilities {
		if err := capability.Validate(); err != nil {
			t.Fatalf("capability %s validation: %v", capability.Name, err)
		}
	}
	if capabilities[0].State != domain.CapabilitySupported || capabilities[1].Name != "nzbget.post_processing" || capabilities[3].State != domain.CapabilityUnknown {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

func TestRPCEnvelopeAndParameterEvidence(t *testing.T) {
	badEnvelope := &rpcFixtureHandler{
		queue:      fixture(t, "empty.json"),
		history:    fixture(t, "empty.json"),
		version:    fixture(t, "version.json"),
		status:     map[string]int{},
		malformed:  map[string]bool{},
		responseID: json.RawMessage(`999`),
	}
	badClient, closeBad := newFixtureClient(t, badEnvelope, "nzb-envelope", nil)
	defer closeBad()
	_, err := badClient.Version(context.Background(), "nzb-envelope")
	assertErrorCode(t, err, domain.OutcomeUnknown)

	queue := []map[string]any{{
		"NZBID": 91, "NZBName": "Malformed Parameter", "Status": "QUEUED", "DestDir": "/downloads",
		"FileSizeLo": 1, "RemainingSizeLo": 1,
		"Parameters": []map[string]any{{"Name": "drone", "Value": map[string]any{"unexpected": true}}},
	}}
	handler := &rpcFixtureHandler{queue: rpcResult(t, queue), history: fixture(t, "empty.json"), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-parameters", nil)
	defer closeServer()
	page, err := client.ListDetailed(context.Background(), "nzb-parameters", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].ArrDownloadID != "" || !hasReason(page.Coverage, "item_0_parameters_malformed") || page.Coverage.Completeness != domain.CompletenessPartial {
		t.Fatalf("malformed parameter evidence = %#v, %v", page, err)
	}
}

func TestJSONRPC11EnvelopeAndRequest(t *testing.T) {
	handler := &rpcFixtureHandler{version: fixture(t, "version.json"), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-rpc11", nil)
	defer closeServer()
	version, err := client.Version(context.Background(), "nzb-rpc11")
	if err != nil || version.Version != "24.2.1" {
		t.Fatalf("JSON-RPC 1.1 version = %#v, %v", version, err)
	}
	handler.mu.Lock()
	if len(handler.calls) != 1 || handler.calls[0].Version != "1.1" || handler.calls[0].Method != "version" || len(handler.calls[0].Params) != 0 {
		t.Fatalf("JSON-RPC 1.1 request = %#v", handler.calls)
	}
	handler.mu.Unlock()

	errorHandler := &rpcFixtureHandler{
		version:       fixture(t, "version.json"),
		status:        map[string]int{},
		malformed:     map[string]bool{},
		responseError: &rpcError{Code: -32601, Message: "synthetic method unavailable"},
	}
	errorClient, closeError := newFixtureClient(t, errorHandler, "nzb-rpc11-error", nil)
	defer closeError()
	assertErrorCode(t, func() error {
		_, callErr := errorClient.Version(context.Background(), "nzb-rpc11-error")
		return callErr
	}(), domain.OutcomeUnsupported)

	wrongVersion := &rpcFixtureHandler{
		version:         fixture(t, "version.json"),
		status:          map[string]int{},
		malformed:       map[string]bool{},
		responseJSONRPC: "2.0",
	}
	wrongClient, closeWrong := newFixtureClient(t, wrongVersion, "nzb-rpc11-wrong", nil)
	defer closeWrong()
	assertErrorCode(t, func() error {
		_, callErr := wrongClient.Version(context.Background(), "nzb-rpc11-wrong")
		return callErr
	}(), domain.OutcomeUnknown)
}

func TestStandaloneErrorClassificationTranslation(t *testing.T) {
	notImplementedHandler := &rpcFixtureHandler{
		version:   fixture(t, "version.json"),
		status:    map[string]int{"version": http.StatusNotImplemented},
		malformed: map[string]bool{},
	}
	notImplementedClient, closeNotImplemented := newFixtureClient(t, notImplementedHandler, "nzb-http-501", nil)
	defer closeNotImplemented()
	notImplemented := assertErrorCode(t, func() error {
		_, callErr := notImplementedClient.Version(context.Background(), "nzb-http-501")
		return callErr
	}(), domain.OutcomeUnsupported)
	if notImplemented.Status != http.StatusNotImplemented || notImplemented.Retryable {
		t.Fatalf("HTTP 501 classification = %#v", notImplemented)
	}

	zeroCodeHandler := &rpcFixtureHandler{
		version:       fixture(t, "version.json"),
		status:        map[string]int{},
		malformed:     map[string]bool{},
		responseError: &rpcError{Code: 0, Message: "synthetic remote failure"},
	}
	zeroCodeClient, closeZeroCode := newFixtureClient(t, zeroCodeHandler, "nzb-rpc-zero", nil)
	defer closeZeroCode()
	zeroCode := assertErrorCode(t, func() error {
		_, callErr := zeroCodeClient.Version(context.Background(), "nzb-rpc-zero")
		return callErr
	}(), domain.OutcomeUnknown)
	if zeroCode.UpstreamID != "0" {
		t.Fatalf("JSON-RPC code zero identity = %#v", zeroCode)
	}
}

func TestFinalDirMergeReplacesFallbackMappingAndReasonIndex(t *testing.T) {
	queue := []map[string]any{{
		"NZBID": 501, "ID": 501, "Kind": "NZB", "NZBName": "Merged Final Directory",
		"DestDir": "/downloads/incoming", "Status": "QUEUED",
	}}
	history := []map[string]any{{
		"NZBID": 501, "ID": 501, "Kind": "NZB", "NZBName": "Merged Final Directory",
		"DestDir": "/downloads/incoming", "FinalDir": "/outside/final", "Status": "SUCCESS/ALL",
	}}
	handler := &rpcFixtureHandler{queue: rpcResult(t, queue), history: rpcResult(t, history), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-final-merge", nil)
	defer closeServer()
	page, err := client.ListDetailed(context.Background(), "nzb-final-merge", "", 1)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("merged final directory = %#v, %v", page, err)
	}
	item := page.Items[0]
	if item.ContentPath != "/outside/final" || item.FinalDir != "/outside/final" || item.MappedPath != nil {
		t.Fatalf("stale fallback mapping retained = %#v", item)
	}
	if !hasReason(page.Coverage, "item_0_final_path_unmapped") || hasReason(page.Coverage, "item_1_final_path_unmapped") {
		t.Fatalf("final directory reason index = %#v", page.Coverage.ReasonCodes)
	}
}

func TestTraversalBearingPathsRemainUnmapped(t *testing.T) {
	handler := &rpcFixtureHandler{queue: fixture(t, "empty.json"), history: fixture(t, "empty.json"), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-path-safety", nil)
	defer closeServer()
	for _, remote := range []string{
		"/downloads/../secret/movie.mkv",
		"/downloads/./movie.mkv",
		"//downloads/movie.mkv",
		"/downloads//movie.mkv",
		"\\downloads\\movie.mkv",
		"downloads/movie.mkv",
	} {
		if _, ok, ambiguous := client.mapPath(remote); ok || ambiguous {
			t.Errorf("unsafe remote path mapped: %q", remote)
		}
	}
	for _, prefix := range []string{"/downloads/../secret", "/downloads/./movies", "/downloads//movies"} {
		config := Config{
			ConnectionID: "nzb-path-safety", Endpoint: "https://example.test",
			Mappings: []domain.PathMapping{{ConnectionID: "nzb-path-safety", SourcePrefix: prefix, RootID: "library"}},
		}
		if _, err := New(config); err == nil {
			t.Errorf("unsafe source prefix accepted: %q", prefix)
		}
	}

	queue := []map[string]any{{"NZBID": 801, "NZBName": "Traversal", "Status": "QUEUED", "FinalDir": "/downloads/../secret"}}
	unsafeHandler := &rpcFixtureHandler{queue: rpcResult(t, queue), history: fixture(t, "empty.json"), status: map[string]int{}, malformed: map[string]bool{}}
	unsafeClient, closeUnsafe := newFixtureClient(t, unsafeHandler, "nzb-path-safety-observed", nil)
	defer closeUnsafe()
	page, err := unsafeClient.ListDetailed(context.Background(), "nzb-path-safety-observed", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].MappedPath != nil || !hasReason(page.Coverage, "item_0_final_path_unsafe") {
		t.Fatalf("unsafe observed final directory = %#v, %v", page, err)
	}
}

func TestNumericDroneDoesNotFabricateArrIdentity(t *testing.T) {
	queue := []map[string]any{{
		"NZBID": 902, "NZBName": "Numeric Drone", "Status": "QUEUED", "DestDir": "/downloads",
		"Parameters": []map[string]any{{"Name": "drone", "Value": 12345}},
	}}
	handler := &rpcFixtureHandler{queue: rpcResult(t, queue), history: fixture(t, "empty.json"), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-drone-type", nil)
	defer closeServer()
	page, err := client.ListDetailed(context.Background(), "nzb-drone-type", "", 1)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("numeric drone page = %#v, %v", page, err)
	}
	item := page.Items[0]
	if item.Drone != "" || item.ArrDownloadID != "" || !hasReason(page.Coverage, "item_0_parameters_malformed") || len(item.Queue.Parameters) != 0 {
		t.Fatalf("numeric drone correlation = %#v, coverage=%#v", item, page.Coverage)
	}
}

func TestDetailedSecretEvidenceIsRedacted(t *testing.T) {
	const (
		password = "synthetic-secret-password"
		token    = "synthetic-secret-token"
		drone    = "password=opaque-drone-secret"
		opaque   = "opaque-pwd-value"
	)
	queue := []map[string]any{{
		"NZBID": 903, "NZBName": "Redacted Queue", "Status": "QUEUED", "DestDir": "/downloads",
		"PostInfoText": opaque,
		"Parameters": []map[string]any{
			{"Name": "*Unpack:Password", "Value": password},
			{"Name": "drone", "Value": drone},
			{"Name": "pwd", "Value": opaque},
			{"Name": "public", "Value": "token=" + token},
		},
	}}
	history := []map[string]any{{
		"NZBID": 903, "ID": 903, "Kind": "NZB", "NZBName": "Redacted History", "Name": "Redacted History",
		"Status": "SUCCESS/ALL", "URL": "https://" + opaque + ":" + opaque + "@example.test/" + opaque + "?apikey=" + opaque + "#" + opaque,
		"Parameters": []map[string]any{{"Name": "ApiKey", "Value": token}, {"Name": "source", "Value": "manual"}},
	}}
	handler := &rpcFixtureHandler{queue: rpcResult(t, queue), history: rpcResult(t, history), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-redaction", nil)
	defer closeServer()
	page, err := client.ListDetailed(context.Background(), "nzb-redaction", "", 1)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("redaction page = %#v, %v", page, err)
	}
	item := page.Items[0]
	if item.Queue == nil || item.History == nil || item.Queue.PostInfoText != "[redacted]" || item.History.URL != "https://example.test" {
		t.Fatalf("redacted detail fields = %#v", item)
	}
	if item.Drone != "" || item.ArrDownloadID != "" {
		t.Fatalf("secret-bearing drone was used as correlation: %#v", item)
	}
	for _, parameter := range append(item.Queue.Parameters, item.History.Parameters...) {
		if parameter.Value == password || parameter.Value == token || parameter.Value == drone || parameter.Value == opaque || strings.Contains(parameter.Value, password) || strings.Contains(parameter.Value, token) || strings.Contains(parameter.Value, opaque) {
			t.Fatalf("secret parameter leaked: %#v", parameter)
		}
	}
	formatted := fmt.Sprintf("%#v", page)
	if strings.Contains(formatted, password) || strings.Contains(formatted, token) || strings.Contains(formatted, drone) || strings.Contains(formatted, opaque) {
		t.Fatalf("secret detailed evidence leaked: %#v", page)
	}
}

func TestDroneParameterNameSemanticsAreExact(t *testing.T) {
	queue := []map[string]any{
		{"NZBID": 910, "NZBName": "Uppercase Drone", "Status": "QUEUED", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": "DRONE", "Value": "wrong-case"}}},
		{"NZBID": 911, "NZBName": "Spaced Drone", "Status": "QUEUED", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": " drone", "Value": "wrong-space"}}},
		{"NZBID": 912, "NZBName": "Mixed Drone", "Status": "QUEUED", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": "DrOnE", "Value": "wrong-mixed"}}},
		{"NZBID": 913, "NZBName": "Duplicate Equal Drone", "Status": "QUEUED", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": "drone", "Value": "arr-equal"}, {"Name": "drone", "Value": "arr-equal"}}},
		{"NZBID": 914, "NZBName": "Duplicate Conflict Drone", "Status": "QUEUED", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": "drone", "Value": "arr-first"}, {"Name": "drone", "Value": "arr-second"}}},
		{"NZBID": 919, "NZBName": "Pinned Arr Drone", "Status": "QUEUED", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": "drone", "Value": "0123456789abcdef0123456789abcdef"}}},
		{"NZBID": 920, "NZBName": "Empty Drone", "Status": "QUEUED", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": "drone", "Value": ""}}},
	}
	history := []map[string]any{
		{"NZBID": 915, "ID": 915, "Kind": "NZB", "NZBName": "History Uppercase Drone", "Status": "SUCCESS/ALL", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": "DRONE", "Value": "history-wrong-case"}}},
		{"NZBID": 916, "ID": 916, "Kind": "NZB", "NZBName": "History Exact Drone", "Status": "SUCCESS/ALL", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": "drone", "Value": "arr-history"}}},
		{"NZBID": 921, "ID": 921, "Kind": "NZB", "NZBName": "History Duplicate Equal Drone", "Status": "SUCCESS/ALL", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": "drone", "Value": "0123456789abcdef0123456789abcdef"}, {"Name": "drone", "Value": "0123456789abcdef0123456789abcdef"}}},
		{"NZBID": 922, "ID": 922, "Kind": "NZB", "NZBName": "History Empty Drone", "Status": "SUCCESS/ALL", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": "drone", "Value": ""}}},
	}
	handler := &rpcFixtureHandler{queue: rpcResult(t, queue), history: rpcResult(t, history), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-drone-name", nil)
	defer closeServer()
	page, err := client.ListDetailed(context.Background(), "nzb-drone-name", "", 20)
	if err != nil {
		t.Fatalf("exact drone page: %v", err)
	}
	for _, want := range []struct {
		id      int64
		arrID   string
		drone   string
		partial bool
	}{
		{id: 910, arrID: "910"},
		{id: 911, arrID: "911"},
		{id: 912, arrID: "912"},
		{id: 913, partial: true},
		{id: 914, partial: true},
		{id: 915, arrID: "915"},
		{id: 916, arrID: "arr-history", drone: "arr-history"},
		{id: 919, arrID: "0123456789abcdef0123456789abcdef", drone: "0123456789abcdef0123456789abcdef"},
		{id: 920, partial: true},
		{id: 921, partial: true},
		{id: 922, partial: true},
	} {
		var found *DownloadObservation
		for index := range page.Items {
			if page.Items[index].NZBID == want.id {
				found = &page.Items[index]
				break
			}
		}
		if found == nil || found.ArrDownloadID != want.arrID || found.Drone != want.drone {
			t.Errorf("drone semantics for %d = %#v, want arr=%q drone=%q", want.id, found, want.arrID, want.drone)
		}
		if want.partial && !hasReason(page.Coverage, fmt.Sprintf("item_%d_parameters_malformed", pageItemIndex(page.Items, want.id))) {
			t.Errorf("conflicting drone %d was not partial: %#v", want.id, page.Coverage)
		}
	}
}

func TestOpaqueDroneDoesNotBypassCorrelationRedaction(t *testing.T) {
	const sentinel = "opaque-7f3a91c5e2"
	queue := []map[string]any{{
		"NZBID": 917, "NZBName": "Opaque Drone Queue", "Status": "QUEUED", "DestDir": "/downloads",
		"Parameters": []map[string]any{{"Name": "drone", "Value": sentinel}},
	}}
	history := []map[string]any{{
		"NZBID": 918, "ID": 918, "Kind": "NZB", "NZBName": "Opaque Drone History", "Status": "SUCCESS/ALL", "DestDir": "/downloads",
		"Parameters": []map[string]any{{"Name": "drone", "Value": sentinel}},
	}}
	handler := &rpcFixtureHandler{queue: rpcResult(t, queue), history: rpcResult(t, history), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-opaque-drone", nil)
	defer closeServer()
	page, err := client.ListDetailed(context.Background(), "nzb-opaque-drone", "", 10)
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("opaque drone page = %#v, %v", page, err)
	}
	for _, item := range page.Items {
		if item.Drone != "" || item.ArrDownloadID != "" {
			t.Errorf("opaque drone became correlation for %#v", item)
		}
	}
	if !hasReason(page.Coverage, "item_0_parameters_malformed") || !hasReason(page.Coverage, "item_1_parameters_malformed") {
		t.Fatalf("opaque drone was not marked partial: %#v", page.Coverage)
	}
	if strings.Contains(fmt.Sprintf("%#v", page), sentinel) {
		t.Fatalf("opaque drone leaked into detailed evidence: %#v", page)
	}
}

func TestExactDronePresenceSurvivesQueueHistoryMerge(t *testing.T) {
	const valid = "0123456789abcdef0123456789abcdef"
	queue := []map[string]any{
		{"NZBID": 1001, "NZBName": "Queue Empty Drone", "Status": "QUEUED", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": "drone", "Value": ""}}},
		{"NZBID": 1002, "NZBName": "Queue No Drone", "Status": "QUEUED", "DestDir": "/downloads"},
		{"NZBID": 1003, "NZBName": "Queue Duplicate Drone", "Status": "QUEUED", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": "drone", "Value": valid}, {"Name": "drone", "Value": valid}}},
	}
	history := []map[string]any{
		{"NZBID": 1001, "ID": 1001, "Kind": "NZB", "NZBName": "Queue Empty Drone", "Status": "SUCCESS/ALL", "DestDir": "/downloads"},
		{"NZBID": 1002, "ID": 1002, "Kind": "NZB", "NZBName": "Queue No Drone", "Status": "SUCCESS/ALL", "DestDir": "/downloads", "Parameters": []map[string]any{{"Name": "drone", "Value": ""}}},
		{"NZBID": 1003, "ID": 1003, "Kind": "NZB", "NZBName": "Queue Duplicate Drone", "Status": "SUCCESS/ALL", "DestDir": "/downloads"},
	}
	handler := &rpcFixtureHandler{queue: rpcResult(t, queue), history: rpcResult(t, history), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-drone-merge", nil)
	defer closeServer()
	page, err := client.ListDetailed(context.Background(), "nzb-drone-merge", "", 10)
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("merged exact drone page = %#v, %v", page, err)
	}
	for index, item := range page.Items {
		if item.ArrDownloadID != "" || item.Drone != "" {
			t.Errorf("merged exact drone fallback for item %d = %#v", index, item)
		}
		if !hasReason(page.Coverage, fmt.Sprintf("item_%d_parameters_malformed", index)) {
			t.Errorf("merged exact drone item %d missing partial reason: %#v", index, page.Coverage)
		}
	}
}

func pageItemIndex(items []DownloadObservation, id int64) int {
	for index := range items {
		if items[index].NZBID == id {
			return index
		}
	}
	return -1
}

func TestNegativeIdentityEvidenceIsPartial(t *testing.T) {
	queue := []map[string]any{
		{"NZBID": 920, "ID": -1, "NZBName": "Positive NZBID Negative Alias", "Kind": "NZB", "Status": "QUEUED", "DestDir": "/downloads"},
		{"NZBID": -1, "ID": 921, "NZBName": "Negative NZBID Positive Alias", "Kind": "NZB", "Status": "QUEUED", "DestDir": "/downloads"},
		{"NZBID": -2, "ID": -3, "NZBName": "Both Negative Queue", "Kind": "NZB", "Status": "QUEUED", "DestDir": "/downloads"},
	}
	history := []map[string]any{
		{"NZBID": 930, "ID": -1, "Kind": "NZB", "NZBName": "Positive History Negative Alias", "Status": "SUCCESS/ALL", "DestDir": "/downloads"},
		{"NZBID": -1, "ID": 931, "Kind": "NZB", "NZBName": "Negative History Positive Alias", "Status": "SUCCESS/ALL", "DestDir": "/downloads"},
		{"NZBID": -4, "ID": -5, "Kind": "NZB", "NZBName": "Both Negative History", "Status": "SUCCESS/ALL", "DestDir": "/downloads"},
	}
	handler := &rpcFixtureHandler{queue: rpcResult(t, queue), history: rpcResult(t, history), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-negative-id", nil)
	defer closeServer()
	page, err := client.ListDetailed(context.Background(), "nzb-negative-id", "", 10)
	if err != nil || len(page.Items) != 6 || page.Coverage.Completeness != domain.CompletenessPartial {
		t.Fatalf("negative identity page = %#v, %v", page, err)
	}
	for _, item := range page.Items {
		if !hasReason(page.Coverage, fmt.Sprintf("item_%d_identity_invalid", pageItemIndex(page.Items, item.NZBID))) {
			t.Errorf("negative identity missing reason for %#v: %#v", item, page.Coverage.ReasonCodes)
		}
		if item.Item.ProcessingDone || item.Item.State == "completed" {
			t.Errorf("negative identity became ready: %#v", item)
		}
	}
	if page.Items[0].NZBID != 920 || page.Items[0].Item.ExternalID != "920" || page.Items[1].NZBID != 921 || page.Items[1].Item.ExternalID != "921" {
		t.Fatalf("positive counterpart identity fallback = %#v", page.Items[:2])
	}
}

func TestHistoryAliasFallbackAndProcessingStates(t *testing.T) {
	history := []map[string]any{
		{"ID": 9, "Kind": "NZB", "NZBName": "Alias Only", "DestDir": "/downloads/a", "Status": "SUCCESS/ALL"},
		{"NZBID": 10, "ID": 10, "Kind": "NZB", "NZBName": "Warning", "DestDir": "/downloads/b", "Status": "WARNING/SPACE"},
		{"NZBID": 11, "ID": 11, "Kind": "NZB", "NZBName": "Failure", "DestDir": "/downloads/c", "Status": "FAILURE/UNPACK"},
		{"NZBID": 12, "ID": 12, "Kind": "NZB", "NZBName": "Future", "DestDir": "/downloads/d", "Status": "FUTURE/STATE"},
		{"NZBID": 13, "ID": 12, "Kind": "NZB", "NZBName": "Alias Mismatch", "DestDir": "/downloads/e", "Status": "SUCCESS/ALL"},
	}
	handler := &rpcFixtureHandler{queue: fixture(t, "empty.json"), history: rpcResult(t, history), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-main", nil)
	defer closeServer()
	page, err := client.ListDetailed(context.Background(), "nzb-main", "", 10)
	if err != nil {
		t.Fatalf("ListDetailed: %v", err)
	}
	if len(page.Items) != len(history) || page.Coverage.Completeness != domain.CompletenessPartial {
		t.Fatalf("history page = %#v", page)
	}
	if page.Items[0].NZBID != 9 || page.Items[0].Item.ExternalID != "9" || page.Items[0].HistoryID != 9 || !page.Items[0].Item.ProcessingDone {
		t.Fatalf("deprecated ID fallback = %#v", page.Items[0])
	}
	if hasReason(page.Coverage, "item_0_history_id_alias_mismatch") {
		t.Fatalf("ID-only history was treated as contradictory: %#v", page.Coverage.ReasonCodes)
	}
	if page.Items[1].Item.ProcessingDone || page.Items[1].Item.State != "completed" || !hasReason(page.Coverage, "item_1_processing_warning") {
		t.Fatalf("warning state = %#v", page.Items[1])
	}
	if page.Items[2].Item.ProcessingDone || page.Items[2].Item.State != "failed" || !hasReason(page.Coverage, "item_2_processing_failed") {
		t.Fatalf("failure state = %#v", page.Items[2])
	}
	if page.Items[3].Item.ProcessingDone || page.Items[3].Item.State != "unknown" || !hasReason(page.Coverage, "item_3_state_unknown") {
		t.Fatalf("future state = %#v", page.Items[3])
	}
	if page.Items[4].Item.ProcessingDone || !hasReason(page.Coverage, "item_4_history_id_alias_mismatch") {
		t.Fatalf("alias mismatch = %#v", page.Items[4])
	}
}

func TestFullArrayBoundAndExactMultiplePagination(t *testing.T) {
	queue := []map[string]any{
		{"NZBID": 1, "Kind": "NZB", "NZBName": "One", "Status": "QUEUED", "DestDir": "/downloads", "FileSizeLo": 1, "RemainingSizeLo": 1},
		{"NZBID": 2, "Kind": "NZB", "NZBName": "Two", "Status": "QUEUED", "DestDir": "/downloads", "FileSizeLo": 1, "RemainingSizeLo": 1},
		{"NZBID": 3, "Kind": "NZB", "NZBName": "Three", "Status": "QUEUED", "DestDir": "/downloads", "FileSizeLo": 1, "RemainingSizeLo": 1},
	}
	handler := &rpcFixtureHandler{queue: rpcResult(t, queue), history: fixture(t, "empty.json"), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-main", func(config *Config) {
		config.MaxItems = 2
		config.MaxPageSize = 1
	})
	defer closeServer()
	first, err := client.ListDetailed(context.Background(), "nzb-main", "", 1)
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" || first.Coverage.ObservedCount != 1 || first.Coverage.Completeness != domain.CompletenessPartial {
		t.Fatalf("first bounded page = %#v, %v", first, err)
	}
	second, err := client.ListDetailed(context.Background(), "nzb-main", first.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" || second.Coverage.ObservedCount != 2 || second.Coverage.Completeness != domain.CompletenessPartial || !hasReason(second.Coverage, "queue_limit") {
		t.Fatalf("second bounded page = %#v, %v", second, err)
	}

	exactHandler := &rpcFixtureHandler{queue: rpcResult(t, queue[:2]), history: fixture(t, "empty.json"), status: map[string]int{}, malformed: map[string]bool{}}
	exactClient, closeExact := newFixtureClient(t, exactHandler, "nzb-exact", func(config *Config) { config.MaxPageSize = 2 })
	defer closeExact()
	exact, err := exactClient.ListDetailed(context.Background(), "nzb-exact", "", 2)
	if err != nil || len(exact.Items) != 2 || exact.NextCursor != "" || exact.Coverage.ObservedCount != 2 || exact.Coverage.Completeness != domain.CompletenessComplete {
		t.Fatalf("exact multiple page = %#v, %v", exact, err)
	}
}

func TestCursorAuthenticationAndSnapshotChange(t *testing.T) {
	firstQueue := []map[string]any{{"NZBID": 1, "NZBName": "One", "Status": "QUEUED", "DestDir": "/downloads"}, {"NZBID": 2, "NZBName": "Two", "Status": "QUEUED", "DestDir": "/downloads"}}
	secondQueue := []map[string]any{{"NZBID": 9, "NZBName": "Inserted", "Status": "QUEUED", "DestDir": "/downloads"}, {"NZBID": 1, "NZBName": "One", "Status": "QUEUED", "DestDir": "/downloads"}, {"NZBID": 2, "NZBName": "Two", "Status": "QUEUED", "DestDir": "/downloads"}}
	handler := &rpcFixtureHandler{queueResults: [][]byte{rpcResult(t, firstQueue), rpcResult(t, secondQueue)}, history: fixture(t, "empty.json"), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-main", func(config *Config) { config.MaxPageSize = 1 })
	defer closeServer()
	first, err := client.ListDetailed(context.Background(), "nzb-main", "", 1)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first cursor page = %#v, %v", first, err)
	}
	tampered := []byte(first.NextCursor)
	if tampered[0] == 'A' {
		tampered[0] = 'B'
	} else {
		tampered[0] = 'A'
	}
	assertErrorCode(t, func() error {
		_, decodeErr := client.decodeCursor(string(tampered))
		return decodeErr
	}(), domain.OutcomeInvalidInput)
	second, err := client.ListDetailed(context.Background(), "nzb-main", first.NextCursor, 1)
	if err != nil {
		t.Fatalf("changed snapshot continuation: %v", err)
	}
	if second.Coverage.Completeness != domain.CompletenessPartial || second.NextCursor != "" || !hasReason(second.Coverage, "pagination_snapshot_changed") {
		t.Fatalf("changed snapshot = %#v", second)
	}
	other, closeOther := newFixtureClient(t, &rpcFixtureHandler{queue: fixture(t, "empty.json"), history: fixture(t, "empty.json"), status: map[string]int{}, malformed: map[string]bool{}}, "nzb-main", func(config *Config) { config.MaxPageSize = 1 })
	defer closeOther()
	assertErrorCode(t, func() error {
		_, decodeErr := other.decodeCursor(first.NextCursor)
		return decodeErr
	}(), domain.OutcomeInvalidInput)
}

func TestDescriptorAvailabilityAndRetentionBoundary(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "Example Film.nzb"), fixture(t, "descriptor.nzb"), 0o600); err != nil {
		t.Fatalf("write descriptor: %v", err)
	}
	handler := &rpcFixtureHandler{queue: fixture(t, "empty.json"), history: fixture(t, "history-provenance.json"), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-main", func(config *Config) {
		config.DescriptorMode = DescriptorBestEffort
		config.DescriptorDirectory = directory
	})
	defer closeServer()
	page, err := client.ListDetailed(context.Background(), "nzb-main", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].Item.Descriptor == nil || !page.Items[0].Item.Descriptor.Available {
		t.Fatalf("retained descriptor = %#v, %v", page, err)
	}
	descriptor := page.Items[0].Item.Descriptor
	digest := sha256.Sum256(fixture(t, "descriptor.nzb"))
	if descriptor.Kind != "nzb" || descriptor.Source != "nzbget.retained_file" || descriptor.Size != int64(len(fixture(t, "descriptor.nzb"))) || descriptor.Digest != "sha256:"+fmt.Sprintf("%x", digest) || descriptor.ID == "" {
		t.Fatalf("descriptor metadata = %#v", descriptor)
	}
	if strings.Contains(fmt.Sprintf("%#v", page), "<file") {
		t.Fatal("descriptor bytes exposed in observation")
	}

	noDirectory, closeNoDirectory := newFixtureClient(t, handler, "nzb-no-descriptor", func(config *Config) { config.DescriptorMode = DescriptorBestEffort })
	defer closeNoDirectory()
	without, err := noDirectory.ListDetailed(context.Background(), "nzb-no-descriptor", "", 1)
	if err != nil || without.Items[0].Item.Descriptor == nil || without.Items[0].Item.Descriptor.Available || without.Items[0].Item.Descriptor.Unavailable != "not_configured" || !hasReason(without.Coverage, "item_0_descriptor_unavailable") {
		t.Fatalf("unconfigured descriptor = %#v, %v", without, err)
	}

	if err := os.WriteFile(filepath.Join(directory, "Example Film.nzb"), []byte("<html>not nzb</html>"), 0o600); err != nil {
		t.Fatalf("write malformed descriptor: %v", err)
	}
	malformed, err := client.ListDetailed(context.Background(), "nzb-main", "", 1)
	if err != nil || malformed.Items[0].Item.Descriptor.Available || malformed.Items[0].Item.Descriptor.Unavailable != "malformed" {
		t.Fatalf("malformed descriptor = %#v, %v", malformed, err)
	}
}

func TestPathMappingBoundariesAndAmbiguousEvidence(t *testing.T) {
	handler := &rpcFixtureHandler{queue: fixture(t, "empty.json"), history: fixture(t, "history-provenance.json"), status: map[string]int{}, malformed: map[string]bool{}}
	client, closeServer := newFixtureClient(t, handler, "nzb-main", func(config *Config) {
		config.Mappings = append(config.Mappings,
			domain.PathMapping{ConnectionID: "nzb-main", SourcePrefix: "/downloads/movies", RootID: "movies", DestinationPrefix: "library"},
			domain.PathMapping{ConnectionID: "nzb-main", SourcePrefix: "/downloads/movies", RootID: "other", DestinationPrefix: "library"},
		)
	})
	defer closeServer()
	pathClient := &Client{config: Config{ConnectionID: "nzb-main", Mappings: []domain.PathMapping{{ConnectionID: "nzb-main", SourcePrefix: "/downloads/movies", RootID: "movies", DestinationPrefix: "library"}}}}
	if _, ok, ambiguous := pathClient.mapPath("/downloads/movies2/film.mkv"); ok || ambiguous {
		t.Fatal("path-prefix ambiguity incorrectly matched")
	}
	if _, ok, ambiguous := client.mapPath("/downloads/movies/film.mkv"); ok || !ambiguous {
		t.Fatal("equal component mapping should be ambiguous")
	}
	page, err := client.ListDetailed(context.Background(), "nzb-main", "", 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].MappedPath != nil || !hasReason(page.Coverage, "item_0_final_path_mapping_ambiguous") {
		t.Fatalf("ambiguous final path = %#v, %v", page, err)
	}

	for _, config := range []Config{
		{ConnectionID: "nzb-main", Endpoint: "https://user:password@example.test"},
		{ConnectionID: "nzb-main", Endpoint: "https://example.test?secret=x"},
		{ConnectionID: "nzb-main", Endpoint: "https://example.test", Mappings: []domain.PathMapping{{ConnectionID: "nzb-main", RootID: "library", SourcePrefix: "relative"}}},
		{ConnectionID: "nzb-main", Endpoint: "https://example.test", DescriptorMode: DescriptorBestEffort, DescriptorDirectory: "relative"},
	} {
		if _, err := New(config); err == nil {
			t.Fatalf("invalid config accepted: %#v", config)
		}
	}
}

func TestRPCErrorTimeoutAndMalformedResponses(t *testing.T) {
	unauthorizedHandler := &rpcFixtureHandler{status: map[string]int{"listgroups": http.StatusUnauthorized, "history": http.StatusUnauthorized}, malformed: map[string]bool{}}
	unauthorizedHandler.requireAuth = true
	unauthorizedHandler.username = "expected"
	unauthorizedHandler.password = "expected"
	client, closeServer := newFixtureClient(t, unauthorizedHandler, "nzb-main", nil)
	defer closeServer()
	upstream := assertErrorCode(t, func() error {
		_, listErr := client.List(context.Background(), "nzb-main", "", 1)
		return listErr
	}(), domain.OutcomeUnauthorized)
	if strings.Contains(upstream.Error(), "fixture-password") || strings.Contains(upstream.Error(), "fixture-user") {
		t.Fatalf("credentials leaked: %v", upstream)
	}

	rateHandler := &rpcFixtureHandler{queue: fixture(t, "empty.json"), history: fixture(t, "empty.json"), status: map[string]int{"listgroups": http.StatusTooManyRequests}, malformed: map[string]bool{}}
	rateClient, closeRate := newFixtureClient(t, rateHandler, "nzb-rate", nil)
	defer closeRate()
	ratePage, err := rateClient.ListDetailed(context.Background(), "nzb-rate", "", 1)
	if err != nil || ratePage.Coverage.Completeness != domain.CompletenessPartial || !hasReason(ratePage.Coverage, "queue_unavailable") {
		t.Fatalf("rate limited partial page = %#v, %v", ratePage, err)
	}

	malformedHandler := &rpcFixtureHandler{queue: []byte(`{}`), history: fixture(t, "empty.json"), status: map[string]int{}, malformed: map[string]bool{}}
	malformedClient, closeMalformed := newFixtureClient(t, malformedHandler, "nzb-malformed", nil)
	defer closeMalformed()
	malformedPage, err := malformedClient.ListDetailed(context.Background(), "nzb-malformed", "", 1)
	if err != nil || malformedPage.Coverage.Completeness != domain.CompletenessPartial || !hasReason(malformedPage.Coverage, "queue_malformed") {
		t.Fatalf("malformed partial page = %#v, %v", malformedPage, err)
	}

	blockDone := make(chan struct{})
	blockHandler := &rpcFixtureHandler{queue: fixture(t, "empty.json"), history: fixture(t, "empty.json"), status: map[string]int{}, malformed: map[string]bool{}, blockMethod: "listgroups", blockDone: blockDone}
	blockClient, closeBlock := newFixtureClient(t, blockHandler, "nzb-timeout", nil)
	defer func() {
		close(blockDone)
		closeBlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = blockClient.List(ctx, "nzb-timeout", "", 1)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %T %v", err, err)
	}
}

func TestCursorBoundsAndScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusNotFound) }))
	defer server.Close()
	client, err := New(Config{ConnectionID: "nzb-main", Endpoint: server.URL, MaxItems: maxItemsCeiling, MaxPageSize: maxItemsCeiling})
	if err != nil {
		t.Fatalf("maximum config: %v", err)
	}
	sourceID, err := domain.NewRuntimeID()
	if err != nil {
		t.Fatalf("source ID: %v", err)
	}
	digest := strings.Repeat("a", sha256.Size*2)
	token, err := client.encodeCursorChecked(inventoryCursor{SourceID: sourceID, StartedAt: time.Now().UTC(), Offset: maxItemsCeiling, PageCount: 1, PageSize: maxItemsCeiling, ObservedCount: maxItemsCeiling - 1, SnapshotDigest: digest})
	if err != nil || len(token) >= maxEncodedCursorBytes {
		t.Fatalf("maximum cursor = %q, %v", token, err)
	}
	decoded, err := client.decodeCursor(token)
	if err != nil || decoded.PageSize != maxItemsCeiling || decoded.SnapshotDigest != digest {
		t.Fatalf("cursor round trip = %#v, %v", decoded, err)
	}
	if _, err := New(Config{ConnectionID: "nzb-main", Endpoint: server.URL, MaxItems: maxItemsCeiling + 1}); err == nil {
		t.Fatal("item bound above ceiling accepted")
	}
	if _, err := client.List(context.Background(), "other", "", 1); err == nil {
		t.Fatal("wrong connection scope accepted")
	} else {
		assertErrorCode(t, err, domain.OutcomeInvalidInput)
	}
	if got := client.ScopedIdentity("42"); got != "nzb-main:nzbget:42" {
		t.Fatalf("scoped identity = %q", got)
	}
}

func TestReadOnlyPort(t *testing.T) {
	var _ ports.DownloadInventoryPort = (*Client)(nil)
	var _ ports.CapabilityPort = (*Client)(nil)
	if got := combinedSize(0, 10); got != 10 {
		t.Fatalf("combined size = %d", got)
	}
	if got := combinedSize(-1, 10); got != -1 {
		t.Fatalf("invalid combined size = %d", got)
	}
	if got := baseName("/incoming/Example Film.nzb"); got != "Example Film" {
		t.Fatalf("base name = %q", got)
	}
}
