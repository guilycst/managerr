package scanning

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
)

const (
	testRootA    domain.ConfigID  = "downloads-a"
	testRootB    domain.ConfigID  = "downloads-b"
	testSourceID domain.RuntimeID = "00000000-0000-4000-8000-000000000001"
)

type testClock struct{ nanos atomic.Int64 }

func newTestClock() *testClock {
	clock := &testClock{}
	clock.nanos.Store(time.Date(2030, time.January, 1, 12, 0, 0, 0, time.UTC).UnixNano())
	return clock
}

func (clock *testClock) Now() time.Time {
	return time.Unix(0, clock.nanos.Load()).UTC()
}

func (clock *testClock) Advance(amount time.Duration) { clock.nanos.Add(amount.Nanoseconds()) }

type runnerCall struct {
	root       RootSchedule
	checkpoint ScanCheckpoint
}

type scriptedRunner struct {
	mu        sync.Mutex
	calls     []runnerCall
	starts    chan runnerCall
	active    atomic.Int32
	maxActive atomic.Int32
	scripts   []func(context.Context, RootSchedule, ScanCheckpoint, ProgressFunc) (ScanResult, error)
}

func newScriptedRunner(scripts ...func(context.Context, RootSchedule, ScanCheckpoint, ProgressFunc) (ScanResult, error)) *scriptedRunner {
	return &scriptedRunner{starts: make(chan runnerCall, 16), scripts: scripts}
}

func (runner *scriptedRunner) Run(ctx context.Context, root RootSchedule, checkpoint ScanCheckpoint, progress ProgressFunc) (ScanResult, error) {
	call := runnerCall{root: root, checkpoint: checkpoint}
	runner.mu.Lock()
	index := len(runner.calls)
	runner.calls = append(runner.calls, call)
	runner.mu.Unlock()
	runner.starts <- call
	active := runner.active.Add(1)
	for {
		old := runner.maxActive.Load()
		if active <= old || runner.maxActive.CompareAndSwap(old, active) {
			break
		}
	}
	defer runner.active.Add(-1)
	if index < len(runner.scripts) {
		return runner.scripts[index](ctx, root, checkpoint, progress)
	}
	return ScanResult{Coverage: completeCoverage(root.ID, time.Now().UTC()), SourceCoverage: []domain.Coverage{completeSourceCoverage(root.ID, time.Now().UTC())}}, nil
}

func (runner *scriptedRunner) callCount() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return len(runner.calls)
}

func newTestScheduler(t *testing.T, store StateStore, runner ScanRunner, clock *testClock) *Scheduler {
	t.Helper()
	options := DefaultOptions()
	options.Now = clock.Now
	options.MinimumInterval = time.Nanosecond
	options.PollInterval = time.Hour
	options.RunTimeout = time.Minute
	options.RetryBackoff = time.Minute
	scheduler, err := New(store, runner, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheduler.Close() })
	return scheduler
}

func disabledSchedule(id domain.ConfigID) RootSchedule {
	return RootSchedule{ID: id, Revision: "rev-1", Interval: time.Minute}
}

func enabledSchedule(id domain.ConfigID, interval time.Duration) RootSchedule {
	return RootSchedule{ID: id, Revision: "rev-1", Enabled: true, Interval: interval}
}

func completeCoverage(rootID domain.ConfigID, observedAt time.Time) domain.Coverage {
	started := observedAt.Add(-time.Second)
	completed := observedAt
	return domain.Coverage{
		RootID: rootID, Completeness: domain.CompletenessComplete,
		ObservedCount: 1, StartedAt: &started, CompletedAt: &completed, ObservedAt: observedAt,
	}
}

func completeSourceCoverage(rootID domain.ConfigID, observedAt time.Time) domain.Coverage {
	coverage := completeCoverage(rootID, observedAt)
	coverage.SourceID = testSourceID
	return coverage
}

