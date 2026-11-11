-- Account security: two-step verification, session management and invitations.

ALTER TABLE members ADD COLUMN totp_secret bytea;                 -- encrypted with the store key
ALTER TABLE members ADD COLUMN totp_enabled_at timestamptz;
ALTER TABLE members ADD COLUMN totp_last_step bigint NOT NULL DEFAULT 0; -- a code is accepted once
ALTER TABLE members ADD COLUMN recovery_codes jsonb NOT NULL DEFAULT '[]'; -- sha256 of each unused code

ALTER TABLE sessions ADD COLUMN id text;
UPDATE sessions SET id = 'ses_' || encode(substring(token_hash from 1 for 8), 'hex') WHERE id IS NULL;
ALTER TABLE sessions ALTER COLUMN id SET NOT NULL;
CREATE UNIQUE INDEX sessions_id ON sessions (id);
ALTER TABLE sessions ADD COLUMN last_seen_at timestamptz NOT NULL DEFAULT now();

CREATE TABLE invitations (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    email       text NOT NULL,
    name        text NOT NULL DEFAULT '',
    permissions jsonb NOT NULL DEFAULT '{}',
    token_hash  bytea NOT NULL UNIQUE,
    invited_by  text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    accepted_at timestamptz,
    revoked_at  timestamptz,
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX invitations_pending ON invitations (tenant_id, created_at DESC) WHERE accepted_at IS NULL AND revoked_at IS NULL;

ALTER TABLE invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invitations
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
