# W-02 independent review, round one

## Decision

`changes_requested`.

Supplied tests, race detector, vet, root matrix, and full repository guardrails
pass. The executor also records intent before calling handlers, observes before
dispatch, routes uncertain responses through reconciliation, persists retry
times, and contains no generic DAG, outbox, or arbitrary command surface.

Four mutation-safety defects block approval. A worker can finalize success after
its lease generation was recovered, an unresolved action releases its overlap
reservation, per-target read-back can change or omit approved effects while the
executor records success, and durable cancellation never reaches an in-flight
handler. The transaction requirement is also optional for arbitrary `Journal`
implementations, and the memory recovery fixture has its lease predicate
inverted.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Reviewed product:
  `a4b152e20f8f82d53ba6d3c901737804357cc807`; direct parent
  `50e5a3a32e2acf975e3c489e4c75839a0d73a87e`; product tree
  `6346319f2f8d5ae8038019789f19ea7124b1b330`.
- Reviewed handoff:
  `beab28bdfb7cf0aa19870b4c032de3327ed06229`; handoff tree
  `33d0477715a928611dd4f50ecb6aae2992418b2a`. The product is an ancestor of
  the handoff checkpoint.
- Product commit changes only `internal/execution/execution.go` and
  `internal/execution/execution_test.go`. Handoff commit adds only
  `docs/execution/handoffs/W-02.md`.
- Scoped product diff SHA-256:
  `9bafff96b4af9ebfcabe24dff43f723ec3b74b03b483eb61bcab141c9a8fb9d5`.
- Acceptance reviewed: A-14, A-32, A-33, A-34, A-35, A-36, A-59, and A-60.
- Review receipt checkpoint: commit containing this file. Exact SHA is reported
  after commit because a Git commit cannot embed its own SHA.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-w02-review-r1` at exact handoff
commit. Reviewer changed no product, state, task, generated, adapter, client,
storage, migration, or shared fixture path. Adversarial tests ran in an archive
copy of exact product tree under `/tmp`; they are not product changes.

## Findings

### P1: stale worker adopts recovered generation and commits success

`beforeDispatch` validates version, worker, and unexpired lease once, immediately
before the handler call (`execution.go:934-978`, `execution.go:1346-1371`). A
handler may outlive that lease. `RecoverExpiredActionRuns` can then move the row
to `reconciling`, clear ownership, and increment its version. When stale handler
returns, executor updates attempt/effect rows and calls `currentAction`, which
loads recovered generation without verifying `ClaimedBy`, lease, or original
claim version (`execution.go:998-1022`, `execution.go:1542-1554`).
`transitionAt` therefore performs its CAS using newly fetched version rather
than stale worker's claimed generation (`execution.go:1576-1605`). Same adoption
pattern exists in uncertain-dispatch and reconciliation completion paths.

Independent archive-copy probe used real `SQLJournal`: worker blocked in
`Dispatch`, reviewer advanced time beyond lease and called
`RecoverExpired`, then released stale handler. Observed:

```text
TestReviewerProbeSQLExpiredLeaseCannotBeAdoptedByStaleWorker:
stale SQL worker result ... State:succeeded ... Version:4 ... Err:<nil>;
want ErrLeaseLost
```

Recovered row became `succeeded`; stale worker rewrote dispatch attempt/effect
evidence after losing ownership. A second reconciler can operate concurrently,
so this breaks A-33 and A-36 fencing and can hide unresolved late effects.

Required change: retain immutable claim generation and worker identity through
entire call. Every post-dispatch attempt/effect/action write must be fenced by
that generation or a transactional ownership token. A fresh row may reveal
cancellation/deadline state, but must not grant stale worker ownership of newer
generation. Add real-SQL fixture matching probe boundary.

Disposition: `current_blocker`.

### P1: unresolved effect releases reservation and permits conflicting dispatch

Reservation lives only around one `claimAndProcess` call and is always released
by defer (`execution.go:795-806`). `finishDispatchError` persists uncertain state
and returns `reconciling` (`execution.go:1133-1168`), after which defer releases
all resource keys. Another action with same key can immediately claim and call
its handler before first action's read-only reconciliation resolves. Database
CAS separates action rows and cannot detect this cross-action conflict.

Independent single-executor probe dispatched first action, returned
`NewDispatchedFailure(FailureUncertain, ...)`, then ran second queued action with
same reservation key. Observed:

```text
TestReviewerProbeUncertainActionKeepsReservation:
second overlapping action ... State:succeeded ... Dispatched:true ... Err:<nil>
```

First action remained `reconciling` with unresolved effect while second changed
same target. This defeats A-36 and can make A-33/A-59 read-back unable to
attribute resulting object to exact action.

Required change: persist resource reservations or otherwise keep them bound to
unresolved action generation until reconciliation proves applied, absent, or an
operator explicitly resolves conflict. Conflict check must cover separate
workers/process-manager instances and retain handler-provided canonical path,
hardlink-object, descendant, and client-item keys. Add restart and two-worker
fixtures.

Disposition: `current_blocker`.

### P1: per-target read-back identity and completeness are not enforced

After dispatch, executor accepts any `ObserveSatisfied` result whose effects are
internally well formed (`execution.go:986-997`). `mergeEffects` keeps first entry
at each ordinal and silently discards later entry (`execution.go:1420-1435`), so
dispatch effect masks conflicting fresh read-back effect. `recordEffects` also
fills missing reports from existing rows and marks every pending effect applied
when report is empty (`execution.go:1448-1516`). It never compares target kind,
target ID, and effect kind against immutable pending identity at this boundary.

Two independent probes observed successful terminal actions:

```text
TestReviewerProbeReadBackCannotChangeEffectTarget:
Observe requested approved/payload.bin; fresh read-back reported
other/payload.bin; result State:succeeded Outcome:applied Err:<nil>

