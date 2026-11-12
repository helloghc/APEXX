-- B2B depth: approvals in levels with rules, a monthly cap per buyer,
-- company locations (branches) and the company's own administrators.

-- Each buyer: the level up to which they approve, their monthly cap on the
-- company's credit, their branch, whether they manage the company's buyers,
-- and who approves for them while they are away.
ALTER TABLE company_buyers
    ADD COLUMN approver_level  int NOT NULL DEFAULT 1 CHECK (approver_level BETWEEN 1 AND 5),
    ADD COLUMN monthly_limit   bigint NOT NULL DEFAULT 0 CHECK (monthly_limit >= 0),
    ADD COLUMN location_id     text NOT NULL DEFAULT '',
    ADD COLUMN admin           boolean NOT NULL DEFAULT false,
    ADD COLUMN substitute_hash bytea,
    ADD COLUMN away_until      date;

-- Rules: [{"minTotal":500000,"level":2,"category":"","location":""}].
-- approval_days: a request not decided in that time expires.
ALTER TABLE companies
    ADD COLUMN approval_rules jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN approval_days  int NOT NULL DEFAULT 7 CHECK (approval_days BETWEEN 1 AND 90);

CREATE TABLE company_locations (
    tenant_id     text NOT NULL REFERENCES tenants(id),
    id            text NOT NULL,
    company_id    text NOT NULL,
    name          text NOT NULL,
    price_group   text NOT NULL DEFAULT '',  -- '' = the company's prices
    monthly_limit bigint NOT NULL DEFAULT 0 CHECK (monthly_limit >= 0), -- cap on credit per month, 0 = none
    address       jsonb NOT NULL DEFAULT '{}', -- delivery address of the branch (a business, not a person)
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, company_id) REFERENCES companies(tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX company_locations_company ON company_locations (tenant_id, company_id);
ALTER TABLE company_locations ENABLE ROW LEVEL SECURITY;
ALTER TABLE company_locations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON company_locations USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- Who used the credit, and for which branch: monthly caps add these up.
ALTER TABLE company_entries
    ADD COLUMN buyer_hash  bytea,
    ADD COLUMN location_id text NOT NULL DEFAULT '';
CREATE INDEX company_entries_buyer ON company_entries (tenant_id, company_id, buyer_hash, created_at) WHERE buyer_hash IS NOT NULL;

-- A request climbs levels: level = approved so far, required_level = needed.
-- Each level gets its own link (the token is replaced when a level approves).
ALTER TABLE purchase_requests
    ADD COLUMN level          int NOT NULL DEFAULT 0,
    ADD COLUMN required_level int NOT NULL DEFAULT 1,
    ADD COLUMN step_level     int NOT NULL DEFAULT 1, -- level of the people the current link went to
    ADD COLUMN location_id    text NOT NULL DEFAULT '',
    ADD COLUMN reason         text NOT NULL DEFAULT '',
    ADD COLUMN decisions      jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN expires_at     timestamptz,
    ADD COLUMN buyer_hash     bytea;
ALTER TABLE purchase_requests DROP CONSTRAINT purchase_requests_status_check;
ALTER TABLE purchase_requests ADD CONSTRAINT purchase_requests_status_check CHECK (status IN ('pending','approved','rejected','expired','cancelled'));
CREATE INDEX purchase_requests_pending ON purchase_requests (tenant_id, expires_at) WHERE status = 'pending';
