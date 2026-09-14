# F-02 discovery correction handoff, round three

## Assignment

- Task ID and title: F-02, discovery groups and media readiness; correction round three.
- Owner/agent and independent reviewer: `/root/d02_implementer`; `/root/x05_reviewer`.
- Exact dispatch checkpoint: `9ccf0648a489ca5b9e9248090ee3416acbc97386`.
- Reviewed product: `dcd3b8c9d93d3b0ea3d9a0592e857b36aa2c8294`.
- Review receipt: `e451a682bc1c7457a489d8328b3b76d0755ae993`, at `docs/execution/handoffs/F-02-review-round3.md`.
- Branch/worktree or shared-checkout ownership: shared `main` checkout; coordinator owns integration and `docs/execution/state.json`.
- Owned paths: `internal/discovery/` and this handoff only. F-04 placement, state, task, module, script, adapter and UI paths were preserved.
- Required acceptance contributions: A-01, A-02, A-10 and A-11.

## Contract and work

This correction closes both P1/A-11 findings recorded in review receipt
`e451a682bc1c7457a489d8328b3b76d0755ae993`:

- A subtitle with no stem relationship to the available video is no longer
  silently paired through the sole-video fallback. It remains unresolved with
  no selected video path and is emitted as explicit unmatched companion
  evidence by `classifyFiles`.
- Token detection uses the same punctuation-aware tokenization as subtitle
  matching and language suffix parsing. Bracketed and parenthesized labels are
  therefore retained: `Up.[forced].en.srt` resolves `en` and `Forced`, while
  `Up.(sdh).pt.srt` resolves `pt` and `HearingImpaired`.
- Root grouping still strips subtitle language and label suffixes, so the
  bracketed subtitle remains grouped with `Up.mkv`. Existing conservative
  ambiguity rules, suffix-only language selection, IDX/SUB pairing, coverage,
  pagination, and deep-clone behavior remain intact.

No registration, import, filesystem mutation, upstream write, live service,
credential, private coordinate or real media inventory was used. All discovery
behavior remains read-only and is exercised through synthetic fixtures.

## Verification

| Command or scenario | Result / exit status | Evidence |
| --- | --- | --- |
| `gofmt -d internal/discovery/discovery.go internal/discovery/discovery_test.go` and `git diff --check` | passed, exit 0 | Scoped discovery files |
| `GOWORK=off go test ./internal/discovery -count=1` | passed, exit 0 | Focused discovery suite |
| `GOWORK=off go test -race ./internal/discovery -count=1` | passed, exit 0 | Focused race suite |
| `GOWORK=off go vet -mod=readonly ./internal/discovery` | passed, exit 0 | Focused vet |
| `GOWORK=off go mod verify` | passed; all modules verified | Root dependency verification |
| `GOWORK=off go test -mod=readonly ./... -count=1` | passed, exit 0 | Full root package matrix |
| `GOWORK=off go vet -mod=readonly ./...` | passed, exit 0 | Full root vet |
| `GOWORK=off go test -race -count=1 ./internal/... ./tests/compatibility/writes` | passed, exit 0; storage completed in 205.309s | Full root internal race matrix |
| `./scripts/check-guardrails.sh --ci` | passed, exit 0 | Reproducible generation/API/Vacuum, lint, architecture, tests, vet and module checks |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=readonly -count=1 -run '^$' -exec=true ./internal/...` | passed, exit 0 | Linux amd64 compile matrix |
| `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go test -mod=readonly -count=1 -run '^$' -exec=true ./internal/...` | passed, exit 0 | Linux arm64 compile matrix |
| Product pre-commit hook | passed, exit 0 | Product commit below |
| `TestSubtitleLanguageComesOnlyFromMatchedVideoSuffix` | passed | Unmatched companion, bracketed/parenthesized labels, suffix language and IDX/SUB regressions |
| Prior F-02 discovery tests | passed | Pagination bounds, client evidence/readiness, connection scope, grouping and deep-clone regressions |

All checks used synthetic data. F-04 placement edits remained outside the
staging and commit scope.

## Review and integration

- Product commit: `05dcd107d83b77632a0212d5faec31c65e3d4e13` (`fix(discovery): close subtitle pairing gaps`).
- Review source and findings: product `dcd3b8c9d93d3b0ea3d9a0592e857b36aa2c8294`; receipt `e451a682bc1c7457a489d8328b3b76d0755ae993` records the unrelated sole-video fallback and bracketed-label parsing findings.
- Fix product and regression evidence: the product commit removes the arbitrary fallback, emits unresolved companion evidence, and adds deterministic bracketed/parenthesized label and grouping tests.
- Handoff commit: pending; this file is intentionally committed separately from the product.
- Final reviewer decision: pending independent review of the exact product SHA.
- Coordinator integration and execution-state update: pending; this lane did not edit or stage `docs/execution/state.json`.

## Resume checkpoint

- Product is complete at `05dcd107d83b77632a0212d5faec31c65e3d4e13`; subtitle relationships require an actual
  stem match, and forced/SDH labels survive bracket and parenthesis syntax.
- Next safe action: independently review the exact product SHA, rerun the A-11
  pairing regressions, and record product/handoff SHAs in the coordinator ledger.
- Remaining downstream work: API and SQLite projections must carry the explicit
  subtitle associations; those paths remain outside this lane.
- No conflicting files were removed, reset, rebased or force-pushed. Coordinator
  state and unrelated work remain outside this handoff’s staging scope.
