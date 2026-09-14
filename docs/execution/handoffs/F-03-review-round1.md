# F-03 independent review, round one

## Decision

`changes_requested`.

The scheduler provides a strong initial read-only boundary: one concurrent run
per root, a configurable global runner limit, durable cursor checkpoints,
same-revision restart recovery, cancellation/deadline terminal states,
complete/partial/unknown coverage separation, retained last-complete evidence,
and detached in-memory state. Focused and repository guardrails pass.

Two contract defects block F-03. Scheduled and manual triggers do not share one
coalesced follow-up in all paths, and persisted scan cursors/results are not
bound to the root configuration revision that produced them. The latter can
promote complete absence evidence from an obsolete root revision or resume its
cursor against a newly configured root.

## Review identity and scope

- Reviewer: `/root/d02_reviewer`, independent of product author
  `/root/d02_implementer`.
- Reviewed product:
  `50e5a3a32e2acf975e3c489e4c75839a0d73a87e`; direct parent
  `691fd5a1ab079425cb2491c77320ac3e620a29fa`; tree
  `56b63934170525d23b224e0304dde8ee6802ab69`.
- Reviewed handoff:
  `00aabe31a596bd8933301aa614aa0afc7c166d65`; direct parent
  `c01851a2be131ae266ae8286658125f8954b0c1f`; tree
  `a7477591a60eff34b83183f8ff717556ac5b0856`. The product is an ancestor of
  the handoff; intervening commits are outside F-03.
- Product scope adds only `internal/scanning/scanning.go` and
  `internal/scanning/scanning_test.go`.
- Scoped product diff SHA-256:
  `5b726039e2c7b641aed1002a403da82ef49780e5fac98e5a22a359632be8b01c`.
- Acceptance reviewed: A-02, A-03 and A-53.
- Review receipt checkpoint: commit containing this file; exact SHA is reported
  after commit because a Git commit cannot embed its own SHA.

