# X-04 correction round three handoff

## Assignment and review input

- Task: X-04, Jellyfin and Seerr read observations.
- Correction owner: `/root/d01_implementer`.
- Independent reviewer: `/root/d01_reviewer`.
- Previous product: `1df74b45b100b891b6afdf54567d2658d0f6228b`.
- Previous handoff: `bc597dcab95755ffa8e664a1a106dec1936ae934`.
- Review receipt addressed: `c07f13891f19ba40a49ed6d2415e8a19e78a6110`.
- Product correction commit: `b5594b668185245304f70a0f46c1f77ae7b76d7b`.
- Shared checkout: `main`; the coordinator owns integration and execution state.
- Owned paths: `internal/adapters/jellyfin/read/`,
  `tests/fixtures/catalogs/`, and this handoff. Seerr had no changes in this
  correction because its round-two findings were already closed.

No contract, state, storage, Arr, live service, credential, media payload or
upstream mutation was used. All fixtures are synthetic.

## P1 correction

Jellyfin now has a single validation boundary for native media-source evidence.
Before a source can be considered playable it must have a nonempty source ID
and path, `Protocol: File`, `LocationType: FileSystem`, `MediaType: Video`, a
valid normalized absolute path, and an item media type that is present and
matches the source. Only that verified native shape reaches the existing
mapping/no-mapping playability decision.

An item-level `Path` with no native `MediaSources` is synthesized only as a
bounded correlation observation. It always receives
`media_source_path_only_unverified` and cannot set `Item.Playable`, even when
an unambiguous configured mapping yields a `MappedTarget`. A native source with
no configured mapping remains playable only after the complete native shape
has passed validation. Remote, virtual, offline and non-file locations,
unsupported protocols, missing ID/path/protocol/location/media type, invalid
paths, item-level nonlocal locations, media-type mismatch and ambiguous or
wrong mappings remain unplayable with stable evidence reasons.

The Jellyfin catalog fixture now includes `sourceVariants` for remote,
virtual, offline, HTTP, missing protocol/location/media type/path/source ID,
invalid path, item/source mismatch, missing item media type, and item-level
remote/virtual/offline locations. Tests cover path-only with and without a
mapping, valid native mapped and unmapped file sources, every unsupported
variant, wrong mapping and ambiguous mapping. They assert both the stable
reason and the unavailable state.

## Verification evidence

All commands ran in the shared checkout before the product commit, with the
product commit hook passing the fast guardrail suite.

| Command | Result |
| --- | --- |
| `gofmt -l internal/adapters/jellyfin/read/*.go` | Passed; no output. |
| `git diff --check` | Passed before product commit. |
| `GOWORK=off go test -mod=readonly -count=1 ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| `GOWORK=off go test -mod=readonly -race -count=1 ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| `GOWORK=off go test -mod=readonly -count=1 ./...` | Passed; storage completed in 19.431s. |
| `GOWORK=off go test -mod=readonly -race -count=1 -timeout=360s ./...` | Passed; storage completed in 224.809s. |
| `GOWORK=off go vet -mod=readonly ./...` | Passed. |
| `./scripts/generate.sh --check` | Passed. |
| `./scripts/check-api.sh` | Passed; Vacuum quality score 100/100. |
| `./scripts/check-lint.sh` | Passed with zero issues; architecture passed. |
| `python3 scripts/check-architecture.py` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases; local links resolve. |
| `GOWORK=off go mod verify` in root, `tools/`, and `ui/` | Passed. |
| `GOWORK=off go test -mod=readonly ./...` and `GOWORK=off go vet -mod=readonly ./...` in `tools/` and `ui/` | Passed. |
| `./scripts/check-guardrails.sh --fast` and product commit hook | Passed. |
| `GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -mod=readonly -run '^$' -exec=true ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| `GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go test -mod=readonly -run '^$' -exec=true ./internal/adapters/jellyfin/read ./internal/adapters/seerr` | Passed. |
| Python JSON parse of `tests/fixtures/catalogs/jellyfin-items.json` | Passed. |

## Acceptance and next step

The round-three correction addresses the remaining A-08/A-55 safety gap and
preserves the previously closed A-09 and A-54 behavior. Independent acceptance
is pending `/root/d01_reviewer`, who should review product commit
`b5594b668185245304f70a0f46c1f77ae7b76d7b` against the round-two receipt.

The coordinator should record the reviewer result and integration state. This
lane did not edit `docs/execution/state.json` or unrelated working-tree files.
