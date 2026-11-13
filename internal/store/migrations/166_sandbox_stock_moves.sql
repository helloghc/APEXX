-- What test orders of a sandbox took from (or gave back to) stock. A
-- publish of stock adds it back, so only the team's own adjustments reach
-- production; production's reservations are never touched.
CREATE TABLE sandbox_stock_moves (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    sku         text NOT NULL,
    location_id text NOT NULL,
    consumed    bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, sku, location_id)
);
ALTER TABLE sandbox_stock_moves ENABLE ROW LEVEL SECURITY;
ALTER TABLE sandbox_stock_moves FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sandbox_stock_moves
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
