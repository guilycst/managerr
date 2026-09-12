# C-05 independent review, round four

## Decision

`changes_requested`.

The round-three intent-to-add configuration and stale staged-output findings are
closed for the files the snapshot actually consumes. One P1 remains:
`run_staged_check` extracts the exact `git write-tree` snapshot and then
replaces the staged `scripts/generate.sh` with the working-tree copy. A prior
vulnerable generator can therefore remain staged and be committed while direct
generation, pre-commit, and the full CI guardrail all report success.

## Review identity and boundary

- Reviewer: `/root/f01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Correction dispatch checkpoint:
  `fce48a5d86f6b0fbdde57b46a4afe845dd87154a`.
- Reviewed product:
  `b9bf954933a6922bbebb69432d501921eec6b623`; its direct parent is the
  dispatch checkpoint and its tree is
  `916210fe54ba6a50f84f2af856b76b7904abd1b0`.
- Reviewed handoff:
  `c28435a2a5044ddcde1035336f58714fad2349f9`; it is a direct child of the
  product and was treated as a claim inventory.
- Coordinator state checkpoint:
  `d6778a9acd83554d8a827ca51cd11710cdeaf192`.
- Product scope is exactly `scripts/generate.sh`: 40 insertions and 2
  deletions. The scoped binary diff SHA-256 is
  `2c27eae09593947ee7f200a30a5a18d689b6c4b1c78a3bff15c4c2f8e02af75e`.
- Review scope is C-05's four owned product paths, A-43, A-45, the round-three
  staged-tree finding, and the previous generation, architecture, module, and
  workflow regressions.
- Checks ran in a clean detached reviewer worktree at the exact product.
  Adversarial mutation checks used disposable synthetic repositories. No
  product, execution-state, task, script, shared file, live service, media,
  credential, release, or deployment was changed.

## Round-three closure

### Intent-to-add cannot mask a deleted SQLC configuration

A disposable repository removed `sqlc.yaml` from the index and then applied
`git add -N sqlc.yaml`, leaving the valid working file and status
`DA sqlc.yaml`. The staged tree omitted the file.

- `generate.sh --check`, pre-commit, and `check-guardrails.sh --ci` each
  returned one.
- Each path reported that the authoritative SQLC configuration was absent from
  the staged tree.
- The staged tree ID, index, working-file hash, and `DA` status were identical
  before and after every check.

This closes the round-three configuration-membership bypass.

### Stale generated index content is checked even when the working copy is restored

Separate disposable repositories staged stale bytes and then restored the exact
product bytes only in the working tree.

- `internal/storage/sqlc/query.sql.go` remained `MM`; generation and
  pre-commit returned one with the SQLC out-of-date result.
- `clients/qbittorrent/generated/client.gen.go` remained `MM`; generation
  and pre-commit returned one with the qBittorrent generated-client
  out-of-date result.
- In both cases, the staged-tree ID, staged blob, working blob, and status were
  unchanged by failure.

The snapshot also preserved the prior missing, stale, and extra output
detection. This closes the round-three staged generated-output bypass.

## Finding

### P1: the exact staged tree is discarded for the generator script itself

At `scripts/generate.sh:49-70`, `run_staged_check` creates a tree with
`git write-tree`, archives it, and extracts it. Line 69 then runs:

```sh
cp "$repo_dir/scripts/generate.sh" "$staged_root/scripts/generate.sh"
```

The generated inputs and expected outputs remain staged, but the executable
guard that validates them does not. The claim at lines 66-68 that all inputs
come from the exact tree is therefore false for the generator.

Reproduction from the exact product in a disposable repository:

1. Replace `scripts/generate.sh` with the prior round-three product's version
   from `a3b37d9252e8a9a97bf6ed80b30fb31eb62111e8` and stage it.
2. Restore only the working copy to the round-four product version, leaving
   status `MM scripts/generate.sh`.
3. Record `git write-tree`, the index blob, the working-file hash, and status.
4. Run the working `scripts/generate.sh --check`, the repository pre-commit
   hook, and `scripts/check-guardrails.sh --ci`.
5. Re-record the tree, blobs, and status.

Observed results:

- Direct generation returned zero with `generation checks passed`.
- Pre-commit returned zero with `guardrail checks passed (fast)`.
- The full aggregate returned zero with `guardrail checks passed (ci)`.
- The staged tree remained
  `78cf0fbafb7e6d1039d00bb341d5239b4c3ce931` and still contained the prior
  generator. The working copy still contained the round-four
  `run_staged_check` implementation.
- Tree, index, and working-file hashes did not change during any check.

This is a commit-candidate bypass, not only a diagnostic inconsistency. The hook
can authorize committing the prior generator that lacks the staged-tree
protection it used to approve itself. Staging a different weakened generator
has the same trust-boundary shape.

Contract: C-05 requires deterministic committed generation and A-45 requires
clean regeneration with reproducible module checks. The round-four dispatch
explicitly requires success to bind to the exact `git write-tree` snapshot.
A successful hook must cover the generator that Git will commit.

Required correction: fail closed whenever the staged and working
`scripts/generate.sh` blobs differ before copying the trusted working checker,
or execute a separately trusted checker whose identity is outside the commit
candidate. Add a regression that stages the prior vulnerable generator,
restores the current working copy, and requires direct generation, pre-commit,
and `--ci` to return nonzero while the staged tree, index, and working copy
remain unchanged. Preserve the fixed intent-to-add and staged-output cases.

## Previous safeguards rechecked

- Missing or stale bundled OpenAPI, root server, UI client, qBittorrent client,
  NZBGet output, and environment documentation each returned one without
  recreating or changing expected files.
- SQLC stale, missing, and extra generated files each returned one. Generation
  remained isolated and expected content was unchanged.
- Two consecutive offline `generate.sh --write` runs in a disposable clean
  Git copy produced no tracked difference.
- Clean offline `generate.sh --check` passed with `GOWORK=off`,
  `GOPROXY=off`, and `GOSUMDB=off`.
- Forbidden same-module-root and `internal` client imports returned one for
  named, blank, dot, raw, interpreted, grouped, single-line, octal, hex,
  short-Unicode, and long-Unicode forms. Allowed same-client imports and
  import-like comments returned zero.
- No `go.work`, `go.work.sum`, local replacement, root-client coupling,
  cross-client coupling, private coordinate, or credential was found.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product, handoff, state, ancestry, one-file scope, tree, and scoped diff SHA-256 | Passed; recorded above. |
| Round-three intent-to-add deleted `sqlc.yaml` through generation, hook, and CI aggregate | Passed as a negative test; all returned one with tree/index/worktree unchanged. |
| Round-three staged stale SQLC and qBittorrent output with restored working copy | Passed as negative tests; generation and hook returned one with tree/index/worktree unchanged. |
| Partially staged prior generator with corrected working copy | Failed contract; generation, hook, and CI aggregate returned zero while the prior generator remained in `git write-tree`. |
| Missing/stale bundle, root/UI/qBittorrent/NZBGet/envdoc output matrix | Passed as negative tests; every check returned one without mutation. |
| SQLC stale/missing/extra output matrix | Passed as negative tests without mutation. |
| Clean generation and two consecutive offline write runs | Passed; no tracked or working-tree drift. |
| Direct, alias, raw/interpreted/grouped and escaped forbidden-import matrix plus allowed controls | Passed. |
| Root, UI, tools, qBittorrent, and NZBGet tests, race tests, vet, and module verification with `GOWORK=off` and `-mod=readonly` | Passed all five modules; root storage race completed in 207.093 seconds. |
| CGO-free Linux amd64/arm64 test compilation for all five modules | Passed all ten module/architecture combinations. |
| `check-api.sh`, Vacuum, `check-lint.sh`, and clean-tree `check-guardrails.sh --ci` | Passed; five lint runs had zero issues and Vacuum quality was 100/100. |
| `sh -n`, `actionlint`, and Ruby workflow YAML parsing | Passed. |
| Planning self-test and normal validation | Passed: 43 tasks, 60 acceptance cases, and local links are valid. |
| No workspace file/local replace and public dependency/secret/path scan | Passed. |
| Product diff check and detached checkout cleanliness before receipt | Passed. |

## Acceptance contribution

- A-43: the SQLC configuration and generated-query staged-tree corrections are
  accepted for C-05. Database runtime and migration behavior are unchanged.
- A-45: not accepted for C-05. Normal generation, every module check, offline
  reproducibility, architecture boundaries, workflow syntax, and generated
  artifact snapshot cases pass, but the commit candidate can still contain a
  generator different from the one that reported success.

## Reviewer decision

`changes_requested`. Correct the P1 staged-generator binding defect and return
exact product and handoff commits for another independent review. No product,
execution-state, release, deployment, or live-stack action is authorized by
this receipt.
