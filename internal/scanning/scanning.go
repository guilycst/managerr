// Package scanning coordinates durable, read-only observations of configured
// storage roots. It owns admission, scheduling and recovery; the runner owns
// the actual filesystem/client reads and reports bounded progress through the
// callback supplied to it.
//
// StateStore is deliberately a small persistence boundary. The production
// adapter can map it to the root-owned SQLite scans and coverage_snapshots
// tables without making this package depend on storage or SQL. MemoryStore is
// provided for deterministic tests and small embedders.
package scanning

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
)

const (
	defaultMaxConcurrent   = 1
	defaultPollInterval    = time.Second
	defaultRunTimeout      = 15 * time.Minute
	defaultMinimumInterval = 30 * time.Second
	defaultRetryBackoff    = 5 * time.Second
	defaultMaxCursorBytes  = 64 << 10
	defaultMaxErrorDetail  = 2 << 10
	maxScanErrorCodeBytes  = 128
	maxConfigRevisionBytes = 256
)

var (
	ErrInvalidOptions      = errors.New("invalid scanning options")
	ErrInvalidSnapshot     = errors.New("invalid scanning snapshot")
	ErrStateStoreRequired  = errors.New("scanning state store is required")
	ErrRunnerRequired      = errors.New("scanning runner is required")
	ErrRootNotFound        = errors.New("scanning root was not found")
	ErrRootRetired         = errors.New("scanning root is retired")
	ErrScanNotFound        = errors.New("scanning scan was not found")
	ErrScanNotRunning      = errors.New("scanning scan is not running")
	ErrSchedulerNotStarted = errors.New("scanning scheduler is not started")
	ErrSchedulerClosed     = errors.New("scanning scheduler is closed")
	ErrScanProgressInvalid = errors.New("scanning progress is invalid")
	ErrScanProgressRewound = errors.New("scanning progress rewound")
	ErrRetryable           = errors.New("scanning runner requested retry")
)

// ScanState is the durable lifecycle of one root observation request. A
// succeeded scan can still have partial or unknown coverage: operation state
// and evidence completeness are intentionally separate.
type ScanState string

const (
	StateQueued           ScanState = "queued"
	StateRunning          ScanState = "running"
	StateWaiting          ScanState = "waiting"
	StateSucceeded        ScanState = "succeeded"
	StateFailed           ScanState = "failed"
	StateCancelled        ScanState = "cancelled"
	StateDeadlineExceeded ScanState = "deadline_exceeded"
)

func (state ScanState) Valid() bool {
	switch state {
	case StateQueued, StateRunning, StateWaiting, StateSucceeded,
		StateFailed, StateCancelled, StateDeadlineExceeded:
		return true
	default:
		return false
	}
}

func (state ScanState) Terminal() bool {
	switch state {
	case StateSucceeded, StateFailed, StateCancelled, StateDeadlineExceeded:
		return true
	default:
		return false
	}
}

// TriggerKind identifies why a scan was admitted. Recovery is used when a
// process restarts after a run was durably marked running.
type TriggerKind string

const (
	TriggerScheduled TriggerKind = "scheduled"
	TriggerManual    TriggerKind = "manual"
	TriggerRecovery  TriggerKind = "recovery"
)

func (trigger TriggerKind) Valid() bool {
	switch trigger {
	case TriggerScheduled, TriggerManual, TriggerRecovery:
		return true
	default:
		return false
	}
}

// RootSchedule is the validated scheduling portion of one storage root. The
// configuration package enforces the selected thirty-second minimum; this
// package also enforces it by default so a direct embedder cannot accidentally
// create an unbounded trigger loop. Tests may lower MinimumInterval explicitly
// in Options.
type RootSchedule struct {
	ID       domain.ConfigID
	Revision string
	Enabled  bool
	Interval time.Duration
}

func (schedule RootSchedule) Validate(minimumInterval time.Duration) error {
	if !schedule.ID.Valid() {
		return fmt.Errorf("%w: root has invalid id", ErrInvalidSnapshot)
	}
	if strings.TrimSpace(schedule.Revision) != "" && len(schedule.Revision) > maxConfigRevisionBytes {
		return fmt.Errorf("%w: root revision is too long", ErrInvalidSnapshot)
	}
	if schedule.Enabled {
		if schedule.Interval <= 0 {
			return fmt.Errorf("%w: enabled root requires a positive interval", ErrInvalidSnapshot)
		}
		if minimumInterval > 0 && schedule.Interval < minimumInterval {
			return fmt.Errorf("%w: root interval is below the minimum", ErrInvalidSnapshot)
		}
	} else if schedule.Interval < 0 {
		return fmt.Errorf("%w: root interval cannot be negative", ErrInvalidSnapshot)
	}
	return nil
}

// RootState is the durable scheduling projection for one root. LastCoverage
// is the newest result even when it is incomplete. LastCompleteCoverage is
// retained separately so an interrupted scan cannot replace complete evidence
// with a false absence claim.
type RootState struct {
	Schedule             RootSchedule
	Retired              bool
	NextScheduledAt      *time.Time
	ActiveScanID         domain.RuntimeID
	FollowUpPending      bool
	LastScanID           domain.RuntimeID
	LastCoverage         *domain.Coverage
	LastCompleteScanID   domain.RuntimeID
	LastCompleteCoverage *domain.Coverage
	RetiredAt            *time.Time
}

// ScanRecord is the durable state of one scan. Cursor and source coverage are
// opaque to the scheduler but are persisted on every progress callback so a
// replacement process can resume without starting at the beginning.
type ScanRecord struct {
	ID                      domain.RuntimeID
	RootID                  domain.ConfigID
	ConfigRevision          string
	Trigger                 TriggerKind
	State                   ScanState
	Cursor                  string
	ObservedCount           int64
	Coverage                domain.Coverage
	SourceCoverage          []domain.Coverage
	StartedAt               *time.Time
	CompletedAt             *time.Time
	NextAttemptAt           *time.Time
	CancellationRequestedAt *time.Time
	CreatedAt               time.Time
	UpdatedAt               time.Time
	Attempt                 int
	ErrorCode               string
	ErrorDetail             string
}

// CanAssertAbsence is intentionally strict. Only an operation that completed
// successfully with complete aggregate and per-source coverage may establish
// that a root contains no item; partial, unknown, failed, cancelled or stale
// records are evidence but never absence.
func (record ScanRecord) CanAssertAbsence() bool {
	if record.State != StateSucceeded || record.Coverage.Completeness != domain.CompletenessComplete || record.Cursor != "" {
		return false
	}
	if len(record.SourceCoverage) == 0 {
		return false
	}
	for _, coverage := range record.SourceCoverage {
		if coverage.Completeness != domain.CompletenessComplete {
			return false
		}
	}
	return true
}

// ScanCheckpoint is supplied to a runner at dispatch. It contains the last
// durable page boundary and never contains filesystem host paths or secrets.
type ScanCheckpoint struct {
	ScanID         domain.RuntimeID
	RootID         domain.ConfigID
	ConfigRevision string
	Cursor         string
	ObservedCount  int64
	Coverage       domain.Coverage
	SourceCoverage []domain.Coverage
}

// ScanProgress is a bounded durable checkpoint reported by a runner. A runner
// should call Progress after each safe page/chunk and before returning an
// interruption error.
type ScanProgress struct {
	Cursor         string
	ObservedCount  int64
	Coverage       domain.Coverage
	SourceCoverage []domain.Coverage
}

// ScanResult is the terminal or retryable result of one runner invocation. A
// non-empty Cursor with a nil error requests an immediate bounded continuation
// using the same durable scan record.
type ScanResult struct {
	Cursor         string
	ObservedCount  int64
	Coverage       domain.Coverage
	SourceCoverage []domain.Coverage
	NextAttemptAt  *time.Time
}

