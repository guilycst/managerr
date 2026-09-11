# HTTP API contract reference

The v0.0.1 wire contract lives in [api/openapi.yaml](../../../api/openapi.yaml),
created by C-01. This document keeps the resource rationale and acceptance
constraints. Contract changes update both files and affected acceptance cases
before regenerating consumers.

## Conventions

- Base path `/api/v1`; JSON requests/responses, UTF-8. Dates are RFC 3339 UTC.
- Runtime resource IDs are opaque UUID strings. Configuration connection, root
  and mapping IDs are stable constrained strings such as `radarr-main`; upstream
  IDs remain strings scoped by connectionId. File targets use a configured
  rootId plus relativePath; never accept an unrestricted host path for an action.
- GET is read-only. POST creates resources or durable requests. PATCH changes
  application-owned configuration with an explicit field mask/merge contract.
  DELETE removes application config only, never implicitly deletes media.
- List endpoints return `items`, `nextCursor`, `coverage`, `observedAt`.
  Opaque cursors preserve a stable ordering/snapshot. Default limit 50, maximum
  200. An expired inventory cursor returns `409 snapshot_expired`.
- Async creation returns 202, Location, and a durable resource. A synchronous
  resource creation returns 201 and Location. GET of a pending job returns 200.
- No required Authorization header, login, session identity, or security scheme
  in v0.0.1. `actor` is server-set `unauthenticated`, with optional caller label
  explicitly marked unverified. Proxy identity headers are not trusted.
- Require Idempotency-Key on mutation-producing request creation and decisions.
  Persist request digest and resource reference. Same key+same canonical request
  returns the original result; same key+different request returns 409.
  Keys are scoped by route family and instance, not by invented user identity.
- Configuration writes use ETag/If-Match. Immutable plan decisions use planId,
  revision and digest. Require preconditions rather than accepting last-write-wins.
- Reject unknown write fields, illegal combinations, oversized bodies, and
  unsupported discriminators. Read responses may add optional fields compatibly.
- `Cache-Control: no-store` for inventory, credentials metadata, actions, and BFF
  pages. Descriptors never appear inline in ordinary resource responses.
- Configuration responses expose source metadata (`yaml` or `api`, editable flag,
  source document identity, revision, startup time and restart-required reload
  policy) and ETags for optimistic concurrency. Effective storage-root path and
  watch settings are returned, while credential values are never returned.

## Resources

| Routes | Contract |
| --- | --- |
| GET `/health/live`, `/health/ready` outside API prefix | Process live; DB/migrations/key ready. Upstream outage degrades connection health without restarting API. |
| GET/POST `/connections` | List or create managed connection; kind, label, endpoint, explicit credentials input. Source metadata included. |
| GET/PATCH/DELETE `/connections/{id}` | Read, patch with If-Match, or retire managed connection. Block removal used by active work unless cancelled/resolved. Never delete history. |
| POST `/connection-checks` | Read-only upstream capability/version/connectivity check for a saved connection; durable result with sanitized errors. |
| GET `/connection-checks/{id}` | Check progress and discovered capabilities. |
| GET/POST `/storage-roots` | Configured mounted roots, purpose download/library/descriptor/trash, permissions/capabilities and watch settings where relevant. |
| GET/PATCH/DELETE `/storage-roots/{id}` | Managed root configuration, optimistic concurrency, retained historical references. Static roots immutable. |
| GET/POST `/path-mappings` | Explicit connection path namespace mappings to storage roots. |
| GET/PATCH/DELETE `/path-mappings/{id}` | Managed mapping CRUD. No file move or remote path repair as a side effect. |
| GET `/configuration` | Effective nonsecret configuration, source/revision, startup time, key source metadata, reload policy. |
| POST/GET `/scans` | Create or list root/client/catalog observation jobs; manual trigger coalesces per active scope. |
| GET `/scans/{id}` | Per-source progress, completeness, errors, cancellation and follow-up status. |
| POST `/scans/{id}/cancellations` | Durable request to stop scan work; partial results stay partial. |
| GET `/discoveries`, `/discoveries/{id}` | Files, groupings, readiness, provenance, candidate identities, per-instance relationships. |
| GET `/media`, `/media/{id}` | Aggregated media identities with all instance registrations, files, requests and availability observations. |
| GET `/downloads`, `/downloads/{id}` | Client-scoped records, seeding/completion metadata, descriptor links and observations. |
| GET `/metadata-candidates` | Search via chosen Arr instance, kind and text/provider ID. Suggestions only; no search-for-release operation. |
| GET `/connections/{id}/options` | Current valid root folders, profiles, series types, seasons/episodes as supported; typed and timestamped. |
| POST/GET `/action-plans` | Create read-only exact preview or list plans. May return pending plan while evidence collection completes. Every mutation plan requires review or irreversible approval. |
| GET `/action-plans/{id}` | Immutable revision once ready; failed preview has issues and no executable approval. |
| POST `/action-plans/{id}/revisions` | Create a new immutable preview using corrected input. Does not alter an approved revision. |
| POST `/review-decisions` | Approve/reject a ready exact revision; creates action-run atomically on approval and returns its ID. |
| GET `/review-decisions/{id}` | Decision and exact approved scope, unauthenticated attribution. |
| GET `/action-runs`, `/action-runs/{id}` | State, desired state, effects, attempts, next retry, unresolved observations. |
| GET `/action-runs/{id}/attempts` | Paginated attempt journal with sanitized result/evidence. |
| POST `/action-runs/{id}/cancellations` | Durable cancellation resource, 202 even when external outcome still unresolved. |
| POST `/action-runs/{id}/reconciliations` | Request fresh read-only evidence; cannot authorize mutation or broaden a plan. |
| POST `/action-runs/{id}/retry-requests` | Explicit retry request for a retryable unchanged intent. API revalidates; uncertain writes reconcile first. |
| GET/POST `/workflow-runs` | List/create an ordered recipe with explicit saved action-plan references. The server derives approval gates from those plans; the client cannot mark a step approved. |
| GET `/workflow-runs/{id}` | Ordered steps, decisions needed, completed/failed/blocked state and current observations. |
| POST `/workflow-runs/{id}/cancellations` | Cancel undispatched steps, pending retries and future mutations; preserve observations. |
| GET `/trash`, `/trash/{id}` | Manifest, original paths, expiration, client associations, holds and restore/purge capabilities. |
| GET `/descriptors`, `/descriptors/{id}` | Metadata only: type, size, digest, availability, capture source/date, retention. |
| GET `/descriptors/{id}/content` | Explicit original-byte download, attachment, no-store, nosniff; sensitive content never BFF embedded. |
| GET `/audit-events` | Cursor-paginated decisions, config changes, attempts, effects, cancellation, janitor events. |

