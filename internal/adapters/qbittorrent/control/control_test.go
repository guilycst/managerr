package control

import (
	"context"
	"errors"
	"path"
	"strings"
	"sync"
	"testing"

	native "github.com/guilycst/mastarr/clients/qbittorrent"
	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

const testConnection = domain.ConfigID("qbt-main")

const testHash = "0123456789abcdef0123456789abcdef01234567"

type fixtureUpstream struct {
	mu sync.Mutex

	torrent native.Torrent
	files   []native.TorrentFile
	removed bool

	stopCalls         int
	setLocationCalls  int
	renameFileCalls   int
	renameFolderCalls int
	deleteCalls       int
	deleteFiles       []bool
	lastLocation      string
	lastOldPath       string
	lastNewPath       string

	stopError         error
	setLocationError  error
	renameFileError   error
	renameFolderError error
	deleteError       error
	listError         error
	filesError        error
}

func newFixtureUpstream(state, contentPath string, names ...string) *fixtureUpstream {
	files := make([]native.TorrentFile, 0, len(names))
	for index, name := range names {
		files = append(files, native.TorrentFile{Index: int64(index), Name: name, Size: int64(index + 1)})
	}
	return &fixtureUpstream{
		torrent: native.Torrent{Hash: testHash, State: state, ContentPath: contentPath, UpSpeed: 0},
		files:   files,
	}
}

func (fixture *fixtureUpstream) ListTorrents(ctx context.Context, options native.TorrentListOptions) ([]native.Torrent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.listError != nil {
		return nil, fixture.listError
	}
	if fixture.removed {
		return []native.Torrent{}, nil
	}
	return []native.Torrent{fixture.torrent}, nil
}

func (fixture *fixtureUpstream) GetTorrentFiles(ctx context.Context, hash string) ([]native.TorrentFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.filesError != nil {
		return nil, fixture.filesError
	}
	return append([]native.TorrentFile(nil), fixture.files...), nil
}

func (fixture *fixtureUpstream) Stop(ctx context.Context, hash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.stopCalls++
	fixture.torrent.State = "pausedUP"
	return fixture.stopError
}

func (fixture *fixtureUpstream) SetLocation(ctx context.Context, hash, location string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.setLocationCalls++
	fixture.lastLocation = location
	fixture.torrent.ContentPath = location
	return fixture.setLocationError
}

func (fixture *fixtureUpstream) RenameFile(ctx context.Context, hash, oldPath, newPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.renameFileCalls++
	fixture.lastOldPath = oldPath
	fixture.lastNewPath = newPath
	for index := range fixture.files {
		if fixture.files[index].Name == oldPath {
			fixture.files[index].Name = newPath
		}
	}
	return fixture.renameFileError
}

func (fixture *fixtureUpstream) RenameFolder(ctx context.Context, hash, oldPath, newPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.renameFolderCalls++
	fixture.lastOldPath = oldPath
	fixture.lastNewPath = newPath
	for index := range fixture.files {
		if !strings.HasPrefix(fixture.files[index].Name, oldPath+"/") {
			continue
		}
		fixture.files[index].Name = path.Join(newPath, strings.TrimPrefix(fixture.files[index].Name, oldPath+"/"))
	}
	fixture.torrent.ContentPath = path.Join(path.Dir(fixture.torrent.ContentPath), newPath)
	return fixture.renameFolderError
}

func (fixture *fixtureUpstream) Delete(ctx context.Context, hash string, deleteFiles bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	fixture.deleteCalls++
	fixture.deleteFiles = append(fixture.deleteFiles, deleteFiles)
	if !deleteFiles {
		fixture.removed = true
	}
	return fixture.deleteError
}

func newFixtureClient(t *testing.T, fixture Upstream) *Client {
	t.Helper()
	client, err := New(Config{
		ConnectionID:       testConnection,
		Mappings:           []domain.PathMapping{{ConnectionID: testConnection, RootID: "library", SourcePrefix: "/downloads", DestinationPrefix: "managed"}},
		WriteCapability:    domain.CapabilitySupported,
		CapabilityVersion:  "synthetic-qbt-5.0",
		CapabilityEvidence: []string{"synthetic write fixture"},
	}, fixture)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func assertCode(t *testing.T, err error, want domain.UpstreamErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", want)
	}
	var upstream domain.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("error %T does not carry normalized upstream evidence: %v", err, err)
	}
	if upstream.Code != want {
		t.Fatalf("error code = %s, want %s", upstream.Code, want)
	}
}

