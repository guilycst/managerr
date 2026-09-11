# X-03 independent review, round three

## Decision

`changes_requested`.

Round three adds the required Sonarr episode-file query flag, detects history
overlap and late catalog duplicates, rejects duplicate physical reprocess rows,
provides a callable native-candidate bridge, and retains explicit IDX/SUB pair
identity. Three P1 evidence gaps remain. Mutable history can omit a row without
overlap and still become complete, native quality/custom-format objects are not
typed before being sent back to Arr, and Sonarr file inventory accepts
contradictory nested file identities and paths.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of the X-03 implementer.
- Reviewed product commit:
  `e01deb6d90662e34d67cbd8190daf1c553035217`.
- Product parent: `ece8201b0502f8e4eb7930fd290600e3f7b59ba0`.
- Correction handoff commit:
  `29768b68238cc2023af7149592da7b0bd4fa192a`.
- Round-two review receipt commit:
  `aa63124c60364f6f36103f74d4c529c9e6a2640a`.
- Review scope: `internal/adapters/arr/read/` and
  `tests/fixtures/arr/read/`, against the frozen ports, connector contract,
  pinned Radarr/Sonarr sources and A-07/A-09/A-10/A-11/A-16.
- Product diff contains only `internal/adapters/arr/read/client.go` and
  `client_test.go`.
- Scoped product diff SHA-256:
  `82c0036f0e7c4bafa6c64b0a34ae10e27adc6987d3ae6cf9c593edd6b147b0c6`.
- Scoped product snapshot SHA-256:
  `d5924b456cd9d2c9287c046633460f87cadf3b30ad2e5b1bc90ff090e527a06d`.
- Checks ran in a clean detached reviewer-owned worktree at the exact product
  commit. Adversarial tests ran in disposable Git archive
  `/tmp/managerr-x03-r3-review.yuorry` and were never added to the product.
- No live Arr service, credential, private inventory, media payload, command
  endpoint, filesystem action, release or deployment was used.

## Findings

### P1: non-overlapping history drift can still become complete

- Location: `internal/adapters/arr/read/client.go:475-593`, especially the
  first-page-only `SnapshotRevision` assignment at `:530-532` and terminal
  completeness decision at `:556-584`.
- Evidence: cross-page identity tracking now detects a repeated row, but the
  history endpoint has no snapshot token and the adapter does not validate a
  stable ordering boundary. Offset pagination can lose a row after a preceding
  row is deleted. If the new total equals the number of unique rows collected,
  there is no overlap and no total inconsistency, so coverage becomes complete.
- Independent reproduction:
  1. Page 1 reports IDs `4,3`, page size 2 and total 4.
  2. Delete ID `4` before page 2. The current list is `3,2,1`, so offset page 2
     returns only ID `1` and total 3.
  3. Consume page 2 with the signed continuation cursor.
- Observed result from
  `TestReviewerHistoryDeletionDriftCannotBecomeComplete`:

  ```text
  deletion drift omitted history id 2 but became complete:
  items=[id=1] count=3 reasons=[]
  ```

  IDs `4,3,1` were counted as a complete three-row collection although ID `2`
  was never observed.
- Failure mode: incomplete history can become complete absence evidence during
  normal upstream retention or concurrent deletion. Read-only uncertain-write
  reconciliation can therefore conclude that no relevant event exists.
- Contract: `connectors.md` requires bounded pagination termination;
  `spec-001-media-reconciliation.md` I-05 says partial/stale evidence cannot
  become fresh absence. A-16 requires unsafe import and uncertain native
  outcomes to stay blocked. Round-two required unverifiable page drift to remain
  partial.
- Required change: use a stable immutable history boundary/snapshot token when
  the supported Arr API provides one. If it does not, multi-request history
  traversal cannot claim complete coverage; retain useful observations with an
  explicit partial reason. Overlap and total checks remain useful additional
  evidence.
- Required proof: insertion and deletion before/inside/after the page boundary,
  with changed and unchanged totals, repeated and non-overlapping pages, plus a
  supported stable-boundary success case if one exists. Every unverifiable drift
  sequence must remain partial and count only accepted unique rows.
- Disposition: `current_blocker`.

### P1: native quality and custom-format values remain untyped passthrough JSON

- Location: `ReprocessFile` and outbound DTOs at
  `internal/adapters/arr/read/client.go:779-854`; native mapping at `:1545-1683`;
  POST serialization at `:1006-1099`.
