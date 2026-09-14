# F-05 correction round three handoff

## Assignment and review source

- Task: F-05, same-filesystem move/rename, permanent delete and explicit
  cross-device copy/verify/delete composition.
- Owner: `/root/x05_implementer`; independent reviewer:
  `/root/x05_reviewer`.
- Exact correction base product:
  `58ffe6ae5bb50f7693164a28a3749d8c21e65411`.
- Prior review receipt:
  `ae52758ef328bbd3f8fa6ab8acf84243b9115835`.
- Correction product:
  `ffc11d8f4acf6267a763c060bac701004b805dbe`.
- Owned product path: `internal/filesystem/organize/`.
- Coordinator owns `docs/execution/state.json`; this lane did not edit it.

## Corrections

Move and permanent delete now create an operation-derived private quarantine
directory below the retained source-parent descriptor. The directory is mode
`0700`, and the selected source entry is moved into its fixed `payload` child
with descriptor-relative `renameat2(RENAME_NOREPLACE)`. The payload is
revalidated against the approved inode and exact manifest before publication or
removal. Regular-file move publication uses the retained payload descriptor
with `linkat(AT_EMPTY_PATH)`; directory publication uses no-replace rename
from the validated private directory. Cleanup validates the operation-owned
private directory and payload before removal, and mismatch or disappearance
returns an uncertain reconciliation result without removing a replacement at
the public media path.

Delete recursively processes only the approved directory manifest. A child
failure, scope change or directory read-back failure restores the selected
payload to its original path before returning the error, preserving the
remaining source tree for review. Source-parent restoration is synced before
private quarantine cleanup.

Cross-device copy/verify/delete now snapshots the verified destination into an
independent regular-file copy tree under a private destination-root guard.
The guard no longer hardlinks its files to the visible destination, so an
in-place destination write cannot corrupt the protected evidence. Destination
and guard read-backs run before and after source removal. A destination loss or
mutation before removal blocks deletion; a change after removal leaves the
independent approved guard tree available for reconciliation or restoration.
If construction of a later guard fails, all earlier guards are cleaned before
the operation returns, allowing the same operation ID to retry after the
transient collision is removed.

The organizer continues to reject equal, nested, symlink-alias and other
physically conflicting configured roots. Directory enumeration opens a fresh
descriptor relative to the retained descriptor so repeated read-backs do not
reuse a consumed directory stream offset.

## Synthetic evidence

- `TestMovePathExchangeNeverPublishesReplacement` and
  `TestDeletePathExchangeNeverDeletesReplacement` prove a replacement at the
  public source path is restored and neither published nor removed.
- `TestCopyVerifyDeletePreservesSourceWhenDestinationDisappearsAtDeleteBoundary`
  and its directory counterpart prove source preservation when a verified
  destination disappears before source removal.
- `TestCopyVerifyDeleteRetainsIndependentProtectionAfterDestinationMutation`
  and `TestCopyVerifyDeleteRetainsIndependentDirectoryProtectionAfterMutation`
  mutate the visible destination after the final pre-delete guard check and
  prove the private guard still contains approved bytes.
- `TestCopyVerifyDeleteCleansEarlierGuardsOnLaterFailure` injects a
  deterministic second-guard collision, proves the first guard is removed, and
  retries the same operation ID successfully after the collision is cleared.
- `TestCopyVerifyDeleteDirectoryCompletesAndCleansProtection` proves a
  successful exact-directory composition leaves no guard directory.
- `TestNewRejectsConflictingPhysicalRootAliases`,
  `TestNewRejectsNestedPhysicalRoots`, and
  `TestNewRejectsSymlinkedPhysicalRootAlias` prove the physical-root policy.

All fixtures use temporary roots and synthetic media bytes. No credentials,
private coordinates, tracker URLs, live services, production mounts or real
media inventories were used.

## Verification

| Check | Result |
| --- | --- |
| `GOWORK=off go test -mod=readonly ./internal/filesystem/organize -count=1 -timeout=60s` | Passed on Darwin; writes skip because the target is fail-closed. |
| Linux `go test -mod=readonly ./internal/filesystem/organize -count=1 -timeout=120s -v` in `golang:1.27` | Passed; all organize, quarantine, guard and alias fixtures. |
| Linux `go test -mod=readonly ./internal/filesystem/organize -race -count=3 -timeout=120s` in `golang:1.27` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/filesystem/organize` | Passed. |
| `GOWORK=off go test -mod=readonly ./... -count=1 -timeout=180s` | Passed. |
| CGO-free organize test compilation for Darwin amd64/arm64, Linux amd64/arm64, Windows amd64 and FreeBSD amd64 | Passed. |
| `python3 scripts/check_planning.py` | Passed: 44 tasks, 60 acceptance cases; local links resolve. |
| `python3 scripts/check-architecture.py` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed: generation, Vacuum 100/100, lint, architecture, tests, vet and module verification. |
| Fast pre-commit hook on the product commit | Passed. |
| Product `git diff --check` | Passed. |

## Disposition and next step

The round-two P1s and P2 are addressed in product
`ffc11d8f4acf6267a763c060bac701004b805dbe`. The independent reviewer should
rerun the Linux quarantine-exchange, destination in-place mutation and
same-operation guard-retry probes against that exact tree. No linked-client,
workflow, state or live-media behavior is enabled by this correction.
