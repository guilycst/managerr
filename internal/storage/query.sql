-- Configuration snapshots and source-owned resources.

-- name: CreateConfigSnapshot :one
INSERT INTO config_snapshots (
    id, source, document_id, revision, startup_at, effective_json, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(source), sqlc.arg(document_id), sqlc.arg(revision),
    sqlc.arg(startup_at), sqlc.arg(effective_json), sqlc.arg(created_at)
)
RETURNING *;

-- name: GetConfigSnapshot :one
SELECT * FROM config_snapshots WHERE id = sqlc.arg(id);

-- name: ListConfigSnapshots :many
SELECT * FROM config_snapshots ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: CreateConnection :one
INSERT INTO connections (
    id, kind, label, endpoint, source, source_snapshot_id, revision,
    retired_at, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(kind), sqlc.arg(label), sqlc.arg(endpoint),
    sqlc.arg(source), sqlc.arg(source_snapshot_id), sqlc.arg(revision),
    sqlc.arg(retired_at), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetConnection :one
SELECT * FROM connections WHERE id = sqlc.arg(id);

-- name: ListConnections :many
SELECT * FROM connections
WHERE (sqlc.arg(include_retired) = 1 OR retired_at IS NULL)
ORDER BY id LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: RetireConnection :one
UPDATE connections
SET retired_at = sqlc.arg(retired_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND retired_at IS NULL
RETURNING *;

-- name: CreateStorageRoot :one
INSERT INTO storage_roots (
    id, label, purpose, path, source, source_snapshot_id, revision,
    read_only, watch_enabled, watch_interval_seconds, capabilities_json,
    retired_at, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(label), sqlc.arg(purpose), sqlc.arg(path),
    sqlc.arg(source), sqlc.arg(source_snapshot_id), sqlc.arg(revision),
    sqlc.arg(read_only), sqlc.arg(watch_enabled), sqlc.arg(watch_interval_seconds),
    sqlc.arg(capabilities_json), sqlc.arg(retired_at), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetStorageRoot :one
SELECT * FROM storage_roots WHERE id = sqlc.arg(id);

-- name: ListStorageRoots :many
SELECT * FROM storage_roots
WHERE (sqlc.arg(include_retired) = 1 OR retired_at IS NULL)
ORDER BY id LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: RetireStorageRoot :one
UPDATE storage_roots
SET retired_at = sqlc.arg(retired_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND retired_at IS NULL
RETURNING *;

-- name: CreatePathMapping :one
INSERT INTO path_mappings (
    id, connection_id, root_id, source_prefix, destination_prefix, source,
    source_snapshot_id, revision, retired_at, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(connection_id), sqlc.arg(root_id),
    sqlc.arg(source_prefix), sqlc.arg(destination_prefix), sqlc.arg(source),
    sqlc.arg(source_snapshot_id), sqlc.arg(revision), sqlc.arg(retired_at),
    sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetPathMapping :one
SELECT * FROM path_mappings WHERE id = sqlc.arg(id);

-- name: ListPathMappings :many
SELECT * FROM path_mappings
WHERE (sqlc.arg(include_retired) = 1 OR retired_at IS NULL)
ORDER BY id LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: RetirePathMapping :one
UPDATE path_mappings
SET retired_at = sqlc.arg(retired_at), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND retired_at IS NULL
RETURNING *;

-- name: PutEncryptedCredential :one
INSERT INTO encrypted_credentials (
    connection_id, name, envelope_version, nonce, ciphertext, key_fingerprint,
    created_at, updated_at
) VALUES (
    sqlc.arg(connection_id), sqlc.arg(name), sqlc.arg(envelope_version),
    sqlc.arg(nonce), sqlc.arg(ciphertext), sqlc.arg(key_fingerprint),
    sqlc.arg(created_at), sqlc.arg(updated_at)
)
ON CONFLICT (connection_id, name) DO UPDATE SET
    envelope_version = excluded.envelope_version,
    nonce = excluded.nonce,
    ciphertext = excluded.ciphertext,
    key_fingerprint = excluded.key_fingerprint,
    updated_at = excluded.updated_at
RETURNING *;

-- name: GetEncryptedCredential :one
SELECT * FROM encrypted_credentials
WHERE connection_id = sqlc.arg(connection_id) AND name = sqlc.arg(name);

-- name: ListEncryptedCredentials :many
SELECT connection_id, name, envelope_version, key_fingerprint, created_at, updated_at
FROM encrypted_credentials WHERE connection_id = sqlc.arg(connection_id) ORDER BY name;

-- Discovery and coverage observations.

-- name: CreateScan :one
INSERT INTO scans (
    id, scope_kind, scope_id, state, completeness, cursor, observed_count,
    started_at, completed_at, config_revision, error_code, error_detail,
    created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(scope_kind), sqlc.arg(scope_id), sqlc.arg(state),
    sqlc.arg(completeness), sqlc.arg(cursor), sqlc.arg(observed_count),
    sqlc.arg(started_at), sqlc.arg(completed_at), sqlc.arg(config_revision),
    sqlc.arg(error_code), sqlc.arg(error_detail), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetScan :one
SELECT * FROM scans WHERE id = sqlc.arg(id);

-- name: UpdateScan :one
UPDATE scans SET
    state = sqlc.arg(state), completeness = sqlc.arg(completeness),
    cursor = sqlc.arg(cursor), observed_count = sqlc.arg(observed_count),
    started_at = sqlc.arg(started_at), completed_at = sqlc.arg(completed_at),
    error_code = sqlc.arg(error_code), error_detail = sqlc.arg(error_detail),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CreateCoverageSnapshot :one
INSERT INTO coverage_snapshots (
    id, scan_id, source_id, connection_id, root_id, media_identity_id, completeness,
    reason_codes_json, observed_count, snapshot_revision, started_at,
    completed_at, observed_at, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(scan_id), sqlc.arg(source_id), sqlc.arg(connection_id),
    sqlc.arg(root_id), sqlc.arg(media_identity_id), sqlc.arg(completeness), sqlc.arg(reason_codes_json),
    sqlc.arg(observed_count), sqlc.arg(snapshot_revision), sqlc.arg(started_at),
    sqlc.arg(completed_at), sqlc.arg(observed_at), sqlc.arg(created_at)
)
RETURNING *;

-- name: GetCoverageSnapshot :one
SELECT * FROM coverage_snapshots WHERE id = sqlc.arg(id);

-- name: ListCoverageSnapshots :many
SELECT * FROM coverage_snapshots
WHERE (sqlc.arg(connection_id) IS NULL OR connection_id = sqlc.arg(connection_id))
  AND (sqlc.arg(root_id) IS NULL OR root_id = sqlc.arg(root_id))
ORDER BY observed_at DESC, id DESC LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: CreateDiscovery :one
INSERT INTO discoveries (
    id, root_id, relative_path, entry_type, size_bytes, digest, file_identity,
    role, observed_at, first_seen_at, last_seen_at, manifest_revision,
    child_manifest_json, deleted_at
) VALUES (
    sqlc.arg(id), sqlc.arg(root_id), sqlc.arg(relative_path), sqlc.arg(entry_type),
    sqlc.arg(size_bytes), sqlc.arg(digest), sqlc.arg(file_identity), sqlc.arg(role),
    sqlc.arg(observed_at), sqlc.arg(first_seen_at), sqlc.arg(last_seen_at),
    sqlc.arg(manifest_revision), sqlc.arg(child_manifest_json), sqlc.arg(deleted_at)
)
RETURNING *;

-- name: GetDiscovery :one
SELECT * FROM discoveries WHERE id = sqlc.arg(id);

-- name: ListDiscoveries :many
SELECT * FROM discoveries
WHERE (sqlc.arg(root_id) IS NULL OR root_id = sqlc.arg(root_id))
ORDER BY last_seen_at DESC, id DESC LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: CreateFileObservation :one
INSERT INTO file_observations (
    id, discovery_id, root_id, relative_path, entry_type, size_bytes, digest,
    file_identity, role, observed_at, manifest_revision, child_manifest_json,
    first_seen_at, last_seen_at, deleted_at
) VALUES (
    sqlc.arg(id), sqlc.arg(discovery_id), sqlc.arg(root_id), sqlc.arg(relative_path),
    sqlc.arg(entry_type), sqlc.arg(size_bytes), sqlc.arg(digest),
    sqlc.arg(file_identity), sqlc.arg(role), sqlc.arg(observed_at),
    sqlc.arg(manifest_revision), sqlc.arg(child_manifest_json),
    sqlc.arg(first_seen_at), sqlc.arg(last_seen_at), sqlc.arg(deleted_at)
)
RETURNING *;

-- name: ListFileObservations :many
SELECT * FROM file_observations
WHERE discovery_id = sqlc.arg(discovery_id)
ORDER BY observed_at ASC, id ASC;

-- Download provenance and descriptors.

-- name: CreateDownload :one
INSERT INTO downloads (
    id, connection_id, external_id, protocol, name, content_hash, nzb_id,
    state, completed_at, seeding_state, payload_json, history_json,
    first_seen_at, last_seen_at
) VALUES (
    sqlc.arg(id), sqlc.arg(connection_id), sqlc.arg(external_id), sqlc.arg(protocol),
    sqlc.arg(name), sqlc.arg(content_hash), sqlc.arg(nzb_id), sqlc.arg(state),
    sqlc.arg(completed_at), sqlc.arg(seeding_state), sqlc.arg(payload_json),
    sqlc.arg(history_json), sqlc.arg(first_seen_at), sqlc.arg(last_seen_at)
)
RETURNING *;

-- name: GetDownload :one
SELECT * FROM downloads WHERE id = sqlc.arg(id);

-- name: GetDownloadByExternalID :one
SELECT * FROM downloads
WHERE connection_id = sqlc.arg(connection_id) AND external_id = sqlc.arg(external_id);

-- name: ListDownloads :many
SELECT * FROM downloads
WHERE (sqlc.arg(connection_id) IS NULL OR connection_id = sqlc.arg(connection_id))
ORDER BY last_seen_at DESC, id DESC LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: CreateProvenanceLink :one
INSERT INTO provenance_links (
    id, discovery_id, download_id, evidence_json, observed_at, first_seen_at, last_seen_at
) VALUES (
    sqlc.arg(id), sqlc.arg(discovery_id), sqlc.arg(download_id), sqlc.arg(evidence_json),
    sqlc.arg(observed_at), sqlc.arg(first_seen_at), sqlc.arg(last_seen_at)
)
ON CONFLICT (discovery_id, download_id) DO UPDATE SET
    evidence_json = excluded.evidence_json,
    observed_at = excluded.observed_at,
    last_seen_at = excluded.last_seen_at
RETURNING *;

-- name: ListProvenanceForDiscovery :many
SELECT pl.*, d.connection_id, d.external_id, d.protocol, d.state
FROM provenance_links AS pl
JOIN downloads AS d ON d.id = pl.download_id
WHERE pl.discovery_id = sqlc.arg(discovery_id)
ORDER BY pl.last_seen_at DESC;

-- name: CreateDescriptor :one
INSERT INTO descriptors (
    id, download_id, descriptor_type, storage_path, original_digest,
    capture_source, captured_at, retention, unavailable_reason, deleted_at
) VALUES (
    sqlc.arg(id), sqlc.arg(download_id), sqlc.arg(descriptor_type), sqlc.arg(storage_path),
    sqlc.arg(original_digest), sqlc.arg(capture_source), sqlc.arg(captured_at),
    sqlc.arg(retention), sqlc.arg(unavailable_reason), sqlc.arg(deleted_at)
)
RETURNING *;

-- name: GetDescriptor :one
SELECT * FROM descriptors WHERE id = sqlc.arg(id);

-- Media identities and all per-instance upstream records.

-- name: CreateMediaIdentity :one
INSERT INTO media_identities (
    id, kind, provider_namespace, provider_id, title, year, canonical_key,
    created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(kind), sqlc.arg(provider_namespace), sqlc.arg(provider_id),
    sqlc.arg(title), sqlc.arg(year), sqlc.arg(canonical_key), sqlc.arg(created_at),
    sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetMediaIdentity :one
SELECT * FROM media_identities WHERE id = sqlc.arg(id);

-- name: CreateExternalRecord :one
INSERT INTO external_records (
    id, connection_id, media_identity_id, record_kind, external_id, title,
    payload_json, observed_at, first_seen_at, last_seen_at
) VALUES (
    sqlc.arg(id), sqlc.arg(connection_id), sqlc.arg(media_identity_id),
    sqlc.arg(record_kind), sqlc.arg(external_id), sqlc.arg(title),
    sqlc.arg(payload_json), sqlc.arg(observed_at), sqlc.arg(first_seen_at),
    sqlc.arg(last_seen_at)
)
ON CONFLICT (connection_id, record_kind, external_id) DO UPDATE SET
    media_identity_id = excluded.media_identity_id,
    title = excluded.title,
    payload_json = excluded.payload_json,
    observed_at = excluded.observed_at,
    last_seen_at = excluded.last_seen_at
RETURNING *;

-- name: ListExternalRecords :many
SELECT * FROM external_records
WHERE connection_id = sqlc.arg(connection_id)
ORDER BY last_seen_at DESC, id DESC LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- name: CreateTrackingObservation :one
INSERT INTO tracking_observations (
    id, external_record_id, media_identity_id, connection_id, root_id, dimension, status,
    evidence_json, coverage_id, coverage_max_age_seconds, observed_at, registered_at, imported_at
) VALUES (
    sqlc.arg(id), sqlc.arg(external_record_id), sqlc.arg(media_identity_id),
    sqlc.arg(connection_id), sqlc.arg(root_id), sqlc.arg(dimension), sqlc.arg(status),
    sqlc.arg(evidence_json), sqlc.arg(coverage_id), sqlc.arg(coverage_max_age_seconds), sqlc.arg(observed_at),
    sqlc.arg(registered_at), sqlc.arg(imported_at)
)
RETURNING *;

-- name: ListTrackingObservations :many
SELECT * FROM tracking_observations
WHERE (sqlc.arg(external_record_id) IS NULL OR external_record_id = sqlc.arg(external_record_id))
  AND (sqlc.arg(media_identity_id) IS NULL OR media_identity_id = sqlc.arg(media_identity_id))
  AND (sqlc.arg(connection_id) IS NULL OR connection_id = sqlc.arg(connection_id))
ORDER BY observed_at DESC, id DESC;

-- name: ListTrackingObservationQuarantine :many
-- Quarantined compatibility rows are durable operator evidence, but are kept
-- separate from active tracking observations and must never satisfy a tracking
-- lookup or absence predicate.
SELECT * FROM tracking_observation_quarantine
ORDER BY observed_at DESC, id DESC;

-- Immutable plans, exact manifests, approvals and durable execution records.

-- name: CreateActionPlan :one
INSERT INTO action_plans (
    id, kind, state, current_revision, current_digest, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(kind), sqlc.arg(state), sqlc.arg(current_revision),
    sqlc.arg(current_digest), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetActionPlan :one
SELECT * FROM action_plans WHERE id = sqlc.arg(id);

-- name: UpdateActionPlanState :one
UPDATE action_plans SET state = sqlc.arg(state), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CreateActionPlanRevision :one
INSERT INTO action_plan_revisions (
    plan_id, revision, digest, state, input_json, preconditions_json,
    capabilities_json, manifest_json, created_at, expires_at, ready_at
) VALUES (
    sqlc.arg(plan_id), sqlc.arg(revision), sqlc.arg(digest), sqlc.arg(state),
    sqlc.arg(input_json), sqlc.arg(preconditions_json), sqlc.arg(capabilities_json),
    sqlc.arg(manifest_json), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.arg(ready_at)
)
RETURNING *;

-- name: GetActionPlanRevision :one
SELECT * FROM action_plan_revisions
WHERE plan_id = sqlc.arg(plan_id) AND revision = sqlc.arg(revision);

-- name: GetActionPlanRevisionByDigest :one
SELECT * FROM action_plan_revisions
WHERE plan_id = sqlc.arg(plan_id) AND revision = sqlc.arg(revision) AND digest = sqlc.arg(digest);

-- name: ListActionPlanRevisions :many
SELECT * FROM action_plan_revisions
WHERE plan_id = sqlc.arg(plan_id) ORDER BY revision DESC;

-- name: CreatePlanManifest :one
INSERT INTO plan_manifests (
    plan_id, revision, ordinal, root_id, relative_path, entry_type, size_bytes,
    digest, file_identity, role, manifest_json
) VALUES (
    sqlc.arg(plan_id), sqlc.arg(revision), sqlc.arg(ordinal), sqlc.arg(root_id),
    sqlc.arg(relative_path), sqlc.arg(entry_type), sqlc.arg(size_bytes),
    sqlc.arg(digest), sqlc.arg(file_identity), sqlc.arg(role), sqlc.arg(manifest_json)
)
RETURNING *;

-- name: CreateEarlyPurgePlanTarget :one
INSERT INTO early_purge_plan_targets (
    plan_id, revision, plan_digest, intent_kind, trash_entry_id,
    trash_entry_version, manifest_json, created_at
) VALUES (
    sqlc.arg(plan_id), sqlc.arg(revision), sqlc.arg(plan_digest),
    sqlc.arg(intent_kind), sqlc.arg(trash_entry_id), sqlc.arg(trash_entry_version),
    sqlc.arg(manifest_json), sqlc.arg(created_at)
)
RETURNING *;

-- name: GetEarlyPurgePlanTarget :one
SELECT * FROM early_purge_plan_targets
WHERE plan_id = sqlc.arg(plan_id) AND revision = sqlc.arg(revision);

-- name: ListEarlyPurgePlanTargets :many
SELECT * FROM early_purge_plan_targets
ORDER BY plan_id, revision;

-- name: ListPlanManifests :many
SELECT * FROM plan_manifests
WHERE plan_id = sqlc.arg(plan_id) AND revision = sqlc.arg(revision)
ORDER BY ordinal;

-- name: CreateReviewDecision :one
INSERT INTO review_decisions (
    id, plan_id, plan_revision, plan_digest, decision, actor, caller_label,
    reason, idempotency_scope, idempotency_key, created_at
) VALUES (
    sqlc.arg(id), sqlc.arg(plan_id), sqlc.arg(plan_revision), sqlc.arg(plan_digest),
    sqlc.arg(decision), sqlc.arg(actor), sqlc.arg(caller_label), sqlc.arg(reason),
    sqlc.arg(idempotency_scope), sqlc.arg(idempotency_key), sqlc.arg(created_at)
)
RETURNING *;

-- name: GetReviewDecision :one
SELECT * FROM review_decisions WHERE id = sqlc.arg(id);

-- name: CreateWorkflowRun :one
INSERT INTO workflow_runs (
    id, recipe_kind, recipe_version, state, deadline_at,
    cancellation_requested_at, current_step, recipe_json, outcome_json,
    created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(recipe_kind), sqlc.arg(recipe_version), sqlc.arg(state),
    sqlc.arg(deadline_at), sqlc.arg(cancellation_requested_at), sqlc.arg(current_step),
    sqlc.arg(recipe_json), sqlc.arg(outcome_json), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetWorkflowRun :one
SELECT * FROM workflow_runs WHERE id = sqlc.arg(id);

-- name: UpdateWorkflowRunState :one
UPDATE workflow_runs SET
    state = sqlc.arg(state), current_step = sqlc.arg(current_step),
    cancellation_requested_at = sqlc.arg(cancellation_requested_at),
    outcome_json = sqlc.arg(outcome_json), updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CreateWorkflowStep :one
INSERT INTO workflow_steps (
    id, workflow_id, step_index, kind, state, action_plan_id,
    action_plan_revision, gate_kind, outcome_json, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(workflow_id), sqlc.arg(step_index), sqlc.arg(kind),
    sqlc.arg(state), sqlc.arg(action_plan_id), sqlc.arg(action_plan_revision),
    sqlc.arg(gate_kind), sqlc.arg(outcome_json), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: ListWorkflowSteps :many
SELECT * FROM workflow_steps
WHERE workflow_id = sqlc.arg(workflow_id) ORDER BY step_index;

-- name: CreateActionRun :one
INSERT INTO action_runs (
    id, plan_id, plan_revision, plan_digest, state, desired_state_json,
    next_attempt_at, deadline_at, cancellation_requested_at, claimed_by,
    lease_until, version, outcome_json, unresolved_count, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(plan_id), sqlc.arg(plan_revision), sqlc.arg(plan_digest),
    sqlc.arg(state), sqlc.arg(desired_state_json), sqlc.arg(next_attempt_at),
    sqlc.arg(deadline_at), sqlc.arg(cancellation_requested_at), sqlc.arg(claimed_by),
    sqlc.arg(lease_until), sqlc.arg(version), sqlc.arg(outcome_json),
    sqlc.arg(unresolved_count), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetActionRun :one
SELECT * FROM action_runs WHERE id = sqlc.arg(id);

-- name: RecoverRunningActionRuns :many
-- Startup recovery turns every previously claimed dispatch into read-only
-- reconciliation. No external call is retried from a running row blindly.
UPDATE action_runs SET
    state = 'reconciling', next_attempt_at = sqlc.arg(now),
    claimed_by = NULL, lease_until = NULL, version = version + 1,
    updated_at = sqlc.arg(now)
WHERE (state = 'running'
    OR (state = 'reconciling' AND claimed_by IS NOT NULL))
  AND NOT EXISTS (
      SELECT 1
      FROM janitor_records AS approval
      WHERE approval.approval_action_run_id = action_runs.id
        AND approval.operation = 'purge'
        AND approval.approval_plan_id IS NOT NULL
        AND approval.state IN ('queued', 'running', 'waiting_dependency', 'reconciling', 'succeeded', 'failed', 'held', 'cancelled')
  )
RETURNING *;

-- name: RecoverExpiredActionRuns :many
-- A live worker can use the same transition when a lease expires between
-- scheduler ticks; callers must reconcile before any new dispatch.
UPDATE action_runs SET
    state = 'reconciling', next_attempt_at = sqlc.arg(now),
    claimed_by = NULL, lease_until = NULL, version = version + 1,
    updated_at = sqlc.arg(now)
WHERE (state = 'running'
    OR (state = 'reconciling' AND claimed_by IS NOT NULL))
  AND (lease_until IS NULL OR lease_until <= sqlc.arg(now))
  AND NOT EXISTS (
      SELECT 1
      FROM janitor_records AS approval
      WHERE approval.approval_action_run_id = action_runs.id
        AND approval.operation = 'purge'
        AND approval.approval_plan_id IS NOT NULL
        AND approval.state IN ('queued', 'running', 'waiting_dependency', 'reconciling', 'succeeded', 'failed', 'held', 'cancelled')
  )
RETURNING *;

-- name: RecoverRunningActionAttempts :many
UPDATE action_attempts SET
    state = 'reconciling',
    outcome_certainty = CASE
        WHEN phase = 'dispatch' OR outcome_certainty = 'uncertain' THEN 'uncertain'
        ELSE outcome_certainty
    END
WHERE state = 'running'
RETURNING *;

-- name: FinalizeCancelledActionRuns :many
-- Cancellation is terminal for undispatched queue work. A running or
-- reconciling row remains visible until its possible effect is reconciled.
UPDATE action_runs SET
    state = 'cancelled', claimed_by = NULL, lease_until = NULL,
    version = version + 1, updated_at = sqlc.arg(now)
WHERE state IN ('queued', 'waiting_dependency')
  AND cancellation_requested_at IS NOT NULL
RETURNING *;

-- name: FinalizeDeadlineActionRuns :many
UPDATE action_runs SET
    state = 'deadline_exceeded', claimed_by = NULL, lease_until = NULL,
    version = version + 1, updated_at = sqlc.arg(now)
WHERE state IN ('queued', 'waiting_dependency')
  AND cancellation_requested_at IS NULL
  AND deadline_at IS NOT NULL AND deadline_at <= sqlc.arg(now)
RETURNING *;

-- name: FinalizeCancelledActionAttempts :many
UPDATE action_attempts SET
    state = 'cancelled', finished_at = sqlc.arg(now)
WHERE action_run_id IN (SELECT id FROM action_runs WHERE state = 'cancelled')
  AND state IN ('running', 'reconciling')
RETURNING *;

-- name: FinalizeDeadlineActionAttempts :many
UPDATE action_attempts SET
    state = 'failed', finished_at = sqlc.arg(now),
    error_code = 'deadline_exceeded', error_detail = 'action deadline exceeded'
WHERE action_run_id IN (SELECT id FROM action_runs WHERE state = 'deadline_exceeded')
  AND state IN ('running', 'reconciling')
RETURNING *;

-- name: ListDueActionRuns :many
SELECT * FROM action_runs
WHERE action_runs.state IN ('queued', 'waiting_dependency', 'reconciling')
  AND action_runs.cancellation_requested_at IS NULL
  AND (action_runs.next_attempt_at IS NULL OR action_runs.next_attempt_at <= sqlc.arg(now))
  AND (action_runs.deadline_at IS NULL OR action_runs.deadline_at > sqlc.arg(now))
  AND (action_runs.claimed_by IS NULL OR action_runs.lease_until IS NULL OR action_runs.lease_until <= sqlc.arg(now))
  AND NOT EXISTS (
      SELECT 1
      FROM janitor_records AS approval
      WHERE approval.approval_action_run_id = action_runs.id
        AND approval.operation = 'purge'
        AND approval.approval_plan_id IS NOT NULL
        AND approval.state IN ('queued', 'running', 'waiting_dependency', 'reconciling', 'succeeded', 'failed', 'held', 'cancelled')
  )
ORDER BY COALESCE(action_runs.next_attempt_at, action_runs.created_at), action_runs.created_at, action_runs.id
LIMIT sqlc.arg(limit);

-- name: ClaimActionRun :one
UPDATE action_runs SET
    state = 'running', claimed_by = sqlc.arg(worker_id),
    lease_until = sqlc.arg(lease_until), version = version + 1,
    updated_at = sqlc.arg(now)
WHERE action_runs.id = sqlc.arg(id)
  AND action_runs.version = sqlc.arg(version)
  AND action_runs.state IN ('queued', 'waiting_dependency', 'reconciling')
  AND action_runs.cancellation_requested_at IS NULL
  AND (action_runs.next_attempt_at IS NULL OR action_runs.next_attempt_at <= sqlc.arg(now))
  AND (action_runs.deadline_at IS NULL OR action_runs.deadline_at > sqlc.arg(now))
  AND (action_runs.claimed_by IS NULL OR action_runs.lease_until IS NULL OR action_runs.lease_until <= sqlc.arg(now))
  AND NOT EXISTS (
      SELECT 1
      FROM janitor_records AS approval
      WHERE approval.approval_action_run_id = action_runs.id
        AND approval.operation = 'purge'
        AND approval.approval_plan_id IS NOT NULL
        AND approval.state IN ('queued', 'running', 'waiting_dependency', 'reconciling', 'succeeded', 'failed', 'held', 'cancelled')
  )
RETURNING *;

-- name: UpdateActionRunOutcome :one
UPDATE action_runs SET
    state = sqlc.arg(state), next_attempt_at = sqlc.arg(next_attempt_at),
    claimed_by = sqlc.arg(claimed_by), lease_until = sqlc.arg(lease_until),
    outcome_json = sqlc.arg(outcome_json), unresolved_count = sqlc.arg(unresolved_count),
    version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(version)
RETURNING *;

-- name: FinalizeApprovedPurgeAction :one
-- Completes the read-only journal after its exact janitor operation has
-- reached a terminal state. This path never dispatches an external mutation;
-- generic action recovery and claiming remain fenced until this CAS commits.
UPDATE action_runs
SET state = CASE
        WHEN (SELECT state FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'succeeded'
         AND (SELECT json_extract(outcome_json, '$.outcome') FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'applied'
            THEN 'succeeded'
        WHEN (SELECT state FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'succeeded'
         AND (SELECT json_extract(outcome_json, '$.outcome') FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'already_satisfied'
         AND (SELECT CASE
                 WHEN (json_type(outcome_json, '$.deletedObjects') IS NULL
                       OR (json_type(outcome_json, '$.deletedObjects') IN ('integer', 'real')
                           AND json_extract(outcome_json, '$.deletedObjects') = 0))
                  AND (json_type(outcome_json, '$.deleted_objects') IS NULL
                       OR (json_type(outcome_json, '$.deleted_objects') IN ('integer', 'real')
                           AND json_extract(outcome_json, '$.deleted_objects') = 0))
                     THEN 1
                 ELSE 0
               END
              FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 1
            THEN 'succeeded'
        WHEN (SELECT state FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'succeeded' THEN 'needs_review'
        WHEN (SELECT state FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'failed' THEN 'failed'
        WHEN (SELECT state FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'cancelled' THEN 'cancelled'
        ELSE 'needs_review'
    END,
    next_attempt_at = NULL,
    claimed_by = NULL,
    lease_until = NULL,
    outcome_json = CASE
        WHEN (SELECT state FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'succeeded'
         AND (SELECT json_extract(outcome_json, '$.outcome') FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'applied'
            THEN (SELECT outcome_json FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id))
        WHEN (SELECT state FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'succeeded'
         AND (SELECT json_extract(outcome_json, '$.outcome') FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'already_satisfied'
         AND (SELECT CASE
                 WHEN (json_type(outcome_json, '$.deletedObjects') IS NULL
                       OR (json_type(outcome_json, '$.deletedObjects') IN ('integer', 'real')
                           AND json_extract(outcome_json, '$.deletedObjects') = 0))
                  AND (json_type(outcome_json, '$.deleted_objects') IS NULL
                       OR (json_type(outcome_json, '$.deleted_objects') IN ('integer', 'real')
                           AND json_extract(outcome_json, '$.deleted_objects') = 0))
                     THEN 1
                 ELSE 0
               END
              FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 1
            THEN (SELECT outcome_json FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id))
        WHEN (SELECT state FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'succeeded'
            THEN json_object('outcome', 'unknown', 'source', 'approved_purge_janitor', 'reason', 'terminal_outcome_unproven')
        WHEN (SELECT state FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'failed' THEN json_object('reason', 'approved_purge_janitor_failed')
        WHEN (SELECT state FROM janitor_records WHERE janitor_records.id = sqlc.arg(janitor_id)) = 'cancelled' THEN json_object('reason', 'approved_purge_janitor_cancelled')
        ELSE json_object('reason', 'approved_purge_janitor_held')
    END,
    version = version + 1,
    updated_at = sqlc.arg(now)
WHERE action_runs.id = sqlc.arg(action_run_id)
  AND action_runs.version = sqlc.arg(action_run_version)
  AND action_runs.state IN ('running', 'reconciling')
  AND EXISTS (
      SELECT 1
      FROM janitor_records AS janitor
      JOIN action_plan_revisions AS revision
        ON revision.plan_id = janitor.approval_plan_id
       AND revision.revision = janitor.approval_plan_revision
       AND revision.digest = janitor.approval_plan_digest
      JOIN action_plans AS plan
        ON plan.id = revision.plan_id
      JOIN early_purge_plan_targets AS target
        ON target.plan_id = revision.plan_id
       AND target.revision = revision.revision
       AND target.plan_digest = revision.digest
      JOIN review_decisions AS decision
        ON decision.id = janitor.approval_decision_id
      JOIN trash_entries AS entry
        ON entry.id = janitor.trash_entry_id
      WHERE janitor.id = sqlc.arg(janitor_id)
        AND janitor.operation = 'purge'
        AND janitor.state IN ('succeeded', 'failed', 'held', 'cancelled')
        AND janitor.approval_plan_id = action_runs.plan_id
        AND janitor.approval_plan_revision = action_runs.plan_revision
        AND janitor.approval_plan_digest = action_runs.plan_digest
        AND janitor.approval_action_run_id = action_runs.id
        AND janitor.approved_entry_version = target.trash_entry_version
        AND janitor.approval_action_run_version IS NOT NULL
        AND janitor.version = action_runs.version + 1
        AND plan.state = 'ready'
        AND plan.kind = 'fs.delete'
        AND revision.state = 'ready'
        AND decision.plan_id = revision.plan_id
        AND decision.plan_revision = revision.revision
        AND decision.plan_digest = revision.digest
        AND decision.decision = 'approve'
        AND target.intent_kind = 'fs.delete'
        AND target.trash_entry_id = janitor.trash_entry_id
        AND target.trash_entry_version = janitor.approved_entry_version
        AND target.manifest_json IS NOT NULL
        AND json_valid(target.manifest_json)
        AND json_type(target.manifest_json) = 'array'
        AND json_array_length(target.manifest_json) > 0
        AND json_valid(revision.manifest_json)
        AND json(target.manifest_json) = json(revision.manifest_json)
        AND json_valid(entry.manifest_json)
        AND json(target.manifest_json) = json(entry.manifest_json)
        AND entry.version = janitor.version
        AND entry.active_operation IS NULL
        AND entry.state = CASE janitor.state
            WHEN 'succeeded' THEN 'purged'
            WHEN 'held' THEN 'held'
            ELSE 'failed'
        END
  )
RETURNING *;

-- name: RequestActionCancellation :one
UPDATE action_runs SET cancellation_requested_at = sqlc.arg(requested_at), version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND state NOT IN ('succeeded', 'failed', 'cancelled', 'deadline_exceeded')
RETURNING *;

-- name: CreateActionAttempt :one
INSERT INTO action_attempts (
    id, action_run_id, attempt_number, phase, state, started_at, finished_at,
    error_code, error_detail, outcome_certainty, external_id, evidence_json
) VALUES (
    sqlc.arg(id), sqlc.arg(action_run_id), sqlc.arg(attempt_number), sqlc.arg(phase),
    sqlc.arg(state), sqlc.arg(started_at), sqlc.arg(finished_at), sqlc.arg(error_code),
    sqlc.arg(error_detail), sqlc.arg(outcome_certainty), sqlc.arg(external_id),
    sqlc.arg(evidence_json)
)
RETURNING *;

-- name: UpdateActionAttempt :one
UPDATE action_attempts SET
    state = sqlc.arg(state), finished_at = sqlc.arg(finished_at),
    error_code = sqlc.arg(error_code), error_detail = sqlc.arg(error_detail),
    outcome_certainty = sqlc.arg(outcome_certainty), external_id = sqlc.arg(external_id),
    evidence_json = sqlc.arg(evidence_json)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListActionAttempts :many
SELECT * FROM action_attempts
WHERE action_run_id = sqlc.arg(action_run_id) ORDER BY attempt_number;

-- name: CreateActionEffect :one
INSERT INTO action_effects (
    id, action_run_id, attempt_id, ordinal, target_kind, target_id, effect_kind,
    state, evidence_json, observed_at
) VALUES (
    sqlc.arg(id), sqlc.arg(action_run_id), sqlc.arg(attempt_id), sqlc.arg(ordinal),
    sqlc.arg(target_kind), sqlc.arg(target_id), sqlc.arg(effect_kind),
    sqlc.arg(state), sqlc.arg(evidence_json), sqlc.arg(observed_at)
)
RETURNING *;

-- name: UpdateActionEffect :one
UPDATE action_effects SET
    state = sqlc.arg(state), evidence_json = sqlc.arg(evidence_json),
    observed_at = sqlc.arg(observed_at), attempt_id = sqlc.arg(attempt_id)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListActionEffects :many
SELECT * FROM action_effects
WHERE action_run_id = sqlc.arg(action_run_id) ORDER BY ordinal;

-- Idempotency and audit are intentionally append-only.

-- name: CreateIdempotencyRecord :one
INSERT INTO idempotency_records (
    scope, idempotency_key, request_digest, status_code, resource_kind,
    resource_id, response_json, created_at, expires_at
) VALUES (
    sqlc.arg(scope), sqlc.arg(idempotency_key), sqlc.arg(request_digest),
    sqlc.arg(status_code), sqlc.arg(resource_kind), sqlc.arg(resource_id),
    sqlc.arg(response_json), sqlc.arg(created_at), sqlc.arg(expires_at)
)
RETURNING *;

-- name: GetIdempotencyRecord :one
SELECT * FROM idempotency_records
WHERE scope = sqlc.arg(scope) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CreateAuditEvent :one
INSERT INTO audit_events (
    event_id, occurred_at, actor, action, resource_kind, resource_id,
    plan_digest, outcome, metadata_json, redacted
) VALUES (
    sqlc.arg(event_id), sqlc.arg(occurred_at), sqlc.arg(actor), sqlc.arg(action),
    sqlc.arg(resource_kind), sqlc.arg(resource_id), sqlc.arg(plan_digest),
    sqlc.arg(outcome), sqlc.arg(metadata_json), sqlc.arg(redacted)
)
RETURNING *;

-- name: ListAuditEvents :many
SELECT * FROM audit_events
ORDER BY occurred_at DESC, id DESC LIMIT sqlc.arg(limit) OFFSET sqlc.arg(offset);

-- Trash and janitor journal.

-- name: CreateTrashEntry :one
INSERT INTO trash_entries (
    id, root_id, state, original_prefix, trash_prefix, manifest_json,
    retention_seconds, trashed_at, expires_at, hold_reason, client_state_json,
    purge_claimed_at, restore_requested_at, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(root_id), sqlc.arg(state), sqlc.arg(original_prefix),
    sqlc.arg(trash_prefix), sqlc.arg(manifest_json), sqlc.arg(retention_seconds),
    sqlc.arg(trashed_at), sqlc.arg(expires_at), sqlc.arg(hold_reason),
    sqlc.arg(client_state_json), sqlc.arg(purge_claimed_at),
    sqlc.arg(restore_requested_at), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetTrashEntry :one
SELECT * FROM trash_entries WHERE id = sqlc.arg(id);

-- name: ListDueTrashEntries :many
SELECT * FROM trash_entries
WHERE state = 'trashed' AND expires_at <= sqlc.arg(now)
ORDER BY expires_at, id LIMIT sqlc.arg(limit);

-- name: UpdateTrashEntryState :one
UPDATE trash_entries SET
    state = sqlc.arg(state), trashed_at = sqlc.arg(trashed_at),
    hold_reason = sqlc.arg(hold_reason), client_state_json = sqlc.arg(client_state_json),
    purge_claimed_at = sqlc.arg(purge_claimed_at), restore_requested_at = sqlc.arg(restore_requested_at),
    active_operation = sqlc.arg(active_operation),
    operation_claimed_by = sqlc.arg(operation_claimed_by),
    operation_lease_until = sqlc.arg(operation_lease_until),
    version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(version)
RETURNING *;

-- name: CreateTrashItem :one
INSERT INTO trash_items (
    id, entry_id, root_id, original_relative_path, trash_relative_path,
    entry_type, size_bytes, digest, file_identity, client_connection_id,
    client_external_id, state, trashed_at, restored_at, purged_at
) VALUES (
    sqlc.arg(id), sqlc.arg(entry_id), sqlc.arg(root_id), sqlc.arg(original_relative_path),
    sqlc.arg(trash_relative_path), sqlc.arg(entry_type), sqlc.arg(size_bytes),
    sqlc.arg(digest), sqlc.arg(file_identity), sqlc.arg(client_connection_id),
    sqlc.arg(client_external_id), sqlc.arg(state), sqlc.arg(trashed_at),
    sqlc.arg(restored_at), sqlc.arg(purged_at)
)
RETURNING *;

-- name: ListTrashItems :many
SELECT * FROM trash_items WHERE entry_id = sqlc.arg(entry_id) ORDER BY original_relative_path, id;

-- name: CreateJanitorRecord :one
INSERT INTO janitor_records (
    id, trash_entry_id, operation, state, next_attempt_at, claimed_by,
    lease_until, outcome_json, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(trash_entry_id), sqlc.arg(operation), sqlc.arg(state),
    sqlc.arg(next_attempt_at), sqlc.arg(claimed_by), sqlc.arg(lease_until),
    sqlc.arg(outcome_json), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetJanitorRecord :one
SELECT * FROM janitor_records
WHERE trash_entry_id = sqlc.arg(trash_entry_id) AND operation = sqlc.arg(operation);

-- name: RecoverRunningJanitorRecords :many
-- The trigger on janitor_records releases the worker lease on the shared
-- trash entry while preserving its active operation for safe reconciliation.
UPDATE janitor_records SET
    state = 'reconciling', claimed_by = NULL, lease_until = NULL,
    version = version + 1, updated_at = sqlc.arg(now)
WHERE state = 'running'
RETURNING *;

-- name: RecoverExpiredJanitorRecords :many
UPDATE janitor_records SET
    state = 'reconciling', claimed_by = NULL, lease_until = NULL,
    version = version + 1, updated_at = sqlc.arg(now)
WHERE state = 'running'
  AND (lease_until IS NULL OR lease_until <= sqlc.arg(now))
RETURNING *;

-- name: ListDueJanitorRecords :many
SELECT * FROM janitor_records
WHERE state IN ('queued', 'waiting_dependency', 'reconciling')
  AND (next_attempt_at IS NULL OR next_attempt_at <= sqlc.arg(now))
  AND (claimed_by IS NULL OR lease_until IS NULL OR lease_until <= sqlc.arg(now))
ORDER BY COALESCE(next_attempt_at, created_at), created_at, id
LIMIT sqlc.arg(limit);

-- name: ClaimJanitorRecord :one
UPDATE janitor_records SET
    state = 'running', claimed_by = sqlc.arg(worker_id), lease_until = sqlc.arg(lease_until),
    version = version + 1, updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND version = sqlc.arg(version)
  AND state IN ('queued', 'waiting_dependency', 'reconciling')
  AND approval_plan_id IS NULL
  AND (next_attempt_at IS NULL OR next_attempt_at <= sqlc.arg(now))
  AND (claimed_by IS NULL OR lease_until IS NULL OR lease_until <= sqlc.arg(now))
RETURNING *;

-- name: ClaimApprovedEarlyPurge :one
-- Binds an explicit approval and the exact trash-entry version in the same
-- claim statement. The migration trigger validates the immutable plan,
-- decision and action-run identities before it changes the shared entry.
UPDATE janitor_records SET
    state = 'running', claimed_by = sqlc.arg(worker_id), lease_until = sqlc.arg(lease_until),
    approval_plan_id = sqlc.arg(approval_plan_id), approval_plan_revision = sqlc.arg(approval_plan_revision),
    approval_plan_digest = sqlc.arg(approval_plan_digest), approval_decision_id = sqlc.arg(approval_decision_id),
    approval_action_run_id = sqlc.arg(approval_action_run_id), approved_entry_version = sqlc.arg(approved_entry_version),
    approval_action_run_version = sqlc.arg(approval_action_run_version),
    version = version + 1, updated_at = sqlc.arg(now)
WHERE janitor_records.id = sqlc.arg(id)
  AND janitor_records.version = sqlc.arg(version)
  AND janitor_records.trash_entry_id = sqlc.arg(trash_entry_id)
  AND janitor_records.operation = 'purge'
  AND janitor_records.state IN ('queued', 'reconciling')
  AND (janitor_records.next_attempt_at IS NULL OR janitor_records.next_attempt_at <= sqlc.arg(now))
  AND (janitor_records.claimed_by IS NULL OR janitor_records.lease_until IS NULL OR janitor_records.lease_until <= sqlc.arg(now))
  AND EXISTS (
      SELECT 1
      FROM early_purge_plan_targets AS target
      JOIN review_decisions AS decision
        ON decision.id = sqlc.arg(approval_decision_id)
       AND decision.plan_id = target.plan_id
       AND decision.plan_revision = target.revision
       AND decision.plan_digest = target.plan_digest
       AND decision.decision = 'approve'
      WHERE target.plan_id = sqlc.arg(approval_plan_id)
        AND target.revision = sqlc.arg(approval_plan_revision)
        AND target.plan_digest = sqlc.arg(approval_plan_digest)
        AND target.trash_entry_id = sqlc.arg(trash_entry_id)
        AND target.trash_entry_version = sqlc.arg(approved_entry_version)
        AND julianday(target.created_at) IS NOT NULL
        AND julianday(decision.created_at) IS NOT NULL
        AND julianday(target.created_at) <= julianday(decision.created_at)
  )
RETURNING *;

-- name: ClaimApprovedEarlyPurgeReconciliation :one
-- Reacquires a janitor/trash lease for read-only reconciliation after startup
-- recovery. The associated action run remains reconciling but is fenced with
-- the same worker lease by the migration trigger; no external mutation is
-- dispatched by this CAS. The trash generation follows the current janitor
-- generation, rather than a fixed offset from the approval-time version.
UPDATE janitor_records
SET state = 'running',
    claimed_by = sqlc.arg(worker_id),
    lease_until = sqlc.arg(lease_until),
    version = version + 1,
    updated_at = sqlc.arg(now)
WHERE janitor_records.id = sqlc.arg(id)
  AND janitor_records.version = sqlc.arg(version)
  AND janitor_records.trash_entry_id = sqlc.arg(trash_entry_id)
  AND janitor_records.operation = 'purge'
  AND janitor_records.state = 'reconciling'
  AND janitor_records.approval_plan_id = sqlc.arg(approval_plan_id)
  AND janitor_records.approval_plan_revision = sqlc.arg(approval_plan_revision)
  AND janitor_records.approval_plan_digest = sqlc.arg(approval_plan_digest)
  AND janitor_records.approval_decision_id = sqlc.arg(approval_decision_id)
  AND janitor_records.approval_action_run_id = sqlc.arg(approval_action_run_id)
  AND janitor_records.approved_entry_version = sqlc.arg(approved_entry_version)
  AND janitor_records.approval_action_run_version = sqlc.arg(approval_action_run_version)
  AND (janitor_records.next_attempt_at IS NULL OR janitor_records.next_attempt_at <= sqlc.arg(now))
  AND (janitor_records.claimed_by IS NULL OR janitor_records.lease_until IS NULL OR janitor_records.lease_until <= sqlc.arg(now))
  AND EXISTS (
      SELECT 1
      FROM action_runs AS action
      WHERE action.id = sqlc.arg(approval_action_run_id)
        AND action.plan_id = sqlc.arg(approval_plan_id)
        AND action.plan_revision = sqlc.arg(approval_plan_revision)
        AND action.plan_digest = sqlc.arg(approval_plan_digest)
        AND action.state = 'reconciling'
        AND action.version = janitor_records.version
        AND action.version > sqlc.arg(approval_action_run_version) + 1
        AND action.claimed_by IS NULL
        AND action.lease_until IS NULL
        AND (action.next_attempt_at IS NULL OR action.next_attempt_at <= sqlc.arg(now))
  )
  AND EXISTS (
      SELECT 1
      FROM early_purge_plan_targets AS target
      JOIN review_decisions AS decision
        ON decision.id = sqlc.arg(approval_decision_id)
       AND decision.plan_id = target.plan_id
       AND decision.plan_revision = target.revision
       AND decision.plan_digest = target.plan_digest
       AND decision.decision = 'approve'
      WHERE target.plan_id = sqlc.arg(approval_plan_id)
        AND target.revision = sqlc.arg(approval_plan_revision)
        AND target.plan_digest = sqlc.arg(approval_plan_digest)
        AND target.trash_entry_id = sqlc.arg(trash_entry_id)
        AND target.trash_entry_version = sqlc.arg(approved_entry_version)
        AND julianday(target.created_at) IS NOT NULL
        AND julianday(decision.created_at) IS NOT NULL
        AND julianday(target.created_at) <= julianday(decision.created_at)
  )
  AND EXISTS (
      SELECT 1
      FROM trash_entries AS entry
      WHERE entry.id = sqlc.arg(trash_entry_id)
        AND entry.state = 'purging'
        AND entry.active_operation = 'purge'
        AND entry.version = janitor_records.version + sqlc.arg(approved_entry_version) - 1
        AND entry.operation_claimed_by IS NULL
        AND entry.operation_lease_until IS NULL
  )
RETURNING *;

-- name: UpdateJanitorRecord :one
UPDATE janitor_records SET
    state = sqlc.arg(state), next_attempt_at = sqlc.arg(next_attempt_at),
    claimed_by = sqlc.arg(claimed_by), lease_until = sqlc.arg(lease_until),
    outcome_json = sqlc.arg(outcome_json), version = version + 1,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND version = sqlc.arg(version)
RETURNING *;
