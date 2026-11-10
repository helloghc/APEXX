-- Orders, payments and the money ledger (phase 2).

CREATE TABLE orders (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    id            text NOT NULL,
    number        bigint NOT NULL,
    channel       text NOT NULL,
    status        text NOT NULL CHECK (status IN ('pending_payment','paid','fulfilled','cancelled','refunded')),
    currency      char(3) NOT NULL,
    items_total   bigint NOT NULL,
    discounts     bigint NOT NULL,
    shipping      bigint NOT NULL,
    tax_included  bigint NOT NULL,
    grand_total   bigint NOT NULL CHECK (grand_total >= 0),
    customer      bytea NOT NULL,           -- encrypted JSON (email, name, address)
    postal_code   text NOT NULL DEFAULT '',
    version       bigint NOT NULL DEFAULT 1,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, number),
    CHECK (grand_total = items_total - discounts + shipping)
);

CREATE TABLE order_lines (
    tenant_id   text NOT NULL,
    order_id    text NOT NULL,
    line        int NOT NULL,
    sku         text NOT NULL,
    title       text NOT NULL,
    qty         bigint NOT NULL CHECK (qty > 0),
    unit        bigint NOT NULL,
    total       bigint NOT NULL,
    PRIMARY KEY (tenant_id, order_id, line),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders(tenant_id, id)
);

-- Every state change of an order is an event: full, replayable history.
CREATE TABLE order_events (
    tenant_id   text NOT NULL,
    order_id    text NOT NULL,
    seq         int NOT NULL,
    at          timestamptz NOT NULL DEFAULT clock_timestamp(),
    actor       text NOT NULL,
    type        text NOT NULL,
    from_status text,
    to_status   text NOT NULL,
    data        jsonb NOT NULL DEFAULT '{}',
    PRIMARY KEY (tenant_id, order_id, seq),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders(tenant_id, id)
);

CREATE TABLE payments (
    tenant_id     text NOT NULL,
    id            text NOT NULL,
    order_id      text NOT NULL,
    provider      text NOT NULL,
    provider_ref  text NOT NULL DEFAULT '',
    status        text NOT NULL CHECK (status IN ('authorized','captured','failed','voided','refunded')),
    amount        bigint NOT NULL CHECK (amount >= 0),
    refunded      bigint NOT NULL DEFAULT 0 CHECK (refunded >= 0),
    currency      char(3) NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders(tenant_id, id),
    CHECK (refunded <= amount)
);

-- Double-entry ledger: each movement has entries that sum to zero.
CREATE TABLE ledger_entries (
    tenant_id   text NOT NULL,
    id          bigserial,
    tx_id       text NOT NULL,
    account     text NOT NULL,         -- e.g. customer_receivable, psp_clearing, revenue, refunds
    amount      bigint NOT NULL,       -- positive debit, negative credit
    currency    char(3) NOT NULL,
    order_id    text,
    at          timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX ledger_tx ON ledger_entries (tenant_id, tx_id);

-- Where each sold unit came from, so a cancellation restocks the exact location.
CREATE TABLE order_allocations (
    tenant_id   text NOT NULL,
    order_id    text NOT NULL,
    sku         text NOT NULL,
    location_id text NOT NULL,
    qty         bigint NOT NULL CHECK (qty > 0),
    PRIMARY KEY (tenant_id, order_id, sku, location_id),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders(tenant_id, id)
);

-- Invoices are issued asynchronously with retries (Stripe Invoicing; CFDI via a PAC connector).
CREATE TABLE invoices (
    tenant_id    text NOT NULL,
    order_id     text NOT NULL,
    provider     text NOT NULL,
    status       text NOT NULL CHECK (status IN ('pending','issued','failed','voided')),
    provider_ref text NOT NULL DEFAULT '',
    url          text NOT NULL DEFAULT '',
    attempts     int NOT NULL DEFAULT 0,
    last_error   text NOT NULL DEFAULT '',
    next_attempt timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, order_id, provider),
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders(tenant_id, id)
);

-- Each store brings its own Stripe account:
--   mode 'connect'  : linked through Stripe Connect OAuth; only the acct_ id is stored.
--   mode 'own_keys' : the merchant enters its own secret key; stored encrypted with the tenant key.
CREATE TABLE payment_accounts (
    tenant_id          text PRIMARY KEY REFERENCES tenants(id),
    provider           text NOT NULL DEFAULT 'stripe',
    mode               text NOT NULL CHECK (mode IN ('connect','own_keys')),
    account_id         text NOT NULL DEFAULT '',
    publishable_key    text NOT NULL DEFAULT '',
    secret_enc         bytea,
    webhook_secret_enc bytea,
    fee_bps            int NOT NULL DEFAULT 0 CHECK (fee_bps BETWEEN 0 AND 3000),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CHECK ((mode = 'connect' AND account_id LIKE 'acct_%') OR (mode = 'own_keys' AND secret_enc IS NOT NULL))
);

CREATE SEQUENCE order_numbers START 1000;

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['orders','order_lines','order_events','payments','ledger_entries','order_allocations','invoices','payment_accounts']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;

CREATE TRIGGER order_events_no_update BEFORE UPDATE OR DELETE ON order_events
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
CREATE TRIGGER ledger_no_update BEFORE UPDATE OR DELETE ON ledger_entries
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
