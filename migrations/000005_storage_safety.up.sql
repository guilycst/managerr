-- D-01 correction round four. Preserve the v4 schema history and tighten
-- compatibility at the v5 boundary. Legacy tracking rows whose scope cannot
-- be proved are kept in a durable quarantine and never exposed as active
-- observations. Early purge claims are bound to one immutable plan target,
-- approval and action-run dispatch claim.

PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

-- The downgrade keeps these append-only guards in place. Recreate them here
-- so a v5 -> v4 -> v5 cycle is safe and repeatable.
CREATE TABLE IF NOT EXISTS tracking_observation_quarantine (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    original_observation_id TEXT NOT NULL UNIQUE CHECK (length(trim(original_observation_id)) > 0),
    reason TEXT NOT NULL CHECK (reason IN (
        'external_record_missing',
        'external_connection_mismatch',
        'external_media_identity_mismatch',
        'connection_scope_missing',
        'media_identity_scope_missing',
        'root_scope_missing',
        'coverage_reference_missing',
        'coverage_scope_mismatch',
        'identity_scope_missing'
    )),
    original_external_record_id TEXT,
    original_media_identity_id TEXT,
    original_connection_id TEXT,
    original_root_id TEXT,
    dimension TEXT NOT NULL CHECK (dimension IN ('registration', 'import', 'availability', 'request')),
    original_status TEXT NOT NULL CHECK (original_status IN ('present', 'absent', 'unknown')),
    normalized_status TEXT NOT NULL DEFAULT 'unknown' CHECK (normalized_status = 'unknown'),
    original_evidence_json TEXT NOT NULL CHECK (json_valid(original_evidence_json)),
    coverage_id TEXT,
    coverage_max_age_seconds INTEGER,
    observed_at TEXT NOT NULL CHECK (length(trim(observed_at)) > 0),
    registered_at TEXT,
    imported_at TEXT,
    quarantined_at TEXT NOT NULL CHECK (length(trim(quarantined_at)) > 0),
    source_schema_version INTEGER NOT NULL CHECK (source_schema_version > 0)
);

DROP TRIGGER IF EXISTS tracking_observations_connection_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_connection_scope_insert;
DROP TRIGGER IF EXISTS tracking_observations_identity_scope_update;
DROP TRIGGER IF EXISTS tracking_observations_identity_scope_insert;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_update;
DROP TRIGGER IF EXISTS tracking_observations_absence_coverage_insert;

