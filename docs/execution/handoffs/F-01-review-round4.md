# F-01 independent review, round four

## Decision

`changes_requested`.

The exact worker correction
`81f4a89918f1bffb365e9c98a36aa900284da400`, coordinator evidence boundary
`a07fbbb06b8ba9ab6318a16ea56e23e9e95e40cf`, and coordinator module
classification `f3b67a2` resolve four round-three blockers. Successful cursor
generations are single-use, coverage counts remain cumulative through exact
page-size multiples, unsupported-child evidence is consumable through
`ports`, and `golang.org/x/sys` is a direct root dependency.

Cancellation still has a concurrent invalidation race. A same-token caller
already queued behind the active continuation can acquire the cursor state
between the canceling call's state unlock and map deletion. It then advances
past entries consumed by the canceled call, returns success, and receives a
next cursor that the canceling call deletes. This leaves both an evidence gap
and an unusable continuation.

## Review identity

- Reviewer: `/root/f01_reviewer`, independent of `/root/f01_implementer`.
- Original task base: `74d4ebe0727686bbfe0c156721bb40cd940b5195`.
- Round-three receipt: `e291734795741ed1c86af05f3d8cd6b5e2c39ee6`.
- Coordinator evidence correction:
  `a07fbbb06b8ba9ab6318a16ea56e23e9e95e40cf`.
- Coordinator direct-dependency correction:
  `f3b67a237ff9604e821bfb1972a6ec5d74dcc609`.
- Reviewed worker correction:
  `81f4a89918f1bffb365e9c98a36aa900284da400`.
- Worker handoff receipt:
  `2c0f04a72549097733f5cae4d1be7ba09ad91a41`.
- Reviewer worktree: clean detached reviewer-owned worktree under
  `$CODEX_HOME/worktrees`, recorded without its host path.
- Scoped Git snapshot before and after review:
  `0f5d9d4435726f2e00c3de011d0231ef866cb4e4adffb9957e120e80fef2eaba`.
- Snapshot scope: `internal/filesystem/observe/`, `internal/ports/`, `go.mod`,
  and `go.sum`.
- Release stage: `pre-v0.1.0`, target `v0.0.1`.
- Boundary receipt: `transcript_not_read`, `no_polling`, `git_native`.

## Round-three finding disposition

### Resolved: successful replay and concurrent replay are stale

The retained state and opaque token now bind a generation. A successful
continuation increments the state generation while holding `state.mu`; a replay
of the consumed token returns `ErrEnumerationStale`. The production replay and
concurrent-replay tests passed forty repeated runs. Exactly one of two
simultaneous successful-context calls consumes the generation, and traversal
from its returned cursor completes without gaps or duplicates.

### Partially resolved: sequential cancellation invalidates the cursor

The continuation path marks state for deletion after read, context, stat, or
directory-change errors. Existing deterministic tests cancel before and after
a child read, prove the old cursor becomes stale, and prove a clean restart
finds every entry. Those sequential tests passed forty repeated runs. The map
deletion is not atomic with releasing the active state lock, leaving the
concurrent race below.

### Resolved: coverage count is cumulative

Retained state carries `observedCount`, the cursor binds it, and each page adds
only valid manifests. The table test covers one through five files at page size
two, including exact multiples that require an empty terminal page. Every page
reports the cumulative count and terminal coverage reports the complete count.

### Resolved: unsupported evidence crosses the ports boundary

`ports.UnsupportedChildEvidence` and
`ports.ParseUnsupportedChildReasonCode` define the consumer-owned bridge. The
adapter emits the stable compact reason, and tests restricted to the public
port parser recover root-relative paths and reasons. Symlink, FIFO, and socket
children are skipped individually; valid siblings and bounded pagination
continue. Unsupported evidence and inspected children remain bounded by the
page limit, and prior partial state survives to the final page.

### Resolved: filesystem dependency is direct

`golang.org/x/sys v0.47.0` is in the direct root `require` block. Linux
dependency listing reaches `golang.org/x/sys/unix`, and the descriptor/no-follow
implementation imports it directly.

## Finding

### P1: a queued continuation survives cancellation invalidation and skips evidence

- Location: `internal/filesystem/observe/cursor_store.go:54` through
  `internal/filesystem/observe/cursor_store.go:75`,
  `internal/filesystem/observe/cursor_store.go:115` through
  `internal/filesystem/observe/cursor_store.go:125`, and
  `internal/filesystem/observe/observe.go:297` through
  `internal/filesystem/observe/observe.go:304`.
- Evidence: `lockEnumerationCursor` takes `cursorMu`, finds the state, and then
  waits for `state.mu`. The active continuation's deferred cleanup first
  releases `state.mu` and only then calls `dropEnumerationCursor`, which must
  reacquire `cursorMu` and `state.mu`. A second continuation can therefore hold
  `cursorMu` while waiting for the active state. When the canceled call unlocks
  the state, the waiter acquires it first. The canceled call did not reach the
  successful generation increment at `observe.go:446` through
  `observe.go:450`, so the waiter's old token still matches and it advances the
  already-consumed descriptor stream. The invalidator then deletes the state
  after that waiter returns its page.
- Reproduction: a temporary white-box reviewer probe locked the retained state,
  queued `EnumeratePage` with the same token until it held `cursorMu`, released
  the state, and invoked the same unlock-then-drop sequence used by canceled
  continuation cleanup. The queued call returned `nil` instead of
  `ErrEnumerationStale`. The probe failed with
  `concurrent waiter survived invalidation: <nil>` and was removed before the
  final snapshot.
