# X-11 independent review, round three

## Decision

`changes_requested`.

HTTP 408 semantics, the committed OpenRPC server URI, and fail-closed endpoint
path decoding are corrected. All earlier client transport, envelope, method,
DTO, and module safeguards remain green. One P2 from round two remains open:
the generator's URI validator accepts multiple strings that the official
OpenRPC URI format rejects, so an invalid future compatibility document can
still pass generation.

## Review identity and boundary

- Reviewer: `/root/f01_reviewer`, independent of product author
  `/root/d01_implementer`.
- Reviewed product commit:
  `fcaed0489bd22692d1a0bae7ea9b8b96ad439453`; direct parent
  `157f89046e4e6d0e5a811192085bcd1ac986454e`; product tree
  `df9327d25580581f93b2adfe714e3d39c522c1b8`.
- Reviewed handoff commit:
  `41f10c9c4386bd3afab13fa93804f2681be26c8a`; direct child of the product.
- Coordinator state commit inspected:
  `bedb48358ab2d7e3dac6fd063ba26e6a56658296`; direct child of the handoff.
- Prior product and review receipt
  `889c3fdeb6558096e0480e1c1597a8c1aa03c152` and
  `147494c3dea44fd7e62f94c41d20ccd512b3eb6f` are ancestors of the reviewed
  product.
- Review scope: `clients/nzbget/`, `tools/internal/nzbgetgen/`, X-11,
  A-05/A-06/A-09/A-45, and every prior X-11 finding.
- The product commit changes exactly six assigned files: README, handwritten
  client, client tests, OpenRPC document, generator, and generator tests.
  Scoped product diff SHA-256:
  `dc58b68267c6802f5f5b3629463a6c348cd35353edc0caca9acb682b5e308ab4`.
- Checks ran in clean detached reviewer worktree
  `/Users/guilhermecastro/.codex/worktrees/managerr-f01-review-r1` at the exact
  product commit. Disposable external programs exercised the public module and
  generator without changing product files.
- No live NZBGet service, credential, private inventory, retained NZB, media,
  upstream mutation, release, or deployment was used.

## Finding

### P2: generator server-URI validation accepts non-URI inputs

- Location: `tools/internal/nzbgetgen/main.go:233-247`; regression matrix at
  `tools/internal/nzbgetgen/main_test.go:151-189`.
- Evidence: `validateServers` uses `url.ParseRequestURI` and then inspects the
  parsed URL. That parser accepts some raw characters by normalizing them in
  `URL.String`, so generation accepts server strings that a format-aware
  official OpenRPC meta-schema rejects. It also treats raw `#` as path text and
  records a trailing `?` through `ForceQuery`, leaving the current `Fragment`
  and `RawQuery` checks ineffective for those delimiters.
- Independent generator mutation results:

  ```text
  not a uri                         rejected
  {endpoint}/jsonrpc                rejected
  relative/path                     rejected
  userinfo                          rejected
  nonempty query                    rejected
  ftp                               rejected
  malformed percent escape          rejected
  https://nzbget.invalid/json rpc   accepted
  https://nzbget.invalid/json\rpc   accepted
  https://nzbget.invalid/json|rpc   accepted
  https://nzbget.invalid/json[rpc   accepted
  https://éxample.invalid/jsonrpc   accepted
  https://nzbget.invalid/jsonrpc#x  accepted
  https://nzbget.invalid/jsonrpc?   accepted
  ```

- Official comparison: Ajv 8 with `ajv-formats`, the official OpenRPC
  meta-schema 1.11.1 release asset, and its HTTPS Draft 7 meta-schema rejects
  the raw-space, backslash, pipe, bracket, and Unicode-host documents at
  `/servers/0/url` with `must match format "uri"`. The committed
  `https://nzbget.invalid/jsonrpc` document itself passes.
- Failure mode: the committed source is valid today, but an invalid future
  server value can pass the repository's deterministic generator check. The
  correction therefore does not provide the validation guard requested by the
  round-two receipt. Raw fragment and empty-query delimiters also contradict
  the correction handoff's explicit claim that fragments and queries are
  rejected.
- Contract: X-11 requires a machine-valid OpenRPC 1.2.6 compatibility source;
  A-45 requires strict reproducible generation. Round-two finding two required
  a deterministic check that prevents invalid server URI drift.
- Required change: validate the exact input string against URI syntax instead
  of accepting a parser-normalized form. A strict parse-and-round-trip check or
  a pinned URI-format validator can enforce this; inspect raw fragment and
  query delimiters explicitly if the documented no-fragment/no-query policy is
  retained.
- Required proof: reject raw space, backslash, pipe, bracket, invalid Unicode
  host, fragment, and empty/nonempty query inputs; accept the committed
  synthetic URI; rerun official meta-schema and deterministic generation.
- Disposition: `current_blocker`.

## Round-three correction results

