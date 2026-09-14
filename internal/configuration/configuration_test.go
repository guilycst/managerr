package configuration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guilycst/mastarr/internal/credentials"
	"github.com/guilycst/mastarr/internal/domain"
)

var testStartup = time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)

func TestParseYAMLStrictlyValidatesSnapshotAndAliases(t *testing.T) {
	parsed, err := ParseYAML([]byte(validYAML()), ParseOptions{DocumentID: "mastarr.yaml", StartupAt: testStartup})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Snapshot.Source.Source != domain.SourceYAML || parsed.Snapshot.Source.Editable {
		t.Fatalf("source metadata = %#v", parsed.Snapshot.Source)
	}
	if parsed.Snapshot.Source.DocumentID != "mastarr.yaml" || parsed.Snapshot.Source.ReloadPolicy != domain.ReloadOnRestart {
		t.Fatalf("source identity = %#v", parsed.Snapshot.Source)
	}
	if parsed.Policy.TrashRetention != 720*time.Hour || parsed.Policy.JanitorInterval != time.Hour {
		t.Fatalf("policy = %#v", parsed.Policy)
	}
	if got := parsed.Snapshot.PathMappings[0]; got.SourcePrefix != "/downloads" || got.DestinationPrefix != "" {
		t.Fatalf("mapping aliases = %#v", got)
	}

	unknown := []byte("version: 1\nunknown: true\n")
	if _, err := ParseYAML(unknown, ParseOptions{StartupAt: testStartup}); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("unknown YAML field error = %v", err)
	}
	multiple := []byte("version: 1\n---\nversion: 1\n")
	if _, err := ParseYAML(multiple, ParseOptions{StartupAt: testStartup}); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("multiple YAML documents error = %v", err)
	}
}

func TestParseYAMLResolvesStaticReferencesOnceAndRedactsFailures(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "radarr-key")
	resolver := NewStaticResolver(map[string]string{"QBT_USER": "synthetic-user"}, map[string][]byte{secretPath: []byte("synthetic-file-secret")})
	yaml := `version: 1
connections:
  - id: radarr-main
    kind: radarr
    label: Movies
    endpoint: http://radarr.invalid:7878
    credentials:
      apiKey:
        file: ` + secretPath + `
      username:
        env: QBT_USER
storageRoots:
  - id: movies
    label: Movies
    purpose: library
    path: /media/movies
`
	parsed, err := ParseYAML([]byte(yaml), ParseOptions{SecretResolver: resolver, StartupAt: testStartup})
	if err != nil {
		t.Fatal(err)
	}
	if string(parsed.StaticCredentials[CredentialKey{ConnectionID: "radarr-main", Field: "apiKey"}]) != "synthetic-file-secret" {
		t.Fatal("file reference was not resolved")
	}
	if string(parsed.StaticCredentials[CredentialKey{ConnectionID: "radarr-main", Field: "username"}]) != "synthetic-user" {
		t.Fatal("environment reference was not resolved")
	}
	if _, err := ParseYAML([]byte(`version: 1
connections:
  - id: radarr-main
    kind: radarr
    label: Movies
    endpoint: http://radarr.invalid:7878
    credentials:
      apiKey:
        env: MISSING
`), ParseOptions{SecretResolver: resolver, StartupAt: testStartup}); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("missing reference error = %v", err)
	}
	if _, err := ParseYAML([]byte(`version: 1
connections:
  - id: radarr-main
    kind: radarr
    label: Movies
    endpoint: http://radarr.invalid:7878
    credentials:
      apiKey:
        env: QBT_USER
        file: /run/secrets/nope
`), ParseOptions{SecretResolver: resolver, StartupAt: testStartup}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("ambiguous reference error = %v", err)
	}
}

