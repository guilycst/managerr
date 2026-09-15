# X-17 independent review, round three

Decision: **approved** for the scoped standalone client correction. R1a/R2a
are closed. Prior R1-R4 fixes remain intact. No new blocking finding was found.
This decision does not close native runtime, root integration or release gates.

## Exact identity and scope

- Reviewer: `/root/x05_reviewer`; independent of correction authorship.
- Exact product: `4eee8e84eff5a81b24c128444fa6531fec83f5ec`.
- Product tree: `714ac921c40d27729a063dc08b8ccb676f6f7c55`.
- Exact handoff: `9349423c59c1ef801fdfe3a86316bd4be759799c`, path
  `docs/execution/handoffs/X-17-correction-round2.md`.
- Handoff tree: `0cf7f62f06289cbf37982ad511c4799a2699976c`; direct parent is
  the product above.
- Previous product: `d6b84779c8b52b07ccd2e11093b089addca72002`.
- Previous receipt: `942248df68f0c2027a260513c7b913fe990e7ccf`.
- Scoped binary correction diff SHA-256:
  `416e2ca0012d4350a6b9e5b44060e38ec634c014f00ed5e9db09658e7c961af9`.
- Scope: `clients/jellyfin/`, correction handoff, R1a/R2a and prior R1-R4,
  against X-17's A-08/A-09/A-45/A-55 contribution and standalone connector policy.
- Product source, contract, generated boundary, tests and handoff were inspected
  directly. Review ran in a clean detached checkout at the exact handoff.
  Independent probes and write generation ran in an archive of the exact product.
  Only this receipt is committed; product/state and unrelated paths are untouched.

## Findings disposition and independent evidence

R1a is closed. The private `decodedItems` value preserves native start/total
presence separately from their numeric defaults. Missing or null envelope offsets
cannot establish complete coverage, including a first-page request where zero
previously passed the comparison. Existing observed items remain available with
unknown coverage and an explicit missing-start reason. An explicit mismatched
native offset remains malformed.

R2a is closed. The missing-total empty-envelope exception is removed. Missing or
null totals return unknown coverage, including exact scoped empty reads. `ObserveItem`
and `GetItem` report unknown for these responses. A valid exact empty envelope with
explicit `TotalRecordCount: 0` and `StartIndex: 0` still reports `not_found`.
The separately declared full-array compatibility shape is unchanged; it is not
an incomplete envelope or a pinned native runtime fixture.

The two previously failing reviewer probes now pass three times under `-race`.
A separate independent matrix covers missing/null both fields, each field alone,
and partial empty results. Every case is checked through `ListItems`, `Items`,
`ListAllItems`, `ObserveItem` and `GetItem`. None establishes complete coverage or
absence. Positive exact-empty and negative offset/count controls pass as well.

Prior fixes remain safe within the reviewed scope:

- Short partial pages continue through bounded traversal; multi-request observations
  remain partial because no immutable native snapshot is established. Included tests
  retain valid complete/tail pages, missing totals, page limits and interruption.
  An independent short-page traversal retains both observed items without complete
  coverage. Contradictory totals and mismatched offsets are refused.
- Every returned explicit-ID item is checked against requested scope. Foreign and
  mixed evidence is malformed, including aliases and traversal. Independent foreign
  identity lookup does not become `not_found`; exact valid absence remains supported.
- Raw invalid UTF-8 is rejected before JSON token walking or decoding. Included
  item/library/provider/unknown-field and valid Japanese/CJK regressions pass;
  an independent raw-byte identity probe also passes.
- Canonical `/UserViews` fallback preserves configured `userId` or token user context.
  Included explicit-user/default tests and an independent authenticated default-route
  probe pass. No invented `/Users/Me/Views` request is made.

OpenAPI remains 3.1.1. Its optional native metadata and missing-evidence description
align with presence-aware handwritten normalization. Committed generated pointer
fields and `/UserViews` query shape reproduce unchanged from the contract.
Generated values remain module-internal; public normalized observations expose no
raw generated DTOs or root/other-client types. The client adds no root or other-client
imports. Token transport, deadlines, bounds, strict decoding and sanitized error
handling retain the previously reviewed boundary.

Refresh acceptance remains separate from item visibility. Included refresh tests
observe accepted status, later absence and later visibility through distinct reads;
reads never initiate refresh. Unsupported item refresh remains explicit typed failure.
No Arr registration/import, search, filesystem operation or live stack mutation is added.

## Independent checks

All checks below passed with exit status zero. Module commands used `GOWORK=off`;
generation used `GOPROXY=off GOSUMDB=off`. Fixtures were synthetic only.

- Jellyfin `go test -mod=readonly -race -count=3 -timeout=180s ./...`.
- Jellyfin `go vet -mod=readonly ./...` and `go mod verify`: all modules verified.
- Offline `./check-generation.sh`: committed output matches.
- Offline `go generate ./...` in exact-product archive followed by byte comparison
  against the committed internal generated artifact.
- Independent `go test -mod=readonly -run TestReviewer -race -v -count=3
  -timeout=120s ./...`: all old failure probes, missing/null matrix, positive exact
  empty, count/offset refusal and prior correction controls pass, with no races.
- Pinned Vacuum through tools module, repository ruleset, `--remote=false` and
  `--fail-severity=warn`: quality 100/100, zero warnings/errors.
- Pinned golangci-lint through tools module, repository configuration: zero issues.
- Linux amd64 and arm64 `CGO_ENABLED=0 go build -mod=readonly ./...`.
- `./scripts/check-guardrails.sh --ci`: generation/API/Vacuum, architecture,
  existing module lint/tests/vet/verification all pass.
- `python3 scripts/check_planning.py`: 53 tasks, 60 acceptance cases and local links.
- `git diff --check` before receipt.

## Separate gates and next checkpoint

The aggregate scripts/CI matrix still omits Jellyfin. Direct module checks above
provide scoped evidence; C-06 matrix inclusion and consumable module release or
approved bootstrap remain required before root adapter migration. Native release
pins, refresh capability fixtures and actual deployment mapping verification remain
open. A-08/A-09/A-45/A-55 end-to-end closure is not claimed.

G-01 remains open; no Arr write capability is enabled. Local review approval does
not imply publication, deployment or verified live behavior. Coordinator may
integrate this receipt and record exact product/handoff/review SHAs. Only this
receipt is committed; its exact SHA is reported after commit.
