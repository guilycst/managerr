# D-01 independent review, round one

## Decision

`changes_requested`.

No P0 finding was found. Six P1 findings and one P2 finding remain. They affect
the current D-01 storage contract and must close before integration. Passing
unit, race, vet, generation and cross-compile checks do not cover these negative
cases.

## Review identity and boundary

- Release stage: `pre-v0.1.0`, target release `v0.0.1`.
- Reviewed implementation commit:
  `293acffaf250cabdda2c9295f1f612724ef350e4`.
- Actual implementation parent:
  `74d4ebe0727686bbfe0c156721bb40cd940b5195`.
- Recorded dispatch baseline:
  `e28ff3f7e6978a94e6357aa2c998a70b1fc286c6`.
- Reviewed paths: `internal/storage/`, `migrations/`, and `sqlc.yaml`.
- Scoped review snapshot:
  `8716ebf4c7b221b63ee783ea69a7d011ffebfc88bd6644baf3d96fa8c2733086`.
- Reviewer: `/root/d01_reviewer`, independent of the D-01 implementer.
- Tests ran in a detached reviewer-owned worktree at the exact product commit.
  Its local path is omitted from this public receipt.
- Producer claims were not accepted as evidence. No producer transcript or
  terminal was queried. One team-roster snapshot confirmed ownership and was not
  used as review evidence. Git, source, generated output, migrations and direct
  runtime probes supplied the evidence below.
- No live media stack, credential, private inventory, upstream write,
  filesystem media mutation, release or deployment was used.

## Findings

### P1: coverage from another instance can prove absence

Evidence: `tracking_observations.connection_id` is nullable in
`migrations/000001_initial.up.sql:228`. The connection trigger at lines 484-500
only compares an external record when both IDs exist. The absence trigger at
lines 502-522 checks only that the referenced coverage is complete. It does not
require an observation connection, matching coverage connection, completed
coverage no newer than the observation, or a freshness bound.

Reproduction against a freshly migrated SQLite database inserted complete
coverage for `radarr-b`, then stored both rows successfully:

```text
no-scope|NULL|cov-b
wrong-scope|radarr-a|cov-b
```

Failure mode: another Arr instance's inventory, or an unscoped record, can be
persisted as proof that media is absent from `radarr-a`. That turns missing
evidence into a false untracked result.

Contract: `internal/domain/observations.go:354-403` requires a valid connection,
matching connection-scoped coverage, completion ordering and a positive
freshness bound. `data-and-recovery.md:19-22` says incomplete or mismatched
coverage cannot prove absence.

Required change: make every tracking observation connection-scoped. For
`absent`, enforce matching complete coverage with valid completion ordering.
Persist or otherwise enforce the selected freshness bound before insertion.
Add negative storage tests for NULL scope, wrong connection, partial,
unfinished, future and stale coverage.

Disposition: `current_blocker`.

### P1: restart and terminal requests strand action work

Evidence: `ListDueActionRuns` and `ClaimActionRun` in
`internal/storage/query.sql:457-479` only consider `queued` and
`waiting_dependency`, exclude cancellation requests, and exclude expired
deadlines. No generated query moves a crashed `running` action or attempt into
`reconciling`, releases an expired claim, or materializes cancellation and
deadline terminal states. `ClaimJanitorRecord` at lines 639-647 also excludes
expired `running` claims.

Reproduction inserted a running action with an expired lease, a queued action
with cancellation requested, and a queued action past its deadline. The due
predicate returned zero and all rows kept their old states:

```text
due|0
cancelled-requested|queued
crashed|running
deadline-passed|queued
```

Failure mode: process restart, cancel, or deadline makes durable work disappear
from worker selection without reaching `reconciling`, `cancelled`, or
`deadline_exceeded`. Uncertain writes may remain hidden forever.

Contract: `data-and-recovery.md:78-82` requires running dispatches to become
reconciling on startup. Lines 92-96 require restart to preserve queued work,
deadlines and backoff.

Required change: add generated, transactional recovery queries for action runs,
attempts and janitor claims. Startup must release stale leases, move potentially
dispatched work to reconciliation, and materialize cancel/deadline states without
hiding unresolved effects. Add close/reopen tests for every state above.

