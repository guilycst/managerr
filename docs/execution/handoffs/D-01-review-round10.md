# D-01 independent review, round ten

## Decision

`changes_requested`.

Round ten closes the ordinary initial-dispatch crash gap and preserves a
coherent `applied` or `already_satisfied` janitor outcome. Two P1 recovery and
truthfulness defects remain. A cancellation request that commits after the
janitor/trash terminal transaction advances only the action generation, which
makes the exact finalizer permanently unreachable. The success classifier also
accepts contradictory or ambiguous `applied` evidence, including zero or
malformed deletion counts and duplicate JSON keys.

## Review identity and boundary

- Reviewed product commit:
  `a929c71126dbfcad24f1fcebdcf0eb9e920bf79c`.
- Product parent: `37e501801c6d6d66735913e03d5997e56c9eaa38`.
- Assigned correction baseline:
  `eb2f8076f5aef8bfb33cb58f81ada53b7759a189`.
- Implementation handoff commit:
  `42758d4ec54854d5df9cbc60be5b4fe421c0e17c`, file
  `docs/execution/handoffs/D-01-round10.md`.
- Round-nine receipt:
  `5f58a72d6ab4484dc1f4851236152bbae3e7a4ed`, file
  `docs/execution/handoffs/D-01-review-round9.md`.
- Reviewed paths: `internal/storage/`, `migrations/`, and `sqlc.yaml`.
- Scoped product diff identity:
  `4ec09992dfe69f051a7f65ab41ed4ed35d2c3eee5dfc8db71e7564cebecd92aa`.
- Scoped product snapshot:
  `a259a40eab326fc85131c71609244aa608334904a1000393cb1893cb6b2b7a8f`.
- Reviewer: `/root/f01_reviewer`, independent of the round-ten implementer.
- Checks ran in a clean detached reviewer-owned worktree at the exact product
  commit. Adversarial tests ran only in a disposable `/tmp` Git archive.
- This review changes only this receipt. It does not change execution state,
  product paths, credentials, live services, media, release state, or
  deployment.

The correction changes only
`internal/storage/compatibility_round10_test.go`,
`internal/storage/compatibility_round9_test.go`, `internal/storage/query.sql`,
and generated `internal/storage/sqlc/query.sql.go`. Migrations and `sqlc.yaml`
are unchanged from the assigned baseline. Regenerated SQLC is byte-identical.

## Findings

### P1: cancellation after terminal janitor commit strands the action

- Location: `RequestActionCancellation` at
  `internal/storage/query.sql:730-733`, the terminal finalizer generation gate
  at `:672-701`, and the v9 cancellation-sync trigger at
  `migrations/000009_storage_recovery_boundaries.up.sql:601-647`.
- Evidence: initial approved dispatch has action version 2 and
  janitor/trash-entry version 2. Terminal janitor completion advances the
  janitor and trash entry to version 3 while the action remains running at
  version 2. A cancellation request is still allowed for that action and
  advances it to version 3. The cancellation-sync trigger matches only a
  janitor in `running` or `reconciling`, so it does not advance a terminal
  janitor or entry. `FinalizeApprovedPurgeAction` then requires
  `janitor.version = action_runs.version + 1`; version 3 cannot equal 4.
- Independent reproduction:
  1. Claim an exact approved early purge and commit a synthetic successful
     janitor outcome `{"outcome":"applied","deletedObjects":1}`.
  2. Confirm the coupled action is still running at version 2 and the terminal
     janitor/trash entry are version 3.
  3. Call `RequestActionCancellation`; it succeeds and advances the action to
     version 3 without changing the terminal janitor generation.
  4. Call the exact finalizer with that current action version.
- Observed result, repeated five times:

  ```text
  terminal finalization after cancellation race: sql: no rows in result set
  ```

- Failure mode: if cancellation wins the small race after terminal purge
  recording but before action finalization, the accepted irreversible effect
  remains hidden behind a leased running action. Generic recovery, due listing,
  and claiming correctly remain fenced, but there is no exact completion path.
  The same generation mismatch applies to every terminal janitor state.
