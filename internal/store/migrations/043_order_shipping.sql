-- The order keeps the delivery option the shopper chose, the tax breakdown
-- and the number shown to the shopper (prefix + number at purchase time).
ALTER TABLE orders
    ADD COLUMN shipping_method jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN tax_detail      jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN display_number  text  NOT NULL DEFAULT '';
