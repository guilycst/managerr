# W-02 correction round four handoff

## Assignment

- Task ID and title: W-02 correction round four, durable dispatch-barrier
  recovery after marker-write outage.
- Owner/agent and independent reviewer: `/root/x05_implementer`; independent
  review requested from `/root/x05_reviewer`.
- Base product commit: `d10bcb6bba9bc59ba80819950991b45cb05bce99`.
- Prior review receipt: `ef07140a8d41696c4e4514e6860e47e6211b8814`.
- Branch/worktree: shared `main` checkout in repository-relative public paths.
- Owned files: `internal/execution/` and this handoff only. No schema, module,
  adapter, client, UI, state, or generated-output changes.
- Dependencies verified at commits: W-02 round-three product at the base
  commit above; the coordinator's dispatch metadata is present in the shared
  checkout.
- Required acceptance IDs: A-14, A-32, A-33, A-34, A-35, A-36, A-59, A-60.

## Contract and work

- Linked invariants: observe before write; a returned dispatch remains a live
  barrier until exact attempt evidence is durable; lost responses reconcile
  before any mutation retry; claims are generation-fenced; action and effect
  identity/counts remain exact; cancellation remains cooperative and durable.
- Intended result: marker-write failure can outlast the bounded in-memory
  retry window without stranding reconciliation until process startup.
- Completed changes:
  - Before the action-level `dispatchBarrierReturnedAttempt` marker, the
    exact running dispatch attempt records
    `dispatchBarrierRetryAttempt` in its existing durable evidence JSON,
    together with uncertain/pending barrier detail. This preserves the live
    attempt barrier while the marker is unavailable.
  - Reconciliation discovers only a running dispatch attempt whose intent
    value exactly equals that attempt's ID. It then releases that exact
    barrier through the existing effect/attempt uncertainty path and resumes
    read-only reconciliation.
  - A fresh worker preserves its own valid claim while committing the marker,
    adopts the one resulting action version increment, and remains fenced for
    subsequent journal writes. Background/stale release paths clear claims
    after the returned barrier is known.
  - The memory fault journal can reject every action outcome update containing
    the marker, count attempts, and later restore writes for deterministic
    recovery coverage. The prior transient fixture now waits for its exact
    background barrier transition before a competing scheduler claim.
- Remaining uncertainty: G-01 native upstream capability/version evidence
  remains open. This correction does not enable any upstream or filesystem
  mutation capability.
- Shared contract changes requested from coordinator: none.
- Tested product commits:
  - `a645abe60341a3f21a71aaabf3ba5364eeb10a09` — durable intent and recovery
    implementation plus outage fixture.
  - `4af1583d393fa9606ce8682585056b7c1ce1d3f8` — deterministic barrier fixture
    synchronization and scheduler-path assertion.

## Verification

| Command or scenario | Commit / fixture version | Result / exit status | Evidence path |
| --- | --- | --- | --- |
| `GOWORK=off go test -mod=readonly ./internal/execution -run '^TestBarrierReleaseRetriesAfterTransientJournalFailure$' -count=100 -timeout=240s` | `4af1583d393fa9606ce8682585056b7c1ce1d3f8` | Passed | Existing transient fault fixture; exact attempt reaches `reconciling` before fresh claim. |
| `GOWORK=off go test -mod=readonly ./internal/execution -run '^TestDispatchBarrierIntentSurvivesMarkerWriteOutage$' -count=100 -timeout=240s` | `4af1583d393fa9606ce8682585056b7c1ce1d3f8` | Passed | Marker outcome blocked for initial write plus all 32 bounded retries; durable exact-attempt intent survives; fresh scheduler reconciles once. |
| `GOWORK=off go test -mod=readonly ./internal/execution -count=1 -timeout=180s` | `4af1583d393fa9606ce8682585056b7c1ce1d3f8` | Passed | Full execution package. |
| `GOWORK=off go test -mod=readonly -race ./internal/execution -run 'TestDispatchBarrierIntentSurvivesMarkerWriteOutage\|TestBarrierReleaseRetriesAfterTransientJournalFailure\|TestExpiredLiveDispatchBlocksRecoveredWorkerUntilReturn' -count=3 -timeout=240s` | `4af1583d393fa9606ce8682585056b7c1ce1d3f8` | Passed | Focused race regressions. |
| `GOWORK=off go vet -mod=readonly ./internal/execution` | `4af1583d393fa9606ce8682585056b7c1ce1d3f8` | Passed | Execution package vet. |
| `GOWORK=off go test -mod=readonly ./... -count=1 -timeout=240s`; `GOWORK=off go vet -mod=readonly ./...`; `GOWORK=off go mod verify` | `4af1583d393fa9606ce8682585056b7c1ce1d3f8` | Passed | Root module matrix and module verification. |
| `./scripts/check-api.sh`; `python3 scripts/check-architecture.py`; `python3 scripts/check_planning.py` | `4af1583d393fa9606ce8682585056b7c1ce1d3f8` | Passed | API bundle/Vacuum, import boundaries, and 44 tasks/60 acceptance links. |
| `./scripts/check-guardrails.sh --ci` | `4af1583d393fa9606ce8682585056b7c1ce1d3f8` | Passed | Reproducible generation, Vacuum 100/100, lint, architecture, all root/UI/tools/client tests, vet, and module verification. |
| Linux `amd64` and `arm64`, CGO-free compile matrix with `GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` for root, `ui`, `tools`, `clients/qbittorrent`, `clients/qbittorrent/internal/generated`, and `clients/nzbget` | `4af1583d393fa9606ce8682585056b7c1ce1d3f8` | Passed | Both architectures. |
| `git diff --check`; versioned pre-commit hook during product commits | `4af1583d393fa9606ce8682585056b7c1ce1d3f8` | Passed | No whitespace errors; fast guardrail hook passed. |
| Live services, credentials, private coordinates, or real media | `4af1583d393fa9606ce8682585056b7c1ce1d3f8` | Not used | Public synthetic fixtures only; G-01 remains open. |

The outage fixture's effects remain pending while marker writes fail and the
dispatch attempt remains running. Once writes recover, the fresh scheduler
observes the exact intent, persists the action marker, marks the dispatch
attempt/effects uncertain, and performs one read-only reconciliation. No
blind dispatch or mutation retry is introduced.

## Review and integration

- Reviewer identity/role and reviewed commit: pending independent review by
  `/root/x05_reviewer` of final product `4af1583d393fa9606ce8682585056b7c1ce1d3f8`.
- Findings addressed: the round-four P2 requiring durable recovery after a
  marker-write outage; prior lease, cancellation, effect-identity,
  uncertainty, reservation, and lost-response fixes were retained.
- Fix commit and regression evidence: product commits above; handoff commit
  SHA is recorded after this file is committed.
- Final reviewer decision: pending.
- Integrated commit, recorded by coordinator: pending; coordinator owns
  `docs/execution/state.json`.

## Resume checkpoint

- Current state: product correction is committed and all listed checks pass;
  independent review remains outstanding. G-01 remains open.
- Active process or agent ownership: no long-running test process; coordinator
  owns state updates and review dispatch.
- Next safe action: coordinator records both product commits and this handoff,
  then dispatches independent review against the final product tree.
- Blocker and exact input/evidence needed: reviewer receipt for
  `4af1583d393fa9606ce8682585056b7c1ce1d3f8`; native upstream capability
  evidence remains the separate G-01 gate.
- No conflicting writes or unknown files removed.
