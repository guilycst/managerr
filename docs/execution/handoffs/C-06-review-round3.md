# C-06 independent review, round three

Decision: **changes_requested** for the remaining R3 verification gate. R1,
R2 and R2a are closed; no new blocking code finding was found in the owned C-06
paths. Independent full aggregate timed out at 180 seconds in root storage race
tests, so the required bounded full success is not reproduced.

## Exact identity and scope

- Reviewer: `/root/x05_reviewer`; independent of product authorship.
- Product: `5e53134e40322b512efef1865d78da2da6368e5b`.
- Product tree: `926c1439fae83d09fded1850d2687b727ee13329`.
- Handoff: `90f44a58057cad652e4eb499d5ddac94b2c05c98`, path
  `docs/execution/handoffs/C-06-correction-round2.md`; direct parent is the product.
- Handoff tree: `53c8894c43133c38777d0cade4ce178d566c19fa`.
- Dispatch checkpoint: `a135bb65f9dc4bc0992139f95f232d5a709d5e84`.
- Previous product: `650cdfa51ec161b260097652246a59753429b2ab`.
- Previous receipt: `3ef41d3a567692f67fb70d18d290003e44e34777`.
- Scoped binary correction diff SHA-256:
  `307f1d3ba01e72ad4aca8937f985f9e8fd21b5815258e4a8ebaf25fc86b39207`.
- Scope: the four C-06 owned script/workflow paths, correction handoff and
  R1/R2/R2a/R3, C-06's contribution to A-43/A-45. No database implementation
  acceptance or live stack behavior is claimed.
- Review used a clean detached checkout at the exact handoff. Negative probes
  used an archive of the exact product and isolated alternate Git indexes.
  Product/state and unrelated paths were not edited. Only this receipt is committed.

## Candidate contract validation verified

The correction reads `git write-tree`, requires root bundle, all five standalone
OpenAPI contracts and `api/vacuum.yaml` as regular candidate files, and archives
those exact paths. Pinned Vacuum then reads the extracted candidate document and
candidate policy together. No working contract or ruleset is copied over it.
Missing/nonregular inputs fail explicitly. Remote lookup and update checks remain
disabled; Go resolution remains offline and `GOWORK=off`. Warning/error threshold
is still `--fail-severity=warn`. Temporary cleanup is limited to its owned directory.

Independent regressions used synthetic alternate indexes with unchanged baseline
working files and actual versioned pre-commit execution:

- For each of qBittorrent, Sonarr, Radarr, Jellyfin and Seerr, add
  `unexpectedTopLevelField: true` only to its staged contract. Each actual hook
  exits 1 with one `oas3-schema` error. Working files remain byte-identical to
  baseline. The previous staged-only Seerr bypass is therefore closed, including
  the other four generated OpenAPI surfaces.
- Add a valid synthetic warning rule only to staged `api/vacuum.yaml`, matching
  no legitimate root title. Actual hook exits 1 with zero errors and one warning.
  Working policy remains byte-identical to baseline. This proves staged policy
  binding and fail-on-warning behavior, rather than merely checking a flag.
- Root working-tree bundle/API validation remains an additional check before
  candidate validation. Candidate generation still executes the exact staged
  generation script from its snapshot; committed generated output remains compared.

R1 is also retained: independent YAML assertions prove exactly eighteen unique
cross-build pairs, equal to the nine module directories times amd64/arm64. The
ordinary Go CI matrix contains the exact nine modules. `actionlint` and YAML parse
pass; semantic pair coverage is asserted separately from syntactic validity.

## Matrix and architecture retained

- Root, UI, tools and all six clients remain independently listed for lint,
  tests/race/vet/module verification, generation and Linux CGO-free builds.
  Module commands use `GOWORK=off`; no workspace or replace directive is introduced.
  Existing fail-closed isolation checks remain unchanged.
- Candidate Vacuum covers root plus all five standalone OpenAPI contracts.
  NZBGet retains format-appropriate OpenRPC generator validation and reproducibility;
  it is not fed to Vacuum.
- API workflow and full aggregate invoke the candidate validator through fast
  guardrails; the versioned hook invokes that same fast path. All client import
  boundaries remain checked. No root/client DTO coupling or runtime capability
  changes are introduced by this scripts-only correction.
- qBittorrent's legacy local checker still uses documented normal pinned command
  resolution. Its forced-offline deprecation lookup limitation remains separate;
  authoritative root generation is offline once tools are warm. No authoritative
  check is weakened to conceal bootstrap failure.

## Independent checks

- Offline root `scripts/generate.sh --check`: exit 0; all committed outputs match.
- qBittorrent local checker with normal pinned resolution: exit 0.
- NZBGet/Sonarr/Radarr/Jellyfin/Seerr local checkers with
  `GOWORK=off GOPROXY=off GOSUMDB=off`: exit 0 each.
- All five staged-only invalid client contract actual-hook regressions: exit 1
  each as required; schema errors are observed and working bytes remain unchanged.
- Staged-only warning ruleset actual-hook regression: exit 1 as required;
  exactly zero errors and one warning; working policy remains unchanged.
- Independently rerun UI, tools and all six clients: tests/race/vet/module
  verification and both Linux CGO-free builds exit 0 for each.
- Independent root vet, module verification and Linux amd64/arm64 CGO-free builds:
  exit 0. Ordinary root tests passed in the aggregate, including storage 13.747s.
- All nine pinned lint runs, architecture and baseline root/candidate Vacuum passed
  before the aggregate root race phase, with zero issues/warnings/errors.
- `actionlint`, YAML parse/exact eighteen-pair/nine-directory assertions, shell
  syntax, `python3 scripts/check_planning.py`, `python3 scripts/check-architecture.py`
  and `git diff --check`: exit 0; planning has 53 tasks, 60 acceptance cases and links.

## R3 bounded full gate remains open

The independent clean-checkout full `scripts/check-guardrails.sh --ci` run used
a Python subprocess wrapper with a 180-second timeout and its own process group.
It exited 124 at the bound. Root race tests reported other package completions,
but root storage's test process stayed active with no package completion.
The wrapper terminated only its owned process group; no owned check remains.
Ordinary root storage tests passed in 13.747 seconds. Full root race/aggregate
success is not claimed, and the producer's 41.5-second successful-run claim was
not reproduced independently.

This is a remaining verification blocker, not a diagnosed new scripts or storage
code defect. The repeated fresh-checkout timeout needs separately owned root
storage investigation and a successful bounded full run. Independent nested
module checks and root vet/builds provide the completed matrix evidence above;
they do not replace the full gate. The actual receipt commit's fast hook passes,
but does not execute the blocked full root race suite.

## Separate gates

C-06 establishes repository matrix checks only. Nested-module release/bootstrap,
root adapter migration, native compatibility fixtures, G-01 write capability,
publication, deployment and verified live behavior remain separate gates.
Only this receipt is committed; exact SHA is reported after commit.
