# X-15 independent review, round one

Decision: **changes_requested**. Two P1 and three P2 findings remain. The
client is read-only, but its normalized evidence and preview scope are not yet
safe/reliable for the intended root integration.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`; independent of product authorship.
- Exact product: `8a11bea1003b50d00d57318244a4bca8b138397b`.
- Product tree: `5b3cb2652ba24bf4142907f91cc5129247e646e3`.
- Exact handoff: `462a662b6a3bd56049bf47425b4a7c5165effaca`.
- Handoff tree: `3ef8e6c5fd4a428dadff9ac997b792eea7b8c526`; direct parent is
  the exact product. Handoff: `docs/execution/handoffs/X-15.md`.
- Base: `f78d0a0a3beac9a8ead04c62f21bfd55ef48591a`.
- Scoped binary product diff SHA-256:
  `153c13e866995a209c4dafbb74e051ea23d2e2ea901d63439f218a89f192ec75`.
- Scope: `clients/sonarr/`, X-15 handoff, X-15/A-07/A-09/A-10/A-45 and the
  standalone-client connector contract. Module source, generated boundary,
  compatibility specification, README and every included test were inspected.
- Review used a clean detached checkout at the handoff. Reviewer probes ran in
  a disposable archive of the exact product. Only this receipt is committed;
  product/state and unrelated files were untouched.

Upstream compatibility evidence was checked against immutable Sonarr
`3.0.10.1567` source commit `2639c069bc970c148024a42cb61f5f8611a58c4b`, within
the README's declared candidate 3.x range. No live upstream or private media
data was used.

## Findings

### R1 — P1: seriesId changes the native preview mode and ignores folder

Locations: `clients/sonarr/client.go:590`, `:675`; manual-import query contract
and README.

The API presents `Folder` as mandatory and `SeriesID` as an optional parameter
on the same folder-preview operation. In Sonarr 3.x, supplying `seriesId`
selects the library-series overload, with optional season number, rather than
filtering the downloaded folder. The upstream module branches before the
folder/download-ID overload. See the immutable
[ManualImportModule](https://github.com/Sonarr/Sonarr/blob/2639c069bc970c148024a42cb61f5f8611a58c4b/src/Sonarr.Api.V3/ManualImport/ManualImportModule.cs#L23).
The selected overload reads registered series files and scans its library path;
see [ManualImportService](https://github.com/Sonarr/Sonarr/blob/2639c069bc970c148024a42cb61f5f8611a58c4b/src/NzbDrone.Core/MediaFiles/EpisodeImport/Manual/ManualImportService.cs#L70).

`TestReviewerSeriesQueryUsesLibraryScope` models that documented native branch:
a request for folder `/downloads/Show` plus series 42 receives
`/library/Show/Episode.mkv`. `PreviewManualImport` accepts the unrelated library
result as complete folder-preview evidence. The current product fixture instead
assumes seriesId and folder jointly constrain a downloaded-media preview.

Required correction: expose/document distinct native folder versus library
preview modes, or reject the mode-changing combination in the downloaded-folder
operation. Never imply that seriesId filters the requested download folder.
Add synthetic native-branch fixtures and preserve exact scope semantics.

### R2 — P1: foreign-series association evidence is accepted as complete

Locations: `clients/sonarr/client.go:552`, `:721`, `:1091`, `:1128`;
`EpisodeFile` compatibility schema.

`ListEpisodeFiles(seriesID)` drops native file `seriesId` instead of validating
its requested scope. Native Sonarr 3.x files carry that field; see immutable
[EpisodeFileResource](https://github.com/Sonarr/Sonarr/blob/2639c069bc970c148024a42cb61f5f8611a58c4b/src/Sonarr.Api.V3/EpisodeFiles/EpisodeFileResource.cs#L10).
Manual-import validation checks the top-level series against the query, but
never checks each nested episode's series against that series/query.

Independent reproductions:

- `TestReviewerEpisodeFilesWrongSeriesAccepted`: querying series 42 receives
  file 9001 with `seriesId: 99`; the client accepts it, erases its foreign-series
  identity, and claims complete coverage.
- `TestReviewerNestedPreviewMissingAndCrossSeries/cross series`: query and
  candidate series are 42, while the selected nested episode belongs to 99;
  the client returns that candidate with complete coverage and no conflict.

This undermines A-09/A-10's exact, scoped title/file/episode evidence. A sent
filter is not proof that a returned association belongs to that filter.

Required correction: model/preserve native file series identity and validate
file/nested-episode series agreement against explicit query and parent scope.
Refuse contradictory associations without complete authoritative coverage.
Add both positive and foreign-series fixtures, including nested episode files.

### R3 — P2: declared Sonarr 3.x language and rejection text are discarded

Locations: `clients/sonarr/openapi.yaml` `ManualImportResource`/`ImportRejection`;
`clients/sonarr/client.go:1191`, `:1230` and preview fixture.

The contract/fixture use plural `languages` and rejection `message`. Sonarr
3.0.10.1567's resource uses singular `language`, while its rejections contain
`reason` and `type`. See immutable
[ManualImportResource](https://github.com/Sonarr/Sonarr/blob/2639c069bc970c148024a42cb61f5f8611a58c4b/src/Sonarr.Api.V3/ManualImport/ManualImportResource.cs#L12)
and [Rejection](https://github.com/Sonarr/Sonarr/blob/2639c069bc970c148024a42cb61f5f8611a58c4b/src/NzbDrone.Core/DecisionEngine/Rejection.cs#L2).

`TestReviewerNativeRejectionReasonDropped` returns a native rejection with
`reason: "Existing episode file must not be replaced"`; normalized rejection
text is empty. `TestReviewerNativeV3LanguageDropped` returns native
`language: {id: 1, name: "English"}`; normalized Languages is empty. Both responses
are accepted as complete. Unknown-field isolation silently removes evidence
that the declared compatibility contract should explicitly preserve.

Required correction: freeze native version-accurate DTOs and synthetic fixtures,
map 3.x fields into normalized observations, and explicitly document/test any
additional supported-version aliases. Preserve rejection diagnostics and native
language without inferring nonexistent data. Update the compatibility claim
if 3.x is intentionally unsupported instead.

### R4 — P2: missing required nested fields become known zero values

Locations: `clients/sonarr/client.go:961`, `:1039`, `:1091`, `:1277`.

Required-member checks inspect top-level records only. Generated DTO decoding
does not enforce schema `required` for nested objects; scalar omissions default
to zero, and the normalizers accept those zero values as real evidence.

- `TestReviewerNestedFileMissingSizeDefaultsZero`: a nested episode file omits
  schema-required size; its normalized size becomes zero and the episode page
  is complete.
- `TestReviewerNestedPreviewMissingAndCrossSeries/missing required numbers`:
  nested episode omits schema-required season/episode numbers; normalized
  values become 0/0 with complete preview coverage.

Missing evidence is being converted into a known value, contrary to strict
decoding and unknown-evidence requirements.

Required correction: validate required nonnull members recursively before
normalization, or model presence explicitly. Cover required nested identity,
numeric and language fields, omitted versus null values, and legitimate zero
sentinels. Reject malformed responses without inventing complete evidence.

### R5 — P2: offline generation fails after dependencies are available

Locations: `clients/sonarr/check-generation.sh:21`, `generate.go:4`.

Both entrypoints use `go run ...oapi-codegen@v2.8.0`, which performs a module
metadata lookup even after normal generation has succeeded. Exact reproduction
in the warmed review environment:

```sh
GOPROXY=off GOSUMDB=off ./check-generation.sh
```

Exit 1: `loading deprecation for github.com/oapi-codegen/oapi-codegen/v2:
module lookup disabled by GOPROXY=off`.

The same pinned generator invoked with the tools module's modfile works offline;
independent regeneration in the disposable archive is byte-identical to the
committed artifact. Thus this is entrypoint resolution, not missing generator
dependencies. A-45 and the offline deterministic guardrail requirement remain
unmet for the new module.

Required correction: invoke the pinned tools-module generator without online
version resolution, for both generation and reproducibility checking. Add an
offline warmed-dependency check; do not silently skip a failure.

## Verification and retained guarantees

| Check | Result |
| --- | --- |
| Six independent `TestReviewer*` probes, synthetic HTTP only, race, count 3 | Exit 0; all unexpected behaviors above reproduced |
| Sonarr `GOWORK=off go test -mod=readonly -race -count=3 -timeout=180s ./...` | Exit 0 |
| Sonarr `GOWORK=off go vet -mod=readonly ./...` | Exit 0 |
| Sonarr `GOWORK=off go mod verify` | Exit 0; all modules verified |
| Sonarr `./check-generation.sh` | Exit 0; byte-identical committed output |
| Sonarr offline generation check above | Exit 1; R5 |
| Offline tools-modfile pinned generation in disposable product archive, then artifact `cmp -s` | Exit 0; byte-identical output |
| Sonarr Vacuum via pinned tools module, `--remote=false --fail-severity=warn` and repository ruleset | Exit 0; 100/100, zero-warning/error gate |
| Sonarr pinned golangci-lint with tools modfile/repository config, `GOWORK=off` | Exit 0; zero issues |
| Sonarr CGO-free Linux amd64/arm64 readonly builds, `GOWORK=off` | Exit 0 both targets |
| `python3 scripts/check-architecture.py` | Exit 0 |
| `python3 scripts/check_planning.py` | Exit 0; 53 tasks, 60 acceptance cases; links resolve |
| `./scripts/check-guardrails.sh --ci` | Exit 0 for its current root/UI/tools/qBittorrent/NZBGet matrix |
| Review-start status, `git diff --check` | Clean exact handoff; exit 0 |

The OpenAPI document is 3.1.1 and read-only, with generated output committed
under the module's `internal/generated`. There are no exposed write methods or
Mastarr root/other-client imports. API-key headers, literal reverse-proxy prefix,
sanitized status errors, redirect rejection, bounded body/count handling and
request-context cancellation retain passing product tests and direct code
evidence. Complete full-array coverage is emitted only after accepted records
are retained; malformed/error paths do not claim complete coverage. However,
R1-R4 show that acceptance/normalization must be stricter before that coverage
is authoritative for reconciliation.

C-06/published-module bootstrap remains a coordinator-owned integration blocker:
the aggregate guardrails do not yet test/generate/lint the Sonarr module. Its
direct module checks above are separate evidence. Existing root adapters remain
in place; no permanent replace/go.work or root migration was added. G-01 stays
open and native Arr write capabilities remain blocked.

The receipt commit SHA is reported after commit. Integration, published CI,
release, deployment and verified live behavior are separate gates.
