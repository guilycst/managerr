# X-10 independent review, round five

## Decision

`changes_requested`.

The round-four P1 closes under an independent race-enabled probe. Eight callers
that join one failed authentication flight receive the same typed HTTP 403
result from exactly one login POST. Canceled waiters remain prompt, a later
independent call can retry, and all earlier response, identity, status, bound,
endpoint and generation findings remain closed.

One new P1 remains in expired-session concurrency. Two reads can leave under
the old session, the first HTTP 403 can complete a successful refresh, and the
second delayed HTTP 403 then invalidates that newer session and performs a
second refresh. The deterministic probe observed three login POSTs where only
the initial login and one refresh were valid.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Reviewed correction commit:
  `797822ff86bbe9882354e9f1e395e5408c527d03`.
- Product parent:
  `ed8a9b63239be8b46163b8fb107284e4f71b6c3d`.
- Product tree:
  `a6cb4b56760fb49bc5608a9a0cee31ada3897ac0`.
- Reviewed correction handoff commit:
  `58b5f71b9f7b600a8158451b367ee5c2712d4533`.
- Coordinator state commit inspected:
  `3fcf0e24e6cf780808faa9e421bd1edce25e0652`.
- Round-four receipt:
  `7516268b594e0a3498a6f5bc197d23899878a720`.
- Review scope: `clients/qbittorrent/`, X-10, A-04/A-09/A-28/A-45,
  the round-five handoff, the round-four P1 and every retained finding.
- The product commit changes exactly three assigned qBittorrent files:
  `README.md`, `client.go` and `client_test.go`. It changes no generated
  output, root adapter, shared API, execution state, task, plan or script.
- Scoped product diff SHA-256:
  `b64dad0634cf0217379d610a32ff24c06f267e1f5003105af4a6465911ad8c33`.
- Scoped product archive SHA-256:
  `b1a0103862abd883fcd43f04f5b591cbc22ace1f0122ef94be67e6916fd9d6d6`.
- Clean checks ran in a detached reviewer-owned worktree at the exact product
  commit. Independent probes ran in a disposable Git archive and were not
  committed.
- No live qBittorrent instance, credential, tracker coordinate, media payload,
  torrent mutation, filesystem action, release or deployment was used.

## Finding

### P1: a delayed old-session rejection invalidates a successful newer session

- Location: `clients/qbittorrent/client.go:536-613` and `:615-643`.
- Evidence: authentication has no session generation. Every 401 or 403 enters
  `invalidateSession`, which unconditionally sets `authenticated = false`.
  The read does not retain which authenticated session it used, so a response
  from an older request cannot be distinguished from rejection of the current
  session.
- Independent reproduction:
  1. Perform the initial successful login.
  2. Start two version reads and hold both after the server receives them under
     that session.
  3. Return HTTP 403 to the first read. It invalidates, performs one successful
     login refresh and completes its retry with `v5.0.0`.
  4. Only after that retry completes, return HTTP 403 to the second old-session
     read.
  5. Let both calls finish and count login POSTs.
- Observed result in three race-enabled repetitions:

  ```text
  login attempts=3, want initial login plus one shared refresh
  login attempts=3, want initial login plus one shared refresh
  login attempts=3, want initial login plus one shared refresh
  ```

  Both reads eventually succeeded, but the delayed rejection revoked the fresh
  in-memory authentication state and triggered an unnecessary third POST.
- Failure mode: an old response can overwrite newer connection truth. The
  duplicate login can replace or disturb the fresh cookie, amplify
  authentication traffic, and create another failure or IP-ban opportunity
  after a valid refresh already succeeded. If the redundant login fails, the
  client reports the connection unauthenticated despite the prior successful
  refresh.
- Contract: X-10 owns cookie session handling and authenticated reads; A-09
  requires instance-scoped connection observations. The repository requires
  already-materialized desired state to avoid another write and treats
  duplicated writes and false state as critical review findings.
- Required change: bind each authenticated request to a monotonic session
  generation. Apply 401/403 invalidation only when the rejected request's
  generation is still current. If another caller has already advanced the
  generation, retry once using that newer session without invalidating it or
  issuing another login. Preserve exact one-retry limits and the shared
  immutable flight result.
