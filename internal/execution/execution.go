// Package execution implements Mastarr's durable action process manager.
//
// The executor owns local scheduling and journal transitions. Handlers own the
// meaning of a desired state and the upstream/filesystem calls used to make it
// true. The two concerns are deliberately separated so an action can be run
// directly or as one ordered workflow step without a second execution path.
package execution

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guilycst/mastarr/internal/domain"
	"github.com/guilycst/mastarr/internal/storage/sqlc"
)

var (
	// ErrNoHandler means that a persisted action kind has no registered typed
	// implementation. Such work is held for review and is never dispatched.
	ErrNoHandler = errors.New("execution handler is not registered")
	// ErrLeaseLost means that the worker no longer owns the claimed action.
	// Callers must not dispatch after receiving this error.
	ErrLeaseLost = errors.New("execution lease was lost")
	// ErrReservationConflict means another Mastarr action owns an overlapping
	// resource reservation in this executor process.
	ErrReservationConflict = errors.New("execution reservation is held by another action")
	// ErrInvalidJournal means that persisted state is not safe to interpret.
	ErrInvalidJournal = errors.New("execution journal is invalid")
)

// FailureKind classifies a handler failure at the dispatch boundary. The
// distinction between not-dispatched and uncertain is safety-critical: only a
// proven pre-dispatch dependency failure may be scheduled for mutation retry.
type FailureKind string

const (
	FailureDependency FailureKind = "dependency_unavailable"
	FailureConflict   FailureKind = "conflict"
	FailureInvalid    FailureKind = "invalid"
	FailureUncertain  FailureKind = "uncertain"
	FailureCancelled  FailureKind = "cancelled"
)

// Failure is a typed handler error. Dispatched is advisory evidence from the
// handler; the executor still treats an unknown result after dispatch as
// uncertain unless a read-only reconciliation proves otherwise.
type Failure struct {
	Kind       FailureKind
	Err        error
	Dispatched bool
}

func (failure *Failure) Error() string {
	if failure == nil {
		return "<nil>"
	}
	if failure.Err == nil {
		return string(failure.Kind)
	}
	return fmt.Sprintf("%s: %v", failure.Kind, failure.Err)
}

func (failure *Failure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Err
}

// NewFailure annotates an error for the executor.
func NewFailure(kind FailureKind, err error) error {
	if err == nil {
		err = errors.New(string(kind))
	}
	return &Failure{Kind: kind, Err: err}
}

// NewDispatchedFailure annotates an error that occurred after a handler may
// have sent its write. It always enters read-only reconciliation.
func NewDispatchedFailure(kind FailureKind, err error) error {
	if err == nil {
		err = errors.New(string(kind))
	}
	return &Failure{Kind: kind, Err: err, Dispatched: true}
}

func failureKind(err error) FailureKind {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure.Kind
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return FailureUncertain
	}
	return FailureInvalid
}

func failureWasDispatched(err error) bool {
	var failure *Failure
	return errors.As(err, &failure) && failure.Dispatched
}

// ObserveState is the result of a read-only desired-state check.
type ObserveState string

const (
	ObserveSatisfied   ObserveState = "satisfied"
	ObserveNeedsAction ObserveState = "needs_action"
	ObserveUnknown     ObserveState = "unknown"
)

func (state ObserveState) valid() bool {
	return state == ObserveSatisfied || state == ObserveNeedsAction || state == ObserveUnknown
}

// AttemptPhase identifies the ordered phase recorded in the attempt journal.
type AttemptPhase string

const (
	AttemptObserve   AttemptPhase = "observe"
	AttemptDispatch  AttemptPhase = "dispatch"
	AttemptReconcile AttemptPhase = "reconcile"
)

func (phase AttemptPhase) valid() bool {
	return phase == AttemptObserve || phase == AttemptDispatch || phase == AttemptReconcile
}

// OutcomeCertainty records what is known about an external effect.
type OutcomeCertainty string

const (
	CertaintyNotDispatched OutcomeCertainty = "not_dispatched"
	CertaintyKnown         OutcomeCertainty = "known"
	CertaintyUncertain     OutcomeCertainty = "uncertain"
)

func (certainty OutcomeCertainty) valid() bool {
	return certainty == CertaintyNotDispatched || certainty == CertaintyKnown || certainty == CertaintyUncertain
}

// EffectState is the local evidence state for one exact target.
type EffectState string

const (
	EffectPending          EffectState = "pending"
	EffectApplied          EffectState = "applied"
	EffectAlreadySatisfied EffectState = "already_satisfied"
	EffectFailed           EffectState = "failed"
	EffectCancelled        EffectState = "cancelled"
	EffectUnknown          EffectState = "unknown"
)

func (state EffectState) valid() bool {
	switch state {
	case EffectPending, EffectApplied, EffectAlreadySatisfied, EffectFailed, EffectCancelled, EffectUnknown:
		return true
	default:
		return false
	}
}

// Action is the generated-query-independent representation passed to typed
// handlers. DesiredState is immutable bytes from the approved plan.
type Action struct {
	ID                      string
	PlanID                  string
	PlanRevision            int64
	PlanDigest              string
	Kind                    domain.ActionKind
	State                   domain.ActionState
	DesiredState            json.RawMessage
	NextAttemptAt           string
	DeadlineAt              string
	CancellationRequestedAt string
	ClaimedBy               string
	LeaseUntil              string
	Version                 int64
	Outcome                 json.RawMessage
	UnresolvedCount         int64
	CreatedAt               string
	UpdatedAt               string
}

func (action Action) validate() error {
	if strings.TrimSpace(action.ID) == "" || strings.TrimSpace(action.PlanID) == "" || strings.TrimSpace(action.PlanDigest) == "" {
		return fmt.Errorf("%w: action identity is incomplete", ErrInvalidJournal)
	}
	if action.PlanRevision <= 0 || action.Version <= 0 {
		return fmt.Errorf("%w: action revision/version is invalid", ErrInvalidJournal)
	}
	if !action.Kind.Valid() {
		return fmt.Errorf("%w: unsupported action kind %q", ErrInvalidJournal, action.Kind)
	}
	if !action.State.Valid() {
		return fmt.Errorf("%w: unsupported action state %q", ErrInvalidJournal, action.State)
	}
	if !json.Valid(action.DesiredState) || !json.Valid(action.Outcome) {
		return fmt.Errorf("%w: action JSON is invalid", ErrInvalidJournal)
	}
	if action.UnresolvedCount < 0 {
		return fmt.Errorf("%w: unresolved count is negative", ErrInvalidJournal)
	}
	return nil
}

// Plan identifies the typed handler for an action without exposing sqlc rows.
type Plan struct {
	ID              string
	Kind            domain.ActionKind
	State           string
	CurrentRevision int64
	CurrentDigest   string
}

func (plan Plan) validate() error {
	if strings.TrimSpace(plan.ID) == "" || !plan.Kind.Valid() || strings.TrimSpace(plan.State) == "" {
		return fmt.Errorf("%w: invalid action plan", ErrInvalidJournal)
	}
	return nil
}

// Attempt is one ordered journal entry. It is safe to expose to handlers for
// operation IDs and never contains a raw upstream response.
type Attempt struct {
	ID               string
	ActionRunID      string
	AttemptNumber    int64
	Phase            AttemptPhase
	State            domain.AttemptState
	StartedAt        string
	FinishedAt       string
	ErrorCode        string
	ErrorDetail      string
	OutcomeCertainty OutcomeCertainty
	ExternalID       string
	Evidence         json.RawMessage
}

func (attempt Attempt) validate() error {
	if strings.TrimSpace(attempt.ID) == "" || strings.TrimSpace(attempt.ActionRunID) == "" || attempt.AttemptNumber <= 0 {
		return fmt.Errorf("%w: attempt identity is invalid", ErrInvalidJournal)
	}
	if !attempt.Phase.valid() || !attempt.State.Valid() || !attempt.OutcomeCertainty.valid() {
		return fmt.Errorf("%w: attempt state is invalid", ErrInvalidJournal)
	}
	if strings.TrimSpace(attempt.StartedAt) == "" || !json.Valid(attempt.Evidence) {
		return fmt.Errorf("%w: attempt evidence is invalid", ErrInvalidJournal)
	}
	return nil
}

// Effect is one exact target in the ordered effect journal.
type Effect struct {
	ID          string
	ActionRunID string
	AttemptID   string
	Ordinal     int64
	TargetKind  string
	TargetID    string
	EffectKind  string
	State       EffectState
	Evidence    json.RawMessage
	ObservedAt  string
}

func (effect Effect) validate() error {
	if strings.TrimSpace(effect.ID) == "" || strings.TrimSpace(effect.ActionRunID) == "" || effect.Ordinal < 0 || strings.TrimSpace(effect.TargetKind) == "" || strings.TrimSpace(effect.TargetID) == "" || strings.TrimSpace(effect.EffectKind) == "" {
		return fmt.Errorf("%w: effect identity is invalid", ErrInvalidJournal)
	}
	if !effect.State.valid() || !json.Valid(effect.Evidence) || strings.TrimSpace(effect.ObservedAt) == "" {
		return fmt.Errorf("%w: effect evidence is invalid", ErrInvalidJournal)
	}
	return nil
}

// Observation is a read-only desired-state result.
type Observation struct {
	State    ObserveState
	Evidence []string
	Effects  []Effect
}

func (observation Observation) validate(actionID string) error {
	if !observation.State.valid() {
		return fmt.Errorf("%w: invalid observation state %q", ErrInvalidJournal, observation.State)
	}
	normalizeReportedEffects(observation.Effects, actionID)
	return validateEffects(observation.Effects, actionID)
}

// DispatchResult contains handler evidence after a mutation call returns.
// The executor still performs a fresh Observe before recording success.
type DispatchResult struct {
	Accepted   bool
	Outcome    domain.EffectOutcome
	ExternalID string
	Evidence   []string
	Effects    []Effect
}

func (result DispatchResult) validate(actionID string) error {
	if !result.Accepted || !result.Outcome.Valid() {
		return fmt.Errorf("%w: dispatch result must state accepted and valid outcome", ErrInvalidJournal)
	}
	normalizeReportedEffects(result.Effects, actionID)
	return validateEffects(result.Effects, actionID)
}

// ReconcileResult is read-only evidence after a lost or recovered dispatch.
// SafeToRetry is allowed only when the handler proves that no desired effect
// was materialized. Outcome is required when the desired state is proven.
type ReconcileResult struct {
	Outcome     domain.EffectOutcome
	SafeToRetry bool
	Evidence    []string
	Effects     []Effect
}

func (result ReconcileResult) validate(actionID string) error {
	if result.SafeToRetry && result.Outcome.Valid() {
		return fmt.Errorf("%w: reconciliation cannot be both safe-to-retry and terminal", ErrInvalidJournal)
	}
	if !result.SafeToRetry && result.Outcome != "" && !result.Outcome.Valid() {
		return fmt.Errorf("%w: reconciliation outcome is invalid", ErrInvalidJournal)
	}
	normalizeReportedEffects(result.Effects, actionID)
	return validateEffects(result.Effects, actionID)
}

func normalizeReportedEffects(effects []Effect, actionID string) {
	for index := range effects {
		if effects[index].ActionRunID == "" {
			effects[index].ActionRunID = actionID
		}
		if effects[index].Ordinal < 0 {
			effects[index].Ordinal = int64(index)
		}
		if effects[index].State == "" {
			effects[index].State = EffectPending
		}
		if effects[index].Evidence == nil {
			effects[index].Evidence = json.RawMessage(`{}`)
		}
		if effects[index].ObservedAt == "" {
			effects[index].ObservedAt = "execution-observation"
		}
	}
}

func validateEffects(effects []Effect, actionID string) error {
	seen := make(map[int64]struct{}, len(effects))
	seenIdentity := make(map[string]struct{}, len(effects))
	for index, effect := range effects {
		if effect.ActionRunID == "" {
			effect.ActionRunID = actionID
		}
		if effect.ActionRunID != actionID {
			return fmt.Errorf("%w: effect %d belongs to another action", ErrInvalidJournal, index)
		}
		if effect.State == "" {
			effect.State = EffectPending
		}
		if effect.Evidence == nil {
			effect.Evidence = json.RawMessage(`{}`)
		}
		if effect.ObservedAt == "" {
			effect.ObservedAt = "execution-observation"
		}
		if err := validateReportedEffect(effect, actionID); err != nil {
			return fmt.Errorf("effect %d: %w", index, err)
		}
		if _, ok := seen[effect.Ordinal]; ok {
			return fmt.Errorf("%w: duplicate effect ordinal %d", ErrInvalidJournal, effect.Ordinal)
		}
		seen[effect.Ordinal] = struct{}{}
		identity := effectIdentity(effect)
		if _, ok := seenIdentity[identity]; ok {
			return fmt.Errorf("%w: duplicate effect identity %s", ErrInvalidJournal, identity)
		}
		seenIdentity[identity] = struct{}{}
	}
	return nil
}

