-- tenant_plans holds a store's data like every tenant table: the same
-- isolation policy (the app role has no grant on it anyway; the operator
-- and the API read it with the system role).
CREATE POLICY tenant_isolation ON tenant_plans
    USING (tenant_id = current_setting('app.tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
