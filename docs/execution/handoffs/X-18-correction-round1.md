# X-18 correction round one: Seerr certainty and transport safety

## Assignment

- Task ID and title: X-18 correction round one, close Seerr certainty,
  deadline and service-evidence findings.
- Owner/agent and independent reviewer: `/root/x05_implementer`; independent
  reviewer `/root/x05_reviewer`.
- Base commit: `00c1b9c9fcb90642e6bdf6795894eb9589ef255d`.
- Branch/worktree or shared-checkout ownership, repository-relative public
  notation: shared checkout on `main`; coordinator owns integration and
  `docs/execution/state.json`.
- Owned files and generated outputs: `clients/seerr/` and this handoff at
  `docs/execution/handoffs/X-18-correction-round1.md`. Generated output remains
  committed at `clients/seerr/internal/generated/client.gen.go` and is never
  hand-edited.
- Prior product and handoff: `67b01ca1d5555bfe11b348a96bda0b6a67d16bc3`
  and `7742877c37a0b8f0b6bd5d04ed01c0c4d7bceb16`.
- Prior independent review receipt:
  `d51e7fcedd730a6f58bdcce99e9fc926baaee5a5`.
- Required acceptance IDs and exact planned commands: A-08, A-09, A-45 and
  A-54; standalone module tests/race/vet/module verification, offline
  generation and reproducibility, Vacuum, lint, architecture, planning,
  guardrails and Linux amd64/arm64 CGO-free builds.

## Contract and work

- Linked specification sections and relevant invariants: X-18 in
  `docs/plans/implementation.md`; Seerr in
  `docs/specs/spec-001-media-reconciliation/connectors.md`; A-08, A-09, A-45
  and A-54 in `docs/verification/acceptance.md`; prior findings are recorded
  in `docs/execution/handoffs/X-18-review-round1.md`. Missing evidence stays
  unknown, service failures cannot become successful empty observations, and
  Seerr remains read-only in v0.0.1.
- Intended result and capability limits: preserve native Seerr status and
  independent availability evidence, enforce a bounded request context for
  every transport, and retain or reject malformed service relationships and
  `serviceErrors` safely. No writes, search, hidden discovery or generated DTO
  leakage were added.
- Changes completed / remaining:
  - `NativeStatusKnown` now means that Seerr supplied a numeric native status.
    `Availability.Known` is independent and is true only for a recognized
    status enum. Native `UNKNOWN` (`1`), future values and missing status keep
    the native numeric/presence evidence while remaining unknown availability;
    they never become known-unavailable media. Recognized pending,
    processing, partial, available, blocklisted and deleted values retain
    their known semantics. Nested request media follows the same rule.
  - Every request derives a child context with the configured request timeout,
    so a configured shorter timeout still applies under a longer caller
    deadline while an earlier caller deadline remains authoritative. Transport
    and body read failures preserve `context.Canceled` and
    `context.DeadlineExceeded` through `errors.Is`; other body failures remain
    sanitized upstream errors.
  - Service relationships validate manager kind, positive paired native
    service identities and bounded text. Zero is treated as Seerr's unbound
    sentinel and is never exposed as a normal tracked relationship. Invalid,
    incomplete or unknown relationships are omitted with explicit evidence,
    which prevents complete coverage from hiding the defect.
  - `serviceErrors` keys are restricted to the supported Arr kinds and are
    validated for text, identity, names and total collection bounds. Malformed
    evidence returns a sanitized malformed-response error instead of being
    skipped or truncated. A record with a name but no positive ID is retained
    with `IDKnown=false` so unknown native identity remains explicit.
  - README and public type comments document the separate native-presence and
    availability-certainty meanings. The OpenAPI contract and generated output
    remain byte-for-byte reproducible because this correction changes only
    handwritten normalization and transport behavior.
- Shared contract changes requested from coordinator: C-06 should add
  `clients/seerr` to repository generation, lint, architecture, test, vet,
  module-verification, cross-build and CI matrices. X-22 must publish and
  consume this nested module without a local replace before migrating the root
  Seerr adapter. A versioned disposable Seerr fixture and runtime capability
  gate remain integration work.
- Changed paths and tested commit: product commit
  `3dc53eee75cf296662a60b7422ec557692cdaee8`
  (`fix(seerr): harden availability and service evidence`).

## Verification

All HTTP fixtures are synthetic `httptest` handlers or in-memory transports.
No live Seerr endpoint, credential, private coordinate, inventory or media
data was used.

