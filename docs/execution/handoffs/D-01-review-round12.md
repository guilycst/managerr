# D-01 independent review, round twelve

## Decision

`changes_requested`.

Both round-eleven P1 findings are closed. Count-free applied evidence now binds
the exact approved trash-entry delete effect, state and outcome use coherent
predicates, and NULL/blank/unparsable cancellation values cannot change a row.
One P2 validation defect remains: SQLite's `julianday()` accepts values that
are dates, times, or Julian-day numbers but are not timestamps under the
public `date-time` contract. Those values still advance the action generation
and become durable cancellation markers.

## Review identity and boundary

- Reviewed product commit:
  `35ad5ca2ba265643134a8fa08d747ee91ad95c73`.
- Product parent: `a11c8787c5118c44a6c4f075fb8bbf55cb6cb06c`.
- Assigned correction baseline:
  `ce6cc272deeb7bf93c10d4bb7a6d8f93f03f4972`.
- Implementation handoff commit:
  `c07ba01410949a052ad5276c5f9a48406b49a6a7`, file
  `docs/execution/handoffs/D-01-round12.md`.
- Round-eleven receipt:
  `559a9b435f648f89e79eca707ba2b86bada51dbf`, file
  `docs/execution/handoffs/D-01-review-round11.md`.
- Reviewed paths: `internal/storage/`, `migrations/`, `sqlc.yaml`, and generated
  SQLC output.
- Scoped correction diff identity:
  `b6a38d1a65b4eb73d75ca41a920579ae6d24d2a77b3f12f3b1384ae57b4dbe11`.
- Scoped product snapshot:
  `5927eaa8f9875e1d4b64efa1ea67c8e8e72df793612fab14b81e2ab639526673`.
- Reviewer: `/root/f01_reviewer`, independent of the round-twelve implementer.
- Checks ran in a clean detached reviewer-owned worktree at the exact product
  commit. Adversarial tests ran only in a disposable `/tmp` archive.
- This review changes only this receipt. It does not change execution state,
  product paths, credentials, live services, media, release state, or
  deployment.

The direct product commit changes
`internal/storage/compatibility_round11_test.go`,
`internal/storage/query.sql`, and generated
`internal/storage/sqlc/query.sql.go`. Migrations, `sqlc.yaml`, and the SQLC
interface are unchanged. Regenerated SQLC is byte-identical.

## Finding

### P2: SQLite date parsing accepts non-timestamps as cancellation timestamps

- Location: `RequestActionCancellation` at
  `internal/storage/query.sql:1002-1014`, especially the validation predicate
  `julianday(requested_at) IS NOT NULL` at line 1013.
- Evidence: SQLite `julianday()` recognizes a broad set of date/time values. It
  does not establish that its input is a complete, valid timestamp. The API
  defines `Timestamp` as a string with `format: date-time` at
  `api/fragments/components/schemas.yaml:14-16`, and a cancellation's required
  `requestedAt` uses that type at `:1834-1857`.
- Independent reproduction:
  1. Seed a running exact approved purge at action, janitor, and trash-entry
     generation 2.
  2. Call the generated `RequestActionCancellation` query with each nonempty
     value below in `RequestedAt`.
  3. Read the returned action and stored cancellation marker.
- Observed result, deterministic across five repeated runs:

  ```text
  "1"                    -> action version 3, marker "1"
  "12:34"                -> action version 3, marker "12:34"
  "2026-09-11"           -> action version 3, marker "2026-09-11"
  "2026-02-30T00:00:00Z" -> action version 3, marker "2026-02-30T00:00:00Z"
  ```

  The first is interpreted as a Julian-day number, the second as a time-only
  value, the third is date-only, and the fourth has an impossible calendar
  date. None satisfies the API timestamp type.
- Failure mode: the storage boundary returns success and commits a generation
  change with a value that cannot be represented truthfully as the required
  API timestamp. Later API mapping, audit display, ordering, or recovery code
  must either fail parsing or expose a value outside the contract. This does
  not recreate the NULL generation split because the marker is nonnull and the
  cancellation trigger runs, so severity is P2 rather than P1.
- Contract: the round-eleven required proof explicitly includes invalid
  timestamps changing no row. The frozen API requires `requestedAt` to be a
  `date-time`. I-05 forbids invalid evidence from becoming a fresh claim, and
  A-32 requires durable state to survive recovery without malformed required
  fields.
- Required change: validate the complete timestamp contract before the SQL
  mutation, using a strict timestamp parser and a nonnullable validated value,
  or enforce an equivalent strict representation at the storage boundary.
  A generic SQLite date/time parse is insufficient. Reject calendar-invalid,
  date-only, time-only, numeric Julian-day, trailing-data, and out-of-range
  values without changing any action, janitor, or trash-entry field.
