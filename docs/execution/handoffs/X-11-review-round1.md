# X-11 independent review, round one

## Decision

`changes_requested`.

Standalone module ownership, four-method read boundary, generated output,
positional request order, common DTO normalization, Basic authentication,
same-origin redirect refusal, ordinary deadlines and synthetic fixtures are
sound. Three P1 and three P2 findings remain. Transport error chains expose
details promised as sanitized, response-body handling erases HTTP and I/O
outcome semantics, ambiguous JSON objects can defeat response-ID binding, the
OpenRPC document does not machine-describe mandatory and nullable values,
endpoint paths accept ambiguous traversal forms, and a well-formed unsupported
method is classified as invalid input.

## Review identity and boundary

- Reviewer: `/root/f01_reviewer`, independent of product author
  `/root/d01_implementer`.
- Assigned dispatch base:
  `05b2d6e6a2f99252fdcf71b13af5a49acdb0b69f`.
- Initial product commit:
  `ed0d4d274e6fae5b6f59c2d40ab60f81aaadb421`; its direct parent is
  `b855f6494be5d2c9abf586ce0d67642ef7a73895`.
- Final reviewed product head:
  `7b88619ba7371b1379e1d2a50c756c703d7fd02a`; product tree
  `401fecb35e5d85a7d84e70900d9f359cc8d3eeaa`.
- Reviewed handoff commit:
  `70babc71eacc5ed632432d21cd7147b55656373c`.
- Coordinator state commit inspected:
  `eb64a555daad79084c25161248f7568b3692be82`.
- Review scope: `clients/nzbget/`, `tools/internal/nzbgetgen/`, task X-11,
  NZBGet connector requirements and A-05/A-06/A-09/A-45.
- Product scope contains exactly 14 assigned files. It changes no root adapter,
  execution state, task definition, shared script or unrelated lane.
- Scoped product diff SHA-256:
  `67791f767fdae971e60759d33c236b6f77b1032332f3e494dcd12599e8913a3b`.
- Checks ran in clean detached reviewer-owned worktree
  `/Users/guilhermecastro/.codex/worktrees/managerr-f01-review-r1` at exact
  product head. Independent black-box probes used disposable external Go
  harnesses importing that module through a local replacement; product bytes
  remained unchanged.
- No live NZBGet instance, credential, private hostname, retained NZB, media,
  upstream mutation, release or deployment was used.

## Findings

### P1: sanitized upstream errors expose raw transport details through their error chain

- Location: `clients/nzbget/client.go:417-428` and `:515-541`.
- Evidence: transport failures retain the raw `http.Client.Do` error in the
  unexported `cause`, while public `Unwrap` returns that cause. `Do` normally
  wraps transport failures in `*url.Error`, whose text includes the endpoint.
  This contradicts both the type comment and README guarantee that endpoint
  URLs and upstream details are not included in errors.
- Independent reproduction: configure an `http.RoundTripper` that returns
  `SENTINEL_TRANSPORT_SECRET` for endpoint
  `https://example.test/private/jsonrpc`, then call `Version` and inspect the
  error chain.
- Observed result:

  ```text
  transport kind=unavailable public="nzbget unavailable for version"
  unwrap="Post \"https://example.test/private/jsonrpc\": SENTINEL_TRANSPORT_SECRET"
  leak=true
  ```

- Failure mode: ordinary logging, telemetry or error inspection that walks the
  chain can disclose a private endpoint path or transport-provided secret even
  though `Error()` and `Detail` look sanitized. Custom redirect and transport
  failures can carry arbitrary sensitive text through the same path.
- Contract: `clients/nzbget/README.md:50-53`, the `UpstreamError` contract at
  `client.go:515-516`, X-11's sanitized-error requirement, and connector
  specification `connectors.md:23-29`.
- Required change: never expose raw transport or URL errors from the sanitized
  public error. Preserve `errors.Is` for cancellation/deadlines with safe
  sentinels or a narrow `Is` implementation; retain no raw error whose text can
  contain endpoint, response or credential details.
- Required proof: walk the full error chain for synthetic DNS, transport,
  redirect and timeout failures containing opaque sentinels and private paths;
  assert none are reachable while `errors.Is` still detects caller cancellation
  and deadline expiry.
- Disposition: `current_blocker`.

### P1: body handling turns authentication and transport failures into response-size failures

- Location: `clients/nzbget/client.go:430-436` and `:655-666`.
- Evidence: `Invoke` reads and bounds every body before classifying HTTP
  status. It also maps every `readBounded` error to `ErrorResponseTooLarge`,
  although `readBounded` returns the same error channel for I/O failure and
  size overflow.
