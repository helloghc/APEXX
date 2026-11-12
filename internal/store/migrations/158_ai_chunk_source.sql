-- Document chunks (IA-2) cite where they come from: the document's title
-- and its address in the storefront. Product chunks leave them empty.
ALTER TABLE ai_chunks ADD COLUMN title text NOT NULL DEFAULT '';
ALTER TABLE ai_chunks ADD COLUMN url text NOT NULL DEFAULT '';
