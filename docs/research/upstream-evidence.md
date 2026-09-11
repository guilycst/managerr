# Upstream evidence

Research date: 2026-09-11. These are public source observations, not a tested
supported-version matrix or evidence from a deployed media stack. Recheck releases
at implementation start and pin exact fixture versions before adapter sign-off.
No real media inventories, credentials or tracker metadata were used.

## Arr source snapshots

Radarr inspected commit: `0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4`.
Sonarr inspected commit: `9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb`.

- Radarr [ManualImportController](https://github.com/Radarr/Radarr/blob/0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4/src/Radarr.Api.V3/ManualImport/ManualImportController.cs)
  separates GET candidates from POST reprocessing. Neither is the command execution route.
- Radarr [manual import service](https://github.com/Radarr/Radarr/blob/0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4/src/NzbDrone.Core/MediaFiles/MovieImport/Manual/ManualImportService.cs)
  executes ManualImportCommand and constructs a new ImportDecision for submitted files.
- Sonarr [manual import service](https://github.com/Sonarr/Sonarr/blob/9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb/src/NzbDrone.Core/MediaFiles/EpisodeImport/Manual/ManualImportService.cs)
  has corresponding episode-level execution and mapping. Exact file DTOs differ.
- Radarr [NZBGet adapter](https://github.com/Radarr/Radarr/blob/0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4/src/NzbDrone.Core/Download/Clients/Nzbget/Nzbget.cs)
  provides the inspected downloadId correlation and FinalDir/DestDir behavior.

Inference from execution source: a saved native preview alone does not guarantee
its rejections will still be enforced by the later command, nor prevent an external
race. G-01 requires reproducible tests and a supported coordination strategy.
Managerr must not promise no overwrite merely from check-then-write preflight.

## Download clients and catalogs

- [qBittorrent 5 WebUI API](https://github.com/qbittorrent/qBittorrent/wiki/WebUI-API-%28qBittorrent-5.0%29)
  documents stop, payload rename, relocation, deletion flags and response semantics.
  Treat HTTP acceptance separately from observed desired state.
- [NZBGet API reference](https://nzbget.com/documentation/api/)
  documents supported RPC protocols, Basic authentication and positional JSON-RPC
  parameters. Client history/descriptor availability needs capability tests.
- [NZBGet post-processing](https://nzbget.com/documentation/post-processing-scripts/)
  supplies lifecycle context. Completed transfer does not necessarily mean completed unpacking.
- Seerr inspected commit: `68c5bc8c7d8560d295387adeeee73982ea518e8f`.
  [Media route](https://github.com/seerr-team/seerr/blob/68c5bc8c7d8560d295387adeeee73982ea518e8f/server/routes/media.ts)
  provides take/skip pagination and page information in the inspected schema.
- [Seerr media server settings](https://docs.seerr.dev/using-seerr/settings/mediaserver/)
  describes library scans updating availability. Managerr can observe eventual
  state without creating a request. [Service settings](https://docs.seerr.dev/using-seerr/settings/services/)
  describes the connected Arr service context.
- [Jellyfin API documentation](https://api.jellyfin.org/)
  is the source for choosing tested inventory and refresh endpoints in C-00.
  Exact refresh scope/version is not yet verified by this planning baseline.

## Go and UI

- [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) for strict server/client
  generation. Freeze version and std-http configuration in C-01/C-02.
- [sqlc transactions](https://docs.sqlc.dev/en/stable/howto/transactions.html)
  supports the explicit transaction boundaries required by approvals and work claiming.
- [golang-migrate](https://github.com/golang-migrate/migrate) requires a compatible
  SQLite driver/build selection; test before freezing C-02/D-01.
- [Go tool dependencies](https://go.dev/doc/modules/managing-dependencies#tools)
  informs the separate tools module. No root runtime tools dependency is needed.
- [Goshtoso v0.3.0 release](https://github.com/araihu/goshtoso/releases/tag/v0.3.0)
  was latest stable when rechecked through GitHub on 2026-09-11, published at
  02:04:47Z. No module has been installed in this repository.
- [Goshtoso App Shells](https://github.com/araihu/goshtoso-app-shells)
  must be selected for the same runtime generation. Skill discovery reported a
  compatible baseline `v0.1.9-0.20260910224508-5b2222e54637` for Goshtoso v0.3.0,
  while v0.1.8 used the older HTMX runtime. This is a candidate, not a frozen
  dependency. U-00 verifies current public APIs and releases before selection.

## Evidence still required

C-00 creates a matrix of product version, API version, capability, evidence source,
fixture and pass/fail/unknown. X-05 adds disposable native write tests. U-00 records
public component discovery and module pins. V-02 records filesystem/container and
restore evidence. Until those tasks complete, unsupported/minimum versions,
Arr race safety, subtitle behavior, descriptor availability and runtime compatibility
remain explicit gates rather than asserted facts.
