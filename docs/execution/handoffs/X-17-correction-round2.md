# X-17 correction round two: require pagination authority

## Assignment

- Task ID and title: X-17 correction round two, close missing Jellyfin
  pagination metadata findings.
- Owner/agent and independent reviewer: `/root/x05_implementer`; independent
  reviewer `/root/x05_reviewer` (coordinator assignment pending).
- Base commit: `3d61e21a57c7b5ab2996b9616f41b0a3e6b6a2b2`.
- Branch/worktree or shared-checkout ownership, repository-relative public notation:
  shared checkout on `main`; coordinator owns `docs/execution/state.json` and
  integration.
- Owned files and generated outputs: `clients/jellyfin/` and this handoff at
  `docs/execution/handoffs/X-17-correction-round2.md`. Generated output remains
  committed at `clients/jellyfin/internal/generated/client.gen.go`.
- Dependencies verified at commits: prior correction product
  `d6b84779c8b52b07ccd2e11093b089addca72002`; prior correction handoff
  `acf25a416d7f09d8459e945c3d4789029885bd1f`; independent review receipt
  `942248df68f0c2027a260513c7b913fe990e7ccf`, integrated at
  `7f3e8ed12da956a1154bb4ffd202aae5b7e3c8f8`.
- Required acceptance IDs and exact planned commands: A-08, A-09, A-45 and
  A-55; standalone module tests/race/vet/module verification, offline
  generation and reproducibility, Vacuum, lint, architecture, planning,
  guardrails and Linux amd64/arm64 CGO-free builds.

## Contract and work

- Linked specification sections and relevant invariants: X-17 in
  `docs/plans/implementation.md`; Jellyfin in
  `docs/specs/spec-001-media-reconciliation/connectors.md`; A-08, A-09, A-45
  and A-55 in `docs/verification/acceptance.md`; prior findings are recorded
  in `docs/execution/handoffs/X-17-review-round2.md`. Missing evidence stays
  unknown, and `not_found` requires authoritative complete scoped evidence.
- Intended result and capability limits: close R1a and R2a while preserving
  standalone module isolation, generated DTO boundary, read observations,
  canonical `/UserViews` fallback, explicit refresh acceptance and no Arr or
  filesystem writes. Native item pages only claim complete coverage when
  required boundary metadata proves it.
- Changes completed / remaining:
  - R1a: retain `StartIndex` presence separately from its numeric value.
    Missing or explicit-null envelope offsets now return items with unknown
    coverage and `pagination_start_missing`; offset mismatches and impossible
    totals remain malformed. Explicit native start and total values establish
    valid tail/full-page completeness; partial pages continue safely, bounded
    page limits remain partial, and interrupted reads retain prior observations
    with unknown coverage.
  - R2a: remove the prior exact-ID empty-envelope exception for missing
    `TotalRecordCount`. Missing or explicit-null totals remain unknown even
    when `Items` is empty, including `Items`, `ObserveItem`, `GetItem` and
    `ListAllItems`. An exact empty envelope reports `not_found` only with
    `TotalRecordCount: 0` and `StartIndex: 0`; the separately compatible full
    array shape retains its exact-ID empty behavior.
  - Added deterministic regressions for missing/null start on first and
    offset requests, missing/null total exact empty aliases and traversal,
    valid zero-total exact empty aliases, while retaining prior foreign/mixed
    scope, pagination, UTF-8 and canonical fallback regressions.
  Remaining work is coordinator-owned C-06 matrix/bootstrap and X-09/X-21
  adapter/version gates.
- Shared contract changes requested from coordinator: add `clients/jellyfin`
  to aggregate generation, lint, test, vet, module-verification, cross-build
  and CI matrices in C-06. Preserve required start/total evidence and canonical
  `/UserViews` semantics when migrating the root adapter.
- Changed paths and tested commit: product commit
  `4eee8e84eff5a81b24c128444fa6531fec83f5ec` (`fix(jellyfin): require
  explicit page metadata`).

## Verification

All checks used synthetic `httptest` responses. No live Jellyfin endpoint,
credential, private coordinate, inventory, media data or file operation was
used.

