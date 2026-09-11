# F-01 independent review, round one

## Decision

`changes_requested`.

The exact implementation commit
`68d5508c0cbc455ac22f835a1767b2a973dd160b` has three current blockers.
Pagination cannot finish directories larger than the configured page maximum,
one unsupported child prevents observation of every sibling in its page, and
capability reporting can contradict authoritative root evidence. These defects
block F-02/F-03 discovery and safe filesystem planning.

## Review identity

- Reviewer: `/root/f01_reviewer`, independent of `/root/f01_implementer`.
- Dispatch base: `74d4ebe0727686bbfe0c156721bb40cd940b5195`.
- Reviewed implementation commit:
  `68d5508c0cbc455ac22f835a1767b2a973dd160b`.
- Reviewer worktree: detached reviewer-owned worktree under
  `$CODEX_HOME/worktrees`, recorded here without its host path.
- Git review snapshot:
  `dea8fa6954e12a683b7b70c5d20bdc248e2e37fbc7bd13975ffde5ec1f2c9e1e`.
- Release stage: `pre-v0.1.0`, target `v0.0.1`.
- Current milestone: bounded, read-only, root-confined filesystem observation
  primitives for later discovery and reviewed filesystem actions.
- Review scope: `internal/filesystem/observe/` against C-03 ports,
  `spec-001-media-reconciliation.md`, `configuration.md`,
  `data-and-recovery.md`, `connectors.md`, and A-19/A-21/A-23.
- Boundary receipt: `transcript_not_read`, `no_polling`, `git_native`.

## Findings

### P1: Page maximum also caps cumulative cursor offset

- Location: `internal/filesystem/observe/observe.go:260` and
  `internal/filesystem/observe/observe.go:317`.
- Evidence: `EnumerationMax` is documented and enforced as the largest page,
  but `EnumeratePage` rejects a continuation once its cumulative offset exceeds
  the same value. A temporary reviewer probe used five files, page size 1 and
  maximum 2. It returned three files, then failed with
  `filesystem enumeration limit reached: cursor offset exceeds maximum`.
- Failure mode: any direct-child directory with more than the default 10,000
  entries becomes permanently partial. Repeated calls cannot reach the remaining
  entries, so later discovery can neither show them nor establish complete
  coverage. This also blocks the planned 100k-file capacity journey.
- Contract: F-01 requires bounded enumeration; the HTTP contract makes limits
  page bounds and requires cursors to preserve a stable snapshot; discovery must
  mark incomplete evidence without losing reachable records.
- Required change: keep the per-page maximum independent from continuation
  position. Use a continuation representation that can traverse the full stable
  directory snapshot without dropping or duplicating entries. Do not rely on
  replaying an unspecified directory order unless the supported filesystem
  contract proves that order stable.
- Proof: add a regression with more entries than `EnumerationMax`, multiple
  page sizes, and a complete unique result set. Run it on Linux as well as the
  local platform.
- Disposition: `current_blocker`.

### P1: One unsupported child aborts the whole directory page

- Location: `internal/filesystem/observe/observe.go:430` through
  `internal/filesystem/observe/observe.go:437`.
- Evidence: `readEntries` returns a zero page immediately when one descriptor
  open or `manifestEntry` rejects a symlink or special file. A temporary probe
  containing `movie.mkv` and `link.mkv` returned
  `observe "link.mkv": filesystem symlink is not allowed`; it returned neither
  the valid sibling nor a continuation cursor.
- Failure mode: a single symlink, FIFO, socket or other unsupported entry makes
  the remainder of that directory unreachable. F-02 cannot present the
  unsupported path with a structured reason and cannot continue to valid media.
- Contract: the discovery specification says not to follow symlinks and to
  present unsupported entries with a reason. A-21 requires rejection and
  confinement, not loss of unrelated observations.
- Required change: preserve descriptor-relative no-follow rejection per child,
  advance past the rejected child, and return structured path-scoped unsupported
  evidence while retaining valid siblings and continuation. If the frozen port
  cannot represent that evidence, coordinate the smallest contract correction
  before F-02 consumes it.
- Proof: add a mixed-directory regression for regular files, symlinks and a
  special file. Assert valid siblings appear once, unsupported children carry
  reasons, no target is followed, and pagination completes.
- Disposition: `current_blocker`.

### P1: Capability output discards configured and read-only authority

- Location: `internal/filesystem/observe/observe.go:53` through
  `internal/filesystem/observe/observe.go:58`,
  `internal/filesystem/observe/observe.go:329` through
  `internal/filesystem/observe/observe.go:373`.
