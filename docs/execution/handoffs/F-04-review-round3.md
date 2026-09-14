# F-04 independent review, round three

## Decision

`approved`.

The exact correction closes all three Linux blockers from round two. Linux copy
staging has no pathname alias, failure cleanup performs no directory-name
deletion, and hardlinks publish from the already-open approved source
descriptor. The fallback package also compiles on Windows and FreeBSD. No new
blocking finding was reproduced in the owned scope.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Reviewed product:
  `2621343dcd3b80c259f5cec808dadb7bfbb0434d`; direct parent
  `9ccf0648a489ca5b9e9248090ee3416acbc97386`; tree
  `241f15e5ede21b34c329002137747f728599d357`.
- Reviewed handoff:
  `f3dd6498f207a0a0283f9503d62ff5bab67d1299`; direct product parent; tree
  `fc9dbd421f2036e9ec53149fd5577e16973b94f6`.
- Prior review receipt:
  `9c39e1d7e024b06548c81ab994af608327463afc`.
- Product scope: `internal/filesystem/placement/` only.
- Scoped correction diff SHA-256, from prior product
  `3e8f2f52a27b95c4f2cb4f6fd7abbf22496fe9bb`:
  `ff39f1c3271907d4d0b9e93c94172e732d359fa5a34690ba4a724310d77506b7`.
- Acceptance reviewed: A-18, A-19, A-20, A-22, and A-59.

Review used a clean exact-handoff worktree and synthetic temporary files only.
The independent Linux probe was injected with a Go overlay and ran in an
unprivileged container, leaving product files untouched. No credential, private
coordinate, live media, external filesystem, release, or deployment was used.

## Round-two blocker disposition

### Successful copy has no staging pathname alias: closed

Linux creates an unnamed inode with `O_TMPFILE` in the already-open destination
directory at `internal/filesystem/placement/fs_linux.go:15-32`. Publication
links that descriptor directly to the no-replace final name with
`linkat(AT_EMPTY_PATH)` at lines 35-46. `copyOne` closes unpublished stages on
every pre-publication failure and closes the published descriptor before parent
sync/read-back at `placement.go:637-724`.

The independent probe completed a copy, enumerated the destination directory,
and found exactly `movie.mkv`; no hidden or old-convention stage entry existed.
It passed three race-detector repetitions on Linux. The same copy path also
passed as UID/GID 65532 with no added container capabilities.

### Substituted directory is never removed by path: closed

`cleanupDirectories` performs no unlink at
`internal/filesystem/placement/placement.go:1260-1266`. Error and cancellation
paths can preserve a created directory for later explicit recovery, but cannot
delete an object another actor substituted at the same name.

The independent probe replaced the newly created destination directory inside
the directory-sync seam and returned a synthetic sync failure. The replacement
survived the operation in all three Linux race-detector repetitions.

### Hardlink publication uses the validated source descriptor: closed

`hardlinkOne` retains the source handle opened and checked against the approved
manifest, invokes the race seam, then passes `source.file` to publication at
`internal/filesystem/placement/placement.go:765-774`. Linux uses
`linkat(AT_EMPTY_PATH)` on that descriptor at `fs_linux.go:49-60`; it never
resolves the mutable source pathname during publication. Post-publication
directory sync, inode read-back, and source-path recheck remain at
`placement.go:796-810`.

The independent probe renamed the approved source and created a replacement at
the original path after validation. The destination named the approved inode,
never the replacement, while the action returned `ErrSourceChanged` for
read-only reconciliation. This passed three race-detector repetitions and also
passed in an unprivileged Linux container.

### Portable fallback build: closed

`fs_other.go` now has non-shadowing, compile-safe fail-closed helpers at lines
187-213. Windows amd64 and FreeBSD amd64 package compile probes passed. Linux
amd64/arm64 probes also passed; Darwin and other target writes remain explicitly
unsupported rather than using a pathname-based fallback.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/parent/tree, handoff ancestry/tree, clean status, scoped paths and diff hash | Passed. |
| Independent Linux copy-alias, substituted-directory and source-exchange overlay probe, `-race -count=3` | Passed as unprivileged UID/GID 65532. |
| Product's six focused copy/cancellation/collision/directory/hardlink regressions, Linux `-count=3` | Passed. |
| Full Linux placement suite, `-race -count=3`, and Linux `go vet` in `golang:1.27` | Passed. |
| Unprivileged Linux ordinary copy and descriptor-source hardlink tests, verbose | Passed without extra capabilities. |
| Host `GOWORK=off go test -mod=readonly -count=50 ./internal/filesystem/placement` | Passed; Darwin writes skip at the explicit platform gate. |
| Host focused `-race -count=10` and `go vet` | Passed. |
| Root `GOWORK=off go test -mod=readonly -count=1 ./...`, `go vet`, and `go mod verify` | Passed; all modules verified. |
| Windows amd64 and FreeBSD amd64 CGO-free placement compile | Passed. |
| Linux amd64 and arm64 CGO-free placement compile | Passed. |
| Lint, architecture and planning self/full checks | Passed; zero lint issues and 44 tasks/60 acceptance cases. |
| `./scripts/check-guardrails.sh --ci` | Passed; generation, Vacuum 100/100, lint, architecture, all module tests/vet and verification passed. |

## Acceptance disposition

- A-18: accepted for the F-04 contribution. Copy publishes only the reviewed
  destination, verifies its digest, and performs no Arr action.
- A-19: accepted for the F-04 contribution. Copy conflicts on unequal content;
  hardlink identity is descriptor and inode based.
- A-20: accepted for the implementation contribution. Descriptor hardlink
  errors fail unsupported with no copy fallback. Native cross-device mount
  execution remains a separate release-environment verification gate.
- A-22: accepted for the corrected race contribution. Publication is
  no-replace; anonymous staging and preserved directories cannot delete or
  publish substituted pathname objects.
- A-59: accepted for the F-04 contribution. Successful copy has one final
  directory entry; hardlink/copy read-back and uncertain-result behavior remain
  explicit.

Native crash, disk-full, and cross-device mount exercises remain outside this
local review and are not implied by approval. Future directory janitor ownership
and W-02 effect persistence remain separate lanes. This receipt approves only
the reviewed F-04 product; it does not authorize executor wiring, release,
deployment, or live filesystem/media mutation.

No product file, discovery path, task definition, shared script, module
manifest, or `docs/execution/state.json` was modified by the reviewer.
