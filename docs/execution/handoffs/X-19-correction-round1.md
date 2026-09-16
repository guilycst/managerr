# X-19 Arr read-adapter correction round 1

## Assignment

- Task ID and title: X-19, migrate the Arr read adapter to standalone Sonarr and Radarr clients; correction round 1.
- Owner: `/root/x05_implementer`.
- Independent reviewer: `/root/x05_reviewer`.
- Correction base checkpoint: `5d9a14ca800c7a17634f8a25dfc29b758b25fe66`.
- Prior product: `a03066b4923cb51d4797bdd40eaedc3dad9305b3`.
- Prior review receipt: `docs/execution/handoffs/X-19-review-round1.md`.
- Branch/worktree: shared `main`; coordinator owns `docs/execution/state.json`.
- Owned product paths: `internal/adapters/arr/read/`, `tests/fixtures/arr/read/`, root `go.mod`, and `go.sum`.
- Owned handoff path: this file.
- Product commit: `4384a873d54df46b6d0f996a3bc03f6a2bde2edd`.
- Dependencies: published Sonarr and Radarr client modules at
  `v0.0.0-20260915230044-873e53ef6729`; no local `replace` or `go.work`.
- Acceptance contributions: A-07, A-08, A-09, A-10, A-16; standalone module policy.

## Correction result

The root adapter now applies one native file identity registry across each
catalog snapshot and each import observation. A repeated upstream file ID is
accepted only when its mapped path and size agree. Distinct upstream file IDs
cannot claim the same mapped path. Contradictory entries are omitted with a
reason and leave catalog coverage partial; import observations return a
sanitized unknown/incomplete result. Sonarr still permits one file ID to map to
multiple episodes when all physical details agree. Radarr movie identity is
validated by the standalone client and root translation before registry use.

Strict native failures no longer fall through to the old decoder by error code
alone. The adapter captures only one bounded, complete native response body per
serialized native operation and permits fallback only for an operation-specific,
explicitly recognized legacy shape. Legacy decoding runs through a strict JSON
validator that rejects invalid UTF-8, duplicate object members, malformed or
trailing JSON, and excessive nesting. Catalog fallback requires legacy rows with
an identity and without native `path` or `monitored` markers; preview fallback
requires all native file identity fields plus the retained old rejection alias;
file read-back fallback remains closed until an older shape is specified. Native
transport/status/identity failures and incomplete observations cannot use this
fallback. Native operations on one adapter are serialized while response
evidence is matched to its endpoint path.

The Radarr manual-import fixture now includes the native language ID required by
the standalone strict contract. No generated client type, upstream response
body, credential, endpoint, inventory, filesystem path or write capability
crosses the root boundary. Arr registration/import execution remains disabled;
G-01 stays open.

## Verification

All commands below ran from the product tree ending at
`4384a873d54df46b6d0f996a3bc03f6a2bde2edd`, or against the staged product tree
where stated. Synthetic `httptest` fixtures were used.

| Command or scenario | Result / exit status | Evidence |
| --- | --- | --- |
| `GOWORK=off go test -mod=readonly ./internal/adapters/arr/read -run 'TestArrNative|TestArrManualImportRedirectsNeverReachCommand' -count=1 -v` | Exit 0; native translation, collisions, strict malformed/UTF-8 rejection, explicit legacy shape, typed errors and redirect tests passed. | `internal/adapters/arr/read/native_test.go` |
| `GOWORK=off go test -mod=readonly -race -count=3 -timeout=180s ./internal/adapters/arr/read` | Exit 0; `24.703s`. | `internal/adapters/arr/read/native_test.go` and existing adapter tests |
| `GOWORK=off go test -mod=readonly ./...` | Exit 0; all root packages passed. | root module |
| `GOWORK=off go vet -mod=readonly ./...` | Exit 0. | root module |
| `GOWORK=off go mod verify` | Exit 0; `all modules verified`. | root module |
| `(cd clients/sonarr && GOWORK=off go test -mod=readonly ./... && GOWORK=off go test -mod=readonly -race -count=3 -timeout=180s ./... && GOWORK=off go vet -mod=readonly ./... && GOWORK=off go mod verify)` | Exit 0; tests, race, vet and module verification passed. | `clients/sonarr/` |
| `(cd clients/radarr && GOWORK=off go test -mod=readonly ./... && GOWORK=off go test -mod=readonly -race -count=3 -timeout=180s ./... && GOWORK=off go vet -mod=readonly ./... && GOWORK=off go mod verify)` | Exit 0; tests, race, vet and module verification passed. | `clients/radarr/` |
| `GOWORK=off GOPROXY=off GOSUMDB=off ./check-generation.sh` in `clients/sonarr` and `clients/radarr` | Exit 0 for both; committed generated output reproduced offline. | each client `check-generation.sh` |
| `python3 scripts/check_planning.py` | Exit 0; `53 tasks, 60 acceptance cases; local links resolve.` | planning scripts |
| `python3 scripts/check-architecture.py` | Exit 0; import boundaries passed. | architecture checker |
| `GOWORK=off ./scripts/check-lint.sh` | Exit 0; all nine module entries reported `0 issues.` | lint/architecture checker |
| `GOWORK=off ./scripts/check-api.sh` | Exit 0; generation and Vacuum quality `100/100`, zero warnings/errors. | API checker |
| `GOWORK=off GOPROXY=off GOSUMDB=off ./scripts/check-guardrails.sh --fast` | Exit 0; generation, staged generation, API/Vacuum, architecture, format and targeted tests passed. | fast guardrail checker |
| Product pre-commit hook during product commit | Exit 0; fast guardrail checks passed. | `.githooks/pre-commit` |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` | Exit 0. | root module |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` | Exit 0. | root module |
| Linux amd64/arm64 CGO-free builds in each Sonarr and Radarr module | Exit 0 for all four builds. | `clients/sonarr/`, `clients/radarr/` |
| `git diff --check` and owned-file `gofmt` | Exit 0 before product commit. | owned product paths |

The full aggregate `./scripts/check-guardrails.sh --ci` was not rerun by this
correction lane; C-06 owns that repository-wide matrix. No live Arr instance or
mutation was used.

## Review and integration

- Prior review findings: R1 P1, contradictory native file identities could be
  emitted as complete; R2 P1, broad malformed-native fallback could re-decode
  duplicate-key JSON with last-wins semantics.
- Fix commit: `4384a873d54df46b6d0f996a3bc03f6a2bde2edd`.
- Regression evidence: native Radarr/Sonarr catalog and ObserveImport collision
  tests, valid Sonarr multi-episode reuse, duplicate/missing native identity,
  invalid UTF-8 and explicit legacy catalog tests all pass under repeated race
  execution.
- Final reviewer decision: pending independent re-review.
- Coordinator state update: pending; this lane did not edit `state.json`.

## Resume checkpoint

- Product correction is committed at
  `4384a873d54df46b6d0f996a3bc03f6a2bde2edd`.
- Next safe action: commit this handoff separately, then assign independent
  review against the exact product tree.
- Outstanding uncertainty: old compatibility fallback remains intentionally
  narrow; unsupported legacy file read-back shapes remain unknown until a
  versioned contract is added. Native Arr writes and G-01 remain blocked.
- No conflicting writes or unknown files were removed.
