# X-03 independent review, round two

## Decision

`changes_requested`.

Product commit `b77aa5275a632246b76b009722719bc56e3f0448`
corrects ordinary full-array paging, bounded-history terminal conditions, exact
candidate paths, title and episode-set validation, semantic revision binding,
redirect refusal, and several missing fixtures. It still cannot supply complete
Sonarr file inventory or import read-back against the pinned endpoint. History
and large catalog collections can still produce false complete coverage.
Reprocessing can POST the same physical source twice, and no callable preview
surface retains the native candidate fields needed to construct the new
reprocess DTO without invention. The IDX/SUB test observes two filenames but
does not retain or prove their pair association.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of the X-03 implementer.
- Reviewed product commit:
  `b77aa5275a632246b76b009722719bc56e3f0448`.
- Product parent: `cba10b7a9f1a74475ce09c17d2e61f5683798027`.
- Correction handoff commit:
  `58ab8f6ce567e9baebad4b59030b6d1ce18177de`.
- Prior review receipt:
  `cf1d984a29be7b677aef2611b636e97a23aaee67`.
- Review scope: `internal/adapters/arr/read/` and
  `tests/fixtures/arr/read/`, against the frozen ports, connector contract,
  pinned Radarr/Sonarr sources and A-07/A-09/A-10/A-11/A-16.
- Scoped product snapshot:
  `9c1a593b2dfd44abd359ec4f7ea0ba91012164fe227f24ab6a4c1e725a4898ae`.
- Checks ran from a clean detached reviewer worktree at the exact product
  commit. Adversarial probes ran in a disposable archive of that commit and
  were not added to the product.
- No live Arr service, credential, private inventory, media payload, command
  endpoint, filesystem action, release or deployment was used.

## Findings

### P1: Sonarr inventory and import observation omit required file details

- Location: `internal/adapters/arr/read/client.go:1005-1036` and
  `:1110-1130`.
- Evidence: both Sonarr episode reads call
  `GET /api/v3/episode?seriesId=...` without `includeEpisodeFile=true`.
  The pinned Sonarr `EpisodeController.GetEpisodes` declares
  `includeEpisodeFile = false`; its mapper therefore does not include the
  nested `episodeFile` object by default. The adapter sees a non-empty
  `episodeFileId` with no `episodeFile` and records
  `episode_file_details_missing`. Committed fixtures always embed the object
  and do not model the pinned default.
- Reproduction: reviewer probe
  `TestReviewerSonarrObservationRequestsEpisodeFileDetails` returned
  `outcome_unknown: required import evidence is incomplete:
  episode_file_details_missing`. Its server exposes the nested object only
  when the documented query flag is true.
- Failure mode: every file-bearing Sonarr series is partial in inventory, and
  `ObserveImport` cannot confirm an imported episode file. Lost-response
  reconciliation therefore cannot reach a successful read-back against the
  pinned API.
- Contract: `connectors.md:84-91` requires all registrations and files;
  `connectors.md:127-133` requires exact final file associations. A-07 and
  A-16 depend on those observations.
- Required change: request `includeEpisodeFile=true` for Sonarr episode reads
  that map files, then retain the current strict checks for absent, malformed,
  mismatched, capped and unmapped details.
- Required proof: use a pinned-shape response where the nested file appears
  only with the flag, for both catalog inventory and `ObserveImport`; assert
  the exact query and final mapped file/episode set.
- Disposition: `current_blocker`.

### P1: overlapping history pages can become complete absence evidence

- Location: `internal/adapters/arr/read/client.go:465-571`.
- Evidence: history counts every decoded row but keeps no record identity set
  and detects no overlap. Two one-row pages both returned history ID `1`, with
  a stable `totalRecords=2`. The terminal page reported
  `Completeness=complete`, `ObservedCount=2`, and no reason even though only one
  unique event was observed.
- Reproduction: reviewer probe
  `TestReviewerHistoryOverlapCannotBeComplete` failed with a complete coverage
  object whose reason list was empty.
- Failure mode: pagination drift or a repeated page can omit an event while
  authorizing conclusions that depend on complete history absence.
- Contract: missing evidence is unknown; complete coverage is required before
  absence evidence may be used. This is the same completeness boundary as the
  round-one history finding and A-16 reconciliation.
- Required change: bind history continuation to stable ordering/snapshot
  evidence where supported, track unique history identities across pages, and
  mark overlap, duplicate or unverifiable page drift partial. `ObservedCount`
  must count accepted unique records.
- Required proof: cover overlapping pages, an exact repeated page, insertion
  drift, stable distinct pages and cap/lying-total cases. No overlap case may
  complete without an explicit partial reason.
- Disposition: `current_blocker`.

### P1: the catalog duplicate guard stops after 2,048 identities

- Location: `internal/adapters/arr/read/client.go:39`, `:286-324`, and
  cursor state at `:1841-1864`.
