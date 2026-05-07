CREATE TABLE alert_subscriptions (
    id           uuid           PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid           NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    store_id     uuid           REFERENCES stores(id) ON DELETE CASCADE,
    user_id      uuid           REFERENCES users(id)  ON DELETE CASCADE,
    channel      alert_channel  NOT NULL,
    target       text           NOT NULL,
    min_severity issue_severity NOT NULL DEFAULT 'warning',
    enabled      boolean        NOT NULL DEFAULT true,
    created_at   timestamptz    NOT NULL DEFAULT now(),
    updated_at   timestamptz    NOT NULL DEFAULT now()
);
CREATE INDEX alert_subscriptions_tenant_id_idx ON alert_subscriptions (tenant_id);
CREATE INDEX alert_subscriptions_store_id_idx
    ON alert_subscriptions (store_id) WHERE store_id IS NOT NULL;
