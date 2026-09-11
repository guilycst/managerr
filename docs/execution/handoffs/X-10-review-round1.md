# X-10 independent review, round one

## Decision

`changes_requested`.

The standalone module boundary, generated output, ordinary read surface,
session isolation, redirect refusal, context handling, sanitized errors and
synthetic fixtures are sound. Four P1 and four P2 findings remain. A login can
report success without a SID, one hash value can widen a pipe-delimited query,
oversized error bodies erase the HTTP classification, malformed plain-text
version bodies become version evidence, semantic piece ranges are not
validated, the documented unsupported outcome is unreachable, handwritten
limits disagree with the OpenAPI contract, and unsafe endpoint path forms are
accepted.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Reviewed product commit:
  `915eaab2568394ae4afb2ab0e580e8db9936fec8`.
- Product parent:
  `05b2d6e6a2f99252fdcf71b13af5a49acdb0b69f`.
- Product tree:
  `b4dae17f9d619b9387b264d59fb87982f552fddb`.
- Reviewed handoff commit:
  `54584d510466281d0bc5a39a7ec161894596e934`.
- Coordinator state commit inspected:
  `b855f6494be5d2c9abf586ce0d67642ef7a73895`.
- Review scope: `clients/qbittorrent/`, task X-10, qBittorrent connector
  requirements and A-04/A-09/A-28/A-45.
- Product commit adds exactly 15 assigned module files and changes no root
  adapter, shared contract, execution state, task definition or script.
- Scoped product diff SHA-256:
  `95c5fc0f4e3d624d67b384a55e4bc832bd42795c1dce1444ea9a1697e26a8ca9`.
- Scoped product archive SHA-256:
  `5d607940b2c8a35eb74fef83b0279079853d271c100d5e9e917d377acda7d3d2`.
- Product checks ran in a clean detached reviewer-owned worktree at the exact
  product commit. Independent adversarial tests ran in disposable Git archive
  `/tmp/managerr-x10-r1-review.Zs99sk` and were not added to the product.
- No live qBittorrent instance, credential, tracker coordinate, media payload,
  torrent mutation, filesystem action, release or deployment was used.

## Findings

### P1: login reports success without establishing a SID session

- Location: `clients/qbittorrent/client.go:493-516`.
- Evidence: `ensureSession` checks the successful `Ok.` body, then sets
  `authenticated = true` without verifying that the isolated cookie jar
  contains a usable `SID` cookie for the configured endpoint. This contradicts
  the OpenAPI login contract and README statement that successful login stores
  the returned SID.
- Independent reproduction: return HTTP 200 and body `Ok.` from
  `/api/v2/auth/login` without `Set-Cookie`, then call `Login`.
- Observed result:

  ```text
  Login error: <nil>
  authenticated: true
  SID cookie: absent
  ```

- Failure mode: startup or a connection check can claim authentication while
  no authenticated session exists. The first protected read then fails or
  causes a second login. A `Secure` cookie returned over HTTP or a cookie with
  an unusable path has the same false-success risk.
- Contract: `openapi.yaml:21-44` says success sets a SID cookie;
  `README.md:26-33` says the client stores it. The pinned official qBittorrent
  WebUI documentation says the success response contains a SID cookie that
  must accompany authenticated operations.
- Required change: after the `Ok.` body, require a nonempty usable `SID` in the
  isolated jar before setting authenticated. If cookie-less authentication
  bypass is intentionally supported, model and prove it as a distinct mode
  with a protected read rather than silently treating no cookie as a session.
- Required proof: valid SID, missing SID, empty SID, wrong cookie name, unusable
  cookie path/domain/Secure combination, concurrent idempotent login and
  expired-session reauthentication.
- Disposition: `current_blocker`.

### P1: hash-list elements can expand scope and exceed the frozen request bound

- Location: `clients/qbittorrent/client.go:614-658` and
  `clients/qbittorrent/openapi.yaml:159-165`.
- Evidence: `TorrentListOptions.Hashes` presents each slice member as one hash,
  but `validateHash` accepts qBittorrent's `|` list delimiter. `listQuery` joins
  members with the same delimiter without validating the final length. The
  OpenAPI contract caps the decoded `hashes` parameter at 4096 bytes.
- Independent reproduction:
  1. Call `listQuery` with one member `hash-one|hash-two`.
  2. Call it with 33 members of 128 bytes each.
- Observed result:

  ```text
  one logical member encoded as hashes=hash-one|hash-two
  33-member decoded hashes value accepted at 4256 bytes
  ```

- Failure mode: a value presented as one selected hash widens the upstream
  read scope. A large slice bypasses the documented query bound and can create
  an oversized request despite every member passing validation.