func TestManagerAppliesYAMLAtomicallyAndRetiresRemovedResources(t *testing.T) {
	manager, err := New(Options{Now: func() time.Time { return testStartup }, YAML: []byte(validYAML()), YAMLDocumentID: "mastarr.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	before, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	invalid := []byte("version: 1\nconnections:\n  - id: duplicate\n    kind: radarr\n    label: one\n    endpoint: http://radarr.invalid\n  - id: duplicate\n    kind: radarr\n    label: two\n    endpoint: http://radarr.invalid\nstorageRoots:\n  - id: movies\n    label: Movies\n    purpose: library\n    path: /media/movies\n")
	if err := manager.ApplyYAML(context.Background(), invalid); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("invalid apply error = %v", err)
	}
	afterInvalid, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.Source.Revision != afterInvalid.Source.Revision || len(afterInvalid.Connections) != len(before.Connections) {
		t.Fatal("invalid YAML changed active snapshot")
	}
	removed := `version: 1
connections:
  - id: radarr-main
    kind: radarr
    label: Movies
    endpoint: http://radarr.invalid:7878
storageRoots:
  - id: movies
    label: Movies
    purpose: library
    path: /media/movies
`
	if err := manager.ApplyYAML(context.Background(), []byte(removed)); err != nil {
		t.Fatal(err)
	}
	active, err := manager.GetConnection(context.Background(), "qbt-main", false)
	if !errors.Is(err, ErrResourceNotFound) || active.ID != "" {
		t.Fatalf("removed active connection = %#v, err %v", active, err)
	}
	historical, err := manager.GetConnection(context.Background(), "qbt-main", true)
	if err != nil || historical.RetiredAt == nil || historical.Source.Source != domain.SourceYAML {
		t.Fatalf("retired connection = %#v, err %v", historical, err)
	}
	if _, err := manager.CreateConnection(context.Background(), ConnectionSpec{ID: "radarr-main", Kind: domain.ConnectionRadarr, Label: "duplicate", Endpoint: "http://other.invalid"}); !errors.Is(err, ErrConfigSourceConflict) {
		t.Fatalf("YAML ownership collision = %v", err)
	}
}

func TestManagerValidatesMergedYAMLBeforeActivationAndInvalidatesAuthorityChanges(t *testing.T) {
	apiSource := domain.SourceMetadata{Source: domain.SourceAPI, Editable: true, DocumentID: "api", Revision: "api-revision", StartupAt: testStartup, ReloadPolicy: domain.ReloadOnRestart}
	manager, err := New(Options{
		Now:  func() time.Time { return testStartup },
		YAML: []byte(validYAML()),
		APIState: APIState{
			StorageRoots: []domain.StorageRoot{{ID: "staging", Label: "Staging", Purpose: domain.StorageLibrary, Path: "/media/staging", Source: apiSource, Revision: "staging-revision"}},
			PathMappings: []domain.PathMapping{{ID: "api-radarr", ConnectionID: "radarr-main", RootID: "staging", SourcePrefix: "/incoming", DestinationPrefix: "", Source: apiSource, Revision: "mapping-revision"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	before, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	removedConnection := `version: 1
connections:
  - id: qbt-main
    kind: qbittorrent
    label: Torrents
    endpoint: http://qbittorrent.invalid:8080
storageRoots:
  - id: downloads
    label: Downloads
    path: /media/downloads
    purpose: download
    watch:
      enabled: true
      interval: 5m
  - id: movies
    label: Movies
    path: /media/movies
    purpose: library
`
	if err := manager.ApplyYAML(context.Background(), []byte(removedConnection)); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("YAML removal with API dependency error = %v", err)
	}
	afterRejected, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if before.Source.Revision != afterRejected.Source.Revision || len(afterRejected.Connections) != len(before.Connections) {
		t.Fatal("invalid merged YAML changed active snapshot")
	}

	var changes []RevisionChange
	manager2, err := New(Options{
		Now:  func() time.Time { return testStartup },
		YAML: []byte(validYAML()),
		Invalidator: func(_ context.Context, change RevisionChange) error {
			changes = append(changes, change)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager2.Close()
	updatedYAML := strings.Replace(validYAML(), "http://radarr.invalid:7878", "http://radarr-new.invalid:7878", 1)
	if err := manager2.ApplyYAML(context.Background(), []byte(updatedYAML)); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Kind != ResourceConnection || changes[0].ID != "radarr-main" || !changes[0].AuthorityChanged || !slicesEqual(changes[0].ChangedFields, []string{"endpoint"}) {
		t.Fatalf("YAML authority invalidation = %#v", changes)
	}
}

func TestManagerAPIETagAndManagedCredentialLifecycle(t *testing.T) {
	key := bytes.Repeat([]byte{0x2a}, 32)
	crypt, err := credentials.NewManager(key)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryCredentialStore()
	var changes []RevisionChange
	var changesMu sync.Mutex
	manager, err := New(Options{
		Now:               func() time.Time { return testStartup },
		CredentialManager: crypt,
		CredentialStore:   store,
		Invalidator: func(_ context.Context, change RevisionChange) error {
			changesMu.Lock()
			defer changesMu.Unlock()
			changes = append(changes, change)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	connection, err := manager.CreateConnection(context.Background(), ConnectionSpec{
		ID:       "radarr-main",
		Kind:     domain.ConnectionRadarr,
		Label:    "Movies",
		Endpoint: "http://radarr.invalid:7878",
		Credentials: map[string]CredentialInput{
			"apiKey": {Value: []byte("synthetic-api-key")},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if connection.Source.Source != domain.SourceAPI || !connection.Source.Editable {
		t.Fatalf("API source metadata = %#v", connection.Source)
	}
	envelopes, err := store.Load(context.Background(), "radarr-main")
	if err != nil {
		t.Fatal(err)
	}
	if string(envelopes["apiKey"].Ciphertext) == "synthetic-api-key" || len(envelopes["apiKey"].Ciphertext) == 0 {
		t.Fatal("managed credential was not encrypted")
	}
	resolved, err := manager.ResolveCredential(context.Background(), "radarr-main", "apiKey")
	if err != nil || string(resolved) != "synthetic-api-key" {
		t.Fatalf("managed credential resolve = %q, err %v", resolved, err)
	}
	zero(resolved)
	metadata, err := manager.CredentialMetadata(context.Background(), "radarr-main", "apiKey")
	if err != nil || !metadata.Configured || metadata.KeyFingerprint == "" {
		t.Fatalf("credential metadata = %#v, err %v", metadata, err)
	}
	oldRevision := connection.Revision
	if _, err := manager.UpdateConnection(context.Background(), connection.ID, "stale", ConnectionPatch{}); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("stale revision error = %v", err)
	}
	rotated, err := manager.UpdateConnection(context.Background(), connection.ID, oldRevision, ConnectionPatch{Credentials: map[string]CredentialInput{"apiKey": {Value: []byte("rotated-api-key")}}})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Revision == oldRevision {
		t.Fatal("managed credential update did not advance ETag")
	}
	if len(changes) != 0 {
		t.Fatalf("secret-only rotation invalidated revisions: %#v", changes)
	}
	if _, err := manager.UpdateConnection(context.Background(), connection.ID, rotated.Revision, ConnectionPatch{Endpoint: stringPtr("http://radarr-new.invalid:7878")}); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || !changes[0].AuthorityChanged || changes[0].ChangedFields[0] != "endpoint" {
		t.Fatalf("authority change = %#v", changes)
	}
	current, err := manager.GetConnection(context.Background(), connection.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateConnection(context.Background(), connection.ID, current.Revision, ConnectionPatch{Credentials: map[string]CredentialInput{"apiKey": {Reference: &domain.CredentialReference{Kind: domain.CredentialFromFile, Value: "/tmp/arbitrary"}}}}); !errors.Is(err, ErrCredentialInvalid) {
		t.Fatalf("API secret reference error = %v", err)
	}
}

func TestManagerSnapshotIsRaceSafeAndYAMLFileIsStartupOnly(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "config.yaml")
	if err := writeTestFile(filePath, []byte(validYAML())); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Options{Now: func() time.Time { return testStartup }, YAMLPath: filePath, YAMLDocumentID: "config.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	initial, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	changed := validYAML() + "\n"
	if err := writeTestFile(filePath, []byte(changed)); err != nil {
		t.Fatal(err)
	}
	unchanged, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if initial.Source.Revision != unchanged.Source.Revision {
		t.Fatal("editing YAML file changed active snapshot without explicit apply")
	}
	var group sync.WaitGroup
	for index := 0; index < 8; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for count := 0; count < 20; count++ {
				if _, err := manager.Snapshot(context.Background()); err != nil {
					t.Errorf("snapshot: %v", err)
				}
			}
		}()
	}
	group.Wait()
}

func TestStaticCredentialIsResolvedOnlyOnExplicitReload(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "radarr-api-key")
	if err := writeTestFile(secretPath, []byte("first-secret")); err != nil {
		t.Fatal(err)
	}
	yaml := `version: 1
connections:
  - id: radarr-main
    kind: radarr
    label: Movies
    endpoint: http://radarr.invalid:7878
    credentials:
      apiKey:
        file: ` + secretPath + "\n"
	manager, err := New(Options{Now: func() time.Time { return testStartup }, YAML: []byte(yaml)})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	first, err := manager.ResolveCredential(context.Background(), "radarr-main", "apiKey")
	if err != nil || string(first) != "first-secret" {
		t.Fatalf("initial static credential = %q, err %v", first, err)
	}
	zero(first)
	if err := writeTestFile(secretPath, []byte("second-secret")); err != nil {
		t.Fatal(err)
	}
	unchanged, err := manager.ResolveCredential(context.Background(), "radarr-main", "apiKey")
	if err != nil || string(unchanged) != "first-secret" {
		t.Fatalf("credential changed without reload = %q, err %v", unchanged, err)
	}
	zero(unchanged)
	if err := manager.ApplyYAML(context.Background(), []byte(yaml)); err != nil {
		t.Fatal(err)
	}
	second, err := manager.ResolveCredential(context.Background(), "radarr-main", "apiKey")
	if err != nil || string(second) != "second-secret" {
		t.Fatalf("reloaded static credential = %q, err %v", second, err)
	}
	zero(second)
}

func validYAML() string {
	return `version: 1
connections:
  - id: radarr-main
    kind: radarr
    label: Movies
    endpoint: http://radarr.invalid:7878
  - id: qbt-main
    kind: qbittorrent
    label: Torrents
    endpoint: http://qbittorrent.invalid:8080
storageRoots:
  - id: downloads
    label: Downloads
    path: /media/downloads
    purpose: download
    watch:
      enabled: true
      interval: 5m
  - id: movies
    label: Movies
    path: /media/movies
    purpose: library
pathMappings:
  - id: radarr-downloads
    connectionId: radarr-main
    rootId: downloads
    relativePrefix: ""
    externalPrefix: /downloads
trash:
  retention: 720h
  janitorInterval: 1h
`
}

func stringPtr(value string) *string { return &value }

func writeTestFile(path string, value []byte) error {
	return os.WriteFile(path, value, 0o600)
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
