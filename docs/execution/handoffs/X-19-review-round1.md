# X-19 independent review, round 1

## Decision and exact scope

Decision: **changes_requested**. Two P1 findings remain open. Passing existing
tests and guardrails does not establish safe native file identity or malformed
response handling.

- Reviewer: `/root/x05_reviewer`; not the author of this batch.
- Product: `a03066b4923cb51d4797bdd40eaedc3dad9305b3`.
- Product tree: `ee5d00e5c9461aa2e804438ba4e0b579e5f0bcc4`.
- Handoff: `f92177ce984e9b3d84d0f2a07e22e9fa823043e7`.
- Handoff tree: `19044ec4f7b4aea31a625e805578e99807590672`.
- Reviewed paths: `internal/adapters/arr/read/`, `tests/fixtures/arr/read/`,
  root `go.mod` and `go.sum`, and the standalone Sonarr/Radarr client modules.
- SHA256 of the product's binary Git diff from its parent, restricted to the
  four owned product paths: `515198de471144df7228cfea661e18125f107655a635cb52474898026c4169fa`.
- Acceptance contributions: A-07/A-08/A-09/A-10/A-16; standalone module policy.

Review used an isolated detached checkout at the handoff. Additional synthetic
probes ran in an archive of the exact product. No product, state, shared-checkout
or live-service data was changed. This commit contains only this receipt.

## R1 — P1: native Sonarr observations accept contradictory file identities

Locations: `internal/adapters/arr/read/native.go:361-385` (catalog aggregation)
and `native.go:516-537` (import observation aggregation).

The native read-back aggregates only by file ID. When a second episode reports
that ID with different path or size, it adds the episode ID to the first file
without comparing the second file's details. Both native paths also accept
different file IDs at the same mapped path. The catalog reports complete
coverage for this collision. The established legacy aggregation at
`client.go:1433-1526` rejects these contradictory relationships; the migration
loses those checks when a response meets the native contract.

Deterministic reproduction uses `newSyntheticClient` from the existing package
tests with Sonarr, connection `reviewer-sonarr`, page limit 2, and bounds
10/50/50. The synthetic GET `/api/v3/episode` returns these rows:

```json
[
  {"id":301,"seriesId":201,"seasonNumber":1,"episodeNumber":1,"hasFile":true,"episodeFileId":801,"episodeFile":{"id":801,"seriesId":201,"path":"/downloads/series/one.mkv","size":10}},
  {"id":302,"seriesId":201,"seasonNumber":1,"episodeNumber":2,"hasFile":true,"episodeFileId":801,"episodeFile":{"id":801,"seriesId":201,"path":"/downloads/series/two.mkv","size":20}}
]
```

`ObserveImport(ctx, "reviewer-sonarr", "201")` returns nil error and a single
file 801 at `library:series/one.mkv`, size 10, with episode IDs 301 and 302. This
claims the second episode belongs to a file whose path and size disagree with
its evidence. Changing the second row's file IDs to 802 and its path/size to
`/downloads/series/one.mkv`/10 returns nil error and two distinct file records at
the same target. For the catalog case, also serve GET `/api/v3/series` with:

```json
[{"id":201,"title":"Synthetic","path":"/downloads/series","monitored":true}]
```

`List(ctx, "reviewer-sonarr", "", 2)` returns complete coverage, no reason codes,
and both collided files. A positive control with file ID 801, identical path
and size on both episodes correctly returns one file associated with both.

The reviewer probes `TestReviewerNativeReadbackRejectsConflictingFiles` and
`TestReviewerNativeCatalogRejectsPathIdentityCollision` reproduced these failures
on all three race-enabled repetitions. Existing package tests pass because the
older contradictory fixtures use legacy shapes and do not exercise this native
aggregation. These are semantic failures, not race detector reports.

Required correction: enforce cross-row identity consistency in both native
paths, including same-ID path/size agreement and unique file identity per mapped
path. Contradictory evidence must remain incomplete/conflicted rather than a
successful import observation or complete catalog. Add fully native synthetic
regressions and retain the valid multi-episode-file positive control. Shared
aggregation would reduce the chance of catalog and read-back rules diverging.

## R2 — P1: malformed native identities become complete through legacy fallback

Locations: `native.go:50-71`, catalog fallback at `native.go:43`, and the legacy
JSON decoder at `client.go:3339`.

`nativeCompatibilityFallback` accepts any standalone `ErrorMalformed`, and
generally translated unknown/unsupported results. It does not establish that
the payload is a documented older response shape. The second decoder uses
standard `encoding/json`, which accepts duplicate object keys. A strict native
identity rejection can consequently become complete authoritative evidence.
The handoff's claim that fallback is limited to old synthetic shapes is not
enforced by this predicate.

