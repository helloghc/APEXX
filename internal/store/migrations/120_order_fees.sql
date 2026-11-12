-- Extra charges of an order (handling, packaging, small order) and import
-- duties, kept one by one and in the total.
ALTER TABLE orders ADD COLUMN fees bigint NOT NULL DEFAULT 0 CHECK (fees >= 0);
ALTER TABLE orders ADD COLUMN fee_lines jsonb NOT NULL DEFAULT '[]';
ALTER TABLE orders DROP CONSTRAINT orders_check;
ALTER TABLE orders ADD CONSTRAINT orders_check CHECK (grand_total = items_total - discounts + shipping + tax_adjust + fees);

-- Customs data of a product: tariff code and country of origin.
ALTER TABLE product_logistics ADD COLUMN hs_code text NOT NULL DEFAULT '';
ALTER TABLE product_logistics ADD COLUMN origin char(2);