- Evidence: `SeenIDs` is persisted only while its length is below
  `maxCursorSeenIDs` (`2048`). A stable 2,200-record full array placed ID 2049
  on local page 21 and repeated it on page 22. Both occurrences were accepted;
  the terminal page reported complete coverage with no overlap reason and an
  observed count equal to the raw array length.
- Reproduction: reviewer probe
  `TestReviewerCatalogDuplicateBeyondCursorCapCannotBeComplete` failed with
  `late duplicate produced complete coverage without overlap evidence: []`.
- Failure mode: the advertised default `MaxRecords=10000` exceeds the identity
  evidence retained by the cursor. A malformed or drifting large catalog can
  omit one distinct title and still establish complete absence.
- Contract: A-07 requires all catalog records. The product invariant forbids
  turning missing evidence into untracked/absent state.
- Required change: make the cursor/snapshot scheme prove uniqueness across the
  entire configured record bound, or mark coverage partial once exact identity
  evidence can no longer be retained. Do not silently stop tracking at 2,048.
- Required proof: consume stable arrays with duplicates before, across and
  after the current threshold, including a catalog up to `MaxRecords`; assert
  unique counts and partial overlap evidence.
- Disposition: `current_blocker`.

### P1: reprocessing allows duplicate DTO rows for one physical source

- Location: `internal/adapters/arr/read/client.go:839-942`,
  `:1473-1517`, and `:1714-1762`.
- Evidence: `rejectDuplicateReprocessIdentities` keys a row by
  root/path plus `MovieOrEpisodeID`. Two Sonarr rows for the same source path
  with IDs 301 and 302 therefore pass validation, even when both carry the
  same exact episode set. The adapter serializes both rows and performs the
  POST. Its source-keyed episode/download maps then merge or overwrite those
  duplicate inputs after the request has already escaped.
- Reproduction: reviewer probe
  `TestReviewerReprocessRejectsDuplicatePhysicalSourceBeforePOST` observed one
  POST and nil error for two rows targeting `series/pack.mkv`.
- Failure mode: one season-pack file can be reprocessed twice, with
  contradictory candidate state or download IDs. This expands the reviewed
  physical-file scope and makes response association ambiguous.
- Contract: approval binds exact file and episode scope; the round-one
  correction explicitly required duplicate file identities to be rejected.
  A-10 and A-15 require exact season-pack associations and changed mappings to
  invalidate review.
- Required change: reprocessing must have exactly one row per normalized
  `FileTarget`; that row carries the canonical complete episode set. Reject
  duplicate physical sources before any POST and reject conflicting download,
  season, language or native candidate fields.
- Required proof: assert zero upstream calls for duplicate same-path rows with
  different media IDs, episode sets and download IDs; prove one canonical
  multi-episode/season-pack row succeeds.
- Disposition: `current_blocker`.

### P1: native candidate state cannot flow losslessly into reprocessing

- Location: `internal/adapters/arr/read/client.go:599-634`, `:637-756`,
  `:839-942`, and `:1473-1489`; shared result at
  `internal/ports/ports.go:189-210`.
- Evidence: GET manual-import decodes `RadarrManualImportResource` or
  `SonarrManualImportResource` only inside `mapPreviewResponseFor`, then
  returns `ports.ImportPreview.Files`. That type retains path, one media ID and
  subtitle labels only. It cannot carry quality, native languages, release
  group, custom formats/score, indexer flags, season, release type, full
  episodes or anime absolute numbers. Repository search finds no builder or
  callable preview method that returns a `ReprocessFile`; only tests manually
  construct it. `Quality`, response `Languages` and `CustomFormats` also remain
  unvalidated `json.RawMessage` rather than complete bounded DTOs.
- Failure mode: a caller cannot implement the documented two-step native
  workflow by taking the reviewed GET candidate and reprocessing it. It must
  omit, guess, or independently reconstruct fields the POST is supposed to
  preserve. The committed anime test proves fields exist by decoding the raw
  fixture separately, not that application code receives them.
- Contract: `connectors.md:93-111` requires selected current schema values and
  exact typed per-product DTOs. The round-one required change was to derive the
  complete POST resource from the native candidate. A-10/A-11 require selected
  associations and labels to survive review.
- Required change: expose or durably retain a bounded native candidate model
  and provide a deterministic conversion to one exact reprocess row. Validate
  the typed nested quality/language/custom-format shapes and bind every retained
  field to the returned revision.
- Required proof: start from public GET preview output, select/edit it through
  the supported API, produce the exact pinned Radarr/Sonarr POST payload, and
  prove quality, languages, episode/absolute numbering, release and score
  fields survived without test-only reconstruction.
- Disposition: `current_blocker`.

### P2: the subtitle fixture does not prove IDX/SUB pair preservation

- Location: `internal/adapters/arr/read/client_test.go:686-715`,
  `tests/fixtures/arr/read/radarr-subtitle-evidence.json`, and
  `internal/ports/ports.go:189-210`.
