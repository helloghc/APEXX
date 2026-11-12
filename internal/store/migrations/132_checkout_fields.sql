-- Answers to the store's checkout blocks (required checkboxes, extra fields).
ALTER TABLE orders ADD COLUMN checkout_fields jsonb;
