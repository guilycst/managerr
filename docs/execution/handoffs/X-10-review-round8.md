# X-10 independent review, round eight

## Decision

`changes_requested`.

The round-seven canceled-leader failure closes for authentication attempts that
end because the leader's context is canceled. The leader returns its own
context error, one active waiter elects one replacement login, other active
waiters join it, and their reads succeed. An independent eight-waiter probe
passed three times under `-race`.

One shared-flight P1 remains at the cancellation boundary. If an upstream
failure has already completed when the leader context becomes canceled, the
implementation overwrites that completed result with the leader's context
error and tells waiters to repeat authentication. A deterministic completed
HTTP 403 probe observed two login POSTs instead of one in three race-enabled
runs.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Reviewed product commit:
  `da5fa85f22d0c5374657d14cdc6a16ea09e0e038`.
- Product parent:
  `bcd5a926cc4b82cf2f3f8bc22655043bd0937b0b`.
- Product tree:
  `3d57b22935ec70d4b9f51cb11e78cff14c3bae18`.
- Reviewed handoff commit:
  `da37079df3cf041fcfab58228723b50e14d2d8d4`.
- Coordinator state checkpoint:
  `f61657544c09da617f4ece9ca23f905c0daff882`.
- Round-seven receipt:
  `e26ba78bbb75497fdea13cd0fb2b853d12c17c38`.
- Scope: `clients/qbittorrent/`, X-10, A-04/A-09/A-28/A-45, the
  round-seven P1 and all retained findings.
- The product changes exactly `README.md`, `client.go` and `client_test.go`
  under the assigned standalone module. It changes no generated output, root
  adapter, shared API, execution state, task, plan or script.
- Scoped product diff SHA-256:
  `18a6f2291c657142e2b28f95818605e218c5d7e88165f5a63db6463df682caf8`.
- Scoped product archive SHA-256:
  `71d6c78b862982c81469e0996c08404739d79d24b9fd12adcecb07c8d613265a`.
- Clean checks ran in a detached reviewer-owned worktree at the exact product
  commit. Independent probes were temporary and removed before the clean
  worktree check.
- No live qBittorrent instance, credential, private coordinate, tracker,
  torrent, media payload, mutation, release or deployment was used.

## Finding

### P1: leader cancellation masks a completed upstream failure and repeats login

- Location: `clients/qbittorrent/client.go:547-605`.
- Evidence: `authenticate` returns `authErr`, but any concurrent leader context
  error unconditionally replaces it at lines 567-573. The flight stores only
  the replacement context error and `leaderCanceled=true`. Every active waiter
  then continues at lines 593-601 and elects another login, even when
  `authErr` already contains a complete typed HTTP response that should be the
  immutable shared result.
- Independent reproduction:
  1. Start a login leader with a cancelable context and hold its synthetic
     `RoundTrip` until a live background waiter joins the flight.
  2. At release, cancel the leader and return a complete HTTP 403 response from
     the same `RoundTrip`.
  3. A separate direct boundary probe verifies that `requestOnce` receives
     status 403 with no transport error in this ordering.
  4. Return HTTP 403 from any later login and observe the leader result, waiter
     result and request count.
- Observed in three race-enabled repetitions:

  ```text
  login attempts=2, want completed 403 shared from one request
  ```

  The leader correctly returned `context.Canceled`. The active waiter
  eventually received a typed unauthorized result, but only after it discarded
  the first completed 403 and issued a duplicate login POST.
- Failure mode: a completed upstream fact is lost because one caller's context
  state wins the flight's single result slot. Concurrent callers repeat a known
  failed login, increasing authentication traffic and ban/rate-limit exposure.
  The shared flight no longer represents the upstream attempt that all callers
  joined.
- Contract: X-10 owns request authentication, deadlines and typed upstream
  errors. The retained round-four and round-five correction requires one
  immutable failed result for callers joining the same completed attempt. The
  round-seven receipt explicitly requires one completed upstream 403 to remain
  one shared failure even around leader cancellation.
- Required change: retain the authentication attempt's result separately from
  the leader's return error. The leader may return its own context error, while
  active waiters receive a completed typed upstream failure from that attempt.
  Elect a replacement flight only when authentication itself ended because the
  leader context canceled or expired before a usable upstream outcome existed.
- Required proof:
  - a completed HTTP 403 concurrent with leader cancellation produces one POST,
    the leader's context error and the same typed unauthorized result for one
    and many active waiters;
  - representative completed 429/5xx and malformed HTTP 200 failures preserve
    their typed shared results at the same boundary;
  - a transport request actually terminated by leader cancellation still lets
    one active waiter elect exactly one replacement login;
  - leader deadline expiry covers both no-response and completed-response
    orderings;
  - canceled waiters remain prompt, a normal shared 403 remains one result, and
    successful/retry/session-generation tests remain green under `-race`.
- Disposition: `current_blocker`.

## Round-seven finding verification

