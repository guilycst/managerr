package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/storage"
	"github.com/guilycst/mastarr/internal/storage/sqlc"
)

const executionFixtureTime = "2026-09-14T12:00:00Z"

func TestRunOnceAlreadySatisfiedSkipsDispatch(t *testing.T) {
	journal, action := newMemoryAction("already-satisfied", domain.ActionFSCopy, domain.ActionQueued)
	handler := &scriptedHandler{
		kind: domain.ActionFSCopy,
		observeFn: func(_ context.Context, _ Action, _ int) (Observation, error) {
			return Observation{State: ObserveSatisfied, Evidence: []string{"destination_digest_matches"}}, nil
		},
	}
	clockValue := executionTime()
	clock := func() time.Time { return clockValue }
	executor := newTestExecutor(t, journal, handler, clock)

	batch, err := executor.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if batch.Considered != 1 || len(batch.Results) != 1 {
		t.Fatalf("batch = %+v, want one considered result", batch)
	}
	result := batch.Results[0]
	if result.State != domain.ActionSucceeded || result.Outcome != domain.OutcomeAlreadySatisfied {
		t.Fatalf("result = %+v, want already-satisfied success", result)
	}
	if handler.dispatchCalls() != 0 {
		t.Fatalf("dispatch calls = %d, want zero", handler.dispatchCalls())
	}
	current := mustAction(t, journal, action.ID)
	if current.State != domain.ActionSucceeded || current.Version != 3 {
		t.Fatalf("persisted action = %+v, want succeeded at version 3", current)
	}
	attempts := mustAttempts(t, journal, action.ID)
	if len(attempts) != 1 || attempts[0].Phase != AttemptObserve || attempts[0].State != domain.AttemptSucceeded {
		t.Fatalf("attempts = %+v, want one successful observe", attempts)
	}
	if effects := mustEffects(t, journal, action.ID); len(effects) != 0 {
		t.Fatalf("effects = %+v, want no effects for an empty already-satisfied observation", effects)
	}
}

func TestDispatchRecordsOrderedAttemptsAndReadBack(t *testing.T) {
	journal, action := newMemoryAction("dispatch-success", domain.ActionFSCopy, domain.ActionQueued)
	effect := executionEffect("copy", "payload.bin")
	handler := &scriptedHandler{
		kind: domain.ActionFSCopy,
		observeFn: func(_ context.Context, _ Action, call int) (Observation, error) {
			if call == 1 {
				return Observation{State: ObserveNeedsAction, Evidence: []string{"destination_missing"}, Effects: []Effect{effect}}, nil
			}
			return Observation{State: ObserveSatisfied, Evidence: []string{"destination_digest_matches"}, Effects: []Effect{effect}}, nil
		},
		dispatchFn: func(_ context.Context, _ Action, _ Attempt) (DispatchResult, error) {
			return DispatchResult{Accepted: true, Outcome: domain.OutcomeApplied, ExternalID: "copy-effect"}, nil
		},
	}
	executor := newTestExecutor(t, journal, handler, executionClock())

	batch, err := executor.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Results) != 1 {
		t.Fatalf("results = %+v, want one result", batch.Results)
	}
	result := batch.Results[0]
	if result.State != domain.ActionSucceeded || result.Outcome != domain.OutcomeApplied || !result.Dispatched {
		t.Fatalf("result = %+v, want applied dispatched success", result)
	}
	if got := handler.eventsSnapshot(); !equalStrings(got, []string{"observe", "dispatch", "observe"}) {
		t.Fatalf("handler order = %v, want observe, dispatch, observe", got)
	}
	attempts := mustAttempts(t, journal, action.ID)
	if len(attempts) != 2 {
		t.Fatalf("attempt count = %d, want observe and dispatch", len(attempts))
	}
	if attempts[0].Phase != AttemptObserve || attempts[0].State != domain.AttemptSucceeded || attempts[1].Phase != AttemptDispatch || attempts[1].State != domain.AttemptSucceeded {
		t.Fatalf("ordered attempts = %+v", attempts)
	}
	effects := mustEffects(t, journal, action.ID)
	if len(effects) != 1 || effects[0].State != EffectApplied || effects[0].AttemptID != attempts[1].ID {
		t.Fatalf("effects = %+v, want one applied effect bound to dispatch", effects)
	}
	if journal.transactionCalls() < 2 {
		t.Fatalf("transaction calls = %d, want prepare and effect transactions", journal.transactionCalls())
	}
}

