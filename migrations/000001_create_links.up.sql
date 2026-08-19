CREATE TABLE IF NOT EXISTS links (
    code         TEXT PRIMARY KEY,
    original_url TEXT NOT NULL,
    owner_id     TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ,
    active       BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE INDEX IF NOT EXISTS idx_links_owner_id ON links (owner_id) WHERE owner_id <> '';

CREATE SEQUENCE IF NOT EXISTS link_code_seq START 1;
