# X-18 independent review, round two

Decision: **changes_requested**. Original R1 and deadline/body-cancellation
reproductions pass. Two correction defects remain: valid zero-valued manager
identity is lost, and wrapped context errors bypass sanitization.

## Exact identity and scope

- Reviewer: `/root/x05_reviewer`; independent of correction authorship.
- Exact product: `3dc53eee75cf296662a60b7422ec557692cdaee8`.
- Product tree: `fe4e5210687a3225a2fa19cc2946b10dda69243b`.
- Exact handoff: `460b5d7d45a2ab837a9e7c2f66cdee1aa153487c`, path
  `docs/execution/handoffs/X-18-correction-round1.md`.
- Handoff tree: `4ec0e4e3c836179e584a9de32c5c10101e75acff`; direct parent is
  the product above.
- Previous product: `67b01ca1d5555bfe11b348a96bda0b6a67d16bc3`.
- Previous receipt: `d51e7fcedd730a6f58bdcce99e9fc926baaee5a5`.
- Scoped binary correction diff SHA-256:
  `3943692e585c5d8ce944876e2853b16583a4471687cb4ed2361c2f8504f2b245`.
- Scope: `clients/seerr/`, correction handoff, R1-R3 and X-18's contribution
  to A-08/A-09/A-45/A-54 against the standalone connector policy.
- Source, contract, generated boundary, README and correction fixtures were
  inspected directly. Review used a clean detached checkout at the exact handoff.
  Independent probes/write generation ran in an archive of the exact product.
  Only this receipt is committed; product/state and unrelated paths are untouched.

## Remaining findings

### R3a — P1: valid first-manager identity zero is discarded as an unbound sentinel

Locations: `clients/seerr/client.go:1279`-`:1288`, `:1519`-`:1521`,
`TestServiceRelationshipsRejectInvalidEvidence` zero-sentinel expectation,
and correction handoff's zero-sentinel claim.
Contract: A-09 manager identity preservation, connector service relationships,
and OpenAPI `serviceId`, `serviceId4k` and service-error `id` minima of zero.

