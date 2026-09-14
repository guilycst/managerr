# F-05 independent review, round two

## Decision

`changes_requested`.

The correction closes the public-source pathname exchange demonstrated in
round one and rejects equal, nested, and existing symlink-alias roots. The
ordinary Linux suite, race run, cross-build matrix, and repository guardrails
pass.

Two destructive races still block approval. The new quarantine is a
predictable pathname in the writable media root; move cleanup and permanent
delete validate that entry, close the descriptor, and then unlink the name.
An exchange in that final gap deletes an unapproved replacement. The
cross-device destination witness consists of hardlinks, so an in-place write
changes both the destination and its witness. A write after the last guard
check can therefore corrupt the only protected copy before source removal.
The operation returns uncertain only after the approved bytes are gone.

Partial destination-guard construction also leaves earlier guards behind and
prevents the same operation ID from making progress on retry.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Round-one product:
  `174ff5db3ff393af02b10db24e7a9ef70b76a56f`.
- Round-one review receipt:
  `0951caf99f1814349b3d5dcc02db6d1b4fb06026`.
- Correction implementation:
  `f202aacafd24320cd45335064f97409d72b4c3ab`.
- Reviewed final product:
  `58ffe6ae5bb50f7693164a28a3749d8c21e65411`; tree
  `587b932b14caeb3d2e010b75e0a1d115f0e76f9e`.
- Reviewed handoff:
  `e4d289f079a475a2b9fef1d84ee6946bd0b02c76`; tree
  `1fbd5217a719bd38daa124b890bd65db440db99e`. The product is an ancestor of
  the handoff, and the handoff contains no later change under
  `internal/filesystem/organize/`.
- Product-owned paths are confined to `internal/filesystem/organize/`.
  Commit `f202aac` changes five files there; `58ffe6a` changes only
  `organize_test.go`.
- Scoped correction diff SHA-256 from the round-one product:
  `b45ff9ef6a06776d0a91d6f496eabb9e1cd75c0fcd31ca9868abc814cf093833`.
- Acceptance reviewed: A-21, A-23, A-24, and A-59.
- Review receipt checkpoint: the commit containing this file; its exact SHA is
  reported to the coordinator after commit because a commit cannot contain its
  own SHA.

Review ran from the clean detached handoff worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-f05-review-r2`.
Adversarial probes ran only in an archive copy of the exact product under
`/tmp`; they are not product changes. The reviewer changed no product, task,
state, fixture, generated, adapter, client, or shared-worktree path.

## Findings

### P1: quarantine cleanup still unlinks an unapproved replacement

Regular-file move keeps the approved descriptor through descriptor-bound
destination linking, but then records its identity, closes it, reopens the
quarantine pathname, closes that validation handle, and finally calls
path-based `unlinkat` (`fs_linux.go:110-133`). Permanent delete follows the
same validate-close-unlink sequence through `removeOwnedQuarantine`
(`fs_linux.go:171-175`, `fs_linux.go:220-231`). The quarantine name is a
deterministic digest of the operation ID, ordinal, and reviewed relative path
(`organize.go:1471-1475`) and is created directly in the writable source
parent. It is not protected by a private directory or a descriptor-bound
unlink primitive.

An independent Linux archive-copy probe watched the final validation close
and atomically exchanged the quarantine with a same-sized synthetic
replacement before `unlinkat`. Under `-race` it observed:

```text
TestReviewerProbeDeleteQuarantineRaceCanDeleteReplacement:
attempt 19: quarantine unlink deleted replacement while approved inode survived under spare path

