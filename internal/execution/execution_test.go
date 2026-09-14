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
			return ReconcileResult{SafeToRetry: true, Evidence: []string{"target_absent"}, Effects: []Effect{effect}}, nil
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

func TestMemoryRecoverExpiredOnlyReclaimsExpiredLeases(t *testing.T) {
	journal, expiredAction := newMemoryAction("expired-lease", domain.ActionFSCopy, domain.ActionRunning)
	expiredAction.ClaimedBy = "old-worker"
	expiredAction.LeaseUntil = "2026-09-14T11:59:00Z"
	expiredAction.Version = 2
	journal.putAction(expiredAction)
	_, activeAction := newMemoryActionOnJournal(journal, "active-lease", domain.ActionFSCopy, domain.ActionRunning)
	activeAction.ClaimedBy = "active-worker"
	activeAction.LeaseUntil = "2026-09-14T12:01:00Z"
	activeAction.Version = 2
	journal.putAction(activeAction)

	recovered, err := journal.RecoverExpired(context.Background(), executionFixtureTime)
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].ID != expiredAction.ID {
		t.Fatalf("recovered = %+v, want only expired lease", recovered)
	}
	if current := mustAction(t, journal, expiredAction.ID); current.State != domain.ActionReconciling || current.ClaimedBy != "" {
		t.Fatalf("expired action = %+v, want unleased reconciliation", current)
	}
	if current := mustAction(t, journal, activeAction.ID); current.State != domain.ActionRunning || current.ClaimedBy != activeAction.ClaimedBy {
		t.Fatalf("active action = %+v, want active lease preserved", current)
	}
}

func TestNewRejectsNonTransactionalJournal(t *testing.T) {
	journal, _ := newMemoryAction("non-transactional", domain.ActionFSCopy, domain.ActionQueued)
	_, err := New(journalWithoutTransactions{Journal: journal}, Options{})
	if !errors.Is(err, ErrInvalidJournal) {
		t.Fatalf("New error = %v, want invalid journal", err)
	}
}

func TestDispatchIntentTransactionRollsBackBeforeHandler(t *testing.T) {
	journal, action := newMemoryAction("dispatch-rollback", domain.ActionFSCopy, domain.ActionQueued)
	journal.failCreateEffect = true
	handler := &scriptedHandler{
		kind: domain.ActionFSCopy,
		observeFn: func(_ context.Context, _ Action, _ int) (Observation, error) {
			return Observation{State: ObserveNeedsAction, Effects: []Effect{executionEffect("copy", "payload.bin")}}, nil
		},
		dispatchFn: func(_ context.Context, _ Action, _ Attempt) (DispatchResult, error) {
			t.Fatal("dispatch called after intent transaction failure")
			return DispatchResult{}, nil
		},
	}
	result := newTestExecutor(t, journal, handler, executionClock()).RunAction(context.Background(), action.ID)
	if result.Err == nil {
		t.Fatal("dispatch rollback result = nil error, want transaction failure")
	}
	if handler.dispatchCalls() != 0 {
		t.Fatalf("dispatch calls = %d, want zero", handler.dispatchCalls())
	}
	if attempts := mustAttempts(t, journal, action.ID); len(attempts) != 1 || attempts[0].Phase != AttemptObserve || attempts[0].State != domain.AttemptRunning {
		t.Fatalf("attempts after rollback = %+v, want only the running observe attempt", attempts)
	}
	if effects := mustEffects(t, journal, action.ID); len(effects) != 0 {
		t.Fatalf("effects after rollback = %+v, want no persisted effects", effects)
	}
}

