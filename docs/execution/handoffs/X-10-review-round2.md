# X-10 independent review, round two

## Decision

`changes_requested`.

All four P1 and four P2 findings from round one are closed. The corrected SID
gate, hash request bounds, status-first error handling, version shape, piece
ranges, unsupported outcome, OpenAPI input limits and endpoint paths passed
independent adversarial probes. Three new P1 and two new P2 findings remain at
the same HTTP/evidence boundary: undocumented 2xx statuses become canonical
success, JSON identity fields are not decoded exactly, an empty inventory hash
becomes a usable torrent record, a negative injected HTTP timeout removes the
transport deadline, and case-equivalent hash identities bypass duplicate
rejection.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Reviewed correction commit:
  `aa9195b94d62d8a1c53825d638f111592f593617`.
- Product parent:
  `a8c690b4ef39b3fe87aae5a3a0dea8583fccdf59`.
- Product tree:
  `6f4f51585ae8eb2584e5936bb6b25754a64a4bfa`.
- Reviewed correction handoff commit:
  `43c5731c77249892f9c920719901e961ea45b630`.
- Coordinator state commit inspected:
  `3627fd10b84204fc8815ef0b363d422251cb58b1`.
- Round-one receipt:
  `8aa5867ebc587d0e61700ed94f15ef2dc8200237`.
- Review scope: `clients/qbittorrent/`, task X-10, A-04/A-09/A-28/A-45,
  the correction handoff and every round-one finding.
- The correction changes exactly five assigned qBittorrent files. It changes no
  root adapter, shared API, execution state, task, plan or script.
- Scoped correction diff SHA-256:
  `ca1df929fe7f4e914f20600b60a2194de1e54473e9f28e1467825d39b6f6e937`.
- Scoped product archive SHA-256:
  `42826a26b91b4fc160ec3d51e1b50cc99a8fd2950bf5101bea4b933cb79bd4db`.
- Clean checks ran in a detached reviewer-owned worktree at the exact correction
  commit. Independent probes ran in a disposable Git archive; the probe file is
  not product evidence and was not committed.
- No live qBittorrent instance, credential, tracker coordinate, media payload,
  torrent mutation, filesystem action, release or deployment was used.

## Findings

### P1: undocumented 2xx responses become canonical success and complete evidence

- Location: `clients/qbittorrent/client.go:526-539` and `:559-577`.
- Evidence: both login and read paths treat the entire 200-299 range as
  successful. The module OpenAPI defines HTTP 200 as the only success response
  for login and every exposed read. No response metadata records that another
  success status was observed.
- Independent reproduction:
  1. Return HTTP 201, a usable SID and `Ok.` from the login route.
  2. Return HTTP 206 and syntactically valid `[]` from the torrent inventory
     route after a normal login.
- Observed result:

  ```text
  201 login -> nil error, authenticated=true
  206 inventory -> empty slice, nil error
  ```

- Failure mode: a proxy or upstream partial response can be normalized as a
  complete empty inventory. Downstream reconciliation can then mistake missing
  client evidence for absence. The login path similarly certifies a response
  outside its frozen compatibility contract.
- Contract: the module OpenAPI declares only 200 success responses;
  `connectors.md:20-30` requires typed capability/evidence and bounded HTTP
  behavior; `connectors.md:160-164` forbids partial coverage from becoming false
  absence.
- Required change: accept only each operation's documented success status before
  interpreting its body. Return a sanitized typed outcome for 201/202/206 and
  other undocumented 2xx responses; do not decode or normalize them as complete
  inventory/version/session evidence.
- Required proof: exact 200 login and reads; 201 login; 202 version; 204 and 206
  for inventory, properties, files, categories and tags; valid-looking bodies on
  every rejected status; no false empty or complete result.
- Disposition: `current_blocker`.

### P1: strict JSON decoding changes or ambiguously selects identity evidence

- Location: `clients/qbittorrent/client.go:819-832`.
- Evidence: `decodeJSON` uses `encoding/json.Decoder` with unknown-field and
  trailing-value checks, but it does not reject invalid UTF-8, duplicate object
  members or case-folded property names. Those behaviors are outside the exact
  closed OpenAPI object contract.
- Independent reproduction: mutate the synthetic torrent fixture three ways:
  1. include conflicting `hash` members with `a...` followed by `b...`;
  2. replace the `hash` property name with `HASH`;
  3. place raw byte `0xff` inside the hash JSON string.
- Observed result:

  ```text
  duplicate hash -> second b... value returned
  HASH property -> accepted as hash
  invalid UTF-8 -> returned as replacement character U+FFFD
  ```

- Failure mode: the client silently chooses or fabricates the identifier used to
  associate a torrent. Missing or contradictory identity evidence must stay
  unknown; it cannot be normalized by decoder implementation details.
