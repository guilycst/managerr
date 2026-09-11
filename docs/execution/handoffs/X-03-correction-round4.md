# X-03 correction handoff, round four

## Assignment

- Task ID and title: X-03, Arr inventory, lookup and native preview read adapters; correction round four.
- Owner/agent and independent reviewer: `/root/c01_implementer`; `/root/d01_reviewer`.
- Base commit: `e01deb6d90662e34d67cbd8190daf1c553035217`.
- Branch/worktree or shared-checkout ownership: shared checkout `main`; coordinator owns integration and `docs/execution/state.json`.
- Owned files and generated outputs: `internal/adapters/arr/read/`, `tests/fixtures/arr/read/`, and this handoff. No generated product output was changed.
- Dependencies verified at commits: frozen domain/ports and prior X-03 product at `e01deb6`; review receipt `ad3fb145cf746580c9416aa365d4de7e41787133`.
- Required acceptance IDs and exact planned commands: A-07, A-09, A-10, A-11 and A-16; focused/root tests, race, vet, generation, API, architecture, planning, module and guardrail checks.

## Contract and work

- Linked specification sections and relevant invariants: `docs/specs/spec-001-media-reconciliation/connectors.md` Arr and coverage sections; `docs/specs/spec-001-media-reconciliation/spec-001-media-reconciliation.md` I-05 and the read-only discovery, exact association and uncertain-outcome invariants. Native preview remains separate from reprocessing and command execution.
- Intended result and capability limits: preserve useful Arr observations while refusing to claim complete mutable history coverage without an immutable boundary; allow only pinned, bounded quality/custom-format shapes into reprocessing; reject contradictory Sonarr episode-file identity evidence. The adapter remains read-only for catalog/observation/preview and never calls `/api/v3/command`.
- Changes completed:
  - History cursors now retain an explicit snapshot token and first-page total. Supported `snapshotId`, `snapshotToken` and `snapshotRevision` values must be stable across pages; token loss/change, total drift, page drift and page-size drift become reason-coded partial evidence. Multi-request history without a stable token terminates with `history_snapshot_unverified`, including non-overlapping deletion drift.
  - Native quality models and custom-format resources are validated against the pinned Radarr/Sonarr V3 shapes with unknown-field rejection, scalar/type checks, positive IDs, bounded nested bytes/items and recursive specification/field checks. Missing quality rejects video candidates; subtitle candidates may omit quality because it is not applicable. Bad raw values are rejected by `ReprocessPreview` before serialization or POST, while valid raw values remain lossless evidence.
  - Sonarr episode-file reads now require a scalar outer `episodeFileId` to match the nested file ID. Repeated IDs must retain the same mapped root-relative path and size; a physical path cannot be reported under multiple IDs. Contradictions remain incomplete/unknown evidence and cannot be returned as a successful observation.
  - Added adversarial regressions for deletion drift and stable-boundary success, untyped/null quality/custom-format values with zero POSTs, outer/nested ID mismatch, repeated ID path/size drift, same path under different IDs, and byte-equivalent multi-episode evidence.
- Changes remaining: none in this correction slice. Arr's pinned history endpoint does not expose a stable token in ordinary responses, so multi-page histories intentionally remain partial unless a supported explicit boundary is returned.
- Shared contract changes requested from coordinator: none.
- Changed paths and tested commit: product commit `7c2c3712fd69865d03f0a13308633fadc7ba7228` changes only `internal/adapters/arr/read/client.go` and `internal/adapters/arr/read/client_test.go`; no fixture file was needed.

## Verification