Deterministic reproduction uses the existing synthetic helper with Radarr and
connection `reviewer-radarr`. Every GET `/api/v3/movie` returns exactly:

```json
[{"id":101,"id":102,"title":"Synthetic","path":"/downloads/movies","monitored":true,"movieFile":{"id":501,"movieId":102,"path":"/downloads/movies/one.mkv","size":10}}]
```

`List(ctx, "reviewer-radarr", "", 2)` makes two requests: strict native decoding
rejects duplicate `id`, then fallback accepts the last value. The result has nil
error, complete coverage, record ID 102, and file 501 with movie ID 102. The
reviewer probe `TestReviewerMalformedNativeIdentityCannotUseLegacyFallback`
reproduced the failure on all three race-enabled repetitions. The payload is a
malformed native identity, not an old compatibility schema.

Required correction: distinguish explicitly supported compatibility shapes or
capabilities from native syntax/identity violations. Never downgrade duplicate
keys, invalid UTF-8, missing required identity or foreign scoped identity into
a permissive successful decode. Preserve unknown/error evidence. Any retained
legacy mode must enforce the same global syntax and identity invariants. Add
malformed native rejection tests alongside a positive older-shape compatibility
test, including catalog and scoped preview entrypoints that share this helper.

## Independent checks and limits

| Check run by reviewer | Exact outcome |
| --- | --- |
| Root: `GOWORK=off go test -mod=readonly -race -count=3 -timeout=120s ./internal/adapters/arr/read ./internal/domain ./internal/ports` | Exit 0; adapter 8.491s, domain 1.891s, ports 1.597s. |
| Root focused vet and `GOWORK=off go mod verify` | Exit 0; all modules verified. |
| Each Sonarr/Radarr module: `GOWORK=off go test -mod=readonly -race -count=3 -timeout=120s ./...`, vet and module verification | Exit 0 for both; all modules verified. |
| Each Sonarr/Radarr module: `GOWORK=off GOPROXY=off GOSUMDB=off ./check-generation.sh` | Exit 0; committed output reproduced offline with installed pinned tool dependencies. |
| Each Sonarr/Radarr module: Linux amd64 and arm64 builds, `CGO_ENABLED=0 GOWORK=off`, `-mod=readonly ./...` | Exit 0 for all four builds. |
| Root `GOWORK=off ./scripts/check-lint.sh` | Exit 0; nine modules reported zero issues; architecture passed. |
| Planning and architecture scripts | Exit 0; 53 tasks, 60 acceptance cases and resolving local links; import boundaries passed. |
| `GOWORK=off GOPROXY=off GOSUMDB=off ./scripts/check-guardrails.sh --fast` | Exit 0; generation/staged generation, working/staged Vacuum contracts, architecture, formatting and targeted tests; final `guardrail checks passed (fast)`. |
| Exact-product reviewer probes: `GOWORK=off go test -mod=readonly -race -count=3 -timeout=120s -run TestReviewer -v ./internal/adapters/arr/read` | Test exit 1; four conflict assertions failed on every repetition; consistent multi-episode positive control passed; no race detector finding. |

Fresh public bootstrap was independently tested using a new module cache,
`GOWORK=off GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org`, and
`go mod download -json` for both exact root dependencies. Both succeeded at
`v0.0.0-20260915230044-873e53ef6729`, with Git origin
`873e53ef67292549bcd3e42f8246d10c81496132`, correct module subdirectories, and
checksums matching root `go.sum`. The local module file contents match those
published modules byte-for-byte. No local replace or go.work is needed for this
adapter's dependency bootstrap. Offline generation still requires the pinned
tools/dependencies to be installed; this is not a fresh empty-cache offline
installation claim.

Source inspection and existing focused tests support private generated DTO
isolation, connection-scoped translation, sanitized typed transport errors,
native downloaded-folder query behavior and read-only dispatch. These passing
properties do not close R1/R2. Catalog reads are bounded but fetch all records'
file evidence before local pagination; a local page does not imply one upstream
request or a stable upstream snapshot.

Lookup/history and detailed reprocess preview remain on the root's read-only
compatibility transport; they are not supported operations in the standalone
Sonarr/Radarr contracts. Their existing synthetic coverage is not live version
compatibility proof. No Arr write capability is enabled; G-01 remains open.
Full root tests/vet, root cross-builds, the full CI aggregate, remote CI and live
upstream behavior were not rerun in this review and are not claimed approved.

## Handoff

Coordinator should record this receipt's exact commit SHA separately. Keep X-19
open for correction of R1 and R2, followed by independent re-review against the
exact corrected product and handoff. This receipt does not authorize publication,
deployment, writes or live media operations.