- Contract: A-04 and `connectors.md:34-37` require retained exact v1/v2 identity.
  The OpenAPI object schemas are closed and use exact property names. The X-10
  deliverable requires strict synthetic WebUI decoding and typed malformed
  outcomes.
- Required change: reject non-UTF-8 JSON before decoding, reject duplicate member
  names within each object, and enforce exact case-sensitive OpenAPI property
  names. Keep map keys and repeated names in different objects valid where the
  schema permits them.
- Required proof: duplicate identical/conflicting scalar members, nested
  duplicates, mixed-case known names, unknown names, invalid UTF-8 in identity
  and path fields, valid Unicode, repeated keys across separate array objects,
  and existing trailing-value/unknown-field cases.
- Disposition: `current_blocker`.

### P1: invalid inventory hash values are returned as scoped torrent records

- Location: `clients/qbittorrent/client.go:366-383` and `:894-945`, and
  `clients/qbittorrent/openapi.yaml` `TorrentInfo.hash`.
- Evidence: the input hash path is now bounded and delimiter-safe, but
  `ListTorrents` does not validate the required upstream `hash` before
  normalization. The output schema declares only `type: string`, without the
  nonempty/bounded identity rules applied to query hashes.
- Independent reproduction: replace the first fixture's hash value with an empty
  string and return HTTP 200 after a normal login.
- Observed result: `ListTorrents` returned one `Torrent` with `Hash: ""` and a nil
  error.
- Failure mode: a record without a usable client identity cannot be scoped,
  correlated, re-read or safely referenced. Returning it as normal evidence
  invites association by weaker fields such as name or path.
- Contract: A-04 and `connectors.md:34-37` require exact supported v1/v2 hash
  identities. Missing evidence is unknown, not a valid tracked item.
- Required change: put the retained hash rules in the response schema and verify
  every inventory record before returning any normalized result. Reject the
  whole observation as malformed rather than dropping a record or inventing an
  identity.
- Required proof: empty, delimiter-bearing, whitespace/control, invalid UTF-8,
  over-bound and malformed identity values, plus supported 40- and 64-character
  identities and a multi-record response in which one identity is invalid.
- Disposition: `current_blocker`.

### P2: a negative injected HTTP timeout disables the transport deadline

- Location: `clients/qbittorrent/client.go:293-304`.
- Evidence: `New` replaces only a zero `http.Client.Timeout`; a negative value is
  retained. Go's HTTP client applies its deadline only when `Timeout > 0`, so a
  negative value has the same no-deadline behavior as zero.
- Independent reproduction: construct with
  `HTTPClient: &http.Client{Timeout: -time.Second}` and inspect the isolated
  client.
- Observed result:

  ```text
  New error: <nil>
  effective HTTP timeout: -1s
  transport deadline: absent
  ```

- Failure mode: a malformed injected client configuration silently removes the
  module's default deadline for callers using a background context.
- Contract: `connectors.md:27-29` requires context deadlines and bounded HTTP
  clients; X-10 owns deadlines inside the standalone module.
- Required change: reject a negative timeout or apply the safe default for every
  nonpositive timeout. Keep explicitly positive caller timeouts intact.
- Required proof: negative, zero, small positive and default timeout values, plus
  a blocking synthetic server showing the effective deadline and cancellation
  outcome.
- Disposition: `current_blocker`.

### P2: case-equivalent hash identities bypass duplicate rejection

