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

func writeFixtureBytes(t *testing.T, writer http.ResponseWriter, body []byte) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(body)
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
		case "/proxy/UserViews":
			fallbackCalls.Add(1)
			if request.URL.Query().Get("userId") != "user-1" {
				t.Fatalf("fallback userId = %q", request.URL.Query().Get("userId"))
			}
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
		if request.URL.Path != "/proxy/UserViews" || request.URL.Query().Get("userId") != "user-1" {
			t.Fatalf("fallback path = %q", request.URL.Path)
		}
		writeFixtureJSON(t, writer, `{"Items":[{"Id":"library-fallback","Name":"Fallback"}]}`)
	})
	fallbackClient, _ := newFixtureClient(t, fallbackHandler, Config{UserID: "user-1"})
	fallback, err := fallbackClient.Libraries(context.Background())
	if err != nil || len(fallback.Items) != 1 || fallback.Items[0].ID != "library-fallback" {
		t.Fatalf("fallback = %#v, err %v", fallback, err)
	}

	noUserHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/proxy/Library/MediaFolders":
			writer.WriteHeader(http.StatusNotFound)
		case "/proxy/UserViews":
			if request.URL.Query().Get("userId") != "" {
				t.Fatalf("unexpected implicit userId = %q", request.URL.Query().Get("userId"))
			}
			writeFixtureJSON(t, writer, `[{"Id":"library-context","Name":"Token Context"}]`)
		case "/proxy/Users/Me/Views":
			t.Fatalf("non-native current-user alias requested")
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	})
	noUserClient, _ := newFixtureClient(t, noUserHandler, Config{})
	noUser, err := noUserClient.ListLibraries(context.Background())
	if err != nil || len(noUser.Items) != 1 || noUser.Items[0].ID != "library-context" {
		t.Fatalf("no-user fallback = %#v, err %v", noUser, err)
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
	if _, err := client.ListItems(context.Background(), ItemQuery{ItemIDs: []string{"requested"}, Limit: 1}); !IsCode(err, ErrorMalformed) {
		t.Fatalf("foreign scoped response error = %v", err)
	}
	if _, err := client.ObserveItem(context.Background(), "requested"); !IsCode(err, ErrorMalformed) {
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

func TestPaginationCoverageRetainsUncertainty(t *testing.T) {
	t.Run("impossible total", func(t *testing.T) {
		handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writeFixtureJSON(t, writer, `{"Items":[{"Id":"one"}],"TotalRecordCount":0,"StartIndex":0}`)
		})
		client, _ := newFixtureClient(t, handler, Config{})
		if _, err := client.ListItems(context.Background(), ItemQuery{Limit: 1}); !IsCode(err, ErrorMalformed) {
			t.Fatalf("impossible total error = %v", err)
		}
	})

	t.Run("missing total", func(t *testing.T) {
		handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writeFixtureJSON(t, writer, `{"Items":[{"Id":"one"}],"StartIndex":0}`)
		})
		client, _ := newFixtureClient(t, handler, Config{})
		page, err := client.ListItems(context.Background(), ItemQuery{Limit: 1})
		if err != nil || page.Coverage.Completeness != CompletenessUnknown || len(page.Items) != 1 || len(page.Coverage.ReasonCodes) != 1 || page.Coverage.ReasonCodes[0] != "pagination_total_missing" {
			t.Fatalf("missing total page = %#v, err %v", page, err)
		}
	})

	t.Run("tail page", func(t *testing.T) {
		handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writeFixtureJSON(t, writer, `{"Items":[{"Id":"tail"}],"TotalRecordCount":2,"StartIndex":1}`)
		})
		client, _ := newFixtureClient(t, handler, Config{})
		page, err := client.ListItems(context.Background(), ItemQuery{StartIndex: 1, Limit: 2})
		if err != nil || page.Coverage.Completeness != CompletenessComplete || len(page.Items) != 1 {
			t.Fatalf("tail page = %#v, err %v", page, err)
		}
	})

	t.Run("short partial traversal", func(t *testing.T) {
		var calls atomic.Int32
		handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			switch request.URL.Query().Get("StartIndex") {
			case "":
				writeFixtureJSON(t, writer, `{"Items":[{"Id":"one"}],"TotalRecordCount":3,"StartIndex":0}`)
			case "1":
				writeFixtureJSON(t, writer, `{"Items":[{"Id":"two"}],"TotalRecordCount":3,"StartIndex":1}`)
			case "2":
				writeFixtureJSON(t, writer, `{"Items":[{"Id":"three"}],"TotalRecordCount":3,"StartIndex":2}`)
			default:
				t.Fatalf("unexpected start index = %q", request.URL.Query().Get("StartIndex"))
			}
		})
		client, _ := newFixtureClient(t, handler, Config{})
		page, err := client.ListAllItems(context.Background(), ItemQuery{Limit: 2})
		if err != nil || page.Coverage.Completeness == CompletenessComplete || len(page.Items) != 3 || calls.Load() != 3 {
			t.Fatalf("short traversal = %#v, calls %d, err %v", page, calls.Load(), err)
		}
	})

	t.Run("page limit", func(t *testing.T) {
		var calls atomic.Int32
		handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			writeFixtureJSON(t, writer, `{"Items":[{"Id":"one"}],"TotalRecordCount":3,"StartIndex":0}`)
		})
		client, _ := newFixtureClient(t, handler, Config{MaxPages: 1})
		page, err := client.ListAllItems(context.Background(), ItemQuery{Limit: 1})
		if err != nil || page.Coverage.Completeness != CompletenessPartial || !containsString(page.Coverage.ReasonCodes, "pagination_limit") || len(page.Items) != 1 || calls.Load() != 1 {
			t.Fatalf("page limit = %#v, calls %d, err %v", page, calls.Load(), err)
		}
	})

	t.Run("interrupted traversal", func(t *testing.T) {
		var calls atomic.Int32
		handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if calls.Add(1) == 1 {
				writeFixtureJSON(t, writer, `{"Items":[{"Id":"one"}],"TotalRecordCount":3,"StartIndex":0}`)
				return
			}
			writer.WriteHeader(http.StatusServiceUnavailable)
		})
		client, _ := newFixtureClient(t, handler, Config{})
		page, err := client.ListAllItems(context.Background(), ItemQuery{Limit: 2})
		if err == nil || page.Coverage.Completeness != CompletenessUnknown || len(page.Items) != 1 || calls.Load() != 2 {
			t.Fatalf("interrupted traversal = %#v, calls %d, err %v", page, calls.Load(), err)
		}
	})
}

