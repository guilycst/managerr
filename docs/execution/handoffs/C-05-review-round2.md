# C-05 independent review, round two

## Decision

`changes_requested`.

The correction closes the oapi self-comparison defect and makes ordinary SQLC
output comparison side-effect-free. Named, blank, dot, raw-string, grouped and
ordinary interpreted imports are also covered. One P1 generation skip and one
P2 import-path decoding defect remain: deleting the now-required `sqlc.yaml`
causes every local guardrail to pass without running SQLC, and the standalone
architecture command accepts a semantically forbidden interpreted import when
the path uses a valid Go escape.

## Review identity and boundary

- Reviewer: `/root/f01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Prior reviewed product:
  `38a27ccf87ce04bc83f7180c99eb384df4bfcef9`.
- Original round-one receipt:
  `cdfcac8c1e7113d2dbeec060ee3923e13c3e5372`; coordinator-integrated receipt
  `5063525b6bbe8bdbd83560cd4e4532cc08ce8dbb`.
- Correction dispatch checkpoint:
  `17c239abaa5b6167326719ebd41d1f0c3428cfbe`.
- Reviewed product commit:
  `428199ae4e1f03936d58bb48dbee962186c7266f`; direct parent is the dispatch
  checkpoint; product tree
  `b690996553e830c626ba987067867a4291430f4c`.
- Handoff commit:
  `385a783a454ff9e500b1250c7df94be0a7f47a07`; direct child of the product.
- Coordinator state checkpoint:
  `6108ba26ae1e83111aedf9ef3ab916d3a6451c35`; direct child of the handoff.
- Product changes are confined to `scripts/generate.sh` and
  `scripts/check-lint.sh`: 177 insertions and 61 deletions. The scoped binary
  diff SHA-256 is
  `e58fc6e8cf3dda3ce771b73a1b43a867f8ca7d99fc1957dbeaf107a0d8a99409`.
- Review scope remains C-05's four product paths, A-43, A-45, and the three
  round-one findings. The producer handoff was read only as a claim inventory.
- Checks ran in a clean detached worktree at the exact product commit.
  Destructive cases used disposable synthetic copies. No product, state, task,
  or shared file was changed.
- No live service, credential, private coordinate, media, release, deployment,
  or remote mutation was used.

## Round-one closure

### Oapi expected and candidate paths: resolved

`scripts/generate.sh:22-31` now uses names unique to `make_config`, while
`generate_oapi` keeps distinct `oapi_expected_output` and
`oapi_candidate_output` values at `:61-90`. Independent probes removed and
separately altered each of these committed artifacts:

```text
internal/api/generated/server.gen.go
ui/internal/api/generated/client.gen.go
clients/qbittorrent/generated/client.gen.go
```

All six missing/stale cases returned one. Missing files stayed missing, stale
files retained their exact pre-check hashes, and each error named the committed
expected path. No candidate overwrote the checkout. This closes the round-one
self-comparison P1.

### SQLC stale/missing/extra output and staged state: resolved

Check mode now copies the current SQLC config, migration schema and query input
into a fresh temporary root, generates there, and recursively compares the
candidate output directory with `internal/storage/sqlc` at
`scripts/generate.sh:176-213`.

Independent stale-file, missing-file and extra-file cases each returned one.
The altered expected tree was byte-identical before and after every check. In a
disposable Git copy with stale SQLC output staged, the pre-commit hook returned
one; both index and working-tree hashes remained unchanged. This closes the
round-one mutation and stale-staged-output P1 for an existing configuration.

### Aliased and raw import declarations: resolved for the requested ordinary forms

The replacement scanner rejects direct, named, blank, dot, raw-string and
grouped forbidden imports. It allows same-client imports and ignores import-like
text inside comments and ordinary strings. Each source file was accepted by
`gofmt` before the architecture result was evaluated. The exact round-one named
root/internal and blank cross-client bypasses now return one.

## Findings

### P1: removing `sqlc.yaml` silently disables SQLC generation and passes every guardrail

The production repository now requires SQLC generation, but
`scripts/generate.sh:216-218` still treats `sqlc.yaml` as optional:

```sh
if [ -f "$repo_dir/sqlc.yaml" ]; then
  generate_sqlc
