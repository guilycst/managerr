# X-11 independent review, round two

## Decision

`changes_requested`.

The correction closes the raw transport-error leak, response-body outcome
collapse, ambiguous JSON-RPC envelope, required-parameter/nullability dialect,
and local unsupported-method findings. The exact product remains blocked by
three P2 contract defects: HTTP 408 is normalized as rate limiting rather than
timeout, the compatibility document fails the official OpenRPC meta-schema's
URI check, and endpoint validation fails open after eight decoding passes while
also allowing encoded control or invalid UTF-8 bytes.

## Review identity and boundary

- Reviewer: `/root/f01_reviewer`, independent of product author
  `/root/d01_implementer`.
- Reviewed product commit:
  `889c3fdeb6558096e0480e1c1597a8c1aa03c152`; direct parent
  `3627fd10b84204fc8815ef0b363d422251cb58b1`; product tree
  `8da8ca385f4776e9e4b7e72bdd6a9198a8b38309`.
- Reviewed handoff commit:
  `ed93915dfcf5858f84ad8c505331da51909e7a33`; it is a direct child of the
  product commit.
- Coordinator state commit inspected:
  `ceda3061d490ce8c29c445789520fe7ad8677b23`; it is a direct child of the
  handoff commit.
- Prior review receipt: `0cfcaa1fe437b699595654ba750195c739dff668`.
- Review scope: `clients/nzbget/`, `tools/internal/nzbgetgen/`, X-11, its
  A-05/A-06/A-09/A-45 contribution, and the six round-one findings.
- The product commit changes exactly six assigned files: README, handwritten
  client, client tests, OpenRPC document, generator, and generator tests.
  Scoped product diff SHA-256:
  `05df601e0911af946cbc53f78d7b738ef23667a616965f2f9316e5a2af1b4822`.
- Checks ran from clean detached reviewer worktree
  `/Users/guilhermecastro/.codex/worktrees/managerr-f01-review-r1` at the exact
  product commit. Disposable external Go harnesses imported the standalone
  client through a local replacement without changing product bytes.
- No live NZBGet instance, credential, private hostname, retained NZB, media,
  upstream mutation, release, or deployment was used.

## Findings

### P2: HTTP 408 is still reported as rate limiting

- Location: `clients/nzbget/client.go:917-932` and regression expectation at
  `clients/nzbget/client_test.go:220-250`.
- Evidence: `normalizeHTTPError` groups `http.StatusRequestTimeout` (408) with
  `http.StatusTooManyRequests` (429), assigning `ErrorRateLimited` to both.
  The committed test explicitly expects this incorrect pairing.
- Independent reproduction: return an oversized untrusted body with each of
  401, 403, 408, 429, 500, and 502 while configuring an eight-byte successful
  response bound. Status-first handling works, but 408 produces:

  ```text
  STATUS 408 kind=rate_limited retryable=true too_large=false timeout_is=false
  ```

  The 429 response correctly produces `rate_limited`; 500/502 correctly produce
  retryable `unavailable`.
- Failure mode: a server or reverse proxy request timeout is presented as quota
  pressure. Consumers and operators cannot distinguish waiting for rate-limit
  recovery from a timed-out request, and `errors.Is(err,
  context.DeadlineExceeded)` is false despite the public safe timeout mechanism.
- Contract: X-11 owns typed timeout and rate-limit outcomes; connector contract
  `connectors.md:23-29` requires distinct normalized outcomes. Round-one
  finding two required exact 408/429 kind, status, and retryability proof.
- Required change: map HTTP 408 to `ErrorTimeout`, retain status 408 and
  retryability, and keep 429 as `ErrorRateLimited`. Add exact assertions for
  both small and oversized bodies and the safe timeout sentinel behavior.
- Disposition: `current_blocker`.

### P2: the OpenRPC document fails official meta-schema validation

- Location: `clients/nzbget/openrpc.json:8-18` and generator validation at
  `tools/internal/nzbgetgen/main.go:17-23,133-176`.
- Evidence: the document declares `servers[0].url` as
  `{endpoint}/jsonrpc`. The official OpenRPC meta-schema release 1.11.1, whose
  version enum begins with `1.2.6`, defines a server URL as a Draft 7 string
  with `format: uri`. Format-aware validation rejects the template. The custom
  generator omits the `servers` object from its parsed document and accepts
  even a replacement URL of `not a uri`.
- Independent reproduction: download the official 1.11.1
  `open-rpc-meta-schema.json` release asset, register its HTTPS Draft 7
  meta-schema, and validate with Ajv 8 plus `ajv-formats`:

  ```text
  clients/nzbget/openrpc.json invalid
  instancePath: /servers/0/url
  keyword: format
  message: must match format "uri"
  invalid_server_uri_generator_exit 0
  ```

