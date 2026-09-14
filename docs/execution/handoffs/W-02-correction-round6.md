# W-02 correction round six handoff

## Assignment

- Task: W-02 correction round six, retain returned-dispatch recovery when both
  durable marker writes are unavailable.
- Owner: `/root/x05_implementer`; independent reviewer: `/root/x05_reviewer`.
- Product base: `addb005de8972d4e67c0cc61f0f96f2ac11a7a3a`.
- Prior review receipt: `fc67e8b5ea2ade046bfca38fd68f87690a552b99`.
- Product commit: `ff23e1b939f04c1136cba8134d3c9ae505094dba`.
- Branch/worktree: shared `main` checkout.
- Owned paths: `internal/execution/` and this handoff only. No state, schema,
  modules, adapters, clients, UI, or generated-output changes.
- Linked acceptance: A-14, A-32, A-33, A-34, A-35, A-36, A-59, A-60.

## Correction

Round-five made either returned-handler marker sufficient, but a simultaneous
outage could still exhaust both marker paths before recovery evidence became
durable. This correction retains an exact process-local fallback keyed by both
action ID and dispatch attempt ID. A fresh `Executor` in the same process can
discover that fallback after journal writes recover and release only that live
dispatch barrier. The fallback is removed only after the exact attempt leaves
the running dispatch state or a successful barrier transition commits.

`processReconciliation` now checks action evidence, attempt evidence, then the
exact process-local fallback. The fallback still requires a matching running
dispatch attempt, so it can release a returned handler barrier but cannot
authorize a new mutation or a later attempt generation. Existing bounded retry
workers remain in place; their exhaustion no longer discards the only local
knowledge of a returned handler. Process restart remains covered by durable
marker or startup recovery behavior; this fallback is intentionally not
presented as restart durability.

`TestDispatchBarrierFallbackSurvivesDualMarkerWriteOutage` blocks both exact
marker writes through the initial release and all 32 bounded retries, verifies
the action is reconciling with a running dispatch and pending effect, restores
writes, creates a fresh executor, and proves one read-only reconciliation with
zero dispatches. It also verifies the exact fallback is removed after barrier
resolution.

## Verification

| Check | Result |
| --- | --- |
| `GOWORK=off go test -mod=readonly ./internal/execution -run '^TestDispatchBarrier(FallbackSurvivesDualMarkerWriteOutage\|IntentSurvivesMarkerWriteOutage\|ActionMarkerSurvivesIntentWriteOutage)$' -count=20 -timeout=180s` | Passed |
| `GOWORK=off go test -mod=readonly -race ./internal/execution -run '^TestDispatchBarrierFallbackSurvivesDualMarkerWriteOutage$' -count=5 -timeout=180s` | Passed |
| `GOWORK=off go test -mod=readonly -race ./internal/execution -count=2 -timeout=240s` | Passed |
| `GOWORK=off go vet -mod=readonly ./internal/execution` | Passed |
| Root, UI, tools, qBittorrent, and NZBGet module tests with `GOWORK=off` and `-mod=readonly` | Passed |
| Root `go vet`, `go mod verify`, API/Vacuum, architecture, planning, and `./scripts/check-guardrails.sh --ci` | Passed; Vacuum 100/100 and all guardrails green |
| Linux `amd64` and `arm64`, CGO-free compile matrix for root, UI, tools, qBittorrent, and NZBGet using `go test -run '^$' -exec=true` | Passed |
| Product `git diff --check` and versioned pre-commit fast hook | Passed |
| Live services, credentials, private coordinates, or real media | Not used; synthetic journal/handler fixtures only |

## Review and integration

- Product tree for review: `ff23e1b939f04c1136cba8134d3c9ae505094dba`.
- Handoff commit: recorded after this file is committed.
- Independent review: pending.
- G-01 native upstream capability/version evidence remains open; this
  correction does not enable upstream or filesystem mutation capabilities.

## Resume checkpoint

- Current state: product correction committed; handoff is ready for review.
- Next safe action: coordinator records product and handoff SHAs, then dispatches
  independent review against the exact product tree.
- Blocker: independent review receipt; process-local fallback does not replace
  durable startup recovery across a process restart.
- No conflicting writes or unknown files removed.
