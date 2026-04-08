-- +goose Up
CREATE TABLE accounts (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                TEXT NOT NULL,
    owner_user_id       UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    plan                TEXT NOT NULL DEFAULT 'free',
    computer_limit      INTEGER NOT NULL DEFAULT 3,
    stripe_customer_id  TEXT NOT NULL DEFAULT '',
    subscription_status TEXT NOT NULL DEFAULT 'trialing',
    grace_until         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE account_members (
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('owner', 'admin')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, user_id)
);

CREATE INDEX idx_account_members_user ON account_members(user_id);

-- No GRANT here: migration 00001 set ALTER DEFAULT PRIVILEGES for this schema,
-- so both tables above are already granted to guardian_app on creation.

-- +goose Down
DROP TABLE account_members;
DROP TABLE accounts;
