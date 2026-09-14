# D-03 correction round one handoff

## Assignment

- Task ID and title: D-03, implement effective configuration and source ownership.
- Implementer: `/root/d02_implementer`.
- Independent reviewer: `/root/d02_reviewer` (pending re-review).
- Correction base product: `3e86f117155753e7165f38e0f9290bdcc78bc322`.
- Review receipt: `e05b8d56d784577cd7dda8b0a40ef180992925fa`.
- Checkout ownership: shared `main` checkout. Product changes are limited to
  `internal/configuration/`; this handoff is the only other path in this
  correction. The coordinator owns `docs/execution/state.json`.
- Product commit: `754410462c38c0fb2b95b33c4c5af82634f0fd90`
  (`fix(configuration): close D-03 correction findings`).

## Findings addressed

- Managed credential writes now read back the complete encrypted field set
  after every `Replace`, including commit-then-error outcomes. A desired set is
  considered committed only when the read-back matches exactly. A read-back of
  the previous set proves a failed write had no effect; every other uncertain
  set quarantines the connection. No blind rollback write is issued. Active
  managed-field membership is required by both `ResolveCredential` and
  `CredentialMetadata`, so orphan or unrequested fields remain inaccessible.
- Credential-only managed and static changes have an explicit
  `IdentityVerifier` seam. Verified target identity preserves approval intent;
  unavailable, failed, or uncertain verification invalidates through the
  existing callback when available and otherwise returns
  `ErrCredentialIdentityUnverified`. Static resolved values use a private
  random-key HMAC binding to detect changes without storing or exposing
  plaintext or a reversible secret digest. The binding participates in the
  resource revision, including after restart.
- Source prefixes require canonical absolute remote namespaces. POSIX and
  Windows forms such as `/downloads` and `C:/downloads` are accepted; empty,
  relative, backslash, traversal, and noncanonical forms are rejected for YAML
  and API mappings. Destination prefixes remain root-relative.
- YAML credential cleanup uses a local resolved-value map whose ownership is
  transferred only on successful conversion. Parse errors and duplicate
  resources zero both prior resolved values and the current connection's
  values before returning.
- Credential-store I/O and invalidator callbacks run without `manager.mu`.
  Mutations snapshot generation and expected revisions, perform external work,
  then compare-and-set before activation. YAML activation and API connection,
  root, mapping, and retirement operations follow this boundary. Tests cover
  reentrant callbacks and concurrent stale updates.

## Verification

| Command or scenario | Result | Evidence |
| --- | --- | --- |
| `GOWORK=off go test ./internal/configuration -count=1` | Passed | Focused D-03 suite, including store uncertainty, identity, mapping, cleanup, reentry, and CAS tests. |
| `GOWORK=off go test -race ./internal/configuration -count=1` | Passed | Configuration concurrency and external-callback tests. |
| `GOWORK=off go test -race ./...` | Passed | Root packages and synthetic compatibility tests. |
| `GOWORK=off go vet ./...` | Passed | Root packages. |
| `GOWORK=off go mod verify` | Passed; `all modules verified` | Root module. |
| `./scripts/check-guardrails.sh --fast` | Passed | Bundled API/Vacuum, generation, architecture, format, targeted root and UI checks. |
| `./scripts/check-guardrails.sh --ci` | Passed | Full root, UI, tools, qBittorrent, and NZBGet module checks with lint, vet, and module verification. |
| `./scripts/check-lint.sh` | Passed; 0 issues | Root, UI, tools, and standalone client architecture checks. |
| `python3 scripts/check_planning.py` | Passed; 44 tasks, 60 acceptance cases | Planning links and structure. |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64/arm64 GOWORK=off go test -run '^$' -c ./internal/configuration` | Passed | Linux compile-only checks for both architectures. |
| Secret/store fixtures and invalidator probes | Passed | Synthetic values only; no live service or private coordinates. |

## Review and integration

- Product SHA: `754410462c38c0fb2b95b33c4c5af82634f0fd90`.
- Handoff SHA: pending this commit.
- Reviewer decision: pending independent re-review of the product SHA.
- Coordinator owns state recording, integration, and any shared-contract
  changes.
- Cross-adapter follow-up: this lane accepts documented POSIX and Windows
  remote forms, while the existing qBittorrent control adapter still has a
  POSIX-only local validator. Adapter alignment remains outside this assigned
  correction path and must be resolved before enabling Windows mappings there.

## Resume checkpoint

- D-03 correction product is committed and all local root/module guardrails
  pass.
- Next safe action: independent review of product
  `754410462c38c0fb2b95b33c4c5af82634f0fd90`, then coordinator state update and
  integration after approval.
- No state file, module manifest, script, generated output, adapter, UI path,
  credential, private endpoint, media inventory, or live upstream was changed.
