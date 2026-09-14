# F-04 independent review, round one

## Decision

`changes_requested`.

The ordinary copy and hardlink paths pass the repository tests and preserve
several required invariants: exact destination preflight, descriptor-relative
no-follow access on Unix, exclusive staging, digest verification, atomic
no-replace publication, hardlink identity proof with no copy fallback, and a
portable write gate that fails closed.

Three filesystem safety defects block approval. Read-only hardlink
reconciliation can accept a replacement object as the approved effect,
canceled-copy cleanup can delete another actor's replacement at the staging
pathname, and newly created destination directory entries are not synced in
their containing directories before the action reports and journals success.
The handoff also records a nonexistent final product SHA.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Dispatch base:
  `8c75ba0c9bc2772f705241675262c6816dec7d3c`; tree
  `d18c1fe3580071655447ce982a971b364f1e4050`.
- Initial product:
  `c7c5418f6121f16429c49da45dc15e7fcea3eccd`; direct parent
  `99a7ed0b109dc1c19253408d1a8f0d6880ecda2c`; tree
  `ca3c7b923dd76d42c92dd43814351ae72380bff1`.
- Reviewed final product:
  `1733a0998e86bf4c71250fab11e17b41857bbc85`; direct parent is the initial
  product; tree `ec08ec06a85f7c418a7becd7d5f63b339c200bdb`.
- Reviewed handoff:
  `d7b299adaf9f1cb4f18115cb1c934d85d4e5aeb5`; direct parent is the final
  product; tree `df635c40db1d0b61ab9f8bdf9fb94a76e8099c46`.
- Owned product scope adds only `internal/filesystem/placement/fs_other.go`,
  `fs_unix.go`, `placement.go` and `placement_test.go` relative to the dispatch
  base. The handoff commit adds only `docs/execution/handoffs/F-04.md`.
- Scoped product diff SHA-256:
  `372616cd2789793e90748af3177816da25186c82f7a26069ce1450e2bdcb2baf`.
