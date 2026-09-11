# D-01 independent review, round seven

## Decision

`changes_requested`.

Round seven fixes repeated recovery for fresh v8 state and fences the action in
the same claim that leases the janitor and trash entry. No P0 finding was found.
Three P1 recovery gaps remain: an in-flight round-six reconciliation cannot
survive the supported v7-to-v8 upgrade, cancellation or deadline prevents the
required read-only reclaim after a crash, and expired-lease recovery briefly
removes the action fence before the janitor is recovered.

## Review identity and boundary

- Release stage: `pre-v0.1.0`, target release `v0.0.1`.
- Reviewed product commit:
  `c2ab5dc60fd5ec51c2bebbf6ef35fb5d545b43ce`.
- Product parent: `f0b92348d06729ee0743d8da1049b3067b4ce4e4`.
- Round-seven implementation handoff commit:
  `4a8b929ecae18cd6b067ee5248a857ae1fbab951`.
- Round-six review receipt commit:
  `1edab456859701d665cf92daf5fc2a54bfa0a3a2`.
- Reviewed paths: `internal/storage/`, `migrations/`, and `sqlc.yaml`.
- Scoped product diff identity:
  `774c782cba69f6f096cfe7c43e27cfb0dcebe08d01793f9a98ab8f1bfa7b8fef`.
- Reviewer: `/root/d01_reviewer`, independent of the round-seven worker.
- Checks ran in a detached reviewer-owned worktree at the exact product commit.
  Its local path is omitted from this public receipt.
- Producer conclusions were not accepted as evidence. Git objects, generated
  output, committed synthetic tests and separate SQLite probes supplied the
  evidence below.
- The shared checkout remained outside the review worktree. This review changes
  only this receipt. It did not change execution state, product paths, unrelated
  Arr work, credentials, live services, media, release state or deployment.

The product adds only migration `000008`; migrations `000001` through `000007`
are byte-identical to the product parent. Relevant frozen Git blobs are:

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
| `000007_storage_recovery_safety.up.sql` | `4016a1402707c5fa53cb1a1ff170fd1282734ed4` |
| `000007_storage_recovery_safety.down.sql` | `4174107ecc003ee5b4edf7a567b4365aa5d26cef` |

## Findings

### P1: a crashed round-six reconciler is stranded by the v7-to-v8 upgrade

Evidence: round six reacquired only the janitor/trash lease and deliberately
left the action reconciling and unleased. After its first reconciliation claim,
the populated versions are action 3, janitor 4 and trash 4. A crash leaves that
valid v7 state for the next release to migrate.

Migration `000008_storage_recovery_concurrency.up.sql:7-19` replaces the trigger
without preflighting or neutralizing this supported populated state. On v8
startup, `RecoverRunningActionRuns` at `internal/storage/query.sql:483-492`
does not advance the already-unleased reconciling action, while
`RecoverRunningJanitorRecords` at lines 735-742 advances the janitor and trash
entry. The result is action 3, janitor 5 and trash 5. The v8 query then requires
`action.version = janitor_records.version` at lines 836-850, so no recovery
claim can succeed.

Independent reproduction:

1. Create a database with migrations through `000007`.
2. Seed a valid exact approved purge and perform its initial claim.
3. Run the v7 startup recovery and the v7 reconciliation claim.
4. Close the database while that reconciler is running.
5. Open with the current migrations, then run action and janitor startup
   recovery and attempt the v8 exact reconciliation claim.

Observed result:

```text
upgraded crashed v7 reconciliation cannot reclaim: sql: no rows in result set versions action=3 janitor=5 entry=5
```

Failure mode: deploying the correction while round-six read-only reconciliation
is in flight converts recoverable approved work into a permanent unleased
`reconciling`/`purging` record. The application cannot resume it without direct
database repair or discarding the prior approval.

