package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/guilycst/mastarr/internal/storage/sqlc"
)

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
		name          string
		outcome       string
		actionState   string
		expectedRaw   string
		effectApplied bool
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
			name:          "applied-durable-effect-row",
			outcome:       `{"outcome":"applied"}`,
			actionState:   "succeeded",
			expectedRaw:   `{"outcome":"applied"}`,
			effectApplied: true,
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
			if tc.effectApplied {
				if _, err := store.Queries().CreateActionEffect(ctx, &sqlc.CreateActionEffectParams{
					ID: "round11-effect-" + tc.name, ActionRunID: fixture.actionRunID, Ordinal: 0,
					TargetKind: "trash_entry", TargetID: fixture.entryID, EffectKind: "fs.delete", State: "applied", EvidenceJson: `{}`,
					ObservedAt: "2026-09-11T00:01:11Z",
				}); err != nil {
					t.Fatalf("create durable effect: %v", err)
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