TestReviewerProbeReadBackCannotOmitPlannedEffects:
Observe requested two targets; fresh read-back returned zero effects; result
State:succeeded Outcome:applied Err:<nil>
```

This permits aggregate false success after partial or misdirected work, contrary
to ordered exact-effect evidence in A-33 and A-59. It also weakens typed handler
semantics required by A-60.

Required change: bind each pending ordinal to immutable target/effect identity.
Fresh read-back or reconciliation must account for every required target and
match that identity before applied/already-satisfied success. Reject duplicate,
missing, extra, or changed identities; never synthesize applied state from an
empty report. Add multi-effect omission, target drift, and reconciliation drift
fixtures against memory and SQL journals.

Disposition: `current_blocker`.

### P1: durable cancellation does not cooperatively stop in-flight dispatch

`Cancel` only calls `RequestCancellation` and returns updated row
(`execution.go:1688-1702`). Executor has no in-flight cancel-function registry or
handler-visible durable cancellation check. `Dispatch` receives caller context
created before cancellation (`execution.go:978`), so API cancellation cannot
signal a long transfer at its next bounded checkpoint. Existing cancellation
test manually releases handler and proves late-effect labeling; it does not prove
cooperative stop.

Independent probe blocked handler on `ctx.Done()`, called `Executor.Cancel`, and
waited for handler checkpoint. Observed:

```text
TestReviewerProbeDurableCancelStopsInFlightHandler:
Cancel did not cancel the in-flight handler context
```

Without manual release the handler continued indefinitely. This fails A-35's
during-transfer cooperative-stop case. Deadline changes have same propagation
gap unless caller independently cancels context.

Required change: make durable cancellation observable by active handler between
bounded chunks, through executor-owned cancellable contexts or explicit typed
checkpoint callback. Preserve late accepted effects and use a non-cancelled,
bounded journal context to persist uncertainty after handler context ends. Add
before, during, after-acceptance, restart, and deadline fixtures.

Disposition: `current_blocker`.

### P2: intent/effect transactions are optional at executor boundary

`Journal` does not require `InTx`; `New` accepts any `Journal`
(`execution.go:412-440`, `execution.go:548-558`). `prepareDispatch` and
`recordEffects` use a transaction only after a runtime type assertion, otherwise
perform sequential writes (`execution.go:1282-1342`, `execution.go:1518-1522`).
Production `SQLJournal` implements transaction support, but executor's public
durability contract allows partial observe/dispatch/effect journal state. That
contradicts task's intent-transaction invariant and handoff claim for all accepted
journals.

Required change: require transactional journal capability when constructing an
executor, or make transaction method part of required journal interface. Add
rollback injection at each write and prove zero handler calls plus one consistent
retryable journal shape.

Disposition: `current_blocker`.

### P2: in-memory expired-lease fixture uses inverted predicate

`memoryLeaseAvailable` returns true for unclaimed or expired leases
(`execution_test.go:1090-1095`), but `RecoverExpired` skips exactly those rows and
recovers rows with active leases (`execution_test.go:824-845`). Consequently the
memory fixture cannot validate product's live expired-lease boundary and would
recover wrong row if exercised through `RunOnce`. Existing tests cover startup
recovery and active same-row claim, not real expired dispatch lease.

Required change: correct predicate and add focused expired-versus-active lease
table plus stale-worker real-SQL race fixture.

Disposition: `current_blocker`.

## Preserved behavior

- Already-materialized observation reaches `already_satisfied` without handler
  dispatch or effect rows in supplied fixture.
- Observe, dispatch, and reconcile attempts receive ordered numbers, and
  `SQLJournal.InTx` groups dispatch intent/effect creation before handler call.
- Uncertain dispatched errors enter reconciliation; supplied safe-retry fixture
  performs read-only reconciliation before another mutation.
- Startup recovery converts running actions and attempts to reconciliation, and
  SQLite reopen fixture preserves that state.
- Dependency waits and next-attempt times persist; cancellation/deadline markers
  prevent a mutation that has not crossed dispatch boundary.
- Direct `RunAction` and polling `RunOnce` use same typed handler path. Package
  has no arbitrary command execution, generic DAG, duplicate outbox, generated
  storage DTO leakage into handlers, or adapter import.

These properties do not compensate for stale ownership, cross-action overlap,
false per-target success, or ineffective in-flight cancellation.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, commit-owned paths, scoped diff | Passed. |
| `GOWORK=off go test -mod=readonly ./internal/execution -count=1 -timeout=30s` | Passed. |
| `GOWORK=off go test -mod=readonly ./internal/execution -race -count=1 -timeout=60s` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/execution` | Passed. |
| `GOWORK=off go test -mod=readonly ./... -count=1 -timeout=120s` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed: generation, staged generation, Vacuum 100/100, architecture, lint, root/nested tests, vet, and module verification. |
| Product diff and receipt `git diff --check` | Passed. |
| Real-SQL expired-lease reviewer probe | Failed unsafe: stale worker finalized recovered row as succeeded. |
| Unresolved-reservation reviewer probe | Failed unsafe: overlapping second action dispatched and succeeded. |
| Target-drift and omitted-effect reviewer probes | Failed unsafe: both reached applied success. |
| Durable-cancel reviewer probe | Failed: in-flight handler context was not cancelled. |
| Live services, credentials, private coordinates, and real media | Intentionally not used. |

## Acceptance disposition

- A-14: accepted for W-02's local executor contribution.
- A-32: partial contribution only. Production SQL transaction exists, but
  approval/queue remains downstream and executor accepts nontransactional
  journals.
- A-33: not accepted. Stale worker can finalize after lease recovery; effect
  identity can be falsely reconciled.
- A-34: accepted for persisted dependency backoff/restart contribution. Full
  workflow outage behavior remains downstream.
- A-35: not accepted. Pre-dispatch marker works, but in-flight cancellation does
  not cooperatively stop handler.
- A-36: not accepted. Reservation ends while first effect remains unresolved.
- A-59: not accepted. Read-back can omit or change exact targets and still mark
  effects applied.
- A-60: partial contribution only. Typed direct/polling path is shared, but exact
  effect and cancellation semantics fail.

## Required correction gate

Do not integrate W-02 into workflow or standalone handlers until all P1/P2
findings are corrected and independently re-reviewed. Correction evidence must
include real-SQL stale-lease race, unresolved overlap across restart/workers,
strict multi-effect identity/read-back, cooperative durable cancellation, and
transaction rollback fixtures. Keep downstream A-32 approval atomicity and A-59
native filesystem publication evidence explicitly open.
