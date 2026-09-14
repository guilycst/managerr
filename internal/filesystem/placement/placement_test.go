package placement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/ports"
)

type recordingJournal struct {
	records  []FileEffect
	err      error
	onAppend func(FileEffect)
}

func (journal *recordingJournal) Append(_ context.Context, effect FileEffect) error {
	if journal.err != nil {
		return journal.err
	}
	journal.records = append(journal.records, effect)
	if journal.onAppend != nil {
		journal.onAppend(effect)
	}
	return nil
}

func TestCopyPublishesExactDigestAndRecordsEachFile(t *testing.T) {
	requirePlacementWrites(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := canonicalTempDir(t)
	rootID := mustConfigID(t, "downloads")
	libraryID := mustConfigID(t, "library")
	sourcePath := filepath.Join(sourceRoot, "film.mkv")
	content := []byte("synthetic movie bytes")
	if err := os.WriteFile(sourcePath, content, 0o640); err != nil {
		t.Fatal(err)
	}
	source := fileEntry(t, rootID, sourceRoot, "film.mkv", domain.ManifestFile)
	journal := &recordingJournal{}
	placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{Journal: journal, BufferSize: 3})
	request := ports.FilesystemCopyRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Movies/film.mkv"},
	}}}

	effect, err := placer.CopyWithOperation(context.Background(), "copy-operation", request)
	if err != nil {
		t.Fatal(err)
	}
	if effect.Outcome != domain.OutcomeApplied || len(effect.Affected) != 1 {
		t.Fatalf("effect = %#v", effect)
	}
	got, err := os.ReadFile(filepath.Join(destinationRoot, "Movies", "film.mkv"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("copied bytes = %q, want %q", got, content)
	}
	if len(journal.records) != 1 || journal.records[0].Mode != TransferCopy || journal.records[0].Outcome != domain.OutcomeApplied {
		t.Fatalf("journal records = %#v", journal.records)
	}
	if journal.records[0].Destination.RelativePath != "Movies/film.mkv" {
		t.Fatalf("journal destination = %#v", journal.records[0].Destination)
	}

	already, err := placer.CopyWithOperation(context.Background(), "copy-operation-again", request)
	if err != nil {
		t.Fatal(err)
	}
	if already.Outcome != domain.OutcomeAlreadySatisfied {
		t.Fatalf("repeat outcome = %q, want already_satisfied", already.Outcome)
	}
	if len(journal.records) != 2 || journal.records[1].Outcome != domain.OutcomeAlreadySatisfied {
		t.Fatalf("repeat journal = %#v", journal.records)
	}

	if err := os.WriteFile(filepath.Join(destinationRoot, "Movies", "film.mkv"), []byte("different movie bytes"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := placer.CopyWithOperation(context.Background(), "copy-conflict", request); !errors.Is(err, ErrDestinationConflict) {
		t.Fatalf("different destination error = %v, want destination conflict", err)
	}
}

func TestCopyRejectsChangedSourceBeforeAnyPublication(t *testing.T) {
	requirePlacementWrites(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := canonicalTempDir(t)
	rootID := mustConfigID(t, "downloads")
	libraryID := mustConfigID(t, "library")
	sourcePath := filepath.Join(sourceRoot, "episode.mkv")
	if err := os.WriteFile(sourcePath, []byte("approved bytes"), 0o640); err != nil {
		t.Fatal(err)
	}
	source := fileEntry(t, rootID, sourceRoot, "episode.mkv", domain.ManifestFile)
	if err := os.WriteFile(sourcePath, []byte("changed bytes"), 0o640); err != nil {
		t.Fatal(err)
	}
	placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{})
	request := ports.FilesystemCopyRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Series/episode.mkv"},
	}}}
	if _, err := placer.CopyWithOperation(context.Background(), "changed-source", request); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("changed source error = %v, want source changed", err)
	}
	if _, err := os.Stat(filepath.Join(destinationRoot, "Series", "episode.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination after changed source = %v, want absent", err)
	}
}

