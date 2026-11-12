-- Blog: posts with a publication time; a future time keeps the post hidden
-- until then (scheduled), and the store can preview it before.
CREATE TABLE blog_posts (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    handle      text NOT NULL,
    title       text NOT NULL CHECK (length(title) BETWEEN 1 AND 160),
    excerpt     text NOT NULL DEFAULT '',
    body        text NOT NULL DEFAULT '',       -- sanitized HTML
    cover       text NOT NULL DEFAULT '',       -- media id
    author      text NOT NULL DEFAULT '',
    tags        text[] NOT NULL DEFAULT '{}',
    status      text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','published')),
    publish_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, handle)
);
CREATE INDEX blog_live ON blog_posts (tenant_id, publish_at DESC) WHERE status = 'published';
ALTER TABLE blog_posts ENABLE ROW LEVEL SECURITY;
ALTER TABLE blog_posts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON blog_posts USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
