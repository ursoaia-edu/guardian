-- +goose Up
CREATE TABLE binding_tokens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);

ALTER TABLE binding_tokens ENABLE ROW LEVEL SECURITY;
CREATE POLICY binding_tokens_isolation ON binding_tokens
    USING (account_id = current_account_id())
    WITH CHECK (account_id = current_account_id());
-- Enrollment arrives with no account scope: the token itself is what names the
-- account, so the lookup by hash must be readable unscoped. It reveals nothing
-- without the 32-byte secret.
CREATE POLICY binding_tokens_lookup ON binding_tokens
    FOR SELECT USING (current_account_id() IS NULL);

-- +goose Down
DROP TABLE binding_tokens;
