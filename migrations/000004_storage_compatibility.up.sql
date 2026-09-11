-- D-01 correction round three. This migration keeps populated legacy journals
-- conservative, completes in-flight trash claims, and adds the storage
-- primitive for an explicitly approved early purge.
--
-- 000003 is retained for fresh v2 upgrades with its compatibility copy policy;
-- this ordered migration is also required for databases already at v3.

PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

-- A v3 database already has composite foreign keys, but a database that was
-- upgraded by an earlier 000003 can have been copied while checks were off.
-- Preflight the legacy tuples before this migration rebuilds any table.
CREATE TEMP TABLE d04_scope_preflight (
    check_name TEXT PRIMARY KEY,
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d04_scope_preflight (check_name, ok)
SELECT 'file_observations', CASE WHEN EXISTS (
    SELECT 1
    FROM file_observations AS fo
    WHERE NOT EXISTS (
        SELECT 1
        FROM discoveries AS d
        WHERE d.id = fo.discovery_id
          AND d.root_id = fo.root_id
          AND d.relative_path = fo.relative_path
          AND d.manifest_revision = fo.manifest_revision
    )
) THEN 0 ELSE 1 END;
INSERT INTO d04_scope_preflight (check_name, ok)
SELECT 'action_effects', CASE WHEN EXISTS (
    SELECT 1
    FROM action_effects AS ae
    WHERE ae.attempt_id IS NOT NULL
      AND NOT EXISTS (
          SELECT 1
          FROM action_attempts AS aa
          WHERE aa.id = ae.attempt_id
            AND aa.action_run_id = ae.action_run_id
      )
) THEN 0 ELSE 1 END;
INSERT INTO d04_scope_preflight (check_name, ok)
SELECT 'trash_items', CASE WHEN EXISTS (
    SELECT 1
    FROM trash_items AS ti
    WHERE NOT EXISTS (
        SELECT 1
        FROM trash_entries AS te
        WHERE te.id = ti.entry_id
          AND te.root_id = ti.root_id
    )
) THEN 0 ELSE 1 END;
DROP TABLE d04_scope_preflight;

ALTER TABLE janitor_records ADD COLUMN approval_plan_id TEXT REFERENCES action_plans(id);
ALTER TABLE janitor_records ADD COLUMN approval_plan_revision INTEGER CHECK (approval_plan_revision IS NULL OR approval_plan_revision > 0);
ALTER TABLE janitor_records ADD COLUMN approval_plan_digest TEXT;
ALTER TABLE janitor_records ADD COLUMN approval_decision_id TEXT REFERENCES review_decisions(id);
ALTER TABLE janitor_records ADD COLUMN approval_action_run_id TEXT REFERENCES action_runs(id);
ALTER TABLE janitor_records ADD COLUMN approved_entry_version INTEGER CHECK (approved_entry_version IS NULL OR approved_entry_version > 0);

DROP TRIGGER IF EXISTS tracking_observations_connection_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_connection_scope_insert;
DROP TRIGGER IF EXISTS tracking_observations_identity_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_identity_scope_insert;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_update;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_insert;

-- All current rows are copied into one shape that can retain an explicit
-- unknown observation without inventing a connection. The connection check
-- below still prevents present/absent observations from being unscoped.
ALTER TABLE tracking_observations RENAME TO tracking_observations_legacy;
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
FROM tracking_observations_legacy;
DROP TABLE tracking_observations_legacy;

-- Reconcile legacy active janitor/trash state as one claim. An opposite active
-- operation or a terminal state is ambiguous and aborts this migration before
-- any recovery record is synthesized.
CREATE TEMP TABLE d04_janitor_preflight (
    check_name TEXT PRIMARY KEY,
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d04_janitor_preflight (check_name, ok)
SELECT 'dual_active_operation', CASE WHEN EXISTS (
    SELECT 1
    FROM janitor_records AS first_record
    JOIN janitor_records AS second_record
      ON second_record.trash_entry_id = first_record.trash_entry_id
     AND second_record.id <> first_record.id
    WHERE first_record.state IN ('running', 'reconciling')
      AND second_record.state IN ('running', 'reconciling')
      AND first_record.operation <> second_record.operation
) THEN 0 ELSE 1 END;
INSERT INTO d04_janitor_preflight (check_name, ok)
SELECT 'entry_operation_conflict', CASE WHEN EXISTS (
    SELECT 1
    FROM trash_entries AS te
    JOIN janitor_records AS jr ON jr.trash_entry_id = te.id
    WHERE jr.state IN ('running', 'reconciling')
      AND te.active_operation IS NOT NULL
      AND te.active_operation <> jr.operation
) THEN 0 ELSE 1 END;
INSERT INTO d04_janitor_preflight (check_name, ok)
SELECT 'active_state_operation_conflict', CASE WHEN EXISTS (
    SELECT 1
    FROM trash_entries AS te
    JOIN janitor_records AS jr ON jr.trash_entry_id = te.id
    WHERE jr.state IN ('running', 'reconciling')
      AND ((te.state = 'restoring' AND jr.operation = 'purge')
        OR (te.state = 'purging' AND jr.operation = 'restore'))
) THEN 0 ELSE 1 END;
INSERT INTO d04_janitor_preflight (check_name, ok)
SELECT 'active_terminal_conflict', CASE WHEN EXISTS (
    SELECT 1
    FROM janitor_records AS jr
    JOIN trash_entries AS te ON te.id = jr.trash_entry_id
    WHERE jr.state IN ('running', 'reconciling')
      AND ((jr.operation = 'purge' AND te.state IN ('restored', 'purged', 'held'))
        OR (jr.operation = 'restore' AND te.state IN ('restored', 'purged')))
) THEN 0 ELSE 1 END;
INSERT INTO d04_janitor_preflight (check_name, ok)
SELECT 'active_trash_row_conflict', CASE WHEN EXISTS (
    SELECT 1
    FROM trash_entries AS te
    JOIN janitor_records AS jr ON jr.trash_entry_id = te.id
    WHERE te.state IN ('restoring', 'purging')
      AND jr.state NOT IN ('running', 'reconciling')
) THEN 0 ELSE 1 END;
INSERT INTO d04_janitor_preflight (check_name, ok)
SELECT 'recovery_id_collision', CASE WHEN EXISTS (
    SELECT 1
    FROM trash_entries AS te
    JOIN janitor_records AS jr ON jr.id = 'migration-recovery:' || te.id || ':' ||
        CASE te.state WHEN 'purging' THEN 'purge' ELSE 'restore' END
    WHERE te.state IN ('restoring', 'purging')
      AND NOT EXISTS (
          SELECT 1 FROM janitor_records AS active_record
          WHERE active_record.trash_entry_id = te.id
      )
) THEN 0 ELSE 1 END;
DROP TABLE d04_janitor_preflight;

UPDATE trash_entries
SET active_operation = CASE state
        WHEN 'purging' THEN 'purge'
        WHEN 'restoring' THEN 'restore'
    END,
    version = version + 1
WHERE state IN ('purging', 'restoring')
  AND active_operation IS NULL;

UPDATE trash_entries
SET active_operation = (
        SELECT jr.operation
        FROM janitor_records AS jr
        WHERE jr.trash_entry_id = trash_entries.id
          AND jr.state IN ('running', 'reconciling')
    ),
    operation_claimed_by = (
        SELECT jr.claimed_by
        FROM janitor_records AS jr
        WHERE jr.trash_entry_id = trash_entries.id
          AND jr.state IN ('running', 'reconciling')
    ),
    operation_lease_until = (
        SELECT jr.lease_until
        FROM janitor_records AS jr
        WHERE jr.trash_entry_id = trash_entries.id
          AND jr.state IN ('running', 'reconciling')
    ),
    state = CASE (
        SELECT jr.operation
        FROM janitor_records AS jr
        WHERE jr.trash_entry_id = trash_entries.id
          AND jr.state IN ('running', 'reconciling')
    ) WHEN 'purge' THEN 'purging' ELSE 'restoring' END,
    version = version + 1
WHERE EXISTS (
    SELECT 1
    FROM janitor_records AS jr
    WHERE jr.trash_entry_id = trash_entries.id
      AND jr.state IN ('running', 'reconciling')
      AND (trash_entries.active_operation IS NULL OR trash_entries.active_operation = jr.operation)
);

UPDATE janitor_records
SET version = version + 1
WHERE state IN ('running', 'reconciling');

INSERT INTO janitor_records (
    id, trash_entry_id, operation, state, next_attempt_at, claimed_by,
    lease_until, outcome_json, created_at, updated_at
)
SELECT 'migration-recovery:' || te.id || ':' ||
       CASE te.state WHEN 'purging' THEN 'purge' ELSE 'restore' END,
       te.id,
       CASE te.state WHEN 'purging' THEN 'purge' ELSE 'restore' END,
       'reconciling', NULL, NULL, NULL,
       '{"reason":"legacy_trash_state_recovery"}', te.updated_at, te.updated_at
FROM trash_entries AS te
WHERE te.state IN ('purging', 'restoring')
  AND NOT EXISTS (
      SELECT 1 FROM janitor_records AS jr
      WHERE jr.trash_entry_id = te.id
  );

CREATE TRIGGER janitor_records_approval_shape_insert
BEFORE INSERT ON janitor_records
WHEN (
    NEW.approval_plan_id IS NOT NULL OR NEW.approval_plan_revision IS NOT NULL
    OR NEW.approval_plan_digest IS NOT NULL OR NEW.approval_decision_id IS NOT NULL
    OR NEW.approval_action_run_id IS NOT NULL OR NEW.approved_entry_version IS NOT NULL
)
BEGIN
    SELECT RAISE(ABORT, 'early purge approval must be attached by an atomic claim');
END;

CREATE TRIGGER janitor_records_approval_shape_update
BEFORE UPDATE OF approval_plan_id, approval_plan_revision, approval_plan_digest,
    approval_decision_id, approval_action_run_id, approved_entry_version ON janitor_records
WHEN (
    (NEW.approval_plan_id IS NULL AND (
        NEW.approval_plan_revision IS NOT NULL OR NEW.approval_plan_digest IS NOT NULL
        OR NEW.approval_decision_id IS NOT NULL OR NEW.approval_action_run_id IS NOT NULL
        OR NEW.approved_entry_version IS NOT NULL
    ))
    OR (NEW.approval_plan_id IS NOT NULL AND (
        NEW.approval_plan_revision IS NULL OR NEW.approval_plan_digest IS NULL
        OR NEW.approval_decision_id IS NULL OR NEW.approval_action_run_id IS NULL
        OR NEW.approved_entry_version IS NULL OR NEW.operation <> 'purge'
        OR NEW.state <> 'running'
    ))
    OR (NEW.approval_plan_id IS NOT NULL AND OLD.approval_plan_id IS NULL
        AND (OLD.state = 'running' OR NEW.state <> 'running' OR NEW.operation <> 'purge'))
)
BEGIN
    SELECT RAISE(ABORT, 'janitor approval binding is incomplete');
END;

CREATE TRIGGER janitor_records_approval_immutable_update
BEFORE UPDATE OF approval_plan_id, approval_plan_revision, approval_plan_digest,
    approval_decision_id, approval_action_run_id, approved_entry_version ON janitor_records
WHEN OLD.approval_plan_id IS NOT NULL AND (
    NEW.approval_plan_id IS NOT OLD.approval_plan_id
    OR NEW.approval_plan_revision IS NOT OLD.approval_plan_revision
    OR NEW.approval_plan_digest IS NOT OLD.approval_plan_digest
    OR NEW.approval_decision_id IS NOT OLD.approval_decision_id
    OR NEW.approval_action_run_id IS NOT OLD.approval_action_run_id
    OR NEW.approved_entry_version IS NOT OLD.approved_entry_version
)
BEGIN
    SELECT RAISE(ABORT, 'janitor approval binding is immutable');
END;

CREATE TRIGGER janitor_records_approved_purge_claim
BEFORE UPDATE OF state, approval_plan_id, approval_plan_revision,
    approval_plan_digest, approval_decision_id, approval_action_run_id,
    approved_entry_version ON janitor_records
WHEN OLD.approval_plan_id IS NULL
 AND OLD.state <> 'running'
 AND NEW.state = 'running'
 AND NEW.operation = 'purge'
 AND NEW.approval_plan_id IS NOT NULL
 AND NOT EXISTS (
     SELECT 1
     FROM action_plan_revisions AS apr
     JOIN action_plans AS ap
       ON ap.id = apr.plan_id
    WHERE apr.plan_id = NEW.approval_plan_id
      AND apr.revision = NEW.approval_plan_revision
      AND apr.digest = NEW.approval_plan_digest
      AND apr.state = 'ready'
      AND ap.state = 'ready'
      AND EXISTS (
          SELECT 1
          FROM review_decisions AS rd
          WHERE rd.id = NEW.approval_decision_id
            AND rd.plan_id = apr.plan_id
            AND rd.plan_revision = apr.revision
            AND rd.plan_digest = apr.digest
            AND rd.decision = 'approve'
      )
      AND EXISTS (
          SELECT 1
          FROM action_runs AS ar
          WHERE ar.id = NEW.approval_action_run_id
            AND ar.plan_id = apr.plan_id
            AND ar.plan_revision = apr.revision
            AND ar.plan_digest = apr.digest
            AND ar.state NOT IN ('succeeded', 'failed', 'cancelled', 'deadline_exceeded')
      )
      AND EXISTS (
          SELECT 1
          FROM trash_entries AS te
          WHERE te.id = NEW.trash_entry_id
            AND (
                (te.state = 'trashed' AND te.active_operation IS NULL AND te.version = NEW.approved_entry_version)
                -- The ordinary claim trigger may run first. Its same-row
                -- update increments the entry version exactly once; retain
                -- that relation so approval still binds to the pre-claim
                -- version and cannot be replayed against a later entry.
                OR (te.active_operation = 'purge' AND te.state = 'purging'
                    AND te.version = NEW.approved_entry_version + 1
                    AND te.operation_claimed_by IS NEW.claimed_by)
            )
      )
 )
BEGIN
    SELECT RAISE(ABORT, 'early purge requires an exact approved plan and entry version');
END;

DROP TRIGGER IF EXISTS tracking_observations_connection_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_connection_scope_insert;
DROP TRIGGER IF EXISTS tracking_observations_identity_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_identity_scope_insert;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_update;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_insert;

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

-- Recreate the v3 claim triggers after the janitor additions so automatic
-- purge remains expiry-gated and restore/purge stay mutually exclusive.
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
          (NEW.operation = 'purge' AND (
              (state = 'trashed' AND julianday(expires_at) IS NOT NULL AND julianday(NEW.updated_at) IS NOT NULL AND julianday(expires_at) <= julianday(NEW.updated_at))
              OR (active_operation = 'purge' AND state = 'purging')
              OR (NEW.approval_plan_id IS NOT NULL AND state = 'trashed'
                  AND active_operation IS NULL AND version = NEW.approved_entry_version)
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
CREATE TEMP TABLE d04_foreign_key_check (
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d04_foreign_key_check (ok)
SELECT CASE WHEN EXISTS (SELECT 1 FROM pragma_foreign_key_check) THEN 0 ELSE 1 END;
DROP TABLE d04_foreign_key_check;