- Contract: I-09 requires cancellation to stop future dispatch without claiming
  rollback of accepted effects. `data-and-recovery.md:48-55` keeps effects
  visible across cancellation, and `:68-76` requires durable read-back
  completion. A-32 requires restart-safe recovery; A-36 requires exact CAS
  fencing without losing the owner.
- Required change: make cancellation and terminal finalization linearizable for
  this exact bound generation. Either synchronize the proven terminal binding
  when cancellation advances the action, or let the exact finalizer consume a
  separately proven terminal-after-cancellation generation. Preserve the
  immutable plan, decision, target, manifest, entry, and single-winner CAS
  gates. Do not release the action to generic mutation work.
- Required proof: exercise cancellation immediately before and after terminal
  janitor commit for `succeeded`, `failed`, `held`, and `cancelled`, including
  two-connection cancellation/finalizer races. Every ordering must have one
  durable terminal action result, no duplicate dispatch, and truthful retained
  effects.
- Disposition: `current_blocker`.

### P1: contradictory or duplicate success evidence is accepted as `applied`

- Location: the first success branches in
  `internal/storage/query.sql:618-623` and `:644-648`, with generated SQLC at
  `internal/storage/sqlc/query.sql.go:2090-2135`.
- Evidence: a terminal `succeeded` janitor is finalized as action `succeeded`
  whenever the first JSON value at `$.outcome` equals `applied`. No deletion
  count, effect record, field type, alias agreement, or top-level key
  cardinality is checked. The raw ambiguous JSON is then copied to the action
  outcome.
- Independent reproductions all finalized as `succeeded` with outcome
  `applied`:

  ```json
  {"outcome":"applied","deletedObjects":0}
  {"outcome":"applied","deleted_objects":0}
  {"outcome":"applied","deletedObjects":"one"}
  {"outcome":"applied","deletedObjects":0,"deleted_objects":1}
  ```

  SQLite also accepted duplicate-key objects and the finalizer copied them into
  a terminal success, including:

  ```json
  {"outcome":"already_satisfied","deletedObjects":0,"deletedObjects":1}
  {"outcome":"already_satisfied","outcome":"applied","deletedObjects":0}
  {"outcome":"applied","outcome":"already_satisfied","deletedObjects":1}
  ```

- Failure mode: the durable action and UI can report an irreversible purge as
  applied even when its own count says zero, has the wrong type, conflicts with
  its alias, or contains two incompatible outcome values. Duplicate keys are
  valid to SQLite's JSON parser, and `json_extract` selecting one value does not
  make the evidence unambiguous.
- Contract: the action state model in `data-and-recovery.md:48-55` permits
  `succeeded` only when desired state is proven. Its transaction protocol at
  `:68-76` requires successful effects and read-back evidence. I-05 forbids
  ambiguous evidence from becoming a positive claim; I-06 reserves
  `already_satisfied` for zero new effects. `http-api.md:118-123` permits
  `applied|already_satisfied` only when desired-state evidence supports success
  and forbids terminal labels from hiding effects.
- Required change: validate one canonical, unambiguous terminal outcome shape
  before copying it. Reject duplicate recognized keys. If deletion/effect
  counts are present, require numeric integral values and agreement between
  supported aliases. `applied` must have verified positive effect evidence from
  the outcome or durable effect rows; `already_satisfied` must have no positive
  effects. Any missing, malformed, zero-applied, positive-already-satisfied, or
  contradictory evidence must finalize as `needs_review` with outcome
  `unknown`.
- Required proof: cover both success labels with absent, zero, positive,
  negative, fractional, string, null, boolean, conflicting-alias, and duplicate
  recognized fields. Assert final action state/outcome, preserved valid effect
  evidence, exact one-time CAS, and no second dispatch.
- Disposition: `current_blocker`.

## Round-nine finding disposition

