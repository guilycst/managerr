# X-12 independent review, round two

## Decision

`changes_requested`.

Both round-one P1 findings are closed. The compatibility parser now validates
retained identity, metadata and file-seed members before projection, rejects
duplicate semantic names and preserves misplaced fields for the standalone
strict decoder to reject. Expanded end-to-end adversarial probes produced
sanitized unknown inventory or item-level partial file evidence as required.

One P2 compatibility regression remains. The correction rejects the established
qBittorrent file-seed unknown sentinel `-1`, although the existing adapter
contract explicitly retains it as visible unknown evidence. A response with
otherwise valid files now drops every file and mapped payload entry as malformed.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of correction author
  `/root/c01_implementer`.
- Round-one product:
  `cb0c5db5cb4460e9fb8dacc4027ab09ada6dfda3`.
- Original round-one receipt:
  `255d63c7a1b979f0a9244d72d97105e84a020a75`; coordinator-integrated receipt
  `a8723268fda092d8efc9dda118bd6126f510247d` contains the same receipt bytes.
- Correction product:
  `c1ce2d2dd5202801f09cad6420af864574b6cfef`; tree
  `55d846c56d12f4928d23298c683a3d938314a66f`.
- Correction handoff:
  `e4ea4f6cc424ee151a9134f5cd365ae25a69a6e7`; it directly follows the product.
- Coordinator state checkpoint:
  `c556d18989c0a05650bcda53de737b10db4d44ea`; it records X-12 round two as
  `in_review` with the exact product, handoff and reviewer.
- The integrated round-one receipt and correction handoff are ancestors of the
  state checkpoint. The correction commit changes exactly
  `internal/adapters/qbittorrent/inventory/inventory.go` and its test; it does
  not change fixtures, contracts, modules, scripts or execution state.
- Review scope: X-12 owned adapter and fixture paths, both round-one P1s,
  A-04/A-09/A-28 and the correction handoff.
- Scoped correction diff SHA-256:
  `ab0a71fdc203f0e94ae83041874675a307501a9ff615e49168e13e146a1021db`.
- Scoped corrected-product archive SHA-256:
  `9404deb82847f2f7c6afa826eb8feb8a7af10878e47ef120f5a615e259647e3f`.
- Checks ran in a clean detached reviewer-owned worktree at the exact correction
  product. Temporary adversarial tests were deleted before receipt creation.
- No live qBittorrent instance, credential, private hostname, tracker, torrent,
  media payload, upstream mutation, release or deployment was used.

## Round-one findings rechecked

### Closed: retained compatibility fields are validated before projection

- Location: `projectLegacyInventoryFields`,
  `rewriteLegacyInventoryObject` and `validateLegacyInventoryValue` at
  `internal/adapters/qbittorrent/inventory/inventory.go:290-515`.
- The parser requires one top-level array of object rows. It tracks decoded
  legacy member names per row, so equal, unequal and escaped-name duplicates
  fail before removal.
- `infohash_v1` and `infohash_v2` accept only empty optional values or exact
  valid 40/64-character hashes without surrounding whitespace.
  `has_metadata` accepts only a JSON boolean. File `seeds` accepts a lexical
  integer in the configured machine range; the remaining `-1` issue is recorded
  separately below.
- A projection error leaves the bounded original response intact. The
  standalone strict decoder sees the legacy member and returns a sanitized
  malformed/unknown result instead of allowing raw sidecar decoding to choose
  a duplicate or silently discard a type error.
- Independent end-to-end probes confirmed sanitized `OutcomeUnknown` and no
  item for unequal, equal and escaped `infohash_v1` duplicates, number and array
  identity values, duplicate and object-valued `has_metadata`, and malformed
  `infohash_v2`.
- Independent file probes confirmed one retained torrent with no file/payload
  observations, partial coverage and `item_0_files_malformed` for unequal/equal
  duplicate seeds and string/object seed values. Zero and repeated valid seeds
  in separate file rows remained exact observations.
- Disposition: `resolved`.

