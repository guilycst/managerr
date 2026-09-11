package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/guilycst/mastarr/internal/storage/sqlc"
)

func TestRound8V7InFlightApprovalUpgradeRemainsReclaimable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v7-in-flight.sqlite")
	store, fixture := seedRound8V7InFlightApproval(t, path)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatalf("v7 in-flight upgrade: %v", err)
	}
	defer upgraded.Close()
	ctx := context.Background()

	// Action recovery must leave the repaired action fence in place until the
	// coupled janitor recovery owns the transition.
	if recovered, err := upgraded.Queries().RecoverExpiredActionRuns(ctx, sql.NullString{String: "2026-09-11T00:01:10Z", Valid: true}); err != nil {
		t.Fatal(err)
	} else if len(recovered) != 0 {
		t.Fatalf("v7 action recovery cleared the coupled fence: %+v", recovered)
	}
	action, err := upgraded.Queries().GetActionRun(ctx, fixture.actionRunID)
	if err != nil {
		t.Fatal(err)
	}
	if action.State != "reconciling" || action.Version != 4 || !action.ClaimedBy.Valid || action.ClaimedBy.String != "round8-v7-worker" {
		t.Fatalf("upgraded action fence = %+v, want reconciling/version 4/round8-v7-worker", action)
	}

	recovered, err := upgraded.Queries().RecoverExpiredJanitorRecords(ctx, "2026-09-11T00:01:10Z")
	if err != nil {
		t.Fatalf("coupled janitor recovery: %v", err)
	}
	if len(recovered) != 1 || recovered[0].ID != fixture.janitorID || recovered[0].Version != 5 || recovered[0].State != "reconciling" {
		t.Fatalf("upgraded janitor recovery = %+v, want one reconciling version 5", recovered)
	}

	claimed, err := upgraded.Queries().ClaimApprovedEarlyPurgeReconciliation(ctx, round8ReconciliationClaim(
		fixture, recovered[0].Version, "2026-09-11T00:01:11Z", "2026-09-11T00:02:00Z", "round8-v7-reconciler",
	))
	if err != nil {
		t.Fatalf("reclaim upgraded v7 approval: %v", err)
	}
	if claimed.Version != 6 || claimed.State != "running" {
		t.Fatalf("reclaimed upgraded v7 janitor = %+v, want running/version 6", claimed)
	}

	entry, err := upgraded.Queries().GetTrashEntry(ctx, fixture.entryID)
	if err != nil {
		t.Fatal(err)
	}
	action, err = upgraded.Queries().GetActionRun(ctx, fixture.actionRunID)
	if err != nil {
		t.Fatal(err)
	}
	if action.Version != claimed.Version || entry.Version != claimed.Version || !action.ClaimedBy.Valid || action.ClaimedBy.String != "round8-v7-reconciler" {
		t.Fatalf("reclaimed upgraded v7 generations = action=%+v janitor=%+v entry=%+v", action, claimed, entry)
	}
}

func TestRound8V7ActionRecoveryFirstUpgradeRemainsReclaimable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v7-action-first.sqlite")
	store, fixture := seedRound8V7InFlightApproval(t, path)
	// Action recovery committed before janitor recovery. Restore the valid v7
	// half-recovered state that can exist across a process crash.
	legacyExec(t, store.DB(), `UPDATE janitor_records
		SET state = 'running', claimed_by = 'round8-action-first-worker', lease_until = '2026-09-11T00:01:05Z',
			version = 2, updated_at = '2026-09-11T00:01:00Z'
		WHERE id = ?`, fixture.janitorID)
	legacyExec(t, store.DB(), `UPDATE trash_entries
		SET state = 'purging', active_operation = 'purge', operation_claimed_by = 'round8-action-first-worker',
			operation_lease_until = '2026-09-11T00:01:05Z', version = 2, updated_at = '2026-09-11T00:01:00Z'
		WHERE id = ?`, fixture.entryID)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatalf("v7 action-first upgrade: %v", err)
	}
	defer upgraded.Close()
	ctx := context.Background()
	janitor, err := upgraded.Queries().GetJanitorRecord(ctx, &sqlc.GetJanitorRecordParams{TrashEntryID: fixture.entryID, Operation: "purge"})
	if err != nil {
		t.Fatal(err)
	}
	action, err := upgraded.Queries().GetActionRun(ctx, fixture.actionRunID)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := upgraded.Queries().GetTrashEntry(ctx, fixture.entryID)
	if err != nil {
		t.Fatal(err)
	}
	if janitor.State != "reconciling" || janitor.Version != 3 || janitor.ClaimedBy.Valid || janitor.LeaseUntil.Valid ||
		action.State != "reconciling" || action.Version != 3 || action.ClaimedBy.Valid || action.LeaseUntil.Valid ||
		entry.Version != 3 || entry.OperationClaimedBy.Valid || entry.OperationLeaseUntil.Valid {
		t.Fatalf("action-first upgrade generation = janitor=%+v action=%+v entry=%+v", janitor, action, entry)
	}
	claimed, err := upgraded.Queries().ClaimApprovedEarlyPurgeReconciliation(ctx, round8ReconciliationClaim(
		fixture, 3, "2026-09-11T00:01:11Z", "2026-09-11T00:02:00Z", "round8-action-first-reconciler",
	))
	if err != nil {
		t.Fatalf("reclaim action-first upgrade: %v", err)
	}
	if claimed.Version != 4 {
		t.Fatalf("action-first reclaimed janitor = %+v, want version 4", claimed)
	}
}

