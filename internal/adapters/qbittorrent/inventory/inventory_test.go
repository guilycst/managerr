package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guilycst/managerr/internal/domain"
)

const (
	fixtureFilmHash    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fixtureSeriesHash  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	fixturePackHash    = "cccccccccccccccccccccccccccccccccccccccc"
	fixtureProcessHash = "dddddddddddddddddddddddddddddddddddddddd"
)

type qbitFixtureHandler struct {
	mu sync.Mutex

	info             func(offset int) ([]byte, int)
	files            map[string][]byte
	properties       []byte
	descriptor       []byte
	loginStatus      int
	loginBody        []byte
	versionValue     string
	versionStatus    int
	malformedVersion bool
	requireAuth      bool
	blockLogin       bool
	blockDone        chan struct{}
	requests         []string
	queries          []string
}

func (handler *qbitFixtureHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	handler.mu.Lock()
	handler.requests = append(handler.requests, request.URL.Path)
	handler.queries = append(handler.queries, request.URL.RawQuery)
	handler.mu.Unlock()

	if handler.requireAuth && request.URL.Path != apiLogin {
		if _, err := request.Cookie("SID"); err != nil {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	switch request.URL.Path {
	case apiLogin:
		if handler.blockLogin {
			select {
			case <-request.Context().Done():
			case <-handler.blockDone:
			}
			return
		}
		status := handler.loginStatus
		if status == 0 {
			status = http.StatusOK
		}
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			response.Header().Set("Set-Cookie", "SID=fixture-session; Path=/")
			if handler.loginBody == nil {
				handler.loginBody = []byte("Ok.")
			}
			response.WriteHeader(status)
			if handler.loginBody != nil {
				_, _ = response.Write(handler.loginBody)
			}
			return
		}
		response.WriteHeader(status)
		return
	case apiWebAPIVersion, apiAppVersion:
		if handler.versionStatus != 0 && handler.versionStatus != http.StatusOK {
			response.WriteHeader(handler.versionStatus)
			return
		}
		value := handler.versionValue
		if value == "" {
			value = "5.0.4"
		}
		if handler.malformedVersion {
			value = "not-a-version"
		}
		_, _ = response.Write([]byte(value))
		return
	case apiTorrentInfo:
		if handler.info == nil {
			_, _ = response.Write([]byte("[]"))
			return
		}
		offset, _ := strconv.Atoi(request.URL.Query().Get("offset"))
		body, status := handler.info(offset)
		if status != 0 && status != http.StatusOK {
			response.WriteHeader(status)
			return
		}
		_, _ = response.Write(body)
		return
	case apiProperties:
		if handler.properties == nil {
			_, _ = response.Write([]byte(`{"save_path":"/downloads"}`))
			return
		}
		_, _ = response.Write(handler.properties)
		return
	case apiFiles:
		hash := strings.ToLower(request.URL.Query().Get("hash"))
		body := handler.files[hash]
		if body == nil {
			body = []byte("[]")
		}
		_, _ = response.Write(body)
		return
	case apiExport:
		if handler.descriptor == nil {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = response.Write(handler.descriptor)
		return
	default:
		response.WriteHeader(http.StatusNotFound)
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../../../tests/fixtures/qbittorrent", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func pageFromFixtures(first, second, empty []byte) func(int) ([]byte, int) {
	return func(offset int) ([]byte, int) {
		switch offset {
		case 0:
			return first, http.StatusOK
		case 2:
			return second, http.StatusOK
		default:
			return empty, http.StatusOK
		}
	}
}

func newFixtureClient(t *testing.T, handler *qbitFixtureHandler, connectionID domain.ConfigID) (*Client, func()) {
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
	client, err := New(config)
	if err != nil {
		server.Close()
		t.Fatalf("New: %v", err)
	}
	return client, server.Close
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

func TestListEmptyAndCapabilities(t *testing.T) {
	handler := &qbitFixtureHandler{info: func(int) ([]byte, int) {
		return fixture(t, "info-empty.json"), http.StatusOK
	}}
	client, closeServer := newFixtureClient(t, handler, "qbt-main")
	defer closeServer()

	page, err := client.List(context.Background(), "qbt-main", "", 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("empty page = %#v", page)
	}
	if page.Coverage.Completeness != domain.CompletenessComplete || page.Coverage.ObservedCount != 0 {
		t.Fatalf("coverage = %#v", page.Coverage)
	}
	if err := page.Coverage.Validate(); err != nil {
		t.Fatalf("coverage validation: %v", err)
	}

	capabilities, err := client.Capabilities(context.Background(), "qbt-main")
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if len(capabilities) != 6 {
		t.Fatalf("capability count = %d, want 6", len(capabilities))
	}
	for _, capability := range capabilities {
		if err := capability.Validate(); err != nil {
			t.Fatalf("capability %s validation: %v", capability.Name, err)
		}
	}
	if capabilities[0].Version != "5.0.4" || capabilities[1].Version != "5.0.4" {
		t.Fatalf("version capabilities = %#v", capabilities[:2])
	}
	if capabilities[2].State != domain.CapabilitySupported || capabilities[5].State != domain.CapabilityUnknown {
		t.Fatalf("capability states = %#v", capabilities)
	}
}

func TestAuthenticatedSessionCookieIsReused(t *testing.T) {
	handler := &qbitFixtureHandler{
		requireAuth: true,
		info: func(int) ([]byte, int) {
			return fixture(t, "info-empty.json"), http.StatusOK
		},
	}
	client, closeServer := newFixtureClient(t, handler, "qbt-main")
	defer closeServer()
	if _, err := client.List(context.Background(), "qbt-main", "", 1); err != nil {
		t.Fatalf("authenticated List: %v", err)
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	var loginCalls int
	for _, requestPath := range handler.requests {
		if requestPath == apiLogin {
			loginCalls++
		}
	}
	if loginCalls != 1 {
		t.Fatalf("login calls = %d, want one", loginCalls)
	}
}

func TestListDetailedPagesAndMetadata(t *testing.T) {
	handler := &qbitFixtureHandler{
		info:       pageFromFixtures(fixture(t, "info-page-1.json"), fixture(t, "info-page-2.json"), fixture(t, "info-empty.json")),
		properties: fixture(t, "properties.json"),
		files: map[string][]byte{
			fixtureFilmHash:   fixture(t, "files-film.json"),
			fixtureSeriesHash: fixture(t, "files-series.json"),
			fixturePackHash:   fixture(t, "files-pack.json"),
		},
	}
	client, closeServer := newFixtureClient(t, handler, "qbt-main")
	defer closeServer()

	first, err := client.ListDetailed(context.Background(), "qbt-main", "", 2)
	if err != nil {
		t.Fatalf("first ListDetailed: %v", err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %#v", first)
	}
	if first.Coverage.Completeness != domain.CompletenessPartial || first.Coverage.ObservedCount != 2 {
		t.Fatalf("first coverage = %#v", first.Coverage)
	}
	if err := first.Coverage.Validate(); err != nil {
		t.Fatalf("first coverage validation: %v", err)
	}
	film, series := first.Items[0], first.Items[1]
	if film.Item.ExternalID != fixtureFilmHash || film.ScopedIdentity != "qbt-main:"+fixtureFilmHash {
		t.Fatalf("film identity = %#v", film)
	}
	if film.Ratio != 3.5 || film.ContentPath != "/downloads/Example Film (2024)" || film.SavePath != "/downloads" {
		t.Fatalf("film properties = %#v", film)
	}
	if film.Item.Category != "movies" || len(film.Item.Tags) != 2 || film.Item.Tags[0] != "manual" || film.Item.Tags[1] != "arr" {
		t.Fatalf("film hints = %#v", film.Item)
	}
	if !film.Item.Seeding || !film.Item.ProcessingDone || film.Item.CompletedAt == nil || len(film.Files) != 2 || len(film.Item.Payload) != 2 {
		t.Fatalf("film state/files = %#v", film)
	}
	if film.Item.Descriptor == nil || film.Item.Descriptor.Available || film.Item.Descriptor.Unavailable != "not_requested" {
		t.Fatalf("film descriptor = %#v", film.Item.Descriptor)
	}
	if film.Files[0].Index != 0 || film.Files[0].Size != 1234567890 || film.Files[0].Availability != 1 || !film.Files[0].IsSeed {
		t.Fatalf("film file metadata = %#v", film.Files[0])
	}
	if film.Item.Payload[0].RootID != "library" || film.Item.Payload[0].RelativePath != "managed/Example Film (2024)/Example Film (2024).mkv" {
		t.Fatalf("film payload = %#v", film.Item.Payload[0])
	}
	if series.InfoHashV2 != fixtureSeriesHash || series.Item.Hash != fixtureSeriesHash || len(series.Item.Payload) != 2 {
		t.Fatalf("series v2 observation = %#v", series)
	}
	if series.Files[0].Availability != -1 || series.Files[0].Path != "/downloads/Example Series/Season 01/Example Series - S01E01.mkv" {
		t.Fatalf("series file = %#v", series.Files[0])
	}
	if series.Item.Payload[1].Type != domain.ManifestSubtitle || series.Item.Payload[1].Role != domain.RoleSubtitle {
		t.Fatalf("series subtitle = %#v", series.Item.Payload[1])
	}

	second, err := client.ListDetailed(context.Background(), "qbt-main", first.NextCursor, 2)
	if err != nil {
		t.Fatalf("second ListDetailed: %v", err)
	}
	if len(second.Items) != 1 || second.NextCursor != "" || second.Coverage.Completeness != domain.CompletenessComplete || second.Coverage.ObservedCount != 3 {
		t.Fatalf("second page = %#v", second)
	}
	pack := second.Items[0]
	if !pack.Item.Seeding || !pack.Item.ProcessingDone || pack.Item.CompletedAt == nil || pack.Ratio != 3.5 {
		t.Fatalf("pack state = %#v", pack)
	}
	if err := second.Coverage.Validate(); err != nil {
		t.Fatalf("second coverage validation: %v", err)
	}
}

func TestConnectionScopedIdentityAcrossInstances(t *testing.T) {
	handler := &qbitFixtureHandler{info: func(int) ([]byte, int) {
		return fixture(t, "info-page-1.json"), http.StatusOK
	}, properties: fixture(t, "properties.json"), files: map[string][]byte{fixtureFilmHash: fixture(t, "files-film.json")}}
	first, closeFirst := newFixtureClient(t, handler, "qbt-one")
	defer closeFirst()
	second, closeSecond := newFixtureClient(t, handler, "qbt-two")
	defer closeSecond()

	left, err := first.ListDetailed(context.Background(), "qbt-one", "", 1)
	if err != nil {
		t.Fatalf("first ListDetailed: %v", err)
	}
	right, err := second.ListDetailed(context.Background(), "qbt-two", "", 1)
	if err != nil {
		t.Fatalf("second ListDetailed: %v", err)
	}
	if left.Items[0].Item.ExternalID != right.Items[0].Item.ExternalID {
		t.Fatalf("fixture IDs differ: %q and %q", left.Items[0].Item.ExternalID, right.Items[0].Item.ExternalID)
	}
	if left.Items[0].ScopedIdentity == right.Items[0].ScopedIdentity || left.Items[0].ScopedIdentity != "qbt-one:"+fixtureFilmHash || right.Items[0].ScopedIdentity != "qbt-two:"+fixtureFilmHash {
		t.Fatalf("scoped identities = %q and %q", left.Items[0].ScopedIdentity, right.Items[0].ScopedIdentity)
	}
}

func TestBestEffortDescriptorMetadata(t *testing.T) {
	handler := &qbitFixtureHandler{
		info:       func(int) ([]byte, int) { return fixture(t, "info-page-1.json"), http.StatusOK },
		properties: fixture(t, "properties.json"),
		files:      map[string][]byte{fixtureFilmHash: fixture(t, "files-film.json")},
		descriptor: bytes.TrimSpace(fixture(t, "descriptor.torrent")),
	}
	config := Config{
		ConnectionID:   "qbt-main",
		Endpoint:       "",
		DescriptorMode: DescriptorBestEffort,
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	config.Endpoint = server.URL
	config.Mappings = []domain.PathMapping{{ConnectionID: "qbt-main", RootID: "library", SourcePrefix: "/downloads", DestinationPrefix: "managed"}}
	client, err := New(config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	page, err := client.ListDetailed(context.Background(), "qbt-main", "", 1)
	if err != nil {
		t.Fatalf("ListDetailed: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Item.Descriptor == nil {
		t.Fatalf("page = %#v", page)
	}
	descriptor := page.Items[0].Item.Descriptor
	if !descriptor.Available || descriptor.Size != int64(len(bytes.TrimSpace(fixture(t, "descriptor.torrent")))) || !strings.HasPrefix(descriptor.Digest, "sha256:") || descriptor.Source != "qbittorrent.export" || !descriptor.ID.Valid() {
		t.Fatalf("descriptor = %#v", descriptor)
	}
}

func TestPaginationBoundsAndObservedCount(t *testing.T) {
	handler := &qbitFixtureHandler{
		info:       func(int) ([]byte, int) { return fixture(t, "info-overflow.json"), http.StatusOK },
		properties: fixture(t, "properties.json"),
		files:      map[string][]byte{fixtureFilmHash: fixture(t, "files-film.json"), fixtureSeriesHash: fixture(t, "files-series.json")},
	}
	client, closeServer := newFixtureClient(t, handler, "qbt-main")
	defer closeServer()

	page, err := client.ListDetailed(context.Background(), "qbt-main", "", 2)
	if err != nil {
		t.Fatalf("overflow page: %v", err)
	}
	if len(page.Items) != 2 || page.NextCursor != "" || page.Coverage.Completeness != domain.CompletenessPartial || !hasReason(page.Coverage, "pagination_response_exceeded_limit") {
		t.Fatalf("overflow result = %#v", page)
	}

	handler.info = pageFromFixtures(fixture(t, "info-page-1.json"), fixture(t, "info-page-2.json"), fixture(t, "info-empty.json"))
	client.config.MaxItems = 3
	client.config.MaxPageSize = 2
	first, err := client.ListDetailed(context.Background(), "qbt-main", "", 2)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("bounded first page = %#v, %v", first, err)
	}
	second, err := client.ListDetailed(context.Background(), "qbt-main", first.NextCursor, 2)
	if err != nil {
		t.Fatalf("bounded second page: %v", err)
	}
	if len(second.Items) != 1 || second.Coverage.ObservedCount != 3 || second.NextCursor != "" || second.Coverage.Completeness != domain.CompletenessPartial || !hasReason(second.Coverage, "pagination_limit") {
		t.Fatalf("bounded second page = %#v", second)
	}
}

func TestRepeatedPageTerminatesAsPartial(t *testing.T) {
	repeated := fixture(t, "info-page-1.json")
	handler := &qbitFixtureHandler{
		info:       func(int) ([]byte, int) { return repeated, http.StatusOK },
		properties: fixture(t, "properties.json"),
		files:      map[string][]byte{fixtureFilmHash: fixture(t, "files-film.json"), fixtureSeriesHash: fixture(t, "files-series.json")},
	}
	client, closeServer := newFixtureClient(t, handler, "qbt-main")
	defer closeServer()

	first, err := client.ListDetailed(context.Background(), "qbt-main", "", 2)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first repeated page = %#v, %v", first, err)
	}
	second, err := client.ListDetailed(context.Background(), "qbt-main", first.NextCursor, 2)
	if err != nil {
		t.Fatalf("second repeated page: %v", err)
	}
	if len(second.Items) != 0 || second.NextCursor != "" || second.Coverage.Completeness != domain.CompletenessPartial || !hasReason(second.Coverage, "pagination_stalled") {
		t.Fatalf("repeated page = %#v", second)
	}
	if second.Coverage.ObservedCount != 2 {
		t.Fatalf("repeated count = %d, want 2", second.Coverage.ObservedCount)
	}
}

func TestMalformedFilesAndVersionRemainVisibleAsPartial(t *testing.T) {
	handler := &qbitFixtureHandler{
		info:             func(int) ([]byte, int) { return fixture(t, "info-page-1.json"), http.StatusOK },
		properties:       fixture(t, "properties.json"),
		files:            map[string][]byte{fixtureFilmHash: fixture(t, "malformed-files.json")},
		malformedVersion: true,
	}
	client, closeServer := newFixtureClient(t, handler, "qbt-main")
	defer closeServer()

	page, err := client.ListDetailed(context.Background(), "qbt-main", "", 1)
	if err != nil {
		t.Fatalf("ListDetailed: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Item.Descriptor == nil {
		t.Fatalf("malformed result = %#v", page)
	}
	if page.Coverage.Completeness != domain.CompletenessPartial || !hasReason(page.Coverage, "version_observation_partial") || !hasReason(page.Coverage, "item_0_files_malformed") {
		t.Fatalf("malformed coverage = %#v", page.Coverage)
	}
	if page.Items[0].Item.Descriptor.Available {
		t.Fatalf("descriptor should be disabled: %#v", page.Items[0].Item.Descriptor)
	}
}

func TestProcessingStateAndAvailabilitySentinel(t *testing.T) {
	handler := &qbitFixtureHandler{
		info:       func(int) ([]byte, int) { return fixture(t, "info-processing.json"), http.StatusOK },
		properties: fixture(t, "properties.json"),
		files:      map[string][]byte{fixtureProcessHash: fixture(t, "files-processing.json")},
	}
	client, closeServer := newFixtureClient(t, handler, "qbt-main")
	defer closeServer()

	page, err := client.ListDetailed(context.Background(), "qbt-main", "", 1)
	if err != nil {
		t.Fatalf("ListDetailed: %v", err)
	}
	item := page.Items[0]
	if item.Item.ProcessingDone || item.Item.Seeding || item.Item.Progress != 0.25 || len(item.Files) != 1 || item.Files[0].Availability != -1 {
		t.Fatalf("processing observation = %#v", item)
	}
}

func TestTypedErrorsAreSanitized(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*qbitFixtureHandler)
		want      domain.UpstreamErrorCode
		check     func(*testing.T, error)
	}{
		{
			name:      "unauthorized",
			configure: func(handler *qbitFixtureHandler) { handler.loginStatus = http.StatusUnauthorized },
			want:      domain.OutcomeUnauthorized,
			check: func(t *testing.T, err error) {
				if strings.Contains(err.Error(), "fixture-password") || strings.Contains(err.Error(), "fixture-user") {
					t.Fatalf("credentials leaked in error: %v", err)
				}
			},
		},
		{
			name: "rate limited",
			configure: func(handler *qbitFixtureHandler) {
				handler.info = func(int) ([]byte, int) { return nil, http.StatusTooManyRequests }
			},
			want: domain.OutcomeRateLimited,
		},
		{
			name: "malformed response",
			configure: func(handler *qbitFixtureHandler) {
				handler.info = func(int) ([]byte, int) { return fixture(t, "malformed-info.json"), http.StatusOK }
			},
			want: domain.OutcomeUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := &qbitFixtureHandler{info: func(int) ([]byte, int) { return fixture(t, "info-empty.json"), http.StatusOK }}
			test.configure(handler)
			client, closeServer := newFixtureClient(t, handler, "qbt-main")
			defer closeServer()
			_, err := client.List(context.Background(), "qbt-main", "", 1)
			upstream := assertErrorCode(t, err, test.want)
			if upstream.Operation == "" {
				t.Fatal("missing operation")
			}
			if test.check != nil {
				test.check(t, err)
			}
		})
	}

	t.Run("timeout", func(t *testing.T) {
		handler := &qbitFixtureHandler{blockLogin: true, blockDone: make(chan struct{})}
		server := httptest.NewServer(handler)
		client, err := New(Config{ConnectionID: "qbt-main", Endpoint: server.URL, HTTPClient: &http.Client{Timeout: time.Second}})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		_, err = client.List(ctx, "qbt-main", "", 1)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout error = %T %v", err, err)
		}
		close(handler.blockDone)
		server.Close()
	})
}

func TestPathMappingAndRemotePathBoundaries(t *testing.T) {
	if got, ok := remoteFilePath("/downloads/folder.with.dots", "video.mkv", 1); !ok || got != "/downloads/folder.with.dots/video.mkv" {
		t.Fatalf("single file path = %q, %t", got, ok)
	}
	for _, name := range []string{"../escape.mkv", "folder/../../escape.mkv", "./escape.mkv", "folder//escape.mkv"} {
		if got, ok := remoteFilePath("/downloads/folder", name, 2); ok {
			t.Fatalf("unsafe path %q accepted as %q", name, got)
		}
	}

	client := &Client{config: Config{ConnectionID: "qbt-main", Mappings: []domain.PathMapping{
		{ConnectionID: "qbt-main", SourcePrefix: "/downloads", RootID: "short", DestinationPrefix: "short"},
		{ConnectionID: "qbt-main", SourcePrefix: "/downloads/movies", RootID: "library", DestinationPrefix: "movies"},
	}}}
	target, ok, ambiguous := client.mapPath("/downloads/movies/film.mkv")
	if !ok || ambiguous || target.RootID != "library" || target.RelativePath != "movies/film.mkv" {
		t.Fatalf("longest mapping = %#v, %t, %t", target, ok, ambiguous)
	}
	client.config.Mappings = append(client.config.Mappings, domain.PathMapping{ConnectionID: "qbt-main", SourcePrefix: "/downloads/movies", RootID: "other", DestinationPrefix: "movies"})
	if _, ok, ambiguous = client.mapPath("/downloads/movies/film.mkv"); ok || !ambiguous {
		t.Fatal("equal-length mappings should be ambiguous")
	}
}

func TestValidationRejectsEndpointCredentialsAndBadMapping(t *testing.T) {
	for _, config := range []Config{
		{ConnectionID: "qbt-main", Endpoint: "https://user:password@example.test"},
		{ConnectionID: "qbt-main", Endpoint: "https://example.test", Mappings: []domain.PathMapping{{ConnectionID: "qbt-main", RootID: "library", SourcePrefix: "relative"}}},
	} {
		if _, err := New(config); err == nil {
			t.Fatalf("invalid config accepted: %#v", config)
		}
	}
}

func TestCursorRejectsWrongConnection(t *testing.T) {
	handler := &qbitFixtureHandler{info: func(int) ([]byte, int) { return fixture(t, "info-single.json"), http.StatusOK }}
	first, closeFirst := newFixtureClient(t, handler, "qbt-one")
	defer closeFirst()
	second, closeSecond := newFixtureClient(t, handler, "qbt-two")
	defer closeSecond()
	page, err := first.ListDetailed(context.Background(), "qbt-one", "", 1)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	_, err = second.ListDetailed(context.Background(), "qbt-two", page.NextCursor, 1)
	upstream := assertErrorCode(t, err, domain.OutcomeInvalidInput)
	if upstream.Operation != "qbit.inventory.cursor" {
		t.Fatalf("cursor operation = %q", upstream.Operation)
	}
}

func TestErrorDetailsDoNotIncludeEndpoint(t *testing.T) {
	handler := &qbitFixtureHandler{info: func(int) ([]byte, int) { return nil, http.StatusTooManyRequests }}
	client, closeServer := newFixtureClient(t, handler, "qbt-main")
	defer closeServer()
	_, err := client.List(context.Background(), "qbt-main", "", 1)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), "httptest") {
		t.Fatalf("endpoint leaked in error: %v", err)
	}
}

func TestResponseReadBound(t *testing.T) {
	handler := &qbitFixtureHandler{info: func(int) ([]byte, int) { return bytes.Repeat([]byte("x"), 2048), http.StatusOK }}
	server := httptest.NewServer(handler)
	defer server.Close()
	client, err := New(Config{ConnectionID: "qbt-main", Endpoint: server.URL, MaxResponseBytes: 128})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.List(context.Background(), "qbt-main", "", 1)
	assertErrorCode(t, err, domain.OutcomeUnknown)
}

func TestStableFingerprintForFixturePage(t *testing.T) {
	first := fixture(t, "info-page-1.json")
	second := fixture(t, "info-page-1.json")
	if !bytes.Equal(first, second) || fingerprint(first) != fingerprint(second) {
		t.Fatal("fixture fingerprint is not stable")
	}
}

func TestFullPageCursorRoundTrip(t *testing.T) {
	summaries := make([]torrentSummary, 200)
	for index := range summaries {
		summaries[index] = torrentSummary{
			Hash:        fmt.Sprintf("%040x", index+1),
			Name:        fmt.Sprintf("Fixture %03d", index),
			ContentPath: "/downloads/fixture",
			Progress:    1,
			Ratio:       1,
			State:       "uploading",
			HasMetadata: true,
		}
	}
	body, err := json.Marshal(summaries)
	if err != nil {
		t.Fatalf("marshal summaries: %v", err)
	}
	empty := fixture(t, "info-empty.json")
	handler := &qbitFixtureHandler{info: func(offset int) ([]byte, int) {
		if offset == 0 {
			return body, http.StatusOK
		}
		return empty, http.StatusOK
	}}
	client, closeServer := newFixtureClient(t, handler, "qbt-main")
	defer closeServer()

	first, err := client.ListDetailed(context.Background(), "qbt-main", "", 200)
	if err != nil {
		t.Fatalf("full first page: %v", err)
	}
	if len(first.Items) != 200 || first.NextCursor == "" {
		t.Fatalf("full first page = %d items, cursor %q", len(first.Items), first.NextCursor)
	}
	second, err := client.ListDetailed(context.Background(), "qbt-main", first.NextCursor, 200)
	if err != nil {
		t.Fatalf("full cursor continuation: %v", err)
	}
	if len(second.Items) != 0 || second.Coverage.ObservedCount != 200 || second.Coverage.Completeness != domain.CompletenessComplete {
		t.Fatalf("full cursor continuation = %#v", second)
	}
}

func TestMalformedTorrentDescriptorRejected(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("d4:infod4:nameee"), []byte("d4:infod4:name4:testee\n")} {
		if validTorrentDescriptor(data) {
			t.Fatalf("malformed descriptor accepted: %q", data)
		}
	}
	if !validTorrentDescriptor([]byte("d4:infod4:name4:testee")) {
		t.Fatal("valid descriptor rejected")
	}
}

func ExampleClient_ScopedIdentity() {
	client := &Client{config: Config{ConnectionID: "qbt-main"}}
	fmt.Println(client.ScopedIdentity(fixtureFilmHash))
	// Output:
	// qbt-main:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
}
