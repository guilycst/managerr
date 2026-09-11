package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/guilycst/mastarr/internal/storage/sqlc"
)

func TestRound9TerminalApprovedPurgeStaysFencedUntilExactFinalization(t *testing.T) {
	for _, tc := range []struct {
		name         string
		janitorState string
		actionState  string
		entryState   string
	}{
		{name: "succeeded", janitorState: "succeeded", actionState: "succeeded", entryState: "purged"},
		{name: "failed", janitorState: "failed", actionState: "failed", entryState: "failed"},
		{name: "held", janitorState: "held", actionState: "needs_review", entryState: "held"},
		{name: "cancelled", janitorState: "cancelled", actionState: "cancelled", entryState: "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "terminal-approved-purge.sqlite")
			store, fixture := seedRound7ApprovedPurge(t, path, "round9-"+tc.name)
			defer store.Close()
			ctx := context.Background()

			recovered, err := store.Queries().RecoverRunningJanitorRecords(ctx, "2026-09-11T00:01:10Z")
			if err != nil || len(recovered) != 1 {
				t.Fatalf("startup recovery = %+v, err=%v", recovered, err)
			}
			claimed, err := store.Queries().ClaimApprovedEarlyPurgeReconciliation(ctx, round8ReconciliationClaim(
				fixture, recovered[0].Version, "2026-09-11T00:01:11Z", "2026-09-11T00:02:00Z", "round9-terminal-worker",
			))
			if err != nil {
				t.Fatalf("read-only claim = %v", err)
			}

			if _, err := store.Queries().UpdateJanitorRecord(ctx, &sqlc.UpdateJanitorRecordParams{
				State: tc.janitorState, NextAttemptAt: sql.NullString{}, ClaimedBy: sql.NullString{}, LeaseUntil: sql.NullString{}, OutcomeJson: `{"outcome":"terminal"}`,
				UpdatedAt: "2026-09-11T00:01:12Z", ID: claimed.ID, Version: claimed.Version,
			}); err != nil {
				t.Fatalf("terminal janitor update: %v", err)
			}

			// Janitor/trash completion does not authorize a generic action
			// recovery or mutation claim while action finalization is pending.
			action, err := store.Queries().GetActionRun(ctx, fixture.actionRunID)
			if err != nil {
				t.Fatal(err)
			}
			if action.State != "reconciling" || !action.ClaimedBy.Valid {
				t.Fatalf("terminal janitor released action = %+v", action)
			}
			if recovered, err := store.Queries().RecoverExpiredActionRuns(ctx, sql.NullString{String: "2026-09-11T00:03:00Z", Valid: true}); err != nil {
				t.Fatal(err)
			} else if len(recovered) != 0 {
				t.Fatalf("terminal janitor allowed generic recovery = %+v", recovered)
			}
			due, err := store.Queries().ListDueActionRuns(ctx, &sqlc.ListDueActionRunsParams{Now: sql.NullString{String: "2026-09-11T00:03:00Z", Valid: true}, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range due {
				if candidate.ID == fixture.actionRunID {
					t.Fatalf("terminal janitor listed action as due mutation: %+v", candidate)
				}
			}
			if _, err := store.Queries().ClaimActionRun(ctx, &sqlc.ClaimActionRunParams{
				WorkerID: sql.NullString{String: "round9-generic-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:04:00Z", Valid: true}, Now: "2026-09-11T00:03:00Z", ID: fixture.actionRunID, Version: action.Version,
			}); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("terminal janitor generic claim = %v, want sql.ErrNoRows", err)
			}

			finalized, err := store.Queries().FinalizeApprovedPurgeAction(ctx, &sqlc.FinalizeApprovedPurgeActionParams{
				JanitorID: claimed.ID, Now: "2026-09-11T00:03:01Z", ActionRunID: fixture.actionRunID, ActionRunVersion: action.Version,
			})
			if err != nil {
				t.Fatalf("exact terminal finalization: %v", err)
			}
			if finalized.State != tc.actionState || finalized.Version != claimed.Version+1 || finalized.ClaimedBy.Valid || finalized.LeaseUntil.Valid {
				t.Fatalf("finalized action = %+v, want %s/version %d/unleased", finalized, tc.actionState, claimed.Version+1)
			}
			entry, err := store.Queries().GetTrashEntry(ctx, fixture.entryID)
			if err != nil {
				t.Fatal(err)
			}
			if entry.State != tc.entryState || entry.Version != finalized.Version {
				t.Fatalf("finalized entry = %+v, want %s/version %d", entry, tc.entryState, finalized.Version)
			}
			if _, err := store.Queries().FinalizeApprovedPurgeAction(ctx, &sqlc.FinalizeApprovedPurgeActionParams{
				JanitorID: claimed.ID, Now: "2026-09-11T00:03:02Z", ActionRunID: fixture.actionRunID, ActionRunVersion: finalized.Version,
			}); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("repeated terminal finalization = %v, want sql.ErrNoRows", err)
			}
		})
	}
}

