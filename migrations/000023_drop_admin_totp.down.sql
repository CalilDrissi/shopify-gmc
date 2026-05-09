-- Restore TOTP columns. Existing rows get NULL secrets, so admins will be
-- prompted to re-enroll on next login (matching the original 000015 contract).
ALTER TABLE platform_admins ADD COLUMN totp_secret      text;
ALTER TABLE platform_admins ADD COLUMN totp_enrolled_at timestamptz;
