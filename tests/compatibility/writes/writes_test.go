package writes

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// These fixtures describe synthetic native API interactions. They are kept in
// this package so the disposable evidence can run without a live media stack.
//
//go:embed *.json
var fixtureFiles embed.FS

type arrRejection struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type arrPreviewCandidate struct {
	Path       string         `json:"path"`
	Movie      *arrMovie      `json:"movie,omitempty"`
	Series     *arrSeries     `json:"series,omitempty"`
	Episodes   []arrEpisode   `json:"episodes,omitempty"`
	Rejections []arrRejection `json:"rejections"`
}

type arrMovie struct {
	ID int `json:"id"`
}

type arrSeries struct {
	ID int `json:"id"`
}

type arrEpisode struct {
	ID                    int  `json:"id"`
	AbsoluteEpisodeNumber *int `json:"absoluteEpisodeNumber,omitempty"`
}

type arrCommand struct {
	Name  string           `json:"name"`
	Files []arrCommandFile `json:"files"`
}

type arrCommandFile struct {
	Path       string `json:"path"`
	MovieID    int    `json:"movieId,omitempty"`
	SeriesID   int    `json:"seriesId,omitempty"`
	EpisodeIDs []int  `json:"episodeIds,omitempty"`
}

type arrMovieReadback struct {
	ID        int           `json:"id"`
	MovieFile *arrMovieFile `json:"movieFile,omitempty"`
}

type arrMovieFile struct {
	Path string `json:"path"`
}

