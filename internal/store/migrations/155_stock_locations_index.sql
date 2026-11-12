-- Listing the locations that hold stock (product editor, CSV import) read
-- every stock row of the store: 100 000 rows for 50 000 products, 20 ms on
-- each product opened in the panel. With this index the list is walked one
-- location at a time (see DB.Locations).
CREATE INDEX stock_levels_tenant_location ON stock_levels (tenant_id, location_id);
