package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	migratedb "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/guilycst/mastarr/internal/storage/sqlc"
	"github.com/guilycst/mastarr/migrations"
)

func TestRound4TrackingUpgradeQuarantinesFalseAndUnscopedRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tracking-v4.sqlite")
	store, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, 4)})
	if err != nil {
		t.Fatal(err)
	}
	legacyExec(t, store.DB(), `INSERT INTO config_snapshots
		(id, source, document_id, revision, startup_at, effective_json, created_at)
		VALUES ('round4-snapshot', 'api', 'runtime', '1', ?, '{}', ?)`, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, store.DB(), `INSERT INTO connections
		(id, kind, label, endpoint, source, source_snapshot_id, revision, created_at, updated_at)
		VALUES
		 ('round4-connection-a', 'radarr', 'Round4 A', 'http://radarr-a.invalid', 'api', 'round4-snapshot', '1', ?, ?),
		 ('round4-connection-b', 'radarr', 'Round4 B', 'http://radarr-b.invalid', 'api', 'round4-snapshot', '1', ?, ?)`, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, store.DB(), `INSERT INTO media_identities
		(id, kind, title, canonical_key, created_at, updated_at)
		VALUES
		 ('round4-media-a', 'movie', 'Round4 A', 'tmdb:9401', ?, ?),
		 ('round4-media-b', 'movie', 'Round4 B', 'tmdb:9402', ?, ?)`, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, store.DB(), `INSERT INTO external_records
		(id, connection_id, media_identity_id, record_kind, external_id, title, payload_json, observed_at, first_seen_at, last_seen_at)
		VALUES ('round4-record-a', 'round4-connection-a', 'round4-media-a', 'title', '9401', 'Round4 A', '{}', ?, ?, ?)`, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)

	// The v4 triggers correctly reject these rows for new callers. Disable only
	// those guards to model a populated older database whose split identity was
	// written before the v5 compatibility boundary.
	legacyExec(t, store.DB(), `DROP TRIGGER tracking_observations_connection_scope_insert`)
	legacyExec(t, store.DB(), `DROP TRIGGER tracking_observations_identity_scope_insert`)
	legacyExec(t, store.DB(), `PRAGMA foreign_keys = OFF`)
	legacyExec(t, store.DB(), `INSERT INTO tracking_observations
		(id, external_record_id, media_identity_id, connection_id, root_id, dimension, status, evidence_json, observed_at)
		VALUES
		 ('round4-valid', 'round4-record-a', 'round4-media-a', 'round4-connection-a', NULL, 'registration', 'present', '{"kind":"valid"}', ?),
		 ('round4-wrong-connection', 'round4-record-a', 'round4-media-a', 'round4-connection-b', NULL, 'registration', 'present', '{"kind":"wrong-connection"}', ?),
		 ('round4-wrong-media', 'round4-record-a', 'round4-media-b', 'round4-connection-a', NULL, 'registration', 'present', '{"kind":"wrong-media"}', ?),
		 ('round4-missing-record', 'round4-missing-record', NULL, 'round4-connection-a', NULL, 'registration', 'unknown', '{"kind":"missing-record"}', ?),
		 ('round4-unscoped-unknown', NULL, NULL, NULL, NULL, 'registration', 'unknown', '{"kind":"unscoped"}', ?)`, legacyFixtureTime, "2026-09-11T00:00:01Z", "2026-09-11T00:00:02Z", "2026-09-11T00:00:03Z", "2026-09-11T00:00:04Z")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatalf("v4 populated tracking upgrade: %v", err)
	}
	defer upgraded.Close()
	var version int
	if err := upgraded.DB().QueryRow("SELECT version FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 6 {
		t.Fatalf("schema version = %d, want 6", version)
	}

	active, err := upgraded.Queries().ListTrackingObservations(context.Background(), &sqlc.ListTrackingObservationsParams{})
	if err != nil {
		t.Fatal(err)
	}
	activeIDs := make(map[string]bool, len(active))
	for _, row := range active {
		activeIDs[row.ID] = true
		if row.ConnectionID == "" {
			t.Fatalf("active tracking row has empty connection: %+v", row)
		}
	}
	if !activeIDs["round4-valid"] || len(activeIDs) != 1 {
		t.Fatalf("active tracking IDs = %v, want only round4-valid", activeIDs)
	}

	quarantined, err := upgraded.Queries().ListTrackingObservationQuarantine(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byOriginalID := make(map[string]*sqlc.TrackingObservationQuarantine, len(quarantined))
	for _, row := range quarantined {
		byOriginalID[row.OriginalObservationID] = row
		if row.NormalizedStatus != "unknown" {
			t.Fatalf("quarantine row normalized status = %q, want unknown", row.NormalizedStatus)
		}
		if row.OriginalStatus == "" || row.OriginalEvidenceJson == "" {
			t.Fatalf("quarantine row lost original evidence: %+v", row)
		}
	}
	wantReasons := map[string]string{
		"round4-wrong-connection": "external_connection_mismatch",
		"round4-wrong-media":      "external_media_identity_mismatch",
		"round4-missing-record":   "external_record_missing",
		"round4-unscoped-unknown": "connection_scope_missing",
	}
	if len(byOriginalID) != len(wantReasons) {
		t.Fatalf("quarantine rows = %d, want %d (%v)", len(byOriginalID), len(wantReasons), byOriginalID)
	}
	for id, reason := range wantReasons {
		row, ok := byOriginalID[id]
		if !ok || row.Reason != reason {
			t.Fatalf("quarantine %q = %+v, want reason %q", id, row, reason)
		}
		if activeIDs[id] {
			t.Fatalf("quarantined row %q leaked into active tracking", id)
		}
	}
	if _, err := upgraded.DB().Exec(`UPDATE tracking_observation_quarantine SET reason = 'identity_scope_missing' WHERE original_observation_id = 'round4-wrong-connection'`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("quarantine update error = %v", err)
	}
	if _, err := upgraded.DB().Exec(`DELETE FROM tracking_observation_quarantine WHERE original_observation_id = 'round4-wrong-connection'`); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("quarantine delete error = %v", err)
	}
	assertNoForeignKeyViolations(t, upgraded.DB())

	if _, err := upgraded.DB().Exec(`INSERT INTO tracking_observations
		(id, external_record_id, media_identity_id, connection_id, dimension, status, evidence_json, observed_at)
		VALUES ('round4-fresh-null', NULL, NULL, NULL, 'registration', 'unknown', '{}', ?)`, legacyFixtureTime); err == nil {
		t.Fatal("fresh unscoped unknown tracking row was accepted")
	}
}

