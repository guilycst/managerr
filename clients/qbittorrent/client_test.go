package qbittorrent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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
			_, _ = io.WriteString(w, "v5.0.0")
		case apiWebAPI:
			_, _ = io.WriteString(w, "2.11.3")
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
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
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
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
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
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
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
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
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
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
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
		{Category: strings.Repeat("x", maxCategoryChars+1)},
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

func TestLoginRequiresUsableSIDCookie(t *testing.T) {
	for _, test := range []struct {
		name   string
		cookie *http.Cookie
		wantOK bool
	}{
		{name: "missing", wantOK: false},
		{name: "empty", cookie: &http.Cookie{Name: "SID", Value: "", Path: "/"}, wantOK: false},
		{name: "wrong name", cookie: &http.Cookie{Name: "SESSION", Value: testSID, Path: "/"}, wantOK: false},
		{name: "wrong path", cookie: &http.Cookie{Name: "SID", Value: testSID, Path: "/unrelated"}, wantOK: false},
		{name: "app-only path", cookie: &http.Cookie{Name: "SID", Value: testSID, Path: apiAppVersion}, wantOK: false},
		{name: "secure cookie over HTTP", cookie: &http.Cookie{Name: "SID", Value: testSID, Path: "/", Secure: true}, wantOK: false},
		{name: "wrong domain", cookie: &http.Cookie{Name: "SID", Value: testSID, Path: "/", Domain: "other.invalid"}, wantOK: false},
		{name: "usable API path", cookie: &http.Cookie{Name: "SID", Value: testSID, Path: "/api/v2/"}, wantOK: true},
		{name: "usable", cookie: &http.Cookie{Name: "SID", Value: testSID, Path: "/"}, wantOK: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != apiLogin || r.Method != http.MethodPost {
					t.Errorf("login request = %s %s, want POST %s", r.Method, r.URL.Path, apiLogin)
				}
				if test.cookie != nil {
					http.SetCookie(w, test.cookie)
				}
				_, _ = io.WriteString(w, "Ok.")
			}))
			defer server.Close()

			client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			err = client.Login(context.Background())
			if test.wantOK {
				if err != nil {
					t.Fatalf("Login: %v", err)
				}
				if !client.authenticated {
					t.Fatal("authenticated = false after usable SID")
				}
				return
			}
			if err == nil || !IsCode(err, ErrorUnauthorized) {
				t.Fatalf("Login error = %v, want unauthorized", err)
			}
			if client.authenticated {
				t.Fatal("authenticated = true without usable SID")
			}
		})
	}
}

func TestConcurrentLoginIsIdempotentAndExpiredSessionReauthenticates(t *testing.T) {
	var loginCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiLogin {
			loginCount.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
			_, _ = io.WriteString(w, "Ok.")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const callers = 16
	var wg sync.WaitGroup
	errorsCh := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorsCh <- client.Login(context.Background())
		}()
	}
	wg.Wait()
	close(errorsCh)
	for loginErr := range errorsCh {
		if loginErr != nil {
			t.Fatalf("concurrent Login: %v", loginErr)
		}
	}
	if got := loginCount.Load(); got != 1 {
		t.Fatalf("concurrent login count = %d, want one", got)
	}

	var reauthCount atomic.Int32
	var readCount atomic.Int32
	reauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case apiLogin:
			login := reauthCount.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: fmt.Sprintf("session-%d", login), Path: "/"})
			_, _ = io.WriteString(w, "Ok.")
		case apiAppVersion:
			if readCount.Add(1) == 1 {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, strings.Repeat("expired session detail", 8))
				return
			}
			_, _ = io.WriteString(w, "v5.0.0")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer reauthServer.Close()
	reauthClient, err := New(Config{Endpoint: reauthServer.URL, Username: testUsername, Password: testPassword, MaxResponseBytes: 64})
	if err != nil {
		t.Fatalf("New reauth client: %v", err)
	}
	version, err := reauthClient.ApplicationVersion(context.Background())
	if err != nil || version != "v5.0.0" {
		t.Fatalf("ApplicationVersion = %q, error %v; want one reauthentication and success", version, err)
	}
	if got := reauthCount.Load(); got != 2 {
		t.Fatalf("reauth login count = %d, want exactly two", got)
	}
}