- Acceptance reviewed: A-18, A-19, A-20, A-22 and A-59.
- Review receipt checkpoint: commit containing this file; exact SHA is reported
  to the coordinator after commit because a Git commit cannot embed its own
  SHA.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-f04-review-r1` at the exact
handoff commit. The reviewer changed no product, execution-state, task,
generated, discovery, adapter or shared fixture path. Adversarial probes ran in
an archive copy of the exact product and were not added to the product tree.

## Findings

### P1: hardlink reconciliation is not bound to the approved source identity

`ReconcileHardlink` validates the request and passes its manifest to
`reconcilePlans` (`placement.go:234-250`). The hardlink reconciliation branch
opens the current source path (`placement.go:926-933`) and proves only that the
current source and destination are the same object (`placement.go:949-955`). It
never verifies the current source's identity, size or regular-file type against
the approved `plan.source` before claiming `already_satisfied`.

Independent archive-copy probe:

1. Create source object A and capture its exact manifest.
2. Remove A and create replacement object B at the approved source path.
3. Hardlink the destination to B.
4. Reconcile the request that remains bound to A.

Observed result, repeated under the race detector:

```text
replacement source/destination accepted as approved hardlink:
outcome="already_satisfied" affected=1
evidence=[operation=lost-hardlink-response ordinal=0
source_root=downloads source_path=episode.mkv
destination_root=library destination_path=Series/episode.mkv state=reconciled]
```

Failure mode: after a lost response or restart, an unrelated replacement inode
can be recorded as the approved hardlink effect. That defeats immutable-plan
binding and turns read-only reconciliation into false success evidence.

Required change: before comparing source and destination, require the current
source to be a regular file and match the approved manifest identity and size.
Keep a changed or unavailable approved source unresolved. Add the replacement
fixture above and preserve the valid lost-response fixture where the original
source and destination are the same inode.

Disposition: `current_blocker`.

### P1: staging cleanup deletes an unowned replacement file

`copyOne` creates an exclusive stage but records no opened-object identity
(`placement.go:626-630`). Every pre-publication cleanup closes the descriptor
and then unconditionally removes `stageName` from the destination directory
(`placement.go:631-638`). On Unix, `removeChild` is an unqualified
`unlinkat(parent, name)` (`fs_unix.go:264-265`). It does not prove the directory
entry still names the inode created by this operation.

Independent deterministic probe used a one-byte copy buffer to hold the copy
open, then:

1. Waited for the operation-created stage pathname.
2. Unlinked that pathname while Mastarr retained the open descriptor.
3. Created another actor's file at the same pathname.
4. Canceled the copy.

All ten ordinary repetitions and all three race-detector repetitions failed:

```text
replacement was deleted by cleanup:
open .../Movies/.mastarr-stage-cleanup-race-0.part: no such file or directory
```

Failure mode: cancellation or a copy/write/digest failure can delete a file
Mastarr did not create. The same pathname substitution window exists before
name-based publication, so the exclusive create alone does not retain ownership
of the later directory entry.

Required change: bind staging lifecycle to the object created by the operation
and prove that binding before any pathname publication or cleanup. Use the
strongest target-specific primitive available and fail closed when ownership
cannot be proven. An opaque unpredictable stage token reduces accidental
collision but does not replace object-identity verification. Add a controlled
pathname-substitution fixture that proves another object's file is preserved.

Disposition: `current_blocker`.

### P1: success does not durably sync newly created destination directory entries

Unix directory creation uses `mkdirat(current, component)`
(`fs_unix.go:73-124`) but never syncs `current`, the containing directory whose
new child entry must become durable. The single-file copy and hardlink paths
create missing parent directories through `ensureDirectoryPath`
(`placement.go:1019-1040`), then sync only the leaf destination parent after
file publication (`placement.go:690-699`, `placement.go:762-779`).

Directory mappings have the same gap. `ensureDestinationDirectory` creates
one or more components, then syncs only the final created directory itself
(`placement.go:1173-1191`), not each containing directory in which `mkdirat`
published a new name. The placement package has no other parent-sync call for
directory creation; the complete sync call inventory confirms only stage data,
leaf directory and final target-directory syncs.

Failure mode: copying to a previously absent `Movies/film.mkv` may sync the file
and `Movies` contents, journal `applied`, and return success while the new
`Movies` entry in the configured root is not crash-durable. A crash can make
the reported destination path disappear, contradicting the A-59 recovery
predicate and the specification's requirement to sync the parent directory
before durable completion.

Required change: after every successful directory creation, sync the containing
directory before closing or advancing. Preserve the ordering through nested
components. Treat sync failure with certainty matching whether a visible file
effect has already occurred, and add injected sync-order/failure coverage for
single-file, hardlink and nested-directory plans.

Disposition: `current_blocker`.

### P2: the handoff records a nonexistent final product SHA

`docs/execution/handoffs/F-04.md` twice identifies the follow-up and review
target as `1733a0903cd3d963b1cb312f7c5962af40954d1f`. That object does not match the
review target supplied by the coordinator and does not identify the reviewed
product. The exact final product is
`1733a0998e86bf4c71250fab11e17b41857bbc85`.

Failure mode: resume and integration evidence cannot resolve the claimed
checkpoint, despite this repository requiring exact product and review SHAs.

Required change: correct both handoff references to the exact final product and
record correction product/handoff SHAs separately in the next handoff.

Disposition: `current_blocker`.

## Preserved behavior

- Copy validates strong SHA-256 evidence and exact directory-child manifests.
- Source identities and sizes are rechecked before each write; copied bytes are
  hashed during transfer and source stability is checked before publication.
- All selected destination paths are inspected before the first directory or
  file mutation. Equal copy content is already satisfied; unequal content and
  different hardlink inodes conflict.
- Unix path traversal uses descriptor-relative no-follow operations. Traversal,
  root targets, symlinks and special files fail closed.
- Staging creation is exclusive, bounded and adjacent to the destination.
  Publication uses a no-replace hardlink/unlink sequence and final copy digest
  or hardlink inode read-back.
- Hardlink execution reports cross-device/unsupported outcomes and contains no
  copy fallback.
- Per-file applied/already-satisfied effects are returned and optionally
  journaled. Journal failure after publication returns `UncertainError`; copy
  reconciliation can prove an approved digest after source loss.
- Caller cancellation is checked before mutation and between bounded copy
  chunks. Post-publication read-back continues to resolve visible effects.
- Non-Darwin/Linux writes are rejected before filesystem mutation because the
  descriptor-relative guarantee is unavailable.
- The package imports root domain and ports only. It imports no generated DTO,
  adapter, storage or workflow package, and the architecture checks pass.

## Independent checks

| Check | Result |
| --- | --- |
| Exact base/product/handoff identity, ancestry, trees, owned scope and scoped diff | Passed, except the handoff's recorded final SHA is wrong as reported. |
| Full implementation and specification trace for preflight, copy, hardlink, journal and reconciliation paths | Completed independently. |
| Replacement-source hardlink reconciliation probe from exact product archive | Failed unsafe: replacement object accepted as approved effect. |
| Stage-path replacement plus cancellation probe, 10 repetitions | Failed unsafe in every repetition: replacement file deleted. |
| Both adversarial probes with `-race -count=3` | Failed in every repetition with the same semantic defects; no Go data race reported. |
| `GOWORK=off go test -mod=readonly -count=20 ./internal/filesystem/placement` | Passed. |
| `GOWORK=off go test -race -mod=readonly -count=3 ./internal/filesystem/placement` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/filesystem/placement` | Passed. |
| Root `GOWORK=off go test -mod=readonly ./...` and `go vet` | Passed. |
| Root, UI, tools, qBittorrent and NZBGet standalone tests/module verification | Passed with `GOWORK=off`; all modules verified. |
| Linux amd64/arm64, Windows amd64 and FreeBSD amd64 CGO-free placement compile matrix | Passed. |
| `./scripts/generate.sh --check` | Passed; aggregate and staged generation current. |
| Architecture and planning checks | Passed; 44 tasks, 60 acceptance cases and local links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed; Vacuum 100/100, architecture, standalone-module boundaries, lint, tests, vet, generation and verification. |
| Native power-loss, disk-full and cross-device mount | Not available in this review environment; the missing parent-sync sequence is established by the reviewed call graph. |
| Live media, credentials, private coordinates and mounted production data | Intentionally not used. |

## Acceptance disposition

- A-18: not accepted for F-04. Library-only placement exists, but staging
  ownership and durability defects block enabling it.
- A-19: not accepted. Copy distinguishes content by digest and normal hardlink
  execution distinguishes inodes, but hardlink reconciliation can certify an
  unapproved replacement inode.
- A-20: not accepted. Execution has no copy fallback, but recovery can falsely
  certify a replacement hardlink and the native EXDEV path lacks environment
  evidence.
- A-22: not accepted. No-replace publication and source checks pass, but
  cleanup can delete an unowned file and newly created directory entries are
  not crash-durable.
- A-59: not accepted. Copy digest reconciliation works, but hardlink read-back
  is not approval-bound and journaled success can outpace parent-directory
  durability.

This receipt does not approve executor wiring, release, deployment or any live
filesystem/media mutation. Durable W-02 effect persistence and native
cross-device/disk-full verification remain separate gates after these findings
are resolved.