- HTTP 408 now returns `ErrorTimeout`, status 408, `Retryable: true`, and
  `errors.Is(err, context.DeadlineExceeded) == true` for both small and
  oversized untrusted bodies. HTTP 429 remains `ErrorRateLimited`, retryable,
  and does not match the deadline sentinel. Status-first response handling is
  preserved.
- `openrpc.json` now uses the reserved synthetic absolute endpoint
  `https://nzbget.invalid/jsonrpc`. It passes the official OpenRPC 1.11.1
  meta-schema whose supported-version enum begins with 1.2.6, using Draft 7 and
  format-aware URI validation. The remaining issue is the generator drift gate
  described above.
- Endpoint path decoding reaches a fixed point under a 32-pass cap and fails
  closed at the cap. Independent public-client probes rejected one-, nine-,
  and 33-layer dot/separator forms, encoded NUL/LF/DEL, and invalid UTF-8.
  `/proxy/nzbget%20rpc/` and `/safe%25value/jsonrpc` remained accepted and were
  emitted as their exact configured `RequestURI`.

## Prior safeguards rechecked

- Raw transport errors, endpoint text, response bodies, and upstream RPC
  messages are absent from public error strings, details, and unwrap chains.
  Safe context cancellation and deadline sentinels remain discoverable.
- Body I/O, cancellation, deadline, actual size overflow, 401, 403, 408, 429,
  and 5xx remain distinct bounded outcomes with expected retryability.
- Duplicate-aware JSON decoding rejects duplicate envelope/nested members,
  trailing JSON, wrong/missing/null IDs and versions, incoherent result/error
  branches, and incomplete error objects. Valid pinned NZBGet one-branch
  success and fault responses remain accepted.
- The OpenRPC document contains exactly `version`, `listgroups`, `listfiles`,
  and `history`; all use `by-position`, every argument is required, nullable
  values use Draft 7 unions, and no OpenAPI `nullable` keyword remains.
- Generated wrappers retain exact positional argument order, unsigned Hi/Lo
  halves, 64-bit combinations, nullable/version-specific pointers, and strict
  ID/NZBID alias behavior.
- `rpc.discover`, mutation names, and safe unknown methods return
  `unsupported` with zero dispatch. Malformed method names and invalid
  supported arguments remain `invalid_input`.
- `clients/nzbget` remains an independent standard-library-only module with no
  Mastarr root/internal import, local replacement, or `go.work` dependency.
  Fixtures remain small synthetic JSON documents.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/tree/handoff/state identities, ancestry, six-file owned diff, and scoped SHA-256 | Passed; identities and digest recorded above. |
| `GOWORK=off go test -mod=readonly -count=25 -timeout=180s ./...` in `clients/nzbget` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=10 -timeout=240s ./...` in `clients/nzbget` | Passed. |
| Client vet, module verification, and `go mod tidy -diff` | Passed with no diff. |
| Tools generator tests and race at `-count=25`/`-count=10`, full tools tests, vet, module verification, and tidy diff | Passed. |
| `check-generation.sh`, `go generate`, direct generator `-check`, `gofmt -d`, and `git diff --check` | Passed; generated output reproduced and detached checkout stayed clean. |
| Root test, vet, module verification, and tidy diff | Passed; normal storage tests completed in 11.498 seconds. |
| Root `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed all packages; storage completed in 201.986 seconds. |
| `generate.sh --check`, API, lint, architecture, planning self-test/normal, and fast guardrails | Passed; API score 100, zero lint issues, 43 tasks and 60 acceptance cases. |
| Root, UI, tools, qBittorrent client, and NZBGet client module verification | Passed. UI and qBittorrent module tests/vet also passed. |
| Client test and generator build cross-compiles | Passed with CGO disabled for Linux amd64/arm64, Windows amd64, Darwin arm64, and AIX ppc64. |
| Official OpenRPC 1.11.1/Draft 7/Ajv-format validation of committed document | Passed. Invalid generator mutations failed as recorded in the finding. |
| External 408/429, deep path, encoded control/UTF-8, clean URI, exact request-target, and unsupported zero-dispatch probes | Passed all round-three runtime requirements. |
| Four-method/no-mutation/root-import scan and synthetic fixture parse/public-secret scan | Passed; only explicit fake test credentials were found. |
| Clean detached worktree status | Passed before receipt creation. |

## Acceptance contribution

- A-05: accepted for X-11. Positional queue/file/history reads, envelope binding,
  Hi/Lo values, aliases, and nullable evidence remain correct.
- A-06: accepted for X-11. Public failures remain bounded and sanitized;
  descriptor translation remains X-13 scope.
- A-09: accepted for X-11. Client-local endpoint, credentials, request IDs,
  exact request targets, and fail-closed runtime endpoint validation pass.
- A-45: not accepted. Generated output is deterministic and all module gates
  pass, but the generator still accepts server values rejected by the official
  URI format.

## Reviewer decision

`changes_requested`. Correct the one generator URI-validation finding and add
its exact mutation matrix before another independent review. The committed
OpenRPC document and all runtime client behavior otherwise pass this review.