func assertZeroEffect(t *testing.T, effect ports.ClientEffect) {
	t.Helper()
	if effect.OperationID != "" || effect.Outcome != "" || !effect.ObservedAt.IsZero() || len(effect.Evidence) != 0 {
		t.Fatalf("effect = %#v, want zero effect", effect)
	}
}

func TestObserveKeepsZeroSpeedSeedingDistinctFromStopped(t *testing.T) {
	fixture := newFixtureUpstream("stalledUP", "/downloads/Synthetic Film.mkv", "Synthetic Film.mkv")
	client := newFixtureClient(t, fixture)

	observation, err := client.Observe(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash})
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if observation.State != "stalledUP" || !observation.Seeding {
		t.Fatalf("observation = %#v, want seeding stalledUP", observation)
	}
	if len(observation.Payload) != 1 || observation.Payload[0].RelativePath != "managed/Synthetic Film.mkv" {
		t.Fatalf("payload = %#v, want mapped synthetic film", observation.Payload)
	}

	effect, err := client.Stop(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if effect.Outcome != domain.OutcomeApplied {
		t.Fatalf("stop effect = %#v, want applied", effect)
	}
	fixture.mu.Lock()
	stopCalls := fixture.stopCalls
	fixture.mu.Unlock()
	if stopCalls != 1 {
		t.Fatalf("stop calls = %d, want one", stopCalls)
	}
}

func TestStopLostResponseResolvesFromReadBackWithoutRetry(t *testing.T) {
	fixture := newFixtureUpstream("uploading", "/downloads/Synthetic Film.mkv", "Synthetic Film.mkv")
	fixture.stopError = native.UpstreamError{Code: native.ErrorUnavailable, Retryable: true}
	client := newFixtureClient(t, fixture)

	effect, err := client.Stop(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash})
	if err != nil {
		t.Fatalf("Stop with lost response: %v", err)
	}
	if effect.Outcome != domain.OutcomeApplied {
		t.Fatalf("effect = %#v, want applied from stopped read-back", effect)
	}
	fixture.mu.Lock()
	stopCalls := fixture.stopCalls
	fixture.mu.Unlock()
	if stopCalls != 1 {
		t.Fatalf("stop calls = %d, want no blind retry", stopCalls)
	}
}

func TestStopExternalResumeRemainsUnknownAndIsNotRetried(t *testing.T) {
	fixture := newFixtureUpstream("uploading", "/downloads/Synthetic Film.mkv", "Synthetic Film.mkv")
	client := newFixtureClient(t, fixture)

	if _, err := client.Stop(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash}); err != nil {
		t.Fatalf("initial Stop: %v", err)
	}
	fixture.mu.Lock()
	fixture.torrent.State = "stalledUP"
	fixture.mu.Unlock()

	effect, err := client.Remove(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash})
	assertZeroEffect(t, effect)
	assertCode(t, err, domain.OutcomeConflict)
	fixture.mu.Lock()
	stopCalls, deleteCalls := fixture.stopCalls, fixture.deleteCalls
	fixture.mu.Unlock()
	if stopCalls != 1 || deleteCalls != 0 {
		t.Fatalf("calls stop=%d delete=%d, want stop=1 delete=0", stopCalls, deleteCalls)
	}
}

