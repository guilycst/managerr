# D-01 independent review, round eleven

## Decision

`changes_requested`.

Round eleven closes both exact round-ten reproductions for valid cancellation
timestamps and self-contained JSON count evidence. Two P1 storage-boundary
defects remain. The durable-effect fallback can prove a purge from an unrelated
effect or unrelated target, and its duplicated state/outcome classifiers can
disagree. The cancellation query also accepts a SQL `NULL` timestamp, advances
the action generation without recording cancellation, and can make the exact
approval-bound recovery generation unreachable.

## Review identity and boundary

- Reviewed product commit:
  `ce6cc272deeb7bf93c10d4bb7a6d8f93f03f4972`.
- Product parent: `1ee893dd5fad950d74c947d5a57bb8ee9c9a7454`.
- Assigned correction baseline:
  `a929c71126dbfcad24f1fcebdcf0eb9e920bf79c`.
- Implementation handoff commit:
  `c354dfd73c46f7bf08f4ad007d93c2df23daca9d`, file
  `docs/execution/handoffs/D-01-round11.md`.
- Round-ten receipt:
  `5f3632a397b1a858a8493c7528c9a80cc94164a6`, file
  `docs/execution/handoffs/D-01-review-round10.md`.
- Reviewed paths: `internal/storage/`, `migrations/`, and `sqlc.yaml`.
- Scoped product diff identity:
  `76039b84f22ccafceeb7ee5b9b2ca937e0655a56d4942616469cabe0851ff1be`.
- Scoped product snapshot:
  `5a645b94b24b720979e21545671d543903e4ba12966fa8ce8f26bb7c1570bf54`.
- Reviewer: `/root/f01_reviewer`, independent of the round-eleven implementer.
- Checks ran in a clean detached reviewer-owned worktree at the exact product
  commit. Adversarial tests ran only in a disposable `/tmp` archive.
- This review changes only this receipt. It does not change execution state,
  product paths, credentials, live services, media, release state, or
  deployment.

The direct product commit changes coordinator-owned
`docs/execution/state.json`, adds
`internal/storage/compatibility_round11_test.go`, and changes
`internal/storage/query.sql`, generated `internal/storage/sqlc/querier.go`, and
generated `internal/storage/sqlc/query.sql.go`. Migrations and `sqlc.yaml` are
unchanged from the assigned baseline. Regenerated SQLC is byte-identical.

## Findings

### P1: unrelated durable effects can prove the purge and split state from outcome

- Location: the four copied terminal-outcome classifiers in
  `internal/storage/query.sql:624-926`. The action-state predicates at
  `:652-660` and `:725-733` require only the same action, `fs.delete`, and
  `applied`. The outcome predicates at `:807-814` and `:878-885` require only
  the same action and `applied` state. None binds `target_kind` or `target_id`
  to the janitor's trash entry.
- Evidence: `action_effects` stores an explicit `target_kind`, `target_id`, and
  `effect_kind`; the exact approved purge is bound to
  `janitor.trash_entry_id`. The success fallback ignores some or all of that
  identity. Because the classifier is repeated with different predicates, the
  action state and serialized outcome can also contradict each other.
- Independent reproduction:
  1. Seed one exact approved purge whose janitor reports only
     `{"outcome":"applied"}` or `{"outcome":"already_satisfied"}`.
  2. Add one same-action applied effect that is `fs.copy`, or an `fs.delete`
     for another target ID or target kind.
  3. Terminalize the janitor and call `FinalizeApprovedPurgeAction` with the
     exact action generation.
- Observed result, deterministic across five repeated runs:

  ```text
  applied + fs.copy              -> state=needs_review, outcome=applied
  already_satisfied + fs.copy    -> state=succeeded, outcome=unknown
  applied + wrong target ID      -> state=succeeded, outcome=applied
  applied + wrong target kind    -> state=succeeded, outcome=applied
  ```

- Failure mode: a completed copy, a deletion of another object, or an effect
  with a different target type can be presented as proof that this approved
  trash entry was purged. One branch can also return terminal `succeeded` while
  serializing `unknown`, or `needs_review` while preserving `applied`. This is
  a false desired-state claim and loses the state/outcome invariant.
