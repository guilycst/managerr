# X-10 independent review, round seven

## Decision

`changes_requested`.

Both round-six session-binding P1s close. The client owns an immutable SID and
generation credential, sets that SID on the request before transport dispatch,
and does not install an HTTP cookie jar. Delayed old `Set-Cookie` headers cannot
replace the fresh SID. A transport-ordering probe also proved that a read keeps
session 1 with generation 1 while a concurrent read refreshes to session 2.

One cancellation P1 remains. Authentication uses the context of whichever
caller becomes the flight leader, then publishes that caller's cancellation as
the shared flight result. An uncanceled waiter therefore receives
`context.Canceled` from another caller instead of starting a new authentication
attempt. The failure reproduced three times under `-race`.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Reviewed product commit:
  `a16e4b3713b75c4321ab7ff37c5de4ad64ab83d6`.
- Product parent:
  `84a50fa8a458a9550c231d1fcae3f0de9bb7faf1`.
- Product tree:
  `5f13c80f258fdcca90b1175bcf4e4d009555bba1`.
- Reviewed handoff commit:
  `c6f9dfa8bc639997420d2d7a58767a26226c5bc0`.
- Coordinator state checkpoint:
  `83b1ab78deafdb0da9624e7a0f23d5580bbef908`.
- Round-six receipt:
  `ef0b917061ebfdaf246d9190438dcf3febec7d1b`.
- Scope: `clients/qbittorrent/`, X-10, A-04/A-09/A-28/A-45, both
  round-six P1s and all retained findings.
- The product changes exactly `README.md`, `client.go` and `client_test.go`
  under the assigned standalone module. It changes no generated output, root
  adapter, shared API, execution state, task, plan or script.
- Scoped product diff SHA-256:
  `beff712d110cc16df7e29df8a6e4afa49038e2ecab3476f544c95c3c5a60e208`.
- Scoped product archive SHA-256:
  `df856dad70c92880af6b1abaf0421381b09510e48e0b915b5e845ab589e20135`.
- Clean checks ran in a detached reviewer-owned worktree at the exact product
  commit. Independent probes were temporary and removed before the clean
  worktree check.
- No live qBittorrent instance, credential, private coordinate, tracker,
  torrent, media payload, mutation, release or deployment was used.

## Finding

### P1: a canceled authentication leader cancels unrelated active waiters

- Location: `clients/qbittorrent/client.go:545-597`.
- Evidence: the first caller executes `authenticate(ctx)` with its own context
  at line 563. Its resulting `context.Canceled` or
  `context.DeadlineExceeded` is stored in `flight.err` at line 574. A waiter
  whose own context remains active returns that foreign error directly at
  lines 588-595. The waiter neither retries nor becomes the next flight leader.
- Independent reproduction:
  1. Start `Login` with a cancelable context and hold its first login request
     at the synthetic server.
  2. Start `ApplicationVersion` with `context.Background`; it joins the active
     authentication flight.
  3. Cancel only the leader's context while the first request remains held.
  4. Observe both caller results and the number of login requests.
- Observed in three race-enabled repetitions:

  ```text
  active waiter inherited leader cancellation: context canceled
  ```

  The leader correctly returned `context.Canceled`. The uncanceled read also
  returned that error, only one login POST occurred, and no read request was
  issued. A second authentication attempt was available and would have
  succeeded, but the active waiter never elected it.
- Failure mode: cancellation belongs to one API call, yet it aborts another
  caller that still has time and wants the connection. The second caller is
  falsely reported as canceled and loses the opportunity to complete its
  read. This can propagate one workflow's explicit cancellation into unrelated
  reconciliation work sharing the client.
- Contract: X-10 owns request authentication and deadlines. The connector
  contract requires context deadlines and typed errors, and the prior
  cancellation correction established caller-selectable cancellation. An
  uncanceled caller must not receive another caller's context state.
- Required change: keep cancellation caller-scoped. Once a flight whose leader
  was canceled has drained, an uncanceled waiter must continue through a new
  single authentication flight, or the shared operation must have a lifetime
  independent of any one caller while still letting each canceled caller
  return promptly. Preserve one active login POST at a time and do not turn a
  canceled leader into success.
- Required proof:
  - one canceled leader plus one uncanceled `Login` waiter yields the leader's
    cancellation and a successful waiter through exactly one later login;
  - one canceled leader plus many uncanceled read waiters starts one later
    shared login and lets every active waiter read successfully;
  - leader deadline expiry behaves the same as explicit cancellation;
  - canceled waiters behind an active leader still return promptly and never
    issue a read;
  - one upstream 403 remains one immutable shared failure for all callers that
    joined that completed upstream attempt;
  - successful concurrency, expired-session reauthentication and session
    credential tests remain green under `-race`.
- Disposition: `current_blocker`.

## Round-six finding verification

| Round-six finding or required proof | Independent result |
| --- | --- |
| Delayed old response replaced the fresh SID through `http.CookieJar` | Closed. `New` removes the inherited jar and stores no automatic jar. Login extracts one validated SID into client-owned state; read-response cookies are never installed. |
| Old 401/403 with obsolete `Set-Cookie` caused read failure and third login | Closed. Two held session-1 reads, each returning HTTP 403 plus `Set-Cookie: SID=session-1`, both retried through session 2; a later read succeeded and total login count stayed two in three race-enabled repetitions. |
| Generation snapshot differed from the SID selected later by the jar | Closed. SID and generation are copied together under `authMu`; the SID is then placed directly in the request header before transport dispatch. |
| Refresh between credential selection and request dispatch | Closed. A reviewer transport held read A after its Cookie header was set. Read B refreshed to session 2. The held A request still carried session 1, its rejection stayed stale, and its bounded retry used session 2. The probe passed three times under `-race`. |
| Current-credential rejection refreshes the matching generation | Closed. After the stale transition, rejection of session 2 performed one login and retried through session 3. The independent probe observed the required final total of three login POSTs. |
| Old deletion/expiry response cookie cannot alter active state | Closed by construction for reads because no response cookie is persisted. Login deletion cookies are rejected explicitly. |
| Oversized 401/403 keeps status and session semantics | Closed. Existing bounded-status tests and the prior independent oversized-stale probe remain consistent with status-first handling; this correction does not change response bounds. |