// ProgressFunc is passed to a runner and persists the supplied page boundary.
type ProgressFunc func(ScanProgress) error

// ScanRunner owns one read-only observation implementation. The scheduler
// controls context cancellation and persistence; the runner must honor ctx
// between bounded filesystem/client operations.
type ScanRunner interface {
	Run(context.Context, RootSchedule, ScanCheckpoint, ProgressFunc) (ScanResult, error)
}

// RunnerFunc adapts a function to ScanRunner.
type RunnerFunc func(context.Context, RootSchedule, ScanCheckpoint, ProgressFunc) (ScanResult, error)

func (runner RunnerFunc) Run(ctx context.Context, root RootSchedule, checkpoint ScanCheckpoint, progress ProgressFunc) (ScanResult, error) {
	if runner == nil {
		return ScanResult{}, ErrRunnerRequired
	}
	return runner(ctx, root, checkpoint, progress)
}

// StateStore is the durability seam for scheduler state. Save must atomically
// publish the supplied snapshot; the scheduler serializes saves and restores
// its prior in-memory state when Save fails.
type StateStore interface {
	Load(context.Context) (Snapshot, error)
	Save(context.Context, Snapshot) error
}

// Snapshot is the complete scheduler projection needed for restart recovery.
// It intentionally mirrors the information that a SQLite adapter will map to
// scans and coverage snapshots while keeping root scheduling state together.
type Snapshot struct {
	Version uint64
	Roots   []RootState
	Scans   []ScanRecord
}

// Validate checks durable invariants independently of scheduler options. It
// rejects duplicate roots/scans and more than one non-terminal scan per root,
// which would otherwise violate per-root admission.
func (snapshot Snapshot) Validate(minimumInterval time.Duration) error {
	seenRoots := make(map[domain.ConfigID]struct{}, len(snapshot.Roots))
	for index, root := range snapshot.Roots {
		if err := root.Schedule.Validate(minimumInterval); err != nil {
			return fmt.Errorf("%w: root %d: %v", ErrInvalidSnapshot, index, err)
		}
		if _, exists := seenRoots[root.Schedule.ID]; exists {
			return fmt.Errorf("%w: duplicate root %q", ErrInvalidSnapshot, root.Schedule.ID)
		}
		seenRoots[root.Schedule.ID] = struct{}{}
		if root.ActiveScanID != "" && !root.ActiveScanID.Valid() {
			return fmt.Errorf("%w: root %q has invalid active scan id", ErrInvalidSnapshot, root.Schedule.ID)
		}
		if root.LastScanID != "" && !root.LastScanID.Valid() {
			return fmt.Errorf("%w: root %q has invalid last scan id", ErrInvalidSnapshot, root.Schedule.ID)
		}
		if root.LastCompleteScanID != "" && !root.LastCompleteScanID.Valid() {
			return fmt.Errorf("%w: root %q has invalid complete scan id", ErrInvalidSnapshot, root.Schedule.ID)
		}
		if root.Retired {
			if root.Schedule.Enabled {
				return fmt.Errorf("%w: retired root %q cannot remain enabled", ErrInvalidSnapshot, root.Schedule.ID)
			}
			if root.RetiredAt == nil {
				return fmt.Errorf("%w: retired root %q requires retirement time", ErrInvalidSnapshot, root.Schedule.ID)
			}
			if root.NextScheduledAt != nil {
				return fmt.Errorf("%w: retired root %q cannot have a next schedule", ErrInvalidSnapshot, root.Schedule.ID)
			}
		} else if root.RetiredAt != nil {
			return fmt.Errorf("%w: active root %q has retirement time", ErrInvalidSnapshot, root.Schedule.ID)
		}
		for name, coverage := range map[string]*domain.Coverage{
			"last": root.LastCoverage, "last complete": root.LastCompleteCoverage,
		} {
			if coverage == nil {
				continue
			}
			if coverage.RootID != "" && coverage.RootID != root.Schedule.ID {
				return fmt.Errorf("%w: root %q %s coverage has another root", ErrInvalidSnapshot, root.Schedule.ID, name)
			}
			if err := coverage.Validate(); err != nil {
				return fmt.Errorf("%w: root %q %s coverage: %v", ErrInvalidSnapshot, root.Schedule.ID, name, err)
			}
		}
	}
	seenScans := make(map[domain.RuntimeID]struct{}, len(snapshot.Scans))
	activeByRoot := make(map[domain.ConfigID]domain.RuntimeID)
	for index, scan := range snapshot.Scans {
		if err := scan.validate(); err != nil {
			return fmt.Errorf("%w: scan %d: %v", ErrInvalidSnapshot, index, err)
		}
		if _, exists := seenRoots[scan.RootID]; !exists {
			return fmt.Errorf("%w: scan %q references unknown root", ErrInvalidSnapshot, scan.ID)
		}
		if _, exists := seenScans[scan.ID]; exists {
			return fmt.Errorf("%w: duplicate scan %q", ErrInvalidSnapshot, scan.ID)
		}
		seenScans[scan.ID] = struct{}{}
		if !scan.State.Terminal() {
			if previous, exists := activeByRoot[scan.RootID]; exists {
				return fmt.Errorf("%w: root %q has active scans %q and %q", ErrInvalidSnapshot, scan.RootID, previous, scan.ID)
			}
			activeByRoot[scan.RootID] = scan.ID
		}
	}
	for _, root := range snapshot.Roots {
		active := activeByRoot[root.Schedule.ID]
		if active != root.ActiveScanID {
			return fmt.Errorf("%w: root %q active scan reference is inconsistent", ErrInvalidSnapshot, root.Schedule.ID)
		}
	}
	return nil
}

func (record ScanRecord) validate() error {
	if !record.ID.Valid() {
		return errors.New("scan has invalid id")
	}
	if !record.RootID.Valid() {
		return errors.New("scan has invalid root id")
	}
	if !record.Trigger.Valid() {
		return errors.New("scan has invalid trigger")
	}
	if !record.State.Valid() {
		return errors.New("scan has invalid state")
	}
	if record.ObservedCount < 0 {
		return errors.New("scan observed count cannot be negative")
	}
	if record.Attempt < 0 {
		return errors.New("scan attempt cannot be negative")
	}
	if len(record.Cursor) > defaultMaxCursorBytes {
		return errors.New("scan cursor is too large")
	}
	if len(record.ErrorCode) > maxScanErrorCodeBytes || len(record.ConfigRevision) > maxConfigRevisionBytes || len(record.ErrorDetail) > defaultMaxErrorDetail {
		return errors.New("scan metadata is too large")
	}
	if record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() {
		return errors.New("scan timestamps are required")
	}
	if record.UpdatedAt.Before(record.CreatedAt) {
		return errors.New("scan updated time precedes creation")
	}
	if record.StartedAt != nil && record.StartedAt.Before(record.CreatedAt) {
		return errors.New("scan started time precedes creation")
	}
	if record.CompletedAt != nil && record.StartedAt == nil && record.State != StateCancelled && record.State != StateDeadlineExceeded {
		return errors.New("completed scan requires started time")
	}
	if record.CompletedAt != nil && record.StartedAt != nil && record.CompletedAt.Before(*record.StartedAt) {
		return errors.New("scan completed time precedes start")
	}
	if record.State.Terminal() && record.CompletedAt == nil {
		return errors.New("terminal scan requires completed time")
	}
	if !record.State.Terminal() && record.CompletedAt != nil {
		return errors.New("non-terminal scan cannot have completed time")
	}
	if record.Coverage.RootID != "" && record.Coverage.RootID != record.RootID {
		return errors.New("scan coverage root does not match scan")
	}
	if err := record.Coverage.Validate(); err != nil {
		return fmt.Errorf("scan coverage: %w", err)
	}
	if record.Coverage.Completeness == domain.CompletenessComplete && record.Coverage.CompletedAt == nil {
		return errors.New("complete scan coverage requires completed time")
	}
	for index, coverage := range record.SourceCoverage {
		if coverage.RootID != "" && coverage.RootID != record.RootID {
			return fmt.Errorf("source coverage %d belongs to another root", index)
		}
		if err := coverage.Validate(); err != nil {
			return fmt.Errorf("source coverage %d: %w", index, err)
		}
		if coverage.Completeness == domain.CompletenessComplete && coverage.CompletedAt == nil {
			return fmt.Errorf("source coverage %d complete evidence requires completed time", index)
		}
	}
	return nil
}