func TestHashValidationAndAggregateBound(t *testing.T) {
	valid40 := strings.Repeat("a", 40)
	valid64 := strings.Repeat("b", 64)
	for _, hashes := range [][]string{{valid40}, {valid64}, {valid40, valid64}} {
		query, err := listQuery(TorrentListOptions{Hashes: hashes})
		if err != nil {
			t.Fatalf("listQuery hashes %v: %v", hashes, err)
		}
		if got := query.Get("hashes"); got != strings.Join(hashes, "|") {
			t.Fatalf("hashes query = %q, want %q", got, strings.Join(hashes, "|"))
		}
	}
	for _, hash := range []string{
		"",
		valid40 + "|tail",
		"head|" + valid40,
		valid40 + " ",
		" " + valid40,
		"\t" + valid40,
		"\x00" + valid40,
		string([]byte{0xff}),
	} {
		if _, err := listQuery(TorrentListOptions{Hashes: []string{hash}}); err == nil || !IsCode(err, ErrorInvalidInput) {
			t.Fatalf("hash %q accepted with error %v, want invalid input", hash, err)
		}
	}
	if _, err := listQuery(TorrentListOptions{Hashes: []string{valid40, valid40}}); err == nil || !IsCode(err, ErrorInvalidInput) {
		t.Fatalf("duplicate hash error = %v, want invalid input", err)
	}
	upper40 := strings.ToUpper(valid40)
	if _, err := listQuery(TorrentListOptions{Hashes: []string{valid40, upper40}}); err == nil || !IsCode(err, ErrorInvalidInput) {
		t.Fatalf("case-equivalent v1 hash error = %v, want invalid input", err)
	}
	upper64 := strings.ToUpper(valid64)
	if _, err := listQuery(TorrentListOptions{Hashes: []string{valid64, upper64}}); err == nil || !IsCode(err, ErrorInvalidInput) {
		t.Fatalf("case-equivalent v2 hash error = %v, want invalid input", err)
	}
	if query, err := listQuery(TorrentListOptions{Hashes: []string{upper40}}); err != nil || query.Get("hashes") != upper40 {
		t.Fatalf("accepted hash spelling = %q, error %v; want original spelling %q", query.Get("hashes"), err, upper40)
	}

	exact := make([]string, 32)
	for i := 0; i < 31; i++ {
		prefix := fmt.Sprintf("%02d", i)
		exact[i] = prefix + strings.Repeat("x", 128-len(prefix))
	}
	exact[31] = "last" + strings.Repeat("y", 93)
	query, err := listQuery(TorrentListOptions{Hashes: exact})
	if err != nil {
		t.Fatalf("exact aggregate listQuery: %v", err)
	}
	if got := len(query.Get("hashes")); got != maxHashAggregateBytes {
		t.Fatalf("exact aggregate bytes = %d, want %d", got, maxHashAggregateBytes)
	}
	exact[31] = "last" + strings.Repeat("y", 94)
	if _, err := listQuery(TorrentListOptions{Hashes: exact}); err == nil || !IsCode(err, ErrorInvalidInput) {
		t.Fatalf("one-over aggregate error = %v, want invalid input", err)
	}
}

func TestInputBoundsMatchOpenAPI(t *testing.T) {
	fields := []struct {
		name  string
		max   int
		build func(string) TorrentListOptions
	}{
		{name: "filter", max: maxFilterChars, build: func(value string) TorrentListOptions { return TorrentListOptions{Filter: value} }},
		{name: "category", max: maxCategoryChars, build: func(value string) TorrentListOptions { return TorrentListOptions{Category: value} }},
		{name: "tag", max: maxTagChars, build: func(value string) TorrentListOptions { return TorrentListOptions{Tag: value} }},
		{name: "sort", max: maxSortChars, build: func(value string) TorrentListOptions { return TorrentListOptions{Sort: value} }},
	}
	for _, field := range fields {
		if _, err := listQuery(field.build(strings.Repeat("x", field.max))); err != nil {
			t.Errorf("%s exact bound error = %v", field.name, err)
		}
		if _, err := listQuery(field.build(strings.Repeat("x", field.max+1))); err == nil || !IsCode(err, ErrorInvalidInput) {
			t.Errorf("%s one-over error = %v, want invalid input", field.name, err)
		}
	}

	base := Config{Endpoint: "http://synthetic.invalid", Username: testUsername, Password: testPassword}
	for _, test := range []struct {
		name   string
		config Config
	}{
		{name: "empty username", config: Config{Endpoint: base.Endpoint, Password: base.Password}},
		{name: "empty password", config: Config{Endpoint: base.Endpoint, Username: base.Username}},
		{name: "username one over", config: Config{Endpoint: base.Endpoint, Username: strings.Repeat("u", maxUsernameChars+1), Password: base.Password}},
		{name: "password one over", config: Config{Endpoint: base.Endpoint, Username: base.Username, Password: strings.Repeat("p", maxPasswordChars+1)}},
	} {
		if _, err := New(test.config); err == nil {
			t.Errorf("New %s succeeded, want credential validation error", test.name)
		}
	}
	if _, err := New(Config{Endpoint: base.Endpoint, Username: strings.Repeat("u", maxUsernameChars), Password: strings.Repeat("p", maxPasswordChars)}); err != nil {
		t.Fatalf("New exact credential bounds: %v", err)
	}
}