func TestCopyChecksAllDestinationCollisionsBeforePublication(t *testing.T) {
	requirePlacementWrites(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := canonicalTempDir(t)
	rootID := mustConfigID(t, "downloads")
	libraryID := mustConfigID(t, "library")
	firstPath := filepath.Join(sourceRoot, "first.mkv")
	secondPath := filepath.Join(sourceRoot, "second.mkv")
	if err := os.WriteFile(firstPath, []byte("first"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("second-approved"), 0o640); err != nil {
		t.Fatal(err)
	}
	first := fileEntry(t, rootID, sourceRoot, "first.mkv", domain.ManifestFile)
	second := fileEntry(t, rootID, sourceRoot, "second.mkv", domain.ManifestFile)
	if err := os.MkdirAll(filepath.Join(destinationRoot, "Movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destinationRoot, "Movies", "second.mkv"), []byte("different"), 0o640); err != nil {
		t.Fatal(err)
	}
	placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{})
	request := ports.FilesystemCopyRequest{Files: []ports.FileMap{
		{Source: first, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Movies/first.mkv"}},
		{Source: second, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Movies/second.mkv"}},
	}}
	if _, err := placer.CopyWithOperation(context.Background(), "destination-collision", request); !errors.Is(err, ErrDestinationConflict) {
		t.Fatalf("destination collision error = %v, want destination conflict", err)
	}
	if _, err := os.Stat(filepath.Join(destinationRoot, "Movies", "first.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("first destination after preflight collision = %v, want absent", err)
	}
}

func TestCopyUsesExclusiveStagingWithoutOverwritingExistingStage(t *testing.T) {
	requirePlacementWrites(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := canonicalTempDir(t)
	rootID := mustConfigID(t, "downloads")
	libraryID := mustConfigID(t, "library")
	if err := os.WriteFile(filepath.Join(sourceRoot, "movie.mkv"), []byte("movie"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(destinationRoot, "Movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	stagePath := filepath.Join(destinationRoot, "Movies", stageName(DefaultStagePrefix, "stage-collision", 0))
	if err := os.WriteFile(stagePath, []byte("pre-existing stage"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := fileEntry(t, rootID, sourceRoot, "movie.mkv", domain.ManifestFile)
	placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{})
	request := ports.FilesystemCopyRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Movies/movie.mkv"},
	}}}
	if _, err := placer.CopyWithOperation(context.Background(), "stage-collision", request); !errors.Is(err, os.ErrExist) {
		t.Fatalf("stage collision error = %v, want existing-file error", err)
	}
	assertFileBytes(t, stagePath, "pre-existing stage")
	if _, err := os.Stat(filepath.Join(destinationRoot, "Movies", "movie.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination after stage collision = %v, want absent", err)
	}
}

func TestCopyDirectoryUsesOnlyExactManifestChildren(t *testing.T) {
	requirePlacementWrites(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := canonicalTempDir(t)
	rootID := mustConfigID(t, "downloads")
	libraryID := mustConfigID(t, "library")
	pack := filepath.Join(sourceRoot, "pack")
	season := filepath.Join(pack, "Season 1")
	if err := os.MkdirAll(season, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "movie.mkv"), []byte("movie"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(season, "episode.srt"), []byte("subtitle"), 0o640); err != nil {
		t.Fatal(err)
	}
	movie := fileEntry(t, rootID, sourceRoot, "pack/movie.mkv", domain.ManifestFile)
	subtitle := fileEntry(t, rootID, sourceRoot, "pack/Season 1/episode.srt", domain.ManifestSubtitle)
	seasonEntry := directoryEntry(t, rootID, sourceRoot, "pack/Season 1", []domain.FileManifestEntry{subtitle})
	packEntry := directoryEntry(t, rootID, sourceRoot, "pack", []domain.FileManifestEntry{movie, seasonEntry})
	placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{})
	request := ports.FilesystemCopyRequest{Files: []ports.FileMap{{
		Source: packEntry, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Example Film"},
	}}}
	if effect, err := placer.CopyWithOperation(context.Background(), "directory-copy", request); err != nil {
		t.Fatal(err)
	} else if len(effect.Affected) != 2 {
		t.Fatalf("directory affected = %d, want two file effects", len(effect.Affected))
	}
	assertFileBytes(t, filepath.Join(destinationRoot, "Example Film", "movie.mkv"), "movie")
	assertFileBytes(t, filepath.Join(destinationRoot, "Example Film", "Season 1", "episode.srt"), "subtitle")

	if err := os.WriteFile(filepath.Join(pack, "unreviewed.txt"), []byte("outside manifest"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := placer.CopyWithOperation(context.Background(), "directory-changed", request); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("changed directory error = %v, want source changed", err)
	}
}

func TestCopyRechecksDirectoryChildrenAfterPublication(t *testing.T) {
	requirePlacementWrites(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := canonicalTempDir(t)
	rootID := mustConfigID(t, "downloads")
	libraryID := mustConfigID(t, "library")
	pack := filepath.Join(sourceRoot, "pack")
	if err := os.Mkdir(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "movie.mkv"), []byte("movie"), 0o640); err != nil {
		t.Fatal(err)
	}
	movie := fileEntry(t, rootID, sourceRoot, "pack/movie.mkv", domain.ManifestFile)
	packEntry := directoryEntry(t, rootID, sourceRoot, "pack", []domain.FileManifestEntry{movie})
	journal := &recordingJournal{}
	journal.onAppend = func(_ FileEffect) {
		if err := os.WriteFile(filepath.Join(pack, "late.txt"), []byte("late child"), 0o640); err != nil {
			t.Fatalf("add late child: %v", err)
		}
	}
	placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{Journal: journal})
	request := ports.FilesystemCopyRequest{Files: []ports.FileMap{{
		Source: packEntry, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Example Film"},
	}}}
	effect, err := placer.CopyWithOperation(context.Background(), "late-directory-child", request)
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("late directory child error = %v, want source changed", err)
	}
	if len(effect.Affected) != 1 || effect.Outcome != domain.OutcomeApplied {
		t.Fatalf("late directory child effect = %#v, want partial applied effect", effect)
	}
	assertFileBytes(t, filepath.Join(destinationRoot, "Example Film", "movie.mkv"), "movie")
	if _, err := os.Stat(filepath.Join(destinationRoot, "Example Film", "late.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("late unreviewed destination = %v, want absent", err)
	}
}

func TestHardlinkProvesObjectIdentityAndNeverCopies(t *testing.T) {
	requirePlacementWrites(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := canonicalTempDir(t)
	rootID := mustConfigID(t, "downloads")
	libraryID := mustConfigID(t, "library")
	sourcePath := filepath.Join(sourceRoot, "episode.mkv")
	if err := os.WriteFile(sourcePath, []byte("same bytes"), 0o640); err != nil {
		t.Fatal(err)
	}
	source := fileEntry(t, rootID, sourceRoot, "episode.mkv", domain.ManifestFile)
	placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{})
	request := ports.FilesystemHardlinkRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Series/episode.mkv"},
	}}}
	effect, err := placer.HardlinkWithOperation(context.Background(), "hardlink-operation", request)
	if err != nil {
		t.Fatal(err)
	}
	if effect.Outcome != domain.OutcomeApplied {
		t.Fatalf("hardlink outcome = %q", effect.Outcome)
	}
	destinationPath := filepath.Join(destinationRoot, "Series", "episode.mkv")
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	destinationInfo, err := os.Stat(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(sourceInfo, destinationInfo) {
		t.Fatal("hardlink destination is not the source file object")
	}

	if err := os.Remove(destinationPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destinationPath, []byte("same bytes"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := placer.HardlinkWithOperation(context.Background(), "hardlink-different-inode", request); !errors.Is(err, ErrDestinationConflict) {
		t.Fatalf("same-content different-inode error = %v, want conflict", err)
	}
	info, err := os.Stat(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(sourceInfo, info) {
		t.Fatal("hardlink conflict replaced the existing different inode")
	}
}

func TestReconcileHardlinkRequiresApprovedSourceIdentity(t *testing.T) {
	sourceRoot := canonicalTempDir(t)
	destinationRoot := canonicalTempDir(t)
	rootID := mustConfigID(t, "downloads")
	libraryID := mustConfigID(t, "library")
	sourcePath := filepath.Join(sourceRoot, "episode.mkv")
	if err := os.WriteFile(sourcePath, []byte("approved bytes"), 0o640); err != nil {
		t.Fatal(err)
	}
	source := fileEntry(t, rootID, sourceRoot, "episode.mkv", domain.ManifestFile)
	placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{})
	request := ports.FilesystemHardlinkRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Series/episode.mkv"},
	}}}
	if err := os.MkdirAll(filepath.Join(destinationRoot, "Series"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(sourcePath, filepath.Join(destinationRoot, "Series", "episode.mkv")); err != nil {
		t.Fatal(err)
	}
	valid, err := placer.ReconcileHardlink(context.Background(), "approved-hardlink", request)
	if err != nil {
		t.Fatal(err)
	}
	if valid.Outcome != domain.OutcomeAlreadySatisfied || len(valid.Affected) != 1 {
		t.Fatalf("valid hardlink reconciliation = %#v", valid)
	}

	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourcePath, []byte("replaced bytes"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(destinationRoot, "Series", "episode.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(sourcePath, filepath.Join(destinationRoot, "Series", "episode.mkv")); err != nil {
		t.Fatal(err)
	}
	replacement, err := placer.ReconcileHardlink(context.Background(), "replacement-hardlink", request)
	if !errors.Is(err, ErrSourceChanged) || !errors.Is(err, ErrReconciliationNeeded) {
		t.Fatalf("replacement source reconciliation = effect %#v error %v, want unresolved source change", replacement, err)
	}
	if len(replacement.Affected) != 0 {
		t.Fatalf("replacement source was accepted = %#v", replacement)
	}
}

func TestCopyCleanupPreservesReplacementStage(t *testing.T) {
	requirePlacementWrites(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := canonicalTempDir(t)
	rootID := mustConfigID(t, "downloads")
	libraryID := mustConfigID(t, "library")
	sourcePath := filepath.Join(sourceRoot, "large.mkv")
	content := bytes.Repeat([]byte("x"), 8<<20)
	if err := os.WriteFile(sourcePath, content, 0o640); err != nil {
		t.Fatal(err)
	}
	source := fileEntry(t, rootID, sourceRoot, "large.mkv", domain.ManifestFile)
	placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{BufferSize: 1})
	request := ports.FilesystemCopyRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Movies/large.mkv"},
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		effect ports.FilesystemEffect
		err    error
	}
	done := make(chan result, 1)
	go func() {
		effect, err := placer.CopyWithOperation(ctx, "cleanup-race", request)
		done <- result{effect: effect, err: err}
	}()
	stagePath := filepath.Join(destinationRoot, "Movies", stageName(DefaultStagePrefix, "cleanup-race", 0))
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(stagePath); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for staging path")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := os.Remove(stagePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagePath, []byte("another actor"), 0o600); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case outcome := <-done:
		if !errors.Is(outcome.err, context.Canceled) {
			t.Fatalf("copy after stage substitution = effect %#v error %v, want cancellation", outcome.effect, outcome.err)
		}
	case <-deadline.C:
		t.Fatal("timed out waiting for canceled copy")
	}
	assertFileBytes(t, stagePath, "another actor")
	if _, err := os.Stat(filepath.Join(destinationRoot, "Movies", "large.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination after stage substitution = %v, want absent", err)
	}
}

func TestCopyPreservesStageReplacementBetweenCheckAndCleanup(t *testing.T) {
	requirePlacementWrites(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := canonicalTempDir(t)
	rootID := mustConfigID(t, "downloads")
	libraryID := mustConfigID(t, "library")
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	if err := os.WriteFile(sourcePath, []byte("movie"), 0o640); err != nil {
		t.Fatal(err)
	}
	source := fileEntry(t, rootID, sourceRoot, "movie.mkv", domain.ManifestFile)
	stagePath := filepath.Join(destinationRoot, "Movies", stageName(DefaultStagePrefix, "check-cleanup-race", 0))
	destinationPath := filepath.Join(destinationRoot, "Movies", "movie.mkv")
	placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{
		beforeStagePublication: func(*os.File, string) {
			if err := os.Remove(stagePath); err != nil {
				t.Fatalf("replace stage: remove original: %v", err)
			}
			if err := os.WriteFile(stagePath, []byte("another actor"), 0o600); err != nil {
				t.Fatalf("replace stage: create replacement: %v", err)
			}
			if err := os.WriteFile(destinationPath, []byte("existing destination"), 0o640); err != nil {
				t.Fatalf("create destination collision: %v", err)
			}
		},
	})
	request := ports.FilesystemCopyRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Movies/movie.mkv"},
	}}}
	if _, err := placer.CopyWithOperation(context.Background(), "check-cleanup-race", request); !errors.Is(err, ErrStageChanged) {
		t.Fatalf("stage replacement after ownership check = %v, want stage changed", err)
	}
	assertFileBytes(t, stagePath, "another actor")
	assertFileBytes(t, destinationPath, "existing destination")
}

func TestCopyAndHardlinkSyncCreatedDirectoryParents(t *testing.T) {
	requirePlacementWrites(t)
	t.Run("copy nested", func(t *testing.T) {
		sourceRoot := canonicalTempDir(t)
		destinationRoot := canonicalTempDir(t)
		rootID := mustConfigID(t, "downloads")
		libraryID := mustConfigID(t, "library")
		sourcePath := filepath.Join(sourceRoot, "episode.mkv")
		if err := os.WriteFile(sourcePath, []byte("copy"), 0o640); err != nil {
			t.Fatal(err)
		}
		source := fileEntry(t, rootID, sourceRoot, "episode.mkv", domain.ManifestFile)
		var synced []string
		placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{
			SyncDirectory: func(directory *os.File) error {
				synced = append(synced, directory.Name())
				return nil
			},
		})
		request := ports.FilesystemCopyRequest{Files: []ports.FileMap{{
			Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Movies/Season/episode.mkv"},
		}}}
		if _, err := placer.CopyWithOperation(context.Background(), "copy-sync", request); err != nil {
			t.Fatal(err)
		}
		if len(synced) != 3 || synced[1] != "Movies" || synced[2] != "Season" {
			t.Fatalf("directory sync order = %#v, want containing root, Movies, Season", synced)
		}
	})

	t.Run("hardlink nested", func(t *testing.T) {
		sourceRoot := canonicalTempDir(t)
		destinationRoot := canonicalTempDir(t)
		rootID := mustConfigID(t, "downloads")
		libraryID := mustConfigID(t, "library")
		sourcePath := filepath.Join(sourceRoot, "episode.mkv")
		if err := os.WriteFile(sourcePath, []byte("hardlink"), 0o640); err != nil {
			t.Fatal(err)
		}
		source := fileEntry(t, rootID, sourceRoot, "episode.mkv", domain.ManifestFile)
		var synced []string
		placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{
			SyncDirectory: func(directory *os.File) error {
				synced = append(synced, directory.Name())
				return nil
			},
		})
		request := ports.FilesystemHardlinkRequest{Files: []ports.FileMap{{
			Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Series/Season/episode.mkv"},
		}}}
		if _, err := placer.HardlinkWithOperation(context.Background(), "hardlink-sync", request); err != nil {
			t.Fatal(err)
		}
		if len(synced) != 3 || synced[1] != "Series" || synced[2] != "Season" {
			t.Fatalf("directory sync order = %#v, want containing root, Series, Season", synced)
		}
	})
}

