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
	"strconv"
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
	episodeQuery []url.Values
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
			writeFixture(response, "sonarr-manual-import-reprocess.json")
		} else {
			writeFixture(response, "radarr-manual-import.json")
		}
		return
	}

	switch request.URL.Path {
	case apiMovies:
		writeFixture(response, "radarr-movies-full.json")
	case apiMovieFiles:
		if request.URL.Query().Get("movieId") == "102" {
			writeFixture(response, "radarr-moviefile-102.json")
		} else {
			writeJSON(response, []any{})
		}
	case apiMovieLookup:
		writeFixture(response, "radarr-lookup.json")
	case apiSeries:
		writeFixture(response, "sonarr-series-full.json")
	case apiEpisodes:
		if handler.kind == domain.ConnectionSonarr {
			query := request.URL.Query()
			handler.mu.Lock()
			handler.episodeQuery = append(handler.episodeQuery, query)
			handler.mu.Unlock()
			if query.Get("includeEpisodeFile") != "true" {
				response.WriteHeader(http.StatusBadRequest)
				return
			}
		}
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

func newSyntheticClient(t *testing.T, kind domain.ConnectionKind, connectionID domain.ConfigID, handler http.Handler, maxPageSize, maxPages, maxRecords, maxFiles int) (*Client, *httptest.Server) {
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
		MaxPageSize: maxPageSize,
		MaxPages:    maxPages,
		MaxRecords:  maxRecords,
		MaxFiles:    maxFiles,
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

func TestArrCatalogPagesFullArrayTailForBothProducts(t *testing.T) {
	for _, product := range []struct {
		name string
		kind domain.ConnectionKind
	}{
		{name: "radarr", kind: domain.ConnectionRadarr},
		{name: "sonarr", kind: domain.ConnectionSonarr},
	} {
		t.Run(product.name, func(t *testing.T) {
			connectionID := domain.ConfigID(product.name + "-full-array")
			catalog := make([]map[string]any, 0, 3)
			for index, externalID := range []int{101, 102, 103} {
				if product.kind == domain.ConnectionRadarr {
					catalog = append(catalog, map[string]any{
						"id": externalID, "title": "Synthetic Film " + strconv.Itoa(externalID), "tmdbId": externalID + 4000,
						"movieFile": map[string]any{"id": externalID + 400, "path": "/downloads/catalog/" + strconv.Itoa(externalID) + ".mkv", "size": 100 + index},
					})
					continue
				}
				catalog = append(catalog, map[string]any{"id": externalID, "title": "Synthetic Series " + strconv.Itoa(externalID), "tvdbId": externalID + 6000})
			}
			catalogBody, err := json.Marshal(catalog)
			if err != nil {
				t.Fatal(err)
			}
			var pageQuerySeen bool
			handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Header.Get("X-Api-Key") != "fixture-api-key" {
					response.WriteHeader(http.StatusUnauthorized)
					return
				}
				if request.URL.Query().Get("page") != "" || request.URL.Query().Get("pageSize") != "" {
					pageQuerySeen = true
				}
				switch request.URL.Path {
				case catalogPath(product.kind):
					response.Header().Set("Content-Type", "application/json")
					_, _ = response.Write(catalogBody)
				case apiEpisodes:
					writeJSON(response, []any{})
				default:
					response.WriteHeader(http.StatusNotFound)
				}
			})
			client, server := newSyntheticClient(t, product.kind, connectionID, handler, 2, 10, 20, 50)
			defer server.Close()
			var got []string
			var cursor string
			var final ports.Page[ports.MediaRecord]
			for {
				page, listErr := client.List(context.Background(), connectionID, cursor, 2)
				if listErr != nil {
					t.Fatal(listErr)
				}
				for _, item := range page.Items {
					got = append(got, item.ExternalID)
				}
				final = page
				if page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
			if pageQuerySeen {
				t.Fatal("full-array catalog request included page or pageSize")
			}
			if len(got) != 3 || got[0] != "101" || got[1] != "102" || got[2] != "103" {
				t.Fatalf("full-array IDs = %#v", got)
			}
			if final.Coverage.Completeness != domain.CompletenessComplete || final.Coverage.ObservedCount != 3 {
				t.Fatalf("full-array final coverage = %#v", final.Coverage)
			}

			var allCatalog []map[string]any
			if err := json.Unmarshal(catalogBody, &allCatalog); err != nil {
				t.Fatal(err)
			}
			for _, size := range []int{0, 2} {
				body, marshalErr := json.Marshal(allCatalog[:size])
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				catalogBody = body
				sizedConnectionID := domain.ConfigID(connectionID.String() + "-size-" + strconv.Itoa(size))
				sizedClient, sizedServer := newSyntheticClient(t, product.kind, sizedConnectionID, handler, 2, 10, 20, 50)
				page, sizeErr := sizedClient.List(context.Background(), sizedConnectionID, "", 2)
				sizedServer.Close()
				if sizeErr != nil || len(page.Items) != size || page.NextCursor != "" || page.Coverage.Completeness != domain.CompletenessComplete || page.Coverage.ObservedCount != int64(size) {
					t.Fatalf("full-array size %d page = %#v, err=%v", size, page, sizeErr)
				}
			}
			catalogBody, err = json.Marshal(allCatalog)
			if err != nil {
				t.Fatal(err)
			}

			cappedClient, cappedServer := newSyntheticClient(t, product.kind, connectionID+"-cap", handler, 2, 10, 2, 50)
			defer cappedServer.Close()
			capped, capErr := cappedClient.List(context.Background(), connectionID+"-cap", "", 2)
			if capErr != nil {
				t.Fatal(capErr)
			}
			if len(capped.Items) != 2 || capped.NextCursor != "" || capped.Coverage.Completeness != domain.CompletenessPartial || !hasReason(capped.Coverage, "catalog_record_limit") {
				t.Fatalf("capped full-array coverage = %#v", capped)
			}
		})
	}
}

func TestArrHistoryCapsRemainPartial(t *testing.T) {
	historyHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Api-Key") != "fixture-api-key" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.URL.Path != apiHistory {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		page, _ := strconv.Atoi(request.URL.Query().Get("page"))
		if page <= 0 {
			page = 1
		}
		if page > 10 {
			writeJSON(response, map[string]any{"page": page, "pageSize": 1, "totalRecords": 10, "records": []any{}})
			return
		}
		writeJSON(response, map[string]any{
			"page": page, "pageSize": 1, "totalRecords": 10,
			"records": []map[string]any{{"id": page, "eventType": "imported", "date": "2026-09-11T12:00:00Z", "successful": true}},
		})
	})
	pageCapped, pageServer := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-history-page-cap", historyHandler, 1, 2, 100, 50)
	defer pageServer.Close()
	first, err := pageCapped.History(context.Background(), "radarr-history-page-cap", "", 1)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("history first page = %#v, %v", first, err)
	}
	second, err := pageCapped.History(context.Background(), "radarr-history-page-cap", first.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second.NextCursor != "" || second.Coverage.Completeness != domain.CompletenessPartial || !hasReason(second.Coverage, "history_page_limit") || second.Coverage.ObservedCount != 2 {
		t.Fatalf("page-capped history = %#v", second)
	}

	recordCapped, recordServer := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-history-record-cap", historyHandler, 1, 10, 2, 50)
	defer recordServer.Close()
	first, err = recordCapped.History(context.Background(), "radarr-history-record-cap", "", 1)
	if err != nil || first.NextCursor == "" || !hasReason(first.Coverage, "history_record_limit") {
		t.Fatalf("record-capped first page = %#v, %v", first, err)
	}
	second, err = recordCapped.History(context.Background(), "radarr-history-record-cap", first.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if second.NextCursor != "" || second.Coverage.Completeness != domain.CompletenessPartial || !hasReason(second.Coverage, "history_record_limit") || second.Coverage.ObservedCount != 2 {
		t.Fatalf("record-capped history = %#v", second)
	}

	boundaryHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		page, _ := strconv.Atoi(request.URL.Query().Get("page"))
		if page <= 0 {
			page = 1
		}
		if page > 2 {
			writeJSON(response, map[string]any{"page": page, "pageSize": 1, "totalRecords": 2, "snapshotId": "history-snapshot-1", "records": []any{}})
			return
		}
		writeJSON(response, map[string]any{"page": page, "pageSize": 1, "totalRecords": 2, "snapshotId": "history-snapshot-1", "records": []map[string]any{{"id": page, "date": "2026-09-11T12:00:00Z"}}})
	})
	boundary, boundaryServer := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-history-boundary", boundaryHandler, 1, 10, 2, 50)
	defer boundaryServer.Close()
	first, err = boundary.History(context.Background(), "radarr-history-boundary", "", 1)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("boundary first page = %#v, %v", first, err)
	}
	second, err = boundary.History(context.Background(), "radarr-history-boundary", first.NextCursor, 1)
	if err != nil || second.NextCursor != "" || second.Coverage.Completeness != domain.CompletenessPartial || !hasReason(second.Coverage, "history_snapshot_unsupported") {
		t.Fatalf("boundary final page = %#v, %v", second, err)
	}

	lyingTotalHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		page, _ := strconv.Atoi(request.URL.Query().Get("page"))
		if page <= 0 {
			page = 1
		}
		if page == 1 {
			writeJSON(response, map[string]any{"page": 1, "pageSize": 1, "totalRecords": 1, "hasMore": true, "records": []map[string]any{{"id": 1, "date": "2026-09-11T12:00:00Z"}}})
			return
		}
		writeJSON(response, map[string]any{"page": page, "pageSize": 1, "totalRecords": 1, "hasMore": false, "records": []map[string]any{{"id": 2, "date": "2026-09-11T12:00:01Z"}}})
	})
	lyingTotal, lyingServer := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-history-lying-total", lyingTotalHandler, 1, 10, 10, 50)
	defer lyingServer.Close()
	first, err = lyingTotal.History(context.Background(), "radarr-history-lying-total", "", 1)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("lying-total first page = %#v, %v", first, err)
	}
	second, err = lyingTotal.History(context.Background(), "radarr-history-lying-total", first.NextCursor, 1)
	if err != nil || second.NextCursor != "" || second.Coverage.Completeness != domain.CompletenessPartial || !hasReason(second.Coverage, "history_total_inconsistent") {
		t.Fatalf("lying-total final page = %#v, %v", second, err)
	}
}

