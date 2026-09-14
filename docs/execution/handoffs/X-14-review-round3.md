# X-14 independent review, round three

## Decision

`approved`.

The coordinator correction closes the sole round-two blocker. Aggregate
generation now always targets
`clients/qbittorrent/internal/generated/client.gen.go`; it no longer derives
the destination from generated artifact existence. Removing the internal
artifact and running the documented write generator restores the committed
file byte-for-byte and leaves the obsolete public generated path absent.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of the X-14 product author and
  coordinator correction author.
- Reviewed correction:
  `0d3cc2ead00c26237d8b24f0c8f269da0a974bfa`; direct parent
  `9f2dff6780ab166258be8892e8f192f0fb6c0bbe`; tree
  `6c73317280851990c490d3a7619ed9fa03344485`.
- Reviewed handoff:
  `fea793d67c88970e58337b435cc499495febad2a`; direct parent is the correction;
  tree `1c18470b8a728c81aea788ec4fce87051df51c96`.
- Prior review receipt:
  `4208c1c5ce32699e5fa78ce8fd33d4f5d639c329`.
- Correction changes only `scripts/generate.sh`. The handoff changes only
  `docs/execution/handoffs/X-14-correction-round2.md`.
- Correction diff SHA-256:
  `60ddd43c45d0d4627ec486b7e82c6e3618ae4a841605af5b39bce5681390452c`.
- Acceptance reviewed: A-28, A-29, A-30, A-31, A-33 and A-45.
- Review receipt checkpoint: commit containing this file; exact SHA is reported
  to the coordinator after commit because a Git commit cannot embed its own
  SHA.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-x14-review-r3` at the exact
handoff commit. The reviewer changed no product, generated, execution-state,
task, adapter or fixture path.

## Finding disposition

### Aggregate write generation: closed

`scripts/generate.sh` assigns the qBittorrent output unconditionally to
`clients/qbittorrent/internal/generated/client.gen.go`. The removed
file-existence branch and public-path fallback cannot recreate the externally
importable raw client.

Independent source-only probe used an archive of exact correction
`0d3cc2ead00c26237d8b24f0c8f269da0a974bfa`, recorded the committed internal
artifact digest, removed that artifact, and ran `./scripts/generate.sh --write`:

```text
generation completed
expected=2c800e4c6b95a28d68b7ff092bb7b28324d3a6d569056b909112cde4527c5577
actual=2c800e4c6b95a28d68b7ff092bb7b28324d3a6d569056b909112cde4527c5577
internal=recreated-byte-for-byte
public=absent
```

The reviewed checkout also contains neither
`clients/qbittorrent/generated/client.gen.go` nor a
`clients/qbittorrent/generated/` directory.

No remaining finding was identified.

## Independent checks

| Check | Result |
| --- | --- |
| Exact correction/handoff identity, ancestry, trees, scope and diff digest | Passed. |
| Missing-artifact `./scripts/generate.sh --write` probe from exact correction archive | Passed; internal output restored byte-for-byte, public path absent. |
| `./scripts/generate.sh --check` | Passed; aggregate and staged generation checks. |
| qBittorrent `GOWORK=off go generate ./...` and `./check-generation.sh` | Passed; no generated drift. |
| qBittorrent `GOWORK=off go test -mod=readonly -count=20 ./...` | Passed. |
| qBittorrent `GOWORK=off go test -race -mod=readonly -count=3 ./...` | Passed. |
| qBittorrent `GOWORK=off go vet -mod=readonly ./...` and `GOWORK=off go mod verify` | Passed; all modules verified. |
| Generated-output diff after regeneration | Clean. |
| `./scripts/check-guardrails.sh --ci` | Passed; API/Vacuum 100/100, architecture, standalone-client boundaries, lint, root/UI/tools/client tests, vet, generation and module verification. |
| Live qBittorrent, credentials, private coordinates and mounted media | Intentionally not used. |

## Acceptance disposition

- A-28: accepted for X-14, retaining the exact Stop scope and native empty
  success evidence approved in round two.
- A-29: accepted for X-14. Metadata-only Delete safety and the internal raw
  transport boundary remain intact, including after source-only regeneration.
- A-30: accepted for client scope; no re-add or resume surface exists.
- A-31: accepted for the control client and its route-specific response
  normalization.
- A-33: accepted. Typed uncertainty and no-blind-retry behavior remain intact.
- A-45: accepted. Both ordinary and missing-artifact generation are
  reproducible from the authoritative sources, and the full guardrails pass.

G-01/no-overwrite, live qBittorrent product-version evidence and runtime
capability approval remain open. This receipt does not approve release,
deployment, Arr writes or live media mutation.
