# X-03 correction handoff, round three

## Assignment and ownership

- Task: X-03, implement Arr inventory, lookup and native previews.
- Implementer: `/root/c01_implementer`; independent reviewer: `/root/d01_reviewer`.
- Correction base product: `b77aa5275a632246b76b009722719bc56e3f0448`.
- Independent review receipt: `aa63124c60364f6f36103f74d4c529c9e6a2640a`.
- Product parent at commit time: `ece8201b0502f8e4eb7930fd290600e3f7b59ba0`.
- Product commit: `e01deb6d90662e34d67cbd8190daf1c553035217` (`fix(arr): close X-03 round-three read gaps`).
- Shared checkout: `main`; coordinator owns integration and execution state.
- Owned paths: `internal/adapters/arr/read/`, `tests/fixtures/arr/read/`, and this handoff.
- No `docs/execution/state.json`, shared contract, storage, NZBGet or UI path was staged.

## Corrections and evidence

- Sonarr inventory and `ObserveImport` now use one helper that always sends
  `includeEpisodeFile=true` with `seriesId`. The fixture handler rejects an
  episode read without that exact flag, and the multi-episode inventory plus
  observation test checks every recorded query.
- History records unique accepted IDs across pages. An overlapping page adds
  `history_overlap`, suppresses the repeated row, and leaves terminal coverage
  partial with `ObservedCount` equal to the unique accepted rows. The new
  adversarial test proves a stable lying two-row total cannot become complete.
- Catalog cursors retain an exact identity prefix and then a signed, bounded
  32 KiB membership filter. The filter continues overlap detection after the
  former 2,048-ID prefix; a late duplicate is suppressed and emits
  `catalog_overlap`. The 2,050-unique-plus-late-duplicate test proves unique
  output and partial terminal coverage.
- Reprocessing validates duplicate physical sources by `rootId` plus canonical
  `relativePath`, independently of media or episode IDs, before serializing or
  POSTing any DTO. The adversarial Sonarr test records zero upstream POSTs for
  two rows targeting one physical source.
- `PreviewImportForReprocess` is a product-specific read-only bridge that
  returns common preview evidence plus a typed `ReprocessPreviewRequest`. It
  retains native ID/path, source download, subtitle classification and
  reviewed subtitle intent, quality, typed language objects, custom formats,
  scores, indexer flags, release group/type, season, complete episode records,
  and anime/absolute numbering. `ReprocessRequestFromNativePreview` provides
  the same mapping for a persisted native GET body. Nested quality, language
  and custom-format values are bounded/validated before becoming reprocess
  fields; malformed or untyped native values remain rejection evidence.
- The detailed native fields and raw language digests are included in the
  reprocess revision, so changing native source, download, subtitle, quality,
  language, episode, release or scoring context changes the reviewed binding.
  The mapping retains one detailed row per physical source even when a common
  season-pack selection names several episodes; `ReprocessPreview` therefore
  receives one canonical row.
- IDX/SUB pair identity is explicit and caller-supplied through
  `NativePreviewRequest.SubtitlePairIDs`, keyed with the exported
  `NativeSourceKey`. `ReprocessRequestFromNativePreviewWithPairs` copies the
  same ID to both retained `.idx` and `.sub` rows and binds it into the
  revision. The tests cover a positive pair, an unpaired same-stem candidate
  with no inferred ID, and a one-member pair rejected before mapping.
- No Arr command endpoint was added or called. Existing redirect refusal and
  native rejection/path/association checks remain in force.

## Verification

| Check | Exit | Evidence |
| --- | ---: | --- |
| `gofmt -w internal/adapters/arr/read/client.go internal/adapters/arr/read/client_test.go` | 0 | Final owned Go files formatted. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/arr/read` | 0 | Focused inventory, options, lookup, history, cancellation, exact path/association, native mapping, duplicate-source, subtitle pair and no-command regressions. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/arr/read` | 0 | Focused race suite passed after final product edits. |
| `GOWORK=off go vet -mod=readonly ./internal/adapters/arr/read` | 0 | Owned adapter vet passed after final product edits. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | 0 | Root packages, including storage and generated package, passed after final product edits. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./...` | 0 | Root race suite passed; storage completed in 90.182s. |
| `GOWORK=off go vet -mod=readonly ./...` | 0 | Root vet passed after final product edits. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` in `ui/` | 0 | UI module tests passed. |
| `GOWORK=off go vet -mod=readonly ./...` in `ui/` | 0 | UI module vet passed. |
| `GOWORK=off go mod verify` in root, `ui/`, and `tools/` | 0 | All modules verified. |
| `./scripts/generate.sh --check` | 0 | Generated artifacts clean. |
| `./scripts/check-api.sh` | 0 | OpenAPI checks passed; Vacuum quality 100/100. |
| `./scripts/check-guardrails.sh --fast` | 0 | Guardrails passed. |
| `python3 scripts/check_planning.py` | 0 | 38 tasks, 60 acceptance cases and local links valid. |
| `python3 scripts/check-architecture.py` | 0 | Import boundaries passed. |
| JSON parse of `tests/fixtures/arr/read/*.json` | 0 | 19 synthetic Arr fixtures parsed. |
| `git diff --check` and staged diff check for owned product | 0 | No whitespace errors. |
| `./.githooks/pre-commit` and commit hook during product commit | 0 | Generation, API, architecture, domain/ports/qBittorrent and fast guardrails passed. |

The shared checkout still contains the coordinator's unstaged
`docs/execution/state.json` edit. It was not read into the product diff or
staged by this lane. Product commit `e01deb6d90662e34d67cbd8190daf1c553035217`
contains only the two Arr Go files; this handoff is committed separately.
All fixtures and tests use synthetic data and `httptest`; no live Arr service,
credential, private endpoint, media payload, native command, filesystem
mutation, release or deployment was used.

## Acceptance contribution and remaining review risks

- A-07: round-three closes the late duplicate and Sonarr file-detail evidence
  gaps; Arr inventory still stays partial on snapshot drift, bounds or
  conservative filter hits.
- A-09: connection-scoped IDs, mappings, cursors and revisions remain covered.
- A-10: exact season-pack episode sets map to one physical reprocess row and
  native episode/absolute numbering survives the typed bridge.
- A-11: native subtitle source/classification and explicit IDX/SUB pair IDs
  survive mapping; forced/SDH remains unknown when Arr omits evidence.
- A-16: duplicate history and read-back evidence remain conservative, and no
  command execution is exposed; native no-overwrite execution remains X-05.

The membership filter is intentionally conservative and can mark an unusual
hash collision as overlap/partial; it never claims complete coverage on that
signal. Arr does not provide a trustworthy IDX/SUB companion key in the native
candidate, so pairing requires explicit reviewed `SubtitlePairIDs`; basename
inference remains forbidden. Stable history insertion drift cannot be proven
when Arr omits a snapshot token and remains a review risk. The independent
reviewer should rerun the exact product commit before coordinator integration.

## Resume checkpoint

- Product is ready at `e01deb6d90662e34d67cbd8190daf1c553035217`.
- Next safe action: coordinator stages only this handoff, records its commit
  and product SHA in execution state, then requests independent review.
- No conflicting files were removed, reset, rebased, cleaned or force-updated.
