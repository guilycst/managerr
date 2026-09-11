package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guilycst/managerr/internal/storage/sqlc"
)

const legacyFixtureTime = "2026-09-11T00:00:00Z"

func legacyExec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatalf("legacy fixture SQL failed: %v\n%s", err, statement)
	}
}

func assertDirtyMigration(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(path, defaultBusyTimeout))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var version, dirty int
	if err := db.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if dirty != 1 {
		t.Fatalf("failed migration version=%d dirty=%d, want dirty=1", version, dirty)
	}
}

func seedLegacyTrackingAndJanitor(t *testing.T, db *sql.DB) {
	t.Helper()
	legacyExec(t, db, `INSERT INTO config_snapshots
		(id, source, document_id, revision, startup_at, effective_json, created_at)
		VALUES ('legacy-snapshot', 'api', 'runtime', '1', ?, '{}', ?)`, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, db, `INSERT INTO connections
		(id, kind, label, endpoint, source, source_snapshot_id, revision, created_at, updated_at)
		VALUES ('legacy-radarr', 'radarr', 'Legacy Radarr', 'http://radarr.invalid:7878', 'api', 'legacy-snapshot', '1', ?, ?)`, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, db, `INSERT INTO storage_roots
		(id, label, purpose, path, source, source_snapshot_id, revision, created_at, updated_at)
		VALUES ('legacy-trash-root', 'Legacy trash', 'trash', '/legacy/trash', 'api', 'legacy-snapshot', '1', ?, ?)`, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, db, `INSERT INTO media_identities
		(id, kind, title, canonical_key, created_at, updated_at)
		VALUES ('legacy-media', 'movie', 'Legacy Film', 'tmdb:9001', ?, ?)`, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, db, `INSERT INTO external_records
		(id, connection_id, media_identity_id, record_kind, external_id, title, payload_json, observed_at, first_seen_at, last_seen_at)
		VALUES ('legacy-record', 'legacy-radarr', 'legacy-media', 'title', '9001', 'Legacy Film', '{}', ?, ?, ?)`, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, db, `INSERT INTO coverage_snapshots
		(id, scan_id, source_id, connection_id, root_id, completeness, reason_codes_json,
		 observed_count, snapshot_revision, started_at, completed_at, observed_at, created_at)
		VALUES ('legacy-coverage', NULL, 'legacy-catalog', 'legacy-radarr', 'legacy-trash-root',
		 'complete', '[]', 1, 'legacy-coverage-revision', ?, ?, ?, ?)`, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)

	legacyExec(t, db, `INSERT INTO tracking_observations
		(id, external_record_id, media_identity_id, connection_id, dimension, status, evidence_json, coverage_id, observed_at, registered_at, imported_at)
		VALUES
		 ('legacy-present-null', NULL, 'legacy-media', NULL, 'registration', 'present', '{"kind":"present-null"}', NULL, ?, NULL, NULL),
		 ('legacy-present-derived', 'legacy-record', 'legacy-media', NULL, 'registration', 'present', '{"kind":"derived"}', NULL, ?, NULL, NULL),
		 ('legacy-unknown-null', NULL, NULL, NULL, 'registration', 'unknown', '{"kind":"unknown"}', NULL, ?, NULL, NULL),
		 ('legacy-absent', NULL, 'legacy-media', 'legacy-radarr', 'registration', 'absent', '{"kind":"absent"}', 'legacy-coverage', ?, NULL, NULL)`, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)

	legacyExec(t, db, `INSERT INTO trash_entries
		(id, root_id, state, original_prefix, trash_prefix, manifest_json, retention_seconds,
		 trashed_at, expires_at, client_state_json, created_at, updated_at)
		VALUES
		 ('legacy-restore-entry', 'legacy-trash-root', 'restoring', 'original/restore', 'trash/restore', '[]', 3600, ?, ?, '{}', ?, ?),
		 ('legacy-purge-entry', 'legacy-trash-root', 'purging', 'original/purge', 'trash/purge', '[]', 3600, ?, ?, '{}', ?, ?),
		 ('legacy-orphan-entry', 'legacy-trash-root', 'restoring', 'original/orphan', 'trash/orphan', '[]', 3600, ?, ?, '{}', ?, ?)`,
		legacyFixtureTime, "2026-09-20T00:00:00Z", legacyFixtureTime, legacyFixtureTime,
		legacyFixtureTime, "2026-09-20T00:00:00Z", legacyFixtureTime, legacyFixtureTime,
		legacyFixtureTime, "2026-09-20T00:00:00Z", legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, db, `INSERT INTO janitor_records
		(id, trash_entry_id, operation, state, next_attempt_at, claimed_by, lease_until, outcome_json, created_at, updated_at)
		VALUES
		 ('legacy-restore', 'legacy-restore-entry', 'restore', 'running', NULL, 'old-worker', '2026-09-11T00:02:00Z', '{}', ?, ?),
		 ('legacy-purge', 'legacy-purge-entry', 'purge', 'reconciling', NULL, NULL, NULL, '{}', ?, ?)`, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)
}