func TestRound8V7JanitorRecoveryFirstUpgradeRemainsReclaimable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v7-janitor-first.sqlite")
	store, fixture := seedRound8V7InFlightApproval(t, path)
	// Janitor recovery committed before action recovery. Keep exact v7
	// approval evidence, but leave action carrying its old running lease.
	legacyExec(t, store.DB(), `UPDATE action_runs
		SET state = 'running', claimed_by = 'round8-janitor-first-worker', lease_until = '2026-09-11T00:01:05Z',
			version = 2, updated_at = '2026-09-11T00:01:00Z'
		WHERE id = ?`, fixture.actionRunID)
	legacyExec(t, store.DB(), `UPDATE janitor_records
		SET state = 'reconciling', claimed_by = NULL, lease_until = NULL,
			version = 3, updated_at = '2026-09-11T00:01:00Z'
		WHERE id = ?`, fixture.janitorID)
	legacyExec(t, store.DB(), `UPDATE trash_entries
		SET state = 'purging', active_operation = 'purge', operation_claimed_by = NULL,
			operation_lease_until = NULL, version = 3, updated_at = '2026-09-11T00:01:00Z'
		WHERE id = ?`, fixture.entryID)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := Open(path)
	if err != nil {
		t.Fatalf("v7 janitor-first upgrade: %v", err)
	}
	defer upgraded.Close()
	ctx := context.Background()
	janitor, err := upgraded.Queries().GetJanitorRecord(ctx, &sqlc.GetJanitorRecordParams{TrashEntryID: fixture.entryID, Operation: "purge"})
	if err != nil {
		t.Fatal(err)
	}
	action, err := upgraded.Queries().GetActionRun(ctx, fixture.actionRunID)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := upgraded.Queries().GetTrashEntry(ctx, fixture.entryID)
	if err != nil {
		t.Fatal(err)
	}
	if janitor.State != "reconciling" || janitor.Version != 3 || janitor.ClaimedBy.Valid || janitor.LeaseUntil.Valid ||
		action.State != "reconciling" || action.Version != 3 || action.ClaimedBy.Valid || action.LeaseUntil.Valid ||
		entry.Version != 3 || entry.OperationClaimedBy.Valid || entry.OperationLeaseUntil.Valid {
		t.Fatalf("janitor-first upgrade generation = janitor=%+v action=%+v entry=%+v", janitor, action, entry)
	}
	claimed, err := upgraded.Queries().ClaimApprovedEarlyPurgeReconciliation(ctx, round8ReconciliationClaim(
		fixture, 3, "2026-09-11T00:01:11Z", "2026-09-11T00:02:00Z", "round8-janitor-first-reconciler",
	))
	if err != nil {
		t.Fatalf("reclaim janitor-first upgrade: %v", err)
	}
	if claimed.Version != 4 {
		t.Fatalf("janitor-first reclaimed janitor = %+v, want version 4", claimed)
	}
}