- Failure mode: children consumed by the canceled call have no returned page.
  The concurrent waiter begins after those invisible children and reports
  success, while its returned `NextCursor` refers to state subsequently closed
  and removed by the invalidator. A caller can observe missing media despite
  all accepted calls except the explicit cancellation reporting success.
- Contract: F-01 requires bounded continuation without dropped or duplicated
  entries. A-23 requires cursor mutation detection, and incomplete discovery
  cannot prove absence. Cancellation must invalidate an advanced stream rather
  than leave it usable behind an old token.
- Required change: make cursor invalidation atomic with releasing ownership of
  the current generation. Delete the map entry or irrevocably consume/mark the
  generation while the active continuation still owns `state.mu`, using a lock
  order that cannot deadlock with lookup, expiry, and eviction. Every waiter
  holding the canceled generation must observe stale before reading the stream.
- Required proof: queue one or more same-token continuations behind a call that
  cancels after consuming a child. Assert the canceled call returns
  `context.Canceled`, every waiter returns `ErrEnumerationStale`, and a fresh
  enumeration returns all entries exactly once. Run this under `-race` and
  repeat it enough to exercise the lock handoff.
- Disposition: `current_blocker`.

## Module and hardening register

- `must_correct_before_integration`: `GOWORK=off go mod tidy -diff` still fails
  for the root module. It proposes removing unused `oapi-codegen/runtime`,
  adding the test dependency and missing transitive checksums. The direct
  `x/sys` classification itself is corrected. This wider root-module state is
  coordinator-owned and should be made deliberate before integration.
- `must_correct_before_ui_integration`: the current UI module contains no Go
  packages, and its declared future dependencies are therefore removed by
  `go mod tidy -diff`. `go mod verify` passes. This is outside F-01 product
  scope but prevents claiming all modules are tidy.
- `post_v0.1.0`: fallback platforms retain check-then-open confinement and
  report no-follow unknown. Keep filesystem use disabled there until runtime
  confinement is implemented and tested.
- `v0.1.0_candidate`: fixed 32 KiB buffers, the 256-cursor cap, and ten-minute
  idle expiry bound retained resources. Kernel directory and regular-file reads
  remain blocking syscalls; verify cancellation and inode/timestamp behavior on
  each supported mounted filesystem.

## Checks and direct results

All write-producing checks ran in the clean detached reviewer worktree at the
exact candidate. Temporary adversarial code was removed before the final
snapshot.

| Command or inspection | Result |
| --- | --- |
| Scoped Git snapshot before and after review | Passed; identity remained `0f5d9d4435726f2e00c3de011d0231ef866cb4e4adffb9957e120e80fef2eaba`, with no staged, unstaged, or untracked files. |
| Worker `81f4a89`, coordinator `a07fbbb`/`f3b67a2`, handoff, prior receipt, and complete scoped sources | Inspected independently. |
| Replay, concurrent replay, cancellation, cumulative counts, interface pagination, unsupported-child bounds, prior partial, and no-follow race tests, `-count=40` | Passed on Darwin arm64. |
| Identity/hash, traversal/root/symlink/prefix rejection, capability authority/read-only evidence, special-file rejection, and symlink-race tests, `-count=20` | Passed on Darwin arm64. |
| Temporary queued-waiter invalidation probe | Failed as described; probe removed. |
| `GOWORK=off go test ./... -count=1` | Passed all root packages. |
| `GOWORK=off go test -race ./internal/filesystem/observe ./internal/ports -count=1` | Passed on Darwin arm64; the logical cancellation race requires the missing interleaving assertion. |
| `GOWORK=off go vet ./...` | Passed for the root module. |
| Linux amd64/arm64, Windows amd64, and AIX ppc64 `go test -c` with `CGO_ENABLED=0` | Passed. |
| `GOWORK=off go mod verify` | Passed in root, UI, and tools modules. |
| `GOWORK=off go mod tidy -diff` | Failed in root and UI as registered above; passed in tools. |
| UI module test/vet | No Go packages are present; commands report `matched no packages`. |
| Tools module `GOWORK=off go test ./... -count=1` and `go vet ./...` | Passed. |
| `./scripts/generate.sh --check` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, links resolve. |
| Scoped `git diff --check` for worker and both coordinator corrections | Passed. |
| Public path, private IPv4, credential-assignment, and tracker URL scan of scoped additions | Passed with zero matches. API-shaped evidence uses configured IDs and root-relative paths; host paths remain internal. |

No live media service, filesystem mutation, release, deployment, or producer
transcript polling was used during this review.

## Acceptance contribution assessment

- A-19: identity and SHA-256 observation remain locally sound. Same-size byte
  mutation changes the digest, and capability evidence preserves configured
  unsupported/unknown authority plus read-only precedence. Placement conflict
  and already-satisfied predicates remain F-04 work.
- A-21: Darwin descriptor-relative confinement, traversal/root/symlink/special
  rejection, and symlink-race tests pass. Linux amd64/arm64 compile; Linux
  runtime race proof remains open.
- A-23: retained descriptors and directory identity/mtime validation protect
  ordinary continuation from path replacement. Successful generations are
  single-use and cumulative snapshot identity is stable. Concurrent
  cancellation still permits a gap and an immediately stale returned cursor,
  so this contribution is not accepted.
- Downstream discovery: unsupported-child evidence is now typed and consumable
  through `ports`, but F-02/F-03 must not use a canceled scan for absence while
  the queued-waiter race remains.

## Next review event

Make invalidation atomic against queued same-generation continuations and add
the concurrent cancellation regression described above. Provide the exact
correction commit for round five after standard checks pass.
