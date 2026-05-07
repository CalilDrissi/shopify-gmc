CREATE TABLE stores (
    id                       uuid           PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                uuid           NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    shop_domain              citext         NOT NULL,
    display_name             text,
    access_token_encrypted   bytea,
    access_token_nonce       bytea,
    scope                    text,
    status                   store_status   NOT NULL DEFAULT 'connected',
    monitor_enabled          boolean        NOT NULL DEFAULT true,
    monitor_frequency        interval       NOT NULL DEFAULT '24 hours',
    monitor_alert_threshold  issue_severity NOT NULL DEFAULT 'warning',
    last_audit_at            timestamptz,
    next_audit_at            timestamptz,
    created_at               timestamptz    NOT NULL DEFAULT now(),
    updated_at               timestamptz    NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, shop_domain)
);
CREATE INDEX stores_next_audit_at_idx ON stores (next_audit_at) WHERE monitor_enabled;

CREATE TABLE gmc_connections (
    id                       uuid                  PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                uuid                  NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    store_id                 uuid                  NOT NULL REFERENCES stores(id)  ON DELETE CASCADE,
    merchant_id              text                  NOT NULL,
    account_email            citext,
    access_token_encrypted   bytea,
    refresh_token_encrypted  bytea,
    token_nonce              bytea,
    token_expires_at         timestamptz,
    status                   gmc_connection_status NOT NULL DEFAULT 'active',
    created_at               timestamptz           NOT NULL DEFAULT now(),
    updated_at               timestamptz           NOT NULL DEFAULT now(),
    UNIQUE (store_id, merchant_id)
);
CREATE INDEX gmc_connections_tenant_id_idx ON gmc_connections (tenant_id);

CREATE TABLE gmc_snapshots (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         uuid        NOT NULL REFERENCES tenants(id)         ON DELETE CASCADE,
    gmc_connection_id uuid        NOT NULL REFERENCES gmc_connections(id) ON DELETE CASCADE,
    captured_at       timestamptz NOT NULL DEFAULT now(),
    product_count     int,
    raw_data          jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX gmc_snapshots_connection_captured_idx
    ON gmc_snapshots (gmc_connection_id, captured_at DESC);
