-- A company that buys only its negotiated catalog: products without a price
-- list of its group are hidden from its buyers and cannot be bought.
ALTER TABLE companies ADD COLUMN catalog_restricted boolean NOT NULL DEFAULT false;
