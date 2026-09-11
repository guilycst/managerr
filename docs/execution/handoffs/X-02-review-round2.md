# X-02 implementation handoff, review round two

## Decision

`ready_for_independent_review`.

This correction implements every finding from the independent round-one
receipt `c68bccdbdc1a9e49cc79e26e317b480caa697315` while keeping the NZBGet
boundary read-only, bounded and connection-scoped. The correction product is
`c92622b468523f37539df7a0f468bf8fe83c0569`.

## Assignment and ownership

- Task: X-02, NZBGet inventory adapter.
- Implementer: `/root/f01_implementer`.
- Requested independent reviewer: `/root/f01_reviewer`.
- Correction base: `84861c964407b6307e56bc70b109fae50ac278fd`.
- Dependency: C-03 approved receipt `8c9bf9a6277e068f9ae6b859fab2c5f7aa4890a1`.
- Owned product paths: `internal/adapters/nzbget/` and
  `tests/fixtures/nzbget/`.
- Owned evidence path: this file only. The original implementation handoff
  `docs/execution/handoffs/X-02.md` and round-one receipt remain unchanged.
- Shared checkout: `main`; the coordinator owns integration and execution
  state. No state file was staged or changed by this lane.
- Acceptance contributions: A-05, A-06 and A-09.

## Correction scope

### JSON-RPC 1.1 boundary

The request DTO now sends NZBGet's positional JSON-RPC 1.1 envelope with a
`version` member, method, positional parameters and a numeric request ID.
Responses require `version: "1.1"` and the echoed request ID. Successful
results and error envelopes are both accepted through that boundary; missing,
wrong or generic JSON-RPC 2.0 markers remain malformed. Response bodies stay
bounded and error details remain normalized without response text, endpoints or
credentials.

The synthetic RPC fixture records the request version and emits valid 1.1
success and error envelopes. `TestJSONRPC11EnvelopeAndRequest` proves
`version([])`, echoed IDs, error normalization and rejection of a 2.0-only
envelope.

### Correlated path and reason integrity

Correlation now returns the actual merged item index before adding item-scoped
reasons. `FinalDir` evidence is merged atomically with its content path and
mapping result. When a history `FinalDir` is present but unmapped or
ambiguous, the earlier `DestDir` target is cleared instead of being retained.
After queue/history correlation, path reasons are recomputed from the final
merged observation, so a stale path reason or an impossible item index cannot
remain. `TestFinalDirMergeReplacesFallbackMappingAndReasonIndex` covers a
mapped `DestDir` replaced by an unmapped `FinalDir`.

### Source path confinement

Source prefixes and observed paths are rejected before normalization when they
are relative, contain dot components, repeated separators, backslashes or NUL
bytes. A traversal-bearing `FinalDir` remains visible as content evidence but
cannot produce a `FileTarget`; coverage reports
`final_path_unsafe`. Component-boundary mapping behavior remains intact.
`TestTraversalBearingPathsRemainUnmapped` covers configuration and observed
path rejection, including prefix lookalikes and canonical valid-path behavior
through the existing mapping tests.

### Typed `drone` and deprecated ID evidence

NZBGet parameter values are accepted only when their JSON type is a string.
Numeric, boolean, object, array and null values remain absent and make the
observation partial. A malformed or conflicting `drone` never fabricates an
Arr download ID; the canonical positive NZBID/deprecated-ID fallback is used
when available. Duplicate equal string values remain stable. Both queue and
history emit the malformed-parameter reason for unusable correlation evidence.
`TestNumericDroneDoesNotFabricateArrIdentity` proves the numeric case.

The deprecated `ID` alias is now considered contradictory only when both
`NZBID` and `ID` are positive and unequal. An ID-only history record therefore
uses its documented fallback without an alias-mismatch reason, while a
positive unequal pair remains partial. `TestHistoryAliasFallbackAndProcessingStates`
asserts the ID-only behavior and existing coverage for a positive mismatch.

### Detailed evidence redaction

Exported detailed observations no longer expose secret-bearing upstream
evidence. Parameter values with password, passphrase, secret, token, API key,
authorization, cookie, credential, username or private-key names are replaced
with `[redacted]`; values containing those markers are also redacted. URL
parameters are reduced to a safe parsed URL without userinfo, query or
fragment. History URLs apply the same userinfo/query/fragment removal. Queue
post-processing text follows a bounded policy: secret-marked text is replaced
with `[redacted]`, otherwise it is trimmed and capped at 4096 runes. Typed
names and safe correlation values such as a valid string `drone` remain
available. `TestDetailedSecretEvidenceIsRedacted` asserts that synthetic
password and token sentinels are absent from fields and formatted detailed
responses.

