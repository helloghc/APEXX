-- Where an app shows itself in the panel: its own page and a block in the
-- order detail, both loaded in a sandboxed frame with a signed session.
ALTER TABLE apps ADD COLUMN admin_page text NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN order_block text NOT NULL DEFAULT '';
