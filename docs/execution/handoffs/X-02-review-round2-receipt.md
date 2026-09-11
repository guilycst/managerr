# X-02 independent review, round two

## Decision

`changes_requested`.

Product commit `c92622b468523f37539df7a0f468bf8fe83c0569` fixes the
JSON-RPC 1.1 boundary, correlated path/reason handling, traversal rejection,
typed non-string `drone` handling, and the valid deprecated-ID fallback from
round one. The detailed-observation redaction finding remains open: multiple
ordinary exported fields still return synthetic secret plaintext. Two
correlation-quality cases also remain: parameter-name normalization can assert
an Arr relationship that the pinned Arr client does not use, and negative ID
evidence is silently accepted as complete.

## Review basis

- Reviewed product: `c92622b468523f37539df7a0f468bf8fe83c0569`.
- Reviewed implementation handoff:
  `f0b92348d06729ee0743d8da1049b3067b4ce4e4`.
- Correction base: `84861c964407b6307e56bc70b109fae50ac278fd`.
- Prior independent receipt:
  `c68bccdbdc1a9e49cc79e26e317b480caa697315`.
- Scope: `internal/adapters/nzbget/`, `tests/fixtures/nzbget/`, frozen
  connector/acceptance contracts, and this receipt.
- Acceptance reviewed: A-05, A-06, and A-09.

The executable review used a clean detached snapshot at the exact product
commit. Adversarial tests were added only to an external disposable copy under
`/tmp`; they were not added to the reviewed tree. The shared checkout had
unrelated Arr, storage, migration, fixture, and execution-state work, which was
preserved. The implementation handoff already occupies
`X-02-review-round2.md`; this distinct receipt path preserves both current
artifacts.

## Findings

### P1: ordinary detailed observations still expose secret plaintext

- Location: `observeQueue` and `observeHistory` copy validated `drone`
  directly into exported `Drone` and `ArrDownloadID` at
  `internal/adapters/nzbget/inventory/inventory.go:668-674` and `:725-731`.
  Detailed queue/history construction and the heuristic sanitizers are at
  `:796-948`.
- Reproduction: a synthetic exact-name parameter
  `drone=password=opaque-drone-secret` is redacted in
  `Queue.Parameters`, but the same plaintext is returned in both
  `DownloadObservation.Drone` and `ArrDownloadID`. A parameter named
  `pwd` with opaque value `opaque-7f3a91c5e2`, an unmarked
  `PostInfoText` with the same value, and a history URL containing the value
  in its path are also returned unchanged by `ListDetailed`.
- Failure mode: ordinary response models can expose NZB credentials, opaque
  post-processing data, fetch-path tokens, or secret-shaped correlation values.
  Capping text at 4096 runes bounds its size but does not make it safe.
- Contract: invariant I-13 requires secrets to be absent from ordinary
  responses, logs, fixtures, and previews. A-06 requires no plaintext in
  ordinary responses/logs. The round-one correction explicitly required
  free-text sentinels to be absent and only explicitly safe correlation values
  to be exposed.
- Required change: build exported detail from an allowlist of safe evidence.
  Validate `drone` as a bounded safe correlation identifier before exposing
  or using it; otherwise omit it and mark the observation partial. Do not
  return arbitrary `PostInfoText` or arbitrary parameter values. Reduce
  history URL evidence to a representation whose path cannot carry a secret,
  or redact it entirely. Apply one policy before values are copied into any
  duplicate exported field.
- Required proof: use opaque sentinels without marker words in password aliases,
  post-processing text, URL userinfo/path/query/fragment, and `drone`. Assert
  every direct field and a formatted full `DetailedPage` omit the sentinel.
- Disposition: `current_blocker`; the round-one secret-redaction finding is
  only partially corrected.

### P1: case-folding the `drone` parameter fabricates Arr correlation

- Location: `parameterValueStatus` uses
  `strings.EqualFold(strings.TrimSpace(parameter.Name), name)` at
  `internal/adapters/nzbget/inventory/inventory.go:841-865`; its result
  overrides the canonical NZB ID at `:668-674` and `:725-731`.
