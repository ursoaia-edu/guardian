-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, expires_at, ip, user_agent)
VALUES ($1, $2, $3, $4, $5);

-- name: GetSession :one
SELECT * FROM sessions WHERE token_hash = $1 AND expires_at > now();

-- name: TouchSession :exec
-- Sliding renewal, self-throttled, under an absolute ceiling.
--
-- Renewing on every authenticated request turns every GET into a write: WAL
-- traffic proportional to all API traffic, and a row lock that serialises
-- concurrent requests sharing one session. At a 30-day TTL, renewing at most
-- hourly is indistinguishable to the user.
--
-- LEAST(..., created_at + 1 year) is the ceiling. Without it the renewal is
-- unbounded: a session used once a week never expires, so a token stolen from
-- a device that stays in use is good forever, and "sign out everywhere" is the
-- only revocation that exists. A year is long enough that no parent is thrown
-- out of the app by it and short enough that an abandoned token dies.
UPDATE sessions SET
    last_used_at = now(),
    expires_at   = LEAST(sqlc.arg(expires_at)::timestamptz, created_at + interval '1 year')
WHERE token_hash = sqlc.arg(token_hash) AND last_used_at < now() - interval '1 hour';

-- name: DeleteExpiredSessions :execrows
-- Expired rows are already unusable (GetSession filters on expires_at), so
-- this is housekeeping, not security: without it the table only grows. The
-- day of grace keeps a row around long enough to be visible in a "your
-- session expired" investigation.
DELETE FROM sessions WHERE expires_at < now() - interval '1 day';

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;
