-- Return the active v5 additions to the v4 journal shape. Quarantined
-- observations remain durable for an operator to inspect after a rollback;
-- v4 active tracking remains nullable because that was its compatibility
-- contract.
PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

-- A direct v5 -> v4 downgrade also removes the target table and action-run
-- CAS column. Neutralize approval-bearing work before that information is
-- removed so the v4 runtime cannot resume it as an ordinary janitor claim.
UPDATE action_runs
SET state = 'needs_review',
    claimed_by = NULL,
    lease_until = NULL,
    version = version + 1,
    outcome_json = json_object(
        'reason', 'approval_requires_new_review_after_downgrade',
        'previous_state', state,
        'previous_outcome', outcome_json
    ),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id IN (
    SELECT approval_action_run_id
    FROM janitor_records
    WHERE approval_plan_id IS NOT NULL
       OR approval_plan_revision IS NOT NULL
       OR approval_plan_digest IS NOT NULL
       OR approval_decision_id IS NOT NULL
       OR approval_action_run_id IS NOT NULL
       OR approved_entry_version IS NOT NULL
       OR approval_action_run_version IS NOT NULL
)
  AND state NOT IN ('succeeded', 'failed', 'cancelled', 'deadline_exceeded', 'needs_review');

UPDATE janitor_records
SET state = 'held',
    claimed_by = NULL,
    lease_until = NULL,
    version = version + 1,
    outcome_json = json_object(
        'reason', 'approval_requires_new_review_after_downgrade',
        'previous_state', state,
        'previous_outcome', outcome_json
    ),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE (
       approval_plan_id IS NOT NULL
    OR approval_plan_revision IS NOT NULL
    OR approval_plan_digest IS NOT NULL
    OR approval_decision_id IS NOT NULL
    OR approval_action_run_id IS NOT NULL
    OR approved_entry_version IS NOT NULL
    OR approval_action_run_version IS NOT NULL
)
  AND state IN ('queued', 'running', 'waiting_dependency', 'reconciling');

DROP TRIGGER IF EXISTS tracking_observations_quarantine_id_guard_insert;
DROP TRIGGER IF EXISTS tracking_observations_quarantine_id_guard_update;
DROP TRIGGER IF EXISTS janitor_records_generic_claim_requires_exact_approval;
DROP TRIGGER IF EXISTS external_records_tracking_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_connection_required_update;
DROP TRIGGER IF EXISTS tracking_observations_connection_required_insert;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_update;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_insert;
DROP TRIGGER IF EXISTS tracking_observations_identity_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_identity_scope_insert;
DROP TRIGGER IF EXISTS tracking_observations_connection_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_connection_scope_insert;

ALTER TABLE tracking_observations RENAME TO tracking_observations_v5;
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
    id, external_record_id, media_identity_id, connection_id, root_id, dimension,
    status, evidence_json, coverage_id, coverage_max_age_seconds, observed_at,
    registered_at, imported_at
)
SELECT id, external_record_id, media_identity_id, connection_id, root_id, dimension,
       status, evidence_json, coverage_id, coverage_max_age_seconds, observed_at,
       registered_at, imported_at
FROM tracking_observations_v5;
DROP TABLE tracking_observations_v5;

DROP TRIGGER IF EXISTS early_purge_plan_targets_immutable_delete;
DROP TRIGGER IF EXISTS early_purge_plan_targets_immutable_update;
DROP TRIGGER IF EXISTS early_purge_plan_targets_validate_insert;
DROP TRIGGER IF EXISTS trash_entries_manifest_bound_update;
DROP TABLE IF EXISTS early_purge_plan_targets;

DROP TRIGGER IF EXISTS janitor_records_approved_purge_claim;
DROP TRIGGER IF EXISTS janitor_records_approval_immutable_update;
DROP TRIGGER IF EXISTS janitor_records_approval_shape_update;
DROP TRIGGER IF EXISTS janitor_records_approval_shape_insert;
DROP TRIGGER IF EXISTS janitor_records_claim_trash_entry;
DROP TRIGGER IF EXISTS janitor_records_reconcile_trash_entry;
DROP TRIGGER IF EXISTS janitor_records_finish_trash_entry;

-- SQLite supports DROP COLUMN on the selected modernc SQLite runtime. The
-- v4 approval columns remain because 000004 introduced them; only the v5
-- action-run CAS column is removed here.
ALTER TABLE janitor_records DROP COLUMN approval_action_run_version;

-- The v4-compatible guard keeps approval-bearing rows out of the ordinary
-- janitor claim even after the v5 CAS column has been removed.
CREATE TRIGGER janitor_records_generic_claim_requires_exact_approval
BEFORE UPDATE OF state ON janitor_records
WHEN NEW.state = 'running'
 AND OLD.state <> 'running'
 AND (
       OLD.approval_plan_id IS NOT NULL
    OR OLD.approval_plan_revision IS NOT NULL
    OR OLD.approval_plan_digest IS NOT NULL
    OR OLD.approval_decision_id IS NOT NULL
    OR OLD.approval_action_run_id IS NOT NULL
    OR OLD.approved_entry_version IS NOT NULL
 )
