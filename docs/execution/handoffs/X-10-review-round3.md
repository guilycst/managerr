# X-10 independent review, round three

## Decision

`changes_requested`.

All five round-two findings close under independent probes. Exact HTTP 200
handling, duplicate-aware and case-exact JSON member decoding, inventory hash
shape validation, safe nonpositive timeout handling, and case-insensitive
duplicate request-hash detection behave as required. The eight earlier
cookie, request-bound, status, version, piece-range and endpoint findings also
remain closed.

Two new P1 findings and one P2 finding remain. The strict decoder does not
enforce OpenAPI-required response members and silently materializes absent
evidence as Go zero values. A caller canceled while another goroutine owns the
authentication mutex cannot leave promptly and public `Login` can report
success after its context was canceled. A single inventory response can also
return the same logical torrent identity more than once when the hash spelling
differs only by hexadecimal case.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Reviewed correction commit:
  `ddd2b83d01ac6acaa76316db6368002dc7e7ca5b`.
- Product parent:
  `c5cb6066d63b6f0e2da23c3a3c93d1a862156d38`.
- Product tree:
  `98744d10e9a558c38bc5efd7c579d0b1b0bad3c0`.
- Reviewed correction handoff commit:
  `c0cb825e5f559ae02cc4baa7757f1f8c2743e271`.
- Coordinator state commit inspected:
  `157f89046e4e6d0e5a811192085bcd1ac986454e`.
- Round-two receipt:
  `cb7ca6cad52b3aaeec86e4d8339ee8bfb83832e3`.
- Review scope: `clients/qbittorrent/`, X-10, A-04/A-09/A-28/A-45,
  the round-three handoff, all five round-two findings, and the eight retained
  round-one corrections.
- The correction changes exactly five assigned qBittorrent files. It changes
  no root adapter, shared API, execution state, task, plan or script.
- Scoped correction diff SHA-256:
  `0b2cb16c146bc55d2d89b8ce9477650a68afa01418e030164efc8f472d70113b`.
- Scoped product archive SHA-256:
  `5656e55ecec6bd92253cb0ce3e8baff022f89a12848725e595f3c538bc6e7d46`.
- Clean checks ran in a detached reviewer-owned worktree at the exact product
  commit. Independent probes ran in a disposable Git archive. The probe file
  is not product evidence and was not committed.
- No live qBittorrent instance, credential, tracker coordinate, media payload,
  torrent mutation, filesystem action, release or deployment was used.

## Findings

### P1: absent required response members become ordinary zero-valued evidence

- Location: `clients/qbittorrent/client.go:374-389`, `:407-411`,
  `:428-441`, `:452-469` and `:855-1018`.
- Contract location: `clients/qbittorrent/openapi.yaml:467-570`, `:572-644`,
  `:646-663` and `:665-671` declare every fixed member of `TorrentInfo`,
  `TorrentProperties`, `TorrentFile` and `Category` required.
- Evidence: `validateJSONMembers` records names that are present but never
  compares that set with the target struct's required fields. The generated
  scalar fields are non-pointer Go values, so `encoding/json` cannot
  distinguish a missing property from a legitimate zero. The bespoke
  inventory loop notices a missing hash only; it does not restore the other
  schema requirements.
- Independent reproduction after a normal synthetic login and HTTP 200:
  1. Return one inventory object with a valid 40-character `hash` and no other
     `TorrentInfo` member.
  2. Return `{}` for torrent properties.
  3. Return one file object containing only `piece_range: [0, 0]`.
  4. Return a category map whose `synthetic` value is an empty object.
- Observed result:

  ```text
  inventory only hash -> one Torrent, nil error; name/state/paths/timestamps/progress are zero values
  properties empty object -> zero-valued TorrentProperties, nil error
  file only piece_range -> zero-valued file with [0,0], nil error
  category empty object -> empty Name/SavePath category, nil error
  ```

- Failure mode: a partial or version-incompatible upstream response is
  certified as the frozen response shape. Missing completion, state, path,
  seeding, size and category evidence becomes concrete zero/empty/false data,
  so downstream code cannot retain it as unknown.
- Contract: the X-10 task requires a narrow strict OpenAPI compatibility
  boundary; A-45 requires generated contract and client agreement; R-02 and
  the repository invariant require missing evidence to remain unknown. The
  module README also promises strict JSON response decoding.
