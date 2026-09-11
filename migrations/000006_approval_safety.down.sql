-- Neutralize any v6 approval dispatch before returning to the v5 trigger
-- contract. The target and decision remain immutable, but a later v5 runtime
-- must require a fresh exact claim after this downgrade.
PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

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

DROP TRIGGER IF EXISTS janitor_records_generic_claim_requires_exact_approval;
DROP TRIGGER IF EXISTS review_decisions_approval_window_validate_insert;
DROP TRIGGER IF EXISTS action_runs_approval_window_validate_insert;
DROP TRIGGER IF EXISTS early_purge_plan_targets_validate_insert;
DROP TRIGGER IF EXISTS janitor_records_approved_purge_claim;

-- Restore the original v5 target validation and approved claim trigger. The
-- v6 down migration itself leaves target/approval columns intact; 000005.down
-- removes those additions only when the caller continues to v4.
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

-- Keep generic recovery safe even while the caller is on the v4-compatible
-- side of 000005.down. This trigger intentionally omits the v5 CAS column so
-- the same guard remains valid after that column is removed.
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
       OR OLD.approval_action_run_version IS NOT NULL
 )
BEGIN
    SELECT RAISE(ABORT, 'approval-bearing janitor requires an exact approved claim');
END;

PRAGMA legacy_alter_table = OFF;
PRAGMA foreign_keys = ON;
CREATE TEMP TABLE d06_down_foreign_key_check (
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d06_down_foreign_key_check (ok)
SELECT CASE WHEN EXISTS (SELECT 1 FROM pragma_foreign_key_check) THEN 0 ELSE 1 END;
DROP TABLE d06_down_foreign_key_check;
