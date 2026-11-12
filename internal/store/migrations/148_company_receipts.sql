-- Online payment of a company's invoices: one receipt per payment, listing
-- the invoices it paid. Each invoice also gets its own payment entry.
CREATE TABLE company_receipts (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    id         text NOT NULL,
    company_id text NOT NULL,
    amount     bigint NOT NULL CHECK (amount > 0),
    currency   char(3) NOT NULL DEFAULT 'MXN',
    ref        text NOT NULL DEFAULT '',
    invoices   jsonb NOT NULL,            -- [{orderId, number, amount}]
    paid_by    text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, company_id) REFERENCES companies (tenant_id, id)
);
CREATE INDEX company_receipts_company ON company_receipts (tenant_id, company_id, created_at DESC);
ALTER TABLE company_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE company_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON company_receipts USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
