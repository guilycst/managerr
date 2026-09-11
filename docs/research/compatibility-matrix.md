# v0.0.1 compatibility matrix

Task C-00 coordinator evidence, captured 2026-09-11. This document freezes the
source snapshots and candidate API baselines used to design adapters. It does not
claim that a live deployment, a minimum product version, or an unsafe write path
has passed. X-05 must exercise the write rows against disposable upstreams before
the corresponding capability is enabled.

## Source snapshots

| Integration | Source or API baseline | Evidence | Scope frozen for adapter design | Status |
| --- | --- | --- | --- | --- |
| Radarr | Source commit `0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4` | [ManualImportController](https://github.com/Radarr/Radarr/blob/0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4/src/Radarr.Api.V3/ManualImport/ManualImportController.cs), [manual import service](https://github.com/Radarr/Radarr/blob/0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4/src/NzbDrone.Core/MediaFiles/MovieImport/Manual/ManualImportService.cs) | GET manual-import candidate/rejection preview; POST command execution is separate and can construct a fresh import decision. | Read contract observed. No write capability enabled. |
| Sonarr | Source commit `9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb` | [manual import service](https://github.com/Sonarr/Sonarr/blob/9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb/src/NzbDrone.Core/MediaFiles/EpisodeImport/Manual/ManualImportService.cs) | Episode and multi-episode mapping is product-specific. Series, season, episode, quality and language fields remain typed adapter data. | Read contract observed. Subtitle/anime writes require X-05. |
| qBittorrent | WebUI API documentation for 5.0+ | [WebUI API 5.0](https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API-%28qBittorrent-5.0%29) | Stop, state/files/properties, setLocation, renameFile, renameFolder, and delete with `deleteFiles=false`. Display-name rename is not payload rename. | Candidate baseline. Must test the selected product build. |
| NZBGet | API documentation for version 13+ | [API reference](https://nzbget.com/documentation/api/) and [post-processing](https://nzbget.com/documentation/post-processing-scripts/) | JSON-RPC positional parameters, Basic authentication, queue/history, FinalDir/DestDir and processing status. Original NZB export is capability-dependent. | Candidate baseline. History bounds and descriptor export remain open. |
| Seerr | Source commit `68c5bc8c7d8560d295387adeeee73982ea518e8f` | [media route](https://github.com/seerr-team/seerr/blob/68c5bc8c7d8560d295387adeeee73982ea518e8f/server/routes/media.ts), [media-server scan behavior](https://docs.seerr.dev/using-seerr/settings/mediaserver/) | Read media/request pages with `take`, `skip` and page information; preserve native status and eventual Jellyfin scan behavior. | Read-only capability. No request writes. |
| Jellyfin | Public API catalog, exact endpoint TBD | [Jellyfin API](https://api.jellyfin.org/) | Read libraries/items/provider IDs and use an explicitly tested refresh scope. | Version and refresh route need a disposable fixture. |

Source branches and current product versions can change after this record. The
source commits are reproducibility anchors, not a claim that these commits are
the user's installed versions. At implementation start, record product version,
API version, adapter fixture version and test result in a new row rather than
silently replacing this evidence.

## Capability gates

| Gate | Question and test | Result now | Required before enabling |
| --- | --- | --- | --- |
| G-01 / Arr preview binding | Submit a native preview with a rejection, alter the destination or accepted fields, then submit the manual-import command. | Source inspection shows command execution can rebuild an `ImportDecision`; a saved preview is not an atomic compare-and-swap. | X-05 must prove exact-file/no-overwrite behavior with an external race. If no supported coordination exists, block the write or record a product decision for a weaker guarantee. |
| G-02 / Arr completion | Make a multi-episode or partial command succeed/fail and inspect command status, history and every destination file. | Source models exact file/episode fields; aggregate command success is insufficient. | X-05 and X-06 require per-item read-back and unresolved partial state. |
| G-03 / qBittorrent stop | Stop seeding and verify state; use a zero-upload torrent, lost response and external resume. | API routes are documented; HTTP success alone is not desired-state evidence. | X-05/X-07 need read-back stop before payload mutation. |
| G-04 / qBittorrent scope | Select part of a multi-file torrent, rename/move, remove metadata and check unselected bytes. | `deleteFiles=false` gives metadata-only removal; native relocation scope needs testing. | X-05/X-07 must reject scope expansion and preserve unselected payload. |
| G-05 / NZB provenance | Make NZBID, history ID and Arr `drone` identifier differ; prune history; compare FinalDir and DestDir. | Inspected Arr adapter uses `drone` when present, otherwise history item ID. | X-02 fixtures must retain each identifier and report absent descriptor/history as unknown. |
| G-06 / NZB processing | Observe queue, repair/unpack/post-processing failure and success. | Documentation separates total status, unpack/par status and script status. | X-02 must not expose a failed or still-processing download as ready. |
| G-07 / Seerr coverage | Return overlapping pages, a capped response and delayed media-server scan. | Media route exposes page information; scan behavior is eventual. | X-04 must deduplicate, mark partial coverage and never write requests. |
| G-08 / Jellyfin refresh | Call the chosen refresh endpoint and verify later library item/provider ID visibility. | Exact supported endpoint/version not yet selected. | X-04/X-09 disposable fixture and a documented unsupported result. |
| G-09 / subtitle/anime mapping | Preview forced/SDH/language companions, IDX/SUB pairs and absolute-number anime. | Product-specific behavior is not established by current source inspection. | X-03/X-05 require explicit mapping or a review blocker; no silent companion loss. |

## Adapter behavior locked by this evidence

- Inventory adapters are read-only. Missing credentials, incomplete pages, capped
  history or an unavailable endpoint produce `unknown` coverage, never confirmed
  absence.
- Connection ID scopes every upstream identifier. A hash, history ID, Arr numeric
  ID or provider ID from one instance cannot identify an object in another.
- qBittorrent categories/tags remain hints. Managerr does not recategorize or tag
  an item because a review is open.
- NZBGet JSON-RPC calls use positional parameters and bounded response handling.
  The adapter keeps NZBID, history ID, `drone`, FinalDir and DestDir as separate
  fields. It never fabricates an original `.nzb` from history metadata.
- Arr adapters expose native preview evidence but never expose arbitrary command
  JSON. Manual import uses exact typed files and episode IDs only after X-05.
- Seerr has no v0.0.1 mutation port. Jellyfin availability and refresh acceptance
  are separate observations.
- A source-level observation is not an implementation pass. Each enabled write
  capability needs disposable upstream evidence, effect counts, lost-response
  reconciliation and an independent review.

## Recheck record

The coordinator rechecked the public qBittorrent wiki, NZBGet API/post-processing
documentation, Seerr media route/docs, Radarr source, Sonarr source and the
Goshtoso release catalog on 2026-09-11. The inspected local source snapshots were
the commits listed above. No credentials, real media, tracker metadata or live
homelab endpoints were used. The next recheck occurs at implementation freeze,
before X-05, or when an upstream capability test fails.
