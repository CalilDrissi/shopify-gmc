ALTER TABLE users ADD COLUMN default_tenant_id uuid REFERENCES tenants(id) ON DELETE SET NULL;
CREATE INDEX users_default_tenant_id_idx ON users (default_tenant_id) WHERE default_tenant_id IS NOT NULL;
