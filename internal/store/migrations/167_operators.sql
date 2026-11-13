-- Platform operators (superadmin): the people who run Apexx, not members of
-- any store. They set each store's plan. Created only from the command line
-- (`apexx operator create`), with two-step verification always on.
CREATE TABLE operators (
    id             text PRIMARY KEY,
    email          text NOT NULL UNIQUE,
    name           text NOT NULL,
    password_hash  text NOT NULL,
    totp_secret    bytea NOT NULL,          -- encrypted with the platform key
    totp_last_step bigint NOT NULL DEFAULT 0, -- a code is accepted once
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_login_at  timestamptz,
    disabled_at    timestamptz
);
CREATE TABLE operator_sessions (
    token_hash  bytea PRIMARY KEY,
    operator_id text NOT NULL REFERENCES operators(id) ON DELETE CASCADE,
    ip          text NOT NULL DEFAULT '',
    user_agent  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);
CREATE INDEX operator_sessions_expiry ON operator_sessions (expires_at);
-- Everything an operator does, kept apart from the stores' own activity
-- (the store also sees the change in its Activity).
CREATE TABLE operator_log (
    id          bigserial PRIMARY KEY,
    operator_id text NOT NULL,
    action      text NOT NULL,
    target      text NOT NULL DEFAULT '',
    data        jsonb NOT NULL DEFAULT '{}',
    ip          text NOT NULL DEFAULT '',
    at          timestamptz NOT NULL DEFAULT now()
);
-- Keys of the platform itself (wrapped with the master key, like a store's).
CREATE TABLE platform_keys (
    id      text PRIMARY KEY,
    wrapped bytea NOT NULL
);
