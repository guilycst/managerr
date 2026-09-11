# D-01 independent review, round five

## Decision

`changes_requested`.

Round five closes the direct v4 approval, direct v5 downgrade, quarantine
collision, fresh target-ordering and accepted-after-preview-expiry findings from
round four. No P0 finding was found. Two P1 findings remain: a populated v5
target created after approval is still executable after the v6 upgrade, and a
valid v6 approved purge cannot reacquire a lease after startup recovery.

## Review identity and boundary

- Release stage: `pre-v0.1.0`, target release `v0.0.1`.
- Reviewed product commit:
  `c688f2cc5fcd59515943fb6e5c4baca361dca3b6`.
- Product parent: `563afd8ca729977968223296fd2e0d5bb59e6e5d`.
- Round-five implementation handoff commit:
  `8c2d76cc4927af04a0d6837d0be8e12fbb97518c`.
- Round-four receipt:
  `ebae3157302c046b8f5497492064400c28333268`.
- Reviewed paths: `internal/storage/`, `migrations/`, and `sqlc.yaml`.
- Scoped product diff identity:
  `f7f9b4ec962e8d69dbe83ddda43ac50d7f969504f7fb78eef92861b0eb044636`.
- Reviewer: `/root/d01_reviewer`, independent of the round-five worker.
- Checks ran in a detached reviewer-owned worktree at the exact product commit.
  Its local path is omitted from this public receipt.
- Producer conclusions were not accepted as evidence. Git objects, source,
  generated output, synthetic tests and direct SQLite probes supplied the
  evidence below.
- The shared checkout remained outside the review worktree. No product path,
  execution state, live stack, credential, private inventory, upstream system,
  media file, release or deployment was changed.

The `000001`, `000003` and `000004` migration pairs remain byte-identical to
round four. Their Git blobs are:

| Migration | Git blob |
| --- | --- |
| `000001_initial.up.sql` | `ce05d9deb8317af700f191f1ac227b4e2a922b5b` |
| `000001_initial.down.sql` | `bedf519dc741bdef9c6aa2143b793e2970fd18f8` |
| `000003_storage_contract.up.sql` | `6877149c012b60de883138f81d401705b457c6ea` |
| `000003_storage_contract.down.sql` | `27b624547cc12afd6c8acd9ababaac48d7e88319` |
| `000004_storage_compatibility.up.sql` | `3983edd599ac298ba62230787b121ddc70e6b429` |
| `000004_storage_compatibility.down.sql` | `47d8492f958ef51d8a438e22d868deeb5be2d81a` |

The revised 000005 and new 000006 embedded files match the hashes frozen by the
round-five migration-history test.

## Findings

### P1: populated v5 post-approval targets remain executable after upgrade

Evidence: `migrations/000006_approval_safety.up.sql:15-59` neutralizes a legacy
janitor only when approval fields have already been attached to that janitor.
A v5 target that was created after an immutable approval but has not yet been
claimed leaves all janitor approval fields null, so this migration does not
mark it held or move its action to `needs_review`.

The new insert trigger at migration lines 69-105 correctly prevents this order
for fresh v6 writes. It cannot validate a target and decision that already
exist. The claim trigger at lines 280-288 checks the target, decision and action
timestamps independently against preview expiry, but never proves
`target.created_at <= review_decisions.created_at`.

An independent fixture created a schema-v5 plan and approval at
`00:00:30`, attached its exact target at `00:00:40`, and migrated to v6. Both
timestamps preceded the `00:01:00` preview expiry. The v6 exact claim accepted
the old post-approval choice:

```text
populated-post-approval=claim-error:false|running|purging|running
```

The final fields are janitor, trash-entry and action-run state.

Failure mode: an existing v5 database can permanently delete a trash entry that
was selected only after the immutable approval. Fresh v6 validation is correct,
but the supported populated upgrade leaves the round-four safety defect active.

Contract: `data-and-recovery.md:24-30` binds a decision to immutable intended
objects. Lines 63-74 require approval-scope revalidation before mutation.
`http-api.md:63-67,81-84` requires review of the exact revision before an action
run exists, and lines 144-146 require an exact trash manifest and explicit
irreversible delete intent.

Required change: preflight every populated v5 `early_purge_plan_targets` row
against its existing approve decision. A target created after its decision, or
whose chronology cannot be proved, must be held and its action moved to
`needs_review` before the v6 claim trigger is installed. The claim predicate
should also require target creation no later than the decision. Add a v5 fixture
with a queued janitor, action and post-approval target, then prove upgrade makes
it non-executable.

Disposition: `current_blocker`.

### P1: a valid approved purge is stranded after startup recovery

Evidence: the first exact v6 claim attaches immutable approval metadata to the
janitor. `RecoverRunningJanitorRecords` at
`internal/storage/query.sql:733-740` then correctly moves it to `reconciling`
and releases its lease after restart. `ListDueJanitorRecords` includes that row,
but `ClaimJanitorRecord` at lines 758-767 invokes the new generic-claim guard.

That guard, `migrations/000006_approval_safety.up.sql:212-227`, aborts every
transition back to `running` when any old approval field is present. The strict
approved trigger at lines 232-257 cannot provide the recovery route: it runs
only when `OLD.approval_plan_id IS NULL`, and its action CAS requires the action
run to be `queued`. Startup recovery has already made that action
`reconciling`.

