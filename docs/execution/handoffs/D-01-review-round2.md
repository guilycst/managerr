# D-01 independent review, round two

## Decision

`changes_requested`.

The correction closes the fresh-write coverage, enum, composite-key, source,
queue and default-path lock regressions from round one. Four P1 findings and
one P2 finding remain. Three P1 findings affect upgrades from supported,
populated v1/v2 databases; the fourth still permits two owners of one database.

## Review identity and boundary

- Release stage: `pre-v0.1.0`, target release `v0.0.1`.
- Reviewed correction commit:
  `d713fe432db106503d49870def691af5d4dbc911`.
- Correction parent: `3c1c3ced825d9f3d0035347ee48cb14457776724`.
- Original implementation commit:
  `293acffaf250cabdda2c9295f1f612724ef350e4`.
- Round-one receipt:
  `d35a719465b3f870bb1c2e70a16d0eb18051aa40`.
- Correction handoff update:
  `5dbdc33d06e0425af7720842fc4e002c1157d037`.
- Reviewed paths: `internal/storage/`, `migrations/`, and `sqlc.yaml`.
- Scoped review identity:
  `169098a547f327d4fe177d1a2bcde7d21e6211ed839ce918152ca9ee8479c260`.
- Reviewer: `/root/d01_reviewer`, independent of the D-01 implementer.
- Tests ran in a detached reviewer-owned worktree at the exact correction
  commit. Its local path is omitted from this public receipt.
- Producer claims were not accepted as evidence. No producer transcript or
  terminal was queried. Git objects, SQL, generated output, tests and direct
  synthetic SQLite/Go probes supplied the evidence below.
- No live media stack, credential, private inventory, upstream write,
  filesystem media mutation, release or deployment was used.

The `000001_initial.up.sql` blob is
`ce05d9deb8317af700f191f1ac227b4e2a922b5b` at both the original implementation
and correction commits. The corresponding down blob is
`bedf519dc741bdef9c6aa2143b793e2970fd18f8` at both commits. The released first
migration is byte-identical.

## Findings

### P1: populated tracking journals cannot upgrade from v1/v2

Evidence: `migrations/000003_storage_contract.up.sql:36-63` rebuilds
`tracking_observations` with `connection_id NOT NULL` and a strict absence
check. Its copy statement preserves `connection_id` and `status`, while setting
every legacy `root_id` and `coverage_max_age_seconds` to `NULL`. Both shapes
were valid in v1/v2: connection was nullable, and an absent observation needed
only a referenced complete coverage row.

Two independent v2 probes failed at the copy on line 55:

```text
NOT NULL constraint failed: tracking_observations.connection_id
null-upgrade-rc=1

CHECK constraint failed: status <> 'absent' OR (... root_id ... coverage_max_age_seconds ...)
absent-upgrade-rc=1
```

The first fixture contained a media-scoped `unknown` observation with a NULL
connection. The second contained a connection-scoped `absent` observation and
complete coverage, valid under v2. The shipped upgrade tests at
`internal/storage/store_test.go:93-206` populate only downloads, so neither
case is covered.

Failure mode: a user who has already accumulated the tracking evidence this
application is designed to store cannot start after upgrading. Because the
migration is deliberately not transaction-wrapped, it has already renamed and
created tables before the failing copy; golang-migrate marks the schema dirty
and startup stops, requiring restore or manual repair.

Contract: `data-and-recovery.md:180-186` requires upgrades from every supported
previous schema. Its lines 19-22 and the product invariant say missing evidence
is unknown, not absent. A migration cannot manufacture a connection, root,
identity or freshness bound to make an old row appear proven.

Required change: define an explicit compatibility policy for every legacy
tracking shape. Safely derivable connection scope may come from the referenced
external record. An old absence without the new complete scope/freshness proof
must become inactive/unknown while retaining its original evidence, or move to
a durable legacy/quarantine record. An unscoped row that cannot be derived must
also be preserved without assigning an arbitrary connection. Add populated
v1-to-current and v2-to-current tests for NULL connection, present, absent and
unknown rows and assert both retained evidence and conservative semantics.

Disposition: `current_blocker`.

### P1: the upgrade silently activates legacy cross-scope evidence

Evidence: 000003 disables foreign keys at line 12, copies the old
`file_observations`, `action_effects` and `trash_items` rows into tables with new
composite foreign keys at lines 65-185, then enables foreign keys at line 189.
SQLite does not retroactively validate copied rows, and the migration never
runs `PRAGMA foreign_key_check` or an equivalent preflight.

A valid v2 fixture used discovery `d1` with the `(root,path,revision)` tuple of
`d2`. Migration 000003 exited zero and retained it:

```text
cross-upgrade-rc=0
file_observations|1|discoveries|0
retained-row=mixed|d1|r2|b.mkv
```