func partialCoverage(rootID domain.ConfigID, observedAt time.Time, reason string) domain.Coverage {
	coverage := completeCoverage(rootID, observedAt)
	coverage.Completeness = domain.CompletenessPartial
	coverage.ReasonCodes = []string{reason}
	return coverage
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func scanForRoot(snapshot Snapshot, rootID domain.ConfigID) []ScanRecord {
	result := make([]ScanRecord, 0)
	for _, scan := range snapshot.Scans {
		if scan.RootID == rootID {
			result = append(result, scan)
		}
	}
	return result
}

func blockingScript(release <-chan struct{}, result ScanResult) func(context.Context, RootSchedule, ScanCheckpoint, ProgressFunc) (ScanResult, error) {
	return func(ctx context.Context, _ RootSchedule, _ ScanCheckpoint, _ ProgressFunc) (ScanResult, error) {
		select {
		case <-release:
			return result, nil
		case <-ctx.Done():
			return ScanResult{}, ctx.Err()
		}
	}
}

func TestSchedulerManualTriggersCoalesceOneFollowUp(t *testing.T) {
	clock := newTestClock()
	firstRelease := make(chan struct{})
	secondRelease := make(chan struct{})
	runner := newScriptedRunner(
		blockingScript(firstRelease, ScanResult{Coverage: completeCoverage(testRootA, clock.Now()), SourceCoverage: []domain.Coverage{completeSourceCoverage(testRootA, clock.Now())}}),
		blockingScript(secondRelease, ScanResult{Coverage: completeCoverage(testRootA, clock.Now()), SourceCoverage: []domain.Coverage{completeSourceCoverage(testRootA, clock.Now())}}),
	)
	scheduler := newTestScheduler(t, NewMemoryStore(), runner, clock)
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{disabledSchedule(testRootA)}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := scheduler.Trigger(context.Background(), testRootA)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Scan.ID.Valid() {
		t.Fatal("manual trigger did not return a durable scan id")
	}
	<-runner.starts
	second, err := scheduler.Trigger(context.Background(), testRootA)
	if err != nil {
		t.Fatal(err)
	}
	third, err := scheduler.Trigger(context.Background(), testRootA)
	if err != nil {
		t.Fatal(err)
	}
	if second.Coalesced || third.Coalesced || !second.FollowUpPending || !third.FollowUpPending {
		t.Fatalf("running trigger coalescing mismatch: second=%+v third=%+v", second, third)
	}
	if second.Scan.ID != first.Scan.ID || third.Scan.ID != first.Scan.ID {
		t.Fatal("coalesced triggers returned different active scan ids")
	}
	close(firstRelease)
	var secondCall runnerCall
	waitFor(t, func() bool {
		select {
		case secondCall = <-runner.starts:
			return true
		default:
			return false
		}
	})
	if secondCall.checkpoint.ScanID == first.Scan.ID {
		t.Fatal("follow-up reused the completed scan id")
	}
	close(secondRelease)
	waitFor(t, func() bool {
		scans := scanForRoot(scheduler.Snapshot(), testRootA)
		if len(scans) != 2 {
			return false
		}
		for _, scan := range scans {
			if !scan.State.Terminal() {
				return false
			}
		}
		return true
	})
	root, ok := scheduler.Root(testRootA)
	if !ok || root.FollowUpPending || root.ActiveScanID != "" {
		t.Fatalf("root retained active/follow-up state: %+v", root)
	}
	if got := runner.callCount(); got != 2 {
		t.Fatalf("got %d runner calls, want exactly one follow-up", got)
	}
}

func TestSchedulerBoundsConcurrentRunsPerConfiguredLimit(t *testing.T) {
	clock := newTestClock()
	firstRelease := make(chan struct{})
	secondRelease := make(chan struct{})
	runner := newScriptedRunner(
		blockingScript(firstRelease, ScanResult{Coverage: partialCoverage(testRootA, clock.Now(), "test"), SourceCoverage: []domain.Coverage{partialCoverage(testRootA, clock.Now(), "test")}}),
		blockingScript(secondRelease, ScanResult{Coverage: partialCoverage(testRootB, clock.Now(), "test"), SourceCoverage: []domain.Coverage{partialCoverage(testRootB, clock.Now(), "test")}}),
	)
	scheduler := newTestScheduler(t, NewMemoryStore(), runner, clock)
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{disabledSchedule(testRootA), disabledSchedule(testRootB)}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, err := scheduler.Trigger(context.Background(), testRootA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Trigger(context.Background(), testRootB); err != nil {
		t.Fatal(err)
	}
	<-runner.starts
	time.Sleep(20 * time.Millisecond)
	if got := runner.callCount(); got != 1 {
		t.Fatalf("runner exceeded default concurrency bound: got %d calls", got)
	}
	if got := runner.maxActive.Load(); got > 1 {
		t.Fatalf("runner observed %d concurrent calls", got)
	}
	close(firstRelease)
	<-runner.starts
	close(secondRelease)
	waitFor(t, func() bool { return runner.callCount() == 2 && len(scheduler.Snapshot().Scans) == 2 })
}

func TestSchedulerCancellationPersistsAndCannotAssertAbsence(t *testing.T) {
	clock := newTestClock()
	release := make(chan struct{})
	runner := newScriptedRunner(blockingScript(release, ScanResult{Coverage: completeCoverage(testRootA, clock.Now()), SourceCoverage: []domain.Coverage{completeSourceCoverage(testRootA, clock.Now())}}))
	scheduler := newTestScheduler(t, NewMemoryStore(), runner, clock)
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{disabledSchedule(testRootA)}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.Trigger(context.Background(), testRootA)
	if err != nil {
		t.Fatal(err)
	}
	<-runner.starts
	if err := scheduler.Cancel(context.Background(), result.Scan.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		scan, ok := scheduler.Scan(result.Scan.ID)
		return ok && scan.State == StateCancelled
	})
	scan, _ := scheduler.Scan(result.Scan.ID)
	if scan.CancellationRequestedAt == nil || scan.CanAssertAbsence() {
		t.Fatalf("cancelled scan exposed invalid evidence: %+v", scan)
	}
	root, ok := scheduler.Root(testRootA)
	if !ok || root.ActiveScanID != "" {
		t.Fatalf("cancelled scan remained active: %+v", root)
	}
}

func TestSchedulerPersistsCursorAcrossRetryAndRestart(t *testing.T) {
	clock := newTestClock()
	store := NewMemoryStore()
	firstRunner := newScriptedRunner(func(_ context.Context, root RootSchedule, _ ScanCheckpoint, progress ProgressFunc) (ScanResult, error) {
		coverage := partialCoverage(root.ID, clock.Now(), "page_interrupted")
		if err := progress(ScanProgress{Cursor: "page-1", ObservedCount: 10, Coverage: coverage, SourceCoverage: []domain.Coverage{coverage}}); err != nil {
			return ScanResult{}, err
		}
		return ScanResult{Cursor: "page-1", ObservedCount: 10, Coverage: coverage, SourceCoverage: []domain.Coverage{coverage}}, ErrRetryable
	})
	scheduler := newTestScheduler(t, store, firstRunner, clock)
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{disabledSchedule(testRootA)}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Trigger(context.Background(), testRootA); err != nil {
		t.Fatal(err)
	}
	<-firstRunner.starts
	waitFor(t, func() bool {
		snapshot := scheduler.Snapshot()
		return len(snapshot.Scans) == 1 && snapshot.Scans[0].State == StateWaiting
	})
	waiting := scheduler.Snapshot().Scans[0]
	if waiting.Cursor != "page-1" || waiting.ObservedCount != 10 {
		t.Fatalf("retry checkpoint was not durable: %+v", waiting)
	}
	if err := scheduler.Close(); err != nil {
		t.Fatal(err)
	}

	secondRunner := newScriptedRunner(func(_ context.Context, root RootSchedule, checkpoint ScanCheckpoint, _ ProgressFunc) (ScanResult, error) {
		if checkpoint.Cursor != "page-1" || checkpoint.ObservedCount != 10 {
			return ScanResult{}, errors.New("restart did not receive persisted checkpoint")
		}
		coverage := completeCoverage(root.ID, clock.Now())
		return ScanResult{ObservedCount: 11, Coverage: coverage, SourceCoverage: []domain.Coverage{completeSourceCoverage(root.ID, clock.Now())}}, nil
	})
	restarted := newTestScheduler(t, store, secondRunner, clock)
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Trigger(context.Background(), testRootA); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-secondRunner.starts
	waitFor(t, func() bool {
		snapshot := restarted.Snapshot()
		return len(snapshot.Scans) == 1 && snapshot.Scans[0].State == StateSucceeded
	})
}

func TestSchedulerRecoveryRequeuesDurableRunningScan(t *testing.T) {
	clock := newTestClock()
	created := clock.Now().Add(-time.Minute)
	started := created.Add(time.Second)
	runtimeID := domain.RuntimeID("00000000-0000-4000-8000-000000000002")
	coverage := partialCoverage(testRootA, started, "process_interrupted")
	snapshot := Snapshot{
		Version: 4,
		Roots:   []RootState{{Schedule: disabledSchedule(testRootA), ActiveScanID: runtimeID}},
		Scans: []ScanRecord{{
			ID: runtimeID, RootID: testRootA, ConfigRevision: "rev-1", Trigger: TriggerManual,
			State: StateRunning, Cursor: "page-4", ObservedCount: 40, Coverage: coverage,
			SourceCoverage: []domain.Coverage{coverage}, StartedAt: &started,
			CreatedAt: created, UpdatedAt: started, Attempt: 1,
		}},
	}
	store, err := NewMemoryStoreWithSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	runner := newScriptedRunner(func(_ context.Context, root RootSchedule, checkpoint ScanCheckpoint, _ ProgressFunc) (ScanResult, error) {
		if checkpoint.Cursor != "page-4" || checkpoint.ObservedCount != 40 {
			return ScanResult{}, errors.New("recovery lost durable progress")
		}
		coverage := completeCoverage(root.ID, clock.Now())
		return ScanResult{Coverage: coverage, SourceCoverage: []domain.Coverage{completeSourceCoverage(root.ID, clock.Now())}}, nil
	})
	scheduler := newTestScheduler(t, store, runner, clock)
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	call := <-runner.starts
	if call.checkpoint.Cursor != "page-4" {
		t.Fatalf("recovery checkpoint cursor = %q", call.checkpoint.Cursor)
	}
	waitFor(t, func() bool { return scheduler.Snapshot().Scans[0].State == StateSucceeded })
	if scheduler.Snapshot().Scans[0].Trigger != TriggerRecovery {
		t.Fatal("recovered scan did not retain recovery trigger")
	}
}

func TestSchedulerKeepsLastCompleteEvidenceWhenLaterScanIsPartial(t *testing.T) {
	clock := newTestClock()
	runner := newScriptedRunner(
		func(_ context.Context, root RootSchedule, _ ScanCheckpoint, _ ProgressFunc) (ScanResult, error) {
			return ScanResult{Coverage: completeCoverage(root.ID, clock.Now()), SourceCoverage: []domain.Coverage{completeSourceCoverage(root.ID, clock.Now())}}, nil
		},
		func(_ context.Context, root RootSchedule, _ ScanCheckpoint, _ ProgressFunc) (ScanResult, error) {
			coverage := partialCoverage(root.ID, clock.Now(), "permission_denied")
			return ScanResult{Coverage: coverage, SourceCoverage: []domain.Coverage{coverage}}, nil
		},
	)
	scheduler := newTestScheduler(t, NewMemoryStore(), runner, clock)
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{disabledSchedule(testRootA)}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := scheduler.Trigger(context.Background(), testRootA)
	if err != nil {
		t.Fatal(err)
	}
	<-runner.starts
	waitFor(t, func() bool {
		scan, ok := scheduler.Scan(first.Scan.ID)
		return ok && scan.State == StateSucceeded
	})
	root, ok := scheduler.Root(testRootA)
	if !ok || root.LastCompleteCoverage == nil || root.LastCompleteCoverage.Completeness != domain.CompletenessComplete {
		t.Fatalf("complete evidence was not retained: %+v", root)
	}
	second, err := scheduler.Trigger(context.Background(), testRootA)
	if err != nil {
		t.Fatal(err)
	}
	<-runner.starts
	waitFor(t, func() bool {
		scan, ok := scheduler.Scan(second.Scan.ID)
		return ok && scan.State == StateSucceeded
	})
	latest, _ := scheduler.Scan(second.Scan.ID)
	if latest.CanAssertAbsence() {
		t.Fatal("partial scan asserted absence")
	}
	root, _ = scheduler.Root(testRootA)
	if root.LastCoverage == nil || root.LastCoverage.Completeness != domain.CompletenessPartial {
		t.Fatalf("latest partial evidence was not visible: %+v", root)
	}
	if root.LastCompleteCoverage == nil || root.LastCompleteCoverage.Completeness != domain.CompletenessComplete || root.LastCompleteScanID != first.Scan.ID {
		t.Fatalf("partial scan replaced complete evidence: %+v", root)
	}
}

func TestSchedulerSchedulesEachRootAtItsInterval(t *testing.T) {
	clock := newTestClock()
	runner := newScriptedRunner(
		func(_ context.Context, root RootSchedule, _ ScanCheckpoint, _ ProgressFunc) (ScanResult, error) {
			return ScanResult{Coverage: completeCoverage(root.ID, clock.Now()), SourceCoverage: []domain.Coverage{completeSourceCoverage(root.ID, clock.Now())}}, nil
		},
		func(_ context.Context, root RootSchedule, _ ScanCheckpoint, _ ProgressFunc) (ScanResult, error) {
			return ScanResult{Coverage: completeCoverage(root.ID, clock.Now()), SourceCoverage: []domain.Coverage{completeSourceCoverage(root.ID, clock.Now())}}, nil
		},
	)
	scheduler := newTestScheduler(t, NewMemoryStore(), runner, clock)
	interval := 30 * time.Second
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{enabledSchedule(testRootA, interval)}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-runner.starts
	waitFor(t, func() bool {
		return runner.callCount() == 1 && len(scheduler.Snapshot().Scans) == 1 && scheduler.Snapshot().Scans[0].State == StateSucceeded
	})
	clock.Advance(interval - time.Second)
	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := runner.callCount(); got != 1 {
		t.Fatalf("scheduled scan ran before interval: %d calls", got)
	}
	clock.Advance(time.Second)
	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-runner.starts
	waitFor(t, func() bool { return runner.callCount() == 2 })
	for _, scan := range scheduler.ListScans(testRootA) {
		if scan.Trigger != TriggerScheduled {
			t.Fatalf("interval scan has trigger %q", scan.Trigger)
		}
	}
}

func TestMemoryStoreReadsAndWritesAreDeepCopies(t *testing.T) {
	clock := newTestClock()
	coverage := completeCoverage(testRootA, clock.Now())
	snapshot := Snapshot{Roots: []RootState{{Schedule: disabledSchedule(testRootA), LastCoverage: &coverage}}, Version: 1}
	store, err := NewMemoryStoreWithSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	loaded.Roots[0].LastCoverage.ReasonCodes = append(loaded.Roots[0].LastCoverage.ReasonCodes, "mutated")
	loaded.Roots[0].LastCoverage.ObservedAt = loaded.Roots[0].LastCoverage.ObservedAt.Add(time.Hour)
	loaded.Version = 99
	again := store.Current()
	if again.Version != 1 || len(again.Roots[0].LastCoverage.ReasonCodes) != 0 || !again.Roots[0].LastCoverage.ObservedAt.Equal(clock.Now()) {
		t.Fatalf("memory store exposed mutable nested state: %+v", again)
	}
}

func TestSchedulerRejectsIncompleteCoverageAsAbsence(t *testing.T) {
	clock := newTestClock()
	runner := newScriptedRunner(func(_ context.Context, root RootSchedule, _ ScanCheckpoint, _ ProgressFunc) (ScanResult, error) {
		return ScanResult{Coverage: domain.Coverage{RootID: root.ID, Completeness: domain.CompletenessUnknown, ObservedAt: clock.Now()}}, nil
	})
	scheduler := newTestScheduler(t, NewMemoryStore(), runner, clock)
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{disabledSchedule(testRootA)}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.Trigger(context.Background(), testRootA)
	if err != nil {
		t.Fatal(err)
	}
	<-runner.starts
	waitFor(t, func() bool {
		scan, ok := scheduler.Scan(result.Scan.ID)
		return ok && scan.State == StateSucceeded
	})
	scan, _ := scheduler.Scan(result.Scan.ID)
	if scan.Coverage.Completeness != domain.CompletenessUnknown || scan.CanAssertAbsence() {
		t.Fatalf("unknown coverage was treated as absence: %+v", scan)
	}
}

func TestSchedulerDeadlineIsDurableAndCannotAssertAbsence(t *testing.T) {
	clock := newTestClock()
	runner := newScriptedRunner(func(ctx context.Context, _ RootSchedule, _ ScanCheckpoint, _ ProgressFunc) (ScanResult, error) {
		<-ctx.Done()
		return ScanResult{Coverage: completeCoverage(testRootA, clock.Now()), SourceCoverage: []domain.Coverage{completeSourceCoverage(testRootA, clock.Now())}}, ctx.Err()
	})
	options := DefaultOptions()
	options.Now = clock.Now
	options.MinimumInterval = time.Nanosecond
	options.PollInterval = time.Hour
	options.RunTimeout = 5 * time.Millisecond
	scheduler, err := New(NewMemoryStore(), runner, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheduler.Close() })
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{disabledSchedule(testRootA)}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.Trigger(context.Background(), testRootA)
	if err != nil {
		t.Fatal(err)
	}
	<-runner.starts
	waitFor(t, func() bool {
		scan, ok := scheduler.Scan(result.Scan.ID)
		return ok && scan.State == StateDeadlineExceeded
	})
	scan, _ := scheduler.Scan(result.Scan.ID)
	if scan.CanAssertAbsence() || scan.ErrorCode != "deadline_exceeded" {
		t.Fatalf("deadline scan exposed an absence claim: %+v", scan)
	}
}

