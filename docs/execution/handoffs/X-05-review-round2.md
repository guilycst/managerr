# X-05 independent review, round two

## Decision

`changes_requested`.

Round two closes the three structural findings from round one: product-version
claims are narrowed to blocked protocol models, every fixture file is decoded,
Sonarr file results come from the episode read-back, and qBittorrent deletion
uses the exact `/delete` route with explicit `deleteFiles=false`. Two remaining
wire assertions can still pass after the fixture stops representing the stated
acceptance case.

## Review identity and scope

- Reviewer: `/root/x05_reviewer`, independent of product author
  `/root/x05_implementer`.
- Reviewed correction product:
  `ca85b60f4c4bf9dd4dcf130e5075ef163316d513`; direct parent
  `93420af525440b8ad81df31af1b22f9276d89fb2`; product tree
  `dd33187a2d5deb8a6a69386ab61993f538d6f854`.
- Reviewed correction handoff:
  `5587ab87827d848d6de1b36391d54476f1beed3e`; direct child of the
  product; tree `2a995b1ce4ba7fe7bc077e7e755ea89043f6f256`.
- Original product: `597e793fa37c4707f066380ecc91b19be5bf19ef`.
- Prior review receipt: `2fcd872c255757bb61731c298fdf2e9b150b1d20`.
- Correction scope: `tests/compatibility/writes/` and
  `docs/research/write-capabilities.md`; handoff scope:
  `docs/execution/handoffs/X-05-correction-round2.md`.
- Scoped correction diff SHA-256:
  `b886c153900d0d4f1f538a8cda534dd9903ffa6112a22999b377fc25eba4f563`.
- Release stage: pre-v0.1.0. Current milestone: executable protocol contracts
  may guide later adapters, but no runtime write capability is enabled without
  version-pinned native evidence.
- Acceptance reviewed: A-16, A-17, A-28, A-29, A-31 and A-55. G-01 remains
  open.

Review ran in clean detached worktree
`/Users/guilhermecastro/.codex/worktrees/managerr-x05-review-r2` at the exact
handoff commit. No producer transcript, live upstream, credential, private
coordinate, release, deployment or product path was used or changed.

## Round-one closure

- Native evidence boundary: closed. Research now calls every handler a local
  protocol model, says no pinned upstream process or image ran, and blocks each
  runtime capability pending versioned evidence.
- Fixture binding: partially closed. All five files are decoded and their main
  route/mapping fields are used, but two acceptance-defining values are compared
  only with values produced from the same fixture.
- Sonarr per-file evidence: closed. The test uses
  `/api/v3/episode?seriesId=201&includeEpisodeFile=true`, verifies nested file
  identity, derives observed paths and episode IDs from decoded HTTP data, and
  keeps command and history records separate.
- qBittorrent route drift: closed. Research, fixture, handler and test use
  `/api/v2/torrents/delete`; the handler rejects every value except the literal
  form value `false`, and payload bytes remain after the record disappears.

## Findings

### P2: Acceptance-defining fixture values remain self-fulfilling

`TestQBTZeroUploadIsNotStopped` initializes the handler from
`spec.Initial.UpSpeed`, reads that same value back, then compares it with
`spec.Initial.UpSpeed` (`writes_test.go:474-486`). It never asserts zero. The
initial state is only required to be non-empty and non-stopped, so changing
`stalledUP` to an unrelated active state also passes. A-28 and the research row
specifically claim a zero-speed `stalledUP` seeding case.

The Jellyfin handler writes `spec.AcceptedStatus`, and the client compares the
response with the same field (`writes_test.go:652-687`). Validation requires
only a nonzero status. Changing the fixture from `202` to another nonzero code
therefore passes while `write-capabilities.md:39` still states that the model
returns the fixture's `202`.

Failure mode: the committed evidence can drift away from the exact zero-speed
and 202-accepted cases while every check remains green, contradicting the new
claim that fixture drift is visible.

Required change: assert `upspeed == 0`, assert the selected seeding state used by
the fixture, and assert `acceptedStatus == 202` before constructing handlers.
Keep the handler/client comparisons as separate wire checks.

Disposition: `current_blocker`.

### P2: Sonarr history uses a value outside the pinned API enum

The corrected test returns and accepts `eventType: "Downloaded"`
(`writes_test.go:305-312`, `343-348`). The exact pinned Sonarr OpenAPI document
used by this repository defines `EpisodeHistoryEventType` as `unknown`,
`grabbed`, `seriesFolderImported`, `downloadFolderImported`, `downloadFailed`,
`episodeFileDeleted`, `episodeFileRenamed`, or `downloadIgnored`. `Downloaded`
is not a valid native value.

Evidence: Sonarr OpenAPI at pinned commit
`9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb`, schema
`EpisodeHistoryEventType`:
https://raw.githubusercontent.com/Sonarr/Sonarr/9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb/src/Sonarr.Api.V3/openapi.json

Failure mode: the protocol model labels an impossible record as independent
import evidence. A later adapter can copy that assumption and fail to recognize
the actual import event required by A-17.

Required change: use and assert the supported native import event
`downloadFolderImported` for this command result. Keep history as supporting
provenance; decoded episode/file association remains the per-file authority.

Disposition: `current_blocker`.

## Preserved behavior

- G-01 remains explicitly open and X-06 native Arr writes remain disabled.
- Local handlers are described as protocol models. qBittorrent, Jellyfin,
  subtitle/anime and Arr runtime capabilities stay blocked pending native,
  versioned evidence.
- Lost stop responses trigger read-only state reconciliation; external resume
  prevents payload mutation and no second stop is sent.
- Partial import results use decoded Sonarr episode evidence and leave the
  missing episode file unresolved without aggregate success.
- All paths, bytes, hashes, IDs and endpoints are public synthetic fixtures.

## Independent checks

| Check | Result |
| --- | --- |
| Exact correction/handoff identity, ancestry, tree, scope and diff | Passed; correction changes only assigned X-05 product paths and handoff is its direct child. |
| `GOWORK=off go test -mod=readonly -count=20 ./tests/compatibility/writes` | Passed. |
| `GOWORK=off go test -race -mod=readonly -count=5 ./tests/compatibility/writes` | Passed. |
| `GOWORK=off go vet -mod=readonly ./tests/compatibility/writes` | Passed. |
| Fixture decode/reference scan | Passed for all five JSON files. Behavior-value binding remains incomplete as recorded above. |
| Pinned Sonarr OpenAPI enum comparison | Failed for `Downloaded`; accepted native import value is `downloadFolderImported`. |
| `./scripts/check-guardrails.sh --ci` | Passed; generation, Vacuum, lint, architecture and root/UI/tools/client module checks completed. |
| `git diff --check` | Passed. |

## Acceptance disposition

- A-16: protocol model and G-01 blocking remain correct; native no-overwrite is
  unproven and disabled.
- A-17: per-file read-back mechanics pass, but the native history event fixture
  remains invalid.
- A-28: lost-response and external-resume flow passes; exact zero-speed fixture
  binding needs correction.
- A-29 and A-31: local scope, metadata-only deletion, path read-back and
  collision models pass; runtime capability remains blocked.
- A-55: acceptance and availability remain separate, but the stated 202 fixture
  is not independently pinned by the test.

No product file, fixture, task definition, shared script or
`docs/execution/state.json` was modified by the reviewer.
