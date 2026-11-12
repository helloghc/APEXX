-- The personalization the buyer chose on each order line (engraving, wrap,
-- finish), with what each one added to the price.
ALTER TABLE order_lines ADD COLUMN custom jsonb;