func TestVersionValidation(t *testing.T) {
	for _, test := range []struct {
		name     string
		endpoint string
		body     string
		want     string
	}{
		{name: "application stable", endpoint: apiAppVersion, body: "v5.0.0", want: "v5.0.0"},
		{name: "application hyphen suffix", endpoint: apiAppVersion, body: "v5.0.0-alpha1", want: "v5.0.0-alpha1"},
		{name: "application direct suffix", endpoint: apiAppVersion, body: "v5.0.0beta1", want: "v5.0.0beta1"},
		{name: "application build suffix", endpoint: apiAppVersion, body: "v5.0.0+git20260911", want: "v5.0.0+git20260911"},
		{name: "web API major minor", endpoint: apiWebAPI, body: "2.11", want: "2.11"},
		{name: "web API stable", endpoint: apiWebAPI, body: "2.11.3", want: "2.11.3"},
		{name: "web API suffix", endpoint: apiWebAPI, body: "2.11.3-rc1+git20260911", want: "2.11.3-rc1+git20260911"},
		{name: "application multiline", endpoint: apiAppVersion, body: "v5.0.0\nunexpected"},
		{name: "application control", endpoint: apiAppVersion, body: "v5.0.0\x00unexpected"},
		{name: "application HTML", endpoint: apiAppVersion, body: "<html>upstream error</html>"},
		{name: "application trailing token", endpoint: apiAppVersion, body: "v5.0.0 trailing"},
		{name: "application leading space", endpoint: apiAppVersion, body: " v5.0.0"},
		{name: "application trailing space", endpoint: apiAppVersion, body: "v5.0.0 "},
		{name: "application empty", endpoint: apiAppVersion, body: ""},
		{name: "application oversized", endpoint: apiAppVersion, body: "v5.0.0-" + strings.Repeat("x", maxVersionBytes)},
		{name: "web API application shape", endpoint: apiWebAPI, body: "v5.0.0"},
		{name: "web API HTML", endpoint: apiWebAPI, body: "<html>2.11</html>"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiLogin {
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
					_, _ = io.WriteString(w, "Ok.")
					return
				}
				if r.URL.Path != test.endpoint {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			var got string
			if test.endpoint == apiAppVersion {
				got, err = client.ApplicationVersion(context.Background())
			} else {
				got, err = client.WebAPIVersion(context.Background())
			}
			if test.want != "" {
				if err != nil || got != test.want {
					t.Fatalf("version = %q, error %v; want %q", got, err, test.want)
				}
				return
			}
			if err == nil || !IsCode(err, ErrorUnknown) {
				t.Fatalf("version = %q, error %v; want malformed unknown", got, err)
			}
		})
	}
}

