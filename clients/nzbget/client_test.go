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
	var networkCalls int
	var networkCallsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		networkCallsMu.Lock()
		networkCalls++
		networkCallsMu.Unlock()
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
	if err := client.Invoke(context.Background(), "rpc.discover", nil, new(any)); !IsKind(err, ErrorUnsupported) {
		t.Fatalf("rpc.discover error = %v", err)
	}
	if err := client.Invoke(context.Background(), "append", nil, new(any)); !IsKind(err, ErrorUnsupported) {
		t.Fatalf("mutation error = %v", err)
	}
	if err := client.Invoke(context.Background(), "unknown.read", nil, new(any)); !IsKind(err, ErrorUnsupported) {
		t.Fatalf("unknown safe method error = %v", err)
	}
	for _, method := range []string{"bad method", "bad/method", "", "?method"} {
		if err := client.Invoke(context.Background(), method, nil, new(any)); !IsKind(err, ErrorInvalidInput) {
			t.Fatalf("malformed method %q error = %v", method, err)
		}
	}
	if err := client.Invoke(context.Background(), MethodListGroups, []any{1}, new([]GroupRecord)); !IsKind(err, ErrorInvalidInput) {
		t.Fatalf("nonzero listgroups log count error = %v", err)
	}
	if _, err := client.ListFiles(context.Background(), 1, 0, 0); !IsKind(err, ErrorInvalidInput) {
		t.Fatalf("nonzero IDFrom error = %v", err)
	}
	networkCallsMu.Lock()
	gotNetworkCalls := networkCalls
	networkCallsMu.Unlock()
	if gotNetworkCalls != 1 {
		t.Fatalf("locally refused methods reached network: %d calls", gotNetworkCalls)
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

func TestHTTPStatusIsClassifiedBeforeResponseBound(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		kind      ErrorKind
		retryable bool
		deadline  bool
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, kind: ErrorUnauthorized},
		{name: "forbidden", status: http.StatusForbidden, kind: ErrorForbidden},
		{name: "request timeout", status: http.StatusRequestTimeout, kind: ErrorTimeout, retryable: true, deadline: true},
		{name: "rate limited", status: http.StatusTooManyRequests, kind: ErrorRateLimited, retryable: true},
		{name: "server error", status: http.StatusBadGateway, kind: ErrorUnavailable, retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, strings.Repeat("untrusted-body", 100))
			}))
			defer server.Close()
			client, err := New(Config{Endpoint: server.URL, MaxResponseBytes: 8})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Version(context.Background())
			var upstream *UpstreamError
			if !errors.As(err, &upstream) || upstream.Kind != test.kind || upstream.StatusCode != test.status || upstream.Retryable != test.retryable {
				t.Fatalf("status %d error = %#v, %v", test.status, upstream, err)
			}
			if errors.Is(err, context.DeadlineExceeded) != test.deadline {
				t.Fatalf("status %d deadline sentinel = %v", test.status, errors.Is(err, context.DeadlineExceeded))
			}
		})
	}
}

func TestHTTPTimeoutAndRateLimitSemanticsWithSmallBodies(t *testing.T) {
	for _, test := range []struct {
		status   int
		kind     ErrorKind
		deadline bool
	}{
		{status: http.StatusRequestTimeout, kind: ErrorTimeout, deadline: true},
		{status: http.StatusTooManyRequests, kind: ErrorRateLimited},
	} {
		t.Run(fmt.Sprint(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, "small response")
			}))
			defer server.Close()
			client, err := New(Config{Endpoint: server.URL, MaxResponseBytes: 64})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Version(context.Background())
			var upstream *UpstreamError
			if !errors.As(err, &upstream) || upstream.Kind != test.kind || upstream.StatusCode != test.status || !upstream.Retryable {
				t.Fatalf("status %d error = %#v, %v", test.status, upstream, err)
			}
			if errors.Is(err, context.DeadlineExceeded) != test.deadline {
				t.Fatalf("status %d deadline sentinel = %v", test.status, errors.Is(err, context.DeadlineExceeded))
			}
		})
	}
}

