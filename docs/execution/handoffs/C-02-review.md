# C-02 independent review: modules and pinned tools

## Decision

`changes_requested`.

One P1 finding blocks C-02. No P0 findings remain. The tools dependency cleanup
committed during review resolved the only other issue found in the reviewed
module metadata.

## Review identity and boundary

- Release stage: `pre-v0.1.0`, target release `v0.0.1`.
- Dispatch base: `af392d3ef00865ea1d00c67e35157885af48a612`.
- Initial module commit: `c9e26e641b0f7d748fb144c0e87643eb710a967a`.
- Migration compatibility correction:
  `814312663471627053a2b44a6ded2ecb96c743af`.
- Reviewed head: `6140a5393a5ec607e14792a4267292bdbd2a3686`.
- Coordinator state and handoff checkpoint in reviewed ancestry:
  `15da2dc167e28a1478b0c9fc885b42ed5bd05f29`.
- Target: Git worktree `/Users/guilhermecastro/repos/managerr`.
- Review round: 1.
- Isolation receipt: producer transcript not read; no thread polling; Git evidence
  and commands inspected directly; no live service, credential, media, release,
  deploy or product-file mutation performed.
- Reviewer-owned path: this receipt only.

The reviewed head descends from the dispatch base. Product bytes stayed at the
recorded head through the final pass. Root, UI and tools modules have distinct
module paths. The tree contains no `go.work`, local `replace`, root-to-UI import,
or UI-to-root import. Root and UI contain no Go packages yet; tools contains only
the synthetic compatibility package.

## Finding

### P1: The generation script invokes pinned sqlc from the wrong module

Disposition: `current_blocker`.

Evidence: `scripts/generate.sh:69-73` detects root `sqlc.yaml`, changes directory
to the root module, then runs:

```sh
GOWORK=off go tool github.com/sqlc-dev/sqlc/cmd/sqlc generate
```

The sqlc tool directive belongs only to `tools/go.mod`. Running the exact tool
lookup from the root module exits 2 with:

```text
go: no such tool "github.com/sqlc-dev/sqlc/cmd/sqlc"
```

The same lookup from `tools/` succeeds and reports `v1.31.1`. The current
`./scripts/generate.sh --check` passes only because `sqlc.yaml` does not exist,
so it never enters the broken branch.

Failure mode: D-01 adds `sqlc.yaml`, then the standard generation command and CI
fail before sqlc can generate queries. This breaks the separate-tools-module
contract and blocks the next database slice.

Required change: resolve and execute sqlc through `tools/go.mod` while passing the
root configuration and preserving its intended path semantics. Keep `GOWORK=off`.
Prove the conditional branch resolves the pinned tool when a config is present;
the ordinary no-config generation check does not exercise it.

## Resolved during review

`GOWORK=off go mod tidy -diff` initially reported drift in `tools/go.mod` and
`tools/go.sum`. Commit `6140a5393a5ec607e14792a4267292bdbd2a3686`
removed six stale indirect requirements and reconciled sums. Independent rerun
now exits 0. Exact direct versions remain:

- Go language version `1.27.0`, toolchain `go1.27.1` in all modules.
- `github.com/oapi-codegen/oapi-codegen/v2 v2.8.0`.
- `github.com/sqlc-dev/sqlc v1.31.1`.
- `github.com/golang-migrate/migrate/v4 v4.19.1`.
- `github.com/g4s8/envdoc v1.11.0`.
- `modernc.org/sqlite v1.56.0`.
- `github.com/oapi-codegen/runtime v1.7.0` in root and UI.

## Checks and direct results

| Command or inspection | Result |
| --- | --- |
| Git ancestry, status, changed paths, module paths and import inventory | Passed. Reviewed head descends from the supplied base; tree was clean; no `go.work`, local `replace` or cross-module source import exists. |
| `python3 scripts/check_planning.py --self-test` | Passed: cycles, unknown dependencies and unsupported completion self-checks. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases and local links. |
| `GOWORK=off go mod verify` in root, `ui/` and `tools/` | Passed in all three modules. |
| Package-aware `go list`, `go test` and `go vet` in root and `ui/` | Passed. Both `go list ./...` calls returned no packages and exit 0; the guard reported that state instead of claiming tests ran. |
| `GOWORK=off go test -count=1 -v ./...` in `tools/` | Passed. `TestSQLiteMigrateDriver` opened modernc SQLite, applied embedded migration 1 through migrate `database/sqlite`, and verified version 1 with `dirty=false`. |
| `GOWORK=off go vet ./...` in `tools/` | Passed. |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go test -c -o /dev/null ./compatibility` | Passed. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go test -c -o /dev/null ./compatibility` | Passed. |
| `GOWORK=off go mod tidy -diff` in `tools/` | Passed at reviewed head after the dependency cleanup. |
| Tool-module version inspection and direct `go tool` probes | Passed from `tools/`; pins listed above. Root sqlc probe failed as recorded in the P1 finding. |
| `./scripts/generate.sh --check` | Passed for both frozen oapi-codegen configs. It generated disposable root server and UI client files and left no repository output. Sqlc branch was absent and therefore untested. |
| `actionlint .github/workflows/checks.yml` | Passed with actionlint v1.7.12. Workflow covers root, UI and tools and handles empty modules without naming nonexistent packages. |
| `git diff --check af392d3..6140a53` and `git diff --check` | Passed. |
| Focused public-data scan of reviewed additions | Passed. No credential value, tracker URL, private endpoint, real inventory, private hostname or user filesystem path found. |

## Acceptance contributions

- A-43: C-02 contribution is accepted. The synthetic test proves the selected
  modernc SQLite and migrate `database/sqlite` pair can open a database, apply an
  embedded migration and report a clean version on the current host. CGO-free
  linux/amd64 and linux/arm64 compilation passes. Full A-43 remains open for D-01:
  real schema/query consistency, supported upgrades, interrupted migrations,
  locking and second-executor rejection.
- A-45: not accepted for C-02 while the P1 finding remains. Independent module
  boundaries, exact tool pins, oapi-codegen generation and package-aware CI are
  sound, but the pinned sqlc path fails when its config appears. Full A-45 also
  requires later generated sqlc, envdoc and templ outputs plus their module checks.

## Deferred gates

These are outside the current blocker and must stay open:

- D-01 owns production migrations, sqlc configuration and queries, SQLite locking,
  interrupted migration behavior and second-process exclusion. Its generation
  work must also compare expected sqlc outputs and catch missing or untracked
  generated files. The current workflow's `git diff --exit-code` checks tracked
  changes only.
- C-03 owns typed bootstrap environment parsing with env/v11 and the envdoc source
  and generated document. C-02 pins envdoc but does not prove that future output.
- U-00 owns released Goshtoso, App Shell/runtime and templ version selection. Their
  absence from `ui/go.mod` is deliberate at this milestone.
- Target-container SQLite locking, migration recovery and runtime behavior remain
  later D-01 and V-02 evidence. Cross-compilation is not runtime validation.

## Next review event

Correct the sqlc module invocation on the same target, record a new exact product
commit, and request an independent rereview. Coordinator owns state updates and
integration. C-03 must remain blocked until C-02 review approves the corrected
head.
