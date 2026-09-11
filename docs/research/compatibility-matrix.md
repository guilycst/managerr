# v0.0.1 compatibility matrix

Task C-00 coordinator correction, captured 2026-09-11. Each row carries a
reproducibility identity, fixture reference and a `PASS`, `FAIL`, or `UNKNOWN`
result. `PASS` means the source fact was observed. It does not mean a product
release or a write capability is approved. A moving branch, mutable wiki or
unselected product version remains `UNKNOWN` and blocks the affected capability.

Global gates `G-01` through `G-06` keep their meanings in
[decisions](../architecture/decisions.md). Integration-specific capability IDs
below use the `CAP-*` namespace and must not be copied into execution state as
global gates.

## Source and capability rows

| Capability | Product/version candidate | API/schema identity | Immutable evidence | Fixture | Result | Enablement |
| --- | --- | --- | --- | --- | --- | --- |
| `CAP-ARR-PREVIEW` | Radarr release `UNKNOWN`; inspected source snapshot only | Radarr v3 manual-import controller/service at source commit `0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4` | [controller](https://github.com/Radarr/Radarr/blob/0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4/src/Radarr.Api.V3/ManualImport/ManualImportController.cs), [service](https://github.com/Radarr/Radarr/blob/0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4/src/NzbDrone.Core/MediaFiles/MovieImport/Manual/ManualImportService.cs) | `none`, source inspection | PASS for route separation; UNKNOWN for release compatibility | Read adapter only until X-05 |
| `CAP-SONARR-PREVIEW` | Sonarr release `UNKNOWN`; inspected source snapshot only | Sonarr manual episode-import service at source commit `9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb` | [service](https://github.com/Sonarr/Sonarr/blob/9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb/src/NzbDrone.Core/MediaFiles/EpisodeImport/Manual/ManualImportService.cs) | `none`, source inspection | PASS for typed episode fields; UNKNOWN for release and mapping behavior | Read adapter only until X-05 |
| `CAP-ARR-NO-OVERWRITE` | Radarr/Sonarr release `UNKNOWN` | Native manual-import command path, same source identities above | Same immutable source links | `none` | UNKNOWN | X-05 must prove exact-file/no-overwrite under an external race or block writes |
| `CAP-QBT-STOP` | qBittorrent product build `UNKNOWN` | WebUI API document revision `485b0ee5e6f7e037f301500c25b23833569a2266`, API family documented for 5.0+ | [wiki revision](https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API-(qBittorrent-5.0)?oldformat=true) | `none`, source inspection | PASS for documented routes; UNKNOWN for selected build | X-05/X-07 must verify stop read-back |
| `CAP-QBT-SCOPE` | qBittorrent product build `UNKNOWN` | Same WebUI API document identity; `deleteFiles=false`, setLocation, renameFile, renameFolder | [wiki revision](https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API-(qBittorrent-5.0)?oldformat=true) | `none`, source inspection | PASS for flag semantics; UNKNOWN for multi-file relocation scope | X-05/X-07 must reject scope expansion |
| `CAP-NZB-PROVENANCE` | NZBGet product build `UNKNOWN` | JSON-RPC API document captured 2026-09-11; `ID` is a deprecated alias of `NZBID` | [API reference](https://nzbget.com/documentation/api/) and inspected Radarr adapter source | `none`, source inspection | PASS for alias/correlation rule; UNKNOWN for selected build | X-02 must vary `drone`, not invent a second history ID |
| `CAP-NZB-PROCESSING` | NZBGet product build `UNKNOWN` | Queue/history/post-processing status fields, document captured 2026-09-11 | [post-processing](https://nzbget.com/documentation/post-processing-scripts/) | `none`, source inspection | PASS for status distinction; UNKNOWN for selected build | X-02 must block while processing/failed |
| `CAP-SEERR-MEDIA-PAGE` | Seerr release `UNKNOWN`; inspected source snapshot only | `server/routes/media.ts` at source commit `68c5bc8c7d8560d295387adeeee73982ea518e8f`, `take`/`skip`/pageInfo | [media route](https://github.com/seerr-team/seerr/blob/68c5bc8c7d8560d295387adeeee73982ea518e8f/server/routes/media.ts) | `none`, source inspection | PASS for route shape; UNKNOWN for release compatibility | X-04 read-only adapter; pagination fixture required |
| `CAP-SEERR-REQUEST-PAGE` | Seerr release `UNKNOWN`; inspected source snapshot only | `server/routes/request.ts` at source commit `68c5bc8c7d8560d295387adeeee73982ea518e8f`, `take`/`skip`/pageInfo | [request route](https://github.com/seerr-team/seerr/blob/68c5bc8c7d8560d295387adeeee73982ea518e8f/server/routes/request.ts) | `none`, source inspection | PASS for route shape; UNKNOWN for release compatibility | X-04 must preserve partial coverage and native status |
| `CAP-JELLYFIN-READ` | Jellyfin product/API release `UNKNOWN` | Public OpenAPI/API catalog, exact version and fixture not selected | [API catalog](https://api.jellyfin.org/) | `none`, no disposable server yet | UNKNOWN | X-04 blocks until a versioned read fixture exists |
| `CAP-JELLYFIN-REFRESH` | Jellyfin product/API release `UNKNOWN` | Refresh route/scope not frozen; accepted refresh is not availability | [API catalog](https://api.jellyfin.org/) | `none`, no disposable server yet | UNKNOWN | X-09 blocked until endpoint/version test |
| `CAP-SUBTITLE-ANIME` | Arr releases `UNKNOWN` | Subtitle companion and absolute-number mapping are product-specific | Arr source snapshots above; no immutable behavior fixture | `none` | UNKNOWN | X-05 requires explicit mapping or review blocker |

The qBittorrent wiki identity is recorded as the revision observed by the
reviewer. If the upstream does not expose a stable revision to the adapter, the
row remains source evidence only and the selected product build must be pinned by
the disposable fixture before enablement. `UNKNOWN` is a safe result, not a
reason to guess.

## Capability gates and required tests

| Capability | Test | Current result | Required evidence |
| --- | --- | --- | --- |
| `CAP-ARR-PREVIEW` / `CAP-ARR-NO-OVERWRITE` | Submit a native preview with a rejection, change destination or fields, then submit the manual-import command. | Source inspection shows command execution can rebuild an import decision; preview is not an atomic compare-and-swap. | X-05 must prove exact-file/no-overwrite under an external race. If unsupported, block native write or record a product decision for a weaker guarantee. |
| `CAP-ARR-NO-OVERWRITE` | Execute a partial pack and inspect command, history, every destination and source. | UNKNOWN. | X-05/X-06 record per-file effects and unresolved items. |
| `CAP-QBT-STOP` | Stop a seeding torrent and verify state, including zero-upload, lost response and external resume. | Routes observed; desired state unknown. | X-05/X-07 read back stopped state before payload mutation. |
| `CAP-QBT-SCOPE` | Select part of a multi-file torrent, relocate/rename, remove metadata and inspect unselected bytes. | `deleteFiles=false` is documented as metadata-only; native scope unknown. | X-05/X-07 reject expansion and preserve unselected payload. |
| `CAP-NZB-PROVENANCE` | Vary `drone` from NZBID, prune history, compare FinalDir/DestDir and keep `ID` equal to its deprecated NZBID alias. | Correlation rule observed; selected build unknown. | X-02 fixture must distinguish lifecycle observations from identifiers. |
| `CAP-NZB-PROCESSING` | Exercise queue, par/repair, unpack and post-processing failures/success. | Status categories observed; selected build unknown. | X-02 never marks processing/failed download ready. |
| `CAP-SEERR-MEDIA-PAGE` / `CAP-SEERR-REQUEST-PAGE` | Return overlapping pages, a capped response and delayed media-server scan. | Both route shapes observed; selected build unknown. | X-04 deduplicates, marks partial coverage and performs no writes. |
| `CAP-JELLYFIN-READ` / `CAP-JELLYFIN-REFRESH` | Read provider IDs, call chosen refresh route, then verify later library visibility. | UNKNOWN. | X-04/X-09 disposable versioned fixture or an explicit unsupported result. |
| `CAP-SUBTITLE-ANIME` | Preview forced/SDH/language companions, IDX/SUB pairs and absolute-number anime. | UNKNOWN. | X-03/X-05 require explicit mapping or a review blocker. |

## Adapter rules from evidence

- Inventory is read-only. Missing credentials, incomplete pages, capped history
  and unavailable endpoints yield `unknown`, never confirmed absence.
- Every upstream ID is scoped by connection ID. A hash, NZBID, Arr numeric ID or
  provider ID from one instance cannot identify an object in another.
- qBittorrent categories/tags remain hints. Managerr never recategorizes or tags
  an item because a review is open.
- NZBGet JSON-RPC uses positional parameters and bounded responses. Keep NZBID,
  deprecated ID alias, Arr `drone`, FinalDir and DestDir as separate typed fields.
  Never fabricate an original `.nzb` from history metadata.
- Arr adapters expose native preview evidence but never arbitrary command JSON.
  Manual import uses exact typed files and episode IDs only after X-05.
- Seerr has no v0.0.1 mutation port. Jellyfin refresh acceptance and availability
  remain independent observations.
- A source observation is not a write pass. Enabled mutation needs a pinned
  disposable fixture, effect counts, lost-response reconciliation and independent
  review.

## Recheck record

The coordinator and reviewer rechecked qBittorrent, NZBGet, Seerr, Radarr, Sonarr,
Jellyfin and Goshtoso public sources on 2026-09-11. Local source snapshots are
identified above. No credentials, real media, tracker metadata or live homelab
endpoints were used. Recheck this matrix at implementation freeze, before X-05,
or whenever a capability test fails.
