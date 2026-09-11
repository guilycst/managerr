# D-01 independent review, round fourteen

## Decision

`approved`.

Round fourteen closes the embedded-NUL trailing-data finding. The mutation
boundary inspects the raw bound bytes before SQLite text parsing, rejects NUL,
control, DEL, UTF-8, and invalid non-ASCII bytes without changing any bound
row, and preserves the strict RFC3339 and exact purge-effect invariants. No
new finding was identified.

## Review identity and boundary

- Reviewed product commit:
  `8098e2b552737e5e18848d27b665991fddce18bf`.
- Product parent: `274809cf57bf08a17dd0e12910d1f26d26b9c8af`.
- Assigned correction baseline:
  `bda29901e9ffc06a61632da171ce853bd9708261`.
- Implementation handoff commit:
  `4dceac51acff597041d8d1497891daafe42afb62`, file
  `docs/execution/handoffs/D-01-round14.md`.
- Round-thirteen receipt:
  `9265acfa62c46b667978f05264a44da4ba5f641a`, file
  `docs/execution/handoffs/D-01-review-round13.md`.
- Reviewed paths: `internal/storage/`, `migrations/`, `sqlc.yaml`, and generated
  SQLC output.
- Scoped correction diff identity:
  `2908902cd23a1729456760ea9999c72f461df1476ba5f081bfefbe24ee9205a0`.
- Scoped product snapshot:
  `e302524f4695443b51b29e3797bec119c4a3b2f35ad8059b68a0c4fe1bc26737`.
- Reviewer: `/root/f01_reviewer`, independent of the round-fourteen
  implementer.
- Checks ran in a clean detached reviewer-owned worktree at the exact product
  commit. Additional adversarial tests ran only in a disposable `/tmp`
  archive.
- This review changes only this receipt. It does not change execution state,
  product paths, credentials, live services, media, release state, or
  deployment.

The direct product commit changes
`internal/storage/compatibility_round11_test.go`,
`internal/storage/query.sql`, and generated
`internal/storage/sqlc/query.sql.go`. Migrations, `sqlc.yaml`, and the SQLC
interface are unchanged. Regenerated SQLC is byte-identical and contains no
unexpanded `sqlc.arg` references.

## Finding disposition

| Prior finding | Round-fourteen result |
| --- | --- |
| P2: embedded NUL makes trailing data invisible to the SQL validator | Closed. `instr(CAST(requested_at AS BLOB), X'00') = 0` rejects NUL at any byte position before text parsing. The printable-ASCII guard rejects every other control, DEL, UTF-8, and invalid non-ASCII byte. Rejected input returns `sql.ErrNoRows` and leaves the action, janitor, and trash entry unchanged. |

No current blocker or follow-up finding remains in the reviewed D-01 scope.

## Independent adversarial evidence

The prior round-eleven, twelve, and thirteen reviewer tests pass unchanged.
They cover exact effect kind/target binding, state/outcome agreement, NULL and
ordinary malformed timestamps, calendar and offset validation, and the
original NUL reproduction.

An additional reviewer-only matrix exercised:

- NUL after a valid UTC timestamp;
- NUL after a valid numeric-offset timestamp;
- NUL inside fractional seconds and inside an offset;
- ASCII control byte `0x1f` and DEL `0x7f` after a valid timestamp;
- valid year-zero UTC, one-digit and long fractional seconds, maximum numeric
  offset `+23:59`, and RFC3339 unknown-local-offset `-00:00` boundaries.

Every invalid-byte case returned `sql.ErrNoRows`. Full equality comparisons
confirmed no change to the action, janitor, trash entry, generation, marker,
lease, timestamps, or other approval binding. Every valid boundary produced
one version-three cancellation marker. The product's UTF-8 and invalid raw
non-ASCII cases also pass.

## Preserved invariants

- The byte checks run before all `length`, `substr`, `GLOB`, cast, and calendar
  predicates. SQLite cannot hide an invalid suffix from the validator.
- Valid UTC, positive/negative numeric offset, fractional-second, leap-day,
  year-zero, long-fraction, and offset-boundary timestamps remain accepted.
  Repeated valid cancellation retains the first timestamp and generation.
- NULL, blank, numeric, date-only, time-only, impossible date, invalid hour or
  offset, visible trailing data, NUL-bearing data, controls, DEL, UTF-8, and
  invalid non-ASCII bytes change no durable row.
- Count-free applied evidence remains bound to the same action, applied
  `fs.delete`, `target_kind='trash_entry'`, and exact approved trash-entry ID in
  both state and outcome classification.
- Any same-action applied effect prevents an `already_satisfied` zero-effect
  claim. Wrong effect kind/target/state and malformed or contradictory outcome
  evidence remain `needs_review`/`unknown`.
- Exact action/version, approval decision, immutable plan/target/manifest,
  janitor/trash generation, generic-claim exclusion, terminal finalization,
  deadline, migration, process-lock, and restart fencing regressions remain
  green.

## Independent checks

All executable checks used synthetic SQLite fixtures and no live service,
credential, media payload, external mutation, release, or deployment.

| Command or scenario | Result |
| --- | --- |
| Exact product and handoff diff, round-thirteen receipt, frozen API/spec, task and acceptance cases | Inspected independently. The direct product commit changes only the three assigned storage/generated files. |
| Scoped diff/snapshot, migration comparison, `git diff --check`, owned gofmt, public path/credential scan | Passed. Migrations and `sqlc.yaml` are unchanged. |
| SQLC generation in a disposable archive, byte comparison, and unexpanded-argument scan | Passed; generated `query.sql.go` is byte-identical and contains no `sqlc.arg` call. |
| Prior reviewer adversarial suite, `-count=10` | Passed in 10.788 seconds; every round-eleven through thirteen reproduction is closed. |
| Additional byte and valid-boundary reviewer matrix, `-count=10` | Passed in 10.546 seconds; rejected inputs preserve all three bound records exactly. |
| Product `Round11|Round12|Round13|Round14` suite, `-count=10` | Passed in 62.907 seconds in the exact clean product worktree. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage/...` | Passed; storage completed in 21.743 seconds. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=300s ./internal/storage/...` | Passed; storage completed in 244.730 seconds. |
| `GOWORK=off go vet -mod=readonly ./internal/storage/...` | Passed. |
| Full exact-product root test and vet | Passed; all root packages are green. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed; storage completed in 244.216 seconds. |
| Generation, API, architecture, planning, and fast guardrail checks | Passed; Vacuum quality 100/100, planning reports 38 tasks and 60 acceptance cases. |
| Root module verify/tidy and host compile-only check | Passed. |
| Tools module test, vet, verify, and tidy diff | Passed. |
| UI module test, vet, and verify | Passed. |
| UI module tidy diff | Reports removal of existing future UI dependency pins; D-01 changes no UI or module file. |
| Darwin host and Linux amd64/arm64 CGO-free compile-only checks | Passed. |

## Acceptance contribution assessment

- A-32: accepted for D-01. Cancellation requires a contract-valid durable
  timestamp before any generation changes; valid cancellation and recovery are
  exact and idempotent.
- A-36: accepted for D-01. Exact effect identity, coherent state/outcome, and
  action/janitor/trash CAS fencing pass.
- A-43: accepted for D-01. Migrations are unchanged and prior upgrade,
  downgrade, locking, and recovery checks remain green.
- A-45: accepted for D-01. SQLC regeneration, tests, race, vet, modules,
  guardrails, planning, architecture, and cross-compiles pass.

No product or `docs/execution/state.json` file was modified by this review.
