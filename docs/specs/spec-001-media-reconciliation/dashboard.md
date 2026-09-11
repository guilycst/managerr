# Dashboard and BFF

Future v0.0.1 UI in nested module github.com/guilycst/mastarr/ui. The BFF serves
Goshtoso/templ HTML and consumes the generated Mastarr HTTP client. It cannot
open SQLite, import root internals, access media mounts, call upstreams, or make
approval/safety decisions. API errors and observed state remain authoritative.

## Goshtoso baseline and component discovery

Use latest released Goshtoso when implementation begins, verify compatibility,
and pin explicit versions. Planning research observed v0.3.0 on 2026-09-11.
Do not confuse that observation with an installed dependency. Check compatible
public operations App Shell, assets, forms, tables, dialogs, feedback and navigation
before adding app markup. Record reuse, composition or an actual public-API gap.
Do not copy demo-site internals or build a custom shell before this check.

The researched v0.3.0 ecosystem uses HTMX 4 and Alpine 3. Its compatible App Shells
baseline differs from older HTMX 2 releases; freeze the pair with evidence in U-00.
Use supported head/asset APIs and local runtime assets for portable self-hosting.
Run templ generation only for consumer templates and keep generator in tools module.
Goshtoso component markup already ships generated. No root/UI go.work or local
replace is needed because their communication is HTTP.

## Pages

| Route | Purpose and essential content |
| --- | --- |
| `/` | Counts by discovery/review/workflow state, degraded connections and actionable failures; no unrelated dashboard widgets. |
| `/discoveries` | Filter root/client/category/tag/readiness/tracking; show media suggestion, completion date or unknown, age, client and descriptor availability. |
| `/discoveries/{id}` | Exact files/subtitles, evidence and unknowns, per-instance tracking, seeding impacts, suggested title and corrections. |
| `/media` | Full Arr catalog with instance/monitoring/missing/import/availability filters, including media unrelated to Mastarr downloads. |
| `/media/{id}` | Provider identity, all instance registrations/files/requests, timestamps and links; distinguish suggestion from verified association. |
| `/reviews/{planId}` | Saved plan revision, exact actions/files/episodes/fields, conflicts, source preservation, bytes/space, monitoring and deletion impacts. |
| `/workflows`, `/workflows/{id}` | Ordered steps, approvals, attempts, completed effects, failed vs blocked, unresolved late effects, retry schedule, cancel. |
| `/actions/{id}` | Standalone action with same evidence/recovery controls as a workflow step. |
| `/trash`, `/trash/{id}` | Original locations, selected items, expiry, stopped/removed client state, restore conflicts, purge effects and holds. |
| `/settings/connections`, `/settings/storage`, `/settings/mappings` | Multi-instance config, checks, credential replacement, source/read-only status and restart instructions. |

Unknown IDs render a useful 404 within the shell. API outage renders a retriable
error without clearing form drafts or showing an empty successful inventory.
Large lists use API pagination and filters; no BFF fetch-all assumption.

## Review behavior

The page shows which title/instance is selected in URL, content and subsequent
request. Search candidates through API and let users correct identity, season,
episode and subtitle associations. Preserve inputs across validation/upstream
failures. Provider IDs remain visible where needed to disambiguate same-name titles.

Unknown Arr title review explains the two approvals before the first submission.
Registration success lands at a new exact-import review, not automatic import.
Monitoring starts unchecked and explains future acquisition. Direct library
placement is a separate choice and does not imply Arr tracking.

Each composed action is listed with effects and prerequisites. Destructive scope
uses exact files, affected torrent records, unselected remaining payload, and
irreversible effects. Subtitles appear as explicit associated files. Hardlink
failure offers a new copy plan with additional space, never automatic fallback.
No hidden blanket approval in an attractive primary button.

Trash action is recoverable. Permanent deletion is available in UI only from
trash view, with a second explicit confirmation of selected entries. Direct API
hard delete remains supported but no UI shortcut bypasses trash. Expiry purge
policy is visible before trash approval. Restore leaves seeding stopped and
explains removed torrent records cannot be restored automatically.

The workflow view shows current state per step, applied vs already_satisfied,
last observation, error reason, retry time, cancellation and unresolved effects.
Cancel acknowledgement says no further dispatch; it does not promise an accepted
Arr command stopped or completed effects rolled back. A failed later step does
not turn earlier verified effects into failure or hide them.

## Browser interaction and accessibility

Use native forms and semantic public components. Standard HTML navigation works;
HTMX enhances partial updates. Keep outer shell stable, preserve URL selection,
focus destination and form values after swaps. Do not rely on a disabled button
for server enforcement. Refreshing or opening a deep link recovers API-owned
workflow state. Repeated submit and lost response reuse the same idempotency key.

Use labelled inputs, field-linked error text, keyboard access, visible focus,
accessible dialogs with focus restoration, and status announcements. Never rely
only on color for tracking/conflict/seeding state. Tables need headers and a
usable narrow-screen form. Destructive confirmation states exact scope in words.
Support reduced motion, contrast and zoom. Do not leak secrets into HTML, browser
storage, metadata, analytics, error fragments or screenshot fixtures.

U-03 creates an adversarial ledger with columns route/state, action/request,
allowed, expected response, preserved input/context, focus/destination, effect
count. Every consequential action gets allowed, denied/stale and repeated cases.
Test direct forged HTTP requests too. Include real transport interruption,
upstream error, fragment error behavior under selected HTMX version, retry,
back/forward, reload, modal escape and keyboard-only confirmation.

## Initial HTML and preview metadata

Render title, description, absolute route canonical, Open Graph metadata and an
explicit X card in initial HTML. Use configured public origin, never request Host
or private media filenames. Generic product metadata and static preview image
avoid disclosing inventory. Strip filters/private identifiers from shared metadata
where they would expose content; canonical routing policy must be tested per page.
Use noindex/nofollow and no-store for this private operational dashboard.

Ship an actual public-safe image asset, with correct type/dimensions and alt text,
and verify it loads from the configured origin. No screenshot of real inventory
as a social image. Local CI tests use a configured fixture origin. Production
origin/image reachability is a separate deployment gate; no real domain or live
preview is asserted by this specification.

## UI completion evidence

- Test routes at 390px and 1440px, light/dark, Goshtoso and Minimal themes where
  supported by the pinned public API. Record unsupported theme limits explicitly.
- Browser-check identity correction, registration/import approvals, cancellation,
  retries, trash/restore/purge, file-owned config and secret replacement.
- Verify accessible names, focus, keyboard, contrast, no console errors, initial
  HTML metadata, local runtime assets, and no missing preview image.
- Run UI module checks with GOWORK=off and prove no root internal/DB/upstream imports.
- Record exact browser states and effect counts in acceptance evidence. A mock
  HTML screenshot alone does not prove the workflow or API safety boundary.