### Closed: projection is endpoint- and row-aware

- Location: endpoint selection in
  `internal/adapters/qbittorrent/inventory/inventory.go:186-240` and structured
  projection at `:274-477`.
- `/torrents/info` removes only row-level `infohash_v1`, `infohash_v2` and
  `has_metadata`; `/torrents/files` removes only row-level `seeds`.
  Cross-endpoint names and every nested name remain in the projected JSON for
  strict unknown-member rejection.
- Independent end-to-end probes added row-level `seeds` and nested
  `infohash_v1` to torrent info. Both responses returned sanitized
  `OutcomeUnknown` without observations.
- Independent file probes added row-level `infohash_v1` and nested `seeds` to
  file responses. Both retained the torrent as partial, omitted malformed file
  evidence and reported `item_0_files_malformed`.
- Non-object rows, malformed/trailing JSON and invalid UTF-8 follow the same
  fail-closed path. Existing strict standalone tests continue to reject unknown
  ordinary fields and duplicate native members.
- Disposition: `resolved`.

## Finding

### P2: explicit unknown file-seed evidence is rejected instead of retained

- Location: seed validation at
  `internal/adapters/qbittorrent/inventory/inventory.go:503-510`; native-to-local
  file translation at `:1143-1156`; established sentinel handling at
  `:1184-1207`.
- Evidence: the correction treats every seed value below zero as a projection
  error. The same adapter initializes absent seed evidence to `-1`, considers
  only values below `-1` invalid, retains `Seeds=-1` in `FileObservation`, and
  adds `file_seeds_unknown`. The X-01 handoff freezes this exact behavior:
  `availability=-1` and `seeds=-1` remain visible unknown sentinels with partial
  reason evidence.
- Independent reproduction:
  1. Start with the synthetic `files-film.json` fixture.
  2. Change the first row from `"seeds": 4` to `"seeds": -1` and leave every
     native required member and the second row valid.
  3. Serve normal inventory, property and version fixtures and call
     `ListDetailed(ctx, "qbt-main", "", 2)`.
- Expected result: two safe file observations and mapped payload entries remain
  visible; the first has `Seeds=-1`; coverage is partial with
  `item_0_file_seeds_unknown`.
- Observed result: the item remained, but `Files` and `Payload` were both empty.
  Coverage was partial with only `item_0_files_malformed` for the file boundary.
- Failure mode: explicit unknown seed availability discards valid file names,
  paths, sizes, progress, roles and mappings for the whole torrent. Manual
  reconciliation loses the exact payload evidence needed for review even
  though only one optional seed count is unknown. An omitted `seeds` member
  still follows the established sentinel path, so explicit and absent unknown
  evidence behave inconsistently.
- Contract: X-12 must preserve current read-only adapter behavior;
  `docs/execution/handoffs/X-01.md` explicitly retains `seeds=-1`; `AGENTS.md`
  and the connector contract require missing evidence to remain unknown rather
  than erase independent observations. The correction handoff's nonnegative
  seed claim conflicts with that established behavior.
- Required change: allow the exact integer sentinel `-1` for the file-row
  compatibility field, retain its file observation, and continue rejecting
  values below `-1`, fractional/exponent/string/null/container values and
  overflow. Update the handoff to describe `-1` as unknown rather than invalid.
- Required proof: explicit `-1` in the first, later and multiple file rows;
  omitted seeds; zero and positive seeds; mixed known/unknown rows; values below
  `-1`; wrong JSON types and overflow. Valid file evidence must remain visible,
  with only the exact unknown-seed reason added for the sentinel.
- Disposition: `current_blocker`.

## Retained X-12 properties

- The root adapter imports the released public standalone qBittorrent package,
  not its generated package. Standalone DTOs and errors are translated inside
  unexported helpers; generated/native types do not appear in port signatures,
  cursors or exported observations.
- The pinned standalone module and checksums are unchanged. Root, UI, tools,
  qBittorrent and NZBGet modules have no local replacement, and no `go.work` or
  `go.work.sum` exists.
