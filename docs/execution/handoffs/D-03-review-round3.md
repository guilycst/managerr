# D-03 independent review, round three

## Decision

`changes_requested`.

Managed pending-read fencing, restart quarantine, candidate managed
verification, and POSIX-only mapping compatibility pass. Static credential
binding remains unsafe: its HMAC key is derived entirely from public metadata,
so the exposed revision verifies offline secret guesses. Static verification
also still receives the old active credential instead of the parsed candidate.
Same-value managed replacement performs another write and changes revision,
contrary to Mastarr's idempotence invariant.

## Review identity and scope

- Reviewer: `/root/d02_reviewer`, independent of product author
  `/root/d02_implementer`.
- Reviewed product:
  `bc7583175d11f6df78aa0ea7b867d077d26d5c5a`; direct parent
  `d36242f59f562dff1cd700c27019e7783a73c647`; tree
  `6414f7234f95ab016399f2bdc8f8a2dde1b131f2`.
- Reviewed handoff:
  `bf18818b3da5df9fa80485630bbf4c85eaa2707e`; product parent; tree
  `e68b45420bf17a4d6569a6f7b267412bd37658cb`.
- Product scope: `internal/configuration/configuration.go` and
  `configuration_test.go`.
- Scoped product diff SHA-256:
  `547144d5d446302fca8adaceb870cc2fefcf4b6bdd45b0ac66b84dd1558ec33c`.
- Prior review receipt:
  `a1e643b562365245def8f2d665ad15c86ac6d79b`.
- Acceptance reviewed: A-38, A-39, and A-42.
- Release stage: pre-v0.1.0, target v0.0.1.

Executable checks ran in clean reviewer-owned Git worktrees at the exact
product. Probe files remain untracked outside the product branch. No live
service, credential, private endpoint, media, upstream mutation, release, or
deployment was used.

## Findings

### P1: exposed static revision is an offline secret verifier

`stableBindingKey` at
`internal/configuration/configuration.go:2443-2462` hashes a fixed literal,
the document ID, and `credentials.Manager.Fingerprint()`. Document ID and key
fingerprint are non-secret metadata. Without a credential manager, the
fallback uses only the document ID. `opaqueCredentialBinding` then HMACs the
static secret with this publicly derivable key at lines 2465-2468, and
`connectionRevisionWithBindings` publishes a deterministic hash containing
that result at lines 2481-2490.

A probe loaded a synthetic static secret through a manager with a credential
encryption key, retained only the public document ID, public key fingerprint,
public connection fields, and exposed connection revision, then tried three
candidate values. Reimplementing the committed derivation from those public
values identified the exact secret:

```text
public revision confirmed offline secret guess "synthetic-password"
```

The second SHA-256 layer in the connection revision does not prevent dictionary
testing. This is not a private HMAC binding and conflicts with the A-42
redaction boundary and the prior requirement that revision metadata not expose
a secret verifier.

Required correction: compute static bindings with secret material. Preferred
shape is a domain-separated HMAC operation inside
`internal/credentials.Manager`, using its persistent key material without
exporting that key. A separately persisted random binding key is also valid.
If neither exists, fail closed for static credential binding. Do not use a
fresh process-random fallback because that restores restart revision churn, and
do not derive the key from document ID or public fingerprint.

Disposition: `current_blocker`.

### P1: static identity verification cannot use the parsed candidate

Managed rotation now has a scoped `CandidateCredentialReader`, and its
independent probe passes. Static YAML changes still call
`runRevisionChanges` without candidate credentials at
`internal/configuration/configuration.go:789,997-1026`, then use the legacy
`IdentityVerifier` at lines 1029-1047. During that callback,
`ResolveCredential` returns the currently active static value at lines
1384-1387.

A probe changed a synthetic secret file from `first-key` to `second-key`.
The verifier required the exact parsed candidate through the manager, saw the
old value, and activation failed:

```text
exact static candidate was unavailable to verifier: configuration credential target identity is unverified
```

Re-reading the file inside a verifier is not equivalent. It bypasses the
configured resolver and can verify bytes different from the already parsed
candidate if the file changes between reads.

