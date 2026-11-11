-- Gift cards and store credit, instalment plans and risk review.

CREATE TABLE gift_cards (
    tenant_id      text NOT NULL REFERENCES tenants(id),
    id             text NOT NULL,
    code_hash      bytea NOT NULL,            -- keyed hash of the code; the code itself is never stored
    last4          text NOT NULL,
    kind           text NOT NULL DEFAULT 'gift' CHECK (kind IN ('gift','credit')),
    initial        bigint NOT NULL CHECK (initial >= 0),
    balance        bigint NOT NULL CHECK (balance >= 0),
    currency       char(3) NOT NULL,
    status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    customer_id    text,
    recipient_name text NOT NULL DEFAULT '',
    note           text NOT NULL DEFAULT '',
    expires_at     timestamptz,
    created_by     text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, code_hash)
);
CREATE INDEX gift_cards_recent ON gift_cards (tenant_id, created_at DESC);

-- Every movement of a card. The balance is always the sum of its movements.
CREATE TABLE gift_card_tx (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    id         text NOT NULL,
    card_id    text NOT NULL,
    kind       text NOT NULL CHECK (kind IN ('issue','redeem','release','refund','adjust')),
    amount     bigint NOT NULL,              -- positive adds balance, negative spends it
    order_id   text,
    actor      text NOT NULL,
    note       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, card_id) REFERENCES gift_cards(tenant_id, id)
);
CREATE INDEX gift_card_tx_card ON gift_card_tx (tenant_id, card_id, created_at);
CREATE INDEX gift_card_tx_order ON gift_card_tx (tenant_id, order_id) WHERE order_id IS NOT NULL;

-- Risk review and instalments chosen at checkout.
ALTER TABLE orders ADD COLUMN risk jsonb NOT NULL DEFAULT '{}';
ALTER TABLE orders ADD COLUMN installments integer NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN ip_hash bytea;
CREATE INDEX orders_ip_recent ON orders (tenant_id, ip_hash, created_at DESC) WHERE ip_hash IS NOT NULL;

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['gift_cards','gift_card_tx']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;
