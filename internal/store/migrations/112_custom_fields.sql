-- Custom fields: the store adds its own fields to customers, orders and
-- companies. Values that are not personal are searchable (segments, filters);
-- personal ones are stored encrypted with the store's key.
CREATE TABLE custom_values (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    resource   text NOT NULL CHECK (resource IN ('customer','order','company')),
    owner_id   text NOT NULL,
    data       jsonb NOT NULL DEFAULT '{}',
    secret     bytea,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, resource, owner_id)
);
CREATE INDEX custom_values_data ON custom_values USING gin (data);

ALTER TABLE custom_values ENABLE ROW LEVEL SECURITY;
ALTER TABLE custom_values FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON custom_values USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