Single actions execute through plan plus review-decision even without a workflow.
There is no monolithic reconcile endpoint and no endpoint that executes arbitrary
shell commands. Direct API hard delete means a standalone `fs.delete` plan and
approval; it bypasses trash retention, not scope validation or explicit intent.
Every async subresource returns a durable ID, parent ID, state and timestamps;
its Location is retrievable under its parent collection. Repeated cancellation
converges on one effective request.

## Principal schemas

`Coverage`: source/connection/root IDs, startedAt, completedAt, completeness
`complete|partial|unknown`, reason codes, observed count, snapshot revision.

`CredentialInput` accepts one typed variant, `api_key`, `username_password` or
`token`. Secret fields are write-only and only credential state metadata appears
in responses.

`TrackingObservation`: connectionId, dimension
`registration|import|availability|request`, value `present|absent|unknown`,
external IDs, provider IDs, observedAt, coverageId, evidence summaries.

`FileManifestEntry`: rootId, relativePath, type, size, observed file identity,
content digest when required, mtime as observation, selected role
`video|subtitle|companion`, association IDs. Internal file descriptors are not
wire values. Manifests are exact, bounded lists, never wildcard promises.

`ActionPlan`: id, revision, digest, status `preparing|ready|invalid|expired`,
createdAt, expiresAt, inputs, desiredState, manifest, preconditions,
connectionRevisions, mappingRevisions, capabilities, impacts, conflicts,
blockingIssues, estimatedBytes and requiredApproval. Conflicts prohibit ready
execution until resolved with a new plan. Digest excludes volatile display text
but includes every authority-bearing input and relevant configuration revision.

`ReviewDecision`: id, planId, revision, digest, decision `approve|reject`,
explicit irreversible acknowledgement where needed, createdAt, actor,
unverifiedLabel, resultingActionRunId. Client cannot set authoritative actor.

`ActionRun`: id, plan reference, optional workflow/step ID, state
`queued|running|waiting_dependency|reconciling|needs_review|succeeded|failed|cancelled|deadline_exceeded`,
outcome, retryPolicy, retryReason, deadline, cancellation, effects,
unresolvedEffects, lastObservation, nextAttemptAt, attempts URL. Outcome `applied|already_satisfied` is populated only
when desired-state evidence supports success. A cancellation can coexist with
verified effects; no rollback claim is encoded in a terminal label.

`WorkflowRun`: id, recipe name/version, ordered step IDs, server-derived approval
gates, currentStep, deadlineAt, state
`awaiting_approval|running|waiting_dependency|needs_review|succeeded|failed|cancelled|deadline_exceeded`, aggregate effect count and unresolved count. Supported
recipes are Arr import, library placement, organize, trash, restore and purge.
A client may compose supported action kinds in an ordered list. API rejects
invalid dependencies, unapproved destructive steps and an import preapproved
before a missing title's registration/preview phase is resolved.