func TestRound4JanitorUpgradeAllowsMatchingActiveWithTerminalHistory(t *testing.T) {
	for _, tc := range []struct {
		operation  string
		entryState string
	}{
		{operation: "restore", entryState: "restoring"},
		{operation: "purge", entryState: "purging"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.operation+"-history.sqlite")
			store, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, 4)})
			if err != nil {
				t.Fatal(err)
			}
			seedRound4JanitorHistory(t, store, tc.operation, tc.entryState)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}

			upgraded, err := Open(path)
			if err != nil {
				t.Fatalf("matching %s with terminal history upgrade: %v", tc.operation, err)
			}
			defer upgraded.Close()
			assertNoForeignKeyViolations(t, upgraded.DB())
			var state, oppositeState string
			if err := upgraded.DB().QueryRow("SELECT state FROM janitor_records WHERE id = ?", "round4-active-"+tc.operation).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if err := upgraded.DB().QueryRow("SELECT state FROM janitor_records WHERE id = ?", "round4-opposite-"+tc.operation).Scan(&oppositeState); err != nil {
				t.Fatal(err)
			}
			if state != "running" || oppositeState != "failed" {
				t.Fatalf("janitor history states = %q/%q, want running/failed", state, oppositeState)
			}

			ctx := context.Background()
			recovered, err := upgraded.Queries().RecoverRunningJanitorRecords(ctx, "2026-09-11T00:03:00Z")
			if err != nil {
				t.Fatal(err)
			}
			if len(recovered) != 1 || recovered[0].ID != "round4-active-"+tc.operation || recovered[0].State != "reconciling" {
				t.Fatalf("recovered %s history = %+v", tc.operation, recovered)
			}
			claimed, err := upgraded.Queries().ClaimJanitorRecord(ctx, &sqlc.ClaimJanitorRecordParams{
				WorkerID:   sql.NullString{String: "round4-worker", Valid: true},
				LeaseUntil: sql.NullString{String: "2026-09-11T00:04:00Z", Valid: true},
				Now:        "2026-09-11T00:03:01Z", ID: recovered[0].ID, Version: recovered[0].Version,
			})
			if err != nil {
				t.Fatal(err)
			}
			if claimed.State != "running" {
				t.Fatalf("reclaimed %s state = %q", tc.operation, claimed.State)
			}
			var activeOperation, entryState string
			if err := upgraded.DB().QueryRow("SELECT active_operation, state FROM trash_entries WHERE id = 'round4-entry'").Scan(&activeOperation, &entryState); err != nil {
				t.Fatal(err)
			}
			if activeOperation != tc.operation || entryState != tc.entryState {
				t.Fatalf("reclaimed %s entry = operation=%q state=%q", tc.operation, activeOperation, entryState)
			}
		})
	}
}

