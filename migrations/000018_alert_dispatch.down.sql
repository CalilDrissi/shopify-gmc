DROP TABLE IF EXISTS alert_dispatches;

ALTER TABLE store_alert_subscriptions DROP COLUMN IF EXISTS score_drop_threshold;
ALTER TABLE store_alert_subscriptions DROP COLUMN IF EXISTS on_gmc_account_change;
ALTER TABLE store_alert_subscriptions DROP COLUMN IF EXISTS on_audit_failed;
ALTER TABLE store_alert_subscriptions DROP COLUMN IF EXISTS on_score_drop;
ALTER TABLE store_alert_subscriptions DROP COLUMN IF EXISTS on_new_critical;
