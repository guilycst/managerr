-- D-01 correction round six. Close the populated-v5 early-purge chronology
-- gap and provide a dedicated read-only recovery claim for an approved purge.
-- Historical migration files remain immutable; this ordered correction runs
-- immediately after 000006 before the store is made available.

PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

-- A v5 target could have been inserted after its immutable approval because
-- the v5 target trigger did not know about review decisions. Keep every such
-- target as evidence, but hold all related work before the v6 claim contract
-- can be used. A missing or malformed timestamp is unprovable and is held by
-- the same rule. Joining by plan/revision (rather than digest only) also
-- catches a corrupted legacy digest relation conservatively.
CREATE TEMP TABLE d07_unsafe_early_purge_targets (
    plan_id TEXT NOT NULL,
    revision INTEGER NOT NULL,
    plan_digest TEXT NOT NULL,
    trash_entry_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    PRIMARY KEY (plan_id, revision)
);

INSERT INTO d07_unsafe_early_purge_targets (
    plan_id, revision, plan_digest, trash_entry_id, reason
)
SELECT target.plan_id, target.revision, target.plan_digest,
       target.trash_entry_id, 'approval_target_chronology_unprovable'
FROM early_purge_plan_targets AS target
JOIN review_decisions AS decision
  ON decision.plan_id = target.plan_id
 AND decision.plan_revision = target.revision
WHERE decision.decision = 'approve'
  AND NOT (
      decision.plan_digest = target.plan_digest
      AND julianday(target.created_at) IS NOT NULL
      AND julianday(decision.created_at) IS NOT NULL
      AND julianday(target.created_at) <= julianday(decision.created_at)
  );

-- Preserve the immutable target and the original outcome while making any
-- undispatched or in-flight action require a fresh review. Terminal action
-- records are historical evidence and do not need a new transition.
UPDATE action_runs
SET state = 'needs_review',
    claimed_by = NULL,
    lease_until = NULL,
    version = version + 1,
    outcome_json = json_object(
        'reason', 'approval_target_chronology_unprovable',
        'previous_state', state,
        'previous_outcome', outcome_json
    ),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE state NOT IN ('succeeded', 'failed', 'cancelled', 'deadline_exceeded', 'needs_review')
  AND EXISTS (
      SELECT 1
      FROM d07_unsafe_early_purge_targets AS unsafe
      WHERE unsafe.plan_id = action_runs.plan_id
        AND unsafe.revision = action_runs.plan_revision
        AND unsafe.plan_digest = action_runs.plan_digest
  );

-- Hold every queued or active purge janitor for the affected entry, including
-- a v5 row that has not yet copied approval metadata onto the janitor record.
-- The existing finish trigger also moves an active trash entry to held.
UPDATE janitor_records
SET state = 'held',
    claimed_by = NULL,
    lease_until = NULL,
    version = version + 1,
    outcome_json = json_object(
        'reason', 'approval_target_chronology_unprovable',
        'previous_state', state,
        'previous_outcome', outcome_json
    ),
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE operation = 'purge'
  AND state IN ('queued', 'running', 'waiting_dependency', 'reconciling')
  AND EXISTS (
      SELECT 1
      FROM d07_unsafe_early_purge_targets AS unsafe
      WHERE unsafe.trash_entry_id = janitor_records.trash_entry_id
  );

-- A target without a queued janitor must not leave its trash entry eligible
-- for a later generic expiry claim. Do not interfere with an unrelated active
-- restore operation; its own state machine remains authoritative.
UPDATE trash_entries
SET state = 'held',
    hold_reason = 'approval_target_chronology_unprovable',
    active_operation = NULL,
    operation_claimed_by = NULL,
    operation_lease_until = NULL,
    version = version + 1,
    updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id IN (SELECT trash_entry_id FROM d07_unsafe_early_purge_targets)
  AND state IN ('trashed', 'purging')
  AND (active_operation IS NULL OR active_operation = 'purge');

DROP TABLE d07_unsafe_early_purge_targets;

