-- TOTP removed from admin login. Drop the columns added in
-- 000015_admin_totp_and_impersonation.up.sql.
ALTER TABLE platform_admins DROP COLUMN IF EXISTS totp_enrolled_at;
ALTER TABLE platform_admins DROP COLUMN IF EXISTS totp_secret;
