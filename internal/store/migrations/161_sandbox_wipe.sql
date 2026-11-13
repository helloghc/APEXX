-- Append-only tables (audit log, order events, ledger, site revisions, price
-- history, storefront events) stay append-only for every production store.
-- A sandbox's own rows can be removed, and only by the sandbox engine (it
-- sets apexx.sandbox_wipe inside its transaction) when the sandbox is
-- refreshed or deleted. Updates are never allowed.
CREATE OR REPLACE FUNCTION audit_log_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' AND current_setting('apexx.sandbox_wipe', true) = 'on'
       AND EXISTS (SELECT 1 FROM tenants t WHERE t.id = OLD.tenant_id AND t.kind = 'sandbox') THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'audit_log is append-only';
END $$;

CREATE OR REPLACE FUNCTION analytics_events_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'analytics_events is append-only';
    END IF;
    IF current_setting('apexx.sandbox_wipe', true) = 'on'
       AND EXISTS (SELECT 1 FROM tenants t WHERE t.id = OLD.tenant_id AND t.kind = 'sandbox') THEN
        RETURN OLD;
    END IF;
    IF OLD.at > now() - interval '90 days' THEN
        RAISE EXCEPTION 'analytics_events are kept for 90 days';
    END IF;
    RETURN OLD;
END $$;
