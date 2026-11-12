-- B2B: the buyer's purchase order number on the order; a company can require
-- it, and can be limited to paying on credit (no card).
ALTER TABLE companies
    ADD COLUMN require_po  boolean NOT NULL DEFAULT false,
    ADD COLUMN credit_only boolean NOT NULL DEFAULT false;
ALTER TABLE orders ADD COLUMN po_number text NOT NULL DEFAULT '';