func seedRound4JanitorHistory(t *testing.T, store *Store, operation, entryState string) {
	t.Helper()
	queries := store.Queries()
	ctx := context.Background()
	if _, err := queries.CreateConfigSnapshot(ctx, &sqlc.CreateConfigSnapshotParams{
		ID: "round4-janitor-snapshot", Source: "api", DocumentID: "runtime", Revision: "1", StartupAt: legacyFixtureTime, EffectiveJson: "{}", CreatedAt: legacyFixtureTime,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateStorageRoot(ctx, &sqlc.CreateStorageRootParams{
		ID: "round4-janitor-root", Label: "Round4 trash", Purpose: "trash", Path: "/round4/trash", Source: "api", SourceSnapshotID: "round4-janitor-snapshot", Revision: "1", CapabilitiesJson: "[]", CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.CreateTrashEntry(ctx, &sqlc.CreateTrashEntryParams{
		ID: "round4-entry", RootID: "round4-janitor-root", State: entryState, OriginalPrefix: "original/round4", TrashPrefix: "trash/round4", ManifestJson: `[{"path":"round4.mkv"}]`, RetentionSeconds: 3600,
		TrashedAt: sql.NullString{String: legacyFixtureTime, Valid: true}, ExpiresAt: "2026-09-20T00:00:00Z", ClientStateJson: `{}`, CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime,
	}); err != nil {
		t.Fatal(err)
	}
	legacyExec(t, store.DB(), `INSERT INTO janitor_records
				(id, trash_entry_id, operation, state, claimed_by, lease_until, outcome_json, created_at, updated_at)
				VALUES (?, 'round4-entry', ?, 'running', 'old-worker', '2026-09-11T00:02:00Z', '{}', ?, ?)`, "round4-active-"+operation, operation, legacyFixtureTime, legacyFixtureTime)
	opposite := "purge"
	if operation == "purge" {
		opposite = "restore"
	}
	legacyExec(t, store.DB(), `INSERT INTO janitor_records
				(id, trash_entry_id, operation, state, outcome_json, created_at, updated_at)
				VALUES (?, 'round4-entry', ?, 'failed', '{"reason":"previous operation"}', ?, ?)`, "round4-opposite-"+operation, opposite, legacyFixtureTime, legacyFixtureTime)
}

func TestRound4EarlyPurgeRejectsUnboundOrUnsafeClaims(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	fixture := seedRound4EarlyPurge(t, store, "safe", "queued")

	// The exact target is append-only and the manifest cannot be changed after
	// it becomes an approved target.
	if _, err := store.DB().Exec(`UPDATE early_purge_plan_targets SET manifest_json = '[{"path":"changed.mkv"}]' WHERE plan_id = ?`, fixture.planID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("plan target mutation error = %v", err)
	}
	if _, err := store.DB().Exec(`DELETE FROM early_purge_plan_targets WHERE plan_id = ?`, fixture.planID); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("plan target delete error = %v", err)
	}

	base := sqlc.ClaimApprovedEarlyPurgeParams{
		WorkerID: sql.NullString{String: "round4-janitor", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true},
		ApprovalPlanID: sql.NullString{String: fixture.planID, Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: fixture.digest, Valid: true},
		ApprovalDecisionID: sql.NullString{String: fixture.decisionID, Valid: true}, ApprovalActionRunID: sql.NullString{String: fixture.actionRunID, Valid: true}, ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true},
		Now: "2026-09-11T00:01:00Z", ID: fixture.janitorID, Version: 1, TrashEntryID: fixture.entryID,
	}

	wrongEntry := seedRound4EarlyPurgeEntry(t, store, "safe-other-entry", "safe-other-janitor")
	wrongEntryClaim := base
	wrongEntryClaim.ID = wrongEntry.janitorID
	wrongEntryClaim.TrashEntryID = wrongEntry.entryID
	if _, err := store.Queries().ClaimApprovedEarlyPurge(ctx, &wrongEntryClaim); err == nil {
		t.Fatal("approved plan claimed an unrelated trash entry")
	}
	assertRound4EarlyPurgePending(t, store, fixture)

	for _, tc := range []struct {
		name       string
		state      string
		cancel     sql.NullString
		deadline   sql.NullString
		claimedBy  sql.NullString
		leaseUntil sql.NullString
	}{
		{name: "waiting", state: "waiting_dependency"},
		{name: "needs-review", state: "needs_review"},
		{name: "cancelled", state: "queued", cancel: sql.NullString{String: "2026-09-11T00:00:30Z", Valid: true}},
		{name: "expired-deadline", state: "queued", deadline: sql.NullString{String: "2026-09-11T00:00:30Z", Valid: true}},
		{name: "worker-lease-mismatch", state: "queued", claimedBy: sql.NullString{String: "other-worker", Valid: true}, leaseUntil: sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caseFixture := seedRound4EarlyPurge(t, store, tc.name, tc.state)
			if tc.cancel.Valid || tc.deadline.Valid || tc.claimedBy.Valid || tc.leaseUntil.Valid {
				if _, err := store.DB().Exec(`UPDATE action_runs SET cancellation_requested_at = ?, deadline_at = ?, claimed_by = ?, lease_until = ? WHERE id = ?`, tc.cancel, tc.deadline, tc.claimedBy, tc.leaseUntil, caseFixture.actionRunID); err != nil {
					t.Fatal(err)
				}
			}
			claim := base
			claim.ApprovalPlanID = sql.NullString{String: caseFixture.planID, Valid: true}
			claim.ApprovalPlanDigest = sql.NullString{String: caseFixture.digest, Valid: true}
			claim.ApprovalDecisionID = sql.NullString{String: caseFixture.decisionID, Valid: true}
			claim.ApprovalActionRunID = sql.NullString{String: caseFixture.actionRunID, Valid: true}
			claim.ApprovalActionRunVersion = sql.NullInt64{Int64: 1, Valid: true}
			claim.ID = caseFixture.janitorID
			claim.TrashEntryID = caseFixture.entryID
			if _, err := store.Queries().ClaimApprovedEarlyPurge(ctx, &claim); err == nil {
				t.Fatalf("unsafe %s action run was claimed", tc.name)
			}
			wantActionState := "queued"
			if tc.state == "waiting_dependency" || tc.state == "needs_review" {
				wantActionState = tc.state
			}
			assertRound4EarlyPurgePendingState(t, store, caseFixture, wantActionState)
		})
	}

	// A target for another action kind, an empty manifest and a mismatched
	// manifest are all rejected before approval can exist.
	for _, tc := range []struct {
		name, kind, intent, manifest string
	}{
		{name: "unsupported-kind", kind: "fs.copy", intent: "fs.delete", manifest: `[{"path":"movie.mkv"}]`},
		{name: "empty-manifest", kind: "fs.delete", intent: "fs.delete", manifest: `[]`},
		{name: "mismatched-manifest", kind: "fs.delete", intent: "fs.delete", manifest: `[{"path":"other.mkv"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planID := "round4-" + tc.name + "-plan"
			entryID := "round4-" + tc.name + "-entry"
			if _, err := store.Queries().CreateActionPlan(ctx, &sqlc.CreateActionPlanParams{ID: planID, Kind: tc.kind, State: "ready", CurrentRevision: 1, CurrentDigest: planID + "-digest", CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Queries().CreateActionPlanRevision(ctx, &sqlc.CreateActionPlanRevisionParams{PlanID: planID, Revision: 1, Digest: planID + "-digest", State: "ready", InputJson: `{}`, PreconditionsJson: `{}`, CapabilitiesJson: `[]`, ManifestJson: `[{"path":"movie.mkv"}]`, CreatedAt: legacyFixtureTime, ExpiresAt: "2026-09-20T00:00:00Z", ReadyAt: sql.NullString{String: legacyFixtureTime, Valid: true}}); err != nil {
				t.Fatal(err)
			}
			entry := seedRound4EarlyPurgeEntry(t, store, entryID, "round4-"+tc.name+"-janitor")
			if _, err := store.Queries().CreateEarlyPurgePlanTarget(ctx, &sqlc.CreateEarlyPurgePlanTargetParams{PlanID: planID, Revision: 1, PlanDigest: planID + "-digest", IntentKind: tc.intent, TrashEntryID: entry.entryID, TrashEntryVersion: 1, ManifestJson: tc.manifest, CreatedAt: legacyFixtureTime}); err == nil {
				t.Fatalf("unsafe %s target was accepted", tc.name)
			}
		})
	}
}

func TestRound4MigrationDownRestoresV4Shape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "down.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedRound4EarlyPurge(t, store, "down", "queued")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", sqliteDSN(path, defaultBusyTimeout))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := configureSQLite(db, defaultBusyTimeout); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	driver, err := migratedb.WithInstance(db, &migratedb.Config{NoTxWrap: true})
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	runner, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := runner.Steps(-2); err != nil {
		_, _ = runner.Close()
		t.Fatalf("v6/v5 down migrations: %v", err)
	}
	sourceErr, databaseErr := runner.Close()
	if sourceErr != nil || databaseErr != nil {
		t.Fatalf("close down migration runner: source=%v database=%v", sourceErr, databaseErr)
	}

	checkDB, err := sql.Open("sqlite", sqliteDSN(path, defaultBusyTimeout))
	if err != nil {
		t.Fatal(err)
	}
	defer checkDB.Close()
	var version, dirty int
	if err := checkDB.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if version != 4 || dirty != 0 {
		t.Fatalf("down migration schema version=%d dirty=%d, want 4/0", version, dirty)
	}
	var targetCount int
	if err := checkDB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'early_purge_plan_targets'").Scan(&targetCount); err != nil {
		t.Fatal(err)
	}
	if targetCount != 0 {
		t.Fatal("early purge target table remained after down migration")
	}
	rows, err := checkDB.Query("PRAGMA table_info(janitor_records)")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == "approval_action_run_version" {
			t.Fatal("v5 action-run approval column remained after down migration")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var connectionNotNull int
	if err := checkDB.QueryRow(`SELECT "notnull" FROM pragma_table_info('tracking_observations') WHERE name = 'connection_id'`).Scan(&connectionNotNull); err != nil {
		t.Fatal(err)
	}
	if connectionNotNull != 0 {
		t.Fatalf("v4 tracking connection notnull=%d, want 0", connectionNotNull)
	}
	assertNoForeignKeyViolations(t, checkDB)
}

type round4EarlyPurgeFixture struct {
	planID, digest, decisionID, actionRunID, entryID, janitorID string
}

func seedRound4EarlyPurge(t *testing.T, store *Store, suffix, actionState string) round4EarlyPurgeFixture {
	t.Helper()
	ctx := context.Background()
	fixture := round4EarlyPurgeFixture{
		planID: "round4-" + suffix + "-plan", digest: "round4-" + suffix + "-digest", decisionID: "round4-" + suffix + "-decision",
		actionRunID: "round4-" + suffix + "-action", entryID: "round4-" + suffix + "-entry", janitorID: "round4-" + suffix + "-janitor",
	}
	if _, err := store.Queries().CreateConfigSnapshot(ctx, &sqlc.CreateConfigSnapshotParams{ID: "round4-" + suffix + "-snapshot", Source: "api", DocumentID: "runtime-" + suffix, Revision: "1", StartupAt: legacyFixtureTime, EffectiveJson: "{}", CreatedAt: legacyFixtureTime}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateStorageRoot(ctx, &sqlc.CreateStorageRootParams{ID: "round4-" + suffix + "-root", Label: "Round4 trash " + suffix, Purpose: "trash", Path: "/round4/" + suffix, Source: "api", SourceSnapshotID: "round4-" + suffix + "-snapshot", Revision: "1", CapabilitiesJson: "[]", CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionPlan(ctx, &sqlc.CreateActionPlanParams{ID: fixture.planID, Kind: "fs.delete", State: "ready", CurrentRevision: 1, CurrentDigest: fixture.digest, CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionPlanRevision(ctx, &sqlc.CreateActionPlanRevisionParams{PlanID: fixture.planID, Revision: 1, Digest: fixture.digest, State: "ready", InputJson: `{}`, PreconditionsJson: `{}`, CapabilitiesJson: `[]`, ManifestJson: `[{"path":"movie.mkv"}]`, CreatedAt: legacyFixtureTime, ExpiresAt: "2026-09-20T00:00:00Z", ReadyAt: sql.NullString{String: legacyFixtureTime, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateTrashEntry(ctx, &sqlc.CreateTrashEntryParams{ID: fixture.entryID, RootID: "round4-" + suffix + "-root", State: "trashed", OriginalPrefix: "original/" + suffix, TrashPrefix: "trash/" + suffix, ManifestJson: `[{"path":"movie.mkv"}]`, RetentionSeconds: 3600, TrashedAt: sql.NullString{String: legacyFixtureTime, Valid: true}, ExpiresAt: "2026-09-20T00:00:00Z", ClientStateJson: `{}`, CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateEarlyPurgePlanTarget(ctx, &sqlc.CreateEarlyPurgePlanTargetParams{PlanID: fixture.planID, Revision: 1, PlanDigest: fixture.digest, IntentKind: "fs.delete", TrashEntryID: fixture.entryID, TrashEntryVersion: 1, ManifestJson: `[{"path":"movie.mkv"}]`, CreatedAt: legacyFixtureTime}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{ID: fixture.decisionID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest, Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "round4-" + suffix, IdempotencyKey: "review", CreatedAt: legacyFixtureTime}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionRun(ctx, &sqlc.CreateActionRunParams{ID: fixture.actionRunID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest, State: actionState, DesiredStateJson: `{}`, Version: 1, OutcomeJson: `{}`, CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateJanitorRecord(ctx, &sqlc.CreateJanitorRecordParams{ID: fixture.janitorID, TrashEntryID: fixture.entryID, Operation: "purge", State: "queued", OutcomeJson: `{}`, CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime}); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func seedRound4EarlyPurgeEntry(t *testing.T, store *Store, entryID, janitorID string) round4EarlyPurgeFixture {
	t.Helper()
	suffix := strings.TrimPrefix(entryID, "round4-")
	ctx := context.Background()
	if _, err := store.Queries().CreateConfigSnapshot(ctx, &sqlc.CreateConfigSnapshotParams{ID: "round4-entry-" + suffix + "-snapshot", Source: "api", DocumentID: "runtime-other-" + suffix, Revision: "1", StartupAt: legacyFixtureTime, EffectiveJson: "{}", CreatedAt: legacyFixtureTime}); err != nil {
		t.Fatal(err)
	}
	rootID := "round4-entry-" + suffix + "-root"
	if _, err := store.Queries().CreateStorageRoot(ctx, &sqlc.CreateStorageRootParams{ID: rootID, Label: "Round4 other trash", Purpose: "trash", Path: "/round4/other/" + suffix, Source: "api", SourceSnapshotID: "round4-entry-" + suffix + "-snapshot", Revision: "1", CapabilitiesJson: "[]", CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateTrashEntry(ctx, &sqlc.CreateTrashEntryParams{ID: entryID, RootID: rootID, State: "trashed", OriginalPrefix: "original/other/" + suffix, TrashPrefix: "trash/other/" + suffix, ManifestJson: `[{"path":"movie.mkv"}]`, RetentionSeconds: 3600, TrashedAt: sql.NullString{String: legacyFixtureTime, Valid: true}, ExpiresAt: "2026-09-20T00:00:00Z", ClientStateJson: `{}`, CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateJanitorRecord(ctx, &sqlc.CreateJanitorRecordParams{ID: janitorID, TrashEntryID: entryID, Operation: "purge", State: "queued", OutcomeJson: `{}`, CreatedAt: legacyFixtureTime, UpdatedAt: legacyFixtureTime}); err != nil {
		t.Fatal(err)
	}
	return round4EarlyPurgeFixture{entryID: entryID, janitorID: janitorID}
}

func assertRound4EarlyPurgePending(t *testing.T, store *Store, fixture round4EarlyPurgeFixture) {
	assertRound4EarlyPurgePendingState(t, store, fixture, "queued")
}

func assertRound4EarlyPurgePendingState(t *testing.T, store *Store, fixture round4EarlyPurgeFixture, wantActionState string) {
	t.Helper()
	var janitorState, entryState, actionState string
	var activeOperation sql.NullString
	if err := store.DB().QueryRow(`SELECT jr.state, te.state, te.active_operation
		FROM janitor_records AS jr JOIN trash_entries AS te ON te.id = jr.trash_entry_id
		WHERE jr.id = ?`, fixture.janitorID).Scan(&janitorState, &entryState, &activeOperation); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow("SELECT state FROM action_runs WHERE id = ?", fixture.actionRunID).Scan(&actionState); err != nil {
		t.Fatal(err)
	}
	if janitorState != "queued" || entryState != "trashed" || activeOperation.Valid || actionState != wantActionState {
		t.Fatalf("unsafe early purge state = janitor=%q entry=%q operation=%+v action=%q, want action=%q", janitorState, entryState, activeOperation, actionState, wantActionState)
	}
}

func TestRound4MigrationHistoryIsImmutable(t *testing.T) {
	expectedDigests := map[string]string{
		"000001_initial.up.sql":               "1b5bd13d2184f5109b5483026783466ad1f5551c377fef174500496f34aacead",
		"000003_storage_contract.up.sql":      "502c484fb39e822f047ea1ad05697b13ce30ed1d1bb8835d73183bbef7f80393",
		"000004_storage_compatibility.up.sql": "3c77b61d8f216f0ae985f9e244ff37cde5b6c6e41a2d6c73bf093f182edc6b2c",
		"000005_storage_safety.up.sql":        "dc394314e4e13eff00976028cedb0110b222eb30a5afe8bbef79a0e6156a65bf",
		"000005_storage_safety.down.sql":      "1ae04987891fd42f5b4b3976666b97a34144863c0826d3e6efa873f4ce1b3781",
		"000006_approval_safety.up.sql":       "145c1726d20277a72402222204fbc581fafc62380429d20a116126a038b618a9",
		"000006_approval_safety.down.sql":     "c172ff330989cb1eeb6d98af7923daaababe036b5d6d1d9e406da4622fccae28",
	}
	for name, expected := range expectedDigests {
		data, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if got := hex.EncodeToString(digest[:]); got != expected {
			t.Fatalf("%s changed: got %s want %s", name, got, expected)
		}
	}
}
