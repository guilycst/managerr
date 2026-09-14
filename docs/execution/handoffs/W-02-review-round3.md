# W-02 independent review, round three

## Decision

`changes_requested`.

The correction closes the five exact defects from round two at their tested
boundaries. A recovered worker cannot cross a still-running dispatch attempt,
dispatch and reconciliation handlers observe cancellation written through a
different executor, empty or contradictory terminal effect evidence is
rejected, uncertain dispatch changes every planned effect to `unknown`, and
idle terminal reconciliation releases the process-local reservation.

Four adjacent correctness gaps remain. The worker lease is now an implicit
maximum handler duration even when the action has no deadline, multi-target
uncertainty persists an incorrect aggregate unresolved count, a transient
journal failure can strand the live-dispatch barrier without an in-process
retry, and cross-executor cancellation still does not reach the initial
`Observe` handler. These block the W-02 contributions to A-33, A-34, A-35,
A-59, and A-60.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Prior reviewed product:
  `f56908a19b775dc5218d485a61ffcf0faebb5ace`; tree
  `5f507a78c9b8da030ccd21123643190969c5e977`.
- Prior review receipt:
  `6e5c8c6d8d882dc5cdf3237fc47e7d571ba69136`.
- Reviewed correction product:
  `04c2e245a9624287417522c9f876056090016b83`; tree
  `718a33a9d0b78b92ec3173bcdd2618d537d4710d`.
- Reviewed handoff:
  `23d1fecb1ff7ff95a24f871acb4847def9fef0a6`; tree
  `96a357f7c73e88b55acdb217ffd4a77a69ed5e7c`; its direct parent is the exact
  correction product.
- The correction product commit changes only
  `internal/execution/execution.go` and
  `internal/execution/execution_test.go`. The handoff commit adds only
  `docs/execution/handoffs/W-02-correction-round2.md` and contains no later
  product delta.
- Scoped `internal/execution` diff SHA-256 from the prior product:
  `525cd306adc4e41b3bc607b02c9d6a73334efc4bf6eb4735d79acd574175ba61`.
- Acceptance reviewed: A-14, A-32, A-33, A-34, A-35, A-36, A-59, and A-60.
- Review receipt checkpoint: the commit containing this file; its exact SHA is
  reported after commit because a Git commit cannot contain its own SHA.

