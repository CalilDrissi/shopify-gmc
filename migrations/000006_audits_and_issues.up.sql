CREATE TABLE audits (
    id            uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     uuid         NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    store_id      uuid         NOT NULL REFERENCES stores(id)  ON DELETE CASCADE,
    triggered_by  uuid         REFERENCES users(id) ON DELETE SET NULL,
    trigger       text         NOT NULL,
    status        audit_status NOT NULL DEFAULT 'queued',
    started_at    timestamptz,
    finished_at   timestamptz,
    product_count int,
    issue_counts  jsonb        NOT NULL DEFAULT '{}'::jsonb,
    error_message text,
    created_at    timestamptz  NOT NULL DEFAULT now(),
    updated_at    timestamptz  NOT NULL DEFAULT now()
);
CREATE INDEX audits_store_created_idx ON audits (store_id, created_at DESC);
CREATE INDEX audits_status_idx        ON audits (status);

CREATE TABLE issues (
    id               uuid           PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        uuid           NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    audit_id         uuid           NOT NULL REFERENCES audits(id)  ON DELETE CASCADE,
    store_id         uuid           NOT NULL REFERENCES stores(id)  ON DELETE CASCADE,
    product_id       text,
    product_title    text,
    rule_code        text           NOT NULL,
    severity         issue_severity NOT NULL,
    status           issue_status   NOT NULL DEFAULT 'open',
    title            text           NOT NULL,
    description      text,
    fix_instructions text,
    fix_payload      jsonb,
    resolved_at      timestamptz,
    created_at       timestamptz    NOT NULL DEFAULT now(),
    updated_at       timestamptz    NOT NULL DEFAULT now()
);
CREATE INDEX issues_audit_id_idx       ON issues (audit_id);
CREATE INDEX issues_store_severity_idx ON issues (store_id, severity);
CREATE INDEX issues_rule_code_idx      ON issues (rule_code);
CREATE INDEX issues_open_per_store_idx ON issues (store_id) WHERE status = 'open';

CREATE TABLE audit_diffs (
    id                   uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            uuid        NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    audit_id             uuid        NOT NULL UNIQUE REFERENCES audits(id) ON DELETE CASCADE,
    previous_audit_id    uuid        REFERENCES audits(id) ON DELETE SET NULL,
    new_issue_count      int         NOT NULL DEFAULT 0,
    resolved_issue_count int         NOT NULL DEFAULT 0,
    diff                 jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at           timestamptz NOT NULL DEFAULT now()
);
