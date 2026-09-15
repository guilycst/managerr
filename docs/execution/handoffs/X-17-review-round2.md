# X-17 independent review, round two

Decision: **changes_requested**. R1 and R2 remain partly open because missing
pagination metadata still becomes authoritative completeness or absence.
R3 invalid UTF-8 refusal and R4 canonical user-view fallback pass this review.

## Exact identity and scope

- Reviewer: `/root/x05_reviewer`, independent of correction authorship.
- Final product: `d6b84779c8b52b07ccd2e11093b089addca72002`.
- Product tree: `3f321f6aab6f326556a25558be9d0703db5652ef`.
- Handoff: `acf25a416d7f09d8459e945c3d4789029885bd1f` at
  `docs/execution/handoffs/X-17-correction-round1.md`.
- Handoff tree: `6acd10a80ffbb6fd9dee62ae12545e8f5fb10b88`;
  direct parent is the final product.
- Prior reviewed product: `f9e0754853e1855af37e61ac96c8bc70a7e017d4`.
- Prior receipt: `390ada5463f939d7a1b6dacced762c1f05ad873e`.
- Scoped binary correction diff SHA-256:
  `dfd7c81df0e2f2b44fe8bb7d1384d025de4b76cbaf823853e76be10e2f3859f5`.
- Scope: `clients/jellyfin/`, correction handoff, R1-R4 and the X-17
  contribution to A-08/A-09/A-45/A-55 and standalone connector policy.
- Review ran in a clean detached checkout at the exact handoff. Independent
  probes and write-mode generation ran in an archive of the exact product.
  Only this receipt is committed; product, state and unrelated paths are untouched.

## Remaining findings

### R1a — P1: missing native offset still proves complete coverage

Locations: `clients/jellyfin/client.go:851`, `:873`, `:406`, `:446`-`:458`.
Contract: `/Items` description in `clients/jellyfin/openapi.yaml:132` states
that missing total/offset metadata remains unknown or malformed. The README
also requires native start and total metadata to prove the boundary.

`decodeItems` initializes `start` to zero. When an envelope omits `StartIndex`,
that default passes the requested-offset comparison for a first-page request.
A matching total then produces complete coverage, although the response did
not establish its native offset. Explicit null has the same generated-pointer
representation and must not silently become a proven zero either.

Independent `TestReviewerMissingStartCannotBeComplete` returns:

```json
{"Items":[{"Id":"one"}],"TotalRecordCount":1}
```

`ListItems(context.Background(), ItemQuery{Limit: 1})` returns nil error and
complete coverage. The expected-uncertainty assertion fails three times with
`-race`. This is a remaining R1 contract defect, distinct from the repaired
short-page traversal and impossible-total cases.

Required correction: retain presence/validity of envelope start metadata through
decoding and completeness evaluation. Missing/null native offsets cannot prove
complete coverage or absence. Preserve observed items with unknown coverage, or
return a typed malformed response according to the versioned contract. Add first
page, empty exact-ID, explicit-null and offset-request regressions. Do not weaken
the missing-evidence invariant to make the existing default authoritative.

### R2a — P1: missing total on empty exact lookup still proves absence

Locations: `clients/jellyfin/client.go:434`-`:444`, `:553`-`:555`.
Contract: `/Items` missing-metadata description, prior R2 requirement that
incomplete/unknown evidence must not prove absence, and the discovery invariant
that missing evidence remains unknown.

The correction explicitly promotes an empty single-ID envelope without
`TotalRecordCount` to complete coverage. `ObserveItem` then reports `not_found`.
The requested ID limits intended scope but does not supply missing response
coverage metadata. No native version fixture or contract exception establishes
that this incomplete envelope is authoritative; the OpenAPI description says
the opposite.

Independent `TestReviewerMissingTotalEmptyCannotProveAbsence` asserts
`Ids=wanted` and returns:

```json
{"Items":[],"StartIndex":0}
```

`ListItems(ItemQuery{ItemIDs: []string{"wanted"}, Limit: 1})` reports complete;
`ObserveItem("wanted")` returns `not_found`, status 404. Both expected-uncertainty
assertions fail three times with `-race`. A separate positive control with
`Items: []`, `TotalRecordCount: 0`, and `StartIndex: 0` still returns `not_found`.
This finding concerns incomplete envelopes; it does not reject the contract's
separate compatible full-array shape.

