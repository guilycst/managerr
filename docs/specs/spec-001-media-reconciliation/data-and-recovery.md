# Data, execution and recovery

Future v0.0.1 design. SQLite is the authoritative local journal. Upstream systems
and the filesystem remain authoritative for their actual state. No transaction
spans SQLite and an external side effect.

## Persistence model

Use sqlc for SQL access and golang-migrate for ordered schema migrations. Enable
foreign keys, WAL and an explicit busy timeout; use FULL synchronous durability
for approval/effect records. Pin and test the selected SQLite driver with migrate
before freezing it. One active API/executor owns the database, enforced by a
process lock. Network calls and filesystem copies never run inside SQL transactions.

| Records | Required content and constraints |
| --- | --- |
| connections, storage_roots, path_mappings | Stable ID, source yaml/api, source/config revision, effective nonsecret spec, tombstone; unique IDs and valid foreign keys. |
| encrypted_credentials | Connection ID, envelope version, nonce/ciphertext, key fingerprint; never plaintext. |
| scans, coverage_snapshots | Per-source start/end, completeness, cursor/progress and errors; incomplete snapshots cannot prove absence. |
| discoveries, file_observations | Root-scoped paths, file identity and timestamps, manifest revisions, first/last seen. Retain replaced/deleted observations as history. |
| downloads, provenance_links | Unique connection+external ID, protocol/hash/history identifiers, completion/seeding metadata, evidence. Many-to-many discovery links. |
| media_identities, external_records, tracking_observations | Provider IDs, connection-scoped title/file/request IDs, dimension, evidence, coverage reference. |
| descriptors | Type, private relative storage object, original digest, capture source/time, retention, unavailable reason. |
| action_plans, plan_manifests | Immutable ready revision, digest, intended fields/objects, preconditions, capabilities and expiry. |
| review_decisions | Exact plan revision/digest, approve/reject, unauthenticated attribution and optional unverified label. |
| workflow_runs, workflow_steps | Versioned ordered recipe, action/gate references, current state, optional deadline/cancellation. No arbitrary DAG schema. |
| action_runs | Approved intent, state, next attempt, cancellation/deadline, outcome, lease/version. This is the durable work queue. |
| action_attempts, action_effects | Dispatch journal, attempt sequence, upstream IDs, outcome certainty, verified per-object effects and evidence. |
| idempotency_records | Unique route-family+key, canonical request digest, original status/resource reference. |
| trash_entries, trash_items | Exact manifest, original/trash path, identities, trashedAt, expiresAt, client associations, retention/restore/purge state and holds. |
| audit_events | Append-only local decision/config/attempt/effect history with redaction. Not tamper-proof or authenticated user evidence. |

Selected retention defaults: preserve decisions, effect audit, descriptors and
idempotency tombstones until an explicit supported deletion policy is added.
Payload purge does not automatically purge descriptors or erase decisions.
Descriptor bytes can be independently deleted through a reviewed action; retain
nonsecret digest/provenance metadata. Diagnostic logs and raw scan detail may use
bounded retention, but never prune evidence needed by active work or trash restore.
Expose retained storage size. A future janitor for metadata is separate scope.

## State machines

Action states:

| State | Meaning / next allowed transitions |
| --- | --- |
| queued | Approved; worker may observe, then running, succeeded, waiting_dependency, needs_review or cancelled. |
| running | Attempt journal committed; execute/reconcile into succeeded, waiting_dependency, reconciling, needs_review or failed. |
| waiting_dependency | Known temporary failure before effect or proven safe retry. Re-observe when due; cancellation/deadline stops dispatch. |
| reconciling | Effect may have occurred. Read-only evidence collection; succeeds if verified, returns to queue only after safe non-effect proof, otherwise remains unresolved or needs_review. |
| needs_review | Conflict, changed scope/config, unsupported capability or invalid credentials. New approval or explicit same-intent retry can unblock as appropriate. |
| succeeded | Desired state proven, outcome applied or already_satisfied. Terminal execution state. |
| failed | Known nonretryable failure; completed effects remain recorded. A new plan can address remaining work. |
| cancelled | No further mutation dispatch. Accepted external effects may still be observed in separate resolution status. |
| deadline_exceeded | Same dispatch boundary as cancelled, with deadline cause. Late effects remain visible. |

