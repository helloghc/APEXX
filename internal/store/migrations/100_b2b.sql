-- B2B: companies that buy with their own price and on credit, and custom
-- data entities with public forms.

CREATE TABLE companies (
    tenant_id    text NOT NULL REFERENCES tenants(id),
    id           text NOT NULL,
    name         text NOT NULL,
    rfc          text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'active' CHECK (status IN ('active','suspended')),
    terms        text NOT NULL DEFAULT 'prepaid' CHECK (terms IN ('prepaid','net15','net30','net60')),
    credit_limit bigint NOT NULL DEFAULT 0 CHECK (credit_limit >= 0),
    discount_bps bigint NOT NULL DEFAULT 0 CHECK (discount_bps BETWEEN 0 AND 9000),
    note         text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id)
);

-- People who may buy for a company. A person belongs to one company per store.
CREATE TABLE company_buyers (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    company_id text NOT NULL,
    email_hash bytea NOT NULL,   -- keyed hash, to find the company of a signed-in shopper
    email      bytea NOT NULL,   -- encrypted, to show the list to the store
    PRIMARY KEY (tenant_id, email_hash),
    FOREIGN KEY (tenant_id, company_id) REFERENCES companies(tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX company_buyers_company ON company_buyers (tenant_id, company_id);

-- What each company owes: charges add, payments and refunds subtract.
CREATE TABLE company_entries (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    id         text NOT NULL,
    company_id text NOT NULL,
    kind       text NOT NULL CHECK (kind IN ('charge','payment','refund','release','adjust')),
    amount     bigint NOT NULL,
    order_id   text,
    due_at     timestamptz,
    note       text NOT NULL DEFAULT '',
    actor      text NOT NULL DEFAULT 'system',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, company_id) REFERENCES companies(tenant_id, id)
);
CREATE INDEX company_entries_company ON company_entries (tenant_id, company_id, created_at DESC);
CREATE INDEX company_entries_order ON company_entries (tenant_id, order_id) WHERE order_id IS NOT NULL;

-- Records of the store's own data entities (leads, warranties, wholesale requests…).
CREATE TABLE entity_records (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    entity     text NOT NULL,
    id         text NOT NULL,
    data       bytea NOT NULL,   -- encrypted JSON: forms usually carry personal data
    source     text NOT NULL DEFAULT 'admin',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, entity, id)
);
CREATE INDEX entity_records_recent ON entity_records (tenant_id, entity, created_at DESC);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['companies','company_buyers','company_entries','entity_records']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;
