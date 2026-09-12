# C-05 independent review, round seven

## Decision

`approved`.

Round seven provides one authoritative staged-generation entry point and routes
every supported current caller through it. The stable
`scripts/check-guardrails.sh --generation` command, current
`scripts/generate.sh --check`, pre-commit, fast/CI aggregates, and the CI
generation job now validate the exact staged tree. Both partial-staging
directions fail without changing the tree, index, or working files.

Manually replacing `scripts/generate.sh` with a historical implementation and
then invoking those historical bytes remains outside the current product's
control. That disclosed boundary is acceptable: the authoritative direct
command and all repository-controlled callers fail closed, while no current
code can execute before a user explicitly starts a different old file.

## Review identity and boundary

- Reviewer: `/root/f01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Correction dispatch checkpoint:
  `d97cbc1bc8e2f5c5ec3b11856889de9ac21ba84c`.
- Reviewed product:
  `0b219e9d86776382532eced5251d363322d6dc37`; its direct parent is the
  dispatch checkpoint and its tree is
  `a019126d0816ea2c3dbd51e22344506c46ab5a62`.
- Reviewed handoff:
  `8e928d909a7778d1f104f5ef72652bc410056eac`; it is a direct child of the
  product and was treated as a claim inventory.
- Coordinator state checkpoint:
  `508adb098aaaa4b07b26dd94f0411c4fdcdc567b`; it is a direct child of the
  handoff and records round seven as pending this reviewer.
- Product scope is exactly `scripts/generate.sh`,
  `scripts/check-guardrails.sh`, and `.github/workflows/checks.yml`: 64
  insertions and 2 deletions. The scoped binary diff SHA-256 is
  `8988670fbcd00c6b8ceb8027426fdd293cba482ec3b31b8cc3af7306976f88d0`.
- Review scope is C-05's four owned product paths, A-43, A-45, the round-six
  direct-entrypoint finding, and all earlier staged-generation, architecture,
  workflow, module, and reproducibility regressions.
- Checks ran in a clean detached reviewer worktree at the exact product.
  Mutation checks used disposable synthetic repositories. No product,
  execution-state, task, script, shared file, live service, media, credential,
  release, or deployment was changed.

## Round-six closure

### Stable staged-generation entry point

`scripts/check-guardrails.sh:62-107` implements the generation-only entry
point. It:

- requires a Git worktree;
- captures the exact `git write-tree`;
- requires staged `sqlc.yaml` and `scripts/generate.sh`;
- requires matching staged and working generator blobs and executable modes;
- archives the staged tree;
- verifies the extracted generator's blob against the staged blob; and
- executes that archived generator directly with the snapshot marker.

The path never copies the working generator into the archive. Matching product
bytes return zero with `staged generation checks passed`, and the checkout
remains unchanged.

### Every supported current caller reaches the stable entry point

The current `scripts/generate.sh:19-26` delegates Git-backed `--check` runs
to `check-guardrails.sh --generation` before its own generation logic.
Independent clean execution ended with `staged generation checks passed`,
confirming delegation.

Pre-commit, `--fast`, and `--ci` retain their early staged identity gate.
The generation job in `.github/workflows/checks.yml:105` now invokes
`./scripts/check-guardrails.sh --generation` directly.

### Both partial-staging directions fail closed

Two disposable repositories preserved the staged tree, staged generator blob,
working generator blob, and status around every entry point.

With round seven staged and the round-four generator only in the working tree:

```text
stable=1 historical-generate=0 hook=1 fast=1 ci=1
status= M scripts/generate.sh
state unchanged=yes
```

The supported stable direct command and all repository callers rejected the
mismatch. The zero result came only from deliberately invoking the restored
round-four file itself.

With the round-four generator staged and the current round-seven generator in
the working tree:

```text
stable=1 current-generate=1 hook=1 fast=1 ci=1
status=MM scripts/generate.sh
state unchanged=yes
```

The current direct generator delegated to the stable caller and rejected the
mismatch. This closes the round-six requirement for current product code and
repository-controlled entry points.

## Historical-file boundary assessment

A shell cannot apply code from round seven before the operating system starts a
different historical file that the user explicitly selected from the working
tree. Treating that manual invocation as a failure of round-seven product code
would require an external trusted launcher or checkout policy outside this
repository slice.

The boundary is contract-compatible for C-05 and A-45 because:

- the documented authoritative staged command is
  `scripts/check-guardrails.sh --generation`;
- the current traditional `scripts/generate.sh --check` command delegates to
  it;
- pre-commit and both aggregate modes fail closed before invoking a mismatched
  working generator;
- the CI generation job invokes the stable command from a clean checkout; and
- non-Git archive checks retain the intentional filesystem generation path.

The handoff states this boundary explicitly. It does not claim that arbitrary
historical executable bytes gain later behavior.

## Preserved staged-tree and generation behavior

- Normal and intent-to-add deletion of `sqlc.yaml` returned one. Stable
  generation, current direct generation, pre-commit, and CI aggregate paths
  preserved the staged tree and working configuration.
- Stale staged SQLC and qBittorrent output with restored working bytes returned
  one from stable generation, current direct generation, and pre-commit; tree,
  index, working bytes, and `MM` status remained unchanged.
- Staged missing qBittorrent output and staged extra SQLC output returned one
  without tree or status changes.
- Staged and working generator executable-mode failures returned one without
  mutation.
- Outside Git, missing and stale bundled OpenAPI, root server, UI client,
  qBittorrent client, NZBGet output, and environment documentation each
  returned one and remained missing or byte-identical.
- Outside Git, SQLC missing, stale, and extra outputs each returned one and
  remained unchanged.
- Two consecutive offline `generate.sh --write` runs in a disposable clean
  repository produced no tracked or working-tree difference.
- Clean offline stable and delegated generation passed with `GOWORK=off`,
  `GOPROXY=off`, and `GOSUMDB=off`.

## Architecture, workflow, and module behavior

- Forbidden root, internal, and cross-client imports returned one in direct,
  named, blank, dot, raw, grouped, single-line, octal, hexadecimal,
  short-Unicode, and long-Unicode forms.
- Same-client imports and import-like comments remained accepted.
- Root, UI, tools, qBittorrent, and NZBGet modules contain no
  `go.work`/local replacement coupling. Clean architecture and standalone
  client boundary checks pass.
- The workflow enumerates all five modules and retains generation, API,
  lint/architecture, and aggregate guardrail jobs. Its generation job uses the
  stable staged entry point.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product, handoff, state, ancestry, three-file scope, tree, and scoped diff SHA-256 | Passed; recorded above. |
| Stable generation and current delegated `generate.sh --check` | Passed with the staged-check result and no checkout drift. |
| Current staged generator with round-four working generator | Stable command, hook, fast, and CI returned one without mutation; manual historical-file invocation returned zero within the documented boundary. |
| Round-four staged generator with current working generator | Stable command, current direct generator, hook, fast, and CI returned one without mutation. |
| Staged and working generator mode checks | Passed as negative tests without mutation. |
| Intent-to-add and ordinary staged deletion of `sqlc.yaml` | Passed as negative tests without mutation. |
| Staged stale SQLC/qBittorrent, staged missing qBittorrent, and staged extra SQLC output | Passed as negative tests without mutation. |
| Missing/stale bundle, root/UI/qBittorrent/NZBGet/envdoc output matrix | Passed as negative tests; every check returned one without mutation. |
| SQLC stale/missing/extra output matrix | Passed as negative tests without mutation. |
| Clean generation and two consecutive offline write runs | Passed with no tracked or working-tree drift. |
| Alias, raw/interpreted/grouped, escaped forbidden-import matrix and allowed controls | Passed. |
| Root, UI, tools, qBittorrent, and NZBGet tests, race tests, vet, and module verification with `GOWORK=off` and `-mod=readonly` | Passed all five modules; root storage race completed in 207.521 seconds. |
| CGO-free Linux amd64/arm64 test compilation for all five modules | Passed all ten module/architecture combinations. |
| `check-api.sh`, Vacuum, `check-lint.sh`, and clean-tree `check-guardrails.sh --ci` | Passed; five lint runs had zero issues and Vacuum quality was 100/100. |
| `sh -n`, `actionlint`, and Ruby workflow YAML parsing | Passed. |
| Planning self-test and normal validation | Passed: 43 tasks, 60 acceptance cases, and local links are valid. |
| No workspace file/local replace and public dependency/secret/path scan | Passed. |
| Product and receipt diff checks and detached checkout cleanliness before receipt | Passed. |

## Acceptance contribution

- A-43: accepted for C-05. Staged and filesystem SQLC configuration/output
  cases remain isolated, deterministic, and side-effect free; database runtime
  and migrations are unchanged.
- A-45: accepted for C-05. Exact staged generation, clean offline
  reproducibility, all five module gates, architecture boundaries, workflow
  wiring, and generated-output mutation matrices pass.

## Reviewer decision

`approved`. No open C-05 finding remains. Coordinator may integrate this
receipt and close C-05 subject to the separately recorded integration gate.
This approval does not authorize a release, deployment, publication, or
live-stack action.
