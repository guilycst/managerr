# D-01 implementation handoff, correction round twelve

## Decision

`pending_independent_review`.

This handoff records the two bounded fixes requested by the independent
round-eleven storage review. `/root/f01_reviewer` should review the product
commit below independently and write its receipt separately.

## Assignment and boundary

- Task: D-01, SQLite schema, migrations and sqlc repository.
- Review input: round-eleven receipt at
  `docs/execution/handoffs/D-01-review-round11.md`, receipt
  `559a9b435f648f89e79eca707ba2b86bada51dbf`.
- Review baseline product:
  `ce6cc272deeb7bf93c10d4bb7a6d8f93f03f4972`.
- Previous handoff:
  `docs/execution/handoffs/D-01-round11.md`, receipt
  `c354dfd73c46f7bf08f4ad007d93c2df23daca9d`.
- Shared checkout queue commit:
  `520d2ae0a0d85128958889c73d79411a4b6422e6`.
- Implementer: `/root/f01_implementer`; reviewer: `/root/f01_reviewer`.
- Product commit:
  `35ad5ca2ba265643134a8fa08d747ee91ad95c73`,
  `fix(storage): bind purge evidence and validate cancellation`.
- Owned product paths: `internal/storage/`, `migrations/`, `sqlc.yaml`, and
  generated SQLC output under `internal/storage/sqlc/`.
- Handoff path: `docs/execution/handoffs/D-01-round12.md`.
- `migrations/` and `sqlc.yaml` are unchanged. Coordinator state, Arr files,
  and Arr fixtures were not staged by this lane.

## Changes

The count-free `applied` terminal-outcome fallback now requires one exact
durable effect for the approved purge: the same approval action run,
`effect_kind = 'fs.delete'`, `state = 'applied'`,
`target_kind = 'trash_entry'`, and `target_id = janitor.trash_entry_id`.
The same evidence predicate is present in both action-state and serialized
outcome classification branches. A copy, a different target kind, a different
target ID, a failed effect, or an unknown effect cannot prove the approved
trash entry was purged.

`already_satisfied` is rejected whenever the same action has any applied
effect. This prevents an unrelated applied copy or delete from being hidden by
a zero-effect claim. Valid count evidence remains accepted, and valid raw
janitor outcome JSON remains preserved. Unproven terminal success becomes
`needs_review` with an `unknown` outcome as before.

`RequestActionCancellation` now rejects a missing, blank, or unparsable
`RequestedAt` at the SQL mutation boundary. The `sql.NullString` argument is
checked for non-NULL, non-empty content and a valid SQLite date value before
the row can be updated. Invalid input returns `sql.ErrNoRows` and changes no
action generation, cancellation marker, lease, janitor, or trash entry. A
valid request remains a durable one-shot generation change; repeated valid
requests remain idempotent.

No schema or migration change was needed. SQLC was regenerated from the
updated query, and the generated query is clean.

## Regression coverage

`internal/storage/compatibility_round11_test.go` now covers the round-twelve
boundaries in addition to the prior round-eleven cases:

- count-free applied evidence with the exact approved trash-entry delete
  effect;
- an exact effect mixed with an unrelated effect;
- wrong effect kind, wrong target ID, wrong target kind, failed effect, and
  unknown effect;
- already-satisfied outcomes with unrelated, wrong-target, exact, and mixed
  applied effects, asserting `needs_review`/`unknown` and state/outcome
  agreement;
- NULL, blank, and invalid cancellation timestamps on an active
  approval-bound action, asserting that action, janitor, and trash-entry rows
  remain unchanged;
- the same invalid inputs on an ordinary unbound running action.

The existing valid cancellation-after-terminal-janitor matrix still proves
exact-generation finalization and repeated-cancellation idempotence. No live
media service, upstream write, filesystem mutation, credential, private
inventory, or deployment was used.

## Verification

| Command or scenario | Result |
| --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage -run 'Round11|Round12' -v` | Passed; all strict evidence and invalid cancellation cases passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage/...` | Passed in 12.759s. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=300s ./internal/storage/...` | Passed in 189.840s. |
| `GOWORK=off go vet -mod=readonly ./internal/storage/...` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed for all root packages. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed for all root packages in 223.727s. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `cd tools && GOWORK=off go tool github.com/sqlc-dev/sqlc/cmd/sqlc generate -f ../sqlc.yaml` | Passed; regenerated SQLC is clean. |
| `./scripts/generate.sh --check` | Passed. |
| `./scripts/check-api.sh` | Passed; Vacuum quality 100/100. |
| `python3 scripts/check-architecture.py && python3 scripts/check_planning.py` | Passed; 38 tasks, 60 acceptance cases and local links resolve. |
| `cd tools && GOWORK=off go test -mod=readonly -count=1 ./... && GOWORK=off go vet -mod=readonly ./... && GOWORK=off go mod verify && GOWORK=off go mod tidy -diff` | Passed. |
| `cd ui && GOWORK=off go test -mod=readonly -count=1 ./... && GOWORK=off go vet -mod=readonly ./... && GOWORK=off go mod verify` | Passed. |
| `GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed host compile-only check. |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| Product commit hook | Passed generation, API, architecture, tests, and fast guardrails. |
| `git diff --check` | Passed for the product and handoff changes. |

The shared checkout retains unrelated modified Arr client files and Arr
fixture files; they were never staged by this lane.

## Acceptance contribution

- A-32: NULL or malformed cancellation input can no longer advance an action
  without a durable marker; valid terminal-cancellation recovery remains
  exact and idempotent.
- A-36: count-free purge evidence is bound to the immutable approved trash
  entry and the `fs.delete` effect, with action state and outcome using the
  same evidence rules.
- A-45: storage tests, race, vet, SQLC regeneration, root checks, guardrails,
  planning, architecture, module, and Linux compile gates pass.

Independent review should inspect action/effect identity joins, mixed applied
effects, malformed and conflicting terminal evidence, SQL NULL/blank/date
validation, valid repeated cancellation, and stale-generation finalization.

## Handoff

- Product commit: `35ad5ca2ba265643134a8fa08d747ee91ad95c73`.
- Documentation commit: pending this handoff commit.
- Independent review: `/root/f01_reviewer` against the product commit above.
- `docs/execution/state.json` remains coordinator-owned; this handoff does not
  edit it.
- Resume from the product commit, then apply this documentation commit while
  preserving unrelated shared-checkout changes.
