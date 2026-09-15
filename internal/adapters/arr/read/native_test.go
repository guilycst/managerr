package read

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

func TestArrNativeModulesTranslateTypedReadErrors(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		kind   domain.ConnectionKind
		path   string
		code   domain.UpstreamErrorCode
		status int
	}{
		{name: "radarr rate limit", kind: domain.ConnectionRadarr, path: apiMovies, code: domain.OutcomeRateLimited, status: http.StatusTooManyRequests},
		{name: "sonarr unauthorized", kind: domain.ConnectionSonarr, path: apiSeries, code: domain.OutcomeUnauthorized, status: http.StatusUnauthorized},
		{name: "radarr unavailable", kind: domain.ConnectionRadarr, path: apiMovies, code: domain.OutcomeUnavailable, status: http.StatusBadGateway},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Header.Get("X-Api-Key") != "fixture-api-key" {
					response.WriteHeader(http.StatusUnauthorized)
					return
				}
				if request.URL.Path != testCase.path {
					response.WriteHeader(http.StatusNotFound)
					return
				}
				response.WriteHeader(testCase.status)
			}))
			defer server.Close()
			connectionID := domain.ConfigID("native-error-" + strings.ReplaceAll(testCase.name, " ", "-"))
			client, err := New(Config{ConnectionID: connectionID, Kind: testCase.kind, Endpoint: server.URL, APIKey: "fixture-api-key", MaxPageSize: 2, MaxPages: 2, MaxRecords: 10, MaxFiles: 10})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.List(context.Background(), connectionID, "", 1)
			if err == nil {
				t.Fatal("native read unexpectedly succeeded")
			}
			var upstream domain.UpstreamError
			if !errors.As(err, &upstream) || upstream.Code != testCase.code || upstream.Status != testCase.status {
				t.Fatalf("translated error = %#v (%v), want code=%s status=%d", upstream, err, testCase.code, testCase.status)
			}
			if strings.Contains(err.Error(), server.URL) || strings.Contains(err.Error(), "fixture-api-key") {
				t.Fatalf("translated error leaked endpoint or API key: %v", err)
			}
		})
	}
}

func TestArrNativeSonarrPreviewUsesDownloadedFolderMode(t *testing.T) {
	var seenQuery url.Values
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Api-Key") != "fixture-api-key" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.Method != http.MethodGet || request.URL.Path != apiManualImport {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		seenQuery = request.URL.Query()
		writeFixture(response, "sonarr-manual-import.json")
	})
	client, server := newSyntheticClient(t, domain.ConnectionSonarr, "sonarr-native-preview", handler, 2, 10, 50, 50)
	defer server.Close()
	preview, err := client.PreviewImport(context.Background(), "sonarr-native-preview", ports.ImportPreviewRequest{
		RegisteredExternalID: "201",
		Transfer:             "copy",
		Files: []ports.ImportFile{{
			Source:           domain.FileTarget{RootID: "library", RelativePath: "series/Synthetic Series - S01E01.mkv"},
			MovieOrEpisodeID: "301",
		}},
	})
	if err != nil || len(preview.Files) != 1 || len(preview.Rejections) != 0 {
		t.Fatalf("native Sonarr preview = %#v, err=%v", preview, err)
	}
	if seenQuery.Get("folder") != "/downloads/series" || seenQuery.Get("filterExistingFiles") != "true" || seenQuery.Get("seriesId") != "" {
		t.Fatalf("Sonarr folder preview query = %#v", seenQuery)
	}
}

func TestArrNativeCatalogMappingKeepsConnectionScopedIdentity(t *testing.T) {
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Api-Key") != "fixture-api-key" || request.URL.Path != apiMovies {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		writeFixture(response, "radarr-movies-full.json")
	})
	first, firstServer := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-native-one", handler, 2, 10, 50, 50)
	defer firstServer.Close()
	second, secondServer := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-native-two", handler, 2, 10, 50, 50)
	defer secondServer.Close()
	firstPage, err := first.List(context.Background(), "radarr-native-one", "", 2)
	if err != nil || len(firstPage.Items) == 0 {
		t.Fatalf("first native catalog = %#v, err=%v", firstPage, err)
	}
	secondPage, err := second.List(context.Background(), "radarr-native-two", "", 2)
	if err != nil || len(secondPage.Items) == 0 {
		t.Fatalf("second native catalog = %#v, err=%v", secondPage, err)
	}
	if firstPage.Items[0].ExternalID != secondPage.Items[0].ExternalID || first.ScopedIdentity(firstPage.Items[0].ExternalID) == second.ScopedIdentity(secondPage.Items[0].ExternalID) {
		t.Fatalf("native IDs lost connection scope: first=%q second=%q", first.ScopedIdentity(firstPage.Items[0].ExternalID), second.ScopedIdentity(secondPage.Items[0].ExternalID))
	}
}
