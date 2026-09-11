# X-02 independent review, round one

## Decision

`changes_requested`.

Product commit `84861c964407b6307e56bc70b109fae50ac278fd` is read-only,
bounded, connection-scoped, and preserves useful queue/history/post-processing
evidence. It cannot yet satisfy the frozen NZBGet contract. The client requires
a JSON-RPC 2.0 response envelope, while the pinned NZBGet implementation emits
JSON-RPC 1.1. Five additional evidence and safety defects remain: merged
`FinalDir` evidence can retain a stale `DestDir` target, traversal-bearing paths
are accepted, malformed numeric `drone` values become Arr identities, an
alias-only ID is falsely called contradictory, and exported detailed
observations retain plaintext secrets.

## Review identity

- Reviewer: `/root/f01_reviewer`, independent of the X-02 implementer.
- Reviewed product: `84861c964407b6307e56bc70b109fae50ac278fd`.
- Product parent: `7c1d817f79189693bc15bdbd4cb4bdd8c94b66f7`.
- Worker handoff: `b5e6907cca3ace1891255c3aa64002c3ec3600e4`.
- Review scope: `internal/adapters/nzbget/` and
  `tests/fixtures/nzbget/`, against the frozen ports, connector contract,
  compatibility evidence, A-05, A-06, and A-09.
- Review environment: clean detached reviewer-owned snapshot of the exact
  product commit.
- Scoped product snapshot before and after review:
  `bbf31c5c363dbb872e14374518f218e8de356a1c66269072d65a11c118074aeb`.
- Release stage: `pre-v0.1.0`, target `v0.0.1`.
- Boundary receipt: `transcript_not_read`, `no_polling`, `git_native`.

## Findings

### P1: the client rejects NZBGet's supported JSON-RPC 1.1 envelope

- Location: request/response DTOs and validation at
  `internal/adapters/nzbget/inventory/inventory.go:1176` through `:1189`,
  request construction at `:1200` through `:1202`, and response validation at
  `:1242` through `:1247`.
- Evidence: the source pinned by `docs/research/upstream-evidence.md` at NZBGet
  commit `b609226e18da11955ce8dda2c7df959258655579` constructs JSON replies in
  `daemon/remote/XmlRpc.cpp` with `"version" : "1.1"`, the request ID, and
  `result` or `error`. It does not emit a `jsonrpc` member. A reviewer server
  returned the valid synthetic envelope
  `{"version":"1.1","id":1,"result":"24.2.1"}`. `Client.Version` rejected
  it as `outcome_unknown: upstream response was malformed` because line 1246
  requires `JSONRPC == "2.0"`.
- Failure mode: every successful response from the pinned supported boundary
  is rejected before queue, history, version, mapping, correlation, or
  descriptor behavior can run. The shipped fixture handler models a different
  protocol and therefore passes while the real API shape fails.
- Contract: connector specification lines 60-63 require supported positional
  NZBGet JSON-RPC. X-02 cannot contribute A-05/A-06/A-09 if the upstream
  envelope is rejected.
- Required change: model and validate NZBGet JSON-RPC 1.1 exactly, including
  its `version` member and echoed ID, while preserving bounded decoding and
  sanitized error handling. Do not substitute generic JSON-RPC 2.0 semantics.
- Required proof: use synthetic 1.1 success and error envelopes matching the
  pinned server source for `version`, `listgroups([0])`, and `history([false])`;
  reject missing/wrong version, mismatched ID, null result, and malformed
  result without exposing a response body.
- Disposition: `current_blocker`.

### P1: merging an unmapped FinalDir preserves an unrelated DestDir target

- Location: history reasons are labeled before correlation at
  `internal/adapters/nzbget/inventory/inventory.go:503` through `:519`; merge
  replacement is at `:558` through `:568`.
- Reproduction: a queue item with ID 501 and mapped
  `DestDir=/downloads/incoming` was correlated with history ID 501 and the
  preferred but unmapped `FinalDir=/outside/final`. The merged observation set
  `ContentPath=/outside/final` but retained
  `MappedPath={RootID:library, RelativePath:managed/incoming}`. It also emitted
  `item_1_final_path_unmapped`, although correlation left only item 0.
- Failure mode: the actionable-looking target no longer describes the selected
  content path. The UI or a later workflow can display or bind a destination
  derived from the fallback path after stronger `FinalDir` evidence replaced
  it. The path failure points at a nonexistent item.
- Contract: connector specification lines 62-63 and A-05 require the mapped
  final path, with `DestDir` used only when `FinalDir` is absent. Missing
  evidence must remain explicit rather than retain a stale target.
- Required change: merge the path value and its mapping result atomically. A
  present `FinalDir` that is unmapped or ambiguous must clear the fallback
  `MappedPath`. Attach all history reasons to the correlated item's actual
  index.
- Required proof: cover mapped, unmapped, ambiguous, and invalid `FinalDir`
  over a mapped `DestDir`; assert `ContentPath`, target, reason code, and item
  index agree after queue/history correlation.
