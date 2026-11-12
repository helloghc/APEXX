-- AI foundations (plan IA-0). Every table holds store data, so every table
-- has row level security forced, like the other tenant tables.
--
-- ai_chunks: text that is searched by meaning, with its vector. A product is
-- one chunk (chunk_no 0); long documents (IA-2) are several. The vector is
-- int8, normalized, packed as bytea: the same form the in-memory index
-- uses, so loading it is a copy. source_hash says which text the vector was
-- made from: when the product changes, the hash changes and it is embedded
-- again; when it is deleted, its chunks go in the same transaction.
CREATE TABLE ai_chunks (
    tenant_id    text NOT NULL REFERENCES tenants(id),
    source_kind  text NOT NULL,             -- product, page, policy, doc…
    source_id    text NOT NULL,
    chunk_no     int  NOT NULL DEFAULT 0,
    body         text NOT NULL,
    context      text NOT NULL DEFAULT '', -- contextual chunking (IA-2)
    model        text NOT NULL,
    vec          bytea NOT NULL,
    source_hash  bytea NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, source_kind, source_id, chunk_no)
);

-- kg_edges: the knowledge graph (IA-4). Edges carry their source (rule,
-- model, merchant) and a status: proposed edges show only after approval.
CREATE TABLE kg_edges (
    tenant_id   text NOT NULL REFERENCES tenants(id),
    id          text NOT NULL,
    src_kind    text NOT NULL,
    src_id      text NOT NULL,
    rel         text NOT NULL,              -- accessory_of, compatible_with, substitute_of…
    dst_kind    text NOT NULL,
    dst_id      text NOT NULL,
    confidence  real NOT NULL DEFAULT 1,
    source      text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT 'proposed', -- proposed, approved, rejected
    created_at  timestamptz NOT NULL DEFAULT now(),
    decided_by  text NOT NULL DEFAULT '',
    decided_at  timestamptz,
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, src_kind, src_id, rel, dst_kind, dst_id)
);
CREATE INDEX kg_edges_dst ON kg_edges (tenant_id, dst_kind, dst_id);

-- ai_suggestions: what an AI feature proposed, the value before, who
-- applied it and how it is undone.
CREATE TABLE ai_suggestions (
    tenant_id       text NOT NULL REFERENCES tenants(id),
    id              text NOT NULL,
    feature         text NOT NULL,
    target_kind     text NOT NULL,
    target_id       text NOT NULL,
    proposal        jsonb NOT NULL,
    before          jsonb,
    status          text NOT NULL DEFAULT 'proposed', -- proposed, applied, rejected, undone
    model           text NOT NULL DEFAULT '',
    prompt_version  text NOT NULL DEFAULT '',
    sources         jsonb NOT NULL DEFAULT '[]',
    created_at      timestamptz NOT NULL DEFAULT now(),
    decided_by      text NOT NULL DEFAULT '',
    decided_at      timestamptz,
    PRIMARY KEY (tenant_id, id)
);
CREATE INDEX ai_suggestions_target ON ai_suggestions (tenant_id, target_kind, target_id);

-- ai_usage: tokens and cost per store, day, feature and model, for budgets.
CREATE TABLE ai_usage (
    tenant_id      text NOT NULL REFERENCES tenants(id),
    day            date NOT NULL,
    feature        text NOT NULL,
    model          text NOT NULL,
    requests       bigint NOT NULL DEFAULT 0,
    input_tokens   bigint NOT NULL DEFAULT 0,
    output_tokens  bigint NOT NULL DEFAULT 0,
    cost_micros    bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, day, feature, model)
);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['ai_chunks','kg_edges','ai_suggestions','ai_usage']
    LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I USING (tenant_id = current_setting(''app.tenant_id'', true)) WITH CHECK (tenant_id = current_setting(''app.tenant_id'', true))', t);
    END LOOP;
END $$;
