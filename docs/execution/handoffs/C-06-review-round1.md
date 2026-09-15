# C-06 independent review, round one

Decision: **changes_requested**. Generation and local Go matrices include all
six standalone clients, but the dedicated CI cross-build matrix expands to only
Seerr, and standalone OpenAPI contracts are not covered by aggregate Vacuum.

## Exact identity and scope

- Reviewer: `/root/x05_reviewer`; independent of product authorship.
- Product: `4ca9df839a7187e63f8058807aa11ef847c50f55`.
- Product tree: `121f9461df545093171fd7d18c1ec9a619d50367`.
- Handoff: `152cff6e6ab2f9cf4991d337b622e54e147ce381`, path
  `docs/execution/handoffs/C-06.md`; direct parent is the product above.
- Handoff tree: `5044d1b8d899be714b67eadd5cd77f8d6283eca1`.
- Dispatch checkpoint: `5244388f0371b08365a96c3f5aa53b35eae40376`.
- Scoped binary product diff SHA-256:
  `76043f65b3d1fc83e63388f9992294306c371cac19984be9f0c274f112bf2525`.
- Scope: `scripts/generate.sh`, `scripts/check-guardrails.sh`,
  `scripts/check-lint.sh`, `.github/workflows/checks.yml`, C-06 and its
  A-43/A-45 contribution. No database implementation acceptance is claimed.
- Review used a clean detached checkout at the exact handoff. Negative probes
  ran only in an archive of the exact product. Product, state and unrelated
  paths were not edited; only this receipt is committed.

## Findings

### R1 — P2: CI cross-build matrix silently drops eight modules

Location: `.github/workflows/checks.yml:254`-`:274`.
Contract: C-06 requires Linux amd64 and arm64 CGO-free CI coverage for all nine
modules, including all six standalone clients.

Only `arch` is an original matrix axis. Each include object adds `module` and
`directory` to both original combinations; subsequent include objects overwrite
those previously added fields. The final Seerr include therefore wins for both
architectures. This is not a Cartesian product of module and architecture.
GitHub's [documented matrix include semantics](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/run-job-variations#expanding-or-adding-matrix-configurations)
allow later includes to overwrite fields introduced by earlier includes, while
preserving original axis values.

Independent expansion of the committed YAML using those semantics produced:

```json
[
  {"arch":"amd64","module":"Seerr client","directory":"clients/seerr"},
  {"arch":"arm64","module":"Seerr client","directory":"clients/seerr"}
]
```

Expected: eighteen distinct module/architecture combinations. Actual: two.
`actionlint` and YAML parsing both succeed because this is valid workflow syntax.
The aggregate guardrails job separately loops over all nine modules and both
architectures; this limits the impact but does not fix the dedicated job or its
claimed per-module CI coverage.

Required correction: define both independent axes, using module objects containing
name/directory, or enumerate all eighteen include entries with explicit architecture.
Add a deterministic expansion assertion for exact pair coverage and uniqueness;
syntax validation alone cannot catch this defect.

### R2 — P2: standalone OpenAPI contracts bypass the zero-warning Vacuum gate

Locations: `scripts/check-guardrails.sh` fast/API call and CI path;
`.github/workflows/checks.yml` generation/API/aggregate jobs. Their invoked
`check-api.sh:11`-`:19` applies Vacuum exclusively to `api/openapi.yaml`.
None of the standalone checker scripts invokes Vacuum.
Contract: the assigned C-06 matrix review requires standalone contract validation
with warning/error failures, alongside generation and module checks.

The root generator now includes qBittorrent, Sonarr, Radarr, Jellyfin and Seerr
OpenAPI documents, but successful generation does not imply schema validity under
Vacuum. No aggregate or workflow command lints those five documents. NZBGet owns
OpenRPC, so its format-appropriate generator validation must remain distinct;
feeding OpenRPC to an OpenAPI validator is not the requested fix.

Independent reproduction in an exact-product archive:

1. Append `unexpectedTopLevelField: true` to `clients/seerr/openapi.yaml`.
2. Run `GOWORK=off GOPROXY=off GOSUMDB=off scripts/generate.sh --check`:
   exit 0, generated output remains byte-identical.