Contract: `data-and-recovery.md:47-55` defines reconciliation as resolvable
read-only work. Lines 78-82 require crash recovery rather than replay, and lines
92-96 require restarts to preserve and resume durable work. A-43 requires a
consistent supported schema upgrade whose failure prevents workers.

Required change: preflight the populated round-six in-flight shape during
`000008`. Either establish the new action fence and align its generation using
the exact existing janitor/trash binding, or conservatively hold the janitor and
trash and move the action to `needs_review`. Add a fixture that creates and
claims under migrations through `000007`, crashes, upgrades to `000008`, and
proves a safe reclaim or explicit held state.

Disposition: `current_blocker`.

### P1: cancellation and deadline strand uncertain read-only reconciliation

Evidence: the new recovery trigger requires
`action.cancellation_requested_at IS NULL` and a live action deadline at
`migrations/000008_storage_recovery_concurrency.up.sql:79-89`; the generated
claim applies the same gates at `internal/storage/query.sql:836-850`. These are
mutation-dispatch gates applied to a query whose documented purpose is read-only
reconciliation.

Cancellation adds another failure: `RequestActionCancellation` at
`internal/storage/query.sql:583-585` increments only the action version. After
startup recovery, the action and janitor generations differ, so the equality
predicate rejects the claim independently of the cancellation predicate.

Independent cancellation reproduction:

1. Seed, recover and fence a valid approved purge under v8.
2. Request cancellation while its effect remains uncertain.
3. Simulate a crash by recovering the fenced action and running janitor.
4. Attempt the exact read-only reconciliation claim.

Observed result:

```text
cancelled uncertain purge lost read-only reclaim: sql: no rows in result set actionVersion=6 janitorVersion=5
```

Independent deadline reproduction performs the same sequence without
cancellation, then recovers after the action deadline:

```text
deadline-expired uncertain purge lost read-only reclaim: sql: no rows in result set
```

Failure mode: the escape hatch that stops further mutation also stops the only
exact janitor/trash evidence claim. Late effects and uncertainty cannot be
resolved after a crash, even though mutation is already forbidden.

Contract: `data-and-recovery.md:50-55` keeps late effects visible after
cancellation/deadline. Lines 78-82 explicitly state that read-only observation
can continue after either boundary. A-35 requires no later dispatch while late
effects and uncertainty remain visible. `http-api.md:70-72` defines cancellation
and reconciliation as separate durable operations and says reconciliation does
not authorize mutation.

Required change: allow a cancelled or deadline-expired approval-bound operation
to reacquire an explicitly read-only reconciliation lease while continuing to
reject every mutation dispatch. Do not use the original dispatch eligibility
predicate for observation. Keep action/janitor/trash generations coherent when
cancellation changes the action record. Add committed cancellation-before-crash
and deadline-before-reclaim fixtures that resolve or reach `needs_review`
without another delete dispatch.

Disposition: `current_blocker`.

### P1: split expired-lease recovery reopens the action race

Evidence: `RecoverExpiredActionRuns` independently changes a fenced
`reconciling` action to unclaimed reconciliation at
`internal/storage/query.sql:494-504`. The matching janitor and trash entry
remain `running` with their old lease until `RecoverExpiredJanitorRecords` runs
at lines 744-750. During that interval, generic `ClaimActionRun` accepts the
unleased reconciling action at lines 550-572. The migration trigger cannot help
because it runs only when the janitor later moves from reconciling to running.

Independent reproduction:

1. Seed an initially claimed exact approved purge whose action and janitor share
   an expired lease.
2. Call `RecoverExpiredActionRuns` only.
3. Before janitor recovery, call generic `ClaimActionRun` with the returned
   action version.

Observed result:

```text
generic action raced split recovery: claim=&{... State:running ... ClaimedBy:generic-action-worker ... Version:4 ...} err=<nil>
```

The generic action worker owns the action while the old janitor still owns the
same logical purge. Subsequent janitor recovery produces generations that the
exact claim cannot reconcile with the now-running competing action.