- Required change: enforce the current OpenAPI `required` sets during decoding,
  including map values, and reject non-nullable object values encoded as
  `null`; alternatively, revise the compatibility contract using pinned
  supported-version evidence and generated optional fields. Do not infer
  presence from decoded zero values.
- Required proof: representative and exhaustive presence tests for all four
  fixed response objects; one missing member at the beginning, middle and end;
  missing boolean/numeric fields whose valid value may be zero; null top-level,
  array-element and category-map values; and complete canonical fixtures that
  still pass.
- Disposition: `current_blocker`.

### P1: context cancellation cannot interrupt an authentication waiter

- Location: `clients/qbittorrent/client.go:520-547` and `:555-573`.
- Evidence: `ensureSession` checks the context once and then waits on
  `authMu.Lock()`. The mutex wait has no context selection, and the context is
  not rechecked after the lock is acquired. If another login succeeds first,
  the canceled waiter takes the `authenticated` fast path and returns nil.
- Independent reproduction:
  1. Start a background `Login` and block its synthetic HTTP 200 response after
     the request reaches the server.
  2. Start a second `Login` with a live context so it waits behind the first.
  3. Cancel the second context and wait 100 ms without releasing the first
     response.
  4. Release the first login.
- Observed result:

  ```text
  canceled waiter did not return during the 100 ms cancellation window
  after the first login completed, canceled Login returned <nil>
  ```

- Failure mode: cancellation cannot release goroutines queued behind a slow or
  stuck authentication request. Public `Login` can additionally report a
  successful operation after cancellation. Read methods eventually return the
  context error only after the unrelated login releases the mutex, so they
  also lose prompt cancellation at this boundary.
- Contract: X-10 explicitly owns request authentication and deadlines;
  `connectors.md:27-29` requires context deadlines and bounded HTTP clients.
  Cancellation is part of the repository's worker and explicit escape-hatch
  invariants.
- Required change: coordinate authentication through a context-selectable
  in-flight result or another design that lets each waiting caller return as
  soon as its own context ends. Preserve one active login and the authenticated
  fast path, while never turning a canceled call into nil success.
- Required proof: one blocked login plus one and many canceled waiters; waiters
  return `context.Canceled` before the first response is released; the active
  login remains usable; concurrent uncanceled callers still cause one login;
  expired-session reauthentication and race tests remain green.
- Disposition: `current_blocker`.

### P2: duplicate logical torrent identities survive one inventory response

- Location: `clients/qbittorrent/client.go:374-390` and `:725-752`.
- Evidence: the inventory loop validates each hash independently but does not
  track canonical identities. The request filter now correctly compares
  supported hexadecimal identities case-insensitively; the same identity rule
  is not applied to returned records.
- Independent reproduction: after a normal synthetic login, return HTTP 200
  with two inventory objects whose hashes are the same 40 hexadecimal digits,
  once lowercase and once uppercase.
- Observed result: `ListTorrents` returned two records with nil error, retaining
  both spellings as separate entries.
- Failure mode: one connection produces two ordinary records for one scoped
  torrent identity. A later adapter must guess whether the records are an
  overlap, contradiction or duplicate, even though the read client already
  owns exact v1/v2 identity validation.
- Contract: A-04 requires exact connection-scoped hash identity, and
  `connectors.md:34-37` requires retained supported v1/v2 identities. Missing
  or contradictory identity evidence cannot become two normal observations.
- Required change: reject the complete inventory as malformed when an exact or
  case-equivalent supported hash repeats. Preserve distinct v1 and v2
  identities and their original spellings.
- Required proof: exact and case-equivalent duplicate 40- and 64-character
  hashes fail the whole response; distinct hashes and distinct v1/v2 records
  pass; one invalid or duplicate later record returns no partial slice.
- Disposition: `current_blocker`.

## Round-two finding verification

| Round-two finding | Independent result |
| --- | --- |
| Undocumented 2xx became success | Closed. Login and all reads accept exact HTTP 200 only. 201/202/204/206 return typed nonretryable `unsupported` before decoding; no false empty inventory is returned. |
| JSON selected duplicate/case-folded/invalid-UTF-8 identity | Closed for the reported cases. Raw invalid UTF-8, exact and escape-equivalent duplicate names, mixed-case known names, nested duplicates, unknown fields and trailing values fail. Valid Unicode and repeated names in separate array objects pass. Required-member enforcement remains the new P1 above. |
| Invalid inventory hashes became records | Closed for per-record shape. Empty, missing, delimiter, whitespace/control, raw invalid UTF-8, wrong-length and non-hex hashes reject the complete response; 40/64 and uppercase identities pass. Response-level duplicate identity remains the new P2 above. |
| Negative timeout removed the deadline | Closed. Negative and zero injected timeouts become 15 seconds; positive timeouts are preserved. |
| Case-equivalent request hashes bypassed duplicate rejection | Closed. Exact and mixed-case duplicates for supported 40/64 hexadecimal query identities fail while the first spelling is retained for distinct inputs and aggregate accounting. |