func effectIdentity(effect Effect) string {
	return strings.Join([]string{effect.TargetKind, effect.TargetID, effect.EffectKind}, "\x00")
}

// validateReportedEffect validates a handler-reported effect before the
// executor assigns its durable ID and attempt binding. Handlers must provide
// the exact target and effect kind, while IDs remain journal-owned so a
// replayed observation cannot invent a second row for the same ordinal.
func validateReportedEffect(effect Effect, actionID string) error {
	if strings.TrimSpace(effect.ActionRunID) == "" || effect.ActionRunID != actionID || effect.Ordinal < 0 || strings.TrimSpace(effect.TargetKind) == "" || strings.TrimSpace(effect.TargetID) == "" || strings.TrimSpace(effect.EffectKind) == "" {
		return fmt.Errorf("%w: effect identity is invalid", ErrInvalidJournal)
	}
	if !effect.State.valid() || !json.Valid(effect.Evidence) || strings.TrimSpace(effect.ObservedAt) == "" {
		return fmt.Errorf("%w: effect evidence is invalid", ErrInvalidJournal)
	}
	return nil
}

// Handler is the typed action contract. Implementations may call external
// systems in Dispatch, but Observe and Reconcile must remain read-only.
type Handler interface {
	Kind() domain.ActionKind
	// Reservations returns canonical local resource keys. Returning a key for
	// a parent path reserves its descendants as well by handler convention.
	Reservations(Action) []string
	Observe(context.Context, Action) (Observation, error)
	Dispatch(context.Context, Action, Attempt) (DispatchResult, error)
	Reconcile(context.Context, Action, Attempt) (ReconcileResult, error)
}

// Journal is the narrow durable boundary used by Executor. SQLJournal adapts
// the generated storage queries; tests can implement this interface with an
// in-memory fault-injecting journal.
type Journal interface {
	GetAction(context.Context, string) (Action, error)
	GetPlan(context.Context, string) (Plan, error)
	ListDue(context.Context, string, int) ([]Action, error)
	ListReconciling(context.Context, string, int) ([]Action, error)
	Claim(context.Context, string, int64, string, string, string) (Action, error)
	RecoverRunning(context.Context, string) ([]Action, error)
	RecoverExpired(context.Context, string) ([]Action, error)
	RecoverRunningAttempts(context.Context) ([]Attempt, error)
	FinalizeCancelled(context.Context, string) ([]Action, error)
	FinalizeDeadline(context.Context, string) ([]Action, error)
	RequestCancellation(context.Context, string, string) (Action, error)
	ListAttempts(context.Context, string) ([]Attempt, error)
	CreateAttempt(context.Context, Attempt) (Attempt, error)
	UpdateAttempt(context.Context, Attempt) (Attempt, error)
	ListEffects(context.Context, string) ([]Effect, error)
	CreateEffect(context.Context, Effect) (Effect, error)
	UpdateEffect(context.Context, Effect) (Effect, error)
	UpdateOutcome(context.Context, OutcomeUpdate) (Action, error)
}

// TransactionalJournal gives journal phase writes one short transaction. No
// handler call is made while the transaction is open.
type TransactionalJournal interface {
	InTx(context.Context, func(Journal) error) error
}

// ClaimFence is the immutable ownership evidence captured immediately after a
// successful claim. Post-dispatch journal writes must use this fence; a fresh
// action row must never grant a stale worker ownership of a recovered or
// re-claimed generation. Cancellation may advance Version once while keeping
// the same worker and lease, so implementations accept that durable marker as
// part of the original fence.
type ClaimFence struct {
	ActionID   string
	Version    int64
	WorkerID   string
	LeaseUntil string
	Now        string
}

// FencedTransactionalJournal provides the atomic ownership boundary used for
// all journal writes made by a claimed worker. The callback runs inside the
// transaction after the implementation has acquired its writer/ownership
// fence, and no handler call is made from the callback.
type FencedTransactionalJournal interface {
	InTxClaimed(context.Context, ClaimFence, func(Journal) error) error
}

// ReservationJournal persists canonical resource keys with an action while it
// has unresolved effects. Reserve is called inside InTxClaimed, allowing the
// implementation to inspect other active action rows and reject overlap
// atomically across workers and process restarts.
type ReservationJournal interface {
	Reserve(context.Context, string, []string) error
}

// OutcomeUpdate is the CAS-fenced action transition persisted after attempt
// and effect evidence. Empty optional fields clear their corresponding SQL
// columns; the current action is read before constructing this value.
type OutcomeUpdate struct {
	ID              string
	Version         int64
	State           domain.ActionState
	NextAttemptAt   string
	ClaimedBy       string
	LeaseUntil      string
	Outcome         json.RawMessage
	UnresolvedCount int64
	UpdatedAt       string
}

// RetryPolicy is persisted by converting the delay into NextAttemptAt. A
// zero MaxAttempts means no retry-count deadline; cancellation or DeadlineAt
// remains the explicit stop condition for an approved action.
type RetryPolicy struct {
	Initial     time.Duration
	Maximum     time.Duration
	Multiplier  float64
	MaxAttempts int
}

func (policy RetryPolicy) normalized() RetryPolicy {
	if policy.Initial <= 0 {
		policy.Initial = 5 * time.Second
	}
	if policy.Maximum <= 0 {
		policy.Maximum = 15 * time.Minute
	}
	if policy.Multiplier < 1 {
		policy.Multiplier = 2
	}
	return policy
}

func (policy RetryPolicy) delay(attempt int64) time.Duration {
	policy = policy.normalized()
	if attempt <= 1 {
		return policy.Initial
	}
	delay := float64(policy.Initial)
	for index := int64(1); index < attempt; index++ {
		delay *= policy.Multiplier
		if delay >= float64(policy.Maximum) {
			return policy.Maximum
		}
	}
	if delay > float64(policy.Maximum) {
		return policy.Maximum
	}
	return time.Duration(delay)
}

// Options controls one executor instance.
type Options struct {
	WorkerID       string
	LeaseDuration  time.Duration
	JournalTimeout time.Duration
	Retry          RetryPolicy
	MaxBatch       int
	Now            func() time.Time
	AttemptID      func(actionID string, number int64, phase AttemptPhase) string
	EffectID       func(actionID string, ordinal int64) string
}

func (options Options) normalized() Options {
	if strings.TrimSpace(options.WorkerID) == "" {
		options.WorkerID = "mastarr-executor"
	}
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = 30 * time.Second
	}
	if options.JournalTimeout <= 0 {
		options.JournalTimeout = 5 * time.Second
	}
	if options.MaxBatch <= 0 {
		options.MaxBatch = 16
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.AttemptID == nil {
		options.AttemptID = func(actionID string, number int64, phase AttemptPhase) string {
			return fmt.Sprintf("%s:attempt:%d:%s", actionID, number, phase)
		}
	}
	if options.EffectID == nil {
		options.EffectID = func(actionID string, ordinal int64) string {
			return fmt.Sprintf("%s:effect:%d", actionID, ordinal)
		}
	}
	options.Retry = options.Retry.normalized()
	return options
}

// Executor is a single process manager. Multiple Executor workers may share
// a journal: Claim's version/lease CAS is the authority, while reservations
// serialize overlapping local actions inside one process.
type Executor struct {
	journal       Journal
	options       Options
	handlersMu    sync.RWMutex
	handlers      map[domain.ActionKind]Handler
	reservations  map[string]string
	reservationMu sync.Mutex
	activeMu      sync.Mutex
	active        map[string]*activeHandler
}

type activeHandler struct {
	cancel context.CancelFunc
}

// New constructs an executor over a durable journal.
func New(journal Journal, options Options) (*Executor, error) {
	if journal == nil {
		return nil, errors.New("execution journal is required")
	}
	if _, ok := journal.(TransactionalJournal); !ok {
		return nil, fmt.Errorf("%w: transactional journal capability is required", ErrInvalidJournal)
	}
	if _, ok := journal.(FencedTransactionalJournal); !ok {
		return nil, fmt.Errorf("%w: fenced transactional journal capability is required", ErrInvalidJournal)
	}
	if _, ok := journal.(ReservationJournal); !ok {
		return nil, fmt.Errorf("%w: durable reservation journal capability is required", ErrInvalidJournal)
	}
	return &Executor{
		journal:      journal,
		options:      options.normalized(),
		handlers:     make(map[domain.ActionKind]Handler),
		reservations: make(map[string]string),
		active:       make(map[string]*activeHandler),
	}, nil
}

// RegisterHandler adds one typed action implementation. Duplicate kinds are
// rejected so configuration cannot silently replace a reviewed handler.
func (executor *Executor) RegisterHandler(handler Handler) error {
	if executor == nil || handler == nil {
		return errors.New("execution handler is required")
	}
	kind := handler.Kind()
	if !kind.Valid() {
		return fmt.Errorf("unsupported action handler kind %q", kind)
	}
	executor.handlersMu.Lock()
	defer executor.handlersMu.Unlock()
	if _, exists := executor.handlers[kind]; exists {
		return fmt.Errorf("handler for %q is already registered", kind)
	}
	executor.handlers[kind] = handler
	return nil
}

func (executor *Executor) handler(kind domain.ActionKind) (Handler, error) {
	executor.handlersMu.RLock()
	defer executor.handlersMu.RUnlock()
	handler, ok := executor.handlers[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoHandler, kind)
	}
	return handler, nil
}

func nowUTC(options Options) time.Time {
	return options.Now().UTC()
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func (executor *Executor) journalContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), executor.options.JournalTimeout)
}

func (executor *Executor) claimFence(action Action) ClaimFence {
	return ClaimFence{
		ActionID:   action.ID,
		Version:    action.Version,
		WorkerID:   executor.options.WorkerID,
		LeaseUntil: action.LeaseUntil,
		Now:        formatTime(nowUTC(executor.options)),
	}
}

func (executor *Executor) withClaimedTransaction(ctx context.Context, action Action, fn func(context.Context, Journal) error) error {
	if fn == nil {
		return errors.New("execution transaction callback is required")
	}
	transactional, ok := executor.journal.(FencedTransactionalJournal)
	if !ok {
		return fmt.Errorf("%w: fenced transactional journal capability is required", ErrInvalidJournal)
	}
	transactionCtx, cancel := executor.journalContext(ctx)
	defer cancel()
	return transactional.InTxClaimed(transactionCtx, executor.claimFence(action), func(journal Journal) error {
		return fn(transactionCtx, journal)
	})
}

func (executor *Executor) withTransaction(ctx context.Context, fn func(context.Context, Journal) error) error {
	if fn == nil {
		return errors.New("execution transaction callback is required")
	}
	transactional, ok := executor.journal.(TransactionalJournal)
	if !ok {
		return fmt.Errorf("%w: transactional journal capability is required", ErrInvalidJournal)
	}
	transactionCtx, cancel := executor.journalContext(ctx)
	defer cancel()
	return transactional.InTx(transactionCtx, func(journal Journal) error {
		return fn(transactionCtx, journal)
	})
}

func (executor *Executor) registerActiveHandler(actionID string, cancel context.CancelFunc) func() {
	active := &activeHandler{cancel: cancel}
	executor.activeMu.Lock()
	executor.active[actionID] = active
	executor.activeMu.Unlock()
	return func() {
		executor.activeMu.Lock()
		if executor.active[actionID] == active {
			delete(executor.active, actionID)
		}
		executor.activeMu.Unlock()
	}
}

func (executor *Executor) cancelActiveHandler(actionID string) {
	executor.activeMu.Lock()
	active := executor.active[actionID]
	executor.activeMu.Unlock()
	if active != nil && active.cancel != nil {
		active.cancel()
	}
}

func parseTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid persisted timestamp %q: %w", value, err)
	}
	return parsed, nil
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrNotFound)
}

// ErrNotFound is implemented by SQLJournal and is useful to fake journals.
var ErrNotFound = errors.New("execution resource was not found")

// RecoveryReport records startup recovery without claiming any external
// effect was undone. Running dispatches become read-only reconciliation.
type RecoveryReport struct {
	Actions          int
	Attempts         int
	Expired          int
	Cancelled        int
	DeadlineExceeded int
}

// Recover applies durable startup recovery. It never calls a handler.
func (executor *Executor) Recover(ctx context.Context) (RecoveryReport, error) {
	if executor == nil {
		return RecoveryReport{}, errors.New("execution executor is nil")
	}
	now := formatTime(nowUTC(executor.options))
	var report RecoveryReport
	actions, err := executor.journal.RecoverRunning(ctx, now)
	if err != nil {
		return report, err
	}
	report.Actions = len(actions)
	attempts, err := executor.journal.RecoverRunningAttempts(ctx)
	if err != nil {
		return report, err
	}
	report.Attempts = len(attempts)
	cancelled, err := executor.journal.FinalizeCancelled(ctx, now)
	if err != nil {
		return report, err
	}
	report.Cancelled = len(cancelled)
	deadline, err := executor.journal.FinalizeDeadline(ctx, now)
	if err != nil {
		return report, err
	}
	report.DeadlineExceeded = len(deadline)
	return report, nil
}

