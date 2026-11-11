-- Transactional email: per-store sender and provider, editable templates and
-- a message queue that doubles as the delivery log. A message is inserted in
-- the same transaction as the change that caused it (transactional outbox),
-- then sent by a worker with retries.

CREATE TABLE notification_settings (
    tenant_id             text PRIMARY KEY REFERENCES tenants(id),
    provider              text NOT NULL DEFAULT 'outbox' CHECK (provider IN ('outbox','smtp','sendgrid')),
    sender_name           text NOT NULL DEFAULT '',
    sender_email          text NOT NULL DEFAULT '',
    reply_to              text NOT NULL DEFAULT '',
    config                bytea,                    -- encrypted provider credentials
    brand                 jsonb NOT NULL DEFAULT '{}',
    cart_reminder_minutes int NOT NULL DEFAULT 60 CHECK (cart_reminder_minutes BETWEEN 15 AND 10080),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE notification_templates (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    key         text NOT NULL,
    subject     text NOT NULL,
    blocks      jsonb NOT NULL,
    enabled     boolean NOT NULL DEFAULT true,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, key)
);

CREATE TABLE notification_messages (
    tenant_id    text NOT NULL REFERENCES tenants(id),
    id           text NOT NULL,
    template     text NOT NULL,
    status       text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','sent','failed','dead','skipped')),
    to_hash      bytea NOT NULL,
    payload      bytea NOT NULL,                    -- encrypted JSON: recipient, variables, rendered copy
    provider     text NOT NULL DEFAULT '',
    provider_ref text NOT NULL DEFAULT '',
    attempts     int NOT NULL DEFAULT 0,
    last_error   text NOT NULL DEFAULT '',
    next_attempt timestamptz NOT NULL DEFAULT now(),
    order_id     text,
    customer_id  text,
    dedupe_key   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    sent_at      timestamptz,
    PRIMARY KEY (tenant_id, id)
);
CREATE UNIQUE INDEX notification_messages_dedupe ON notification_messages (tenant_id, dedupe_key) WHERE dedupe_key IS NOT NULL;
CREATE INDEX notification_messages_due ON notification_messages (next_attempt) WHERE status IN ('queued','failed');
CREATE INDEX notification_messages_log ON notification_messages (tenant_id, created_at DESC, id);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['notification_settings','notification_templates','notification_messages']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (tenant_id = current_setting('app.tenant_id', true))
            WITH CHECK (tenant_id = current_setting('app.tenant_id', true))$p$, t);
    END LOOP;
END $$;
