# X-17 independent review, round one

Decision: **changes_requested**. Two P1 and two P2 findings remain. Module
isolation, reproducibility and explicit refresh acceptance separation pass, but
inventory authority and one native fallback contract need correction.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`; independent of product authorship.
- Exact final product: `f9e0754853e1855af37e61ac96c8bc70a7e017d4`, including
  initial product `a48d7b3a6892c36a52c83293f1973739448e8c8c`.
- Product tree: `e333961337ec88661302228a9d4204bae4ceff4b`.
- Exact handoff: `9eb1edadf323fccd3278a30a0f19a765cbf63de9`.
- Handoff tree: `4936ac9742436c10cc388d6a51f58489984ba447`; direct parent is
  the final product. Path: `docs/execution/handoffs/X-17.md`.
- Base: `b8b1f42ec86116eb087ef977885e322de7ba9bc1`.
- Scoped binary product diff SHA-256:
  `e2366d8bd634636b15431e59a883ab4b7bda8f85aeba9c11c6bfe08c84fdab34`.
- Scope: `clients/jellyfin/`, X-17 handoff, A-08/A-09/A-45/A-55 and the
  standalone-client connector policy. Source, every included fixture,
  specification, generated boundary and README were inspected directly.
- Review used a clean detached checkout at the exact handoff. Independent
  probes and write-mode regeneration ran in an archive of the exact product.
  Only this receipt is committed; product/state and unrelated files were untouched.

## Findings

### R1 — P1: incomplete traversal is promoted to complete inventory

Locations: `clients/jellyfin/client.go:501`, `:514`, `:418`-`:426`, `decodeItems`;
OpenAPI `/Items` coverage description and A-08 missing-evidence invariant.

`ListAllItems` stops on any short page. Its final coverage starts as complete
and is downgraded only for multiple requests or unknown coverage; an explicitly
partial first page is therefore upgraded to complete.

Independent `TestReviewerShortPartialTraversal` returns
`{"Items":[{"Id":"one"}],"TotalRecordCount":3,"StartIndex":0}` for
`ListAllItems(ItemQuery{Limit: 2})`. At the exact product, only one HTTP request
occurs, one item is retained, and the result is complete despite native total
three. The existing `pagination_continues` reason can coexist with this false
complete status. Reproduced three times with `-race`.

Independent `TestReviewerImpossibleTotal` also shows that
`{"Items":[{"Id":"one"}],"TotalRecordCount":0,"StartIndex":0}` is accepted
as complete by `ListItems(Limit: 1)`. Count contradictions are not rejected.
An offset arithmetic comparison alone is insufficient completeness evidence.

Required correction: never upgrade partial evidence on early/short termination;
continue safely or retain partial/unknown coverage. Validate count/start/items
consistency and make native missing-total/offset/array coverage assumptions
explicit. Add short partial, contradictory total, missing total, tail page,
page limit and interrupted traversal regressions. Preserve previously observed
items without claiming absence for unobserved records.

### R2 — P1: requested-ID evidence accepts foreign items and reports false absence

Locations: `clients/jellyfin/client.go:393`, `:529`, `:538`; `/Items` `Ids`
contract; A-08/A-09 and connection-scoped exact identity evidence.

`ListItems` sends `ItemIDs` but never checks returned IDs against that scope.
`ObserveItem` then maps a foreign-ID response to `not_found`, conflating an
upstream contradiction with authoritative absence.

Independent `TestReviewerScopedIDsForeignEvidence` asserts `Ids=wanted` and
returns `{"Items":[{"Id":"foreign"}],"TotalRecordCount":1,"StartIndex":0}`.
`ListItems(ItemIDs: [wanted], Limit: 1)` returns the foreign item with complete
coverage; `ObserveItem(wanted)` returns `ErrorNotFound`. Both behaviors were
asserted and reproduced three times with `-race`. The existing product test
explicitly expects `not_found` for foreign identity, so it encodes this error.

Required correction: validate every returned item against explicit requested
IDs before exposing authoritative scoped evidence. Foreign evidence is
malformed/conflicting/unknown, not proof of absence. Reserve `not_found` for a
valid authoritative empty requested-ID result; incomplete/unknown responses
must not prove absence. Apply this through aliases and traversal, and add
foreign/mixed/partial/missing-total/valid-empty cases. Keep same native IDs in
different client connections independent.

### R3 — P2: malformed UTF-8 is rewritten into accepted identity

Locations: `clients/jellyfin/client.go:1044`, `:1060`, `:880`; strict JSON
boundary, required item `Id`, A-09/A-45 and handoff malformed-response claims.

The raw JSON boundary does not validate UTF-8. Go JSON decoding replaces an
invalid byte inside a string with U+FFFD; later text validation sees valid
UTF-8 and accepts the altered identity.

Independent `TestReviewerMalformedUTF8` returns raw bytes for
`{"Items":[{"Id":"<0xff byte>"}],"TotalRecordCount":1,"StartIndex":0}`.
`ListItems(Limit: 1)` succeeds, returns item ID U+FFFD, and reports complete
coverage. Reproduced three times with `-race`. This loses exact upstream
identity and contradicts strict malformed-response refusal.

Required correction: reject invalid UTF-8 before JSON decoding or token walking.
Cover item/library identities, provider values and unknown fields; retain valid
Unicode support. Never normalize corrupted wire identity into authoritative data.

### R4 — P2: default current-user fallback is a fictional native alias

Locations: `clients/jellyfin/client.go:47`, `:323`; OpenAPI
`/Users/Me/Views`, generated fallback route and README library fallback.

With no configured user ID, a missing media-folders route falls back to
`/Users/Me/Views`. Jellyfin 10.10.7 does not resolve `Me` as a user alias on
that path. Primary source was checked at immutable commit
`aefb9b2cffca3ccb7a95d89af0d9fb2040de6ed3`, resolved from its release tag.
[UserViewsController](https://github.com/jellyfin/jellyfin/blob/aefb9b2cffca3ccb7a95d89af0d9fb2040de6ed3/Jellyfin.Api/Controllers/UserViewsController.cs#L60)
exposes `/UserViews` with optional user query/context, while the legacy
`/Users/{userId}/Views` requires a GUID route value. `Me` cannot satisfy it.

Independent `TestReviewerCurrentUserFallbackRoute` provides a successful
canonical `/UserViews` fixture, a missing media-folders response and a synthetic
invalid-user rejection for the legacy path. The client chooses
`/Users/Me/Views` and fails instead of using the valid canonical surface.
Reproduced three times with `-race`. The fixture's HTTP 400 models rejection;
no live native HTTP response was run. Included fallback tests cover only a
configured user path and do not exercise this default branch.

Required correction: version the native canonical user-view route and its
query/auth-context requirements, or refuse fallback without sufficient user
context. Keep explicit user selection and read-only behavior. Regenerate the
contract and add no-configured-user plus explicit-user synthetic native-shape
cases. A missing pinned runtime release is a capability gate, not support for a
non-native route in the compatibility specification.

## Passing evidence and evidence limits

The module owns its Go manifests, OpenAPI 3.1.1 document, generated internal
package, transport and normalized observations. No root/other-client imports
or raw generated DTOs leak into public observations. Token headers, deadlines,
cookie-free cloned HTTP client and sanitized errors are implemented. Bounds,
duplicate/trailing/deep JSON and provider/media-source validation were inspected.

Refresh calls are explicit and separate from reads. Library acceptance records
native status/evidence without availability; the included fixture performs a
later item read while unavailable and again when visible. Item refresh remains
an explicit caller capability gate and unsupported routes are typed errors.
No Arr registration/import or media/filesystem mutation is enabled.

The committed test file has seven top-level tests. It does not exercise
`ListAllItems`, deep JSON, redirects or duplicate media sources despite those
scenarios appearing in the handoff's focused-tests claim. Those claims are not
independent passing evidence. Future handoffs must distinguish inspected code,
synthetic executable regressions and still-open native version gates.

## Independent checks

All checks below passed with exit status zero. Module commands used
`GOWORK=off`; generation used `GOPROXY=off GOSUMDB=off`. Tests used synthetic
fixtures only, with no live media stack, private endpoint or inventory.

| Check | Result |
| --- | --- |
| Jellyfin `go test -mod=readonly -race -count=3 -timeout=180s ./...` | Passed; included expectations do not catch R1-R4. |
| Reviewer `go test -mod=readonly -run TestReviewer -race -v -count=3 -timeout=120s ./...` | Passed; R1-R3 bad-behavior reproductions and bounded status observation. |
| Reviewer current-user fallback probe, `-race -count=3` | Passed; R4 route mismatch reproduced. |
| `go vet -mod=readonly ./...`, `go mod verify` | Passed; all modules verified. |
| Offline `./check-generation.sh` | Passed. |
| Offline `go generate ./...` in exact-product archive plus byte comparison | Passed; committed artifact reproduced through pinned tools module. |
| Pinned Vacuum offline, repository ruleset, `--remote=false --fail-severity=warn` | Passed; quality 100/100, zero warnings/errors. |
| Pinned golangci-lint through tools module and repository configuration | Passed; zero issues. |
| Linux amd64/arm64 `CGO_ENABLED=0 go build -mod=readonly ./...` | Passed for both architectures. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed; 53 tasks, 60 acceptance cases and local links. |
| `./scripts/check-guardrails.sh --ci` | Passed; existing matrix generation/API/Vacuum/architecture/lint/tests/vet/verification. |
| `git diff --check` | Passed before receipt. |

## Separate gates and checkpoint

The aggregate scripts/CI matrix still omit Jellyfin. Direct module checks above
cover this review; coordinator-owned matrix expansion and consumable module
release or approved bootstrap remain required before root adapter migration.
A-08/A-09/A-45/A-55 end-to-end closure is not claimed.

No Jellyfin product release is pinned in the compatibility matrix. Runtime
refresh enablement, native version fixtures and deployment mapping verification
remain open. G-01 remains open and no Arr write capability is enabled. Local
success is separate from publication, deployment and verified live behavior.

Only this receipt is committed. Exact review SHA is reported after commit;
execution state and product paths remain unchanged.