Required correction: provide the verifier an attempt-scoped reader over the
exact parsed static values, with the same zero-after-call behavior as managed
candidates. Simpler v0.0.1 behavior may always invalidate static credential
changes rather than claim verified identity preservation.

Disposition: `current_blocker`.

### P1: same-value managed credential replacement is not idempotent

`UpdateConnection` seals every supplied credential again at
`internal/configuration/configuration.go:1653-1685`. Fresh AEAD nonce bytes
change the envelope digest and appended revision even when field names and
plaintext are unchanged. It then verifies and writes the new envelope set at
lines 1703-1712.

An independent probe replaced `apiKey` with its existing value. The request
performed a second store replacement, called identity verification, and
returned a different revision:

```text
same credential mutated state: writes=2, verifications=1
```

This violates the repository invariant that already-materialized desired state
succeeds without another write.

Required correction: compare the complete candidate field/value set with the
authenticated active envelope set before sealing or writing. Zero all
temporary plaintext. If equal, return the current connection and revision
without verification, invalidation, or store replacement.

Disposition: `current_blocker`.

## Closed prior findings

- Pending managed mutations block both resolution and metadata. Post-store
  checks revalidate pending state, quarantine, field membership, and the
  complete envelope-set digest.
- A store write that loses configuration compare-and-set remains quarantined
  after restart. The active revision's embedded digest disagrees with the
  stored set, and startup reconciliation keeps both credential reads
  unavailable without rollback.
- Managed target verification reads only the exact candidate field set.
  Unknown fields fail, and manager-owned returned buffers are zeroed after the
  verifier returns.
- Configuration, qBittorrent control, and NZBGet inventory now agree on the
  POSIX source-prefix contract. Empty, relative, backslash, and Windows
  drive-letter forms fail configuration validation.
- Round-two parse cleanup and callback/store lock findings remain closed.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product, parent, tree, handoff ancestry, clean status, scoped diff and hash | Passed. |
| `GOWORK=off go test -count=50 ./internal/configuration` | Passed. |
| `GOWORK=off go test -race -count=10 ./internal/configuration` | Passed. |
| Pending read, rejected-CAS restart, candidate reader/zeroing, and configuration POSIX probes under `-race -count=50` | Passed. |
| qBittorrent control and NZBGet inventory POSIX/Windows compatibility probes, `-count=50` | Passed. |
| Static offline-guess and exact-candidate probes | Failed as findings one and two record. |
| Same-value managed replacement probe | Failed as finding three records. |
| `GOWORK=off go test -race ./...` | Passed. |
| `GOWORK=off go vet ./...` and `GOWORK=off go mod verify` | Passed; all modules verified. |
| Linux amd64 and arm64 CGO-free focused compile checks | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed; generation, Vacuum 100/100, architecture, format, root/UI/tools/client tests, lint, vet, and module verification passed. |
| `./scripts/check-lint.sh`, architecture, and planning checks | Passed; 0 lint issues and 44 tasks/60 acceptance cases. |
| CodeRabbit 0.7.6 committed correction review | Completed with four suggestions. Secret-key derivation was independently reproduced; unsafe legacy-digest adoption was rejected because fail-closed quarantine is correct. |
| Focused public-data scan | Passed; no private path, address, tracker credential, or key material found. |

## Non-blocking hardening register

- `v0.1.0_candidate`: align default document identity between
  `New(Options{YAMLPath: ...})` and `LoadYAMLFile`; one uses
  `config.yaml` while the other uses the file basename.
- `post_v0.1.0`: track credential replacement separately from general
  connection updates if brief credential unavailability during label-only
  updates becomes material.

## Acceptance disposition

- A-38: accepted for D-03's source ownership, retirement, collision, stale
  revision, and narrowed POSIX mapping contribution.
- A-39: remains open. Restart durability and stable unchanged revisions pass,
  but static candidate verification remains unavailable and same-value managed
  replacement changes revision.
- A-42: not accepted. Managed encryption and reconciliation pass, but static
  revision metadata enables offline secret guessing.

No product file, task definition, shared script, module manifest, or
`docs/execution/state.json` was modified by the reviewer.
