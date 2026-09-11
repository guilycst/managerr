# X-01 independent review, round one

## Decision

`changes_requested`.

The exact product commit
`d74a735b18a2cc5768dcb9f3ff0656f8540d3cff` provides a read-only adapter,
scoped v1/v2 hash evidence, bounded HTTP reads, typed sanitized errors, detailed
torrent/file/property observations, synthetic fixtures, and partial evidence for
many optional failures. Six correctness boundaries remain open. Sessions can
share a caller-provided cookie jar, pagination can skip or duplicate torrents
while claiming complete coverage, accepted page settings can emit an unusable
cursor, an unknown torrent state is treated as processing-complete, ambiguous
mapped files disappear from detailed observations, and structurally valid
non-torrent bencode is reported as an available descriptor.

## Review identity

- Reviewer: `/root/f01_reviewer`, independent of the X-01 implementer.
- Review base supplied by coordinator:
  `857cea48188dc79fac323988d181581af29a32c6`.
- Dispatch base recorded in the worker handoff:
  `bc75aacbfda920272618d8e76118b4bc5f10df23`.
- Reviewed product commit:
  `d74a735b18a2cc5768dcb9f3ff0656f8540d3cff`.
- Worker handoff receipt:
  `45836c4eb43d6708c7e5dca8159cd9fc239b620d`.
- Review scope: `internal/adapters/qbittorrent/inventory/` and
  `tests/fixtures/qbittorrent/` against the frozen domain/port contract,
  connector specification, and A-04/A-09/A-28.
- Reviewer worktree: clean detached reviewer-owned worktree under
  `$CODEX_HOME/worktrees`, recorded without its host path.
- Scoped Git snapshot before and after review:
  `47e1894534e212cfd2e17f562afa40731326cb0ff684329cae842bceb8954d7c`.
- Release stage: `pre-v0.1.0`, target `v0.0.1`.
- Boundary receipt: `transcript_not_read`, `no_polling`, `git_native`.

## Findings

### P1: caller-provided cookie jars break per-client session isolation

- Location: `internal/adapters/qbittorrent/inventory/inventory.go:194` through
  `internal/adapters/qbittorrent/inventory/inventory.go:209`.
- Evidence: `New` shallow-copies the supplied `http.Client` and creates a cookie
  jar only when the copied client has no jar. A temporary reviewer overlay
  supplied a standard jar and observed `client.http.Jar == suppliedJar`.
- Failure mode: two configured qBittorrent clients using one base HTTP client
  share SID cookies. Instances on the same host can overwrite or send each
  other's session, causing cross-instance authentication, extra login cycles,
  or failures determined by request timing. The adapter's `authenticated` flag
  remains per client while the actual session store does not.
- Contract: X-01 requires authenticated session handling and explicit instance
  scope. The connector boundary requires secret isolation and the handoff claims
  a private cookie jar.
- Required change: always allocate a private cookie jar owned by each adapter
  instance. Copy reusable transport, timeout, and redirect policy separately;
  do not inherit mutable session cookies from a caller-owned client.
- Required proof: construct two clients from one base client with a populated
  jar and same-host endpoints. Prove neither adapter reuses the supplied jar,
  no login or inventory request carries the other instance's SID, and concurrent
  inventories do not trigger cross-session retries.
- Disposition: `current_blocker`.

### P1: offset pagination can omit or duplicate torrents and still become complete

- Location: `internal/adapters/qbittorrent/inventory/inventory.go:266` through
  `internal/adapters/qbittorrent/inventory/inventory.go:424`, and cursor history
  at `internal/adapters/qbittorrent/inventory/inventory.go:1126` through
  `internal/adapters/qbittorrent/inventory/inventory.go:1212`.
- Evidence: the cursor retains only the immediately previous page's IDs and
  fingerprint. A reviewer overlay returned `[A,B]`, then `[C,D]`, then `[A,E]`.
  The third page emitted A a second time because only C and D were remembered.
  A second probe modeled A being removed from sorted `[A,B,C,D]` after page one;
  qBittorrent offset 2 then returned only D. The adapter omitted C and returned
  `Completeness=complete`, `ObservedCount=3`, and no reason code.