The middle line is `PRAGMA foreign_key_check`, which reports the active
violation after the successful migration. The same copy pattern applies to
action effect/attempt scope and trash item/entry/root scope.

Failure mode: historical evidence that the correction is meant to reject
remains queryable as current evidence. A successful schema version therefore
does not imply the composite integrity the generated repository assumes.

Contract: `data-and-recovery.md:20,28,30` requires one discovery scope,
per-action attempt/effect journals and exact trash manifests. Lines 180-186
require a consistent upgraded schema and startup refusal on migration failure.

Required change: preflight all three legacy relationships before destructive
DDL, or quarantine/normalize them without asserting a corrected relationship.
Fail the migration closed when no conservative conversion exists. End the
upgrade with a zero-row foreign-key check and add populated negative upgrade
tests for file observation, action effect and trash item mismatches.

Disposition: `current_blocker`.

### P1: upgraded in-flight janitor work cannot be reclaimed

Evidence: 000003 adds `active_operation`, entry claim fields and versions at
lines 16-20 but does not populate them from existing janitor/trash state. The
claim trigger at lines 344-366 permits a `restoring` or `purging` entry to be
reclaimed only when `active_operation` already names that same operation.

A v2 fixture had a trash entry in `restoring` and its restore janitor record in
`running`. That is valid in the old schema. After upgrade:

```text
post-upgrade-entry=restoring|NULL|1
trash entry is already claimed or not eligible
reclaim-rc=19
janitor-state=reconciling|2
```

The startup recovery update moved the record from running to reconciling, but
the new reconcile trigger could not release an operation that the migration
never recorded. The next same-operation claim then failed. The equivalent
purging shape has the same problem. Current tests create only fresh v3 janitor
rows.

Failure mode: a restart after upgrading can strand a partially applied restore
or purge indefinitely. The worker cannot reconcile or finish the existing
operation through the generated claim path.

Contract: `data-and-recovery.md:92-96` says restarts preserve pending work, and
lines 161-178 require restore/purge uncertainty and unfinished per-item effects
to remain recoverable.

Required change: migrate in-flight janitor and trash state as one consistent
claim/reconciliation record. Backfill an unambiguous active operation and
preserve the old lease/worker only as historical evidence, or normalize both
records to a reclaimable reconciling state without implying that no effect
occurred. Cover running and reconciling restore/purge fixtures from both v1 and
v2, then close/reopen and reclaim them.

Disposition: `current_blocker`.

### P1: custom lock paths still bypass the single-owner guarantee

Evidence: `internal/storage/store.go:34` exposes `Options.LockPath`.
`OpenWithOptions` at lines 61-69 canonicalizes and locks that supplied path but
does not bind it to the canonical database identity. Canonicalization fixes a
database symlink alias only when both callers use the derived default lock.

An independent Go overlay probe opened the same SQLite path twice while the
first handle remained live, supplying `first.lock` and `second.lock`:

```text
second owner accepted with a distinct custom lock path
PASS
```

Both lock files were canonical. SQLite admitted both handles, so this is not a
path-normalization race.

Failure mode: two API/executor processes configured with different lock paths
can both own and dispatch from one journal. That defeats the safety property the
process lock is meant to provide.

Contract: `data-and-recovery.md:9-13` and A-43 require one active owner and
rejection of a second executor.

Required change: derive persistent-database lock identity solely from the
canonical database identity, or reject a custom lock that is not the canonical
derived lock. If an override remains necessary for `:memory:` tests, keep it
from weakening persistent paths. Add the two-distinct-overrides negative test.

Disposition: `current_blocker`.

### P2: no storage claim represents an explicitly approved early purge

Evidence: the janitor claim trigger at
`migrations/000003_storage_contract.up.sql:358-364` accepts a new purge only
when `expires_at <= updated_at`; otherwise it accepts only a retry of an already
active purge. `janitor_records` has no plan/action/decision binding. The generic
`UpdateTrashEntryState` query at `internal/storage/query.sql:663-673` can change
operation fields but supplies no proof that an early purge was explicitly
approved.

Failure mode: the storage API can enforce automatic expiry or accept an
unproven generic state change. It cannot express the other allowed branch,
an exact approved purge of a still-retained entry, without moving the recorded
expiry or bypassing the claim trigger.

Contract: `data-and-recovery.md:161-164` permits a purge claim for an expired
**or explicitly approved** entry and requires a transactional state claim.

Required change: add an atomic manual-purge claim bound to the immutable
approved plan/action identity and exact trash entry/version, or record the
specific downstream task that will add that safe path before any manual purge
consumer is implemented. Test early unapproved rejection and exact approved
acceptance without modifying `expires_at`.

Disposition: `current_blocker`.

## Round-one finding disposition

