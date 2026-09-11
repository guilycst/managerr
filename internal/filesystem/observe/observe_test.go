package observe

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/guilycst/managerr/internal/domain"
)

func TestObserveIdentityAndHash(t *testing.T) {
	observer, root, rootID := newTestObserver(t, false)
	writeFile(t, filepath.Join(root, "different-a.mkv"), "1234")
	writeFile(t, filepath.Join(root, "different-b.mkv"), "5678")
	writeFile(t, filepath.Join(root, "identical.mkv"), "1234")

	a, err := observer.Stat(context.Background(), domain.FileTarget{RootID: rootID, RelativePath: "different-a.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := observer.Stat(context.Background(), domain.FileTarget{RootID: rootID, RelativePath: "different-b.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := observer.Stat(context.Background(), domain.FileTarget{RootID: rootID, RelativePath: "identical.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Entry.Size != b.Entry.Size || a.Entry.Size != c.Entry.Size {
		t.Fatalf("test files are not same size: %d, %d, %d", a.Entry.Size, b.Entry.Size, c.Entry.Size)
	}
	if a.Entry.FileIdentity == b.Entry.FileIdentity || a.Entry.FileIdentity == c.Entry.FileIdentity {
		t.Fatal("different files unexpectedly share identity")
	}

	aHash, err := observer.Hash(context.Background(), domain.FileTarget{RootID: rootID, RelativePath: a.Entry.RelativePath})
	if err != nil {
		t.Fatal(err)
	}
	bHash, err := observer.Hash(context.Background(), domain.FileTarget{RootID: rootID, RelativePath: b.Entry.RelativePath})
	if err != nil {
		t.Fatal(err)
	}
	cHash, err := observer.Hash(context.Background(), domain.FileTarget{RootID: rootID, RelativePath: c.Entry.RelativePath})
	if err != nil {
		t.Fatal(err)
	}
	if aHash == bHash || aHash != cHash || aHash == "" || len(aHash) != len("sha256:")+64 {
		t.Fatalf("unexpected hashes: %q, %q, %q", aHash, bHash, cHash)
	}
}

func TestObserveRejectsTraversalRootSymlinkAndPathPrefixAmbiguity(t *testing.T) {
	observer, root, rootID := newTestObserver(t, false)
	outside := filepath.Join(t.TempDir(), "outside.mkv")
	writeFile(t, outside, "outside")
	if _, err := observer.Stat(context.Background(), domain.FileTarget{RootID: rootID, RelativePath: "../outside.mkv"}); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("traversal error = %v, want ErrPathEscape", err)
	}
	if _, err := observer.Stat(context.Background(), domain.FileTarget{RootID: rootID}); !errors.Is(err, ErrRootTarget) {
		t.Fatalf("root target error = %v, want ErrRootTarget", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.mkv")); err != nil {
		t.Fatal(err)
	}
	if _, err := observer.Stat(context.Background(), domain.FileTarget{RootID: rootID, RelativePath: "escape.mkv"}); !errors.Is(err, ErrSymlink) {
		t.Fatalf("symlink error = %v, want ErrSymlink", err)
	}

	connectionID := domain.ConfigID("radarr")
	base := domain.PathMapping{ID: "base", ConnectionID: connectionID, RootID: rootID, SourcePrefix: "/downloads", DestinationPrefix: ""}
	if _, err := MapExternalPath(base, "/downloads2/movie.mkv"); !errors.Is(err, ErrMappingMismatch) {
		t.Fatalf("prefix ambiguity error = %v, want ErrMappingMismatch", err)
	}
	selected, err := SelectMapping([]domain.PathMapping{
		base,
		{ID: "nested", ConnectionID: connectionID, RootID: rootID, SourcePrefix: "/downloads/films", DestinationPrefix: "films"},
	}, connectionID, "/downloads/films/movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != "nested" {
		t.Fatalf("selected mapping = %q, want nested", selected.ID)
	}
	target, err := MapExternalPath(selected, "/downloads/films/movie.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if target.RootID != rootID || target.RelativePath != "films/movie.mkv" {
		t.Fatalf("mapped target = %#v", target)
	}
	external, err := MapRootPath(selected, target)
	if err != nil || external != "/downloads/films/movie.mkv" {
		t.Fatalf("reverse mapping = %q, %v", external, err)
	}
	if _, err := SelectMapping([]domain.PathMapping{
		{ID: "one", ConnectionID: connectionID, RootID: rootID, SourcePrefix: "/downloads", DestinationPrefix: "one"},
		{ID: "two", ConnectionID: connectionID, RootID: rootID, SourcePrefix: "/downloads", DestinationPrefix: "two"},
	}, connectionID, "/downloads/movie.mkv"); !errors.Is(err, ErrAmbiguousMapping) {
		t.Fatalf("equal mapping error = %v, want ErrAmbiguousMapping", err)
	}
}

func TestObserveEnumerationIsBoundedAndCursorRejectsChangedDirectory(t *testing.T) {
	observer, root, rootID := newTestObserver(t, false)
	pack := filepath.Join(root, "pack")
	if err := os.Mkdir(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pack, "one.mkv"), "one")
	writeFile(t, filepath.Join(pack, "two.srt"), "two")

	page, err := observer.Enumerate(context.Background(), rootID, "pack", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.NextCursor == "" || page.Coverage.Completeness != domain.CompletenessPartial {
		t.Fatalf("bounded page = %#v", page)
	}
	if page.Items[0].Type == domain.ManifestDirectory {
		t.Fatal("file page unexpectedly reported directory")
	}

	old := filepath.Join(root, "pack-old")
	if err := os.Rename(pack, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pack, "replacement.mkv"), "replacement")
	if _, err := observer.EnumeratePage(context.Background(), rootID, "pack", page.NextCursor, 1); !errors.Is(err, ErrChanged) {
		t.Fatalf("changed cursor error = %v, want ErrChanged", err)
	}

	full, err := observer.Enumerate(context.Background(), rootID, "pack", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Items) != 1 || full.Items[0].RelativePath != "pack/replacement.mkv" {
		t.Fatalf("replacement page = %#v", full.Items)
	}
}

func TestObserveEnumerationCursorDoesNotDropProbedEntry(t *testing.T) {
	observer, root, rootID := newTestObserver(t, false)
	directory := filepath.Join(root, "paged")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.mkv", "two.mkv", "three.mkv"} {
		writeFile(t, filepath.Join(directory, name), name)
	}

	var all []domain.FileManifestEntry
	page, err := observer.Enumerate(context.Background(), rootID, "paged", 1)
	if err != nil {
		t.Fatal(err)
	}
	all = append(all, page.Items...)
	for page.NextCursor != "" {
		page, err = observer.EnumeratePage(context.Background(), rootID, "paged", page.NextCursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page.Items...)
	}
	if len(all) != 3 {
		t.Fatalf("paged entries = %d, want 3: %#v", len(all), all)
	}
	seen := make(map[string]struct{}, len(all))
	for _, entry := range all {
		if _, exists := seen[entry.RelativePath]; exists {
			t.Fatalf("paged entry repeated: %q", entry.RelativePath)
		}
		seen[entry.RelativePath] = struct{}{}
	}
}

func TestObserveCapabilitiesExposeReadOnlyAndMissingAsEvidence(t *testing.T) {
	observer, root, rootID := newTestObserver(t, true)
	caps, err := observer.Capabilities(context.Background(), rootID)
	if err != nil {
		t.Fatal(err)
	}
	states := make(map[string]domain.CapabilityState, len(caps))
	for _, capability := range caps {
		states[capability.Name] = capability.State
	}
	if states["fs.enumerate"] != domain.CapabilitySupported || states["fs.hash"] != domain.CapabilitySupported {
		t.Fatalf("read capabilities = %#v", states)
	}
	if states["fs.copy"] != domain.CapabilityUnsupported || states["fs.delete"] != domain.CapabilityUnsupported {
		t.Fatalf("read-only write capabilities = %#v", states)
	}

	missing, err := New([]Root{{ID: rootID, Path: filepath.Join(root, "missing")}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	missingCaps, err := missing.Capabilities(context.Background(), rootID)
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range missingCaps {
		if capability.Name == "fs.no_follow" {
			continue
		}
		if capability.State != domain.CapabilityUnknown {
			t.Fatalf("missing root capability %q = %q", capability.Name, capability.State)
		}
	}
}

func newTestObserver(t *testing.T, readOnly bool) (*Observer, string, domain.ConfigID) {
	t.Helper()
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	rootID := domain.ConfigID("downloads")
	observer, err := New([]Root{{ID: rootID, Path: root, ReadOnly: readOnly}}, Options{EnumerationLimit: 100, EnumerationMax: 100})
	if err != nil {
		t.Fatal(err)
	}
	return observer, root, rootID
}

func writeFile(t *testing.T, name, contents string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
