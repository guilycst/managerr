# X-18 correction round two: Seerr identity and context privacy

## Assignment

- Task ID and title: X-18 correction round two, close Seerr zero-identity and
  context-error sanitization findings.
- Owner/agent and independent reviewer: /root/x05_implementer; independent
  reviewer /root/x05_reviewer.
- Base commit: f094d17da5794dc059659b3d3a5ca259cc0f9a70.
- Branch/worktree or shared-checkout ownership: shared checkout on main;
  coordinator owns docs/execution/state.json.
- Owned files and generated outputs: clients/seerr/ and this handoff.
  Generated output remains at clients/seerr/internal/generated/client.gen.go
  and is never hand-edited.
- Prior product and handoff: 3dc53eee75cf296662a60b7422ec557692cdaee8 and
  460b5d7d45a2ab837a9e7c2f66cdee1aa153487c.
- Prior independent review receipt:
  90e6a2cdd327734d3b95df327c25655e46facbf7.
- Required acceptance IDs: A-08, A-09, A-45 and A-54. Planned checks are
  standalone module tests/race/vet/module verification, offline generation
  and reproducibility, Vacuum, lint, architecture, planning, guardrails and
  Linux amd64/arm64 CGO-free builds.

## Contract and work

- Linked specification sections and invariants: X-18 in
  docs/plans/implementation.md; Seerr in
  docs/specs/spec-001-media-reconciliation/connectors.md; A-08, A-09, A-45
  and A-54 in docs/verification/acceptance.md; findings are in
  docs/execution/handoffs/X-18-review-round2.md. Nullable absence remains
  unknown, valid native manager identity is preserved, context cancellation
  identity remains observable, and private transport details remain inside
  the client boundary.
- Intended result and capability limits: preserve native zero-valued Seerr
  configured-manager IDs in base, 4K, nested and service-error evidence, and
  return canonical context errors for wrapped transport/body failures without
  exposing endpoint, URL, body or transport detail. No writes, search, hidden
  discovery or generated DTO leakage were added.
- Changes completed:
  - Service relationship normalization now treats nullable fields as the
    absence signal. Present serviceId/externalServiceId values of zero are
    retained as "0"; negative IDs, invalid kinds, invalid slugs and incomplete
    pairs still produce explicit evidence and no normal relationship. The same
    behavior applies to 4K relationships and nested request media.
  - serviceErrors preserves a present native id: 0 with IDKnown=true. A
    missing nullable ID remains IDKnown=false when a bounded name supplies the
    only identity; negative IDs and malformed records remain rejected.
  - Transport and response-body errors that wrap or join context.Canceled or
    context.DeadlineExceeded now return the canonical context sentinel. This
    retains errors.Is behavior while dropping raw http.Client URL wrappers and
    arbitrary reader/transport detail.
  - README and public type comments document zero identity and IDKnown
    semantics. The OpenAPI contract and generated output remain unchanged and
    reproducible.
  - Deterministic synthetic regressions cover movie and TV kinds, base and 4K
    IDs, nested request media, repeated zero IDs across configured connections,
    nullable/invalid evidence, zero-valued serviceErrors, and joined
    transport/body cancellation and deadline errors with privacy assertions.
- Shared contract changes requested from coordinator: C-06 should publish
  clients/seerr into repository generation, lint, architecture, test, vet,
  module-verification, cross-build and CI matrices. X-22 must publish and
  consume this nested module without a local replace before migrating the root
  Seerr adapter. Product-version compatibility and runtime capability evidence
  remain integration work.

## Verification

All HTTP fixtures are synthetic httptest handlers or in-memory transports.
No live Seerr endpoint, credential, private coordinate, inventory or media
data was used. Commands below were run with GOWORK=off; generation also used
GOPROXY=off GOSUMDB=off.

