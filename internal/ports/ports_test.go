package ports

import (
	"testing"
	"time"

	"github.com/guilycst/managerr/internal/domain"
)

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
		Digest:       "sha256:fixture",
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
	directory := entry
	directory.Type = domain.ManifestDirectory
	hardlink.Files[0].Source = directory
	if err := hardlink.Validate(); err == nil {
		t.Fatal("directory hardlink was accepted")
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
