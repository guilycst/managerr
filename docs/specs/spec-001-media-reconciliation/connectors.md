# Ports and connector contracts

Future v0.0.1 design. Public upstream evidence is cataloged in
[research](../../research/upstream-evidence.md). Research branch heads are not
minimum supported versions. C-00 and X-05 must freeze tested version/capability
combinations before affected writes are enabled.

## Port responsibilities

| Port | Reads | Allowed writes |
| --- | --- | --- |
| Download inventory | Client items/files, state/completion, category/tags, identifiers, original descriptor capability | None during discovery |
| Download control | Observe stopped state, payload paths, native operation results | qBittorrent stop, supported relocation/file/folder rename, metadata-only removal |
| Media manager | Search identities, options/profiles/roots, registrations/files/history, native import preview | Explicit field upsert and reviewed manual import through Radarr/Sonarr |
| Media server | Jellyfin libraries/items/provider IDs/availability | Explicit supported refresh request |
| Request catalog | Seerr media and requests with provider IDs/status | None in v0.0.1 |
| Filesystem actions | Enumerate/stat/hash/capabilities, root confinement | Reviewed copy/hardlink/move/rename/trash/restore/delete |
| Credential/config repository | Effective typed configuration and source metadata | Managed configuration and encrypted credential replacement only |

Separate read capabilities from optional writes. A connector returns supported,
unsupported or unknown with version/evidence, not a guessed boolean. Missing APIs
must disable the particular action, not invent a success response or disable all
inventory. Normalize typed errors: unavailable, rate_limited, unauthorized,
invalid_input, conflict, unsupported and outcome_unknown. Preserve sanitized
upstream status/command identifiers for evidence.

HTTP adapters use bounded clients, certificate verification, context deadlines,
secret redaction and pagination termination. Retries in HTTP helpers must not
silently repeat mutating requests. Durable action execution owns retry policy.
No shared upstream databases, browser scraping, or unsupported hidden endpoints.

## qBittorrent

Read WebUI API version/application version, torrent list, properties/files,
content paths, state, progress, completion timestamp where present, ratio/seed
status, category, tags and info hashes. Scope hashes by connection and retain
supported v1/v2 identities rather than assuming every ID is a 40-character hash.
A magnet-only item may lack an original exportable descriptor. Retain exported
original bytes only when supported and obtainable.

Use categories/tags as provenance hints and optional filters. Changing them can
trigger existing Arr automation, so no automatic recategorization/tagging in
v0.0.1. Seeding status is a real state observation; zero upload rate is not stopped.

For compatible qBittorrent 5 APIs, stop uses `/api/v2/torrents/stop`; relocation
uses setLocation; payload renaming uses renameFile/renameFolder. Torrent name
rename is a distinct operation and is not a payload rename. Delete with
`deleteFiles=false` removes only the client record. Some methods report HTTP 200
without proving the desired effect. Read back item/file paths and state.

Compatibility tasks must test full and partial multi-file selections, native
operation completion, stop/resume races, path collisions, files inaccessible to
the API container, and response loss. If native relocation would move unselected
files or cannot preserve reviewed destination semantics, refuse that plan.
Trash keeps the torrent record stopped. Permanent deletion removes it; untouched
payload remains on disk. An absent record is not proof files were deleted.

## NZBGet

Use supported JSON-RPC with positional parameters and configured credentials.
Observe queue, post-processing and retained history separately. Complete download
is not ready while unpack/repair/post-processing is ongoing. Prefer FinalDir when
reported, otherwise DestDir, subject to explicit mapping and filesystem checks.
Keep NZBID and its deprecated `ID` alias as one upstream identity, while keeping
queue/history lifecycle observations and upstream parameters as distinct typed
fields. A history item does not provide a second independent ID merely because
the client exposes an alias.

Arr's inspected NZBGet adapter uses a `drone` parameter as DownloadId when present,
otherwise the history item ID. Do not assume NZBID equals Arr's downloadId. Test
a differing Arr `drone` parameter, history pruning and FinalDir/DestDir fallback;
do not fabricate an independent history ID.
Correlate through actual history/client evidence and target instance. Categories
are hints, not proof Arr registered/imported a title.