- Required proof:
  - initial login plus two concurrent old-session reads, with one early and one
    delayed 401/403 around a successful refresh, produces exactly two total
    login POSTs and two successful reads;
  - the late rejection cannot set the newer session unauthenticated;
  - three or more delayed old-session responses still cause one refresh;
  - oversized 401/403 bodies preserve the same generation guard;
  - a rejection from the current generation still performs exactly one
    refresh and one read retry;
  - shared failed-flight, later deliberate retry, caller cancellation and race
    tests remain green.
- Disposition: `current_blocker`.

## Round-four finding verification

| Round-four finding | Independent result |
| --- | --- |
| Failed authentication repeated once per waiter | Closed for callers that joined the same flight. Eight concurrent callers received one immutable sanitized unauthorized 403 result from one POST in three race-enabled repetitions. A later independent login remained able to retry. Delayed old-session responses expose the separate P1 above. |

## Earlier finding regression verification

| Earlier finding | Independent result |
| --- | --- |
| Canceled authentication waiter blocked or returned success | Closed. One login waiter and eight read waiters return `context.Canceled` before leader release and issue no read. |
| Missing required members became zero-valued evidence | Closed. Required and non-null inventory, properties, file and category members are enforced while legitimate zero values pass. |
| Duplicate logical inventory identities survived | Closed. Exact and case-equivalent v1/v2 duplicates reject the whole response. |
| Undocumented 2xx became success | Closed. Login and reads accept exact HTTP 200 only; other 2xx statuses are typed unsupported. |
| JSON selected duplicate, case-folded or invalid-UTF-8 fields | Closed. Duplicate and escape-equivalent members, mixed-case names, raw invalid UTF-8, unknown fields and trailing values fail. |
| Invalid inventory hashes became records | Closed. Each record requires one supported 40/64 hexadecimal identity; malformed records reject the whole response. |
| Nonpositive timeout removed the deadline | Closed. Nonpositive injected timeouts use 15 seconds and positive values are retained. |
| Case-equivalent request hashes bypassed duplicate checks | Closed. Supported request hash identities are compared case-insensitively. |
| Login accepted unusable SID state | Closed. Missing, empty, wrong-name, wrong-path, wrong-domain and Secure-over-HTTP cookies fail. |
| Hash delimiter and aggregate scope were unsafe | Closed. Empty, pipe, Unicode whitespace, controls, invalid UTF-8 and duplicates fail; exact aggregate maximum passes and one-over fails. |
| Oversized errors lost status or auth recovery | Closed for status and one-read behavior. 401/403/429/5xx retain sanitized classification; stale concurrent 401/403 generation is the new P1. |
| Version, piece-range and unsupported semantics were loose | Closed. Version shapes and piece ranges are strict; unsupported statuses remain distinct. |
| Handwritten/OpenAPI input bounds drifted | Closed. Exact query and credential maxima pass; missing credentials and one-over values fail. |
| Ambiguous endpoint paths were accepted | Closed. Literal root/prefix paths are stable; dot, encoded, repeated-separator and backslash forms fail. |

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
| OpenAPI structural parser | Passed: eight operations, login-only POST, closed fully-required response objects and no security scheme. |
| Root-import, generated-type, local-replace and mutation-route scans | Passed. |
| Public credential/private-coordinate scan | Passed; only synthetic `.invalid` fixture values exist. |
| Scoped diff/archive hashes, `git diff --check` and clean reviewer worktree | Passed before receipt creation. |
| Product failure-sharing, cancellation and reauthentication tests repeated twenty times | Passed. |
| Focused product correction and prior-response matrix under race, repeated three times | Passed. |
| Independent eight-caller shared-403 probe under race, repeated three times | Passed with one POST and one shared typed result. |
| Independent delayed old-session 403 probe under race, repeated three times | Failed contract consistently with one redundant second refresh. |

## Acceptance contribution

- A-04: X-10's response identity contribution passes. Hash identity and
  whole-response duplicate handling remain exact.
- A-09: not accepted for X-10. One authentication flight is now shared, but a
  delayed rejection from an older request can invalidate a successful newer
  session and repeat login.
- A-28: read-only progress/state/seeding evidence remains strict and bounded.
- A-45: generation and standard module/repository gates pass, but concurrent
  reauthentication still permits stale state to overwrite newer session state.

## Reviewer decision

`changes_requested`. Add generation-bound session invalidation and retry, then
prove delayed old-session failures cannot revoke a newer successful refresh or
cause duplicate login POSTs.
