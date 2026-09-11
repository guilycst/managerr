# X-11 correction round 4 handoff

## Assignment

- Task ID and title: X-11, standalone NZBGet read client, correction round 4.
- Owner and next independent reviewer: `/root/d01_implementer`; `/root/f01_reviewer`.
- Review receipt addressed: `806cce9de10fc66146d4720cebceea53b78e89d0`.
- Dispatch checkout SHA: `668bdb35d32c9667bfae1c72664da36788d29879`.
- Prior correction product and handoff: `fcaed0489bd22692d1a0bae7ea9b8b96ad439453` and `41f10c9c4386bd3afab13fa93804f2681be26c8a`.
- Checkout ownership: shared `main`; coordinator owns integration and execution state.
- Owned product paths: `clients/nzbget/` and `tools/internal/nzbgetgen/`.
- Owned evidence path: this new correction handoff only. Existing X-11
  handoffs and execution state were left unchanged.
- No root imports, state/task files, CI/scripts, live services, credentials,
  private inventory, or media paths were changed or accessed.

## Product result

Product commit: `1b9b3f9c425e56a7a3be6dfb43829157d08877c9` (`fix(nzbget): validate server URI syntax`).

The remaining URI-validation blocker is closed:

- `tools/internal/nzbgetgen` now validates every `servers[].url` against the
  exact source text before parser normalization. It rejects raw spaces,
  backslashes, pipes, brackets, controls, Unicode bytes, URI templates,
  fragments, nonempty queries, trailing `?`/`ForceQuery`, relative or
  non-HTTP(S) values, credentials, and parser round-trip changes. Parsed host
  values are also required to remain ASCII, closing percent-encoded Unicode
  host forms that become Unicode during parsing.
- The committed synthetic server URI remains
  `https://nzbget.invalid/jsonrpc`, which passes the official format-aware
  OpenRPC meta-schema. Clean escaped URI values such as `%20`, encoded
  fragment/query characters in a path, and path parameters remain valid URI
  syntax when they do not violate the compatibility endpoint policy.
- Deterministic generator tests cover all rejected and accepted URI cases,
  pinned OpenRPC 1.2.6, required positional parameters, Draft 7 null unions,
  and the exact four-method read surface.

The runtime 408 timeout, fail-closed endpoint path decoder, sanitized
transport errors, status-first body handling, duplicate-aware JSON-RPC
envelope, local unsupported-method boundary, Basic authentication, bounded
responses, monotonic request IDs, and no-root-import module boundary remain
unchanged from the reviewed prior product.

Only the two generator files changed in this correction product commit; no
generated Go output or runtime client file required regeneration changes.

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
| `cd tools && GOWORK=off GOOS=linux GOARCH=amd64 go build -o /tmp/mastarr-nzbgetgen-r4 ./internal/nzbgetgen` | Passed. |
| `cd clients/nzbget && GOWORK=off GOOS=linux GOARCH=amd64 go test -c -o /tmp/mastarr-nzbget-r4.test` | Passed. |
| Official schema: `curl -fsSL https://github.com/open-rpc/meta-schema/releases/download/1.11.1/open-rpc-meta-schema.json -o /tmp/open-rpc-meta-schema.json`, followed by the temporary `github.com/santhosh-tekuri/jsonschema/v6` Draft 7 validator harness | Passed: `valid`. No product dependency was added. |
| Generator URI mutation matrix: raw space, backslash, pipe, brackets, raw and percent-encoded Unicode host, raw fragment, trailing/nonempty query, template, relative, and missing server | Passed: all invalid cases rejected. |
| Generator URI acceptance matrix: synthetic URI, encoded space, encoded path `#`/`?`, and semicolon parameter | Passed. |
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

Focused runtime regressions from the prior product were re-run and remain
green for 408/429 taxonomy and sentinels, deep encoded path traversal,
encoded controls and invalid UTF-8, exact clean custom request targets,
duplicate envelopes, and unsupported local methods with zero dispatch.

## Review and resume

- Acceptance contribution: A-05, A-06, A-09, and A-45.
- Independent review of product commit
  `1b9b3f9c425e56a7a3be6dfb43829157d08877c9` is pending with
  `/root/f01_reviewer`.
- X-13 remains responsible for translating this standalone module into the
  root NZBGet adapter. This correction adds no local `replace` or `go.work`.
- The coordinator must record the product and handoff commits in execution
  state. This lane did not edit `docs/execution/state.json`.
- No live NZBGet instance, credentials, private inventory, mutation, release,
  deployment, or real media path was used.
