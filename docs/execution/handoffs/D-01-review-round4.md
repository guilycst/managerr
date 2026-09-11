# D-01 independent review, round four

## Decision

`changes_requested`.

Round four closes the active tracking, ordinary janitor-history, supported-kind,
new-claim cancellation/deadline and action-run CAS cases from round three. No P0
finding was found. Four P1 findings remain around the approval timeline and
populated migration safety: the exact purge target can be attached after
approval, v4 and downgraded approval claims bypass the v5 gate, a quarantine
collision silently loses newer evidence, and an accepted action is blocked by
the old plan-preview expiry.

## Review identity and boundary

- Release stage: `pre-v0.1.0`, target release `v0.0.1`.
- Reviewed product commit:
  `1a469f858b82b0f78b2d6d48c5e50999cdfdf00f`.
- Product base: `bc75aacbfda920272618d8e76118b4bc5f10df23`.
- Round-four handoff commit:
  `94e4c67e4b770424c5b84508671b636e811c2502`.
- Round-three receipt:
  `6c690d7c76c2979fa467edda8ac0a18663df45be`.
- Reviewed paths: `internal/storage/`, `migrations/`, and `sqlc.yaml`.
- Scoped product diff identity:
  `6f9b4716f083af48ef3ce628df4cf523fc38f4dbf9f27bec0266d819ae27621d`.
- Reviewer: `/root/d01_reviewer`, independent of the round-four worker.
- Checks ran in a detached reviewer-owned worktree at the exact product commit.
  Its local path is omitted from this public receipt.
- Producer conclusions were not accepted as evidence. Git objects, source,
  generated output, synthetic tests and direct SQLite probes supplied the
  evidence below.
- The shared checkout's untracked X-01 adapter and fixture files were not read,
  staged or modified. No product path, execution state, live stack, credential,
  private inventory, upstream system, media file, release or deployment was
  changed.

The historical migrations are byte-identical to round three. Their Git blobs
are:

| Migration | Git blob |
| --- | --- |
| `000001_initial.up.sql` | `ce05d9deb8317af700f191f1ac227b4e2a922b5b` |
| `000001_initial.down.sql` | `bedf519dc741bdef9c6aa2143b793e2970fd18f8` |
| `000003_storage_contract.up.sql` | `6877149c012b60de883138f81d401705b457c6ea` |
| `000003_storage_contract.down.sql` | `27b624547cc12afd6c8acd9ababaac48d7e88319` |
| `000004_storage_compatibility.up.sql` | `3983edd599ac298ba62230787b121ddc70e6b429` |
| `000004_storage_compatibility.down.sql` | `47d8492f958ef51d8a438e22d868deeb5be2d81a` |

The `000001` pair also matches the original D-01 implementation.

## Findings

### P1: the purge target can be selected after approval

Evidence: `migrations/000005_storage_safety.up.sql:473-510` makes the target
immutable after insertion and compares its manifest with the plan revision and
trash entry. It does not require the target to exist before a review decision,
and no review-decision trigger binds the target's entry ID and version to the
decision. `internal/storage/query.sql:390-423` exposes target and decision
creation as independent inserts.

An independent fixture created an `fs.delete` revision whose input contained no
trash-entry identity and whose manifest matched two distinct trash entries. It
approved the revision first, then attached the second entry as the immutable
target ten seconds later. The atomic claim succeeded:

```text
post-approval-target=running|purging|running|2
```

The fields are janitor state, trash state, action-run state and new action-run
version. The row-level manifest checks passed because both entries used the same
relative manifest. Approval never selected either entry, its root, its original
prefix, its trash prefix or its entry version.

Failure mode: after the only immutable review decision exists, a caller can
choose which matching trash entry that approval permanently deletes. The target
row then becomes immutable, which prevents correction through the same plan
revision and makes the post-approval choice look like reviewed intent.

Contract: `data-and-recovery.md:24-30` says the ready revision retains intended
fields and objects and the decision binds that immutable scope.
`data-and-recovery.md:63-74` requires approval-scope revalidation before
mutation. `http-api.md:144-146` requires an exact trash manifest and explicit
irreversible `fs.delete` intent.

Required change: make the target part of the immutable revision before approval
and action-run creation. A relational target is sufficient only if approval of
an `fs.delete` revision requires that row already to exist and target insertion
is rejected after any decision. Bind root/trash-entry ID, entry version and
manifest to the revision digest or an equivalently immutable pre-decision
relation. Add two same-manifest entries and prove a post-decision target insert
fails.

Disposition: `current_blocker`.

### P1: unsafe v4 and down-migrated purge claims bypass the v5 approval gate