func TestSchedulerStopPersistsActiveCancellationBeforeReturning(t *testing.T) {
	clock := newTestClock()
	runner := newScriptedRunner(func(ctx context.Context, _ RootSchedule, _ ScanCheckpoint, _ ProgressFunc) (ScanResult, error) {
		<-ctx.Done()
		return ScanResult{}, ctx.Err()
	})
	store := NewMemoryStore()
	scheduler := newTestScheduler(t, store, runner, clock)
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{disabledSchedule(testRootA)}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := scheduler.Trigger(context.Background(), testRootA)
	if err != nil {
		t.Fatal(err)
	}
	<-runner.starts
	if err := scheduler.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := store.Current()
	if len(snapshot.Scans) != 1 || snapshot.Scans[0].ID != result.Scan.ID || snapshot.Scans[0].State != StateCancelled {
		t.Fatalf("shutdown did not persist active cancellation: %+v", snapshot)
	}
}

func TestSchedulerRetiresQueuedRootWithoutDeletingScanHistory(t *testing.T) {
	clock := newTestClock()
	runner := newScriptedRunner()
	store := NewMemoryStore()
	scheduler := newTestScheduler(t, store, runner, clock)
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{disabledSchedule(testRootA)}); err != nil {
		t.Fatal(err)
	}
	if _, err := scheduler.Trigger(context.Background(), testRootA); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.ConfigureRoots(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		snapshot := scheduler.Snapshot()
		return len(snapshot.Scans) == 1 && snapshot.Scans[0].State == StateCancelled && len(snapshot.Roots) == 1 && snapshot.Roots[0].Retired
	})
	root := scheduler.Snapshot().Roots[0]
	if root.ActiveScanID != "" || root.RetiredAt == nil {
		t.Fatalf("retired root retained active scheduling state: %+v", root)
	}
}

