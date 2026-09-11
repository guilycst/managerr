//go:build darwin || linux

package observe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/guilycst/managerr/internal/domain"
	"golang.org/x/sys/unix"
)

func TestObserveRejectsSpecialFile(t *testing.T) {
	observer, root, rootID := newTestObserver(t, false)
	name := filepath.Join(root, "pipe")
	if err := unix.Mkfifo(name, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := observer.Stat(context.Background(), domain.FileTarget{RootID: rootID, RelativePath: "pipe"}); !errors.Is(err, ErrSpecialFile) {
		t.Fatalf("special file error = %v, want ErrSpecialFile", err)
	}
}

func TestObserveEnumerationSkipsUnsupportedChildren(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "mg-f01-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}
	rootID := domain.ConfigID("downloads")
	observer, err := New([]Root{{ID: rootID, Path: root}}, Options{EnumerationLimit: 100, EnumerationMax: 100})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "a.mkv"), "a")
	writeFile(t, filepath.Join(root, "e.mkv"), "e")
	outside := filepath.Join(t.TempDir(), "outside.mkv")
	writeFile(t, outside, "outside")
	if err := os.Symlink(outside, filepath.Join(root, "b-link.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "c-pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(root, "d-socket")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	var items []domain.FileManifestEntry
	var reasons []UnsupportedChildEvidence
	page, err := observer.Enumerate(context.Background(), rootID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	for {
		items = append(items, page.Items...)
		for _, code := range page.Coverage.ReasonCodes {
			if evidence, ok := ParseUnsupportedChildReasonCode(code); ok {
				reasons = append(reasons, evidence)
			}
		}
		if page.NextCursor == "" {
			break
		}
		page, err = observer.EnumeratePage(context.Background(), rootID, "", page.NextCursor, 1)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(items) != 2 || items[0].RelativePath != "a.mkv" || items[1].RelativePath != "e.mkv" {
		t.Fatalf("supported entries = %#v, want a.mkv and e.mkv", items)
	}
	gotReasons := make(map[string]string, len(reasons))
	for _, reason := range reasons {
		gotReasons[reason.RelativePath] = reason.Reason
	}
	for path, want := range map[string]string{
		"b-link.mkv": "symlink",
		"c-pipe":     "special_file",
		"d-socket":   "special_file",
	} {
		if gotReasons[path] != want {
			t.Fatalf("unsupported child %q reason = %q, want %q; all=%#v", path, gotReasons[path], want, gotReasons)
		}
	}
	if page.Coverage.Completeness != domain.CompletenessPartial {
		t.Fatalf("coverage completeness = %q, want partial for unsupported evidence", page.Coverage.Completeness)
	}
}

func TestObserveRejectsSymlinkSwapDuringNestedResolution(t *testing.T) {
	observer, root, rootID := newTestObserver(t, false)
	inside := filepath.Join(root, "inside")
	outside := t.TempDir()
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(inside, "movie.mkv"), "inside")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := observer.Stat(context.Background(), domain.FileTarget{RootID: rootID, RelativePath: "link/movie.mkv"}); !errors.Is(err, ErrSymlink) {
		t.Fatalf("nested symlink error = %v, want ErrSymlink", err)
	}
}

func TestObserveNeverFollowsDirectorySymlinkRace(t *testing.T) {
	observer, root, rootID := newTestObserver(t, false)
	inside := filepath.Join(root, "swap")
	outside := t.TempDir()
	if err := os.Mkdir(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(inside, "movie.mkv"), "inside")
	writeFile(t, filepath.Join(outside, "movie.mkv"), "outside")
	expected := sha256.Sum256([]byte("inside"))
	expectedHash := "sha256:" + hex.EncodeToString(expected[:])

	var stop sync.WaitGroup
	stop.Add(1)
	go func() {
		defer stop.Done()
		for i := 0; i < 250; i++ {
			realPath := filepath.Join(root, "swap-real")
			if err := os.Rename(inside, realPath); err != nil {
				continue
			}
			_ = os.Symlink(outside, inside)
			_ = os.Remove(inside)
			_ = os.Rename(realPath, inside)
		}
	}()
	for i := 0; i < 250; i++ {
		hash, err := observer.Hash(context.Background(), domain.FileTarget{RootID: rootID, RelativePath: "swap/movie.mkv"})
		if err == nil && hash != expectedHash {
			t.Fatalf("race returned outside content: %q", hash)
		}
		if !errors.Is(err, ErrSymlink) && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, ErrChanged) && err != nil {
			// Permission and transient lookup errors are not proof of escape;
			// unexpected successful reads are checked above.
			continue
		}
	}
	stop.Wait()
	insideHash, err := observer.Hash(context.Background(), domain.FileTarget{RootID: rootID, RelativePath: "swap/movie.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if insideHash != expectedHash {
		t.Fatalf("final in-root hash = %q, want %q", insideHash, expectedHash)
	}
}