An independent exact-target fixture claimed a valid purge, closed and reopened
the store, then ran the existing action and janitor startup-recovery queries:

```text
initial-claim=running|purging|running|janitor-version:2
after-recovery=reconciling|purging|reconciling|janitor-version:3
reclaims=generic-error:true|approved-error:true|reconciling|purging|reconciling
```

Both generated claim methods rejected the recovered row. It remains visible as
due work but cannot acquire a worker lease for read-only reconciliation or safe
continuation.

Failure mode: any process exit after the first approved claim permanently
strands a legitimate purge in `reconciling`, with its trash entry still
`purging`. Cancellation, observation and operator retry cannot use the only
janitor claim primitives to resolve it.

Contract: `data-and-recovery.md:47-55` defines reconciling as read-only evidence
collection followed by a safe resolution. Lines 68-80 require a version/lease
claim and require running work to become reconciling after restart. Lines 92-96
require API/BFF restarts to preserve and resume durable work.

Required change: add a dedicated CAS for approval-bearing reconciling janitor
records, or extend the exact approved claim to validate the immutable existing
binding while claiming the action and janitor for reconciliation. It must not
dispatch mutation until read-only evidence proves the safe next state. Keep the
generic guard for incomplete legacy approvals. Add a claim, close/open,
startup-recovery, lease-reacquisition and resolution fixture for a valid v6
early purge.

Disposition: `current_blocker`.

## Round-four finding disposition

| Round-four finding | Round-five result |
| --- | --- |
| Purge target can be selected after approval | Closed for fresh v6 inserts. Not closed for a populated v5 queued target created after approval; it remains executable after upgrade. |
| Unsafe v4 and down-migrated claims bypass v5 gate | Closed for the covered legacy and direct-down paths. Approval-bearing records are held, action runs need review, and generic claim is blocked. |
| Quarantine down/re-up silently loses evidence | Closed. Downgrade retains immutability and an identity guard; a bypassed changed collision fails dirty before dropping either row, while an exact duplicate converges. |
| Dispatch reapplies preview expiry after approval | Closed. Target, approval and action creation are checked inside the preview window; an accepted action can claim later while its action deadline remains live. |

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Historical migration Git-blob comparison | Passed; 000001, 000003 and 000004 up/down blobs match round four, and 000001 matches the original implementation. |
| Scoped Git snapshot before and after review | Passed; product diff identity remained `f7f9b4ec962e8d69dbe83ddda43ac50d7f969504f7fb78eef92861b0eb044636`. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed on Go 1.27.1, Darwin arm64, including qBittorrent inventory at the exact product commit. |
| `GOWORK=off go test -mod=readonly -count=1 -race ./internal/storage/...` | Passed in 65.513 seconds. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage/... ./migrations` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 -run 'TestRound5' ./internal/storage` | Passed. |
| `./scripts/generate.sh --check` and generated SQLC diff | Passed; generated output matches migrations and queries. |
| `GOWORK=off go mod verify` | Passed. |
| Linux CGO-free compile for `amd64` and `arm64` using `-run '^$' -exec=true` | Passed. |
| Tools module test, vet and tidy-diff | Passed. |
| UI module verification | `go mod verify` passed. The module has no Go packages, so `go test ./...` reports no matched packages and exits 1; this is not a useful gate. |
| Fresh target-before-approval and post-approval rejection tests | Passed. |
| Legacy v4 approval upgrade, v6-to-v4-to-v6 and direct v5 down/up tests | Passed for the committed fixtures; unsafe work becomes held/needs-review. |
| Quarantine changed-collision and exact-duplicate down/up tests | Passed; changed evidence fails dirty and exact evidence converges. |
| Accepted action after preview expiry plus late target/decision/action negatives | Passed. |
| Populated v5 queued post-approval target probe | Failed the contract; v6 claimed the target. |
| Valid v6 claim, restart, recovery and reacquisition probe | Failed the contract; both available claims rejected the recovered work. |
| `python3 scripts/check_planning.py` | Passed; 38 tasks, 60 acceptance cases and local links are valid. |
| D-01 diff check and public credential/path scan | Passed. |

Root `go mod tidy -diff` retains the coordinator/later-consumer delta: it removes
the not-yet-consumed pinned oapi runtime and adds all-tag test/transitive module
metadata. Root read-only tests, vet and module verification pass. This module
metadata item does not change the two D-01 findings.

## Acceptance contribution assessment

- A-32: fresh approval ordering and accepted-after-expiry dispatch pass. A valid
  claimed operation cannot recover a lease after restart.
- A-36: legacy approval-bearing rows are excluded from generic mutation claims,
  but the same guard also excludes complete current approvals from their
  required reconciliation claim.
- A-43: direct upgrade/down, dirty refusal, quarantine collision handling and
  foreign-key checks pass. The populated v5 target chronology is not migrated
  safely.
- A-45: SQLC generation, root read-only module checks, tools checks and Linux
  builds pass for the D-01 slice.

## Next round

Neutralize populated v5 post-approval targets before installing the v6 claim,
and add a lease-bearing recovery path for complete approval-bound janitor work.
Keep incomplete legacy approvals held and require new review. Add both direct
probes above to the populated/restart suite, then rerun the same migration,
foreign-key, race, generation, module and cross-platform checks. This review
changed only this receipt.
