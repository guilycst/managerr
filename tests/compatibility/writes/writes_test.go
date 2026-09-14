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

type arrRaceFixture struct {
	Upstream    string         `json:"upstream"`
	Source      string         `json:"source"`
	Destination string         `json:"destination"`
	Preview     arrFixtureCase `json:"preview"`
	Rejection   arrFixtureCase `json:"rejection"`
	Race        arrRaceCase    `json:"race"`
	Expected    string         `json:"expected"`
}

type arrFixtureCase struct {
	Path       string         `json:"path"`
	Rejections []arrRejection `json:"rejections"`
}

type arrRaceCase struct {
	ExternalReplacement string `json:"externalReplacement"`
	NativeCommand       string `json:"nativeCommand"`
}

type qbtStopFixture struct {
	Upstream string `json:"upstream"`
	Hash     string `json:"hash"`
	Initial  struct {
		State   string `json:"state"`
		UpSpeed int64  `json:"upspeed"`
	} `json:"initial"`
	Stop struct {
		Endpoint       string `json:"endpoint"`
		LostResponse   bool   `json:"lostResponse"`
		ExternalResume string `json:"externalResume"`
	} `json:"stop"`
	Expected string `json:"expected"`
}

type qbtScopeFixture struct {
	Upstream   string   `json:"upstream"`
	Hash       string   `json:"hash"`
	Files      []string `json:"files"`
	Operations []struct {
		Endpoint    string `json:"endpoint"`
		OldPath     string `json:"oldPath"`
		NewPath     string `json:"newPath"`
		DeleteFiles *bool  `json:"deleteFiles,omitempty"`
	} `json:"operations"`
	Expected string `json:"expected"`
}

type jellyfinRefreshFixture struct {
	Upstream            string `json:"upstream"`
	Scope               string `json:"scope"`
	Endpoint            string `json:"endpoint"`
	AcceptedStatus      int    `json:"acceptedStatus"`
	InitialAvailability string `json:"initialAvailability"`
	LaterAvailability   string `json:"laterAvailability"`
	Expected            string `json:"expected"`
}

type sonarrEpisodeReadback struct {
	ID            int                        `json:"id"`
	SeriesID      int                        `json:"seriesId"`
	EpisodeFileID int                        `json:"episodeFileId"`
	EpisodeFile   *sonarrEpisodeFileReadback `json:"episodeFile,omitempty"`
}

type sonarrEpisodeFileReadback struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
}

type sonarrHistoryRecord struct {
	ID          int    `json:"id"`
	EventType   string `json:"eventType"`
	EpisodeID   int    `json:"episodeId"`
	SourceTitle string `json:"sourceTitle"`
	DownloadID  string `json:"downloadId"`
}

type expectedEpisodeFile struct {
	Path       string
	EpisodeIDs []int
}

type observedEpisodeFile struct {
	Path       string
	EpisodeIDs []int
}

