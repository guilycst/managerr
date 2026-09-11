# X-04 independent review, round one

## Decision

`changes_requested`.

Read-only HTTP boundaries, scoped detailed identities, path confinement,
native Seerr status retention, typed unsupported refresh, and ordinary
pagination tests pass. Four P1 findings remain. Both adapters claim read
capabilities from any non-empty version string despite unresolved version
gates. Offset pagination can omit an identity and still produce complete
coverage. Jellyfin treats an item path with no native media source or mapping
as playable. Duplicate Jellyfin library identities can produce complete empty
coverage.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of X-04 implementer
  `/root/d01_implementer`.
- Reviewed product commit:
  `b4ad382326df4bd780572aed91fafdb0899a1e99`.
- Product parent:
  `e5e742e98939aa9046f8c10ecbfa16484bd9c33c`.
- Product tree:
  `2247c9569a9a3f4cff5cd6efdbd2568daddfdc27`.
- Corrected handoff commit:
  `3ca44e16768e567e15069eadc44c68e0962a590c`.
- Superseded inputs, not reviewed: empty product commit
  `82a8a80b05cd150716dcdc1f41965f30148cbbdc` and handoff
  `9ea995acef259301117c3c446c3ab80b1af18b45`.
- Review scope: `internal/adapters/jellyfin/read/`,
  `internal/adapters/seerr/`, and `tests/fixtures/catalogs/`, against the
  frozen ports, connector specification, compatibility matrix, and
  A-08/A-09/A-54/A-55.
- Product commit adds exactly seven assigned files. It does not change shared
  contracts or execution state.
- Scoped product diff SHA-256:
  `616f69cad4d992dcbe2498273dd9ae5bc39cea3afd2741dc049475e3e84037c2`.
- Scoped product archive SHA-256:
  `afd06db9b3b2065703b42df6240609c2ed03daa84b5af9341bbe17a060d71cd4`.
- Product checks ran in a clean detached reviewer-owned worktree at the exact
  product commit. Independent adversarial tests ran in disposable Git archive
  `/tmp/managerr-x04-r1-review.7ocBSH` and were not added to the product.
- No live Jellyfin/Seerr service, credential, private inventory, media payload,
  upstream mutation, filesystem action, release, or deployment was used.

## Findings

### P1: arbitrary versions claim unverified read capabilities

- Location: `internal/adapters/jellyfin/read/client.go:487-527` and
  `internal/adapters/seerr/client.go:387-423`.
- Evidence: Jellyfin copies `supported` from any successful non-empty version
  response to libraries, items, and provider IDs. Seerr likewise marks media,
  requests, and provider relationships supported. Neither adapter matches a
  tested release or probes the reported read routes. The compatibility matrix
  leaves both selected product releases unknown and says Jellyfin read blocks
  until a versioned fixture exists.
- Independent reproduction:
  1. Return version `999.0.0` from Jellyfin `/System/Info/Public` or Seerr
     `/api/v1/status`.
  2. Return HTTP 404 from every catalog route.
  3. Call `Capabilities` and inspect read states.
- Observed result:

  ```text
  jellyfin.libraries State:supported Version:999.0.0
  seerr.media.read State:supported Version:999.0.0
  ```

- Failure mode: runtime metadata tells the operator that an untested and
  unusable read API is supported. Reconciliation can then rely on a capability
  claim that has no route or version evidence.
- Contract: `connectors.md:137-140` and `:146-150` require supported APIs.
  `connectors.md:158-167` requires supported-version detection and explicit
  unsupported reasons. `compatibility-matrix.md:26-29` leaves these product
  release gates unknown, with a hard Jellyfin read blocker.
- Required change: bind each capability to a documented tested version range
  and its specific route evidence. Until that evidence exists, report unknown
  or unsupported with a stable reason. A version endpoint alone can establish
  observed version metadata, not support for other endpoints.
- Required proof: known tested, unknown newer, unknown older, absent route,
  malformed route, and unauthorized cases for each read capability. An
  arbitrary version with 404 catalog routes must never report supported.
- Disposition: `current_blocker`.

### P1: mutable offset traversal can silently omit records and become complete

