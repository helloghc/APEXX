-- Carts of identified shoppers (email typed at checkout) that did not end in
-- an order. One live cart per email; contents are encrypted.

CREATE TABLE abandoned_carts (
    tenant_id          text NOT NULL REFERENCES tenants(id),
    id                 text NOT NULL,
    email_hash         bytea NOT NULL,
    customer_id        text,
    data               bytea NOT NULL,              -- encrypted JSON {email,name,items}
    items_count        int NOT NULL DEFAULT 0,
    total              bigint NOT NULL DEFAULT 0,
    currency           char(3) NOT NULL DEFAULT 'MXN',
    channel            text NOT NULL DEFAULT '',
    site               text NOT NULL DEFAULT 'main',
    status             text NOT NULL DEFAULT 'open' CHECK (status IN ('open','reminded','recovered','dismissed')),
    token_hash         bytea NOT NULL,
    reminded_at        timestamptz,
    recovered_order_id text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE UNIQUE INDEX abandoned_carts_live ON abandoned_carts (tenant_id, email_hash) WHERE status IN ('open','reminded');
CREATE INDEX abandoned_carts_list ON abandoned_carts (tenant_id, updated_at DESC, id);
CREATE INDEX abandoned_carts_due ON abandoned_carts (updated_at) WHERE status = 'open';
CREATE UNIQUE INDEX abandoned_carts_token ON abandoned_carts (tenant_id, token_hash);

ALTER TABLE abandoned_carts ENABLE ROW LEVEL SECURITY;
ALTER TABLE abandoned_carts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON abandoned_carts
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
