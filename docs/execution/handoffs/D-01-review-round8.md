# D-01 independent review, round eight

## Decision

`changes_requested`.

Round eight closes the three round-seven forward-recovery findings. Populated
v7 states upgrade safely, cancellation and expired deadlines retain the exact
read-only reconciliation claim, and active approval-bound janitor recovery
keeps the action fenced from generic workers. Repeated crash and concurrent
claim regressions pass. Two P1 crash boundaries remain. A terminal janitor no
longer excludes its still-reconciling action from generic mutation claims, and
the v9 down migration recreates the v8 reconciliation trigger without its
action-fencing update.

## Review identity and boundary

- Release stage: `pre-v0.1.0`, target release `v0.0.1`.
- Reviewed product commit:
  `5941e92cd9b50b6d1f32f1397025bda3fc8f6ddd`.
- Product parent: `b93a7ba3c5c05d400f8db3ca8eee0be98232857d`.
- Round-eight implementation handoff commit:
  `4e8f5fe150ea50eaace362b211aaeb9910d44be4`.
- Round-seven review receipt commit:
  `39b66cd1d28342543171f90b0d9fccd9a8d5a571`.
- Reviewed paths: `internal/storage/`, `migrations/`, and `sqlc.yaml`.
- Scoped product diff identity:
  `d21f19a8b04fc807760cc5d04f387adfb0b2ba8b22aa5ad588305bf47f5327f0`.
- Scoped product snapshot:
  `1d4a9196cc9c7c6e583dd1d4e0eacbaeae68ccaa902b0412a0487f16831e5984`.
- Migration `000009` blobs: up
  `cdc4cb66829b2f334de9a70143df6f8b54f45df3`, down
  `56bfb4999ce7585e8497b81af3ae36b73b886675`.
- Reviewer: `/root/d01_reviewer`, independent of the round-eight worker.
- Checks ran in a clean detached reviewer-owned worktree at the exact product
  commit. Adversarial tests ran in a disposable Git archive and were not added
  to the product.
- This review changes only this receipt. It does not change execution state,
  product paths, credentials, live services, media, release state or
  deployment.

Migrations `000001` through `000008` are byte-identical to the product parent.
Generated SQLC is clean against `query.sql` and the schema through `000009`.

## Findings

### P1: a terminal janitor releases the approved action to generic mutation work

- Location: generic action recovery and claim at
  `internal/storage/query.sql:502-520` and `:566-614`; janitor finalization at
  `:912-918` and `migrations/000005_storage_safety.up.sql:755-773`.
- Evidence: the new generic action exclusion checks only approval-bound
  janitors in `queued`, `running`, `waiting_dependency`, or `reconciling`.
  `UpdateJanitorRecord` can commit `succeeded`, `failed`, `held`, or
  `cancelled` and atomically finalize the trash entry, but it does not finalize
  or permanently fence the coupled action. If the process dies before the
  separate `UpdateActionRunOutcome`, the action remains `reconciling` with the
  old worker lease. Once that lease expires, `RecoverExpiredActionRuns` accepts
  it because the janitor is terminal, and `ClaimActionRun` accepts it for a
  generic worker for the same reason.
- Independent reproduction:
  1. Claim an exact approved early purge and recover it into read-only
     reconciliation.
  2. Finish the janitor as `succeeded`; the trash entry becomes `purged`.
  3. Simulate a crash before the action outcome update.
  4. Recover the expired action lease and call generic `ClaimActionRun`.
- Observed result from
  `TestReviewerTerminalJanitorDoesNotReleaseApprovedActionToGenericMutation`:

  ```text
  resolved approved purge became generic mutation work: <nil>
  ```

  The nil error is a successful generic claim of an already-resolved approved
  purge.
- Failure mode: a crash in the ordinary two-journal completion window can
  convert a read-only reconciliation into a new mutation dispatch. A succeeded
  purge may be dispatched again; failed/held outcomes can likewise bypass the
  dedicated approval-bound path.
- Contract: `data-and-recovery.md:47-55` separates reconciliation from
  mutation; lines 68-82 require CAS/lease recovery without replay; lines 92-96
  require restart-safe durable work. A-14 requires already-materialized state
  to produce zero extra effects, and A-36 requires conflicting execution to be
  fenced.
- Required change: keep every action referenced by an approval-bearing purge
  janitor out of generic selection, claiming and independent recovery until the
  coupled action is terminal. Prefer an atomic terminal transition for action,
  janitor and trash entry, or add an exact terminal read-back/finalization claim
  that cannot dispatch mutation.
- Required proof: crash after each janitor terminal outcome and before action
  finalization. After lease expiry and restart, generic list/claim must return
  no work, while an exact read-only/finalization path reaches the corresponding
  action terminal state with zero new delete dispatch.
- Disposition: `current_blocker`.

### P1: the v9 down migration restores the reconciliation trigger without its action fence

- Location: `migrations/000009_storage_recovery_boundaries.down.sql:55-147`,
  compared with the v8 trigger at
  `migrations/000008_storage_recovery_concurrency.up.sql:108-124`.
- Evidence: the down migration says it restores the round-seven trigger, but
  after the validation `SELECT` it ends the trigger immediately. It omits the
  v8 `UPDATE action_runs` that copies the reconciliation worker/lease, advances
  the action version, and aborts if the fence is lost. The existing committed
  downgrade test steps from v9 down two versions and only inspects held
  pre-existing work; it neither stops at v8 nor creates and reclaims new valid
  work under the downgraded trigger.
