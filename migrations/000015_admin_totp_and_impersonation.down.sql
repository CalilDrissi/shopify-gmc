DROP INDEX IF EXISTS tenants_suspended_idx;
ALTER TABLE tenants DROP COLUMN IF EXISTS suspended_at;

DROP INDEX IF EXISTS sessions_impersonation_log_idx;
ALTER TABLE sessions DROP COLUMN IF EXISTS impersonation_log_id;
ALTER TABLE sessions DROP COLUMN IF EXISTS impersonating_tenant_id;
ALTER TABLE sessions DROP COLUMN IF EXISTS impersonating_user_id;

ALTER TABLE platform_admins DROP COLUMN IF EXISTS totp_enrolled_at;
ALTER TABLE platform_admins DROP COLUMN IF EXISTS totp_secret;
