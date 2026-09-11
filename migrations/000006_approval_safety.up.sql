-- D-01 correction round five. Approval is an immutable decision over an
-- already-bound target. Legacy approval-bearing janitor rows are held for a
-- new review before the v6 dispatch guards are installed. Preview expiry is
-- checked while creating the target, decision and action; an accepted action
-- may dispatch after preview expiry while its own deadline remains live.

PRAGMA foreign_keys = OFF;
PRAGMA legacy_alter_table = ON;

-- A v4/v5 approval binding has no safe interpretation after a down/up cycle
-- unless the exact target and action CAS relation are still present. Preserve
-- the old values in the outcome journal, clear the worker lease, and require a
-- new review. This is deliberately conservative even for a row that looked
-- valid before the upgrade: its target table may be removed by a later down.
UPDATE action_runs
SET state = 'needs_review',
    claimed_by = NULL,
    lease_until = NULL,
    version = version + 1,
    outcome_json = json_object(
        'reason', 'approval_requires_new_review',
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
        'reason', 'approval_requires_new_review',
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

DROP TRIGGER IF EXISTS early_purge_plan_targets_validate_insert;
DROP TRIGGER IF EXISTS review_decisions_approval_window_validate_insert;
DROP TRIGGER IF EXISTS action_runs_approval_window_validate_insert;
DROP TRIGGER IF EXISTS janitor_records_generic_claim_requires_exact_approval;
DROP TRIGGER IF EXISTS janitor_records_approved_purge_claim;

-- Targets are created before approval and cannot be rebound after an approve
-- decision. The target timestamp must also fall inside the revision preview.
CREATE TRIGGER early_purge_plan_targets_validate_insert
BEFORE INSERT ON early_purge_plan_targets
WHEN EXISTS (
    SELECT 1
    FROM review_decisions AS rd
    WHERE rd.plan_id = NEW.plan_id
      AND rd.plan_revision = NEW.revision
      AND rd.decision = 'approve'
)
 OR NOT EXISTS (
    SELECT 1
    FROM action_plans AS ap
    JOIN action_plan_revisions AS apr
      ON apr.plan_id = ap.id AND apr.revision = NEW.revision
    JOIN trash_entries AS te ON te.id = NEW.trash_entry_id
    WHERE ap.id = NEW.plan_id
      AND ap.kind = 'fs.delete'
      AND ap.state = 'ready'
      AND apr.state = 'ready'
      AND apr.digest = NEW.plan_digest
      AND apr.expires_at IS NOT NULL
      AND julianday(apr.created_at) IS NOT NULL
      AND julianday(apr.created_at) <= julianday(apr.expires_at)
      AND julianday(NEW.created_at) IS NOT NULL
      AND julianday(NEW.created_at) <= julianday(apr.expires_at)
      AND json_valid(NEW.manifest_json)
      AND json_valid(apr.manifest_json)
      AND json(NEW.manifest_json) = json(apr.manifest_json)
      AND te.state = 'trashed'
      AND te.active_operation IS NULL
      AND te.version = NEW.trash_entry_version
      AND json_valid(te.manifest_json)
      AND json(NEW.manifest_json) = json(te.manifest_json)
)
BEGIN
    SELECT RAISE(ABORT, 'early purge target must precede approval and bind an exact live preview scope');
END;

-- An approval is valid only for a ready, unexpired revision and must be
-- recorded before that revision's preview expires. A delete whose manifest
-- matches a live trash entry is an early purge and requires exactly one
-- pre-existing target whose manifest/version matches the revision and entry.
-- A standalone live delete with no matching trash entry remains a valid API
-- action and is planned/executed by its own fs.delete handler.
CREATE TRIGGER review_decisions_approval_window_validate_insert
BEFORE INSERT ON review_decisions
WHEN NEW.decision = 'approve'
 AND (
    NOT EXISTS (
        SELECT 1
        FROM action_plans AS ap
        JOIN action_plan_revisions AS apr
          ON apr.plan_id = ap.id
         AND apr.revision = NEW.plan_revision
         AND apr.digest = NEW.plan_digest
        WHERE ap.id = NEW.plan_id
          AND ap.state = 'ready'
          AND apr.state = 'ready'
          AND apr.expires_at IS NOT NULL
          AND julianday(apr.created_at) IS NOT NULL
          AND julianday(apr.created_at) <= julianday(apr.expires_at)
          AND julianday(NEW.created_at) IS NOT NULL
          AND julianday(NEW.created_at) <= julianday(apr.expires_at)
    )
    OR EXISTS (
        SELECT 1
        FROM action_plans AS ap
        JOIN action_plan_revisions AS apr
          ON apr.plan_id = ap.id AND apr.revision = NEW.plan_revision
         AND apr.digest = NEW.plan_digest
        JOIN trash_entries AS te
          ON te.state = 'trashed'
        WHERE ap.id = NEW.plan_id
          AND ap.kind = 'fs.delete'
          AND json_valid(apr.manifest_json)
          AND json_valid(te.manifest_json)
          AND json(apr.manifest_json) = json(te.manifest_json)
    ) AND NOT EXISTS (
        SELECT 1
        FROM action_plans AS ap
        JOIN action_plan_revisions AS apr
          ON apr.plan_id = ap.id
         AND apr.revision = NEW.plan_revision
         AND apr.digest = NEW.plan_digest
        JOIN early_purge_plan_targets AS target
          ON target.plan_id = apr.plan_id
         AND target.revision = apr.revision
         AND target.plan_digest = apr.digest
        JOIN trash_entries AS te ON te.id = target.trash_entry_id
        WHERE ap.id = NEW.plan_id
          AND ap.kind = 'fs.delete'
          AND ap.state = 'ready'
          AND apr.state = 'ready'
          AND apr.expires_at IS NOT NULL
          AND julianday(apr.created_at) IS NOT NULL
          AND julianday(apr.created_at) <= julianday(apr.expires_at)
          AND julianday(NEW.created_at) IS NOT NULL
          AND julianday(NEW.created_at) <= julianday(apr.expires_at)
          AND target.intent_kind = 'fs.delete'
          AND target.trash_entry_version > 0
          AND julianday(target.created_at) IS NOT NULL
          AND julianday(target.created_at) <= julianday(apr.expires_at)
          AND json_valid(target.manifest_json)
          AND json_type(target.manifest_json) = 'array'
          AND json_array_length(target.manifest_json) > 0
          AND json_valid(apr.manifest_json)
          AND json(target.manifest_json) = json(apr.manifest_json)
          AND te.state = 'trashed'
          AND te.active_operation IS NULL
          AND te.version = target.trash_entry_version
          AND json_valid(te.manifest_json)
          AND json(target.manifest_json) = json(te.manifest_json)
    )
 )
BEGIN
    SELECT RAISE(ABORT, 'approval must be created before preview expiry with an exact fs.delete target');
END;

-- Action creation is part of the accepted preview window. Dispatch later is
-- allowed; the action deadline and cancellation state are checked at claim.
CREATE TRIGGER action_runs_approval_window_validate_insert
BEFORE INSERT ON action_runs
WHEN EXISTS (
    SELECT 1
    FROM action_plan_revisions AS apr
    WHERE apr.plan_id = NEW.plan_id
      AND apr.revision = NEW.plan_revision
      AND apr.digest = NEW.plan_digest
      AND (
          julianday(apr.created_at) IS NULL
          OR julianday(apr.expires_at) IS NULL
          OR julianday(apr.created_at) > julianday(apr.expires_at)
          OR julianday(NEW.created_at) IS NULL
          OR julianday(NEW.created_at) > julianday(apr.expires_at)
      )
)
BEGIN
    SELECT RAISE(ABORT, 'action run must be created before preview expiry');
END;

-- Generic janitor recovery must never reinterpret an approval-bearing row as
-- an ordinary expiry purge or restore. Only the exact approved claim trigger
-- below may attach approval metadata, and its old row is unbound.
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

-- This trigger preserves the v5 atomic CAS while allowing an accepted claim
-- after preview expiry. Creation timestamps are checked against expiry; only
-- the action deadline and cancellation request remain dispatch gates.
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
          AND julianday(apr.created_at) IS NOT NULL
          AND julianday(apr.created_at) <= julianday(apr.expires_at)
          AND julianday(target.created_at) IS NOT NULL
          AND julianday(target.created_at) <= julianday(apr.expires_at)
          AND julianday(rd.created_at) IS NOT NULL
          AND julianday(rd.created_at) <= julianday(apr.expires_at)
          AND julianday(ar.created_at) IS NOT NULL
          AND julianday(ar.created_at) <= julianday(apr.expires_at)
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

PRAGMA legacy_alter_table = OFF;
PRAGMA foreign_keys = ON;
CREATE TEMP TABLE d06_foreign_key_check (
    ok INTEGER NOT NULL CHECK (ok = 1)
);
INSERT INTO d06_foreign_key_check (ok)
SELECT CASE WHEN EXISTS (SELECT 1 FROM pragma_foreign_key_check) THEN 0 ELSE 1 END;
DROP TABLE d06_foreign_key_check;
