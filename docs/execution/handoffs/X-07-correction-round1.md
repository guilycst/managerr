# X-07 qBittorrent control correction, round one

## Assignment and review input

- Task: X-07, qBittorrent control adapter.
- Correction owner: `/root/x05_implementer`.
- Independent reviewer: `/root/x05_reviewer`.
- Correction source product: `a51e51945bde229265163707261483fa7edce06a`.
- Review addressed: `2c26b06fe083372432889901f84d98246ab761be`, round-one
  decision `changes_requested`.
- Shared checkout: `main`; coordinator owns integration and
  `docs/execution/state.json`.
- Owned product paths: `internal/adapters/qbittorrent/control/`.
- Owned handoff path: this file. No state, script, module, shared fixture or
  D-03 configuration path was changed.
- Acceptance contributions: A-28, A-29, A-30, A-31 and A-33.

## Correction result

Product commit:

`ac8d0a3b61d5ad6b3bb16e67d5bd1d25359859df`
(`fix(qbittorrent): harden relocation and capability gates`)

The correction closes both review blockers:

- `domain.FileTarget` is treated as the final content path. Relocate derives
  the containing directory for qBittorrent `setLocation`, separately models
  single-file and multi-file content paths, and requires a read-back proving
  the final content path plus every expected payload path. Synthetic fixtures
  intentionally keep native `location` and `content_path` distinct.
- Relocate requires an adapter-owned `DestinationChecker` precondition for the
  final destination target. An occupied target returns conflict before native
  dispatch; an unavailable checker keeps relocation unsupported and performs
  no write. The root integration must supply this through a reviewed,
  root-confined filesystem read implementation.
- Stop, relocation, file rename, folder rename and metadata-only removal each
  have independent capability state, version and evidence. Supporting Stop
  alone cannot authorize any other operation. Capabilities reports five
  operation observations rather than one aggregate write flag.
- Existing safety behavior remains intact: exact torrent scope is re-read
  before dispatch; every dispatched write gets one detached bounded read-back;
  lost responses remain `outcome_unknown` without blind retry; removal always
  sends `deleteFiles=false`, never re-adds or resumes, and requires stopped
  state.

Focused synthetic coverage includes single-file and multi-file relocation,
containing-directory dispatch, destination collision, missing vacancy evidence,
partial payload read-back, and independent Stop-only capability gating. No
native qBittorrent write transport was added: `clients/qbittorrent` remains a
read-only nested module, so runtime mutation wiring stays blocked pending its
separate contract and generated transport lane.

## Verification

Commands below ran in the shared checkout against the product correction and
returned exit status 0 unless stated otherwise.

| Command or scenario | Result / evidence |
| --- | --- |
| `GOWORK=off go test -mod=readonly -count=50 ./internal/adapters/qbittorrent/control` | Passed; relocation, collision, scope, idempotency, lost-response and capability tests |
| `GOWORK=off go test -race -mod=readonly -count=10 ./internal/adapters/qbittorrent/control` | Passed |
| `GOWORK=off go vet -mod=readonly ./internal/adapters/qbittorrent/control` | Passed |
| `GOWORK=off go tool -modfile=tools/go.mod github.com/golangci/golangci-lint/v2/cmd/golangci-lint run --config .golangci.yml --timeout=5m ./internal/adapters/qbittorrent/control` | Passed; 0 issues |
| `cd clients/qbittorrent && GOWORK=off go test -mod=readonly -count=1 ./...` | Passed |
| `cd clients/qbittorrent && GOWORK=off go test -race -mod=readonly -count=1 ./...` | Passed |
| `cd clients/qbittorrent && GOWORK=off go vet -mod=readonly ./...` | Passed |
| `cd clients/qbittorrent && GOWORK=off go mod verify` | Passed; all modules verified |
| `GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=readonly -c -o /tmp/mastarr-control-linux-amd64-r1.test ./internal/adapters/qbittorrent/control` | Passed |
| `GOOS=linux GOARCH=arm64 GOWORK=off go test -mod=readonly -c -o /tmp/mastarr-control-linux-arm64-r1.test ./internal/adapters/qbittorrent/control` | Passed |
| `./scripts/generate.sh --check` | Passed; generated output reproducible |
| `./scripts/check-api.sh` | Passed; bundled OpenAPI and Vacuum quality 100/100 |
| `python3 scripts/check-architecture.py` | Passed; import boundaries intact |
| `python3 scripts/check_planning.py --self-test` | Passed; 43 tasks, 60 acceptance cases |
| `./scripts/check-guardrails.sh --fast` | Passed |
| `./scripts/check-guardrails.sh --ci` | Blocked at root module `go list` with `go: updates to go.mod needed` from concurrent D-03 configuration dependencies; no X-07 files or module manifests were changed |
| `git diff --check` and `gofmt -l` for owned Go files | Passed |
| Live qBittorrent, credentials, private coordinates, mounted media and native mutations | Intentionally not run; all evidence is synthetic and injected |

## Review and integration

- Review receipt addressed: `2c26b06fe083372432889901f84d98246ab761be`.
- Independent review of the exact product commit is pending
  `/root/x05_reviewer`.
- Coordinator must record product and this handoff's exact SHAs, checks and
  blocker in `docs/execution/state.json`; this worker does not edit that file.
- G-01 remains open outside this lane. No Arr write enablement or runtime
  qBittorrent write enablement is implied by protocol-model fixtures.

## Resume checkpoint

- Product checkpoint: `ac8d0a3b61d5ad6b3bb16e67d5bd1d25359859df`.
- Handoff checkpoint: pending this documentation commit.
- Next safe action: independently review the exact product checkpoint, then
  coordinator records the receipts and integrates after approval.
- Runtime blocker: the nested qBittorrent client still exposes read methods
  only. Add stop, setLocation, rename and delete routes through its own
  versioned OpenAPI/generated transport lane before wiring any runtime writer;
  do not hand-roll those routes in this root adapter.
