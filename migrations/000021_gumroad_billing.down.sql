DROP FUNCTION IF EXISTS app_increment_usage(uuid, text, int);
DROP INDEX IF EXISTS tenants_pending_plan_idx;
ALTER TABLE tenants DROP COLUMN IF EXISTS gumroad_subscription_id;
ALTER TABLE tenants DROP COLUMN IF EXISTS pending_plan_at;
ALTER TABLE tenants DROP COLUMN IF EXISTS pending_plan;

DROP INDEX IF EXISTS gumroad_webhook_events_uniq;
ALTER TABLE gumroad_webhook_events DROP COLUMN IF EXISTS signature_ok;
ALTER TABLE gumroad_webhook_events DROP COLUMN IF EXISTS product_id;
ALTER TABLE gumroad_webhook_events DROP COLUMN IF EXISTS tenant_id;
ALTER TABLE gumroad_webhook_events DROP COLUMN IF EXISTS sale_id;
