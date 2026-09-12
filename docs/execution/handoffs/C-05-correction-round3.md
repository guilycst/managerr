# C-05 correction handoff, round three

## Assignment

- Task ID and title: C-05, nested generation and CI guardrail corrections.
- Owner and independent reviewer: `/root/c01_implementer`; `/root/f01_reviewer`.
- Dispatch checkpoint: `558e9caf379e1065657e4e982135e57c57c116f3`.
- Review receipt addressed: `45daade2a8dd4ec1999ecff9ce88ff6a7f82f85c`
  (original reviewer receipt `1f611907ba4f23143b3896bc2d2707573bfa25e1`).
- Reviewed product parent: `428199ae4e1f03936d58bb48dbee962186c7266f`;
  prior correction handoff `385a783a454ff9e500b1250c7df94be0a7f47a07`.
- Branch/worktree: shared checkout on `main`; coordinator owns integration and
  `docs/execution/state.json`.
- Owned product paths: `scripts/generate.sh`,
  `scripts/check-guardrails.sh`, `scripts/check-lint.sh`,
  `.github/workflows/checks.yml`.
- Owned evidence path: `docs/execution/handoffs/C-05-correction-round3.md`.
- Acceptance contributions: A-43 and A-45.

## Product result

Product commit: `a3b37d9252e8a9a97bf6ed80b30fb31eb62111e8`
(`ci: fail closed on missing SQLC configuration`).

Generation now requires the authoritative `sqlc.yaml` in the working tree
before any generation mode runs. When the repository has a Git index, it also
requires the config to be present in the index. A working-tree file with an
index-staged deletion therefore fails closed. The check runs before generators
are invoked, so API checks, pre-commit, and both fast and CI aggregate
guardrails fail rather than silently skipping SQLC.

The client architecture lexer now decodes Go interpreted-string escapes
including octal, hex, `\xNN`, and Unicode escapes before classifying import
paths. Named, blank, dot, raw-string, and grouped imports remain covered, and
an escaped root/internal path is rejected before compilation. Raw strings stay
literal. The previous side-effect-free OpenAPI, oapi-codegen, NZBGet, envdoc,
and SQLC output comparisons remain enabled.

No generated artifact, client source, shared contract, state ledger, task
definition, credential, private coordinate, live service, release, or
deployment was changed.

## Verification

Commands below returned the stated exit codes from the repository root unless
a module directory is shown.

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `./scripts/generate.sh --check` | exit 0 | generated artifact reproduction |
| `./scripts/check-lint.sh --architecture-only` | exit 0 | architecture checks |
| `./scripts/check-lint.sh` | exit 0 | pinned lint across all five modules |
| `./scripts/check-guardrails.sh --fast` | exit 0 | fast aggregate |
| `./scripts/check-guardrails.sh --ci` | exit 0 | full aggregate: API, lint, architecture, tests, vet, module verification |
| `sh -n scripts/generate.sh scripts/check-guardrails.sh scripts/check-lint.sh` | exit 0 | shell syntax |
| `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/checks.yml")'` | exit 0 | workflow YAML parsed |
| `python3 scripts/check_planning.py` | exit 0 | 43 tasks, 60 acceptance cases; links resolve |
| `git diff --check` | exit 0 | product diff |
| `GOWORK=off go test -mod=readonly -race ./...` in root, UI, tools, qBittorrent, and NZBGet | exit 0 for all five | race matrix |
| Linux amd64 and arm64 cross-build test in each module | exit 0 for all ten combinations | `GOOS=linux GOARCH=... CGO_ENABLED=0 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` |
| No `go.work` and no local module replacements | exit 0 | module boundary check |
| Missing `sqlc.yaml` probe | expected exit 1; file stayed absent and was not recreated | disposable archived checkout |
| Index-staged `sqlc.yaml` deletion with worktree file retained | expected exit 1; worktree file stayed intact and index deletion remained | disposable initialized repository |
| Escaped interpreted import `\x69nternal/domain` probe | expected exit 1; decoded dependency was reported | disposable architecture checkout |
| Product commit hook | exit 0 | fast aggregate ran during commit |

Deletion and escaped-import probes used temporary checkouts and did not modify
the shared worktree. The existing round-two stale/missing generated-output and
staged SQLC output protections remain covered by the full generation and
guardrail checks.

## Review and integration

- Product is ready at `a3b37d9252e8a9a97bf6ed80b30fb31eb62111e8`.
- This handoff is a separate docs-only commit following the product commit.
- Coordinator must record product and handoff SHAs in
  `docs/execution/state.json`; this worker did not edit that file.
- Prior C-05 handoff and review receipts remain unchanged.

## Remaining review risks

- SQLC check input staging mirrors the declared `migrations/` schema tree and
  `internal/storage/query.sql`. A future SQLC config adding inputs outside
  those paths needs an explicit generator update.
- The import lexer is limited to dependency extraction for `clients/`;
  invalid Go syntax remains the compiler/linter's responsibility. A future
  standalone module outside the five declared paths needs a matrix and
  architecture-scope update.
- The index presence check is intentionally skipped when run outside a Git
  worktree, while the filesystem presence check always applies.

## Resume checkpoint

- Product ready: `a3b37d9252e8a9a97bf6ed80b30fb31eb62111e8`.
- Handoff path: `docs/execution/handoffs/C-05-correction-round3.md`.
- Next safe action: independent review against the exact product SHA, then
  coordinator state integration.
