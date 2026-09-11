# X-03 independent review, round four

## Decision

`changes_requested`.

Round four closes the three exact round-three reproductions. Ordinary Arr
history without a snapshot token stays partial across multiple requests,
unknown-only nested objects no longer reach POST, and the tested Sonarr file-ID
and path contradictions become unknown. Three P1 gaps remain. Unpinned
snapshot-shaped fields can still certify drifting history as complete, nested
quality validation is shared across incompatible Radarr and Sonarr schemas and
can also be bypassed by omitting video quality, and one Sonarr episode identity
can still be accepted on two different files.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of the X-03 implementer.
- Reviewed product commit:
  `7c2c3712fd69865d03f0a13308633fadc7ba7228`.
- Product parent:
  `2e30201f197afcfbd6998116180a71b7af15372a`.
- Correction handoff commit:
  `37e501801c6d6d66735913e03d5997e56c9eaa38`.
- Round-three review receipt commit:
  `e1cf5447ab27f64390875a5350fcefcf5dd88a40`.
- Review scope: `internal/adapters/arr/read/` and
  `tests/fixtures/arr/read/`, against the frozen ports, connector contract,
  pinned Radarr/Sonarr sources and A-07/A-09/A-10/A-11/A-16.
- Product commit changes only `internal/adapters/arr/read/client.go` and
  `client_test.go`.
- Scoped product diff SHA-256:
  `a275c6e551f78b51ea3070a59b4f4af291be533440d36c71feeb37c5d5f70086`.
- Scoped product snapshot SHA-256:
  `8a8b90a9fcf3d7e45d5cf5db6de0e0b95d34f1c8ba731c2b2ab05def9301e6b3`.
- Checks ran in a clean detached reviewer-owned worktree at the exact product
  commit. Adversarial tests ran in disposable Git archive
  `/tmp/managerr-x03-r4-review.8zBAy3` and were not added to the product.
- Pinned schema inspection used the exact public source commits already listed
  in `docs/research/compatibility-matrix.md`. No branch head or unpinned release
  was treated as contract evidence.
- No live Arr service, credential, private inventory, media payload, command
  endpoint, filesystem action, release or deployment was used.

## Findings

### P1: unsupported snapshot-shaped fields can certify drifting history as complete

- Location: `internal/adapters/arr/read/client.go:513-555`, `:619-625`, and
  `:3223-3291`.
- Evidence: the pinned Arr history contract has offset pagination and no
  immutable snapshot token. The adapter nevertheless treats any top-level
  string named `snapshotId`, `snapshotToken`, or `snapshotRevision` as a stable
  boundary. It does not negotiate a capability, bind the field to a supported
  Arr version, or send the token back with later requests. The implementation
  handoff itself records that ordinary pinned Arr responses expose no such
  token.
- Independent reproduction:
  1. Page 1 returns history IDs `4,3`, `hasMore=true`, no total and an unknown
     `snapshotId` value.
  2. Delete ID `4` before page 2, so offset page 2 returns only ID `1`; ID `2`
     is skipped. Return the same unknown field value and `hasMore=false`.
  3. Consume the signed cursor and inspect terminal coverage.
- Observed result from
  `TestReviewerHistoryUnpinnedSnapshotFieldCannotProveCompleteness`:

  ```text
  unsupported snapshot field certified deletion drift:
  items=[id=1] count=3 reasons=[]
  ```

- Failure mode: an extra field that is not part of the pinned upstream contract
  turns a known mutable offset traversal into complete absence evidence. A
  proxy, fork, or future response can therefore bypass the conservative rule
  without any proven token semantics.
- Contract: `connectors.md:20-30` requires supported APIs and explicit
  capability evidence; I-05 forbids partial evidence from becoming a fresh
  absence claim. The round-three required change said multi-request history
  must remain partial when the supported Arr API has no immutable boundary.
- Required change: for the currently pinned Arr contracts, keep every
  multi-request history traversal partial. Add complete snapshot-backed
  traversal only after a versioned capability identifies a documented token,
  its immutability semantics, and any request parameter needed to retain that
  snapshot across pages. Unknown response keys cannot enable completeness.
- Required proof: replay insertion and deletion drift with each unknown
  snapshot-shaped key, unchanged and changed totals, and overlapping and
  non-overlapping pages. All stay partial. A future complete case must name a
  pinned API/version and prove that every request belongs to the same immutable
  snapshot.
