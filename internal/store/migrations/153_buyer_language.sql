-- The language a buyer used in the store, so the emails about their order
-- (and the access codes they ask for) arrive in that language.
ALTER TABLE orders ADD COLUMN lang text NOT NULL DEFAULT 'es';
ALTER TABLE customers ADD COLUMN lang text NOT NULL DEFAULT 'es';