- Failure mode: the file is described as an OpenRPC 1.2.6 compatibility
  document but does not pass the official machine contract requested by the
  prior receipt. Deterministic generation stays green because the generator
  ignores the invalid section.
- Contract: X-11 requires a Mastarr-owned OpenRPC compatibility document; A-45
  and round-one finding four require strict, reproducible contract semantics
  and validation against the pinned OpenRPC 1.2.6 meta-schema.
- Required change: make the document pass a format-aware official meta-schema
  validation, for example by omitting the optional server list or using a valid
  synthetic absolute URI. Add a deterministic validation check so an invalid
  server URI cannot remain invisible to generation/check gates.
- Required proof: official meta-schema validation with URI formats enabled,
  rejection of an invalid server URI, exact 1.2.6 rejection tests, and clean
  deterministic regeneration.
- Disposition: `current_blocker`.

### P2: endpoint validation fails open at its decoding limit and accepts unsafe decoded bytes

- Location: `clients/nzbget/client.go:815-876`; documented guarantee at
  `clients/nzbget/README.md:42-46`.
- Evidence: `unsafeEndpointSegment` unescapes at most eight times and returns
  safe after the loop even if decoding has not reached a fixed point. A
  nine-layer encoding of `..` is therefore accepted. The same validator accepts
  encoded NUL, LF, DEL, and invalid UTF-8 and sends them unchanged in the
  credential-bearing request target.
- Independent reproduction:

  ```text
  /%25252525252525252e%25252525252525252e/jsonrpc -> accepted
  decode pass 8 -> /%2e%2e/jsonrpc
  decode pass 9 -> /../jsonrpc
  /%00/jsonrpc -> accepted, RequestURI "/%00/jsonrpc"
  /%0A/jsonrpc -> accepted, RequestURI "/%0A/jsonrpc"
  /%7F/jsonrpc -> accepted, RequestURI "/%7F/jsonrpc"
  /%FF/jsonrpc -> accepted, RequestURI "/%FF/jsonrpc"
  ```

- Failure mode: the documented claim that repeatedly encoded dot segments are
  rejected is depth-dependent. Different proxy decoding or routing behavior can
  interpret the Basic-auth request target differently from the reviewed
  endpoint. Encoded controls and invalid UTF-8 create the same parser-boundary
  ambiguity while clean `%20` prefixes remain a supported use case.
- Contract: X-11 owns endpoint/path security. Round-one finding five requires
  rejection or explicit normalization of encoded dot/separator forms and exact
  clean `RequestURI`; the correction handoff explicitly claims repeatedly
  encoded path forms are rejected.
- Required change: decode until a fixed point under a strict input/iteration
  bound and fail closed when that bound is reached; reject decoded controls,
  NUL, invalid UTF-8, separators, backslashes, and dot segments while preserving
  the documented clean encoded-space prefix.
- Required proof: one through more-than-eight encoding layers for dot and
  separators, encoded control/NUL/invalid UTF-8 cases, clean `%20`, and exact
  emitted `RequestURI`.
- Disposition: `current_blocker`.

## Round-one finding closure

- Raw transport details are no longer retained or reachable through
  `errors.Unwrap`. External probes found zero wrapped errors and no transport
  sentinel or private host. `UpstreamError.Is` safely preserves caller
  cancellation and deadline discovery.
- Non-success status is classified before any response body is retained. A
  successful body I/O failure becomes retryable `unavailable`; an actual
  overflow becomes non-retryable `response_too_large`; body cancellation and
  deadline paths retain their context kinds. The remaining 408 taxonomy defect
  is finding one above.
- Duplicate-aware recursive JSON scanning rejects duplicate envelope and nested
  error members, trailing values, absent/null/wrong version or ID, incoherent
  result/error branches, null result, and missing/null/wrong-typed error fields.
  Valid success and fault branches remain accepted.
- Pinned NZBGet source commit
  `b609226e18da11955ce8dda2c7df959258655579`,
  `daemon/remote/XmlRpc.cpp:690-728`, independently confirms JSON-RPC 1.1
  responses emit the request ID and exactly one of `result` or `error`.
- Every positional argument is now `required: true`, nullable fields use valid
  Draft 7 two-type unions, no `nullable` keyword remains, and the generator
  rejects missing/false required parameters, bad unions, and OpenRPC versions
  other than exactly 1.2.6. Official meta-schema validity remains finding two.
- Ordinary dot, encoded-dot, repeated separator, slash/backslash, and
  double-encoded cases are rejected, and a clean `%20` custom path is sent
  exactly. Deeper encoding and decoded-byte validation remain finding three.
- Safe unknown reads, `rpc.discover`, and mutation names return
  `unsupported` before transport dispatch; malformed names and bad supported
  arguments remain `invalid_input`.