- Disposition: `current_blocker`.

### P1: quality validation is not product-specific and missing video quality bypasses it

- Location: `internal/adapters/arr/read/client.go:1735-1787`, `:1790-1869`,
  `:2135-2148`, and `:2640-2674`.
- Evidence: `nativeObject` and `validateQualityModel` receive no Arr product
  kind. `validQualitySource` contains Radarr's source enum only. The pinned
  Sonarr v3 schema instead uses values including `television`,
  `televisionRaw`, `web`, `webRip`, and `blurayRaw`, and has no Radarr
  `modifier` field. A valid full Sonarr quality using source `web` is rejected
  as `native_quality_untyped`. Conversely, direct `ReprocessPreview` validates
  quality only when its raw byte slice is nonempty, so a video with no quality
  serializes and POSTs `"quality":null`.
- Pinned source:
  [Sonarr v3 OpenAPI at `9c0d8deb`](https://github.com/Sonarr/Sonarr/blob/9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb/src/Sonarr.Api.V3/openapi.json)
  defines the Sonarr `QualitySource` enum and `QualityModel`; the
  [Radarr v3 OpenAPI at `0220f0da`](https://github.com/Radarr/Radarr/blob/0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4/src/Radarr.Api.V3/openapi.json)
  defines the distinct Radarr source/modifier schema.
- Independent reproductions:
  1. Return a Sonarr native candidate with the pinned quality object
     `id=3`, name `WEBDL-1080p`, source `web`, resolution 1080 and a typed
     revision. Request an exact one-episode reprocess preview.
  2. Call the exported Radarr `ReprocessPreview` with one video file and an
     empty quality value while recording all manual-import POST bodies.
- Observed results:

  ```text
  pinned Sonarr quality was rejected:
  code=native_quality_untyped

  missing typed quality reached Arr POST: posts=1
  ```

- Failure mode: valid Sonarr native evidence cannot flow through the reviewed
  reprocess bridge, while an untyped `null` video quality can reach Arr through
  the direct path. Product-specific fields can also cross the wrong product
  schema because the shared validator accepts a union rather than the selected
  instance's schema.
- Contract: `connectors.md:93-111` requires quality values from the selected
  instance's supported schema and explicit DTOs verified against pinned
  upstream schemas. A-10/A-11/A-16 require retained native evidence and reject
  unsafe preview input before POST.
- Required change: validate quality and custom-format fields against the
  selected `ConnectionKind`, using separate bounded Radarr and Sonarr types or
  validators. Require typed quality for video on every callable path. Keep the
  explicit subtitle exception only where the product contract permits it, and
  reject fields and enum values from the other Arr product.
- Required proof: use complete pinned Radarr and Sonarr quality objects and
  every pinned enum value. Each product accepts its own values, rejects the
  other's exclusive values, and preserves the exact validated POST body.
  Missing, null, unknown-only and malformed video quality must produce zero
  POSTs from both native mapping and direct reprocessing.
- Disposition: `current_blocker`.

### P1: one Sonarr episode identity can be accepted on two physical files

- Location: `internal/adapters/arr/read/client.go:1364-1433`.
- Evidence: the new `index` checks repeated file IDs and `pathIndex` checks
  repeated physical paths. There is no inverse index for episode IDs. If two
  episode rows have the same episode ID but different, internally consistent
  file IDs and paths, both files receive that episode and no reason code is
  emitted.
- Independent reproduction:
  1. Return episode ID `301` with outer/nested file ID `801`, path
     `series/one.mkv` and size 10.
  2. Return episode ID `301` again with outer/nested file ID `802`, path
     `series/two.mkv` and size 20.
  3. Call `ObserveImport` for series `201` with
     `includeEpisodeFile=true`.
- Observed result from
  `TestReviewerEpisodeIdentityCannotBelongToTwoFiles`:

  ```text
  one episode identity was accepted on two files:
  file 801 series/one.mkv episodeIDs=[301]
  file 802 series/two.mkv episodeIDs=[301]
  ```

- Failure mode: exact import read-back can claim two conflicting final file
  associations for one episode. That can turn a lost-response reconciliation
  into a false success and makes placement/import review ambiguous.
- Contract: `connectors.md:127-133` requires verified expected file
  associations for the exact episode set and says unsupported mappings remain
  review issues. I-05 keeps inconsistent evidence unknown. A-07/A-10/A-16 use
  this inventory/read-back evidence.
- Required change: index every episode identity across the full response.
  Reject a repeated episode row, especially when it names a different file;
  retain a reason code and return partial/unknown rather than silently
  deduplicating or accepting both associations.
- Required proof: duplicate episode ID with the same file, duplicate episode
  ID with a different file, two episode IDs sharing one byte-equivalent
  multi-episode file, and ordinary distinct episode/file rows. Only the valid
  associations succeed, and inventory plus `ObserveImport` behave the same.
- Disposition: `current_blocker`.

## Round-three finding disposition

| Round-three finding | Round-four result |
| --- | --- |
| Non-overlapping history drift can become complete | Ordinary pinned no-token traversal is fixed and the exact deletion-drift regression passes. The new unsupported-token completeness path keeps the finding open. |
| Native quality/custom-format objects remain untyped | Unknown-only/null objects are rejected before POST. Product-specific schema validation and missing direct video quality remain open. |
| Sonarr nested episode-file contradictions are accepted | Outer/nested ID mismatch, repeated file-ID detail drift, and one path under different IDs are fixed. Repeated episode identity across files remains open. |

## Prior safeguard review

- Full-array catalog digest checks, bounded signed cursors, overlap detection,
  unique counting and late duplicate handling remain in place.
- Every inspected Sonarr episode request still includes `seriesId` and
  `includeEpisodeFile=true`.
- Exact source path, root mapping, download ID, movie/series ID and complete
  episode-set validation remain active for native candidates.
- Duplicate physical source rows are rejected before reprocess serialization.
  One season-pack source still maps to one detailed row with all exact episode
  IDs and anime absolute numbering.
- Explicit caller-supplied IDX/SUB pair IDs remain bound to both files and the
  revision. Same-stem pairing is not inferred.
- Redirect refusal remains installed on the cloned HTTP client. Synthetic GET
  and POST redirects cannot reach `/api/v3/command`.
- Repository and request-log inspection found no Arr command call. X-03 still
  exposes only inventory, observation and preview/reprocess behavior.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product, parent, handoff and prior receipt objects; owned diff | Inspected. Product changes only two owned Arr Go files. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/arr/read ./internal/domain ./internal/ports` | Passed. |
| Round-three adversarial probe suite | Passed; all three earlier reproductions are closed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/arr/read` | Passed in 4.228 seconds. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed; storage completed in 99.935 seconds. |
| Root and focused `GOWORK=off go vet -mod=readonly` | Passed. |
| UI and tools tests/vet with `GOWORK=off -mod=readonly`; tools tidy diff | Passed. |
| Root, UI and tools `GOWORK=off go mod verify` | Passed. |
| `./scripts/generate.sh --check` | Passed. |
| `./scripts/check-api.sh` | Passed; Vacuum quality 100/100. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases and local links resolve. |
| `python3 scripts/check-architecture.py` | Passed. |
| Linux amd64 and arm64 CGO-free Arr adapter compile | Passed. |
| Nineteen owned fixture JSON files | Parsed successfully. |
| Scoped diff/archive identity, gofmt and `git diff --check` | Passed. |
| Public credential/private-path scan | Passed; only a reserved `example.test` credential URL fixture was excluded. |
| Unpinned snapshot deletion-drift probe | Failed the contract: incomplete IDs, complete coverage and no reason. |
| Pinned Sonarr quality plus missing-quality POST probes | Failed the contract: valid Sonarr evidence was rejected and untyped null reached POST. |
| Repeated episode identity probe | Failed the contract: one episode was accepted on two files. |

## Acceptance contribution

- A-07: not accepted. Full-array title coverage and tested file-ID/path
  contradictions pass, but history and episode association evidence can still
  become false completeness/success.
- A-09: accepted for X-03. Connection-scoped IDs, mappings, cursors and
  revisions remain isolated per instance.
- A-10: not accepted. Season-pack and exact source safeguards pass, but a valid
  Sonarr quality is rejected and one episode can map to two files.
- A-11: not accepted for the corrected nested-value contribution. Subtitle and
  anime evidence survives, but the validator does not implement the selected
  product's pinned quality schema.
- A-16: not accepted. Redirect/no-command safeguards pass, but history can
  falsely become complete, direct malformed input can reach reprocessing, and
  contradictory file evidence can become a successful observation.

No product or execution-state file was changed by this review.
