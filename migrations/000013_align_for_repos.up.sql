-- Rename tables to canonical repository names.
ALTER TABLE alert_subscriptions      RENAME TO store_alert_subscriptions;
ALTER TABLE gmc_connections          RENAME TO store_gmc_connections;
ALTER TABLE gmc_snapshots            RENAME TO gmc_account_snapshots;
ALTER TABLE gumroad_events           RENAME TO gumroad_webhook_events;
ALTER TABLE admin_audit_log          RENAME TO platform_admin_audit_log;
ALTER TABLE admin_impersonation_log  RENAME TO impersonation_log;
ALTER TABLE job_queue                RENAME TO audit_jobs;

-- Rename indexes to match.
ALTER INDEX gmc_connections_tenant_id_idx          RENAME TO store_gmc_connections_tenant_id_idx;
ALTER INDEX gmc_snapshots_connection_captured_idx  RENAME TO gmc_account_snapshots_connection_captured_idx;
ALTER INDEX alert_subscriptions_tenant_id_idx      RENAME TO store_alert_subscriptions_tenant_id_idx;
ALTER INDEX alert_subscriptions_store_id_idx       RENAME TO store_alert_subscriptions_store_id_idx;
ALTER INDEX gumroad_events_event_type_idx          RENAME TO gumroad_webhook_events_event_type_idx;
ALTER INDEX gumroad_events_unprocessed_idx         RENAME TO gumroad_webhook_events_unprocessed_idx;
ALTER INDEX admin_audit_log_admin_idx              RENAME TO platform_admin_audit_log_admin_idx;
ALTER INDEX admin_audit_log_target_idx             RENAME TO platform_admin_audit_log_target_idx;
ALTER INDEX admin_impersonation_log_admin_idx      RENAME TO impersonation_log_admin_idx;
ALTER INDEX job_queue_ready_idx                    RENAME TO audit_jobs_ready_idx;
ALTER INDEX job_queue_kind_idx                     RENAME TO audit_jobs_kind_idx;
ALTER INDEX job_queue_tenant_id_idx                RENAME TO audit_jobs_tenant_id_idx;

-- Rename RLS policies to match new table names.
ALTER POLICY alert_subscriptions_isolation ON store_alert_subscriptions
    RENAME TO store_alert_subscriptions_isolation;
ALTER POLICY gmc_connections_isolation ON store_gmc_connections
    RENAME TO store_gmc_connections_isolation;
ALTER POLICY gmc_snapshots_isolation ON gmc_account_snapshots
    RENAME TO gmc_account_snapshots_isolation;
ALTER POLICY job_queue_isolation ON audit_jobs
    RENAME TO audit_jobs_isolation;

-- Split email_tokens into two purpose-specific tables.
CREATE TABLE email_verification_tokens (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email       citext      NOT NULL,
    token_hash  bytea       NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX email_verification_tokens_user_id_idx ON email_verification_tokens (user_id);
CREATE INDEX email_verification_tokens_email_idx   ON email_verification_tokens (email);

CREATE TABLE password_reset_tokens (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   bytea       NOT NULL UNIQUE,
    requested_ip inet,
    expires_at   timestamptz NOT NULL,
    consumed_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX password_reset_tokens_user_id_idx ON password_reset_tokens (user_id);

DROP TABLE email_tokens;
DROP TYPE  email_token_kind;

-- New table: per-product GMC compliance statuses.
CREATE TABLE gmc_product_statuses (
    id                   uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            uuid        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    store_id             uuid        NOT NULL REFERENCES stores(id)  ON DELETE CASCADE,
    gmc_connection_id    uuid        REFERENCES store_gmc_connections(id) ON DELETE SET NULL,
    product_id           text        NOT NULL,
    gmc_item_id          text,
    approval_status      text        NOT NULL,
    destination_statuses jsonb       NOT NULL DEFAULT '[]'::jsonb,
    issues               jsonb       NOT NULL DEFAULT '[]'::jsonb,
    last_checked_at      timestamptz NOT NULL DEFAULT now(),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (store_id, product_id)
);
CREATE INDEX gmc_product_statuses_tenant_id_idx     ON gmc_product_statuses (tenant_id);
CREATE INDEX gmc_product_statuses_store_status_idx  ON gmc_product_statuses (store_id, approval_status);

ALTER TABLE gmc_product_statuses ENABLE ROW LEVEL SECURITY;
ALTER TABLE gmc_product_statuses FORCE  ROW LEVEL SECURITY;
CREATE POLICY gmc_product_statuses_isolation ON gmc_product_statuses
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
