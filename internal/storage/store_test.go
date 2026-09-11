package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/guilycst/mastarr/internal/storage/sqlc"
	"github.com/guilycst/mastarr/migrations"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func migrationsThroughVersion(t *testing.T, max int) fs.FS {
	t.Helper()
	files := fstest.MapFS{}
	for _, name := range []string{
		"000001_initial.up.sql", "000002_indexes.up.sql", "000003_storage_contract.up.sql", "000004_storage_compatibility.up.sql", "000005_storage_safety.up.sql", "000006_approval_safety.up.sql",
	} {
		var version int
		if _, err := fmt.Sscanf(name, "%d_", &version); err != nil {
			t.Fatal(err)
		}
		if version > max {
			continue
		}
		data, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	return files
}

func TestOpenMigratesCompleteSchema(t *testing.T) {
	store := newTestStore(t)

	var foreignKeys, synchronous int
	if err := store.DB().QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign keys disabled: %d", foreignKeys)
	}
	if err := store.DB().QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if synchronous != 2 { // FULL
		t.Fatalf("synchronous pragma = %d, want FULL (2)", synchronous)
	}

	var version int
	if err := store.DB().QueryRow("SELECT version FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 6 {
		t.Fatalf("migration version = %d, want 6", version)
	}

	for _, table := range []string{
		"config_snapshots", "connections", "storage_roots", "path_mappings",
		"encrypted_credentials", "scans", "coverage_snapshots", "discoveries",
		"file_observations", "downloads", "provenance_links", "descriptors",
		"media_identities", "external_records", "tracking_observations",
		"action_plans", "action_plan_revisions", "plan_manifests", "review_decisions",
		"workflow_runs", "workflow_steps", "action_runs", "action_attempts",
		"action_effects", "idempotency_records", "trash_entries", "trash_items",
		"janitor_records", "audit_events",
		"tracking_observation_quarantine", "early_purge_plan_targets",
	} {
		var got string
		if err := store.DB().QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&got); err != nil {
			t.Fatalf("table %q missing: %v", table, err)
		}
	}
}

func TestMigrationsUpgradeFromVersionOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.sqlite")
	store, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO config_snapshots
		(id, source, document_id, revision, startup_at, effective_json, created_at)
		VALUES ('legacy-snapshot', 'api', 'runtime', '1', '2026-09-11T00:00:00Z', '{}', '2026-09-11T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO connections
		(id, kind, label, endpoint, source, source_snapshot_id, revision, created_at, updated_at)
		VALUES ('legacy-qbt', 'qbittorrent', 'Legacy qBittorrent', 'http://qbt.invalid:8080', 'api', 'legacy-snapshot', '1', '2026-09-11T00:00:00Z', '2026-09-11T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO downloads
		(id, connection_id, external_id, protocol, state, payload_json, history_json, first_seen_at, last_seen_at)
		VALUES ('legacy-completed', 'legacy-qbt', 'completed-id', 'qbittorrent', 'completed', '{}', '[]', '2026-09-11T00:00:00Z', '2026-09-11T00:00:00Z'),
		       ('legacy-paused', 'legacy-qbt', 'paused-id', 'qbittorrent', 'paused', '{}', '[]', '2026-09-11T00:00:00Z', '2026-09-11T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	var version int
	if err := upgraded.DB().QueryRow("SELECT version FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 6 {
		t.Fatalf("upgraded migration version = %d, want 6", version)
	}
	var count int
	if err := upgraded.DB().QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_due_action_runs_claim'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("upgrade index count = %d, want 1", count)
	}
	var completedState, pausedState string
	if err := upgraded.DB().QueryRow("SELECT state FROM downloads WHERE id = 'legacy-completed'").Scan(&completedState); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.DB().QueryRow("SELECT state FROM downloads WHERE id = 'legacy-paused'").Scan(&pausedState); err != nil {
		t.Fatal(err)
	}
	if completedState != "complete" || pausedState != "unknown" {
		t.Fatalf("legacy download state translation = %q/%q, want complete/unknown", completedState, pausedState)
	}
}

func TestMigrationsUpgradeFromVersionTwo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade-v2.sqlite")
	store, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, 2)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO config_snapshots
		(id, source, document_id, revision, startup_at, effective_json, created_at)
		VALUES ('legacy-v2-snapshot', 'api', 'runtime', '1', '2026-09-11T00:00:00Z', '{}', '2026-09-11T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO connections
		(id, kind, label, endpoint, source, source_snapshot_id, revision, created_at, updated_at)
		VALUES ('legacy-v2-qbt', 'qbittorrent', 'Legacy v2 qBittorrent', 'http://qbt.invalid:8080', 'api', 'legacy-v2-snapshot', '1', '2026-09-11T00:00:00Z', '2026-09-11T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO downloads
		(id, connection_id, external_id, protocol, state, payload_json, history_json, first_seen_at, last_seen_at)
		VALUES ('legacy-v2-download', 'legacy-v2-qbt', 'v2-id', 'qbittorrent', 'completed', '{}', '[]', '2026-09-11T00:00:00Z', '2026-09-11T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	var version int
	if err := upgraded.DB().QueryRow("SELECT version FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 6 {
		t.Fatalf("v2 upgrade migration version = %d, want 6", version)
	}
	var state string
	if err := upgraded.DB().QueryRow("SELECT state FROM downloads WHERE id = 'legacy-v2-download'").Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "complete" {
		t.Fatalf("v2 download state translation = %q, want complete", state)
	}
	for _, index := range []string{
		"idx_tracking_dimension", "idx_file_observations_discovery", "idx_downloads_connection_state",
		"idx_action_effects_run", "idx_trash_items_entry",
	} {
		var count int
		if err := upgraded.DB().QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?", index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("v2 upgrade index %q count = %d, want 1", index, count)
		}
	}
}

func TestDirtyMigrationBlocksStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dirty.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", sqliteDSN(path, defaultBusyTimeout))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE schema_migrations SET dirty = 1"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("dirty migration database was accepted")
	}
}

