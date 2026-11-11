-- B2B: price per product for a company (through a price list group) and
-- purchase approvals inside the company.
ALTER TABLE companies ADD COLUMN price_group text NOT NULL DEFAULT '';
ALTER TABLE companies ADD COLUMN approval_threshold bigint NOT NULL DEFAULT 0 CHECK (approval_threshold >= 0);
ALTER TABLE company_buyers ADD COLUMN approver boolean NOT NULL DEFAULT false;

CREATE TABLE purchase_requests (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    company_id  text NOT NULL,
    status      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    total       bigint NOT NULL,
    currency    char(3) NOT NULL,
    request     bytea NOT NULL,          -- encrypted checkout request (who, what, where)
    summary     jsonb NOT NULL,          -- lines shown to the approver: title, qty, total
    token_hash  bytea NOT NULL,
    order_id    text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    decided_at  timestamptz,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, token_hash),
    FOREIGN KEY (tenant_id, company_id) REFERENCES companies(tenant_id, id)
);
CREATE INDEX purchase_requests_company ON purchase_requests (tenant_id, company_id, created_at DESC);

ALTER TABLE purchase_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_requests FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_requests
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