func TestRound8CancellationKeepsReadOnlyApprovalReconciliationAvailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cancelled-recovery.sqlite")
	store, fixture := seedRound7ApprovedPurge(t, path, "round8-cancel")
	defer store.Close()
	ctx := context.Background()

	if recovered, err := store.Queries().RecoverExpiredActionRuns(ctx, sql.NullString{String: "2026-09-11T00:01:10Z", Valid: true}); err != nil {
		t.Fatal(err)
	} else if len(recovered) != 0 {
		t.Fatalf("split action recovery cleared the action fence: %+v", recovered)
	}
	recovered, err := store.Queries().RecoverExpiredJanitorRecords(ctx, "2026-09-11T00:01:10Z")
	if err != nil || len(recovered) != 1 {
		t.Fatalf("initial coupled recovery = %+v, err=%v", recovered, err)
	}
	claimed, err := store.Queries().ClaimApprovedEarlyPurgeReconciliation(ctx, round8ReconciliationClaim(
		fixture, recovered[0].Version, "2026-09-11T00:01:11Z", "2026-09-11T00:02:00Z", "round8-cancel-reconciler",
	))
	if err != nil {
		t.Fatalf("initial read-only claim: %v", err)
	}
	if claimed.Version != 4 {
		t.Fatalf("initial read-only claim version = %d, want 4", claimed.Version)
	}

	cancelled, err := store.Queries().RequestActionCancellation(ctx, &sqlc.RequestActionCancellationParams{
		RequestedAt: sql.NullString{String: "2026-09-11T00:01:12Z", Valid: true},
		UpdatedAt:   "2026-09-11T00:01:12Z",
		ID:          fixture.actionRunID,
	})
	if err != nil {
		t.Fatalf("request cancellation: %v", err)
	}
	if !cancelled.CancellationRequestedAt.Valid || cancelled.Version != 5 {
		t.Fatalf("cancelled action = %+v, want cancellation/version 5", cancelled)
	}
	assertRound8Generation(t, store, fixture, 5, "round8-cancel-reconciler")

	// The expired action lease cannot be cleared independently. Janitor
	// recovery fences and advances the cancelled action in one boundary.
	if recovered, err := store.Queries().RecoverExpiredActionRuns(ctx, sql.NullString{String: "2026-09-11T00:02:10Z", Valid: true}); err != nil {
		t.Fatal(err)
	} else if len(recovered) != 0 {
		t.Fatalf("cancelled action fence was split from janitor: %+v", recovered)
	}
	recovered, err = store.Queries().RecoverExpiredJanitorRecords(ctx, "2026-09-11T00:02:10Z")
	if err != nil || len(recovered) != 1 || recovered[0].Version != 6 {
		t.Fatalf("cancelled coupled recovery = %+v, err=%v", recovered, err)
	}

	if _, err := store.Queries().ClaimActionRun(ctx, &sqlc.ClaimActionRunParams{
		WorkerID: sql.NullString{String: "round8-cancel-mutation-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:03:00Z", Valid: true},
		Now: "2026-09-11T00:02:11Z", ID: fixture.actionRunID, Version: recovered[0].Version,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cancelled approval generic mutation claim = %v, want sql.ErrNoRows", err)
	}

	readOnly, err := store.Queries().ClaimApprovedEarlyPurgeReconciliation(ctx, round8ReconciliationClaim(
		fixture, recovered[0].Version, "2026-09-11T00:02:11Z", "2026-09-11T00:03:00Z", "round8-cancel-reconciler-2",
	))
	if err != nil {
		t.Fatalf("cancelled read-only reclaim: %v", err)
	}
	if readOnly.Version != 7 {
		t.Fatalf("cancelled read-only reclaim version = %d, want 7", readOnly.Version)
	}
	assertRound8Generation(t, store, fixture, 7, "round8-cancel-reconciler-2")
}

func TestRound8DeadlineKeepsReadOnlyApprovalReconciliationAvailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deadline-recovery.sqlite")
	store, fixture := seedRound7ApprovedPurge(t, path, "round8-deadline")
	defer store.Close()
	ctx := context.Background()

	if recovered, err := store.Queries().RecoverExpiredActionRuns(ctx, sql.NullString{String: "2026-09-11T00:01:10Z", Valid: true}); err != nil {
		t.Fatal(err)
	} else if len(recovered) != 0 {
		t.Fatalf("deadline action recovery cleared the coupled fence: %+v", recovered)
	}
	recovered, err := store.Queries().RecoverExpiredJanitorRecords(ctx, "2026-09-11T00:01:10Z")
	if err != nil || len(recovered) != 1 {
		t.Fatalf("deadline coupled recovery = %+v, err=%v", recovered, err)
	}
	// The action was accepted before preview expiry. Its own deadline now
	// forbids mutation but does not forbid observing the accepted effect.
	legacyExec(t, store.DB(), `UPDATE action_runs SET deadline_at = '2026-09-11T00:01:00Z' WHERE id = ?`, fixture.actionRunID)
	readOnly, err := store.Queries().ClaimApprovedEarlyPurgeReconciliation(ctx, round8ReconciliationClaim(
		fixture, recovered[0].Version, "2026-09-11T00:02:00Z", "2026-09-11T00:03:00Z", "round8-deadline-reconciler",
	))
	if err != nil {
		t.Fatalf("deadline read-only claim: %v", err)
	}
	if readOnly.Version != recovered[0].Version+1 {
		t.Fatalf("deadline read-only claim version = %d, want %d", readOnly.Version, recovered[0].Version+1)
	}
	if _, err := store.Queries().ClaimActionRun(ctx, &sqlc.ClaimActionRunParams{
		WorkerID: sql.NullString{String: "round8-deadline-mutation-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:03:00Z", Valid: true},
		Now: "2026-09-11T00:02:01Z", ID: fixture.actionRunID, Version: readOnly.Version,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deadline approval generic mutation claim = %v, want sql.ErrNoRows", err)
	}
}

