# F-03 independent review, round two

## Decision

`changes_requested`.

The correction closes the four exact round-one reproductions: mixed
scheduled/manual admission retains one follow-up, runner deadline preserves that
follow-up, an active obsolete revision cannot promote complete evidence, and
restart does not resume its cursor. Focused tests, race detector, root tests,
cross-compilation, planning checks, and full repository guardrails pass.

Revision safety remains optional because an empty configuration revision is
valid. Reconfiguration can also start a fresh same-root runner while the old
revision's cancelled runner is still active when `MaxConcurrent` exceeds one.
A scheduled-only coalesced follow-up is persisted as `manual`, losing trigger
provenance.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/d02_implementer`.
- Round-one receipt:
  `f73e7fd38eb78672a015c8d5a1186019038b0082`.
- Reviewed correction product:
  `ebdf5112c5be852fc992f337d47e4e0589f52b94`; direct parent
  `98c4e835bc03824c2ee95b5a8bdcd1edbf527354`; product tree
  `86569b54d0fa189517d3ef1f656ac8bcff7af2ab`.
- Reviewed correction handoff:
  `a84853ac3bf25886aadc69c625ad7b10f9327801`; handoff tree
  `cee52c4aafc38451d8e15255b6b787384abe4af6`. Product is an ancestor of
  handoff, and intervening commits do not change `internal/scanning/`.
- Product commit changes only `internal/scanning/scanning.go` and
  `internal/scanning/scanning_test.go`. Handoff commit adds only
  `docs/execution/handoffs/F-03-correction-round1.md`.
- Scoped correction diff from original F-03 product
  `50e5a3a32e2acf975e3c489e4c75839a0d73a87e` has SHA-256
  `abb906ed2f1605423e85726033f8368af937bbd522822843065fd77f2286bad0`.
- Acceptance reviewed: A-02, A-03, and A-53.
- Review receipt checkpoint: commit containing this file; exact SHA is reported
  after commit because a Git commit cannot embed its own SHA.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-f03-review-r2` at exact handoff.
Reviewer changed no product, state, task, generated, adapter, filesystem,
storage, module, or shared fixture path. Adversarial probes ran in an archive
copy of exact product tree under `/tmp`; they are not product changes.

## Round-one correction verification

- Scheduled due work now advances `NextScheduledAt` while an active scan exists
  and shares its bounded pending bit. Repeated overdue ticks do not create a
  third scan (`scanning.go:1183-1208`, `scanning.go:1251-1259`).
- Deadline completion now materializes a retained follow-up because only
  explicit cancellation suppresses it (`scanning.go:1806-1855`).
- Live reconfiguration, tick, progress, finish, startup recovery, and final
  dispatch compare scan and root revisions. Obsolete non-terminal records become
  cancelled history; their aggregate complete coverage is downgraded and their
  cursor is not passed to fresh work (`scanning.go:939-968`,
  `scanning.go:1183-1195`, `scanning.go:1313-1388`,
  `scanning.go:1391-1463`, `scanning.go:1566-1583`,
  `scanning.go:1702-1748`, `scanning.go:1756-1780`).
- Root current and last-complete projections are cleared when revision changes,
  while same-revision restart retains its checkpoint.

These observations were made from code and rerun behavior, independently of
handoff conclusions.

## Findings

### P1: empty configuration revision disables all stale-cursor/evidence fencing

`RootSchedule.Validate` rejects only revisions longer than 256 bytes; empty and
whitespace-only values are valid (`scanning.go:118-135`). Every new correction
gate is equality-based. Two different effective root configurations represented
with empty revisions therefore compare equal, so old cursor/progress/evidence can
still resume or become current. `ScanRecord.validate` likewise permits an empty
`ConfigRevision` (`scanning.go:346-412`).

Independent archive-copy probe called `ConfigureRoots` with a valid root ID and
empty revision. Observed:

```text
TestReviewerProbeConfigurationRevisionIsMandatory:
ConfigureRoots accepted an empty configuration revision, disabling
cursor/evidence fencing
```

Revision is the only field supplied to this package that binds opaque cursor and
coverage to the effective root topology. Making it optional leaves round-one
stale-revision defect open for a valid public input and breaks A-02/A-03.

Required change: require a non-empty, canonical configuration revision on every
current root schedule and newly admitted/non-terminal scan. Reject
whitespace-only or noncanonical values. If legacy terminal history may lack
revisions, retain it only as explicitly unbound evidence; terminalize legacy
non-terminal scans and invalidate their current projections. Never silently
treat two missing bindings as equal. Add admission and restart fixtures.

Disposition: `current_blocker`.

### P1: revision replacement can run two scanners for one root

On revision change, `ConfigureRoots` immediately terminalizes old durable record
and appends fresh queued record (`scanning.go:949-965`). After save it cancels old
runner, then calls `pump` (`scanning.go:997-1014`). `startOne` limits only total
entries in `scheduler.running`; it does not reject another runner for same root
(`scanning.go:1529-1539`). With `MaxConcurrent > 1`, fresh runner starts before
old runner returns from its current bounded operation and removes itself from
`scheduler.running`.

