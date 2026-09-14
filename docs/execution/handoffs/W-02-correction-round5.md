# W-02 correction round five handoff

## Assignment

- Task ID and title: W-02 correction round five, durable recovery when the
  attempt-level dispatch barrier intent cannot be written.
- Owner/agent and independent reviewer: `/root/x05_implementer`; independent
  review requested from `/root/x05_reviewer`.
- Base product commit: `4af1583d393fa9606ce8682585056b7c1ce1d3f8`.
- Prior review receipt: `a82461e2954f8a7cadd25f714c720ab1f7fe3727`.
- Branch/worktree: shared `main` checkout in repository-relative public paths.
- Owned files: `internal/execution/` and this handoff only. No schema, module,
  adapter, client, UI, state, or generated-output changes.
- Required acceptance IDs: A-14, A-32, A-33, A-34, A-35, A-36, A-59, A-60.

## Contract and work

- Linked invariants: a returned dispatch remains a live barrier until exact
  recovery evidence is durable; lost responses reconcile before any mutation
  retry; claims remain generation-fenced; exact attempt/effect identity and
  unresolved counts are preserved; cancellation remains durable and
  cooperative.
- Intended result: failure to persist the attempt-level
  `dispatchBarrierRetryAttempt` evidence through the initial call and all
  bounded in-memory retries no longer strands recovery until process startup.
- Completed changes:
  - `releaseDispatchBarrierWithClaim` retains an attempt-intent write error
    long enough to persist the action-level exact
    `dispatchBarrierReturnedAttempt` marker. The marker is written only from
    the post-handler-return barrier path and is therefore a safe durable
    fallback for the same exact dispatch attempt.
  - When attempt evidence is unavailable, the exact dispatch attempt remains
    `running`, effects remain pending, and the release returns the prerequisite
    error so background retries continue. No worker is allowed to reconcile or
    dispatch through that live barrier.
  - Once the attempt write recovers, a fresh scheduler uses the durable action
    marker, writes the exact attempt intent, transitions the attempt/effects
    through the existing uncertainty path, and performs read-only
    reconciliation. Repeated marker handling is idempotent while the action is
    already reconciling, avoiding version churn.
  - The synthetic memory journal can reject every attempt update carrying the
    intent field and count failures. The new fixture proves the initial update
    plus all 32 bounded retries fail, the action marker and running barrier
    persist, recovery reaches one reconciliation, and dispatch remains zero.
- Remaining uncertainty: G-01 native upstream capability/version evidence
  remains open. This correction does not enable any upstream or filesystem
  mutation capability.
- Shared contract changes requested from coordinator: none.
- Tested product commit: `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a`.

## Verification

| Command or scenario | Commit / fixture version | Result / exit status | Evidence path |
| --- | --- | --- | --- |
| `GOWORK=off go test -mod=readonly ./internal/execution -run 'TestDispatchBarrierActionMarkerSurvivesIntentWriteOutage\|TestDispatchBarrierIntentSurvivesMarkerWriteOutage\|TestBarrierReleaseRetriesAfterTransientJournalFailure\|TestExpiredLiveDispatchBlocksRecoveredWorkerUntilReturn' -count=100 -timeout=300s` | `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a` | Passed | Attempt-intent outage, action-marker outage, transient retry, and live-handler barrier fixtures. |
| `GOWORK=off go test -mod=readonly -race ./internal/execution -run '^TestDispatchBarrierActionMarkerSurvivesIntentWriteOutage$' -count=10 -timeout=300s` | `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a` | Passed | New prerequisite-marker outage race regression. |
| `GOWORK=off go test -mod=readonly ./internal/execution -count=1 -timeout=180s`; `GOWORK=off go vet -mod=readonly ./internal/execution` | `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a` | Passed | Full execution package and vet. |
| `GOWORK=off go test -mod=readonly -race ./internal/execution -count=3 -timeout=300s` | `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a` | Passed | Full execution race matrix. |
| `GOWORK=off go test -mod=readonly ./... -count=1 -timeout=240s`; `GOWORK=off go vet -mod=readonly ./...`; `GOWORK=off go mod verify` | `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a` | Passed | Root module tests, vet, and module verification. |
| `./scripts/check-api.sh`; `python3 scripts/check-architecture.py`; `python3 scripts/check_planning.py` | `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a` | Passed | API/Vacuum, import boundaries, and 44 tasks/60 acceptance links. |
| `./scripts/check-guardrails.sh --ci` | `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a` | Passed | Reproducible generation, Vacuum 100/100, lint, architecture, all root/UI/tools/client tests, vet, and module verification. |
| Linux `amd64` and `arm64`, CGO-free compile matrix with `GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` for root, `ui`, `tools`, `clients/qbittorrent`, `clients/qbittorrent/internal/generated`, and `clients/nzbget` | `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a` | Passed | Both architectures. |
| `git diff --check`; versioned pre-commit hook during product commit | `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a` | Passed | No whitespace errors; fast guardrail hook passed. |
| Live services, credentials, private coordinates, or real media | `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a` | Not used | Public synthetic fixtures only; G-01 remains open. |

The fault fixture keeps the returned dispatch attempt running while the
attempt-level marker is unavailable and confirms pending effects remain behind
that barrier. The action-level marker is the durable fallback. After writes
recover, the fresh scheduler releases only the exact attempt, records
uncertainty, reconciles read-only once, and returns a safe queued outcome with
zero unresolved effects. No blind mutation retry is introduced.

## Review and integration

- Reviewer identity/role and reviewed commit: pending independent review by
  `/root/x05_reviewer` of product `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a`.
- Finding addressed: the round-five P2 requiring recovery when the
  attempt-level intent update fails for the full bounded retry window; prior
  lease, cancellation, effect-identity, uncertainty, reservation, and
  action-marker fixes remain intact.
- Fix commit and regression evidence: product commit above; handoff commit SHA
  is recorded after this file is committed.
- Final reviewer decision: pending.
- Integrated commit, recorded by coordinator: pending; coordinator owns
  `docs/execution/state.json`.

## Resume checkpoint

- Current state: product correction is committed and checks pass; independent
  review remains outstanding. G-01 remains open.
- Active process or agent ownership: no long-running test process; coordinator
  owns state updates and review dispatch.
- Next safe action: coordinator records the product and handoff commits, then
  dispatches independent review against the final product tree.
- Blocker and exact input/evidence needed: reviewer receipt for
  `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a`; native upstream capability
  evidence remains the separate G-01 gate.
- No conflicting writes or unknown files removed.
