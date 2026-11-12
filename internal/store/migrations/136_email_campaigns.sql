-- Email campaigns to a customer group (only those with marketing consent),
-- sent by a worker, with the sales they bring attributed to them.
CREATE TABLE email_campaigns (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    group_id    text NOT NULL DEFAULT '',     -- '' = everyone with consent
    subject     text NOT NULL,
    blocks      jsonb NOT NULL,
    link        text NOT NULL DEFAULT '',     -- where the button leads (store path)
    status      text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','scheduled','sending','sent')),
    send_at     timestamptz,
    sent_count  int NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now(),
    sent_at     timestamptz,
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX email_campaigns_due ON email_campaigns (send_at) WHERE status IN ('scheduled','sending');
ALTER TABLE email_campaigns ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_campaigns FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON email_campaigns USING (tenant_id = current_setting('app.tenant_id', true)) WITH CHECK (tenant_id = current_setting('app.tenant_id', true));

-- The campaign whose link brought the order (last click within 7 days).
ALTER TABLE orders ADD COLUMN email_campaign_id text;
CREATE INDEX orders_email_campaign ON orders (tenant_id, email_campaign_id) WHERE email_campaign_id IS NOT NULL;
