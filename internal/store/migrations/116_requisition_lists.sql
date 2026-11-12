-- Reorder lists: products a buyer orders again and again. A list of a
-- company buyer is shared with every buyer of that company.
CREATE TABLE reorder_lists (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    customer_id text NOT NULL,
    company_id  text,
    name        text NOT NULL,
    items       jsonb NOT NULL,      -- [{sku, qty}]
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX reorder_lists_owner ON reorder_lists (tenant_id, customer_id);
CREATE INDEX reorder_lists_company ON reorder_lists (tenant_id, company_id) WHERE company_id IS NOT NULL;

ALTER TABLE reorder_lists ENABLE ROW LEVEL SECURITY;
ALTER TABLE reorder_lists FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON reorder_lists USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
