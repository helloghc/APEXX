-- Recurring purchases: one row per subscription, renewed by a worker.

CREATE TABLE subscriptions (
    tenant_id        text NOT NULL REFERENCES tenants(id),
    id               text NOT NULL,
    customer_id      text,
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','past_due','cancelled')),
    plan             text NOT NULL,
    discount_bps     bigint NOT NULL DEFAULT 0,
    items            jsonb NOT NULL,               -- [{sku, qty, title}]
    channel          text NOT NULL,
    postal_code      text NOT NULL DEFAULT '',
    shipping_option  text NOT NULL DEFAULT '',
    customer         bytea NOT NULL,               -- encrypted contact and address
    payment_method   text NOT NULL DEFAULT '',     -- provider token of the saved card (not the card number)
    payment_customer text NOT NULL DEFAULT '',
    token_hash       bytea NOT NULL,               -- manage link sent to the customer
    next_at          timestamptz NOT NULL,
    cycle            integer NOT NULL DEFAULT 1,   -- deliveries made so far
    failures         integer NOT NULL DEFAULT 0,
    last_error       text NOT NULL DEFAULT '',
    first_order_id   text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    cancelled_at     timestamptz,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, token_hash)
);
CREATE INDEX subscriptions_due ON subscriptions (next_at) WHERE status = 'active';
CREATE INDEX subscriptions_recent ON subscriptions (tenant_id, created_at DESC);

CREATE TABLE subscription_orders (
    tenant_id       text NOT NULL REFERENCES tenants(id),
    subscription_id text NOT NULL,
    order_id        text NOT NULL,
    cycle           integer NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, subscription_id, order_id),
    FOREIGN KEY (tenant_id, subscription_id) REFERENCES subscriptions(tenant_id, id)
);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['subscriptions','subscription_orders']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;
