package radarr

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
		config.APIKey = "synthetic-radarr-key"
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
			writeFixtureJSON(t, writer, `{"version":"5.6.0.8846","branch":"master","appName":"Radarr","instanceName":"synthetic"}`)
		default:
			http.NotFound(writer, request)
		}
	})
	client, _ := newFixtureClient(t, handler, Config{})
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Version != "5.6.0.8846" || status.Branch != "master" || status.AppName != "Radarr" || status.InstanceName != "synthetic" {
		t.Fatalf("status = %#v", status)
	}
	version, err := client.Version(context.Background())
	if err != nil || version != status.Version {
		t.Fatalf("Version() = %q, %v", version, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 2 || paths[0] != "/proxy/api/v3/system/status" || paths[1] != paths[0] {
		t.Fatalf("paths = %#v", paths)
	}
	for _, key := range keys {
		if key != "synthetic-radarr-key" {
			t.Fatalf("API key header = %q", key)
		}
	}
	if strings.Contains(fmt.Sprint(client), "synthetic-radarr-key") {
		t.Fatalf("client formatting exposed API key")
	}
}

func TestListMoviesReportsCompleteCoverageAndCopiesPointers(t *testing.T) {
	body := `[{"id":101,"title":"Synthetic Film (2024)","year":2024,"path":"/library/movies/Synthetic Film (2024)","monitored":true,"hasFile":true,"movieFileId":501,"tmdbId":4242,"imdbId":"tt0000042","providerIds":{"tmdb":"4242"},"movieFile":{"id":501,"movieId":101,"path":"/library/movies/Synthetic Film (2024).mkv","relativePath":"Synthetic Film (2024).mkv","size":1234,"newField":"ignored"}}]`
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/proxy/api/v3/movie" || request.Method != http.MethodGet {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		writeFixtureJSON(t, writer, body)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	page, err := client.ListMovies(context.Background())
	if err != nil {
		t.Fatalf("ListMovies() error = %v", err)
	}
	if page.NextCursor != "" || page.Coverage.Completeness != CompletenessComplete || page.Coverage.ObservedCount != 1 || page.Coverage.ObservedAt.IsZero() {
		t.Fatalf("coverage = %#v", page.Coverage)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %#v", page.Items)
	}
	movie := page.Items[0]
	if movie.ID != 101 || movie.Title != "Synthetic Film (2024)" || movie.Year == nil || *movie.Year != 2024 || movie.TMDBID == nil || *movie.TMDBID != 4242 || movie.MovieFile == nil || movie.MovieFile.ID != 501 || movie.MovieFile.MovieID != 101 {
		t.Fatalf("movie = %#v", movie)
	}
	movie.ProviderIDs["tmdb"] = "changed-locally"
	encoded, err := json.Marshal(movie)
	if err != nil {
		t.Fatalf("marshal normalized movie: %v", err)
	}
	if strings.Contains(string(encoded), "newField") || strings.Contains(string(encoded), "AdditionalProperties") {
		t.Fatalf("raw upstream property leaked: %s", encoded)
	}
}

func TestListMoviesRejectsDuplicateAndWrongNestedIdentity(t *testing.T) {
	tests := map[string]string{
		"duplicate movie":         `[{"id":101,"title":"One","path":"/library/one","monitored":true},{"id":101,"title":"Two","path":"/library/two","monitored":false}]`,
		"wrong movie file":        `[{"id":101,"title":"One","path":"/library/one","monitored":true,"movieFile":{"id":501,"movieId":999,"path":"/library/one.mkv","size":10}}]`,
		"missing movie file size": `[{"id":101,"title":"One","path":"/library/one","monitored":true,"movieFile":{"id":501,"movieId":101,"path":"/library/one.mkv"}}]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeFixtureJSON(t, writer, body)
			})
			client, _ := newFixtureClient(t, handler, Config{})
			if _, err := client.ListMovies(context.Background()); !IsCode(err, ErrorMalformed) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestGetMovieRejectsWrongIdentityAndMapsNotFound(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/proxy/api/v3/movie/101":
			writeFixtureJSON(t, writer, `{"id":102,"title":"Wrong","path":"/library/wrong","monitored":false}`)
		case "/proxy/api/v3/movie/404":
			writer.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(writer, "private upstream detail")
		default:
			http.NotFound(writer, request)
		}
	})
	client, _ := newFixtureClient(t, handler, Config{})
	if _, err := client.GetMovie(context.Background(), 101); !IsCode(err, ErrorMalformed) {
		t.Fatalf("wrong identity error = %v", err)
	}
	if _, err := client.GetMovie(context.Background(), 404); !IsCode(err, ErrorNotFound) {
		t.Fatalf("not found error = %v", err)
	}
	if strings.Contains(fmt.Sprint(client.GetMovie(context.Background(), 404)), "private upstream detail") {
		t.Fatalf("upstream body leaked")
	}
}

func TestOptionsAndMovieFileObservations(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/proxy/api/v3/rootfolder":
			writeFixtureJSON(t, writer, `[{"id":1,"path":"/library/movies","name":"Movies","accessible":true,"freeSpace":1234}]`)
		case "/proxy/api/v3/qualityprofile":
			writeFixtureJSON(t, writer, `[{"id":7,"name":"Synthetic HD"}]`)
		case "/proxy/api/v3/moviefile":
			if request.URL.Query().Get("movieId") != "101" {
				t.Errorf("movie-file query = %s", request.URL.RawQuery)
			}
			writeFixtureJSON(t, writer, `[{"id":501,"movieId":101,"path":"/library/movies/Synthetic Film.mkv","relativePath":"Synthetic Film.mkv","size":1234,"dateAdded":"2024-01-02T03:04:05Z","sceneName":"Synthetic.Film.2024","releaseGroup":"synthetic","languages":[{"id":1,"name":"English","isoCode":"en"}]}]`)
		default:
			http.NotFound(writer, request)
		}
	})
	client, _ := newFixtureClient(t, handler, Config{})
	roots, err := client.ListRootFolders(context.Background())
	if err != nil || len(roots.Items) != 1 || roots.Items[0].Path != "/library/movies" || roots.Items[0].Accessible == nil || !*roots.Items[0].Accessible {
		t.Fatalf("roots = %#v, err=%v", roots, err)
	}
	profiles, err := client.ListQualityProfiles(context.Background())
	if err != nil || len(profiles.Items) != 1 || profiles.Items[0].Name != "Synthetic HD" {
		t.Fatalf("profiles = %#v, err=%v", profiles, err)
	}
	files, err := client.ListMovieFiles(context.Background(), 101)
	if err != nil || len(files.Items) != 1 {
		t.Fatalf("files = %#v, err=%v", files, err)
	}
	file := files.Items[0]
	if file.ID != 501 || file.MovieID != 101 || file.Size != 1234 || file.SceneName != "Synthetic.Film.2024" || len(file.Languages) != 1 || file.Languages[0].ISOCode != "en" {
		t.Fatalf("file = %#v", file)
	}
}

func TestListMovieFilesRejectsForeignMovieAndDuplicateLanguage(t *testing.T) {
	tests := map[string]string{
		"foreign movie":      `[{"id":501,"movieId":999,"path":"/library/other.mkv","size":10}]`,
		"duplicate language": `[{"id":501,"movieId":101,"path":"/library/movie.mkv","size":10,"languages":[{"id":1,"name":"English"},{"id":1,"name":"English"}]}]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeFixtureJSON(t, writer, body)
			})
			client, _ := newFixtureClient(t, handler, Config{})
			if _, err := client.ListMovieFiles(context.Background(), 101); !IsCode(err, ErrorMalformed) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestManualImportFolderPreviewIsReadOnlyAndPreservesTypedEvidence(t *testing.T) {
	var method string
	var gotQuery map[string]string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		method = request.Method
		gotQuery = map[string]string{
			"folder":              request.URL.Query().Get("folder"),
			"filterExistingFiles": request.URL.Query().Get("filterExistingFiles"),
			"movieId":             request.URL.Query().Get("movieId"),
			"downloadId":          request.URL.Query().Get("downloadId"),
		}
		writeFixtureJSON(t, writer, `[{"id":701,"path":"/downloads/incoming/Synthetic Film.mkv","relativePath":"Synthetic Film.mkv","folderName":"/downloads/incoming","name":"Synthetic Film.mkv","size":123456789,"movie":{"id":101,"title":"Synthetic Film (2024)","tmdbId":4242},"movieFileId":0,"releaseGroup":"synthetic","quality":{"quality":{"id":7,"name":"Bluray-1080p","source":"bluray","resolution":1080},"revision":{"version":1,"real":0,"isRepack":false}},"languages":[{"id":1,"name":"English","isoCode":"en"}],"downloadId":"synthetic-download-1","customFormatScore":10,"indexerFlags":1,"rejections":[{"type":"ExistingFile","reason":"A file already exists"}]}]`)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	page, err := client.PreviewManualImport(context.Background(), ManualImportQuery{Folder: "/downloads/incoming", FilterExistingFiles: true, DownloadID: "synthetic-download-1"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("preview = %#v, err=%v", page, err)
	}
	candidate := page.Items[0]
	if method != http.MethodGet || gotQuery["folder"] != "/downloads/incoming" || gotQuery["filterExistingFiles"] != "true" || gotQuery["movieId"] != "" || gotQuery["downloadId"] != "synthetic-download-1" {
		t.Fatalf("request = %s %#v", method, gotQuery)
	}
	if candidate.Movie == nil || candidate.Movie.ID != 101 || candidate.Quality == nil || candidate.Quality.Quality == nil || candidate.Quality.Quality.Resolution == nil || *candidate.Quality.Quality.Resolution != 1080 || len(candidate.Languages) != 1 || candidate.Languages[0].Name != "English" || len(candidate.Rejections) != 1 || candidate.Rejections[0].Reason != "A file already exists" || candidate.Rejections[0].Message != candidate.Rejections[0].Reason {
		t.Fatalf("candidate = %#v", candidate)
	}
}

func TestManualImportPreviewRejectsInvalidMovieScopeBeforeNetwork(t *testing.T) {
	var calls int
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		http.Error(writer, "unexpected request", http.StatusTeapot)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	movieID := int64(0)
	_, err := client.PreviewManualImport(context.Background(), ManualImportQuery{Folder: "/downloads", MovieID: &movieID})
	if !IsCode(err, ErrorInvalidInput) || calls != 0 {
		t.Fatalf("invalid movie preview error = %v, calls=%d", err, calls)
	}
	if _, err := client.PreviewManualImport(context.Background(), ManualImportQuery{}); !IsCode(err, ErrorInvalidInput) || calls != 0 {
		t.Fatalf("empty preview error = %v, calls=%d", err, calls)
	}
}

func TestManualImportLibraryPreviewUsesNativeMovieScope(t *testing.T) {
	var gotQuery urlValues
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotQuery = urlValues{folder: request.URL.Query().Get("folder"), movieID: request.URL.Query().Get("movieId"), downloadID: request.URL.Query().Get("downloadId"), filter: request.URL.Query().Get("filterExistingFiles")}
		writeFixtureJSON(t, writer, `[{"id":702,"path":"/downloads/incoming/Synthetic Film.mkv","relativePath":"Synthetic Film.mkv","name":"Synthetic Film.mkv","size":100,"movie":{"id":101,"title":"Synthetic Film (2024)"},"rejections":[]}]`)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	page, err := client.PreviewLibraryImport(context.Background(), LibraryImportQuery{Folder: "/downloads/incoming", MovieID: 101, FilterExistingFiles: true})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("library preview = %#v, err=%v", page, err)
	}
	if gotQuery != (urlValues{folder: "/downloads/incoming", movieID: "101", filter: "true"}) {
		t.Fatalf("native query = %#v", gotQuery)
	}
}

func TestManualImportLibraryPreviewRejectsMissingPathBeforeNetwork(t *testing.T) {
	var calls int
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		http.Error(writer, "missing source path should not reach the server", http.StatusBadRequest)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	_, err := client.PreviewLibraryImport(context.Background(), LibraryImportQuery{MovieID: 101})
	if !IsCode(err, ErrorInvalidInput) {
		t.Fatalf("missing path error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("missing path reached network: %d", calls)
	}
}

type urlValues struct {
	folder     string
	movieID    string
	downloadID string
	filter     string
}

func TestManualImportLibraryPreviewRejectsForeignMovieAndMissingReference(t *testing.T) {
	tests := map[string]string{
		"foreign": `[{"id":702,"path":"/library/other.mkv","relativePath":"other.mkv","name":"other.mkv","size":100,"movie":{"id":999},"rejections":[]}]`,
		"missing": `[{"id":702,"path":"/library/missing.mkv","relativePath":"missing.mkv","name":"missing.mkv","size":100,"rejections":[]}]`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeFixtureJSON(t, writer, body)
			})
			client, _ := newFixtureClient(t, handler, Config{})
			if _, err := client.PreviewLibraryImport(context.Background(), LibraryImportQuery{Folder: "/downloads", MovieID: 101}); !IsCode(err, ErrorMalformed) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestManualImportRejectsMalformedNestedEvidence(t *testing.T) {
	tests := map[string]string{
		"language-name":    `[{"id":1,"path":"/downloads/movie.mkv","relativePath":"movie.mkv","name":"movie.mkv","size":1,"languages":[{"id":1}],"rejections":[]}]`,
		"rejection-reason": `[{"id":1,"path":"/downloads/movie.mkv","relativePath":"movie.mkv","name":"movie.mkv","size":1,"rejections":[{"type":"ExistingFile"}]}]`,
		"duplicate":        `[{"id":1,"id":2,"path":"/downloads/movie.mkv","relativePath":"movie.mkv","name":"movie.mkv","size":1}]`,
		"trailing":         `[{"id":1,"path":"/downloads/movie.mkv","relativePath":"movie.mkv","name":"movie.mkv","size":1}] {"extra":true}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeFixtureJSON(t, writer, body)
			})
			client, _ := newFixtureClient(t, handler, Config{})
			if _, err := client.PreviewManualImport(context.Background(), ManualImportQuery{Folder: "/downloads"}); !IsCode(err, ErrorMalformed) {
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
	if strings.Contains(fmt.Sprint(err), "secret") || strings.Contains(fmt.Sprint(err), "synthetic-radarr-key") {
		t.Fatalf("error exposed upstream data: %v", err)
	}
}

func TestResponseBoundAndContextDeadline(t *testing.T) {
	tooLargeHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeFixtureJSON(t, writer, `{"version":"5.6.0"}`)
	})
	client, _ := newFixtureClient(t, tooLargeHandler, Config{MaxResponseBytes: 8})
	if _, err := client.Status(context.Background()); !IsCode(err, ErrorResponseTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	client, err := New(Config{Endpoint: "http://radarr.invalid", APIKey: "synthetic-key", HTTPClient: &http.Client{Transport: transport}, RequestTimeout: time.Hour})
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
		{Endpoint: "http://radarr.invalid", APIKey: ""},
		{Endpoint: "http://radarr.invalid", APIKey: " key"},
		{Endpoint: "http://radarr.invalid/%2e%2e", APIKey: "key"},
		{Endpoint: "http://radarr.invalid/a//b", APIKey: "key"},
		{Endpoint: "http://radarr.invalid/a\\b", APIKey: "key"},
		{Endpoint: "http://radarr.invalid", APIKey: "key", RequestTimeout: -time.Second},
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
	if _, err := client.ListMovieFiles(context.Background(), 0); !IsCode(err, ErrorInvalidInput) {
		t.Fatalf("invalid movie id error = %v", err)
	}
	if _, err := client.PreviewLibraryImport(context.Background(), LibraryImportQuery{}); !IsCode(err, ErrorInvalidInput) {
		t.Fatalf("invalid library preview error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("invalid requests reached network: %d", calls)
	}
}