| Command or scenario | Commit / fixture version | Result / exit status | Evidence path |
| --- | --- | --- | --- |
| `GOWORK=off go test -mod=readonly -count=1 -timeout=90s ./...` from `clients/seerr` | `3dc53ee`; synthetic fixtures | Passed, exit 0 | `clients/seerr/client_test.go` |
| `GOWORK=off go test -mod=readonly -race -count=3 -timeout=180s ./...` from `clients/seerr` | `3dc53ee`; repeated synthetic fixtures | Passed, exit 0 | `clients/seerr/client_test.go` |
| `GOWORK=off go vet -mod=readonly ./...` from `clients/seerr` | `3dc53ee` | Passed, exit 0 | `clients/seerr/*.go` |
| `GOWORK=off go mod verify` from `clients/seerr` | `3dc53ee` | Passed: all modules verified | `clients/seerr/go.mod`, `clients/seerr/go.sum` |
| `GOWORK=off GOPROXY=off GOSUMDB=off go generate ./...` from `clients/seerr` | `3dc53ee`; pinned tools module | Passed; generated output unchanged | `clients/seerr/generate.go`, `clients/seerr/internal/generated/client.gen.go` |
| `GOWORK=off GOPROXY=off GOSUMDB=off ./check-generation.sh` from `clients/seerr` | `3dc53ee` | Passed: Seerr generation checks passed | `clients/seerr/check-generation.sh` |
| Pinned Vacuum through `tools/go.mod`, `lint --no-update-check --remote=false --ruleset ../../api/vacuum.yaml --fail-severity=warn` | `3dc53ee`; OpenAPI 3.1.1 contract | Passed; quality 100/100 with zero warnings/errors, exit 0 | `clients/seerr/openapi.yaml` |
| Pinned golangci-lint through `tools/go.mod` with repository configuration | `3dc53ee` | Passed: 0 issues, exit 0 | `clients/seerr/*.go` |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/seerr` | `3dc53ee` | Passed, exit 0 | `clients/seerr` |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./...` from `clients/seerr` | `3dc53ee` | Passed, exit 0 | `clients/seerr` |
| `python3 scripts/check-architecture.py` | `3dc53ee` product tree | Passed: architecture import boundaries | `scripts/check-architecture.py` |
| `python3 scripts/check_planning.py` | `3dc53ee` product tree | Passed: 53 tasks, 60 acceptance cases; local links resolve | `scripts/check_planning.py` |
| `./scripts/check-guardrails.sh --ci` | `3dc53ee` product tree | Passed: API/Vacuum, generation, architecture, lint, root tests/vet/module verification and guardrails | `scripts/check-guardrails.sh` |
| `TestAvailabilityCertaintyRequiresRecognizedNativeStatus`, `TestNestedUnknownAvailabilityRemainsUnknown` | Native missing, UNKNOWN=1, future=99, pending, processing, partial, available, blocklisted and deleted status fixtures | Passed under race; native presence is retained while unknown availability stays unknown for missing/UNKNOWN/future values, including nested media | `clients/seerr/client_test.go` |
| `TestServiceRelationshipsRejectInvalidEvidence` | Negative, control-character, zero-unbound, missing, unknown-kind and invalid-4K relationship fixtures plus valid controls | Passed under race; no invalid normal relationship is exposed and coverage retains explicit evidence | `clients/seerr/client_test.go` |
| `TestMalformedServiceErrorsFailClosed` | Invalid kind/text, unknown kind, negative identity, empty record, over-limit and unknown-ID service-error fixtures | Passed under race; malformed evidence is rejected and unknown identity is retained explicitly | `clients/seerr/client_test.go` |
| `TestRequestTimeoutBoundsLongParentAndPreservesShortParent` | In-memory transport with one-hour parent/20 ms client and 20 ms parent/one-hour client contexts | Passed under race; request deadline is always bounded by the earlier limit | `clients/seerr/client_test.go` |
| `TestTransportAndBodyContextErrorsPreserveIdentity` | In-memory transport/body cancellation and timeout fixtures, including wrapped transport context error | Passed under race; `errors.Is` preserves caller cancellation and request deadline identity | `clients/seerr/client_test.go` |
| Versioned pre-commit hook and `git diff --check` | `3dc53ee` | Passed; generation, API, architecture, focused root checks and fast guardrails passed | `.githooks/pre-commit`, owned paths |

The aggregate guardrail script does not yet include the unpublished Seerr
module in its client matrix. Direct module checks above are the lane evidence;
C-06 owns matrix/bootstrap completion.

## Review and integration

- Reviewer identity/role and reviewed commit: `/root/x05_reviewer`, independent
  review pending against `3dc53eee75cf296662a60b7422ec557692cdaee8`.
- Findings with severity, reproduction and contract reference: prior receipt
  `d51e7fcedd730a6f58bdcce99e9fc926baaee5a5` identified R1 P1 (native unknown
  availability reported as known negative), R2 P2 (request deadline and
  context identity), and R3 P2 (service relationship validation and silently
  dropped malformed service errors). All three are addressed with executable
  regressions above; final independent disposition is pending.
- Fix commit and regression evidence:
  `3dc53eee75cf296662a60b7422ec557692cdaee8`; focused, race, module,
  generation, Vacuum, lint, cross-build and root guardrail results are listed
  above.
- Final reviewer decision: pending.
- Integrated commit, recorded by coordinator: pending; this lane did not edit
  `docs/execution/state.json`.

## Resume checkpoint

- Current state and outstanding uncertainty: the isolated read-only client,
  certainty normalization, strict service evidence and bounded transport are
  committed at `3dc53eee75cf296662a60b7422ec557692cdaee8`. Seerr product-version
  compatibility and runtime capability remain unpinned; native offset
  pagination remains intentionally partial/unknown when its evidence is
  incomplete or mutable.
- Active process or agent ownership, sanitized: no active process; coordinator
  owns state/integration and `/root/x05_reviewer` owns the independent review.
- Next safe action: commit this handoff separately, review the exact product
  tree, and record both commits and the review result in coordinator state.
- Blocker and exact input/evidence needed: C-06 must publish the module and
  add its matrix/CI checks before X-22 imports it. A versioned disposable
  Seerr fixture is needed before promoting a runtime capability. G-01 remains
  open; this lane added no writes.
- No conflicting writes or unknown files were removed: only `clients/seerr/`
  and this correction handoff are owned here; state, root adapters, scripts,
  other client modules and unrelated shared paths were preserved.
