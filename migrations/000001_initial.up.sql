-- Managerr owns this journal. Upstream systems and mounted files remain the
-- authority for their actual state.

PRAGMA foreign_keys = ON;

CREATE TABLE config_snapshots (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    source TEXT NOT NULL CHECK (source IN ('yaml', 'api')),
    document_id TEXT NOT NULL CHECK (length(trim(document_id)) > 0),
    revision TEXT NOT NULL CHECK (length(trim(revision)) > 0),
    startup_at TEXT NOT NULL CHECK (length(trim(startup_at)) > 0),
    effective_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(effective_json)),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    UNIQUE (source, document_id, revision)
);

CREATE TABLE connections (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    kind TEXT NOT NULL CHECK (kind IN ('qbittorrent', 'nzbget', 'radarr', 'sonarr', 'jellyfin', 'seerr')),
    label TEXT NOT NULL CHECK (length(trim(label)) > 0),
    endpoint TEXT NOT NULL CHECK (length(trim(endpoint)) > 0),
    source TEXT NOT NULL CHECK (source IN ('yaml', 'api')),
    source_snapshot_id TEXT NOT NULL REFERENCES config_snapshots(id),
    revision TEXT NOT NULL CHECK (length(trim(revision)) > 0),
    retired_at TEXT,
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    updated_at TEXT NOT NULL CHECK (length(trim(updated_at)) > 0)
);

CREATE TABLE storage_roots (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    label TEXT NOT NULL CHECK (length(trim(label)) > 0),
    purpose TEXT NOT NULL CHECK (purpose IN ('download', 'library', 'descriptor', 'trash')),
    path TEXT NOT NULL CHECK (length(trim(path)) > 0 AND substr(path, 1, 1) = '/'),
    source TEXT NOT NULL CHECK (source IN ('yaml', 'api')),
    source_snapshot_id TEXT NOT NULL REFERENCES config_snapshots(id),
    revision TEXT NOT NULL CHECK (length(trim(revision)) > 0),
    read_only INTEGER NOT NULL DEFAULT 0 CHECK (read_only IN (0, 1)),
    watch_enabled INTEGER NOT NULL DEFAULT 0 CHECK (watch_enabled IN (0, 1)),
    watch_interval_seconds INTEGER NOT NULL DEFAULT 0 CHECK (watch_interval_seconds >= 0),
    capabilities_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(capabilities_json)),
    retired_at TEXT,
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    updated_at TEXT NOT NULL CHECK (length(trim(updated_at)) > 0)
);

CREATE TABLE path_mappings (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    connection_id TEXT NOT NULL REFERENCES connections(id),
    root_id TEXT NOT NULL REFERENCES storage_roots(id),
    source_prefix TEXT NOT NULL DEFAULT '',
    destination_prefix TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL CHECK (source IN ('yaml', 'api')),
    source_snapshot_id TEXT NOT NULL REFERENCES config_snapshots(id),
    revision TEXT NOT NULL CHECK (length(trim(revision)) > 0),
    retired_at TEXT,
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    updated_at TEXT NOT NULL CHECK (length(trim(updated_at)) > 0)
);

-- Only encrypted envelopes are stored. Secret references and plaintext values
-- belong to the configuration boundary and never enter this table.
CREATE TABLE encrypted_credentials (
    connection_id TEXT NOT NULL REFERENCES connections(id),
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    envelope_version INTEGER NOT NULL CHECK (envelope_version > 0),
    nonce BLOB NOT NULL CHECK (length(nonce) > 0),
    ciphertext BLOB NOT NULL CHECK (length(ciphertext) > 0),
    key_fingerprint TEXT NOT NULL CHECK (length(trim(key_fingerprint)) > 0),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    updated_at TEXT NOT NULL CHECK (length(trim(updated_at)) > 0),
    PRIMARY KEY (connection_id, name)
);