- Independent reproductions:
  1. Configure `MaxResponseBytes: 32` and return status 401 with a 64-byte body.
  2. Return status 200 with a body whose first read returns
     `SENTINEL_BODY_READ_FAILURE`.
- Observed results:

  ```text
  large-401 kind=response_too_large status=401 retryable=false
  body-read kind=response_too_large retryable=false detail="response exceeds configured bound"
  ```

- Failure mode: an oversized proxy/auth error loses its unauthorized,
  forbidden, rate-limited or unavailable classification. A transient body-read
  failure becomes a false non-retryable size violation. Downstream recovery can
  ask for the wrong operator action or stop retrying a read that should be
  reconciled.
- Contract: X-11 owns bounded responses and typed upstream errors;
  `connectors.md:23-29` requires distinct sanitized outcomes and context
  deadlines.
- Required change: classify a non-2xx status independently of body retention,
  and distinguish actual limit overflow from body I/O/cancellation failure on
  successful responses. Keep every path bounded and sanitized.
- Required proof: small and oversized bodies for 401, 403, 408, 429 and 5xx;
  exact-bound and one-byte-over 2xx bodies; immediate and mid-stream I/O
  failures; cancellation/deadline during body read; exact kind, status and
  retryability assertions.
- Disposition: `current_blocker`.

### P1: ambiguous JSON response objects can bypass exact request-ID binding

- Location: `clients/nzbget/client.go:438-456` and `:481-492`.
- Evidence: `encoding/json.Unmarshal` silently keeps the last duplicate object
  member. A response containing a wrong `id` followed by the expected `id` is
  accepted as success. The same decoder cannot distinguish missing fields from
  zero values: an `error` object with neither required `code` nor `message`
  becomes a normal `remote_error` with fabricated code zero.
- Independent reproductions:

  ```json
  {"version":"1.1","id":999,"id":1,"result":"24.2"}
  {"version":"1.1","id":1,"error":{}}
  ```

- Observed results:

  ```text
  duplicate-envelope-id accepted=true version="24.2" err=<nil>
  empty-error-envelope kind=remote_error rpc=0 detail="JSON-RPC method failed"
  ```

- Failure mode: ambiguous/corrupted response identity is treated as bound to
  the active request, and malformed error evidence is normalized as a real
  upstream outcome. Duplicate `version`, `result`, `error`, `code` or `message`
  members have the same last-value ambiguity.
- Contract: X-11 explicitly owns request IDs and the JSON-RPC envelope. Product
  invariants prohibit invented evidence; connector errors must remain typed and
  honest. Trailing JSON is already rejected, but it does not cover duplicate
  members in one object.
- Required change: decode the envelope with duplicate-member detection and
  explicit member-presence checks. Require one exact version and ID and one
  coherent result-or-error branch. Validate required error members before
  normalization; reject ambiguous duplicates as malformed.
- Required proof: duplicate equal and unequal IDs in both orders; duplicate
  version/result/error and nested error members; missing/null result and error;
  both branches present; error missing code/message; wrong numeric ID; trailing
  document; one valid success and one valid error.
- Disposition: `current_blocker`.

### P2: OpenRPC source does not machine-describe mandatory parameters or nullable values

- Location: `clients/nzbget/openrpc.json:34-107` and nullable schema properties
  beginning at `:133`; generator validation at
  `tools/internal/nzbgetgen/main.go:95-123`.
- Evidence: all five positional parameters omit Content Descriptor
  `required: true`, whose OpenRPC default is false. Pinned NZBGet API evidence
  states that all JSON-RPC parameters are mandatory. The document also uses 86
  OpenAPI-style `nullable: true` keywords inside Schema Objects. OpenRPC 1.2.6
  requires JSON Schema Draft 7 semantics, where `nullable` does not admit null;
  the private generator interprets it anyway. It accepts a document mutated
  from `openrpc: 1.2.6` to `1.999.0`, so its advertised version validation does
  not pin the dialect used by this contract.
- Independent reproduction: inspect parsed parameter descriptors and count
  nullable keywords, then mutate only the `openrpc` value to `1.999.0` and run
  `tools/internal/nzbgetgen`; generation succeeds.
- Observed result:

  ```text
  param-required [('version', []), ('listgroups', [False]),
                  ('listfiles', [False, False, False]), ('history', [False])]
  nullable-keywords 86
  1.999.0 generation exit=0
  ```

- Failure mode: generic OpenRPC tooling sees mandatory positional inputs as
  optional and nullable upstream values as non-null. Generated Go happens to
  send every argument and uses pointers because of generator-private behavior,
  masking drift in the declared compatibility source.