- Contract: `connectors.md:34-37` requires scoped v1/v2 identities. The OpenAPI
  contract and pinned qBittorrent documentation define `|` as the multiple-hash
  separator. A-04 requires exact hash evidence.
- Required change: reject the delimiter and unsafe whitespace in each logical
  hash, retain control/UTF-8/length checks, and validate the final joined value
  against the 4096-byte contract before allocating or sending the request.
  Preserve supported 40- and 64-character identities without assuming every
  ID is 40 characters.
- Required proof: 40/64-character hashes, delimiter in any position, control
  bytes, invalid UTF-8, empty member, duplicate member, maximum exact aggregate
  size and one-byte-over aggregate size.
- Disposition: `current_blocker`.

### P1: oversized error bodies erase typed HTTP outcomes and auth recovery

- Location: `clients/qbittorrent/client.go:535-590`.
- Evidence: `requestOnce` reads and bounds the body before `getAfterSession`
  classifies the status. When an error response exceeds `MaxResponseBytes`, it
  returns `ErrorUnknown` immediately. The 401/403 branch is never reached, so
  the single reauthentication path is also skipped.
- Independent reproduction: configure `MaxResponseBytes: 64`, establish a SID,
  then return a 65-byte body with status 401, 403, 429 or 500 from a read route.
- Observed result:

  ```text
  401 -> unknown, no reauthentication
  403 -> unknown, no reauthentication
  429 -> unknown, Retryable:false
  500 -> unknown, Retryable:false
  ```

- Failure mode: response-size defense changes the outcome semantics needed by
  credential repair and retry policy. Rate limits and outages become terminal
  unknown failures, while expired sessions never receive the bounded login
  retry.
- Contract: `connectors.md:24-30` requires sanitized typed outcomes, deadlines
  and bounded clients. The X-10 deliverable explicitly owns authentication and
  typed upstream errors.
- Required change: preserve/classify the HTTP status independently of body
  retention. Discard over-limit error text without losing unauthorized,
  rate-limited, unavailable, retryable or reauthentication behavior.
- Required proof: small and oversized bodies for 401, 403, 429 and 5xx,
  including one and only one reauthentication attempt for 401/403 and no body
  text in returned errors.
- Disposition: `current_blocker`.

### P1: arbitrary successful text becomes application/WebAPI version evidence

- Location: `clients/qbittorrent/client.go:481-490`.
- Evidence: `readVersion` trims leading/trailing whitespace and checks only
  nonempty length and UTF-8. It accepts embedded newlines, NUL controls, HTML
  and multiple logical values as a version.
- Independent reproduction: return HTTP 200 with each body below, then call
  `ApplicationVersion`:

  ```text
  v5.0.0\nunexpected
  v5.0.0\x00unexpected
  <html>upstream error</html>
  ```

- Observed result: every body was returned as a successful version string.
- Failure mode: a reverse-proxy or malformed upstream success body can become
  compatibility evidence and later drive an incorrect route/capability gate.
- Contract: `connectors.md:158-164` requires supported-version detection and
  explicit unsupported reasons. The module's OpenAPI describes a bounded
  plain-text version value, not arbitrary text.
- Required change: define and enforce the supported single-value version shape
  for both endpoints. Reject controls, multiple lines and non-version bodies
  with a sanitized typed malformed outcome while retaining documented suffixes
  needed by qBittorrent builds.
- Required proof: application and WebAPI examples, allowed suffixes, surrounding
  whitespace policy, empty, oversized, multiline, control, HTML and trailing
  token bodies.
- Disposition: `current_blocker`.

### P2: file piece ranges are length-checked but not semantically validated

- Location: `clients/qbittorrent/client.go:400-421`.
- Evidence: `GetTorrentFiles` accepts every two-element `piece_range`. It does
  not verify nonnegative indices or that the inclusive start is at most the
  end, despite the handoff's claim that file piece ranges are validated.
- Independent reproduction: replace the first fixture range with `[-1, 5]` or
  `[5, 4]`, then call `GetTorrentFiles`.
- Observed result: both values were returned without error.
- Failure mode: malformed upstream file evidence reaches callers as a valid
  normalized range and can undermine file/piece reasoning.
- Contract: the pinned WebUI API defines the two values as inclusive starting
  and ending piece indices; strict decoding must reject impossible evidence.
- Required change: enforce the documented order and domain, or explicitly
  document and test any real upstream sentinel exception.
- Required proof: valid single-piece/multi-piece ranges, negative start/end,
  reversed range, wrong length and any documented zero-size-file sentinel.