func TestCopyDirectorySyncFailureIsUncertainBeforeFilePublication(t *testing.T) {
	requirePlacementWrites(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := canonicalTempDir(t)
	rootID := mustConfigID(t, "downloads")
	libraryID := mustConfigID(t, "library")
	sourcePath := filepath.Join(sourceRoot, "episode.mkv")
	if err := os.WriteFile(sourcePath, []byte("sync failure"), 0o640); err != nil {
		t.Fatal(err)
	}
	source := fileEntry(t, rootID, sourceRoot, "episode.mkv", domain.ManifestFile)
	syncFailure := errors.New("synthetic parent sync failure")
	var calls int
	placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{
		SyncDirectory: func(*os.File) error {
			calls++
			if calls == 2 {
				return syncFailure
			}
			return nil
		},
	})
	request := ports.FilesystemCopyRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Movies/Season/episode.mkv"},
	}}}
	effect, err := placer.CopyWithOperation(context.Background(), "sync-failure", request)
	if !errors.Is(err, syncFailure) || !errors.Is(err, ErrPublicationUnknown) {
		t.Fatalf("sync failure = effect %#v error %v, want uncertain parent sync", effect, err)
	}
	if calls != 2 {
		t.Fatalf("sync calls = %d, want failure on second containing-parent sync", calls)
	}
	if _, err := os.Stat(filepath.Join(destinationRoot, "Movies", "Season", "episode.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination after sync failure = %v, want absent", err)
	}
}