CREATE TABLE scans (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('root', 'download', 'catalog', 'request', 'media_server')),
    scope_id TEXT NOT NULL CHECK (length(trim(scope_id)) > 0),
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'waiting', 'succeeded', 'failed', 'cancelled', 'deadline_exceeded')),
    completeness TEXT NOT NULL CHECK (completeness IN ('complete', 'partial', 'unknown')),
    cursor TEXT,
    observed_count INTEGER NOT NULL DEFAULT 0 CHECK (observed_count >= 0),
    started_at TEXT,
    completed_at TEXT,
    config_revision TEXT CHECK (config_revision IS NULL OR length(trim(config_revision)) > 0),
    error_code TEXT,
    error_detail TEXT,
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    updated_at TEXT NOT NULL CHECK (length(trim(updated_at)) > 0),
    CHECK (completed_at IS NULL OR started_at IS NOT NULL),
    CHECK (completed_at IS NULL OR started_at <= completed_at)
);

CREATE TABLE coverage_snapshots (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    scan_id TEXT REFERENCES scans(id),
    source_id TEXT,
    connection_id TEXT REFERENCES connections(id),
    root_id TEXT REFERENCES storage_roots(id),
    completeness TEXT NOT NULL CHECK (completeness IN ('complete', 'partial', 'unknown')),
    reason_codes_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(reason_codes_json)),
    observed_count INTEGER NOT NULL DEFAULT 0 CHECK (observed_count >= 0),
    snapshot_revision TEXT,
    started_at TEXT,
    completed_at TEXT,
    observed_at TEXT NOT NULL CHECK (length(trim(observed_at)) > 0),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    CHECK (completeness <> 'complete' OR completed_at IS NOT NULL),
    CHECK (completed_at IS NULL OR started_at IS NOT NULL),
    CHECK (completed_at IS NULL OR started_at <= completed_at),
    CHECK (completed_at IS NULL OR completed_at <= observed_at)
);

CREATE TABLE discoveries (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    root_id TEXT NOT NULL REFERENCES storage_roots(id),
    relative_path TEXT NOT NULL CHECK (length(trim(relative_path)) > 0 AND relative_path NOT IN ('.', '..') AND substr(relative_path, 1, 1) <> '/' AND instr('/' || relative_path || '/', '/../') = 0 AND instr('/' || relative_path || '/', '/./') = 0 AND instr(relative_path, '//') = 0),
    entry_type TEXT NOT NULL CHECK (entry_type IN ('file', 'directory', 'subtitle', 'companion')),
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    digest TEXT,
    file_identity TEXT,
    role TEXT CHECK (role IS NULL OR role IN ('video', 'subtitle', 'companion')),
    observed_at TEXT NOT NULL CHECK (length(trim(observed_at)) > 0),
    first_seen_at TEXT NOT NULL CHECK (length(trim(first_seen_at)) > 0),
    last_seen_at TEXT NOT NULL CHECK (length(trim(last_seen_at)) > 0),
    manifest_revision TEXT NOT NULL CHECK (length(trim(manifest_revision)) > 0),
    child_manifest_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(child_manifest_json)),
    deleted_at TEXT,
    UNIQUE (root_id, relative_path, manifest_revision)
);

CREATE TABLE file_observations (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    discovery_id TEXT NOT NULL REFERENCES discoveries(id),
    root_id TEXT NOT NULL REFERENCES storage_roots(id),
    relative_path TEXT NOT NULL CHECK (length(trim(relative_path)) > 0 AND relative_path NOT IN ('.', '..') AND substr(relative_path, 1, 1) <> '/' AND instr('/' || relative_path || '/', '/../') = 0 AND instr('/' || relative_path || '/', '/./') = 0 AND instr(relative_path, '//') = 0),
    entry_type TEXT NOT NULL CHECK (entry_type IN ('file', 'directory', 'subtitle', 'companion')),
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    digest TEXT,
    file_identity TEXT,
    role TEXT CHECK (role IS NULL OR role IN ('video', 'subtitle', 'companion')),
    observed_at TEXT NOT NULL CHECK (length(trim(observed_at)) > 0),
    manifest_revision TEXT NOT NULL CHECK (length(trim(manifest_revision)) > 0),
    child_manifest_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(child_manifest_json)),
    first_seen_at TEXT NOT NULL CHECK (length(trim(first_seen_at)) > 0),
    last_seen_at TEXT NOT NULL CHECK (length(trim(last_seen_at)) > 0),
    deleted_at TEXT,
    FOREIGN KEY (root_id, relative_path, manifest_revision)
        REFERENCES discoveries(root_id, relative_path, manifest_revision)
);

