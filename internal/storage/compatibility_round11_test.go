package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/guilycst/mastarr/internal/storage/sqlc"
)

const round12ApprovedEntryTarget = "$approved-entry"

type round12EffectSpec struct {
	targetKind string
	targetID   string
	effectKind string
	state      string
}

func TestRound11CancellationAfterTerminalJanitorFinalizesExactGeneration(t *testing.T) {
	for _, tc := range []struct {
		name           string
		janitorState   string
		janitorOutcome string
		actionState    string
		entryState     string
		expectedRaw    string
	}{
		{
			name:           "succeeded-applied",
			janitorState:   "succeeded",
			janitorOutcome: `{"outcome":"applied","deletedObjects":1}`,
			actionState:    "succeeded",
			entryState:     "purged",
			expectedRaw:    `{"outcome":"applied","deletedObjects":1}`,
		},
		{
			name:           "succeeded-already-satisfied",
			janitorState:   "succeeded",
			janitorOutcome: `{"outcome":"already_satisfied"}`,
			actionState:    "succeeded",
			entryState:     "purged",
			expectedRaw:    `{"outcome":"already_satisfied"}`,
		},
		{
			name:           "failed",
			janitorState:   "failed",
			janitorOutcome: `{"reason":"synthetic-failure"}`,
			actionState:    "failed",
			entryState:     "failed",
		},
		{
			name:           "held",
			janitorState:   "held",
			janitorOutcome: `{"reason":"synthetic-hold"}`,
			actionState:    "needs_review",
			entryState:     "held",
		},
		{
			name:           "cancelled",
			janitorState:   "cancelled",
			janitorOutcome: `{"reason":"synthetic-cancel"}`,
			actionState:    "cancelled",
			entryState:     "failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, fixture := seedRound7ApprovedPurge(t,
				filepath.Join(t.TempDir(), "terminal-cancel.sqlite"), "round11-cancel-"+tc.name)
			defer store.Close()
			ctx := context.Background()

			if _, err := store.Queries().UpdateJanitorRecord(ctx, &sqlc.UpdateJanitorRecordParams{
				State: tc.janitorState, NextAttemptAt: sql.NullString{}, ClaimedBy: sql.NullString{}, LeaseUntil: sql.NullString{}, OutcomeJson: tc.janitorOutcome,
				UpdatedAt: "2026-09-11T00:01:12Z", ID: fixture.janitorID, Version: 2,
			}); err != nil {
				t.Fatalf("terminal janitor update: %v", err)
			}

			// The cancellation is committed after the janitor and trash entry
			// have reached generation three. Its action CAS advances to the
			// same generation, so exact finalization can still consume it.
			cancelled, err := store.Queries().RequestActionCancellation(ctx, &sqlc.RequestActionCancellationParams{
				RequestedAt: sql.NullString{String: "2026-09-11T00:01:13Z", Valid: true},
				UpdatedAt:   "2026-09-11T00:01:13Z", ID: fixture.actionRunID,
			})
			if err != nil {
				t.Fatalf("request cancellation after terminal janitor: %v", err)
			}
			if cancelled.Version != 3 || !cancelled.CancellationRequestedAt.Valid || !cancelled.ClaimedBy.Valid {
				t.Fatalf("cancelled action generation = %+v, want version 3 with retained lease", cancelled)
			}

			// Repeating the cancellation is idempotent and cannot move the
			// exact terminal generation away from the finalizer.
			repeated, err := store.Queries().RequestActionCancellation(ctx, &sqlc.RequestActionCancellationParams{
				RequestedAt: sql.NullString{String: "2026-09-11T00:01:14Z", Valid: true},
				UpdatedAt:   "2026-09-11T00:01:14Z", ID: fixture.actionRunID,
			})
			if err != nil {
				t.Fatalf("repeat cancellation: %v", err)
			}
			if repeated.Version != cancelled.Version || repeated.CancellationRequestedAt.String != cancelled.CancellationRequestedAt.String {
				t.Fatalf("repeat cancellation changed generation = %+v, initial %+v", repeated, cancelled)
			}

			finalized, err := store.Queries().FinalizeApprovedPurgeAction(ctx, &sqlc.FinalizeApprovedPurgeActionParams{
				JanitorID: fixture.janitorID, Now: "2026-09-11T00:01:15Z", ActionRunID: fixture.actionRunID, ActionRunVersion: repeated.Version,
			})
			if err != nil {
				t.Fatalf("exact finalization after cancellation race: %v", err)
			}
			if finalized.State != tc.actionState || finalized.Version != 4 || finalized.ClaimedBy.Valid || finalized.LeaseUntil.Valid {
				t.Fatalf("finalized action = %+v, want %s/version 4/unleased", finalized, tc.actionState)
			}
			if tc.expectedRaw != "" && finalized.OutcomeJson != tc.expectedRaw {
				t.Fatalf("finalized outcome = %s, want preserved %s", finalized.OutcomeJson, tc.expectedRaw)
			}
			entry, err := store.Queries().GetTrashEntry(ctx, fixture.entryID)
			if err != nil {
				t.Fatal(err)
			}
			if entry.State != tc.entryState || entry.Version != 3 {
				t.Fatalf("finalized entry = %+v, want %s/version 3", entry, tc.entryState)
			}

			if _, err := store.Queries().FinalizeApprovedPurgeAction(ctx, &sqlc.FinalizeApprovedPurgeActionParams{
				JanitorID: fixture.janitorID, Now: "2026-09-11T00:01:16Z", ActionRunID: fixture.actionRunID, ActionRunVersion: finalized.Version,
			}); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("repeated exact finalization = %v, want sql.ErrNoRows", err)
			}
		})
	}
}

