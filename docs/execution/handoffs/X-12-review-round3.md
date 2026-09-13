# X-12 independent review, round three

## Decision

`approved`.

The round-two file-seed sentinel finding is closed. Product
`37f62b515681f7ad37fc9b1d4597581f069320e5` accepts the exact qBittorrent
unknown sentinel `seeds=-1`, retains the corresponding file and mapped payload
observations, and reports partial `item_0_file_seeds_unknown` evidence. Values
below `-1` remain malformed. The two earlier strict-projection fixes remain
covered and passed focused repetition and race checks.

## Review identity and boundary

- Reviewer: `/root/f01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Product: `37f62b515681f7ad37fc9b1d4597581f069320e5`.
- Handoff: `04fa9689d565db23d92e05b1516dec5421d86cf6`; the product is its direct
  parent.
- Coordinator state checkpoint:
  `7ad6e359b831e75dcad784e5d0005fbc851b986f`; the handoff is its ancestor and
  it records the bounded reviewer reroute.
- Review base: `29b2a2a6e3dd0a09854f05ac27ad7f8b409ff9a1`.
- Scoped product diff SHA-256:
  `4634eb05dac5a43bd5b00002249b9d929cad1e070ca7b022d60ed2378c3d3b65`.
- Product changes are limited to
  `internal/adapters/qbittorrent/inventory/inventory.go` and
  `internal/adapters/qbittorrent/inventory/inventory_test.go`.
- Review ran in a clean detached reviewer worktree. No live qBittorrent,
  credentials, private data, upstream mutation, release or deployment was
  involved.

## Finding closure

### Closed: explicit unknown file seed sentinel is retained

- Seed projection validation at
  `internal/adapters/qbittorrent/inventory/inventory.go:503-510` now accepts
  exactly `-1` and rejects values below it. `strconv.ParseInt` still rejects
  fractional, exponent, wrong-type and overflow representations.
- Translation at `inventory.go:1143-1156` preserves the captured value, while
  mapping at `inventory.go:1194-1207` accepts `-1`, retains the file
  observation, and emits `file_seeds_unknown`.
- `TestExplicitUnknownFileSeedsRetainsEvidence` covers `-1` in the first,
  later and multiple rows, omitted seeds, mixed known/unknown rows, mapped
  payload retention, partial reason evidence, and rejection below the
  sentinel.
- Repeated targeted execution preserved `Seeds=-1`, both file and payload
  rows, and exact `item_0_file_seeds_unknown` coverage. The malformed case
  retained the torrent but published no unsafe file/payload rows and reported
  `item_0_files_malformed`.
- Disposition: `resolved`.

### Preserved: strict compatibility projection

- Duplicate semantic members, escaped duplicate names, invalid identity and
  metadata types, invalid hash forms, null, fractional seed values and integer
  overflow remain rejected before compatibility projection.
- Projection remains endpoint- and row-aware: only supported row-level legacy
  members are removed. Cross-endpoint and nested names remain visible to the
  standalone strict decoder and fail closed.
- `TestLegacyProjectionValidatesTypesAndDuplicateMembers`,
  `TestLegacyProjectionIsEndpointAndPathAware`, and
  `TestMisplacedLegacyMembersReachStrictNativeDecoder` passed 50 repeated
  targeted runs. No prior projection finding reopened.
- Disposition: `resolved`.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product, handoff and state ancestry | Passed. |
| Scoped product diff and `git diff --check` | Passed; two owned adapter files only. |
| `GOWORK=off go test -mod=readonly -count=25 ./internal/adapters/qbittorrent/inventory` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=5 ./internal/adapters/qbittorrent/inventory` | Passed. |
| `GOWORK=off go vet ./internal/adapters/qbittorrent/inventory` | Passed. |
| Four sentinel/projection regression tests with `-count=50` | Passed. |
| `gofmt -d` on both changed files | Passed with no output. |
| Reviewer worktree cleanliness before receipt | Passed. |

The coordinator requested a bounded correction review because the prior round
already passed the full five-module matrix. This receipt therefore does not
claim a new broad matrix run.

## Acceptance contribution

- A-04 and A-09 remain satisfied by the unchanged connection-scoped identity
  and strict compatibility projection behavior.
- A-28 remains satisfied by the unchanged read-only adapter behavior. The
  corrected optional per-file seed evidence is represented as unknown without
  erasing independent file and payload observations.

## Reviewer decision

`approved`. No finding remains in the bounded X-12 round-three correction
scope. Coordinator integration, execution-state advancement, release,
deployment and live-stack verification remain separate gates.