| Command or scenario | Product / fixture | Result / exit status | Evidence path |
| --- | --- | --- | --- |
| GOWORK=off go test -mod=readonly -count=1 -timeout=90s ./... from clients/seerr | a0670e0a60b7b4920ff8a672bfa311d9b93a0ce1; synthetic fixtures | Passed, exit 0 | clients/seerr/client_test.go |
| GOWORK=off go test -mod=readonly -race -count=3 -timeout=180s ./... from clients/seerr | a0670e0a60b7b4920ff8a672bfa311d9b93a0ce1; repeated fixtures | Passed, exit 0 | clients/seerr/client_test.go |
| GOWORK=off go vet -mod=readonly ./... from clients/seerr | product tree | Passed, exit 0 | clients/seerr/*.go |
| GOWORK=off go mod verify from clients/seerr | product tree | Passed: all modules verified | clients/seerr/go.mod and go.sum |
| Offline GOWORK=off GOPROXY=off GOSUMDB=off go generate ./... and ./check-generation.sh | product tree; pinned tools | Passed; generated output unchanged and reproducibility check passed | clients/seerr/generate.go and check-generation.sh |
| Pinned Vacuum through tools/go.mod, repository ruleset, --remote=false and --fail-severity=warn | OpenAPI 3.1.1 contract | Passed; quality 100/100, zero warnings/errors | clients/seerr/openapi.yaml |
| Pinned golangci-lint through tools/go.mod with repository configuration | product tree | Passed; 0 issues | clients/seerr Go files |
| GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./... | product tree | Passed, exit 0 | clients/seerr |
| GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go build -mod=readonly ./... | product tree | Passed, exit 0 | clients/seerr |
| python3 scripts/check-architecture.py and python3 scripts/check_planning.py | product candidate | Passed; architecture boundaries and planning links valid | repository scripts |
| ./scripts/check-guardrails.sh --ci | product candidate | Passed; aggregate generation/API/Vacuum/lint/root checks passed; aggregate client matrix still excludes Seerr | scripts/check-guardrails.sh |
| Versioned pre-commit hook during product commit and git diff --check | a0670e0 | Passed; staged generation/API/architecture/fast checks and whitespace checks passed | .githooks/pre-commit |
| TestNativeZeroManagerIdentitiesRemainKnown | movie/TV, base/4K, nested and two configured connections with native zero IDs | Passed under race; present zero manager IDs remain known and paired relationships remain scoped | clients/seerr/client_test.go |
| TestMalformedServiceErrorsFailClosed | malformed records plus name-only and id:0 controls | Passed under race; zero is known when present, nullable absence stays explicit unknown, malformed records fail closed | clients/seerr/client_test.go |
| TestWrappedContextErrorsRemainSanitized | joined cancellation/deadline errors from synthetic transport and body readers | Passed under race; errors.Is identity is retained and marker/URL/body detail is absent | clients/seerr/client_test.go |

## Review and integration

- Reviewer identity/role and reviewed commit: /root/x05_reviewer, independent
  re-review pending against product
  a0670e0a60b7b4920ff8a672bfa311d9b93a0ce1.
- Findings addressed: review receipt
  90e6a2cdd327734d3b95df327c25655e46facbf7 identified R3a P1 (zero is a
  valid first configured Seerr manager identity) and R2a P2 (wrapped context
  errors exposed raw private detail). Both have executable regression coverage
  above.
- Final reviewer decision: pending. Coordinator owns state recording and
  integration.
- G-01 remains open; no Arr or Seerr writes are enabled. C-06/bootstrap,
  consumable nested-module publication, X-22 migration, pinned native
  version/auth fixtures and deployment mappings remain separate gates.

## Resume checkpoint

- Current product checkpoint:
  a0670e0a60b7b4920ff8a672bfa311d9b93a0ce1.
- Handoff commit: pending until this file is committed separately.
- Current state and outstanding uncertainty: Seerr read normalization and
  sanitized transport boundary are covered by synthetic evidence.
  Product-version compatibility and runtime capability remain unpinned;
  mutable offset pagination remains partial/unknown when evidence is
  incomplete or mutable.
- Active process or agent ownership, sanitized: no active process; coordinator
  owns state/integration and /root/x05_reviewer owns independent re-review.
- Next safe action: review the exact product tree, record product and handoff
  SHAs in coordinator state, then integrate only after independent approval.
- No conflicting writes or unknown files were removed: only clients/seerr/ and
  this handoff are owned here; state, root adapters, scripts, other clients
  and unrelated shared paths were preserved.