func TestArrHistorySnapshotShapedFieldsCannotCertifyOffsetDeletionDrift(t *testing.T) {
	for index, field := range []string{"snapshotId", "snapshotToken", "snapshotRevision"} {
		t.Run(field, func(t *testing.T) {
			connectionID := domain.ConfigID("radarr-history-unsupported-snapshot-" + strconv.Itoa(index))
			historyHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Header.Get("X-Api-Key") != "fixture-api-key" {
					response.WriteHeader(http.StatusUnauthorized)
					return
				}
				if request.URL.Path != apiHistory {
					response.WriteHeader(http.StatusNotFound)
					return
				}
				page, _ := strconv.Atoi(request.URL.Query().Get("page"))
				if page <= 1 {
					writeJSON(response, map[string]any{
						"page": 1, "pageSize": 2, "hasMore": true,
						field: "synthetic-boundary-that-is-not-pinned",
						"records": []map[string]any{
							{"id": 1, "eventType": "imported", "date": "2026-09-11T12:00:00Z"},
							{"id": 2, "eventType": "imported", "date": "2026-09-11T12:00:01Z"},
						},
					})
					return
				}
				// A deletion between offset requests removes id 3. The response has
				// no overlap with page one, so an unbound snapshot field must not turn
				// this into complete coverage.
				writeJSON(response, map[string]any{
					"page": 2, "pageSize": 2, "hasMore": false,
					field:     "synthetic-boundary-that-is-not-pinned",
					"records": []map[string]any{{"id": 4, "eventType": "imported", "date": "2026-09-11T12:00:03Z"}},
				})
			})
			client, server := newSyntheticClient(t, domain.ConnectionRadarr, connectionID, historyHandler, 2, 10, 50, 50)
			defer server.Close()
			first, err := client.History(context.Background(), connectionID, "", 2)
			if err != nil || first.NextCursor == "" {
				t.Fatalf("first history page = %#v, %v", first, err)
			}
			second, err := client.History(context.Background(), connectionID, first.NextCursor, 2)
			if err != nil {
				t.Fatal(err)
			}
			if second.NextCursor != "" || second.Coverage.Completeness != domain.CompletenessPartial || !hasReason(second.Coverage, "history_snapshot_unsupported") || second.Coverage.ObservedCount != 3 {
				t.Fatalf("snapshot-shaped %s certified deletion drift: %#v", field, second.Coverage)
			}
			if len(second.Items) != 1 || second.Items[0].ID != "4" {
				t.Fatalf("snapshot-shaped %s lost tail evidence: %#v", field, second.Items)
			}
		})
	}
}

func TestArrHistoryOverlapRemainsPartialAndCountsUniqueRecords(t *testing.T) {
	historyHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != apiHistory {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		page, _ := strconv.Atoi(request.URL.Query().Get("page"))
		if page <= 1 {
			writeJSON(response, map[string]any{
				"page": 1, "pageSize": 1, "totalRecords": 2, "hasMore": true,
				"records": []map[string]any{{"id": 1, "eventType": "imported", "date": "2026-09-11T12:00:00Z"}},
			})
			return
		}
		writeJSON(response, map[string]any{
			"page": 2, "pageSize": 1, "totalRecords": 2, "hasMore": false,
			"records": []map[string]any{{"id": 1, "eventType": "imported", "date": "2026-09-11T12:00:01Z"}},
		})
	})
	client, server := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-history-overlap", historyHandler, 1, 10, 10, 50)
	defer server.Close()
	first, err := client.History(context.Background(), "radarr-history-overlap", "", 1)
	if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
		t.Fatalf("history overlap first page = %#v, %v", first, err)
	}
	second, err := client.History(context.Background(), "radarr-history-overlap", first.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 0 || second.NextCursor != "" || second.Coverage.ObservedCount != 1 || second.Coverage.Completeness != domain.CompletenessPartial || !hasReason(second.Coverage, "history_overlap") {
		t.Fatalf("history overlap terminal page = %#v", second)
	}
}

func TestArrHistoryDeletionDriftCannotBecomeComplete(t *testing.T) {
	historyHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != apiHistory {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		page, _ := strconv.Atoi(request.URL.Query().Get("page"))
		if page <= 1 {
			// The first page is observed before the oldest record is deleted.
			writeJSON(response, map[string]any{
				"page": 1, "pageSize": 2, "totalRecords": 4, "hasMore": true,
				"records": []map[string]any{{"id": 4, "date": "2026-09-11T12:00:04Z"}, {"id": 3, "date": "2026-09-11T12:00:03Z"}},
			})
			return
		}
		// Arr's offset page now starts after 3 in the shortened collection;
		// record 2 is skipped even though there is no overlapping identity.
		writeJSON(response, map[string]any{
			"page": 2, "pageSize": 2, "totalRecords": 3, "hasMore": false,
			"records": []map[string]any{{"id": 1, "date": "2026-09-11T12:00:01Z"}},
		})
	})
	client, server := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-history-deletion-drift", historyHandler, 2, 10, 50, 50)
	defer server.Close()
	first, err := client.History(context.Background(), "radarr-history-deletion-drift", "", 2)
	if err != nil || len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("history deletion-drift first page = %#v, %v", first, err)
	}
	second, err := client.History(context.Background(), "radarr-history-deletion-drift", first.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.NextCursor != "" || second.Coverage.ObservedCount != 3 || second.Coverage.Completeness != domain.CompletenessPartial {
		t.Fatalf("history deletion-drift terminal page = %#v", second)
	}
	for _, reason := range []string{"history_total_changed", "history_snapshot_unverified"} {
		if !hasReason(second.Coverage, reason) {
			t.Fatalf("history deletion-drift missing reason %q: %#v", reason, second.Coverage)
		}
	}
}

