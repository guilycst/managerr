# W-02 correction round two handoff

## Assignment

- Task ID and title: W-02 correction round two, durable action executor and cancellation.
- Owner and independent reviewer: `/root/x05_implementer`; `/root/x05_reviewer`.
- Base product: `f56908a19b775dc5218d485a61ffcf0faebb5ace`.
- Prior review receipt: `6e5c8c6d8d882dc5cdf3237fc47e7d571ba69136`.
- Shared checkout: `main`. The coordinator owns `docs/execution/state.json` and integration.
- Owned paths: `internal/execution/` and this handoff only. No state, task, storage,
  migration, generated, adapter, client, module or UI paths were changed.
- Required acceptance contributions: A-14, A-32, A-33, A-34, A-35, A-36, A-59 and A-60.
- Product checkpoint: `04c2e245a9624287417522c9f876056090016b83`,
  `fix(execution): close recovery and effect uncertainty gaps`.

## Contract and work

The correction closes the five findings in the round-two review while preserving
observe-before-write, immutable claim fencing, durable reservations and
read-only recovery. There is still no generic DAG, duplicate outbox or upstream
write enablement in this lane.

### Live dispatch recovery barrier

`processReconciliation` first looks for the highest numbered dispatch attempt
that is still `running`. A worker recovered by lease expiry leaves that attempt
as a durable barrier and returns the action to `reconciling` with a persisted
retry time; it does not call `Reconcile` or `Dispatch` while the old generation
is externally active. `handlerContext` is bounded by both the persisted action
deadline and the claimed lease, and a journal watcher cooperatively cancels the
handler when the action leaves its running generation.

When the old handler actually returns, `releaseDispatchBarrier` uses the exact
dispatch attempt ID in a detached bounded transaction. It changes that attempt
to uncertain reconciliation and moves every effect belonging to it to
`unknown`. An old worker cannot alter a later attempt. A startup `Recover` still
converts abandoned running attempts to read-only reconciliation; `RunOnce`
lease recovery remains fail-closed behind the live-handler barrier until the
earlier handler returns.

### Durable cancellation across executors

Every claimed dispatch and reconciliation handler has a bounded journal watcher.
It observes the action version, claim generation, cancellation marker, state
and lease from the shared journal, then cancels the handler context on any
ownership or durable cancellation change. The watcher is configurable for
deterministic fixtures and defaults to a bounded 100 ms poll. `Cancel` still
persists its marker transactionally; post-cancellation journal finalization is
detached from the cancelled handler context, so accepted late effects remain
visible.

### Complete and non-contradictory effect evidence

`ObserveNeedsAction` now requires at least one exact effect. Terminal observe and
reconciliation evidence rejects failed, cancelled and unknown per-target states;
safe-to-retry evidence accepts only pending targets; terminal reconciliation
requires a complete target set. Dispatch read-back continues to require exact
target, effect-kind and ordinal identity for every approved target. Invalid or
contradictory evidence enters review/uncertainty and cannot produce a
`succeeded/applied` action.

An uncertain dispatch with no handler report now clones every already-journaled
target into the uncertain attempt and persists `unknown` for each one. No
applied result is synthesized and no target remains misleadingly `pending`.

### Idle cancellation and reservations

When unclaimed reconciliation reaches terminal cancellation, deadline or a
proven safe retry, the executor releases matching process-local reservation
entries. It retains those entries while the action remains reconciling or needs
review. Durable reservation metadata remains authoritative until the terminal
transaction clears it, so a second executor cannot overlap an unresolved action
while a local map is being released.

## Verification