func TestJournalFailureLeavesPublishedCopyForReadOnlyReconciliation(t *testing.T) {
	requirePlacementWrites(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := canonicalTempDir(t)
	rootID := mustConfigID(t, "downloads")
	libraryID := mustConfigID(t, "library")
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	if err := os.WriteFile(sourcePath, []byte("journal uncertainty"), 0o640); err != nil {
		t.Fatal(err)
	}
	source := fileEntry(t, rootID, sourceRoot, "movie.mkv", domain.ManifestFile)
	journal := &recordingJournal{err: errors.New("synthetic journal outage")}
	placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{Journal: journal})
	request := ports.FilesystemCopyRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "Movies/movie.mkv"},
	}}}
	_, err := placer.CopyWithOperation(context.Background(), "uncertain-copy", request)
	var uncertain *UncertainError
	if !errors.As(err, &uncertain) || !errors.Is(err, journal.err) {
		t.Fatalf("journal failure = %v, want uncertain journal error", err)
	}
	if uncertain.OperationID != "uncertain-copy" || !errors.Is(err, ErrJournalUnknown) {
		t.Fatalf("uncertain = %#v, error = %v", uncertain, err)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}

	readOnly, err := placer.ReconcileCopy(context.Background(), "uncertain-copy", request)
	if err != nil {
		t.Fatal(err)
	}
	if readOnly.Outcome != domain.OutcomeAlreadySatisfied || len(readOnly.Affected) != 1 {
		t.Fatalf("reconciled effect = %#v", readOnly)
	}
}

