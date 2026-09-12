# X-10 independent review, round eleven

## Decision

`approved`.

The round-ten P1 closes for every received non-200 login status. Arbitrary
body-read failures no longer erase status-first authentication outcomes. The
canceled leader receives its own context error, joined live callers receive
the same typed upstream result, and no duplicate login occurs. An independent
eight-waiter matrix covered 401, 403, 429 and 500 for ten race-enabled
repetitions. An unreadable HTTP 200 remained incomplete, installed no session,
and elected exactly one replacement login for the active waiters.

The new handoff also corrects the prior stale lineage. It explicitly records
round-ten dispatch checkpoint `1b3cb11...`, round-nine review receipt
`bb124a5...`, and round-ten source product `f514ced...`, while its current
assignment accurately identifies dispatch `5e06129...`, round-ten receipt
`63b185c...`, and product parent `5e06129...`.

No unresolved finding remains in the reviewed X-10 scope.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Reviewed product commit:
  `a9c847ff562fad1a3edf23e59bb6f6b27facb7af`.
- Product parent:
  `5e061298732e5af3cf18d068a5f2315d884d031b`.
- Product tree:
  `919a0e7b886adbd8683c8364a252cd2c2d4a5b28`.
- Reviewed handoff commit:
  `2cde17f8bff2306112be8aa047220baa9acdad43`.
- Handoff tree:
  `93814a6d5ea74b102cfca10d9343c201dd955891`.
- Coordinator state checkpoint:
  `b8d0c639e292eba4fdaa1a390277bb32ecf194ac`.
- Round-ten receipt:
  `63b185c02e0276d9d3df82ef57ca34c8f9b8d40e`.
- Scope: `clients/qbittorrent/`, X-10, A-04/A-09/A-28/A-45, both
  round-ten findings and every retained qBittorrent safeguard.
- The product changes exactly `README.md`, `client.go` and `client_test.go`
  inside the assigned standalone module. It changes no generated output, root
  adapter, shared API, execution state, task, plan or script.
- Scoped product diff SHA-256:
  `8f07e6c83b06829ede5596efb9cadc782f031ffdb0fc10d809d250faba14afe4`.
- Scoped product archive SHA-256:
  `1593f7f314c80b46a2a35c52a87888dde336a27391105c6560b90c62ac9dd515`.
- Clean checks ran in a detached reviewer-owned worktree at the exact product
  commit. Independent probes were temporary and removed before the clean
  worktree check.
- No live qBittorrent instance, credential, private coordinate, tracker,
  torrent, media payload, mutation, release or deployment was used.

## Round-ten finding verification

| Round-ten requirement | Independent result |
| --- | --- |
| Received non-200 remains semantically complete when its body read fails | Closed. `requestOnce` marks every received non-200 status complete independently of body retention at `client.go:753-763`. |
| Status-first Unauthorized remains immutable at leader cancellation | Closed. Product and independent probes return the leader's `context.Canceled` and typed Unauthorized to joined waiters from one 403 response. |
| No duplicate login for unreadable 401/403 | Closed. Eight-waiter independent 401 and 403 cases each issued exactly one POST across ten race-enabled repetitions. |
| Rate-limit and server failures remain typed and shared | Closed. Independent unreadable-body 429 and 500 cases returned `rate_limited` and `unavailable` respectively, preserved status, and issued one POST. |
| Unreadable HTTP 200 does not become successful | Closed. Eight active waiters elected one replacement, used its validated SID, and total login count was exactly two. |
| No-response transport interruption still permits replacement | Closed. The retained canceled-leader regression performs one later login and one successful read. |
| Round-ten handoff lineage | Closed by explicit correction in `X-10-correction-round11.md`: dispatch `1b3cb11e826b7b65f0953e283af42c73d6a3ccf6`, round-nine receipt `bb124a5287bb7a86134dcf928818edfb6ca37d32`, and source product `f514ced46d72775b4609f3b7956a79d54468777b`. |

