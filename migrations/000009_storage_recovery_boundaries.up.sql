-- D-01 correction round eight. Preserve in-flight approved purge work across
-- the v7-to-v9 upgrade, keep read-only reconciliation available after a
-- cancellation/deadline boundary, and recover the coupled action and janitor
-- lease as one SQLite write boundary.
-- Historical migrations remain immutable.

PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

-- A v7 reconciler could leave the janitor running at version N, the trash
-- entry purging at version N, and the action reconciling/unleased at version
-- N-1. v8's trigger is intentionally strict, so repair only the exact
-- populated shape whose immutable plan, decision, target and manifest prove
-- that the janitor still owns the same approved purge. A v7 initial dispatch
-- (action running at N) and a fully recovered generation (all three at N) are
-- already safe and are retained as-is.
CREATE TEMP TABLE d09_safe_approved_recovery (
    janitor_id TEXT PRIMARY KEY,
    action_id TEXT NOT NULL UNIQUE,
    entry_id TEXT NOT NULL,
    janitor_version INTEGER NOT NULL,
    action_version INTEGER NOT NULL,
    worker_id TEXT NOT NULL,
    lease_until TEXT NOT NULL,
    repair_action INTEGER NOT NULL CHECK (repair_action IN (0, 1, 2, 3))
);

INSERT INTO d09_safe_approved_recovery (
    janitor_id, action_id, entry_id, janitor_version, action_version,
    worker_id, lease_until, repair_action
)
SELECT jr.id, action.id, entry.id, jr.version, action.version,
       jr.claimed_by, jr.lease_until,
       CASE
           WHEN action.state = 'reconciling'
           AND action.claimed_by IS NULL
           AND action.lease_until IS NULL
           AND action.version = jr.version - 1
           THEN 1
           WHEN action.state = 'reconciling'
            AND action.claimed_by IS NULL
            AND action.lease_until IS NULL
            AND action.version = jr.version + 1
           THEN 2
           ELSE 0
       END
FROM janitor_records AS jr
JOIN action_runs AS action
  ON action.id = jr.approval_action_run_id
JOIN trash_entries AS entry
  ON entry.id = jr.trash_entry_id
JOIN action_plan_revisions AS revision
  ON revision.plan_id = jr.approval_plan_id
 AND revision.revision = jr.approval_plan_revision
 AND revision.digest = jr.approval_plan_digest
JOIN action_plans AS plan
  ON plan.id = revision.plan_id
JOIN early_purge_plan_targets AS target
  ON target.plan_id = revision.plan_id
 AND target.revision = revision.revision
 AND target.plan_digest = revision.digest
JOIN review_decisions AS decision
  ON decision.id = jr.approval_decision_id
WHERE jr.operation = 'purge'
  AND jr.state IN ('running', 'reconciling')
  AND jr.approval_plan_id IS NOT NULL
  AND jr.approval_plan_revision IS NOT NULL
  AND jr.approval_plan_digest IS NOT NULL
  AND jr.approval_decision_id IS NOT NULL
  AND jr.approval_action_run_id IS NOT NULL
  AND jr.approved_entry_version IS NOT NULL
  AND jr.approval_action_run_version IS NOT NULL
  AND jr.claimed_by IS NOT NULL
  AND length(trim(jr.claimed_by)) > 0
  AND jr.lease_until IS NOT NULL
  AND julianday(jr.lease_until) IS NOT NULL
  AND plan.state = 'ready'
  AND plan.kind = 'fs.delete'
  AND revision.state = 'ready'
  AND decision.plan_id = revision.plan_id
  AND decision.plan_revision = revision.revision
  AND decision.plan_digest = revision.digest
  AND decision.decision = 'approve'
  AND target.intent_kind = 'fs.delete'
  AND target.trash_entry_id = jr.trash_entry_id
  AND target.trash_entry_version = jr.approved_entry_version
  AND target.manifest_json IS NOT NULL
  AND json_valid(target.manifest_json)
  AND json_type(target.manifest_json) = 'array'
  AND json_array_length(target.manifest_json) > 0
  AND json_valid(revision.manifest_json)
  AND json(target.manifest_json) = json(revision.manifest_json)
  AND json_valid(entry.manifest_json)
  AND json(target.manifest_json) = json(entry.manifest_json)
  AND julianday(target.created_at) IS NOT NULL
  AND julianday(decision.created_at) IS NOT NULL
  AND julianday(target.created_at) <= julianday(decision.created_at)
  AND entry.state = 'purging'
  AND entry.active_operation = 'purge'
  AND entry.version = jr.version
  AND entry.operation_claimed_by IS jr.claimed_by
  AND entry.operation_lease_until IS jr.lease_until
  AND (
      (
          action.state = 'running'
          AND action.version = jr.version
          AND action.claimed_by IS jr.claimed_by
          AND action.lease_until IS jr.lease_until
      )
      OR (
          action.state = 'reconciling'
          AND action.claimed_by IS NULL
          AND action.lease_until IS NULL
          AND action.version = jr.version - 1
      )
      OR (
          action.state = 'reconciling'
          AND action.version = jr.version
          AND action.claimed_by IS jr.claimed_by
          AND action.lease_until IS jr.lease_until
      )
      OR (
          action.state = 'reconciling'
          AND action.claimed_by IS NULL
          AND action.lease_until IS NULL
          AND action.version = jr.version + 1
      )
  );