- Reproduction: queue evidence with `Name="DRONE"` and
  `Value="not-used-by-arr"` yields
  `ArrDownloadID="not-used-by-arr"` and no malformed-parameter reason.
  Pinned Radarr commit `0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4`
  selects queue and history parameters with the exact predicate
  `p.Name == "drone"` in `Nzbget.cs:71` and `:121`.
- Failure mode: Mastarr can display an item as correlated to an Arr download ID
  that the target Arr implementation never selected. A manual parameter whose
  name differs only by case can therefore become false tracked evidence.
- Contract: the connector specification and A-05 require correlation through
  the actual Arr `drone` evidence. Missing evidence must remain unknown rather
  than be normalized into a stronger relationship.
- Required change: match the parameter-name semantics of the pinned Arr client
  exactly. Keep duplicate/conflict and string-type checks conservative without
  case-folding or trimming a different upstream parameter name into `drone`.
- Required proof: cover exact `drone`, leading/trailing whitespace,
  `DRONE`, mixed case, duplicate equal values, and duplicate conflicts for
  queue and history; only the exact-name evidence may override the NZB ID.
- Disposition: `current_blocker`.

### P2: negative ID fields are silently normalized into complete provenance

- Location: `effectiveNZBID` at
  `internal/adapters/nzbget/inventory/inventory.go:1613-1618` prefers any
  positive NZBID and otherwise returns the deprecated ID. Alias contradiction
  checks at `:661-665`, `:688-690`, `:718-722`, and `:749-751` inspect
  only unequal positive pairs.
- Reproduction: complete history records with `NZBID=741, ID=-1` and
  `NZBID=-1, ID=742` both return completed, processing-done observations with
  IDs 741/742 and final `Coverage.Completeness="complete"`, with no reason
  for the invalid negative field.
- Failure mode: malformed identity evidence is made indistinguishable from a
  valid absent alias/fallback. This hides schema drift and can overstate the
  confidence of later correlation.
- Contract: the connector model keeps NZBID and deprecated ID as typed evidence,
  and invariant I-05 forbids partial/unknown evidence from becoming a safety
  claim. The round-one receipt required zero and negative cases for both queue
  and history; the correction tests cover the positive ID-only case but omit
  negative values.
- Required change: distinguish a missing/zero alias from an explicitly negative
  value. A valid positive counterpart may still supply the canonical identity,
  but the malformed field must keep coverage partial with a stable reason.
- Required proof: exercise NZBID-only, ID-only, equal pair, unequal positive
  pair, zero, and negative values independently for queue and history.
- Disposition: `current_blocker`.

## Round-one finding disposition

| Round-one finding | Result |
| --- | --- |
| Supported JSON-RPC 1.1 envelope rejected | Closed. Requests use `version: "1.1"`, positional parameters and an ID; matching 1.1 success/error envelopes are accepted and wrong versions/IDs are rejected. |
| Unmapped `FinalDir` retains stale `DestDir` mapping and wrong reason index | Closed. Merge replaces content path and mapping atomically, returns the actual item index, and recomputes path-scoped reasons. |
| Traversal-bearing source/observed paths accepted | Closed for the requested aliases. Relative, dot-component, repeated-separator, backslash, NUL, and prefix-lookalike inputs remain unmapped/rejected while canonical component paths work. |
| Numeric `drone` fabricates Arr identity | Closed. Only JSON strings enter correlation; non-string values remain absent and partial. |
| Valid deprecated-ID fallback reported as mismatch | Closed for valid positive ID-only, NZBID-only, equal, and unequal-positive cases. Negative-field evidence remains open as the P2 finding above. |
| Detailed observations expose plaintext secret-bearing fields | Open. Named marker cases are redacted, but direct correlation fields and opaque/free-text/path cases remain plaintext. |

## Confirmed behavior

- The adapter sends HTTP POST only to the configured JSON-RPC endpoint and
  invokes only the read methods `listgroups`, `history`, and `version`.
  No queue edit, append, pause/resume, delete, filesystem write, or mutation
  retry path exists in this adapter.
- Queue and history arrays, response bytes, page size/count, cursors, reason
  codes, descriptor reads, and post-processing text length are bounded.
  Continuations remain HMAC-authenticated, connection-scoped, per-client, and
  stale when the upstream snapshot changes.
