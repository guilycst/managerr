# Product interview

Status: interview consolidated into the v0.0.1 planning baseline. User answers remain authoritative. Implementation is not authorized in this phase. Engineering details selected after the interview are recorded separately in [decisions](../architecture/decisions.md).

## Confirmed requirements

- Public repository requested at `guilycst/managerr`. Final license answer: MIT.
- Go, API first, resource-oriented REST API using `oapi-codegen`.
- A nested Go module serves the UI as a BFF consuming the API.
- Use the latest released `github.com/araihu/goshtoso` for the UI/BFF. Live release check on 2026-09-11 found stable v0.3.0, published 2026-09-11T02:04:47Z. This is the planning baseline, not an installed dependency; recheck the latest release and pin an explicit version when implementation begins.
- Goshtoso integration uses server-rendered templ components and supported public assets/App Shell APIs. Follow the using-goshtoso skill when designing/implementing the dashboard. Put the matching templ generator in the separate tools module. Do not implement from demo-site internals or invent custom UI before checking supported components.
- Detailed specification and implementation tasks organized into lanes, with durable recovery records for future subagents.
- Unknown titles use two approvals: registration first, then exact file/episode import review.
- Discovery examines one or more download directories. Client-retained inventory alone is insufficient as the product boundary.
- Detected media should show which download client downloaded it, when it was downloaded, and the associated torrent or NZB descriptor. Retain Managerr's own copy of the original `.torrent` or `.nzb` when available so it can survive client-history removal. If unavailable, preserve that fact rather than fabricate an original descriptor.
- Each discovery should show whether external managers track it, which managers do, and what media they identify it as.
- Integrations with external managers are ports in a hexagonal architecture. Sonarr, Radarr, and Seerr are named integrations.
- For untracked media, assist the user in upserting it into external managers.
- Offer both Arr-managed registration/import and library-only placement through copy or hardlink. Jellyfin scans the destination; it does not perform the file transfer itself.
- Expose each small action independently in the REST API. The UI/BFF may compose multiple actions into convenient workflows. Do not require API clients to use a single bundled reconciliation operation.
- Registration, Arr import, and direct library placement are distinct actions. Granularity does not implicitly authorize destructive actions or live changes during the specification phase.
- The UI explicitly presents composed workflows as multiple steps, explains that partial failure is possible, identifies the failed step, and reports current state and completed effects.
- Persistence uses SQLite, sqlc, and Go migrations. Interpret the migration tool as `github.com/golang-migrate/migrate/v4`; driver/build compatibility must be checked during implementation.
- Development/code-generation tools belong to a separate `./tools` Go module, including oapi-codegen, sqlc, migrate, and envdoc. Do not retain the earlier root-module tool placement.
- Approved workflows continue independently of an open UI and recover after a server restart, pausing for another approval or required intervention. The API owns durable execution; the BFF composes and presents workflows.
- Workflows have explicit cancellation, per-attempt timeouts, and safe retry handling. For temporarily unavailable external services, wait until cancelled by default, with an optional overall workflow deadline. Retry attempts back off; changed inputs or uncertain write outcomes pause for review/reconciliation instead of blind mutation retries.
- Actions are semantically idempotent: observe the desired state before writing; if it is already materialized, perform no mutation and complete successfully as already satisfied. This applies across new requests/workflows as well as retries and restarts, independently of HTTP idempotency keys.
- Conflicting existing state stops for explicit user review. Show the conflict and require approval before any replacement. This establishes an approval boundary; it does not yet define which replacement operations v1 will support.
- Discover files remaining in configured download directories even when qBittorrent/NZBGet history no longer exists. The user explicitly values this for cleanup assistance.
- Missing client, download timestamp, or torrent/NZB association remains explicitly unknown/unavailable. Filesystem timestamps are separate observations, not inferred download dates. No matching client record does not prove a file is abandoned or safe to delete.
- Filesystem actions include move, rename, copy, delete, and the previously confirmed hardlink action, where the underlying filesystem and permissions permit them. Actions apply to files or directories where their semantics support that target type; for example, directory hardlinks cannot be assumed supported.
- Keep filesystem actions extensible so new actions can be added for file or directory targets without rebuilding the overall workflow architecture. Expose capabilities and enforce permissions at execution time.
- Delete/move/rename are confirmed product features, not authorization to mutate live media during the specification phase. Recovery, treatment of tracked/seeding files, recursive directory behavior, and conflict handling still need precise contracts.
- Default deletion is recoverable trash with configurable retention, defaulting to 30 days. A background janitor eventually removes expired trash. Scheduling and restoration behavior remain to be specified.
- Expose permanent deletion directly as an explicit API action; it must not require waiting for trash retention to expire. The UI uses two explicit steps: move to trash first, then permanently delete from the trash view. The first step remains recoverable until explicit purge or janitor expiry.
- Keep handling of externally tracked files simple. The qBittorrent hexagonal port supplies relevant download/seeding metadata, the API exposes it, and the UI clearly identifies files still being seeded and the effect of the selected action.
- Use qBittorrent's supported API for linked-file moves and renames where possible. Do not assume a torrent display-name change renames its payload or that every arbitrary filesystem move is supported.
- Deleting/trashing associated payload should interrupt seeding. While files are in trash, keep the associated torrent entry stopped, preserving its association for possible restoration. Permanent purge also removes the associated torrent entry. Whether restore resumes seeding still needs definition. Merely deleting a filesystem path is not a verified stop action.
- Do not expand this into automatic coordinated repair across all external managers. Broader Arr path repair and automatic seeding restoration are not confirmed features.
- Seerr integration is read-only for now. Expose relevant observed media/request status; creating, approving, cancelling, or deleting Seerr requests is outside v1.
- Preserve extensibility through the hexagonal ports and independent action architecture so future releases can manage more of the Arr stack from one place. This is a product direction, not authorization to implement every manager/action now.
- Deploy as portable containers, without requiring Kubernetes or a particular container engine. The user's homelab Kubernetes cluster is the first deployment target, not a product dependency. Filesystem access, supported host/CPU platforms, volumes, and credential/configuration delivery still require concrete deployment contracts.
- Require all directories Managerr manages to be mounted into its API/executor container. Bind mounts, named volumes, and mounted NAS storage are valid deployment mechanisms. A remote-filesystem access protocol is not required for v1.
- Support explicit path mappings where Managerr, download clients, and external managers address the same files using different paths. The UI/BFF consumes the API and does not need direct media mounts.
- Configure connections, watched directories, and path mappings through both API/UI and static YAML files. File configuration supports GitOps delivery, including mounted Kubernetes ConfigMaps and Secrets, while remaining portable to other container environments.
- Clearly expose configuration provenance. Static YAML-owned configuration is read-only through the application; changing the source file is the only way to change it. The API/UI must not silently override, delete, or write back a file-owned resource.
- The REST API also supports external operator integrations for application-managed configuration. Implementing a Kubernetes operator is not requested for v1.
- Static YAML is loaded at startup only. Changes require an application restart; no file watcher or automatic reload. API/UI-managed configuration remains a separate runtime-editable source.
- YAML integration configuration supports credential references to environment variables and mounted secret files, allowing secrets to be kept out of ConfigMaps.
- API/UI-entered integration credentials are stored encrypted in SQLite. Accept a credential-encryption key supplied through an environment variable or mounted secret file. If neither is provided, the instance generates its own key and stores it at a stable, documented path on persistent storage.
- Document that the generated key must persist and accompany database recovery. Exact path, key format, startup validation, and backup/restore checks belong in the implementation contract. Missing key material for existing encrypted credentials must not be treated as a fresh instance eligible for silent key replacement.
- Support multiple instances of every integration, including multiple Radarr/Sonarr and qBittorrent/NZBGet instances. Each instance has its own stable identity, credentials, and path mappings. Tracking observations and actions must identify the particular instance, not merely the product name.
- The currently discussed release is v0.0.1. It has no application authentication or sign-in in the API or UI/BFF. The user's deployment places OIDC at the HTTPRoute boundary; this is external deployment configuration, not an application Kubernetes dependency.
- Media scope for v0.0.1 includes movies, individual episodes, season packs, anime, and subtitles. Mixed folders means video with accompanying subtitles; folders mixing unrelated titles are not implicitly included. Music is not part of the confirmed scope.
- By default, accompanying subtitles follow their associated video during import, copy, move, and rename. Show associations in the preview, preserve language/forced/SDH labels, and allow correction of uncertain matches before approval. The user accepted this proposed behavior as the default.
- Subtitle grouping must remain compatible with independent API actions: the composed workflow makes all affected video/subtitle files explicit. Do not silently broaden a single-file action to unrelated files or discard unmatched subtitles.
- Provide suggested movie/series identities and episode mappings from available metadata. The review UI lets the user search for a different title and correct season/episode assignments before approval, including ambiguous release names and anime numbering. Suggestions are not automatic approval or proof of identity.
- During new Radarr/Sonarr registration, let the user explicitly choose monitoring. Default to unmonitored, managing only files already downloaded; enabling monitoring is an opt-in that can permit future Arr acquisition of missing episodes or upgrades. Do not silently change an existing title's monitoring state or start a search merely because registration occurs.
- If a requested hardlink is unsupported or cannot be created, do not silently substitute a copy. Explain the filesystem limitation and additional destination-space requirement, and obtain explicit approval for copying. If discovered during execution, preserve completed effects and return the remaining work to review with the changed action made explicit.
- Discovery runs automatically on a configurable schedule, with an interval per watched directory. Expose a manual Scan now action in both API and UI. Large archives can use a slower schedule than active download folders; exact interval defaults and scan concurrency remain implementation design details.
- Defer basic auth and application OIDC support to v0.0.2. Their exact protocol, account, and authorization design is not part of v0.0.1.
- Preserve review decisions and operation history in v0.0.1 without claiming an authenticated application user. Earlier required API bearer tokens, UI login/session secrets, and mandatory authenticated actor fields are superseded. External header trust and per-user attribution must not be invented as replacements.
- Startup validation, duplicate identity/ownership collisions, file removal behavior, and credential references/redaction need explicit contracts. Environment parsing remains relevant for bootstrap settings; it is no longer the sole configuration mechanism.

