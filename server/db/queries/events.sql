-- name: RecordEvent :exec
INSERT INTO events (account_id, room_id, computer_id, type, payload)
VALUES ($1, $2, $3, $4, $5);

-- name: ListRecentEvents :many
SELECT * FROM events ORDER BY created_at DESC LIMIT $1;