-- The inverse supported split can occur when janitor recovery commits first:
-- janitor/trash are unleased at generation N while the action still carries
-- its old running lease at generation N-1. Clear that stale action lease and
-- retain the exact read-only generation before v9 claims can resume it.
INSERT INTO d09_safe_approved_recovery (
    janitor_id, action_id, entry_id, janitor_version, action_version,
    worker_id, lease_until, repair_action
)
SELECT jr.id, action.id, entry.id, jr.version, action.version,
       '', '', 3
FROM janitor_records AS jr
JOIN action_runs AS action
  ON action.id = jr.approval_action_run_id
JOIN trash_entries AS entry
  ON entry.id = jr.trash_entry_id
JOIN action_plan_revisions AS revision
  ON revision.plan_id = jr.approval_plan_id
 AND revision.revision = jr.approval_plan_revision
 AND revision.digest = jr.approval_plan_digest
JOIN action_plans AS plan
  ON plan.id = revision.plan_id
JOIN early_purge_plan_targets AS target
  ON target.plan_id = revision.plan_id
 AND target.revision = revision.revision
 AND target.plan_digest = revision.digest
JOIN review_decisions AS decision
  ON decision.id = jr.approval_decision_id
WHERE jr.operation = 'purge'
  AND jr.state = 'reconciling'
  AND jr.approval_plan_id IS NOT NULL
  AND jr.claimed_by IS NULL
  AND jr.lease_until IS NULL
  AND action.state = 'running'
  AND action.claimed_by IS NOT NULL
  AND length(trim(action.claimed_by)) > 0
  AND action.lease_until IS NOT NULL
  AND julianday(action.lease_until) IS NOT NULL
  AND action.version = jr.version - 1
  AND entry.state = 'purging'
  AND entry.active_operation = 'purge'
  AND entry.version = jr.version
  AND entry.operation_claimed_by IS NULL
  AND entry.operation_lease_until IS NULL
  AND plan.state = 'ready'
  AND plan.kind = 'fs.delete'
  AND revision.state = 'ready'
  AND decision.plan_id = revision.plan_id
  AND decision.plan_revision = revision.revision
  AND decision.plan_digest = revision.digest
  AND decision.decision = 'approve'
  AND target.intent_kind = 'fs.delete'
  AND target.trash_entry_id = jr.trash_entry_id
  AND target.trash_entry_version = jr.approved_entry_version
  AND target.manifest_json IS NOT NULL
  AND json_valid(target.manifest_json)
  AND json_type(target.manifest_json) = 'array'
  AND json_array_length(target.manifest_json) > 0
  AND json_valid(revision.manifest_json)
  AND json(target.manifest_json) = json(revision.manifest_json)
  AND json_valid(entry.manifest_json)
  AND json(target.manifest_json) = json(entry.manifest_json)
  AND julianday(target.created_at) IS NOT NULL
  AND julianday(decision.created_at) IS NOT NULL
  AND julianday(target.created_at) <= julianday(decision.created_at);