CREATE TABLE downloads (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    connection_id TEXT NOT NULL REFERENCES connections(id),
    external_id TEXT NOT NULL CHECK (length(trim(external_id)) > 0),
    protocol TEXT NOT NULL CHECK (protocol IN ('qbittorrent', 'nzbget')),
    name TEXT,
    content_hash TEXT,
    nzb_id TEXT,
    state TEXT NOT NULL CHECK (state IN ('queued', 'downloading', 'processing', 'completed', 'seeding', 'paused', 'failed', 'unknown')),
    completed_at TEXT,
    seeding_state TEXT,
    payload_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload_json)),
    history_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(history_json)),
    first_seen_at TEXT NOT NULL CHECK (length(trim(first_seen_at)) > 0),
    last_seen_at TEXT NOT NULL CHECK (length(trim(last_seen_at)) > 0),
    UNIQUE (connection_id, external_id)
);

CREATE TABLE provenance_links (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    discovery_id TEXT NOT NULL REFERENCES discoveries(id),
    download_id TEXT NOT NULL REFERENCES downloads(id),
    evidence_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(evidence_json)),
    observed_at TEXT NOT NULL CHECK (length(trim(observed_at)) > 0),
    first_seen_at TEXT NOT NULL CHECK (length(trim(first_seen_at)) > 0),
    last_seen_at TEXT NOT NULL CHECK (length(trim(last_seen_at)) > 0),
    UNIQUE (discovery_id, download_id)
);

CREATE TABLE descriptors (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    download_id TEXT REFERENCES downloads(id),
    descriptor_type TEXT NOT NULL CHECK (length(trim(descriptor_type)) > 0),
    storage_path TEXT NOT NULL CHECK (length(trim(storage_path)) > 0 AND storage_path NOT IN ('.', '..') AND substr(storage_path, 1, 1) <> '/' AND instr('/' || storage_path || '/', '/../') = 0 AND instr('/' || storage_path || '/', '/./') = 0 AND instr(storage_path, '//') = 0),
    original_digest TEXT,
    capture_source TEXT NOT NULL CHECK (length(trim(capture_source)) > 0),
    captured_at TEXT NOT NULL CHECK (length(trim(captured_at)) > 0),
    retention TEXT NOT NULL CHECK (retention IN ('retain', 'eligible', 'deleted')),
    unavailable_reason TEXT,
    deleted_at TEXT,
    CHECK (original_digest IS NOT NULL OR unavailable_reason IS NOT NULL),
    UNIQUE (download_id, descriptor_type)
);

CREATE TABLE media_identities (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    kind TEXT NOT NULL CHECK (kind IN ('movie', 'episode', 'season', 'anime')),
    provider_namespace TEXT,
    provider_id TEXT,
    title TEXT NOT NULL CHECK (length(trim(title)) > 0),
    year INTEGER CHECK (year IS NULL OR (year >= 1800 AND year <= 3000)),
    canonical_key TEXT NOT NULL CHECK (length(trim(canonical_key)) > 0),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    updated_at TEXT NOT NULL CHECK (length(trim(updated_at)) > 0),
    UNIQUE (provider_namespace, provider_id),
    UNIQUE (canonical_key)
);