| Round-seven requirement | Independent result |
| --- | --- |
| Canceled leader returns its own error | Closed. The product regression and independent probe observed `context.Canceled` for the leader. |
| One active waiter does not inherit leader cancellation | Closed when the authentication request itself ends through cancellation. The product waiter elected a replacement login and completed one version read. |
| Many active waiters share one replacement | Closed. Eight background read waiters joined the canceled leader, elected exactly one second login, and completed eight reads in three race-enabled repetitions. |
| Canceled waiters remain caller-scoped | Closed. One and eight canceled waiters return promptly without issuing a read while the active leader continues. |
| Normal upstream failure remains one immutable result | Closed when the leader context remains active. Eight callers share one typed HTTP 403 and only a later independent call retries. The cancellation boundary exposes the P1 above. |
| Leader cancellation concurrent with completed upstream failure | Open. The completed 403 was overwritten by the leader context marker and repeated. |

## Retained session and contract verification

| Safeguard | Independent result |
| --- | --- |
| SID and generation request binding | Passed. Both values remain one immutable credential selected under the auth lock and the SID is installed in the request before dispatch. |
| Delayed old response cookie cannot replace fresh SID | Passed. The client owns no automatic cookie jar and never persists read-response cookies. |
| Stale rejection cannot revoke a newer session | Passed. Three delayed old HTTP 403 responses retry through one fresh SID with exactly initial login plus one refresh. |
| Current-generation rejection | Passed. It invalidates only its generation, performs one shared refresh and retries once. |
| Login SID validation | Passed. Missing, empty, wrong-name, wrong-path, wrong-domain, expired/deletion and Secure-over-HTTP cookies fail; unsafe header values cannot become credentials. |
| Exact status and bounded errors | Passed. Exact HTTP 200 is required; oversized 401/403/429/5xx retain sanitized typed classification and bounded one-reauth behavior. |
| Strict JSON and required response members | Passed. Duplicate/case-folded/invalid-UTF-8/unknown/trailing data and missing/null required values fail while valid zero values pass. |
| Torrent identity and input bounds | Passed. Valid v1/v2 hashes, duplicate logical identity rejection, case-insensitive query duplicates, delimiter/control/aggregate limits and exact maxima remain enforced. |
| Versions, piece ranges and unsupported outcomes | Passed. Shapes and ranges remain strict, and unsupported status remains distinct from unavailable. |
| Endpoint, redirect and request security | Passed. Origin/Referer, literal endpoint prefixes, redirect refusal, isolated credentials and timeout defaults remain intact. |
| Read-only and module boundaries | Passed. Seven GETs plus login POST; no mutation route, root import, local replace or generated DTO leak. |

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/parent/tree/handoff/state, task/spec/acceptance and three-file scope | Inspected directly; identities above are exact. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` in `clients/qbittorrent` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed. |
| Retained cancellation/auth/session/status/identity/response matrix, twenty repetitions | Passed. |
| Same focused matrix under `-race`, five repetitions | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `GOWORK=off go mod verify` | Passed: all modules verified. |
| `GOWORK=off go mod tidy -diff` | Passed with no diff. |
| `GOWORK=off go generate ./...` and `./check-generation.sh` | Passed; pinned output remained clean and reproducible. |
| Module-local Vacuum lint with `../../api/vacuum.yaml` | Passed, quality 100/100. |
| `python3 scripts/check_planning.py` | Passed: 43 tasks, 60 acceptance cases and local links. |
| `./scripts/check-api.sh` | Passed. |
| `python3 scripts/check-architecture.py` and `./scripts/check-lint.sh` | Passed. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed root/UI/tools generation, API, lint, architecture, tests, vet and module verification. |
| Linux amd64/arm64 CGO-free module test compile | Passed; both outputs are statically linked Linux ELF executables. |
| OpenAPI structural parser | Passed: eight operations, login-only POST, closed required response objects and no security scheme. |
| Root-import, generated-type, local-replace, mutation-route and automatic-cookie scans | Passed. |
| Public credential/private-coordinate scan | Passed. |
| Scoped diff/archive hashes, `git diff --check` and clean reviewer worktree | Passed before receipt creation. |
| Independent canceled-leader/eight-live-waiter probe under `-race`, three repetitions | Passed: one replacement login and eight successful reads. |
| Independent completed-403 transport boundary probe under `-race`, three repetitions | Passed: `requestOnce` received a complete HTTP 403 with no transport error while the leader context became canceled. |
| Independent completed-403 shared-flight probe under `-race`, three repetitions | Failed consistently: two login POSTs were issued where the first completed 403 should have been shared. |

## Acceptance contribution

- A-04: accepted for X-10. Supported torrent identities and whole-response
  duplicate handling remain exact and scoped to this client result.
- A-09: accepted for X-10's identity/session contribution. The selected SID
  and generation remain one immutable connection-local request value.
- A-28: accepted for this read-only X-10 contribution. State, progress,
  completion and seeding values remain preserved and bounded; no control route
  exists in this module.
- A-45: generation and all standard module/repository gates pass. X-10 remains
  blocked because a completed upstream authentication result can be discarded
  and repeated at the leader-cancellation boundary.

## Reviewer decision

`changes_requested`. Separate the leader's caller-specific return error from
the completed flight outcome, then prove waiters retry only a genuinely
canceled attempt and never repeat a completed upstream failure.
