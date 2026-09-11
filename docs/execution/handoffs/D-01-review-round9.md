# D-01 independent review, round nine

## Decision

`changes_requested`.

Round nine closes both round-eight blockers: every approval-bound janitor state
now fences the associated action from generic recovery/list/claim, and the v9
down migration restores the complete v8 reconciliation action fence. The exact
terminal finalizer is CAS-safe under concurrent callers and works after
cancellation or deadline expiry. Two P1 completion defects remain. A crash
after an initial purge dispatch reaches a terminal janitor leaves the action
`running` without an exact recovery path. Successful finalization also rewrites
an `applied` janitor outcome as `already_satisfied`.

## Review identity and boundary

- Release stage: `pre-v0.1.0`, target release `v0.0.1`.
- Reviewed product commit:
  `eb2f8076f5aef8bfb33cb58f81ada53b7759a189`.
- Product parent: `29768b68238cc2023af7149592da7b0bd4fa192a`.
- Round-nine implementation handoff commit:
  `94228102c14bcaa6bec4199cd1b9feff018d13c0`.
- Round-eight review receipt commit:
  `d17f62313d5db71e37a0a9223418d5fed08ed49f`.
- Reviewed paths: `internal/storage/`, `migrations/`, and `sqlc.yaml`.
- Scoped product diff identity:
  `f6d17bc2ea9af0f435491b45d5d0ff4896446ac8d1cdc9443cff9423ba0370b3`.
- Scoped product snapshot:
  `a1a7256f4a58459a5048a984fdd000bd14be9ebbeb5177736012a6c5aa2a9506`.
- Migration `000009` down blob:
  `dc2a92ae70b89754f20dd2cbd1b01beb5d305bb8`.
- Reviewer: `/root/d01_reviewer`, independent of the round-nine worker.
- Checks ran in a clean detached reviewer-owned worktree at the exact product
  commit. Adversarial tests ran in a disposable Git archive and were not added
  to the product.
- This review changes only this receipt. It does not change execution state,
  product paths, credentials, live services, media, release state or
  deployment.

Migrations `000001` through `000008` and `000009` up are byte-identical to the
round-eight product. Generated SQLC is clean against `query.sql` and the schema
through `000009`.

## Findings

### P1: an initial-dispatch terminal crash has no exact action recovery path

- Location: `internal/storage/query.sql:483-500` and `:615-693`, especially
  the `action_runs.state = 'reconciling'` finalizer gate at line 639.
- Evidence: an initial `ClaimApprovedEarlyPurge` atomically places the action,
  janitor and trash entry in `running`/`running`/`purging`. When the purge
  finishes, `UpdateJanitorRecord` commits the janitor terminal state and the
  `janitor_records_finish_trash_entry` trigger commits the corresponding
  terminal trash state, but the action remains `running` until a separate
  write. If the process dies in that window, the corrected generic startup
  recovery deliberately excludes the approval-bound action. The new exact
  finalizer also excludes it because it accepts only `reconciling`.
- Independent reproduction:
  1. Create and claim an exact approved early purge; confirm the coupled action
     is `running` at version 2.
  2. Finish the janitor as `succeeded`; the janitor/trash entry advance to
     version 3 and the trash entry becomes `purged`.
  3. Simulate a crash before the action outcome write.
  4. Confirm `RecoverRunningActionRuns` returns no generic work, then call
     `FinalizeApprovedPurgeAction` with the exact action/janitor IDs and current
     action version.
- Observed result from
  `TestReviewerRound9InitialDispatchCrashCanFinalizeTerminalAction`:

  ```text
  exact terminal finalization after initial-dispatch crash: sql: no rows in result set
  ```

- Failure mode: every terminal janitor result can strand its action after the
  ordinary dispatch completion crash boundary. The action remains leased and
  `running`; it is correctly unavailable to generic mutation workers but has
  no exact durable completion route.
- Contract: `data-and-recovery.md:68-82` requires journaled dispatch, read-back
  completion and restart-safe recovery without blind replay; lines 92-96
  require queued work and outcomes to survive restart. A-32 covers recovery,
  and A-36 requires exact CAS fencing without conflicting ownership.
- Required change: let the exact terminal finalization query safely consume
  the supported terminal-janitor/current-action generation when the bound
  action is still `running`, as well as the recovered `reconciling` shape. Keep
  generic recovery/list/claim excluded and retain exact plan, decision, target,
  manifest, entry and version checks.
- Required proof: for each janitor terminal result (`succeeded`, `failed`,
  `held`, `cancelled`), crash after the initial-dispatch janitor/trash terminal
  transaction and before the action write. After restart, generic action work
  remains empty and exact finalization reaches the corresponding terminal
  action state exactly once with no new purge dispatch.
- Disposition: `current_blocker`.

### P1: terminal finalization rewrites an applied deletion as already satisfied

- Location: `internal/storage/query.sql:629-633` and generated SQLC at
  `internal/storage/sqlc/query.sql.go:2101-2105`.
- Evidence: every `succeeded` janitor is assigned the constant action outcome
  `{"outcome":"already_satisfied","source":"approved_purge_janitor"}`.
  The query does not inspect or preserve the terminal janitor outcome. A
  successfully applied purge recorded as
  `{"outcome":"applied","deletedObjects":1}` is therefore relabeled as
  `already_satisfied` when the action journal is finalized.