func TestArrCatalogDuplicateBeyondIdentityPrefixRemainsPartial(t *testing.T) {
	const uniqueCount = 2_050
	catalog := make([]map[string]any, 0, uniqueCount+1)
	for id := 1; id <= uniqueCount; id++ {
		catalog = append(catalog, map[string]any{
			"id": id, "title": "Synthetic Film " + strconv.Itoa(id), "tmdbId": id + 10_000,
			"movieFile": map[string]any{"id": id + 20_000, "path": "/downloads/catalog/" + strconv.Itoa(id) + ".mkv"},
		})
	}
	// This duplicate lands after the old 2,048-ID cursor prefix. The bounded
	// cursor filter must still reject it and leave the terminal coverage partial.
	catalog = append(catalog, catalog[uniqueCount-2])
	body, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != apiMovies {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(body)
	})
	client, server := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-catalog-late-duplicate", handler, 100, 30, 3_000, 50)
	defer server.Close()
	var cursor string
	var observed int
	var final ports.Page[ports.MediaRecord]
	seen := make(map[string]struct{}, uniqueCount)
	for {
		page, listErr := client.List(context.Background(), "radarr-catalog-late-duplicate", cursor, 100)
		if listErr != nil {
			t.Fatal(listErr)
		}
		observed += len(page.Items)
		for _, item := range page.Items {
			if _, exists := seen[item.ExternalID]; exists {
				t.Fatalf("duplicate catalog ID escaped mapping: %s", item.ExternalID)
			}
			seen[item.ExternalID] = struct{}{}
		}
		final = page
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if observed != uniqueCount || len(seen) != uniqueCount || final.Coverage.Completeness != domain.CompletenessPartial || !hasReason(final.Coverage, "catalog_overlap") {
		t.Fatalf("late duplicate catalog coverage observed=%d unique=%d coverage=%#v", observed, len(seen), final.Coverage)
	}
}

func TestArrObserveImportSurfacesIncompleteEvidence(t *testing.T) {
	missingFileHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case apiMovies + "/101":
			writeJSON(response, map[string]any{"id": 101, "title": "Synthetic Film"})
		case apiMovieFiles:
			response.WriteHeader(http.StatusServiceUnavailable)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	})
	radarr, server := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-observe-missing", missingFileHandler, 2, 10, 50, 2)
	defer server.Close()
	_, err := radarr.ObserveImport(context.Background(), "radarr-observe-missing", "101")
	assertUpstreamCode(t, err, domain.OutcomeUnavailable)

	mismatchedMovieHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == apiMovies+"/101" {
			writeJSON(response, map[string]any{"id": 999, "title": "Another Synthetic Film"})
			return
		}
		response.WriteHeader(http.StatusNotFound)
	})
	mismatched, mismatchedServer := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-observe-mismatch", mismatchedMovieHandler, 2, 10, 50, 2)
	defer mismatchedServer.Close()
	_, err = mismatched.ObserveImport(context.Background(), "radarr-observe-mismatch", "101")
	assertUpstreamCode(t, err, domain.OutcomeUnknown)

	malformedFileHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == apiMovies+"/101" {
			writeJSON(response, map[string]any{"id": 101, "title": "Synthetic Film"})
			return
		}
		if request.URL.Path == apiMovieFiles {
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`[{"id":501`))
			return
		}
		response.WriteHeader(http.StatusNotFound)
	})
	malformedClient, malformedServer := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-observe-malformed", malformedFileHandler, 2, 10, 50, 2)
	defer malformedServer.Close()
	_, err = malformedClient.ObserveImport(context.Background(), "radarr-observe-malformed", "101")
	assertUpstreamCode(t, err, domain.OutcomeUnknown)

	partialFilesHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == apiMovies+"/101" {
			writeJSON(response, map[string]any{"id": 101, "title": "Synthetic Film"})
			return
		}
		if request.URL.Path == apiMovieFiles {
			writeJSON(response, map[string]any{"totalRecords": 3, "records": []map[string]any{{"id": 501, "path": "/downloads/incoming/one.mkv"}}})
			return
		}
		response.WriteHeader(http.StatusNotFound)
	})
	partialFiles, partialFilesServer := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-observe-partial", partialFilesHandler, 2, 10, 50, 50)
	defer partialFilesServer.Close()
	_, err = partialFiles.ObserveImport(context.Background(), "radarr-observe-partial", "101")
	assertUpstreamCode(t, err, domain.OutcomeUnknown)

	sonarrCapHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != apiEpisodes {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(response, []map[string]any{
			{"id": 301, "seriesId": 201, "episodeFileId": 801, "episodeFile": map[string]any{"id": 801, "path": "/downloads/series/one.mkv"}},
			{"id": 302, "seriesId": 201, "episodeFileId": 802, "episodeFile": map[string]any{"id": 802, "path": "/downloads/series/two.mkv"}},
		})
	})
	sonarr, sonarrServer := newSyntheticClient(t, domain.ConnectionSonarr, "sonarr-observe-cap", sonarrCapHandler, 2, 10, 50, 1)
	defer sonarrServer.Close()
	_, err = sonarr.ObserveImport(context.Background(), "sonarr-observe-cap", "201")
	assertUpstreamCode(t, err, domain.OutcomeUnknown)

	completeHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != apiEpisodes {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		writeJSON(response, []map[string]any{{
			"id": 301, "seriesId": 201, "episodeFileId": 801,
			"episodeFile": map[string]any{"id": 801, "path": "/downloads/series/one.mkv", "size": 1},
		}})
	})
	complete, completeServer := newSyntheticClient(t, domain.ConnectionSonarr, "sonarr-observe-complete", completeHandler, 2, 10, 50, 2)
	defer completeServer.Close()
	observation, err := complete.ObserveImport(context.Background(), "sonarr-observe-complete", "201")
	if err != nil || observation.ExternalID != "201" || len(observation.Files) != 1 || observation.Files[0].EpisodeIDs[0] != "301" {
		t.Fatalf("complete Sonarr observation = %#v, %v", observation, err)
	}
}

