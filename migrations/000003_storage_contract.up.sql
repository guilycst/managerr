-- D-01 correction migration. The first public journal shape allowed several
-- redundant scope columns to drift and used an internal download state that
-- did not match the API. This migration rebuilds those tables with composite
-- foreign keys and keeps the old records that can be translated safely.
--
-- golang-migrate runs this migration with transaction wrapping disabled by the
-- storage runner. Foreign-key checks must be disabled while parent tables are
-- rebuilt; legacy_alter_table keeps existing child references pointed at the
-- replacement table. A failure leaves the migration dirty and startup fails
-- closed, which is safer than serving a partially upgraded journal.

PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

ALTER TABLE coverage_snapshots ADD COLUMN media_identity_id TEXT REFERENCES media_identities(id);
ALTER TABLE trash_entries ADD COLUMN active_operation TEXT CHECK (active_operation IS NULL OR active_operation IN ('purge', 'restore'));
ALTER TABLE trash_entries ADD COLUMN operation_claimed_by TEXT;
ALTER TABLE trash_entries ADD COLUMN operation_lease_until TEXT;
ALTER TABLE trash_entries ADD COLUMN version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0);
ALTER TABLE janitor_records ADD COLUMN version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0);

CREATE UNIQUE INDEX IF NOT EXISTS config_snapshots_id_source
    ON config_snapshots (id, source);
CREATE UNIQUE INDEX IF NOT EXISTS discoveries_id_scope
    ON discoveries (id, root_id, relative_path, manifest_revision);
CREATE UNIQUE INDEX IF NOT EXISTS action_attempts_id_scope
    ON action_attempts (id, action_run_id);
CREATE UNIQUE INDEX IF NOT EXISTS trash_entries_id_root
    ON trash_entries (id, root_id);

DROP TRIGGER IF EXISTS tracking_observations_connection_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_connection_scope_insert;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_update;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_insert;

ALTER TABLE tracking_observations RENAME TO tracking_observations_legacy;
CREATE TABLE tracking_observations (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    external_record_id TEXT REFERENCES external_records(id),
    media_identity_id TEXT REFERENCES media_identities(id),
    connection_id TEXT NOT NULL REFERENCES connections(id),
    root_id TEXT REFERENCES storage_roots(id),
    dimension TEXT NOT NULL CHECK (dimension IN ('registration', 'import', 'availability', 'request')),
    status TEXT NOT NULL CHECK (status IN ('present', 'absent', 'unknown')),
    evidence_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(evidence_json)),
    coverage_id TEXT REFERENCES coverage_snapshots(id),
    coverage_max_age_seconds INTEGER,
    observed_at TEXT NOT NULL CHECK (length(trim(observed_at)) > 0),
    registered_at TEXT,
    imported_at TEXT,
    CHECK (external_record_id IS NOT NULL OR media_identity_id IS NOT NULL OR status = 'unknown'),
    CHECK (status <> 'absent' OR (media_identity_id IS NOT NULL AND root_id IS NOT NULL AND coverage_id IS NOT NULL AND coverage_max_age_seconds IS NOT NULL AND coverage_max_age_seconds > 0)),
    UNIQUE (external_record_id, dimension, observed_at)
);
INSERT INTO tracking_observations (
    id, external_record_id, media_identity_id, connection_id, root_id, dimension, status,
    evidence_json, coverage_id, coverage_max_age_seconds, observed_at,
    registered_at, imported_at
)
SELECT id, external_record_id, media_identity_id, connection_id, NULL, dimension, status,
       evidence_json, coverage_id, NULL, observed_at, registered_at, imported_at
FROM tracking_observations_legacy;
DROP TABLE tracking_observations_legacy;

ALTER TABLE file_observations RENAME TO file_observations_legacy;
CREATE TABLE file_observations (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    discovery_id TEXT NOT NULL,
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
    FOREIGN KEY (discovery_id, root_id, relative_path, manifest_revision)
        REFERENCES discoveries(id, root_id, relative_path, manifest_revision)
);
INSERT INTO file_observations (
    id, discovery_id, root_id, relative_path, entry_type, size_bytes, digest,
    file_identity, role, observed_at, manifest_revision, child_manifest_json,
    first_seen_at, last_seen_at, deleted_at
)
SELECT id, discovery_id, root_id, relative_path, entry_type, size_bytes, digest,
       file_identity, role, observed_at, manifest_revision, child_manifest_json,
       first_seen_at, last_seen_at, deleted_at
FROM file_observations_legacy;
DROP TABLE file_observations_legacy;