-- A fully recovered v7 generation has no active leases and already has the
-- same version in all three journals. It is safe to leave available for the
-- normal v9 read-only claim.
INSERT INTO d09_safe_approved_recovery (
    janitor_id, action_id, entry_id, janitor_version, action_version,
    worker_id, lease_until, repair_action
)
SELECT jr.id, action.id, entry.id, jr.version, action.version,
       '', '', 0
FROM janitor_records AS jr
JOIN action_runs AS action
  ON action.id = jr.approval_action_run_id
JOIN trash_entries AS entry
  ON entry.id = jr.trash_entry_id
JOIN action_plan_revisions AS revision
  ON revision.plan_id = jr.approval_plan_id
 AND revision.revision = jr.approval_plan_revision
 AND revision.digest = jr.approval_plan_digest
JOIN action_plans AS plan
  ON plan.id = revision.plan_id
JOIN early_purge_plan_targets AS target
  ON target.plan_id = revision.plan_id
 AND target.revision = revision.revision
 AND target.plan_digest = revision.digest
JOIN review_decisions AS decision
  ON decision.id = jr.approval_decision_id
WHERE jr.operation = 'purge'
  AND jr.state = 'reconciling'
  AND jr.approval_plan_id IS NOT NULL
  AND jr.claimed_by IS NULL
  AND jr.lease_until IS NULL
  AND action.state = 'reconciling'
  AND action.claimed_by IS NULL
  AND action.lease_until IS NULL
  AND action.version = jr.version
  AND entry.state = 'purging'
  AND entry.active_operation = 'purge'
  AND entry.version = jr.version
  AND entry.operation_claimed_by IS NULL
  AND entry.operation_lease_until IS NULL
  AND plan.state = 'ready'
  AND plan.kind = 'fs.delete'
  AND revision.state = 'ready'
  AND decision.plan_id = revision.plan_id
  AND decision.plan_revision = revision.revision
  AND decision.plan_digest = revision.digest
  AND decision.decision = 'approve'
  AND target.intent_kind = 'fs.delete'
  AND target.trash_entry_id = jr.trash_entry_id
  AND target.trash_entry_version = jr.approved_entry_version
  AND target.manifest_json IS NOT NULL
  AND json_valid(target.manifest_json)
  AND json_type(target.manifest_json) = 'array'
  AND json_array_length(target.manifest_json) > 0
  AND json_valid(revision.manifest_json)
  AND json(target.manifest_json) = json(revision.manifest_json)
  AND json_valid(entry.manifest_json)
  AND json(target.manifest_json) = json(entry.manifest_json)
  AND julianday(target.created_at) IS NOT NULL
  AND julianday(decision.created_at) IS NOT NULL
  AND julianday(target.created_at) <= julianday(decision.created_at);

-- Repair only the v7 post-claim shape. The janitor and trash versions remain
-- unchanged; the action receives the current janitor generation and the same
-- worker lease so startup recovery can advance all three together.
UPDATE action_runs
SET claimed_by = (
        SELECT safe.worker_id
        FROM d09_safe_approved_recovery AS safe
        WHERE safe.action_id = action_runs.id
          AND safe.repair_action = 1
    ),
    lease_until = (
        SELECT safe.lease_until
        FROM d09_safe_approved_recovery AS safe
        WHERE safe.action_id = action_runs.id
          AND safe.repair_action = 1
    ),
    version = (
        SELECT safe.janitor_version
        FROM d09_safe_approved_recovery AS safe
        WHERE safe.action_id = action_runs.id
          AND safe.repair_action = 1
    ),
    updated_at = (
        SELECT jr.updated_at
        FROM janitor_records AS jr
        JOIN d09_safe_approved_recovery AS safe ON safe.janitor_id = jr.id
        WHERE safe.action_id = action_runs.id
          AND safe.repair_action = 1
    )
