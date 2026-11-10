-- Builder persistence (phase 3): drafts, immutable revisions and releases.

CREATE TABLE sites (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    id            text NOT NULL,
    name          text NOT NULL,
    live_revision text,
    draft_version bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE site_drafts (
    tenant_id   text NOT NULL,
    site_id     text NOT NULL,
    handle      text NOT NULL,
    template    jsonb NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, site_id, handle),
    FOREIGN KEY (tenant_id, site_id) REFERENCES sites(tenant_id, id)
);

CREATE TABLE site_revisions (
    tenant_id   text NOT NULL,
    site_id     text NOT NULL,
    id          text NOT NULL,
    parent_id   text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    created_by  text NOT NULL,
    message     text NOT NULL DEFAULT '',
    checksum    text NOT NULL,
    templates   jsonb NOT NULL,
    pages       jsonb NOT NULL,          -- handle -> rendered HTML
    PRIMARY KEY (tenant_id, site_id, id)
);

CREATE TABLE site_releases (
    tenant_id        text NOT NULL,
    site_id          text NOT NULL,
    seq              bigserial,
    revision_id      text NOT NULL,
    published_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    published_by     text NOT NULL,
    duration_micros  bigint NOT NULL,
    rolled_back_from text,
    PRIMARY KEY (tenant_id, site_id, seq)
);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['sites','site_drafts','site_revisions','site_releases']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;

CREATE TRIGGER site_revisions_immutable BEFORE UPDATE OR DELETE ON site_revisions
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
