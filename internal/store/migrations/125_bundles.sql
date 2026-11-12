-- Kits (bundles): a sellable SKU made of other SKUs. The kit keeps no stock of
-- its own; selling one reserves and decrements each component, so the kit can
-- never be sold while a component is missing.
CREATE TABLE bundle_components (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    bundle_sku    text NOT NULL,
    component_sku text NOT NULL,
    qty           bigint NOT NULL CHECK (qty BETWEEN 1 AND 10000),
    position      int NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, bundle_sku, component_sku),
    CHECK (bundle_sku <> component_sku)
);
CREATE INDEX bundle_components_component ON bundle_components (tenant_id, component_sku);
ALTER TABLE bundle_components ENABLE ROW LEVEL SECURITY;
ALTER TABLE bundle_components FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON bundle_components
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- Each order line keeps the composition it was sold with, so a cancellation
-- or return puts back exactly those units even if the kit changes later.
ALTER TABLE order_lines ADD COLUMN components jsonb;

CREATE FUNCTION order_line_components() RETURNS trigger AS $$
BEGIN
    IF NEW.components IS NULL THEN
        SELECT jsonb_agg(jsonb_build_object('sku', component_sku, 'qty', qty) ORDER BY position, component_sku)
          INTO NEW.components
          FROM bundle_components WHERE tenant_id = NEW.tenant_id AND bundle_sku = NEW.sku;
    END IF;
    RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER order_lines_components BEFORE INSERT ON order_lines
    FOR EACH ROW EXECUTE FUNCTION order_line_components();
