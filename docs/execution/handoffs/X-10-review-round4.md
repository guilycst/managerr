# X-10 independent review, round four

## Decision

`changes_requested`.

All three round-three findings close under independent race-enabled probes.
Required and non-null response members are enforced, canceled authentication
waiters return before the active login is released, and duplicate logical
inventory identities reject the whole response. All findings retained from
rounds one and two also remain closed.

One new P1 finding remains. Concurrent callers share only an authentication
completion channel, not the leader's result. When the active login fails, every
waiting caller becomes the next leader and repeats the same login POST. Eight
callers queued behind one HTTP 403 produced eight sequential authentication
attempts.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Reviewed correction commit:
  `1a9eb570fa434668c5bfff11b0d2f54180ad1a49`.
- Product parent:
  `f21d7c16d7453e78a4cd7be0e17ee030709c497f`.
- Product tree:
  `917bf6a1dcac723a0aac61e10d53b69571a66dab`.
- Reviewed correction handoff commit:
  `c38583733f6a194e27640f496c466b2a8063f4f7`.
- Coordinator state commit inspected:
  `3cd41dc60987414ce2a2bddec564b31754085cd3`.
- Round-three receipt:
  `a01fbbe294a4014483a78a0c64604c2870ce6228`.
- Review scope: `clients/qbittorrent/`, X-10, A-04/A-09/A-28/A-45,
  the round-four handoff, the three round-three findings, and all retained
  findings from rounds one and two.
- The product commit changes exactly three assigned qBittorrent files:
  `README.md`, `client.go` and `client_test.go`. It changes no generated
  output, root adapter, shared API, execution state, task, plan or script.
- Scoped product diff SHA-256:
  `18e373d40b60f273d6a22377d4868901c1a0c1fdf9b4eb5189fbe9b9e9491516`.
- Scoped product archive SHA-256:
  `f621133f8479eafc7cfac77f67276053f693589e0ce14ccfe2458eab1213b12d`.
- Clean checks ran in a detached reviewer-owned worktree at the exact product
  commit. Independent probes ran in a disposable Git archive. The probe file
  is not product evidence and was not committed.
- No live qBittorrent instance, credential, tracker coordinate, media payload,
  torrent mutation, filesystem action, release or deployment was used.

## Finding

### P1: concurrent authentication failures are repeated once per waiting caller

- Location: `clients/qbittorrent/client.go:528-576`.
- Contract location: `clients/qbittorrent/README.md:49-50` states that
  concurrent authentication is single-flight. The X-10 deliverable keeps
  authentication inside this module, and its OpenAPI at
  `clients/qbittorrent/openapi.yaml:426-438` models rejected authentication
  and the WebUI IP-ban outcome.
- Evidence: `authInFlight` is a `chan struct{}`. A leader closes it after
  `authenticate` returns, but the error is not retained or delivered to
  callers already waiting on that flight. Each waiter wakes, sees
  `authenticated == false`, installs a new channel, and performs another
  login POST.
- Independent reproduction:
  1. Start eight concurrent `Login(context.Background())` calls against one
     synthetic client.
  2. Block the first login handler until all callers are queued behind the
     active flight.
  3. Release the handler and return HTTP 403 for every login.
  4. Count POSTs and check each typed result.
- Observed result in three race-enabled repetitions:

  ```text
  concurrent failed login attempts=8, want one shared attempt
  concurrent failed login attempts=8, want one shared attempt
  concurrent failed login attempts=8, want one shared attempt
  ```

  Every caller received typed `unauthorized`, but the module issued eight
  upstream POSTs.
- Failure mode: one concurrent credential, permission or upstream failure is
  amplified by the number of waiting reads. qBittorrent can treat repeated
  failures as an IP-ban condition, which the module contract already exposes
  as HTTP 403. The repeated POSTs also contradict the public single-flight
  guarantee and the repository's duplicate-write review gate.
- Contract: X-10 owns cookie login and request authentication; A-09 exercises
  per-connection authenticated inventory; A-45 requires the handwritten
  behavior to match its documented contract. The implementation plan classifies
  duplicated writes as critical review findings.
- Required change: let callers that joined one in-flight authentication attempt
  observe that attempt's non-context result without becoming new leaders.
  Preserve prompt caller-specific cancellation and allow a distinct call made
  after the failed flight has drained to begin a deliberate retry. Do not cache
  an authentication failure indefinitely.
- Required proof:
  - one blocked flight with one and many waiters returns one shared HTTP 403,
    HTTP 200 failure-body, rate-limit and transport failure using exactly one
    POST per concurrent batch;
  - all uncanceled waiters receive the same sanitized typed result;
  - a later independent call can start exactly one new attempt and succeed;
  - a canceled waiter still returns its own context error before leader release;
  - leader cancellation does not deadlock uncanceled waiters;
  - successful concurrent login, expired-session reauthentication and race
    tests remain green.
- Disposition: `current_blocker`.

## Round-three finding verification

| Round-three finding | Independent result |
| --- | --- |
| Missing required members became zero-valued evidence | Closed. Inventory, properties, file and category objects reject missing required fields. Null top-level, array-element, required-member and category-map values fail. Complete fixtures with legitimate zero and false values pass. |
| Canceled authentication waiter blocked and returned success | Closed for the reported case. A canceled waiter returns `context.Canceled` before the active login is released, issues no read and cannot take the authenticated success path. Failed uncanceled waiters expose the new P1 above. |
| Duplicate logical inventory identities survived | Closed. Exact and case-equivalent 40- and 64-character hash duplicates reject the whole observation; distinct v1/v2 identities pass with original spelling retained. |

