# X-14 independent review, round one

## Decision

`changes_requested`.

Generation and module guardrails pass, and the handwritten wrappers preserve
the intended form field names, SID cookie, context deadline and
`deleteFiles=false` for ordinary single-hash calls. Three compatibility and
safety defects block approval: the handwritten hash guard accepts
qBittorrent's `all` sentinel, real qBittorrent control success has an empty
body rather than `Ok.`, and the public generated client can directly encode
both `hashes=all` and `deleteFiles=true`.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Reviewed product:
  `3fce4c79f4e52ba3147f300e8a460e9e44c228bc`; direct parent
  `5497915f233ba90ddfef318f2dd35161a8e67375`; product tree
  `83eaaba24d75ffe300759b1c0e687290dbbea5f7`.
- Reviewed handoff:
  `8e6b7831bfa5f1e1b84e0ad901c694e30d8623cc`; direct parent is the product;
  handoff tree `ca4bd00cc613e9a4aa1718ba27bddc198ab1939e`.
- Product scope: `clients/qbittorrent/`. Product commit changes six files only:
  `README.md`, `client.go`, `client_test.go`, `generate.go`,
  `generated/client.gen.go` and `openapi.yaml`.
- Product-to-handoff diff adds only `docs/execution/handoffs/X-14.md`.
- Scoped product diff SHA-256:
  `6d9386b944d141e9f7c06cee26a2e224fdd32790b460e0481f8d0f0934f01258`.
- Acceptance reviewed: A-28, A-29, A-30, A-31, A-33 and A-45.
- Review receipt checkpoint: commit containing this file; exact SHA is reported
  to coordinator after commit because a Git commit cannot embed its own SHA.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-x14-review-r1` at exact
handoff commit. Reviewer changed no product file, execution state, task,
shared fixture, root adapter or generated artifact.

## Findings

### P1: handwritten control wrappers accept qBittorrent's all-torrents sentinel

`controlHashValue` rejects empty, whitespace and pipe-separated hashes, but it
does not reject literal `all` (`client.go:652-656`). All five control wrappers
use this validator. qBittorrent's official WebUI API defines `all` as the
all-torrents value for `hashes`, including Stop and Delete:

https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API-%28qBittorrent-5.0%29?oldformat=true

Independent archive-copy probe called `Delete(ctx, "all", false)`. The method
authenticated, sent one control request and returned success:

```text
--- FAIL: TestReviewControlRejectsAllScopeBeforeDispatch (0.00s)
    x14_review_probe_test.go:54: Delete accepted qBittorrent all-torrents sentinel after 1 control request
```

Failure mode: a supposedly single-item call can remove every torrent record.
Stop and SetLocation can similarly broaden to every torrent. Payload deletion
is false in the handwritten Delete path, but losing every torrent record still
breaks tracking, seeding and exact approved scope.

Required change: reject the case-insensitive reserved `all` sentinel before
authentication or dispatch, in addition to pipe-separated values. Add a table
covering all five wrappers and assert zero login/control requests for `all`,
empty, pipe-separated and malformed hashes. Prefer exact supported torrent ID
syntax when the compatibility contract can freeze it.

Disposition: `current_blocker`.

### P1: control success contract requires a body qBittorrent does not return

The OpenAPI document models every control HTTP 200 as `text/plain` enum `Ok.`.
`postControl` accepts success only when the trimmed body equals `Ok.`
(`client.go:627-639`). Product tests therefore synthesize `Ok.` for every
control response.

Official qBittorrent 5.0 documentation specifies status outcomes for Stop,
Delete, SetLocation and both rename operations without a success body. The
qBittorrent 5.0 source handlers perform their mutation and return the default
empty result; `stopAction`, `deleteAction`, `setLocationAction`,
`renameFileAction` and `renameFolderAction` do not return `Ok.`:

https://github.com/qbittorrent/qBittorrent/blob/release-5.0.0/src/webui/api/torrentscontroller.cpp

Independent archive-copy probe supplied the documented/native HTTP 200 empty
response. Handwritten Stop rejected it:

```text
--- FAIL: TestReviewDocumentedEmptyControlSuccess (0.00s)
    x14_review_probe_test.go:32: documented HTTP 200 empty response rejected: qBittorrent qbit.torrents.stop failed (unknown)