func TestArrPreviewRequiresExactPathAndAssociation(t *testing.T) {
	client, server := newFixtureClient(t, domain.ConnectionRadarr, &arrFixtureHandler{kind: domain.ConnectionRadarr}, "radarr-preview-exact")
	defer server.Close()
	requested := ports.ImportFile{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/same.mkv"}, MovieOrEpisodeID: "101"}
	wrongDirectory, _ := json.Marshal([]RadarrManualImportResource{{
		Path: "/downloads/other/same.mkv", Movie: &ArrMovieReference{ID: json.RawMessage(`101`)}, Rejections: []ArrImportRejection{},
	}})
	accepted, rejected, err := client.mapPreviewResponseFor(wrongDirectory, []ports.ImportFile{requested}, "/downloads/incoming", "101", nil, nil)
	if err != nil || len(accepted) != 0 || len(rejected) != 1 || rejected[0].Code != "not_returned" {
		t.Fatalf("same-basename candidate = accepted=%#v rejected=%#v err=%v", accepted, rejected, err)
	}
	relative, _ := json.Marshal([]RadarrManualImportResource{{
		RelativePath: "same.mkv", Movie: &ArrMovieReference{ID: json.RawMessage(`101`)}, Rejections: []ArrImportRejection{},
	}})
	accepted, rejected, err = client.mapPreviewResponseFor(relative, []ports.ImportFile{requested}, "/downloads/incoming", "101", nil, nil)
	if err != nil || len(accepted) != 1 || len(rejected) != 0 {
		t.Fatalf("exact relative candidate = accepted=%#v rejected=%#v err=%v", accepted, rejected, err)
	}
	duplicates, _ := json.Marshal([]RadarrManualImportResource{
		{Path: "/downloads/incoming/same.mkv", Movie: &ArrMovieReference{ID: json.RawMessage(`101`)}},
		{RelativePath: "same.mkv", Movie: &ArrMovieReference{ID: json.RawMessage(`101`)}},
	})
	accepted, rejected, err = client.mapPreviewResponseFor(duplicates, []ports.ImportFile{requested}, "/downloads/incoming", "101", nil, nil)
	if err != nil || len(accepted) != 0 || len(rejected) != 1 || rejected[0].Code != "candidate_ambiguous" {
		t.Fatalf("duplicate exact candidates = accepted=%#v rejected=%#v err=%v", accepted, rejected, err)
	}

	windows := &Client{config: Config{ConnectionID: "radarr-windows", RootPaths: map[domain.ConfigID]string{"library": `C:\\downloads`}}}
	windowsTarget := domain.FileTarget{RootID: "library", RelativePath: "incoming/same.mkv"}
	if !targetMatches(`C:\\downloads\\incoming\\same.mkv`, windowsTarget, windows.config.RootPaths) {
		t.Fatal("Windows native path did not match its exact root-relative target")
	}
	if targetMatches(`C:\\downloads\\other\\same.mkv`, windowsTarget, windows.config.RootPaths) {
		t.Fatal("Windows same-basename path crossed directory boundary")
	}

	sonarr, sonarrServer := newFixtureClient(t, domain.ConnectionSonarr, &arrFixtureHandler{kind: domain.ConnectionSonarr}, "sonarr-preview-association")
	defer sonarrServer.Close()
	sonarrFile := ports.ImportFile{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/episode.mkv"}, MovieOrEpisodeID: "301"}
	wrongSeries, _ := json.Marshal([]SonarrManualImportResource{{
		Path: "/downloads/incoming/episode.mkv", Series: &ArrSeriesReference{ID: json.RawMessage(`999`)},
		Episodes: []ArrEpisodeReference{{ID: json.RawMessage(`301`)}},
	}})
	accepted, rejected, err = sonarr.mapPreviewResponseFor(wrongSeries, []ports.ImportFile{sonarrFile}, "/downloads/incoming", "201", nil, nil)
	if err != nil || len(accepted) != 0 || len(rejected) != 1 || rejected[0].Code != "series_mismatch" {
		t.Fatalf("wrong Sonarr series = accepted=%#v rejected=%#v err=%v", accepted, rejected, err)
	}
	incomplete, _ := json.Marshal([]SonarrManualImportResource{{
		Path: "/downloads/incoming/episode.mkv", Series: &ArrSeriesReference{ID: json.RawMessage(`201`)},
		Episodes: []ArrEpisodeReference{{ID: json.RawMessage(`301`)}},
	}})
	accepted, rejected, err = sonarr.mapPreviewResponseFor(incomplete, []ports.ImportFile{
		sonarrFile,
		{Source: sonarrFile.Source, MovieOrEpisodeID: "302"},
	}, "/downloads/incoming", "201", map[string][]string{sourceKey(sonarrFile.Source): []string{"301", "302"}}, nil)
	if err != nil || len(accepted) != 0 || len(rejected) != 2 || rejected[0].Code != "episode_set_mismatch" || rejected[1].Code != "episode_set_mismatch" {
		t.Fatalf("incomplete Sonarr episode set = accepted=%#v rejected=%#v err=%v", accepted, rejected, err)
	}
	seasonPack := fixture(t, "sonarr-anime-season-pack.json")
	seasonSource := domain.FileTarget{RootID: "library", RelativePath: "incoming/anime-season-pack.mkv"}
	seasonFiles := []ports.ImportFile{{Source: seasonSource, MovieOrEpisodeID: "401"}, {Source: seasonSource, MovieOrEpisodeID: "402"}}
	accepted, rejected, err = sonarr.mapPreviewResponseFor(seasonPack, seasonFiles, "/downloads/incoming", "201", nil, nil)
	if err != nil || len(accepted) != 2 || len(rejected) != 0 {
		t.Fatalf("anime season-pack association = accepted=%#v rejected=%#v err=%v", accepted, rejected, err)
	}
	var seasonResources []SonarrManualImportResource
	if err := decodeJSON(seasonPack, &seasonResources); err != nil || len(seasonResources) != 1 || seasonResources[0].ReleaseType != "seasonPack" || len(seasonResources[0].Episodes) != 2 || seasonResources[0].Episodes[0].AbsoluteEpisodeNumber != 1 || seasonResources[0].Episodes[1].AbsoluteEpisodeNumber != 2 {
		t.Fatalf("anime absolute season-pack evidence = %#v, err=%v", seasonResources, err)
	}
	nestedMismatch, _ := json.Marshal([]SonarrManualImportResource{{
		Path: "/downloads/incoming/episode.mkv", Series: &ArrSeriesReference{ID: json.RawMessage(`201`)},
		Episodes: []ArrEpisodeReference{{ID: json.RawMessage(`301`), SeriesID: json.RawMessage(`999`)}},
	}})
	accepted, rejected, err = sonarr.mapPreviewResponseFor(nestedMismatch, []ports.ImportFile{sonarrFile}, "/downloads/incoming", "201", nil, nil)
	if err != nil || len(accepted) != 0 || len(rejected) != 1 || rejected[0].Code != "episode_series_mismatch" {
		t.Fatalf("nested Sonarr series mismatch = accepted=%#v rejected=%#v err=%v", accepted, rejected, err)
	}
}

func TestArrPreviewRevisionBindsSemanticScope(t *testing.T) {
	sourceA := domain.FileTarget{RootID: "library", RelativePath: "incoming/a.mkv"}
	sourceB := domain.FileTarget{RootID: "library", RelativePath: "incoming/b.en.srt"}
	base := ports.ImportPreviewRequest{
		RegisteredExternalID: "101", Transfer: "copy",
		Files: []ports.ImportFile{
			{Source: sourceA, MovieOrEpisodeID: "101"},
			{Source: sourceB, MovieOrEpisodeID: "101", Subtitle: true, Language: "eng"},
		},
	}
	evidence := [][]byte{[]byte(`[{"id":1}]`)}
	baseRevision := previewRevision(base, evidence)
	reordered := base
	reordered.Files = []ports.ImportFile{base.Files[1], base.Files[0]}
	if got := previewRevision(reordered, evidence); got != baseRevision {
		t.Fatalf("reordered preview changed revision: %s != %s", got, baseRevision)
	}
	for name, changed := range map[string]ports.ImportPreviewRequest{
		"subtitle": func() ports.ImportPreviewRequest {
			value := base
			value.Files = append([]ports.ImportFile(nil), base.Files...)
			value.Files[1].Subtitle = false
			return value
		}(),
		"language": func() ports.ImportPreviewRequest {
			value := base
			value.Files = append([]ports.ImportFile(nil), base.Files...)
			value.Files[1].Language = "por"
			return value
		}(),
		"forced": func() ports.ImportPreviewRequest {
			value := base
			value.Files = append([]ports.ImportFile(nil), base.Files...)
			value.Files[1].Forced = true
			return value
		}(),
		"sdh": func() ports.ImportPreviewRequest {
			value := base
			value.Files = append([]ports.ImportFile(nil), base.Files...)
			value.Files[1].HearingImpaired = true
			return value
		}(),
	} {
		if got := previewRevision(changed, evidence); got == baseRevision {
			t.Fatalf("%s semantic variation reused revision %s", name, got)
		}
	}
	if got := previewRevision(base, [][]byte{[]byte(`[{"id":2}]`)}); got == baseRevision {
		t.Fatal("native evidence variation reused preview revision")
	}
	duplicate := base
	duplicate.Files = append(append([]ports.ImportFile(nil), base.Files...), base.Files[0])
	client, server := newFixtureClient(t, domain.ConnectionRadarr, &arrFixtureHandler{kind: domain.ConnectionRadarr}, "radarr-preview-duplicate")
	defer server.Close()
	_, err := client.PreviewImport(context.Background(), "radarr-preview-duplicate", duplicate)
	assertUpstreamCode(t, err, domain.OutcomeInvalidInput)

	reprocess := ReprocessPreviewRequest{RegisteredExternalID: "201", Transfer: "hardlink", Files: []ReprocessFile{{
		Source: sourceA, MovieOrEpisodeID: "301", EpisodeIDs: []string{"301", "302"}, DownloadID: "download-a", Subtitle: false,
	}}}
	reprocessRevision := client.reprocessRevision(reprocess, []byte(`[1]`))
	reorderedEpisodes := reprocess
	reorderedEpisodes.Files = append([]ReprocessFile(nil), reprocess.Files...)
	reorderedEpisodes.Files[0].EpisodeIDs = []string{"302", "301"}
	if got := client.reprocessRevision(reorderedEpisodes, []byte(`[1]`)); got != reprocessRevision {
		t.Fatalf("reordered episode set changed revision: %s != %s", got, reprocessRevision)
	}
	changedDownload := reprocess
	changedDownload.Files = append([]ReprocessFile(nil), reprocess.Files...)
	changedDownload.Files[0].DownloadID = "download-b"
	if got := client.reprocessRevision(changedDownload, []byte(`[1]`)); got == reprocessRevision {
		t.Fatal("source download variation reused reprocess revision")
	}
}

func TestArrReprocessRejectsDuplicatePhysicalSourcesBeforePOST(t *testing.T) {
	handler := &arrFixtureHandler{kind: domain.ConnectionSonarr}
	client, server := newFixtureClient(t, domain.ConnectionSonarr, handler, "sonarr-duplicate-source")
	defer server.Close()
	source := domain.FileTarget{RootID: "library", RelativePath: "series/season-pack.mkv"}
	_, err := client.ReprocessPreview(context.Background(), "sonarr-duplicate-source", ReprocessPreviewRequest{
		RegisteredExternalID: "201", Transfer: "copy", Files: []ReprocessFile{
			{Source: source, MovieOrEpisodeID: "301", EpisodeIDs: []string{"301"}, DownloadID: "download-a", Quality: json.RawMessage(`{"quality":{"name":"WEB-1080p"}}`)},
			{Source: source, MovieOrEpisodeID: "302", EpisodeIDs: []string{"302"}, DownloadID: "download-b", Quality: json.RawMessage(`{"quality":{"name":"WEB-1080p"}}`)},
		},
	})
	assertUpstreamCode(t, err, domain.OutcomeInvalidInput)
	handler.mu.Lock()
	posts := len(handler.manualPosts)
	handler.mu.Unlock()
	if posts != 0 {
		t.Fatalf("duplicate physical source escaped to Arr POST: %d payloads", posts)
	}
}

func TestArrNativeCandidateReprocessMappingPreservesFields(t *testing.T) {
	client, server := newFixtureClient(t, domain.ConnectionRadarr, &arrFixtureHandler{kind: domain.ConnectionRadarr}, "radarr-native-mapping")
	defer server.Close()
	request := ports.ImportPreviewRequest{
		RegisteredExternalID: "101", Transfer: "copy", Files: []ports.ImportFile{{
			Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/Synthetic Film (2024).mkv"}, MovieOrEpisodeID: "101",
		}},
	}
	nativeBody := []byte(`[{"id":771,"path":"/downloads/incoming/Synthetic Film (2024).mkv","relativePath":"Synthetic Film (2024).mkv","movie":{"id":101,"title":"Synthetic Film"},"releaseGroup":"NativeGroup","quality":{"quality":{"name":"WEB-1080p"}},"languages":[{"id":1,"name":"English"}],"downloadId":"synthetic-download-1","customFormats":[{"id":9,"name":"HDR"}],"customFormatScore":42,"indexerFlags":7,"rejections":[]}]`)
	reprocess, preview, err := client.ReprocessRequestFromNativePreview(request, nativeBody)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Files) != 1 || len(preview.Rejections) != 0 || len(reprocess.Files) != 1 {
		t.Fatalf("native mapping preview=%#v request=%#v", preview, reprocess)
	}
	file := reprocess.Files[0]
	if file.Source != request.Files[0].Source || file.MovieOrEpisodeID != "101" || file.NativeID != "771" || file.NativePath != "/downloads/incoming/Synthetic Film (2024).mkv" || file.NativeRelativePath != "Synthetic Film (2024).mkv" || file.DownloadID != "synthetic-download-1" {
		t.Fatalf("native source mapping lost identity: %#v", file)
	}
	if file.ReleaseGroup != "NativeGroup" || len(file.Languages) != 1 || file.Languages[0].ID != 1 || file.Languages[0].Name != "English" || len(file.CustomFormats) != 1 || file.CustomFormatScore != 42 || file.IndexerFlags != 7 {
		t.Fatalf("native reprocess fields = %#v", file)
	}
	if string(file.Quality) != `{"quality":{"name":"WEB-1080p"}}` || len(file.NativeLanguages) != 1 {
		t.Fatalf("native nested fields = quality %s languages %#v", file.Quality, file.NativeLanguages)
	}
	networkHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != apiManualImport || request.Header.Get("X-Api-Key") != "fixture-api-key" {
			response.WriteHeader(http.StatusNotFound)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(nativeBody)
	})
	networkClient, networkServer := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-native-network", networkHandler, 2, 10, 50, 50)
	defer networkServer.Close()
	networkPreview, err := networkClient.PreviewImportForReprocess(context.Background(), "radarr-native-network", NativePreviewRequest{Request: request})
	if err != nil || len(networkPreview.Preview.Files) != 1 || len(networkPreview.Preview.Rejections) != 0 || len(networkPreview.Request.Files) != 1 || networkPreview.Request.Files[0].DownloadID != "synthetic-download-1" {
		t.Fatalf("network native mapping = %#v, err=%v", networkPreview, err)
	}

	subtitleBody := []byte(`[
{"id":781,"path":"/downloads/incoming/pair.idx","relativePath":"pair.idx","movie":{"id":101},"languages":[],"rejections":[]},
{"id":782,"path":"/downloads/incoming/pair.sub","relativePath":"pair.sub","movie":{"id":101},"languages":[],"rejections":[]}
]`)
	subtitleRequest := ports.ImportPreviewRequest{RegisteredExternalID: "101", Transfer: "copy", Files: []ports.ImportFile{
		{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/pair.idx"}, MovieOrEpisodeID: "101", Subtitle: true},
		{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/pair.sub"}, MovieOrEpisodeID: "101", Subtitle: true},
	}}
	idxSource := subtitleRequest.Files[0].Source
	subSource := subtitleRequest.Files[1].Source
	paired, subtitlePreview, err := client.ReprocessRequestFromNativePreviewWithPairs(NativePreviewRequest{
		Request: subtitleRequest,
		SubtitlePairIDs: map[string]string{
			NativeSourceKey(idxSource): "pair-idx-sub-1",
			NativeSourceKey(subSource): "pair-idx-sub-1",
		},
	}, subtitleBody)
	if err != nil || len(subtitlePreview.Rejections) != 0 || len(paired.Files) != 2 {
		t.Fatalf("IDX/SUB native mapping preview=%#v request=%#v err=%v", subtitlePreview, paired, err)
	}
	if !paired.Files[0].NativeSubtitle || !paired.Files[1].NativeSubtitle || paired.Files[0].NativePath == paired.Files[1].NativePath || paired.Files[0].Source.RelativePath == paired.Files[1].Source.RelativePath || paired.Files[0].SubtitlePairID != "pair-idx-sub-1" || paired.Files[1].SubtitlePairID != "pair-idx-sub-1" {
		t.Fatalf("IDX/SUB pair sources were not retained independently: %#v", paired.Files)
	}
	single, singlePreview, err := client.ReprocessRequestFromNativePreview(ports.ImportPreviewRequest{
		RegisteredExternalID: "101", Transfer: "copy", Files: []ports.ImportFile{{
			Source: idxSource, MovieOrEpisodeID: "101", Subtitle: true,
		}},
	}, subtitleBody)
	if err != nil || len(singlePreview.Files) != 1 || len(single.Files) != 1 || single.Files[0].SubtitlePairID != "" {
		t.Fatalf("unpaired IDX was inferred as a companion: request=%#v preview=%#v err=%v", single, singlePreview, err)
	}
	_, _, err = client.ReprocessRequestFromNativePreviewWithPairs(NativePreviewRequest{
		Request: subtitleRequest, SubtitlePairIDs: map[string]string{NativeSourceKey(idxSource): "only-one"},
	}, subtitleBody)
	assertUpstreamCode(t, err, domain.OutcomeInvalidInput)
}

func TestSonarrNativeCandidateReprocessMappingPreservesEpisodeNumbers(t *testing.T) {
	client, server := newFixtureClient(t, domain.ConnectionSonarr, &arrFixtureHandler{kind: domain.ConnectionSonarr}, "sonarr-native-mapping")
	defer server.Close()
	request := ports.ImportPreviewRequest{
		RegisteredExternalID: "201", Transfer: "hardlink", Files: []ports.ImportFile{
			{Source: domain.FileTarget{RootID: "library", RelativePath: "series/Synthetic Series - S01E01.mkv"}, MovieOrEpisodeID: "301"},
			{Source: domain.FileTarget{RootID: "library", RelativePath: "series/Synthetic Series - S01E01.mkv"}, MovieOrEpisodeID: "302"},
		},
	}
	nativeBody := []byte(`[{"id":991,"path":"/downloads/series/Synthetic Series - S01E01.mkv","relativePath":"Synthetic Series - S01E01.mkv","series":{"id":201,"title":"Synthetic Series"},"seasonNumber":1,"episodes":[{"id":301,"seriesId":201,"seasonNumber":1,"episodeNumber":1,"absoluteEpisodeNumber":101,"sceneAbsoluteEpisodeNumber":1001},{"id":302,"seriesId":201,"seasonNumber":1,"episodeNumber":2,"absoluteEpisodeNumber":102,"sceneAbsoluteEpisodeNumber":1002}],"releaseGroup":"AnimeNative","quality":{"quality":{"name":"WEBDL-1080p"}},"languages":[{"id":8,"name":"Japanese"}],"downloadId":"sonarr-download-9","releaseType":"seasonPack","customFormats":[{"id":12,"name":"Anime"}],"customFormatScore":99,"indexerFlags":5,"rejections":[]}]`)
	reprocess, preview, err := client.ReprocessRequestFromNativePreview(request, nativeBody)
	if err != nil || len(preview.Files) != 2 || len(preview.Rejections) != 0 || len(reprocess.Files) != 1 {
		t.Fatalf("Sonarr native mapping preview=%#v request=%#v err=%v", preview, reprocess, err)
	}
	file := reprocess.Files[0]
	if file.MovieOrEpisodeID != "301" || len(file.EpisodeIDs) != 2 || file.EpisodeIDs[0] != "301" || file.EpisodeIDs[1] != "302" || file.DownloadID != "sonarr-download-9" || file.ReleaseType != "seasonPack" || file.SeasonNumber == nil || *file.SeasonNumber != 1 {
		t.Fatalf("Sonarr native association = %#v", file)
	}
	if len(file.Episodes) != 2 || scalarString(file.Episodes[0].SeriesID) != "201" || file.Episodes[0].AbsoluteEpisodeNumber != 101 || file.Episodes[1].SceneAbsoluteEpisodeNumber != 1002 {
		t.Fatalf("Sonarr native episode numbering = %#v", file.Episodes)
	}
	if file.ReleaseGroup != "AnimeNative" || len(file.Languages) != 1 || file.Languages[0].ID != 8 || len(file.CustomFormats) != 1 || file.CustomFormatScore != 99 || file.IndexerFlags != 5 {
		t.Fatalf("Sonarr native reprocess fields = %#v", file)
	}
}

func TestArrNativeNestedObjectsRejectUntypedValuesBeforePOST(t *testing.T) {
	const validQuality = `"quality":{"quality":{"name":"WEB-1080p"}}`
	const validCustomFormats = `"customFormats":[{"id":9,"name":"HDR"}]`
	cases := []struct {
		name  string
		body  string
		file  ReprocessFile
		field string
	}{
		{
			name: "quality unknown-only object",
			body: `[{"id":771,"path":"/downloads/incoming/native.mkv","relativePath":"native.mkv","movie":{"id":101},"quality":{"junk":true},` + validCustomFormats + `,"rejections":[]}]`,
			file: ReprocessFile{Quality: json.RawMessage(`{"junk":true}`)}, field: "quality",
		},
		{
			name: "quality null",
			body: `[{"id":771,"path":"/downloads/incoming/native.mkv","relativePath":"native.mkv","movie":{"id":101},"quality":null,` + validCustomFormats + `,"rejections":[]}]`,
			file: ReprocessFile{Quality: json.RawMessage(`null`)}, field: "quality",
		},
		{
			name: "custom format unknown-only object",
			body: `[{"id":771,"path":"/downloads/incoming/native.mkv","relativePath":"native.mkv","movie":{"id":101},` + validQuality + `,"customFormats":[{"junk":true}],"rejections":[]}]`,
			file: ReprocessFile{Quality: json.RawMessage(`{"quality":{"name":"WEB-1080p"}}`), CustomFormats: []json.RawMessage{json.RawMessage(`{"junk":true}`)}}, field: "custom_formats",
		},
		{
			name: "custom format null member",
			body: `[{"id":771,"path":"/downloads/incoming/native.mkv","relativePath":"native.mkv","movie":{"id":101},` + validQuality + `,"customFormats":[null],"rejections":[]}]`,
			file: ReprocessFile{Quality: json.RawMessage(`{"quality":{"name":"WEB-1080p"}}`), CustomFormats: []json.RawMessage{json.RawMessage(`null`)}}, field: "custom_formats",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var posts int
			connectionID := domain.ConfigID("radarr-native-nested-" + strings.ReplaceAll(strings.ToLower(testCase.name), " ", "-"))
			handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Header.Get("X-Api-Key") != "fixture-api-key" {
					response.WriteHeader(http.StatusUnauthorized)
					return
				}
				if request.Method == http.MethodPost {
					if request.URL.Path == "/api/v3/command" {
						t.Fatalf("native nested validation reached command endpoint")
					}
					posts++
					writeJSON(response, []any{})
					return
				}
				if request.Method != http.MethodGet || request.URL.Path != apiManualImport {
					response.WriteHeader(http.StatusNotFound)
					return
				}
				response.Header().Set("Content-Type", "application/json")
				_, _ = response.Write([]byte(testCase.body))
			})
			client, server := newSyntheticClient(t, domain.ConnectionRadarr, connectionID, handler, 2, 10, 50, 50)
			defer server.Close()
			request := NativePreviewRequest{Request: ports.ImportPreviewRequest{
				RegisteredExternalID: "101", Transfer: "copy", Files: []ports.ImportFile{{
					Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/native.mkv"}, MovieOrEpisodeID: "101",
				}},
			}}
			preview, err := client.PreviewImportForReprocess(context.Background(), connectionID, request)
			if err != nil {
				t.Fatalf("native nested preview returned error instead of rejection evidence: %v", err)
			}
			if len(preview.Preview.Files) != 0 || len(preview.Request.Files) != 0 || len(preview.Preview.Rejections) != 1 {
				t.Fatalf("native nested value escaped mapping: %#v", preview)
			}
			if preview.Preview.Rejections[0].Code == "" {
				t.Fatalf("native nested rejection lacks code: %#v", preview.Preview.Rejections[0])
			}

			testCase.file.Source = domain.FileTarget{RootID: "library", RelativePath: "incoming/native.mkv"}
			testCase.file.MovieOrEpisodeID = "101"
			_, err = client.ReprocessPreview(context.Background(), connectionID, ReprocessPreviewRequest{
				RegisteredExternalID: "101", Transfer: "copy", Files: []ReprocessFile{testCase.file},
			})
			assertUpstreamCode(t, err, domain.OutcomeInvalidInput)
			if posts != 0 {
				t.Fatalf("%s reached Arr POST with %d payloads", testCase.field, posts)
			}
		})
	}
}

