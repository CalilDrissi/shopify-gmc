-- TOTP secret per platform admin (base32, stored plaintext for now;
-- production should encrypt with SETTINGS_ENCRYPTION_KEY).
ALTER TABLE platform_admins ADD COLUMN totp_secret       text;
ALTER TABLE platform_admins ADD COLUMN totp_enrolled_at  timestamptz;

-- Impersonation state lives on the session row itself so the request
-- pipeline can find it without an extra lookup.
ALTER TABLE sessions ADD COLUMN impersonating_user_id   uuid REFERENCES users(id)              ON DELETE SET NULL;
ALTER TABLE sessions ADD COLUMN impersonating_tenant_id uuid REFERENCES tenants(id)            ON DELETE SET NULL;
ALTER TABLE sessions ADD COLUMN impersonation_log_id    uuid REFERENCES impersonation_log(id)  ON DELETE SET NULL;

CREATE INDEX sessions_impersonation_log_idx ON sessions (impersonation_log_id) WHERE impersonation_log_id IS NOT NULL;

-- Tenant suspension flag (admins can suspend without deleting).
ALTER TABLE tenants ADD COLUMN suspended_at timestamptz;
CREATE INDEX tenants_suspended_idx ON tenants (suspended_at) WHERE suspended_at IS NOT NULL;