func TestArrPreviewRejectionDoesNotAuthorizeCommand(t *testing.T) {
	const source = "/synthetic/downloads/Synthetic Film (2024).mkv"
	const destination = "/synthetic/library/Synthetic Film (2024).mkv"

	var commandCalls atomic.Int32
	files := map[string][]byte{
		source:      []byte("synthetic-source-bytes"),
		destination: []byte("synthetic-existing-library-bytes"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/manualimport":
			writeJSON(t, w, []arrPreviewCandidate{{
				Path:       source,
				Movie:      &arrMovie{ID: 101},
				Rejections: []arrRejection{{Type: "ExistingFile", Message: "synthetic destination exists"}},
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			commandCalls.Add(1)
			var command arrCommand
			decodeJSON(t, r, &command)
			if command.Name != "ManualImport" || len(command.Files) != 1 || command.Files[0].Path != source {
				t.Errorf("command = %#v, want exact synthetic ManualImport file", command)
			}
			// Native command execution receives no preview rejection. This models
			// the documented command/preview split and intentionally overwrites.
			files[destination] = append([]byte(nil), files[source]...)
			writeJSON(t, w, map[string]any{"id": 7001, "status": "completed"})
		default:
			http.Error(w, "synthetic route not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	var preview []arrPreviewCandidate
	getJSON(t, server.Client(), server.URL+"/api/v3/manualimport", &preview)
	if len(preview) != 1 || len(preview[0].Rejections) != 1 {
		t.Fatalf("preview = %#v, want one rejected candidate", preview)
	}
	if !arrPreviewMustBlock(preview) {
		t.Fatal("native import must be blocked when preview contains a rejection")
	}

	// Direct command call proves the upstream route can bypass GET evidence;
	// Mastarr must therefore refuse dispatch rather than trust command success.
	postJSON(t, server.Client(), server.URL+"/api/v3/command", arrCommand{
		Name:  "ManualImport",
		Files: []arrCommandFile{{Path: source, MovieID: 101}},
	}, nil)
	if got, want := commandCalls.Load(), int32(1); got != want {
		t.Fatalf("native command calls = %d, want one bypass reproduction", got)
	}
	if !bytes.Equal(files[destination], files[source]) {
		t.Fatal("synthetic native command did not demonstrate destination replacement")
	}
}

func TestArrNativeCommandRaceProvesNoOverwriteUnknown(t *testing.T) {
	const source = "/synthetic/downloads/Synthetic Film (2024).mkv"
	const destination = "/synthetic/library/Synthetic Film (2024).mkv"

	var filesMu sync.Mutex
	files := map[string][]byte{source: []byte("synthetic-source-bytes")}
	commandEntered := make(chan struct{})
	releaseCommand := make(chan struct{})
	var commandCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/manualimport":
			writeJSON(t, w, []arrPreviewCandidate{{Path: source, Movie: &arrMovie{ID: 101}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			commandCalls.Add(1)
			close(commandEntered)
			<-releaseCommand
			filesMu.Lock()
			files[destination] = append([]byte(nil), files[source]...)
			filesMu.Unlock()
			writeJSON(t, w, map[string]any{"id": 7002, "status": "completed"})
		default:
			http.Error(w, "synthetic route not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	var preview []arrPreviewCandidate
	getJSON(t, server.Client(), server.URL+"/api/v3/manualimport", &preview)
	if arrPreviewMustBlock(preview) {
		t.Fatal("accepted synthetic preview unexpectedly contains rejection")
	}

	result := make(chan error, 1)
	go func() {
		_, err := postJSON(t, server.Client(), server.URL+"/api/v3/command", arrCommand{
			Name:  "ManualImport",
			Files: []arrCommandFile{{Path: source, MovieID: 101}},
		}, nil)
		result <- err
	}()
	<-commandEntered

	// External writer wins the preflight race after native command dispatch.
	filesMu.Lock()
	files[destination] = []byte("external-writer-bytes")
	filesMu.Unlock()
	close(releaseCommand)
	if err := <-result; err != nil {
		t.Fatalf("synthetic command: %v", err)
	}
	if got, want := commandCalls.Load(), int32(1); got != want {
		t.Fatalf("native command calls = %d, want one", got)
	}

	filesMu.Lock()
	final := append([]byte(nil), files[destination]...)
	filesMu.Unlock()
	if !bytes.Equal(final, []byte("synthetic-source-bytes")) {
		t.Fatalf("final destination = %q, want native overwrite reproduction", final)
	}
	if outcome := reconcileArrRace(true, true); outcome != "outcome_unknown" {
		t.Fatalf("race outcome = %q, want outcome_unknown", outcome)
	}
}

func TestArrPartialPackReconcilesPerFile(t *testing.T) {
	const first = "/synthetic/downloads/Synthetic Pack/S01E01.mkv"
	const second = "/synthetic/downloads/Synthetic Pack/S01E02.mkv"
	var imported []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			var command arrCommand
			decodeJSON(t, r, &command)
			if command.Name != "ManualImport" || len(command.Files) != 2 {
				t.Errorf("command = %#v, want two exact pack files", command)
			}
			imported = []string{first}
			writeJSON(t, w, map[string]any{"id": 7003, "status": "completed"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/series/201":
			writeJSON(t, w, arrMovieReadback{ID: 201, MovieFile: &arrMovieFile{Path: first}})
		default:
			http.Error(w, "synthetic route not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	_, err := postJSON(t, server.Client(), server.URL+"/api/v3/command", arrCommand{
		Name: "ManualImport",
		Files: []arrCommandFile{
			{Path: first, SeriesID: 201, EpisodeIDs: []int{301}},
			{Path: second, SeriesID: 201, EpisodeIDs: []int{302}},
		},
	}, nil)
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	var readback arrMovieReadback
	getJSON(t, server.Client(), server.URL+"/api/v3/series/201", &readback)
	if readback.MovieFile == nil || readback.MovieFile.Path != first {
		t.Fatalf("readback = %#v, want first imported file", readback)
	}
	results := reconcileFiles([]string{first, second}, imported)
	if results[first] != "applied" || results[second] != "unresolved" {
		t.Fatalf("per-file results = %#v, want applied plus unresolved", results)
	}
	if aggregateImportSuccess(results) {
		t.Fatal("partial pack must not become aggregate success")
	}
}

type qbtInfo struct {
	Hash    string `json:"hash"`
	State   string `json:"state"`
	UpSpeed int64  `json:"upspeed"`
}

type qbtFile struct {
	Name string `json:"name"`
}

type qbtFixture struct {
	mu                sync.Mutex
	hash              string
	state             string
	upSpeed           int64
	files             map[string][]byte
	removed           bool
	stopCalls         int
	removeDeleteFiles []bool
	lostStopResponse  bool
}

func newQBTServer(t *testing.T, fixture *qbtFixture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/torrents/info":
			fixture.mu.Lock()
			info := qbtInfo{Hash: fixture.hash, State: fixture.state, UpSpeed: fixture.upSpeed}
			fixture.mu.Unlock()
			writeJSON(t, w, []qbtInfo{info})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/torrents/files":
			fixture.mu.Lock()
			names := make([]string, 0, len(fixture.files))
			for name := range fixture.files {
				names = append(names, name)
			}
			fixture.mu.Unlock()
			writeJSON(t, w, namesToQBTFiles(names))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/torrents/stop":
			if err := r.ParseForm(); err != nil || r.PostForm.Get("hashes") != fixture.hash {
				http.Error(w, "synthetic hash mismatch", http.StatusBadRequest)
				return
			}
			fixture.mu.Lock()
			fixture.stopCalls++
			fixture.state = "pausedUP"
			lost := fixture.lostStopResponse
			fixture.mu.Unlock()
			if lost {
				if hijacker, ok := w.(http.Hijacker); ok {
					conn, _, err := hijacker.Hijack()
					if err == nil {
						_ = conn.Close()
						return
					}
				}
				return
			}
			_, _ = io.WriteString(w, "")
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/torrents/delete":
			if err := r.ParseForm(); err != nil || r.PostForm.Get("hashes") != fixture.hash {
				http.Error(w, "synthetic hash mismatch", http.StatusBadRequest)
				return
			}
			deleteFiles := r.PostForm.Get("deleteFiles") == "true"
			fixture.mu.Lock()
			fixture.removeDeleteFiles = append(fixture.removeDeleteFiles, deleteFiles)
			if !deleteFiles {
				fixture.removed = true
			}
			fixture.mu.Unlock()
			if deleteFiles {
				http.Error(w, "deleteFiles=true is outside Mastarr scope", http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(w, "")
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/torrents/renameFolder":
			if err := renameQBTFolder(fixture, r); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			_, _ = io.WriteString(w, "")
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/torrents/renameFile":
			if err := renameQBTFile(fixture, r); err != nil {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			_, _ = io.WriteString(w, "")
		default:
			http.Error(w, "synthetic route not found", http.StatusNotFound)
		}
	}))
}

func TestQBTZeroUploadIsNotStopped(t *testing.T) {
	fixture := &qbtFixture{
		hash:    "0123456789abcdef0123456789abcdef01234567",
		state:   "stalledUP",
		upSpeed: 0,
		files:   map[string][]byte{"Synthetic Film.mkv": []byte("synthetic")},
	}
	server := newQBTServer(t, fixture)
	t.Cleanup(server.Close)

	var infos []qbtInfo
	getJSON(t, server.Client(), server.URL+"/api/v2/torrents/info?hashes="+fixture.hash, &infos)
	if len(infos) != 1 || infos[0].UpSpeed != 0 {
		t.Fatalf("torrent info = %#v, want zero-speed synthetic seeding state", infos)
	}
	if qbtStopped(infos[0].State) {
		t.Fatalf("state %q was incorrectly treated as stopped", infos[0].State)
	}
}

func TestQBTLostStopResponseReconcilesBeforePayloadMutation(t *testing.T) {
	fixture := &qbtFixture{
		hash:             "0123456789abcdef0123456789abcdef01234567",
		state:            "stalledUP",
		upSpeed:          0,
		files:            map[string][]byte{"Synthetic Film.mkv": []byte("synthetic")},
		lostStopResponse: true,
	}
	server := newQBTServer(t, fixture)
	t.Cleanup(server.Close)

	_, err := postForm(t, server.Client(), server.URL+"/api/v2/torrents/stop", url.Values{"hashes": {fixture.hash}})
	if err == nil {
		t.Fatal("lost stop response unexpectedly returned success")
	}
	var afterStop []qbtInfo
	getJSON(t, server.Client(), server.URL+"/api/v2/torrents/info?hashes="+fixture.hash, &afterStop)
	if len(afterStop) != 1 || !qbtStopped(afterStop[0].State) {
		t.Fatalf("readback after lost response = %#v, want stopped", afterStop)
	}

	// Another actor resumes seeding after the first read-back. Payload action
	// must perform a fresh observation and stop when state is no longer safe.
	fixture.mu.Lock()
	fixture.state = "stalledUP"
	fixture.mu.Unlock()
	var resumed []qbtInfo
	getJSON(t, server.Client(), server.URL+"/api/v2/torrents/info?hashes="+fixture.hash, &resumed)
	if len(resumed) != 1 || qbtStopped(resumed[0].State) {
		t.Fatalf("resumed readback = %#v, want seeding state", resumed)
	}
	if qbtPayloadMutationAllowed(resumed[0]) {
		t.Fatal("payload mutation was allowed after external resume")
	}
	fixture.mu.Lock()
	stopCalls := fixture.stopCalls
	fixture.mu.Unlock()
	if stopCalls != 1 {
		t.Fatalf("stop calls = %d, want no blind retry", stopCalls)
	}
}

func TestQBTPartialTrashStopsWholeTorrentAndRemovesMetadataOnly(t *testing.T) {
	fixture := &qbtFixture{
		hash:  "0123456789abcdef0123456789abcdef01234567",
		state: "stalledUP",
		files: map[string][]byte{
			"Synthetic Pack/episode-one.mkv": []byte("one"),
			"Synthetic Pack/episode-two.mkv": []byte("two"),
			"Synthetic Pack/sample.txt":      []byte("sample"),
		},
	}
	server := newQBTServer(t, fixture)
	t.Cleanup(server.Close)

	if _, err := postForm(t, server.Client(), server.URL+"/api/v2/torrents/stop", url.Values{"hashes": {fixture.hash}}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	var stopped []qbtInfo
	getJSON(t, server.Client(), server.URL+"/api/v2/torrents/info?hashes="+fixture.hash, &stopped)
	if len(stopped) != 1 || !qbtStopped(stopped[0].State) {
		t.Fatalf("stopped readback = %#v", stopped)
	}
	if _, err := postForm(t, server.Client(), server.URL+"/api/v2/torrents/delete", url.Values{
		"hashes":      {fixture.hash},
		"deleteFiles": {"false"},
	}); err != nil {
		t.Fatalf("metadata-only remove: %v", err)
	}

	fixture.mu.Lock()
	deferred := cloneFileMap(fixture.files)
	removed := fixture.removed
	deleteFlags := append([]bool(nil), fixture.removeDeleteFiles...)
	fixture.mu.Unlock()
	if !removed || len(deleteFlags) != 1 || deleteFlags[0] {
		t.Fatalf("remove state removed=%v deleteFlags=%v, want metadata-only", removed, deleteFlags)
	}
	if len(deferred) != 3 || !bytes.Equal(deferred["Synthetic Pack/episode-two.mkv"], []byte("two")) || !bytes.Equal(deferred["Synthetic Pack/sample.txt"], []byte("sample")) {
		t.Fatalf("payload after metadata removal = %#v, want all three synthetic files retained", deferred)
	}
}

func TestQBTNativeRenameRequiresWholeTorrentAndReadback(t *testing.T) {
	fixture := &qbtFixture{
		hash:  "0123456789abcdef0123456789abcdef01234567",
		state: "pausedUP",
		files: map[string][]byte{
			"Synthetic Pack/episode-one.mkv": []byte("one"),
			"Synthetic Pack/episode-two.mkv": []byte("two"),
			"Synthetic Pack/existing.mkv":    []byte("existing"),
		},
	}
	server := newQBTServer(t, fixture)
	t.Cleanup(server.Close)

	all := []string{"Synthetic Pack/episode-one.mkv", "Synthetic Pack/episode-two.mkv", "Synthetic Pack/existing.mkv"}
	if qbtNativeScopeAllowed(all[:1], all) {
		t.Fatal("native folder rename must refuse selected-file scope")
	}
	if _, err := postForm(t, server.Client(), server.URL+"/api/v2/torrents/renameFolder", url.Values{
		"hash":    {fixture.hash},
		"oldPath": {"Synthetic Pack"},
		"newPath": {"Renamed Pack"},
	}); err != nil {
		t.Fatalf("whole-torrent folder rename: %v", err)
	}
	var renamed []qbtFile
	getJSON(t, server.Client(), server.URL+"/api/v2/torrents/files?hash="+fixture.hash, &renamed)
	want := []string{"Renamed Pack/episode-one.mkv", "Renamed Pack/episode-two.mkv", "Renamed Pack/existing.mkv"}
	if got := qbtFileNames(renamed); !sameStringSet(got, want) {
		t.Fatalf("renamed paths = %#v, want %#v", got, want)
	}

	before := cloneFileMapLocked(fixture)
	if _, err := postForm(t, server.Client(), server.URL+"/api/v2/torrents/renameFile", url.Values{
		"hash":    {fixture.hash},
		"oldPath": {"Renamed Pack/episode-one.mkv"},
		"newPath": {"Renamed Pack/existing.mkv"},
	}); err == nil {
		t.Fatal("native rename collision unexpectedly succeeded")
	}
	after := cloneFileMapLocked(fixture)
	if !sameFileMap(before, after) {
		t.Fatalf("collision changed payload paths: before=%#v after=%#v", before, after)
	}
}

type jellyfinItem struct {
	ID   string `json:"Id"`
	Path string `json:"Path"`
}

func TestJellyfinRefreshAcceptanceIsSeparateFromAvailability(t *testing.T) {
	var refreshCalls atomic.Int32
	var visible atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/Library/Refresh":
			refreshCalls.Add(1)
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet && r.URL.Path == "/Items":
			if visible.Load() {
				writeJSON(t, w, map[string]any{"Items": []jellyfinItem{{ID: "jf-101", Path: "/synthetic/library/Synthetic Film (2024).mkv"}}})
				return
			}
			writeJSON(t, w, map[string]any{"Items": []jellyfinItem{}})
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/Items/") && strings.HasSuffix(r.URL.Path, "/Refresh"):
			// Item scope is deliberately not enabled until a versioned route is
			// pinned; this fixture exposes the unsupported response explicitly.
			http.Error(w, "synthetic item refresh unsupported", http.StatusNotFound)
		default:
			http.Error(w, "synthetic route not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	response, err := http.Post(server.URL+"/Library/Refresh", "application/json", nil)
	if err != nil {
		t.Fatalf("library refresh: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("refresh status = %d, want 202", response.StatusCode)
	}
	var before map[string][]jellyfinItem
	getJSON(t, server.Client(), server.URL+"/Items", &before)
	if len(before["Items"]) != 0 {
		t.Fatalf("availability immediately after refresh = %#v, want absent", before)
	}
	visible.Store(true)
	var after map[string][]jellyfinItem
	getJSON(t, server.Client(), server.URL+"/Items", &after)
	if len(after["Items"]) != 1 || after["Items"][0].ID != "jf-101" {
		t.Fatalf("later availability = %#v, want one synthetic item", after)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want one", refreshCalls.Load())
	}
	if !jellyfinPathMapped(after["Items"][0].Path, "/synthetic/library") {
		t.Fatal("expected synthetic item path did not map to configured library root")
	}
	if jellyfinPathMapped("/synthetic/other/Synthetic Film (2024).mkv", "/synthetic/library") {
		t.Fatal("wrong library root was treated as available")
	}
	itemRefresh, err := http.Post(server.URL+"/Items/jf-101/Refresh", "application/json", nil)
	if err != nil {
		t.Fatalf("item refresh: %v", err)
	}
	itemRefresh.Body.Close()
	if itemRefresh.StatusCode != http.StatusNotFound {
		t.Fatalf("item refresh status = %d, want unsupported 404", itemRefresh.StatusCode)
	}
}

type subtitleFixture struct {
	Video     animeVideo      `json:"video"`
	Subtitles []subtitleEntry `json:"subtitles"`
}

type animeVideo struct {
	Path            string   `json:"path"`
	EpisodeIDs      []string `json:"episodeIds"`
	AbsoluteNumbers []int    `json:"absoluteNumbers"`
}

type subtitleEntry struct {
	Path            string   `json:"path"`
	Language        string   `json:"language"`
	Forced          bool     `json:"forced,omitempty"`
	HearingImpaired bool     `json:"hearingImpaired,omitempty"`
	Pair            string   `json:"pair,omitempty"`
	EpisodeIDs      []string `json:"episodeIds"`
}

func TestArrSubtitleAndAnimeMappingsRequireExplicitAssociations(t *testing.T) {
	var fixture subtitleFixture
	readFixture(t, "arr-subtitle-anime.json", &fixture)
	if len(fixture.Video.EpisodeIDs) != len(fixture.Video.AbsoluteNumbers) || len(fixture.Video.EpisodeIDs) != 2 {
		t.Fatalf("anime mapping = %#v, want two explicit absolute-number associations", fixture.Video)
	}
	if got := explicitEpisodeIDs(fixture.Video); !sameStrings(got, []string{"401", "402"}) {
		t.Fatalf("episode IDs = %#v, want native selected IDs", got)
	}

	subtitles, unresolved, err := validateSubtitleMappings(fixture.Subtitles)
	if err != nil {
		t.Fatalf("subtitle mappings: %v", err)
	}
	if len(subtitles) != 4 || len(unresolved) != 1 || unresolved[0] != "/synthetic/downloads/unmatched.srt" {
		t.Fatalf("subtitle result = %#v unresolved=%#v, want four explicit plus one unresolved", subtitles, unresolved)
	}
	if !subtitles["/synthetic/downloads/Synthetic Anime S01E01.en.forced.srt"].Forced || !subtitles["/synthetic/downloads/Synthetic Anime S01E01.en.sdh.srt"].HearingImpaired {
		t.Fatalf("subtitle labels were not preserved: %#v", subtitles)
	}
	if subtitles["/synthetic/downloads/Synthetic Anime S01E01.pt.idx"].Pair != "/synthetic/downloads/Synthetic Anime S01E01.pt.sub" {
		t.Fatalf("IDX/SUB pair was not retained: %#v", subtitles)
	}

	withoutAssociation := append([]subtitleEntry(nil), fixture.Subtitles...)
	withoutAssociation[0].EpisodeIDs = nil
	_, unresolved, err = validateSubtitleMappings(withoutAssociation)
	if err != nil {
		t.Fatalf("unmatched subtitle mapping: %v", err)
	}
	if len(unresolved) != 2 {
		t.Fatalf("unmatched subtitles = %#v, want explicit unresolved entries", unresolved)
	}

	duplicate := append([]string(nil), fixture.Video.EpisodeIDs...)
	duplicate[1] = duplicate[0]
	if err := validateEpisodeIDs(duplicate); err == nil {
		t.Fatal("duplicate episode identity unexpectedly accepted")
	}
}

func arrPreviewMustBlock(preview []arrPreviewCandidate) bool {
	for _, candidate := range preview {
		if len(candidate.Rejections) > 0 {
			return true
		}
	}
	return false
}

func reconcileArrRace(preflightPassed, externalRace bool) string {
	if preflightPassed && externalRace {
		return "outcome_unknown"
	}
	return "applied"
}

func reconcileFiles(expected, observed []string) map[string]string {
	seen := make(map[string]struct{}, len(observed))
	for _, path := range observed {
		seen[path] = struct{}{}
	}
	result := make(map[string]string, len(expected))
	for _, path := range expected {
		if _, ok := seen[path]; ok {
			result[path] = "applied"
		} else {
			result[path] = "unresolved"
		}
	}
	return result
}

func aggregateImportSuccess(results map[string]string) bool {
	if len(results) == 0 {
		return false
	}
	for _, result := range results {
		if result != "applied" {
			return false
		}
	}
	return true
}

func qbtStopped(state string) bool {
	switch state {
	case "pausedDL", "pausedUP", "paused", "stoppedDL", "stoppedUP", "stopped":
		return true
	default:
		return false
	}
}

func qbtPayloadMutationAllowed(info qbtInfo) bool {
	return qbtStopped(info.State)
}

func qbtNativeScopeAllowed(selected, all []string) bool {
	return len(selected) == len(all) && sameStringSet(selected, all)
}

func namesToQBTFiles(names []string) []qbtFile {
	files := make([]qbtFile, 0, len(names))
	for _, name := range names {
		files = append(files, qbtFile{Name: name})
	}
	return files
}

func qbtFileNames(files []qbtFile) []string {
	names := make([]string, 0, len(files))
	for _, file := range files {
		names = append(names, file.Name)
	}
	return names
}

func renameQBTFolder(fixture *qbtFixture, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return err
	}
	if r.PostForm.Get("hash") != fixture.hash {
		return errors.New("synthetic hash mismatch")
	}
	oldPath, newPath := r.PostForm.Get("oldPath"), r.PostForm.Get("newPath")
	if oldPath == "" || newPath == "" {
		return errors.New("synthetic rename fields missing")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	updates := make(map[string][]byte, len(fixture.files))
	for name, content := range fixture.files {
		if name == oldPath || strings.HasPrefix(name, oldPath+"/") {
			target := strings.TrimPrefix(newPath+strings.TrimPrefix(name, oldPath), "/")
			if _, exists := fixture.files[target]; exists && target != name {
				return errors.New("synthetic folder rename collision")
			}
			if _, exists := updates[target]; exists {
				return errors.New("synthetic folder rename collision")
			}
			updates[target] = content
		} else {
			if _, exists := updates[name]; exists {
				return errors.New("synthetic folder rename collision")
			}
			updates[name] = content
		}
	}
	fixture.files = updates
	return nil
}

func renameQBTFile(fixture *qbtFixture, r *http.Request) error {
	if err := r.ParseForm(); err != nil {
		return err
	}
	if r.PostForm.Get("hash") != fixture.hash {
		return errors.New("synthetic hash mismatch")
	}
	oldPath, newPath := r.PostForm.Get("oldPath"), r.PostForm.Get("newPath")
	if oldPath == "" || newPath == "" {
		return errors.New("synthetic rename fields missing")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	content, ok := fixture.files[oldPath]
	if !ok {
		return errors.New("synthetic old path missing")
	}
	if _, exists := fixture.files[newPath]; exists {
		return errors.New("synthetic destination collision")
	}
	delete(fixture.files, oldPath)
	fixture.files[newPath] = content
	return nil
}

func jellyfinPathMapped(candidate, root string) bool {
	candidate, root = path.Clean(candidate), path.Clean(root)
	return candidate == root || strings.HasPrefix(candidate, root+"/")
}

type explicitSubtitle struct {
	Language        string
	Forced          bool
	HearingImpaired bool
	Pair            string
	EpisodeIDs      []string
}

func validateSubtitleMappings(entries []subtitleEntry) (map[string]explicitSubtitle, []string, error) {
	byPath := make(map[string]subtitleEntry, len(entries))
	for _, entry := range entries {
		byPath[entry.Path] = entry
	}
	result := make(map[string]explicitSubtitle, len(entries))
	var unresolved []string
	for _, entry := range entries {
		if len(entry.EpisodeIDs) == 0 {
			unresolved = append(unresolved, entry.Path)
			continue
		}
		if entry.Pair != "" {
			pair, ok := byPath[entry.Pair]
			if !ok || !strings.HasSuffix(entry.Path, ".idx") || !strings.HasSuffix(pair.Path, ".sub") {
				return nil, nil, fmt.Errorf("invalid subtitle pair %q", entry.Path)
			}
		}
		result[entry.Path] = explicitSubtitle{
			Language: entry.Language, Forced: entry.Forced,
			HearingImpaired: entry.HearingImpaired, Pair: entry.Pair,
			EpisodeIDs: append([]string(nil), entry.EpisodeIDs...),
		}
	}
	return result, unresolved, nil
}

func validateEpisodeIDs(ids []string) error {
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			return errors.New("empty episode identity")
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("duplicate episode identity %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func explicitEpisodeIDs(video animeVideo) []string {
	return append([]string(nil), video.EpisodeIDs...)
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
		if counts[value] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func cloneFileMap(input map[string][]byte) map[string][]byte {
	output := make(map[string][]byte, len(input))
	for name, content := range input {
		output[name] = append([]byte(nil), content...)
	}
	return output
}

func cloneFileMapLocked(fixture *qbtFixture) map[string][]byte {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	return cloneFileMap(fixture.files)
}

func sameFileMap(left, right map[string][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for name, content := range left {
		if !bytes.Equal(content, right[name]) {
			return false
		}
	}
	return true
}

func readFixture(t *testing.T, name string, value any) {
	t.Helper()
	data, err := fixtureFiles.ReadFile(name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("write JSON: %v", err)
	}
}

func decodeJSON(t *testing.T, r *http.Request, value any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(value); err != nil {
		t.Errorf("decode JSON: %v", err)
	}
}

func getJSON(t *testing.T, client *http.Client, endpoint string, value any) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatalf("new GET: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", endpoint, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(value); err != nil {
		t.Fatalf("decode GET %s: %v", endpoint, err)
	}
}

func postJSON(t *testing.T, client *http.Client, endpoint string, value any, result any) (*http.Response, error) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal POST: %v", err)
	}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new POST: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, fmt.Errorf("POST %s status = %d", endpoint, response.StatusCode)
	}
	if result != nil {
		if err := json.NewDecoder(response.Body).Decode(result); err != nil {
			return response, err
		}
	}
	return response, nil
}

func postForm(t *testing.T, client *http.Client, endpoint string, values url.Values) (*http.Response, error) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatalf("new form POST: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, fmt.Errorf("POST %s status = %d", endpoint, response.StatusCode)
	}
	return response, nil
}
