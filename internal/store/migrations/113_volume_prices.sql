-- Volume pricing: a fixed-price list can set a different unit price from a
-- quantity upward ($100 from 1 piece, $90 from 12).
ALTER TABLE price_list_items ADD COLUMN min_qty bigint NOT NULL DEFAULT 1 CHECK (min_qty >= 1);
ALTER TABLE price_list_items DROP CONSTRAINT price_list_items_pkey;
ALTER TABLE price_list_items ADD PRIMARY KEY (tenant_id, list_id, sku, min_qty);
