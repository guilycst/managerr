# F-02 discovery correction handoff, round two

## Assignment

- Task ID and title: F-02, discovery groups and media readiness; correction round two.
- Owner/agent and independent reviewer: `/root/d02_implementer`; `/root/x05_reviewer`.
- Exact dispatch checkpoint: `5c546fc6e50929c5024d5b94a33282c95e8b7ff8`.
- Reviewed product: `949f4af065aeb8e2dcf13d3ddd7a21ccb593f2ec`.
- Review receipt: `ffe5b8a96634f0e4605977f20e82454949f13d03`, expected at `docs/execution/handoffs/F-02-review-round3.md`.
- Branch/worktree or shared-checkout ownership: shared `main` checkout; coordinator owns integration and `docs/execution/state.json`.
- Owned paths: `internal/discovery/` and this handoff only. No F-04, state, task, module, script, adapter or UI paths were changed.
- Required acceptance contributions: A-01, A-02, A-10 and A-11.

## Contract and work

This correction closes the remaining P1/A-11 subtitle language finding from review receipt `ffe5b8a96634f0e4605977f20e82454949f13d03` while preserving the four previously accepted correction classes:

- Subtitle language extraction now requires one unambiguous candidate video and derives metadata only from the suffix after that video’s normalized stem. Short title words such as `Up` and `The` cannot become languages. When more than one video matches, or a single-video fallback has no stem relationship, language remains unresolved.
- Forced and SDH/HI labels remain independent flags and are excluded from language selection. SRT/ASS/SSA/VTT and IDX/SUB handling remains visible; complete IDX/SUB pairs retain exact pairing confidence and their shared pair ID.
- The focused synthetic regressions cover `Up.forced.en.srt` → `en`, `The.Movie.sdh.pt.srt` → `pt`, ambiguous and unmatched relationships, and a paired `The.Movie.sdh.pt.idx`/`.sub` set.

No registration, import, filesystem mutation, upstream write, live service,
credential, private coordinate or real media inventory was used. All discovery
behavior remains read-only and is exercised through synthetic fixtures.

## Verification

| Command or scenario | Result / exit status | Evidence |
| --- | --- | --- |
| `gofmt -w internal/discovery/discovery.go internal/discovery/discovery_test.go` and `git diff --check` | passed, exit 0 | Scoped discovery files |
| `GOWORK=off go test -count=1 ./internal/discovery` | passed, exit 0 | Focused discovery suite |
| `GOWORK=off go test -race -count=1 ./internal/discovery` | passed, exit 0 | Focused race suite |
| `GOWORK=off go vet ./internal/discovery` | passed, exit 0 | Focused vet |
| `GOWORK=off go mod verify` | passed; all modules verified | Root dependency verification |
| `GOWORK=off go test -mod=readonly ./... -count=1` | passed, exit 0 | Full root package matrix |
| `GOWORK=off go vet -mod=readonly ./...` | passed, exit 0 | Full root vet |
| `GOWORK=off go test -race -count=1 ./internal/... ./tests/compatibility/writes` | passed, exit 0; storage completed in 203.891s | Full root internal race matrix |
| `./scripts/generate.sh --check` | passed, exit 0 | Reproducible generated output |
| `./scripts/check-api.sh` | passed, exit 0; Vacuum quality 100/100 with zero warnings/errors | Bundled OpenAPI |
| `./scripts/check-lint.sh` | passed, exit 0; zero lint issues and architecture boundaries passed | Root/UI/tools/client checks |
| `python3 scripts/check_planning.py --self-test && python3 scripts/check_planning.py` | passed, exit 0; 44 tasks, 60 acceptance cases | Planning ledger |
| `./scripts/check-guardrails.sh --ci` | passed, exit 0 | CI-equivalent generation/API/Vacuum/lint/architecture/tests/module checks |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=readonly -count=1 -run '^$' -exec=true ./internal/...` | passed, exit 0 | Linux amd64 compile matrix |
| `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go test -mod=readonly -count=1 -run '^$' -exec=true ./internal/...` | passed, exit 0 | Linux arm64 compile matrix |
| Product pre-commit hook | passed, exit 0 | Product commit `dcd3b8c9d93d3b0ea3d9a0592e857b36aa2c8294` |
| `TestSubtitleLanguageComesOnlyFromMatchedVideoSuffix` | passed | Short title words, suffix languages, forced/SDH labels, ambiguous and unmatched cases, IDX/SUB pairing |
| Prior F-02 discovery tests | passed | Pagination bounds, client evidence/readiness, connection scope, grouping and deep-clone regressions |

All checks used synthetic data. The working tree was clean at the product
checkpoint and no unrelated staged or untracked path was included.

## Review and integration

- Product commit: `dcd3b8c9d93d3b0ea3d9a0592e857b36aa2c8294` (`fix(discovery): parse subtitle language suffix`).
- Review source and finding: product `949f4af065aeb8e2dcf13d3ddd7a21ccb593f2ec`; receipt `ffe5b8a96634f0e4605977f20e82454949f13d03` records the P1/A-11 short-title-word language issue.
- Fix product and regression evidence: product commit above adds suffix-only parsing and deterministic short-title/label/pairing tests.
- Handoff commit: pending; this file is intentionally committed separately from the product.
- Final reviewer decision: pending independent review of the exact product SHA.
- Coordinator integration and execution-state update: pending; this lane did not edit or stage `docs/execution/state.json`.

## Resume checkpoint

- Product is complete at `dcd3b8c9d93d3b0ea3d9a0592e857b36aa2c8294`; subtitle language is resolved only from an exact, unambiguous video-stem suffix.
- Next safe action: independently review the exact product SHA, rerun the A-11 suffix and pairing regressions, and record product/handoff SHAs in the coordinator ledger.
- Remaining downstream work: API and SQLite projections must carry the explicit subtitle associations; those paths remain outside this lane.
- No conflicting files were removed, reset, rebased or force-pushed. Coordinator state and unrelated work remain outside this handoff’s staging scope.
