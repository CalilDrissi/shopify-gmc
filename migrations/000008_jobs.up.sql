CREATE TABLE job_queue (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    uuid        REFERENCES tenants(id) ON DELETE CASCADE,
    kind         text        NOT NULL,
    payload      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    status       job_status  NOT NULL DEFAULT 'queued',
    run_at       timestamptz NOT NULL DEFAULT now(),
    attempts     int         NOT NULL DEFAULT 0,
    max_attempts int         NOT NULL DEFAULT 5,
    last_error   text,
    locked_at    timestamptz,
    locked_by    text,
    finished_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX job_queue_ready_idx
    ON job_queue (run_at) WHERE status = 'queued';
CREATE INDEX job_queue_kind_idx ON job_queue (kind);
CREATE INDEX job_queue_tenant_id_idx ON job_queue (tenant_id) WHERE tenant_id IS NOT NULL;