TestReviewerProbeMoveQuarantineCleanupCanDeleteReplacement:
attempt 30: quarantine cleanup deleted replacement after publishing approved inode
```

The delete operation returned success after deleting the unapproved
replacement. The move operation published the approved file successfully but
also deleted the replacement during cleanup. Moving the race to a dot-prefixed
name does not maintain approved-object binding through the destructive
syscall. The same helper removes selected recursive children and empty
directories, so this also leaves the exact-directory boundary exposed.

Required change: retain ownership through the irreversible unlink, or use a
protocol whose namespace cannot be exchanged by another process with access
to the media root. A final open/stat followed by name-based unlink has the same
gap. Add tests that exchange the quarantine after its final validation and
prove no replacement is removed.

Disposition: `current_blocker`.

### P1: hardlink witnesses do not preserve cross-device bytes

`cloneGuardEntry` creates the destination witness with
`linkDescriptorNoReplace` for every regular file (`organize.go:549-579`). A
hardlink protects against pathname removal, but it shares the destination
inode and its mutable bytes. `deleteOne` verifies the destination and witness,
then reopens and validates the source before invoking source deletion
(`organize.go:877-891`). An external in-place destination write in that gap
changes both the visible destination and the witness.

An independent Linux archive-copy probe installed its watcher at the supplied
delete-boundary seam, waited for the source validation descriptor to close,
then rewrote the destination before source quarantine/removal. Under `-race`
it failed on the first attempt:

```text
TestReviewerProbeCrossDeviceGuardDoesNotProtectAgainstInPlaceMutation:
attempt 0: source deleted after destination and hardlink witness were mutated in place;
operation error = ... destination changed after source removal ... restore: destination path is occupied;
source bytes = ""; witness bytes = "corrupt!"
```

The post-delete guard verification correctly reports uncertainty, but the
approved source is already absent and both remaining names contain corrupt
bytes. The supplied disappearance fixtures pass because unlinking one
hardlink leaves the inode alive; they do not cover mutation of that inode.
Returning an uncertain error after losing the only verified bytes does not
satisfy A-24.

Required change: protect independently immutable verified content through
source removal, or otherwise exclude in-place writes until the destructive
step and final read-back complete. Cover file and directory-child in-place
mutation after the last pre-delete guard check.

Disposition: `current_blocker`.

### P2: partial guard construction is not resumable with the same operation ID

The guard-construction loop returns immediately when a later guard fails but
does not clean or adopt guards created for earlier mappings
(`organize.go:268-275`). Guard names are deterministic, and
`createPrivateDirectory` rejects an existing name (`fs_linux.go:247-267`).

A deterministic two-file archive-copy probe injected a collision for the
second guard. The first guard remained after the first call. After removing
only the injected second collision, a retry with the same operation ID failed
again because the first operation-owned guard now occupied its deterministic
name. Both sources remained safe, but the approved operation could not resume
and the hidden hardlink tree had no reconciliation or cleanup path.

Required change: roll back all guards created before source mutation when
construction fails, or validate and reuse exact existing operation-owned
guards. Add a multi-file retry fixture that removes the injected later failure
and proves the same operation ID can continue without leaked protection trees.

Disposition: `current_blocker`.

## Closed round-one findings and preserved behavior

- Public source exchange before quarantine is rejected and restored without
  publishing or deleting the replacement. The new fixtures assert both paths.
- Destination-path disappearance before source removal preserves the source
  for regular files and exact directories. Successful directory composition
  removes its guard in the ordinary case.
- Equal physical roots, lexical nesting, and existing symlink aliases are
  rejected by the constructor. The tests assert each rejection directly.
- Same-filesystem publication remains no-replace. Regular files publish from
  the validated descriptor; directories publish from quarantine. There is no
  silent copy fallback.
- Request validation, root confinement, exact manifest traversal, source
  identity checks, cancellation before mutation, and read-only reconciliation
  remain present.
- The package does not import linked clients, adapters, storage, workflow, or
  generated upstream DTOs. Darwin and unsupported platforms stay fail-closed.
- The correction handoff names both final product commits and the exact final
  review source, closing the stale-SHA documentation finding.

These properties do not offset deletion of an unapproved quarantine
replacement or loss of the only verified content through a mutable hardlink
witness.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, commit-owned paths, and scoped diff | Passed. |
| Linux `GOWORK=off go test -mod=readonly ./internal/filesystem/organize -count=1 -timeout=120s -v` | Passed; all supplied synthetic cases. |
| Linux `GOWORK=off go test -mod=readonly -race ./internal/filesystem/organize -count=3 -timeout=180s` | Passed. |
| Linux focused `GOWORK=off go vet -mod=readonly ./internal/filesystem/organize` | Passed. |
| Archive-copy quarantine exchange probes under `-race` | Failed unsafe: move cleanup and permanent delete unlinked exchanged replacements. |
| Archive-copy in-place destination mutation probe under `-race` | Failed unsafe on attempt 0: source absent; destination and hardlink witness corrupt. |
| Archive-copy partial guard/retry probe | Failed resumability: earlier guard leaked and blocked the same operation ID after the injected later collision was removed. |
| Root `GOWORK=off go test -mod=readonly ./... -count=1 -timeout=180s` and focused vet | Passed. |
| Linux amd64/arm64, Darwin amd64/arm64, Windows amd64, and FreeBSD amd64 CGO-free test compilation | Passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 44 tasks, 60 acceptance cases, local links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed: reproducible generation, Vacuum 100/100, architecture, lint, tests, vet, and all module verification. |
| Product and handoff `git diff --check` | Passed. |
| Live media, private service coordinates, credentials, and production mounts | Not used. |

## Acceptance disposition

- A-21: not accepted for F-05. Root-alias configuration checks are corrected,
  but move cleanup and permanent delete can still unlink an object that was
  never part of the approved plan.
- A-23: not accepted. Exact ordinary manifest tests pass, but the recursive
  delete path uses the same validate-close-unlink helper for selected children,
  leaving a final child-replacement race.
- A-24: not accepted. The witness preserves an unlinked destination but not an
  in-place modified inode, and partial guard construction cannot resume with
  the same operation ID.
- A-59: not accepted for the F-05 contribution. Descriptor-bound regular-file
  publication works, but an operation can perform an unapproved cleanup delete
  after publication, and operation-owned quarantine/guard artifacts lack a
  complete read-only recovery path.

This receipt does not approve workflow wiring, release, deployment, or live
filesystem/media mutation. Linked-client stop/remove behavior remains a
separate workflow/control gate.
