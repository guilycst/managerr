# X-16 independent review, round two

Decision: **changes_requested**. Original P2 R1 is closed. One new P1 scope
validation finding remains in the newly supported movie-scoped folder preview.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`; independent of product authorship.
- Exact product: `30aa6734fa7705d61e3d93c9b7f93c8173c4b9b3`.
- Product tree: `feaa969e07938454356353815107e0369f6dabbc`.
- Exact handoff: `345fbc83bbf85d7f6c3f04d93d7e9bf4ae8a31e5`.
- Handoff tree: `b059c3d189b39c6021b00c1a3c089c8a4fdeb1d9`.
- Product base/parent: `aa9d7c753703a56225fb65fdbd2794b8bd1b1821`.
- Prior review: `d4b57f80b141e447ce7a45656ba5ce29814eb8b1`.
- Scoped binary correction diff SHA-256:
  `66dbcab2bf9dcd51de0e1563ff49de2e09065cf4b323222a20b5d03479fd4b91`.
- Scope: `clients/radarr/`, correction handoff, prior R1,
  X-16/A-07/A-09/A-10/A-45 and the standalone-client connector policy.
- Review used a clean detached checkout at the handoff. Independent synthetic
  probes and write-mode regeneration ran in an archive of the exact product.
  Only this receipt is committed; product/state and unrelated files were untouched.

## Finding

### R2 — P1: scoped folder preview disables requested movie identity validation

Location: `clients/radarr/client.go:642`, in `PreviewManualImport`;
`normalizeManualImport` and `PreviewImport` forwarding behavior.

The correction adds support for optional `ManualImportQuery.MovieID`. The
method validates a positive value and sends it as `movieId`, but calls the
shared preview reader with `expectedMovieID` equal to zero. This disables the
normalizer's foreign/missing movie refusal. The compatibility-named
`PreviewLibraryImport` passes its requested ID correctly; the ordinary public
folder entry point does not.

Independent reproduction `TestReviewerFolderMovieScope` sends a GET with
`folder=/downloads` and `movieId=101`, asserted by its synthetic server. Return:

```json
[{"id":1,"path":"/downloads/movie.mkv","relativePath":"movie.mkv",
  "name":"movie.mkv","size":10,
  "movie":{"id":999,"title":"Other Movie"}}]
```

At the exact product, `PreviewManualImport(context.Background(),
ManualImportQuery{Folder: "/downloads", MovieID: &requestedID})` returns no
error and complete coverage while retaining movie 999. Removing the entire
`movie` member is also accepted as complete, with a nil association. Both
independent cases explicitly assert the bad acceptance and passed three times
with `-race`. Passing these reproduction probes demonstrates the defect.

This violates the requested title scope and the exact preview evidence
required by A-09/A-10. Sending an upstream filter is not proof that a returned
association belongs to it. The newly supported entry point can therefore
supply complete authority for a different or unidentified movie.

Required correction: pass the optional positive requested movie ID through to
the shared preview normalizer, with zero reserved for an intentionally
unscoped request. Add positive/missing/foreign identity regressions through
`PreviewManualImport` and `PreviewImport`, alongside the compatibility-named
movie entry points. A scoped candidate lacking the selected association must
not yield complete evidence. Preserve intentionally unscoped folder matching.

## Prior finding and regression verification

**Original R1 closed:** OpenAPI 3.1.1 now requires folder, and generated
`PreviewManualImportParams.Folder` is a required string rather than an optional
pointer. The contract/README no longer describe a movie-ID-only native library
scan. Compatibility-named methods now require an explicit source folder.
Independent atomic request counting confirms movie-only input is rejected as
`ErrorInvalidInput` before any network call.

The correction agrees with the previously inspected Radarr 3.2.2.5080 primary
source behavior at commit `2bca1a71a2ed5130ea642343cb76250f3bf5bc4e`: a path is
needed and movie ID selects the associated movie during folder processing.
No native runtime or HTTP exception status is claimed by the synthetic tests.

Independent `TestReviewerValidPathMoviePreview` verifies exact GET
folder/movie/filter query values, movie 101 preservation and complete coverage
for a valid response through both ordinary and compatibility-named methods.
Existing compatibility-named foreign/missing movie tests also passed; R2 is
specific to the ordinary entry point's new optional scope.

Other prior checks remain intact: direct movie identity, direct/nested file
movie scope, matching movie-file IDs, required size/language/rejection fields,
duplicate/trailing JSON rejection, complete bounded full arrays, native
language/rejection evidence and generated unknown-field isolation. Independent
foreign direct/nested file, nested file-ID mismatch, null size and missing
rejection text probes were rerun and rejected malformed responses.

API-key transport, endpoint/path checks, cookie-free cloned client, redirects,
deadlines, bounded strict response decoding and sanitized typed errors remain
unchanged. Generated output stays module-local under `internal/generated`.
There are no root/other-client imports, raw public generated clients, local
replace directives or registration/import/search/command writes.

## Independent checks

All checks below passed with exit status zero, using `GOWORK=off` for module
commands. Synthetic fixtures only; no live stack, private coordinates,
credentials or real inventory was accessed.

| Check | Result |
| --- | --- |
| Radarr `go test -mod=readonly -race -count=3 -timeout=180s ./...` | Passed; included tests miss ordinary scoped response validation. |
| Reviewer `go test -mod=readonly -run TestReviewer -race -v -count=3 -timeout=120s ./...` | Passed; refusal/scope/shape probes, including R2 bad-acceptance reproductions. |
| Reviewer valid path/movie query probe, `-race -count=3` | Passed for both preview entry points. |
| Radarr `go vet -mod=readonly ./...`, `go mod verify` | Passed; all modules verified. |
| Offline `GOPROXY=off GOSUMDB=off ./check-generation.sh` | Passed. |
| Offline `go generate ./...` in exact-product archive plus byte comparison | Passed; pinned-tools generated artifact reproduced byte-for-byte. |
| Pinned Vacuum offline with repository ruleset, `--remote=false --fail-severity=warn` | Passed; quality 100/100, zero warnings/errors. |
| Pinned golangci-lint via tools module and repository configuration | Passed; zero issues. |
| Linux amd64/arm64 `CGO_ENABLED=0 go build -mod=readonly ./...` | Passed for both architectures. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed; 53 tasks, 60 acceptance cases and local links. |
| `./scripts/check-guardrails.sh --ci` | Passed; existing module matrix generation/API/Vacuum/architecture/lint/tests/vet/verification. |
| `git diff --check` | Passed before receipt. |

## Separate gates and checkpoint

Aggregate scripts/CI still omit Radarr; coordinator-owned matrix expansion and
a consumable module release or approved bootstrap remain required before root
adapter migration. Full cross-stack acceptance is separate from this review.

G-01 remains open. No Arr writes are enabled. Synthetic local verification is
separate from native compatibility certification, publication, deployment and
verified live behavior.

Only this receipt is committed. Its exact SHA is reported after commit;
execution state and product paths remain unchanged.
