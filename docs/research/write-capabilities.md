# v0.0.1 write capability evidence

X-05 records disposable native API behavior for the write-sensitive paths that
later adapters may use. Every interaction in this document is served by a
local `httptest` handler and uses synthetic paths and bytes. No upstream
process, credential, tracker, mounted media directory, or live mutation was
used.

## Evidence boundary

The Arr source snapshots are Radarr `0220f0daa9f68ffa40e5f0fe1ce4f909858ceba4`
and Sonarr `9c0d8deb01a51b35b85b8dbe296bbff87feb0ffb`. Their inspected manual
import surfaces separate `GET /api/v3/manualimport` preview/rejection evidence
from `POST /api/v3/manualimport` reprocessing and
`POST /api/v3/command` execution. The command receives a `ManualImport` payload
and can rebuild import decisions without receiving the earlier preview.

The qBittorrent route names and `deleteFiles=false` rule come from the pinned
qBittorrent WebUI API v2 page recorded in the compatibility matrix. The matrix
does not pin a qBittorrent product build. The Jellyfin API catalog also has no
versioned refresh fixture. Those facts keep the corresponding runtime
capabilities blocked even where the synthetic protocol probe passes.

## Disposable probes

| Capability | Fixture and native routes | Synthetic observation | Runtime decision |
| --- | --- | --- | --- |
| `CAP-ARR-PREVIEW` | `arr-no-overwrite-race.json`; `GET /api/v3/manualimport` then `POST /api/v3/command` | Preview retains `ExistingFile`; a direct command with the same exact source is still accepted and replaces the synthetic destination. | Preview rejection blocks Mastarr dispatch. Native command is never an authorization check. |
| `CAP-ARR-NO-OVERWRITE` | `arr-no-overwrite-race.json`; preview, command barrier, external destination write, command release | An external writer can change the destination between preview and native command execution. The command then replaces those bytes. Final read-back cannot prove that no overwrite race occurred. | **Blocked by G-01.** No Arr import write may be enabled from this evidence. |
| `CAP-ARR-PARTIAL-IMPORT` | `writes_test.go`; `POST /api/v3/command`, `GET /api/v3/series/201` | A completed command can publish only one file from a two-file pack. Reconciliation returns `applied` for the observed file and `unresolved` for the missing file. | Per-file effects required. Command completion never becomes aggregate success. |
| `CAP-QBT-STOP` | `qbittorrent-stop.json`; `GET /api/v2/torrents/info`, `POST /api/v2/torrents/stop` | `stalledUP` with `upspeed=0` remains seeding. A stop response can be lost after state changes; read-back observes `pausedUP`. An external resume on the next read-back blocks payload mutation and avoids a blind retry. | Stop plus fresh stopped-state read-back is the only safe predicate. Product-version capability remains unverified. |
| `CAP-QBT-SCOPE` | `qbittorrent-scope.json`; `POST /api/v2/torrents/renameFolder`, `POST /api/v2/torrents/renameFile`, `POST /api/v2/torrents/remove`, `GET /api/v2/torrents/files` | Folder rename moves every file in the torrent and is checked by exact read-back. Selected-file scope is refused. A destination collision leaves paths unchanged. Metadata removal with `deleteFiles=false` removes the record while all three synthetic payloads remain. | Whole-torrent scope only; never send `deleteFiles=true`. Product-version capability remains unverified. |
| `CAP-JELLYFIN-REFRESH` | `jellyfin-refresh.json`; `POST /Library/Refresh`, `GET /Items`, item refresh candidate | Library refresh returns `202`; the item is initially absent and becomes visible only on a later observation. The synthetic item route is unsupported. A wrong configured root does not count as availability. | Refresh acceptance and availability stay separate. Item refresh remains unsupported until a versioned endpoint is pinned. |
| `CAP-SUBTITLE-ANIME` | `arr-subtitle-anime.json`; explicit episode and companion associations | A multi-episode anime file preserves native episode IDs and absolute numbers. Forced, SDH, language and IDX/SUB pair fields survive. A subtitle without an explicit episode association stays unresolved; duplicate episode identity is rejected. | Explicit mapping is required. Filename inference and silent companion discard remain blocked. |

## Safety rules frozen for later adapters

These are the only conclusions downstream implementation may rely on:

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

Focused command used:

```text
GOWORK=off go test ./tests/compatibility/writes -count=1
```

The suite also passes with `-race`. Repository-wide generation, API,
architecture, lint, module, vet and guardrail outcomes belong to the X-05
handoff and coordinator integration checkpoint; no live upstream behavior is
claimed by this document.
