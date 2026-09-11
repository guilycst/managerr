-- Keep schema evolution explicit. This migration adds the common worker lookup
-- indexes without changing the v1 record shape.
CREATE INDEX idx_due_action_runs_claim
    ON action_runs (state, next_attempt_at, lease_until, version);

CREATE INDEX idx_expired_trash_entries
    ON trash_entries (state, expires_at, purge_claimed_at);
