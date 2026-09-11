# X-03 independent review, round five

## Decision

`changes_requested`.

The exact three round-four reproductions now pass. Unsupported snapshot-shaped
history fields cannot certify a multi-request traversal, valid pinned Sonarr
quality is accepted while the tested missing video quality is rejected, and one
episode identity on two different files returns unknown. Three P1 findings and
one P2 finding remain. A caller-controlled subtitle flag can still exempt an MKV
from typed quality validation, Sonarr accepts a Radarr-only nested custom-format
field, catalog and import observations can attach files to the wrong title, and
an exact duplicate Sonarr episode row is silently accepted.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of the X-03 implementer.
- Reviewed product commit:
  `5a4ceaac7307f264cdfacca126a88f319d76a842`.
- Product parent:
  `020e8bf3b690f638424cdcc207a733a47998bc96`.
- Product tree:
  `ef985a1aa04cf0fdead5eb17866b883a2b360aca`.
- Correction handoff commit:
  `58a5ecb80f5df7352be84cc6d7b33efdb5673328`.
- Round-four review receipt:
  `51113716a3b9fccc20df26a097d634f758b78704`.
- Review scope: `internal/adapters/arr/read/` and
  `tests/fixtures/arr/read/`, against the frozen ports, connector contract,
  pinned Radarr/Sonarr schemas, and A-07/A-09/A-10/A-11/A-16.
- The product commit changes only
  `internal/adapters/arr/read/client.go` and `client_test.go`.
- Scoped product diff SHA-256:
  `7c6f6a27fc9ef4e10622c71a0b03649516c847ae74cd4f85a72671f3cfe1ddce`.
- Scoped product snapshot SHA-256:
  `79b28b56b13c4d4b06732c64a97acc41bbac04e66732b0fcacfe7445151d9377`.
- Checks ran in a clean detached reviewer-owned worktree at the exact product
  commit. Independent adversarial tests ran in disposable Git archive
  `/tmp/managerr-x03-r5-review.X3lZI1` and were not added to the product.
- Pinned schema inspection used the exact public source commits recorded in
  `docs/research/compatibility-matrix.md`.
- No live Arr service, credential, private inventory, media payload, command
  endpoint, filesystem action, release, or deployment was used.

## Findings

### P1: the subtitle flag bypasses typed quality for a video path

- Location: `internal/adapters/arr/read/client.go:1037-1079`, `:2343-2360`, and
  `:2679-2705`.
- Evidence: native candidate mapping checks the requested role against the
  native path extension, but exported `ReprocessPreview` does not. Its missing
  quality exception trusts only `ReprocessFile.Subtitle`. A direct caller can
  set `Subtitle=true` and `NativeSubtitle=false` on `incoming/movie.mkv`; the
  validator exempts the row and serializes a manual-import POST with null
  quality.
- Independent reproduction:
  1. Construct a Radarr reprocess row for root-relative
     `incoming/movie.mkv`, movie ID `101`, no quality, `Subtitle=true`, and
     `NativeSubtitle=false`.
  2. Call exported `ReprocessPreview` and count POST requests to
     `/api/v3/manualimport`.
- Observed result from `TestReviewerSubtitleFlagCannotBypassVideoQuality`:

  ```text
  caller-controlled subtitle flag let an MKV with no typed quality reach POST:
  posts=1
  ```

- Failure mode: a caller can relabel a video row as a subtitle and bypass the
  exact missing-quality gate that round four required on every callable path.
  The adapter already recognizes `.mkv` as non-subtitle evidence when mapping a
  native candidate, so the direct path applies a weaker rule to the same
  authority-bearing fields.
- Contract: `spec-001-media-reconciliation.md:106-112` distinguishes common
  video containers from supported subtitle formats.
  `connectors.md:93-111` requires selected-instance typed quality and explicit
  manual-import DTO validation. A-10/A-11/A-16 require exact video/subtitle
  evidence and rejection before POST.
- Required change: bind the subtitle exception to validated immutable file-role
  evidence. On the direct path, check the exact source role/path and any retained
  native role before allowing missing quality. A caller-provided boolean alone
  cannot grant the exception.
- Required proof: `.mkv` with `Subtitle=true`, missing quality, and absent or
  false native subtitle evidence produces invalid input and zero POSTs. A
  supported subtitle path with an explicitly approved subtitle role retains the
  documented exception. Exercise both Radarr and Sonarr and both direct and
  native-derived requests.
- Disposition: `current_blocker`.

### P1: Sonarr accepts a Radarr-only nested custom-format field

- Location: `internal/adapters/arr/read/client.go:1781-1787`, `:1889-1931`, and
  `:2010-2077`.
- Evidence: `validateCustomFormatForKind` rejects an unknown product kind, but
  does not pass the selected kind into specification, field, or select-option
  validation. The shared select-option allowlist always accepts
  `dividerAfter`. The pinned Radarr `SelectOption` schema defines that property;
  the pinned Sonarr schema does not and has `additionalProperties=false`.