func TestPopulatedLegacyTrackingAndJanitorUpgrade(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.sqlite")
			store, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, version)})
			if err != nil {
				t.Fatal(err)
			}
			seedLegacyTrackingAndJanitor(t, store.DB())
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}

			upgraded, err := Open(path)
			if err != nil {
				t.Fatalf("populated v%d upgrade: %v", version, err)
			}
			defer upgraded.Close()
			var schemaVersion int
			if err := upgraded.DB().QueryRow("SELECT version FROM schema_migrations").Scan(&schemaVersion); err != nil {
				t.Fatal(err)
			}
			if schemaVersion != 4 {
				t.Fatalf("schema version = %d, want 4", schemaVersion)
			}
			assertNoForeignKeyViolations(t, upgraded.DB())

			assertTracking := func(id, wantStatus string, wantConnection bool, connection string, wantLegacyMarker bool) {
				t.Helper()
				var status string
				var connectionID sql.NullString
				var evidence string
				if err := upgraded.DB().QueryRow("SELECT status, connection_id, evidence_json FROM tracking_observations WHERE id = ?", id).Scan(&status, &connectionID, &evidence); err != nil {
					t.Fatal(err)
				}
				if status != wantStatus || connectionID.Valid != wantConnection || (wantConnection && connectionID.String != connection) {
					t.Fatalf("tracking %q = status=%q connection=%+v, want status=%q connection=%q/%t", id, status, connectionID, wantStatus, connection, wantConnection)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal([]byte(evidence), &fields); err != nil {
					t.Fatalf("tracking %q evidence: %v", id, err)
				}
				_, hasLegacyMarker := fields["_managerr_legacy_status"]
				if hasLegacyMarker != wantLegacyMarker {
					t.Fatalf("tracking %q legacy marker=%t, want %t (%s)", id, hasLegacyMarker, wantLegacyMarker, evidence)
				}
			}
			assertTracking("legacy-present-null", "unknown", false, "", true)
			assertTracking("legacy-present-derived", "present", true, "legacy-radarr", false)
			assertTracking("legacy-unknown-null", "unknown", false, "", false)
			assertTracking("legacy-absent", "unknown", true, "legacy-radarr", true)

			assertEntry := func(id, wantState, wantOperation string) int64 {
				t.Helper()
				var state string
				var operation sql.NullString
				var version int64
				if err := upgraded.DB().QueryRow("SELECT state, active_operation, version FROM trash_entries WHERE id = ?", id).Scan(&state, &operation, &version); err != nil {
					t.Fatal(err)
				}
				if state != wantState || !operation.Valid || operation.String != wantOperation || version <= 1 {
					t.Fatalf("trash entry %q = state=%q operation=%+v version=%d", id, state, operation, version)
				}
				return version
			}
			assertEntry("legacy-restore-entry", "restoring", "restore")
			assertEntry("legacy-purge-entry", "purging", "purge")
			assertEntry("legacy-orphan-entry", "restoring", "restore")

			var recoveryCount int
			if err := upgraded.DB().QueryRow("SELECT count(*) FROM janitor_records WHERE id = ? AND state = 'reconciling'", "migration-recovery:legacy-orphan-entry:restore").Scan(&recoveryCount); err != nil {
				t.Fatal(err)
			}
			if recoveryCount != 1 {
				t.Fatalf("orphan recovery records = %d, want 1", recoveryCount)
			}

			ctx := context.Background()
			recovered, err := upgraded.Queries().RecoverRunningJanitorRecords(ctx, "2026-09-11T00:03:00Z")
			if err != nil {
				t.Fatal(err)
			}
			if len(recovered) != 1 || recovered[0].ID != "legacy-restore" || recovered[0].State != "reconciling" {
				t.Fatalf("legacy running recovery = %+v", recovered)
			}
			reclaimed, err := upgraded.Queries().ClaimJanitorRecord(ctx, &sqlc.ClaimJanitorRecordParams{
				WorkerID: sql.NullString{String: "new-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:04:00Z", Valid: true},
				Now: "2026-09-11T00:03:01Z", ID: recovered[0].ID, Version: recovered[0].Version,
			})
			if err != nil {
				t.Fatal(err)
			}
			if reclaimed.State != "running" {
				t.Fatalf("legacy restore reclaim state = %q", reclaimed.State)
			}
			if _, err := upgraded.Queries().UpdateJanitorRecord(ctx, &sqlc.UpdateJanitorRecordParams{
				State: "failed", ClaimedBy: sql.NullString{}, LeaseUntil: sql.NullString{}, OutcomeJson: `{"reason":"synthetic"}`,
				UpdatedAt: "2026-09-11T00:03:02Z", ID: reclaimed.ID, Version: reclaimed.Version,
			}); err != nil {
				t.Fatal(err)
			}

			var purgeVersion int64
			if err := upgraded.DB().QueryRow("SELECT version FROM janitor_records WHERE id = ?", "legacy-purge").Scan(&purgeVersion); err != nil {
				t.Fatal(err)
			}
			reclaimedPurge, err := upgraded.Queries().ClaimJanitorRecord(ctx, &sqlc.ClaimJanitorRecordParams{
				WorkerID: sql.NullString{String: "new-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:04:00Z", Valid: true},
				Now: "2026-09-11T00:03:01Z", ID: "legacy-purge", Version: purgeVersion,
			})
			if err != nil {
				t.Fatal(err)
			}
			if reclaimedPurge.State != "running" {
				t.Fatalf("legacy purge reclaim state = %q", reclaimedPurge.State)
			}
		})
	}
}

