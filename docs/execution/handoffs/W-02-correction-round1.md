# W-02 correction round one handoff

## Assignment

- Task ID: W-02 correction round one, durable action executor and cancellation.
- Owner: `/root/x05_implementer`; independent reviewer: `/root/x05_reviewer`.
- Base product: `a4b152e20f8f82d53ba6d3c901737804357cc807`.
- Prior review receipt: `e65de463ef8258aac5bf02a2a97e7a5e3a36ac34`.
- Product checkpoint: `8a5574ddb0154b50812dca2ad632acd343e803a6`.
- Shared checkout: `main`; coordinator owns `docs/execution/state.json` and
  integration. This lane changed only `internal/execution/` and this handoff.

## Correction evidence

The executor now requires transactional, fenced, and durable-reservation
journal capabilities at construction. A claimed worker captures the action
ID, claim version, worker identity, lease, and claim time. Every attempt,
effect, reservation, and action transition after claim runs through the
fenced transaction; SQL acquires the SQLite writer lock and checks the exact
running generation before the callback. A stale worker therefore receives
`ErrLeaseLost` after lease recovery and cannot adopt the recovered row.

Reservation keys are canonicalized, include parent/descendant path overlap,
and are persisted in the action outcome while the action is running,
reconciling, or awaiting review. SQL and memory journals inspect those keys
atomically across workers and process restart. Terminal success, safe retry,
dependency wait, and explicit terminal cancellation clear them.

Effect evidence is identity-bound by target kind, target ID, effect kind, and
ordinal. Duplicate identities, changed identities, extra targets, and omitted
targets are rejected. A dispatch result is only evidence; a fresh read-back
must account for every approved effect before applied or already-satisfied
success. Lost dispatch evidence leaves existing effects unresolved instead of
synthesizing a state from an empty report.

`Cancel` persists its durable marker in a bounded transaction and cancels the
active handler context. Action deadlines also create handler contexts with
the persisted deadline. Journal writes after handler cancellation use a
bounded context detached from caller cancellation, so late accepted effects
and uncertainty remain durable. The handler context is never used for journal
finalization.

The in-memory expired-lease fixture now distinguishes an active lease from an
expired claimed lease. The SQL fixture exercises recovery while a stale worker
is blocked in dispatch. Rollback injection proves a failed dispatch-intent
transaction leaves no dispatch attempt or effect rows and makes no handler
call.

## Verification

| Check | Result | Evidence |
| --- | --- | --- |
| `GOWORK=off go test ./internal/execution -count=1 -timeout=90s` | Passed | Product checkpoint `8a5574d`; synthetic memory and SQLite fixtures |
| `GOWORK=off go test ./internal/execution -race -count=1 -timeout=120s` | Passed | Concurrent cancellation, reservations, and SQL stale-worker fixture |
| `GOWORK=off go vet ./internal/execution` | Passed | Product checkpoint `8a5574d` |
| SQL stale lease recovery | Passed | `TestSQLStaleWorkerCannotFinalizeRecoveredLease` |
| SQL partial read-back and reconciliation identity | Passed | `TestSQLReadBackRejectsPartialEffectSet`, `TestSQLReconciliationRejectsChangedEffectIdentity` |
| durable cancellation context | Passed | `TestDurableCancelCancelsInFlightHandler` |
| reservation restart boundary | Passed | `TestUnresolvedReservationPersistsAcrossExecutorRestart` |
| expired-versus-active memory lease | Passed | `TestMemoryRecoverExpiredOnlyReclaimsExpiredLeases` |
| transaction capability and rollback | Passed | `TestNewRejectsNonTransactionalJournal`, `TestDispatchIntentTransactionRollsBackBeforeHandler` |
| `git diff --check` | Passed | Product and handoff trees |
| fast pre-commit guardrail | Passed | generation, API/Vacuum, architecture, format, targeted tests |
| live services, credentials, private coordinates, and media | Not used | Public synthetic fixtures only |

## Scope and blockers

No Arr, Jellyfin, qBittorrent, NZBGet, Seerr, filesystem, UI, API, storage,
migration, state, or module changes were made. G-01 native Arr import evidence
remains open outside this lane; this correction does not enable any upstream
write. The downstream workflow and approval transaction lanes remain separate.

## Resume

- Independent review should use product checkpoint
  `8a5574ddb0154b50812dca2ad632acd343e803a6` and this handoff commit.
- Coordinator records both exact SHAs in `docs/execution/state.json`.
- Do not integrate claimed workflow handlers until the independent review
  confirms stale-generation fencing, reservation persistence, strict effect
  identity, cooperative cancellation, transaction construction, and lease
  recovery evidence.