func TestStopAlreadySatisfiedDoesNotRequireWriteCapability(t *testing.T) {
	fixture := newFixtureUpstream("pausedUP", "/downloads/Synthetic Film.mkv", "Synthetic Film.mkv")
	client, err := New(Config{ConnectionID: testConnection, Mappings: []domain.PathMapping{{ConnectionID: testConnection, RootID: "library", SourcePrefix: "/downloads", DestinationPrefix: "managed"}}}, fixture)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	effect, err := client.Stop(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if effect.Outcome != domain.OutcomeAlreadySatisfied {
		t.Fatalf("effect = %#v, want already satisfied", effect)
	}
	fixture.mu.Lock()
	stopCalls := fixture.stopCalls
	fixture.mu.Unlock()
	if stopCalls != 0 {
		t.Fatalf("stop calls = %d, want zero", stopCalls)
	}
}

func TestRemoveIsStoppedMetadataOnlyAndRetainsPayload(t *testing.T) {
	fixture := newFixtureUpstream("pausedUP", "/downloads/Synthetic Pack", "Synthetic Pack/one.mkv", "Synthetic Pack/two.mkv", "Synthetic Pack/sample.txt")
	client := newFixtureClient(t, fixture)

	effect, err := client.Remove(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if effect.Outcome != domain.OutcomeApplied {
		t.Fatalf("effect = %#v, want applied", effect)
	}
	fixture.mu.Lock()
	removed, deleteCalls := fixture.removed, fixture.deleteCalls
	deleteFiles := append([]bool(nil), fixture.deleteFiles...)
	payloadCount := len(fixture.files)
	fixture.mu.Unlock()
	if !removed || deleteCalls != 1 || len(deleteFiles) != 1 || deleteFiles[0] {
		t.Fatalf("remove calls=%d flags=%v removed=%v, want one deleteFiles=false", deleteCalls, deleteFiles, removed)
	}
	if payloadCount != 3 {
		t.Fatalf("payload count = %d, want all three retained", payloadCount)
	}
}

func TestRemoveRejectsActiveTorrentWithoutStoppingOrDeleting(t *testing.T) {
	fixture := newFixtureUpstream("stalledUP", "/downloads/Synthetic Film.mkv", "Synthetic Film.mkv")
	client := newFixtureClient(t, fixture)

	effect, err := client.Remove(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash})
	assertZeroEffect(t, effect)
	assertCode(t, err, domain.OutcomeConflict)
	fixture.mu.Lock()
	stopCalls, deleteCalls := fixture.stopCalls, fixture.deleteCalls
	fixture.mu.Unlock()
	if stopCalls != 0 || deleteCalls != 0 {
		t.Fatalf("calls stop=%d delete=%d, want no implicit stop/delete", stopCalls, deleteCalls)
	}
}

func TestRemoveLostResponseResolvesFromAbsentRecord(t *testing.T) {
	fixture := newFixtureUpstream("pausedUP", "/downloads/Synthetic Film.mkv", "Synthetic Film.mkv")
	fixture.deleteError = native.UpstreamError{Code: native.ErrorUnavailable, Retryable: true}
	client := newFixtureClient(t, fixture)

	effect, err := client.Remove(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash})
	if err != nil {
		t.Fatalf("Remove with lost response: %v", err)
	}
	if effect.Outcome != domain.OutcomeApplied {
		t.Fatalf("effect = %#v, want applied from absent read-back", effect)
	}
	fixture.mu.Lock()
	deleteCalls := fixture.deleteCalls
	fixture.mu.Unlock()
	if deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want no retry", deleteCalls)
	}
}

func TestControlCapabilityBlocksWritesButKeepsObserve(t *testing.T) {
	fixture := newFixtureUpstream("uploading", "/downloads/Synthetic Film.mkv", "Synthetic Film.mkv")
	client := newFixtureClient(t, fixture)
	client.writeCapability = domain.CapabilityUnknown

	if _, err := client.Observe(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash}); err != nil {
		t.Fatalf("Observe with blocked writes: %v", err)
	}
	effect, err := client.Stop(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash})
	assertZeroEffect(t, effect)
	assertCode(t, err, domain.OutcomeUnsupported)
	fixture.mu.Lock()
	stopCalls := fixture.stopCalls
	fixture.mu.Unlock()
	if stopCalls != 0 {
		t.Fatalf("stop calls = %d, want zero while capability blocked", stopCalls)
	}
}

