# C-05 correction handoff, round seven

## Assignment

- Task ID and title: C-05, nested generation and CI guardrail corrections.
- Owner and independent reviewer: `/root/c01_implementer`; `/root/f01_reviewer`.
- Dispatch checkpoint: `d97cbc1bc8e2f5c5ec3b11856889de9ac21ba84c`.
- Review receipt addressed: `ec5135079a0e1e433271f55455280dfa2bdba43e`
  (original reviewer receipt `f7a23fbd9dfc2033551c653732c18f792013a7e4`).
- Reviewed product parent: `e58428caa17c55605b77f5a4d8d388b61b6e5fef`;
  prior correction handoff `ad00af9306468611c466b31ec99840b71fd127da`.
- Branch/worktree: shared checkout on `main`; coordinator owns integration and
  `docs/execution/state.json`.
- Owned product paths: `scripts/generate.sh`,
  `scripts/check-guardrails.sh`, `scripts/check-lint.sh`,
  `.github/workflows/checks.yml`.
- Owned evidence path: `docs/execution/handoffs/C-05-correction-round7.md`.
- Acceptance contributions: A-43 and A-45.

## Product result

Product commit: `0b219e9d86776382532eced5251d363322d6dc37`
(`ci: make staged generation caller authoritative`).

`scripts/check-guardrails.sh --generation` is now the stable staged-generation
entrypoint. It resolves the exact `git write-tree`, requires staged
`sqlc.yaml` and an executable staged generator, rejects staged-versus-working
generator divergence, archives the staged tree, verifies the archived script
blob byte-for-byte, and executes that archived executable with the snapshot
marker. It never copies the worktree generator into the staged archive.

The current `scripts/generate.sh --check` delegates to this stable entrypoint
before selecting any worktree generation logic. Pre-commit and fast/CI
aggregate guardrails retain their early staged identity gate, and the CI
generation job invokes `check-guardrails.sh --generation` directly. This makes
the supported direct check and all repository callers use the same staged
checker.

No generated artifact, client source, shared contract, state ledger, task
definition, credential, private coordinate, live service, release, or
deployment was changed.

## Verification

Commands below returned the stated exit codes from the repository root unless
a module directory is shown.

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `./scripts/check-guardrails.sh --generation` | exit 0 | stable exact staged-tree generation entrypoint |
| `./scripts/generate.sh --check` | exit 0 | current direct command delegates to the stable entrypoint |
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
around every command. With the current generator staged and the exact
round-four generator restored in the worktree, the stable direct command
`./scripts/check-guardrails.sh --generation`, pre-commit, `--fast`, and `--ci`
each returned exit 1 and left all values unchanged. The matching product
bytes returned exit 0 from the stable command, current `generate.sh --check`,
pre-commit, fast, and CI. With the round-four generator staged and the current
generator in the worktree, all current entrypoints returned exit 1 without
mutation.

## Direct historical-file boundary

The probe also invokes `./scripts/generate.sh --check` after deliberately
restoring the old round-four file in the worktree while retaining the current
generator in the index. That command executes the restored historical bytes
before any code from this product can run, so it returns exit 0; the stable
entrypoint and all repository callers fail closed. No tracked shell change can
make arbitrary already-selected historical executable bytes run a later
identity check. The documented direct generation command is therefore the
stable `./scripts/check-guardrails.sh --generation`; the current
`./scripts/generate.sh --check` routes through it. Enforcing a different
historical file invocation would require an external trusted launcher or a
filesystem/checkout policy outside these owned paths.

## Review and integration

- Product is ready at `0b219e9d86776382532eced5251d363322d6dc37`.
- This handoff is a separate docs-only commit following the product commit.
- Coordinator must record product and handoff SHAs in
  `docs/execution/state.json`; this worker did not edit that file.
- Prior C-05 handoffs and review receipts remain unchanged.

## Resume checkpoint

- Product ready: `0b219e9d86776382532eced5251d363322d6dc37`.
- Handoff path: `docs/execution/handoffs/C-05-correction-round7.md`.
- Next safe action: independent review of the stable staged-generation entry
  point and the documented direct-command boundary.
