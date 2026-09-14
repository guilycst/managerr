# X-07 independent review, round one

## Decision

`changes_requested`.

The adapter preserves several critical controls: it never treats zero upload
speed as stopped, reads state after dispatched writes, does not blindly retry
an uncertain mutation, passes `deleteFiles=false`, refuses active-torrent
removal, and does not expose generated DTOs through Mastarr ports or domain
types. Two control-boundary defects remain: relocation sends the final content
path as qBittorrent's download location, and one aggregate capability flag
enables all five mutation families without operation-specific evidence.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Reviewed product checkpoint:
  `a51e51945bde229265163707261483fa7edce06a`; direct parent
  `7f8d0998b7ff3c081d055051a461a39cd224b1b7`; product tree
  `533edaedd709f61764ed8431b4f0645155849f8e`.
- Initial product commit:
  `7f8d0998b7ff3c081d055051a461a39cd224b1b7`; direct parent/base
  `f53cdd2efe912672fa521d05cfac5eefaaee5b3a`; tree
  `57663dd713932e6d38b4960cafe1eba37ece9f54`.
- Reviewed corrected handoff:
  `36212b350469504dee689958215609ed1040c949`; parent
  `cf4f5c14ef47273a1312f8f3bd71aee9acde73fc`; tree
  `2ae7cc16ba5ab787cea7a10d26af4e243bd1d561`. The handoff-only chain from
  the product is `343f027418c6ce67c8be1fca92427089c35b55b6`,
  `cf4f5c14ef47273a1312f8f3bd71aee9acde73fc`, then the reviewed commit.
- Product scope: `internal/adapters/qbittorrent/control/control.go` and
  `internal/adapters/qbittorrent/control/control_test.go`. Handoff scope:
  `docs/execution/handoffs/X-07.md`.
- Scoped product diff SHA-256:
  `69884e8470a990cbf0aecf9371171dcf05299b5b9b241ed2d8c8388d0b5d0468`.
- Release stage: pre-v0.1.0. The nested qBittorrent module remains read-only,
  so this adapter is not wired to a runtime write transport at the reviewed
  checkpoint.
