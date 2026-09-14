# F-05 independent review, round three

## Decision

`changes_requested`.

The correction closes the cross-device hardlink-witness defect and the partial
guard-construction retry defect. Independent regular-file and directory guards
now contain byte copies, remain intact when the visible destination is changed,
and earlier guards are removed when a later setup fails so the same operation
ID can retry.

The quarantine cleanup blocker remains. Nesting the payload in a mode-0700
directory restricts other users, but cleanup still validates and closes the
payload descriptor before unlinking its deterministic name. Another process
running as the Mastarr filesystem UID can exchange that entry in the final gap.
Updated exact-tree probes reproduced deletion of an unapproved replacement in
both permanent delete and move cleanup.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Prior product:
  `58ffe6ae5bb50f7693164a28a3749d8c21e65411`.
- Round-two review receipt:
  `ae52758ef328bbd3f8fa6ab8acf84243b9115835`.
- Reviewed correction product:
  `ffc11d8f4acf6267a763c060bac701004b805dbe`; tree
  `e46112235a5245434a7408a9cfc5cb4c8fafdbd1`.
- Reviewed handoff:
  `ba03c61e0298a5ec9499945be5e5cc0eca3fcd1a`; tree
  `2259dd372138f4073e8cef0f900db0dc763faad4`; direct parent is the exact
  product.
- Product commit changes only `internal/filesystem/organize/fs_linux.go`,
  `fs_unsupported.go`, `organize.go`, and `organize_test.go`.
- The handoff contains no later product change under
  `internal/filesystem/organize/`.
- Scoped correction diff SHA-256 from the prior product:
  `d718af9f4df2fab651c9a7c02958711c4d7816b55973e672b1b7cb523cd3a7bc`.
- Acceptance reviewed: A-21, A-23, A-24, and A-59.
- Review receipt checkpoint: the commit containing this file; its exact SHA is
  reported after commit because a Git commit cannot contain its own SHA.

Review ran from clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-f05-review-r3` at the exact
handoff. Adversarial tests ran only in an archive copy of the exact product
under `/tmp`; they are not product changes. Reviewer changed no product, task,
state, fixture, generated, adapter, client, module, or shared-worktree path.

## Finding

### P1: mode-0700 quarantine still loses object binding before unlink

Move and delete now create a deterministic private directory below the source
parent and rename the selected entry to its `payload` child
(`fs_linux.go:57-100`, `fs_linux.go:163-200`). This safely prevents a process
with only group or other access from traversing the quarantine.

The destructive cleanup remains name based. `removeOwnedQuarantine` opens the
payload, compares its inode with the approved identity, closes that handle,
then calls `unlinkat` on the payload name (`fs_linux.go:303-316`). Unix mode
0700 does not exclude another process running as the directory owner. The
quarantine directory name is derived from the operation ID, ordinal, and
reviewed relative path, and its child name is the fixed literal `payload`, so a
same-UID process with access to the media root can locate and exchange it.

Independent Linux archive-copy probes adapted the exact round-two race to the
new nested payload. They watched the final validation close and atomically
exchanged `.mastarr-*/payload` with a same-sized synthetic replacement before
`unlinkat`. Under `-race`:

```text
TestReviewerProbeDeleteQuarantineRaceCanDeleteReplacement:
attempt 32: quarantine unlink deleted replacement while approved inode survived under spare path