- Contract: X-11 requires a Mastarr-owned OpenRPC 1.2.6 compatibility document,
  mandatory positional quirks and nullable/version-specific DTO fields. A-45
  requires strict, reproducible contract generation. Pinned NZBGet API source
  `docs/api/API.md` at `b609226...` states that all positional parameters are
  mandatory; OpenRPC 1.2.6 defines `required` default false and Schema Objects
  through JSON Schema Draft 7.
- Required change: mark every NZBGet argument required; encode nullability with
  valid Draft 7 schema or a clearly named extension paired with an accurate
  base schema; validate against the pinned OpenRPC 1.2.6 meta-schema and reject
  unsupported document versions before generation.
- Required proof: pinned meta-schema validation, generic-schema checks for a
  nullable fixture, exact required-parameter assertions, deterministic
  regeneration, and rejection of missing/unsupported OpenRPC versions.
- Disposition: `current_blocker`.

### P2: endpoint validation accepts ambiguous traversal and separator forms

- Location: `clients/nzbget/client.go:643-652`.
- Evidence: `parseEndpoint` validates scheme, host, user info, query and
  fragment but accepts dot segments, encoded dot segments, repeated separators
  and encoded slash/backslash. Clearing `RawPath` silently changes some encoded
  input into a different stored path.
- Independent reproduction: construct clients with:

  ```text
  https://fixture.invalid/base/../admin
  https://fixture.invalid/%2e%2e/admin
  https://fixture.invalid/base//admin
  https://fixture.invalid/base/%2fadmin
  https://fixture.invalid/base/%5cadmin
  ```

- Observed result: all five were accepted; encoded dot and slash forms were
  stored as decoded traversal/repeated-separator paths.
- Failure mode: proxy, client and server normalization can disagree about the
  credential-bearing request target. The operator-reviewed endpoint can differ
  from the path that handles Basic authentication.
- Contract: X-11 owns endpoint, redirect and path security. README permits a
  custom path, but it does not authorize ambiguous normalization.
- Required change: define one clean custom-prefix/RPC-path policy and reject or
  canonicalize dot segments, encoded dot/separator/backslash forms and repeated
  separators before storing or sending the endpoint. Preserve exact clean
  reverse-proxy prefixes.
- Required proof: root/default path, clean custom path, trailing slash, dot and
  encoded-dot, encoded slash/backslash, repeated separator, user info, query,
  fragment and exact emitted `RequestURI`; keep cross-origin redirects refused.
- Disposition: `current_blocker`.

### P2: well-formed unsupported methods are reported as invalid input

- Location: `clients/nzbget/client.go:551-578` and
  `clients/nzbget/client_test.go:129-151`.
- Evidence: the final product adds `ErrorUnsupported` and returns it for an
  upstream JSON-RPC `-32601`, but the local allowlist returns
  `ErrorInvalidInput` for every syntactically valid method outside the four
  reads. The committed test pins `rpc.discover` to `invalid_input` even though
  the document and README say NZBGet does not support it.
- Independent reproduction: invoke `rpc.discover` with a valid context, empty
  positional arguments and a nonnil result; no network request occurs and the
  kind is `invalid_input`.
- Failure mode: consumers cannot use one stable classification for permanent
  method/capability absence. A local read-only refusal looks like caller data
  invalidity, while the same upstream condition is `unsupported`.
- Contract: `connectors.md:20-25` distinguishes unsupported capabilities from
  invalid inputs, and X-11 requires typed upstream errors without assuming
  `rpc.discover`.
- Required change: return `unsupported` for a syntactically valid method that
  is outside the module's four-method surface, while retaining `invalid_input`
  for empty, overlong or control-bearing method names and invalid arguments to
  supported methods.
- Required proof: `rpc.discover`, a mutating NZBGet method, an unknown safe
  method, malformed names and bad parameters; assert exact kind and zero
  network dispatch for local refusals, plus `-32601` normalization.
- Disposition: `current_blocker`.

## Safeguards that passed

- `clients/nzbget` is an independent module with no requirements, local
  replacement or Mastarr root/internal import. Generator code stays in the
  tools module and uses only the standard library.
- `openrpc.json` declares version 1.2.6 and contains exactly `version`,
  `listgroups`, `listfiles` and `history`, all `by-position`; no
  `rpc.discover` or mutation method exists.
- Committed generated output reproduces byte-for-byte. Method wrappers emit
  `[]`, `[0]`, `[0,0,NZBID]` and `[Hidden]` in exact order.
- Wire DTOs keep unsigned 32-bit `Hi`/`Lo` halves. Normalized observations
  combine them without narrowing, retain optional halves, preserve native
  paths/status/parameters and copy slice data.
