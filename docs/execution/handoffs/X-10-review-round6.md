# X-10 independent review, round six

## Decision

`changes_requested`.

The round-five delayed-response case closes when old reads return only a 401 or
403 and every request selects its cookie before a concurrent refresh. Three
held old-session reads then produce the initial login plus one refresh, all
three retry successfully through the fresh SID, and a later read performs no
third login.

The session generation is not bound to the complete HTTP session state,
however. A delayed old response can replace the fresh SID through the standard
cookie jar before the generation CAS runs. A refresh can also occur between the
client's generation snapshot and the cookie jar selecting the SID for the
request. Independent race-enabled probes reproduced both failures three times.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Reviewed product commit:
  `9202d4ccab811f0daccd089e2cce642b4805dc80`.
- Product parent:
  `982b98422a26bcd9fb97efb2b131a588cefa85cd`.
- Product tree:
  `8cb2c505ad7610f4220c9b6d223fd3b165cfffc5`.
- Reviewed handoff commit:
  `3db67877884b1197f24dfb44299fc0321947746a`.
- Coordinator state checkpoint:
  `43a0bf78df5bb81d05b72de01bc90138a8605620`.
- Round-five receipt:
  `7b51ffc0a4286d92768df1b7b29f667eb6c32d90`.
- Scope: `clients/qbittorrent/`, X-10, A-04/A-09/A-28/A-45, the
  round-six handoff, the round-five P1, and retained findings from rounds one
  through four.
- The product changes exactly `README.md`, `client.go` and `client_test.go`
  under the assigned qBittorrent module. It changes no generated output, root
  adapter, shared API, execution state, task, plan or script.
- Scoped product diff SHA-256:
  `644ece0b162e7e0ee7e00bd50de18f02ddd7916c0af0de3699c2b3f56e8ce4c5`.
- Scoped product archive SHA-256:
  `e647d946af150b4abe793ce0be85dde86ee83a445ce5547600eec6f06f6cb985`.
- Clean checks ran in a detached reviewer-owned worktree at the exact product
  commit. Independent probes were temporary and were removed before the clean
  worktree check.
- No live qBittorrent instance, credential, private coordinate, tracker,
  torrent, media payload, mutation, release or deployment was used.

## Findings

### P1: a delayed old response can replace the fresh SID before the CAS

- Location: `clients/qbittorrent/client.go:314-318`, `:611-645` and
  `:658-693`.
- Evidence: `New` installs a normal `cookiejar.Jar` on `http.Client`. Go applies
  response `Set-Cookie` headers to that jar as part of `Client.Do`, before
  `requestOnce` returns the response status to `getAfterSession`. The
  generation CAS protects only `authenticated`; it neither fences nor restores
  the SID stored by the jar.
- Independent reproduction:
  1. Complete the initial login with `SID=session-1`.
  2. Start two version reads and hold both after the server receives them with
     session 1.
  3. Return HTTP 403 to the first read. It refreshes once to
     `SID=session-2`, retries and succeeds.
  4. Return HTTP 403 to the second old read with an applicable
     `Set-Cookie: SID=session-1; Path=/` header.
  5. Let that read retry and then perform one later version read.
- Observed in three race-enabled repetitions:

  ```text
  stale read err=qBittorrent qbit.app.version failed (unauthorized, status 403);
  post-race err=<nil>; logins=3, want all success and 2 logins
  ```

  The delayed response restored the obsolete cookie before its generation-1
  rejection reached the CAS. Its retry used session 1 and failed. The next read
  then performed a redundant third login to recover.
- Failure mode: a response belonging to an obsolete request can overwrite the
  newer authenticated transport state. The client reports one read failure,
  retains authentication state inconsistent with the cookie jar, and performs
  another login even though the refresh had already succeeded.
- Contract: X-10 owns cookie login and request authentication. A-09 requires
  observations to remain bound to the selected connection. The round-five P1
  and round-six README promise that an old rejection cannot invalidate the
  newer session or trigger another login. Repository review policy treats
  false state and duplicated writes as blocking.
- Required change: make the SID part of the generation-owned session state.
  Do not allow SID updates from an obsolete or unauthenticated read response to
  change a newer session. An equivalent design may accept or install SID state
  only from the successful login that advances its generation.
- Required proof:
  - a delayed old 401 and 403 carrying an applicable old SID cannot replace the
    newer SID, fail its retry or cause a later login;
  - an expired/deletion SID cookie on the delayed response has the same fence;
  - ordinary current-generation rejection still performs exactly one refresh
    and retry;
  - oversized rejection bodies retain the same response/session fencing;
  - all shared-flight and cancellation tests remain green under `-race`.