func TestLostDispatchResponseReconcilesBeforeRetry(t *testing.T) {
	journal, action := newMemoryAction("uncertain-dispatch", domain.ActionFSCopy, domain.ActionQueued)
	effect := executionEffect("copy", "payload.bin")
	handler := &scriptedHandler{
		kind: domain.ActionFSCopy,
		observeFn: func(_ context.Context, _ Action, _ int) (Observation, error) {
			return Observation{State: ObserveNeedsAction, Effects: []Effect{effect}}, nil
		},
		dispatchFn: func(_ context.Context, _ Action, _ Attempt) (DispatchResult, error) {
			return DispatchResult{}, NewDispatchedFailure(FailureUncertain, errors.New("connection lost after write"))
		},
		reconcileFn: func(_ context.Context, _ Action, _ Attempt) (ReconcileResult, error) {
			return ReconcileResult{Outcome: domain.OutcomeApplied, Evidence: []string{"read_back_applied"}, Effects: []Effect{effect}}, nil
		},
	}
	executor := newTestExecutor(t, journal, handler, executionClock())

	first := mustRunOnce(t, executor)
	if len(first.Results) != 1 || first.Results[0].State != domain.ActionReconciling {
		t.Fatalf("first result = %+v, want reconciling", first.Results)
	}
	if handler.dispatchCalls() != 1 || handler.reconcileCalls() != 0 {
		t.Fatalf("calls after uncertain dispatch = dispatch %d reconcile %d", handler.dispatchCalls(), handler.reconcileCalls())
	}
	current := mustAction(t, journal, action.ID)
	if current.State != domain.ActionReconciling || current.UnresolvedCount != 1 {
		t.Fatalf("uncertain action = %+v, want unresolved reconciling", current)
	}
	clockValue := executionTime().Add(10 * time.Second)
	clock := func() time.Time { return clockValue }
	executor = newTestExecutor(t, journal, handler, clock)

	second := mustRunOnce(t, executor)
	if len(second.Results) != 1 || second.Results[0].State != domain.ActionSucceeded || second.Results[0].Outcome != domain.OutcomeApplied {
		t.Fatalf("reconciliation result = %+v, want applied success", second.Results)
	}
	if handler.dispatchCalls() != 1 || handler.reconcileCalls() != 1 {
		t.Fatalf("calls after reconciliation = dispatch %d reconcile %d, want 1/1", handler.dispatchCalls(), handler.reconcileCalls())
	}
	if got := handler.eventsSnapshot(); !equalStrings(got, []string{"observe", "dispatch", "reconcile"}) {
		t.Fatalf("handler order = %v, want observe, dispatch, reconcile", got)
	}
	if effects := mustEffects(t, journal, action.ID); len(effects) != 1 || effects[0].State != EffectApplied {
		t.Fatalf("reconciled effects = %+v, want applied evidence", effects)
	}
}

func TestReconciliationMustProveNoEffectBeforeMutationRetry(t *testing.T) {
	journal, _ := newMemoryAction("safe-retry", domain.ActionFSCopy, domain.ActionQueued)
	effect := executionEffect("copy", "payload.bin")
	handler := &scriptedHandler{
		kind: domain.ActionFSCopy,
		observeFn: func(_ context.Context, _ Action, _ int) (Observation, error) {
			return Observation{State: ObserveNeedsAction, Effects: []Effect{effect}}, nil
		},
		dispatchFn: func(_ context.Context, _ Action, _ Attempt) (DispatchResult, error) {
			return DispatchResult{}, NewDispatchedFailure(FailureUncertain, errors.New("write response lost"))
		},
		reconcileFn: func(_ context.Context, _ Action, _ Attempt) (ReconcileResult, error) {
			return ReconcileResult{SafeToRetry: true, Evidence: []string{"target_absent"}}, nil
		},
	}
	executor := newTestExecutor(t, journal, handler, executionClock())

	first := mustRunOnce(t, executor)
	if len(first.Results) != 1 || first.Results[0].State != domain.ActionReconciling {
		t.Fatalf("uncertain result = %+v", first.Results)
	}
	clockValue := executionTime().Add(10 * time.Second)
	clock := func() time.Time { return clockValue }
	// Rebuild the executor with the advanced clock only after the first
	// uncertain result has been persisted; reconciliation is backoff-gated.
	executor = newTestExecutor(t, journal, handler, clock)
	second := mustRunOnce(t, executor)
	if len(second.Results) != 1 || second.Results[0].State != domain.ActionQueued {
		t.Fatalf("safe retry result = %+v, want queued", second.Results)
	}
	if handler.dispatchCalls() != 1 {
		t.Fatalf("dispatch calls before due retry = %d, want one", handler.dispatchCalls())
	}
	if got := handler.eventsSnapshot(); !equalStrings(got, []string{"observe", "dispatch", "reconcile"}) {
		t.Fatalf("handler order before retry = %v", got)
	}

	third := mustRunOnce(t, executor)
	if len(third.Results) != 1 || third.Results[0].State != domain.ActionReconciling {
		t.Fatalf("second dispatch result = %+v, want uncertain reconciling", third.Results)
	}
	if handler.dispatchCalls() != 2 {
		t.Fatalf("dispatch calls after proven retry = %d, want two", handler.dispatchCalls())
	}
	if got := handler.eventsSnapshot(); !equalStrings(got, []string{"observe", "dispatch", "reconcile", "observe", "dispatch"}) {
		t.Fatalf("handler order after retry = %v", got)
	}
}

