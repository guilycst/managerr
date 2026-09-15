# X-17 correction round one: Jellyfin evidence safety

## Assignment

- Task ID and title: X-17 correction round one, close standalone Jellyfin
  client review findings.
- Owner/agent and independent reviewer: `/root/x05_implementer`; independent
  reviewer `/root/x05_reviewer` (coordinator assignment pending).
- Base commit: `e5e9a944a39841587b9a15d97dcb4b8d1b10a3b1`.
- Branch/worktree or shared-checkout ownership, repository-relative public notation:
  shared checkout on `main`; coordinator owns `docs/execution/state.json` and
  integration.
- Owned files and generated outputs: `clients/jellyfin/`, including
  regenerated `clients/jellyfin/internal/generated/client.gen.go`, and this
  handoff at `docs/execution/handoffs/X-17-correction-round1.md`.
- Dependencies verified at commits: original X-17 product
  `f9e0754853e1855af37e61ac96c8bc70a7e017d4`; original handoff
  `9eb1edadf323fccd3278a30a0f19a765cbf63de9`; prior independent review
  `390ada5463f939d7a1b6dacced762c1f05ad873e` (integrated at
  `7994b04f21d7bcefed70f1601b1406825e908d04`).
- Required acceptance IDs and exact planned commands: A-08, A-09, A-45 and
  A-55; standalone module tests/race/vet/module verification, offline
  generation and reproducibility, Vacuum, lint, architecture, planning,
  guardrails and Linux amd64/arm64 CGO-free builds.

## Contract and work

- Linked specification sections and relevant invariants: X-17 in
  `docs/plans/implementation.md`; Jellyfin in
  `docs/specs/spec-001-media-reconciliation/connectors.md`; A-08, A-09, A-45
  and A-55 in `docs/verification/acceptance.md`; prior findings are recorded
  in `docs/execution/handoffs/X-17-review-round1.md`. Missing or contradictory
  evidence remains unknown or malformed, and refresh acceptance remains
  separate from later availability.
- Intended result and capability limits: close review R1-R4 while keeping the
  independent Jellyfin module, generated boundary, read observations and
  explicit refresh acceptance. No registration, Arr import, search, file
  operation or automatic refresh was added. A single exact item lookup may
  report an authoritative empty result as `not_found`; foreign identity never
  proves absence.
- Changes completed / remaining:
  - R1: validate native `StartIndex`, `TotalRecordCount` and returned count;
    reject impossible totals; keep missing totals and gaps unknown; recognize
    a proven tail as complete; keep limited array responses unknown unless
    they are a single exact-ID lookup; traverse partial pages without
    upgrading them, preserve observed items on interruption, and mark
    multi-page results partial because Jellyfin has no immutable snapshot.
  - R2: validate every normalized item against explicit `ItemQuery.ItemIDs`
    before exposing it; foreign or mixed responses are malformed. `ObserveItem`
    returns `not_found` only for a complete, valid empty exact lookup and
    returns unknown for incomplete absence evidence.
  - R3: reject invalid UTF-8 before JSON token walking or decoding, while
    retaining valid Unicode in identities, names and provider values.
  - R4: replace fictional `/Users/Me/Views` fallback with canonical
    `/UserViews`; send optional `userId` query for explicit context and use
    token user context when no user is configured. The OpenAPI 3.1.1 contract
    and generated code now describe this route.
  Remaining work is coordinator-owned C-06 matrix/bootstrap and X-09/X-21
  adapter/version gates.
- Shared contract changes requested from coordinator: add `clients/jellyfin`
  to aggregate generation, lint, test, vet, module-verification, cross-build
  and CI matrices in C-06, then publish a consumable module version or use the
  approved bootstrap procedure. Preserve the canonical `/UserViews` route and
  exact evidence/coverage semantics when migrating the root adapter.
- Changed paths and tested commit: product commit
  `d6b84779c8b52b07ccd2e11093b089addca72002` (`fix(jellyfin): close inventory
  evidence gaps`).

## Verification

All checks used synthetic `httptest` responses. No live Jellyfin endpoint,
credential, private coordinate, inventory, media data or file operation was
used.