CREATE TABLE external_records (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    connection_id TEXT NOT NULL REFERENCES connections(id),
    media_identity_id TEXT REFERENCES media_identities(id),
    record_kind TEXT NOT NULL CHECK (record_kind IN ('title', 'file', 'request')),
    external_id TEXT NOT NULL CHECK (length(trim(external_id)) > 0),
    title TEXT,
    payload_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload_json)),
    observed_at TEXT NOT NULL CHECK (length(trim(observed_at)) > 0),
    first_seen_at TEXT NOT NULL CHECK (length(trim(first_seen_at)) > 0),
    last_seen_at TEXT NOT NULL CHECK (length(trim(last_seen_at)) > 0),
    UNIQUE (connection_id, record_kind, external_id)
);

CREATE TABLE tracking_observations (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    external_record_id TEXT REFERENCES external_records(id),
    media_identity_id TEXT REFERENCES media_identities(id),
    connection_id TEXT REFERENCES connections(id),
    dimension TEXT NOT NULL CHECK (dimension IN ('registration', 'import', 'availability', 'request')),
    status TEXT NOT NULL CHECK (status IN ('present', 'absent', 'unknown')),
    evidence_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(evidence_json)),
    coverage_id TEXT REFERENCES coverage_snapshots(id),
    observed_at TEXT NOT NULL CHECK (length(trim(observed_at)) > 0),
    registered_at TEXT,
    imported_at TEXT,
    CHECK (external_record_id IS NOT NULL OR media_identity_id IS NOT NULL OR status = 'unknown'),
    UNIQUE (external_record_id, dimension, observed_at)
);

CREATE TABLE action_plans (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    kind TEXT NOT NULL CHECK (length(trim(kind)) > 0),
    state TEXT NOT NULL CHECK (state IN ('preparing', 'ready', 'invalid', 'expired')),
    current_revision INTEGER NOT NULL DEFAULT 1 CHECK (current_revision > 0),
    current_digest TEXT NOT NULL CHECK (length(trim(current_digest)) > 0),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    updated_at TEXT NOT NULL CHECK (length(trim(updated_at)) > 0)
);

CREATE TABLE action_plan_revisions (
    plan_id TEXT NOT NULL REFERENCES action_plans(id),
    revision INTEGER NOT NULL CHECK (revision > 0),
    digest TEXT NOT NULL CHECK (length(trim(digest)) > 0),
    state TEXT NOT NULL CHECK (state IN ('preparing', 'ready', 'invalid', 'expired')),
    input_json TEXT NOT NULL CHECK (json_valid(input_json)),
    preconditions_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(preconditions_json)),
    capabilities_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(capabilities_json)),
    manifest_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(manifest_json)),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    expires_at TEXT NOT NULL CHECK (length(trim(expires_at)) > 0),
    ready_at TEXT,
    PRIMARY KEY (plan_id, revision),
    UNIQUE (plan_id, revision, digest)
);

CREATE TABLE plan_manifests (
    plan_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    root_id TEXT NOT NULL REFERENCES storage_roots(id),
    relative_path TEXT NOT NULL CHECK (length(trim(relative_path)) > 0 AND relative_path NOT IN ('.', '..') AND substr(relative_path, 1, 1) <> '/' AND instr('/' || relative_path || '/', '/../') = 0 AND instr('/' || relative_path || '/', '/./') = 0 AND instr(relative_path, '//') = 0),
    entry_type TEXT NOT NULL CHECK (entry_type IN ('file', 'directory', 'subtitle', 'companion')),
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    digest TEXT,
    file_identity TEXT,
    role TEXT CHECK (role IS NULL OR role IN ('video', 'subtitle', 'companion')),
    manifest_json TEXT NOT NULL CHECK (json_valid(manifest_json)),
    PRIMARY KEY (plan_id, revision, ordinal),
    UNIQUE (plan_id, revision, root_id, relative_path),
    FOREIGN KEY (plan_id, revision) REFERENCES action_plan_revisions(plan_id, revision)
);

