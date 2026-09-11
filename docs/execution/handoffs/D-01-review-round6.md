# D-01 implementation handoff, correction round six

## Decision

`pending_independent_review`.

This is the implementation handoff for the two P1 findings in
`docs/execution/handoffs/D-01-review-round5.md`; it is not an independent
review decision.

## Review identity and boundary

- Reviewed input: round-five receipt at
  `docs/execution/handoffs/D-01-review-round5.md`.
- Reviewer of that input: `/root/d01_reviewer`.
- Reviewer product commit supplied for this round:
  `d6837ed31f09c26e797a6e5d2b24982c5b6cbb61`.
- Dispatch base: `d2e0566f68d2e64199037dc320b40c4426ee4f1c`.
- Implementation commit: `5829da918aecad00346075b9f1cb29612f196e0d`.
- Owner: `/root/d01_implementer`; next reviewer: `/root/d01_reviewer`.
- Owned paths: `internal/storage/`, `migrations/`, `sqlc.yaml`, and this
  handoff. Shared coordinator state and unrelated adapter changes were left
  untouched.

## Findings addressed

The new ordered migration `000007_storage_recovery_safety` keeps migration
files 000001 through 000006 immutable. Before installing the v6 claim guards,
it identifies every populated early-purge target whose digest is inconsistent,
whose timestamps are malformed, or whose target was created after approval.
It retains the target as immutable evidence, moves affected purge janitors and
trash entries to `held`, and moves nonterminal action runs to `needs_review`.
The new chronology guard and SQLC claim query require a parseable target and
decision timestamp with `target.created_at <= decision.created_at`.

The migration also adds the dedicated
`ClaimApprovedEarlyPurgeReconciliation` CAS. After a valid approved claim is
recovered from a restart, the action run remains `reconciling` and unleased,
while the exact janitor and purging trash entry receive a new read-only
reconciliation lease. The CAS validates the immutable plan revision/digest,
approval decision, target intent/manifest/version, action identity/version,
trash operation/version, worker/lease, cancellation state, live action
deadline, due time and chronology. Generic janitor claiming remains blocked
for approval-bearing rows. Incomplete legacy approval evidence cannot use the
dedicated route.

## Regression evidence

`internal/storage/compatibility_round6_test.go` provides populated synthetic
fixtures for:

1. v5 approval at `00:00:30`, target created at `00:00:40`, and queued action/
   janitor work. Upgrade holds the target's work and both claim routes reject
   it while preserving the target timestamp.
2. An exact target created before approval. Initial approved claim, close/open,
   startup recovery, dedicated lease reacquisition and successful resolution
   produce `succeeded/purged/succeeded` for janitor/trash/action.
3. An incomplete legacy approval. Upgrade leaves it held and both generic and
   recovery claims reject it.

## Checks

All of the following passed after implementation commit
`5829da918aecad00346075b9f1cb29612f196e0d`:

| Check | Result |
| --- | --- |
| `GOWORK=off go test -count=1 ./...` | Passed. |
| `GOWORK=off go test -count=1 ./internal/storage/... ./migrations` | Passed. |
| `GOWORK=off go test -race -count=1 ./internal/storage/...` | Passed in 73.154 seconds. |
| Focused `TestRound4MigrationHistoryIsImmutable`, `TestRound5*` and `TestRound6*` | Passed. |
| `GOWORK=off go vet ./...` | Passed. |
| `./scripts/generate.sh --check` | Passed. |
| Pinned SQLC generation from `tools/` | Passed. |
| Root, tools and UI `go mod verify` | Passed. |
| Tools `go test ./...`, `go vet ./...`, `go mod tidy -diff` | Passed. |
| Linux amd64/arm64 `CGO_ENABLED=0 GOOS=linux GOARCH=... go test -c` | Passed compile-only builds. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases. |
| `git diff --check` and staged diff check | Passed. |

No credentials, private hostnames, tracker data, live media paths, upstream
writes, filesystem mutations, deployment or release operations were used.

## Handoff

- Product commit: `5829da918aecad00346075b9f1cb29612f196e0d`.
- Documentation commit: pending this handoff commit.
- Independent review: pending `/root/d01_reviewer`.
