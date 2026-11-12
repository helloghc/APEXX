-- Orders sent to third-party warehouses (3PL) to be shipped: one request per
-- order and location, delivered signed with retries until accepted or refused.
CREATE TABLE fulfillment_requests (
    tenant_id    text NOT NULL REFERENCES tenants(id),
    id           text NOT NULL,
    order_id     text NOT NULL,
    location_id  text NOT NULL,
    kind         text NOT NULL DEFAULT 'fulfill' CHECK (kind IN ('fulfill','cancel')),
    status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','rejected','failed')),
    attempts     int NOT NULL DEFAULT 0,
    next_attempt timestamptz NOT NULL DEFAULT now(),
    reference    text NOT NULL DEFAULT '',   -- the warehouse's own id
    last_error   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, order_id, location_id, kind)
);
CREATE INDEX fulfillment_requests_due ON fulfillment_requests (next_attempt) WHERE status = 'pending';
ALTER TABLE fulfillment_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE fulfillment_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON fulfillment_requests USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