CREATE TABLE review_decisions (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    plan_id TEXT NOT NULL,
    plan_revision INTEGER NOT NULL CHECK (plan_revision > 0),
    plan_digest TEXT NOT NULL CHECK (length(trim(plan_digest)) > 0),
    decision TEXT NOT NULL CHECK (decision IN ('approve', 'reject')),
    actor TEXT NOT NULL DEFAULT 'unauthenticated' CHECK (length(trim(actor)) > 0),
    caller_label TEXT,
    reason TEXT,
    idempotency_scope TEXT NOT NULL CHECK (length(trim(idempotency_scope)) > 0),
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) > 0 AND length(idempotency_key) <= 200),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    FOREIGN KEY (plan_id, plan_revision, plan_digest)
        REFERENCES action_plan_revisions(plan_id, revision, digest),
    UNIQUE (plan_id, plan_revision)
);

CREATE TABLE workflow_runs (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    recipe_kind TEXT NOT NULL CHECK (length(trim(recipe_kind)) > 0),
    recipe_version INTEGER NOT NULL CHECK (recipe_version > 0),
    state TEXT NOT NULL CHECK (state IN ('awaiting_approval', 'running', 'waiting_dependency', 'needs_review', 'succeeded', 'failed', 'cancelled', 'deadline_exceeded')),
    deadline_at TEXT,
    cancellation_requested_at TEXT,
    current_step INTEGER NOT NULL DEFAULT 0 CHECK (current_step >= 0),
    recipe_json TEXT NOT NULL CHECK (json_valid(recipe_json)),
    outcome_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(outcome_json)),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    updated_at TEXT NOT NULL CHECK (length(trim(updated_at)) > 0)
);

CREATE TABLE workflow_steps (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    workflow_id TEXT NOT NULL REFERENCES workflow_runs(id),
    step_index INTEGER NOT NULL CHECK (step_index >= 0),
    kind TEXT NOT NULL CHECK (length(trim(kind)) > 0),
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'blocked', 'skipped', 'cancelled')),
    action_plan_id TEXT,
    action_plan_revision INTEGER,
    gate_kind TEXT,
    outcome_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(outcome_json)),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    updated_at TEXT NOT NULL CHECK (length(trim(updated_at)) > 0),
    CHECK ((action_plan_id IS NULL AND action_plan_revision IS NULL) OR (action_plan_id IS NOT NULL AND action_plan_revision IS NOT NULL AND action_plan_revision > 0)),
    FOREIGN KEY (action_plan_id, action_plan_revision) REFERENCES action_plan_revisions(plan_id, revision),
    UNIQUE (workflow_id, step_index)
);

CREATE TABLE action_runs (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    plan_id TEXT NOT NULL,
    plan_revision INTEGER NOT NULL CHECK (plan_revision > 0),
    plan_digest TEXT NOT NULL CHECK (length(trim(plan_digest)) > 0),
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'waiting_dependency', 'reconciling', 'needs_review', 'succeeded', 'failed', 'cancelled', 'deadline_exceeded')),
    desired_state_json TEXT NOT NULL CHECK (json_valid(desired_state_json)),
    next_attempt_at TEXT,
    deadline_at TEXT,
    cancellation_requested_at TEXT,
    claimed_by TEXT,
    lease_until TEXT,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    outcome_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(outcome_json)),
    unresolved_count INTEGER NOT NULL DEFAULT 0 CHECK (unresolved_count >= 0),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    updated_at TEXT NOT NULL CHECK (length(trim(updated_at)) > 0),
    FOREIGN KEY (plan_id, plan_revision, plan_digest)
        REFERENCES action_plan_revisions(plan_id, revision, digest),
    UNIQUE (plan_id, plan_revision, plan_digest)
);

CREATE TABLE action_attempts (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    action_run_id TEXT NOT NULL REFERENCES action_runs(id),
    attempt_number INTEGER NOT NULL CHECK (attempt_number > 0),
    phase TEXT NOT NULL CHECK (phase IN ('observe', 'dispatch', 'reconcile')),
    state TEXT NOT NULL CHECK (state IN ('running', 'reconciling', 'succeeded', 'failed', 'cancelled')),
    started_at TEXT NOT NULL CHECK (length(trim(started_at)) > 0),
    finished_at TEXT,
    error_code TEXT,
    error_detail TEXT,
    outcome_certainty TEXT NOT NULL CHECK (outcome_certainty IN ('not_dispatched', 'known', 'uncertain')),
    external_id TEXT,
    evidence_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(evidence_json)),
    UNIQUE (action_run_id, attempt_number)
);

