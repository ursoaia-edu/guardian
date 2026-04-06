-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email             TEXT NOT NULL UNIQUE,
    password_hash     TEXT NOT NULL,
    name              TEXT NOT NULL DEFAULT '',
    email_verified_at TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The application role owns nothing and creates nothing; it is granted DML and
-- no more. These two statements live in a migration rather than in
-- db/init/01-roles.sql because init scripts run only when Docker initialises a
-- fresh volume — production creates the role by hand and would silently end up
-- with an application that cannot read its own tables.
--
-- ALTER DEFAULT PRIVILEGES is a persistent catalogue change: every table
-- created later by this same owner role (that is, by every later migration)
-- inherits the grant, so no migration after this one needs to think about it.
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO guardian_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO guardian_app;

-- +goose Down
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM guardian_app;
DROP TABLE users;
DROP EXTENSION IF EXISTS pgcrypto;
