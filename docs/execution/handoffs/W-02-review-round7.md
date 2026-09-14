# W-02 independent review, round seven

## Decision

`approved`.

No blocking finding remains in the reviewed W-02 correction. The exact
process-local fallback retains returned-handler knowledge when both durable
marker writes fail through the complete bounded retry window. A newly created
executor in the same process discovers only the retained action/attempt pair,
releases only the matching running dispatch after writes recover, performs one
read-only reconciliation, and makes zero dispatch calls. The fallback is
removed after the attempt leaves `running`.

The restart boundary is also explicit and safe. Process-local knowledge is not
claimed as durable across restart. When neither marker was committed before
process loss, startup `Recover` is the proof that the old handler can no longer
be live; it changes the running attempt to uncertain reconciliation before the
next poll. An independent restart simulation then reconciled once with zero
dispatches. `RunOnce` alone continues to document that it does not replace the
startup recovery call.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Prior reviewed product:
  `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a`; tree
  `ad47193bbf1cb4f36cdec6d048447e7be182ac07`.
- Prior review receipt:
  `fc67e8b5ea2ade046bfca38fd68f87690a552b99`.
- Reviewed correction product:
  `ff23e1b939f04c1136cba8134d3c9ae505094dba`; tree
  `e59b7f35b991a11af98210d6f06a5d2c515cb80d`.
- Reviewed handoff:
  `e1f125a6dbc135fd3bfec65dd27c42952e3e1afb`; tree
  `98e2b03f6096f6b45004fef30b7c240149f822ec`; its direct parent is the exact
  correction product.
- The product commit changes only `internal/execution/execution.go` and
  `internal/execution/execution_test.go`. The handoff adds only
  `docs/execution/handoffs/W-02-correction-round6.md` and contains no later
  execution delta.
- Scoped `internal/execution` diff SHA-256 from the prior product:
  `3a842db6fa7c45fc5e5286155d049bad4e24db2c3198ffdc1c46193d34a84027`.
- Acceptance reviewed: A-14, A-32, A-33, A-34, A-35, A-36, A-59, and A-60.
- Review receipt checkpoint: the commit containing this file; its exact SHA is
  reported after commit because a Git commit cannot contain its own SHA.

Review ran from the clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-w02-review-r7` at the exact
handoff. Reviewer-only adversarial tests ran in an archive of the exact product
at `/tmp/mastarr-w02-r7-probe.koiKbo`; they are not product changes. Reviewer
changed no product, state, task, storage, migration, generated, adapter, client,
module, UI, or shared-checkout path.

## Review evidence

The package-level fallback key contains both action and attempt identity. It is
remembered only from the post-handler-return barrier path. Discovery copies
the retained IDs, reads the journal, and accepts only a matching dispatch
attempt still in `running`; non-running or absent attempts remove stale local
entries. Successful exact barrier transition and independent read-back of an
already-resolved attempt also remove the entry. This preserves the earlier
rule that no worker may infer handler return from lease expiry alone.

Independent probes covered these boundaries:

- Both attempt and action markers failed for the initial release and all 32
  background retries. After both writes recovered, a separate `Executor`
  running `RunOnce` found the process fallback, transitioned the exact dispatch
  and its effect to uncertainty, called reconciliation once, called dispatch
  zero times, requeued only after safe-to-retry evidence, and cleared the
  fallback.
- Attempt-marker-only and action-marker-only outages retained their durable
  fallback behavior. The action-marker path also survived a real SQLite store
  close/reopen.
- Attempt evidence naming another attempt ID did not release the real running
  dispatch and caused neither reconciliation nor mutation.
- A simulated process restart removed the process fallback after a dual-marker
  outage. Explicit startup `Recover` moved the running attempt to uncertain
  reconciliation, and the next `RunOnce` reconciled once without dispatching.
- A handler still running after lease recovery remained a barrier. No fallback
  existed until that handler actually returned.
- No-deadline lease renewal, exact multi-effect unresolved counts,
  cross-executor initial-observe cancellation, strict effect-set validation,
  durable reservations, and stale-generation fencing remained green.

No generic DAG, duplicate outbox, generated DTO leak, upstream write
capability, filesystem mutation capability, or public contract broadening was
introduced.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, trees, commit-owned paths, and scoped diff | Passed. |
| Product and handoff `git diff --check`; no execution delta after product | Passed. |
| Three producer dual/single-marker outage fixtures, count 100 | Passed. |
| Producer dual-marker fallback fixture under race, count 10 | Passed. |
| Independent reviewer probes, including combined outage, count 20 | Passed. |
| Independent process-restart/startup-recovery probe, count 20 | Passed. |
| All independent reviewer probes under race, count 3 | Passed. |
| `GOWORK=off go test -mod=readonly ./internal/execution -count=1 -timeout=180s` | Passed. |
| `GOWORK=off go test -mod=readonly -race ./internal/execution -count=3 -timeout=300s` | Passed. |
| `GOWORK=off go vet -mod=readonly ./internal/execution` | Passed. |
| Linux and Darwin amd64/arm64 CGO-free execution compilation | Passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 44 tasks, 60 acceptance cases, local links resolve. |
| `./scripts/check-guardrails.sh --ci` | Passed: reproducible generation, Vacuum 100/100, architecture, lint, tests, vet, and all module verification. |
| Live services, credentials, private coordinates, and real media | Not used. |

## Acceptance disposition

- A-14: accepted for W-02. Same-key replay and already-materialized success
  retain zero duplicate effects.
- A-32: W-02's transaction, claim, and dedupe contribution is accepted;
  approval/enqueue atomicity remains W-04 scope.
- A-33: accepted for W-02. Post-dispatch uncertainty cannot trigger blind
  resubmission; same-process and startup recovery both reach read-only
  reconciliation through their stated evidence boundaries.
- A-34: accepted for W-02. No-deadline lease renewal, persisted action backoff,
  and returned-handler recovery survive the covered temporary outages.
- A-35: accepted for W-02. Cancellation is cooperative across handler phases;
  late/uncertain effects remain visible and recoverable without rollback claims.
- A-36: accepted for W-02. Exact action/attempt fallback keys, claim CAS, and
  durable/local reservations prevent conflicting execution.
- A-59: accepted for W-02's journal/read-back contribution. Exact effects remain
  unresolved until identity evidence proves applied or safe retry; native
  filesystem publication recovery remains the filesystem verification scope.
- A-60: accepted for W-02. Typed direct and scheduled paths share validation,
  idempotency, cancellation, claim, and recovery behavior without arbitrary
  command authority.

This receipt approves the reviewed W-02 correction for coordinator integration.
It does not close G-01, approve a release or deployment, or authorize live
filesystem/upstream mutation.
