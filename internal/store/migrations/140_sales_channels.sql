-- External sales channels (Mercado Libre, Amazon): the store's account with
-- its encrypted credentials, the products it lists there with what was last
-- sent, and the orders imported from them (once each).
ALTER TABLE orders DROP CONSTRAINT orders_source_check;
ALTER TABLE orders ADD CONSTRAINT orders_source_check CHECK (source IN ('checkout','draft','pos','marketplace'));

CREATE TABLE channel_accounts (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    provider      text NOT NULL CHECK (provider IN ('mercadolibre','amazon')),
    seller_id     text NOT NULL,
    creds         bytea NOT NULL,          -- encrypted; never returned by the API
    settings      jsonb NOT NULL DEFAULT '{}',
    status        text NOT NULL DEFAULT 'connected' CHECK (status IN ('connected','error','paused')),
    last_error    text NOT NULL DEFAULT '',
    orders_since  timestamptz NOT NULL DEFAULT now() - interval '1 day',
    connected_at  timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, provider)
);
ALTER TABLE channel_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE channel_accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON channel_accounts USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

CREATE TABLE channel_listings (
    tenant_id    text NOT NULL,
    provider     text NOT NULL,
    sku          text NOT NULL,
    external_id  text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','active','paused','error','closing','closed')),
    category     text NOT NULL DEFAULT '',  -- the channel's category or product type
    pushed_price bigint,                     -- what the channel has now
    pushed_qty   bigint,
    error        text NOT NULL DEFAULT '',
    attempts     int NOT NULL DEFAULT 0,
    next_try     timestamptz NOT NULL DEFAULT now(),
    synced_at    timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, provider, sku),
    FOREIGN KEY (tenant_id, provider) REFERENCES channel_accounts (tenant_id, provider) ON DELETE CASCADE
);
CREATE INDEX channel_listings_due ON channel_listings (next_try) WHERE status IN ('pending','active','error','closing');
ALTER TABLE channel_listings ENABLE ROW LEVEL SECURITY;
ALTER TABLE channel_listings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON channel_listings USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

CREATE TABLE channel_orders (
    tenant_id   text NOT NULL,
    provider    text NOT NULL,
    external_id text NOT NULL,
    order_id    text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, provider, external_id)
);
CREATE INDEX channel_orders_order ON channel_orders (tenant_id, order_id);
ALTER TABLE channel_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE channel_orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON channel_orders USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