// BatchResult describes one polling pass. Item errors are returned in Results
// while the pass continues so one unavailable dependency cannot starve other
// due actions.
type BatchResult struct {
	RecoveredExpired int
	Cancelled        int
	DeadlineExceeded int
	Considered       int
	Claimed          int
	Results          []Result
}

// Result is the durable execution outcome observed by this call.
type Result struct {
	Action     Action
	Outcome    domain.EffectOutcome
	State      domain.ActionState
	Dispatched bool
	AttemptID  string
	Err        error
}

// RunOnce recovers expired leases, claims due work, and processes one bounded
// batch. It does not perform startup recovery of every running row; call
// Recover once during process startup for that boundary.
func (executor *Executor) RunOnce(ctx context.Context) (BatchResult, error) {
	if executor == nil {
		return BatchResult{}, errors.New("execution executor is nil")
	}
	now := formatTime(nowUTC(executor.options))
	var batch BatchResult
	expired, err := executor.journal.RecoverExpired(ctx, now)
	if err != nil {
		return batch, err
	}
	batch.RecoveredExpired = len(expired)
	cancelled, err := executor.journal.FinalizeCancelled(ctx, now)
	if err != nil {
		return batch, err
	}
	batch.Cancelled = len(cancelled)
	deadline, err := executor.journal.FinalizeDeadline(ctx, now)
	if err != nil {
		return batch, err
	}
	batch.DeadlineExceeded = len(deadline)

	due, err := executor.journal.ListDue(ctx, now, executor.options.MaxBatch)
	if err != nil {
		return batch, err
	}
	reconciling, err := executor.journal.ListReconciling(ctx, now, executor.options.MaxBatch)
	if err != nil {
		return batch, err
	}
	seen := make(map[string]struct{}, len(due)+len(reconciling))
	for _, action := range append(due, reconciling...) {
		if _, exists := seen[action.ID]; exists {
			continue
		}
		seen[action.ID] = struct{}{}
		batch.Considered++
		result := executor.claimAndProcess(ctx, action)
		if result.Action.ID != "" {
			batch.Results = append(batch.Results, result)
		}
		if result.Dispatched || result.State == domain.ActionRunning {
			batch.Claimed++
		}
	}
	return batch, nil
}

// RunAction claims and processes one action by ID. It is useful to expose a
// durable worker trigger while preserving the same claim and handler path as
// RunOnce.
func (executor *Executor) RunAction(ctx context.Context, id string) Result {
	if executor == nil {
		return Result{Err: errors.New("execution executor is nil")}
	}
	action, err := executor.journal.GetAction(ctx, id)
	if err != nil {
		return Result{Err: err}
	}
	return executor.claimAndProcess(ctx, action)
}

func (executor *Executor) claimAndProcess(ctx context.Context, action Action) Result {
	result := Result{Action: action, State: action.State}
	if err := action.validate(); err != nil {
		result.Err = err
		return result
	}
	now := nowUTC(executor.options)
	// A cancelled/deadline-exceeded reconciliation cannot use the ordinary
	// ClaimActionRun SQL predicate. It is read-only, so process it without a
	// mutation lease and let its final transition use the action version CAS.
	if action.State == domain.ActionReconciling && (action.CancellationRequestedAt != "" || expired(action.DeadlineAt, now)) {
		result = executor.processReconciliation(ctx, action, false)
		return result
	}
	plan, err := executor.journal.GetPlan(ctx, action.PlanID)
	if err != nil {
		result.Err = err
		return result
	}
	if err := plan.validate(); err != nil {
		result.Err = err
		return result
	}
	if plan.Kind != action.Kind {
		result.Err = fmt.Errorf("%w: action plan kind %q differs from action kind %q", ErrInvalidJournal, plan.Kind, action.Kind)
		return result
	}
	handler, err := executor.handler(action.Kind)
	if err != nil {
		result.Err = executor.hold(ctx, action, err)
		return result
	}

	claimed, err := executor.journal.Claim(ctx, action.ID, action.Version, executor.options.WorkerID, formatTime(now.Add(executor.options.LeaseDuration)), formatTime(now))
	if err != nil {
		if isNoRows(err) {
			result.Err = ErrLeaseLost
		} else {
			result.Err = err
		}
		return result
	}
	claimed.Kind = action.Kind
	claimed.PlanID = action.PlanID
	claimed.PlanRevision = action.PlanRevision
	claimed.PlanDigest = action.PlanDigest
	if err := claimed.validate(); err != nil {
		result.Action = claimed
		result.Err = err
		return result
	}
	result.Action = claimed
	reserved, err := executor.acquireAndPersistReservations(ctx, claimed, handler)
	if err != nil {
		result.Err = executor.waitOwned(ctx, claimed, err)
		result.State = domain.ActionWaitingDependency
		return result
	}

	var processed Result
	if action.State == domain.ActionReconciling {
		processed = executor.processReconciliation(ctx, claimed, true)
	} else {
		processed = executor.processObserved(ctx, claimed, handler)
	}
	// Reservations remain durable and local while an effect is unresolved. A
	// stale worker releases only its process-local map on lease loss; the
	// durable outcome metadata remains authoritative for the next worker and
	// avoids a local reservation leak after reconciliation completes elsewhere.
	if processed.State != domain.ActionReconciling {
		executor.releaseReservations(reserved, claimed.ID)
	}
	return processed
}

func (executor *Executor) acquireAndPersistReservations(ctx context.Context, action Action, handler Handler) ([]string, error) {
	keys, err := reservationKeys(action, handler)
	if err != nil {
		return nil, err
	}
	canonical, err := executor.acquireReservations(action.ID, keys)
	if err != nil {
		return nil, err
	}
	transactional, ok := executor.journal.(FencedTransactionalJournal)
	if !ok {
		executor.releaseReservations(canonical, action.ID)
		return nil, fmt.Errorf("%w: fenced transactional journal capability is required", ErrInvalidJournal)
	}
	transactionCtx, cancel := executor.journalContext(ctx)
	defer cancel()
	err = transactional.InTxClaimed(transactionCtx, executor.claimFence(action), func(journal Journal) error {
		reservations, ok := journal.(ReservationJournal)
		if !ok {
			return fmt.Errorf("%w: durable reservation journal capability is required", ErrInvalidJournal)
		}
		return reservations.Reserve(transactionCtx, action.ID, canonical)
	})
	if err != nil {
		executor.releaseReservations(canonical, action.ID)
		return nil, err
	}
	return canonical, nil
}

func (executor *Executor) acquireReservations(actionID string, canonical []string) ([]string, error) {
	executor.reservationMu.Lock()
	defer executor.reservationMu.Unlock()
	for _, key := range canonical {
		for existing, owner := range executor.reservations {
			if owner != actionID && reservationConflicts(existing, key) {
				return nil, fmt.Errorf("%w: %s", ErrReservationConflict, key)
			}
		}
	}
	for _, key := range canonical {
		executor.reservations[key] = actionID
	}
	return canonical, nil
}

func (executor *Executor) releaseReservations(keys []string, actionID string) {
	executor.reservationMu.Lock()
	defer executor.reservationMu.Unlock()
	for _, key := range keys {
		if executor.reservations[key] == actionID {
			delete(executor.reservations, key)
		}
	}
}

func reservationKeys(action Action, handler Handler) ([]string, error) {
	if handler == nil {
		return nil, errors.New("execution handler is required")
	}
	persisted, err := reservationKeysFromOutcome(action.Outcome)
	if err != nil {
		return nil, err
	}
	return canonicalReservationKeys(append(persisted, handler.Reservations(action)...)), nil
}

func canonicalReservationKeys(keys []string) []string {
	canonical := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		canonical = append(canonical, key)
	}
	sort.Strings(canonical)
	return canonical
}

func reservationConflicts(left, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	if left == right {
		return true
	}
	leftNamespace, leftPath := reservationNamespace(left)
	rightNamespace, rightPath := reservationNamespace(right)
	if leftNamespace != rightNamespace || leftNamespace == "" {
		return false
	}
	return pathReservationConflicts(leftPath, rightPath)
}

func reservationNamespace(value string) (string, string) {
	index := strings.IndexByte(value, ':')
	if index < 0 {
		return "", value
	}
	return value[:index], strings.TrimPrefix(value[index+1:], "/")
}

