-- +goose Up
-- One digest table for both the "confirm your address" and the "reset your
-- password" links. They have the same shape — a single-use bearer token with
-- an expiry, tied to one user and one address — and splitting them would mean
-- two tables, two purges and two sets of the same mistakes.
--
-- No RLS, for the same reason sessions has none: the row carries no
-- account_id and is reached before any account scope exists. A reset link is
-- followed by somebody who is, by definition, not signed in.
CREATE TABLE email_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose    TEXT NOT NULL CHECK (purpose IN ('verify', 'reset')),
    -- The address the link was sent to, which is not necessarily the user's
    -- current one: somebody who changes their address must not have an old
    -- link confirm the new one.
    email      TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Every read is "this user's tokens of this purpose", when superseding them.
CREATE INDEX idx_email_tokens_user ON email_tokens(user_id, purpose);

-- +goose Down
DROP TABLE email_tokens;
