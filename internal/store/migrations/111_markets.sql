-- Markets: a company can be exempt from tax (until the date of its
-- certificate, if it has one).
ALTER TABLE companies
    ADD COLUMN tax_exempt       boolean NOT NULL DEFAULT false,
    ADD COLUMN tax_exempt_until date;

-- Tax added on top (markets whose prices exclude tax) or taken out (exempt
-- buyers) is part of the total, so the total still adds up exactly.
ALTER TABLE orders ADD COLUMN tax_adjust bigint NOT NULL DEFAULT 0;
ALTER TABLE orders DROP CONSTRAINT orders_check;
ALTER TABLE orders ADD CONSTRAINT orders_check CHECK (grand_total = items_total - discounts + shipping + tax_adjust);
