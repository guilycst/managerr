# X-04 independent review, round two

## Decision

`changes_requested`.

The capability, mutable-pagination and duplicate-library corrections close
their round-one blockers. Jellyfin playability remains a P1 blocker: the
adapter still promotes item-level path-only evidence to playable when a path
mapping exists, and it treats remote, virtual, offline or incomplete native
media-source metadata as playable whenever a normalized path is present.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of correction author
  `/root/d01_implementer`.
- Reviewed product commit:
  `1df74b45b100b891b6afdf54567d2658d0f6228b`.
- Product parent:
  `c2df3d2b2c8a54a63a8a68d6d1f70dc178b7732e`.
- Product tree:
  `069941027bb18cf2eb7250e3299eb534f1709a10`.
- Reviewed handoff commit:
  `bc597dcab95755ffa8e664a1a106dec1936ae934`.
- Review scope: `internal/adapters/jellyfin/read/`,
  `internal/adapters/seerr/`, and `tests/fixtures/catalogs/`, against the four
  P1 findings in receipt
  `02b1f2475af3abefa9e8c823f9fd85f1047ddc34`, the frozen connector contract,
  and A-08/A-09/A-54/A-55.
- The product changes exactly the seven assigned files. It changes no shared
  contract or execution state.
- Scoped product diff SHA-256:
  `190ead7e93bdfd55c4d9678f533c24bd74379c74b48bb5dbef506b6c356c7ecf`.
- Scoped product archive SHA-256:
  `6b274c61a0595918f22e89c46f273b8de88fe87815a64c116f27039006b2dc28`.
- Product checks ran in a clean detached reviewer-owned worktree at the exact
  product commit. Independent adversarial tests ran in disposable Git archive
  `/tmp/managerr-x04-r2-review.bSvAp6` and were not added to the product.
- No live Jellyfin/Seerr service, credential, private inventory, media payload,
  upstream mutation, filesystem action, release or deployment was used.

## Finding

### P1: path and malformed source metadata still become playable evidence

- Location: `internal/adapters/jellyfin/read/client.go:764-840`.
- Evidence: `observeItem` synthesizes a `mediaSourceDTO` when an item has
  `Path` but no native `MediaSources`. The round-two guard rejects that
  synthetic source only when `MappedTarget == nil`; an unambiguous syntactic
  mapping therefore promotes it to `Item.Playable == true` even though no
  native media-source observation exists. Separately, `observeMediaSource`
  validates only that `Path` normalizes. It ignores the source's `Protocol`,
  `LocationType` and `MediaType`, so remote, virtual, offline and incomplete
  source shapes all become playable when no mapping is configured.
- Independent reproduction:
  1. Construct a `FileSystem` movie with item-level path
     `/remote/media/movie.mkv`, no `MediaSources`, and an unambiguous mapping
     from `/remote/media` to root `library`; call `observeItem`.
  2. Construct a `FileSystem` movie with one native source and no mapping.
     Repeat with source location `Virtual`, `Offline`, `Remote`, missing
     location, missing protocol, missing media type, and an audio source for a
     movie; call `observeItem` for each.
- Observed result:

  ```text
  mapped path-only: Playable:true
    Evidence:[media_source_from_item_path media_source_mapped]
  virtual source:  Playable:true Evidence:[media_source_present]
  offline source:  Playable:true Evidence:[media_source_present]
  remote source:   Playable:true Evidence:[media_source_present]
  unknown source fields and movie/audio mismatch: Playable:true
  ```

- Failure mode: correlation or malformed source metadata becomes a positive
  availability observation. A configured string mapping proves namespace
  translation; it does not manufacture the missing native `MediaSources`
  record or verify current permission/mount access. An item can therefore
  appear available while Jellyfin supplied only presence evidence or an
  explicitly nonlocal/unavailable source shape.
- Contract: `connectors.md:137-142` requires item presence and playable-media
  observation to remain distinct when permissions or mounts prevent
  verification. A-08 requires no false availability. A-55 requires mapping
  verification. The round-one required change explicitly said path-only rows
  remain presence/correlation evidence and requested remote, virtual, offline
  and unknown-source regression proof.