BEGIN
    SELECT RAISE(ABORT, 'approval-bearing janitor requires an exact approved claim');
END;

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
     SELECT 1 FROM coverage_snapshots
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
     SELECT 1 FROM coverage_snapshots
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

-- Quarantine rows are durable evidence even while the active v4 table remains
-- nullable. Prevent an older writer from reusing their observation identity;
-- the v5 upgrade preflight compares any bypassed row and fails closed before
-- dropping either version of the evidence.
CREATE TRIGGER tracking_observations_quarantine_id_guard_insert
BEFORE INSERT ON tracking_observations
WHEN EXISTS (
    SELECT 1 FROM tracking_observation_quarantine
    WHERE original_observation_id = NEW.id
)
BEGIN
    SELECT RAISE(ABORT, 'tracking observation identity is reserved by quarantine');
END;

CREATE TRIGGER tracking_observations_quarantine_id_guard_update
BEFORE UPDATE OF id ON tracking_observations
WHEN NEW.id IS NOT OLD.id
 AND EXISTS (
    SELECT 1 FROM tracking_observation_quarantine
    WHERE original_observation_id = NEW.id
 )
BEGIN
    SELECT RAISE(ABORT, 'tracking observation identity is reserved by quarantine');
END;

CREATE TRIGGER janitor_records_claim_trash_entry
BEFORE UPDATE OF state ON janitor_records
WHEN NEW.state = 'running' AND OLD.state <> 'running'
BEGIN
    UPDATE trash_entries
    SET active_operation = NEW.operation,
        operation_claimed_by = NEW.claimed_by,
        operation_lease_until = NEW.lease_until,
        state = CASE NEW.operation WHEN 'purge' THEN 'purging' ELSE 'restoring' END,
        version = version + 1,
        updated_at = NEW.updated_at
    WHERE id = NEW.trash_entry_id
      AND (active_operation IS NULL OR active_operation = NEW.operation)
      AND (active_operation IS NULL OR operation_lease_until IS NULL OR julianday(operation_lease_until) <= julianday(NEW.updated_at))
      AND (
          (NEW.operation = 'purge' AND (
              (state = 'trashed' AND julianday(expires_at) IS NOT NULL AND julianday(NEW.updated_at) IS NOT NULL AND julianday(expires_at) <= julianday(NEW.updated_at))
              OR (active_operation = 'purge' AND state = 'purging')
          ))
          OR (NEW.operation = 'restore' AND ((state IN ('trashed', 'held', 'failed')) OR (active_operation = 'restore' AND state = 'restoring')))
      );
    SELECT CASE WHEN changes() = 0 THEN RAISE(ABORT, 'trash entry is already claimed or not eligible') END;
END;

CREATE TRIGGER janitor_records_reconcile_trash_entry
AFTER UPDATE OF state ON janitor_records
WHEN OLD.state = 'running' AND NEW.state = 'reconciling'
BEGIN
    UPDATE trash_entries
    SET operation_claimed_by = NULL, operation_lease_until = NULL,
        version = version + 1, updated_at = NEW.updated_at
    WHERE id = NEW.trash_entry_id AND active_operation = NEW.operation;
END;

CREATE TRIGGER janitor_records_finish_trash_entry
AFTER UPDATE OF state ON janitor_records
WHEN OLD.state IN ('running', 'reconciling')
 AND NEW.state IN ('succeeded', 'failed', 'held', 'cancelled')
BEGIN
    UPDATE trash_entries
    SET active_operation = NULL, operation_claimed_by = NULL,
        operation_lease_until = NULL,
        state = CASE
            WHEN NEW.state = 'succeeded' AND NEW.operation = 'purge' THEN 'purged'
            WHEN NEW.state = 'succeeded' AND NEW.operation = 'restore' THEN 'restored'
            WHEN NEW.state = 'held' THEN 'held'
            ELSE 'failed'
        END,
        version = version + 1, updated_at = NEW.updated_at
    WHERE id = NEW.trash_entry_id AND active_operation = NEW.operation;
END;

CREATE INDEX IF NOT EXISTS idx_tracking_dimension
    ON tracking_observations (dimension, observed_at);
CREATE INDEX IF NOT EXISTS idx_janitor_due
    ON janitor_records (state, next_attempt_at);

PRAGMA legacy_alter_table = OFF;
PRAGMA foreign_keys = ON;
CREATE TEMP TABLE d05_down_foreign_key_check (
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d05_down_foreign_key_check (ok)
SELECT CASE WHEN EXISTS (SELECT 1 FROM pragma_foreign_key_check) THEN 0 ELSE 1 END;
DROP TABLE d05_down_foreign_key_check;