func TestRound9V9DownRestoresRound8ReconciliationActionFence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v9-down-v8-reconciliation.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runRound5MigrationSteps(path, -1); err != nil {
		t.Fatalf("v9 to v8 down migration: %v", err)
	}

	v8, fixture := seedRound9ApprovedPurgeWithMigrations(t, path, "down-v8", 8)
	ctx := context.Background()
	// Model v8's supported action-first recovery ordering. The janitor trigger
	// restored by 000009.down must fence the action during the next claim.
	legacyExec(t, v8.DB(), `UPDATE action_runs
		SET state = 'reconciling', claimed_by = NULL, lease_until = NULL,
			version = 3, next_attempt_at = '2026-09-11T00:01:00Z', updated_at = '2026-09-11T00:01:00Z'
		WHERE id = ?`, fixture.actionRunID)
	recovered, err := v8.Queries().RecoverRunningJanitorRecords(ctx, "2026-09-11T00:01:10Z")
	if err != nil || len(recovered) != 1 {
		_ = v8.Close()
		t.Fatalf("v8 janitor recovery = %+v, err=%v", recovered, err)
	}
	claimed, err := v8.Queries().ClaimApprovedEarlyPurgeReconciliation(ctx, round8ReconciliationClaim(
		fixture, recovered[0].Version, "2026-09-11T00:01:11Z", "2026-09-11T00:02:00Z", "round9-v8-reconciler",
	))
	if err != nil {
		_ = v8.Close()
		t.Fatalf("v8 exact reconciliation claim: %v", err)
	}
	assertRound8Generation(t, v8, fixture, claimed.Version, "round9-v8-reconciler")
	if _, err := v8.Queries().ClaimActionRun(ctx, &sqlc.ClaimActionRunParams{
		WorkerID: sql.NullString{String: "round9-v8-generic-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true}, Now: "2026-09-11T00:01:12Z", ID: fixture.actionRunID, Version: claimed.Version,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("downgraded v8 generic action claim = %v, want sql.ErrNoRows", err)
	}
	if err := v8.Close(); err != nil {
		t.Fatal(err)
	}

	// A populated v8 state must re-upgrade through v9 and retain the coupled
	// recovery fence rather than being treated as a fresh unbound action.
	reup, err := Open(path)
	if err != nil {
		t.Fatalf("v8 to v9 re-upgrade: %v", err)
	}
	defer reup.Close()
	recovered, err = reup.Queries().RecoverRunningJanitorRecords(ctx, "2026-09-11T00:03:10Z")
	if err != nil || len(recovered) != 1 {
		t.Fatalf("v9 janitor recovery after re-upgrade = %+v, err=%v", recovered, err)
	}
	claimed, err = reup.Queries().ClaimApprovedEarlyPurgeReconciliation(ctx, round8ReconciliationClaim(
		fixture, recovered[0].Version, "2026-09-11T00:03:11Z", "2026-09-11T00:04:00Z", "round9-reup-reconciler",
	))
	if err != nil {
		t.Fatalf("v9 exact reconciliation after re-upgrade: %v", err)
	}
	assertRound8Generation(t, reup, fixture, claimed.Version, "round9-reup-reconciler")
}

func seedRound9ApprovedPurgeWithMigrations(t *testing.T, path, suffix string, maxVersion int) (*Store, round5PurgeFixture) {
	t.Helper()
	store, err := OpenWithOptions(Options{Path: path, MigrationsFS: migrationsThroughVersion(t, maxVersion)})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fixture := seedRound5PurgeBase(t, store, "round9-"+suffix, "2026-09-11T00:10:00Z", round5CreatedAt)
	createRound5Target(t, store, fixture, fixture.entryID, round5CreatedAt)
	if _, err := store.Queries().CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: fixture.decisionID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest,
		Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "round9-" + suffix, IdempotencyKey: "approve", CreatedAt: round5CreatedAt,
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionRun(ctx, &sqlc.CreateActionRunParams{
		ID: fixture.actionRunID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest,
		State: "queued", DesiredStateJson: `{}`, DeadlineAt: sql.NullString{String: "2026-09-11T00:10:00Z", Valid: true}, Version: 1, OutcomeJson: `{}`,
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
		WorkerID: sql.NullString{String: "round9-v8-initial", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:01:00Z", Valid: true},
		ApprovalPlanID: sql.NullString{String: fixture.planID, Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: fixture.digest, Valid: true},
		ApprovalDecisionID: sql.NullString{String: fixture.decisionID, Valid: true}, ApprovalActionRunID: sql.NullString{String: fixture.actionRunID, Valid: true},
		ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true},
		Now: round5CreatedAt, ID: fixture.janitorID, Version: 1, TrashEntryID: fixture.entryID,
	}); err != nil {
		_ = store.Close()
		t.Fatalf("initial approved claim under v%d: %v", maxVersion, err)
	}
	return store, fixture
}
