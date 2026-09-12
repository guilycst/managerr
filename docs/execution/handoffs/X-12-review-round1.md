# X-12 independent review, round one

## Decision

`changes_requested`.

The adapter consumes the released standalone qBittorrent module, translates
its public DTOs and typed errors below the port boundary, preserves connection
scope and bounded pagination, and exposes no new control write. Built-in tests,
race checks, generation, module verification, lint, architecture, workflow and
Linux checks pass. Two related P1 findings remain at the compatibility
projection: ambiguous or malformed legacy evidence can bypass the standalone
decoder, and legacy field names are removed without checking the endpoint or
object in which they occur. Both failures can produce complete coverage from
an upstream response that the strict contract should reject.

## Review identity and boundary

- Reviewer: `/root/d01_reviewer`, independent of product author
  `/root/c01_implementer`.
- Assigned dispatch base:
  `58dfe1eb7881b74d5c03baef91034e0738b457b7`.
- Root standalone-client dependency pin:
  `522cf5bd1ea52e20754ea52d1458947b1eef1603`; tree
  `b3923bb2599c336aaf3a5e24a86ff997e4453b2d`.
- Initial product commit:
  `4c665d314d8d8e3ea31e54606be228829c595a25`; tree
  `2b98b7162413e341f202477c529120feb739c27c`.
- Final reviewed product commit:
  `cb0c5db5cb4460e9fb8dacc4027ab09ada6dfda3`; tree
  `83c186709b39187782e128fe4b02b875ff6988ad`.
- Reviewed handoff commit:
  `c8c1df60b4ba553721fecbc6e1eddcea0d1c806b`; it directly follows the final
  product commit.
- Coordinator state checkpoint:
  `886f265007f162eee7a9fbf4b6d45d09dea48e20`; it records X-12 as `in_review`,
  round one, with this reviewer and the exact product/handoff identities.
- Both the root dependency pin and initial product commit are ancestors of the
  final product commit. No local replacement or workspace file is involved.
- Review scope: `internal/adapters/qbittorrent/inventory/`,
  `tests/fixtures/qbittorrent/`, task X-12, A-04/A-09/A-28, the qBittorrent
  connector contract and the X-12 handoff.
- Scoped product diff SHA-256:
  `d8bf48d14d4f92cb0d25c82bdd4786d4da046ac5566323489e3f7ed8f3068911`.
- Scoped final-product archive SHA-256:
  `1e4e511b703f16aab6fcc7c2e95a26b9ba6138d4b305abae6cba95bf6b6d8606`.
- Product checks ran from a clean detached reviewer-owned worktree at the exact
  final product. Disposable adversarial tests were removed before this receipt
  was written.
- No live qBittorrent instance, credential, private hostname, tracker, torrent,
  media payload, upstream mutation, release or deployment was used.

## Findings

### P1: legacy response evidence bypasses duplicate and type validation

- Location: compatibility transport and projection at
  `internal/adapters/qbittorrent/inventory/inventory.go:185-235` and
  `:256-369`; raw identity/file recovery at `:1792-1857`; complete coverage is
  initialized at `:604-609`.
- Evidence: the transport captures the original response, removes every legacy
  field from the copy sent to the standalone strict decoder, and later decodes
  the original bytes with ordinary `encoding/json`. The rewriter does not track
  duplicate keys. The raw decoders also do not reject duplicates, so the last
  value wins. `mergeNativeSummaries` silently substitutes an empty identity
  slice when raw identity decoding fails, then returns a nonnil result. No
  reason code records that adapter-owned evidence was ambiguous or malformed.
- Independent identity reproduction:
  1. Start from synthetic `info-single.json`.
  2. Replace its one `infohash_v1` member with two members whose valid 40-hex
     values differ.
  3. Serve the response through the existing synthetic handler and call
     `ListDetailed(ctx, "qbt-main", "", 2)`.
- Observed identity result: the call succeeded, selected the second
  `infohash_v1`, returned one item, and reported `Completeness=complete` with no
  reason codes.
- Independent malformed-type reproduction: replace the same member with
  `"infohash_v1": 7`. The standalone decoder accepted the projected response;
  raw identity decoding failed and was silently discarded. The call still
  returned one item with empty `InfoHashV1` and complete coverage without a
  reason.
- Independent file reproduction:
  1. Start from synthetic `files-film.json`.
  2. Replace the first `"seeds": 4` with
     `"seeds": 4, "seeds": 999`.
  3. Return the normal inventory/properties fixtures and call `ListDetailed`.
- Observed file result: the first `FileObservation.Seeds` became `999`; the
  page remained complete with no reason codes.
- Failure mode: a proxy, incompatible upstream or corrupted response can choose
  a different supported descriptor identity or file-seed value solely by key
  order. A malformed legacy value can disappear while the application claims
  complete observation. Descriptor identity binding and review provenance can
  therefore use ambiguous evidence, and reconciliation can mistake missing
  legacy evidence for a trustworthy observation.