- Disposition: `current_blocker`.

### P2: the public unsupported error code is never produced

- Location: `clients/qbittorrent/client.go:61` and `:708-727`.
- Evidence: the public error taxonomy declares `ErrorUnsupported`, and the
  README lists it as a typed outcome, but `statusError` never assigns that
  code. Method/version incompatibility responses therefore collapse into a
  different class.
- Independent reproduction: return HTTP 405 and HTTP 501 from
  `/api/v2/app/version`, then call `ApplicationVersion`.
- Observed result:

  ```text
  405 -> unknown, Retryable:false
  501 -> unavailable, Retryable:true
  ```

- Failure mode: a caller cannot distinguish an unsupported WebAPI route or
  method from an unclassified response or a transient outage, despite the
  compatibility contract advertising that distinction.
- Contract: `connectors.md:158-164` requires supported-version detection and an
  explicit unsupported reason. `README.md:31-33` exposes `unsupported` in the
  handwritten error taxonomy.
- Required change: define operation-aware unsupported status handling. Preserve
  real not-found semantics for scoped resource reads while classifying version
  or route/method incompatibility as unsupported where the WebUI contract makes
  that conclusion safe.
- Required proof: version-route 404, 405 and 501; resource-route 404; transient
  5xx; and a supported response, with exact code and retryability assertions.
- Disposition: `current_blocker`.

### P2: handwritten input validation disagrees with the OpenAPI contract

- Location: `clients/qbittorrent/client.go:256-278` and `:614-665`, versus
  `clients/qbittorrent/openapi.yaml:113-165` and `:433-446`.
- Evidence: the OpenAPI caps filter/sort at 64, category/tag at 512, username at
  256 and password at 1024, with nonempty credentials. Handwritten validation
  uses one 4096-byte query limit and one 4096-byte credential limit and accepts
  empty credentials.
- Independent reproduction: pass 65-byte filter/sort, 513-byte category/tag,
  257-byte username, 1025-byte password or both credentials empty.
- Observed result: every value was accepted by the handwritten public API.
- Failure mode: generated compatibility documentation and the actual supported
  client surface describe different request contracts. Consumers cannot rely
  on the frozen schema for validation or generation parity.
- Contract: X-10 requires one narrow versioned compatibility document and a
  handwritten API that does not leak generated types. A-45 requires generated
  artifact and module correctness, including drift-free ownership.
- Required change: share or mirror the exact contract bounds in handwritten
  validation, or revise the OpenAPI limits with documented upstream evidence
  and regenerate. Define any credential-less bypass mode explicitly.
- Required proof: exact maximum and one-over values for every bounded field,
  empty credential policy and regeneration check.
- Disposition: `current_blocker`.

### P2: endpoint validation accepts ambiguous path traversal forms

- Location: `clients/qbittorrent/client.go:556-611`.
- Evidence: `parseEndpoint` validates scheme/host/query/fragment but accepts
  dot segments, encoded dot segments and repeated separators. `requestOnce`
  clears `RawPath` and concatenates the uncanonicalized base path with an API
  path. Different proxies or servers can normalize that request differently.
- Independent reproduction: construct clients with:

  ```text
  https://fixture.invalid/base/../admin
  https://fixture.invalid/%2e%2e/admin
  https://fixture.invalid/base//admin
  ```

- Observed result: all three endpoints were accepted.
- Failure mode: a malformed reverse-proxy base can send the login form and SID
  request to a different same-origin path than the operator reviewed. Redirect
  refusal does not resolve same-origin path ambiguity.
- Contract: HTTP boundaries must protect credentials and make the configured
  endpoint exact. X-10 owns endpoint validation, Origin/Referer and session
  safety.
- Required change: allow a clean reverse-proxy prefix if needed, but reject or
  canonicalize dot segments, encoded separators/dot segments and repeated
  separators before storing the endpoint. Build request paths with a method
  whose normalization semantics are explicit and tested.
- Required proof: root endpoint, clean prefix, trailing slash, dot/encoded-dot,
  encoded slash/backslash, repeated slash, userinfo/query/fragment and
  cross-origin redirect cases; assert exact request URI and Origin/Referer.
- Disposition: `current_blocker`.

## Safeguards that passed

- The module imports only its generated subpackage, oapi-codegen runtime and Go
  standard library. No Mastarr root/domain/port/storage/workflow/adapter import
  or local replace directive exists.
- Generated code declares oapi-codegen v2.8.0 and reproduces byte-for-byte from
  the module OpenAPI document. The public handwritten method signatures expose
  only handwritten DTOs.
