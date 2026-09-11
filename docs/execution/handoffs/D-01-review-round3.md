# D-01 independent review, round three

## Decision

`changes_requested`.

The correction closes the composite-foreign-key, final integrity-check,
canonical persistent-lock and basic legacy-janitor recovery findings from round
two. Three P1 findings remain: legacy tracking compatibility is still exposed
as active tracking data, the janitor preflight rejects valid historical rows,
and an early purge is not bound to the approved mutation scope or dispatch
boundary.

## Review identity and boundary

- Release stage: `pre-v0.1.0`, target release `v0.0.1`.
- Reviewed product commit:
  `0dce2b9b882130d892fa0e02ffaaf0e2f3761da3`.
- Product parent: `f3b67a237ff9604e821bfb1972a6ec5d74dcc609`.
- Round-three handoff commit:
  `f9cd29fac881aaf801603b2070649e8a8f93f5b9`.
- Round-two receipt:
  `2656a9bb9de4389b5170e17c6a420d0884eeffd3`.
- Reviewed paths: `internal/storage/`, `migrations/`, and `sqlc.yaml`.
- Scoped review identity:
  `7965282b2670d1e46b18974dbe44156517b73e69570739a1a75e4bc90cd85c16`.
- Reviewer: `/root/d01_reviewer`, independent of the round-three worker.
- Tests ran in a detached reviewer-owned worktree at the exact product commit.
  Its local path is omitted from this public receipt.
- Producer conclusions were not used as evidence. No producer transcript or
  terminal was queried. Git objects, source, generated output, synthetic tests
  and direct SQLite probes supplied the evidence below.
- No live stack, credential, private inventory, upstream write, media mutation,
  release or deployment was used.

The `000001_initial.up.sql` blob remains
`ce05d9deb8317af700f191f1ac227b4e2a922b5b`; its down blob remains
`bedf519dc741bdef9c6aa2143b793e2970fd18f8`. Both match the original D-01
implementation commit byte for byte.

## Findings

### P1: legacy tracking compatibility remains active and can assert the wrong title

Evidence: `migrations/000003_storage_contract.up.sql:202-255` keeps legacy rows
in `tracking_observations`. It downgrades unscoped present/absent values but does
not compare a referenced external record's media identity with the observation's
media identity. The identity trigger is created only after the copy. Migration
000004 rebuilds and recopies the same active shape at lines 73-102.

A v2 fixture contained an external record for `m1` and a tracking observation
that pointed to that record while naming `m2`. This was legal in v2. The full
v2-to-v4 upgrade succeeded and retained:

```text
legacy-identity-after-upgrade=present|m2|m1
```

The row therefore states that the wrong media identity is presently registered
or imported. Neither the composite preflight nor `PRAGMA foreign_key_check`
detects it because all individual foreign keys exist.

The compatibility design also makes `connection_id` nullable in the active
table and generates a nullable SQLC parameter. A fresh v4 insert with no
connection, external record or media identity succeeded:

```text
fresh-unscoped-count=1
```

`internal/storage/store_test.go:527-532` rejects only an unscoped **present**
row. It does not reject a fresh unscoped unknown row. The API requires
`connectionId` at `api/openapi.yaml:2087-2093`, and
`internal/domain/observations.go:354-358` rejects every invalid connection ID,
including unknown observations.

Failure mode: a migrated false title association remains a current `present`
fact, while new storage callers can create rows that no domain/API tracking
object can represent. The dashboard cannot answer which manager was observed,
and downstream reconciliation can consume a false tracked-media association.

Contract: `data-and-recovery.md:19-22` requires connection-scoped tracking and
states that incomplete evidence cannot establish absence. The product
invariant says missing evidence is unknown; it does not make unknown evidence
unscoped.

Required change: keep active `tracking_observations.connection_id` non-null and
move non-derivable legacy evidence into a distinct durable quarantine/history
shape that active list queries do not return as instance evidence. During
migration, compare both connection and media identity with the referenced
external record. Downgrade a mismatched legacy association to quarantined
unknown evidence without inventing the correct title. Add v1/v2 mismatch tests
and a fresh SQLC-insert test proving every active status, including unknown,
requires a connection.

Disposition: `current_blocker`.