-- A rollback to the nullable v4 table must not allow an observation ID to be
-- reused with different evidence while its original row is quarantined. The
-- preflight runs before the legacy table is renamed or either row is dropped;
-- a conflict therefore leaves both pieces of evidence available in a dirty
-- migration for operator repair. Exact duplicates remain idempotent.
CREATE TEMP TABLE d05_tracking_quarantine_collision (
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d05_tracking_quarantine_collision (ok)
SELECT 0
FROM tracking_observations AS legacy
JOIN tracking_observation_quarantine AS quarantine
  ON quarantine.original_observation_id = legacy.id
WHERE NOT (
    legacy.external_record_id IS quarantine.original_external_record_id
    AND legacy.media_identity_id IS quarantine.original_media_identity_id
    AND legacy.connection_id IS quarantine.original_connection_id
    AND legacy.root_id IS quarantine.original_root_id
    AND legacy.dimension IS quarantine.dimension
    AND legacy.status IS quarantine.original_status
    AND legacy.evidence_json IS quarantine.original_evidence_json
    AND legacy.coverage_id IS quarantine.coverage_id
    AND legacy.coverage_max_age_seconds IS quarantine.coverage_max_age_seconds
    AND legacy.observed_at IS quarantine.observed_at
    AND legacy.registered_at IS quarantine.registered_at
    AND legacy.imported_at IS quarantine.imported_at
);
DROP TABLE d05_tracking_quarantine_collision;

-- Only remove the older table guards after the collision preflight succeeds;
-- a dirty migration must keep the evidence append-only and identity-reserved.
DROP TRIGGER IF EXISTS tracking_observation_quarantine_immutable_update;
DROP TRIGGER IF EXISTS tracking_observation_quarantine_immutable_delete;
DROP TRIGGER IF EXISTS tracking_observations_quarantine_id_guard_insert;
DROP TRIGGER IF EXISTS tracking_observations_quarantine_id_guard_update;

CREATE TRIGGER tracking_observation_quarantine_immutable_update
BEFORE UPDATE ON tracking_observation_quarantine
BEGIN
    SELECT RAISE(ABORT, 'tracking observation quarantine is immutable');
END;

CREATE TRIGGER tracking_observation_quarantine_immutable_delete
BEFORE DELETE ON tracking_observation_quarantine
BEGIN
    SELECT RAISE(ABORT, 'tracking observation quarantine is immutable');
END;

-- Materialize the old rows with the external-record identity beside the
-- observation identity. This makes the copy decision explicit and avoids
-- preserving a false manager/title association in the active table.
ALTER TABLE tracking_observations RENAME TO tracking_observations_legacy;
CREATE TEMP TABLE d05_tracking_legacy AS
SELECT scoped.*,
       CASE
           WHEN scoped.external_record_id IS NOT NULL AND scoped.matched_external_record_id IS NULL
               THEN 'external_record_missing'
           WHEN scoped.external_record_id IS NOT NULL
                AND scoped.connection_id IS NOT NULL
                AND scoped.connection_id IS NOT scoped.external_connection_id
               THEN 'external_connection_mismatch'
           WHEN scoped.external_record_id IS NOT NULL
                AND scoped.media_identity_id IS NOT NULL
                AND scoped.media_identity_id IS NOT scoped.external_media_identity_id
               THEN 'external_media_identity_mismatch'
           WHEN scoped.derived_connection_id IS NULL
               THEN 'connection_scope_missing'
           WHEN NOT EXISTS (
               SELECT 1 FROM connections AS c WHERE c.id = scoped.derived_connection_id
           ) THEN 'connection_scope_missing'
           WHEN scoped.media_identity_id IS NOT NULL
                AND NOT EXISTS (
                    SELECT 1 FROM media_identities AS mi WHERE mi.id = scoped.media_identity_id
                ) THEN 'media_identity_scope_missing'
           WHEN scoped.root_id IS NOT NULL
                AND NOT EXISTS (
                    SELECT 1 FROM storage_roots AS sr WHERE sr.id = scoped.root_id
                ) THEN 'root_scope_missing'
           WHEN scoped.status = 'absent'
                AND COALESCE(scoped.media_identity_id, scoped.external_media_identity_id) IS NULL
               THEN 'media_identity_scope_missing'
           WHEN scoped.status = 'absent' AND scoped.root_id IS NULL
               THEN 'root_scope_missing'
           WHEN scoped.status = 'absent'
                AND (scoped.coverage_id IS NULL OR scoped.coverage_max_age_seconds IS NULL
                     OR scoped.coverage_max_age_seconds <= 0)
               THEN 'coverage_reference_missing'
           WHEN scoped.coverage_id IS NOT NULL
                AND NOT EXISTS (
                    SELECT 1 FROM coverage_snapshots AS cs WHERE cs.id = scoped.coverage_id
                ) THEN 'coverage_reference_missing'
           WHEN scoped.status = 'absent'
                AND NOT EXISTS (
                    SELECT 1
                    FROM coverage_snapshots AS cs
                    WHERE cs.id = scoped.coverage_id
                      AND cs.connection_id = scoped.derived_connection_id
                      AND cs.media_identity_id = COALESCE(scoped.media_identity_id, scoped.external_media_identity_id)
                      AND cs.root_id = scoped.root_id
                      AND cs.completeness = 'complete'
                      AND cs.completed_at IS NOT NULL
                      AND cs.completed_at <= scoped.observed_at
                      AND cs.observed_at <= scoped.observed_at
                      AND julianday(cs.observed_at) IS NOT NULL
                      AND julianday(scoped.observed_at) IS NOT NULL
                      AND (julianday(scoped.observed_at) - julianday(cs.observed_at)) * 86400.0 <= scoped.coverage_max_age_seconds
                      AND (julianday(scoped.observed_at) - julianday(cs.observed_at)) * 86400.0 >= 0
                ) THEN 'coverage_scope_mismatch'
           WHEN scoped.external_record_id IS NULL
                AND scoped.media_identity_id IS NULL
                AND scoped.status <> 'unknown'
               THEN 'identity_scope_missing'
           ELSE NULL
       END AS quarantine_reason
FROM (
    SELECT legacy.*,
           external_records.id AS matched_external_record_id,
           external_records.connection_id AS external_connection_id,
           external_records.media_identity_id AS external_media_identity_id,
           COALESCE(legacy.connection_id, external_records.connection_id) AS derived_connection_id
    FROM tracking_observations_legacy AS legacy
    LEFT JOIN external_records
      ON external_records.id = legacy.external_record_id
) AS scoped;

CREATE TABLE tracking_observations (
    id TEXT PRIMARY KEY CHECK (length(trim(id)) > 0),
    external_record_id TEXT REFERENCES external_records(id),
    media_identity_id TEXT REFERENCES media_identities(id),
    connection_id TEXT NOT NULL REFERENCES connections(id)
        CHECK (length(trim(connection_id)) > 0),
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

INSERT INTO tracking_observation_quarantine (
    id, original_observation_id, reason, original_external_record_id,
    original_media_identity_id, original_connection_id, original_root_id,
    dimension, original_status, original_evidence_json, coverage_id,
    coverage_max_age_seconds, observed_at, registered_at, imported_at,
    quarantined_at, source_schema_version
)
SELECT 'tracking-quarantine:' || id, id, quarantine_reason,
       external_record_id, media_identity_id, connection_id, root_id,
       dimension, status, evidence_json, coverage_id,
       coverage_max_age_seconds, observed_at, registered_at, imported_at,
       strftime('%Y-%m-%dT%H:%M:%fZ', 'now'), 4
FROM d05_tracking_legacy
WHERE quarantine_reason IS NOT NULL
ON CONFLICT (original_observation_id) DO NOTHING;

INSERT INTO tracking_observations (
    id, external_record_id, media_identity_id, connection_id, root_id, dimension,
    status, evidence_json, coverage_id, coverage_max_age_seconds, observed_at,
    registered_at, imported_at
)
SELECT id, external_record_id,
       CASE
           WHEN external_record_id IS NOT NULL THEN COALESCE(media_identity_id, external_media_identity_id)
           ELSE media_identity_id
       END,
       derived_connection_id, root_id, dimension, status, evidence_json,
       coverage_id, coverage_max_age_seconds, observed_at, registered_at, imported_at
FROM d05_tracking_legacy
WHERE quarantine_reason IS NULL
  AND NOT EXISTS (
      SELECT 1
      FROM tracking_observation_quarantine AS quarantine
      WHERE quarantine.original_observation_id = d05_tracking_legacy.id
  );
DROP TABLE d05_tracking_legacy;
DROP TABLE tracking_observations_legacy;

CREATE TRIGGER tracking_observations_connection_required_insert
BEFORE INSERT ON tracking_observations
WHEN NEW.connection_id IS NULL OR length(trim(NEW.connection_id)) = 0
BEGIN
    SELECT RAISE(ABORT, 'tracking observation connection scope is required');
END;

CREATE TRIGGER tracking_observations_connection_required_update
BEFORE UPDATE ON tracking_observations
WHEN NEW.connection_id IS NULL OR length(trim(NEW.connection_id)) = 0
BEGIN
    SELECT RAISE(ABORT, 'tracking observation connection scope is required');
END;

CREATE TRIGGER tracking_observations_connection_scope_insert
BEFORE INSERT ON tracking_observations
WHEN NEW.external_record_id IS NOT NULL
 AND (
     NOT EXISTS (SELECT 1 FROM external_records WHERE id = NEW.external_record_id)
     OR NEW.connection_id IS NOT (SELECT connection_id FROM external_records WHERE id = NEW.external_record_id)
 )
BEGIN
    SELECT RAISE(ABORT, 'tracking observation connection does not match external record');
END;

CREATE TRIGGER tracking_observations_connection_scope_update
BEFORE UPDATE ON tracking_observations
WHEN NEW.external_record_id IS NOT NULL
 AND (
     NOT EXISTS (SELECT 1 FROM external_records WHERE id = NEW.external_record_id)
     OR NEW.connection_id IS NOT (SELECT connection_id FROM external_records WHERE id = NEW.external_record_id)
 )
BEGIN
    SELECT RAISE(ABORT, 'tracking observation connection does not match external record');
END;

CREATE TRIGGER tracking_observations_identity_scope_insert
BEFORE INSERT ON tracking_observations
WHEN NEW.external_record_id IS NOT NULL
 AND NEW.media_identity_id IS NOT (SELECT media_identity_id FROM external_records WHERE id = NEW.external_record_id)
BEGIN
    SELECT RAISE(ABORT, 'tracking observation media identity does not match external record');
END;

CREATE TRIGGER tracking_observations_identity_scope_update
BEFORE UPDATE ON tracking_observations
WHEN NEW.external_record_id IS NOT NULL
 AND NEW.media_identity_id IS NOT (SELECT media_identity_id FROM external_records WHERE id = NEW.external_record_id)
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

CREATE TRIGGER external_records_tracking_scope_update
BEFORE UPDATE OF connection_id, media_identity_id ON external_records
WHEN EXISTS (
    SELECT 1
    FROM tracking_observations AS observation
    WHERE observation.external_record_id = OLD.id
      AND (
          observation.connection_id IS NOT NEW.connection_id
          OR observation.media_identity_id IS NOT NEW.media_identity_id
      )
)
BEGIN
    SELECT RAISE(ABORT, 'external record identity is bound to tracking observations');
END;

CREATE INDEX IF NOT EXISTS idx_tracking_dimension
    ON tracking_observations (dimension, observed_at);
CREATE INDEX IF NOT EXISTS idx_tracking_quarantine_observed
    ON tracking_observation_quarantine (observed_at, id);

-- A v4 journal may contain one active operation and terminal history for the
-- opposite operation. Only two active operations or an active operation that
-- contradicts the entry state are ambiguous. Terminal history is retained.
DROP TRIGGER IF EXISTS janitor_records_approved_purge_claim;
DROP TRIGGER IF EXISTS janitor_records_approval_immutable_update;
DROP TRIGGER IF EXISTS janitor_records_approval_shape_update;
DROP TRIGGER IF EXISTS janitor_records_approval_shape_insert;
DROP TRIGGER IF EXISTS janitor_records_claim_trash_entry;
DROP TRIGGER IF EXISTS janitor_records_reconcile_trash_entry;
DROP TRIGGER IF EXISTS janitor_records_finish_trash_entry;

ALTER TABLE janitor_records ADD COLUMN approval_action_run_version INTEGER
    CHECK (approval_action_run_version IS NULL OR approval_action_run_version > 0);

CREATE TEMP TABLE d05_janitor_preflight (
    check_name TEXT PRIMARY KEY,
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d05_janitor_preflight (check_name, ok)
SELECT 'dual_active_operation', CASE WHEN EXISTS (
    SELECT 1
    FROM janitor_records AS first_record
    JOIN janitor_records AS second_record
      ON second_record.trash_entry_id = first_record.trash_entry_id
     AND second_record.id <> first_record.id
    WHERE first_record.state IN ('running', 'reconciling')
      AND second_record.state IN ('running', 'reconciling')
) THEN 0 ELSE 1 END;
INSERT INTO d05_janitor_preflight (check_name, ok)
SELECT 'active_operation_mismatch', CASE WHEN EXISTS (
    SELECT 1
    FROM trash_entries AS te
    JOIN janitor_records AS jr ON jr.trash_entry_id = te.id
    WHERE jr.state IN ('running', 'reconciling')
      AND (
          (te.active_operation IS NOT NULL AND te.active_operation <> jr.operation)
          OR (te.state = 'restoring' AND jr.operation = 'purge')
          OR (te.state = 'purging' AND jr.operation = 'restore')
          OR (jr.operation = 'purge' AND te.state <> 'purging')
          OR (jr.operation = 'restore' AND te.state <> 'restoring')
          OR (jr.operation = 'purge' AND te.state IN ('restored', 'purged', 'held'))
          OR (jr.operation = 'restore' AND te.state IN ('restored', 'purged'))
      )
) THEN 0 ELSE 1 END;
INSERT INTO d05_janitor_preflight (check_name, ok)
SELECT 'recovery_id_collision', CASE WHEN EXISTS (
    SELECT 1
    FROM trash_entries AS te
    WHERE te.state IN ('purging', 'restoring')
      AND NOT EXISTS (
          SELECT 1
          FROM janitor_records AS jr
          WHERE jr.trash_entry_id = te.id
            AND jr.state IN ('running', 'reconciling')
            AND jr.operation = CASE te.state WHEN 'purging' THEN 'purge' ELSE 'restore' END
      )
      AND EXISTS (
          SELECT 1
          FROM janitor_records AS collision
          WHERE collision.id = 'migration-recovery:' || te.id || ':' ||
              CASE te.state WHEN 'purging' THEN 'purge' ELSE 'restore' END
      )
) THEN 0 ELSE 1 END;
INSERT INTO d05_janitor_preflight (check_name, ok)
SELECT 'recovery_operation_conflict', CASE WHEN EXISTS (
    SELECT 1
    FROM trash_entries AS te
    WHERE te.state IN ('purging', 'restoring')
      AND NOT EXISTS (
          SELECT 1
          FROM janitor_records AS jr
          WHERE jr.trash_entry_id = te.id
            AND jr.state IN ('running', 'reconciling')
            AND jr.operation = CASE te.state WHEN 'purging' THEN 'purge' ELSE 'restore' END
      )
      AND EXISTS (
          SELECT 1
          FROM janitor_records AS history
          WHERE history.trash_entry_id = te.id
            AND history.operation = CASE te.state WHEN 'purging' THEN 'purge' ELSE 'restore' END
      )
) THEN 0 ELSE 1 END;
DROP TABLE d05_janitor_preflight;

-- Normalize the entry state before associating a legacy claim. An orphaned
-- in-flight entry receives a matching reconciling record below; a terminal
-- record for the opposite operation remains history.
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
          AND jr.operation = CASE trash_entries.state
              WHEN 'purging' THEN 'purge'
              WHEN 'restoring' THEN 'restore'
          END
    ),
    operation_claimed_by = (
        SELECT jr.claimed_by
        FROM janitor_records AS jr
        WHERE jr.trash_entry_id = trash_entries.id
          AND jr.state IN ('running', 'reconciling')
          AND jr.operation = CASE trash_entries.state
              WHEN 'purging' THEN 'purge'
              WHEN 'restoring' THEN 'restore'
          END
    ),
    operation_lease_until = (
        SELECT jr.lease_until
        FROM janitor_records AS jr
        WHERE jr.trash_entry_id = trash_entries.id
          AND jr.state IN ('running', 'reconciling')
          AND jr.operation = CASE trash_entries.state
              WHEN 'purging' THEN 'purge'
              WHEN 'restoring' THEN 'restore'
          END
    ),
    version = version + 1
WHERE state IN ('purging', 'restoring')
  AND EXISTS (
      SELECT 1
      FROM janitor_records AS jr
      WHERE jr.trash_entry_id = trash_entries.id
        AND jr.state IN ('running', 'reconciling')
        AND jr.operation = CASE trash_entries.state
            WHEN 'purging' THEN 'purge'
            WHEN 'restoring' THEN 'restore'
        END
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
      SELECT 1
      FROM janitor_records AS jr
      WHERE jr.trash_entry_id = te.id
        AND jr.operation = CASE te.state WHEN 'purging' THEN 'purge' ELSE 'restore' END
  );

-- A plan target is the immutable relational binding for an approved early
-- purge. Its manifest is copied from the exact trash entry at the target
-- version and must remain a nonempty array.
CREATE TABLE IF NOT EXISTS early_purge_plan_targets (
    plan_id TEXT NOT NULL,
    revision INTEGER NOT NULL CHECK (revision > 0),
    plan_digest TEXT NOT NULL CHECK (length(trim(plan_digest)) > 0),
    intent_kind TEXT NOT NULL CHECK (intent_kind = 'fs.delete'),
    trash_entry_id TEXT NOT NULL REFERENCES trash_entries(id),
    trash_entry_version INTEGER NOT NULL CHECK (trash_entry_version > 0),
    manifest_json TEXT NOT NULL CHECK (
        CASE WHEN json_valid(manifest_json)
             THEN json_type(manifest_json) = 'array' AND json_array_length(manifest_json) > 0
             ELSE 0 END
    ),
    created_at TEXT NOT NULL CHECK (length(trim(created_at)) > 0),
    PRIMARY KEY (plan_id, revision),
    FOREIGN KEY (plan_id, revision, plan_digest)
        REFERENCES action_plan_revisions(plan_id, revision, digest)
);

CREATE TRIGGER early_purge_plan_targets_validate_insert
BEFORE INSERT ON early_purge_plan_targets
WHEN NOT EXISTS (
    SELECT 1
    FROM action_plans AS ap
    JOIN action_plan_revisions AS apr
      ON apr.plan_id = ap.id AND apr.revision = NEW.revision
    JOIN trash_entries AS te ON te.id = NEW.trash_entry_id
    WHERE ap.id = NEW.plan_id
      AND ap.kind = 'fs.delete'
      AND apr.digest = NEW.plan_digest
      AND json(NEW.manifest_json) = json(apr.manifest_json)
      AND te.state = 'trashed'
      AND te.active_operation IS NULL
      AND te.version = NEW.trash_entry_version
      AND json(NEW.manifest_json) = json(te.manifest_json)
)
BEGIN
    SELECT RAISE(ABORT, 'early purge target must bind an exact fs.delete manifest and trash version');
END;

CREATE TRIGGER early_purge_plan_targets_immutable_update
BEFORE UPDATE ON early_purge_plan_targets
BEGIN
    SELECT RAISE(ABORT, 'early purge plan target is immutable');
END;

CREATE TRIGGER early_purge_plan_targets_immutable_delete
BEFORE DELETE ON early_purge_plan_targets
BEGIN
    SELECT RAISE(ABORT, 'early purge plan target is immutable');
END;

CREATE TRIGGER trash_entries_manifest_bound_update
BEFORE UPDATE OF manifest_json ON trash_entries
WHEN EXISTS (SELECT 1 FROM early_purge_plan_targets WHERE trash_entry_id = OLD.id)
 AND NEW.manifest_json IS NOT OLD.manifest_json
BEGIN
    SELECT RAISE(ABORT, 'trash manifest is bound to an immutable early purge target');
END;

CREATE TRIGGER janitor_records_approval_shape_insert
BEFORE INSERT ON janitor_records
WHEN (
    NEW.approval_plan_id IS NOT NULL OR NEW.approval_plan_revision IS NOT NULL
    OR NEW.approval_plan_digest IS NOT NULL OR NEW.approval_decision_id IS NOT NULL
    OR NEW.approval_action_run_id IS NOT NULL OR NEW.approved_entry_version IS NOT NULL
    OR NEW.approval_action_run_version IS NOT NULL
)
BEGIN
    SELECT RAISE(ABORT, 'early purge approval must be attached by an atomic claim');
END;

CREATE TRIGGER janitor_records_approval_shape_update
BEFORE UPDATE OF approval_plan_id, approval_plan_revision,
    approval_plan_digest, approval_decision_id, approval_action_run_id,
    approved_entry_version, approval_action_run_version ON janitor_records
WHEN (
    (NEW.approval_plan_id IS NULL AND (
        NEW.approval_plan_revision IS NOT NULL OR NEW.approval_plan_digest IS NOT NULL
        OR NEW.approval_decision_id IS NOT NULL OR NEW.approval_action_run_id IS NOT NULL
        OR NEW.approved_entry_version IS NOT NULL OR NEW.approval_action_run_version IS NOT NULL
    ))
    OR (NEW.approval_plan_id IS NOT NULL AND (
        NEW.approval_plan_revision IS NULL OR NEW.approval_plan_digest IS NULL
        OR NEW.approval_decision_id IS NULL OR NEW.approval_action_run_id IS NULL
        OR NEW.approved_entry_version IS NULL OR NEW.approval_action_run_version IS NULL
        OR NEW.operation <> 'purge' OR NEW.state <> 'running'
    ))
    OR (NEW.approval_plan_id IS NOT NULL AND OLD.approval_plan_id IS NULL
        AND (OLD.state = 'running' OR NEW.state <> 'running' OR NEW.operation <> 'purge'))
)
BEGIN
    SELECT RAISE(ABORT, 'janitor approval binding is incomplete');
END;

CREATE TRIGGER janitor_records_approval_immutable_update
BEFORE UPDATE OF approval_plan_id, approval_plan_revision,
    approval_plan_digest, approval_decision_id, approval_action_run_id,
    approved_entry_version, approval_action_run_version ON janitor_records
WHEN OLD.approval_plan_id IS NOT NULL AND (
    NEW.approval_plan_id IS NOT OLD.approval_plan_id
    OR NEW.approval_plan_revision IS NOT OLD.approval_plan_revision
    OR NEW.approval_plan_digest IS NOT OLD.approval_plan_digest
    OR NEW.approval_decision_id IS NOT OLD.approval_decision_id
    OR NEW.approval_action_run_id IS NOT OLD.approval_action_run_id
    OR NEW.approved_entry_version IS NOT OLD.approved_entry_version
    OR NEW.approval_action_run_version IS NOT OLD.approval_action_run_version
)
BEGIN
    SELECT RAISE(ABORT, 'janitor approval binding is immutable');
END;

-- This trigger claims the matching action run and validates every binding in
-- the same SQLite write statement that claims the janitor record. If either
-- CAS fails, the surrounding statement rolls back both claims.
CREATE TRIGGER janitor_records_approved_purge_claim
BEFORE UPDATE OF state, approval_plan_id, approval_plan_revision,
    approval_plan_digest, approval_decision_id, approval_action_run_id,
    approved_entry_version, approval_action_run_version ON janitor_records
WHEN OLD.approval_plan_id IS NULL
 AND OLD.state <> 'running'
 AND NEW.state = 'running'
 AND NEW.operation = 'purge'
 AND NEW.approval_plan_id IS NOT NULL
BEGIN
    UPDATE action_runs
    SET state = 'running', claimed_by = NEW.claimed_by,
        lease_until = NEW.lease_until, version = version + 1,
        updated_at = NEW.updated_at
    WHERE id = NEW.approval_action_run_id
      AND version = NEW.approval_action_run_version
      AND state = 'queued'
      AND cancellation_requested_at IS NULL
      AND (deadline_at IS NULL OR julianday(deadline_at) > julianday(NEW.updated_at))
      AND (next_attempt_at IS NULL OR julianday(next_attempt_at) <= julianday(NEW.updated_at))
      AND (claimed_by IS NULL OR lease_until IS NULL OR julianday(lease_until) <= julianday(NEW.updated_at))
      AND NEW.claimed_by IS NOT NULL
      AND length(trim(NEW.claimed_by)) > 0
      AND NEW.lease_until IS NOT NULL
      AND julianday(NEW.lease_until) > julianday(NEW.updated_at);
    SELECT CASE WHEN changes() = 0 THEN RAISE(ABORT, 'early purge action run is not claimable') END;

    SELECT CASE WHEN NOT EXISTS (
        SELECT 1
        FROM action_plan_revisions AS apr
        JOIN action_plans AS ap
          ON ap.id = apr.plan_id
        JOIN early_purge_plan_targets AS target
          ON target.plan_id = apr.plan_id
         AND target.revision = apr.revision
         AND target.plan_digest = apr.digest
        JOIN trash_entries AS te
          ON te.id = target.trash_entry_id
        JOIN action_runs AS ar
          ON ar.id = NEW.approval_action_run_id
        JOIN review_decisions AS rd
          ON rd.id = NEW.approval_decision_id
        WHERE apr.plan_id = NEW.approval_plan_id
          AND apr.revision = NEW.approval_plan_revision
          AND apr.digest = NEW.approval_plan_digest
          AND apr.state = 'ready'
          AND ap.state = 'ready'
          AND ap.kind = 'fs.delete'
          AND apr.expires_at IS NOT NULL
          AND julianday(apr.expires_at) > julianday(NEW.updated_at)
          AND rd.plan_id = apr.plan_id
          AND rd.plan_revision = apr.revision
          AND rd.plan_digest = apr.digest
          AND rd.decision = 'approve'
          AND target.intent_kind = 'fs.delete'
          AND target.trash_entry_id = NEW.trash_entry_id
          AND target.trash_entry_version = NEW.approved_entry_version
          AND target.manifest_json IS NOT NULL
          AND json_type(target.manifest_json) = 'array'
          AND json_array_length(target.manifest_json) > 0
          AND json(target.manifest_json) = json(apr.manifest_json)
          AND json(target.manifest_json) = json(te.manifest_json)
          AND te.id = NEW.trash_entry_id
          AND (
              (te.state = 'trashed' AND te.active_operation IS NULL
               AND te.version = NEW.approved_entry_version)
              OR (te.state = 'purging' AND te.active_operation = 'purge'
                  AND te.version = NEW.approved_entry_version + 1
                  AND te.operation_claimed_by IS NEW.claimed_by)
          )
          AND ar.plan_id = apr.plan_id
          AND ar.plan_revision = apr.revision
          AND ar.plan_digest = apr.digest
          AND ar.state = 'running'
          AND ar.version = NEW.approval_action_run_version + 1
          AND ar.claimed_by IS NEW.claimed_by
          AND ar.lease_until IS NEW.lease_until
          AND ar.cancellation_requested_at IS NULL
          AND (ar.deadline_at IS NULL OR julianday(ar.deadline_at) > julianday(NEW.updated_at))
    ) THEN RAISE(ABORT, 'early purge requires exact approved scope and dispatch claim') END;
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
          (NEW.operation = 'purge' AND (
              (state = 'trashed' AND julianday(expires_at) IS NOT NULL AND julianday(NEW.updated_at) IS NOT NULL AND julianday(expires_at) <= julianday(NEW.updated_at))
              OR (active_operation = 'purge' AND state = 'purging')
              OR (OLD.approval_plan_id IS NULL AND NEW.approval_plan_id IS NOT NULL
                  AND state = 'trashed' AND active_operation IS NULL
                  AND version = NEW.approved_entry_version)
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

CREATE INDEX IF NOT EXISTS idx_janitor_due
    ON janitor_records (state, next_attempt_at);

PRAGMA legacy_alter_table = OFF;
PRAGMA foreign_keys = ON;
CREATE TEMP TABLE d05_foreign_key_check (
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d05_foreign_key_check (ok)
SELECT CASE WHEN EXISTS (SELECT 1 FROM pragma_foreign_key_check) THEN 0 ELSE 1 END;
DROP TABLE d05_foreign_key_check;
