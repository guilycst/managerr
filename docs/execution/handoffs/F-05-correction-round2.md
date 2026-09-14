# F-05 correction round two handoff

## Assignment and review source

- Task: F-05, move, rename, permanent delete and explicit cross-device
  copy/verify/delete composition.
- Owner: `/root/x05_implementer`; independent reviewer:
  `/root/x05_reviewer`.
- Correction dispatch checkpoint:
  `1d384697a750392c8b62d63a5e8119235c085458`.
- Round-one product under review:
  `174ff5db3ff393af02b10db24e7a9ef70b76a56f`.
- Round-one review receipt:
  `0951caf99f1814349b3d5dcc02db6d1b4fb06026`.
- Correction product commits:
  `f202aacafd24320cd45335064f97409d72b4c3ab` and
  `58ffe6ae5bb50f7693164a28a3749d8c21e65411`.
- Owned product path: `internal/filesystem/organize/`.
- Coordinator owns `docs/execution/state.json`; this lane did not edit it.

## Corrections

The Linux move and delete paths now move the selected source entry into a
private, operation-derived quarantine using descriptor-relative no-replace
rename before any irreversible publication or removal. The quarantined inode
is checked against the approved object and exact manifest. A raced replacement
at the public source pathname is restored to that pathname and returns
`ErrSourceChanged`; it is never published or removed. Regular-file moves use
the still-open quarantined descriptor with `linkat(AT_EMPTY_PATH)` and verify
the quarantine object before removing its final private link. Directory moves
use no-replace publication from the validated private quarantine. Delete
recurses only through the exact selected manifest and removes the quarantined
object after identity read-back.

Cross-device copy/verify/delete now retains an operation-owned destination
protection witness before source removal. The witness is a private tree in the
destination root, built from descriptor-backed links after verified copy
read-back. The destination and witness are checked immediately before source
removal and again after it. If the destination disappears or changes, source
removal is refused; if the destination changes after source removal, the
witness is used to restore it when vacant and the operation returns an
uncertain reconciliation error. The witness is cleaned only after a successful
final read-back. Exact directory payloads receive the same protection and
cleanup behavior.

The organizer constructor now rejects equal, nested, symlink-alias and other
physically conflicting configured roots. This gives every logical root one
unambiguous physical authority and prevents a writable/read-only alias from
selecting different mutation behavior for the same namespace.

Directory enumeration now opens a fresh descriptor relative to the retained
directory descriptor. Repeated exact read-backs therefore begin at the start
of the directory stream instead of sharing a consumed directory offset.

## Synthetic evidence

- `TestMovePathExchangeNeverPublishesReplacement` exchanges the public source
  path before native publication and proves the replacement remains at the
  source while the destination stays absent.
- `TestDeletePathExchangeNeverDeletesReplacement` performs the equivalent
  delete exchange and proves the replacement remains intact.
- `TestCopyVerifyDeletePreservesSourceWhenDestinationDisappearsAtDeleteBoundary`
  removes a verified file destination at the delete seam and proves the source
  is preserved with no destination claim.
- `TestCopyVerifyDeletePreservesDirectorySourceWhenDestinationDisappearsAtDeleteBoundary`
  repeats that boundary failure for an exact episode directory manifest.
- `TestCopyVerifyDeleteDirectoryCompletesAndCleansProtection` proves a
  successful directory composition removes the source, retains the verified
  destination, and leaves no private protection directory.
- `TestNewRejectsConflictingPhysicalRootAliases`,
  `TestNewRejectsNestedPhysicalRoots`, and
  `TestNewRejectsSymlinkedPhysicalRootAlias` prove the physical-root policy.

All fixtures use temporary roots and synthetic media bytes. No credentials,
private coordinates, tracker URLs, live services, production mounts or real
media inventories were used.

## Verification

| Check | Result |
| --- | --- |
| `GOWORK=off go test -mod=readonly ./internal/filesystem/organize -count=1 -timeout=60s` | Passed on Darwin; write cases skip because the platform is fail-closed. |
| Linux `docker run --rm -v "$PWD":/workspace -w /workspace golang:1.27 ... go test -mod=readonly ./internal/filesystem/organize -count=1 -timeout=120s -v` | Passed; all synthetic move, delete, cross-device, guard and alias cases. |
| Linux `docker run --rm -v "$PWD":/workspace -w /workspace golang:1.27 ... go test -mod=readonly ./internal/filesystem/organize -race -count=3 -timeout=120s` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/filesystem/organize` | Passed. |
| `GOWORK=off go test -mod=readonly ./... -count=1 -timeout=180s` | Passed. |
| CGO-free `go test -c` for Darwin amd64/arm64, Linux amd64/arm64, Windows amd64 and FreeBSD amd64 | Passed. |
| Fast pre-commit guardrail on both product commits | Passed: generation, Vacuum, API, architecture and targeted tests. |
| `git diff --check` | Passed for product and handoff changes. |

## Disposition and next step

The round-one P1s are addressed in the correction tree. The independent
reviewer must rerun the adversarial Linux pathname-exchange and destination-loss
probes against product `58ffe6ae5bb50f7693164a28a3749d8c21e65411`. Any
remaining native crash, disk-full or arbitrary same-user race is an explicit
uncertainty and must not enable an unreviewed write path. The coordinator should
record the product commits and this handoff commit in `state.json` without
editing this lane's product scope.