CREATE TABLE action_effects (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    action_run_id TEXT NOT NULL REFERENCES action_runs(id),
    attempt_id TEXT REFERENCES action_attempts(id),
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    target_kind TEXT NOT NULL CHECK (length(trim(target_kind)) > 0),
    target_id TEXT NOT NULL CHECK (length(trim(target_id)) > 0),
    effect_kind TEXT NOT NULL CHECK (length(trim(effect_kind)) > 0),
    state TEXT NOT NULL CHECK (state IN ('pending', 'applied', 'already_satisfied', 'failed', 'cancelled', 'unknown')),
    evidence_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(evidence_json)),
    observed_at TEXT NOT NULL CHECK (length(trim(observed_at)) > 0),
    UNIQUE (action_run_id, ordinal)
);

CREATE TABLE idempotency_records (
    scope TEXT NOT NULL CHECK (length(trim(scope)) > 0),
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) > 0 AND length(idempotency_key) <= 200),
    request_digest TEXT NOT NULL CHECK (length(trim(request_digest)) > 0),
    status_code INTEGER NOT NULL CHECK (status_code >= 100 AND status_code <= 599),
    resource_kind TEXT NOT NULL CHECK (length(trim(resource_kind)) > 0),
    resource_id TEXT NOT NULL CHECK (length(trim(resource_id)) > 0),
    response_json TEXT NOT NULL CHECK (json_valid(response_json)),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    expires_at TEXT,
    PRIMARY KEY (scope, idempotency_key)
);

CREATE TABLE trash_entries (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    root_id TEXT NOT NULL REFERENCES storage_roots(id),
    state TEXT NOT NULL CHECK (state IN ('planned', 'trashed', 'restoring', 'restored', 'purging', 'purged', 'held', 'failed')),
    original_prefix TEXT NOT NULL CHECK (length(trim(original_prefix)) > 0),
    trash_prefix TEXT NOT NULL CHECK (length(trim(trash_prefix)) > 0),
    manifest_json TEXT NOT NULL CHECK (json_valid(manifest_json)),
    retention_seconds INTEGER NOT NULL CHECK (retention_seconds > 0),
    trashed_at TEXT,
    expires_at TEXT NOT NULL CHECK (length(trim(expires_at)) > 0),
    hold_reason TEXT,
    client_state_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(client_state_json)),
    purge_claimed_at TEXT,
    restore_requested_at TEXT,
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    updated_at TEXT NOT NULL CHECK (length(trim(updated_at)) > 0),
    CHECK (state <> 'trashed' OR trashed_at IS NOT NULL)
);

CREATE TABLE trash_items (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    entry_id TEXT NOT NULL REFERENCES trash_entries(id),
    root_id TEXT NOT NULL REFERENCES storage_roots(id),
    original_relative_path TEXT NOT NULL CHECK (length(trim(original_relative_path)) > 0 AND original_relative_path NOT IN ('.', '..') AND substr(original_relative_path, 1, 1) <> '/' AND instr('/' || original_relative_path || '/', '/../') = 0 AND instr('/' || original_relative_path || '/', '/./') = 0 AND instr(original_relative_path, '//') = 0),
    trash_relative_path TEXT NOT NULL CHECK (length(trim(trash_relative_path)) > 0 AND trash_relative_path NOT IN ('.', '..') AND substr(trash_relative_path, 1, 1) <> '/' AND instr('/' || trash_relative_path || '/', '/../') = 0 AND instr('/' || trash_relative_path || '/', '/./') = 0 AND instr(trash_relative_path, '//') = 0),
    entry_type TEXT NOT NULL CHECK (entry_type IN ('file', 'directory', 'subtitle', 'companion')),
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    digest TEXT,
    file_identity TEXT,
    client_connection_id TEXT REFERENCES connections(id),
    client_external_id TEXT,
    state TEXT NOT NULL CHECK (state IN ('selected', 'trashed', 'restored', 'purged', 'held', 'failed')),
    trashed_at TEXT,
    restored_at TEXT,
    purged_at TEXT,
    UNIQUE (entry_id, original_relative_path),
    CHECK ((client_connection_id IS NULL AND client_external_id IS NULL) OR (client_connection_id IS NOT NULL AND client_external_id IS NOT NULL AND length(trim(client_external_id)) > 0))
);

