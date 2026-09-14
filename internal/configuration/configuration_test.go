package configuration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
		Now:                       func() time.Time { return testStartup },
		CredentialManager:         crypt,
		CredentialStore:           store,
		CandidateIdentityVerifier: verifiedCandidateIdentity,
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

func TestManagedCredentialRotationFailsClosedWithoutIdentityOrInvalidator(t *testing.T) {
	crypt, err := credentials.NewManager(bytes.Repeat([]byte{0x31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryCredentialStore()
	manager, err := New(Options{Now: func() time.Time { return testStartup }, CredentialManager: crypt, CredentialStore: store})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	connection, err := manager.CreateConnection(context.Background(), ConnectionSpec{
		ID: "radarr-main", Kind: domain.ConnectionRadarr, Label: "Movies", Endpoint: "http://radarr.invalid:7878",
		Credentials: map[string]CredentialInput{"apiKey": {Value: []byte("first-key")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.UpdateConnection(context.Background(), connection.ID, connection.Revision, ConnectionPatch{Credentials: map[string]CredentialInput{"apiKey": {Value: []byte("second-key")}}}); !errors.Is(err, ErrCredentialIdentityUnverified) {
		t.Fatalf("unverified credential rotation error = %v", err)
	}
	resolved, err := manager.ResolveCredential(context.Background(), connection.ID, "apiKey")
	if err != nil || string(resolved) != "first-key" {
		t.Fatalf("credential after rejected rotation = %q, err %v", resolved, err)
	}
	zero(resolved)
}

func TestManagedCredentialStoreCommitThenErrorUsesReadBackAndQuarantinesUnknownState(t *testing.T) {
	crypt, err := credentials.NewManager(bytes.Repeat([]byte{0x32}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := newFaultCredentialStore()
	store.replaceMode = replaceCommitThenError
	manager, err := New(Options{Now: func() time.Time { return testStartup }, CredentialManager: crypt, CredentialStore: store, CandidateIdentityVerifier: verifiedCandidateIdentity})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	connection, err := manager.CreateConnection(context.Background(), ConnectionSpec{
		ID: "radarr-main", Kind: domain.ConnectionRadarr, Label: "Movies", Endpoint: "http://radarr.invalid:7878",
		Credentials: map[string]CredentialInput{"apiKey": {Value: []byte("committed-key")}},
	})
	if err != nil {
		t.Fatalf("commit-then-error create = %v", err)
	}
	resolved, err := manager.ResolveCredential(context.Background(), connection.ID, "apiKey")
	if err != nil || string(resolved) != "committed-key" {
		t.Fatalf("read-back committed credential = %q, err %v", resolved, err)
	}
	zero(resolved)
	store.replaceMode = replacePartialThenError
	callCount := store.replaceCalls
	if _, err := manager.UpdateConnection(context.Background(), connection.ID, connection.Revision, ConnectionPatch{Credentials: map[string]CredentialInput{"apiKey": {Value: []byte("uncertain-rotation")}}}); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("uncertain update error = %v", err)
	}
	if store.replaceCalls != callCount+1 {
		t.Fatalf("uncertain update replace calls = %d, want %d without rollback", store.replaceCalls, callCount+1)
	}
	if _, err := manager.ResolveCredential(context.Background(), connection.ID, "apiKey"); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("quarantined update resolve error = %v", err)
	}

	store2 := newFaultCredentialStore()
	store2.replaceMode = replacePartialThenError
	manager2, err := New(Options{Now: func() time.Time { return testStartup }, CredentialManager: crypt, CredentialStore: store2, IdentityVerifier: verifiedIdentity})
	if err != nil {
		t.Fatal(err)
	}
	defer manager2.Close()
	if _, err := manager2.CreateConnection(context.Background(), ConnectionSpec{
		ID: "radarr-main", Kind: domain.ConnectionRadarr, Label: "Movies", Endpoint: "http://radarr.invalid:7878",
		Credentials: map[string]CredentialInput{"apiKey": {Value: []byte("uncertain-key")}},
	}); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("uncertain create error = %v", err)
	}
	if store2.replaceCalls != 1 {
		t.Fatalf("uncertain create replace calls = %d, want one without rollback", store2.replaceCalls)
	}
	if _, err := manager2.CreateConnection(context.Background(), ConnectionSpec{
		ID: "radarr-main", Kind: domain.ConnectionRadarr, Label: "Retry", Endpoint: "http://radarr.invalid:7878",
		Credentials: map[string]CredentialInput{"apiKey": {Value: []byte("retry-key")}},
	}); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("orphan retry error = %v", err)
	}
}

func TestPendingManagedCredentialIsUnreadableDuringReplacement(t *testing.T) {
	crypt, err := credentials.NewManager(bytes.Repeat([]byte{0x35}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := newFaultCredentialStore()
	manager, err := New(Options{
		Now:                       func() time.Time { return testStartup },
		CredentialManager:         crypt,
		CredentialStore:           store,
		CandidateIdentityVerifier: verifiedCandidateIdentity,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	connection, err := manager.CreateConnection(context.Background(), ConnectionSpec{
		ID: "radarr-pending", Kind: domain.ConnectionRadarr, Label: "Movies", Endpoint: "http://radarr.invalid:7878",
		Credentials: map[string]CredentialInput{"apiKey": {Value: []byte("initial-key")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	store.replaceEntered = entered
	store.replaceRelease = release
	updateDone := make(chan error, 1)
	go func() {
		_, updateErr := manager.UpdateConnection(context.Background(), connection.ID, connection.Revision, ConnectionPatch{
			Credentials: map[string]CredentialInput{"apiKey": {Value: []byte("pending-key")}},
		})
		updateDone <- updateErr
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("credential replacement did not reach the blocking store")
	}
	if _, err := manager.ResolveCredential(context.Background(), connection.ID, "apiKey"); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("pending credential resolve error = %v", err)
	}
	if _, err := manager.CredentialMetadata(context.Background(), connection.ID, "apiKey"); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("pending credential metadata error = %v", err)
	}
	close(release)
	select {
	case err := <-updateDone:
		if err != nil {
			t.Fatalf("completed credential update = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("credential replacement did not complete")
	}
}

func TestRejectedCredentialCASRemainsQuarantinedAfterRestart(t *testing.T) {
	key := bytes.Repeat([]byte{0x36}, 32)
	crypt, err := credentials.NewManager(key)
	if err != nil {
		t.Fatal(err)
	}
	store := newFaultCredentialStore()
	var manager *Manager
	var mutateOnce sync.Once
	manager, err = New(Options{
		Now:               func() time.Time { return testStartup },
		CredentialManager: crypt,
		CredentialStore:   store,
		CandidateIdentityVerifier: func(_ context.Context, connection domain.Connection, read CandidateCredentialReader) (IdentityVerification, error) {
			value, readErr := read("apiKey")
			if readErr != nil {
				return IdentityUnknown, readErr
			}
			if string(value) != "rejected-after-restart" {
				zero(value)
				return IdentityFailed, errors.New("candidate credential was not supplied")
			}
			zero(value)
			mutateOnce.Do(func() {
				_, _ = manager.CreateStorageRoot(context.Background(), StorageRootSpec{
					ID: "cas-race-root", Label: "CAS race", Purpose: domain.StorageLibrary, Path: "/media/cas-race",
				})
			})
			return IdentityVerified, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := manager.CreateConnection(context.Background(), ConnectionSpec{
		ID: "radarr-restart", Kind: domain.ConnectionRadarr, Label: "Movies", Endpoint: "http://radarr.invalid:7878",
		Credentials: map[string]CredentialInput{"apiKey": {Value: []byte("initial-before-cas")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := APIState{
		Connections:             []domain.Connection{connection},
		ManagedCredentialFields: map[domain.ConfigID][]string{connection.ID: {"apiKey"}},
	}
	if _, err := manager.UpdateConnection(context.Background(), connection.ID, connection.Revision, ConnectionPatch{
		Credentials: map[string]CredentialInput{"apiKey": {Value: []byte("rejected-after-restart")}},
	}); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("CAS-racing credential update error = %v", err)
	}
	manager.Close()

	crypt2, err := credentials.NewManager(key)
	if err != nil {
		t.Fatal(err)
	}
	manager2, err := New(Options{
		Now:               func() time.Time { return testStartup },
		APIState:          state,
		CredentialManager: crypt2,
		CredentialStore:   store,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager2.Close()
	if _, err := manager2.ResolveCredential(context.Background(), connection.ID, "apiKey"); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("restart quarantined credential resolve error = %v", err)
	}
	if _, err := manager2.CredentialMetadata(context.Background(), connection.ID, "apiKey"); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("restart quarantined credential metadata error = %v", err)
	}
}

func TestManagedCredentialFieldMembershipBlocksOrphans(t *testing.T) {
	crypt, err := credentials.NewManager(bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := newFaultCredentialStore()
	manager, err := New(Options{Now: func() time.Time { return testStartup }, CredentialManager: crypt, CredentialStore: store})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	connection, err := manager.CreateConnection(context.Background(), ConnectionSpec{ID: "radarr-main", Kind: domain.ConnectionRadarr, Label: "Movies", Endpoint: "http://radarr.invalid:7878"})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := crypt.Seal(connection.ID.String(), "orphan", []byte("orphan-key"))
	if err != nil {
		t.Fatal(err)
	}
	store.values[connection.ID] = map[string]credentials.Envelope{"orphan": envelope}
	if _, err := manager.ResolveCredential(context.Background(), connection.ID, "orphan"); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("orphan resolve error = %v", err)
	}
	if _, err := manager.CredentialMetadata(context.Background(), connection.ID, "orphan"); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("orphan metadata error = %v", err)
	}
}

func TestStaticCredentialValueChangeRequiresVerificationOrInvalidation(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "radarr-api-key")
	if err := writeTestFile(secretPath, []byte("first-static")); err != nil {
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
	before, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTestFile(secretPath, []byte("second-static")); err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyYAML(context.Background(), []byte(yaml)); !errors.Is(err, ErrCredentialIdentityUnverified) {
		t.Fatalf("unverified static reload error = %v", err)
	}
	afterRejected, err := manager.Snapshot(context.Background())
	if err != nil || before.Source.Revision != afterRejected.Source.Revision {
		t.Fatalf("rejected static reload changed snapshot = %#v, err %v", afterRejected, err)
	}
	if err := writeTestFile(secretPath, []byte("first-static")); err != nil {
		t.Fatal(err)
	}

	var changes []RevisionChange
	manager2, err := New(Options{
		Now: func() time.Time { return testStartup }, YAML: []byte(yaml),
		Invalidator: func(_ context.Context, change RevisionChange) error { changes = append(changes, change); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager2.Close()
	if err := writeTestFile(secretPath, []byte("third-static")); err != nil {
		t.Fatal(err)
	}
	if err := manager2.ApplyYAML(context.Background(), []byte(yaml)); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].ID != "radarr-main" || !changes[0].AuthorityChanged || !slicesEqual(changes[0].ChangedFields, []string{"credentials"}) {
		t.Fatalf("static value invalidation = %#v", changes)
	}
}

func TestStaticCredentialChangeAcrossRestartGetsNewOpaqueRevision(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "radarr-api-key")
	if err := writeTestFile(secretPath, []byte("restart-first")); err != nil {
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
	firstManager, err := New(Options{Now: func() time.Time { return testStartup }, YAML: []byte(yaml)})
	if err != nil {
		t.Fatal(err)
	}
	firstSnapshot, err := firstManager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	firstManager.Close()
	secondManager, err := New(Options{Now: func() time.Time { return testStartup }, YAML: []byte(yaml)})
	if err != nil {
		t.Fatal(err)
	}
	defer secondManager.Close()
	secondSnapshot, err := secondManager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if firstSnapshot.Source.Revision != secondSnapshot.Source.Revision || firstSnapshot.Connections[0].Revision != secondSnapshot.Connections[0].Revision {
		t.Fatal("unchanged static credential changed revision across restart")
	}
	secondManager.Close()
	if err := writeTestFile(secretPath, []byte("restart-second")); err != nil {
		t.Fatal(err)
	}
	thirdManager, err := New(Options{Now: func() time.Time { return testStartup }, YAML: []byte(yaml)})
	if err != nil {
		t.Fatal(err)
	}
	defer thirdManager.Close()
	thirdSnapshot, err := thirdManager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if firstSnapshot.Source.Revision == thirdSnapshot.Source.Revision || firstSnapshot.Connections[0].Revision == thirdSnapshot.Connections[0].Revision {
		t.Fatal("changed static credential did not advance opaque revision")
	}
}

func TestAPIMappingMutationRejectsRelativeSourcePrefix(t *testing.T) {
	manager, err := New(Options{Now: func() time.Time { return testStartup }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	connection, err := manager.CreateConnection(context.Background(), ConnectionSpec{ID: "radarr-main", Kind: domain.ConnectionRadarr, Label: "Movies", Endpoint: "http://radarr.invalid:7878"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := manager.CreateStorageRoot(context.Background(), StorageRootSpec{ID: "movies", Label: "Movies", Purpose: domain.StorageLibrary, Path: "/media/movies"})
	if err != nil {
		t.Fatal(err)
	}
	for index, prefix := range []string{"", "downloads", "../downloads"} {
		_, err := manager.CreatePathMapping(context.Background(), PathMappingSpec{ID: domain.ConfigID(fmt.Sprintf("mapping-%d", index)), ConnectionID: connection.ID, RootID: root.ID, SourcePrefix: prefix})
		if !errors.Is(err, ErrMappingInvalid) {
			t.Errorf("API source prefix %q error = %v", prefix, err)
		}
	}
}

func TestMappingSourcePrefixRequiresAbsoluteRemoteNamespace(t *testing.T) {
	for _, value := range []string{"", "downloads", "../downloads", "./downloads", "C:\\downloads"} {
		if err := validateMappingShape(domain.PathMapping{SourcePrefix: value}); !errors.Is(err, ErrMappingInvalid) {
			t.Errorf("source prefix %q error = %v", value, err)
		}
	}
	if _, err := cleanSourcePrefix("/downloads"); err != nil {
		t.Errorf("absolute source prefix error = %v", err)
	}
	for _, value := range []string{"C:/downloads", "c:/downloads", "Z:/"} {
		if _, err := cleanSourcePrefix(value); err == nil {
			t.Errorf("Windows source prefix %q was accepted", value)
		}
	}
}

func TestQBTAndNZBMappingPrefixesUsePOSIXNamespaceContract(t *testing.T) {
	manager, err := New(Options{Now: func() time.Time { return testStartup }})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	for _, item := range []struct {
		kind domain.ConnectionKind
		id   domain.ConfigID
	}{
		{kind: domain.ConnectionQBittorrent, id: "qbt-main"},
		{kind: domain.ConnectionNZBGet, id: "nzbget-main"},
	} {
		connection, err := manager.CreateConnection(context.Background(), ConnectionSpec{
			ID: item.id, Kind: item.kind, Label: string(item.kind), Endpoint: "http://upstream.invalid:8080",
		})
		if err != nil {
			t.Fatalf("%s connection = %v", item.kind, err)
		}
		root, err := manager.CreateStorageRoot(context.Background(), StorageRootSpec{
			ID: item.id + "-root", Label: string(item.kind), Purpose: domain.StorageDownload, Path: "/media/" + string(item.kind),
		})
		if err != nil {
			t.Fatalf("%s root = %v", item.kind, err)
		}
		if _, err := manager.CreatePathMapping(context.Background(), PathMappingSpec{
			ID: item.id + "-mapping", ConnectionID: connection.ID, RootID: root.ID, SourcePrefix: "/downloads",
		}); err != nil {
			t.Fatalf("%s POSIX mapping = %v", item.kind, err)
		}
		if _, err := manager.CreatePathMapping(context.Background(), PathMappingSpec{
			ID: item.id + "-windows-mapping", ConnectionID: connection.ID, RootID: root.ID, SourcePrefix: "C:/downloads",
		}); !errors.Is(err, ErrMappingInvalid) {
			t.Fatalf("%s Windows mapping error = %v", item.kind, err)
		}
	}
}

func TestYAMLParseZeroesResolvedSecretsOnLaterFailure(t *testing.T) {
	secret := []byte("tracked-secret")
	resolver := &trackingResolver{secret: secret}
	data := `version: 1
connections:
  - id: first
    kind: radarr
    label: First
    endpoint: http://first.invalid
    credentials:
      apiKey:
        file: /synthetic/first
  - id: second
    kind: radarr
    label: Second
    endpoint: http://second.invalid
    credentials:
      apiKey:
        file: /synthetic/missing
`
	if _, err := ParseYAML([]byte(data), ParseOptions{SecretResolver: resolver, StartupAt: testStartup}); !errors.Is(err, ErrCredentialUnavailable) {
		t.Fatalf("later YAML failure = %v", err)
	}
	if !allZero(secret) {
		t.Fatal("resolved secret survived later YAML failure")
	}

	secret = []byte("duplicate-secret")
	resolver = &trackingResolver{secret: secret}
	duplicate := `version: 1
connections:
  - id: duplicate
    kind: radarr
    label: First
    endpoint: http://first.invalid
    credentials:
      apiKey:
        file: /synthetic/first
  - id: duplicate
    kind: radarr
    label: Second
    endpoint: http://second.invalid
    credentials:
      apiKey:
        file: /synthetic/first
`
	if _, err := ParseYAML([]byte(duplicate), ParseOptions{SecretResolver: resolver, StartupAt: testStartup}); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("duplicate YAML failure = %v", err)
	}
	if !allZero(secret) {
		t.Fatal("resolved secret survived duplicate YAML failure")
	}
}

func TestInvalidatorRunsOutsideConfigurationLock(t *testing.T) {
	var manager *Manager
	manager, err := New(Options{
		Now: func() time.Time { return testStartup },
		Invalidator: func(ctx context.Context, _ RevisionChange) error {
			_, err := manager.Snapshot(ctx)
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	root, err := manager.CreateStorageRoot(context.Background(), StorageRootSpec{ID: "movies", Label: "Movies", Purpose: domain.StorageLibrary, Path: "/media/movies"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := manager.UpdateStorageRoot(context.Background(), root.ID, root.Revision, StorageRootPatch{Path: stringPtr("/media/new-movies")})
	if err != nil || updated.Path != "/media/new-movies" {
		t.Fatalf("reentrant invalidator update = %#v, err %v", updated, err)
	}
}

func TestConcurrentUpdatesUseCompareAndSetAfterExternalInvalidation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var callsMu sync.Mutex
	calls := 0
	manager, err := New(Options{
		Now: func() time.Time { return testStartup },
		Invalidator: func(_ context.Context, _ RevisionChange) error {
			callsMu.Lock()
			calls++
			call := calls
			callsMu.Unlock()
			if call == 1 {
				close(entered)
				<-release
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	root, err := manager.CreateStorageRoot(context.Background(), StorageRootSpec{ID: "movies", Label: "Movies", Purpose: domain.StorageLibrary, Path: "/media/movies"})
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, updateErr := manager.UpdateStorageRoot(context.Background(), root.ID, root.Revision, StorageRootPatch{Path: stringPtr("/media/first")})
		firstDone <- updateErr
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first invalidator did not run")
	}
	second, secondErr := manager.UpdateStorageRoot(context.Background(), root.ID, root.Revision, StorageRootPatch{Path: stringPtr("/media/second")})
	if secondErr != nil {
		t.Fatalf("second concurrent update = %#v, err %v", second, secondErr)
	}
	close(release)
	firstErr := <-firstDone
	if !errors.Is(firstErr, ErrRevisionMismatch) {
		t.Fatalf("stale first update error = %v", firstErr)
	}
	current, err := manager.GetStorageRoot(context.Background(), root.ID, false)
	if err != nil || current.Path != "/media/second" {
		t.Fatalf("compare-and-set winner = %#v, err %v", current, err)
	}
}

func TestCredentialStoreRunsOutsideConfigurationLock(t *testing.T) {
	crypt, err := credentials.NewManager(bytes.Repeat([]byte{0x34}, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := newFaultCredentialStore()
	var manager *Manager
	store.onLoad = func() {
		_, _ = manager.Snapshot(context.Background())
	}
	store.onReplace = func() {
		_, _ = manager.Snapshot(context.Background())
	}
	manager, err = New(Options{Now: func() time.Time { return testStartup }, CredentialManager: crypt, CredentialStore: store, IdentityVerifier: verifiedIdentity})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err := manager.CreateConnection(context.Background(), ConnectionSpec{
		ID: "radarr-main", Kind: domain.ConnectionRadarr, Label: "Movies", Endpoint: "http://radarr.invalid:7878",
		Credentials: map[string]CredentialInput{"apiKey": {Value: []byte("store-key")}},
	}); err != nil {
		t.Fatalf("reentrant store create = %v", err)
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
	manager, err := New(Options{
		Now:  func() time.Time { return testStartup },
		YAML: []byte(yaml),
		IdentityVerifier: func(_ context.Context, _ domain.Connection) (IdentityVerification, error) {
			return IdentityVerified, nil
		},
	})
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

func verifiedIdentity(_ context.Context, _ domain.Connection) (IdentityVerification, error) {
	return IdentityVerified, nil
}

func verifiedCandidateIdentity(_ context.Context, _ domain.Connection, read CandidateCredentialReader) (IdentityVerification, error) {
	value, err := read("apiKey")
	if err != nil {
		return IdentityUnknown, err
	}
	zero(value)
	return IdentityVerified, nil
}

type replaceMode uint8

const (
	replaceNormal replaceMode = iota
	replaceCommitThenError
	replacePartialThenError
)

type faultCredentialStore struct {
	mu             sync.Mutex
	values         map[domain.ConfigID]map[string]credentials.Envelope
	replaceMode    replaceMode
	replaceCalls   int
	onLoad         func()
	onReplace      func()
	replaceEntered chan struct{}
	replaceRelease <-chan struct{}
	replaceOnce    sync.Once
}

func newFaultCredentialStore() *faultCredentialStore {
	return &faultCredentialStore{values: make(map[domain.ConfigID]map[string]credentials.Envelope)}
}

func (store *faultCredentialStore) Load(ctx context.Context, id domain.ConfigID) (map[string]credentials.Envelope, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if store.onLoad != nil {
		store.onLoad()
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return cloneEnvelopeSet(store.values[id]), nil
}

func (store *faultCredentialStore) Replace(ctx context.Context, id domain.ConfigID, values map[string]credentials.Envelope) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if store.onReplace != nil {
		store.onReplace()
	}
	if store.replaceEntered != nil && store.replaceRelease != nil {
		store.replaceOnce.Do(func() {
			close(store.replaceEntered)
			<-store.replaceRelease
		})
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.replaceCalls++
	switch store.replaceMode {
	case replacePartialThenError:
		partial := make(map[string]credentials.Envelope)
		for field, envelope := range values {
			partial["unexpected-"+field] = cloneEnvelope(envelope)
			break
		}
		store.values[id] = partial
		return errors.New("synthetic commit-then-error")
	case replaceCommitThenError:
		store.values[id] = cloneEnvelopeSet(values)
		return errors.New("synthetic commit-then-error")
	default:
		store.values[id] = cloneEnvelopeSet(values)
		return nil
	}
}

type trackingResolver struct {
	secret []byte
}

func (resolver *trackingResolver) Environment(string) (string, bool) {
	return "", false
}

func (resolver *trackingResolver) File(filePath string) ([]byte, error) {
	if filePath != "/synthetic/first" {
		return nil, ErrCredentialUnavailable
	}
	return resolver.secret, nil
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}
