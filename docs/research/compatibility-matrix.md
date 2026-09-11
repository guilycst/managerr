# v0.0.1 compatibility matrix

Task C-00 coordinator correction, captured 2026-09-11. Each row carries a
reproducibility identity, fixture reference and a `PASS`, `FAIL`, or `UNKNOWN`
result. `PASS` means the capability passed its versioned fixture. Source
observations stay in a separate column and never enable a capability. A moving
branch, mutable wiki, unselected product version or missing fixture remains
`UNKNOWN` and blocks the affected capability.

Global gates `G-01` through `G-06` keep their meanings in
[decisions](../architecture/decisions.md). Integration-specific capability IDs
below use the `CAP-*` namespace and must not be copied into execution state as
global gates.

## Source and capability rows

| Capability | Product/version candidate | API/schema identity | Immutable evidence | Fixture | Observation | Result | Enablement |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `CAP-ARR-PREVIEW` | Radarr release `UNKNOWN`; inspected source snapshot only | Radarr v3 manual-import controller/service at source commit `0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4` | [controller](https://github.com/Radarr/Radarr/blob/0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4/src/Radarr.Api.V3/ManualImport/ManualImportController.cs), [service](https://github.com/Radarr/Radarr/blob/0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4/src/NzbDrone.Core/MediaFiles/MovieImport/Manual/ManualImportService.cs) | planned; owner X-03; `tests/fixtures/arr/read/manual-import.json` | Source shows separate candidate and execution paths | UNKNOWN | Read adapter only until X-05 |
| `CAP-SONARR-PREVIEW` | Sonarr release `UNKNOWN`; inspected source snapshot only | Sonarr manual episode-import service at source commit `9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb` | [service](https://github.com/Sonarr/Sonarr/blob/9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb/src/NzbDrone.Core/MediaFiles/EpisodeImport/Manual/ManualImportService.cs) | planned; owner X-03; `tests/fixtures/arr/read/sonarr-episode-import.json` | Source shows episode-level manual import mapping | UNKNOWN | Read adapter only until X-05 |
| `CAP-ARR-NO-OVERWRITE` | Radarr/Sonarr release `UNKNOWN` | Native manual-import command path, same source identities above | Same immutable source links | planned; owner X-05; `tests/compatibility/writes/arr-no-overwrite-race.json` | Source rebuilds import decisions at command time; race behavior untested | UNKNOWN | X-05 must prove exact-file/no-overwrite under an external race or block writes |
| `CAP-QBT-STOP` | qBittorrent product build `UNKNOWN` | WebUI API page commit `cc6579ab58f534d03b40ae0deba5755c4a342319`, blob `82f67b6f5aa73ef54d4c88dcd38c695ae1191b72`; wiki repository snapshot `485b0ee5e6f7e037f301500c25b23833569a2266` | [API page](https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API-%28qBittorrent-5.0%29?oldformat=true), pinned by commit and blob above | planned; owner X-05; `tests/compatibility/writes/qbittorrent-stop.json` | Pinned page documents stop endpoint; build read-back untested | UNKNOWN | X-05/X-07 must verify stop read-back |
| `CAP-QBT-SCOPE` | qBittorrent product build `UNKNOWN` | Same pinned API page; `deleteFiles=false`, setLocation, renameFile, renameFolder | [API page](https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API-%28qBittorrent-5.0%29?oldformat=true), pinned by commit and blob above | planned; owner X-05; `tests/compatibility/writes/qbittorrent-scope.json` | Pinned page documents metadata-only deletion and relocation/rename routes; multi-file scope untested | UNKNOWN | X-05/X-07 must reject scope expansion |
| `CAP-NZB-PROVENANCE` | NZBGet product build `UNKNOWN` | NZBGet history schema at source commit `b609226e18da11955ce8dda2c7df959258655579`; `ID` is deprecated alias of `NZBID`; Radarr adapter at `0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4` | [NZBGet history](https://github.com/nzbgetcom/nzbget/blob/b609226e18da11955ce8dda2c7df959258655579/docs/api/HISTORY.md) (blob `1807fc83be9af8354801f0ddcb9d7f04da4b7219`), [Radarr adapter](https://github.com/Radarr/Radarr/blob/0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4/src/NzbDrone.Core/Download/Clients/Nzbget/Nzbget.cs) (blob `fc4cd2ac3872a6593baae50f0aea5a618d4f7d38`) | planned; owner X-02; `tests/fixtures/nzbget/history-provenance.json` | Pinned source separates NZBID, deprecated ID, Arr `drone`, FinalDir and DestDir | UNKNOWN | X-02 must vary `drone`, not invent a second history ID |
| `CAP-NZB-PROCESSING` | NZBGet product build `UNKNOWN` | NZBGet post-processing contract at source commit `b609226e18da11955ce8dda2c7df959258655579` | [post-processing contract](https://github.com/nzbgetcom/nzbget/blob/b609226e18da11955ce8dda2c7df959258655579/docs/extensions/POST-PROCESSING.md) (blob `11df5abd79671266308a7eaf6da8d9ccb6f81a43`) | planned; owner X-02; `tests/fixtures/nzbget/post-processing.json` | Pinned source enumerates queue, par, unpack and script status distinctions | UNKNOWN | X-02 must block while processing/failed |
| `CAP-SEERR-MEDIA-PAGE` | Seerr release `UNKNOWN`; inspected source snapshot only | `server/routes/media.ts` at source commit `68c5bc8c7d8560d295387adeeee73982ea518e8f`, `take`/`skip`/pageInfo | [media route](https://github.com/seerr-team/seerr/blob/68c5bc8c7d8560d295387adeeee73982ea518e8f/server/routes/media.ts) | planned; owner X-04; `tests/fixtures/catalogs/seerr-media-pages.json` | Pinned source exposes route pagination fields | UNKNOWN | X-04 read-only adapter; pagination fixture required |
| `CAP-SEERR-REQUEST-PAGE` | Seerr release `UNKNOWN`; inspected source snapshot only | `server/routes/request.ts` at source commit `68c5bc8c7d8560d295387adeeee73982ea518e8f`, `take`/`skip`/pageInfo | [request route](https://github.com/seerr-team/seerr/blob/68c5bc8c7d8560d295387adeeee73982ea518e8f/server/routes/request.ts) | planned; owner X-04; `tests/fixtures/catalogs/seerr-request-pages.json` | Pinned source exposes route pagination fields | UNKNOWN | X-04 must preserve partial coverage and native status |
| `CAP-JELLYFIN-READ` | Jellyfin product/API release `UNKNOWN` | Public OpenAPI/API catalog, exact version and fixture not selected | [API catalog](https://api.jellyfin.org/) (mutable catalog; no pinned version) | planned; owner X-04; `tests/fixtures/catalogs/jellyfin-items.json` | No versioned endpoint or disposable server selected | UNKNOWN | X-04 blocks until a versioned read fixture exists |
| `CAP-JELLYFIN-REFRESH` | Jellyfin product/API release `UNKNOWN` | Refresh route/scope not frozen; accepted refresh is not availability | [API catalog](https://api.jellyfin.org/) (mutable catalog; no pinned version) | planned; owner X-05; `tests/compatibility/writes/jellyfin-refresh.json` | No versioned refresh endpoint or availability fixture selected | UNKNOWN | X-09 blocked until endpoint/version test |
| `CAP-SUBTITLE-ANIME` | Arr releases `UNKNOWN` | Subtitle companion and absolute-number mapping are product-specific | Arr source snapshots above; no immutable behavior fixture | planned; owner X-05; `tests/compatibility/writes/arr-subtitle-anime.json` | No pinned product behavior for subtitle companions or absolute numbering | UNKNOWN | X-05 requires explicit mapping or review blocker |

Planned fixture entries are contracts for later tasks, not files that exist in
this commit. Their owner and path bind future evidence to one capability. Until
the owner adds the fixture and records its product build, every corresponding
result stays `UNKNOWN` and enablement stays blocked. `UNKNOWN` is a safe result,
not a reason to guess.

## Capability gates and required tests

| Capability | Test | Result | Observation | Required evidence |
| --- | --- | --- | --- | --- |
| `CAP-ARR-PREVIEW` / `CAP-ARR-NO-OVERWRITE` | Submit a native preview with a rejection, change destination or fields, then submit the manual-import command. | UNKNOWN | Command execution can rebuild an import decision; preview is not an atomic compare-and-swap. | X-05 must prove exact-file/no-overwrite under an external race. If unsupported, block native write or record a product decision for a weaker guarantee. |
| `CAP-ARR-NO-OVERWRITE` | Execute a partial pack and inspect command, history, every destination and source. | UNKNOWN | No disposable write fixture exists yet. | X-05/X-06 record per-file effects and unresolved items. |
| `CAP-QBT-STOP` | Stop a seeding torrent and verify state, including zero-upload, lost response and external resume. | UNKNOWN | Stop route is pinned; desired-state read-back is untested. | X-05/X-07 read back stopped state before payload mutation. |
| `CAP-QBT-SCOPE` | Select part of a multi-file torrent, relocate/rename, remove metadata and inspect unselected bytes. | UNKNOWN | `deleteFiles=false` is pinned as metadata-only; native scope is untested. | X-05/X-07 reject expansion and preserve unselected payload. |
| `CAP-NZB-PROVENANCE` | Vary `drone` from NZBID, prune history, compare FinalDir/DestDir and keep `ID` equal to its deprecated NZBID alias. | UNKNOWN | Correlation rule is pinned in source; selected build is untested. | X-02 fixture must distinguish lifecycle observations from identifiers. |
| `CAP-NZB-PROCESSING` | Exercise queue, par/repair, unpack and post-processing failures/success. | UNKNOWN | Status categories are pinned in source; selected build is untested. | X-02 never marks processing/failed download ready. |
| `CAP-SEERR-MEDIA-PAGE` / `CAP-SEERR-REQUEST-PAGE` | Return overlapping pages, a capped response and delayed media-server scan. | UNKNOWN | Both route shapes are pinned in source; selected release is untested. | X-04 deduplicates, marks partial coverage and performs no writes. |
| `CAP-JELLYFIN-READ` / `CAP-JELLYFIN-REFRESH` | Read provider IDs, call chosen refresh route, then verify later library visibility. | UNKNOWN | No versioned endpoint or fixture is selected. | X-04/X-09 disposable versioned fixture or an explicit unsupported result. |
| `CAP-SUBTITLE-ANIME` | Preview forced/SDH/language companions, IDX/SUB pairs and absolute-number anime. | UNKNOWN | No pinned product behavior or fixture exists. | X-03/X-05 require explicit mapping or a review blocker. |

## Adapter rules from evidence

- Inventory is read-only. Missing credentials, incomplete pages, capped history
  and unavailable endpoints yield `unknown`, never confirmed absence.
- Every upstream ID is scoped by connection ID. A hash, NZBID, Arr numeric ID or
  provider ID from one instance cannot identify an object in another.
- qBittorrent categories/tags remain hints. Mastarr never recategorizes or tags
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
