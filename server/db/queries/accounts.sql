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
