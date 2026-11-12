-- Cards a customer chose to keep for faster checkout: only provider tokens
-- and what the customer needs to recognise them (brand, last four digits).
CREATE TABLE customer_cards (
    tenant_id         text NOT NULL REFERENCES tenants(id),
    id                text NOT NULL,
    customer_id       text NOT NULL,
    provider          text NOT NULL,
    provider_customer text NOT NULL,
    payment_method    text NOT NULL,
    brand             text NOT NULL DEFAULT '',
    last4             text NOT NULL DEFAULT '',
    exp_month         int NOT NULL DEFAULT 0,
    exp_year          int NOT NULL DEFAULT 0,
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, customer_id, payment_method)
);
CREATE INDEX customer_cards_customer ON customer_cards (tenant_id, customer_id);
ALTER TABLE customer_cards ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_cards FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_cards USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));
