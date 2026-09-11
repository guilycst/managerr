# F-01 independent review, round two

## Decision

`changes_requested`.

The exact correction commit
`31572c442e81d3f3fa17e2835fb6d1232106b503` resolves the three round-one
reproductions. Sorted-name cursors traverse beyond `EnumerationMax`, unsupported
children no longer abort valid siblings, and configured/read-only capability
authority is preserved. Three current blockers remain: the frozen filesystem
port cannot accept the returned cursor, page size does not bound directory work
or unsupported evidence, and partial evidence can become complete on the final
page of the same snapshot.

## Review identity

- Reviewer: `/root/f01_reviewer`, independent of `/root/f01_implementer`.
- Dispatch base: `74d4ebe0727686bbfe0c156721bb40cd940b5195`.
- Round-one implementation: `68d5508c0cbc455ac22f835a1767b2a973dd160b`.
- Round-one receipt: `e8c58feb59418b7237759671a039150039e1a79b`.
- Reviewed correction commit:
  `31572c442e81d3f3fa17e2835fb6d1232106b503`.
- Reviewer worktree: detached reviewer-owned worktree under
  `$CODEX_HOME/worktrees`, recorded without its host path.
- Scoped Git snapshot before and after review:
  `f3e0031f32c6bd974221a3bbe8cede2c13e6ec69c35cb3e6597c17cfe18a8b57`.
- Release stage: `pre-v0.1.0`, target `v0.0.1`.
- Review scope: the eight files in `internal/filesystem/observe/`, the exact
  correction diff from round one, frozen C-03 `FilesystemReadPort`, the F-01
  plan, filesystem/discovery specifications, and A-19/A-21/A-23.
- Boundary receipt: `transcript_not_read`, `no_polling`, `git_native`.

## Round-one finding disposition

### Resolved: cursors traverse beyond the page maximum

`TestObserveEnumerationCursorTraversesBeyondPageMaximum` covers five sorted
synthetic files with `EnumerationMax=2`, initial page sizes 1 and 2, and changing
continuation sizes. Ten repeated runs returned all five paths exactly once. The
cursor now binds directory identity, mtime, prefix, a digest of sorted child
names, and the last scanned name. Directory replacement and name-snapshot
changes return `ErrChanged`.

### Resolved: unsupported children do not abort valid siblings

`TestObserveEnumerationSkipsUnsupportedChildren` covers regular files, a
symlink, FIFO and Unix socket. Ten repeated runs returned both valid files once,
never followed the unsupported objects, preserved `symlink` or `special_file`
as parseable root-relative evidence, and reached the end of pagination.
Non-Unix fallbacks now reject special file modes before opening them.

### Resolved: configured and read-only capabilities keep authority

`TestObserveCapabilitiesHonorConfiguredAuthority` covers writable,
source/destination-dependent, configured unsupported/unknown, and read-only
roots. Ten repeated runs preserved configured prohibitions and unknown states;
read-only kept every mutation unsupported; source/destination operations became
unknown only without a stronger prohibition.

## Findings

### P1: Continuation is unreachable through the frozen filesystem port

- Location: `internal/ports/ports.go:270` through `internal/ports/ports.go:275`;
  `internal/filesystem/observe/observe.go:227` through
  `internal/filesystem/observe/observe.go:239`.
- Evidence: the shared `ports.Page` returns `NextCursor`, and the concrete
  observer implements `EnumeratePage`, but `FilesystemReadPort` exposes only
  `Enumerate(rootID, relativePrefix, limit)`. Every call through the frozen
  hexagonal port starts with an empty cursor. There is no typed operation by
  which F-02/F-03 can fetch page two.
- Failure mode: a directory larger than its page size remains unreachable to
  the discovery consumer even though direct concrete-adapter tests can traverse
  it. Unsupported evidence or valid media after page one cannot be reconciled.
- Contract: C-03 says downstream consumers use typed ports; F-01 requires
  bounded enumeration, and its handoff promises opaque continuation cursors.
  F-02 depends on this port for discovery, while missing evidence must remain
  unknown rather than silently absent.
- Required change: coordinate the smallest C-03 contract correction so the
  port accepts an opaque cursor, then compile and test a consumer using only
  `ports.FilesystemReadPort` across more entries than one page.
- Disposition: `current_blocker`.

### P1: A page limit does not bound directory work, memory, or evidence size

- Location: `internal/filesystem/observe/observe.go:284` through
  `internal/filesystem/observe/observe.go:301`, and
  `internal/filesystem/observe/observe.go:552` through
  `internal/filesystem/observe/observe.go:571`.
- Evidence: every page calls `Readdirnames(-1)`, retains and sorts every child
  name, and hashes the full list. `readEntries` stops only after `limit` valid
  manifests, so unsupported children do not count toward the bound and each
  adds a reason string. A temporary reviewer probe created five symlinks and
  one valid file, then requested limit 1. The first response scanned all six
  children and returned one item plus five unsupported records, with no cursor.
- Failure mode: a directory with a large name set allocates and sorts the whole
  set on every request. A directory dominated by unsupported children also
  produces an unbounded reason array and descriptor-open work in one response.
  The advertised page maximum therefore limits valid items only.
- Contract: F-01 explicitly requires bounded enumeration; `ports.Page` is the
  bounded result contract. The HTTP specification requires bounded exact
  manifests, and A-53 later measures bounded memory and page sizes at 100k
  files. This implementation prevents that later slice from satisfying the
  existing boundary without redesign.
- Required change: define one bound over all inspected/emitted child records per
  call, including unsupported evidence, and avoid materializing the full
  directory on each page. Preserve deterministic no-gap/no-duplicate traversal
  and changed-snapshot rejection within that bound.