func TestSchedulerNoOpReconciliationDoesNotWriteNewVersion(t *testing.T) {
	clock := newTestClock()
	scheduler := newTestScheduler(t, NewMemoryStore(), newScriptedRunner(), clock)
	schedule := disabledSchedule(testRootA)
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{schedule}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	version := scheduler.Snapshot().Version
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{schedule}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := scheduler.Snapshot().Version; got != version {
		t.Fatalf("no-op schedule reconciliation advanced version from %d to %d", version, got)
	}
}

func TestSchedulerCoalescesScheduledAndManualFollowUp(t *testing.T) {
	clock := newTestClock()
	firstRelease := make(chan struct{})
	secondRelease := make(chan struct{})
	runner := newScriptedRunner(
		blockingScript(firstRelease, ScanResult{
			Coverage:       completeCoverage(testRootA, clock.Now()),
			SourceCoverage: []domain.Coverage{completeSourceCoverage(testRootA, clock.Now())},
		}),
		blockingScript(secondRelease, ScanResult{
			Coverage:       completeCoverage(testRootA, clock.Now()),
			SourceCoverage: []domain.Coverage{completeSourceCoverage(testRootA, clock.Now())},
		}),
	)
	scheduler := newTestScheduler(t, NewMemoryStore(), runner, clock)
	interval := 30 * time.Second
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{enabledSchedule(testRootA, interval)}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstCall := <-runner.starts
	if firstCall.root.Revision != "rev-1" {
		t.Fatalf("scheduled scan used revision %q", firstCall.root.Revision)
	}
	first := scheduler.Snapshot().Scans[0]
	if _, err := scheduler.Trigger(context.Background(), testRootA); err != nil {
		t.Fatal(err)
	}
	clock.Advance(3 * interval)
	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	root, ok := scheduler.Root(testRootA)
	if !ok || !root.FollowUpPending || root.NextScheduledAt == nil || !root.NextScheduledAt.After(clock.Now()) {
		t.Fatalf("scheduled overlap did not retain one bounded follow-up: %+v", root)
	}
	if got := len(scanForRoot(scheduler.Snapshot(), testRootA)); got != 1 {
		t.Fatalf("overdue scheduled trigger created %d scans while active, want 1", got)
	}
	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(scanForRoot(scheduler.Snapshot(), testRootA)); got != 1 {
		t.Fatalf("repeated overdue tick created %d active scans, want 1", got)
	}
	close(firstRelease)
	var secondCall runnerCall
	select {
	case secondCall = <-runner.starts:
	case <-time.After(3 * time.Second):
		t.Fatal("coalesced follow-up did not start")
	}
	if secondCall.checkpoint.ScanID == first.ID || secondCall.root.Revision != "rev-1" {
		t.Fatalf("follow-up dispatched invalid checkpoint: %+v", secondCall)
	}
	close(secondRelease)
	waitFor(t, func() bool {
		scans := scanForRoot(scheduler.Snapshot(), testRootA)
		if len(scans) != 2 {
			return false
		}
		for _, scan := range scans {
			if !scan.State.Terminal() {
				return false
			}
		}
		return true
	})
	if err := scheduler.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(scanForRoot(scheduler.Snapshot(), testRootA)); got != 2 {
		t.Fatalf("overdue schedule produced %d scans after follow-up, want 2", got)
	}
}

