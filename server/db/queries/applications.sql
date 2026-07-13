-- name: ListRoomApplications :many
SELECT * FROM applications WHERE room_id = $1 ORDER BY name;

-- name: AddRoomApplication :one
INSERT INTO applications (account_id, room_id, name, list)
VALUES ($1, $2, $3, $4)
ON CONFLICT (room_id, name, list) DO UPDATE SET enabled = true
RETURNING *;

-- name: UpdateRoomApplication :one
-- The `enabled` column existed from the first migration and nothing could ever
-- clear it: an entry could be added and deleted, never switched off. Turning a
-- rule off for an afternoon without losing it is the ordinary case.
UPDATE applications SET enabled = COALESCE(sqlc.narg(enabled), enabled)
WHERE id = sqlc.arg(id) AND room_id = sqlc.arg(room_id)
RETURNING *;

-- name: DeleteRoomApplication :execrows
DELETE FROM applications WHERE id = $1 AND room_id = $2;