func TestRestartRecoveryNeverBlindlyDispatchesRunningAttempt(t *testing.T) {
	journal, action := newMemoryAction("crash-recovery", domain.ActionFSCopy, domain.ActionRunning)
	action.ClaimedBy = "old-worker"
	action.LeaseUntil = executionFixtureTime
	action.Version = 2
	journal.putAction(action)
	journal.putAttempt(Attempt{
		ID: "crash-recovery-dispatch", ActionRunID: action.ID, AttemptNumber: 1,
		Phase: AttemptDispatch, State: domain.AttemptRunning, StartedAt: executionFixtureTime,
		OutcomeCertainty: CertaintyNotDispatched, Evidence: json.RawMessage(`{}`),
	})
	handler := &scriptedHandler{
		kind: domain.ActionFSCopy,
		reconcileFn: func(_ context.Context, _ Action, _ Attempt) (ReconcileResult, error) {
			return ReconcileResult{Outcome: domain.OutcomeApplied, Evidence: []string{"startup_read_back"}}, nil
		},
	}
	executor := newTestExecutor(t, journal, handler, executionClock())

	report, err := executor.Recover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Actions != 1 || report.Attempts != 1 {
		t.Fatalf("recovery report = %+v, want one action and attempt", report)
	}
	recovered := mustAction(t, journal, action.ID)
	if recovered.State != domain.ActionReconciling || recovered.ClaimedBy != "" || recovered.LeaseUntil != "" {
		t.Fatalf("recovered action = %+v, want unleased reconciliation", recovered)
	}
	attempts := mustAttempts(t, journal, action.ID)
	if len(attempts) != 1 || attempts[0].State != domain.AttemptReconciling || attempts[0].OutcomeCertainty != CertaintyUncertain {
		t.Fatalf("recovered attempt = %+v, want uncertain reconciliation", attempts)
	}
	result := mustRunOnce(t, executor)
	if len(result.Results) != 1 || result.Results[0].State != domain.ActionSucceeded {
		t.Fatalf("post-recovery result = %+v", result.Results)
	}
	if handler.dispatchCalls() != 0 || handler.reconcileCalls() != 1 {
		t.Fatalf("post-recovery calls = dispatch %d reconcile %d", handler.dispatchCalls(), handler.reconcileCalls())
	}
}

func TestDependencyBackoffPersistsAcrossPollingAndRestart(t *testing.T) {
	journal, action := newMemoryAction("dependency-backoff", domain.ActionFSCopy, domain.ActionQueued)
	clock := executionClock()
	var observations int
	handler := &scriptedHandler{
		kind: domain.ActionFSCopy,
		observeFn: func(_ context.Context, _ Action, _ int) (Observation, error) {
			observations++
			if observations == 1 {
				return Observation{}, NewFailure(FailureDependency, errors.New("upstream unavailable"))
			}
			return Observation{State: ObserveSatisfied}, nil
		},
	}
	executor := newTestExecutor(t, journal, handler, clock)
	first := mustRunOnce(t, executor)
	if len(first.Results) != 1 || first.Results[0].State != domain.ActionWaitingDependency {
		t.Fatalf("dependency result = %+v, want waiting", first.Results)
	}
	waiting := mustAction(t, journal, action.ID)
	if waiting.State != domain.ActionWaitingDependency {
		t.Fatalf("waiting action = %+v", waiting)
	}
	next, err := parseTime(waiting.NextAttemptAt)
	if err != nil || !next.Equal(clock().Add(5*time.Second)) {
		t.Fatalf("next attempt = %q (%v), want five second persisted backoff", waiting.NextAttemptAt, err)
	}
	beforeDue := mustRunOnce(t, executor)
	if beforeDue.Considered != 0 || len(beforeDue.Results) != 0 || observations != 1 {
		t.Fatalf("before due batch = %+v observations=%d", beforeDue, observations)
	}
	clockNow := clock().Add(5 * time.Second)
	clock = func() time.Time { return clockNow }
	// Construct a second executor over the same journal to prove the delay is
	// read from the durable action row, rather than held in worker memory.
	restarted := newTestExecutor(t, journal, handler, clock)
	second := mustRunOnce(t, restarted)
	if len(second.Results) != 1 || second.Results[0].State != domain.ActionSucceeded {
		t.Fatalf("post-backoff result = %+v", second.Results)
	}
	if observations != 2 {
		t.Fatalf("observations = %d, want one failed and one retry", observations)
	}
}

func TestCancellationBeforeDispatchHasNoMutationCall(t *testing.T) {
	journal, action := newMemoryAction("cancel-before-dispatch", domain.ActionFSCopy, domain.ActionQueued)
	handler := &scriptedHandler{kind: domain.ActionFSCopy}
	handler.observeFn = func(_ context.Context, _ Action, call int) (Observation, error) {
		if call == 1 {
			if _, err := journal.RequestCancellation(context.Background(), action.ID, executionFixtureTime); err != nil {
				return Observation{}, err
			}
			return Observation{State: ObserveNeedsAction, Effects: []Effect{executionEffect("copy", "payload.bin")}}, nil
		}
		return Observation{State: ObserveSatisfied}, nil
	}
	handler.dispatchFn = func(_ context.Context, _ Action, _ Attempt) (DispatchResult, error) {
		t.Fatal("dispatch was called after durable cancellation")
		return DispatchResult{}, nil
	}
	executor := newTestExecutor(t, journal, handler, executionClock())
	batch := mustRunOnce(t, executor)
	if len(batch.Results) != 1 || batch.Results[0].State != domain.ActionCancelled {
		t.Fatalf("cancelled result = %+v, want cancelled", batch.Results)
	}
	current := mustAction(t, journal, action.ID)
	if current.State != domain.ActionCancelled {
		t.Fatalf("persisted action = %+v, want cancelled", current)
	}
	if handler.dispatchCalls() != 0 {
		t.Fatalf("dispatch calls = %d, want zero", handler.dispatchCalls())
	}
	attempts := mustAttempts(t, journal, action.ID)
	if len(attempts) != 2 || attempts[1].Phase != AttemptDispatch || attempts[1].State != domain.AttemptCancelled || attempts[1].OutcomeCertainty != CertaintyNotDispatched {
		t.Fatalf("cancelled attempts = %+v", attempts)
	}
}

