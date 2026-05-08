-- Gumroad webhook dedup + dispatch + scheduled downgrade fields.
-- The pre-existing schema indexed gumroad_webhook_events on `gumroad_event_id`
-- (Gumroad's optional UUID), but Gumroad doesn't always send one and retries
-- can carry the same logical (event_type, sale_id) pair. We add the columns
-- the dispatcher actually keys off.

ALTER TABLE gumroad_webhook_events ADD COLUMN sale_id      text;
ALTER TABLE gumroad_webhook_events ADD COLUMN tenant_id    uuid REFERENCES tenants(id) ON DELETE SET NULL;
ALTER TABLE gumroad_webhook_events ADD COLUMN product_id   text;
ALTER TABLE gumroad_webhook_events ADD COLUMN signature_ok boolean NOT NULL DEFAULT false;

-- One row per (event_type, sale_id) — prevents double-processing when Gumroad
-- retries the same webhook. We allow NULL sale_id for events that don't have
-- one (e.g. subscription_updated for an unknown sale), and use the random PK
-- in that case.
CREATE UNIQUE INDEX gumroad_webhook_events_uniq
  ON gumroad_webhook_events (event_type, sale_id)
  WHERE sale_id IS NOT NULL;

-- Subscription cancellation in Gumroad keeps the user on the paid plan until
-- the period ends. We record what the plan should DOWNGRADE to and when, so
-- the scheduler can reconcile on schedule.
ALTER TABLE tenants ADD COLUMN pending_plan      plan_tier;
ALTER TABLE tenants ADD COLUMN pending_plan_at   timestamptz;
ALTER TABLE tenants ADD COLUMN gumroad_subscription_id text;
CREATE INDEX tenants_pending_plan_idx
  ON tenants (pending_plan_at)
  WHERE pending_plan_at IS NOT NULL;

-- usage_counters table already exists. We just add a helper function the
-- middleware can call without round-tripping a full UPSERT every request.
CREATE OR REPLACE FUNCTION app_increment_usage(
  p_tenant uuid, p_metric text, p_inc int DEFAULT 1
) RETURNS void AS $$
DECLARE
  s date := date_trunc('month', now())::date;
  e date := (date_trunc('month', now()) + interval '1 month')::date;
BEGIN
  INSERT INTO usage_counters (tenant_id, period_start, period_end, metric, count)
  VALUES (p_tenant, s, e, p_metric, p_inc)
  ON CONFLICT (tenant_id, period_start, metric)
  DO UPDATE SET count = usage_counters.count + EXCLUDED.count, updated_at = now();
END;
$$ LANGUAGE plpgsql;
