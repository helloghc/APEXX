-- Labels bought from connected carriers, automatic tracking and prepaid
-- return labels.
ALTER TABLE fulfillments
    ADD COLUMN carrier_id      text NOT NULL DEFAULT '',
    ADD COLUMN label_url       text NOT NULL DEFAULT '',
    ADD COLUMN label_cost      bigint NOT NULL DEFAULT 0 CHECK (label_cost >= 0),
    ADD COLUMN tracking_status text NOT NULL DEFAULT '',
    ADD COLUMN tracked_at      timestamptz;
CREATE INDEX fulfillments_to_track ON fulfillments (tenant_id, tracked_at) WHERE status = 'shipped' AND carrier_id <> '';

ALTER TABLE returns
    ADD COLUMN carrier_id      text NOT NULL DEFAULT '',
    ADD COLUMN label_tracking  text NOT NULL DEFAULT '',
    ADD COLUMN label_url       text NOT NULL DEFAULT '',
    ADD COLUMN label_cost      bigint NOT NULL DEFAULT 0 CHECK (label_cost >= 0),
    ADD COLUMN requested_by    text NOT NULL DEFAULT 'store'; -- store | customer
