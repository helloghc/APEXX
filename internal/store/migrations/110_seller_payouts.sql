-- Automatic payouts to marketplace sellers. A payout takes the money out of
-- the seller's statement first and sends it second, so a crash between the
-- two can only delay a transfer, never send it twice.

ALTER TABLE sellers
    ADD COLUMN payout_account   text NOT NULL DEFAULT '',
    ADD COLUMN payout_auto      boolean NOT NULL DEFAULT false,
    ADD COLUMN payout_min       bigint NOT NULL DEFAULT 0 CHECK (payout_min >= 0),
    ADD COLUMN payout_hold_days integer NOT NULL DEFAULT 7 CHECK (payout_hold_days BETWEEN 0 AND 90);

CREATE TABLE seller_payouts (
    tenant_id    text NOT NULL REFERENCES tenants(id),
    id           text NOT NULL,
    seller_id    text NOT NULL,
    amount       bigint NOT NULL CHECK (amount > 0),
    currency     text NOT NULL DEFAULT 'MXN',
    status       text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','paid','failed')),
    destination  text NOT NULL,
    provider     text NOT NULL DEFAULT '',
    provider_ref text NOT NULL DEFAULT '',
    error        text NOT NULL DEFAULT '',
    attempts     integer NOT NULL DEFAULT 0,
    actor        text NOT NULL DEFAULT 'system',
    created_at   timestamptz NOT NULL DEFAULT now(),
    settled_at   timestamptz,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, seller_id) REFERENCES sellers(tenant_id, id)
);
CREATE INDEX seller_payouts_seller ON seller_payouts (tenant_id, seller_id, created_at DESC);
CREATE INDEX seller_payouts_pending ON seller_payouts (created_at) WHERE status = 'pending';
-- One transfer in flight per seller: a second one waits for the first to settle.
CREATE UNIQUE INDEX seller_payouts_one_pending ON seller_payouts (tenant_id, seller_id) WHERE status = 'pending';

ALTER TABLE seller_entries ADD COLUMN payout_id text;
ALTER TABLE seller_entries DROP CONSTRAINT seller_entries_kind_check;
ALTER TABLE seller_entries ADD CONSTRAINT seller_entries_kind_check CHECK (kind IN ('sale','refund','payout','adjust','payout_failed'));

ALTER TABLE seller_payouts ENABLE ROW LEVEL SECURITY;
ALTER TABLE seller_payouts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON seller_payouts USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