## Earlier finding regression verification

| Earlier finding | Independent result |
| --- | --- |
| Undocumented 2xx became success | Closed. Login and reads accept exact HTTP 200 only; 201/202/204/206 are typed unsupported before decoding. |
| JSON selected duplicate, case-folded or invalid-UTF-8 fields | Closed. Duplicate and escape-equivalent members, mixed-case known names, raw invalid UTF-8, unknown fields and trailing values fail. |
| Invalid inventory hashes became records | Closed. Each record requires one supported 40/64 hexadecimal identity; any malformed record rejects the whole response. |
| Negative timeout removed the deadline | Closed. Nonpositive injected timeouts use 15 seconds and positive values are retained. |
| Case-equivalent request hashes bypassed duplicate checks | Closed. Supported hex identities are compared case-insensitively for request duplicate detection. |
| Login accepted unusable SID state | Closed. Missing, empty, wrong-name, wrong-path, wrong-domain and Secure-over-HTTP cookies fail; applicable cookies pass. |
| Hash delimiter and aggregate scope were unsafe | Closed. Empty, pipe, Unicode whitespace, controls, invalid UTF-8 and duplicates fail; exact aggregate maximum passes and one-over fails. |
| Oversized error bodies lost status or auth recovery | Closed. Small and oversized 401/403 reauthenticate once; 429 and 5xx preserve sanitized code/status/retryability. |
| Arbitrary version text passed | Closed. Supported token shapes pass; whitespace, multiline, controls, HTML, trailing tokens and over-bound values fail. |
| Piece-range semantics were not validated | Closed. Exactly two ordered nonnegative indices pass; negative, reversed and wrong-size arrays fail. |
| Unsupported outcome was unreachable | Closed. Version 404/405/501 and undocumented 2xx statuses are unsupported; resource 404 remains unavailable. |
| Handwritten/OpenAPI input bounds drifted | Closed. Query and credential exact maxima pass; missing credentials and one-over values fail. |
| Ambiguous endpoint paths were accepted | Closed. Literal root/prefix paths produce exact request URIs; dot, encoded, repeated-separator and backslash forms fail. |

## Other safeguards that passed

- oapi-codegen v2.8.0 output reproduces byte-for-byte from the unchanged module
  OpenAPI.
- The public handwritten API exposes no generated DTO type.
- The OpenAPI has seven GET reads and the sole login POST. It has no mutation
  route or security scheme.
- No Mastarr root/domain/port/storage/workflow/adapter import or local replace
  directive exists in the standalone module.
- Redirect refusal, Origin/Referer, cookie applicability, isolated cookie jar,
  response bounds, exact status handling and sanitized errors remain intact.
- All fixtures and examples are synthetic. The public safety scan found no
  credential, private address, private tracker or user-specific runtime path.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/parent/tree/handoff/state and three-file scoped correction | Inspected; identities above are exact. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` in `clients/qbittorrent` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `GOWORK=off go mod verify` | Passed: all modules verified. |
| `GOWORK=off go mod tidy -diff` | Passed with no diff. |
| `./check-generation.sh` | Passed; pinned generated output reproduced. |
| Module-local Vacuum lint with `../../api/vacuum.yaml` | Passed, quality 100/100. |
| `python3 scripts/check_planning.py` | Passed: 43 tasks, 60 acceptance cases and local links. |
| `./scripts/check-api.sh` | Passed. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed root/UI/tools generation, API, lint, architecture, tests, vet and module verification. |
| Linux amd64/arm64 CGO-free module test compile | Passed; both outputs are statically linked Linux ELF executables. |
| OpenAPI structural parser | Passed: eight operations, seven GETs plus login POST, closed fully-required response objects and no security scheme. |
| Root-import, generated-type, local-replace and mutation-route scans | Passed. |
| Public credential/private-coordinate scan | Passed; only synthetic `.invalid` fixture values exist. |
| Scoped diff/archive hashes, `git diff --check` and clean reviewer worktree | Passed before receipt creation. |
| Product correction tests repeated ten times and focused race matrix | Passed. |
| Independent required/null and duplicate-identity probes under race, repeated three times | Passed. |
| Independent canceled-waiter probe under race, repeated three times | Passed before active login release. |
| Independent concurrent failed-login probe under race, repeated three times | Failed contract consistently with eight login POSTs per eight callers. |

## Acceptance contribution

- A-04: X-10's response identity contribution passes. Supported hashes are
  exact and duplicate logical identities reject the whole observation.
- A-09: not accepted for X-10. Successful session scoping and caller
  cancellation pass, but one failed authentication flight becomes one POST per
  waiter and can worsen the connection's authentication state.
- A-28: X-10's read-only progress/state/seeding contribution passes for the
  frozen response boundary; missing or malformed evidence is rejected.
- A-45: generation and standard module/repository gates pass, but runtime
  failed-auth concurrency contradicts the module's single-flight contract.

## Reviewer decision

`changes_requested`. Share each in-flight authentication result with callers
that joined that flight, while preserving caller-specific cancellation and
later deliberate retry. Add the required failure-batch regressions before
another independent review.