- Proof: add a regression where unsupported children exceed the requested
  limit and assert response evidence, opened children, and continuation remain
  bounded. Add a large synthetic-directory memory/work check.
- Disposition: `current_blocker`.

### P1: Pagination loses earlier partial evidence and changes snapshot identity

- Location: `internal/filesystem/observe/observe.go:241`,
  `internal/filesystem/observe/observe.go:305` through
  `internal/filesystem/observe/observe.go:329`, and
  `internal/filesystem/observe/observe.go:467` through
  `internal/filesystem/observe/observe.go:478`.
- Evidence: every page generates a new `SourceID`, `StartedAt`, count, reasons
  and completeness. The cursor carries no coverage state. A temporary reviewer
  probe sorted one symlink before two valid files and requested limit 1. Page
  one was partial and carried the symlink reason; the final page returned
  `complete`, omitted the reason, and had a different source ID.
- Failure mode: retaining only the completed page states that the source
  snapshot is complete despite a child that could not be observed. Downstream
  absence reasoning can therefore replace missing evidence with false absence.
  Different source IDs also prevent the page sequence from identifying itself
  as one snapshot.
- Contract: coverage is the scope and quality of one observation snapshot;
  missing evidence is unknown, never untracked. F-03 must record complete,
  partial, or unknown source coverage and must not replace complete evidence
  with false absence.
- Required change: bind one source ID/start time and accumulated partial state
  to the continuation sequence, or make a documented consumer-owned aggregate
  contract that cannot emit source-level complete until all pages and reasons
  are combined. Test an unsupported early page followed by a clean final page.
- Disposition: `current_blocker`.

## Module and hardening register

- `must_correct_before_integration`: `GOWORK=off go mod tidy -diff` remains
  non-clean. `golang.org/x/sys` is imported by F-01 but is still marked
  indirect. The command also reflects current root-module ownership changes,
  including removal of unused `oapi-codegen/runtime` and additions needed by
  tests/tooling. Coordinator must integrate module ownership deliberately and
  rerun tidy without folding unrelated dependency changes into this lane.
- `post_v0.1.0`: before enabling filesystem reads outside Linux and Darwin,
  replace the fallback check-then-open confinement or keep no-follow operations
  unavailable. Compile portability does not prove runtime confinement.
- `v0.1.0_candidate`: context checks surround loops, but blocking filesystem
  syscalls cannot observe cancellation. Exercise deadline behavior on each
  supported mounted filesystem and document kernel I/O recovery limits.
- `v0.1.0_candidate`: stat-before/stat-after hashing detects ordinary identity,
  mode, size and mtime changes. Verify timestamp and inode semantics on each
  supported network filesystem before treating that evidence as a mutation
  guarantee.

## Checks and direct results

All write-producing checks ran in the clean reviewer-owned worktree at the
exact correction commit. Temporary negative probes were removed before the
final snapshot.

| Command or inspection | Result |
| --- | --- |
| Scoped Git snapshot before and after review | Passed; identity remained `f3e0031f32c6bd974221a3bbe8cede2c13e6ec69c35cb3e6597c17cfe18a8b57`, with no staged, unstaged, or untracked files. |
| Exact correction diff `68d5508..31572c4` and all F-01 source/test files | Inspected; five files changed, 420 insertions and 68 deletions. |
| Round-one regression trio, `-count=10` | Passed. |
| Temporary bounded-evidence and final-coverage probes | Failed as described in the two pagination findings; probe file removed. |
| `GOWORK=off go test ./internal/filesystem/observe -count=1` | Passed. |
| `GOWORK=off go test -race ./internal/filesystem/observe -count=1` | Passed on Darwin arm64. |
| `GOWORK=off go vet ./internal/filesystem/observe` | Passed. |
| `GOWORK=off go test ./... -count=1` | Passed in the exact clean correction worktree. |
| `GOWORK=off go vet ./...` | Passed. |
| Linux amd64 and arm64 `go test -c`, `CGO_ENABLED=0` | Passed. Linux runtime and the symlink race did not execute in this environment. |
| Windows amd64 and AIX ppc64 `go test -c`, `CGO_ENABLED=0` | Passed through the fallback implementation. |
| `GOWORK=off go mod verify` | Passed. |
| `./scripts/generate.sh --check` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, links resolve. |
| `git diff --check 74d4ebe..31572c4 -- internal/filesystem/observe` | Passed. |
| Public path, private IPv4, credential-assignment, and tracker URL scan of F-01 diff | Passed with zero matches. API-shaped values contain root-relative paths; configured host paths remain internal constructor data. |
| `GOWORK=off go mod tidy -diff` | Failed with the module differences recorded above. |

## Acceptance contribution assessment

- A-19: local observation contribution still passes. Same-size different bytes
  produce different SHA-256 values, while identical bytes in distinct files
  retain distinct Unix identities. Copy/hardlink predicates remain F-04 work.
- A-21: traversal, root targets, symlinks and special objects fail closed;
  descriptor-relative Darwin race checks pass. Unsupported objects are now
  individually represented, but bounded evidence and Linux runtime proof remain
  open.
- A-23: directory identity, timestamp and sorted-name digest reject ordinary
  continuation changes. The exact recursive action-manifest gate remains later
  work; the port and coverage defects prevent complete discovery traversal.

## Next review event

Correct the three pagination boundaries, freeze a new implementation and any
coordinator-owned port/module commit, then provide exact base/head identity for
round three. Required regressions must consume only `FilesystemReadPort`, bound
unsupported-heavy pages, and retain partial coverage from an early page through
completion. No producer transcript polling occurred during this review.
