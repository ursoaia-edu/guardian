-- name: CreateEmailToken :exec
INSERT INTO email_tokens (token_hash, user_id, purpose, email, expires_at)
VALUES ($1, $2, $3, $4, $5);

-- name: DeleteEmailTokensFor :exec
-- Minting supersedes: a fresh link invalidates the previous one, so a mailbox
-- never holds two working links to the same door.
DELETE FROM email_tokens WHERE user_id = $1 AND purpose = $2;

-- name: ConsumeEmailToken :one
-- Marks the token used and returns it, in one statement. Two statements would
-- be a race: two clicks on the same link, milliseconds apart, would both find
-- it unused. The WHERE clause carries every condition, so a spent, expired or
-- unknown token all return no rows and are answered identically.
UPDATE email_tokens SET used_at = now()
WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > now()
RETURNING user_id, email;

-- name: MarkEmailVerified :execrows
-- Only when the address still matches the one the link was sent to, and only
-- when it is not already verified.
UPDATE users SET email_verified_at = now()
WHERE id = $1 AND email = $2 AND email_verified_at IS NULL;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: SetPassword :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: DeleteSessionsForUser :execrows
-- Every session, for a completed reset.
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteOtherSessionsForUser :execrows
-- Every session except the one asking, for a password change from inside the
-- cabinet: the person doing it should not be signed out by their own action.
DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2;

-- name: PurgeExpiredEmailTokens :execrows
-- Spent or expired, plus a day of grace so a "my link says it is invalid"
-- question can still be answered by looking.
DELETE FROM email_tokens WHERE expires_at < now() - interval '1 day';
