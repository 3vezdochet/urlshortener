-- +goose Up
CREATE TABLE link_health (
    code               TEXT PRIMARY KEY REFERENCES links(code) ON DELETE CASCADE,
    status             TEXT NOT NULL DEFAULT 'unknown',
    last_checked_at    TIMESTAMPTZ,
    next_check_at      TIMESTAMPTZ NOT NULL,
    consecutive_fails  INTEGER NOT NULL DEFAULT 0,
    last_status_code   INTEGER NOT NULL DEFAULT 0,
    last_error         TEXT NOT NULL DEFAULT ''
);

-- Backs ClaimDue's "find due checks" scan — without it, every checker
-- tick does a full table scan as link_health grows.
CREATE INDEX idx_link_health_next_check_at ON link_health (next_check_at);

-- +goose Down
DROP TABLE IF EXISTS link_health;
