# F-05 independent review, round one

## Decision

`changes_requested`.

The ordinary synthetic cases and all repository guardrails pass. The reviewed
implementation also preserves no-replace destination semantics, explicit
cross-device selection, read-only reconciliation entry points, and the package
does not import clients, adapters, storage, or workflow code.

Two destructive race defects block approval. Linux move and delete validate an
approved descriptor but mutate the pathname later, allowing an external
replacement object to be moved or deleted. Cross-device copy/verify/delete can
delete the source after the verified destination disappears, leaving no copy
while returning success. Root aliases with conflicting mutability are also
accepted, and the handoff's review target remains stale.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Dispatch checkpoint:
  `d6a9c59bc096e11eed6cc191d02aaff9a3a11e0b`.
- Initial product:
  `123df00da9c83c67ccab9b3a2b4c51cfbcf55f35`; tree
  `31d604122a6ebbacf7863eaac3ffafbc79a7f1bc`. That commit adds only the
  seven files under `internal/filesystem/organize/`.
- Reviewed final product:
  `174ff5db3ff393af02b10db24e7a9ef70b76a56f`; tree
  `5368b5348a7db0b7375ea0451142ee68641b4662`. The follow-up changes only
  `internal/filesystem/organize/organize.go`.
- Reviewed handoff:
  `2166486018bcc1512dcaa813104c507fe2f9dbbe`; direct parent is the final
  product; tree `a9bc78f9e223f089b714a2386effb4054ac78962`.
- Scoped product diff SHA-256 from the dispatch checkpoint:
  `c02202ef84bfffbed957107137c797ab0ce2d8da04ab185cbcfb7e0af7ba7cda`.
- Acceptance reviewed: A-21, A-23, A-24, and A-59.
- Review receipt checkpoint: the commit containing this file; its exact SHA is
  reported to the coordinator after commit because a commit cannot embed its
  own SHA.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-f05-review-r1` at the exact
handoff commit. The reviewer changed no product, task, execution-state,
generated, discovery, adapter, client, or shared fixture path. Adversarial
tests ran in an archive copy of the exact handoff tree under `/tmp`; they are
not product changes.

## Findings

### P1: move and delete mutate a pathname after losing approved-object binding

`moveOne` opens and validates the approved source, calls the test race seam,
then reopens and validates the current path (`organize.go:484-522`). It closes
that current descriptor before `renameat2(RENAME_NOREPLACE)` mutates the source
name (`organize.go:523`; `fs_linux.go:15-27`). An external process can exchange
the directory entry after the last validation and before the syscall. The
retained earlier descriptor does not bind `renameat2` to that inode.

`deleteOne` has the same gap. It reopens and validates the current target, then
closes the descriptor (`organize.go:587-610`). Recursive `deleteNode` opens and
validates a child, closes it, and calls name-based `unlinkat`
(`organize.go:627-672`; `fs_linux.go:29-41`). The syscall can therefore unlink
a replacement inode. Post-read-back can report uncertainty, but it cannot undo
an unapproved move or recover a deleted replacement.

An independent Linux archive-copy probe used inotify to observe the final
validation descriptor close and atomically exchanged the selected pathname
with a same-sized replacement before the native syscall. Under `-race`:

```text
TestReviewerProbeDeletePathRaceCanDeleteReplacement:
attempt 1: name-based unlink deleted replacement while approved inode survived
under spare path