- Evidence: `nativeObject` accepts any non-empty JSON object. `nativeObjects`
  applies only that check to custom formats. The mapper therefore accepts
  `quality: {"junk":true}` and `customFormats: [{"junk":true}]`, stores them
  as `json.RawMessage`, and `ReprocessPreview` sends the same objects to Arr.
  This is not validation against the pinned `QualityModel` and
  `CustomFormatResource` shapes. A `null` array member also passes as an empty
  raw value.
- Independent reproduction:
  1. Return one exact Radarr native candidate with a valid path, movie,
     language and download ID.
  2. Set quality and one custom format to non-empty objects containing no pinned
     fields.
  3. Call `PreviewImportForReprocess`, then pass its returned request to
     `ReprocessPreview`.
- Observed result from
  `TestReviewerNativeNestedFieldsMustBeTypedBeforePOST`:

  ```text
  untyped quality/custom-format objects escaped native mapping and reached POST:
  posts=1 quality={"junk":true} customFormats=[{"junk":true}]
  ```

- Failure mode: the new bridge still permits schema drift or malformed native
  evidence to flow through the reviewed request as arbitrary JSON. The review
  cannot explain the selected quality/custom formats, and a later POST can
  silently replace those inputs with Arr defaults or different evidence.
- Contract: the Arr connector section requires values from the selected
  instance's supported schema and explicit typed product DTOs. Approval binds
  the exact preview revision and scope; A-10/A-11/A-16 require retained native
  evidence rather than invented or malformed values. This is the unclosed
  typed-nested-value part of the round-two finding.
- Required change: replace raw quality/custom-format passthrough with bounded
  typed models for every field used by the pinned reprocess resource, or reject
  candidates whose required typed shape is absent. Serialize only the validated
  typed values. Preserve unknown extra upstream fields only as non-executable
  evidence if needed.
- Required proof: begin from GET candidates with valid pinned objects and prove
  exact POST payload preservation. Mutate each required nested field to absent,
  null, wrong type, invalid ID and unknown-only object; mapping must reject it
  before any POST. Bound array length and nested encoded size.
- Disposition: `current_blocker`.

### P1: Sonarr nested episode-file contradictions are accepted as import evidence

- Location: `internal/adapters/arr/read/client.go:1107-1203` and
  `:1267-1357`.
- Evidence: every episode read now sends `includeEpisodeFile=true`, but
  `episodeFiles` never compares `episodeFileId` with nested `episodeFile.id`.
  It also groups repeated nested file IDs without checking that path and size
  agree across episodes. The first nested object wins while later episode IDs
  are appended to it.
- Independent reproductions against `ObserveImport`:
  1. Episode `301` reports `episodeFileId=801` but nested file ID `802`.
  2. Episodes `301` and `302` both report file ID `801`, while their nested
     objects name different paths and sizes.
- Observed results from
  `TestReviewerSonarrNestedFileEvidenceMustBeConsistent`:

  ```text
  episodeFileId disagrees with nested id:
  accepted externalID=802 path=series/one.mkv episodeIDs=[301]

  same nested file id disagrees on physical path:
  accepted externalID=801 path=series/one.mkv episodeIDs=[301 302]
  ```

- Failure mode: inventory and lost-response read-back can report an exact
  imported file/episode association from internally contradictory upstream
  evidence. For a multi-episode file, a later conflicting path is silently
  attached to the first physical source.
- Contract: the Arr connector requires all registrations/files and exact final
  file associations, with multi-episode files mapped to every exact episode ID.
  Missing or inconsistent evidence stays unknown. A-07/A-10/A-16 depend on this
  read-back. Round-two required the new file-detail query to retain strict
  mismatch checks.
- Required change: when `episodeFileId` is present, require it to equal the
  nested file ID. For every repeated file ID, require the same canonical mapped
  path and file metadata before adding another episode association. Return an
  explicit partial/unknown reason on any contradiction.
- Required proof: inventory and `ObserveImport` tests for outer/nested ID
  mismatch, same ID with path/size mismatch, same path with different IDs, and
  a valid multi-episode file whose repeated nested evidence is byte-equivalent.
  Verify the exact `includeEpisodeFile=true` query remains present.
- Disposition: `current_blocker`.

## Round-two finding disposition