Disposition: `current_blocker`.

### P1: purge and restore can both claim one trash entry

Evidence: `janitor_records` allows one row per `(trash_entry_id, operation)` in
`migrations/000001_initial.up.sql:435-446`, so one purge and one restore row may
coexist. `ClaimJanitorRecord` in `internal/storage/query.sql:639-647` compares
only that individual row. It does not claim the shared trash entry, exclude the
opposite operation, or check purge expiry.

Reproduction queued both operations for one unexpired trash entry. Both claims
succeeded:

```text
purge|running
restore|running
```

Failure mode: two workers can restore and delete the same payload. A separately
queued purge can also claim before `expires_at`.

Contract: `data-and-recovery.md:148-152` forbids early purge. Lines 161-164
require a transactional entry claim and state that restore and purge cannot both
claim it.

Required change: put mutual exclusion on the shared trash entry with an atomic
state/version claim. A purge claim must require recorded expiry or an exact
explicit approval, and an active restore must exclude it. Add both claim orders,
early purge, lease expiry and restart tests.

Disposition: `current_blocker`.

### P1: download state enum differs from the frozen API

Evidence: `migrations/000001_initial.up.sql:160` accepts `completed` and `paused`.
The authoritative API enum at `api/openapi.yaml:2210` uses `complete` and has no
`paused` value.

Reproduction stored `completed`, then tried the valid wire value `complete`:

```text
stored|completed
CHECK constraint failed: state IN (... 'completed' ...)
```

Failure mode: the repository cannot persist a valid generated API value, while
it can persist values the generated API cannot return.

Contract: C-01 makes `api/openapi.yaml` authoritative. D-01's handoff also claims
the schema enums follow frozen API/domain values.

Required change: use the frozen enum exactly, or add and test an explicit typed
translation if storage states intentionally differ. Regenerate sqlc and add a
table test comparing every persisted public state to the generated contract.

Disposition: `current_blocker`.

### P1: split foreign keys permit cross-record evidence

Evidence: `file_observations` separately references `discovery_id` and a
`(root_id, relative_path, manifest_revision)` tuple in
`migrations/000001_initial.up.sql:132-150`. Those references can identify two
different discoveries. `action_effects.action_run_id` and `attempt_id` at lines
369-380 can identify different action runs. `trash_items.entry_id` and `root_id`
at lines 415-432 can identify different trash entries and roots.

Direct probes stored a file observation mixing discovery `d1` with `d2`'s path,
and an effect for `run1` linked to an attempt from `run2`:

```text
mixed|d1|r2|b.mkv
effect-cross|run1|run2
```

Failure mode: observation history, effect certainty and trash recovery evidence
can be attached to the wrong aggregate while every foreign key reports valid.
That can make one action appear applied based on another action's attempt.

Contract: `data-and-recovery.md:20,28,30` requires root-scoped observations,
per-action attempt/effect journals and exact trash manifests.

Required change: use composite foreign keys or equivalent triggers so each
redundant scope field names the same parent. Cover file observation/discovery,
effect/attempt/action run, and trash item/entry/root mismatches with negative
tests.

Disposition: `current_blocker`.

### P1: database path aliases bypass the process lock

Evidence: `normalizeDatabasePath` in `internal/storage/store.go:212-227` applies
only `Abs` and `Clean`. Default lock identity at lines 61-69 is the resulting
path spelling plus `.lock`; it does not resolve an existing database symlink or
canonical parent.

A reviewer probe opened `state.sqlite`, created `alias.sqlite` as a symlink to
that same file, then called `storage.Open` through the alias while the first store
remained open:

```text
second accepted through symlink alias
```

Failure mode: two API/executor processes can own one SQLite database through two
path aliases and use distinct lock files. SQLite serialization does not replace
Managerr's single-executor guarantee.

Contract: `data-and-recovery.md:9-13` and A-43 require one active owner and
rejection of a second executor.

Required change: derive lock identity from a canonical database target and
canonical parent, reject unsafe aliases, and revalidate identity around initial
creation. Add a symlink-alias negative test plus close/reopen release test.

Disposition: `current_blocker`.

### P2: resource source can disagree with its snapshot

