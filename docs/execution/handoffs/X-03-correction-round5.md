# X-03 correction handoff, round five

## Assignment

- Task ID and title: X-03, Arr inventory, lookup and native preview read adapters; correction round five.
- Owner/agent and independent reviewer: `/root/c01_implementer`; `/root/d01_reviewer`.
- Base product commit: `7c2c3712fd69865d03f0a13308633fadc7ba7228`.
- Review receipt: `51113716a3b9fccc20df26a097d634f758b78704`.
- Branch/worktree or shared-checkout ownership: shared checkout `main`; coordinator owns integration and `docs/execution/state.json`.
- Owned paths: `internal/adapters/arr/read/`, `tests/fixtures/arr/read/`, and this handoff only. No fixture file was needed for this correction.
- Product commit: `5a4ceaac7307f264cdfacca126a88f319d76a842` (`fix(arr): close X-03 round-five read gaps`).
- Required acceptance contributions: A-07, A-09, A-10, A-11 and A-16.

## Contract and work

The correction closes all three P1 findings from review receipt `51113716` while preserving Arr read-only and preview/reprocessing boundaries:

- Pinned Arr history is offset-paginated and has no documented immutable snapshot capability or request parameter. Top-level `snapshotId`, `snapshotToken` and `snapshotRevision` fields are therefore retained only as unsupported evidence. Any such field, and any legacy cursor state that claims a snapshot, adds `history_snapshot_unsupported`, clears stable-token state and cannot produce complete coverage. Multi-request history still carries `history_snapshot_unverified` when no supported boundary exists. A synthetic deletion drift with no overlap proves the terminal result remains partial and retains the tail record.
- Native quality validation now receives the selected `ConnectionKind`. Radarr and Sonarr use separate pinned source field sets and enums; Sonarr accepts `web`, `television`, `televisionRaw`, `webRip` and `blurayRaw`, while rejecting Radarr-only sources and the Radarr `modifier` field. Custom-format validation is dispatched through the selected product kind and keeps unknown nested fields, null members and malformed values out of the typed DTO path.
- Direct `ReprocessPreview` requires a validated typed quality object for every video file before payload construction. Missing, whitespace, null, unknown-only or malformed video quality returns typed invalid input with zero manual-import POSTs; subtitle rows retain the explicit missing-quality exception.
- Sonarr episode inventory now indexes episode identity back to its physical file. The same episode ID on distinct file IDs or root-relative paths is reason-coded `episode_identity_conflict` and causes observation to remain unknown. Repeated same-file episode evidence remains accepted and multi-episode files retain their complete episode set.

No command endpoint, automatic import, filesystem action or live upstream was used. Source and request paths remain root-constrained, native GET fields remain lossless evidence, and POST DTOs remain explicit.

## Verification

| Command or scenario | Result / exit status | Evidence |
| --- | --- | --- |
| `gofmt -w internal/adapters/arr/read/client.go internal/adapters/arr/read/client_test.go` | passed, exit 0 | Owned Arr files |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/arr/read` | passed, exit 0 | Focused Arr suite |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/arr/read` | passed, exit 0 | Focused race suite |
| `GOWORK=off go vet -mod=readonly ./internal/adapters/arr/read` | passed, exit 0 | Focused vet |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | passed, exit 0 | Root packages; concurrent untracked Jellyfin and Seerr packages compiled |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | passed, exit 0; storage completed in 112.003s | Root race suite |
| `GOWORK=off go vet -mod=readonly ./...` | passed, exit 0 | Root vet |
| Root, `ui/` and `tools/` `GOWORK=off go mod verify` | passed, exit 0 | All module dependency checks |
| UI `GOWORK=off go test -mod=readonly -count=1 ./...` and vet | both passed, exit 0 | `ui/` module |
| Tools `GOWORK=off go test -mod=readonly -count=1 ./...` and vet | both passed, exit 0 | `tools/` module |
| `./scripts/generate.sh --check` | passed, exit 0 | Generated artifacts |
| `./scripts/check-api.sh` | passed, exit 0; Vacuum 100/100 | `api/openapi.yaml` |
| `python3 scripts/check_planning.py` | passed, exit 0; 38 tasks/60 acceptance cases | Planning ledger |
| `python3 scripts/check-architecture.py` | passed, exit 0 | Import boundaries |
| `./scripts/check-guardrails.sh --fast` | passed, exit 0 | Guardrail checks |
| Product commit pre-commit hook | passed, exit 0 | Product commit `5a4ceaac` |
| `git diff --check` and staged-name inspection | passed, exit 0 | Only the two Arr files staged for product commit |
| Snapshot keys `snapshotId`, `snapshotToken`, `snapshotRevision` with synthetic offset deletion drift | passed | `TestArrHistorySnapshotShapedFieldsCannotCertifyOffsetDeletionDrift`; each key remains partial with `history_snapshot_unsupported`, unique count 3 and tail ID 4 |
| Product-specific quality and custom-format cases | passed | `TestArrProductSpecificQualityValidation` and `TestArrQualitySourceEnumsAreProductSpecific`; valid Radarr/Sonarr paths POST once, cross-product source/modifier and untyped custom format POST zero times |
| Direct missing video quality | passed | Radarr and Sonarr invalid input before manual-import POST |
| Sonarr episode identity contradictions | passed | `TestSonarrEpisodeFileEvidenceRejectsContradictions`; distinct physical files remain unknown, same physical file retains both episode IDs |
| `git status --short` scope check | inspected | Concurrent `internal/storage/query.sql`/generated output and untracked Jellyfin, Seerr and catalog fixtures were preserved and never staged |

All test data is synthetic. No credentials, private endpoints, private inventories, media payloads, command requests, releases or deployments were used.

## Review and integration

- Reviewer: `/root/d01_reviewer`, independent of the implementer.
- Review source and findings: receipt `51113716a3b9fccc20df26a097d634f758b78704` identified unsupported snapshot-shaped fields, shared Radarr/Sonarr quality validation plus missing direct video quality, and repeated Sonarr episode identity across files.
- Fix product: `5a4ceaac7307f264cdfacca126a88f319d76a842`.
- Handoff commit: pending; this file is intentionally committed separately from product.
- Final reviewer decision: pending independent review of the exact product commit.
- Coordinator integration and execution-state update: pending; this lane did not edit or stage `docs/execution/state.json`.

## Resume checkpoint

- Product is ready at `5a4ceaac7307f264cdfacca126a88f319d76a842`; ordinary multi-request Arr history remains partial because the pinned endpoint has no immutable snapshot boundary.
- Next safe action: independently review the exact product SHA, rerun the three adversarial scenarios and record the result in the coordinator ledger.
- Remaining review risks: verify the product-specific nested schemas against the pinned Arr source commits and ensure no future unsupported response key is treated as an immutable boundary without an explicit capability contract.
- No conflicting files were removed or reset. The coordinator's execution state and concurrent Jellyfin, Seerr, catalog and storage edits remain outside this handoff's staging scope.
