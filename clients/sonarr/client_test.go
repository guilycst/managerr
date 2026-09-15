package sonarr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func newFixtureClient(t *testing.T, handler http.Handler, config Config) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	if config.Endpoint == "" {
		config.Endpoint = server.URL + "/proxy"
	}
	if config.APIKey == "" {
		config.APIKey = "synthetic-sonarr-key"
	}
	client, err := New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client, server
}

func writeFixtureJSON(t *testing.T, writer http.ResponseWriter, value string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(writer, value)
}

func TestStatusAndVersionUseAPIKeyAndSafePrefix(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var keys []string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		paths = append(paths, request.URL.Path)
		keys = append(keys, request.Header.Get("X-Api-Key"))
		mu.Unlock()
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		switch request.URL.Path {
		case "/proxy/api/v3/system/status":
			writeFixtureJSON(t, writer, `{"version":"3.0.10","branch":"main","appName":"Sonarr","instanceName":"synthetic"}`)
		default:
			http.NotFound(writer, request)
		}
	})
	client, _ := newFixtureClient(t, handler, Config{})
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Version != "3.0.10" || status.Branch != "main" || status.AppName != "Sonarr" || status.InstanceName != "synthetic" {
		t.Fatalf("status = %#v", status)
	}
	version, err := client.Version(context.Background())
	if err != nil || version != "3.0.10" {
		t.Fatalf("Version() = %q, %v", version, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != "/proxy/api/v3/system/status" || paths[1] != paths[0] {
		t.Fatalf("paths = %#v", paths)
	}
	for _, key := range keys {
		if key != "synthetic-sonarr-key" {
			t.Fatalf("API key header = %q", key)
		}
	}
}

func TestListSeriesReportsCompleteCoverageAndCopiesPointers(t *testing.T) {
	body := `[{
		"id": 42,
		"title": "Synthetic Show",
		"year": 2024,
		"path": "/library/tv/Synthetic Show",
		"monitored": true,
		"seasonFolder": true,
		"seriesType": "standard",
		"tvdbId": 12345,
		"tvMazeId": 456,
		"imdbId": "tt0000001",
		"providerIds": {"tvdb": "12345", "tvmaze": "456"},
		"newUpstreamField": {"ignored": true}
	}]`
	var gotKey string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotKey = request.Header.Get("X-Api-Key")
		if request.URL.Path != "/proxy/api/v3/series" {
			http.NotFound(writer, request)
			return
		}
		writeFixtureJSON(t, writer, body)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	page, err := client.ListSeries(context.Background())
	if err != nil {
		t.Fatalf("ListSeries() error = %v", err)
	}
	if page.NextCursor != "" || page.Coverage.Completeness != CompletenessComplete || page.Coverage.ObservedCount != 1 || page.Coverage.ObservedAt.IsZero() {
		t.Fatalf("coverage = %#v", page.Coverage)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %#v", page.Items)
	}
	series := page.Items[0]
	if series.ID != 42 || series.Title != "Synthetic Show" || series.Path == "" || !series.Monitored || series.Year == nil || *series.Year != 2024 || series.TVDBID == nil || *series.TVDBID != 12345 || series.ProviderIDs["tvdb"] != "12345" {
		t.Fatalf("series = %#v", series)
	}
	series.ProviderIDs["tvdb"] = "changed-locally"
	if gotKey != "synthetic-sonarr-key" {
		t.Fatalf("API key = %q", gotKey)
	}
}

func TestGetSeriesRejectsWrongIdentityAndMapsNotFound(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/proxy/api/v3/series/42":
			writeFixtureJSON(t, writer, `{"id":43,"title":"Wrong","path":"/library/wrong","monitored":false}`)
		case "/proxy/api/v3/series/404":
			writer.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(writer, `private upstream detail`)
		default:
			http.NotFound(writer, request)
		}
	})
	client, _ := newFixtureClient(t, handler, Config{})
	if _, err := client.GetSeries(context.Background(), 42); !IsCode(err, ErrorMalformed) {
		t.Fatalf("wrong identity error = %v", err)
	}
	if _, err := client.GetSeries(context.Background(), 404); !IsCode(err, ErrorNotFound) {
		t.Fatalf("not found error = %v", err)
	}
	if strings.Contains(fmt.Sprint(client), "synthetic-sonarr-key") {
		t.Fatalf("client formatting exposed API key")
	}
}

