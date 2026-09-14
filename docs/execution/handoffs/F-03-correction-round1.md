# F-03 correction round one handoff

## Assignment

- Task ID and title: F-03 correction round one, durable scan scheduler and coverage.
- Owner: `/root/d02_implementer`.
- Independent reviewer: `/root/d02_reviewer`.
- Exact dispatch checkpoint: `29d17b282b35174278699c2f93c877e0a0c26265`.
- Review receipt addressed: `f73e7fd38eb78672a015c8d5a1186019038b0082`.
- Shared-checkout ownership: shared `main`; coordinator owns integration and `docs/execution/state.json`.
- Owned product paths: `internal/scanning/`.
- Owned handoff path: this file only. State, F-05, W-02 and all other paths remain coordinator or peer owned.

## Findings closed

The correction addresses both P1 findings in the review receipt.

- Scheduled due events now join the same `FollowUpPending` bit used by manual
  triggers while a root has queued, running or waiting work. A due or overdue
  interval is consumed and advanced beyond the current clock, so repeated ticks
  cannot create a third scan. An accepted follow-up survives a runner deadline;
  explicit cancellation retains its existing follow-up cancellation semantics.
- Every progress, dispatch, completion and restart recovery path compares the
  scan `ConfigRevision` with the current root revision. A mismatch is
  terminalized as `cancelled` with bounded `stale_config_revision` metadata,
  its cursor is never resumed or promoted, and one fresh current-revision scan
  is queued. Live reconfiguration cancels the old runner context after the
  durable replacement is published. Current and last-complete root evidence is
  invalidated on revision change and stale terminal results cannot repopulate it.
  Restart also drops stale persisted root evidence before scheduling.

## Product

- Product commit: `ebdf5112c5be852fc992f337d47e4e0589f52b94`
  (`fix(scanning): fence follow-ups by schedule revision`).
- Added deterministic synthetic coverage for mixed scheduled/manual overdue
  triggers, deadline follow-up retention, live revision replacement, restart
  revision mismatch and old-cursor rejection. Existing same-revision recovery,
  pagination, cancellation, coverage and deep-copy tests remain green.
- No upstream calls, filesystem mutation, credentials, private coordinates or
  live media data were used.

## Verification

| Command or scenario | Product / fixture | Result | Evidence |
| --- | --- | --- | --- |
| `GOWORK=off go test -race -count=3 -timeout=120s ./internal/scanning` | `ebdf511`; synthetic scheduler fixtures | Passed, exit 0 | `internal/scanning/scanning_test.go` |
| `GOWORK=off go vet ./internal/scanning` | `ebdf511` | Passed, exit 0 | scanning package |
| `GOWORK=off go test -count=1 -timeout=120s ./internal/...` | `ebdf511` plus shared tree | Passed, exit 0 | root internal package matrix |
| `GOWORK=off go vet ./internal/...` | `ebdf511` plus shared tree | Passed, exit 0 | root internal package matrix |
| `GOWORK=off go mod verify` | `ebdf511` | Passed: `all modules verified` | root, UI, tools and client modules |
| `./scripts/check-api.sh` | `ebdf511` | Passed; generation checks and Vacuum 100/100 with zero warnings/errors | bundled API and generated output |
| `./scripts/check-lint.sh` | `ebdf511` plus shared tree | Passed; all lint and import-boundary checks reported zero issues | root/UI/tools/client checks |
| `python3 scripts/check-architecture.py` | `ebdf511` | Passed: architecture import boundaries | architecture checker |
| `python3 scripts/check_planning.py --self-test && python3 scripts/check_planning.py` | `ebdf511` | Passed: 44 tasks, 60 acceptance cases and links resolve | planning checker |
| `./scripts/check-guardrails.sh --ci` | `ebdf511` plus shared tree | Passed; API/Vacuum, generation, lint, architecture, tests, vet and module verification | CI-equivalent guardrail runner |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=readonly -count=1 -run '^$' -exec=true ./internal/...` | `ebdf511` | Passed, exit 0 | Linux amd64 compile-only matrix |
| `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go test -mod=readonly -count=1 -run '^$' -exec=true ./internal/...` | `ebdf511` | Passed, exit 0 | Linux arm64 compile-only matrix |
| `GOWORK=off go test -race -count=1 -timeout=180s ./internal/...` | `ebdf511` plus shared tree | Failed, exit 1 after 182.414s: existing `internal/storage` `TestMigrationsUpgradeFromVersionTwo` hit `panic: test timed out after 3m0s`; all packages through `internal/scanning` passed | unrelated storage migration fixture; scoped scanning race above passed |
| Mixed scheduled/manual overdue trigger | `ebdf511`; fake clock and two blocking runners | Passed: one active scan plus one follow-up, no third scan after repeated overdue ticks | `TestSchedulerCoalescesScheduledAndManualFollowUp` |
| Pending follow-up at runner deadline | `ebdf511`; deadline-aware synthetic runner | Passed: deadline terminal state retains and activates exactly one follow-up | `TestSchedulerRetainsFollowUpAfterDeadline` |
| Live root revision replacement | `ebdf511`; blocked rev-1 runner, rev-2 replacement | Passed: old run terminalized/cancelled, stale context cancelled, rev-2 scan receives empty cursor and current revision | `TestSchedulerStalesRunningScanWhenRevisionChanges` |
| Restart revision mismatch | `ebdf511`; persisted running rev-1 cursor under rev-2 root | Passed: old record is stale terminal history; fresh scan receives rev-2 and no old cursor/count | `TestSchedulerRecoveryRejectsMismatchedRevisionCursor` |

## Acceptance disposition

- A-02: correction preserves partial/unknown source evidence and now prevents
  obsolete revision evidence from remaining current after reconfiguration or
  restart.
- A-03: mixed scheduled/manual admission, overdue interval coalescing,
  deadline follow-up retention and revision-safe restart are covered. Full
  production runner and SQLite persistence remain downstream integration work.
- A-53: bounded scheduler concurrency and detached durable state remain covered;
  the full 100k-observation/10k-catalog capacity measurement remains later
  discovery/integration verification.

## Review and integration

- Reviewer decision for this correction: pending `/root/d02_reviewer`.
- Findings, review SHA and integration state: coordinator records after review;
  this lane does not edit `docs/execution/state.json`.

## Resume checkpoint

- Product correction is committed at `ebdf5112c5be852fc992f337d47e4e0589f52b94`.
- This handoff is ready for its separate docs commit and independent review.
- Shared checkout contains unrelated F-05 organize edits; no such path was
  staged or modified by this lane.
- Full root race gate remains blocked by the unrelated storage timeout recorded
  above. No scoped F-03 blocker remains.
