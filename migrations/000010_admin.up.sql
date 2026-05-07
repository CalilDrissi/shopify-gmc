CREATE TABLE platform_admins (
    id         uuid                PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid                NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    role       platform_admin_role NOT NULL DEFAULT 'admin',
    created_at timestamptz         NOT NULL DEFAULT now()
);

CREATE TABLE admin_audit_log (
    id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_user_id uuid        REFERENCES users(id) ON DELETE SET NULL,
    action        text        NOT NULL,
    target_type   text,
    target_id     text,
    metadata      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    ip_address    inet,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX admin_audit_log_admin_idx  ON admin_audit_log (admin_user_id, created_at DESC);
CREATE INDEX admin_audit_log_target_idx ON admin_audit_log (target_type, target_id);

CREATE TABLE admin_impersonation_log (
    id                   uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_user_id        uuid        REFERENCES users(id)   ON DELETE SET NULL,
    impersonated_user_id uuid        REFERENCES users(id)   ON DELETE SET NULL,
    tenant_id            uuid        REFERENCES tenants(id) ON DELETE SET NULL,
    session_id           uuid,
    started_at           timestamptz NOT NULL DEFAULT now(),
    ended_at             timestamptz,
    reason               text
);
CREATE INDEX admin_impersonation_log_admin_idx
    ON admin_impersonation_log (admin_user_id, started_at DESC);