- History keeps deprecated `ID` as evidence, falls back only when `NZBID` is
  absent/zero, and rejects negative or unequal positive aliases. List-file
  `ID` remains distinct from its group `NZBID`, matching pinned API evidence.
- Requests emit NZBGet's JSON-RPC 1.1 `version` member, monotonic process-local
  unsigned IDs and Basic authentication. Concurrent requests produced unique
  IDs 1 through 24 under race detection.
- Caller cancellation and the default/configured request deadline return typed
  errors discoverable through `errors.Is`. Invalid parameters and nil
  context/result are rejected before dispatch.
- Response success bytes are bounded, wrong version/ID and null result are
  rejected, trailing JSON is malformed, and upstream `-32601` becomes
  `unsupported` without exposing its message through `Error()` or `Detail`.
- Cross-origin HTTP redirect was not followed and received zero requests.
  Ordinary endpoint validation rejects non-HTTP schemes, user info, query and
  fragment.
- Fixtures are synthetic JSON and README links only to public pinned evidence.
  Scoped scans found no real credential, cookie, tracker URL, private hostname,
  inventory or user runtime path.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product, tree, handoff, state, ancestry and 14-file owned diff | Inspected; identities above are exact. |
| `GOWORK=off go test -count=1 -timeout=60s ./...` in `clients/nzbget` | Passed. |
| `GOWORK=off go test -race -count=1 -timeout=120s ./...` in `clients/nzbget` | Passed. |
| `GOWORK=off go vet ./...`, `go mod verify`, `go mod tidy -diff` in `clients/nzbget` | Passed; no diff. |
| Tools generator focused test/race/vet and full tools test/vet/module checks | Passed. |
| Generator `-check`, `./clients/nzbget/check-generation.sh`, `gofmt -d`, `git diff --check` | Passed; generated output and checkout stayed clean. |
| Root `GOWORK=off go test -count=1 ./...`, `go vet ./...`, `go mod verify` | Passed. |
| Root `GOWORK=off go test -race -count=1 -timeout=300s ./...` | Passed; storage completed in 219.048s. |
| `./scripts/generate.sh --check`, `./scripts/check-api.sh`, `./scripts/check-lint.sh` | Passed. |
| `python3 scripts/check-architecture.py` | Passed: `architecture import boundaries passed`. |
| `python3 scripts/check_planning.py --self-test` and normal planning check | Passed: 43 tasks, 60 acceptance cases; links resolve. |
| `./scripts/check-guardrails.sh --fast` | Passed. Current shared guardrail does not yet discover standalone clients; C-05 owns that wiring. |
| Root, UI, tools, qBittorrent client and NZBGet client `go mod verify` | Passed independently with `GOWORK=off`. |
| Linux amd64/arm64 CGO-free and Windows amd64 module test cross-compiles | Passed; binaries had expected formats. |
| Unknown method, malformed/trailing/oversized/null/error envelope and ID probes | Baseline cases passed; duplicate/member-presence cases failed as finding three. |
| Cancellation, timeout, concurrent IDs, Basic auth and redirect probes | Passed except raw cause redaction as finding one. |
| Response-bound/status/I/O matrix | Failed as finding two. |
| OpenRPC semantic/version mutation probes | Failed as finding four. |
| Endpoint path-normalization matrix | Failed as finding five. |
| Clean detached worktree status and public secret/path scan | Passed before receipt creation. |

## Acceptance contribution

- A-05: not accepted for X-11. Positional queue/file/history wrappers, Hi/Lo
  values and ordinary history aliases are correct, but ambiguous envelope
  identity can be accepted and the machine contract marks mandatory arguments
  optional.
- A-06: not accepted for X-11. Raw response bodies and RPC messages remain out
  of `Error()` and `Detail`, but transport details remain reachable through the
  public error chain. Descriptor retention itself remains X-13/root scope.
- A-09: partial. Each client has isolated endpoint, credentials and request-ID
  state, and concurrent IDs are unique. Persisted connection-scoped identity
  remains X-13/root scope; X-11 is not accepted while response binding is
  ambiguous.
- A-45: not accepted. Deterministic generation, module isolation and all normal
  checks pass, but the OpenRPC source does not strictly express the runtime
  parameter/nullability contract.

## Reviewer decision

`changes_requested`. Correct all six findings and add the specified adversarial
regressions before another independent review. Existing green checks prove
ordinary behavior and deterministic output; they do not prove sanitization,
status-preserving bounds, unambiguous envelope identity, machine-readable
OpenRPC semantics or exact path/method classification.