func TestSchedulerRetainsFollowUpAfterDeadline(t *testing.T) {
	clock := newTestClock()
	secondRelease := make(chan struct{})
	runner := newScriptedRunner(
		func(ctx context.Context, root RootSchedule, _ ScanCheckpoint, _ ProgressFunc) (ScanResult, error) {
			<-ctx.Done()
			return ScanResult{Coverage: completeCoverage(root.ID, clock.Now())}, ctx.Err()
		},
		blockingScript(secondRelease, ScanResult{
			Coverage:       completeCoverage(testRootA, clock.Now()),
			SourceCoverage: []domain.Coverage{completeSourceCoverage(testRootA, clock.Now())},
		}),
	)
	options := DefaultOptions()
	options.Now = clock.Now
	options.MinimumInterval = time.Nanosecond
	options.PollInterval = time.Hour
	options.RunTimeout = 20 * time.Millisecond
	scheduler, err := New(NewMemoryStore(), runner, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheduler.Close() })
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{enabledSchedule(testRootA, time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstCall := <-runner.starts
	if _, err := scheduler.Trigger(context.Background(), testRootA); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		scans := scanForRoot(scheduler.Snapshot(), testRootA)
		return len(scans) == 2 && scans[0].State == StateDeadlineExceeded
	})
	scans := scanForRoot(scheduler.Snapshot(), testRootA)
	if scans[0].ID != firstCall.checkpoint.ScanID || scans[0].ErrorCode != "deadline_exceeded" {
		t.Fatalf("unexpected deadline scan: %+v", scans[0])
	}
	if scans[1].ConfigRevision != "rev-1" {
		t.Fatalf("deadline follow-up used revision %q", scans[1].ConfigRevision)
	}
	root, ok := scheduler.Root(testRootA)
	if !ok || root.FollowUpPending || root.ActiveScanID != scans[1].ID {
		t.Fatalf("deadline follow-up was not materialized as active work: %+v", root)
	}
	close(secondRelease)
}

