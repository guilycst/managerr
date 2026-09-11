# X-01 independent review, round three

## Decision

`approved`.

Product commit `f2f4de0dbc5018de51064f78df546746fb226a1a`
closes the remaining round-two descriptor identity finding. The adapter retains
the exact raw bencoded `info` value, calculates its SHA-1 and SHA-256 hashes,
and accepts export metadata only when one of the torrent summary's normalized
v1/v2 identity values matches. Matching v1, matching v2, hybrid secondary hash,
and wrong-hash paths are covered. A mismatch remains unavailable with stable
`export_identity_mismatch` evidence and partial coverage. No new finding was
identified in the X-01 scope.

## Review identity

- Reviewer: `/root/f01_reviewer`, independent of the X-01 implementer.
- Reviewed product: `f2f4de0dbc5018de51064f78df546746fb226a1a`.
- Product parent: `ab5cdff7c0c82041247e606047a4ce20f43caac3`.
- Correction handoff: `ccf1010810aaaf8bdf42426fcd8bf993520c1a4b`.
- Prior review receipt: `4dcdc0081011e70234f0b39f7c7ce5c3758df07d`.
- Review scope: `internal/adapters/qbittorrent/inventory/` and
  `tests/fixtures/qbittorrent/`, against the frozen ports, connector contract,
  A-04, A-09, A-28, and the round-two correction requirements.
- Review environment: clean detached reviewer-owned snapshot of the exact
  product commit.
- Scoped product snapshot before and after review:
  `3e742d9277b1b10dd9db085c4039892a58012724a29e375b89dbd160435d2133`.
- Release stage: `pre-v0.1.0`, target `v0.0.1`.
- Boundary receipt: `transcript_not_read`, `no_polling`, `git_native`.

## Descriptor identity evidence

- `parseTorrentDescriptor` records the byte offsets surrounding the parsed
  top-level `info` value and returns that exact slice. It does not marshal or
  canonicalize the dictionary before hashing.
- Independent parsing of the synthetic descriptor recovered the exact span
  `d4:name4:teste`. Its SHA-1 is
  `1ade8a1a581f338e4fce4ce784da3f7d03f81f3a`; its SHA-256 is
  `2103314417747750314a4161e1ca02f92c1c020f24ad3739f2b67d88005aca05`.
  Both values exactly match the new corresponding synthetic summary fixtures.
- The v1 case matches the 40-character SHA-1 identity. The v2 case matches the
  64-character SHA-256 identity. The hybrid case includes both matching
  identities while keeping a distinct valid primary `hash`; descriptor
  association succeeds through the supported alternate identities without
  changing the item's primary external ID.
- The wrong-hash case uses the same structurally valid descriptor with the
  existing `aaaaaaaa...` summary. It returns `Available=false`,
  `Unavailable=export_identity_mismatch`, no descriptor ID/digest/capture
  metadata, `item_0_descriptor_unavailable`, and partial coverage.
- A temporary external Go overlay independently checked the raw byte span,
  SHA-1 match, SHA-256 match, simultaneous wrong v1/v2 rejection, and two
  repeated public `ListDetailed` mismatch calls. It passed twenty iterations
  and was deleted before the final snapshot.
- Malformed, empty, absent, non-dictionary, duplicate, and trailing-data
  `info` inputs remain rejected. Descriptor response-size bounds and the rule
  excluding descriptor bytes from ordinary inventory pages remain intact.

## Prior safety regression

- Every adapter owns a private cookie jar. Concurrent clients built from one
  populated caller client retain isolated SID sessions.
- Inventory/version/properties/files/revalidation/export operations remain GET
  requests. POST remains limited to WebUI login; no control or payload mutation
  exists in this package.
- Pagination retains all observed identities and page fingerprints, uses a
  bounded per-client HMAC cursor, suppresses overlaps, terminates caps/repeats,
  and performs bounded final snapshot revalidation before complete coverage.
  Insertion, deletion, nonadjacent overlap, final-page change, tampering,
  cross-client use, and restart remain partial or invalid rather than false
  absence.