- Contract: `AGENTS.md` requires missing evidence to remain unknown;
  `docs/specs/spec-001-media-reconciliation/connectors.md:32-39` requires
  supported v1/v2 identities and real seeding evidence; X-12 must preserve the
  strict standalone boundary while translating retained adapter fields. The
  reviewed handoff specifically claims that duplicate members and malformed
  native responses still fail closed.
- Required change: validate the original compatibility envelope before or while
  projecting it. Detect duplicate semantic keys, including equal duplicates,
  validate the exact allowed type and value shape of each retained identity,
  metadata and seed member, and propagate a validation result rather than
  silently defaulting after raw decode failure. Inventory ambiguity must return
  typed malformed/unknown; per-file ambiguity may retain the item only with
  omitted untrusted file evidence and explicit partial coverage.
- Required proof: duplicate equal and unequal `infohash_v1`, `infohash_v2`,
  `has_metadata` and file `seeds` in both orders; escaped spellings that decode
  to the same key; string/number/bool/null/container type swaps; invalid hash
  lengths/hex and invalid seed ranges; raw row-count mismatch; valid absent
  optional evidence; and one valid legacy response. No malformed or ambiguous
  case may report complete coverage or publish a last-wins value.
- Disposition: `current_blocker`.

### P1: field-name-only projection hides unknown fields in the wrong response schema

- Location: `stripLegacyInventoryFields`, `rewriteInventoryJSON`, and
  `legacyInventoryField` at
  `internal/adapters/qbittorrent/inventory/inventory.go:256-369`.
- Evidence: one recursive allowlist is applied to both `/torrents/info` and
  `/torrents/files`. It drops `infohash_v1`, `infohash_v2`, `has_metadata` and
  `seeds` from every object at every depth. It does not know that identity and
  metadata extensions belong only to a torrent-info row and `seeds` belongs
  only to a file row. The standalone decoder never sees a misplaced member and
  cannot reject it as unknown.
- Independent torrent-info reproduction: add `"seeds": 999` beside the valid
  `num_seeds` in `info-single.json`. `ListDetailed` succeeded and reported one
  item with complete coverage and no reason codes.
- Independent file reproduction: add a valid-looking `infohash_v1` beside the
  first file's valid `seeds` member. The file response was accepted, all files
  were returned, and coverage remained complete with no reason codes.
- Failure mode: an arbitrary unsupported field is converted into an allowed
  extension merely because its name is used by the other endpoint. Future
  nesting or schema drift can likewise be erased. This weakens the frozen
  standalone schema and makes the handoff's statement that unknown native
  fields fail closed false.
- Contract: X-12 requires the standalone typed, strict read surface to remain
  the compatibility boundary. The connector contract requires unknown evidence
  instead of invented success, and the handoff permits only adapter-owned
  compatibility fields in their supported meaning.
- Required change: make projection endpoint- and structure-aware. Permit each
  extension only in its documented root array row for that endpoint; reject it
  in another endpoint, nested object, wrong top-level shape or duplicate
  position. Prefer one strict compatibility decoder that validates both native
  and retained fields, then produces the exact standalone payload and retained
  sidecar atomically.
- Required proof: `seeds` at torrent-info row and nested positions;
  identity/metadata fields at file-row and nested positions; extension fields
  at top level; unknown ordinary fields; duplicate extension fields; valid info
  and file extensions; and malformed/trailing/invalid-UTF-8 inputs. Every
  misplaced or unknown field must become sanitized malformed/unknown evidence,
  never complete coverage.
- Disposition: `current_blocker`.

## Verified implementation properties

- The root adapter imports the public standalone package
  `github.com/guilycst/mastarr/clients/qbittorrent`, not its generated package.
  Native `Torrent`, `TorrentProperties`, `TorrentFile` and `UpstreamError`
  values are translated inside unexported adapter functions. No generated or
  native DTO appears in a port signature, cursor, or exported observation.
- The root module pins
  `github.com/guilycst/mastarr/clients/qbittorrent`
  `v0.0.0-20260912004727-a9c847ff562f` with committed checksums. Root, UI,
  tools, qBittorrent and NZBGet modules contain no `replace` directive, and no
  `go.work`/`go.work.sum` exists.
- Native typed errors are mapped to `domain.UpstreamError` codes, status and
  retryability with a fixed sanitized detail. Context cancellation and deadline
  identity are preserved.
- Normal inventory reads use the standalone client's private authenticated
  session. Existing concurrent-instance tests show that equal hashes on two
  connections retain distinct scoped identities and cannot share SID cookies.
- Cursor signatures bind connection ID, runtime source ID, offset, page size,
  cumulative identities and fingerprints. Page overlap, duplicates, stalls,
  item/page ceilings and bounded final snapshot revalidation remain in the root
  adapter.