## Action kinds and input constraints

| Kind | Required intent | Additional boundary |
| --- | --- | --- |
| `arr.registration` | connectionId, mediaKind, providerId, and a nonempty explicitly desired registration fields object | New monitoring defaults false; never search automatically. |
| `arr.import` | connectionId, registered external title ID, exact file/episode map, native preview revision, and required transfer behavior | No arbitrary upstream command payload; require tested capabilities. |
| `fs.copy` | source manifest and exact destinations | Verify content; preserve source. |
| `fs.hardlink` | selected regular files and destinations | Same filesystem; never copy fallback. |
| `fs.move` / `fs.rename` | exact source/destination map and selected executor (`mastarr` or `native_client`) | Linked torrents use supported native API; cross-device requires separate approved copy/delete steps. |
| `client.stop` | qBittorrent connection/item IDs | Independent action; verify stopped state. Zero upload speed is insufficient. |
| `client.remove` | qBittorrent connection/item IDs, retainPayload=true | Metadata-only removal, explicit lost-tracking impact. No deleteFiles=true. |
| `fs.trash` | exact payload manifest, original paths, retention, proven stopped-client prerequisites | Creates per-volume trash entries. Original approval includes expiry purge policy. |
| `fs.restore` | trash entry/manifest, exact destinations | Check collisions; leave clients stopped. |
| `fs.delete` | exact live or trash manifest, permanent=true, irreversible acknowledgement | UI restricts to trash; API permits live scope. Required client removal is explicit dependent work. |
| `descriptor.delete` | exact retained descriptor IDs, irreversible acknowledgement | Separate from payload deletion; retain minimal audit metadata. |
| `jellyfin.refresh` | connectionId and exactly one closed scope variant: `scope: library`, or `scope: item` with required itemId | Does not claim scan completion or availability. |

Typed action handlers implement plan, observe, execute, reconcile and capability
checks. Add new typed action kinds through contracts and acceptance cases. Do not
invent a generic plugin runtime. Prerequisite steps remain independently callable,
but a low-level filesystem action still refuses an unsafe client association.
The composition shows stop, trash or delete, and client removal as separate effects.

## Errors, concurrency and deadlines

Errors use `application/problem+json`: type, title, status, detail, code,
requestId, optional resourceId, fieldErrors, retryable and nextAction. Sanitize
upstream bodies, URL query strings, credentials, and filesystem internals that
are outside the configured resource namespace.

| Status/code | Meaning |
| --- | --- |
| 400 `invalid_request` | Malformed or unsupported fields. |
| 404 `not_found` | Unknown resource, including guessed IDs. |
| 409 `stale_plan`, `state_conflict`, `idempotency_conflict` | Reviewed input changed, wrong lifecycle state, or key reused with changed intent. |
| 409 `config_source_read_only`, `resource_in_use` | File-owned resource or live references prohibit requested config write. |
| 412 `revision_mismatch` / 428 `precondition_required` | Configuration optimistic concurrency failure or missing condition. |
| 413 `request_too_large` | Body or manifest exceeds declared bounds. |
| 422 `unsupported_capability`, `mapping_ambiguous`, `destination_conflict`, `evidence_incomplete` | Cannot safely plan requested action. |
| 429 `capacity_limited` | Bounded local admission; Retry-After supplied. |
| 503 `dependency_unavailable` | Required immediate read unavailable; durable jobs expose waiting state instead. |

Plan expiry defaults to 15 minutes before approval. Once approved, work waits
through temporary outages by default. Approval age alone does not expire an
accepted action, but source identity, desired fields, capabilities and relevant
config revisions are checked before dispatch. Changed intent needs a new plan.
A workflow deadline prevents further mutation dispatch when exceeded; it does
not erase late results or stop read-only reconciliation of accepted requests.

Default external HTTP timeout is 30 seconds. Transfer attempts use a configurable
inactivity timeout plus cancellation and progress checkpoints rather than a
30-second whole-file limit. Backoff starts at 5 seconds, doubles to a 15-minute
cap with jitter, and is persisted across restart. These are selected engineering
defaults, not claims about upstream latency. See [recovery](data-and-recovery.md).

## BFF contract and tests

BFF may post forms and render HTML but every effect goes through these resources.
It never invents success from an HTTP 202. Lost BFF responses retry with the same
key and recover the same action. URLs identify selected discovery/media/run;
fragment navigation and direct navigation render equivalent state.

Apply [acceptance cases](../../verification/acceptance.md) for forged requests,
stale approvals, repeat submissions, conflicting config revisions, incomplete
coverage, no-auth attribution, and browser failure/recovery. Authentication
contracts are explicitly deferred to v0.0.2.