- Location: `internal/adapters/jellyfin/read/client.go:539-687`,
  `internal/adapters/seerr/client.go:456-597`, and
  `internal/adapters/seerr/client.go:601-744`.
- Evidence: Jellyfin collects a snapshot with repeated `StartIndex` requests,
  then hashes only the collected result. Seerr carries the first page digest
  in `SnapshotRevision`, but never validates a stable collection revision on
  later `take`/`skip` requests. Stable totals, page counts, and page sizes are
  accepted as sufficient. Overlap detection catches some insertions, but a
  deletion or reorder that changes page membership without overlap remains
  invisible.
- Independent reproduction:
  1. Return page one with identities `1,2`, total 4, two pages.
  2. Simulate mutation before page two and return identities `4,5`, preserving
     total 4, two pages, page size 2, and no overlap. Identity `3` is omitted.
  3. Complete Jellyfin inventory, Seerr media, and Seerr request traversals.
- Observed result:

  ```text
  Jellyfin: ObservedCount:4 Completeness:complete ReasonCodes:[]
  Seerr media: ObservedCount:4 Completeness:complete ReasonCodes:[]
  Seerr requests: ObservedCount:4 Completeness:complete ReasonCodes:[]
  ```

- Failure mode: incomplete inventory becomes fresh complete evidence and may
  prove a missing title or request absent. This violates the central rule that
  missing evidence remains unknown.
- Contract: I-05 in `spec-001-media-reconciliation.md:201` forbids partial or
  uncertain evidence from becoming fresh absence. `connectors.md:149-150` and
  `:158-164` require complete coverage, overlap defense, and partial coverage
  under truncation. A-08 and A-54 require no false availability/absence and
  preserved coverage.
- Required change: establish an immutable or validated collection boundary for
  every multi-request offset traversal. If the upstream cannot provide one,
  retain multi-request results as partial. A local digest of already-collected
  rows or the first page does not certify collection stability.
- Required proof: insertion, deletion, reorder, overlap, stable traversal,
  empty traversal, and changed total/page metadata for Jellyfin items, Seerr
  media, and Seerr requests. Every drift case stays partial with a stable reason
  and cannot certify absence.
- Disposition: `current_blocker`.

### P1: Jellyfin path-only evidence becomes playable without verification

- Location: `internal/adapters/jellyfin/read/client.go:689-768`.
- Evidence: when `MediaSources` is empty, `observeItem` synthesizes a source
  from the item `Path`. `observeMediaSource` returns `playable=true` for any
  normalized path whenever no mappings are configured. This contradicts the
  adjacent comment that path-only evidence is retained without treating the
  item as playable, and contradicts the handoff statement that unmapped paths
  remain unavailable or unresolved.
- Independent reproduction: return one `FileSystem` movie with path
  `/media/path-only.mkv` and no `MediaSources`; configure no path mappings; call
  `ObserveItem`.
- Observed result:

  ```text
  Playable:true Evidence:[media_source_from_item_path media_source_present]
  MappedTarget:<nil>
  ```

- Failure mode: item presence becomes a playable-media observation despite
  absent native source evidence and no configured namespace proof. Permission
  or mount failures can therefore appear as availability.
- Contract: `connectors.md:137-142` requires separate item presence and
  playable-media observation when permissions or mounts prevent verification.
  A-08 forbids false availability; A-55 requires mapping verification.
- Required change: keep path-only records as presence/correlation evidence.
  Report playable only from explicit supported native source evidence under a
  defined rule, and require an unambiguous configured mapping wherever local
  namespace or mount verification is part of that claim.
- Required proof: path only, title only, missing path, wrong mapping, ambiguous
  mapping, valid mapped file source, remote/virtual/offline source, and unknown
  source metadata. Only the documented verified cases become playable.
- Disposition: `current_blocker`.

### P1: duplicate Jellyfin library identities can certify empty coverage

- Location: `internal/adapters/jellyfin/read/client.go:221-261` and
  `internal/adapters/jellyfin/read/client.go:539-591`.
- Evidence: `Libraries` accepts repeated library IDs. `collectSnapshot` detects
  duplicate item IDs but never duplicate library identities. When both repeated
  library reads return empty item lists, no item overlap exists to expose the
  malformed collection and inventory coverage becomes complete.
