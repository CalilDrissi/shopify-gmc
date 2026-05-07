DROP POLICY IF EXISTS job_queue_isolation           ON job_queue;
ALTER TABLE job_queue           NO FORCE ROW LEVEL SECURITY;
ALTER TABLE job_queue           DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS purchases_isolation           ON purchases;
ALTER TABLE purchases           NO FORCE ROW LEVEL SECURITY;
ALTER TABLE purchases           DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS usage_counters_isolation      ON usage_counters;
ALTER TABLE usage_counters      NO FORCE ROW LEVEL SECURITY;
ALTER TABLE usage_counters      DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS alert_subscriptions_isolation ON alert_subscriptions;
ALTER TABLE alert_subscriptions NO FORCE ROW LEVEL SECURITY;
ALTER TABLE alert_subscriptions DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS audit_diffs_isolation         ON audit_diffs;
ALTER TABLE audit_diffs         NO FORCE ROW LEVEL SECURITY;
ALTER TABLE audit_diffs         DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS issues_isolation              ON issues;
ALTER TABLE issues              NO FORCE ROW LEVEL SECURITY;
ALTER TABLE issues              DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS audits_isolation              ON audits;
ALTER TABLE audits              NO FORCE ROW LEVEL SECURITY;
ALTER TABLE audits              DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS gmc_snapshots_isolation       ON gmc_snapshots;
ALTER TABLE gmc_snapshots       NO FORCE ROW LEVEL SECURITY;
ALTER TABLE gmc_snapshots       DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS gmc_connections_isolation     ON gmc_connections;
ALTER TABLE gmc_connections     NO FORCE ROW LEVEL SECURITY;
ALTER TABLE gmc_connections     DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS stores_isolation              ON stores;
ALTER TABLE stores              NO FORCE ROW LEVEL SECURITY;
ALTER TABLE stores              DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS invitations_isolation         ON invitations;
ALTER TABLE invitations         NO FORCE ROW LEVEL SECURITY;
ALTER TABLE invitations         DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS memberships_isolation         ON memberships;
ALTER TABLE memberships         NO FORCE ROW LEVEL SECURITY;
ALTER TABLE memberships         DISABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenants_isolation             ON tenants;
ALTER TABLE tenants             NO FORCE ROW LEVEL SECURITY;
ALTER TABLE tenants             DISABLE ROW LEVEL SECURITY;

DROP FUNCTION IF EXISTS app_current_tenant_id();
