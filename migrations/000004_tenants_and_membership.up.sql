CREATE TABLE tenants (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    name           text        NOT NULL,
    slug           citext      NOT NULL UNIQUE,
    kind           tenant_kind NOT NULL DEFAULT 'individual',
    plan           plan_tier   NOT NULL DEFAULT 'free',
    plan_renews_at timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE memberships (
    id         uuid            PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id  uuid            NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id    uuid            NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
    role       membership_role NOT NULL,
    created_at timestamptz     NOT NULL DEFAULT now(),
    updated_at timestamptz     NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, user_id)
);
CREATE UNIQUE INDEX memberships_one_owner_per_tenant
    ON memberships (tenant_id) WHERE role = 'owner';
CREATE INDEX memberships_user_id_idx ON memberships (user_id);

CREATE TABLE invitations (
    id          uuid              PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   uuid              NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    inviter_id  uuid              REFERENCES users(id) ON DELETE SET NULL,
    email       citext            NOT NULL,
    role        membership_role   NOT NULL DEFAULT 'member',
    token_hash  bytea             NOT NULL UNIQUE,
    status      invitation_status NOT NULL DEFAULT 'pending',
    expires_at  timestamptz       NOT NULL,
    accepted_at timestamptz,
    created_at  timestamptz       NOT NULL DEFAULT now()
);
CREATE INDEX invitations_tenant_id_idx ON invitations (tenant_id);
CREATE INDEX invitations_email_idx     ON invitations (email);
