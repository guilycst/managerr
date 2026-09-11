package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	migratedb "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/guilycst/mastarr/internal/storage/sqlc"
	"github.com/guilycst/mastarr/migrations"
)

const (
	round5PreviewExpiry = "2026-09-11T00:01:00Z"
	round5CreatedAt     = "2026-09-11T00:00:30Z"
)

type round5PurgeFixture struct {
	planID, digest, decisionID, actionRunID, entryID, janitorID string
	rootID                                                      string
}

func TestRound5ApprovalTargetMustPrecedeImmutableApproval(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	missing := seedRound5PurgeBase(t, store, "missing-target", "2026-09-20T00:00:00Z", round5CreatedAt)
	if _, err := store.Queries().CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: "round5-missing-decision", PlanID: missing.planID, PlanRevision: 1, PlanDigest: missing.digest,
		Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "round5-missing", IdempotencyKey: "approve", CreatedAt: round5CreatedAt,
	}); err == nil {
		t.Fatal("fs.delete approval without an exact pre-existing target was accepted")
	}
	if _, err := store.Queries().CreateActionPlan(ctx, &sqlc.CreateActionPlanParams{
		ID: "round5-live-delete-plan", Kind: "fs.delete", State: "ready", CurrentRevision: 1, CurrentDigest: "round5-live-delete-digest", CreatedAt: round5CreatedAt, UpdatedAt: round5CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionPlanRevision(ctx, &sqlc.CreateActionPlanRevisionParams{
		PlanID: "round5-live-delete-plan", Revision: 1, Digest: "round5-live-delete-digest", State: "ready", InputJson: `{"scope":"live"}`, PreconditionsJson: `{}`, CapabilitiesJson: `[]`, ManifestJson: `[{"path":"live.mkv"}]`, CreatedAt: round5CreatedAt, ExpiresAt: "2026-09-20T00:00:00Z", ReadyAt: sql.NullString{String: round5CreatedAt, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: "round5-live-delete-decision", PlanID: "round5-live-delete-plan", PlanRevision: 1, PlanDigest: "round5-live-delete-digest",
		Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "round5-live-delete", IdempotencyKey: "approve", CreatedAt: round5CreatedAt,
	}); err != nil {
		t.Fatalf("standalone live fs.delete approval: %v", err)
	}

	bound := seedRound5PurgeBase(t, store, "bound-target", "2026-09-20T00:00:00Z", round5CreatedAt)
	createRound5Target(t, store, bound, bound.entryID, round5CreatedAt)
	if _, err := store.Queries().CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: bound.decisionID, PlanID: bound.planID, PlanRevision: 1, PlanDigest: bound.digest,
		Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "round5-bound", IdempotencyKey: "approve", CreatedAt: round5CreatedAt,
	}); err != nil {
		t.Fatalf("bound approval: %v", err)
	}

	// A second trash entry has the same manifest. Once the immutable decision
	// exists, a caller cannot rebind the plan to that other entry.
	if _, err := store.Queries().CreateTrashEntry(ctx, &sqlc.CreateTrashEntryParams{
		ID: "round5-bound-other-entry", RootID: bound.rootID, State: "trashed",
		OriginalPrefix: "original/other", TrashPrefix: "trash/other", ManifestJson: `[{"path":"movie.mkv"}]`,
		RetentionSeconds: 3600, TrashedAt: sql.NullString{String: round5CreatedAt, Valid: true},
		ExpiresAt: "2026-09-20T00:00:00Z", ClientStateJson: `{}`, CreatedAt: round5CreatedAt, UpdatedAt: round5CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateEarlyPurgePlanTarget(ctx, &sqlc.CreateEarlyPurgePlanTargetParams{
		PlanID: bound.planID, Revision: 1, PlanDigest: bound.digest, IntentKind: "fs.delete",
		TrashEntryID: "round5-bound-other-entry", TrashEntryVersion: 1, ManifestJson: `[{"path":"movie.mkv"}]`, CreatedAt: round5CreatedAt,
	}); err == nil {
		t.Fatal("post-approval target rebinding was accepted")
	}
	var targetEntry string
	if err := store.DB().QueryRow("SELECT trash_entry_id FROM early_purge_plan_targets WHERE plan_id = ?", bound.planID).Scan(&targetEntry); err != nil {
		t.Fatal(err)
	}
	if targetEntry != bound.entryID {
		t.Fatalf("target was rebound to %q, want %q", targetEntry, bound.entryID)
	}
}

func TestRound5LegacyApprovalIsHeldAndCannotGenericReclaimAcrossDownUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-approval.sqlite")
	store, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, 4)})
	if err != nil {
		t.Fatal(err)
	}
	seedRound5LegacyUnsafeApproval(t, store.DB())
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatalf("legacy approval upgrade: %v", err)
	}
	assertRound5HeldApproval(t, upgraded, true)
	// Model a stale worker queueing the held record through a state-only write;
	// the v6 generic claim guard must refuse to reinterpret its approval.
	legacyExec(t, upgraded.DB(), `UPDATE janitor_records SET state = 'queued' WHERE id = 'round5-legacy-janitor'`)
	if _, err := upgraded.Queries().ClaimJanitorRecord(context.Background(), &sqlc.ClaimJanitorRecordParams{
		WorkerID: sql.NullString{String: "generic-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:05:00Z", Valid: true},
		Now: "2026-09-11T00:04:00Z", ID: "round5-legacy-janitor", Version: 2,
	}); err == nil {
		t.Fatal("generic janitor claim reinterpreted an approval-bearing legacy row")
	}
	if err := upgraded.Close(); err != nil {
		t.Fatal(err)
	}

	if err := runRound5MigrationSteps(path, -2); err != nil {
		t.Fatalf("down to v4: %v", err)
	}
	check := openRound5DB(t, path)
	var state, actionState string
	if err := check.QueryRow("SELECT state FROM janitor_records WHERE id = 'round5-legacy-janitor'").Scan(&state); err != nil {
		t.Fatal(err)
	}
	if err := check.QueryRow("SELECT state FROM action_runs WHERE id = 'round5-legacy-action'").Scan(&actionState); err != nil {
		t.Fatal(err)
	}
	if state != "held" || actionState != "needs_review" {
		t.Fatalf("v4 down safety state = janitor=%q action=%q, want held/needs_review", state, actionState)
	}
	var targetTable int
	if err := check.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'early_purge_plan_targets'").Scan(&targetTable); err != nil {
		t.Fatal(err)
	}
	if targetTable != 0 {
		t.Fatal("v4 downgrade retained early purge targets without a binding")
	}
	if err := check.Close(); err != nil {
		t.Fatal(err)
	}

	if err := runRound5MigrationSteps(path, 2); err != nil {
		t.Fatalf("re-up to v6: %v", err)
	}
	reup, err := Open(path)
	if err != nil {
		t.Fatalf("reopen v6: %v", err)
	}
	defer reup.Close()
	assertRound5HeldApproval(t, reup, false)
	var version sql.NullInt64
	if err := reup.DB().QueryRow("SELECT approval_action_run_version FROM janitor_records WHERE id = 'round5-legacy-janitor'").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version.Valid {
		t.Fatalf("downgrade/re-up invented approval action version %d", version.Int64)
	}
}

