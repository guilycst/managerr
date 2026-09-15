package jellyfin

import (
	"context"
	"errors"
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
	if config.Token == "" && config.APIKey == "" && config.AuthToken == "" {
		config.Token = "fixture-jellyfin-token"
	}
	client, err := New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client, server
}

func writeFixtureJSON(t *testing.T, writer http.ResponseWriter, body string) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(writer, body)
}

func TestSystemInfoUsesTokenAndPreservesTypedObservation(t *testing.T) {
	var calls atomic.Int32
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/proxy/System/Info/Public" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if got := request.Header.Get("X-Emby-Token"); got != "fixture-jellyfin-token" {
			t.Fatalf("token = %q", got)
		}
		if got := request.Header.Get("Authorization"); got != "" {
			t.Fatalf("unexpected authorization header = %q", got)
		}
		writeFixtureJSON(t, writer, `{"ProductName":"Jellyfin","ServerName":"fixture","Version":"10.10.7","OperatingSystem":"Linux","Id":"server-1","StartupWizardCompleted":true,"future":true}`)
	})
	client, _ := newFixtureClient(t, handler, Config{})
	info, err := client.GetSystemInfo(context.Background())
	if err != nil {
		t.Fatalf("GetSystemInfo() error = %v", err)
	}
	if info.ProductName != "Jellyfin" || info.ServerName != "fixture" || info.Version != "10.10.7" || info.OperatingSystem != "Linux" || info.ServerID != "server-1" || info.StartupWizardCompleted == nil || !*info.StartupWizardCompleted || info.ObservedAt.IsZero() {
		t.Fatalf("info = %#v", info)
	}
	if version, err := client.Version(context.Background()); err != nil || version != "10.10.7" {
		t.Fatalf("Version() = %q, %v", version, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("system calls = %d, want 2", calls.Load())
	}
	if strings.Contains(client.String(), "fixture-jellyfin-token") {
		t.Fatalf("String() exposed token: %s", client)
	}
}

func TestLibrariesAcceptEnvelopeAndFallbackToUserViews(t *testing.T) {
	var primaryCalls atomic.Int32
	var fallbackCalls atomic.Int32
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/proxy/Library/MediaFolders":
			primaryCalls.Add(1)
			writeFixtureJSON(t, writer, `{"Items":[{"Id":"library-movies","Name":"Movies","CollectionType":"movies","Type":"CollectionFolder"},{"Id":"library-series","title":"Series","CollectionType":"tvshows"}]}`)
		case "/proxy/Users/user-1/Views":
			fallbackCalls.Add(1)
			writeFixtureJSON(t, writer, `[{"Id":"library-fallback","Name":"Fallback","Type":"CollectionFolder"}]`)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	})
	client, _ := newFixtureClient(t, handler, Config{UserID: "user-1"})
	libraries, err := client.ListLibraries(context.Background())
	if err != nil {
		t.Fatalf("ListLibraries() error = %v", err)
	}
	if libraries.Coverage.Completeness != CompletenessComplete || len(libraries.Items) != 2 || libraries.Items[1].Name != "Series" || libraries.Items[1].Type != "" {
		t.Fatalf("libraries = %#v", libraries)
	}
	if primaryCalls.Load() != 1 || fallbackCalls.Load() != 0 {
		t.Fatalf("calls = primary %d fallback %d", primaryCalls.Load(), fallbackCalls.Load())
	}

	fallbackHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/proxy/Library/MediaFolders" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if request.URL.Path != "/proxy/Users/user-1/Views" {
			t.Fatalf("fallback path = %q", request.URL.Path)
		}
		writeFixtureJSON(t, writer, `{"Items":[{"Id":"library-fallback","Name":"Fallback"}]}`)
	})
	fallbackClient, _ := newFixtureClient(t, fallbackHandler, Config{UserID: "user-1"})
	fallback, err := fallbackClient.Libraries(context.Background())
	if err != nil || len(fallback.Items) != 1 || fallback.Items[0].ID != "library-fallback" {
		t.Fatalf("fallback = %#v, err %v", fallback, err)
	}
}