- FinalDir/DestDir selection is component-safe after correction. Invalid paths
  remain visible only as untrusted evidence and produce partial reason codes;
  no `FileTarget` is fabricated.
- Transport/status/error details remain typed and sanitized. Basic credentials
  stay request-scoped, redirects remain same-scheme/same-host, and context
  cancellation is preserved.
- Processing readiness remains conservative for queue, post-processing, failed,
  malformed, unavailable, duplicate, and capped observations. History does not
  fabricate an exact payload manifest.
- Retained descriptor states remain capability-dependent and bounded; descriptor
  bytes do not enter common inventory pages.
- Connection-scoped canonical identities remain stable across instances.
- Repository fixtures are synthetic.

## Checks and direct results

All executable product checks ran in the clean detached snapshot at the exact
candidate. No live NZBGet instance, credential, media payload, upstream write,
filesystem mutation, release, or deployment was used.

| Command or inspection | Result |
| --- | --- |
| Exact product/handoff diff, full owned adapter/tests/fixtures, ports, connector specification, compatibility matrix, A-05/A-06/A-09, and prior receipt | Inspected independently. Product correction changes only the two owned inventory Go files; fixtures are unchanged. |
| Pinned NZBGet source `b609226...` and pinned Radarr source `0220f0d...` | Confirmed JSON-RPC response `version: 1.1`, positional list/history signatures, string parameter values, deprecated ID semantics, and exact case-sensitive `p.Name == "drone"` selection in both queue and history. |
| Six round-one adversarial tests in an external exact-commit copy, `-count=50` | Passed: 1.1 envelope, path traversal, merged FinalDir target/reason, numeric `drone`, valid ID-only fallback, and marker-bearing secret cases. |
| Four additional adversarial tests in the external copy, `-count=10` | Failed deterministically as described: case-folded `DRONE` correlation, plaintext `drone` duplicate fields, opaque detailed evidence, and silent negative-ID normalization. |
| `GOWORK=off go test -timeout=180s -count=25 ./internal/adapters/nzbget/inventory` | Passed. |
| `GOWORK=off go test -race -timeout=240s -count=10 ./internal/adapters/nzbget/inventory` | Passed. |
| Focused coverage and vet | Passed; 79.3% statement coverage. |
| `GOWORK=off go test -timeout=240s -count=1 ./...` | Passed all root packages. |
| `GOWORK=off go test -race -timeout=300s -count=1 ./...` | Passed all root packages; storage completed in 69.653 seconds. |
| `GOWORK=off go vet ./...` | Passed. |
| `./scripts/generate.sh --check`, API check, architecture check, and planning check | Passed; planning reports 38 tasks and 60 acceptance cases. |
| `./scripts/check-lint.sh` and `./scripts/check-guardrails.sh --fast` | Passed. |
| Root `GOWORK=off go mod verify` and `go mod tidy -diff` | Passed cleanly. |
| Tools module test, vet, verify, and tidy | Passed. |
| UI module test, vet, and verify | Passed; generated client package has no tests. |
| UI module `go mod tidy -diff` | Reports removal of existing future UI dependency pins; X-02 changes no UI or module file. |
| Linux amd64/arm64, Windows amd64, and AIX ppc64 focused `CGO_ENABLED=0 go test -c` | Passed. |
| `gofmt -d`, `git diff --check`, fixture JSON parsing, RPC-call/mutation inspection, and public-data scan | Passed; credential-like matches are limited to synthetic tests and sanitization identifiers. |
| Product scoped snapshot | Clean and unchanged at SHA-256 `bd1123d191229c8387ad45162d3cac39f43d6f5a7d5ef6c008a4975935a5b49e`. |

## Acceptance disposition

- A-05: not accepted. JSON-RPC, processing, final-path, type, and valid-alias
  corrections pass, but case-normalized `DRONE` can fabricate the Arr
  download ID and negative ID evidence can overstate completeness.
- A-06: not accepted. Descriptor availability remains honest, but ordinary
  detailed observations can still expose plaintext secret evidence.
- A-09: the connection-scoped identity contribution remains accepted; the
  false `DRONE` override affects correlation confidence rather than
  cross-instance scope.

No product file, fixture, module file, or `docs/execution/state.json` was
modified by the reviewer.
