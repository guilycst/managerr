# X-10 correction handoff, round three

## Assignment

- Task ID and title: X-10, qBittorrent standalone read client; correction round three.
- Owner and independent reviewer: `/root/c01_implementer`; `/root/d01_reviewer`.
- Base product SHA: `aa9195b94d62d8a1c53825d638f111592f593617`.
- Review receipt: `cb7ca6cad52b3aaeec86e4d8339ee8bfb83832e3`.
- Branch/worktree: shared checkout on `main`; coordinator owns integration and `docs/execution/state.json`.
- Owned product path: `clients/qbittorrent/` (including generated output).
- Owned evidence path: this handoff only; earlier round-one and round-two receipts remain untouched.
- Required acceptance contributions: A-04, A-09, A-28 and A-45.

## Product result

Product commit: `ddd2b83d01ac6acaa76316db6368002dc7e7ca5b` (`fix(qbittorrent): enforce exact response and identity contracts`).

The client now accepts only documented HTTP 200 responses for login and every
read route. Non-200 2xx statuses are classified as typed unsupported outcomes
before response-body decoding, so 201/202/204/206 cannot become empty or
complete evidence. Existing status-first handling also preserves typed,
sanitized 401/403/429/5xx outcomes when bounded body retention fails.

The JSON decoder now rejects raw invalid UTF-8, duplicate members in one JSON
object, and mixed-case spellings of known fields while permitting valid Unicode
values and the same field in separate array objects. Every inventory
`TorrentInfo.hash` is required to be a nonempty 40-character v1 or 64-character
v2 hexadecimal identity; any empty, malformed, overbound, delimiter,
whitespace, control, invalid-UTF-8, or otherwise invalid record rejects the
whole inventory observation. Hex identities are canonicalized only for
duplicate detection, while the first accepted spelling remains in the emitted
query.

An injected HTTP client with a nonpositive timeout now receives the safe
15-second default; a positive timeout remains unchanged. The prior correction
round's cookie, input-bound, endpoint, version, piece-range, status,
read-only-route, and no-root-import safeguards remain in place. OpenAPI and
generated client output were regenerated together.

No qBittorrent mutation route, root-module import, live service, credential,
private coordinate, media payload, release, or deployment was used.

## Verification

Each command below returned exit status 0 against the product tree before the
product commit, and the product commit hook repeated the repository guardrail
checks successfully.

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./...` from `clients/qbittorrent` | Passed | `clients/qbittorrent/client_test.go` |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed | `clients/qbittorrent/client_test.go` |
| `GOWORK=off go vet -mod=readonly ./...` | Passed | `clients/qbittorrent/` |
| `GOWORK=off go mod verify` and `GOWORK=off go mod tidy -diff` | Passed with no diff | `clients/qbittorrent/go.mod`, `go.sum` |
| `GOWORK=off go generate ./... && ./check-generation.sh` | Pinned generation reproduced byte-for-byte | `clients/qbittorrent/generated/client.gen.go` |
| Module-local Vacuum lint with `../../api/vacuum.yaml` | Quality 100/100 | `clients/qbittorrent/openapi.yaml` |
| Python YAML/JSON structural parser | OpenAPI, schema, and fixture checks passed | `clients/qbittorrent/openapi.yaml`, `testdata/` |
| `./scripts/check-guardrails.sh --fast` from repository root | Generation, API, architecture, and focused root checks passed | repository guardrails |
| `python3 scripts/check_planning.py` | Planning valid: 43 tasks, 60 acceptance cases | `docs/execution/` |
| `./scripts/check-api.sh` | Passed | repository API guardrail |
| Linux amd64 and arm64 CGO-free cross-compile with `GOWORK=off go test -mod=readonly -c` | Both static test binaries built successfully | `/tmp/mastarr-qbittorrent-*.test` |
| Exact 200 matrix for login, version, inventory, properties, files, categories and tags | 201/202/204/206 rejected before decoding; no false evidence | `client_test.go` |
| Strict JSON and inventory identity adversarial matrix | Duplicate, mixed-case, invalid-UTF8, malformed and whole-inventory hash cases rejected; valid Unicode and separate-object repeats accepted | `client_test.go` |
| Timeout and canonical hash matrix | Negative/zero timeout defaulted, positive preserved; mixed-case hex duplicates detected with original spelling retained | `client_test.go` |

The shared checkout still contains unrelated unstaged NZBGet edits. They were
preserved and are absent from the product commit and this handoff commit.

## Review and integration

- Findings addressed: all five round-three findings in receipt `cb7ca6...`: exact HTTP 200 for every read, duplicate-aware strict JSON, per-record inventory hash validation, safe nonpositive timeout handling, and canonical hexadecimal duplicate detection.
- Earlier corrections retained: usable SID authentication, bounded and sanitized status classification, strict versions and ranges, query/credential limits, endpoint path policy, unsupported-route semantics, and read-only surface.
- Final independent review: pending against product `ddd2b83d01ac6acaa76316db6368002dc7e7ca5b`.
- Integrated commit and execution-state update: pending coordinator action; this worker did not edit `docs/execution/state.json`.

## Resume checkpoint

- Product is ready at `ddd2b83d01ac6acaa76316db6368002dc7e7ca5b`; no implementation blocker remains for this correction slice.
- The module remains a standalone compatibility client and has not been exercised against a live qBittorrent instance.
- Next safe action: independent reviewer reruns the adversarial matrix against the exact product SHA, then the coordinator records integration in the state ledger.