Failure mode: scheduler interleaving can recreate the round-six concurrent
ownership defect before the new atomic fencing trigger is reached. A single
process lock does not serialize independent worker goroutines or SQL calls.

Contract: `data-and-recovery.md:68-82` requires version/lease claims and makes
lease expiry a safe recovery boundary. A-36 requires local reservation/CAS to
prevent conflicting execution.

Required change: recover the coupled action, janitor and trash lease in one
transactional boundary, or make generic action selection and claiming exclude
an action referenced by a nonterminal approval-bound janitor. Add a test that
interleaves an action worker between the two recovery calls and proves the
generic claim returns no row.

Disposition: `current_blocker`.

## Round-six finding disposition

| Round-six finding | Round-seven result |
| --- | --- |
| Second crash strands approved reconciliation | Closed for state created entirely under v8: the committed two-cycle fixture passes and all three generations advance together. Not closed for supported populated v7 state or cancellation/deadline recovery. |
| Janitor reconciliation leaves the coupled action claimable | Closed after the v8 janitor claim: the trigger fences the action atomically and the separate-connection competitor test passes. Not closed during the split action/janitor expired-lease recovery interval. |

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Historical migration Git-blob comparison | Passed; `000001` through `000007` are unchanged from the product parent. |
| Scoped Git diff identity and whitespace check | Passed; identity remained `774c782cba69f6f096cfe7c43e27cfb0dcebe08d01793f9a98ab8f1bfa7b8fef`, `git diff --check` was clean and the owned Go test was gofmt-clean. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed on Darwin arm64. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/storage/...` | Passed in 73.442 seconds. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| Focused committed round 4/5/6/7 storage suite | Passed, including migration immutability, current-version repeated crashes, post-claim competitor fencing and v8 down/re-up. |
| `./scripts/generate.sh --check` | Passed; generated SQLC matches the query and migrations. |
| `GOWORK=off go mod verify` | Passed for root, tools and UI modules. |
| Tools module test, vet and `go mod tidy -diff` | Passed. |
| UI module test/vet | Module verification and vet passed. It has no test-bearing Go package. |
| Linux amd64/arm64 CGO-free `go test -run '^$' -exec=true ./...` | Passed for both architectures. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases and local links are valid. |
| Public credential/private-path scan of D-01 paths | Passed; no match. |
| Populated v7 in-flight upgrade probe | Failed the contract; v8 cannot reclaim action/janitor/entry versions 3/5/5. |
| Cancellation and deadline recovery probes | Failed the contract; both exact read-only claims return `sql.ErrNoRows`, and cancellation also desynchronizes generations. |
| Split expired-recovery competitor probe | Failed the contract; generic action claim succeeds before janitor recovery. |

The `000008` down migration moves the committed active fenced fixture to
`needs_review`/`held`, restores the v7 trigger, passes foreign-key validation,
and safely re-upgrades in the committed test. Prior approval chronology,
immutable-target, quarantine, direct-down, lock, JSON/path and queue regression
tests remain green. Passing committed tests do not cover the three state-machine
boundaries above.

## Acceptance contribution assessment

- A-32: fresh-v8 repeated restart recovery passes. A supported populated-v7
  crash state cannot be resumed after upgrade.
- A-35: cancellation and deadline still prohibit mutation, but they also
  prohibit the required read-only evidence claim after a crash.
- A-36: the v8 claim atomically fences action/janitor/trash. Split lease recovery
  still permits a conflicting generic action claim.
- A-43: migration ordering, historical immutability, ordinary down/re-up and
  foreign-key checks pass. The populated in-flight upgrade is not safe.
- A-45: SQLC generation, root/tools/UI module verification, vet, race checks and
  Linux builds pass for this slice.

## Next round

Migrate or hold the populated round-six in-flight shape, retain read-only
reconciliation after cancellation/deadline, and close the split recovery race.
Commit the three independent reproductions above, then rerun the same migration,
race, generation, module, downgrade and Linux checks. This review changed only
this receipt.