- Independent reproduction: return libraries `library/Movies` and
  `library/Movies Copy` with the same ID, then return an empty item page with
  total 0 for both reads.
- Observed result:

  ```text
  ObservedCount:0 Completeness:complete ReasonCodes:[]
  SnapshotRevision:sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
  ```

- Failure mode: inconsistent library identity evidence is hidden and becomes a
  fresh empty inventory claim.
- Contract: I-05 forbids inconsistent evidence from becoming fresh absence.
  `connectors.md:160-164` requires malformed and duplicate collection behavior
  to remain bounded and honest. A-08 relies on no false absence/availability.
- Required change: validate and deduplicate library identities before item
  traversal. Any repeated ID, including equal rows, must retain a stable partial
  reason. Do not silently certify empty coverage from a malformed library list.
- Required proof: exact duplicate library, conflicting name/type for one ID,
  distinct libraries, duplicate item across libraries, and empty valid library
  list. Duplicate cases remain partial; the valid empty case may be complete.
- Disposition: `current_blocker`.

## Safeguards that passed

- Every inspected production HTTP request uses GET. Both cloned clients refuse
  redirects, and Seerr exposes no write method.
- Jellyfin refresh returns typed `OutcomeUnsupported` for valid scopes without
  sending a request. Refresh acceptance remains separate from availability.
- Connection validation and detailed `connection-id:external-id` identities
  keep equal upstream IDs distinct across configured instances.
- Path normalization rejects traversal, aliases, mixed separators, relative
  sources, boundary-prefix confusion, and ambiguous equal-length mappings.
- Seerr retains native integer statuses, normalized names, timestamps,
  provider IDs, service relationships, 4K variants, season data, and independent
  request/media availability.
- Existing tests cover unauthorized, rate limit, timeout, malformed response,
  response/page/record bounds, empty result, overlap, and signed cursor scope.
- Fixture files are synthetic valid JSON. No real host, tracker, credential,
  inventory, or media payload was found.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/parent/tree/handoff and seven-file owned diff | Inspected; identities above are exact. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed; storage completed in 15.069 seconds. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed; storage completed in 187.823 seconds. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| Root, UI, and tools `GOWORK=off go mod verify` | Passed. |
| UI and tools tests/vet with `GOWORK=off -mod=readonly` | Passed. |
| `./scripts/generate.sh --check` | Passed. |
| `./scripts/check-api.sh` | Passed; Vacuum quality score 100/100. |
| `./scripts/check-lint.sh` | Passed with zero issues; architecture passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, local links resolve. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| Linux amd64 and arm64 CGO-free focused compile | Passed. |
| Three owned fixture JSON files | Parsed successfully. |
| Scoped diff/archive identity, `git diff --check`, commit whitespace check, and clean status | Passed before receipt creation. |
| Public credential/private-path scan | Passed; matches are API field names and synthetic fixture values only. |
| Arbitrary-version capability probes | Failed contract: both adapters reported unverified catalog routes supported. |
| Offset deletion/reorder probes | Failed contract: Jellyfin, Seerr media, and Seerr requests all became complete after omitting identity 3. |
| Path-only Jellyfin probe | Failed contract: item became playable with no `MediaSources` or mapping. |
| Duplicate Jellyfin library probe | Failed contract: malformed duplicate collection became complete empty coverage. |

## Acceptance contribution

- A-08: not accepted. Path-only Jellyfin evidence can become false
  availability, while incomplete traversal can become false absence.
- A-09: accepted for detailed observation, cursor, and connection scoping. No
  cross-instance identity collision was reproduced.
- A-54: not accepted. Native Seerr states and no-write behavior pass, but
  mutable media/request pagination can silently claim complete coverage.
- A-55: not accepted. Refresh is safely unsupported and configured path
  confinement passes, but path-only data bypasses mapping verification.

## Reviewer decision

`changes_requested`. Fix all four P1 findings and add the required adversarial
regressions before another independent review. Existing green tests and tool
gates do not exercise these unsafe evidence transitions.
