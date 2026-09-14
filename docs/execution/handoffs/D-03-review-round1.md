# D-03 independent review, round one

## Decision

`changes_requested`.

Source ownership, strict one-document YAML decoding, bounded regular-file
reads, immutable snapshots, stale revision rejection, runtime tombstones, and
encrypted credential envelopes pass. Five current-scope failures remain:
credential-store uncertainty can resurrect an unrequested secret, secret-only
changes preserve approval bindings without target verification, mapping
validation accepts prefixes rejected by consumers, failed parsing retains
resolved plaintext, and invalidator reentry deadlocks.

## Review identity and scope

- Reviewer: `/root/d02_reviewer`, independent of product author
  `/root/d02_implementer`.
- Reviewed product: `3e86f117155753e7165f38e0f9290bdcc78bc322`;
  direct parent `231da2eea083fcc5914361478a960e5ef7009a38`; tree
  `d62529424cf5560f2a7cc73046da5d9e2a01f69f`.
- Reviewed handoff: `17e623cddbe307fa96879b5bdfc7aa56b8bfd711`;
  product ancestor; tree `014d12b82013df504d147271fa08d0da41f902d7`.
- Product scope: `internal/configuration/configuration.go`,
  `configuration_test.go`, `specs.go`, and `yaml.go`; no other path changed.
- Scoped product diff SHA-256:
  `81245a3b308ad4e05659f20f3b3e5a20f3993ddef353bd30b2be5556c12cae06`.
- Acceptance reviewed: A-38, A-39, and A-42.
- Release stage: pre-v0.1.0, target v0.0.1.

Executable review ran in clean reviewer-owned Git worktrees at the exact
product and dependency-fix commits. Disposable probes were untracked and
remain outside the product branch. No live service, credential, private
endpoint, media, upstream mutation, release, or deployment was used.

## Findings

### P1: ambiguous credential-store success can resurrect an unrequested secret

`CreateConnection` installs managed-field state, rebuilds the active snapshot,
then calls `ManagedCredentialStore.Replace` at
`internal/configuration/configuration.go:1146-1169`. When `Replace` atomically
commits the complete encrypted set but returns an uncertain error, the method
removes only in-memory state. It neither reads back the store nor prevents ID
reuse. `ResolveCredential` then loads any stored field at lines 1047-1075
without checking that the active connection declares that managed field.

A synthetic store committed the encrypted set and returned
`commit outcome unavailable`. Retrying the same connection creation without
credentials succeeded, after which resolution exposed the failed attempt's
secret:

```text
unrequested credential resurrected: value="ghost-secret" err=<nil>
```

`UpdateConnection` has the related unsafe recovery path at lines 1262-1269: on
a failed replacement it issues a second mutating replacement as rollback and
ignores that result, without first reconciling whether the first write
committed. This conflicts with the repository invariant that a lost write
response requires read-only reconciliation before another mutation.

Required correction: define and implement outcome-aware store semantics.
Read back and compare the complete encrypted field set after an uncertain
write; do not issue blind rollback. Keep a failed create inaccessible and
prevent retries from activating orphan fields. `ResolveCredential` and
`CredentialMetadata` must require the field in the active managed-field set.
Add commit-then-error and failed-rollback regressions.

### P1: secret-only changes preserve intent without target-identity verification

The contract permits secret-only rotation to preserve approved intent only
after successful target-identity verification. `UpdateConnection` marks only
endpoint changes as authority changes at lines 1247-1253 and exposes no target
verification seam. Existing tests explicitly expect credential rotation to
produce no invalidation.

Static credentials are weaker. `connectionChangedFields` compares only env/file
reference names at lines 824-826, while `connectionRevision` hashes those
references, not resolved values, at lines 1798-1805. Reapplying identical YAML
after the referenced secret changed updated the resolved plaintext but retained
both connection and effective revisions and emitted no callback:

```text
changed static credential retained every approval binding without target verification
```

Required correction: add a target-identity verification boundary for managed
and static secret rotation. Preserve intent only after verified identity. If
verification is unavailable, fails, or is uncertain, invalidate affected
unapproved plans and return approved undispatched work to review. Detect a
changed resolved static value with a non-secret opaque binding; never expose a
reversible digest of secret material. Add restart and explicit-apply tests.

### P1: effective configuration accepts unusable upstream mapping prefixes

`cleanSourcePrefix` and `cleanNamespace` at lines 2082-2116 accept empty and
ordinary relative paths. Every current mapping consumer requires an absolute
remote namespace: qBittorrent inventory/control, NZBGet, Arr, and Jellyfin all
reject such input during adapter construction. A YAML mapping with
`sourcePrefix: relative/downloads` passed `ParseYAML`:

```text
relative upstream source prefix accepted: <nil>
```

This violates D-03's complete mapping-validation deliverable and allows an
effective snapshot that cannot seed the read adapters.

Required correction: share one source-prefix contract across configuration and
all consumers. Accept only documented absolute POSIX or Windows remote forms,
reject empty/relative prefixes, and retain component-aware canonical/traversal
checks. Add YAML and API mutation regressions plus constructor compatibility
tests.

