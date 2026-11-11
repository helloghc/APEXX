-- Price lists (scheduled overrides or percentage adjustments per channel and,
-- later, per customer group) and the price history of every SKU.

CREATE TABLE price_lists (
    tenant_id      text NOT NULL REFERENCES tenants(id),
    id             text NOT NULL,
    name           text NOT NULL,
    description    text NOT NULL DEFAULT '',
    status         text NOT NULL DEFAULT 'active' CHECK (status IN ('active','paused')),
    channel        text NOT NULL DEFAULT '',        -- '' = every channel
    customer_group text NOT NULL DEFAULT '',        -- '' = every customer (groups arrive with the customers module)
    kind           text NOT NULL CHECK (kind IN ('override','adjust')),
    adjust_bps     int NOT NULL DEFAULT 0 CHECK (adjust_bps BETWEEN -10000 AND 100000), -- -1000 = 10% cheaper
    applies_to     text NOT NULL DEFAULT 'items' CHECK (applies_to IN ('all','items')),
    priority       int NOT NULL DEFAULT 0,
    starts_at      timestamptz,
    ends_at        timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

CREATE TABLE price_list_items (
    tenant_id text NOT NULL,
    list_id   text NOT NULL,
    sku       text NOT NULL,
    amount    bigint CHECK (amount IS NULL OR amount >= 0), -- NULL for adjust lists
    currency  char(3) NOT NULL DEFAULT 'MXN',
    PRIMARY KEY (tenant_id, list_id, sku),
    FOREIGN KEY (tenant_id, list_id) REFERENCES price_lists(tenant_id, id) ON DELETE CASCADE
);

-- Append-only history of base prices. Written by triggers on prices, so every
-- writer (admin, importer, bulk tool, API) is recorded without extra code.
CREATE TABLE price_history (
    tenant_id   text NOT NULL,
    id          bigserial,
    sku         text NOT NULL,
    channel     text NOT NULL,
    min_qty     bigint NOT NULL,
    old_amount  bigint,
    new_amount  bigint,            -- NULL = the price was removed
    old_compare bigint,
    new_compare bigint,
    currency    char(3) NOT NULL,
    source      text NOT NULL DEFAULT 'manual',  -- manual | bulk | import
    ref         text NOT NULL DEFAULT '',        -- bulk change id
    actor       text NOT NULL DEFAULT '',
    at          timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX price_history_sku ON price_history (tenant_id, sku, channel, min_qty, id DESC);
CREATE INDEX price_history_ref ON price_history (tenant_id, ref) WHERE ref <> '';

CREATE OR REPLACE FUNCTION price_history_upsert() RETURNS trigger AS $$
DECLARE last record;
BEGIN
    SELECT new_amount, new_compare INTO last FROM price_history
        WHERE tenant_id = NEW.tenant_id AND sku = NEW.sku AND channel = NEW.channel AND min_qty = NEW.min_qty
        ORDER BY id DESC LIMIT 1;
    IF FOUND AND last.new_amount IS NOT DISTINCT FROM NEW.amount AND last.new_compare IS NOT DISTINCT FROM NEW.compare_at THEN
        RETURN NEW; -- rewritten with the same value: not a change
    END IF;
    INSERT INTO price_history (tenant_id, sku, channel, min_qty, old_amount, new_amount, old_compare, new_compare, currency, source, ref, actor)
    VALUES (NEW.tenant_id, NEW.sku, NEW.channel, NEW.min_qty, last.new_amount, NEW.amount, last.new_compare, NEW.compare_at, NEW.currency,
        COALESCE(NULLIF(current_setting('app.price_source', true), ''), 'manual'),
        COALESCE(current_setting('app.price_ref', true), ''),
        COALESCE(current_setting('app.actor', true), ''));
    RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER price_history_upsert AFTER INSERT OR UPDATE ON prices
    FOR EACH ROW EXECUTE FUNCTION price_history_upsert();

-- Writers replace tiers with DELETE + INSERT; a removal only counts if the
-- row is still gone when the transaction commits.
CREATE OR REPLACE FUNCTION price_history_delete() RETURNS trigger AS $$
DECLARE last record;
BEGIN
    IF EXISTS (SELECT 1 FROM prices WHERE tenant_id = OLD.tenant_id AND channel = OLD.channel AND sku = OLD.sku AND min_qty = OLD.min_qty) THEN
        RETURN NULL;
    END IF;
    SELECT new_amount INTO last FROM price_history
        WHERE tenant_id = OLD.tenant_id AND sku = OLD.sku AND channel = OLD.channel AND min_qty = OLD.min_qty
        ORDER BY id DESC LIMIT 1;
    IF FOUND AND last.new_amount IS NULL THEN
        RETURN NULL;
    END IF;
    INSERT INTO price_history (tenant_id, sku, channel, min_qty, old_amount, new_amount, old_compare, new_compare, currency, source, ref, actor)
    VALUES (OLD.tenant_id, OLD.sku, OLD.channel, OLD.min_qty, OLD.amount, NULL, OLD.compare_at, NULL, OLD.currency,
        COALESCE(NULLIF(current_setting('app.price_source', true), ''), 'manual'),
        COALESCE(current_setting('app.price_ref', true), ''),
        COALESCE(current_setting('app.actor', true), ''));
    RETURN NULL;
END $$ LANGUAGE plpgsql;
CREATE CONSTRAINT TRIGGER price_history_delete AFTER DELETE ON prices
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION price_history_delete();

-- Existing prices become the first entry of each history.
INSERT INTO price_history (tenant_id, sku, channel, min_qty, old_amount, new_amount, old_compare, new_compare, currency, source, actor)
SELECT tenant_id, sku, channel, min_qty, NULL, amount, NULL, compare_at, currency, 'import', 'migration' FROM prices;

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['price_lists','price_list_items','price_history']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;

CREATE TRIGGER price_history_no_update BEFORE UPDATE OR DELETE ON price_history
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
