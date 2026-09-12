# X-13 correction round 2 handoff: preserve upstream error evidence

## Assignment

- Task ID and title: X-13, Migrate NZBGet adapter to standalone client.
- Correction round: 2.
- Owner and independent reviewer: `/root/f01_implementer`; `/root/f01_reviewer`.
- Coordinator checkpoint: `4f17bb2d65d7780129b6920af93452f119e85888`.
- Product base under review: `28a86ca9b7ca31e9adc2c14eaa356ed33ac0ee4c`.
- Owned paths: `internal/adapters/nzbget/`, `tests/fixtures/nzbget/`, and this
  correction handoff. `docs/execution/state.json`, root module files, scripts,
  X-12 paths and unrelated adapter files were not changed.
- Dependency: X-11 standalone NZBGet client public commit
  `1b9b3f9c425e56a7a3be6dfb43829157d08877c9`, resolved as
  `v0.0.0-20260911223922-1b9b3f9c425e`.
- Acceptance contributions: A-05, A-06 and A-09.

## Review findings and fixes

The correction product commit is
`f54ec97c70d75a58099b92e7ee8350220599d009`.

1. Standalone qBittorrent/NZBGet migration error translation now handles the
   standalone client's HTTP 501 path, which is emitted as an unavailable error
   with `StatusCode=501`: the adapter converts it to
   `domain.OutcomeUnsupported` with `Retryable=false`, preserving the legacy
   adapter contract and the standalone method classification.
2. JSON-RPC error code zero is retained as `UpstreamID="0"` for the
   standalone client's remote-error kind. Transport errors do not receive a
   fabricated upstream ID. This preserves an explicitly supplied zero code
   without confusing it with absent transport evidence.

The synthetic regression `TestStandaloneErrorClassificationTranslation`
exercises both paths through the public adapter `Version` method. It verifies
HTTP status, normalized outcome, retryability and the zero RPC identity. No
standalone error or generated DTO crosses the root adapter boundary.

## Verification

All commands below returned exit status 0 unless stated otherwise. The root
adapter checks use a temporary module manifest containing the public standalone
dependency; no local `replace` directive or `go.work` was used.

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/nzbget/inventory` with a temporary dependency modfile | Passed | focused NZBGet adapter suite |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/nzbget/inventory` with a temporary dependency modfile | Passed | focused adapter race suite |
| `GOWORK=off go vet -mod=readonly ./internal/adapters/nzbget/inventory` with a temporary dependency modfile | Passed | focused adapter vet |
| `(cd clients/nzbget && GOWORK=off go test -mod=readonly ./...)` | Passed | approved standalone client |
| `(cd clients/nzbget && GOWORK=off go test -mod=readonly -race ./...)` | Passed | standalone race suite |
| `(cd clients/nzbget && GOWORK=off go vet -mod=readonly ./...)` | Passed | standalone vet |
| `(cd clients/nzbget && GOWORK=off go mod verify)` | Passed: all modules verified | standalone module |
| `git diff --check` and `git diff --cached --check` | Passed | product and handoff whitespace checks |
| Product pre-commit hook | Passed: generation, API/Vacuum, architecture and fast guardrails | `.githooks/pre-commit`; product commit `f54ec97` |
| `GOWORK=off ./scripts/generate.sh --check` | Passed | committed generated artifacts |
| `python3 scripts/check_planning.py` | Passed: 43 tasks, 60 acceptance cases; local links resolve | planning ledger |
| `./scripts/check-lint.sh --architecture-only` | Passed: root and standalone import boundaries | architecture checks |
| `python3 scripts/check-architecture.py` | Passed | root architecture check |
| `GOWORK=off go mod verify` | Passed: all modules verified | root module |
| Live NZBGet, credentials, private endpoints, real media or mutations | Intentionally excluded | synthetic fixtures and `httptest` only |

## Review and integration

- Product commit: `f54ec97c70d75a58099b92e7ee8350220599d009`.
- Handoff commit: pending; this file is the only intended handoff change.
- Prior X-13 product and handoff: `28a86ca9b7ca31e9adc2c14eaa356ed33ac0ee4c`
  and `a5f7e51570850535bcaf63fa805b623a309c3bb9`.
- Reviewer: pending independent review by `/root/f01_reviewer`.
- Review receipt: pending at `docs/execution/handoffs/X-13-review-round2.md`.
- Coordinator integration and execution-state update: pending; the coordinator
  owns `docs/execution/state.json` and root module dependency integration.

## Resume checkpoint

- Product state: correction code and regression tests are committed at
  `f54ec97c70d75a58099b92e7ee8350220599d009`.
- The standalone dependency remains public and must be added to the root
  `go.mod`/`go.sum` by the coordinator using the pseudo-version recorded above.
- Next safe action: commit this handoff separately, then perform independent
  review and record the correction in execution state.
- No unrelated paths were staged, reset, rebased, cleaned or deleted.
