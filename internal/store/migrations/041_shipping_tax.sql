-- Shipping engine and taxes. Each row is a validated JSON document (the Go
-- types in internal/logistics are the schema), like channels and promotions.

CREATE TABLE shipping_zones (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    id         text NOT NULL,
    data       jsonb NOT NULL,            -- name, countries, states, postalRanges
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE shipping_profiles (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    id         text NOT NULL,
    data       jsonb NOT NULL,            -- name, kind (default | heavy | custom)
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE carriers (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    id         text NOT NULL,
    data       jsonb NOT NULL,            -- name, trackingUrl template
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE shipping_options (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    id         text NOT NULL,
    data       jsonb NOT NULL,            -- zone, profile, carrier, rate, estimate, pickup location
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE holidays (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    day        date NOT NULL,
    name       text NOT NULL,
    PRIMARY KEY (tenant_id, day)
);

CREATE TABLE tax_regions (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    id         text NOT NULL,
    data       jsonb NOT NULL,            -- name, country, rateBps, postalRanges, default
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

-- Per-product tax class and shipping profile (only products that differ from the default).
CREATE TABLE product_logistics (
    tenant_id        text NOT NULL REFERENCES tenants(id),
    product_id       text NOT NULL,
    tax_class        text NOT NULL DEFAULT 'general' CHECK (tax_class IN ('general','exento','tasa0')),
    shipping_profile text NOT NULL DEFAULT '',
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, product_id)
);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['shipping_zones','shipping_profiles','carriers','shipping_options','holidays','tax_regions','product_logistics']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;
