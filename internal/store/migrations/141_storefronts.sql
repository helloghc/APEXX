-- Several storefronts per store: each site has its own design and may open
-- in a market (its currency, prices and language); each domain may point to
-- a site and a market, so tienda.mx and tienda.us show the same catalog in
-- pesos or dollars.
ALTER TABLE sites ADD COLUMN market text NOT NULL DEFAULT '', ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE domains ADD COLUMN market text NOT NULL DEFAULT '';
-- A storefront is archived, never deleted: its published history stays.
ALTER TABLE sites ADD COLUMN archived_at timestamptz;
