# D-03 independent review, round two

## Decision

`changes_requested`.

Correction closes plaintext cleanup and lock-held callback findings. Three
current blockers remain. Managed credential writes are visible before the
matching configuration commit and can survive a rejected update across
restart. Target-identity verification cannot use the candidate managed
credential, while static credential revisions change on every restart even
when the secret is unchanged. Windows prefixes remain incompatible with two
current consumers and drive-letter aliases bypass ambiguity validation.

## Review identity and scope

- Reviewer: `/root/d02_reviewer`, independent of product author
  `/root/d02_implementer`.
- Reviewed correction product:
  `754410462c38c0fb2b95b33c4c5af82634f0fd90`; direct parent
  `d179dad6197eb58c51b256a6538cdc6c6865702e`; tree
  `773953b1d4aed44103ae4c2ed026cc01187a00f7`.
- Reviewed handoff:
  `fe1a9e784b63902a1e11454219d361100e18dcad`; product parent; tree
  `719aa0207452f0c402f1a653972e689d1f991f33`.
- Correction scope: `internal/configuration/configuration.go`,
  `configuration_test.go`, and `yaml.go`.
- Scoped correction diff SHA-256:
  `97e3470500a85c120253f8412c7be0303930ff17911d51f388b1e092e6d9525d`.
- Prior receipt:
  `e05b8d56d784577cd7dda8b0a40ef180992925fa`.
- Acceptance reviewed: A-38, A-39, and A-42.
- Release stage: pre-v0.1.0, target v0.0.1.

Executable review ran in clean reviewer-owned Git worktrees at the exact
product commit. Probe files are untracked outside the product branch. No live
service, credential, private endpoint, media, upstream mutation, release, or
deployment was used.

## Findings

### P1: pending and rejected managed credential writes can become active

`UpdateConnection` writes the desired encrypted set at
`internal/configuration/configuration.go:1533-1543` before its final
generation compare-and-set at lines 1544-1550. During that interval,
`ResolveCredential` checks active field membership and quarantine at lines
1222-1235, but does not reject a connection in `pendingConnections`. A
blocking synthetic store committed the second credential while the update
remained pending. Concurrent resolution returned the new value with no error:

```text
pending credential was readable before config commit: error <nil>
```

The final compare-and-set failure quarantines only the current `Manager`.
`APIState` persists managed field names but no envelope-set binding, and
`installAPIState` performs no store-to-revision reconciliation. A probe
forced an unrelated generation change after verification, let the credential
store commit, and observed `ErrRevisionMismatch` from the update. Resolution
failed in-process because of quarantine. Recreating the manager with the
unchanged pre-update connection and the same store exposed the rejected value:

```text
rejected update became active after restart: "second-key"
```

This leaves round-one store uncertainty open across concurrent reads and
restart, despite correct complete-set readback and removal of blind rollback.

Required correction: make pending credential sets unavailable to resolution
and metadata reads. Bind the active durable configuration revision to the
complete stored envelope set, or persist a quarantine/outcome that startup
reconciles before serving credentials. A write whose configuration
compare-and-set fails must remain inaccessible after restart without issuing a
blind rollback mutation.

Disposition: `current_blocker`.

### P1: credential identity preservation has no candidate credential and no stable binding

`IdentityVerifier` receives only a redacted `domain.Connection` at
`internal/configuration/configuration.go:164-166`.
`authorizeRevisionChange` calls it at lines 930-948, before the candidate
envelopes are written at lines 1533-1543. `ResolveCredential` therefore
returns the old active value during verification. A verifier that required the
second managed credential saw the first and the update failed with:

```text
candidate credential was unavailable to verifier: configuration credential target identity is unverified
```

Tests that return `IdentityVerified` unconditionally do not prove target
identity. The current boundary cannot authenticate the candidate managed
credential against an upstream target.

Static bindings also lack stable restart identity. `New` creates a fresh
random HMAC key at lines 294-322, and that process-local result participates in
the connection revision at lines 2266-2300. Two managers loaded identical YAML
and identical secret bytes with the same startup time but produced different
connection and effective revisions. This makes unchanged configuration appear
changed after every restart and makes revision-bound intent stale without a
material change.

