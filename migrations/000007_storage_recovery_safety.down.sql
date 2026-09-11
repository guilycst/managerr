-- Neutralize approval-bound work before returning to the v6 trigger
-- contract. A downgrade must never leave a valid-looking lease that the v6
-- runtime can dispatch without the round-six recovery guard.
PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

UPDATE action_runs
SET state = 'needs_review',
    claimed_by = NULL,
    lease_until = NULL,
    version = version + 1,
    outcome_json = json_object(
        'reason', 'approval_requires_new_review_after_round6_downgrade',
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
        'reason', 'approval_requires_new_review_after_round6_downgrade',
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
DROP TRIGGER IF EXISTS janitor_records_approved_target_chronology_guard;
DROP TRIGGER IF EXISTS janitor_records_generic_claim_requires_exact_approval;

-- Restore the v6 generic guard. The v6 exact approval trigger was preserved
-- unchanged by the up migration and therefore remains in place.
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
CREATE TEMP TABLE d07_down_foreign_key_check (
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d07_down_foreign_key_check (ok)
SELECT CASE WHEN EXISTS (SELECT 1 FROM pragma_foreign_key_check) THEN 0 ELSE 1 END;
DROP TABLE d07_down_foreign_key_check;