func TestCancellationDuringDispatchReportsLateEffect(t *testing.T) {
	journal, action := newMemoryAction("cancel-during-dispatch", domain.ActionFSCopy, domain.ActionQueued)
	handler := &scriptedHandler{kind: domain.ActionFSCopy}
	handler.observeFn = func(_ context.Context, _ Action, call int) (Observation, error) {
		if call == 1 {
			return Observation{State: ObserveNeedsAction, Effects: []Effect{executionEffect("copy", "payload.bin")}}, nil
		}
		return Observation{State: ObserveSatisfied, Effects: []Effect{executionEffect("copy", "payload.bin")}}, nil
	}
	handler.dispatchFn = func(_ context.Context, _ Action, _ Attempt) (DispatchResult, error) {
		if _, err := journal.RequestCancellation(context.Background(), action.ID, executionFixtureTime); err != nil {
			return DispatchResult{}, err
		}
		return DispatchResult{Accepted: true, Outcome: domain.OutcomeApplied}, nil
	}
	executor := newTestExecutor(t, journal, handler, executionClock())
	batch := mustRunOnce(t, executor)
	if len(batch.Results) != 1 || batch.Results[0].State != domain.ActionCancelled {
		t.Fatalf("late cancellation result = %+v, want cancelled", batch.Results)
	}
	current := mustAction(t, journal, action.ID)
	if current.State != domain.ActionCancelled {
		t.Fatalf("persisted action = %+v, want cancelled", current)
	}
	if handler.dispatchCalls() != 1 {
		t.Fatalf("dispatch calls = %d, want one accepted call", handler.dispatchCalls())
	}
	effects := mustEffects(t, journal, action.ID)
	if len(effects) != 1 || effects[0].State != EffectApplied {
		t.Fatalf("late effect evidence = %+v, want applied evidence retained", effects)
	}
}