WHERE id IN (
    SELECT action_id
    FROM d09_safe_approved_recovery
    WHERE repair_action = 1
)
  AND state = 'reconciling'
  AND claimed_by IS NULL
  AND lease_until IS NULL;

-- Repair the action-recovered-first shape by running the historical janitor
-- recovery transition. Its existing trigger advances the shared entry to the
-- same generation while releasing the stale janitor lease.
UPDATE janitor_records
SET state = 'reconciling',
    claimed_by = NULL,
    lease_until = NULL,
    version = version + 1,
    updated_at = updated_at
WHERE id IN (
    SELECT janitor_id
    FROM d09_safe_approved_recovery
    WHERE repair_action = 2
)
  AND state = 'running'
  AND claimed_by IS NOT NULL
  AND lease_until IS NOT NULL;

-- Repair the janitor-recovered-first shape by converting the stale action to
-- the already reconciled generation. No external dispatch is replayed.
UPDATE action_runs
SET state = 'reconciling',
    claimed_by = NULL,
    lease_until = NULL,
    version = (
        SELECT safe.janitor_version
        FROM d09_safe_approved_recovery AS safe
        WHERE safe.action_id = action_runs.id
          AND safe.repair_action = 3
    ),
    updated_at = (
        SELECT jr.updated_at
        FROM janitor_records AS jr
        JOIN d09_safe_approved_recovery AS safe ON safe.janitor_id = jr.id
        WHERE safe.action_id = action_runs.id
          AND safe.repair_action = 3
    )
WHERE id IN (
    SELECT action_id
    FROM d09_safe_approved_recovery
    WHERE repair_action = 3
)
  AND state = 'running'
  AND claimed_by IS NOT NULL
  AND lease_until IS NOT NULL;

