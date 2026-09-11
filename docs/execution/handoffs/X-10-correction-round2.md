# X-10 correction handoff, round two

## Assignment

- Task ID and title: X-10, qBittorrent standalone read client; correction round two.
- Owner/agent and independent reviewer: `/root/c01_implementer`; `/root/d01_reviewer`.
- Base product SHA: `915eaab2568394ae4afb2ab0e580e8db9936fec8`.
- Correction dispatch/review context: shared-checkout assignment at `a018da9adb6a15487b616be189d92a5ba91c7366`; review receipt `8aa5867ebc587d0e61700ed94f15ef2dc8200237`.
- Branch/worktree ownership: shared checkout on `main`; coordinator owns integration and `docs/execution/state.json`.
- Owned product files: `clients/qbittorrent/`; generated output remains `clients/qbittorrent/generated/client.gen.go`.
- Owned evidence file: this handoff only; the round-one receipt remains untouched.
- Dependencies verified: the pinned qBittorrent WebUI API evidence, standalone module and oapi-codegen v2.8.0 generator are present. No root adapter, shared contract, state, task, script or plan file was changed.
- Required acceptance contributions: A-04, A-09, A-28 and A-45.

## Contract and work

Product commit: `aa9195b94d62d8a1c53825d638f111592f593617` (`fix(qbittorrent): close standalone client review gaps`).

The read-only module now requires every successful `Ok.` login response to
establish a nonempty SID usable across the configured API path. It rejects
missing, empty, wrong-name, wrong-path, wrong-domain and Secure-over-HTTP
cookies, while retaining per-client cookie isolation and one reauthentication
attempt after an expired session.

Hash-list members reject pipes, whitespace, controls, invalid UTF-8, empty
values and duplicates. The decoded joined hash value is checked before query
construction against the OpenAPI 4096-byte bound, while 40- and 64-character
identities remain valid. Filter/sort, category/tag, username and password
validation now use the exact OpenAPI bounds; credentials are explicitly
nonempty.

HTTP status classification runs independently of bounded error-body retention:
oversized 401/403 responses still trigger at most one reauthentication, and
oversized 429/5xx responses retain typed retryability without copying upstream
text into errors. Application and WebAPI versions accept one whitespace-free
numeric token with documented qBittorrent-style suffixes and reject multiline,
control, HTML and trailing-token bodies. File piece ranges require inclusive
nonnegative ordered pairs with no sentinel. Version-route 404/405/501 statuses
are typed `unsupported`, while torrent resource 404 remains `unavailable`.

Endpoint paths allow literal reverse-proxy prefixes and trailing slashes but
reject dot segments, encoded path bytes, backslashes and repeated separators.
The OpenAPI descriptions and generated client were regenerated from the same
contract. Synthetic adversarial tests cover each correction, including exact
and one-byte-over hash aggregates, cookie attributes, small and oversized
status bodies, version suffixes, valid/invalid piece ranges, status semantics,
exact input limits, and request URI plus Origin/Referer behavior.

No Seerr/qBittorrent mutation route, root import, live service, credential,
private coordinate, media payload, release or deployment was used.

## Verification

The following checks were run against the product tree committed as
`aa9195b94d62d8a1c53825d638f111592f593617`; each returned exit status 0.

