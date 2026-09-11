# X-03 correction handoff, round six

## Assignment

- Task ID and title: X-03, Arr inventory, lookup and native preview read adapters; correction round six.
- Owner and independent reviewer: `/root/c01_implementer`; `/root/d01_reviewer`.
- Base product under review: `5a4ceaac7307f264cdfacca126a88f319d76a842`.
- Prior correction handoff: `58a5ecb80f5df7352be84cc6d7b33efdb5673328`.
- Review receipt: `1afbd30d4d8dbaa3f32e4d5b558366ce2ae02548`.
- Product parent at commit time: `a351b8d54794fb034be025ae940741c63c4c32eb`.
- Branch/worktree ownership: shared checkout `main`; coordinator owns integration and `docs/execution/state.json`.
- Owned paths: `internal/adapters/arr/read/`, `tests/fixtures/arr/read/`, and this handoff only.
- Product commit: `31243c71d90717ffa8ca7404817fca541b8ba935` (`fix(arr): close X-03 round-six read gaps`).
- Required acceptance contributions: A-07, A-09, A-10, A-11 and A-16.

## Contract and work

The correction closes the four findings from review receipt `1afbd30d` while retaining the Arr read-only and preview/reprocessing boundary:

- Reprocess subtitle quality exceptions are bound to the exact root-relative source role. A caller cannot mark an `.mkv` as a subtitle to bypass typed quality validation. Retained native absolute and relative paths must both identify the same subtitle role, and native mapping rejects a video candidate for a subtitle request. Supported subtitle paths keep the documented no-quality exception for both Radarr and Sonarr.
- Custom-format validation carries the selected Arr product through specifications, fields and select options. Radarr accepts its pinned `SelectOption.dividerAfter` field; Sonarr rejects that Radarr-only nested field before manual-import POST construction.
- Radarr movie-file decoding now requires the file's own `movieId` to match the catalog or observation parent, for both embedded and fallback `/moviefile` reads. Sonarr catalog and observation episode decoding requires every present `seriesId` to match the selected series; missing, malformed or contradictory ownership is retained as partial/unknown evidence with stable reason codes.
- Sonarr episode identity is indexed even for byte-equivalent rows. An exact repeated episode row now records `episode_identity_duplicate`, while distinct episodes on one physical file remain valid and one episode appearing on different files remains `episode_identity_conflict`.

The product change includes synthetic adversarial coverage for direct and native-derived subtitle requests, product-specific nested custom formats, embedded and fallback Radarr parent identity, Sonarr catalog and observation identity, and exact duplicate episode rows. Existing bounded pagination, snapshot, path, download, native DTO, season-pack, anime and IDX/SUB safeguards remain in place.

No command endpoint, automatic import, filesystem action or live upstream was used. No credentials, private coordinates, inventories or media payloads were introduced.

## Verification

| Command or scenario | Result / exit status | Evidence |
| --- | --- | --- |
| `gofmt -w internal/adapters/arr/read/client.go internal/adapters/arr/read/client_test.go` | passed, exit 0 | Owned Go files |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/arr/read` | passed, exit 0 | Focused Arr suite |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/arr/read` | passed, exit 0 | Focused race suite |
| `GOWORK=off go vet -mod=readonly ./internal/adapters/arr/read` | passed, exit 0 | Focused vet |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | passed, exit 0 | Root suite, including concurrent adapter packages |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | passed, exit 0; storage completed in 205.529s | Root race suite |
| `GOWORK=off go vet -mod=readonly ./...` | passed, exit 0 | Root vet |
| UI and tools `GOWORK=off go test -mod=readonly -count=1 ./...` and vet | passed, exit 0 | Nested modules |
| Root, UI and tools `GOWORK=off go mod verify` | passed, exit 0 | Dependency verification |
| `./scripts/generate.sh --check` | passed, exit 0 | Generated artifacts |
| `./scripts/check-api.sh` | passed, exit 0; Vacuum 100/100 | API contract |
| `python3 scripts/check_planning.py` | passed, exit 0; 38 tasks/60 acceptance cases | Planning links and ledger |
| `python3 scripts/check-architecture.py` | passed, exit 0 | Import boundaries |
| `./scripts/check-guardrails.sh --fast` | passed, exit 0 | Guardrail checks |
| `git diff --check` and staged-name inspection | passed, exit 0 | Product index contained only five owned paths |
| Product pre-commit hook | passed, exit 0 | Product commit `31243c71` |
| Subtitle role and quality probes | passed | `.mkv` relabeling is invalid with zero POST; supported `.srt` direct/native role keeps the exception for Radarr and Sonarr |
| Nested custom-format product probe | passed | Radarr `dividerAfter` accepted; Sonarr rejected before POST |
| Radarr movie-file parent identity probe | passed | Embedded and fallback matching files succeed; missing, malformed and contradictory `movieId` remain partial/unknown |
| Sonarr series identity probe | passed | Matching catalog/observation succeeds; missing, malformed and contradictory `seriesId` remain partial/unknown |
| Sonarr duplicate episode probe | passed | Exact duplicate is reason-coded; distinct multi-episode evidence remains accepted |

All test data is synthetic. Concurrent Jellyfin, Seerr, catalog, storage and coordinator state edits were preserved outside the product staging scope.

## Review and integration

- Reviewer: `/root/d01_reviewer`, independent of the implementer.
- Review source and findings: receipt `1afbd30d` identified caller-controlled subtitle quality bypass, Sonarr acceptance of Radarr-only nested `dividerAfter`, wrong-title file ownership, and exact duplicate Sonarr episode rows.
- Fix product: `31243c71d90717ffa8ca7404817fca541b8ba935`.
- Handoff commit: pending; this file is intentionally committed separately from product.
- Final reviewer decision: pending independent review of the exact product commit.
- Coordinator integration and execution-state update: pending; this lane did not edit or stage `docs/execution/state.json`.

## Resume checkpoint

- Product is ready at `31243c71d90717ffa8ca7404817fca541b8ba935`; ordinary Arr history remains conservative because the pinned endpoint has no immutable snapshot boundary.
- Next safe action: independently replay the four round-six adversarial scenarios against the exact product SHA and record the receipt in the coordinator ledger.
- Remaining review risks: verify the product-specific nested custom-format graph against future pinned Arr schema changes, and retain the rule that source and native path roles must agree before any subtitle quality exception.
- No conflicting files were removed, reset or staged. Concurrent X-04 Jellyfin/Seerr edits remain outside this handoff's staging scope.
