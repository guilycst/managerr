# W-02 independent review, round two

## Decision

`changes_requested`.

The correction closes the original journal-generation adoption defect: a stale
worker cannot write attempts, effects, or terminal action state after its exact
claim is recovered. It also adds required transactional journal capabilities,
persists cross-action reservations, rejects omitted or changed effect identities,
propagates cancellation within one executor, and fixes the supplied memory
expired-lease predicate.

Five boundaries remain. Lease recovery can dispatch a second mutation while
the expired handler is still live, durable cancellation does not reach a handler
owned by another executor sharing the journal, contradictory or empty effect
sets can reach terminal success, uncertain dispatch leaves exact effects marked
pending, and terminal cancellation of idle reconciliation leaks its process-local
reservation. These failures block A-33, A-35, A-36, A-59, and the shared
semantics contribution to A-60.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Original product:
  `a4b152e20f8f82d53ba6d3c901737804357cc807`.
- Round-one review receipt:
  `e65de463ef8258aac5bf02a2a97e7a5e3a36ac34`.
- Base correction commit:
  `8a5574ddb0154b50812dca2ad632acd343e803a6`; tree
  `7cf1de1b4c440b4173fc70e7d2b76ba15cfd488e`.
- Reviewed final product:
  `f56908a19b775dc5218d485a61ffcf0faebb5ace`; tree
  `5f507a78c9b8da030ccd21123643190969c5e977`.
- Reviewed handoff:
  `a3812a94513b74b4d67b412fc90f6d65184872c6`; tree
  `f9f0dc2d0deb9e9d822b7bc754df03e47094ba97`; direct parent is the exact
  final product.
- The base correction changes only `internal/execution/execution.go` and
  `internal/execution/execution_test.go`. The final follow-up changes only
  `internal/execution/execution.go`. The handoff changes only
  `docs/execution/handoffs/W-02-correction-round1.md`.
- The handoff contains no later product change under `internal/execution/`.
- Scoped correction diff SHA-256 from the original product:
  `7bb0f00909f19f9c8faf241998b43e2e93476a23f75427623c38994c9fd89ed4`.
- Acceptance reviewed: A-14, A-32, A-33, A-34, A-35, A-36, A-59, and A-60.
- Review receipt checkpoint: the commit containing this file; its exact SHA is
  reported after commit because a Git commit cannot contain its own SHA.

