-- Negotiable quotes: a buyer asks for a price, the store answers with an
-- offer (a draft order with its prices and an expiry), the buyer accepts,
-- rejects or asks for changes. Every step is kept in the history.
CREATE TABLE quotes (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    number      bigint NOT NULL,
    status      text NOT NULL DEFAULT 'requested' CHECK (status IN ('requested','offered','accepted','rejected','expired','cancelled')),
    channel     text NOT NULL DEFAULT 'mx',
    company_id  text,
    customer    bytea NOT NULL,          -- encrypted: email, name, phone
    email_hash  bytea NOT NULL,
    items       jsonb NOT NULL,          -- what the buyer asked for: [{sku, qty}]
    draft_id    text,                    -- the offer, as a draft order with its prices
    order_id    text,
    expires_at  timestamptz,
    token_hash  bytea NOT NULL,          -- the buyer's link; only the hash is stored
    history     jsonb NOT NULL DEFAULT '[]',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, number)
);
CREATE INDEX quotes_status ON quotes (tenant_id, status, created_at DESC);
CREATE UNIQUE INDEX quotes_token ON quotes (token_hash);
CREATE INDEX quotes_expiring ON quotes (expires_at) WHERE status = 'offered';

ALTER TABLE quotes ENABLE ROW LEVEL SECURITY;
ALTER TABLE quotes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON quotes USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
ALTER TABLE quotes ADD COLUMN token bytea; -- the buyer's link, encrypted, so every offer email carries the same link
