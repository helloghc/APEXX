-- Sandbox per store (docs/fases/plan-sandbox.md): a second, complete store
-- linked to the production one. It is a tenant like any other, so every
-- table, query and row level policy works unchanged; kind and parent_id say
-- what it is.
ALTER TABLE tenants ADD COLUMN kind text NOT NULL DEFAULT 'production' CHECK (kind IN ('production', 'sandbox'));
ALTER TABLE tenants ADD COLUMN parent_id text REFERENCES tenants(id);
ALTER TABLE tenants ADD CONSTRAINT tenants_sandbox_parent CHECK ((kind = 'sandbox') = (parent_id IS NOT NULL));

-- sandboxes: one per production store, with its state and the facts the
-- panel shows. System role only (the API reads it for the signed-in store).
CREATE TABLE sandboxes (
    id            text PRIMARY KEY REFERENCES tenants(id),
    parent_id     text NOT NULL UNIQUE REFERENCES tenants(id),
    status        text NOT NULL CHECK (status IN ('copying', 'ready', 'failed', 'deleting')),
    data_mode     text NOT NULL DEFAULT 'none' CHECK (data_mode IN ('none', 'demo')),
    progress      jsonb NOT NULL DEFAULT '{}',
    error         text NOT NULL DEFAULT '',
    created_by    text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    copied_at     timestamptz,
    copy_ms       integer NOT NULL DEFAULT 0,
    last_used_at  timestamptz NOT NULL DEFAULT now(),
    warned_at     timestamptz
);
ALTER TABLE sandboxes ENABLE ROW LEVEL SECURITY;
ALTER TABLE sandboxes FORCE ROW LEVEL SECURITY;

-- sandbox_base: the fingerprint of every publishable row at the moment of
-- the copy (or of the last publish). Publishing compares three versions of
-- each row: the base, the sandbox and production. A row both sides changed
-- is a conflict, never overwritten in silence.
CREATE TABLE sandbox_base (
    sandbox_id  text NOT NULL REFERENCES sandboxes(id) ON DELETE CASCADE,
    tbl         text NOT NULL,
    pk          text NOT NULL,
    hash        text NOT NULL,
    PRIMARY KEY (sandbox_id, tbl, pk)
);
ALTER TABLE sandbox_base ENABLE ROW LEVEL SECURITY;
ALTER TABLE sandbox_base FORCE ROW LEVEL SECURITY;

-- sandbox_publishes: each publish to production, with the exact rows it
-- replaced (sandbox_publish_rows.before; null: the row did not exist), so
-- a revert puts production back exactly. Kept after the sandbox is deleted.
CREATE TABLE sandbox_publishes (
    id           text PRIMARY KEY,
    parent_id    text NOT NULL REFERENCES tenants(id),
    sandbox_id   text NOT NULL,
    areas        jsonb NOT NULL,
    summary      jsonb NOT NULL,
    published_by text NOT NULL,
    published_at timestamptz NOT NULL DEFAULT now(),
    reverted_by  text,
    reverted_at  timestamptz
);
CREATE INDEX sandbox_publishes_parent ON sandbox_publishes (parent_id, published_at DESC);
CREATE TABLE sandbox_publish_rows (
    publish_id  text NOT NULL REFERENCES sandbox_publishes(id) ON DELETE CASCADE,
    seq         integer NOT NULL,
    tbl         text NOT NULL,
    pk          text NOT NULL,
    before      jsonb,
    after_hash  text NOT NULL DEFAULT '', -- the row as published ('' : deleted)
    base_before text,                     -- the sandbox's base before the publish
    PRIMARY KEY (publish_id, seq)
);
ALTER TABLE sandbox_publishes ENABLE ROW LEVEL SECURITY;
ALTER TABLE sandbox_publishes FORCE ROW LEVEL SECURITY;
ALTER TABLE sandbox_publish_rows ENABLE ROW LEVEL SECURITY;
ALTER TABLE sandbox_publish_rows FORCE ROW LEVEL SECURITY;
