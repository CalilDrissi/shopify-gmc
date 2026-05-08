-- Per-trigger flags. Existing schema only had a single 'enabled' flag — we
-- now subscribe per-trigger so a user can opt in to "new critical" but skip
-- "score drop" noise.
ALTER TABLE store_alert_subscriptions ADD COLUMN on_new_critical       boolean NOT NULL DEFAULT true;
ALTER TABLE store_alert_subscriptions ADD COLUMN on_score_drop         boolean NOT NULL DEFAULT true;
ALTER TABLE store_alert_subscriptions ADD COLUMN on_audit_failed       boolean NOT NULL DEFAULT true;
ALTER TABLE store_alert_subscriptions ADD COLUMN on_gmc_account_change boolean NOT NULL DEFAULT false;
ALTER TABLE store_alert_subscriptions ADD COLUMN score_drop_threshold  integer NOT NULL DEFAULT 10;

-- Log of every email actually sent. Two reasons it exists:
--   1. dedupe within an audit: UNIQUE(audit_id, user_id, trigger) — even
--      if the dispatcher fires twice we never double-send.
--   2. 24h rate limit: a quick lookup keyed by (user, store, sent_at)
--      so the dispatcher can decide whether to suppress a non-critical alert.
CREATE TABLE alert_dispatches (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id         uuid REFERENCES users(id) ON DELETE SET NULL,
  store_id        uuid REFERENCES stores(id) ON DELETE CASCADE,
  audit_id        uuid REFERENCES audits(id) ON DELETE CASCADE,
  subscription_id uuid REFERENCES store_alert_subscriptions(id) ON DELETE SET NULL,
  trigger         text NOT NULL,
  channel         text NOT NULL DEFAULT 'email',
  target          text NOT NULL,
  sent_at         timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX alert_dispatches_audit_user_trigger_idx
  ON alert_dispatches (audit_id, user_id, trigger) WHERE user_id IS NOT NULL;
CREATE INDEX alert_dispatches_user_store_sent_idx
  ON alert_dispatches (user_id, store_id, sent_at DESC);
CREATE INDEX alert_dispatches_tenant_idx
  ON alert_dispatches (tenant_id, sent_at DESC);

ALTER TABLE alert_dispatches ENABLE ROW LEVEL SECURITY;
ALTER TABLE alert_dispatches FORCE ROW LEVEL SECURITY;
CREATE POLICY alert_dispatches_isolation ON alert_dispatches
  USING (tenant_id = app_current_tenant_id())
  WITH CHECK (tenant_id = app_current_tenant_id());