func TestRound5DirectV5DownNeutralizesApprovedPurge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "direct-v5-down.sqlite")
	store, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, 5)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fixture := seedRound5PurgeBase(t, store, "direct-v5-down", "2026-09-20T00:00:00Z", round5CreatedAt)
	createRound5Target(t, store, fixture, fixture.entryID, round5CreatedAt)
	if _, err := store.Queries().CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: fixture.decisionID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest,
		Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "round5-direct-v5-down", IdempotencyKey: "approve", CreatedAt: round5CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionRun(ctx, &sqlc.CreateActionRunParams{
		ID: fixture.actionRunID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest,
		State: "queued", DesiredStateJson: `{}`, DeadlineAt: sql.NullString{String: "2026-09-11T00:03:00Z", Valid: true}, Version: 1, OutcomeJson: `{}`,
		CreatedAt: round5CreatedAt, UpdatedAt: round5CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateJanitorRecord(ctx, &sqlc.CreateJanitorRecordParams{
		ID: fixture.janitorID, TrashEntryID: fixture.entryID, Operation: "purge", State: "queued", OutcomeJson: `{}`, CreatedAt: round5CreatedAt, UpdatedAt: round5CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().ClaimApprovedEarlyPurge(ctx, &sqlc.ClaimApprovedEarlyPurgeParams{
		WorkerID: sql.NullString{String: "round5-direct-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:03:00Z", Valid: true},
		ApprovalPlanID: sql.NullString{String: fixture.planID, Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: fixture.digest, Valid: true},
		ApprovalDecisionID: sql.NullString{String: fixture.decisionID, Valid: true}, ApprovalActionRunID: sql.NullString{String: fixture.actionRunID, Valid: true},
		ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true},
		Now: "2026-09-11T00:00:45Z", ID: fixture.janitorID, Version: 1, TrashEntryID: fixture.entryID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runRound5MigrationSteps(path, -1); err != nil {
		t.Fatalf("direct v5 down: %v", err)
	}
	check := openRound5DB(t, path)
	assertRound5HeldApprovalV4(t, check, fixture)
	if err := check.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runRound5MigrationSteps(path, 1); err != nil {
		t.Fatalf("direct v4 to v5 re-up: %v", err)
	}
	reup, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, 5)})
	if err != nil {
		t.Fatal(err)
	}
	defer reup.Close()
	assertRound5HeldApprovalV5(t, reup, fixture)
}

func assertRound5HeldApprovalV4(t *testing.T, db *sql.DB, fixture round5PurgeFixture) {
	t.Helper()
	var janitorState, entryState, actionState string
	if err := db.QueryRow("SELECT state FROM janitor_records WHERE id = ?", fixture.janitorID).Scan(&janitorState); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT state FROM trash_entries WHERE id = ?", fixture.entryID).Scan(&entryState); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT state FROM action_runs WHERE id = ?", fixture.actionRunID).Scan(&actionState); err != nil {
		t.Fatal(err)
	}
	if janitorState != "held" || entryState != "held" || actionState != "needs_review" {
		t.Fatalf("direct v5 down safety state = janitor=%q entry=%q action=%q, want held/held/needs_review", janitorState, entryState, actionState)
	}
}