func TestOversizedHTTPStatusesRetainClassificationAndAuthRecovery(t *testing.T) {
	for _, test := range []struct {
		name             string
		status           int
		wantCode         ErrorCode
		wantRetryable    bool
		recoverAfterAuth bool
		oversized        bool
		wantLogins       int32
	}{
		{name: "small unauthorized", status: http.StatusUnauthorized, wantCode: ErrorUnauthorized, wantLogins: 2},
		{name: "small forbidden", status: http.StatusForbidden, wantCode: ErrorUnauthorized, wantLogins: 2},
		{name: "small rate limited", status: http.StatusTooManyRequests, wantCode: ErrorRateLimited, wantRetryable: true, wantLogins: 1},
		{name: "small server failure", status: http.StatusInternalServerError, wantCode: ErrorUnavailable, wantRetryable: true, wantLogins: 1},
		{name: "oversized unauthorized", status: http.StatusUnauthorized, recoverAfterAuth: true, oversized: true, wantLogins: 2},
		{name: "oversized forbidden", status: http.StatusForbidden, recoverAfterAuth: true, oversized: true, wantLogins: 2},
		{name: "always oversized unauthorized", status: http.StatusUnauthorized, wantCode: ErrorUnauthorized, oversized: true, wantLogins: 2},
		{name: "oversized rate limited", status: http.StatusTooManyRequests, wantCode: ErrorRateLimited, oversized: true, wantRetryable: true, wantLogins: 1},
		{name: "oversized server failure", status: http.StatusInternalServerError, wantCode: ErrorUnavailable, oversized: true, wantRetryable: true, wantLogins: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logins atomic.Int32
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiLogin {
					login := logins.Add(1)
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: fmt.Sprintf("sid-%d", login), Path: "/"})
					_, _ = io.WriteString(w, "Ok.")
					return
				}
				if r.URL.Path != apiAppVersion {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				read := reads.Add(1)
				if test.recoverAfterAuth && read > 1 {
					_, _ = io.WriteString(w, "v5.0.0")
					return
				}
				w.WriteHeader(test.status)
				body := "safe synthetic status detail"
				if test.oversized {
					body = "oversized secret upstream response " + strings.Repeat("x", 128)
				}
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword, MaxResponseBytes: 64})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = client.ApplicationVersion(context.Background())
			if test.recoverAfterAuth {
				if err != nil {
					t.Fatalf("ApplicationVersion error = %v, want recovery", err)
				}
			} else {
				var upstream UpstreamError
				if !errors.As(err, &upstream) || upstream.Code != test.wantCode || upstream.Retryable != test.wantRetryable {
					t.Fatalf("error = %#v, want code %s retryable=%t", err, test.wantCode, test.wantRetryable)
				}
			}
			if strings.Contains(errString(err), "oversized secret upstream response") {
				t.Fatalf("error leaked bounded response body: %v", err)
			}
			if got := logins.Load(); got != test.wantLogins {
				t.Fatalf("login count = %d, want %d", got, test.wantLogins)
			}
		})
	}
}

func TestPieceRangesRequireOrderedNonnegativeIndices(t *testing.T) {
	for _, test := range []struct {
		name   string
		range_ string
		wantOK bool
	}{
		{name: "single piece", range_: "[0,0]", wantOK: true},
		{name: "multi piece", range_: "[0,127]", wantOK: true},
		{name: "negative start", range_: "[-1,5]"},
		{name: "negative end", range_: "[0,-1]"},
		{name: "reversed", range_: "[5,4]"},
		{name: "wrong length", range_: "[0]"},
		{name: "too many", range_: "[0,1,2]"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiLogin {
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
					_, _ = io.WriteString(w, "Ok.")
					return
				}
				if r.URL.Path == apiFiles {
					_, _ = io.WriteString(w, `[{"piece_range":`+test.range_+`}]`)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()
			client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			files, err := client.GetTorrentFiles(context.Background(), testHash)
			if test.wantOK {
				if err != nil || len(files) != 1 {
					t.Fatalf("files = %#v, error %v; want one valid file", files, err)
				}
				return
			}
			if err == nil || !IsCode(err, ErrorUnknown) {
				t.Fatalf("files = %#v, error %v; want malformed unknown", files, err)
			}
		})
	}
}

func TestUnsupportedAndResourceStatusClassification(t *testing.T) {
	for _, test := range []struct {
		name        string
		path        string
		status      int
		wantCode    ErrorCode
		wantRetry   bool
		resource404 bool
	}{
		{name: "version 404", path: apiAppVersion, status: http.StatusNotFound, wantCode: ErrorUnsupported},
		{name: "version 405", path: apiAppVersion, status: http.StatusMethodNotAllowed, wantCode: ErrorUnsupported},
		{name: "version 501", path: apiAppVersion, status: http.StatusNotImplemented, wantCode: ErrorUnsupported},
		{name: "resource 404", path: apiProperties, status: http.StatusNotFound, wantCode: ErrorUnavailable, resource404: true},
		{name: "resource 500", path: apiProperties, status: http.StatusInternalServerError, wantCode: ErrorUnavailable, wantRetry: true, resource404: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiLogin {
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
					_, _ = io.WriteString(w, "Ok.")
					return
				}
				if r.URL.Path != test.path {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, "safe synthetic status detail")
			}))
			defer server.Close()
			client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if test.resource404 {
				_, err = client.GetTorrentProperties(context.Background(), testHash)
			} else {
				_, err = client.ApplicationVersion(context.Background())
			}
			var upstream UpstreamError
			if !errors.As(err, &upstream) || upstream.Code != test.wantCode || upstream.Retryable != test.wantRetry {
				t.Fatalf("error = %#v, want code %s retryable=%t", err, test.wantCode, test.wantRetry)
			}
		})
	}
}

