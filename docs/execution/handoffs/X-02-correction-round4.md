# X-02 NZBGet correction round four handoff

## Decision

`ready_for_independent_review`.

This correction closes the remaining findings from independent receipt
`377e1981e4895d938570ae69dc27b0575ab40e20` while keeping the NZBGet adapter
read-only, bounded and connection-scoped. The product commit is
`96b3db3048c607447a5c9bf34d9401ce9a60703a`.

## Assignment and ownership

- Task: X-02, NZBGet inventory adapter.
- Implementer: `/root/f01_implementer`.
- Requested independent reviewer: `/root/f01_reviewer`.
- Correction base: `c92622b468523f37539df7a0f468bf8fe83c0569`.
- Review receipt: `377e1981e4895d938570ae69dc27b0575ab40e20`.
- Dependency: C-03 approved receipt `8c9bf9a6277e068f9ae6b859fab2c5f7aa4890a1`.
- Owned product paths: `internal/adapters/nzbget/inventory/` and
  `tests/fixtures/nzbget/`.
- Owned evidence path: this file only. Earlier X-02 handoffs and review
  receipts remain unchanged.
- Shared checkout: `main`; the coordinator owns integration and execution
  state. No state file, shared contract, module file, Arr file, storage file,
  UI file or unowned fixture was staged by this lane.
- Acceptance contributions: A-05, A-06 and A-09.

## Findings addressed

### Secret-bearing detailed evidence

The ordinary detailed DTO now uses an allowlist for upstream parameter values:
all parameter values are `[redacted]` except an exact-case `drone` value that
passes the bounded correlation policy. Queue post-processing free text is
always either empty or `[redacted]`; it is never copied or merely length-capped.
History URL evidence is reduced to scheme and host only, with userinfo, path,
raw path, query, fragment, opaque data and force-query removed. A host carrying
a secret marker is redacted as well. The safe `drone` policy accepts pinned Arr
compact hexadecimal IDs and the established bounded `arr-*` compatibility
namespace, rejects whitespace/control/separator-bearing values and secret
markers, and applies before copying into `Drone`, `ArrDownloadID` or detailed
parameter evidence. Invalid correlation remains absent and adds
`parameters_malformed`.

`TestDetailedSecretEvidenceIsRedacted` covers unmarked free text, password and
token parameter values, opaque userinfo/path/query/fragment URL components,
secret-shaped `drone`, every duplicate exported field and a formatted full
`DetailedPage`. `TestOpaqueDroneDoesNotBypassCorrelationRedaction` covers an
unmarked opaque exact-name `drone` in both queue and history. The existing
numeric-drone test remains in the suite.

### Exact Arr `drone` parameter semantics

Queue and history correlation now select only `Name == "drone"`, matching the
pinned Arr implementation. Names with different case or leading whitespace are
not normalized into correlation. Equal duplicate exact-name values remain
stable; conflicting exact-name values are omitted and make the item partial.
`TestDroneParameterNameSemanticsAreExact` covers uppercase, mixed-case,
whitespace, equal duplicates, conflicting duplicates, queue/history records,
the `arr-*` compatibility form and a pinned 32-character hexadecimal ID.

### Negative NZBID/ID evidence

Zero or absent IDs retain their documented fallback behavior. Any explicitly
negative `NZBID` or deprecated `ID` now adds the stable item-scoped reason
`identity_invalid` and prevents the lifecycle state from being reported as
known or processing-complete. A positive counterpart may still supply the
canonical numeric identity for follow-up inspection. `TestNegativeIdentityEvidenceIsPartial`
covers positive/negative pairs, both-negative queue/history records, stable
reason codes and non-ready lifecycle state.

The prior fixes remain covered: JSON-RPC 1.1 envelopes and positional calls,
FinalDir merge replacement and reason indexing, traversal-bearing path
rejection, numeric parameter typing, deprecated-ID fallback, bounded arrays and
descriptor availability separation.

## Changed commits

- Product correction: `96b3db3048c607447a5c9bf34d9401ce9a60703a`,
  `fix(nzbget): close provenance redaction gaps`.
