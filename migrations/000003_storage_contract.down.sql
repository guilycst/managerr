-- Restore the v2 journal shape. This path is intended for an explicitly
-- managed downgrade; startup still refuses a dirty or failed migration.
PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

DROP TRIGGER IF EXISTS janitor_records_finish_trash_entry;
DROP TRIGGER IF EXISTS janitor_records_reconcile_trash_entry;
DROP TRIGGER IF EXISTS janitor_records_claim_trash_entry;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_update;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_insert;
DROP TRIGGER IF EXISTS tracking_observations_connection_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_connection_scope_insert;
DROP TRIGGER IF EXISTS tracking_observations_identity_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_identity_scope_insert;
DROP TRIGGER IF EXISTS config_source_scope_mappings_update;
DROP TRIGGER IF EXISTS config_source_scope_mappings_insert;
DROP TRIGGER IF EXISTS config_source_scope_roots_update;
DROP TRIGGER IF EXISTS config_source_scope_roots_insert;
DROP TRIGGER IF EXISTS config_source_scope_connections_update;
DROP TRIGGER IF EXISTS config_source_scope_connections_insert;

DROP INDEX IF EXISTS config_snapshots_id_source;
DROP INDEX IF EXISTS discoveries_id_scope;
DROP INDEX IF EXISTS action_attempts_id_scope;
DROP INDEX IF EXISTS trash_entries_id_root;

ALTER TABLE coverage_snapshots RENAME TO coverage_snapshots_v3;
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
INSERT INTO coverage_snapshots (
    id, scan_id, source_id, connection_id, root_id, completeness,
    reason_codes_json, observed_count, snapshot_revision, started_at,
    completed_at, observed_at, created_at
)
SELECT id, scan_id, source_id, connection_id, root_id, completeness,
       reason_codes_json, observed_count, snapshot_revision, started_at,
       completed_at, observed_at, created_at
FROM coverage_snapshots_v3;
DROP TABLE coverage_snapshots_v3;

ALTER TABLE tracking_observations RENAME TO tracking_observations_v3;
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
INSERT INTO tracking_observations (
    id, external_record_id, media_identity_id, connection_id, dimension, status,
    evidence_json, coverage_id, observed_at, registered_at, imported_at
)
SELECT id, external_record_id, media_identity_id, connection_id, dimension, status,
       evidence_json, coverage_id, observed_at, registered_at, imported_at
FROM tracking_observations_v3;
DROP TABLE tracking_observations_v3;

ALTER TABLE downloads RENAME TO downloads_v3;
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
INSERT INTO downloads (
    id, connection_id, external_id, protocol, name, content_hash, nzb_id,
    state, completed_at, seeding_state, payload_json, history_json,
    first_seen_at, last_seen_at
)
SELECT id, connection_id, external_id, protocol, name, content_hash, nzb_id,
       CASE state WHEN 'complete' THEN 'completed' ELSE state END,
       completed_at, seeding_state, payload_json, history_json,
       first_seen_at, last_seen_at
FROM downloads_v3;
DROP TABLE downloads_v3;

ALTER TABLE file_observations RENAME TO file_observations_v3;
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
INSERT INTO file_observations (
    id, discovery_id, root_id, relative_path, entry_type, size_bytes, digest,
    file_identity, role, observed_at, manifest_revision, child_manifest_json,
    first_seen_at, last_seen_at, deleted_at
)
SELECT id, discovery_id, root_id, relative_path, entry_type, size_bytes, digest,
       file_identity, role, observed_at, manifest_revision, child_manifest_json,
       first_seen_at, last_seen_at, deleted_at
FROM file_observations_v3;
DROP TABLE file_observations_v3;

ALTER TABLE action_effects RENAME TO action_effects_v3;
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
INSERT INTO action_effects (
    id, action_run_id, attempt_id, ordinal, target_kind, target_id, effect_kind,
    state, evidence_json, observed_at
)
SELECT id, action_run_id, attempt_id, ordinal, target_kind, target_id, effect_kind,
       state, evidence_json, observed_at
FROM action_effects_v3;
DROP TABLE action_effects_v3;

ALTER TABLE trash_entries RENAME TO trash_entries_v3;
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
INSERT INTO trash_entries (
    id, root_id, state, original_prefix, trash_prefix, manifest_json,
    retention_seconds, trashed_at, expires_at, hold_reason, client_state_json,
    purge_claimed_at, restore_requested_at, created_at, updated_at
)
SELECT id, root_id, state, original_prefix, trash_prefix, manifest_json,
       retention_seconds, trashed_at, expires_at, hold_reason, client_state_json,
       purge_claimed_at, restore_requested_at, created_at, updated_at
FROM trash_entries_v3;
DROP TABLE trash_entries_v3;

ALTER TABLE trash_items RENAME TO trash_items_v3;
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
INSERT INTO trash_items (
    id, entry_id, root_id, original_relative_path, trash_relative_path,
    entry_type, size_bytes, digest, file_identity, client_connection_id,
    client_external_id, state, trashed_at, restored_at, purged_at
)
SELECT id, entry_id, root_id, original_relative_path, trash_relative_path,
       entry_type, size_bytes, digest, file_identity, client_connection_id,
       client_external_id, state, trashed_at, restored_at, purged_at
FROM trash_items_v3;
DROP TABLE trash_items_v3;

ALTER TABLE janitor_records RENAME TO janitor_records_v3;
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
INSERT INTO janitor_records (
    id, trash_entry_id, operation, state, next_attempt_at, claimed_by,
    lease_until, outcome_json, created_at, updated_at
)
SELECT id, trash_entry_id, operation, state, next_attempt_at, claimed_by,
       lease_until, outcome_json, created_at, updated_at
FROM janitor_records_v3;
DROP TABLE janitor_records_v3;

CREATE INDEX IF NOT EXISTS idx_tracking_dimension
    ON tracking_observations (dimension, observed_at);
CREATE INDEX IF NOT EXISTS idx_coverage_scope
    ON coverage_snapshots (connection_id, root_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_file_observations_discovery
    ON file_observations (discovery_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_downloads_connection_state
    ON downloads (connection_id, state, last_seen_at);
CREATE INDEX IF NOT EXISTS idx_action_effects_run
    ON action_effects (action_run_id, ordinal);
CREATE INDEX IF NOT EXISTS idx_trash_items_entry
    ON trash_items (entry_id, state);

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

PRAGMA legacy_alter_table = OFF;
PRAGMA foreign_keys = ON;