Review ran from the clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-w02-review-r3` at the exact
handoff. Reviewer-only adversarial tests ran in an archive of the exact product
at `/tmp/mastarr-w02-r3-probe.11SGHF`; they are not product changes. Reviewer
changed no product, state, task, storage, migration, generated, adapter, client,
module, UI, or shared-checkout path.

## Findings

### P1: the worker lease is an implicit transfer timeout with no renewal

`handlerContext` chooses the earlier of `Action.DeadlineAt` and
`Action.LeaseUntil` and constructs a real context timeout
(`execution.go:1873-1897`). The watcher independently cancels a handler once
the fixed lease expires (`execution.go:799-835`). No renewal or heartbeat
extends the claim while a healthy handler makes progress.

This means the default 30-second lease bounds every dispatch and reconciliation
call even when `DeadlineAt` is empty. A cooperative copy that needs longer than
one lease cannot complete in that call; if cancellation discards partial work
and read-only reconciliation proves safe to retry, each new attempt receives
the same fixed maximum and can repeat forever.
The durable running-attempt barrier prevents a duplicate while the old handler
ignores cancellation, but it does not let a legitimate long transfer retain
ownership.

An independent fixture used no action deadline, a 20 ms lease, and a
cooperative handler that would complete after 100 ms. The executor cancelled
it at the lease boundary and returned:

```text
State:running Dispatched:true
Err: execution lease was lost
```

Required change: renew the persisted lease with the same immutable generation
while the handler remains healthy, or keep the durable barrier without treating
lease expiry as a configured operation deadline. Preserve the stale-generation
fence and cancellation watcher. Add a deterministic handler that outlives at
least one lease and still completes when `DeadlineAt` is absent.

Disposition: `current_blocker`.

### P2: multi-target uncertainty records one unresolved effect

The correction correctly converts every planned effect to `unknown`, but
`transitionUncertainOwned` still hardcodes both `UnresolvedCount` and the
`unresolvedEffects` outcome field to `1` (`execution.go:2382-2396`). The active
dispatch deferral path also hardcodes one (`execution.go:1466-1478`).

An independent lost-response fixture approved two exact targets. Both durable
effect rows became `unknown`, while the action exposed:

```text
UnresolvedCount: 1
Outcome: {"reason":"dispatch_uncertain","unresolvedEffects":1}
Unknown effect rows: 2
```

This misstates per-file recovery state to API and workflow consumers. Required
change: derive the unresolved count from the exact unresolved effect set in the
same transaction that records or observes it. Cover multi-file uncertainty,
barrier deferral, partial terminal reconciliation, and the transition back to
zero.

Disposition: `current_blocker`.

### P2: a failed barrier release is ignored and never retried in-process

The dispatch defer discards every error from `releaseDispatchBarrier`
(`execution.go:1369-1379`). That method is the only normal path that changes a
stale generation's exact running dispatch attempt to reconciliation after its
handler returns (`execution.go:1489-1530`). Future processing sees the attempt
as active and only persists another delay (`execution.go:1447-1487`,
`execution.go:1544-1551`); it never retries the release transaction.

An independent fault journal rejected exactly the first barrier
`UpdateAttempt`, then allowed all later operations. After the old handler had
returned, a fresh executor still made zero reconciliation calls and persisted:

```text
Action state: reconciling
Attempt phase/state: dispatch/running
Reason: dispatch_still_active
```

The row remains blocked until an explicit process restart invokes broad startup
recovery. A single SQLite busy, timeout, cancellation, or transient write error
at this point defeats eventual reconciliation and the cancellation escape
hatch. Required change: surface and durably retry exact-attempt barrier release,
or let the polling path safely complete the same idempotent transition. Keep the
attempt-ID check so an old worker cannot change a later attempt.

Disposition: `current_blocker`.

### P2: cross-executor cancellation does not reach initial Observe

The new durable watcher wraps dispatch and claimed reconciliation handlers, but
`processObserved` calls the initial `handler.Observe(ctx, action)` with the raw
caller context (`execution.go:1280-1289`). It does not use `handlerContext`,
register the active call, or start `watchHandlerState` until after observation
and dispatch intent creation (`execution.go:1366-1369`).

An independent fixture blocked initial observation, requested cancellation
through a second executor sharing the journal, and waited 100 ms with a 2 ms
poll configuration. The handler never observed `ctx.Done()`:

```text
durable cancellation did not reach initial Observe owned by another executor
```

The later `beforeDispatch` check still prevents a mutation after the observer
returns, so this is fail-closed for writes. It can nevertheless leave a slow or
hung upstream read holding work after cancellation or lease expiry. Required
change: apply the same persisted deadline, lease, and durable cancellation
context to every claimed handler phase, including initial observation. Preserve
the final pre-dispatch read of the cancellation marker.

Disposition: `current_blocker`.

## Closed round-two findings

- A recovered action with a still-running dispatch attempt is delayed without
  any reconciliation or new dispatch. When the old handler returns, its exact
  attempt and effects become uncertain before read-only reconciliation resumes.
- Durable cancellation written by another executor reaches dispatch and claimed
  reconciliation handler contexts through the journal watcher. The old claim
  cannot commit terminal success after the version changes.
- `ObserveNeedsAction` requires a nonempty exact effect set. Empty, omitted,
  changed, duplicate, reordered, failed, cancelled, and unknown terminal
  evidence cannot be promoted to `succeeded/applied` at the covered observe,
  dispatch read-back, or reconciliation boundaries.
- Lost dispatch responses update every previously planned target from `pending`
  to `unknown` without synthesizing an applied outcome. Exact target identity,
  effect kind, and ordinal stay journal-owned.
- Unclaimed terminal cancellation, deadline, and proven-safe retry release the
  matching process-local reservation. Durable overlap protection remains while
  the action is unresolved.
- Prior transaction capability, claim-generation fencing, reservation
  persistence, observe-before-write, backoff, restart recovery, and SQL fixtures
  remain green.

These corrections remain accepted. The findings above concern lease duration,
aggregate uncertainty, barrier-release failure, and an uncovered handler phase;
they do not reopen the repaired exact fixtures.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, commit-owned paths, and scoped diff | Passed. |
| Product and handoff `git diff --check`; no execution delta after product | Passed. |
| Ten named correction and retained-recovery fixtures | Passed. |
| `GOWORK=off go test -mod=readonly ./internal/execution -count=1 -timeout=180s` | Passed. |
| `GOWORK=off go test -mod=readonly -race ./internal/execution -count=3 -timeout=240s` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/execution` | Passed. |
| Root `GOWORK=off go test -mod=readonly ./... -count=1 -timeout=240s` | Passed. |
| Linux and Darwin amd64/arm64 CGO-free execution test compilation | Passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 44 tasks, 60 acceptance cases, local links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed: reproducible generation, Vacuum 100/100, architecture, lint, tests, vet, and all module verification. |
| No-deadline handler outliving one lease probe | Failed: the lease cancelled the handler and produced `ErrLeaseLost`. |
| Two-target lost-response unresolved-count probe | Failed: two unknown rows, aggregate count one. |
| One-shot barrier-release transaction failure probe | Failed: later polling never retried release or reconciliation. |
| Cross-executor cancellation during initial Observe probe | Failed: handler context remained live. |
| Live services, credentials, private coordinates, and real media | Not used. |

## Acceptance disposition

- A-14: preserved for same-key replay and fresh-key already-satisfied behavior.
- A-32: W-02's transaction and dedupe contribution remains accepted;
  approval/enqueue atomicity remains W-04 scope.
- A-33: not accepted. Blind redispatch while the prior handler is live is
  blocked, but one failed barrier-release write can prevent reconciliation
  indefinitely and multi-target uncertainty is summarized incorrectly.
- A-34: not accepted. Persisted backoff remains, but every healthy handler has
  an unrenewed lease timeout even when the action has no configured deadline.
- A-35: not accepted. Dispatch and reconciliation cancellation pass; initial
  observation does not observe durable cancellation, and lease expiry can stop
  an otherwise healthy transfer.
- A-36: accepted for W-02's durable/local overlap contribution. Stale handlers
  cannot overlap recovered dispatch, and terminal idle reservations are
  released.
- A-59: not accepted for W-02. Exact unknown effect rows are preserved, but the
  aggregate unresolved count can disagree with that authoritative set.
- A-60: partial contribution only. Typed phases share validation and claim
  fences, but cancellation semantics differ between initial observation and
  dispatch/reconciliation.

Do not integrate W-02 into standalone or workflow handlers until the P1 and P2
findings are corrected and independently re-reviewed. This receipt does not
approve release, deployment, or live filesystem/upstream mutation.