func TestMissingPaginationMetadataCannotProveCompletenessOrAbsence(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
	}{
		{name: "missing start", body: `{"Items":[{"Id":"one"}],"TotalRecordCount":1}`},
		{name: "null start", body: `{"Items":[{"Id":"one"}],"TotalRecordCount":1,"StartIndex":null}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeFixtureJSON(t, writer, testCase.body)
			})
			client, _ := newFixtureClient(t, handler, Config{})
			page, err := client.ListItems(context.Background(), ItemQuery{Limit: 1})
			if err != nil || page.Coverage.Completeness != CompletenessUnknown || len(page.Items) != 1 || !containsString(page.Coverage.ReasonCodes, "pagination_start_missing") {
				t.Fatalf("missing start page = %#v, err %v", page, err)
			}
		})
	}
	offsetHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("StartIndex") != "1" {
			t.Fatalf("offset StartIndex = %q", request.URL.Query().Get("StartIndex"))
		}
		writeFixtureJSON(t, writer, `{"Items":[{"Id":"one"}],"TotalRecordCount":2}`)
	})
	offsetClient, _ := newFixtureClient(t, offsetHandler, Config{})
	offsetPage, err := offsetClient.ListItems(context.Background(), ItemQuery{StartIndex: 1, Limit: 1})
	if err != nil || offsetPage.Coverage.Completeness != CompletenessUnknown || len(offsetPage.Items) != 1 || !containsString(offsetPage.Coverage.ReasonCodes, "pagination_start_missing") {
		t.Fatalf("missing offset page = %#v, err %v", offsetPage, err)
	}

	for _, testCase := range []struct {
		name string
		body string
	}{
		{name: "missing total", body: `{"Items":[],"StartIndex":0}`},
		{name: "null total", body: `{"Items":[],"TotalRecordCount":null,"StartIndex":0}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writeFixtureJSON(t, writer, testCase.body)
			})
			client, _ := newFixtureClient(t, handler, Config{})
			page, err := client.Items(context.Background(), ItemQuery{ItemIDs: []string{"wanted"}, Limit: 1})
			if err != nil || page.Coverage.Completeness != CompletenessUnknown || len(page.Items) != 0 || !containsString(page.Coverage.ReasonCodes, "pagination_total_missing") {
				t.Fatalf("incomplete exact empty = %#v, err %v", page, err)
			}
			if _, err := client.ObserveItem(context.Background(), "wanted"); !IsCode(err, ErrorUnknown) {
				t.Fatalf("ObserveItem incomplete empty error = %v", err)
			}
			if _, err := client.GetItem(context.Background(), "wanted"); !IsCode(err, ErrorUnknown) {
				t.Fatalf("GetItem incomplete empty error = %v", err)
			}
			traversed, err := client.ListAllItems(context.Background(), ItemQuery{ItemIDs: []string{"wanted"}, Limit: 1})
			if err != nil || traversed.Coverage.Completeness != CompletenessUnknown {
				t.Fatalf("traversed incomplete empty = %#v, err %v", traversed, err)
			}
		})
	}

	completeHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeFixtureJSON(t, writer, `{"Items":[],"TotalRecordCount":0,"StartIndex":0}`)
	})
	completeClient, _ := newFixtureClient(t, completeHandler, Config{})
	page, err := completeClient.Items(context.Background(), ItemQuery{ItemIDs: []string{"wanted"}, Limit: 1})
	if err != nil || page.Coverage.Completeness != CompletenessComplete || len(page.Items) != 0 {
		t.Fatalf("complete exact empty = %#v, err %v", page, err)
	}
	if _, err := completeClient.ObserveItem(context.Background(), "wanted"); !IsCode(err, ErrorNotFound) {
		t.Fatalf("complete exact empty error = %v", err)
	}
	if _, err := completeClient.GetItem(context.Background(), "wanted"); !IsCode(err, ErrorNotFound) {
		t.Fatalf("complete exact empty alias error = %v", err)
	}
}