| Command or scenario | Commit / fixture version | Result / exit status | Evidence path |
| --- | --- | --- | --- |
| `gofmt -w internal/adapters/arr/read/client.go internal/adapters/arr/read/client_test.go` | product `7c2c3712` | passed, exit 0 | owned Go files |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/arr/read` | product `7c2c3712` | passed, exit 0 | focused Arr tests and adversarial regressions |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/arr/read` | product `7c2c3712` | passed, exit 0 | focused race suite |
| `GOWORK=off go vet -mod=readonly ./internal/adapters/arr/read` | product `7c2c3712` | passed, exit 0 | focused vet |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | product `7c2c3712` | passed, exit 0 | root modules, including untracked Jellyfin package compilation |
| `GOWORK=off go vet -mod=readonly ./...` | product `7c2c3712` | passed, exit 0 | root vet |
| UI `GOWORK=off go test -mod=readonly -count=1 ./...` and `GOWORK=off go vet -mod=readonly ./...` | product `7c2c3712` | both passed, exit 0 | `ui/` module |
| `GOWORK=off go mod verify` in root, `ui/`, and `tools/` | product `7c2c3712` | all passed, exit 0 | module caches |
| `./scripts/generate.sh --check` | product `7c2c3712` | passed, exit 0 | generated artifacts |
| `./scripts/check-api.sh` | product `7c2c3712` | passed, exit 0; Vacuum 100/100 | `api/openapi.yaml` |
| `python3 scripts/check_planning.py` | product `7c2c3712` | passed, exit 0; 38 tasks/60 acceptance cases | planning ledger |
| `python3 scripts/check-architecture.py` | product `7c2c3712` | passed, exit 0 | import-boundary check |
| `./scripts/check-guardrails.sh --fast` and commit pre-hook | product `7c2c3712` | passed, exit 0 | guardrail checks |
| `git diff --check` and explicit staged-name check | product `7c2c3712` | passed, exit 0; only two owned Arr files staged | scoped product diff |
| Mutable page deletion drift: first page IDs 4/3, next page ID 1 after total 4→3 | synthetic `TestArrHistoryDeletionDriftCannotBecomeComplete` | passed; unique count 3, partial with `history_total_changed` and `history_snapshot_unverified` | `internal/adapters/arr/read/client_test.go` |
| Stable token pages with unchanged `snapshotId` | synthetic boundary handler | passed; terminal coverage complete | `internal/adapters/arr/read/client_test.go` |
| Quality/custom-format unknown-only, null and malformed members | synthetic `TestArrNativeNestedObjectsRejectUntypedValuesBeforePOST` | passed; mapping rejects and manual reprocess emits invalid input with zero POSTs | `internal/adapters/arr/read/client_test.go` |
| Sonarr outer/nested ID mismatch, repeated ID path/size drift, same path/different IDs and valid repeated evidence | synthetic `TestSonarrEpisodeFileEvidenceRejectsContradictions` | passed; contradictions return unknown and valid pair retains both episode IDs | `internal/adapters/arr/read/client_test.go` |

No live Arr service, credentials, private coordinates, media payload, command request, filesystem mutation, release or deployment was used.

## Review and integration

- Reviewer identity/role and reviewed commit: `/root/d01_reviewer`, independent reviewer; receipt `ad3fb145cf746580c9416aa365d4de7e41787133` reviewed product `e01deb6`.
- Findings with severity, reproduction and contract reference: three P1s in the round-three receipt: mutable history deletion drift could become complete; untyped quality/custom-format objects could reach POST; contradictory Sonarr outer/nested IDs and repeated file details were accepted. Reproductions and contract links are recorded in the review receipt.
- Fix commit and regression evidence: product `7c2c3712fd69865d03f0a13308633fadc7ba7228`; focused regressions and repository gates above.
- Final reviewer decision: pending independent review of `7c2c3712fd69865d03f0a13308633fadc7ba7228`.
- Integrated commit, recorded by coordinator: pending; coordinator updates `docs/execution/state.json`.

## Resume checkpoint

- Current state and outstanding uncertainty: product is ready at `7c2c3712fd69865d03f0a13308633fadc7ba7228`; ordinary Arr history remains partial across more than one request because its pinned endpoint lacks an immutable snapshot boundary. Future supported boundary names outside the explicit allowlist require an adapter change.
- Active process or agent ownership: no active process; coordinator owns integration and execution-state updates.
- Next safe action: stage only this handoff, commit it separately, then run the independent review against the exact product SHA.
- Blocker and exact input/evidence needed: none for this implementation; review should verify the strict pinned nested shapes against the selected Arr versions and rerun the mutable-page and contradictory-file probes.
- No conflicting writes or unknown files removed: the coordinator's execution state was not edited or staged; unrelated untracked `internal/adapters/jellyfin/` remains in the shared checkout and was not touched by this correction.
