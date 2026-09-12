# C-05 correction handoff, round six

## Assignment

- Task ID and title: C-05, nested generation and CI guardrail corrections.
- Owner and independent reviewer: `/root/c01_implementer`; `/root/f01_reviewer`.
- Dispatch checkpoint: `bb71b875299cc598e7463154359d0a5b7f2304bf`.
- Review receipt addressed: `fe6ea8a4ac0e68c016d1b78f918c1236f09fdfb`
  (original reviewer receipt `a6e3b643df259b4bd048cfd0f8e9e7235c98db1c`).
- Reviewed product parent: `2433b5c0b5114d61dee59657b2f8a6991d2e3755`;
  prior correction handoff `ff7a0a40967012de6380c29a090a848d498a7e38`.
- Branch/worktree: shared checkout on `main`; coordinator owns integration and
  `docs/execution/state.json`.
- Owned product paths: `scripts/generate.sh`,
  `scripts/check-guardrails.sh`, `scripts/check-lint.sh`,
  `.github/workflows/checks.yml`.
- Owned evidence path: `docs/execution/handoffs/C-05-correction-round6.md`.
- Acceptance contributions: A-43 and A-45.

## Product result

Product commit: `e58428caa17c55605b77f5a4d8d388b61b6e5fef`
(`ci: fail closed on inverse staged generator`).

The staged generation path in `scripts/generate.sh` now verifies the extracted
archive's `scripts/generate.sh` bytes against the staged Git blob before
execution and invokes that executable directly with the snapshot marker. The
staged generator is never replaced by a worktree copy. Existing staged tree,
SQLC configuration, isolated SQLC output, generated artifact, and mode checks
remain in force.

`scripts/check-guardrails.sh` now performs the staged tree presence, mode, and
staged-versus-working generator identity checks before calling `check-api.sh`.
This gives pre-commit and both aggregate guardrail modes a stable caller that
rejects inverse partial staging before a worktree generator can run. The check
also fails closed when the authoritative staged `sqlc.yaml` is absent.

No generated artifact, client source, shared contract, state ledger, task
definition, credential, private coordinate, live service, release, or
deployment was changed.

## Verification

Commands below returned the stated exit codes from the repository root unless
a module directory is shown.

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `./scripts/generate.sh --check` | exit 0 | exact staged-tree generation and archive-byte check |
| `./scripts/check-lint.sh --architecture-only` | exit 0 | architecture checks |
| `./scripts/check-lint.sh` | exit 0 | pinned lint across all five modules |
| `./scripts/check-guardrails.sh --fast` | exit 0 | fast aggregate |
| `./scripts/check-guardrails.sh --ci` | exit 0 | full API, lint, architecture, tests, vet, and module verification aggregate |
| `sh -n scripts/generate.sh scripts/check-guardrails.sh scripts/check-lint.sh` | exit 0 | shell syntax |
| `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/checks.yml")'` | exit 0 | workflow YAML parse |
| `python3 scripts/check_planning.py` | exit 0 | 43 tasks, 60 acceptance cases, and local links |
| `git diff --check` and `git diff --cached --check` | exit 0 | product and staged whitespace checks |
| `GOWORK=off go test -mod=readonly -race ./...` in root, UI, tools, qBittorrent, and NZBGet | exit 0 for all five | race matrix |
| Linux amd64 and arm64 cross-build test in each module | exit 0 for all ten combinations | CGO-free `go test -run '^$' -exec=true` matrix |
| No `go.work` or local module replacement | exit 0 | module boundary check |
| Product commit hook | exit 0 | fast aggregate ran during commit |

The inverse partial-staging probe used a disposable local clone and preserved
the tree object, staged generator blob, worktree generator blob, and status
around every command. With current product bytes staged and the exact round-
four generator restored in the worktree, pre-commit, `check-guardrails.sh
--fast`, and `check-guardrails.sh --ci` each returned exit 1 and left all four
values unchanged. The matching staged/worktree product returned exit 0 for
direct generation, pre-commit, fast, and CI, also without mutation. The
opposite direction (round-four staged, current worktree) returned exit 1 from
all four current entrypoints without mutation.

## Boundary and remaining review risk

An invocation of an older `scripts/generate.sh` worktree file itself cannot be
intercepted by a later commit's shell code: the operating system selects and
starts those old bytes before the corrected file can compare the staged blob.
In the inverse probe, direct execution of that deliberately restored old file
therefore returned exit 0; the stable pre-commit and aggregate callers failed
closed as described above. Closing direct execution of an arbitrary historical
worktree copy would require changing an unowned stable caller such as
`scripts/check-api.sh` or `.githooks/pre-commit`, or installing a separately
trusted checker outside the commit candidate. This handoff records the result
so review can decide whether to expand C-05's owned paths.

## Review and integration

- Product is ready at `e58428caa17c55605b77f5a4d8d388b61b6e5fef`.
- This handoff is a separate docs-only commit following the product commit.
- Coordinator must record product and handoff SHAs in
  `docs/execution/state.json`; this worker did not edit that file.
- Prior C-05 handoffs and review receipts remain unchanged.

## Resume checkpoint

- Product ready: `e58428caa17c55605b77f5a4d8d388b61b6e5fef`.
- Handoff path: `docs/execution/handoffs/C-05-correction-round6.md`.
- Next safe action: independent review of the stable caller fix and decision on
  whether direct historical-generator invocation needs an ownership expansion.
