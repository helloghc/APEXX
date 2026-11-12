-- A company pays its credit automatically: its administrator saves a card
-- and the store charges what is overdue on the due date, after a reminder.
ALTER TABLE companies
    ADD COLUMN autopay           boolean NOT NULL DEFAULT false,
    ADD COLUMN autopay_customer  text NOT NULL DEFAULT '',  -- provider tokens, never the card number
    ADD COLUMN autopay_method    text NOT NULL DEFAULT '',
    ADD COLUMN autopay_saved_at  timestamptz,
    ADD COLUMN autopay_last_try  timestamptz,
    ADD COLUMN autopay_failures  int NOT NULL DEFAULT 0,
    ADD COLUMN autopay_error     text NOT NULL DEFAULT '',
    ADD COLUMN reminder_days     int NOT NULL DEFAULT 3 CHECK (reminder_days BETWEEN 0 AND 30);
CREATE INDEX companies_autopay ON companies (tenant_id) WHERE autopay;
CREATE INDEX company_entries_due ON company_entries (tenant_id, due_at) WHERE kind = 'charge';
