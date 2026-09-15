# X-18 independent review, round one

Decision: **changes_requested**. One P1 and two P2 findings remain. Read-only
scope, generated isolation and bounded pagination pass the reviewed checks,
but availability certainty, request deadlines/cancellation and service evidence
validation need correction.

## Exact identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product authorship.
- Exact product: `67b01ca1d5555bfe11b348a96bda0b6a67d16bc3`.
- Product tree: `471b6a40cfd15716495bf43bda81aab92b69ee09`.
- Exact handoff: `7742877c37a0b8f0b6bd5d04ed01c0c4d7bceb16`, path
  `docs/execution/handoffs/X-18.md`.
- Handoff tree: `d4dc07992ccba20f24c10100348996bef55172b5`; direct parent is
  the product above.
- Recorded product base: `1568d815eb218f619c02c66269af007fde6cd3b9`.
- Scoped binary product diff SHA-256:
  `c442e1d349200f478efa2211a08fa52885a13b01fe36149073c9701e0bd5c0b3`.
- Scope: `clients/seerr/`, X-18 handoff and A-08/A-09/A-45/A-54 contribution
  against the standalone connector policy. Source, contract, generated boundary,
  README and every included test fixture were inspected directly.
- Review used a clean detached checkout at the exact handoff. Independent probes
  and write-mode generation ran in an archive of the exact product. Only this
  receipt is committed; state/product and unrelated paths are untouched.

## Findings

### R1 — P1: unknown native status becomes known negative availability

Location: `clients/seerr/client.go:1152`-`:1181`.
Contract: A-08/A-54, connector separation of native status and availability,
README unknown-evidence semantics, and `AvailabilityObservation.Known`.

`normalizeStatus` reports whether a native field is present. `normalizeAvailability`
uses that presence boolean as availability certainty. Native UNKNOWN and future
unrecognized statuses therefore produce `Known: true`, `Available: false`,
`PartiallyAvailable: false`, despite the accompanying unknown reason. Consumers
following the explicit certainty field receive a known negative observation.

