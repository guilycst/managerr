# D-01 implementation handoff, correction round nine

## Decision

`pending_independent_review`.

This handoff records the bounded fixes for the two P1 findings in
`D-01-review-round8.md`. It is the implementer handoff; `/root/d01_reviewer`
should write the independent receipt separately.

## Assignment and boundary

- Task: D-01, SQLite schema, migrations and sqlc repository.
- Review input: `docs/execution/handoffs/D-01-review-round8.md` at
  `d17f62313d5db71e37a0a9223418d5fed08ed49f`.
- Review baseline product: `5941e92cd9b50b6d1f32f1397025bda3fc8f6ddd`.
- Shared checkout baseline when correction started:
  `29768b6` (`docs(arr): hand off X-03 round-three correction`).
- Implementer: `/root/d01_implementer`; next reviewer: `/root/d01_reviewer`.
- Product commit:
  `eb2f8076f5aef8bfb33cb58f81ada53b7759a189`,
  `fix(storage): close terminal recovery gaps`.
- Owned product paths: `internal/storage/`, `migrations/`, `sqlc.yaml`, and
  generated SQLC output under `internal/storage/sqlc/`.
- Handoff path: `docs/execution/handoffs/D-01-round9.md`.
- `docs/execution/state.json`, Arr/NZBGet adapter files and other lane files
  were not staged or modified by this correction.

## Changes

Approved purge action runs remain fenced for the complete lifetime of their
janitor binding. Startup recovery, lease recovery, due listing and generic
action claiming now exclude every approval-bound janitor state, including
terminal `succeeded`, `failed`, `held` and `cancelled` states. The associated
action therefore cannot be reclaimed as a generic mutation while the terminal
janitor result still needs durable action finalization.

`FinalizeApprovedPurgeAction` is the exact read-only finalization primitive. It
uses a single versioned update to require the same action, ready plan and
revision/digest, approving decision, immutable `fs.delete` target, trash entry
version and nonempty manifest. It accepts a terminal janitor result even after
preview expiry, verifies the terminal entry generation, clears the read-only
lease, and records the action outcome. It maps a held janitor result to
`needs_review`; failed and cancelled results remain terminal failure/cancel
outcomes. A repeated or stale finalization returns no row, preserving CAS
idempotency.

The `000009_storage_recovery_boundaries.down.sql` compatibility path now
restores the round-eight reconciliation trigger's action fence. Its exact
claim validation requires the action generation to match the janitor's old
generation, then atomically copies the read-only worker lease and increments
the action version. A lost action update aborts the janitor claim. Existing
approval-bound rows are held before the downgrade trigger is installed, so a
downgrade cannot reopen an old conflicting lease; newly created v8 work keeps
the coupled action/janitor/trash generation contract. Migrations `000001`
through `000008` remain byte-identical.

## Regression coverage

`internal/storage/compatibility_round9_test.go` adds synthetic SQLite coverage
for:

1. Every terminal approved purge result (`succeeded`, `failed`, `held` and
   `cancelled`) keeping its action fenced from recovery, due listing and
   generic claim until exact finalization; finalization is versioned and
   repeated finalization is a no-op.
2. A v9 to v8 downgrade restoring the round-eight action fence for a populated
   approval-bound purge, followed by v8 read-only reclaim and v8 to v9
   re-upgrade with the same coherent generation.

No live media service, upstream write, filesystem mutation, credential,
private inventory or deployment was used.

## Verification

All commands below passed against product commit
`eb2f8076f5aef8bfb33cb58f81ada53b7759a189` or its staged equivalent:

| Command or scenario | Result |
| --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed all root packages. |
| `GOWORK=off go test -race -mod=readonly -count=1 ./internal/storage/...` | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `cd tools && GOWORK=off go tool github.com/sqlc-dev/sqlc/cmd/sqlc generate -f ../sqlc.yaml` | Passed; generated output clean. |
| `./scripts/generate.sh --check` | Passed. |
| `./scripts/check-api.sh` | Passed; Vacuum score 100/100 and API checks passed. |
| `GOWORK=off go mod verify` in root, `tools/` and `ui/` | Passed; all modules verified. |
| `cd tools && GOWORK=off go test -mod=readonly -count=1 ./...` | Passed. |
| `cd tools && GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `cd tools && GOWORK=off go mod tidy -diff` | Passed. |
| `GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed. |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases; local links resolve. |
| `python3 scripts/check-architecture.py` | Passed. |
| `git diff --check` | Passed. |
| Pre-commit guardrail (`generation`, API, architecture, fast checks) | Passed. |

## Acceptance contribution

- A-32: terminal approved purge journals cannot be mistaken for generic
  mutation work; exact read-only finalization is durable and idempotent.
- A-36: action fencing remains coupled to janitor recovery and is restored on
  v9 to v8 downgrade.
- A-43: populated downgrade and re-upgrade preserve safe approval-bound
  generations while ambiguous old work remains held.
- A-45: SQLC regeneration, root/tools/UI module verification, full root and
  storage race tests, vet, API, architecture, planning and Linux amd64/arm64
  compile checks pass.

Full worker fault injection, upstream effects, filesystem reservation and
backup/restore remain downstream acceptance work.

## Handoff

- Product commit: `eb2f8076f5aef8bfb33cb58f81ada53b7759a189`.
- Documentation commit: pending this handoff commit.
- Independent review: `/root/d01_reviewer` against the product commit above.