func TestArrProductSpecificQualityValidation(t *testing.T) {
	validRadarrQuality := json.RawMessage(`{"quality":{"id":3,"name":"WEB-1080p","source":"webdl","resolution":1080},"revision":{"version":1,"real":1,"isRepack":false}}`)
	validSonarrQuality := json.RawMessage(`{"quality":{"id":3,"name":"WEBDL-1080p","source":"web","resolution":1080},"revision":{"version":1,"real":1,"isRepack":false}}`)
	sonarrQualityWithRadarrSource := json.RawMessage(`{"quality":{"id":3,"name":"WEB-1080p","source":"webdl","resolution":1080}}`)
	radarrQualityWithSonarrSource := json.RawMessage(`{"quality":{"id":3,"name":"WEBDL-1080p","source":"web","resolution":1080}}`)
	sonarrQualityWithRadarrModifier := json.RawMessage(`{"quality":{"id":3,"name":"WEBDL-1080p","source":"web","resolution":1080,"modifier":"none"}}`)
	cases := []struct {
		name               string
		kind               domain.ConnectionKind
		registeredID       string
		file               ReprocessFile
		wantError          bool
		wantPreview        bool
		wantPosts          int
		wantResponseDetail string
	}{
		{
			name: "Radarr accepts Radarr quality source", kind: domain.ConnectionRadarr, registeredID: "101",
			file:        ReprocessFile{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/Synthetic Film (2024).mkv"}, MovieOrEpisodeID: "101", Quality: validRadarrQuality, CustomFormats: []json.RawMessage{json.RawMessage(`{"id":9,"name":"HDR"}`)}},
			wantPreview: true, wantPosts: 1,
		},
		{
			name: "Sonarr accepts pinned web quality source", kind: domain.ConnectionSonarr, registeredID: "201",
			file:        ReprocessFile{Source: domain.FileTarget{RootID: "library", RelativePath: "series/Synthetic Series - S01E01.mkv"}, MovieOrEpisodeID: "301", EpisodeIDs: []string{"301", "302"}, Quality: validSonarrQuality, CustomFormats: []json.RawMessage{json.RawMessage(`{"id":12,"name":"Anime"}`)}},
			wantPreview: true, wantPosts: 1,
		},
		{
			name: "Radarr rejects Sonarr quality source", kind: domain.ConnectionRadarr, registeredID: "101",
			file:      ReprocessFile{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/Synthetic Film (2024).mkv"}, MovieOrEpisodeID: "101", Quality: radarrQualityWithSonarrSource},
			wantError: true, wantResponseDetail: "radarr accepted Sonarr-only source",
		},
		{
			name: "Sonarr rejects Radarr quality source", kind: domain.ConnectionSonarr, registeredID: "201",
			file:      ReprocessFile{Source: domain.FileTarget{RootID: "library", RelativePath: "series/Synthetic Series - S01E01.mkv"}, MovieOrEpisodeID: "301", EpisodeIDs: []string{"301", "302"}, Quality: sonarrQualityWithRadarrSource},
			wantError: true, wantResponseDetail: "Sonarr accepted Radarr-only source",
		},
		{
			name: "Sonarr rejects Radarr quality modifier", kind: domain.ConnectionSonarr, registeredID: "201",
			file:      ReprocessFile{Source: domain.FileTarget{RootID: "library", RelativePath: "series/Synthetic Series - S01E01.mkv"}, MovieOrEpisodeID: "301", EpisodeIDs: []string{"301", "302"}, Quality: sonarrQualityWithRadarrModifier},
			wantError: true, wantResponseDetail: "Sonarr accepted Radarr-only modifier",
		},
		{
			name: "Radarr video rejects missing quality", kind: domain.ConnectionRadarr, registeredID: "101",
			file:      ReprocessFile{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/Synthetic Film (2024).mkv"}, MovieOrEpisodeID: "101"},
			wantError: true, wantResponseDetail: "Radarr video without quality reached POST",
		},
		{
			name: "Sonarr video rejects missing quality", kind: domain.ConnectionSonarr, registeredID: "201",
			file:      ReprocessFile{Source: domain.FileTarget{RootID: "library", RelativePath: "series/Synthetic Series - S01E01.mkv"}, MovieOrEpisodeID: "301", EpisodeIDs: []string{"301", "302"}},
			wantError: true, wantResponseDetail: "Sonarr video without quality reached POST",
		},
		{
			name: "Sonarr rejects untyped custom format before POST", kind: domain.ConnectionSonarr, registeredID: "201",
			file:      ReprocessFile{Source: domain.FileTarget{RootID: "library", RelativePath: "series/Synthetic Series - S01E01.mkv"}, MovieOrEpisodeID: "301", EpisodeIDs: []string{"301", "302"}, Quality: validSonarrQuality, CustomFormats: []json.RawMessage{json.RawMessage(`{"unexpected":true}`)}},
			wantError: true, wantResponseDetail: "Sonarr untyped custom format reached POST",
		},
	}
	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			connectionID := domain.ConfigID("arr-quality-" + strconv.Itoa(index))
			handler := &arrFixtureHandler{kind: testCase.kind}
			client, server := newFixtureClient(t, testCase.kind, handler, connectionID)
			defer server.Close()
			preview, err := client.ReprocessPreview(context.Background(), connectionID, ReprocessPreviewRequest{
				RegisteredExternalID: testCase.registeredID, Transfer: "copy", Files: []ReprocessFile{testCase.file},
			})
			if testCase.wantError {
				assertUpstreamCode(t, err, domain.OutcomeInvalidInput)
			} else if err != nil || !testCase.wantPreview || len(preview.Files) != 1 {
				t.Fatalf("valid %s request preview=%#v err=%v", testCase.kind, preview, err)
			}
			handler.mu.Lock()
			posts := len(handler.manualPosts)
			handler.mu.Unlock()
			if posts != testCase.wantPosts {
				t.Fatalf("%s: %s (posts=%d, want %d)", testCase.name, testCase.wantResponseDetail, posts, testCase.wantPosts)
			}
		})
	}
}

