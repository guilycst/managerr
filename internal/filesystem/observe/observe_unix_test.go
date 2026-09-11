//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package observe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
