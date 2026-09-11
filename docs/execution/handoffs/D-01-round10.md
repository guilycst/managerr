# D-01 implementation handoff, correction round ten

## Decision

`pending_independent_review`.

This handoff records the two bounded fixes requested by the independent
round-nine storage review. `/root/f01_reviewer` should review the product
commit below independently and write its receipt separately.

## Assignment and boundary

- Task: D-01, SQLite schema, migrations and sqlc repository.
- Review input: round-nine receipt at
  `docs/execution/handoffs/D-01-review-round9.md`, receipt
  `5f58a72d6ab4484dc1f4851236152bbae3e7a4ed`.
- Review baseline product:
  `eb2f8076f5aef8bfb33cb58f81ada53b7759a189`.
- Shared checkout baseline when this correction started:
  `2e30201f197afcfbd6998116180a71b7af15372a`.
- Implementer: `/root/f01_implementer`; reviewer: `/root/f01_reviewer`.
- Product commit:
  `a929c71`, `fix(storage): recover initial terminal purge crash`.
- Owned product paths: `internal/storage/`, `migrations/`, `sqlc.yaml`, and
  generated SQLC output under `internal/storage/sqlc/`.
- Handoff path: `docs/execution/handoffs/D-01-round10.md`.
- `docs/execution/state.json`, Arr/Jellyfin adapter files, and other lane
  files were not staged or modified by this correction. An unrelated
  untracked `internal/adapters/jellyfin/` directory remains in the shared
  checkout.

## Changes

`FinalizeApprovedPurgeAction` now accepts both supported action generations:
the normal `reconciling` generation and an initial-dispatch `running`
generation whose exact janitor and trash-entry transaction has already reached
terminal state. The existing exact CAS still requires the action ID and
version, approval-bound janitor ID, matching ready plan and revision digest,
approving decision, immutable `fs.delete` target, nonempty matching manifests,
terminal janitor generation, terminal trash-entry generation and an inactive
entry operation. Generic startup recovery, lease recovery, due listing and
generic claiming remain fenced while an approval-bound janitor is terminal.
The finalizer clears the action lease and increments the action generation once;
repeated or stale calls return no row and never dispatch another purge.

Terminal success now preserves the janitor's verified outcome. A janitor
outcome of `applied` remains `applied`, including its effect evidence. An
`already_satisfied` outcome is accepted only when both supported deleted-object
fields are absent or numeric zero; positive, malformed, contradictory or
missing success evidence becomes action `needs_review` with outcome
`unknown`. The finalizer no longer labels an unproven terminal success as
`already_satisfied`. Failed, cancelled and held terminal journals retain their
existing action-state mappings and safe reason outcomes.

No schema or migration change was needed. SQLC output was regenerated from the
updated query; `migrations/` and `sqlc.yaml` remain unchanged.

## Regression coverage

`internal/storage/compatibility_round10_test.go` adds synthetic SQLite coverage
for an initial-dispatch crash after the janitor and trash entry commit but
before action outcome finalization. It covers terminal janitor states
`succeeded`, `failed`, `held` and `cancelled`, preserves `applied` and
`already_satisfied` outcomes, rejects incomplete and contradictory success,
checks the generic recovery/list/claim fence, verifies the exact entry/action
generation, and proves repeated finalization is stale and side-effect free.

`internal/storage/compatibility_round9_test.go` now uses valid terminal outcome
fixtures so the prior terminal-finalization matrix exercises the same outcome
contract.

No live media service, upstream write, filesystem mutation, credential,
private inventory or deployment was used.

## Verification

All commands below passed against product commit `a929c71` or its staged
equivalent:

| Command or scenario | Result |
| --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage/...` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/storage/...` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/storage/...` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed all root packages. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed all root packages. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `cd tools && GOWORK=off go tool github.com/sqlc-dev/sqlc/cmd/sqlc generate -f ../sqlc.yaml` | Passed; generated SQLC is clean. |
| `./scripts/generate.sh --check` | Passed. |
| `./scripts/check-api.sh` | Passed; Vacuum quality 100/100 and API checks passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases and local links resolve. |
| `cd tools && GOWORK=off go test -mod=readonly -count=1 ./...` | Passed. |
| `cd tools && GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `cd tools && GOWORK=off go mod verify && GOWORK=off go mod tidy -diff` | Passed; no tidy drift. |
| `cd ui && GOWORK=off go test -mod=readonly -count=1 ./...` | Passed. |
| `cd ui && GOWORK=off go vet -mod=readonly ./... && GOWORK=off go mod verify` | Passed. |
| `GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed host compile-only check. |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| Product commit hook | Passed fast generation, API, architecture and test guardrails. |
| `git diff --check` | Passed before product commit. |

## Acceptance contribution

- A-32: exact restart-safe finalization now covers the initial-dispatch
  terminal crash window without blind purge replay.
- A-36: terminal approval-bound action fencing remains coupled to exact CAS
  finalization, including the action's initial `running` generation.
- A-45: storage regressions, full root tests and race, vet, SQLC generation,
  guardrails, planning, architecture, module checks and Linux amd64/arm64
  compile checks pass.

Independent review should continue to inspect cancellation/deadline races,
concurrent finalizer CAS behavior, stale plan/entry generations, malformed
terminal evidence and the distinction between applied, already-satisfied and
unknown outcomes.

## Handoff

- Product commit: `a929c71`.
- Documentation commit: pending this handoff commit.
- Independent review: `/root/f01_reviewer` against product commit `a929c71`.
- `docs/execution/state.json` remains coordinator-owned and was not edited.
- Resume from the product commit, then apply this handoff documentation
  commit. Preserve unrelated shared-checkout changes.
