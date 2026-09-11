# X-11 correction round 3 handoff

## Assignment

- Task ID and title: X-11, standalone NZBGet read client, correction round 3.
- Owner and next independent reviewer: `/root/d01_implementer`; `/root/f01_reviewer`.
- Review receipt addressed: `147494c3dea44fd7e62f94c41d20ccd512b3eb6f`.
- Prior correction product: `889c3fdeb6558096e0480e1c1597a8c1aa03c152`.
- Prior correction handoff: `ed93915dfcf5858f84ad8c505331da51909e7a33`.
- Shared checkout state before this round: `c5cb6066d63b6f0e2da23c3a3c93d1a862156d38`.
- Checkout ownership: shared `main`; coordinator owns integration and execution state.
- Owned product paths: `clients/nzbget/` and `tools/internal/nzbgetgen/`.
- Owned evidence path: this new correction handoff only. Existing X-11
  handoffs and execution state were left unchanged.
- No root imports, task/state files, CI/scripts, real credentials, live
  services, or media paths were changed or accessed.

## Product result

Product commit: `fcaed0489bd22692d1a0bae7ea9b8b96ad439453` (`fix(nzbget): close round-three contract gaps`).

The three blockers in the review receipt are closed:

- HTTP 408 is now `ErrorTimeout` with `Retryable: true` and the safe
  `errors.Is(err, context.DeadlineExceeded)` behavior. HTTP 429 remains
  `ErrorRateLimited`; 401, 403, and 5xx mappings retain their status and
  retryability semantics. Both bounded oversized and small response cases
  assert the distinction.
- The OpenRPC server entry now uses the valid synthetic absolute URI
  `https://nzbget.invalid/jsonrpc`. The deterministic generator parses and
  validates every server entry, requires a server, rejects URI templates,
  relative values, credentials, queries/fragments, and non-HTTP(S) values.
  Generator tests mutate the server URI and verify rejection. The official
  OpenRPC 1.11.1 meta-schema with URI format validation accepts the committed
  1.2.6 document.
- Endpoint path validation rejects decoded controls, NUL, DEL, invalid
  UTF-8, dot segments, encoded separators, and repeatedly encoded traversal.
  It decodes to a fixed point with a bounded 32-pass policy and fails closed
  when the bound is exceeded. Clean custom prefixes, including `%20`, remain
  unchanged and their exact `RequestURI` is asserted.

All prior correction safeguards remain intact: sanitized transport errors with
safe context sentinels, status-before-body retention, duplicate-aware strict
JSON-RPC envelopes, required positional parameters, Draft 7 null unions,
exactly four read methods, no `rpc.discover` dispatch, local unsupported-method
classification, Basic authentication, bounded responses, monotonic IDs, and
no root-module imports.

`clients/nzbget/generated.go` remains deterministic generated output. The
contract change affects only the OpenRPC server metadata and does not add a
method, mutation, or generated API surface.

## Verification

All commands below were run for this round; each returned exit status 0 unless
explicitly stated otherwise.

| Command or scenario | Result |
| --- | --- |
| `cd clients/nzbget && GOWORK=off go test -count=1 -timeout 90s ./...` | Passed. |
| `cd clients/nzbget && GOWORK=off go test -race -count=1 -timeout 120s ./...` | Passed. |
| `cd clients/nzbget && GOWORK=off go vet ./...` | Passed. |
| `cd clients/nzbget && GOWORK=off go mod verify` | Passed: `all modules verified`. |
| `cd clients/nzbget && ./check-generation.sh` | Passed: `NZBGet generation checks passed`. |
| `cd tools && GOWORK=off go test -count=1 -timeout 90s ./internal/nzbgetgen` | Passed. |
| `cd tools && GOWORK=off go test -race -count=1 -timeout 120s ./internal/nzbgetgen` | Passed. |
| `cd tools && GOWORK=off go vet ./internal/nzbgetgen` | Passed. |
| `cd tools && GOWORK=off go mod verify` | Passed: `all modules verified`. |
| `cd tools && GOWORK=off go run ./internal/nzbgetgen -check -input ../clients/nzbget/openrpc.json -output ../clients/nzbget/generated.go` | Passed. |
| `cd tools && GOWORK=off GOOS=linux GOARCH=amd64 go build -o /tmp/mastarr-nzbgetgen-r3 ./internal/nzbgetgen` | Passed. |
| `cd clients/nzbget && GOWORK=off GOOS=linux GOARCH=amd64 go test -c -o /tmp/mastarr-nzbget-r3.test` | Passed. |
| Official schema: `curl -fsSL https://github.com/open-rpc/meta-schema/releases/download/1.11.1/open-rpc-meta-schema.json -o /tmp/open-rpc-meta-schema.json` followed by the temporary `github.com/santhosh-tekuri/jsonschema/v6` Draft 7 validator harness | Passed: `valid`. No product dependency was added. |
| Generator server mutation tests for `not a uri`, `{endpoint}/jsonrpc`, relative URI, and no servers | Passed: all rejected. |
| `GOWORK=off go test -count=1 -timeout 180s ./...` | Passed all root packages. |
| `GOWORK=off go test -race -count=1 -timeout 300s ./...` | Passed all root packages, including storage. |
| `GOWORK=off go vet ./...` | Passed. |
| `./scripts/generate.sh --check` | Passed: `generation checks passed`. |
| `./scripts/check-api.sh` | Passed: `API checks passed`. |
| `./scripts/check-lint.sh` | Passed: zero issues and architecture checks. |
| `python3 scripts/check-architecture.py` | Passed: import boundaries. |
| `python3 scripts/check_planning.py` | Passed: 43 tasks, 60 acceptance cases, links resolve. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| `gofmt` on owned Go files and `git diff --check` | Passed with no diff. |

Focused regressions cover 408 and 429 with oversized and small bodies,
deadline sentinel behavior, invalid and official server URI semantics,
one-layer and nine-plus-layer encoded dot/separator paths, encoded NUL/LF/DEL,
invalid UTF-8, clean encoded-space prefixes, and exact request targets.

## Review and resume

- Acceptance contribution: A-05, A-06, A-09, and A-45.
- Independent review of product commit
  `fcaed0489bd22692d1a0bae7ea9b8b96ad439453` is pending with
  `/root/f01_reviewer`.
- X-13 remains responsible for translating this independent module into the
  root NZBGet adapter. This lane added no local `replace` or `go.work`.
- The coordinator must record the product and handoff commits in execution
  state. This lane did not edit `docs/execution/state.json`.
- No live NZBGet instance, credentials, private inventory, mutation, release,
  deployment, or real media path was used.