- Contract: `data-and-recovery.md:52` permits `succeeded` only when desired
  state is proven; `:68-76` requires each successful effect to be recorded and
  desired state read back before success. I-02 binds execution to the immutable
  approved target, I-05 forbids ambiguous evidence from becoming a safety
  claim, and I-06 reserves `already_satisfied` for zero new effects.
  `http-api.md:118-123` permits positive outcome labels only with supporting
  desired-state evidence. A-36 requires the exact local target/CAS boundary.
- Required change: use one identical terminal-evidence predicate for both
  action state and outcome. A durable purge-effect proof must match the exact
  approval action, `effect_kind='fs.delete'`, `state='applied'`,
  `target_kind='trash_entry'`, and `target_id=janitor.trash_entry_id` (or a
  documented equally exact immutable target identity). An unrelated applied
  effect must neither prove `applied` nor allow `already_satisfied` to claim
  zero effects.
- Required proof: cover exact matching delete effect, unrelated effect kind,
  wrong target ID, wrong target kind, failed/unknown effect, multiple mixed
  effects, and both terminal outcome labels. Assert that state and outcome
  always agree and that unproven evidence becomes `needs_review`/`unknown`.
- Disposition: `current_blocker`.

### P1: NULL cancellation advances the action without a cancellation marker

- Location: `RequestActionCancellation` at
  `internal/storage/query.sql:994-1003`, generated parameter type at
  `internal/storage/sqlc/query.sql.go:4735-4739`, and the cancellation-sync
  trigger at
  `migrations/000009_storage_recovery_boundaries.up.sql:601-647`.
- Evidence: the public SQLC parameter is `sql.NullString`. With
  `RequestedAt.Valid=false`, `COALESCE(NULL, NULL)` leaves
  `cancellation_requested_at` null, while the same statement increments
  `version` because the old column is null and updates `updated_at`. The trigger
  watches a real marker change, so NULL-to-NULL does not advance the bound
  janitor or trash entry.
- Independent reproduction:
  1. Seed a running exact approved purge at action, janitor, and trash-entry
     generation 2.
  2. Call `RequestActionCancellation` with `RequestedAt: sql.NullString{}`.
  3. Read the returned action and the bound records.
- Observed result, deterministic across five repeated runs:

  ```text
  null cancellation advanced action from version 2 to 3 without a cancellation marker
  ```

- Failure mode: this is neither a durable cancellation nor a no-op. It can
  separate the action generation from the approval-bound janitor/trash
  generation, while the round-eleven equal-generation finalizer remains gated
  on a nonnull cancellation marker. A crash then leaves no truthful exact
  terminal/recovery path for the current generation.
- Contract: I-09 requires cancellation to be durable and stop future dispatch;
  `data-and-recovery.md:68-82` requires exact claim/cancellation boundaries and
  restart-safe observation. A-32 requires durable recovery and A-36 requires
  exact CAS fencing without losing the owner.
- Required change: reject a missing cancellation timestamp before any row or
  generation changes, preferably in the SQL mutation boundary as well as its
  caller. Do not return a changed action without a durable cancellation marker.
  Preserve the one-shot behavior for an already valid marker.
- Required proof: exercise SQL NULL and blank/invalid timestamps against active
  approval-bound and ordinary actions, plus valid first/repeated requests.
  Invalid input must change no row, generation, lease, janitor, or trash entry;
  valid input must remain one-shot and restart/finalizer safe.
- Disposition: `current_blocker`.

## Round-ten finding disposition

| Round-ten finding | Round-eleven result |
| --- | --- |
| Cancellation after terminal janitor commit strands the action | Closed for a valid nonnull cancellation request. All terminal janitor states accept the separately proven equal generation, finalize once, clear the lease, and reject repetition. The new NULL-input generation defect above remains open at the cancellation mutation boundary. |
| Contradictory or duplicate success evidence is accepted as applied | Closed for the requested JSON cases: zero, negative, fractional, string, null, boolean, conflicting aliases, duplicate recognized keys, missing outcome, and malformed JSON become `needs_review`/`unknown`; coherent counts are preserved. The newly added durable-effect alternative is not exact and remains open in the first P1. |

