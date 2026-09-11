# D-01 independent review, round thirteen

## Decision

`changes_requested`.

Round thirteen closes the ordinary RFC3339/date-time validation finding and
preserves the exact purge-effect and cancellation-generation invariants. One
P2 trailing-data bypass remains. SQLite text functions stop at an embedded NUL,
so a valid timestamp followed by `\x00trailing` satisfies every SQL shape check,
advances the action generation, and stores the full invalid marker.

## Review identity and boundary

- Reviewed product commit:
  `bda29901e9ffc06a61632da171ce853bd9708261`.
- Product parent: `2284a793175de3e1ba89c94f027b5ff6ae8993e4`.
- Assigned correction baseline:
  `35ad5ca2ba265643134a8fa08d747ee91ad95c73`.
- Implementation handoff commit:
  `0437c149cc32b41c3351c2671bcdd4e476c397fe`, file
  `docs/execution/handoffs/D-01-round13.md`.
- Round-twelve receipt:
  `825f2c694372fb0d1a9f0e336b8adc1353f3161f`, file
  `docs/execution/handoffs/D-01-review-round12.md`.
- Reviewed paths: `internal/storage/`, `migrations/`, `sqlc.yaml`, and generated
  SQLC output.
- Scoped correction diff identity:
  `ba22ace1b21884a273a39cf513871101348de47999550e225961336482ddc01d`.
- Scoped product snapshot:
  `0b361d4a8dc009d8627927657e8270dafa2fa9e037f05814bcfcc5f1d63ccc03`.
- Reviewer: `/root/f01_reviewer`, independent of the round-thirteen
  implementer.
- Checks ran in a clean detached reviewer-owned worktree at the exact product
  commit. Adversarial tests ran only in a disposable `/tmp` archive.
- This review changes only this receipt. It does not change execution state,
  product paths, credentials, live services, media, release state, or
  deployment.

The direct product commit changes
`internal/storage/compatibility_round11_test.go`,
`internal/storage/query.sql`, and generated
`internal/storage/sqlc/query.sql.go`. Migrations, `sqlc.yaml`, and the SQLC
interface are unchanged. Regenerated SQLC is byte-identical and contains no
unexpanded `sqlc.arg` references.

## Finding

### P2: embedded NUL makes trailing data invisible to the SQL validator

- Location: the RFC3339 predicate in
  `internal/storage/query.sql:1011-1066`, especially its repeated `length`,
  `substr`, and `GLOB` checks.
- Evidence: SQLite stores the complete bound Go string, including embedded NUL
  bytes, but its text `length()` and `substr()` operations treat the first NUL
  as the end of the text. The validator therefore observes only the valid
  prefix while `cancellation_requested_at` receives the entire invalid value.
- Independent reproduction:
  1. Seed a running exact approved purge at action, janitor, and trash-entry
     generation 2.
  2. Call `RequestActionCancellation` with
     `RequestedAt="2026-09-11T00:01:13Z\x00trailing"`.
  3. Read the returned action and marker.
- Observed result, deterministic across five repeated runs:

  ```text
  NUL trailing timestamp mutated action version 2 to 3;
  marker bytes="2026-09-11T00:01:13Z\x00trailing"
  ```

- Failure mode: the mutation succeeds with trailing data even though the
  frozen API requires a complete `date-time` string. Later strict parsing for
  API output, audit display, ordering, or recovery can fail or expose a value
  outside the contract. The marker is nonnull and cancellation synchronization
  still occurs, so this does not recreate the generation split and remains P2.
- Contract: the round-twelve correction explicitly requires trailing data to
  be rejected without any generation or marker change. The API defines
  `Timestamp` as `format: date-time` at
  `api/fragments/components/schemas.yaml:14-16`, and required cancellation
  `requestedAt` uses it at `:1834-1857`. I-05 forbids invalid evidence from
  becoming a fresh claim, and A-32 requires durable recoverable state.
- Required change: reject embedded NUL before evaluating text shape or perform
  strict RFC3339 parsing outside SQLite and pass a validated nonnullable value.
  If the SQL boundary remains authoritative, compare byte length with text
  length or use another NUL-safe predicate before the update. Invalid input
  must not fire the cancellation trigger or change any row.
- Required proof: cover embedded NUL after valid UTC and offset timestamps,
  NUL inside the fraction and offset, and ordinary visible trailing data.
  Assert unchanged action, janitor, trash entry, generation, marker, lease, and
  update timestamp. Retain the valid UTC/offset/fraction/leap-day matrix.