Required correction: remove the missing-total empty-envelope promotion. Keep
missing/null totals unknown or malformed, and reserve absence for a valid
complete exact lookup. Cover `ListItems`, `Items`, `ObserveItem`, `GetItem` and
traversal, including total omission/null, complete native empty envelopes,
partial empty envelopes, foreign/mixed identities and valid array compatibility.

## Fixes verified directly

- R1 short partial pages now continue through bounded traversal. A multi-request
  result remains partial. Impossible totals are refused; interrupted reads retain
  previously observed items with unknown coverage. Included tests cover missing
  general totals, proven tails, page bounds and interruption. The two missing
  metadata cases above are not covered by those tests.
- R2 foreign and mixed explicit-ID results are refused before observations escape.
  The check applies through `Items`, `ObserveItem` and traversal. Correct exact
  empty envelopes still report absence. Remaining incomplete-empty behavior is R2a.
- R3 `validateJSON` rejects invalid raw UTF-8 before token walking and decoding.
  Included tests cover item/library/provider/unknown fields and retain valid
  Japanese/CJK identity, title and provider strings.
- R4 contract and generated code describe canonical `/UserViews` and optional
  `userId`. Handwritten fallback sends configured user query or preserves token
  user context without inventing `Me`. Both configured and unconfigured cases
  are tested. The legacy explicit-user route remains module-internal contract
  surface; fallback does not call it.
- Generated types remain module-internal. No root or other-client imports or raw
  generated DTOs leak into public observations. Transport, sanitized errors,
  deadlines, bounds and refresh semantics retain the previously reviewed boundary.
- Refresh acceptance remains a status observation separate from later item reads.
  No read triggers refresh; unsupported item refresh remains explicit typed failure.
  No Arr write, filesystem mutation or live media-stack operation was introduced.

## Independent checks

Every command below used the exact product or its direct handoff checkout.
Module commands used `GOWORK=off`; generation used `GOPROXY=off GOSUMDB=off`.
All fixtures were synthetic.

- Module `go test -mod=readonly -race -count=3 -timeout=180s ./...`: exit 0.
- Module `go vet -mod=readonly ./...` and `go mod verify`: exit 0; all modules verified.
- Offline `./check-generation.sh`: exit 0; generated artifact matches.
- Offline `go generate ./...` in exact-product archive plus byte comparison: exit 0.
- Pinned Vacuum through tools module, repository ruleset, `--remote=false`
  and `--fail-severity=warn`: exit 0; quality 100/100, zero warnings/errors.
- Pinned golangci-lint through tools module, repository configuration: exit 0;
  zero issues.
- Linux amd64 and arm64 `CGO_ENABLED=0 go build -mod=readonly ./...`: exit 0 each.
- Independent `TestReviewerCorrectionClosures` and `TestReviewerValidExactEmpty`,
  `-race -count=3`: exit 0. Short partial traversal, foreign rejection, raw UTF-8
  refusal, token-context canonical fallback and complete exact empty passed.
- Independent expected-safety probes `TestReviewerMissingStartCannotBeComplete`
  and `TestReviewerMissingTotalEmptyCannotProveAbsence`, `-race -count=3`: exit 1;
  both findings reproduced each run, without race-detector findings.
- `./scripts/check-guardrails.sh --ci`: exit 0; existing generation, API/Vacuum,
  architecture, lint, root/UI/tools/qBittorrent/NZBGet tests/vet/module checks pass.
- `python3 scripts/check_planning.py`: exit 0; 53 tasks, 60 acceptance cases,
  local links resolve.
- `git diff --check`: exit 0 before receipt.

## Separate gates and handoff

The aggregate CI matrix still omits Jellyfin. Direct module checks provide this
review's evidence; C-06 inclusion and module release/bootstrap remain open before
root adapter migration. Pinned native versions, refresh capabilities and deployment
mapping validation remain separate gates. No end-to-end acceptance closure,
publication, deployment or verified live behavior is claimed. G-01 remains open;
no Arr write capability is enabled.

Next action: correct R1a/R2a with executable missing/null metadata regressions,
then request an independent review at exact product and handoff commits. Exact
receipt SHA is reported after commit. Product/state paths remain unchanged.
