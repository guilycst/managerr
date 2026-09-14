# W-02 independent review, round five

## Decision

`changes_requested`.

The correction adds exact attempt-level evidence before the action outcome
marker. That evidence survives all 33 blocked action-marker writes, is found by
a fresh executor, and drives one read-only reconciliation with zero dispatches.
It also survives a real SQLite store close/reopen, and mismatched attempt IDs do
not release a live barrier.

One P2 remains. The new attempt-level evidence is itself written only through
the same bounded in-memory retry. If that prerequisite marker cannot be
persisted for the initial call and all 32 retries, no durable retry intent
exists. Once journal writes recover, an ordinary fresh scheduler still treats
the returned dispatch as live and performs no reconciliation until process
startup recovery. This leaves the W-02 contributions to A-33, A-34, A-35, and
A-59 incomplete.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Prior reviewed product:
  `d10bcb6bba9bc59ba80819950991b45cb05bce99`; tree
  `b25a07806d250d3c0b565088d82b5e7f20b36499`.
- Prior review receipt:
  `ef07140a8d41696c4e4514e6860e47e6211b8814`.
- Reviewed implementation commit:
  `a645abe60341a3f21a71aaabf3ba5364eeb10a09`; tree
  `a09c2abcd3845332e034c03455703f0a1a4e5cb0`.
- Reviewed final product:
  `4af1583d393fa9606ce8682585056b7c1ce1d3f8`; tree
  `cc82e87dd39d90b892fc1365db9d31e426d1a5e4`.
- Reviewed handoff:
  `8b396e92f89ab8f34ed7f4ee129012822e3007aa`; tree
  `54b3b42592049804cf5eb79e3935856be99127a9`; its direct parent is the exact
  final product.
- The implementation commit changes only `internal/execution/execution.go`
  and `internal/execution/execution_test.go`; the final product commit changes
  only `internal/execution/execution_test.go`. The handoff adds only
  `docs/execution/handoffs/W-02-correction-round4.md` and contains no later
  execution delta.
- Scoped `internal/execution` diff SHA-256 from the prior product:
  `b360a2c3fefa2b6b59ea2b93903f10aed3659129cae6c7bb62c55f3435ecf108`.
- Acceptance reviewed: A-14, A-32, A-33, A-34, A-35, A-36, A-59, and A-60.
- Review receipt checkpoint: the commit containing this file; its exact SHA is
  reported after commit because a Git commit cannot contain its own SHA.