3. Run the archive's `scripts/check-api.sh` with the same offline environment:
   exit 0, reporting API checks passed.
4. Run pinned Vacuum directly against that Seerr contract with the repository
   ruleset, `--remote=false --fail-severity=warn`: exit 1, one schema error and
   zero warnings. Quality score is 10/100.

The unmodified five standalone OpenAPI contracts all pass direct pinned Vacuum.
The defect is missing enforcement, not an existing invalid product document.

Required correction: put all five standalone OpenAPI contracts behind the same
pinned, offline-capable zero-warning/error gate used by aggregate checks and CI.
Keep explicit OpenRPC validation for NZBGet. Add a negative contract probe proving
that an invalid standalone document fails the authoritative command even when
its generated Go output is unchanged.

## Verified behavior and limitations

- Root generation explicitly compares committed generated outputs for every
  standalone client, preserving each module's own specification and internal
  generated package. All tool invocations use `GOWORK=off`, `GOPROXY=off` and
  `GOSUMDB=off`; warmed-tool offline generation succeeds.
- Local lint and aggregate test/race/vet/verification matrices list root, UI,
  tools and all six clients. Local cross-build loops explicitly cover amd64 and
  arm64. The Go CI include-only matrix has nine entries and is valid; R1 affects
  the separate cross-build strategy with its extra axis.
- Module-isolation negative probes refuse a synthetic `go.work` and a Seerr
  local replace before generation, both exit 1. Package-discovery and command
  failures propagate through `set -eu`; no silent success path was added.
- The qBittorrent module-local checker remains a documented legacy bootstrap
  limitation: forcing `GOPROXY=off GOSUMDB=off` reproduces its pinned version
  deprecation lookup failure. Its normal configured resolution passes. Root
  authoritative generation remains offline and does not use that fallback.
  No product checker was weakened to hide the limitation.
- No module publication, root adapter migration, runtime capability enablement,
  native upstream compatibility or live CI/deployment behavior is established.
  G-01 and release/runtime gates remain separate.

## Independent checks

- Offline root `scripts/generate.sh --check`: exit 0; committed outputs match.
- qBittorrent local checker with normal resolution: exit 0.
- NZBGet/Sonarr/Radarr/Jellyfin/Seerr local checkers with
  `GOWORK=off GOPROXY=off GOSUMDB=off`: exit 0 each.
- Forced-offline qBittorrent local checker: exit 1, documented version lookup
  limitation; authoritative offline generation above passes.
- Direct pinned Vacuum for the five standalone OpenAPI documents with repository
  ruleset, offline and warning failure threshold: exit 0 each.
- Invalid standalone contract reproduction: generation and aggregate API exit 0;
  direct Vacuum exit 1. This is the R2 enforcement failure.
- YAML parse, `actionlint .github/workflows/checks.yml`, and `sh -n` on assigned
  shell scripts: exit 0. Matrix semantic expansion demonstrates R1.
- `python3 scripts/check_planning.py`: exit 0; 53 tasks, 60 acceptance cases and links.
- `python3 scripts/check-architecture.py`: exit 0.
- Module-isolation go.work/replace negative probes: exit 1 as required.
- `./scripts/check-guardrails.sh --ci`: full entrypoint was run. Generation,
  root-only Vacuum, architecture, formatting, all nine lint runs and ordinary
  root tests passed. Root race run made no further progress while its storage
  test process remained active for over three minutes; the reviewer terminated
  only that owned test process to bound the review. Aggregate exit 1 (signal
  termination), not a successful full gate or a diagnosed product regression.
  This out-of-scope root storage check requires a fresh full run before integration.
- Independently rerun UI, tools and all six clients: tests, race tests, vet,
  module verification and Linux amd64/arm64 CGO-free builds exit 0 for each.
- Independent root vet, module verification and both Linux CGO-free builds:
  exit 0. Root ordinary tests passed in the aggregate run; complete root race
  success is not claimed.
- `git diff --check`: exit 0 before receipt.

Correct R1/R2 in separate atomic slices and request independent re-review against
exact product/handoff commits. Only this review receipt is committed; its exact
SHA and final aggregate/hook result are reported after commit.