func TestDurableCancelCancelsInFlightHandler(t *testing.T) {
	journal, action := newMemoryAction("cancel-in-flight", domain.ActionFSCopy, domain.ActionQueued)
	started := make(chan struct{})
	observedCancel := make(chan struct{})
	var startOnce, cancelOnce sync.Once
	effect := executionEffect("copy", "payload.bin")
	handler := &scriptedHandler{
		kind: domain.ActionFSCopy,
		observeFn: func(_ context.Context, _ Action, _ int) (Observation, error) {
			return Observation{State: ObserveNeedsAction, Effects: []Effect{effect}}, nil
		},
		dispatchFn: func(ctx context.Context, _ Action, _ Attempt) (DispatchResult, error) {
			startOnce.Do(func() { close(started) })
			<-ctx.Done()
			cancelOnce.Do(func() { close(observedCancel) })
			return DispatchResult{}, ctx.Err()
		},
	}
	executor := newTestExecutor(t, journal, handler, executionClock())
	done := make(chan Result, 1)
	go func() { done <- executor.RunAction(context.Background(), action.ID) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("dispatch did not start")
	}
	if _, err := executor.Cancel(context.Background(), action.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-observedCancel:
	case <-time.After(2 * time.Second):
		t.Fatal("durable cancellation did not reach handler context")
	}
	select {
	case result := <-done:
		if result.State != domain.ActionCancelled {
			t.Fatalf("cancelled result = %+v, want cancelled", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight action did not finish after cancellation")
	}
	if current := mustAction(t, journal, action.ID); current.State != domain.ActionCancelled {
		t.Fatalf("persisted action = %+v, want cancelled", current)
	}
}

func TestUnresolvedReservationPersistsAcrossExecutorRestart(t *testing.T) {
	journal, first := newMemoryAction("reservation-persistent-first", domain.ActionFSCopy, domain.ActionQueued)
	_, second := newMemoryActionOnJournal(journal, "reservation-persistent-second", domain.ActionFSCopy, domain.ActionQueued)
	effect := executionEffect("copy", "payload.bin")
	firstHandler := &scriptedHandler{
		kind:        domain.ActionFSCopy,
		reservation: []string{"root:library/show"},
		observeFn: func(_ context.Context, _ Action, _ int) (Observation, error) {
			return Observation{State: ObserveNeedsAction, Effects: []Effect{effect}}, nil
		},
		dispatchFn: func(_ context.Context, _ Action, _ Attempt) (DispatchResult, error) {
			return DispatchResult{}, NewDispatchedFailure(FailureUncertain, errors.New("response lost"))
		},
	}
	firstExecutor := newTestExecutor(t, journal, firstHandler, executionClock())
	firstResult := firstExecutor.RunAction(context.Background(), first.ID)
	if firstResult.State != domain.ActionReconciling {
		t.Fatalf("first result = %+v, want unresolved reconciliation", firstResult)
	}
	current := mustAction(t, journal, first.ID)
	keys, err := reservationKeysFromOutcome(current.Outcome)
	if err != nil || !equalStrings(keys, []string{"root:library/show"}) {
		t.Fatalf("persisted reservation keys = %v (%v), want first action key", keys, err)
	}

	secondHandler := &scriptedHandler{
		kind:        domain.ActionFSCopy,
		reservation: []string{"root:library/show/file.mkv"},
		observeFn: func(_ context.Context, _ Action, _ int) (Observation, error) {
			return Observation{State: ObserveNeedsAction, Effects: []Effect{executionEffect("copy", "other.bin")}}, nil
		},
		dispatchFn: func(_ context.Context, _ Action, _ Attempt) (DispatchResult, error) {
			return DispatchResult{Accepted: true, Outcome: domain.OutcomeApplied}, nil
		},
	}
	// A fresh executor has no process-local reservation map. The durable
	// action outcome still prevents a descendant reservation from dispatching.
	secondExecutor := newTestExecutor(t, journal, secondHandler, executionClock())
	secondResult := secondExecutor.RunAction(context.Background(), second.ID)
	if secondResult.State != domain.ActionWaitingDependency {
		t.Fatalf("overlapping restart result = %+v, want waiting dependency", secondResult)
	}
	if secondHandler.dispatchCalls() != 0 {
		t.Fatalf("overlapping dispatch calls = %d, want zero", secondHandler.dispatchCalls())
	}
}

func TestReadBackRejectsPartialAndChangedEffectSets(t *testing.T) {
	tests := []struct {
		name     string
		readBack []Effect
	}{
		{
			name:     "omitted target",
			readBack: []Effect{executionEffect("copy", "one.bin")},
		},
		{
			name: "changed target",
			readBack: []Effect{{Ordinal: 0, TargetKind: "file", TargetID: "different.bin", EffectKind: "copy", State: EffectApplied, Evidence: json.RawMessage(`{}`), ObservedAt: executionFixtureTime},
				{Ordinal: 1, TargetKind: "file", TargetID: "two.bin", EffectKind: "copy", State: EffectApplied, Evidence: json.RawMessage(`{}`), ObservedAt: executionFixtureTime}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			journal, action := newMemoryAction("effect-set-"+test.name, domain.ActionFSCopy, domain.ActionQueued)
			first := executionEffect("copy", "one.bin")
			second := executionEffect("copy", "two.bin")
			second.Ordinal = 1
			handler := &scriptedHandler{
				kind: domain.ActionFSCopy,
				observeFn: func(_ context.Context, _ Action, call int) (Observation, error) {
					if call == 1 {
						return Observation{State: ObserveNeedsAction, Effects: []Effect{first, second}}, nil
					}
					return Observation{State: ObserveSatisfied, Effects: test.readBack}, nil
				},
				dispatchFn: func(_ context.Context, _ Action, _ Attempt) (DispatchResult, error) {
					return DispatchResult{Accepted: true, Outcome: domain.OutcomeApplied}, nil
				},
			}
			result := mustRunOnce(t, newTestExecutor(t, journal, handler, executionClock()))
			if len(result.Results) != 1 || result.Results[0].State != domain.ActionReconciling {
				t.Fatalf("result = %+v, want unresolved reconciliation", result.Results)
			}
			if current := mustAction(t, journal, action.ID); current.State != domain.ActionReconciling {
				t.Fatalf("persisted action = %+v, want reconciling", current)
			}
		})
	}
}

func TestReconciliationRejectsChangedEffectIdentity(t *testing.T) {
	journal, action := newMemoryAction("reconcile-effect-drift", domain.ActionFSCopy, domain.ActionQueued)
	expected := executionEffect("copy", "payload.bin")
	handler := &scriptedHandler{
		kind: domain.ActionFSCopy,
		observeFn: func(_ context.Context, _ Action, _ int) (Observation, error) {
			return Observation{State: ObserveNeedsAction, Effects: []Effect{expected}}, nil
		},
		dispatchFn: func(_ context.Context, _ Action, _ Attempt) (DispatchResult, error) {
			return DispatchResult{}, NewDispatchedFailure(FailureUncertain, errors.New("response lost"))
		},
		reconcileFn: func(_ context.Context, _ Action, _ Attempt) (ReconcileResult, error) {
			return ReconcileResult{Outcome: domain.OutcomeApplied, Effects: []Effect{{Ordinal: 0, TargetKind: "file", TargetID: "other.bin", EffectKind: "copy", State: EffectApplied, Evidence: json.RawMessage(`{}`), ObservedAt: executionFixtureTime}}, Evidence: []string{"drift"}}, nil
		},
	}
	executor := newTestExecutor(t, journal, handler, executionClock())
	first := mustRunOnce(t, executor)
	if first.Results[0].State != domain.ActionReconciling {
		t.Fatalf("first result = %+v, want reconciliation", first.Results)
	}
	clockNow := executionTime().Add(10 * time.Second)
	restarted := newTestExecutor(t, journal, handler, func() time.Time { return clockNow })
	second := mustRunOnce(t, restarted)
	if len(second.Results) != 1 || second.Results[0].State != domain.ActionNeedsReview {
		t.Fatalf("reconciliation drift result = %+v, want needs review", second.Results)
	}
	if current := mustAction(t, journal, action.ID); current.State != domain.ActionNeedsReview {
		t.Fatalf("persisted action = %+v, want needs review", current)
	}
}

func TestValidateEffectsRejectsDuplicateTargetIdentity(t *testing.T) {
	first := executionEffect("copy", "payload.bin")
	second := executionEffect("copy", "payload.bin")
	second.Ordinal = 1
	if err := (Observation{State: ObserveNeedsAction, Effects: []Effect{first, second}}).validate("duplicate-effects"); !errors.Is(err, ErrInvalidJournal) {
		t.Fatalf("duplicate validation error = %v, want invalid journal", err)
	}
}

func TestSQLStaleWorkerCannotFinalizeRecoveredLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale-worker.sqlite")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	created := "2026-09-14T11:00:00Z"
	planID, actionID, digest := "stale-plan", "stale-action", "stale-digest"
	if _, err := store.Queries().CreateActionPlan(ctx, &sqlc.CreateActionPlanParams{ID: planID, Kind: string(domain.ActionFSCopy), State: "ready", CurrentRevision: 1, CurrentDigest: digest, CreatedAt: created, UpdatedAt: created}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionPlanRevision(ctx, &sqlc.CreateActionPlanRevisionParams{PlanID: planID, Revision: 1, Digest: digest, State: "ready", InputJson: `{}`, PreconditionsJson: `{}`, CapabilitiesJson: `[]`, ManifestJson: `[]`, CreatedAt: created, ExpiresAt: "2026-09-20T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionRun(ctx, &sqlc.CreateActionRunParams{ID: actionID, PlanID: planID, PlanRevision: 1, PlanDigest: digest, State: string(domain.ActionQueued), DesiredStateJson: `{}`, Version: 1, OutcomeJson: `{}`, CreatedAt: created, UpdatedAt: created}); err != nil {
		t.Fatal(err)
	}
	journal, err := NewSQLJournal(store)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	handler := &scriptedHandler{
		kind: domain.ActionFSCopy,
		observeFn: func(_ context.Context, _ Action, _ int) (Observation, error) {
			return Observation{State: ObserveNeedsAction}, nil
		},
		dispatchFn: func(_ context.Context, _ Action, _ Attempt) (DispatchResult, error) {
			once.Do(func() { close(started) })
			<-release
			return DispatchResult{Accepted: true, Outcome: domain.OutcomeApplied}, nil
		},
	}
	clock := executionClock()
	worker, err := New(journal, Options{WorkerID: "stale-worker", Now: clock, LeaseDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.RegisterHandler(handler); err != nil {
		t.Fatal(err)
	}
	resultDone := make(chan Result, 1)
	go func() { resultDone <- worker.RunAction(ctx, actionID) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("stale worker dispatch did not start")
	}
	recovered, err := journal.RecoverExpired(ctx, "2026-09-14T12:00:02Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 1 || recovered[0].State != domain.ActionReconciling {
		t.Fatalf("recovered = %+v, want one reconciling action", recovered)
	}
	close(release)
	select {
	case result := <-resultDone:
		if !errors.Is(result.Err, ErrLeaseLost) {
			t.Fatalf("stale worker result = %+v, want lease loss", result)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stale worker did not finish")
	}
	current, err := journal.GetAction(ctx, actionID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != domain.ActionReconciling || current.ClaimedBy != "" {
		t.Fatalf("recovered action after stale completion = %+v, want untouched reconciliation", current)
	}
}

func TestSQLReadBackRejectsPartialEffectSet(t *testing.T) {
	store, journal, action := newSQLExecutionFixture(t, "sql-effect-set")
	defer store.Close()
	first := executionEffect("copy", "one.bin")
	second := executionEffect("copy", "two.bin")
	second.Ordinal = 1
	handler := &scriptedHandler{
		kind: domain.ActionFSCopy,
		observeFn: func(_ context.Context, _ Action, call int) (Observation, error) {
			if call == 1 {
				return Observation{State: ObserveNeedsAction, Effects: []Effect{first, second}}, nil
			}
			return Observation{State: ObserveSatisfied, Effects: []Effect{first}}, nil
		},
		dispatchFn: func(_ context.Context, _ Action, _ Attempt) (DispatchResult, error) {
			return DispatchResult{Accepted: true, Outcome: domain.OutcomeApplied}, nil
		},
	}
	executor, err := New(journal, Options{WorkerID: "sql-effect-worker", Now: executionClock(), LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.RegisterHandler(handler); err != nil {
		t.Fatal(err)
	}
	result := executor.RunAction(context.Background(), action.ID)
	if result.State != domain.ActionReconciling {
		t.Fatalf("SQL partial read-back result = %+v, want reconciling", result)
	}
	current, err := journal.GetAction(context.Background(), action.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != domain.ActionReconciling || current.UnresolvedCount != 1 {
		t.Fatalf("SQL partial read-back action = %+v, want unresolved reconciliation", current)
	}
}

func TestSQLReconciliationRejectsChangedEffectIdentity(t *testing.T) {
	store, journal, action := newSQLExecutionFixture(t, "sql-reconcile-drift")
	defer store.Close()
	expected := executionEffect("copy", "payload.bin")
	handler := &scriptedHandler{
		kind: domain.ActionFSCopy,
		observeFn: func(_ context.Context, _ Action, _ int) (Observation, error) {
			return Observation{State: ObserveNeedsAction, Effects: []Effect{expected}}, nil
		},
		dispatchFn: func(_ context.Context, _ Action, _ Attempt) (DispatchResult, error) {
			return DispatchResult{}, NewDispatchedFailure(FailureUncertain, errors.New("response lost"))
		},
		reconcileFn: func(_ context.Context, _ Action, _ Attempt) (ReconcileResult, error) {
			return ReconcileResult{Outcome: domain.OutcomeApplied, Effects: []Effect{{Ordinal: 0, TargetKind: "file", TargetID: "other.bin", EffectKind: "copy", State: EffectApplied, Evidence: json.RawMessage(`{}`), ObservedAt: executionFixtureTime}}}, nil
		},
	}
	executor, err := New(journal, Options{WorkerID: "sql-reconcile-worker", Now: executionClock(), LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := executor.RegisterHandler(handler); err != nil {
		t.Fatal(err)
	}
	first := executor.RunAction(context.Background(), action.ID)
	if first.State != domain.ActionReconciling {
		t.Fatalf("SQL uncertain result = %+v, want reconciliation", first)
	}
	clockNow := executionTime().Add(10 * time.Second)
	restarted, err := New(journal, Options{WorkerID: "sql-reconcile-restart", Now: func() time.Time { return clockNow }, LeaseDuration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.RegisterHandler(handler); err != nil {
		t.Fatal(err)
	}
	second := restarted.RunAction(context.Background(), action.ID)
	if second.State != domain.ActionNeedsReview {
		t.Fatalf("SQL reconciliation drift result = %+v, want needs review", second)
	}
	current, err := journal.GetAction(context.Background(), action.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State != domain.ActionNeedsReview {
		t.Fatalf("SQL reconciliation drift action = %+v, want needs review", current)
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

func newSQLExecutionFixture(t *testing.T, actionID string) (*storage.Store, *SQLJournal, Action) {
	t.Helper()
	path := filepath.Join(t.TempDir(), actionID+".sqlite")
	store, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	created := executionFixtureTime
	planID := actionID + "-plan"
	digest := actionID + "-digest"
	ctx := context.Background()
	if _, err := store.Queries().CreateActionPlan(ctx, &sqlc.CreateActionPlanParams{ID: planID, Kind: string(domain.ActionFSCopy), State: "ready", CurrentRevision: 1, CurrentDigest: digest, CreatedAt: created, UpdatedAt: created}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionPlanRevision(ctx, &sqlc.CreateActionPlanRevisionParams{PlanID: planID, Revision: 1, Digest: digest, State: "ready", InputJson: `{}`, PreconditionsJson: `{}`, CapabilitiesJson: `[]`, ManifestJson: `[]`, CreatedAt: created, ExpiresAt: "2026-09-20T00:00:00Z"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := store.Queries().CreateActionRun(ctx, &sqlc.CreateActionRunParams{ID: actionID, PlanID: planID, PlanRevision: 1, PlanDigest: digest, State: string(domain.ActionQueued), DesiredStateJson: `{}`, Version: 1, OutcomeJson: `{}`, CreatedAt: created, UpdatedAt: created}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	journal, err := NewSQLJournal(store)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	action, err := journal.GetAction(ctx, actionID)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store, journal, action
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

type journalWithoutTransactions struct {
	Journal
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
	revision         uint64
	baseRevision     uint64
	failCreateEffect bool
}

func (journal *memoryJournal) putPlan(plan Plan) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.plans[plan.ID] = plan
	journal.revision++
}

func (journal *memoryJournal) putAction(action Action) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.actions[action.ID] = cloneAction(action)
	journal.revision++
}

func (journal *memoryJournal) putAttempt(attempt Attempt) {
	journal.mu.Lock()
	defer journal.mu.Unlock()
	journal.attempts[attempt.ActionRunID] = append(journal.attempts[attempt.ActionRunID], cloneAttempt(attempt))
	journal.revision++
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
	journal.revision++
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
		journal.revision++
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
		if (action.State != domain.ActionRunning && !(action.State == domain.ActionReconciling && action.ClaimedBy != "")) || action.ClaimedBy == "" || action.LeaseUntil == "" || !expired(action.LeaseUntil, current) {
			continue
		}
		action.State = domain.ActionReconciling
		action.NextAttemptAt = now
		action.ClaimedBy = ""
		action.LeaseUntil = ""
		action.Version++
		action.UpdatedAt = now
		journal.actions[id] = cloneAction(action)
		journal.revision++
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
		journal.revision++
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
		journal.revision++
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
		journal.revision++
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
		journal.revision++
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
	journal.revision++
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
		journal.revision++
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
	if journal.failCreateEffect {
		return Effect{}, errors.New("injected effect journal failure")
	}
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
	journal.revision++
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
		journal.revision++
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
	preserved, err := outcomePreservingReservations(action.Outcome, update.Outcome, update.State)
	if err != nil {
		return Action{}, err
	}
	action.State = update.State
	action.NextAttemptAt = update.NextAttemptAt
	action.ClaimedBy = update.ClaimedBy
	action.LeaseUntil = update.LeaseUntil
	action.Outcome = cloneRaw(preserved)
	action.UnresolvedCount = update.UnresolvedCount
	action.Version++
	action.UpdatedAt = update.UpdatedAt
	journal.actions[update.ID] = cloneAction(action)
	journal.revision++
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
	if journal.revision != transaction.baseRevision {
		return ErrLeaseLost
	}
	journal.actions = transaction.actions
	journal.plans = transaction.plans
	journal.attempts = transaction.attempts
	journal.effects = transaction.effects
	journal.revision++
	journal.transactionCount++
	return nil
}

func (journal *memoryJournal) InTxClaimed(_ context.Context, fence ClaimFence, fn func(Journal) error) error {
	if fn == nil {
		return errors.New("claimed transaction callback is required")
	}
	journal.mu.Lock()
	transaction := journal.cloneLocked()
	journal.mu.Unlock()
	if err := fn(transaction); err != nil {
		return err
	}
	journal.mu.Lock()
	defer journal.mu.Unlock()
	if journal.revision != transaction.baseRevision {
		return ErrLeaseLost
	}
	current, ok := journal.actions[fence.ActionID]
	if !ok || !claimFenceMatches(current, fence) {
		return ErrLeaseLost
	}
	journal.actions = transaction.actions
	journal.plans = transaction.plans
	journal.attempts = transaction.attempts
	journal.effects = transaction.effects
	journal.revision++
	journal.transactionCount++
	return nil
}

func (journal *memoryJournal) Reserve(_ context.Context, actionID string, keys []string) error {
	keys = canonicalReservationKeys(keys)
	var own Action
	ownFound := false
	for id, action := range journal.actions {
		if action.State != domain.ActionRunning && action.State != domain.ActionReconciling && action.State != domain.ActionNeedsReview {
			continue
		}
		candidate, err := reservationKeysFromOutcome(action.Outcome)
		if err != nil {
			return err
		}
		if id == actionID {
			own = action
			ownFound = true
			continue
		}
		for _, requested := range keys {
			for _, held := range candidate {
				if reservationConflicts(requested, held) {
					return fmt.Errorf("%w: %s", ErrReservationConflict, requested)
				}
			}
		}
	}
	if !ownFound {
		return ErrNotFound
	}
	existing, err := reservationKeysFromOutcome(own.Outcome)
	if err != nil {
		return err
	}
	merged, err := outcomeWithReservations(own.Outcome, append(existing, keys...))
	if err != nil {
		return err
	}
	own.Outcome = merged
	journal.actions[actionID] = cloneAction(own)
	journal.revision++
	return nil
}

func (journal *memoryJournal) cloneLocked() *memoryJournal {
	clone := &memoryJournal{actions: make(map[string]Action, len(journal.actions)), plans: make(map[string]Plan, len(journal.plans)), attempts: make(map[string][]Attempt, len(journal.attempts)), effects: make(map[string][]Effect, len(journal.effects)), revision: journal.revision, baseRevision: journal.revision, failCreateEffect: journal.failCreateEffect}
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
var _ FencedTransactionalJournal = (*memoryJournal)(nil)
var _ ReservationJournal = (*memoryJournal)(nil)
var _ Handler = (*scriptedHandler)(nil)