Review ran from clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-w02-review-r2` at the exact
handoff. Adversarial tests ran only in an archive copy of the exact product
under `/tmp`; they are not product changes. Reviewer changed no product, state,
task, storage, migration, generated, adapter, client, module, or shared-worktree
path.

## Findings

### P1: lease expiry permits a second dispatch while the old handler is live

The claim fence correctly rejects stale journal writes, but it does not stop the
external call. The handler context is bounded by the caller and persisted action
deadline, not by `LeaseUntil`, and there is no lease renewal or generation
checkpoint inside a handler (`execution.go:907-945`, `execution.go:1232-1258`,
`execution.go:1633-1647`).

After recovery, another executor can claim the same action. Reservations are
owned by action ID, so the recovered generation treats the prior reservation as
its own. If read-only reconciliation observes the target absent, it can requeue
and dispatch while the old generation remains blocked in its external call
(`execution.go:979-992`, `execution.go:1371-1400`). The final claim fence stops
the old worker from recording success later, but it cannot undo either external
dispatch.

An independent real-SQL probe blocked the first worker in `Dispatch`, advanced
past its one-second lease, recovered the row, and used a second executor to
reconcile `SafeToRetry`. Before releasing the first handler, the second executor
dispatched and completed another mutation:

```text
fresh worker dispatched 1 mutation(s) while expired handler is still live
result State:succeeded Dispatched:true Outcome:applied
```

Required change: ensure an expired generation cannot remain externally active
while a recovered generation can become safe to retry. Bind cooperative handler
execution to the lease with renewal/fencing, or retain a durable generation
barrier until the earlier dispatch is proven unable to complete. Add the
blocked-live-handler recovery probe for SQL and multiple executor workers.

Disposition: `current_blocker`.

### P1: durable cancellation reaches only the executor that receives Cancel

Each `Executor` owns a private `active` map (`execution.go:613-618`,
`execution.go:706-729`). `Cancel` persists the marker and calls
`cancelActiveHandler` only on that instance (`execution.go:2272-2292`). A handler
running through another executor over the same durable journal receives no
context cancellation and has no typed checkpoint for re-reading the marker.

The supplied same-executor fixture passes. An independent two-executor fixture
started dispatch through one executor and called `Cancel` through another. The
durable marker was committed, but the handler did not observe `ctx.Done()`:

```text
durable cancellation did not reach handler context owned by another executor
```

This contradicts the package's stated support for multiple executors sharing a
journal and fails cooperative cancellation during transfer. Required change:
make durable cancellation observable at bounded handler checkpoints regardless
of which executor instance receives the API request. Keep detached bounded
journal finalization for late effects.

Disposition: `current_blocker`.

### P1: effect validation still permits false or unjournaled success

`Observation.validate` accepts an empty effect slice for
`ObserveNeedsAction` (`execution.go:284-297`). `validateEffectSet` compares only
set size, target/effect identity, and ordinal; it accepts the empty set and does
not validate that a satisfied report's per-target state proves the terminal
outcome (`execution.go:1788-1816`). `recordEffectsInJournal` preserves explicit
`failed`, `cancelled`, or `unknown` states instead of applying the terminal
default (`execution.go:1858-1908`), after which the action transition still
records `succeeded/applied` (`execution.go:1271-1299`).

Two independent fixtures observed terminal false success:

```text
ObserveNeedsAction Effects:[] -> dispatches=1, effects=[], State:succeeded,
Outcome:applied