- Typed native errors map to sanitized domain codes, HTTP status and retryability.
  Cancellation and deadline identities remain intact.
- Connection-scoped item identities, private SID sessions, signed cursors,
  overlap/stall detection, bounded snapshot revalidation and conservative
  partial coverage remain unchanged.
- Component-aware path mapping remains root-relative and connection-scoped.
  Ambiguous, unmapped and invalid paths cannot become actionable targets.
- The adapter remains read-only. Its only POST is qBittorrent session login;
  inventory, version, properties, files and optional descriptor export use GET.
  No control or payload mutation exists in the reviewed scope.
- Descriptor bytes remain outside ordinary observations. Best-effort export is
  bounded and identity-bound and exposes metadata/digest only.
- Fourteen normal JSON fixtures parse, the two named malformed fixtures remain
  intentionally invalid, and the descriptor fixture has no announce URL.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/tree/handoff/state/round-one receipt identity and ancestry | Passed; identities are recorded above. |
| Seventeen-case compatibility closure matrix | Passed: duplicate/type/hash, escaped-name, wrong-context, nested and valid-value behavior matched the correction contract. Temporary probe removed. |
| Explicit `seeds=-1` end-to-end probe | Failed as described: all file/payload evidence was dropped as malformed instead of retaining the unknown sentinel. Temporary probe removed. |
| Focused adapter tests and race tests with `-count=3` | Passed. |
| Standalone qBittorrent tests and race tests with `-count=3` | Passed, including generated-package compilation. |
| `GOWORK=off go test -mod=readonly ./... -count=1` at root | Passed; storage completed in 15.503 seconds. |
| `GOWORK=off go test -mod=readonly -race ./... -count=1 -timeout=360s` at root | Passed; storage completed in 234.945 seconds. |
| Root tests/vet/module verification | Passed; all modules verified. |
| UI and tools tests/race/vet/module verification | Passed. |
| Standalone qBittorrent and NZBGet tests/race/vet/module verification | Passed. |
| `./scripts/generate.sh --check` | Passed; staged generation and generated output are reproducible. |
| `./scripts/check-api.sh` | Passed; Vacuum quality 100/100. |
| `./scripts/check-lint.sh` | Passed with zero issues for all five modules; architecture boundaries passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py --self-test` | Passed: 43 tasks, 60 acceptance cases and local links. |
| `./scripts/check-guardrails.sh --ci` | Passed the full five-module aggregate. |
| `actionlint`, Ruby workflow YAML parsing and `sh -n` | Passed for the workflow, scripts and pre-commit hook. |
| CGO-free Linux amd64 and arm64 compile-only tests for all five modules | Passed for all ten module/architecture combinations. |
| Scoped diff/archive identities, `gofmt -d`, `git diff --check` and clean product status | Passed before receipt creation. |
| Public secret/private-coordinate/path scan | Passed; only synthetic credential-shaped field names were present. |
| HTTP method, generated-import and local-replacement scans | Passed; login plus reads only, no generated leakage and no local replacement/workspace. |

## Acceptance contribution

- A-04: accepted for the round-two P1 correction. Valid retained v1/v2
  identities remain connection-scoped; malformed, duplicate and misplaced
  identities now fail closed instead of becoming current evidence.
- A-09: accepted for X-12 connection scoping, private sessions, cursor identity
  and port-boundary translation. Same upstream identifiers cannot collide
  across configured connections.
- A-28: accepted for the aggregate torrent-state and read-only contribution.
  Zero rate is not stopped and the adapter adds no control write. Exact per-file
  seed-unknown preservation remains blocked by the P2 above.

## Reviewer decision

`changes_requested`. The two requested round-one P1s are resolved, but X-12
must not integrate as approved at product
`c1ce2d2dd5202801f09cad6420af864574b6cfef` until explicit `seeds=-1` evidence
retains the established file observations and partial unknown reason. Apply the
small bounded correction and return it for independent recheck. Execution-state
integration, release, deployment and live compatibility remain separate
coordinator gates.
