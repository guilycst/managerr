# F-02 discovery correction handoff, round four

## Assignment

- Task ID and title: F-02, discovery groups and media readiness; correction round four.
- Owner/agent and independent reviewer: `/root/d02_implementer`; `/root/x05_reviewer`.
- Exact dispatch checkpoint: `4899e645fd456676d9bf6cb0a4a29ef6e2e75c59`.
- Reviewed product: `05dcd107d83b77632a0212d5faec31c65e3d4e13`.
- Review receipt: `49a2e72430acba544d77ecb34121624cbb73bb62`, integrated by `2963240b6a833e60c8a42c7a8025d60492b02fd9`, at `docs/execution/handoffs/F-02-review-round4.md`.
- Branch/worktree or shared-checkout ownership: shared `main` checkout; coordinator owns integration and `docs/execution/state.json`.
- Owned paths: `internal/discovery/` and this handoff only. F-05, state, task, module, script, adapter and UI paths were preserved.
- Required acceptance contributions: A-01, A-02, A-10 and A-11.

## Contract and work

This correction closes the remaining P1/A-11 finding recorded in review receipt
`49a2e72430acba544d77ecb34121624cbb73bb62`:

- Forced and hearing-impaired labels are now derived from the suffix remaining
  after one exact matched video stem. A title named `Hi` no longer makes
  `Hi.en.srt` hearing-impaired, and a title named `Forced` no longer makes
  `Forced.en.srt` forced.
- Existing suffix metadata remains intact: `Up.forced.en.srt` resolves `en`
  with `Forced`, `The.Movie.sdh.pt.srt` resolves `pt` with
  `HearingImpaired`, and bracketed/parenthesized forms preserve those flags.
  Ambiguous and unmatched relationships do not acquire title-derived flags,
  and complete IDX/SUB pairs retain exact association and pair identity.
- The focused regression table covers both label-shaped title collisions and
  the previous short-title, bracketed, parenthesized, unmatched, and IDX/SUB
  cases. Root grouping remains conservative and unchanged.

No registration, import, filesystem mutation, upstream write, live service,
credential, private coordinate or real media inventory was used. All discovery
behavior remains read-only and is exercised through synthetic fixtures.

## Verification

| Command or scenario | Result / exit status | Evidence |
| --- | --- | --- |
| `gofmt -w internal/discovery/discovery.go internal/discovery/discovery_test.go` and `git diff --check` | passed, exit 0 | Scoped discovery files |
| `GOWORK=off go test ./internal/discovery -count=1` | passed, exit 0 | Focused discovery suite |
| `GOWORK=off go test -race -mod=readonly ./internal/discovery -count=1` | passed, exit 0 | Focused race suite |
| `GOWORK=off go vet -mod=readonly ./internal/discovery` | passed, exit 0 | Focused vet |
| `GOWORK=off go mod verify` | passed; all modules verified | Root dependency verification |
| `GOWORK=off go test -mod=readonly ./... -count=1` | passed, exit 0 | Full root package matrix |
| `GOWORK=off go vet -mod=readonly ./...` | passed, exit 0 | Full root vet |
| `GOWORK=off go test -race -count=1 ./internal/... ./tests/compatibility/writes` | passed, exit 0; storage completed in 194.451s | Full root internal race matrix |
| `python3 scripts/check_planning.py --self-test && python3 scripts/check_planning.py` | passed, exit 0; 44 tasks and 60 acceptance cases | Planning ledger |
| `./scripts/check-guardrails.sh --ci` | passed, exit 0 | Reproducible generation/API/Vacuum, lint, architecture, tests and module checks |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=readonly -count=1 -run '^$' -exec=true ./internal/...` | passed, exit 0 | Linux amd64 compile matrix |
| `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go test -mod=readonly -count=1 -run '^$' -exec=true ./internal/...` | passed, exit 0 | Linux arm64 compile matrix |
| Product pre-commit hook | passed, exit 0 | Product commit below |
| `TestSubtitleLanguageComesOnlyFromMatchedVideoSuffix` | passed | Short-title, bracketed/parenthesized labels, unmatched and IDX/SUB regressions, including `Hi` and `Forced` title collisions |
| Prior F-02 discovery tests | passed | Pagination bounds, client evidence/readiness, connection scope, grouping and deep-clone regressions |

All checks used synthetic data. F-05 files were outside the staging and commit
scope.

## Review and integration

- Product commit: `1a44ea97962f40820b042320970dc2f5d1571206` (`fix(discovery): scope subtitle flags to matched stems`).
- Review source and finding: product `05dcd107d83b77632a0212d5faec31c65e3d4e13`; receipt `49a2e72430acba544d77ecb34121624cbb73bb62` records the full-basename flag parsing blocker.
- Fix product and regression evidence: the product commit scopes flag extraction to the exact matched suffix and adds deterministic `Hi`/`Forced` title-collision cases.
- Handoff commit: pending; this file is intentionally committed separately from the product.
- Final reviewer decision: pending independent review of the exact product SHA.
- Coordinator integration and execution-state update: pending; this lane did not edit or stage `docs/execution/state.json`.

## Resume checkpoint

- Product is complete at `1a44ea97962f40820b042320970dc2f5d1571206`; title tokens are not treated as subtitle labels unless they occur after the matched video stem.
- Next safe action: independently review the exact product SHA, rerun the A-11 collision and pairing regressions, and record product/handoff SHAs in the coordinator ledger.
- Remaining downstream work: API and SQLite projections must carry the explicit subtitle associations; those paths remain outside this lane.
- No conflicting files were removed, reset, rebased or force-pushed. Coordinator state and unrelated F-05 work remain outside this handoff’s staging scope.
