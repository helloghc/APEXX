-- Prospects from the public "Empezar" page: people who want to open a store and
-- asked to be contacted by sales. Platform-level (not tied to any tenant); the
-- public endpoint writes them with the system role, and sales follows up.
CREATE TABLE platform_leads (
    id         text PRIMARY KEY,
    name       text NOT NULL,
    email      text NOT NULL,
    phone      text NOT NULL DEFAULT '',
    company    text NOT NULL DEFAULT '',
    country    text NOT NULL DEFAULT '',
    industry   text NOT NULL DEFAULT '',   -- giro
    volume     text NOT NULL DEFAULT '',   -- monthly sales range, for the quote
    platform   text NOT NULL DEFAULT '',   -- what they use today
    message    text NOT NULL DEFAULT '',
    source     text NOT NULL DEFAULT 'empezar',
    status     text NOT NULL DEFAULT 'new',
    ip         text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX platform_leads_created ON platform_leads (created_at DESC);