TestReviewerProbeMovePathRaceCanMoveReplacement:
attempt 77: name-based rename moved replacement to the approved destination;
operation error = ... reconciliation: destination identity changed ...
```

The delete call returned success after deleting the unapproved replacement.
The move call returned an uncertain error only after the replacement was
already visible at the approved destination. The same primitive can move a
directory after it gains an unreviewed child or delete a replaced selected
child, so the defect reaches A-21 and A-23 as well as single files.

Required change: use a destructive protocol that retains object ownership
through publication/removal. A second pathname stat immediately before the
same name-based syscall is insufficient. Quarantine/exchange and validate
before irreversible removal, with safe restoration on mismatch, is one
possible shape. Fail closed where the target cannot provide the required
primitive. Add fixtures that exchange the pathname after final validation and
prove the replacement is neither moved nor deleted.

Disposition: `current_blocker`.

### P1: cross-device composition can delete the only remaining copy

After placement returns, `CopyVerifyDeleteWithOperation` runs read-only copy
reconciliation (`organize.go:249-259`) and then passes only source manifests to
`DeleteWithOperation` (`organize.go:260-264`). The delete phase has no
destination handle, manifest, or guard. If the verified destination is removed
or replaced before source unlink, source deletion still proceeds.

The deterministic Linux probe removed the verified destination from the
package's delete-boundary seam without changing the approved source. Observed
under `-race`:

```text
TestReviewerProbeCrossDeviceDestinationLossPreservesSource:
data loss: source and previously verified destination are both absent;
operation error = <nil>
```

This directly contradicts A-24's requirement that interrupted removal never
destroy the only verified copy. The product follow-up strengthens digest and
directory-name read-back, but that evidence is released before deletion and
does not close this gap.

Required change: make source removal conditional on destination evidence that
remains protected through the irreversible step. The protocol must preserve or
restore the source when the destination changes at the boundary; adding
another ordinary check before unlink leaves the same race. Cover destination
removal and replacement, for files and exact directories, including a
lost-response retry.

Disposition: `current_blocker`.

### P2: conflicting physical root aliases are accepted

`New` rejects duplicate IDs but does not compare canonical root paths or their
authority (`organize.go:110-141`). A deterministic probe configured the same
canonical path once writable and once read-only under different IDs. The
constructor returned success:

```text
TestReviewerProbeAmbiguousRootAliasesRejected:
duplicate physical root aliases with conflicting mutability were accepted
```

This makes the action boundary's mutability decision depend on which logical
alias a request names even though both aliases address the same objects. It is
an ambiguous configuration for permanent delete and undermines the fail-closed
root authority expected by A-21.

Required change: reject duplicate physical paths with conflicting authority,
and define and enforce the policy for nested physical roots. Put the invariant
at the effective-configuration boundary and retain a defensive check in the
organizer constructor if it can be instantiated independently.

Disposition: `current_blocker`.

### P2: the handoff names the superseded product as the review source

The product checkpoint correctly records follow-up
`174ff5db3ff393af02b10db24e7a9ef70b76a56f`, but the review section still says
the independent review is pending from
`123df00da9c83c67ccab9b3a2b4c51cfbcf55f35`. The coordinator dispatched and
this receipt reviewed the follow-up tree. Durable execution tracking requires
one exact review source.

Required change: make the handoff review source the final product SHA and
record subsequent correction product and handoff SHAs separately.

Disposition: `current_blocker`.

## Preserved behavior

- Request validation rejects empty manifests, root targets, traversal,
  noncanonical paths, overlapping selected source/destination maps, missing
  identity, and missing strong digests for copy composition.
- Linux traversal is descriptor-relative and no-follow. Symlinks and special
  files are rejected, and destination publication uses
  `RENAME_NOREPLACE` without an overwrite fallback.
- Ordinary same-filesystem move verifies the final destination identity and
  old-path absence, syncs both parent directories, and detects EXDEV rather
  than silently copying.
- Ordinary delete uses exact recursive manifests, never calls `RemoveAll`, and
  does not recurse into an unlisted child. Missing selected paths reconcile as
  already satisfied.
- Cross-device behavior is exposed through a distinct request and method. It
  delegates copying to reviewed placement, requires SHA-256 evidence, and does
  not silently replace move with copy.
- `ReconcileMove`, `ReconcileRename`, and `ReconcileDelete` perform read-only
  observations. Normal prior-publication and prior-deletion repetitions are
  idempotent in the supplied fixtures.
- Darwin and other non-Linux targets compile and reject organize writes. The
  package contains no linked-client mutation or generated DTO leakage.

These preserved properties do not compensate for the two paths that can move
or destroy an unapproved object or the cross-device path that can destroy both
copies.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, commit-owned paths, and scoped diff | Passed. |
| Linux `GOWORK=off go test -mod=readonly -race -count=3 ./internal/filesystem/organize` and focused vet in `golang:1.27` | Passed. |
| Host `GOWORK=off go test -mod=readonly ./internal/filesystem/organize` and focused vet | Passed; destructive tests correctly skip on fail-closed Darwin. |
| Archive-copy destination-loss probe | Failed unsafe: both copies absent and operation error was nil. |
| Archive-copy pathname-exchange move/delete probes | Failed unsafe under `-race`: replacement object was moved or deleted. |
| Archive-copy conflicting-root-alias probe | Failed: constructor accepted the ambiguous authority. |
| Root `GOWORK=off go test -mod=readonly ./...` | Passed. |
| Linux amd64/arm64, Darwin amd64/arm64, Windows amd64, and FreeBSD amd64 CGO-free test compilation | Passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 44 tasks, 60 acceptance cases, local links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed: deterministic generation, Vacuum 100/100, architecture, lint, tests, vet, and all module verification. |
| Product/handoff `git diff --check` | Passed. |
| Live media, private service coordinates, credentials, and production mounts | Intentionally not used. |

The repository's passing tests do not exercise the final path-to-syscall race
or destination disappearance at the copy/delete boundary.

## Acceptance disposition

- A-21: not accepted for F-05. Normal confinement checks pass, but a pathname
  replacement can be destructively acted upon, and conflicting physical root
  authority is accepted.
- A-23: not accepted. Ordinary changed-child fixtures fail closed, but a
  directory or selected child can change after validation and before the
  name-based move/delete syscall.
- A-24: not accepted. The independent probe destroys source and destination
  while the composed operation returns success.
- A-59: not accepted for the move contribution. Ordinary destination read-back
  can reconcile a prior publication, but read-back only discovers the wrong
  object after an unapproved name-based move has already occurred.

This receipt does not approve workflow wiring, release, deployment, or live
filesystem/media mutation. Linked-client stop/remove behavior remains a
separate workflow/control gate.
