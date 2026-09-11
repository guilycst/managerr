package read

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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

type arrFixtureHandler struct {
	mu sync.Mutex

	kind         domain.ConnectionKind
	calls        []string
	manualPosts  []map[string]any
	commandCalls int
}

func (handler *arrFixtureHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	handler.mu.Lock()
	handler.calls = append(handler.calls, request.Method+" "+request.URL.RequestURI())
	handler.mu.Unlock()

	if request.Header.Get("X-Api-Key") != "fixture-api-key" {
		response.WriteHeader(http.StatusUnauthorized)
		return
	}
	if request.Method == http.MethodPost {
		if request.URL.Path == "/api/v3/command" {
			handler.mu.Lock()
			handler.commandCalls++
			handler.mu.Unlock()
			response.WriteHeader(http.StatusInternalServerError)
			return
		}
		if request.URL.Path != apiManualImport {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		var payload []map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		handler.mu.Lock()
		handler.manualPosts = append(handler.manualPosts, payload...)
		handler.mu.Unlock()
		if handler.kind == domain.ConnectionSonarr {
			writeFixture(response, "sonarr-manual-import.json")
		} else {
			writeFixture(response, "radarr-manual-import.json")
		}
		return
	}

	switch request.URL.Path {
	case apiMovies:
		switch request.URL.Query().Get("page") {
		case "1":
			writeFixture(response, "radarr-movies-page-1.json")
		case "2":
			writeFixture(response, "radarr-movies-page-2.json")
		default:
			writeFixture(response, "radarr-movies-page-3.json")
		}
	case apiMovieFiles:
		if request.URL.Query().Get("movieId") == "102" {
			writeFixture(response, "radarr-moviefile-102.json")
		} else {
			writeJSON(response, []any{})
		}
	case apiMovieLookup:
		writeFixture(response, "radarr-lookup.json")
	case apiSeries:
		writeFixture(response, "sonarr-series-page-1.json")
	case apiEpisodes:
		if request.URL.Query().Get("seriesId") == "201" {
			writeFixture(response, "sonarr-episodes-201.json")
		} else {
			writeJSON(response, []any{})
		}
	case apiSeriesLookup:
		writeFixture(response, "sonarr-lookup.json")
	case apiRootFolders:
		writeFixture(response, "radarr-root-folders.json")
	case apiQuality:
		writeFixture(response, "radarr-quality-profiles.json")
	case apiHistory:
		if request.URL.Query().Get("page") == "1" {
			writeFixture(response, "radarr-history-page-1.json")
		} else {
			writeFixture(response, "radarr-history-page-2.json")
		}
	case apiManualImport:
		if handler.kind == domain.ConnectionSonarr {
			writeFixture(response, "sonarr-manual-import.json")
		} else {
			writeFixture(response, "radarr-manual-import.json")
		}
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
	data, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../../../tests/fixtures/arr/read", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func writeFixture(response http.ResponseWriter, name string) {
	response.Header().Set("Content-Type", "application/json")
	_, _ = response.Write(fixtureNoTest(name))
}

func fixtureNoTest(name string) []byte {
	_, source, _, _ := runtime.Caller(0)
	data, _ := os.ReadFile(filepath.Join(filepath.Dir(source), "../../../../tests/fixtures/arr/read", name))
	return data
}

func writeJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(value)
}

func newFixtureClient(t *testing.T, kind domain.ConnectionKind, handler *arrFixtureHandler, connectionID domain.ConfigID) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	client, err := New(Config{
		ConnectionID: connectionID,
		Kind:         kind,
		Endpoint:     server.URL,
		APIKey:       "fixture-api-key",
		RootPaths:    map[domain.ConfigID]string{"library": "/downloads"},
		Mappings: []domain.PathMapping{{
			ConnectionID: connectionID, SourcePrefix: "/downloads", RootID: "library",
		}},
		MaxPageSize: 2,
		MaxPages:    10,
		MaxRecords:  50,
		MaxFiles:    50,
	})
	if err != nil {
		server.Close()
		t.Fatalf("New: %v", err)
	}
	return client, server
}

