-- Promotions v2: full definitions, campaigns with budgets, coupons and
-- redemptions. The promotions table from 001 is extended in place so rules
-- written by older tools (kind percent_over_threshold / buy_x_pay_y) keep
-- working; the loader converts them.

ALTER TABLE promotions DROP CONSTRAINT IF EXISTS promotions_kind_check;
ALTER TABLE promotions
    ADD COLUMN name            text NOT NULL DEFAULT '',
    ADD COLUMN status          text NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused','draft','archived')),
    ADD COLUMN starts_at       timestamptz,
    ADD COLUMN ends_at         timestamptz,
    ADD COLUMN campaign_id     text NOT NULL DEFAULT '',
    ADD COLUMN priority        int NOT NULL DEFAULT 0,
    ADD COLUMN requires_coupon boolean NOT NULL DEFAULT false,
    ADD COLUMN uses            bigint NOT NULL DEFAULT 0 CHECK (uses >= 0),          -- reserved + redeemed
    ADD COLUMN discount_total  bigint NOT NULL DEFAULT 0 CHECK (discount_total >= 0), -- discount of those uses
    ADD COLUMN created_at      timestamptz NOT NULL DEFAULT now(),
    ADD COLUMN updated_at      timestamptz NOT NULL DEFAULT now();
CREATE INDEX promotions_campaign ON promotions (tenant_id, campaign_id) WHERE campaign_id <> '';

CREATE TABLE campaigns (
    tenant_id    text NOT NULL REFERENCES tenants(id),
    id           text NOT NULL,
    name         text NOT NULL,
    description  text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused')),
    starts_at    timestamptz,
    ends_at      timestamptz,
    budget_type  text NOT NULL DEFAULT 'none' CHECK (budget_type IN ('none','amount','uses')),
    budget_limit bigint NOT NULL DEFAULT 0 CHECK (budget_limit >= 0),
    spent        bigint NOT NULL DEFAULT 0 CHECK (spent >= 0),
    used         bigint NOT NULL DEFAULT 0 CHECK (used >= 0),
    currency     char(3) NOT NULL DEFAULT 'MXN',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE coupon_batches (
    tenant_id    text NOT NULL REFERENCES tenants(id),
    id           text NOT NULL,
    promotion_id text NOT NULL,
    prefix       text NOT NULL DEFAULT '',
    quantity     int NOT NULL CHECK (quantity > 0),
    actor        text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

-- One row per code. Codes are stored upper case; uses counts reserved + redeemed.
CREATE TABLE coupons (
    tenant_id             text NOT NULL REFERENCES tenants(id),
    code                  text NOT NULL CHECK (code = upper(code) AND length(code) BETWEEN 3 AND 40),
    promotion_id          text NOT NULL,
    batch_id              text NOT NULL DEFAULT '',
    status                text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    starts_at             timestamptz,
    ends_at               timestamptz,
    max_uses              bigint NOT NULL DEFAULT 0 CHECK (max_uses >= 0),
    max_uses_per_customer bigint NOT NULL DEFAULT 0 CHECK (max_uses_per_customer >= 0),
    uses                  bigint NOT NULL DEFAULT 0 CHECK (uses >= 0),
    created_at            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, code),
    FOREIGN KEY (tenant_id, promotion_id) REFERENCES promotions(tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX coupons_promotion ON coupons (tenant_id, promotion_id, code);
CREATE INDEX coupons_batch ON coupons (tenant_id, batch_id, code) WHERE batch_id <> '';

-- One row per (order, promotion). reserved while the order awaits payment,
-- redeemed once paid, released on decline, cancellation or refund. The
-- customer is a keyed digest of the email, never the email itself.
CREATE TABLE promotion_redemptions (
    tenant_id       text NOT NULL REFERENCES tenants(id),
    order_id        text NOT NULL,
    promotion_id    text NOT NULL,
    promotion_name  text NOT NULL DEFAULT '',
    coupon_code     text NOT NULL DEFAULT '',
    campaign_id     text NOT NULL DEFAULT '',
    customer_hash   text NOT NULL DEFAULT '',
    amount          bigint NOT NULL CHECK (amount >= 0),
    currency        char(3) NOT NULL,
    status          text NOT NULL CHECK (status IN ('reserved','redeemed','released')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, order_id, promotion_id)
);
CREATE INDEX redemptions_promotion ON promotion_redemptions (tenant_id, promotion_id, created_at DESC);
CREATE INDEX redemptions_customer ON promotion_redemptions (tenant_id, promotion_id, customer_hash) WHERE status <> 'released';
CREATE INDEX redemptions_coupon ON promotion_redemptions (tenant_id, coupon_code, customer_hash) WHERE coupon_code <> '' AND status <> 'released';

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['campaigns','coupon_batches','coupons','promotion_redemptions']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;