- Disposition: `current_blocker`.

## Round-twelve finding disposition

| Round-twelve finding | Round-thirteen result |
| --- | --- |
| SQLite date parsing accepts non-timestamps as cancellation timestamps | Closed for numeric Julian days, time-only and date-only values, impossible/non-leap dates, ordinary trailing data, invalid hours/offsets, NULL, blank, and unparsable values. They return `sql.ErrNoRows` and preserve every bound row. Valid UTC, numeric offsets, fractions, leap days, and repeated cancellation pass. The P2 above is the remaining NUL-specific trailing-data boundary. |

## Preserved invariants

- Valid UTC, positive/negative numeric offset, fractional-second, leap-day, and
  leap-day-offset timestamps produce one durable cancellation generation.
  Repeated valid cancellation retains the first timestamp and generation.
- NULL, blank, numeric, date-only, time-only, impossible calendar date,
  non-leap date, visible trailing data, invalid hour, and invalid offset values
  change no action, janitor, or trash-entry field.
- Count-free applied evidence remains bound to the same action, applied
  `fs.delete`, `target_kind='trash_entry'`, and exact approved trash-entry ID in
  both state and outcome classification.
- Any same-action applied effect still prevents an `already_satisfied`
  zero-effect claim. Wrong effect kind/target/state and malformed or
  contradictory outcome evidence remain `needs_review`/`unknown`.
- Exact action/version, approval decision, immutable plan/target/manifest,
  janitor/trash generation, generic-claim exclusion, terminal finalization,
  deadline, migration, process-lock, and restart fencing regressions remain
  green.

## Independent checks

All executable checks used synthetic SQLite fixtures and no live service,
credential, media payload, external mutation, release, or deployment.

| Command or scenario | Result |
| --- | --- |
| Exact product and handoff diff, round-twelve receipt, frozen API/spec, task and acceptance cases | Inspected independently. The direct product commit changes only the three assigned storage/generated files. |
| Scoped diff/snapshot, migration comparison, `git diff --check`, owned gofmt, public path/credential scan | Passed. Migrations and `sqlc.yaml` are unchanged. |
| SQLC generation in a disposable archive, byte comparison, and unexpanded-argument scan | Passed; generated `query.sql.go` is byte-identical and contains no `sqlc.arg` call. |
| Prior round-eleven/twelve reviewer adversarial suite, `-count=10` | Passed in 8.922 seconds; exact effect binding and the prior invalid timestamp cases are closed. |
| Product `Round11|Round12|Round13` suite, `-count=10` | Passed in 59.116 seconds. |
| Embedded-NUL trailing-data adversarial probe, `-count=5` | Failed deterministically as described in the P2. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage/...` | Passed; storage completed in 18.222 seconds. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=300s ./internal/storage/...` | Passed; storage completed in 238.236 seconds. |
| `GOWORK=off go vet -mod=readonly ./internal/storage/...` | Passed. |
| Full exact-product root test and vet | Passed; all root packages are green. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed; storage completed in 239.487 seconds. |
| Generation, API, architecture, planning, and fast guardrail checks | Passed; Vacuum quality 100/100, planning reports 38 tasks and 60 acceptance cases. |
| Root module verify/tidy and host compile-only check | Passed. |
| Tools module test, vet, verify, and tidy diff | Passed. |
| UI module test, vet, and verify | Passed. |
| UI module tidy diff | Reports removal of existing future UI dependency pins; D-01 changes no UI or module file. |
| Darwin host and Linux amd64/arm64 CGO-free compile-only checks | Passed. |

## Acceptance contribution assessment

- A-32: not accepted while NUL-bearing trailing data can be committed as the
  required cancellation timestamp. All ordinary timestamp and generation
  cases are accepted.
- A-36: accepted for the preserved exact effect identity, coherent
  state/outcome, and action/janitor/trash CAS contribution.
- A-43: accepted for the reviewed migration contribution. Migrations are
  unchanged and prior upgrade, downgrade, locking, and recovery checks remain
  green.
- A-45: SQLC regeneration, tests, race, vet, module, guardrail, planning,
  architecture, and cross-compile gates pass. The remaining P2 is a semantic
  validation blocker rather than a generation failure.

No product or `docs/execution/state.json` file was modified by this review.
