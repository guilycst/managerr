# C-05 independent review, round six

## Decision

`changes_requested`.

Round six closes the inverse partial-staging gap for pre-commit and both
aggregate guardrail modes. It does not close the explicitly required direct
generation case. With the corrected generator staged and the round-four
generator in the working tree, direct `scripts/generate.sh --check` still
returns zero because the shell starts the older working bytes before any
round-six identity check can run. The handoff records this limitation, and the
independent reproducer confirms it.

## Review identity and boundary

- Reviewer: `/root/f01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Correction dispatch checkpoint:
  `bb71b875299cc598e7463154359d0a5b7f2304bf`.
- Reviewed product:
  `e58428caa17c55605b77f5a4d8d388b61b6e5fef`; its direct parent is the
  dispatch checkpoint and its tree is
  `9e8563da99335dc0cec80066f80991e8745209a5`.
- Reviewed handoff:
  `ad00af9306468611c466b31ec99840b71fd127da`; it is a direct child of the
  product and was treated as a claim inventory.
- Coordinator state checkpoint:
  `9ac02c43270cf2144a1d19d0ba79c092fcaf0db4`; it is a direct child of the
  handoff and records round six as pending this reviewer.
- Product scope is exactly `scripts/generate.sh` and
  `scripts/check-guardrails.sh`: 45 insertions and 4 deletions. The scoped
  binary diff SHA-256 is
  `2606615e59cbe865e1194f211f7aded8ef9ed142cd842d6e92f03156ef552e5d`.
- Review scope is C-05's four owned product paths, A-43, A-45, the round-five
  inverse partial-staging finding, and all earlier staged-generation,
  architecture, workflow, module, and reproducibility regressions.
- Checks ran in a clean detached reviewer worktree at the exact product.
  Mutation checks used disposable synthetic repositories. No product,
  execution-state, task, script, shared file, live service, media, credential,
  release, or deployment was changed.

## Round-five closure

### Stable aggregate caller rejects inverse partial staging

`scripts/check-guardrails.sh:61-96` now checks the staged generator's
presence, blob, and executable mode before `check-api.sh` invokes generation.
A disposable repository kept the exact round-six generator staged and restored
only the working generator to round four
(`b9bf954933a6922bbebb69432d501921eec6b623:scripts/generate.sh`).

- Pre-commit, `check-guardrails.sh --fast`, and
  `check-guardrails.sh --ci` each returned one.
- Each reported that the staged generator differed from the working generator.
- The staged tree, staged blob, working blob, and
  ` M scripts/generate.sh` status were unchanged around every command.

This closes the round-five finding for the stable pre-commit and aggregate
entry points.

### Current generation no longer overwrites its archived script

The round-six `scripts/generate.sh:74-86` path hashes the archived generator
against the staged blob and invokes the archived executable directly. It does
not copy the working generator into the snapshot. The opposite mismatch
direction, with the round-four generator staged and round six in the working
tree, returned one from direct generation, pre-commit, fast, and CI aggregate
checks without state changes.

Matching staged and working round-six bytes pass clean generation. Staged and
working executable-mode failures both return one without mutation.

## Finding

### P1: the required direct inverse mismatch still succeeds

The round-six dispatch requires a newer staged generator with an older working
generator to fail across direct generation, pre-commit, fast, and CI checks.
The product closes three of those four entry points.

Reproduction from a disposable repository at the exact product:

1. Keep
   `e58428caa17c55605b77f5a4d8d388b61b6e5fef:scripts/generate.sh` in the
   index.
2. Restore only the working file from
   `b9bf954933a6922bbebb69432d501921eec6b623:scripts/generate.sh`.
3. Record `git write-tree`, the staged generator blob, working-file blob, and
   status.
4. Run `sh scripts/generate.sh --check`, pre-commit,
   `check-guardrails.sh --fast`, and `check-guardrails.sh --ci`.
5. Re-record the same state.

Observed result:

```text
direct=0 hook=1 fast=1 ci=1
status= M scripts/generate.sh
tree/index/work/status unchanged=yes
```

Direct generation ends with `generation checks passed`. The working
round-four generator is selected by the shell, archives the staged tree, then
copies its own older bytes over the staged round-six generator. The staged
generator's identity and archived-byte checks never execute. This is the exact
inverse partial-staging condition from round five and contradicts the round-six
requirement that all four entry points reject it.

The handoff's “Boundary and remaining review risk” section correctly states
that this direct invocation remains successful. That disclosure is accurate,
but it does not satisfy the dispatched acceptance condition.

Contract: C-05 and A-45 require a successful generation result to describe the
committed generator and generated outputs. The round-six dispatch explicitly
requires direct generation, pre-commit, fast, and CI to reject the inverse
mismatch while preserving state.

Required resolution: choose one of the following and record it before another
review.

- Introduce an authoritative stable staged-check entry point that selects the
  staged generator before any working generator bytes execute, and use that
  entry point for the documented direct check as well as pre-commit and
  aggregates.
- Explicitly narrow the contract so historical working-copy invocation is
  outside the direct generation guarantee, while retaining the now-correct
  pre-commit/fast/CI behavior. Update the task, handoff expectation, and exact
  review command accordingly.

Under the current dispatch, the direct exit-zero result remains a P1 acceptance
blocker.

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

## Architecture, workflow, and module behavior

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
| Exact product, handoff, state, ancestry, two-file scope, tree, and scoped diff SHA-256 | Passed; recorded above. |
| Current staged generator with round-four working generator | Direct failed contract with exit zero; hook, fast, and CI correctly returned one. All state remained unchanged. |
| Round-four staged generator with current working generator | Passed as a negative test; direct, hook, fast, and CI returned one without mutation. |
| Staged and working generator mode checks | Passed as negative tests without mutation. |
| Intent-to-add and ordinary staged deletion of `sqlc.yaml` | Passed as negative tests without mutation. |
| Staged stale SQLC/qBittorrent, staged missing qBittorrent, and staged extra SQLC output | Passed as negative tests without mutation. |
| Missing/stale bundle, root/UI/qBittorrent/NZBGet/envdoc output matrix | Passed as negative tests; every check returned one without mutation. |
| SQLC stale/missing/extra output matrix | Passed as negative tests without mutation. |
| Clean generation and two consecutive offline write runs | Passed with no tracked or working-tree drift. |
| Alias, raw/interpreted/grouped, escaped forbidden-import matrix and allowed controls | Passed. |
| Root, UI, tools, qBittorrent, and NZBGet tests, race tests, vet, and module verification with `GOWORK=off` and `-mod=readonly` | Passed all five modules; root storage race completed in 215.929 seconds. |
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
- A-45: not accepted under the round-six dispatch. Clean generation, all five
  module gates, architecture, workflow, and generated-output matrices pass;
  pre-commit and aggregates now reject inverse partial staging, but the
  required direct invocation does not.

## Reviewer decision

`changes_requested`. Resolve or explicitly narrow the direct historical
working-generator requirement and return exact product, contract, and handoff
commits for another independent review. No product, execution-state, release,
deployment, or live-stack action is authorized by this receipt.
