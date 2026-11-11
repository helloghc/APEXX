-- Area "settings" (040–049): stock locations, transfers and low-stock thresholds.
-- stock_levels.location_id stays a plain id; a location row describes it
-- (address, hours, pickup). Existing ids are registered automatically.

CREATE TABLE stock_locations (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL CHECK (id ~ '^[a-z0-9][a-z0-9-]{0,39}$'),
    data        jsonb NOT NULL,           -- name, kind, address, hours, pickup, active
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

INSERT INTO stock_locations (tenant_id, id, data)
SELECT DISTINCT tenant_id, location_id,
       jsonb_build_object('name', location_id, 'kind', 'warehouse', 'active', true, 'pickupEnabled', false)
FROM stock_levels
WHERE location_id ~ '^[a-z0-9][a-z0-9-]{0,39}$';

-- A transfer moves units between two locations in one transaction.
CREATE TABLE inventory_transfers (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    id            text NOT NULL,
    number        bigint NOT NULL,
    from_location text NOT NULL,
    to_location   text NOT NULL,
    note          text NOT NULL DEFAULT '',
    actor         text NOT NULL,
    units         bigint NOT NULL CHECK (units > 0),
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, number),
    CHECK (from_location <> to_location)
);

CREATE TABLE inventory_transfer_lines (
    tenant_id   text NOT NULL,
    transfer_id text NOT NULL,
    line        int NOT NULL,
    sku         text NOT NULL,
    qty         bigint NOT NULL CHECK (qty > 0),
    PRIMARY KEY (tenant_id, transfer_id, line),
    FOREIGN KEY (tenant_id, transfer_id) REFERENCES inventory_transfers(tenant_id, id) ON DELETE CASCADE
);

-- Low-stock threshold per SKU; location_id '' applies to every location.
CREATE TABLE stock_thresholds (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    sku         text NOT NULL,
    location_id text NOT NULL DEFAULT '',
    threshold   bigint NOT NULL CHECK (threshold >= 0),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, sku, location_id)
);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['stock_locations','inventory_transfers','inventory_transfer_lines','stock_thresholds']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;
