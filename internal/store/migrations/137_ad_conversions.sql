-- Purchases reported to ad platforms from the server (Meta Conversions API,
-- Google Analytics 4), only when the shopper accepted advertising.
ALTER TABLE orders ADD COLUMN ads_consent boolean NOT NULL DEFAULT false;

CREATE TABLE conversion_jobs (
    tenant_id    text NOT NULL REFERENCES tenants(id),
    id           text NOT NULL,
    order_id     text NOT NULL,
    platform     text NOT NULL CHECK (platform IN ('meta','ga4')),
    status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent','failed')),
    attempts     int NOT NULL DEFAULT 0,
    next_attempt timestamptz NOT NULL DEFAULT now(),
    last_error   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    sent_at      timestamptz,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, order_id, platform)
);
CREATE INDEX conversion_jobs_due ON conversion_jobs (next_attempt) WHERE status = 'pending';
ALTER TABLE conversion_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversion_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON conversion_jobs USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
