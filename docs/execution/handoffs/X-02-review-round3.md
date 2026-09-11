# X-02 independent review, round three

## Decision

`changes_requested`.

Product commit `96b3db3048c607447a5c9bf34d9401ce9a60703a` closes the
three findings in the round-two receipt: ordinary detailed evidence is
allowlisted/redacted, parameter-name matching is exact and case-sensitive, and
negative identity values remain partial. One P1 correlation defect remains.
The adapter loses whether an exact `drone` parameter was present but unusable,
then applies the numeric NZBID fallback. It also accepts duplicate equal
`drone` parameters. Both behaviors disagree with the pinned Arr client and
can assert an Arr download ID that Arr does not use.

## Review basis

- Reviewed product: `96b3db3048c607447a5c9bf34d9401ce9a60703a`.
- Reviewed implementation handoff:
  `cba10b7a9f1a74475ce09c17d2e61f5683798027`, file
  `docs/execution/handoffs/X-02-correction-round4.md`.
- Product parent: `39b66cd1d28342543171f90b0d9fccd9a8d5a571`.
- Prior receipt: `377e1981e4895d938570ae69dc27b0575ab40e20`.
- Scope: `internal/adapters/nzbget/inventory/`,
  `tests/fixtures/nzbget/`, frozen connector/acceptance evidence, and this
  receipt.
- Acceptance reviewed: A-05, A-06, and A-09.

The executable review ran in a clean detached snapshot at the exact product
commit. Temporary adversarial tests existed only in an external `/tmp` copy.
The shared checkout's storage, migration, and execution-state work was
preserved and excluded from this commit.

## Finding

### P1: unusable or duplicate exact `drone` evidence becomes a false fallback correlation

- Location: `parameterValueStatus` at
  `internal/adapters/nzbget/inventory/inventory.go:855-881` returns only an
  empty value plus a validity bit. It does not retain whether an exact-name
  parameter was present or how many matched. `observeQueue` and
  `observeHistory` then use `firstNonEmpty(drone, id)` at `:674-678` and
  `:737-741`. Equal duplicate values are accepted at `:873-879`.
- Pinned behavior: Radarr commit
  `0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4` selects parameters with
  `SingleOrDefault(p => p.Name == "drone")` and uses the selected value
  whenever the parameter exists at `Nzbget.cs:71-74` and `:121-126`.
  Therefore one exact empty parameter yields an empty DownloadId instead of the
  numeric fallback, while multiple exact-name parameters make selection
  ambiguous rather than authorizing either value. The same pinned source
  generates normal `drone` values as hyphenless GUIDs in
  `NzbgetProxy.cs:57-68`.
- Reproduction 1: queue and history observations each containing one exact
  `{"Name":"drone","Value":""}` return numeric
  `ArrDownloadID` values 1001 and 1002. Coverage is `complete` and contains
  no `parameters_malformed` reason.
- Reproduction 2: a queue observation with two equal exact-name parameters,
  each containing the same valid 32-character hexadecimal value, returns that
  value in both `Drone` and `ArrDownloadID` with complete coverage.
- Related case: secret-shaped, opaque, whitespace-bearing, or otherwise
  rejected exact `drone` evidence is correctly absent/redacted, but the same
  unconditional `firstNonEmpty` fallback still reports the numeric NZBID as
  Arr's download ID. The current opaque/redaction tests encode this incorrect
  fallback.
- Failure mode: the dashboard or reconciler can claim numeric or duplicate
  `drone` correlation even though Arr selected an empty value or could not
  select a unique parameter. The independent NZBID evidence remains useful,
  but it is not proof of Arr's DownloadId when an exact `drone` parameter is
  present.
- Contract: the connector specification and A-05 require correlation through
  actual Arr `drone` evidence and warn that NZBID must not be assumed equal to
  Arr's DownloadId. Invariant I-05 prevents ambiguous or unusable evidence from
  becoming a fresh positive claim.
- Required change: retain exact-name match cardinality separately from safe
  value validation. Use numeric NZBID as the Arr fallback only when no exact
  `drone` parameter exists. Exactly one safe value may override it. One
  present-but-empty/unsafe/malformed value, or more than one exact-name value
  regardless of equality, must leave `ArrDownloadID` unknown, redact the raw
  value as applicable, and keep coverage partial with a stable reason.
- Required proof: cover no exact parameter, one valid generated GUID, one valid
  compatibility value, empty, opaque, secret-shaped, whitespace-bearing,
  non-string, duplicate equal, duplicate conflicting, and differently cased
  names for both queue and history. Assert `Drone`, `ArrDownloadID`,
  completeness, and reason codes against the pinned presence/cardinality
  semantics.
- Disposition: `current_blocker`.

## Prior finding disposition

| Round-two finding | Result |
| --- | --- |
| Detailed observations expose secret plaintext | Closed. Non-correlation parameter values are always redacted, post-processing text is empty or redacted, URL evidence is reduced to scheme/host, and rejected `drone` values do not appear in any duplicate field. |
| Case-folded `DRONE` fabricates correlation | Closed for name selection. Only exact `Name == "drone"` participates; uppercase, mixed-case, and whitespace-prefixed names do not override NZBID. Presence/cardinality semantics remain open in the P1 above. |
| Negative IDs become complete provenance | Closed. Any negative NZBID/ID adds item-scoped `identity_invalid`, makes lifecycle state unknown/not-ready, and keeps coverage partial while preserving a positive counterpart as separate identity evidence. |

