# W-02 independent review, round six

## Decision

`changes_requested`.

The correction closes the exact round-five reproduction. When every
attempt-level return-intent update fails, the action-level exact-attempt marker
survives the bounded retry window. A fresh scheduler later discovers that
marker, transitions only the named running dispatch to uncertainty, performs
one read-only reconciliation, and makes zero dispatch calls. The inverse
action-marker outage, exact-ID fencing, SQLite reopen, lease renewal, effect
accounting, and cancellation behavior also remain green.

One P2 remains at the shared journal-write boundary. If both exact recovery
markers are unavailable for the bounded window, the executor forgets in-memory
knowledge that the handler returned before either marker is durable. Once
writes recover, an ordinary fresh scheduler still treats the dispatch as live
and performs no reconciliation until process startup recovery. This leaves the
W-02 contributions to A-33, A-34, A-35, and A-59 incomplete.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Prior reviewed product:
  `4af1583d393fa9606ce8682585056b7c1ce1d3f8`; tree
  `cc82e87dd39d90b892fc1365db9d31e426d1a5e4`.
- Prior review receipt:
  `a82461e2954f8a7cadd25f714c720ab1f7fe3727`.
- Reviewed correction product:
  `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a`; tree
  `ad47193bbf1cb4f36cdec6d048447e7be182ac07`.
- Reviewed handoff:
  `019164e45d90b683374989c53134f0b2be08d45f`; tree
  `81f60de74e12c932485712a511e81fc69b887e0d`; its direct parent is the exact
  correction product.
- The product commit changes only `internal/execution/execution.go` and
  `internal/execution/execution_test.go`. The handoff adds only
  `docs/execution/handoffs/W-02-correction-round5.md` and contains no later
  execution delta.
- Scoped `internal/execution` diff SHA-256 from the prior product:
  `8828a4cc4391f1f70c760cebcaa373da55ba36fcd7ba2d1f4626b138653a6372`.
- Acceptance reviewed: A-14, A-32, A-33, A-34, A-35, A-36, A-59, and A-60.
- Review receipt checkpoint: the commit containing this file; its exact SHA is
  reported after commit because a Git commit cannot contain its own SHA.

