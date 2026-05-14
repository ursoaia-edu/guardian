-- name: CreateBindingToken :one
INSERT INTO binding_tokens (account_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetActiveBindingToken :one
SELECT * FROM binding_tokens
WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now();
