# F-01 independent review, round five

## Decision

`approved`.

The exact worker correction
`475292825d63e2092663b7ff4fe4c645fd7c68ef` closes the round-four
queued-continuation race. Cancellation now marks retained cursor state invalid
while the active continuation still owns the state mutex. Every caller already
queued on that mutex observes the marker and returns `ErrEnumerationStale`
before reading the descriptor stream. No blocking finding remains in the F-01
implementation scope.

## Review identity

- Reviewer: `/root/f01_reviewer`, independent of `/root/f01_implementer`.
- Original task base: `74d4ebe0727686bbfe0c156721bb40cd940b5195`.
- Round-four review receipt:
  `26827f0a29a1c91da6ca6cbf920ddcbd64b1185f`.
- Reviewed worker correction:
  `475292825d63e2092663b7ff4fe4c645fd7c68ef`.
- Worker handoff receipt:
  `451964acfc26a3457532c020e983f7637459fa9e`.
- Coordinator evidence boundary:
  `a07fbbb06b8ba9ab6318a16ea56e23e9e95e40cf`.
- Coordinator direct-dependency correction:
  `f3b67a237ff9604e821bfb1972a6ec5d74dcc609`.
- Reviewer worktree: clean detached reviewer-owned worktree under
  `$CODEX_HOME/worktrees`, recorded without its host path.
- Scoped Git snapshot before and after review:
  `2a894ca8aff5657a536969c76f40ca51935da357e36471ecb9bfe8182ac2aa27`.
- Snapshot scope: `internal/filesystem/observe/`, `internal/ports/`, `go.mod`,
  and `go.sum`.
- Release stage: `pre-v0.1.0`, target `v0.0.1`.
- Boundary receipt: `transcript_not_read`, `no_polling`, `git_native`.

## Findings

No open product findings.

## Round-four finding closure

### Queued cancellation is linearized

Resolved. The active continuation owns `state.mu` from lookup through page
completion. Every path marked for state removal sets `state.invalidated` before
releasing that mutex. A same-generation caller that already found the cursor
and is waiting for the mutex checks the marker immediately after acquisition,
before reading state metadata, opening the current path, or advancing the
retained directory stream.

The new regression creates the exact prior failure order. It holds the cursor
state, queues the active continuation, releases it until cancellation blocks
after a child read, then queues a second same-token continuation before allowing
the canceled call to finish. An atomic waiter count makes both lock handoffs
deterministic. The canceled call returns `context.Canceled` without page state;
the queued call and later retry both return `ErrEnumerationStale` without items
or a next cursor.

The regression passed 500 ordinary runs and 100 race-instrumented runs. The
invalidated field is accessed only while `state.mu` is held. Expiry, eviction,
explicit invalidation, successful generation consumption, and final cursor
removal retain the established map-then-state lock order and do not expose an
invalid stream to waiters.

## Preserved behavior

### Cursor integrity, coverage, and bounds

Successful generations remain single-use. Sequential and concurrent replay
return stale for the consumed token, while the winning next cursor completes
without gaps or duplicates. The descriptor-backed cursor is process-local,
capped at 256 retained cursors, expires after ten idle minutes, and is rejected
after eviction or restart.

Each page inspects at most its requested limit. Cumulative valid-manifest count,
source ID, start time, and prior-partial state remain bound to the token and
stable across pages. Counts below, equal to, and above the page size, including
exact multiples with an empty terminal page, remain correct.

### Unsupported entries and port consumption

Symlink, FIFO, and socket children remain unfollowed and are skipped
individually. Valid siblings continue through bounded pagination. Each
unsupported entry carries a root-relative path and reason through
`ports.UnsupportedChildEvidence` and
`ports.ParseUnsupportedChildReasonCode`; the final coverage remains partial
after earlier unsupported evidence.

### Confinement, identity, mapping, and capabilities

Descriptor-relative Unix opens retain `O_NOFOLLOW` component checks. Root
targets, traversal, symlinks, special files, path-prefix ambiguity, and
directory replacement are rejected. File identity and SHA-256 digest change
evidence remains stable. Configured unsupported or unknown capability authority
is preserved, and read-only roots keep every mutation unsupported.