All checks below ran against product `04c2e245a9624287417522c9f876056090016b83`
and synthetic memory/SQLite fixtures only.

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `GOWORK=off go test ./internal/execution -count=1 -timeout=120s` | Passed, exit 0 | `internal/execution/execution_test.go` |
| `GOWORK=off go test -race ./internal/execution -count=1 -timeout=180s` | Passed, exit 0 | execution race suite |
| `GOWORK=off go vet ./internal/execution` | Passed, exit 0 | execution package |
| `TestExpiredLiveDispatchBlocksRecoveredWorkerUntilReturn` | Passed | SQL lease-expiry fixture; fresh worker made zero reconcile/dispatch calls while old handler was blocked, then reconciled only after return; effects became unknown at the barrier release |
| `TestDurableCancelReachesHandlerAcrossExecutors` | Passed | shared in-memory journal and separate owner/canceller executors |
| `TestNeedsActionRequiresEffectEvidence`, `TestReadBackRejectsPartialAndChangedEffectSets`, `TestTerminalReadBackRejectsFailedEffectEvidence`, `TestReconciliationRejectsFailedTerminalEffect` | Passed | empty, omitted, changed and failed evidence stays unresolved/reviewed |
| `TestUncertainDispatchMarksEveryPlannedEffectUnknown` | Passed | two planned effects both persisted as unknown after lost dispatch response |
| `TestIdleCancellationReleasesLocalReservation` | Passed | terminal idle cancellation allows overlapping action after local release |
| `TestRestartRecoveryNeverBlindlyDispatchesRunningAttempt`, `TestSQLJournalRecoveryPersistsAcrossReopen` | Passed | prior running-attempt recovery still uses read-only reconciliation with complete effects |
| `GOWORK=off go test -mod=readonly ./... -count=1 -timeout=180s` | Passed, exit 0 | root module |
| `GOWORK=off go test -race -mod=readonly ./internal/execution -count=1 -timeout=180s` | Passed, exit 0 | root execution race check |
| `GOWORK=off go vet -mod=readonly ./...` and `GOWORK=off go mod verify` | Passed, exit 0 | root module |
| `GOWORK=off go test/go vet/go mod verify` in `ui`, `tools`, `clients/qbittorrent`, `clients/nzbget` | Passed, exit 0 | every nested module; qBittorrent race suite also passed |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed, exit 0 | root and every nested module |
| `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed, exit 0 | root and every nested module |
| `./scripts/check-guardrails.sh --ci` | Passed, exit 0 | generation, staged generation, Vacuum 100/100 with zero warnings/errors, architecture, lint, tests, vet and module verification |
| `./scripts/check-api.sh` | Passed, exit 0 | deterministic bundle, generation and Vacuum |
| `./scripts/check-lint.sh`, `python3 scripts/check-architecture.py`, `python3 scripts/check_planning.py`, `git diff --check` | Passed, exit 0 | lint/import boundaries, planning links, and diff hygiene |
| Live services, credentials, private coordinates, real media and destructive writes | Not used | public synthetic fixtures only |

## Review and integration

- Reviewer identity/role and reviewed commit: pending independent review by
  `/root/x05_reviewer` against product `04c2e245a9624287417522c9f876056090016b83`.
- Findings addressed: all five P1/P2 findings from
  `6e5c8c6d8d882dc5cdf3237fc47e7d571ba69136` have focused regression coverage
  and bounded implementation changes. Independent disposition is pending.
- Fix commit and regression evidence: product commit above; no later product
  delta is included under this handoff.
- Final reviewer decision: pending.
- Integrated commit, recorded by coordinator: pending.

## Resume checkpoint

- Current state: product is committed and all assigned focused, race, vet,
  module, cross-build, generation/API/Vacuum, lint, architecture, planning and
  guardrail checks pass. G-01 native Arr write evidence remains open; this lane
  does not enable Arr, qBittorrent, NZBGet, Jellyfin, Seerr or filesystem writes.
- Next safe action: coordinator records the exact product and handoff SHAs in
  `docs/execution/state.json`, then requests independent review against the
  product checkpoint.
- Outstanding uncertainty: startup recovery assumes `Recover` is invoked after
  a process restart; lease-expiry polling deliberately remains fail-closed
  while a prior live handler has not returned. External side effects are never
  claimed exactly once.
- Blocker and evidence needed: independent review of the SQL live-dispatch
  barrier, cross-executor watcher, strict effect evidence and reservation release.
- No conflicting writes or unknown files removed: confirmed; only the assigned
  execution product and this handoff were changed.

Coordinator updates `state.json`; this worker does not declare integration.
