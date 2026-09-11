package ports

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/guilycst/managerr/internal/domain"
)

func TestParseUnsupportedChildReasonCode(t *testing.T) {
	payload, err := json.Marshal(UnsupportedChildEvidence{RelativePath: "Season 1/subtitle.ass", Reason: "permission_denied"})
	if err != nil {
		t.Fatal(err)
	}
	evidence, ok := ParseUnsupportedChildReasonCode("unsupported_child:" + string(payload))
	if !ok || evidence.RelativePath != "Season 1/subtitle.ass" || evidence.Reason != "permission_denied" {
		t.Fatalf("evidence = %#v, ok = %v", evidence, ok)
	}
	if _, ok := ParseUnsupportedChildReasonCode("unsupported_child:{"); ok {
		t.Fatal("malformed reason was accepted")
	}
	if _, ok := ParseUnsupportedChildReasonCode("enumeration_limit"); ok {
		t.Fatal("unrelated reason was accepted")
	}
}

func TestFilesystemRequestsBindExactManifests(t *testing.T) {
	root, err := domain.ParseConfigID("downloads")
	if err != nil {
		t.Fatal(err)
	}
	destinationRoot, err := domain.ParseConfigID("movies")
	if err != nil {
		t.Fatal(err)
	}
	entry := domain.FileManifestEntry{
		RootID:       root,
		RelativePath: "Example Film/movie.mkv",
		Type:         domain.ManifestFile,
		Size:         42,
		Digest:       "sha256:" + strings.Repeat("0", 64),
		FileIdentity: "fixture-inode",
		ObservedAt:   time.Now(),
	}
	request := FilesystemCopyRequest{Files: []FileMap{{
		Source:      entry,
		Destination: domain.FileTarget{RootID: destinationRoot, RelativePath: "Example Film/movie.mkv"},
	}}}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	hardlink := FilesystemHardlinkRequest{Files: request.Files}
	if err := hardlink.Validate(); err != nil {
		t.Fatal(err)
	}
	directory := domain.FileManifestEntry{
		RootID:       root,
		RelativePath: "Example Film",
		Type:         domain.ManifestDirectory,
		FileIdentity: "fixture-directory",
		ObservedAt:   entry.ObservedAt,
		Children:     []domain.FileManifestEntry{entry},
	}
	directoryRequest := FilesystemCopyRequest{Files: []FileMap{{
		Source:      directory,
		Destination: domain.FileTarget{RootID: destinationRoot, RelativePath: "Example Film"},
	}}}
	if err := directoryRequest.Validate(); err != nil {
		t.Fatalf("exact directory manifest rejected: %v", err)
	}
	if err := (FilesystemCopyRequest{Files: []FileMap{{
		Source:      directory,
		Destination: domain.FileTarget{RootID: root, RelativePath: "Example Film/copy"},
	}}}).Validate(); err == nil {
		t.Fatal("copy accepted a directory destination inside its source")
	}
	nestedDirectory := directory
	nestedDirectory.RelativePath = "Example Film/Sub"
	nestedDirectory.Children = []domain.FileManifestEntry{entry}
	nestedDirectory.Children[0].RelativePath = "Example Film/Sub/movie.mkv"
	if err := (FilesystemMoveRequest{Files: []FileMap{{
		Source:      nestedDirectory,
		Destination: domain.FileTarget{RootID: root, RelativePath: "Example Film"},
	}}}).Validate(); err == nil {
		t.Fatal("move accepted a directory destination above its source")
	}
	directoryHardlink := FilesystemHardlinkRequest{Files: directoryRequest.Files}
	if err := directoryHardlink.Validate(); err == nil {
		t.Fatal("directory hardlink was accepted")
	}
	withoutIdentity := entry
	withoutIdentity.FileIdentity = ""
	if err := (FilesystemCopyRequest{Files: []FileMap{{
		Source:      withoutIdentity,
		Destination: domain.FileTarget{RootID: destinationRoot, RelativePath: "Example Film/no-identity.mkv"},
	}}}).Validate(); err == nil {
		t.Fatal("copy accepted a source without identity evidence")
	}
	withoutDigest := entry
	withoutDigest.Digest = ""
	if err := (FilesystemCopyRequest{Files: []FileMap{{
		Source:      withoutDigest,
		Destination: domain.FileTarget{RootID: destinationRoot, RelativePath: "Example Film/no-digest.mkv"},
	}}}).Validate(); err == nil {
		t.Fatal("copy accepted a source without a strong digest")
	}
	hardlinkWithoutDigest := FilesystemHardlinkRequest{Files: []FileMap{{
		Source:      withoutDigest,
		Destination: domain.FileTarget{RootID: destinationRoot, RelativePath: "Example Film/hardlink.mkv"},
	}}}
	if err := hardlinkWithoutDigest.Validate(); err != nil {
		t.Fatalf("hardlink incorrectly required a content digest: %v", err)
	}
	if err := (FilesystemMoveRequest{Files: hardlinkWithoutDigest.Files}).Validate(); err != nil {
		t.Fatalf("move incorrectly required a content digest: %v", err)
	}
	if err := (FilesystemRenameRequest{Files: hardlinkWithoutDigest.Files}).Validate(); err != nil {
		t.Fatalf("rename incorrectly required a content digest: %v", err)
	}
	chainFirst := entry
	chainFirst.RelativePath = "Example Film/first.mkv"
	chainSecond := entry
	chainSecond.RelativePath = "Example Film/second.mkv"
	if err := (FilesystemMoveRequest{Files: []FileMap{
		{Source: chainFirst, Destination: domain.FileTarget{RootID: root, RelativePath: chainSecond.RelativePath}},
		{Source: chainSecond, Destination: domain.FileTarget{RootID: root, RelativePath: "Example Film/final.mkv"}},
	}}).Validate(); err == nil {
		t.Fatal("move accepted a source-to-destination alias chain")
	}
	if err := (FilesystemCopyRequest{}).Validate(); err == nil {
		t.Fatal("empty filesystem map was accepted")
	}

	duplicate := request
	duplicate.Files = append(duplicate.Files, request.Files[0])
	if err := duplicate.Validate(); err == nil {
		t.Fatal("duplicate filesystem map was accepted")
	}
}
