# X-14 independent review, round two

## Decision

`changes_requested`.

The correction closes all four round-one compatibility and control-safety
findings. Exact-hash validation happens before login, native empty HTTP 200
control responses succeed, the committed raw generated package is internal,
and SetLocation's route-specific 403 preserves the authenticated session.

One generation guardrail defect remains. The aggregate write generator chooses
the qBittorrent output path by testing whether the generated internal artifact
already exists. When that artifact is absent, the documented write path creates
the obsolete public raw client and does not recreate the committed internal
client. This blocks reproducibility approval and can recreate the exact public
control surface that the correction is intended to remove.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Reviewed coordinator prerequisite:
  `47eac36f73a881c7c597b9c6d02fa115168343ee`; tree
  `fcdc9d299197227282a67cf6683bae443b5cc009`.
- Reviewed product:
  `08cbb20b68aeb284673e0d19c40c0ea9e5f4f3c4`; direct parent is the
  prerequisite; product tree `cf7157728b87099552f3489681372ce2594ae6bc`.
- Reviewed handoff:
  `c9dd654c9d831022a59876d5f350e631c894a45c`; direct parent is the product;
  handoff tree `99bd9745697198ec8a04486cd211ff6d92ff72df`.
- Prior review receipt:
  `0243a396e44a99a0c36f1d2c04721c8bc522564f`.
- Product scope changes seven files under `clients/qbittorrent/`. The handoff
  commit adds only
  `docs/execution/handoffs/X-14-correction-round1.md`.
- Scoped product diff SHA-256:
  `317b247c2f1a17cad974c3f5810474d8d8926d73c8f68abd52f89c932dee053a`.
