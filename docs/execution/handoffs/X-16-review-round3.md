# X-16 independent review, round three

Decision: **approved** for the standalone read-only Radarr client scope.
Prior P2 R1 and P1 R2 are closed. No unresolved scoped finding was identified;
root integration and native write gates remain separate.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`; independent of product authorship.
- Exact product: `1ef0c07af604e7e63d88bc68955c8097479f7e82`.
- Product tree: `c8767e7b98bcb3de12da175cd49727a2e694f2fc`.
- Exact handoff: `4432d7f6ca65dee2b3a598969bca3e3f8b7e10b6`.
- Handoff tree: `6e84e0ed92f4b0b41cce6757518efffa491b3463`; direct parent is
  the exact product. Path: `docs/execution/handoffs/X-16-correction-round2.md`.
- Correction base/parent: `6276071456f73d7e43928efd19fd291d738f64df`.
- Prior review: `41ae0c2b01b8712029dbb72024a72c81d1d990e2`.
- Scoped binary correction diff SHA-256:
  `642debde17c3f9cdff73135f704a0990adc3459f8a8f0c005ba948cd55961275`.
- Scope: `clients/radarr/`, correction handoff, prior R1/R2,
  X-16/A-07/A-09/A-10/A-45 and the standalone-client connector policy.
- Review used a clean detached checkout at the exact handoff. Independent
  synthetic probes and write-mode regeneration ran in an archive of the exact
  product. Product/state and unrelated files were untouched; only this receipt
  is committed.

## Finding closure and independent regressions

**R2 closed:** `PreviewManualImport` captures the positive requested movie ID
and passes it into the common normalizer. Zero is retained only when no movie
scope was requested. `PreviewImport` forwards through that method;
`PreviewLibraryImport` already passes its required movie ID and
`PreviewMovieImport` forwards through it. The normalizer requires a matching
nested movie when the expected ID is nonzero.

Independent `TestReviewerAllFolderEntryPointScopes` exercises all four public
entrypoints against four response shapes, three times with the race detector:

- Every request asserts GET, the explicit folder and `movieId=101`.
- Foreign movie 999, missing movie and explicit null movie are rejected as
  malformed without complete coverage.
- A valid movie 101 response succeeds with complete coverage and preserves the
  association.

Independent `TestReviewerUnscopedFolderUnknownMovie` confirms that an
intentionally unscoped request sends no movie ID, accepts a native candidate
with unknown movie association, and preserves nil association rather than
inventing identity. The included new ordinary-entrypoint rejection and alias
forwarding tests were directly inspected and rerun.

**R1 remains closed:** folder is required in OpenAPI 3.1.1 and in generated
`PreviewManualImportParams`. Compatibility-named movie preview methods require
an explicit path. Independent atomic request counting confirms movie-ID-only
input is rejected locally with zero network calls. Valid path/movie/filter
queries and observations pass through both ordinary and compatibility-named
methods. The contract/README no longer advertise a movie-ID-only native
registered-library scan.

Those path-plus-movie semantics agree with the previously inspected Radarr
3.2.2.5080 service at immutable commit
`2bca1a71a2ed5130ea642343cb76250f3bf5bc4e`. Tests model the narrow compatibility
boundary; no native runtime execution or native HTTP exception status is
claimed. Tracked-download path resolution remains upstream behavior, not
Mastarr filesystem confinement or path mapping.

Other prior checks remain intact: requested movie identity, direct/nested
movie-file scope and matching file IDs, required nested size/language/rejection
fields, duplicate/trailing JSON rejection, complete bounded arrays and typed
native language/rejection evidence. Independent foreign direct/nested files,
file-ID mismatch, null required size and missing rejection text probes were
rerun and rejected malformed evidence.

The correction changes only requested-ID threading and its tests. Narrow
read-only OpenAPI, generated internal output and transport remain unchanged.
API-key headers, endpoint/prefix validation, cookie-free cloned transport,
redirect refusal, deadlines, bounded strict decoding and sanitized typed errors
remain intact. Generated unknown members stay isolated from normalized
observations; no root/other-client imports, raw public generated client,
permanent local replace or mutation methods were introduced.

## Independent checks

All checks below passed with exit status zero. Module commands used
`GOWORK=off`. Synthetic fixtures only; no live stack, private endpoint,
credentials or real inventory was accessed.

| Check | Result |
| --- | --- |
| Radarr `go test -mod=readonly -race -count=3 -timeout=180s ./...` | Passed; all included regressions. |
| Reviewer `go test -mod=readonly -run TestReviewer -race -v -count=3 -timeout=120s ./...` | Passed; all-entrypoint scope matrix, unscoped behavior, no-network refusal and prior shape/query probes. |
| Radarr `go vet -mod=readonly ./...`, `go mod verify` | Passed; all modules verified. |
| Offline `GOPROXY=off GOSUMDB=off ./check-generation.sh` | Passed. |
| Offline `go generate ./...` in exact-product archive plus byte comparison | Passed; committed artifact reproduced byte-for-byte through pinned tools module. |
| Pinned Vacuum offline with repository ruleset, `--remote=false --fail-severity=warn` | Passed; quality 100/100, zero warnings/errors. |
| Pinned golangci-lint via tools module and repository configuration | Passed; zero issues. |
| Linux amd64/arm64 `CGO_ENABLED=0 go build -mod=readonly ./...` | Passed for both architectures. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed; 53 tasks, 60 acceptance cases and local links. |
| `./scripts/check-guardrails.sh --ci` | Passed; existing matrix generation/API/Vacuum/architecture/lint/tests/vet/verification. |
| `git diff --check` | Passed before receipt. |

## Separate gates and checkpoint

Aggregate scripts/CI still omit Radarr. Direct module checks above cover this
review; coordinator-owned matrix expansion and a consumable module version or
approved bootstrap remain required before root adapter migration. Approval
does not claim end-to-end A-07/A-09/A-10/A-45 closure across the stack.

G-01 remains open and no Arr registration/import/search/command writes are
enabled. Synthetic local checks are separate from native certification,
publication, deployment and verified live behavior.

Only this receipt is committed. Its exact review SHA is reported after commit;
execution state and product paths remain unchanged.
