-- name: ListRooms :many
SELECT * FROM rooms ORDER BY created_at;

-- name: CreateRoom :one
INSERT INTO rooms (account_id, name) VALUES ($1, $2) RETURNING *;

-- name: GetRoom :one
SELECT * FROM rooms WHERE id = $1;

-- name: UpdateRoom :one
UPDATE rooms SET
    name               = COALESCE(sqlc.narg(name), name),
    mode               = COALESCE(sqlc.narg(mode), mode),
    protection_enabled = COALESCE(sqlc.narg(protection_enabled), protection_enabled),
    power_allowed      = COALESCE(sqlc.narg(power_allowed), power_allowed)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: DeleteRoom :execrows
DELETE FROM rooms WHERE id = $1;
