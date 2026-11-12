-- Sales representatives: a member of the store's team is assigned to a
-- company and, when the company allows it, opens the store as one of its
-- buyers. The session carries the representative, so every order and change
-- records both.
ALTER TABLE companies ADD COLUMN sales_rep text NOT NULL DEFAULT '', ADD COLUMN rep_access boolean NOT NULL DEFAULT false;
ALTER TABLE shopper_sessions ADD COLUMN acting_member text, ADD COLUMN acting_name text;
CREATE TABLE rep_handoffs (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    token_hash  bytea NOT NULL,
    customer_id text NOT NULL,
    company_id  text NOT NULL,
    member_id   text NOT NULL,
    member_name text NOT NULL,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, token_hash)
);
ALTER TABLE rep_handoffs ENABLE ROW LEVEL SECURITY;
ALTER TABLE rep_handoffs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON rep_handoffs USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