func assertRound5HeldApprovalV5(t *testing.T, store *Store, fixture round5PurgeFixture) {
	t.Helper()
	var janitorState, entryState, actionState, approvalPlan string
	if err := store.DB().QueryRow(`SELECT jr.state, te.state, jr.approval_plan_id
		FROM janitor_records AS jr JOIN trash_entries AS te ON te.id = jr.trash_entry_id
		WHERE jr.id = ?`, fixture.janitorID).Scan(&janitorState, &entryState, &approvalPlan); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow("SELECT state FROM action_runs WHERE id = ?", fixture.actionRunID).Scan(&actionState); err != nil {
		t.Fatal(err)
	}
	if janitorState != "held" || entryState != "held" || approvalPlan != fixture.planID || actionState != "needs_review" {
		t.Fatalf("direct v5 re-up safety state = janitor=%q entry=%q plan=%q action=%q", janitorState, entryState, approvalPlan, actionState)
	}
	var version sql.NullInt64
	if err := store.DB().QueryRow("SELECT approval_action_run_version FROM janitor_records WHERE id = ?", fixture.janitorID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version.Valid {
		t.Fatalf("direct v5 down/re-up invented approval action version %d", version.Int64)
	}
}

func assertRound5HeldApproval(t *testing.T, store *Store, expectActionRun bool) {
	t.Helper()
	var janitorState, entryState, actionState, approvalPlan string
	if err := store.DB().QueryRow(`SELECT jr.state, te.state, jr.approval_plan_id
		FROM janitor_records AS jr JOIN trash_entries AS te ON te.id = jr.trash_entry_id
		WHERE jr.id = 'round5-legacy-janitor'`).Scan(&janitorState, &entryState, &approvalPlan); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow("SELECT state FROM action_runs WHERE id = 'round5-legacy-action'").Scan(&actionState); err != nil {
		t.Fatal(err)
	}
	if janitorState != "held" || entryState != "held" || approvalPlan != "round5-legacy-plan" {
		t.Fatalf("held approval state = janitor=%q entry=%q plan=%q", janitorState, entryState, approvalPlan)
	}
	if expectActionRun && actionState != "needs_review" {
		t.Fatalf("legacy action state = %q, want needs_review", actionState)
	}
}

func TestRound5QuarantineReupgradeRejectsChangedEvidenceAndAllowsExactDuplicate(t *testing.T) {
	for _, tc := range []struct {
		name, evidence string
		wantFailure    bool
	}{
		{name: "changed", evidence: `{"version":"new"}`, wantFailure: true},
		{name: "exact", evidence: `{"version":"original"}`, wantFailure: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name+"-quarantine.sqlite")
			store, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, 4)})
			if err != nil {
				t.Fatal(err)
			}
			legacyExec(t, store.DB(), `INSERT INTO tracking_observations
				(id, external_record_id, media_identity_id, connection_id, root_id, dimension, status, evidence_json, observed_at)
				VALUES ('round5-reused-observation', NULL, NULL, NULL, NULL, 'registration', 'unknown', '{"version":"original"}', ?)`, legacyFixtureTime)
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			upgraded, err := Open(path)
			if err != nil {
				t.Fatalf("quarantine seed upgrade: %v", err)
			}
			if err := upgraded.Close(); err != nil {
				t.Fatal(err)
			}
			if err := runRound5MigrationSteps(path, -2); err != nil {
				t.Fatalf("down to nullable v4: %v", err)
			}

			legacyDB := openRound5DB(t, path)
			legacyExec(t, legacyDB, `DROP TRIGGER tracking_observations_quarantine_id_guard_insert`)
			legacyExec(t, legacyDB, `DROP TRIGGER tracking_observations_quarantine_id_guard_update`)
			legacyExec(t, legacyDB, `INSERT INTO tracking_observations
				(id, external_record_id, media_identity_id, connection_id, root_id, dimension, status, evidence_json, observed_at)
				VALUES ('round5-reused-observation', NULL, NULL, NULL, NULL, 'registration', 'unknown', ?, ?)`, tc.evidence, legacyFixtureTime)
			if err := legacyDB.Close(); err != nil {
				t.Fatal(err)
			}

			migrationErr := runRound5MigrationSteps(path, 2)
			if tc.wantFailure {
				if migrationErr == nil {
					t.Fatal("changed quarantine evidence was silently discarded")
				}
				assertDirtyMigration(t, path)
				failed := openRound5DB(t, path)
				defer failed.Close()
				var quarantineEvidence, activeEvidence string
				if err := failed.QueryRow("SELECT original_evidence_json FROM tracking_observation_quarantine WHERE original_observation_id = 'round5-reused-observation'").Scan(&quarantineEvidence); err != nil {
					t.Fatal(err)
				}
				if err := failed.QueryRow("SELECT evidence_json FROM tracking_observations WHERE id = 'round5-reused-observation'").Scan(&activeEvidence); err != nil {
					t.Fatal(err)
				}
				if quarantineEvidence != `{"version":"original"}` || activeEvidence != tc.evidence {
					t.Fatalf("changed evidence preservation = quarantine=%q active=%q", quarantineEvidence, activeEvidence)
				}
				return
			}
			if migrationErr != nil {
				t.Fatalf("exact duplicate re-up: %v", migrationErr)
			}
			reup, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reup.Close()
			var quarantineCount, activeCount int
			if err := reup.DB().QueryRow("SELECT count(*) FROM tracking_observation_quarantine WHERE original_observation_id = 'round5-reused-observation'").Scan(&quarantineCount); err != nil {
				t.Fatal(err)
			}
			if err := reup.DB().QueryRow("SELECT count(*) FROM tracking_observations WHERE id = 'round5-reused-observation'").Scan(&activeCount); err != nil {
				t.Fatal(err)
			}
			if quarantineCount != 1 || activeCount != 0 {
				t.Fatalf("exact duplicate re-up rows = quarantine=%d active=%d, want 1/0", quarantineCount, activeCount)
			}
		})
	}
}

