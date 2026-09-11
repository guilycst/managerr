-- D-01 correction round seven. Keep approved purge reconciliation resumable
-- across any number of worker crashes and fence the coupled action run while
-- the janitor owns the read-only reconciliation lease.
-- Historical migrations remain immutable; this ordered migration replaces the
-- round-six recovery trigger with a generation-safe variant.

PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

DROP TRIGGER IF EXISTS janitor_records_approved_reconciliation_claim;

-- The janitor and its trash entry advance together on every claim/recovery
-- transition. The immutable approval version is the origin, while the
-- janitor version identifies the current generation. Accept either side of
-- the trash claim trigger so this remains atomic regardless of trigger order.
-- The associated action is fenced in this same trigger: it stays explicitly
-- reconciling, but its worker lease prevents a generic action claim from
-- racing the janitor's read-only evidence collection.
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
          AND action.cancellation_requested_at IS NULL
          AND (action.deadline_at IS NULL OR julianday(action.deadline_at) > julianday(NEW.updated_at))
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
      AND cancellation_requested_at IS NULL
      AND (deadline_at IS NULL OR julianday(deadline_at) > julianday(NEW.updated_at))
      AND (next_attempt_at IS NULL OR julianday(next_attempt_at) <= julianday(NEW.updated_at));
    SELECT CASE WHEN changes() = 0 THEN RAISE(ABORT, 'approved purge action fence was lost') END;
END;

PRAGMA legacy_alter_table = OFF;
PRAGMA foreign_keys = ON;
CREATE TEMP TABLE d08_foreign_key_check (
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d08_foreign_key_check (ok)
SELECT CASE WHEN EXISTS (SELECT 1 FROM pragma_foreign_key_check) THEN 0 ELSE 1 END;
DROP TABLE d08_foreign_key_check;
