# X-13 independent review, round one

## Decision

`changes_requested`.

The standalone NZBGet client is integrated through a narrow read-only boundary,
and the inventory, correlation, descriptor, mapping, pagination, module, and
architecture checks pass. Two error-translation regressions remain in the
reviewed product: HTTP 501 loses the root adapter's nonretryable `unsupported`
classification, and a valid JSON-RPC error code of zero loses its upstream
identifier. The first can turn an unsupported operation into outage retries;
the second discards exact upstream evidence.

## Review identity and scope

- Reviewer: `/root/f01_reviewer`, independent of product author
  `/root/f01_implementer`.
- Reviewed product commit:
  `28a86ca9b7ca31e9adc2c14eaa356ed33ac0ee4c`; direct parent
  `58dfe1eb7881b74d5c03baef91034e0738b457b7`; product tree
  `74649a91513419914836996b8c3f33cf502b74aa`.
- Reviewed handoff commit:
  `a5f7e51570850535bcaf63fa805b623a309c3bb9`; direct child of the
  product; tree `3e8218e7ba989bb9ef5ec4b272a331440e660742`.
- Coordinator dependency commit:
  `522cf5bd1ea52e20754ea52d1458947b1eef1603`; direct child of the
  handoff; tree `b3923bb2599c336aaf3a5e24a86ff997e4453b2d`.
- Coordinator state checkpoint:
  `1568a7a8fcebeb53058f92231cf76fbce9f46cd0`; direct child of the
  dependency commit; tree `68bda8aa42e56c7b5983ea32da92860cb26065f4`.
- Product scope: `internal/adapters/nzbget/` and
  `tests/fixtures/nzbget/`; the product changes only
  `inventory.go` and `inventory_test.go` in the owned adapter package.
- Scoped product diff SHA-256:
  `ad7a7dad6b52f96538d6b6547da589ec1bf71877e0b7e261525b5217ac77066b`.
- Acceptance reviewed: A-05, A-06, and A-09.

Executable review ran in the clean detached checkout
`/Users/guilhermecastro/.codex/worktrees/managerr-f01-review-x13-r1` at
the exact state checkpoint. The checkpoint is a linear descendant of the
product and changes no X-13 product byte after the product commit. Disposable
adversarial tests ran only in external `/tmp` archives of the exact product
state and dispatch base. No live NZBGet service, credentials, private endpoint,
media, upstream write, release, or deployment was used.

## Findings

### P1: HTTP 501 is translated as retryable outage instead of unsupported

The root adapter's established normalized contract classifies HTTP 501 Not
Implemented as `domain.OutcomeUnsupported` with `Retryable=false`. The dispatch
base does this explicitly in `normalizeStatus`, alongside 404 and 405.

The migrated path changes that result:

1. The pinned standalone client classifies every otherwise unhandled 5xx
   status, including 501, as `nzbget.ErrorUnavailable` and retryable.
2. `normalizeClientError` in
   `internal/adapters/nzbget/inventory/inventory.go:612-613` maps that kind
   directly to `domain.OutcomeUnavailable` and retryable without considering
   `source.StatusCode`.
3. The adapter still lists HTTP 501 in the `OutcomeUnsupported` branch at
   `inventory.go:639-640`, but that branch is unreachable for 501 because it
   runs only for `nzbget.ErrorHTTP`.

Reproduction against the exact reviewed state used a synthetic `httptest`
server and called the exported `Client.Version` boundary. The status matrix
passed for 400, 401, 403, 404, 405, 408, 409, 429, 500, and 502, then failed:

```text
status 501 = code "unavailable" retry=true, want "unsupported"/false
```

The same test against dispatch base `58dfe1e` passed with
`OutcomeUnsupported` and `Retryable=false`, proving a migration regression.
An upstream that does not implement the endpoint can now enter retry/backoff
behavior as if temporarily offline, while capability and operator evidence
report the wrong cause.

Required correction: make adapter translation preserve HTTP 501 as
nonretryable `OutcomeUnsupported` before the broad standalone
`ErrorUnavailable` mapping, and add a deterministic exported-boundary
regression. Preserve 408 and ordinary 5xx as retryable unavailable, 429 as
rate-limited, and 404/405 as unsupported.

### P2: JSON-RPC error code zero loses its upstream identifier

The dispatch-base adapter records every JSON-RPC integer error code in
`domain.UpstreamError.UpstreamID`, including `"0"`. The migrated
`mapClientError` copies `source.RPCCode` only when it is nonzero at
`internal/adapters/nzbget/inventory/inventory.go:593-595`.

JSON-RPC requires an integer code and does not exclude zero. The pinned client
correctly decodes a zero code and classifies the envelope as `ErrorRemote`, but
the adapter treats the numeric zero as if the field were absent. The external
probe failed:

```text
RPC code 0 translated upstream ID ""
```

The same probe against dispatch base `58dfe1e` passed with `UpstreamID="0"`.
Negative standard/server codes such as `-32601` and `-32000` remain preserved,
and the synthetic upstream message stays redacted.

Required correction: retain `"0"` for a remote JSON-RPC error while still
leaving `UpstreamID` empty for transport/HTTP/config failures that have no RPC
code. A kind-aware adapter rule is sufficient because the public client uses
`ErrorRemote` for an otherwise unclassified RPC error. Add zero and nonzero
regressions at the exported root adapter boundary.

## Preserved behavior

