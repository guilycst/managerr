# F-01 independent review, round three

## Decision

`changes_requested`.

The exact worker correction
`f8773d27d77a67670b8584c26442cb2a4af16fe5` and coordinator port correction
`fd15dbc90933301783f67dc4ba07e9e576d91e73` close the three round-two
reproductions at their immediate boundaries. A `FilesystemReadPort` consumer
can paginate; each call inspects and emits at most its page limit; the retained
descriptor store is capped, expires idle cursors, and rejects cursors after
restart; source identity, start time, and prior-partial state now continue
across pages.

Three correctness boundaries remain open. A consumed cursor can be replayed to
advance the mutable stream again, cancellation retains an already advanced
stream, and completed coverage reports only its final page count. The
unsupported-child decoder also remains owned by the concrete adapter rather
than the frozen port/domain contract. Direct module classification for
`golang.org/x/sys` is still unresolved.

## Review identity

- Reviewer: `/root/f01_reviewer`, independent of `/root/f01_implementer`.
- Dispatch base: `74d4ebe0727686bbfe0c156721bb40cd940b5195`.
- Round-two worker target: `31572c442e81d3f3fa17e2835fb6d1232106b503`.
- Round-two receipt: `43c2aacca1a4c418be383c78d554ed5118cfd6e2`.
- Coordinator port correction:
  `fd15dbc90933301783f67dc4ba07e9e576d91e73`.
- Reviewed worker correction:
  `f8773d27d77a67670b8584c26442cb2a4af16fe5`.
- Reviewer worktree: clean detached reviewer-owned worktree under
  `$CODEX_HOME/worktrees`, recorded without its host path.
- Scoped Git snapshot before and after review:
  `ebb072d9aaae592de1acee68acb7cdf78a389e008f845d03614ab648d84b6425`.
- Snapshot scope: `internal/filesystem/observe/` and
  `internal/ports/ports.go`.
- Release stage: `pre-v0.1.0`, target `v0.0.1`.
- Boundary receipt: `transcript_not_read`, `no_polling`, `git_native`.

## Round-two finding disposition

### Resolved: pagination is reachable through the port

The coordinator added `EnumeratePage` to `ports.FilesystemReadPort`. The worker
keeps its compile-time interface assertion, and
`TestObserveEnumerationPageThroughFilesystemReadPort` traverses four files
through an interface value. Twenty repeated interface runs passed with one
source ID and start time.

### Resolved: one page has bounded directory and evidence work

Linux and Darwin use a retained descriptor stream with one fixed 32 KiB buffer;
fallback platforms use `Readdirnames(1)`. `readEntries` counts every returned
child before classification, so valid items and path-scoped unsupported reasons
together cannot exceed the requested limit. Twenty repeated mixed-entry tests
passed, including twelve symlinks at limit 1. The former fetch-all/sort/hash
path is gone.

### Resolved: descriptor retention has explicit bounds and expiry

A temporary reviewer probe opened 257 unfinished directory enumerations. The
store retained exactly 256 and rejected the evicted cursor. Moving one retained
cursor beyond the ten-minute idle TTL caused `ErrEnumerationCursor` and closed
its state. A new observer rejected an old cursor, confirming documented
process-local restart behavior. Cursors are bound to root ID, prefix, directory
identity/mtime, source ID, start time, and prior-partial state.

### Resolved: prior partial state remains visible at completion

Source ID and start time remain stable across continuation. Once a page reports
an unsupported child, later pages remain partial and include
`unsupported_child_prior`; twenty repeated unsupported-heavy scans reached a
partial terminal page.

## Findings

### P1: A consumed cursor is reusable against an already advanced stream

- Location: `internal/filesystem/observe/observe.go:277` through
  `internal/filesystem/observe/observe.go:303`,
  `internal/filesystem/observe/observe.go:388` through
  `internal/filesystem/observe/observe.go:405`, and cursor fields at
  `internal/filesystem/observe/observe.go:694` through
  `internal/filesystem/observe/observe.go:702`.
- Evidence: cursor state contains no position generation or consumed-token
  marker. For an all-valid scan, page two returns the same opaque cursor value
  it consumed because none of its encoded fields changes. A temporary reviewer
  probe fetched page two with the first cursor, then submitted that first cursor
  again. The second submission succeeded and returned the next stream page.
- Failure mode: an HTTP retry after a lost page response silently skips the
  lost page. Concurrent requests using the same cursor serialize into different
  pages rather than one repeatable result or one explicit stale-cursor error.
  The caller can therefore finish with gaps while every request reports success.
