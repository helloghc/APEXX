-- First-party analytics, reports and operations (area "reports").
--
--   analytics_events          raw storefront events, append-only, kept 90 days
--   analytics_traffic_hourly  traffic counters per hour (UTC instants)
--   analytics_funnel_daily    sessions per funnel step per store day
--   analytics_product_traffic_daily, analytics_referrer_daily
--   analytics_orders          one fact row per order (no personal data)
--   analytics_sales_hourly / _daily, analytics_sku_daily, analytics_promo_daily
--
-- "Day" is always the store day in America/Mexico_City. No table stores an IP
-- address, a user agent or an e-mail: visitors are a keyed hash whose salt is
-- rotated and destroyed every day, customers are a keyed hash of the e-mail.

-- Daily salt for the visitor hash. RLS without policies: only the system role
-- (BYPASSRLS) can read it; the application role never sees a salt.
CREATE TABLE analytics_salts (
    day   date PRIMARY KEY,
    salt  bytea NOT NULL
);
ALTER TABLE analytics_salts ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_salts FORCE ROW LEVEL SECURITY;

-- Cursors of the analytics workers. System role only.
CREATE TABLE analytics_state (
    name   text PRIMARY KEY,
    at     timestamptz NOT NULL
);
ALTER TABLE analytics_state ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_state FORCE ROW LEVEL SECURITY;

CREATE TABLE analytics_events (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          bigserial,
    at          timestamptz NOT NULL DEFAULT clock_timestamp(),
    type        text NOT NULL CHECK (type IN ('page_view','product_view','add_to_cart','checkout_start','purchase')),
    visitor     bytea NOT NULL CHECK (octet_length(visitor) = 16),
    channel     text NOT NULL DEFAULT '',
    site        text NOT NULL DEFAULT '',
    path        text NOT NULL DEFAULT '' CHECK (char_length(path) <= 200),
    product_id  text NOT NULL DEFAULT '',
    sku         text NOT NULL DEFAULT '',
    qty         int NOT NULL DEFAULT 0 CHECK (qty >= 0),
    value       bigint NOT NULL DEFAULT 0 CHECK (value >= 0),
    order_id    text NOT NULL DEFAULT '',
    referrer    text NOT NULL DEFAULT '' CHECK (char_length(referrer) <= 100),
    device      text NOT NULL DEFAULT 'desktop' CHECK (device IN ('desktop','mobile','tablet')),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX analytics_events_at ON analytics_events (tenant_id, at);
CREATE INDEX analytics_events_time ON analytics_events (at);

-- Raw events are append-only; a row can only be deleted once it is older than
-- the 90-day retention window.
CREATE OR REPLACE FUNCTION analytics_events_guard() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'analytics_events is append-only';
    END IF;
    IF OLD.at > now() - interval '90 days' THEN
        RAISE EXCEPTION 'analytics_events are kept for 90 days';
    END IF;
    RETURN OLD;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER analytics_events_guard BEFORE UPDATE OR DELETE ON analytics_events
    FOR EACH ROW EXECUTE FUNCTION analytics_events_guard();

CREATE TABLE analytics_traffic_hourly (
    tenant_id       text NOT NULL REFERENCES tenants(id),
    hour            timestamptz NOT NULL,
    channel         text NOT NULL,
    sessions        bigint NOT NULL DEFAULT 0,   -- distinct visitors in the hour
    page_views      bigint NOT NULL DEFAULT 0,
    product_views   bigint NOT NULL DEFAULT 0,
    add_to_cart     bigint NOT NULL DEFAULT 0,
    checkout_starts bigint NOT NULL DEFAULT 0,
    purchases       bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, hour, channel)
);

-- A session is one visitor during one store day (the hash cannot be followed
-- across days). Steps are cumulative: a session that reached a later step
-- counts in every earlier one, so the funnel never grows downstream.
CREATE TABLE analytics_funnel_daily (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    day         date NOT NULL,
    channel     text NOT NULL,
    device      text NOT NULL,
    sessions    bigint NOT NULL DEFAULT 0,
    s_product   bigint NOT NULL DEFAULT 0,
    s_cart      bigint NOT NULL DEFAULT 0,
    s_checkout  bigint NOT NULL DEFAULT 0,
    s_purchase  bigint NOT NULL DEFAULT 0,
    page_views  bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, day, channel, device)
);

CREATE TABLE analytics_product_traffic_daily (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    day           date NOT NULL,
    product_id    text NOT NULL,
    views         bigint NOT NULL DEFAULT 0,
    visitors      bigint NOT NULL DEFAULT 0,
    add_to_cart   bigint NOT NULL DEFAULT 0,
    cart_visitors bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, day, product_id)
);

CREATE TABLE analytics_referrer_daily (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    day         date NOT NULL,
    referrer    text NOT NULL,                -- host only; '' = direct
    sessions    bigint NOT NULL DEFAULT 0,
    purchases   bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, day, referrer)
);