### P2: failed YAML parsing retains resolved plaintext

`yamlConnection.toDomain` uses named result `values` and a deferred zeroing
loop at `internal/configuration/yaml.go:140-149`. Every explicit error return
assigns `nil` to that named result before the defer runs. If one reference was
resolved and a later reference fails, the defer sees no map and leaves the
package-owned byte slice intact. A custom resolver proved the first resolved
buffer remained unchanged after the second lookup failed:

```text
resolved plaintext retained after parse failure: "failed-parse-plaintext"
```

`ParseYAML` also drops the current connection's successfully resolved values
without zeroing them when it detects a duplicate connection ID at lines
412-417.

Required correction: keep resolved values in a local map whose cleanup cannot
be overwritten by named return assignment, transfer ownership only on success,
and zero current values on duplicate rejection. Add both failure regressions.

### P2: invalidation callbacks run under the manager write lock and can deadlock

YAML activation and every authority-bearing API update call the injected
`Invalidator` while holding `manager.mu`. For example,
`UpdateConnection` locks at lines 1186-1187 and invokes the callback at lines
1248-1253. A valid callback that called `Snapshot` to inspect effective
configuration blocked on the same mutex; the focused test timed out with
`UpdateConnection` waiting inside `Snapshot`.

Credential-store calls have the same external-call-under-lock shape in
`ResolveCredential`, `CreateConnection`, and `UpdateConnection`. Besides
reentry deadlock, a slow or cancellation-insensitive implementation blocks all
snapshot reads and configuration writes.

Required correction: do not invoke injected callbacks or storage I/O while
holding the manager mutex. Snapshot the expected revision/state, perform the
external operation, reacquire the lock, and use compare-and-set validation
before activation. Keep the uncertainty reconciliation from finding one. Add
reentrant invalidator and concurrent stale-revision tests.

## Verified behavior

- Unknown YAML fields, multiple documents, duplicate resource IDs, unresolved
  references, invalid intervals, dangling mappings, API/YAML ID collisions,
  noncanonical root paths, traversal, and equal-specificity ambiguity fail
  before snapshot replacement.
- YAML resources return read-only errors through API mutation methods. API
  writes require exact revisions; stale and missing preconditions fail.
- Removed YAML connections and roots become runtime tombstones with retirement
  time. Removed mappings remain runtime history within the current domain
  shape. Cross-restart persistence and API transport remain C-04 integration
  evidence.
- Snapshot values and nested capability evidence are deep-copied. Focused race
  checks passed for current tests.
- Static refs resolve once per activation; file edits do not affect the current
  snapshot until explicit apply. API-supplied env/file references are rejected.
- Managed values are sealed with D-02 connection/field AAD, stores receive only
  envelopes, and ordinary metadata omits plaintext and ciphertext.
- Root `gopkg.in/yaml.v3@v3.0.1` direct dependency absence is not a D-03 product
  finding. Exact product readonly lint/test discovery fails as documented.
  Coordinator commit `1a1e5d1c8899d19174c8785d2770a5bff89769d7`
  changes only `go.mod`/`go.sum` relative to D-03 scope and resolves the blocker.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product/tree/ancestry, owned-path diff, clean detached status | Passed. |
| `GOWORK=off go test -modfile=<review-modfile> -count=50 ./internal/configuration` | Passed. |
| `GOWORK=off go test -modfile=<review-modfile> -race -count=10 ./internal/configuration` | Passed. |
| `GOWORK=off go test -modfile=<review-modfile> ./...` and `go vet` | Passed. |
| `GOWORK=off go mod verify` | Passed; all modules verified. |
| CGO-free Linux amd64 and arm64 compile-only focused tests | Passed. |
| `./scripts/check-guardrails.sh --fast` at exact product | Passed; generation, Vacuum 100/100, architecture, format, targeted root and UI checks passed. |
| Exact-product `-mod=readonly` package discovery and full lint | Blocked only by missing direct YAML requirement, as handoff states. |
| Full lint, `./scripts/check-guardrails.sh --ci`, and planning at dependency fix `1a1e5d1` | Passed; D-03 product bytes unchanged. |
| Ambiguous credential commit, static rotation, mapping-prefix, parse cleanup, and invalidator-reentry probes | Failed as findings record. |
| CodeRabbit 0.7.6 review against exact parent | Completed with two suggestions; plaintext cleanup confirmed and lock-held external calls independently reproduced. |
| Public secret/private-coordinate scan | Passed; no private value or coordinate found. |

## Acceptance disposition

- A-38: D-03 source ownership, collision rejection, runtime retirement, and
  stale-write behavior pass. Full acceptance remains open pending corrections
  and C-04 persistence/transport evidence.
- A-39: not accepted. Startup-only visibility and stale ETag rejection pass,
  but a changed static secret can become active with unchanged approval
  bindings and no target verification.
- A-42: not accepted. Intended managed values are encrypted and arbitrary API
  file refs are rejected, but uncertain writes can activate orphan credentials
  and failed parsing retains resolved plaintext.

No product file, task definition, shared script, module manifest, or
`docs/execution/state.json` was modified by the reviewer.
