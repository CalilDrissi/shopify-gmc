DROP INDEX IF EXISTS issues_source_idx;
ALTER TABLE issues DROP COLUMN IF EXISTS external_issue_code;
ALTER TABLE issues DROP COLUMN IF EXISTS source;

DROP INDEX IF EXISTS gmc_product_statuses_audit_idx;
ALTER TABLE gmc_product_statuses DROP COLUMN IF EXISTS audit_id;

DROP INDEX IF EXISTS gmc_account_snapshots_audit_idx;
ALTER TABLE gmc_account_snapshots DROP COLUMN IF EXISTS datafeed_errors;
ALTER TABLE gmc_account_snapshots DROP COLUMN IF EXISTS suspensions_count;
ALTER TABLE gmc_account_snapshots DROP COLUMN IF EXISTS warnings_count;
ALTER TABLE gmc_account_snapshots DROP COLUMN IF EXISTS website_claimed;
ALTER TABLE gmc_account_snapshots DROP COLUMN IF EXISTS account_status;
ALTER TABLE gmc_account_snapshots DROP COLUMN IF EXISTS audit_id;

ALTER TABLE store_gmc_connections DROP COLUMN IF EXISTS scope;
ALTER TABLE store_gmc_connections DROP COLUMN IF EXISTS website_claimed;
ALTER TABLE store_gmc_connections DROP COLUMN IF EXISTS suspensions_count;
ALTER TABLE store_gmc_connections DROP COLUMN IF EXISTS warnings_count;
ALTER TABLE store_gmc_connections DROP COLUMN IF EXISTS account_status;
ALTER TABLE store_gmc_connections DROP COLUMN IF EXISTS revoked_at;
ALTER TABLE store_gmc_connections DROP COLUMN IF EXISTS last_error_message;
ALTER TABLE store_gmc_connections DROP COLUMN IF EXISTS last_sync_status;
ALTER TABLE store_gmc_connections DROP COLUMN IF EXISTS last_sync_at;