- Required proof: cover the four reproduced values plus a valid UTC timestamp,
  a valid offset timestamp, leap-day boundaries, and trailing data. Invalid
  cases must return a typed rejection or no row and preserve all generations,
  markers, timestamps, and leases. Valid first and repeated cancellation must
  remain one-shot and exact.
- Disposition: `current_blocker`.

## Round-eleven finding disposition

| Round-eleven finding | Round-twelve result |
| --- | --- |
| Unrelated durable effects can prove the purge and split state from outcome | Closed. All four classification copies require the same action, applied `fs.delete`, `target_kind='trash_entry'`, and the exact janitor trash-entry ID. `already_satisfied` rejects every same-action applied effect. Prior wrong-kind/ID/target and split state/outcome probes pass. |
| NULL cancellation advances the action without a marker | Closed for NULL, blank, and values that SQLite cannot parse. They return `sql.ErrNoRows` and preserve action, janitor, trash entry, marker, lease, version, and update timestamp. Valid cancellation remains one-shot. The P2 above narrows the remaining issue to SQLite-parseable values that violate the timestamp contract. |

## Preserved invariants

- Count-free applied evidence succeeds only with an exact same-action applied
  `fs.delete` effect for the approved trash entry. Wrong effect kinds, target
  IDs, target kinds, failed effects, and unknown effects become
  `needs_review`/`unknown`.
- Exact delete evidence mixed with an unrelated applied effect remains applied,
  with all effects retained. Any applied effect prevents an
  `already_satisfied` zero-effect claim.
- Coherent count-based applied and already-satisfied evidence remains
  unchanged. Malformed, duplicate, conflicting, zero-applied, and
  positive-already-satisfied evidence remains `needs_review`/`unknown`.
- NULL, blank, and unparsable cancellation requests change no row for either
  approval-bound or ordinary actions. Valid first/repeated cancellation and
  terminal finalization remain exact and idempotent.
- Exact action/version, approval decision, immutable plan/target/manifest,
  janitor/trash generation, generic-claim exclusion, deadline, migration,
  process-lock, and restart fencing regressions remain green.

## Independent checks

All executable checks used synthetic SQLite fixtures and no live service,
credential, media payload, external mutation, release, or deployment.

| Command or scenario | Result |
| --- | --- |
| Exact product and handoff diff, round-eleven receipt, frozen API/spec, task and acceptance cases | Inspected independently. The direct product commit changes only the three assigned storage/generated files. |
| Scoped diff/snapshot, migration comparison, `git diff --check`, owned gofmt, public path/credential scan | Passed. Migrations and `sqlc.yaml` are unchanged. |
| SQLC generation in a disposable archive plus byte comparison | Passed; regenerated `query.sql.go` is byte-identical to the product. |
| Prior round-eleven reviewer adversarial suite, `-count=10` | Passed in 5.786 seconds; both P1 reproductions are closed. |
| Product `Round11|Round12` suite, `-count=10` | Passed in 44.568 seconds. |
| Non-timestamp cancellation adversarial probe, `-count=5` | Failed deterministically for all four values described in the P2. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage/...` | Passed; storage completed in 16.058 seconds. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=300s ./internal/storage/...` | Passed; storage completed in 215.685 seconds. |
| `GOWORK=off go vet -mod=readonly ./internal/storage/...` | Passed. |
| Full exact-product root test and vet | Passed; all root packages are green. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed; storage completed in 215.761 seconds. |
| Generation, API, architecture, planning, and fast guardrail checks | Passed; Vacuum quality 100/100, planning reports 38 tasks and 60 acceptance cases. |
| Root module verify/tidy and host compile-only check | Passed. |
| Tools module test, vet, verify, and tidy diff | Passed. |
| UI module test, vet, and verify | Passed. |
| UI module tidy diff | Reports removal of existing future UI dependency pins; D-01 changes no UI or module file. |
| Darwin host and Linux amd64/arm64 CGO-free compile-only checks | Passed. |

## Acceptance contribution assessment

- A-32: not accepted while the cancellation boundary can commit an invalid
  required timestamp. NULL/blank/unparsable mutation and generation safety is
  accepted.
- A-36: accepted for the corrected effect identity and state/outcome
  contribution. Exact target and action CAS fencing pass.
- A-43: accepted for the reviewed migration contribution. Migrations are
  unchanged and prior upgrade, downgrade, locking, and recovery checks remain
  green.
- A-45: SQLC regeneration, tests, race, vet, module, guardrail, planning,
  architecture, and cross-compile gates pass. The remaining P2 is a semantic
  validation blocker rather than a generation failure.

No product or `docs/execution/state.json` file was modified by this review.
