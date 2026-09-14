package organize

import (
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

func TestMoveNoReplaceAndReadBack(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot, destinationRoot, downloads, library := testRoots(t)
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	writeSynthetic(t, sourcePath, "movie bytes")
	source := manifestFor(t, downloads, sourceRoot, "movie.mkv", domain.ManifestFile)
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{})

	effect, err := organizer.MoveWithOperation(context.Background(), "move-no-replace", ports.FilesystemMoveRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: library, RelativePath: "Movies/movie.mkv"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if effect.Outcome != domain.OutcomeApplied || len(effect.Affected) != 1 {
		t.Fatalf("effect = %#v", effect)
	}
	assertMissing(t, sourcePath)
	assertSynthetic(t, filepath.Join(destinationRoot, "Movies", "movie.mkv"), "movie bytes")
}

func TestMoveDoesNotReplaceExistingDestination(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot, destinationRoot, downloads, library := testRoots(t)
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	destinationPath := filepath.Join(destinationRoot, "Movies", "movie.mkv")
	writeSynthetic(t, sourcePath, "approved movie")
	mustMkdir(t, filepath.Dir(destinationPath))
	writeSynthetic(t, destinationPath, "existing movie")
	source := manifestFor(t, downloads, sourceRoot, "movie.mkv", domain.ManifestFile)
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{})

	_, err := organizer.MoveWithOperation(context.Background(), "move-conflict", ports.FilesystemMoveRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: library, RelativePath: "Movies/movie.mkv"},
	}}})
	if !errors.Is(err, ErrDestinationConflict) && !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("error = %v, want destination conflict", err)
	}
	assertSynthetic(t, sourcePath, "approved movie")
	assertSynthetic(t, destinationPath, "existing movie")
}

func TestMoveDirectoryUsesExactManifestChildren(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot, destinationRoot, downloads, library := testRoots(t)
	pack := filepath.Join(sourceRoot, "pack")
	mustMkdir(t, filepath.Join(pack, "Season 1"))
	writeSynthetic(t, filepath.Join(pack, "Season 1", "episode.mkv"), "episode bytes")
	writeSynthetic(t, filepath.Join(pack, "Season 1", "episode.srt"), "subtitle bytes")
	episode := manifestFor(t, downloads, sourceRoot, "pack/Season 1/episode.mkv", domain.ManifestFile)
	subtitle := manifestFor(t, downloads, sourceRoot, "pack/Season 1/episode.srt", domain.ManifestSubtitle)
	season := directoryManifest(t, downloads, sourceRoot, "pack/Season 1", []domain.FileManifestEntry{episode, subtitle})
	packEntry := directoryManifest(t, downloads, sourceRoot, "pack", []domain.FileManifestEntry{season})
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{})

	effect, err := organizer.MoveWithOperation(context.Background(), "move-directory", ports.FilesystemMoveRequest{Files: []ports.FileMap{{
		Source: packEntry, Destination: domain.FileTarget{RootID: library, RelativePath: "Shows/Example"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if effect.Outcome != domain.OutcomeApplied {
		t.Fatalf("outcome = %q, want applied", effect.Outcome)
	}
	assertMissing(t, pack)
	assertSynthetic(t, filepath.Join(destinationRoot, "Shows", "Example", "Season 1", "episode.mkv"), "episode bytes")
	assertSynthetic(t, filepath.Join(destinationRoot, "Shows", "Example", "Season 1", "episode.srt"), "subtitle bytes")
}

func TestMoveAlreadySatisfiedAfterPriorPublication(t *testing.T) {
	requireOrganizeWrites(t)
	sourceRoot, destinationRoot, downloads, library := testRoots(t)
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	destinationPath := filepath.Join(destinationRoot, "Movies", "movie.mkv")
	writeSynthetic(t, sourcePath, "movie bytes")
	source := manifestFor(t, downloads, sourceRoot, "movie.mkv", domain.ManifestFile)
	mustMkdir(t, filepath.Dir(destinationPath))
	if err := os.Rename(sourcePath, destinationPath); err != nil {
		t.Fatal(err)
	}
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{})

	effect, err := organizer.MoveWithOperation(context.Background(), "move-reconcile", ports.FilesystemMoveRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: library, RelativePath: "Movies/movie.mkv"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if effect.Outcome != domain.OutcomeAlreadySatisfied {
		t.Fatalf("outcome = %q, want already_satisfied", effect.Outcome)
	}
	assertSynthetic(t, destinationPath, "movie bytes")
}

func TestMoveRaceRejectsChangedSourceBeforeNativeCall(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot, destinationRoot, downloads, library := testRoots(t)
	sourcePath := filepath.Join(sourceRoot, "episode.mkv")
	destinationPath := filepath.Join(destinationRoot, "Series", "episode.mkv")
	writeSynthetic(t, sourcePath, "approved episode")
	source := manifestFor(t, downloads, sourceRoot, "episode.mkv", domain.ManifestFile)
	called := false
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{beforeMovePublication: func() {
		called = true
		if err := os.Remove(sourcePath); err != nil {
			t.Fatalf("replace source remove: %v", err)
		}
		writeSynthetic(t, sourcePath, "replacement episode")
	}})

	_, err := organizer.MoveWithOperation(context.Background(), "move-race", ports.FilesystemMoveRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: library, RelativePath: "Series/episode.mkv"},
	}}})
	if !called {
		t.Fatal("race seam was not called")
	}
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("error = %v, want source changed", err)
	}
	assertSynthetic(t, sourcePath, "replacement episode")
	assertMissing(t, destinationPath)
}