func assertUpstreamCode(t *testing.T, err error, want domain.UpstreamErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected upstream %s error", want)
	}
	var upstream domain.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("error %T does not carry upstream evidence: %v", err, err)
	}
	if upstream.Code != want {
		t.Fatalf("upstream code = %s, want %s", upstream.Code, want)
	}
}

func TestRadarrCatalogLookupOptionsAndHistory(t *testing.T) {
	handler := &arrFixtureHandler{kind: domain.ConnectionRadarr}
	client, server := newFixtureClient(t, domain.ConnectionRadarr, handler, "radarr-main")
	defer server.Close()

	first, err := client.List(context.Background(), "radarr-main", "", 1)
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" || first.Coverage.Completeness != domain.CompletenessPartial {
		t.Fatalf("first catalog page = %#v, %v", first, err)
	}
	second, err := client.List(context.Background(), "radarr-main", first.NextCursor, 1)
	if err != nil || len(second.Items) != 1 || second.NextCursor == "" {
		t.Fatalf("second catalog page = %#v, %v", second, err)
	}
	third, err := client.List(context.Background(), "radarr-main", second.NextCursor, 1)
	if err != nil || len(third.Items) != 0 || third.NextCursor != "" || third.Coverage.Completeness != domain.CompletenessComplete || third.Coverage.ObservedCount != 2 {
		t.Fatalf("final catalog page = %#v, %v", third, err)
	}
	if first.Items[0].ExternalID != "101" || first.Items[0].ProviderID != "4242" || first.Items[0].Files[0].Path.RelativePath != "movies/Synthetic Film (2024).mkv" {
		t.Fatalf("first movie = %#v", first.Items[0])
	}
	if second.Items[0].ExternalID != "102" || second.Items[0].Monitored || len(second.Items[0].Files) != 1 || second.Items[0].Files[0].Path.RelativePath != "movies/Missing Synthetic Film.mkv" {
		t.Fatalf("second movie = %#v", second.Items[0])
	}

	lookup, err := client.Lookup(context.Background(), "radarr-main", "4242", domain.MediaMovie)
	if err != nil || len(lookup) != 1 || lookup[0].ExternalID != "103" {
		t.Fatalf("lookup = %#v, %v", lookup, err)
	}
	options, err := client.Options(context.Background(), "radarr-main")
	if err != nil || len(options.RootFolders) != 2 || len(options.QualityProfiles) != 1 || options.QualityProfiles[0].ID != "7" {
		t.Fatalf("options = %#v, %v", options, err)
	}
	history, err := client.History(context.Background(), "radarr-main", "", 1)
	if err != nil || len(history.Items) != 1 || history.NextCursor == "" {
		t.Fatalf("history first page = %#v, %v", history, err)
	}
	historyEnd, err := client.History(context.Background(), "radarr-main", history.NextCursor, 1)
	if err != nil || len(historyEnd.Items) != 1 || historyEnd.NextCursor != "" || historyEnd.Coverage.Completeness != domain.CompletenessComplete {
		t.Fatalf("history final page = %#v, %v", historyEnd, err)
	}
	if history.Items[0].Date.Location() != time.UTC || history.Items[0].MovieID != "101" || historyEnd.Items[0].Destination == "" {
		t.Fatalf("history evidence = %#v / %#v", history.Items[0], historyEnd.Items[0])
	}

	capabilities, err := client.Capabilities(context.Background(), "radarr-main")
	if err != nil || len(capabilities) != 4 || capabilities[3].State != domain.CapabilityUnknown {
		t.Fatalf("capabilities = %#v, %v", capabilities, err)
	}
	other, otherServer := newFixtureClient(t, domain.ConnectionRadarr, &arrFixtureHandler{}, "radarr-main")
	defer otherServer.Close()
	assertUpstreamCode(t, func() error {
		_, listErr := other.List(context.Background(), "radarr-main", first.NextCursor, 1)
		return listErr
	}(), domain.OutcomeInvalidInput)

	handler.mu.Lock()
	calls := append([]string(nil), handler.calls...)
	handler.mu.Unlock()
	for _, call := range calls {
		if strings.Contains(call, "/api/v3/command") {
			t.Fatalf("read adapter called command endpoint: %s", call)
		}
	}
}

