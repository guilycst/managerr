# F-03 independent review, round three

## Decision

`approved`.

The exact correction product closes all three round-two findings. Current root
schedules require a non-empty, trimmed configuration revision; legacy scans
without a binding are terminalized before dispatch or evidence publication. A
queued replacement cannot run while an old runner still owns the same root,
even after the old durable record has been marked stale and global capacity is
available. Coalesced scheduled work retains scheduled provenance, while manual
participation has consistent explicit precedence.

No product correctness finding remains in the bounded F-03 correction scope.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/d02_implementer`.
- Prior reviewed product:
  `ebdf5112c5be852fc992f337d47e4e0589f52b94`.
- Round-two review receipt:
  `884a505a4820d475c16a1e034fd8680367ebf9d7`.
- Correction implementation:
  `b78a30b6186f025f6dc59fb7911cd7adebecb0ea`; tree
  `45464a416a45938bf59f0a1f0f1e67374872542f`.
- Reviewed final product:
  `b65f5d65d67e6fc823fbf54fa8ab4f2a4d17d191`; tree
  `b8951613d3f5b9732187b2c82a638374fac77894`.
  The implementation commit is an ancestor of this final test commit.
- Reviewed handoff:
  `74d8f233e269c2fc7bc5cc0f269980641ddbf616`; tree
  `93ad1fb71f5b4e88f5c42da745b41fe5dcfa0b8c`; direct parent is the final
  product.
- Product commits change only `internal/scanning/scanning.go` and
  `internal/scanning/scanning_test.go`.
- The handoff contains no later change under `internal/scanning/`.
- Scoped correction diff SHA-256 from the prior reviewed product:
  `528905cc12446d706dfde3aee6a918eb26f6e0ae45f2aec5411f8a0f34c79b13`.
- Acceptance reviewed: A-02, A-03, and the bounded F-03 contribution to A-53.
- Review receipt checkpoint: the commit containing this file; its exact SHA is
  reported after commit because a Git commit cannot contain its own SHA.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-f03-review-r3` at the exact
handoff. The prior adversarial probes ran from an archive copy of the exact
product under `/tmp`. Reviewer changed no product, state, task, generated,
filesystem, adapter, storage, module, or shared-worktree path.

## Round-two finding closure

### Configuration revision is mandatory and canonical

Resolved. `RootSchedule.Validate` calls one revision validator for both
`ConfigureRoots` and `RegisterRoot`. It rejects empty, whitespace-only,
untrimmed, control-character, and oversized revisions
(`scanning.go:119-136`, `scanning.go:157-172`). Newly admitted records inherit
that validated revision.

`ScanRecord.validate` permits an empty revision only for legacy history.
Startup recovery compares every active scan with the current non-empty root
revision, terminalizes an unbound legacy record as `stale_config_revision`,
invalidates its projections, and admits a fresh bound scan before the old
cursor can be dispatched (`scanning.go:377-392`, `scanning.go:1451-1512`).

The independent empty-revision probe now passes. Product tests additionally
cover whitespace boundaries and legacy restart, asserting that the fresh
checkpoint has the current revision and no old cursor or count.

### Same-root revision replacement waits for runner exit

Resolved. Queued selection now skips any root represented in
`scheduler.running`, while continuing to consider queued work for unrelated
roots (`scanning.go:1596-1639`, `scanning.go:1731-1765`). The runtime map keeps
the old scan ID until `finish` returns, including when reconfiguration has
already made its durable record terminal. `finish` removes the ownership and
pumps the queue even when the old terminal record causes
`ErrScanNotRunning` (`scanning.go:1953-1961`).

The independent `MaxConcurrent=2` probe kept the cancelled old runner inside a
non-cooperative bounded operation and observed no fresh start. The product
regression also releases the old runner and proves that the current-revision
scan starts afterward with the correct root and checkpoint binding. Focused
and race-instrumented repetitions passed.

### Follow-up provenance is retained with deterministic precedence

Resolved. `RootState` now persists `FollowUpTrigger` with the bounded pending
bit. `retainFollowUp` preserves scheduled-only provenance and gives manual work
precedence regardless of trigger order; `clearFollowUp` clears both fields
together (`scanning.go:143-154`, `scanning.go:1293-1309`). `finish` creates the
follow-up from the retained trigger (`scanning.go:1935-1949`).

Snapshot validation rejects a trigger without pending work and invalid trigger
values. Startup converts the legacy pending-bit-only representation to its
historical manual meaning before new work is admitted
(`scanning.go:288-375`, `scanning.go:1451-1461`). The independent
scheduled-only probe and product scheduled/manual-order fixtures pass.

## Preserved behavior

- Scheduled and manual overlap remains bounded to the active scan plus one
  follow-up. Repeated overdue ticks consume schedule time without creating a
  third scan.
- Deadline completion retains pending work; explicit cancellation retains its
  separate suppression behavior.
- Stale progress, final evidence, cursor recovery, queued dispatch, and root
  projections remain fenced by the current configuration revision.
- Complete, partial, and unknown coverage remain distinct. Incomplete work
  cannot assert absence or replace last-complete evidence.
- Total runner concurrency remains bounded, while unrelated roots may use
  capacity left idle by a blocked same-root replacement.
- The package remains read-only and storage-neutral. It imports only domain
  types and performs no upstream or filesystem mutation.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, commit-owned paths, and scoped diff | Passed. |
| Prior reviewer probes for mandatory revision, same-root replacement, and scheduled provenance under `-race -count=5` | Passed. |
| `GOWORK=off go test -mod=readonly -count=20 -timeout=120s ./internal/scanning` | Passed. |
| `GOWORK=off go test -race -mod=readonly -count=5 -timeout=120s ./internal/scanning` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/scanning` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 -timeout=180s ./...` | Passed. |
| Linux amd64 and arm64 CGO-free scanning compile-only checks | Passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py --self-test` and planning validation | Passed: 44 tasks, 60 acceptance cases, local links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed: deterministic generation, Vacuum 100/100, architecture, lint, root/nested tests, vet, and all module verification. |
| Product and handoff `git diff --check` | Passed. |
| Live media, private coordinates, credentials, upstream writes, and filesystem mutation | Not used. |

## Acceptance disposition

- A-02: accepted for F-03. Current and recovered scans require an effective
  revision binding before cursor or coverage evidence can become current;
  partial and unknown evidence rules remain intact.
- A-03: accepted for F-03. One runtime runner owns each root through exit,
  trigger coalescing stays bounded and attributed, and revision/restart cursor
  behavior passes focused race tests. Production SQLite mapping and concrete
  runner composition remain downstream integration work.
- A-53: accepted only for F-03's concurrency and queue-bounding contribution.
  The required 100k observation/10k catalog capacity measurements and API
  responsiveness remain later integration verification.

## Integration note

The handoff's `Review and integration` section contains one stale line naming
`ebdf5112...` as the review source. Its product section, resume checkpoint,
Git parentage, coordinator dispatch, and this receipt consistently bind the
actual final source `b65f5d65d67e6fc823fbf54fa8ab4f2a4d17d191`. Correct the stale line when
integrating the execution ledger; it does not change the product decision.

Approval covers only this exact F-03 product and its bounded acceptance
contributions. Storage integration, API/UI exposure, capacity measurement,
release, deployment, and live-stack verification remain separate gates.