func TestPlacementRejectsTraversalSymlinkAndCancellation(t *testing.T) {
	requirePlacementWrites(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := canonicalTempDir(t)
	outside := canonicalTempDir(t)
	rootID := mustConfigID(t, "downloads")
	libraryID := mustConfigID(t, "library")
	if err := os.WriteFile(filepath.Join(sourceRoot, "movie.mkv"), []byte("bytes"), 0o640); err != nil {
		t.Fatal(err)
	}
	source := fileEntry(t, rootID, sourceRoot, "movie.mkv", domain.ManifestFile)
	placer := mustPlacer(t, []Root{{ID: rootID, Path: sourceRoot}, {ID: libraryID, Path: destinationRoot}}, Options{})
	traversal := ports.FilesystemCopyRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "../outside/movie.mkv"},
	}}}
	if _, err := placer.CopyWithOperation(context.Background(), "traversal", traversal); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("traversal error = %v, want invalid plan", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "movie.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside path = %v, want absent", err)
	}
	if err := os.Symlink(outside, filepath.Join(destinationRoot, "escape")); err != nil {
		t.Fatal(err)
	}
	symlinkTarget := ports.FilesystemCopyRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "escape/movie.mkv"},
	}}}
	if _, err := placer.CopyWithOperation(context.Background(), "symlink", symlinkTarget); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink error = %v, want symlink rejection", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := ports.FilesystemCopyRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: libraryID, RelativePath: "cancelled/movie.mkv"},
	}}}
	if _, err := placer.CopyWithOperation(ctx, "cancelled", cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v, want context canceled", err)
	}
}

