-- Orders paid later in cash (OXXO) or by bank transfer (SPEI): the order
-- keeps its stock until the voucher expires and is paid when the money arrives.
ALTER TABLE orders ADD COLUMN voucher jsonb;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_status_check;
ALTER TABLE payments ADD CONSTRAINT payments_status_check CHECK (status IN ('authorized','captured','failed','voided','refunded','pending'));
CREATE INDEX orders_voucher_pending ON orders (tenant_id) WHERE voucher IS NOT NULL AND status = 'pending_payment';