func TestRequestedScopeRejectsForeignAndMixedEvidence(t *testing.T) {
	foreign := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("Ids") != "wanted" {
			t.Fatalf("requested IDs = %q", request.URL.Query().Get("Ids"))
		}
		writeFixtureJSON(t, writer, `[{"Id":"foreign"}]`)
	})
	client, _ := newFixtureClient(t, foreign, Config{})
	if _, err := client.Items(context.Background(), ItemQuery{ItemIDs: []string{"wanted"}, Limit: 1}); !IsCode(err, ErrorMalformed) {
		t.Fatalf("foreign alias error = %v", err)
	}

	mixed := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeFixtureJSON(t, writer, `[{"Id":"wanted"},{"Id":"foreign"}]`)
	})
	mixedClient, _ := newFixtureClient(t, mixed, Config{})
	if _, err := mixedClient.ListItems(context.Background(), ItemQuery{ItemIDs: []string{"wanted", "other"}, Limit: 2}); !IsCode(err, ErrorMalformed) {
		t.Fatalf("mixed scope error = %v", err)
	}

	var calls atomic.Int32
	traversal := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Query().Get("StartIndex") == "" {
			writeFixtureJSON(t, writer, `{"Items":[{"Id":"wanted"}],"TotalRecordCount":2,"StartIndex":0}`)
			return
		}
		writeFixtureJSON(t, writer, `{"Items":[{"Id":"foreign"}],"TotalRecordCount":2,"StartIndex":1}`)
	})
	traversalClient, _ := newFixtureClient(t, traversal, Config{})
	if _, err := traversalClient.ListAllItems(context.Background(), ItemQuery{ItemIDs: []string{"wanted", "other"}, Limit: 1}); !IsCode(err, ErrorMalformed) || calls.Load() != 2 {
		t.Fatalf("traversal scope error = %v, calls %d", err, calls.Load())
	}

	empty := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeFixtureJSON(t, writer, `[]`)
	})
	emptyClient, _ := newFixtureClient(t, empty, Config{})
	page, err := emptyClient.Items(context.Background(), ItemQuery{ItemIDs: []string{"wanted"}, Limit: 1})
	if err != nil || page.Coverage.Completeness != CompletenessComplete || len(page.Items) != 0 {
		t.Fatalf("valid scoped empty = %#v, err %v", page, err)
	}
	if _, err := emptyClient.ObserveItem(context.Background(), "wanted"); !IsCode(err, ErrorNotFound) {
		t.Fatalf("valid scoped absence error = %v", err)
	}
}