func TestSonarrMultiEpisodeMappingAndAnimeLookup(t *testing.T) {
	handler := &arrFixtureHandler{kind: domain.ConnectionSonarr}
	client, server := newFixtureClient(t, domain.ConnectionSonarr, handler, "sonarr-main")
	defer server.Close()

	page, err := client.List(context.Background(), "sonarr-main", "", 2)
	if err != nil || len(page.Items) != 1 || page.Coverage.Completeness != domain.CompletenessPartial {
		t.Fatalf("series page = %#v, %v", page, err)
	}
	if len(page.Items[0].Files) != 1 || len(page.Items[0].Files[0].EpisodeIDs) != 2 || page.Items[0].Files[0].EpisodeIDs[0] != "301" || page.Items[0].Files[0].EpisodeIDs[1] != "302" {
		t.Fatalf("multi-episode file = %#v", page.Items[0].Files)
	}
	if !hasReason(page.Coverage, "episode_file_mapping_missing") {
		t.Fatalf("unmapped episode file did not remain partial: %#v", page.Coverage)
	}
	lookup, err := client.Lookup(context.Background(), "sonarr-main", "6363", domain.MediaAnime)
	if err != nil || len(lookup) != 1 || lookup[0].Kind != domain.MediaAnime {
		t.Fatalf("anime lookup = %#v, %v", lookup, err)
	}

	observation, err := client.ObserveImport(context.Background(), "sonarr-main", "201")
	if err != nil || observation.Effect != nil || len(observation.Files) != 1 {
		t.Fatalf("sonarr observe = %#v, %v", observation, err)
	}
	if got := observation.Files[0].EpisodeIDs; len(got) != 2 {
		t.Fatalf("sonarr observed episode IDs = %#v", got)
	}
}

