-- Search merchandising, search terms, product reviews and wish lists.

-- Small JSON documents per store and kind (search configuration and other
-- settings that are read whole and cached in memory).
CREATE TABLE tenant_docs (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    kind       text NOT NULL,
    id         text NOT NULL,
    data       jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, kind, id)
);

-- What shoppers searched, per day. results = results of the latest search.
CREATE TABLE search_terms_daily (
    tenant_id text NOT NULL REFERENCES tenants(id),
    day       date NOT NULL,
    term      text NOT NULL CHECK (length(term) BETWEEN 1 AND 80),
    searches  integer NOT NULL DEFAULT 0,
    results   integer NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, day, term)
);

CREATE TABLE reviews (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    product_id  text NOT NULL,
    rating      smallint NOT NULL CHECK (rating BETWEEN 1 AND 5),
    title       text NOT NULL DEFAULT '',
    body        text NOT NULL DEFAULT '',
    author      text NOT NULL,
    email_hash  text NOT NULL,              -- keyed hash; the address is not stored
    verified    boolean NOT NULL DEFAULT false, -- the author bought the product
    status      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    reply       text NOT NULL DEFAULT '',
    replied_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, product_id, email_hash)
);
CREATE INDEX reviews_product ON reviews (tenant_id, product_id, status, created_at DESC);
CREATE INDEX reviews_status ON reviews (tenant_id, status, created_at DESC);

CREATE TABLE wishlist_items (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    customer_id text NOT NULL,
    product_id  text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, customer_id, product_id)
);
CREATE INDEX wishlist_product ON wishlist_items (tenant_id, product_id);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['tenant_docs','search_terms_daily','reviews','wishlist_items']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;