- Failure mode: insertion or deletion before the current offset changes page
  membership without necessarily overlapping the preceding page. A completed
  scan can therefore omit an existing torrent or count one torrent twice. That
  complete evidence can later be used to claim an association is absent.
- Contract: connector pagination must deduplicate page overlaps and terminate
  defensively. Partial coverage cannot overwrite complete evidence or prove
  absence when the upstream inventory changed during the scan.
- Required change: retain bounded all-pages identity state and establish a
  stable inventory revision or perform a bounded validation pass before marking
  a multi-page scan complete. If qBittorrent cannot provide or validate a stable
  snapshot, terminate with partial coverage. Remembering one page is
  insufficient.
- Required proof: cover nonadjacent overlap, insertion and deletion before the
  offset, repeated pages, and changes on the final short page. Assert every
  emitted ID is unique and any possible gap prevents complete coverage.
- Disposition: `current_blocker`.

### P1: cursor state is forgeable and accepted bounds can produce a self-invalid cursor

- Location: cursor decoding/encoding at
  `internal/adapters/qbittorrent/inventory/inventory.go:793` through
  `internal/adapters/qbittorrent/inventory/inventory.go:823`, configuration
  defaults at `internal/adapters/qbittorrent/inventory/inventory.go:162` through
  `internal/adapters/qbittorrent/inventory/inventory.go:181`, and cursor fields
  at `internal/adapters/qbittorrent/inventory/inventory.go:1126` through
  `internal/adapters/qbittorrent/inventory/inventory.go:1136`.
- Evidence: the opaque cursor is unsigned base64 JSON. A reviewer decoded the
  first real cursor, changed its offset from 2 to 4, and resubmitted it. The
  adapter skipped C and D, returned E, and marked the snapshot complete with an
  observed count of 3. Separately, `New` accepted page size 2,000; a valid state
  containing 2,000 v2 hashes encoded to about 179 KiB, above the 128 KiB decoder
  ceiling, so `decodeCursor(encodeCursor(state))` returned `invalid_input`.
- Failure mode: an API caller can choose offset, counts, prior IDs, page count,
  source identity, and start time, producing false complete coverage. Valid
  configuration can also return a continuation that fails on its first use.
- Contract: pagination cursors are opaque adapter evidence. Bounds must produce
  usable continuation, reject altered state, and terminate as partial rather
  than strand a scan.
- Required change: authenticate cursor contents with an instance-owned key or
  retain cursor state server-side under a bounded opaque ID. Add hard
  configuration ceilings consistent with the cursor representation, and make
  cursor encoding fail explicitly instead of returning an unchecked string.
- Required proof: mutate each cursor field and require typed invalid input;
  round-trip the largest accepted v1/v2 page; verify caps, expiry/restart
  semantics, and concurrent/replayed continuation behavior.
- Disposition: `current_blocker`.

### P1: missing or unknown torrent state is reported as processing-complete

- Location: item construction at
  `internal/adapters/qbittorrent/inventory/inventory.go:511` through
  `internal/adapters/qbittorrent/inventory/inventory.go:540`, and state helpers
  at `internal/adapters/qbittorrent/inventory/inventory.go:1296` through
  `internal/adapters/qbittorrent/inventory/inventory.go:1311`.
- Evidence: `ProcessingDone` is `progress >= 1 && !isProcessingState(state)`.
  `isProcessingState` returns false for an empty or unrecognized value. A
  reviewer response with a valid hash, progress 1, and empty state produced
  `ProcessingDone=true`, no state reason, and complete coverage.
- Failure mode: a malformed response or a state introduced by a newer client is
  interpreted as ready rather than unknown. Downstream review can permit file
  handling while qBittorrent's processing condition is not understood.
- Contract: processing completion and seeding are real state observations;
  unknown evidence must remain unknown and cannot authorize an action.