- Contract: the port calls this an opaque cursor from the previous page and
  requires changed snapshots to fail rather than mix evidence. F-01 requires
  no-gap/no-duplicate bounded continuation; A-02 includes interrupted
  pagination, and missing evidence may never become confirmed absence.
- Required change: bind a monotonically advancing generation to both retained
  state and every returned token. Consume each generation at most once. A stale
  retry may return a cached identical page or an explicit cursor error requiring
  a full restart; it must never advance from the stale token.
- Proof: fetch page two, replay page one's cursor, and assert identical replay
  or explicit rejection. Add the same assertion for two concurrent requests.
- Disposition: `current_blocker`.

### P1: Cancellation keeps a stream after discarding consumed entries

- Location: `internal/filesystem/observe/observe.go:368` through
  `internal/filesystem/observe/observe.go:376`, and
  `internal/filesystem/observe/observe.go:648` through
  `internal/filesystem/observe/observe.go:691`.
- Evidence: `readEntries` advances the descriptor while building a page but
  returns a nil item slice when a later context check fails. `EnumeratePage`
  deliberately drops cursor state only when `ctx.Err() == nil`, so a canceled
  continuation retains the advanced stream. A deterministic reviewer context
  canceled after one continuation child had been consumed. The call returned
  `context.Canceled`; reusing the old cursor then succeeded from a later entry.
- Failure mode: interrupted pagination loses every child consumed before the
  cancellation check. The response contains no page or new cursor, while the
  only cursor known to the caller now points beyond unseen media.
- Contract: cancellation must be cooperative and observable between bounded
  chunks. A-02 requires interrupted pagination to remain partial/unknown, and
  the discovery invariant forbids false absence from missing evidence.
- Required change: on any continuation error after stream access, invalidate
  and close the cursor so the caller restarts, or checkpoint/replay the page
  until successful delivery. Never retain an advanced state behind the old
  token.
- Proof: cancel before the first child and after one or more children, then
  prove retry cannot silently skip any child.
- Disposition: `current_blocker`.

### P1: Completed coverage reports only the final page count

- Location: `internal/filesystem/observe/observe.go:378` through
  `internal/filesystem/observe/observe.go:423`, and
  `internal/filesystem/observe/observe.go:569` through
  `internal/filesystem/observe/observe.go:574`.
- Evidence: the retained state carries no observed count. Each page constructs
  coverage from `len(page.Items)`. When an entry count is an exact multiple of
  the page limit, the implementation cannot know it is exhausted without a
  later call, so the terminal response is an empty page. A reviewer probe over
  three files at limit 1 ended with stable source/start identity,
  `Completeness=complete`, `CompletedAt` set, and `ObservedCount=0` rather than
  3.
- Failure mode: the authoritative completed coverage row contradicts the
  records observed under its source ID. A consumer retaining or upserting the
  completed snapshot can report zero observed objects and use that complete
  evidence for absence reasoning.
- Contract: `domain.Coverage` records the scope and quality of one observation
  snapshot, including observed count. Complete, completed coverage is authority
  for an absent tracking observation.
- Required change: retain the cumulative observed count with the source state
  and report it on every continuation or at least on the terminal coverage.
  A bounded one-entry lookahead may avoid the empty terminal page, but cumulative
  coverage must remain correct either way.
- Proof: paginate counts below, equal to, and above the limit, including exact
  multiples and unsupported children; assert the terminal count matches every
  valid manifest in the source snapshot.
- Disposition: `current_blocker`.

### P1: Unsupported-child evidence is not consumable through the port boundary

- Location: adapter-owned encoding and decoder at
  `internal/filesystem/observe/observe.go:596` through
  `internal/filesystem/observe/observe.go:628`; frozen page/port types at
  `internal/ports/ports.go:14` through `internal/ports/ports.go:19` and
  `internal/ports/ports.go:270` through `internal/ports/ports.go:278`.
- Evidence: the port correction exposes pagination but no typed unsupported
  child. The path/reason type, prefix, encoder, and decoder remain inside the
  concrete filesystem adapter. An F-02 consumer restricted to
  `FilesystemReadPort` receives generic reason strings and cannot decode them
  without importing the adapter implementation or duplicating its private wire
  format.
- Failure mode: the next discovery slice either violates the hexagonal boundary
  or treats a path-scoped unsupported object as an opaque global reason. That
  blocks the API/UI requirement to present the specific object and reason.
- Contract: C-03 freezes typed ports for downstream services; adapters implement
  those contracts. The discovery specification requires unsupported entries to
  remain visible with a reason.