func TestRound5AcceptedApprovalDispatchesAfterPreviewExpiry(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	fixture := seedRound5PurgeBase(t, store, "post-expiry", round5PreviewExpiry, round5CreatedAt)
	createRound5Target(t, store, fixture, fixture.entryID, round5CreatedAt)
	if _, err := store.Queries().CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: fixture.decisionID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest,
		Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "round5-post-expiry", IdempotencyKey: "approve", CreatedAt: round5CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionRun(ctx, &sqlc.CreateActionRunParams{
		ID: fixture.actionRunID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest,
		State: "queued", DesiredStateJson: `{}`, DeadlineAt: sql.NullString{String: "2026-09-11T00:03:00Z", Valid: true}, Version: 1, OutcomeJson: `{}`,
		CreatedAt: round5CreatedAt, UpdatedAt: round5CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateJanitorRecord(ctx, &sqlc.CreateJanitorRecordParams{
		ID: fixture.janitorID, TrashEntryID: fixture.entryID, Operation: "purge", State: "queued", OutcomeJson: `{}`, CreatedAt: round5CreatedAt, UpdatedAt: round5CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Queries().ClaimApprovedEarlyPurge(ctx, &sqlc.ClaimApprovedEarlyPurgeParams{
		WorkerID: sql.NullString{String: "round5-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:03:00Z", Valid: true},
		ApprovalPlanID: sql.NullString{String: fixture.planID, Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: fixture.digest, Valid: true},
		ApprovalDecisionID: sql.NullString{String: fixture.decisionID, Valid: true}, ApprovalActionRunID: sql.NullString{String: fixture.actionRunID, Valid: true},
		ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true},
		Now: "2026-09-11T00:02:00Z", ID: fixture.janitorID, Version: 1, TrashEntryID: fixture.entryID,
	})
	if err != nil {
		t.Fatalf("accepted post-expiry claim: %v", err)
	}
	if claimed.State != "running" {
		t.Fatalf("post-expiry janitor state = %q, want running", claimed.State)
	}
	var actionState, entryState string
	if err := store.DB().QueryRow("SELECT state FROM action_runs WHERE id = ?", fixture.actionRunID).Scan(&actionState); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow("SELECT state FROM trash_entries WHERE id = ?", fixture.entryID).Scan(&entryState); err != nil {
		t.Fatal(err)
	}
	if actionState != "running" || entryState != "purging" {
		t.Fatalf("post-expiry materialization = action=%q entry=%q, want running/purging", actionState, entryState)
	}

	lateDecision := seedRound5PurgeBase(t, store, "late-decision", round5PreviewExpiry, round5CreatedAt)
	createRound5Target(t, store, lateDecision, lateDecision.entryID, round5CreatedAt)
	if _, err := store.Queries().CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: lateDecision.decisionID, PlanID: lateDecision.planID, PlanRevision: 1, PlanDigest: lateDecision.digest,
		Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "round5-late-decision", IdempotencyKey: "approve", CreatedAt: "2026-09-11T00:01:30Z",
	}); err == nil {
		t.Fatal("approval recorded after preview expiry")
	}

	lateTarget := seedRound5PurgeBase(t, store, "late-target", round5PreviewExpiry, round5CreatedAt)
	if _, err := store.Queries().CreateEarlyPurgePlanTarget(ctx, &sqlc.CreateEarlyPurgePlanTargetParams{
		PlanID: lateTarget.planID, Revision: 1, PlanDigest: lateTarget.digest, IntentKind: "fs.delete", TrashEntryID: lateTarget.entryID,
		TrashEntryVersion: 1, ManifestJson: `[{"path":"movie.mkv"}]`, CreatedAt: "2026-09-11T00:01:30Z",
	}); err == nil {
		t.Fatal("target recorded after preview expiry")
	}

	lateAction := seedRound5PurgeBase(t, store, "late-action", round5PreviewExpiry, round5CreatedAt)
	createRound5Target(t, store, lateAction, lateAction.entryID, round5CreatedAt)
	if _, err := store.Queries().CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: lateAction.decisionID, PlanID: lateAction.planID, PlanRevision: 1, PlanDigest: lateAction.digest,
		Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "round5-late-action", IdempotencyKey: "approve", CreatedAt: round5CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionRun(ctx, &sqlc.CreateActionRunParams{
		ID: lateAction.actionRunID, PlanID: lateAction.planID, PlanRevision: 1, PlanDigest: lateAction.digest,
		State: "queued", DesiredStateJson: `{}`, Version: 1, OutcomeJson: `{}`, CreatedAt: "2026-09-11T00:01:30Z", UpdatedAt: "2026-09-11T00:01:30Z",
	}); err == nil {
		t.Fatal("action run recorded after preview expiry")
	}
}

func seedRound5PurgeBase(t *testing.T, store *Store, suffix, expiry, createdAt string) round5PurgeFixture {
	t.Helper()
	ctx := context.Background()
	fixture := round5PurgeFixture{
		planID: "round5-" + suffix + "-plan", digest: "round5-" + suffix + "-digest", decisionID: "round5-" + suffix + "-decision",
		actionRunID: "round5-" + suffix + "-action", entryID: "round5-" + suffix + "-entry", janitorID: "round5-" + suffix + "-janitor", rootID: "round5-" + suffix + "-root",
	}
	if _, err := store.Queries().CreateConfigSnapshot(ctx, &sqlc.CreateConfigSnapshotParams{ID: "round5-" + suffix + "-snapshot", Source: "api", DocumentID: "round5-" + suffix, Revision: "1", StartupAt: createdAt, EffectiveJson: "{}", CreatedAt: createdAt}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateStorageRoot(ctx, &sqlc.CreateStorageRootParams{ID: fixture.rootID, Label: "Round5 " + suffix, Purpose: "trash", Path: "/round5/" + suffix, Source: "api", SourceSnapshotID: "round5-" + suffix + "-snapshot", Revision: "1", CapabilitiesJson: "[]", CreatedAt: createdAt, UpdatedAt: createdAt}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionPlan(ctx, &sqlc.CreateActionPlanParams{ID: fixture.planID, Kind: "fs.delete", State: "ready", CurrentRevision: 1, CurrentDigest: fixture.digest, CreatedAt: createdAt, UpdatedAt: createdAt}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionPlanRevision(ctx, &sqlc.CreateActionPlanRevisionParams{PlanID: fixture.planID, Revision: 1, Digest: fixture.digest, State: "ready", InputJson: `{}`, PreconditionsJson: `{}`, CapabilitiesJson: `[]`, ManifestJson: `[{"path":"movie.mkv"}]`, CreatedAt: createdAt, ExpiresAt: expiry, ReadyAt: sql.NullString{String: createdAt, Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateTrashEntry(ctx, &sqlc.CreateTrashEntryParams{ID: fixture.entryID, RootID: fixture.rootID, State: "trashed", OriginalPrefix: "original/" + suffix, TrashPrefix: "trash/" + suffix, ManifestJson: `[{"path":"movie.mkv"}]`, RetentionSeconds: 3600, TrashedAt: sql.NullString{String: createdAt, Valid: true}, ExpiresAt: "2026-09-20T00:00:00Z", ClientStateJson: `{}`, CreatedAt: createdAt, UpdatedAt: createdAt}); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func createRound5Target(t *testing.T, store *Store, fixture round5PurgeFixture, entryID, createdAt string) {
	t.Helper()
	if _, err := store.Queries().CreateEarlyPurgePlanTarget(context.Background(), &sqlc.CreateEarlyPurgePlanTargetParams{
		PlanID: fixture.planID, Revision: 1, PlanDigest: fixture.digest, IntentKind: "fs.delete", TrashEntryID: entryID,
		TrashEntryVersion: 1, ManifestJson: `[{"path":"movie.mkv"}]`, CreatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
}

func seedRound5LegacyUnsafeApproval(t *testing.T, db *sql.DB) {
	t.Helper()
	legacyExec(t, db, `INSERT INTO config_snapshots (id, source, document_id, revision, startup_at, effective_json, created_at)
		VALUES ('round5-legacy-snapshot', 'api', 'round5-legacy', '1', ?, '{}', ?)`, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, db, `INSERT INTO storage_roots (id, label, purpose, path, source, source_snapshot_id, revision, created_at, updated_at)
		VALUES ('round5-legacy-root', 'Round5 legacy trash', 'trash', '/round5/legacy', 'api', 'round5-legacy-snapshot', '1', ?, ?)`, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, db, `INSERT INTO action_plans (id, kind, state, current_revision, current_digest, created_at, updated_at)
		VALUES ('round5-legacy-plan', 'fs.copy', 'ready', 1, 'round5-legacy-digest', ?, ?)`, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, db, `INSERT INTO action_plan_revisions (plan_id, revision, digest, state, input_json, preconditions_json, capabilities_json, manifest_json, created_at, expires_at, ready_at)
		VALUES ('round5-legacy-plan', 1, 'round5-legacy-digest', 'ready', '{}', '{}', '[]', '[]', ?, '2026-09-20T00:00:00Z', ?)`, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, db, `INSERT INTO review_decisions (id, plan_id, plan_revision, plan_digest, decision, actor, idempotency_scope, idempotency_key, created_at)
		VALUES ('round5-legacy-decision', 'round5-legacy-plan', 1, 'round5-legacy-digest', 'approve', 'unauthenticated', 'round5-legacy', 'approve', ?)`, legacyFixtureTime)
	legacyExec(t, db, `INSERT INTO action_runs (id, plan_id, plan_revision, plan_digest, state, desired_state_json, version, outcome_json, created_at, updated_at)
		VALUES ('round5-legacy-action', 'round5-legacy-plan', 1, 'round5-legacy-digest', 'queued', '{}', 1, '{}', ?, ?)`, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, db, `INSERT INTO trash_entries (id, root_id, state, original_prefix, trash_prefix, manifest_json, retention_seconds, trashed_at, expires_at, client_state_json, created_at, updated_at)
		VALUES ('round5-legacy-entry', 'round5-legacy-root', 'trashed', 'original/legacy', 'trash/legacy', '[{"path":"legacy.mkv"}]', 3600, ?, '2026-09-20T00:00:00Z', '{}', ?, ?)`, legacyFixtureTime, legacyFixtureTime, legacyFixtureTime)
	legacyExec(t, db, `INSERT INTO janitor_records (id, trash_entry_id, operation, state, outcome_json, created_at, updated_at)
		VALUES ('round5-legacy-janitor', 'round5-legacy-entry', 'purge', 'queued', '{}', ?, ?)`, legacyFixtureTime, legacyFixtureTime)
	// This fixture intentionally represents a populated older database that
	// attached approval fields outside the current atomic claim primitive.
	legacyExec(t, db, `DROP TRIGGER janitor_records_approval_shape_update`)
	legacyExec(t, db, `UPDATE janitor_records
		SET state = 'running', claimed_by = 'old-worker', lease_until = '2026-09-11T00:02:00Z',
			approval_plan_id = 'round5-legacy-plan', approval_plan_revision = 1,
			approval_plan_digest = 'round5-legacy-digest', approval_decision_id = 'round5-legacy-decision',
			approval_action_run_id = 'round5-legacy-action', approved_entry_version = 1
		WHERE id = 'round5-legacy-janitor'`)
}

func runRound5MigrationSteps(path string, steps int) error {
	db, err := sql.Open("sqlite", sqliteDSN(path, defaultBusyTimeout))
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := configureSQLite(db, defaultBusyTimeout); err != nil {
		_ = db.Close()
		return err
	}
	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		_ = db.Close()
		return err
	}
	driver, err := migratedb.WithInstance(db, &migratedb.Config{NoTxWrap: true})
	if err != nil {
		_ = db.Close()
		return err
	}
	runner, err := migrate.NewWithInstance("iofs", source, "sqlite", driver)
	if err != nil {
		_ = db.Close()
		return err
	}
	stepErr := runner.Steps(steps)
	sourceErr, databaseErr := runner.Close()
	return errors.Join(stepErr, sourceErr, databaseErr)
}

func openRound5DB(t *testing.T, path string) *sql.DB {
	t.Helper()
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
	return db
}