func TestSecondStoreIsRejectedUntilFirstCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.sqlite")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if _, err := Open(path); !errors.Is(err, ErrAlreadyLocked) {
		t.Fatalf("second open error = %v, want ErrAlreadyLocked", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatalf("open after release: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSecondStoreThroughDatabaseSymlinkIsRejected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.sqlite")
	alias := filepath.Join(dir, "state-alias.sqlite")
	first, err := Open(target)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(alias); !errors.Is(err, ErrAlreadyLocked) {
		t.Fatalf("symlink alias open error = %v, want ErrAlreadyLocked", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(alias)
	if err != nil {
		t.Fatalf("open alias after release: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInMemoryStoreRemainsSupported(t *testing.T) {
	store, err := OpenWithOptions(Options{Path: ":memory:", LockPath: filepath.Join(t.TempDir(), "memory.lock")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWithTxCommitsOrRollsBackAsOneUnit(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	rollbackErr := errors.New("synthetic rollback")
	if err := store.WithTx(ctx, func(queries *sqlc.Queries) error {
		if _, err := queries.CreateConfigSnapshot(ctx, &sqlc.CreateConfigSnapshotParams{
			ID: "rolled-back", Source: "api", DocumentID: "runtime", Revision: "1",
			StartupAt: "2026-09-11T00:00:00Z", EffectiveJson: "{}", CreatedAt: "2026-09-11T00:00:00Z",
		}); err != nil {
			return err
		}
		return rollbackErr
	}); !errors.Is(err, rollbackErr) {
		t.Fatalf("rollback error = %v", err)
	}
	if _, err := store.Queries().GetConfigSnapshot(ctx, "rolled-back"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rolled-back snapshot lookup error = %v", err)
	}

	if err := store.WithTx(ctx, func(queries *sqlc.Queries) error {
		_, err := queries.CreateConfigSnapshot(ctx, &sqlc.CreateConfigSnapshotParams{
			ID: "committed", Source: "api", DocumentID: "runtime", Revision: "2",
			StartupAt: "2026-09-11T00:00:00Z", EffectiveJson: "{}", CreatedAt: "2026-09-11T00:00:00Z",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().GetConfigSnapshot(ctx, "committed"); err != nil {
		t.Fatalf("committed snapshot lookup: %v", err)
	}
}

func TestSchemaConstraintsAndImmutableRecords(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	queries := store.Queries()

	if _, err := queries.CreateConfigSnapshot(ctx, &sqlc.CreateConfigSnapshotParams{
		ID: "snap-api", Source: "api", DocumentID: "runtime", Revision: "1",
		StartupAt: "2026-09-11T00:00:00Z", EffectiveJson: "{}", CreatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateConfigSnapshot(ctx, &sqlc.CreateConfigSnapshotParams{
		ID: "snap-yaml", Source: "yaml", DocumentID: "config.yaml", Revision: "1",
		StartupAt: "2026-09-11T00:00:00Z", EffectiveJson: "{}", CreatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateConnection(ctx, &sqlc.CreateConnectionParams{
		ID: "qbt-main", Kind: "qbittorrent", Label: "Synthetic qBittorrent",
		Endpoint: "http://qbt.invalid:8080", Source: "api", SourceSnapshotID: "snap-api", Revision: "1",
		RetiredAt: sql.NullString{}, CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec("UPDATE config_snapshots SET revision = 'changed' WHERE id = 'snap-api'"); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("immutable config snapshot update error = %v", err)
	}
	if _, err := queries.CreateStorageRoot(ctx, &sqlc.CreateStorageRootParams{
		ID: "downloads", Label: "Downloads", Purpose: "download", Path: "/media/downloads",
		Source: "api", SourceSnapshotID: "snap-api", Revision: "1", CapabilitiesJson: "[]",
		CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateConnection(ctx, &sqlc.CreateConnectionParams{
		ID: "radarr-main", Kind: "radarr", Label: "Synthetic Radarr",
		Endpoint: "http://radarr.invalid:7878", Source: "api", SourceSnapshotID: "snap-api", Revision: "1",
		RetiredAt: sql.NullString{}, CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreatePathMapping(ctx, &sqlc.CreatePathMappingParams{
		ID: "mapping-api", ConnectionID: "qbt-main", RootID: "downloads", SourcePrefix: "", DestinationPrefix: "",
		Source: "api", SourceSnapshotID: "snap-api", Revision: "1", CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO connections
		(id, kind, label, endpoint, source, source_snapshot_id, revision, created_at, updated_at)
		VALUES ('mismatched-source', 'sonarr', 'Mismatched', 'http://sonarr.invalid', 'api', 'snap-yaml', '1', 'now', 'now')`); err == nil {
		t.Fatal("connection accepted a snapshot owned by another source")
	}
	if _, err := store.DB().Exec(`INSERT INTO storage_roots
		(id, label, purpose, path, source, source_snapshot_id, revision, created_at, updated_at)
		VALUES ('mismatched-root', 'Mismatched', 'library', '/media/mismatch', 'yaml', 'snap-api', '1', 'now', 'now')`); err == nil {
		t.Fatal("storage root accepted a snapshot owned by another source")
	}
	if _, err := store.DB().Exec(`INSERT INTO path_mappings
		(id, connection_id, root_id, source_prefix, destination_prefix, source, source_snapshot_id, revision, created_at, updated_at)
		VALUES ('mismatched-mapping', 'qbt-main', 'downloads', '', '', 'yaml', 'snap-api', '1', 'now', 'now')`); err == nil {
		t.Fatal("path mapping accepted a snapshot owned by another source")
	}
	if _, err := store.DB().Exec("UPDATE connections SET source = 'yaml' WHERE id = 'qbt-main'"); err == nil {
		t.Fatal("connection update accepted a snapshot owned by another source")
	}
	if _, err := store.DB().Exec("UPDATE storage_roots SET source = 'yaml' WHERE id = 'downloads'"); err == nil {
		t.Fatal("storage root update accepted a snapshot owned by another source")
	}
	if _, err := store.DB().Exec("UPDATE path_mappings SET source = 'yaml' WHERE id = 'mapping-api'"); err == nil {
		t.Fatal("path mapping update accepted a snapshot owned by another source")
	}

	if _, err := store.DB().Exec(`INSERT INTO storage_roots
		(id, label, purpose, path, source, source_snapshot_id, revision, capabilities_json, created_at, updated_at)
		VALUES ('bad', 'Bad', 'library', '/media/bad', 'api', 'snap-api', '1', 'not-json', 'now', 'now')`); err == nil {
		t.Fatal("invalid JSON constraint accepted")
	}
	if _, err := store.DB().Exec(`INSERT INTO connections
		(id, kind, label, endpoint, source, source_snapshot_id, revision, created_at, updated_at)
		VALUES ('orphan', 'radarr', 'Orphan', 'http://radarr.invalid', 'api', 'missing', '1', 'now', 'now')`); err == nil {
		t.Fatal("foreign key constraint accepted missing snapshot")
	}

	if _, err := queries.CreateActionPlan(ctx, &sqlc.CreateActionPlanParams{
		ID: "plan-1", Kind: "fs.copy", State: "ready", CurrentRevision: 1, CurrentDigest: "digest-1",
		CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateActionPlanRevision(ctx, &sqlc.CreateActionPlanRevisionParams{
		PlanID: "plan-1", Revision: 1, Digest: "digest-1", State: "ready", InputJson: `{}`,
		PreconditionsJson: `{}`, CapabilitiesJson: `[]`, ManifestJson: `[]`, CreatedAt: "2026-09-11T00:00:00Z",
		ExpiresAt: "2026-09-12T00:00:00Z", ReadyAt: sql.NullString{String: "2026-09-11T00:00:00Z", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: "decision-1", PlanID: "plan-1", PlanRevision: 1, PlanDigest: "digest-1", Decision: "approve",
		Actor: "unauthenticated", IdempotencyScope: "review-decisions", IdempotencyKey: "decision-key", CreatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec("UPDATE action_plan_revisions SET digest = 'changed' WHERE plan_id = 'plan-1' AND revision = 1"); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("immutable revision update error = %v", err)
	}
	if _, err := store.DB().Exec("DELETE FROM review_decisions WHERE id = 'decision-1'"); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("immutable decision delete error = %v", err)
	}

	first, err := queries.CreateIdempotencyRecord(ctx, &sqlc.CreateIdempotencyRecordParams{
		Scope: "action-plans", IdempotencyKey: "same-key", RequestDigest: "request-1", StatusCode: 202,
		ResourceKind: "action_plan", ResourceID: "plan-1", ResponseJson: `{"id":"plan-1"}`,
		CreatedAt: "2026-09-11T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ResourceID != "plan-1" {
		t.Fatalf("stored idempotency resource = %q", first.ResourceID)
	}
	if _, err := queries.CreateIdempotencyRecord(ctx, &sqlc.CreateIdempotencyRecordParams{
		Scope: "action-plans", IdempotencyKey: "same-key", RequestDigest: "request-2", StatusCode: 202,
		ResourceKind: "action_plan", ResourceID: "plan-2", ResponseJson: `{"id":"plan-2"}`,
		CreatedAt: "2026-09-11T00:00:00Z",
	}); err == nil {
		t.Fatal("duplicate idempotency key accepted")
	}
}

func TestUnknownTrackingObservationCanBeStoredWithoutExternalRecord(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	queries := store.Queries()
	if _, err := queries.CreateConfigSnapshot(ctx, &sqlc.CreateConfigSnapshotParams{
		ID: "snap-api", Source: "api", DocumentID: "runtime", Revision: "1",
		StartupAt: "2026-09-11T00:00:00Z", EffectiveJson: "{}", CreatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateConfigSnapshot(ctx, &sqlc.CreateConfigSnapshotParams{
		ID: "snap-yaml", Source: "yaml", DocumentID: "config.yaml", Revision: "1",
		StartupAt: "2026-09-11T00:00:00Z", EffectiveJson: "{}", CreatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	for _, connection := range []sqlc.CreateConnectionParams{
		{ID: "qbt-main", Kind: "qbittorrent", Label: "Synthetic qBittorrent", Endpoint: "http://qbt.invalid:8080", Source: "api", SourceSnapshotID: "snap-api", Revision: "1", CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z"},
		{ID: "radarr-main", Kind: "radarr", Label: "Synthetic Radarr", Endpoint: "http://radarr.invalid:7878", Source: "api", SourceSnapshotID: "snap-api", Revision: "1", CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z"},
	} {
		if _, err := queries.CreateConnection(ctx, &connection); err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range []sqlc.CreateStorageRootParams{
		{ID: "root-a", Label: "Downloads A", Purpose: "download", Path: "/media/downloads-a", Source: "api", SourceSnapshotID: "snap-api", Revision: "1", CapabilitiesJson: "[]", CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z"},
		{ID: "root-b", Label: "Downloads B", Purpose: "download", Path: "/media/downloads-b", Source: "api", SourceSnapshotID: "snap-api", Revision: "1", CapabilitiesJson: "[]", CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z"},
	} {
		if _, err := queries.CreateStorageRoot(ctx, &root); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := queries.CreateMediaIdentity(ctx, &sqlc.CreateMediaIdentityParams{
		ID: "media-example", Kind: "movie", Title: "Example Film", CanonicalKey: "tmdb:4242",
		CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateMediaIdentity(ctx, &sqlc.CreateMediaIdentityParams{
		ID: "media-other", Kind: "movie", Title: "Other Film", CanonicalKey: "tmdb:4343",
		CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateExternalRecord(ctx, &sqlc.CreateExternalRecordParams{
		ID: "record-qbt", ConnectionID: "qbt-main", RecordKind: "title", ExternalID: "42",
		MediaIdentityID: sql.NullString{String: "media-example", Valid: true}, PayloadJson: `{}`,
		ObservedAt: "2026-09-11T00:00:00Z", FirstSeenAt: "2026-09-11T00:00:00Z", LastSeenAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	newCoverage := func(id, connectionID, rootID, mediaID, completeness, observedAt string, completedAt sql.NullString) {
		t.Helper()
		if _, err := queries.CreateCoverageSnapshot(ctx, &sqlc.CreateCoverageSnapshotParams{
			ID: id, Completeness: completeness, ReasonCodesJson: `[]`, ObservedCount: 1,
			ConnectionID: sql.NullString{String: connectionID, Valid: true}, RootID: sql.NullString{String: rootID, Valid: true},
			MediaIdentityID: sql.NullString{String: mediaID, Valid: true}, SnapshotRevision: sql.NullString{String: id + "-revision", Valid: true},
			StartedAt: sql.NullString{String: "2026-09-11T00:00:00Z", Valid: true}, CompletedAt: completedAt,
			ObservedAt: observedAt, CreatedAt: observedAt,
		}); err != nil {
			t.Fatal(err)
		}
	}
	newCoverage("coverage-complete", "radarr-main", "root-a", "media-example", "complete", "2026-09-11T00:00:01Z", sql.NullString{String: "2026-09-11T00:00:01Z", Valid: true})
	newCoverage("coverage-other-connection", "qbt-main", "root-a", "media-example", "complete", "2026-09-11T00:00:01Z", sql.NullString{String: "2026-09-11T00:00:01Z", Valid: true})
	newCoverage("coverage-other-root", "radarr-main", "root-b", "media-example", "complete", "2026-09-11T00:00:01Z", sql.NullString{String: "2026-09-11T00:00:01Z", Valid: true})
	newCoverage("coverage-other-media", "radarr-main", "root-a", "media-other", "complete", "2026-09-11T00:00:01Z", sql.NullString{String: "2026-09-11T00:00:01Z", Valid: true})
	newCoverage("coverage-partial", "radarr-main", "root-a", "media-example", "partial", "2026-09-11T00:00:01Z", sql.NullString{String: "2026-09-11T00:00:01Z", Valid: true})
	newCoverage("coverage-unfinished", "radarr-main", "root-a", "media-example", "unknown", "2026-09-11T00:00:01Z", sql.NullString{})
	newCoverage("coverage-future", "radarr-main", "root-a", "media-example", "complete", "2026-09-11T00:00:10Z", sql.NullString{String: "2026-09-11T00:00:10Z", Valid: true})
	newCoverage("coverage-stale", "radarr-main", "root-a", "media-example", "complete", "2026-09-11T00:00:01Z", sql.NullString{String: "2026-09-11T00:00:01Z", Valid: true})

	if _, err := queries.CreateTrackingObservation(ctx, &sqlc.CreateTrackingObservationParams{
		ID: "tracking-null-scope", MediaIdentityID: sql.NullString{String: "media-example", Valid: true},
		ConnectionID: "", Dimension: "registration", Status: "present", EvidenceJson: `{}`, ObservedAt: "2026-09-11T00:00:00Z",
	}); err == nil {
		t.Fatal("tracking observation accepted a NULL connection scope")
	}
	if _, err := queries.CreateTrackingObservation(ctx, &sqlc.CreateTrackingObservationParams{
		ID: "tracking-unknown-null-scope", ConnectionID: "", Dimension: "registration", Status: "unknown", EvidenceJson: `{}`, ObservedAt: "2026-09-11T00:00:00Z",
	}); err == nil {
		t.Fatal("unknown tracking observation accepted a NULL connection scope")
	}
	if _, err := queries.CreateTrackingObservation(ctx, &sqlc.CreateTrackingObservationParams{
		ID: "tracking-mismatch", ExternalRecordID: sql.NullString{String: "record-qbt", Valid: true},
		ConnectionID: "radarr-main", Dimension: "registration", Status: "present", EvidenceJson: `{}`, ObservedAt: "2026-09-11T00:00:00Z",
	}); err == nil {
		t.Fatal("mismatched tracking connection was accepted")
	}
	if _, err := queries.CreateTrackingObservation(ctx, &sqlc.CreateTrackingObservationParams{
		ID: "tracking-identity-mismatch", ExternalRecordID: sql.NullString{String: "record-qbt", Valid: true},
		MediaIdentityID: sql.NullString{String: "media-other", Valid: true}, ConnectionID: "qbt-main",
		Dimension: "registration", Status: "present", EvidenceJson: `{}`, ObservedAt: "2026-09-11T00:00:00Z",
	}); err == nil {
		t.Fatal("mismatched tracking media identity was accepted")
	}
	for _, coverageID := range []string{"coverage-other-connection", "coverage-other-root", "coverage-other-media", "coverage-partial", "coverage-unfinished", "coverage-future"} {
		if _, err := queries.CreateTrackingObservation(ctx, &sqlc.CreateTrackingObservationParams{
			ID: "tracking-" + coverageID, MediaIdentityID: sql.NullString{String: "media-example", Valid: true},
			ConnectionID: "radarr-main", RootID: sql.NullString{String: "root-a", Valid: true}, Dimension: "registration", Status: "absent",
			EvidenceJson: `{}`, CoverageID: sql.NullString{String: coverageID, Valid: true}, CoverageMaxAgeSeconds: sql.NullInt64{Int64: 60, Valid: true}, ObservedAt: "2026-09-11T00:00:05Z",
		}); err == nil {
			t.Fatalf("absence accepted invalid coverage %q", coverageID)
		}
	}
	if _, err := queries.CreateTrackingObservation(ctx, &sqlc.CreateTrackingObservationParams{
		ID: "tracking-no-coverage", MediaIdentityID: sql.NullString{String: "media-example", Valid: true},
		ConnectionID: "radarr-main", RootID: sql.NullString{String: "root-a", Valid: true}, Dimension: "registration", Status: "absent",
		EvidenceJson: `{}`, ObservedAt: "2026-09-11T00:00:05Z",
	}); err == nil {
		t.Fatal("absence without coverage was accepted")
	}
	if _, err := queries.CreateTrackingObservation(ctx, &sqlc.CreateTrackingObservationParams{
		ID: "tracking-stale", MediaIdentityID: sql.NullString{String: "media-example", Valid: true},
		ConnectionID: "radarr-main", RootID: sql.NullString{String: "root-a", Valid: true}, Dimension: "registration", Status: "absent",
		EvidenceJson: `{}`, CoverageID: sql.NullString{String: "coverage-stale", Valid: true}, CoverageMaxAgeSeconds: sql.NullInt64{Int64: 1, Valid: true}, ObservedAt: "2026-09-11T00:00:05Z",
	}); err == nil {
		t.Fatal("stale absence coverage was accepted")
	}
	abset, err := queries.CreateTrackingObservation(ctx, &sqlc.CreateTrackingObservationParams{
		ID: "tracking-absent", MediaIdentityID: sql.NullString{String: "media-example", Valid: true},
		ConnectionID: "radarr-main", RootID: sql.NullString{String: "root-a", Valid: true}, Dimension: "registration", Status: "absent",
		EvidenceJson: `{}`, CoverageID: sql.NullString{String: "coverage-complete", Valid: true}, CoverageMaxAgeSeconds: sql.NullInt64{Int64: 60, Valid: true}, ObservedAt: "2026-09-11T00:00:05Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !abset.MediaIdentityID.Valid || abset.ConnectionID != "radarr-main" || !abset.CoverageID.Valid || abset.ExternalRecordID.Valid || abset.Status != "absent" {
		t.Fatalf("unexpected absent observation: %+v", abset)
	}
	row, err := store.Queries().CreateTrackingObservation(ctx, &sqlc.CreateTrackingObservationParams{
		ID: "tracking-unknown", ConnectionID: "radarr-main", Dimension: "registration", Status: "unknown",
		EvidenceJson: `{}`, ObservedAt: "2026-09-11T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "unknown" || row.ExternalRecordID.Valid || row.ConnectionID != "radarr-main" {
		t.Fatalf("unexpected unknown observation: %+v", row)
	}
}

func TestCompositeScopeAndDownloadStateConstraints(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	queries := store.Queries()
	if _, err := queries.CreateConfigSnapshot(ctx, &sqlc.CreateConfigSnapshotParams{
		ID: "scope-snapshot", Source: "api", DocumentID: "runtime", Revision: "1",
		StartupAt: "2026-09-11T00:00:00Z", EffectiveJson: "{}", CreatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	for _, root := range []sqlc.CreateStorageRootParams{
		{ID: "scope-root-a", Label: "Scope A", Purpose: "download", Path: "/media/scope-a", Source: "api", SourceSnapshotID: "scope-snapshot", Revision: "1", CapabilitiesJson: "[]", CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z"},
		{ID: "scope-root-b", Label: "Scope B", Purpose: "download", Path: "/media/scope-b", Source: "api", SourceSnapshotID: "scope-snapshot", Revision: "1", CapabilitiesJson: "[]", CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z"},
	} {
		if _, err := queries.CreateStorageRoot(ctx, &root); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := queries.CreateConnection(ctx, &sqlc.CreateConnectionParams{
		ID: "scope-qbt", Kind: "qbittorrent", Label: "Scope qBittorrent", Endpoint: "http://qbt.invalid:8080",
		Source: "api", SourceSnapshotID: "scope-snapshot", Revision: "1", CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	for _, discovery := range []sqlc.CreateDiscoveryParams{
		{ID: "discovery-a", RootID: "scope-root-a", RelativePath: "a.mkv", EntryType: "file", SizeBytes: 1, ObservedAt: "2026-09-11T00:00:00Z", FirstSeenAt: "2026-09-11T00:00:00Z", LastSeenAt: "2026-09-11T00:00:00Z", ManifestRevision: "manifest-1", ChildManifestJson: `[]`},
		{ID: "discovery-b", RootID: "scope-root-b", RelativePath: "b.mkv", EntryType: "file", SizeBytes: 1, ObservedAt: "2026-09-11T00:00:00Z", FirstSeenAt: "2026-09-11T00:00:00Z", LastSeenAt: "2026-09-11T00:00:00Z", ManifestRevision: "manifest-1", ChildManifestJson: `[]`},
	} {
		if _, err := queries.CreateDiscovery(ctx, &discovery); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().Exec(`INSERT INTO file_observations
		(id, discovery_id, root_id, relative_path, entry_type, size_bytes, observed_at, manifest_revision, child_manifest_json, first_seen_at, last_seen_at)
		VALUES ('mixed-file-observation', 'discovery-a', 'scope-root-b', 'b.mkv', 'file', 1, '2026-09-11T00:00:00Z', 'manifest-1', '[]', '2026-09-11T00:00:00Z', '2026-09-11T00:00:00Z')`); err == nil {
		t.Fatal("file observation accepted a discovery from another root/path")
	}

	for _, id := range []string{"action-one", "action-two"} {
		if _, err := queries.CreateActionPlan(ctx, &sqlc.CreateActionPlanParams{
			ID: id + "-plan", Kind: "fs.copy", State: "ready", CurrentRevision: 1, CurrentDigest: id + "-digest",
			CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := queries.CreateActionPlanRevision(ctx, &sqlc.CreateActionPlanRevisionParams{
			PlanID: id + "-plan", Revision: 1, Digest: id + "-digest", State: "ready", InputJson: `{}`, PreconditionsJson: `{}`, CapabilitiesJson: `[]`, ManifestJson: `[]`, CreatedAt: "2026-09-11T00:00:00Z", ExpiresAt: "2026-09-12T00:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := queries.CreateActionRun(ctx, &sqlc.CreateActionRunParams{
			ID: id, PlanID: id + "-plan", PlanRevision: 1, PlanDigest: id + "-digest", State: "queued", DesiredStateJson: `{}`, Version: 1, OutcomeJson: `{}`, CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := queries.CreateActionAttempt(ctx, &sqlc.CreateActionAttemptParams{
		ID: "attempt-one", ActionRunID: "action-one", AttemptNumber: 1, Phase: "dispatch", State: "running", StartedAt: "2026-09-11T00:00:00Z", OutcomeCertainty: "uncertain", EvidenceJson: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateActionAttempt(ctx, &sqlc.CreateActionAttemptParams{
		ID: "attempt-two", ActionRunID: "action-two", AttemptNumber: 1, Phase: "dispatch", State: "running", StartedAt: "2026-09-11T00:00:00Z", OutcomeCertainty: "uncertain", EvidenceJson: `{}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`INSERT INTO action_effects
		(id, action_run_id, attempt_id, ordinal, target_kind, target_id, effect_kind, state, evidence_json, observed_at)
		VALUES ('cross-effect', 'action-one', 'attempt-two', 0, 'file', 'target', 'copy', 'pending', '{}', '2026-09-11T00:00:00Z')`); err == nil {
		t.Fatal("action effect accepted an attempt from another action run")
	}

	for _, entry := range []string{"trash-one", "trash-two"} {
		root := "scope-root-a"
		if entry == "trash-two" {
			root = "scope-root-b"
		}
		if _, err := queries.CreateTrashEntry(ctx, &sqlc.CreateTrashEntryParams{
			ID: entry, RootID: root, State: "trashed", OriginalPrefix: "original/" + entry, TrashPrefix: "trash/" + entry, ManifestJson: `[]`, RetentionSeconds: 3600,
			TrashedAt: sql.NullString{String: "2026-09-11T00:00:00Z", Valid: true}, ExpiresAt: "2026-09-12T00:00:00Z", ClientStateJson: `{}`, CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().Exec(`INSERT INTO trash_items
		(id, entry_id, root_id, original_relative_path, trash_relative_path, entry_type, size_bytes, state)
		VALUES ('cross-trash-item', 'trash-one', 'scope-root-b', 'movie.mkv', 'movie.mkv', 'file', 1, 'selected')`); err == nil {
		t.Fatal("trash item accepted an entry from another root")
	}

	if _, err := queries.CreateDownload(ctx, &sqlc.CreateDownloadParams{
		ID: "complete-download", ConnectionID: "scope-qbt", ExternalID: "complete", Protocol: "qbittorrent", State: "complete", PayloadJson: `{}`, HistoryJson: `[]`, FirstSeenAt: "2026-09-11T00:00:00Z", LastSeenAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatalf("API download state complete was rejected: %v", err)
	}
	if _, err := store.DB().Exec(`INSERT INTO downloads
		(id, connection_id, external_id, protocol, state, payload_json, history_json, first_seen_at, last_seen_at)
		VALUES ('invalid-download', 'scope-qbt', 'invalid', 'qbittorrent', 'completed', '{}', '[]', '2026-09-11T00:00:00Z', '2026-09-11T00:00:00Z')`); err == nil {
		t.Fatal("legacy download state completed remains accepted")
	}
}

func TestActionQueueRecoveryAndTerminalization(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	queries := store.Queries()
	createRun := func(id, state string, cancellationAt, deadlineAt sql.NullString, leaseUntil string) {
		t.Helper()
		if _, err := queries.CreateActionPlan(ctx, &sqlc.CreateActionPlanParams{
			ID: id + "-plan", Kind: "fs.copy", State: "ready", CurrentRevision: 1, CurrentDigest: id + "-digest", CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := queries.CreateActionPlanRevision(ctx, &sqlc.CreateActionPlanRevisionParams{
			PlanID: id + "-plan", Revision: 1, Digest: id + "-digest", State: "ready", InputJson: `{}`, PreconditionsJson: `{}`, CapabilitiesJson: `[]`, ManifestJson: `[]`, CreatedAt: "2026-09-11T00:00:00Z", ExpiresAt: "2026-09-12T00:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := queries.CreateActionRun(ctx, &sqlc.CreateActionRunParams{
			ID: id, PlanID: id + "-plan", PlanRevision: 1, PlanDigest: id + "-digest", State: state, DesiredStateJson: `{}`, CancellationRequestedAt: cancellationAt, DeadlineAt: deadlineAt, ClaimedBy: sql.NullString{String: "old-worker", Valid: state == "running"}, LeaseUntil: sql.NullString{String: leaseUntil, Valid: state == "running"}, Version: 1, OutcomeJson: `{}`, CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
	}
	createRun("crashed", "running", sql.NullString{}, sql.NullString{}, "2026-09-11T00:02:00Z")
	createRun("expired-lease", "running", sql.NullString{}, sql.NullString{}, "2026-09-11T00:00:05Z")
	createRun("cancel-requested", "queued", sql.NullString{String: "2026-09-11T00:00:02Z", Valid: true}, sql.NullString{}, "")
	createRun("deadline-passed", "queued", sql.NullString{}, sql.NullString{String: "2026-09-11T00:00:03Z", Valid: true}, "")
	createRun("dependency-wait", "waiting_dependency", sql.NullString{}, sql.NullString{}, "")
	if _, err := queries.CreateActionAttempt(ctx, &sqlc.CreateActionAttemptParams{
		ID: "crashed-attempt", ActionRunID: "crashed", AttemptNumber: 1, Phase: "dispatch", State: "running", StartedAt: "2026-09-11T00:00:01Z", OutcomeCertainty: "not_dispatched", EvidenceJson: `{}`,
	}); err != nil {
		t.Fatal(err)
	}

	now := sql.NullString{String: "2026-09-11T00:01:00Z", Valid: true}
	if err := store.WithTx(ctx, func(tx *sqlc.Queries) error {
		expired, err := tx.RecoverExpiredActionRuns(ctx, now)
		if err != nil {
			return err
		}
		if len(expired) != 1 || expired[0].ID != "expired-lease" {
			return fmt.Errorf("expired action recovery = %+v", expired)
		}
		if _, err := tx.RecoverRunningActionRuns(ctx, now); err != nil {
			return err
		}
		if _, err := tx.RecoverRunningActionAttempts(ctx); err != nil {
			return err
		}
		if _, err := tx.FinalizeCancelledActionRuns(ctx, now.String); err != nil {
			return err
		}
		if _, err := tx.FinalizeDeadlineActionRuns(ctx, now.String); err != nil {
			return err
		}
		if _, err := tx.FinalizeCancelledActionAttempts(ctx, now); err != nil {
			return err
		}
		_, err = tx.FinalizeDeadlineActionAttempts(ctx, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	crashed, err := queries.GetActionRun(ctx, "crashed")
	if err != nil {
		t.Fatal(err)
	}
	if crashed.State != "reconciling" || crashed.ClaimedBy.Valid || crashed.LeaseUntil.Valid {
		t.Fatalf("crashed action recovery = %+v", crashed)
	}
	attempts, err := queries.ListActionAttempts(ctx, "crashed")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].State != "reconciling" || attempts[0].OutcomeCertainty != "uncertain" {
		t.Fatalf("crashed attempt recovery = %+v", attempts)
	}
	cancelled, err := queries.GetActionRun(ctx, "cancel-requested")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.State != "cancelled" {
		t.Fatalf("cancelled action state = %q", cancelled.State)
	}
	deadline, err := queries.GetActionRun(ctx, "deadline-passed")
	if err != nil {
		t.Fatal(err)
	}
	if deadline.State != "deadline_exceeded" {
		t.Fatalf("deadline action state = %q", deadline.State)
	}
	due, err := queries.ListDueActionRuns(ctx, &sqlc.ListDueActionRunsParams{Now: now, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 3 {
		t.Fatalf("due action count = %d, want recovered, expired and dependency-wait rows", len(due))
	}
	claimed, err := queries.ClaimActionRun(ctx, &sqlc.ClaimActionRunParams{WorkerID: sql.NullString{String: "reconciler", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true}, Now: now.String, ID: "crashed", Version: crashed.Version})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.State != "running" || !claimed.ClaimedBy.Valid {
		t.Fatalf("recovered action claim = %+v", claimed)
	}
}

func TestJanitorClaimsAreEntryScopedAndRecoverable(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	queries := store.Queries()
	if _, err := queries.CreateConfigSnapshot(ctx, &sqlc.CreateConfigSnapshotParams{
		ID: "janitor-snapshot", Source: "api", DocumentID: "runtime", Revision: "1", StartupAt: "2026-09-11T00:00:00Z", EffectiveJson: "{}", CreatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateStorageRoot(ctx, &sqlc.CreateStorageRootParams{
		ID: "janitor-root", Label: "Janitor", Purpose: "trash", Path: "/media/janitor", Source: "api", SourceSnapshotID: "janitor-snapshot", Revision: "1", CapabilitiesJson: "[]", CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	createEntry := func(id, expiresAt string) {
		t.Helper()
		if _, err := queries.CreateTrashEntry(ctx, &sqlc.CreateTrashEntryParams{
			ID: id, RootID: "janitor-root", State: "trashed", OriginalPrefix: "original/" + id, TrashPrefix: "trash/" + id, ManifestJson: `[]`, RetentionSeconds: 3600, TrashedAt: sql.NullString{String: "2026-09-11T00:00:00Z", Valid: true}, ExpiresAt: expiresAt, ClientStateJson: `{}`, CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
	}
	createJanitor := func(id, entryID, operation string) {
		t.Helper()
		if _, err := queries.CreateJanitorRecord(ctx, &sqlc.CreateJanitorRecordParams{
			ID: id, TrashEntryID: entryID, Operation: operation, State: "queued", OutcomeJson: `{}`, CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
		}); err != nil {
			t.Fatal(err)
		}
	}
	claim := func(id string, version int64, now string) (*sqlc.JanitorRecord, error) {
		return queries.ClaimJanitorRecord(ctx, &sqlc.ClaimJanitorRecordParams{
			WorkerID: sql.NullString{String: "janitor", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true}, Now: now, ID: id, Version: version,
		})
	}

	createEntry("restore-first", "2026-09-12T00:00:00Z")
	createJanitor("restore-first-restore", "restore-first", "restore")
	createJanitor("restore-first-purge", "restore-first", "purge")
	claimedRestore, err := claim("restore-first-restore", 1, "2026-09-11T00:01:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := claim("restore-first-purge", 1, "2026-09-11T00:01:00Z"); err == nil {
		t.Fatal("purge claimed while restore owned the trash entry")
	}
	if _, err := queries.UpdateJanitorRecord(ctx, &sqlc.UpdateJanitorRecordParams{
		State: "reconciling", NextAttemptAt: sql.NullString{}, ClaimedBy: sql.NullString{}, LeaseUntil: sql.NullString{}, OutcomeJson: `{}`, UpdatedAt: "2026-09-11T00:01:01Z", ID: claimedRestore.ID, Version: claimedRestore.Version,
	}); err != nil {
		t.Fatal(err)
	}
	recovered, err := queries.RecoverRunningJanitorRecords(ctx, "2026-09-11T00:02:01Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 0 {
		t.Fatalf("already reconciled janitor records recovered again: %d", len(recovered))
	}
	entry, err := queries.GetTrashEntry(ctx, "restore-first")
	if err != nil {
		t.Fatal(err)
	}
	if !entry.ActiveOperation.Valid || entry.ActiveOperation.String != "restore" || entry.OperationClaimedBy.Valid || entry.OperationLeaseUntil.Valid {
		t.Fatalf("restored lease recovery entry = %+v", entry)
	}
	reclaimedRestore, err := claim("restore-first-restore", 3, "2026-09-11T00:03:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queries.UpdateJanitorRecord(ctx, &sqlc.UpdateJanitorRecordParams{
		State: "succeeded", NextAttemptAt: sql.NullString{}, ClaimedBy: sql.NullString{}, LeaseUntil: sql.NullString{}, OutcomeJson: `{}`, UpdatedAt: "2026-09-11T00:03:01Z", ID: reclaimedRestore.ID, Version: reclaimedRestore.Version,
	}); err != nil {
		t.Fatal(err)
	}
	entry, err = queries.GetTrashEntry(ctx, "restore-first")
	if err != nil {
		t.Fatal(err)
	}
	if entry.State != "restored" || entry.ActiveOperation.Valid {
		t.Fatalf("restored entry final state = %+v", entry)
	}

	createEntry("purge-first", "2026-09-11T00:00:00Z")
	createJanitor("purge-first-purge", "purge-first", "purge")
	createJanitor("purge-first-restore", "purge-first", "restore")
	claimedPurge, err := claim("purge-first-purge", 1, "2026-09-11T00:01:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := claim("purge-first-restore", 1, "2026-09-11T00:01:00Z"); err == nil {
		t.Fatal("restore claimed while purge owned the trash entry")
	}
	if _, err := queries.UpdateJanitorRecord(ctx, &sqlc.UpdateJanitorRecordParams{
		State: "succeeded", NextAttemptAt: sql.NullString{}, ClaimedBy: sql.NullString{}, LeaseUntil: sql.NullString{}, OutcomeJson: `{}`, UpdatedAt: "2026-09-11T00:01:01Z", ID: claimedPurge.ID, Version: claimedPurge.Version,
	}); err != nil {
		t.Fatal(err)
	}
	entry, err = queries.GetTrashEntry(ctx, "purge-first")
	if err != nil {
		t.Fatal(err)
	}
	if entry.State != "purged" || entry.ActiveOperation.Valid {
		t.Fatalf("purged entry final state = %+v", entry)
	}

	createEntry("restart-general", "2026-09-12T00:00:00Z")
	createJanitor("restart-general-record", "restart-general", "restore")
	if _, err := claim("restart-general-record", 1, "2026-09-11T00:01:00Z"); err != nil {
		t.Fatal(err)
	}
	generalRecovered, err := queries.RecoverRunningJanitorRecords(ctx, "2026-09-11T00:01:30Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(generalRecovered) != 1 || generalRecovered[0].ID != "restart-general-record" || generalRecovered[0].State != "reconciling" {
		t.Fatalf("running janitor recovery = %+v", generalRecovered)
	}

	createEntry("restart-purge", "2026-09-11T00:00:00Z")
	createJanitor("restart-purge-record", "restart-purge", "purge")
	claimedPurge, err = claim("restart-purge-record", 1, "2026-09-11T00:01:00Z")
	if err != nil {
		t.Fatal(err)
	}
	recovered, err = queries.RecoverExpiredJanitorRecords(ctx, "2026-09-11T00:03:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].State != "reconciling" {
		t.Fatalf("running janitor recovery = %+v", recovered)
	}
	entry, err = queries.GetTrashEntry(ctx, "restart-purge")
	if err != nil {
		t.Fatal(err)
	}
	if !entry.ActiveOperation.Valid || entry.ActiveOperation.String != "purge" || entry.OperationClaimedBy.Valid || entry.OperationLeaseUntil.Valid {
		t.Fatalf("janitor restart entry = %+v", entry)
	}
	if _, err := claim("restart-purge-record", claimedPurge.Version+1, "2026-09-11T00:04:00Z"); err != nil {
		t.Fatal(err)
	}
}