- Required change: move the smallest stable path/reason representation and
  decoding contract into `domain` or `ports`, or add a typed bounded collection
  to the page contract. Keep host paths out and retain the generic coverage
  reason if needed for HTTP compatibility.
- Proof: add a consumer test in a package that imports only `domain` and
  `ports`; it must enumerate and recover the unsupported root-relative path and
  reason without importing `internal/filesystem/observe`.
- Disposition: `current_blocker`.

## Module and hardening register

- `must_correct_before_integration`: `golang.org/x/sys v0.47.0` is directly
  imported by the descriptor and no-follow implementations but remains in the
  indirect `go.mod` block. `GOWORK=off go mod tidy -diff` also identifies other
  current root-module ownership changes, including unused
  `oapi-codegen/runtime` and test/tool dependencies. Coordinator must produce a
  clean deliberate root module state and rerun tidy.
- `post_v0.1.0`: fallback platforms retain check-then-open confinement and
  report no-follow unknown. Keep filesystem use disabled there until runtime
  confinement is implemented and tested.
- `v0.1.0_candidate`: fixed 32 KiB buffers and 256 cursors bound retained memory
  and descriptors, but kernel directory and regular-file reads remain blocking
  syscalls. Verify cancellation and timestamp/inode behavior on each supported
  mounted filesystem.

## Checks and direct results

All write-producing checks ran in the clean detached reviewer worktree at the
exact worker commit. Temporary adversarial probes were removed before the final
snapshot.

| Command or inspection | Result |
| --- | --- |
| Scoped Git snapshot before and after review | Passed; identity remained `ebb072d9aaae592de1acee68acb7cdf78a389e008f845d03614ab648d84b6425`, with no staged, unstaged, or untracked files. |
| Coordinator `fd15dbc` and worker `f8773d2` diffs plus complete scoped sources | Inspected; port adds one method, worker changes eight files with 624 insertions and 105 deletions. |
| Interface, per-page bound, prior-partial, and no-follow race tests, `-count=20` | Passed on Darwin arm64. |
| Temporary replay, cancellation, coverage-count, cap, expiry, and restart probes | Cap/expiry/restart passed. Replay, cancellation, and terminal count failed as described; probe file removed. |
| `GOWORK=off go test ./internal/filesystem/observe -count=1` | Passed. |
| `GOWORK=off go test -race ./internal/filesystem/observe -count=1` | Passed on Darwin arm64. |
| `GOWORK=off go vet ./internal/filesystem/observe ./internal/ports` | Passed. |
| `GOWORK=off go test ./... -count=1` | Passed in the exact clean review snapshot. |
| `GOWORK=off go vet ./...` | Passed. |
| Linux amd64/arm64, Windows amd64, and AIX ppc64 `go test -c` with `CGO_ENABLED=0` | Passed. |
| Linux runtime attempt using cached local images | Not run: cached images contained no Go executable. No image was pulled. |
| `GOWORK=off go mod verify` | Passed. |
| `GOWORK=off go mod tidy -diff` | Failed with the root module differences recorded above. |
| `./scripts/generate.sh --check` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, links resolve. |
| `git diff --check 31572c4..f8773d2 -- internal/filesystem/observe internal/ports/ports.go` | Passed. |
| Public path, private IPv4, credential-assignment, and tracker URL scan of scoped diff | Passed with zero matches. Returned evidence uses configured IDs and root-relative paths; host paths remain internal. |

The shared checkout's unrelated D-01 correction files were not read, staged, or
modified for this review.

## Acceptance contribution assessment

- A-19: identity/hash observation contribution remains locally sound. Placement
  conflict and already-satisfied predicates remain F-04 work.
- A-21: Darwin descriptor-relative confinement and symlink-race tests pass;
  special objects remain unfollowed and path-scoped. Linux compiles, but the
  required Linux runtime race proof remains open.
- A-23: directory identity and mtime changes reject continuation, while a
  retained descriptor prevents replay-order gaps from directory ordering.
  Stale-token and cancellation gaps still prevent acceptance.
- A-02/A-53 downstream boundary: pages now have bounded inspected/evidence work
  and process resources, but interruption and terminal coverage semantics remain
  unsafe for F-03 absence decisions.

## Next review event

Correct cursor generation/replay and cancellation behavior, accumulate snapshot
coverage count, expose unsupported-child evidence through the typed boundary,
and clean direct module ownership. Freeze exact worker and coordinator commits,
then provide their identities for round four. No producer transcript polling
occurred during this review.