- Acceptance reviewed: A-28, A-29, A-30, A-31, A-33 and A-45.
- Review receipt checkpoint: commit containing this file; exact SHA is reported
  to the coordinator after commit because a Git commit cannot embed its own
  SHA.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-x14-review-r2` at the exact
handoff commit. The reviewer changed no product file, execution state, task,
fixture, adapter or generated artifact.

## Round-one finding disposition

### Exact single-torrent scope: closed

All five handwritten control methods share an exact identity validator. It
accepts only 40- or 64-character hexadecimal hashes and rejects empty,
case-insensitive `all`, pipe-separated and malformed values before session
selection or login. Product tests exercise every invalid form for Stop,
SetLocation, RenameFile, RenameFolder and Delete and assert zero requests.

An independent archive-copy probe called `Delete(ctx, "all", false)` against a
server that counted every request. It returned typed invalid input and the
server observed zero requests.

### Native empty control success: closed

The OpenAPI contract now describes status-only HTTP 200 responses without a
response body for all five control routes. The handwritten transport accepts
only an empty successful body; login retains its distinct exact `Ok.` and SID
cookie contract. Product tests cover empty success and reject unexpected
nonempty bodies as unknown.

An independent archive-copy probe returned login `Ok.` plus SID and an empty
HTTP 200 for Stop. Stop succeeded with exactly one login and one control
request.

### Generated control boundary: closed in the committed tree

The generated package is committed only at
`clients/qbittorrent/internal/generated/`. The handwritten public client wraps
that package and exposes the guarded methods. No public generated package is
present in the reviewed product tree.

An independent external-module compile probe imported
`github.com/guilycst/mastarr/clients/qbittorrent/internal/generated` through a
local replacement. Go rejected it with `use of internal package ... not
allowed`, proving consumers outside this module cannot use the raw builders.

### SetLocation 403 session preservation: closed

SetLocation maps its documented 403 to a sanitized, nonretryable conflict and
does not clear the selected SID. Other control-route 403 responses retain the
authentication classification and invalidation behavior.

An independent archive-copy probe returned SetLocation 403, then called Stop.
It observed the conflict, a successful Stop using the same SID, and exactly one
login across both calls.

## Remaining finding

### P1: aggregate write generation recreates the removed public raw client

The coordinator prerequisite makes the qBittorrent output path depend on the
existence of generated output (`scripts/generate.sh:189-201`). It defaults to
`clients/qbittorrent/generated/client.gen.go` and selects
`clients/qbittorrent/internal/generated/client.gen.go` only when that internal
generated file is already present. The authoritative module config already
declares `output: internal/generated/client.gen.go`; the aggregate generator
overrides that value with its state-dependent selection.

Independent source-only probe from an archive of exact product
`08cbb20b68aeb284673e0d19c40c0ea9e5f4f3c4`:

```text
rm clients/qbittorrent/internal/generated/client.gen.go
./scripts/generate.sh --write
generation completed
clients/qbittorrent/generated/client.gen.go
internal=missing
public=present
```

The corresponding aggregate check also searches for the obsolete public path
when the internal artifact is missing, rather than reporting the authoritative
internal output as missing. Normal checks pass only because the generated
artifact is already present and selects its own expected path.

Failure mode: committed output cannot be recreated from authoritative source
and generator configuration after deletion. The write command reintroduces a
public importable generated client capable of constructing unguarded control
requests, while leaving the required internal package absent and the module
unbuildable.

Required change: derive the aggregate output from authoritative configuration,
or set the current qBittorrent output unconditionally to
`clients/qbittorrent/internal/generated/client.gen.go`. If older-checkout
compatibility is retained, select it from authoritative source/configuration,
not generated artifact existence. Add a deletion-and-write probe that asserts
the internal output is restored byte-for-byte and the public path remains
absent.

Disposition: `current_blocker`.

## Independent checks

| Check | Result |
| --- | --- |
| Exact prerequisite/product/handoff identity, ancestry, trees, scope and scoped diff | Passed. |
| Every round-one invalid hash across all five methods, with zero requests | Passed. |
| Empty HTTP 200 control success and unexpected nonempty rejection | Passed. |
| SetLocation 403 classification and SID preservation across a later Stop | Passed. |
| Metadata-only Delete form and rejection of payload deletion | Passed. |
| External import of the internal generated package | Passed expected rejection. |
| `GOWORK=off go test -mod=readonly -count=20 ./...` in qBittorrent module | Passed. |
| `GOWORK=off go test -race -mod=readonly -count=3 ./...` in qBittorrent module | Passed. |
| `GOWORK=off go vet -mod=readonly ./...` and `GOWORK=off go mod verify` | Passed. |
| Pinned qBittorrent-module golangci-lint | Passed; 0 issues. |
| Nested generation and `check-generation.sh` with committed output present | Passed; no generated drift. |
| Root qBittorrent control adapter focused tests, race and vet | Passed. |
| Linux amd64 and arm64 qBittorrent client test compilation | Passed. |
| API/Vacuum, architecture, planning and formatting checks | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed. |
| Aggregate source-only deletion plus `./scripts/generate.sh --write` | Failed: public output created, internal output missing. |
| Live qBittorrent, credentials, private coordinates and mounted media | Intentionally not used. |

## Acceptance disposition

- A-28: accepted for X-14. Stop has exact single-hash scope and native empty
  success handling.
- A-29: accepted for the committed client API. Delete hard-codes the
  metadata-only form unless explicitly passed false, rejects true and has no
  externally importable raw generator in the committed tree.
- A-30: accepted for client scope. No re-add or resume surface was introduced.
- A-31: accepted for client control semantics and route-specific status
  normalization.
- A-33: accepted. Typed uncertainty remains, no mutation is blindly retried,
  and the session handling probes pass.
- A-45: not accepted. Aggregate write generation is state-dependent and does
  not recreate the authoritative internal output from source when it is absent.

G-01/no-overwrite, live qBittorrent product-version evidence and runtime
capability approval remain open. This receipt does not approve root
integration, release, deployment, Arr writes or live media mutation.
