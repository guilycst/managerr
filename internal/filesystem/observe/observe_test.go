package observe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guilycst/managerr/internal/domain"
	"github.com/guilycst/managerr/internal/ports"
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

func TestObserveEnumerationPageThroughFilesystemReadPort(t *testing.T) {
	observer, root, rootID := newTestObserver(t, false)
	for _, name := range []string{"one.mkv", "two.mkv", "three.mkv", "four.mkv"} {
		writeFile(t, filepath.Join(root, name), name)
	}

	var readPort ports.FilesystemReadPort = observer
	page, err := readPort.Enumerate(context.Background(), rootID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.NextCursor == "" {
		t.Fatal("one-item page unexpectedly complete")
	}
	firstSource := page.Coverage.SourceID
	firstStarted := page.Coverage.StartedAt
	seen := make(map[string]struct{})
	for {
		for _, item := range page.Items {
			seen[item.RelativePath] = struct{}{}
		}
		if page.Coverage.SourceID != firstSource || page.Coverage.StartedAt == nil || firstStarted == nil || !page.Coverage.StartedAt.Equal(*firstStarted) {
			t.Fatalf("page coverage identity changed: first=%#v current=%#v", firstSource, page.Coverage)
		}
		if page.NextCursor == "" {
			break
		}
		page, err = readPort.EnumeratePage(context.Background(), rootID, "", page.NextCursor, 1)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 4 {
		t.Fatalf("interface pagination returned %d unique entries, want 4: %#v", len(seen), seen)
	}
}

func TestObserveEnumerationCursorRejectsReplay(t *testing.T) {
	observer, root, rootID := newTestObserver(t, false)
	for _, name := range []string{"one.mkv", "two.mkv", "three.mkv", "four.mkv"} {
		writeFile(t, filepath.Join(root, name), name)
	}

	first, err := observer.Enumerate(context.Background(), rootID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := observer.EnumeratePage(context.Background(), rootID, "", first.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := observer.EnumeratePage(context.Background(), rootID, "", first.NextCursor, 1); !errors.Is(err, ErrEnumerationStale) {
		t.Fatalf("replayed cursor error = %v, want ErrEnumerationStale", err)
	}

	seen := len(first.Items) + len(second.Items)
	page := second
	for page.NextCursor != "" {
		page, err = observer.EnumeratePage(context.Background(), rootID, "", page.NextCursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		seen += len(page.Items)
	}
	if seen != 4 {
		t.Fatalf("replay-safe continuation returned %d items, want 4", seen)
	}
}

func TestObserveEnumerationCursorRejectsConcurrentReplay(t *testing.T) {
	observer, root, rootID := newTestObserver(t, false)
	for _, name := range []string{"one.mkv", "two.mkv", "three.mkv", "four.mkv"} {
		writeFile(t, filepath.Join(root, name), name)
	}
	first, err := observer.Enumerate(context.Background(), rootID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		page ports.Page[domain.FileManifestEntry]
		err  error
	}
	results := make([]result, 2)
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(2)
	for index := range results {
		go func(index int) {
			defer group.Done()
			<-start
			results[index].page, results[index].err = observer.EnumeratePage(context.Background(), rootID, "", first.NextCursor, 1)
		}(index)
	}
	close(start)
	group.Wait()

	successes := 0
	stale := 0
	var successfulPage ports.Page[domain.FileManifestEntry]
	for _, result := range results {
		if result.err == nil {
			successes++
			successfulPage = result.page
			continue
		}
		if errors.Is(result.err, ErrEnumerationStale) {
			stale++
			continue
		}
		t.Fatalf("concurrent replay error = %v, want nil or ErrEnumerationStale", result.err)
	}
	if successes != 1 || stale != 1 {
		t.Fatalf("concurrent replay outcomes = successes %d stale %d, want one each", successes, stale)
	}

	seen := len(first.Items) + len(successfulPage.Items)
	page := successfulPage
	for page.NextCursor != "" {
		page, err = observer.EnumeratePage(context.Background(), rootID, "", page.NextCursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		seen += len(page.Items)
	}
	if seen != 4 {
		t.Fatalf("concurrent replay continuation returned %d items, want 4", seen)
	}
}

func TestObserveEnumerationCancellationInvalidatesCursor(t *testing.T) {
	for _, test := range []struct {
		name        string
		cancelAfter int
	}{
		{name: "before child", cancelAfter: 1},
		{name: "after child", cancelAfter: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			observer, root, rootID := newTestObserver(t, false)
			for _, name := range []string{"one.mkv", "two.mkv", "three.mkv", "four.mkv"} {
				writeFile(t, filepath.Join(root, name), name)
			}
			first, err := observer.Enumerate(context.Background(), rootID, "", 1)
			if err != nil {
				t.Fatal(err)
			}
			cancelContext := &cancelAfterChecksContext{cancelAfter: test.cancelAfter}
			if _, err := observer.EnumeratePage(cancelContext, rootID, "", first.NextCursor, 1); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled continuation error = %v, want context.Canceled", err)
			}
			if _, err := observer.EnumeratePage(context.Background(), rootID, "", first.NextCursor, 1); !errors.Is(err, ErrEnumerationStale) {
				t.Fatalf("canceled cursor retry error = %v, want ErrEnumerationStale", err)
			}
			if test.cancelAfter == 3 && cancelContext.checks < 4 {
				t.Fatalf("after-child context checks = %d, want post-child check", cancelContext.checks)
			}

			page, err := observer.Enumerate(context.Background(), rootID, "", 1)
			if err != nil {
				t.Fatal(err)
			}
			seen := len(page.Items)
			for page.NextCursor != "" {
				page, err = observer.EnumeratePage(context.Background(), rootID, "", page.NextCursor, 1)
				if err != nil {
					t.Fatal(err)
				}
				seen += len(page.Items)
			}
			if seen != 4 {
				t.Fatalf("restart after %s returned %d items, want 4", test.name, seen)
			}
		})
	}
}

func TestObserveEnumerationCancellationInvalidatesQueuedContinuation(t *testing.T) {
	observer, root, rootID := newTestObserver(t, false)
	for _, name := range []string{"one.mkv", "two.mkv", "three.mkv", "four.mkv"} {
		writeFile(t, filepath.Join(root, name), name)
	}

	first, err := observer.Enumerate(context.Background(), rootID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	cursorValue, err := decodeCursor(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	state, found := observer.lockEnumerationCursor(cursorValue.CursorID.String())
	if !found {
		t.Fatal("first page did not retain a cursor state")
	}

	cancelContext := newQueuedCancellationContext()
	type result struct {
		page ports.Page[domain.FileManifestEntry]
		err  error
	}
	activeResult := make(chan result, 1)
	go func() {
		page, callErr := observer.EnumeratePage(cancelContext, rootID, "", first.NextCursor, 1)
		activeResult <- result{page: page, err: callErr}
	}()
	waitForCursorWaiter(t, state)
	state.mu.Unlock()

	<-cancelContext.readCheck
	queuedResult := make(chan result, 1)
	go func() {
		page, callErr := observer.EnumeratePage(context.Background(), rootID, "", first.NextCursor, 1)
		queuedResult <- result{page: page, err: callErr}
	}()
	waitForCursorWaiter(t, state)
	close(cancelContext.release)

	active := <-activeResult
	if !errors.Is(active.err, context.Canceled) {
		t.Fatalf("active canceled continuation error = %v, want context.Canceled", active.err)
	}
	if len(active.page.Items) != 0 || active.page.NextCursor != "" {
		t.Fatalf("active canceled continuation returned page state = %#v", active.page)
	}
	queued := <-queuedResult
	if !errors.Is(queued.err, ErrEnumerationStale) {
		t.Fatalf("queued continuation error = %v, want ErrEnumerationStale", queued.err)
	}
	if len(queued.page.Items) != 0 || queued.page.NextCursor != "" {
		t.Fatalf("queued stale continuation returned page state = %#v", queued.page)
	}
	if _, err := observer.EnumeratePage(context.Background(), rootID, "", first.NextCursor, 1); !errors.Is(err, ErrEnumerationStale) {
		t.Fatalf("canceled cursor retry error = %v, want ErrEnumerationStale", err)
	}
}

func TestObserveEnumerationCoverageCountIsCumulative(t *testing.T) {
	for _, total := range []int{1, 2, 3, 4, 5} {
		t.Run(fmt.Sprintf("files-%d", total), func(t *testing.T) {
			observer, root, rootID := newTestObserver(t, false)
			for index := 0; index < total; index++ {
				writeFile(t, filepath.Join(root, fmt.Sprintf("%02d.mkv", index)), "content")
			}
			page, err := observer.Enumerate(context.Background(), rootID, "", 2)
			if err != nil {
				t.Fatal(err)
			}
			var seen int64
			for {
				seen += int64(len(page.Items))
				if page.Coverage.ObservedCount != seen {
					t.Fatalf("coverage count = %d after %d items, want cumulative count", page.Coverage.ObservedCount, seen)
				}
				if page.NextCursor == "" {
					if page.Coverage.ObservedCount != int64(total) {
						t.Fatalf("terminal coverage count = %d, want %d", page.Coverage.ObservedCount, total)
					}
					if page.Coverage.Completeness != domain.CompletenessComplete {
						t.Fatalf("terminal completeness = %q, want complete", page.Coverage.Completeness)
					}
					break
				}
				page, err = observer.EnumeratePage(context.Background(), rootID, "", page.NextCursor, 2)
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestObserveEnumerationCursorTraversesBeyondPageMaximum(t *testing.T) {
	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	rootID := domain.ConfigID("downloads")
	observer, err := New([]Root{{ID: rootID, Path: root}}, Options{EnumerationLimit: 2, EnumerationMax: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"01.mkv", "02.mkv", "03.mkv", "04.mkv", "05.mkv"} {
		writeFile(t, filepath.Join(root, name), name)
	}

	for _, firstLimit := range []int{1, 2} {
		var all []domain.FileManifestEntry
		page, err := observer.Enumerate(context.Background(), rootID, "", firstLimit)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page.Items...)
		for page.NextCursor != "" {
			page, err = observer.EnumeratePage(context.Background(), rootID, "", page.NextCursor, 3-firstLimit)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, page.Items...)
		}
		if len(all) != 5 {
			t.Fatalf("first limit %d returned %d entries: %#v", firstLimit, len(all), all)
		}
		paths := make([]string, 0, len(all))
		seen := make(map[string]struct{}, len(all))
		for _, entry := range all {
			if _, exists := seen[entry.RelativePath]; exists {
				t.Fatalf("first limit %d repeated entry %q", firstLimit, entry.RelativePath)
			}
			seen[entry.RelativePath] = struct{}{}
			paths = append(paths, entry.RelativePath)
		}
		sort.Strings(paths)
		for index, expected := range []string{"01.mkv", "02.mkv", "03.mkv", "04.mkv", "05.mkv"} {
			if paths[index] != expected {
				t.Fatalf("first limit %d paths = %#v, want ordered synthetic files", firstLimit, paths)
			}
		}
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

func TestObserveCapabilitiesHonorConfiguredAuthority(t *testing.T) {
	type capabilityExpectation struct {
		name  string
		state domain.CapabilityState
	}
	tests := []struct {
		name       string
		readOnly   bool
		configured []domain.Capability
		want       []capabilityExpectation
	}{
		{
			name: "writable source destination operations remain unknown",
			want: []capabilityExpectation{
				{name: "fs.copy", state: domain.CapabilitySupported},
				{name: "fs.hardlink", state: domain.CapabilityUnknown},
				{name: "fs.move", state: domain.CapabilityUnknown},
				{name: "fs.rename", state: domain.CapabilityUnknown},
			},
		},
		{
			name: "configured unsupported and unknown are retained",
			configured: []domain.Capability{
				{Name: "fs.copy", State: domain.CapabilityUnsupported, Reason: "mount forbids copy", ObservedAt: time.Now()},
				{Name: "fs.delete", State: domain.CapabilityUnknown, Reason: "not verified", ObservedAt: time.Now()},
			},
			want: []capabilityExpectation{
				{name: "fs.copy", state: domain.CapabilityUnsupported},
				{name: "fs.delete", state: domain.CapabilityUnknown},
			},
		},
		{
			name:     "read only forbids every mutation",
			readOnly: true,
			configured: []domain.Capability{
				{Name: "fs.hardlink", State: domain.CapabilitySupported, ObservedAt: time.Now()},
				{Name: "fs.move", State: domain.CapabilityUnknown, ObservedAt: time.Now()},
			},
			want: []capabilityExpectation{
				{name: "fs.copy", state: domain.CapabilityUnsupported},
				{name: "fs.hardlink", state: domain.CapabilityUnsupported},
				{name: "fs.move", state: domain.CapabilityUnsupported},
				{name: "fs.rename", state: domain.CapabilityUnsupported},
				{name: "fs.trash", state: domain.CapabilityUnsupported},
				{name: "fs.restore", state: domain.CapabilityUnsupported},
				{name: "fs.delete", state: domain.CapabilityUnsupported},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if resolved, err := filepath.EvalSymlinks(root); err == nil {
				root = resolved
			}
			rootID := domain.ConfigID("downloads")
			observer, err := New([]Root{{ID: rootID, Path: root, ReadOnly: test.readOnly, Capabilities: test.configured}}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			caps, err := observer.Capabilities(context.Background(), rootID)
			if err != nil {
				t.Fatal(err)
			}
			states := make(map[string]domain.CapabilityState, len(caps))
			for _, capability := range caps {
				states[capability.Name] = capability.State
			}
			for _, want := range test.want {
				if states[want.name] != want.state {
					t.Fatalf("capability %q = %q, want %q; all=%#v", want.name, states[want.name], want.state, states)
				}
			}
		})
	}
}

type cancelAfterChecksContext struct {
	cancelAfter int
	checks      int
}

type queuedCancellationContext struct {
	readCheck chan struct{}
	release   chan struct{}
	once      sync.Once
	checks    atomic.Int32
}

func newQueuedCancellationContext() *queuedCancellationContext {
	return &queuedCancellationContext{readCheck: make(chan struct{}), release: make(chan struct{})}
}

func (ctx *queuedCancellationContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (ctx *queuedCancellationContext) Done() <-chan struct{} {
	return nil
}

func (ctx *queuedCancellationContext) Err() error {
	if ctx.checks.Add(1) != 2 {
		return nil
	}
	ctx.once.Do(func() { close(ctx.readCheck) })
	<-ctx.release
	return context.Canceled
}

func (ctx *queuedCancellationContext) Value(any) any {
	return nil
}

func waitForCursorWaiter(t *testing.T, state *enumerationCursorState) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for state.waiters.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for continuation to queue on cursor state")
		}
		runtime.Gosched()
	}
}

func (ctx *cancelAfterChecksContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (ctx *cancelAfterChecksContext) Done() <-chan struct{} {
	return nil
}

func (ctx *cancelAfterChecksContext) Err() error {
	ctx.checks++
	if ctx.checks > ctx.cancelAfter {
		return context.Canceled
	}
	return nil
}

func (ctx *cancelAfterChecksContext) Value(any) any {
	return nil
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
