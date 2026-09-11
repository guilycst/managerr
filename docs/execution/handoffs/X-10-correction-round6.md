# X-10 correction handoff, round six

## Assignment

- Task ID and title: X-10, qBittorrent standalone read client; correction round six.
- Owner and independent reviewer: `/root/c01_implementer`; `/root/d01_reviewer`.
- Base product SHA: `797822ff86bbe9882354e9f1e395e5408c527d03`.
- Review receipt: `7b51ffc0a4286d92768df1b7b29f667eb6c32d90`.
- State checkpoint used for dispatch: `982b984`.
- Branch/worktree: shared checkout on `main`; coordinator owns integration and `docs/execution/state.json`.
- Owned product path: `clients/qbittorrent/`.
- Owned evidence path: this handoff only; earlier round receipts and handoffs remain untouched.
- Required acceptance contributions: A-04, A-09, A-28 and A-45.

## Product result

Product commit: `9202d4ccab811f0daccd089e2cce642b4805dc80` (`fix(qbittorrent): guard session refresh generations`).

Each authenticated read now captures the monotonic session generation that
was current after authentication. A 401 or 403 invalidates the session only
when the rejected request's generation is still current. If another caller has
already completed a refresh, the stale rejection leaves the newer session
authenticated and retries once through that session. The generation advances
only after a successful login, while the prior immutable authentication-flight
result and caller-selectable cancellation behavior remain intact.

The deterministic regression starts three reads under the initial SID, holds
all of their old-session requests, releases one rejection to trigger the sole
refresh, waits until the fresh login completes, then releases the remaining
delayed old-session 403 responses. All reads succeed through the new SID,
exactly two login POSTs occur (initial plus one refresh), all three fresh
retries are observed, and a later read does not trigger a stale-session third
login. Existing shared-failure, canceled-waiter, successful-concurrency and
expired-session tests remain covered.

README and tests document the generation binding. No qBittorrent mutation
route, root-module import, live service, credential, private coordinate,
media payload, release or deployment was used.

## Verification

Each command below returned exit status 0. Module commands were run from
`clients/qbittorrent`; repository commands were run from the repository root.

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./...` in the standalone module | Passed | `clients/qbittorrent/client_test.go` |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed | `clients/qbittorrent/client_test.go` |
| `GOWORK=off go vet -mod=readonly ./...` | Passed | `clients/qbittorrent/` |
| `GOWORK=off go mod verify` | Passed: all modules verified | `clients/qbittorrent/go.sum` |
| `GOWORK=off go mod tidy -diff` | Passed with no diff | `clients/qbittorrent/go.mod` |
| `GOWORK=off go generate ./...` and `./check-generation.sh` | Pinned generated output reproduced byte-for-byte | `clients/qbittorrent/generated/client.gen.go` |
| Module-local Vacuum lint with `../../api/vacuum.yaml` | Quality 100/100 | `clients/qbittorrent/openapi.yaml` |
| `python3 scripts/check_planning.py` | Planning valid: 43 tasks, 60 acceptance cases | `docs/execution/` |
| `./scripts/check-api.sh` | Passed | repository API guardrail |
| `./scripts/check-guardrails.sh --fast` | Passed generation, API, architecture and focused root checks | repository guardrails |
| `./scripts/check-guardrails.sh --ci` | Passed all generation, lint, architecture, tests, vet, module and UI/tool checks | repository guardrails |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -c -o /tmp/mastarr-qbittorrent-round6-amd64.test .` | Passed; static Linux x86-64 ELF | `/tmp/mastarr-qbittorrent-round6-amd64.test` |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -c -o /tmp/mastarr-qbittorrent-round6-arm64.test .` | Passed; static Linux aarch64 ELF | `/tmp/mastarr-qbittorrent-round6-arm64.test` |
| `GOWORK=off go test -mod=readonly -count=20 -run 'Test(StaleSessionRejectionCannotInvalidateFreshSession|ConcurrentAuthenticationFailureIsShared|AuthenticationWaitersHonorCancellation|ConcurrentLoginIsIdempotentAndExpiredSessionReauthenticates)$' ./...` | Passed repeated session-generation, shared-failure, cancellation and reauthentication runs | `client_test.go` |
| Focused generation/auth matrix under `-race`, repeated five times | Passed | `client_test.go` |
| Delayed old-session matrix | Three old-session 403 responses produced exactly two total login POSTs, three fresh retries, successful reads and no stale-session third login | `TestStaleSessionRejectionCannotInvalidateFreshSession` |
| Product commit hook | Passed generation, Vacuum/API, architecture and focused root guardrails | product commit output |

## Review and integration

- Finding addressed: the round-five P1 in receipt `7b51ffc...`: a delayed 401/403 from an older authenticated request can no longer invalidate a newer successfully refreshed session or trigger a redundant login.
- Required semantics: request generation is monotonic and bound before each read; invalidation is compare-and-swap guarded; stale failures retry once using current authentication; current-generation failures still refresh once; immutable shared auth results and caller-specific cancellation remain intact.
- Earlier corrections retained: complete required response members and null rejection, exact HTTP 200 success, strict duplicate/case/UTF-8 JSON, inventory identity validation and duplicate rejection, safe timeout defaults, usable SID cookies, bounded/sanitized errors, strict versions/ranges, input bounds, endpoint path policy, unsupported-route semantics, read-only routes and no root imports.
- Final independent review: pending against product `9202d4ccab811f0daccd089e2cce642b4805dc80`.
- Integrated commit and execution-state update: pending coordinator action; this worker did not edit `docs/execution/state.json`.

## Resume checkpoint

- Product is ready at `9202d4ccab811f0daccd089e2cce642b4805dc80`; no implementation blocker remains for this correction slice.
- The module remains a standalone compatibility client and has not been exercised against a live qBittorrent instance.
- Next safe action: independent review against the exact product SHA, followed by coordinator state recording.
