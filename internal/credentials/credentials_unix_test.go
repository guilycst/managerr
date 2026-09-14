//go:build aix || android || darwin || dragonfly || freebsd || hurd || illumos || ios || linux || netbsd || openbsd || solaris

package credentials

import (
	"bytes"
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

func TestReadBoundedDescriptorUsesOpenedObject(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "key")
	original := []byte("original descriptor bytes")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.Rename(path, filepath.Join(directory, "key.old")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement path bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readBoundedDescriptor(file)
	if err != nil {
		t.Fatalf("read opened descriptor: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("descriptor bytes = %q, want %q", got, original)
	}
}