Evidence: connections, roots and mappings store both `source` and
`source_snapshot_id`, but their foreign keys only reference snapshot ID.
`migrations/000001_initial.up.sql:17-59` has no constraint tying both source
values together.

Reproduction stored an API-owned connection against a YAML snapshot:

```text
api-owned|api|yaml
```

Failure mode: the API can expose or enforce the wrong editability authority for
a persisted resource.

Contract: `configuration.md` defines source ownership as explicit and controls
whether a resource is editable. D-01's deliverable explicitly includes source
ownership constraints.

Required change: use a composite `(snapshot_id, source)` foreign key or matching
insert/update triggers for connections, roots and mappings. Add both mismatch
directions as negative tests.

Disposition: `current_blocker`.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Scoped Git snapshot before and after review | Passed; product identity stayed `8716ebf4c7b221b63ee783ea69a7d011ffebfc88bd6644baf3d96fa8c2733086`. |
| `GOWORK=off go test -mod=mod ./...` | Passed on Go 1.27.1, Darwin arm64. |
| `GOWORK=off go test -mod=mod -race ./internal/storage/...` | Passed. |
| `GOWORK=off go vet -mod=mod ./...` | Passed. |
| `./scripts/generate.sh --check` plus scoped generated diff | Passed; sqlc output remained byte-for-byte clean. |
| `GOWORK=off go mod verify` | Passed. |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=mod -run '^$' -exec=true ./internal/storage/...` | Passed. |
| Same Linux compile for `arm64` | Passed. |
| Standard test, race, vet and module verification after checksum refresh | Passed. Reviewer-resolved `go.mod` and `go.sum` blobs exactly matched coordinator commit `eb70179dcedcfb514df428b39b052f2083c5a871`. |
| `GOWORK=off go mod tidy -diff` in root | Exit 1. It would remove the pre-pinned, not-yet-consumed oapi runtime and add all-tag transitive sums. This remains the recorded C-02/C-04 module-integration gate, separate from D-01 product findings. |
| Tools module test, vet and tidy-diff | Passed. |
| UI module verification | Passed. |
| `python3 scripts/check_planning.py` | Passed; 38 tasks, 60 acceptance cases and local links valid. |
| `git diff --check` for the D-01 product diff | Passed. |
| Public-data scan of D-01 paths | Passed; no credential, private host, user path, private key or real inventory found. |
| Fresh migration, v1-to-v2 upgrade, dirty migration and same-path second-open tests | Existing tests passed independently. Direct negative probes above expose uncovered cases. |

## Acceptance contribution assessment

- A-32: transaction helper and uniqueness constraints exist, but restart recovery
  is incomplete. Contribution remains unaccepted until the stranded-state finding
  closes. Full approval/queue crash injection remains W-02/V-01.
- A-36: same-action version/lease CAS exists. Cross-action reservations remain
  downstream, and the process-lock alias bypass prevents accepting the local
  single-owner contribution.
- A-43: migrations, dirty-state refusal, normal same-path locking and both Linux
  compile targets pass. Alias locking and restart recovery block acceptance.
- A-45: sqlc generation is clean and module checks run. Enum drift blocks the
  storage contract contribution. Root tidy remains a separately recorded future
  package-consumption gate.

## Deferred and not-run evidence

- Windows uses an exclusive lock file that can remain after a crash. Linux amd64
  and arm64 containers are the v0.0.1 baseline, so Windows stale-lock recovery is
  `post_v0.1.0` and does not block this round.
- Runtime proof that WAL, locking and sync work on target container storage stays
  under compatibility gate G-03 and V-02. Local-file WAL and FULL synchronous
  checks passed; no network-mounted SQLite support is claimed.
- Full worker crash injection, path/torrent reservations, filesystem effects,
  backup/restore and live service behavior remain assigned downstream. This
  review does not convert their absence into extra D-01 blockers.
- Check mode currently relies on the following Git diff to detect sqlc changes.
  Missing-output detection belongs to the existing coordinator-owned generation
  script follow-up recorded by C-02.

## Next round

Revise the same D-01 paths, regenerate sqlc, add the required negative tests and
rerun focused plus broad checks. Coordinator should record the correction commit
and dispatch a new independent reviewer. This review changed only this receipt.
