-- Activity rollups: the operations screens count what happened (changes in
-- the last 24 hours, who did what in the audit log over 90 days). Counting
-- the raw rows took 0.6 s with 5.8 million outbox events and 0.3 s with a
-- million audit entries. A worker adds up only what is new since its mark;
-- a screen adds the rollup and the few rows after the mark, so the numbers
-- stay exact and cost milliseconds.
--
-- activity_rollup: per store, kind 'outbox' per hour, 'actor' and 'action'
-- per day (UTC).
CREATE TABLE activity_rollup (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    kind       text NOT NULL,
    bucket     timestamptz NOT NULL,
    key        text NOT NULL DEFAULT '',
    n          bigint NOT NULL,
    PRIMARY KEY (tenant_id, kind, bucket, key)
);
ALTER TABLE activity_rollup ENABLE ROW LEVEL SECURITY;
ALTER TABLE activity_rollup FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON activity_rollup
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- activity_marks: the last sequence added up per source ('outbox' for all
-- stores, 'audit:<store>' per store). System role only.
CREATE TABLE activity_marks (
    source  text PRIMARY KEY,
    seq     bigint NOT NULL
);
ALTER TABLE activity_marks ENABLE ROW LEVEL SECURITY;
ALTER TABLE activity_marks FORCE ROW LEVEL SECURITY;
