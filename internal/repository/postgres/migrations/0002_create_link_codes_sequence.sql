-- +goose Up
-- Backs postgres.CodeGen: each nextval() gives a unique, gap-tolerant id
-- that gets base62-encoded into a short code. A dedicated sequence (rather
-- than a serial column) lets the code be reserved before the row exists.
CREATE SEQUENCE IF NOT EXISTS link_codes START WITH 1 INCREMENT BY 1;

-- +goose Down
DROP SEQUENCE IF EXISTS link_codes;
