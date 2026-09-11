# X-01 independent review, round two

## Decision

`changes_requested`.

Correction commit
`dd7156396c649c00190ddc7f25d2e0e12868b1c5` closes all five round-one P1
findings. Each adapter now owns a private cookie jar; pagination carries all
observed identities, authenticates bounded cursor state, and revalidates a
multi-page traversal before it can claim complete coverage; unknown torrent
states cannot set processing complete; and ambiguous mappings retain their
exact file observations. The round-one P2 descriptor finding remains partly
open: structural metainfo validation was added, but an exported descriptor is
not bound to the requested torrent's v1 or v2 info hash. The adapter can still
report a valid descriptor for a different torrent as available provenance.

## Review identity

- Reviewer: `/root/f01_reviewer`, independent of the X-01 implementer.
- Round-one product: `d74a735b18a2cc5768dcb9f3ff0656f8540d3cff`.
- Round-one review receipt: `b89ffa1b9fc829134fed84cf55fd88e614243d91`.
- Reviewed correction product:
  `dd7156396c649c00190ddc7f25d2e0e12868b1c5`.
- Correction handoff receipt:
  `f219bdf58dd8d0c82a2f279f0f67084734e43c70`.
- Review scope: `internal/adapters/qbittorrent/inventory/` and
  `tests/fixtures/qbittorrent/`, against the frozen ports and connector
  contract plus A-04, A-09, and A-28.
- Review environment: clean detached reviewer-owned snapshot of the exact
  product commit. The scoped product snapshot before and after review was
  `254a4acb0a19199d5573f88fee16ab55fab947b96860ce370233ae0d90f4a1df`.
- Release stage: `pre-v0.1.0`, target `v0.0.1`.
- Boundary receipt: `transcript_not_read`, `no_polling`, `git_native`.

## Finding

### P2: an exported descriptor is not bound to the requested torrent identity

- Location:
  `internal/adapters/qbittorrent/inventory/inventory.go:842` through `:883`,
  especially the structural-only check at `:867`; and
  `validTorrentDescriptor` at `:1174` through `:1205`.
- Reproduction: the shipped `info-single.json` identifies
  `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`. The exact bencoded `info`
  dictionary in the shipped `descriptor.torrent` hashes to
  `1ade8a1a581f338e4fce4ce784da3f7d03f81f3a` with SHA-1 and
  `2103314417747750314a4161e1ca02f92c1c020f24ad3739f2b67d88005aca05`
  with SHA-256. A temporary external Go overlay returned that structurally
  valid descriptor for the `aaaaaaaa...` item through `ListDetailed`.
  `DescriptorObservation.Available` was `true`, with a generated ID, byte
  digest, capture time, and `qbittorrent.export` source. The independent
  regression expecting unavailable failed. The overlay was deleted after the
  probe and did not change the reviewed tree.
- Existing-test evidence:
  `TestBestEffortDescriptorMetadata` accepts this same mismatched pair as a
  positive case. `TestMalformedTorrentDescriptorRejected` now correctly
  rejects empty, missing, non-dictionary, duplicate, and trailing-data forms,
  but does not test a structurally valid descriptor belonging to another
  torrent.
- Failure mode: a wrong, stale, cached, proxy-substituted, or fixture-swapped
  export becomes positive original-descriptor provenance for the current
  download. The ordinary page still omits descriptor bytes, but its
  availability, digest, and association are false.
- Contract: R-02 requires associating an original `.torrent` only when evidence
  exists; the connector contract says to retain exported original bytes only
  when supported and obtainable; I-05 requires unknown evidence to stay
  unknown. The round-one receipt explicitly required a valid descriptor for a
  different hash to be rejected.
- Required correction: retain the exact raw bencoded `info` span, calculate its
  SHA-1 and SHA-256 identities, and compare them with the item's supported v1
  and v2 hash evidence before setting `Available=true`. Pass all known hash
  variants to descriptor validation rather than only the selected external ID.
  A mismatch must remain unavailable/partial with a stable sanitized reason.
- Required proof: add matching v1, matching v2, and hybrid synthetic fixtures;
  reject a structurally valid descriptor for another torrent; retain the new
  malformed-structure cases, response-size bound, and ordinary-response byte
  exclusion.
- Disposition: `current_blocker`.

## Round-one disposition

