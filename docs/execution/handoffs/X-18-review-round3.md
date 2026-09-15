# X-18 independent review, round three

Decision: **approved** for the scoped correction. R3a and R2a are closed by
implementation inspection and independent synthetic probes. No unresolved
blocking finding was found in this review. Integration and native-runtime gates
remain separate.

## Exact identity and scope

- Reviewer: `/root/x05_reviewer`; independent of product authorship.
- Exact product: `a0670e0a60b7b4920ff8a672bfa311d9b93a0ce1`.
- Product tree: `4753249f43d5dc564d69962ba22dec958f09bafb`.
- Exact handoff: `7f408d0908bf9e8e61e97dace70f620322e6c9d7`, path
  `docs/execution/handoffs/X-18-correction-round2.md`.
- Handoff tree: `48385061d780914a5c2ab16ca55ea4de5fded812`; direct parent is
  the product above.
- Coordinator dispatch checkpoint: `e7649a4706fc57e809a10836706aa74f7c40a7e6`.
- Previous product: `3dc53eee75cf296662a60b7422ec557692cdaee8`.
- Previous receipt: `90e6a2cdd327734d3b95df327c25655e46facbf7`.
- Scoped binary correction diff SHA-256, previous product to reviewed product:
  `474cfad9f48e2730261d224a14ca80ab48c5d54666e39b1b7de7051b6d591ce0`.
- Scope: `clients/seerr/`, correction handoff, R3a/R2a, original R1-R3 and
  X-18's contribution to A-08/A-09/A-45/A-54 under the standalone connector policy.
- Source, fixtures, contract, generated boundary and README were inspected directly
  in a clean detached checkout at the exact handoff. Independent probes and write
  generation used an archive of the exact product. Product, state and unrelated
  paths were not changed. Only this receipt is committed.

## Findings closed

### R3a: present native zero identity is preserved

`serviceRelationships` retains present nonnegative service IDs, including zero,
for base and 4K relationships. Nullable identity fields remain the absence signal;
missing pairs, negative values, unsupported media kinds and invalid slugs do not
produce normal relationships and retain explicit evidence. Nested request media
uses the same normalization. Configured connection and service-kind scope remain
separate from the native numeric identity.

`normalizeServiceErrors` now sets `IDKnown=true` for every present valid ID,
including zero. Missing/null ID with a bounded name remains explicitly unknown;
invalid IDs, kinds, text, malformed records and bounds remain refused.

The original independent zero-ID reproduction now passes: serviceId zero with
externalServiceId seven yields the exact relationship, and serviceErrors id zero
remains known. Expanded reviewer fixtures cover movie and TV, base and 4K,
nested request media and nullable absence. Product tests additionally cover the
same zero across configured connections and service kinds. All pass three times
under the race detector. Retaining a native external ID is evidence preservation;
it does not itself establish that a corresponding Arr title exists.

### R2a: wrapped context errors remain observable and sanitized

Transport and body branches now use `canonicalContextError`. Both own-context
failures and errors wrapping or joining a context sentinel return the canonical
sentinel, retaining `errors.Is` without exposing HTTP URL wrappers or arbitrary
transport/reader detail. Other transport and body failures remain normalized.

The original active-context joined cancellation reproduction now passes in both
branches. An independent deadline-exceeded fixture exercises both branches too;
all assertions require context identity and absence of synthetic URL/body/detail
markers. Included product fixtures cover canceled and deadline-exceeded joined
errors. All pass three times under the race detector.

## Prior safety and contract evidence retained

- Missing, UNKNOWN and future native statuses retain their numeric evidence while
  availability remains unknown. Recognized statuses keep their separate semantics.
  Independent UNKNOWN/future controls and included status coverage pass.
- Configured request timeout remains an upper bound under a longer caller deadline.
  Caller cancellation and body cancellation preserve context identity. Independent
  request-bound and body-cancellation controls pass.
- Invalid relationships retain explicit uncertainty; malformed service-error kinds
  and records cannot disappear into apparently complete evidence. Independent
  invalid-ID/slug/kind controls pass.
- Interrupted media traversal preserves observations and uncertainty. Multi-page
  request traversal, service errors, instance-scoped identities and foreign-cursor
  refusal remain covered by independent controls. Mutable offset pagination does
  not claim a complete immutable catalog.
- OpenAPI remains 3.1.1 with unchanged narrow read-only scope and generated output.
  Offline pinned-tools generation reproduces committed bytes. The generated package
  remains module-internal; no generated DTOs leak through public observations.
- No root or other-client imports, permanent local replaces, writes, search or
  hidden discovery methods were introduced. Existing synthetic API-key/bearer,
  endpoint, redirect, deadline, bounded JSON/UTF-8 and typed-error tests pass.

## Independent checks

Module commands used `GOWORK=off`; generation and pinned tools used
`GOPROXY=off GOSUMDB=off`. All fixtures were synthetic.

- Seerr `go test -mod=readonly -race -count=3 -timeout=180s ./...`: exit 0.
- Seerr `go vet -mod=readonly ./...` and `go mod verify`: exit 0; all modules verified.
- Offline `./check-generation.sh`: exit 0.
- Offline `go generate ./...` in the exact-product archive and generated byte
  comparison: exit 0; committed artifact reproduced unchanged.
- Pinned Vacuum through tools, repository ruleset, `--remote=false` and
  `--fail-severity=warn`: exit 0; quality 100/100, zero warnings/errors.
- Pinned golangci-lint through tools and repository configuration: exit 0; zero issues.
- Linux amd64/arm64 `CGO_ENABLED=0 go build -mod=readonly ./...`: exit 0 each.
- Independent original reproductions and expanded zero/base/4K/nested/null,
  wrapped canceled/deadline, availability, malformed service, timeout/body,
  interrupted traversal and request/instance/cursor controls,
  `go test -mod=readonly -run TestReviewer -race -v -count=3 -timeout=120s ./...`:
  exit 0; no race detector findings.
- `./scripts/check-guardrails.sh --ci`: exit 0; existing generation/API/Vacuum,
  architecture/import boundaries, lint, root/UI/tools/qBittorrent/NZBGet
  tests/vet/module verification pass.
- `python3 scripts/check_planning.py`: exit 0; 53 tasks, 60 acceptance cases and links.
- `git diff --check`: exit 0 before receipt.

## Separate gates

C-06 matrix/bootstrap, consumable nested-module release, X-22 root migration,
pinned native product-version/auth fixtures and deployment topology/mappings
remain open. Aggregate checks still omit Seerr; explicit direct module checks
above provide the scoped evidence. End-to-end A-08/A-09/A-45/A-54 closure and live
behavior are not claimed. G-01 stays open; this correction enables no Arr or Seerr
writes. Publication, deployment and runtime capability need their own evidence.

Only this review receipt is committed. The exact receipt commit SHA is reported
after commit; product/state paths remain unchanged.