| Command or scenario | Commit / fixture version | Result / exit status | Evidence path |
| --- | --- | --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 -timeout=60s ./...` from `clients/jellyfin` | `d6b8477`; synthetic fixtures | Passed, exit 0 | `clients/jellyfin/client_test.go` |
| `GOWORK=off go test -mod=readonly -race -count=3 -timeout=180s ./...` from `clients/jellyfin` | `d6b8477`; repeated synthetic fixtures | Passed, exit 0 | `clients/jellyfin/client_test.go` |
| `GOWORK=off go vet -mod=readonly ./...` from `clients/jellyfin` | `d6b8477` | Passed, exit 0 | `clients/jellyfin/*.go` |
| `GOWORK=off go mod verify` from `clients/jellyfin` | `d6b8477` | Passed: `all modules verified` | `clients/jellyfin/go.mod`, `clients/jellyfin/go.sum` |
| `GOWORK=off GOPROXY=off GOSUMDB=off go generate ./...` from `clients/jellyfin` | `d6b8477`; pinned tools module | Passed; committed generated output unchanged | `clients/jellyfin/generate.go`, `clients/jellyfin/internal/generated/client.gen.go` |
| `GOWORK=off GOPROXY=off GOSUMDB=off ./check-generation.sh` from `clients/jellyfin` | `d6b8477` | Passed: `Jellyfin generation checks passed` | `clients/jellyfin/check-generation.sh` |
| Pinned Vacuum command against `openapi.yaml` with `--remote=false --fail-severity=warn` | `d6b8477`; OpenAPI 3.1.1 `/UserViews` contract | Passed, quality 100/100, zero warnings/errors, exit 0 | `clients/jellyfin/openapi.yaml` |
| Pinned golangci-lint through `tools/go.mod` with repository config | `d6b8477` | Passed: `0 issues.`, exit 0 | `clients/jellyfin/*.go` |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/jellyfin` | `d6b8477` | Passed, exit 0 | `clients/jellyfin` |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/jellyfin` | `d6b8477` | Passed, exit 0 | `clients/jellyfin` |
| `./scripts/check-guardrails.sh --ci` from repository root | `d6b8477`; aggregate repository fixtures and matrices | Passed: API/Vacuum quality 100/100, generation, architecture, lint, tests, vet and module verification; exit 0 | `scripts/check-guardrails.sh` |
| `TestPaginationCoverageRetainsUncertainty` | `d6b8477`; impossible total, missing total, tail, short traversal, page limit and interrupted traversal | Passed; no incomplete result was complete and observed items survived interruption | `clients/jellyfin/client_test.go` |
| `TestRequestedScopeRejectsForeignAndMixedEvidence` and updated `TestObserveItemRejectsForeignAndMissingIdentity` | `d6b8477`; aliases, explicit IDs, multi-page foreign item and valid empty exact lookup | Passed; foreign/mixed evidence malformed and valid empty exact lookup remained `not_found` | `clients/jellyfin/client_test.go` |
| `TestInvalidUTF8RejectedAndUnicodePreserved` | `d6b8477`; item/library/provider/unknown-field invalid bytes and valid Japanese/CJK values | Passed; invalid bytes rejected before normalization and valid Unicode retained | `clients/jellyfin/client_test.go` |
| `TestLibrariesAcceptEnvelopeAndFallbackToUserViews` | `d6b8477`; explicit `userId` and token-context `/UserViews` fixtures | Passed; no `/Users/Me/Views` request was made | `clients/jellyfin/client_test.go` |
| Product pre-commit hook and `git diff --check` | `d6b8477` | Passed; working tree clean before handoff | `.githooks/pre-commit`, owned paths |

The aggregate guardrail script still omits `clients/jellyfin`; direct module
checks above are the lane evidence until C-06 updates that matrix.

## Review and integration

- Reviewer identity/role and reviewed commit: `/root/x05_reviewer`, prior
  independent review `390ada5463f939d7a1b6dacced762c1f05ad873e` against the
  original product; correction review pending against
  `d6b84779c8b52b07ccd2e11093b089addca72002`.
- Findings with severity, reproduction and contract reference: prior R1/R2
  P1 and R3/R4 P2 findings are addressed above with executable regressions;
  final independent disposition is pending.
- Fix commit and regression evidence: `d6b84779c8b52b07ccd2e11093b089addca72002`;
  focused regressions and full module/race/guardrail results are listed above.
- Final reviewer decision: pending.
- Integrated commit, recorded by coordinator: pending; this lane did not edit
  `docs/execution/state.json`.

## Resume checkpoint

- Current state and outstanding uncertainty: product correction is complete;
  generated output is reproducible; working tree was clean after product
  commit. Jellyfin release pin, native capability matrix, root adapter
  migration and aggregate C-06 inclusion remain open gates.
- Active process or agent ownership, sanitized: no active process; reviewer
  and coordinator own the next review/state steps.
- Next safe action: commit this correction handoff separately, assign
  independent review against the exact product SHA, then record both SHAs and
  the review result in coordinator state.
- Blocker and exact input/evidence needed: C-06 needs module matrix/bootstrap
  evidence; X-09/X-21 need a versioned Jellyfin fixture and root translation
  that preserve refresh acceptance versus later availability. G-01 remains
  open; no Arr write capability was enabled.
- No conflicting writes or unknown files removed: only `clients/jellyfin/`
  and this correction handoff are owned here; no state, root adapter, other
  client, UI, script or unrelated file was changed.