CREATE TABLE janitor_records (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    trash_entry_id TEXT NOT NULL REFERENCES trash_entries(id),
    operation TEXT NOT NULL CHECK (operation IN ('purge', 'restore')),
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'waiting_dependency', 'reconciling', 'succeeded', 'failed', 'held', 'cancelled')),
    next_attempt_at TEXT,
    claimed_by TEXT,
    lease_until TEXT,
    outcome_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(outcome_json)),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    updated_at TEXT NOT NULL CHECK (length(trim(updated_at)) > 0),
    UNIQUE (trash_entry_id, operation)
);

CREATE TABLE audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE CHECK (length(trim(event_id)) > 0),
    occurred_at TEXT NOT NULL CHECK (length(trim(occurred_at)) > 0),
    actor TEXT NOT NULL DEFAULT 'unauthenticated' CHECK (length(trim(actor)) > 0),
    action TEXT NOT NULL CHECK (length(trim(action)) > 0),
    resource_kind TEXT NOT NULL CHECK (length(trim(resource_kind)) > 0),
    resource_id TEXT,
    plan_digest TEXT,
    outcome TEXT NOT NULL CHECK (length(trim(outcome)) > 0),
    metadata_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(metadata_json)),
    redacted INTEGER NOT NULL DEFAULT 1 CHECK (redacted IN (0, 1))
);

CREATE INDEX idx_connections_active ON connections (retired_at, kind);
CREATE INDEX idx_roots_active ON storage_roots (retired_at, purpose);
CREATE INDEX idx_mappings_scope ON path_mappings (connection_id, root_id, retired_at);
CREATE INDEX idx_scans_scope ON scans (scope_kind, scope_id, state);
CREATE INDEX idx_coverage_scope ON coverage_snapshots (connection_id, root_id, observed_at);
CREATE INDEX idx_discoveries_root_path ON discoveries (root_id, relative_path, last_seen_at);
CREATE INDEX idx_file_observations_discovery ON file_observations (discovery_id, observed_at);
CREATE INDEX idx_downloads_connection_state ON downloads (connection_id, state, last_seen_at);
CREATE INDEX idx_provenance_download ON provenance_links (download_id, last_seen_at);
CREATE INDEX idx_external_records_connection ON external_records (connection_id, record_kind, last_seen_at);
CREATE INDEX idx_tracking_dimension ON tracking_observations (dimension, observed_at);
CREATE INDEX idx_plan_revisions_state ON action_plan_revisions (state, expires_at);
CREATE INDEX idx_action_runs_due ON action_runs (state, next_attempt_at, deadline_at);
CREATE INDEX idx_action_attempts_run ON action_attempts (action_run_id, attempt_number);
CREATE INDEX idx_action_effects_run ON action_effects (action_run_id, ordinal);
CREATE INDEX idx_workflow_steps_run ON workflow_steps (workflow_id, step_index);
CREATE INDEX idx_trash_expiry ON trash_entries (state, expires_at);
CREATE INDEX idx_trash_items_entry ON trash_items (entry_id, state);
CREATE INDEX idx_janitor_due ON janitor_records (state, next_attempt_at);
CREATE INDEX idx_audit_resource ON audit_events (resource_kind, resource_id, occurred_at);

CREATE TRIGGER tracking_observations_connection_scope_insert
BEFORE INSERT ON tracking_observations
WHEN NEW.external_record_id IS NOT NULL
 AND NEW.connection_id IS NOT NULL
 AND NEW.connection_id <> (SELECT connection_id FROM external_records WHERE id = NEW.external_record_id)