- Evidence: `NewFromStorageRoots` copies `StorageRoot.Capabilities`, but
  `Capabilities` never reads `root.Capabilities`. A temporary probe supplied
  `fs.copy=unsupported` on a writable root and received `supported`. The same
  probe supplied `ReadOnly=true`; `fs.hardlink`, `fs.move`, and `fs.rename`
  returned `unknown` because lines 367-369 overwrite the earlier authoritative
  `unsupported` state.
- Failure mode: a tested or configured unsupported operation can be promoted to
  supported from a permission check alone. Read-only roots present contradictory
  states to the API/UI and action planner.
- Contract: connectors must report `supported`, `unsupported`, or `unknown` from
  evidence rather than a guessed boolean. Filesystem actions must honor root
  read-only state, permissions, mount capability, and action-time evidence.
- Required change: define and enforce evidence precedence. `ReadOnly=true` must
  keep every mutation unsupported. Existing unsupported or unknown capability
  evidence must not become supported without a fresh, sufficient probe. Keep
  source/destination-dependent operations unknown only when no stronger
  prohibition applies.
- Proof: add table tests covering read-only, missing, inaccessible, explicit
  unsupported/unknown, writable, and source/destination-dependent roots.
- Disposition: `current_blocker`.

## Non-blocking hardening register

- `post_v0.1.0`: before supporting filesystem reads on platforms outside Linux
  and Darwin, replace `open_other.go` check-then-open confinement or keep reads
  disabled. Its `classifyOtherOpenError` default returns raw `os.PathError`
  values, which can include configured host paths. Compile portability alone
  does not prove safe runtime confinement or private path redaction.
- `v0.1.0_candidate`: context checks surround hash and enumeration loops, but a
  blocked regular-file or directory syscall cannot observe cancellation. Exercise
  deadline behavior on each supported mounted filesystem and document the
  process-recovery limit when the kernel cannot interrupt an I/O request.
- `v0.1.0_candidate`: stat-before/stat-after hashing detects ordinary identity,
  size and mtime changes. Compatibility tests must still establish timestamp and
  inode behavior on supported network filesystems before that evidence is used
  as a stable mutation guarantee.

## Checks and direct results

All executable checks ran in the clean reviewer-owned worktree at the exact
implementation commit. Temporary negative probes were removed before the final
snapshot.

| Command or inspection | Result |
| --- | --- |
| Git snapshot before and after review | Passed; identity remained `dea8fa6954e12a683b7b70c5d20bdc248e2e37fbc7bd13975ffde5ec1f2c9e1e`, with no staged, unstaged or untracked files. |
| Full diff and every F-01 source/test file | Inspected against dispatch base; eight added files, 1,521 lines. |
| `GOWORK=off go test ./internal/filesystem/observe -count=1` | Passed. |
| `GOWORK=off go test -race ./internal/filesystem/observe -count=1` | Passed on Darwin arm64. |
| `GOWORK=off go vet ./internal/filesystem/observe` | Passed. |
| Temporary `TestReviewerProbe*` negative checks | Failed as expected, reproducing all three findings; probe file removed. |
| Linux amd64 and arm64 `go test -c` with `CGO_ENABLED=0` | Passed. Linux runtime and symlink race execution were not run in this review environment. |
| Windows amd64 and AIX ppc64 `go test -c` with `CGO_ENABLED=0` | Passed through the fallback implementation. |
| `GOWORK=off go mod verify` | Passed. |
| `./scripts/generate.sh --check` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, links resolve. |
| `git diff --check 74d4ebe..68d5508 -- internal/filesystem/observe` | Passed. |
| Public path, private IPv4, credential and secret scan of F-01 paths | Passed. |
| `GOWORK=off go mod tidy -diff` | Failed. `x/sys` is now imported directly but remains indirect. Other root-module dependency changes belong to concurrent coordinator-owned lanes; promote `x/sys` during integration without folding unrelated cleanup into F-01. |

## Acceptance contribution assessment

- A-19: observation contribution passes focused local tests. Same-size different
  bytes produce different SHA-256 values, and identical bytes in distinct files
  retain distinct Unix identities. Copy/hardlink action predicates remain later
  F-04 work.
- A-21: traversal, root target, symlink and special-file observations fail closed,
  and Darwin descriptor-relative race checks pass. Contribution remains open
  because unsupported entries block unrelated observations and Linux runtime
  proof has not run.
- A-23: replacement of the enumerated directory invalidates a cursor in the
  local test. Contribution remains open because pagination cannot traverse a
  directory beyond the page maximum and exact recursive action manifests remain
  downstream work.

## Next review event

Revise the same implementation target, add the focused negative proofs above,
run the Linux descriptor-relative race test, freeze the new commit, then send
the exact base/head identity for round two. Coordinator must keep concurrent
D-01 storage edits outside the F-01 correction.
