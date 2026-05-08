-- store_gmc_connections — add the columns the OAuth + sync code paths need.
-- Existing schema already has tenant_id, store_id, merchant_id, account_email,
-- access_token_encrypted/refresh_token_encrypted/token_nonce/token_expires_at,
-- and the gmc_connection_status enum (active/expired/revoked/error).

ALTER TABLE store_gmc_connections ADD COLUMN last_sync_at        timestamptz;
ALTER TABLE store_gmc_connections ADD COLUMN last_sync_status    text;
ALTER TABLE store_gmc_connections ADD COLUMN last_error_message  text;
ALTER TABLE store_gmc_connections ADD COLUMN revoked_at          timestamptz;
-- Cached top-level health flags so the store-detail card doesn't have to
-- decode the latest snapshot blob on every page load. Updated whenever a
-- successful sync completes.
ALTER TABLE store_gmc_connections ADD COLUMN account_status     text;
ALTER TABLE store_gmc_connections ADD COLUMN warnings_count     integer NOT NULL DEFAULT 0;
ALTER TABLE store_gmc_connections ADD COLUMN suspensions_count  integer NOT NULL DEFAULT 0;
ALTER TABLE store_gmc_connections ADD COLUMN website_claimed    boolean;
ALTER TABLE store_gmc_connections ADD COLUMN scope              text;

-- Snapshots are now per-audit so the report can show "the GMC state at the
-- moment this audit ran". The pre-existing gmc_account_snapshots row keeps
-- captured_at + raw_data; we just attach the audit_id and a few flat columns
-- so checks can read them without parsing raw_data.
ALTER TABLE gmc_account_snapshots ADD COLUMN audit_id           uuid REFERENCES audits(id) ON DELETE CASCADE;
ALTER TABLE gmc_account_snapshots ADD COLUMN account_status     text;
ALTER TABLE gmc_account_snapshots ADD COLUMN website_claimed    boolean;
ALTER TABLE gmc_account_snapshots ADD COLUMN warnings_count     integer NOT NULL DEFAULT 0;
ALTER TABLE gmc_account_snapshots ADD COLUMN suspensions_count  integer NOT NULL DEFAULT 0;
ALTER TABLE gmc_account_snapshots ADD COLUMN datafeed_errors    jsonb;
CREATE INDEX gmc_account_snapshots_audit_idx ON gmc_account_snapshots (audit_id) WHERE audit_id IS NOT NULL;

-- The product-status table already has issues + destination_statuses; just
-- attach audit_id for the per-audit join from checks.
ALTER TABLE gmc_product_statuses ADD COLUMN audit_id uuid REFERENCES audits(id) ON DELETE CASCADE;
CREATE INDEX gmc_product_statuses_audit_idx ON gmc_product_statuses (audit_id) WHERE audit_id IS NOT NULL;

-- issues table — add the source + external_issue_code columns the GMC-native
-- checks populate so the report can distinguish crawler-found issues from
-- Google-API-reported ones, and the email/UI can deep-link to Google's docs.
ALTER TABLE issues ADD COLUMN source              text NOT NULL DEFAULT 'crawler';
ALTER TABLE issues ADD COLUMN external_issue_code text;
CREATE INDEX issues_source_idx ON issues (audit_id, source);