func TestArrQualitySourceEnumsAreProductSpecific(t *testing.T) {
	radarrSources := []string{"unknown", "cam", "telesync", "telecine", "workprint", "dvd", "tv", "webdl", "webrip", "bluray"}
	sonarrSources := []string{"unknown", "television", "televisionRaw", "web", "webRip", "dvd", "bluray", "blurayRaw"}
	for _, source := range radarrSources {
		if !validQualitySourceForKind(source, domain.ConnectionRadarr) {
			t.Fatalf("Radarr source %q was rejected", source)
		}
	}
	for _, source := range sonarrSources {
		if !validQualitySourceForKind(source, domain.ConnectionSonarr) {
			t.Fatalf("Sonarr source %q was rejected", source)
		}
	}
	for _, source := range []string{"television", "televisionRaw", "web", "webRip", "blurayRaw"} {
		if validQualitySourceForKind(source, domain.ConnectionRadarr) {
			t.Fatalf("Radarr accepted Sonarr-only source %q", source)
		}
	}
	for _, source := range []string{"cam", "telesync", "telecine", "workprint", "tv", "webdl", "webrip"} {
		if validQualitySourceForKind(source, domain.ConnectionSonarr) {
			t.Fatalf("Sonarr accepted Radarr-only source %q", source)
		}
	}
}