-- Ordinary janitor selection must never claim a complete approval binding.
-- The dedicated recovery claim below is the only path from an approval-bound
-- reconciling row back to a lease-bearing running row.
DROP TRIGGER IF EXISTS janitor_records_generic_claim_requires_exact_approval;
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
 AND NOT (
     OLD.state = 'reconciling'
     AND OLD.operation = 'purge'
     AND OLD.approval_plan_id IS NOT NULL
 )
BEGIN
    SELECT RAISE(ABORT, 'approval-bearing janitor requires an exact approved claim');
END;

-- Every approval-bearing claim, including the historical v6 initial claim,
-- must prove that the immutable target existed no later than the approval.
-- This guard is separate from the v6 trigger so the v6 migration blob remains
-- immutable while the populated upgrade receives the stronger predicate.
CREATE TRIGGER janitor_records_approved_target_chronology_guard
BEFORE UPDATE OF state, approval_plan_id, approval_plan_revision,
    approval_plan_digest, approval_decision_id, approval_action_run_id,
    approved_entry_version, approval_action_run_version ON janitor_records
WHEN NEW.state = 'running'
 AND NEW.operation = 'purge'
 AND NEW.approval_plan_id IS NOT NULL
 AND NOT EXISTS (
     SELECT 1
     FROM early_purge_plan_targets AS target
     JOIN review_decisions AS decision
       ON decision.id = NEW.approval_decision_id
      AND decision.plan_id = target.plan_id
      AND decision.plan_revision = target.revision
      AND decision.plan_digest = target.plan_digest
      AND decision.decision = 'approve'
     WHERE target.plan_id = NEW.approval_plan_id
       AND target.revision = NEW.approval_plan_revision
       AND target.plan_digest = NEW.approval_plan_digest
       AND target.trash_entry_id = NEW.trash_entry_id
       AND target.trash_entry_version = NEW.approved_entry_version
       AND julianday(target.created_at) IS NOT NULL
       AND julianday(decision.created_at) IS NOT NULL
       AND julianday(target.created_at) <= julianday(decision.created_at)
 )
BEGIN
    SELECT RAISE(ABORT, 'approved purge target must precede its immutable decision');
END;

-- A recovered approval binding is safe to lease only after both action and
-- janitor startup recovery have run. The action remains reconciling and
-- unleased; this claim leases the janitor and shared trash entry for
-- read-only evidence collection. It does not dispatch an external effect.
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
          ON decision.id = NEW.approval_decision_id
        JOIN action_runs AS action
          ON action.id = NEW.approval_action_run_id
        WHERE revision.plan_id = NEW.approval_plan_id
          AND revision.revision = NEW.approval_plan_revision
          AND revision.digest = NEW.approval_plan_digest
          AND revision.state = 'ready'
          AND plan.state = 'ready'
          AND plan.kind = 'fs.delete'
          AND decision.plan_id = revision.plan_id
          AND decision.plan_revision = revision.revision
          AND decision.plan_digest = revision.digest
          AND decision.decision = 'approve'
          AND target.intent_kind = 'fs.delete'
          AND target.trash_entry_id = NEW.trash_entry_id
          AND target.trash_entry_version = NEW.approved_entry_version
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
          AND entry.id = NEW.trash_entry_id
          AND entry.state = 'purging'
          AND entry.active_operation = 'purge'
          AND (
              (entry.version = NEW.approved_entry_version + 2
               AND entry.operation_claimed_by IS NULL
               AND entry.operation_lease_until IS NULL)
              OR (entry.version = NEW.approved_entry_version + 3
                  AND entry.operation_claimed_by IS NEW.claimed_by
                  AND entry.operation_lease_until IS NEW.lease_until)
          )
          AND action.plan_id = revision.plan_id
          AND action.plan_revision = revision.revision
          AND action.plan_digest = revision.digest
          AND action.state = 'reconciling'
          AND action.version > NEW.approval_action_run_version + 1
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
CREATE TEMP TABLE d07_foreign_key_check (
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d07_foreign_key_check (ok)
SELECT CASE WHEN EXISTS (SELECT 1 FROM pragma_foreign_key_check) THEN 0 ELSE 1 END;
DROP TABLE d07_foreign_key_check;