- Required change: validate the complete native media-source shape before
  setting playable. Item-level path-only observations must remain unplayable
  correlation evidence even when their path maps. Reject or retain as unknown
  unsupported protocol/location/media-type combinations with stable evidence;
  do not infer valid values into missing native metadata.
- Required proof: path-only with and without mapping, valid native mapped file
  source, valid native file source under the documented no-mapping rule,
  missing path, wrong/ambiguous mapping, remote/virtual/offline source,
  missing protocol/location/media type, and item/source media-type mismatch.
  Only explicitly documented supported native source shapes may become
  playable.
- Disposition: `current_blocker`.

## Round-one findings rechecked

### Closed: capability claims remain unknown without a pinned release

Independent probes returned version `999.0.0` and HTTP 404 for every catalog
route. Jellyfin libraries/items/provider/playable and Seerr
media/request/provider capabilities remained `unknown`, with
`compatibility_version_unpinned` evidence. Version metadata alone did not
promote a read route.

### Closed: offset traversal cannot certify complete coverage

Independent Jellyfin, Seerr media and Seerr request probes returned identities
`1,2` followed by `4,5` while preserving total four and two-page metadata.
Every terminal result remained `partial` with
`pagination_snapshot_unverified`. Jellyfin retained a signed local-review
cursor while preserving partial coverage.

### Closed: duplicate library identities cannot certify empty coverage

Independent equal and conflicting duplicate-library probes remained partial.
The conflicting case preserved both `library_identity_duplicate` and
`library_identity_conflict`; the second library row was not traversed.

## Safeguards that passed

- Both adapters retain read-only GET-only HTTP boundaries, redirect refusal,
  connection-scoped identity and bounded response/page/record handling. Seerr
  exposes no write surface; Jellyfin refresh remains typed unsupported.
- Jellyfin mapping rejects traversal, relative and ambiguous paths. The
  remaining finding concerns what evidence can establish playability after a
  path has been syntactically mapped.
- Seerr retains native statuses, provider relationships and separate media and
  request observations while preserving partial multi-request coverage.
- Three catalog fixtures parse as synthetic JSON. Public-secret/private-path
  scanning found only API field names, synthetic fixture keys and adversarial
  traversal strings.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/parent/tree/handoff and seven-file owned diff | Inspected; identities above are exact. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed; storage completed in 14.846 seconds. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed; storage completed in 235.119 seconds. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| Root, UI and tools `GOWORK=off go mod verify` | Passed. |
| UI and tools tests/vet with `GOWORK=off -mod=readonly` | Passed. |
| `./scripts/generate.sh --check` | Passed. |
| `./scripts/check-api.sh` | Passed; Vacuum quality score 100/100. |
| `./scripts/check-lint.sh` | Passed with zero issues; architecture passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases; local links resolve. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| Linux amd64 and arm64 CGO-free focused compile | Passed. |
| Three owned fixture JSON files | Parsed successfully. |
| Scoped diff/archive identity, `git diff --check`, commit whitespace check and clean status | Passed before receipt creation. |
| Public credential/private-path scan | Passed with synthetic/API-only matches. |
| Arbitrary-version capability probes | Passed; unpinned read capabilities remained unknown. |
| Offset deletion/reorder probes | Passed; Jellyfin and both Seerr collections remained partial. |
| Equal/conflicting duplicate-library probes | Passed; stable partial reasons retained. |
| Path-only without mapping probe | Passed; remained unplayable. |
| Mapped path-only and unsupported-source-shape probes | Failed contract as reproduced above. |

## Acceptance contribution

- A-08: not accepted. Unsupported source evidence can still produce false
  Jellyfin availability.
- A-09: accepted for connection scoping and collision resistance.
- A-54: accepted for read-only Seerr status and honest multi-page coverage.
- A-55: not accepted. Mapping confinement passes, but a mapping still promotes
  non-native item-path evidence to playable.

## Reviewer decision

`changes_requested`. Correct the remaining playability boundary and add the
listed adversarial regressions before another independent review. The green
repository gates do not exercise this unsafe evidence promotion.
