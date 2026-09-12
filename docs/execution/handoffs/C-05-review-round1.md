# C-05 independent review, round one

## Decision

`changes_requested`.

Three P1 findings remain. The checked-in scripts exercise all five modules on
the clean product tree, but the generation and client-boundary checks do not
fail closed under adversarial drift. A stale or missing oapi-codegen artifact
can pass generation, stale SQLC output is repaired by `--check` while the
command reports success, and valid aliased Go imports bypass the standalone
client boundary. These gaps also let the versioned pre-commit hook accept
invalid staged content.

## Review identity and boundary

- Reviewer: `/root/f01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Initial product commit:
  `4776dd80bda5a5a5c1b1bfcb600521cf5d283f99`; direct parent
  `50809b6e9c61c2b38ca2f10c322e91d412ad4ed0`; product tree
  `2a8a44b12537b0a4bc829c1ad7d82016543fc523`.
- Final product commit:
  `38a27ccf87ce04bc83f7180c99eb384df4bfcef9`; direct parent is the
  initial product; product tree
  `a340159b29aaf700ba93210b7742911c442a78b3`.
- Handoff commit:
  `343a0d68562e587f1356325bbed705338e293178`; direct child of the final
  product.
- Coordinator state checkpoint:
  `8cb78a6b181cb7c1446df5912459350ddad5fbf9`; direct child of the
  handoff.
- Review scope: `scripts/generate.sh`, `scripts/check-guardrails.sh`,
  `scripts/check-lint.sh`, `.github/workflows/checks.yml`, C-05, A-43 and
  A-45. The handoff was read from its exact commit and not treated as proof.
- The product series changes exactly the four assigned product files: 221
  insertions and 60 deletions. The scoped binary diff SHA-256 is
  `43dd3344119f03d0cdefeeb2bc875e9905db5596816edb0ae6651852ab335186`.
- Checks ran in a clean detached reviewer worktree at the exact final product.
  Disposable synthetic copies were used for destructive negative probes; no
  product or execution-state file was changed.
- No credential, private coordinate, live service, media, release, deployment,
  or remote mutation was used.

## Findings

### P1: every oapi-codegen check compares the temporary output to itself

`scripts/generate.sh:22-31` assigns to the shell-global variable `output` in
`make_config`. `generate_oapi` first assigns `output` to the committed artifact
at `:54-64`, but its call to `make_config` at `:67` overwrites that value with
`target`. The missing-file and byte comparison at `:74-82` therefore inspect
the temporary generated file twice. They never inspect the committed root, UI,
or qBittorrent binding.

Independent missing-file probes produced:

```text
root internal/api/generated/server.gen.go:          rc=0, not recreated
UI ui/internal/api/generated/client.gen.go:         rc=0, not recreated
qBittorrent generated/client.gen.go:                rc=0, not recreated
stdout for each: generation checks passed
NZBGet generated.go control:                        rc=1, missing-file error
```

A second qBittorrent probe appended a compilable stale sentinel. The committed
artifact's expected SHA-256 was
`31702df10c833cb77e8d85e33e0da9bfb2ffa79aed957a93ad00c0abac9ecd72`;
the altered file remained
`49eeb53e6b6a3ee91a36cec3d597f53bfd20f99e27e71a0d042f58b8296cd143`
before and after `generate.sh --check`. The command returned zero, and the
qBittorrent module still passed `go test -mod=readonly ./...`. A later CI
`git diff --exit-code` cannot detect stale bytes already committed when check
mode only writes the temporary artifact.

The pre-commit impact is direct. In a disposable Git copy, staging deletion of
the qBittorrent binding and invoking `./.githooks/pre-commit` returned zero,
left the deletion staged, and printed both `generation checks passed` and
`guardrail checks passed (fast)`.

Contract: C-05 requires deterministic generation for the root, UI and both
standalone clients. A-45 requires clean regeneration of the strict API/client
artifacts. The handoff additionally claims that every expected artifact is
checked and missing output fails. The current self-comparison proves none of
those three oapi artifacts.

Required correction: keep the committed-output path distinct from temporary
configuration variables across nested shell functions, then add negative
regressions for both missing and byte-stale root, UI and qBittorrent outputs.
Each must return nonzero without modifying the checkout in `--check` mode.

### P1: SQLC `--check` mutates the checkout and can approve stale staged output

The SQLC branch at `scripts/generate.sh:169-175` always invokes `sqlc generate`
against the repository configuration and repository output paths. It does this
in both `--write` and `--check` modes and performs no missing or byte comparison
against an isolated candidate.

In a disposable exact-product copy, a stale sentinel changed
`internal/storage/sqlc/query.sql.go` from its expected SHA-256
`11cdc32d1810e14f5fe53a6297a4dc79f1dfb4a9f24539aab8d3635ad02b3022`
to
`1f2821ea575457f65af3be902daf5f85c99e9cf48742c2c413ba8a4ed749d134`.
Running the following restored the expected bytes and returned zero:

```sh
GOWORK=off GOPROXY=off GOSUMDB=off ./scripts/generate.sh --check
# generation checks passed
```

This is not merely a read-only-command issue. In a disposable Git copy, the
stale SQLC file was staged before invoking the versioned hook. The hook returned
zero. SQLC repaired only the working-tree copy, so Git reported `MM` and the
stale sentinel remained in the index while disappearing from the working tree.
A commit driven by that successful hook would contain the stale generated
artifact.

Contract: A-45 requires clean regeneration, and the C-05 deliverable keeps
generated output committed and reproducible. The pre-commit guardrail must not
silently accept a stale staged generator result. This also carries forward the
earlier C-02 review requirement to compare SQLC output once the production
`sqlc.yaml` exists.

Required correction: make SQLC check mode generate into an isolated candidate
tree or equivalent safe staging area and compare the complete expected SQLC
file set to the committed output. It must reject missing, extra and byte-stale
files, leave both index and worktree untouched, and include a regression that
executes the versioned hook with stale staged SQLC output.

### P1: valid aliased single-line imports bypass the standalone-client boundary

The parser in `scripts/check-lint.sh:67-98` recognizes a single import only
when the path quote immediately follows `import`. Valid declarations with an
alias, blank identifier or dot before the import path do not match the regular
expression at `:75`. The grouped-import expression happens to accept aliases,
so equivalent syntax receives different enforcement.

The independent control and adversarial matrix was:

```text
import "github.com/guilycst/mastarr/internal/domain"
  rejected, rc=1

