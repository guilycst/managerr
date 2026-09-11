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

func TestRound10InitialDispatchTerminalCrashFinalizesExactGeneration(t *testing.T) {
	for _, tc := range []struct {
		name            string
		janitorState    string
		janitorOutcome  string
		actionState     string
		entryState      string
		expectedRaw     string
		expectedOutcome string
	}{
		{
			name:            "applied",
			janitorState:    "succeeded",
			janitorOutcome:  `{"outcome":"applied","deletedObjects":1}`,
			actionState:     "succeeded",
			entryState:      "purged",
			expectedRaw:     `{"outcome":"applied","deletedObjects":1}`,
			expectedOutcome: "applied",
		},
		{
			name:            "already-satisfied",
			janitorState:    "succeeded",
			janitorOutcome:  `{"outcome":"already_satisfied"}`,
			actionState:     "succeeded",
			entryState:      "purged",
			expectedRaw:     `{"outcome":"already_satisfied"}`,
			expectedOutcome: "already_satisfied",
		},
		{
			name:            "already-satisfied-zero-counts",
			janitorState:    "succeeded",
			janitorOutcome:  `{"outcome":"already_satisfied","deletedObjects":0,"deleted_objects":0}`,
			actionState:     "succeeded",
			entryState:      "purged",
			expectedRaw:     `{"outcome":"already_satisfied","deletedObjects":0,"deleted_objects":0}`,
			expectedOutcome: "already_satisfied",
		},
		{
			name:            "incomplete-success",
			janitorState:    "succeeded",
			janitorOutcome:  `{}`,
			actionState:     "needs_review",
			entryState:      "purged",
			expectedOutcome: "unknown",
		},
		{
			name:            "contradictory-already-satisfied",
			janitorState:    "succeeded",
			janitorOutcome:  `{"outcome":"already_satisfied","deletedObjects":1}`,
			actionState:     "needs_review",
			entryState:      "purged",
			expectedOutcome: "unknown",
		},
		{
			name:            "failed",
			janitorState:    "failed",
			janitorOutcome:  `{"reason":"synthetic-failure"}`,
			actionState:     "failed",
			entryState:      "failed",
			expectedOutcome: "",
		},
		{
			name:            "held",
			janitorState:    "held",
			janitorOutcome:  `{"reason":"synthetic-hold"}`,
			actionState:     "needs_review",
			entryState:      "held",
			expectedOutcome: "",
		},
		{
			name:            "cancelled",
			janitorState:    "cancelled",
			janitorOutcome:  `{"reason":"synthetic-cancel"}`,
			actionState:     "cancelled",
			entryState:      "failed",
			expectedOutcome: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "initial-terminal-crash.sqlite")
			store, fixture := seedRound7ApprovedPurge(t, path, "round10-"+tc.name)
			defer store.Close()
			ctx := context.Background()

			before, err := store.Queries().GetActionRun(ctx, fixture.actionRunID)
			if err != nil {
				t.Fatal(err)
			}
			if before.State != "running" || before.Version != 2 || !before.ClaimedBy.Valid || !before.LeaseUntil.Valid {
				t.Fatalf("initial dispatch generation = %+v, want running/version 2 with lease", before)
			}

			terminal, err := store.Queries().UpdateJanitorRecord(ctx, &sqlc.UpdateJanitorRecordParams{
				State: tc.janitorState, NextAttemptAt: sql.NullString{}, ClaimedBy: sql.NullString{}, LeaseUntil: sql.NullString{}, OutcomeJson: tc.janitorOutcome,
				UpdatedAt: "2026-09-11T00:01:12Z", ID: fixture.janitorID, Version: 2,
			})
			if err != nil {
				t.Fatalf("terminal janitor update: %v", err)
			}
			if terminal.Version != 3 {
				t.Fatalf("terminal janitor version = %d, want 3", terminal.Version)
			}

			// The terminal janitor keeps the associated action out of generic
			// recovery and claiming until the exact finalizer consumes it.
			if recovered, err := store.Queries().RecoverRunningActionRuns(ctx, sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true}); err != nil {
				t.Fatal(err)
			} else if len(recovered) != 0 {
				t.Fatalf("initial terminal crash entered generic recovery: %+v", recovered)
			}
			due, err := store.Queries().ListDueActionRuns(ctx, &sqlc.ListDueActionRunsParams{
				Now: sql.NullString{String: "2026-09-11T00:02:00Z", Valid: true}, Limit: 10,
			})
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range due {
				if candidate.ID == fixture.actionRunID {
					t.Fatalf("initial terminal crash listed generic due work: %+v", candidate)
				}
			}
			if _, err := store.Queries().ClaimActionRun(ctx, &sqlc.ClaimActionRunParams{
				WorkerID: sql.NullString{String: "round10-generic-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-11T00:03:00Z", Valid: true},
				Now: "2026-09-11T00:02:00Z", ID: fixture.actionRunID, Version: before.Version,
			}); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("initial terminal crash generic claim = %v, want sql.ErrNoRows", err)
			}

			finalized, err := store.Queries().FinalizeApprovedPurgeAction(ctx, &sqlc.FinalizeApprovedPurgeActionParams{
				JanitorID: fixture.janitorID, Now: "2026-09-11T00:02:01Z", ActionRunID: fixture.actionRunID, ActionRunVersion: before.Version,
			})
			if err != nil {
				t.Fatalf("exact finalization after initial terminal crash: %v", err)
			}
			if finalized.State != tc.actionState || finalized.Version != 3 || finalized.ClaimedBy.Valid || finalized.LeaseUntil.Valid {
				t.Fatalf("finalized action = %+v, want %s/version 3/unleased", finalized, tc.actionState)
			}
			if tc.expectedRaw != "" && finalized.OutcomeJson != tc.expectedRaw {
				t.Fatalf("finalized outcome = %s, want preserved %s", finalized.OutcomeJson, tc.expectedRaw)
			}
			if tc.expectedOutcome != "" {
				var payload struct {
					Outcome string `json:"outcome"`
				}
				if err := json.Unmarshal([]byte(finalized.OutcomeJson), &payload); err != nil {
					t.Fatalf("finalized outcome JSON = %q: %v", finalized.OutcomeJson, err)
				}
				if payload.Outcome != tc.expectedOutcome {
					t.Fatalf("finalized outcome value = %q, want %q", payload.Outcome, tc.expectedOutcome)
				}
			}
			entry, err := store.Queries().GetTrashEntry(ctx, fixture.entryID)
			if err != nil {
				t.Fatal(err)
			}
			if entry.State != tc.entryState || entry.Version != 3 {
				t.Fatalf("finalized trash entry = %+v, want %s/version 3", entry, tc.entryState)
			}

			if _, err := store.Queries().FinalizeApprovedPurgeAction(ctx, &sqlc.FinalizeApprovedPurgeActionParams{
				JanitorID: fixture.janitorID, Now: "2026-09-11T00:02:02Z", ActionRunID: fixture.actionRunID, ActionRunVersion: finalized.Version,
			}); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("repeated exact finalization = %v, want sql.ErrNoRows", err)
			}
		})
	}
}