- Pinned sources:
  [Radarr v3 OpenAPI at `0220f0da`](https://github.com/Radarr/Radarr/blob/0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4/src/Radarr.Api.V3/openapi.json)
  and
  [Sonarr v3 OpenAPI at `9c0d8deb`](https://github.com/Sonarr/Sonarr/blob/9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb/src/Sonarr.Api.V3/openapi.json).
- Independent reproduction:
  1. Build a valid Sonarr video reprocess row and quality object.
  2. Add a typed custom format whose nested
     `specifications[].fields[].selectOptions[]` contains the Radarr-only
     `dividerAfter=true` property.
  3. Call exported `ReprocessPreview` and count Sonarr manual-import POSTs.
- Observed result from
  `TestReviewerSonarrRejectsRadarrOnlyCustomFormatField`:

  ```text
  Radarr-only CustomFormat SelectOption.dividerAfter reached Sonarr POST:
  posts=1
  ```

- Failure mode: unsupported fields from one Arr product can cross the selected
  product's supposedly typed boundary and are copied losslessly into its POST.
  Dispatching only at the top-level function does not make nested validation
  product-specific.
- Contract: `connectors.md:93-111` requires quality and related native values
  from the selected instance's current supported schema and explicit DTOs
  verified against the pinned product schema. A-10/A-11/A-16 require unsafe
  native or direct preview input to stop before POST.
- Required change: carry `ConnectionKind` through every nested custom-format
  validator and maintain product-specific field allowlists wherever the pinned
  schemas differ. Sonarr must reject `dividerAfter`; Radarr may accept it with
  its pinned boolean type.
- Required proof: compare every differing nested field in the pinned Radarr and
  Sonarr custom-format graphs. Each product accepts its own complete value,
  rejects the other's exclusive fields with zero POSTs, and preserves the exact
  accepted body from both native mapping and direct reprocessing.
- Disposition: `current_blocker`.

### P1: file ownership can be rewritten onto the wrong movie or series

- Location: `internal/adapters/arr/read/client.go:1167-1198`, `:1243-1326`,
  `:1329-1345`, and `:2963-2984`.
- Evidence: `movieFileDTO` omits the pinned `movieId` field. The fallback
  `/moviefile?movieId=...` response is therefore accepted even when its own
  `movieId` contradicts the requested movie, and `movieFile` writes the parent
  movie ID into the result. The Sonarr observation path validates every
  `episode.seriesId`, but catalog `decodeRecord` sends episode rows directly to
  `episodeFiles`, which never receives or checks the expected series ID.
- Independent reproductions:
  1. Observe movie `101`; return that movie without an embedded file, then
     return file `801` with `movieId=999` from the filtered movie-file request.
  2. List catalog series `201`; return episode `301` with `seriesId=999` and a
     mapped file from the filtered episode request.
- Observed results:

  ```text
  movie file owned by movie 999 satisfied observation for movie 101:
  file=801 reported MovieID=101

  episode from series 999 was attached to catalog series 201 with complete
  coverage: file=801 episodeIDs=[301]
  ```

- Failure mode: a filter in the request is treated as stronger evidence than
  contradictory identity in the response. The adapter can report a successful
  import observation or complete tracked catalog for the wrong title, which is
  unsafe reconciliation evidence after a lost write response.
- Contract: `connectors.md:127-133` requires verified expected movie or exact
  episode associations. I-05 requires inconsistent evidence to remain unknown.
  A-07/A-10/A-16 rely on title-scoped file inventory and read-back evidence.
- Required change: decode the pinned `movieId` on movie-file resources and
  validate it against the parent before assigning any file. Pass the expected
  series ID into catalog episode decoding and require every present episode
  identity to match it. Missing, malformed, or contradictory ownership remains
  partial/unknown with a stable reason code.
- Required proof: for embedded and fallback Radarr files and for Sonarr catalog
  and observation paths, test matching, missing, malformed, and contradictory
  parent identities. No contradictory row appears under a title or produces
  successful observation/complete coverage.
- Disposition: `current_blocker`.

### P2: an exact duplicate Sonarr episode row is silently accepted

- Location: `internal/adapters/arr/read/client.go:1348-1423`.
- Evidence: `episodeIndex` emits `episode_identity_conflict` only when a repeated
  episode ID names a different file ID. When the same episode ID, file ID, path,
  and size appear twice, `contains` silently deduplicates the second row and no
  reason is retained. The response then becomes a successful import
  observation.
- Independent reproduction: return episode `301` twice with file `801`, the
  same mapped path, and the same size, then observe series `201`.
- Observed result from
  `TestReviewerRepeatedSonarrEpisodeRowCannotProveCompleteAssociation`:

  ```text
  repeated episode row was silently deduplicated into a successful observation:
  file=801 episodeIDs=[301]
  ```

- Failure mode: the adapter hides an invalid repeated upstream identity and
  reports normal association evidence. This leaves the round-four required
  same-file duplicate proof unmet even though the more dangerous different-file
  conflict is fixed.
- Contract: `connectors.md:127-133` requires verified exact episode
  associations; I-05 keeps inconsistent collection evidence visible. The
  round-four receipt required same-file and different-file duplicate cases to
  be distinguished from a valid multi-episode file.
- Required change: track whether each episode ID has already appeared. Emit a
  stable duplicate reason and return partial/unknown for a repeated episode row,
  while continuing to allow distinct episode IDs on one byte-equivalent file.
- Required proof: exact same-row duplicate, same episode on different files,
  distinct episodes on one file, and ordinary distinct rows. Both inventory and
  `ObserveImport` must preserve the same classification.
- Disposition: `current_blocker`.

## Round-four finding disposition

| Round-four finding | Round-five result |
| --- | --- |
| Unsupported snapshot-shaped fields certify history drift | Closed for the exact reproduction. All three unsupported key names remain partial and retain `history_snapshot_unsupported`; ordinary multi-request history retains `history_snapshot_unverified`. |
| Product-neutral quality plus missing direct video quality | Valid pinned Radarr/Sonarr quality and the unflagged missing-quality case pass. Caller-controlled role and nested custom-format schema bypasses keep this finding open. |
| One Sonarr episode identity appears on two files | Closed for different file IDs and paths. Exact duplicate rows remain silently accepted instead of becoming review evidence. |

## Prior safeguard review

- Multi-request Arr history remains partial without a documented immutable
  boundary. Insertion/deletion drift, overlap, totals, unique counts, bounded
  cursors, and unsupported snapshot-shaped fields remain covered.
- Full-array catalog digest checks and current connection-scoped cursor binding
  remain in place.
- Manual-preview exact source path, root mapping, download ID, title ID, episode
  set, native rejection, and duplicate physical source checks remain active.
- Every inspected Sonarr episode request includes `seriesId` and
  `includeEpisodeFile=true`; the catalog response-identity gap is reported above.
- Season-pack, multi-episode, anime absolute-numbering, subtitle metadata, and
  explicit IDX/SUB pairing evidence remain retained.
- Redirect refusal remains installed on the cloned HTTP client. Synthetic GET
  and POST redirects cannot reach `/api/v3/command`.
- Product and request-log inspection found no Arr command call or title/file
  mutation. X-03 remains inventory, observation, and preview reprocessing only.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product, parent, tree, handoff, prior receipt, and owned diff | Inspected; product changes only the two assigned Arr Go files. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/arr/read ./internal/domain ./internal/ports` | Passed. |
| Exact round-three and round-four adversarial probe suites | Passed; all previously failing exact reproductions are closed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/arr/read` | Passed in 4.909 seconds. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed; storage completed in 120.465 seconds. |
| Root and focused `GOWORK=off go vet -mod=readonly` | Passed. |
| UI and tools tests/vet with `GOWORK=off -mod=readonly`; tools tidy diff | Passed. |
| Root, UI, and tools `GOWORK=off go mod verify` | Passed. |
| `./scripts/generate.sh --check` | Passed. |
| `./scripts/check-api.sh` | Passed; Vacuum quality 100/100. |
| `./scripts/check-lint.sh` | Passed with zero issues in root and UI; architecture passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, and local links resolve. |
| `python3 scripts/check-architecture.py` | Passed. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| Linux amd64 and arm64 CGO-free Arr adapter compile | Passed; both outputs are statically linked ELF binaries. |
| Nineteen owned fixture JSON files | Parsed successfully. |
| Scoped diff/archive identity, gofmt, `git diff --check`, and clean status | Passed before receipt creation. |
| Public credential/private-path scan | Passed; matches are field names and synthetic `fixture-api-key`/`example.test` values only. |
| Unsupported snapshot, pinned quality, missing unflagged quality, and episode-on-two-files probes | Passed. |
| Caller-controlled subtitle quality probe | Failed the contract: one MKV without typed quality reached POST. |
| Sonarr nested custom-format schema probe | Failed the contract: Radarr-only `dividerAfter` reached POST. |
| Cross-title Radarr observation and Sonarr catalog probes | Failed the contract: wrong-title files became successful/complete evidence. |
| Exact duplicate Sonarr episode probe | Failed the contract: the duplicate became a successful observation. |

## Acceptance contribution

- A-07: not accepted. History coverage is conservative now, but complete Sonarr
  catalog evidence can attach an episode file from another series.
- A-09: accepted for X-03 connection and cursor scoping. The reported ownership
  gap is within one selected connection and does not cross connection IDs.
- A-10: not accepted. A video role can bypass typed quality and wrong-title or
  repeated episode evidence can appear successful.
- A-11: not accepted. Subtitle/anime/season-pack evidence survives, but the
  subtitle quality exception and Sonarr nested schema boundary are not safe.
- A-16: not accepted. Redirect and no-command safeguards pass, but unsupported
  values can reach preview reprocessing and contradictory file evidence can
  become complete/successful observation.

No product or execution-state file was changed by this review.
