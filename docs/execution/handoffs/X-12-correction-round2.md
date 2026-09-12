# X-12 correction handoff, round two

## Assignment

- Task ID and title: X-12, qBittorrent adapter migration; compatibility projection correction.
- Owner and independent reviewer: `/root/c01_implementer`; `/root/d01_reviewer`.
- Correction base product SHA: `cb0c5db5cb4460e9fb8dacc4027ab09ada6dfda3`.
- Coordinator dispatch checkpoint: `f7e8a2a5d9c77a95cf17e6112385629fcc829a08`.
- Review receipt addressed: `a8723268fda092d8efc9dda118bd6126f510247d` (X-12 independent review, round one).
- Branch/worktree ownership: shared checkout on `main`; coordinator owns integration and `docs/execution/state.json`.
- Owned product paths: `internal/adapters/qbittorrent/inventory/` and `tests/fixtures/qbittorrent/`.
- Owned evidence path: `docs/execution/handoffs/X-12-correction-round2.md`.
- Product commit: `c1ce2d2dd5202801f09cad6420af864574b6cfef` (`fix(qbittorrent): validate compatibility projection`).
- Required acceptance contributions: A-04, A-09 and A-28.

No state, task, plan, root module dependency, script, shared contract, X-13
path, credential, private coordinate, live service or upstream mutation changed.

## Findings fixed

### Legacy sidecar validation

Compatibility fields are now checked before removal from strict native payloads.
For each top-level array row, `infohash_v1` and `infohash_v2` must be either
empty or exact 40/64-character hexadecimal strings respectively; surrounding
whitespace, invalid lengths and non-hex values fail. `has_metadata` must be a
JSON boolean. File `seeds` must be a JSON integer in the nonnegative `int`
range; fractional, exponent, null, string, negative and overflowing values
fail. Duplicate semantic members, including escaped names that decode to the
same key, fail before projection. Repeated members in separate rows remain
independent and valid. Any failed projection returns original bounded bytes to
the standalone decoder, so the strict decoder rejects the response instead of
publishing a last-wins or missing sidecar value.

### Endpoint and path-aware projection

The transport now uses separate projections for `/torrents/info` and
`/torrents/files`. Identity and metadata fields are removed only from the
immediate object of an info array row; `seeds` is removed only from the
immediate object of a files array row. Same names in nested objects, the other
endpoint's row schema, non-array top-level values, unknown members and
malformed/trailing values stay in the bytes sent to the standalone strict
client, which rejects them. The bounded original response remains captured
only for adapter-local identity and file-seed observations after native decode
succeeds.

Synthetic regressions cover valid full rows, absent/empty optional identities,
zero seeds, equal and unequal duplicate identity/metadata/seed members,
escaped duplicate names, wrong scalar/container/null types, invalid identity
length/hex/whitespace, invalid seed range, nested legacy names, cross-endpoint
members, strict native rejection and invalid UTF-8 preservation. Existing full
qBittorrent fixtures continue to exercise descriptor identity, v1/v2 mapping,
file-seed retention and pagination.

## Verification

Commands below ran in the shared checkout and returned exit status 0 unless
stated otherwise. Product checks ran before product commit; the product commit
hook also ran the fast guardrail set successfully.

| Command | Result / evidence |
| --- | --- |
| `GOWORK=off go test ./...` | Passed root packages, including qBittorrent adapter. |
| `GOWORK=off go test -race ./internal/adapters/qbittorrent/inventory -count=1` | Passed focused adapter race suite. |
| `GOWORK=off go vet ./...` | Passed root vet. |
| `GOWORK=off go mod verify` | Passed: `all modules verified`. |
| `(cd clients/qbittorrent && GOWORK=off go test ./... -count=1)` | Passed standalone and generated packages. |
| `(cd clients/qbittorrent && GOWORK=off go test -race ./... -count=1)` | Passed standalone race suite. |
| `(cd clients/qbittorrent && GOWORK=off go vet ./...)` | Passed standalone vet. |
| `(cd clients/qbittorrent && GOWORK=off go mod verify)` | Passed: `all modules verified`. |
| `./scripts/generate.sh --check` | Passed generated output and staged generation checks. |
| `./scripts/check-api.sh` | Passed; Vacuum quality 100/100. |
| `./scripts/check-lint.sh` | Passed with zero lint issues and architecture boundaries. |
| `python3 scripts/check-architecture.py` | Passed import boundaries. |
| `python3 scripts/check_planning.py --self-test` | Passed: 43 tasks, 60 acceptance cases; local links resolve. |
| `./scripts/check-guardrails.sh --fast` | Passed fast guardrails. |
| `./scripts/check-guardrails.sh --ci` | Passed full five-module tests, lint, architecture, generation, API, vet and module verification. |
| `gofmt -d internal/adapters/qbittorrent/inventory/inventory.go internal/adapters/qbittorrent/inventory/inventory_test.go` | Passed with no output. |
| `git diff --check` | Passed. |
| Product commit hook | Passed fast guardrails. |
| Live qBittorrent, credentials, private inventories, upstream writes and filesystem mutations | Intentionally not run; all evidence uses synthetic fixtures and `httptest`. |

## Review and integration

- Product is ready at `c1ce2d2dd5202801f09cad6420af864574b6cfef`.
- This handoff is a separate docs-only commit; the round-one `X-12.md` handoff
  and review receipt remain unchanged.
- Coordinator must record product and handoff SHAs in `docs/execution/state.json`;
  this worker did not edit that file.
- Independent review of the exact product SHA is pending `/root/d01_reviewer`.

## Remaining risks

The standalone read contract remains a qBittorrent 5/WebAPI 2 compatibility
candidate and has no live product run. Descriptor export remains an adapter-only
GET path outside the standalone module and retains its separate session risk.
Projection errors intentionally fall through to native strict decoding, yielding
sanitized native malformed evidence; file-schema failures remain item-level
partial observations under the existing adapter contract.

## Resume checkpoint

- Product commit: `c1ce2d2dd5202801f09cad6420af864574b6cfef`.
- Handoff path: `docs/execution/handoffs/X-12-correction-round2.md`.
- Next safe action: independent review against exact product SHA, then
  coordinator integration and state update.
- No conflicting files were staged, reset, rebased, cleaned or deleted.
