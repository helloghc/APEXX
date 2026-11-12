-- Bulk operations: thousands of stock or price changes in one job, applied
-- in chunks with visible progress and an error per bad line.
CREATE TABLE bulk_jobs (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('stock','prices')),
    channel     text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','done','failed')),
    total       int NOT NULL,
    processed   int NOT NULL DEFAULT 0,
    failed      int NOT NULL DEFAULT 0,
    applied     int NOT NULL DEFAULT 0,  -- valid lines written so far (a restart resumes here)
    errors      jsonb NOT NULL DEFAULT '[]',
    items       jsonb NOT NULL,
    created_by  text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    started_at  timestamptz,
    finished_at timestamptz,
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX bulk_jobs_queued ON bulk_jobs (created_at) WHERE status IN ('queued','running');
ALTER TABLE bulk_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE bulk_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON bulk_jobs USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