Review ran from the clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-w02-review-r5` at the exact
handoff. Reviewer-only adversarial tests ran in an archive of the exact product
at `/tmp/mastarr-w02-r5-probe.WxvahQ`; they are not product changes. Reviewer
changed no product, state, task, storage, migration, generated, adapter, client,
module, UI, or shared-checkout path.

## Finding

### P2: failure of the new durable intent write still exhausts all recovery work

`releaseDispatchBarrierWithClaim` first calls
`persistDispatchBarrierIntent`. A non-lease journal error returns immediately,
before the action-level marker or exact attempt/effect transition is attempted
(`execution.go:1792-1806`). Its caller starts `scheduleBarrierRetry`, but that
goroutine still runs only 32 times and exits without recording another retry
trigger (`execution.go:1748-1776`). The intent write itself is an unclaimed
transaction that updates the exact running attempt (`execution.go:1871-1905`).
Fresh-executor discovery requires that update to have succeeded
(`execution.go:1926-1954`, `execution.go:2030-2047`).

An independent fault journal rejected `UpdateAttempt` only when the exact
`dispatchBarrierRetryAttempt` evidence was present. It rejected the initial
write and every bounded background retry while other journal operations
remained available. After the handler returned and the retry goroutine ended,
the fixture restored intent writes and invoked `RunOnce` on a fresh executor.
The scheduler made zero reconciliation calls and persisted:

```text
Action state: reconciling
Dispatch attempt state: running
Reason: dispatch_still_active
Unresolved effects: 1
Reconcile calls: 0
Attempt evidence: {}
```

The new producer fixture blocks only action `UpdateOutcome` calls containing
`dispatchBarrierReturnedAttempt`; the preceding attempt-level intent succeeds.
It therefore proves the second marker can be unavailable indefinitely after
the first marker is durable. It does not prove recovery when the first durable
marker is unavailable through the bounded window. A temporary transaction or
database outage can affect that prerequisite write and reproduce the stranded
state.

Required change: keep recoverable evidence until the exact returned-attempt
intent or barrier transition is durable. If the intent transaction cannot
commit, later ordinary processing must resume after availability returns
without requiring process restart. Preserve the exact-attempt match and the
live-handler barrier. Add a fixture that rejects the attempt-level intent write
for longer than the local retry window, restores it, and proves a fresh
scheduler performs read-only reconciliation with zero dispatches.

Disposition: `current_blocker`.

## Accepted correction behavior

- Blocking every action-outcome return marker for the initial call and all 32
  background retries leaves exact durable intent on the running attempt. Once
  writes recover, a fresh executor releases that attempt and reconciles once
  without dispatching.
- The same action-marker outage path survived closing and reopening a real
  SQLite store before the fresh executor ran.
- Attempt evidence naming another attempt ID was ignored. The actual live
  dispatch stayed `running`, and neither reconciliation nor dispatch ran.
- The producer's transient barrier fixture and retained live-handler fixture
  passed repeatedly. No polling worker released a handler before its return.
- No-deadline lease renewal, exact four-effect uncertainty accounting, and
  cross-executor cancellation during initial `Observe` remain green.
- Retained claim fencing, strict effect identity/completeness, reservation,
  cancellation, deadline, observe-before-write, and restart tests remain green.

These behaviors remain accepted. The finding concerns failure of the new
prerequisite intent write, before any discoverable evidence becomes durable.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, commit-owned paths, and scoped diff | Passed. |
| Product and handoff `git diff --check`; no execution delta after final product | Passed. |
| Six named correction and retained regression fixtures, count 20 | Passed. |
| Producer action-marker outage fixture, count 100 | Passed. |
| Producer transient barrier fixture, count 100 | Passed. |
| `GOWORK=off go test -mod=readonly ./internal/execution -count=1 -timeout=180s` | Passed. |
| `GOWORK=off go test -mod=readonly -race ./internal/execution -count=3 -timeout=300s` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/execution` | Passed. |
| Linux and Darwin amd64/arm64 CGO-free execution compilation | Passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 44 tasks, 60 acceptance cases, local links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed: reproducible generation, Vacuum 100/100, architecture, lint, tests, vet, and all module verification. |
| Independent action-marker outage and fresh-executor probe, count 20 | Passed. |
| Independent SQLite action-marker outage, close/reopen, and fresh-executor probe, count 20 | Passed. |
| Independent mismatched attempt-intent probe, count 20 | Passed. |
| Independent action-marker, SQLite reopen, and exact-ID probes under race, count 3 | Passed. |
| Attempt-level intent outage extending beyond all local retries | Failed deterministically: restored writes plus fresh `RunOnce` left the exact dispatch attempt running and made zero reconciliation calls. |
| Live services, credentials, private coordinates, and real media | Not used. |

## Acceptance disposition

- A-14: preserved for same-key replay and already-materialized success.
- A-32: W-02 transaction and dedupe behavior remains accepted; approval/enqueue
  atomicity remains W-04 scope.
- A-33: not accepted. Blind redispatch remains blocked, but recovery cannot
  resume through ordinary scheduling if the first returned-handler marker
  misses the bounded retry window.
- A-34: not accepted. Lease renewal and persisted action backoff pass; the
  returned-handler retry still expires after bounded process-local work before
  its retry intent is necessarily durable.
- A-35: not accepted. Cancellation reaches every covered handler phase, but a
  returned handler can still leave cancellation completion and reconciliation
  stuck until restart after an intent-write outage.
- A-36: accepted for W-02. Exact attempt-ID fencing, durable/local reservations,
  and live-handler exclusion remain intact.
- A-59: not accepted for W-02. Exact effects and read-back validation pass, but
  post-publication uncertainty can remain blocked until restart when the first
  durable intent write fails throughout the local retry window.
- A-60: W-02's typed direct and scheduled paths preserve shared validation,
  idempotency, cancellation, and claim-fence behavior.

Do not integrate W-02 into standalone or workflow handlers until the P2 is
corrected and independently re-reviewed. This receipt does not approve release,
deployment, or live filesystem/upstream mutation.
