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
	Method string
	Params []json.RawMessage
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
	responseID         json.RawMessage
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
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
		ID     json.RawMessage   `json:"id"`
	}
	if err := json.NewDecoder(request.Body).Decode(&incoming); err != nil {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	handler.mu.Lock()
	handler.methods = append(handler.methods, incoming.Method)
	handler.calls = append(handler.calls, rpcFixtureCall{Method: incoming.Method, Params: append([]json.RawMessage(nil), incoming.Params...)})
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
	jsonrpc := handler.responseJSONRPC
	if jsonrpc == "" {
		jsonrpc = "2.0"
	}
	responseID := handler.responseID
	if len(responseID) == 0 {
		responseID = incoming.ID
	}
	_, _ = response.Write([]byte(`{"jsonrpc":"` + jsonrpc + `","id":`))
	_, _ = response.Write(responseID)
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
	if processing.NZBID != 78 || processing.Item.State != "completed" || !processing.Item.ProcessingDone || processing.ArrDownloadID != "78" || processing.MappedPath == nil || processing.MappedPath.RelativePath != "managed/movies/Example Processing" || processing.Item.Progress != 1 {
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
	if err != nil || len(page.Items) != 1 || page.Items[0].ArrDownloadID != "91" || !hasReason(page.Coverage, "item_0_parameters_malformed") || page.Coverage.Completeness != domain.CompletenessPartial {
		t.Fatalf("malformed parameter evidence = %#v, %v", page, err)
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
