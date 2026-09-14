# D-03 independent review, round four

## Decision

`approve`.

The correction closes all three round-three blockers. Static revisions now use
private persistent key material and the former public-data derivation cannot
verify guesses. Static credential changes are verified against the exact
parsed candidate and cannot fall back to the legacy active-value verifier.
Replacing a managed credential set with the same plaintext is a read-only
success with no envelope, store, callback, invalidation, generation, or
revision change. Prior durability, quarantine, zeroing, and POSIX mapping
corrections remain intact.

## Review identity and scope

- Reviewer: `/root/d02_reviewer`, independent of product author
  `/root/d02_implementer`.
- Reviewed product:
  `9eea2a9e1f7c87da162b88b915cf3f565077b41b`; direct parent
  `d2f1d9a00f9efd9c169141056f8561e124539bde`; tree
  `6f538c356c759c61904a5585b7e71429da7b5b32`.
- Reviewed handoff:
  `6fb0cf19b43c20e90bd58d7892909b13758814a4`; product parent; tree
  `e1e33c5628c21085f93fcc563f0b7bad241e5a60`.
- Product scope: `internal/configuration/` and `internal/credentials/`.
- Scoped product diff SHA-256:
  `0fa4c3dc0c211c711aded39047164641fda720ffc81506bb1cdddb8e2dabb1910`.
- Prior review receipt:
  `8676667a9ce9cd7fb1aaf683dfd635457ddc8bd8`.
- Acceptance reviewed: A-38, A-39, and A-42.
- Release stage: pre-v0.1.0, target v0.0.1.

Executable review ran in clean reviewer-owned Git worktrees at the exact
product. Adversarial probe files remain untracked in the separate probe
worktree and are not part of this receipt. No live service, credential, private
endpoint, media, upstream mutation, release, or deployment was used.

## Findings

No blocking findings.

### Closed: static revision has no public or process-only fallback

`credentials.Manager.Bind` at
`internal/credentials/credentials.go:306-327` computes an HMAC over a versioned
domain, length-delimited document namespace, and secret value using the
manager's private key material. Configuration passes only the opaque result
into its revision at `internal/configuration/configuration.go:508-526`.
Documents containing static credentials fail closed without a credential
manager at lines 513-520; no public fingerprint, document-only, or process
random fallback remains.

An independent probe retained only public document identity, public key
fingerprint, connection metadata, and the exposed revision, then reimplemented
the removed public-data algorithm against the exact candidate and two wrong
guesses. None matched. Identical input under a different private key produced
a different revision, and parsing without a credential manager returned
`ErrCredentialManagerNeeded`.

### Closed: static verification uses the exact candidate and fails closed

YAML activation passes values resolved during that parse at
`internal/configuration/configuration.go:798-850`. The authorization path at
lines 1077-1107 selects the candidate verifier for static changes and invokes
the legacy verifier only when no candidate set exists. The attempt-scoped
reader at lines 1148-1177 copies only fields in the parsed candidate and zeroes
every returned buffer after the callback.

An independent probe changed a synthetic static value from `static-first` to
`static-second`. The candidate verifier observed only `static-second`, an
unknown field returned `ErrCredentialUnavailable`, and the retained returned
buffer was zeroed after activation. A second manager configured with only the
legacy verifier rejected the same change with
`ErrCredentialIdentityUnverified`, never called that verifier, and retained
the old active value.

### Closed: same managed value is idempotent

`UpdateConnection` loads and authenticates the complete stored set before
sealing at `internal/configuration/configuration.go:1734-1777`.
`managedCredentialValuesEqual` at lines 2344-2390 checks the bound envelope-set
digest, opens every field with its connection/field AAD, compares the complete
plaintext set, and zeroes temporary plaintext. The equal path returns the
current state after a generation/revision check and does not enter candidate
verification or persistence.

An independent probe snapshotted the complete encrypted set and all counters,
then submitted the same credential. Revision, envelope bytes, store writes,
verifier calls, invalidator calls, and generation-sensitive state remained
unchanged. A label change submitted with the same credential updated only the
label and revision; the envelope set and credential-related callbacks remained
unchanged.