func TestItemsPreserveProviderRelationshipsAndCoverage(t *testing.T) {
	var gotQuery string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/proxy/Items" || request.Method != http.MethodGet {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		gotQuery = request.URL.RawQuery
		writeFixtureJSON(t, writer, `{"Items":[{"Id":"jf-film-101","Name":"Fixture Film","Type":"Movie","MediaType":"Video","LocationType":"FileSystem","Path":"/media/film.mkv","ProviderIds":{"Tvdb":"2002","Tmdb":"550"},"MediaSources":[{"Id":"source-1","Path":"/media/film.mkv","Protocol":"File","LocationType":"FileSystem","MediaType":"Video"}],"future":{"ignored":true}}],"TotalRecordCount":1,"StartIndex":0}`)
	})
	client, _ := newFixtureClient(t, handler, Config{UserID: "user-1"})
	page, err := client.ListItems(context.Background(), ItemQuery{
		ParentID: "library-movies", IncludeItemTypes: []string{"Movie", "Series"}, Recursive: true,
		StartIndex: 0, Limit: 5, Fields: []string{"ProviderIds", "MediaSources"},
	})
	if err != nil {
		t.Fatalf("ListItems() error = %v", err)
	}
	if page.Coverage.Completeness != CompletenessComplete || len(page.Items) != 1 || page.Items[0].ID != "jf-film-101" || page.Items[0].ProviderIDs["Tmdb"] != "550" || len(page.Items[0].ProviderRelations) != 2 || page.Items[0].ProviderRelations[0].Provider != "Tmdb" || len(page.Items[0].MediaSources) != 1 {
		t.Fatalf("page = %#v", page)
	}
	if !strings.Contains(gotQuery, "ParentId=library-movies") || !strings.Contains(gotQuery, "IncludeItemTypes=Movie%2CSeries") || !strings.Contains(gotQuery, "Recursive=true") || !strings.Contains(gotQuery, "UserId=user-1") {
		t.Fatalf("query = %q", gotQuery)
	}
	page.Items[0].ProviderIDs["Tmdb"] = "changed-locally"
	if page.Items[0].ProviderRelations[1].ID == "changed-locally" {
		t.Fatal("provider relationship shared mutable map")
	}

	partialHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeFixtureJSON(t, writer, `{"Items":[{"Id":"jf-1"}],"TotalRecordCount":3,"StartIndex":0}`)
	})
	partialClient, _ := newFixtureClient(t, partialHandler, Config{})
	partial, err := partialClient.ListItems(context.Background(), ItemQuery{Limit: 1})
	if err != nil || partial.Coverage.Completeness != CompletenessPartial || len(partial.Coverage.ReasonCodes) != 1 || partial.Coverage.ReasonCodes[0] != "pagination_continues" {
		t.Fatalf("partial = %#v, err %v", partial, err)
	}
}

func TestObserveItemRejectsForeignAndMissingIdentity(t *testing.T) {
	var mode atomic.Int32
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("Ids") != "requested" {
			t.Fatalf("Ids = %q", request.URL.Query().Get("Ids"))
		}
		switch mode.Load() {
		case 0:
			writeFixtureJSON(t, writer, `[{"Id":"foreign"}]`)
		case 1:
			writeFixtureJSON(t, writer, `[]`)
		default:
			writeFixtureJSON(t, writer, `[{"Id":"requested","ProviderIds":{"Tmdb":"550"}}]`)
		}
	})
	client, _ := newFixtureClient(t, handler, Config{})
	if _, err := client.ObserveItem(context.Background(), "requested"); !IsCode(err, ErrorNotFound) {
		t.Fatalf("foreign identity error = %v", err)
	}
	mode.Store(1)
	if _, err := client.GetItem(context.Background(), "requested"); !IsCode(err, ErrorNotFound) {
		t.Fatalf("missing identity error = %v", err)
	}
	mode.Store(2)
	item, err := client.ObserveItem(context.Background(), "requested")
	if err != nil || item.ID != "requested" {
		t.Fatalf("valid item = %#v, err %v", item, err)
	}
}

