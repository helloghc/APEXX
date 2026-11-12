-- Punchout (cXML): a company's purchasing system (Ariba, Coupa, Jaggaer...)
-- opens the store for one of its buyers, receives the cart back as a
-- PunchOutOrderMessage and later sends the purchase order. Each company has
-- its identity and a shared secret (only its hash is kept).
CREATE TABLE company_punchout (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    company_id  text NOT NULL,
    identity    text NOT NULL,
    secret_hash bytea NOT NULL,
    buyer_email text NOT NULL,
    enabled     boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, company_id),
    UNIQUE (tenant_id, identity),
    FOREIGN KEY (tenant_id, company_id) REFERENCES companies (tenant_id, id) ON DELETE CASCADE
);
ALTER TABLE company_punchout ENABLE ROW LEVEL SECURITY;
ALTER TABLE company_punchout FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON company_punchout USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

CREATE TABLE punchout_sessions (
    tenant_id      text NOT NULL REFERENCES tenants(id),
    id             text NOT NULL,
    company_id     text NOT NULL,
    start_hash     bytea NOT NULL,          -- the single-use start link
    buyer_cookie   text NOT NULL,
    return_url     text NOT NULL,
    expires_at     timestamptz NOT NULL,
    started_at     timestamptz,
    message        text NOT NULL DEFAULT '', -- the last cart sent back (PunchOutOrderMessage)
    transferred_at timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE UNIQUE INDEX punchout_sessions_start ON punchout_sessions (start_hash);
ALTER TABLE punchout_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE punchout_sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON punchout_sessions USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

ALTER TABLE shopper_sessions ADD COLUMN punchout_id text;

-- Purchase orders received by punchout, once each (the system may resend).
CREATE TABLE punchout_orders (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    company_id text NOT NULL,
    po         text NOT NULL,
    draft_id   text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, company_id, po)
);
ALTER TABLE punchout_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE punchout_orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON punchout_orders USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
