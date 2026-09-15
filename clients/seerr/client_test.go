package seerr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newFixtureClient(t *testing.T, handler http.Handler, config Config) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	if config.Endpoint == "" {
		config.Endpoint = server.URL + "/proxy"
	}
	client, err := New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client, server
}

func fixtureJSON(t *testing.T, writer http.ResponseWriter, body string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(writer, body)
}

func assertCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error", want)
	}
	var upstream UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("error %T does not carry upstream evidence: %v", err, err)
	}
	if upstream.Code != want {
		t.Fatalf("error code = %s, want %s", upstream.Code, want)
	}
}

func TestMediaPaginationPreservesNativeEvidence(t *testing.T) {
	var calls atomic.Int32
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/proxy/api/v1/media" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("X-Api-Key") != "synthetic-seerr-key" || request.Header.Get("Authorization") != "" {
			t.Fatalf("authentication headers = %#v", request.Header)
		}
		if request.URL.Query().Get("take") != "2" {
			t.Fatalf("take = %q", request.URL.Query().Get("take"))
		}
		switch request.URL.Query().Get("skip") {
		case "0":
			fixtureJSON(t, writer, `{"pageInfo":{"pages":2,"pageSize":2,"results":3,"page":1},"results":[{"id":101,"mediaType":"movie","tmdbId":550,"imdbId":"tt0137523","status":5,"status4k":4,"serviceId":7,"externalServiceId":7001,"externalServiceSlug":"radarr-main","jellyfinMediaId":"jf-101","requests":[{"id":9001,"status":5,"type":"movie","is4k":false}],"seasons":[]},{"id":102,"mediaType":"tv","tmdbId":1002,"tvdbId":2002,"status":4,"status4k":3,"serviceId":8,"externalServiceId":8002,"externalServiceSlug":"sonarr-main","seasons":[{"id":10201,"seasonNumber":1,"status":4,"status4k":3}]}]}`)
		case "2":
			fixtureJSON(t, writer, `{"pageInfo":{"pages":2,"pageSize":2,"results":3,"page":2},"results":[{"id":103,"mediaType":"movie","tmdbId":1030,"status":3,"status4k":99}]}`)
		default:
			t.Fatalf("unexpected skip = %q", request.URL.Query().Get("skip"))
		}
	})
	client, _ := newFixtureClient(t, handler, Config{APIKey: "synthetic-seerr-key", InstanceID: "seerr-main", MaxPageSize: 2})
	first, err := client.ListMedia(context.Background(), "", 2)
	if err != nil {
		t.Fatalf("first page error = %v", err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" || first.PageInfo.Page != 1 || !first.PageInfo.Complete {
		t.Fatalf("first page = %#v", first)
	}
	if first.Coverage.Completeness != CompletenessPartial || !hasReason(first.Coverage.ReasonCodes, "pagination_snapshot_unverified") {
		t.Fatalf("first coverage = %#v", first.Coverage)
	}
	film := first.Items[0]
	if film.ID != 101 || film.ScopedIdentity != "seerr-main:101" || film.ProviderIDs["tmdb"] != "550" || film.ProviderIDs["imdb"] != "tt0137523" || film.NativeStatus != int(MediaStatusAvailable) || !film.NativeStatusKnown || !film.Availability.Available || len(film.ServiceRelationships) != 1 || film.ServiceRelationships[0].Kind != "radarr" {
		t.Fatalf("film = %#v", film)
	}
	series := first.Items[1]
	if !series.Availability.PartiallyAvailable || series.Availability.Available || len(series.Seasons) != 1 || series.Seasons[0].NativeStatusName != "partially_available" {
		t.Fatalf("series = %#v", series)
	}
	second, err := client.ListMedia(context.Background(), first.NextCursor, 2)
	if err != nil {
		t.Fatalf("second page error = %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].ID != 103 || second.NextCursor != "" || second.Items[0].NativeStatus4K != 99 || second.Items[0].NativeStatus4KName != "unknown" {
		t.Fatalf("second page = %#v", second)
	}
	if second.Coverage.Completeness != CompletenessPartial || second.Coverage.ObservedCount != 3 || second.Coverage.CompletedAt == nil {
		t.Fatalf("second coverage = %#v", second.Coverage)
	}
	if calls.Load() != 2 {
		t.Fatalf("media calls = %d, want 2", calls.Load())
	}
}

func TestRequestPaginationPreservesNestedAvailabilityAndServiceErrors(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/proxy/api/v1/request" || request.URL.Query().Get("take") != "2" || request.URL.Query().Get("skip") != "0" {
			t.Fatalf("request = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
		}
		fixtureJSON(t, writer, `{"pageInfo":{"pages":1,"pageSize":2,"results":1,"page":1},"results":[{"id":9002,"status":2,"type":"tv","seasonCount":1,"is4k":true,"serverId":8,"profileId":4,"rootFolder":"/fixture/series","languageProfileId":2,"tags":[30],"isAutoRequest":true,"ignoreQuota":true,"media":{"id":102,"mediaType":"tv","tvdbId":2002,"status":4,"status4k":3,"serviceId":8,"externalServiceId":8002,"externalServiceSlug":"sonarr-main"},"seasons":[{"id":10201,"seasonNumber":1,"status":4,"status4k":3}]}],"serviceErrors":{"radarr":[{"id":99,"name":"offline-radarr"}],"sonarr":[]}}`)
	})
	client, _ := newFixtureClient(t, handler, Config{APIKey: "synthetic-seerr-key", MaxPageSize: 2})
	page, err := client.ListRequests(context.Background(), "", 2)
	if err != nil {
		t.Fatalf("request page error = %v", err)
	}
	if len(page.Items) != 1 || page.NextCursor != "" || page.Coverage.Completeness != CompletenessComplete {
		t.Fatalf("page = %#v", page)
	}
	if len(page.ServiceErrors) != 1 || page.ServiceErrors[0].Kind != "radarr" || page.ServiceErrors[0].ID != 99 || page.ServiceErrors[0].Name != "offline-radarr" {
		t.Fatalf("service errors = %#v", page.ServiceErrors)
	}
	requestValue := page.Items[0]
	if requestValue.ID != 9002 || requestValue.NativeStatusName != "approved" || !requestValue.MediaKnown || requestValue.Media.Availability.NativeStatusName != "partially_available" || !requestValue.Media.Availability.PartiallyAvailable || !requestValue.Is4K || requestValue.ServerID != 8 || !requestValue.ServerIDKnown || len(requestValue.Tags) != 1 || requestValue.Tags[0] != 30 || len(requestValue.ServiceRelationships) != 1 || requestValue.ServiceRelationships[0].Kind != "sonarr" {
		t.Fatalf("request value = %#v", requestValue)
	}
}

