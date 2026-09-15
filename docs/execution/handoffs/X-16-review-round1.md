# X-16 independent review, round one

Decision: **changes_requested**. One P2 native preview compatibility finding
remains. The standalone module and other reviewed transport/evidence behavior
pass their scoped checks.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`; independent of product authorship.
- Exact product: `3823f66eaca1796457d7dc9efa93d2ccf5b6bc67`.
- Product tree: `6b107b19e375ce915f8db08c4de2c6a0f12ba504`.
- Exact handoff: `6ce4943f920971f82df661f3dcc31dbc648e2a66`.
- Handoff tree: `175eb2fc70b58a5347ea761cd568144de53c5afe`.
- Product base/parent: `67b2da6f3335c043bf2cf7cc0adc53149427a984`.
- Scoped binary product diff SHA-256:
  `d0d63285571e315dba2aaa07c3ab6039a8353bb776d2c7db59bbd08c127005ce`.
- Scope: `clients/radarr/`, X-16 handoff, A-07/A-09/A-10/A-45 and the
  standalone-client connector policy. Source, compatibility specification,
  generated boundary, README and all included tests were inspected directly.
- Review used a clean detached checkout at the exact handoff. Independent
  synthetic probes and write-mode regeneration ran in an archive of the exact
  product. Only this receipt is committed; product/state and unrelated lane
  files were untouched.

## Finding

### R1 — P2: movieId-only library preview is unsupported by declared Radarr 3.x

Locations: `clients/radarr/client.go:530`, `:542`, `:637`, `:640`;
`clients/radarr/openapi.yaml` manual-import description;
`clients/radarr/README.md` native query modes; and
`TestManualImportLibraryPreviewUsesNativeMovieScope`.

`PreviewLibraryImport`/`PreviewMovieImport` send only `movieId` and
`filterExistingFiles`, with no folder or download ID, while the specification,
README and fixture claim that this selects a native registered-library scan.
This is not the declared candidate Radarr 3.x behavior.

Primary evidence was checked against Radarr `v3.2.2.5080`, resolved through the
GitHub tag API to immutable commit
`2bca1a71a2ed5130ea642343cb76250f3bf5bc4e`:

- [ManualImportModule](https://github.com/Radarr/Radarr/blob/2bca1a71a2ed5130ea642343cb76250f3bf5bc4e/src/Radarr.Api.V3/ManualImport/ManualImportModule.cs#L22)
  forwards folder, download ID, movie ID and filter into one service method.
- [ManualImportService](https://github.com/Radarr/Radarr/blob/2bca1a71a2ed5130ea642343cb76250f3bf5bc4e/src/NzbDrone.Core/MediaFiles/MovieImport/Manual/ManualImportService.cs#L62)
  obtains a path from folder or tracked download, checks that path, and only
  then processes it. Movie ID selects the associated movie during folder
  processing; it does not resolve the registered movie's library path.
- [DiskProviderBase](https://github.com/Radarr/Radarr/blob/2bca1a71a2ed5130ea642343cb76250f3bf5bc4e/src/NzbDrone.Common/Disk/DiskProviderBase.cs#L87)
  validates the path before the existence check;
  [IsValidPath](https://github.com/Radarr/Radarr/blob/2bca1a71a2ed5130ea642343cb76250f3bf5bc4e/src/NzbDrone.Common/EnsureThat/EnsureStringExtensions.cs#L102)
  throws for null/blank input. A movie-ID-only request therefore cannot execute
  the advertised scan. No native HTTP exception status is claimed here.

Independent reproduction `TestReviewerLibraryQueryMissingRequiredPath` calls
`PreviewLibraryImport` with movie 101, verifies GET and the requested movie ID,
and observes that both path-bearing inputs are absent. A synthetic server
rejecting this missing-path request produces an error rather than a scan.
Its HTTP 400 is a fixture choice, not a certified native error status. The
included product fixture instead returns a library candidate for this request,
masking the compatibility failure. Probe passed three times with `-race`.

An initial reviewer model assumed missing-path existence checks simply returned
false; direct inspection of the validator disproved that detail. The corrected
probe above models rejection. This review does not claim that native Radarr
returns a successful empty response for a missing path.

Required correction: freeze version-accurate Radarr query semantics. Require
an explicit path for a movie-bound preview, or resolve and bind the movie's
actual path through a read before dispatch; alternatively reject/block this
unsupported mode under the declared range. Do not transplant Sonarr's native
series-only overload into the Radarr contract. Correct the OpenAPI, README and
fixtures together. Retain exact returned movie identity validation and add
source-faithful missing-path and valid path-plus-movie scenarios. If another
product version is intended, establish its behavior and adjust the supported
range explicitly.

## Other reviewed evidence

- Catalog/options/file endpoints retain complete accepted full arrays with
  timestamps/counts and no invented cursor. Bounds reject rather than truncate.
  Duplicate catalog/file/language identities and JSON members are rejected.
- Direct movie reads validate requested identity; file and nested file
  associations validate movie scope and matching movie-file IDs. Independent
  foreign direct/nested files, nested file-ID mismatch and null required size
  probes passed, rejecting malformed evidence.
- Native language and rejection reason evidence is retained. Required nested
  languages/rejections are validated before scalar decoding; missing rejection
  text is rejected. Unknown generated properties do not leak into normalized
  public observations.
- OpenAPI is narrow/read-only and 3.1.1. Generated output is module-local under
  `internal/generated`, reproducible, and exposes no raw public generated
  client. The module has no root/other-client imports or write methods.
- API-key headers, prefix/endpoint validation, cloned cookie-free transport,
  redirect refusal, request deadlines, bounded strict decoding and sanitized
  typed HTTP errors were inspected and included fixtures rerun.

## Independent checks

All commands below passed with exit status zero. Radarr module commands used
`GOWORK=off`. Tests used synthetic fixtures only; no live media stack or private
coordinates, credentials or inventory was accessed.

| Check | Result |
| --- | --- |
| Radarr `go test -mod=readonly -race -count=3 -timeout=180s ./...` | Passed; includes the inaccurate library-mode fixture noted in R1. |
| Reviewer `go test -mod=readonly -run TestReviewer -race -v -count=3 -timeout=120s ./...` | Passed; required-path query and identity/shape probes. |
| Radarr `go vet -mod=readonly ./...`, `go mod verify` | Passed; all modules verified. |
| Offline `GOPROXY=off GOSUMDB=off ./check-generation.sh` | Passed. |
| Offline `go generate ./...` in exact-product archive plus byte comparison | Passed; committed artifact reproduced byte-for-byte via pinned tools module. |
| Pinned Vacuum offline, repository ruleset, `--remote=false --fail-severity=warn` | Passed; quality 100/100, zero warnings/errors. |
| Pinned golangci-lint via tools module and repository configuration | Passed; zero issues. |
| Linux amd64/arm64 `CGO_ENABLED=0 go build -mod=readonly ./...` | Passed for both architectures. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed; 53 tasks, 60 acceptance cases and local links. |
| `./scripts/check-guardrails.sh --ci` | Passed; existing matrix generation, API/Vacuum, architecture, lint, tests, vet and verification. |
| `git diff --check` | Passed before receipt. |

## Separate gates and checkpoint

The aggregate scripts/CI matrix omit Radarr. Direct module checks above cover
this review; coordinator-owned matrix expansion and a consumable module version
or approved bootstrap remain required before adapter migration. Passing the
existing CI matrix does not close that blocker or end-to-end acceptance cases.

G-01 remains open. Registration/import/search/command writes are not enabled.
Synthetic checks and primary source inspection are separate from native runtime
certification, publication, deployment and verified live behavior.

Only this receipt is committed. Its exact SHA is reported after commit;
execution state and product paths remain unchanged.
