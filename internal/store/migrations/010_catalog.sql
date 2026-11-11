-- Advanced catalog: category tree, brands, collections, typed attributes,
-- product types and tags, media library and CSV import jobs. Products gain
-- options, SEO, scheduled publishing, an ordered gallery and related products.

ALTER TABLE products
    ADD COLUMN brand_id        text NOT NULL DEFAULT '',
    ADD COLUMN product_type    text NOT NULL DEFAULT '',
    ADD COLUMN tags            text[] NOT NULL DEFAULT '{}',
    ADD COLUMN options         jsonb NOT NULL DEFAULT '[]',   -- [{"name":"Talla","values":["S","M"]}]
    ADD COLUMN seo_title       text NOT NULL DEFAULT '',
    ADD COLUMN seo_description text NOT NULL DEFAULT '',
    ADD COLUMN publish_at      timestamptz,                   -- draft becomes active at this time
    ADD COLUMN media           text[] NOT NULL DEFAULT '{}',  -- ordered media ids; the first is the cover
    ADD COLUMN variant_media   jsonb NOT NULL DEFAULT '{}',   -- {"SKU":"media id"}
    ADD COLUMN related         jsonb NOT NULL DEFAULT '{}',   -- {"similar":[ids],"accessories":[ids]}
    ADD COLUMN created_at      timestamptz NOT NULL DEFAULT now();
UPDATE products SET created_at = updated_at;
CREATE INDEX products_publish_due ON products (publish_at) WHERE publish_at IS NOT NULL AND status = 'draft';
CREATE INDEX products_brand ON products (tenant_id, brand_id) WHERE brand_id <> '';

CREATE TABLE categories (
    tenant_id       text NOT NULL REFERENCES tenants(id),
    id              text NOT NULL,
    parent_id       text NOT NULL DEFAULT '',                 -- '' = root
    handle          text NOT NULL CHECK (handle ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    name            text NOT NULL,
    description     text NOT NULL DEFAULT '',
    seo_title       text NOT NULL DEFAULT '',
    seo_description text NOT NULL DEFAULT '',
    active          boolean NOT NULL DEFAULT true,
    position        int NOT NULL DEFAULT 0,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, handle)
);
CREATE INDEX categories_parent ON categories (tenant_id, parent_id, position);

CREATE TABLE brands (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    id            text NOT NULL,
    handle        text NOT NULL CHECK (handle ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    name          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    logo_media_id text NOT NULL DEFAULT '',
    active        boolean NOT NULL DEFAULT true,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, handle)
);

CREATE TABLE collections (
    tenant_id       text NOT NULL REFERENCES tenants(id),
    id              text NOT NULL,
    handle          text NOT NULL CHECK (handle ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    title           text NOT NULL,
    description     text NOT NULL DEFAULT '',
    kind            text NOT NULL CHECK (kind IN ('manual','rules')),
    product_ids     text[] NOT NULL DEFAULT '{}',             -- manual: ordered list
    rules           jsonb NOT NULL DEFAULT '{}',              -- rules: brands, categories, tags, price range
    seo_title       text NOT NULL DEFAULT '',
    seo_description text NOT NULL DEFAULT '',
    active          boolean NOT NULL DEFAULT true,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, handle)
);

CREATE TABLE attribute_defs (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL CHECK (id ~ '^[a-z0-9]+(_[a-z0-9]+)*$'),
    name        text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('text','number','boolean','select')),
    options     text[] NOT NULL DEFAULT '{}',
    unit        text NOT NULL DEFAULT '',
    filterable  boolean NOT NULL DEFAULT false,
    position    int NOT NULL DEFAULT 0,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

-- Managed vocabularies for product types and tags.
CREATE TABLE catalog_terms (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    kind        text NOT NULL CHECK (kind IN ('type','tag')),
    name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, kind, name)
);

-- Media library. The original and every resized variant live in the store's
-- object storage under "<media id>/<file>"; this row is the index.
CREATE TABLE media (
    tenant_id    text NOT NULL REFERENCES tenants(id),
    id           text NOT NULL,
    filename     text NOT NULL,
    content_type text NOT NULL,
    bytes        bigint NOT NULL CHECK (bytes > 0),
    width        int NOT NULL CHECK (width > 0),
    height       int NOT NULL CHECK (height > 0),
    alt          text NOT NULL CHECK (length(btrim(alt)) > 0),  -- alt text is required
    sha256       bytea NOT NULL,
    variants     jsonb NOT NULL DEFAULT '[]',                   -- [{"w":640,"h":480,"format":"jpeg","key":"…","bytes":123}]
    created_by   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX media_created ON media (tenant_id, created_at DESC, id DESC);
CREATE INDEX media_sha ON media (tenant_id, sha256);

-- CSV imports: validated first (preview), applied later in batches.
CREATE TABLE import_jobs (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    filename    text NOT NULL DEFAULT '',
    status      text NOT NULL CHECK (status IN ('preview','running','done','failed')),
    source      bytea NOT NULL,                                 -- the uploaded CSV
    summary     jsonb NOT NULL DEFAULT '{}',                    -- rows, products, creates, updates
    errors      jsonb NOT NULL DEFAULT '[]',                    -- row-level validation and apply errors
    total       int NOT NULL DEFAULT 0,
    processed   int NOT NULL DEFAULT 0,
    created     int NOT NULL DEFAULT 0,
    updated     int NOT NULL DEFAULT 0,
    failed      int NOT NULL DEFAULT 0,
    created_by  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX import_jobs_recent ON import_jobs (tenant_id, created_at DESC);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['categories','brands','collections','attribute_defs','catalog_terms','media','import_jobs']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;