Evidence: migration 000005 adds nullable
`approval_action_run_version` at lines 318-319 but neither rejects nor
neutralizes an existing approval-bearing janitor record. The strict trigger at
lines 587-667 runs only when `OLD.approval_plan_id IS NULL`. Once such a record
is recovered to `reconciling`, generic `ClaimJanitorRecord` at
`internal/storage/query.sql:758-767` returns it to `running`; the generic trash
trigger at migration lines 669-696 does not revalidate approval scope.

An independent v4 fixture used the old round-three weakness to claim an
unexpired trash entry with an approved `fs.copy` plan. Its v5 upgrade succeeded
and retained an incomplete approval as executable work:

```text
legacy-after-v5=running|purging|queued|null-version:1|targets:0
legacy-generic-reclaim=running|purging|new
```

The first row shows janitor, trash and action-run states, followed by a true
NULL-version flag and zero v5 targets. The second row shows that normal startup
recovery followed by the generic claim restored `running|purging` under the new
worker without any target, supported action kind or action-run CAS.

The v5 down migration creates the same invalid state for a valid v5 claim. It
drops all targets and the action-run-version column at
`migrations/000005_storage_safety.down.sql:51-68`, while retaining the approval
columns and running janitor. A populated v5-to-v4-to-v5 probe returned:

```text
approved-down-up=running|purging|null-version:1|targets:0
```

Foreign-key checks cannot detect either case because the missing v5 relation is
not a foreign key from `janitor_records`.

Failure mode: the first restart after upgrading can continue a permanently
destructive operation that v5 itself would reject. A supported down/up cycle
also strips the safety proof from a valid claim while leaving it apparently
approved and runnable.

Contract: `data-and-recovery.md:63-82` requires the approval, cancellation and
dispatch claim boundary to survive restart. Lines 78-80 require old running
dispatches to enter reconciliation, not blind replay. Lines 161-178 require
unfinished trash effects to remain visible and safely recoverable.

Required change: migrate every existing approval-bearing janitor record into a
safe non-mutation state unless its exact pre-approved target can be proved
without inventing evidence. Block generic claims for any record that carries
approval metadata; only the complete v5 target/action CAS may reclaim it. The
down migration must preserve the binding needed for a safe re-upgrade or
explicitly neutralize the work and require new review. Add populated v4 unsafe
claim and claimed-v5 down/up recovery fixtures.

Disposition: `current_blocker`.

### P1: quarantine downgrade and re-upgrade silently discard newer evidence

Evidence: the v5 down migration intentionally retains
`tracking_observation_quarantine`, but drops both immutability triggers at
`migrations/000005_storage_safety.down.sql:8-9`. The v4 active table permits an
unscoped unknown observation whose ID matches a retained quarantine row. On the
next upgrade, `migrations/000005_storage_safety.up.sql:157-170` uses `INSERT OR
IGNORE`; the uniqueness collision is ignored, and lines 184-187 omit the unsafe
active row and then drop its legacy table.

The independent probe quarantined generation `first`, migrated down, inserted
a new v4 observation with the same ID and generation `second`, then re-upgraded:

```text
quarantine-reupgrade={"generation":"first"}|active:0
```

The older quarantine record survived, the newer evidence disappeared, and the
upgrade reported success. The final foreign-key check also passed because this
is logical data loss rather than a dangling relation.

Failure mode: a supported rollback and later upgrade can silently erase the
latest observation while showing an older record under the same original ID.
This breaks the journal's historical evidence and can mislead reconciliation.

Contract: `data-and-recovery.md:19-22` retains observations and evidence as
history. Lines 92-96 require restarts and recovery to preserve pending state,
and the product invariant forbids inventing or silently discarding evidence.

Required change: do not ignore quarantine-key collisions. Preserve quarantine
immutability and prevent original-ID reuse during the v4 downgrade, or version
quarantine identities so distinct generations are append-only. Add a populated
v5-to-v4-to-v5 collision fixture that proves both evidence generations remain
visible or that the migration fails dirty before dropping either row.

Disposition: `current_blocker`.

### P1: dispatch incorrectly reapplies preview expiry after approval

Evidence: `migrations/000005_storage_safety.up.sql:632-636` requires the plan
and revision to remain ready and requires `apr.expires_at` to be later than the
claim time. This is separate from the action-run deadline check at lines
664-665.

An independent fixture created the exact target before review, approved the
revision ten seconds before its preview expiry, and created an action run with a
deadline one hour later. Claiming thirty seconds after preview expiry was
rejected and both durable jobs stayed queued:

```text
accepted-after-plan-expiry=error:true|queued|queued
```

Failure mode: a correctly accepted irreversible action becomes permanently
undispatchable after an ordinary delay, restart or temporary dependency outage,
even though it has not been cancelled and its execution deadline remains live.

