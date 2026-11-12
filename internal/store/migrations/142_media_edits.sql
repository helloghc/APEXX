-- Image edits are kept as a recipe applied to the untouched original, so any
-- edit can be changed or undone later.
ALTER TABLE media ADD COLUMN edits jsonb;
