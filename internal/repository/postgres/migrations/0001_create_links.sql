-- +goose Up
CREATE TABLE links (
    code         TEXT PRIMARY KEY,
    original_url TEXT NOT NULL,
    owner_id     TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ,
    active       BOOLEAN NOT NULL DEFAULT true
);

-- Partial index: most links belong to nobody in particular (anonymous
-- shortening); indexing only owned links keeps it small and useful for the
-- "my links" listing query.
CREATE INDEX idx_links_owner_id ON links (owner_id) WHERE owner_id <> '';

-- +goose Down
DROP TABLE IF EXISTS links;