func pathReservationConflicts(left, right string) bool {
	left = strings.TrimSuffix(strings.TrimSpace(left), "/")
	right = strings.TrimSuffix(strings.TrimSpace(right), "/")
	if left == "" || right == "" {
		return true
	}
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

const reservationOutcomeField = "reservations"

func reservationKeysFromOutcome(outcome json.RawMessage) ([]string, error) {
	if len(outcome) == 0 || string(outcome) == "null" {
		return nil, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(outcome, &object); err != nil {
		return nil, fmt.Errorf("%w: invalid outcome reservation metadata: %v", ErrInvalidJournal, err)
	}
	raw, ok := object[reservationOutcomeField]
	if !ok || string(raw) == "null" {
		return nil, nil
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, fmt.Errorf("%w: invalid outcome reservations: %v", ErrInvalidJournal, err)
	}
	return canonicalReservationKeys(keys), nil
}

func outcomeWithReservations(outcome json.RawMessage, keys []string) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if len(outcome) == 0 || string(outcome) == "null" {
		object = make(map[string]json.RawMessage)
	} else if err := json.Unmarshal(outcome, &object); err != nil {
		return nil, fmt.Errorf("%w: invalid outcome: %v", ErrInvalidJournal, err)
	} else if object == nil {
		object = make(map[string]json.RawMessage)
	}
	keys = canonicalReservationKeys(keys)
	if len(keys) == 0 {
		delete(object, reservationOutcomeField)
	} else {
		encoded, err := json.Marshal(keys)
		if err != nil {
			return nil, fmt.Errorf("%w: encode reservations: %v", ErrInvalidJournal, err)
		}
		object[reservationOutcomeField] = encoded
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("%w: encode outcome: %v", ErrInvalidJournal, err)
	}
	return encoded, nil
}

func outcomePreservingReservations(existing, next json.RawMessage, state domain.ActionState) (json.RawMessage, error) {
	keys, err := reservationKeysFromOutcome(existing)
	if err != nil {
		return nil, err
	}
	if state != domain.ActionRunning && state != domain.ActionReconciling && state != domain.ActionNeedsReview {
		keys = nil
	}
	return outcomeWithReservations(next, keys)
}

func claimFenceMatches(action Action, fence ClaimFence) bool {
	if action.ID != fence.ActionID || action.State != domain.ActionRunning || action.ClaimedBy != fence.WorkerID || action.LeaseUntil != fence.LeaseUntil {
		return false
	}
	if action.Version != fence.Version && (action.Version != fence.Version+1 || action.CancellationRequestedAt == "") {
		return false
	}
	now, err := parseTime(fence.Now)
	if err != nil {
		return false
	}
	lease, err := parseTime(action.LeaseUntil)
	return err == nil && !lease.IsZero() && lease.After(now)
}

func expired(value string, now time.Time) bool {
	parsed, err := parseTime(value)
	return err == nil && !parsed.IsZero() && !parsed.After(now)
}

func (executor *Executor) processObserved(ctx context.Context, action Action, handler Handler) Result {
	result := Result{Action: action, State: action.State}
	attempt, err := executor.newAttemptOwned(ctx, action, AttemptObserve, CertaintyNotDispatched)
	if err != nil {
		result.Err = err
		return result
	}
	result.AttemptID = attempt.ID
	observation, err := handler.Observe(ctx, action)
	if err != nil {
		kind := failureKind(err)
		finished := attempt
		finished.State = domain.AttemptFailed
		finished.FinishedAt = formatTime(nowUTC(executor.options))
		finished.ErrorCode = string(kind)
		finished.ErrorDetail = safeDetail(err)
		if failureWasDispatched(err) || kind == FailureUncertain {
			finished.OutcomeCertainty = CertaintyUncertain
		}
		if _, updateErr := executor.updateAttemptOwned(ctx, action, finished); updateErr != nil {
			result.Err = updateErr
			return result
		}
		if kind == FailureDependency {
			result.Err = executor.waitOwned(ctx, action, err)
			result.Action = action
			result.State = domain.ActionWaitingDependency
			return result
		}
		result.Err = executor.holdOwned(ctx, action, err)
		result.Action = action
		result.State = domain.ActionNeedsReview
		return result
	}
	if err := observation.validate(action.ID); err != nil {
		finished := finishAttempt(attempt, domain.AttemptFailed, CertaintyKnown, err, executor.options)
		if _, updateErr := executor.updateAttemptOwned(ctx, action, finished); updateErr != nil {
			result.Err = updateErr
			return result
		}
		result.Err = executor.holdOwned(ctx, action, err)
		result.State = domain.ActionNeedsReview
		return result
	}
	if observation.State == ObserveUnknown {
		finished := finishAttempt(attempt, domain.AttemptSucceeded, CertaintyKnown, nil, executor.options)
		finished.Evidence = evidenceJSON(observation.Evidence)
		if _, err := executor.updateAttemptOwned(ctx, action, finished); err != nil {
			result.Err = err
			return result
		}
		result.Err = executor.holdOwned(ctx, action, errors.New("desired state could not be proven"))
		result.State = domain.ActionNeedsReview
		return result
	}
	if observation.State == ObserveSatisfied {
		finished := finishAttempt(attempt, domain.AttemptSucceeded, CertaintyKnown, nil, executor.options)
		finished.Evidence = evidenceJSON(observation.Evidence)
		if _, err := executor.updateAttemptOwned(ctx, action, finished); err != nil {
			result.Err = err
			return result
		}
		if err := executor.recordEffectsOwned(ctx, action, finished, observation.Effects, EffectAlreadySatisfied); err != nil {
			result.Err = err
			return result
		}
		updated, err := executor.transitionOwned(ctx, action, finished, domain.ActionSucceeded, domain.OutcomeAlreadySatisfied, 0, "already_satisfied")
		result.Action = updated
		result.State = updated.State
		result.Outcome = domain.OutcomeAlreadySatisfied
		result.Err = err
		return result
	}

	// Observe succeeded and requested a mutation. Close that journal entry and
	// create a separate dispatch entry plus pending effects before the handler
	// can call an upstream API or change a file.
	finishedObserve := finishAttempt(attempt, domain.AttemptSucceeded, CertaintyKnown, nil, executor.options)
	finishedObserve.Evidence = evidenceJSON(observation.Evidence)
	dispatchAttempt, err := executor.prepareDispatch(ctx, action, finishedObserve, observation.Effects)
	if err != nil {
		result.Err = err
		return result
	}
	result.AttemptID = dispatchAttempt.ID

	dispatchCtx, cancelDispatch := executor.handlerContext(ctx, action)
	unregister := executor.registerActiveHandler(action.ID, cancelDispatch)
	defer unregister()
	defer cancelDispatch()
	if err := executor.beforeDispatch(dispatchCtx, action); err != nil {
		kind := failureKind(err)
		if errors.Is(err, ErrLeaseLost) {
			result.Err = err
			return result
		}
		finished := finishAttempt(dispatchAttempt, domain.AttemptCancelled, CertaintyNotDispatched, err, executor.options)
		if _, updateErr := executor.updateAttemptOwned(ctx, action, finished); updateErr != nil {
			result.Err = updateErr
			return result
		}
		updated, updateErr := executor.transitionStopOwned(ctx, action, finished, string(kind))
		result.Action = updated
		result.State = updated.State
		result.Err = updateErr
		if result.Err == nil {
			result.Err = err
		}
		return result
	}

	dispatchResult, dispatchErr := handler.Dispatch(dispatchCtx, action, dispatchAttempt)
	result.Dispatched = true
	if dispatchErr != nil {
		return executor.finishDispatchError(ctx, result, dispatchAttempt, dispatchErr)
	}
	if err := dispatchResult.validate(action.ID); err != nil {
		return executor.finishDispatchError(ctx, result, dispatchAttempt, NewDispatchedFailure(FailureUncertain, err))
	}
	// A returned success is only an input to the final read-back. The handler
	// must prove the desired state through its read-only Observe implementation.
	readBack, readErr := handler.Observe(dispatchCtx, action)
	if readErr != nil {
		return executor.finishDispatchError(ctx, result, dispatchAttempt, NewDispatchedFailure(FailureUncertain, readErr))
	}
	if err := readBack.validate(action.ID); err != nil || readBack.State != ObserveSatisfied || effectSetMismatch(observation.Effects, readBack.Effects, action.ID) {
		if err == nil {
			if effectSetMismatch(observation.Effects, readBack.Effects, action.ID) {
				err = errors.New("dispatch read-back effects did not match the approved target set")
			} else {
				err = errors.New("dispatch read-back did not prove desired state")
			}
		}
		return executor.finishDispatchError(ctx, result, dispatchAttempt, NewDispatchedFailure(FailureUncertain, err))
	}
	if len(dispatchResult.Effects) > 0 && effectSetMismatch(observation.Effects, dispatchResult.Effects, action.ID) {
		return executor.finishDispatchError(ctx, result, dispatchAttempt, NewDispatchedFailure(FailureUncertain, errors.New("dispatch effects did not match the approved target set")))
	}
	finished := finishAttempt(dispatchAttempt, domain.AttemptSucceeded, CertaintyKnown, nil, executor.options)
	finished.ExternalID = dispatchResult.ExternalID
	finished.Evidence = evidenceJSON(append(dispatchResult.Evidence, readBack.Evidence...))
	if _, err := executor.updateAttemptOwned(ctx, action, finished); err != nil {
		result.Err = err
		return result
	}
	if err := executor.recordEffectsOwned(ctx, action, finished, readBack.Effects, effectForOutcome(dispatchResult.Outcome)); err != nil {
		result.Err = err
		return result
	}
	updated, _, outcome, err := executor.transitionDispatchOwned(ctx, action, finished, dispatchResult.Outcome)
	result.Action = updated
	result.State = updated.State
	result.Outcome = outcome
	result.Err = err
	return result
}

func (executor *Executor) processReconciliation(ctx context.Context, action Action, claimed bool) Result {
	result := Result{Action: action, State: action.State}
	handler, err := executor.handler(action.Kind)
	if err != nil {
		if claimed {
			result.Err = executor.holdOwned(ctx, action, err)
		} else {
			result.Err = executor.hold(ctx, action, err)
		}
		result.State = domain.ActionNeedsReview
		return result
	}
	var attempt Attempt
	if claimed {
		attempt, err = executor.reconciliationAttemptOwned(ctx, action)
	} else {
		attempt, err = executor.reconciliationAttempt(ctx, action)
	}
	if err != nil {
		result.Err = err
		return result
	}
	result.AttemptID = attempt.ID
	reconcileCtx, cancelReconcile := executor.handlerContext(ctx, action)
	if claimed {
		unregister := executor.registerActiveHandler(action.ID, cancelReconcile)
		defer unregister()
	}
	defer cancelReconcile()
	reconciled, reconcileErr := handler.Reconcile(reconcileCtx, action, attempt)
	if reconcileErr != nil {
		return executor.finishReconciliationError(ctx, result, attempt, reconcileErr, claimed)
	}
	if err := reconciled.validate(action.ID); err != nil {
		return executor.finishReconciliationError(ctx, result, attempt, NewFailure(FailureInvalid, err), claimed)
	}
	if reconciled.Outcome.Valid() {
		finished := finishAttempt(attempt, domain.AttemptSucceeded, CertaintyKnown, nil, executor.options)
		finished.Evidence = evidenceJSON(reconciled.Evidence)
		if _, err := executor.updateAttemptFor(ctx, action, finished, claimed); err != nil {
			result.Err = err
			return result
		}
		if err := executor.recordEffectsFor(ctx, action, finished, reconciled.Effects, effectForOutcome(reconciled.Outcome), claimed); err != nil {
			return executor.finishReconciliationError(ctx, result, attempt, NewFailure(FailureInvalid, err), claimed)
		}
		var updated Action
		var err error
		if claimed {
			updated, _, _, err = executor.transitionDispatchOwned(ctx, action, finished, reconciled.Outcome)
		} else {
			latest, latestErr := executor.currentAction(ctx, action)
			if latestErr != nil {
				result.Err = latestErr
				return result
			}
			state, _ := executor.cancelledOrDeadline(latest)
			if state == "" {
				state = domain.ActionSucceeded
			}
			updated, err = executor.transition(ctx, latest, finished, state, reconciled.Outcome, 0, "reconciled")
		}
		result.Action = updated
		result.State = updated.State
		result.Outcome = reconciled.Outcome
		result.Err = err
		return result
	}
	if reconciled.SafeToRetry {
		finished := finishAttempt(attempt, domain.AttemptSucceeded, CertaintyKnown, nil, executor.options)
		finished.Evidence = evidenceJSON(reconciled.Evidence)
		if _, err := executor.updateAttemptFor(ctx, action, finished, claimed); err != nil {
			result.Err = err
			return result
		}
		if err := executor.recordEffectsFor(ctx, action, finished, reconciled.Effects, EffectPending, claimed); err != nil {
			return executor.finishReconciliationError(ctx, result, attempt, NewFailure(FailureInvalid, err), claimed)
		}
		var updated Action
		var err error
		if claimed {
			updated, err = executor.requeueOwned(ctx, action, finished, "reconciliation_proved_no_effect")
		} else {
			latest, latestErr := executor.currentAction(ctx, action)
			if latestErr != nil {
				result.Err = latestErr
				return result
			}
			if state, _ := executor.cancelledOrDeadline(latest); state != "" {
				updated, err = executor.transition(ctx, latest, finished, state, "", 0, "reconciliation_cancelled")
			} else {
				updated, err = executor.requeue(ctx, latest, finished, "reconciliation_proved_no_effect")
			}
		}
		result.Action = updated
		result.State = updated.State
		result.Err = err
		return result
	}

	finished := attempt
	finished.State = domain.AttemptReconciling
	finished.OutcomeCertainty = CertaintyUncertain
	finished.Evidence = evidenceJSON(reconciled.Evidence)
	finished.FinishedAt = ""
	if _, err := executor.updateAttemptFor(ctx, action, finished, claimed); err != nil {
		result.Err = err
		return result
	}
	if err := executor.recordEffectsFor(ctx, action, finished, reconciled.Effects, EffectUnknown, claimed); err != nil {
		return executor.finishReconciliationError(ctx, result, attempt, NewFailure(FailureInvalid, err), claimed)
	}
	var updated Action
	if claimed {
		updated, err = executor.transitionOwnedAt(ctx, action, finished, domain.ActionReconciling, "", 1, "reconciliation_unresolved", executor.retryAt(attempt.AttemptNumber))
	} else {
		latest, latestErr := executor.currentAction(ctx, action)
		if latestErr != nil {
			result.Err = latestErr
			return result
		}
		updated, err = executor.transitionAt(ctx, latest, finished, domain.ActionReconciling, "", 1, "reconciliation_unresolved", executor.retryAt(attempt.AttemptNumber))
	}
	result.Action = updated
	result.State = updated.State
	result.Err = err
	return result
}

func (executor *Executor) finishDispatchError(ctx context.Context, result Result, attempt Attempt, dispatchErr error) Result {
	kind := failureKind(dispatchErr)
	uncertain := failureWasDispatched(dispatchErr) || kind == FailureUncertain
	if uncertain {
		finished := attempt
		finished.State = domain.AttemptReconciling
		finished.OutcomeCertainty = CertaintyUncertain
		finished.ErrorCode = string(FailureUncertain)
		finished.ErrorDetail = safeDetail(dispatchErr)
		finished.Evidence = evidenceJSON([]string{"dispatch_result_uncertain"})
		if _, err := executor.updateAttemptOwned(ctx, result.Action, finished); err != nil {
			result.Err = err
			return result
		}
		if err := executor.recordUnknownEffectsOwned(ctx, result.Action, finished, EffectUnknown); err != nil {
			result.Err = err
			return result
		}
		updated, _, err := executor.transitionUncertainOwned(ctx, result.Action, finished, "dispatch_uncertain", executor.retryAt(attempt.AttemptNumber))
		result.Action = updated
		result.State = updated.State
		result.Err = err
		return result
	}
	finished := finishAttempt(attempt, domain.AttemptFailed, CertaintyNotDispatched, dispatchErr, executor.options)
	if _, err := executor.updateAttemptOwned(ctx, result.Action, finished); err != nil {
		result.Err = err
		return result
	}
	if kind == FailureDependency {
		result.Err = executor.waitOwned(ctx, result.Action, dispatchErr)
		result.State = domain.ActionWaitingDependency
		return result
	}
	updated, err := executor.transitionOwned(ctx, result.Action, finished, domain.ActionFailed, "", 0, string(kind))
	result.Action = updated
	result.State = updated.State
	result.Err = err
	return result
}

func (executor *Executor) finishReconciliationError(ctx context.Context, result Result, attempt Attempt, reconcileErr error, claimed bool) Result {
	kind := failureKind(reconcileErr)
	if kind == FailureDependency || kind == FailureUncertain {
		finished := attempt
		finished.State = domain.AttemptReconciling
		finished.OutcomeCertainty = CertaintyUncertain
		finished.ErrorCode = string(kind)
		finished.ErrorDetail = safeDetail(reconcileErr)
		if _, err := executor.updateAttemptFor(ctx, result.Action, finished, claimed); err != nil {
			result.Err = err
			return result
		}
		var updated Action
		var err error
		if claimed {
			updated, _, err = executor.transitionUncertainOwned(ctx, result.Action, finished, string(kind), executor.retryAt(attempt.AttemptNumber))
		} else {
			latest, latestErr := executor.currentAction(ctx, result.Action)
			if latestErr != nil {
				result.Err = latestErr
				return result
			}
			updated, err = executor.transitionAt(ctx, latest, finished, domain.ActionReconciling, "", 1, string(kind), executor.retryAt(attempt.AttemptNumber))
		}
		result.Action = updated
		result.State = updated.State
		result.Err = err
		return result
	}
	finished := finishAttempt(attempt, domain.AttemptFailed, CertaintyKnown, reconcileErr, executor.options)
	if _, err := executor.updateAttemptFor(ctx, result.Action, finished, claimed); err != nil {
		result.Err = err
		return result
	}
	var updated Action
	var err error
	if claimed {
		updated, err = executor.transitionOwned(ctx, result.Action, finished, domain.ActionNeedsReview, "", 0, string(kind))
	} else {
		latest, latestErr := executor.currentAction(ctx, result.Action)
		if latestErr != nil {
			result.Err = latestErr
			return result
		}
		state := domain.ActionNeedsReview
		if stopState, _ := executor.cancelledOrDeadline(latest); stopState != "" {
			state = stopState
		}
		updated, err = executor.transition(ctx, latest, finished, state, "", 0, string(kind))
	}
	result.Action = updated
	result.State = updated.State
	result.Err = err
	return result
}

func (executor *Executor) nextAttempt(ctx context.Context, action Action, phase AttemptPhase, certainty OutcomeCertainty) (Attempt, error) {
	return executor.nextAttemptInJournal(ctx, executor.journal, action, phase, certainty)
}

func (executor *Executor) nextAttemptInJournal(ctx context.Context, journal Journal, action Action, phase AttemptPhase, certainty OutcomeCertainty) (Attempt, error) {
	attempts, err := journal.ListAttempts(ctx, action.ID)
	if err != nil {
		return Attempt{}, err
	}
	var number int64
	for _, existing := range attempts {
		if existing.AttemptNumber > number {
			number = existing.AttemptNumber
		}
	}
	number++
	attempt := Attempt{
		ID:               executor.options.AttemptID(action.ID, number, phase),
		ActionRunID:      action.ID,
		AttemptNumber:    number,
		Phase:            phase,
		State:            domain.AttemptRunning,
		StartedAt:        formatTime(nowUTC(executor.options)),
		OutcomeCertainty: certainty,
		Evidence:         json.RawMessage(`{}`),
	}
	if err := attempt.validate(); err != nil {
		return Attempt{}, err
	}
	return attempt, nil
}

func (executor *Executor) newAttempt(ctx context.Context, action Action, phase AttemptPhase, certainty OutcomeCertainty) (Attempt, error) {
	attempt, err := executor.nextAttempt(ctx, action, phase, certainty)
	if err != nil {
		return Attempt{}, err
	}
	return executor.journal.CreateAttempt(ctx, attempt)
}

func (executor *Executor) newAttemptOwned(ctx context.Context, action Action, phase AttemptPhase, certainty OutcomeCertainty) (Attempt, error) {
	var created Attempt
	err := executor.withClaimedTransaction(ctx, action, func(transactionCtx context.Context, journal Journal) error {
		attempt, err := executor.nextAttemptInJournal(transactionCtx, journal, action, phase, certainty)
		if err != nil {
			return err
		}
		created, err = journal.CreateAttempt(transactionCtx, attempt)
		return err
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Attempt{}, ErrLeaseLost
		}
		return Attempt{}, err
	}
	return created, nil
}

func (executor *Executor) reconciliationAttempt(ctx context.Context, action Action) (Attempt, error) {
	attempts, err := executor.journal.ListAttempts(ctx, action.ID)
	if err != nil {
		return Attempt{}, err
	}
	if len(attempts) > 0 {
		latest := attempts[len(attempts)-1]
		for _, candidate := range attempts {
			if candidate.AttemptNumber > latest.AttemptNumber {
				latest = candidate
			}
		}
		if latest.Phase == AttemptReconcile && latest.State == domain.AttemptReconciling {
			return latest, nil
		}
	}
	return executor.newAttempt(ctx, action, AttemptReconcile, CertaintyUncertain)
}

func (executor *Executor) reconciliationAttemptOwned(ctx context.Context, action Action) (Attempt, error) {
	attempts, err := executor.journal.ListAttempts(ctx, action.ID)
	if err != nil {
		return Attempt{}, err
	}
	for _, candidate := range attempts {
		if candidate.Phase == AttemptReconcile && candidate.State == domain.AttemptReconciling {
			return candidate, nil
		}
	}
	return executor.newAttemptOwned(ctx, action, AttemptReconcile, CertaintyUncertain)
}

func (executor *Executor) updateAttemptOwned(ctx context.Context, action Action, attempt Attempt) (Attempt, error) {
	var updated Attempt
	err := executor.withClaimedTransaction(ctx, action, func(transactionCtx context.Context, journal Journal) error {
		var err error
		updated, err = journal.UpdateAttempt(transactionCtx, attempt)
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, ErrLeaseLost
	}
	return updated, err
}

func (executor *Executor) handlerContext(ctx context.Context, action Action) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	child, cancel := context.WithCancel(ctx)
	deadline, err := parseTime(action.DeadlineAt)
	if err == nil && !deadline.IsZero() {
		withDeadline, deadlineCancel := context.WithDeadline(child, deadline)
		return withDeadline, func() {
			deadlineCancel()
			cancel()
		}
	}
	return child, cancel
}

func (executor *Executor) prepareDispatch(ctx context.Context, action Action, observed Attempt, effects []Effect) (Attempt, error) {
	// Create the dispatch intent and all pending effect rows in one short
	// transaction. No handler call occurs until this callback commits.
	var dispatch Attempt
	err := executor.withClaimedTransaction(ctx, action, func(transactionCtx context.Context, journal Journal) error {
		var err error
		dispatch, err = executor.nextAttemptInJournal(transactionCtx, journal, action, AttemptDispatch, CertaintyNotDispatched)
		if err != nil {
			return err
		}
		if _, err := journal.UpdateAttempt(transactionCtx, observed); err != nil {
			return err
		}
		if _, err := journal.CreateAttempt(transactionCtx, dispatch); err != nil {
			return err
		}
		existing, err := journal.ListEffects(transactionCtx, action.ID)
		if err != nil {
			return err
		}
		byOrdinal := make(map[int64]Effect, len(existing))
		for _, candidate := range existing {
			byOrdinal[candidate.Ordinal] = candidate
		}
		for ordinal, effect := range effects {
			if effect.Ordinal < 0 {
				effect.Ordinal = int64(ordinal)
			}
			effect.ID = executor.options.EffectID(action.ID, effect.Ordinal)
			effect.ActionRunID = action.ID
			effect.AttemptID = dispatch.ID
			// Observation is read-only evidence. Every target is pending until a
			// dispatch returns and its fresh read-back proves the effect.
			effect.State = EffectPending
			if effect.Evidence == nil {
				effect.Evidence = json.RawMessage(`{}`)
			}
			if effect.ObservedAt == "" {
				effect.ObservedAt = formatTime(nowUTC(executor.options))
			}
			if previous, ok := byOrdinal[effect.Ordinal]; ok {
				if previous.TargetKind != effect.TargetKind || previous.TargetID != effect.TargetID || previous.EffectKind != effect.EffectKind {
					return fmt.Errorf("%w: effect ordinal %d changed target across attempts", ErrInvalidJournal, effect.Ordinal)
				}
				effect.ID = previous.ID
				if _, err := journal.UpdateEffect(transactionCtx, effect); err != nil {
					return err
				}
				continue
			}
			if _, err := journal.CreateEffect(transactionCtx, effect); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Attempt{}, ErrLeaseLost
		}
		return Attempt{}, err
	}
	return dispatch, nil
}

func (executor *Executor) beforeDispatch(ctx context.Context, action Action) error {
	if err := ctx.Err(); err != nil {
		return NewFailure(FailureCancelled, err)
	}
	current, err := executor.journal.GetAction(ctx, action.ID)
	if err != nil {
		return err
	}
	now := nowUTC(executor.options)
	// Cancellation and deadline are durable stop markers. Check them before
	// the version fence so a marker committed by another caller is surfaced as
	// a terminal pre-dispatch decision rather than an opaque lease conflict.
	if current.CancellationRequestedAt != "" {
		return NewFailure(FailureCancelled, errors.New("cancellation requested before dispatch"))
	}
	if expired(current.DeadlineAt, now) {
		return NewFailure(FailureConflict, errors.New("action deadline exceeded before dispatch"))
	}
	if current.Version != action.Version || current.ClaimedBy != executor.options.WorkerID || current.LeaseUntil == "" {
		return ErrLeaseLost
	}
	lease, err := parseTime(current.LeaseUntil)
	if err != nil || !lease.After(now) {
		return ErrLeaseLost
	}
	return nil
}

func finishAttempt(attempt Attempt, state domain.AttemptState, certainty OutcomeCertainty, err error, options Options) Attempt {
	attempt.State = state
	attempt.OutcomeCertainty = certainty
	if state.Terminal() {
		attempt.FinishedAt = formatTime(nowUTC(options))
	}
	if err != nil {
		attempt.ErrorCode = string(failureKind(err))
		attempt.ErrorDetail = safeDetail(err)
	}
	if attempt.Evidence == nil {
		attempt.Evidence = json.RawMessage(`{}`)
	}
	return attempt
}

func safeDetail(err error) string {
	if err == nil {
		return ""
	}
	detail := strings.TrimSpace(err.Error())
	if len(detail) > 512 {
		detail = detail[:512]
	}
	return detail
}

func evidenceJSON(values []string) json.RawMessage {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > 256 {
			value = value[:256]
		}
		filtered = append(filtered, value)
	}
	payload, err := json.Marshal(map[string]any{"evidence": filtered})
	if err != nil {
		return json.RawMessage(`{"evidence":[]}`)
	}
	return payload
}

// validateEffectSet requires a read-back to describe exactly the approved
// targets. Ordinal and the full target/effect identity are checked together;
// an empty or partial report is therefore never promoted to applied.
func validateEffectSet(expected, reported []Effect, actionID string) error {
	normalizeReportedEffects(expected, actionID)
	normalizeReportedEffects(reported, actionID)
	if err := validateEffects(expected, actionID); err != nil {
		return fmt.Errorf("approved effects: %w", err)
	}
	if err := validateEffects(reported, actionID); err != nil {
		return fmt.Errorf("reported effects: %w", err)
	}
	if len(expected) != len(reported) {
		return fmt.Errorf("%w: effect set has %d reported targets, want %d", ErrInvalidJournal, len(reported), len(expected))
	}
	byIdentity := make(map[string]Effect, len(expected))
	for _, effect := range expected {
		byIdentity[effectIdentity(effect)] = effect
	}
	for _, effect := range reported {
		approved, ok := byIdentity[effectIdentity(effect)]
		if !ok {
			return fmt.Errorf("%w: unapproved effect target %s", ErrInvalidJournal, effectIdentity(effect))
		}
		if approved.Ordinal != effect.Ordinal {
			return fmt.Errorf("%w: effect target %s changed ordinal", ErrInvalidJournal, effectIdentity(effect))
		}
	}
	return nil
}

func effectSetMismatch(expected, reported []Effect, actionID string) bool {
	return validateEffectSet(expected, reported, actionID) != nil
}

func effectForOutcome(outcome domain.EffectOutcome) EffectState {
	if outcome == domain.OutcomeAlreadySatisfied {
		return EffectAlreadySatisfied
	}
	if outcome == domain.OutcomeApplied {
		return EffectApplied
	}
	return EffectUnknown
}

func (executor *Executor) recordEffects(ctx context.Context, action Action, attempt Attempt, reported []Effect, defaultState EffectState) error {
	return executor.withTransaction(ctx, func(transactionCtx context.Context, journal Journal) error {
		return executor.recordEffectsInJournal(transactionCtx, journal, action, attempt, reported, defaultState, true)
	})
}

func (executor *Executor) recordEffectsOwned(ctx context.Context, action Action, attempt Attempt, reported []Effect, defaultState EffectState) error {
	return executor.withClaimedTransaction(ctx, action, func(transactionCtx context.Context, journal Journal) error {
		return executor.recordEffectsInJournal(transactionCtx, journal, action, attempt, reported, defaultState, true)
	})
}

func (executor *Executor) recordUnknownEffectsOwned(ctx context.Context, action Action, attempt Attempt, defaultState EffectState) error {
	return executor.withClaimedTransaction(ctx, action, func(transactionCtx context.Context, journal Journal) error {
		return executor.recordEffectsInJournal(transactionCtx, journal, action, attempt, nil, defaultState, false)
	})
}

func (executor *Executor) recordEffectsFor(ctx context.Context, action Action, attempt Attempt, reported []Effect, defaultState EffectState, claimed bool) error {
	if claimed {
		return executor.recordEffectsOwned(ctx, action, attempt, reported, defaultState)
	}
	return executor.recordEffects(ctx, action, attempt, reported, defaultState)
}

func (executor *Executor) recordEffectsInJournal(ctx context.Context, journal Journal, action Action, attempt Attempt, reported []Effect, defaultState EffectState, requireComplete bool) error {
	existing, err := journal.ListEffects(ctx, action.ID)
	if err != nil {
		return err
	}
	if len(existing) > 0 && (requireComplete || len(reported) > 0) {
		if err := validateEffectSet(existing, reported, action.ID); err != nil {
			return err
		}
	}
	byOrdinal := make(map[int64]Effect, len(existing))
	for _, candidate := range existing {
		byOrdinal[candidate.Ordinal] = candidate
	}
	for index, effect := range reported {
		if effect.Ordinal < 0 {
			effect.Ordinal = int64(index)
		}
		if existingEffect, ok := byOrdinal[effect.Ordinal]; ok {
			effect.ID = existingEffect.ID
			effect.ActionRunID = action.ID
			effect.AttemptID = attempt.ID
			if effect.State == "" || effect.State == EffectPending {
				effect.State = defaultState
			}
			if effect.Evidence == nil {
				effect.Evidence = existingEffect.Evidence
			}
			if effect.ObservedAt == "" {
				effect.ObservedAt = formatTime(nowUTC(executor.options))
			}
			if _, err := journal.UpdateEffect(ctx, effect); err != nil {
				return err
			}
			continue
		}
		effect.ID = executor.options.EffectID(action.ID, effect.Ordinal)
		effect.ActionRunID = action.ID
		effect.AttemptID = attempt.ID
		if effect.State == "" || effect.State == EffectPending {
			effect.State = defaultState
		}
		if effect.Evidence == nil {
			effect.Evidence = json.RawMessage(`{}`)
		}
		if effect.ObservedAt == "" {
			effect.ObservedAt = formatTime(nowUTC(executor.options))
		}
		if _, err := journal.CreateEffect(ctx, effect); err != nil {
			return err
		}
	}
	return nil
}

func (executor *Executor) updateAttemptFor(ctx context.Context, action Action, attempt Attempt, claimed bool) (Attempt, error) {
	if claimed {
		return executor.updateAttemptOwned(ctx, action, attempt)
	}
	return executor.journal.UpdateAttempt(ctx, attempt)
}

func (executor *Executor) cancelledOrDeadline(action Action) (domain.ActionState, domain.EffectOutcome) {
	if action.CancellationRequestedAt != "" {
		return domain.ActionCancelled, ""
	}
	if expired(action.DeadlineAt, nowUTC(executor.options)) {
		return domain.ActionDeadlineExceeded, ""
	}
	return "", ""
}

func (executor *Executor) retryAt(attemptNumber int64) string {
	if attemptNumber <= 0 {
		attemptNumber = 1
	}
	return formatTime(nowUTC(executor.options).Add(executor.options.Retry.delay(attemptNumber)))
}

func (executor *Executor) currentAction(ctx context.Context, action Action) (Action, error) {
	latest, err := executor.journal.GetAction(ctx, action.ID)
	if err != nil {
		return Action{}, err
	}
	// Journal implementations derive Kind from the persisted plan. Preserve
	// the caller's immutable plan identity for journals that intentionally keep
	// their action rows transport-independent.
	latest.Kind = action.Kind
	latest.PlanID = action.PlanID
	latest.PlanRevision = action.PlanRevision
	latest.PlanDigest = action.PlanDigest
	return latest, nil
}

func outcomeJSON(outcome domain.EffectOutcome, reason string, unresolved int64) json.RawMessage {
	payload := map[string]any{"reason": reason}
	if outcome.Valid() {
		payload["outcome"] = outcome
	}
	if unresolved > 0 {
		payload["unresolvedEffects"] = unresolved
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{"reason":"journal_encoding_failed"}`)
	}
	return encoded
}

func (executor *Executor) transition(ctx context.Context, action Action, attempt Attempt, state domain.ActionState, outcome domain.EffectOutcome, unresolved int64, reason string) (Action, error) {
	return executor.transitionAt(ctx, action, attempt, state, outcome, unresolved, reason, "")
}

func (executor *Executor) transitionAt(ctx context.Context, action Action, attempt Attempt, state domain.ActionState, outcome domain.EffectOutcome, unresolved int64, reason, nextAttemptAt string) (Action, error) {
	var updated Action
	err := executor.withTransaction(ctx, func(transactionCtx context.Context, journal Journal) error {
		current, err := journal.GetAction(transactionCtx, action.ID)
		if err != nil {
			return err
		}
		if state == "" {
			state = domain.ActionNeedsReview
		}
		if err := domain.TransitionAction(current.State, state); err != nil && current.State != state {
			return err
		}
		updated, err = journal.UpdateOutcome(transactionCtx, OutcomeUpdate{
			ID:              current.ID,
			Version:         current.Version,
			State:           state,
			NextAttemptAt:   nextAttemptAt,
			Outcome:         outcomeJSON(outcome, reason, unresolved),
			UnresolvedCount: unresolved,
			UpdatedAt:       formatTime(nowUTC(executor.options)),
		})
		return err
	})
	if err != nil {
		if isNoRows(err) {
			return action, ErrLeaseLost
		}
		return action, err
	}
	updated = preserveActionPlan(updated, action)
	return updated, nil
}

func preserveActionPlan(updated, source Action) Action {
	updated.Kind = source.Kind
	updated.PlanID = source.PlanID
	updated.PlanRevision = source.PlanRevision
	updated.PlanDigest = source.PlanDigest
	return updated
}

func (executor *Executor) transitionOwned(ctx context.Context, action Action, attempt Attempt, state domain.ActionState, outcome domain.EffectOutcome, unresolved int64, reason string) (Action, error) {
	return executor.transitionOwnedAt(ctx, action, attempt, state, outcome, unresolved, reason, "")
}

func (executor *Executor) transitionOwnedAt(ctx context.Context, action Action, attempt Attempt, state domain.ActionState, outcome domain.EffectOutcome, unresolved int64, reason, nextAttemptAt string) (Action, error) {
	var updated Action
	err := executor.withClaimedTransaction(ctx, action, func(transactionCtx context.Context, journal Journal) error {
		current, err := journal.GetAction(transactionCtx, action.ID)
		if err != nil {
			return err
		}
		if state == "" {
			state = domain.ActionNeedsReview
		}
		if err := domain.TransitionAction(current.State, state); err != nil && current.State != state {
			return err
		}
		updated, err = journal.UpdateOutcome(transactionCtx, OutcomeUpdate{
			ID:              current.ID,
			Version:         current.Version,
			State:           state,
			NextAttemptAt:   nextAttemptAt,
			Outcome:         outcomeJSON(outcome, reason, unresolved),
			UnresolvedCount: unresolved,
			UpdatedAt:       formatTime(nowUTC(executor.options)),
		})
		return err
	})
	if err != nil {
		if isNoRows(err) {
			return action, ErrLeaseLost
		}
		return action, err
	}
	return preserveActionPlan(updated, action), nil
}

func (executor *Executor) transitionStopOwned(ctx context.Context, action Action, attempt Attempt, reason string) (Action, error) {
	var updated Action
	err := executor.withClaimedTransaction(ctx, action, func(transactionCtx context.Context, journal Journal) error {
		current, err := journal.GetAction(transactionCtx, action.ID)
		if err != nil {
			return err
		}
		state, _ := executor.cancelledOrDeadline(current)
		if state == "" {
			return ErrLeaseLost
		}
		updated, err = journal.UpdateOutcome(transactionCtx, OutcomeUpdate{
			ID: current.ID, Version: current.Version, State: state,
			Outcome: outcomeJSON("", reason, 0), UpdatedAt: formatTime(nowUTC(executor.options)),
		})
		return err
	})
	if err != nil {
		if isNoRows(err) {
			return action, ErrLeaseLost
		}
		return action, err
	}
	return preserveActionPlan(updated, action), nil
}

func (executor *Executor) transitionDispatchOwned(ctx context.Context, action Action, attempt Attempt, applied domain.EffectOutcome) (Action, domain.ActionState, domain.EffectOutcome, error) {
	var updated Action
	var state domain.ActionState
	outcome := applied
	err := executor.withClaimedTransaction(ctx, action, func(transactionCtx context.Context, journal Journal) error {
		current, err := journal.GetAction(transactionCtx, action.ID)
		if err != nil {
			return err
		}
		state, outcome = executor.cancelledOrDeadline(current)
		if state == "" {
			state = domain.ActionSucceeded
			outcome = applied
		}
		updated, err = journal.UpdateOutcome(transactionCtx, OutcomeUpdate{
			ID: current.ID, Version: current.Version, State: state,
			Outcome: outcomeJSON(outcome, "dispatch_applied", 0), UpdatedAt: formatTime(nowUTC(executor.options)),
		})
		return err
	})
	if err != nil {
		if isNoRows(err) {
			err = ErrLeaseLost
		}
		return action, state, outcome, err
	}
	return preserveActionPlan(updated, action), state, outcome, nil
}

func (executor *Executor) transitionUncertainOwned(ctx context.Context, action Action, attempt Attempt, reason, nextAttemptAt string) (Action, domain.ActionState, error) {
	var updated Action
	var state domain.ActionState
	err := executor.withClaimedTransaction(ctx, action, func(transactionCtx context.Context, journal Journal) error {
		current, err := journal.GetAction(transactionCtx, action.ID)
		if err != nil {
			return err
		}
		state, _ = executor.cancelledOrDeadline(current)
		if state == "" {
			state = domain.ActionReconciling
		} else {
			nextAttemptAt = ""
		}
		updated, err = journal.UpdateOutcome(transactionCtx, OutcomeUpdate{ID: current.ID, Version: current.Version, State: state, NextAttemptAt: nextAttemptAt, Outcome: outcomeJSON("", reason, 1), UnresolvedCount: 1, UpdatedAt: formatTime(nowUTC(executor.options))})
		return err
	})
	if err != nil {
		if isNoRows(err) {
			return action, state, ErrLeaseLost
		}
		return action, state, err
	}
	return preserveActionPlan(updated, action), state, nil
}

func (executor *Executor) requeue(ctx context.Context, action Action, attempt Attempt, reason string) (Action, error) {
	var updated Action
	err := executor.withTransaction(ctx, func(transactionCtx context.Context, journal Journal) error {
		current, err := journal.GetAction(transactionCtx, action.ID)
		if err != nil {
			return err
		}
		if current.State == domain.ActionRunning {
			current, err = journal.UpdateOutcome(transactionCtx, OutcomeUpdate{ID: current.ID, Version: current.Version, State: domain.ActionReconciling, Outcome: outcomeJSON("", reason, 0), UnresolvedCount: 0, UpdatedAt: formatTime(nowUTC(executor.options))})
			if err != nil {
				return err
			}
		}
		now := nowUTC(executor.options)
		updated, err = journal.UpdateOutcome(transactionCtx, OutcomeUpdate{ID: current.ID, Version: current.Version, State: domain.ActionQueued, NextAttemptAt: formatTime(now), Outcome: outcomeJSON("", reason, 0), UpdatedAt: formatTime(now)})
		return err
	})
	if err != nil {
		if isNoRows(err) {
			return action, ErrLeaseLost
		}
		return action, err
	}
	return preserveActionPlan(updated, action), nil
}

func (executor *Executor) wait(ctx context.Context, action Action, cause error) error {
	return executor.waitFor(ctx, action, cause, false)
}

func (executor *Executor) hold(ctx context.Context, action Action, cause error) error {
	return executor.holdFor(ctx, action, cause, false)
}

func (executor *Executor) requeueOwned(ctx context.Context, action Action, attempt Attempt, reason string) (Action, error) {
	var updated Action
	err := executor.withClaimedTransaction(ctx, action, func(transactionCtx context.Context, journal Journal) error {
		current, err := journal.GetAction(transactionCtx, action.ID)
		if err != nil {
			return err
		}
		if current.State == domain.ActionRunning {
			current, err = journal.UpdateOutcome(transactionCtx, OutcomeUpdate{ID: current.ID, Version: current.Version, State: domain.ActionReconciling, Outcome: outcomeJSON("", reason, 0), UnresolvedCount: 0, UpdatedAt: formatTime(nowUTC(executor.options))})
			if err != nil {
				return err
			}
		}
		now := nowUTC(executor.options)
		updated, err = journal.UpdateOutcome(transactionCtx, OutcomeUpdate{ID: current.ID, Version: current.Version, State: domain.ActionQueued, NextAttemptAt: formatTime(now), Outcome: outcomeJSON("", reason, 0), UpdatedAt: formatTime(now)})
		return err
	})
	if err != nil {
		if isNoRows(err) {
			return action, ErrLeaseLost
		}
		return action, err
	}
	return preserveActionPlan(updated, action), nil
}

func (executor *Executor) waitOwned(ctx context.Context, action Action, cause error) error {
	return executor.waitFor(ctx, action, cause, true)
}

func (executor *Executor) waitFor(ctx context.Context, action Action, cause error, claimed bool) error {
	var err error
	var current Action
	if claimed {
		err = executor.withClaimedTransaction(ctx, action, func(transactionCtx context.Context, journal Journal) error {
			var innerErr error
			current, innerErr = journal.GetAction(transactionCtx, action.ID)
			if innerErr != nil {
				return innerErr
			}
			return executor.applyWait(transactionCtx, journal, current, cause)
		})
	} else {
		err = executor.withTransaction(ctx, func(transactionCtx context.Context, journal Journal) error {
			var innerErr error
			current, innerErr = journal.GetAction(transactionCtx, action.ID)
			if innerErr != nil {
				return innerErr
			}
			return executor.applyWait(transactionCtx, journal, current, cause)
		})
	}
	if isNoRows(err) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	return cause
}

func (executor *Executor) applyWait(ctx context.Context, journal Journal, action Action, cause error) error {
	attempts, err := journal.ListAttempts(ctx, action.ID)
	if err != nil {
		return err
	}
	attemptNumber := int64(len(attempts))
	if executor.options.Retry.MaxAttempts > 0 && int(attemptNumber) >= executor.options.Retry.MaxAttempts {
		return executor.applyHold(ctx, journal, action, fmt.Errorf("retry limit reached: %w", cause))
	}
	next := nowUTC(executor.options).Add(executor.options.Retry.delay(attemptNumber))
	_, err = journal.UpdateOutcome(ctx, OutcomeUpdate{ID: action.ID, Version: action.Version, State: domain.ActionWaitingDependency, NextAttemptAt: formatTime(next), Outcome: outcomeJSON("", "dependency_wait", 0), UpdatedAt: formatTime(nowUTC(executor.options))})
	return err
}

func (executor *Executor) holdOwned(ctx context.Context, action Action, cause error) error {
	return executor.holdFor(ctx, action, cause, true)
}

func (executor *Executor) holdFor(ctx context.Context, action Action, cause error, claimed bool) error {
	apply := func(transactionCtx context.Context, journal Journal) error {
		current, err := journal.GetAction(transactionCtx, action.ID)
		if err != nil {
			return err
		}
		return executor.applyHold(transactionCtx, journal, current, cause)
	}
	var err error
	if claimed {
		err = executor.withClaimedTransaction(ctx, action, apply)
	} else {
		err = executor.withTransaction(ctx, apply)
	}
	if isNoRows(err) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	return cause
}

func (executor *Executor) applyHold(ctx context.Context, journal Journal, action Action, cause error) error {
	_, err := journal.UpdateOutcome(ctx, OutcomeUpdate{ID: action.ID, Version: action.Version, State: domain.ActionNeedsReview, Outcome: outcomeJSON("", safeDetail(cause), 0), UpdatedAt: formatTime(nowUTC(executor.options))})
	return err
}

// Cancel persists one cancellation marker. Repeating it is idempotent at the
// SQL boundary; an in-flight dispatch is never claimed to be rolled back.
func (executor *Executor) Cancel(ctx context.Context, id string) (Action, error) {
	if executor == nil {
		return Action{}, errors.New("execution executor is nil")
	}
	requestedAt := formatTime(nowUTC(executor.options))
	var action Action
	err := executor.withTransaction(ctx, func(transactionCtx context.Context, journal Journal) error {
		var err error
		action, err = journal.RequestCancellation(transactionCtx, id, requestedAt)
		return err
	})
	if err != nil {
		return Action{}, err
	}
	if err := action.validate(); err != nil {
		return Action{}, err
	}
	executor.cancelActiveHandler(id)
	return action, nil
}

// SQLJournal adapts the generated D-01 queries without exposing generated
// rows to handlers or the public Mastarr API.
type SQLJournal struct {
	store  SQLStore
	query  sqlQueryer
	db     *sql.DB
	runner sqlRunner
}

// SQLStore is implemented by storage.Store. It is kept as a local interface
// so execution tests can provide a transaction-capable fixture store.
type SQLStore interface {
	DB() *sql.DB
	Queries() *sqlc.Queries
	WithTx(context.Context, func(*sqlc.Queries) error) error
}

type sqlRunner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// NewSQLJournal binds the action executor to an opened storage.Store.
func NewSQLJournal(store SQLStore) (*SQLJournal, error) {
	if store == nil || store.Queries() == nil || store.DB() == nil {
		return nil, errors.New("execution SQL store is required")
	}
	return &SQLJournal{store: store, query: store.Queries(), db: store.DB(), runner: store.DB()}, nil
}

type sqlQueryer interface {
	GetActionRun(context.Context, string) (*sqlc.ActionRun, error)
	GetActionPlan(context.Context, string) (*sqlc.ActionPlan, error)
	ListDueActionRuns(context.Context, *sqlc.ListDueActionRunsParams) ([]*sqlc.ActionRun, error)
	ClaimActionRun(context.Context, *sqlc.ClaimActionRunParams) (*sqlc.ActionRun, error)
	RecoverRunningActionRuns(context.Context, sql.NullString) ([]*sqlc.ActionRun, error)
	RecoverExpiredActionRuns(context.Context, sql.NullString) ([]*sqlc.ActionRun, error)
	RecoverRunningActionAttempts(context.Context) ([]*sqlc.ActionAttempt, error)
	FinalizeCancelledActionRuns(context.Context, string) ([]*sqlc.ActionRun, error)
	FinalizeDeadlineActionRuns(context.Context, string) ([]*sqlc.ActionRun, error)
	RequestActionCancellation(context.Context, *sqlc.RequestActionCancellationParams) (*sqlc.ActionRun, error)
	ListActionAttempts(context.Context, string) ([]*sqlc.ActionAttempt, error)
	CreateActionAttempt(context.Context, *sqlc.CreateActionAttemptParams) (*sqlc.ActionAttempt, error)
	UpdateActionAttempt(context.Context, *sqlc.UpdateActionAttemptParams) (*sqlc.ActionAttempt, error)
	ListActionEffects(context.Context, string) ([]*sqlc.ActionEffect, error)
	CreateActionEffect(context.Context, *sqlc.CreateActionEffectParams) (*sqlc.ActionEffect, error)
	UpdateActionEffect(context.Context, *sqlc.UpdateActionEffectParams) (*sqlc.ActionEffect, error)
	UpdateActionRunOutcome(context.Context, *sqlc.UpdateActionRunOutcomeParams) (*sqlc.ActionRun, error)
}

func (journal *SQLJournal) clone(query sqlQueryer) *SQLJournal {
	return &SQLJournal{store: journal.store, query: query, db: journal.db}
}

func (journal *SQLJournal) cloneWithRunner(query sqlQueryer, runner sqlRunner) *SQLJournal {
	return &SQLJournal{store: journal.store, query: query, db: journal.db, runner: runner}
}

// InTx implements TransactionalJournal. The handler is never called from the
// callback by Executor; this method only groups local journal records.
func (journal *SQLJournal) InTx(ctx context.Context, fn func(Journal) error) error {
	if journal == nil || journal.store == nil {
		return errors.New("execution SQL transaction is unavailable")
	}
	if fn == nil {
		return errors.New("execution transaction callback is required")
	}
	return journal.store.WithTx(ctx, func(query *sqlc.Queries) error {
		return fn(journal.clone(query))
	})
}

// InTxClaimed acquires SQLite's writer lock and verifies the immutable claim
// before exposing a transactional journal. Every effect/attempt/transition
// written by the callback is therefore part of the same ownership generation;
// lease recovery or re-claim cannot interleave between the check and commit.
func (journal *SQLJournal) InTxClaimed(ctx context.Context, fence ClaimFence, fn func(Journal) error) error {
	if journal == nil || journal.db == nil {
		return errors.New("execution SQL claimed transaction is unavailable")
	}
	if fn == nil {
		return errors.New("execution claimed transaction callback is required")
	}
	tx, err := journal.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	rollback := func() { _ = tx.Rollback() }
	defer rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE action_runs
		SET updated_at = updated_at
		WHERE id = ?1
		  AND state = 'running'
		  AND claimed_by = ?2
		  AND lease_until = ?3
		  AND lease_until > ?4
		  AND (version = ?5 OR (version = ?5 + 1 AND cancellation_requested_at IS NOT NULL))`,
		fence.ActionID, fence.WorkerID, fence.LeaseUntil, fence.Now, fence.Version)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrLeaseLost
	}
	if err := fn(journal.cloneWithRunner(sqlc.New(tx), tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (journal *SQLJournal) Reserve(ctx context.Context, actionID string, keys []string) error {
	if journal == nil || journal.runner == nil {
		return fmt.Errorf("%w: durable reservation transaction is unavailable", ErrInvalidJournal)
	}
	keys = canonicalReservationKeys(keys)
	rows, err := journal.runner.QueryContext(ctx, `SELECT id, outcome_json FROM action_runs WHERE state IN ('running', 'reconciling', 'needs_review')`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ownOutcome json.RawMessage
	var ownFound bool
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		candidate, err := reservationKeysFromOutcome(json.RawMessage(raw))
		if err != nil {
			return err
		}
		if id == actionID {
			ownOutcome = json.RawMessage(raw)
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
	if err := rows.Err(); err != nil {
		return err
	}
	if !ownFound {
		return ErrNotFound
	}
	current, err := reservationKeysFromOutcome(ownOutcome)
	if err != nil {
		return err
	}
	merged, err := outcomeWithReservations(ownOutcome, append(current, keys...))
	if err != nil {
		return err
	}
	result, err := journal.runner.ExecContext(ctx, `UPDATE action_runs SET outcome_json = ?1 WHERE id = ?2`, string(merged), actionID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (journal *SQLJournal) GetAction(ctx context.Context, id string) (Action, error) {
	run, err := journal.query.GetActionRun(ctx, id)
	if err != nil {
		if isNoRows(err) {
			return Action{}, ErrNotFound
		}
		return Action{}, err
	}
	plan, err := journal.query.GetActionPlan(ctx, run.PlanID)
	if err != nil {
		return Action{}, err
	}
	action, err := actionFromSQL(run, plan)
	return action, err
}

func (journal *SQLJournal) GetPlan(ctx context.Context, id string) (Plan, error) {
	plan, err := journal.query.GetActionPlan(ctx, id)
	if err != nil {
		if isNoRows(err) {
			return Plan{}, ErrNotFound
		}
		return Plan{}, err
	}
	return planFromSQL(plan)
}

func (journal *SQLJournal) ListDue(ctx context.Context, now string, limit int) ([]Action, error) {
	rows, err := journal.query.ListDueActionRuns(ctx, &sqlc.ListDueActionRunsParams{Now: sql.NullString{String: now, Valid: true}, Limit: int64(limit)})
	if err != nil {
		return nil, err
	}
	return journal.actionsWithPlans(ctx, rows)
}

func (journal *SQLJournal) ListReconciling(ctx context.Context, now string, limit int) ([]Action, error) {
	if journal.db == nil {
		return nil, errors.New("execution reconciliation listing requires a database")
	}
	rows, err := journal.db.QueryContext(ctx, `
		SELECT id, plan_id, plan_revision, plan_digest, state, desired_state_json,
		       next_attempt_at, deadline_at, cancellation_requested_at, claimed_by,
		       lease_until, version, outcome_json, unresolved_count, created_at, updated_at
		FROM action_runs
		WHERE state = 'reconciling'
		  AND (
		      cancellation_requested_at IS NOT NULL
		      OR (deadline_at IS NOT NULL AND deadline_at <= ?1)
		      OR next_attempt_at IS NULL
		      OR next_attempt_at <= ?1
		  )
		  AND (claimed_by IS NULL OR lease_until IS NULL OR lease_until <= ?1)
		  AND NOT EXISTS (
		      SELECT 1 FROM janitor_records AS approval
		      WHERE approval.approval_action_run_id = action_runs.id
		        AND approval.operation = 'purge'
		        AND approval.approval_plan_id IS NOT NULL
		        AND approval.state IN ('queued', 'running', 'waiting_dependency', 'reconciling', 'succeeded', 'failed', 'held', 'cancelled')
		  )
		ORDER BY COALESCE(next_attempt_at, created_at), created_at, id
		LIMIT ?2`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []*sqlc.ActionRun
	for rows.Next() {
		run := new(sqlc.ActionRun)
		if err := rows.Scan(&run.ID, &run.PlanID, &run.PlanRevision, &run.PlanDigest, &run.State, &run.DesiredStateJson, &run.NextAttemptAt, &run.DeadlineAt, &run.CancellationRequestedAt, &run.ClaimedBy, &run.LeaseUntil, &run.Version, &run.OutcomeJson, &run.UnresolvedCount, &run.CreatedAt, &run.UpdatedAt); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var actions []Action
	for _, run := range runs {
		plan, err := journal.query.GetActionPlan(ctx, run.PlanID)
		if err != nil {
			return nil, err
		}
		action, err := actionFromSQL(run, plan)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func (journal *SQLJournal) actionsWithPlans(ctx context.Context, rows []*sqlc.ActionRun) ([]Action, error) {
	actions := make([]Action, 0, len(rows))
	for _, row := range rows {
		plan, err := journal.query.GetActionPlan(ctx, row.PlanID)
		if err != nil {
			return nil, err
		}
		action, err := actionFromSQL(row, plan)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func (journal *SQLJournal) Claim(ctx context.Context, id string, version int64, workerID, leaseUntil, now string) (Action, error) {
	run, err := journal.query.ClaimActionRun(ctx, &sqlc.ClaimActionRunParams{WorkerID: nullString(workerID), LeaseUntil: nullString(leaseUntil), Now: now, ID: id, Version: version})
	if err != nil {
		return Action{}, err
	}
	plan, err := journal.query.GetActionPlan(ctx, run.PlanID)
	if err != nil {
		return Action{}, err
	}
	return actionFromSQL(run, plan)
}

func (journal *SQLJournal) RecoverRunning(ctx context.Context, now string) ([]Action, error) {
	rows, err := journal.query.RecoverRunningActionRuns(ctx, nullString(now))
	if err != nil {
		return nil, err
	}
	return journal.actionsWithPlans(ctx, rows)
}

func (journal *SQLJournal) RecoverExpired(ctx context.Context, now string) ([]Action, error) {
	rows, err := journal.query.RecoverExpiredActionRuns(ctx, nullString(now))
	if err != nil {
		return nil, err
	}
	return journal.actionsWithPlans(ctx, rows)
}

func (journal *SQLJournal) RecoverRunningAttempts(ctx context.Context) ([]Attempt, error) {
	rows, err := journal.query.RecoverRunningActionAttempts(ctx)
	if err != nil {
		return nil, err
	}
	attempts := make([]Attempt, 0, len(rows))
	for _, row := range rows {
		attempt, err := attemptFromSQL(row)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	return attempts, nil
}

func (journal *SQLJournal) FinalizeCancelled(ctx context.Context, now string) ([]Action, error) {
	rows, err := journal.query.FinalizeCancelledActionRuns(ctx, now)
	if err != nil {
		return nil, err
	}
	return journal.actionsWithPlans(ctx, rows)
}

func (journal *SQLJournal) FinalizeDeadline(ctx context.Context, now string) ([]Action, error) {
	rows, err := journal.query.FinalizeDeadlineActionRuns(ctx, now)
	if err != nil {
		return nil, err
	}
	return journal.actionsWithPlans(ctx, rows)
}

func (journal *SQLJournal) RequestCancellation(ctx context.Context, id, requestedAt string) (Action, error) {
	run, err := journal.query.RequestActionCancellation(ctx, &sqlc.RequestActionCancellationParams{RequestedAt: nullString(requestedAt), UpdatedAt: requestedAt, ID: id})
	if err != nil {
		return Action{}, err
	}
	plan, err := journal.query.GetActionPlan(ctx, run.PlanID)
	if err != nil {
		return Action{}, err
	}
	return actionFromSQL(run, plan)
}

func (journal *SQLJournal) ListAttempts(ctx context.Context, id string) ([]Attempt, error) {
	rows, err := journal.query.ListActionAttempts(ctx, id)
	if err != nil {
		return nil, err
	}
	attempts := make([]Attempt, 0, len(rows))
	for _, row := range rows {
		attempt, err := attemptFromSQL(row)
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	return attempts, nil
}

func (journal *SQLJournal) CreateAttempt(ctx context.Context, attempt Attempt) (Attempt, error) {
	if err := attempt.validate(); err != nil {
		return Attempt{}, err
	}
	row, err := journal.query.CreateActionAttempt(ctx, &sqlc.CreateActionAttemptParams{ID: attempt.ID, ActionRunID: attempt.ActionRunID, AttemptNumber: attempt.AttemptNumber, Phase: string(attempt.Phase), State: string(attempt.State), StartedAt: attempt.StartedAt, FinishedAt: nullString(attempt.FinishedAt), ErrorCode: nullString(attempt.ErrorCode), ErrorDetail: nullString(attempt.ErrorDetail), OutcomeCertainty: string(attempt.OutcomeCertainty), EvidenceJson: string(attempt.Evidence), ExternalID: nullString(attempt.ExternalID)})
	if err != nil {
		return Attempt{}, err
	}
	return attemptFromSQL(row)
}

func (journal *SQLJournal) UpdateAttempt(ctx context.Context, attempt Attempt) (Attempt, error) {
	if err := attempt.validate(); err != nil {
		return Attempt{}, err
	}
	row, err := journal.query.UpdateActionAttempt(ctx, &sqlc.UpdateActionAttemptParams{ID: attempt.ID, State: string(attempt.State), FinishedAt: nullString(attempt.FinishedAt), ErrorCode: nullString(attempt.ErrorCode), ErrorDetail: nullString(attempt.ErrorDetail), OutcomeCertainty: string(attempt.OutcomeCertainty), EvidenceJson: string(attempt.Evidence), ExternalID: nullString(attempt.ExternalID)})
	if err != nil {
		return Attempt{}, err
	}
	return attemptFromSQL(row)
}

func (journal *SQLJournal) ListEffects(ctx context.Context, id string) ([]Effect, error) {
	rows, err := journal.query.ListActionEffects(ctx, id)
	if err != nil {
		return nil, err
	}
	effects := make([]Effect, 0, len(rows))
	for _, row := range rows {
		effect, err := effectFromSQL(row)
		if err != nil {
			return nil, err
		}
		effects = append(effects, effect)
	}
	return effects, nil
}

func (journal *SQLJournal) CreateEffect(ctx context.Context, effect Effect) (Effect, error) {
	if err := effect.validate(); err != nil {
		return Effect{}, err
	}
	row, err := journal.query.CreateActionEffect(ctx, &sqlc.CreateActionEffectParams{ID: effect.ID, ActionRunID: effect.ActionRunID, AttemptID: nullString(effect.AttemptID), Ordinal: effect.Ordinal, TargetKind: effect.TargetKind, TargetID: effect.TargetID, EffectKind: effect.EffectKind, State: string(effect.State), EvidenceJson: string(effect.Evidence), ObservedAt: effect.ObservedAt})
	if err != nil {
		return Effect{}, err
	}
	return effectFromSQL(row)
}

func (journal *SQLJournal) UpdateEffect(ctx context.Context, effect Effect) (Effect, error) {
	if err := effect.validate(); err != nil {
		return Effect{}, err
	}
	row, err := journal.query.UpdateActionEffect(ctx, &sqlc.UpdateActionEffectParams{ID: effect.ID, State: string(effect.State), EvidenceJson: string(effect.Evidence), ObservedAt: effect.ObservedAt, AttemptID: nullString(effect.AttemptID)})
	if err != nil {
		return Effect{}, err
	}
	return effectFromSQL(row)
}

func (journal *SQLJournal) UpdateOutcome(ctx context.Context, update OutcomeUpdate) (Action, error) {
	if update.ID == "" || update.Version <= 0 || !update.State.Valid() || !json.Valid(update.Outcome) || update.UnresolvedCount < 0 {
		return Action{}, fmt.Errorf("%w: invalid outcome update", ErrInvalidJournal)
	}
	current, err := journal.query.GetActionRun(ctx, update.ID)
	if err != nil {
		if isNoRows(err) {
			return Action{}, ErrNotFound
		}
		return Action{}, err
	}
	preserved, err := outcomePreservingReservations(json.RawMessage(current.OutcomeJson), update.Outcome, update.State)
	if err != nil {
		return Action{}, err
	}
	run, err := journal.query.UpdateActionRunOutcome(ctx, &sqlc.UpdateActionRunOutcomeParams{ID: update.ID, Version: update.Version, State: string(update.State), NextAttemptAt: nullString(update.NextAttemptAt), ClaimedBy: nullString(update.ClaimedBy), LeaseUntil: nullString(update.LeaseUntil), OutcomeJson: string(preserved), UnresolvedCount: update.UnresolvedCount, UpdatedAt: update.UpdatedAt})
	if err != nil {
		return Action{}, err
	}
	plan, err := journal.query.GetActionPlan(ctx, run.PlanID)
	if err != nil {
		return Action{}, err
	}
	return actionFromSQL(run, plan)
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func actionFromSQL(run *sqlc.ActionRun, plan *sqlc.ActionPlan) (Action, error) {
	if run == nil || plan == nil {
		return Action{}, fmt.Errorf("%w: action or plan row is nil", ErrInvalidJournal)
	}
	actionKind, err := parseActionKind(plan.Kind)
	if err != nil {
		return Action{}, err
	}
	action := Action{ID: run.ID, PlanID: run.PlanID, PlanRevision: run.PlanRevision, PlanDigest: run.PlanDigest, Kind: actionKind, State: domain.ActionState(run.State), DesiredState: json.RawMessage(run.DesiredStateJson), NextAttemptAt: run.NextAttemptAt.String, DeadlineAt: run.DeadlineAt.String, CancellationRequestedAt: run.CancellationRequestedAt.String, ClaimedBy: run.ClaimedBy.String, LeaseUntil: run.LeaseUntil.String, Version: run.Version, Outcome: json.RawMessage(run.OutcomeJson), UnresolvedCount: run.UnresolvedCount, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}
	if err := action.validate(); err != nil {
		return Action{}, err
	}
	return action, nil
}

func parseActionKind(value string) (domain.ActionKind, error) {
	kind := domain.ActionKind(value)
	if !kind.Valid() {
		return "", fmt.Errorf("%w: unsupported action kind %q", ErrInvalidJournal, value)
	}
	return kind, nil
}

func planFromSQL(plan *sqlc.ActionPlan) (Plan, error) {
	if plan == nil {
		return Plan{}, fmt.Errorf("%w: action plan row is nil", ErrInvalidJournal)
	}
	kind, err := parseActionKind(plan.Kind)
	if err != nil {
		return Plan{}, err
	}
	value := Plan{ID: plan.ID, Kind: kind, State: plan.State, CurrentRevision: plan.CurrentRevision, CurrentDigest: plan.CurrentDigest}
	if err := value.validate(); err != nil {
		return Plan{}, err
	}
	return value, nil
}

func attemptFromSQL(row *sqlc.ActionAttempt) (Attempt, error) {
	if row == nil {
		return Attempt{}, fmt.Errorf("%w: attempt row is nil", ErrInvalidJournal)
	}
	attempt := Attempt{ID: row.ID, ActionRunID: row.ActionRunID, AttemptNumber: row.AttemptNumber, Phase: AttemptPhase(row.Phase), State: domain.AttemptState(row.State), StartedAt: row.StartedAt, FinishedAt: row.FinishedAt.String, ErrorCode: row.ErrorCode.String, ErrorDetail: row.ErrorDetail.String, OutcomeCertainty: OutcomeCertainty(row.OutcomeCertainty), ExternalID: row.ExternalID.String, Evidence: json.RawMessage(row.EvidenceJson)}
	if err := attempt.validate(); err != nil {
		return Attempt{}, err
	}
	return attempt, nil
}

func effectFromSQL(row *sqlc.ActionEffect) (Effect, error) {
	if row == nil {
		return Effect{}, fmt.Errorf("%w: effect row is nil", ErrInvalidJournal)
	}
	effect := Effect{ID: row.ID, ActionRunID: row.ActionRunID, AttemptID: row.AttemptID.String, Ordinal: row.Ordinal, TargetKind: row.TargetKind, TargetID: row.TargetID, EffectKind: row.EffectKind, State: EffectState(row.State), Evidence: json.RawMessage(row.EvidenceJson), ObservedAt: row.ObservedAt}
	if err := effect.validate(); err != nil {
		return Effect{}, err
	}
	return effect, nil
}

// Compile-time checks keep generated storage changes visible to this adapter.
var _ sqlQueryer = (*sqlc.Queries)(nil)
