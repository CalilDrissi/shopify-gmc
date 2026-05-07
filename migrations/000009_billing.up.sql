CREATE TABLE purchases (
    id              uuid            PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       uuid            REFERENCES tenants(id) ON DELETE SET NULL,
    user_id         uuid            REFERENCES users(id)   ON DELETE SET NULL,
    gumroad_sale_id text            UNIQUE,
    license_key     text            UNIQUE,
    product_id      text,
    plan            plan_tier       NOT NULL,
    amount_cents    int,
    currency        text,
    status          purchase_status NOT NULL DEFAULT 'active',
    purchased_at    timestamptz,
    refunded_at     timestamptz,
    created_at      timestamptz     NOT NULL DEFAULT now(),
    updated_at      timestamptz     NOT NULL DEFAULT now()
);
CREATE INDEX purchases_tenant_id_idx ON purchases (tenant_id) WHERE tenant_id IS NOT NULL;
CREATE INDEX purchases_user_id_idx   ON purchases (user_id)   WHERE user_id   IS NOT NULL;

CREATE TABLE gumroad_events (
    id               uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    gumroad_event_id text        UNIQUE,
    event_type       text        NOT NULL,
    payload          jsonb       NOT NULL,
    processed_at     timestamptz,
    error_message    text,
    received_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX gumroad_events_event_type_idx ON gumroad_events (event_type);
CREATE INDEX gumroad_events_unprocessed_idx
    ON gumroad_events (received_at) WHERE processed_at IS NULL;
