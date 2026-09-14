# W-02 correction round three handoff

## Assignment

- Task ID and title: W-02 correction round three, lease renewal, uncertainty
  accounting, barrier retry and initial-observe cancellation.
- Owner and independent reviewer: `/root/x05_implementer`; `/root/x05_reviewer`.
- Base product: `04c2e245a9624287417522c9f876056090016b83`.
- Prior review receipt: `6f0fec3d84f7b88e25609bd5066a185268c27adc`.
- Shared checkout: `main`. The coordinator owns `docs/execution/state.json` and
  integration.
- Owned paths: `internal/execution/` and this handoff only. No state, task,
  storage, migration, generated, adapter, client, module or UI paths were
  changed.
- Required acceptance contributions: A-14, A-32, A-33, A-34, A-35, A-36,
  A-59 and A-60.
- Product checkpoint: `d10bcb6bba9bc59ba80819950991b45cb05bce99`,
  `fix(execution): renew leases and close recovery gaps`.

## Contract and work

This correction addresses all four findings in the round-three review while
preserving the five approved round-two fixes. The executor still uses
observe-before-write, immutable claim generations, durable reservations,
ordered attempts and read-only reconciliation. There is no generic DAG,
duplicate outbox or upstream write enablement in this lane.

### Renewable worker leases

`Options` now exposes a bounded `LeaseRenewalInterval` with a default of one
third of `LeaseDuration`. After a successful claim and durable reservation
transaction, the executor synchronously extends the claim and then renews it
periodically while the claimed handler runs. The renewal CAS uses the exact
action ID, worker, version, previous lease and timestamp; a stale or cancelled
generation cannot extend itself. A claim-state lock covers the renewal CAS and
the in-memory fence update, while claimed journal transactions hold its read
side through commit. This prevents a transaction from constructing an old
fence between a persisted renewal and its local state update.

The handler context is bounded by the persisted action `DeadlineAt` only. Lease
expiry remains a watcher safety boundary when renewal fails, while a healthy
handler with no action deadline can run beyond one lease interval. The watcher
continues to cancel on claim generation, durable cancellation, state, worker
ownership or current lease expiry changes.

### Exact unresolved-effect accounting

Action transitions derive `UnresolvedCount` and the `unresolvedEffects` outcome
field from the journal's exact effect rows in the same transaction. Unknown,
failed and cancelled rows count in every state; pending rows count while an
action is reconciling. This removes the previous single-effect assumptions from
uncertain dispatch, active-dispatch deferral, partial read-back and
reconciliation. Safe retry and terminal read-back transitions return the count
to zero after their effect rows are updated.

### Durable dispatch-barrier release retry

When a dispatch handler returns after its claim has been recovered, the defer
path now schedules bounded retries if the exact barrier release transaction
fails. A durable outcome marker records the returned attempt separately before
effect/attempt updates, allowing a later executor to resume the exact release
without broad startup recovery. Retries are deduplicated per action and attempt,
and concurrent optimistic transaction conflicts are retried or treated as
success after read-back proves that the target attempt is no longer running.
An existing marker never fences a fresh claim that is about to reconcile the
returned attempt. Attempt identity remains mandatory, so an old worker cannot
change a later dispatch attempt.

### Cancellation during initial observation

The initial claimed `Observe` phase now receives the same action-deadline
context and durable journal watcher used by dispatch and reconciliation. It is
registered as an active handler, and a cancellation marker written through a
second executor cancels it cooperatively. When the observer returns, the
executor performs a detached read of the durable cancellation/deadline state,
records a not-dispatched cancelled observe attempt, and finalizes the action
without creating a dispatch intent or mutation.

## Verification

