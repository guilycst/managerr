# F-05 independent review, round four

## Decision

`approved` for the correction.

No correctness finding remains from round three. Pending move, rename,
permanent-delete, and cross-device copy/verify/delete requests now fail with
`ErrUnsupported` before any quarantine, destination, guard, publication, or
source mutation. The nested payload and private-directory exchange probes
preserve both the approved inode and the replacement inode. Already-materialized
move, delete, and cross-device plans still reconcile as successful no-ops.

This approval accepts the fail-closed safety correction. It does not claim that
F-05 mutation acceptance is available. A-21, A-23, A-24, and A-59 remain
capability-blocked for move, permanent delete, and cross-device source removal
until a reviewed inode-bound quarantine removal primitive exists.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Prior correction product:
  `ffc11d8f4acf6267a763c060bac701004b805dbe`.
- Round-three review receipt:
  `7235e042302702abb3aeebd92ffb15dbd28577b1`.
- Reviewed correction product:
  `3be99364621311c4edc31a2571c095acfb5c1ed4`; tree
  `10a712da5cd34b10f845338f99b8bee68157b75e`.
- Reviewed handoff:
  `0cb30eb2421bdb62adea7f2ea878e3fef577b3b8`; tree
  `913066540ece66773af350a0267fe8b179757144`; direct parent is the exact
  product.
- Product commit changes only `internal/filesystem/organize/fs_linux.go`,
  `fs_unsupported.go`, `organize.go`, `organize_test.go`, and
  `quarantine_linux_test.go`.
- The handoff changes only
  `docs/execution/handoffs/F-05-correction-round4.md`; it contains no later
  product change.
- Scoped product diff SHA-256 from the prior product:
  `293b53b84f086c697e522765f51ce543f091ebc9bcd48d2961d0fbbd6b7a5e9a`.
- Acceptance reviewed: A-21, A-23, A-24, and A-59.
- Review receipt checkpoint: the commit containing this file; its exact SHA is
  reported after commit because a Git commit cannot contain its own SHA.

Review ran from clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-f05-review-r4` at the exact
handoff. Independent adversarial tests ran only in an archive copy of the exact
product under `/tmp`; they are not product changes. Reviewer changed no
product, task, state, fixture, generated, adapter, client, module, or shared
worktree path.

## Correction review

### Pending operations stop before mutation

`moveWithOperation` and `deleteWithProtection` first build read-only plans, then
call `requireQuarantineCleanup` for every pending item before entering their
effect loops. `CopyVerifyDeleteWithOperation` performs its read-only source or
destination reconciliation, then calls the same gate before placement copy.
The Linux implementation reports that no reviewed conditional removal syscall
can bind an unlink to the already-open inode. Non-Linux implementations expose
the same fail-closed contract.

The public synthetic fixture proves pending move and permanent delete leave the
source bytes intact, create no destination, and create no `.mastarr-*`
quarantine. An independent Linux cross-device probe additionally proved a
pending composition creates neither its destination directory nor a private
destination guard before returning `ErrUnsupported`.

The lower-level `moveOwned` and `deleteOwnedNode` seams repeat the capability
gate before quarantine creation. This is defense in depth if a future caller
bypasses the current public preflight.

### Nested exchanges perform no removal

`removeOwnedQuarantineWithHook` and `removePrivateDirectoryWithHook` retain the
last identity check and deterministic exchange seam, but return unsupported or
uncertain without calling `unlinkat`. Twenty race-instrumented repetitions of
the three Linux regressions passed:

- permanent-delete payload exchange preserved the approved spare and the
  unapproved replacement;
- move payload exchange preserved both inodes and their distinct bytes;
- private quarantine-directory exchange preserved both the approved directory
  and its replacement.

This closes the round-three validate-close-name-unlink finding. No product path
can reach these helpers for a pending public operation while cleanup support is
false.

### Idempotent read-back remains available

The cleanup gate is evaluated only after planning identifies a pending effect.
The supplied move fixture and independent delete and cross-device probes prove
that an absent approved source with the matching materialized state returns
`OutcomeAlreadySatisfied` without requiring cleanup support or issuing a
write.

### Prior copy protection and retry logic

The independently accepted byte-copy destination guard and partial-guard
rollback implementations remain unchanged except that the public composition
now stops before reaching them when source removal is unavailable. Their prior
round-three evidence therefore remains applicable to the unchanged code.

On the exact round-four Linux candidate, the mutation tests for destination
protection and guard retry correctly report `SKIP` at
`requireOrganizeCleanup`; they were not counted as newly executed mutation
acceptance. The full suite and cross-build prove that the retained code compiles
and does not regress unrelated paths. Pending cross-device behavior was
independently exercised at the new gate instead.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, commit-owned paths, and scoped diff | Passed. |
| Linux supplied fail-closed and three nested-exchange fixtures under `-race -count=20` | Passed. |
| Independent Linux pending cross-device no-mutation probe under `-race -count=20` | Passed. |
| Independent already-materialized delete and cross-device probes under `-race -count=20` | Passed. |
| Supplied already-materialized move fixture under `-race -count=10` | Passed. |
| Linux `GOWORK=off go test -mod=readonly -race ./internal/filesystem/organize -count=5 -timeout=180s` | Passed. |
| Host focused organize test and vet | Passed. |
| Root `GOWORK=off go test -mod=readonly ./... -count=1 -timeout=180s` | Passed. |
| Linux amd64/arm64, Darwin amd64/arm64, Windows amd64, and FreeBSD amd64 CGO-free organize test compilation | Passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 44 tasks, 60 acceptance cases, local links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed: reproducible generation, Vacuum 100/100, architecture, lint, tests, vet, and all module verification. |
| Product and handoff `git diff --check` and no product delta after the product commit | Passed. |
| Prior destination-copy and guard-retry mutation cases on this Linux candidate | Intentionally skipped by the cleanup capability gate; prior accepted code is unchanged. |
| Live media, private service coordinates, credentials, and production mounts | Not used. |

## Disposition

The round-three P1 is closed. The safe behavior for the current target is an
explicit unsupported result before mutation, with read-only reconciliation
still available. F-05 should remain recorded as capability-blocked rather than
complete for mutation acceptance.

This receipt does not approve workflow wiring, linked-client actions, release,
deployment, or live filesystem/media mutation.