- Independent reproduction:
  1. Recover and claim an exact approved purge for read-only reconciliation.
  2. Finish its janitor as `succeeded` with an `applied` outcome and a synthetic
     deleted-object count.
  3. Run the exact terminal finalizer and inspect the action outcome.
- Observed result from
  `TestReviewerRound9FinalizerDoesNotRewriteAppliedAsAlreadySatisfied`:

  ```text
  janitor applied outcome was rewritten as "already_satisfied"
  ```

- Failure mode: the durable action and UI can claim that an irreversible purge
  produced zero external effects even when its own terminal janitor journal
  says the deletion was applied. This destroys the distinction needed to show
  completed effects honestly after a crash.
- Contract: R-08 and `data-and-recovery.md:68-75` define
  `already_satisfied` as the pre-dispatch zero-effect path. I-06 requires an
  already-satisfied action to produce zero new external effects.
  `http-api.md:119-123` requires `applied|already_satisfied` only from verified
  effects and forbids terminal labels from hiding effects.
- Required change: derive the finalized success outcome from verified durable
  janitor/effect evidence. Preserve `applied` when the journal records an
  applied purge, use `already_satisfied` only when zero new effects are proven,
  and do not invent either outcome when the evidence is incomplete.
- Required proof: finalize synthetic terminal journals for both `applied` and
  `already_satisfied`, including crash/restart and cancellation/deadline cases;
  assert the action outcome and recorded effect counts remain truthful and a
  repeated/stale finalizer changes nothing.
- Disposition: `current_blocker`.

## Round-eight finding disposition

| Round-eight finding | Round-nine result |
| --- | --- |
| Terminal janitor releases action to generic mutation work | Closed for all eight janitor states. Startup and lease recovery, due listing and generic claim remain fenced through exact terminal finalization. Independent startup and two-connection probes pass. |
| v9 down migration omits v8 action fence | Closed. The restored trigger includes the exact action CAS and `changes()` abort. It is semantically identical to the v8 trigger except for diagnostic text; populated one-step down/re-up tests pass. |

## Additional invariant review

- Terminal approved-purge bindings exclude the complete constrained janitor
  enum from `RecoverRunningActionRuns`, `RecoverExpiredActionRuns`,
  `ListDueActionRuns` and `ClaimActionRun`.
- `FinalizeApprovedPurgeAction` checks action version/state and the exact ready
  `fs.delete` plan/revision/digest, approving decision, immutable target,
  nonempty matching manifests, terminal janitor generation and terminal trash
  state in one update. Concurrent finalizers produce one success and one stale
  CAS result.
- Cancellation generation synchronization remains coherent. Independent probes
  confirm exact read-only finalization works after a cancellation request and
  after deadline expiry while generic mutation stays blocked.
- Split action-first and janitor-first lease recovery, current-version repeated
  crash recovery, separate-connection competitors and stale claims remain
  covered by the round 7/8/9 focused suite and pass under the race detector.
- The one-step v9-to-v8 down migration holds active v9-only work before
  restoring the v8 trigger. The restored trigger validates the exact live
  binding, updates the associated action lease/version and aborts the whole
  janitor claim if that action CAS loses. The down migration ends with a
  foreign-key check.
- Historical migration chronology, populated upgrade/down/re-upgrade, source
  scope/coverage constraints, immutable plans/decisions/targets, queue and
  janitor CAS, trash safety, JSON/path constraints, canonical lock behavior and
  close/restart tests remain green.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Historical migration Git-blob comparison | Passed; `000001` through `000008` and `000009` up are unchanged from the round-eight product. |
| v8-up versus v9-down reconciliation trigger comparison | Passed semantically; only the diagnostic error string differs. The action update and lost-fence abort are present. |
| Scoped Git diff identity, product snapshot, `git diff --check`, owned gofmt check | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed on Darwin arm64. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/storage/...` | Passed in 94.473 seconds. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| Focused round 4/5/6/7/8/9 migration, recovery, cancellation, deadline, queue, trash and janitor suite | Passed. |
| Independent startup/cancellation/deadline/concurrent-finalizer probes | Passed; concurrent finalization produced exactly one winner. |
| Initial-dispatch terminal-crash probe | Failed the contract; exact finalizer returned `sql.ErrNoRows`. |
| Applied-outcome preservation probe | Failed the contract; `applied` became `already_satisfied`. |
| `./scripts/generate.sh --check` | Passed; generated SQLC is clean. |
| `./scripts/check-api.sh` | Passed; Vacuum quality 100/100. |
| `./scripts/check-guardrails.sh` | Passed. |
| Root, tools and UI `GOWORK=off go mod verify` | Passed. |
| Tools and UI module tests/vet; tools `go mod tidy -diff` | Passed. |
| Darwin and Linux amd64/arm64 CGO-free compile-only checks | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases and local links resolve. |
| `python3 scripts/check-architecture.py` | Passed. |
| Public credential/private-path scan of D-01 paths | Passed; no finding. |

## Acceptance contribution assessment

- A-32: not accepted for this correction. Recovered terminal reconciliation is
  safe, but the ordinary initial-dispatch terminal crash has no completion
  path.
- A-36: generic mutation and concurrent finalization are fenced correctly; the
  initial-dispatch terminal state is safely fenced but stranded.
- A-43: accepted for the reviewed migration contribution. Populated forward,
  one-step down and re-upgrade checks preserve the supported v8/v9 fencing.
- A-45: generation, test, race, vet, module, guardrail, planning, architecture
  and cross-compile gates pass.

No product or execution-state file was changed by this review.
