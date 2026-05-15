-- name: CreateBindingToken :one
INSERT INTO binding_tokens (account_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetActiveBindingToken :one
SELECT * FROM binding_tokens
WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now();

-- name: RevokeAllBindingTokens :execrows
-- The kill switch for a leaked installer. There is no per-token variant because
-- there is nothing to select from yet — the cabinet lists tokens in a later
-- plan — and "my installer got out, invalidate it" is the whole of what a
-- customer needs today. Enrolled agents are unaffected: they hold their own
-- per-machine tokens by now.
UPDATE binding_tokens SET revoked_at = now()
WHERE revoked_at IS NULL;
