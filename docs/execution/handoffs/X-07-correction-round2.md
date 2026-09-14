# X-07 qBittorrent control correction, round two

## Assignment and review input

- Task: X-07, qBittorrent control adapter.
- Correction owner: `/root/x05_implementer`.
- Independent reviewer: `/root/x05_reviewer`.
- Correction source product: `ac8d0a3b61d5ad6b3bb16e67d5bd1d25359859df`.
- Review addressed: `9c7e35dc3e8accf25b0751a69aa85b5f4ae0cab4`, round-two
  decision `changes_requested`.
- Shared checkout: `main`; coordinator owns integration and
  `docs/execution/state.json`.
- Owned product paths: `internal/adapters/qbittorrent/control/`.
- Owned handoff path: this file. No state, client module, script, shared
  fixture or unrelated lane path was changed.
- Acceptance contributions: A-28, A-29, A-30, A-31 and A-33.

## Correction result

Product commit:

`7b74043b3d4a8e76993dd6ff511e3982cfa5d369`
(`fix(qbittorrent): reject unrepresentable relocation targets`)

The remaining relocation safety blocker is closed. Before vacancy evidence or
native dispatch, `Relocate` now proves that the approved final content path
has the same basename as the observed qBittorrent content path. qBittorrent's
parent-directory `setLocation` preserves that content name; a changed
basename requires a separate reviewed rename action and returns unsupported
with zero native calls. Synthetic tests cover both a single-file content path
and a multi-file content root, and assert that neither `SetLocation` nor the
vacancy precondition runs for either rejection.

Representable single-file and multi-file relocation remains unchanged: the
adapter sends the containing directory, checks final-target vacancy, and
requires read-back of the final content path and every mapped payload path.
Independent Stop, relocation, file-rename, folder-rename and metadata-only
removal capability evidence remains in force. Lost responses remain unknown
without retry; removal still sends `deleteFiles=false` and never re-adds or
resumes.

No nested-client contract or runtime write transport was added. The
read-only `clients/qbittorrent` module remains a separate bootstrap blocker;
the control adapter continues to use only its injected normalized upstream
seam and synthetic fixtures.

## Verification

Commands below ran in the shared checkout against the product correction and
returned exit status 0 unless stated otherwise.

| Command or scenario | Result / evidence |
| --- | --- |
| `GOWORK=off go test -mod=readonly -count=50 ./internal/adapters/qbittorrent/control` | Passed; representability, single/multi relocation, vacancy, read-back, scope and capability tests |
| `GOWORK=off go test -race -mod=readonly -count=10 ./internal/adapters/qbittorrent/control` | Passed |
| `GOWORK=off go vet -mod=readonly ./internal/adapters/qbittorrent/control` | Passed |
| `GOWORK=off go tool -modfile=tools/go.mod github.com/golangci/golangci-lint/v2/cmd/golangci-lint run --config .golangci.yml --timeout=5m ./internal/adapters/qbittorrent/control` | Passed; 0 issues |
| `GOWORK=off go test -mod=readonly ./...` in root | Passed |
| `GOWORK=off go mod verify` in root | Passed; all modules verified |
| `cd clients/qbittorrent && GOWORK=off go test -mod=readonly -count=1 ./...` | Passed |
| `cd clients/qbittorrent && GOWORK=off go test -race -mod=readonly -count=1 ./...` | Passed |
| `cd clients/qbittorrent && GOWORK=off go vet -mod=readonly ./...` | Passed |
| `cd clients/qbittorrent && GOWORK=off go mod verify` | Passed; all modules verified |
| `GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=readonly -c -o /tmp/mastarr-control-linux-amd64-r2.test ./internal/adapters/qbittorrent/control` | Passed |
| `GOOS=linux GOARCH=arm64 GOWORK=off go test -mod=readonly -c -o /tmp/mastarr-control-linux-arm64-r2.test ./internal/adapters/qbittorrent/control` | Passed |
| `./scripts/generate.sh --check` | Passed; generated output reproducible |
| `./scripts/check-api.sh` | Passed; bundled OpenAPI and Vacuum quality 100/100 |
| `python3 scripts/check-architecture.py` | Passed; import boundaries intact |
| `python3 scripts/check_planning.py --self-test` | Passed; 43 tasks, 60 acceptance cases |
| `./scripts/check-guardrails.sh --fast` | Passed |
| `./scripts/check-guardrails.sh --ci` | Passed; root, UI, tools and both nested client modules, lint, architecture, generation, API, vet and module verification |
| `git diff --check` and `gofmt -l` for owned Go files | Passed |
| Live qBittorrent, credentials, private coordinates, mounted media and native mutations | Intentionally not run; all evidence is synthetic and injected |

## Review and integration

- Review receipt addressed: `9c7e35dc3e8accf25b0751a69aa85b5f4ae0cab4`.
- Independent review of the exact product commit is pending
  `/root/x05_reviewer`.
- Coordinator must record product and this handoff's exact SHAs, checks and
  blocker in `docs/execution/state.json`; this worker does not edit that file.
- G-01 remains open outside this lane. Protocol-model evidence does not enable
  Arr writes or qBittorrent runtime writes.

## Resume checkpoint

- Product checkpoint: `7b74043b3d4a8e76993dd6ff511e3982cfa5d369`.
- Handoff checkpoint: pending this documentation commit.
- Next safe action: independently review the exact product checkpoint, then
  coordinator records the receipts and integrates after approval.
- Runtime blocker: extend `clients/qbittorrent` through its own versioned
  OpenAPI/generated transport lane before wiring stop, setLocation, rename or
  delete; do not hand-roll upstream routes in the root adapter.
