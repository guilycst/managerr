# X-07 independent review, round three

## Decision

`approved`.

The remaining relocation safety finding is closed. `Relocate` now rejects a
requested final content path whose basename cannot be produced by
qBittorrent's parent-directory `setLocation` operation. Single-file and
multi-file changed-basename cases return unsupported before destination
vacancy or native mutation. Representable parent-directory moves retain
vacancy checks, containing-directory dispatch and exact full-payload read-back.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of correction author
  `/root/x05_implementer`.
- Reviewed product:
  `7b74043b3d4a8e76993dd6ff511e3982cfa5d369`; direct parent
  `f70a7bdb7652015878471bb650a811111d02e526`; product tree
  `dd76895941a1a1b5ecd77b25f31842fe416c8b51`.
- Reviewed handoff:
  `be48c353fcd5710de7f223a1525b29060010ea92`; direct parent is the product
  commit; handoff tree `f7c98bc543614df15428f877cad58018f649e0c6`.
- Prior correction product:
  `ac8d0a3b61d5ad6b3bb16e67d5bd1d25359859df`; prior review receipt:
  `9c7e35dc3e8accf25b0751a69aa85b5f4ae0cab4`.
- Product commit changes only
  `internal/adapters/qbittorrent/control/control.go` and
  `internal/adapters/qbittorrent/control/control_test.go`.
- Scoped correction diff SHA-256:
  `f63359c579e7855b889ff34428562e97cfcbaa6136efbd7f1116c283bfd8f48c`.
- Acceptance reviewed: A-28, A-29, A-30, A-31 and A-33.
- Review receipt checkpoint: commit containing this file; exact SHA is reported
  to coordinator after commit because a Git commit cannot embed its own SHA.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-x07-review-r3` at exact
handoff commit. Reviewer did not edit product paths, execution state, task
definitions, client modules, shared scripts or fixtures.

## Finding closure

`relocationRepresentable` normalizes current and desired content paths and
requires equal basenames. `Relocate` invokes this check immediately after its
already-materialized read and before stopped-state, capability, vacancy or
`SetLocation` processing. A changed final basename therefore requires a
separate reviewed rename action and cannot cause a partial relocation.

Synthetic cases cover:

- a single file changing `Synthetic Film.mkv` to `Renamed Film.mkv`;
- a multi-file root changing `Synthetic Pack` to `Renamed Pack`;
- zero vacancy checks and zero `SetLocation` calls for both rejections;
- a representable single-file parent-directory move;
- a representable multi-file root move with every video/subtitle payload path;
- an occupied final target rejected after one vacancy check and before write;
- a changed payload read-back returning unknown after one dispatch.

Direct code inspection confirms these assertions bind independent call
counters and final paths. The producer's tests do not infer success from HTTP
acceptance or from only the torrent root path.

## Preserved controls

- Stop, relocation, file rename, folder rename and metadata-only removal keep
  independent versioned capability gates.
- Relocation requires a stable stopped whole-torrent snapshot, mapped final
  target and explicit destination-vacancy evidence.
- Successful relocation reads back exact final content path and every payload
  path. Missing or altered payload evidence remains unknown.
- `stalledUP` with zero upload speed remains active. Lost responses use one
  detached bounded read-back and never trigger blind mutation retry.
- Removal requires stopped state, sends literal `deleteFiles=false`, treats an
  absent torrent record as already satisfied and never claims payload removal.
- File and folder renames retain exact observed-scope and collision checks.
- Generated qBittorrent DTOs do not leak into Mastarr domain or ports.
- Nested qBittorrent client remains read-only. No runtime write transport or
  Arr mutation was enabled by this correction.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees and product-owned scope | Passed. |
| Complete 59-line correction diff and relevant surrounding control paths | Inspected independently. |
| Focused changed-basename, representable single/multi move, vacancy and partial-read-back tests | Passed. |
| `GOWORK=off go test -mod=readonly -count=50 ./internal/adapters/qbittorrent/control` | Passed. |
| `GOWORK=off go test -race -mod=readonly -count=10 ./internal/adapters/qbittorrent/control` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/adapters/qbittorrent/control` | Passed. |
| Focused pinned `golangci-lint` for control package | Passed; 0 issues. |
| `GOWORK=off go test -mod=readonly ./...` in root | Passed. |
| Root `GOWORK=off go mod verify` | Passed. |
| Nested qBittorrent module verify, test, race test and vet with `GOWORK=off` | Passed. |
| Linux amd64 and arm64 control-package test compilation | Passed. |
| `./scripts/generate.sh --check` | Passed; committed generated output reproducible. |
| `./scripts/check-api.sh` | Passed; bundled OpenAPI and Vacuum quality 100/100. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py --self-test` | Passed; 43 tasks and 60 acceptance cases. |
| `gofmt -l` on owned Go files and `git diff --check` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed; root, UI, tools and both nested clients, lint, architecture, generation, API, vet and module verification. |
| Live qBittorrent, credentials, private coordinates and mounted media | Intentionally not used. |

## Acceptance disposition

- A-28: accepted for X-07 adapter contribution. Explicit state and post-write
  reconciliation remain intact.
- A-29: accepted. Whole-torrent Stop and metadata-only removal behavior remain
  exact and independently gated.
- A-30: accepted. Missing torrent record stays distinct from payload state;
  no automatic re-add or resume exists.
- A-31: accepted. Unrepresentable basename changes, collisions and scope
  expansion are rejected before native mutation; representable relocation and
  rename require exact payload-path read-back.
- A-33: accepted. Dispatched writes receive bounded read-back, unresolved
  effects stay unknown and no method blindly resubmits.

G-01 remains open. Runtime qBittorrent writes remain blocked until the nested
client gains its own versioned contract, generated transport and independent
review. This receipt does not approve Arr writes, release, deployment or live
media mutation.
