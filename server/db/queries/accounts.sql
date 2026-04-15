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

-- name: AddAccountMember :exec
INSERT INTO account_members (account_id, user_id, role)
VALUES ($1, $2, $3);

-- name: ListAccountsForUser :many
SELECT a.* FROM accounts a
JOIN account_members m ON m.account_id = a.id
WHERE m.user_id = $1
ORDER BY a.created_at;

-- name: GetAccountMembership :one
SELECT role FROM account_members
WHERE account_id = $1 AND user_id = $2;
