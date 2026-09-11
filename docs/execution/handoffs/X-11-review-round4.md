# X-11 independent review, round four

## Decision

`approved`.

The exact correction closes the last server-URI validation finding. The
committed OpenRPC 1.2.6 document passes the official format-aware meta-schema,
the generator rejects invalid or parser-normalized server values, and every
prior X-11 transport, envelope, endpoint, modeling, read-only, generation, and
module safeguard remains green. No unresolved finding remains in X-11 scope.

## Review identity and boundary

- Reviewer: `/root/f01_reviewer`, independent of product author
  `/root/d01_implementer`.
- Reviewed product commit:
  `1b9b3f9c425e56a7a3be6dfb43829157d08877c9`; direct parent
  `668bdb35d32c9667bfae1c72664da36788d29879`; product tree
  `3615df84c384bd37d27ad23e61e531ce6873b5d9`.
- Reviewed handoff commit:
  `284b2c511ed10d4c39de621049b9728d7bfd9a52`; direct child of the product.
- Coordinator state checkpoint:
  `f21d7c16d7453e78a4cd7be0e17ee030709c497f`; direct child of the handoff.
- Prior round-three review receipt:
  `806cce9de10fc66146d4720cebceea53b78e89d0`.
- Review scope: `clients/nzbget/`, `tools/internal/nzbgetgen/`, X-11,
  A-05/A-06/A-09/A-45, and all findings from rounds one through three.
- The product commit changes only
  `tools/internal/nzbgetgen/main.go` and `main_test.go`. Scoped product diff
  SHA-256:
  `9087fe91a70206aa1c5b8572f75b3bedf58c2d12da3bcbe5ce343267f1e98410`.
- Checks ran in clean detached reviewer worktree
  `/Users/guilhermecastro/.codex/worktrees/managerr-f01-review-r1` at the exact
  product commit. Disposable external harnesses exercised public behavior and
  generator mutations without changing product files.
- No live NZBGet service, credential, private inventory, retained NZB, media,
  upstream mutation, release, or deployment was used.

## Round-four finding closure

`validateServers` now validates the exact source text before accepting Go's
parsed representation. It rejects non-ASCII/control input, braces, raw URI
characters excluded by this compatibility policy, non-HTTP(S) and relative
values, credentials, queries/fragments, empty query delimiters, invalid hosts,
and any parser round-trip change. Parsed hosts must also remain ASCII, which
rejects percent-encoded Unicode host forms.

The complete independent rejection matrix passed:

```text
raw space                    rejected
raw backslash                rejected
raw pipe                     rejected
raw left/right bracket       rejected
raw Unicode host             rejected
percent-encoded Unicode host rejected
raw fragment / empty #       rejected
nonempty query / trailing ?  rejected
URI template                 rejected
relative URI                 rejected
userinfo                     rejected
non-HTTP scheme              rejected
raw control                  rejected
malformed percent escape     rejected
parser normalization change  rejected
```

The acceptance matrix also passed for the committed synthetic URI, encoded
space, encoded path `#` and `?`, semicolon path parameter, and encoded literal
percent. A broader printable-ASCII path probe accepted exactly the RFC 3986
path-character set permitted by the custom policy and rejected every raw
forbidden or parser-rewritten character.

The committed `https://nzbget.invalid/jsonrpc` server URI independently passed
Ajv 8 with `ajv-formats`, the official OpenRPC meta-schema 1.11.1 release asset
whose version enum begins with 1.2.6, and its HTTPS Draft 7 meta-schema.
Generator output remains byte-for-byte unchanged because the correction affects
validation rather than the four-method model.

## Prior safeguards rechecked

- The OpenRPC document contains exactly `version`, `listgroups`, `listfiles`,
  and `history`, all with `paramStructure: by-position`. Every positional
  argument is required, nullable values use Draft 7 type unions, and no
  OpenAPI `nullable` keyword exists.
- Generated wrappers retain `[]`, `[0]`, `[0,0,NZBID]`, and `[Hidden]` in exact
  order. Wire DTOs retain unsigned Hi/Lo halves, non-narrowed 64-bit combined
  values, nullable/version-specific pointers, native statuses and paths, and
  strict ID/NZBID alias handling.
- `rpc.discover`, mutation names, and syntactically safe unknown methods return
  `unsupported` before network dispatch. Malformed names and invalid arguments
  to supported methods remain `invalid_input`.
