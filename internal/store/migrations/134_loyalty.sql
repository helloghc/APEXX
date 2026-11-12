-- Loyalty points: every movement is an entry (earn, adjust when an order is
-- refunded or cancelled, redeem into store credit, manual); the balance is
-- their sum and the money value is booked in the ledger too.
CREATE TABLE loyalty_entries (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    customer_id text NOT NULL,
    order_id    text,
    kind        text NOT NULL CHECK (kind IN ('earn','adjust','redeem','manual')),
    points      bigint NOT NULL,
    value       bigint NOT NULL DEFAULT 0,   -- money value at the program's point value (cents)
    note        text NOT NULL DEFAULT '',
    card_id     text,                          -- store credit created by a redemption
    actor       text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX loyalty_customer ON loyalty_entries (tenant_id, customer_id, created_at DESC);
CREATE INDEX loyalty_order ON loyalty_entries (tenant_id, order_id) WHERE order_id IS NOT NULL;
ALTER TABLE loyalty_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE loyalty_entries FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON loyalty_entries USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
