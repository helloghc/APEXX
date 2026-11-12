-- Recommendations and learning search. Only aggregate counts are kept: which
-- products were seen together in one visit, and which product shoppers chose
-- after searching a term. Nothing here identifies a person.
CREATE TABLE product_coviews (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    a          text NOT NULL,
    b          text NOT NULL,
    views      integer NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, a, b),
    CHECK (a < b)
);
ALTER TABLE product_coviews ENABLE ROW LEVEL SECURITY;
ALTER TABLE product_coviews FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON product_coviews USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

CREATE TABLE search_clicks (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    term       text NOT NULL,
    product_id text NOT NULL,
    clicks     integer NOT NULL DEFAULT 0,
    carts      integer NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, term, product_id)
);
ALTER TABLE search_clicks ENABLE ROW LEVEL SECURITY;
ALTER TABLE search_clicks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON search_clicks USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
