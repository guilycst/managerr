# X-02 independent review, round four

## Decision

`approved`.

Product commit `d5f0e886de162d67a4855cc89c8391fca1f37c37` closes the
remaining round-three P1 finding. Exact, case-sensitive `drone` parameter
presence and cardinality now survive queue/history merging. The adapter uses
the numeric NZBID/ID fallback only when no exact `drone` parameter exists. One
safe exact value is accepted; an empty, non-string, unsafe, or duplicate exact
value leaves Arr correlation unknown and makes coverage partial. Prior
redaction, negative-ID, pagination, transport, and read-only guarantees remain
intact.

## Review basis

- Reviewed product: `d5f0e886de162d67a4855cc89c8391fca1f37c37`.
- Reviewed implementation handoff:
  `b93a7ba3c5c05d400f8db3ca8eee0be98232857d`, file
  `docs/execution/handoffs/X-02-correction-round5.md`.
- Product parent: `0f0b751ca8e41694b3f4047f6f00519f0448d161`.
- Prior receipt: `0f0b751ca8e41694b3f4047f6f00519f0448d161`.
- Scope: `internal/adapters/nzbget/inventory/`,
  `tests/fixtures/nzbget/`, frozen connector/acceptance evidence, and this
  receipt.
- Acceptance reviewed: A-05, A-06, and A-09.

The executable review ran in a clean detached snapshot at the exact product
commit. Temporary adversarial tests existed only in an external `/tmp` copy.
The shared checkout's execution-state, Arr, and storage work was preserved and
excluded from this commit.

## Finding disposition

No open finding remains in the reviewed X-02 scope.

| Round-three finding | Result |
| --- | --- |
| Present-but-unusable exact `drone` falls back to NZBID | Closed. Queue and history retain exact-name presence independently of usability. Empty, numeric, unsafe, malformed, and duplicate values now leave `Drone` and `ArrDownloadID` empty and add stable partial evidence. |
| Equal duplicate exact `drone` is accepted | Closed. A second exact-name parameter makes the selection unusable regardless of value equality, matching the pinned `SingleOrDefault` cardinality. |
| Presence is lost during queue/history merge | Closed. Invalid or ambiguous evidence in either view cannot be overwritten by an absent parameter in the other view. Two usable matching values remain valid; differing values are cleared and reported as a queue/history conflict. |

## Direct contract evidence

The pinned Radarr source at commit
`0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4` was fetched and inspected
independently. `Nzbget.cs:71-74` and `:121-126` select exactly one parameter by
`Name == "drone"` with `SingleOrDefault`, then use numeric NZBGet identity only
when the parameter is absent. `NzbgetProxy.cs:57-68` generates the normal
value from a hyphenless GUID. This confirms exact case, presence-based
fallback, and duplicate ambiguity.

The implementation's `parameterValueStatus` retains `Present` and `Usable`
separately. `observeQueue` and `observeHistory` suppress numeric fallback
whenever `Present` is true. `mergeDroneEvidence` preserves invalid presence
and clears conflicting usable values. Exported parameter evidence exposes one
safe unique correlation value only; duplicates and all other parameter values
remain redacted.

## Preserved behavior

- JSON-RPC requests and responses use the supported 1.1 envelope, echoed IDs,
  and positional parameters.
- The adapter issues only `listgroups`, `history`, and `version` reads. It has
  no queue edit, append, pause/resume, delete, filesystem write, or mutation
  retry path.
- Response bytes, item arrays, page sizes/counts, cursors, reason codes,
  descriptor reads, and exported free text remain bounded.
- Cursors remain HMAC-authenticated, connection-scoped, per-client, and stale
  after snapshot change.
- FinalDir/DestDir merging and component-safe path mapping retain correct item
  indexes. Unsafe, ambiguous, and unmapped paths remain partial and cannot
  create a `FileTarget`.
- Negative NZBID/ID values remain explicit `identity_invalid` evidence,
  lifecycle state stays unknown/not-ready, and a positive counterpart remains
  separate conservative identity evidence.
- Transport and status errors remain typed and sanitized. Credentials stay
  scoped to same-scheme, same-host requests and redirects. Context
  cancellation is preserved.
- Descriptor availability remains distinct from history metadata. Descriptor
  bytes and fabricated payload manifests are absent from ordinary inventory.
- Canonical client identities remain connection-scoped. Repository fixtures
  are synthetic.

## Checks and direct results

All executable checks used synthetic fixtures and no live NZBGet, credential,
media payload, upstream write, filesystem mutation, release, or deployment.

| Command or inspection | Result |
| --- | --- |
| Exact product/handoff diff, owned implementation/tests/fixtures, prior finding, connector contract, and A-05/A-06/A-09 | Inspected independently. The product commit changes only the two owned NZBGet inventory Go files. |
| Pinned Radarr `Nzbget.cs` and `NzbgetProxy.cs` at `0220f0d...` | Confirms exact-name `SingleOrDefault`, presence-based fallback, duplicate ambiguity, and normal 32-character hyphenless GUID generation. |
| Updated prior adversarial probes plus a six-case queue/history merge matrix, `-count=20` | Passed. Covered empty, numeric, unsafe, differently cased, equal duplicate, conflicting duplicate, matching unique, differing unique, one-view invalid, one-view valid, and absent exact parameters. Assertions include both correlation fields and stable partial/conflict reasons. |
| The same adversarial probes under `-race -count=10` | Passed. |
| `GOWORK=off go test -timeout=180s -count=50 ./internal/adapters/nzbget/inventory` | Passed. |
| Focused race, coverage, and vet | Passed; 81.2% statement coverage. |
| `GOWORK=off go test -timeout=300s -count=1 ./...` | Passed all root packages. |
| `GOWORK=off go test -race -timeout=360s -count=1 ./...` | Passed all root packages; storage completed in 76.247 seconds. |
| `GOWORK=off go vet ./...` | Passed. |
| Generation, API, architecture, planning, lint, and fast guardrail checks | Passed; Vacuum quality 100/100, planning reports 38 tasks and 60 acceptance cases. |
| Root module verify and tidy diff | Passed cleanly. |
| Tools module test, vet, verify, and tidy diff | Passed. |
| UI module test, vet, and verify | Passed; generated client package has no tests. |
| UI module tidy diff | Reports removal of existing future dependency pins; X-02 changes no UI or module file. |
| Linux amd64/arm64, Windows amd64, and AIX ppc64 focused `CGO_ENABLED=0 go test -c` | Passed. |
| `gofmt -d`, `git diff --check`, fixture JSON parsing, RPC mutation inspection, and public-data scan | Passed; credential-like matches are limited to configuration fields and synthetic tests. |
| Product scoped snapshot | Clean and unchanged at SHA-256 `c1ec5ca75c61a3e1320bfbc4d4f828780bfc42b4d752a68da1b7261ac5d5c2db`. |

## Acceptance disposition

- A-05: accepted for X-02. Queue, post-processing, and history evidence now
  preserve exact Arr correlation semantics, lifecycle readiness, and mapped
  final paths without promoting ambiguity.
- A-06: accepted for X-02. Descriptor availability and detailed evidence stay
  bounded, honest, and secret-free under the reviewed probes.
- A-09: accepted for X-02. Canonical client identity and observations remain
  connection-scoped.

No product file, fixture, module file, or `docs/execution/state.json` was
modified by the reviewer.