func TestReservationAndClaimCASPreventOverlap(t *testing.T) {
	journal, first := newMemoryAction("reservation-first", domain.ActionFSCopy, domain.ActionQueued)
	_, second := newMemoryActionOnJournal(journal, "reservation-second", domain.ActionFSCopy, domain.ActionQueued)
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	handler := &scriptedHandler{
		kind:        domain.ActionFSCopy,
		reservation: []string{"root:library"},
		observeFn: func(_ context.Context, _ Action, call int) (Observation, error) {
			if call > 1 {
				return Observation{State: ObserveSatisfied, Effects: []Effect{executionEffect("copy", "payload.bin")}}, nil
			}
			return Observation{State: ObserveNeedsAction, Effects: []Effect{executionEffect("copy", "payload.bin")}}, nil
		},
		dispatchFn: func(ctx context.Context, _ Action, _ Attempt) (DispatchResult, error) {
			startOnce.Do(func() { close(started) })
			select {
			case <-release:
				return DispatchResult{Accepted: true, Outcome: domain.OutcomeApplied}, nil
			case <-ctx.Done():
				return DispatchResult{}, ctx.Err()
			}
		},
	}
	// The same handler is registered on both executor instances. Reservations
	// protect work inside a process, while Claim's version CAS protects the
	// durable row if another executor races on the same action.
	executor := newTestExecutor(t, journal, handler, executionClock())
	otherExecutor := newTestExecutor(t, journal, handler, executionClock())
	firstDone := make(chan Result, 1)
	go func() { firstDone <- executor.RunAction(context.Background(), first.ID) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first dispatch did not start")
	}
	secondDone := make(chan Result, 1)
	go func() { secondDone <- executor.RunAction(context.Background(), second.ID) }()
	select {
	case result := <-secondDone:
		if result.State != domain.ActionWaitingDependency {
			t.Fatalf("overlapping result = %+v, want waiting dependency", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("overlapping action did not stop at reservation boundary")
	}
	// A second worker trying to claim the first row observes the active lease
	// and must not dispatch a second copy.
	casResult := otherExecutor.RunAction(context.Background(), first.ID)
	if !errors.Is(casResult.Err, ErrLeaseLost) {
		t.Fatalf("second worker result = %+v, want lease-loss CAS rejection", casResult)
	}
	close(release)
	firstResult := <-firstDone
	if firstResult.State != domain.ActionSucceeded {
		t.Fatalf("first result = %+v, want success", firstResult)
	}
	if handler.dispatchCalls() != 1 {
		t.Fatalf("dispatch calls = %d, want one", handler.dispatchCalls())
	}
	if current := mustAction(t, journal, second.ID); current.State != domain.ActionWaitingDependency {
		t.Fatalf("second persisted action = %+v, want waiting", current)
	}
}

func TestSQLJournalRecoveryPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "execution.sqlite")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created := "2026-09-14T11:00:00Z"
	planID, actionID, digest := "sql-plan", "sql-action", "sql-digest"
	if _, err := store.Queries().CreateActionPlan(ctx, &sqlc.CreateActionPlanParams{
		ID: planID, Kind: string(domain.ActionFSCopy), State: "ready", CurrentRevision: 1, CurrentDigest: digest,
		CreatedAt: created, UpdatedAt: created,
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionPlanRevision(ctx, &sqlc.CreateActionPlanRevisionParams{
		PlanID: planID, Revision: 1, Digest: digest, State: "ready", InputJson: `{}`, PreconditionsJson: `{}`, CapabilitiesJson: `[]`, ManifestJson: `[]`, CreatedAt: created, ExpiresAt: "2026-09-20T00:00:00Z",
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionRun(ctx, &sqlc.CreateActionRunParams{
		ID: actionID, PlanID: planID, PlanRevision: 1, PlanDigest: digest, State: string(domain.ActionRunning), DesiredStateJson: `{}`, ClaimedBy: sql.NullString{String: "old-worker", Valid: true}, LeaseUntil: sql.NullString{String: "2026-09-14T11:59:00Z", Valid: true}, Version: 2, OutcomeJson: `{}`, CreatedAt: created, UpdatedAt: created,
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionAttempt(ctx, &sqlc.CreateActionAttemptParams{
		ID: "sql-dispatch-attempt", ActionRunID: actionID, AttemptNumber: 1, Phase: string(AttemptDispatch), State: string(domain.AttemptRunning), StartedAt: created, OutcomeCertainty: string(CertaintyNotDispatched), EvidenceJson: `{}`,
	}); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	journal, err := NewSQLJournal(reopened)
	if err != nil {
		t.Fatal(err)
	}
	handler := &scriptedHandler{
		kind: domain.ActionFSCopy,
		reconcileFn: func(_ context.Context, _ Action, _ Attempt) (ReconcileResult, error) {
			return ReconcileResult{Outcome: domain.OutcomeApplied, Evidence: []string{"reopen_read_back"}}, nil
		},
	}
	executor, err := New(journal, Options{WorkerID: "restarted-worker", Now: executionClock()})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.RegisterHandler(handler); err != nil {
		t.Fatal(err)
	}
	report, err := executor.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if report.Actions != 1 || report.Attempts != 1 {
		t.Fatalf("SQL recovery report = %+v", report)
	}
	batch, err := executor.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Results) != 1 || batch.Results[0].State != domain.ActionSucceeded {
		t.Fatalf("SQL post-recovery batch = %+v", batch)
	}
	row, err := journal.GetAction(ctx, actionID)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != domain.ActionSucceeded || row.ClaimedBy != "" || row.LeaseUntil != "" {
		t.Fatalf("SQL final action = %+v", row)
	}
	if handler.dispatchCalls() != 0 || handler.reconcileCalls() != 1 {
		t.Fatalf("SQL calls = dispatch %d reconcile %d", handler.dispatchCalls(), handler.reconcileCalls())
	}
}

func executionClock() func() time.Time {
	value := executionTime()
	return func() time.Time { return value }
}

func executionTime() time.Time {
	value, _ := time.Parse(time.RFC3339, executionFixtureTime)
	return value
}

func newTestExecutor(t *testing.T, journal *memoryJournal, handler *scriptedHandler, now func() time.Time) *Executor {
	t.Helper()
	executor, err := New(journal, Options{WorkerID: "test-worker", Now: now, LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.RegisterHandler(handler); err != nil {
		t.Fatal(err)
	}
	return executor
}

func mustRunOnce(t *testing.T, executor *Executor) BatchResult {
	t.Helper()
	batch, err := executor.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func newMemoryAction(id string, kind domain.ActionKind, state domain.ActionState) (*memoryJournal, Action) {
	journal := &memoryJournal{actions: make(map[string]Action), plans: make(map[string]Plan), attempts: make(map[string][]Attempt), effects: make(map[string][]Effect)}
	return newMemoryActionOnJournal(journal, id, kind, state)
}

func newMemoryActionOnJournal(journal *memoryJournal, id string, kind domain.ActionKind, state domain.ActionState) (*memoryJournal, Action) {
	action := Action{
		ID: id, PlanID: id + "-plan", PlanRevision: 1, PlanDigest: id + "-digest", Kind: kind, State: state,
		DesiredState: json.RawMessage(`{"target":"payload.bin"}`), Outcome: json.RawMessage(`{}`), Version: 1,
		CreatedAt: executionFixtureTime, UpdatedAt: executionFixtureTime,
	}
	journal.putPlan(Plan{ID: action.PlanID, Kind: kind, State: "ready", CurrentRevision: 1, CurrentDigest: action.PlanDigest})
	journal.putAction(action)
	return journal, action
}

func executionEffect(kind, target string) Effect {
	return Effect{Ordinal: 0, TargetKind: "file", TargetID: target, EffectKind: kind, State: EffectPending, Evidence: json.RawMessage(`{}`), ObservedAt: executionFixtureTime}
}

func mustAction(t *testing.T, journal Journal, id string) Action {
	t.Helper()
	action, err := journal.GetAction(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return action
}

func mustAttempts(t *testing.T, journal Journal, id string) []Attempt {
	t.Helper()
	attempts, err := journal.ListAttempts(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return attempts
}

func mustEffects(t *testing.T, journal Journal, id string) []Effect {
	t.Helper()
	effects, err := journal.ListEffects(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return effects
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type scriptedHandler struct {
	mu             sync.Mutex
	kind           domain.ActionKind
	reservation    []string
	observeFn      func(context.Context, Action, int) (Observation, error)
	dispatchFn     func(context.Context, Action, Attempt) (DispatchResult, error)
	reconcileFn    func(context.Context, Action, Attempt) (ReconcileResult, error)
	events         []string
	observeCount   int
	dispatchCount  int
	reconcileCount int
}

func (handler *scriptedHandler) Kind() domain.ActionKind { return handler.kind }

func (handler *scriptedHandler) Reservations(Action) []string {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return append([]string(nil), handler.reservation...)
}

func (handler *scriptedHandler) Observe(ctx context.Context, action Action) (Observation, error) {
	handler.mu.Lock()
	handler.observeCount++
	call := handler.observeCount
	handler.events = append(handler.events, "observe")
	fn := handler.observeFn
	handler.mu.Unlock()
	if fn == nil {
		return Observation{State: ObserveSatisfied}, nil
	}
	return fn(ctx, action, call)
}

func (handler *scriptedHandler) Dispatch(ctx context.Context, action Action, attempt Attempt) (DispatchResult, error) {
	handler.mu.Lock()
	handler.dispatchCount++
	handler.events = append(handler.events, "dispatch")
	fn := handler.dispatchFn
	handler.mu.Unlock()
	if fn == nil {
		return DispatchResult{Accepted: true, Outcome: domain.OutcomeApplied}, nil
	}
	return fn(ctx, action, attempt)
}

func (handler *scriptedHandler) Reconcile(ctx context.Context, action Action, attempt Attempt) (ReconcileResult, error) {
	handler.mu.Lock()
	handler.reconcileCount++
	handler.events = append(handler.events, "reconcile")
	fn := handler.reconcileFn
	handler.mu.Unlock()
	if fn == nil {
		return ReconcileResult{SafeToRetry: true}, nil
	}
	return fn(ctx, action, attempt)
}

func (handler *scriptedHandler) dispatchCalls() int {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.dispatchCount
}

func (handler *scriptedHandler) reconcileCalls() int {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.reconcileCount
}

func (handler *scriptedHandler) eventsSnapshot() []string {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return append([]string(nil), handler.events...)
}

type memoryJournal struct {
	mu               sync.Mutex
	actions          map[string]Action
	plans            map[string]Plan
	attempts         map[string][]Attempt
	effects          map[string][]Effect
	transactionCount int
}

func (journal *memoryJournal) putPlan(plan Plan) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.plans[plan.ID] = plan
}

func (journal *memoryJournal) putAction(action Action) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.actions[action.ID] = cloneAction(action)
}

func (journal *memoryJournal) putAttempt(attempt Attempt) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.attempts[attempt.ActionRunID] = append(journal.attempts[attempt.ActionRunID], cloneAttempt(attempt))
}

func (journal *memoryJournal) transactionCalls() int {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	return journal.transactionCount
}

func (journal *memoryJournal) GetAction(_ context.Context, id string) (Action, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	action, ok := journal.actions[id]
	if !ok {
		return Action{}, ErrNotFound
	}
	return cloneAction(action), nil
}

func (journal *memoryJournal) GetPlan(_ context.Context, id string) (Plan, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	plan, ok := journal.plans[id]
	if !ok {
		return Plan{}, ErrNotFound
	}
	return plan, nil
}

func (journal *memoryJournal) ListDue(_ context.Context, now string, limit int) ([]Action, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	current, err := parseTime(now)
	if err != nil {
		return nil, err
	}
	var actions []Action
	for _, action := range journal.actions {
		if action.State != domain.ActionQueued && action.State != domain.ActionWaitingDependency && action.State != domain.ActionReconciling {
			continue
		}
		if action.CancellationRequestedAt != "" || expired(action.DeadlineAt, current) {
			continue
		}
		if !memoryDue(action.NextAttemptAt, current) || !memoryLeaseAvailable(action, current) {
			continue
		}
		actions = append(actions, cloneAction(action))
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].ID < actions[j].ID })
	if limit > 0 && len(actions) > limit {
		actions = actions[:limit]
	}
	return actions, nil
}

func (journal *memoryJournal) ListReconciling(_ context.Context, now string, limit int) ([]Action, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	current, err := parseTime(now)
	if err != nil {
		return nil, err
	}
	var actions []Action
	for _, action := range journal.actions {
		if action.State != domain.ActionReconciling || !memoryLeaseAvailable(action, current) {
			continue
		}
		if action.CancellationRequestedAt == "" && !expired(action.DeadlineAt, current) && !memoryDue(action.NextAttemptAt, current) {
			continue
		}
		actions = append(actions, cloneAction(action))
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].ID < actions[j].ID })
	if limit > 0 && len(actions) > limit {
		actions = actions[:limit]
	}
	return actions, nil
}

func (journal *memoryJournal) Claim(_ context.Context, id string, version int64, workerID, leaseUntil, now string) (Action, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	action, ok := journal.actions[id]
	if !ok || action.Version != version || (action.State != domain.ActionQueued && action.State != domain.ActionWaitingDependency && action.State != domain.ActionReconciling) {
		return Action{}, sql.ErrNoRows
	}
	current, err := parseTime(now)
	if err != nil {
		return Action{}, err
	}
	if action.CancellationRequestedAt != "" || expired(action.DeadlineAt, current) || !memoryDue(action.NextAttemptAt, current) || !memoryLeaseAvailable(action, current) {
		return Action{}, sql.ErrNoRows
	}
	action.State = domain.ActionRunning
	action.ClaimedBy = workerID
	action.LeaseUntil = leaseUntil
	action.Version++
	action.UpdatedAt = now
	journal.actions[id] = cloneAction(action)
	return cloneAction(action), nil
}

func (journal *memoryJournal) RecoverRunning(_ context.Context, now string) ([]Action, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	var recovered []Action
	for id, action := range journal.actions {
		if action.State != domain.ActionRunning && !(action.State == domain.ActionReconciling && action.ClaimedBy != "") {
			continue
		}
		action.State = domain.ActionReconciling
		action.NextAttemptAt = now
		action.ClaimedBy = ""
		action.LeaseUntil = ""
		action.Version++
		action.UpdatedAt = now
		journal.actions[id] = cloneAction(action)
		recovered = append(recovered, cloneAction(action))
	}
	return recovered, nil
}

func (journal *memoryJournal) RecoverExpired(ctx context.Context, now string) ([]Action, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	current, err := parseTime(now)
	if err != nil {
		return nil, err
	}
	var recovered []Action
	for id, action := range journal.actions {
		if (action.State != domain.ActionRunning && !(action.State == domain.ActionReconciling && action.ClaimedBy != "")) || memoryLeaseAvailable(action, current) {
			continue
		}
		action.State = domain.ActionReconciling
		action.NextAttemptAt = now
		action.ClaimedBy = ""
		action.LeaseUntil = ""
		action.Version++
		action.UpdatedAt = now
		journal.actions[id] = cloneAction(action)
		recovered = append(recovered, cloneAction(action))
	}
	return recovered, nil
}

func (journal *memoryJournal) RecoverRunningAttempts(_ context.Context) ([]Attempt, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	var recovered []Attempt
	for id, attempts := range journal.attempts {
		for index := range attempts {
			if attempts[index].State != domain.AttemptRunning {
				continue
			}
			attempts[index].State = domain.AttemptReconciling
			if attempts[index].Phase == AttemptDispatch || attempts[index].OutcomeCertainty == CertaintyUncertain {
				attempts[index].OutcomeCertainty = CertaintyUncertain
			}
			recovered = append(recovered, cloneAttempt(attempts[index]))
		}
		journal.attempts[id] = attempts
	}
	return recovered, nil
}

func (journal *memoryJournal) FinalizeCancelled(_ context.Context, now string) ([]Action, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	var finalized []Action
	for id, action := range journal.actions {
		if (action.State != domain.ActionQueued && action.State != domain.ActionWaitingDependency) || action.CancellationRequestedAt == "" {
			continue
		}
		action.State = domain.ActionCancelled
		action.ClaimedBy = ""
		action.LeaseUntil = ""
		action.Version++
		action.UpdatedAt = now
		journal.actions[id] = cloneAction(action)
		finalized = append(finalized, cloneAction(action))
	}
	return finalized, nil
}

func (journal *memoryJournal) FinalizeDeadline(_ context.Context, now string) ([]Action, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	current, err := parseTime(now)
	if err != nil {
		return nil, err
	}
	var finalized []Action
	for id, action := range journal.actions {
		if (action.State != domain.ActionQueued && action.State != domain.ActionWaitingDependency) || action.CancellationRequestedAt != "" || !expired(action.DeadlineAt, current) {
			continue
		}
		action.State = domain.ActionDeadlineExceeded
		action.ClaimedBy = ""
		action.LeaseUntil = ""
		action.Version++
		action.UpdatedAt = now
		journal.actions[id] = cloneAction(action)
		finalized = append(finalized, cloneAction(action))
	}
	return finalized, nil
}

func (journal *memoryJournal) RequestCancellation(_ context.Context, id, requestedAt string) (Action, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	action, ok := journal.actions[id]
	if !ok {
		return Action{}, ErrNotFound
	}
	if action.State.Terminal() {
		return Action{}, sql.ErrNoRows
	}
	if action.CancellationRequestedAt == "" {
		action.CancellationRequestedAt = requestedAt
		action.Version++
		action.UpdatedAt = requestedAt
		journal.actions[id] = cloneAction(action)
	}
	return cloneAction(action), nil
}

func (journal *memoryJournal) ListAttempts(_ context.Context, id string) ([]Attempt, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	attempts := cloneAttempts(journal.attempts[id])
	sort.Slice(attempts, func(i, j int) bool { return attempts[i].AttemptNumber < attempts[j].AttemptNumber })
	return attempts, nil
}

func (journal *memoryJournal) CreateAttempt(_ context.Context, attempt Attempt) (Attempt, error) {
	if err := attempt.validate(); err != nil {
		return Attempt{}, err
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	for _, existing := range journal.attempts[attempt.ActionRunID] {
		if existing.ID == attempt.ID || existing.AttemptNumber == attempt.AttemptNumber {
			return Attempt{}, fmt.Errorf("duplicate attempt %s", attempt.ID)
		}
	}
	journal.attempts[attempt.ActionRunID] = append(journal.attempts[attempt.ActionRunID], cloneAttempt(attempt))
	return cloneAttempt(attempt), nil
}

func (journal *memoryJournal) UpdateAttempt(_ context.Context, attempt Attempt) (Attempt, error) {
	if err := attempt.validate(); err != nil {
		return Attempt{}, err
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	attempts := journal.attempts[attempt.ActionRunID]
	for index := range attempts {
		if attempts[index].ID != attempt.ID {
			continue
		}
		attempts[index] = cloneAttempt(attempt)
		journal.attempts[attempt.ActionRunID] = attempts
		return cloneAttempt(attempt), nil
	}
	return Attempt{}, ErrNotFound
}

func (journal *memoryJournal) ListEffects(_ context.Context, id string) ([]Effect, error) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	effects := cloneEffects(journal.effects[id])
	sort.Slice(effects, func(i, j int) bool { return effects[i].Ordinal < effects[j].Ordinal })
	return effects, nil
}

func (journal *memoryJournal) CreateEffect(_ context.Context, effect Effect) (Effect, error) {
	if err := effect.validate(); err != nil {
		return Effect{}, err
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	for _, existing := range journal.effects[effect.ActionRunID] {
		if existing.ID == effect.ID || existing.Ordinal == effect.Ordinal {
			return Effect{}, fmt.Errorf("duplicate effect %s", effect.ID)
		}
	}
	if effect.AttemptID != "" {
		found := false
		for _, attempt := range journal.attempts[effect.ActionRunID] {
			if attempt.ID == effect.AttemptID {
				found = true
				break
			}
		}
		if !found {
			return Effect{}, fmt.Errorf("effect attempt %s is not in action", effect.AttemptID)
		}
	}
	journal.effects[effect.ActionRunID] = append(journal.effects[effect.ActionRunID], cloneEffect(effect))
	return cloneEffect(effect), nil
}

func (journal *memoryJournal) UpdateEffect(_ context.Context, effect Effect) (Effect, error) {
	if err := effect.validate(); err != nil {
		return Effect{}, err
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	effects := journal.effects[effect.ActionRunID]
	for index := range effects {
		if effects[index].ID != effect.ID {
			continue
		}
		effects[index] = cloneEffect(effect)
		journal.effects[effect.ActionRunID] = effects
		return cloneEffect(effect), nil
	}
	return Effect{}, ErrNotFound
}

func (journal *memoryJournal) UpdateOutcome(_ context.Context, update OutcomeUpdate) (Action, error) {
	if update.ID == "" || update.Version <= 0 || !update.State.Valid() || !json.Valid(update.Outcome) {
		return Action{}, ErrInvalidJournal
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	action, ok := journal.actions[update.ID]
	if !ok || action.Version != update.Version {
		return Action{}, sql.ErrNoRows
	}
	action.State = update.State
	action.NextAttemptAt = update.NextAttemptAt
	action.ClaimedBy = update.ClaimedBy
	action.LeaseUntil = update.LeaseUntil
	action.Outcome = cloneRaw(update.Outcome)
	action.UnresolvedCount = update.UnresolvedCount
	action.Version++
	action.UpdatedAt = update.UpdatedAt
	journal.actions[update.ID] = cloneAction(action)
	return cloneAction(action), nil
}

func (journal *memoryJournal) InTx(_ context.Context, fn func(Journal) error) error {
	if fn == nil {
		return errors.New("transaction callback is required")
	}
	journal.mu.Lock()
	transaction := journal.cloneLocked()
	journal.mu.Unlock()
	if err := fn(transaction); err != nil {
		return err
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.actions = transaction.actions
	journal.plans = transaction.plans
	journal.attempts = transaction.attempts
	journal.effects = transaction.effects
	journal.transactionCount++
	return nil
}

func (journal *memoryJournal) cloneLocked() *memoryJournal {
	clone := &memoryJournal{actions: make(map[string]Action, len(journal.actions)), plans: make(map[string]Plan, len(journal.plans)), attempts: make(map[string][]Attempt, len(journal.attempts)), effects: make(map[string][]Effect, len(journal.effects))}
	for id, action := range journal.actions {
		clone.actions[id] = cloneAction(action)
	}
	for id, plan := range journal.plans {
		clone.plans[id] = plan
	}
	for id, attempts := range journal.attempts {
		clone.attempts[id] = cloneAttempts(attempts)
	}
	for id, effects := range journal.effects {
		clone.effects[id] = cloneEffects(effects)
	}
	return clone
}

func memoryDue(value string, now time.Time) bool {
	if value == "" {
		return true
	}
	parsed, err := parseTime(value)
	return err == nil && !parsed.After(now)
}

func memoryLeaseAvailable(action Action, now time.Time) bool {
	if action.ClaimedBy == "" || action.LeaseUntil == "" {
		return true
	}
	parsed, err := parseTime(action.LeaseUntil)
	return err == nil && !parsed.After(now)
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func cloneAction(action Action) Action {
	action.DesiredState = cloneRaw(action.DesiredState)
	action.Outcome = cloneRaw(action.Outcome)
	return action
}

func cloneAttempt(attempt Attempt) Attempt {
	attempt.Evidence = cloneRaw(attempt.Evidence)
	return attempt
}

func cloneAttempts(attempts []Attempt) []Attempt {
	cloned := make([]Attempt, len(attempts))
	for index, attempt := range attempts {
		cloned[index] = cloneAttempt(attempt)
	}
	return cloned
}

func cloneEffect(effect Effect) Effect {
	effect.Evidence = cloneRaw(effect.Evidence)
	return effect
}

func cloneEffects(effects []Effect) []Effect {
	cloned := make([]Effect, len(effects))
	for index, effect := range effects {
		cloned[index] = cloneEffect(effect)
	}
	return cloned
}

var _ Journal = (*memoryJournal)(nil)
var _ TransactionalJournal = (*memoryJournal)(nil)
var _ Handler = (*scriptedHandler)(nil)
