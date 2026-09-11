# C-00 independent review

## Review identity

- Decision: `changes_requested`
- Release stage: `pre-v0.1.0`, target release `v0.0.1`
- Reviewed implementation commit: `7ed5f6c46aeaf2678f93dc19d34bd3052c886e4b`
- Base commit: `ef4fbc3668e3f021b24c8e6c0e7ecbe0704b5bd0`
- Coordinator checkpoint: `1bf670e4c1c749c141c5f0a46bfd9cde0bcfd082`
- Review boundary: committed C-00 files only. No producer transcript, live stack,
  upstream write, media operation, or private runtime data was used.
- Review round: 1

## Findings

### P1: Capability rows reuse authoritative gate IDs with different meanings

Disposition: `current_blocker`.

Evidence: `docs/research/compatibility-matrix.md:31-38` names Arr completion as
G-02, qBittorrent stop as G-03, qBittorrent scope as G-04, NZB provenance as
G-05, and NZB processing as G-06. `docs/architecture/decisions.md:48-53`
already defines G-02 as the upstream version/field gate, G-03 as SQLite and
filesystem compatibility, G-04 as Goshtoso compatibility, G-05 as private
deployment coordinates, and G-06 as future authentication. `state.json` tracks
that authoritative G-01 through G-06 set.

Failure mode: a later task or state update cannot tell whether G-05 means NZB
provenance or deployment coordinates. G-07 through G-09 also create a second
unregistered gate namespace.

Required change: keep G-01 through G-06 meanings from
`docs/architecture/decisions.md`. Put per-integration questions under G-02 with
distinct, non-colliding capability IDs. Coordinator must make any required
authoritative decision or state changes.

Proof: planning check passes with every matrix gate resolving to one unique
authoritative meaning.

### P1: Core version and schema matrix is not frozen

Disposition: `current_blocker`.

Evidence: C-00 requires candidate supported upstream versions and public schemas.
`docs/research/upstream-evidence.md:69-74` further requires product version, API
version, capability, evidence source, fixture, and PASS/FAIL/UNKNOWN. The new
matrix has no fixture column or normalized result and does not select exact
product/API versions. Radarr, Sonarr, and Seerr use untagged branch snapshots
rather than candidate releases. qBittorrent uses the mutable 5.0 wiki as `5.0+`,
NZBGet uses mutable documentation as `13+`, and Jellyfin remains `TBD` at
`docs/research/compatibility-matrix.md:13-18`. The qBittorrent wiki HEAD observed
during review was `485b0ee5e6f7e037f301500c25b23833569a2266` but the matrix does not pin it.
The handoff also says limits were recorded, while exact Seerr defaults and source
for request pagination are absent from the matrix.

Failure mode: adapter authors cannot reproduce the claimed baseline or determine
which exact product/API combination is candidate-supported, failed, or blocked.
Mutable documentation can change without changing this repository.

Required change: add exact candidate product release and API/schema snapshot
identity per capability, fixture ownership, and PASS/FAIL/UNKNOWN. If no candidate
can be selected yet, record UNKNOWN and block that capability. Add direct immutable
evidence for Seerr request pagination and Arr/NZBGet claims rather than relying on
an adjacent planning document.

Proof: every C-00 capability row has reproducible source identity and an explicit
result; no range such as `5.0+` or `13+` acts as a version pin.

### P1: NZBGet provenance test requires two aliases to differ

Disposition: `current_blocker`.

Evidence: `docs/research/compatibility-matrix.md:34` requires NZBID and history ID
to differ, and line 50 treats them as separate fields. NZBGet's public `history`
API defines `NZBID` as the NZB file ID and `ID` as a deprecated alias of `NZBID`.
The inspected Radarr source models history `Id` and falls back to that value only
when the `drone` parameter is absent. It does not provide an independent history
identifier.

Failure mode: X-02 receives an impossible fixture requirement and may invent a
second upstream identity, breaking provenance correlation.

Required change: document NZBGet's `ID`/`NZBID` alias relationship and distinguish
queue/history lifecycle observations from identifiers. Test a differing `drone`
value, history pruning, and FinalDir/DestDir fallback without fabricating an
independent history ID. Coordinator must reconcile the same incorrect premise in
A-05 and connector planning.

Proof: fixture contract matches the pinned NZBGet schema and no test requires
deprecated `ID` to differ from `NZBID`.

## Evidence and checks

| Command or inspection | Result |
| --- | --- |
| `git show --format=fuller --no-patch 7ed5f6c46aeaf2678f93dc19d34bd3052c886e4b` and `git diff --name-status ef4fbc3668e3f021b24c8e6c0e7ecbe0704b5bd0 7ed5f6c46aeaf2678f93dc19d34bd3052c886e4b` | Exact reviewed identity verified; three C-00 artifacts added, plus coordinator-owned state change. |
| `git diff --check ef4fbc3668e3f021b24c8e6c0e7ecbe0704b5bd0 7ed5f6c46aeaf2678f93dc19d34bd3052c886e4b` | Passed. |
| `python3 scripts/check_planning.py` | Passed: 38 tasks, 60 acceptance cases, local links resolve. This does not validate source semantics. |
| `gh api` reads of pinned Radarr and Sonarr manual-import services | Confirmed command execution constructs fresh `ImportDecision` objects without carrying preview rejections. G-01 remains blocked correctly. |
| qBittorrent 5.0 wiki inspection | Confirmed stop, metadata-only delete with `deleteFiles=false`, setLocation, display rename, renameFile, and renameFolder. Scope/read-back gates are appropriate. |
| NZBGet API, post-processing docs, and pinned Radarr adapter inspection | Confirmed positional JSON-RPC, processing statuses, FinalDir/DestDir fallback, `drone` fallback, and the `ID`/`NZBID` alias contradiction above. |
| Pinned Seerr `media.ts` and `request.ts` inspection | Confirmed separate take/skip pagination and pageInfo for media and requests. Matrix links only media source. No Seerr write is enabled. |
| Jellyfin v12.0 source inspection | Confirmed authenticated item reads with provider fields and elevated `POST /Items/{itemId}/Refresh`; queued refresh is not later availability. Matrix correctly leaves route/version support unknown, but C-00 has not selected a candidate baseline. |
| Secret-pattern scan of all three reviewed artifacts | Passed. No credential value, private hostname, tracker URL, real inventory, or user path found. |
| `scripts/snapshot_git_review.py` | Not run: repository does not contain the helper. `git diff --exit-code 7ed5f6c46aeaf2678f93dc19d34bd3052c886e4b -- docs/research/compatibility-matrix.md tests/compatibility/research/README.md docs/execution/handoffs/C-00.md` and SHA-256 hashes confirmed reviewed C-00 artifacts stayed unchanged during review. |

## Acceptance contribution assessment

- A-16: source evidence supports the native Arr no-overwrite blocker. Contribution
  is technically sound but gate naming must be fixed.
- A-31: qBittorrent operations and scope uncertainty are identified. Exact tested
  product/API version remains missing.
- A-54: Seerr read-only and eventual-state boundaries are preserved. Direct request
  route evidence and exact candidate version remain missing.
- A-55: refresh acceptance and later availability remain separate; unsupported
  scope is blocked. Exact Jellyfin candidate version and endpoint are still unset.
- Public-secret hygiene: approved.

## Decision and next event

`changes_requested`. Fix all three P1 findings in the same C-00 target, keep writes
disabled, then provide a new immutable commit for round 2. No deferred hardening
items were opened; findings concern the current C-00 deliverable and source truth.