func TestOptionsAndEpisodeFileObservations(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/proxy/api/v3/rootfolder":
			writeFixtureJSON(t, writer, `[{"id":1,"path":"/library/tv","accessible":true,"freeSpace":1234}]`)
		case "/proxy/api/v3/qualityprofile":
			writeFixtureJSON(t, writer, `[{"id":2,"name":"HD-1080p"}]`)
		case "/proxy/api/v3/episode":
			if request.URL.Query().Get("seriesId") != "42" || request.URL.Query().Get("includeEpisodeFile") != "true" {
				t.Errorf("episode query = %s", request.URL.RawQuery)
			}
			writeFixtureJSON(t, writer, `[{"id":1001,"seriesId":42,"episodeFileId":9001,"seasonNumber":1,"episodeNumber":2,"absoluteEpisodeNumber":12,"hasFile":true,"title":"Second","episodeFile":{"id":9001,"seriesId":42,"path":"/library/tv/Synthetic Show/Season 01/episode.mkv","relativePath":"Season 01/episode.mkv","size":100,"episodeIds":[1001],"dateAdded":"2024-01-02T03:04:05Z"}}]`)
		case "/proxy/api/v3/episodefile":
			if request.URL.Query().Get("seriesId") != "42" {
				t.Errorf("episode-file query = %s", request.URL.RawQuery)
			}
			writeFixtureJSON(t, writer, `[{"id":9001,"seriesId":42,"path":"/library/tv/Synthetic Show/Season 01/episode.mkv","relativePath":"Season 01/episode.mkv","size":100,"episodeIds":[1001]}]`)
		default:
			http.NotFound(writer, request)
		}
	})
	client, _ := newFixtureClient(t, handler, Config{})
	roots, err := client.ListRootFolders(context.Background())
	if err != nil || len(roots.Items) != 1 || roots.Items[0].FreeSpace == nil || *roots.Items[0].FreeSpace != 1234 {
		t.Fatalf("roots = %#v, err=%v", roots, err)
	}
	profiles, err := client.ListQualityProfiles(context.Background())
	if err != nil || len(profiles.Items) != 1 || profiles.Items[0].Name != "HD-1080p" {
		t.Fatalf("profiles = %#v, err=%v", profiles, err)
	}
	episodes, err := client.ListEpisodesWithFiles(context.Background(), 42)
	if err != nil || len(episodes.Items) != 1 {
		t.Fatalf("episodes = %#v, err=%v", episodes, err)
	}
	episode := episodes.Items[0]
	if episode.ID != 1001 || episode.SeriesID != 42 || episode.EpisodeFile == nil || episode.EpisodeFile.ID != 9001 || episode.EpisodeFile.SeriesID != 42 || episode.EpisodeFileID == nil || *episode.EpisodeFileID != 9001 || episode.AbsoluteEpisodeNumber == nil || *episode.AbsoluteEpisodeNumber != 12 {
		t.Fatalf("episode = %#v", episode)
	}
	files, err := client.ListEpisodeFiles(context.Background(), 42)
	if err != nil || len(files.Items) != 1 || files.Items[0].SeriesID != 42 || files.Items[0].EpisodeIDs[0] != 1001 {
		t.Fatalf("files = %#v, err=%v", files, err)
	}
}

func TestListEpisodesCanExplicitlyOmitNestedFiles(t *testing.T) {
	var gotInclude string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotInclude = request.URL.Query().Get("includeEpisodeFile")
		writeFixtureJSON(t, writer, `[{"id":1001,"seriesId":42,"seasonNumber":1,"episodeNumber":2,"hasFile":false,"episodeFileId":0}]`)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	page, err := client.ListEpisodes(context.Background(), 42, false)
	if err != nil || len(page.Items) != 1 || page.Items[0].EpisodeFile != nil {
		t.Fatalf("episodes = %#v, err=%v", page, err)
	}
	if gotInclude != "false" {
		t.Fatalf("includeEpisodeFile = %q", gotInclude)
	}
}

