-- Preorders: a SKU sold before it exists, from a pool of units (unlimited when
-- max_units = 0) instead of stock, with an optional deposit: the buyer pays
-- deposit_bps of the line now and the rest when the store asks for it.
CREATE TABLE preorders (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    sku         text NOT NULL,
    ships_on    date NOT NULL,
    deposit_bps int NOT NULL DEFAULT 10000 CHECK (deposit_bps BETWEEN 500 AND 10000),
    max_units   bigint NOT NULL DEFAULT 0 CHECK (max_units >= 0),
    sold        bigint NOT NULL DEFAULT 0 CHECK (sold >= 0),
    active      boolean NOT NULL DEFAULT true,
    note        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, sku),
    CHECK (max_units = 0 OR sold <= max_units)
);
ALTER TABLE preorders ENABLE ROW LEVEL SECURITY;
ALTER TABLE preorders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON preorders USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- What the buyer still owes for preorder lines (charged later with a link).
ALTER TABLE orders ADD COLUMN deferred bigint NOT NULL DEFAULT 0 CHECK (deferred >= 0);
-- The promised date of a preorder line, frozen when sold.
ALTER TABLE order_lines ADD COLUMN preorder_ships_on date;

CREATE FUNCTION order_line_preorder() RETURNS trigger AS $$
BEGIN
    IF NEW.preorder_ships_on IS NULL THEN
        SELECT ships_on INTO NEW.preorder_ships_on FROM preorders
         WHERE tenant_id = NEW.tenant_id AND sku = NEW.sku AND active;
    END IF;
    RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER order_lines_preorder BEFORE INSERT ON order_lines
    FOR EACH ROW EXECUTE FUNCTION order_line_preorder();
