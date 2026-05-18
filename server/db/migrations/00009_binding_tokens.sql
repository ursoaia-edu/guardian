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
-- account, so the lookup by hash has to be readable unscoped. Be precise about
-- how big the hole is, as with the accounts pre-scope policy: this permits an
-- unscoped connection to enumerate EVERY account's token rows — ids, account
-- ids, lifetimes and digests — not merely to look one up by hash. What it does
-- not permit is using any of them: the digests are SHA-256 of 32 random bytes
-- and enrolment needs the plaintext. GetActiveBindingToken is the only query in
-- the server allowed to run unscoped against this table, and it is keyed by
-- digest. It is FOR SELECT alone, so the account-wide revoke UPDATE stays
-- confined by the isolation policy above.
CREATE POLICY binding_tokens_lookup ON binding_tokens
    FOR SELECT USING (current_account_id() IS NULL);

-- +goose Down
DROP TABLE binding_tokens;
