# X-03 independent review, round one

## Decision

`changes_requested`.

Product commit `f12fe75568b263c285fe673598e8b97f7333634c` adds a
bounded, connection-scoped Arr client with typed inventory, lookup, options,
history and manual-import preview models. Direct requests use only read routes
and `POST /api/v3/manualimport`, and native import execution remains
`unknown`. Nine correctness boundaries remain open. The catalog algorithm
does not match the pinned Arr full-array endpoints, history can become complete
after a cap, import read-back can turn unavailable evidence into an empty
success, candidate matching is not path-exact, Sonarr reprocessing does not
validate the exact series/episode selection or send the pinned DTO shape,
preview revisions omit meaningful subtitle fields, redirects can escape the
manual-import route, and the claimed subtitle/anime fixture coverage is absent.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of the X-03 implementer.
- Reviewed product commit:
  `f12fe75568b263c285fe673598e8b97f7333634c`.
- Product parent: `b5e6907cca3ace1891255c3aa64002c3ec3600e4`.
- Worker handoff commit:
  `93197292c5c186faa180edc64ecf3c522ffdeb7b`.
- Review scope: `internal/adapters/arr/read/` and
  `tests/fixtures/arr/read/`, against the frozen ports, connector contract,
  compatibility evidence and A-07/A-09/A-10/A-11/A-16.
- Scoped product snapshot:
  `474770195f7de03f4f37233dc3edf7e0d9f87ca63277e3e0e220e76ede9fadab`.
- Checks ran from a clean detached reviewer-owned worktree at the exact product
  commit. Its host path is omitted from this public receipt.
- Producer conclusions were not accepted as evidence. Git objects, complete
  source and fixture inspection, pinned upstream sources, repository checks and
  synthetic adversarial HTTP probes supplied the evidence below.
- No live Arr service, credential, private inventory, media file, upstream
  write, filesystem mutation, release or deployment was used.

## Findings

### P1: pinned Arr catalog endpoints lose every record after the first local page

- Location: `internal/adapters/arr/read/client.go:223-246` and `:272-318`,
  with the array heuristic at `:1573-1585`.
- Evidence: the pinned Radarr `AllMovie` and Sonarr `AllSeries` controllers
  return one complete `List` and have no `page` or `pageSize` parameters. The
  adapter sends those ignored parameters, truncates the returned array to the
  requested limit, then refetches the same array. A synthetic fixed three-title
  response with limit two returned IDs 1 and 2 on page one, no items on page
  two, and never returned ID 3. Final coverage was partial with
  `catalog_page_exceeded_limit`, `catalog_overlap` and `catalog_page_limit`.
- Failure mode: a normal Arr instance with more titles than the API page limit
  cannot satisfy A-07. The omitted tail is never represented, regardless of
  how many continuations the caller consumes.
- Contract: `connectors.md:84-91` requires every Arr registration and file;
  A-07 requires all records when the catalog exceeds one application page.
- Required change: model movie/series inventory as a bounded full-array source.
  Page locally over a stable, connection-scoped snapshot or refetch with a
  validated unchanged digest and a cursor offset. A cap or changed snapshot
  must remain partial; do not invent upstream pagination parameters.
- Required proof: exercise fixed full arrays below, equal to and above the
  requested limit for both Radarr and Sonarr. Consume every cursor, assert every
  ID exactly once, and prove a response above `MaxRecords` is partial.
- Disposition: `current_blocker`.

### P1: a history page cap is reported as complete while upstream records remain

- Location: `internal/adapters/arr/read/client.go:456-505`.
- Evidence: a synthetic paginated history reported `totalRecords=10`, one
  record per page and `MaxPages=2`. After page two, line 487 forced `hasMore`
  false without adding a reason. Lines 498-502 returned
  `Completeness=complete`, no cursor, no reason and `ObservedCount=2`, although
  eight records remained.
- Failure mode: incomplete history can become complete absence evidence. That
  can erase provenance or let read-before-write reconciliation conclude that
  an Arr effect did not happen.
- Contract: the product invariant says missing evidence is unknown, not
  untracked. The compatibility matrix requires capped history to remain
  unknown, and complete coverage is the prerequisite for absence evidence.
- Required change: distinguish natural terminal pages from `MaxPages` and
  `MaxRecords`. Add explicit partial reasons for both limits, enforce the
  remaining-record budget before returning items, and never set `CompletedAt`
  as complete while upstream says more records exist.
- Required proof: cover page and record caps, lying totals, exact-boundary
  totals, empty terminal pages and normal final pages.
- Disposition: `current_blocker`.

### P1: failed import read-back becomes a successful empty observation

