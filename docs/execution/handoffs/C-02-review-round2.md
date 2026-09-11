# C-02 independent review round two: modules and pinned tools

## Decision

`approved`.

The round-one P1 is fixed. No P0, P1 or P2 findings remain for C-02. This
decision accepts C-02's bounded contributions to A-43 and A-45. It does not
close either full acceptance case.

## Review identity and boundary

- Release stage: `pre-v0.1.0`, target release `v0.0.1`.
- Dispatch base: `af392d3ef00865ea1d00c67e35157885af48a612`.
- Corrected implementation commit:
  `e2db57d9a8a875ef0c7f16f66e850601a572d33b`.
- Coordinator checkpoint supplied at dispatch:
  `3f3f09c5737206cd16ebd01baf751cc4b7d46038`.
- Final reviewed head: `624f7b2b2c7e05a7e563e171834d04c1d72d2342`.
- Review round: 2.
- Target: shared repository checkout, recorded without a local filesystem path.
- Reviewer-owned path: this receipt only.
- Isolation receipt: producer transcript and hidden reasoning were not read; no
  producer thread, terminal or status polling occurred. Evidence came from Git,
  repository files and commands run by this reviewer.
- Safety boundary: no live service, credentials, real media, release, deploy,
  upload or product-file mutation occurred.

The final head descends from both the dispatch base and corrected implementation
commit. It differs from the fully tested coordinator checkpoint only in the
round-one receipt's local-path redaction. Product bytes did not change during the
final identity pass.

## Round-one finding resolution

Round one found `scripts/generate.sh` running sqlc from the root module even
though `tools/go.mod` owns the tool directive. Commit
`e2db57d9a8a875ef0c7f16f66e850601a572d33b` changes the sqlc branch to:

```sh
(
  cd "$tools_dir"
  GOWORK=off go tool github.com/sqlc-dev/sqlc/cmd/sqlc generate \
    -f "$repo_dir/sqlc.yaml"
)
```

Direct inspection confirms the change is limited to those working-directory and
config-path lines. The disposable fixture below exercises the formerly failing
branch rather than relying on the ordinary no-config path.

## Disposable sqlc fixture

The fixture copied `scripts/`, `tools/`, `api/` and `ui/` into a temporary
directory. Its root `sqlc.yaml` used version 2, SQLite, root-relative
`fixture/schema.sql` and `fixture/query.sql` inputs, package `fixture`, and output
`fixture/generated`. The schema defined one synthetic `widgets` table; the query
defined `GetWidget :one`. No repository or user data entered the fixture.

Core commands:

```sh
fixture_dir=$(mktemp -d /tmp/mastarr-c02-round2.XXXXXX)
cp -R scripts tools api ui "$fixture_dir"/
cd "$fixture_dir/tools"
GOWORK=off go tool github.com/sqlc-dev/sqlc/cmd/sqlc version
../scripts/generate.sh --check
find "$fixture_dir/fixture/generated" -maxdepth 1 -type f -print
find "$fixture_dir" -depth -delete
```

Result: exit 0. Direct tool lookup printed `v1.31.1`.
`scripts/generate.sh --check` printed `generation checks passed`. Sqlc wrote
`models.go`, `db.go` and `query.sql.go`; all three headers recorded sqlc
`v1.31.1`, and the generated query matched the synthetic root query. The final
`find ... -delete` removed the disposable fixture.

## Independent checks

