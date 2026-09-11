# D-01 independent review, round six

## Decision

`changes_requested`.

Round six closes the populated-v5 approval chronology defect from round five,
and its dedicated claim can reacquire a valid approved purge once after startup
recovery. No P0 finding was found. Two P1 recovery/concurrency findings remain:
the claim cannot survive a second reconciliation-worker crash, and it does not
reserve the coupled action run from another worker.

## Review identity and boundary

- Release stage: `pre-v0.1.0`, target release `v0.0.1`.
- Reviewed product commit:
  `5829da918aecad00346075b9f1cb29612f196e0d`.
- Product parent: `93197292c5c186faa180edc64ecf3c522ffdeb7b`.
- Round-six implementation handoff commit:
  `9cf358c7c31bef2bb828f528356f8bd71a220b8c`.
- Round-five receipt commit:
  `d6837ed31f09c26e797a6e5d2b24982c5b6cbb61`.
- Reviewed paths: `internal/storage/`, `migrations/`, and `sqlc.yaml`.
- Scoped product diff identity:
  `55dad3942a8951cf2040745cfc302d47b086b8d3ec42b75152578c5d695259ee`.
- Reviewer: `/root/d01_reviewer`, independent of the round-six worker.
- Checks ran in a detached reviewer-owned worktree at the exact product commit.
  Its local path is omitted from this public receipt.
- Producer conclusions were not accepted as evidence. Git objects, source,
  generated output, synthetic tests and direct SQLite probes supplied the
  evidence below.
- The shared checkout remained outside the review worktree. This review changes
  only this receipt. It did not change product paths, execution state, unrelated
  adapter work, live services, media, credentials, release state or deployment.

The round-six product adds only migration `000007`; migrations `000001` through
`000006` are byte-identical to the product parent. The frozen migration blobs
are:

| Migration | Git blob |
| --- | --- |
| `000001_initial.up.sql` | `ce05d9deb8317af700f191f1ac227b4e2a922b5b` |
| `000001_initial.down.sql` | `bedf519dc741bdef9c6aa2143b793e2970fd18f8` |
| `000003_storage_contract.up.sql` | `6877149c012b60de883138f81d401705b457c6ea` |
| `000003_storage_contract.down.sql` | `27b624547cc12afd6c8acd9ababaac48d7e88319` |
| `000004_storage_compatibility.up.sql` | `3983edd599ac298ba62230787b121ddc70e6b429` |
| `000004_storage_compatibility.down.sql` | `47d8492f958ef51d8a438e22d868deeb5be2d81a` |
| `000005_storage_safety.up.sql` | `d0e5bec935b9e7e67004288ff8a7ab9b02cfa5ad` |
| `000005_storage_safety.down.sql` | `0b9a2eb633a739be651a7f25c1ca288c5e093c78` |
| `000006_approval_safety.up.sql` | `ae8518377f2918bd6b21081568a76ec5971cfeb3` |
| `000006_approval_safety.down.sql` | `82f36a4b79c8c9f4a0f6eae505928d27e3244f8c` |

## Findings

### P1: a second crash permanently strands approved reconciliation

Evidence: `ClaimApprovedEarlyPurgeReconciliation` requires the trash entry to
have exactly `approved_entry_version + 2` at
`internal/storage/query.sql:865-874`. The first startup recovery reaches that
version and the committed test exercises exactly that cycle. Claiming the
janitor increments the entry version through
`migrations/000005_storage_safety.up.sql:713-741`; recovering the newly running
janitor after another crash releases the entry lease and increments its version
again at lines 743-753. The immutable approval-time version can therefore no
longer satisfy the query.

The round-six trigger anticipates only the first claim's before/after versions,
`approved_entry_version + 2` or `+ 3`, at
`migrations/000007_storage_recovery_safety.up.sql:218-225`. It does not make the
SQLC predicate restart-stable. The committed fixture at
`internal/storage/compatibility_round6_test.go:104-228` claims, recovers once,
reclaims once and then finishes; it never crashes the reconciliation worker.

Independent reproduction:

1. Create a valid exact `fs.delete` plan, pre-approval target, approve decision,
   queued action and purge janitor.
2. Perform the initial approved claim.
3. Recover the running action and janitor as startup does.
4. Reacquire with `ClaimApprovedEarlyPurgeReconciliation`; this succeeds.
5. Simulate another process exit by calling `RecoverRunningJanitorRecords`.
6. Re-run the exact reconciliation claim with the returned janitor version.

Observed result:

```text
second reconciliation claim failed after another crash: sql: no rows in result set entryVersion=5 janitorVersion=5
```

Failure mode: a process exit during read-only reconciliation leaves a valid,
approved purge in `reconciling`/`purging` with no worker lease and no legal route
to reacquire one. This violates the explicit escape/retry model and can require
manual database intervention.

Contract: `data-and-recovery.md:47-55` defines reconciling as read-only evidence
collection with a safe resolution. Lines 68-82 require version/lease recovery,
make lease expiry a recovery boundary, and move running work to reconciliation
on startup. Lines 92-96 require restarts to preserve and resume durable work.

Required change: make the recovery CAS validate the current internally
consistent janitor/trash generation rather than a single offset from the
approval-time entry version. Preserve the immutable approval version as scope
evidence, while allowing any number of claim/recovery cycles. Add a committed
fixture with at least two reconciliation crashes and prove each live worker can
reacquire before final resolution.

Disposition: `current_blocker`.

### P1: janitor reconciliation leaves the coupled action claimable by another worker

