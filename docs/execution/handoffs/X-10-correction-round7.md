# X-10 correction handoff, round seven

## Assignment

- Task ID and title: X-10, qBittorrent standalone read client; correction round seven.
- Owner and independent reviewer: `/root/c01_implementer`; `/root/d01_reviewer`.
- Base product SHA: `84a50fa8a458a9550c231d1fcae3f0de9bb7faf1`.
- Review receipt: `ef0b917061ebfdaf246d9190438dcf3febec7d1b` (round-six review).
- State checkpoint used for dispatch: `84a50fa`.
- Branch/worktree: shared checkout on `main`; coordinator owns integration and
  `docs/execution/state.json`.
- Owned product path: `clients/qbittorrent/`.
- Owned evidence path: this handoff only; prior round receipts remain untouched.
- Required acceptance contributions: A-04, A-09, A-28 and A-45.

## Product result

Product commit: `a16e4b3713b75c4321ab7ff37c5de4ad64ab83d6`
(`fix(qbittorrent): bind session cookies to generations`).

The client no longer gives `net/http` a cookie jar. A successful login
extracts one validated, applicable, nonexpired SID from the HTTP 200 response
and stores it with the monotonic authentication generation. Every read sends
that SID explicitly from an immutable `sessionCredential` snapshot. Read
response cookies are parsed for diagnostics/login handling but are never
installed into active session state, so delayed old 401/403 responses,
including deletion or obsolete `Set-Cookie` headers, cannot restore an old
SID.

401/403 handling compares the rejected request's generation with the active
generation. A stale response leaves a newer session and SID intact, then
retries once through the current credential. A response carrying the current
credential clears that generation, performs one shared refresh, and retries
once. The prior shared authentication result and caller-selectable
cancellation behavior remain intact.

The README now documents explicit SID handling and SID-plus-generation binding.
The stale-session matrix includes an applicable obsolete `Set-Cookie` header.
The new credential regression asserts the exact SID sequence across a stale
rejection and a current-generation rejection: `session-1`,
`session-2`, `session-2`, `session-3), with only the required three
login POSTs. Login deletion cookies are also rejected.

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
| Focused stale-cookie and credential tests, `-count=20` | Passed | `TestStaleSessionRejectionCannotInvalidateFreshSession`; `TestSessionCredentialBindsSIDAndGeneration` |
| `python3 scripts/check_planning.py` | Planning valid: 43 tasks, 60 acceptance cases; local links resolve | `docs/execution/` |
| `./scripts/check-api.sh` | Passed; Vacuum quality 100/100 | `api/openapi.yaml` |
| `python3 scripts/check-architecture.py` | Passed | repository architecture boundaries |
| `./scripts/check-guardrails.sh --fast` | Passed generation, API, architecture and focused root checks | repository guardrails |
| `./scripts/check-guardrails.sh --ci` | Passed root/UI/tools generation, API, lint, architecture, tests, vet and module verification | repository guardrails |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -c -o /tmp/mastarr-qbittorrent-round7-amd64.test .` | Passed; static Linux amd64 test binary | `/tmp/mastarr-qbittorrent-round7-amd64.test` |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go test -mod=readonly -c -o /tmp/mastarr-qbittorrent-round7-arm64.test .` | Passed; static Linux arm64 test binary | `/tmp/mastarr-qbittorrent-round7-arm64.test` |
| `git diff --check` | Passed | product commit |
| Product commit hook | Passed generation, Vacuum/API, architecture and focused root guardrails | `a16e4b3713b75c4321ab7ff37c5de4ad64ab83d6` |

## Review and integration

- Round-six P1 addressed: automatic cookie-jar selection and response
  persistence are removed from the session path.
- Round-six P1 addressed: each read carries an atomic SID and generation
  credential; status handling uses the generation associated with the SID
  actually sent.
- Retained corrections: exact HTTP 200, strict duplicate-aware JSON and
  required-member validation, inventory hash identity checks, bounded and
  sanitized errors, safe timeout defaults, strict version/range semantics,
  endpoint path policy, unsupported-route classification, read-only surface,
  shared authentication failures and prompt waiter cancellation.
- Final independent review: pending against product
  `a16e4b3713b75c4321ab7ff37c5de4ad64ab83d6`.
- Integrated commit and execution-state update: pending coordinator action;
  this worker did not edit `docs/execution/state.json`.

## Resume checkpoint

- Product is ready at `a16e4b3713b75c4321ab7ff37c5de4ad64ab83d6`.
- Handoff is intentionally a separate commit and will follow the product
  commit.
- The standalone module has not been exercised against a live qBittorrent
  instance.
- Next safe action: independent review against the exact product SHA,
  followed by coordinator state recording.
