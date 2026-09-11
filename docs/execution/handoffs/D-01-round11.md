# D-01 implementation handoff, correction round eleven

## Decision

`pending_independent_review`.

This handoff records the two bounded fixes requested by the independent
round-ten storage review. `/root/f01_reviewer` should review the coordinator
product commit below independently and write its receipt separately.

## Assignment and boundary

- Task: D-01, SQLite schema, migrations and sqlc repository.
- Review input: round-ten receipt at
  `docs/execution/handoffs/D-01-review-round10.md`, receipt
  `5f3632a397b1a858a8493c7528c9a80cc94164a6`.
- Review baseline product:
  `a929c71126dbfcad24f1fcebdcf0eb9e920bf79c`.
- Previous handoff:
  `docs/execution/handoffs/D-01-round10.md`, receipt
  `42758d4ec54854d5df9cbc60be5b4fe421c0e17c`.
- Shared checkout baseline when this correction was queued:
  `020e8bf` (the full ancestor is recorded in repository history).
- Implementer: `/root/f01_implementer`; reviewer: `/root/f01_reviewer`.
- Product commit containing this correction:
  `ce6cc272deeb7bf93c10d4bb7a6d8f93f03f4972`,
  `docs: queue X-03 correction round six`.
- Owned product paths: `internal/storage/`, `migrations/`, `sqlc.yaml`, and
  generated SQLC output under `internal/storage/sqlc/`.
- Handoff path: `docs/execution/handoffs/D-01-round11.md`.
- `docs/execution/state.json` was changed by the coordinator in the product
  commit and was not edited by this lane. Arr, Jellyfin, Seerr, and catalog
  files were not staged by this handoff.

## Changes

`FinalizeApprovedPurgeAction` now handles the exact initial-dispatch terminal
cancellation window. The normal terminal transaction has
`janitor.version = action.version + 1`; when cancellation is requested after
that janitor and trash-entry transaction has committed, the action advances to
the same generation as those already-terminal records. The equal-generation
branch is admitted only with the durable cancellation marker and retains the
existing exact plan, revision, decision, target, manifest, entry, terminal
state, and inactive-operation predicates. The finalizer advances and clears
the action lease once; stale or repeated finalization returns no row.

`RequestActionCancellation` is now a one-shot idempotent generation change.
The first request records the cancellation timestamp and advances the action;
later requests return the unchanged cancellation generation and timestamp.
This prevents a retry from widening the terminal-generation window.

Terminal janitor success is now classified from strict, unambiguous evidence.
The parser requires a valid JSON object with exactly one recognized `outcome`
member. `applied` requires a positive integral `deletedObjects` or
`deleted_objects` count, or equal positive integral values in both aliases, or
a durable matching `fs.delete` action effect in state `applied`. The aliases
must have matching values when both are present. `already_satisfied` permits
absent counts or integral zero counts, with no positive matching delete effect.
Malformed JSON, duplicate recognized keys, duplicate aliases, conflicting
aliases, wrong JSON types, zero or negative applied counts, positive
already-satisfied counts, missing outcome, and unsupported evidence become
`needs_review` with an `unknown` outcome. Valid raw evidence remains preserved.

No schema or migration change was needed. SQLC was regenerated from the
updated query; `migrations/` and `sqlc.yaml` remain unchanged.

## Regression coverage

`internal/storage/compatibility_round11_test.go` adds synthetic SQLite tests
for:

- terminal janitor states `succeeded` with `applied` and
  `already_satisfied`, `failed`, `held`, and `cancelled` after cancellation;
- exact action, janitor, and trash-entry generations, lease clearing, and
  repeat-finalizer idempotence;
- repeat cancellation preserving the first timestamp and generation;
- positive camel-case, snake-case, matching-alias, and durable-effect proof;
- absent and zero already-satisfied evidence;
- zero, negative, fractional, string, null, boolean, conflicting, duplicate,
  missing, and malformed terminal evidence;
- preservation of raw valid outcomes and conversion of unproven success to
  `needs_review`/`unknown`.

No live media service, upstream write, filesystem mutation, credential,
private inventory, or deployment was used.

## Verification

The following checks were run against the round-eleven product before and
after it landed in `ce6cc272deeb7bf93c10d4bb7a6d8f93f03f4972`:

| Command or scenario | Result |
| --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage -run 'Round11' -v` | Passed, including terminal-cancellation and strict-evidence matrices. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage/...` | Passed; current scoped recheck passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=300s ./internal/storage/...` | Passed in 167.893s. |
| `GOWORK=off go vet -mod=readonly ./internal/storage/...` | Passed; current scoped recheck passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed in the round-eleven validation run. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Storage passed; the root run was red only in the unowned Seerr test `internal/adapters/seerr/client_test.go:397` (`TestSeerrEmptyMalformedAndPaginationBounds`). |
| `cd tools && GOWORK=off go tool github.com/sqlc-dev/sqlc/cmd/sqlc generate -f ../sqlc.yaml` | Passed; generated output is clean and the current scoped regeneration produced no diff. |
| `./scripts/generate.sh --check` | Passed. |
| `./scripts/check-api.sh` | Passed; Vacuum quality 100/100. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases and local links resolve. |
| `cd tools && GOWORK=off go test -mod=readonly -count=1 ./...` | Passed. |
| `cd tools && GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `cd tools && GOWORK=off go mod verify && GOWORK=off go mod tidy -diff` | Passed. |
| `cd ui && GOWORK=off go test -mod=readonly -count=1 ./...` | Passed. |
| `cd ui && GOWORK=off go vet -mod=readonly ./... && GOWORK=off go mod verify` | Passed. |
| `GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed host compile-only check. |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| `git diff --check` | Passed before the product commit and after scoped regeneration. |
| Handoff commit hook | Generation, API, and architecture checks passed; the hook then stopped on pre-existing unowned formatting in `internal/adapters/jellyfin/read/client.go` and `internal/adapters/seerr/client.go`. Those files were left untouched; the handoff commit uses the already-completed scoped checks. |

The root race failure is outside this lane and was not modified here. The
untracked shared-checkout directories `internal/adapters/jellyfin/`,
`internal/adapters/seerr/`, and `tests/fixtures/catalogs/` remain preserved.

## Acceptance contribution

- A-32: exact restart-safe finalization now closes the cancellation-after-
  terminal-janitor crash window without blind purge replay.
- A-36: terminal approval-bound action fencing remains coupled to exact CAS
  finalization, including the cancellation generation.
- A-45: storage regressions, race, vet, SQLC regeneration, guardrails,
  planning, architecture, module, and Linux compile checks were run; the
  single root-race failure is recorded above for coordinator review.

Independent review should inspect concurrent cancellation/finalization,
stale-generation fencing, action-effect proof, duplicate JSON-key handling,
alias conflicts, malformed terminal evidence, and the distinction between
applied, already-satisfied, and unknown outcomes.

## Handoff

- Product commit: `ce6cc272deeb7bf93c10d4bb7a6d8f93f03f4972`.
- Documentation commit: pending this handoff commit.
- Independent review: `/root/f01_reviewer` against the product commit above.
- `docs/execution/state.json` remains coordinator-owned; this handoff did not
  edit it.
- Resume from the product commit, then apply this documentation commit while
  preserving unrelated shared-checkout changes.
