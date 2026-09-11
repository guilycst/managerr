# C-00 independent review, round 2

## Review identity

- Decision: `changes_requested`
- Release stage: `pre-v0.1.0`, target release `v0.0.1`
- Reviewed correction commit: `ed333625993c14ae499a6407171986155683b5fc`
- Correction parent: `6067b160fb5c4c883c10e455e48b8afe65b6db5a`
- Coordinator checkpoint: `d1373dd4a722048fcd0636d2bac7b38d6dd6e43b`
- Review round: 2
- Current milestone: freeze upstream evidence and capability blockers before C-01;
  no mutation capability or live-stack operation is enabled by C-00.
- Review boundary: committed correction and checkpoint evidence only. No producer
  transcript, live media stack, upstream write, media operation, credential, or
  private runtime data was used.

## Finding

### P1: Matrix rows still lack a reproducible fixture and one normalized result

Disposition: `current_blocker`.

Evidence: `docs/research/compatibility-matrix.md:3-7` says every row has a
reproducibility identity, fixture reference, and a `PASS`, `FAIL`, or `UNKNOWN`
result. Lines 18-29 instead use `none` for every fixture. Eight rows combine
`PASS` and `UNKNOWN` in one Result cell, so the capability has no single result.
The NZBGet rows identify mutable documentation only by capture date and refer to
an inspected Radarr adapter without a path, commit, or link. That does not meet
the matrix contract in `docs/research/upstream-evidence.md:69-70`.

The qBittorrent identity is reproducible as a wiki repository snapshot, but it is
described as a WebUI API document revision and linked to the mutable rendered
page. Independent Git inspection found `485b0ee5e6f7e037f301500c25b23833569a2266`
is the wiki repository HEAD commit with subject `Add Catthode to known themes`.
The API page's latest modifying commit is
`cc6579ab58f534d03b40ae0deba5755c4a342319`, and its blob at the recorded
repository snapshot is `82f67b6f5aa73ef54d4c88dcd38c695ae1191b72`.

Failure mode: X-01 through X-05 cannot bind a fixture or review receipt to each
row's exact evidence bytes, and a consumer cannot determine whether a mixed
`PASS`/`UNKNOWN` row is enabled. Mutable documentation can change while the
matrix still appears to describe the same baseline.

Required change: give each row an exact fixture identifier/path and status, or an
explicit planned fixture owner/path with capability result `UNKNOWN` and blocked
enablement. Use one normalized capability result per row; keep source-observation
results in a separate field. Pin every source-backed fact to exact bytes or an
immutable source path and commit, including NZBGet and the Radarr NZBGet adapter.
Describe `485b0ee5...` as a wiki repository snapshot and identify the API page by
its immutable page commit or blob. Equivalent evidence preserving these
invariants is acceptable.

Proof: every capability row has a unique `CAP-*` ID, exact evidence identity,
fixture path/status, and one `PASS`, `FAIL`, or `UNKNOWN` capability result. Every
mutable, unselected, or missing-fixture baseline is `UNKNOWN` and explicitly
blocks affected enablement.

## Corrected findings that passed

- Namespace: `CAP-*` rows do not collide with authoritative `G-01` through
  `G-06`. `docs/architecture/decisions.md:48-53` and `state.json` retain the same
  six global meanings; no capability ID appears in `openGates`.
- NZBGet: `ID` is correctly treated as the deprecated alias of `NZBID`.
  A-05, connector prose, and the matrix now vary Arr's `drone` parameter and keep
  queue/history lifecycle plus FinalDir/DestDir evidence separate.
- Seerr: media and request rows link directly to source commit
  `68c5bc8c7d8560d295387adeeee73982ea518e8f`. Both routes independently use
  `take`, `skip`, and `pageInfo`. A-54 remains read-only and preserves native
  status and partial coverage.
- Arr: exact source inspection confirms command execution constructs fresh
  `ImportDecision` values without preview rejections. G-01 and A-16 correctly
  block unsafe writes until X-05 proves the external-race guarantee or a product
  decision changes the promise.
- qBittorrent behavior: recorded snapshot contains stop, setLocation,
  renameFile, renameFolder, and metadata-only deletion with
  `deleteFiles=false`. A-31 still requires path/state read-back and blocks scope
  expansion; only the source identity wording remains in the P1 above.
- Jellyfin: refresh acceptance and later availability remain separate. Unknown
  endpoint/version support blocks A-55 capability enablement.
- Public-secret hygiene: no credential value, cookie, private hostname, tracker
  URL, real inventory, or user-specific runtime path was found.

## Commands and evidence

| Command or inspection | Result |
| --- | --- |
| `git show -s --format=... ed333625993c14ae499a6407171986155683b5fc` and checkpoint inspection | Exact correction, parent, and direct-child checkpoint verified. |
| `git diff --binary --full-index 6067b160fb5c4c883c10e455e48b8afe65b6db5a ed333625993c14ae499a6407171986155683b5fc --` | Four corrected documentation files inspected in full. |
| `git diff --exit-code ed333625993c14ae499a6407171986155683b5fc d1373dd4a722048fcd0636d2bac7b38d6dd6e43b -- <C-00 files>` | C-00 correction files unchanged by coordinator checkpoint. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, local links resolve. Semantic evidence issue above remains outside this validator. |
| `git diff --check 6067b160fb5c4c883c10e455e48b8afe65b6db5a ed333625993c14ae499a6407171986155683b5fc` | Passed. |
| `git ls-remote https://github.com/qbittorrent/qBittorrent.wiki.git HEAD` plus independent bare-clone `git log`, `ls-tree`, and `rev-parse` | Verified repository snapshot `485b0ee5...`, page commit `cc6579ab...`, page blob `82f67b6f...`, and route/flag contents. |
| Authenticated `gh api` reads at pinned Radarr, Sonarr, and Seerr commits | Immutable paths resolve. Arr command rebuild, Seerr media/request pagination, and Radarr `drone` fallback inspected directly. |
| NZBGet public `history` schema and pinned Radarr NZBGet adapter inspection | Confirmed `ID` is deprecated in favor of `NZBID`; `drone` overrides the fallback ID; FinalDir falls back to DestDir. |
| HTTP status checks for cited Radarr, Sonarr, Seerr, NZBGet, and Jellyfin links | All returned HTTP 200. |
| Focused secret-pattern scan of four corrected files | Passed; matches were only policy text such as `tracker metadata`. |
| SHA-256 and Git identity checks before handoff write | Reviewed C-00 files matched `ed33362` and stayed unchanged throughout read-only inspection. Repository lacks `scripts/snapshot_git_review.py`. |

## Decision and next event

`changes_requested`. Resolve the single P1 on the same C-00 target, freeze a new
commit, and request another independent review. No deferred-hardening item was
opened. No producer transcript polling occurred.
