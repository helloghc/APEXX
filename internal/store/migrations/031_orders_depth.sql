-- Orders after the sale: partial fulfilments with tracking, partial refunds,
-- returns (RMA), exchanges, order edits, notes, saved views and draft orders.

ALTER TABLE orders ADD COLUMN fulfillment_status text NOT NULL DEFAULT 'unfulfilled'
    CHECK (fulfillment_status IN ('unfulfilled','partial','fulfilled','delivered'));
ALTER TABLE orders ADD COLUMN payment_status text NOT NULL DEFAULT 'pending'
    CHECK (payment_status IN ('pending','paid','balance_due','partially_refunded','refunded','voided'));
-- Value given back to the customer (money refunds and exchange credit).
ALTER TABLE orders ADD COLUMN refunded_total bigint NOT NULL DEFAULT 0 CHECK (refunded_total >= 0);
ALTER TABLE orders ADD COLUMN refunded_tax bigint NOT NULL DEFAULT 0 CHECK (refunded_tax >= 0);
ALTER TABLE orders ADD COLUMN source text NOT NULL DEFAULT 'checkout' CHECK (source IN ('checkout','draft'));

ALTER TABLE order_lines ADD COLUMN fulfilled_qty bigint NOT NULL DEFAULT 0 CHECK (fulfilled_qty >= 0);
ALTER TABLE order_lines ADD COLUMN returned_qty bigint NOT NULL DEFAULT 0 CHECK (returned_qty >= 0);
ALTER TABLE order_lines ADD COLUMN refunded_qty bigint NOT NULL DEFAULT 0 CHECK (refunded_qty >= 0);
ALTER TABLE order_lines ADD COLUMN origin text NOT NULL DEFAULT 'checkout' CHECK (origin IN ('checkout','edit','exchange','draft'));

-- Existing orders: derive the new columns from the old single status.
UPDATE order_lines l SET fulfilled_qty = l.qty FROM orders o
    WHERE o.tenant_id = l.tenant_id AND o.id = l.order_id AND o.status = 'fulfilled';
ALTER TABLE order_lines ADD CONSTRAINT order_lines_qty_bounds CHECK (fulfilled_qty <= qty AND returned_qty <= fulfilled_qty);
UPDATE orders SET
    fulfillment_status = CASE WHEN status = 'fulfilled' THEN 'fulfilled' ELSE 'unfulfilled' END,
    payment_status = CASE status WHEN 'pending_payment' THEN 'pending' WHEN 'cancelled' THEN 'voided'
        WHEN 'refunded' THEN 'refunded' ELSE 'paid' END,
    refunded_total = CASE WHEN status = 'refunded' THEN grand_total ELSE 0 END,
    refunded_tax = CASE WHEN status = 'refunded' THEN tax_included ELSE 0 END;

CREATE INDEX orders_created ON orders (tenant_id, created_at DESC);
CREATE INDEX orders_filters ON orders (tenant_id, status, payment_status, fulfillment_status);

CREATE TABLE fulfillments (
    tenant_id       text NOT NULL,
    id              text NOT NULL,
    order_id        text NOT NULL,
    number          int NOT NULL,
    status          text NOT NULL CHECK (status IN ('pending','shipped','delivered','cancelled')),
    carrier         text NOT NULL DEFAULT '',
    tracking_number text NOT NULL DEFAULT '',
    tracking_url    text NOT NULL DEFAULT '',
    note            text NOT NULL DEFAULT '',
    created_by      text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    shipped_at      timestamptz,
    delivered_at    timestamptz,
    cancelled_at    timestamptz,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, order_id, number),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders(tenant_id, id)
);

CREATE TABLE fulfillment_items (
    tenant_id      text NOT NULL,
    fulfillment_id text NOT NULL,
    line           int NOT NULL,
    sku            text NOT NULL,
    title          text NOT NULL DEFAULT '',
    qty            bigint NOT NULL CHECK (qty > 0),
    PRIMARY KEY (tenant_id, fulfillment_id, line),
    FOREIGN KEY (tenant_id, fulfillment_id) REFERENCES fulfillments(tenant_id, id)
);

-- kind: refund (money back), credit (value applied to an exchange, no money
-- moves), adjustment (overpayment returned after an order edit), cancellation.
CREATE TABLE refunds (
    tenant_id       text NOT NULL,
    id              text NOT NULL,
    order_id        text NOT NULL,
    kind            text NOT NULL CHECK (kind IN ('refund','credit','adjustment','cancellation')),
    status          text NOT NULL CHECK (status IN ('pending','succeeded','failed')),
    amount          bigint NOT NULL CHECK (amount > 0),
    tax_amount      bigint NOT NULL DEFAULT 0 CHECK (tax_amount >= 0),
    shipping_amount bigint NOT NULL DEFAULT 0 CHECK (shipping_amount >= 0),
    currency        char(3) NOT NULL,
    reason          text NOT NULL DEFAULT '',
    items           jsonb NOT NULL DEFAULT '[]',
    parts           jsonb NOT NULL DEFAULT '[]',   -- how the amount was split across payments
    restock         boolean NOT NULL DEFAULT false,
    notify          boolean NOT NULL DEFAULT true,
    return_id       text,
    idem_key        text,
    attempts        int NOT NULL DEFAULT 0,
    last_error      text NOT NULL DEFAULT '',
    created_by      text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    completed_at    timestamptz,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders(tenant_id, id)
);
CREATE UNIQUE INDEX refunds_idem ON refunds (tenant_id, idem_key) WHERE idem_key IS NOT NULL;
CREATE INDEX refunds_order ON refunds (tenant_id, order_id);
CREATE INDEX refunds_pending ON refunds (created_at) WHERE status = 'pending';