func TestSonarrEpisodeFileEvidenceRejectsContradictions(t *testing.T) {
	cases := []struct {
		name      string
		episodes  []map[string]any
		wantError bool
	}{
		{
			name: "outer and nested file IDs disagree",
			episodes: []map[string]any{{
				"id": 301, "seriesId": 201, "episodeFileId": 801,
				"episodeFile": map[string]any{"id": 802, "path": "/downloads/series/one.mkv", "size": 10},
			}}, wantError: true,
		},
		{
			name: "repeated ID changes physical details",
			episodes: []map[string]any{
				{"id": 301, "seriesId": 201, "episodeFileId": 801, "episodeFile": map[string]any{"id": 801, "path": "/downloads/series/one.mkv", "size": 10}},
				{"id": 302, "seriesId": 201, "episodeFileId": 801, "episodeFile": map[string]any{"id": 801, "path": "/downloads/series/two.mkv", "size": 20}},
			}, wantError: true,
		},
		{
			name: "same path has different IDs",
			episodes: []map[string]any{
				{"id": 301, "seriesId": 201, "episodeFileId": 801, "episodeFile": map[string]any{"id": 801, "path": "/downloads/series/one.mkv", "size": 10}},
				{"id": 302, "seriesId": 201, "episodeFileId": 802, "episodeFile": map[string]any{"id": 802, "path": "/downloads/series/one.mkv", "size": 10}},
			}, wantError: true,
		},
		{
			name: "episode identity appears on different files",
			episodes: []map[string]any{
				{"id": 301, "seriesId": 201, "episodeFileId": 801, "episodeFile": map[string]any{"id": 801, "path": "/downloads/series/one.mkv", "size": 10}},
				{"id": 301, "seriesId": 201, "episodeFileId": 802, "episodeFile": map[string]any{"id": 802, "path": "/downloads/series/two.mkv", "size": 20}},
			}, wantError: true,
		},
		{
			name: "byte-equivalent multi-episode evidence",
			episodes: []map[string]any{
				{"id": 301, "seriesId": 201, "episodeFileId": 801, "episodeFile": map[string]any{"id": 801, "path": "/downloads/series/one.mkv", "size": 10}},
				{"id": 302, "seriesId": 201, "episodeFileId": 801, "episodeFile": map[string]any{"id": 801, "path": "/downloads/series/one.mkv", "size": 10}},
			}, wantError: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Header.Get("X-Api-Key") != "fixture-api-key" {
					response.WriteHeader(http.StatusUnauthorized)
					return
				}
				if request.Method != http.MethodGet || request.URL.Path != apiEpisodes || request.URL.Query().Get("includeEpisodeFile") != "true" {
					response.WriteHeader(http.StatusNotFound)
					return
				}
				writeJSON(response, testCase.episodes)
			})
			connectionID := domain.ConfigID("sonarr-episode-file-" + strings.ReplaceAll(strings.ToLower(testCase.name), " ", "-"))
			client, server := newSyntheticClient(t, domain.ConnectionSonarr, connectionID, handler, 2, 10, 50, 50)
			defer server.Close()
			observation, err := client.ObserveImport(context.Background(), connectionID, "201")
			if testCase.wantError {
				assertUpstreamCode(t, err, domain.OutcomeUnknown)
				return
			}
			if err != nil || len(observation.Files) != 1 || len(observation.Files[0].EpisodeIDs) != 2 || observation.Files[0].EpisodeIDs[0] != "301" || observation.Files[0].EpisodeIDs[1] != "302" {
				t.Fatalf("valid multi-episode observation = %#v, %v", observation, err)
			}
		})
	}
}