func TestInvalidUTF8RejectedAndUnicodePreserved(t *testing.T) {
	cases := []struct {
		name string
		path string
		body []byte
		call func(*Client) error
	}{
		{
			name: "item identity",
			path: "/proxy/Items",
			body: []byte(`{"Items":[{"Id":"` + string([]byte{0xff}) + `"}],"TotalRecordCount":1,"StartIndex":0}`),
			call: func(client *Client) error {
				_, err := client.ListItems(context.Background(), ItemQuery{Limit: 1})
				return err
			},
		},
		{
			name: "library identity",
			path: "/proxy/Library/MediaFolders",
			body: []byte(`[ {"Id":"` + string([]byte{0xff}) + `","Name":"Library"} ]`),
			call: func(client *Client) error { _, err := client.ListLibraries(context.Background()); return err },
		},
		{
			name: "provider value",
			path: "/proxy/Items",
			body: []byte(`{"Items":[{"Id":"item","ProviderIds":{"Tmdb":"` + string([]byte{0xff}) + `"}}],"TotalRecordCount":1,"StartIndex":0}`),
			call: func(client *Client) error {
				_, err := client.ListItems(context.Background(), ItemQuery{Limit: 1})
				return err
			},
		},
		{
			name: "unknown field",
			path: "/proxy/System/Info/Public",
			body: []byte(`{"Version":"10.10.7","future":"` + string([]byte{0xff}) + `"}`),
			call: func(client *Client) error { _, err := client.GetSystemInfo(context.Background()); return err },
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != testCase.path {
					t.Fatalf("path = %q", request.URL.Path)
				}
				writeFixtureBytes(t, writer, testCase.body)
			})
			client, _ := newFixtureClient(t, handler, Config{})
			if err := testCase.call(client); !IsCode(err, ErrorMalformed) {
				t.Fatalf("invalid UTF-8 error = %v", err)
			}
		})
	}

	unicodeHandler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeFixtureJSON(t, writer, `{"Items":[{"Id":"アイテム","Name":"影片","ProviderIds":{"Tmdb":"五五〇"}}],"TotalRecordCount":1,"StartIndex":0}`)
	})
	unicodeClient, _ := newFixtureClient(t, unicodeHandler, Config{})
	page, err := unicodeClient.ListItems(context.Background(), ItemQuery{Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "アイテム" || page.Items[0].Name != "影片" || page.Items[0].ProviderIDs["Tmdb"] != "五五〇" {
		t.Fatalf("valid Unicode page = %#v, err %v", page, err)
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
