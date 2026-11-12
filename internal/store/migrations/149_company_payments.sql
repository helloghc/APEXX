-- Payment methods a company (or one of its branches) may use: card, OXXO,
-- SPEI and company credit. Empty means every method the store offers.
ALTER TABLE companies ADD COLUMN allowed_payments text[] NOT NULL DEFAULT '{}';
ALTER TABLE company_locations ADD COLUMN allowed_payments text[] NOT NULL DEFAULT '{}';