// MemoryStore is a concurrency-safe reference StateStore. It retains the
// complete snapshot across scheduler instances in a process, which makes
// crash/restart and persistence-failure behavior deterministic in tests. A
// production process should provide a SQLite-backed implementation.
type MemoryStore struct {
	mu       sync.Mutex
	snapshot Snapshot
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

func NewMemoryStoreWithSnapshot(snapshot Snapshot) (*MemoryStore, error) {
	if err := snapshot.Validate(0); err != nil {
		return nil, err
	}
	return &MemoryStore{snapshot: cloneSnapshot(snapshot)}, nil
}

func (store *MemoryStore) Load(ctx context.Context) (Snapshot, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	if store == nil {
		return Snapshot{}, ErrStateStoreRequired
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return cloneSnapshot(store.snapshot), nil
}

func (store *MemoryStore) Save(ctx context.Context, snapshot Snapshot) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if store == nil {
		return ErrStateStoreRequired
	}
	if err := snapshot.Validate(0); err != nil {
		return err
	}
	store.mu.Lock()
	store.snapshot = cloneSnapshot(snapshot)
	store.mu.Unlock()
	return nil
}

func (store *MemoryStore) Current() Snapshot {
	if store == nil {
		return Snapshot{}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return cloneSnapshot(store.snapshot)
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	copySnapshot := Snapshot{Version: snapshot.Version, Roots: make([]RootState, len(snapshot.Roots)), Scans: make([]ScanRecord, len(snapshot.Scans))}
	for index, root := range snapshot.Roots {
		copySnapshot.Roots[index] = cloneRootState(root)
	}
	for index, scan := range snapshot.Scans {
		copySnapshot.Scans[index] = cloneScanRecord(scan)
	}
	return copySnapshot
}

func cloneRootState(root RootState) RootState {
	root.NextScheduledAt = cloneTime(root.NextScheduledAt)
	root.LastCoverage = cloneCoveragePointer(root.LastCoverage)
	root.LastCompleteCoverage = cloneCoveragePointer(root.LastCompleteCoverage)
	root.RetiredAt = cloneTime(root.RetiredAt)
	return root
}

func cloneScanRecord(record ScanRecord) ScanRecord {
	record.Coverage = cloneCoverage(record.Coverage)
	record.SourceCoverage = cloneCoverages(record.SourceCoverage)
	record.StartedAt = cloneTime(record.StartedAt)
	record.CompletedAt = cloneTime(record.CompletedAt)
	record.NextAttemptAt = cloneTime(record.NextAttemptAt)
	record.CancellationRequestedAt = cloneTime(record.CancellationRequestedAt)
	return record
}

func cloneCoveragePointer(coverage *domain.Coverage) *domain.Coverage {
	if coverage == nil {
		return nil
	}
	copyCoverage := cloneCoverage(*coverage)
	return &copyCoverage
}

func cloneCoverage(coverage domain.Coverage) domain.Coverage {
	coverage.ReasonCodes = append([]string(nil), coverage.ReasonCodes...)
	coverage.StartedAt = cloneTime(coverage.StartedAt)
	coverage.CompletedAt = cloneTime(coverage.CompletedAt)
	return coverage
}

func cloneCoverages(coverages []domain.Coverage) []domain.Coverage {
	if coverages == nil {
		return nil
	}
	cloned := make([]domain.Coverage, len(coverages))
	for index, coverage := range coverages {
		cloned[index] = cloneCoverage(coverage)
	}
	return cloned
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func nowUTC(now func() time.Time) time.Time {
	value := now()
	if value.IsZero() {
		value = time.Unix(1, 0).UTC()
	}
	return value.UTC()
}

func hasReason(coverage domain.Coverage, reason string) bool {
	for _, candidate := range coverage.ReasonCodes {
		if candidate == reason {
			return true
		}
	}
	return false
}

func appendReason(coverage *domain.Coverage, reason string) {
	if coverage == nil || reason == "" || hasReason(*coverage, reason) {
		return
	}
	coverage.ReasonCodes = append(coverage.ReasonCodes, reason)
}

func sanitizeError(err error, maxBytes int) string {
	if err == nil {
		return ""
	}
	message := strings.Join(strings.Fields(err.Error()), " ")
	if maxBytes <= 0 || len(message) <= maxBytes {
		return message
	}
	return message[:maxBytes]
}

func cloneAndSortRoots(roots []RootState) []RootState {
	result := make([]RootState, len(roots))
	for index, root := range roots {
		result[index] = cloneRootState(root)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Schedule.ID < result[right].Schedule.ID })
	return result
}

// Options bounds scheduler admission and runner execution. The interval
// minimum can be lowered by a test or a deliberately different deployment
// policy, but the default follows the configuration contract.
type Options struct {
	MaxConcurrent   int
	PollInterval    time.Duration
	RunTimeout      time.Duration
	MinimumInterval time.Duration
	RetryBackoff    time.Duration
	MaxCursorBytes  int
	MaxErrorDetail  int
	Now             func() time.Time
}

func DefaultOptions() Options {
	return Options{
		MaxConcurrent:   defaultMaxConcurrent,
		PollInterval:    defaultPollInterval,
		RunTimeout:      defaultRunTimeout,
		MinimumInterval: defaultMinimumInterval,
		RetryBackoff:    defaultRetryBackoff,
		MaxCursorBytes:  defaultMaxCursorBytes,
		MaxErrorDetail:  defaultMaxErrorDetail,
		Now:             func() time.Time { return time.Now().UTC() },
	}
}

func normalizeOptions(options Options) (Options, error) {
	defaults := DefaultOptions()
	if options.MaxConcurrent <= 0 {
		options.MaxConcurrent = defaults.MaxConcurrent
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaults.PollInterval
	}
	if options.RunTimeout <= 0 {
		options.RunTimeout = defaults.RunTimeout
	}
	if options.MinimumInterval <= 0 {
		options.MinimumInterval = defaults.MinimumInterval
	}
	if options.RetryBackoff <= 0 {
		options.RetryBackoff = defaults.RetryBackoff
	}
	if options.MaxCursorBytes <= 0 {
		options.MaxCursorBytes = defaults.MaxCursorBytes
	}
	if options.MaxErrorDetail <= 0 {
		options.MaxErrorDetail = defaults.MaxErrorDetail
	}
	if options.Now == nil {
		options.Now = defaults.Now
	}
	if options.MaxCursorBytes > defaultMaxCursorBytes {
		return Options{}, fmt.Errorf("%w: max cursor size exceeds %d bytes", ErrInvalidOptions, defaultMaxCursorBytes)
	}
	if options.MaxErrorDetail > defaultMaxErrorDetail {
		return Options{}, fmt.Errorf("%w: max error detail exceeds %d bytes", ErrInvalidOptions, defaultMaxErrorDetail)
	}
	return options, nil
}

// TriggerResult explains whether a request created a new scan or was
// coalesced onto an active scan/follow-up. The returned record is a detached
// copy and may be safely retained by an API response.
type TriggerResult struct {
	Scan            ScanRecord
	Coalesced       bool
	FollowUpPending bool
}

// Scheduler owns one in-process admission loop. State mutations are serialized
// with commitMu and atomically persisted through StateStore. A single API
// process owns the journal, so this package does not pretend to coordinate
// multiple schedulers against one database.
type Scheduler struct {
	store   StateStore
	runner  ScanRunner
	options Options

	mu       sync.RWMutex
	commitMu sync.Mutex
	state    Snapshot

	started  bool
	stopping bool
	closed   bool
	baseCtx  context.Context
	stop     context.CancelFunc
	wake     chan struct{}
	wg       sync.WaitGroup
	running  map[domain.RuntimeID]context.CancelFunc
}

// New constructs a scheduler without loading or changing durable state. Start
// performs recovery and launches the interval loop; this separation keeps
// construction side-effect free and makes startup failures explicit.
func New(store StateStore, runner ScanRunner, options Options) (*Scheduler, error) {
	if store == nil {
		return nil, ErrStateStoreRequired
	}
	if runner == nil {
		return nil, ErrRunnerRequired
	}
	options, err := normalizeOptions(options)
	if err != nil {
		return nil, err
	}
	return &Scheduler{
		store: store, runner: runner, options: options,
		wake: make(chan struct{}, 1), running: make(map[domain.RuntimeID]context.CancelFunc),
	}, nil
}

// NewScheduler is an explicit constructor alias for dependency wiring.
func NewScheduler(store StateStore, runner ScanRunner, options Options) (*Scheduler, error) {
	return New(store, runner, options)
}

// Start loads the durable snapshot, recovers abandoned running scans to queued
// recovery scans, and starts interval admission. Calling Start twice is
// idempotent; a closed scheduler cannot be restarted.
func (scheduler *Scheduler) Start(ctx context.Context) error {
	if scheduler == nil {
		return ErrSchedulerClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	scheduler.commitMu.Lock()
	defer scheduler.commitMu.Unlock()
	scheduler.mu.RLock()
	started, stopping, closed := scheduler.started, scheduler.stopping, scheduler.closed
	scheduler.mu.RUnlock()
	if closed {
		return ErrSchedulerClosed
	}
	if stopping {
		return ErrSchedulerClosed
	}
	if started {
		return nil
	}

	loaded, err := scheduler.store.Load(ctx)
	if err != nil {
		return fmt.Errorf("load scanning state: %w", err)
	}
	if err := loaded.Validate(scheduler.options.MinimumInterval); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	candidate := cloneSnapshot(loaded)
	changed := scheduler.recoverSnapshot(&candidate, nowUTC(scheduler.options.Now))
	if changed {
		candidate.Version = loaded.Version + 1
		if err := candidate.Validate(scheduler.options.MinimumInterval); err != nil {
			return fmt.Errorf("%w after recovery: %v", ErrInvalidSnapshot, err)
		}
		if err := scheduler.store.Save(ctx, candidate); err != nil {
			return fmt.Errorf("persist scanning recovery: %w", err)
		}
	}
	scheduler.mu.Lock()
	scheduler.state = candidate
	scheduler.baseCtx, scheduler.stop = context.WithCancel(ctx)
	scheduler.started = true
	scheduler.mu.Unlock()

	scheduler.wg.Add(1)
	go scheduler.loop()
	scheduler.signal()
	return nil
}

// Stop requests cancellation of the scheduler and all active runner contexts,
// then waits for durable terminal updates. It is safe to call more than once.
func (scheduler *Scheduler) Stop(ctx context.Context) error {
	if scheduler == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	scheduler.commitMu.Lock()
	scheduler.mu.Lock()
	if scheduler.closed {
		scheduler.mu.Unlock()
		scheduler.commitMu.Unlock()
		return nil
	}
	if !scheduler.started {
		scheduler.closed = true
		scheduler.mu.Unlock()
		scheduler.commitMu.Unlock()
		return nil
	}
	scheduler.stopping = true
	stop := scheduler.stop
	cancellations := make([]context.CancelFunc, 0, len(scheduler.running))
	for _, cancel := range scheduler.running {
		cancellations = append(cancellations, cancel)
	}
	scheduler.mu.Unlock()
	scheduler.commitMu.Unlock()
	if stop != nil {
		stop()
	}
	for _, cancel := range cancellations {
		cancel()
	}
	done := make(chan struct{})
	go func() {
		scheduler.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		scheduler.mu.Lock()
		scheduler.closed = true
		scheduler.stopping = false
		scheduler.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close is the conventional shutdown alias. The background wait is bounded
// only by runner cooperation with context cancellation, as required by the
// runner contract.
func (scheduler *Scheduler) Close() error { return scheduler.Stop(context.Background()) }

func (scheduler *Scheduler) loop() {
	defer scheduler.wg.Done()
	scheduler.mu.RLock()
	ctx := scheduler.baseCtx
	interval := scheduler.options.PollInterval
	scheduler.mu.RUnlock()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-scheduler.wake:
			_ = scheduler.Tick(context.Background())
		case <-ticker.C:
			_ = scheduler.Tick(context.Background())
		}
	}
}

func (scheduler *Scheduler) signal() {
	if scheduler == nil {
		return
	}
	select {
	case scheduler.wake <- struct{}{}:
	default:
	}
}

// Snapshot returns a detached view of all durable scheduler state. It is safe
// to call while scans are running and never exposes mutable internal slices.
func (scheduler *Scheduler) Snapshot() Snapshot {
	if scheduler == nil {
		return Snapshot{}
	}
	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()
	return cloneSnapshot(scheduler.state)
}

// Root returns one detached root projection. The boolean is false when the
// root is absent from the current configured snapshot.
func (scheduler *Scheduler) Root(rootID domain.ConfigID) (RootState, bool) {
	if scheduler == nil {
		return RootState{}, false
	}
	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()
	index := rootIndex(scheduler.state.Roots, rootID)
	if index < 0 {
		return RootState{}, false
	}
	return cloneRootState(scheduler.state.Roots[index]), true
}

// Scan returns one detached scan record. It is suitable for an API read path
// and never exposes the scheduler's mutable slices or timestamp pointers.
func (scheduler *Scheduler) Scan(scanID domain.RuntimeID) (ScanRecord, bool) {
	if scheduler == nil {
		return ScanRecord{}, false
	}
	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()
	index := scanIndex(scheduler.state.Scans, scanID)
	if index < 0 {
		return ScanRecord{}, false
	}
	return cloneScanRecord(scheduler.state.Scans[index]), true
}

// ListScans returns detached records in durable creation order. Passing an
// empty root ID lists all roots; a non-empty ID narrows the read without
// changing scheduling state.
func (scheduler *Scheduler) ListScans(rootID domain.ConfigID) []ScanRecord {
	if scheduler == nil {
		return nil
	}
	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()
	result := make([]ScanRecord, 0, len(scheduler.state.Scans))
	for _, scan := range scheduler.state.Scans {
		if rootID != "" && scan.RootID != rootID {
			continue
		}
		result = append(result, cloneScanRecord(scan))
	}
	return result
}

// ConfigureRoots atomically reconciles configured root schedules. Removed
// roots become retired history and are never deleted; active work is allowed
// to finish, while no new scheduled work is admitted for a retired root.
func (scheduler *Scheduler) ConfigureRoots(ctx context.Context, schedules []RootSchedule) error {
	if scheduler == nil {
		return ErrSchedulerClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateSchedules(schedules, scheduler.options.MinimumInterval); err != nil {
		return err
	}
	now := nowUTC(scheduler.options.Now)
	err := scheduler.commit(ctx, func(snapshot *Snapshot) error {
		byID := make(map[domain.ConfigID]RootState, len(snapshot.Roots))
		for _, root := range snapshot.Roots {
			byID[root.Schedule.ID] = cloneRootState(root)
		}
		seen := make(map[domain.ConfigID]struct{}, len(schedules))
		for _, schedule := range schedules {
			seen[schedule.ID] = struct{}{}
			root, exists := byID[schedule.ID]
			if !exists {
				root = RootState{Schedule: schedule}
				if schedule.Enabled {
					root.NextScheduledAt = cloneTime(&now)
				}
			} else {
				wasEnabled := root.Schedule.Enabled && !root.Retired
				root.Schedule = schedule
				root.Retired = false
				root.RetiredAt = nil
				if !schedule.Enabled {
					root.NextScheduledAt = nil
				} else if !wasEnabled || root.NextScheduledAt == nil {
					root.NextScheduledAt = cloneTime(&now)
				}
			}
			byID[schedule.ID] = root
		}
		for id, root := range byID {
			if _, exists := seen[id]; exists {
				continue
			}
			if root.Retired {
				continue
			}
			root.Retired = true
			root.Schedule.Enabled = false
			root.NextScheduledAt = nil
			root.RetiredAt = cloneTime(&now)
			byID[id] = root
		}
		snapshot.Roots = snapshot.Roots[:0]
		for _, root := range byID {
			snapshot.Roots = append(snapshot.Roots, root)
		}
		snapshot.Roots = cloneAndSortRoots(snapshot.Roots)
		return nil
	})
	if err == nil {
		scheduler.signal()
		if scheduler.isStarted() {
			scheduler.pump()
		}
	}
	return err
}

// RegisterRoot is a convenience for adding one root without replacing other
// configured schedules. It is useful during bootstrap and in focused tests.
func (scheduler *Scheduler) RegisterRoot(ctx context.Context, schedule RootSchedule) error {
	if scheduler == nil {
		return ErrSchedulerClosed
	}
	snapshot := scheduler.Snapshot()
	schedules := make([]RootSchedule, 0, len(snapshot.Roots)+1)
	for _, root := range snapshot.Roots {
		if !root.Retired {
			schedules = append(schedules, root.Schedule)
		}
	}
	schedules = append(schedules, schedule)
	return scheduler.ConfigureRoots(ctx, schedules)
}

func validateSchedules(schedules []RootSchedule, minimumInterval time.Duration) error {
	seen := make(map[domain.ConfigID]struct{}, len(schedules))
	for index, schedule := range schedules {
		if err := schedule.Validate(minimumInterval); err != nil {
			return fmt.Errorf("%w: schedule %d: %v", ErrInvalidOptions, index, err)
		}
		if _, exists := seen[schedule.ID]; exists {
			return fmt.Errorf("%w: duplicate root %q", ErrInvalidOptions, schedule.ID)
		}
		seen[schedule.ID] = struct{}{}
	}
	return nil
}

func (scheduler *Scheduler) isStarted() bool {
	scheduler.mu.RLock()
	defer scheduler.mu.RUnlock()
	return scheduler.started && !scheduler.stopping && !scheduler.closed
}

// Trigger manually requests a root scan. If the root already has queued,
// running or waiting work, exactly one follow-up is retained; repeated calls
// remain coalesced and do not grow an in-memory or durable queue.
func (scheduler *Scheduler) Trigger(ctx context.Context, rootID domain.ConfigID) (TriggerResult, error) {
	if scheduler == nil {
		return TriggerResult{}, ErrSchedulerClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !rootID.Valid() {
		return TriggerResult{}, fmt.Errorf("%w: invalid root id", ErrRootNotFound)
	}
	now := nowUTC(scheduler.options.Now)
	var result TriggerResult
	err := scheduler.commit(ctx, func(snapshot *Snapshot) error {
		rootIndex := rootIndex(snapshot.Roots, rootID)
		if rootIndex < 0 {
			return ErrRootNotFound
		}
		root := &snapshot.Roots[rootIndex]
		if root.Retired {
			return ErrRootRetired
		}
		if activeIndex := activeScanIndex(snapshot.Scans, rootID); activeIndex >= 0 {
			active := &snapshot.Scans[activeIndex]
			if active.State == StateWaiting {
				active.NextAttemptAt = cloneTime(&now)
				result.Coalesced = true
			} else {
				root.FollowUpPending = true
			}
			result.FollowUpPending = root.FollowUpPending
			result.Scan = cloneScanRecord(*active)
			return nil
		}
		record := newScanRecord(root.Schedule, TriggerManual, now)
		snapshot.Scans = append(snapshot.Scans, record)
		root.ActiveScanID = record.ID
		result.Scan = cloneScanRecord(record)
		return nil
	})
	if err == nil {
		scheduler.signal()
		if scheduler.isStarted() {
			scheduler.pump()
		}
	}
	return result, err
}

// Cancel requests cancellation of one scan. Queued work is finalized
// immediately; running work is marked durably before its context is canceled,
// so a late successful runner result cannot erase the cancellation request.
func (scheduler *Scheduler) Cancel(ctx context.Context, scanID domain.RuntimeID) error {
	if scheduler == nil {
		return ErrSchedulerClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !scanID.Valid() {
		return ErrScanNotFound
	}
	now := nowUTC(scheduler.options.Now)
	var cancel context.CancelFunc
	err := scheduler.commit(ctx, func(snapshot *Snapshot) error {
		scanIndex := scanIndex(snapshot.Scans, scanID)
		if scanIndex < 0 {
			return ErrScanNotFound
		}
		scan := &snapshot.Scans[scanIndex]
		if scan.State.Terminal() {
			return nil
		}
		if scan.CancellationRequestedAt == nil {
			scan.CancellationRequestedAt = cloneTime(&now)
		}
		rootIndex := rootIndex(snapshot.Roots, scan.RootID)
		if scan.State == StateQueued {
			finalizeCancelled(scan, now, scheduler.options.MaxErrorDetail)
			if rootIndex >= 0 {
				root := &snapshot.Roots[rootIndex]
				updateRootAfterTerminal(root, *scan)
				root.FollowUpPending = false
			}
		} else if scan.State == StateWaiting {
			finalizeCancelled(scan, now, scheduler.options.MaxErrorDetail)
			if rootIndex >= 0 {
				root := &snapshot.Roots[rootIndex]
				updateRootAfterTerminal(root, *scan)
				root.FollowUpPending = false
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	scheduler.mu.RLock()
	cancel = scheduler.running[scanID]
	scheduler.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
	scheduler.signal()
	return nil
}

// Tick runs one deterministic scheduling pass. The background loop invokes
// the same method; exposing it lets tests and bootstrap code advance a fake
// clock without sleeping.
func (scheduler *Scheduler) Tick(ctx context.Context) error {
	if scheduler == nil {
		return ErrSchedulerClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !scheduler.isStarted() {
		return ErrSchedulerNotStarted
	}
	now := nowUTC(scheduler.options.Now)
	err := scheduler.commit(ctx, func(snapshot *Snapshot) error {
		for rootIndex := range snapshot.Roots {
			root := &snapshot.Roots[rootIndex]
			activeIndex := activeScanIndex(snapshot.Scans, root.Schedule.ID)
			if activeIndex >= 0 {
				active := &snapshot.Scans[activeIndex]
				root.ActiveScanID = active.ID
				if active.State == StateWaiting && due(active.NextAttemptAt, now) {
					active.State = StateQueued
					active.NextAttemptAt = nil
					active.UpdatedAt = now
				}
				continue
			}
			if root.Retired || !root.Schedule.Enabled {
				continue
			}
			root.ActiveScanID = ""
			if root.NextScheduledAt == nil {
				root.NextScheduledAt = cloneTime(&now)
			}
			if !due(root.NextScheduledAt, now) {
				continue
			}
			record := newScanRecord(root.Schedule, TriggerScheduled, now)
			snapshot.Scans = append(snapshot.Scans, record)
			root.ActiveScanID = record.ID
			root.NextScheduledAt = timePointer(now.Add(root.Schedule.Interval))
		}
		return nil
	})
	if err != nil {
		return err
	}
	scheduler.pump()
	return nil
}

func due(at *time.Time, now time.Time) bool {
	return at == nil || !at.After(now)
}

func newScanRecord(schedule RootSchedule, trigger TriggerKind, now time.Time) ScanRecord {
	id, err := domain.NewRuntimeID()
	if err != nil {
		// crypto/rand failures are extraordinarily rare and are handled by the
		// caller's snapshot validation. An invalid ID cannot be dispatched.
		return ScanRecord{RootID: schedule.ID, ConfigRevision: schedule.Revision, Trigger: trigger, State: StateQueued, CreatedAt: now, UpdatedAt: now, Coverage: unknownCoverage(schedule.ID, now, "scan_id_unavailable")}
	}
	return ScanRecord{
		ID: id, RootID: schedule.ID, ConfigRevision: schedule.Revision,
		Trigger: trigger, State: StateQueued, CreatedAt: now, UpdatedAt: now,
		Coverage: unknownCoverage(schedule.ID, now, "scan_queued"),
	}
}

func unknownCoverage(rootID domain.ConfigID, observedAt time.Time, reason string) domain.Coverage {
	coverage := domain.Coverage{RootID: rootID, Completeness: domain.CompletenessUnknown, ObservedAt: observedAt}
	appendReason(&coverage, reason)
	return coverage
}

func timePointer(value time.Time) *time.Time {
	copyValue := value
	return &copyValue
}

func rootIndex(roots []RootState, id domain.ConfigID) int {
	for index, root := range roots {
		if root.Schedule.ID == id {
			return index
		}
	}
	return -1
}

func scanIndex(scans []ScanRecord, id domain.RuntimeID) int {
	for index, scan := range scans {
		if scan.ID == id {
			return index
		}
	}
	return -1
}

func activeScanIndex(scans []ScanRecord, rootID domain.ConfigID) int {
	for index, scan := range scans {
		if scan.RootID == rootID && !scan.State.Terminal() {
			return index
		}
	}
	return -1
}

func updateRootAfterTerminal(root *RootState, scan ScanRecord) {
	if root == nil || !scan.State.Terminal() {
		return
	}
	if root.ActiveScanID == scan.ID {
		root.ActiveScanID = ""
	}
	root.LastScanID = scan.ID
	lastCoverage := cloneCoverage(scan.Coverage)
	root.LastCoverage = &lastCoverage
	if scan.CanAssertAbsence() {
		root.LastCompleteScanID = scan.ID
		completeCoverage := cloneCoverage(scan.Coverage)
		root.LastCompleteCoverage = &completeCoverage
	}
}

func (scheduler *Scheduler) recoverSnapshot(snapshot *Snapshot, now time.Time) bool {
	changed := false
	for index := range snapshot.Scans {
		scan := &snapshot.Scans[index]
		if scan.State != StateRunning {
			continue
		}
		if scan.CancellationRequestedAt != nil {
			finalizeCancelled(scan, now, scheduler.options.MaxErrorDetail)
			if rootIndex := rootIndex(snapshot.Roots, scan.RootID); rootIndex >= 0 {
				root := &snapshot.Roots[rootIndex]
				updateRootAfterTerminal(root, *scan)
				root.FollowUpPending = false
			}
			changed = true
			continue
		}
		scan.State = StateQueued
		scan.Trigger = TriggerRecovery
		scan.UpdatedAt = now
		scan.ErrorCode = "recovered_after_restart"
		scan.ErrorDetail = "previous process stopped while the scan was running"
		changed = true
	}
	for rootIndex := range snapshot.Roots {
		root := &snapshot.Roots[rootIndex]
		activeIndex := activeScanIndex(snapshot.Scans, root.Schedule.ID)
		activeID := domain.RuntimeID("")
		if activeIndex >= 0 {
			activeID = snapshot.Scans[activeIndex].ID
		}
		if root.ActiveScanID != activeID {
			root.ActiveScanID = activeID
			changed = true
		}
		if root.Schedule.Enabled && !root.Retired && root.NextScheduledAt == nil {
			root.NextScheduledAt = cloneTime(&now)
			changed = true
		}
	}
	return changed
}

// commit serializes a state transition and publishes one complete snapshot.
// The state lock is released before Store.Save so a storage adapter may safely
// perform callbacks or blocking I/O; commitMu still keeps saves ordered. A
// failed save rolls memory back to the exact prior snapshot.
func (scheduler *Scheduler) commit(ctx context.Context, change func(*Snapshot) error) error {
	return scheduler.commitState(ctx, false, change)
}

// commitDuringStop is used only by a runner which is finishing after Stop has
// requested cancellation. Keeping this narrow escape hatch lets shutdown
// persist the terminal result while public mutations fail closed during the
// stopping window.
func (scheduler *Scheduler) commitDuringStop(ctx context.Context, change func(*Snapshot) error) error {
	return scheduler.commitState(ctx, true, change)
}

func (scheduler *Scheduler) commitState(ctx context.Context, allowStopping bool, change func(*Snapshot) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	scheduler.commitMu.Lock()
	defer scheduler.commitMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	scheduler.mu.RLock()
	if scheduler.closed || (scheduler.stopping && !allowStopping) {
		scheduler.mu.RUnlock()
		return ErrSchedulerClosed
	}
	previous := cloneSnapshot(scheduler.state)
	scheduler.mu.RUnlock()
	candidate := cloneSnapshot(previous)
	if err := change(&candidate); err != nil {
		return err
	}
	if reflect.DeepEqual(previous, candidate) {
		return nil
	}
	candidate.Version = previous.Version + 1
	if err := candidate.Validate(scheduler.options.MinimumInterval); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	if err := scheduler.store.Save(ctx, candidate); err != nil {
		return fmt.Errorf("persist scanning state: %w", err)
	}
	scheduler.mu.Lock()
	scheduler.state = candidate
	scheduler.mu.Unlock()
	return nil
}

func (scheduler *Scheduler) pump() {
	if scheduler == nil || !scheduler.isStarted() {
		return
	}
	for scheduler.startOne() {
	}
}

// startOne claims one queued record under the same serialized transition used
// by public mutations. It returns false when no capacity or runnable scan is
// available. The runner starts only after durable StateRunning publication.
func (scheduler *Scheduler) startOne() bool {
	scheduler.commitMu.Lock()
	defer scheduler.commitMu.Unlock()

	scheduler.mu.Lock()
	if !scheduler.started || scheduler.stopping || scheduler.closed || len(scheduler.running) >= scheduler.options.MaxConcurrent {
		scheduler.mu.Unlock()
		return false
	}
	candidate := cloneSnapshot(scheduler.state)
	selected := selectQueuedScan(candidate)
	if selected < 0 {
		scheduler.mu.Unlock()
		return false
	}
	scan := &candidate.Scans[selected]
	rootIndex := rootIndex(candidate.Roots, scan.RootID)
	if rootIndex < 0 || candidate.Roots[rootIndex].Retired {
		// Retired roots retain history but queued work cannot run. Finalize it
		// durably and let the next pump consider other roots.
		now := nowUTC(scheduler.options.Now)
		finalizeCancelled(scan, now, scheduler.options.MaxErrorDetail)
		if rootIndex >= 0 {
			root := &candidate.Roots[rootIndex]
			updateRootAfterTerminal(root, *scan)
			root.FollowUpPending = false
		}
		published := withVersion(candidate, scheduler.state.Version+1)
		scheduler.mu.Unlock()
		if err := scheduler.store.Save(context.Background(), published); err != nil {
			return false
		}
		scheduler.mu.Lock()
		scheduler.state = published
		scheduler.mu.Unlock()
		return true
	}
	root := candidate.Roots[rootIndex]
	if scan.CancellationRequestedAt != nil {
		now := nowUTC(scheduler.options.Now)
		finalizeCancelled(scan, now, scheduler.options.MaxErrorDetail)
		root := &candidate.Roots[rootIndex]
		updateRootAfterTerminal(root, *scan)
		root.FollowUpPending = false
		published := withVersion(candidate, scheduler.state.Version+1)
		scheduler.mu.Unlock()
		if err := scheduler.store.Save(context.Background(), published); err != nil {
			return false
		}
		scheduler.mu.Lock()
		scheduler.state = published
		scheduler.mu.Unlock()
		return true
	}

	now := nowUTC(scheduler.options.Now)
	if scan.StartedAt == nil {
		scan.StartedAt = timePointer(now)
	}
	scan.State = StateRunning
	scan.Attempt++
	scan.UpdatedAt = now
	scan.NextAttemptAt = nil
	if scan.ErrorCode == "scan_queued" || scan.ErrorCode == "recovered_after_restart" {
		scan.ErrorCode = ""
		scan.ErrorDetail = ""
	}
	var runCtx context.Context
	var cancel context.CancelFunc
	if scheduler.options.RunTimeout > 0 {
		runCtx, cancel = context.WithTimeout(scheduler.baseCtx, scheduler.options.RunTimeout)
	} else {
		runCtx, cancel = context.WithCancel(scheduler.baseCtx)
	}
	scheduler.running[scan.ID] = cancel
	published := withVersion(candidate, scheduler.state.Version+1)
	scheduler.mu.Unlock()

	if err := published.Validate(scheduler.options.MinimumInterval); err != nil {
		cancel()
		scheduler.mu.Lock()
		delete(scheduler.running, scan.ID)
		scheduler.mu.Unlock()
		return false
	}
	if err := scheduler.store.Save(context.Background(), published); err != nil {
		cancel()
		scheduler.mu.Lock()
		delete(scheduler.running, scan.ID)
		scheduler.mu.Unlock()
		return false
	}
	scheduler.mu.Lock()
	// A public cancellation can race the durable claim. Preserve it if it was
	// recorded while Save was in flight; the runner will see cancellation below.
	scheduler.state = published
	claimed := cloneScanRecord(published.Scans[scanIndex(published.Scans, scan.ID)])
	scheduler.mu.Unlock()

	scheduler.wg.Add(1)
	go scheduler.execute(runCtx, cancel, root.Schedule, claimed)
	return true
}

func withVersion(snapshot Snapshot, version uint64) Snapshot {
	snapshot.Version = version
	return snapshot
}

func selectQueuedScan(snapshot Snapshot) int {
	indices := make([]int, 0)
	for index, scan := range snapshot.Scans {
		if scan.State == StateQueued {
			indices = append(indices, index)
		}
	}
	sort.SliceStable(indices, func(left, right int) bool {
		a, b := snapshot.Scans[indices[left]], snapshot.Scans[indices[right]]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID < b.ID
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	if len(indices) == 0 {
		return -1
	}
	return indices[0]
}

func (scheduler *Scheduler) execute(ctx context.Context, cancel context.CancelFunc, root RootSchedule, scan ScanRecord) {
	defer scheduler.wg.Done()
	defer cancel()
	checkpoint := ScanCheckpoint{
		ScanID: scan.ID, RootID: scan.RootID, ConfigRevision: scan.ConfigRevision,
		Cursor: scan.Cursor, ObservedCount: scan.ObservedCount,
		Coverage: cloneCoverage(scan.Coverage), SourceCoverage: cloneCoverages(scan.SourceCoverage),
	}
	var (
		outcome ScanResult
		runErr  error
	)
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				runErr = fmt.Errorf("scan runner panic: %v", recovered)
			}
		}()
		outcome, runErr = scheduler.runner.Run(ctx, root, checkpoint, func(progress ScanProgress) error {
			return scheduler.reportProgress(ctx, scan.ID, progress)
		})
	}()
	_ = scheduler.finish(scan.ID, outcome, runErr, ctx)
}

func (scheduler *Scheduler) reportProgress(ctx context.Context, scanID domain.RuntimeID, progress ScanProgress) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if len(progress.Cursor) > scheduler.options.MaxCursorBytes {
		return fmt.Errorf("%w: cursor exceeds %d bytes", ErrScanProgressInvalid, scheduler.options.MaxCursorBytes)
	}
	if progress.ObservedCount < 0 {
		return fmt.Errorf("%w: observed count is negative", ErrScanProgressInvalid)
	}
	err := scheduler.commit(ctx, func(snapshot *Snapshot) error {
		index := scanIndex(snapshot.Scans, scanID)
		if index < 0 {
			return ErrScanNotFound
		}
		scan := &snapshot.Scans[index]
		if scan.State != StateRunning {
			return ErrScanNotRunning
		}
		if progress.ObservedCount < scan.ObservedCount {
			return ErrScanProgressRewound
		}
		now := nowUTC(scheduler.options.Now)
		coverage := progress.Coverage
		if coverage.Completeness == "" {
			coverage = cloneCoverage(scan.Coverage)
		}
		sources := progress.SourceCoverage
		if sources == nil {
			sources = cloneCoverages(scan.SourceCoverage)
		}
		normalizedCoverage, normalizedSources, err := scheduler.normalizeEvidence(scan.RootID, coverage, sources, progress.Cursor, progress.ObservedCount, scan.StartedAt, now)
		if err != nil {
			return err
		}
		scan.Cursor = progress.Cursor
		scan.ObservedCount = progress.ObservedCount
		scan.Coverage = normalizedCoverage
		scan.SourceCoverage = normalizedSources
		scan.UpdatedAt = now
		return nil
	})
	return err
}

// finish converts one runner invocation into a durable state transition. A
// cancellation request or context termination always wins over a late success
// result. A cursor returned without an error becomes a waiting continuation;
// the next scheduler pass requeues that same scan record rather than creating a
// duplicate root scan.
func (scheduler *Scheduler) finish(scanID domain.RuntimeID, outcome ScanResult, runErr error, runCtx context.Context) error {
	now := nowUTC(scheduler.options.Now)
	var shouldSignal bool
	err := scheduler.commitDuringStop(context.Background(), func(snapshot *Snapshot) error {
		index := scanIndex(snapshot.Scans, scanID)
		if index < 0 {
			return ErrScanNotFound
		}
		scan := &snapshot.Scans[index]
		if scan.State != StateRunning {
			return ErrScanNotRunning
		}
		rootIndex := rootIndex(snapshot.Roots, scan.RootID)
		if rootIndex < 0 {
			return fmt.Errorf("%w: scan root disappeared", ErrInvalidSnapshot)
		}
		observedCount := outcome.ObservedCount
		if observedCount < scan.ObservedCount {
			observedCount = scan.ObservedCount
		}
		cursor := outcome.Cursor
		coverage := outcome.Coverage
		if coverage.Completeness == "" {
			coverage = cloneCoverage(scan.Coverage)
		}
		sources := outcome.SourceCoverage
		if sources == nil {
			sources = cloneCoverages(scan.SourceCoverage)
		}
		coverage, sources, err := scheduler.normalizeEvidence(scan.RootID, coverage, sources, cursor, observedCount, scan.StartedAt, now)
		if err != nil {
			return err
		}
		scan.Cursor = cursor
		scan.ObservedCount = observedCount
		scan.Coverage = coverage
		scan.SourceCoverage = sources
		scan.ErrorCode = ""
		scan.ErrorDetail = ""

		cancelRequested := scan.CancellationRequestedAt != nil
		ctxErr := contextError(runCtx)
		deadline := errors.Is(ctxErr, context.DeadlineExceeded) || errors.Is(runErr, context.DeadlineExceeded)
		cancelled := cancelRequested || errors.Is(ctxErr, context.Canceled) || errors.Is(runErr, context.Canceled)
		switch {
		case cancelled:
			finalizeCancelled(scan, now, scheduler.options.MaxErrorDetail)
		case deadline:
			finalizeDeadline(scan, now, scheduler.options.MaxErrorDetail)
		case errors.Is(runErr, ErrRetryable):
			scan.State = StateWaiting
			scan.NextAttemptAt = retryAt(outcome.NextAttemptAt, now, scheduler.options.RetryBackoff)
			scan.ErrorCode = "retryable"
			scan.ErrorDetail = sanitizeError(runErr, scheduler.options.MaxErrorDetail)
			shouldSignal = true
		case runErr != nil:
			scan.State = StateFailed
			scan.CompletedAt = timePointer(now)
			scan.ErrorCode = "runner_failed"
			scan.ErrorDetail = sanitizeError(runErr, scheduler.options.MaxErrorDetail)
		default:
			if cursor != "" {
				scan.State = StateWaiting
				scan.NextAttemptAt = continuationAt(outcome.NextAttemptAt, now)
				scan.ErrorCode = "continuation_pending"
				shouldSignal = true
			} else {
				scan.State = StateSucceeded
				scan.NextAttemptAt = nil
				scan.CompletedAt = timePointer(now)
			}
		}
		if scan.State == StateWaiting {
			scan.CompletedAt = nil
		} else if !scan.State.Terminal() {
			scan.CompletedAt = nil
		}
		scan.UpdatedAt = now

		if scan.State.Terminal() {
			root := &snapshot.Roots[rootIndex]
			updateRootAfterTerminal(root, *scan)
			if root.FollowUpPending && !root.Retired && scan.State != StateCancelled && scan.State != StateDeadlineExceeded {
				followUp := newScanRecord(root.Schedule, TriggerManual, now)
				snapshot.Scans = append(snapshot.Scans, followUp)
				root.ActiveScanID = followUp.ID
				root.FollowUpPending = false
				shouldSignal = true
			} else {
				root.FollowUpPending = false
			}
		}
		return nil
	})
	scheduler.mu.Lock()
	delete(scheduler.running, scanID)
	scheduler.mu.Unlock()
	if err == nil && shouldSignal {
		scheduler.signal()
	}
	if err == nil {
		scheduler.pump()
	}
	return err
}

func retryAt(requested *time.Time, now time.Time, backoff time.Duration) *time.Time {
	if requested != nil && requested.After(now) {
		return cloneTime(requested)
	}
	return timePointer(now.Add(backoff))
}

func continuationAt(requested *time.Time, now time.Time) *time.Time {
	if requested != nil && requested.After(now) {
		return cloneTime(requested)
	}
	return timePointer(now)
}

func (scheduler *Scheduler) normalizeEvidence(rootID domain.ConfigID, coverage domain.Coverage, sources []domain.Coverage, cursor string, observedCount int64, startedAt *time.Time, now time.Time) (domain.Coverage, []domain.Coverage, error) {
	if len(cursor) > scheduler.options.MaxCursorBytes {
		return domain.Coverage{}, nil, fmt.Errorf("%w: cursor exceeds %d bytes", ErrScanProgressInvalid, scheduler.options.MaxCursorBytes)
	}
	if observedCount < 0 {
		return domain.Coverage{}, nil, fmt.Errorf("%w: observed count is negative", ErrScanProgressInvalid)
	}
	if coverage.RootID == "" {
		coverage.RootID = rootID
	}
	if coverage.RootID != rootID {
		return domain.Coverage{}, nil, fmt.Errorf("%w: coverage root does not match scan root", ErrScanProgressInvalid)
	}
	if coverage.Completeness == "" {
		coverage.Completeness = domain.CompletenessUnknown
	}
	if coverage.ObservedAt.IsZero() {
		coverage.ObservedAt = now
	}
	if coverage.ObservedCount < observedCount {
		coverage.ObservedCount = observedCount
	}
	if coverage.StartedAt == nil && startedAt != nil {
		coverage.StartedAt = cloneTime(startedAt)
	}
	if coverage.StartedAt == nil {
		coverage.StartedAt = timePointer(now)
	}
	if coverage.Completeness == domain.CompletenessComplete && coverage.CompletedAt == nil {
		coverage.CompletedAt = timePointer(now)
	}
	if coverage.CompletedAt != nil && coverage.CompletedAt.After(coverage.ObservedAt) {
		coverage.ObservedAt = *coverage.CompletedAt
	}
	if cursor != "" && coverage.Completeness == domain.CompletenessComplete {
		coverage.Completeness = domain.CompletenessPartial
		appendReason(&coverage, "continuation_pending")
	}
	for index := range sources {
		source := &sources[index]
		if source.RootID == "" {
			source.RootID = rootID
		} else if source.RootID != rootID {
			return domain.Coverage{}, nil, fmt.Errorf("%w: source coverage %d belongs to another root", ErrScanProgressInvalid, index)
		}
		if source.Completeness == "" {
			source.Completeness = domain.CompletenessUnknown
		}
		if source.ObservedAt.IsZero() {
			source.ObservedAt = now
		}
		if source.Completeness == domain.CompletenessComplete {
			if source.StartedAt == nil {
				source.StartedAt = timePointer(now)
			}
			if source.CompletedAt == nil {
				source.CompletedAt = timePointer(now)
			}
			if source.CompletedAt.After(source.ObservedAt) {
				source.ObservedAt = *source.CompletedAt
			}
		}
		if source.ObservedCount < observedCount {
			// Aggregate count may include more than one source, so only ensure
			// a source cannot report an impossible negative value below.
			if source.ObservedCount < 0 {
				source.ObservedCount = 0
			}
		}
		if err := source.Validate(); err != nil {
			return domain.Coverage{}, nil, fmt.Errorf("%w: source coverage %d: %v", ErrScanProgressInvalid, index, err)
		}
		switch source.Completeness {
		case domain.CompletenessUnknown:
			coverage.Completeness = domain.CompletenessUnknown
			appendReason(&coverage, "source_coverage_unknown")
		case domain.CompletenessPartial:
			if coverage.Completeness == domain.CompletenessComplete {
				coverage.Completeness = domain.CompletenessPartial
			}
			appendReason(&coverage, "source_coverage_partial")
		}
	}
	if err := coverage.Validate(); err != nil {
		return domain.Coverage{}, nil, fmt.Errorf("%w: coverage: %v", ErrScanProgressInvalid, err)
	}
	return coverage, cloneCoverages(sources), nil
}

func finalizeCancelled(scan *ScanRecord, now time.Time, maxDetail int) {
	scan.State = StateCancelled
	scan.CompletedAt = timePointer(now)
	scan.NextAttemptAt = nil
	scan.ErrorCode = "cancelled"
	scan.ErrorDetail = sanitizeError(errors.New("scan cancellation requested"), maxDetail)
	if scan.Coverage.Completeness == domain.CompletenessComplete {
		scan.Coverage.Completeness = domain.CompletenessPartial
		appendReason(&scan.Coverage, "scan_cancelled")
	}
}

func finalizeDeadline(scan *ScanRecord, now time.Time, maxDetail int) {
	scan.State = StateDeadlineExceeded
	scan.CompletedAt = timePointer(now)
	scan.NextAttemptAt = nil
	scan.ErrorCode = "deadline_exceeded"
	scan.ErrorDetail = sanitizeError(context.DeadlineExceeded, maxDetail)
	if scan.Coverage.Completeness == domain.CompletenessComplete {
		scan.Coverage.Completeness = domain.CompletenessPartial
		appendReason(&scan.Coverage, "scan_deadline_exceeded")
	}
}