Review ran from clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/mastarr-f03-review-round1` at the exact
product SHA. Adversarial tests ran in separate reviewer worktree
`/Users/guilhermecastro/.codex/worktrees/mastarr-f03-probes-round1`; probe-only
files are not part of product or receipt. Reviewer changed no product,
filesystem action, state, task, module, generated, adapter, UI or shared fixture
path.

## Findings

### P1: scheduled/manual overlap can produce two follow-ups or lose the retained follow-up

`Trigger` records one `FollowUpPending` bit while a scan is queued or running
(`scanning.go:1041-1051`). A due scheduled tick sees the active scan and simply
continues without consuming the schedule or coalescing it into that bit
(`scanning.go:1143-1152`). After the active scan and manual follow-up finish,
the still-overdue schedule creates a third scan (`scanning.go:1158-1168`). This
violates the system contract to coalesce another trigger into one follow-up
scan, regardless of whether the trigger is manual or scheduled.

Independent deterministic probe:

1. Start one scheduled scan and keep its runner active.
2. Submit a manual trigger, producing the retained follow-up.
3. Advance the fake clock until the next scheduled interval is due and tick
   while the first scan remains active.
4. Complete the active scan and its manual follow-up, then tick again.

Observed result:

```text
scheduled/manual overlap produced 3 scans, want active plus one follow-up
```

The inverse loss exists at runner deadline. Terminal follow-up creation excludes
`StateDeadlineExceeded` (`scanning.go:1640-1651`), so a manual trigger accepted
while a disabled root is running disappears when that run times out:

```text
deadline dropped coalesced manual follow-up: scans=[{State:deadline_exceeded ...}]
```

Required change: represent one pending follow-up shared by manual and scheduled
admission. Consume/advance an interval that becomes due during active work into
that same bounded pending request. Preserve an already accepted follow-up after
a runner deadline; explicit user cancellation may retain its separately defined
escape-hatch semantics. Add mixed-trigger and deadline regressions.

Disposition: `current_blocker`.

### P1: scan cursor and complete evidence are not enforced against configuration revision

Each scan records `ConfigRevision` at admission (`scanning.go:1182-1193`), but
configuration reconciliation replaces the root schedule without handling an
active scan from the previous revision (`scanning.go:939-948`). On completion,
`updateRootAfterTerminal` promotes complete coverage solely through
`CanAssertAbsence` and never compares the scan revision to current root revision
(`scanning.go:1234-1248`, `1640-1647`). `CanAssertAbsence` itself has no revision
input (`scanning.go:179-195`).

Independent probe started a manual scan at `rev-1`, changed the same root to
`rev-2` while it ran, then returned complete aggregate and source coverage. The
obsolete result became the root's current last-complete absence evidence:

```text
old revision promoted complete absence:
root_revision="rev-2" scan_revision="rev-1"
LastCompleteScanID=<old scan> CanAssertAbsence=true
```

Restart recovery has the same defect. `recoverSnapshot` requeues every running
scan without comparing revisions (`scanning.go:1251-1274`), and `startOne`
dispatches it with the current root schedule plus old checkpoint
(`scanning.go:1394-1457`, `1486-1506`). A snapshot containing root `rev-2` and
running scan `rev-1` produced:

```text
old cursor dispatched against changed root:
root_revision="rev-2" checkpoint_revision="rev-1" cursor="old-root-page-4"
```

This can apply an opaque cursor from one configured directory/source topology
to another and turn old complete evidence into false absence. Persisting the
revision string without enforcing it is not revision binding.

Required change: reject or terminally stale any non-terminal scan whose
`ConfigRevision` differs from its root schedule. Never resume that cursor or
promote its terminal coverage as current/last-complete evidence. A revision
change during a run must create or retain one bounded scan under the new
revision. Add live-reconfiguration and restart-mismatch fixtures alongside the
valid same-revision recovery case.

Disposition: `current_blocker`.

## Preserved behavior

- Default minimum interval is thirty seconds; enabled schedules below it are
  rejected, while configuration remains responsible for its documented
  five-minute default.
- One non-terminal scan per root and global `MaxConcurrent` bound are enforced.
  Manual-only success coalesces repeated triggers to one follow-up.
- Progress callbacks durably preserve bounded cursor, observed count, aggregate
  coverage and source coverage. Same-revision waiting/running scans resume from
  their saved checkpoint after restart.
- Failed, cancelled, deadline and incomplete scans cannot assert absence. A
  later partial/unknown result does not replace retained last-complete evidence.
- Snapshot, root, scan, list and `MemoryStore` reads clone nested coverage reason
  slices and timestamp pointers.
- Root retirement preserves scan history and blocks new scheduling. No-op
  schedule reconciliation does not advance durable version.
- Package imports only domain types. Actual scanning remains behind the
  read-only runner seam; no upstream or media-filesystem write exists in scope.

## Unrelated storage race report

The handoff reports that a three-minute full `./internal/...` race invocation
timed out while the storage subtest
`TestRound9TerminalApprovedPurgeStaysFencedUntilExactFinalization/held` was
running. Independent isolation did not reproduce a deadlock or semantic defect:

- the exact `held` subtest passed under `-race` in about 3 seconds including
  package setup;
- all four parent subtests passed under `-race -count=3` in about 21 seconds.

The reported event is an aggregate full-suite timeout/budget observation, not a
validated storage blocker and not an F-03 finding. Full-root race duration may
still need a larger CI timeout or storage-suite optimization before that broad
gate is claimed.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/parent/tree, handoff ancestry/tree, clean status, owned paths and scoped diff hash | Passed. |
| Full scheduler implementation, tests and linked scan/coverage/configuration contracts | Reviewed independently. |
| Mixed scheduled/manual overlap probe | Failed contract: three scans created instead of active plus one follow-up. |
| Pending-manual-follow-up plus deadline probe | Failed contract: accepted follow-up discarded. |
| Live revision change and complete-result probe | Failed safety: obsolete scan promoted as last-complete absence. |
| Restart revision mismatch probe | Failed safety: old cursor dispatched against new root revision. |
| All adversarial probes with `-race -count=3` | Reproduced each semantic defect; no Go data race reported. |
| `GOWORK=off go test -mod=readonly -count=50 -timeout=120s ./internal/scanning` | Passed. |
| `GOWORK=off go test -race -mod=readonly -count=10 -timeout=120s ./internal/scanning` | Passed. |
| Focused vet, formatting and scoped `git diff --check` | Passed. |
| Root `GOWORK=off go test -mod=readonly -count=1 -timeout=180s ./...`, vet and module verification | Passed. |
| Linux amd64/arm64 CGO-free scanning compile | Passed. |
| Isolated storage `held` race subtest and full parent `-race -count=3` | Passed; reported aggregate timeout was not reproduced as a test defect. |
| `./scripts/check-guardrails.sh --ci` | Passed; generation, API/Vacuum 100/100, lint, architecture, tests, vet and all module verification passed. |

## Acceptance disposition

- A-02: not accepted for F-03. Partial/unknown evidence handling passes, but a
  complete result from an obsolete configuration revision can become current
  absence evidence.
- A-03: not accepted. One runner per root and same-revision cursor recovery
  pass, but mixed scheduled/manual admission can create more than one follow-up,
  deadline can drop an accepted follow-up, and changed-revision restart can
  dispatch an invalid cursor.
- A-53: scoped contribution only. Global runner concurrency and per-root active
  queue bounds pass. Full 100k-file/10k-catalog responsiveness, scan-history
  retention/pagination and resource measurements remain later integration and
  verification work; this receipt makes no full A-53 claim.

No live media, credential, private coordinate, upstream mutation, filesystem
action, release or deployment was used. This receipt does not approve the
coordinator-owned SQLite StateStore adapter, production runner or API wiring.
