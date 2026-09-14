# F-02 discovery correction handoff, round one

## Assignment

- Task ID and title: F-02, discovery groups and media readiness; correction round one.
- Owner/agent and independent reviewer: `/root/d02_implementer`; independent review by `/root/d02_reviewer`.
- Exact dispatch checkpoint: `adad9e93d5c1cb53ce721d77dcb5c0e3e438d2ce`.
- Review receipt: `30bca5041495eed51ea150b809332403d5c17eb3`.
- Branch/worktree or shared-checkout ownership: shared `main` checkout; coordinator owns integration and `docs/execution/state.json`.
- Owned paths: `internal/discovery/` and this handoff only. F-04 placement, state, tasks, scripts, modules, adapters and UI paths were outside this lane.
- Required acceptance contributions: A-01, A-02, A-10 and A-11.

## Contract and work

This correction closes all five P1 findings in review receipt `30bca5041495eed51ea150b809332403d5c17eb3` while preserving read-only discovery and the existing domain/port boundaries:

- Filesystem enumeration keeps a per-directory set of opaque continuation cursors and a scan-wide page budget. Non-adjacent cursor cycles and unending empty pages stop with partial coverage and stable reason codes instead of waiting for caller cancellation. Client inventory pagination also detects cycles and marks page/item limits on the affected connection coverage.
- Incomplete, unknown or truncated client coverage remains visible per connection and cannot certify completion or readiness. A correlated unknown item dominates aggregate completion state. Missing client coverage is treated as insufficient evidence for a complete aggregate. Review reasons retain client inventory incompleteness.
- Client completion now retains authoritative connection-scoped `ClientItemObservation` records, including connection, external ID/hash, state and completion time. Legacy single-connection projections remain for compatibility, while equal IDs across client instances remain distinct in the authoritative item evidence and provenance.
- Root file groups use an explicit `file:` namespace so a root `Movie.mkv` cannot collide with a `Movie/` directory. Multiple ambiguous movie files remain mixed; season-pack classification requires episode or season evidence. Parenthesized year tokens do not become anime absolute numbers, and subtitle language parsing skips `forced`, `sdh` and `hi` labels while retaining their flags.
- `MemoryStore`, scan results and list results deep-clone all pointer-bearing evidence: association numbers, provenance paths/times, client completion times, stability times and filesystem/client coverage timestamps. Mutating a caller-owned result cannot alter persisted history or a later read.

No registration, import, filesystem mutation, upstream write, live service, credential, private coordinate or real media inventory was used. Fixtures are synthetic and all client/filesystem access remains through read-only ports.

## Verification

| Command or scenario | Result / exit status | Evidence |
| --- | --- | --- |
| `gofmt -w internal/discovery/discovery.go internal/discovery/discovery_test.go` and `git diff --check` | passed, exit 0 | Scoped discovery files |
| `GOWORK=off go test -count=1 ./internal/discovery` | passed, exit 0 | Focused discovery suite |
| `GOWORK=off go test -race -count=1 ./internal/discovery` | passed, exit 0 | Focused race suite |
| `GOWORK=off go vet ./internal/discovery` | passed, exit 0 | Focused vet |
| `GOWORK=off go mod verify` | passed; all modules verified | Root dependency verification |
| `GOWORK=off go test -mod=readonly ./... -count=1` | passed, exit 0 | Full root package matrix |
| `GOWORK=off go test -race -count=1 ./internal/... ./tests/compatibility/writes` | passed, exit 0; storage completed in 219.713s | Full root internal race matrix |
| `GOWORK=off go vet -mod=readonly ./...` | passed, exit 0 | Full root vet |
| `./scripts/generate.sh --check` | passed, exit 0 | Reproducible generated output |
| `./scripts/check-api.sh` | passed, exit 0; Vacuum quality 100/100 with zero warnings/errors | Bundled OpenAPI |
| `./scripts/check-lint.sh` | passed, exit 0; all lint and architecture checks reported zero issues | Root/UI/tools/client lint and boundaries |
| `python3 scripts/check_planning.py --self-test && python3 scripts/check_planning.py` | passed, exit 0; 44 tasks, 60 acceptance cases | Planning ledger |
| `./scripts/check-guardrails.sh --fast` | passed, exit 0 | Fast guardrail runner |
| `./scripts/check-guardrails.sh --ci` | passed, exit 0 | CI-equivalent root/UI/tools/client checks, generation, API, lint and module verification |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=readonly -count=1 -run '^$' -exec=true ./internal/...` | passed, exit 0 | Linux amd64 compile matrix |
| `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go test -mod=readonly -count=1 -run '^$' -exec=true ./internal/...` | passed, exit 0 | Linux arm64 compile matrix |
| `TestScannerBoundsNonAdjacentFilesystemCursorCycles` and `TestScannerBoundsFilesystemPageBudget` | passed | Empty-page cycle and finite page budget stop safely |
| `TestClientCoverageAndUnknownItemsCannotClaimReady` and `TestClientItemLimitMarksAffectedConnectionCoverage` | passed | Partial, unknown and truncated client evidence cannot claim ready |
| `TestClientCompletionKeepsConnectionScopedIdentities` | passed | Equal external IDs from two connections remain distinct |
| `TestGroupingKeepsAmbiguityAndRootNamespacesExplicit` and `TestMediaAndSubtitleClassificationRejectsYearAsAnimeAndLabelAsLanguage` | passed | Conservative groups, year handling and subtitle labels |
| `TestMemoryStoreDeepClonesPointerBearingEvidence` | passed | Mutation-after-scan and mutation-after-list regressions |
| Product pre-commit hook | passed, exit 0 | Product commit `949f4af065aeb8e2dcf13d3ddd7a21ccb593f2ec` |

All checks used synthetic data. The parallel API/generation attempt briefly hit a shared Git `index.lock`; after the concurrent read-only checks completed, the standalone generation check passed sequentially. No lock or worktree data was removed.

## Review and integration

- Product commit: `949f4af065aeb8e2dcf13d3ddd7a21ccb593f2ec` (`fix(discovery): close review findings`).
- Reviewed product and findings: `12dfe8c0b39acd00f6f3cb72d3e0ff4ded140ab0`, review receipt `30bca5041495eed51ea150b809332403d5c17eb3`.
- Fix product and regression evidence: product commit above adds bounded cursor/page traversal, per-source client coverage, connection-scoped completion items, conservative classification and deep-clone tests.
- Handoff commit: pending; this file is intentionally committed separately from the product.
- Final reviewer decision: pending independent review of the exact product SHA.
- Coordinator integration and execution-state update: pending; this lane did not edit or stage `docs/execution/state.json`.

## Resume checkpoint

- Product is complete at `949f4af065aeb8e2dcf13d3ddd7a21ccb593f2ec`; the scanner remains read-only, bounded and conservative when evidence is incomplete or ambiguous.
- Next safe action: independently review the exact product SHA, rerun the adversarial discovery scenarios and record the product and handoff SHAs in the coordinator ledger.
- Remaining non-blocking consideration: downstream SQLite persistence and API mapping must carry the authoritative connection-scoped client item observations; those integration paths remain coordinator-owned.
- No conflicting files were removed, reset, rebased or force-pushed. Concurrent coordinator state edits remain outside this handoff's staging scope.