func seedLegacyScopeMismatch(t *testing.T, store *Store, kind string, currentSchema bool) {
	t.Helper()
	if currentSchema {
		if _, err := store.DB().Exec("PRAGMA foreign_keys = OFF"); err != nil {
			t.Fatal(err)
		}
	}
	legacyExec(t, store.DB(), `INSERT INTO config_snapshots
		(id, source, document_id, revision, startup_at, effective_json, created_at)
		VALUES ('scope-snapshot', 'api', 'runtime', '1', ?, '{}', ?)`, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, store.DB(), `INSERT INTO storage_roots
		(id, label, purpose, path, source, source_snapshot_id, revision, created_at, updated_at)
		VALUES
		 ('scope-root-a', 'Scope A', 'download', '/scope/a', 'api', 'scope-snapshot', '1', ?, ?),
		 ('scope-root-b', 'Scope B', 'download', '/scope/b', 'api', 'scope-snapshot', '1', ?, ?)`, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, store.DB(), `INSERT INTO discoveries
		(id, root_id, relative_path, entry_type, size_bytes, observed_at, first_seen_at, last_seen_at, manifest_revision, child_manifest_json)
		VALUES
		 ('scope-discovery-a', 'scope-root-a', 'a.mkv', 'file', 1, ?, ?, ?, 'manifest-a', '[]'),
		 ('scope-discovery-b', 'scope-root-b', 'b.mkv', 'file', 1, ?, ?, ?, 'manifest-b', '[]')`,
		legacyFixtureTime, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime,
		legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, store.DB(), `INSERT INTO connections
		(id, kind, label, endpoint, source, source_snapshot_id, revision, created_at, updated_at)
		VALUES ('scope-qbt', 'qbittorrent', 'Scope qBittorrent', 'http://qbt.invalid:8080', 'api', 'scope-snapshot', '1', ?, ?)`, legacyFixtureTime, legacyFixtureTime)

	switch kind {
	case "file":
		legacyExec(t, store.DB(), `INSERT INTO file_observations
			(id, discovery_id, root_id, relative_path, entry_type, size_bytes, observed_at, manifest_revision, child_manifest_json, first_seen_at, last_seen_at)
			VALUES ('scope-file-mismatch', 'scope-discovery-a', 'scope-root-b', 'b.mkv', 'file', 1, ?, 'manifest-b', '[]', ?, ?)`, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)
	case "effect":
		for _, suffix := range []string{"one", "two"} {
			legacyExec(t, store.DB(), `INSERT INTO action_plans
				(id, kind, state, current_revision, current_digest, created_at, updated_at)
				VALUES (?, 'fs.copy', 'ready', 1, ?, ?, ?)`, "scope-plan-"+suffix, "scope-digest-"+suffix, legacyFixtureTime, legacyFixtureTime)
			legacyExec(t, store.DB(), `INSERT INTO action_plan_revisions
				(plan_id, revision, digest, state, input_json, preconditions_json, capabilities_json, manifest_json, created_at, expires_at)
				VALUES (?, 1, ?, 'ready', '{}', '{}', '[]', '[]', ?, '2026-09-20T00:00:00Z')`, "scope-plan-"+suffix, "scope-digest-"+suffix, legacyFixtureTime)
			legacyExec(t, store.DB(), `INSERT INTO action_runs
				(id, plan_id, plan_revision, plan_digest, state, desired_state_json, version, outcome_json, created_at, updated_at)
				VALUES (?, ?, 1, ?, 'queued', '{}', 1, '{}', ?, ?)`, "scope-run-"+suffix, "scope-plan-"+suffix, "scope-digest-"+suffix, legacyFixtureTime, legacyFixtureTime)
			legacyExec(t, store.DB(), `INSERT INTO action_attempts
				(id, action_run_id, attempt_number, phase, state, started_at, outcome_certainty, evidence_json)
				VALUES (?, ?, 1, 'dispatch', 'running', ?, 'uncertain', '{}')`, "scope-attempt-"+suffix, "scope-run-"+suffix, legacyFixtureTime)
		}
		legacyExec(t, store.DB(), `INSERT INTO action_effects
			(id, action_run_id, attempt_id, ordinal, target_kind, target_id, effect_kind, state, evidence_json, observed_at)
			VALUES ('scope-effect-mismatch', 'scope-run-one', 'scope-attempt-two', 0, 'file', 'target', 'copy', 'pending', '{}', ?)`, legacyFixtureTime)
	case "trash":
		legacyExec(t, store.DB(), `INSERT INTO trash_entries
			(id, root_id, state, original_prefix, trash_prefix, manifest_json, retention_seconds, trashed_at, expires_at, client_state_json, created_at, updated_at)
			VALUES
			 ('scope-trash-one', 'scope-root-a', 'trashed', 'original/one', 'trash/one', '[]', 3600, ?, '2026-09-20T00:00:00Z', '{}', ?, ?),
			 ('scope-trash-two', 'scope-root-b', 'trashed', 'original/two', 'trash/two', '[]', 3600, ?, '2026-09-20T00:00:00Z', '{}', ?, ?)`,
			legacyFixtureTime, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)
		legacyExec(t, store.DB(), `INSERT INTO trash_items
			(id, entry_id, root_id, original_relative_path, trash_relative_path, entry_type, size_bytes, state)
			VALUES ('scope-trash-item-mismatch', 'scope-trash-one', 'scope-root-b', 'movie.mkv', 'movie.mkv', 'file', 1, 'selected')`)
	default:
		t.Fatalf("unknown mismatch kind %q", kind)
	}
}

