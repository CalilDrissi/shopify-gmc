DROP POLICY IF EXISTS gmc_product_statuses_isolation ON gmc_product_statuses;
DROP TABLE IF EXISTS gmc_product_statuses;

CREATE TYPE email_token_kind AS ENUM ('verify_email', 'password_reset', 'email_change');

CREATE TABLE email_tokens (
    id         uuid             PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid             REFERENCES users(id) ON DELETE CASCADE,
    email      citext           NOT NULL,
    token_hash bytea            NOT NULL UNIQUE,
    kind       email_token_kind NOT NULL,
    expires_at timestamptz      NOT NULL,
    used_at    timestamptz,
    created_at timestamptz      NOT NULL DEFAULT now()
);
CREATE INDEX email_tokens_email_kind_idx ON email_tokens (email, kind);
CREATE INDEX email_tokens_user_id_idx    ON email_tokens (user_id);

DROP TABLE password_reset_tokens;
DROP TABLE email_verification_tokens;

ALTER POLICY audit_jobs_isolation                  ON audit_jobs                RENAME TO job_queue_isolation;
ALTER POLICY gmc_account_snapshots_isolation       ON gmc_account_snapshots     RENAME TO gmc_snapshots_isolation;
ALTER POLICY store_gmc_connections_isolation       ON store_gmc_connections     RENAME TO gmc_connections_isolation;
ALTER POLICY store_alert_subscriptions_isolation   ON store_alert_subscriptions RENAME TO alert_subscriptions_isolation;

ALTER INDEX audit_jobs_tenant_id_idx                          RENAME TO job_queue_tenant_id_idx;
ALTER INDEX audit_jobs_kind_idx                               RENAME TO job_queue_kind_idx;
ALTER INDEX audit_jobs_ready_idx                              RENAME TO job_queue_ready_idx;
ALTER INDEX impersonation_log_admin_idx                       RENAME TO admin_impersonation_log_admin_idx;
ALTER INDEX platform_admin_audit_log_target_idx               RENAME TO admin_audit_log_target_idx;
ALTER INDEX platform_admin_audit_log_admin_idx                RENAME TO admin_audit_log_admin_idx;
ALTER INDEX gumroad_webhook_events_unprocessed_idx            RENAME TO gumroad_events_unprocessed_idx;
ALTER INDEX gumroad_webhook_events_event_type_idx             RENAME TO gumroad_events_event_type_idx;
ALTER INDEX store_alert_subscriptions_store_id_idx            RENAME TO alert_subscriptions_store_id_idx;
ALTER INDEX store_alert_subscriptions_tenant_id_idx           RENAME TO alert_subscriptions_tenant_id_idx;
ALTER INDEX gmc_account_snapshots_connection_captured_idx     RENAME TO gmc_snapshots_connection_captured_idx;
ALTER INDEX store_gmc_connections_tenant_id_idx               RENAME TO gmc_connections_tenant_id_idx;

ALTER TABLE audit_jobs                RENAME TO job_queue;
ALTER TABLE impersonation_log         RENAME TO admin_impersonation_log;
ALTER TABLE platform_admin_audit_log  RENAME TO admin_audit_log;
ALTER TABLE gumroad_webhook_events    RENAME TO gumroad_events;
ALTER TABLE gmc_account_snapshots     RENAME TO gmc_snapshots;
ALTER TABLE store_gmc_connections     RENAME TO gmc_connections;
ALTER TABLE store_alert_subscriptions RENAME TO alert_subscriptions;
