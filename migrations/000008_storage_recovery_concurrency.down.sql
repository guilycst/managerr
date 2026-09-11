-- Returning to the round-six recovery trigger must not leave an
-- approval-bound lease that no longer has the round-seven action fence.
PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

UPDATE action_runs
SET state = 'needs_review',
    claimed_by = NULL,
    lease_until = NULL,
    version = version + 1,
    outcome_json = json_object(
        'reason', 'approval_requires_new_review_after_round7_downgrade',
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
        'reason', 'approval_requires_new_review_after_round7_downgrade',
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

DROP TRIGGER IF EXISTS janitor_records_approved_reconciliation_claim;

-- Restore the round-six trigger. Approval-bound work was held above, so this
-- route is present for historical shape only and cannot dispatch that work.
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
              (entry.version = OLD.approved_entry_version + 2
               AND entry.operation_claimed_by IS NULL
               AND entry.operation_lease_until IS NULL)
              OR (entry.version = OLD.approved_entry_version + 3
                  AND entry.operation_claimed_by IS NEW.claimed_by
                  AND entry.operation_lease_until IS NEW.lease_until)
          )
          AND action.plan_id = revision.plan_id
          AND action.plan_revision = revision.revision
          AND action.plan_digest = revision.digest
          AND action.state = 'reconciling'
          AND action.version > OLD.approval_action_run_version + 1
          AND action.claimed_by IS NULL
          AND action.lease_until IS NULL
          AND action.cancellation_requested_at IS NULL
          AND (action.deadline_at IS NULL OR julianday(action.deadline_at) > julianday(NEW.updated_at))
          AND (action.next_attempt_at IS NULL OR julianday(action.next_attempt_at) <= julianday(NEW.updated_at))
          AND NEW.approval_plan_id IS NOT NULL
          AND NEW.approval_plan_revision IS NOT NULL
          AND NEW.approval_plan_digest IS NOT NULL
          AND NEW.approval_decision_id IS NOT NULL
          AND NEW.approval_action_run_id IS NOT NULL
          AND NEW.approved_entry_version IS NOT NULL
          AND NEW.approval_action_run_version IS NOT NULL
          AND NEW.claimed_by IS NOT NULL
          AND length(trim(NEW.claimed_by)) > 0
          AND NEW.lease_until IS NOT NULL
          AND julianday(NEW.updated_at) IS NOT NULL
          AND julianday(NEW.lease_until) > julianday(NEW.updated_at)
          AND (OLD.next_attempt_at IS NULL OR julianday(OLD.next_attempt_at) <= julianday(NEW.updated_at))
          AND (OLD.claimed_by IS NULL OR OLD.lease_until IS NULL OR julianday(OLD.lease_until) <= julianday(NEW.updated_at))
    ) THEN RAISE(ABORT, 'approved purge reconciliation requires an exact live binding') END;
END;

PRAGMA legacy_alter_table = OFF;
PRAGMA foreign_keys = ON;
CREATE TEMP TABLE d08_down_foreign_key_check (
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d08_down_foreign_key_check (ok)
SELECT CASE WHEN EXISTS (SELECT 1 FROM pragma_foreign_key_check) THEN 0 ELSE 1 END;
DROP TABLE d08_down_foreign_key_check;
