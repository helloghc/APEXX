-- Sandbox quotas by plan, several sandboxes per store, an optional password
-- and scheduled publishes (docs/sandbox.md).

-- A store can have as many sandboxes as its plan allows (one by default).
ALTER TABLE sandboxes DROP CONSTRAINT sandboxes_parent_id_key;
ALTER TABLE sandboxes ADD COLUMN name text NOT NULL DEFAULT 'Sandbox' CHECK (length(name) BETWEEN 1 AND 60);
-- Optional storefront password (a hash; empty: no password).
ALTER TABLE sandboxes ADD COLUMN password_hash text NOT NULL DEFAULT '';
CREATE INDEX sandboxes_parent ON sandboxes (parent_id, created_at);
CREATE UNIQUE INDEX sandboxes_parent_name ON sandboxes (parent_id, lower(name));

-- tenant_plans: what the store's plan includes, set by the platform
-- operator (`apexx tenant plan`), never by the store. No row: the defaults
-- (one sandbox, no size limit, test data allowed).
CREATE TABLE tenant_plans (
    tenant_id             text PRIMARY KEY REFERENCES tenants(id),
    plan                  text NOT NULL DEFAULT '',
    sandboxes             integer NOT NULL DEFAULT 1 CHECK (sandboxes BETWEEN 0 AND 50),
    sandbox_max_products  integer NOT NULL DEFAULT 0 CHECK (sandbox_max_products >= 0), -- 0: no limit
    sandbox_demo_data     boolean NOT NULL DEFAULT true,
    sandbox_schedule      boolean NOT NULL DEFAULT true,
    updated_by            text NOT NULL DEFAULT '',
    updated_at            timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE tenant_plans ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_plans FORCE ROW LEVEL SECURITY;

-- sandbox_schedules: a publish to production set for a later time. What
-- travels is the sandbox as it is when it runs.
CREATE TABLE sandbox_schedules (
    id          text PRIMARY KEY,
    parent_id   text NOT NULL REFERENCES tenants(id),
    sandbox_id  text NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
    areas       jsonb NOT NULL,
    products    jsonb NOT NULL DEFAULT '[]',
    force       boolean NOT NULL DEFAULT false,
    run_at      timestamptz NOT NULL,
    status      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'running', 'done', 'failed', 'canceled')),
    publish_id  text NOT NULL DEFAULT '',
    result      text NOT NULL DEFAULT '',
    created_by  text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    ran_at      timestamptz
);
CREATE INDEX sandbox_schedules_due ON sandbox_schedules (run_at) WHERE status = 'pending';
CREATE INDEX sandbox_schedules_parent ON sandbox_schedules (parent_id, run_at DESC);
ALTER TABLE sandbox_schedules ENABLE ROW LEVEL SECURITY;
ALTER TABLE sandbox_schedules FORCE ROW LEVEL SECURITY;
