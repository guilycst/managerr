# D-01 implementation handoff, correction round fourteen

## Decision

`pending_independent_review`.

This handoff records the embedded-byte correction requested by the independent
round-thirteen storage review. `/root/f01_reviewer` should review the product
commit below independently and write its receipt separately.

## Assignment and boundary

- Task: D-01, SQLite schema, migrations and sqlc repository.
- Review input: round-thirteen receipt at
  `docs/execution/handoffs/D-01-review-round13.md`, receipt
  `9265acfa62c46b667978f05264a44da4ba5f641a`.
- Review baseline product:
  `bda29901e9ffc06a61632da171ce853bd9708261`.
- Previous handoff:
  `docs/execution/handoffs/D-01-round13.md`, receipt
  `0437c149cc32b41c3351c2671bcdd4e476c397fe`.
- Shared checkout queue commit:
  `aba918130e5b79b54f5b068a086b66b76f12bb54` (the coordinator's round-fourteen
  dispatch record).
- Implementer: `/root/f01_implementer`; reviewer: `/root/f01_reviewer`.
- Product commit:
  `8098e2b552737e5e18848d27b665991fddce18bf`,
  `fix(storage): reject embedded cancellation bytes`.
- Owned product paths: `internal/storage/`, `migrations/`, `sqlc.yaml`, and
  generated SQLC output under `internal/storage/sqlc/`.
- Handoff path: `docs/execution/handoffs/D-01-round14.md`.
- `migrations/` and `sqlc.yaml` are unchanged. Coordinator state and other
  adapter or fixture files were not staged by this lane.

## Contract and work

`RequestActionCancellation` now rejects embedded NUL and non-ASCII bytes before
SQLite text functions inspect the requested timestamp. The SQL boundary checks
the bound value as a BLOB with `instr(CAST(... AS BLOB), X'00') = 0`, closing the
SQLite behavior where `length`, `substr`, and `GLOB` stop at an embedded NUL.
It also requires the entire bound text to be printable ASCII with
`NOT GLOB '*[^ -~]*'`; the existing exact RFC3339 shape checks continue to
reject all other trailing data.

The correction is intentionally before the prior field, date, fraction, and
zone checks. It therefore rejects a valid-looking timestamp followed by a NUL
and arbitrary bytes without advancing the action generation or persisting the
cancellation marker. It also rejects UTF-8 and invalid raw non-ASCII trailing
bytes. Valid UTC, numeric-offset, fractional, and leap-day RFC3339 cases remain
accepted. The round-twelve exact purge-effect identity and state/outcome
consistency rules, and the round-thirteen strict calendar/timestamp checks,
remain unchanged.

## Regression coverage

`internal/storage/compatibility_round11_test.go` adds
`TestRound14CancellationRejectsEmbeddedNULAndNonASCII` with these adversarial
requests:

- valid UTC timestamp followed by `NUL + "trailing"`, reproducing the review
  finding;
- NUL before the zone marker;
- UTF-8 non-ASCII trailing data;
- an invalid raw non-ASCII byte after the otherwise valid timestamp.

Each case asserts `sql.ErrNoRows`, an unchanged action version and update time,
and no cancellation marker. Earlier round-twelve invalid-input tests continue
to assert unchanged action, janitor, trash-entry, marker, lease, version and
update state; round-thirteen valid and idempotent timestamp cases remain green.
No live media service, upstream write, filesystem mutation, credential,
private inventory, or deployment was used.

## Verification

