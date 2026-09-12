# X-12 correction handoff, round three

## Assignment

- Task ID and title: X-12, qBittorrent adapter migration; file-seed sentinel correction.
- Owner and independent reviewer: `/root/c01_implementer`; `/root/d01_reviewer`.
- Correction base product SHA: `c1ce2d2dd5202801f09cad6420af864574b6cfef`.
- Coordinator dispatch checkpoint: `29b2a2a6e3dd0a09854f05ac27ad7f8b409ff9a1`.
- Review receipt addressed: `055da019de55082f9608cd365d4de4ab3d04e786` (X-12 independent review, round two).
- Prior correction handoff: `e4ea4f6cc424ee151a9134f5cd365ae25a69a6e7`.
- Branch/worktree ownership: shared checkout on `main`; coordinator owns integration and `docs/execution/state.json`.
- Owned product paths: `internal/adapters/qbittorrent/inventory/` and `tests/fixtures/qbittorrent/`.
- Owned evidence path: `docs/execution/handoffs/X-12-correction-round3.md`.
- Product commit: `37f62b515681f7ad37fc9b1d4597581f069320e5` (`fix(qbittorrent): preserve unknown file seed sentinel`).
- Required acceptance contributions: A-04, A-09 and A-28.

No state, task, plan, root module dependency, script, shared contract, X-13
path, credential, private coordinate, live service or upstream mutation changed.

## Finding fixed

The compatibility projection now accepts explicit file-row `seeds=-1` as the
established unknown sentinel. It remains in the bounded sidecar, translates to
`FileObservation.Seeds == -1`, retains the complete file and mapped payload
observations, and emits `item_0_file_seeds_unknown`. Omitted `seeds` continues
to use the same sentinel and reason. Known zero and positive values remain
unchanged. Values below `-1`, wrong JSON types, fractional/exponent numbers,
null, overflow and malformed rows remain fail-closed; they reach the native
strict decoder and produce item-level `files_malformed` evidence without
publishing partial file rows.

Synthetic mixed-row regressions cover unknown sentinels in the first row,
later row and multiple rows, omitted seed members, known plus unknown rows, and
below-sentinel rejection. Existing full fixtures continue to cover positive
seeds, file mapping, payload retention, availability unknown evidence and
read-only behavior.

## Verification

Commands below ran in the shared checkout and returned exit status 0 unless
stated otherwise. Product checks ran before product commit; the product commit
hook also ran the fast guardrail set successfully.

| Command | Result / evidence |
| --- | --- |
| `GOWORK=off go test -mod=readonly ./... -count=1` | Passed root packages, including qBittorrent adapter. |
| `GOWORK=off go test -mod=readonly -race ./internal/adapters/qbittorrent/inventory -count=1` | Passed focused adapter race suite. |
| `GOWORK=off go test -mod=readonly -race ./... -count=1 -timeout=360s` | Passed full root race suite; storage completed in 216.298s. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed root vet. |
| `GOWORK=off go mod verify` | Passed: `all modules verified`. |
| `(cd clients/qbittorrent && GOWORK=off go test -mod=readonly ./... -count=1)` | Passed standalone and generated packages. |
| `(cd clients/qbittorrent && GOWORK=off go test -mod=readonly -race ./... -count=1)` | Passed standalone race suite. |
| `(cd clients/qbittorrent && GOWORK=off go vet -mod=readonly ./...)` | Passed standalone vet. |
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

- Product is ready at `37f62b515681f7ad37fc9b1d4597581f069320e5`.
- This handoff is a separate docs-only commit; prior X-12 handoffs and review
  receipts remain unchanged.
- Coordinator must record product and handoff SHAs in `docs/execution/state.json`;
  this worker did not edit that file.
- Independent review of the exact product SHA is pending `/root/d01_reviewer`.

## Remaining risks

The standalone read contract remains a qBittorrent 5/WebAPI 2 compatibility
candidate and has no live product run. Descriptor export remains an adapter-only
GET path outside the standalone module and retains its separate session risk.
Projection failures continue to fall through to native strict decoding, yielding
sanitized malformed evidence; explicit `-1` is intentionally retained as
unknown per the existing adapter contract.

## Resume checkpoint

- Product commit: `37f62b515681f7ad37fc9b1d4597581f069320e5`.
- Handoff path: `docs/execution/handoffs/X-12-correction-round3.md`.
- Next safe action: independent review against exact product SHA, then
  coordinator integration and state update.
- No conflicting files were staged, reset, rebased, cleaned or deleted.