func TestMovePathExchangeNeverPublishesReplacement(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot, destinationRoot, downloads, library := testRoots(t)
	sourcePath := filepath.Join(sourceRoot, "episode.mkv")
	destinationPath := filepath.Join(destinationRoot, "Series", "episode.mkv")
	writeSynthetic(t, sourcePath, "approved episode")
	source := manifestFor(t, downloads, sourceRoot, "episode.mkv", domain.ManifestFile)
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{beforeMovePublication: func() {
		if err := os.Remove(sourcePath); err != nil {
			t.Fatalf("exchange source remove: %v", err)
		}
		writeSynthetic(t, sourcePath, "unapproved replacement")
	}})

	_, err := organizer.MoveWithOperation(context.Background(), "move-path-exchange", ports.FilesystemMoveRequest{Files: []ports.FileMap{{
		Source: source, Destination: domain.FileTarget{RootID: library, RelativePath: "Series/episode.mkv"},
	}}})
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("error = %v, want source changed", err)
	}
	assertSynthetic(t, sourcePath, "unapproved replacement")
	assertMissing(t, destinationPath)
}

func TestDeleteExactManifestAndIdempotent(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot, destinationRoot, downloads, library := testRoots(t)
	_ = destinationRoot
	_ = library
	sourcePath := filepath.Join(sourceRoot, "subtitle.srt")
	writeSynthetic(t, sourcePath, "subtitle bytes")
	entry := manifestFor(t, downloads, sourceRoot, "subtitle.srt", domain.ManifestSubtitle)
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{})
	request := ports.FilesystemDeleteRequest{Files: []domain.FileManifestEntry{entry}}

	effect, err := organizer.DeleteWithOperation(context.Background(), "delete-exact", request)
	if err != nil {
		t.Fatal(err)
	}
	if effect.Outcome != domain.OutcomeApplied {
		t.Fatalf("outcome = %q, want applied", effect.Outcome)
	}
	assertMissing(t, sourcePath)
	already, err := organizer.DeleteWithOperation(context.Background(), "delete-repeat", request)
	if err != nil {
		t.Fatal(err)
	}
	if already.Outcome != domain.OutcomeAlreadySatisfied {
		t.Fatalf("repeat outcome = %q, want already_satisfied", already.Outcome)
	}
}

func TestDeleteRejectsChangedSource(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot, destinationRoot, downloads, library := testRoots(t)
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	writeSynthetic(t, sourcePath, "approved movie")
	entry := manifestFor(t, downloads, sourceRoot, "movie.mkv", domain.ManifestFile)
	writeSynthetic(t, sourcePath, "replacement movie")
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{})

	_, err := organizer.DeleteWithOperation(context.Background(), "delete-changed", ports.FilesystemDeleteRequest{Files: []domain.FileManifestEntry{entry}})
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("error = %v, want source changed", err)
	}
	assertSynthetic(t, sourcePath, "replacement movie")
}

