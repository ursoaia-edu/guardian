-- name: CreateUser :one
INSERT INTO users (email, password_hash, name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: CreateAccount :one
INSERT INTO accounts (name, owner_user_id)
VALUES ($1, $2)
RETURNING *;

-- name: ListAccountMembers :many
SELECT u.id, u.email, u.name, m.role, m.created_at
FROM account_members m
JOIN users u ON u.id = m.user_id
ORDER BY CASE m.role WHEN 'owner' THEN 0 ELSE 1 END, u.email;

-- name: DeleteAccountMember :execrows
-- The owner row is deliberately unreachable here: an account with no owner has
-- nobody who can be billed or delete it, and ownership is not transferable in
-- v1 (see specs/2026-09-05-saas-design.md).
DELETE FROM account_members WHERE user_id = $1 AND role <> 'owner';

-- name: AddAccountMember :exec
INSERT INTO account_members (account_id, user_id, role)
VALUES ($1, $2, $3);
