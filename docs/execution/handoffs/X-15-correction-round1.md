# X-15 correction round one handoff

## Assignment

- Task ID: X-15 correction round one.
- Owner: `/root/x05_implementer`.
- Independent reviewer: coordinator-assigned after this handoff.
- Correction base checkpoint: `49d7bb8f000353f6f6909247ff8001e34f0f0524`.
- Prior product: `8a11bea1003b50d00d57318244a4bca8b138397b`.
- Prior review receipt: `907815db38e3b336fbc51e7d96f37a94d6110469`.
- Branch/worktree: shared checkout on `main`; coordinator owns execution state.
- Owned product paths: `clients/sonarr/`.
- Owned handoff path: `docs/execution/handoffs/X-15-correction-round1.md`.
- Acceptance contributions: A-07, A-09, A-10 and A-45.

## Product result

Product commit: `f258104718691807e9679e0c6eb278b144ea7a09`
(`fix(sonarr): enforce native preview scope and nested evidence`). The product
remains a standalone, read-only Sonarr v3 client. No root module, adapter,
state, task plan, UI, other client, write endpoint or local replace directive
was changed.

The correction closes the five findings in the independent review:

- `PreviewManualImport` is now the downloaded-folder mode. It rejects a
  supplied `SeriesID` before authentication or network dispatch, so a caller
  cannot accidentally ask Sonarr's library-series overload while intending a
  folder preview. `PreviewLibraryImport` is a separate request type that sends
  only the native `seriesId`, optional `seasonNumber` and filter values. Its
  returned series and every nested episode association must match the requested
  series. The OpenAPI contract and README describe both modes.
- Episode-file DTOs retain native `seriesId`. Direct episode-file reads and
  nested episode-file observations reject missing, zero or foreign series
  identities. Top-level episode observations also reject a response series ID
  that differs from the requested series.
- Native Sonarr 3.x singular `language` and rejection `reason` members are
  modeled and preserved in normalized observations. Older plural `languages`
  and `message` aliases remain supported; when both forms are present they
  must agree. Native rejection type and reason/message evidence is required.
- Required nested series, episode, episode-file, language and rejection
  members are checked from raw JSON before generated scalar decoding. Duplicate
  JSON members remain rejected by the recursive scanner, and malformed nested
  responses cannot be reported as complete coverage. Unknown upstream members
  stay isolated in generated DTO storage and never enter normalized public
  observations.
- Generation and reproducibility use the pinned oapi-codegen tool from the
  repository `tools` module. Both `go generate` and `check-generation.sh` run
  with `GOWORK=off GOPROXY=off GOSUMDB=off`, so a warmed cache is sufficient and
  no network or version lookup is performed.

Generated output remains committed under
`clients/sonarr/internal/generated/client.gen.go`, carries the pinned
oapi-codegen v2.8.0 header, and was regenerated from the OpenAPI 3.1.1 source.
The generated package remains behind the module's `internal` boundary. The
handwritten public client exposes only normalized Sonarr observations and no
registration, import, command, search or other write method.

## Verification

All checks below were run after product commit `f258104718691807e9679e0c6eb278b144ea7a09`
unless a command is explicitly identified as a repository check. Fixtures are
synthetic `httptest` responses only; no live Sonarr service, credential,
private endpoint, inventory, media file or write was used.

| Command or scenario | Result |
| --- | --- |
| `GOWORK=off go test -mod=readonly ./...` from `clients/sonarr` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=3 -timeout=180s ./...` from `clients/sonarr` | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` from `clients/sonarr` | Passed. |
| `GOWORK=off go mod verify` from `clients/sonarr` | Passed: `all modules verified`. |
| `GOWORK=off GOPROXY=off GOSUMDB=off go generate ./...` from `clients/sonarr` | Passed; generated output is reproducible. |
| `GOWORK=off GOPROXY=off GOSUMDB=off ./check-generation.sh` from `clients/sonarr` | Passed: `Sonarr generation checks passed`. |
| Pinned Sonarr Vacuum from `tools`: `GOWORK=off GOPROXY=off GOSUMDB=off go tool github.com/daveshanley/vacuum lint --no-update-check --remote=false --ruleset ../api/vacuum.yaml --fail-severity=warn --no-banner --no-style ../clients/sonarr/openapi.yaml` | Passed; quality 100/100 with zero warnings/errors. |
| Pinned Sonarr lint from `clients/sonarr`: `GOWORK=off go tool -modfile=../../tools/go.mod github.com/golangci/golangci-lint/v2/cmd/golangci-lint run --config ../../.golangci.yml --timeout=5m ./...` | Passed: `0 issues.` |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/sonarr` | Passed. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/sonarr` | Passed. |
| `GOWORK=off go test -mod=readonly ./...` from repository root | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` and `GOWORK=off go mod verify` from repository root | Passed. |
| `./scripts/generate.sh --check` | Passed: generation and staged generation checks. |
| `./scripts/check-api.sh` | Passed; aggregate Vacuum quality 100/100. |
| `./scripts/check-lint.sh` | Passed with zero issues; root, UI, tools, qBittorrent and NZBGet matrix plus architecture checks. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 53 tasks, 60 acceptance cases; local links resolve. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed; existing CI matrix, generation, API, architecture, lint, tests, vet and module verification. |
| `git diff --check` and commit pre-commit hook | Passed. |

## Integration and blocker

The coordinator-owned aggregate scripts and CI matrix currently discover the
published qBittorrent and NZBGet modules but do not yet include
`clients/sonarr`. Direct Sonarr module checks above are the evidence for this
correction. Before an Arr adapter migration, the coordinator must add Sonarr to
the root generation, lint, test, vet, verification, cross-build and CI matrices
and publish a consumable module version (or use the repository's approved
bootstrap procedure). This handoff does not add `go.work`, a permanent local
replace, or a root dependency.

Arr registration/import writes remain outside this read-only client and remain
blocked by the existing upstream evidence gate (G-01). No native write
capability was enabled here.

## Review and resume checkpoint

- Product commit: `f258104718691807e9679e0c6eb278b144ea7a09`.
- Handoff commit: pending; this file is intentionally committed separately.
- Prior review receipt: `907815db38e3b336fbc51e7d96f37a94d6110469`.
- New independent review receipt: pending.
- Coordinator state update: pending; `docs/execution/state.json` was not
  changed by this lane.
- Safe next action: assign independent review against the exact product commit,
  then record both exact SHAs and the review result in coordinator state.