- Request IDs remain monotonic, nonzero, process-local, and safe under
  concurrency. Responses require one exact JSON-RPC 1.1 version and matching
  numeric ID plus exactly one coherent `result` or `error` branch.
- Duplicate-aware recursive decoding rejects duplicate top-level and nested
  members, missing/null fields, wrong version/ID, incomplete error objects,
  trailing JSON, and malformed results. Valid pinned NZBGet success and fault
  shapes remain accepted.
- HTTP status is classified before untrusted error-body retention. HTTP 408 is
  retryable `timeout` and safely matches `context.DeadlineExceeded`; HTTP 429
  remains retryable `rate_limited`. 401, 403, 5xx, body I/O, body overflow,
  cancellation, deadline, and transport failures retain their distinct types.
- Public errors contain no raw transport cause, endpoint, credential, response
  body, or upstream message. Context sentinels remain discoverable without a
  sensitive unwrap chain.
- Runtime endpoint paths reject ordinary and deeply nested dot/separator
  encodings, repeated separators, encoded controls/NUL/DEL, invalid UTF-8, and
  decoding beyond the 32-pass cap. Clean `%20` and encoded literal-percent
  paths preserve the exact configured `RequestURI`.
- Basic authentication, bounded/default deadlines, bounded success bodies,
  redirect isolation, version reporting, normalized observations, slice-copy
  behavior, and synthetic fixtures remain unchanged and covered.
- `clients/nzbget` remains an independent standard-library-only module with no
  Mastarr root/internal import, local replacement, or `go.work` dependency.
  The generator remains inside the tools module.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/tree/handoff/state identities, ancestry, two-file owned diff, and scoped SHA-256 | Passed; identities and digest recorded above. |
| Client `go test -mod=readonly -count=25` and `go test -race -count=10` | Passed. |
| Client vet, module verification, and `go mod tidy -diff` | Passed with no diff. |
| Tools generator `go test -count=25`, `go test -race -count=10`, full tools tests, vet, module verification, and tidy diff | Passed. |
| `check-generation.sh`, `go generate`, direct generator `-check`, `gofmt -d`, and `git diff --check` | Passed; generated output reproduced and detached checkout stayed clean. |
| Official OpenRPC 1.11.1 meta-schema, HTTPS Draft 7, and Ajv-format validation | Passed for the committed document. |
| Independent rejected/accepted URI mutation matrices and printable-ASCII normalization probe | Passed; exact cases recorded above. |
| External 408/429, transport/body/context, deep endpoint, clean request-target, and unsupported zero-dispatch probes | Passed. |
| Root test, vet, module verification, and tidy diff | Passed; normal storage tests completed in 15.467 seconds. |
| Root `go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed all packages; storage completed in 213.384 seconds. |
| `generate.sh --check`, API, lint, architecture, planning self-test/normal, and fast guardrails | Passed; API score 100, zero lint issues, 43 tasks and 60 acceptance cases. |
| Root, UI, tools, qBittorrent client, and NZBGet client module verification | Passed. UI and qBittorrent module tests/vet also passed. |
| Client test and generator build cross-compiles | Passed with CGO disabled for Linux amd64/arm64, Windows amd64, Darwin arm64, and AIX ppc64. |
| Four-method/no-mutation/root-import scan and synthetic fixture/public-secret scan | Passed; only explicit fake test credentials were found. |
| Clean detached reviewer status | Passed before receipt creation. |

## Acceptance contribution

- A-05: accepted for X-11. Positional queue/file/history reads, exact envelope
  binding, Hi/Lo values, aliases, and nullable evidence are covered.
- A-06: accepted for X-11. Public failures and response retention remain bounded
  and sanitized; descriptor translation remains X-13 scope.
- A-09: accepted for X-11. Endpoint, credentials, request IDs, response binding,
  and exact request-target behavior are isolated per client.
- A-45: accepted for X-11. The source contract passes official meta-schema
  validation, generation is deterministic, invalid URI drift is rejected, and
  every standalone/repository generation and module gate passes.

## Reviewer decision

`approved`. The exact X-11 product commit has no unresolved review finding.
This approves the standalone read-client and generator contribution only;
X-13 still owns translation into the root NZBGet adapter, and release,
deployment, and live-service behavior remain separate gates.