- Evidence: the fixture contains one `.idx` and one `.sub`; the assertion only
  checks that four independent files were accepted and three were rejected.
  Neither the request nor response model carries a companion/pair identity, so
  the test cannot tell a preserved IDX/SUB pair from two unrelated subtitles
  that share a basename. The unmatched SRT rejection and forced/SDH unknown
  blockers are correctly represented.
- Failure mode: X-03 can claim A-11 evidence while silently losing the pair
  relation needed by later exact review and companion outcomes.
- Contract: A-11 requires IDX/SUB pair labels to be preserved with no silent
  discard or scope expansion; `spec-001-media-reconciliation.md:106-112`
  treats paired IDX/SUB and unmatched companions explicitly.
- Required change: retain the already-reviewed companion association through
  preview, or explicitly mark the pair relation outside X-03 and leave this
  contribution partial. Do not infer a pair from basename during Arr mapping.
- Required proof: pass an explicit synthetic pair identity through the public
  preview result, plus same-stem unpaired and unmatched controls.
- Disposition: `current_blocker`.

## Round-one finding disposition

| Round-one finding | Round-two result |
| --- | --- |
| Full-array catalog loses its tail | Ordinary below/equal/above-page arrays are corrected. Completeness still fails after the 2,048-ID evidence cap. |
| History cap becomes complete | Page, record and lying-total caps are corrected. Cross-page overlap still becomes complete. |
| Failed import read-back becomes empty success | Radarr failures and strict empty/mismatch cases are corrected. Pinned Sonarr reads omit the file-detail flag and cannot succeed for file-bearing episodes. |
| Basename fallback crosses directories | Corrected. Absolute/relative exact paths, Windows separators, prefix lookalikes and ambiguity are covered. |
| Sonarr wrong series/incomplete episode set accepted | Corrected for one physical candidate. Nested series, exact sets, duplicates and reorder handling are covered. |
| Sonarr POST drops required native DTO fields | Field names were added, but the reviewed GET candidate cannot supply them to callers and nested payloads remain raw/unvalidated. Duplicate physical DTO rows are also emitted. |
| Revision omits subtitle/source scope | Corrected. Connection/config, download, path, transfer, subtitle labels, episode sets and native reprocess fields affect canonical revisions. |
| Redirect can reach command | Corrected. The cloned client rejects every redirect; synthetic 307 GET/POST cannot reach `/api/v3/command`. |
| Subtitle/anime/season-pack fixtures absent | Fixtures now exist and forced/SDH absence stays unknown. Anime/release fields are inspected only in a raw test object, and IDX/SUB pair identity is not retained. |

## Verification

| Check | Result |
| --- | --- |
| Exact product/handoff/prior-receipt Git objects and full owned diff | Inspected independently; product changes only the two owned adapter files and six synthetic fixtures. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/arr/read ./internal/domain ./internal/ports` | Exit 0. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/arr/read` | Exit 0. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Exit 0. |
| Root and focused `GOWORK=off go vet -mod=readonly` | Exit 0. |
| Root, UI and tools `GOWORK=off go mod verify` | Exit 0. |
| UI and tools tests/vet with `GOWORK=off -mod=readonly` | Exit 0. |
| `./scripts/generate.sh --check` | Exit 0. |
| `./scripts/check-api.sh` | Exit 0; Vacuum quality 100/100. |
| `./scripts/check-guardrails.sh --fast` | Exit 0. |
| `python3 scripts/check_planning.py` | Exit 0; 38 tasks and 60 acceptance cases, local links resolve. |
| `python3 scripts/check-architecture.py` | Exit 0. |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=readonly -c ...` | Exit 0; produced a statically linked x86-64 ELF test binary. |
| Owned fixture JSON parse, gofmt check, scoped `git diff --check`, public secret/path scan | Exit 0; no findings. |
| Four disposable adversarial regression probes | Failed as described: false complete history, false complete large catalog, missing Sonarr file-detail flag, and duplicate-source POST. |

The first Linux attempt used `go test` without `-c`; compilation succeeded but
the macOS host could not execute the Linux binary (`exec format error`). The
compile-only rerun above is the applicable cross-platform result.

## Acceptance contribution

- A-07: not accepted. Ordinary title paging works, but Sonarr file inventory
  lacks the pinned detail flag and large duplicate catalogs can be falsely
  complete.
- A-09: accepted for X-03. Connection validation, runtime identities, cursors,
  revisions and returned coverage remain instance-scoped.
- A-10: not accepted. Exact normal episode sets are validated, but duplicate
  physical reprocess rows and discarded native anime/season fields leave the
  reviewed season-pack workflow incomplete.
- A-11: not accepted. Language and forced/SDH unknown evidence improved, but
  native fields cannot flow into reprocessing and IDX/SUB pairing is not
  represented.
- A-16: not accepted. Redirect and rejection checks improved, but Sonarr
  read-back remains unavailable and false-complete history cannot support safe
  uncertain-write reconciliation.

No product or execution-state file was changed by this review.