- Independent reproduction:
  1. Create an empty v9 database and migrate it down exactly one step to v8.
  2. Create and initially claim a valid exact approved purge under v8.
  3. Model the supported action-first recovery and recover the janitor/trash
     generation.
  4. Claim exact read-only reconciliation.
- Observed result from
  `TestReviewerRound9DownRestoresRound8ReconciliationFence`:

  ```text
  downgraded round-eight trigger did not fence action:
  action state=reconciling version=3 claimed_by=NULL
  janitor state=running version=4 claimed_by=review-down-reconciler
  ```

- Failure mode: after a one-step rollback, janitor and trash entry ownership
  advances while the action remains unleased at the prior generation. The
  downgraded v8 generic claim contract can race that action, recreating the
  concurrent ownership defect that migration `000008` was created to fix.
- Contract: D-01 requires ordered reversible migrations. A-36 requires the
  action/janitor/trash claim fence, and A-43 requires a consistent supported
  down/up schema path.
- Required change: reproduce the complete byte-for-byte v8 trigger semantics
  in `000009.down.sql`, including the action update and `changes()` abort, after
  holding v9-only active shapes. Do not restore the earlier round-six trigger.
- Required proof: migrate v9 down exactly one step, create a fresh v8 approved
  purge, recover and claim reconciliation, then assert action/janitor/trash
  share one version and worker lease and a competing generic action claim
  fails. Re-upgrade that populated state to v9 and repeat the recovery gate.
- Disposition: `current_blocker`.

## Round-seven finding disposition

| Round-seven finding | Round-eight result |
| --- | --- |
| Crashed v7 reconciler stranded by upgrade | Closed for the three supported populated shapes: in-flight claim, action-recovery-first, and janitor-recovery-first. Migration repair produces coherent reclaimable generations. |
| Cancellation/deadline strand read-only reconciliation | Closed while the approval-bound operation is active. Cancellation synchronizes all three versions; deadline and cancellation block generic mutation while the dedicated read-only claim remains available. |
| Split expired-lease recovery reopens the action race | Closed while the janitor is active. Independent action recovery excludes the active approved janitor; janitor recovery advances and unleases all three records in one SQLite statement. |

## Additional invariant review

- The v7 repair only recognizes exact ready `fs.delete` plans, immutable
  revision/digest, approving decision, pre-approval target, matching non-empty
  JSON manifests, exact trash entry and supported version/lease shapes.
  Unrecognized active approval rows are moved to `needs_review`/`held`.
- Cancellation synchronization requires exactly one active approval-bound
  purge janitor and exact previous generations, then advances action, janitor
  and trash entry in the same statement.
- Dedicated reconciliation no longer applies cancellation or deadline as
  mutation eligibility gates. It still requires exact plan, decision, target,
  manifest, current generation, due time and an unleased action/trash entry.
- Generic action listing and claiming both enforce cancellation, deadline,
  due-time, lease, version and active approval-bound janitor guards. The first
  finding identifies the remaining terminal-state hole.
- Existing repeated-crash tests run against schema version 9 and maintain one
  action/janitor/trash generation across two recovery cycles. The committed
  separate-connection competitor test remains green.
- The `000009` forward and down migrations finish with foreign-key checks;
  forward populated migration, direct down/up, historical chronology,
  immutable target, JSON/path, queue, janitor, lock and restart tests pass.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Historical migration Git-blob comparison | Passed; `000001` through `000008` are unchanged from the product parent. |
| Scoped Git diff identity, `git diff --check`, owned gofmt check | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed on Darwin arm64. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/storage/...` | Passed in 91.707 seconds. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| Focused round 4/5/6/7/8, migration, queue, trash and janitor suite | Passed. |
| `./scripts/generate.sh --check` | Passed; generated SQLC is clean. |
| `./scripts/check-api.sh` | Passed; Vacuum quality 100/100. |
| Root, tools and UI `GOWORK=off go mod verify` | Passed. |
| Tools and UI module tests/vet; tools `go mod tidy -diff` | Passed. |
| Linux amd64 and arm64 CGO-free storage compile | Passed; produced statically linked ELF binaries. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases and local links resolve. |
| `python3 scripts/check-architecture.py` | Passed. |
| Public credential/private-path scan of D-01 paths | Passed; no finding. |
| Terminal-janitor crash probe | Failed the contract; generic mutation claim succeeded. |
| Exact one-step down-migration trigger probe | Failed the contract; janitor advanced without fencing the action. |

## Acceptance contribution assessment

- A-14: not accepted for this storage contribution. A resolved approved purge
  can re-enter generic mutation work after the terminal-janitor crash window.
- A-32: forward v7-to-v9 upgrade and active repeated-crash recovery are
  accepted; terminal completion recovery remains open.
- A-35: cancellation and deadline preserve active read-only reconciliation and
  prohibit generic mutation as required.
- A-36: active split recovery is fenced, but terminal janitor state and the v8
  downgrade trigger reopen conflicting generic execution.
- A-43: forward populated migration passes; one-step downgrade does not restore
  the advertised v8 trigger contract.
- A-45: generation, test, race, vet, module, planning, architecture and Linux
  compile gates pass.

No product or execution-state file was changed by this review.