Seerr manager ID zero is a valid configured server identity. Primary source was
checked directly at immutable commit `6fa7473dbb3bc0a44fd47748435f53a3480cc4da`:
[Radarr settings](https://github.com/seerr-team/seerr/blob/6fa7473dbb3bc0a44fd47748435f53a3480cc4da/server/routes/settings/radarr.ts#L19)
assign the first configured server ID zero;
[Sonarr settings](https://github.com/seerr-team/seerr/blob/6fa7473dbb3bc0a44fd47748435f53a3480cc4da/server/routes/settings/sonarr.ts)
use the same allocation. The
[Media entity](https://github.com/seerr-team/seerr/blob/6fa7473dbb3bc0a44fd47748435f53a3480cc4da/server/entity/Media.ts#L179)
represents missing service identity with nullable fields. This source evidence
supports the identity model; it does not close a runtime product-version fixture.

The correction rejects any service identity equal to zero and emits `unbound`.
It also marks serviceErrors `id: 0` as unknown. This erases valid first-manager
relationships and prevents failures from retaining their exact configured manager
identity. Partial coverage is honest about the resulting omission but cannot
restore the source relationship the client incorrectly discarded.

Independent `TestReviewerNativeZeroServiceIdentityIsKnown` returns a valid media
page containing `serviceId: 0`, `externalServiceId: 7`, and a valid Radarr slug.
The relationship disappears, with `service_relationship_unbound` evidence.
Its second fixture returns `serviceErrors.radarr: [{id: 0, name: first-radarr}]`;
that record is retained with `IDKnown: false`. Both expected-identity assertions
fail three times under `-race`.

Required correction: separate Seerr configured manager IDs from native Arr title
IDs and their absence/sentinel rules. Preserve manager zero as a known identity
in relationships and serviceErrors, including 4K and nested-request paths. Retain
missing/null as unknown and validate unsupported negative identities. Document any
external-title sentinel with primary evidence rather than sharing an assumed
positive-only rule. Replace the misleading zero-sentinel regression and add movie,
TV, 4K, nested, same-zero-across-connections/kinds and zero-error identity controls.

### R2a — P2: wrapped context errors escape the sanitized boundary

Locations: `clients/seerr/client.go:1585`-`:1586`, `:1599`-`:1600`.
Contract: X-18 typed sanitized errors, `UpstreamError` no-endpoint/credential/body
promise, README credential/error sanitization and standalone transport policy.

The new `errors.Is` branches return raw transport/body errors when the request
context itself is still active. Preserving context identity does not require
preserving arbitrary surrounding error text. Standard HTTP wrapping includes the
request URL; custom transports/readers may include credential or upstream details.
The client must keep those details inside the transport boundary.

Independent `TestReviewerWrappedContextErrorsRemainSanitized` uses an active
context and an error joining a synthetic detail marker with `context.Canceled`.
Both transport and body branches satisfy `errors.Is`, but expose the detail marker.
The transport branch additionally exposes the full synthetic request URL through
`http.Client.Do`'s URL error. The body branch exposes the raw reader detail text.
Both privacy assertions fail three times under `-race`. No real secret or endpoint
was used; this is an executable raw-error disclosure, not a live credential incident.

Required correction: return canonical context errors, or a sanitized typed wrapper
that implements context identity without retaining/exposing arbitrary original
text. Cover canceled and deadline-exceeded errors from active-context transport
and body branches, nested wrappers, shorter HTTP-client timeouts, and ordinary
caller cancellation. Assert both `errors.Is` and absence of endpoint/credential/
body detail. Keep non-context errors sanitized as before.

## Original fixes verified and scope retained

- R1 is closed: native missing/UNKNOWN/future status retains numeric presence while
  availability stays unknown. Recognized statuses retain their separate semantics.
  Included all-enum and nested tests pass; independent UNKNOWN/future probes pass.
- Original R2 request-bound and body-cancellation probes pass. A child timeout now
  keeps configured timeout under longer caller deadlines, and shorter caller
  deadlines remain authoritative. Included header/body/cancellation/deadline tests
  pass. Raw wrapped-error sanitization remains R2a.
- Original invalid service ID/NUL slug and malformed service-error kind probes pass.
  Invalid relationships retain explicit uncertainty; malformed failure records/kinds
  and collection limits are refused. The zero-identity regression remains R3a.
- Paginated media/request reads remain read-only and bounded. Independent request
  traversal, instance/cursor fences and interrupted media controls pass; mutable
  multi-page coverage stays partial and prior observations survive interruption.
- OpenAPI remains 3.1.1 and generated output is unchanged/reproducible. The generated
  package is module-internal; no raw generated types escape public observations.
  No root/other-client imports, permanent replace, writes, search or hidden discovery
  methods are added. Synthetic auth header, endpoint, redirect, strict JSON/UTF-8
  and bounded decoding coverage remains intact. No native runtime auth guarantee
  or complete immutable catalog is claimed.

## Independent checks

Module commands used `GOWORK=off`; generation used `GOPROXY=off GOSUMDB=off`.
All fixtures and transports were synthetic.

- Seerr `go test -mod=readonly -race -count=3 -timeout=180s ./...`: exit 0.
- Seerr `go vet -mod=readonly ./...` and `go mod verify`: exit 0; all modules verified.
- Offline `./check-generation.sh`: exit 0.
- Offline `go generate ./...` in exact-product archive plus generated byte comparison:
  exit 0; committed generated output reproduced unchanged.
- Pinned Vacuum through tools, repository ruleset, `--remote=false` and
  `--fail-severity=warn`: exit 0; quality 100/100, zero warnings/errors.
- Pinned golangci-lint through tools, repository configuration: exit 0; zero issues.
- Linux amd64/arm64 `CGO_ENABLED=0 go build -mod=readonly ./...`: exit 0 each.
- Independent original expected-safety probes plus interruption/request/instance/cursor
  controls, `-race -count=3`: exit 0.
- Independent R3a identity and R2a wrapped-context privacy probes, `-race -count=3`:
  exit 1; every reported behavior reproduced each run, without detector races.
- `./scripts/check-guardrails.sh --ci`: exit 0; existing generation/API/Vacuum,
  architecture, lint, root/UI/tools/qBittorrent/NZBGet tests/vet/verification pass.
- `python3 scripts/check_planning.py`: exit 0; 53 tasks, 60 acceptance cases and links.
- `git diff --check`: exit 0 before receipt.

## Separate gates and next action

C-06 matrix/bootstrap, consumable module release, X-22 migration, pinned native
version/auth fixtures and deployment topology/mappings remain open. Aggregate CI
still omits Seerr; direct module checks provide scoped evidence. End-to-end
A-08/A-09/A-45/A-54 closure is not claimed. G-01 remains open; no Arr writes are
enabled. Local success is separate from publication, deployment and live behavior.

Correct R3a/R2a, produce exact product/handoff commits, then request independent
re-review. Only this receipt is committed; exact SHA is reported after commit.
Product/state paths remain unchanged.