BEGIN
    SELECT RAISE(ABORT, 'tracking observation connection does not match external record');
END;

CREATE TRIGGER tracking_observations_connection_scope_update
BEFORE UPDATE ON tracking_observations
WHEN NEW.external_record_id IS NOT NULL
 AND NEW.connection_id IS NOT NULL
 AND NEW.connection_id <> (SELECT connection_id FROM external_records WHERE id = NEW.external_record_id)
BEGIN
    SELECT RAISE(ABORT, 'tracking observation connection does not match external record');
END;

CREATE TRIGGER tracking_observations_absence_coverage_insert
BEFORE INSERT ON tracking_observations
WHEN NEW.status = 'absent'
 AND NOT EXISTS (
     SELECT 1 FROM coverage_snapshots
     WHERE id = NEW.coverage_id AND completeness = 'complete'
 )
BEGIN
    SELECT RAISE(ABORT, 'absent tracking observation requires complete coverage');
END;

CREATE TRIGGER tracking_observations_absence_coverage_update
BEFORE UPDATE ON tracking_observations
WHEN NEW.status = 'absent'
 AND NOT EXISTS (
     SELECT 1 FROM coverage_snapshots
     WHERE id = NEW.coverage_id AND completeness = 'complete'
 )
BEGIN
    SELECT RAISE(ABORT, 'absent tracking observation requires complete coverage');
END;

CREATE TRIGGER config_snapshots_immutable_update
BEFORE UPDATE ON config_snapshots
BEGIN
    SELECT RAISE(ABORT, 'configuration snapshots are immutable');
END;

CREATE TRIGGER config_snapshots_immutable_delete
BEFORE DELETE ON config_snapshots
BEGIN
    SELECT RAISE(ABORT, 'configuration snapshots are immutable');
END;

-- These records are the approval/evidence boundary. They are append-only so a
-- later request cannot silently change what was approved or audited.
CREATE TRIGGER action_plan_revisions_immutable_update
BEFORE UPDATE ON action_plan_revisions
BEGIN
    SELECT RAISE(ABORT, 'action plan revisions are immutable');
END;

CREATE TRIGGER action_plan_revisions_immutable_delete
BEFORE DELETE ON action_plan_revisions
BEGIN
    SELECT RAISE(ABORT, 'action plan revisions are immutable');
END;

CREATE TRIGGER plan_manifests_immutable_update
BEFORE UPDATE ON plan_manifests
BEGIN
    SELECT RAISE(ABORT, 'plan manifests are immutable');
END;

CREATE TRIGGER plan_manifests_immutable_delete
BEFORE DELETE ON plan_manifests
BEGIN
    SELECT RAISE(ABORT, 'plan manifests are immutable');
END;

CREATE TRIGGER review_decisions_immutable_update
BEFORE UPDATE ON review_decisions
BEGIN
    SELECT RAISE(ABORT, 'review decisions are immutable');
END;

CREATE TRIGGER review_decisions_immutable_delete
BEFORE DELETE ON review_decisions
BEGIN
    SELECT RAISE(ABORT, 'review decisions are immutable');
END;

CREATE TRIGGER idempotency_records_immutable_update
BEFORE UPDATE ON idempotency_records
BEGIN
    SELECT RAISE(ABORT, 'idempotency records are immutable');
END;

CREATE TRIGGER idempotency_records_immutable_delete
BEFORE DELETE ON idempotency_records
BEGIN
    SELECT RAISE(ABORT, 'idempotency records are immutable');
END;

CREATE TRIGGER audit_events_immutable_update
BEFORE UPDATE ON audit_events
BEGIN
    SELECT RAISE(ABORT, 'audit events are append-only');
END;

CREATE TRIGGER audit_events_immutable_delete
BEFORE DELETE ON audit_events
BEGIN
    SELECT RAISE(ABORT, 'audit events are append-only');
END;