func TestNativeErrorsAreNormalizedAtControlBoundary(t *testing.T) {
	fixture := newFixtureUpstream("pausedUP", "/downloads/Synthetic Film.mkv", "Synthetic Film.mkv")
	fixture.listError = native.UpstreamError{Code: native.ErrorUnauthorized, Status: 401}
	client := newFixtureClient(t, fixture)

	_, err := client.Observe(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash})
	assertCode(t, err, domain.OutcomeUnauthorized)
	var upstream domain.UpstreamError
	if !errors.As(err, &upstream) || upstream.Operation != operationObserve || upstream.Status != 401 {
		t.Fatalf("normalized error = %#v, want operation/status preserved without body", upstream)
	}
}

func TestRenameFileRejectsCollisionAndReadsBackExactPath(t *testing.T) {
	fixture := newFixtureUpstream("pausedUP", "/downloads/Synthetic Pack", "Synthetic Pack/one.mkv", "Synthetic Pack/two.mkv")
	client := newFixtureClient(t, fixture)
	source := domain.FileTarget{RootID: "library", RelativePath: "managed/Synthetic Pack/one.mkv"}
	destinationCollision := domain.FileTarget{RootID: "library", RelativePath: "managed/Synthetic Pack/two.mkv"}

	effect, err := client.RenameFile(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash}, source, "two.mkv")
	assertZeroEffect(t, effect)
	assertCode(t, err, domain.OutcomeConflict)
	fixture.mu.Lock()
	fileCalls := fixture.renameFileCalls
	fixture.mu.Unlock()
	if fileCalls != 0 {
		t.Fatalf("rename file calls = %d, want zero on collision", fileCalls)
	}

	effect, err = client.RenameFile(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash}, source, "renamed.mkv")
	if err != nil {
		t.Fatalf("RenameFile: %v", err)
	}
	if effect.Outcome != domain.OutcomeApplied {
		t.Fatalf("effect = %#v, want applied", effect)
	}
	fixture.mu.Lock()
	oldPath, newPath := fixture.lastOldPath, fixture.lastNewPath
	fixture.mu.Unlock()
	if oldPath != "Synthetic Pack/one.mkv" || newPath != "Synthetic Pack/renamed.mkv" {
		t.Fatalf("native rename paths = %q, %q", oldPath, newPath)
	}
	if destinationCollision.RelativePath == "" {
		t.Fatal("collision target was not constructed")
	}
}

func TestRenameFolderRequiresWholeTorrentScopeAndReadsBack(t *testing.T) {
	fixture := newFixtureUpstream("pausedUP", "/downloads/Synthetic Pack", "Synthetic Pack/one.mkv", "Synthetic Pack/two.mkv")
	client := newFixtureClient(t, fixture)
	source := domain.FileTarget{RootID: "library", RelativePath: "managed/Synthetic Pack"}

	fixture.mu.Lock()
	fixture.files = append(fixture.files, native.TorrentFile{Index: 2, Name: "outside.txt", Size: 3})
	fixture.mu.Unlock()
	effect, err := client.RenameFolder(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash}, source, "Renamed Pack")
	assertZeroEffect(t, effect)
	assertCode(t, err, domain.OutcomeConflict)
	fixture.mu.Lock()
	folderCalls := fixture.renameFolderCalls
	fixture.mu.Unlock()
	if folderCalls != 0 {
		t.Fatalf("rename folder calls = %d, want zero on scope expansion", folderCalls)
	}

	fixture.mu.Lock()
	fixture.files = fixture.files[:2]
	fixture.mu.Unlock()
	effect, err = client.RenameFolder(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash}, source, "Renamed Pack")
	if err != nil {
		t.Fatalf("RenameFolder: %v", err)
	}
	if effect.Outcome != domain.OutcomeApplied {
		t.Fatalf("effect = %#v, want applied", effect)
	}
	fixture.mu.Lock()
	oldPath, newPath := fixture.lastOldPath, fixture.lastNewPath
	fixture.mu.Unlock()
	if oldPath != "Synthetic Pack" || newPath != "Renamed Pack" {
		t.Fatalf("native folder paths = %q, %q", oldPath, newPath)
	}
}