func TestSchedulerStalesRunningScanWhenRevisionChanges(t *testing.T) {
	clock := newTestClock()
	firstRelease := make(chan struct{})
	secondRelease := make(chan struct{})
	runner := newScriptedRunner(
		blockingScript(firstRelease, ScanResult{
			Coverage:       completeCoverage(testRootA, clock.Now()),
			SourceCoverage: []domain.Coverage{completeSourceCoverage(testRootA, clock.Now())},
		}),
		blockingScript(secondRelease, ScanResult{
			Coverage:       completeCoverage(testRootA, clock.Now()),
			SourceCoverage: []domain.Coverage{completeSourceCoverage(testRootA, clock.Now())},
		}),
	)
	scheduler := newTestScheduler(t, NewMemoryStore(), runner, clock)
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{disabledSchedule(testRootA)}); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, err := scheduler.Trigger(context.Background(), testRootA)
	if err != nil {
		t.Fatal(err)
	}
	<-runner.starts
	if err := scheduler.ConfigureRoots(context.Background(), []RootSchedule{{
		ID: testRootA, Revision: "rev-2", Interval: time.Minute,
	}}); err != nil {
		t.Fatal(err)
	}
	var secondCall runnerCall
	select {
	case secondCall = <-runner.starts:
	case <-time.After(3 * time.Second):
		t.Fatal("current-revision scan did not start after stale cancellation")
	}
	if secondCall.root.Revision != "rev-2" || secondCall.checkpoint.ConfigRevision != "rev-2" || secondCall.checkpoint.Cursor != "" {
		t.Fatalf("new revision dispatched stale checkpoint: %+v", secondCall)
	}
	snapshot := scheduler.Snapshot()
	old, ok := scheduler.Scan(first.Scan.ID)
	if !ok || old.State != StateCancelled || old.ErrorCode != "stale_config_revision" || old.CanAssertAbsence() {
		t.Fatalf("old revision remained usable: %+v", old)
	}
	if len(scanForRoot(snapshot, testRootA)) != 2 {
		t.Fatalf("revision change created unexpected scan count: %+v", snapshot.Scans)
	}
	root, ok := scheduler.Root(testRootA)
	if !ok || root.Schedule.Revision != "rev-2" || root.LastCompleteScanID != "" || root.LastCompleteCoverage != nil {
		t.Fatalf("old revision evidence remained current: %+v", root)
	}
	close(secondRelease)
	waitFor(t, func() bool {
		fresh, ok := scheduler.Scan(secondCall.checkpoint.ScanID)
		return ok && fresh.State.Terminal()
	})
}