func TestListEpisodesRejectsConflictingNestedFileIdentity(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeFixtureJSON(t, writer, `[{"id":1001,"seriesId":42,"episodeFileId":9002,"seasonNumber":1,"episodeNumber":2,"hasFile":true,"episodeFile":{"id":9001,"seriesId":42,"path":"/library/episode.mkv","size":10}}]`)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	if _, err := client.ListEpisodesWithFiles(context.Background(), 42); !IsCode(err, ErrorMalformed) {
		t.Fatalf("conflicting file error = %v", err)
	}
}

func TestManualImportPreviewIsReadOnlyAndPreservesTypedEvidence(t *testing.T) {
	var method string
	var queryValues map[string]string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		method = request.Method
		queryValues = map[string]string{
			"folder":              request.URL.Query().Get("folder"),
			"filterExistingFiles": request.URL.Query().Get("filterExistingFiles"),
			"seriesId":            request.URL.Query().Get("seriesId"),
			"downloadId":          request.URL.Query().Get("downloadId"),
		}
		writeFixtureJSON(t, writer, `[{"id":17,"path":"/downloads/Synthetic Show/episode.mkv","relativePath":"episode.mkv","folderName":"/downloads/Synthetic Show","name":"episode.mkv","size":123,"series":{"id":42,"title":"Synthetic Show","tvdbId":12345},"seasonNumber":1,"episodes":[{"id":1001,"seriesId":42,"seasonNumber":1,"episodeNumber":2,"episodeFileId":9001},{"id":1002,"seriesId":42,"seasonNumber":1,"episodeNumber":3}],"episodeFileId":0,"releaseGroup":"synthetic-group","quality":{"quality":{"id":7,"name":"HDTV-1080p","source":"tv","resolution":1080},"revision":{"version":1,"real":0,"isRepack":false}},"language":{"id":1,"name":"English"},"languages":[{"id":1,"name":"English"}],"downloadId":"qbt-17","releaseType":"singleEpisode","customFormatScore":10,"indexerFlags":1,"forced":false,"hearingImpaired":true,"rejections":[{"type":"ExistingFile","reason":"already present"}]}]`)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	page, err := client.PreviewManualImport(context.Background(), ManualImportQuery{Folder: "/downloads/Synthetic Show", FilterExistingFiles: true, DownloadID: "qbt-17"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("preview = %#v, err=%v", page, err)
	}
	candidate := page.Items[0]
	if method != http.MethodGet || queryValues["folder"] != "/downloads/Synthetic Show" || queryValues["filterExistingFiles"] != "true" || queryValues["seriesId"] != "" || queryValues["downloadId"] != "qbt-17" {
		t.Fatalf("request method/query = %s %#v", method, queryValues)
	}
	if candidate.Series == nil || candidate.Series.ID != 42 || len(candidate.Episodes) != 2 || candidate.Episodes[1].ID != 1002 || candidate.Quality == nil || candidate.Quality.Quality == nil || candidate.Quality.Quality.Resolution == nil || *candidate.Quality.Quality.Resolution != 1080 || candidate.Language == nil || candidate.Language.Name != "English" || len(candidate.Languages) != 1 || candidate.Languages[0].Name != "English" || candidate.Forced == nil || *candidate.Forced || candidate.HearingImpaired == nil || !*candidate.HearingImpaired || len(candidate.Rejections) != 1 || candidate.Rejections[0].Type != "ExistingFile" || candidate.Rejections[0].Reason != "already present" || candidate.Rejections[0].Message != "already present" {
		t.Fatalf("candidate = %#v", candidate)
	}
}

func TestPreviewManualImportRejectsSeriesScopeCombination(t *testing.T) {
	var calls int
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		http.Error(writer, "unexpected request", http.StatusTeapot)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	seriesID := int64(42)
	_, err := client.PreviewManualImport(context.Background(), ManualImportQuery{Folder: "/downloads/Show", SeriesID: &seriesID})
	if !IsCode(err, ErrorInvalidInput) {
		t.Fatalf("ambiguous preview error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("ambiguous preview reached network: %d", calls)
	}
}

func TestPreviewLibraryImportUsesNativeSeriesScope(t *testing.T) {
	var gotQuery map[string]string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/proxy/api/v3/manualimport" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		gotQuery = map[string]string{
			"folder":              request.URL.Query().Get("folder"),
			"seriesId":            request.URL.Query().Get("seriesId"),
			"seasonNumber":        request.URL.Query().Get("seasonNumber"),
			"filterExistingFiles": request.URL.Query().Get("filterExistingFiles"),
			"downloadId":          request.URL.Query().Get("downloadId"),
		}
		writeFixtureJSON(t, writer, `[{"id":17,"path":"/library/Synthetic Show/Episode.mkv","relativePath":"Episode.mkv","name":"Episode.mkv","size":123,"series":{"id":42,"title":"Synthetic Show"},"seasonNumber":1,"episodes":[{"id":1001,"seriesId":42,"seasonNumber":1,"episodeNumber":2}]}]`)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	season := int32(1)
	page, err := client.PreviewLibraryImport(context.Background(), LibraryImportQuery{SeriesID: 42, SeasonNumber: &season, FilterExistingFiles: true})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("library preview = %#v, err=%v", page, err)
	}
	if gotQuery["seriesId"] != "42" || gotQuery["seasonNumber"] != "1" || gotQuery["filterExistingFiles"] != "true" || gotQuery["folder"] != "" || gotQuery["downloadId"] != "" {
		t.Fatalf("native library query = %#v", gotQuery)
	}
	if page.Items[0].Path != "/library/Synthetic Show/Episode.mkv" || page.Items[0].Series == nil || page.Items[0].Series.ID != 42 {
		t.Fatalf("library candidate = %#v", page.Items[0])
	}
}

