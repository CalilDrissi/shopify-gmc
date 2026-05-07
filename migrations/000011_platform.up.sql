CREATE TABLE platform_settings (
    key        text        PRIMARY KEY,
    value      jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE usage_counters (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    period_start date        NOT NULL,
    period_end   date        NOT NULL,
    metric       text        NOT NULL,
    count        bigint      NOT NULL DEFAULT 0,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, period_start, metric)
);
CREATE INDEX usage_counters_tenant_period_idx ON usage_counters (tenant_id, period_start);