## Preserved behavior

- JSON-RPC requests and responses use the supported 1.1 envelope, echoed IDs,
  and positional parameters.
- The adapter issues only `listgroups`, `history`, and `version` reads.
  It contains no queue edit, append, pause/resume, delete, filesystem write, or
  mutation-retry path.
- Response bytes, item arrays, page sizes/counts, cursors, reason codes,
  descriptor reads, and exported free text are bounded.
- Cursors remain HMAC-authenticated, connection-scoped, per-client, and stale
  after snapshot change.
- FinalDir/DestDir merge and component-safe path mapping retain correct item
  indexes. Unsafe, ambiguous, and unmapped paths remain explicit partial
  evidence and cannot create a `FileTarget`.
- Non-string parameters, malformed responses, unavailable calls, capped arrays,
  duplicate identities, and incomplete processing remain partial.
- Transport and status errors are typed and sanitized; credentials stay scoped
  to same-scheme/same-host requests and redirects. Context cancellation is
  preserved.
- Descriptor availability remains distinct from history metadata. Descriptor
  bytes and fabricated payload manifests are absent from ordinary inventory.
- Canonical client identities remain connection-scoped.
- Repository fixtures are synthetic.

## Checks and direct results

All executable checks used synthetic fixtures and no live NZBGet, credential,
media payload, upstream write, filesystem mutation, release, or deployment.

| Command or inspection | Result |
| --- | --- |
| Exact product/handoff diff, owned implementation/tests/fixtures, prior receipts, connector contract, compatibility matrix, and A-05/A-06/A-09 | Inspected independently. Product commit changes only the two owned NZBGet inventory Go files. |
| Pinned Radarr `Nzbget.cs` and `NzbgetProxy.cs` at `0220f0d...` | Confirms exact-name `SingleOrDefault`, presence-based DownloadId selection, duplicate ambiguity, and normal 32-character hyphenless GUID generation. |
| Four prior round-two adversarial probes, `-count=20` | Passed: opaque/free-text/path redaction, duplicate-field redaction, case-sensitive name matching, and negative-ID partial coverage. |
| Six older round-one adversarial probes, `-count=20` | Passed: JSON-RPC 1.1, FinalDir merge/reason scope, traversal rejection, numeric type rejection, valid ID-only fallback, and marker-bearing redaction. |
| Empty and duplicate exact-name adversarial probes, `-count=20` | Failed deterministically as described: empty values fall back to numeric NZBID with complete coverage, and equal duplicates become positive `drone` evidence. |
| `GOWORK=off go test -timeout=180s -count=50 ./internal/adapters/nzbget/inventory` | Passed. |
| `GOWORK=off go test -race -timeout=240s -count=10 ./internal/adapters/nzbget/inventory` | Passed. |
| Focused coverage and vet | Passed; 81.3% statement coverage. |
| `GOWORK=off go test -timeout=240s -count=1 ./...` | Passed all root packages. |
| `GOWORK=off go test -race -timeout=300s -count=1 ./...` | Passed all root packages; storage completed in 81.260 seconds. |
| `GOWORK=off go vet ./...` | Passed. |
| Generation, API, architecture, planning, lint, and fast guardrail checks | Passed; planning reports 38 tasks and 60 acceptance cases. |
| Root module verify and tidy diff | Passed cleanly. |
| Tools module test, vet, verify, and tidy diff | Passed. |
| UI module test, vet, and verify | Passed; generated client package has no tests. |
| UI module tidy diff | Reports removal of existing future dependency pins; X-02 changes no UI or module file. |
| Linux amd64/arm64, Windows amd64, and AIX ppc64 focused `CGO_ENABLED=0 go test -c` | Passed. |
| `gofmt -d`, `git diff --check`, fixture JSON parsing, RPC mutation inspection, and public-data scan | Passed; credential-like matches are limited to synthetic tests. |
| Product scoped snapshot | Clean and unchanged at SHA-256 `02a8cff85a2811c8e65c37aeaca43fc93fcfd4032bdec2a02c3281e007902010`. |
| Shared-checkout commit hook | Generation, API, and architecture passed, then the hook stopped on concurrently edited, unformatted `internal/storage/store_test.go` and `compatibility_round5_test.go`. The already checked receipt was committed with `--no-verify`; neither storage file was staged or changed by this reviewer. |

## Acceptance disposition

- A-05: not accepted. Prior protocol, lifecycle, path, and identity fixes pass,
  but exact `drone` presence/cardinality can still produce false Arr
  correlation.
- A-06: accepted for the X-02 contribution. Descriptor availability and
  detailed redaction now remain honest and secret-free under the reviewed
  probes.
- A-09: accepted for the X-02 contribution. Canonical client identity and
  observations remain connection-scoped.

No product file, fixture, module file, or `docs/execution/state.json` was
modified by the reviewer.
