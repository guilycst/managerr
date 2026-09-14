# F-04 independent review, round two

## Decision

`changes_requested`.

The correction closes all four findings recorded in round one as written:
hardlink reconciliation now binds the current regular source to the approved
identity and size, stage-path substitution is no longer followed by an
unqualified stage unlink, each newly created directory entry is synchronized
through its containing parent, and the correction handoff names the exact
product SHA.

Three independently reproduced Linux safety defects still block F-04. A
successful copy leaves its staging pathname as an unreported hardlink to the
published destination, cleanup can remove another actor's replacement empty
directory, and ordinary hardlink execution can publish a replacement source
inode in the check-to-link race. A separate non-Linux fallback compile defect
is recorded as deferred because v0.0.1 image acceptance is Linux-only.

## Review identity and scope

- Reviewer: `/root/d02_reviewer`, independent of product author
  `/root/x05_implementer`.
- Prior reviewed product:
  `1733a0998e86bf4c71250fab11e17b41857bbc85`.
- Reviewed correction product:
  `3e8f2f52a27b95c4f2cb4f6fd7abbf22496fe9bb`; direct parent
  `149ec74d674d542a93acfcf900bb7344b64675d2`; tree
  `bd61f1e811540bfdd9fb29713a310c4a0f07f3c3`.
- Reviewed handoff:
  `408f0707cff2d753811ae4c879487f8973c52836`; direct parent is the reviewed
  correction product; tree
  `737cc63c07672f2d48174a0e1484c0e47cbbeec4`.
- Scoped correction diff SHA-256, from the prior reviewed product through
  `internal/filesystem/placement/` only:
  `6eb04fce1fa14ba51d295ebd06d4ffff94d09aab19912473af4e6cfaa3e64465`.
- Acceptance reviewed: A-18, A-19, A-20, A-22 and A-59.
- Review receipt checkpoint: commit containing this file; exact SHA is
  reported to the coordinator after commit because a Git commit cannot embed
  its own SHA.

