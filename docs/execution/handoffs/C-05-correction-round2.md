# C-05 correction handoff, round two

## Assignment

- Task ID and title: C-05, nested generation and CI guardrail corrections.
- Owner and independent reviewer: `/root/c01_implementer`; `/root/f01_reviewer`.
- Dispatch checkpoint: `17c239abaa5b6167326719ebd41d1f0c3428cfbe`.
- Review receipt addressed: `5063525b6bbe8bdbd83560cd4e4532cc08ce8dbb`
  (original reviewer receipt `cdfcac8c1e7113d2dbeec060ee3923e13c3e5372`).
- Product parent reviewed: C-05 product series ending at
  `38a27ccf87ce04bc83f7180c99eb384df4bfcef9`.
- Branch/worktree: shared checkout on `main`; coordinator owns integration and
  `docs/execution/state.json`.
- Owned product paths: `scripts/generate.sh`,
  `scripts/check-guardrails.sh`, `scripts/check-lint.sh`,
  `.github/workflows/checks.yml`.
- Owned evidence path: `docs/execution/handoffs/C-05-correction-round2.md`.
- Acceptance contributions: A-43 and A-45.

## Product result

Product commit: `428199ae4e1f03936d58bb48dbee962186c7266f`
(`ci: make nested generation checks side-effect-free`).

The root generation script now uses distinct expected and candidate paths for
bundled OpenAPI, every oapi-codegen output, NZBGet output, and envdoc output.
In `--check` mode each generated candidate is compared with the committed
expected file and a missing expected file fails before any checkout repair can
occur. `--write` keeps the existing in-place generation behavior.

SQLC check mode now copies the config, migrations, and query input into a
fresh temporary tree, generates into a fresh output directory, and recursively
compares that output with `internal/storage/sqlc`. It therefore detects
missing, extra, and stale generated files without running SQLC against the
repository output or changing a stale worktree/staged result. Write mode still
uses the repository config and output path.

The standalone-client architecture phase now lexes Go comments and literals
before reading imports. It recognizes named, blank, and dot aliases, raw and
interpreted import strings, and single-line or grouped declarations. Forbidden
Mastarr root/internal and cross-client imports are rejected independently of
compilation; same-client imports remain allowed. The existing repository
architecture checker and the prior five-module lint/guardrail/CI matrix remain
enabled, including `tools/internal/nzbgetgen`.

No generated artifact, client source, shared contract, state ledger, task
definition, credential, private coordinate, live service, release, or
deployment was changed.

## Verification

Commands below returned the stated exit codes from the repository root unless
a module directory is shown.

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `./scripts/generate.sh --check` | exit 0 | generated artifact reproduction |
| `./scripts/check-lint.sh --architecture-only` | exit 0 | existing and client architecture checks |
| `./scripts/check-lint.sh` | exit 0 | pinned lint across root, UI, tools, qBittorrent, NZBGet |
| `./scripts/check-guardrails.sh --fast` | exit 0 | generation, API/Vacuum, architecture, format and focused tests |
| `./scripts/check-guardrails.sh --ci` | exit 0 | all five module tests, vet, module verification, lint and architecture |
| `sh -n scripts/generate.sh scripts/check-guardrails.sh scripts/check-lint.sh` | exit 0 | shell syntax |
| `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/checks.yml")'` | exit 0 | workflow YAML parsed |
| `python3 scripts/check_planning.py` | exit 0 | 43 tasks, 60 acceptance cases; links resolve |
| `git diff --check` | exit 0 | product diff |
| `GOWORK=off go test -mod=readonly -race ./...` in each module | exit 0 for root, UI, tools, qBittorrent, and NZBGet | race matrix |
| Linux amd64 and arm64 cross-build test command in each module | exit 0 for all ten module/architecture combinations | `GOOS=linux GOARCH=... CGO_ENABLED=0 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` |
| No `go.work` and no local module replacements | exit 0 | module boundary check |
| Missing root, UI, and qBittorrent generated-file probes | expected exit 1; files stayed missing and checkout was not recreated | disposable archive probe |
| Stale UI, qBittorrent, and SQLC output probes | expected exit 1; stale bytes stayed unchanged | disposable archive probe |
| Staged stale SQLC probe | expected exit 1; staged stale diff and worktree bytes remained unchanged | disposable initialized repository probe |
| Named root/internal, blank cross-client, dot root/internal, raw-string root/internal, and grouped alias import probes | expected exit 1 for every forbidden import | disposable architecture probe |
| Product commit hook | exit 0 | fast guardrails ran during commit |

The mutation probes used temporary archived checkouts and did not modify this
shared worktree.

## Review and integration

- Product is ready at `428199ae4e1f03936d58bb48dbee962186c7266f`.
- This handoff is a separate docs-only commit following the product commit.
- Coordinator must record product and handoff SHAs in
  `docs/execution/state.json`; this worker did not edit that file.
- The prior C-05 handoff and review receipts remain unchanged.

## Remaining review risks

- SQLC check input staging currently mirrors the repository's declared
  `migrations/` schema tree and `internal/storage/query.sql`; a future
  SQLC config adding inputs outside those paths needs an explicit generator
  update.
- The import lexer is deliberately limited to dependency extraction for
  `clients/`; full Go syntax remains the compiler/linter's responsibility.
  A future standalone module outside the five declared paths needs a matrix and
  architecture-scope update.
- CI availability, generator tool cache behavior, and live upstream
  compatibility remain separate gates from these local checks.

## Resume checkpoint

- Product ready: `428199ae4e1f03936d58bb48dbee962186c7266f`.
- Handoff path: `docs/execution/handoffs/C-05-correction-round2.md`.
- Next safe action: independent review against the exact product SHA, then
  coordinator state integration.
