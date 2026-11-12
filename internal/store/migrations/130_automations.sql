-- Automations: "when X happens to an order, do Y". A run is queued in the
-- same transaction as the order event (never lost), carried out by a worker
-- with retries, and kept as history.
CREATE TABLE automations (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    trigger     text NOT NULL,
    conditions  jsonb NOT NULL DEFAULT '{}',
    actions     jsonb NOT NULL DEFAULT '[]',
    active      boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX automations_trigger ON automations (tenant_id, trigger) WHERE active;
ALTER TABLE automations ENABLE ROW LEVEL SECURITY;
ALTER TABLE automations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON automations USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

CREATE TABLE automation_runs (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    id            text NOT NULL,
    automation_id text NOT NULL,
    order_id      text NOT NULL,
    event         text NOT NULL,
    event_seq     bigint NOT NULL,
    status        text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','done','skipped','failed')),
    step          int NOT NULL DEFAULT 0,      -- actions already done (a retry resumes here)
    attempts      int NOT NULL DEFAULT 0,
    next_attempt  timestamptz NOT NULL DEFAULT now(),
    log           jsonb NOT NULL DEFAULT '[]',
    last_error    text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    finished_at   timestamptz,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, automation_id, order_id, event_seq)
);
CREATE INDEX automation_runs_due ON automation_runs (next_attempt) WHERE status = 'pending';
CREATE INDEX automation_runs_automation ON automation_runs (tenant_id, automation_id, created_at DESC);
ALTER TABLE automation_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_runs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON automation_runs USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