Evidence: the dedicated claim intentionally requires the action run to remain
`reconciling`, unclaimed and unleased at
`internal/storage/query.sql:832-845`, then updates only `janitor_records` at
lines 812-831. The trigger repeats that unowned-action requirement at
`migrations/000007_storage_recovery_safety.up.sql:226-235`, but neither statement
reserves the action for the janitor worker.

At the same time, `ListDueActionRuns` includes every unleased reconciling action
and `ClaimActionRun` changes it to `running` for a generic worker at
`internal/storage/query.sql:548-570`. The janitor/trash reconciliation lease
therefore does not establish exclusive ownership of the coupled operation.

Independent reproduction:

1. Create and initially claim the same valid approved purge fixture.
2. Recover the action and janitor after startup.
3. Reacquire the janitor/trash reconciliation lease as `janitor-worker`.
4. Before resolution, call generic `ClaimActionRun` for the associated action as
   `action-worker`, using its current version.

Observed result:

```text
claim=&{... State:running ... ClaimedBy:action-worker ... Version:4 ...} err=<nil>
```

The generic action claim succeeds while the exact janitor/trash reconciliation
lease is active. The two workers can now observe or advance one logical purge
through independent state machines. A subsequent action transition can also
invalidate the janitor trigger's expected reconciling state after the janitor
has already acquired its lease.

Failure mode: overlapping workers can concurrently own the same approved purge,
defeating the single-CAS dispatch/reconciliation boundary. The action worker may
treat `running` as executable work while the janitor worker believes it has the
exclusive read-only reconciliation lease.

Contract: `data-and-recovery.md:63-82` requires a version/lease claim before work,
read-only reconciliation after uncertain execution, and cancellation at the
same transactional dispatch boundary. A-36 requires local reservation/CAS to
prevent conflicting execution. `http-api.md:71-72` states that reconciliation
cannot authorize mutation and uncertain writes reconcile before retry.

Required change: atomically reserve the associated action when claiming the
janitor/trash reconciliation work, or exclude approval-bound purge actions from
generic action selection and claiming while their janitor recovery is active.
The reserved action must remain explicitly read-only until evidence resolves the
uncertain effect. Add a two-worker fixture proving the losing claim gets no row
and that cancellation/deadline resolution remains observable without mutation.

Disposition: `current_blocker`.

## Round-five finding disposition

| Round-five finding | Round-six result |
| --- | --- |
| Populated v5 post-approval targets remain executable after upgrade | Closed for the committed valid, malformed and post-approval fixtures. Migration 000007 joins target and approve decision by plan/revision, conservatively holds unprovable targets, janitors and trash, and adds a fresh chronology claim guard. |
| Valid approved purge is stranded after startup recovery | Partially closed. One startup recovery and one reconciliation lease reacquisition pass. A second crash strands the work, and the first reacquisition leaves the coupled action available to another worker. |

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Historical migration Git-blob comparison | Passed; `000001` through `000006` are unchanged from the round-six product parent. |
| Scoped Git diff identity and whitespace check | Passed; identity remained `55dad3942a8951cf2040745cfc302d47b086b8d3ec42b75152578c5d695259ee`, and `git diff --check` was clean. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed on Darwin arm64. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/storage/...` | Passed in 69.435 seconds. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| Focused committed `TestRound(4|5|6)` storage suite | Passed. |
| `./scripts/generate.sh --check` | Passed; generated SQLC matches the query and migrations. |
| `GOWORK=off go mod verify` | Passed for root, tools and UI modules. |
| Tools module test, vet and `go mod tidy -diff` | Passed. |
| UI module test/vet | Module verification and vet passed. It has no test-bearing Go package. |
| Linux amd64/arm64 CGO-free `go test -run '^$' -exec=true ./...` | Passed for both architectures. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases and local links are valid. |
| Public credential/private-path scan of D-01 paths | Passed; no match. |
| Populated v5 target chronology, incomplete approval and one-cycle restart fixtures | Passed. The unsafe target becomes held/needs-review; valid evidence reclaims once; incomplete evidence cannot claim. |
| Repeated reconciliation crash probe | Failed the contract; the second exact reclaim returns `sql.ErrNoRows` at entry/janitor version 5. |
| Competing action-worker probe | Failed the contract; generic `ClaimActionRun` acquires the associated action while the janitor/trash reconciliation lease is active. |

The new `000007` down migration neutralizes nonterminal approval-bound actions
and janitors before restoring the older trigger set, and its foreign-key check
passes the committed migration suite. No separate down-migration blocker was
found in this round. Passing committed and module checks do not close the two
missing recovery/concurrency cases above.

## Acceptance contribution assessment

- A-32: populated chronology quarantine and one recovery cycle pass. Repeated
  restart recovery remains broken, so durable approved work is not accepted.
- A-36: the dedicated janitor/trash CAS validates exact approval evidence, but
  it neither survives repeated recovery nor excludes a competing action claim.
- A-43: ordered up/down migrations, historical immutability, foreign-key checks
  and ordinary populated paths pass. The runtime state produced by repeated
  recovery has no legal continuation.
- A-45: SQLC generation, root/tools/UI module verification, vet, race checks and
  Linux builds pass for this slice.

## Next round

Make the reconciliation lease generation-safe across repeated crashes and give
one worker exclusive ownership of the coupled action, janitor and trash entry.
Commit the repeated-crash and competing-worker probes, then rerun the focused
migration/recovery suite, race detector, generation, module and Linux checks.
This review changed only this receipt.