func TestManualImportRejectsSeriesAssociationMismatch(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeFixtureJSON(t, writer, `[{"id":17,"path":"/downloads/episode.mkv","relativePath":"episode.mkv","name":"episode.mkv","size":123,"series":{"id":99,"title":"Other"}}]`)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	if _, err := client.PreviewLibraryImport(context.Background(), LibraryImportQuery{SeriesID: 42}); !IsCode(err, ErrorMalformed) {
		t.Fatalf("association mismatch error = %v", err)
	}
}

func TestPreviewLibraryImportRejectsForeignNestedEpisode(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeFixtureJSON(t, writer, `[{"id":17,"path":"/library/Synthetic Show/Episode.mkv","relativePath":"Episode.mkv","name":"Episode.mkv","size":123,"series":{"id":42,"title":"Synthetic Show"},"episodes":[{"id":1001,"seriesId":99,"seasonNumber":1,"episodeNumber":2}]}]`)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	if _, err := client.PreviewLibraryImport(context.Background(), LibraryImportQuery{SeriesID: 42}); !IsCode(err, ErrorMalformed) {
		t.Fatalf("foreign nested episode error = %v", err)
	}
}

func TestListEpisodeFilesRejectsForeignSeries(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeFixtureJSON(t, writer, `[{"id":9001,"seriesId":99,"path":"/library/Other/Episode.mkv","size":100}]`)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	if _, err := client.ListEpisodeFiles(context.Background(), 42); !IsCode(err, ErrorMalformed) {
		t.Fatalf("foreign episode-file error = %v", err)
	}
}