func TestRound11TerminalOutcomeRequiresStrictEvidence(t *testing.T) {
	for _, tc := range []struct {
		name        string
		outcome     string
		actionState string
		expectedRaw string
		effects     []round12EffectSpec
	}{
		{
			name:        "applied-positive-camel",
			outcome:     `{"outcome":"applied","deletedObjects":1}`,
			actionState: "succeeded",
			expectedRaw: `{"outcome":"applied","deletedObjects":1}`,
		},
		{
			name:        "applied-positive-snake",
			outcome:     `{"outcome":"applied","deleted_objects":2}`,
			actionState: "succeeded",
			expectedRaw: `{"outcome":"applied","deleted_objects":2}`,
		},
		{
			name:        "applied-positive-matching-aliases",
			outcome:     `{"outcome":"applied","deletedObjects":2,"deleted_objects":2}`,
			actionState: "succeeded",
			expectedRaw: `{"outcome":"applied","deletedObjects":2,"deleted_objects":2}`,
		},
		{
			name:        "applied-durable-effect-row",
			outcome:     `{"outcome":"applied"}`,
			actionState: "succeeded",
			expectedRaw: `{"outcome":"applied"}`,
			effects: []round12EffectSpec{{
				targetKind: "trash_entry", targetID: round12ApprovedEntryTarget,
				effectKind: "fs.delete", state: "applied",
			}},
		},
		{
			name:        "applied-durable-effect-with-unrelated-effect",
			outcome:     `{"outcome":"applied"}`,
			actionState: "succeeded",
			expectedRaw: `{"outcome":"applied"}`,
			effects: []round12EffectSpec{
				{targetKind: "trash_entry", targetID: round12ApprovedEntryTarget, effectKind: "fs.delete", state: "applied"},
				{targetKind: "trash_entry", targetID: round12ApprovedEntryTarget, effectKind: "fs.copy", state: "applied"},
			},
		},
		{
			name:        "applied-durable-effect-wrong-kind",
			outcome:     `{"outcome":"applied"}`,
			actionState: "needs_review",
			effects: []round12EffectSpec{{
				targetKind: "trash_entry", targetID: round12ApprovedEntryTarget,
				effectKind: "fs.copy", state: "applied",
			}},
		},
		{
			name:        "applied-durable-effect-wrong-target-id",
			outcome:     `{"outcome":"applied"}`,
			actionState: "needs_review",
			effects: []round12EffectSpec{{
				targetKind: "trash_entry", targetID: "other-entry",
				effectKind: "fs.delete", state: "applied",
			}},
		},
		{
			name:        "applied-durable-effect-wrong-target-kind",
			outcome:     `{"outcome":"applied"}`,
			actionState: "needs_review",
			effects: []round12EffectSpec{{
				targetKind: "trash_item", targetID: round12ApprovedEntryTarget,
				effectKind: "fs.delete", state: "applied",
			}},
		},
		{
			name:        "applied-durable-effect-failed",
			outcome:     `{"outcome":"applied"}`,
			actionState: "needs_review",
			effects: []round12EffectSpec{{
				targetKind: "trash_entry", targetID: round12ApprovedEntryTarget,
				effectKind: "fs.delete", state: "failed",
			}},
		},
		{
			name:        "applied-durable-effect-unknown",
			outcome:     `{"outcome":"applied"}`,
			actionState: "needs_review",
			effects: []round12EffectSpec{{
				targetKind: "trash_entry", targetID: round12ApprovedEntryTarget,
				effectKind: "fs.delete", state: "unknown",
			}},
		},
		{
			name:        "already-satisfied-absent-count",
			outcome:     `{"outcome":"already_satisfied"}`,
			actionState: "succeeded",
			expectedRaw: `{"outcome":"already_satisfied"}`,
		},
		{
			name:        "already-satisfied-zero-counts",
			outcome:     `{"outcome":"already_satisfied","deletedObjects":0,"deleted_objects":0}`,
			actionState: "succeeded",
			expectedRaw: `{"outcome":"already_satisfied","deletedObjects":0,"deleted_objects":0}`,
		},
		{
			name:        "already-satisfied-unrelated-applied-effect",
			outcome:     `{"outcome":"already_satisfied"}`,
			actionState: "needs_review",
			effects: []round12EffectSpec{{
				targetKind: "trash_entry", targetID: round12ApprovedEntryTarget,
				effectKind: "fs.copy", state: "applied",
			}},
		},
		{
			name:        "already-satisfied-wrong-target-delete-effect",
			outcome:     `{"outcome":"already_satisfied"}`,
			actionState: "needs_review",
			effects: []round12EffectSpec{{
				targetKind: "trash_entry", targetID: "other-entry",
				effectKind: "fs.delete", state: "applied",
			}},
		},
		{
			name:        "already-satisfied-exact-delete-effect",
			outcome:     `{"outcome":"already_satisfied"}`,
			actionState: "needs_review",
			effects: []round12EffectSpec{{
				targetKind: "trash_entry", targetID: round12ApprovedEntryTarget,
				effectKind: "fs.delete", state: "applied",
			}},
		},
		{
			name:        "already-satisfied-mixed-unrelated-effects",
			outcome:     `{"outcome":"already_satisfied","deletedObjects":0}`,
			actionState: "needs_review",
			effects: []round12EffectSpec{
				{targetKind: "trash_entry", targetID: "other-entry", effectKind: "fs.delete", state: "failed"},
				{targetKind: "trash_item", targetID: round12ApprovedEntryTarget, effectKind: "fs.copy", state: "applied"},
			},
		},
		{name: "applied-zero-camel", outcome: `{"outcome":"applied","deletedObjects":0}`, actionState: "needs_review"},
		{name: "applied-zero-snake", outcome: `{"outcome":"applied","deleted_objects":0}`, actionState: "needs_review"},
		{name: "applied-negative", outcome: `{"outcome":"applied","deletedObjects":-1}`, actionState: "needs_review"},
		{name: "applied-fractional", outcome: `{"outcome":"applied","deletedObjects":1.0}`, actionState: "needs_review"},
		{name: "applied-string", outcome: `{"outcome":"applied","deletedObjects":"1"}`, actionState: "needs_review"},
		{name: "applied-null", outcome: `{"outcome":"applied","deletedObjects":null}`, actionState: "needs_review"},
		{name: "applied-boolean", outcome: `{"outcome":"applied","deletedObjects":true}`, actionState: "needs_review"},
		{name: "applied-conflicting-aliases", outcome: `{"outcome":"applied","deletedObjects":1,"deleted_objects":2}`, actionState: "needs_review"},
		{name: "applied-duplicate-count-key", outcome: `{"outcome":"applied","deletedObjects":0,"deletedObjects":1}`, actionState: "needs_review"},
		{name: "applied-duplicate-outcome-key", outcome: `{"outcome":"applied","outcome":"already_satisfied","deletedObjects":1}`, actionState: "needs_review"},
		{name: "applied-missing-outcome", outcome: `{"deletedObjects":1}`, actionState: "needs_review"},
		{name: "already-satisfied-positive", outcome: `{"outcome":"already_satisfied","deletedObjects":1}`, actionState: "needs_review"},
		{name: "already-satisfied-invalid-type", outcome: `{"outcome":"already_satisfied","deletedObjects":"0"}`, actionState: "needs_review"},
		{name: "already-satisfied-conflicting-aliases", outcome: `{"outcome":"already_satisfied","deletedObjects":0,"deleted_objects":1}`, actionState: "needs_review"},
		{name: "already-satisfied-duplicate-count-key", outcome: `{"outcome":"already_satisfied","deletedObjects":0,"deletedObjects":0}`, actionState: "needs_review"},
		{name: "already-satisfied-duplicate-outcome-key", outcome: `{"outcome":"already_satisfied","outcome":"already_satisfied"}`, actionState: "needs_review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, fixture := seedRound7ApprovedPurge(t,
				filepath.Join(t.TempDir(), "strict-outcome.sqlite"), "round11-outcome-"+tc.name)
			defer store.Close()
			ctx := context.Background()
			for ordinal, effect := range tc.effects {
				targetID := effect.targetID
				if targetID == round12ApprovedEntryTarget {
					targetID = fixture.entryID
				}
				if _, err := store.Queries().CreateActionEffect(ctx, &sqlc.CreateActionEffectParams{
					ID: "round12-effect-" + tc.name + "-" + strconv.Itoa(ordinal), ActionRunID: fixture.actionRunID, Ordinal: int64(ordinal),
					TargetKind: effect.targetKind, TargetID: targetID, EffectKind: effect.effectKind, State: effect.state, EvidenceJson: `{}`,
					ObservedAt: "2026-09-11T00:01:11Z",
				}); err != nil {
					t.Fatalf("create durable effect %d: %v", ordinal, err)
				}
			}
			if _, err := store.Queries().UpdateJanitorRecord(ctx, &sqlc.UpdateJanitorRecordParams{
				State: "succeeded", NextAttemptAt: sql.NullString{}, ClaimedBy: sql.NullString{}, LeaseUntil: sql.NullString{}, OutcomeJson: tc.outcome,
				UpdatedAt: "2026-09-11T00:01:12Z", ID: fixture.janitorID, Version: 2,
			}); err != nil {
				t.Fatalf("terminal janitor update: %v", err)
			}

			finalized, err := store.Queries().FinalizeApprovedPurgeAction(ctx, &sqlc.FinalizeApprovedPurgeActionParams{
				JanitorID: fixture.janitorID, Now: "2026-09-11T00:01:13Z", ActionRunID: fixture.actionRunID, ActionRunVersion: 2,
			})
			if err != nil {
				t.Fatalf("exact finalization: %v", err)
			}
			if finalized.State != tc.actionState {
				t.Fatalf("action state = %q, want %q; outcome=%s", finalized.State, tc.actionState, finalized.OutcomeJson)
			}
			if tc.expectedRaw != "" && finalized.OutcomeJson != tc.expectedRaw {
				t.Fatalf("valid outcome = %s, want preserved %s", finalized.OutcomeJson, tc.expectedRaw)
			}
			if tc.expectedRaw == "" {
				var payload struct {
					Outcome string `json:"outcome"`
				}
				if err := json.Unmarshal([]byte(finalized.OutcomeJson), &payload); err != nil {
					t.Fatalf("unknown outcome JSON = %q: %v", finalized.OutcomeJson, err)
				}
				if payload.Outcome != "unknown" {
					t.Fatalf("invalid evidence outcome = %q, want unknown", payload.Outcome)
				}
			} else {
				var payload struct {
					Outcome string `json:"outcome"`
				}
				if err := json.Unmarshal([]byte(finalized.OutcomeJson), &payload); err != nil {
					t.Fatalf("valid outcome JSON = %q: %v", finalized.OutcomeJson, err)
				}
				if payload.Outcome != "applied" && payload.Outcome != "already_satisfied" {
					t.Fatalf("successful evidence outcome = %q, want applied/already_satisfied", payload.Outcome)
				}
			}
		})
	}
}