All checks below ran against product
`d10bcb6bba9bc59ba80819950991b45cb05bce99` and synthetic memory/SQLite
fixtures only.

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `GOWORK=off go test ./internal/execution -count=1 -timeout=180s` | Passed, exit 0 | Complete execution package |
| `GOWORK=off go test -race ./internal/execution -count=1 -timeout=180s` | Passed, exit 0 | Complete execution race suite |
| `GOWORK=off go vet ./internal/execution` | Passed, exit 0 | Execution package |
| `GOWORK=off go test ./internal/execution -run 'Test(NoDeadlineDispatchLeaseRenewsUntilHandlerReturns|UncertainDispatchPersistsExactUnresolvedCount|DurableCancelReachesInitialObserveAcrossExecutors|BarrierReleaseRetriesAfterTransientJournalFailure)$' -count=1 -timeout=120s` | Passed, exit 0 | Four required correction fixtures |
| `GOWORK=off go test ./internal/execution -run TestBarrierReleaseRetriesAfterTransientJournalFailure -count=20 -timeout=180s` | Passed, exit 0 | Repeated transient barrier/reclaim race fixture |
| `GOWORK=off go test -race ./internal/execution -run 'Test(NoDeadlineDispatchLeaseRenewsUntilHandlerReturns|UncertainDispatchPersistsExactUnresolvedCount|DurableCancelReachesInitialObserveAcrossExecutors|BarrierReleaseRetriesAfterTransientJournalFailure|ExpiredLiveDispatchBlocksRecoveredWorkerUntilReturn)$' -count=3 -timeout=240s` | Passed, exit 0 | Required and retained live-dispatch fixtures |
| `GOWORK=off go test ./... -count=1 -timeout=180s` | Passed, exit 0 | Root module, including adapters, storage, filesystem and compatibility fixtures |
| `GOWORK=off go vet ./...` and `GOWORK=off go mod verify` | Passed, exit 0 | Root module |
| `./scripts/check-guardrails.sh --ci` | Passed, exit 0 | Generation, staged generation, Vacuum 100/100 with zero warnings/errors, lint, architecture, tests, vet and all module verification |
| `./scripts/check-api.sh` | Passed, exit 0 | Deterministic bundle, generated output and Vacuum |
| `./scripts/check-lint.sh`, `python3 scripts/check-architecture.py`, `python3 scripts/check_planning.py` | Passed, exit 0 | Lint/import boundaries; planning reported 44 tasks, 60 acceptance cases and resolved local links |
| `GOWORK=off go test -mod=readonly ./...` and `go mod verify` in `ui`, `tools`, `clients/qbittorrent`, `clients/nzbget` | Passed, exit 0 | Every nested module |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed, exit 0 | Root and every nested module |
| `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed, exit 0 | Root and every nested module |
| `git diff --check` and Go formatting | Passed, exit 0 | Product tree and pre-commit hook |
| Live services, credentials, private coordinates, real media and destructive writes | Not used | Public synthetic fixtures only |

The first exploratory cross-build invocation omitted `-exec=true` and therefore
reported the expected host `exec format error` while trying to run foreign test
binaries. The corrected compile-only invocations above passed for both Linux
architectures and are the release evidence.

## Review and integration

- Reviewer identity/role and reviewed commit: pending independent review by
  `/root/x05_reviewer` against product
  `d10bcb6bba9bc59ba80819950991b45cb05bce99`.
- Findings addressed: the four findings in
  `docs/execution/handoffs/W-02-review-round3.md` have focused regression
  coverage for no-deadline renewal, exact multi-target counts, failed barrier
  release retry and cancellation during initial observation. The five
  round-two corrections remain covered by the retained suite.
- Fix commit and regression evidence: product commit above; no later product
  delta is included under this handoff.
- Final reviewer decision: pending.
- Integrated commit, recorded by coordinator: pending.

## Resume checkpoint

- Current state: product is committed and all assigned focused, race, vet,
  module, cross-build, generation/API/Vacuum, lint, architecture, planning and
  guardrail checks pass. G-01 native Arr write evidence remains open; this lane
  does not enable Arr, qBittorrent, NZBGet, Jellyfin, Seerr or filesystem
  writes.
- Next safe action: coordinator records the exact product and handoff SHAs in
  `docs/execution/state.json`, then requests independent review against the
  product checkpoint.
- Outstanding uncertainty: lease renewal depends on a live journal renewal
  capability and keeps the watcher fail-closed when the renewal CAS cannot be
  proven. Barrier retry is bounded per in-process worker and leaves a durable
  returned-attempt marker for later reconciliation; external side effects are
  never claimed exactly once.
- Blocker and evidence needed: independent review of the renewal/fence race,
  multi-target unresolved accounting, durable barrier marker/retry and initial
  observe cancellation. G-01 still requires evidence-backed native Arr write
  capabilities before any such runtime lane is enabled.
- No conflicting writes or unknown files removed: confirmed; only the assigned
  execution product and this handoff were changed.

Coordinator updates state.json; this worker does not declare integration.