func TestListEpisodesRejectsMalformedNestedEvidence(t *testing.T) {
	tests := map[string]string{
		"missing-size":   `[{"id":1001,"seriesId":42,"seasonNumber":1,"episodeNumber":2,"hasFile":true,"episodeFile":{"id":9001,"seriesId":42,"path":"/library/episode.mkv"}}]`,
		"foreign-series": `[{"id":1001,"seriesId":42,"seasonNumber":1,"episodeNumber":2,"hasFile":true,"episodeFile":{"id":9001,"seriesId":99,"path":"/library/episode.mkv","size":10}}]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeFixtureJSON(t, writer, body)
			})
			client, _ := newFixtureClient(t, handler, Config{})
			if _, err := client.ListEpisodesWithFiles(context.Background(), 42); !IsCode(err, ErrorMalformed) {
				t.Fatalf("nested evidence error = %v", err)
			}
		})
	}
}

func TestPreviewManualImportRejectsMissingNestedEvidence(t *testing.T) {
	tests := map[string]string{
		"episode-number":   `[{"id":17,"path":"/downloads/episode.mkv","relativePath":"episode.mkv","name":"episode.mkv","size":123,"series":{"id":42,"title":"Synthetic Show"},"episodes":[{"id":1001,"seriesId":42,"seasonNumber":1}]}]`,
		"language-name":    `[{"id":17,"path":"/downloads/episode.mkv","relativePath":"episode.mkv","name":"episode.mkv","size":123,"language":{"id":1}}]`,
		"rejection-reason": `[{"id":17,"path":"/downloads/episode.mkv","relativePath":"episode.mkv","name":"episode.mkv","size":123,"rejections":[{"type":"ExistingFile"}]}]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeFixtureJSON(t, writer, body)
			})
			client, _ := newFixtureClient(t, handler, Config{})
			if _, err := client.PreviewManualImport(context.Background(), ManualImportQuery{Folder: "/downloads"}); !IsCode(err, ErrorMalformed) {
				t.Fatalf("missing nested evidence error = %v", err)
			}
		})
	}
}

func TestPreviewManualImportRejectsNonEquivalentLanguageAliasSets(t *testing.T) {
	tests := map[string]string{
		"different-language": `[{"id":17,"path":"/downloads/episode.mkv","relativePath":"episode.mkv","name":"episode.mkv","size":123,"language":{"id":1,"name":"English"},"languages":[{"id":2,"name":"Portuguese"}]}]`,
		"plural-superset":    `[{"id":17,"path":"/downloads/episode.mkv","relativePath":"episode.mkv","name":"episode.mkv","size":123,"language":{"id":1,"name":"English"},"languages":[{"id":1,"name":"English"},{"id":2,"name":"Portuguese"}]}]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeFixtureJSON(t, writer, body)
			})
			client, _ := newFixtureClient(t, handler, Config{})
			if _, err := client.PreviewManualImport(context.Background(), ManualImportQuery{Folder: "/downloads"}); !IsCode(err, ErrorMalformed) {
				t.Fatalf("language alias error = %v", err)
			}
		})
	}
}

func TestPreviewManualImportAcceptsEquivalentLanguageAliasesRegardlessOfOrder(t *testing.T) {
	tests := map[string]string{
		"language-first":  `[{"id":17,"path":"/downloads/episode.mkv","relativePath":"episode.mkv","name":"episode.mkv","size":123,"language":{"id":1,"name":"English"},"languages":[{"id":1,"name":"English"}]}]`,
		"languages-first": `[{"id":17,"path":"/downloads/episode.mkv","relativePath":"episode.mkv","name":"episode.mkv","size":123,"languages":[{"id":1,"name":"English"}],"language":{"id":1,"name":"English"}}]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeFixtureJSON(t, writer, body)
			})
			client, _ := newFixtureClient(t, handler, Config{})
			page, err := client.PreviewManualImport(context.Background(), ManualImportQuery{Folder: "/downloads"})
			if err != nil || len(page.Items) != 1 || page.Items[0].Language == nil || page.Items[0].Language.ID != 1 || len(page.Items[0].Languages) != 1 || page.Items[0].Languages[0].ID != 1 {
				t.Fatalf("language aliases = %#v, err=%v", page, err)
			}
		})
	}
}