func assertNoForeignKeyViolations(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		var table, rowID, parent string
		var foreignKey int
		if err := rows.Scan(&table, &rowID, &parent, &foreignKey); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("foreign key violation: table=%s row=%s parent=%s fk=%d", table, rowID, parent, foreignKey)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestPopulatedLegacyScopeMismatchAbortsUpgrade(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		for _, kind := range []string{"file", "effect", "trash"} {
			t.Run(fmt.Sprintf("v%d_%s", version, kind), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "mismatch.sqlite")
				store, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, version)})
				if err != nil {
					t.Fatal(err)
				}
				seedLegacyScopeMismatch(t, store, kind, version == 3)
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}

				if _, err := Open(path); err == nil {
					t.Fatalf("v%d %s mismatch upgrade succeeded", version, kind)
				} else if !strings.Contains(err.Error(), "run sqlite migrations") {
					t.Fatalf("v%d %s mismatch error = %v, want migration failure", version, kind, err)
				}
				assertDirtyMigration(t, path)
			})
		}
	}
}

func seedLegacyJanitorConflict(t *testing.T, store *Store, conflict string, currentSchema bool) {
	t.Helper()
	if currentSchema {
		if _, err := store.DB().Exec("PRAGMA foreign_keys = OFF"); err != nil {
			t.Fatal(err)
		}
	}
	legacyExec(t, store.DB(), `INSERT INTO config_snapshots
		(id, source, document_id, revision, startup_at, effective_json, created_at)
		VALUES ('janitor-conflict-snapshot', 'api', 'runtime', '1', ?, '{}', ?)`, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, store.DB(), `INSERT INTO storage_roots
		(id, label, purpose, path, source, source_snapshot_id, revision, created_at, updated_at)
		VALUES ('janitor-conflict-root', 'Conflict trash', 'trash', '/conflict/trash', 'api', 'janitor-conflict-snapshot', '1', ?, ?)`, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, store.DB(), `INSERT INTO trash_entries
		(id, root_id, state, original_prefix, trash_prefix, manifest_json, retention_seconds, trashed_at, expires_at, client_state_json, created_at, updated_at)
		VALUES ('janitor-conflict-entry', 'janitor-conflict-root', 'restoring', 'original/conflict', 'trash/conflict', '[]', 3600, ?, '2026-09-20T00:00:00Z', '{}', ?, ?)`, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)
	if conflict == "dual" {
		legacyExec(t, store.DB(), `INSERT INTO janitor_records
			(id, trash_entry_id, operation, state, claimed_by, lease_until, outcome_json, created_at, updated_at)
			VALUES
			 ('janitor-conflict-restore', 'janitor-conflict-entry', 'restore', 'running', 'worker-a', '2026-09-11T00:02:00Z', '{}', ?, ?),
			 ('janitor-conflict-purge', 'janitor-conflict-entry', 'purge', 'reconciling', NULL, NULL, '{}', ?, ?)`, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)
		return
	}
	if conflict == "state" {
		legacyExec(t, store.DB(), `INSERT INTO janitor_records
			(id, trash_entry_id, operation, state, claimed_by, lease_until, outcome_json, created_at, updated_at)
			VALUES ('janitor-conflict-purge', 'janitor-conflict-entry', 'purge', 'running', 'worker-a', '2026-09-11T00:02:00Z', '{}', ?, ?)`, legacyFixtureTime, legacyFixtureTime)
		return
	}
	t.Fatalf("unknown janitor conflict %q", conflict)
}