func TestArrManualImportRedirectsNeverReachCommand(t *testing.T) {
	var commandCalls int
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v3/command" {
			commandCalls++
			response.WriteHeader(http.StatusInternalServerError)
			return
		}
		if request.URL.Path == apiManualImport {
			response.Header().Set("Location", "/api/v3/command")
			response.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		response.WriteHeader(http.StatusNotFound)
	})
	client, server := newSyntheticClient(t, domain.ConnectionRadarr, "radarr-redirect", handler, 2, 10, 50, 50)
	defer server.Close()
	request := ports.ImportPreviewRequest{RegisteredExternalID: "101", Transfer: "copy", Files: []ports.ImportFile{{
		Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/a.mkv"}, MovieOrEpisodeID: "101",
	}}}
	_, err := client.PreviewImport(context.Background(), "radarr-redirect", request)
	assertUpstreamCode(t, err, domain.OutcomeUnknown)
	_, err = client.ReprocessPreview(context.Background(), "radarr-redirect", ReprocessPreviewRequest{RegisteredExternalID: "101", Transfer: "copy", Files: []ReprocessFile{{
		Source: request.Files[0].Source, MovieOrEpisodeID: "101", Quality: json.RawMessage(`{"quality":{"name":"WEB-1080p"}}`),
	}}})
	assertUpstreamCode(t, err, domain.OutcomeUnknown)
	if commandCalls != 0 {
		t.Fatalf("redirect reached command endpoint %d times", commandCalls)
	}
}

func TestArrSubtitleEvidenceAndUnsupportedAttributes(t *testing.T) {
	client, server := newFixtureClient(t, domain.ConnectionRadarr, &arrFixtureHandler{kind: domain.ConnectionRadarr}, "radarr-subtitles")
	defer server.Close()
	body := fixture(t, "radarr-subtitle-evidence.json")
	files := []ports.ImportFile{
		{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/video.mkv"}, MovieOrEpisodeID: "101"},
		{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/video.en.srt"}, MovieOrEpisodeID: "101", Subtitle: true, Language: "eng"},
		{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/video.forced.en.srt"}, MovieOrEpisodeID: "101", Subtitle: true, Language: "eng", Forced: true},
		{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/video.sdh.en.srt"}, MovieOrEpisodeID: "101", Subtitle: true, Language: "eng", HearingImpaired: true},
		{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/video.idx"}, MovieOrEpisodeID: "101", Subtitle: true},
		{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/video.sub"}, MovieOrEpisodeID: "101", Subtitle: true},
		{Source: domain.FileTarget{RootID: "library", RelativePath: "incoming/unmatched.srt"}, MovieOrEpisodeID: "101", Subtitle: true},
	}
	accepted, rejected, err := client.mapPreviewResponseFor(body, files, "/downloads/incoming", "101", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(accepted) != 4 || len(rejected) != 3 {
		t.Fatalf("subtitle evidence accepted=%#v rejected=%#v", accepted, rejected)
	}
	reasons := make(map[string]bool, len(rejected))
	for _, item := range rejected {
		reasons[item.Code] = true
	}
	for _, code := range []string{"subtitle_forced_unknown", "subtitle_hearing_impaired_unknown", "not_returned"} {
		if !reasons[code] {
			t.Fatalf("subtitle evidence missing rejection %q: %#v", code, rejected)
		}
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
	if err != nil || len(second.Items) != 1 || second.NextCursor != "" {
		t.Fatalf("second catalog page = %#v, %v", second, err)
	}
	if second.NextCursor != "" || second.Coverage.Completeness != domain.CompletenessComplete || second.Coverage.ObservedCount != 2 {
		t.Fatalf("final catalog page = %#v", second)
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
	if err != nil || len(historyEnd.Items) != 1 || historyEnd.NextCursor != "" || historyEnd.Coverage.Completeness != domain.CompletenessPartial || !hasReason(historyEnd.Coverage, "history_snapshot_unverified") {
		t.Fatalf("history final page = %#v, %v", historyEnd, err)
	}
	if history.Items[0].Date.Location() != time.UTC || history.Items[0].MovieID != "101" || historyEnd.Items[0].Destination == "" {
		t.Fatalf("history evidence = %#v / %#v", history.Items[0], historyEnd.Items[0])
	}

	capabilities, err := client.Capabilities(context.Background(), "radarr-main")
	if err != nil || len(capabilities) != 5 || capabilities[4].State != domain.CapabilityUnknown {
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

	_, err = client.ObserveImport(context.Background(), "sonarr-main", "201")
	if err == nil {
		t.Fatal("sonarr observe accepted incomplete unmapped episode evidence")
	}
	handler.mu.Lock()
	episodeQueries := append([]url.Values(nil), handler.episodeQuery...)
	handler.mu.Unlock()
	if len(episodeQueries) < 2 {
		t.Fatalf("expected Sonarr inventory and observation episode reads, got %d", len(episodeQueries))
	}
	for _, query := range episodeQueries {
		if query.Get("includeEpisodeFile") != "true" {
			t.Fatalf("Sonarr episode read omitted includeEpisodeFile=true: %#v", query)
		}
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
			MovieOrEpisodeID: "101", Subtitle: true, Language: "eng",
		}},
	})
	if err != nil || len(subtitle.Files) != 1 || len(subtitle.Rejections) != 0 {
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
			MovieOrEpisodeID: "101", DownloadID: "synthetic-download-1", Quality: json.RawMessage(`{"quality":{"name":"WEB-1080p"}}`),
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
			MovieOrEpisodeID: "301", EpisodeIDs: []string{"301", "302"}, Language: "eng",
			SeasonNumber: func() *int { value := 1; return &value }(),
			Quality:      json.RawMessage(`{"quality":{"name":"WEB-1080p"}}`), ReleaseGroup: "SyntheticGroup",
			IndexerFlags: 3, ReleaseType: "singleEpisode",
		}},
	})
	if err != nil || len(reprocessed.Files) != 1 {
		t.Fatalf("sonarr reprocessed preview = %#v, %v", reprocessed, err)
	}
	handler.mu.Lock()
	posts := append([]map[string]any(nil), handler.manualPosts...)
	handler.mu.Unlock()
	if len(posts) != 1 || posts[0]["seriesId"] != float64(201) || posts[0]["importMode"] != nil || posts[0]["seasonNumber"] != float64(1) || posts[0]["releaseGroup"] != "SyntheticGroup" || posts[0]["releaseType"] != "singleEpisode" || posts[0]["indexerFlags"] != float64(3) {
		t.Fatalf("sonarr reprocess payload = %#v", posts)
	}
	if languages, ok := posts[0]["languages"].([]any); !ok || len(languages) != 1 {
		t.Fatalf("sonarr languages = %#v", posts[0]["languages"])
	}
	if quality, ok := posts[0]["quality"].(map[string]any); !ok || quality["quality"] == nil {
		t.Fatalf("sonarr quality = %#v", posts[0]["quality"])
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
