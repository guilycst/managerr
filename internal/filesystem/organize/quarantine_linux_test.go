//go:build linux

package organize

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteQuarantineCleanupFailsClosedAfterNestedPayloadExchange(t *testing.T) {
	if organizeCleanupSupported {
		t.Skip("target has a reviewed inode-bound quarantine removal primitive")
	}
	root := canonicalTempDir(t)
	quarantinePath := filepath.Join(root, "quarantine")
	mustMkdir(t, quarantinePath)
	payloadPath := filepath.Join(quarantinePath, "payload")
	approvedPath := filepath.Join(quarantinePath, "approved-spare")
	writeSynthetic(t, payloadPath, "approved payload")
	approvedInfo, err := os.Stat(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(quarantinePath)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	exchanged := false
	err = removeOwnedQuarantineWithHook("delete-nested-exchange", 0, parent, "payload", approvedInfo, func() {
		exchanged = true
		if err := os.Rename(payloadPath, approvedPath); err != nil {
			t.Fatalf("exchange approved payload: %v", err)
		}
		writeSynthetic(t, payloadPath, "unapproved replacement")
	})
	if !exchanged {
		t.Fatal("nested payload exchange hook was not called after identity validation")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want unsupported", err)
	}
	approvedAfter, err := os.Stat(approvedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(approvedInfo, approvedAfter) {
		t.Fatal("approved payload inode changed during failed cleanup")
	}
	replacementAfter, err := os.Stat(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(approvedInfo, replacementAfter) {
		t.Fatal("replacement payload unexpectedly reused approved inode")
	}
	assertSynthetic(t, approvedPath, "approved payload")
	assertSynthetic(t, payloadPath, "unapproved replacement")
}

func TestMoveQuarantinePayloadCleanupFailsClosedAfterNestedEntryExchange(t *testing.T) {
	if organizeCleanupSupported {
		t.Skip("target has a reviewed inode-bound quarantine removal primitive")
	}
	root := canonicalTempDir(t)
	quarantinePath := filepath.Join(root, "quarantine")
	mustMkdir(t, quarantinePath)
	payloadPath := filepath.Join(quarantinePath, "payload")
	approvedPath := filepath.Join(quarantinePath, "approved-spare")
	writeSynthetic(t, payloadPath, "approved payload")
	approvedInfo, err := os.Stat(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(quarantinePath)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	exchanged := false
	err = removeOwnedQuarantineWithHook("move-nested-exchange", 0, parent, "payload", approvedInfo, func() {
		exchanged = true
		if err := os.Rename(payloadPath, approvedPath); err != nil {
			t.Fatalf("exchange approved payload: %v", err)
		}
		writeSynthetic(t, payloadPath, "unapproved replacement")
	})
	if !exchanged {
		t.Fatal("nested payload exchange hook was not called after identity validation")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want unsupported", err)
	}
	approvedAfter, err := os.Stat(approvedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(approvedInfo, approvedAfter) {
		t.Fatal("approved payload inode changed during failed cleanup")
	}
	replacementAfter, err := os.Stat(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(approvedInfo, replacementAfter) {
		t.Fatal("replacement payload unexpectedly reused approved inode")
	}
	assertSynthetic(t, approvedPath, "approved payload")
	assertSynthetic(t, payloadPath, "unapproved replacement")
}

func TestMoveQuarantineCleanupFailsClosedAfterNestedDirectoryExchange(t *testing.T) {
	if organizeCleanupSupported {
		t.Skip("target has a reviewed inode-bound quarantine removal primitive")
	}
	root := canonicalTempDir(t)
	quarantinePath := filepath.Join(root, "quarantine")
	mustMkdir(t, quarantinePath)
	approvedInfo, err := os.Stat(quarantinePath)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	replacementPath := filepath.Join(root, "replacement-spare")
	exchanged := false
	err = removePrivateDirectoryWithHook(parent, "quarantine", approvedInfo, func() {
		exchanged = true
		if err := os.Rename(quarantinePath, replacementPath); err != nil {
			t.Fatalf("exchange approved quarantine: %v", err)
		}
		mustMkdir(t, quarantinePath)
	})
	if !exchanged {
		t.Fatal("nested quarantine exchange hook was not called after identity validation")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("error = %v, want unsupported", err)
	}
	replacementAfter, err := os.Stat(replacementPath)
	if err != nil {
		t.Fatalf("approved quarantine after failed cleanup = %v", err)
	}
	quarantineAfter, err := os.Stat(quarantinePath)
	if err != nil {
		t.Fatalf("replacement quarantine after failed cleanup = %v", err)
	}
	if !os.SameFile(approvedInfo, replacementAfter) {
		t.Fatal("approved quarantine inode changed during failed cleanup")
	}
	if os.SameFile(approvedInfo, quarantineAfter) {
		t.Fatal("replacement quarantine unexpectedly reused approved inode")
	}
}
