//go:build aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris

package credentials

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestExplicitKeyFileRejectsFIFOAndNeverGeneratesFallback(t *testing.T) {
	dataDir := t.TempDir()
	fifo := filepath.Join(t.TempDir(), "key-pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKey(KeyOptions{DataDir: dataDir, KeyFile: fifo, KeyFileProvided: true}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("FIFO key source = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "keys", "credentials.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("FIFO key source created fallback: %v", err)
	}
}