Workflow states are awaiting_approval, running, waiting_dependency, needs_review,
succeeded, failed, cancelled or deadline_exceeded. Step states distinguish blocked
from failed and skipped. A completed prerequisite never silently approves the next
gate. A cancelled workflow cannot acquire newly approved steps. Terminal execution
state and unresolved-effect count are independent so the UI cannot hide uncertainty.

## Transaction and worker protocol

1. Validate decision against ready, unexpired plan revision and current binding.
2. In one short transaction insert decision, deduplicated action, workflow link
   and audit event. A crash commits all or none.
3. Claim due work with compare-and-set version/lease. Observe desired state before
   any mutation. If exact predicate holds, record already_satisfied and no dispatch.
4. Revalidate path identities, relevant config, approval scope and cancellation.
   Reserve overlapping source/destination paths and client items against other
   Managerr actions. Overlap includes directory descendants and hardlinked objects.
5. Persist attempt and dispatch intent before calling an external API or changing
   files. Record each successful effect before advancing dependent steps.
6. Read back desired state, then commit success. Submission and command completion
   are evidence inputs, not the final predicate.

Worker lease expiry permits recovery, not blind replay. On startup, previously
running dispatches become reconciling. Cancellation uses the same transactional
dispatch claim boundary. It cannot retract a call already claimed/accepted. The
worker checks cancellation between bounded chunks/steps and reports any late
result. Read-only observation can continue after cancellation or deadline.

Retry only known safe cases. Connection refused before sending a write can wait;
a response lost after sending is uncertain. Timeout, broken connection and ambiguous
upstream 5xx after dispatch do not prove no effect. Reconcile provider identity,
command/history and file evidence. If no safe determination is possible, require
intervention; cancellation remains available. Never promise exactly-once external
API effects. Request-key dedupe and semantic desired-state checks solve different
problems and are both required.

API/BFF restarts do not reset deadlines/backoff or lose queued work. A temporary
outage waits without an overall limit unless the workflow specifies one. Invalid
credentials, path conflicts and stale approval inputs need intervention. Fixing
credentials can re-enable the same intent if only secret material changed and
target identity is reverified; endpoint/root/identity changes require new approval.

No automatic rollback. Explicit follow-up actions may reverse recoverable effects.
An outbox is unnecessary because action_runs is polled from the same database.
Add a separate transactional outbox only when committed events must be delivered
to another process or webhook. It would not make upstream writes transactional.

## Filesystem execution

Resolve configured roots once and use root-confined, descriptor-relative operations
with no-follow semantics where the supported OS permits. A string prefix or
check-then-open realpath comparison is not a confinement guarantee. Reject symlinks,
special files, traversal, root targets and ambiguous path mappings. Recheck identity
at execution and after transfer; fail closed on changed children or replaced files.
Permissions reflect effective UID/GID and current mounts, not a promise from preview.

For copy, create an exclusive staging file beside the intended destination, copy
in bounded chunks, verify content digest and source stability, sync data, then
publish with an atomic no-replace operation supported by the platform. Sync parent
directory before recording durable completion. If destination exists, inspect for
already-satisfied content or raise conflict. Never replace it with ordinary rename.
A crash between publication and DB update is recovered by identity/digest evidence.
Staging names carry opaque operation IDs; only proven app-owned staging is cleaned.

For hardlink, require same-filesystem regular files and verify source/destination
object identity. Existing identical content in a different inode is not a satisfied
hardlink. EXDEV/unsupported returns needs_review for a separately approved copy.
For same-filesystem move/rename, use no-replace semantics and verify the old path
is absent and new object matches. For cross-filesystem moves, the explicit workflow
is copy, verify and separately authorized source removal. Interrupted removal must
never destroy the only verified copy.