func TestDeleteRacePreservesReplacement(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot, destinationRoot, downloads, library := testRoots(t)
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	writeSynthetic(t, sourcePath, "approved movie")
	entry := manifestFor(t, downloads, sourceRoot, "movie.mkv", domain.ManifestFile)
	called := false
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{beforeDeletePublication: func() {
		called = true
		if err := os.Remove(sourcePath); err != nil {
			t.Fatalf("replace source remove: %v", err)
		}
		writeSynthetic(t, sourcePath, "replacement movie")
	}})

	_, err := organizer.DeleteWithOperation(context.Background(), "delete-race", ports.FilesystemDeleteRequest{Files: []domain.FileManifestEntry{entry}})
	if !called {
		t.Fatal("race seam was not called")
	}
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("error = %v, want source changed", err)
	}
	assertSynthetic(t, sourcePath, "replacement movie")
}

func TestDeletePathExchangeNeverDeletesReplacement(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot, destinationRoot, downloads, library := testRoots(t)
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	writeSynthetic(t, sourcePath, "approved movie")
	entry := manifestFor(t, downloads, sourceRoot, "movie.mkv", domain.ManifestFile)
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{beforeDeletePublication: func() {
		if err := os.Remove(sourcePath); err != nil {
			t.Fatalf("exchange source remove: %v", err)
		}
		writeSynthetic(t, sourcePath, "unapproved replacement")
	}})

	_, err := organizer.DeleteWithOperation(context.Background(), "delete-path-exchange", ports.FilesystemDeleteRequest{Files: []domain.FileManifestEntry{entry}})
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("error = %v, want source changed", err)
	}
	assertSynthetic(t, sourcePath, "unapproved replacement")
}

func TestDeleteDirectoryRejectsUnreviewedChild(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot, destinationRoot, downloads, library := testRoots(t)
	directoryPath := filepath.Join(sourceRoot, "pack")
	mustMkdir(t, directoryPath)
	writeSynthetic(t, filepath.Join(directoryPath, "selected.mkv"), "selected")
	entry := manifestFor(t, downloads, sourceRoot, "pack/selected.mkv", domain.ManifestFile)
	pack := directoryManifest(t, downloads, sourceRoot, "pack", []domain.FileManifestEntry{entry})
	writeSynthetic(t, filepath.Join(directoryPath, "unreviewed.srt"), "unreviewed")
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{})

	_, err := organizer.DeleteWithOperation(context.Background(), "delete-directory-scope", ports.FilesystemDeleteRequest{Files: []domain.FileManifestEntry{pack}})
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("error = %v, want source changed", err)
	}
	assertSynthetic(t, filepath.Join(directoryPath, "selected.mkv"), "selected")
	assertSynthetic(t, filepath.Join(directoryPath, "unreviewed.srt"), "unreviewed")
}