func TestTransportAndBodyReadErrorsAreSanitizedAndDistinct(t *testing.T) {
	const sentinel = "SENTINEL_TRANSPORT_SECRET"
	transportSentinel := errors.New(sentinel)
	transportErrorClient, err := New(Config{
		Endpoint: "https://private.example.test/jsonrpc",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, transportSentinel
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = transportErrorClient.Version(context.Background())
	if IsKind(err, ErrorInvalidInput) || strings.Contains(err.Error(), sentinel) || strings.Contains(err.Error(), "private.example.test") || errors.Is(err, transportSentinel) {
		t.Fatalf("transport error was exposed: %v", err)
	}
	for current := err; current != nil; current = errors.Unwrap(current) {
		if strings.Contains(current.Error(), sentinel) || strings.Contains(current.Error(), "private.example.test") {
			t.Fatalf("transport error chain was exposed: %v", current)
		}
	}
	var transportUpstream *UpstreamError
	if !errors.As(err, &transportUpstream) || transportUpstream.Kind != ErrorUnavailable || !transportUpstream.Retryable {
		t.Fatalf("transport error = %#v, %v", transportUpstream, err)
	}

	bodyReadClient, err := New(Config{
		Endpoint: "https://example.test/jsonrpc",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: failingBody{}}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = bodyReadClient.Version(context.Background())
	if !IsKind(err, ErrorUnavailable) {
		t.Fatalf("body read error = %v", err)
	}
	if errors.As(err, &transportUpstream) && transportUpstream.Kind != ErrorUnavailable {
		t.Fatalf("body read classification = %#v", transportUpstream)
	}

	deadlineClient, err := New(Config{
		Endpoint:       "https://example.test/jsonrpc",
		RequestTimeout: 20 * time.Millisecond,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: contextBody{context: request.Context()}}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = deadlineClient.Version(context.Background())
	if !IsKind(err, ErrorTimeout) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("body read timeout = %#v, %v", err, err)
	}
}

func TestRPCEnvelopeRequiresPresenceAndUniqueMembers(t *testing.T) {
	tests := []struct {
		name string
		body func(uint64) string
	}{
		{name: "duplicate version", body: func(id uint64) string {
			return fmt.Sprintf(`{"version":"1.1","version":"1.1","id":%d,"result":"24.2"}`, id)
		}},
		{name: "duplicate id", body: func(id uint64) string {
			return fmt.Sprintf(`{"version":"1.1","id":%d,"id":%d,"result":"24.2"}`, id, id)
		}},
		{name: "missing version", body: func(id uint64) string { return fmt.Sprintf(`{"id":%d,"result":"24.2"}`, id) }},
		{name: "null version", body: func(id uint64) string { return fmt.Sprintf(`{"version":null,"id":%d,"result":"24.2"}`, id) }},
		{name: "missing id", body: func(uint64) string { return `{"version":"1.1","result":"24.2"}` }},
		{name: "null id", body: func(uint64) string { return `{"version":"1.1","id":null,"result":"24.2"}` }},
		{name: "both result and error", body: func(id uint64) string {
			return fmt.Sprintf(`{"version":"1.1","id":%d,"result":"24.2","error":{"code":1,"message":"failed"}}`, id)
		}},
		{name: "neither result nor error", body: func(id uint64) string { return fmt.Sprintf(`{"version":"1.1","id":%d}`, id) }},
		{name: "null result", body: func(id uint64) string { return fmt.Sprintf(`{"version":"1.1","id":%d,"result":null}`, id) }},
		{name: "empty error", body: func(id uint64) string { return fmt.Sprintf(`{"version":"1.1","id":%d,"error":{}}`, id) }},
		{name: "missing error code", body: func(id uint64) string {
			return fmt.Sprintf(`{"version":"1.1","id":%d,"error":{"message":"failed"}}`, id)
		}},
		{name: "null error code", body: func(id uint64) string {
			return fmt.Sprintf(`{"version":"1.1","id":%d,"error":{"code":null,"message":"failed"}}`, id)
		}},
		{name: "missing error message", body: func(id uint64) string { return fmt.Sprintf(`{"version":"1.1","id":%d,"error":{"code":1}}`, id) }},
		{name: "null error message", body: func(id uint64) string {
			return fmt.Sprintf(`{"version":"1.1","id":%d,"error":{"code":1,"message":null}}`, id)
		}},
		{name: "duplicate nested code", body: func(id uint64) string {
			return fmt.Sprintf(`{"version":"1.1","id":%d,"error":{"code":1,"code":1,"message":"failed"}}`, id)
		}},
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
				t.Fatalf("envelope accepted: %v", err)
			}
		})
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
	if !errors.As(err, &upstream) || upstream.Kind != ErrorUnsupported || upstream.RPCCode != -32601 || strings.Contains(err.Error(), "secret") {
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
	for _, endpoint := range []string{
		"", "localhost:6789", "ftp://example.test", "https://user:pass@example.test",
		"https://example.test/path?token=hidden", "https://example.test/path#fragment",
		"https://example.test/./jsonrpc", "https://example.test/../jsonrpc",
		"https://example.test/%2e%2e/jsonrpc", "https://example.test/base//jsonrpc",
		"https://example.test/base/%2fjsonrpc", "https://example.test/base/%5Cjsonrpc",
		"https://example.test/base/%2e%2fjsonrpc", "https://example.test/base/%252e%252e/jsonrpc",
		"https://example.test/%00/jsonrpc", "https://example.test/%0A/jsonrpc",
		"https://example.test/%7F/jsonrpc", "https://example.test/%FF/jsonrpc",
	} {
		if _, err := New(Config{Endpoint: endpoint}); err == nil {
			t.Fatalf("accepted unsafe endpoint %q", endpoint)
		}
	}
	for _, layers := range []int{9, maxEndpointPathDecodes + 1} {
		for _, unsafe := range []struct {
			value string
			name  string
		}{
			{value: "%2e%2e", name: "dot"},
			{value: "%2f", name: "separator"},
		} {
			encoded := unsafe.value
			for index := 0; index < layers; index++ {
				encoded = strings.ReplaceAll(encoded, "%", "%25")
			}
			endpoint := "https://example.test/" + encoded + "/jsonrpc"
			if _, err := New(Config{Endpoint: endpoint}); err == nil {
				t.Fatalf("accepted %d-layer encoded %s segment", layers, unsafe.name)
			}
		}
	}
}

func TestEndpointPreservesCleanCustomPrefixAndRequestURI(t *testing.T) {
	var requestURI string
	var requestMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestMu.Lock()
		requestURI = request.RequestURI
		requestMu.Unlock()
		var rpcRequest testRPCRequest
		if err := json.NewDecoder(request.Body).Decode(&rpcRequest); err != nil {
			t.Fatal(err)
		}
		writeRPCResult(t, writer, rpcRequest.ID, "24.2.1")
	}))
	defer server.Close()

	client, err := New(Config{Endpoint: server.URL + "/proxy/nzbget%20rpc/"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Version(context.Background()); err != nil {
		t.Fatal(err)
	}
	requestMu.Lock()
	got := requestURI
	requestMu.Unlock()
	if got != "/proxy/nzbget%20rpc/" {
		t.Fatalf("request URI = %q", got)
	}
	if !strings.HasSuffix(client.Endpoint(), "/proxy/nzbget%20rpc/") {
		t.Fatalf("endpoint = %q", client.Endpoint())
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTripFunc) CloseIdleConnections() {}

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) {
	return 0, errors.New("SENTINEL_BODY_SECRET")
}

func (failingBody) Close() error { return nil }

type contextBody struct {
	context context.Context
}

func (body contextBody) Read([]byte) (int, error) {
	<-body.context.Done()
	return 0, errors.New("SENTINEL_BODY_CANCELLATION")
}

func (contextBody) Close() error { return nil }

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