-- One row per order, rebuilt from orders + order_events + payments whenever
-- the order version changes. customer_hash = HMAC(tenant key, lower(email)).
CREATE TABLE analytics_orders (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    order_id      text NOT NULL,
    number        bigint NOT NULL,
    channel       text NOT NULL,
    currency      char(3) NOT NULL,
    status        text NOT NULL,
    src_version   bigint NOT NULL,
    created_at    timestamptz NOT NULL,
    paid_at       timestamptz,
    refunded_at   timestamptz,
    gross         bigint NOT NULL,
    discounts     bigint NOT NULL,
    shipping      bigint NOT NULL,
    tax           bigint NOT NULL,
    total         bigint NOT NULL,
    refunded      bigint NOT NULL DEFAULT 0,
    units         bigint NOT NULL DEFAULT 0,
    customer_hash bytea,
    customer_seq  int NOT NULL DEFAULT 0,      -- 1 = first paid order of that customer
    PRIMARY KEY (tenant_id, order_id)
);
CREATE INDEX analytics_orders_paid ON analytics_orders (tenant_id, paid_at) WHERE paid_at IS NOT NULL;
CREATE INDEX analytics_orders_refunded ON analytics_orders (tenant_id, refunded_at) WHERE refunded_at IS NOT NULL;
CREATE INDEX analytics_orders_customer ON analytics_orders (tenant_id, customer_hash, paid_at) WHERE paid_at IS NOT NULL;

CREATE TABLE analytics_order_promos (
    tenant_id     text NOT NULL,
    order_id      text NOT NULL,
    promotion_id  text NOT NULL,
    name          text NOT NULL DEFAULT '',
    amount        bigint NOT NULL,
    PRIMARY KEY (tenant_id, order_id, promotion_id),
    FOREIGN KEY (tenant_id, order_id) REFERENCES analytics_orders(tenant_id, order_id) ON DELETE CASCADE
);

-- Sales are counted in the bucket where the order was paid; refunds in the
-- bucket where the money went back.
CREATE TABLE analytics_sales_hourly (
    tenant_id           text NOT NULL REFERENCES tenants(id),
    hour                timestamptz NOT NULL,
    channel             text NOT NULL,
    currency            char(3) NOT NULL,
    orders              bigint NOT NULL DEFAULT 0,
    units               bigint NOT NULL DEFAULT 0,
    gross               bigint NOT NULL DEFAULT 0,
    discounts           bigint NOT NULL DEFAULT 0,
    shipping            bigint NOT NULL DEFAULT 0,
    tax                 bigint NOT NULL DEFAULT 0,
    refunds             bigint NOT NULL DEFAULT 0,
    refund_count        bigint NOT NULL DEFAULT 0,
    new_customers       bigint NOT NULL DEFAULT 0,
    returning_customers bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, hour, channel, currency)
);

CREATE TABLE analytics_sales_daily (
    tenant_id           text NOT NULL REFERENCES tenants(id),
    day                 date NOT NULL,
    channel             text NOT NULL,
    currency            char(3) NOT NULL,
    orders              bigint NOT NULL DEFAULT 0,
    units               bigint NOT NULL DEFAULT 0,
    gross               bigint NOT NULL DEFAULT 0,
    discounts           bigint NOT NULL DEFAULT 0,
    shipping            bigint NOT NULL DEFAULT 0,
    tax                 bigint NOT NULL DEFAULT 0,
    refunds             bigint NOT NULL DEFAULT 0,
    refund_count        bigint NOT NULL DEFAULT 0,
    new_customers       bigint NOT NULL DEFAULT 0,
    returning_customers bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, day, channel, currency)
);

CREATE TABLE analytics_sku_daily (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    day         date NOT NULL,
    channel     text NOT NULL,
    currency    char(3) NOT NULL,
    sku         text NOT NULL,
    title       text NOT NULL DEFAULT '',
    orders      bigint NOT NULL DEFAULT 0,
    units       bigint NOT NULL DEFAULT 0,
    revenue     bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, day, channel, currency, sku)
);

CREATE TABLE analytics_promo_daily (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    day           date NOT NULL,
    channel       text NOT NULL,
    currency      char(3) NOT NULL,
    promotion_id  text NOT NULL,
    name          text NOT NULL DEFAULT '',
    orders        bigint NOT NULL DEFAULT 0,
    discount      bigint NOT NULL DEFAULT 0,
    revenue       bigint NOT NULL DEFAULT 0,   -- order total of the orders that used it
    PRIMARY KEY (tenant_id, day, channel, currency, promotion_id)
);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['analytics_events','analytics_traffic_hourly','analytics_funnel_daily',
        'analytics_product_traffic_daily','analytics_referrer_daily','analytics_orders','analytics_order_promos',
        'analytics_sales_hourly','analytics_sales_daily','analytics_sku_daily','analytics_promo_daily']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;

-- Indexes the analytics sync, the home alerts and the activity viewer need on
-- tables of other areas.
CREATE INDEX IF NOT EXISTS orders_updated_at ON orders (updated_at);
CREATE INDEX IF NOT EXISTS orders_pending_payment ON orders (tenant_id, created_at) WHERE status = 'pending_payment';
CREATE INDEX IF NOT EXISTS payments_order ON payments (tenant_id, order_id);
CREATE INDEX IF NOT EXISTS audit_log_at ON audit_log (tenant_id, at);
CREATE INDEX IF NOT EXISTS webhook_deliveries_dead ON webhook_deliveries (tenant_id, created_at) WHERE status = 'dead';