| Round-nine finding | Round-ten result |
| --- | --- |
| Initial-dispatch terminal crash has no exact finalization path | Closed for the ordinary no-cancellation boundary and all four terminal janitor states. The finalizer now accepts the exact running action generation, clears its lease, advances once, and rejects stale repetition. The cancellation race above remains open. |
| Applied purge is rewritten as already satisfied | Closed for coherent `{"outcome":"applied","deletedObjects":1}` evidence. It is preserved byte-for-byte. Validation of contradictory and ambiguous applied evidence remains open in the second P1. |

## Preserved invariants

- Ordinary initial-dispatch terminal success, failure, hold, and cancellation
  finalize the exact action generation once. Repeated or stale calls return no
  row and do not dispatch another purge.
- Coherent applied outcomes retain their effect evidence. Coherent
  already-satisfied outcomes with absent or zero count fields remain
  already-satisfied. Missing outcome and a positive already-satisfied count
  become `needs_review` with outcome `unknown`.
- Generic startup recovery, lease recovery, due listing, and generic claim stay
  fenced for approval-bound janitors in every supported state.
- The finalizer still binds exact action ID/version, approval-bound janitor ID,
  ready plan/revision/digest, approving decision, immutable `fs.delete` target,
  nonempty equal manifests, terminal janitor generation, terminal trash entry,
  and inactive entry operation.
- Prior action-first and janitor-first recovery, concurrent finalizer CAS,
  cancellation before terminal completion, deadline handling, stale plan/entry
  generations, migration downgrade/re-upgrade, immutable approval scope,
  canonical process lock, JSON/path constraints, trash safety, and close/restart
  regressions remain green.

## Independent checks

All executable checks used synthetic SQLite fixtures and no live service,
credential, media payload, external mutation, release, or deployment.

| Command or scenario | Result |
| --- | --- |
| Exact product/handoff diff, round-nine receipt, frozen specs, task and acceptance cases | Inspected independently. Product commit changes only the four assigned storage/generated-test files. |
| Scoped diff/snapshot, migration comparison, `git diff --check`, owned gofmt, public path/credential scan | Passed. Migrations and `sqlc.yaml` are unchanged. |
| SQLC generation in disposable archive plus byte comparison | Passed; regenerated `query.sql.go` is byte-identical to the product. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage/...` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=240s ./internal/storage/...` | Passed in 124.295 seconds. |
| Focused round 4/5/6/7/8/9/10 recovery, cancellation, deadline, queue, trash, janitor, and migration suite, `-count=10` | Passed in 41.926 seconds. |
| `GOWORK=off go vet -mod=readonly ./internal/storage/...` | Passed. |
| Full root test and vet | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed; storage completed in 111.864 seconds. |
| Cancellation-after-terminal adversarial probe, `-count=5` | Failed deterministically with `sql.ErrNoRows` as described in P1. |
| Contradictory applied and duplicate-key outcome probes, `-count=5` | Failed deterministically; every adversarial payload was copied into a terminal `succeeded` action. |
| Generation, API, architecture, planning, lint, and guardrail checks | Passed; Vacuum quality 100/100, planning reports 38 tasks and 60 acceptance cases. |
| Root module verify/tidy and compile-only check | Passed. |
| Tools module test, vet, verify, and tidy diff | Passed. |
| UI module test, vet, and verify | Passed; generated client package has no tests. |
| UI module tidy diff | Reports removal of existing future dependency pins; D-01 changes no UI or module file. |
| Darwin host and Linux amd64/arm64 CGO-free compile-only checks | Passed. |

## Acceptance contribution assessment

- A-32: not accepted. Ordinary initial terminal recovery succeeds, but a valid
  cancellation ordering can still strand the durable action after the effect.
- A-36: generic workers and normal finalization remain fenced, but the terminal
  cancellation race loses the exact completion generation.
- A-43: accepted for the reviewed migration contribution. Migrations are
  unchanged and the prior downgrade/re-upgrade fencing suite remains green.
- A-45: generation, tests, race, vet, module, guardrail, planning,
  architecture, SQLC, and cross-compile gates pass.

No product or `docs/execution/state.json` file was modified by this review.