func TestEndpointPathFormsAndRequestURI(t *testing.T) {
	var requestURIs []string
	var origins []string
	var referers []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURIs = append(requestURIs, r.URL.RequestURI())
		origins = append(origins, r.Header.Get("Origin"))
		referers = append(referers, r.Header.Get("Referer"))
		if r.URL.Path == "/proxy"+apiLogin {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
			_, _ = io.WriteString(w, "Ok.")
			return
		}
		if r.URL.Path == "/proxy"+apiAppVersion {
			_, _ = io.WriteString(w, "v5.0.0")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL + "/proxy/", Username: testUsername, Password: testPassword})
	if err != nil {
		t.Fatalf("New clean proxy prefix: %v", err)
	}
	if _, err := client.ApplicationVersion(context.Background()); err != nil {
		t.Fatalf("ApplicationVersion clean proxy prefix: %v", err)
	}
	if got, want := strings.Join(requestURIs, "\n"), "/proxy/api/v2/auth/login\n/proxy/api/v2/app/version"; got != want {
		t.Fatalf("request URIs = %q, want %q", got, want)
	}
	for i := range origins {
		if origins[i] != server.URL || referers[i] != server.URL+"/" {
			t.Fatalf("request %d Origin/Referer = %q/%q, want %q/%q", i, origins[i], referers[i], server.URL, server.URL+"/")
		}
	}
	for _, endpoint := range []string{
		server.URL + "/base/./admin",
		server.URL + "/base/../admin",
		server.URL + "/base/%2e%2e/admin",
		server.URL + "/base/%2F/admin",
		server.URL + "/base/%5C/admin",
		server.URL + "/base//admin",
	} {
		if _, err := New(Config{Endpoint: endpoint, Username: testUsername, Password: testPassword}); err == nil {
			t.Errorf("New(%q) succeeded, want ambiguous endpoint rejection", endpoint)
		}
	}
}

