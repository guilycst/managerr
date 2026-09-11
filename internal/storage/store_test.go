package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guilycst/managerr/internal/storage/sqlc"
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
	if version != 2 {
		t.Fatalf("migration version = %d, want 2", version)
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
	} {
		var got string
		if err := store.DB().QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&got); err != nil {
			t.Fatalf("table %q missing: %v", table, err)
		}
	}
}

func TestMigrationsUpgradeFromVersionOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.sqlite")
	store, err := OpenWithOptions(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec("DELETE FROM schema_migrations"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec("DROP INDEX idx_due_action_runs_claim"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec("DROP INDEX idx_expired_trash_entries"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec("INSERT INTO schema_migrations (version, dirty) VALUES (1, 0)"); err != nil {
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
	if version != 2 {
		t.Fatalf("upgraded migration version = %d, want 2", version)
	}
	var count int
	if err := upgraded.DB().QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_due_action_runs_claim'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("upgrade index count = %d, want 1", count)
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
	queries := store.Queries()
	if _, err := queries.CreateConfigSnapshot(context.Background(), &sqlc.CreateConfigSnapshotParams{
		ID: "snap-api", Source: "api", DocumentID: "runtime", Revision: "1",
		StartupAt: "2026-09-11T00:00:00Z", EffectiveJson: "{}", CreatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	for _, connection := range []sqlc.CreateConnectionParams{
		{ID: "qbt-main", Kind: "qbittorrent", Label: "Synthetic qBittorrent", Endpoint: "http://qbt.invalid:8080", Source: "api", SourceSnapshotID: "snap-api", Revision: "1", CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z"},
		{ID: "radarr-main", Kind: "radarr", Label: "Synthetic Radarr", Endpoint: "http://radarr.invalid:7878", Source: "api", SourceSnapshotID: "snap-api", Revision: "1", CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z"},
	} {
		if _, err := queries.CreateConnection(context.Background(), &connection); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := queries.CreateMediaIdentity(context.Background(), &sqlc.CreateMediaIdentityParams{
		ID: "media-example", Kind: "movie", Title: "Example Film", CanonicalKey: "tmdb:4242",
		CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateExternalRecord(context.Background(), &sqlc.CreateExternalRecordParams{
		ID: "record-qbt", ConnectionID: "qbt-main", RecordKind: "title", ExternalID: "42",
		MediaIdentityID: sql.NullString{String: "media-example", Valid: true}, PayloadJson: `{}`,
		ObservedAt: "2026-09-11T00:00:00Z", FirstSeenAt: "2026-09-11T00:00:00Z", LastSeenAt: "2026-09-11T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateCoverageSnapshot(context.Background(), &sqlc.CreateCoverageSnapshotParams{
		ID: "coverage-complete", Completeness: "complete", ReasonCodesJson: `[]`, ObservedCount: 1,
		ConnectionID: sql.NullString{String: "radarr-main", Valid: true}, SnapshotRevision: sql.NullString{String: "scan-1", Valid: true},
		StartedAt: sql.NullString{String: "2026-09-11T00:00:00Z", Valid: true}, CompletedAt: sql.NullString{String: "2026-09-11T00:00:01Z", Valid: true},
		ObservedAt: "2026-09-11T00:00:01Z", CreatedAt: "2026-09-11T00:00:01Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateTrackingObservation(context.Background(), &sqlc.CreateTrackingObservationParams{
		ID: "tracking-mismatch", ExternalRecordID: sql.NullString{String: "record-qbt", Valid: true},
		ConnectionID: sql.NullString{String: "radarr-main", Valid: true}, Dimension: "registration", Status: "present",
		EvidenceJson: `{}`, ObservedAt: "2026-09-11T00:00:00Z",
	}); err == nil {
		t.Fatal("mismatched tracking connection was accepted")
	}
	if _, err := queries.CreateTrackingObservation(context.Background(), &sqlc.CreateTrackingObservationParams{
		ID: "tracking-absent-without-coverage", MediaIdentityID: sql.NullString{String: "media-example", Valid: true},
		ConnectionID: sql.NullString{String: "radarr-main", Valid: true}, Dimension: "registration", Status: "absent",
		EvidenceJson: `{}`, ObservedAt: "2026-09-11T00:00:00Z",
	}); err == nil {
		t.Fatal("absence without complete coverage was accepted")
	}
	abset, err := queries.CreateTrackingObservation(context.Background(), &sqlc.CreateTrackingObservationParams{
		ID: "tracking-absent", MediaIdentityID: sql.NullString{String: "media-example", Valid: true},
		ConnectionID: sql.NullString{String: "radarr-main", Valid: true}, Dimension: "registration", Status: "absent",
		CoverageID: sql.NullString{String: "coverage-complete", Valid: true}, EvidenceJson: `{}`, ObservedAt: "2026-09-11T00:00:01Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !abset.MediaIdentityID.Valid || !abset.ConnectionID.Valid || abset.ExternalRecordID.Valid || abset.Status != "absent" {
		t.Fatalf("unexpected absent observation: %+v", abset)
	}
	row, err := store.Queries().CreateTrackingObservation(context.Background(), &sqlc.CreateTrackingObservationParams{
		ID: "tracking-unknown", Dimension: "registration", Status: "unknown",
		EvidenceJson: `{}`, ObservedAt: "2026-09-11T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "unknown" || row.ExternalRecordID.Valid || row.ConnectionID.Valid {
		t.Fatalf("unexpected unknown observation: %+v", row)
	}
}
