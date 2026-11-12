-- Third-party apps installed in a store: each one with its own permissions
-- per module (the same model that protects the team), a secret shown once,
-- and immediate revocation.
CREATE TABLE apps (
    id            text PRIMARY KEY,
    tenant_id     text NOT NULL REFERENCES tenants(id),
    name          text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    developer     text NOT NULL DEFAULT '',
    url           text NOT NULL DEFAULT '',
    permissions   jsonb NOT NULL DEFAULT '{}',
    secret_hash   bytea NOT NULL,
    created_by    text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz,
    revoked_at    timestamptz
);
CREATE INDEX apps_tenant ON apps (tenant_id);
ALTER TABLE apps ENABLE ROW LEVEL SECURITY;
ALTER TABLE apps FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON apps USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
