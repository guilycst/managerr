# D-03 configuration correction round 2 handoff

## Assignment

- Task ID and title: D-03 correction round 2, close the independent review blockers for effective configuration and source ownership.
- Owner and independent reviewer: `/root/d02_implementer`; independent review of the product is required from an agent other than the author.
- Dispatch base: `754410462c38c0fb2b95b33c4c5af82634f0fd90`.
- Review receipt addressed: `a1e643b562365245def8f2d665ad15c86ac6d79b`.
- Branch/worktree: shared `main` checkout; the coordinator owns `docs/execution/state.json` and integration.
- Owned paths: `internal/configuration/` and this handoff only. No state, module, script, adapter, port, UI or generated path was changed.
- Product commit: `bc7583175d11f6df78aa0ea7b867d077d26d5c5a` (`fix(configuration): reconcile managed credentials across restarts`).

## Review corrections

### Durable managed credential publication

`ResolveCredential` and `CredentialMetadata` now reject active managed fields while a connection mutation is pending. They also recheck pending state, quarantine, field membership and the complete envelope-set digest after store I/O, so a concurrent replacement cannot expose a candidate that has not reached the matching configuration CAS.

API connection revisions now carry an opaque `|managed=sha256:<digest>` binding for the complete encrypted envelope set. `APIState.ManagedCredentialDigests` is available to storage adapters that persist the digest separately during migration. Startup extracts and validates the binding, reads the complete stored envelope set outside `manager.mu`, and quarantines an active managed connection when the set is absent, unreadable or different. A write that reaches the store but loses the configuration generation CAS therefore remains inaccessible after restart; no rollback write is attempted.

### Attempt-scoped candidate identity verification

Added `CandidateIdentityVerifier` and `CandidateCredentialReader`. For a managed credential-only change, the verifier receives the candidate connection’s redacted domain object plus a reader scoped to that authorization attempt. The reader decrypts only the requested candidate envelope, never places plaintext in domain data, revisions, snapshots or the store boundary, and the manager zeroes all buffers it returned after verification. The legacy verifier is not used to preserve intent for managed changes because it cannot observe the candidate; without candidate verification, the change fails closed or invokes the existing invalidator. Static YAML changes retain the existing verifier path.

### Stable static bindings

Static secret bindings now use a deterministic HMAC namespace scoped to the YAML document identity and, when present, the non-secret credential-key fingerprint. Identical YAML and resolved static bytes produce the same connection/effective revisions after restart; a resolved secret change still advances the connection revision and follows the verification/invalidation rule.

### Upstream path compatibility

The current qBittorrent control and NZBGet inventory consumers accept POSIX source namespaces. Configuration now rejects empty, relative, and Windows drive-letter source prefixes, including both `C:/` and `c:/` forms, until all upstream adapters share a canonical cross-platform contract. Focused compatibility coverage exercises both qBittorrent and NZBGet connection kinds with POSIX acceptance and Windows rejection.

## Tests and checks

All checks below ran against product commit `bc7583175d11f6df78aa0ea7b867d077d26d5c5a` in the shared checkout using synthetic data only:

| Command or scenario | Result |
| --- | --- |
| `gofmt -w internal/configuration/configuration.go internal/configuration/configuration_test.go` and `git diff --check` | Passed. |
| `GOWORK=off go test ./internal/configuration -count=10` | Passed. |
| `GOWORK=off go test -race ./internal/configuration -count=5` | Passed. |
| `GOWORK=off go test -race ./...` | Passed. |
| `GOWORK=off go vet ./...` | Passed. |
| `GOWORK=off go mod verify` | Passed; all modules verified. |
| `./scripts/check-guardrails.sh --fast` | Passed; bundled API/Vacuum, generation, architecture and targeted tests passed. Vacuum reported quality 100/100 with zero warnings/errors. |
| `./scripts/check-lint.sh` | Passed; zero lint issues and architecture checks passed. |
| `python3 scripts/check_planning.py` | Passed; 44 tasks, 60 acceptance cases and local links resolved. |
| `GOOS=linux GOARCH=amd64 CGO_ENABLED=0 GOWORK=off go test -run '^$' -c ./internal/configuration` | Passed. |
| `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 GOWORK=off go test -run '^$' -c ./internal/configuration` | Passed. |
| Pending read probe | Passed: resolve and metadata return `ErrCredentialUnavailable` while replacement is blocked. |
| Candidate verifier probe | Passed: verifier reads the exact candidate value through the attempt reader and receives no domain plaintext. |
| Restart-after-CAS-failure probe | Passed: a committed store write that loses generation CAS is quarantined after manager restart using the persisted revision binding. |
| Unchanged static restart probe | Passed: identical YAML and secret bytes retain revisions across restart; changed bytes advance the connection revision. |
| qBittorrent/NZBGet source-prefix compatibility probe | Passed: POSIX source prefixes are accepted and Windows drive-letter prefixes are rejected for both kinds. |
| Live services, credentials, private coordinates, media data or upstream mutations | Intentionally not run. |

## Review and integration

- Independent review status: pending for product `bc7583175d11f6df78aa0ea7b867d077d26d5c5a`.
- Review findings addressed: pending managed reads, restart durability after a rejected CAS, candidate credential verification, stable static revisions, and POSIX-only qBittorrent/NZBGet source mapping.
- Remaining integration work: coordinator must map the durable digest and managed field set through the persistent API-state loader, wire candidate verification at the adapter boundary, and record the exact product and handoff SHAs in `docs/execution/state.json`.
- No credentials, private service coordinates, live inventory or media data were used.

## Resume checkpoint

- Product checkpoint: `bc7583175d11f6df78aa0ea7b867d077d26d5c5a`.
- Safe next action: independent reviewer runs the scoped probes and full guardrails from a clean checkout, then coordinator integrates or dispatches any narrowly scoped finding.
- Active ownership: this lane has no uncommitted product files; coordinator owns state and integration.
- Known contract limit: source prefixes remain POSIX-only until the qBittorrent and NZBGet adapters are migrated to one explicit cross-platform namespace contract.