func TestCopyVerifyDeleteRequiresDifferentFilesystem(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := crossDeviceTempDir(t)
	downloads := mustConfigID(t, "downloads")
	library := mustConfigID(t, "library")
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	writeSynthetic(t, sourcePath, "cross device movie")
	entry := manifestFor(t, downloads, sourceRoot, "movie.mkv", domain.ManifestFile)
	if sameFilesystem(t, sourcePath, destinationRoot) {
		t.Skip("test host does not expose a separate filesystem")
	}
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{})

	effect, err := organizer.CopyVerifyDeleteWithOperation(context.Background(), "cross-device-compose", CrossDeviceMoveRequest{Files: []ports.FileMap{{
		Source: entry, Destination: domain.FileTarget{RootID: library, RelativePath: "Movies/movie.mkv"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if effect.Outcome != domain.OutcomeApplied {
		t.Fatalf("outcome = %q, want applied", effect.Outcome)
	}
	assertMissing(t, sourcePath)
	assertSynthetic(t, filepath.Join(destinationRoot, "Movies", "movie.mkv"), "cross device movie")
	repeated, err := organizer.CopyVerifyDeleteWithOperation(context.Background(), "cross-device-repeat", CrossDeviceMoveRequest{Files: []ports.FileMap{{
		Source: entry, Destination: domain.FileTarget{RootID: library, RelativePath: "Movies/movie.mkv"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Outcome != domain.OutcomeAlreadySatisfied {
		t.Fatalf("repeat outcome = %q, want already_satisfied", repeated.Outcome)
	}
}

func TestMoveRejectsCrossDeviceWithoutMutation(t *testing.T) {
	requireOrganizeWrites(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := crossDeviceTempDir(t)
	downloads := mustConfigID(t, "downloads")
	library := mustConfigID(t, "library")
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	writeSynthetic(t, sourcePath, "cross device movie")
	entry := manifestFor(t, downloads, sourceRoot, "movie.mkv", domain.ManifestFile)
	if sameFilesystem(t, sourcePath, destinationRoot) {
		t.Skip("test host does not expose a separate filesystem")
	}
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{})
	_, err := organizer.MoveWithOperation(context.Background(), "cross-device-move", ports.FilesystemMoveRequest{Files: []ports.FileMap{{
		Source: entry, Destination: domain.FileTarget{RootID: library, RelativePath: "Movies/movie.mkv"},
	}}})
	if !errors.Is(err, ErrCrossDevice) {
		t.Fatalf("error = %v, want cross-device", err)
	}
	assertSynthetic(t, sourcePath, "cross device movie")
	assertMissing(t, filepath.Join(destinationRoot, "Movies", "movie.mkv"))
}

func TestCopyVerifyDeletePreservesVerifiedCopyWhenSourceRemovalIsRejected(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := crossDeviceTempDir(t)
	downloads := mustConfigID(t, "downloads")
	library := mustConfigID(t, "library")
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	writeSynthetic(t, sourcePath, "approved movie")
	entry := manifestFor(t, downloads, sourceRoot, "movie.mkv", domain.ManifestFile)
	if sameFilesystem(t, sourcePath, destinationRoot) {
		t.Skip("test host does not expose a separate filesystem")
	}
	called := false
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{beforeDeletePublication: func() {
		called = true
		if err := os.Remove(sourcePath); err != nil {
			t.Fatalf("replace source remove: %v", err)
		}
		writeSynthetic(t, sourcePath, "replacement movie")
	}})
	_, err := organizer.CopyVerifyDeleteWithOperation(context.Background(), "cross-device-delete-race", CrossDeviceMoveRequest{Files: []ports.FileMap{{
		Source: entry, Destination: domain.FileTarget{RootID: library, RelativePath: "Movies/movie.mkv"},
	}}})
	if !called {
		t.Fatal("delete race seam was not called")
	}
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("error = %v, want source changed", err)
	}
	assertSynthetic(t, sourcePath, "replacement movie")
	assertSynthetic(t, filepath.Join(destinationRoot, "Movies", "movie.mkv"), "approved movie")
}

func TestCopyVerifyDeletePreservesSourceWhenDestinationDisappearsAtDeleteBoundary(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := crossDeviceTempDir(t)
	downloads := mustConfigID(t, "downloads")
	library := mustConfigID(t, "library")
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	destinationPath := filepath.Join(destinationRoot, "Movies", "movie.mkv")
	writeSynthetic(t, sourcePath, "approved movie")
	entry := manifestFor(t, downloads, sourceRoot, "movie.mkv", domain.ManifestFile)
	if sameFilesystem(t, sourcePath, destinationRoot) {
		t.Skip("test host does not expose a separate filesystem")
	}
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{beforeDeletePublication: func() {
		if err := os.Remove(destinationPath); err != nil {
			t.Fatalf("remove verified destination: %v", err)
		}
	}})

	_, err := organizer.CopyVerifyDeleteWithOperation(context.Background(), "cross-device-destination-loss", CrossDeviceMoveRequest{Files: []ports.FileMap{{
		Source: entry, Destination: domain.FileTarget{RootID: library, RelativePath: "Movies/movie.mkv"},
	}}})
	if !errors.Is(err, ErrReconciliationNeeded) {
		t.Fatalf("error = %v, want reconciliation required", err)
	}
	assertSynthetic(t, sourcePath, "approved movie")
	assertMissing(t, destinationPath)
}

func TestCopyVerifyDeletePreservesDirectorySourceWhenDestinationDisappearsAtDeleteBoundary(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := crossDeviceTempDir(t)
	downloads := mustConfigID(t, "downloads")
	library := mustConfigID(t, "library")
	sourceDir := filepath.Join(sourceRoot, "pack")
	sourcePath := filepath.Join(sourceDir, "episode.mkv")
	destinationPath := filepath.Join(destinationRoot, "Shows", "Example")
	mustMkdir(t, sourceDir)
	writeSynthetic(t, sourcePath, "approved episode")
	child := manifestFor(t, downloads, sourceRoot, "pack/episode.mkv", domain.ManifestFile)
	entry := directoryManifest(t, downloads, sourceRoot, "pack", []domain.FileManifestEntry{child})
	if sameFilesystem(t, sourcePath, destinationRoot) {
		t.Skip("test host does not expose a separate filesystem")
	}
	called := false
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{beforeDeletePublication: func() {
		called = true
		if err := os.RemoveAll(destinationPath); err != nil {
			t.Fatalf("remove verified destination directory: %v", err)
		}
		if _, err := os.Lstat(destinationPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("destination after removal = %v", err)
		}
	}})

	_, err := organizer.CopyVerifyDeleteWithOperation(context.Background(), "cross-device-directory-destination-loss", CrossDeviceMoveRequest{Files: []ports.FileMap{{
		Source: entry, Destination: domain.FileTarget{RootID: library, RelativePath: "Shows/Example"},
	}}})
	if !errors.Is(err, ErrReconciliationNeeded) {
		t.Fatalf("error = %v, want reconciliation required", err)
	}
	if !called {
		t.Fatal("delete boundary seam was not called")
	}
	assertSynthetic(t, sourcePath, "approved episode")
	assertMissing(t, destinationPath)
}

func TestCopyVerifyDeleteRetainsIndependentProtectionAfterDestinationMutation(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := crossDeviceTempDir(t)
	downloads := mustConfigID(t, "downloads")
	library := mustConfigID(t, "library")
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	destinationPath := filepath.Join(destinationRoot, "Movies", "movie.mkv")
	writeSynthetic(t, sourcePath, "approved movie")
	entry := manifestFor(t, downloads, sourceRoot, "movie.mkv", domain.ManifestFile)
	if sameFilesystem(t, sourcePath, destinationRoot) {
		t.Skip("test host does not expose a separate filesystem")
	}
	called := false
	operationID := "cross-device-destination-mutation"
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{afterDestinationGuardVerification: func() {
		called = true
		file, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_TRUNC, 0o640)
		if err != nil {
			t.Fatalf("open destination for in-place mutation: %v", err)
		}
		if _, err := file.WriteString("rejected movie"); err != nil {
			_ = file.Close()
			t.Fatalf("mutate destination: %v", err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			t.Fatalf("sync destination mutation: %v", err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("close destination mutation: %v", err)
		}
	}})
	_, err := organizer.CopyVerifyDeleteWithOperation(context.Background(), operationID, CrossDeviceMoveRequest{Files: []ports.FileMap{{
		Source: entry, Destination: domain.FileTarget{RootID: library, RelativePath: "Movies/movie.mkv"},
	}}})
	if !called {
		t.Fatal("destination mutation seam was not called")
	}
	if !errors.Is(err, ErrReconciliationNeeded) {
		t.Fatalf("error = %v, want reconciliation required", err)
	}
	assertSynthetic(t, destinationPath, "rejected movie")
	guardPath := filepath.Join(destinationRoot, privateEntryName("copy-guard", operationID, 0, "Movies/movie.mkv"), "payload")
	assertSynthetic(t, guardPath, "approved movie")
}

func TestCopyVerifyDeleteRetainsIndependentDirectoryProtectionAfterMutation(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := crossDeviceTempDir(t)
	downloads := mustConfigID(t, "downloads")
	library := mustConfigID(t, "library")
	sourceDir := filepath.Join(sourceRoot, "pack")
	sourcePath := filepath.Join(sourceDir, "episode.mkv")
	destinationPath := filepath.Join(destinationRoot, "Shows", "Example", "episode.mkv")
	mustMkdir(t, sourceDir)
	writeSynthetic(t, sourcePath, "approved episode")
	child := manifestFor(t, downloads, sourceRoot, "pack/episode.mkv", domain.ManifestFile)
	entry := directoryManifest(t, downloads, sourceRoot, "pack", []domain.FileManifestEntry{child})
	if sameFilesystem(t, sourcePath, destinationRoot) {
		t.Skip("test host does not expose a separate filesystem")
	}
	operationID := "cross-device-directory-mutation"
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{afterDestinationGuardVerification: func() {
		file, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_TRUNC, 0o640)
		if err != nil {
			t.Fatalf("open destination episode for in-place mutation: %v", err)
		}
		if _, err := file.WriteString("rejected episode"); err != nil {
			_ = file.Close()
			t.Fatalf("mutate destination episode: %v", err)
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			t.Fatalf("sync destination episode mutation: %v", err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("close destination episode mutation: %v", err)
		}
	}})
	_, err := organizer.CopyVerifyDeleteWithOperation(context.Background(), operationID, CrossDeviceMoveRequest{Files: []ports.FileMap{{
		Source: entry, Destination: domain.FileTarget{RootID: library, RelativePath: "Shows/Example"},
	}}})
	if !errors.Is(err, ErrReconciliationNeeded) {
		t.Fatalf("error = %v, want reconciliation required", err)
	}
	assertSynthetic(t, destinationPath, "rejected episode")
	guardPath := filepath.Join(destinationRoot, privateEntryName("copy-guard", operationID, 0, "Shows/Example"), "payload", "episode.mkv")
	assertSynthetic(t, guardPath, "approved episode")
}

func TestCopyVerifyDeleteDirectoryCompletesAndCleansProtection(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := crossDeviceTempDir(t)
	downloads := mustConfigID(t, "downloads")
	library := mustConfigID(t, "library")
	sourceDir := filepath.Join(sourceRoot, "pack")
	sourcePath := filepath.Join(sourceDir, "episode.mkv")
	destinationPath := filepath.Join(destinationRoot, "Shows", "Example")
	mustMkdir(t, sourceDir)
	writeSynthetic(t, sourcePath, "approved episode")
	child := manifestFor(t, downloads, sourceRoot, "pack/episode.mkv", domain.ManifestFile)
	entry := directoryManifest(t, downloads, sourceRoot, "pack", []domain.FileManifestEntry{child})
	if sameFilesystem(t, sourcePath, destinationRoot) {
		t.Skip("test host does not expose a separate filesystem")
	}
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{})
	if _, err := organizer.CopyVerifyDeleteWithOperation(context.Background(), "cross-device-directory-complete", CrossDeviceMoveRequest{Files: []ports.FileMap{{
		Source: entry, Destination: domain.FileTarget{RootID: library, RelativePath: "Shows/Example"},
	}}}); err != nil {
		t.Fatal(err)
	}
	assertMissing(t, sourceDir)
	assertSynthetic(t, filepath.Join(destinationPath, "episode.mkv"), "approved episode")
	entries, err := os.ReadDir(destinationRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range entries {
		if strings.HasPrefix(candidate.Name(), ".mastarr-copy-guard-") {
			t.Fatalf("destination protection leaked %q", candidate.Name())
		}
	}
}

func TestCopyVerifyDeleteCleansEarlierGuardsOnLaterFailure(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot := canonicalTempDir(t)
	destinationRoot := crossDeviceTempDir(t)
	downloads := mustConfigID(t, "downloads")
	library := mustConfigID(t, "library")
	firstPath := filepath.Join(sourceRoot, "first.mkv")
	secondPath := filepath.Join(sourceRoot, "second.mkv")
	writeSynthetic(t, firstPath, "first approved")
	writeSynthetic(t, secondPath, "second approved")
	first := manifestFor(t, downloads, sourceRoot, "first.mkv", domain.ManifestFile)
	second := manifestFor(t, downloads, sourceRoot, "second.mkv", domain.ManifestFile)
	if sameFilesystem(t, firstPath, destinationRoot) {
		t.Skip("test host does not expose a separate filesystem")
	}
	operationID := "cross-device-guard-retry"
	secondCollision := filepath.Join(destinationRoot, privateEntryName("copy-guard", operationID, 1, "Movies/second.mkv"))
	if err := os.Mkdir(secondCollision, 0o700); err != nil {
		t.Fatal(err)
	}
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{})
	request := CrossDeviceMoveRequest{Files: []ports.FileMap{
		{Source: first, Destination: domain.FileTarget{RootID: library, RelativePath: "Movies/first.mkv"}},
		{Source: second, Destination: domain.FileTarget{RootID: library, RelativePath: "Movies/second.mkv"}},
	}}
	if _, err := organizer.CopyVerifyDeleteWithOperation(context.Background(), operationID, request); !errors.Is(err, ErrReconciliationNeeded) {
		t.Fatalf("first operation error = %v, want reconciliation required", err)
	}
	assertSynthetic(t, firstPath, "first approved")
	assertSynthetic(t, secondPath, "second approved")
	firstGuard := filepath.Join(destinationRoot, privateEntryName("copy-guard", operationID, 0, "Movies/first.mkv"))
	assertMissing(t, firstGuard)
	if err := os.Remove(secondCollision); err != nil {
		t.Fatal(err)
	}
	if _, err := organizer.CopyVerifyDeleteWithOperation(context.Background(), operationID, request); err != nil {
		t.Fatal(err)
	}
	assertMissing(t, firstPath)
	assertMissing(t, secondPath)
	assertSynthetic(t, filepath.Join(destinationRoot, "Movies", "first.mkv"), "first approved")
	assertSynthetic(t, filepath.Join(destinationRoot, "Movies", "second.mkv"), "second approved")
}

func TestNewRejectsConflictingPhysicalRootAliases(t *testing.T) {
	rootPath := canonicalTempDir(t)
	first := mustConfigID(t, "first")
	second := mustConfigID(t, "second")
	if _, err := New([]Root{{ID: first, Path: rootPath}, {ID: second, Path: rootPath, ReadOnly: true}}, Options{}); !errors.Is(err, ErrRootNotConfigured) {
		t.Fatalf("error = %v, want conflicting physical roots rejected", err)
	}
}

func TestNewRejectsNestedPhysicalRoots(t *testing.T) {
	rootPath := canonicalTempDir(t)
	nestedPath := filepath.Join(rootPath, "nested")
	mustMkdir(t, nestedPath)
	first := mustConfigID(t, "first")
	second := mustConfigID(t, "second")
	if _, err := New([]Root{{ID: first, Path: rootPath}, {ID: second, Path: nestedPath}}, Options{}); !errors.Is(err, ErrRootNotConfigured) {
		t.Fatalf("error = %v, want nested physical roots rejected", err)
	}
}

func TestNewRejectsSymlinkedPhysicalRootAlias(t *testing.T) {
	rootPath := canonicalTempDir(t)
	aliasParent := canonicalTempDir(t)
	aliasPath := filepath.Join(aliasParent, "alias")
	if err := os.Symlink(rootPath, aliasPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	first := mustConfigID(t, "first")
	second := mustConfigID(t, "second")
	if _, err := New([]Root{{ID: first, Path: rootPath}, {ID: second, Path: aliasPath}}, Options{}); !errors.Is(err, ErrRootNotConfigured) {
		t.Fatalf("error = %v, want symlinked physical roots rejected", err)
	}
}

func TestCanceledActionsDoNotMutate(t *testing.T) {
	requireOrganizeCleanup(t)
	sourceRoot, destinationRoot, downloads, library := testRoots(t)
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	writeSynthetic(t, sourcePath, "movie bytes")
	entry := manifestFor(t, downloads, sourceRoot, "movie.mkv", domain.ManifestFile)
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := organizer.MoveWithOperation(ctx, "canceled-move", ports.FilesystemMoveRequest{Files: []ports.FileMap{{
		Source: entry, Destination: domain.FileTarget{RootID: library, RelativePath: "Movies/movie.mkv"},
	}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	assertSynthetic(t, sourcePath, "movie bytes")
	assertMissing(t, filepath.Join(destinationRoot, "Movies", "movie.mkv"))
}

func TestOrganizeCleanupFailsClosedBeforeQuarantineMutation(t *testing.T) {
	if organizeCleanupSupported {
		t.Skip("target has a reviewed inode-bound quarantine removal primitive")
	}
	sourceRoot, destinationRoot, downloads, library := testRoots(t)
	sourcePath := filepath.Join(sourceRoot, "movie.mkv")
	destinationPath := filepath.Join(destinationRoot, "Movies", "movie.mkv")
	writeSynthetic(t, sourcePath, "approved movie")
	entry := manifestFor(t, downloads, sourceRoot, "movie.mkv", domain.ManifestFile)
	organizer := mustOrganizer(t, sourceRoot, destinationRoot, downloads, library, Options{})

	if _, err := organizer.MoveWithOperation(context.Background(), "blocked-move", ports.FilesystemMoveRequest{Files: []ports.FileMap{{
		Source: entry, Destination: domain.FileTarget{RootID: library, RelativePath: "Movies/movie.mkv"},
	}}}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("move error = %v, want unsupported", err)
	}
	assertSynthetic(t, sourcePath, "approved movie")
	assertMissing(t, destinationPath)
	assertMissing(t, filepath.Join(sourceRoot, privateEntryName("move", "blocked-move", 0, entry.RelativePath)))

	if _, err := organizer.DeleteWithOperation(context.Background(), "blocked-delete", ports.FilesystemDeleteRequest{Files: []domain.FileManifestEntry{entry}}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("delete error = %v, want unsupported", err)
	}
	assertSynthetic(t, sourcePath, "approved movie")
	assertMissing(t, filepath.Join(sourceRoot, privateEntryName("delete", "blocked-delete", 0, entry.RelativePath)))
}

func requireOrganizeWrites(t *testing.T) {
	t.Helper()
	if !organizeWritesSupported {
		t.Skip("organize writes are fail-closed on this platform")
	}
}

func requireOrganizeCleanup(t *testing.T) {
	t.Helper()
	if !organizeCleanupSupported {
		t.Skip("quarantine cleanup is fail-closed on this platform")
	}
}

func testRoots(t *testing.T) (string, string, domain.ConfigID, domain.ConfigID) {
	t.Helper()
	return canonicalTempDir(t), canonicalTempDir(t), mustConfigID(t, "downloads"), mustConfigID(t, "library")
}

func mustConfigID(t *testing.T, value string) domain.ConfigID {
	t.Helper()
	id, err := domain.ParseConfigID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustOrganizer(t *testing.T, sourceRoot, destinationRoot string, sourceID, destinationID domain.ConfigID, options Options) *Organizer {
	t.Helper()
	organizer, err := New([]Root{{ID: sourceID, Path: sourceRoot}, {ID: destinationID, Path: destinationRoot}}, options)
	if err != nil {
		t.Fatal(err)
	}
	return organizer
}

func manifestFor(t *testing.T, rootID domain.ConfigID, rootPath, relative string, entryType domain.ManifestEntryType) domain.FileManifestEntry {
	t.Helper()
	info, err := os.Stat(filepath.Join(rootPath, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	digest := ""
	if !info.IsDir() {
		bytes, err := os.ReadFile(filepath.Join(rootPath, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(bytes)
		digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	return domain.FileManifestEntry{RootID: rootID, RelativePath: relative, Type: entryType, Size: info.Size(), Digest: digest, FileIdentity: fileIdentity(info), ObservedAt: time.Now().UTC()}
}

func directoryManifest(t *testing.T, rootID domain.ConfigID, rootPath, relative string, children []domain.FileManifestEntry) domain.FileManifestEntry {
	t.Helper()
	entry := manifestFor(t, rootID, rootPath, relative, domain.ManifestDirectory)
	entry.Children = children
	return entry
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

func crossDeviceTempDir(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat("/dev/shm"); err != nil {
		t.Skip("no synthetic second filesystem available")
	}
	root, err := os.MkdirTemp("/dev/shm", "mastarr-organize-")
	if err != nil {
		t.Skipf("cannot create synthetic second filesystem: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func sameFilesystem(t *testing.T, sourcePath, destinationRoot string) bool {
	t.Helper()
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	destinationInfo, err := os.Stat(destinationRoot)
	if err != nil {
		t.Fatal(err)
	}
	sourceDevice, err := filesystemDevice(sourceInfo)
	if err != nil {
		t.Fatal(err)
	}
	destinationDevice, err := filesystemDevice(destinationInfo)
	if err != nil {
		t.Fatal(err)
	}
	return sourceDevice == destinationDevice
}

func writeSynthetic(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, name string) {
	t.Helper()
	if err := os.MkdirAll(name, 0o755); err != nil {
		t.Fatal(err)
	}
}

func assertSynthetic(t *testing.T, name, want string) {
	t.Helper()
	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func assertMissing(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s = %v, want absent", name, err)
	}
}
