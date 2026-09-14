package discovery

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

const (
	testRoot       domain.ConfigID  = "downloads"
	testConnection domain.ConfigID  = "qbt-main"
	testRuntime    domain.RuntimeID = "00000000-0000-4000-8000-000000000001"
)

func TestScannerGroupsMediaRetainsCompanionsAndExcludesAppPaths(t *testing.T) {
	clock := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	filesystem := newFixtureFilesystem(testRoot, clock)
	completedAt := clock.Add(-5 * time.Minute)
	inventory := &fixtureInventory{items: []ports.DownloadItem{{
		ExternalID: "torrent-1", Protocol: "torrent", State: "completed", ProcessingDone: true,
		Hash: "abc123", CompletedAt: &completedAt,
		Descriptor: &ports.DescriptorObservation{ID: testRuntime, Kind: "torrent", Available: true},
		Payload:    []domain.FileManifestEntry{fixtureFile(testRoot, "Movie/Movie.mkv", domain.ManifestFile, domain.RoleVideo, 100, "movie")},
	}}}
	store := NewMemoryStore()
	options := Options{PageSize: 2, MinimumStableSpacing: 30 * time.Second, Now: func() time.Time { return clock }}
	scanner, err := New(filesystem, store, []DownloadSource{{ConnectionID: testConnection, Inventory: inventory}}, options)
	if err != nil {
		t.Fatal(err)
	}

	first, err := scanner.Scan(context.Background(), testRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Persisted || first.Coverage.Completeness != domain.CompletenessComplete {
		t.Fatalf("first scan was not a complete persisted observation: %+v", first)
	}
	if len(first.Observation.Entries) != 17 {
		t.Fatalf("excluded paths were not removed from the exact observation: got %d entries", len(first.Observation.Entries))
	}
	for _, entry := range first.Observation.Entries {
		if entry.RelativePath == ".mastarr-trash" || len(entry.RelativePath) >= len(".mastarr-trash/") && entry.RelativePath[:len(".mastarr-trash/")] == ".mastarr-trash/" {
			t.Fatalf("trash entry leaked into observation: %q", entry.RelativePath)
		}
	}
	if len(first.Discoveries) != 3 {
		t.Fatalf("expected movie, season pack and anime groups, got %d", len(first.Discoveries))
	}

	movie := discoveryByPath(t, first.Discoveries, "Movie")
	if movie.Kind != GroupMovie || movie.ProvenanceState != ProvenanceKnown || movie.ClientCompletion.State != ClientCompletionComplete {
		t.Fatalf("unexpected movie evidence: %+v", movie)
	}
	if len(movie.ClientCoverage) != 1 || movie.ClientCoverage[0].ConnectionID != testConnection || movie.ClientCoverage[0].RootID != testRoot || len(movie.Provenance) != 1 || movie.Provenance[0].DescriptorID != testRuntime {
		t.Fatalf("client coverage or descriptor provenance was not retained: coverage=%+v provenance=%+v", movie.ClientCoverage, movie.Provenance)
	}
	if movie.Stability.State != StabilityUnknown || movie.Readiness != domain.ReadinessUnknown {
		t.Fatalf("first observation must not claim stable/ready: stability=%+v readiness=%q", movie.Stability, movie.Readiness)
	}
	if !hasReview(movie.ReviewReasons, ReviewStabilityUnknown) || !hasReview(movie.ReviewReasons, ReviewUnsupportedCompanion) {
		t.Fatalf("movie review reasons lost stability or unsupported companion evidence: %v", movie.ReviewReasons)
	}
	if len(movie.Subtitles) != 4 {
		t.Fatalf("expected four subtitle files including IDX/SUB, got %d", len(movie.Subtitles))
	}
	forced, hearingImpaired, paired := false, false, 0
	for _, subtitle := range movie.Subtitles {
		forced = forced || subtitle.Forced
		hearingImpaired = hearingImpaired || subtitle.HearingImpaired
		if subtitle.PairID != "" {
			paired++
			if subtitle.PairID != "Movie/Movie" {
				t.Fatalf("IDX/SUB pair identity changed: %q", subtitle.PairID)
			}
		}
	}
	if !forced || !hearingImpaired || paired != 2 {
		t.Fatalf("subtitle labels/pair were not preserved: %+v", movie.Subtitles)
	}

	pack := discoveryByPath(t, first.Discoveries, "Pack")
	if pack.Kind != GroupSeasonPack || len(pack.Videos) != 2 {
		t.Fatalf("season pack grouping failed: kind=%q videos=%d", pack.Kind, len(pack.Videos))
	}
	for _, video := range pack.Videos {
		if video.Kind != domain.MediaEpisode || video.Confidence != ConfidenceExact || len(video.EpisodeNumbers) != 1 {
			t.Fatalf("episode association is not exact: %+v", video)
		}
	}
	if pack.ProvenanceState != ProvenanceUnknown || !hasReview(pack.ReviewReasons, ReviewOrphanProvenance) {
		t.Fatalf("orphan provenance was not explicit: %+v", pack)
	}

	anime := discoveryByPath(t, first.Discoveries, "Anime")
	if anime.Kind != GroupAnime || len(anime.Videos) != 1 || anime.Videos[0].Confidence != ConfidenceUnresolved || !hasReview(anime.ReviewReasons, ReviewAnimeMappingRequired) {
		t.Fatalf("anime absolute numbering was overclaimed: %+v", anime)
	}

	clock = clock.Add(31 * time.Second)
	second, err := scanner.Scan(context.Background(), testRoot)
	if err != nil {
		t.Fatal(err)
	}
	secondMovie := discoveryByPath(t, second.Discoveries, "Movie")
	if secondMovie.ID != movie.ID || secondMovie.ManifestRevision != movie.ManifestRevision {
		t.Fatalf("unchanged group identity/revision was not durable: first=%q/%q second=%q/%q", movie.ID, movie.ManifestRevision, secondMovie.ID, secondMovie.ManifestRevision)
	}
	if secondMovie.Stability.State != StabilityStable || secondMovie.Readiness != domain.ReadinessReady {
		t.Fatalf("completed unchanged movie did not become ready after spacing: stability=%q readiness=%q", secondMovie.Stability.State, secondMovie.Readiness)
	}
	if !secondMovie.FirstSeenAt.Equal(firstMovieFirstSeen(t, first.Discoveries, "Movie")) {
		t.Fatalf("first seen timestamp changed across scans")
	}
}

func TestMemoryStoreDoesNotRetireOnPartialCoverageAndRetiresOnCompleteAbsence(t *testing.T) {
	clock := time.Date(2026, 9, 14, 13, 0, 0, 0, time.UTC)
	filesystem := newFixtureFilesystem(testRoot, clock)
	store := NewMemoryStore()
	scanner, err := New(filesystem, store, nil, Options{Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scanner.Scan(context.Background(), testRoot); err != nil {
		t.Fatal(err)
	}

	filesystem.partial = true
	filesystem.setRootFiles(fixtureFile(testRoot, "Movie/Movie.mkv", domain.ManifestFile, domain.RoleVideo, 100, "movie"))
	clock = clock.Add(time.Minute)
	if _, err := scanner.Scan(context.Background(), testRoot); err != nil {
		t.Fatal(err)
	}
	partial, err := store.ListDiscoveries(context.Background(), testRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(partial) != 3 {
		t.Fatalf("partial coverage incorrectly retired groups: got %d", len(partial))
	}

	filesystem.partial = false
	filesystem.setRootFiles()
	clock = clock.Add(time.Minute)
	result, err := scanner.Scan(context.Background(), testRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Discoveries) != 0 {
		t.Fatalf("empty complete scan produced groups: %+v", result.Discoveries)
	}
	active, err := store.ListDiscoveries(context.Background(), testRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Fatalf("complete absence did not retire old groups: %d", len(active))
	}
	history, err := store.ListDiscoveries(context.Background(), testRoot, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("retired groups were not retained in history: %d", len(history))
	}
	for _, discovery := range history {
		if discovery.Active || !hasReview(discovery.ReviewReasons, ReviewNotSeenInCompleteScan) {
			t.Fatalf("retirement state was not explicit: %+v", discovery)
		}
	}
}

func TestScannerPersistsUnknownCoverageWhenRootReadFails(t *testing.T) {
	clock := time.Date(2026, 9, 14, 14, 0, 0, 0, time.UTC)
	filesystem := newFixtureFilesystem(testRoot, clock)
	filesystem.rootErr = errors.New("synthetic mount unavailable")
	store := NewMemoryStore()
	scanner, err := New(filesystem, store, nil, Options{Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := scanner.Scan(context.Background(), testRoot)
	if err == nil || !result.Persisted || result.Coverage.Completeness != domain.CompletenessUnknown {
		t.Fatalf("root failure did not preserve unknown evidence: result=%+v err=%v", result, err)
	}
	history, err := store.ListDirectoryObservations(context.Background(), testRoot)
	if err != nil || len(history) != 1 || history[0].Coverage.Completeness != domain.CompletenessUnknown {
		t.Fatalf("unknown root observation was not persisted: history=%+v err=%v", history, err)
	}
}

func TestScannerClientFailureLeavesProvenanceUnknown(t *testing.T) {
	clock := time.Date(2026, 9, 14, 15, 0, 0, 0, time.UTC)
	filesystem := newFixtureFilesystem(testRoot, clock)
	store := NewMemoryStore()
	scanner, err := New(filesystem, store, []DownloadSource{{ConnectionID: testConnection, Inventory: &fixtureInventory{err: errors.New("synthetic timeout")}}}, Options{Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := scanner.Scan(context.Background(), testRoot)
	if err != nil || len(result.Discoveries) != 3 {
		t.Fatalf("client read failure should not lose filesystem groups: result=%+v err=%v", result, err)
	}
	for _, discovery := range result.Discoveries {
		if discovery.ProvenanceState != ProvenanceUnknown || discovery.ClientCompletion.State != ClientCompletionUnknown || len(discovery.ClientCoverage) != 1 || discovery.ClientCoverage[0].Completeness != domain.CompletenessUnknown || !hasReview(discovery.ReviewReasons, ReviewClientInventoryIncomplete) {
			t.Fatalf("client failure was overclaimed: %+v", discovery)
		}
	}
}

func TestFilenameClassificationKeepsAmbiguityExplicit(t *testing.T) {
	root := testRoot
	files := []domain.FileManifestEntry{
		fixtureFile(root, "Show.S01E01-E02.mkv", domain.ManifestFile, domain.RoleVideo, 1, "1"),
		fixtureFile(root, "Anime - 001.mkv", domain.ManifestFile, domain.RoleVideo, 1, "2"),
		fixtureFile(root, "unmatched.en.srt", domain.ManifestSubtitle, domain.RoleSubtitle, 1, "3"),
	}
	videos, subtitles, _, kind := classifyFiles(files)
	if kind != GroupMixed || len(videos) != 2 || len(subtitles) != 1 {
		t.Fatalf("mixed classification was not retained: kind=%q videos=%d subtitles=%d", kind, len(videos), len(subtitles))
	}
	if len(videos[0].EpisodeNumbers) != 2 || videos[0].Confidence != ConfidenceExact {
		t.Fatalf("multi-episode evidence was lost: %+v", videos[0])
	}
	if videos[1].Kind != domain.MediaAnime || videos[1].Confidence != ConfidenceUnresolved {
		t.Fatalf("absolute anime evidence was overclaimed: %+v", videos[1])
	}
	if subtitles[0].Confidence != ConfidenceUnresolved || subtitles[0].Reason == "" {
		t.Fatalf("unmatched subtitle was not reviewable: %+v", subtitles[0])
	}
}

func TestRootLevelSubtitleUsesVideoGroupKey(t *testing.T) {
	files := []domain.FileManifestEntry{
		fixtureFile(testRoot, "Show.S01E01.mkv", domain.ManifestFile, domain.RoleVideo, 1, "video"),
		fixtureFile(testRoot, "Show.S01E01.en.forced.srt", domain.ManifestSubtitle, domain.RoleSubtitle, 1, "subtitle"),
	}
	groups := groupEntries(files, fixtureCoverage(testRoot, testRuntime, domain.CompletenessComplete), nil, nil, false, nil, nil, DefaultOptions(), time.Date(2026, 9, 14, 18, 0, 0, 0, time.UTC))
	if len(groups) != 1 || len(groups[0].Subtitles) != 1 || groups[0].Subtitles[0].Confidence != ConfidenceExact {
		t.Fatalf("root-level video and subtitle were split: %+v", groups)
	}
}

func TestScannerRetainsUnsupportedChildEvidenceInDirectoryAndGroup(t *testing.T) {
	clock := time.Date(2026, 9, 14, 18, 30, 0, 0, time.UTC)
	filesystem := newFixtureFilesystem(testRoot, clock)
	filesystem.pages["Movie"][0].coverage = domain.CompletenessPartial
	filesystem.pages["Movie"][0].reasons = []string{unsupportedReasonCode("Movie/blocked.sock", "special_file")}
	store := NewMemoryStore()
	scanner, err := New(filesystem, store, nil, Options{Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := scanner.Scan(context.Background(), testRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Observation.UnsupportedChildren) != 1 || result.Observation.UnsupportedChildren[0].RelativePath != "Movie/blocked.sock" {
		t.Fatalf("unsupported child was not persisted as structured evidence: %+v", result.Observation.UnsupportedChildren)
	}
	movie := discoveryByPath(t, result.Discoveries, "Movie")
	if len(movie.UnsupportedChildren) != 1 || !hasReview(movie.ReviewReasons, ReviewUnsupportedChild) || movie.Readiness != domain.ReadinessUnknown {
		t.Fatalf("group lost path-scoped unsupported evidence or overclaimed readiness: %+v", movie)
	}
}

func TestStabilityRequiresIdentityAndDetectsRemovedFiles(t *testing.T) {
	now := time.Date(2026, 9, 14, 16, 0, 0, 0, time.UTC)
	priorAt := now.Add(-time.Minute)
	prior := StabilityObservation{State: StabilityStable, ObservedAt: priorAt, MinimumSpacing: 30 * time.Second, Files: []FileStability{
		{Path: "movie.mkv", State: StabilityStable, ObservedAt: priorAt, ObservationCount: 2, CurrentFingerprint: "movie-fingerprint"},
		{Path: "removed.srt", State: StabilityStable, ObservedAt: priorAt, ObservationCount: 2, CurrentFingerprint: "removed-fingerprint"},
	}}
	current := []domain.FileManifestEntry{
		fixtureFile(testRoot, "movie.mkv", domain.ManifestFile, domain.RoleVideo, 100, "movie"),
		fixtureFile(testRoot, "no-identity.mkv", domain.ManifestFile, domain.RoleVideo, 100, ""),
	}
	result := stabilityFor(current, prior, 30*time.Second, now)
	if result.State != StabilityChanging {
		t.Fatalf("removed file should keep the group changing: %+v", result)
	}
	var identityUnknown bool
	for _, file := range result.Files {
		if file.Path == "no-identity.mkv" && file.State == StabilityUnknown {
			identityUnknown = true
		}
	}
	if !identityUnknown {
		t.Fatalf("missing identity evidence was overclaimed as stable: %+v", result.Files)
	}
}

func TestScannerTreatsMissingFilesystemCoverageAsUnknown(t *testing.T) {
	clock := time.Date(2026, 9, 14, 17, 0, 0, 0, time.UTC)
	filesystem := newFixtureFilesystem(testRoot, clock)
	filesystem.pages[""] = []fixturePage{{items: []domain.FileManifestEntry{fixtureFile(testRoot, "Movie/Movie.mkv", domain.ManifestFile, domain.RoleVideo, 100, "movie")}}}
	filesystem.pages[""][0].coverage = ""
	store := NewMemoryStore()
	scanner, err := New(filesystem, store, nil, Options{Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	result, err := scanner.Scan(context.Background(), testRoot)
	if err != nil || result.Coverage.Completeness != domain.CompletenessUnknown {
		t.Fatalf("missing coverage should be unknown: result=%+v err=%v", result, err)
	}
	if len(result.Discoveries) != 1 || result.Discoveries[0].Readiness != domain.ReadinessUnknown || !hasReview(result.Discoveries[0].ReviewReasons, ReviewCoveragePartial) {
		t.Fatalf("unknown filesystem coverage must remain visible but review blocked: %+v", result.Discoveries)
	}
}

type fixturePage struct {
	items    []domain.FileManifestEntry
	coverage domain.Completeness
	reasons  []string
}

type fixtureFilesystem struct {
	rootID  domain.ConfigID
	pages   map[string][]fixturePage
	partial bool
	rootErr error
}

func newFixtureFilesystem(rootID domain.ConfigID, observed time.Time) *fixtureFilesystem {
	filesystem := &fixtureFilesystem{rootID: rootID, pages: make(map[string][]fixturePage)}
	filesystem.pages[""] = []fixturePage{
		{items: []domain.FileManifestEntry{
			fixtureFile(rootID, "Movie", domain.ManifestDirectory, "", 0, "dir-movie"),
			fixtureFile(rootID, "Pack", domain.ManifestDirectory, "", 0, "dir-pack"),
		}, coverage: domain.CompletenessComplete},
		{items: []domain.FileManifestEntry{
			fixtureFile(rootID, "Anime", domain.ManifestDirectory, "", 0, "dir-anime"),
			fixtureFile(rootID, ".mastarr-trash", domain.ManifestDirectory, "", 0, "dir-trash"),
		}, coverage: domain.CompletenessComplete},
	}
	filesystem.pages["Movie"] = []fixturePage{{items: []domain.FileManifestEntry{
		fixtureFile(rootID, "Movie/Movie.mkv", domain.ManifestFile, domain.RoleVideo, 100, "movie"),
		fixtureFile(rootID, "Movie/Movie.en.forced.srt", domain.ManifestSubtitle, domain.RoleSubtitle, 10, "forced"),
		fixtureFile(rootID, "Movie/Movie.en.sdh.srt", domain.ManifestSubtitle, domain.RoleSubtitle, 11, "sdh"),
		fixtureFile(rootID, "Movie/Movie.idx", domain.ManifestSubtitle, domain.RoleSubtitle, 12, "idx"),
		fixtureFile(rootID, "Movie/Movie.sub", domain.ManifestSubtitle, domain.RoleSubtitle, 13, "sub"),
		fixtureFile(rootID, "Movie/movie.nfo", domain.ManifestCompanion, domain.RoleCompanion, 14, "nfo"),
		fixtureFile(rootID, "Movie/info.bin", domain.ManifestCompanion, domain.RoleCompanion, 15, "bin"),
	}, coverage: domain.CompletenessComplete}}
	filesystem.pages["Pack"] = []fixturePage{{items: []domain.FileManifestEntry{
		fixtureFile(rootID, "Pack/Show.S01E01.mkv", domain.ManifestFile, domain.RoleVideo, 100, "e1"),
		fixtureFile(rootID, "Pack/Show.S01E02.mkv", domain.ManifestFile, domain.RoleVideo, 100, "e2"),
		fixtureFile(rootID, "Pack/Show.S01E01.en.srt", domain.ManifestSubtitle, domain.RoleSubtitle, 10, "e1s"),
		fixtureFile(rootID, "Pack/Show.S01E01.idx", domain.ManifestSubtitle, domain.RoleSubtitle, 10, "e1i"),
		fixtureFile(rootID, "Pack/Show.S01E01.sub", domain.ManifestSubtitle, domain.RoleSubtitle, 10, "e1b"),
	}, coverage: domain.CompletenessComplete}}
	filesystem.pages["Anime"] = []fixturePage{{items: []domain.FileManifestEntry{
		fixtureFile(rootID, "Anime/[Show] - 001.mkv", domain.ManifestFile, domain.RoleVideo, 100, "anime"),
		fixtureFile(rootID, "Anime/[Show] - 001.en.srt", domain.ManifestSubtitle, domain.RoleSubtitle, 10, "animes"),
	}, coverage: domain.CompletenessComplete}}
	_ = observed
	return filesystem
}

func (filesystem *fixtureFilesystem) setRootFiles(items ...domain.FileManifestEntry) {
	filesystem.pages[""] = []fixturePage{{items: items, coverage: domain.CompletenessComplete}}
}

func (filesystem *fixtureFilesystem) Enumerate(ctx context.Context, rootID domain.ConfigID, relativePrefix string, limit int) (ports.Page[domain.FileManifestEntry], error) {
	return filesystem.EnumeratePage(ctx, rootID, relativePrefix, "", limit)
}

func (filesystem *fixtureFilesystem) EnumeratePage(ctx context.Context, rootID domain.ConfigID, relativePrefix, cursor string, limit int) (ports.Page[domain.FileManifestEntry], error) {
	if err := ctx.Err(); err != nil {
		return ports.Page[domain.FileManifestEntry]{}, err
	}
	if rootID != filesystem.rootID {
		return ports.Page[domain.FileManifestEntry]{}, errors.New("wrong root")
	}
	if relativePrefix == "" && filesystem.rootErr != nil {
		return ports.Page[domain.FileManifestEntry]{}, filesystem.rootErr
	}
	pages := filesystem.pages[relativePrefix]
	index := 0
	if cursor != "" {
		if _, err := fmt.Sscanf(cursor, "%d", &index); err != nil {
			return ports.Page[domain.FileManifestEntry]{}, errors.New("bad cursor")
		}
	}
	if index >= len(pages) {
		return ports.Page[domain.FileManifestEntry]{Coverage: fixtureCoverage(rootID, testRuntime, domain.CompletenessComplete)}, nil
	}
	page := pages[index]
	completeness := page.coverage
	if filesystem.partial && relativePrefix == "" {
		completeness = domain.CompletenessPartial
	}
	result := ports.Page[domain.FileManifestEntry]{Items: cloneEntries(page.items), Coverage: fixtureCoverage(rootID, testRuntime, completeness)}
	result.Coverage.ReasonCodes = append([]string(nil), page.reasons...)
	if index+1 < len(pages) {
		result.NextCursor = strconv.Itoa(index + 1)
	}
	_ = limit
	return result, nil
}

func (filesystem *fixtureFilesystem) Stat(context.Context, domain.FileTarget) (ports.FilesystemObservation, error) {
	return ports.FilesystemObservation{}, errors.New("not implemented in fixture")
}

func (filesystem *fixtureFilesystem) Hash(context.Context, domain.FileTarget) (string, error) {
	return "", errors.New("not implemented in fixture")
}

func (filesystem *fixtureFilesystem) Capabilities(context.Context, domain.ConfigID) ([]domain.Capability, error) {
	return nil, nil
}

type fixtureInventory struct {
	items []ports.DownloadItem
	err   error
}

func (inventory *fixtureInventory) List(ctx context.Context, connectionID domain.ConfigID, cursor string, limit int) (ports.Page[ports.DownloadItem], error) {
	if err := ctx.Err(); err != nil {
		return ports.Page[ports.DownloadItem]{}, err
	}
	if inventory.err != nil {
		return ports.Page[ports.DownloadItem]{}, inventory.err
	}
	if connectionID != testConnection || cursor != "" {
		return ports.Page[ports.DownloadItem]{Coverage: fixtureCoverage(testRoot, testRuntime, domain.CompletenessComplete)}, nil
	}
	_ = limit
	items := make([]ports.DownloadItem, len(inventory.items))
	for index, item := range inventory.items {
		items[index] = cloneDownloadItem(item)
	}
	return ports.Page[ports.DownloadItem]{Items: items, Coverage: fixtureCoverage(testRoot, testRuntime, domain.CompletenessComplete)}, nil
}

func fixtureCoverage(rootID domain.ConfigID, sourceID domain.RuntimeID, completeness domain.Completeness) domain.Coverage {
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	return domain.Coverage{SourceID: sourceID, RootID: rootID, Completeness: completeness, ObservedAt: now}
}

func fixtureFile(rootID domain.ConfigID, relativePath string, entryType domain.ManifestEntryType, role domain.ManifestRole, size int64, identity string) domain.FileManifestEntry {
	return domain.FileManifestEntry{RootID: rootID, RelativePath: relativePath, Type: entryType, Role: role, Size: size, FileIdentity: identity, ObservedAt: time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)}
}

func discoveryByPath(t *testing.T, discoveries []Discovery, relativePath string) Discovery {
	t.Helper()
	for _, discovery := range discoveries {
		if discovery.RelativePath == relativePath {
			return discovery
		}
	}
	t.Fatalf("discovery %q not found: %+v", relativePath, discoveries)
	return Discovery{}
}

func firstMovieFirstSeen(t *testing.T, discoveries []Discovery, relativePath string) time.Time {
	t.Helper()
	return discoveryByPath(t, discoveries, relativePath).FirstSeenAt
}

func hasReview(reasons []ReviewReason, expected ReviewReason) bool {
	for _, reason := range reasons {
		if reason == expected {
			return true
		}
	}
	return false
}
