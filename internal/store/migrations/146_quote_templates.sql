-- Quote templates: an agreement with a company (negotiated price per
-- product, minimum and maximum per order, minimum order total, a committed
-- total and validity) from which its buyers order again and again without
-- renegotiating. Every order placed from it is linked, so the store sees how
-- much of the commitment is used.
CREATE TABLE quote_templates (
    tenant_id       text NOT NULL REFERENCES tenants(id),
    id              text NOT NULL,
    company_id      text NOT NULL,
    name            text NOT NULL,
    items           jsonb NOT NULL,          -- [{sku, unit, minQty, maxQty}]
    min_order_total bigint NOT NULL DEFAULT 0,
    commit_total    bigint NOT NULL DEFAULT 0,
    valid_from      date NOT NULL,
    valid_until     date NOT NULL,
    status          text NOT NULL DEFAULT 'active' CHECK (status IN ('active','closed')),
    note            text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CHECK (valid_until >= valid_from),
    FOREIGN KEY (tenant_id, company_id) REFERENCES companies (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX quote_templates_company ON quote_templates (tenant_id, company_id);
ALTER TABLE quote_templates ENABLE ROW LEVEL SECURITY;
ALTER TABLE quote_templates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON quote_templates USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

CREATE TABLE quote_template_orders (
    tenant_id   text NOT NULL,
    template_id text NOT NULL,
    draft_id    text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, draft_id),
    FOREIGN KEY (tenant_id, template_id) REFERENCES quote_templates (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX quote_template_orders_template ON quote_template_orders (tenant_id, template_id);
ALTER TABLE quote_template_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE quote_template_orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON quote_template_orders USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