- Acceptance reviewed: A-28, A-29, A-30, A-31 and A-33.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-x07-review-r1` at the exact
handoff commit. No producer transcript, live qBittorrent instance, credential,
private coordinate, media payload, release, deployment or product path was
used or changed.

## Findings

### P1: Relocate confuses qBittorrent's save location with the final content path

`Relocate` translates the requested `domain.FileTarget` to `desiredRemote` and
passes that value verbatim to `SetLocation` (`control.go:247-280`). It then
declares success only when `torrent.ContentPath == desiredRemote`
(`control.go:281-283`). The official WebUI API defines `location` as the
location to download the torrent to, while `content_path` is the absolute root
path for a multi-file torrent or absolute file path for a single-file torrent:

https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API-%28qBittorrent-5.0%29?oldformat=true

The passing test masks this mismatch. For desired single-file content
`/downloads/relocated/Synthetic Film.mkv`, it explicitly requires
`SetLocation(..., "/downloads/relocated/Synthetic Film.mkv")`
(`control_test.go:448-465`), and its fake sets `ContentPath` equal to the
location argument. A native-compatible model must send the containing download
location and then observe the final content path. Multi-file roots require the
same distinction.

The method also has no destination-vacancy evidence. Its second snapshot checks
only that the torrent's current payload did not change; it cannot detect an
unrelated file or directory already occupying the requested destination. A-31
requires collision refusal before native mutation and exact payload-path
read-back.

Failure mode: once a write-capable nested client is connected, a reviewed
single-file or multi-file destination can be passed as a save directory,
placing content below the wrong path. An occupied destination can reach the
native mutation without a safe preflight. Post-write read-back can report
unknown, but it cannot undo an incorrect or colliding move.

Required change: define whether `Relocate` receives the final content root or
the qBittorrent save directory and encode that distinction explicitly. For the
current final-content contract, derive the correct native `location`, model
single-file and multi-file native behavior, and verify every final payload path
after the write. Require destination-vacancy evidence before dispatch; if the
current port cannot supply it, keep relocation unsupported until a reviewed
precondition or contract extension exists. Add collision, single-file, and
multi-file tests whose fake does not set `ContentPath` equal to `location`.

Disposition: `current_blocker`.

### P1: One broad capability state authorizes unrelated control mutations

The repository's capability model says each capability describes one narrowly
scoped operation, and the compatibility matrix separates `CAP-QBT-STOP` from
`CAP-QBT-SCOPE`. X-05 keeps both runtime capabilities unknown pending their own
versioned evidence. X-07 instead stores one `WriteCapability`, reports one
`qbt.control` capability, and makes `writeAllowed(operation)` ignore its
operation argument (`control.go:52-65`, `150-168`, `617-621`). Once that single
state is supported, Stop, Relocate, RenameFile, RenameFolder and Remove all
dispatch.

The constructor requires a nonempty version and evidence string, but it does
not bind that evidence to a method or capability ID. The tests demonstrate the
problem by using one synthetic evidence string to enable every mutation
(`control_test.go:161-170`). The only blocked-capability test exercises Stop;
it does not prove that unverified relocation, rename and removal remain blocked
independently (`control_test.go:346-363`).

Failure mode: evidence that proves only stop behavior can authorize metadata
deletion or payload movement. This turns an unknown destructive capability
into an enabled one and defeats the compatibility matrix's fail-closed split.

Required change: store and report operation-specific capability states,
versions and evidence for stop, relocation, file rename, folder rename and
metadata-only removal, or at minimum preserve the frozen stop/scope split.
Gate each method by its own capability key. Add tests showing that supporting
Stop cannot dispatch Delete, SetLocation or either rename, and that supporting
one scope operation cannot enable the others without matching evidence.

Disposition: `current_blocker`.

## Preserved behavior

- `stalledUP` with zero upload speed remains seeding, and only explicit
  paused/stopped states satisfy Stop.
- A lost stop response is reconciled through a detached bounded read; an
  externally resumed torrent blocks later removal without another stop or any
  delete call.
- Remove requires a stopped state, always passes `false` to Delete, treats an
  already absent record as satisfied, and never claims payload deletion.
- File/folder rename rejects observed in-torrent collisions or scope expansion,
  rereads the torrent before dispatch, and requires path read-back afterward.
- Unknown post-dispatch effects remain `outcome_unknown`; tests confirm one
  native call and no blind resubmission.
- Native qBittorrent errors map to sanitized domain errors. The adapter imports
  normalized `clients/qbittorrent` DTOs; no generated type reaches root ports
  or domain packages.
- No re-add, resume, recategorization, retagging or runtime transport wiring was
  added. The read-only nested client cannot satisfy the mutation seam at this
  checkpoint.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, tree, owned scope and diff | Passed; product changes only the two assigned control files, and the later chain adds only the X-07 handoff. |
| `GOWORK=off go test -mod=readonly -count=50 ./internal/adapters/qbittorrent/control` | Passed. |
| `GOWORK=off go test -race -mod=readonly -count=10 ./internal/adapters/qbittorrent/control` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/adapters/qbittorrent/control` | Passed. |
| Focused pinned `golangci-lint` for the control package | Passed; 0 issues. |
| Root and nested qBittorrent `go mod verify`; nested client test/vet | Passed. |
| Linux amd64 and arm64 control-package test compilation | Passed. |
| `python3 scripts/check-architecture.py` | Passed. No generated DTO imports in domain or ports. |
| `./scripts/check-guardrails.sh --ci` | Passed; generation, Vacuum, lint, architecture and root/UI/tools/client module checks completed. |
| Official qBittorrent `setLocation` and `content_path` contract comparison | Failed for the adapter's relocation model, as recorded above. |
| Capability matrix versus adapter gate comparison | Failed; distinct stop/scope evidence is collapsed into `qbt.control`. |
| `git diff --check` | Passed. |

## Acceptance disposition

- A-28: accepted for this adapter contribution. Zero-speed seeding,
  post-write state reconciliation, external-resume refusal and no blind retry
  are preserved.
- A-29: partially accepted. The stopped precondition, metadata-only false flag,
  absent-record idempotency and retained fake payload pass; removal remains
  incorrectly coupled to unrelated capability evidence.
- A-30: accepted for this adapter contribution. No re-add or resume operation
  exists, and missing record state remains distinct from payload state.
- A-31: not accepted. Rename scope and observed collision checks pass, but
  relocation uses the wrong native path semantic, lacks destination-vacancy
  evidence, and can be enabled by unrelated control evidence.
- A-33: accepted for the implemented reconciliation mechanics. Dispatched
  writes receive one detached bounded read-back, unresolved effects stay
  unknown, and no method retries the write internally.

No product file, fixture, task definition, shared script, client module or
`docs/execution/state.json` was modified by the reviewer.
