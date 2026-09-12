# C-05 correction handoff, round five

## Assignment

- Task ID and title: C-05, nested generation and CI guardrail corrections.
- Owner and independent reviewer: `/root/c01_implementer`; `/root/f01_reviewer`.
- Dispatch checkpoint: `ddc603d00b309e88402b50eb8f00791659c74ec0`.
- Review receipt addressed: `250800a61669dc6ac8c5e7d9d7db5c574ec982d8`
  (original reviewer receipt `0282fdfd623b7e18ad648bf9483207e21d5e9ceb`).
- Reviewed product parent: `b9bf954933a6922bbebb69432d501921eec6b623`;
  prior correction handoff `c28435a2a5044ddcde1035336f58714fad2349f9`.
- Branch/worktree: shared checkout on `main`; coordinator owns integration and
  `docs/execution/state.json`.
- Owned product paths: `scripts/generate.sh`,
  `scripts/check-guardrails.sh`, `scripts/check-lint.sh`,
  `.github/workflows/checks.yml`.
- Owned evidence path: `docs/execution/handoffs/C-05-correction-round5.md`.
- Acceptance contributions: A-43 and A-45.

## Product result

Product commit: `2433b5c0b5114d61dee59657b2f8a6991d2e3755`
(`ci: bind checker identity to staged generator`).

The staged-tree generation check now validates the generator itself before
running it. It resolves the staged generator blob from `git write-tree`,
hashes the working generator with Git path filtering, and rejects any staged
versus working content mismatch. It also requires the staged generator mode
to be executable and checks the current worktree mode. The check then runs the
generator script extracted from the exact staged archive; it no longer copies
the worktree script over that archive.

This closes the commit-candidate bypass in which an older vulnerable generator
could remain staged while a corrected worktree copy reported success. Staged
SQLC configuration membership, staged generated output bytes, isolated SQLC
generation, OpenAPI/client/NZBGet/envdoc comparisons, and import-path
unquoting remain enforced. API checks, pre-commit, and fast/CI aggregate
guardrails continue to reach this gate through `check-api.sh`.

No generated artifact, client source, shared contract, state ledger, task
definition, credential, private coordinate, live service, release, or
deployment was changed.

## Verification

Commands below returned the stated exit codes from the repository root unless
a module directory is shown.

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `./scripts/generate.sh --check` | exit 0 | exact staged-tree generation check |
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
| Staged older generator with corrected worktree generator | expected exit 1 for direct generation, pre-commit, `--fast`, and `--ci`; tree, staged blob, worktree blob, and `MM` status unchanged | disposable initialized repository |
| Matching staged/worktree generator | exit 0 | staged-tree generation check |
| Product commit hook | exit 0 | fast aggregate ran during commit |

The mismatch probe used a temporary initialized repository and invoked the
pre-commit hook from that repository so its Git root was unambiguous. No probe
modified this shared worktree.

## Review and integration

- Product is ready at `2433b5c0b5114d61dee59657b2f8a6991d2e3755`.
- This handoff is a separate docs-only commit following the product commit.
- Coordinator must record product and handoff SHAs in
  `docs/execution/state.json`; this worker did not edit that file.
- Prior C-05 handoffs and review receipts remain unchanged.

## Remaining review risks

- Staged-tree checking requires the normal Git and tar tooling available in
  development and CI. Outside a Git worktree, the working-tree generation
  path remains available and SQLC presence is still required.
- The staged snapshot intentionally evaluates staged inputs and outputs. An
  unstaged source change is outside the staged result until it is added; the
  checkout remains untouched by `--check`.
- SQLC check input staging mirrors the declared `migrations/` schema tree and
  `internal/storage/query.sql`. A future SQLC config adding inputs outside
  those paths needs an explicit generator update.

## Resume checkpoint

- Product ready: `2433b5c0b5114d61dee59657b2f8a6991d2e3755`.
- Handoff path: `docs/execution/handoffs/C-05-correction-round5.md`.
- Next safe action: independent review against the exact product SHA, then
  coordinator state integration.