### P1: janitor preflight rejects unambiguous historical operation records

Evidence: the v1/v2 preflight at
`migrations/000003_storage_contract.up.sql:104-111` rejects an active trash row
when **any** joined janitor record is not running or reconciling. Migration 000004
uses the same predicate at lines 149-156. It does not ask whether one matching
active record exists; a terminal record for the other operation is enough to
abort.

A valid v2 fixture had a trash entry in `restoring`, its restore record in
`running`, and an older purge record in `failed`. The state is unambiguous: the
current operation is restore, while the failed purge is retained history. The
upgrade aborted at the preflight:

```text
CHECK constraint failed: ok = 1
unambiguous-history-upgrade-rc=1
```

The round-three tests cover dual active operations and an active operation that
contradicts the trash state. They do not cover one matching active record plus
the other operation's terminal history, even though the schema explicitly
permits one record per `(trash_entry_id, operation)`.

Failure mode: a supported populated database can become dirty and fail startup
despite having one safely recoverable active operation. Retaining prior failed,
held, cancelled or succeeded janitor history makes upgrades less reliable.

Contract: `data-and-recovery.md:92-96` requires restarts to preserve pending
work. Lines 161-178 require unfinished trash effects and per-item state to remain
visible and recoverable.

Required change: formulate `active_trash_row_conflict` with `NOT EXISTS` for a
matching active record, and assess terminal records as history unless they
actually contradict the active trash state. Add v1/v2/v3 fixtures for active
restore after terminal purge history and active purge after terminal restore
history, including close/open recovery and reclaim.

Disposition: `current_blocker`.

### P1: early purge accepts unrelated and cancelled approval intent

Evidence: `janitor_records_approved_purge_claim` at
`migrations/000004_storage_compatibility.up.sql:283-338` proves that the supplied
plan revision, approval and action run refer to each other. It never proves that
the immutable plan input/manifest authorizes `NEW.trash_entry_id`, its manifest,
or `NEW.approved_entry_version`; it does not check the action kind. The entry
version is supplied only at claim time, after approval.

A valid approval and action run for `fs.copy` with empty manifests successfully
claimed an unrelated, unexpired trash entry:

```text
unrelated-approval-claim=running|purging|purge|fs.copy
```

The trigger also accepts any nonterminal action-run state at lines 311-319 and
does not check `cancellation_requested_at`, `deadline_at`, the run's claim/lease,
or worker identity. A second probe used an `fs.delete` action run that had both
a cancellation request and an expired deadline. At claim time the janitor and
trash entry still moved to mutation states:

```text
cancelled-deadline-claim=running|purging|2026-09-11T00:00:30Z|2026-09-11T00:00:45Z
```

The committed positive test uses a synthetic `trash.purge` action kind, which
is not one of the supported action kinds in `http-api.md:133-147`, and gives the
approved plan an empty manifest. It tests referential consistency, not approved
scope or the cancellation dispatch boundary.

Failure mode: approval for one harmless or unrelated action can authorize
permanent deletion of another entry. A cancellation or deadline that should
prevent new mutation dispatch does not stop the purge claim. The associated
action run also remains queued, allowing its own worker lifecycle to diverge
from the destructive janitor operation.

Contract: `data-and-recovery.md:24-30,63-82` binds approval to immutable intended
objects, requires approval-scope and cancellation revalidation, and requires one
transactional dispatch-claim boundary. `http-api.md:139-146` requires exact
manifests for copy, restore and delete. `http-api.md:175-180` forbids mutation
dispatch after a workflow deadline.

Required change: bind the approved plan revision to the exact trash entry,
entry version and immutable manifest through a relational plan target or a
strictly validated immutable plan field. Require the supported delete/purge
intent. Claim or verify the action run in the same transaction with CAS,
matching worker/lease, no cancellation request and an unexpired deadline before
the janitor/trash claim can change state. Add negative tests using an unrelated
approved plan, another trash entry, cancellation requested, deadline exceeded,
waiting/needs-review action state and a worker/lease mismatch.

Disposition: `current_blocker`.

## Round-two finding disposition