Contract: `http-api.md:175-180` states that plan expiry applies before approval;
once approved, work waits through temporary outages and approval age alone does
not expire the accepted action. `data-and-recovery.md:47-55,78-82` gives
cancellation and action/workflow deadline the mutation-dispatch boundary.

Required change: validate that the immutable decision was created before the
revision expired, then use the accepted action's cancellation and deadline at
dispatch. Do not compare dispatch time with the old preview expiry. Add an
exact-target fixture approved before plan expiry and claimed after it, with a
live action deadline, plus a decision-created-after-expiry negative case.

Disposition: `current_blocker`.

## Round-three finding disposition

| Round-three finding | Round-four result |
| --- | --- |
| Legacy tracking compatibility remains active and can assert the wrong title | Closed for direct v4-to-v5 upgrade: mismatched/unscoped rows enter append-only quarantine and fresh active unknown rows require a connection. The separate down/up collision finding above prevents migration acceptance. |
| Janitor preflight rejects unambiguous historical operation records | Closed. Matching active purge and restore operations upgrade, recover and reclaim while opposite terminal history remains unchanged. |
| Early purge accepts unrelated and cancelled approval intent | New v5 claims reject an unrelated entry, unsupported kind, empty/mismatched manifest, cancellation, expired action deadline, waiting/needs-review state and live foreign lease. The target is still selectable after approval, and legacy/down-migrated records bypass this trigger. |

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Historical migration Git-blob comparison | Passed; 000001, 000003 and 000004 up/down blobs match round three, and 000001 matches the original implementation. |
| Scoped Git snapshot before and after review | Passed; product diff identity remained `6f9b4716f083af48ef3ce628df4cf523fc38f4dbf9f27bec0266d819ae27621d`. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed on Go 1.27.1, Darwin arm64. |
| `GOWORK=off go test -mod=readonly -count=1 -race ./internal/storage/...` | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage/... ./migrations` | Passed. |
| `./scripts/generate.sh --check` and generated SQLC diff | Passed; generated SQLC matches the ordered migrations and queries. |
| `GOWORK=off go mod verify` | Passed. |
| Linux CGO-free compile for `amd64` and `arm64` using `-run '^$' -exec=true` | Passed. |
| Tools module test, vet and tidy-diff | Passed. |
| UI module verification | `go mod verify` passed. The module has no Go packages, so `go test ./...` reports no matched packages and exits 1; this is not a useful gate. |
| Built-in v4 tracking quarantine and fresh-null negative fixture | Passed. |
| Built-in matching janitor plus opposite terminal-history restore/purge fixtures | Passed, including recovery and reclaim. |
| Built-in exact target, kind, manifest, action state, cancellation, deadline and lease negatives | Passed for a newly created v5 binding. |
| Built-in populated v5-to-v4 down migration | Passed its structural and foreign-key assertions. The independent populated down/up probe failed semantic safety as described above. |
| Post-approval same-manifest target probe | Failed the contract; the later target and destructive claim were accepted. |
| Unsafe v4 claim upgrade and generic-reclaim probe | Failed the contract; incomplete approval remained executable. |
| Quarantine ID collision across v5-to-v4-to-v5 | Failed the contract; newer evidence was silently lost. |
| Accepted action after preview expiry | Failed the contract; live accepted work was rejected. |
| `python3 scripts/check_planning.py` | Passed; 38 tasks, 60 acceptance cases and local links are valid. |
| D-01 diff check and public credential/path scan | Passed. |

Root `go mod tidy -diff` retains the coordinator/later-consumer delta: it removes
the not-yet-consumed pinned oapi runtime and adds all-tag test/transitive module
metadata. Root read-only tests, vet and module verification pass. This module
metadata item does not change the four D-01 findings.

## Acceptance contribution assessment

- A-32: the new claim updates action run, janitor and trash state atomically for
  a fresh v5 target. Legacy approval records bypass that boundary, and accepted
  actions are stranded by preview expiry.
- A-36: worker/version/lease CAS and cancellation/action-deadline negatives pass
  for the new claim. Generic reclaim remains available to approval-bearing
  legacy records without the v5 proof.
- A-43: ordinary populated upgrade/down and foreign-key checks pass. Silent
  quarantine evidence loss and destructive-claim proof loss across supported
  migration paths block acceptance.
- A-45: SQLC generation, root read-only module checks, tools checks and Linux
  builds pass for the D-01 slice.

## Next round

Freeze the exact purge target before approval, migrate or neutralize every
legacy approval-bearing janitor, preserve binding safety over down/up, reject
quarantine collisions without data loss, and apply plan expiry at decision time
rather than dispatch time. Add the four independent scenarios above to the
populated migration suite and rerun the same generation, module, race,
foreign-key and cross-platform checks. This review changed only this receipt.
