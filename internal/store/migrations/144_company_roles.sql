-- Roles inside a company: named sets of permissions (buy on credit, see the
-- credit, request quotes, approve at a level, manage people) given to its
-- members. A member without a role keeps the individual switches.
CREATE TABLE company_roles (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    company_id text NOT NULL,
    id         text NOT NULL,
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
    perms      jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, company_id, id),
    UNIQUE (tenant_id, company_id, name),
    FOREIGN KEY (tenant_id, company_id) REFERENCES companies (tenant_id, id) ON DELETE CASCADE
);
ALTER TABLE company_roles ENABLE ROW LEVEL SECURITY;
ALTER TABLE company_roles FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON company_roles USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
ALTER TABLE company_buyers ADD COLUMN role_id text;
ALTER TABLE company_buyers ADD CONSTRAINT company_buyers_role_fk FOREIGN KEY (tenant_id, company_id, role_id) REFERENCES company_roles (tenant_id, company_id, id) ON DELETE SET NULL (role_id);