## Draft assumptions superseded or awaiting confirmation

- Client-only discovery in the initial draft is superseded by directory discovery.
- The initial prohibition on Managerr placing files itself is superseded for the explicitly requested library-only copy/hardlink workflow. Its filesystem ownership and safety contract still need design.
- The API contract must be revised to support independent actions and BFF composition. Previous operation/plan schemas are proposals, not frozen contracts.
- Treating external adapters merely as implementation helpers is superseded by the explicit hexagonal architecture requirement.
- Seerr observation-only scope is now confirmed for v1. Future Seerr writes must be added explicitly through the established port/action contracts.
- Multiple instances per integration, portable container deployment, SQLite/sqlc/migrations, the tools module, and the latest released Goshtoso UI are confirmed. Application authentication is explicitly excluded from v0.0.1 and planned for v0.0.2. New Arr registration defaults to unmonitored with an explicit monitoring opt-in.
- Earlier html/template/custom-CSS UI defaults are superseded by the user's choice of Goshtoso and its templ integration. Use the chosen release's runtime conventions rather than old HTMX compatibility assumptions.
- Native import mechanics, no-overwrite behavior, path safety, and reliable descriptor/provenance recovery require compatibility evidence before implementation.
- Earlier environment-only integration configuration and read-only `/connections` assumptions are superseded. API/UI-managed resources are writable, while YAML-owned resources are not. The exact REST resource schemas still require revision.
- Earlier single-instance-per-product assumptions are superseded. Do not use product kind as a unique resource identity or silently choose a default destination when more than one instance is eligible.