func TestStatusSupportsBearerAndStatusQuery(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/proxy/api/v1/status" || request.URL.Query().Get("checkUpdateAvailable") != "false" {
			t.Fatalf("request = %s %s?%s", request.Method, request.URL.Path, request.URL.RawQuery)
		}
		if request.Header.Get("Authorization") != "Bearer synthetic-seerr-token" || request.Header.Get("X-Api-Key") != "" {
			t.Fatalf("authentication headers = %#v", request.Header)
		}
		fixtureJSON(t, writer, `{"version":"3.4.1","commitTag":"fixture","commit":"fixture-commit","future":true}`)
	})
	client, _ := newFixtureClient(t, handler, Config{BearerToken: "synthetic-seerr-token"})
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.ProductName != "seerr" || status.Version != "3.4.1" || status.CommitTag != "fixture" || status.Commit != "fixture-commit" || status.ObservedAt.IsZero() {
		t.Fatalf("status = %#v", status)
	}
	if version, err := client.Version(context.Background()); err != nil || version != "3.4.1" {
		t.Fatalf("Version() = %q, %v", version, err)
	}
	if strings.Contains(client.String(), "synthetic-seerr-token") {
		t.Fatalf("String() exposed bearer token: %s", client)
	}
}

func TestPaginationMissingOrContradictoryMetadataNeverClaimsComplete(t *testing.T) {
	tests := map[string]struct {
		body       string
		wantCode   ErrorCode
		wantState  string
		wantReason string
	}{
		"missing pageInfo": {
			body:      `{"results":[{"id":1,"status":5}]}`,
			wantState: CompletenessUnknown, wantReason: "pagination_page_info_missing",
		},
		"missing page field": {
			body:      `{"pageInfo":{"pages":1,"pageSize":2,"results":1},"results":[{"id":1,"status":5}]}`,
			wantState: CompletenessUnknown, wantReason: "pagination_page_info_incomplete",
		},
		"short page before total": {
			body:      `{"pageInfo":{"pages":2,"pageSize":2,"results":4,"page":1},"results":[{"id":1,"status":5}]}`,
			wantState: CompletenessPartial, wantReason: "pagination_short_page_before_total",
		},
		"impossible total": {
			body:     `{"pageInfo":{"pages":1,"pageSize":1,"results":0,"page":1},"results":[{"id":1,"status":5}]}`,
			wantCode: ErrorMalformed,
		},
		"page exceeds pages": {
			body:     `{"pageInfo":{"pages":1,"pageSize":1,"results":1,"page":2},"results":[{"id":1,"status":5}]}`,
			wantCode: ErrorMalformed,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				fixtureJSON(t, writer, test.body)
			})
			client, _ := newFixtureClient(t, handler, Config{APIKey: "synthetic-seerr-key", MaxPageSize: 2})
			page, err := client.ListMedia(context.Background(), "", 2)
			if test.wantCode != "" {
				assertCode(t, err, test.wantCode)
				return
			}
			if err != nil || page.Coverage.Completeness != test.wantState || !hasReason(page.Coverage.ReasonCodes, test.wantReason) || page.NextCursor != "" {
				t.Fatalf("page = %#v, err = %v", page, err)
			}
		})
	}
}

