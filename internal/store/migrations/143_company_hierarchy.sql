-- Company hierarchy: a subsidiary has a parent (up to three levels). It may
-- inherit the parent's terms, prices and rules, and may buy on the parent's
-- credit, which is then one pool for the parent and those subsidiaries.
ALTER TABLE companies ADD COLUMN parent_id text, ADD COLUMN shared_credit boolean NOT NULL DEFAULT false, ADD COLUMN inherit_terms boolean NOT NULL DEFAULT false;
ALTER TABLE companies ADD CONSTRAINT companies_parent_fk FOREIGN KEY (tenant_id, parent_id) REFERENCES companies (tenant_id, id);
ALTER TABLE companies ADD CONSTRAINT companies_parent_not_self CHECK (parent_id IS NULL OR parent_id <> id);
ALTER TABLE companies ADD CONSTRAINT companies_shared_needs_parent CHECK (NOT shared_credit OR parent_id IS NOT NULL);
ALTER TABLE companies ADD CONSTRAINT companies_inherit_needs_parent CHECK (NOT inherit_terms OR parent_id IS NOT NULL);
CREATE INDEX companies_parent ON companies (tenant_id, parent_id) WHERE parent_id IS NOT NULL;
