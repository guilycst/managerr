# F-05 correction round four handoff

## Assignment and review source

- Task: F-05, same-filesystem move/rename, permanent delete and explicit
  cross-device copy/verify/delete composition.
- Owner: `/root/x05_implementer`; independent reviewer:
  `/root/x05_reviewer`.
- Dispatch product base:
  `ffc11d8f4acf6267a763c060bac701004b805dbe`.
- Prior review receipt:
  `7235e042302702abb3aeebd92ffb15dbd28577b1`.
- Owned product path: `internal/filesystem/organize/`.
- Coordinator owns `docs/execution/state.json`; this lane did not edit it.

## Correction result

The round-three review found that quarantine cleanup validated an entry, closed
the descriptor, and then removed the mutable pathname with `unlinkat`. Linux
does not provide a reviewed conditional unlink that binds that final removal to
the validated inode. A same-UID process can exchange the nested `payload` or
private quarantine directory after validation and before a pathname removal.

The product now fails closed at that boundary:

- Linux advertises placement primitives separately from quarantine cleanup.
  `requireQuarantineCleanup` returns `ErrUnsupported` with the missing
  inode-bound primitive as the reason.
- `removeOwnedQuarantine` and `removePrivateDirectory` retain the identity
  validation and the deterministic test seam, but issue no pathname unlink and
  return an explicit unsupported/uncertain result. The validated descriptor is
  never closed and followed by a name-based removal in this code path.
- Pending move, rename and permanent-delete plans are rejected before private
  quarantine creation. A pending cross-device copy/verify/delete composition is
  rejected before copying because its source-removal phase is unavailable.
  Already-materialized plans still reconcile as successful no-ops.
- Non-Linux fallback stubs expose the same fail-closed contract and keep the
  package cross-compilable. The existing independently verified byte-copy
  destination guards and partial-guard rollback remain in the source tree; the
  composition cannot enter source deletion while the final cleanup capability
  is blocked.

## Synthetic evidence

- `TestOrganizeCleanupFailsClosedBeforeQuarantineMutation` proves public move
  and permanent-delete requests return `ErrUnsupported` before creating a
  quarantine directory, publishing a destination or changing the source.
- `TestDeleteQuarantineCleanupFailsClosedAfterNestedPayloadExchange` validates
  the approved payload inode, exchanges it with an unapproved replacement, and
  proves both entries and their bytes remain after fail-closed cleanup.
- `TestMoveQuarantinePayloadCleanupFailsClosedAfterNestedEntryExchange` proves
  the same nested payload exchange is safe for move cleanup.
- `TestMoveQuarantineCleanupFailsClosedAfterNestedDirectoryExchange` validates
  the private quarantine directory, exchanges the directory entry, and proves
  both the approved and replacement directory inodes remain.

All fixtures use temporary roots and synthetic bytes. No credentials, private
coordinates, live services, production mounts or real media were used.

## Product checkpoint

- Product commit:
  `3be99364621311c4edc31a2571c095acfb5c1ed4`
  (`fix(filesystem): fail closed without inode-bound cleanup`).
- Handoff commit is separate and is reported after commit because Git cannot
  include its own object ID in this file.

## Verification

| Check | Result |
| --- | --- |
| `GOWORK=off go test -mod=readonly ./internal/filesystem/organize -count=1 -timeout=60s` | Passed on host. |
| `GOWORK=off go vet -mod=readonly ./internal/filesystem/organize` | Passed on host. |
| Linux `GOWORK=off go test -mod=readonly ./internal/filesystem/organize -count=1 -timeout=120s -v` in `golang:1.27` | Passed; pending mutation cases skip at the explicit cleanup gate and fail-closed/exchange cases pass. |
| Linux `GOWORK=off go test -mod=readonly -race ./internal/filesystem/organize -count=5 -timeout=120s` in `golang:1.27` | Passed. |
| Linux `GOWORK=off go vet -mod=readonly ./internal/filesystem/organize` in `golang:1.27` | Passed. |
| `GOWORK=off go test -mod=readonly ./... -count=1 -timeout=180s` | Passed. |
| CGO-free organize compilation for Linux amd64/arm64, Darwin amd64/arm64, Windows amd64 and FreeBSD amd64 | Passed. |
| `python3 scripts/check_planning.py` | Passed: 44 tasks, 60 acceptance cases; local links resolve. |
| `python3 scripts/check-architecture.py` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed: generation, staged generation, API/Vacuum, lint, architecture, tests, vet and module verification. |
| Product `git diff --check` | Passed. |
| Fast pre-commit guardrail on the product commit | Passed. |

## Disposition and blocker

The final quarantine cleanup P1 is closed by an explicit fail-closed result;
the unsafe validate-close-name-unlink sequence is unreachable. F-05 move,
permanent-delete and cross-device copy/verify/delete mutation acceptance stays
blocked until a reviewed inode/object-bound quarantine removal primitive is
available for the target. The independent reviewer should rerun the nested
payload and directory exchange probes against the exact product commit above
and verify that no approved or replacement entry is removed.

This handoff does not enable workflow wiring, linked-client actions, release,
deployment or live filesystem/media mutation.
