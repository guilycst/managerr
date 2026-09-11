package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/guilycst/mastarr/internal/storage/sqlc"
)

func TestRound6V5PostApprovalTargetIsHeldBeforeClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-approval-target.sqlite")
	store, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, 5)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fixture := seedRound5PurgeBase(t, store, "round6-post-approval", "2026-09-20T00:01:00Z", round5CreatedAt)
	decisionAt := "2026-09-11T00:00:30Z"
	targetAt := "2026-09-11T00:00:40Z"
	if _, err := store.Queries().CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: fixture.decisionID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest,
		Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "round6-post-approval", IdempotencyKey: "approve", CreatedAt: decisionAt,
	}); err != nil {
		t.Fatal(err)
	}
	// v5 allowed a target to be inserted after the decision. This is the
	// populated upgrade shape that v6 must neutralize before it can be claimed.
	if _, err := store.Queries().CreateEarlyPurgePlanTarget(ctx, &sqlc.CreateEarlyPurgePlanTargetParams{
		PlanID: fixture.planID, Revision: 1, PlanDigest: fixture.digest, IntentKind: "fs.delete",
		TrashEntryID: fixture.entryID, TrashEntryVersion: 1, ManifestJson: `[{"path":"movie.mkv"}]`, CreatedAt: targetAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionRun(ctx, &sqlc.CreateActionRunParams{
		ID: fixture.actionRunID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest,
		State: "queued", DesiredStateJson: `{}`, DeadlineAt: sql.NullString{String: "2026-09-11T00:03:00Z", Valid: true}, Version: 1, OutcomeJson: `{}`,
		CreatedAt: "2026-09-11T00:00:45Z", UpdatedAt: "2026-09-11T00:00:45Z",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateJanitorRecord(ctx, &sqlc.CreateJanitorRecordParams{
		ID: fixture.janitorID, TrashEntryID: fixture.entryID, Operation: "purge", State: "queued", OutcomeJson: `{}`,
		CreatedAt: "2026-09-11T00:00:45Z", UpdatedAt: "2026-09-11T00:00:45Z",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatalf("v5 post-approval upgrade: %v", err)
	}
	defer upgraded.Close()

	var targetAtAfterUpgrade string
	if err := upgraded.DB().QueryRow("SELECT created_at FROM early_purge_plan_targets WHERE plan_id = ?", fixture.planID).Scan(&targetAtAfterUpgrade); err != nil {
		t.Fatal(err)
	}
	if targetAtAfterUpgrade != targetAt {
		t.Fatalf("immutable target timestamp = %q, want %q", targetAtAfterUpgrade, targetAt)
	}
	var janitorState, entryState, actionState, reason string
	if err := upgraded.DB().QueryRow(`SELECT jr.state, te.state, ar.state, json_extract(jr.outcome_json, '$.reason')
		FROM janitor_records AS jr
		JOIN trash_entries AS te ON te.id = jr.trash_entry_id
		JOIN action_runs AS ar ON ar.id = ?
		WHERE jr.id = ?`, fixture.actionRunID, fixture.janitorID).Scan(&janitorState, &entryState, &actionState, &reason); err != nil {
		t.Fatal(err)
	}
	if janitorState != "held" || entryState != "held" || actionState != "needs_review" || reason != "approval_target_chronology_unprovable" {
		t.Fatalf("post-approval upgrade states = janitor=%q entry=%q action=%q reason=%q, want held/held/needs_review/chronology", janitorState, entryState, actionState, reason)
	}

	_, err = upgraded.Queries().ClaimApprovedEarlyPurge(ctx, &sqlc.ClaimApprovedEarlyPurgeParams{
		WorkerID: sql.NullString{String: "round6-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true},
		ApprovalPlanID: sql.NullString{String: fixture.planID, Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: fixture.digest, Valid: true},
		ApprovalDecisionID: sql.NullString{String: fixture.decisionID, Valid: true}, ApprovalActionRunID: sql.NullString{String: fixture.actionRunID, Valid: true},
		ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true},
		Now: "2026-09-11T00:01:00Z", ID: fixture.janitorID, Version: 2, TrashEntryID: fixture.entryID,
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("post-approval exact claim error = %v, want sql.ErrNoRows", err)
	}
	if _, err := upgraded.Queries().ClaimJanitorRecord(ctx, &sqlc.ClaimJanitorRecordParams{
		WorkerID: sql.NullString{String: "round6-generic", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true},
		Now: "2026-09-11T00:01:00Z", ID: fixture.janitorID, Version: 2,
	}); err == nil {
		t.Fatal("generic janitor claim revived a held post-approval target")
	}
	var finalEntryState string
	if err := upgraded.DB().QueryRow("SELECT state FROM trash_entries WHERE id = ?", fixture.entryID).Scan(&finalEntryState); err != nil {
		t.Fatal(err)
	}
	if finalEntryState != "held" {
		t.Fatalf("post-approval claim changed trash state to %q", finalEntryState)
	}
}

func TestRound6ApprovedPurgeReacquiresAfterRestartAndResolves(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approved-recovery.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fixture := seedRound5PurgeBase(t, store, "round6-recovery", "2026-09-11T00:02:00Z", round5CreatedAt)
	createRound5Target(t, store, fixture, fixture.entryID, "2026-09-11T00:00:20Z")
	if _, err := store.Queries().CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: fixture.decisionID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest,
		Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "round6-recovery", IdempotencyKey: "approve", CreatedAt: round5CreatedAt,
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
		ID: fixture.janitorID, TrashEntryID: fixture.entryID, Operation: "purge", State: "queued", OutcomeJson: `{}`,
		CreatedAt: round5CreatedAt, UpdatedAt: round5CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}
	initial, err := store.Queries().ClaimApprovedEarlyPurge(ctx, &sqlc.ClaimApprovedEarlyPurgeParams{
		WorkerID: sql.NullString{String: "round6-initial", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:01:00Z", Valid: true},
		ApprovalPlanID: sql.NullString{String: fixture.planID, Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: fixture.digest, Valid: true},
		ApprovalDecisionID: sql.NullString{String: fixture.decisionID, Valid: true}, ApprovalActionRunID: sql.NullString{String: fixture.actionRunID, Valid: true},
		ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true},
		Now: "2026-09-11T00:00:45Z", ID: fixture.janitorID, Version: 1, TrashEntryID: fixture.entryID,
	})
	if err != nil {
		t.Fatalf("initial approved claim: %v", err)
	}
	if initial.State != "running" || initial.Version != 2 {
		t.Fatalf("initial approved claim = state=%q version=%d, want running/2", initial.State, initial.Version)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen approved recovery: %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.Queries().RecoverRunningActionRuns(ctx, sql.NullString{String: "2026-09-11T00:01:10Z", Valid: true}); err != nil {
		t.Fatal(err)
	}
	recovered, err := reopened.Queries().RecoverRunningJanitorRecords(ctx, "2026-09-11T00:01:10Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].ID != fixture.janitorID || recovered[0].State != "reconciling" || recovered[0].Version != 3 {
		t.Fatalf("startup janitor recovery = %+v, want one reconciling version 3", recovered)
	}
	var actionState, actionClaimedBy, entryState, entryClaimedBy string
	if err := reopened.DB().QueryRow(`SELECT state, COALESCE(claimed_by, ''),
		(SELECT state FROM trash_entries WHERE id = ?),
		(SELECT COALESCE(operation_claimed_by, '') FROM trash_entries WHERE id = ?)
		FROM action_runs WHERE id = ?`, fixture.entryID, fixture.entryID, fixture.actionRunID).Scan(&actionState, &actionClaimedBy, &entryState, &entryClaimedBy); err != nil {
		t.Fatal(err)
	}
	if actionState != "reconciling" || actionClaimedBy != "" || entryState != "purging" || entryClaimedBy != "" {
		t.Fatalf("startup recovered state = action=%q/%q entry=%q/%q", actionState, actionClaimedBy, entryState, entryClaimedBy)
	}

	reclaimed, err := reopened.Queries().ClaimApprovedEarlyPurgeReconciliation(ctx, &sqlc.ClaimApprovedEarlyPurgeReconciliationParams{
		WorkerID: sql.NullString{String: "round6-reconciler", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true},
		Now: "2026-09-11T00:01:11Z", ID: fixture.janitorID, Version: recovered[0].Version, TrashEntryID: fixture.entryID,
		ApprovalPlanID: sql.NullString{String: fixture.planID, Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: fixture.digest, Valid: true},
		ApprovalDecisionID: sql.NullString{String: fixture.decisionID, Valid: true}, ApprovalActionRunID: sql.NullString{String: fixture.actionRunID, Valid: true},
		ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true},
	})
	if err != nil {
		t.Fatalf("approved reconciliation claim: %v", err)
	}
	if reclaimed.State != "running" || reclaimed.Version != 4 || !reclaimed.ClaimedBy.Valid || reclaimed.ClaimedBy.String != "round6-reconciler" {
		t.Fatalf("reacquired janitor = %+v, want running version 4 with worker lease", reclaimed)
	}
	var actionVersion int64
	var actionLeaseUntil string
	if err := reopened.DB().QueryRow("SELECT version, claimed_by, lease_until FROM action_runs WHERE id = ? AND state = 'reconciling' AND claimed_by IS NOT NULL AND lease_until IS NOT NULL", fixture.actionRunID).Scan(&actionVersion, &actionClaimedBy, &actionLeaseUntil); err != nil {
		t.Fatalf("action was not fenced for read-only reconciling: %v", err)
	}
	if actionVersion != 4 || actionClaimedBy != "round6-reconciler" || actionLeaseUntil != "2026-09-11T00:02:00Z" {
		t.Fatalf("reconciled action fence = version=%d worker=%q lease=%q, want 4/round6-reconciler/2026-09-11T00:02:00Z", actionVersion, actionClaimedBy, actionLeaseUntil)
	}
	var entryVersion int64
	if err := reopened.DB().QueryRow("SELECT version FROM trash_entries WHERE id = ? AND state = 'purging' AND active_operation = 'purge' AND operation_claimed_by = 'round6-reconciler'", fixture.entryID).Scan(&entryVersion); err != nil {
		t.Fatal(err)
	}
	if entryVersion != 4 {
		t.Fatalf("reconciled trash version = %d, want 4", entryVersion)
	}

	finished, err := reopened.Queries().UpdateJanitorRecord(ctx, &sqlc.UpdateJanitorRecordParams{
		State: "succeeded", NextAttemptAt: sql.NullString{}, ClaimedBy: sql.NullString{}, LeaseUntil: sql.NullString{}, OutcomeJson: `{"outcome":"already_satisfied"}`,
		UpdatedAt: "2026-09-11T00:01:12Z", ID: reclaimed.ID, Version: reclaimed.Version,
	})
	if err != nil {
		t.Fatalf("finish reconciled janitor: %v", err)
	}
	if finished.State != "succeeded" {
		t.Fatalf("finished janitor state = %q", finished.State)
	}
	if _, err := reopened.Queries().UpdateActionRunOutcome(ctx, &sqlc.UpdateActionRunOutcomeParams{
		State: "succeeded", NextAttemptAt: sql.NullString{}, ClaimedBy: sql.NullString{}, LeaseUntil: sql.NullString{}, OutcomeJson: `{"outcome":"already_satisfied"}`, UnresolvedCount: 0,
		UpdatedAt: "2026-09-11T00:01:12Z", ID: fixture.actionRunID, Version: actionVersion,
	}); err != nil {
		t.Fatalf("finish reconciled action: %v", err)
	}
	var finalJanitor, finalEntry, finalAction string
	if err := reopened.DB().QueryRow(`SELECT jr.state, te.state, ar.state
		FROM janitor_records AS jr JOIN trash_entries AS te ON te.id = jr.trash_entry_id
		JOIN action_runs AS ar ON ar.id = ? WHERE jr.id = ?`, fixture.actionRunID, fixture.janitorID).Scan(&finalJanitor, &finalEntry, &finalAction); err != nil {
		t.Fatal(err)
	}
	if finalJanitor != "succeeded" || finalEntry != "purged" || finalAction != "succeeded" {
		t.Fatalf("resolved approved purge = janitor=%q entry=%q action=%q, want succeeded/purged/succeeded", finalJanitor, finalEntry, finalAction)
	}
}

func TestRound6IncompleteApprovalCannotUseRecoveryClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "incomplete-approval.sqlite")
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
		t.Fatal(err)
	}
	defer upgraded.Close()
	legacyExec(t, upgraded.DB(), `UPDATE janitor_records SET state = 'reconciling' WHERE id = 'round5-legacy-janitor'`)
	var version int64
	if err := upgraded.DB().QueryRow("SELECT version FROM janitor_records WHERE id = 'round5-legacy-janitor'").Scan(&version); err != nil {
		t.Fatal(err)
	}
	_, err = upgraded.Queries().ClaimApprovedEarlyPurgeReconciliation(context.Background(), &sqlc.ClaimApprovedEarlyPurgeReconciliationParams{
		WorkerID: sql.NullString{String: "round6-incomplete", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:04:00Z", Valid: true},
		Now: "2026-09-11T00:03:00Z", ID: "round5-legacy-janitor", Version: version, TrashEntryID: "round5-legacy-entry",
		ApprovalPlanID: sql.NullString{String: "round5-legacy-plan", Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: "round5-legacy-digest", Valid: true},
		ApprovalDecisionID: sql.NullString{String: "round5-legacy-decision", Valid: true}, ApprovalActionRunID: sql.NullString{String: "round5-legacy-action", Valid: true},
		ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true},
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("incomplete approval recovery claim error = %v, want sql.ErrNoRows", err)
	}
	if _, err := upgraded.Queries().ClaimJanitorRecord(context.Background(), &sqlc.ClaimJanitorRecordParams{
		WorkerID: sql.NullString{String: "round6-generic", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:04:00Z", Valid: true},
		Now: "2026-09-11T00:03:00Z", ID: "round5-legacy-janitor", Version: version,
	}); err == nil {
		t.Fatal("generic claim accepted incomplete approval")
	}
}