- Disposition: `current_blocker`.

### P1: the recorded generation can differ from the SID actually sent

- Location: `clients/qbittorrent/client.go:621-645` and `:658-677`.
- Evidence: `get` snapshots `authGeneration` before it calls
  `getAfterSession`. The actual SID is selected later by `http.Client` from its
  cookie jar during `Do`. There is no lock, immutable request-session value or
  other binding across those operations.
- Independent reproduction:
  1. Complete the initial login with session 1.
  2. Start read A. A reviewer cookie-jar wrapper blocks its first cookie
     selection after `get` has recorded generation 1.
  3. Read B leaves with session 1, receives HTTP 403, refreshes to generation 2
     and session 2, retries and succeeds.
  4. Configure the server to reject session 2, then release A's cookie
     selection. A therefore sends session 2 while retaining generation 1.
  5. Observe A's HTTP 403 handling and login count.
- Observed in three race-enabled repetitions:

  ```text
  logins=2, want current session rejection to trigger a third login
  ```

  A's first request and its unauthenticated retry both failed with the typed
  HTTP 403 result. Because A carried generation 1, the CAS classified rejection
  of the actual current session 2 as stale and skipped the required refresh.
- Failure mode: the client can claim generation 2 remains authenticated after
  the server rejected the session-2 cookie actually used. The read fails and
  recovery is deferred to an unrelated later call. This is the inverse of the
  round-five race: the CAS preserves rejected current state because its token
  describes an earlier session.
- Contract: the round-six README says each read is bound to the authentication
  generation that sent it. X-10 owns cookie session handling and A-09 requires
  connection-bound evidence. The implementation does not yet materialize that
  stated request identity.
- Required change: atomically bind each read to both the generation and the
  concrete SID sent on the wire, or otherwise serialize session transitions
  with request cookie selection. The generation used by 401/403 invalidation
  must describe the session that the rejected request actually carried.
- Required proof:
  - force a successful refresh between a read's authentication check and its
    cookie selection; rejection of the newly selected SID must invalidate that
    current generation, perform one refresh and let the read retry once;
  - rejection of a request that actually carried the old SID must remain stale
    and cannot invalidate the newer generation;
  - concurrent reads around the same transition cannot produce a request whose
    generation and SID refer to different sessions;
  - race-enabled repetitions and the existing delayed-old-response matrix pass.
- Disposition: `current_blocker`.

## Round-five finding verification

| Round-five requirement | Independent result |
| --- | --- |
| Three delayed old-session 403 responses share one refresh | Closed for responses without session-cookie side effects and when the captured generation matches the cookie sent. The product test passed twenty normal repetitions and five race-enabled repetitions. |
| Exactly initial login plus one refresh; later read does not log in | Passed in the product's ordinary delayed-403 matrix. Failed when the second old response carried its obsolete SID: the old call failed and the later read caused login three. |
| Generation/CAS prevents stale invalidation | The boolean CAS works for its recorded generation, but the two P1 findings show that the generation does not own the cookie jar or reliably identify the SID on the request. |
| Oversized old 401/403 retains the generation guard | Passed an independent two-reader probe five times under `-race`: oversized old HTTP 403 bodies still produced one refresh, two successful retries and two total logins when no SID cookie was returned. |
| Current-generation rejection refreshes once | Passed existing sequential status/auth tests. The second P1 fails when the generation token and actual request SID differ. |
| Shared failed auth and canceled waiters remain correct | Passed repeated and race-enabled tests. Eight joined callers still share one immutable failed result, later retry remains possible, and canceled waiters return promptly. |

## Earlier finding regression verification

