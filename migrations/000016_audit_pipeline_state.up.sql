-- Pipeline state on the audit row: stage-by-stage progress + the score, risk
-- level, and executive summary the report page renders.
ALTER TABLE audits ADD COLUMN progress     jsonb       NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE audits ADD COLUMN score        int;
ALTER TABLE audits ADD COLUMN risk_level   text;
ALTER TABLE audits ADD COLUMN summary      text;
ALTER TABLE audits ADD COLUMN next_steps   jsonb;

-- The "I've applied this fix" marker on an issue. Distinct from resolved_at,
-- which the next audit run sets when re-detection passes.
ALTER TABLE issues ADD COLUMN fix_applied_at timestamptz;
ALTER TABLE issues ADD COLUMN fix_applied_by uuid REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX issues_fix_applied_idx ON issues (fix_applied_at) WHERE fix_applied_at IS NOT NULL;
