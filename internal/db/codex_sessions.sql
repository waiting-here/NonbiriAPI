CREATE TABLE IF NOT EXISTS agent_codex_session (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    revision INTEGER NOT NULL CHECK (revision >= 1),
    encrypted_credentials TEXT NOT NULL DEFAULT '' CHECK(length(encrypted_credentials)<=524288),
    account_id TEXT NOT NULL DEFAULT '' CHECK(length(CAST(account_id AS BLOB))<=512),
    account_label TEXT NOT NULL DEFAULT '' CHECK(length(CAST(account_label AS BLOB))<=512),
    expires_at INTEGER NOT NULL DEFAULT 0 CHECK(expires_at>=0),
    updated_at INTEGER NOT NULL CHECK(updated_at>=0)
) STRICT;
