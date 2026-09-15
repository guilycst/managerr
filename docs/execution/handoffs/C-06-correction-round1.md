# C-06 correction round one: explicit cross-build and contract validation

## Assignment

- Task ID and title: C-06 correction round one, close the cross-build and
  standalone-contract validation findings.
- Owner/agent: /root/x05_implementer. Independent reviewer: /root/x05_reviewer.
- Base commit: 7191dd6265c0df38d4cb25df28261762b8cbc1d1.
- Branch/worktree or shared-checkout ownership: shared checkout on main;
  coordinator owns docs/execution/state.json and integration.
- Owned paths: scripts/generate.sh, scripts/check-guardrails.sh,
  scripts/check-lint.sh, .github/workflows/checks.yml and this handoff.
  This correction changed only scripts/check-guardrails.sh and
  .github/workflows/checks.yml; scripts/generate.sh and scripts/check-lint.sh
  remain part of the owned matrix surface.
- Prior independent review receipt:
  f7ef97b0685522d802b8ffba7a3114f1909109b1.
- Linked acceptance: A-43 and A-45.

## Findings and correction

- R1 P2, cross-build coverage: the GitHub cross-build job no longer relies on
  matrix include merge behavior. It explicitly lists 18 entries: root, ui,
  tools, qBittorrent, NZBGet, Sonarr, Radarr, Jellyfin and Seerr, each once
  for amd64 and once for arm64. A local YAML/count check proves 18 unique
  module/architecture pairs, with nine modules and both requested arches.
- R2 P2, contract validation: aggregate fast and CI guardrails now run the
  pinned Vacuum binary through tools/go.mod against api/openapi.yaml through
  the existing API check and against every standalone OpenAPI contract:
  qBittorrent, Sonarr, Radarr, Jellyfin and Seerr. Vacuum uses
  --fail-severity=warn, --remote=false, --no-update-check and offline Go
  module resolution, so warnings and errors fail closed. NZBGet's OpenRPC
  contract remains covered by its deterministic generator and module matrix.
- R3, race-gate evidence: the full aggregate command was rerun after the
  prior storage-race stall using a Python subprocess timeout of 180 seconds.
  It completed in approximately 39 seconds with exit 0, including the root,
  UI, tools and all six client modules' tests, race tests, vet, module
  verification and Linux amd64/arm64 CGO-free builds.
- Existing C-06 protections remain: GOWORK=off is used throughout; generator
  commands use GOPROXY=off and GOSUMDB=off after the tools cache is warmed;
  local go.work/go.work.sum and replace directives are rejected; standalone
  client architecture checks and local reproducibility checkers remain in
  place. The pre-existing qBittorrent checker still needs normal pinned
  command resolution on a cold cache, while root generation is offline once
  tools are warmed.
- No client source, upstream contract, generated output, state file, local
  replacement or workspace file was added. No credentials, private
  coordinates, inventories or media data were used.

## Verification

All fixtures and contract checks were synthetic or static repository inputs.
The product commit is 650cdfa51ec161b260097652246a59753429b2ab.

| Command or scenario | Result / exit status | Evidence path |
| --- | --- | --- |
| GOWORK=off GOPROXY=off GOSUMDB=off ./scripts/generate.sh --check | Passed; root, UI, qBittorrent, NZBGet, Sonarr, Radarr, Jellyfin and Seerr generated outputs matched | scripts/generate.sh |
| Pinned Vacuum from tools/go.mod over api/openapi.yaml and five standalone OpenAPI contracts | Passed; every document quality 100/100 with zero warnings/errors | scripts/check-api.sh and scripts/check-guardrails.sh |
| ./scripts/check-guardrails.sh --fast | Passed after staged product changes; generation, six-contract Vacuum, architecture, format and targeted tests passed | scripts/check-guardrails.sh |
| Python YAML parse and actionlint .github/workflows/checks.yml | Passed; workflow syntax valid | .github/workflows/checks.yml |
| Explicit cross-build matrix count | Passed; 18 unique module/architecture entries, nine modules x amd64/arm64 | .github/workflows/checks.yml |
| python3 scripts/check-architecture.py | Passed; root import boundaries | scripts/check-architecture.py |
| python3 scripts/check_planning.py | Passed; 53 tasks, 60 acceptance cases and links valid | scripts/check_planning.py |
| python3 bounded subprocess of ./scripts/check-guardrails.sh --ci with timeout=180 | Passed within 180 seconds, exit 0; full aggregate gate completed after prior storage-race stall | scripts/check-guardrails.sh |
| Aggregate module test and race matrix | Passed for root, ui, tools, qBittorrent, NZBGet, Sonarr, Radarr, Jellyfin and Seerr; no race detector failure | scripts/check-guardrails.sh |
| Aggregate vet and GOWORK=off go mod verify matrix | Passed for all nine modules | scripts/check-guardrails.sh |
| Aggregate Linux amd64/arm64 CGO-free build matrix | Passed for all nine modules and both architectures | scripts/check-guardrails.sh |
| Full lint and client architecture matrix | Passed; pinned golangci-lint reported 0 issues and standalone client boundaries passed | scripts/check-lint.sh |
| Versioned pre-commit hook and git diff --check | Passed; staged generation/API, six-contract Vacuum, architecture, format and targeted checks passed | .githooks/pre-commit |

The workflow generation job also runs the qBittorrent local checker after the
tools module download, then runs NZBGet, Sonarr, Radarr, Jellyfin and Seerr
local checkers with GOPROXY=off and GOSUMDB=off. The authoritative root
generation check is offline for all generated surfaces.

## Review and integration

- Product commit: 650cdfa51ec161b260097652246a59753429b2ab,
  message ci: validate all client contracts and cross-builds.
- Independent review is pending against the exact product commit above.
- Coordinator must record product and handoff SHAs in state.json; this lane
  did not edit state.json or the prior review receipt.
- C-06 remains a repository matrix/bootstrap contribution. It does not
  publish nested modules, add root adapters, freeze native runtime versions,
  resolve G-01 Arr write safety, or claim live CI/deployment behavior.

## Resume checkpoint

- Current product checkpoint: 650cdfa51ec161b260097652246a59753429b2ab.
- Handoff commit: pending until this file is committed separately.
- Current state: explicit 18-entry cross-build coverage and pinned Vacuum
  checks cover every standalone OpenAPI contract; all nine modules are tested,
  raced, vetted, verified and cross-built by the aggregate guardrail.
- Active process or agent ownership: no active process; coordinator owns
  state/integration and /root/x05_reviewer owns independent re-review.
- Next safe action: commit this handoff separately, record both exact SHAs in
  state.json and dispatch independent review against the final product tree.
- Remaining limitation: a cold checkout needs the documented module download
  bootstrap before qBittorrent's legacy versioned local checker can resolve;
  root generation remains offline once tools are warmed.