func TestRound11MalformedTerminalOutcomeBecomesUnknown(t *testing.T) {
	store, fixture := seedRound7ApprovedPurge(t,
		filepath.Join(t.TempDir(), "malformed-outcome.sqlite"), "round11-malformed")
	defer store.Close()

	// The schema normally rejects malformed JSON before a terminal journal can
	// exist. Ignore only the SQLite check for this corruption fixture so the
	// finalizer's defensive parser is exercised against persisted bad state.
	if _, err := store.DB().Exec("PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE janitor_records
		SET state = 'succeeded', claimed_by = NULL, lease_until = NULL,
		    outcome_json = '{"outcome":"applied"', updated_at = '2026-09-11T00:01:12Z', version = version + 1
		WHERE id = ? AND version = 2`, fixture.janitorID); err != nil {
		t.Fatalf("persist malformed terminal outcome: %v", err)
	}

	finalized, err := store.Queries().FinalizeApprovedPurgeAction(context.Background(), &sqlc.FinalizeApprovedPurgeActionParams{
		JanitorID: fixture.janitorID, Now: "2026-09-11T00:01:13Z", ActionRunID: fixture.actionRunID, ActionRunVersion: 2,
	})
	if err != nil {
		t.Fatalf("exact finalization: %v", err)
	}
	if finalized.State != "needs_review" {
		t.Fatalf("malformed outcome action state = %q, want needs_review", finalized.State)
	}
	var payload struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal([]byte(finalized.OutcomeJson), &payload); err != nil {
		t.Fatalf("unknown outcome JSON = %q: %v", finalized.OutcomeJson, err)
	}
	if payload.Outcome != "unknown" {
		t.Fatalf("malformed outcome = %q, want unknown", payload.Outcome)
	}
}

func TestRound12CancellationRejectsMissingOrInvalidRequestedAt(t *testing.T) {
	for _, tc := range []struct {
		name        string
		requestedAt sql.NullString
	}{
		{name: "null", requestedAt: sql.NullString{}},
		{name: "blank", requestedAt: sql.NullString{String: "   ", Valid: true}},
		{name: "invalid", requestedAt: sql.NullString{String: "not-a-timestamp", Valid: true}},
		{name: "numeric", requestedAt: sql.NullString{String: "1", Valid: true}},
		{name: "time-only", requestedAt: sql.NullString{String: "12:34", Valid: true}},
		{name: "date-only", requestedAt: sql.NullString{String: "2026-09-11", Valid: true}},
		{name: "impossible-date", requestedAt: sql.NullString{String: "2026-02-30T00:00:00Z", Valid: true}},
		{name: "invalid-leap-day", requestedAt: sql.NullString{String: "2026-02-29T00:00:00Z", Valid: true}},
		{name: "trailing-data", requestedAt: sql.NullString{String: "2026-09-11T00:01:13Ztrailing", Valid: true}},
		{name: "invalid-hour", requestedAt: sql.NullString{String: "2026-09-11T24:00:00Z", Valid: true}},
		{name: "invalid-offset", requestedAt: sql.NullString{String: "2026-09-11T00:01:13+99:99", Valid: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, fixture := seedRound7ApprovedPurge(t,
				filepath.Join(t.TempDir(), "invalid-cancellation.sqlite"), "round12-invalid-cancel-"+tc.name)
			defer store.Close()
			ctx := context.Background()

			beforeAction, err := store.Queries().GetActionRun(ctx, fixture.actionRunID)
			if err != nil {
				t.Fatal(err)
			}
			beforeJanitor, err := store.Queries().GetJanitorRecord(ctx, &sqlc.GetJanitorRecordParams{TrashEntryID: fixture.entryID, Operation: "purge"})
			if err != nil {
				t.Fatal(err)
			}
			beforeEntry, err := store.Queries().GetTrashEntry(ctx, fixture.entryID)
			if err != nil {
				t.Fatal(err)
			}

			_, err = store.Queries().RequestActionCancellation(ctx, &sqlc.RequestActionCancellationParams{
				RequestedAt: tc.requestedAt,
				UpdatedAt:   "2026-09-11T00:01:13Z",
				ID:          fixture.actionRunID,
			})
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("invalid cancellation = %v, want sql.ErrNoRows", err)
			}

			afterAction, err := store.Queries().GetActionRun(ctx, fixture.actionRunID)
			if err != nil {
				t.Fatal(err)
			}
			if afterAction.Version != beforeAction.Version || afterAction.UpdatedAt != beforeAction.UpdatedAt || afterAction.CancellationRequestedAt.Valid != beforeAction.CancellationRequestedAt.Valid {
				t.Fatalf("invalid cancellation changed action generation/marker: before=%+v after=%+v", beforeAction, afterAction)
			}
			if afterAction.CancellationRequestedAt.Valid && afterAction.CancellationRequestedAt.String != beforeAction.CancellationRequestedAt.String {
				t.Fatalf("invalid cancellation changed marker: before=%+v after=%+v", beforeAction.CancellationRequestedAt, afterAction.CancellationRequestedAt)
			}

			afterJanitor, err := store.Queries().GetJanitorRecord(ctx, &sqlc.GetJanitorRecordParams{TrashEntryID: fixture.entryID, Operation: "purge"})
			if err != nil {
				t.Fatal(err)
			}
			if afterJanitor.Version != beforeJanitor.Version || afterJanitor.UpdatedAt != beforeJanitor.UpdatedAt || afterJanitor.State != beforeJanitor.State {
				t.Fatalf("invalid cancellation changed janitor: before=%+v after=%+v", beforeJanitor, afterJanitor)
			}
			afterEntry, err := store.Queries().GetTrashEntry(ctx, fixture.entryID)
			if err != nil {
				t.Fatal(err)
			}
			if afterEntry.Version != beforeEntry.Version || afterEntry.UpdatedAt != beforeEntry.UpdatedAt || afterEntry.State != beforeEntry.State {
				t.Fatalf("invalid cancellation changed trash entry: before=%+v after=%+v", beforeEntry, afterEntry)
			}
		})
	}
}

func TestRound12CancellationRejectsInvalidRequestedAtForUnboundAction(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "invalid-unbound-cancellation.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	createdAt := "2026-09-11T00:00:00Z"
	planID := "round12-unbound-plan"
	digest := "round12-unbound-digest"
	actionID := "round12-unbound-action"
	if _, err := store.Queries().CreateActionPlan(ctx, &sqlc.CreateActionPlanParams{
		ID: planID, Kind: "fs.copy", State: "ready", CurrentRevision: 1, CurrentDigest: digest,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionPlanRevision(ctx, &sqlc.CreateActionPlanRevisionParams{
		PlanID: planID, Revision: 1, Digest: digest, State: "ready", InputJson: `{}`, PreconditionsJson: `{}`,
		CapabilitiesJson: `[]`, ManifestJson: `[]`, CreatedAt: createdAt, ExpiresAt: "2026-09-20T00:00:00Z",
		ReadyAt: sql.NullString{String: createdAt, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionRun(ctx, &sqlc.CreateActionRunParams{
		ID: actionID, PlanID: planID, PlanRevision: 1, PlanDigest: digest, State: "running", DesiredStateJson: `{}`,
		DeadlineAt: sql.NullString{String: "2026-09-20T00:00:00Z", Valid: true},
		ClaimedBy:  sql.NullString{String: "round12-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:05:00Z", Valid: true},
		Version: 1, OutcomeJson: `{}`, CreatedAt: createdAt, UpdatedAt: createdAt,
	}); err != nil {
		t.Fatal(err)
	}

	for _, requestedAt := range []sql.NullString{
		{},
		{String: " ", Valid: true},
		{String: "not-a-timestamp", Valid: true},
	} {
		if _, err := store.Queries().RequestActionCancellation(ctx, &sqlc.RequestActionCancellationParams{
			RequestedAt: requestedAt, UpdatedAt: "2026-09-11T00:01:00Z", ID: actionID,
		}); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("unbound invalid cancellation %v = %v, want sql.ErrNoRows", requestedAt, err)
		}
	}
	action, err := store.Queries().GetActionRun(ctx, actionID)
	if err != nil {
		t.Fatal(err)
	}
	if action.Version != 1 || action.CancellationRequestedAt.Valid || action.UpdatedAt != createdAt || !action.ClaimedBy.Valid || !action.LeaseUntil.Valid {
		t.Fatalf("invalid unbound cancellation changed action = %+v", action)
	}
}

func TestRound13CancellationAcceptsStrictRFC3339DateTimes(t *testing.T) {
	for _, tc := range []struct {
		name        string
		requestedAt string
	}{
		{name: "utc", requestedAt: "2026-09-11T00:01:13Z"},
		{name: "offset", requestedAt: "2026-09-11T00:01:13+05:30"},
		{name: "fractional-offset", requestedAt: "2026-09-11T00:01:13.123456789-04:00"},
		{name: "leap-day", requestedAt: "2024-02-29T23:59:59Z"},
		{name: "leap-day-offset", requestedAt: "2024-02-29T00:00:00+00:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, fixture := seedRound7ApprovedPurge(t,
				filepath.Join(t.TempDir(), "valid-cancellation.sqlite"), "round13-valid-cancel-"+tc.name)
			defer store.Close()
			ctx := context.Background()

			cancelled, err := store.Queries().RequestActionCancellation(ctx, &sqlc.RequestActionCancellationParams{
				RequestedAt: sql.NullString{String: tc.requestedAt, Valid: true},
				UpdatedAt:   "2026-09-11T00:02:00Z", ID: fixture.actionRunID,
			})
			if err != nil {
				t.Fatalf("valid RFC3339 cancellation: %v", err)
			}
			if cancelled.Version != 3 || !cancelled.CancellationRequestedAt.Valid || cancelled.CancellationRequestedAt.String != tc.requestedAt {
				t.Fatalf("valid cancellation = %+v, want version 3 and marker %q", cancelled, tc.requestedAt)
			}

			repeated, err := store.Queries().RequestActionCancellation(ctx, &sqlc.RequestActionCancellationParams{
				RequestedAt: sql.NullString{String: "2026-09-11T00:02:01Z", Valid: true},
				UpdatedAt:   "2026-09-11T00:02:01Z", ID: fixture.actionRunID,
			})
			if err != nil {
				t.Fatalf("repeat valid RFC3339 cancellation: %v", err)
			}
			if repeated.Version != cancelled.Version || repeated.CancellationRequestedAt.String != tc.requestedAt {
				t.Fatalf("repeat valid cancellation = %+v, initial=%+v", repeated, cancelled)
			}
		})
	}
}

func TestRound14CancellationRejectsEmbeddedNULAndNonASCII(t *testing.T) {
	for _, tc := range []struct {
		name        string
		requestedAt string
	}{
		{name: "embedded-nul-trailing", requestedAt: "2026-09-11T00:01:13Z\x00trailing"},
		{name: "embedded-nul-before-zone", requestedAt: "2026-09-11T00:01:13\x00Z"},
		{name: "non-ascii-trailing", requestedAt: "2026-09-11T00:01:13Zé"},
		{name: "non-ascii-byte-trailing", requestedAt: "2026-09-11T00:01:13Z\xff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, fixture := seedRound7ApprovedPurge(t,
				filepath.Join(t.TempDir(), "invalid-byte-cancellation.sqlite"), "round14-invalid-byte-"+tc.name)
			defer store.Close()
			ctx := context.Background()

			before, err := store.Queries().GetActionRun(ctx, fixture.actionRunID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.Queries().RequestActionCancellation(ctx, &sqlc.RequestActionCancellationParams{
				RequestedAt: sql.NullString{String: tc.requestedAt, Valid: true},
				UpdatedAt:   "2026-09-11T00:02:00Z", ID: fixture.actionRunID,
			})
			if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("invalid byte cancellation = %v, want sql.ErrNoRows", err)
			}

			after, err := store.Queries().GetActionRun(ctx, fixture.actionRunID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Version != before.Version || after.UpdatedAt != before.UpdatedAt || after.CancellationRequestedAt.Valid {
				t.Fatalf("invalid byte cancellation changed action: before=%+v after=%+v", before, after)
			}
		})
	}
}
