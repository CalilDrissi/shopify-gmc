DROP INDEX IF EXISTS audits_trigger_idx;

ALTER TABLE audit_diffs DROP COLUMN IF EXISTS score_delta;
ALTER TABLE audit_diffs DROP COLUMN IF EXISTS new_score;
ALTER TABLE audit_diffs DROP COLUMN IF EXISTS prev_score;
ALTER TABLE audit_diffs DROP COLUMN IF EXISTS new_critical_count;
ALTER TABLE audit_diffs DROP COLUMN IF EXISTS unchanged_count;
