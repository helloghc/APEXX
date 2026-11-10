-- Unlimited team seats per store with permissions per module.
-- permissions: {"catalog":"edit","orders":"read",...}; levels: none < read < edit < admin.

CREATE TABLE members (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    id            text NOT NULL,
    email         text NOT NULL,
    name          text NOT NULL DEFAULT '',
    password_hash text NOT NULL,             -- argon2id
    permissions   jsonb NOT NULL DEFAULT '{}',
    is_owner      boolean NOT NULL DEFAULT false,
    status        text NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, email)
);

CREATE TABLE sessions (
    token_hash  bytea PRIMARY KEY,           -- sha256 of the opaque token
    tenant_id   text NOT NULL REFERENCES tenants(id),
    member_id   text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    ip          text NOT NULL DEFAULT '',
    user_agent  text NOT NULL DEFAULT '',
    FOREIGN KEY (tenant_id, member_id) REFERENCES members(tenant_id, id)
);
CREATE INDEX sessions_member ON sessions (tenant_id, member_id);

ALTER TABLE api_keys ADD COLUMN permissions jsonb NOT NULL DEFAULT '{}';

ALTER TABLE members ENABLE ROW LEVEL SECURITY;
ALTER TABLE members FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON members
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