| Command or inspection | Result |
| --- | --- |
| `git merge-base --is-ancestor af392d3ef00865ea1d00c67e35157885af48a612 HEAD` and the same check for `e2db57d9a8a875ef0c7f16f66e850601a572d33b` | Passed. Final head contains the dispatch base and sqlc correction. |
| `git diff --name-status 3f3f09c5737206cd16ebd01baf751cc4b7d46038..624f7b2b2c7e05a7e563e171834d04c1d72d2342` | One documentation-only change: `docs/execution/handoffs/C-02-review.md`. |
| `python3 scripts/check_planning.py --self-test` | Exit 0. Cycles, unknown dependencies and unsupported completion self-checks passed; planning remained valid. |
| `python3 scripts/check_planning.py` | Exit 0. 38 tasks, 60 acceptance cases and local links validated. |
| `GOWORK=off go mod tidy -diff` in root | Exit 1. With no root Go packages, tidy would remove the three deliberately pre-pinned runtime requirements and six sum lines. Recorded below as a bounded pre-package deferral. |
| `GOWORK=off go mod tidy -diff` in `ui/` | Exit 1. With no UI Go packages, tidy would remove the deliberately pre-pinned oapi runtime requirement and two sum lines. Recorded below as a bounded pre-package deferral. |
| `GOWORK=off go mod tidy -diff` in `tools/` | Exit 0 with no diff. |
| `GOWORK=off go mod verify` in root, `ui/` and `tools/` | Exit 0 in all three modules; each printed `all modules verified`. |
| Package-aware `GOWORK=off go list ./...`, test and vet guards in root and `ui/` | Exit 0. Both modules reported no packages; no test execution was claimed. |
| `GOWORK=off go test -count=1 -v ./...` in `tools/` | Exit 0. `TestSQLiteMigrateDriver` opened modernc SQLite, applied embedded migration 1 through migrate's `database/sqlite` driver, and observed version 1 with `dirty=false`. |
| `GOWORK=off go vet ./...` in `tools/` | Exit 0. |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go test -c -o /dev/null ./compatibility` in `tools/` | Exit 0. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go test -c -o /dev/null ./compatibility` in `tools/` | Exit 0. |
| `./scripts/generate.sh --check` | Exit 0 with `generation checks passed`; both pinned oapi-codegen configurations generated in the script's disposable directory. |
| Disposable root `sqlc.yaml` branch described above | Exit 0; tools-module sqlc `v1.31.1` generated all expected synthetic files from root-relative inputs. |
| `actionlint -version && actionlint .github/workflows/checks.yml` | Exit 0 with actionlint `v1.7.12`. |
| `git diff --check af392d3ef00865ea1d00c67e35157885af48a612..HEAD && git diff --check` | Exit 0 before this receipt. |
| `find` and `rg` inspections for `go.work`, local `replace` directives and root/UI cross-module imports | Passed. None exists. |
| Focused scan of C-02 additions for concrete home paths, credential-bearing URLs, tracker passkeys/announce values and private-key headers | Passed at final head. The prior receipt path was redacted by `624f7b2b2c7e05a7e563e171834d04c1d72d2342`; no secret or private value was found. |

Version inspection also confirmed Go language version `1.27.0` and toolchain
`go1.27.1` in all modules; migrate `v4.19.1`, modernc SQLite `v1.56.0`,
oapi-codegen `v2.8.0`, sqlc `v1.31.1`, envdoc `v1.11.0`, and oapi runtime
`v1.7.0` where declared.

## Acceptance contributions

- A-43: C-02 contribution accepted. The embedded migration test proves the
  selected pure-Go SQLite and migrate driver pair can open a database, apply an
  embedded migration and report a clean version on the review host. CGO-free
  linux/amd64 and linux/arm64 compilation also passes. Full A-43 stays open for
  D-01 and later container verification.
- A-45: C-02 contribution accepted. Independent modules, exact pins,
  GOWORK-off verification, package-aware checks, oapi generation and the corrected
  sqlc tool path pass. Full A-45 stays open for generated sqlc, envdoc and templ
  artifacts plus packages that do not exist yet.

## Deferred evidence and ownership

- Root and UI `go mod tidy -diff` are not clean while those modules have no Go
  packages and retain C-02's future runtime pins. This is visible, not accepted as
  a clean tidy result. C-03 and the first UI generated-client task must rerun tidy
  after their packages consume the pins; unexpected remaining drift then blocks
  their module checks.
- D-01 owns the production schema, migrations, queries, upgrade behavior,
  interrupted migrations, locking and second-executor rejection. When `sqlc.yaml`
  lands, the coordinator must make check mode compare sqlc outputs and detect
  missing untracked generated files. Current `git diff --exit-code` detects only
  tracked changes.
- C-03 owns typed environment parsing through env/v11 plus the envdoc source and
  generated document. A pinned envdoc command alone does not satisfy that work.
- U-00 owns released Goshtoso, App Shell/runtime and templ selection. U-01 owns
  the generated UI client consumer boundary. Their dependencies and runtime
  behavior remain unproven here.
- Cross-compilation does not prove target-container SQLite locking or migration
  recovery. D-01 and V-02 retain those runtime gates.

## Resume checkpoint

C-02 has no open review finding at final head
`624f7b2b2c7e05a7e563e171834d04c1d72d2342`. Coordinator may record approval and
integrate this reviewed identity. C-03 and U-00 may advance under their recorded,
nonoverlapping ownership. No release, deployment or live-stack effect follows
from this local approval.