ALTER TABLE downloads RENAME TO downloads_legacy;
CREATE TABLE downloads (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    connection_id TEXT NOT NULL REFERENCES connections(id),
    external_id TEXT NOT NULL CHECK (length(trim(external_id)) > 0),
    protocol TEXT NOT NULL CHECK (protocol IN ('qbittorrent', 'nzbget')),
    name TEXT,
    content_hash TEXT,
    nzb_id TEXT,
    state TEXT NOT NULL CHECK (state IN ('queued', 'downloading', 'seeding', 'complete', 'processing', 'failed', 'unknown')),
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
       CASE state
           WHEN 'completed' THEN 'complete'
           WHEN 'paused' THEN 'unknown'
           ELSE state
       END,
       completed_at, seeding_state, payload_json, history_json,
       first_seen_at, last_seen_at
FROM downloads_legacy;
DROP TABLE downloads_legacy;

ALTER TABLE action_effects RENAME TO action_effects_legacy;
CREATE TABLE action_effects (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    action_run_id TEXT NOT NULL REFERENCES action_runs(id),
    attempt_id TEXT,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    target_kind TEXT NOT NULL CHECK (length(trim(target_kind)) > 0),
    target_id TEXT NOT NULL CHECK (length(trim(target_id)) > 0),
    effect_kind TEXT NOT NULL CHECK (length(trim(effect_kind)) > 0),
    state TEXT NOT NULL CHECK (state IN ('pending', 'applied', 'already_satisfied', 'failed', 'cancelled', 'unknown')),
    evidence_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(evidence_json)),
    observed_at TEXT NOT NULL CHECK (length(trim(observed_at)) > 0),
    UNIQUE (action_run_id, ordinal),
    FOREIGN KEY (attempt_id, action_run_id)
        REFERENCES action_attempts(id, action_run_id)
);
INSERT INTO action_effects (
    id, action_run_id, attempt_id, ordinal, target_kind, target_id, effect_kind,
    state, evidence_json, observed_at
)
SELECT id, action_run_id, attempt_id, ordinal, target_kind, target_id, effect_kind,
       state, evidence_json, observed_at
FROM action_effects_legacy;
DROP TABLE action_effects_legacy;

ALTER TABLE trash_items RENAME TO trash_items_legacy;
CREATE TABLE trash_items (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    entry_id TEXT NOT NULL,
    root_id TEXT NOT NULL,
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
    CHECK ((client_connection_id IS NULL AND client_external_id IS NULL) OR (client_connection_id IS NOT NULL AND client_external_id IS NOT NULL AND length(trim(client_external_id)) > 0)),
    FOREIGN KEY (entry_id, root_id)
        REFERENCES trash_entries(id, root_id)
);
INSERT INTO trash_items (
    id, entry_id, root_id, original_relative_path, trash_relative_path,
    entry_type, size_bytes, digest, file_identity, client_connection_id,
    client_external_id, state, trashed_at, restored_at, purged_at
)
SELECT id, entry_id, root_id, original_relative_path, trash_relative_path,
       entry_type, size_bytes, digest, file_identity, client_connection_id,
       client_external_id, state, trashed_at, restored_at, purged_at
FROM trash_items_legacy;
DROP TABLE trash_items_legacy;

PRAGMA legacy_alter_table = OFF;
PRAGMA foreign_keys = ON;

DROP TRIGGER IF EXISTS config_source_scope_connections_update;
DROP TRIGGER IF EXISTS config_source_scope_connections_insert;
DROP TRIGGER IF EXISTS config_source_scope_roots_update;
DROP TRIGGER IF EXISTS config_source_scope_roots_insert;
DROP TRIGGER IF EXISTS config_source_scope_mappings_update;
DROP TRIGGER IF EXISTS config_source_scope_mappings_insert;

CREATE TRIGGER config_source_scope_connections_insert
BEFORE INSERT ON connections
WHEN NOT EXISTS (
    SELECT 1 FROM config_snapshots
    WHERE id = NEW.source_snapshot_id AND source = NEW.source
)
BEGIN
    SELECT RAISE(ABORT, 'connection source does not match configuration snapshot');
END;

CREATE TRIGGER config_source_scope_connections_update
BEFORE UPDATE ON connections
WHEN NOT EXISTS (
    SELECT 1 FROM config_snapshots
    WHERE id = NEW.source_snapshot_id AND source = NEW.source
)
BEGIN
    SELECT RAISE(ABORT, 'connection source does not match configuration snapshot');
END;

CREATE TRIGGER config_source_scope_roots_insert
BEFORE INSERT ON storage_roots
WHEN NOT EXISTS (
    SELECT 1 FROM config_snapshots
    WHERE id = NEW.source_snapshot_id AND source = NEW.source
)
BEGIN
    SELECT RAISE(ABORT, 'storage root source does not match configuration snapshot');
END;

