# D-03 configuration correction round three handoff

## Assignment

- Task ID and title: D-03, effective configuration and source ownership, correction round three.
- Implementer: `/root/d02_implementer`.
- Independent reviewer: `/root/d02_reviewer` (pending re-review).
- Dispatch base: `d2f1d9a00f9efd9c169141056f8561e124539bde`.
- Product baseline under review: `bc7583175d11f6df78aa0ea7b867d077d26d5c5a`.
- Review receipt addressed: `8676667a9ce9cd7fb1aaf683dfd635457ddc8bd8`.
- Review integration checkpoint: `a493a8939d9f4d49a84f7d93fe5b82f2d7a50cca`.
- Checkout ownership: shared `main` checkout. Coordinator owns `docs/execution/state.json`; this lane changed only `internal/configuration/`, `internal/credentials/` and this handoff.
- Product commit: `9eea2a9e1f7c87da162b88b915cf3f565077b41b` (`fix(configuration): secure static bindings and idempotent credentials`).

## Corrections completed

### Private static credential bindings

`credentials.Manager.Bind` now computes a domain-separated HMAC using the
manager's private encryption key. The key is never exported or copied into
configuration metadata. YAML parsing, explicit reload and direct `ParseYAML`
callers use this callback; a document with static credentials and no credential
manager fails closed with `ErrCredentialManagerNeeded`. Equal static values
remain stable across restarts when the same persistent or supplied key is used,
while changed values produce a different opaque binding. The old public,
document-only fallback was removed.

### Exact static candidate verification

YAML activation now indexes the exact values resolved during that parse,
including an empty candidate map when a connection clears all credentials. The
existing candidate verifier receives an attempt-scoped reader backed by copies
of those parsed values. Returned buffers are zeroed after the verifier returns;
the parsed source buffers are cleared after activation or any failure. The old
legacy verifier is ignored for static credential-only changes because it cannot
observe the candidate. Missing candidate verification therefore invalidates or
fails closed.

### Idempotent managed credential replacement

Before sealing an API credential patch, `UpdateConnection` loads the complete
current envelope set and decrypts each field for an authenticated plaintext
comparison. It checks the active envelope-set digest when one is available,
zeros each temporary plaintext buffer, and returns the current connection when
the requested set is already materialized. This path performs no seal, store
replacement, identity verification, invalidation or revision change. Any
missing, malformed, unauthenticated or digest-inconsistent set fails closed.

## Changed paths

- `internal/configuration/configuration.go`
- `internal/configuration/configuration_test.go`
- `internal/configuration/yaml.go`
- `internal/credentials/credentials.go`
- `internal/credentials/credentials_test.go`
- `docs/execution/handoffs/D-03-correction-round3.md`

No state file, module manifest, script, adapter, generated output, live service,
credential, private endpoint, media inventory or upstream mutation was used or
changed.

## Verification

| Command or scenario | Commit / fixture version | Result / evidence |
| --- | --- | --- |
| `gofmt -w internal/configuration/*.go internal/credentials/*.go` and `git diff --check` | `9eea2a9e1f7c87da162b88b915cf3f565077b41b` | Passed. |
| `GOWORK=off go test ./internal/configuration ./internal/credentials -count=1` | Product commit; synthetic fixtures | Passed. |
| `GOWORK=off go test -race ./internal/configuration ./internal/credentials -count=5` | Product commit; synthetic fixtures | Passed. |
| `GOWORK=off go test -race ./...` | Product commit | Passed. |
| `GOWORK=off go vet ./...` | Product commit | Passed. |
| `GOWORK=off go mod verify` | Product commit | Passed; all modules verified. |
| `./scripts/check-guardrails.sh --ci` | Product commit | Passed: API/Vacuum, reproducible generation, architecture, format, lint, root/UI/tools/client tests, vet and module verification. |
| `python3 scripts/check_planning.py` | Product commit | Passed; 44 tasks, 60 acceptance cases and local links resolved. |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Product commit | Passed. |
| `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOWORK=off go test -mod=readonly -run '^$' -exec=true ./...` | Product commit | Passed. |
| Private static binding regression | `credentials.Manager`, synthetic key/value | Passed: same key/value is stable across managers; namespace and key changes differ; closed manager rejects binding. |
| No-manager static binding regression | `ParseYAML`, synthetic environment | Passed: static parse returns `ErrCredentialManagerNeeded`. |
| Exact static candidate regression | `CandidateIdentityVerifier`, synthetic file values | Passed: reload verifier observes the newly parsed value, and active resolution returns it after commit. |
| Same-value managed replacement regression | synthetic store and candidate verifier counters | Passed: revision and complete envelope set remain unchanged; no verifier, invalidator or store replacement call occurs. |
| Live services, credentials, private coordinates, media data or upstream mutations | N/A | Intentionally not run. |

## Review and integration

- Reviewed product: `bc7583175d11f6df78aa0ea7b867d077d26d5c5a`.
- Findings addressed: private static binding, exact parsed static candidate
  verification, and idempotent same-value managed replacement.
- Prior round-two closures preserved: durable pending/read fencing, restart
  quarantine after a rejected CAS, candidate managed verification and zeroing,
  POSIX-only qBittorrent/NZBGet mapping validation, parse cleanup, and external
  callback/store work outside `manager.mu`.
- Independent review status: pending `/root/d02_reviewer` review of product
  `9eea2a9e1f7c87da162b88b915cf3f565077b41b`.
- Handoff commit: pending this commit.
- Coordinator owns state recording, review integration and any later
  publication bookkeeping.

## Resume checkpoint

- Product checkpoint: `9eea2a9e1f7c87da162b88b915cf3f565077b41b`.
- Safe next action: independent scoped review from a clean checkout, then
  coordinator records the exact product and handoff SHAs in `state.json`.
- No uncommitted product files remain in this lane. No conflicting writes or
  unknown files were removed.
- Known uncertainty: upstream target identity verifier wiring remains an
  integration concern at the adapter boundary; this correction provides the
  exact candidate reader and fail-closed behavior but does not add live
  upstream calls.