| Round-two finding | Round-three result |
| --- | --- |
| Sonarr inventory/read-back omit file details | Query flag is fixed for both paths. Contradictory returned file details are still accepted, so exact read-back remains open. |
| Overlapping history pages become complete | Repeated identities now produce `history_overlap` and unique counts. Non-overlapping mutable-page drift still becomes complete. |
| Catalog duplicate guard ends after 2,048 IDs | Closed. Signed cursor keeps a bounded membership filter after the exact prefix; a late duplicate is suppressed and coverage stays partial. False-positive hits are conservative. |
| Reprocessing duplicates one physical source | Closed. Generic reprocess validation keys only canonical root/path and runs before serialization or POST. One canonical season-pack row carries the complete episode set. |
| Native state cannot flow into reprocessing | Callable network and persisted-body bridges now retain native path/ID, download, episode/anime, language, release and score evidence. Quality/custom-format nested values remain arbitrary raw objects and keep this finding open. |
| IDX/SUB fixture does not prove pair preservation | Closed for X-03. Explicit caller-supplied pair IDs bind both retained candidates and revision; unpaired same-stem input gains no inferred ID, and a missing member is rejected. |

## Additional invariant review

- Full-array inventory compares the complete response digest on every local
  continuation. Identity tracking switches from an exact prefix to a signed
  32 KiB membership filter. Duplicate hits, including conservative false
  positives, make terminal coverage partial.
- History records accepted unique IDs across pages and propagates
  `history_overlap` and inconsistent-total reasons. The first finding identifies
  the remaining no-snapshot drift boundary.
- Reprocess validation rejects duplicate physical root/path rows before any
  POST, independently of movie/episode IDs or download metadata.
- Native preview mapping retains one detailed Sonarr row for a season pack and
  its complete exact episode set, including absolute and scene-absolute numbers.
- Explicit subtitle pair IDs survive mapping and revision binding. Pairing is
  not inferred from a same-stem filename.
- Every Sonarr episode request inspected in committed tests includes
  `seriesId` and `includeEpisodeFile=true`.
- HTTP redirect refusal remains installed on the cloned client. Synthetic 307
  GET and POST responses do not reach `/api/v3/command`.
- Repository search and fixture request logs show no Arr command endpoint call.
  The only POST remains typed manual-import reprocessing; native execution stays
  outside this adapter and capability remains unknown for X-05.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product, parent, handoff and prior receipt objects; owned diff | Inspected. Product changes only two owned Arr Go files. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/arr/read ./internal/domain ./internal/ports` | Passed. |
| Focused seven round-three regression tests | Passed, including overlap, late duplicate, duplicate source, native mapping, anime/season pack, query flag and redirect safety. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/arr/read` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed; storage completed in 86.273 seconds. |
| Root and focused `GOWORK=off go vet -mod=readonly` | Passed. |
| UI and tools tests/vet with `GOWORK=off -mod=readonly`; tools tidy diff | Passed. |
| Root, UI and tools `GOWORK=off go mod verify` | Passed. |
| `./scripts/generate.sh --check` | Passed. |
| `./scripts/check-api.sh` | Passed; Vacuum quality 100/100. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases and local links resolve. |
| `python3 scripts/check-architecture.py` | Passed. |
| Linux amd64 and arm64 CGO-free adapter test compile | Passed; both outputs are statically linked ELF binaries. |
| Nineteen owned fixture JSON files | Parsed successfully. |
| Scoped diff/archive identity, gofmt and `git diff --check` | Passed. |
| Public credential/private-path scan | Passed; only the documented synthetic fixture API key appears in test code. |
| Mutable history deletion-drift probe | Failed contract: omitted row, complete coverage, no reason. |
| Untyped nested native-field probe | Failed contract: arbitrary objects reached POST. |
| Contradictory Sonarr nested-file probes | Failed contract: both inconsistent shapes became successful observations. |

## Acceptance contribution

- A-07: not accepted. Full-array title coverage and late duplicate handling pass,
  but Sonarr file inventory can accept contradictory nested file evidence.
- A-09: accepted for X-03. Connection-scoped identities, mappings, cursors and
  revisions remain isolated per instance.
- A-10: not accepted. Duplicate physical reprocess rows and native anime/season
  mapping are corrected, but inconsistent multi-episode file evidence can still
  be accepted.
- A-11: accepted for this contribution. Native typed languages, explicit
  subtitle labels and supplied IDX/SUB pair identity survive the product-specific
  preview bridge and revision; missing forced/SDH evidence remains an explicit
  rejection/unknown.
- A-16: not accepted. Redirect/rejection behavior and the Sonarr detail query
  pass, but history can falsely become complete and read-back can accept
  contradictory file evidence. Untyped nested reprocess values also remain open.

No product or execution-state file was changed by this review.