## Preserved safeguards

- `clients/nzbget` is an independent standard-library-only module with no root,
  internal, adapter, storage, workflow, UI, tools, local replacement, or
  `go.work` dependency.
- The public generated surface contains exactly `version`, `listgroups`,
  `listfiles`, and `history`, all by-position. No `rpc.discover`, arbitrary
  request, queue-control, or mutation wrapper exists.
- Positional wrappers emit `[]`, `[0]`, `[0,0,NZBID]`, and `[Hidden]` in exact
  order. Hi/Lo halves remain unsigned and combine without narrowing;
  nullable/version-specific fields remain pointers.
- History retains `ID` as a fallback only when `NZBID` is absent and rejects
  negative or conflicting positive aliases. File IDs remain distinct from
  group NZBIDs.
- Request IDs remain monotonic, nonzero, process-local, and concurrency-safe.
  Requests retain Basic authentication, bounded/default deadlines, sanitized
  typed errors, same-origin redirect policy, and bounded success bodies.
- Synthetic fixtures contain only public fake records. Public scans found only
  explicit test credentials such as `reader`/`secret`; no real credential,
  cookie, tracker URL, private hostname, inventory, or user runtime path exists.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/tree/handoff/state identities, ancestry, six-file owned diff, and SHA-256 | Passed; identities and digest are recorded above. |
| `GOWORK=off go test -mod=readonly -count=1 -timeout=90s ./...` in `clients/nzbget` | Passed. |
| Focused client suite at `-count=25` and race suite at `-count=10` | Passed. |
| `GOWORK=off go vet -mod=readonly ./...`, `go mod verify`, and `go mod tidy -diff` in `clients/nzbget` | Passed with no diff. |
| Tools generator focused tests, race, vet, full tools tests, module verification, and tidy diff | Passed. |
| `./clients/nzbget/check-generation.sh`, `go generate`, direct generator check, `gofmt -d`, and `git diff --check` | Passed; generated output reproduced and detached checkout remained clean. |
| Root `GOWORK=off go test -mod=readonly -count=1 -timeout=300s ./...` | Passed all packages; storage completed in 12.446 seconds. |
| Root `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed all packages; storage completed in 213.112 seconds. |
| Root vet, module verify, and tidy diff | Passed. |
| `./scripts/generate.sh --check`, `check-api.sh`, `check-lint.sh` | Passed; API score 100 and lint reported zero issues. |
| Architecture, planning self-test/normal check, and fast guardrails | Passed; 43 tasks and 60 acceptance cases resolve. |
| Root, UI, tools, qBittorrent client, and NZBGet client module verification | Passed. UI and qBittorrent module tests/vet also passed. |
| Extra `go mod tidy -diff` across every module | Root, tools, and both client modules passed. UI proposed removing currently unused UI dependencies; X-11 changes no UI or module file, so this is an unrelated existing module-maintenance result. |
| Client and generator test/build cross-compiles | Passed for Linux amd64/arm64, Windows amd64, Darwin arm64, and AIX ppc64 with CGO disabled. |
| Official OpenRPC 1.11.1 meta-schema asset plus Draft 7/Ajv formats | Failed only at `servers[0].url`, as finding two records. Structural validation without URI formats otherwise passed. |
| Generator mutations: wrong version, missing/false required, `nullable`, bad union | All rejected. Invalid server URI was accepted, confirming finding two. |
| External status/body/transport/context/method/path/envelope probes | Transport redaction, body distinction, cancellation/deadline, unsupported zero-dispatch, and envelope cases passed; 408 and endpoint depth/byte cases failed as findings one and three. |
| Pinned NZBGet server-source envelope inspection | Passed; exactly one result/error member matches the new decoder. |
| Clean detached status and public secret/path/mutation scan | Passed before receipt creation. |

## Acceptance contribution

- A-05: queue/file/history wrappers, positional parameters, identity aliases,
  64-bit values, and exact envelope binding now pass. The X-11 contribution
  remains conditional on correcting the typed 408 and endpoint findings.
- A-06: sanitized error-chain behavior is accepted for X-11. Descriptor
  retention and root-port translation remain X-13 scope.
- A-09: client-local endpoint, credentials, request IDs, and ordinary request
  binding are preserved. The endpoint ambiguity in finding three keeps the
  complete X-11 contribution open.
- A-45: deterministic generation and module isolation pass, but the declared
  OpenRPC source fails official format-aware meta-schema validation.

## Reviewer decision

`changes_requested`. Correct all three findings and add their exact regressions
before another independent review. The green suites establish the ordinary
read client and close most round-one defects; they do not establish correct 408
semantics, official OpenRPC validity, or fail-closed endpoint decoding.
