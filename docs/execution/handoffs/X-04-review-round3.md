# X-04 independent review, round three

## Decision

`approved`.

The round-two P1 is closed. Item-level path-only evidence cannot become
playable with or without a mapping. A native Jellyfin source becomes playable
only after its ID, path, protocol, location, media type and item/source media
type relationship pass the defined local-video checks. Independent adversarial
tests found no remaining X-04 blocker.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of correction author
  `/root/d01_implementer`.
- Reviewed product commit:
  `b5594b668185245304f70a0f46c1f77ae7b76d7b`.
- Product parent:
  `aba918130e5b79b54f5b068a086b66b76f12bb54`.
- Product tree:
  `1420c2a76bf7ca9d70c3be0e9ad6174e30f2f157`.
- Reviewed handoff commit:
  `f1dfeafe50212bd0d763eb61e5d8c15fd9c4da6c`.
- Review input: round-two receipt
  `c07f13891f19ba40a49ed6d2415e8a19e78a6110`, frozen Jellyfin connector
  rules and A-08/A-09/A-54/A-55.
- Review scope: `internal/adapters/jellyfin/read/`, unchanged
  `internal/adapters/seerr/`, and `tests/fixtures/catalogs/`.
- The product changes exactly three assigned files:
  `internal/adapters/jellyfin/read/client.go`, its test, and the synthetic
  Jellyfin catalog fixture. It changes no contract, execution state, storage,
  Arr or Seerr file.
- Scoped X-04 diff SHA-256:
  `49a84ab9cac6bf7e9974f27732a5351d6ef7946cae6229753b21a326cf9bef1d`.
- Scoped X-04 archive SHA-256:
  `878833ddee0b646f014206bcea4b62fbb4784dad038cffb003accdfa2885ce02`.
- Product checks ran in a clean detached reviewer-owned worktree at the exact
  product commit. Independent adversarial tests ran in disposable Git archive
  `/tmp/managerr-x04-r3-review.qCAEsK` and were not added to the product.
- No live Jellyfin/Seerr service, credential, private inventory, media payload,
  upstream mutation, filesystem action, release or deployment was used.

## Round-two P1 rechecked

### Closed: playability requires verified native source evidence

- Location: `internal/adapters/jellyfin/read/client.go:764-901`.
- `observeItem` marks every synthesized item-level `Path` source with
  `media_source_path_only_unverified` and forces it unplayable. A successful
  mapping can preserve a bounded `MappedTarget` for correlation but cannot
  promote the synthetic record.
- `nativeMediaSourceReason` requires a nonblank source ID and path, a valid
  normalized absolute path, `Protocol: File`, `LocationType: FileSystem`, and
  a nonblank media type before mapping or no-mapping playability is considered.
- `nativeItemMediaTypeReason` requires a nonblank item media type, exact
  case-insensitive item/source agreement, and the supported `Video` type.
- Nonempty item locations outside `FileSystem` are rejected before source
  evaluation. Remote, virtual and offline items cannot inherit a valid nested
  source's playability.
- Wrong and ambiguous configured mappings remain unplayable. A valid native
  local-video source remains playable without a mapping as an upstream source
  observation; when mappings are configured, an unambiguous mapping is also
  required and retained separately.

Independent tests exercised the full decision boundary twice where relevant,
with and without a configured mapping:

- valid native mapped and no-mapping sources became playable;
- path-only mapped and no-mapping observations remained unplayable;
- remote, virtual, offline and HTTP source shapes remained unplayable;
- missing source ID, path, protocol, location or media type remained
  unplayable with the expected stable reason;
- relative/traversal paths remained invalid;
- missing item media type, item/source mismatch and matching non-video types
  remained unplayable;
- remote, virtual and offline item locations remained unplayable even with an
  otherwise valid source.

All adversarial assertions passed. This closes the exact reproductions in the
round-two receipt.

## Prior X-04 invariants preserved

- Unpinned Jellyfin and Seerr catalog capabilities remain `unknown`; a version
  observation does not promote unrelated routes.
- Multi-request Jellyfin and Seerr offset traversal retains
  `pagination_snapshot_unverified` and cannot certify complete absence.
- Equal or conflicting duplicate Jellyfin library identities remain partial
  with stable duplicate/conflict reasons.
- Both adapters issue GET only, refuse redirects and preserve connection-scoped
  identities. Seerr exposes no write. Jellyfin refresh remains typed
  unsupported.
- Seerr retains native status, provider and service evidence independently of
  Jellyfin availability.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/parent/tree/handoff and three-file owned diff | Inspected; identities above are exact. |
| Disposable playability decision-matrix probe | Passed all mapped, unmapped, missing, invalid, unsupported and mismatched cases. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed; storage completed in 14.488 seconds. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed; storage completed in 222.642 seconds. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| Root, UI and tools `GOWORK=off go mod verify` | Passed. |
| UI and tools tests/vet with `GOWORK=off -mod=readonly` | Passed. |
| `./scripts/generate.sh --check` | Passed. |
| `./scripts/check-api.sh` | Passed; Vacuum quality score 100/100. |
| `./scripts/check-lint.sh` | Passed with zero issues; architecture passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases; local links resolve. |
| `./scripts/check-guardrails.sh --fast` | Passed. |
| Linux amd64 and arm64 CGO-free focused compile | Passed. |
| Three catalog fixture JSON files | Parsed successfully. |
| Scoped diff/archive identity, `git diff --check`, commit whitespace check and clean status | Passed before receipt creation. |
| Public credential/private-path scan | Passed; matches were API field names, synthetic fixture keys and an adversarial traversal value. |
| HTTP method/write-surface scan | Passed; production calls remain GET-only. |

## Acceptance contribution

- A-08: accepted for X-04. Presence/path correlation and verified native
  playable-source evidence remain separate; unsupported shapes cannot create
  false availability.
- A-09: accepted for connection-scoped identities and collision resistance.
- A-54: accepted for read-only Seerr native status and honest multi-page
  coverage.
- A-55: accepted for X-04. Refresh remains safely unsupported, mapping
  confinement is enforced, and path-only evidence cannot become availability.

## Reviewer decision

`approved`. X-04 has no remaining reviewer finding at product commit
`b5594b668185245304f70a0f46c1f77ae7b76d7b`. This receipt approves the scoped
local implementation and tests; integration, release, deployment and live
service verification remain separate coordinator gates.
