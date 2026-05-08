-- Extend audit_diffs with the columns the differ now writes.
-- Existing schema already has: tenant_id, audit_id, previous_audit_id,
-- new_issue_count, resolved_issue_count, diff jsonb.

ALTER TABLE audit_diffs ADD COLUMN unchanged_count    int NOT NULL DEFAULT 0;
ALTER TABLE audit_diffs ADD COLUMN new_critical_count int NOT NULL DEFAULT 0;
ALTER TABLE audit_diffs ADD COLUMN prev_score         int;
ALTER TABLE audit_diffs ADD COLUMN new_score          int;
ALTER TABLE audit_diffs ADD COLUMN score_delta        int;

-- Audits made by the scheduler have triggered_by IS NULL and trigger='scheduled'.
-- The trigger column is plain text, so no schema change is needed; we add
-- a partial index so the audits-list "source" filter is cheap.
CREATE INDEX IF NOT EXISTS audits_trigger_idx
  ON audits (tenant_id, trigger, created_at DESC);