func mustPlacer(t *testing.T, roots []Root, options Options) *Placer {
	t.Helper()
	placer, err := New(roots, options)
	if err != nil {
		t.Fatal(err)
	}
	return placer
}

func requirePlacementWrites(t *testing.T) {
	t.Helper()
	if !placementWritesSupported {
		t.Skip("descriptor-bound placement writes are unavailable on this platform")
	}
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func mustConfigID(t *testing.T, value string) domain.ConfigID {
	t.Helper()
	id, err := domain.ParseConfigID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func fileEntry(t *testing.T, rootID domain.ConfigID, rootPath, relative string, entryType domain.ManifestEntryType) domain.FileManifestEntry {
	t.Helper()
	full := filepath.Join(rootPath, filepath.FromSlash(relative))
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	digest := ""
	if !info.IsDir() {
		bytes, err := os.ReadFile(full)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(bytes)
		digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	return domain.FileManifestEntry{RootID: rootID, RelativePath: relative, Type: entryType, Size: info.Size(), Digest: digest, FileIdentity: fileIdentity(info), ObservedAt: time.Now().UTC()}
}

func directoryEntry(t *testing.T, rootID domain.ConfigID, rootPath, relative string, children []domain.FileManifestEntry) domain.FileManifestEntry {
	t.Helper()
	entry := fileEntry(t, rootID, rootPath, relative, domain.ManifestDirectory)
	entry.Children = children
	return entry
}

func assertFileBytes(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}

func TestNormalizeDigestRejectsWeakOrMalformedValues(t *testing.T) {
	valid := "sha256:" + strings.Repeat("a", 64)
	if normalizeDigest(valid) != strings.Repeat("a", 64) {
		t.Fatal("valid digest was not normalized")
	}
	for _, value := range []string{"", "sha1:" + strings.Repeat("a", 40), "sha256:short", "sha256:" + strings.Repeat("z", 64)} {
		if normalizeDigest(value) != "" {
			t.Fatalf("weak digest %q was accepted", value)
		}
	}
}

func TestOperationAndStageNamesAreBounded(t *testing.T) {
	for _, value := range []string{"", " ", "operation/with-slash", "operation\\with-slash", "operation\nwith-control", strings.Repeat("x", 129)} {
		if err := validateOperationID(value); !errors.Is(err, ErrInvalidPlan) {
			t.Fatalf("operation id %q error = %v, want invalid plan", value, err)
		}
	}
	for _, value := range []string{"", ".", "..", "nested/stage", strings.Repeat("x", 65)} {
		if validStagePrefix(value) {
			t.Fatalf("stage prefix %q was accepted", value)
		}
	}
	if _, err := New(nil, Options{BufferSize: MaxBufferSize + 1}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("oversized buffer error = %v, want invalid plan", err)
	}
}