| Round-one finding | Round-two result |
| --- | --- |
| Coverage from another instance proves absence | Fresh v3 writes now require matching connection/root/media identity, completed coverage and a positive freshness bound. Closed for new writes; the tracking migration finding above blocks upgrade. |
| Restart and terminal requests strand action work | Generated transaction-friendly recovery, cancellation and deadline queries cover the D-01 repository contribution. Closed here; worker startup invocation and crash injection remain W-02/V-01. |
| Purge and restore both claim one entry | Fresh janitor claims are entry-scoped and mutually exclusive, and automatic purge requires expiry. Closed for fresh automatic work; in-flight migration and approved-early-purge findings remain. |
| Download state enum differs from API | Fresh storage accepts `complete`, rejects `completed`, and translates legacy `completed`/`paused` to `complete`/`unknown`. Closed. |
| Split foreign keys permit cross-record evidence | Composite foreign keys reject new mismatches. Closed for new writes; legacy mismatches survive migration and remain a blocker. |
| Database path aliases bypass process lock | Default-lock database symlink aliases are canonicalized and the close/reopen test passes. Closed for default paths; the independent custom-lock bypass remains. |
| Resource source disagrees with snapshot | Insert/update triggers reject mismatches. A legacy mismatch aborts 000003 at line 416 in a direct upgrade probe. Closed. |

## Independent checks

| Command or scenario | Result |
| --- | --- |
| `git rev-parse <commit>:migrations/000001_initial.{up,down}.sql` at original and correction commits | Passed; both migration blobs are byte-identical. |
| Scoped Git snapshot before and after review | Passed; D-01 product identity remained `169098a547f327d4fe177d1a2bcde7d21e6211ed839ce918152ca9ee8479c260`. |
| `GOWORK=off go test -mod=mod -count=1 ./...` | Passed on Go 1.27.1, Darwin arm64. |
| `GOWORK=off go test -mod=mod -count=1 -race ./internal/storage/...` | Passed. |
| `GOWORK=off go vet -mod=mod ./...` | Passed. |
| Standard test, race and vet commands without `-mod=mod` after module classification | Passed. |
| Focused migration, symlink lock, coverage/composite, action recovery and janitor tests | Passed. These fresh fixtures do not exercise the populated failures above. |
| `./scripts/generate.sh --check` plus generated SQLC diff | Passed; generated output is clean. |
| `GOWORK=off go mod verify` | Passed; all modules verified. |
| Linux CGO-free compile for `amd64` and `arm64` with `-run '^$' -exec=true` | Passed. |
| Tools module test, vet and tidy-diff | Passed. |
| UI module verification | Passed. |
| Populated valid v3-to-v2 down migration | Passed; download `complete` became `completed`, affected records remained, v3-only columns disappeared and `foreign_key_check` returned zero rows. |
| Populated v1/v2 tracking upgrade probes | Failed as described: valid NULL and absent rows make 000003 abort. |
| Populated v2 cross-scope upgrade probe | Failed the contract: migration exited zero and post-upgrade `foreign_key_check` reported the retained row. |
| Populated v2 in-flight janitor upgrade/reclaim probe | Failed as described: the operation remained unreclaimable in reconciling. |
| Distinct custom-lock-path second-owner probe | Failed the contract: both handles opened one database concurrently. |
| Legacy source mismatch upgrade probe | Passed the safety expectation: 000003 aborted on the source trigger. |
| `python3 scripts/check_planning.py` | Passed; 38 tasks, 60 acceptance cases and local links valid. |
| D-01 `git diff --check` and public secret/path scan | Passed. |

The first `-mod=mod` root test reclassified `golang.org/x/sys` from indirect to
direct because the correction baseline includes a direct F-01 production
import. Exact `d713fe4` and current coordinator HEAD still need that separate
root `go.mod` refresh. It is outside D-01 ownership and was not staged here.
Root `go mod tidy -diff` also retains the previously recorded C-02/C-04 delta:
the pre-pinned oapi runtime is not consumed yet and all-tag transitive sums are
not fully present. Neither item changes the D-01 findings above.

## Acceptance contribution assessment

- A-32: transaction and queue recovery primitives pass, but full approval/queue
  crash behavior remains W-02/V-01. The in-flight janitor upgrade failure is a
  separate restart blocker.
- A-36: action-run CAS and default canonical lock paths pass. The custom lock
  override prevents accepting D-01's single-owner contribution.
- A-43: fresh migration, dirty refusal, default second-owner rejection, valid
  populated down migration and Linux builds pass. Supported populated upgrades
  remain unaccepted because tracking data can abort and cross-scope data can
  survive as active foreign-key violations.
- A-45: SQLC regeneration and module verification pass. Full repository
  generation remains a coordinator/later-consumer gate.

## Next round

Revise 000003 without changing 000001. Add populated v1/v2 fixtures for tracking,
cross-scope evidence and in-flight janitor work, close the custom-lock bypass,
and represent or explicitly assign the approved early-purge claim. Re-run the
focused and broad gates, including a clean generated diff and a populated
up/down cycle. This review changed only this receipt.
