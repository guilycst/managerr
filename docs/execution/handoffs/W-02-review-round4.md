# W-02 independent review, round four

## Decision

`changes_requested`.

The correction closes three of the four round-three findings. A healthy
no-deadline handler renews its lease and completes across multiple lease
intervals, multi-file uncertainty exposes the exact unresolved count, and
durable cancellation reaches the initial `Observe` call across executor
instances. The returned-dispatch barrier also recovers from the producer's
covered single transient failure.

One P2 remains. The evidence that a dispatch handler returned is itself written
only during a bounded in-memory retry window. If that marker write is
unavailable through the window, no durable state distinguishes the returned
handler from a still-running handler. Later processing therefore keeps
deferring reconciliation until broad startup recovery runs. This leaves the
W-02 contributions to A-33, A-34, A-35, and A-59 incomplete.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Prior reviewed product:
  `04c2e245a9624287417522c9f876056090016b83`; tree
  `718a33a9d0b78b92ec3173bcdd2618d537d4710d`.
- Prior review receipt:
  `6f0fec3d84f7b88e25609bd5066a185268c27adc`.
- Reviewed correction product:
  `d10bcb6bba9bc59ba80819950991b45cb05bce99`; tree
  `b25a07806d250d3c0b565088d82b5e7f20b36499`.
- Reviewed handoff:
  `f402eec6f9c308313399d1c471c73d48a68009f4`; tree
  `d6fc323ca687a53fcd0368e561cd578f2bac50d2`; its direct parent is the exact
  correction product.
- The product commit changes only `internal/execution/execution.go` and
  `internal/execution/execution_test.go`. The handoff commit adds only
  `docs/execution/handoffs/W-02-correction-round3.md` and contains no later
  execution delta.
- Scoped `internal/execution` diff SHA-256 from the prior product:
  `5b00ebba7ea8a796b9b8deac48c91740829c817d2659d4b8a255de7df6457320`.
- Acceptance reviewed: A-14, A-32, A-33, A-34, A-35, A-36, A-59, and A-60.
- Review receipt checkpoint: the commit containing this file; its exact SHA is
  reported after commit because a Git commit cannot contain its own SHA.

Review ran from the clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-w02-review-r4` at the exact
handoff. Reviewer-only adversarial tests ran in an archive of the exact product
at `/tmp/mastarr-w02-r4-probe.LXXrFc`; they are not product changes. Reviewer
changed no product, state, task, storage, migration, generated, adapter, client,
module, UI, or shared-checkout path.

## Finding

### P2: a marker-write outage exhausts the in-memory barrier retry and strands reconciliation

When the dispatch defer cannot release its exact barrier, it starts
`scheduleBarrierRetry` (`execution.go:1562-1573`). That goroutine retries only
32 times, no more than once every 100 ms, then deletes its process-local dedupe
entry and exits without persisting another retry trigger
(`execution.go:1684-1713`). `releaseDispatchBarrier` must first persist
`dispatchBarrierReturnedAttempt` before it changes the running attempt and
effects (`execution.go:1719-1782`, `execution.go:1804-1855`). This marker is the
only evidence that lets another executor distinguish a returned handler from a
still-running handler.

An independent journal wrapper rejected only outcome updates containing the
returned-attempt marker. It rejected the initial marker write and all 32
background retries while every other journal operation remained available.
After the old handler returned and the retry goroutine ended, marker writes
were restored. A fresh executor then processed the same action. It made zero
reconciliation calls and persisted:

```text
Action state: reconciling
Dispatch attempt state: running
Reason: dispatch_still_active
Unresolved effects: 1
Reconcile calls: 0
```

The producer fixture proves a short transient failure that clears inside the
in-memory window. It does not prove durable recovery after the marker itself
cannot be written through that window. The state is fail-closed for another
mutation, but reconciliation and the cancellation escape hatch remain stuck
until process restart invokes broad recovery.

Required change: preserve retry intent until the exact returned-attempt marker
or the exact barrier transition is durable. A later executor must be able to
resume after journal availability returns without relying on process restart.
Keep exact attempt identity and the live-handler barrier so polling cannot
release a handler that may still be running. Add a deterministic fixture where
the marker write remains unavailable beyond the local retry window, becomes
available again, and ordinary processing reaches read-only reconciliation.

Disposition: `current_blocker`.

## Closed round-three findings

- Lease renewal uses an exact persisted generation fence. A real SQLite fixture
  ran a no-deadline dispatch for 140 ms with a 35 ms lease and 10 ms renewal
  interval; it completed `succeeded/applied` across several lease periods.
- Lost-response handling for four approved effects persisted all four as
  `unknown` and exposed `UnresolvedCount == 4`.
- Cancellation requested through a second executor cancelled a blocked initial
  `Observe`, finalized the action as cancelled, and never dispatched.
- The retained stale-handler fencing, strict terminal effect validation,
  uncertain-effect persistence, reservation cleanup, transaction capability,
  observe-before-write, backoff, deadline, and overlap fixtures remain green.

These corrections remain accepted. The finding above concerns recovery when
the new returned-attempt marker cannot be written for the complete bounded
retry interval.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, commit-owned paths, and scoped diff | Passed. |
| Product and handoff `git diff --check`; no execution delta after product | Passed. |
| Four named correction fixtures and retained expired-live-dispatch fixture | Passed. |
| Producer barrier fixture repeated 20 times | Passed. |
| `GOWORK=off go test -mod=readonly ./internal/execution -count=1 -timeout=180s` | Passed. |
| `GOWORK=off go test -mod=readonly -race ./internal/execution -count=3 -timeout=300s` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/execution` | Passed. |
| Root `GOWORK=off go test -mod=readonly ./... -count=1 -timeout=240s` | Passed. |
| Linux and Darwin amd64/arm64 CGO-free execution compilation | Passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 44 tasks, 60 acceptance cases, local links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed: reproducible generation, Vacuum 100/100, architecture, lint, tests, vet, and all module verification. |
| Reviewer no-deadline SQLite renewal probe | Passed. |
| Reviewer four-effect uncertainty/count probe | Passed. |
| Reviewer cross-executor initial-observe cancellation probe | Passed. |
| Reviewer probes under the race detector, count 3 | Passed. |
| Returned-marker outage extending beyond all local retries | Failed: restored journal writes did not cause later reconciliation; the exact dispatch attempt remained `running`. |
| Live services, credentials, private coordinates, and real media | Not used. |

## Acceptance disposition

- A-14: preserved for same-key replay and already-materialized success.
- A-32: W-02 transaction and dedupe behavior remains accepted; approval/enqueue
  atomicity remains W-04 scope.
- A-33: not accepted. Blind redispatch remains blocked, but ordinary recovery
  cannot resume after the returned-attempt marker misses its bounded window.
- A-34: not accepted. Lease renewal and persisted action backoff pass, but the
  barrier-release retry itself is only transient in-memory work.
- A-35: not accepted. Initial-observe cancellation is fixed; a returned handler
  can still leave reconciliation and cancellation completion stuck until
  restart after a marker-write outage.
- A-36: accepted for W-02's durable and process-local overlap contribution.
  Exact stale-generation fencing and live-handler exclusion remain intact.
- A-59: not accepted for W-02. Exact effects and counts pass, but uncertain
  post-dispatch recovery can remain blocked after publication until restart.
- A-60: W-02's typed handler phases now share cancellation and claim fencing;
  this correction does not broaden upstream or filesystem capabilities.

Do not integrate W-02 into standalone or workflow handlers until the P2 is
corrected and independently re-reviewed. This receipt does not approve release,
deployment, or live filesystem/upstream mutation.