func TestOnlyDocumentedHTTP200ResponsesBecomeEvidence(t *testing.T) {
	properties := fixture(t, "torrent-properties.json")
	files := fixture(t, "torrent-files.json")
	for _, test := range []struct {
		name   string
		path   string
		status int
		body   []byte
		read   func(*Client) error
	}{
		{
			name:   "login 201",
			path:   apiLogin,
			status: http.StatusCreated,
			body:   []byte("Ok."),
			read: func(client *Client) error {
				return client.Login(context.Background())
			},
		},
		{
			name:   "version 202",
			path:   apiAppVersion,
			status: http.StatusAccepted,
			body:   []byte("v5.0.0"),
			read: func(client *Client) error {
				_, err := client.ApplicationVersion(context.Background())
				return err
			},
		},
		{
			name:   "inventory 204",
			path:   apiTorrentInfo,
			status: http.StatusNoContent,
			body:   []byte(`[]`),
			read: func(client *Client) error {
				_, err := client.ListTorrents(context.Background(), TorrentListOptions{})
				return err
			},
		},
		{
			name:   "inventory 206",
			path:   apiTorrentInfo,
			status: http.StatusPartialContent,
			body:   []byte(`[]`),
			read: func(client *Client) error {
				_, err := client.ListTorrents(context.Background(), TorrentListOptions{})
				return err
			},
		},
		{
			name:   "properties 204",
			path:   apiProperties,
			status: http.StatusNoContent,
			body:   properties,
			read: func(client *Client) error {
				_, err := client.GetTorrentProperties(context.Background(), testHash)
				return err
			},
		},
		{
			name:   "properties 206",
			path:   apiProperties,
			status: http.StatusPartialContent,
			body:   properties,
			read: func(client *Client) error {
				_, err := client.GetTorrentProperties(context.Background(), testHash)
				return err
			},
		},
		{
			name:   "files 204",
			path:   apiFiles,
			status: http.StatusNoContent,
			body:   files,
			read: func(client *Client) error {
				_, err := client.GetTorrentFiles(context.Background(), testHash)
				return err
			},
		},
		{
			name:   "files 206",
			path:   apiFiles,
			status: http.StatusPartialContent,
			body:   files,
			read: func(client *Client) error {
				_, err := client.GetTorrentFiles(context.Background(), testHash)
				return err
			},
		},
		{
			name:   "categories 204",
			path:   apiCategories,
			status: http.StatusNoContent,
			body:   []byte(`{}`),
			read: func(client *Client) error {
				_, err := client.Categories(context.Background())
				return err
			},
		},
		{
			name:   "categories 206",
			path:   apiCategories,
			status: http.StatusPartialContent,
			body:   []byte(`{}`),
			read: func(client *Client) error {
				_, err := client.Categories(context.Background())
				return err
			},
		},
		{
			name:   "tags 204",
			path:   apiTags,
			status: http.StatusNoContent,
			body:   []byte(`[]`),
			read: func(client *Client) error {
				_, err := client.Tags(context.Background())
				return err
			},
		},
		{
			name:   "tags 206",
			path:   apiTags,
			status: http.StatusPartialContent,
			body:   []byte(`[]`),
			read: func(client *Client) error {
				_, err := client.Tags(context.Background())
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiLogin {
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
					if test.path == apiLogin {
						w.WriteHeader(test.status)
						_, _ = w.Write(test.body)
						return
					}
					_, _ = io.WriteString(w, "Ok.")
					return
				}
				if r.URL.Path == test.path {
					w.WriteHeader(test.status)
					_, _ = w.Write(test.body)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()
			client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			err = test.read(client)
			var upstream UpstreamError
			if !errors.As(err, &upstream) || upstream.Code != ErrorUnsupported || upstream.Status != test.status || upstream.Retryable {
				t.Fatalf("error = %#v, want unsupported status %d", err, test.status)
			}
		})
	}
}

func TestStrictJSONMembersAndUTF8(t *testing.T) {
	valid40 := testHash
	valid64 := strings.Repeat("a", 64)
	strictCases := []struct {
		name     string
		path     string
		body     []byte
		wantSize int
	}{
		{name: "duplicate inventory member", path: apiTorrentInfo, body: []byte(`[{"hash":"` + valid40 + `","hash":"` + valid64 + `"}]`)},
		{name: "mixed-case inventory member", path: apiTorrentInfo, body: []byte(`[{"HASH":"` + valid40 + `"}]`)},
		{name: "raw invalid UTF-8 in hash", path: apiTorrentInfo, body: append([]byte(`[{"hash":"`), append([]byte{0xff}, []byte(`"}]`)...)...)},
		{name: "raw invalid UTF-8 in path", path: apiTorrentInfo, body: append([]byte(`[{"hash":"`+valid40+`","content_path":"`), append([]byte{0xff}, []byte(`"}]`)...)...)},
		{name: "duplicate nested category member", path: apiCategories, body: []byte(`{"synthetic":{"name":"one","name":"two","savePath":"/synthetic"}}`)},
		{name: "valid Unicode value", path: apiTorrentInfo, body: []byte(`[{"hash":"` + valid40 + `","name":"媒体"}]`), wantSize: 1},
		{name: "repeated member names across records", path: apiTorrentInfo, body: []byte(`[{"hash":"` + valid40 + `"},{"hash":"` + valid64 + `"}]`), wantSize: 2},
	}
	for _, test := range strictCases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiLogin {
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
					_, _ = io.WriteString(w, "Ok.")
					return
				}
				if r.URL.Path == test.path {
					_, _ = w.Write(test.body)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()
			client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if test.path == apiCategories {
				categories, categoryErr := client.Categories(context.Background())
				if test.wantSize > 0 {
					t.Fatalf("unexpected valid category case: %#v, error %v", categories, categoryErr)
				}
				if categoryErr == nil || !IsCode(categoryErr, ErrorUnknown) {
					t.Fatalf("Categories error = %v, want malformed unknown", categoryErr)
				}
				return
			}
			torrents, listErr := client.ListTorrents(context.Background(), TorrentListOptions{})
			if test.wantSize > 0 {
				if listErr != nil || len(torrents) != test.wantSize {
					t.Fatalf("ListTorrents = %#v, error %v; want %d records", torrents, listErr, test.wantSize)
				}
				return
			}
			if listErr == nil || !IsCode(listErr, ErrorUnknown) {
				t.Fatalf("ListTorrents error = %v, want malformed unknown", listErr)
			}
		})
	}
}

