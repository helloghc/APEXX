-- Comments per line of a quote, from the buyer or the store, so a question
-- about one product does not get lost in the general messages.
CREATE TABLE quote_comments (
    tenant_id  text NOT NULL REFERENCES tenants(id),
    id         text NOT NULL,
    quote_id   text NOT NULL,
    sku        text NOT NULL,
    who        text NOT NULL CHECK (who IN ('buyer','store')),
    author     text NOT NULL DEFAULT '',
    body       bytea NOT NULL,           -- encrypted: may carry personal details
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, quote_id) REFERENCES quotes (tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX quote_comments_quote ON quote_comments (tenant_id, quote_id, created_at);
ALTER TABLE quote_comments ENABLE ROW LEVEL SECURITY;
ALTER TABLE quote_comments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON quote_comments USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
