# C-05 independent review, round five

## Decision

`changes_requested`.

The exact round-four reproducer is closed: an older staged generator with the
corrected working generator now fails direct generation, pre-commit, fast, and
CI aggregate checks without changing the index or working tree. One P1 remains
in the inverse partial-staging direction. When the corrected generator is
staged but the working copy is the round-four generator, every local entry
point succeeds because the working generator still replaces the archived
staged generator. Validation therefore does not unconditionally execute the
exact `git write-tree` script described by the correction contract.

## Review identity and boundary

- Reviewer: `/root/f01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Correction dispatch checkpoint:
  `ddc603d00b309e88402b50eb8f00791659c74ec0`.
- Reviewed product:
  `2433b5c0b5114d61dee59657b2f8a6991d2e3755`; its direct parent is the
  dispatch checkpoint and its tree is
  `8a773bb996adaeb5223fc8d630f2d098b2cfc15e`.
- Reviewed handoff:
  `ff7a0a40967012de6380c29a090a848d498a7e38`; it is a direct child of the
  product and was treated as a claim inventory.
- Coordinator state checkpoint:
  `eb45024fec1397457cdb0e5b66948b29d04ef568`; it is a direct child of the
  handoff and records round five as pending this reviewer.
- Product scope is exactly `scripts/generate.sh`: 14 insertions and 4
  deletions. The scoped binary diff SHA-256 is
  `8181ca68f697e04e64a2e0c355a8b6c9f352f080d0e9836ae98fe4beaf0dc891`.
- Review scope is C-05's four owned product paths, A-43, A-45, the round-four
  finding, and all earlier staged-generation, architecture, workflow, module,
  and reproducibility regressions.
- Checks ran in a clean detached reviewer worktree at the exact product.
  Mutation checks used disposable synthetic repositories. No product,
  execution-state, task, script, shared file, live service, media, credential,
  release, or deployment was changed.

## Round-four closure

### Older staged generator with the corrected working generator now fails closed

A disposable repository staged
`a3b37d9252e8a9a97bf6ed80b30fb31eb62111e8:scripts/generate.sh` and restored
the working file to the exact round-five product without changing the index.
Git reported `MM scripts/generate.sh`.

- Direct `generate.sh --check`, pre-commit, `check-guardrails.sh --fast`,
  and `check-guardrails.sh --ci` each returned one.
- Every entry point reported that the staged generator differed from the
  working generator.
- The staged tree
  `318bf746e129f5666ba024a837a9422fbef79686`, staged blob
  `cc37686f3ed0fcd90c19d8d047c3751801cd2afa`, working blob
  `f35e6d8ab9686f7751c449c2bce9b2d6ce7e8abc`, and `MM` status were
  identical before and after every check.

The product also rejects a non-executable staged generator and a
non-executable working generator without mutation. With matching content and
mode, line 80 executes the script extracted from the archived staged tree;
line 69's working-copy overwrite is removed.

This closes the exact reproducer and required no-mutation behavior from round
four.

## Finding

### P1: inverse partial staging still executes the working generator instead of the staged generator

The blob and mode checks at `scripts/generate.sh:59-68` only run after the
working copy of `scripts/generate.sh` has already been selected as the
entry point. The pre-commit hook calls the working
`scripts/check-guardrails.sh`, which calls the working
`scripts/generate.sh`. The same applies to direct generation and the local
CI aggregate.

A disposable repository started from the exact product, kept the corrected
round-five generator in the index, and restored only the working generator to
round four
(`b9bf954933a6922bbebb69432d501921eec6b623:scripts/generate.sh`). Git
reported ` M scripts/generate.sh`.

Observed results:

- Direct `generate.sh --check` returned zero with
  `generation checks passed`.
- Pre-commit returned zero with `guardrail checks passed (fast)`.
- `check-guardrails.sh --ci` returned zero with
  `guardrail checks passed (ci)`.
- The staged tree, staged corrected-generator blob, working round-four blob,
  and ` M` status remained unchanged.

If the staged round-five script had executed, its blob comparison would have
rejected that mismatch. Instead, the working round-four script archived the
staged tree and copied itself over the staged corrected script before running
the snapshot. This directly disproves the handoff's claim that any staged
versus working generator mismatch fails and leaves the exact staged-script
property dependent on which working version launches the check.

The remote GitHub Actions checkout is internally consistent and the clean
product passes there. The defect affects local commit-candidate validation:
pre-commit and a local `--ci` result can approve a staged generator that they
did not execute. It also leaves future generator-contract changes vulnerable
when a newer generator is staged while an older working copy is present.

Contract: the round-five dispatch requires validation to execute and validate
the exact staged `scripts/generate.sh` content without overwriting it from the
working tree. C-05 and A-45 require a successful generation result to describe
the committed generator and generated outputs.

Required correction: put the staged-versus-working generator identity gate in
a stable caller reached before the working generator is invoked, and make
pre-commit and aggregate checks fail on mismatch regardless of which generator
version is in the working tree. If direct generation is intended to guarantee
the same property, invoke the staged generator through that stable entry point
rather than selecting the working script first. Add both mismatch directions
to the regression: older staged/current working and current staged/older
working. Require nonzero results and unchanged tree, index, work bytes, and
status.

## Preserved staged-tree and generation behavior

- Normal and intent-to-add deletion of `sqlc.yaml` returned one. Direct,
  pre-commit, and CI aggregate paths preserved the staged tree and working
  configuration.
- Stale staged SQLC and qBittorrent output with restored working bytes returned
  one from direct generation and pre-commit; tree, index, working bytes, and
  `MM` status remained unchanged.
- Staged missing qBittorrent output and staged extra SQLC output returned one
  without tree or status changes.
- Outside Git, missing and stale bundled OpenAPI, root server, UI client,
  qBittorrent client, NZBGet output, and environment documentation each
  returned one and remained missing or byte-identical.
- Outside Git, SQLC missing, stale, and extra outputs each returned one and
  remained unchanged.
- Two consecutive offline `generate.sh --write` runs in a disposable clean
  repository produced no tracked or working-tree difference.
- Clean offline `generate.sh --check` passed with `GOWORK=off`,
  `GOPROXY=off`, and `GOSUMDB=off`.

## Architecture and module behavior

- Forbidden root, internal, and cross-client imports returned one in direct,
  named, blank, dot, raw, grouped, single-line, octal, hexadecimal,
  short-Unicode, and long-Unicode forms.
- Same-client imports and import-like comments remained accepted.
- Root, UI, tools, qBittorrent, and NZBGet modules contain no
  `go.work`/local replacement coupling. Clean architecture and standalone
  client boundary checks pass.
- The workflow still enumerates all five modules and has generation, API,
  lint/architecture, and aggregate guardrail jobs.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product, handoff, state, ancestry, one-file scope, tree, and scoped diff SHA-256 | Passed; recorded above. |
| Older staged generator with current corrected working generator | Passed as a negative test; direct, hook, fast, and CI aggregate returned one with tree/index/worktree unchanged. |
| Current corrected staged generator with older working generator | Failed contract; direct, hook, and CI aggregate returned zero without executing the staged generator. |
| Staged and working generator mode checks | Passed as negative tests without mutation. |
| Intent-to-add and ordinary staged deletion of `sqlc.yaml` | Passed as negative tests without mutation. |
| Staged stale SQLC/qBittorrent, staged missing qBittorrent, and staged extra SQLC output | Passed as negative tests without mutation. |
| Missing/stale bundle, root/UI/qBittorrent/NZBGet/envdoc output matrix | Passed as negative tests; every check returned one without mutation. |
| SQLC stale/missing/extra output matrix | Passed as negative tests without mutation. |
| Clean generation and two consecutive offline write runs | Passed with no tracked or working-tree drift. |
| Alias, raw/interpreted/grouped, escaped forbidden-import matrix and allowed controls | Passed. |
| Root, UI, tools, qBittorrent, and NZBGet tests, race tests, vet, and module verification with `GOWORK=off` and `-mod=readonly` | Passed all five modules; root storage race completed in 217.685 seconds. |
| CGO-free Linux amd64/arm64 test compilation for all five modules | Passed all ten module/architecture combinations. |
| `check-api.sh`, Vacuum, `check-lint.sh`, and clean-tree `check-guardrails.sh --ci` | Passed; five lint runs had zero issues and Vacuum quality was 100/100. |
| `sh -n`, `actionlint`, and Ruby workflow YAML parsing | Passed. |
| Planning self-test and normal validation | Passed: 43 tasks, 60 acceptance cases, and local links are valid. |
| No workspace file/local replace and public dependency/secret/path scan | Passed. |
| Product and receipt diff checks and detached checkout cleanliness before receipt | Passed. |

## Acceptance contribution

- A-43: accepted for C-05. Staged and filesystem SQLC configuration/output
  cases are correctly isolated, deterministic, and side-effect free; database
  runtime and migrations are unchanged.
- A-45: not accepted for C-05. Clean generation, all five module gates,
  architecture, workflow, and both missing/stale output matrices pass, but a
  local success can still describe a generator other than the staged commit
  candidate.

## Reviewer decision

`changes_requested`. Correct the P1 inverse partial-staging gap and return
exact product and handoff commits for another independent review. No product,
execution-state, release, deployment, or live-stack action is authorized by
this receipt.