func TestPaginationOverlapIsRetainedAsPartial(t *testing.T) {
	var calls atomic.Int32
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Query().Get("skip") == "0" {
			fixtureJSON(t, writer, `{"pageInfo":{"pages":2,"pageSize":2,"results":4,"page":1},"results":[{"id":1,"status":5},{"id":2,"status":5}]}`)
			return
		}
		fixtureJSON(t, writer, `{"pageInfo":{"pages":2,"pageSize":2,"results":4,"page":2},"results":[{"id":2,"status":5},{"id":3,"status":5}]}`)
	})
	client, _ := newFixtureClient(t, handler, Config{APIKey: "synthetic-seerr-key", MaxPageSize: 2})
	first, err := client.ListMedia(context.Background(), "", 2)
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first = %#v, err = %v", first, err)
	}
	second, err := client.ListMedia(context.Background(), first.NextCursor, 2)
	if err != nil || len(second.Items) != 1 || second.Items[0].ID != 3 || !hasReason(second.Coverage.ReasonCodes, "pagination_overlap") || second.Coverage.Completeness != CompletenessPartial {
		t.Fatalf("second = %#v, err = %v", second, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d", calls.Load())
	}
}

func TestErrorsBoundsMalformedAndReadOnlyMethods(t *testing.T) {
	t.Run("status mapping", func(t *testing.T) {
		for status, want := range map[int]ErrorCode{http.StatusUnauthorized: ErrorUnauthorized, http.StatusForbidden: ErrorForbidden, http.StatusTooManyRequests: ErrorRateLimited, http.StatusConflict: ErrorConflict, http.StatusInternalServerError: ErrorUnavailable, http.StatusNotFound: ErrorUnsupported} {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(status) })
			client, _ := newFixtureClient(t, handler, Config{APIKey: "synthetic-seerr-key"})
			_, err := client.Status(context.Background())
			assertCode(t, err, want)
		}
	})

	t.Run("malformed and oversized", func(t *testing.T) {
		bodies := []string{`{"version":"1","version":"2"}`, `{"version":"1"} {}`}
		for _, body := range bodies {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { fixtureJSON(t, writer, body) })
			client, _ := newFixtureClient(t, handler, Config{APIKey: "synthetic-seerr-key"})
			_, err := client.Status(context.Background())
			assertCode(t, err, ErrorMalformed)
		}
		large := strings.Repeat("x", 64)
		handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = io.WriteString(writer, `{"version":"`+large+`"}`)
		})
		client, _ := newFixtureClient(t, handler, Config{APIKey: "synthetic-seerr-key", MaxResponseBytes: 16})
		_, err := client.Status(context.Background())
		assertCode(t, err, ErrorResponseTooLarge)
	})

	t.Run("only GET routes are used", func(t *testing.T) {
		var writes atomic.Int32
		handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodGet {
				writes.Add(1)
			}
			switch request.URL.Path {
			case "/proxy/api/v1/status":
				fixtureJSON(t, writer, `{"version":"1"}`)
			case "/proxy/api/v1/media", "/proxy/api/v1/request":
				fixtureJSON(t, writer, `{"pageInfo":{"pages":1,"pageSize":2,"results":0,"page":1},"results":[]}`)
			default:
				t.Fatalf("unexpected route %s", request.URL.Path)
			}
		})
		client, _ := newFixtureClient(t, handler, Config{APIKey: "synthetic-seerr-key", MaxPageSize: 2})
		if _, err := client.Status(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := client.ListMedia(context.Background(), "", 2); err != nil {
			t.Fatal(err)
		}
		if _, err := client.ListRequests(context.Background(), "", 2); err != nil {
			t.Fatal(err)
		}
		if writes.Load() != 0 {
			t.Fatalf("write requests = %d", writes.Load())
		}
	})
}

