# X-05 independent review, round three

## Decision

`approved`.

Round three closes both remaining round-two findings. The qBittorrent probe
independently pins `state=stalledUP` and `upspeed=0`, the Jellyfin probe
independently pins HTTP 202, and the Sonarr history model uses the supported
`downloadFolderImported` event. The corrected tests retain separate fixture
validation and decoded wire-response assertions.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Reviewed correction product:
  `165f22c2d81c4123d2ab31cf00d7613cdba87054`; direct parent
  `484419e329bbcdcd4d768590c889c118a683ecf6`; product tree
  `8704840df5fef66f95a3c183b2f4814c5afb5789`.
- Reviewed correction handoff:
  `949b07d032f5d7bd36055388ffac17fe1be84007`; direct child of the
  product; tree `82204fb91e661e6b4b1de3532e969efbd2fdab55`.
- Original product: `597e793fa37c4707f066380ecc91b19be5bf19ef`.
- Prior correction product:
  `ca85b60f4c4bf9dd4dcf130e5075ef163316d513`.
- Prior review receipts:
  `2fcd872c255757bb61731c298fdf2e9b150b1d20` and
  `1e0b5689f6f24bed067e47e1d55ab1a7b0df2412`.
- Correction scope: `tests/compatibility/writes/` and
  `docs/research/write-capabilities.md`; handoff scope:
  `docs/execution/handoffs/X-05-correction-round3.md`.
- Scoped correction diff SHA-256:
  `ce8ca07008b602e166ce5b3ded2cf1386a18417abbdf75969d12cd8a0477f6ff`.
- Release stage: pre-v0.1.0. These local protocol models constrain later
  adapters; they do not enable runtime write capabilities.
- Acceptance reviewed: A-16, A-17, A-28, A-29, A-31 and A-55. G-01 remains
  open.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-x05-review-r3` at the exact
handoff commit. No producer transcript, live upstream, credential, private
coordinate, release, deployment or product path was used or changed.

## Round-two closure

- qBittorrent literal binding: closed. Fixture validation requires the exact
  `stalledUP` state and zero upload speed before handler construction. The HTTP
  response is separately decoded and checked against those literals.
- Jellyfin literal binding: closed. Fixture validation requires
  `http.StatusAccepted`, and the independent response assertion also requires
  `http.StatusAccepted`.
- Sonarr history enum: closed. The handler and decoded response assertion use
  `downloadFolderImported`, while decoded episode/file read-back remains the
  authority for per-file outcomes.

## Regression review

- All five synthetic JSON fixtures remain decoded and referenced by their
  corresponding probes.
- Sonarr uses
  `/api/v3/episode?seriesId=201&includeEpisodeFile=true`; per-file observed and
  unresolved outcomes are derived from the decoded episode-file read-back.
  Command completion and history evidence remain separate.
- qBittorrent metadata removal uses `/api/v2/torrents/delete`, and the handler
  accepts only the literal form value `deleteFiles=false`. Synthetic payload
  bytes remain after the torrent record is removed.
- Lost stop responses trigger read-only reconciliation. A subsequent external
  resume blocks payload mutation, and the stop request is not retried blindly.
- Whole-torrent scope, destination-collision refusal, explicit subtitle/anime
  association, duplicate-episode rejection and unresolved companion behavior
  remain covered.
- Research consistently labels the handlers as local protocol models. G-01 is
  explicitly open, X-06 native Arr writes remain disabled, and Arr,
  qBittorrent, Jellyfin and subtitle runtime writes remain blocked pending
  version-pinned native evidence.

## Independent checks

| Check | Result |
| --- | --- |
| Exact correction/handoff identity, ancestry, tree, scope and diff | Passed; correction changes only assigned X-05 product paths and handoff is its direct child. |
| `GOWORK=off go test -mod=readonly -count=25 ./tests/compatibility/writes` | Passed. |
| `GOWORK=off go test -race -mod=readonly -count=5 ./tests/compatibility/writes` | Passed. |
| `GOWORK=off go vet -mod=readonly ./tests/compatibility/writes` | Passed. |
| Literal and route assertion scan | Passed for `stalledUP`, zero upload speed, HTTP 202, `downloadFolderImported`, `/api/v2/torrents/delete` and `deleteFiles=false`. |
| Fixture decode/reference and read-back scan | Passed for all five fixtures and the supported Sonarr episode-file route. |
| `./scripts/check-guardrails.sh --ci` | Passed; generation, Vacuum, lint, architecture and root/UI/tools/client module checks completed. |
| `git diff --check` | Passed. |

## Acceptance disposition

- A-16: the no-overwrite race remains reproduced and blocked by open G-01;
  native import remains disabled.
- A-17: command, native history event and decoded per-file read-back remain
  distinct; partial results do not become aggregate success.
- A-28: zero-speed seeding, lost-response reconciliation and external-resume
  refusal are pinned and pass.
- A-29 and A-31: scope refusal, collision behavior, exact path read-back and
  metadata-only deletion pass in the local model; runtime control stays
  blocked.
- A-55: HTTP 202 command acceptance remains distinct from later Jellyfin
  availability.

No product file, fixture, task definition, shared script or
`docs/execution/state.json` was modified by the reviewer.