Required correction: either invalidate every credential-only change in v0.0.1,
or give verification an attempt-scoped way to use candidate credentials
without leaking plaintext into domain/public API types. Use a stable private
binding across restart, or explicitly narrow the revision contract and supply
another durable comparison that distinguishes changed from unchanged static
credentials.

Disposition: `current_blocker`.

### P1: accepted Windows mappings remain unusable and ambiguous

Configuration now accepts `C:/downloads` at
`internal/configuration/configuration.go:2602-2667`. qBittorrent control
still accepts only slash-rooted paths at
`internal/adapters/qbittorrent/control/control.go:814-824,951-953`.
NZBGet inventory has the same POSIX-only contract at
`internal/adapters/nzbget/inventory/inventory.go:1598-1612,1667-1684`.
Focused consumer-package probes failed for both consumers:

```text
configuration-accepted Windows source prefix is rejected by qBittorrent control
configuration-accepted Windows source prefix is rejected by NZBGet inventory
```

Configuration also preserves drive-letter case at lines 2626-2638 and compares
namespaces byte-for-byte at lines 2682-2687. It accepted `C:/downloads` and
`c:/downloads` for the same connection and different roots without
`ErrMappingAmbiguous`.

Required correction: share one canonical source-prefix contract with every
consumer, or validate by connection kind and reject forms that its current
consumer cannot use. Normalize Windows drive aliases before ambiguity and
component matching. Add configuration-to-constructor compatibility tests for
each supported connection kind.

Disposition: `current_blocker`.

## Closed round-one findings

- Parse-failure plaintext cleanup passes. Local resolved-value ownership,
  later-connection failure, and duplicate-ID paths zero current and previously
  resolved buffers.
- `Invalidator`, identity verifier, and credential-store calls execute
  outside `manager.mu`. Reentrant snapshot callbacks complete, and generation
  compare-and-set rejects stale concurrent activation.
- Commit-then-error store writes now use complete-set readback, orphan fields
  require active managed membership, and no blind rollback occurs. Finding one
  remains open only for pending visibility and restart durability described
  above.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product, parent, tree, handoff ancestry, clean status, scoped diff and hash | Passed. |
| `GOWORK=off go test -count=50 ./internal/configuration` | Passed. |
| `GOWORK=off go test -race -count=10 ./internal/configuration` | Passed. |
| Focused store-readback, field-membership, parse-zeroing, reentrant callback, and concurrent CAS tests under `-race -count=50` | Passed. |
| `GOWORK=off go test -race ./...` | Passed. |
| `GOWORK=off go vet ./internal/configuration` and `GOWORK=off go mod verify` | Passed; all modules verified. |
| Linux amd64 and arm64 CGO-free focused compile checks | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed; generation, Vacuum 100/100, architecture, format, root/UI/tools/client tests, lint, vet, and module verification passed. |
| `./scripts/check-lint.sh`, architecture, and planning checks | Passed; 0 lint issues and 44 tasks/60 acceptance cases. |
| Pending read and rejected-write restart probes | Failed as finding one records. |
| Candidate identity and unchanged-static-restart probes | Failed as finding two records. |
| qBittorrent control, NZBGet inventory, and drive-alias mapping probes | Failed as finding three records. |
| CodeRabbit 0.7.6 committed correction review | Completed with one drive-letter canonicalization finding, reproduced independently. |
| Focused public-data scan | Passed; no private path, address, tracker credential, or key material found. |

## Acceptance disposition

- A-38: remains open. Ownership, retirement, collisions, and stale API
  revisions pass, but accepted Windows mapping records cannot seed all current
  consumers and case aliases bypass ambiguity rejection.
- A-39: not accepted. A credential update rejected by configuration CAS becomes
  active after restart, and unchanged static secrets get new revisions on every
  restart.
- A-42: not accepted. Envelope encryption and parse cleanup pass, but pending
  and rejected managed credentials can be resolved outside their committed
  configuration state.

No product file, task definition, shared script, module manifest, or
`docs/execution/state.json` was modified by the reviewer.
