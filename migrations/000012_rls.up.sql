CREATE OR REPLACE FUNCTION app_current_tenant_id() RETURNS uuid AS $$
    SELECT NULLIF(current_setting('app.current_tenant_id', true), '')::uuid;
$$ LANGUAGE sql STABLE;

ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE  ROW LEVEL SECURITY;
CREATE POLICY tenants_isolation ON tenants
    USING (id = app_current_tenant_id())
    WITH CHECK (id = app_current_tenant_id());

ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE memberships FORCE  ROW LEVEL SECURITY;
CREATE POLICY memberships_isolation ON memberships
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE invitations FORCE  ROW LEVEL SECURITY;
CREATE POLICY invitations_isolation ON invitations
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE stores ENABLE ROW LEVEL SECURITY;
ALTER TABLE stores FORCE  ROW LEVEL SECURITY;
CREATE POLICY stores_isolation ON stores
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE gmc_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE gmc_connections FORCE  ROW LEVEL SECURITY;
CREATE POLICY gmc_connections_isolation ON gmc_connections
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE gmc_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE gmc_snapshots FORCE  ROW LEVEL SECURITY;
CREATE POLICY gmc_snapshots_isolation ON gmc_snapshots
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE audits ENABLE ROW LEVEL SECURITY;
ALTER TABLE audits FORCE  ROW LEVEL SECURITY;
CREATE POLICY audits_isolation ON audits
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE issues ENABLE ROW LEVEL SECURITY;
ALTER TABLE issues FORCE  ROW LEVEL SECURITY;
CREATE POLICY issues_isolation ON issues
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE audit_diffs ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_diffs FORCE  ROW LEVEL SECURITY;
CREATE POLICY audit_diffs_isolation ON audit_diffs
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE alert_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE alert_subscriptions FORCE  ROW LEVEL SECURITY;
CREATE POLICY alert_subscriptions_isolation ON alert_subscriptions
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE usage_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE usage_counters FORCE  ROW LEVEL SECURITY;
CREATE POLICY usage_counters_isolation ON usage_counters
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE purchases ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchases FORCE  ROW LEVEL SECURITY;
CREATE POLICY purchases_isolation ON purchases
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());

ALTER TABLE job_queue ENABLE ROW LEVEL SECURITY;
ALTER TABLE job_queue FORCE  ROW LEVEL SECURITY;
CREATE POLICY job_queue_isolation ON job_queue
    USING (tenant_id = app_current_tenant_id())
    WITH CHECK (tenant_id = app_current_tenant_id());