| Command or scenario | Commit / fixture version | Result / exit status | Evidence path |
| --- | --- | --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage -run 'Round14|Round13|Round12' -v` | product `8098e2b`; synthetic SQLite byte fixtures | Passed; NUL, non-ASCII, strict timestamp, idempotence and prior invalid-input cases passed | `internal/storage/compatibility_round11_test.go` |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/storage/...` | product `8098e2b` | Passed in 16.623s | `internal/storage/` |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=300s ./internal/storage/...` | product `8098e2b` | Passed in 226.751s | `internal/storage/` |
| `GOWORK=off go vet -mod=readonly ./internal/storage/...` | product `8098e2b` | Passed | `internal/storage/` |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | product `8098e2b` | Passed for all root packages in 17.817s | root packages |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | product `8098e2b` | Passed for all root packages; storage completed in 227.392s | root packages |
| `GOWORK=off go vet -mod=readonly ./...` | product `8098e2b` | Passed | root packages |
| `cd tools && GOWORK=off go tool github.com/sqlc-dev/sqlc/cmd/sqlc generate -f ../sqlc.yaml` | pinned SQLC tool | Passed; generated SQLC updated with the byte guards | `internal/storage/sqlc/` |
| Generated query scan for unexpanded `sqlc.arg(` | product `8098e2b` | Passed; no unexpanded arguments remain | `internal/storage/sqlc/query.sql.go` |
| `./scripts/generate.sh --check` | product `8098e2b` | Passed | generated outputs |
| `./scripts/check-api.sh` | product `8098e2b` | Passed; Vacuum quality 100/100 | `api/openapi.yaml` |
| `python3 scripts/check-architecture.py` | product `8098e2b` | Passed | root import boundaries |
| `python3 scripts/check_planning.py` | product `8098e2b` | Passed; 38 tasks, 60 acceptance cases and local links resolve | planning/execution docs |
| `GOWORK=off go mod verify && GOWORK=off go mod tidy -diff` | product `8098e2b` | Passed | root module |
| `cd tools && GOWORK=off go test -mod=readonly -count=1 ./... && GOWORK=off go vet -mod=readonly ./... && GOWORK=off go mod verify && GOWORK=off go mod tidy -diff` | product `8098e2b` | Passed | `tools/` |
| `cd ui && GOWORK=off go test -mod=readonly -count=1 ./... && GOWORK=off go vet -mod=readonly ./... && GOWORK=off go mod verify` | product `8098e2b` | Passed | `ui/` |
| `GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | product `8098e2b` | Passed host compile-only check | root packages |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | product `8098e2b` | Passed | root packages |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | product `8098e2b` | Passed | root packages |
| `./scripts/check-guardrails.sh --fast` | product `8098e2b` | Passed | guardrail script |
| Product commit hook | product `8098e2b` | Passed generation, API, architecture, tests and fast guardrails | commit hook output |
| `git diff --check` | product and handoff changes | Passed | product and handoff paths |

All planned checks for this correction ran successfully. No checks are
skipped or blocked.

## Review and integration

- Reviewer identity/role and reviewed commit: pending independent reviewer
  `/root/f01_reviewer` against
  `8098e2b552737e5e18848d27b665991fddce18bf`.
- Findings with severity, reproduction and contract reference: this round
  addresses the round-thirteen P2 where an embedded NUL made
  `2026-09-11T00:01:13Z\x00trailing` appear valid because SQLite text
  functions stopped at the NUL. The new byte guard and regression cover the
  reproduction while preserving the strict RFC3339 contract.
- Fix commit and regression evidence: product commit above; focused Round14
  test and the full storage/root race suites passed.
- Final reviewer decision: pending.
- Integrated commit, recorded by coordinator: pending; coordinator owns
  `docs/execution/state.json`.

## Resume checkpoint

- Current state and outstanding uncertainty: the byte-boundary correction is
  committed at `8098e2b`; independent review remains outstanding.
- Active process or agent ownership, sanitized: no D-01 product files are
  being modified after the product commit; unrelated coordinator changes were
  preserved and excluded from the product commit.
- Next safe action: `/root/f01_reviewer` reviews the exact product commit,
  especially NUL detection on BLOB-bound parameters, non-ASCII handling,
  SQLC substitution, no-row rollback behavior, and preserved strict valid
  timestamp/effect/cancellation invariants. Coordinator then records the
  review and integration in state.
- Blocker and exact input/evidence needed: no product blocker; need the
  independent reviewer receipt and coordinator integration decision.
- No conflicting writes or unknown files removed: confirmed; only the three
  D-01 product paths and this handoff will be committed by this lane.

## Handoff

- Product commit: `8098e2b552737e5e18848d27b665991fddce18bf`.
- Documentation commit: pending this handoff commit.
- Independent review: `/root/f01_reviewer` against the product commit above.
- `docs/execution/state.json` remains coordinator-owned; this handoff does not
  edit it.
- Resume from the product commit, then apply this documentation commit while
  preserving unrelated shared-checkout changes.