func TestNewRejectsAmbiguousConfigurationAndRedirects(t *testing.T) {
	for _, endpoint := range []string{"", " https://seerr.invalid", "ftp://seerr.invalid", "https://user:pass@seerr.invalid", "https://seerr.invalid?token=secret", "https://seerr.invalid/%2f"} {
		if _, err := New(Config{Endpoint: endpoint}); err == nil {
			t.Fatalf("endpoint %q accepted", endpoint)
		}
	}
	if _, err := New(Config{Endpoint: "https://seerr.invalid", APIKey: " key"}); err == nil {
		t.Fatal("credential with surrounding whitespace accepted")
	}
	if _, err := New(Config{Endpoint: "https://seerr.invalid", RequestTimeout: -time.Second}); err == nil {
		t.Fatal("negative timeout accepted")
	}

	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Fatalf("redirect target received request")
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusFound)
	}))
	defer redirect.Close()
	client, err := New(Config{Endpoint: redirect.URL, APIKey: "synthetic-seerr-key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Status(context.Background())
	assertCode(t, err, ErrorUnsupported)
}

func TestInvalidUTF8AndContextCancellation(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte{'{', '"', 'v', 'e', 'r', 's', 'i', 'o', 'n', '"', ':', '"', 0xff, '"', '}'})
	}))
	defer bad.Close()
	client, err := New(Config{Endpoint: bad.URL, APIKey: "synthetic-seerr-key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Status(context.Background())
	assertCode(t, err, ErrorMalformed)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Status(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled status error = %v", err)
	}
}

func TestListAllHonorsConfiguredPageBound(t *testing.T) {
	var calls atomic.Int32
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		fixtureJSON(t, writer, `{"pageInfo":{"pages":3,"pageSize":1,"results":3,"page":`+fmt.Sprint(calls.Load())+`},"results":[{"id":`+fmt.Sprint(calls.Load())+`,"status":5}]}`)
	})
	client, _ := newFixtureClient(t, handler, Config{APIKey: "synthetic-seerr-key", MaxPageSize: 1, MaxPages: 2, MaxRecords: 10})
	page, err := client.ListAllMedia(context.Background(), 1)
	if err != nil || len(page.Items) != 2 || page.NextCursor != "" || page.Coverage.Completeness != CompletenessPartial || !hasReason(page.Coverage.ReasonCodes, "pagination_limit") {
		t.Fatalf("bounded page = %#v, err = %v", page, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}