## Earlier finding regression verification

| Earlier finding | Independent result |
| --- | --- |
| Login required no usable SID | Closed. Missing, empty, wrong-name, wrong-path, API-inapplicable, wrong-domain and Secure-over-HTTP cookies fail; usable cookies pass. Context-canceled waiters remain the new P1 above. |
| Hash delimiter and aggregate scope | Closed. Empty, pipe, Unicode whitespace, controls, invalid UTF-8 and duplicates fail; supported 40/64 inputs and the exact aggregate maximum pass; one-over fails. |
| Oversized error bodies lost status or auth recovery | Closed. Small and oversized 401/403 reauthenticate once; repeated unauthorized stops; 429 and 5xx keep sanitized code/status/retryability. |
| Arbitrary version text passed | Closed. Supported token shapes pass; whitespace, multiline, controls, HTML, trailing tokens and over-bound values fail. |
| Piece-range semantics were not validated | Closed. Exactly two ordered nonnegative indices pass; negative, reversed and wrong-size arrays fail. |
| Unsupported outcome was unreachable | Closed. Version 404/405/501 and undocumented 2xx statuses are unsupported; resource 404 remains unavailable. |
| Handwritten/OpenAPI input bounds drifted | Closed. Query and credential exact maxima pass; empty required credentials and one-over values fail. |
| Ambiguous endpoint paths were accepted | Closed. Literal root/prefix/trailing-slash paths produce exact request URIs; dot, encoded, repeated-separator and backslash forms fail. |

## Other safeguards that passed

- oapi-codegen v2.8.0 output reproduces byte-for-byte from the module OpenAPI.
- The public handwritten API exposes no generated DTO type.
- The OpenAPI has seven GET reads and the sole login POST. It has no mutation
  route or security scheme.
- No Mastarr root/domain/port/storage/workflow/adapter import or local replace
  directive exists in the standalone module.
- Redirect refusal, Origin/Referer, isolated cookie jar, transport timeout,
  response byte/item bounds, typed status mapping and sanitized error messages
  remain intact.
- All fixtures and examples are synthetic. The public safety scan found no
  credential, private address, private tracker or user-specific runtime path.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/parent/tree/handoff/state and five-file scoped correction | Inspected; identities above are exact. |
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
| OpenAPI structural parser | Passed: eight operations, seven GETs plus login POST, closed fully-required fixed objects and no security scheme. |
| Root-import, generated-type, local-replace and mutation-route scans | Passed. |
| Public credential/private-coordinate scan | Passed; only synthetic `.invalid` fixture values exist. |
| Scoped diff/archive hashes, `git diff --check` and clean reviewer worktree | Passed before receipt creation. |
| Focused correction and earlier-regression test matrix | Passed, including the race run of direct status/JSON/timeout/request-identity probes. |
| Missing-required-member response probe | Failed contract for inventory, properties, files and categories as P1 above. |
| Authentication mutex cancellation probe | Failed contract: canceled waiter stayed blocked and `Login` later returned nil. |
| Duplicate logical inventory identity probe | Failed contract: lower/uppercase copies both returned as ordinary records. |

## Acceptance contribution

- A-04: not accepted for X-10. Per-record v1/v2 hash shape is now exact, but
  duplicate logical identities can leave one connection as two ordinary
  records.
- A-09: session scoping, cookie applicability and one-time reauthentication
  pass. Cancellation while waiting for the shared authentication result does
  not.
- A-28: canonical responses preserve read-only progress/state/seeding values,
  and noncanonical statuses no longer become evidence. Missing required state
  and progress members can still become zero-valued evidence.
- A-45: generation and all standard module/repository gates pass, but runtime
  decoding does not implement the required sets in the generated contract.

## Reviewer decision

`changes_requested`. Correct the two P1 findings and the P2 identity finding,
then add the required adversarial regression proof before another independent
review. Passing generation, module tests and repository guardrails does not
establish required-member presence, prompt cancellation under concurrent
authentication, or unique logical identity within an observation.