| Round-two finding | Round-three result |
| --- | --- |
| Populated tracking journals cannot upgrade | Basic NULL/present/absent fixtures now upgrade conservatively. Not closed: identity-mismatched legacy evidence remains active and the compatibility shape permits fresh unscoped rows. |
| Upgrade silently activates cross-scope evidence | Closed for file observation, action effect and trash item scope. v1/v2 preflights and the v3 repair preflight fail dirty; an independent orphan-FK probe reached the final check and failed. |
| Upgraded in-flight janitor work cannot be reclaimed | The matching running/reconciling and orphan active-state fixtures now backfill and reclaim. Not closed: the preflight rejects an unambiguous active operation when terminal history for the other operation exists. |
| Custom lock paths bypass one owner | Closed for persistent databases. Noncanonical overrides are rejected and the canonical derived override shares the same lock. |
| No approved early-purge claim | A dedicated SQLC claim now exists, but its approval binding does not include the approved object/manifest or dispatch cancellation/deadline boundary. The safety requirement remains open at P1. |

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Immutable 000001 up/down Git blob comparison | Passed; both blobs match the original implementation. |
| Scoped Git snapshot before and after review | Passed; product identity remained `7965282b2670d1e46b18974dbe44156517b73e69570739a1a75e4bc90cd85c16`. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed on Go 1.27.1, Darwin arm64. |
| `GOWORK=off go test -mod=readonly -count=1 -race ./internal/storage/...` | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| Focused round-three migration, scope, janitor, lock and purge tests | Passed. The missing cases are reproduced above. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage/... ./migrations` | Passed. |
| `./scripts/generate.sh --check` plus generated SQLC diff | Passed; generated files match the ordered migrations and queries. |
| `GOWORK=off go mod verify` | Passed. |
| Linux CGO-free compile for `amd64` and `arm64` using `-run '^$' -exec=true` | Passed. |
| Tools module test, vet and tidy-diff | Passed. |
| UI module verification | Passed. The UI module currently contains no Go packages, so `go test ./...` reports no matched packages and is not a useful gate. |
| Populated v4-to-v2 down migration followed by v2-to-v4 re-upgrade | Passed for a valid tracking fixture; records remained and both foreign-key checks returned zero rows. |
| Final foreign-key-check probe with an orphan relation outside the three explicit preflights | Passed the safety expectation: migration aborted at the final check. |
| Legacy tracking identity mismatch and fresh unscoped tracking probes | Failed the contract as described; both unsafe active rows persisted. |
| Legacy active janitor plus terminal opposite-operation history | Failed the contract as described; the supported upgrade aborted. |
| Unrelated approval and cancelled/deadline early-purge probes | Failed the contract as described; both destructive claims succeeded. |
| `python3 scripts/check_planning.py` | Passed; 38 tasks, 60 acceptance cases and local links valid. |
| D-01 diff check and public credential/path scan | Passed. |

Root `go mod tidy -diff` retains the previously recorded C-02/C-04 delta: the
pre-pinned oapi runtime is not consumed yet and all-tag transitive sums are not
fully present. The root module otherwise passes read-only tests, vet and module
verification. This known coordinator/later-consumer integration item does not
change the D-01 findings.

## Acceptance contribution assessment

- A-32: storage transaction and queue primitives pass. The early-purge claim
  bypasses the required cancellation/deadline dispatch boundary, so the D-01
  contribution remains unaccepted.
- A-36: canonical persistent locking and the existing action/janitor CAS pass.
  The early-purge action run and janitor are not claimed as one coordinated CAS
  boundary.
- A-43: clean valid up/down migration, dirty refusal, cross-scope preflight,
  final foreign-key check and Linux builds pass. Supported populated upgrades
  still fail for valid janitor history, and tracking identity drift survives.
- A-45: SQLC regeneration, read-only module checks and tools checks pass. Full
  repository generation remains a coordinator/later-consumer gate.

## Next round

Separate quarantined tracking history from active connection-scoped facts and
validate legacy external-record identity. Narrow the janitor preflight to actual
ambiguity. Redesign the early-purge claim around an immutable exact target and
the action-run cancellation/deadline claim boundary. Keep 000001 unchanged, add
the missing populated/negative fixtures, and rerun the same up/down, generated,
race and module gates. This review changed only this receipt.