CREATE SEQUENCE return_numbers START 5000;
CREATE TABLE returns (
    tenant_id       text NOT NULL,
    id              text NOT NULL,
    order_id        text NOT NULL,
    number          bigint NOT NULL,
    kind            text NOT NULL CHECK (kind IN ('return','exchange')),
    status          text NOT NULL CHECK (status IN ('requested','received','completed','cancelled')),
    note            text NOT NULL DEFAULT '',
    refund_shipping bigint NOT NULL DEFAULT 0 CHECK (refund_shipping >= 0),
    credit          bigint NOT NULL DEFAULT 0,      -- value of the returned items
    credit_applied  bigint NOT NULL DEFAULT 0,      -- part used to pay exchange items
    refund_amount   bigint NOT NULL DEFAULT 0,      -- money refunded on receipt
    refund_id       text,
    new_items       jsonb NOT NULL DEFAULT '[]',    -- exchange: items sent to the customer
    created_by      text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    received_at     timestamptz,
    closed_at       timestamptz,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, number),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders(tenant_id, id)
);
CREATE INDEX returns_order ON returns (tenant_id, order_id);
CREATE INDEX returns_status ON returns (tenant_id, status, number DESC);

CREATE TABLE return_items (
    tenant_id        text NOT NULL,
    return_id        text NOT NULL,
    line             int NOT NULL,
    sku              text NOT NULL,
    title            text NOT NULL,
    qty              bigint NOT NULL CHECK (qty > 0),
    reason_code      text NOT NULL CHECK (reason_code IN ('size','defective','not_as_described','wrong_item','changed_mind','arrived_late','other')),
    received_qty     bigint NOT NULL DEFAULT 0 CHECK (received_qty >= 0),
    restock          boolean,
    restock_location text NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, return_id, line),
    FOREIGN KEY (tenant_id, return_id) REFERENCES returns(tenant_id, id)
);

CREATE TABLE order_notes (
    tenant_id   text NOT NULL,
    id          text NOT NULL,
    order_id    text NOT NULL,
    visibility  text NOT NULL CHECK (visibility IN ('internal','customer')),
    body        bytea NOT NULL,                   -- encrypted text
    author      text NOT NULL,
    author_name text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders(tenant_id, id)
);
CREATE INDEX order_notes_order ON order_notes (tenant_id, order_id, created_at);

CREATE TABLE order_edits (
    tenant_id   text NOT NULL,
    id          text NOT NULL,
    order_id    text NOT NULL,
    changes     jsonb NOT NULL,
    before      jsonb NOT NULL,
    after       jsonb NOT NULL,
    difference  bigint NOT NULL,
    reason      text NOT NULL DEFAULT '',
    created_by  text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders(tenant_id, id)
);

CREATE TABLE order_views (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
    filters     jsonb NOT NULL,
    created_by  text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, name)
);

CREATE SEQUENCE draft_numbers START 100;
CREATE TABLE draft_orders (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    number      bigint NOT NULL,
    status      text NOT NULL CHECK (status IN ('open','sent','completed','cancelled')),
    channel     text NOT NULL,
    currency    char(3) NOT NULL,
    items       jsonb NOT NULL,
    customer    bytea NOT NULL,                   -- encrypted JSON (email, name, phone, address)
    postal_code text NOT NULL DEFAULT '',
    items_total bigint NOT NULL CHECK (items_total >= 0),
    discount    bigint NOT NULL DEFAULT 0 CHECK (discount >= 0),
    shipping    bigint NOT NULL DEFAULT 0 CHECK (shipping >= 0),
    grand_total bigint NOT NULL CHECK (grand_total >= 0),
    note        text NOT NULL DEFAULT '',
    order_id    text,
    created_by  text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    sent_at     timestamptz,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, number),
    CHECK (grand_total = items_total - discount + shipping)
);

-- Payment links: a draft order or the balance of an edited order, paid by the
-- customer on a hosted page. Only the hash of the token is stored.
CREATE TABLE payment_links (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    token_hash  bytea NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('draft','balance')),
    ref_id      text NOT NULL,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, token_hash)
);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['fulfillments','fulfillment_items','refunds','returns','return_items','order_notes','order_edits','order_views','draft_orders','payment_links']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;
