-- Customers, shopper accounts and segments (area "customers", range 030-039).
-- Personal data (email, name, phone, addresses, notes) is encrypted with the
-- tenant key. Lookups use blind indexes: HMAC of the normalized value under a
-- key derived from the tenant key, so the database alone reveals nothing.

CREATE TABLE customers (
    tenant_id         text NOT NULL REFERENCES tenants(id),
    id                text NOT NULL,
    email_hash        bytea NOT NULL,                 -- blind index of the lower-cased email
    phone_hash        bytea,                          -- blind index of the digits of the phone
    name_tokens       bytea[] NOT NULL DEFAULT '{}',  -- blind index of each word of the name
    profile           bytea NOT NULL,                 -- encrypted JSON {email,name,phone}
    tags              text[] NOT NULL DEFAULT '{}',
    marketing_consent boolean NOT NULL DEFAULT false,
    consent_at        timestamptz,
    consent_source    text NOT NULL DEFAULT '',
    password_hash     text NOT NULL DEFAULT '',       -- argon2id; empty = sign in with code only
    verified_at       timestamptz,                    -- email proven with a one-time code
    status            text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    orders_count      int NOT NULL DEFAULT 0,
    total_spent       bigint NOT NULL DEFAULT 0,
    currency          char(3) NOT NULL DEFAULT 'MXN',
    first_order_at    timestamptz,
    last_order_at     timestamptz,
    version           bigint NOT NULL DEFAULT 1,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, email_hash)
);
CREATE INDEX customers_phone ON customers (tenant_id, phone_hash) WHERE phone_hash IS NOT NULL;
CREATE INDEX customers_tokens ON customers USING gin (name_tokens);
CREATE INDEX customers_tags ON customers USING gin (tags);
CREATE INDEX customers_created ON customers (tenant_id, created_at DESC, id);

CREATE TABLE customer_addresses (
    tenant_id   text NOT NULL,
    id          text NOT NULL,
    customer_id text NOT NULL,
    data        bytea NOT NULL,                       -- encrypted JSON address
    is_default  boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, customer_id) REFERENCES customers(tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX customer_addresses_customer ON customer_addresses (tenant_id, customer_id);

CREATE TABLE customer_notes (
    tenant_id   text NOT NULL,
    id          text NOT NULL,
    customer_id text NOT NULL,
    body        bytea NOT NULL,                       -- encrypted text
    author      text NOT NULL,
    author_name text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, customer_id) REFERENCES customers(tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX customer_notes_customer ON customer_notes (tenant_id, customer_id, created_at DESC);

-- Groups: 'manual' lists members explicitly; 'rules' is evaluated live from
-- lifetime stats (total spent, order count, days since last order, tags, consent).
CREATE TABLE customer_groups (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    description text NOT NULL DEFAULT '',
    kind        text NOT NULL CHECK (kind IN ('manual','rules')),
    rules       jsonb NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, name)
);

CREATE TABLE customer_group_members (
    tenant_id   text NOT NULL,
    group_id    text NOT NULL,
    customer_id text NOT NULL,
    added_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, group_id, customer_id),
    FOREIGN KEY (tenant_id, group_id) REFERENCES customer_groups(tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, customer_id) REFERENCES customers(tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX customer_group_members_customer ON customer_group_members (tenant_id, customer_id);

-- One-time sign-in codes: 6 digits, 10 minutes, stored hashed, 5 attempts.
CREATE TABLE shopper_otps (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    email_hash  bytea NOT NULL,
    code_hash   bytea NOT NULL,
    attempts    int NOT NULL DEFAULT 0,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX shopper_otps_email ON shopper_otps (tenant_id, email_hash, created_at DESC);

CREATE TABLE shopper_sessions (
    tenant_id   text NOT NULL,
    token_hash  bytea NOT NULL,                       -- sha256 of the opaque token
    customer_id text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, token_hash),
    FOREIGN KEY (tenant_id, customer_id) REFERENCES customers(tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX shopper_sessions_expiry ON shopper_sessions (expires_at);

-- Orders point at their customer; email_hash lets guest orders be claimed.
ALTER TABLE orders ADD COLUMN customer_id text;
ALTER TABLE orders ADD COLUMN email_hash bytea;
CREATE INDEX orders_customer ON orders (tenant_id, customer_id, number DESC) WHERE customer_id IS NOT NULL;
CREATE INDEX orders_email_hash ON orders (tenant_id, email_hash) WHERE email_hash IS NOT NULL;

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['customers','customer_addresses','customer_notes','customer_groups','customer_group_members','shopper_otps','shopper_sessions']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;