- Location: `internal/adapters/arr/read/client.go:786-833`, especially the
  swallowed Radarr file error at `:800-815` and discarded Sonarr mapping/file
  reasons at `:826-829`.
- Evidence: a synthetic `GET /movie/101` returned a valid movie without an
  embedded file and `GET /moviefile?movieId=101` returned 503.
  `ObserveImport` returned nil error, `ExternalID=101` and `Files=[]`.
- Failure mode: an unavailable, malformed, capped or unmappable file
  observation is indistinguishable from verified no import. This is unsafe for
  lost-response reconciliation and can authorize an unnecessary retry.
- Contract: missing evidence must remain unknown; `connectors.md:118-126`
  requires command, history and exact final-file evidence to be reconciled per
  item. A-16 requires unsafe import to remain blocked.
- Required change: return a sanitized typed error or an observation carrying
  explicit incomplete coverage whenever a required file read, decode, mapping
  or cap is incomplete. Validate the returned title identity against the
  requested external ID.
- Required proof: Radarr and Sonarr fixtures must cover transport/status,
  malformed response, file cap, unmapped/ambiguous path and mismatched title;
  none may return a successful empty absence observation.
- Disposition: `current_blocker`.

### P1: basename fallback accepts a candidate from a different directory

- Location: candidate selection at
  `internal/adapters/arr/read/client.go:1178-1208`.
- Evidence: a review requested `/downloads/incoming/same.mkv`; the synthetic
  native response contained only `/downloads/other/same.mkv` with the expected
  movie ID and no rejections. `PreviewImport` accepted the requested file
  because `targetMatches` falls back to equal basenames.
- Failure mode: an exact approval can be built from a different source object.
  Duplicate release filenames across download folders are common, so this can
  select the wrong payload and bypass a native rejection on the reviewed path.
- Contract: A-10 and A-16 require exact selected file/episode associations and
  refusal of unsafe imports. Root mapping must remain component-aware.
- Required change: require the normalized absolute mapped path to equal the
  candidate path. A relative candidate may be resolved only against the exact
  queried folder; never use basename alone. Reject duplicate and ambiguous
  matches instead of taking the first.
- Required proof: cover same basename in two folders, first-candidate ambiguity,
  absolute and relative candidate paths, Windows separators, component-prefix
  lookalikes and the exact positive case.
- Disposition: `current_blocker`.

### P1: Sonarr reprocessing accepts the wrong series and an incomplete episode set

- Location: request construction at
  `internal/adapters/arr/read/client.go:733-767`, response validation at
  `:1017-1048`, and reprocessed request reduction at `:1112-1126`.
- Evidence: the reviewer approved series 201 and episodes `[301,302]`. The
  synthetic reprocess response associated the exact path with series 999 and
  only episode 301. The adapter returned one accepted file and no rejection.
  Sonarr validation checks only that a series ID exists and that the single
  `MovieOrEpisodeID` appears; it never compares the series to
  `RegisteredExternalID` or the full returned episode set to `EpisodeIDs`.
- Failure mode: a multi-episode file or season pack can silently lose an
  episode or bind to a different series while producing an accepted preview.
  The returned `ports.ImportFile` also cannot expose the reviewed episode set.
- Contract: A-10 requires exact selected episode IDs to reach and survive
  preview; A-16 requires rejection evidence to block unsafe imports.
- Required change: retain the selected episode set in the shared preview/import
  model, compare the nested series ID exactly, and require the native response
  to contain the exact approved episode set. Missing, extra, duplicate or
  changed episode associations must reject the file.
- Required proof: cover correct and wrong series, missing/extra/reordered/
  duplicate episode IDs, one file mapped to multiple episodes, season packs and
  absolute anime numbers.
- Disposition: `current_blocker`.

### P1: the Sonarr POST model is not the pinned reprocess DTO and drops language selection

- Location: `SonarrManualImportReprocess` at
  `internal/adapters/arr/read/client.go:598-604` and construction at `:758-761`.
- Evidence: the struct sends only path, series ID, episode IDs and optional
  download ID. The pinned Sonarr
  `ManualImportReprocessResource` also contains `Languages`, `Quality`,
  `SeasonNumber`, `ReleaseGroup`, `IndexerFlags` and `ReleaseType`. The pinned
  controller invokes `item.Languages.Count` while processing the POST; an
  omitted collection remains null under normal model binding. Independently,
  `ReprocessFile.Language` is never serialized, so the user's explicit
  language choice cannot reach native reprocessing.
- Failure mode: the real pinned endpoint can fail before returning a preview,
  and a successful tolerant endpoint would still reprocess without the
  selected language and other candidate state.
