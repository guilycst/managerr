package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/guilycst/mastarr/internal/storage/sqlc"
)

func TestRound7ApprovedPurgeSurvivesRepeatedReconciliationCrashes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repeated-reconciliation.sqlite")
	store, fixture := seedRound7ApprovedPurge(t, path, "repeated")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	for cycle, now := range []string{"2026-09-11T00:01:10Z", "2026-09-11T00:01:30Z"} {
		reopened, err := Open(path)
		if err != nil {
			t.Fatalf("reopen cycle %d: %v", cycle+1, err)
		}

		if _, err := reopened.Queries().RecoverRunningActionRuns(ctx, sql.NullString{String: now, Valid: true}); err != nil {
			_ = reopened.Close()
			t.Fatalf("recover action cycle %d: %v", cycle+1, err)
		}
		recovered, err := reopened.Queries().RecoverRunningJanitorRecords(ctx, now)
		if err != nil {
			_ = reopened.Close()
			t.Fatalf("recover janitor cycle %d: %v", cycle+1, err)
		}
		if len(recovered) != 1 || recovered[0].ID != fixture.janitorID || recovered[0].State != "reconciling" {
			_ = reopened.Close()
			t.Fatalf("recovered cycle %d = %+v, want one reconciling janitor", cycle+1, recovered)
		}

		entry, err := reopened.Queries().GetTrashEntry(ctx, fixture.entryID)
		if err != nil {
			_ = reopened.Close()
			t.Fatalf("read entry cycle %d: %v", cycle+1, err)
		}
		if entry.State != "purging" || entry.Version != recovered[0].Version || entry.OperationClaimedBy.Valid || entry.OperationLeaseUntil.Valid {
			_ = reopened.Close()
			t.Fatalf("recovered generation cycle %d = entry=%+v janitor=%+v", cycle+1, entry, recovered[0])
		}

		worker := "round7-reconciler-" + string(rune('1'+cycle))
		leaseUntil := "2026-09-11T00:01:20Z"
		if cycle == 1 {
			leaseUntil = "2026-09-11T00:01:40Z"
		}
		claimed, err := reopened.Queries().ClaimApprovedEarlyPurgeReconciliation(ctx, &sqlc.ClaimApprovedEarlyPurgeReconciliationParams{
			WorkerID: sql.NullString{String: worker, Valid: true}, LeaseUntil: sql.NullString{String: leaseUntil, Valid: true},
			Now: now, ID: fixture.janitorID, Version: recovered[0].Version, TrashEntryID: fixture.entryID,
			ApprovalPlanID: sql.NullString{String: fixture.planID, Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: fixture.digest, Valid: true},
			ApprovalDecisionID: sql.NullString{String: fixture.decisionID, Valid: true}, ApprovalActionRunID: sql.NullString{String: fixture.actionRunID, Valid: true},
			ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true},
		})
		if err != nil {
			_ = reopened.Close()
			t.Fatalf("reclaim cycle %d: %v", cycle+1, err)
		}
		if claimed.State != "running" || claimed.Version != recovered[0].Version+1 || !claimed.ClaimedBy.Valid || claimed.ClaimedBy.String != worker {
			_ = reopened.Close()
			t.Fatalf("reclaimed cycle %d = %+v", cycle+1, claimed)
		}

		action, err := reopened.Queries().GetActionRun(ctx, fixture.actionRunID)
		if err != nil {
			_ = reopened.Close()
			t.Fatalf("read action cycle %d: %v", cycle+1, err)
		}
		entry, err = reopened.Queries().GetTrashEntry(ctx, fixture.entryID)
		if err != nil {
			_ = reopened.Close()
			t.Fatalf("read claimed entry cycle %d: %v", cycle+1, err)
		}
		if action.State != "reconciling" || !action.ClaimedBy.Valid || action.ClaimedBy.String != worker || action.Version != claimed.Version || entry.Version != claimed.Version || !entry.OperationClaimedBy.Valid || entry.OperationClaimedBy.String != worker {
			_ = reopened.Close()
			t.Fatalf("claimed generation cycle %d = action=%+v janitor=%+v entry=%+v", cycle+1, action, claimed, entry)
		}

		if cycle == 0 {
			if err := reopened.Close(); err != nil {
				t.Fatal(err)
			}
			continue
		}

		if _, err := reopened.Queries().UpdateJanitorRecord(ctx, &sqlc.UpdateJanitorRecordParams{
			State: "succeeded", NextAttemptAt: sql.NullString{}, ClaimedBy: sql.NullString{}, LeaseUntil: sql.NullString{}, OutcomeJson: `{"outcome":"already_satisfied"}`,
			UpdatedAt: "2026-09-11T00:01:31Z", ID: claimed.ID, Version: claimed.Version,
		}); err != nil {
			_ = reopened.Close()
			t.Fatalf("finish janitor after repeated recovery: %v", err)
		}
		if _, err := reopened.Queries().UpdateActionRunOutcome(ctx, &sqlc.UpdateActionRunOutcomeParams{
			State: "succeeded", NextAttemptAt: sql.NullString{}, ClaimedBy: sql.NullString{}, LeaseUntil: sql.NullString{}, OutcomeJson: `{"outcome":"already_satisfied"}`, UnresolvedCount: 0,
			UpdatedAt: "2026-09-11T00:01:31Z", ID: fixture.actionRunID, Version: action.Version,
		}); err != nil {
			_ = reopened.Close()
			t.Fatalf("finish action after repeated recovery: %v", err)
		}
		finalAction, err := reopened.Queries().GetActionRun(ctx, fixture.actionRunID)
		if err != nil {
			_ = reopened.Close()
			t.Fatal(err)
		}
		finalEntry, err := reopened.Queries().GetTrashEntry(ctx, fixture.entryID)
		if err != nil {
			_ = reopened.Close()
			t.Fatal(err)
		}
		finalJanitor, err := reopened.Queries().GetJanitorRecord(ctx, &sqlc.GetJanitorRecordParams{TrashEntryID: fixture.entryID, Operation: "purge"})
		if err != nil {
			_ = reopened.Close()
			t.Fatal(err)
		}
		if finalAction.State != "succeeded" || finalJanitor.State != "succeeded" || finalEntry.State != "purged" {
			_ = reopened.Close()
			t.Fatalf("resolved repeated recovery = action=%q janitor=%q entry=%q", finalAction.State, finalJanitor.State, finalEntry.State)
		}
		if err := reopened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRound7ReconciliationFencesCompetingActionWorker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reconciliation-fence.sqlite")
	store, fixture := seedRound7ApprovedPurge(t, path, "fence")
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	ctx := context.Background()
	if _, err := reopened.Queries().RecoverRunningActionRuns(ctx, sql.NullString{String: "2026-09-11T00:01:10Z", Valid: true}); err != nil {
		t.Fatal(err)
	}
	recovered, err := reopened.Queries().RecoverRunningJanitorRecords(ctx, "2026-09-11T00:01:10Z")
	if err != nil || len(recovered) != 1 {
		t.Fatalf("startup recovery = %+v, err=%v", recovered, err)
	}
	claimed, err := reopened.Queries().ClaimApprovedEarlyPurgeReconciliation(ctx, &sqlc.ClaimApprovedEarlyPurgeReconciliationParams{
		WorkerID: sql.NullString{String: "round7-janitor-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:01:30Z", Valid: true},
		Now: "2026-09-11T00:01:11Z", ID: fixture.janitorID, Version: recovered[0].Version, TrashEntryID: fixture.entryID,
		ApprovalPlanID: sql.NullString{String: fixture.planID, Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: fixture.digest, Valid: true},
		ApprovalDecisionID: sql.NullString{String: fixture.decisionID, Valid: true}, ApprovalActionRunID: sql.NullString{String: fixture.actionRunID, Valid: true},
		ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	competitorDB, err := sql.Open("sqlite", sqliteDSN(path, defaultBusyTimeout))
	if err != nil {
		t.Fatal(err)
	}
	competitorDB.SetMaxOpenConns(1)
	competitorDB.SetMaxIdleConns(1)
	defer competitorDB.Close()
	if err := configureSQLite(competitorDB, defaultBusyTimeout); err != nil {
		t.Fatal(err)
	}
	_, err = sqlc.New(competitorDB).ClaimActionRun(ctx, &sqlc.ClaimActionRunParams{
		WorkerID: sql.NullString{String: "round7-action-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:01:30Z", Valid: true},
		Now: "2026-09-11T00:01:12Z", ID: fixture.actionRunID, Version: claimed.Version,
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("competing action claim error = %v, want sql.ErrNoRows", err)
	}
	action, err := reopened.Queries().GetActionRun(ctx, fixture.actionRunID)
	if err != nil {
		t.Fatal(err)
	}
	if action.State != "reconciling" || !action.ClaimedBy.Valid || action.ClaimedBy.String != "round7-janitor-worker" || action.Version != claimed.Version {
		t.Fatalf("competing action claim changed fence = %+v", action)
	}
}

func TestRound7DowngradeHoldsFencedRecoveryBeforeRestoringTrigger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reconciliation-downgrade.sqlite")
	store, fixture := seedRound7ApprovedPurge(t, path, "downgrade")
	ctx := context.Background()
	if _, err := store.Queries().RecoverRunningActionRuns(ctx, sql.NullString{String: "2026-09-11T00:01:10Z", Valid: true}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	recovered, err := store.Queries().RecoverRunningJanitorRecords(ctx, "2026-09-11T00:01:10Z")
	if err != nil || len(recovered) != 1 {
		_ = store.Close()
		t.Fatalf("startup recovery = %+v, err=%v", recovered, err)
	}
	_, err = store.Queries().ClaimApprovedEarlyPurgeReconciliation(ctx, &sqlc.ClaimApprovedEarlyPurgeReconciliationParams{
		WorkerID: sql.NullString{String: "round7-downgrade-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true},
		Now: "2026-09-11T00:01:11Z", ID: fixture.janitorID, Version: recovered[0].Version, TrashEntryID: fixture.entryID,
		ApprovalPlanID: sql.NullString{String: fixture.planID, Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: fixture.digest, Valid: true},
		ApprovalDecisionID: sql.NullString{String: fixture.decisionID, Valid: true}, ApprovalActionRunID: sql.NullString{String: fixture.actionRunID, Valid: true},
		ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true},
	})
	if err != nil {
		t.Fatalf("reconciliation claim: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	if err := runRound5MigrationSteps(path, -2); err != nil {
		t.Fatalf("v8 to v7 downgrade: %v", err)
	}
	check := openRound5DB(t, path)
	var janitorState, entryState, actionState string
	if err := check.QueryRow(`SELECT jr.state, te.state, ar.state
		FROM janitor_records AS jr
		JOIN trash_entries AS te ON te.id = jr.trash_entry_id
		JOIN action_runs AS ar ON ar.id = ?
		WHERE jr.id = ?`, fixture.actionRunID, fixture.janitorID).Scan(&janitorState, &entryState, &actionState); err != nil {
		_ = check.Close()
		t.Fatal(err)
	}
	if janitorState != "held" || entryState != "held" || actionState != "needs_review" {
		_ = check.Close()
		t.Fatalf("downgraded fenced recovery = janitor=%q entry=%q action=%q, want held/held/needs_review", janitorState, entryState, actionState)
	}
	if err := check.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runRound5MigrationSteps(path, 2); err != nil {
		t.Fatalf("v7 to v8 re-upgrade: %v", err)
	}
	reup, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reup.Close()
	if reup.DB() == nil {
		t.Fatal("reopened downgraded storage without a database")
	}
	var schemaVersion int
	if err := reup.DB().QueryRow("SELECT version FROM schema_migrations").Scan(&schemaVersion); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != 9 {
		t.Fatalf("re-upgraded schema version = %d, want 9", schemaVersion)
	}
	var reupJanitorState, reupActionState string
	if err := reup.DB().QueryRow("SELECT state FROM janitor_records WHERE id = ?", fixture.janitorID).Scan(&reupJanitorState); err != nil {
		t.Fatal(err)
	}
	if err := reup.DB().QueryRow("SELECT state FROM action_runs WHERE id = ?", fixture.actionRunID).Scan(&reupActionState); err != nil {
		t.Fatal(err)
	}
	if reupJanitorState != "held" || reupActionState != "needs_review" {
		t.Fatalf("re-upgraded fenced recovery = janitor=%q action=%q, want held/needs_review", reupJanitorState, reupActionState)
	}
}

func seedRound7ApprovedPurge(t *testing.T, path, suffix string) (*Store, round5PurgeFixture) {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	fixture := seedRound5PurgeBase(t, store, "round7-"+suffix, "2026-09-11T00:10:00Z", round5CreatedAt)
	createRound5Target(t, store, fixture, fixture.entryID, round5CreatedAt)
	if _, err := store.Queries().CreateReviewDecision(ctx, &sqlc.CreateReviewDecisionParams{
		ID: fixture.decisionID, PlanID: fixture.planID, PlanRevision: 1, PlanDigest: fixture.digest,
		Decision: "approve", Actor: "unauthenticated", IdempotencyScope: "round7-" + suffix, IdempotencyKey: "approve", CreatedAt: round5CreatedAt,
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
		WorkerID: sql.NullString{String: "round7-initial", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:01:00Z", Valid: true},
		ApprovalPlanID: sql.NullString{String: fixture.planID, Valid: true}, ApprovalPlanRevision: sql.NullInt64{Int64: 1, Valid: true}, ApprovalPlanDigest: sql.NullString{String: fixture.digest, Valid: true},
		ApprovalDecisionID: sql.NullString{String: fixture.decisionID, Valid: true}, ApprovalActionRunID: sql.NullString{String: fixture.actionRunID, Valid: true},
		ApprovalActionRunVersion: sql.NullInt64{Int64: 1, Valid: true}, ApprovedEntryVersion: sql.NullInt64{Int64: 1, Valid: true},
		Now: round5CreatedAt, ID: fixture.janitorID, Version: 1, TrashEntryID: fixture.entryID,
	}); err != nil {
		_ = store.Close()
		t.Fatalf("initial approved claim: %v", err)
	}
	return store, fixture
}
