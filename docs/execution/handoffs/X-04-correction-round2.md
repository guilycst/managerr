# X-04 correction round two handoff

## Assignment and review input

- Task: X-04, Jellyfin and Seerr read observations.
- Correction owner: `/root/d01_implementer`.
- Independent reviewer: `/root/d01_reviewer`.
- Correction base/product under review: `b4ad382326df4bd780572aed91fafdb0899a1e99`.
- Previous corrected handoff: `3ca44e16768e567e15069eadc44c68e0962a590c`.
- Review receipt addressed: `02b1f2475af3abefa9e8c823f9fd85f1047ddc34`.
- Shared checkout: `main`; the coordinator owns integration and execution state.
- Owned paths: `internal/adapters/jellyfin/read/`, `internal/adapters/seerr/`,
  `tests/fixtures/catalogs/`, and this handoff.
- Product correction commit: `1df74b45b100b891b6afdf54567d2658d0f6228b`.

The correction preserves the frozen domain and port contracts. It changes no
credentials, live configuration, media payload, execution state, or upstream
service. All new evidence is synthetic and read-only.

## Findings resolved

### Capability claims

Jellyfin and Seerr now report a successful version probe as version metadata
only. Catalog, item/request, provider-relationship and playable-media
capabilities remain `unknown` until a tested release and route contract are
pinned. The capability evidence includes `compatibility_version_unpinned` and
stable reasons explain why an arbitrary newer or older version cannot promote
untested routes to `supported`. Unsupported, unavailable, malformed and
unauthorized version probes retain their typed behavior; no capability probe
performs a write.

### Offset traversal

Jellyfin `StartIndex` traversal and Seerr `take`/`skip` traversal do not expose
an immutable upstream snapshot identity. Multi-request traversal now retains
the observed records but carries `pagination_snapshot_unverified`, so terminal
coverage remains `partial` and cannot certify fresh absence. Signed cursors and
local digests still detect a changed Jellyfin re-read; they are not presented
as proof of upstream immutability. Jellyfin local paging remains available for
the unverified-only case so the operator can inspect every observed record;
ambiguous or truncated snapshots do not offer a continuation.

The Seerr fixtures exercise a stable-looking deletion/reorder case: page one
contains IDs `101,102`, while page two contains `104,105` with unchanged
totals, page count and page size. The request fixture exercises the same case
with IDs `9001,9002` followed by `9004,9005`. Both terminal results retain the
records and remain partial.

### Jellyfin path-only playability

An item-level `Path` without native `MediaSources` remains useful
presence/correlation evidence, but it is no longer sufficient for
`Item.Playable`. It can become playable only when the configured, unambiguous
path mapping supplies a verified local target. The adversarial test observes
the path-only fixture through a client without mappings and requires
`media_source_path_only_unverified` plus `Playable == false`; native mapped
media-source evidence remains playable.

### Duplicate Jellyfin libraries

`collectSnapshot` detects duplicate library IDs before traversing the second
row. Equal duplicates produce `library_identity_duplicate`; conflicting
metadata also produces `library_identity_conflict`. The duplicate is not
traversed again, and even an empty duplicate collection remains partial rather
than becoming complete empty coverage. The fixture includes two identical
`library-duplicate` rows and an empty item response.

## Verification evidence

All commands below ran in the shared checkout with synthetic fixtures. The
product commit hook also passed the fast guardrails.

| Command | Result |
| --- | --- |
| `gofmt -l internal/adapters/jellyfin/read/*.go internal/adapters/seerr/*.go` | Passed; no output. |
| `git diff --check` | Passed before product commit. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed; storage completed in 197.851s. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `./scripts/generate.sh --check` | Passed. |
| `./scripts/check-api.sh` | Passed; Vacuum quality score 100/100. |
| `./scripts/check-lint.sh` | Passed with zero issues; architecture passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases; local links resolve. |
| `GOWORK=off go mod verify` in root, `tools/`, and `ui/` | Passed. |
| `GOWORK=off go test -mod=readonly ./...` and `GOWORK=off go vet -mod=readonly ./...` in `tools/` and `ui/` | Passed. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| `GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -mod=readonly -run '^$' -exec=true ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| `GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -mod=readonly -run '^$' -exec=true ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| Python JSON parse of all three catalog fixtures | Passed. |

## Acceptance contribution and next step

This correction closes the four review blockers affecting A-08, A-09, A-54
and A-55: arbitrary versions no longer claim route support, mutable offset
traversals cannot certify complete absence, path-only Jellyfin evidence cannot
claim playability without a mapping, and duplicate libraries cannot certify an
empty inventory. Independent acceptance remains pending `/root/d01_reviewer`.

The coordinator should review commit
`1df74b45b100b891b6afdf54567d2658d0f6228b`, then record the reviewer result
and integration state. No state file was edited by this lane. The next safe
step is the independent adversarial review of the product commit.
