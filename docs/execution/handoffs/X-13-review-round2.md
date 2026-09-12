# X-13 independent review, round two

## Decision

`approved`.

Product commit `f54ec97c70d75a58099b92e7ee8350220599d009` closes both
round-one findings. HTTP 501 now crosses the standalone-client boundary as
nonretryable `domain.OutcomeUnsupported`, and a valid JSON-RPC error code zero
is retained as `UpstreamID="0"` without fabricating an ID for transport or HTTP
failures. The broader status matrix, sanitized error chain, inventory behavior,
and module boundaries remain intact. No open X-13 finding remains.

## Review identity and scope

- Reviewer: `/root/f01_reviewer`, independent of product author
  `/root/f01_implementer`.
- Reviewed correction product:
  `f54ec97c70d75a58099b92e7ee8350220599d009`; direct parent
  `4f17bb2d65d7780129b6920af93452f119e85888`; product tree
  `952e663e6badb81894df1a01a172fae418b80df9`.
- Reviewed correction handoff:
  `6c0da1e81276a7377a93bd743494e92d51cf86d7`; direct child of the
  product; tree `663d625bcf0689bb1d4429d5ddd9227da0243c68`.
- Coordinator state checkpoint:
  `28d41ed99257253b9ce8d06f7ddf19c352c9603c`; direct child of the
  handoff; tree `66977245a5ccaf8ba65952024b7c5b6f93168760`.
- Original product: `28a86ca9b7ca31e9adc2c14eaa356ed33ac0ee4c`.
- Prior independent receipt integrated as `66c0d0b`; the reviewer-authored
  receipt commit was `3e94d1f`.
- Correction scope: `internal/adapters/nzbget/inventory/inventory.go` and
  `inventory_test.go`. No other product path changed.
- Scoped correction diff SHA-256:
  `b77ecfc8c883533e6c5321104a4c87b3b42b98d45cb5911c715c75d793cc3e18`.
- Acceptance reviewed: A-05, A-06, and A-09.

Executable checks ran in clean detached checkout
`/Users/guilhermecastro/.codex/worktrees/managerr-f01-review-x13-r2` at
the exact state checkpoint. Disposable adversarial tests ran only in an
external `/tmp` archive of that checkpoint. No live NZBGet, credentials,
private endpoint, media, upstream mutation, release, or deployment was used.

## Round-one finding closure

### HTTP 501 classification: closed

The standalone client reports HTTP 501 as `ErrorUnavailable` with
`StatusCode=501`. `normalizeClientError` now checks that exact status before its
broad unavailable mapping and returns `OutcomeUnsupported`,
`Retryable=false`, with sanitized detail.

The committed exported-boundary regression passes. The independent matrix
also exercised 400, 401, 403, 404, 405, 408, 409, 429, 500, 501, and 502 over
`Client.Version` for 20 repetitions and under race detection for 10
repetitions. The exact expected classifications pass: 501, 404, and 405 are
unsupported; 408 and ordinary 5xx are retryable unavailable; 429 is retryable
rate-limited; 409 is conflict; 400 is invalid input; and 401/403 are
unauthorized.

The probe additionally verifies that no standalone `UpstreamError` or
synthetic response body is reachable through the translated error chain.

### JSON-RPC code zero identity: closed

`mapClientError` now records the RPC code when it is nonzero or the standalone
kind is `ErrorRemote`. The public client uses `ErrorRemote` for an otherwise
unclassified valid JSON-RPC error, so an explicit code zero becomes
`UpstreamID="0"`. Transport and HTTP failures keep an empty upstream ID.

The committed regression passes through the public root adapter. Independent
probes cover code `0`, standard code `-32601`, and server code `-32000`; every
integer is preserved exactly while the synthetic upstream message remains
absent from the error string and normalized detail.

## Preserved X-13 behavior

- The root adapter uses the published standalone NZBGet client and exposes no
  client-generated or wire type in its API. Raw JSON remains private to the
  item-tolerant queue/history decoder.
- Only `version`, `listgroups([0])`, and `history([false])` reads are emitted.
  No discovery extension, queue edit, append, pause/resume, delete, or retrying
  mutation exists.
- NZBID/deprecated-ID alias rules, exact `drone` cardinality, positional
  correlation, partial item evidence, post-processing readiness, FinalDir and
  DestDir mapping, connection scoping, and HMAC pagination remain covered.
- Retained descriptor lookup remains optional and bounded. Exact digests are
  reported without descriptor bytes; missing, malformed, and unconfigured
  descriptors remain honest partial evidence.
- Authentication, redirect isolation, request IDs, response bounds, deadlines,
  cancellation sentinels, and secret redaction remain delegated to the
  independently approved standalone client.
- The published module resolves to
  `v0.0.0-20260911223922-1b9b3f9c425e`, origin commit
  `1b9b3f9c425e56a7a3be6dfb43829157d08877c9`, with checksum
  `h1:WT/60UNd1nRkOStNkcx3j/QvK0TMvCd3c56ltsZGm1c=`. Its source matches the
  repository client subtree, excluding the repository license added during
  module packaging.
- No local `replace`, `go.work`, private fixture, credential, host path, or
  generated-type leakage was found.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/handoff/state identity, ancestry, owned diff, format, and clean-status checks | Passed; correction changes only the two X-13 adapter files. |
| Committed `TestStandaloneErrorClassificationTranslation` | Passed. |
| Independent full HTTP status and RPC-code matrix, `-count=20` | Passed. |
| Same independent matrix under `-race -count=10` | Passed. |
| Focused adapter tests `-count=25`, race `-count=10`, vet, and coverage | Passed; 80.5% statement coverage. |
| Root tests, race, vet, module verification, and tidy diff | Passed cleanly; storage race completed in 201.598 seconds. |
| NZBGet client tests `-count=10`, race `-count=3`, vet, verify, and tidy diff | Passed cleanly. |
| qBittorrent client tests `-count=3`, race, vet, verify, and tidy diff | Passed cleanly. |
| Tools module tests, race, vet, verify, and tidy diff | Passed cleanly. |
| UI module tests, race, vet, and module verification | Passed. UI tidy continues to report pre-existing future UI dependency pins outside X-13 scope. |
| Generation check and clean post-generation tree | Passed for root, UI, qBittorrent, and NZBGet outputs. |
| Full lint, architecture lint/checks, normal and self-test planning checks | Passed; zero lint issues, 43 tasks and 60 acceptance cases. |
| Fast/default guardrails and API/Vacuum | Passed; Vacuum quality 100/100. |
| Workflow YAML parse, actionlint, and `bash -n` | Passed. `shellcheck` was unavailable and is not recorded as a pass. |
| Root adapter and standalone NZBGet test-binary cross-builds with `CGO_ENABLED=0` | Passed for Linux amd64/arm64, Windows amd64, Darwin arm64, and AIX ppc64. |
| Published-module origin/source, no-replace/workspace, exported API, read-method, mutation, fixture, and public-secret scans | Passed. |

## Acceptance disposition

- A-05: accepted for X-13. Queue, post-processing, history, aliases, exact Arr
  correlation, readiness, and mapped final paths remain correct.
- A-06: accepted for X-13. Descriptor availability/digest and bounded,
  sanitized response behavior remain correct.
- A-09: accepted for X-13. Endpoint, credentials, request binding, identities,
  observations, and cursors remain instance-scoped.

No product file, fixture, module manifest, task definition, shared script, or
`docs/execution/state.json` was modified by the reviewer.