## Retained safeguard verification

| Safeguard | Independent result |
| --- | --- |
| Shared auth outcome and caller cancellation | Passed. Completed success/failure results remain immutable for joined callers; leader and waiter cancellation stays caller-local. |
| SID and generation request binding | Passed. SID and generation remain one credential snapshot selected under the auth lock and installed before dispatch. |
| Stale response and cookie fencing | Passed. No automatic cookie jar exists; delayed cookies cannot replace the active SID and stale rejections cannot invalidate a newer generation. |
| Login cookie validation | Passed. Missing, empty, wrong-name/path/domain, deletion/expired and Secure-over-HTTP SID cookies fail. |
| Exact status and bounded errors | Passed. Only HTTP 200 is success, response bodies remain bounded, and errors contain no upstream body text or credentials. |
| Strict JSON and required members | Passed. Duplicate/case-folded/invalid-UTF-8/unknown/trailing data and missing/null required values fail while valid zero values pass. |
| Torrent identity and input bounds | Passed. Supported v1/v2 hashes, case-insensitive duplicate identities, delimiters, controls, whitespace and aggregate limits remain exact. |
| Versions, piece ranges and unsupported results | Passed. Version shapes and ranges remain strict; unsupported remains distinct from unavailable. |
| Endpoint, redirect and request security | Passed. Literal prefixes, Origin/Referer, redirect refusal, isolated credentials and safe timeout defaults remain intact. |
| Read-only and module boundaries | Passed. Seven GET operations plus the sole login POST; no mutation route, root import, local replace or generated DTO leak. |

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/parent/tree/handoff/state, task/spec/acceptance and three-file scope | Inspected directly; identities above are exact. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` in `clients/qbittorrent` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed. |
| Retained cancellation/auth/session/status matrix, twenty repetitions | Passed. |
| Same focused matrix under `-race`, five repetitions | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `GOWORK=off go mod verify` | Passed: all modules verified. |
| `GOWORK=off go mod tidy -diff` | Passed with no diff. |
| `GOWORK=off go generate ./...` and `./check-generation.sh` | Passed; pinned oapi-codegen v2.8.0 output remained clean and reproducible. |
| Module-local Vacuum lint | Passed, quality 100/100. |
| `python3 scripts/check_planning.py` | Passed: 43 tasks, 60 acceptance cases and local links. |
| `./scripts/check-api.sh` | Passed. |
| `python3 scripts/check-architecture.py` and `./scripts/check-lint.sh` | Passed. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed root/UI/tools generation, API, lint, architecture, tests, vet and module verification. |
| Linux amd64/arm64 CGO-free module test compile | Passed; both outputs are statically linked Linux ELF executables. |
| OpenAPI/module structural parser | Passed: eight operations, login-only POST, no mutation/security scheme/local replace. |
| Public credential/private-coordinate scan | Passed. |
| Scoped diff/archive hashes, `git diff --check` and clean reviewer worktree | Passed before receipt creation. |
| Independent unreadable 401/403/429/500, eight-waiter matrix under `-race`, ten repetitions | Passed; each status remained one typed shared outcome with one POST. |
| Independent unreadable-200, eight-waiter matrix under `-race`, ten repetitions | Passed; one replacement flight, validated success, exactly two POSTs. |

## Acceptance contribution

- A-04: accepted for X-10. Supported torrent identities and whole-response
  duplicate handling are exact and scoped to this client result.
- A-09: accepted for X-10. Connection-local SID/generation binding and shared
  authentication outcomes do not collide or cross caller context state.
- A-28: accepted for this read-only X-10 contribution. State, progress,
  completion and seeding evidence remain bounded; no control route exists.
- A-45: accepted for X-10. Generated output is reproducible and every assigned
  module, race, vet, API, architecture, lint, guardrail and portable-build gate
  passes independently.

## Reviewer decision

`approved`. The standalone qBittorrent read client closes the round-ten
runtime and evidence findings and preserves all previously reviewed safeguards.