| Command or scenario | Result / exit status | Evidence path |
| --- | --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 ./...` from `clients/qbittorrent` | Passed standalone and generated packages / 0 | `clients/qbittorrent/client_test.go` |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | Passed race suite / 0 | `clients/qbittorrent/client_test.go` |
| `GOWORK=off go vet -mod=readonly ./...` | Passed / 0 | `clients/qbittorrent/` |
| `GOWORK=off go mod verify` | Passed / 0 | `clients/qbittorrent/go.sum` |
| `GOWORK=off go mod tidy -diff` | Passed with no diff / 0 | `clients/qbittorrent/go.mod` |
| `GOWORK=off go generate ./...` | Regenerated successfully with pinned v2.8.0 / 0 | `clients/qbittorrent/generate.go` |
| `./check-generation.sh` | Byte-for-byte generated output reproduced / 0 | `clients/qbittorrent/check-generation.sh` |
| Module-local Vacuum OpenAPI lint | Quality 100/100 / 0 | `clients/qbittorrent/openapi.yaml` |
| Python YAML/JSON structural parser | OpenAPI 3.0.3, eight paths, closed file schema, no security scheme, fixtures parsed / 0 | `clients/qbittorrent/openapi.yaml`, `testdata/*.json` |
| Module dependency boundary scan with `GOWORK=off go list -mod=readonly -deps ./...` | No Mastarr root/domain/ports/storage/workflow/adapter imports / 0 | `clients/qbittorrent/go.mod` |
| `./scripts/check-guardrails.sh --fast` from repository root | Generation, API, architecture and focused root checks passed / 0 | repository guardrails |
| `python3 scripts/check_planning.py` | Planning valid: 43 tasks, 60 acceptance cases / 0 | `docs/execution/` |
| `git diff --check` and scoped staged-name check before product commit | Passed; only five qBittorrent product files staged / 0 | `clients/qbittorrent/` |
| SID attribute matrix and concurrent login/expired-session probes | Passed; unusable cookies rejected, concurrent login made one request, expired session reauthenticated once | `clients/qbittorrent/client_test.go` |
| Hash delimiter/control/UTF-8/duplicate/exact-aggregate/one-over probes | Passed; aggregate exact 4096 accepted and 4097 rejected | `clients/qbittorrent/client_test.go` |
| Small and oversized 401/403/429/500 matrix | Passed; typed status retained, 401/403 retried once, body text absent from errors | `clients/qbittorrent/client_test.go` |
| Application/WebAPI version shape matrix | Passed; stable, prerelease/build suffixes accepted and malformed bodies rejected | `clients/qbittorrent/client_test.go` |
| Piece-range and status semantics matrix | Passed; negative/reversed ranges rejected, version incompatibility unsupported, resource 404 unavailable | `clients/qbittorrent/client_test.go` |
| Exact credential/query bounds and endpoint URI matrix | Passed; maximum accepted, one-over rejected, clean prefix URI and headers exact, ambiguous paths rejected | `clients/qbittorrent/client_test.go` |

The shared checkout contains unrelated unstaged NZBGet edits from the other
connector lane. They were preserved and are absent from the product commit.

## Review and integration

- Reviewer identity/role and reviewed commit: `/root/d01_reviewer`, independent reviewer; round-one receipt `8aa5867ebc587d0e61700ed94f15ef2dc8200237` reviewed product `915eaab2568394ae4afb2ab0e580e8db9936fec8`.
- Findings fixed: all four P1 and four P2 findings in the receipt: SID establishment, logical hash delimiter/aggregate scope, status-before-body classification and reauthentication, strict version evidence, piece-range semantics, reachable unsupported outcomes, OpenAPI/handwritten bound parity and ambiguous endpoint paths.
- Fix commit and regression evidence: product `aa9195b94d62d8a1c53825d638f111592f593617`; focused tests and repository gates are listed above.
- Final reviewer decision: pending independent review of the exact product SHA.
- Integrated commit, recorded by coordinator: pending; coordinator updates `docs/execution/state.json`.

## Resume checkpoint

- Current state and outstanding uncertainty: product is ready at `aa9195b94d62d8a1c53825d638f111592f593617`. The module remains a compatibility candidate for qBittorrent 5.0+/WebAPI 2.x and has not been exercised against a live product build.
- Active process or agent ownership: no active process; coordinator owns state and integration. Unrelated NZBGet files remain outside this handoff and product commit.
- Next safe action: commit this handoff separately, then request independent review against the exact product SHA.
- Blocker and exact input/evidence needed: none for this correction slice; reviewer should rerun the adversarial matrix and inspect qBittorrent version suffix compatibility against the pinned upstream evidence.
- No conflicting writes or unknown files removed: state/tasks/scripts/plans and other connector edits were not staged or modified by this handoff.