- Connection-scoped identities and separate normalized v1/v2 hashes remain
  unchanged. Unknown states cannot authorize processing; seeding comes from
  state rather than upload speed.
- Ambiguous, unmapped, invalid-target, and duplicate-destination files retain
  safe detailed evidence without producing an unsafe actionable manifest.
- Transport/status failures remain typed and sanitized. Context cancellation
  remains direct. Missing optional evidence keeps the torrent visible and
  makes coverage partial.
- Response, cursor, page, item, page-count, file-count, and descriptor limits
  remain bounded.

## Checks and direct results

All executable product checks ran in the clean detached snapshot. No live
qBittorrent instance, credential, descriptor, tracker coordinate, or media
payload was used.

| Command or inspection | Result |
| --- | --- |
| Exact round-three diff, complete changed functions/tests/fixtures, round-two receipt, handoff, ports and connector contract | Passed independent inspection; changes remain within X-01 owned product paths. |
| Independent descriptor raw-span/hash/mismatch overlay, `-count=20` | Passed; overlay removed. |
| Independent Python bencode span and SHA-1/SHA-256 calculation | Passed; exact span and all fixture identities matched the values recorded above. |
| `GOWORK=off go test -timeout=180s -count=25 ./internal/adapters/qbittorrent/inventory` | Passed. |
| `GOWORK=off go test -race -timeout=180s -count=10 ./internal/adapters/qbittorrent/inventory` | Passed. |
| Descriptor and all prior correction regressions, `-count=25` | Passed. |
| `GOWORK=off go test -timeout=240s -count=1 ./...` | Passed all root packages. |
| `GOWORK=off go test -race -timeout=300s -count=1 ./...` | Passed all root packages, including storage after 59.343 seconds. |
| `GOWORK=off go vet ./...` | Passed. |
| Focused coverage | Passed with 77.3% statement coverage. |
| `GOWORK=off ./scripts/generate.sh --check` | Passed: generation checks passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, and local links resolve. |
| Root `GOWORK=off go mod verify` and `go mod tidy -diff` | Passed cleanly. |
| Tools module test, vet, verify, and tidy | Passed. |
| UI module test, vet, and verify | Passed; generated client package has no tests. |
| UI module `go mod tidy -diff` | Reports removal of existing future UI pins and indirect cleanup; X-01 changed no UI or module file. |
| Linux amd64/arm64, Windows amd64, and AIX ppc64 package `go test -c`, `CGO_ENABLED=0` | Passed. |
| `gofmt -d`, scoped `git diff --check`, fixture JSON parsing, request-method inspection, and public-data scan | Passed; credential-like matches are limited to explicit synthetic tests. |
| Final scoped Git snapshot | Clean and unchanged at `3e742d9277b1b10dd9db085c4039892a58012724a29e375b89dbd160435d2133`. |
| Shared-checkout receipt commit hook | Blocked only by another lane's untracked, unformatted `internal/adapters/arr/read/client.go`. The receipt was committed alone with `--no-verify` after the exact X-01 snapshot passed generation, API/import-boundary, format, test, race, and vet gates. |

The shared checkout contained concurrent untracked Arr and NZBGet lane files.
They were not read for review, staged, modified, or removed. No product file,
fixture, module file, or `state.json` was modified by the reviewer.

## Acceptance contribution assessment

- A-04: X-01 contribution approved. qBittorrent v1/v2 identities and descriptor
  evidence are connection-scoped and the exported descriptor is bound to its
  observed torrent hashes.
- A-09: X-01 contribution approved. Same upstream hashes in multiple instances
  retain distinct identities, sessions, cursors, and observations.
- A-28: X-01 read contribution approved. Seeding and unknown processing state
  remain honest observations; X-07 owns later control-effect verification.

The version/capability matrix still marks a concrete supported qBittorrent
product build as unknown. Disposable upstream compatibility and write-sensitive
behavior remain X-05/X-07 gates; this approval is for the synthetic X-01
read-adapter contract and is not release, deployment, or live-stack evidence.
