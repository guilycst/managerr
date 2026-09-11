package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
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

func torrentBody(t *testing.T, hashes ...string) []byte {
	t.Helper()
	summaries := make([]torrentSummary, 0, len(hashes))
	for index, hash := range hashes {
		summaries = append(summaries, torrentSummary{
			Hash: hash, Name: "Synthetic " + strconv.Itoa(index), ContentPath: "/downloads/synthetic",
			Progress: 1, Ratio: 1, State: "uploading", HasMetadata: true,
		})
	}
	body, err := json.Marshal(summaries)
	if err != nil {
		t.Fatalf("marshal torrent fixture: %v", err)
	}
	return body
}

type infoResponse struct {
	offset int
	body   []byte
}

func sequenceInfo(t *testing.T, responses ...infoResponse) func(int) ([]byte, int) {
	t.Helper()
	var mu sync.Mutex
	index := 0
	return func(offset int) ([]byte, int) {
		mu.Lock()
		defer mu.Unlock()
		if index >= len(responses) {
			t.Errorf("unexpected info request at offset %d after %d responses", offset, index)
			return []byte("[]"), http.StatusOK
		}
		response := responses[index]
		index++
		if response.offset != offset {
			t.Errorf("info request offset = %d, want %d at sequence %d", offset, response.offset, index-1)
		}
		return response.body, http.StatusOK
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

type isolatedSessionHandler struct {
	mu        sync.Mutex
	items     map[string][]byte
	infoSIDs  []string
	loginSIDs []string
}

func (handler *isolatedSessionHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case apiLogin:
		_ = request.ParseForm()
		sid := request.Form.Get("username")
		handler.mu.Lock()
		handler.loginSIDs = append(handler.loginSIDs, sid)
		handler.mu.Unlock()
		response.Header().Set("Set-Cookie", "SID="+sid+"; Path=/")
		_, _ = response.Write([]byte("Ok."))
	case apiWebAPIVersion, apiAppVersion:
		_, _ = response.Write([]byte("5.0.4"))
	case apiTorrentInfo:
		cookie, err := request.Cookie("SID")
		if err != nil {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.mu.Lock()
		handler.infoSIDs = append(handler.infoSIDs, cookie.Value)
		body := handler.items[cookie.Value]
		handler.mu.Unlock()
		if body == nil {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = response.Write(body)
	case apiProperties, apiFiles:
		_, _ = response.Write([]byte("[]"))
	default:
		response.WriteHeader(http.StatusNotFound)
	}
}

func TestPrivateCookieJarsIsolateConcurrentInstances(t *testing.T) {
	alphaHash := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	betaHash := "ffffffffffffffffffffffffffffffffffffffff"
	alphaBody := bytes.Replace(fixture(t, "info-single.json"), []byte(fixtureFilmHash), []byte(alphaHash), 1)
	betaBody := bytes.Replace(fixture(t, "info-single.json"), []byte(fixtureFilmHash), []byte(betaHash), 1)
	handler := &isolatedSessionHandler{items: map[string][]byte{"alpha": alphaBody, "beta": betaBody}}
	server := httptest.NewServer(handler)
	defer server.Close()

	sharedJar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	sharedJar.SetCookies(serverURL, []*http.Cookie{{Name: "SID", Value: "shared", Path: "/"}})
	baseClient := &http.Client{Jar: sharedJar, Timeout: time.Second}

	newClient := func(id domain.ConfigID, username string) *Client {
		client, newErr := New(Config{
			ConnectionID: id,
			Endpoint:     server.URL,
			Username:     username,
			HTTPClient:   baseClient,
		})
		if newErr != nil {
			t.Fatalf("New(%s): %v", id, newErr)
		}
		return client
	}
	alpha := newClient("qbt-alpha", "alpha")
	beta := newClient("qbt-beta", "beta")
	if alpha.http.Jar == sharedJar || beta.http.Jar == sharedJar || alpha.http.Jar == beta.http.Jar {
		t.Fatal("adapter instances did not receive private cookie jars")
	}

	var wait sync.WaitGroup
	wait.Add(2)
	results := make(chan struct {
		id   string
		page ports.Page[ports.DownloadItem]
		err  error
	}, 2)
	go func() {
		defer wait.Done()
		page, listErr := alpha.List(context.Background(), "qbt-alpha", "", 1)
		results <- struct {
			id   string
			page ports.Page[ports.DownloadItem]
			err  error
		}{id: "alpha", page: page, err: listErr}
	}()
	go func() {
		defer wait.Done()
		page, listErr := beta.List(context.Background(), "qbt-beta", "", 1)
		results <- struct {
			id   string
			page ports.Page[ports.DownloadItem]
			err  error
		}{id: "beta", page: page, err: listErr}
	}()
	wait.Wait()
	close(results)
	for result := range results {
		if result.err != nil || len(result.page.Items) != 1 {
			t.Fatalf("%s result = %#v, %v", result.id, result.page, result.err)
		}
		wantHash := alphaHash
		if result.id == "beta" {
			wantHash = betaHash
		}
		if result.page.Items[0].ExternalID != wantHash {
			t.Fatalf("%s received SID from another instance: %#v", result.id, result.page.Items[0])
		}
	}
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if len(handler.loginSIDs) != 2 || len(handler.infoSIDs) != 2 || handler.infoSIDs[0] == handler.infoSIDs[1] {
		t.Fatalf("session IDs crossed: logins=%v info=%v", handler.loginSIDs, handler.infoSIDs)
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
	if len(second.Items) != 1 || second.NextCursor != "" || second.Coverage.Completeness != domain.CompletenessPartial || second.Coverage.ObservedCount != 3 || !hasReason(second.Coverage, "pagination_prior_partial") {
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

func TestNonAdjacentOverlapAndMutableSnapshotStayPartial(t *testing.T) {
	hashA := strings.Repeat("1", 40)
	hashB := strings.Repeat("2", 40)
	hashC := strings.Repeat("3", 40)
	hashD := strings.Repeat("4", 40)
	hashE := strings.Repeat("5", 40)
	handler := &qbitFixtureHandler{info: sequenceInfo(t,
		infoResponse{offset: 0, body: torrentBody(t, hashA, hashB)},
		infoResponse{offset: 2, body: torrentBody(t, hashC, hashD)},
		infoResponse{offset: 4, body: torrentBody(t, hashA, hashE)},
		infoResponse{offset: 6, body: torrentBody(t)},
		infoResponse{offset: 0, body: torrentBody(t, hashA, hashB)},
		infoResponse{offset: 2, body: torrentBody(t, hashC, hashD)},
		infoResponse{offset: 4, body: torrentBody(t, hashA, hashE)},
		infoResponse{offset: 6, body: torrentBody(t)},
	)}
	client, closeServer := newFixtureClient(t, handler, "qbt-main")
	defer closeServer()

	seen := make(map[string]struct{})
	cursor := ""
	var last DetailedPage
	for page := 0; page < 4; page++ {
		var err error
		last, err = client.ListDetailed(context.Background(), "qbt-main", cursor, 2)
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		for _, item := range last.Items {
			if _, exists := seen[item.Item.ExternalID]; exists {
				t.Fatalf("duplicate emitted ID %q", item.Item.ExternalID)
			}
			seen[item.Item.ExternalID] = struct{}{}
		}
		cursor = last.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 5 || len(last.Items) != 0 || last.Coverage.Completeness != domain.CompletenessPartial || !hasReason(last.Coverage, "pagination_prior_partial") || !hasReason(last.Coverage, "pagination_snapshot_changed") {
		t.Fatalf("non-adjacent overlap result = %#v, seen=%v", last, seen)
	}
}

func TestDeletionBeforeOffsetAndFinalPageChangeStayPartial(t *testing.T) {
	hashA := strings.Repeat("a", 40)
	hashB := strings.Repeat("b", 40)
	hashC := strings.Repeat("c", 40)
	hashD := strings.Repeat("d", 40)
	deletionHandler := &qbitFixtureHandler{info: sequenceInfo(t,
		infoResponse{offset: 0, body: torrentBody(t, hashA, hashB)},
		infoResponse{offset: 2, body: torrentBody(t, hashD)},
		infoResponse{offset: 0, body: torrentBody(t, hashB, hashC)},
		infoResponse{offset: 2, body: torrentBody(t, hashD)},
	)}
	client, closeServer := newFixtureClient(t, deletionHandler, "qbt-main")
	defer closeServer()
	first, err := client.ListDetailed(context.Background(), "qbt-main", "", 2)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("deletion first page = %#v, %v", first, err)
	}
	second, err := client.ListDetailed(context.Background(), "qbt-main", first.NextCursor, 2)
	if err != nil {
		t.Fatalf("deletion second page: %v", err)
	}
	if second.Coverage.Completeness != domain.CompletenessPartial || !hasReason(second.Coverage, "pagination_snapshot_changed") || second.NextCursor != "" {
		t.Fatalf("deletion result = %#v", second)
	}

	hashX := strings.Repeat("e", 40)
	insertionHandler := &qbitFixtureHandler{info: sequenceInfo(t,
		infoResponse{offset: 0, body: torrentBody(t, hashA, hashB)},
		infoResponse{offset: 2, body: torrentBody(t, hashB, hashC)},
		infoResponse{offset: 4, body: torrentBody(t, hashD)},
		infoResponse{offset: 0, body: torrentBody(t, hashX, hashA)},
		infoResponse{offset: 2, body: torrentBody(t, hashB, hashC)},
		infoResponse{offset: 4, body: torrentBody(t, hashD)},
	)}
	insertionClient, closeInsertion := newFixtureClient(t, insertionHandler, "qbt-insertion")
	defer closeInsertion()
	first, err = insertionClient.ListDetailed(context.Background(), "qbt-insertion", "", 2)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("insertion first page = %#v, %v", first, err)
	}
	second, err = insertionClient.ListDetailed(context.Background(), "qbt-insertion", first.NextCursor, 2)
	if err != nil || second.NextCursor == "" {
		t.Fatalf("insertion second page = %#v, %v", second, err)
	}
	third, err := insertionClient.ListDetailed(context.Background(), "qbt-insertion", second.NextCursor, 2)
	if err != nil {
		t.Fatalf("insertion final page: %v", err)
	}
	if third.Coverage.Completeness != domain.CompletenessPartial || !hasReason(third.Coverage, "pagination_snapshot_changed") {
		t.Fatalf("insertion result = %#v", third)
	}

	finalChangeHandler := &qbitFixtureHandler{info: sequenceInfo(t,
		infoResponse{offset: 0, body: torrentBody(t, hashA, hashB)},
		infoResponse{offset: 2, body: torrentBody(t, hashC)},
		infoResponse{offset: 0, body: torrentBody(t, hashA, hashB)},
		infoResponse{offset: 2, body: torrentBody(t, hashD)},
	)}
	finalClient, closeFinal := newFixtureClient(t, finalChangeHandler, "qbt-final")
	defer closeFinal()
	first, err = finalClient.ListDetailed(context.Background(), "qbt-final", "", 2)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("final-change first page = %#v, %v", first, err)
	}
	second, err = finalClient.ListDetailed(context.Background(), "qbt-final", first.NextCursor, 2)
	if err != nil {
		t.Fatalf("final-change second page: %v", err)
	}
	if second.Coverage.Completeness != domain.CompletenessPartial || !hasReason(second.Coverage, "pagination_snapshot_changed") {
		t.Fatalf("final-change result = %#v", second)
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

func TestUnknownTorrentStateCannotAuthorizeProcessing(t *testing.T) {
	unknown := bytes.Replace(torrentBody(t, fixtureProcessHash), []byte(`"state":"uploading"`), []byte(`"state":"futureUP"`), 1)
	handler := &qbitFixtureHandler{
		info:       func(int) ([]byte, int) { return unknown, http.StatusOK },
		properties: fixture(t, "properties.json"),
		files:      map[string][]byte{fixtureProcessHash: fixture(t, "files-processing.json")},
	}
	client, closeServer := newFixtureClient(t, handler, "qbt-main")
	defer closeServer()
	page, err := client.ListDetailed(context.Background(), "qbt-main", "", 1)
	if err != nil {
		t.Fatalf("ListDetailed: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Item.ProcessingDone || page.Items[0].Item.State != "futureUP" || page.Coverage.Completeness != domain.CompletenessPartial || !hasReason(page.Coverage, "item_0_state_unknown") {
		t.Fatalf("unknown state result = %#v", page)
	}

	terminal := bytes.Replace(torrentBody(t, fixtureProcessHash), []byte(`"state":"uploading"`), []byte(`"state":"stoppedUP"`), 1)
	handler.info = func(int) ([]byte, int) { return terminal, http.StatusOK }
	page, err = client.ListDetailed(context.Background(), "qbt-main", "", 1)
	if err != nil || len(page.Items) != 1 || !page.Items[0].Item.ProcessingDone {
		t.Fatalf("recognized terminal state = %#v, %v", page, err)
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
	files, reasons := client.mapFiles("/downloads/movies", []torrentFile{{
		Index: 0, Name: "film.mkv", Size: 10, Progress: 1, Availability: 1, Seeds: 1, IsSeed: true,
	}}, time.Now().UTC())
	if len(files) != 1 || files[0].Entry.RootID != "" || files[0].Observation.Path != "/downloads/movies/film.mkv" || !hasReason(domain.Coverage{ReasonCodes: reasons}, "payload_mapping_ambiguous") {
		t.Fatalf("ambiguous file evidence = %#v, reasons=%v", files, reasons)
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

func TestCursorAuthenticationAndLargestAcceptedRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client, err := New(Config{ConnectionID: "qbt-main", Endpoint: server.URL, MaxPageSize: maxCursorItems, MaxItems: maxCursorItems})
	if err != nil {
		t.Fatalf("largest accepted config: %v", err)
	}
	sourceID, err := domain.NewRuntimeID()
	if err != nil {
		t.Fatalf("source id: %v", err)
	}
	seen := make([]string, maxCursorItems)
	for index := range seen {
		seen[index] = fmt.Sprintf("%064x", index+1)
	}
	state := inventoryCursor{
		SourceID:         sourceID,
		StartedAt:        time.Now().UTC(),
		Offset:           maxCursorItems,
		PageCount:        1,
		PageSize:         maxCursorItems,
		ObservedCount:    maxCursorItems,
		SeenIDs:          seen,
		PageFingerprints: []string{strings.Repeat("0", sha256.Size*2)},
	}
	token, err := client.encodeCursorChecked(state)
	if err != nil {
		t.Fatalf("largest cursor encode: %v", err)
	}
	if len(token) >= maxEncodedCursorBytes {
		t.Fatalf("largest accepted cursor length = %d, ceiling %d", len(token), maxEncodedCursorBytes)
	}
	decoded, err := client.decodeCursor(token)
	if err != nil || len(decoded.SeenIDs) != maxCursorItems || decoded.PageSize != maxCursorItems {
		t.Fatalf("largest cursor decode = %#v, %v", decoded, err)
	}

	tamper := func(value string, index int) string {
		mutated := []byte(value)
		if mutated[index] == 'A' {
			mutated[index] = 'B'
		} else {
			mutated[index] = 'A'
		}
		return string(mutated)
	}
	for _, candidate := range []string{tamper(token, 0), tamper(token, len(token)-1)} {
		assertErrorCode(t, func() error {
			_, decodeErr := client.decodeCursor(candidate)
			return decodeErr
		}(), domain.OutcomeInvalidInput)
	}
	other, err := New(Config{ConnectionID: "qbt-main", Endpoint: server.URL, MaxPageSize: maxCursorItems, MaxItems: maxCursorItems})
	if err != nil {
		t.Fatalf("second client: %v", err)
	}
	assertErrorCode(t, func() error {
		_, decodeErr := other.decodeCursor(token)
		return decodeErr
	}(), domain.OutcomeInvalidInput)

	if _, err := New(Config{ConnectionID: "qbt-main", Endpoint: server.URL, MaxItems: maxCursorItems + 1}); err == nil {
		t.Fatal("item bound above cursor ceiling accepted")
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
	for _, data := range [][]byte{
		nil,
		[]byte("de"),
		[]byte("d4:infod4:nameee"),
		[]byte("d4:info3:fooe"),
		[]byte("d4:infod4:name4:teste4:infod4:name4:testee"),
		[]byte("d4:infod4:name4:testee\n"),
	} {
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
