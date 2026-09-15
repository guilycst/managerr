# X-15 independent review, round two

Decision: **changes_requested**. One P2 finding remains in language alias
normalization. The two P1 findings and the remaining original P2 findings are
closed by directly inspected code and independent synthetic probes.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`; independent of product authorship.
- Exact product: `f258104718691807e9679e0c6eb278b144ea7a09`.
- Product tree: `0c2147b8cf3aaac99970a6fac5727a0a5de03f2c`.
- Exact correction handoff: `c8a35027540234a5a1a3e474f6059dc30715a612`.
- Handoff tree: `733fdd50d9e0b41d3c52f3db2edbfce7c9d1d362`; direct parent is
  the exact product. Path: `docs/execution/handoffs/X-15-correction-round1.md`.
- Correction base: `49d7bb8f000353f6f6909247ff8001e34f0f0524`.
- Prior review: `907815db38e3b336fbc51e7d96f37a94d6110469`.
- Scoped binary correction diff SHA-256:
  `7cde5a520732a24bc961edbacede164afbc5048072a02603922aaee096f3e47f`.
- Scope: `clients/sonarr/`, correction handoff, R1-R5, X-15,
  A-07/A-09/A-10/A-45 and the standalone-client connector policy.
- Review used a clean detached checkout at the handoff. Reviewer probes ran in
  a disposable archive of the exact product. Only this receipt is committed;
  product, execution state and other lane files were untouched.

## Remaining finding

### R3a — P2: non-equivalent language aliases still contribute complete evidence

Locations: `clients/sonarr/client.go:1440`, `:1444`, `:1473`;
`clients/sonarr/README.md:50` and correction handoff's R3 closure claim.

The contract describes plural `languages` as a compatibility alias of singular
`language`; both must agree when supplied together. `normalizeLanguages`
searches for one matching plural entry, then accepts the entire array. A
matching entry plus additional different languages passes this check.

Independent synthetic reproduction, `TestReviewerMixedLanguageAliases`:

```json
[{"id":1,"path":"/downloads/Episode.mkv","relativePath":"Episode.mkv",
  "name":"Episode.mkv","size":10,
  "language":{"id":1,"name":"English"},
  "languages":[{"id":1,"name":"English"},{"id":2,"name":"Portuguese"}]}]
```

Call `PreviewManualImport(context.Background(),
ManualImportQuery{Folder: "/downloads"})` against an `httptest` server returning
that body. At the exact product, it returns no error, `Coverage.Completeness`
equal to `complete`, singular English and both plural entries. The reviewer
probe explicitly asserts this defective acceptance; its passing result is a
reproduction, not evidence of correct behavior. Reproduced three times with
`-race`.

The documented alias agreement is therefore unenforced and downstream readers
can receive contradictory language observations as authoritative complete
coverage. This is narrower than the original R3 data-loss defect: native
language and rejection text are now preserved correctly.

Required correction: enforce equivalence of simultaneously supplied aliases,
including cardinality, rather than containment. Under the current singular
alias contract, require exactly one matching plural entry. If distinct
semantics are intended instead, establish them with upstream compatibility
evidence and revise the contract explicitly. Add cases for native-only,
plural-only, equivalent simultaneous forms, matching-plus-extra forms in both
orders, and contradictions. Ambiguous aliases must not yield complete evidence.

## Prior finding verification

- **R1 closed:** downloaded-folder previews reject `SeriesID` before any
  request. Independent atomic request counting confirmed zero network calls
  for the ambiguous combination. Separate library previews send `seriesId`
  without `folder` or `downloadId`; synthetic native-mode responses and exact
  GET query assertions pass. The README and OpenAPI describe both modes.
- **R2 closed:** episode files retain and validate native `seriesId`.
  Top-level and nested episodes/files are checked against requested and parent
  series scope. Independent foreign direct-file, nested-file and preview-episode
  responses are rejected as malformed rather than complete evidence.
- **R3 partly closed:** native singular language and rejection `reason` are
  retained. Alias-only language and `message` also survive normalization;
  rejection reason/message evidence is mirrored consistently and contradictory
  text is rejected. R3a above remains.
- **R4 closed:** raw recursive validation precedes generated scalar decoding.
  Included tests and independent missing/null nested episode-number,
  missing nested-file size and duplicate nested-ID probes reject malformed
  evidence. Required nested identity, association, language and rejection
  shape checks were inspected directly.
- **R5 closed:** both generation entry points resolve the pinned generator via
  the repository tools module. Offline `go generate` in the exact-product
  archive produced bytes identical to the committed generated artifact;
  offline `check-generation.sh` also passed.

## Independent checks

All commands below passed. Sonarr commands ran with `GOWORK=off`, independently
of the aggregate CI matrix. Generation also used `GOPROXY=off GOSUMDB=off`.
Only synthetic fixtures were used; no live stack or private data was accessed.

| Check | Result |
| --- | --- |
| Sonarr `go test -mod=readonly -race -count=3 -timeout=180s ./...` | Passed. |
| Reviewer `go test -mod=readonly -run TestReviewer -race -v -count=3 -timeout=120s ./...` | Passed; R3a acceptance reproduced, other scope/shape/native-field probes passed. |
| Sonarr `go vet -mod=readonly ./...`, `go mod verify` | Passed; all modules verified. |
| Offline Sonarr `./check-generation.sh` | Passed. |
| Offline Sonarr `go generate ./...` in exact-product archive plus byte comparison | Passed; artifact identical. |
| Pinned Vacuum with repository ruleset, `--remote=false --fail-severity=warn` | Passed; quality 100/100, zero warnings/errors. |
| Pinned golangci-lint with tools module and repository configuration | Passed; zero issues. |
| Sonarr Linux amd64/arm64 `CGO_ENABLED=0 go build -mod=readonly ./...` | Passed for both architectures. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed; 53 tasks, 60 acceptance cases and local links. |
| `./scripts/check-guardrails.sh --ci` | Passed; generation, API/Vacuum, architecture, lint, tests, vet and verification for existing matrix. |
| `git diff --check` | Passed before receipt. |

The generated client remains module-local under `internal/generated`, with no
raw public generated client, root/other-client imports or mutation methods.
Handwritten endpoint/path checks, API-key transport, deadlines, bounded strict
response decoding and typed sanitized errors remain intact. Included failure,
unknown-field, full-array and scope tests were inspected and rerun.

## Separate integration gates

The aggregate module/CI matrix still omits Sonarr. Direct module checks above
cover this review; coordinator-owned matrix expansion and module publication
or approved bootstrap remain required before root adapter migration. This is
an existing blocker, not resolved by passing the existing CI matrix.

G-01 remains open. This read-only client enables no Arr registration/import,
search or command writes. Local fixture success is separate from upstream
compatibility certification, publication and live-stack verification.

Only this receipt is the review commit. Its exact SHA is reported to the
coordinator after commit; no execution-state or product file is changed.
