# X-15 independent review, round three

Decision: **approved** for the standalone read-only Sonarr client scope.
Remaining finding R3a is closed. No unresolved scoped correctness finding was
identified; root integration and write-capability gates remain separate.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`; independent of product authorship.
- Exact product: `828e0084f2d88f86050db5912412352c1a363bc1`.
- Product tree: `1632d889832dd4e234eaa312bac5a6b0f47b8a3a`.
- Exact handoff: `ed9d74f1409367f746c3019666f36c1054db2567`.
- Handoff tree: `ee1de4eebe18bfda78a2d288d140b2d259136fa1`; direct parent is
  the exact product. Path: `docs/execution/handoffs/X-15-correction-round2.md`.
- Correction base: `6f5f582f56cf7840cb0dd549c55ea4c224904c19`.
- Prior review: `3b1347fdef9a760ba2a5ac73395c142ecd2d653b`.
- Scoped binary correction diff SHA-256:
  `f436973fa6d60df085ee24212a211e9eda93ea5659c415da6241b6f75be7acfb`.
- Scope: `clients/sonarr/`, correction handoff, R3a and prior R1-R5,
  X-15/A-07/A-09/A-10/A-45 and the standalone-client connector policy.
- Review used a clean detached checkout at the exact handoff. Independent
  probes and write-mode regeneration ran in an archive of the exact product.
  Product, execution state and unrelated lane files were untouched.

## Findings and regression evidence

**R3a closed:** `normalizeLanguages` now requires one plural element whenever
both forms are supplied, with identical ID and name. That enforces equality
with the singular object's one-element semantic set rather than accepting a
plural superset. Existing duplicate-ID and language validation also remains.
The README states this exact cardinality requirement.

Independent `TestReviewerLanguageAliasSets` exercises ten synthetic response
cases through the public client, three times with the race detector:

- Singular-only and plural-only forms preserve their respective observations;
  plural-only multiple-language responses remain supported.
- Equivalent simultaneous forms pass with reversed JSON object/member order
  and retain both normalized forms with complete coverage.
- A matching language plus an extra language is rejected whether the match
  appears first or last in the plural array.
- Empty simultaneous plural lists, changed IDs, changed names and duplicates
  are rejected as malformed and do not return complete coverage.

The prior reviewer probes were rerun against this exact source:

- **R1 remains closed:** folder plus `SeriesID` is rejected before any network
  call, verified with atomic request counting. Folder and library queries have
  separate request shapes; library mode omits folder/download ID and preserves
  requested series scope. Requests are GET operations.
- **R2 remains closed:** foreign-series direct episode files, nested episode
  files and nested preview episodes are rejected. Native file `seriesId`
  survives normalized output and is validated against request/parent scope.
- **R3 original preservation remains closed:** native singular language and
  rejection `reason`, plus alias-only plural languages and `message`, retain
  typed evidence. Rejection reason/message normalization remains consistent.
- **R4 remains closed:** missing nested file size, missing/null episode
  numbering and duplicate nested IDs are rejected by raw recursive shape
  validation before generated scalar decoding. Included required language,
  rejection and association tests were inspected and rerun.
- **R5 remains closed:** generation resolves pinned oapi-codegen via the tools
  module with `GOWORK=off GOPROXY=off GOSUMDB=off`. Write-mode regeneration in
  the exact-product archive produced bytes identical to committed output.

The correction changes only alias comparison, its tests and README. The narrow
OpenAPI 3.1.1 contract and generated internal package remain unchanged. Public
methods remain read-only; there are no root/other-client imports, permanent
local replaces or raw generated DTOs exposed by the handwritten client.
API-key transport, endpoint/path validation, deadlines, strict bounded
response decoding, typed sanitized errors, full-array coverage and unknown-field
isolation remain intact. Existing synthetic failure and bounds tests passed.

## Independent checks

All checks below passed with exit status zero. Module commands ran from
`clients/sonarr` with `GOWORK=off`. Only synthetic fixtures were used; no live
service, private endpoint, credential or inventory was accessed.

| Check | Result |
| --- | --- |
| `go test -mod=readonly -race -count=3 -timeout=180s ./...` | Passed, including all included client regressions. |
| Reviewer `go test -mod=readonly -run TestReviewer -race -v -count=3 -timeout=120s ./...` | Passed; alias, query, identity, required-field and native-field probes. |
| `go vet -mod=readonly ./...`, `go mod verify` | Passed; all modules verified. |
| `GOPROXY=off GOSUMDB=off ./check-generation.sh` | Passed. |
| `GOPROXY=off GOSUMDB=off go generate ./...` in exact-product archive plus `cmp` | Passed; committed generated artifact reproduced byte-for-byte. |
| Pinned Vacuum via tools module, offline, repository ruleset, `--remote=false --fail-severity=warn` | Passed; quality 100/100, zero warnings/errors. |
| Pinned golangci-lint via tools module and repository configuration | Passed; zero issues. |
| Linux amd64 and arm64, `CGO_ENABLED=0 go build -mod=readonly ./...` | Passed for both architectures. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed; 53 tasks, 60 acceptance cases and local links. |
| `./scripts/check-guardrails.sh --ci` | Passed; existing matrix generation, API/Vacuum, architecture, lint, tests, vet and verification. |
| `git diff --check` | Passed before receipt. |

## Separate gates and checkpoint

The existing aggregate scripts/CI matrix still omit Sonarr. Direct Sonarr
checks above are this review's evidence; coordinator-owned matrix expansion
and a consumable module release or approved bootstrap remain required before
root adapter migration. Approval does not claim A-07/A-09/A-10/A-45 are complete
end to end across the stack.

G-01 remains open and no Arr registration/import, search or command writes are
enabled. Synthetic compatibility evidence is separate from upstream
certification, release publication, deployment and verified live behavior.

Only this receipt is committed. Its exact review SHA is reported after commit;
execution state and product paths remain unchanged.
