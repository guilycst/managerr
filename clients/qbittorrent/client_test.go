package qbittorrent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testUsername = "synthetic-user"
	testPassword = "synthetic-password"
	testSID      = "synthetic-session"
	testHash     = "0123456789abcdef0123456789abcdef01234567"
)

func TestReadSurfaceAndNormalization(t *testing.T) {
	torrentInfo := fixture(t, "torrent-info.json")
	properties := fixture(t, "torrent-properties.json")
	files := fixture(t, "torrent-files.json")
	categories := fixture(t, "categories.json")
	tags := fixture(t, "tags.json")

	var server *httptest.Server
	var loginCount atomic.Int32
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiLogin {
			if r.Method != http.MethodPost {
				t.Errorf("login method = %s, want POST", r.Method)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse login form: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if got := r.PostForm.Get("username"); got != testUsername {
				t.Errorf("login username = %q, want %q", got, testUsername)
			}
			if got := r.PostForm.Get("password"); got != testPassword {
				t.Errorf("login password = %q, want %q", got, testPassword)
			}
			loginCount.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
			w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
			_, _ = io.WriteString(w, "Ok.")
			return
		}
		if r.Method != http.MethodGet {
			t.Errorf("read route %s method = %s, want GET", r.URL.Path, r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		cookie, err := r.Cookie("SID")
		if err != nil || cookie.Value != testSID {
			t.Errorf("SID cookie = %v, want %q", cookie, testSID)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if got, want := r.Header.Get("Origin"), server.URL; got != want {
			t.Errorf("Origin = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Referer"), server.URL+"/"; got != want {
			t.Errorf("Referer = %q, want %q", got, want)
		}
		switch r.URL.Path {
		case apiAppVersion:
			_, _ = io.WriteString(w, "v5.0.0\n")
		case apiWebAPI:
			_, _ = io.WriteString(w, "2.11.3\n")
		case apiTorrentInfo:
			query := r.URL.Query()
			if got, want := query.Get("filter"), "completed"; got != want {
				t.Errorf("filter = %q, want %q", got, want)
			}
			if got, want := query.Get("category"), "synthetic category"; got != want {
				t.Errorf("category = %q, want %q", got, want)
			}
			if got, want := query.Get("tag"), "verified"; got != want {
				t.Errorf("tag = %q, want %q", got, want)
			}
			if got, want := query.Get("sort"), "ratio"; got != want {
				t.Errorf("sort = %q, want %q", got, want)
			}
			if got, want := query.Get("reverse"), "true"; got != want {
				t.Errorf("reverse = %q, want %q", got, want)
			}
			if got, want := query.Get("limit"), "5"; got != want {
				t.Errorf("limit = %q, want %q", got, want)
			}
			if got, want := query.Get("offset"), "-2"; got != want {
				t.Errorf("offset = %q, want %q", got, want)
			}
			if got, want := query.Get("hashes"), "hash-one|hash-two"; got != want {
				t.Errorf("hashes = %q, want %q", got, want)
			}
			_, _ = w.Write(torrentInfo)
		case apiProperties:
			if got, want := r.URL.Query().Get("hash"), testHash; got != want {
				t.Errorf("property hash = %q, want %q", got, want)
			}
			_, _ = w.Write(properties)
		case apiFiles:
			if got, want := r.URL.Query().Get("hash"), testHash; got != want {
				t.Errorf("file hash = %q, want %q", got, want)
			}
			_, _ = w.Write(files)
		case apiCategories:
			_, _ = w.Write(categories)
		case apiTags:
			_, _ = w.Write(tags)
		default:
			t.Errorf("unexpected GET path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	gotVersions, err := client.Versions(context.Background())
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if want := (Versions{Application: "v5.0.0", WebAPI: "2.11.3"}); gotVersions != want {
		t.Fatalf("Versions = %#v, want %#v", gotVersions, want)
	}

	gotTorrents, err := client.ListTorrents(context.Background(), TorrentListOptions{
		Filter:   "completed",
		Category: "synthetic category",
		Tag:      "verified",
		Sort:     "ratio",
		Reverse:  true,
		Limit:    5,
		Offset:   -2,
		Hashes:   []string{"hash-one", "hash-two"},
	})
	if err != nil {
		t.Fatalf("ListTorrents: %v", err)
	}
	if len(gotTorrents) != 1 || gotTorrents[0].Hash != testHash {
		t.Fatalf("ListTorrents = %#v, want one synthetic torrent", gotTorrents)
	}
	if got := gotTorrents[0]; got.Name != "synthetic-example" || got.State != "pausedUP" || got.Progress != 1 || got.ContentPath != "/synthetic/downloads/Example" || got.MagnetURI == "" || got.IsPrivate {
		t.Fatalf("normalized torrent = %#v", got)
	}

	gotProperties, err := client.GetTorrentProperties(context.Background(), testHash)
	if err != nil {
		t.Fatalf("GetTorrentProperties: %v", err)
	}
	if gotProperties.SavePath != "/synthetic/downloads/" || gotProperties.CompletionDate != 1700000100 || gotProperties.TotalSize != 123456789 {
		t.Fatalf("normalized properties = %#v", gotProperties)
	}

	gotFiles, err := client.GetTorrentFiles(context.Background(), testHash)
	if err != nil {
		t.Fatalf("GetTorrentFiles: %v", err)
	}
	if len(gotFiles) != 2 || gotFiles[0].Name != "synthetic-example/video.mkv" || gotFiles[0].Index != 0 || len(gotFiles[0].PieceRange) != 2 {
		t.Fatalf("normalized files = %#v", gotFiles)
	}
	gotFiles[0].PieceRange[0] = 99
	gotFilesAgain, err := client.GetTorrentFiles(context.Background(), testHash)
	if err != nil {
		t.Fatalf("GetTorrentFiles second read: %v", err)
	}
	if gotFilesAgain[0].PieceRange[0] != 0 {
		t.Fatalf("file piece range was not copied: %#v", gotFilesAgain[0].PieceRange)
	}

	gotCategories, err := client.Categories(context.Background())
	if err != nil {
		t.Fatalf("Categories: %v", err)
	}
	if gotCategories["synthetic"].SavePath != "/synthetic/downloads/" {
		t.Fatalf("normalized categories = %#v", gotCategories)
	}
	gotTags, err := client.Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if fmt.Sprint(gotTags) != "[synthetic verified]" {
		t.Fatalf("normalized tags = %#v", gotTags)
	}
	if got := loginCount.Load(); got != 1 {
		t.Fatalf("login count = %d, want one session login", got)
	}
}

func TestLoginFailuresAreTypedAndSanitized(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "invalid body with HTTP 200", status: http.StatusOK, body: "Fails. password=" + testPassword},
		{name: "forbidden", status: http.StatusForbidden, body: "banned password=" + testPassword},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != apiLogin {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = client.ApplicationVersion(context.Background())
			if err == nil || !IsCode(err, ErrorUnauthorized) {
				t.Fatalf("ApplicationVersion error = %v, want unauthorized", err)
			}
			if strings.Contains(err.Error(), testPassword) || strings.Contains(err.Error(), test.name) {
				t.Fatalf("error leaked sensitive or upstream text: %v", err)
			}
		})
	}
}

func TestUnknownFieldsAndMalformedResponsesAreRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case apiLogin:
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
			_, _ = io.WriteString(w, "Ok.")
		case apiTorrentInfo:
			_, _ = io.WriteString(w, `[{"added_on":0,"amount_left":0,"auto_tmm":false,"availability":0,"category":"","completed":0,"completion_on":0,"content_path":"","dl_limit":0,"dlspeed":0,"downloaded":0,"downloaded_session":0,"eta":0,"f_l_piece_prio":false,"force_start":false,"hash":"synthetic","isPrivate":false,"last_activity":0,"magnet_uri":"","max_ratio":0,"max_seeding_time":0,"name":"synthetic","num_complete":0,"num_incomplete":0,"num_leechs":0,"num_seeds":0,"priority":0,"progress":0,"ratio":0,"ratio_limit":0,"reannounce":0,"save_path":"","seeding_time":0,"seeding_time_limit":0,"seen_complete":0,"seq_dl":false,"size":0,"state":"unknown","super_seeding":false,"tags":"","time_active":0,"total_size":0,"tracker":"","up_limit":0,"uploaded":0,"uploaded_session":0,"upspeed":0,"unexpected":"do-not-accept"}]`)
		case apiProperties:
			_, _ = io.WriteString(w, `{"save_path":"/synthetic","unexpected":"do-not-accept"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.ListTorrents(context.Background(), TorrentListOptions{})
	if err == nil || !IsCode(err, ErrorUnknown) || strings.Contains(err.Error(), "do-not-accept") {
		t.Fatalf("unknown inventory field error = %v", err)
	}
	_, err = client.GetTorrentProperties(context.Background(), testHash)
	if err == nil || !IsCode(err, ErrorUnknown) {
		t.Fatalf("unknown properties field error = %v", err)
	}
}

func TestResponseBoundsAndHTTPErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		handle func(http.ResponseWriter, *http.Request)
		check  func(*testing.T, error)
	}{
		{
			name: "bounded body",
			handle: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiLogin {
					_, _ = io.WriteString(w, "Ok.")
					return
				}
				_, _ = io.WriteString(w, strings.Repeat("x", 128))
			},
			check: func(t *testing.T, err error) {
				if err == nil || !IsCode(err, ErrorUnknown) {
					t.Fatalf("error = %v, want bounded unknown", err)
				}
			},
		},
		{
			name: "rate limited",
			handle: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiLogin {
					_, _ = io.WriteString(w, "Ok.")
					return
				}
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, "secret upstream text")
			},
			check: func(t *testing.T, err error) {
				var upstream UpstreamError
				if !errors.As(err, &upstream) || upstream.Code != ErrorRateLimited || !upstream.Retryable || strings.Contains(err.Error(), "secret upstream text") {
					t.Fatalf("error = %#v, want sanitized retryable rate limit", err)
				}
			},
		},
		{
			name: "malformed JSON",
			handle: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiLogin {
					_, _ = io.WriteString(w, "Ok.")
					return
				}
				_, _ = io.WriteString(w, "[{not-json}]")
			},
			check: func(t *testing.T, err error) {
				if err == nil || !IsCode(err, ErrorUnknown) {
					t.Fatalf("error = %v, want malformed unknown", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(test.handle))
			defer server.Close()
			client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword, MaxResponseBytes: 64})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = client.ListTorrents(context.Background(), TorrentListOptions{})
			test.check(t, err)
		})
	}
}

func TestContextCancellationAndTransportTimeout(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == apiLogin {
			_, _ = io.WriteString(w, "Ok.")
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.ApplicationVersion(canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v, want context.Canceled", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("canceled request count = %d, want zero", got)
	}

	deadline, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err = client.ApplicationVersion(deadline)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v, want context deadline", err)
	}

	timeoutServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiLogin {
			_, _ = io.WriteString(w, "Ok.")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}))
	defer timeoutServer.Close()
	timeoutClient, err := New(Config{
		Endpoint:   timeoutServer.URL,
		Username:   testUsername,
		Password:   testPassword,
		HTTPClient: &http.Client{Timeout: 20 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("New timeout client: %v", err)
	}
	_, err = timeoutClient.ApplicationVersion(context.Background())
	if !IsCode(err, ErrorUnavailable) {
		t.Fatalf("transport timeout error = %v, want typed unavailable", err)
	}
}

func TestRedirectIsNotFollowedAndOptionsAreValidated(t *testing.T) {
	var escaped atomic.Int32
	escapedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped.Add(1)
	}))
	defer escapedServer.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiLogin {
			http.Redirect(w, r, escapedServer.URL+"/capture", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.ApplicationVersion(context.Background())
	if err == nil || !IsCode(err, ErrorUnknown) {
		t.Fatalf("redirect error = %v, want unknown", err)
	}
	if got := escaped.Load(); got != 0 {
		t.Fatalf("redirect escaped to another origin %d times", got)
	}

	for _, options := range []TorrentListOptions{
		{Limit: -1},
		{Offset: defaultMaxItems + 1},
		{Category: strings.Repeat("x", maxQueryTextBytes+1)},
		{Hashes: []string{""}},
	} {
		if _, err := client.ListTorrents(context.Background(), options); err == nil || !IsCode(err, ErrorInvalidInput) {
			t.Fatalf("options %#v error = %v, want invalid input", options, err)
		}
	}
}

func TestEndpointValidation(t *testing.T) {
	for _, endpoint := range []string{
		"",
		" /synthetic ",
		"/relative",
		"ftp://synthetic.invalid",
		"https://user:password@synthetic.invalid",
		"https://synthetic.invalid/path?secret=not-allowed",
	} {
		if _, err := New(Config{Endpoint: endpoint}); err == nil {
			t.Errorf("New(%q) succeeded, want endpoint validation error", endpoint)
		}
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %q: %v", name, err)
	}
	return data
}