- Required change: preserve the raw state but derive readiness only from an
  explicit supported-state classification. Empty and unrecognized states must
  set processing incomplete/unknown and add partial reason evidence. Tie the
  recognized vocabulary to version/capability evidence.
- Required proof: cover every supported complete, downloading, checking,
  metadata, moving, error, stopped, and seeding state plus empty and future
  unknown values. Only proven terminal states may set `ProcessingDone`.
- Disposition: `current_blocker`.

### P1: an ambiguous mapping removes the exact file from detailed evidence

- Location: `internal/adapters/qbittorrent/inventory/inventory.go:628` through
  `internal/adapters/qbittorrent/inventory/inventory.go:675`, especially the
  ambiguous branch at lines 656 through 660.
- Evidence: unmapped files append a `FileObservation` with no manifest entry,
  but ambiguous files append only `payload_mapping_ambiguous` and `continue`.
  A reviewer probe configured two equal source prefixes to different roots and
  supplied one valid file. `mapFiles` returned no file observations.
- Failure mode: the conflict most in need of operator review loses its path,
  index, size, progress, priority, availability, seed count, and seed flag. The
  UI cannot show which exact file needs mapping correction, and downstream
  evidence can confuse an ambiguous file with no file observation.
- Contract: missing mappings must keep the download and file visible. The
  handoff explicitly promises that unmapped or ambiguous files remain visible
  in detailed observations.
- Required change: append the complete `FileObservation` without a mapped
  manifest entry for ambiguous mappings, as the unmapped branch does. Preserve
  distinct upstream file observations even when mapping or mapped-path
  deduplication rejects their action target.
- Required proof: ambiguous, unmapped, invalid-target, and duplicate-destination
  cases must retain every safe upstream file observation while emitting no
  unsafe payload manifest.
- Disposition: `current_blocker`.

### P2: structural bencode alone can falsely claim a torrent descriptor exists

- Location: descriptor handling at
  `internal/adapters/qbittorrent/inventory/inventory.go:678` through
  `internal/adapters/qbittorrent/inventory/inventory.go:719`, and parser at
  `internal/adapters/qbittorrent/inventory/inventory.go:966` through
  `internal/adapters/qbittorrent/inventory/inventory.go:1070`.
- Evidence: `validTorrentDescriptor` accepts any fully consumed top-level
  dictionary. A reviewer probe passed `de`, an empty bencode dictionary, and the
  function returned true. The descriptor path would assign an ID, digest,
  capture time, and `Available=true` to that response.
- Failure mode: a malformed or unexpected bencoded response is presented as an
  obtainable original `.torrent`, even though it has no `info` dictionary and
  cannot identify content.
- Contract: descriptor evidence distinguishes a verified original export from
  unavailable or malformed data. Missing evidence must not become available.
- Required change: validate torrent metainfo semantics, including one top-level
  `info` dictionary, before producing available metadata. Where feasible, bind
  its supported v1/v2 info-hash identity to the requested torrent; otherwise
  keep association confidence explicit for X-08 verification.
- Required proof: reject empty dictionaries, absent/non-dictionary/duplicate
  `info`, trailing data, excessive depth/length, and valid bencode that belongs
  to another hash; retain positive v1, v2, and hybrid fixtures.
- Disposition: `current_blocker`.

## Confirmed behavior

- The package contains no torrent control operation. Network methods are GET
  for inventory/version/properties/files/export and POST only for login.
- Response, file-count, item-count, page-count, descriptor, and ordinary page
  limits terminate requests, although accepted configuration and cursor-state
  bounds require the corrections above.
- HTTP status and transport failures become normalized `domain.UpstreamError`
  values without raw bodies, endpoints, usernames, or passwords. Context
  cancellation remains observable.
- Valid v1 and v2 hashes are normalized and preserved separately in detailed
  observations. Common external IDs are connection-scoped by the call and
  coverage, while `ScopedIdentity` prevents cross-instance persistence keys.