TestReviewerProbeMoveQuarantineCleanupCanDeleteReplacement:
attempt 24: quarantine cleanup deleted replacement after publishing approved inode
```

Both calls returned success in the unsafe state checked by the probes. Permanent
delete removed the unapproved replacement and left the approved inode at the
exchange path. Move published the approved file but also removed the unapproved
replacement during cleanup. The same helper is used for recursively selected
children, so the exact-directory scope remains exposed.

The product tests exchange only the public source before quarantine
(`organize_test.go:138-163`, `organize_test.go:231-253`). They do not exchange
the nested payload after its last identity validation, so their passing result
does not cover this syscall boundary.

Required change: keep destructive removal bound to the approved object through
the syscall, or use an isolation boundary that excludes every other process
which can run under the same filesystem identity. A deterministic 0700
directory alone does not provide that boundary. Add the post-validation nested
payload exchanges for move, top-level delete, and recursive child delete.

Disposition: `current_blocker`.

## Closed round-two findings

### Cross-device protection uses independent verified bytes

Resolved. `copyGuardEntry` creates exclusive regular files and copies bytes
from the already validated destination. `copyGuardFile` checks cancellation,
syncs the new file, rechecks source object/size, and verifies the SHA-256 digest
before the guard can protect source removal (`organize.go:572-670`). The guard
and visible destination no longer share an inode.

The injected post-verification destination mutation tests pass for a regular
file and an exact directory child. Each returns reconciliation-required after
source removal while the private guard still contains the approved bytes. The
three targeted correction tests passed five race-instrumented repetitions.

### Partial guard setup rolls back and retries

Resolved. The construction loop tracks every successfully created guard and
cleans them if a later guard fails (`organize.go:268-284`,
`organize.go:435-443`). The two-file collision fixture proves both sources are
preserved, the earlier guard is absent after the failure, and the same
operation ID completes after the injected later collision is removed.

## Preserved behavior

- Public source exchange before quarantine is rejected and restored without
  publishing or deleting the replacement.
- Destination disappearance before cross-device source removal preserves the
  source. Successful file and directory compositions clean their guards.
- Equal, nested, and existing symlink-alias roots remain rejected.
- Same-filesystem publication remains no-replace. Regular-file publication
  uses the validated descriptor, and no copy fallback is introduced.
- Request validation, descriptor-relative root confinement, exact manifest
  traversal, cancellation, and read-only reconciliation remain present.
- Darwin and unsupported targets remain fail-closed. The package imports no
  clients, adapters, storage, workflow, or generated upstream DTOs.

These properties do not compensate for a successful operation deleting an
unapproved object at the final quarantine-cleanup boundary.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, commit-owned paths, and scoped diff | Passed. |
| Linux supplied organize suite | Passed, including all quarantine, independent guard, rollback, directory, and root-alias fixtures. |
| Linux `GOWORK=off go test -mod=readonly -race ./internal/filesystem/organize -count=3 -timeout=180s` | Passed. |
| Linux focused `GOWORK=off go vet -mod=readonly ./internal/filesystem/organize` | Passed. |
| Independent regular-file and directory destination-mutation plus guard-retry tests under `-race -count=5` | Passed. |
| Updated nested-payload exchange probes under `-race` | Failed unsafe: permanent delete and move cleanup unlinked exchanged replacements. |
| Root `GOWORK=off go test -mod=readonly ./... -count=1 -timeout=180s` and focused vet | Passed. |
| Linux amd64/arm64, Darwin amd64/arm64, Windows amd64, and FreeBSD amd64 CGO-free organize test compilation | Passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 44 tasks, 60 acceptance cases, local links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed: reproducible generation, Vacuum 100/100, architecture, lint, tests, vet, and all module verification. |
| Product and handoff `git diff --check` | Passed. |
| Live media, private service coordinates, credentials, and production mounts | Not used. |

## Acceptance disposition

- A-21: not accepted for F-05. Confinement and root-alias checks pass, but move
  cleanup and permanent delete can still unlink an object outside the exact
  approved identity.
- A-23: not accepted. Exact manifest checks pass before cleanup, but recursive
  selected children use the same validate-close-unlink helper and retain the
  final replacement race.
- A-24: the independent byte-copy and partial-guard retry corrections are
  accepted, but F-05's cross-device delete phase still uses the unsafe
  quarantine cleanup primitive.
- A-59: not accepted for the F-05 contribution. Descriptor-bound destination
  publication and read-back remain correct, but successful move can perform an
  unapproved cleanup deletion after publication.

This receipt does not approve workflow wiring, release, deployment, or live
filesystem/media mutation. Linked-client stop/remove behavior remains a
separate workflow/control gate.
