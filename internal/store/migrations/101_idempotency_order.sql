-- A purchase interrupted by a crash must not stay "in progress" forever: the
-- key remembers the order it created, so a retry can be answered from it.
ALTER TABLE idempotency_keys ADD COLUMN order_id text;
