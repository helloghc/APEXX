-- Digital products: files attached to a SKU, delivered after payment through
-- signed links with a download limit and an expiry. The bytes live in the
-- per-store media storage; only metadata is here.
CREATE TABLE digital_files (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    id            text NOT NULL,
    sku           text NOT NULL,
    filename      text NOT NULL CHECK (length(filename) BETWEEN 1 AND 200),
    content_type  text NOT NULL,
    size          bigint NOT NULL CHECK (size > 0),
    sha256        bytea NOT NULL,
    storage_key   text NOT NULL,
    max_downloads int NOT NULL DEFAULT 5 CHECK (max_downloads BETWEEN 1 AND 100),
    link_days     int NOT NULL DEFAULT 30 CHECK (link_days BETWEEN 1 AND 3650),
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX digital_files_sku ON digital_files (tenant_id, sku);
ALTER TABLE digital_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE digital_files FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON digital_files USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- One grant per paid order and file. The id is random (gen_random_uuid) and
-- the link also carries an HMAC, so links cannot be guessed or altered.
CREATE TABLE download_grants (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    id            text NOT NULL,
    order_id      text NOT NULL,
    file_id       text NOT NULL,
    downloads     int NOT NULL DEFAULT 0,
    max_downloads int NOT NULL,
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    last_at       timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, order_id, file_id),
    CHECK (downloads <= max_downloads)
);
ALTER TABLE download_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE download_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON download_grants USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
