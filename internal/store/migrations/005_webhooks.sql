-- Connectors: webhook subscriptions and deliveries with retries and DLQ.

CREATE TABLE webhook_subscriptions (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    url         text NOT NULL CHECK (url LIKE 'https://%' OR url LIKE 'http://%'),
    secret_enc  bytea NOT NULL,
    types       text[] NOT NULL DEFAULT '{}',   -- empty = every type
    active      boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE webhook_deliveries (
    tenant_id       text NOT NULL,
    subscription_id text NOT NULL,
    event_id        bigint NOT NULL,             -- outbox id (apexxsequence)
    type            text NOT NULL,
    subject         text NOT NULL,
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','delivered','dead')),
    attempts        int NOT NULL DEFAULT 0,
    next_attempt    timestamptz NOT NULL DEFAULT now(),
    last_status     int NOT NULL DEFAULT 0,
    last_error      text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    delivered_at    timestamptz,
    PRIMARY KEY (tenant_id, subscription_id, event_id),
    FOREIGN KEY (tenant_id, subscription_id) REFERENCES webhook_subscriptions(tenant_id, id)
);
CREATE INDEX webhook_deliveries_due ON webhook_deliveries (next_attempt) WHERE status = 'pending';

-- Position of the dispatcher in the outbox (single row).
CREATE TABLE dispatcher_cursor (
    name   text PRIMARY KEY,
    cursor bigint NOT NULL
);
INSERT INTO dispatcher_cursor (name, cursor) SELECT 'webhooks', COALESCE(max(id), 0) FROM outbox;

ALTER TABLE webhook_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_subscriptions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON webhook_subscriptions
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
ALTER TABLE webhook_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_deliveries FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON webhook_deliveries
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