CREATE TRIGGER config_source_scope_roots_update
BEFORE UPDATE ON storage_roots
WHEN NOT EXISTS (
    SELECT 1 FROM config_snapshots
    WHERE id = NEW.source_snapshot_id AND source = NEW.source
)
BEGIN
    SELECT RAISE(ABORT, 'storage root source does not match configuration snapshot');
END;

CREATE TRIGGER config_source_scope_mappings_insert
BEFORE INSERT ON path_mappings
WHEN NOT EXISTS (
    SELECT 1 FROM config_snapshots
    WHERE id = NEW.source_snapshot_id AND source = NEW.source
)
BEGIN
    SELECT RAISE(ABORT, 'path mapping source does not match configuration snapshot');
END;

CREATE TRIGGER config_source_scope_mappings_update
BEFORE UPDATE ON path_mappings
WHEN NOT EXISTS (
    SELECT 1 FROM config_snapshots
    WHERE id = NEW.source_snapshot_id AND source = NEW.source
)
BEGIN
    SELECT RAISE(ABORT, 'path mapping source does not match configuration snapshot');
END;

CREATE TRIGGER tracking_observations_connection_scope_insert
BEFORE INSERT ON tracking_observations
WHEN NEW.external_record_id IS NOT NULL
 AND NEW.connection_id <> (SELECT connection_id FROM external_records WHERE id = NEW.external_record_id)
BEGIN
    SELECT RAISE(ABORT, 'tracking observation connection does not match external record');
END;

CREATE TRIGGER tracking_observations_connection_scope_update
BEFORE UPDATE ON tracking_observations
WHEN NEW.external_record_id IS NOT NULL
 AND NEW.connection_id <> (SELECT connection_id FROM external_records WHERE id = NEW.external_record_id)
BEGIN
    SELECT RAISE(ABORT, 'tracking observation connection does not match external record');
END;

CREATE TRIGGER tracking_observations_identity_scope_insert
BEFORE INSERT ON tracking_observations
WHEN NEW.external_record_id IS NOT NULL
 AND NEW.media_identity_id IS NOT NULL
 AND (SELECT media_identity_id FROM external_records WHERE id = NEW.external_record_id) IS NOT NULL
 AND NEW.media_identity_id <> (SELECT media_identity_id FROM external_records WHERE id = NEW.external_record_id)
BEGIN
    SELECT RAISE(ABORT, 'tracking observation media identity does not match external record');
END;

CREATE TRIGGER tracking_observations_identity_scope_update
BEFORE UPDATE ON tracking_observations
WHEN NEW.external_record_id IS NOT NULL
 AND NEW.media_identity_id IS NOT NULL
 AND (SELECT media_identity_id FROM external_records WHERE id = NEW.external_record_id) IS NOT NULL
 AND NEW.media_identity_id <> (SELECT media_identity_id FROM external_records WHERE id = NEW.external_record_id)
BEGIN
    SELECT RAISE(ABORT, 'tracking observation media identity does not match external record');
END;

CREATE TRIGGER tracking_observations_absence_coverage_insert
BEFORE INSERT ON tracking_observations
WHEN NEW.status = 'absent'
 AND NOT EXISTS (
     SELECT 1
     FROM coverage_snapshots
     WHERE id = NEW.coverage_id
       AND connection_id = NEW.connection_id
       AND media_identity_id = NEW.media_identity_id
       AND root_id = NEW.root_id
       AND completeness = 'complete'
       AND completed_at IS NOT NULL
       AND completed_at <= NEW.observed_at
       AND observed_at <= NEW.observed_at
       AND julianday(observed_at) IS NOT NULL
       AND julianday(NEW.observed_at) IS NOT NULL
       AND (julianday(NEW.observed_at) - julianday(observed_at)) * 86400.0 <= NEW.coverage_max_age_seconds
       AND (julianday(NEW.observed_at) - julianday(observed_at)) * 86400.0 >= 0
 )
BEGIN
    SELECT RAISE(ABORT, 'absent tracking observation requires matching fresh complete coverage');
END;

CREATE TRIGGER tracking_observations_absence_coverage_update
BEFORE UPDATE ON tracking_observations
WHEN NEW.status = 'absent'
 AND NOT EXISTS (
     SELECT 1
     FROM coverage_snapshots
     WHERE id = NEW.coverage_id
       AND connection_id = NEW.connection_id
       AND media_identity_id = NEW.media_identity_id
       AND root_id = NEW.root_id
       AND completeness = 'complete'
       AND completed_at IS NOT NULL
       AND completed_at <= NEW.observed_at
       AND observed_at <= NEW.observed_at
       AND julianday(observed_at) IS NOT NULL
       AND julianday(NEW.observed_at) IS NOT NULL
       AND (julianday(NEW.observed_at) - julianday(observed_at)) * 86400.0 <= NEW.coverage_max_age_seconds
       AND (julianday(NEW.observed_at) - julianday(observed_at)) * 86400.0 >= 0
 )
