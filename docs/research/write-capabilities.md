# v0.0.1 write capability evidence

X-05 records executable protocol-model behavior for write-sensitive paths that
later adapters may need. Every interaction in this document is served by a
local `httptest` handler and uses synthetic paths and bytes. No pinned upstream
process or container image was run, so these checks do not establish
product-version compatibility. No credential, tracker, mounted media
directory, or live mutation was used.

All five JSON fixtures in `tests/compatibility/writes/` are decoded and asserted
by the focused test suite. That binding makes fixture drift visible, but the
handlers remain local protocol models. Runtime write capabilities therefore
remain blocked until disposable, version-pinned upstream evidence exists.

## Evidence boundary

The Arr source snapshots are Radarr `0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4`
and Sonarr `9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb`. Their inspected manual
import surfaces separate `GET /api/v3/manualimport` preview/rejection evidence
from `POST /api/v3/manualimport` reprocessing and
`POST /api/v3/command` execution. The command receives a `ManualImport` payload
and can rebuild import decisions without receiving the earlier preview.

The qBittorrent route names and `deleteFiles=false` rule come from the
qBittorrent WebUI API v2 page recorded in the compatibility matrix. The matrix
does not pin a qBittorrent product build. The Jellyfin API catalog also has no
versioned refresh fixture. Those facts keep the corresponding runtime
capabilities blocked even where the synthetic protocol model passes.

## Disposable probes

| Capability | Fixture and native routes | Protocol-model observation | Runtime decision |
| --- | --- | --- | --- |
| `CAP-ARR-PREVIEW` | `arr-no-overwrite-race.json`; `GET /api/v3/manualimport` then `POST /api/v3/command` | The rejection branch retains `ExistingFile`; a separate accepted branch shows that a direct command with the same exact source can replace the synthetic destination. | **Blocked.** Preview rejection must block Mastarr dispatch, and native command success is never an authorization check. |
| `CAP-ARR-NO-OVERWRITE` | `arr-no-overwrite-race.json`; preview, command barrier, external destination write, command release | The local model permits an external writer to change the destination between preview and native command execution. The command then replaces those bytes. Final read-back cannot prove that no overwrite race occurred. | **Blocked by G-01.** No Arr import write may be enabled from this protocol-model evidence. |
| `CAP-ARR-PARTIAL-IMPORT` | `writes_test.go`; `POST /api/v3/command`, `GET /api/v3/history`, `GET /api/v3/episode?seriesId=201&includeEpisodeFile=true` | A completed command and one `downloadFolderImported` history record are recorded separately from supported Sonarr episode read-back. The decoded read-back associates one of two expected files and leaves the other `unresolved`; no handler-side import list contributes to the result. | **Blocked.** Per-file effects remain required, and command completion never becomes aggregate success. Native Arr behavior is unverified. |
| `CAP-QBT-STOP` | `qbittorrent-stop.json`; `GET /api/v2/torrents/info`, `POST /api/v2/torrents/stop` | `stalledUP` with `upspeed=0` remains seeding. A stop response can be lost after state changes; read-back observes `pausedUP`. An external resume on the next read-back blocks payload mutation and avoids a blind retry. | **Blocked pending a pinned build.** Stop plus fresh stopped-state read-back is the only safe protocol predicate. |
| `CAP-QBT-SCOPE` | `qbittorrent-scope.json`; `POST /api/v2/torrents/renameFolder`, `POST /api/v2/torrents/renameFile`, `POST /api/v2/torrents/delete`, `GET /api/v2/torrents/files` | Folder rename moves every file in the torrent and is checked by exact read-back. Selected-file scope is refused. A destination collision leaves paths unchanged. Metadata removal with the exact `deleteFiles=false` form value removes the record while every synthetic payload remains. | **Blocked pending a pinned build.** Whole-torrent scope only; never send `deleteFiles=true`. |
| `CAP-JELLYFIN-REFRESH` | `jellyfin-refresh.json`; `POST /Library/Refresh`, `GET /Items`, item refresh candidate | Library refresh returns the fixture’s `202`; the item is initially absent and becomes visible only on a later observation. The synthetic item route is unsupported. A wrong configured root does not count as availability. | **Blocked pending a versioned endpoint fixture.** Refresh acceptance and availability stay separate. |
| `CAP-SUBTITLE-ANIME` | `arr-subtitle-anime.json`; explicit episode and companion associations | A multi-episode anime file preserves native episode IDs and absolute numbers. Forced, SDH, language and IDX/SUB pair fields survive. A subtitle without an explicit episode association stays unresolved; duplicate episode identity is rejected. | **Blocked pending native evidence.** Explicit mapping is required; filename inference and silent companion discard remain blocked. |

## Safety rules frozen for later adapters

These are the only protocol-model conclusions downstream implementation may
rely on. They do not enable a runtime write capability:

1. Arr preview rejection, destination checks, and command execution are
   separate operations. A later Arr writer must validate the immutable plan and
   native rejections, then reconcile every requested file. It must not expose a
   generic command payload or treat a `200`/completed command as proof of import.
2. The native Arr path has no demonstrated compare-and-swap or coordination
   boundary for the destination. X-06 must remain disabled while G-01 is open.
   A weaker overwrite promise requires a recorded product decision before code
   changes the gate.
3. qBittorrent state is authoritative for stop safety. Zero upload speed is
   not stopped. After any uncertain stop response, perform a read-only state
   observation before touching payload. Re-observe immediately before a later
   payload action; a resumed torrent blocks that action.
4. qBittorrent metadata removal uses `deleteFiles=false`. Stopping the whole
   torrent and retaining the trash association are separate effects. A partial
   selection cannot authorize a native folder relocation that acts on the whole
   torrent. Exact file paths must be read back after rename or relocation.
5. Jellyfin `202 Accepted` means only that refresh was accepted. Availability
   requires a later item observation with a provider/path mapping that matches
   the selected library scope.
6. Subtitle and anime associations are exact reviewed values. Unmatched
   companions and unsupported absolute-number mappings remain visible review
   issues.

## G-01 status

`G-01` remains open. The deterministic race probe reproduces a destination
replacement after a successful preview and before command execution. The
documented Arr command path supplies no conditional destination identity or
transaction shared with the preview request, so this lane cannot establish the
required no-overwrite guarantee. X-06 must keep native Arr import blocked until
later evidence proves coordination/atomicity or the product records an
explicitly approved weaker behavior.

## Checks

Focused commands used:

```text
GOWORK=off go test -mod=readonly -count=1 ./tests/compatibility/writes
GOWORK=off go test -race -mod=readonly -count=1 ./tests/compatibility/writes
GOWORK=off go vet -mod=readonly ./tests/compatibility/writes
```

Repository-wide generation, API, architecture, lint, module, vet and
guardrail outcomes belong to the X-05 handoff and coordinator integration
checkpoint. No live upstream behavior is claimed by this document.
