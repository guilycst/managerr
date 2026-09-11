-- Return to the v3 journal shape. Legacy tracking rows remain explicitly
-- unknown when they could not be safely scoped; this downgrade therefore
-- keeps the v3 nullable connection contract introduced by 000003.
PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

DROP TRIGGER IF EXISTS janitor_records_approved_purge_claim;
DROP TRIGGER IF EXISTS janitor_records_approval_immutable_update;
DROP TRIGGER IF EXISTS janitor_records_approval_shape_update;
DROP TRIGGER IF EXISTS janitor_records_approval_shape_insert;
DROP TRIGGER IF EXISTS janitor_records_finish_trash_entry;
DROP TRIGGER IF EXISTS janitor_records_reconcile_trash_entry;
DROP TRIGGER IF EXISTS janitor_records_claim_trash_entry;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_update;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_insert;
DROP TRIGGER IF EXISTS tracking_observations_identity_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_identity_scope_insert;
DROP TRIGGER IF EXISTS tracking_observations_connection_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_connection_scope_insert;

ALTER TABLE tracking_observations RENAME TO tracking_observations_v4;
CREATE TABLE tracking_observations (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    external_record_id TEXT REFERENCES external_records(id),
    media_identity_id TEXT REFERENCES media_identities(id),
    connection_id TEXT REFERENCES connections(id),
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
    CHECK (connection_id IS NOT NULL OR status = 'unknown'),
    CHECK (status <> 'absent' OR (media_identity_id IS NOT NULL AND root_id IS NOT NULL AND coverage_id IS NOT NULL AND coverage_max_age_seconds IS NOT NULL AND coverage_max_age_seconds > 0)),
    UNIQUE (external_record_id, dimension, observed_at)
);
INSERT INTO tracking_observations (
    id, external_record_id, media_identity_id, connection_id, root_id, dimension, status,
    evidence_json, coverage_id, coverage_max_age_seconds, observed_at,
    registered_at, imported_at
)
SELECT id, external_record_id, media_identity_id, connection_id, root_id, dimension, status,
       evidence_json, coverage_id, coverage_max_age_seconds, observed_at,
       registered_at, imported_at
FROM tracking_observations_v4;
DROP TABLE tracking_observations_v4;

ALTER TABLE janitor_records RENAME TO janitor_records_v4;
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
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    UNIQUE (trash_entry_id, operation)
);
INSERT INTO janitor_records (
    id, trash_entry_id, operation, state, next_attempt_at, claimed_by,
    lease_until, outcome_json, created_at, updated_at, version
)
SELECT id, trash_entry_id, operation, state, next_attempt_at, claimed_by,
       lease_until, outcome_json, created_at, updated_at, version
FROM janitor_records_v4;
DROP TABLE janitor_records_v4;

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

CREATE INDEX IF NOT EXISTS idx_tracking_dimension
    ON tracking_observations (dimension, observed_at);
CREATE INDEX IF NOT EXISTS idx_janitor_due
    ON janitor_records (state, next_attempt_at);

PRAGMA legacy_alter_table = OFF;
PRAGMA foreign_keys = ON;
CREATE TEMP TABLE d04_down_foreign_key_check (
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d04_down_foreign_key_check (ok)
SELECT CASE WHEN EXISTS (SELECT 1 FROM pragma_foreign_key_check) THEN 0 ELSE 1 END;
DROP TABLE d04_down_foreign_key_check;