import forbidden "github.com/guilycst/mastarr/internal/domain"
  accepted, rc=0

import _ "github.com/guilycst/mastarr/clients/qbittorrent"
  from clients/nzbget: accepted, rc=0
```

The aliased cross-client file was gofmt-clean and staged in a disposable Git
copy. `./.githooks/pre-commit` returned zero and printed
`standalone client import boundaries passed`. The fast hook does not compile
either standalone module, so it supplies no secondary protection.

Contract: C-05 explicitly requires client modules to reject imports of Mastarr
root/internal packages and other standalone clients. This must hold for every
valid Go import declaration, not only one textual spelling.

Required correction: derive imports with Go syntax-aware tooling, or otherwise
cover all valid single and grouped import forms, including named, blank, dot and
raw-string imports. Add negative regressions for root, internal and cross-client
paths plus positive same-client imports, and exercise the architecture-only
path used by the hook.

## Positive evidence retained

- The clean final product passes generation with `GOWORK=off`, `GOPROXY=off`
  and `GOSUMDB=off`; the detached checkout remains clean. Two consecutive
  offline `--write` runs in a disposable Git copy produced no tracked diff.
  This proves the pinned local generators work from the populated module cache,
  but it does not cure the false-negative cases above.
- NZBGet generation correctly rejects a missing committed `generated.go`.
- Root, UI, tools, qBittorrent and NZBGet each passed
  `go test -mod=readonly ./...`, `go test -mod=readonly -race ./...`,
  `go vet -mod=readonly ./...`, and `go mod verify` with `GOWORK=off`. Root
  storage race tests completed in 203.031 seconds.
- The same five modules passed CGO-free Linux amd64 and arm64 test compilation
  with `-run '^$' -exec=true` and `-mod=readonly`.
- `scripts/check-lint.sh` passed all five pinned module lint runs on the clean
  tree. Direct dependency inspection shows qBittorrent imports only its own
  generated subpackage and NZBGet imports only its own module.
- `scripts/check-guardrails.sh --ci` passed generation, Vacuum/API, formatting,
  lint, architecture, tests, vet and module verification on the clean tree.
  The five-module CI matrix and aggregate module loop are present and reject a
  missing module manifest or empty discovered package set.
- `scripts/check-api.sh` passed with Vacuum quality 100/100. `actionlint`, Ruby
  YAML parsing and `sh -n` passed for the workflow and owned shell scripts.
- `python3 scripts/check_planning.py --self-test` and the normal planning check
  passed: 43 tasks, 60 acceptance cases and local links are valid.
- No `go.work`, `go.work.sum`, local `replace` directive, existing cross-client
  dependency, user-specific path, credential, private coordinate or secret was
  found in the product diff. Owned scripts and the versioned hook retain mode
  `100755`.
- The exact product diff and detached worktree passed `git diff --check`; the
  worktree was clean before this receipt was created.

## Acceptance contribution

- A-43: the existing database tests, SQLC output and module checks remain green,
  but C-05's reproducibility guard cannot be accepted while SQLC check mode can
  mutate and approve stale staged output. This decision does not reopen D-01's
  independently reviewed runtime database behavior.
- A-45: not accepted for C-05. All clean-tree module checks pass, but strict
  root/UI/qBittorrent generated artifacts are not actually compared, SQLC check
  mode is mutating, and the standalone-client architecture boundary is
  syntactically bypassable.

## Reviewer decision

`changes_requested`. Correct all three P1 findings and return an exact product
and handoff commit for another independent review. No product, execution-state,
release, deployment or live-stack action is authorized by this receipt.