`golang.org/x/sys v0.47.0` remains in the direct root `require` block. Linux
dependency listing reaches `golang.org/x/sys/unix`, matching the descriptor and
no-follow imports.

## Checks and direct results

All executable checks ran in the clean detached reviewer worktree at the exact
candidate. The shared checkout's unrelated D-01 edits were neither read nor
staged.

| Command or inspection | Result |
| --- | --- |
| Scoped Git snapshot before and after review | Passed; identity remained `2a894ca8aff5657a536969c76f40ca51935da357e36471ecb9bfe8182ac2aa27`, with no staged, unstaged, or untracked files. |
| Diff `81f4a89..4752928` and complete cursor-store/enumeration sources | Inspected independently; correction changes three F-01 files with 123 insertions and one deletion. |
| Queued-cancellation regression, `-count=500` | Passed on Darwin arm64. |
| Queued-cancellation regression under `-race`, `-count=100` | Passed on Darwin arm64. |
| Replay, concurrent replay, sequential and queued cancellation, cumulative counts, interface pagination, unsupported-child bounds, prior partial, identity/hash, capability authority, confinement, and no-follow race tests, `-count=50` | Passed on Darwin arm64. |
| `GOWORK=off go test -race ./internal/filesystem/observe ./internal/ports -count=1` | Passed on Darwin arm64. |
| `GOWORK=off go test ./... -count=1` | Passed all root packages. |
| `GOWORK=off go vet ./...` | Passed for the root module. |
| Linux amd64/arm64, Windows amd64, AIX ppc64, and Darwin arm64 `go test -c` with `CGO_ENABLED=0` | Passed. |
| `GOWORK=off go mod verify` | Passed in root, UI, and tools modules. |
| Root `GOWORK=off go mod tidy -diff` | Reports the existing wider module ledger differences: unused `oapi-codegen/runtime`, the test dependency, and missing transitive sums. The round-five worker changes no module file; direct `x/sys` classification is correct. Coordinator retains this integration cleanup. |
| UI module test/vet/tidy | No Go packages are present. Verify passes; tidy proposes removing the frozen future UI pins, as already assigned to U-01 when packages consume them. |
| Tools module test, vet, verify, and tidy | Passed. |
| `./scripts/generate.sh --check` | Passed: generation checks passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, and local links resolve. |
| Scoped `git diff --check` for the correction and complete F-01 contribution | Passed. |
| Public path, private IPv4, credential-assignment, and tracker URL scan of correction and complete scoped additions | Passed with zero matches. Returned evidence uses configured IDs and root-relative paths; host paths remain internal. |

No live media service, filesystem mutation, release, deployment, or producer
transcript polling was used during this review.

## Acceptance contribution assessment

- A-19: F-01 contribution accepted. Identity and SHA-256 observations detect
  changed content, mappings preserve component boundaries, and capability
  evidence retains configured authority and read-only precedence. Placement
  conflict and already-satisfied action predicates remain F-04 work.
- A-21: F-01 contribution accepted for the implemented Unix boundary. Darwin
  runtime tests prove descriptor-relative confinement and synthetic symlink
  races; Linux amd64/arm64 compile. Mounted-filesystem Linux runtime validation
  remains a verification gate before release.
- A-23: F-01 contribution accepted. Directory identity and mtime reject changed
  snapshots; retained descriptor cursors remain bounded; successful generations
  are single-use; cancellation invalidates all queued same-generation callers;
  and cumulative coverage remains internally consistent.
- Downstream discovery: `FilesystemReadPort` consumers can paginate and decode
  bounded unsupported-child evidence without importing the adapter. F-02/F-03
  still own grouping, persistence, scan restart, and absence decisions.

## Integration notes

The round-five handoff contains two stale statements saying `x/sys` is still
indirect and remains coordinator work. Commit `f3b67a2` already made it direct,
and the exact candidate confirms the dependency classification. Correct those
handoff statements while recording final integration so resume documentation
matches Git.

Approval covers the exact F-01 implementation and its bounded acceptance
contributions. Coordinator-owned state integration, root module cleanup, Linux
mounted-filesystem validation, and downstream discovery, storage, API, UI,
release, and deployment gates remain separate. This review changes only this
receipt.