func TestRound8MutationClaimRespectsCancellationAndDeadlineDuringReconciliation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name           string
		cancellationAt sql.NullString
		deadlineAt     sql.NullString
	}{
		{name: "cancelled", cancellationAt: sql.NullString{String: "2026-09-11T00:00:10Z", Valid: true}},
		{name: "deadline", deadlineAt: sql.NullString{String: "2026-09-11T00:00:10Z", Valid: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planID := "round8-mutation-" + tc.name + "-plan"
			if _, err := store.Queries().CreateActionPlan(ctx, &sqlc.CreateActionPlanParams{
				ID: planID, Kind: "fs.copy", State: "ready", CurrentRevision: 1, CurrentDigest: planID + "-digest", CreatedAt: "2026-09-11T00:00:00Z", UpdatedAt: "2026-09-11T00:00:00Z",
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Queries().CreateActionPlanRevision(ctx, &sqlc.CreateActionPlanRevisionParams{
				PlanID: planID, Revision: 1, Digest: planID + "-digest", State: "ready", InputJson: `{}`, PreconditionsJson: `{}`, CapabilitiesJson: `[]`, ManifestJson: `[]`, CreatedAt: "2026-09-11T00:00:00Z", ExpiresAt: "2026-09-11T01:00:00Z",
			}); err != nil {
				t.Fatal(err)
			}
			run, err := store.Queries().CreateActionRun(ctx, &sqlc.CreateActionRunParams{
				ID: "round8-mutation-" + tc.name, PlanID: planID, PlanRevision: 1, PlanDigest: planID + "-digest", State: "reconciling", DesiredStateJson: `{}`, CancellationRequestedAt: tc.cancellationAt, DeadlineAt: tc.deadlineAt, Version: 1, OutcomeJson: `{}`, CreatedAt: "2026-09-11T00:00:01Z", UpdatedAt: "2026-09-11T00:00:01Z",
			})
			if err != nil {
				t.Fatal(err)
			}
			due, err := store.Queries().ListDueActionRuns(ctx, &sqlc.ListDueActionRunsParams{Now: sql.NullString{String: "2026-09-11T00:01:00Z", Valid: true}, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range due {
				if candidate.ID == run.ID {
					t.Fatalf("%s reconciliation listed as mutation work: %+v", tc.name, candidate)
				}
			}
			if _, err := store.Queries().ClaimActionRun(ctx, &sqlc.ClaimActionRunParams{
				WorkerID: sql.NullString{String: "round8-mutation-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true}, Now: "2026-09-11T00:01:00Z", ID: run.ID, Version: run.Version,
			}); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("%s reconciliation mutation claim = %v, want sql.ErrNoRows", tc.name, err)
			}
		})
	}
}

func seedRound8V7InFlightApproval(t *testing.T, path string) (*Store, round5PurgeFixture) {
	t.Helper()
	store, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, 7)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fixture := seedRound5PurgeBase(t, store, "round8-v7", "2026-09-20T00:00:00Z", round5CreatedAt)
	createRound5Target(t, store, fixture, fixture.entryID, round5CreatedAt)
	if _, err := store.Queries().CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: fixture.decisionID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest,
		Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "round8-v7", IdempotencyKey: "approve", CreatedAt: round5CreatedAt,
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionRun(ctx, &sqlc.CreateActionRunParams{
		ID: fixture.actionRunID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest,
		State: "queued", DesiredStateJson: `{}`, DeadlineAt: sql.NullString{String: "2026-09-20T00:00:00Z", Valid: true}, Version: 1, OutcomeJson: `{}`,
		CreatedAt: round5CreatedAt, UpdatedAt: round5CreatedAt,
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateJanitorRecord(ctx, &sqlc.CreateJanitorRecordParams{
		ID: fixture.janitorID, TrashEntryID: fixture.entryID, Operation: "purge", State: "queued", OutcomeJson: `{}`,
		CreatedAt: round5CreatedAt, UpdatedAt: round5CreatedAt,
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.Queries().ClaimApprovedEarlyPurge(ctx, &sqlc.ClaimApprovedEarlyPurgeParams{
		WorkerID: sql.NullString{String: "round8-v7-initial", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:01:00Z", Valid: true},
		ApprovalPlanID: sql.NullString{String: fixture.planID, Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: fixture.digest, Valid: true},
		ApprovalDecisionID: sql.NullString{String: fixture.decisionID, Valid: true}, ApprovalActionRunID: sql.NullString{String: fixture.actionRunID, Valid: true},
		ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true},
		Now: round5CreatedAt, ID: fixture.janitorID, Version: 1, TrashEntryID: fixture.entryID,
	}); err != nil {
		_ = store.Close()
		t.Fatalf("v7 initial approved claim: %v", err)
	}
	// Model the exact v7 post-reconciliation crash: v7 did not fence the
	// action during its janitor reconciliation claim.
	legacyExec(t, store.DB(), `UPDATE action_runs
		SET state = 'reconciling', claimed_by = NULL, lease_until = NULL,
			version = 3, next_attempt_at = '2026-09-11T00:01:00Z', updated_at = '2026-09-11T00:01:00Z'
		WHERE id = ?`, fixture.actionRunID)
	legacyExec(t, store.DB(), `UPDATE janitor_records
		SET state = 'running', claimed_by = 'round8-v7-worker', lease_until = '2026-09-11T00:01:05Z',
			version = 4, next_attempt_at = '2026-09-11T00:01:00Z', updated_at = '2026-09-11T00:01:00Z'
		WHERE id = ?`, fixture.janitorID)
	legacyExec(t, store.DB(), `UPDATE trash_entries
		SET state = 'purging', active_operation = 'purge', operation_claimed_by = 'round8-v7-worker',
			operation_lease_until = '2026-09-11T00:01:05Z', version = 4, updated_at = '2026-09-11T00:01:00Z'
		WHERE id = ?`, fixture.entryID)
	return store, fixture
}

func round8ReconciliationClaim(fixture round5PurgeFixture, version int64, now, leaseUntil, worker string) *sqlc.ClaimApprovedEarlyPurgeReconciliationParams {
	return &sqlc.ClaimApprovedEarlyPurgeReconciliationParams{
		WorkerID: sql.NullString{String: worker, Valid: true}, LeaseUntil: sql.NullString{String: leaseUntil, Valid: true},
		Now: now, ID: fixture.janitorID, Version: version, TrashEntryID: fixture.entryID,
		ApprovalPlanID: sql.NullString{String: fixture.planID, Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: fixture.digest, Valid: true},
		ApprovalDecisionID: sql.NullString{String: fixture.decisionID, Valid: true}, ApprovalActionRunID: sql.NullString{String: fixture.actionRunID, Valid: true},
		ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true},
	}
}

func assertRound8Generation(t *testing.T, store *Store, fixture round5PurgeFixture, wantVersion int64, wantWorker string) {
	t.Helper()
	ctx := context.Background()
	action, err := store.Queries().GetActionRun(ctx, fixture.actionRunID)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := store.Queries().GetTrashEntry(ctx, fixture.entryID)
	if err != nil {
		t.Fatal(err)
	}
	janitor, err := store.Queries().GetJanitorRecord(ctx, &sqlc.GetJanitorRecordParams{TrashEntryID: fixture.entryID, Operation: "purge"})
	if err != nil {
		t.Fatal(err)
	}
	if action.Version != wantVersion || janitor.Version != wantVersion || entry.Version != wantVersion || !action.ClaimedBy.Valid || action.ClaimedBy.String != wantWorker || !janitor.ClaimedBy.Valid || janitor.ClaimedBy.String != wantWorker || !entry.OperationClaimedBy.Valid || entry.OperationClaimedBy.String != wantWorker {
		t.Fatalf("generation = action=%+v janitor=%+v entry=%+v, want version=%d worker=%q", action, janitor, entry, wantVersion, wantWorker)
	}
}