## Earlier finding regression verification

| Earlier finding | Independent result |
| --- | --- |
| Failed authentication repeated once per waiter | Closed for upstream failures. Joined callers share one immutable 403 result and a later independent call can retry. Caller-specific leader cancellation is the new P1 above. |
| Canceled waiter blocked or returned success | Closed. A waiter can select its own context while another login remains active. The inverse leader-cancellation case is the new P1. |
| Missing or null required response members became zero evidence | Closed. Inventory, properties, file and category objects enforce every required non-null member while valid zero values pass. |
| Duplicate logical torrent identities survived | Closed. Exact and case-equivalent v1/v2 identities reject the complete response. |
| Undocumented 2xx became success | Closed. Login and reads accept exact HTTP 200 only. |
| Duplicate, case-folded or invalid-UTF-8 JSON members were accepted | Closed. Strict duplicate-aware decoding rejects those forms, unknown members and trailing values. |
| Invalid inventory hashes became records | Closed. Each record requires a nonempty 40/64-character hexadecimal identity. |
| Nonpositive injected timeout removed the deadline | Closed. Nonpositive values use 15 seconds; positive values remain unchanged. |
| Case-equivalent request hashes bypassed duplicate checks | Closed. Supported hexadecimal identities are compared case-insensitively. |
| Login accepted missing or unusable SID cookies | Closed. Missing, empty, wrong-name, wrong-path, wrong-domain, expired/deletion and Secure-over-HTTP SID cookies fail. Unsafe Cookie-header values cannot become a session. |
| Hash delimiter, whitespace, control and aggregate bounds were unsafe | Closed. Exact aggregate maximum passes and one-over, duplicates, pipe, Unicode whitespace, control and invalid UTF-8 fail. |
| Oversized errors lost status or one-reauth behavior | Closed. Status classification and body bounds remain intact. |
| Version, piece-range and unsupported semantics were loose | Closed. Versions and piece ranges are strict; unsupported statuses remain distinct. |
| Handwritten/OpenAPI bounds drifted | Closed. Exact query/credential maxima pass; missing credentials and one-over values fail. |
| Ambiguous endpoint paths were accepted | Closed. Literal root/prefix paths are stable; dot, encoded, repeated-separator and backslash forms fail. |

## Other safeguards that passed

- Pinned oapi-codegen v2.8.0 output reproduces byte-for-byte from the unchanged
  module OpenAPI. `go generate` leaves the worktree clean.
- The handwritten public API exposes no generated DTO type.
- The OpenAPI contains seven GET reads and the sole login POST. It has no
  mutation route or security scheme. Fixed response objects keep all declared
  members required and non-null.
- No root/domain/port/storage/workflow/adapter import or local replace directive
  exists in the standalone module.
- The configured HTTP client is cloned, its cookie jar is cleared, and the
  existing redirect policy remains replaced with refusal. Origin/Referer,
  endpoint paths, status handling, body/item bounds and sanitized errors remain
  intact.
- The scoped public safety scan found no credential, private address, tracker,
  passkey or user-specific runtime path.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/parent/tree/handoff/state, task/spec/acceptance and three-file scope | Inspected directly; identities above are exact. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` in `clients/qbittorrent` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed. |
| Retained auth/session/status/identity/required-response matrix, twenty repetitions | Passed. |
| Same focused matrix under `-race`, five repetitions | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `GOWORK=off go mod verify` | Passed: all modules verified. |
| `GOWORK=off go mod tidy -diff` | Passed with no diff. |
| `GOWORK=off go generate ./...` and `./check-generation.sh` | Passed; generated output remained clean and reproducible. |
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
| Independent delayed old-response SID probe under `-race`, three repetitions | Passed with successful stale retries, two total logins and no cookie rollback. |
| Independent generation-to-wire-SID ordering probe under `-race`, three repetitions | Passed; held request kept SID 1, stale retry used SID 2, and later current rejection refreshed to SID 3. |
| Independent canceled-leader/active-waiter probe under `-race`, three repetitions | Failed consistently: the active waiter inherited `context.Canceled`, no read ran and no second login was attempted. |

## Acceptance contribution

- A-04: accepted for X-10. Supported torrent identities and whole-response
  duplicate handling remain exact and scoped to this client result.
- A-09: accepted for X-10's identity/session contribution. The selected SID
  and generation now remain one immutable, connection-local request value.
- A-28: accepted for this read-only X-10 contribution. State, progress,
  completion and seeding values remain preserved and bounded; no control route
  exists in this module.
- A-45: generation and all standard module/repository gates pass. X-10 remains
  blocked because caller-specific cancellation is not preserved by the shared
  authentication runtime.

## Reviewer decision

`changes_requested`. Prevent one authentication leader's cancellation or
deadline from becoming an uncanceled waiter's result, then re-run the leader
and waiter matrix with the retained session, authentication, generation and
repository gates.
