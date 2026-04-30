-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at, ip, user_agent)
VALUES ($1, $2, $3, $4, $5);

-- name: GetSession :one
SELECT * FROM sessions WHERE token_hash = $1 AND expires_at > now();

-- name: TouchSession :exec
-- Sliding renewal, self-throttled. Renewing on every authenticated request
-- turns every GET into a write: WAL traffic proportional to all API traffic,
-- and a row lock that serialises concurrent requests sharing one session. At a
-- 30-day TTL, renewing at most hourly is indistinguishable to the user.
UPDATE sessions SET last_used_at = now(), expires_at = $2
WHERE token_hash = $1 AND last_used_at < now() - interval '1 hour';

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;