CREATE TEMP TABLE d09_upgrade_check (
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d09_upgrade_check (ok)
SELECT CASE WHEN EXISTS (
    SELECT 1
    FROM d09_safe_approved_recovery AS safe
    JOIN janitor_records AS jr ON jr.id = safe.janitor_id
    JOIN action_runs AS action ON action.id = safe.action_id
    JOIN trash_entries AS entry ON entry.id = safe.entry_id
    WHERE (
        (safe.repair_action = 1 AND (
            action.state <> 'reconciling'
            OR action.version <> jr.version
            OR action.claimed_by IS NOT jr.claimed_by
            OR action.lease_until IS NOT jr.lease_until
            OR entry.version <> jr.version
            OR entry.operation_claimed_by IS NOT jr.claimed_by
            OR entry.operation_lease_until IS NOT jr.lease_until
        ))
        OR (safe.repair_action = 2 AND (
            jr.state <> 'reconciling'
            OR jr.version <> safe.janitor_version + 1
            OR jr.claimed_by IS NOT NULL
            OR jr.lease_until IS NOT NULL
            OR action.state <> 'reconciling'
            OR action.version <> jr.version
            OR action.claimed_by IS NOT NULL
            OR action.lease_until IS NOT NULL
            OR entry.version <> jr.version
            OR entry.operation_claimed_by IS NOT NULL
            OR entry.operation_lease_until IS NOT NULL
        ))
        OR (safe.repair_action = 3 AND (
            jr.state <> 'reconciling'
            OR jr.claimed_by IS NOT NULL
            OR jr.lease_until IS NOT NULL
            OR action.state <> 'reconciling'
            OR action.version <> jr.version
            OR action.claimed_by IS NOT NULL
            OR action.lease_until IS NOT NULL
            OR entry.version <> jr.version
            OR entry.operation_claimed_by IS NOT NULL
            OR entry.operation_lease_until IS NOT NULL
        ))
    )
) THEN 0 ELSE 1 END;
DROP TABLE d09_upgrade_check;

-- Any other active approval-bearing purge is ambiguous at this boundary. Keep
-- its evidence but stop both dispatch and generic expiry work. Safe rows above
-- are excluded by their immutable identity, so this cannot downgrade a valid
-- initial dispatch or fully recovered generation.
CREATE TEMP TABLE d09_unsafe_approved_recovery (
    janitor_id TEXT PRIMARY KEY,
    action_id TEXT,
    entry_id TEXT NOT NULL
);
INSERT INTO d09_unsafe_approved_recovery (janitor_id, action_id, entry_id)
SELECT jr.id, jr.approval_action_run_id, jr.trash_entry_id
FROM janitor_records AS jr
WHERE jr.operation = 'purge'
  AND jr.state IN ('queued', 'running', 'waiting_dependency', 'reconciling')
  AND jr.approval_plan_id IS NOT NULL
  AND NOT EXISTS (
      SELECT 1
      FROM d09_safe_approved_recovery AS safe
      WHERE safe.janitor_id = jr.id
  );

UPDATE action_runs
SET state = 'needs_review',
    claimed_by = NULL,
    lease_until = NULL,
    version = version + 1,
    outcome_json = json_object(
        'reason', 'approval_recovery_upgrade_requires_review',
        'previous_state', state,
        'previous_outcome', outcome_json
    ),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id IN (
    SELECT action_id
    FROM d09_unsafe_approved_recovery
    WHERE action_id IS NOT NULL
)
  AND state NOT IN ('succeeded', 'failed', 'cancelled', 'deadline_exceeded', 'needs_review');

UPDATE janitor_records
SET state = 'held',
    claimed_by = NULL,
    lease_until = NULL,
    version = version + 1,
    outcome_json = json_object(
        'reason', 'approval_recovery_upgrade_requires_review',
        'previous_state', state,
        'previous_outcome', outcome_json
    ),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id IN (SELECT janitor_id FROM d09_unsafe_approved_recovery)
  AND state IN ('queued', 'running', 'waiting_dependency', 'reconciling');

-- Queued legacy rows do not pass the finish trigger, so explicitly hold their
-- entry. Running/reconciling rows have already been moved by that trigger and
-- are harmlessly excluded by the state predicate.
UPDATE trash_entries
SET state = 'held',
    hold_reason = 'approval_recovery_upgrade_requires_review',
    active_operation = NULL,
    operation_claimed_by = NULL,
    operation_lease_until = NULL,
    version = version + 1,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id IN (SELECT entry_id FROM d09_unsafe_approved_recovery)
  AND state IN ('trashed', 'purging', 'restoring', 'failed')
  AND (active_operation IS NULL OR active_operation = 'purge');

DROP TABLE d09_unsafe_approved_recovery;
DROP TABLE d09_safe_approved_recovery;

-- Recover the coupled action in the same write that transitions a janitor
-- lease to read-only reconciliation. This prevents either action recovery
-- ordering from exposing an unleased approval-bound action to a generic
-- mutation worker.
DROP TRIGGER IF EXISTS janitor_records_approved_recovery_action;
CREATE TRIGGER janitor_records_approved_recovery_action
BEFORE UPDATE OF state ON janitor_records
WHEN OLD.state = 'running'
 AND NEW.state = 'reconciling'
 AND OLD.operation = 'purge'
 AND OLD.approval_plan_id IS NOT NULL
BEGIN
    UPDATE action_runs
    SET state = 'reconciling',
        claimed_by = NULL,
        lease_until = NULL,
        version = version + 1,
        updated_at = NEW.updated_at
    WHERE id = OLD.approval_action_run_id
      AND plan_id = OLD.approval_plan_id
      AND plan_revision = OLD.approval_plan_revision
      AND plan_digest = OLD.approval_plan_digest
      AND state IN ('running', 'reconciling')
      AND version = OLD.version
      AND claimed_by IS OLD.claimed_by
      AND lease_until IS OLD.lease_until;
    SELECT CASE WHEN changes() = 0 THEN RAISE(ABORT, 'approved purge action recovery fence was lost') END;
END;

-- Read-only reconciliation remains valid after cancellation or deadline. Those
-- fields block future mutation dispatch in the executor; they do not erase the
-- evidence required to resolve an accepted effect.
DROP TRIGGER IF EXISTS janitor_records_approved_reconciliation_claim;
CREATE TRIGGER janitor_records_approved_reconciliation_claim
BEFORE UPDATE OF state, claimed_by, lease_until,
    approval_plan_id, approval_plan_revision, approval_plan_digest,
    approval_decision_id, approval_action_run_id, approved_entry_version,
    approval_action_run_version ON janitor_records
WHEN OLD.state = 'reconciling'
 AND NEW.state = 'running'
 AND OLD.operation = 'purge'
 AND OLD.approval_plan_id IS NOT NULL
BEGIN
    SELECT CASE WHEN NOT EXISTS (
        SELECT 1
        FROM action_plan_revisions AS revision
        JOIN action_plans AS plan
          ON plan.id = revision.plan_id
        JOIN early_purge_plan_targets AS target
          ON target.plan_id = revision.plan_id
         AND target.revision = revision.revision
         AND target.plan_digest = revision.digest
        JOIN trash_entries AS entry
          ON entry.id = target.trash_entry_id
        JOIN review_decisions AS decision
          ON decision.id = OLD.approval_decision_id
        JOIN action_runs AS action
          ON action.id = OLD.approval_action_run_id
        WHERE revision.plan_id = OLD.approval_plan_id
          AND revision.revision = OLD.approval_plan_revision
          AND revision.digest = OLD.approval_plan_digest
          AND revision.state = 'ready'
          AND plan.state = 'ready'
          AND plan.kind = 'fs.delete'
          AND decision.plan_id = revision.plan_id
          AND decision.plan_revision = revision.revision
          AND decision.plan_digest = revision.digest
          AND decision.decision = 'approve'
          AND target.intent_kind = 'fs.delete'
          AND target.trash_entry_id = OLD.trash_entry_id
          AND target.trash_entry_version = OLD.approved_entry_version
          AND target.manifest_json IS NOT NULL
          AND json_valid(target.manifest_json)
          AND json_type(target.manifest_json) = 'array'
          AND json_array_length(target.manifest_json) > 0
          AND json_valid(revision.manifest_json)
          AND json(target.manifest_json) = json(revision.manifest_json)
          AND json_valid(entry.manifest_json)
          AND json(target.manifest_json) = json(entry.manifest_json)
          AND julianday(target.created_at) IS NOT NULL
          AND julianday(decision.created_at) IS NOT NULL
          AND julianday(target.created_at) <= julianday(decision.created_at)
          AND entry.id = OLD.trash_entry_id
          AND entry.state = 'purging'
          AND entry.active_operation = 'purge'
          AND (
              (entry.version = OLD.version + OLD.approved_entry_version - 1
               AND entry.operation_claimed_by IS NULL
               AND entry.operation_lease_until IS NULL)
              OR (entry.version = OLD.version + OLD.approved_entry_version
                  AND entry.operation_claimed_by IS NEW.claimed_by
                  AND entry.operation_lease_until IS NEW.lease_until)
          )
          AND action.plan_id = revision.plan_id
          AND action.plan_revision = revision.revision
          AND action.plan_digest = revision.digest
          AND action.state = 'reconciling'
          AND action.version = OLD.version
          AND action.version > OLD.approval_action_run_version + 1
          AND action.claimed_by IS NULL
          AND action.lease_until IS NULL
          AND (action.next_attempt_at IS NULL OR julianday(action.next_attempt_at) <= julianday(NEW.updated_at))
          AND NEW.approval_plan_id IS OLD.approval_plan_id
          AND NEW.approval_plan_revision IS OLD.approval_plan_revision
          AND NEW.approval_plan_digest IS OLD.approval_plan_digest
          AND NEW.approval_decision_id IS OLD.approval_decision_id
          AND NEW.approval_action_run_id IS OLD.approval_action_run_id
          AND NEW.approved_entry_version IS OLD.approved_entry_version
          AND NEW.approval_action_run_version IS OLD.approval_action_run_version
          AND NEW.trash_entry_id = OLD.trash_entry_id
          AND NEW.operation = OLD.operation
          AND NEW.claimed_by IS NOT NULL
          AND length(trim(NEW.claimed_by)) > 0
          AND NEW.lease_until IS NOT NULL
          AND julianday(NEW.updated_at) IS NOT NULL
          AND julianday(NEW.lease_until) > julianday(NEW.updated_at)
          AND (OLD.next_attempt_at IS NULL OR julianday(OLD.next_attempt_at) <= julianday(NEW.updated_at))
          AND (OLD.claimed_by IS NULL OR OLD.lease_until IS NULL OR julianday(OLD.lease_until) <= julianday(NEW.updated_at))
    ) THEN RAISE(ABORT, 'approved purge reconciliation requires an exact current generation') END;

    UPDATE action_runs
    SET claimed_by = NEW.claimed_by,
        lease_until = NEW.lease_until,
        version = version + 1,
        updated_at = NEW.updated_at
    WHERE id = NEW.approval_action_run_id
      AND plan_id = NEW.approval_plan_id
      AND plan_revision = NEW.approval_plan_revision
      AND plan_digest = NEW.approval_plan_digest
      AND state = 'reconciling'
      AND version = OLD.version
      AND claimed_by IS NULL
      AND lease_until IS NULL
      AND (next_attempt_at IS NULL OR julianday(next_attempt_at) <= julianday(NEW.updated_at));
    SELECT CASE WHEN changes() = 0 THEN RAISE(ABORT, 'approved purge action fence was lost') END;
END;

-- Cancellation changes the action version. Propagate that generation to the
-- active janitor and trash entry in the same statement so a later recovery
-- claim still has one coherent CAS version while mutation remains blocked.
DROP TRIGGER IF EXISTS action_runs_approval_reconciliation_cancellation_sync;
CREATE TRIGGER action_runs_approval_reconciliation_cancellation_sync
AFTER UPDATE OF cancellation_requested_at ON action_runs
WHEN NEW.cancellation_requested_at IS NOT OLD.cancellation_requested_at
 AND EXISTS (
     SELECT 1
     FROM janitor_records AS jr
     WHERE jr.approval_action_run_id = NEW.id
       AND jr.approval_plan_id IS NOT NULL
       AND jr.operation = 'purge'
       AND jr.state IN ('running', 'reconciling')
 )
BEGIN
    SELECT CASE WHEN (
        SELECT count(*)
        FROM janitor_records AS jr
        WHERE jr.approval_action_run_id = NEW.id
          AND jr.approval_plan_id IS NOT NULL
          AND jr.operation = 'purge'
          AND jr.state IN ('running', 'reconciling')
    ) <> 1 THEN RAISE(ABORT, 'approved purge cancellation has ambiguous janitor binding') END;

    UPDATE janitor_records
    SET version = NEW.version,
        updated_at = NEW.updated_at
    WHERE approval_action_run_id = NEW.id
      AND approval_plan_id IS NOT NULL
      AND operation = 'purge'
      AND state IN ('running', 'reconciling')
      AND version = OLD.version;
    SELECT CASE WHEN changes() = 0 THEN RAISE(ABORT, 'approved purge cancellation janitor generation was lost') END;

    UPDATE trash_entries
    SET version = NEW.version,
        updated_at = NEW.updated_at
    WHERE id = (
        SELECT trash_entry_id
        FROM janitor_records
        WHERE approval_action_run_id = NEW.id
          AND approval_plan_id IS NOT NULL
          AND operation = 'purge'
          AND state IN ('running', 'reconciling')
    )
      AND version = OLD.version
      AND active_operation = 'purge';
    SELECT CASE WHEN changes() = 0 THEN RAISE(ABORT, 'approved purge cancellation trash generation was lost') END;
END;

PRAGMA legacy_alter_table = OFF;
PRAGMA foreign_keys = ON;
CREATE TEMP TABLE d09_foreign_key_check (
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d09_foreign_key_check (ok)
SELECT CASE WHEN EXISTS (SELECT 1 FROM pragma_foreign_key_check) THEN 0 ELSE 1 END;
DROP TABLE d09_foreign_key_check;
