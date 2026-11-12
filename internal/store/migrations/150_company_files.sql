-- Documents of a company (tax certificate, contract, credit application):
-- stored encrypted with the store's key; only the store and the company's
-- administrators read them.
CREATE TABLE company_files (
    tenant_id    text NOT NULL REFERENCES tenants(id),
    id           text NOT NULL,
    company_id   text NOT NULL,
    name         text NOT NULL,
    content_type text NOT NULL,
    bytes        bigint NOT NULL,
    uploaded_by  text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, company_id) REFERENCES companies (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX company_files_company ON company_files (tenant_id, company_id, created_at DESC);
ALTER TABLE company_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE company_files FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON company_files USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
