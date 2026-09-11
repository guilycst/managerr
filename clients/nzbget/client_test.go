package nzbget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type testRPCRequest struct {
	Version string            `json:"version"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
	ID      uint64            `json:"id"`
}

func TestReadMethodsUsePositionalArgumentsAndNormalizeResponses(t *testing.T) {
	var calls []testRPCRequest
	var callsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "reader" || password != "secret" {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var rpcRequest testRPCRequest
		if err := json.Unmarshal(body, &rpcRequest); err != nil {
			t.Fatalf("request body: %v", err)
		}
		callsMu.Lock()
		calls = append(calls, rpcRequest)
		callsMu.Unlock()
		var result any
		switch rpcRequest.Method {
		case MethodVersion:
			result = "24.2.1"
		case MethodListGroups:
			result = []map[string]any{{
				"NZBID": 7, "NZBName": "Example", "Kind": "NZB", "Status": "UNPACKING",
				"FileSizeHi": 2, "FileSizeLo": 3, "RemainingSizeHi": 1, "RemainingSizeLo": 4,
				"DownloadedSizeHi": 1, "DownloadedSizeLo": 5,
				"URL":          "https://user:secret@example.test/download?token=hidden",
				"Parameters":   []map[string]any{{"Name": "drone", "Value": nil}},
				"PostInfoText": "processing", "PostStageProgress": 400,
			}}
		case MethodListFiles:
			result = []map[string]any{{
				"ID": 11, "NZBID": 7, "NZBName": "Example", "Filename": "episode.mkv",
				"FileSizeHi": 3, "FileSizeLo": 9, "RemainingSizeHi": 0, "RemainingSizeLo": 2,
				"FilenameConfirmed": true,
			}}
		case MethodHistory:
			result = []map[string]any{{
				"NZBID": 0, "ID": 9, "Kind": "NZB", "Name": "Finished", "Status": "SUCCESS/ALL",
				"HistoryTime": 1700000000, "FileSizeHi": 1, "FileSizeLo": 2,
				"DownloadedSizeHi": 4, "DownloadedSizeLo": 5,
			}}
		default:
			t.Fatalf("unexpected method %q", rpcRequest.Method)
		}
		writeRPCResult(t, writer, rpcRequest.ID, result)
	}))
	defer server.Close()

	client, err := New(Config{Endpoint: server.URL, Username: "reader", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	version, err := client.Version(context.Background())
	if err != nil || version.Version != "24.2.1" {
		t.Fatalf("version = %#v, %v", version, err)
	}
	groups, err := client.ListGroups(context.Background(), 0)
	if err != nil || len(groups) != 1 {
		t.Fatalf("groups = %#v, %v", groups, err)
	}
	if groups[0].FileSizeBytes != (uint64(2)<<32|3) || groups[0].RemainingSizeBytes != (uint64(1)<<32|4) || groups[0].DownloadedSizeBytes == nil || *groups[0].DownloadedSizeBytes != (uint64(1)<<32|5) {
		t.Fatalf("group 64-bit normalization = %#v", groups[0])
	}
	if groups[0].URL == nil || *groups[0].URL != "https://example.test" {
		t.Fatalf("group URL normalization = %#v", groups[0].URL)
	}
	if len(groups[0].Parameters) != 1 || groups[0].Parameters[0].Value != nil {
		t.Fatalf("nullable parameter = %#v", groups[0].Parameters)
	}
	files, err := client.ListFiles(context.Background(), 0, 0, 7)
	if err != nil || len(files) != 1 || files[0].FileSizeBytes != (uint64(3)<<32|9) || files[0].RemainingSizeBytes != 2 {
		t.Fatalf("files = %#v, %v", files, err)
	}
	history, err := client.History(context.Background(), false)
	if err != nil || len(history) != 1 {
		t.Fatalf("history = %#v, %v", history, err)
	}
	if history[0].CanonicalNZBID != 9 || history[0].ID == nil || *history[0].ID != 9 || history[0].HistoryTime == nil || history[0].DownloadedSizeBytes == nil || *history[0].DownloadedSizeBytes != (uint64(4)<<32|5) {
		t.Fatalf("history alias/time/size normalization = %#v", history[0])
	}

	callsMu.Lock()
	gotCalls := append([]testRPCRequest(nil), calls...)
	callsMu.Unlock()
	if len(gotCalls) != 4 {
		t.Fatalf("calls = %#v", gotCalls)
	}
	wantMethods := []string{MethodVersion, MethodListGroups, MethodListFiles, MethodHistory}
	for index, call := range gotCalls {
		if call.Version != ProtocolVersion || call.Method != wantMethods[index] || call.ID != uint64(index+1) {
			t.Fatalf("call %d = %#v", index, call)
		}
	}
	if len(gotCalls[0].Params) != 0 || !rawParamsEqual(gotCalls[1].Params, "0") || !rawParamsEqual(gotCalls[2].Params, "0", "0", "7") || !rawParamsEqual(gotCalls[3].Params, "false") {
		t.Fatalf("positional params = %#v", gotCalls)
	}
}

func TestGeneratedWrappersAndReadOnlyMethodBoundary(t *testing.T) {
	params := ListFilesRequest{IDFrom: 0, IDTo: 0, NZBID: 44}.Params()
	if !rawParamsEqual(marshalParams(t, params), "0", "0", "44") {
		t.Fatalf("generated listfiles params = %#v", params)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpcRequest testRPCRequest
		if err := json.NewDecoder(request.Body).Decode(&rpcRequest); err != nil {
			t.Fatal(err)
		}
		writeRPCResult(t, writer, rpcRequest.ID, []map[string]any{})
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Methods().ListFiles(context.Background(), ListFilesRequest{NZBID: 44}); err != nil {
		t.Fatal(err)
	}
	if err := client.Invoke(context.Background(), "rpc.discover", nil, new(any)); !IsKind(err, ErrorInvalidInput) {
		t.Fatalf("rpc.discover error = %v", err)
	}
	if err := client.Invoke(context.Background(), MethodListGroups, []any{1}, new([]GroupRecord)); !IsKind(err, ErrorInvalidInput) {
		t.Fatalf("nonzero listgroups log count error = %v", err)
	}
	if _, err := client.ListFiles(context.Background(), 1, 0, 0); !IsKind(err, ErrorInvalidInput) {
		t.Fatalf("nonzero IDFrom error = %v", err)
	}
}

func TestUnauthorizedAndHTTPFailuresAreTypedAndSanitized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(writer, "password=do-not-leak /private/path")
	}))
	client, err := New(Config{Endpoint: server.URL, Username: "bad", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Version(context.Background())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.Kind != ErrorUnauthorized || upstream.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized error = %#v, %v", upstream, err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(upstream.Detail, "private") {
		t.Fatalf("unauthorized error leaked detail: %v / %q", err, upstream.Detail)
	}
	server.Close()

	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(writer, "token=hidden")
	}))
	defer httpServer.Close()
	client, err = New(Config{Endpoint: httpServer.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Version(context.Background())
	if !IsKind(err, ErrorUnavailable) {
		t.Fatalf("HTTP 502 error = %v", err)
	}
	if !errors.As(err, &upstream) || upstream.StatusCode != http.StatusBadGateway || !upstream.Retryable {
		t.Fatalf("HTTP 502 detail = %#v", upstream)
	}
}

func TestMalformedOversizedAndRPCFailures(t *testing.T) {
	tests := []struct {
		name string
		body func(uint64) string
	}{
		{name: "invalid json", body: func(uint64) string { return "not-json" }},
		{name: "wrong envelope version", body: func(id uint64) string { return fmt.Sprintf(`{"version":"2.0","id":%d,"result":"24.2"}`, id) }},
		{name: "wrong response id", body: func(uint64) string { return `{"version":"1.1","id":99,"result":"24.2"}` }},
		{name: "null result", body: func(id uint64) string { return fmt.Sprintf(`{"version":"1.1","id":%d,"result":null}`, id) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var rpcRequest testRPCRequest
				if err := json.NewDecoder(request.Body).Decode(&rpcRequest); err != nil {
					t.Fatal(err)
				}
				_, _ = io.WriteString(writer, test.body(rpcRequest.ID))
			}))
			defer server.Close()
			client, err := New(Config{Endpoint: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Version(context.Background())
			if !IsKind(err, ErrorMalformed) {
				t.Fatalf("malformed result = %v", err)
			}
		})
	}

	oversizedServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, `{"version":"1.1","id":1,"result":"`+strings.Repeat("x", 100)+`"}`)
	}))
	defer oversizedServer.Close()
	client, err := New(Config{Endpoint: oversizedServer.URL, MaxResponseBytes: 32})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Version(context.Background())
	if !IsKind(err, ErrorResponseTooLarge) {
		t.Fatalf("oversized result = %v", err)
	}

	rpcServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpcRequest testRPCRequest
		if err := json.NewDecoder(request.Body).Decode(&rpcRequest); err != nil {
			t.Fatal(err)
		}
		writeRPCError(t, writer, rpcRequest.ID, -32601, "secret/path must not escape")
	}))
	defer rpcServer.Close()
	client, err = New(Config{Endpoint: rpcServer.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Version(context.Background())
	var upstream *UpstreamError
	if !errors.As(err, &upstream) || upstream.Kind != ErrorProtocol || upstream.RPCCode != -32601 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("RPC error = %#v, %v", upstream, err)
	}
}

func TestTimeoutAndCallerCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, RequestTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Version(context.Background())
	if !IsKind(err, ErrorTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request timeout = %#v, %v", err, err)
	}

	requestContext, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Version(requestContext)
	if !IsKind(err, ErrorCanceled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation = %#v, %v", err, err)
	}
}

func TestRequestIDsRemainUniqueAndMonotonicUnderConcurrency(t *testing.T) {
	var ids []uint64
	var idsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpcRequest testRPCRequest
		if err := json.NewDecoder(request.Body).Decode(&rpcRequest); err != nil {
			t.Fatal(err)
		}
		idsMu.Lock()
		ids = append(ids, rpcRequest.ID)
		idsMu.Unlock()
		writeRPCResult(t, writer, rpcRequest.ID, "24.2.1")
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	const count = 24
	var waitGroup sync.WaitGroup
	waitGroup.Add(count)
	for index := 0; index < count; index++ {
		go func() {
			defer waitGroup.Done()
			if _, err := client.Version(context.Background()); err != nil {
				t.Errorf("Version: %v", err)
			}
		}()
	}
	waitGroup.Wait()
	if len(ids) != count {
		t.Fatalf("request ids = %#v", ids)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	for index, id := range ids {
		if id != uint64(index+1) {
			t.Fatalf("request IDs = %#v", ids)
		}
	}
}

func TestHistoryAliasConflictFailsClosedAndEndpointValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var rpcRequest testRPCRequest
		if err := json.NewDecoder(request.Body).Decode(&rpcRequest); err != nil {
			t.Fatal(err)
		}
		writeRPCResult(t, writer, rpcRequest.ID, []map[string]any{{"NZBID": 10, "ID": 11, "Status": "SUCCESS/ALL"}})
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.History(context.Background(), false)
	if !IsKind(err, ErrorMalformed) {
		t.Fatalf("conflicting aliases = %v", err)
	}
	for _, endpoint := range []string{"", "localhost:6789", "ftp://example.test", "https://user:pass@example.test", "https://example.test/path?token=hidden", "https://example.test/path#fragment"} {
		if _, err := New(Config{Endpoint: endpoint}); err == nil {
			t.Fatalf("accepted unsafe endpoint %q", endpoint)
		}
	}
}

func writeRPCResult(t *testing.T, writer http.ResponseWriter, id uint64, result any) {
	t.Helper()
	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	envelope := map[string]any{"version": ProtocolVersion, "id": id, "result": json.RawMessage(encodedResult)}
	encodedEnvelope, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(encodedEnvelope)
}

func writeRPCError(t *testing.T, writer http.ResponseWriter, id uint64, code int64, message string) {
	t.Helper()
	envelope := map[string]any{"version": ProtocolVersion, "id": id, "error": map[string]any{"code": code, "message": message}}
	encodedEnvelope, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(encodedEnvelope)
}

func rawParamsEqual(values []json.RawMessage, want ...string) bool {
	if len(values) != len(want) {
		return false
	}
	for index, value := range values {
		if string(value) != want[index] {
			return false
		}
	}
	return true
}

func marshalParams(t *testing.T, params []any) []json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	var result []json.RawMessage
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSyntheticFixturesDecodeNullableAliasesAnd64BitValues(t *testing.T) {
	versionBody := readFixture(t, "version-result.json")
	var version VersionResult
	if err := json.Unmarshal(versionBody, &version); err != nil || version != "24.2.1" {
		t.Fatalf("version fixture = %q, %v", version, err)
	}
	groupsBody := readFixture(t, "listgroups-result.json")
	var groupsWire []GroupRecord
	if err := json.Unmarshal(groupsBody, &groupsWire); err != nil || len(groupsWire) != 1 {
		t.Fatalf("groups fixture = %#v, %v", groupsWire, err)
	}
	group := normalizeGroup(groupsWire[0])
	if group.FileSizeBytes != (uint64(1)<<32|1000) || len(group.Parameters) != 1 {
		t.Fatalf("group fixture normalization = %#v", group)
	}
	filesBody := readFixture(t, "listfiles-result.json")
	var filesWire []FileRecord
	if err := json.Unmarshal(filesBody, &filesWire); err != nil || len(filesWire) != 1 {
		t.Fatalf("files fixture = %#v, %v", filesWire, err)
	}
	if got := normalizeFile(filesWire[0]); got.FileSizeBytes != (uint64(2)<<32|2000) || got.Filename == nil || *got.Filename != "episode.mkv" {
		t.Fatalf("file fixture normalization = %#v", got)
	}
	historyBody := readFixture(t, "history-result.json")
	var historyWire []HistoryRecord
	if err := json.Unmarshal(historyBody, &historyWire); err != nil || len(historyWire) != 1 {
		t.Fatalf("history fixture = %#v, %v", historyWire, err)
	}
	history, err := normalizeHistory(historyWire[0])
	if err != nil || history.CanonicalNZBID != 77 || history.HistoryTime == nil || history.Parameters[0].Value != nil {
		t.Fatalf("history fixture normalization = %#v, %v", history, err)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}