- The root adapter imports the public standalone package directly and exposes
  no standalone generated or wire type through its exported API. The public
  client remains a private field; raw `json.RawMessage` is private to the
  tolerant queue/history decoder.
- `listgroups([0])`, `history([false])`, and `version([])` are the only emitted
  calls. The standalone allowlist rejects `rpc.discover`, mutations, and unknown
  methods before network dispatch.
- Queue/history merging keeps NZBID and deprecated ID as one conservative
  identity. Exact case-sensitive `drone` presence, single-value cardinality,
  malformed/duplicate evidence, queue/history conflicts, and numeric fallback
  remain covered.
- Queue, post-processing, and history states preserve readiness only after
  processing completes. FinalDir replaces DestDir fallback when later history
  proves it; unsafe, ambiguous, and unmapped paths never produce a
  `domain.FileTarget`.
- Full-array limits, HMAC cursors, cumulative observed counts, snapshot-change
  detection, reason bounds, partial coverage, and connection-scoped identity
  remain intact.
- Retained `.nzb` lookup remains optional and bounded. Available descriptors
  carry the exact SHA-256 digest and metadata without returning bytes;
  unavailable and malformed descriptors remain honest and partial.
- Basic authentication, same-origin redirect policy, response bounds,
  deadlines, cancellation sentinels, and sanitized error detail remain owned by
  the standalone client. External probes could not reach a standalone
  `UpstreamError` through the translated error chain or recover synthetic
  response/RPC secrets.
- No adapter mutation method, local replacement, `go.work`, real fixture, or
  private runtime coordinate was introduced.

## Dependency evidence

The root dependency commit pins
`github.com/guilycst/mastarr/clients/nzbget` at
`v0.0.0-20260911223922-1b9b3f9c425e`. `go mod download -json` reports origin
hash `1b9b3f9c425e56a7a3be6dfb43829157d08877c9`, the independently approved
X-11 product. All module source and manifest bytes match the committed
`clients/nzbget` subtree; only the repository `LICENSE` added by Go module
packaging differs. The module checksum is
`h1:WT/60UNd1nRkOStNkcx3j/QvK0TMvCd3c56ltsZGm1c=`.

The same coordinator commit also pins the future qBittorrent migration. At the
exact X-13 checkpoint that requirement is not yet imported, so root
`go mod tidy -diff` proposes removing only the qBittorrent requirement and its
checksums. This is an integration-sequencing limitation outside the X-13-owned
adapter paths. Root tests, race, vet, and module verification pass with both
published pins. UI tidy likewise reports its pre-existing future UI dependency
pins; UI tests, race, vet, and module verification pass.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact commit/tree/ancestry, scoped diff/name/status checks | Passed. Product changed only the two X-13-owned adapter files; detached checkout was clean before receipt creation. |
| Published module origin, checksum, source-tree comparison, no replace/go.work scan | Passed. NZBGet resolves to approved X-11 commit `1b9b3f9`; no local replacement or workspace file exists. |
| `GOWORK=off go test -mod=readonly -count=20 ./internal/adapters/nzbget/inventory` | Passed. |
| Focused adapter race `-count=5`, vet, and coverage | Passed; 80.4% statement coverage. |
| External queue/history/status/RPC translation probes | Queue/history and all ordinary mappings passed. HTTP 501 and RPC code zero failed exactly as recorded above; both baseline probes passed at `58dfe1e`. |
| Root `go test -count=1 ./...`, race with 420-second timeout, vet, and `go mod verify` | Passed; storage race completed in 200.484 seconds. Root tidy reports only the not-yet-consumed qBittorrent pin described above. |
| NZBGet module tests `-count=10`, race `-count=3`, vet, verify, and tidy diff | Passed cleanly. |
| qBittorrent module tests/race/vet/verify/tidy | Passed cleanly. |
| Tools module tests/race/vet/verify/tidy | Passed cleanly. |
| UI module tests/race/vet/verify | Passed. UI tidy reports pre-existing future UI pins. |
| `GOWORK=off ./scripts/generate.sh --check` and clean post-generation status | Passed; root, UI, qBittorrent, and NZBGet generation checks were deterministic. |
| Full lint, architecture-only lint, Python architecture checks, normal/self-test planning checks | Passed; zero lint issues, 43 tasks and 60 acceptance cases. |
| Fast/full guardrail entrypoints, API/Vacuum, workflow YAML, actionlint, and `bash -n` | Passed; Vacuum quality 100/100. `shellcheck` was unavailable and was not recorded as a pass. |
| Root adapter and standalone NZBGet test-binary cross-builds with `CGO_ENABLED=0` | Passed for Linux amd64/arm64, Windows amd64, Darwin arm64, and AIX ppc64. |
| Exported API, method allowlist, generated-type/import, mutation, fixture, and public-secret scans | Passed; fixtures are synthetic and no generated DTO crosses the root adapter boundary. |

## Acceptance disposition

- A-05: queue/post-processing/history correlation, exact positional requests,
  readiness, alias/drone semantics, and mapped final paths pass. X-13 remains
  unaccepted until its required typed error translation is corrected.
- A-06: descriptor availability, exact digest, response bounds, redaction, and
  no-plaintext behavior pass for the X-13 contribution.
- A-09: connection-scoped identities, endpoint/credential isolation, request
  binding, and per-client cursors pass for the X-13 contribution.

No product file, fixture, module manifest, task definition, shared script, or
`docs/execution/state.json` was modified by the reviewer.