- Disposition: `current_blocker`.

### P1: traversal-bearing source and observed paths are treated as canonical

- Location: mapping validation and selection at
  `internal/adapters/nzbget/inventory/inventory.go:1363` through `:1438`.
- Reproduction: `New` accepted a mapping with
  `SourcePrefix=/downloads/../secret`. Independently, `mapPath` accepts remote
  paths containing dot components and normalizes the suffix through
  `path.Join`, so a value such as `/downloads/movies/../secret/movie.mkv` can
  become the apparently safe target `managed/secret/movie.mkv`.
- Failure mode: distinct noncanonical upstream paths collapse into a trusted
  root-relative target. The target no longer proves the component relationship
  to the configured source prefix, which can misidentify the payload selected
  for later filesystem review.
- Contract: A-05 requires component-safe explicit mapping; the filesystem
  contract rejects traversal and ambiguous path mappings. The handoff's claim
  that source traversal is rejected is not true for the exact commit.
- Required change: reject noncanonical absolute source prefixes and observed
  paths before prefix matching. Do not clean untrusted remote input into a
  different accepted identity.
- Required proof: reject `.`/`..`, repeated-separator aliases, backslashes,
  NULs, relative paths, and prefix lookalikes while retaining valid root and
  longest-component mappings.
- Disposition: `current_blocker`.

### P1: malformed numeric drone values fabricate Arr correlation

- Location: correlation reads `drone` at
  `internal/adapters/nzbget/inventory/inventory.go:624` through `:629` and
  `:680` through `:685`; `rawString` coerces JSON numbers at `:1481` through
  `:1495`.
- Reproduction: history ID 9 with
  `{"Name":"drone","Value":12345}` produced
  `ArrDownloadID="12345"` and no `parameters_malformed` reason. The pinned
  NZBGet `LISTGROUPS.md` declares parameter `Value` as a string.
- Failure mode: malformed or schema-drifted evidence becomes a positive Arr
  identity and can correlate the download to the wrong manager record.
- Contract: connector specification lines 64-74 require distinct typed
  parameters and correlation through actual evidence. Unknown input must stay
  unknown; A-05 requires correct `drone` correlation.
- Required change: accept a parameter value only when its JSON type is string.
  A malformed `drone` must remain absent, mark coverage partial, and use the
  documented NZBID/ID fallback rather than stringifying it.
- Required proof: test string, number, boolean, object, array, null, empty,
  duplicate, and conflicting `drone` values.
- Disposition: `current_blocker`.

### P2: a valid deprecated-ID fallback is falsely reported as an alias mismatch

- Location: queue and history reason construction at
  `internal/adapters/nzbget/inventory/inventory.go:643` through `:645` and
  `:703` through `:705`.
- Reproduction: a complete history record with `NZBID=0`, `ID=9`, valid status,
  timestamp, kind, and mapped path emitted
  `item_0_history_id_alias_mismatch` and final partial coverage. The same code
  intentionally used ID 9 as the canonical fallback and marked processing
  complete.
- Failure mode: absence of the modern field is mislabeled as contradictory
  evidence. A historical record that the contract permits as a conservative
  fallback can never produce complete coverage even when every available field
  is valid.
- Contract: connector specification lines 64-67 define ID as an alias rather
  than a second identity. The handoff states that a disagreement exists only
  when both positive fields differ.
- Required change: emit mismatch only when both NZBID and ID are positive and
  unequal. Preserve the explicit fallback when NZBID is absent.
- Required proof: cover NZBID-only, ID-only, equal pair, unequal positive pair,
  zero, and negative values for queue and history.
- Disposition: `current_blocker`.

### P1: detailed observations expose plaintext secret-bearing fields

- Location: raw observation copies at
  `internal/adapters/nzbget/inventory/inventory.go:746` through `:772`, exposed
  through exported `ListDetailed`; the exported DTOs are at lines 92-153.
- Reproduction: a synthetic queue parameter
  `*Unpack:Password=hunter2` and `PostInfoText=password=hunter2` were returned
  unchanged. A history URL with userinfo and an `apikey` query is likewise
  copied unchanged into `HistoryObservation.URL`.
- Failure mode: NZB passwords, credentialed fetch URLs, query tokens, and
  script text can reach an ordinary detailed response, log dump, or UI model.
  Error normalization is sanitized, but successful observations are not.
- Contract: connector specification lines 27-29 requires secret redaction;
  invariant I-13 requires secrets and descriptor content to be absent from
  ordinary responses, logs, fixtures, and previews.
- Required change: retain typed parameter names while redacting values for
  secret-bearing parameters, expose only explicitly safe correlation values
  such as validated `drone`, remove userinfo/query/fragment from URLs, and do
  not export arbitrary post-processing text without a bounded redaction policy.
- Required proof: test mixed-case password/token/key parameter names,
  credentialed URLs, query secrets, and free-text secrets; assert ordinary DTOs
  and formatted values contain none of the sentinel plaintext.
- Disposition: `current_blocker`.