Review ran from clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/mastarr-f04-review-round2` at the exact
product SHA. Adversarial Linux probes ran in separate reviewer worktree
`/Users/guilhermecastro/.codex/worktrees/mastarr-f04-probes-round2`; probe-only
changes are not part of the reviewed product or receipt. Reviewer changed no
product, discovery, state, task, generated, adapter or shared fixture path.

## Current blockers

### P1: successful copy leaves an unreported staging hardlink

`copyOne` creates and identifies the exclusive staging object
(`placement.go:632-641`). Its cleanup closure only closes the descriptor and
explicitly leaves the staging pathname for a future janitor
(`placement.go:642-651`). Descriptor-bound publication creates the final
hardlink (`placement.go:682`), but the success path only closes the stage,
synchronizes the directory, verifies the final path and reports `applied`
(`placement.go:704-718`). No code removes the operation-owned stage entry or
records a pending stage cleanup effect. Repository search finds no staging
janitor or durable staging ownership implementation outside this comment.

Independent Linux probe performed one successful copy and inspected both
paths. It failed with:

```text
successful copy left stage alias: path=.../Movies/.mastarr-stage-review-stage-0.part same_object=true
```

The result was identical in three race-detector repetitions. Mastarr therefore
reports and journals one destination while leaving a second hidden directory
entry to the same inode. This is an unreported filesystem effect and prevents
A-59 from resolving publication without a duplicate pathname. A deterministic
name containing operation ID and ordinal makes the entry locatable, but does
not prove ownership after another actor can replace that name.

Required change: give staging a durable ownership and cleanup lifecycle that
preserves the correction's replacement safety. Do not return `applied` while
an untracked stage alias remains. Any janitor handoff must persist enough
identity to distinguish the operation-created object from a later replacement.

Disposition: `current_blocker`.

### P1: created-directory cleanup deletes an unowned replacement

Directory creation records only root ID and relative pathname
(`placement.go:311-314`). When directory preparation fails, `runCopy` passes
those names to `cleanupDirectories` (`placement.go:337-346`), which calls
`removeEmptyDirectory` without retained object identity
(`placement.go:1245-1252`). On Linux this becomes a pathname-based
`unlinkat(..., AT_REMOVEDIR)` (`fs_unix.go:284-293`).

Independent Linux scheduling probe used `SyncDirectory` after Mastarr created
the selected destination directory. The probe removed that directory, created
another actor's empty directory at the same pathname, and returned a synthetic
sync failure. Mastarr's failure cleanup then removed the replacement:

```text
replacement directory was deleted by cleanup
```

The result repeated in all three race-detector runs. This is the same ownership
class as the round-one stage cleanup defect: a path recorded at creation does
not authorize a later unlink after substitution.

Required change: bind every cleanup candidate to the directory object created
by this operation and use a cleanup primitive that cannot remove a substituted
entry, or preserve the identifiable directory for explicit recovery. Do not
perform name-only best-effort cleanup after an uncertain sync or cancellation.

Disposition: `current_blocker`.

### P1: hardlink execution can publish a replacement source inode

`hardlinkOne` opens and validates the approved source, then rechecks its path
(`placement.go:725-757`). Publication still calls pathname-based
`linkNoReplace(source.parent, source.name, ...)` (`placement.go:759`;
`fs_unix.go:277-281`). An external rename between the final recheck and
`linkat` makes the kernel link the replacement path object rather than the
opened approved source descriptor. Post-publication identity read-back detects
the mismatch and returns uncertainty (`placement.go:784-790`), but the wrong
destination remains visible.

Independent Linux probe atomically exchanged approved and replacement source
names while issuing an exact approved hardlink. The replacement inode was
published after 2 attempts in an ordinary run. Race-detector repetitions
reproduced it after 18, 34 and 41 attempts. A separate evidence run observed:

```text
hardlink published replacement source after 147 attempts:
effect={Outcome:"" Affected:[]} error=filesystem hardlink identity verification failed
```

Detection after mutation is not immutable-plan enforcement. It leaves an
unapproved object at the approved destination and cannot reconcile it as the
requested effect.

Required change: publish from the already-open, manifest-validated source
descriptor with a target-specific descriptor-bound no-replace primitive, and
fail closed where that primitive is unavailable. Preserve EXDEV as explicit
unsupported behavior with no copy fallback.

Disposition: `current_blocker`.

## Deferred hardening

### P2: `fs_other.go` does not compile on its selected targets

`publishNoReplace` receives `stage *os.File` and redeclares `stage :=` as a
pathname (`fs_other.go:199-204`). Windows amd64 and FreeBSD amd64 compile probes
both fail with:

```text
fs_other.go:202:8: no new variables on left side of :=
fs_other.go:202:11: cannot use ... string as *os.File value in assignment
fs_other.go:204:20: cannot use stage ... as string value in argument to os.Link
```

The fallback intends to reject placement writes before mutation, so this is a
straight build regression rather than a reachable unsafe write. v0.0.1's
container acceptance A-52 explicitly targets Linux amd64/arm64, both of which
compile and run. Repair the fallback before declaring another OS buildable.

Disposition: `v0.1.0_candidate`; non-blocking for current Linux-only milestone.

## Round-one correction disposition

- Approved-source binding in `ReconcileHardlink`: closed. Current source must
  be regular and match approved identity and size before destination identity
  is accepted; replacement regression passes on Linux.
- Stage-path replacement cleanup: original unowned-file deletion is closed.
  Failure paths no longer unlink the mutable stage pathname. The successful
  alias lifecycle remains a separate blocker above.
- Newly created directory durability: sync ordering is closed. Each successful
  `mkdirat` syncs its containing parent before traversal, final destination
  parent sync remains after file publication, and sync failures are classified
  `ErrPublicationUnknown`. Replacement-safe cleanup remains a separate blocker.
- Handoff identity: closed. Correction handoff records both exact product
  commits and is directly based on the final product.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, scoped changed files and scoped diff hash | Passed. |
| Full correction diff and placement execution/reconciliation call graph | Reviewed independently. |
| Successful-copy stage-alias Linux probe | Failed safety expectation: final and hidden stage paths name the same inode. |
| Directory-substitution plus cleanup Linux probe | Failed safety expectation: replacement empty directory deleted. |
| Concurrent source-name exchange hardlink Linux probe | Failed safety expectation: replacement inode published at approved destination. |
| All three probes with `-race -count=3` | Reproduced each semantic defect; no Go data race reported. |
| `GOWORK=off go test -mod=readonly ./internal/filesystem/placement -count=1` in Linux `golang:1.27` container | Passed. |
| Linux container placement `-race -count=3` and `go vet` | Passed. |
| Host focused test, race and vet | Passed; Darwin write fixtures correctly skip because writes fail closed. |
| Root `GOWORK=off go test -mod=readonly ./...`, `go vet` and module verification | Passed. |
| Linux amd64 and arm64 CGO-free placement compile | Passed. |
| Windows amd64 and FreeBSD amd64 CGO-free placement compile | Failed with `fs_other.go` shadowing/type errors above. |
| Generation, staged generation, API bundle/Vacuum, architecture, planning and lint | Passed; Vacuum 100/100 and planning reports 44 tasks/60 acceptance cases. |
| `./scripts/check-guardrails.sh --ci` | Passed from clean exact-product checkout. |
| Native crash, disk-full and cross-device mount | Not available; no live filesystem or media stack used. |

## Acceptance disposition

- A-18: not accepted for F-04. Library-only copy succeeds with zero Arr work,
  but leaves an unreported stage pathname beside the destination.
- A-19: not accepted. Existing copy/destination comparisons remain sound, but
  hardlink execution can publish a replacement source inode in its final race.
- A-20: not accepted as an enabled action while hardlink publication is unsafe.
  Linux EXDEV/unsupported handling still has no copy fallback; native
  cross-device fault injection remains outstanding.
- A-22: not accepted. No-replace protects an existing destination, but source
  substitution can publish the wrong hardlink and directory cleanup can delete
  an unowned replacement. Staging is identifiable but never completed after a
  successful copy.
- A-59: not accepted. Valid digest/identity read-back remains present, but each
  successful copy retains a duplicate stage pathname and a raced hardlink can
  leave an unapproved destination that read-back cannot resolve as applied.

This receipt does not approve executor wiring, release, deployment or live
filesystem/media mutation. Durable W-02 effect persistence and native
cross-device/disk-full/crash verification remain separate gates after the
current blockers are resolved.