- Path mapping remains connection-scoped, longest-prefix and root-relative.
  Ambiguous, unmapped, duplicate and malformed paths retain observations with
  partial reason codes rather than creating actionable targets.
- The production adapter exposes the inventory and capability ports only.
  Standalone application/WebAPI, torrent list, property and file calls are
  reads; root descriptor export is a bounded GET. The only POST is session
  login. No stop, move, rename, tag, remove or payload mutation route exists in
  the reviewed scope.
- Descriptor export remains an adapter-only best-effort observation. It parses
  bounded bencode, hashes the exact raw `info` span, accepts supported v1/v2
  identity matches and exposes metadata/digest only. The compatibility findings
  above must be closed before the retained identity sidecar is trustworthy.
- The focused UTF-8 correction works for its asserted case: invalid UTF-8 is
  returned unchanged to the standalone decoder instead of being repaired by
  the compatibility rewriter.
- Updated fixture documents include the standalone contract's required members
  and nonnegative piece ranges. Fourteen normal JSON fixtures parsed; the two
  named malformed fixtures remained intentionally invalid. The torrent
  descriptor is synthetic and contains no announce URL.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product, tree, ancestry, dependency, handoff and state identities | Passed; identities are recorded above. |
| Three duplicate/type adversarial probes | Failed as described: duplicate identity last won, malformed identity disappeared, and duplicate file seeds became `999`; every page claimed complete. Probe removed. |
| Two wrong-schema adversarial probes | Failed as described: torrent-info `seeds` and file-row `infohash_v1` were hidden; both pages claimed complete. Probe removed. |
| `GOWORK=off go test -mod=readonly ./internal/adapters/qbittorrent/inventory -count=3` | Passed. |
| Focused adapter race test with `-count=3` | Passed. |
| Standalone qBittorrent tests and race tests with `-count=3` | Passed, including generated-package compilation. |
| `GOWORK=off go test ./... -count=1` at root | Passed. |
| `GOWORK=off go test -race ./... -count=1` at root | Passed; storage completed in 211.962 seconds. |
| Root `go vet ./...` and `go mod verify` | Passed; all modules verified. |
| UI and tools tests/race/vet/module verification | Passed. |
| Standalone qBittorrent and NZBGet tests/race/vet/module verification | Passed. |
| `./scripts/generate.sh --check` | Passed; staged generation and generated output are reproducible. |
| `./scripts/check-api.sh` | Passed on sequential rerun; Vacuum quality 100/100. An initial parallel invocation only contended for Git's temporary index lock with generation and made no product change. |
| `./scripts/check-lint.sh` | Passed with zero issues for all five modules; architecture boundaries passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py --self-test` | Passed: 43 tasks, 60 acceptance cases and local links. |
| `./scripts/check-guardrails.sh --ci` | Passed the full five-module aggregate. |
| `actionlint`, Ruby workflow YAML parsing and `sh -n` | Passed for the workflow, scripts and pre-commit hook. |
| CGO-free Linux amd64 and arm64 compile-only test for root, UI, tools, qBittorrent and NZBGet modules | Passed for all ten module/architecture combinations. |
| Root, tools, qBittorrent and NZBGet `go mod tidy -diff` | Clean. UI reported unchanged, unowned future UI dependencies as tidy drift; X-12 changes no UI or module file. |
| Scoped diff/archive identities, `gofmt -d`, `git diff --check` and clean product status | Passed before receipt creation. |
| Public secret/private-coordinate/path scan | Passed; matches were synthetic example endpoints and credential-shaped field names only. |
| HTTP method and route scan | Passed for product behavior: authentication POST plus read-only GET endpoints; no control mutation. |

## Acceptance contribution

- A-04: not accepted for X-12. Connection scoping and v1/v2 carriage work for
  valid responses, but duplicate or malformed retained identities can become
  last-wins or missing evidence under complete coverage and can influence
  descriptor binding.
- A-09: accepted for the scoped-identity and private-session portion. Equal
  upstream hashes remain distinct across connection IDs, cursors bind their
  connection, and native/generated types do not cross the root port boundary.
  This does not waive the false-complete findings.
- A-28: accepted for the read-only X-12 contribution. Torrent state/progress,
  seeding state and aggregate counters remain observations; zero rate is not
  converted to stopped, and the reviewed adapter adds no control write. The
  ambiguous per-file seed sidecar remains blocked by the first finding.

## Reviewer decision

`changes_requested`. X-12 must not integrate as approved at product commit
`cb0c5db5cb4460e9fb8dacc4027ab09ada6dfda3`. Correct both compatibility
validation findings, add the required adversarial regressions, update the
handoff's strictness claim to match tested behavior, and return the focused
correction for another independent review. Execution-state integration,
release, deployment and live qBittorrent compatibility remain separate
coordinator gates.