fi
```

In a disposable exact-product copy, moving only `sqlc.yaml` aside and running
the offline generation check returned zero with `generation checks passed`.
The file remained absent. In a disposable Git copy, staging deletion of
`sqlc.yaml` and invoking `./.githooks/pre-commit` also returned zero and left
the deletion staged. Finally, `scripts/check-guardrails.sh --ci` returned zero
against that same staged deletion after all five module tests, vet, lint and
module verification completed:

```text
aggregate_rc=0
staged=D sqlc.yaml
guardrail checks passed (ci)
```

The GitHub generation job cannot recover this: a committed deletion is already
clean to `git diff --exit-code`, and the root module compiles against the old
committed SQLC output without the generator config.

Contract: C-05 and A-45 require SQLC regeneration rather than optional
discovery. The review dispatch also requires no silent hook skip. Once D-01
introduced the authoritative production `sqlc.yaml`, absence of that source is
a failed generation precondition.

Required correction: fail explicitly when `sqlc.yaml` is missing before any
generation success message. Add direct generation, fast hook and aggregate
guardrail regressions for a missing configuration, proving a nonzero result and
no index/worktree change.

### P2: interpreted import paths are compared before Go escape decoding

The custom lexer records the source slice between double quotes at
`scripts/check-lint.sh:95-108` but never applies Go string-literal unquoting.
Boundary comparison at `:160-170` therefore uses lexical bytes rather than the
semantic import path.

This valid Go declaration was independently tested:

```go
import _ "github.com/guilycst/masta\x72r/internal/domain"
```

`scripts/check-lint.sh --architecture-only` returned zero and printed
`standalone client import boundaries passed`. With a disposable local module
binding used only to prove compiler semantics, `GOWORK=off go list -mod=mod
./...` also returned zero and resolved the import as the Mastarr root/internal
path. `gofmt -d` shows Go canonicalizing it to
`github.com/guilycst/mastarr/internal/domain`.

The aggregate fast guardrail returns one later because the escaped spelling is
not gofmt-canonical. That secondary formatting gate bounds the current impact,
but the architecture-only command still reports the forbidden semantic import
as safe, contrary to its independent-compilation contract and the round-two
requirement to handle interpreted import strings.

Required correction: parse import specifications with Go syntax-aware tooling
and unquote every `ImportSpec.Path` using Go string-literal rules before module
boundary comparison. Add escaped interpreted-path cases beside ordinary quoted
and raw-string regressions. Malformed literals should fail closed.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product, handoff and state identities; ancestry; two-file scoped diff and SHA-256 | Passed; recorded above. |
| Clean `GOWORK=off GOPROXY=off GOSUMDB=off ./scripts/generate.sh --check` | Passed and left the detached checkout clean. |
| Two consecutive offline `generate.sh --write` runs in a disposable Git copy | Passed; neither produced a tracked diff. |
| Root/UI/qBittorrent missing and stale generated artifact matrix | Passed as negative tests: all returned one and expected paths remained unchanged. |
| SQLC stale, missing and extra output matrix | Passed as negative tests: all returned one and expected paths remained unchanged. |
| Staged stale SQLC pre-commit probe | Passed as a negative test: hook returned one; index and worktree remained unchanged. |
| Missing `sqlc.yaml` generation, hook and aggregate probes | Failed contract: generation, pre-commit and `--ci` guardrails all returned zero. |
| Direct/named/blank/dot/raw/grouped forbidden imports and same-client/comment controls | Passed for ordinary forms; forbidden cases returned one and controls zero. |
| Escaped interpreted forbidden import | Failed contract: architecture-only returned zero; Go tooling resolved the forbidden path. |
| Root, UI, tools, qBittorrent and NZBGet `go test -mod=readonly ./...`, race test, vet and module verification with `GOWORK=off` | Passed in all five modules; root storage race completed in 224.694 seconds. |
| CGO-free Linux amd64 and arm64 `go test -mod=readonly -run '^$' -exec=true ./...` in all five modules | Passed all ten module/architecture combinations. |
| Clean-tree full lint, API/Vacuum and `scripts/check-guardrails.sh --ci` | Passed; five lint runs reported zero issues and Vacuum quality was 100/100. |
| `actionlint`, Ruby workflow YAML parse and `sh -n` for scripts and hook | Passed. |
| Planning self-test and normal check | Passed: 43 tasks, 60 acceptance cases and local links are valid. |
| Current dependency graph, `go.work`/`go.work.sum`, local replace and cross-client scan | Passed; no workspace file, local replacement or forbidden current dependency exists. |
| Product diff secret/private-coordinate scan, executable modes and `git diff --check` | Passed. |
| Clean detached reviewer status before receipt creation | Passed. |

## Acceptance contribution

- A-43: database runtime behavior remains green and ordinary SQLC output
  comparison is now isolated. C-05's generation contribution remains open
  because deleting the authoritative config silently disables its consistency
  check. This does not reopen D-01 runtime behavior.
- A-45: not accepted for C-05. Root/UI/qBittorrent and configured SQLC output
  checks are now deterministic and side-effect-free, and every clean module
  check passes. The required SQLC source can still disappear undetected and the
  standalone architecture check does not yet compare decoded interpreted
  import paths.

## Reviewer decision

`changes_requested`. Correct the P1 missing-configuration skip and P2 semantic
import decoding defect, then return exact product and handoff commits for
another independent review. No product, execution-state, release, deployment,
or live-stack action is authorized by this receipt.