func TestAmbiguousLegacyJanitorStateAbortsUpgrade(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		for _, conflict := range []string{"dual", "state"} {
			t.Run(fmt.Sprintf("v%d_%s", version, conflict), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "janitor-conflict.sqlite")
				store, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, version)})
				if err != nil {
					t.Fatal(err)
				}
				seedLegacyJanitorConflict(t, store, conflict, version == 3)
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err := Open(path); err == nil {
					t.Fatalf("v%d %s janitor conflict upgrade succeeded", version, conflict)
				}
				assertDirtyMigration(t, path)
			})
		}
	}
}

func TestPersistentStoreRejectsNonCanonicalLockOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.sqlite")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWithOptions(Options{Path: path, LockPath: filepath.Join(dir, "other.lock")}); err == nil || !strings.Contains(err.Error(), "canonical database lock path") {
		t.Fatalf("distinct persistent lock override error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := OpenWithOptions(Options{Path: path, LockPath: path + defaultLockSuffix})
	if err != nil {
		t.Fatalf("canonical persistent lock override rejected: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestApprovedEarlyPurgeRequiresExactApprovalAndEntryVersion(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	queries := store.Queries()
	if _, err := queries.CreateConfigSnapshot(ctx, &sqlc.CreateConfigSnapshotParams{
		ID: "early-snapshot", Source: "api", DocumentID: "runtime", Revision: "1", StartupAt: legacyFixtureTime, EffectiveJson: "{}", CreatedAt: legacyFixtureTime,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateStorageRoot(ctx, &sqlc.CreateStorageRootParams{
		ID: "early-trash-root", Label: "Early purge trash", Purpose: "trash", Path: "/early/trash", Source: "api", SourceSnapshotID: "early-snapshot", Revision: "1", CapabilitiesJson: "[]", CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateActionPlan(ctx, &sqlc.CreateActionPlanParams{
		ID: "early-plan", Kind: "trash.purge", State: "ready", CurrentRevision: 1, CurrentDigest: "early-digest", CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateActionPlanRevision(ctx, &sqlc.CreateActionPlanRevisionParams{
		PlanID: "early-plan", Revision: 1, Digest: "early-digest", State: "ready", InputJson: `{}`, PreconditionsJson: `{}`, CapabilitiesJson: `[]`, ManifestJson: `[]`, CreatedAt: legacyFixtureTime, ExpiresAt: "2026-09-20T00:00:00Z", ReadyAt: sql.NullString{String: legacyFixtureTime, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: "early-decision", PlanID: "early-plan", PlanRevision: 1, PlanDigest: "early-digest", Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "early-review", IdempotencyKey: "early-key", CreatedAt: legacyFixtureTime,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateActionRun(ctx, &sqlc.CreateActionRunParams{
		ID: "early-action-run", PlanID: "early-plan", PlanRevision: 1, PlanDigest: "early-digest", State: "queued", DesiredStateJson: `{}`, Version: 1, OutcomeJson: `{}`, CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateTrashEntry(ctx, &sqlc.CreateTrashEntryParams{
		ID: "early-entry", RootID: "early-trash-root", State: "trashed", OriginalPrefix: "original/early", TrashPrefix: "trash/early", ManifestJson: `[]`, RetentionSeconds: 3600, TrashedAt: sql.NullString{String: legacyFixtureTime, Valid: true}, ExpiresAt: "2026-09-20T00:00:00Z", ClientStateJson: `{}`, CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateJanitorRecord(ctx, &sqlc.CreateJanitorRecordParams{
		ID: "early-janitor", TrashEntryID: "early-entry", Operation: "purge", State: "queued", OutcomeJson: `{}`, CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := queries.ClaimJanitorRecord(ctx, &sqlc.ClaimJanitorRecordParams{
		WorkerID: sql.NullString{String: "janitor", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true}, Now: "2026-09-11T00:01:00Z", ID: "early-janitor", Version: 1,
	}); err == nil {
		t.Fatal("unapproved early purge was claimed")
	}
	assertEarlyPurgePending(t, store)

	base := sqlc.ClaimApprovedEarlyPurgeParams{
		WorkerID: sql.NullString{String: "janitor", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true},
		ApprovalPlanID: sql.NullString{String: "early-plan", Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: "early-digest", Valid: true},
		ApprovalDecisionID: sql.NullString{String: "early-decision", Valid: true}, ApprovalActionRunID: sql.NullString{String: "early-action-run", Valid: true}, ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true},
		Now: "2026-09-11T00:01:00Z", ID: "early-janitor", Version: 1, TrashEntryID: "early-entry",
	}
	wrongDigest := base
	wrongDigest.ApprovalPlanDigest = sql.NullString{String: "wrong-digest", Valid: true}
	if _, err := queries.ClaimApprovedEarlyPurge(ctx, &wrongDigest); err == nil {
		t.Fatal("early purge accepted a digest that was not approved")
	}
	assertEarlyPurgePending(t, store)

	wrongeVersion := base
	wrongeVersion.ApprovedEntryVersion = sql.NullInt64{Int64: 2, Valid: true}
	if _, err := queries.ClaimApprovedEarlyPurge(ctx, &wrongeVersion); err == nil {
		t.Fatal("early purge accepted a stale entry version")
	}
	assertEarlyPurgePending(t, store)

	claimed, err := queries.ClaimApprovedEarlyPurge(ctx, &base)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.State != "running" || !claimed.ApprovalPlanID.Valid || claimed.ApprovalPlanID.String != "early-plan" || claimed.ApprovedEntryVersion.Int64 != 1 {
		t.Fatalf("approved early purge claim = %+v", claimed)
	}
	var entryState, activeOperation, expiresAt string
	var entryVersion int64
	if err := store.DB().QueryRow("SELECT state, active_operation, version, expires_at FROM trash_entries WHERE id = 'early-entry'").Scan(&entryState, &activeOperation, &entryVersion, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if entryState != "purging" || activeOperation != "purge" || entryVersion != 2 || expiresAt != "2026-09-20T00:00:00Z" {
		t.Fatalf("approved early purge entry = state=%q operation=%q version=%d expires=%q", entryState, activeOperation, entryVersion, expiresAt)
	}
	if _, err := store.DB().Exec("UPDATE janitor_records SET approval_plan_digest = 'changed' WHERE id = 'early-janitor'"); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("approval binding mutation error = %v", err)
	}
	if _, err := queries.UpdateJanitorRecord(ctx, &sqlc.UpdateJanitorRecordParams{
		State: "failed", ClaimedBy: sql.NullString{}, LeaseUntil: sql.NullString{}, OutcomeJson: `{"reason":"synthetic"}`,
		UpdatedAt: "2026-09-11T00:01:01Z", ID: claimed.ID, Version: claimed.Version,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow("SELECT state FROM trash_entries WHERE id = 'early-entry'").Scan(&entryState); err != nil {
		t.Fatal(err)
	}
	if entryState != "failed" {
		t.Fatalf("failed approved purge entry state = %q", entryState)
	}
}

func assertEarlyPurgePending(t *testing.T, store *Store) {
	t.Helper()
	var janitorState, entryState string
	var activeOperation sql.NullString
	var entryVersion int64
	if err := store.DB().QueryRow(`SELECT jr.state, te.state, te.active_operation, te.version
		FROM janitor_records AS jr JOIN trash_entries AS te ON te.id = jr.trash_entry_id
		WHERE jr.id = 'early-janitor'`).Scan(&janitorState, &entryState, &activeOperation, &entryVersion); err != nil {
		t.Fatal(err)
	}
	if janitorState != "queued" || entryState != "trashed" || activeOperation.Valid || entryVersion != 1 {
		t.Fatalf("early purge pending state = janitor=%q entry=%q operation=%+v version=%d", janitorState, entryState, activeOperation, entryVersion)
	}
}