func TestMalformedJSONDuplicateAndMissingMembersAreRejected(t *testing.T) {
	tests := map[string]string{
		"duplicate": `[{"id":1,"id":2,"title":"Synthetic","path":"/library","monitored":true}]`,
		"missing":   `[{"id":1,"title":"Synthetic","path":"/library"}]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeFixtureJSON(t, writer, body)
			})
			client, _ := newFixtureClient(t, handler, Config{})
			if _, err := client.ListSeries(context.Background()); !IsCode(err, ErrorMalformed) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestHTTPStatusIsClassifiedBeforeOversizedErrorBody(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(writer, strings.Repeat("secret response", 100))
	})
	client, _ := newFixtureClient(t, handler, Config{MaxResponseBytes: 8})
	_, err := client.Status(context.Background())
	if !IsCode(err, ErrorUnauthorized) {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(fmt.Sprint(err), "secret") || strings.Contains(fmt.Sprint(err), "synthetic-sonarr-key") {
		t.Fatalf("error exposed upstream data: %v", err)
	}
}

func TestResponseBoundAndContextDeadline(t *testing.T) {
	tooLargeHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeFixtureJSON(t, writer, `{"version":"3.0.10"}`)
	})
	client, _ := newFixtureClient(t, tooLargeHandler, Config{MaxResponseBytes: 8})
	if _, err := client.Status(context.Background()); !IsCode(err, ErrorResponseTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	client, err := New(Config{Endpoint: "http://sonarr.invalid", APIKey: "synthetic-key", HTTPClient: &http.Client{Transport: transport}, RequestTimeout: time.Hour})
	if err != nil {
		t.Fatalf("New(timeout) error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = client.Status(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v", err)
	}
}

func TestRedirectAndConfigurationValidation(t *testing.T) {
	redirectHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "https://other.invalid/api/v3/system/status", http.StatusTemporaryRedirect)
	})
	client, _ := newFixtureClient(t, redirectHandler, Config{})
	if _, err := client.Status(context.Background()); !IsCode(err, ErrorUnsupported) {
		t.Fatalf("redirect error = %v", err)
	}

	invalid := []Config{
		{Endpoint: "", APIKey: "key"},
		{Endpoint: "http://sonarr.invalid", APIKey: ""},
		{Endpoint: "http://sonarr.invalid", APIKey: " key"},
		{Endpoint: "http://sonarr.invalid/%2e%2e", APIKey: "key"},
		{Endpoint: "http://sonarr.invalid/a//b", APIKey: "key"},
		{Endpoint: "http://sonarr.invalid/a\\b", APIKey: "key"},
		{Endpoint: "http://sonarr.invalid", APIKey: "key", RequestTimeout: -time.Second},
	}
	for index, config := range invalid {
		if _, err := New(config); err == nil {
			t.Fatalf("invalid config %d was accepted", index)
		}
	}
}

func TestInvalidRequestsDoNotReachNetwork(t *testing.T) {
	var calls int
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		writer.WriteHeader(http.StatusTeapot)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	if _, err := client.ListEpisodes(context.Background(), 0, true); !IsCode(err, ErrorInvalidInput) {
		t.Fatalf("invalid episode id error = %v", err)
	}
	if _, err := client.PreviewManualImport(context.Background(), ManualImportQuery{}); !IsCode(err, ErrorInvalidInput) {
		t.Fatalf("invalid preview error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("invalid requests reached network: %d", calls)
	}
}

func TestGeneratedTypesRemainInternalAndNormalizedOutputHasNoRawProperties(t *testing.T) {
	// This fixture has an arbitrary upstream field. The generated type accepts
	// it for compatibility, while the normalized public record has no raw map.
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeFixtureJSON(t, writer, `[{"id":1,"title":"Synthetic","path":"/library","monitored":false,"privateToken":"must-not-leak"}]`)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	page, err := client.ListSeries(context.Background())
	if err != nil {
		t.Fatalf("ListSeries() error = %v", err)
	}
	encoded, err := json.Marshal(page.Items[0])
	if err != nil {
		t.Fatalf("marshal normalized series: %v", err)
	}
	if strings.Contains(string(encoded), "privateToken") || strings.Contains(string(encoded), "AdditionalProperties") {
		t.Fatalf("raw upstream property leaked: %s", encoded)
	}
}