func TestArrPreviewRejectionDoesNotAuthorizeCommand(t *testing.T) {
	var fixture arrRaceFixture
	readFixture(t, "arr-no-overwrite-race.json", &fixture)
	source, destination := fixture.Source, fixture.Destination
	if fixture.Upstream == "" || fixture.Expected == "" || fixture.Race.NativeCommand == "" || source == "" || destination == "" || fixture.Rejection.Path != source || len(fixture.Rejection.Rejections) != 1 {
		t.Fatalf("fixture = %#v, want explicit rejection case", fixture)
	}

	var commandCalls atomic.Int32
	files := map[string][]byte{
		source:      []byte("synthetic-source-bytes"),
		destination: []byte("synthetic-existing-library-bytes"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/manualimport":
			writeJSON(t, w, []arrPreviewCandidate{{
				Path:       fixture.Rejection.Path,
				Movie:      &arrMovie{ID: 101},
				Rejections: fixture.Rejection.Rejections,
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			commandCalls.Add(1)
			var command arrCommand
			decodeJSON(t, r, &command)
			if command.Name != fixture.Race.NativeCommand || len(command.Files) != 1 || command.Files[0].Path != source {
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
		Name:  fixture.Race.NativeCommand,
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
	var fixture arrRaceFixture
	readFixture(t, "arr-no-overwrite-race.json", &fixture)
	source, destination := fixture.Source, fixture.Destination
	if fixture.Upstream == "" || fixture.Expected == "" || fixture.Race.NativeCommand == "" || fixture.Race.ExternalReplacement == "" || source == "" || destination == "" || fixture.Preview.Path != source || len(fixture.Preview.Rejections) != 0 {
		t.Fatalf("fixture = %#v, want accepted race preview", fixture)
	}

	var filesMu sync.Mutex
	files := map[string][]byte{source: []byte("synthetic-source-bytes")}
	commandEntered := make(chan struct{})
	releaseCommand := make(chan struct{})
	var commandCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/manualimport":
			writeJSON(t, w, []arrPreviewCandidate{{Path: fixture.Preview.Path, Movie: &arrMovie{ID: 101}, Rejections: fixture.Preview.Rejections}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			commandCalls.Add(1)
			var command arrCommand
			decodeJSON(t, r, &command)
			if command.Name != fixture.Race.NativeCommand || len(command.Files) != 1 || command.Files[0].Path != source {
				t.Errorf("command = %#v, want exact synthetic %s file", command, fixture.Race.NativeCommand)
			}
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
			Name:  fixture.Race.NativeCommand,
			Files: []arrCommandFile{{Path: source, MovieID: 101}},
		}, nil)
		result <- err
	}()
	<-commandEntered

	// External writer wins the preflight race after native command dispatch.
	filesMu.Lock()
	files[destination] = []byte(fixture.Race.ExternalReplacement)
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
	var commandEvidence struct {
		ID     int    `json:"id"`
		Status string `json:"status"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/command":
			var command arrCommand
			decodeJSON(t, r, &command)
			if command.Name != "ManualImport" || len(command.Files) != 2 {
				t.Errorf("command = %#v, want two exact pack files", command)
			}
			writeJSON(t, w, map[string]any{"id": 7003, "status": "completed"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/history":
			if got := r.URL.Query().Get("page"); got != "1" {
				t.Errorf("history page = %q, want 1", got)
			}
			writeJSON(t, w, map[string]any{"records": []sonarrHistoryRecord{{
				ID: 9001, EventType: "Downloaded", EpisodeID: 301, SourceTitle: first,
				DownloadID: "synthetic-download-1",
			}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/episode":
			if got := r.URL.Query().Get("seriesId"); got != "201" {
				t.Errorf("seriesId = %q, want 201", got)
			}
			if got := r.URL.Query().Get("includeEpisodeFile"); got != "true" {
				t.Errorf("includeEpisodeFile = %q, want true", got)
			}
			writeJSON(t, w, []sonarrEpisodeReadback{
				{ID: 301, SeriesID: 201, EpisodeFileID: 801, EpisodeFile: &sonarrEpisodeFileReadback{ID: 801, Path: first}},
				{ID: 302, SeriesID: 201},
			})
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
	}, &commandEvidence)
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	if commandEvidence.ID != 7003 || commandEvidence.Status != "completed" {
		t.Fatalf("command evidence = %#v, want completed command ID", commandEvidence)
	}
	var historyEnvelope struct {
		Records []sonarrHistoryRecord `json:"records"`
	}
	getJSON(t, server.Client(), server.URL+"/api/v3/history?page=1&pageSize=100", &historyEnvelope)
	if len(historyEnvelope.Records) != 1 || historyEnvelope.Records[0].ID != 9001 || historyEnvelope.Records[0].EventType != "Downloaded" || historyEnvelope.Records[0].EpisodeID != 301 || historyEnvelope.Records[0].SourceTitle != first || historyEnvelope.Records[0].DownloadID != "synthetic-download-1" {
		t.Fatalf("history evidence = %#v, want one first-episode event", historyEnvelope.Records)
	}
	var readback []sonarrEpisodeReadback
	getJSON(t, server.Client(), server.URL+"/api/v3/episode?seriesId=201&includeEpisodeFile=true", &readback)
	observed := sonarrFileEvidence(readback)
	results := reconcileEpisodeFiles([]expectedEpisodeFile{
		{Path: first, EpisodeIDs: []int{301}},
		{Path: second, EpisodeIDs: []int{302}},
	}, observed)
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
			removed := fixture.removed
			info := qbtInfo{Hash: fixture.hash, State: fixture.state, UpSpeed: fixture.upSpeed}
			fixture.mu.Unlock()
			if removed {
				writeJSON(t, w, []qbtInfo{})
				return
			}
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
			// qBittorrent's documented delete endpoint accepts an explicit
			// boolean. The safety contract requires sending false, rather than
			// relying on omission or treating any non-true value as false.
			if got := r.PostForm.Get("deleteFiles"); got != "false" {
				http.Error(w, "deleteFiles must be the explicit false value", http.StatusBadRequest)
				return
			}
			deleteFiles := false
			fixture.mu.Lock()
			fixture.removeDeleteFiles = append(fixture.removeDeleteFiles, deleteFiles)
			if !deleteFiles {
				fixture.removed = true
			}
			fixture.mu.Unlock()
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
	var spec qbtStopFixture
	readFixture(t, "qbittorrent-stop.json", &spec)
	if spec.Upstream != "qBittorrent WebUI API v2" || spec.Hash == "" || spec.Initial.State == "" || spec.Expected == "" {
		t.Fatalf("qBittorrent stop fixture = %#v, want complete synthetic contract", spec)
	}
	fixture := &qbtFixture{
		hash:    spec.Hash,
		state:   spec.Initial.State,
		upSpeed: spec.Initial.UpSpeed,
		files:   map[string][]byte{"Synthetic Film.mkv": []byte("synthetic")},
	}
	server := newQBTServer(t, fixture)
	t.Cleanup(server.Close)

	var infos []qbtInfo
	getJSON(t, server.Client(), server.URL+"/api/v2/torrents/info?hashes="+fixture.hash, &infos)
	if len(infos) != 1 || infos[0].UpSpeed != spec.Initial.UpSpeed {
		t.Fatalf("torrent info = %#v, want zero-speed synthetic seeding state", infos)
	}
	if qbtStopped(infos[0].State) {
		t.Fatalf("state %q was incorrectly treated as stopped", infos[0].State)
	}
}

func TestQBTLostStopResponseReconcilesBeforePayloadMutation(t *testing.T) {
	var spec qbtStopFixture
	readFixture(t, "qbittorrent-stop.json", &spec)
	if spec.Stop.Endpoint != "/api/v2/torrents/stop" || !spec.Stop.LostResponse || spec.Stop.ExternalResume == "" {
		t.Fatalf("qBittorrent stop fixture = %#v, want lost-response contract", spec)
	}
	fixture := &qbtFixture{
		hash:             spec.Hash,
		state:            spec.Initial.State,
		upSpeed:          spec.Initial.UpSpeed,
		files:            map[string][]byte{"Synthetic Film.mkv": []byte("synthetic")},
		lostStopResponse: spec.Stop.LostResponse,
	}
	server := newQBTServer(t, fixture)
	t.Cleanup(server.Close)

	_, err := postForm(t, server.Client(), server.URL+spec.Stop.Endpoint, url.Values{"hashes": {fixture.hash}})
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
	fixture.state = spec.Stop.ExternalResume
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
	var spec qbtScopeFixture
	readFixture(t, "qbittorrent-scope.json", &spec)
	if spec.Upstream != "qBittorrent WebUI API v2" || spec.Hash == "" || spec.Expected == "" || len(spec.Files) != 3 || len(spec.Operations) != 2 {
		t.Fatalf("qBittorrent scope fixture = %#v, want three files and two operations", spec)
	}
	deleteOperation := spec.Operations[1]
	if deleteOperation.Endpoint != "/api/v2/torrents/delete" || deleteOperation.DeleteFiles == nil || *deleteOperation.DeleteFiles {
		t.Fatalf("delete operation = %#v, want documented delete route with deleteFiles=false", deleteOperation)
	}
	fixture := &qbtFixture{
		hash:  spec.Hash,
		state: "stalledUP",
		files: syntheticQBTFiles(t, spec.Files),
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
	if _, err := postForm(t, server.Client(), server.URL+deleteOperation.Endpoint, url.Values{
		"hashes":      {fixture.hash},
		"deleteFiles": {"false"},
	}); err != nil {
		t.Fatalf("metadata-only remove: %v", err)
	}
	var inventory []qbtInfo
	getJSON(t, server.Client(), server.URL+"/api/v2/torrents/info?hashes="+fixture.hash, &inventory)
	if len(inventory) != 0 {
		t.Fatalf("torrent inventory after metadata removal = %#v, want record removed", inventory)
	}

	fixture.mu.Lock()
	deferred := cloneFileMap(fixture.files)
	removed := fixture.removed
	deleteFlags := append([]bool(nil), fixture.removeDeleteFiles...)
	fixture.mu.Unlock()
	if !removed || len(deleteFlags) != 1 || deleteFlags[0] {
		t.Fatalf("remove state removed=%v deleteFlags=%v, want metadata-only", removed, deleteFlags)
	}
	if len(deferred) != len(spec.Files) {
		t.Fatalf("payload after metadata removal = %#v, want all synthetic files retained", deferred)
	}
	for _, absolute := range spec.Files {
		relative := qbtRelativePath(t, absolute)
		if !bytes.Equal(deferred[relative], []byte("synthetic payload:"+absolute)) {
			t.Fatalf("payload %q after metadata removal = %q, want retained synthetic bytes", relative, deferred[relative])
		}
	}
}

func TestQBTNativeRenameRequiresWholeTorrentAndReadback(t *testing.T) {
	var spec qbtScopeFixture
	readFixture(t, "qbittorrent-scope.json", &spec)
	if spec.Upstream != "qBittorrent WebUI API v2" || spec.Hash == "" || spec.Expected == "" || len(spec.Files) < 3 || len(spec.Operations) < 1 {
		t.Fatalf("qBittorrent scope fixture = %#v, want rename scope contract", spec)
	}
	folderOperation := spec.Operations[0]
	if folderOperation.Endpoint != "/api/v2/torrents/renameFolder" || folderOperation.OldPath == "" || folderOperation.NewPath == "" {
		t.Fatalf("rename operation = %#v, want documented folder rename route", folderOperation)
	}
	all := qbtRelativePaths(t, spec.Files)
	fixture := &qbtFixture{
		hash:  spec.Hash,
		state: "pausedUP",
		files: syntheticQBTFiles(t, spec.Files),
	}
	server := newQBTServer(t, fixture)
	t.Cleanup(server.Close)

	if qbtNativeScopeAllowed(all[:1], all) {
		t.Fatal("native folder rename must refuse selected-file scope")
	}
	if _, err := postForm(t, server.Client(), server.URL+folderOperation.Endpoint, url.Values{
		"hash":    {fixture.hash},
		"oldPath": {folderOperation.OldPath},
		"newPath": {folderOperation.NewPath},
	}); err != nil {
		t.Fatalf("whole-torrent folder rename: %v", err)
	}
	var renamed []qbtFile
	getJSON(t, server.Client(), server.URL+"/api/v2/torrents/files?hash="+fixture.hash, &renamed)
	want := qbtRenamedPaths(all, folderOperation.OldPath, folderOperation.NewPath)
	if got := qbtFileNames(renamed); !sameStringSet(got, want) {
		t.Fatalf("renamed paths = %#v, want %#v", got, want)
	}

	before := cloneFileMapLocked(fixture)
	if _, err := postForm(t, server.Client(), server.URL+"/api/v2/torrents/renameFile", url.Values{
		"hash":    {fixture.hash},
		"oldPath": {want[0]},
		"newPath": {want[len(want)-1]},
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
	var spec jellyfinRefreshFixture
	readFixture(t, "jellyfin-refresh.json", &spec)
	if spec.Upstream != "Jellyfin library refresh candidate" || spec.Scope != "library" || spec.Endpoint == "" || spec.AcceptedStatus == 0 || spec.InitialAvailability == "" || spec.LaterAvailability == "" || spec.Expected == "" {
		t.Fatalf("Jellyfin refresh fixture = %#v, want complete synthetic contract", spec)
	}
	var refreshCalls atomic.Int32
	var visible atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == spec.Endpoint:
			refreshCalls.Add(1)
			w.WriteHeader(spec.AcceptedStatus)
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

	response, err := http.Post(server.URL+spec.Endpoint, "application/json", nil)
	if err != nil {
		t.Fatalf("library refresh: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != spec.AcceptedStatus {
		t.Fatalf("refresh status = %d, want %d", response.StatusCode, spec.AcceptedStatus)
	}
	var before map[string][]jellyfinItem
	getJSON(t, server.Client(), server.URL+"/Items", &before)
	if spec.InitialAvailability != "absent" || len(before["Items"]) != 0 {
		t.Fatalf("availability immediately after refresh = %#v, want absent", before)
	}
	visible.Store(true)
	var after map[string][]jellyfinItem
	getJSON(t, server.Client(), server.URL+"/Items", &after)
	if spec.LaterAvailability != "present" || len(after["Items"]) != 1 || after["Items"][0].ID != "jf-101" {
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
	Expected  string          `json:"expected"`
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
	if fixture.Video.Path == "" || fixture.Expected == "" || len(fixture.Video.EpisodeIDs) != len(fixture.Video.AbsoluteNumbers) || len(fixture.Video.EpisodeIDs) != 2 {
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

func sonarrFileEvidence(episodes []sonarrEpisodeReadback) map[string]observedEpisodeFile {
	observed := make(map[string]observedEpisodeFile)
	for _, episode := range episodes {
		if episode.ID == 0 || episode.SeriesID == 0 || episode.EpisodeFile == nil || episode.EpisodeFile.Path == "" {
			continue
		}
		if episode.EpisodeFileID == 0 || episode.EpisodeFile.ID != episode.EpisodeFileID {
			continue
		}
		file := observed[episode.EpisodeFile.Path]
		file.Path = episode.EpisodeFile.Path
		file.EpisodeIDs = append(file.EpisodeIDs, episode.ID)
		observed[episode.EpisodeFile.Path] = file
	}
	return observed
}

func reconcileEpisodeFiles(expected []expectedEpisodeFile, observed map[string]observedEpisodeFile) map[string]string {
	result := make(map[string]string, len(expected))
	for _, file := range expected {
		actual, ok := observed[file.Path]
		if ok && sameIntSet(file.EpisodeIDs, actual.EpisodeIDs) {
			result[file.Path] = "applied"
		} else {
			result[file.Path] = "unresolved"
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

const syntheticQBTDownloadRoot = "/synthetic/downloads/"

func qbtRelativePath(t *testing.T, absolute string) string {
	t.Helper()
	relative := strings.TrimPrefix(absolute, syntheticQBTDownloadRoot)
	if relative == absolute || relative == "" || strings.HasPrefix(relative, "/") {
		t.Fatalf("qBittorrent fixture path %q is outside synthetic download root", absolute)
	}
	return relative
}

func qbtRelativePaths(t *testing.T, absolute []string) []string {
	t.Helper()
	relative := make([]string, 0, len(absolute))
	for _, path := range absolute {
		relative = append(relative, qbtRelativePath(t, path))
	}
	return relative
}

func syntheticQBTFiles(t *testing.T, absolute []string) map[string][]byte {
	t.Helper()
	files := make(map[string][]byte, len(absolute))
	for _, path := range absolute {
		relative := qbtRelativePath(t, path)
		if _, exists := files[relative]; exists {
			t.Fatalf("qBittorrent fixture repeats file path %q", path)
		}
		files[relative] = []byte("synthetic payload:" + path)
	}
	return files
}

func qbtRenamedPaths(names []string, oldPath, newPath string) []string {
	renamed := make([]string, 0, len(names))
	for _, name := range names {
		if name == oldPath || strings.HasPrefix(name, oldPath+"/") {
			suffix := strings.TrimPrefix(name, oldPath)
			renamed = append(renamed, strings.TrimPrefix(newPath+suffix, "/"))
			continue
		}
		renamed = append(renamed, name)
	}
	return renamed
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

func sameIntSet(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[int]int, len(left))
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
