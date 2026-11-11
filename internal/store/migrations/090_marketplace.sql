-- Marketplace: sellers that offer their products in the store, their
-- commission, and a statement of what the store owes them.

CREATE TABLE sellers (
    tenant_id      text NOT NULL REFERENCES tenants(id),
    id             text NOT NULL,
    name           text NOT NULL,
    email          text NOT NULL DEFAULT '',
    commission_bps bigint NOT NULL DEFAULT 1500 CHECK (commission_bps BETWEEN 0 AND 10000),
    status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused')),
    note           text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

-- A product belongs to one seller, or to the store itself (no row).
CREATE TABLE seller_products (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    product_id text NOT NULL,
    seller_id  text NOT NULL,
    PRIMARY KEY (tenant_id, product_id),
    FOREIGN KEY (tenant_id, seller_id) REFERENCES sellers(tenant_id, id)
);
CREATE INDEX seller_products_seller ON seller_products (tenant_id, seller_id);

-- The statement. net is what the seller earns (gross - commission); the
-- balance owed is the sum of net. Sales and their reversals carry the order.
CREATE TABLE seller_entries (
    tenant_id      text NOT NULL REFERENCES tenants(id),
    id             text NOT NULL,
    seller_id      text NOT NULL,
    kind           text NOT NULL CHECK (kind IN ('sale','refund','payout','adjust')),
    order_id       text,
    line           integer,
    commission_bps bigint NOT NULL DEFAULT 0,
    gross          bigint NOT NULL DEFAULT 0,
    commission     bigint NOT NULL DEFAULT 0,
    net            bigint NOT NULL,
    note           text NOT NULL DEFAULT '',
    actor          text NOT NULL DEFAULT 'system',
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, seller_id) REFERENCES sellers(tenant_id, id)
);
CREATE INDEX seller_entries_seller ON seller_entries (tenant_id, seller_id, created_at DESC);
CREATE INDEX seller_entries_order ON seller_entries (tenant_id, order_id, line) WHERE order_id IS NOT NULL;

-- A team member or an invitation can be tied to a seller: that person only
-- sees the seller portal.
ALTER TABLE members ADD COLUMN seller_id text;
ALTER TABLE invitations ADD COLUMN seller_id text;

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['sellers','seller_products','seller_entries']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;
