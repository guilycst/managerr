# X-05 independent review, round one

## Decision

`changes_requested`.

X-05 keeps native Arr imports blocked by G-01 and records useful local safety
contracts. It does not yet supply the disposable upstream evidence required by
the task, and its partial Sonarr import probe derives results from handler state
instead of a valid Sonarr read-back. One documented qBittorrent route also
differs from the exercised route.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Reviewed product commit:
  `597e793fa37c4707f066380ecc91b19be5bf19ef`; direct parent
  `a642adcda155f0f7b6044deb11663ef61b708cc3`; product tree
  `e3ad344dcac163e3e895c3ad1a1605b6f24cf90b`.
- Reviewed handoff commit:
  `00fdfcb6e3fc2780b54fe47d724ab0324bae36d9`; direct child of the
  product; tree `615a47dc7a396b12fbeb8df58b8540925ca35954`.
- Product scope: `tests/compatibility/writes/` and
  `docs/research/write-capabilities.md`. Handoff scope:
  `docs/execution/handoffs/X-05.md`.
- Scoped product diff SHA-256:
  `e788e4538017091789dbdf1576765ec8fa03e0830aad2526c395200dfea948e9`.
- Release stage: pre-v0.1.0. Current milestone: X-05 evidence must either prove
  pinned upstream write behavior or keep each affected capability blocked.
- Acceptance reviewed: A-16, A-17, A-28, A-29, A-31 and A-55; G-01 remains
  explicitly open.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-x05-review-r1` at the exact
handoff commit. No producer transcript, live media stack, credential, private
endpoint, release, deployment or product path was used or changed.

## Findings

### P1: Local mocks are recorded as disposable upstream evidence

The X-05 task requires pinned native APIs in disposable upstream instances.
Every new interaction is instead implemented by the test itself with
`httptest` handlers (`writes_test.go:21-25`, `83-105`, `140-155`, `265-335`,
and `494-512`). The handoff confirms native upstream services were not run and
calls that exclusion intentional (`X-05.md:75`). These checks prove local model
behavior, not Radarr, Sonarr, qBittorrent or Jellyfin behavior at a pinned
version.

Four of the five named JSON fixtures are not decoded by any test. The only
`readFixture` call is for `arr-subtitle-anime.json` at
`writes_test.go:573-575`. The Arr race fixture has an empty rejection array,
while the research table says that fixture retains an `ExistingFile`
rejection (`arr-no-overwrite-race.json:5-8` versus
`write-capabilities.md:28`). Passing tests cannot detect changes to those four
evidence files or this contradiction.

Failure mode: completing X-05 satisfies the dependency for X-06, X-07 and X-09
without the versioned native evidence the gate requires. Self-authored server
behavior can silently drift from upstream while downstream code treats the
research rules as frozen.

Required change: either run the probes against pinned disposable upstream
builds with synthetic files and bind every evidence fixture to an executable
check, or keep X-05 incomplete/blocked and state that these are protocol-model
unit contracts only. Do not use this receipt to enable any affected write
capability. Preserve G-01 as open unless separate atomicity evidence closes it.

Disposition: `current_blocker`.

### P1: Partial Sonarr reconciliation ignores the HTTP read-back

`TestArrPartialPackReconcilesPerFile` models `GET /api/v3/series/201` with a
Radarr-style `movieFile` field (`writes_test.go:65-72`, `211-212`). Existing
Sonarr read code uses `GET /api/v3/episode?seriesId=...&includeEpisodeFile=true`
and nested `episodeFile` evidence (`internal/adapters/arr/read/client.go:53-55`,
`2576-2585`, `3065-3075`).

The test then computes per-file outcomes from the local `imported` slice set by
the command handler (`writes_test.go:200`, `209`, `229-239`), not from the
decoded HTTP read-back. It therefore passes even when the read-back cannot
represent the two requested episode files or prove their associations.

Failure mode: A-17 can appear covered while a completed command imports only
part of a pack and Mastarr has no valid per-file evidence for which episode
associations exist.

Required change: model a supported Sonarr observation route and derive observed
paths and episode IDs solely from its decoded response. Exercise a completed
command with one of two expected files absent, preserve the missing item as
unresolved, and verify no aggregate success. Record command/history evidence
separately from file association evidence.

Disposition: `current_blocker`.

### P2: Frozen qBittorrent route name disagrees with the fixture and test

The research table names `POST /api/v2/torrents/remove`
(`write-capabilities.md:32`). The committed fixture and handler use
`POST /api/v2/torrents/delete` (`qbittorrent-scope.json:9-12` and
`writes_test.go:303-319`). The compatibility matrix also identifies delete
semantics rather than a remove route.

Failure mode: X-07 can consume a frozen route description that its own contract
does not expose, producing avoidable contract drift at the control boundary.

Required change: use `/api/v2/torrents/delete` consistently and make the
executable fixture verify the exact route and `deleteFiles=false` value.

Disposition: `current_blocker`.

## Preserved behavior

- G-01 remains open, and the research document does not enable native Arr
  import from a preview or successful command response.
- Local probes separate qBittorrent zero upload speed from stopped state,
  perform read-back after a lost stop response, refuse a resumed state, preserve
  payload bytes for `deleteFiles=false`, and reject a rename collision.
- Jellyfin refresh acceptance remains separate from later item visibility and
  wrong-root mapping.
- Subtitle/anime data preserves explicit episode IDs, absolute numbers,
  forced/SDH/language fields, IDX/SUB pairing and unresolved companions.
- All paths, bytes and IDs are synthetic. No secret, private coordinate, local
  replacement, live mutation or generated-type leakage was found.

## Independent checks

| Check | Result |
| --- | --- |
| Exact product/handoff identity, ancestry, tree and scoped diff | Passed; product changed only assigned X-05 paths and handoff is its direct child. |
| `GOWORK=off go test -mod=readonly -count=20 ./tests/compatibility/writes` | Passed. |
| `GOWORK=off go test -race -mod=readonly -count=1 ./tests/compatibility/writes` | Passed. |
| `GOWORK=off go vet -mod=readonly ./tests/compatibility/writes` | Passed. |
| Fixture-reference scan | Failed evidence binding: only `arr-subtitle-anime.json` is decoded. |
| Sonarr read-back contract comparison | Failed: `/series/{id}` plus `movieFile` conflicts with existing Sonarr episode evidence contract. |
| `./scripts/check-guardrails.sh --fast` | Passed; generation, Vacuum, architecture, focused tests and formatting stayed clean. |
| `./scripts/check-guardrails.sh --ci` | Passed; all module tests, vet, verification, lint and architecture checks completed successfully. |
| `git diff --check` | Passed. |

## Acceptance disposition

- A-16: G-01 remains blocked correctly, but native upstream behavior is not
  proven by local handlers.
- A-17: not accepted for X-05 because the per-file result bypasses the HTTP
  read-back and uses an invalid Sonarr shape.
- A-28, A-29 and A-31: local protocol-model checks pass; runtime qBittorrent
  capability remains unverified because no pinned product build was exercised.
- A-55: local acceptance-versus-availability model passes; runtime refresh
  scope remains unsupported without a versioned endpoint fixture.

No product file, fixture, task definition, shared script or
`docs/execution/state.json` was modified by the reviewer.
