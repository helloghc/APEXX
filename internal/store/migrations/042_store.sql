-- Store settings, policies, menus, redirects and custom domains.

CREATE TABLE store_settings (
    tenant_id  text PRIMARY KEY REFERENCES tenants(id),
    data       jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Small binary assets of the store (logo). Served publicly with a content hash.
CREATE TABLE store_assets (
    tenant_id    text NOT NULL REFERENCES tenants(id),
    name         text NOT NULL,
    content_type text NOT NULL,
    body         bytea NOT NULL CHECK (octet_length(body) <= 524288),
    sha          text NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, name)
);

CREATE TABLE policies (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    slug       text NOT NULL CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    title      text NOT NULL,
    body       text NOT NULL,             -- sanitized HTML
    published  boolean NOT NULL DEFAULT true,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, slug)
);

CREATE TABLE menus (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    id         text NOT NULL CHECK (id ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    data       jsonb NOT NULL,            -- name, items (nested)
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE redirects (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    id         text NOT NULL,
    from_path  text NOT NULL,
    target     text NOT NULL,
    code       int NOT NULL DEFAULT 301 CHECK (code IN (301, 302)),
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, from_path)
);

CREATE TABLE domains (
    tenant_id       text NOT NULL REFERENCES tenants(id),
    id              text NOT NULL,
    hostname        text NOT NULL CHECK (hostname = lower(hostname)),
    site            text NOT NULL DEFAULT 'main',
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','verified','failed')),
    token           text NOT NULL,
    is_primary      boolean NOT NULL DEFAULT false,
    last_error      text NOT NULL DEFAULT '',
    last_checked_at timestamptz,
    verified_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, hostname)
);
-- A hostname can be verified by one store only (across tenants).
CREATE UNIQUE INDEX domains_verified_host ON domains (hostname) WHERE status = 'verified';

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['store_settings','store_assets','policies','menus','redirects','domains']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;
