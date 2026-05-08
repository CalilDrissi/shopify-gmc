-- One subscription row per (tenant, store, user, channel). The store_id is
-- nullable for tenant-wide alerts but those don't conflict with per-store
-- rows; the partial index covers only per-store subscriptions.
CREATE UNIQUE INDEX store_alert_subscriptions_uniq
  ON store_alert_subscriptions (tenant_id, store_id, user_id, channel)
  WHERE store_id IS NOT NULL AND user_id IS NOT NULL;