- Known seeding states are derived from state rather than upload speed.
  Categories and tags remain read-only hints.
- Longest component-aware path mappings work, traversal is rejected, unmapped
  files remain detailed, and mapped manifests contain root-relative paths.
- Optional properties/files/version/export failures retain the torrent and make
  coverage partial. Availability and seed `-1` sentinels remain explicit for
  valid file observations.
- Descriptor bytes never enter an ordinary page; successful synthetic exports
  expose only metadata and a SHA-256 digest.
- Fixtures are synthetic. No tracker URL, private host, private IP, user path,
  PEM material, or real inventory was found. Credential-like scan matches were
  limited to clearly synthetic test values and an invalid endpoint fixture.

## Checks and direct results

All executable review checks ran from the exact clean detached product commit.
Temporary reviewer tests were supplied through an external Go overlay and
removed before the final snapshot.

| Command or inspection | Result |
| --- | --- |
| Scoped Git snapshot before and after review | Passed; identity remained `47e1894534e212cfd2e17f562afa40731326cb0ff684329cae842bceb8954d7c`, with no staged, unstaged, or untracked files. |
| Product/handoff diff and complete adapter, ports, domain, specifications, acceptance catalog, and fixtures | Inspected independently. |
| Eight temporary reviewer probes | Failed as described: shared jar, nonadjacent duplicate, deletion gap marked complete, forged cursor skip, self-invalid large cursor, unknown state marked done, ambiguous file omission, and false descriptor acceptance. Overlay removed. |
| `GOWORK=off go test ./internal/adapters/qbittorrent/inventory -count=25` | Passed. |
| Focused `go test -race`, `-count=5` | Passed. |
| `GOWORK=off go test ./... -count=1` | Passed all root packages. |
| `GOWORK=off go test -race ./... -count=1` | Passed all root packages. |
| `GOWORK=off go vet ./...` | Passed. |
| Focused coverage run | Passed with 76.5% statement coverage. |
| Linux amd64/arm64, Windows amd64, and AIX ppc64 `go test -c` with `CGO_ENABLED=0` | Passed. |
| `GOWORK=off go mod verify` | Passed in root, UI, and tools modules. |
| Root `GOWORK=off go mod tidy -diff` | Reports the existing coordinator-owned root module differences; X-01 changes no module file. |
| UI module test/vet/tidy | No Go packages are present. Verify passes; tidy proposes removing future UI pins already assigned to U-01. |
| Tools module test, vet, verify, and tidy | Passed. |
| `./scripts/generate.sh --check` | Passed: generation checks passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, and local links resolve. |
| `gofmt -d` and scoped `git diff --check` | Passed with no output. |
| Public-data and fixture scan | Passed with the synthetic credential exceptions noted above. All non-malformed JSON fixtures parse; the torrent fixture contains no announce URL or private marker. |

The shared checkout's unrelated D-01 migration and storage edits were not read,
staged, or modified. No live qBittorrent instance, credential, upstream write,
filesystem mutation, release, or deployment was used.

## Acceptance contribution assessment

- A-04: not accepted. v1/v2 and connection-scoped hash fields are present, but
  mutable pagination can omit or duplicate client records under complete
  coverage, and descriptor availability can be asserted for non-torrent data.
- A-09: not accepted. Stable per-connection identity strings exist, but a shared
  caller jar permits session state to cross configured client instances.
- A-28: not accepted. Known seeding states do not rely on upload speed, but an
  absent or unrecognized state can be marked processing-complete. Control-side
  stop/read-back behavior remains X-07 work.
- Discovery boundary: not accepted. Ambiguous file evidence disappears, and
  pagination/cursor gaps can incorrectly support absence reasoning.

## Next review event

Correct private session ownership, snapshot-safe and tamper-resistant bounded
pagination, maximum cursor round trips, conservative state readiness, detailed
ambiguous-file preservation, and torrent metainfo validation. Add deterministic
regressions for every reproduction above, freeze the correction commit, and
request round-two independent review.