Primary Seerr enum evidence was checked at immutable commit
`6fa7473dbb3bc0a44fd47748435f53a3480cc4da`, resolved read-only from `develop`:
[media constants](https://github.com/seerr-team/seerr/blob/6fa7473dbb3bc0a44fd47748435f53a3480cc4da/server/constants/media.ts#L14)
assign native UNKNOWN value 1. This source check is not a frozen product-version
fixture or runtime compatibility approval.

Independent `TestReviewerUnknownAvailabilityRemainsUnknown` returns a valid
single-record native page with `status: 1` and then `status: 99`, with `status4k: 5`.
Both return the certainty contradiction above. Expected-unknown assertions fail
three times with `-race`; native numeric values and unknown names are retained.

Required correction: separate field presence from known availability. Preserve
native status numbers/presence, but keep availability unknown for native UNKNOWN,
missing and unrecognized values. Explicitly document the meaning of each certainty
field. Add top-level and nested-request regressions for missing/UNKNOWN/future
status alongside pending, processing, partial, available, blocklisted and deleted
controls. Do not infer Jellyfin availability or request completion from these reads.

### R2 — P2: existing caller deadline disables request timeout; body cancellation loses identity

Locations: `clients/seerr/client.go:1520`-`:1530`, `:1510`-`:1515`, `readBounded`.
Contract: X-18 deadlines and typed errors, README per-request timeout, standalone
connector bounded transport and cooperative context handling.

`requestContext` returns any caller context with a deadline unchanged. A long
workflow deadline therefore disables the configured shorter request timeout.
The correct request bound is the earlier of the caller deadline and configured
request timeout; `context.WithTimeout` already preserves that earlier bound.

Independent `TestReviewerConfiguredDeadlineOverridesLongParent` configures a
20 ms request timeout and a one-hour caller deadline. Its synthetic RoundTripper
inspects the request context without waiting: the native request retains almost
one hour instead of the configured bound. The assertion fails three times with
`-race`.

After headers arrive, `readBounded` discards the underlying read error and `request`
returns generic `unknown`, status 200, without checking request-context failure.
Independent `TestReviewerBodyCancellationPreserved` returns a synthetic body which
cancels the caller during `Read`. The result does not satisfy
`errors.Is(err, context.Canceled)`; it is `Seerr seerr.status failed (unknown, status 200)`.
This assertion also fails three times with `-race`. The included cancellation test
only covers cancellation before the request starts.

Required correction: apply a child per-request timeout even when a caller deadline
exists, retain shorter caller deadlines, and preserve cancellation/deadline errors
through body reads without exposing response/endpoint/credential detail. Add long
and short parent deadline, cancellation before/during headers/body and body timeout
regressions. Keep automatic mutation retries absent.

### R3 — P2: service relationship validation is bypassed and malformed service failures disappear

Locations: `clients/seerr/client.go:1238`-`:1246`, `:1445`-`:1460`.
Contract: OpenAPI Media service ID minima and slug bounds, strict decoding/typed
service evidence, and `/api/v1/request` description that serviceErrors do not
become successful or empty service results.

`serviceRelationships` stringifies service IDs and copies slugs without the
validation used by other normalized fields. A valid page containing
`serviceId: -999`, `externalServiceId: -888`, `externalServiceSlug: "bad\u0000slug"`
returns a normal `radarr` relationship with those negative identities and NUL
text, complete coverage and no uncertainty reason. Independent
`TestReviewerServiceEvidenceValidation` reproduces this three times under `-race`.

`normalizeServiceErrors` silently skips invalid kind text and silently truncates
its total collection. Independent `TestReviewerMalformedServiceErrorsCannotDisappear`
returns a valid empty request page and `serviceErrors` under the JSON key
`radarr\u0000`, containing an offline service record. The client returns nil error,
complete coverage and empty serviceErrors. The expected-refusal/uncertainty assertion
fails three times with `-race`. The truncation branch was inspected, not separately
executed by this reproduction.

Required correction: validate service identities and text before exposing normal
relationships, including 4K and nested-request paths. Model documented native
sentinels explicitly as unknown/unbound instead of normal manager identities.
Reject or preserve explicit uncertainty for malformed service-error keys, records,
text and collection limits; never silently convert rejected failure evidence into
an empty success surface. Add valid zero/native-sentinel, negative out-of-contract,
control-character/oversized text, nested/4K and per-kind/total bound regressions.

## Passing evidence and limits

OpenAPI is 3.1.1 and narrow to GET status/media/request. Generated transport and
DTOs stay in `internal/generated`, reproduce from the pinned tools module and do
not leak into public normalized observations. No root or other-client imports,
local replaces, writes, search or hidden discovery methods are present.

API-key and bearer headers are verified by synthetic fixtures only; native runtime
authentication compatibility remains a product-version gate. Redirects are refused,
credential-bearing endpoints rejected, and errors/diagnostic strings omit credentials.
Raw UTF-8, duplicate keys, trailing JSON and nesting are checked before DTO decoding.

Native/provider/service/status/timestamp fields are represented independently;
media partial availability remains separate from request status. The explicit
availability certainty defect is R1. Invalid optional timestamps retain uncertainty.

Presence-aware pageInfo, continuation signatures, client/collection fences,
short/overlapping pages, page/record/identity/cursor bounds and mutable multi-page
coverage were inspected. Included media pagination fixtures and an independent
request traversal prove bounded read-only continuation and partial multi-page
coverage. Independent probes also show different connection identities remain
separate and cross-client cursors are refused. Interrupted media traversal retains
its observed item and non-complete coverage with the explicit returned error.
No immutable native snapshot or complete cross-service catalog is claimed.

## Independent checks

Module commands used `GOWORK=off`; generation used `GOPROXY=off GOSUMDB=off`.
Fixtures were synthetic; no live service, media inventory or private coordinates
were used.

- Seerr `go test -mod=readonly -race -count=3 -timeout=180s ./...`: exit 0.
- Seerr `go vet -mod=readonly ./...`, `go mod verify`: exit 0; all modules verified.
- Offline `./check-generation.sh`: exit 0.
- Offline `go generate ./...` in exact-product archive plus generated byte comparison:
  exit 0; committed output reproduced unchanged.
- Pinned Vacuum through tools, repository ruleset, `--remote=false`
  `--fail-severity=warn`: exit 0; quality 100/100, zero warnings/errors.
- Pinned golangci-lint through tools, repository configuration: exit 0; zero issues.
- Linux amd64/arm64 `CGO_ENABLED=0 go build -mod=readonly ./...`: exit 0 each.
- Independent expected-safety probes for R1-R3, `-race -count=3`: exit 1; all
  reported behaviors reproduced each run, without race-detector findings.
- Independent request traversal/instance/cursor and interrupted catalog controls,
  `-race -count=3`: exit 0.
- `./scripts/check-guardrails.sh --ci`: exit 0; existing generation/API/Vacuum,
  architecture, lint, root/UI/tools/qBittorrent/NZBGet tests/vet/verification pass.
- `python3 scripts/check_planning.py`: exit 0; 53 tasks, 60 acceptance cases and links.
- `git diff --check`: exit 0 before receipt.

## Separate gates and next action

Aggregate scripts/CI still omit Seerr. Direct module checks provide scoped evidence;
C-06 matrix/bootstrap, consumable module release and X-22 root migration remain
open. Native product fixtures, authentication/version gates and topology/mapping
verification are separate from synthetic local success. A-08/A-09/A-45/A-54
end-to-end closure is not claimed. G-01 remains open; no Arr writes are enabled.

Correct R1-R3 with executable regressions, produce exact product/handoff commits,
and request independent re-review. Only this receipt is committed. Exact review
SHA is reported after commit; state/product paths remain unchanged.
