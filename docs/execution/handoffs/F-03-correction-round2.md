# F-03 scanning correction handoff, round two

## Assignment

- Task ID and title: F-03 correction round two, durable scan scheduler and coverage.
- Owner: `/root/d02_implementer`.
- Independent reviewer: `/root/x05_reviewer`.
- Exact dispatch context: reviewed product `ebdf5112c5be852fc992f337d47e4e0589f52b94`; prior review receipt `884a505a4820d475c16a1e034fd8680367ebf9d7`; coordinator dispatch checkpoint `6094d583312ac7f8796d0e4dd539490c8196d227`.
- Shared-checkout ownership: shared `main`; coordinator owns integration and `docs/execution/state.json`.
- Owned paths: `internal/scanning/` for product changes and this handoff only. F-05, state, task, module, script, adapter, UI and generated paths were preserved.
- Acceptance contributions: A-02, A-03 and A-53.

## Findings closed

This correction closes all three findings in review receipt
`884a505a4820d475c16a1e034fd8680367ebf9d7`.

- Root schedules now require a non-empty, trimmed, bounded configuration
  revision and reject whitespace-only, control-character and oversized values
  at `ConfigureRoots` and `RegisterRoot` boundaries. Scan records validate
  non-empty revisions when present; empty values are retained only for legacy
  history so startup recovery can terminalize a legacy non-terminal scan,
  invalidate its projections and queue a current-revision scan before it can
  run or publish evidence. New scans always inherit the validated root
  revision.
- A queued replacement cannot start while any runner still owns the same
  root. The old revision is durably terminalized and its context is cancelled,
  but per-root runtime ownership remains in `scheduler.running` until the
  runner exits and its finish path removes the entry. This holds even when
  `MaxConcurrent > 1`; runnable scans for unrelated roots can still consume
  remaining global capacity.
- Coalesced follow-up state now persists its trigger provenance. Scheduled
  ticks create `TriggerScheduled` follow-ups, manual participation has explicit
  precedence and creates `TriggerManual`, and legacy pending bits are upgraded
  to the historical manual meaning at startup. Follow-up metadata is cleared
  together with the pending bit and validated as a pair.

The product remains read-only and storage-neutral. No upstream calls,
filesystem mutation, credentials, private coordinates, live media data,
registration, import or destructive action was used; all fixtures are
synthetic.

## Product

- Product tip: `b65f5d65d67e6fc823fbf54fa8ab4f2a4d17d191`.
- Atomic product commits: `b78a30b6186f025f6dc59fb7911cd7adebecb0ea`
  (`fix(scanning): fence revisions and follow-up runners`) and
  `b65f5d65d67e6fc823fbf54fa8ab4f2a4d17d191`
  (`test(scanning): cover legacy revision recovery`).
- Product files: `internal/scanning/scanning.go` and
  `internal/scanning/scanning_test.go` only.
- Deterministic regressions cover empty/non-canonical revisions, legacy
  revision recovery, same-root replacement with `MaxConcurrent=2` and a
  non-cooperative old runner, scheduled-only follow-up attribution, explicit
  manual precedence, overdue coalescing, deadline retention and prior
  revision/coverage behavior.

## Verification

| Command or scenario | Result / exit status | Evidence |
| --- | --- | --- |
| `gofmt -w internal/scanning/scanning.go internal/scanning/scanning_test.go` and `git diff --check` | passed, exit 0 | Scoped product files |
| `GOWORK=off go test -count=20 -timeout=120s ./internal/scanning` | passed, exit 0 | Focused deterministic suite |
| `GOWORK=off go test -race -mod=readonly -count=5 -timeout=120s ./internal/scanning` | passed, exit 0 | Focused race suite |
| `GOWORK=off go vet -mod=readonly ./internal/scanning` | passed, exit 0 | Focused vet |
| `GOWORK=off go test -mod=readonly -count=1 -timeout=180s ./internal/...` | passed, exit 0 | Full root internal package matrix |
| `GOWORK=off go vet -mod=readonly ./internal/...` | passed, exit 0 | Full root vet |
| `GOWORK=off go mod verify` | passed; `all modules verified` | Root, UI, tools and client module verification |
| `./scripts/check-api.sh` | passed; generation checks and Vacuum 100/100 with zero warnings/errors | Bundled OpenAPI and generated outputs |
| `./scripts/check-lint.sh` | passed; all lint and import-boundary checks reported zero issues | Root/UI/tools/client lint and architecture |
| `python3 scripts/check_planning.py --self-test && python3 scripts/check_planning.py` | passed; 44 tasks, 60 acceptance cases and links resolve | Planning ledger |
| `./scripts/check-guardrails.sh --ci` | passed, exit 0 | Generation, staged generation, API/Vacuum, architecture, lint, tests, vet and module verification |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=readonly -count=1 -run '^$' -exec=true ./internal/...` | passed, exit 0 | Linux amd64 compile-only matrix |
| `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go test -mod=readonly -count=1 -run '^$' -exec=true ./internal/...` | passed, exit 0 | Linux arm64 compile-only matrix |
| Product pre-commit hook | passed, exit 0 | Fast deterministic guardrails ran during product commit |
| `GOWORK=off go test -race -mod=readonly -count=1 -timeout=180s ./internal/...` | failed, exit 1 after 182.154s: existing `internal/storage` `TestMigrationsUpgradeFromVersionOne` hit `panic: test timed out after 3m0s`; all packages through `internal/scanning` passed | Unrelated golang-migrate storage migration fixture; no scanning race failure |
| Live media services, private coordinates, credentials, upstream writes or destructive filesystem actions | intentionally not run | Repository policy |

## Acceptance disposition

- A-02: the correction preserves partial/unknown evidence rules and prevents
  empty or stale revision bindings from asserting current absence.
- A-03: revision replacement is fenced per root through runner exit, startup
  recovery rejects legacy stale work before dispatch, and scheduled/manual
  follow-ups retain bounded provenance. Production SQLite mapping and runner
  composition remain downstream integration work.
- A-53: global concurrency remains bounded while same-root replacement cannot
  overlap; the full 100k observation/10k catalog capacity measurement remains
  a later discovery/integration verification.

## Review and integration

- Product tip: `b65f5d65d67e6fc823fbf54fa8ab4f2a4d17d191`.
- Review source: `ebdf5112c5be852fc992f337d47e4e0589f52b94`; prior receipt:
  `884a505a4820d475c16a1e034fd8680367ebf9d7`.
- Handoff commit: pending; this file is intentionally committed separately
  from the product.
- Final reviewer decision and coordinator state update: pending; this lane
  did not edit or stage `docs/execution/state.json`.

## Resume checkpoint

- Product is complete at
  `b65f5d65d67e6fc823fbf54fa8ab4f2a4d17d191`; owned product paths are clean.
- Next safe action: independently review the exact product SHA, rerun the
  three new admission/provenance regressions plus the prior F-03 suite, and
  record this handoff SHA in the coordinator ledger.
- The full root race gate remains blocked by the unrelated storage migration
  timeout above. No scoped F-03 blocker remains.
- No conflicting files were removed, reset, rebased, force-pushed or staged.