## Design questions carried into the specification

1. Does tracking mean the title exists in a manager, the exact downloaded files are imported, or both dimensions displayed separately?
2. Seerr is read-only for v1. Define which Radarr/Sonarr fields can be changed by an approved upsert without extending this to Seerr request management.
3. Retain original torrent/NZB descriptors when available. Define retrieval capabilities per client, storage location, retention after media purge, and safe delivery through the API. Descriptor contents may contain credentials and must never appear in public fixtures/logs.
4. Cleanup includes extensible actions and trash-then-purge. Hardlink-to-copy fallback requires explicit approval and space-impact disclosure. For qBittorrent, retain the stopped torrent record during trash and remove it at permanent purge. Define partial multi-file torrent scope, restoration/collision handling, seeding after restore, and directory recursion/symlinks. Removing a torrent record must not implicitly authorize deleting unselected payload files.
5. Movies, episodes, season packs, anime, and accompanying subtitles are confirmed. Subtitles follow their video with reviewable associations and preserved language/forced/SDH labels. Define supported subtitle formats, detection details, and anime episode-mapping behavior. Unrelated-title mixed packages are not implicitly included.
6. Title and episode suggestions with manual search/correction are confirmed. New registration defaults to unmonitored with explicit monitoring opt-in. Define concrete matching inputs, evidence presentation, series monitoring scope, and other editable Radarr/Sonarr upsert settings.
7. Configuration can come from API/UI or static YAML, with explicit source and read-only file ownership. YAML changes require restart and supports environment/mounted-file credential references. API/UI credentials are encrypted in SQLite with supplied or persistently generated key material. Define the stable key path, startup/restore validation, ownership conflicts, supported platforms, and remaining deployment details. Authentication design is deferred to v0.0.2.
8. Default outage policy is confirmed: wait until cancelled, optional workflow deadline, bounded attempts with backoff, and pause/reconcile changed inputs or uncertain writes. Define exact timeout/backoff limits and how pre-approval plan expiry differs from already-approved durable execution. See [workflow patterns](../architecture/workflow-patterns.md).
9. Conflict review is confirmed. The supported v1 conflict-resolution actions, including whether replacement is implemented, still need definition. An existing filename or title alone does not prove the desired result is satisfied.

## Consolidation

The questions above preserve the interview's unresolved design details at the time
of discussion. The consolidated specification now defines defaults for key paths,
restore behavior, retention, configuration ownership, retries, recursion and
conflict handling. These are engineering choices, not additional claimed user answers.
Tracking dimensions remain separate, consistent with the registration/import discussion.
Native upstream behavior remains gated by compatibility evidence.

Resume at [execution instructions](../execution/RESUME.md). Do not restart the
interview or treat implementation tasks as authorization to build or operate media.