```

Failure mode: every real successful qBittorrent control mutation is returned as
unknown. Root read-back may later prove desired state, but the standalone
client's compatibility contract and direct result are still false, and every
synthetic success test exercises a response qBittorrent does not produce.

Required change: model and accept HTTP 200 with the native empty response body
for these control routes. Keep HTTP acceptance separate from desired-state
proof in the root adapter. Replace `Ok.` synthetic control fixtures with empty
responses and add a negative probe for unexpected nonempty bodies if strict
body handling is retained.

Disposition: `current_blocker`.

### P1: public generated client bypasses metadata-only and exact-scope safety

`generated/client.gen.go` is a public importable package and contains request
builders for all control routes. `DeleteTorrentsRequestDeleteFiles` has a
`Valid` helper, but the generated form builder never calls it; its bool-backed
field accepts `true`. Generated hash fields also accept `all` and pipe-separated
values. This bypasses the handwritten `Delete(true)` and hash guards while
remaining part of the client module's exported generated surface.

Independent probe constructed the public generated Delete request and observed:

```text
--- FAIL: TestReviewGeneratedDeleteCannotEncodePayloadRemoval (0.00s)
    x14_review_probe_test.go:43: generated helper accepted payload-delete request: http://synthetic.invalid/api/v2/torrents/delete body="deleteFiles=true&hashes=all"
```

This contradicts README and handoff claims that the module has no helper that
can request payload deletion and can emit only `deleteFiles=false`.

Required change: make raw generated control transport inaccessible to external
module consumers, for example by generating under a module-internal package
and exposing only guarded handwritten methods. Alternatively generate models
without an exported raw control client and build transport around guarded
types. Add a compile/runtime probe proving public supported API cannot encode
`deleteFiles=true`, `hashes=all` or multiple hashes.

Disposition: `current_blocker`.

### P2: SetLocation's documented 403 is misclassified as authentication failure

The official SetLocation contract defines HTTP 403 as lacking write access to
the target directory. X-14 uses a global Forbidden response described as an
authentication rejection, and `postControl` invalidates SID plus returns
`ErrorUnauthorized` for every 403 (`client.go:627-630`). The synthetic status
test asserts this only through Stop and never covers SetLocation's route-specific
meaning.

Failure mode: a filesystem permission/configuration failure is reported as bad
credentials and discards a valid session, sending operator remediation and
retry behavior in the wrong direction.

Required change: define route-specific status normalization. Preserve
SetLocation 403 as a sanitized non-authentication failure and do not invalidate
the SID for that documented outcome. Test subsequent authenticated requests to
prove the session remains installed.

Disposition: `current_blocker`.

## Preserved behavior

- Ordinary single-hash wrappers use POST with exact documented form names,
  URL escaping, Origin, Referer and validated SID cookie.
- Handwritten `Delete(..., true)` rejects before authentication or dispatch,
  and ordinary `Delete(..., false)` emits literal `deleteFiles=false`.
- 401/403 handling does not retry a mutation within the same call.
- Transport timeout and caller cancellation retain typed outcomes and request
  deadlines.
- Upstream bodies, credentials and private coordinates do not enter typed
  errors.
- Generated code is reproducible with pinned oapi-codegen v2.8.0.
- Nested module imports no Mastarr root package. Generated DTOs do not leak into
  root domain or ports.
- No root runtime write wiring, Arr write, live service or media mutation was
  added.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, scope and scoped diff | Passed. |
| OpenAPI control routes and generated request/response types | Inspected independently. |
| Official qBittorrent 5.0 WebUI API and release-5.0.0 control source | Failed product success-body and SetLocation 403 claims, as recorded above. |
| `GOWORK=off go test -mod=readonly -count=20 ./...` in qBittorrent module | Passed. |
| `GOWORK=off go test -race -mod=readonly -count=3 ./...` in qBittorrent module | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` and module verification | Passed. |
| Pinned qBittorrent-module `golangci-lint` | Passed; 0 issues. |
| `GOWORK=off go generate ./...`, module `check-generation.sh`, clean generated diff | Passed. |
| Root control adapter focused tests, race and vet | Passed. |
| Linux amd64 and arm64 qBittorrent client test compilation | Passed. |
| API/Vacuum, architecture, standalone-client boundaries and planning checks | Passed. |
| Full root/UI/tools/client lint matrix | Passed; 0 issues. |
| `./scripts/check-guardrails.sh --ci` | Passed. |
| Reviewer empty-200, handwritten `all`, and public generated Delete probes from archive copy | Failed in all three unsafe/incompatible ways recorded above. |
| Live qBittorrent, credentials, private coordinates and mounted media | Intentionally not used. |

## Acceptance disposition

- A-28: not accepted for X-14. Stop route/form exists, but native successful
  response is rejected and `all` can broaden scope.
- A-29: not accepted. Ordinary Delete(false) is correct, but handwritten and
  generated surfaces can remove all torrent records; generated surface can
  encode payload deletion.
- A-30: partially accepted. No re-add or resume surface exists, but broad record
  removal defeats exact association scope.
- A-31: not accepted. Form fields and 409 mapping exist, but all-torrent
  relocation remains reachable and native success response is modeled wrong.
- A-33: accepted for no blind in-call mutation retry and typed uncertainty;
  compatibility defects above still block the client.
- A-45: accepted. Generation is clean and every module/architecture/guardrail
  check passes with `GOWORK=off`.

G-01 and runtime qBittorrent capabilities remain open. This receipt does not
approve root integration, release, deployment, Arr writes or live media
mutation.