| Command or scenario | Commit / fixture version | Result / exit status | Evidence path |
| --- | --- | --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 -timeout=60s ./...` from `clients/jellyfin` | `4eee8e8`; synthetic fixtures | Passed, exit 0 | `clients/jellyfin/client_test.go` |
| `GOWORK=off go test -mod=readonly -race -count=3 -timeout=180s ./...` from `clients/jellyfin` | `4eee8e8`; repeated synthetic fixtures | Passed, exit 0 | `clients/jellyfin/client_test.go` |
| `GOWORK=off go vet -mod=readonly ./...` from `clients/jellyfin` | `4eee8e8` | Passed, exit 0 | `clients/jellyfin/*.go` |
| `GOWORK=off go mod verify` from `clients/jellyfin` | `4eee8e8` | Passed: `all modules verified` | `clients/jellyfin/go.mod`, `clients/jellyfin/go.sum` |
| `GOWORK=off GOPROXY=off GOSUMDB=off go generate ./...` from `clients/jellyfin` | `4eee8e8`; pinned tools module | Passed; generated output unchanged | `clients/jellyfin/generate.go`, `clients/jellyfin/internal/generated/client.gen.go` |
| `GOWORK=off GOPROXY=off GOSUMDB=off ./check-generation.sh` from `clients/jellyfin` | `4eee8e8` | Passed: `Jellyfin generation checks passed` | `clients/jellyfin/check-generation.sh` |
| Pinned Vacuum through `tools/go.mod`, repository ruleset, `--remote=false --fail-severity=warn` | `4eee8e8`; OpenAPI 3.1.1 contract | Passed, quality 100/100, zero warnings/errors, exit 0 | `clients/jellyfin/openapi.yaml` |
| Pinned golangci-lint through `tools/go.mod` with repository config | `4eee8e8` | Passed: `0 issues.`, exit 0 | `clients/jellyfin/*.go` |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/jellyfin` | `4eee8e8` | Passed, exit 0 | `clients/jellyfin` |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/jellyfin` | `4eee8e8` | Passed, exit 0 | `clients/jellyfin` |
| `./scripts/check-guardrails.sh --ci` from repository root | `4eee8e8`; aggregate repository fixtures and matrices | Passed: API/Vacuum quality 100/100, generation, architecture, lint, tests, vet and module verification; exit 0 | `scripts/check-guardrails.sh` |
| `TestMissingPaginationMetadataCannotProveCompletenessOrAbsence` | `4eee8e8`; missing/null start, missing/null total, complete zero-total exact empty, aliases and traversal | Passed under `-race -count=3`; incomplete metadata never established complete coverage or absence | `clients/jellyfin/client_test.go` |
| `TestPaginationCoverageRetainsUncertainty` | `4eee8e8`; impossible total, missing total, tail, short traversal, page limit and interruption | Passed under `-race -count=3`; partial/unknown states and prior items retained | `clients/jellyfin/client_test.go` |
| `TestRequestedScopeRejectsForeignAndMixedEvidence` and `TestObserveItemRejectsForeignAndMissingIdentity` | `4eee8e8`; aliases, foreign/mixed IDs, multi-page foreign item and valid empty lookup | Passed; foreign evidence malformed and complete empty exact lookup remained `not_found` | `clients/jellyfin/client_test.go` |
| Product pre-commit hook and `git diff --check` | `4eee8e8` | Passed; product tree clean before handoff | `.githooks/pre-commit`, owned paths |

The aggregate guardrail script still omits `clients/jellyfin`; direct module
checks above are lane evidence until C-06 updates that matrix.

## Review and integration

- Reviewer identity/role and reviewed commit: `/root/x05_reviewer`, prior
  review `942248df68f0c2027a260513c7b913fe990e7ccf` against the previous
  correction; independent correction review pending against
  `4eee8e84eff5a81b24c128444fa6531fec83f5ec`.
- Findings with severity, reproduction and contract reference: R1a and R2a
  P1 findings are addressed with presence-aware decoding and executable
  missing/null metadata regressions; final independent disposition is pending.
- Fix commit and regression evidence: `4eee8e84eff5a81b24c128444fa6531fec83f5ec`;
  focused, race, module, generation, Vacuum, lint, cross-build and root
  guardrail results are listed above.
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
  preserving start/total coverage authority and refresh acceptance versus later
  availability. G-01 remains open; no Arr write capability was enabled.
- No conflicting writes or unknown files removed: only `clients/jellyfin/`
  and this correction handoff are owned here; no state, root adapter, other
  client, UI, script or unrelated file was changed.