## Preserved invariants

- A valid cancellation committed after terminal janitor success, failure, hold,
  or cancellation finalizes the exact action once. Repeated cancellation keeps
  the first timestamp and generation; repeated finalization returns no row.
- Coherent positive integral camel-case, snake-case, and equal dual-alias
  applied counts remain `applied`. Absent or integral-zero already-satisfied
  counts remain `already_satisfied` when no effect contradicts them.
- Malformed JSON and the requested ambiguous count/outcome shapes finalize as
  `needs_review` with `unknown` rather than terminal success.
- The finalizer retains the exact action ID/version, ready plan revision and
  digest, approving decision, immutable `fs.delete` target, nonempty equal
  manifests, janitor generation, terminal trash entry, and inactive-operation
  gates.
- Prior action-first and janitor-first recovery, generic claim exclusion,
  concurrent finalizer CAS, cancellation with a valid timestamp, deadlines,
  stale plan/entry generations, migration downgrade/re-upgrade, canonical
  process locking, trash constraints, and close/restart regressions remain
  green.

## Independent checks

All executable checks used synthetic SQLite fixtures and no live service,
credential, media payload, external mutation, release, or deployment.

| Command or scenario | Result |
| --- | --- |
| Exact product/handoff diff, round-ten receipt, frozen specs, D-01 task and acceptance cases | Inspected independently. The direct product commit changes only coordinator state plus the four assigned storage/generated files. |
| Scoped diff/snapshot, migration comparison, `git diff --check`, owned gofmt, public path/credential scan | Passed. Migrations and `sqlc.yaml` are unchanged. |
| SQLC generation in disposable archive plus byte comparison | Passed; regenerated `query.sql.go` is byte-identical to the product. |
| Product `TestRound11` suite, `-count=20` | Passed in 53.792 seconds. |
| Prior round-ten reviewer regressions, `-count=20` | Passed in 16.151 seconds; both original P1 reproductions are closed. |
| Valid terminal-cancellation reviewer probe, `-count=10` | Passed. |
| Durable-effect identity/state-outcome adversarial probe, `-count=5` | Failed deterministically with all four mismatches shown in the first P1. |
| NULL-cancellation adversarial probe, `-count=5` | Failed deterministically; action version advanced from 2 to 3 without a marker. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage/...` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=300s ./internal/storage/...` | Passed; storage completed in 176.056 seconds. |
| `GOWORK=off go vet -mod=readonly ./internal/storage/...` | Passed. |
| Full exact-product root test and vet | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed on the clean exact product; storage completed in 178.235 seconds. The unowned Seerr race failure recorded by the implementer exists only in the shared checkout and is outside this review. |
| Generation, API, architecture, planning, lint, and fast guardrail checks | Passed; Vacuum quality 100/100, planning reports 38 tasks and 60 acceptance cases. |
| Root module verify/tidy and host compile-only check | Passed. |
| Tools module test, vet, verify, and tidy diff | Passed. |
| UI module test, vet, and verify | Passed. |
| UI module tidy diff | Reports removal of existing future UI dependency pins; D-01 changes no UI or module file. |
| Darwin host and Linux amd64/arm64 CGO-free compile-only checks | Passed. |

## Acceptance contribution assessment

- A-32: not accepted. Valid terminal-cancellation recovery is fixed, but a
  nullable cancellation call can advance the action without durable intent and
  strand the exact generation.
- A-36: not accepted. The action/janitor/entry CAS remains exact for valid
  inputs, but NULL cancellation breaks it and unrelated effect targets can
  satisfy the terminal purge predicate.
- A-43: accepted for the reviewed migration contribution. Migrations are
  unchanged and prior upgrade, downgrade, locking, and recovery checks remain
  green.
- A-45: SQLC regeneration, tests, race, vet, module, guardrail, planning,
  architecture, and cross-compile gates pass. The two semantic findings remain
  product blockers rather than generation failures.

No product or `docs/execution/state.json` file was modified by this review.