- The OpenAPI surface contains seven GET reads and the sole session-establishing
  login POST. No stop, resume, relocation, rename, category/tag mutation,
  deletion, add-torrent or arbitrary request method is exposed.
- A normal login sets an isolated SID jar, sends exact Origin/Referer, refuses
  redirects and is idempotent across repeated calls. Small-body
  401/403 responses trigger one bounded reauthentication attempt.
- Context cancellation prevents dispatch. Context deadlines and transport
  timeouts return the expected cancellation or sanitized unavailable outcome.
- JSON decoding rejects unknown fields, empty/null/malformed documents and
  trailing JSON values. Response bytes and array counts are bounded without
  truncating evidence.
- Application/WebAPI examples, torrent inventory/properties/files,
  categories/tags, timestamp/counter sentinels and copy isolation pass ordinary
  fixture tests. Forty- and sixty-four-character values in the documented
  `hash` field are retained exactly.
- Hash controls, invalid UTF-8, empty values and per-member overlength are
  rejected. The remaining hash finding is delimiter and aggregate validation.
- Fixtures are valid synthetic JSON using `.invalid` hosts and synthetic
  credentials/paths. No private hostname, real tracker, passkey, cookie or
  runtime inventory was found.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/parent/tree/handoff/state and 15-file owned diff | Inspected; identities above are exact. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` in `clients/qbittorrent` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `GOWORK=off go mod verify` | Passed. |
| `GOWORK=off go mod tidy -diff` | Passed with no diff. |
| `./check-generation.sh` | Passed; generated v2.8.0 output reproduced. |
| Module-local Vacuum OpenAPI lint | Passed with quality score 100/100. |
| `python3 scripts/check_planning.py` | Passed: 43 tasks, 60 acceptance cases; local links resolve. |
| `./scripts/check-guardrails.sh --fast` | Passed. Current guardrails do not yet include standalone clients; C-05 owns that integration. |
| Linux amd64/arm64 CGO-free module compile | Passed. |
| OpenAPI YAML and five fixture JSON documents | Parsed successfully. |
| OpenAPI method/security/object-closure scan | Seven GETs plus login POST; no security scheme; fixed objects closed. |
| Root/Mastarr import and generated-type signature scan | Passed. |
| Scoped diff/archive hashes, `git diff --check` and clean status | Passed before receipt creation. |
| Public credential/private-path scan | Passed; matches were synthetic fixtures, API field names and `.invalid` URLs. |
| 40/64-character hash and control/length probes | Passed. |
| Unknown, oversized success, malformed and trailing JSON probes | Passed. |
| Context cancel/deadline, transport timeout, redirect, normal Origin/Referer and bounded small-body 401/403 probes | Passed. |
| SID-less successful login probe | Failed contract as finding one. |
| Hash delimiter/aggregate probes | Failed contract as finding two. |
| Oversized HTTP error-body matrix | Failed contract as finding three. |
| Multiline/control/HTML version-body probes | Failed contract as finding four. |
| Negative/reversed piece-range probes | Failed contract as finding five. |
| HTTP 405/501 unsupported-outcome probes | Failed contract as finding six. |
| OpenAPI/handwritten exact-bound probes | Failed contract as finding seven. |
| Dot/encoded-dot/repeated-path endpoint probes | Failed contract as finding eight. |

## Ledger observation

The reviewed state commit records X-10 `baseSha` as
`b808057059f53dbed011a9d3be75e9da28926c77`; the handoff calls
`05b2d6e6a2f99252fdcf71b13af5a49acdb0b69f` the dispatch base, and that SHA is
the actual product parent. This does not change the product findings, but the
coordinator should preserve the original dispatch identity separately from the
later shared-checkout parent when updating execution state.

## Acceptance contribution

- A-04: not accepted for X-10. Documented 40/64-character hash values are
  retained, but one logical hash can widen the upstream filter and aggregate
  request scope is not bounded.
- A-09: partial. Cookie jars are isolated per client instance, but login can
  falsely claim a session without SID evidence. Connection-scoped persistence
  identity remains X-12 responsibility.
- A-28: the ordinary read DTO preserves state, progress, completion and seeding
  evidence without exposing controls, but the module is not accepted while its
  evidence/error boundaries remain open.
- A-45: not accepted. Generation and module gates pass, but handwritten inputs
  drift from the versioned OpenAPI contract and adversarial checks are missing.

## Reviewer decision

`changes_requested`. Correct the eight findings and add the required
adversarial regressions before another independent review. Existing green tests
and generation checks prove reproducibility; they do not prove the missing
session, scope, status, version, semantic-evidence and endpoint boundaries.