Review ran from the clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-w02-review-r6` at the exact
handoff. Reviewer-only adversarial tests ran in an archive of the exact product
at `/tmp/mastarr-w02-r6-probe.iN3UW3`; they are not product changes. Reviewer
changed no product, state, task, storage, migration, generated, adapter, client,
module, UI, or shared-checkout path.

## Finding

### P2: simultaneous recovery-marker outage still exhausts returned-handler recovery

`releaseDispatchBarrierWithClaim` now tries the attempt-level intent and then
the action-level returned marker. This correctly makes either successful write
a durable recovery path (`execution.go:1792-1833`). If both writes return an
error, the dispatch defer starts `scheduleBarrierRetry`. That goroutine still
runs only 32 times and then removes its process-local entry without retaining
another trigger (`execution.go:1748-1776`). Fresh processing can release the
barrier only when it finds one of those markers (`execution.go:2034-2064`).

An independent fixture blocked only updates carrying
`dispatchBarrierRetryAttempt` or `dispatchBarrierReturnedAttempt`. Reads,
recovery, claims, and unrelated writes remained available. The initial
post-handler call and all 32 background retries reached both injected write
failures. After the handler had returned and the retry entry had disappeared,
the fixture restored both writes and invoked `RunOnce` on a fresh executor. It
persisted:

```text
Action state: reconciling
Dispatch attempt state: running
Reason: dispatch_still_active
Unresolved effects: 1
Reconcile calls: 0
Attempt evidence: {}
Action outcome marker: absent
```

The correction proves each marker is an effective fallback when the other
remains writable. It does not retain returned-handler knowledge through a
temporary journal-write outage affecting both transactions. This is a
realistic boundary for a database busy/read-only/unavailable interval and is
the same durability requirement from round five: some exact evidence or the
barrier transition must become durable before local retry knowledge is
discarded.

Required change: keep retrying the exact post-return recovery until one marker
or the exact attempt transition is durable, or until executor shutdown makes
startup recovery the explicit continuation. After availability returns,
ordinary scheduling must resume read-only reconciliation without requiring an
operator restart. Keep the running-attempt barrier and exact attempt-ID checks
while no durable return evidence exists. Add a fixture that blocks both marker
transactions beyond the local retry window, restores them, and proves a fresh
scheduler reconciles once with zero dispatches.

Disposition: `current_blocker`.

## Accepted correction behavior

- Blocking the attempt-level intent for the initial call and all 32 retries
  leaves the exact action-level marker durable. A fresh scheduler later writes
  the attempt intent, releases the exact barrier, and reconciles once without
  dispatching.
- Blocking the action-level marker for the full window still recovers from the
  durable attempt-level intent, including across a real SQLite close/reopen.
- Mismatched attempt evidence cannot release another running dispatch.
- A dispatch that has not returned remains a live barrier; recovered workers
  perform neither reconciliation nor mutation through it.
- No-deadline lease renewal, exact multi-effect unresolved accounting, and
  cross-executor cancellation during initial observation remain green.
- Strict terminal effect identity/completeness, durable reservations, claim
  generations, observe-before-write, cancellation, deadline, and restart
  behavior remain green.

These behaviors remain accepted. The finding concerns the interval before
either returned-handler marker can be committed.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, commit-owned paths, and scoped diff | Passed. |
| Product and handoff `git diff --check`; no execution delta after product | Passed. |
| Seven producer correction and retained regression fixtures, count 50 | Passed. |
| Prior independent attempt-intent outage reproduction, count 20 | Passed against this correction. |
| `GOWORK=off go test -mod=readonly ./internal/execution -count=1 -timeout=180s` | Passed. |
| `GOWORK=off go test -mod=readonly -race ./internal/execution -count=3 -timeout=300s` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/execution` | Passed. |
| Seven independent action/attempt-marker, SQLite reopen, exact-ID, lease, effect, and cancellation probes under race, count 3 | Passed. |
| Linux and Darwin amd64/arm64 CGO-free execution compilation | Passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 44 tasks, 60 acceptance cases, local links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed: reproducible generation, Vacuum 100/100, architecture, lint, tests, vet, and all module verification. |
| Combined attempt/action marker outage extending beyond all local retries | Failed deterministically: restored writes plus fresh `RunOnce` left the exact dispatch attempt running and made zero reconciliation calls. |
| Live services, credentials, private coordinates, and real media | Not used. |

## Acceptance disposition

- A-14: preserved for same-key replay and already-materialized success.
- A-32: W-02 transaction and dedupe behavior remains accepted; approval/enqueue
  atomicity remains W-04 scope.
- A-33: not accepted. Blind redispatch remains blocked, but ordinary recovery
  cannot resume when neither returned-handler marker commits during the bounded
  window.
- A-34: not accepted. Lease renewal and action backoff pass; returned-handler
  retry is still bounded before any retry evidence is necessarily durable.
- A-35: not accepted. Cooperative cancellation passes, but reconciliation and
  cancellation completion can remain stuck until restart after the combined
  write outage.
- A-36: accepted for W-02. Exact attempt fencing, durable/local reservations,
  and live-handler exclusion remain intact.
- A-59: not accepted for W-02. Exact effect evidence and read-back pass, but
  post-publication uncertainty can remain blocked until restart if neither
  returned-handler marker is durable.
- A-60: W-02's typed direct and scheduled paths preserve shared validation,
  idempotency, cancellation, and claim-fence behavior.

Do not integrate W-02 into standalone or workflow handlers until the P2 is
corrected and independently re-reviewed. This receipt does not approve release,
deployment, or live filesystem/upstream mutation.
