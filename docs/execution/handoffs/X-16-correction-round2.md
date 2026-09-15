# X-16 correction round two: scoped Radarr preview identity

## Assignment

- Task ID and title: X-16 correction round two, enforce the requested movie
  identity for Radarr folder previews.
- Owner: `/root/x05_implementer`.
- Independent reviewer: `/root/x05_reviewer`.
- Correction base checkpoint: `6276071456f73d7e43928efd19fd291d738f64df`.
- Prior product: `30aa6734fa7705d61e3d93c9b7f93c8173c4b9b3`.
- Prior handoff: `345fbc83bbf85d7f6c3f04d93d7e9bf4ae8a31e5`.
- Prior review receipt: `41ae0c2b01b8712029dbb72024a72c81d1d990e2`.
- Branch/worktree: shared checkout on `main`; coordinator owns execution state.
- Owned product path: `clients/radarr/`.
- Owned handoff path: `docs/execution/handoffs/X-16-correction-round2.md`.
- Acceptance contributions: A-07, A-09, A-10 and A-45.

## Finding and correction

The prior path-bound correction added an optional `movieId` to the ordinary
`PreviewManualImport` entry point and sent that filter to Radarr, but it passed
`expectedMovieID == 0` to the shared response normalizer. Consequently, a
folder request scoped to movie 101 could accept a candidate associated with a
foreign movie or with no movie association at all. The independent review
reproduced both cases through a synthetic HTTP fixture and marked the scope
validation as R2 P1.

Product commit: `1ef0c07af604e7e63d88bc68955c8097479f7e82`
(`fix(radarr): enforce scoped preview identity`).

The correction threads the caller's requested identity through every relevant
folder-preview path:

- `PreviewManualImport` still permits an intentionally unscoped folder preview
  when `MovieID` is nil. It validates an optional movie ID as positive, keeps
  that identity as `expectedMovieID`, and passes it to the common normalizer.
  Zero is therefore reserved for a genuinely unscoped folder request.
- A scoped folder response must contain a nested movie association whose ID is
  exactly the requested ID. A foreign association or a missing association
  returns the sanitized `ErrorMalformed` result before the response can be
  reported as complete.
- `PreviewImport`, the concise alias, inherits the same forwarding and scope
  checks. `PreviewLibraryImport` and `PreviewMovieImport` already pass their
  required movie ID and remain path-bound compatibility entry points.
- The contract and README semantics from correction round one remain intact:
  `folder` is required by the OpenAPI 3.1.1 contract, movie ID alone is
  rejected locally, and no fictional registered-library scan is represented.

The implementation remains read-only and module-isolated. Generated
oapi-codegen output stays under `clients/radarr/internal/generated`; no raw
generated DTO or client is exposed through the public package. There are no
registration, import, search, command or other write methods. Strict response
decoding, bounded arrays/bodies, required nested identity fields, complete
array coverage, API-key transport, deadlines, redirect refusal and sanitized
upstream errors remain unchanged.

## Evidence

The new synthetic `httptest` coverage in `clients/radarr/client_test.go` binds
the finding to executable checks:

- `TestManualImportFolderPreviewRejectsForeignOrMissingScopedMovie` sends a
  folder plus `movieId=101` and proves both a candidate associated with movie
  999 and a candidate without `movie` fail with `ErrorMalformed`.
- `TestPreviewImportForwardsMovieScope` proves the concise alias sends the
  requested folder and movie ID and accepts a candidate associated with movie
  101.
- Existing `TestManualImportLibraryPreviewRejectsForeignMovieAndMissingReference`
  covers the compatibility-named path-bound movie entry point, while
  `TestManualImportLibraryPreviewUsesNativeMovieScope` covers its valid
  response and exact query.
- Existing unscoped folder-preview coverage continues to exercise the
  intentional `expectedMovieID == 0` behavior.

Fixtures are synthetic only. No live Radarr service, credential, private
endpoint, inventory, media file or write was used.

## Verification

All checks below passed against product commit
`1ef0c07af604e7e63d88bc68955c8097479f7e82` unless a command is identified as
repository-wide.

| Command or scenario | Result |
| --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./...` from `clients/radarr` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=3 -timeout=180s ./...` from `clients/radarr` | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` from `clients/radarr` | Passed. |
| `GOWORK=off go mod verify` from `clients/radarr` | Passed: `all modules verified`. |
| `GOWORK=off GOPROXY=off GOSUMDB=off go generate ./...` from `clients/radarr` | Passed; generated output unchanged. |
| `GOWORK=off GOPROXY=off GOSUMDB=off ./check-generation.sh` from `clients/radarr` | Passed: `Radarr generation checks passed`. |
| Pinned Radarr golangci-lint via the tools module with `GOWORK=off` | Passed: `0 issues.` |
| Pinned Radarr Vacuum with `--remote=false --fail-severity=warn` and the repository ruleset | Passed; quality 100/100 with zero warnings/errors. |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/radarr` | Passed. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/radarr` | Passed. |
| `python3 scripts/check-architecture.py` | Passed: architecture import boundaries. |
| `python3 scripts/check_planning.py` | Passed: 53 tasks, 60 acceptance cases; local links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed: generation, aggregate API/Vacuum, architecture, lint, root/UI/tools/qBittorrent/NZBGet tests, vet and module verification. |
| `git diff --check` and product pre-commit hook | Passed. |

## Integration and resume checkpoint

The coordinator-owned aggregate scripts and CI module matrix currently do not
include the unpublished `clients/radarr` module. Direct Radarr checks above are
the evidence for this lane. C-06/bootstrap must add Radarr to generation,
lint, test, vet, verification and cross-build matrices and publish a
consumable module before X-19/X-20 adapter migration. This correction did not
edit shared scripts, root dependencies, `go.work`, permanent `replace`
directives, state, adapters or other client modules.

G-01 remains open. Arr registration/import writes remain outside this
read-only client and no native write capability was enabled.

- Product commit: `1ef0c07af604e7e63d88bc68955c8097479f7e82`.
- Correction handoff commit: pending; this file is intentionally committed
  separately.
- Prior review receipt: `41ae0c2b01b8712029dbb72024a72c81d1d990e2`.
- Independent review receipt: pending.
- Coordinator state update: pending; `docs/execution/state.json` was not
  changed by this lane.
- Safe next action: commit this handoff, then assign independent review against
  the exact product tree.