ObserveSatisfied target=payload.bin state=failed -> State:succeeded,
Outcome:applied, persisted effect State:failed
```

The correction rejects omitted targets only when the initial handler supplied a
nonempty approved set. A typed mutation must have at least one exact approved
effect, and terminal read-back must give every target a state compatible with
the claimed outcome. Reject contradictory states rather than persisting a
succeeded action with failed evidence. Apply the same rule to reconciliation.

Disposition: `current_blocker`.

### P2: uncertain dispatch leaves the exact effect marked pending

`recordUnknownEffectsOwned` calls `recordEffectsInJournal` with an empty report
and `EffectUnknown` (`execution.go:1845-1848`). The implementation then iterates
only the empty `reported` slice, so it never updates the already-created pending
effect (`execution.go:1858-1908`).

An independent persisted-deadline fixture confirmed that the handler context
was cancelled and the action durably reached `deadline_exceeded` with
`UnresolvedCount:1`, but its exact effect remained:

```text
State:pending TargetID:payload.bin EffectKind:copy
```

The same path handles lost responses and caller cancellation. Pending means the
dispatch has not yet been resolved; it does not state that the effect may
already have happened. Update every existing approved target to `unknown` and
bind it to the uncertain attempt without inventing applied evidence.

Disposition: `current_blocker`.

### P2: terminal idle cancellation leaks a process-local reservation

An unresolved action intentionally retains its local reservation when it enters
`reconciling` (`execution.go:933-945`). If it is later cancelled or reaches its
deadline while idle, `claimAndProcess` enters the unclaimed read-only
reconciliation branch and returns before the common release logic
(`execution.go:880-887`). Terminal transition clears the durable reservation,
but the executor's local map still owns it.

An independent fixture produced an uncertain action, cancelled it while idle,
reconciled it to terminal `cancelled`, then triggered an overlapping action on
the same executor. The second action was incorrectly parked:

```text
State:waiting_dependency
Err: execution reservation is held by another action: root:library/show
```

Required change: release the matching local reservation whenever unclaimed
cancellation/deadline reconciliation reaches a terminal state or proves a safe
retry. Keep it retained while the action remains unresolved.

Disposition: `current_blocker`.

## Closed round-one findings

- Post-claim attempt, effect, reservation, and action writes now use the exact
  immutable worker/version/lease fence. The supplied real-SQL test proves a
  stale worker cannot finalize the recovered row.
- Cross-action reservation keys persist in action outcome metadata across a new
  executor. The supplied descendant fixture blocks the overlapping action
  before dispatch.
- Changed identities or ordinals and omitted, extra, or duplicate targets are
  rejected when the approved set is nonempty. Memory and SQL drift fixtures
  pass.
- `New` rejects journals missing transaction, claim-fence, or durable
  reservation capabilities. The dispatch-intent rollback fixture leaves no
  dispatch attempt/effect and calls no handler.
- Same-executor cancellation reaches the in-flight handler context. Persisted
  action deadlines also cancel the handler context and detached journal writes
  complete after that cancellation.
- The in-memory expired-versus-active predicate now recovers only the expired
  claimed row. The SQL stale-generation fixture passes.

These corrections remain accepted, but they do not close the adjacent live
generation, cross-executor cancellation, effect-state, and local-release
boundaries above.

## Independent checks

| Check | Result |
| --- | --- |
| Exact base/product/handoff identity, ancestry, trees, commit-owned paths, and scoped diff | Passed. |
| `GOWORK=off go test -mod=readonly ./internal/execution -count=1 -timeout=120s` | Passed. |
| `GOWORK=off go test -mod=readonly -race ./internal/execution -count=3 -timeout=180s` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/execution` | Passed. |
| Root `GOWORK=off go test -mod=readonly ./... -count=1 -timeout=180s` | Passed. |
| Linux and Darwin amd64/arm64 CGO-free execution test compilation | Passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 44 tasks, 60 acceptance cases, local links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed: reproducible generation, Vacuum 100/100, architecture, lint, tests, vet, and all module verification. |
| Product/handoff `git diff --check` and no execution delta after product | Passed. |
| Real-SQL expired-live-handler retry probe | Failed unsafe: second mutation dispatched before old handler returned. |
| Two-executor durable-cancellation probe | Failed: running handler context was not cancelled. |
| Empty and contradictory per-target effect probes | Failed unsafe: both reached `succeeded/applied`. |
| Persisted-deadline per-effect uncertainty probe | Failed evidence: action unresolved, exact effect remained `pending`. |
| Terminal idle-cancellation reservation probe | Failed: stale local key blocked later overlapping work. |
| Live services, credentials, private coordinates, and real media | Not used. |

## Acceptance disposition

- A-14: preserved for W-02's already-materialized observation path.
- A-32: W-02's required transactional journal and dispatch-intent rollback
  contribution is accepted. Approval/enqueue atomicity remains W-04 scope.
- A-33: not accepted. An expired live handler and recovered worker can dispatch
  the same mutation concurrently; uncertain exact effects also remain pending.
- A-34: preserved for persisted backoff and configured deadline handling.
- A-35: not accepted. Same-executor cancellation passes, but another executor
  cannot cooperatively stop the in-flight handler and terminal uncertainty is
  mislabeled per target.
- A-36: not accepted. Durable cross-action overlap passes, but lease-generation
  overlap can duplicate one action and terminal cancellation leaks local keys.
- A-59: not accepted for W-02. Changed/omitted identities are rejected, but
  empty and failed target evidence can still produce aggregate success.
- A-60: partial contribution only. Direct and polling paths share the typed
  handler, but cancellation and effect semantics are not consistent across the
  supported executor boundary.

Do not integrate W-02 into standalone or workflow handlers until the P1
findings are corrected and independently re-reviewed. This receipt does not
approve release, deployment, or live filesystem/upstream mutation.