- Contract: `connectors.md:93-111` requires language/quality values from the
  selected schema and exact typed product DTOs. A-11 requires explicit language
  labels to be preserved.
- Required change: derive a complete typed reprocess resource from the native
  candidate and reviewed edits, including a non-null languages collection and
  the other fields consumed by the pinned controller. Do not claim compatibility
  from a permissive fake handler.
- Required proof: assert the full Radarr and Sonarr JSON payload shapes against
  the pinned resource/controller sources, including empty and selected
  languages, quality, release type, season number and indexer flags. Add a
  disposable pinned-version contract test before enabling support.
- Disposition: `current_blocker`.

### P1: preview revisions do not bind subtitle meaning or source provenance

- Location: `previewRevision` at
  `internal/adapters/arr/read/client.go:1760-1781` and its calls at `:563` and
  `:1123`.
- Evidence: the revision serializes only root ID, relative path, one media ID,
  registered ID, transfer and response digests. A reviewer changed language
  from `eng` to `por` and changed forced and hearing-impaired from false to true
  while keeping the same path, media ID and native body. Both requests produced
  the identical revision
  `sha256:49d9135a21fe28aa83e26c3c0758c69176f37a0a5bfe980d326126732d25b563`.
  `PreviewImportWithDownloadID` likewise does not add its download ID to the
  revision.
- Failure mode: a materially different subtitle association or source download
  can reuse the same preview identity. Later approval cannot prove which
  language/forced/SDH intent or download provenance was reviewed.
- Contract: approvals bind the immutable plan revision and scope. A-11 requires
  subtitle labels to be preserved; A-15 requires changed mappings to invalidate
  a preview.
- Required change: bind every meaningful file field, the complete episode set,
  connection ID, source download ID and relevant mapping/config revision into a
  canonical revision. Treat order according to the contract and reject
  duplicate file identities.
- Required proof: independently vary subtitle flag, language, forced, SDH,
  episode set, connection, download ID, path mapping and response evidence;
  every semantic change must change the revision while equivalent reordered
  input follows the documented canonical rule.
- Disposition: `current_blocker`.

### P2: same-origin redirects can send the reprocess POST to `/api/v3/command`

- Location: redirect policy at `internal/adapters/arr/read/client.go:125-143`
  and generic POST request at `:1487-1538`.
- Evidence: a synthetic server returned HTTP 307 from
  `/api/v3/manualimport` to `/api/v3/command`. Go preserved the method and body;
  the configured client followed it and issued one POST to the command route.
- Failure mode: a reverse-proxy rewrite, compromised upstream or future route
  redirect escapes the adapter's declared route allowlist. The current body is
  not a valid ManualImport command, but the review boundary promises zero calls
  to that route and cannot depend on another endpoint rejecting it.
- Contract: the package and handoff state that this adapter never calls the
  command endpoint; `connectors.md:99-111` keeps preview/reprocessing distinct
  from execution and forbids arbitrary command exposure.
- Required change: reject redirects for all adapter requests, or revalidate the
  exact method and endpoint against a fixed allowlist on every redirect. A POST
  must remain pinned to `/api/v3/manualimport`.
- Required proof: cover 301/302/303/307/308 for GET and POST, same-origin and
  cross-origin, and assert no redirected request is emitted to an unapproved
  route.
- Disposition: `current_blocker`.

### P2: subtitle/anime evidence does not cover the claimed acceptance contribution

- Location: `internal/adapters/arr/read/client_test.go:278-389` and
  `tests/fixtures/arr/read/sonarr-manual-import.json`.
- Evidence: the only subtitle fixture is one English SRT. Forced and SDH are
  asserted by echoing the requested booleans; no native field proves them.
  There is no IDX/SUB pair, unmatched companion, native forced/SDH distinction,
  anime absolute episode preview or season-pack response. The anime test only
  changes `MediaRecord.Kind` on provider lookup.
- Failure mode: regressions in companion pairing, explicit labels and absolute
  episode mapping can pass the committed suite. The handoff's statement that
  these cases are covered is unsupported.
- Contract: A-10 requires anime absolute numbering, multi-episode and season
  pack evidence. A-11 requires forced/SDH/language subtitles, IDX/SUB pairs and
  unmatched companions. The compatibility matrix keeps
  `CAP-SUBTITLE-ANIME=UNKNOWN` and explicitly requires an X-03 mapping or review
  blocker.
- Required change: add the missing read/preview fixtures and assert the exact
  retained associations. Where Arr does not expose sufficient native evidence,
  report the capability or association as unknown and leave approval blocked.
- Required proof: exercise video plus forced/SDH/language subtitles, IDX/SUB as
  a pair, an unmatched companion, absolute anime numbering, a multi-episode
  file and a season pack without inferring identity from the filename.