- Location: `clients/qbittorrent/client.go:685-706`.
- Evidence: the duplicate set is keyed by the original Go string. Hexadecimal
  torrent identities differing only by ASCII case pass as two members even
  though they represent the same hash. The official qBittorrent source at
  commit `3c4409d1a204a8b7d878727a538a8f4c5ae292f1` splits the parameter, converts
  each string to a `TorrentID`, and inserts the parsed identity in a set:
  [TorrentsController::infoAction](https://github.com/qbittorrent/qBittorrent/blob/3c4409d1a204a8b7d878727a538a8f4c5ae292f1/src/webui/api/torrentscontroller.cpp#L555-L575).
- Independent reproduction: pass the same 40-character hexadecimal identity in
  lowercase and uppercase as two `TorrentListOptions.Hashes` entries.
- Observed result: `listQuery` returned nil error and emitted both entries.
- Failure mode: the handwritten API and README promise duplicate rejection, but
  one logical identity can consume the request bound twice and reach upstream as
  duplicate scope. This does not widen the selected torrent set, so severity is
  P2.
- Contract: the corrected OpenAPI/README say each logical hash occurs at most
  once; A-04 treats hashes as identities rather than presentation strings.
- Required change: define canonical identity comparison for supported hash forms
  and reject case-equivalent duplicates while retaining the original accepted
  spelling, or require/document one canonical spelling consistently.
- Required proof: exact and mixed-case duplicates for 40- and 64-character
  identities, distinct identities, and aggregate accounting after
  canonicalization.
- Disposition: `current_blocker`.

## Round-one finding verification

| Round-one finding | Independent result |
| --- | --- |
| Login requires a usable SID | Closed. Missing, empty, wrong-name, wrong-path and Secure-over-HTTP cookies fail; usable HTTP and Secure-over-HTTPS cookies pass. |
| Hash delimiter and aggregate scope | Closed for documented cases. Pipe, Unicode whitespace, controls, invalid UTF-8, empty values and exact duplicates fail; 40/64 and exact 4096 pass; 4097 fails. Case-equivalent duplication remains the new P2 above. |
| Oversized errors lose status/reauthentication | Closed. Oversized 401/403 reauthenticate once; repeated unauthorized stops after one retry; 429/5xx retain code and retryability without body text. |
| Arbitrary text accepted as version | Closed for syntax. Application/WebAPI valid forms pass; whitespace, multiline, controls, HTML, trailing tokens and over-bound values fail. Runtime supported-version capability remains a later adapter/compatibility gate. |
| Piece-range semantics | Closed. Nonnegative ordered pairs pass; negative, reversed and wrong-length values fail. |
| Unsupported outcome unreachable | Closed. Version 404/405/501 are unsupported; resource 404 stays unavailable and 5xx stays retryable unavailable. |
| Handwritten/OpenAPI input bounds drift | Closed. Query fields and credentials accept exact character maxima and reject one-over; empty credentials fail. |
| Ambiguous endpoint paths | Closed. Clean root/prefix/trailing slash behavior is exact; dot, encoded, repeated-separator and backslash forms fail. |

## Other safeguards that passed

- Module tests, race tests, vet, module verification and tidy-diff pass with
  `GOWORK=off` and `-mod=readonly` where applicable.
- oapi-codegen v2.8.0 output reproduces byte-for-byte from the module contract.
- The public handwritten API exposes no generated DTO type.
- The OpenAPI surface contains seven GET reads plus the sole login POST. No
  mutation route or security scheme is present.
- No Mastarr root/domain/port/storage/workflow/adapter import or local replace
  directive exists.
- Redirect refusal, Origin/Referer, isolated cookie jar, cancellation, positive
  transport timeout, response byte/item bounds, unknown fields and trailing JSON
  values pass existing and independent checks.
- Returned `UpstreamError` messages omit upstream body text, URL and credentials.
- Fixtures and examples are synthetic. Matches from the public safety scan are
  `.invalid` URLs, synthetic credentials, API field names and negative tests.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/parent/tree/handoff/state and five-file scoped correction | Inspected; identities above are exact. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` in `clients/qbittorrent` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `GOWORK=off go mod verify` | Passed. |
| `GOWORK=off go mod tidy -diff` | Passed with no diff. |
| `./check-generation.sh` | Passed; generated v2.8.0 output reproduced. |
| Module-local Vacuum OpenAPI lint | Passed, quality 100/100. |
| `python3 scripts/check_planning.py` | Passed: 43 tasks, 60 acceptance cases and local links. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed full root/UI/tools generation, API, lint, architecture, tests, vet and module verification. |
| Linux amd64/arm64 CGO-free module compile | Passed; both outputs are statically linked Linux ELF executables. |
| OpenAPI/fixture structural parser | Passed: eight paths, seven GETs plus login POST, closed fixed objects, no security scheme, five fixture documents. |
| Root-import, generated-type, local-replace and mutation-route scans | Passed. |
| Public credential/private-coordinate scan | Passed; only synthetic/test/documentation matches. |
| Scoped diff/archive hashes, `git diff --check` and clean reviewer status | Passed before receipt creation. |
| Independent round-one closure probes, including race run | Passed as recorded above. |
| HTTP 201 login and 206 valid empty-inventory probes | Failed contract as finding one. |
| Duplicate/case-folded/invalid-UTF-8 JSON identity probes | Failed contract as finding two. |
| Empty inventory hash probe | Failed contract as finding three. |
| Negative effective HTTP timeout probe | Failed contract as finding four. |
| Case-equivalent request hash duplicate probe | Failed contract as finding five. |

## Acceptance contribution

- A-04: not accepted for X-10. Request hash delimiter/aggregate controls pass,
  but response identity can be empty, decoder-selected or UTF-8-replaced, and a
  case-equivalent duplicate bypasses the advertised request rule.
- A-09: the standalone session boundary passes usable-cookie, isolation and
  reauthentication tests. Connection-scoped persistence identity remains X-12.
- A-28: normalized progress/state/seeding evidence remains read-only and intact
  for canonical responses, but a partial 206 inventory is currently reported as
  complete evidence.
- A-45: generated and module/repository gates pass. The adversarial failures
  above keep the client candidate from independent approval.

## Reviewer decision

`changes_requested`. Correct the five findings and add the required regression
proof before another independent review. The round-one correction is materially
better and all eight original defects are closed; green generation and module
checks do not establish exact response status, identity decoding or deadline
semantics.