func TestPreviewReprocessAndNativeRejectionEvidence(t *testing.T) {
	handler := &arrFixtureHandler{kind: domain.ConnectionRadarr}
	client, server := newFixtureClient(t, domain.ConnectionRadarr, handler, "radarr-main")
	defer server.Close()

	files := []ports.ImportFile{
		{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/Synthetic Film (2024).mkv"}, MovieOrEpisodeID: "101"},
		{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/Other Film.mkv"}, MovieOrEpisodeID: "101"},
	}
	preview, err := client.PreviewImport(context.Background(), "radarr-main", ports.ImportPreviewRequest{
		RegisteredExternalID: "101", Files: files, Transfer: "copy",
	})
	if err != nil || preview.Revision == "" || len(preview.Files) != 1 || len(preview.Rejections) != 1 {
		t.Fatalf("preview = %#v, %v", preview, err)
	}
	if preview.Files[0].Source.RelativePath != files[0].Source.RelativePath || preview.Rejections[0].Code != "ExistingFile" || preview.Rejections[0].Reason == "" {
		t.Fatalf("preview evidence = %#v", preview)
	}
	subtitle, err := client.PreviewImport(context.Background(), "radarr-main", ports.ImportPreviewRequest{
		RegisteredExternalID: "101", Transfer: "copy", Files: []ports.ImportFile{{
			Source:           domain.FileTarget{RootID: "library", RelativePath: "incoming/Synthetic Film (2024).en.srt"},
			MovieOrEpisodeID: "101", Subtitle: true, Language: "eng", Forced: true, HearingImpaired: true,
		}},
	})
	if err != nil || len(subtitle.Files) != 1 || len(subtitle.Rejections) != 0 || !subtitle.Files[0].Forced || !subtitle.Files[0].HearingImpaired {
		t.Fatalf("subtitle preview = %#v, %v", subtitle, err)
	}
	handler.mu.Lock()
	previewCall := handler.calls[len(handler.calls)-1]
	handler.mu.Unlock()
	parsedPreviewURL, err := url.Parse(strings.TrimPrefix(previewCall, "GET "))
	if err != nil || parsedPreviewURL.Query().Get("movieId") != "" || parsedPreviewURL.Query().Get("downloadId") != "" {
		t.Fatalf("unscoped preview query = %s", previewCall)
	}
	_, err = client.PreviewImportWithDownloadID(context.Background(), "radarr-main", NativePreviewRequest{
		Request: ports.ImportPreviewRequest{RegisteredExternalID: "101", Files: files[:1], Transfer: "copy"}, DownloadID: "synthetic-download-1",
	})
	if err != nil {
		t.Fatalf("source-scoped preview: %v", err)
	}
	handler.mu.Lock()
	scopedPreviewCall := handler.calls[len(handler.calls)-1]
	handler.mu.Unlock()
	parsedScopedURL, err := url.Parse(strings.TrimPrefix(scopedPreviewCall, "GET "))
	if err != nil || parsedScopedURL.Query().Get("movieId") != "101" || parsedScopedURL.Query().Get("downloadId") != "synthetic-download-1" {
		t.Fatalf("source-scoped preview query = %s", scopedPreviewCall)
	}

	reprocessed, err := client.ReprocessPreview(context.Background(), "radarr-main", ReprocessPreviewRequest{
		RegisteredExternalID: "101", Transfer: "copy", Files: []ReprocessFile{{
			Source:           domain.FileTarget{RootID: "library", RelativePath: "incoming/Synthetic Film (2024).mkv"},
			MovieOrEpisodeID: "101", DownloadID: "synthetic-download-1",
		}},
	})
	if err != nil || len(reprocessed.Files) != 1 || len(reprocessed.Rejections) != 0 {
		t.Fatalf("reprocessed preview = %#v, %v", reprocessed, err)
	}

	handler.mu.Lock()
	posts := append([]map[string]any(nil), handler.manualPosts...)
	commandCalls := handler.commandCalls
	calls := append([]string(nil), handler.calls...)
	handler.mu.Unlock()
	if len(posts) != 1 || posts[0]["movieId"] != float64(101) || posts[0]["path"] != "/downloads/incoming/Synthetic Film (2024).mkv" || posts[0]["downloadId"] != "synthetic-download-1" || posts[0]["importMode"] != nil {
		t.Fatalf("reprocess payload = %#v", posts)
	}
	if commandCalls != 0 {
		t.Fatalf("command endpoint was invoked %d times", commandCalls)
	}
	for _, call := range calls {
		if strings.Contains(call, "/api/v3/command") {
			t.Fatalf("command endpoint appeared in calls: %s", call)
		}
	}
}

func TestSonarrPreviewUsesEpisodeIDs(t *testing.T) {
	handler := &arrFixtureHandler{kind: domain.ConnectionSonarr}
	client, server := newFixtureClient(t, domain.ConnectionSonarr, handler, "sonarr-main")
	defer server.Close()

	preview, err := client.PreviewImport(context.Background(), "sonarr-main", ports.ImportPreviewRequest{
		RegisteredExternalID: "201",
		Files: []ports.ImportFile{
			{Source: domain.FileTarget{RootID: "library", RelativePath: "series/Synthetic Series - S01E01.mkv"}, MovieOrEpisodeID: "301"},
			{Source: domain.FileTarget{RootID: "library", RelativePath: "series/Synthetic Series - S01E02.mkv"}, MovieOrEpisodeID: "302"},
		}, Transfer: "hardlink",
	})
	if err != nil || len(preview.Files) != 1 || len(preview.Rejections) != 1 || preview.Rejections[0].Code != "EpisodeFileExists" {
		t.Fatalf("sonarr preview = %#v, %v", preview, err)
	}

	reprocessed, err := client.ReprocessPreview(context.Background(), "sonarr-main", ReprocessPreviewRequest{
		RegisteredExternalID: "201", Transfer: "hardlink", Files: []ReprocessFile{{
			Source:           domain.FileTarget{RootID: "library", RelativePath: "series/Synthetic Series - S01E01.mkv"},
			MovieOrEpisodeID: "301", EpisodeIDs: []string{"301", "302"},
		}},
	})
	if err != nil || len(reprocessed.Files) != 1 {
		t.Fatalf("sonarr reprocessed preview = %#v, %v", reprocessed, err)
	}
	handler.mu.Lock()
	posts := append([]map[string]any(nil), handler.manualPosts...)
	handler.mu.Unlock()
	if len(posts) != 1 || posts[0]["seriesId"] != float64(201) || posts[0]["importMode"] != nil {
		t.Fatalf("sonarr reprocess payload = %#v", posts)
	}
	episodeIDs, ok := posts[0]["episodeIds"].([]any)
	if !ok || len(episodeIDs) != 2 || episodeIDs[0] != float64(301) || episodeIDs[1] != float64(302) {
		t.Fatalf("sonarr episode IDs = %#v", posts[0]["episodeIds"])
	}
}

func TestArrBoundsMappingsAndContext(t *testing.T) {
	for _, config := range []Config{
		{ConnectionID: "radarr-main", Kind: domain.ConnectionRadarr, Endpoint: "https://user:password@example.test"},
		{ConnectionID: "radarr-main", Kind: domain.ConnectionRadarr, Endpoint: "https://example.test?secret=value"},
		{ConnectionID: "radarr-main", Kind: domain.ConnectionRadarr, Endpoint: "https://example.test", RootPaths: map[domain.ConfigID]string{"library": "relative"}},
		{ConnectionID: "radarr-main", Kind: domain.ConnectionRadarr, Endpoint: "https://example.test", Mappings: []domain.PathMapping{{ConnectionID: "radarr-main", RootID: "library", SourcePrefix: "relative"}}},
	} {
		if _, err := New(config); err == nil {
			t.Fatalf("invalid Arr config accepted: %#v", config)
		}
	}
	handler := &arrFixtureHandler{}
	client, server := newFixtureClient(t, domain.ConnectionRadarr, handler, "radarr-main")
	defer server.Close()
	if _, err := client.List(context.Background(), "radarr-main", "", client.config.MaxPageSize+1); err == nil {
		t.Fatal("page limit above configured bound accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.List(ctx, "radarr-main", "", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled List error = %v", err)
	}

	ambiguous := &Client{config: Config{
		ConnectionID: "radarr-main",
		Mappings: []domain.PathMapping{
			{ConnectionID: "radarr-main", SourcePrefix: "/downloads", RootID: "library"},
			{ConnectionID: "radarr-main", SourcePrefix: "/downloads", RootID: "other"},
		},
	}}
	if _, mapped, isAmbiguous := ambiguous.mapPath("/downloads/file.mkv"); mapped || !isAmbiguous {
		t.Fatal("equal path mappings did not remain ambiguous")
	}
	if _, mapped, isAmbiguous := ambiguous.mapPath("/downloads-other/file.mkv"); mapped || isAmbiguous {
		t.Fatal("prefix mapping crossed a component boundary")
	}
	if _, err := client.PreviewImport(context.Background(), "radarr-main", ports.ImportPreviewRequest{
		RegisteredExternalID: "101", Transfer: "copy", Files: []ports.ImportFile{{
			Source: domain.FileTarget{RootID: "library", RelativePath: "../escape.mkv"}, MovieOrEpisodeID: "101",
		}},
	}); err == nil {
		t.Fatal("unsafe preview target accepted")
	}
}

func hasReason(coverage domain.Coverage, want string) bool {
	for _, reason := range coverage.ReasonCodes {
		if reason == want {
			return true
		}
	}
	return false
}