| Earlier finding | Independent result |
| --- | --- |
| Failed authentication repeated once per waiter | Closed. Joined callers share one immutable failed result; a later call can deliberately retry. |
| Canceled waiter blocked or successful Login ignored cancellation | Closed. Waiters honor their contexts and issue no read; canceled Login does not report success. |
| Missing or null required response members became zero evidence | Closed. Inventory, properties, file and category objects enforce every required non-null member while valid zero values pass. |
| Duplicate logical torrent identities survived | Closed. Exact and case-equivalent v1/v2 identities reject the complete response. |
| Undocumented 2xx became success | Closed. Login and reads accept exact HTTP 200 only. |
| Duplicate, case-folded or invalid-UTF-8 JSON members were accepted | Closed. Strict duplicate-aware decoding rejects those forms, unknown members and trailing values. |
| Invalid inventory hashes became records | Closed. Each record requires a nonempty 40/64-character hexadecimal identity. |
| Nonpositive injected timeout removed the deadline | Closed. Nonpositive values use 15 seconds; positive values remain unchanged. |
| Case-equivalent request hashes bypassed duplicate checks | Closed. Supported hexadecimal identities are compared case-insensitively. |
| Login accepted missing or unusable SID cookies | Closed for login responses. Missing, empty, wrong-name, wrong-path, wrong-domain and Secure-over-HTTP SID cookies fail. Read-response SID ownership is the first P1 above. |
| Hash delimiter, whitespace, control and aggregate bounds were unsafe | Closed. Exact aggregate maximum passes and one-over, duplicates, pipe, Unicode whitespace, control and invalid UTF-8 fail. |
| Oversized errors lost status or one-reauth behavior | Closed for status classification and body bounds. Cookie/session ownership remains subject to the two findings above. |
| Version, piece-range and unsupported semantics were loose | Closed. Versions and piece ranges are strict; unsupported statuses remain distinct. |
| Handwritten/OpenAPI bounds drifted | Closed. Exact query/credential maxima pass; missing credentials and one-over values fail. |
| Ambiguous endpoint paths were accepted | Closed. Literal root/prefix paths are stable; dot, encoded, repeated-separator and backslash forms fail. |

## Other safeguards that passed

- Pinned oapi-codegen v2.8.0 output reproduces byte-for-byte from the unchanged
  module OpenAPI.
- The handwritten public API exposes no generated DTO type.
- The OpenAPI contains seven GET reads and the sole login POST. It has no
  mutation route or security scheme. Its response objects keep all declared
  members required and non-null.
- No root/domain/port/storage/workflow/adapter import or local replace directive
  exists in the standalone module.
- Exact status handling, redirects, Origin/Referer, endpoint paths, body and
  item bounds, typed sanitized errors and synthetic fixtures remain intact.
- The scoped public safety scan found no credential, private address, tracker,
  passkey or user-specific runtime path.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/parent/tree/handoff/state, task/spec/acceptance and three-file scope | Inspected directly; identities above are exact. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` in `clients/qbittorrent` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed. |
| Retained auth/status/identity/required-response tests, twenty repetitions | Passed. |
| Same focused matrix under `-race`, five repetitions | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `GOWORK=off go mod verify` | Passed: all modules verified. |
| `GOWORK=off go mod tidy -diff` | Passed with no diff. |
| `./check-generation.sh` | Passed; pinned generated output reproduced. |
| Module-local Vacuum lint with `../../api/vacuum.yaml` | Passed, quality 100/100. |
| `python3 scripts/check_planning.py` | Passed: 43 tasks, 60 acceptance cases and local links. |
| `./scripts/check-api.sh` | Passed. |
| `python3 scripts/check-architecture.py` and `./scripts/check-lint.sh` | Passed. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed root/UI/tools generation, API, lint, architecture, tests, vet and module verification. |
| Linux amd64/arm64 CGO-free module test compile | Passed; both outputs are statically linked Linux ELF executables. |
| OpenAPI structural parser | Passed: eight operations, login-only POST, closed required response objects and no security scheme. |
| Root-import, generated-type, local-replace and mutation-route scans | Passed. |
| Public credential/private-coordinate scan | Passed. |
| Scoped diff/archive hashes, `git diff --check` and clean reviewer worktree | Passed before receipt creation. |
| Independent ordinary oversized-stale-403 probe under `-race`, five repetitions | Passed with one refresh and successful reads. |
| Independent delayed old-response SID replacement probe under `-race`, three repetitions | Failed consistently: stale read returned typed 403, later read recovered, total logins were three. |
| Independent generation-to-wire-SID binding probe under `-race`, three repetitions | Failed consistently: rejection of SID 2 was labeled generation 1, the read failed and login count remained two instead of refreshing to three. |

## Acceptance contribution

- A-04: accepted for X-10. Supported torrent identities and whole-response
  duplicate handling remain exact and scoped to this client result.
- A-09: not accepted for X-10. A delayed response can replace the current SID,
  and the generation used for invalidation can differ from the SID actually
  sent.
- A-28: accepted for this read-only X-10 contribution. State, progress,
  completion and seeding values remain preserved and bounded; no control route
  exists in this module.
- A-45: generation and all standard module/repository gates pass. Runtime
  session-generation behavior remains blocked by the two P1 findings.

## Reviewer decision

`changes_requested`. Bind generation to the concrete SID used by each request
and fence SID updates from delayed read responses, then re-run both adversarial
session-transition probes with the retained auth, cancellation, status,
generation and repository gates.