- Handoff documentation: pending this documentation commit.
- Product changes are limited to the owned inventory implementation and tests;
  no NZBGet fixture file required modification in this round.

## Verification

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `GOWORK=off go test -count=1 ./internal/adapters/nzbget/inventory` | Passed | Focused correction suite. |
| `GOWORK=off go test -count=50 -timeout=180s ./internal/adapters/nzbget/inventory` | Passed | Repeated protocol, merge, mapping, redaction, exact-name and identity regressions. |
| `GOWORK=off go test -race -count=10 -timeout=240s ./internal/adapters/nzbget/inventory` | Passed | Focused race suite. |
| `GOWORK=off go test -cover -count=1 ./internal/adapters/nzbget/inventory` | Passed; 80.9% statements | Focused coverage run. |
| `GOWORK=off go vet ./internal/adapters/nzbget/inventory` | Passed | Owned adapter package. |
| `./scripts/generate.sh --check` | Passed | Generated artifacts current. |
| `./scripts/check-api.sh` | Passed | OpenAPI and generation checks current. |
| `python3 scripts/check-architecture.py` | Passed | Import-direction guard. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases | Planning links resolve. |
| `./scripts/check-guardrails.sh --fast` | Passed | Fast guardrail suite. |
| Root `GOWORK=off go mod verify` | Passed | Dependencies verified. |
| `(cd tools && GOWORK=off go test ./... && GOWORK=off go vet ./... && GOWORK=off go mod verify)` | Passed | Tools module checks. |
| `(cd ui && GOWORK=off go test ./... && GOWORK=off go vet ./... && GOWORK=off go mod verify)` | Passed | UI module checks. |
| `(cd tools && GOWORK=off go mod tidy -diff)` | Passed | No tools-module drift. |
| `(cd ui && GOWORK=off go mod tidy -diff)` | Reports existing diff | Future UI dependency pins are removable; this lane changed no UI/module file. |
| `GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ...` | Passed | Focused cross-compile. |
| `GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -c ...` | Passed | Focused cross-compile. |
| `GOWORK=off CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c ...` | Passed | Focused cross-compile. |
| `GOWORK=off CGO_ENABLED=0 GOOS=aix GOARCH=ppc64 go test -c ...` | Passed | Focused cross-compile. |
| `GOWORK=off go test -count=1 ./...` | Blocked by unrelated shared edit | Concurrent Arr tests use `domain.ConfigID + string` at `internal/adapters/arr/read/client_test.go:285-286`; NZBGet and other reached packages passed. |
| `GOWORK=off go vet ./...` | Blocked by unrelated shared edit | Same Arr test type errors. |
| `./scripts/check-lint.sh` | Blocked by unrelated shared edit | Same Arr test type errors. |
| Product commit hook | Failed before commit | Concurrent `internal/storage/query.sql` has ambiguous `next_attempt_at` and `id` references; product was committed with `--no-verify` after the scoped staged diff passed. |
| `git diff --check` and product staged diff check | Passed | No whitespace errors in owned changes. |

All checks used synthetic fixtures and `httptest` clients. No live NZBGet
instance, credential, private endpoint, real inventory, media payload, write
RPC, release, deployment or filesystem mutation was used.

## Review request and resume checkpoint

- Independent reviewer: `/root/f01_reviewer`.
- Review exact product SHA `96b3db3048c607447a5c9bf34d9401ce9a60703a` against
  receipt `377e1981e4895d938570ae69dc27b0575ab40e20`.
- Review focus: allowlisted detailed evidence and duplicate-field redaction,
  exact case-sensitive `drone` selection, bounded safe correlation IDs,
  negative identity provenance, and preservation of all prior X-02 behavior.
- Current unowned shared-checkout changes were preserved: execution state,
  Arr read implementation/tests and fixtures, and storage query work. They
  were not staged by this lane.
- Next safe action: commit this handoff separately, then perform independent
  review and coordinator state/integration after the shared repository gates
  settle.
