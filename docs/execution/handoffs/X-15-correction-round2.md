# X-15 correction round two handoff

## Assignment

- Task ID: X-15 correction round two.
- Owner: `/root/x05_implementer`.
- Independent reviewer: coordinator-assigned after this handoff.
- Correction base checkpoint: `6f5f582f56cf7840cb0dd549c55ea4c224904c19`.
- Prior correction product: `f258104718691807e9679e0c6eb278b144ea7a09`.
- Prior review receipt: `3b1347fdef9a760ba2a5ac73395c142ecd2d653b`.
- Branch/worktree: shared checkout on `main`; coordinator owns execution state.
- Owned product paths: `clients/sonarr/`.
- Owned handoff path: `docs/execution/handoffs/X-15-correction-round2.md`.
- Acceptance contributions: A-07, A-09, A-10 and A-45.

## Product result

Product commit: `828e0084f2d88f86050db5912412352c1a363bc1`
(`fix(sonarr): require exact language alias sets`). The standalone Sonarr
client remains read-only and module-isolated. No root module, adapter, state,
task plan, UI, other client, write endpoint or local replace directive changed.

This correction closes review finding R3a. When native singular `language` and
compatibility plural `languages` are both present, normalization now requires
the exact same semantic set. The singular object represents a one-element set,
so a plural superset such as `[English, Portuguese]` is rejected as ambiguous;
a different language is also rejected. Equivalent aliases pass regardless of
JSON member order. Older single-form responses remain supported: singular-only
responses populate the native `Language` observation and plural-only responses
populate `Languages`.

The prior correction guarantees remain intact: folder previews reject an
ambiguous `seriesId`, library previews use the native series mode and verify
returned series/episode scope, episode-file observations preserve and validate
native `seriesId`, nested required fields and duplicate JSON members fail
closed, native rejection `reason` and language evidence remain typed, unknown
upstream members stay isolated from normalized observations, and generation
uses the pinned tools-module oapi-codegen command offline.

## Verification

All checks below were run after product commit
`828e0084f2d88f86050db5912412352c1a363bc1` unless a command is explicitly
identified as a repository check. Fixtures are synthetic `httptest` responses
only; no live Sonarr service, credential, private endpoint, inventory, media
file or write was used.

| Command or scenario | Result |
| --- | --- |
| `GOWORK=off go test -mod=readonly ./...` from `clients/sonarr` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=3 -timeout=180s ./...` from `clients/sonarr` | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` from `clients/sonarr` | Passed. |
| `GOWORK=off go mod verify` from `clients/sonarr` | Passed: `all modules verified`. |
| `GOWORK=off GOPROXY=off GOSUMDB=off go generate ./...` from `clients/sonarr` | Passed; generated output unchanged and reproducible. |
| `GOWORK=off GOPROXY=off GOSUMDB=off ./check-generation.sh` from `clients/sonarr` | Passed: `Sonarr generation checks passed`. |
| Pinned Sonarr Vacuum from `tools` | Passed; quality 100/100 with zero warnings/errors. |
| Pinned Sonarr golangci-lint from `clients/sonarr` | Passed: `0 issues.` |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/sonarr` | Passed. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/sonarr` | Passed. |
| `GOWORK=off go test -mod=readonly ./...` and `GOWORK=off go vet -mod=readonly ./...` from repository root | Passed. |
| `GOWORK=off go mod verify` from repository root | Passed: `all modules verified`. |
| `./scripts/check-lint.sh` | Passed with zero issues; architecture checks passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 53 tasks, 60 acceptance cases; local links resolve. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed; existing CI module matrix and all guardrail stages passed. |
| `git diff --check` and product/handoff commit pre-commit hook | Passed. |

## Integration and blocker

The coordinator-owned aggregate scripts and CI matrix currently discover the
published qBittorrent and NZBGet modules but do not yet include
`clients/sonarr`. Direct Sonarr checks above are the evidence for this lane.
Before an Arr adapter migration, the coordinator must add Sonarr to root
generation, lint, test, vet, verification, cross-build and CI matrices and
publish a consumable module version (or use the approved bootstrap procedure).
This lane did not add `go.work`, a permanent local replace, or a root
dependency.

Arr registration/import writes remain outside this read-only client and remain
blocked by G-01. No native write capability was enabled.

## Review and resume checkpoint

- Product commit: `828e0084f2d88f86050db5912412352c1a363bc1`.
- Handoff commit: pending; this file is intentionally committed separately.
- Prior review receipt: `3b1347fdef9a760ba2a5ac73395c142ecd2d653b`.
- New independent review receipt: pending.
- Coordinator state update: pending; `docs/execution/state.json` was not
  changed by this lane.
- Safe next action: assign independent review against the exact product commit,
  then record both exact SHAs and the review result in coordinator state.