func TestInventoryHashesRequireSupportedIdentities(t *testing.T) {
	valid64 := strings.Repeat("a", 64)
	base := fixture(t, "torrent-info.json")
	baseRecord := strings.TrimSpace(string(base))
	baseRecord = strings.TrimPrefix(baseRecord, "[")
	baseRecord = strings.TrimSuffix(baseRecord, "]")
	for _, test := range []struct {
		name     string
		body     []byte
		wantSize int
	}{
		{name: "valid v1", body: base, wantSize: 1},
		{name: "valid v2", body: inventoryWithHash(base, valid64), wantSize: 1},
		{name: "valid uppercase v1", body: inventoryWithHash(base, strings.ToUpper(testHash)), wantSize: 1},
		{name: "empty", body: inventoryWithHash(base, "")},
		{name: "delimiter", body: inventoryWithHash(base, testHash+"|tail")},
		{name: "whitespace", body: inventoryWithHash(base, testHash+" ")},
		{name: "control escape", body: inventoryWithHash(base, `\u0000`+testHash)},
		{name: "overbound", body: inventoryWithHash(base, strings.Repeat("a", 65))},
		{name: "wrong length", body: inventoryWithHash(base, strings.Repeat("a", 39))},
		{name: "non-hex", body: inventoryWithHash(base, strings.Repeat("g", 40))},
		{name: "missing", body: []byte(strings.Replace(string(base), `    "hash": "`+testHash+`",`+"\n", "", 1))},
		{name: "one invalid record rejects whole response", body: []byte("[" + baseRecord + "," + strings.Replace(baseRecord, `"hash": "`+testHash+`"`, `"hash": ""`, 1) + "]")},
		{name: "raw invalid UTF-8", body: inventoryWithRawInvalidHash(base)},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == apiLogin {
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: testSID, Path: "/"})
					_, _ = io.WriteString(w, "Ok.")
					return
				}
				if r.URL.Path == apiTorrentInfo {
					_, _ = w.Write(test.body)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()
			client, err := New(Config{Endpoint: server.URL, Username: testUsername, Password: testPassword})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			torrents, listErr := client.ListTorrents(context.Background(), TorrentListOptions{})
			if test.wantSize > 0 {
				if listErr != nil || len(torrents) != test.wantSize || torrents[0].Hash == "" {
					t.Fatalf("ListTorrents = %#v, error %v; want %d valid records", torrents, listErr, test.wantSize)
				}
				return
			}
			if listErr == nil || !IsCode(listErr, ErrorUnknown) || len(torrents) != 0 {
				t.Fatalf("ListTorrents = %#v, error %v; want whole-observation rejection", torrents, listErr)
			}
		})
	}
}

func TestNonpositiveInjectedTimeoutUsesSafeDefault(t *testing.T) {
	for _, test := range []struct {
		name    string
		timeout time.Duration
		want    time.Duration
	}{
		{name: "negative", timeout: -time.Second, want: defaultHTTPTimeout},
		{name: "zero", timeout: 0, want: defaultHTTPTimeout},
		{name: "positive", timeout: 25 * time.Millisecond, want: 25 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, err := New(Config{
				Endpoint:   "http://synthetic.invalid",
				Username:   testUsername,
				Password:   testPassword,
				HTTPClient: &http.Client{Timeout: test.timeout},
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if client.http.Timeout != test.want {
				t.Fatalf("effective timeout = %s, want %s", client.http.Timeout, test.want)
			}
		})
	}
}

func inventoryWithHash(base []byte, hash string) []byte {
	needle := `"hash": "` + testHash + `"`
	return []byte(strings.Replace(string(base), needle, `"hash": "`+hash+`"`, 1))
}

func inventoryWithRawInvalidHash(base []byte) []byte {
	needle := []byte(`"hash": "` + testHash + `"`)
	replacement := append([]byte(`"hash": "`), 0xff)
	replacement = append(replacement, []byte(`"`)...)
	return bytes.Replace(base, needle, replacement, 1)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %q: %v", name, err)
	}
	return data
}