## Retained corrections

- Pending managed mutations keep credential resolution and metadata
  unavailable. Rejected compare-and-set writes remain quarantined after
  restart through the revision-bound complete envelope digest.
- Store uncertainty uses complete-set readback and never performs a blind
  rollback. Missing, malformed, unauthenticated, or digest-inconsistent active
  sets fail closed.
- Managed and static candidate readers reject fields outside the candidate
  set. Manager-owned returned plaintext buffers and parse-failure resolver
  buffers are zeroed.
- qBittorrent and NZBGet mapping inputs retain the absolute POSIX namespace
  contract; relative, backslash, and Windows drive-letter forms fail
  configuration validation.
- External store and callback operations remain outside `manager.mu`, while
  generation compare-and-set checks reject concurrent stale activation.

## Independent checks

| Command or scenario | Result |
| --- | --- |
| Exact product, parent, tree, handoff ancestry, clean status, scoped diff and hash | Passed. |
| Private static binding, removed public verifier, different-key and no-manager probes | Passed under `-race -count=50`. |
| Exact static candidate, unknown-field, zero-after-call and legacy fail-closed probes | Passed under `-race -count=50`. |
| Same-value managed credential and label-plus-same-value side-effect probes | Passed under `-race -count=50`. |
| `GOWORK=off go test -count=50 ./internal/configuration ./internal/credentials` | Passed. |
| `GOWORK=off go test -race -count=10 ./internal/configuration ./internal/credentials` | Passed. |
| Focused pending, uncertain-write, restart-quarantine, field-membership, parse-zeroing, static-restart, and POSIX tests under `-race -count=50` | Passed. |
| `GOWORK=off go test -race ./...` | Passed, including storage in 208.244 seconds. |
| `GOWORK=off go vet ./...`, `GOWORK=off go mod verify`, and `git diff --check` | Passed; all modules verified and exact product tree remained clean. |
| Linux amd64 and arm64 CGO-free compile checks with `-mod=readonly` | Passed. |
| `./scripts/check-guardrails.sh --ci` | Passed; generation, Vacuum 100/100, architecture, format, lint, root/UI/tools/client tests, vet, and module verification passed. |
| `./scripts/generate.sh --check` and `./scripts/check-api.sh` | Passed; generated output reproducible and Vacuum reported 100/100. |
| `./scripts/check-lint.sh`, architecture, and planning checks | Passed; 0 lint issues and 44 tasks/60 acceptance cases. |
| Focused public-data scan | Passed; no private path, address, tracker credential, or key material found. |

## Non-blocking follow-up register

- `v0.1.0_candidate`: derive a dedicated static-binding subkey if future crypto
  agility work requires formal AES/HMAC key separation. The current HMAC is
  keyed by secret persistent material and satisfies this correction's binding
  and offline-guess requirements.
- `v0.1.0_candidate`: align default document identity between
  `New(Options{YAMLPath: ...})` and `LoadYAMLFile`; one uses the configured
  default while the standalone loader uses the file basename.
- The correction handoff's final Review subsection retains the old reviewed
  product `bc758317...`; its Assignment, product commit, verification, and
  resume sections correctly identify `9eea2a9...`. This is an editorial
  provenance typo and does not change the exact product or review result.

## Acceptance disposition

- A-38: D-03 contribution accepted. YAML/API source ownership, strict
  validation, retirement, stale revision handling, and the POSIX mapping
  contract pass.
- A-39: D-03 contribution accepted. Startup-only activation, durable
  quarantine, exact credential candidate handling, stable static revisions,
  and same-value idempotence pass.
- A-42: D-03 contribution accepted. Credential encryption/AAD, private static
  binding, redaction, zeroing, and fail-closed reconciliation pass.

No product file, task definition, shared script, module manifest, or
`docs/execution/state.json` was modified by the reviewer.
