-- Apexx core schema. Every tenant-owned table carries tenant_id and is
-- protected by Row Level Security: the application role can only see rows of
-- the tenant set with SET LOCAL app.tenant_id inside each transaction.

CREATE TABLE tenants (
    id          text PRIMARY KEY CHECK (id ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
    name        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id          text PRIMARY KEY,               -- public prefix, e.g. "k_3f9a..."
    tenant_id   text NOT NULL REFERENCES tenants(id),
    name        text NOT NULL,
    role        text NOT NULL CHECK (role IN ('owner','admin','editor','publisher','storefront','viewer')),
    secret_hash bytea NOT NULL,                 -- sha256(secret); the secret is shown once
    created_at  timestamptz NOT NULL DEFAULT now(),
    revoked_at  timestamptz
);

-- Per-tenant data encryption key, wrapped with the platform master key (KMS in production).
CREATE TABLE tenant_keys (
    tenant_id   text PRIMARY KEY REFERENCES tenants(id),
    wrapped_key bytea NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Sales channels: currency, tax, limits and shipping rules, all visible through the API.
CREATE TABLE channels (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    config      jsonb NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

-- Promotions as typed rules; the engine explains every outcome.
CREATE TABLE promotions (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('percent_over_threshold','buy_x_pay_y')),
    rule        jsonb NOT NULL,
    active      boolean NOT NULL DEFAULT true,
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE products (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    handle      text NOT NULL,
    title       text NOT NULL,
    description text NOT NULL DEFAULT '',
    brand       text NOT NULL DEFAULT '',
    categories  text[] NOT NULL DEFAULT '{}',
    attributes  jsonb NOT NULL DEFAULT '{}',
    status      text NOT NULL DEFAULT 'active' CHECK (status IN ('active','draft','archived')),
    version     bigint NOT NULL DEFAULT 1,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, handle)
);

CREATE TABLE variants (
    tenant_id    text NOT NULL,
    sku          text NOT NULL,
    product_id   text NOT NULL,
    title        text NOT NULL DEFAULT '',
    options      jsonb NOT NULL DEFAULT '{}',
    barcode      text NOT NULL DEFAULT '',
    weight_grams bigint NOT NULL DEFAULT 0 CHECK (weight_grams >= 0),
    position     int NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, sku),
    FOREIGN KEY (tenant_id, product_id) REFERENCES products(tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX variants_product ON variants (tenant_id, product_id);

CREATE TABLE stock_levels (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    sku         text NOT NULL,
    location_id text NOT NULL,
    on_hand     bigint NOT NULL CHECK (on_hand >= 0),
    reserved    bigint NOT NULL DEFAULT 0 CHECK (reserved >= 0),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, sku, location_id)
);

CREATE TABLE reservations (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          bigserial,
    holder      text NOT NULL,
    sku         text NOT NULL,
    location_id text NOT NULL,
    qty         bigint NOT NULL CHECK (qty > 0),
    expires_at  timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX reservations_holder ON reservations (tenant_id, holder);
CREATE INDEX reservations_expiry ON reservations (expires_at);

CREATE TABLE prices (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    channel     text NOT NULL,
    sku         text NOT NULL,
    min_qty     bigint NOT NULL CHECK (min_qty >= 1),
    amount      bigint NOT NULL CHECK (amount >= 0),
    currency    char(3) NOT NULL,
    compare_at  bigint CHECK (compare_at IS NULL OR compare_at >= 0),
    PRIMARY KEY (tenant_id, channel, sku, min_qty)
);

-- Transactional outbox: written in the same transaction as the change, so
-- projections (catalog, search, storefront) can never miss an update.
CREATE TABLE outbox (
    id          bigserial PRIMARY KEY,
    tenant_id   text NOT NULL,
    topic       text NOT NULL,
    key         text NOT NULL,
    payload     jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE OR REPLACE FUNCTION outbox_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('apexx_outbox', NEW.id::text);
    RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER outbox_notify AFTER INSERT ON outbox FOR EACH ROW EXECUTE FUNCTION outbox_notify();

-- Append-only audit log, hash-chained per tenant so tampering is detectable.
CREATE TABLE audit_log (
    tenant_id   text NOT NULL,
    seq         bigint NOT NULL,
    at          timestamptz NOT NULL DEFAULT clock_timestamp(),
    actor       text NOT NULL,
    action      text NOT NULL,
    target      text NOT NULL,
    data        jsonb NOT NULL DEFAULT '{}',
    prev_hash   bytea NOT NULL,
    hash        bytea NOT NULL,
    PRIMARY KEY (tenant_id, seq)
);

CREATE TABLE idempotency_keys (
    tenant_id   text NOT NULL,
    key         text NOT NULL,
    request_hash bytea NOT NULL,
    status      int NOT NULL,
    response    jsonb NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, key)
);

-- Row Level Security on every tenant-owned table.
DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['tenant_keys','channels','promotions','products','variants','stock_levels','reservations','prices','outbox','audit_log','idempotency_keys']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;

-- The audit log is append-only for the application.
CREATE OR REPLACE FUNCTION audit_log_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only';
END $$ LANGUAGE plpgsql;
CREATE TRIGGER audit_log_no_update BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