Independent archive-copy probe used `MaxConcurrent=2`; old revision runner
received cancellation but remained inside one bounded operation. Before it
returned, fresh revision runner started for same root:

```text
TestReviewerProbeReconfigurationDoesNotOverlapRunnerForSameRoot:
fresh revision runner started while cancelled old runner still active for same
root; checkpoint ConfigRevision=rev-2
```

The old record is terminal in durable state, but old process is still reading.
This violates A-03's one-scan-per-root guarantee and can duplicate expensive
directory/client reads. Progress rejection prevents old evidence publication but
does not enforce runner concurrency.

Required change: retain per-root runtime ownership until old runner exits. Queue
fresh revision durably, but do not dispatch it while any `scheduler.running`
entry owns that root, regardless of global capacity. Add a non-cooperative
bounded-operation fixture with `MaxConcurrent >= 2` proving fresh start occurs
only after old exit.

Disposition: `current_blocker`.

### P2: scheduled-only follow-up is recorded as manual

A due schedule during active work sets only `FollowUpPending`
(`scanning.go:1197-1201`). Terminal handling always constructs pending work with
`TriggerManual` (`scanning.go:1847-1851`). The bit does not retain which source
requested it.

Independent probe used no manual trigger: next interval became due while first
scheduled scan ran. Exactly one follow-up was created, but its persisted trigger
was `manual`:

```text
TestReviewerProbeScheduledFollowUpKeepsScheduledProvenance:
second scan Trigger:manual; want scheduled
```

`TriggerKind` documents why a scan was admitted. False origin weakens scan
history and API/operator explanations, especially for deadline and restart
diagnosis.

Required change: durably retain pending trigger provenance. Scheduled-only work
must remain scheduled; manual participation may be represented by an explicit
precedence rule or a combined reason without growing queue beyond one. Add pure
scheduled, pure manual, and mixed-order fixtures.

Disposition: `current_blocker`.

## Preserved behavior

- One non-terminal durable record per root and global runner limit remain
  validated.
- Manual triggers during queued/running work keep one follow-up; waiting
  retry/continuation is accelerated in place.
- Scheduled/manual overlap now produces active plus one follow-up, with overdue
  interval consumed beyond current clock.
- Accepted follow-up survives runner deadline; explicit cancellation retains its
  separate behavior.
- Valid non-empty revision mismatch blocks stale progress, stale terminal
  promotion, old cursor restart, and queued stale dispatch.
- Complete, partial, and unknown coverage remain separate; incomplete scans do
  not assert absence or replace last complete evidence.
- Package remains read-only and storage-neutral. It imports only domain types and
  performs no upstream or filesystem mutation.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, owned paths, intervening scoped diff | Passed. |
| `GOWORK=off go test -mod=readonly -count=20 -timeout=120s ./internal/scanning` | Passed. |
| `GOWORK=off go test -race -mod=readonly -count=5 -timeout=120s ./internal/scanning` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/scanning` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 -timeout=180s ./...` | Passed. |
| Linux amd64/arm64 CGO-free scanning compilation | Passed. |
| `python3 scripts/check_planning.py --self-test` and planning validation | Passed: 44 tasks, 60 acceptance cases, links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed: generation, staged generation, Vacuum 100/100, architecture, lint, root/nested tests, vet, and module verification. |
| Mixed scheduled/manual, deadline retention, live revision, and restart revision product regressions | Passed repeatedly in focused and race runs. |
| Empty-revision reviewer probe | Failed contract: missing binding accepted. |
| Same-root reconfiguration runner probe under `-race -count=3` | Failed contract: old and fresh runners overlapped; no Go data race reported. |
| Scheduled-only provenance probe under `-race -count=3` | Failed contract: follow-up persisted as manual; no Go data race reported. |
| Product/receipt `git diff --check` | Passed. |
| Live media, private coordinates, credentials, upstream writes, filesystem mutation | Intentionally not used. |

## Acceptance disposition

- A-02: not accepted for F-03. Non-empty mismatched revisions are now safe, but
  valid empty revisions permit obsolete evidence/cursors to remain unbound.
- A-03: not accepted. Original count/deadline/restart reproductions are fixed,
  but revision replacement can overlap two runners for one root and revision
  binding remains optional.
- A-53: scoped contribution only. Global capacity and queue bounds pass, but
  same-root replacement can consume two runner slots. Full 100k/10k capacity,
  retention/pagination, and API responsiveness remain later verification.

## Required correction gate

Require canonical non-empty revisions, retain per-root runtime ownership until a
cancelled old runner exits, and preserve scheduled/manual follow-up provenance.
Re-run original round-one probes plus new admission, restart, `MaxConcurrent=2`,
and trigger-origin probes before independent round-three review. Do not claim
full A-53 capacity or production SQLite/runner integration from this package
review.
