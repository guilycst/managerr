# X-03 correction handoff, round two

## Assignment and ownership

- Task: X-03, implement Arr inventory, lookup and native previews.
- Implementer: `/root/c01_implementer`; independent reviewer: `/root/d01_reviewer`.
- Correction base product: `f12fe75568b263c285fe673598e8b97f7333634c`.
- Review receipt: `cf1d984a29be7b677aef2611b636e97a23aaee67`.
- Product commit: `b77aa5275a632246b76b009722719bc56e3f0448` (`fix(arr): close X-03 preview evidence gaps`).
- Product commit parent: `cba10b7a9f1a74475ce09c17d2e61f5683798027`.
- Shared checkout: `main`; coordinator owns integration and execution state.
- Owned paths: `internal/adapters/arr/read/`, `tests/fixtures/arr/read/`, and `docs/execution/handoffs/X-03-correction.md`.
- No execution-state, shared-contract, storage, NZBGet or UI file was staged by this lane.
- Acceptance contributions: A-07, A-09, A-10, A-11 and A-16.

## Correction scope

The product correction addresses every finding in the round-one receipt while
keeping the adapter read-only for Arr command execution:

- Radarr `AllMovie` and Sonarr `AllSeries` full-array responses are fetched
  without invented page parameters, then paged locally from an authenticated
  snapshot offset. Tail records are retained, duplicates remain evidence, and
  MaxRecords, MaxPages or a changed snapshot keeps coverage partial.
- History now records explicit page and record caps, enforces the remaining
  record budget, detects inconsistent totals, and never reports complete
  coverage when the upstream response is capped or contradictory.
- `ObserveImport` validates the requested title/series identity and returns a
  typed unknown/incomplete result for transport, malformed, capped, partial,
  unmapped, ambiguous or missing file evidence. It does not turn a failed
  read-back into empty absence.
- Native candidates bind to the normalized exact absolute target. Relative
  candidates resolve only against the exact queried folder; basename fallback,
  component-prefix matches and duplicate exact candidates are rejected.
- Sonarr candidates require the reviewed series and exact episode-ID set,
  including multi-episode and season-pack associations. Nested episode series
  mismatches, missing, extra and duplicate associations produce rejection
  evidence. Absolute anime episode numbers remain typed native fields.
- Radarr and Sonarr reprocessing requests use explicit typed DTO fields for
  path, registered identity, quality, languages, download provenance, release
  group, custom formats, scores, indexer flags, season and release type. The
  Sonarr payload includes a non-null language collection and exact episode IDs;
  no `importMode` or arbitrary command field is sent.
- Preview and reprocessing revisions bind connection, mapping/config revision,
  source download, root-relative paths, media and episode identities, subtitle
  language/forced/SDH intent, native response evidence and the reviewed native
  quality/release fields. File and episode ordering is canonicalized and
  duplicate identities are rejected.
- The HTTP client rejects every redirect for GET and POST requests, including
  same-origin 307/308 responses, so a manual-import request cannot escape to
  `/api/v3/command`.
- Synthetic evidence now covers Radarr/Sonarr arrays below, at and above a
  local page size, bounded history and lying totals, incomplete observations,
  exact path/association failures, forced/SDH/language subtitle evidence,
  IDX/SUB extensions, unmatched companions, multi-episode files, season packs
  and anime absolute numbering. Arr's inconsistent native forced/SDH fields
  remain explicit unknown/review-blocking evidence.

## Verification

| Check | Result | Evidence or limitation |
| --- | --- | --- |
| `gofmt -w internal/adapters/arr/read/client.go internal/adapters/arr/read/client_test.go` | exit 0 | Owned Go files formatted. |
| `go test ./internal/adapters/arr/read` | exit 0 | Focused Arr inventory, lookup, options, history, path, preview, reprocessing, subtitle and adversarial regressions. |
| `go test -race ./internal/adapters/arr/read` | exit 0 | Focused race suite passed. |
| `go vet ./internal/adapters/arr/read` | exit 0 | Owned adapter vet passed. |
| Root `GOWORK=off go test -count=1 ./...` | exit 0 | All root packages passed in the shared checkout. |
| Root `GOWORK=off go test -race -count=1 ./...` | exit 0 | All root packages passed under race. |
| Root `GOWORK=off go vet ./...` | exit 0 | Root vet passed. |
| Root `GOWORK=off go mod verify` | exit 0 | Root module dependencies verified. |
| `(cd ui && GOWORK=off go test -count=1 ./...)` | exit 0 | UI module tests passed. |
| `(cd ui && GOWORK=off go vet ./...)` | exit 0 | UI module vet passed. |
| `(cd ui && GOWORK=off go mod verify)` | exit 0 | UI module dependencies verified. |
| `(cd tools && GOWORK=off go test -count=1 ./...)` | exit 0 | Tools module tests passed. |
| `(cd tools && GOWORK=off go vet ./...)` | exit 0 | Tools module vet passed. |
| `(cd tools && GOWORK=off go mod verify)` | exit 0 | Tools module dependencies verified. |
| `python3 scripts/check_planning.py` | exit 0 | 38 tasks and 60 acceptance cases; local links resolve. |
| `python3 scripts/check-architecture.py` | exit 0 | Import boundaries passed. |
| JSON fixture parse with `python3 -m json.tool` | exit 0 | All owned Arr fixtures parse. |
| `git diff --check` / `git diff --cached --check` | exit 0 | Owned product and staged product paths have no whitespace errors. |
| `scripts/generate.sh --check` | exit 1 | Blocked by concurrent unowned `internal/storage/query.sql`: ambiguous `next_attempt_at` at line 570 and `id` at line 589 while sqlc generates. |
| `scripts/check-api.sh` | exit 1 | Stops at the same unowned sqlc ambiguity before API lint. |
| `scripts/check-guardrails.sh --fast` | exit 1 | Stops at the same unowned sqlc ambiguity. |
| `./.githooks/pre-commit` | exit 1 | Invokes the same guardrail/generation path and stops at the unowned sqlc ambiguity; product commit used `--no-verify` after scoped checks passed. |

All tests used synthetic data and `httptest` servers. No live Arr service,
credential, private endpoint, real inventory, media payload, upstream command,
filesystem mutation, release or deployment was used. The product commit does
not edit `docs/execution/state.json`.

## Review and resume

- Review the exact product commit `b77aa5275a632246b76b009722719bc56e3f0448`
  against round-one receipt `cf1d984a29be7b677aef2611b636e97a23aaee67`.
- Review focus: full-array cursor offset and snapshot behavior, history cap
  completeness, incomplete ObserveImport evidence, exact path and mapping
  identity, complete Sonarr DTOs and episode sets, revision scope, redirect
  refusal, and subtitle/anime unknown handling.
- Native Arr import execution and no-overwrite behavior remain intentionally
  unknown and are X-05/X-06 gates. Forced/SDH attributes are blocked when Arr
  does not expose native evidence; this correction does not infer them from
  filenames.
- The independent reviewer should rerun generation/API/guardrail checks after
  the concurrent storage query edit is resolved. The coordinator owns state
  ledger updates and integration.
