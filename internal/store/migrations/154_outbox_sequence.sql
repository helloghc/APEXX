-- The outbox id comes from a sequence when the row is inserted, but
-- transactions commit in any order. A reader that follows "id > cursor" can
-- pass an event whose transaction commits later with a smaller id, and never
-- see it: with many writers at once, the store kept showing products that
-- had been deleted. A single sequencer now numbers committed events (seq),
-- one run at a time; an event that commits late gets a later number. Readers
-- follow seq, so nothing can appear behind them.
ALTER TABLE outbox ADD COLUMN seq bigint;
UPDATE outbox SET seq = id;
CREATE SEQUENCE outbox_order;
SELECT setval('outbox_order', GREATEST((SELECT max(id) FROM outbox), 1));
CREATE UNIQUE INDEX outbox_seq ON outbox (seq);
CREATE INDEX outbox_unsequenced ON outbox (id) WHERE seq IS NULL;
CREATE INDEX outbox_tenant_seq ON outbox (tenant_id, seq);