- Disposition: `current_blocker`.

## Confirmed behavior

- Every direct request validates the selected connection ID. Page coverage is
  connection-scoped, cursors are HMAC-authenticated with a per-client key, and
  a cursor from another client is rejected. Raw numeric IDs remain scoped by
  the caller and coverage; `ScopedIdentity` supplies a composite persistence
  key.
- Direct adapter routes are typed. GET is used for movie, series, file, episode,
  lookup, root-folder, quality-profile, history and manual-import preview.
  POST is used only for manual-import reprocessing. There is no direct PUT,
  PATCH, DELETE, registration, release-search or native execution call.
- Folder-only generic preview avoids the Arr library-file query selected by a
  movie/series ID without a download ID. The product-specific method sends
  folder, download ID and exact registered title ID.
- Native rejection type/message fields are retained and empty fields receive
  bounded generic values. Movie nested association and a requested Sonarr
  episode are checked, subject to the exact-set findings above.
- Response size, file count, record count, page count and cursor bytes are
  bounded. Transport/status errors are sanitized; response bodies, API keys and
  endpoints do not enter returned errors. Context cancellation is preserved.
- Inventory path mappings select the longest component-aware prefix, reject
  traversal, retain partial reasons for unmapped/ambiguous inventory files and
  emit root-relative `FileTarget` values.
- Fixtures are synthetic. The public-data scan found no tracker URLs, passkeys,
  cookies, private hosts, user paths or real inventory. Credential-like values
  are explicitly synthetic API-key fixtures.

## Checks and direct results

All executable checks ran against the clean exact product commit. Reviewer
probes were added only to an external temporary archive and never touched the
reviewed or shared worktree.

| Command or inspection | Result |
| --- | --- |
| Complete product/handoff diff, adapter, tests, fixtures, ports, connector specification, compatibility matrix and acceptance cases | Inspected independently. |
| Pinned Radarr `AllMovie` and Sonarr `AllSeries` controllers | Confirmed both inventory endpoints return full lists and do not accept the page parameters used by the adapter. |
| Pinned Radarr/Sonarr manual-import controllers and reprocess resources | Confirmed GET preview, POST reprocess and separate command execution; confirmed Sonarr POST consumes the fields described above. |
| Seven temporary adversarial tests | Failed as described: full-array tail loss, false-complete history, unavailable read-back becoming empty, basename path substitution, wrong Sonarr series/incomplete episode set, subtitle revision collision and 307 command redirect. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/arr/read ./internal/domain ./internal/ports` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/arr/read` | Passed. |
| Focused `GOWORK=off go vet -mod=readonly` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed all root packages. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed all root packages; storage completed in 64.903 seconds. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `./scripts/generate.sh --check` | Passed: generation checks passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases and local links resolve. |
| Root `GOWORK=off go mod verify` and `go mod tidy -diff` | Passed. |
| Tools module test, vet and tidy-diff | Passed. |
| UI module test and vet | Passed for the generated client package. UI tidy proposes removing future UI dependency pins; X-03 changes no UI/module file. |
| Linux amd64 CGO-free focused compile | Passed. |
| Scoped `git diff --check`, method/route inspection and public-data scan | Passed apart from the redirect escape reported above. |

The shared checkout was clean when the receipt was written. No product file,
fixture, module file or execution state was modified by the reviewer.

## Acceptance contribution assessment

- A-07: not accepted. The pinned full-array catalog loses every title after the
  first local slice; history also has a false-complete cap.
- A-09: partial. Connection arguments, coverage and cursors are scoped, but a
  preview revision does not bind connection or source-download scope.
- A-10: not accepted. Sonarr does not validate the nested series or full episode
  set, and no season-pack or absolute-anime preview fixture exists.
- A-11: not accepted. Language is omitted from Sonarr reprocessing, subtitle
  semantics do not affect the revision, and IDX/SUB/unmatched companion/native
  forced-SDH evidence is absent.
- A-16: not accepted. A different path can satisfy an exact preview, incomplete
  read-back becomes empty success, and redirect handling can reach the command
  route. Native execution correctly remains capability-unknown.

## Next round

Align catalog pagination with the pinned full-array endpoints; make every cap
and nested-evidence failure partial or typed unknown; require exact candidate
paths and nested title/episode sets; implement the complete pinned reprocess
DTOs; bind all semantic scope to the revision; and prohibit redirect escape.
Add each reviewer reproduction plus the missing subtitle/anime/season-pack
fixtures, then rerun the same focused, race, root, generation, module and
portable-build checks. This review changed only this receipt.