func TestSchedulerRecoveryRejectsMismatchedRevisionCursor(t *testing.T) {
	clock := newTestClock()
	created := clock.Now().Add(-time.Minute)
	started := created.Add(time.Second)
	runtimeID := domain.RuntimeID("00000000-0000-4000-8000-000000000003")
	coverage := partialCoverage(testRootA, started, "process_interrupted")
	snapshot := Snapshot{
		Version: 7,
		Roots:   []RootState{{Schedule: RootSchedule{ID: testRootA, Revision: "rev-2", Interval: time.Minute}, ActiveScanID: runtimeID}},
		Scans: []ScanRecord{{
			ID: runtimeID, RootID: testRootA, ConfigRevision: "rev-1", Trigger: TriggerManual,
			State: StateRunning, Cursor: "old-root-page-4", ObservedCount: 40, Coverage: coverage,
			SourceCoverage: []domain.Coverage{coverage}, StartedAt: &started,
			CreatedAt: created, UpdatedAt: started, Attempt: 1,
		}},
	}
	store, err := NewMemoryStoreWithSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	runner := newScriptedRunner(func(_ context.Context, root RootSchedule, checkpoint ScanCheckpoint, _ ProgressFunc) (ScanResult, error) {
		if root.Revision != "rev-2" || checkpoint.ConfigRevision != "rev-2" || checkpoint.Cursor != "" || checkpoint.ObservedCount != 0 {
			return ScanResult{}, fmt.Errorf("stale recovery checkpoint: root=%+v checkpoint=%+v", root, checkpoint)
		}
		return ScanResult{
			Coverage:       completeCoverage(root.ID, clock.Now()),
			SourceCoverage: []domain.Coverage{completeSourceCoverage(root.ID, clock.Now())},
		}, nil
	})
	scheduler := newTestScheduler(t, store, runner, clock)
	if err := scheduler.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	call := <-runner.starts
	if call.checkpoint.ConfigRevision != "rev-2" || call.checkpoint.Cursor != "" {
		t.Fatalf("restart resumed obsolete cursor: %+v", call.checkpoint)
	}
	waitFor(t, func() bool {
		fresh := scanForRoot(scheduler.Snapshot(), testRootA)
		return len(fresh) == 2 && fresh[0].State == StateCancelled && fresh[1].State == StateSucceeded
	})
	old, _ := scheduler.Scan(runtimeID)
	if old.ErrorCode != "stale_config_revision" || old.CanAssertAbsence() {
		t.Fatalf("mismatched restart scan exposed evidence: %+v", old)
	}
}