func TestMalformedDuplicateTrailingAndBoundsFailClosed(t *testing.T) {
	cases := []string{
		`{"Version":"10.10.7","Version":"10.10.8"}`,
		`{"Version":"10.10.7"} {"extra":true}`,
		`{"ProductName":"Jellyfin"}`,
	}
	for _, body := range cases {
		t.Run(body, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeFixtureJSON(t, writer, body)
			})
			client, _ := newFixtureClient(t, handler, Config{})
			if _, err := client.GetSystemInfo(context.Background()); !IsCode(err, ErrorMalformed) {
				t.Fatalf("error = %v", err)
			}
		})
	}

	largeHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, `{"Version":"10.10.7","padding":"`+strings.Repeat("x", 256)+`"}`)
	})
	largeClient, _ := newFixtureClient(t, largeHandler, Config{MaxResponseBytes: 64})
	if _, err := largeClient.GetSystemInfo(context.Background()); !IsCode(err, ErrorResponseTooLarge) {
		t.Fatalf("large response error = %v", err)
	}
}

func TestRefreshAcceptanceDoesNotClaimAvailabilityAndScopesAreExplicit(t *testing.T) {
	var refreshCalls atomic.Int32
	var visible atomic.Bool
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/proxy/Library/Refresh":
			refreshCalls.Add(1)
			if got := request.Header.Get("X-Emby-Token"); got != "fixture-jellyfin-token" {
				t.Fatalf("refresh token = %q", got)
			}
			writer.WriteHeader(http.StatusAccepted)
		case request.Method == http.MethodPost && request.URL.Path == "/proxy/Items/item-1/Refresh":
			writer.WriteHeader(http.StatusNotFound)
		case request.Method == http.MethodGet && request.URL.Path == "/proxy/Items":
			if visible.Load() {
				writeFixtureJSON(t, writer, `[{"Id":"item-1"}]`)
			} else {
				writeFixtureJSON(t, writer, `[]`)
			}
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	})
	client, _ := newFixtureClient(t, handler, Config{})
	accepted, err := client.RefreshLibrary(context.Background())
	if err != nil || !accepted.Accepted || accepted.Status != http.StatusAccepted || accepted.Scope != RefreshLibraryScope || accepted.ItemID != "" || len(accepted.Evidence) != 2 {
		t.Fatalf("refresh = %#v, err %v", accepted, err)
	}
	before, err := client.ListItems(context.Background(), ItemQuery{})
	if err != nil || len(before.Items) != 0 {
		t.Fatalf("before = %#v, err %v", before, err)
	}
	visible.Store(true)
	after, err := client.ListItems(context.Background(), ItemQuery{})
	if err != nil || len(after.Items) != 1 || after.Items[0].ID != "item-1" {
		t.Fatalf("after = %#v, err %v", after, err)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("refresh calls = %d", refreshCalls.Load())
	}
	if _, err := client.RefreshItem(context.Background(), "item-1"); !IsCode(err, ErrorUnsupported) {
		t.Fatalf("unsupported item refresh error = %v", err)
	}
	if _, err := client.Refresh(context.Background(), RefreshRequest{Scope: RefreshLibraryScope, ItemID: "unexpected"}); !IsCode(err, ErrorInvalidInput) {
		t.Fatalf("library item scope error = %v", err)
	}
}

func TestTimeoutAndConfigurationValidation(t *testing.T) {
	if _, err := New(Config{Endpoint: "https://user:password@example.invalid", Token: "token"}); err == nil {
		t.Fatal("credential-bearing endpoint accepted")
	}
	if _, err := New(Config{Endpoint: "https://jellyfin.invalid", Token: " token"}); err == nil {
		t.Fatal("whitespace token accepted")
	}
	if _, err := New(Config{Endpoint: "https://jellyfin.invalid", Token: "token", MaxPageSize: 2, MaxItems: 1}); err == nil {
		t.Fatal("page bound larger than item bound accepted")
	}
	blocked := make(chan struct{})
	handler := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		select {
		case <-blocked:
			return nil, errors.New("unreachable")
		case <-request.Context().Done():
			return nil, request.Context().Err()
		}
	})
	client, err := New(Config{Endpoint: "https://jellyfin.invalid", Token: "token", HTTPClient: &http.Client{Transport: handler}, RequestTimeout: 5 * time.Millisecond})
	if err != nil {
		t.Fatalf("New timeout client: %v", err)
	}
	_, err = client.GetSystemInfo(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout error = %v", err)
	}
	close(blocked)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