BEGIN
    SELECT RAISE(ABORT, 'absent tracking observation requires matching fresh complete coverage');
END;

DROP TRIGGER IF EXISTS janitor_records_claim_trash_entry;
DROP TRIGGER IF EXISTS janitor_records_reconcile_trash_entry;
DROP TRIGGER IF EXISTS janitor_records_finish_trash_entry;

CREATE TRIGGER janitor_records_claim_trash_entry
BEFORE UPDATE OF state ON janitor_records
WHEN NEW.state = 'running' AND OLD.state <> 'running'
BEGIN
    UPDATE trash_entries
    SET active_operation = NEW.operation,
        operation_claimed_by = NEW.claimed_by,
        operation_lease_until = NEW.lease_until,
        state = CASE NEW.operation
            WHEN 'purge' THEN 'purging'
            ELSE 'restoring'
        END,
        version = version + 1,
        updated_at = NEW.updated_at
    WHERE id = NEW.trash_entry_id
      AND (active_operation IS NULL OR active_operation = NEW.operation)
      AND (active_operation IS NULL OR operation_lease_until IS NULL OR julianday(operation_lease_until) <= julianday(NEW.updated_at))
      AND (
          (NEW.operation = 'purge' AND ((state = 'trashed' AND julianday(expires_at) IS NOT NULL AND julianday(NEW.updated_at) IS NOT NULL AND julianday(expires_at) <= julianday(NEW.updated_at)) OR (active_operation = 'purge' AND state = 'purging')))
          OR (NEW.operation = 'restore' AND ((state IN ('trashed', 'held', 'failed')) OR (active_operation = 'restore' AND state = 'restoring')))
      );
    SELECT CASE WHEN changes() = 0 THEN RAISE(ABORT, 'trash entry is already claimed or not eligible') END;
END;

CREATE TRIGGER janitor_records_reconcile_trash_entry
AFTER UPDATE OF state ON janitor_records
WHEN OLD.state = 'running' AND NEW.state = 'reconciling'
BEGIN
    UPDATE trash_entries
    SET operation_claimed_by = NULL,
        operation_lease_until = NULL,
        version = version + 1,
        updated_at = NEW.updated_at
    WHERE id = NEW.trash_entry_id AND active_operation = NEW.operation;
END;

CREATE TRIGGER janitor_records_finish_trash_entry
AFTER UPDATE OF state ON janitor_records
WHEN OLD.state IN ('running', 'reconciling')
 AND NEW.state IN ('succeeded', 'failed', 'held', 'cancelled')
BEGIN
    UPDATE trash_entries
    SET active_operation = NULL,
        operation_claimed_by = NULL,
        operation_lease_until = NULL,
        state = CASE
            WHEN NEW.state = 'succeeded' AND NEW.operation = 'purge' THEN 'purged'
            WHEN NEW.state = 'succeeded' AND NEW.operation = 'restore' THEN 'restored'
            WHEN NEW.state = 'held' THEN 'held'
            ELSE 'failed'
        END,
        version = version + 1,
        updated_at = NEW.updated_at
    WHERE id = NEW.trash_entry_id AND active_operation = NEW.operation;
END;

-- Table rebuilds detach the indexes that belonged to the old table names.
-- Recreate the lookup indexes after the replacement tables are in place.
CREATE INDEX IF NOT EXISTS idx_tracking_dimension
    ON tracking_observations (dimension, observed_at);
CREATE INDEX IF NOT EXISTS idx_file_observations_discovery
    ON file_observations (discovery_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_downloads_connection_state
    ON downloads (connection_id, state, last_seen_at);
CREATE INDEX IF NOT EXISTS idx_action_effects_run
    ON action_effects (action_run_id, ordinal);
CREATE INDEX IF NOT EXISTS idx_trash_items_entry
    ON trash_items (entry_id, state);

-- Exercise the source triggers against historical rows as part of the upgrade.
-- Any pre-existing mismatch aborts the migration and leaves the database dirty
-- instead of allowing an ambiguous ownership record to become active.
UPDATE connections SET source = source
WHERE EXISTS (
    SELECT 1 FROM config_snapshots
    WHERE config_snapshots.id = connections.source_snapshot_id
      AND config_snapshots.source <> connections.source
);
UPDATE storage_roots SET source = source
WHERE EXISTS (
    SELECT 1 FROM config_snapshots
    WHERE config_snapshots.id = storage_roots.source_snapshot_id
      AND config_snapshots.source <> storage_roots.source
);
UPDATE path_mappings SET source = source
WHERE EXISTS (
    SELECT 1 FROM config_snapshots
    WHERE config_snapshots.id = path_mappings.source_snapshot_id
      AND config_snapshots.source <> path_mappings.source
);