Original .nzb retrieval is capability-dependent. Use a supported API export if
verified or a configured mounted retained-NZB directory. Never claim reconstruction
from history JSON is the original NZB. After history pruning, preserve cached
original and mark current client association unavailable rather than erasing it.
Some history APIs return full arrays rather than pages; bound response size and
mark incomplete if capped. NZBGet mutation beyond required file action safety is
not included; require post-processing to be complete before touching payload.

## Radarr and Sonarr

Inventory includes all registrations, monitoring state, files and missing media,
not just downloads previously managed by Managerr. Resolve identities using
supported provider IDs, primarily TMDB for movies and TVDB for series; preserve
other IDs returned for Jellyfin/Seerr correlation. Native title/episode lookup
supplies review options. Never scrape metadata sites or infer identity from a
release name alone.

Use supported v3 movie/series registration APIs with explicit fields only. Preserve
unselected settings on upsert and read back outcomes. Set add options to prevent
automatic search. Monitoring is explicit opt-in for new titles. Root folder,
quality profile, season/episode IDs, series type and language/quality values must
come from the selected instance's current supported schema/options.

The inspected manual import path has two distinct functions:

- GET `/api/v3/manualimport` produces native file candidates/rejections.
- POST `/api/v3/manualimport` reprocesses previews. It does not execute import.
- POST `/api/v3/command` with `name: ManualImport`, typed exact `files`, and
  explicit importMode executes import. Observe command, history and final files.

Radarr preview parameters include folder, downloadId, movieId and
filterExistingFiles. Movie ID without downloadId can select a library-file query;
do not accidentally preview the destination library as a downloaded source.
Sonarr's corresponding series/season/file model differs. Generate typed adapter
DTOs or maintain explicit structs verified against pinned upstream schemas;
never expose raw arbitrary command JSON through Managerr.

Use Copy mode for source-preserving Arr import. Arr may choose copy or hardlink
based on its own settings/filesystem. Label this as Arr-managed source-preserving
transfer, not guaranteed hardlink. A strict hardlink request must use a capability
that proves it; no hidden fallback to extra disk use.

The inspected native command path can reconstruct accepted ImportDecision objects
and bypass preview rejections. Managerr must validate native rejection reasons,
existing destination/file associations, exact approval, and post-import evidence.
A preflight cannot alone prevent an external actor replacing a destination between
check and import. X-05 must establish tested coordination/atomicity conditions.
If a no-overwrite guarantee cannot be upheld, block that native write capability
and record the limitation. Do not weaken I-07 by calling a race window safe.
A proposal to accept a weaker guarantee requires a recorded product decision.

Completion requires verified expected file associations for the selected movie
or exact episode set, mapped destination evidence, source-preservation evidence
where requested, and companion outcomes. History imported events and downloadId
help provenance but are not universal proof. Command failure can coexist with
some imported files; reconcile each item independently. Multi-episode files may
map to several episode IDs. Subtitle behavior and anime/absolute numbering require
explicit contract tests; unsupported mappings remain review issues.

## Jellyfin

Read libraries/items and provider IDs through supported APIs; correlate with
configured mappings and item evidence. Distinguish item present from playable
media observation when permissions/mounts prevent verification. Support explicit
refresh where the tested API permits, with scope visible in the plan. Request
accepted does not mean library scan finished. Poll observations under normal
backoff; do not mark available from Arr success alone.

## Seerr

Read media and request state through supported APIs, including request details
needed to explain tracking. Preserve native status enum with normalized meaning
and source timestamp; do not collapse requested, processing, partially available
and available. `/api/v1/media` uses take/skip and pageInfo in inspected source.
Validate pagination and complete coverage for requests separately.

Seerr's scans of its configured media server and Arr services may eventually
reflect an external import. Managerr observes that state without creating a
request or forcing a false available marker. Instance topology mismatch remains
visible; two Seerr/Arr servers need explicit relationships rather than guessed
shared ownership.

## Coverage and version gates

Every adapter test includes empty success, multi-page/full-history success,
truncation, forbidden credentials, rate limit, timeout and malformed response.
Deduplicate page overlaps and terminate pagination defensively. Partial coverage
never overwrites last complete evidence with false absence. Detect supported
upstream versions and expose unsupported capability reasons at runtime.

C-00 produces the version/capability matrix and test fixtures from public/synthetic
data. X-05 validates write-sensitive behavior in disposable supported upstream
instances with synthetic files. No live homelab write is authorized by these tasks.
