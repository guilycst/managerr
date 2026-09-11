# X-11 correction round 2 handoff

## Assignment

- Task ID and title: X-11, standalone NZBGet read client, correction round 2.
- Owner and next independent reviewer: `/root/d01_implementer`; `/root/f01_reviewer`.
- Assigned base SHA: `05b2d6e6a2f99252fdcf71b13af5a49acdb0b69f`.
- Reviewed product context: `7b88619ba7371b1379e1d2a50c756c703d7fd02a`.
- Independent review receipt: `0cfcaa1fe437b699595654ba750195c739dff668`.
- Checkout ownership: shared `main` checkout; coordinator owns integration and execution state.
- Owned product paths: `clients/nzbget/` and `tools/internal/nzbgetgen/`.
- Owned evidence path: this correction handoff only. `X-11.md` was left unchanged.
- No root imports, state, task, contract, script, CI, or live media changes were made.

## Correction result

Product commit: `889c3fdeb6558096e0480e1c1597a8c1aa03c152` (`fix(nzbget): harden standalone client contract`).

The correction addresses every finding in the review receipt:

- Transport failures are converted to sanitized typed errors. The public error
  value has no raw `http.Client.Do` cause, URL, credential, or private error
  text in its chain. `errors.Is` still recognizes `context.Canceled` and
  `context.DeadlineExceeded` through `UpstreamError.Is`.
- HTTP status is classified before any non-success body is retained. Oversized
  successful bodies produce `ErrorResponseTooLarge`; successful body I/O
  failures produce retryable `ErrorUnavailable`; cancellation and deadline
  failures remain their safe context kinds. Existing 401, 403, 408, 429 and
  5xx status/kind/retryability mappings are preserved.
- JSON-RPC response decoding rejects duplicate object members (including
  nested error members), missing or null `version`/`id`, wrong envelope
  versions or IDs, simultaneous or absent `result`/`error`, null results, and
  error objects without present non-null numeric `code` and string `message`.
- The Mastarr-owned OpenRPC document remains exactly four read methods and
  now marks every positional parameter required. All former OpenAPI
  `nullable` fields use Draft 7 `type` unions containing `null`. The generator
  pins OpenRPC `1.2.6`, validates required parameters, rejects `nullable`,
  checks supported type unions, and retains deterministic generated output.
- Endpoint parsing preserves clean custom prefixes and escaped request paths,
  while rejecting dot and encoded-dot segments, encoded slash/backslash (also
  when repeatedly encoded), and repeated separators. Tests assert the exact
  clean `RequestURI` sent to a synthetic server.
- Syntactically safe locally refused methods, including `rpc.discover`, known
  mutation names, and unknown safe names, return `ErrorUnsupported` before any
  network dispatch. Malformed names and invalid arguments remain
  `ErrorInvalidInput`.

Generated output remains `clients/nzbget/generated.go`; it is reproduced from
`clients/nzbget/openrpc.json` by `tools/internal/nzbgetgen` and was not
hand-edited.

## Verification

All commands below were run after the product changes and before the product
commit unless stated otherwise; each returned exit status 0.

| Command | Result |
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
| `cd tools && GOWORK=off GOOS=linux GOARCH=amd64 go build -o /tmp/mastarr-nzbgetgen ./internal/nzbgetgen` | Passed. |
| `cd clients/nzbget && GOWORK=off GOOS=linux GOARCH=amd64 go test -c -o /tmp/mastarr-nzbget.test` | Passed. |
| `GOWORK=off go test -count=1 -timeout 180s ./...` | Passed all root packages. |
| `GOWORK=off go test -race -count=1 -timeout 240s ./...` | Passed all root packages, including storage. |
| `GOWORK=off go vet ./...` | Passed. |
| `./scripts/generate.sh --check` | Passed: `generation checks passed`. |
| `./scripts/check-api.sh` | Passed: `API checks passed`. |
| `./scripts/check-lint.sh` | Passed: `0 issues` and architecture checks. |
| `python3 scripts/check-architecture.py` | Passed: import boundaries. |
| `python3 scripts/check_planning.py` | Passed: 43 tasks, 60 acceptance cases, local links resolve. |
| `./scripts/check-guardrails.sh --fast` | Passed: generation, API, architecture, focused checks. |
| `gofmt` on owned Go files and `git diff --check` | Passed with no diff. |

Focused tests are synthetic and cover sanitized transport failures, body
overflow versus body I/O and cancellation, status-first classification,
duplicate/missing/null JSON-RPC members, unsupported local methods with a zero
network dispatch count, unsafe endpoint variants, exact custom `RequestURI`,
required OpenRPC parameters, pinned-version rejection, nullable-key rejection,
and Draft 7 union semantics.

## Review and resume

- Acceptance contribution: A-05, A-06, A-09, and A-45; the same four-read
  method boundary and no-mutation contract remains in force.
- No live NZBGet endpoint, credentials, private inventory, mutation, release,
  deployment, or real media path was used.
- Independent review of product commit `889c3fdeb6558096e0480e1c1597a8c1aa03c152` is pending.
- X-13 remains responsible for translating this independent module into the
  root NZBGet adapter; this correction does not add a local `replace` or
  `go.work` dependency.
- The coordinator must record this product SHA and the separate handoff SHA
  in execution state. No state file was edited in this lane.