## Confirmed behavior

- The adapter issues POST only to the configured `/jsonrpc` endpoint and only
  with the read methods `listgroups`, `history`, and `version`. It contains no
  queue edit, append, delete, rename, relocation, filesystem mutation, or
  retry-on-write path.
- Basic credentials are scoped to requests, redirects are restricted to the
  same scheme and host, transport/status failures are typed without raw bodies
  or endpoints, and context cancellation is returned directly.
- Response bytes, item count, page size, page count, cursor size, reason count,
  and descriptor reads are bounded. Cursors are per-client HMAC authenticated,
  connection scoped, and invalid after another client/restart. Snapshot changes
  terminate continuation with partial coverage rather than complete absence.
- Known queue/post-processing/history states keep processing readiness
  conservative. Unknown states, capped arrays, missing sources, and malformed
  arrays remain partial. Duplicate queue/history identities are detected.
- NZBID-based persisted-style identities are connection scoped. Queue and
  history observations remain distinct after correlation, and a valid history
  `drone` can replace the canonical-ID fallback.
- History does not fabricate an exact payload manifest. Descriptor disabled,
  unconfigured, missing, malformed, and available metadata states remain
  distinct, and descriptor bytes are absent from common inventory pages.
- All repository fixtures are synthetic. The public-data scan found only
  explicit synthetic credentials in tests and ordinary source identifiers.

## Checks and direct results

All executable product checks ran in the clean detached snapshot at the exact
candidate. No live NZBGet instance, credential, media payload, upstream write,
filesystem mutation, release, or deployment was used.

| Command or inspection | Result |
| --- | --- |
| Exact product/handoff diff, complete adapter/tests/fixtures, frozen ports, connector contract, compatibility evidence, and A-05/A-06/A-09 | Inspected independently. The correction changes two owned adapter files; fixtures are unchanged. |
| Pinned NZBGet source `b609226...`, `daemon/remote/XmlRpc.cpp` and `docs/api/LISTGROUPS.md` | Confirms response marker `version: 1.1`, echoed request ID, positional parameters, and string parameter values. |
| Six temporary tests in an external exact-commit snapshot | Failed as described: valid 1.1 envelope rejection, stale merged mapping/reason index, traversal acceptance, numeric `drone` fabrication, false alias mismatch, and plaintext detailed evidence. The reviewed tree was never modified. |
| `GOWORK=off go test -timeout=180s -count=25 ./internal/adapters/nzbget/inventory` | Passed. |
| `GOWORK=off go test -race -timeout=240s -count=10 ./internal/adapters/nzbget/inventory` | Passed. |
| Focused coverage and vet | Passed; 77.4% statement coverage. |
| `GOWORK=off go test -timeout=240s -count=1 ./...` | Passed all root packages. |
| `GOWORK=off go test -race -timeout=300s -count=1 ./...` | Passed all root packages; storage completed after 64.836 seconds. |
| `GOWORK=off go vet ./...` | Passed. |
| `GOWORK=off ./scripts/generate.sh --check` | Passed: generation checks passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, and local links resolve. |
| Root `GOWORK=off go mod verify` and `go mod tidy -diff` | Passed cleanly. |
| Tools module test, vet, verify, and tidy | Passed. |
| UI module test/vet/verify | Passed; generated client package has no tests. |
| UI module `go mod tidy -diff` | Reports removal of existing future UI pins; X-02 changes no UI or module file. |
| Linux amd64/arm64, Windows amd64, and AIX ppc64 package `go test -c`, `CGO_ENABLED=0` | Passed. |
| `gofmt -d`, scoped `git diff --check`, fixture JSON parsing, request-method inspection, and public-data scan | Passed; credential-like matches are limited to explicit synthetic tests. |
| Final scoped Git snapshot | Clean and unchanged at `bbf31c5c363dbb872e14374518f218e8de356a1c66269072d65a11c118074aeb`. |

The shared checkout contained an unrelated coordinator edit to
`docs/execution/state.json`. It was not read for review, staged, modified, or
removed. No product file, fixture, module file, or execution state was modified
by the reviewer.

## Acceptance contribution assessment

- A-05: not accepted. The supported upstream envelope is rejected; numeric
  malformed `drone` evidence can become a positive Arr identity; the alias-only
  fallback is falsely partial; and correlated `FinalDir` can retain a target
  from `DestDir`.
- A-06: not accepted. Descriptor availability states and metadata are present,
  but the adapter cannot obtain them through the pinned JSON-RPC boundary, and
  ordinary detailed observations expose secret-bearing plaintext.
- A-09: the scoped identity construction itself is sound, but the contribution
  is not executable against the pinned NZBGet protocol. Multi-instance
  identity evidence therefore remains unproved end to end.

## Next review event

Correct the JSON-RPC 1.1 boundary, correlated-path merge and reason indexing,
canonical path validation, strict string parameter decoding, alias-only
fallback, and detailed-observation redaction. Add deterministic regressions for
each reproduction, freeze the correction commit, and request round-two
independent review.