Full SHA-256 or equally strong evidence is required when claiming different files
have identical bytes. Size/name/mtime alone are insufficient. Native Arr imports
may rely on verified upstream file associations plus mapped filesystem identity
where supported; weaker evidence must stay unresolved. Directory plans enumerate
all affected children. Resource bounds reject an oversized plan and allow reviewed
batches; they do not silently truncate its deletion scope.

External programs can change files concurrently. Managerr's reservations serialize
only its own work. Unsupported filesystem atomicity or inability to establish safe
source access blocks the affected action. See compatibility gates for Arr/client
operations that cannot offer atomic no-replace or external locking.

## Trash, janitor and torrent coordination

Default trash is an application-owned hidden directory on each affected filesystem,
configured under an allowed writable root and excluded from discovery. Use unique
entry IDs and preserve an exact manifest/original destination. Same-filesystem
rename avoids assuming another volume has space. Cross-device trash requires a
reviewed copy-verify-delete sequence. Reject destination collisions and nested trash.

Retention starts when payload is verifiably in trash. Default is 30 times 24 hours
in UTC, configurable per policy. Janitor checks hourly by default and on startup;
expiry grants eligibility, not an exact wall-clock deletion guarantee. Never purge
early. Original trash approval binds its expiry and automatic purge policy. Clock
anomalies must not silently shorten recorded retention; log and hold suspect work.

Before trash/delete of a known qBittorrent association, execute and verify a stop
step for every affected torrent. Keep records stopped while any selected payload
is in trash. Recheck before filesystem mutation; externally resumed torrents pause
work. Where safe mutation cannot be established, refuse rather than unlink active
payload. qBittorrent native relocation/rename support is capability-gated. Do not
assume changing its displayed name changes files.

Purge claims an expired or explicitly approved entry with a transactional state
change. Restore and purge cannot both claim it. Missing/replaced identities,
unresolved effects, operational holds, or uncertain client state prevent
janitor mutation and produce a visible reason. A hold records its reason and the evidence or intervention required to resume.

Purge deletes only the selected manifest and removes associated torrent records
with deleteFiles=false. These are separate recoverable steps. If either fails,
retain the journal and retry/reconcile only the unfinished effect. Missing torrent
record is already satisfied. For partial torrents, retain remaining payload;
preview explicitly discloses that its client record will disappear. Another trash
entry referencing the same torrent must tolerate record absence and preserve its
own recovery manifest. Never use torrent deletion to widen file scope.

Restore checks original-path collisions, claims the entry against janitor, restores
exact selected objects, and clears only their purge eligibility. Restore leaves
seeding stopped. A removed torrent record is not automatically re-added. File and
client states remain distinct in the UI. Partial restore/purge retains per-item
status so remaining trash stays discoverable and recoverable.

## Startup, migrations and backup

Acquire instance lock, load/validate bootstrap and YAML configuration, verify key
availability, migrate DB with backup policy, then start listeners/workers. Do not
serve ready while migrations or key checks fail. Migration failure stops workers;
never automatically downgrade schema. Test migration from every supported previous
schema, interrupted migration handling, and sqlc query compatibility.

Use SQLite online backup or stop the writer and copy a consistent database. Copying
only a live main DB while ignoring WAL is invalid. Recovery requires database,
credential key, retained descriptors and trash manifests/payload locations, plus
matching static config and mounts. Back up these with a documented consistency
procedure; key recovery is mandatory for encrypted credentials.

Restore into an isolated instance with fake upstreams and writable fixture roots.
Verify decryption, pending action recovery, uncertainty preservation, idempotency,
trash restore and no duplicate effects before claiming recoverability. Actual
backup destination and live restore are deployment choices, not inferred here.
