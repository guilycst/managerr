# X-10 correction handoff, round eight

## Assignment

- Task ID and title: X-10, qBittorrent standalone read client; correction round eight.
- Owner and independent reviewer: `/root/c01_implementer`; `/root/d01_reviewer`.
- Base product SHA: `bcd5a926cc4b82cf2f3f8bc22655043bd0937b0b`.
- Review receipt: `e26ba78bbb75497fdea13cd0fb2b853d12c17c38`
  (`X-10-review-round7.md`).
- State checkpoint used for dispatch: `bcd5a92`.
- Branch/worktree: shared checkout on `main`; coordinator owns integration and
  `docs/execution/state.json`.
- Owned product path: `clients/qbittorrent/`.
- Owned evidence path: this handoff only; prior round receipts remain untouched.
- Required acceptance contributions: A-04, A-09, A-28 and A-45.

## Product result

Product commit: `da5fa85f22d0c5374657d14cdc6a16ea09e0e038`
(`fix(qbittorrent): isolate canceled auth leaders`).

Authentication flights now distinguish leader cancellation from ordinary
upstream failure. When the leader context is canceled or reaches its deadline,
the leader receives its own context error and no session is installed. Active
waiters see the completed flight's cancellation marker, then one waiter
becomes the next authentication leader while the others join that replacement
flight. This preserves one replacement login and prevents an uncanceled
caller from inheriting another caller's context state.

Waiters whose own contexts are canceled still return promptly and do not issue
a read. Ordinary upstream failures retain one immutable shared result, so a
shared 403 remains shared and only a later independent call deliberately
retries. Existing explicit SID-plus-generation binding, stale response
fencing, refresh CAS, read-only routes and sanitized bounded errors remain
unchanged.

The deterministic regression holds the first login, joins a live read waiter,
cancels only the leader, then verifies the waiter completes a second login and
one version read through `session-2`. It asserts the leader receives
`context.Canceled), the waiter succeeds, and exactly two login POSTs occur.
The README and auth-flight comments document the caller-scoped behavior.

No qBittorrent mutation route, root-module import, live service, credential,
private coordinate, media payload, release or deployment was used.

## Verification

Each command below returned exit status 0. Module commands ran from
`clients/qbittorrent`; repository commands ran from the repository root.

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed | `clients/qbittorrent/client_test.go` |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed | `clients/qbittorrent/client_test.go` |
| `GOWORK=off go vet -mod=readonly ./...` | Passed | `clients/qbittorrent/` |
| `GOWORK=off go mod verify` | Passed: all modules verified | `clients/qbittorrent/go.sum` |
| `GOWORK=off go mod tidy -diff` | Passed with no diff | `clients/qbittorrent/go.mod` |
| `./check-generation.sh` | Passed: pinned generated output reproduced byte-for-byte | `clients/qbittorrent/generated/client.gen.go` |
| Focused auth/session matrix, `-race -count=5` | Passed | canceled leader, canceled waiters, shared failure, reauthentication, stale SID and SID-generation tests |
| `python3 scripts/check_planning.py` | Planning valid: 43 tasks, 60 acceptance cases; local links resolve | `docs/execution/` |
| `./scripts/check-api.sh` | Passed; Vacuum quality 100/100 | `api/openapi.yaml` |
| `python3 scripts/check-architecture.py` | Passed | repository architecture boundaries |
| `./scripts/check-guardrails.sh --fast` | Passed generation, API, architecture and focused root checks | repository guardrails |
| `./scripts/check-guardrails.sh --ci` | Passed root/UI/tools generation, API, lint, architecture, tests, vet and module verification | repository guardrails |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -c -o /tmp/mastarr-qbittorrent-round8-amd64.test .` | Passed; static Linux amd64 test binary | `/tmp/mastarr-qbittorrent-round8-amd64.test` |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -c -o /tmp/mastarr-qbittorrent-round8-arm64.test .` | Passed; static Linux arm64 test binary | `/tmp/mastarr-qbittorrent-round8-arm64.test` |
| `git diff --check` | Passed | product commit |
| Product commit hook | Passed generation, Vacuum/API, architecture and focused root guardrails | `da5fa85f22d0c5374657d14cdc6a16ea09e0e038` |

## Review and integration

- Round-seven P1 addressed: a canceled or expired authentication leader no
  longer poisons an uncanceled waiter with the leader's context error.
- Active waiters elect one replacement flight after leader cancellation;
  canceled waiters remain caller-scoped and ordinary failed flights retain
  their immutable shared result.
- Retained corrections: explicit SID and generation request binding, stale
  `Set-Cookie` fencing, generation CAS, exact HTTP 200, strict duplicate-aware
  JSON and required-member validation, inventory hash identity checks, bounded
  and sanitized errors, safe timeout defaults, strict version/range semantics,
  endpoint path policy, unsupported-route classification, read-only surface
  and no root imports.
- Final independent review: pending against product
  `da5fa85f22d0c5374657d14cdc6a16ea09e0e038`.
- Integrated commit and execution-state update: pending coordinator action;
  this worker did not edit `docs/execution/state.json`.

## Resume checkpoint

- Product is ready at `da5fa85f22d0c5374657d14cdc6a16ea09e0e038`.
- Handoff is intentionally a separate commit and will follow the product
  commit.
- The standalone module has not been exercised against a live qBittorrent
  instance.
- Next safe action: independent review against the exact product SHA,
  followed by coordinator state recording.
