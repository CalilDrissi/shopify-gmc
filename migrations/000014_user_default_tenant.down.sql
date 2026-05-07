DROP INDEX IF EXISTS users_default_tenant_id_idx;
ALTER TABLE users DROP COLUMN IF EXISTS default_tenant_id;
