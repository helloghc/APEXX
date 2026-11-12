-- Gifts: the wrapping, card message and gift receipt a shopper asked for, and
-- gift registries (wedding, baby, birthday) whose guests buy from a list. What
-- a registry shows as bought is derived from its paid orders.
ALTER TABLE orders ADD COLUMN gift jsonb, ADD COLUMN registry_id text;
CREATE INDEX orders_registry ON orders (tenant_id, registry_id) WHERE registry_id IS NOT NULL;

CREATE TABLE gift_registries (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    handle      text NOT NULL,
    customer_id text NOT NULL,
    title       text NOT NULL,
    kind        text NOT NULL DEFAULT 'otro' CHECK (kind IN ('boda','bebe','cumple','otro')),
    event_date  date,
    message     text NOT NULL DEFAULT '',
    ship_to     bytea,                 -- encrypted {name, address, postalCode}; never shown to guests
    listed      boolean NOT NULL DEFAULT true, -- guests can find it by name
    status      text NOT NULL DEFAULT 'active' CHECK (status IN ('active','closed')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, handle)
);
CREATE INDEX gift_registries_customer ON gift_registries (tenant_id, customer_id);
ALTER TABLE gift_registries ENABLE ROW LEVEL SECURITY;
ALTER TABLE gift_registries FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON gift_registries USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

CREATE TABLE gift_registry_items (
    tenant_id   text NOT NULL,
    registry_id text NOT NULL,
    sku         text NOT NULL,
    wanted      int NOT NULL CHECK (wanted BETWEEN 1 AND 999),
    bought      int NOT NULL DEFAULT 0,
    added_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, registry_id, sku),
    FOREIGN KEY (tenant_id, registry_id) REFERENCES gift_registries (tenant_id, id) ON DELETE CASCADE
);
ALTER TABLE gift_registry_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE gift_registry_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON gift_registry_items USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