No descriptor bytes, credentials or raw RPC error bodies are added to ordinary
observations. The adapter still performs no writes to NZBGet, its queue,
history, post-processing records or the filesystem.

## Changed commits

- Product correction: `c92622b468523f37539df7a0f468bf8fe83c0569`,
  `fix(nzbget): harden protocol and provenance evidence`.
- Handoff documentation: pending this documentation commit.
- No root module, nested module, shared port/domain contract, fixture outside
  the owned area, `docs/execution/state.json`, UI file or migration was
  staged by this lane.

## Verification

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `GOWORK=off go test -count=1 ./internal/adapters/nzbget/inventory` | Passed | Focused correction suite. |
| `GOWORK=off go test -count=25 -timeout=180s ./internal/adapters/nzbget/inventory` | Passed | Repeated protocol, merge, path, type and redaction regressions. |
| `GOWORK=off go test -race -count=1 ./internal/adapters/nzbget/inventory` | Passed | Focused race run. |
| `GOWORK=off go test -race -count=10 -timeout=240s ./internal/adapters/nzbget/inventory` | Passed | Repeated focused race run. |
| `GOWORK=off go test -count=1 -cover ./internal/adapters/nzbget/inventory` | Passed; 79.3% statements | Focused coverage run. |
| `GOWORK=off go vet ./internal/adapters/nzbget/inventory` | Passed | Focused vet. |
| `./scripts/generate.sh --check` | Passed | Generated artifacts current. |
| `./scripts/check-api.sh` | Passed | OpenAPI and generation checks current. |
| `python3 scripts/check-architecture.py` | Passed | Import-direction guard. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases | Planning links resolve. |
| `GOWORK=off go mod verify` | Passed | Root module dependencies verified. |
| `(cd tools && GOWORK=off go test ./... && GOWORK=off go vet ./... && GOWORK=off go mod verify && GOWORK=off go mod tidy -diff)` | Passed | Tools module checks. |
| `(cd ui && GOWORK=off go test ./... && GOWORK=off go vet ./... && GOWORK=off go mod verify)` | Passed | UI module checks. |
| Focused Linux amd64/arm64, Windows amd64 and AIX ppc64 `go test -c`, `CGO_ENABLED=0` | Passed | Cross-compilation checks. |
| Product commit pre-commit hook at `c92622b` | Passed: generation, API, architecture, domain/ports/qBittorrent tests | Versioned fast guardrail hook. |
| `gofmt -l` on owned Go files and `git diff --check` | Passed | Owned correction files are formatted and clean. |
| `GOWORK=off go test -count=1 ./...` before the later unrelated shared-checkout edit | Passed | All root packages, including NZBGet correction, passed once. |
| Current `GOWORK=off go test -count=1 ./...` and `GOWORK=off go test -race -count=1 ./...` | Blocked by unrelated edit | The concurrent `internal/adapters/arr/read/client.go` has an unused `sort` import; NZBGet, qBittorrent and all other packages reached by the compiler passed. This lane did not edit or stage that file. |
| Current `GOWORK=off go vet ./...`, `./scripts/check-lint.sh` and `./scripts/check-guardrails.sh --fast` | Blocked by unrelated edit | The same unformatted/unused-import state in `internal/adapters/arr/read/client.go`; the product commit hook passed before that edit appeared. |

All focused checks use synthetic `httptest` fixtures. No live NZBGet
instance, credential, private endpoint, real inventory, media payload,
mutating RPC, release, deployment or filesystem mutation was used.

## Review request and resume checkpoint

- Round-one review receipt: `c68bccdbdc1a9e49cc79e26e317b480caa697315`.
- Correction product for independent review:
  `c92622b468523f37539df7a0f468bf8fe83c0569`.
- Next reviewer: `/root/f01_reviewer`.
- Requested review path: this file, with the round-one findings closed by the
  product commit above.
- Current unowned shared-checkout changes observed after the product commit:
  `docs/execution/state.json` and `internal/adapters/arr/read/client.go`.
  They were preserved and never staged by this lane.
- Next safe action: commit this handoff separately, then perform the
  independent round-two review against the exact product SHA.
