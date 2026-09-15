# C-06 correction round two: bind contract validation to the staged tree

## Assignment

- Task ID and title: C-06 correction round two, close staged-only OpenAPI
  validation and rerun the bounded aggregate gate.
- Owner and independent reviewer: `/root/x05_implementer`; independent
  reviewer `/root/x05_reviewer`.
- Base commit: `2f9adbf5be1104834b47e77b3f1cc9881c2f1bd4`.
- Prior independent review receipt:
  `3ef41d3a567692f67fb70d18d290003e44e34777`.
- Branch/worktree: shared checkout on `main`; the coordinator owns integration
  and `docs/execution/state.json`.
- Owned paths: `scripts/generate.sh`, `scripts/check-guardrails.sh`,
  `scripts/check-lint.sh`, `.github/workflows/checks.yml` and this handoff.
  This correction changed only `scripts/check-guardrails.sh`; the other
  matrix and generation paths remain preserved from the preceding correction.
- Acceptance contributions: A-43 and A-45.

## Finding and correction

The prior review reproduced R2a: the aggregate loop ran Vacuum against the
working-tree copy of each standalone contract. A staged-only invalid
`clients/seerr/openapi.yaml` could therefore pass the versioned pre-commit hook
when the working copy remained valid.

`check_client_api_contracts` now binds validation to `git write-tree`. It
requires the staged `api/vacuum.yaml`, root `api/openapi.yaml`, and all five
standalone OpenAPI documents to exist as regular files in that tree, archives
those exact entries, and runs the pinned Vacuum tool against the extracted
candidate files using the extracted candidate ruleset. The existing
working-tree API check remains an additional check. Vacuum keeps
`--remote=false`, `--no-update-check`, `--fail-severity=warn`, and offline
`GOWORK=off GOPROXY=off GOSUMDB=off` resolution, so warnings and errors fail
closed without using network data.

The staged candidate loop is reached by the versioned pre-commit hook through
`--fast`, by the API workflow, and by the full `--ci` aggregate. The explicit
18-entry Linux cross-build matrix remains unchanged: root, UI, tools,
qBittorrent, NZBGet, Sonarr, Radarr, Jellyfin and Seerr for both amd64 and
arm64. NZBGet remains covered by its OpenRPC generator and module checks rather
than Vacuum.

## Verification

All inputs were synthetic repository data. No live service, credential,
private coordinate, inventory or media data was used. Commands were run from
the repository root unless a module directory is stated.

| Command or scenario | Result / exit status | Evidence |
| --- | --- | --- |
| Versioned pre-commit for product commit | Passed, exit 0; staged generation, root API check, staged six-document Vacuum, architecture, formatting and targeted tests passed | `.githooks/pre-commit` |
| Staged-only invalid standalone contract probe | Passed negative regression: with `unexpectedTopLevelField: true` only in an alternate index and the working Seerr file valid, the hook exited 1 with one `oas3-schema` error; the working file and real index stayed unchanged | `scripts/check-guardrails.sh` staged archive path |
| `GOWORK=off GOPROXY=off GOSUMDB=off ./scripts/generate.sh --check` | Passed, exit 0; generated outputs and exact staged generation matched | `scripts/generate.sh` |
| qBittorrent generation checker | Passed, exit 0 with normal pinned resolution | `clients/qbittorrent/check-generation.sh` |
| NZBGet, Sonarr, Radarr, Jellyfin and Seerr generation checkers with `GOPROXY=off GOSUMDB=off` | Passed, exit 0 for all five modules | each `clients/*/check-generation.sh` |
| `./scripts/check-guardrails.sh --fast` | Passed, exit 0; staged candidate validation covered root plus qBittorrent, Sonarr, Radarr, Jellyfin and Seerr OpenAPI documents | `scripts/check-guardrails.sh` |
| Bounded full `GOWORK=off ./scripts/check-guardrails.sh --ci` | Passed, exit 0 in 41.5 seconds under a 180-second Python process-group timeout; the prior root storage race timeout did not recur | `scripts/check-guardrails.sh` |
| Aggregate module tests and race tests | Passed for root, UI, tools and all six client modules | `scripts/check-guardrails.sh` |
| Aggregate `go vet` and `go mod verify` | Passed for all nine modules with `GOWORK=off` | `scripts/check-guardrails.sh` |
| Aggregate Linux CGO-free cross-builds | Passed for all 18 module/architecture pairs | `.github/workflows/checks.yml` and `scripts/check-guardrails.sh` |
| `./scripts/check-lint.sh` | Passed, exit 0; all pinned lint and architecture checks reported zero issues | `scripts/check-lint.sh` |
| `actionlint .github/workflows/checks.yml` and exact YAML matrix assertion | Passed, exit 0; 18 unique pairs, nine modules and both architectures | `.github/workflows/checks.yml` |
| `python3 scripts/check_planning.py` | Passed; 53 tasks, 60 acceptance cases and local links resolve | planning checker |
| `python3 scripts/check-architecture.py` | Passed; root and standalone-client import boundaries hold | architecture checker |
| `git diff --check` and `git diff --cached --check` | Passed, exit 0; product tree clean after commit | Git |

## Product and integration

- Product commit: `5e53134e40322b512efef1865d78da2da6368e5b` (`ci: bind
  OpenAPI validation to staged tree`).
- Handoff commit: this docs-only commit; the coordinator records its exact SHA
  in `docs/execution/state.json` after commit.
- No changes were made to `docs/execution/state.json`, client modules,
  generated output, local workspace/replacement directives, credentials or
  live-service configuration.
- Existing qBittorrent cold-cache limitation remains: its legacy local
  checker needs normal pinned command resolution before an offline run can be
  performed. The authoritative repository generation and staged contract
  validation are offline once the tools cache is warm.
- G-01 native write capability and live upstream compatibility remain outside
  this repository-matrix lane; this correction enables no upstream writes.

## Resume checkpoint

- Product checkpoint: `5e53134e40322b512efef1865d78da2da6368e5b`.
- Handoff path: `docs/execution/handoffs/C-06-correction-round2.md`.
- Current state: staged candidate OpenAPI bytes and the staged Vacuum policy are
  validated together by hook, API fast checks and the full aggregate; the
  explicit 18-pair matrix and all prior generation/lint/module guardrails are
  preserved.
- Next safe action: coordinator records this handoff SHA in state and requests
  independent review against the exact product tree.
