-- name: ListRoomApplications :many
SELECT * FROM applications WHERE room_id = $1 ORDER BY name;

-- name: AddRoomApplication :one
INSERT INTO applications (account_id, room_id, name, list)
VALUES ($1, $2, $3, $4)
ON CONFLICT (room_id, name, list) DO UPDATE SET enabled = true
RETURNING *;

-- name: DeleteRoomApplication :execrows
DELETE FROM applications WHERE id = $1 AND room_id = $2;