| Round-one finding | Direct round-two result | Disposition |
| --- | --- | --- |
| P1 caller cookie jar crosses sessions | `New` always allocates a fresh jar. Two clients built from one populated caller client retain different jars and concurrent SID sessions. | `resolved` |
| P1 offset pagination omits/duplicates while complete | Cursor state retains all seen IDs and page fingerprints. Nonadjacent overlap is suppressed. A bounded second traversal detects deletion, insertion, duplicate membership, and final-page changes and leaves coverage partial. | `resolved` |
| P1 forgeable/self-invalid cursor | Cursor payloads use per-client HMAC-SHA-256, canonical base64, fixed connection/page fields, and 128 KiB bounds. Accepted page/item limits cap at 1,000; the maximum v2 identity set round-trips. Tampering, another client, and restart invalidate the token. | `resolved` |
| P1 unknown state authorizes processing | `ProcessingDone` requires a recognized terminal state and complete progress. Empty/future values add `state_unknown`, remain false, and make coverage partial. | `resolved` |
| P1 ambiguous mapping drops file evidence | Ambiguous, unmapped, and duplicate destinations append the complete safe `FileObservation` without an actionable manifest entry and retain a distinct reason. | `resolved` |
| P2 structural bencode claims descriptor availability | Empty/missing/non-dictionary/duplicate `info` structures are rejected. Identity mismatch remains accepted as described above. | `open` |

## Confirmed behavior

- All inventory, version, properties, files, revalidation, and export requests
  use GET. POST is used only for WebUI login; the package exposes no stop,
  rename, relocation, tag/category, removal, or payload-write operation.
- Connection scope remains explicit in calls and persisted-style identities.
  Valid v1 and v2 hashes are separately normalized and preserved.
- Response, page, item, page-count, file-count, cursor, and descriptor bounds
  terminate without unbounded traversal. Cumulative counts and prior-partial
  state survive continuation; complete multi-page coverage requires a matching
  bounded validation traversal.
- Transport/status failures remain typed and sanitized. Context cancellation is
  returned directly. No endpoint, credential, cookie, or response body enters
  an error or observation.
- Full torrent summary, properties, file metadata, mapping, category/tag,
  seeding, completion, progress, ratio, seed/leech, and unknown-sentinel
  evidence remains present. Missing optional observations make coverage partial
  without removing the torrent.
- All fixtures are synthetic. Public-data scanning found no private host, user
  path, private IP, tracker URL, passkey, PEM material, or real inventory.
  Credential-like matches are limited to explicit synthetic test values and an
  invalid endpoint fixture.

## Checks and direct results

All executable product checks ran at the exact clean detached correction
commit. No live qBittorrent instance or media payload was used.

| Command or inspection | Result |
| --- | --- |
| Complete correction diff, adapter, tests, fixtures, ports, connector specification, task and A-04/A-09/A-28 | Inspected independently. Only the two owned adapter files changed from round one; fixtures were unchanged. |
| Independent descriptor identity overlay | Failed as described: a valid descriptor with a different v1/v2 info hash was reported available. Overlay deleted. |
| `GOWORK=off go test -timeout=180s -count=25 ./internal/adapters/qbittorrent/inventory` | Passed. |
| Focused `go test -race -timeout=180s -count=5` | Passed. |
| Focused correction tests `-count=25` and focused concurrency/pagination tests under race `-count=10` | Passed. |
| `GOWORK=off go test -timeout=180s -count=1 ./...` | Passed all root packages. |
| `GOWORK=off go test -race -timeout=240s -count=1 ./...` | Passed all root packages. |
| `GOWORK=off go vet ./...` | Passed. |
| Focused coverage | Passed with 76.5% statement coverage. |
| Linux amd64/arm64, Windows amd64, and AIX ppc64 package `go test -c`, `CGO_ENABLED=0` | Passed. |
| `GOWORK=off ./scripts/generate.sh --check` | Passed: generation checks passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, and local links resolve. |
| Root `GOWORK=off go mod verify` | Passed. |
| Root `GOWORK=off go mod tidy -diff` | Reports existing coordinator-owned module drift: unused future oapi runtime plus test/transitive sum changes. X-01 changed no module file. |
| Tools module test, vet, verify, and tidy | Passed. |
| UI module test/vet/verify/tidy | Test and vet report no packages; verify passes; tidy proposes removing the existing future UI dependency pins. This is outside X-01. |
| `gofmt -d`, scoped `git diff --check`, fixture JSON parsing, and public-data scan | Passed, subject only to the documented synthetic credential strings. |
| Final scoped Git snapshot | Clean and unchanged at `254a4acb0a19199d5573f88fee16ab55fab947b96860ce370233ae0d90f4a1df`. |

The shared checkout's concurrent coordinator and other-lane work was preserved.
No product file, fixture, module file, execution state, live stack, release, or
deployment was modified by this review.

## Acceptance contribution assessment

- A-04: not cleared. Connection-scoped v1/v2 item identity is correct, but a
  descriptor for another hash can be attached as current provenance.
- A-09: the X-01 contribution is confirmed; same hashes in two qBittorrent
  instances remain distinct and sessions/cursors are connection-local.
- A-28: the X-01 read contribution is confirmed; seeding derives from state,
  zero upload speed is not used as stopped evidence, and unknown states cannot
  authorize processing. Control effects remain owned by X-07.

X-01 remains review-blocked until the descriptor identity finding is corrected
and independently re-reviewed. Local passing checks are not integration,
release, deployment, or live-stack evidence.