func TestRelocateReadsBackExactContentPath(t *testing.T) {
	fixture := newFixtureUpstream("pausedUP", "/downloads/Synthetic Film.mkv", "Synthetic Film.mkv")
	client := newFixtureClient(t, fixture)
	destination := domain.FileTarget{RootID: "library", RelativePath: "managed/relocated/Synthetic Film.mkv"}

	effect, err := client.Relocate(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash}, destination)
	if err != nil {
		t.Fatalf("Relocate: %v", err)
	}
	if effect.Outcome != domain.OutcomeApplied {
		t.Fatalf("effect = %#v, want applied", effect)
	}
	fixture.mu.Lock()
	location, calls := fixture.lastLocation, fixture.setLocationCalls
	fixture.mu.Unlock()
	if location != "/downloads/relocated/Synthetic Film.mkv" || calls != 1 {
		t.Fatalf("setLocation = %q calls=%d, want mapped location once", location, calls)
	}
}

func TestLostPayloadWriteIsUnknownWithoutBlindRetry(t *testing.T) {
	fixture := newFixtureUpstream("pausedUP", "/downloads/Synthetic Film.mkv", "Synthetic Film.mkv")
	fixture.renameFileError = native.UpstreamError{Code: native.ErrorUnavailable, Retryable: true}
	client := newFixtureClient(t, fixture)
	source := domain.FileTarget{RootID: "library", RelativePath: "managed/Synthetic Film.mkv"}

	effect, err := client.RenameFile(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash}, source, "renamed.mkv")
	assertZeroEffect(t, effect)
	assertCode(t, err, domain.OutcomeUnknown)
	fixture.mu.Lock()
	fileCalls := fixture.renameFileCalls
	fixture.mu.Unlock()
	if fileCalls != 1 {
		t.Fatalf("rename file calls = %d, want one uncertain dispatch", fileCalls)
	}
}

func TestMissingRecordRemoveIsAlreadySatisfied(t *testing.T) {
	fixture := newFixtureUpstream("pausedUP", "/downloads/Synthetic Film.mkv", "Synthetic Film.mkv")
	fixture.removed = true
	client := newFixtureClient(t, fixture)

	effect, err := client.Remove(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash})
	if err != nil {
		t.Fatalf("Remove absent record: %v", err)
	}
	if effect.Outcome != domain.OutcomeAlreadySatisfied {
		t.Fatalf("effect = %#v, want already satisfied", effect)
	}
	fixture.mu.Lock()
	deleteCalls := fixture.deleteCalls
	fixture.mu.Unlock()
	if deleteCalls != 0 {
		t.Fatalf("delete calls = %d, want zero", deleteCalls)
	}
}

func TestMappingsRejectTraversalAndAmbiguousDestination(t *testing.T) {
	fixture := newFixtureUpstream("pausedUP", "/downloads/Synthetic Film.mkv", "Synthetic Film.mkv")
	if _, err := New(Config{ConnectionID: testConnection, Mappings: []domain.PathMapping{{ConnectionID: testConnection, RootID: "library", SourcePrefix: "/downloads/../other"}}}, fixture); err == nil {
		t.Fatal("New accepted traversal mapping")
	}
	client, err := New(Config{
		ConnectionID: testConnection,
		Mappings: []domain.PathMapping{
			{ConnectionID: testConnection, RootID: "library", SourcePrefix: "/downloads", DestinationPrefix: "managed"},
			{ConnectionID: testConnection, RootID: "library", SourcePrefix: "/elsewhere", DestinationPrefix: "managed"},
		},
		WriteCapability: domain.CapabilitySupported, CapabilityVersion: "synthetic", CapabilityEvidence: []string{"fixture"},
	}, fixture)
	if err != nil {
		t.Fatalf("New ambiguous mapping fixture: %v", err)
	}
	effect, err := client.Relocate(context.Background(), ports.DownloadRef{ConnectionID: testConnection, ExternalID: testHash}, domain.FileTarget{RootID: "library", RelativePath: "managed/Synthetic Film.mkv"})
	assertZeroEffect(t, effect)
	assertCode(t, err, domain.OutcomeConflict)
	fixture.mu.Lock()
	setLocationCalls := fixture.setLocationCalls
	fixture.mu.Unlock()
	if setLocationCalls != 0 {
		t.Fatalf("setLocation calls = %d, want zero for ambiguous destination", setLocationCalls)
	}
}
